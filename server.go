package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type App struct {
	config Config
	keys   *KeyManager
	client *http.Client
	sender *SMTPNotifier
	usage  *usageCache
	logger *slog.Logger
	// cooldowns holds the per-tier cooldown (tier 0 under tier0Name), in memory only.
	cooldowns *tierCooldowns
}

func newApp(cfg Config) *App {
	return &App{config: cfg, keys: NewKeyManager(cfg.UpstreamAPIKeys, cfg.RetryExhaustedAfter), client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: cfg.UpstreamResponseHeaderTimeout, ExpectContinueTimeout: 2 * time.Second}}, sender: NewSMTPNotifier(cfg.SMTP), usage: newUsageCache(), logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), cooldowns: newTierCooldowns()}
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	case r.URL.Path == "/readyz":
		a.handleReadyz(w, r)
	case r.URL.Path == "/status" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		a.handleStatusPage(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/"):
		if !a.authOK(r) {
			writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "Unauthorized")
			return
		}
		a.handleAdmin(w, r)
	case isProxyPath(r.URL.Path):
		if !a.authOK(r) {
			writeAPIError(w, apiStyleForRequest(r), http.StatusUnauthorized, "invalid_api_key", "Unauthorized")
			return
		}
		a.proxyV1(w, r, apiStyleForRequest(r))
	default:
		http.NotFound(w, r)
	}
}

func (a *App) authOK(r *http.Request) bool {
	want := a.config.ProxyAPIKey
	if want == "" {
		return false
	}
	if tok := bearerToken(r.Header.Get("Authorization")); tok != "" {
		return subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("x-api-key")), []byte(want)) == 1
}

func bearerToken(v string) string {
	parts := strings.Fields(v)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func isProxyPath(path string) bool {
	if strings.HasPrefix(path, "/v1/") {
		return true
	}
	switch path {
	case "/models", "/chat/completions", "/messages", "/complete":
		return true
	default:
		return false
	}
}

func (a *App) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/validate-keys" && r.Method == http.MethodPost:
		a.handleValidateKeys(w, r)
	case r.URL.Path == "/admin/reset-key" && r.Method == http.MethodPost:
		a.handleResetKey(w, r)
	case r.URL.Path == "/admin/reset-all-keys" && r.Method == http.MethodPost:
		a.handleResetAllKeys(w, r)
	case r.URL.Path == "/admin/keys" && r.Method == http.MethodGet:
		a.handleKeysReport(w, r)
	case r.URL.Path == "/admin/status" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.keys.Status())
	default:
		http.NotFound(w, r)
	}
}

// handleResetKey un-exhausts a single upstream key by index, without restarting
// the proxy. Body: {"index": <int>}. Responds with the updated status.
func (a *App) handleResetKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index int `json:"index"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if body.Index < 0 || body.Index >= len(a.config.UpstreamAPIKeys) {
		http.Error(w, "index out of range", http.StatusBadRequest)
		return
	}
	a.keys.MarkAvailable(body.Index)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.keys.Status())
}

// handleResetAllKeys un-exhausts every upstream key, without restarting.
func (a *App) handleResetAllKeys(w http.ResponseWriter, r *http.Request) {
	for i := range a.config.UpstreamAPIKeys {
		a.keys.MarkAvailable(i)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.keys.Status())
}

func (a *App) handleReadyz(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"ready": false}
	if err := validateConfig(a.config); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	_, key, ok := a.keys.Current()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	if err := a.checkUpstreamReady(r.Context(), key); err != nil {
		resp["error"] = "upstream not ready"
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp["ready"] = true
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) checkUpstreamReady(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.config.UpstreamBaseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "OpenAI/Python 1.0.0")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("upstream status %d", resp.StatusCode)
}

func (a *App) handleValidateKeys(w http.ResponseWriter, r *http.Request) {
	results := make([]ValidateKeyResult, 0, len(a.config.UpstreamAPIKeys))
	for i, key := range a.config.UpstreamAPIKeys {
		res := ValidateKeyResult{Index: i}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.config.UpstreamBaseURL, "/")+"/models", nil)
		if err != nil {
			cancel()
			res.State = string(KeyUnknown)
			res.Status = http.StatusBadGateway
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("User-Agent", "OpenAI/Python 1.0.0")
		resp, err := a.client.Do(req)
		cancel()
		if err != nil {
			res.State = string(KeyUnknown)
			res.Status = http.StatusBadGateway
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		res.Status = resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			a.keys.MarkAvailable(i)
			res.State = string(KeyAvailable)
		} else if isKeyDead(resp) {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			a.keys.SetState(i, KeyExhausted)
			res.State = string(KeyExhausted)
			res.Error = "quota exhausted"
		} else {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			a.keys.SetState(i, KeyUnknown)
			res.State = string(KeyUnknown)
			res.Error = fmt.Sprintf("status %d", resp.StatusCode)
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, ValidateKeysResponse{Results: results})
}

func (a *App) validateConfigAndPrint() error {
	if err := validateConfig(a.config); err != nil {
		return err
	}
	log.Println(safeConfigSummary(a.config))
	return nil
}
