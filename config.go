package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr          string
	UpstreamBaseURL     string
	ProxyAPIKey         string
	UpstreamAPIKeys     []string
	MaxRequestBodyBytes int64
	// RetryExhaustedAfter is how long a key stays exhausted before it becomes
	// eligible for an automatic retry probe. Zero disables the cooldown so
	// exhausted keys are retried on the very next request (client backoff is the
	// only pacing).
	RetryExhaustedAfter time.Duration
	// UpstreamResponseHeaderTimeout is how long the upstream HTTP client waits
	// for a response's headers. LLM upstreams can spend a long time before the
	// first body bytes (thinking/pre-processing for non-streaming completions),
	// so the default may need raising. Zero disables the timeout entirely.
	UpstreamResponseHeaderTimeout time.Duration
	ConfigSourcePath              string

	// KeyNames labels UpstreamAPIKeys by position (OPENCODE_GO_KEY_NAMES); the
	// key value itself is never shown. Missing entries mean an empty name.
	KeyNames []string
	// UsageURL is the OpenCode Go usage endpoint queried by /admin/keys and /status.
	UsageURL string
	// UsageCacheTTL is how long /admin/keys and /status reuse a usage lookup per key.
	UsageCacheTTL time.Duration

	SMTP SMTPConfig

	// Tiers are extra free upstreams for failover; the upstream above is the
	// implicit tier 0 and is not listed here.
	Tiers []TierConfig
}

type TierConfig struct {
	Name, BaseURL, APIKey string
	Free                  bool
	Styles                []APIStyle
	Models                []TierModel
}

// TierModel is kept in a slice (file order) so a repeated key survives to validateConfig.
type TierModel struct {
	Name, ID string
	Context  int
	Tools    bool
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       string
	TLS      bool
	StartTLS bool
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	if path, ok, err := resolveConfigPath(); err != nil {
		return Config{}, err
	} else if ok {
		fileCfg, err := loadYAMLConfig(path)
		if err != nil {
			return Config{}, err
		}
		mergeConfig(&cfg, fileCfg)
		cfg.ConfigSourcePath = path
	}
	applyEnvOverrides(&cfg)
	return cfg, validateConfig(cfg)
}

func defaultConfig() Config {
	return Config{ListenAddr: ":8080", UpstreamBaseURL: "https://opencode.ai/zen/go/v1", MaxRequestBodyBytes: 20 << 20, RetryExhaustedAfter: 5 * time.Minute, UpstreamResponseHeaderTimeout: 30 * time.Second, UsageURL: defaultUsageURL, UsageCacheTTL: defaultUsageCacheTTL, SMTP: SMTPConfig{Port: 25}}
}

func resolveConfigPath() (string, bool, error) {
	if explicit := strings.TrimSpace(os.Getenv("SWITCHBOARD_GO_CONFIG")); explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", false, fmt.Errorf("read SWITCHBOARD_GO_CONFIG: %w", err)
		}
		return explicit, true, nil
	}
	home, _ := os.UserHomeDir()
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".config", "switchboard-go", "config.yaml"))
	}
	paths = append(paths, "/etc/switchboard-go/config.yaml")
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, true, nil
		}
	}
	return "", false, nil
}

type yamlConfig struct {
	Server struct {
		ListenAddr  string `yaml:"listen_addr"`
		ProxyAPIKey string `yaml:"proxy_api_key"`
	} `yaml:"server"`
	Upstream struct {
		BaseURL               string   `yaml:"base_url"`
		APIKeys               []string `yaml:"api_keys"`
		RetryExhaustedAfter   string   `yaml:"retry_exhausted_after"`
		ResponseHeaderTimeout string   `yaml:"response_header_timeout"`
	} `yaml:"upstream"`
	SMTP struct {
		Host     string `yaml:"host"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		From     string `yaml:"from"`
		To       string `yaml:"to"`
		Port     int    `yaml:"port"`
		TLS      bool   `yaml:"tls"`
		StartTLS bool   `yaml:"starttls"`
	} `yaml:"smtp"`
	Limits struct {
		MaxRequestBodyBytes int64 `yaml:"max_request_body_bytes"`
	} `yaml:"limits"`
	Tiers []yamlTier `yaml:"tiers"`
}

type yamlTier struct {
	Name    string    `yaml:"name"`
	BaseURL string    `yaml:"base_url"`
	APIKey  string    `yaml:"api_key"`
	Free    bool      `yaml:"free"`
	Styles  []string  `yaml:"styles"`
	Models  yaml.Node `yaml:"models"`
}

func convertTiers(in []yamlTier) ([]TierConfig, error) {
	var out []TierConfig
	for i, yt := range in {
		t := TierConfig{Name: yt.Name, BaseURL: yt.BaseURL, APIKey: yt.APIKey, Free: yt.Free}
		for _, s := range yt.Styles {
			switch s {
			case "openai":
				t.Styles = append(t.Styles, APIStyleOpenAI)
			case "anthropic":
				t.Styles = append(t.Styles, APIStyleAnthropic)
			default:
				t.Styles = append(t.Styles, APIStyle(-1)) // validateConfig rejects it
			}
		}
		if len(t.Styles) == 0 {
			t.Styles = []APIStyle{APIStyleOpenAI}
		}
		switch n := &yt.Models; n.Kind {
		case 0: // models absent; validateConfig reports it
		case yaml.MappingNode:
			for j := 0; j+1 < len(n.Content); j += 2 {
				var v struct {
					ID      string `yaml:"id"`
					Context int    `yaml:"context"`
					Tools   bool   `yaml:"tools"`
				}
				if err := n.Content[j+1].Decode(&v); err != nil {
					return nil, fmt.Errorf("parse tiers[%d].models: %w", i, err)
				}
				if v.ID == "" {
					v.ID = n.Content[j].Value
				}
				t.Models = append(t.Models, TierModel{Name: n.Content[j].Value, ID: v.ID, Context: v.Context, Tools: v.Tools})
			}
		default:
			return nil, fmt.Errorf("parse tiers[%d].models: must be a map of model name to object", i)
		}
		out = append(out, t)
	}
	return out, nil
}

func loadYAMLConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file: %w", err)
	}
	var yc yamlConfig
	if err := yaml.Unmarshal(b, &yc); err != nil {
		return Config{}, fmt.Errorf("parse config file: %w", err)
	}
	// -1 marks "not present in file" so mergeConfig can tell an explicit 0
	// (disable cooldown) apart from an omitted value (keep the default).
	retry := time.Duration(-1)
	if s := strings.TrimSpace(yc.Upstream.RetryExhaustedAfter); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return Config{}, fmt.Errorf("parse retry_exhausted_after: %w", err)
		}
		if d < 0 {
			return Config{}, fmt.Errorf("retry_exhausted_after must be >= 0")
		}
		retry = d
	}
	headerTimeout := time.Duration(-1)
	if s := strings.TrimSpace(yc.Upstream.ResponseHeaderTimeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return Config{}, fmt.Errorf("parse response_header_timeout: %w", err)
		}
		if d < 0 {
			return Config{}, fmt.Errorf("response_header_timeout must be >= 0")
		}
		headerTimeout = d
	}
	tiers, err := convertTiers(yc.Tiers)
	if err != nil {
		return Config{}, err
	}
	return Config{Tiers: tiers, ListenAddr: yc.Server.ListenAddr, UpstreamBaseURL: yc.Upstream.BaseURL, ProxyAPIKey: yc.Server.ProxyAPIKey, UpstreamAPIKeys: yc.Upstream.APIKeys, MaxRequestBodyBytes: yc.Limits.MaxRequestBodyBytes, RetryExhaustedAfter: retry, UpstreamResponseHeaderTimeout: headerTimeout, SMTP: SMTPConfig{Host: yc.SMTP.Host, Port: yc.SMTP.Port, Username: yc.SMTP.Username, Password: yc.SMTP.Password, From: yc.SMTP.From, To: yc.SMTP.To, TLS: yc.SMTP.TLS, StartTLS: yc.SMTP.StartTLS}}, nil
}

func mergeConfig(dst *Config, src Config) {
	if src.ListenAddr != "" {
		dst.ListenAddr = src.ListenAddr
	}
	if src.UpstreamBaseURL != "" {
		dst.UpstreamBaseURL = src.UpstreamBaseURL
	}
	if src.ProxyAPIKey != "" {
		dst.ProxyAPIKey = src.ProxyAPIKey
	}
	if len(src.UpstreamAPIKeys) > 0 {
		dst.UpstreamAPIKeys = append([]string(nil), src.UpstreamAPIKeys...)
	}
	if src.MaxRequestBodyBytes > 0 {
		dst.MaxRequestBodyBytes = src.MaxRequestBodyBytes
	}
	if src.RetryExhaustedAfter >= 0 {
		dst.RetryExhaustedAfter = src.RetryExhaustedAfter
	}
	if src.UpstreamResponseHeaderTimeout >= 0 {
		dst.UpstreamResponseHeaderTimeout = src.UpstreamResponseHeaderTimeout
	}
	if src.SMTP.Host != "" {
		dst.SMTP.Host = src.SMTP.Host
	}
	if src.SMTP.Port != 0 {
		dst.SMTP.Port = src.SMTP.Port
	}
	if src.SMTP.Username != "" {
		dst.SMTP.Username = src.SMTP.Username
	}
	if src.SMTP.Password != "" {
		dst.SMTP.Password = src.SMTP.Password
	}
	if src.SMTP.From != "" {
		dst.SMTP.From = src.SMTP.From
	}
	if src.SMTP.To != "" {
		dst.SMTP.To = src.SMTP.To
	}
	if len(src.Tiers) > 0 {
		dst.Tiers = src.Tiers
	}
	dst.SMTP.TLS = src.SMTP.TLS || dst.SMTP.TLS
	dst.SMTP.StartTLS = src.SMTP.StartTLS || dst.SMTP.StartTLS
}

func applyEnvOverrides(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("LISTEN_ADDR")); v != "" {
		cfg.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("UPSTREAM_BASE_URL")); v != "" {
		cfg.UpstreamBaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("PROXY_API_KEY")); v != "" {
		cfg.ProxyAPIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("OPENCODE_GO_API_KEYS")); v != "" {
		var keys []string
		for _, k := range strings.Split(v, ",") {
			if s := strings.TrimSpace(k); s != "" {
				keys = append(keys, s)
			}
		}
		if len(keys) > 0 {
			cfg.UpstreamAPIKeys = keys
		}
	}
	if v := strings.TrimSpace(os.Getenv("MAX_REQUEST_BODY_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxRequestBodyBytes = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("RETRY_EXHAUSTED_AFTER")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			cfg.RetryExhaustedAfter = d
		} else {
			// Preserve an invalid sentinel so validateConfig reports the typo
			// instead of silently retaining a different YAML/default value.
			cfg.RetryExhaustedAfter = -1
		}
	}
	if v := strings.TrimSpace(os.Getenv("UPSTREAM_RESPONSE_HEADER_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			cfg.UpstreamResponseHeaderTimeout = d
		} else {
			// Preserve an invalid sentinel so validateConfig reports the typo
			// instead of silently retaining a different YAML/default value.
			cfg.UpstreamResponseHeaderTimeout = -1
		}
	}
	if v := os.Getenv("OPENCODE_GO_KEY_NAMES"); v != "" {
		cfg.KeyNames = parseKeyNames(v)
	}
	if v := strings.TrimSpace(os.Getenv("USAGE_URL")); v != "" {
		cfg.UsageURL = v
	}
	if v := strings.TrimSpace(os.Getenv("USAGE_CACHE_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			cfg.UsageCacheTTL = d
		} else {
			cfg.UsageCacheTTL = -1 // validateConfig reports the typo
		}
	}
	if v := os.Getenv("SMTP_HOST"); v != "" {
		cfg.SMTP.Host = v
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SMTP.Port = n
		}
	}
	if v := os.Getenv("SMTP_USERNAME"); v != "" {
		cfg.SMTP.Username = v
	}
	if v := os.Getenv("SMTP_PASSWORD"); v != "" {
		cfg.SMTP.Password = v
	}
	if v := os.Getenv("SMTP_FROM"); v != "" {
		cfg.SMTP.From = v
	}
	if v := os.Getenv("SMTP_TO"); v != "" {
		cfg.SMTP.To = v
	}
	if v := os.Getenv("SMTP_TLS"); v != "" {
		cfg.SMTP.TLS = parseBool(v)
	}
	if v := os.Getenv("SMTP_STARTTLS"); v != "" {
		cfg.SMTP.StartTLS = parseBool(v)
	}
}

func defaultString(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.ProxyAPIKey) == "" {
		return errors.New("PROXY_API_KEY is required")
	}
	if len(cfg.UpstreamAPIKeys) == 0 {
		return errors.New("OPENCODE_GO_API_KEYS is required")
	}
	if strings.TrimSpace(cfg.UpstreamBaseURL) == "" {
		return errors.New("UPSTREAM_BASE_URL is required")
	}
	if cfg.MaxRequestBodyBytes <= 0 {
		return errors.New("MAX_REQUEST_BODY_BYTES must be > 0")
	}
	if cfg.RetryExhaustedAfter < 0 {
		return errors.New("RETRY_EXHAUSTED_AFTER must be >= 0")
	}
	if cfg.UpstreamResponseHeaderTimeout < 0 {
		return errors.New("UPSTREAM_RESPONSE_HEADER_TIMEOUT must be >= 0")
	}
	if cfg.UsageCacheTTL < 0 {
		return errors.New("USAGE_CACHE_TTL must be >= 0")
	}
	return validateTiers(cfg.Tiers)
}

func validateTiers(tiers []TierConfig) error {
	names := map[string]bool{}
	for i, t := range tiers {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return fmt.Errorf("tiers[%d]: name is required", i)
		}
		if names[name] {
			return fmt.Errorf("tier %q: duplicate name", t.Name)
		}
		names[name] = true
		if strings.TrimSpace(t.BaseURL) == "" {
			return fmt.Errorf("tier %q: base_url is required", t.Name)
		}
		if strings.TrimSpace(t.APIKey) == "" {
			return fmt.Errorf("tier %q: api_key is required", t.Name)
		}
		if !t.Free {
			return fmt.Errorf("tier %q: free must be true (only free tiers are allowed)", t.Name)
		}
		for _, s := range t.Styles {
			if s != APIStyleOpenAI && s != APIStyleAnthropic {
				return fmt.Errorf("tier %q: unknown style (use openai or anthropic)", t.Name)
			}
		}
		if len(t.Models) == 0 {
			return fmt.Errorf("tier %q: at least one model is required", t.Name)
		}
		models := map[string]bool{}
		for _, m := range t.Models {
			if strings.TrimSpace(m.Name) == "" {
				return fmt.Errorf("tier %q: model name must not be empty", t.Name)
			}
			if models[m.Name] {
				return fmt.Errorf("tier %q: duplicate model %q", t.Name, m.Name)
			}
			models[m.Name] = true
			if m.Context < 0 {
				return fmt.Errorf("tier %q: model %q: context must be >= 0", t.Name, m.Name)
			}
		}
	}
	return nil
}

func safeConfigSummary(cfg Config) string {
	return fmt.Sprintf("listen=%s upstream=%s upstream_keys=%d smtp_configured=%t config_source=%s max_request_body_bytes=%d retry_exhausted_after=%s response_header_timeout=%s tiers=%d", cfg.ListenAddr, cfg.UpstreamBaseURL, len(cfg.UpstreamAPIKeys), cfg.SMTP.Host != "" && cfg.SMTP.From != "" && cfg.SMTP.To != "", defaultString(cfg.ConfigSourcePath, "none"), cfg.MaxRequestBodyBytes, cfg.RetryExhaustedAfter, cfg.UpstreamResponseHeaderTimeout, len(cfg.Tiers))
}

func parseBool(v string) bool { b, _ := strconv.ParseBool(strings.TrimSpace(v)); return b }
