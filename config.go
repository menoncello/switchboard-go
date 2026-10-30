package main

import (
	"cmp"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the binary must not depend on the OS zoneinfo

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

	Telegram TelegramConfig

	// MinContext is the smallest declared context window a tier model needs to be
	// eligible (tier 0 is exempt); 0 turns the floor off.
	MinContext int

	// StateFile is where the cooldowns and the exhausted tier 0 keys are saved so
	// they survive a restart; empty keeps them in memory only. YAML only.
	StateFile string

	// UsageFile is where the usage history is saved (JSON); empty keeps it in memory only.
	// Timezone is the IANA name that decides the history day (empty is UTC). YAML only.
	UsageFile string
	Timezone  string

	// Prices is USD per 1M tokens by tier name and mapped model ID. YAML only.
	Prices map[string]map[string]ModelPrice

	// Tiers are extra free upstreams for failover; the upstream above is the
	// implicit tier 0 and is not listed here.
	Tiers []TierConfig
}

// ModelPrice is USD per 1M input and output tokens.
type ModelPrice struct {
	Input, Output float64
}

// yamlPrice is ModelPrice as written in YAML: pointers tell a missing key from an explicit 0.
type yamlPrice struct {
	Input  *float64 `yaml:"input"`
	Output *float64 `yaml:"output"`
}

func convertPrices(in map[string]map[string]yamlPrice) (map[string]map[string]ModelPrice, error) {
	var out map[string]map[string]ModelPrice
	for tier, models := range in {
		if out == nil {
			out = map[string]map[string]ModelPrice{}
		}
		out[tier] = map[string]ModelPrice{}
		for id, p := range models {
			if p.Input == nil || p.Output == nil {
				return nil, fmt.Errorf("prices: tier %q model %q: input and output are both required", tier, id)
			}
			out[tier][id] = ModelPrice{*p.Input, *p.Output}
		}
	}
	return out, nil
}

// price returns the price of model (the client's name) in tier, looked up by the mapped ID.
func (c Config) price(tier, model string) (ModelPrice, bool) {
	for _, t := range c.Tiers {
		if t.Name != tier {
			continue
		}
		for _, m := range t.Models {
			if m.Name == model {
				id := cmp.Or(m.ID, m.Name)
				p, ok := c.Prices[tier][id]
				return p, ok
			}
		}
	}
	return ModelPrice{}, false
}

type TierConfig struct {
	Name, BaseURL, APIKey string
	Free                  bool
	// DailyCap is the USD spent per day (the usage_file day) after which a paid tier is skipped.
	DailyCap float64
	// Cooldown is how long the tier is skipped after a failure status; nil means
	// RetryExhaustedAfter, 0 means failover only for the request that failed.
	Cooldown *time.Duration
	Styles   []APIStyle
	Models   []TierModel
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
	return Config{ListenAddr: ":8080", UpstreamBaseURL: "https://opencode.ai/zen/go/v1", MaxRequestBodyBytes: 20 << 20, RetryExhaustedAfter: 5 * time.Minute, MinContext: 64000, UpstreamResponseHeaderTimeout: 30 * time.Second, UsageURL: defaultUsageURL, UsageCacheTTL: defaultUsageCacheTTL, SMTP: SMTPConfig{Port: 25}}
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
	Telegram struct {
		BotToken string `yaml:"bot_token"`
		ChatID   string `yaml:"chat_id"`
	} `yaml:"telegram"`
	Limits struct {
		MaxRequestBodyBytes int64 `yaml:"max_request_body_bytes"`
	} `yaml:"limits"`
	MinContext *int                            `yaml:"min_context"`
	StateFile  string                          `yaml:"state_file"`
	UsageFile  string                          `yaml:"usage_file"`
	Timezone   string                          `yaml:"timezone"`
	Prices     map[string]map[string]yamlPrice `yaml:"prices"`
	Tiers      []yamlTier                      `yaml:"tiers"`
}

type yamlTier struct {
	Name     string    `yaml:"name"`
	BaseURL  string    `yaml:"base_url"`
	APIKey   string    `yaml:"api_key"`
	Free     bool      `yaml:"free"`
	DailyCap float64   `yaml:"daily_cap"`
	Cooldown string    `yaml:"cooldown"`
	Styles   []string  `yaml:"styles"`
	Models   yaml.Node `yaml:"models"`
}

func convertTiers(in []yamlTier) ([]TierConfig, error) {
	var out []TierConfig
	for i, yt := range in {
		t := TierConfig{Name: yt.Name, BaseURL: yt.BaseURL, APIKey: yt.APIKey, Free: yt.Free, DailyCap: yt.DailyCap}
		if s := strings.TrimSpace(yt.Cooldown); s != "" {
			d, err := time.ParseDuration(s)
			if err != nil {
				return nil, fmt.Errorf("parse tiers[%d].cooldown: %w", i, err)
			}
			if d < 0 {
				return nil, fmt.Errorf("tiers[%d].cooldown must be >= 0", i)
			}
			t.Cooldown = &d
		}
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
	minContext := -1 // not present in file: keep the default
	if yc.MinContext != nil {
		if *yc.MinContext < 0 {
			return Config{}, fmt.Errorf("min_context must be >= 0")
		}
		minContext = *yc.MinContext
	}
	tiers, err := convertTiers(yc.Tiers)
	if err != nil {
		return Config{}, err
	}
	prices, err := convertPrices(yc.Prices)
	if err != nil {
		return Config{}, err
	}
	return Config{Tiers: tiers, MinContext: minContext, StateFile: strings.TrimSpace(yc.StateFile), UsageFile: strings.TrimSpace(yc.UsageFile), Timezone: strings.TrimSpace(yc.Timezone), Prices: prices, ListenAddr: yc.Server.ListenAddr, UpstreamBaseURL: yc.Upstream.BaseURL, ProxyAPIKey: yc.Server.ProxyAPIKey, UpstreamAPIKeys: yc.Upstream.APIKeys, MaxRequestBodyBytes: yc.Limits.MaxRequestBodyBytes, RetryExhaustedAfter: retry, UpstreamResponseHeaderTimeout: headerTimeout, SMTP: SMTPConfig{Host: yc.SMTP.Host, Port: yc.SMTP.Port, Username: yc.SMTP.Username, Password: yc.SMTP.Password, From: yc.SMTP.From, To: yc.SMTP.To, TLS: yc.SMTP.TLS, StartTLS: yc.SMTP.StartTLS}, Telegram: TelegramConfig{BotToken: yc.Telegram.BotToken, ChatID: yc.Telegram.ChatID}}, nil
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
	if src.StateFile != "" {
		dst.StateFile = src.StateFile
	}
	if src.UsageFile != "" {
		dst.UsageFile = src.UsageFile
	}
	if src.Timezone != "" {
		dst.Timezone = src.Timezone
	}
	if len(src.Prices) > 0 {
		dst.Prices = src.Prices
	}
	if src.MinContext >= 0 {
		dst.MinContext = src.MinContext
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
	if src.Telegram.BotToken != "" {
		dst.Telegram.BotToken = src.Telegram.BotToken
	}
	if src.Telegram.ChatID != "" {
		dst.Telegram.ChatID = src.Telegram.ChatID
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
	if cfg.MinContext < 0 {
		return errors.New("min_context must be >= 0")
	}
	if cfg.UpstreamResponseHeaderTimeout < 0 {
		return errors.New("UPSTREAM_RESPONSE_HEADER_TIMEOUT must be >= 0")
	}
	if cfg.UsageCacheTTL < 0 {
		return errors.New("USAGE_CACHE_TTL must be >= 0")
	}
	if cfg.Timezone != "" {
		_, err := time.LoadLocation(cfg.Timezone)
		if err == nil && cfg.Timezone == "Local" {
			err = errors.New("depends on the host time zone, use an IANA name")
		}
		if err != nil {
			return fmt.Errorf("timezone %q: %w", cfg.Timezone, err)
		}
	}
	if err := validateTiers(cfg.Tiers); err != nil {
		return err
	}
	if err := validatePrices(cfg); err != nil {
		return err
	}
	return validatePaidTiers(cfg)
}

// validatePaidTiers requires a finite daily_cap > 0, usage_file and a price for every model of a paid tier,
// and rejects daily_cap on a free one.
func validatePaidTiers(cfg Config) error {
	for _, t := range cfg.Tiers {
		if t.Free {
			if t.DailyCap != 0 {
				return fmt.Errorf("tier %q: daily_cap is only for paid tiers (remove it or drop free: true)", t.Name)
			}
			continue
		}
		if !(t.DailyCap > 0) || math.IsInf(t.DailyCap, 0) { // also rejects NaN
			return fmt.Errorf("tier %q: a tier without free: true needs daily_cap, finite and > 0 (USD per day)", t.Name)
		}
		if strings.TrimSpace(cfg.UsageFile) == "" {
			return fmt.Errorf("tier %q: a tier without free: true needs usage_file (the daily spend is read from it)", t.Name)
		}
		for _, m := range t.Models {
			id := cmp.Or(m.ID, m.Name)
			p, ok := cfg.Prices[t.Name][id]
			if !ok {
				return fmt.Errorf("tier %q model %q: a tier without free: true needs a price in prices", t.Name, id)
			}
			if p.Input <= 0 && p.Output <= 0 {
				return fmt.Errorf("tier %q model %q: a tier without free: true needs input or output price > 0 (the cap would never accrue)", t.Name, id)
			}
		}
	}
	return nil
}

// maxPrice is the highest input or output price (USD per 1M tokens) in prices: the
// worst-case cost charged to a paid tier response with no usage.
func (c Config) maxPrice() float64 {
	var m float64
	for _, models := range c.Prices {
		for _, p := range models {
			m = max(m, p.Input, p.Output)
		}
	}
	return m
}

// paidTier reports whether name is a configured tier without free: true.
func (c Config) paidTier(name string) bool {
	return slices.ContainsFunc(c.Tiers, func(t TierConfig) bool { return t.Name == name && !t.Free })
}

func validatePrices(cfg Config) error {
	for tier, models := range cfg.Prices {
		i := slices.IndexFunc(cfg.Tiers, func(t TierConfig) bool { return t.Name == tier })
		if i < 0 {
			return fmt.Errorf("prices: tier %q is not in tiers (tier 0 has no price)", tier)
		}
		for id, p := range models {
			if !slices.ContainsFunc(cfg.Tiers[i].Models, func(m TierModel) bool { return cmp.Or(m.ID, m.Name) == id }) {
				return fmt.Errorf("prices: tier %q model %q: no model of the tier maps to this id", tier, id)
			}
			if !(p.Input >= 0 && p.Output >= 0) || math.IsInf(p.Input, 0) || math.IsInf(p.Output, 0) { // also rejects NaN
				return fmt.Errorf("prices: tier %q model %q: input and output must be finite and >= 0", tier, id)
			}
		}
	}
	return nil
}

func validateTiers(tiers []TierConfig) error {
	names := map[string]bool{}
	for i, t := range tiers {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return fmt.Errorf("tiers[%d]: name is required", i)
		}
		if name == tier0Name {
			return fmt.Errorf("tier %q: name is reserved for tier 0", t.Name)
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
		if t.Cooldown != nil && *t.Cooldown < 0 {
			return fmt.Errorf("tier %q: cooldown must be >= 0", t.Name)
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
	return fmt.Sprintf("listen=%s upstream=%s upstream_keys=%d smtp_configured=%t telegram_configured=%t config_source=%s max_request_body_bytes=%d retry_exhausted_after=%s response_header_timeout=%s tiers=%d", cfg.ListenAddr, cfg.UpstreamBaseURL, len(cfg.UpstreamAPIKeys), cfg.SMTP.Host != "" && cfg.SMTP.From != "" && cfg.SMTP.To != "", cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "", defaultString(cfg.ConfigSourcePath, "none"), cfg.MaxRequestBodyBytes, cfg.RetryExhaustedAfter, cfg.UpstreamResponseHeaderTimeout, len(cfg.Tiers))
}

func parseBool(v string) bool { b, _ := strconv.ParseBool(strings.TrimSpace(v)); return b }

// ineligibleTiers returns the names of the tiers with no model declaring at least
// cfg.MinContext. Tier 0 is not in Tiers, so it is exempt.
func ineligibleTiers(cfg Config) []string {
	var names []string
	for _, t := range cfg.Tiers {
		if !slices.ContainsFunc(t.Models, func(m TierModel) bool { return m.Context >= cfg.MinContext }) {
			names = append(names, t.Name)
		}
	}
	return names
}

func warnIneligibleTiers(cfg Config) {
	for _, name := range ineligibleTiers(cfg) {
		log.Printf("warning: tier %q has no model with context >= min_context (%d); it will never serve requests (declare context on its models or set min_context: 0)", name, cfg.MinContext)
	}
}

func warnNoStateFile(cfg Config) {
	if cfg.StateFile == "" {
		log.Printf("warning: state_file is not set; tier cooldowns and exhausted keys are lost on restart")
	}
}

func warnNoUsageFile(cfg Config) {
	if cfg.UsageFile == "" {
		log.Printf("warning: usage_file is not set; the usage history is kept in memory and lost on restart")
	}
}
