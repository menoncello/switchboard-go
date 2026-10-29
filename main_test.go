package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyManagerNoCurrentWhenExhausted(t *testing.T) {
	km := NewKeyManager([]string{"a"}, time.Hour)
	km.MarkExhausted(0)
	if _, _, ok := km.Current(); ok {
		t.Fatal("expected no current key")
	}
}

func TestBearerTokenCaseInsensitive(t *testing.T) {
	if got := bearerToken("bearer abc"); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := bearerToken("BEARER abc"); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestIsQuota429(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"quota exceeded","code":"insufficient_quota"}}`))}
	if !isQuota429(resp) {
		t.Fatal("expected quota 429")
	}
}

func TestIsQuota429NotGenericRateLimit(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"try again","code":"rate_limit_exceeded"}}`))}
	if isQuota429(resp) {
		t.Fatal("expected generic rate_limit_exceeded not to count as quota")
	}
}

func TestIsQuota429RestoresBody(t *testing.T) {
	const body = `{"error":{"message":"quota exceeded","code":"insufficient_quota"}}`
	resp := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	_ = isQuota429(resp)
	got, _ := io.ReadAll(resp.Body)
	if string(got) != body {
		t.Fatalf("body not restored: %q", string(got))
	}
}

func TestIsQuota429AnthropicUsageLimit(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"rate_limit_error","message":"credit balance is too low"}}`))}
	if !isQuota429(resp) {
		t.Fatal("expected anthropic credit balance 429 to count as quota")
	}
}

func TestIsQuota429AnthropicGenericRateLimit(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limit reached"}}`))}
	if isQuota429(resp) {
		t.Fatal("expected generic anthropic rate limit not to count as quota")
	}
}

func TestRequestTooLargeReturns413(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, MaxRequestBodyBytes: 4})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("12345")))
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestRootOpenAIPathProxies(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"glm-5.1","messages":[]}`))
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer u" {
		t.Fatalf("unexpected upstream path=%q auth=%q", gotPath, gotAuth)
	}
}

func TestDoUpstreamSetsDefaultUserAgent(t *testing.T) {
	var gotUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: upstream.URL})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	resp, err := app.doUpstream(req.Context(), req, nil, upstream.URL, "u", APIStyleOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotUA != "OpenAI/Python 1.0.0" {
		t.Fatalf("got user-agent %q", gotUA)
	}
}

func TestDoUpstreamAnthropicSetsHeaders(t *testing.T) {
	var gotPath, gotKey, gotAuth, gotVersion, gotUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("anthropic-version")
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: upstream.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"minimax-m3","messages":[]}`))
	req.Header.Set("x-api-key", "p")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := app.doUpstream(req.Context(), req, []byte(`{"model":"minimax-m3","messages":[]}`), upstream.URL, "u", APIStyleAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotPath != "/messages" || gotKey != "u" || gotAuth != "" || gotVersion != "2023-06-01" || gotUA != "anthropic-sdk-go/1.0.0" {
		t.Fatalf("unexpected upstream request path=%q key=%q auth=%q version=%q ua=%q", gotPath, gotKey, gotAuth, gotVersion, gotUA)
	}
}

func TestProxyAnthropicMessagesCyclesKeys(t *testing.T) {
	var gotKeys []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			http.NotFound(w, r)
			return
		}
		gotKeys = append(gotKeys, r.Header.Get("x-api-key"))
		if r.Header.Get("x-api-key") == "bad" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"usage limit exhausted"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","content":[]}`))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"bad", "good"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"minimax-m3","messages":[]}`))
	req.Header.Set("x-api-key", "p")
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Join(gotKeys, ",") != "bad,good" {
		t.Fatalf("unexpected keys: %v", gotKeys)
	}
}

func TestAuthFailureReturnsOpenAIJSON(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("ct %q", ct)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] == nil {
		t.Fatal("missing error object")
	}
}

func TestAuthOKConstantTimeCompare(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("expected non-401 for correct bearer, got %d", rec.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req2.Header.Set("Authorization", "Bearer wrong")
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong bearer, got %d", rec2.Code)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req3.Header.Set("x-api-key", "p")
	rec3 := httptest.NewRecorder()
	app.ServeHTTP(rec3, req3)
	if rec3.Code == http.StatusUnauthorized {
		t.Fatalf("expected non-401 for correct x-api-key, got %d", rec3.Code)
	}
}

func TestAdminResetKeyUnExhausts(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"a", "b"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	app.keys.MarkExhausted(0)
	app.keys.MarkExhausted(1)
	body := bytes.NewReader([]byte(`{"index":0}`))
	req := httptest.NewRequest(http.MethodPost, "/admin/reset-key", body)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body %s", rec.Code, rec.Body.String())
	}
	var st StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Keys[0].State != string(KeyAvailable) {
		t.Fatalf("key 0 not available: %+v", st.Keys[0])
	}
	if st.Keys[1].State != string(KeyExhausted) {
		t.Fatalf("key 1 should still be exhausted: %+v", st.Keys[1])
	}
}

func TestAdminResetKeyRejectsBadIndex(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"a"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	body := bytes.NewReader([]byte(`{"index":99}`))
	req := httptest.NewRequest(http.MethodPost, "/admin/reset-key", body)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAdminResetAllKeys(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"a", "b", "c"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	for i := range app.config.UpstreamAPIKeys {
		app.keys.MarkExhausted(i)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/reset-all-keys", nil)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var st StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	for i, k := range st.Keys {
		if k.State != string(KeyAvailable) {
			t.Fatalf("key %d not available: %+v", i, k)
		}
	}
}
func TestAuthFailureReturnsAnthropicJSON(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	errObj, ok := payload["error"].(map[string]any)
	if payload["type"] != "error" || !ok || errObj["type"] != "authentication_error" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestRootAnthropicAuthFailureReturnsAnthropicJSON(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://example.com", MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodPost, "/messages", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	errObj, ok := payload["error"].(map[string]any)
	if payload["type"] != "error" || !ok || errObj["type"] != "authentication_error" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestValidateConfigHelper(t *testing.T) {
	if err := validateConfig(Config{}); err == nil {
		t.Fatal("expected error")
	}
	if err := validateConfig(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://x", MaxRequestBodyBytes: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateKeysEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") != "OpenAI/Python 1.0.0" {
			t.Fatalf("unexpected user-agent %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Authorization") == "Bearer good" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded","code":"insufficient_quota"}}`))
	}))
	defer upstream.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"good", "bad"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodPost, "/admin/validate-keys", nil)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var out ValidateKeysResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("got %d results", len(out.Results))
	}
	if out.Results[0].State != string(KeyAvailable) || out.Results[1].State != string(KeyExhausted) {
		t.Fatalf("unexpected results: %+v", out.Results)
	}
}

func TestReadyzUnauthenticated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"good"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestLoadConfigFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`server:
  listen_addr: "127.0.0.1:9090"
  proxy_api_key: "yaml-proxy"
upstream:
  base_url: "https://example.com/v1"
  api_keys: ["k1", "k2"]
smtp:
  host: "smtp.example.com"
  port: 587
  tls: false
  starttls: true
limits:
  max_request_body_bytes: 1234
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	t.Setenv("PROXY_API_KEY", "env-proxy")
	t.Setenv("OPENCODE_GO_API_KEYS", "env1,env2")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:9090" || cfg.ProxyAPIKey != "env-proxy" || cfg.UpstreamBaseURL != "https://example.com/v1" || cfg.MaxRequestBodyBytes != 1234 {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfigExplicitInvalidPathErrors(t *testing.T) {
	t.Setenv("SWITCHBOARD_GO_CONFIG", "/does/not/exist.yaml")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadConfigExplicitInvalidYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error")
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`server: {listen_addr: "127.0.0.1:1", proxy_api_key: "yaml"}
upstream: {base_url: "https://yaml", api_keys: ["yaml1"]}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	t.Setenv("PROXY_API_KEY", "env")
	t.Setenv("OPENCODE_GO_API_KEYS", "e1,e2")
	t.Setenv("LISTEN_ADDR", "0.0.0.0:9999")
	t.Setenv("MAX_REQUEST_BODY_BYTES", "99")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:9999" || cfg.ProxyAPIKey != "env" || len(cfg.UpstreamAPIKeys) != 2 || cfg.MaxRequestBodyBytes != 99 {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestKeyManagerRetryEligibleAfterCooldown(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	km := NewKeyManager([]string{"a"}, time.Minute)
	km.now = func() time.Time { return now }
	km.MarkExhausted(0)
	if _, _, ok := km.Current(); ok {
		t.Fatal("expected no eligible key immediately after exhaustion")
	}
	now = now.Add(59 * time.Second)
	if _, _, ok := km.Current(); ok {
		t.Fatal("expected key still in cooldown at 59s")
	}
	now = now.Add(2 * time.Second) // 61s since exhaustion
	idx, key, ok := km.Current()
	if !ok || idx != 0 || key != "a" {
		t.Fatalf("expected key eligible after cooldown, got idx=%d key=%q ok=%v", idx, key, ok)
	}
}

func TestKeyManagerZeroCooldownImmediatelyEligible(t *testing.T) {
	km := NewKeyManager([]string{"a"}, 0)
	km.MarkExhausted(0)
	idx, key, ok := km.Current()
	if !ok || idx != 0 || key != "a" {
		t.Fatalf("expected exhausted key immediately eligible with zero cooldown, got ok=%v", ok)
	}
}

func TestKeyManagerRetryAfterSeconds(t *testing.T) {
	now := time.Unix(2000, 0).UTC()
	km := NewKeyManager([]string{"a", "b"}, 2*time.Minute)
	km.now = func() time.Time { return now }
	km.MarkExhausted(0) // eligible at t=2120
	now = now.Add(30 * time.Second)
	km.MarkExhausted(1) // eligible at t=2150
	secs, ok := km.RetryAfterSeconds()
	if !ok || secs != 90 { // soonest 2120 - now 2030
		t.Fatalf("expected retry-after 90s, got %d ok=%v", secs, ok)
	}

	km2 := NewKeyManager([]string{"a"}, 0)
	km2.MarkExhausted(0)
	if _, ok := km2.RetryAfterSeconds(); ok {
		t.Fatal("expected no retry-after hint when cooldown is disabled")
	}
}

func TestKeyManagerReArmsNotificationsOnRecovery(t *testing.T) {
	km := NewKeyManager([]string{"a", "b"}, time.Minute)
	km.MarkExhausted(0)
	km.MarkExhausted(1)
	if !km.ShouldNotifyAllExhausted() {
		t.Fatal("expected first all-exhausted notification")
	}
	if km.ShouldNotifyAllExhausted() {
		t.Fatal("expected all-exhausted notification suppressed the second time")
	}
	if !km.ShouldNotifySwitch(0) {
		t.Fatal("expected switch notification for key 0")
	}
	// Recovery of a key must re-arm both the switch flag and the all-exhausted flag.
	km.MarkAvailable(0)
	if !km.ShouldNotifySwitch(0) {
		t.Fatal("expected switch notification re-armed after recovery")
	}
	km.MarkExhausted(0)
	if !km.ShouldNotifyAllExhausted() {
		t.Fatal("expected all-exhausted notification re-armed after a recovery round")
	}
}

func doProxyReq(app *App) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.1","messages":[]}`))
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestProxyRetriesExhaustedKeyAfterCooldown(t *testing.T) {
	var replenished atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if replenished.Load() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded","code":"insufficient_quota"}}`))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k1"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024, RetryExhaustedAfter: time.Minute})
	now := time.Unix(5000, 0).UTC()
	app.keys.now = func() time.Time { return now }

	// First request: the only key quota-fails, so the proxy returns a local 429
	// carrying a Retry-After hint pointing at the next probe window.
	rec := doProxyReq(app)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body %s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "60" {
		t.Fatalf("expected Retry-After 60, got %q", ra)
	}

	// Within the cooldown the proxy fast-fails without probing upstream again.
	rec = doProxyReq(app)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 during cooldown, got %d", rec.Code)
	}

	// Cooldown elapses and the account is replenished: the next real request acts
	// as a probe and succeeds.
	now = now.Add(2 * time.Minute)
	replenished.Store(true)
	rec = doProxyReq(app)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after cooldown+replenish, got %d body %s", rec.Code, rec.Body.String())
	}
	st := app.keys.Status()
	if st.Keys[0].State != string(KeyAvailable) || !st.Keys[0].Eligible {
		t.Fatalf("expected key available and eligible after recovery, got %+v", st.Keys[0])
	}
	if st.RetryExhaustedAfterSeconds != 60 {
		t.Fatalf("expected retry_exhausted_after_seconds 60, got %d", st.RetryExhaustedAfterSeconds)
	}
}

func TestProxyZeroCooldownNoRetryAfterHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded","code":"insufficient_quota"}}`))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k1"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024, RetryExhaustedAfter: 0})
	rec := doProxyReq(app)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "" {
		t.Fatalf("expected no Retry-After header with cooldown disabled, got %q", ra)
	}
}

func TestLoadConfigRetryExhaustedAfterParsedAndOverridden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"], retry_exhausted_after: \"90s\"}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetryExhaustedAfter != 90*time.Second {
		t.Fatalf("expected 90s from yaml, got %s", cfg.RetryExhaustedAfter)
	}
	t.Setenv("RETRY_EXHAUSTED_AFTER", "5m")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetryExhaustedAfter != 5*time.Minute {
		t.Fatalf("expected env override to 5m, got %s", cfg.RetryExhaustedAfter)
	}
}

func TestLoadConfigRetryExhaustedAfterDefaultAndExplicitZero(t *testing.T) {
	dir := t.TempDir()
	omitted := filepath.Join(dir, "omitted.yaml")
	if err := os.WriteFile(omitted, []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", omitted)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetryExhaustedAfter != 5*time.Minute {
		t.Fatalf("expected default 5m when omitted, got %s", cfg.RetryExhaustedAfter)
	}

	zero := filepath.Join(dir, "zero.yaml")
	if err := os.WriteFile(zero, []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"], retry_exhausted_after: \"0\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", zero)
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetryExhaustedAfter != 0 {
		t.Fatalf("expected explicit 0 to disable cooldown, got %s", cfg.RetryExhaustedAfter)
	}
}

func TestLoadConfigRejectsInvalidRetryExhaustedAfterEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"]}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	t.Setenv("RETRY_EXHAUSTED_AFTER", "not-a-duration")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "RETRY_EXHAUSTED_AFTER") {
		t.Fatalf("expected RETRY_EXHAUSTED_AFTER validation error, got %v", err)
	}
}

func TestLoadConfigResponseHeaderTimeoutParsedAndOverridden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"], response_header_timeout: \"90s\"}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamResponseHeaderTimeout != 90*time.Second {
		t.Fatalf("expected 90s from yaml, got %s", cfg.UpstreamResponseHeaderTimeout)
	}
	t.Setenv("UPSTREAM_RESPONSE_HEADER_TIMEOUT", "5m")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamResponseHeaderTimeout != 5*time.Minute {
		t.Fatalf("expected env override to 5m, got %s", cfg.UpstreamResponseHeaderTimeout)
	}
}

func TestLoadConfigResponseHeaderTimeoutDefaultAndExplicitZero(t *testing.T) {
	dir := t.TempDir()
	omitted := filepath.Join(dir, "omitted.yaml")
	if err := os.WriteFile(omitted, []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", omitted)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("expected default 30s when omitted, got %s", cfg.UpstreamResponseHeaderTimeout)
	}

	zero := filepath.Join(dir, "zero.yaml")
	if err := os.WriteFile(zero, []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"], response_header_timeout: \"0\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", zero)
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamResponseHeaderTimeout != 0 {
		t.Fatalf("expected explicit 0 to disable header timeout, got %s", cfg.UpstreamResponseHeaderTimeout)
	}
}

func TestLoadConfigRejectsInvalidResponseHeaderTimeoutEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server: {proxy_api_key: \"p\"}\nupstream: {base_url: \"https://x\", api_keys: [\"k\"]}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
	t.Setenv("UPSTREAM_RESPONSE_HEADER_TIMEOUT", "not-a-duration")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "UPSTREAM_RESPONSE_HEADER_TIMEOUT") {
		t.Fatalf("expected UPSTREAM_RESPONSE_HEADER_TIMEOUT validation error, got %v", err)
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushCount int
}

func (f *flushRecorder) Flush() {
	f.flushCount++
	f.ResponseRecorder.Flush()
}

type chunkedReader struct {
	chunks [][]byte
	idx    int
}

func (cr *chunkedReader) Read(p []byte) (n int, err error) {
	if cr.idx >= len(cr.chunks) {
		return 0, io.EOF
	}
	n = copy(p, cr.chunks[cr.idx])
	cr.idx++
	return n, nil
}

func TestCopyResponseFlushesEventStream(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream; charset=utf-8"},
		},
		Body: io.NopCloser(&chunkedReader{
			chunks: [][]byte{
				[]byte("data: {\"choices\": [{\"delta\": {\"content\": \"Hello\"}}]}\n\n"),
				[]byte("data: {\"choices\": [{\"delta\": {\"content\": \" World\"}}]}\n\n"),
				[]byte("data: [DONE]\n\n"),
			},
		}),
	}

	copyResponse(rec, resp)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.flushCount != 3 {
		t.Fatalf("expected exactly 3 flushes, got %d", rec.flushCount)
	}

	expectedBody := "data: {\"choices\": [{\"delta\": {\"content\": \"Hello\"}}]}\n\ndata: {\"choices\": [{\"delta\": {\"content\": \" World\"}}]}\n\ndata: [DONE]\n\n"
	if rec.Body.String() != expectedBody {
		t.Fatalf("unexpected body: got %q", rec.Body.String())
	}
}

func TestCopyResponseDoesNotFlushNormalJSON(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(&chunkedReader{
			chunks: [][]byte{
				[]byte(`{"choices": `),
				[]byte(`[{"message": {"content": "Hello World"}}]}`),
			},
		}),
	}

	copyResponse(rec, resp)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.flushCount != 0 {
		t.Fatalf("expected 0 explicit flushes for non-stream JSON, got %d", rec.flushCount)
	}

	expectedBody := `{"choices": [{"message": {"content": "Hello World"}}]}`
	if rec.Body.String() != expectedBody {
		t.Fatalf("unexpected body: got %q", rec.Body.String())
	}
}

const subscriptionRequired403 = `{"error":{"message":"Upstream request failed: An active OpenCode Go subscription is required to use Go models"}}`

func TestIsKeyDead403Subscription(t *testing.T) {
	resp := &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(subscriptionRequired403))}
	if !isKeyDead(resp) {
		t.Fatal("expected 403 subscription-required to be a dead key")
	}
	other := `{"error":{"message":"forbidden"}}`
	resp = &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(other))}
	if isKeyDead(resp) {
		t.Fatal("expected other 403 not to be a dead key")
	}
	if got, _ := io.ReadAll(resp.Body); string(got) != other {
		t.Fatalf("body not restored: %q", got)
	}
	resp = &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(subscriptionRequired403))}
	if isKeyDead(resp) {
		t.Fatal("expected 200 not to be a dead key")
	}
}

func TestProxyRotatesOn403SubscriptionRequired(t *testing.T) {
	var gotKeys []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeys = append(gotKeys, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(subscriptionRequired403))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"bad", "good"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	rec := doProxyReq(app)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Join(gotKeys, ",") != "Bearer bad,Bearer good" {
		t.Fatalf("unexpected keys: %v", gotKeys)
	}
	st := app.keys.Status()
	if st.Keys[0].State != string(KeyExhausted) || st.Keys[1].State != string(KeyAvailable) {
		t.Fatalf("unexpected key states: %+v", st.Keys)
	}
}

func TestProxyPassesThroughOther403(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"model not allowed"}}`))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k1", "k2"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	rec := doProxyReq(app)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "model not allowed") {
		t.Fatalf("expected 403 passthrough, got %d body %s", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("expected a single upstream call, got %d", calls.Load())
	}
	if st := app.keys.Status(); st.Keys[0].State != string(KeyAvailable) {
		t.Fatalf("expected key 0 available, got %+v", st.Keys[0])
	}
}

func TestProxyAllKeys403SubscriptionExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(subscriptionRequired403))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k1", "k2"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	rec := doProxyReq(app)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "all upstream keys exhausted") {
		t.Fatalf("expected 429 all exhausted, got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestValidateKeysMarksExhaustedOn403Subscription(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(subscriptionRequired403))
	}))
	defer upstream.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"good", "bad"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1})
	req := httptest.NewRequest(http.MethodPost, "/admin/validate-keys", nil)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	var out ValidateKeysResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 || out.Results[0].State != string(KeyAvailable) || out.Results[1].State != string(KeyExhausted) {
		t.Fatalf("unexpected results: %+v", out.Results)
	}
}

func TestProxyRotatesOn403SubscriptionCompressed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bad" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusForbidden)
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte(subscriptionRequired403))
			_ = zw.Close()
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(subscriptionRequired403))
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"bad", "good"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.1","messages":[]}`))
	req.Header.Set("Authorization", "Bearer p")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestProxyDecompressesUpstreamBodyForClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte(`{"ok":true}`))
		_ = zw.Close()
	}))
	defer upstream.Close()

	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k1"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer p")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != `{"ok":true}` || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected plain body without Content-Encoding, got %q (%q)", rec.Body.String(), rec.Header().Get("Content-Encoding"))
	}
}

func TestProxyStreamsSSEFromGzipUpstream(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte("data: one\n\n"))
		_ = zw.Flush()
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		_, _ = zw.Write([]byte("data: two\n\n"))
		_ = zw.Close()
	}))
	defer upstream.Close()

	srv := httptest.NewServer(newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k1"}, UpstreamBaseURL: upstream.URL, MaxRequestBodyBytes: 1024}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer p")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make(chan string, 1)
	go func() {
		buf := make([]byte, len("data: one\n\n"))
		_, _ = io.ReadFull(resp.Body, buf)
		first <- string(buf)
	}()
	select {
	case got := <-first:
		if got != "data: one\n\n" {
			t.Fatalf("unexpected first event %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first SSE event not streamed before upstream finished")
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "data: two\n\n" {
		t.Fatalf("unexpected rest %q", rest)
	}
}

const fakeUsageBody = `{"usage":{"rolling":{"percent":12.5,"resetsAt":"2026-10-25T12:00:00Z"},"weekly":{"percent":40,"resetsAt":"2026-10-27T00:00:00Z"},"monthly":{"percent":7,"resetsAt":"2026-11-01T00:00:00Z"}}}`

// fakeUsageServer answers /usage per Bearer key: "sk-good*" gets windows, "sk-bad*" gets 401.
func fakeUsageServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-good") {
			_, _ = w.Write([]byte(fakeUsageBody))
			return
		}
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func getKeysReport(t *testing.T, app *App) (int, string, KeysReportResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/keys", nil)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	var out KeysReportResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, rec.Body.String(), out
}

func TestAdminKeysUsageSuccess(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1", "sk-good-2"}, KeyNames: []string{"principal", "reserva"}, UsageURL: srv.URL, UsageCacheTTL: time.Minute, RetryExhaustedAfter: time.Hour})
	app.keys.MarkExhausted(1)
	code, body, out := getKeysReport(t, app)
	if code != http.StatusOK {
		t.Fatalf("got %d %s", code, body)
	}
	if len(out.Keys) != 2 || out.GeneratedAt == "" || out.UsageCachedSeconds != 60 {
		t.Fatalf("unexpected report: %s", body)
	}
	k0, k1 := out.Keys[0], out.Keys[1]
	if k0.Name != "principal" || !k0.Eligible {
		t.Fatalf("key 0: %+v", k0)
	}
	if k1.Name != "reserva" || k1.State != "exhausted" || k1.Eligible || k1.Last429Time == "" {
		t.Fatalf("key 1: %+v", k1)
	}
	u := k0.Usage
	if u == nil || u.Rolling.Percent != 12.5 || u.Weekly.Percent != 40 || u.Monthly.Percent != 7 {
		t.Fatalf("usage: %+v", u)
	}
	if string(u.Weekly.ResetsAt) != `"2026-10-27T00:00:00Z"` {
		t.Fatalf("resets_at: %s", u.Weekly.ResetsAt)
	}
	if !strings.Contains(body, `"resets_at"`) || strings.Contains(body, "resetsAt") {
		t.Fatalf("expected snake_case resets_at: %s", body)
	}
}

func TestAdminKeysOneKeyErrorOneGood(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-bad-secret", "sk-good-secret"}, UsageURL: srv.URL})
	_, body, out := getKeysReport(t, app)
	if out.Keys[0].UsageError != "HTTP 401" || out.Keys[0].Usage != nil {
		t.Fatalf("key 0: %+v", out.Keys[0])
	}
	if out.Keys[1].UsageError != "" || out.Keys[1].Usage == nil {
		t.Fatalf("key 1: %+v", out.Keys[1])
	}
	if out.Keys[0].Name != "" || out.Keys[1].Name != "" {
		t.Fatalf("names should be empty: %s", body)
	}
}

func TestAdminKeysNeverLeaksKeyValue(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // connection refused: the url.Error text would carry the URL, not the key
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	for _, usageURL := range []string{srv.URL, deadURL} {
		app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-bad-SEGREDO123", "sk-good-SEGREDO456"}, KeyNames: []string{"a", "b"}, UsageURL: usageURL})
		_, body, _ := getKeysReport(t, app)
		for _, secret := range []string{"SEGREDO123", "SEGREDO456", "sk-bad", "sk-good"} {
			if strings.Contains(body, secret) || strings.Contains(logs.String(), secret) {
				t.Fatalf("leaked %q\nbody: %s\nlogs: %s", secret, body, logs.String())
			}
		}
	}
	if !strings.Contains(logs.String(), "usage query failed") {
		t.Fatalf("expected the failure to be logged: %q", logs.String())
	}
}

func TestAdminKeysCache(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	now := time.Now()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1", "sk-good-2"}, UsageURL: srv.URL, UsageCacheTTL: time.Minute})
	app.usage.now = func() time.Time { return now }
	getKeysReport(t, app)
	getKeysReport(t, app)
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected 2 upstream hits (1 per key), got %d", got)
	}
	now = now.Add(61 * time.Second)
	getKeysReport(t, app)
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("expected refresh after TTL (4 hits), got %d", got)
	}
}

func TestAdminKeysNamesMissingOrShort(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	long := strings.Repeat("x", 60)
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1", "sk-good-2", "sk-good-3"}, UsageURL: srv.URL, KeyNames: parseKeyNames("principal,\n" + long + "\n")}
	_, _, out := getKeysReport(t, newApp(cfg))
	if out.Keys[0].Name != "principal" || out.Keys[1].Name != long[:40] || out.Keys[2].Name != "" {
		t.Fatalf("names: %q %q %q", out.Keys[0].Name, out.Keys[1].Name, out.Keys[2].Name)
	}
	if got := parseKeyNames("a\r\nb, c"); got[0] != "ab" || got[1] != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestAdminKeysAuthAndMethod(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1"}})
	req := httptest.NewRequest(http.MethodGet, "/admin/keys", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid_api_key") {
		t.Fatalf("no bearer: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/admin/keys", nil)
	req.Header.Set("Authorization", "Bearer p")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST should not match (same as other admin routes), got %d", rec.Code)
	}
}

func getStatusPage(app *App) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	return rec
}

func TestStatusPageOpenAndNoKeyLeak(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-SEGREDO1", "sk-bad-SEGREDO2"}, KeyNames: []string{"principal"}, UsageURL: srv.URL, UsageCacheTTL: time.Minute, RetryExhaustedAfter: time.Hour})
	app.keys.MarkExhausted(1)
	rec := getStatusPage(app)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("got %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control: %q", rec.Header().Get("Cache-Control"))
	}
	for _, want := range []string{"principal", "#2", "exhausted", "<td>12.5%</td>", "<td>40.0%</td>", "<td>7.0%</td>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	rows := strings.Split(body, "<tr>")
	if !strings.Contains(rows[2], "(atual)") || regexp.MustCompile(`<td>\d+s</td>`).MatchString(rows[2]) {
		t.Fatalf("current key row: %s", rows[2])
	}
	if !regexp.MustCompile(`<td>\d+s</td>`).MatchString(rows[3]) || strings.Contains(rows[3], "(atual)") {
		t.Fatalf("exhausted key row: %s", rows[3])
	}
	head := httptest.NewRecorder()
	app.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/status", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD /status: %d", head.Code)
	}
	for _, secret := range []string{"SEGREDO1", "SEGREDO2", "sk-good", "sk-bad"} {
		if strings.Contains(body, secret) {
			t.Fatalf("leaked %q: %s", secret, body)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/keys", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/admin/keys without key: %d", rec.Code)
	}
}

func TestStatusPageUsageErrorOnKeyRow(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-bad-1", "sk-good-2"}, UsageURL: srv.URL})
	rec := getStatusPage(app)
	body := rec.Body.String()
	rows := strings.Split(body, "<tr>")
	if rec.Code != http.StatusOK || len(rows) != 4 {
		t.Fatalf("got %d %s", rec.Code, body)
	}
	if !strings.Contains(rows[2], "erro de uso: HTTP 401") || strings.Contains(rows[2], "%</td>") {
		t.Fatalf("key #1 row: %s", rows[2])
	}
	if !strings.Contains(rows[3], "<td>12.5%</td>") || strings.Contains(rows[3], "erro de uso") {
		t.Fatalf("key #2 row: %s", rows[3])
	}
}

func TestStatusPageEscapesKeyName(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1"}, KeyNames: []string{"<b>x</b>"}, UsageURL: srv.URL})
	body := getStatusPage(app).Body.String()
	if !strings.Contains(body, "&lt;b&gt;x&lt;/b&gt;") || strings.Contains(body, "<b>x</b>") {
		t.Fatalf("name not escaped: %s", body)
	}
}

func TestStatusPagePartialWindows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"weekly":{"percent":40}}}`))
	}))
	t.Cleanup(srv.Close)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1"}, UsageURL: srv.URL})
	rec := getStatusPage(app)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<td>40.0%</td>") || !strings.Contains(body, "<td>—</td>") {
		t.Fatalf("got %d %s", rec.Code, body)
	}
}

func TestUsageForCancelledRequestDoesNotPoisonCache(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			<-r.Context().Done() // slow upstream: hold until the client gives up
			return
		}
		_, _ = w.Write([]byte(fakeUsageBody))
	}))
	t.Cleanup(srv.Close)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1"}, UsageURL: srv.URL, UsageCacheTTL: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if got := app.usageFor(ctx); len(got) != 1 || got[0].err == "" {
		t.Fatalf("expected a cancel error for this response only: %+v", got)
	}
	got := app.usageFor(context.Background())
	if got[0].err != "" || got[0].usage == nil || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected a refetch after cancel: %+v hits=%d", got, atomic.LoadInt32(&hits))
	}
}

func TestUsageCacheTTLEnv(t *testing.T) {
	t.Setenv("USAGE_CACHE_TTL", "5x")
	cfg := defaultConfig()
	applyEnvOverrides(&cfg)
	cfg.ProxyAPIKey, cfg.UpstreamAPIKeys = "p", []string{"k"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected invalid USAGE_CACHE_TTL to fail validation")
	}
	if d := defaultConfig(); d.UsageCacheTTL != 60*time.Second || d.UsageURL != "https://opencode.ai/zen/go/v1/usage" {
		t.Fatalf("defaults: %+v", d)
	}
}

func tiersYAML(t *testing.T, tiers string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "server: {proxy_api_key: p}\nupstream: {api_keys: [u]}\n" + tiers
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWITCHBOARD_GO_CONFIG", path)
}

const tierOK = `  - name: a
    base_url: https://a
    api_key: secret-a
    free: true
    models:
      m1: {id: x/m1, context: 100, tools: true}
`

func TestLoadConfigTiers(t *testing.T) {
	tiersYAML(t, "tiers:\n"+tierOK+`  - name: b
    base_url: https://b
    api_key: secret-b
    free: true
    styles: [anthropic]
    models:
      m2: {id: y/m2}
`)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tiers) != 2 || len(cfg.Tiers[0].Styles) != 1 || cfg.Tiers[0].Styles[0] != APIStyleOpenAI || cfg.Tiers[1].Styles[0] != APIStyleAnthropic {
		t.Fatalf("unexpected tiers: %+v", cfg.Tiers)
	}
	if m := cfg.Tiers[0].Models[0]; m.Name != "m1" || m.ID != "x/m1" || m.Context != 100 || !m.Tools {
		t.Fatalf("unexpected model: %+v", m)
	}
	s := safeConfigSummary(cfg)
	if !strings.Contains(s, "tiers=2") || strings.Contains(s, "secret-") {
		t.Fatalf("bad summary: %s", s)
	}
}

func TestLoadConfigTierModelIDDefaultsToName(t *testing.T) {
	tiersYAML(t, "tiers:\n"+strings.Replace(tierOK, "{id: x/m1, context: 100, tools: true}", "{context: 100}", 1))
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if m := cfg.Tiers[0].Models[0]; m.ID != "m1" {
		t.Fatalf("ID = %q, want m1", m.ID)
	}
}

func TestLoadConfigWithoutTiers(t *testing.T) {
	tiersYAML(t, "")
	cfg, err := loadConfig()
	if err != nil || len(cfg.Tiers) != 0 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadConfigTiersRejected(t *testing.T) {
	cases := map[string]struct{ tier, want string }{
		"paid":             {strings.Replace(tierOK, "free: true", "free: false", 1), "free must be true"},
		"free absent":      {strings.Replace(tierOK, "    free: true\n", "", 1), "free must be true"},
		"no base_url":      {strings.Replace(tierOK, "base_url: https://a", "base_url: \"\"", 1), "base_url is required"},
		"no api_key":       {strings.Replace(tierOK, "api_key: secret-a", "api_key: \"\"", 1), "api_key is required"},
		"no name":          {strings.Replace(tierOK, "name: a", "name: \"\"", 1), "name is required"},
		"dup tier name":    {tierOK + tierOK, "duplicate name"},
		"dup model":        {tierOK + "      m1: {id: x/other}\n", "duplicate model"},
		"bad style":        {tierOK + "    styles: [grpc]\n", "unknown style"},
		"no models":        {strings.Replace(tierOK, "    models:\n      m1: {id: x/m1, context: 100, tools: true}\n", "", 1), "at least one model"},
		"neg context":      {strings.Replace(tierOK, "context: 100", "context: -1", 1), "context must be >= 0"},
		"scalar model":     {strings.Replace(tierOK, "{id: x/m1, context: 100, tools: true}", "5", 1), "parse tiers[0].models"},
		"models not map":   {strings.Replace(tierOK, "    models:\n      m1: {id: x/m1, context: 100, tools: true}\n", "    models: [a]\n", 1), "must be a map"},
		"bad cooldown":     {tierOK + "    cooldown: soon\n", "parse tiers[0].cooldown"},
		"neg cooldown":     {strings.Replace(tierOK, "    free: true\n", "    free: true\n    cooldown: -1s\n", 1), "cooldown must be >= 0"},
		"dup name trimmed": {tierOK + strings.Replace(tierOK, "name: a", "name: \"a \"", 1), "duplicate name"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tiersYAML(t, "tiers:\n"+c.tier)
			_, err := loadConfig()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want containing %q", err, c.want)
			}
			if strings.Contains(err.Error(), "secret-a") {
				t.Fatalf("error leaks api_key: %v", err)
			}
		})
	}
}

const quotaBody = `{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`

type tierFixture struct {
	app        *App
	t0, t1, t2 *int32
	mu         sync.Mutex
	bodies     []string
	auths      []string
}

func (f *tierFixture) seen() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...), append([]string(nil), f.auths...)
}

// newTierFixture wires a tier 0 that is always quota-exhausted (unless t0OK) and tiers 1 and 2.
// Tier 1 maps a -> id-a (openai only); tier 2 maps a, speaks anthropic and openai.
// Tiers 1 and 2 answer 200 with their own name unless t1Quota makes tier 1 answer a quota 429.
func newTierFixture(t *testing.T, t0OK, t1Quota bool) *tierFixture {
	t.Helper()
	f := &tierFixture{t0: new(int32), t1: new(int32), t2: new(int32)}
	mk := func(name string, hits *int32, quota, record bool) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(hits, 1)
			b, _ := io.ReadAll(r.Body)
			if record {
				f.mu.Lock()
				f.bodies = append(f.bodies, string(b))
				f.auths = append(f.auths, r.Header.Get("Authorization")+"|"+r.Header.Get("x-api-key")+"|"+r.Header.Get("anthropic-version"))
				f.mu.Unlock()
			}
			if quota {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(quotaBody))
				return
			}
			_, _ = w.Write([]byte(`{"from":"` + name + `"}`))
		}))
		t.Cleanup(s.Close)
		return s
	}
	s0, s1, s2 := mk("t0", f.t0, !t0OK, false), mk("t1", f.t1, t1Quota, !t1Quota), mk("t2", f.t2, false, true)
	f.app = newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: s0.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, Tiers: []TierConfig{
		{Name: "t1", BaseURL: s1.URL, APIKey: "k1", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "a", ID: "id-a"}}},
		{Name: "t2", BaseURL: s2.URL, APIKey: "k2", Free: true, Styles: []APIStyle{APIStyleOpenAI, APIStyleAnthropic}, Models: []TierModel{{Name: "a", ID: "id-a2"}}},
	}})
	return f
}

// hits reads the three upstream counters atomically.
func (f *tierFixture) hits() [3]int32 {
	return [3]int32{atomic.LoadInt32(f.t0), atomic.LoadInt32(f.t1), atomic.LoadInt32(f.t2)}
}

func tierReq(app *App, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer p")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestTierFailoverRewritesModelAndKey(t *testing.T) {
	f := newTierFixture(t, false, false)
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a","n":1.50,"messages":[]}`, nil)
	bodies, auths := f.seen()
	if h := f.hits(); rec.Code != http.StatusOK || h[1] != 1 || h[2] != 0 {
		t.Fatalf("code=%d hits=%v", rec.Code, h)
	}
	if rec.Body.String() != `{"from":"t1"}` {
		t.Fatalf("client body %q", rec.Body.String())
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"model":"id-a"`) || !strings.Contains(bodies[0], `"n":1.50`) || auths[0] != "Bearer k1||" {
		t.Fatalf("body=%v auth=%v", bodies, auths)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatal("unexpected Retry-After on tier-served response")
	}
	// the one-shot flag is still armed: tier 0 is exhausted and nothing consumed it
	if !f.app.keys.ShouldNotifyAllExhausted() {
		t.Fatal("all-exhausted notification was consumed by a tier-served request")
	}
}

func TestTierNoMappingOrNoModelStaysLocal429(t *testing.T) {
	f := newTierFixture(t, false, false)
	// exhaust tier 0 first (one hit); the cases below are then all served locally
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"z"}`, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("setup: %d", rec.Code)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/chat/completions", `{"model":"z"}`},
		{http.MethodGet, "/v1/models", ``},
		{http.MethodPost, "/v1/chat/completions", `{"messages":[]}`},
		{http.MethodPost, "/v1/chat/completions", `not json`},
	} {
		rec := tierReq(f.app, c.method, c.path, c.body, nil)
		if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "all upstream keys exhausted") {
			t.Fatalf("%v: %d %s", c, rec.Code, rec.Body.String())
		}
	}
	if h := f.hits(); h != [3]int32{1, 0, 0} {
		t.Fatalf("hits %v", h)
	}
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"z"}`, nil); rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestTierSkippedWhenStyleMissing(t *testing.T) {
	f := newTierFixture(t, false, false)
	rec := tierReq(f.app, http.MethodPost, "/v1/messages", `{"model":"a"}`, map[string]string{"anthropic-version": "2023-06-01"})
	bodies, auths := f.seen()
	if h := f.hits(); rec.Code != http.StatusOK || h[1] != 0 || h[2] != 1 {
		t.Fatalf("code=%d hits=%v", rec.Code, h)
	}
	if auths[0] != "|k2|2023-06-01" || !strings.Contains(bodies[0], `"id-a2"`) {
		t.Fatalf("body=%v auth=%v", bodies, auths)
	}
	// with only the openai-only tier left, nothing serves the anthropic request
	f.app.config.Tiers = f.app.config.Tiers[:1]
	rec = tierReq(f.app, http.MethodPost, "/v1/messages", `{"model":"a"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestTierAuthFollowsRequestStyle(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers = f.app.config.Tiers[1:] // only t2, which speaks both styles
	tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	tierReq(f.app, http.MethodPost, "/v1/messages", `{"model":"a"}`, map[string]string{"anthropic-version": "2023-06-01"})
	_, auths := f.seen()
	if len(auths) != 2 || auths[0] != "Bearer k2||" || auths[1] != "|k2|2023-06-01" {
		t.Fatalf("auths %q", auths)
	}
}

func TestNoCompatibleTierReturnsLocal429InRequestStyle(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers = f.app.config.Tiers[:1] // only t1, openai only
	rec := tierReq(f.app, http.MethodPost, "/v1/messages", `{"model":"a"}`, map[string]string{"anthropic-version": "2023-06-01"})
	var got struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusTooManyRequests || got.Type != "error" || got.Error.Type != "rate_limit_error" || f.hits()[1] != 0 {
		t.Fatalf("code=%d body=%q hits=%v", rec.Code, rec.Body.String(), f.hits())
	}
}

func TestTierExhaustedFallsToNextTier(t *testing.T) {
	f := newTierFixture(t, false, true)
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if h := f.hits(); rec.Code != http.StatusOK || h[1] != 1 || h[2] != 1 {
		t.Fatalf("code=%d hits=%v", rec.Code, h)
	}
	f.app.config.Tiers = f.app.config.Tiers[:1]
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestTierNotCalledWhenTier0Healthy(t *testing.T) {
	f := newTierFixture(t, true, false)
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if h := f.hits(); rec.Code != http.StatusOK || h[1] != 0 || h[2] != 0 {
		t.Fatalf("code=%d hits=%v", rec.Code, h)
	}
}

func TestTier400CopiedWithoutFailoverOrCooldown(t *testing.T) {
	f := newTierFixture(t, false, false)
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("tier1 says no"))
	}))
	defer s1.Close()
	f.app.config.Tiers[0].BaseURL = s1.URL
	for i := 0; i < 2; i++ {
		rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
		if rec.Code != http.StatusBadRequest || rec.Body.String() != "tier1 says no" || f.hits()[2] != 0 {
			t.Fatalf("got %d %q hits=%v", rec.Code, rec.Body.String(), f.hits())
		}
	}
}

func TestTierPlain429FailsOverToNextTier(t *testing.T) {
	f := newTierFixture(t, false, false)
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limit"}}`))
	}))
	defer s1.Close()
	f.app.config.Tiers[0].BaseURL = s1.URL
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"from":"t2"}` || f.hits()[2] != 1 {
		t.Fatalf("got %d %q hits=%v", rec.Code, rec.Body.String(), f.hits())
	}
}

func TestTierBaseURLWithPathPrefixAndTrailingSlash(t *testing.T) {
	f := newTierFixture(t, false, false)
	var path string
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer s1.Close()
	f.app.config.Tiers[0].BaseURL = s1.URL + "/api/v1/"
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != http.StatusOK || path != "/api/v1/chat/completions" {
		t.Fatalf("code=%d path=%q", rec.Code, path)
	}
}

func TestTierEmptyIDForwardsName(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers[0].Models = []TierModel{{Name: "a"}}
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	bodies, _ := f.seen()
	if rec.Code != http.StatusOK || len(bodies) != 1 || !strings.Contains(bodies[0], `"model":"a"`) {
		t.Fatalf("code=%d bodies=%v", rec.Code, bodies)
	}
}

func TestTierConnectionErrorFailsOverWithoutCooldown(t *testing.T) {
	f := newTierFixture(t, false, false)
	dead := httptest.NewServer(http.NotFoundHandler())
	f.app.config.Tiers[0].BaseURL = dead.URL
	dead.Close()
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != http.StatusOK || f.hits()[2] != 1 || f.app.cooldowns.remaining("t1") != 0 {
		t.Fatalf("got %d hits=%v", rec.Code, f.hits())
	}
	// every extra tier unreachable: 502 that does not leak the tier URL
	f.app.config.Tiers[1].BaseURL = dead.URL
	rec = tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), dead.URL) {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestAdminAndStatusUnchangedWithTiers(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-1"}, KeyNames: []string{"x"}, UsageURL: srv.URL, RetryExhaustedAfter: time.Hour}
	plain := newApp(cfg)
	cfg.Tiers = []TierConfig{{Name: "t", BaseURL: "http://127.0.0.1:1", APIKey: "kt", Free: true, Models: []TierModel{{Name: "a", ID: "a"}}}}
	tiered := newApp(cfg)
	for _, path := range []string{"/admin/status", "/admin/keys"} {
		a, b := tierReq(plain, http.MethodGet, path, "", nil), tierReq(tiered, http.MethodGet, path, "", nil)
		if a.Code != b.Code || a.Body.String() != b.Body.String() {
			t.Fatalf("%s differs: %d %q vs %d %q", path, a.Code, a.Body.String(), b.Code, b.Body.String())
		}
	}
}

func TestStatusPageTiersSection(t *testing.T) {
	var hits int32
	srv := fakeUsageServer(t, &hits)
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-good-SEGREDO1"}, UsageURL: srv.URL, RetryExhaustedAfter: time.Hour}
	if body := getStatusPage(newApp(cfg)).Body.String(); strings.Contains(body, "<h2>Tiers</h2>") {
		t.Fatalf("tiers section without extra tiers: %s", body)
	}
	cfg.Tiers = []TierConfig{
		{Name: "gratis-a", BaseURL: "http://segredo-a.invalid", APIKey: "kt-SEGREDO2", Free: true, Models: []TierModel{{Name: "a", ID: "a"}}},
		{Name: "gratis-b", BaseURL: "http://segredo-b.invalid", APIKey: "kt-SEGREDO3", Free: true, Models: []TierModel{{Name: "a", ID: "a"}}},
	}
	app := newApp(cfg)
	app.cooldowns.set("gratis-a", 90*time.Second)
	rec := getStatusPage(app) // no Authorization header: the page is open
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	rows := strings.Split(body[strings.Index(body, "<h2>Tiers</h2>"):], "<tr>")
	if len(rows) != 5 || !strings.Contains(rows[1], "<th>Tier</th>") {
		t.Fatalf("tiers table: %v", rows)
	}
	for i, want := range []string{"OpenCode Go (tier 0)</td><td>ok</td><td>—</td>", "gratis-a</td><td>cooldown</td><td>90s</td>", "gratis-b</td><td>ok</td><td>—</td>"} {
		if !strings.Contains(rows[i+2], want) {
			t.Fatalf("row %d: want %q in %s", i+2, want, rows[i+2])
		}
	}
	app.keys.MarkExhausted(0)
	if row := strings.Split(getStatusPage(app).Body.String(), "<h2>Tiers</h2>")[1]; !regexp.MustCompile(`tier 0\)</td><td>cooldown</td><td>\d+s</td>`).MatchString(row) {
		t.Fatalf("tier 0 with every key exhausted: %s", row)
	}
	if i := strings.Index(body, "#1"); i < 0 || i > strings.Index(body, "<h2>Tiers</h2>") {
		t.Fatal("tiers section must come below the keys")
	}
	for _, secret := range []string{"SEGREDO1", "SEGREDO2", "SEGREDO3", "sk-good", "kt-", "segredo-a", "segredo-b"} {
		if strings.Contains(body, secret) {
			t.Fatalf("leaked %q: %s", secret, body)
		}
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLog routes the app's request log into a buffer and returns a reader of the parsed lines.
func captureLog(app *App) func(t *testing.T) []map[string]any {
	buf := &lockedBuf{}
	app.logger = slog.New(slog.NewJSONHandler(buf, nil))
	return func(t *testing.T) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if ln == "" {
				continue
			}
			m := map[string]any{}
			if err := json.Unmarshal([]byte(ln), &m); err != nil {
				t.Fatalf("bad log line %q: %v", ln, err)
			}
			out = append(out, m)
		}
		return out
	}
}

func TestRequestLogTier0AndTier1(t *testing.T) {
	s0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer secret-key-1" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(quotaBody))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer s0.Close()
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"from":"t1"}`))
	}))
	defer s1.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"secret-key-1", "secret-key-2"}, KeyNames: []string{"first", "second"}, UpstreamBaseURL: s0.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, Tiers: []TierConfig{
		{Name: "t1", BaseURL: s1.URL, APIKey: "secret-tier-key", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "a"}}},
	}})
	lines := captureLog(app)
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"glm-5.1"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("tier0: %d", rec.Code)
	}
	app.keys.MarkExhausted(1) // tier 0 is now fully exhausted
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("tier1: %d", rec.Code)
	}
	got := lines(t)
	if len(got) != 2 {
		t.Fatalf("lines: %v", got)
	}
	l0, l1 := got[0], got[1]
	if l0["msg"] != "request" || l0["model"] != "glm-5.1" || l0["api_style"] != "openai" || l0["tier"] != "opencode-go" ||
		l0["key_index"] != float64(1) || l0["key_name"] != "second" || l0["status"] != float64(200) || l0["latency_ms"] == nil {
		t.Fatalf("tier0 line: %v", l0)
	}
	if l1["model"] != "a" || l1["tier"] != "t1" || l1["status"] != float64(200) {
		t.Fatalf("tier1 line: %v", l1)
	}
	if _, ok := l1["key_index"]; ok {
		t.Fatalf("tier1 line has key_index: %v", l1)
	}
	if _, ok := l1["key_name"]; ok {
		t.Fatalf("tier1 line has key_name: %v", l1)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"secret-key-1", "secret-key-2", "secret-tier-key"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("log leaks %q: %s", secret, raw)
		}
	}
}

func TestRequestLogStreamLatencyCoversWholeStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("data: 2\n\n"))
	}))
	defer up.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour})
	lines := captureLog(app)
	if rec := doProxyReq(app); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "data: 2") || !rec.Flushed {
		t.Fatalf("code=%d flushed=%v body=%q", rec.Code, rec.Flushed, rec.Body.String())
	}
	got := lines(t)
	if len(got) != 1 || got[0]["latency_ms"].(float64) < 200 {
		t.Fatalf("lines: %v", got)
	}
}

func TestRequestLogLocal429And502HaveNoTier(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers = nil
	lines := captureLog(f.app)
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("setup: %d", rec.Code)
	}
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("429: %d", rec.Code)
	}
	dead := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: "http://127.0.0.1:1", MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour})
	deadLines := captureLog(dead)
	if rec := doProxyReq(dead); rec.Code != http.StatusBadGateway {
		t.Fatalf("502: %d", rec.Code)
	}
	// tier 0 quota-exhausted, then the tier upstream is unreachable
	tierDead := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: f.app.config.UpstreamBaseURL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, Tiers: []TierConfig{
		{Name: "t1", BaseURL: "http://127.0.0.1:1", APIKey: "k1", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "a"}}},
	}})
	tierDeadLines := captureLog(tierDead)
	if rec := tierReq(tierDead, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("tier 502: %d", rec.Code)
	}
	for i, got := range [][]map[string]any{lines(t), deadLines(t), tierDeadLines(t)} {
		l := got[len(got)-1]
		want := []float64{429, 502, 502}[i]
		if l["tier"] != "" || l["status"] != want {
			t.Fatalf("line %d: %v", i, l)
		}
		if _, ok := l["key_index"]; ok {
			t.Fatalf("line %d has key_index: %v", i, l)
		}
	}
}

func TestRequestLogAnthropicStyle(t *testing.T) {
	s0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(quotaBody))
	}))
	defer s0.Close()
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer s1.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: s0.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, Tiers: []TierConfig{
		{Name: "t1", BaseURL: s1.URL, APIKey: "tk", Free: true, Styles: []APIStyle{APIStyleAnthropic}, Models: []TierModel{{Name: "m"}}},
	}})
	lines := captureLog(app)
	if rec := tierReq(app, http.MethodPost, "/v1/messages", `{"model":"m"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	got := lines(t)
	if len(got) != 1 || got[0]["api_style"] != "anthropic" || got[0]["tier"] != "t1" || got[0]["status"] != float64(200) {
		t.Fatalf("lines: %v", got)
	}
	if _, ok := got[0]["key_index"]; ok {
		t.Fatalf("tier line has key_index: %v", got[0])
	}
}

func TestRequestLogTierNamedLikeTier0HasNoKeyFields(t *testing.T) {
	s0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(quotaBody))
	}))
	defer s0.Close()
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer s1.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, KeyNames: []string{"n"}, UpstreamBaseURL: s0.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, Tiers: []TierConfig{
		{Name: "opencode-go", BaseURL: s1.URL, APIKey: "tk", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "m"}}},
	}})
	lines := captureLog(app)
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"m"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	got := lines(t)
	if len(got) != 1 || got[0]["tier"] != "opencode-go" {
		t.Fatalf("lines: %v", got)
	}
	for _, k := range []string{"key_index", "key_name"} {
		if _, ok := got[0][k]; ok {
			t.Fatalf("extra tier line has %s: %v", k, got[0])
		}
	}
}

func TestRequestBodyTooLargeStill413(t *testing.T) {
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: "http://127.0.0.1:1", MaxRequestBodyBytes: 8, RetryExhaustedAfter: time.Hour})
	lines := captureLog(app)
	srv := httptest.NewServer(app)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"glm-5.1","messages":[]}`))
	req.Header.Set("Authorization", "Bearer p")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	// The connection is closed after a 413 only when MaxBytesReader wraps the original writer.
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !resp.Close {
		t.Fatalf("code=%d close=%v", resp.StatusCode, resp.Close)
	}
	// The log line is written after the handler returns, which can be after the client has the response.
	var got []map[string]any
	for deadline := time.Now().Add(2 * time.Second); len(got) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got = lines(t)
	}
	if len(got) != 1 || got[0]["status"] != float64(413) || got[0]["model"] != "" || got[0]["tier"] != "" {
		t.Fatalf("lines: %v", got)
	}
}

func TestRequestLogKeyZeroIsLogged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer up.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, KeyNames: []string{"first"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour})
	lines := captureLog(app)
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"m"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	got := lines(t)
	if len(got) != 1 || got[0]["key_index"] != float64(0) || got[0]["key_name"] != "first" {
		t.Fatalf("lines: %v", got)
	}
}

func TestRequestLogModelIsTruncated(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer up.Close()
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour})
	lines := captureLog(app)
	// 127 ASCII bytes then a 2-byte rune straddling the 128-byte cut.
	long := strings.Repeat("a", 127) + strings.Repeat("é", 500)
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"`+long+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	got := lines(t)
	if len(got) != 1 || got[0]["model"] != strings.Repeat("a", 127) {
		t.Fatalf("lines: %v", got)
	}
}

// stub is an upstream that answers a fixed status and body and counts its hits.
type stub struct {
	srv    *httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	body   atomic.Value
}

func newStub(t *testing.T, status int, body string) *stub {
	t.Helper()
	st := &stub{}
	st.status.Store(int32(status))
	st.body.Store(body)
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		w.WriteHeader(int(st.status.Load()))
		_, _ = w.Write([]byte(st.body.Load().(string)))
	}))
	t.Cleanup(st.srv.Close)
	return st
}

// failoverApp wires tier 0 (two keys) and extra tiers t1 and t2 (both map "a") to the stubs,
// with a fake clock shared by the key manager and the tier cooldowns.
func failoverApp(t *testing.T, s0, s1, s2 *stub, retryAfter time.Duration) (*App, *time.Time) {
	t.Helper()
	return failoverAppTimeout(t, s0, s1, s2, retryAfter, 0)
}

// failoverAppTimeout is failoverApp with the upstream response header timeout set through config.
func failoverAppTimeout(t *testing.T, s0, s1, s2 *stub, retryAfter, headerTimeout time.Duration) (*App, *time.Time) {
	t.Helper()
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u1", "u2"}, UpstreamBaseURL: s0.srv.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: retryAfter, UpstreamResponseHeaderTimeout: headerTimeout}
	cfg.Tiers = append(cfg.Tiers, TierConfig{Name: "t1", BaseURL: s1.srv.URL, APIKey: "k1", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "a"}}})
	if s2 != nil {
		cfg.Tiers = append(cfg.Tiers, TierConfig{Name: "t2", BaseURL: s2.srv.URL, APIKey: "k2", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "a"}}})
	}
	app := newApp(cfg)
	now := time.Unix(10000, 0).UTC()
	app.keys.now = func() time.Time { return now }
	app.cooldowns.now = func() time.Time { return now }
	return app, &now
}

func chatReq(app *App, body string) *httptest.ResponseRecorder {
	return tierReq(app, http.MethodPost, "/v1/chat/completions", body, nil)
}

func TestFailoverTier0FailureStatusesGoToNextTier(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
	}{
		{401, `{"error":"bad key"}`}, {402, `{"error":"pay"}`}, {404, `{"error":"route gone"}`},
		{500, `oops`}, {503, `oops`}, {429, `{"error":{"code":"rate_limit"}}`},
	} {
		t.Run(strconv.Itoa(c.status), func(t *testing.T) {
			s0, s1 := newStub(t, c.status, c.body), newStub(t, 200, `{"from":"t1"}`)
			app, _ := failoverApp(t, s0, s1, nil, time.Hour)
			for i := 1; i <= 2; i++ {
				rec := chatReq(app, `{"model":"a"}`)
				if rec.Code != 200 || rec.Body.String() != `{"from":"t1"}` || s0.hits.Load() != 1 || s1.hits.Load() != int32(i) {
					t.Fatalf("req %d: %d %q hits=%d/%d", i, rec.Code, rec.Body.String(), s0.hits.Load(), s1.hits.Load())
				}
			}
			// the failure is tier-wide: no key was marked exhausted
			for _, k := range app.keys.Status().Keys {
				if k.State == string(KeyExhausted) {
					t.Fatalf("key exhausted: %+v", k)
				}
			}
		})
	}
}

func TestFailoverExtraTierFailureStatusesGoToNextTier(t *testing.T) {
	for _, status := range []int{401, 402, 404, 500, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s0, s1, s2 := newStub(t, 500, `x`), newStub(t, status, `{"error":"nope"}`), newStub(t, 200, `{"from":"t2"}`)
			app, _ := failoverApp(t, s0, s1, s2, time.Hour)
			for i := 1; i <= 2; i++ {
				rec := chatReq(app, `{"model":"a"}`)
				if rec.Code != 200 || rec.Body.String() != `{"from":"t2"}` || s1.hits.Load() != 1 || s2.hits.Load() != int32(i) {
					t.Fatalf("req %d: %d %q hits=%d/%d", i, rec.Code, rec.Body.String(), s1.hits.Load(), s2.hits.Load())
				}
			}
		})
	}
}

func TestFailover400And404MissingModelPassThroughWithoutCooldown(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
	}{
		{400, `bad request`}, {403, `forbidden`}, {404, `{"error":{"message":"The model gpt-x does not exist"}}`}, {404, `Model not found`},
	} {
		s0, s1 := newStub(t, c.status, c.body), newStub(t, 200, `{"from":"t1"}`)
		app, _ := failoverApp(t, s0, s1, nil, time.Hour)
		for i := 1; i <= 2; i++ {
			rec := chatReq(app, `{"model":"a"}`)
			if rec.Code != c.status || rec.Body.String() != c.body || s0.hits.Load() != int32(i) || s1.hits.Load() != 0 {
				t.Fatalf("%d %q req %d: got %d %q hits=%d/%d", c.status, c.body, i, rec.Code, rec.Body.String(), s0.hits.Load(), s1.hits.Load())
			}
		}
	}
	// a 404 from an extra tier that says the model is missing also goes to the client
	s0, s1, s2 := newStub(t, 500, `x`), newStub(t, 404, `model not found`), newStub(t, 200, `{"from":"t2"}`)
	app, _ := failoverApp(t, s0, s1, s2, time.Hour)
	for i := 1; i <= 2; i++ {
		rec := chatReq(app, `{"model":"a"}`)
		if i == 1 && (rec.Code != 404 || s2.hits.Load() != 0) {
			t.Fatalf("tier 404: %d hits=%d", rec.Code, s2.hits.Load())
		}
	}
	if s1.hits.Load() != 2 {
		t.Fatalf("tier 1 was put in cooldown by a model 404: %d hits", s1.hits.Load())
	}
}

func TestFailover404WithoutRequestModelPassesThrough(t *testing.T) {
	s0, s1 := newStub(t, 404, `{"error":"route gone"}`), newStub(t, 200, `{"from":"t1"}`)
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	for i := 1; i <= 2; i++ {
		rec := chatReq(app, `{"messages":[]}`)
		if rec.Code != 404 || s0.hits.Load() != int32(i) || s1.hits.Load() != 0 {
			t.Fatalf("got %d hits=%d/%d", rec.Code, s0.hits.Load(), s1.hits.Load())
		}
	}
}

func TestFailoverStartedStreamKeepsTier(t *testing.T) {
	s0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // cut the stream after the first event
	}))
	defer s0.Close()
	s1 := newStub(t, 200, `{"from":"t1"}`)
	app, _ := failoverApp(t, &stub{srv: s0}, s1, nil, time.Hour)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data: one") || s1.hits.Load() != 0 {
		t.Fatalf("got %d %q t1 hits=%d", rec.Code, rec.Body.String(), s1.hits.Load())
	}
	if app.cooldowns.remaining(tier0CooldownKey) != 0 {
		t.Fatal("tier 0 in cooldown after a stream that started")
	}
}

func TestFailoverCooldownExpires(t *testing.T) {
	s0, s1 := newStub(t, 500, `x`), newStub(t, 200, `{"from":"t1"}`)
	app, now := failoverApp(t, s0, s1, nil, time.Minute)
	chatReq(app, `{"model":"a"}`)
	*now = now.Add(30 * time.Second)
	chatReq(app, `{"model":"a"}`)
	if s0.hits.Load() != 1 {
		t.Fatalf("tier 0 called during cooldown: %d", s0.hits.Load())
	}
	s0.status.Store(200)
	s0.body.Store(`{"from":"t0"}`)
	*now = now.Add(31 * time.Second)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Body.String() != `{"from":"t0"}` || s0.hits.Load() != 2 {
		t.Fatalf("tier 0 not retried after cooldown: %q hits=%d", rec.Body.String(), s0.hits.Load())
	}
}

func TestFailoverAllFailServesLastResponseAndLogsItsTier(t *testing.T) {
	s0, s1, s2 := newStub(t, 500, `from t0`), newStub(t, 502, `from t1`), newStub(t, 503, `from t2`)
	app, _ := failoverApp(t, s0, s1, s2, time.Hour)
	lines := captureLog(app)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Code != 503 || rec.Body.String() != "from t2" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	for _, name := range []string{tier0CooldownKey, "t1", "t2"} {
		if app.cooldowns.remaining(name) == 0 {
			t.Fatalf("%s not in cooldown", name)
		}
	}
	if l := lines(t); l[len(l)-1]["tier"] != "t2" || l[len(l)-1]["status"] != float64(503) {
		t.Fatalf("log %v", l)
	}
	// without a model only tier 0 can answer, so its failure response is the last one
	s0b, s1b := newStub(t, 500, `from t0`), newStub(t, 200, `x`)
	app2, _ := failoverApp(t, s0b, s1b, nil, time.Hour)
	lines2 := captureLog(app2)
	rec = chatReq(app2, `{"messages":[]}`)
	if rec.Code != 500 || rec.Body.String() != "from t0" || s1b.hits.Load() != 0 {
		t.Fatalf("no model: %d %q", rec.Code, rec.Body.String())
	}
	if l := lines2(t); l[len(l)-1]["tier"] != tier0Name || l[len(l)-1]["key_index"] != float64(0) {
		t.Fatalf("log %v", l)
	}
}

func TestFailoverTier0WithoutExtraTiersCooldownAndRetry(t *testing.T) {
	s0, s1 := newStub(t, 500, `from t0`), newStub(t, 200, `x`)
	app, now := failoverApp(t, s0, s1, nil, time.Minute)
	app.config.Tiers = nil
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Code != 500 || rec.Body.String() != "from t0" {
		t.Fatalf("first: %d %q", rec.Code, rec.Body.String())
	}
	rec = chatReq(app, `{"model":"a"}`)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "60" || s0.hits.Load() != 1 {
		t.Fatalf("second: %d RA=%q hits=%d", rec.Code, rec.Header().Get("Retry-After"), s0.hits.Load())
	}
	*now = now.Add(61 * time.Second)
	chatReq(app, `{"model":"a"}`)
	if s0.hits.Load() != 2 {
		t.Fatalf("tier 0 not retried after cooldown: %d hits", s0.hits.Load())
	}
}

func TestFailoverHeldTier0FailureThenUnreachableTierGoesOn(t *testing.T) {
	s0, s1, s2 := newStub(t, 500, `from t0`), newStub(t, 200, `x`), newStub(t, 200, `{"from":"t2"}`)
	s1.srv.Close() // t1 is unreachable: connection refused
	app, _ := failoverApp(t, s0, s1, s2, time.Hour)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Code != 200 || rec.Body.String() != `{"from":"t2"}` || app.cooldowns.remaining("t1") != 0 {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	// no tier left after the unreachable one: the held tier 0 response is served
	s2.srv.Close()
	app, _ = failoverApp(t, newStub(t, 500, `from t0`), s1, nil, time.Hour)
	rec = chatReq(app, `{"model":"a"}`)
	if rec.Code != 500 || rec.Body.String() != "from t0" {
		t.Fatalf("held: %d %q", rec.Code, rec.Body.String())
	}
}

// slowStub is an upstream that counts its hits and never answers until the client gives up.
func slowStub(t *testing.T) *stub {
	t.Helper()
	st := &stub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client closing
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func TestFailoverConnectionErrorTier0(t *testing.T) {
	s1 := newStub(t, 200, `{"from":"t1"}`)
	// s0 accepts the connection and drops it, so the client sees a connection error.
	s0 := &stub{}
	s0.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s0.hits.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(s0.srv.Close)
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	rec := chatReq(app, `{"model":"a"}`)
	idx, _, ok := app.keys.Current()
	if rec.Body.String() != `{"from":"t1"}` || app.cooldowns.remaining(tier0CooldownKey) != 0 || !ok || idx != 0 || s0.hits.Load() != 1 {
		t.Fatalf("got %d %q idx=%d t0 hits=%d", rec.Code, rec.Body.String(), idx, s0.hits.Load())
	}
	// the next request tries tier 0 again (still no cooldown, keys intact), once
	chatReq(app, `{"model":"a"}`)
	if idx, _, _ := app.keys.Current(); app.cooldowns.remaining(tier0CooldownKey) != 0 || idx != 0 || s1.hits.Load() != 2 || s0.hits.Load() != 2 {
		t.Fatalf("second request: idx=%d t1 hits=%d t0 hits=%d", idx, s1.hits.Load(), s0.hits.Load())
	}
}

func TestFailoverHeaderTimeoutGoesToNextTier(t *testing.T) {
	slow, s1 := slowStub(t), newStub(t, 200, `{"from":"t1"}`)
	app, _ := failoverAppTimeout(t, slow, s1, nil, time.Hour, 50*time.Millisecond)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Body.String() != `{"from":"t1"}` || app.cooldowns.remaining(tier0CooldownKey) != 0 || slow.hits.Load() != 1 {
		t.Fatalf("got %d %q t0 hits=%d", rec.Code, rec.Body.String(), slow.hits.Load())
	}
}

func TestFailoverHeaderTimeoutExtraTierGoesToNextTierWithoutCooldown(t *testing.T) {
	s0, slow, s2 := newStub(t, 500, `from t0`), slowStub(t), newStub(t, 200, `{"from":"t2"}`)
	app, _ := failoverAppTimeout(t, s0, slow, s2, time.Hour, 50*time.Millisecond)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Body.String() != `{"from":"t2"}` || app.cooldowns.remaining("t1") != 0 || slow.hits.Load() != 1 {
		t.Fatalf("got %d %q t1 hits=%d", rec.Code, rec.Body.String(), slow.hits.Load())
	}
}

func TestFailoverTier0UnreachableWithoutExtraTiersHidesTierURL(t *testing.T) {
	s0, s1 := newStub(t, 200, `x`), newStub(t, 200, `x`)
	s0.srv.Close()
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	rec := tierReq(app, http.MethodGet, "/v1/models", "", nil)
	if rec.Code != 502 || rec.Body.String() != "upstream unreachable\n" || strings.Contains(rec.Body.String(), s0.srv.URL) {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestFailoverClientCancelWithHeldResponseLogs499(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s0, s1 := newStub(t, 500, `from t0`), newStub(t, 200, `x`)
	app, now := failoverApp(t, s0, s1, nil, time.Hour)
	// The tier 0 cooldown is set right after its 500 arrives: cancel there, before the final step.
	app.cooldowns.now = func() time.Time {
		if s0.hits.Load() > 0 {
			cancel()
		}
		return *now
	}
	lines := captureLog(app)
	// model "b" maps to no extra tier, so the held tier 0 response is the only thing left to serve
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"b"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer p")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if l := lines(t); len(l) != 1 || l[0]["status"] != float64(499) || rec.Body.Len() != 0 || s1.hits.Load() != 0 {
		t.Fatalf("log=%v body=%q t1 hits=%d", l, rec.Body.String(), s1.hits.Load())
	}
}

func TestFailoverConnectionErrorExtraTierAndAllUnreachable(t *testing.T) {
	s0, s1, s2 := newStub(t, 500, `from t0`), newStub(t, 200, `x`), newStub(t, 200, `{"from":"t2"}`)
	s1.srv.Close()
	app, _ := failoverApp(t, s0, s1, s2, time.Hour)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Body.String() != `{"from":"t2"}` || app.cooldowns.remaining("t1") != 0 {
		t.Fatalf("extra tier: %d %q", rec.Code, rec.Body.String())
	}
	d0, d1 := newStub(t, 200, `x`), newStub(t, 200, `x`)
	d0.srv.Close()
	d1.srv.Close()
	app, _ = failoverApp(t, d0, d1, nil, time.Hour)
	lines := captureLog(app)
	rec = chatReq(app, `{"model":"a"}`)
	if l := lines(t); rec.Code != 502 || l[0]["tier"] != "" || l[0]["status"] != float64(502) ||
		app.cooldowns.remaining(tier0CooldownKey) != 0 || app.cooldowns.remaining("t1") != 0 {
		t.Fatalf("all unreachable: %d %v", rec.Code, l)
	}
}

func TestFailoverNoModelStaysOnTier0(t *testing.T) {
	s0, s1 := newStub(t, 200, `x`), newStub(t, 200, `x`)
	s0.srv.Close()
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	rec := tierReq(app, http.MethodGet, "/v1/models", "", nil)
	if rec.Code != 502 || s1.hits.Load() != 0 {
		t.Fatalf("got %d t1 hits=%d", rec.Code, s1.hits.Load())
	}
}

func TestFailoverClientCancelStopsWithoutTouchingState(t *testing.T) {
	for _, slowTier := range []int{0, 1} {
		ctx, cancel := context.WithCancel(context.Background())
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client closing
			cancel()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}))
		defer slow.Close()
		next, s2 := newStub(t, 200, `x`), newStub(t, 200, `x`)
		var app *App
		if slowTier == 0 {
			app, _ = failoverApp(t, &stub{srv: slow}, next, s2, time.Hour)
		} else {
			app, _ = failoverApp(t, newStub(t, 500, `from t0`), &stub{srv: slow}, s2, time.Hour)
		}
		lines := captureLog(app)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"a"}`)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer p")
		app.ServeHTTP(httptest.NewRecorder(), req)
		l := lines(t)
		if len(l) != 1 || l[0]["status"] != float64(499) || l[0]["tier"] != "" || s2.hits.Load() != 0 || next.hits.Load() != 0 || app.cooldowns.remaining("t1") != 0 {
			t.Fatalf("slow tier %d: log=%v t2 hits=%d next hits=%d", slowTier, l, s2.hits.Load(), next.hits.Load())
		}
		if slowTier == 0 && app.cooldowns.remaining(tier0CooldownKey) != 0 {
			t.Fatal("tier 0 in cooldown after a client cancel")
		}
	}
}

func TestFailoverAllInCooldownLocal429WithRetryAfter(t *testing.T) {
	s0, s1 := newStub(t, 500, `x`), newStub(t, 503, `y`)
	app, now := failoverApp(t, s0, s1, nil, time.Hour)
	d := 10 * time.Minute
	app.config.Tiers[0].Cooldown = &d
	if rec := chatReq(app, `{"model":"a"}`); rec.Code != 503 {
		t.Fatalf("first: %d", rec.Code)
	}
	*now = now.Add(time.Minute)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "540" || s0.hits.Load() != 1 || s1.hits.Load() != 1 {
		t.Fatalf("got %d RA=%q hits=%d/%d", rec.Code, rec.Header().Get("Retry-After"), s0.hits.Load(), s1.hits.Load())
	}
	// a request that tier 1 cannot serve only waits on tier 0 (the soonest return is tier 0 at 1h)
	rec = chatReq(app, `{"model":"zzz"}`)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "3540" {
		t.Fatalf("unmapped: %d RA=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	*now = now.Add(10 * time.Minute)
	if rec := chatReq(app, `{"model":"a"}`); rec.Code != 503 || s1.hits.Load() != 2 || s0.hits.Load() != 1 {
		t.Fatalf("tier 1 not retried alone: %d hits=%d/%d", rec.Code, s0.hits.Load(), s1.hits.Load())
	}
}

func TestFailoverTierCooldownZeroAndDefault(t *testing.T) {
	s0, s1, s2 := newStub(t, 500, `x`), newStub(t, 500, `y`), newStub(t, 200, `{"from":"t2"}`)
	app, now := failoverApp(t, s0, s1, s2, 0)
	// RetryExhaustedAfter 0: no cooldown anywhere, every request walks the tiers again
	chatReq(app, `{"model":"a"}`)
	chatReq(app, `{"model":"a"}`)
	if s0.hits.Load() != 2 || s1.hits.Load() != 2 || s2.hits.Load() != 2 {
		t.Fatalf("zero cooldown hits=%d/%d/%d", s0.hits.Load(), s1.hits.Load(), s2.hits.Load())
	}
	// the default is RetryExhaustedAfter read at use time; a tier value overrides it
	app.config.RetryExhaustedAfter = time.Hour
	d := time.Minute
	app.config.Tiers[0].Cooldown = &d
	chatReq(app, `{"model":"a"}`)
	if app.cooldowns.remaining(tier0CooldownKey) != time.Hour || app.cooldowns.remaining("t1") != time.Minute {
		t.Fatalf("cooldowns %v %v", app.cooldowns.remaining(tier0CooldownKey), app.cooldowns.remaining("t1"))
	}
	*now = now.Add(2 * time.Minute)
	chatReq(app, `{"model":"a"}`)
	if s0.hits.Load() != 3 || s1.hits.Load() != 4 {
		t.Fatalf("hits=%d/%d", s0.hits.Load(), s1.hits.Load())
	}
}

func TestFailoverQuotaStillRotatesTier0Keys(t *testing.T) {
	var seen []string
	s0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if len(seen) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(quotaBody))
			return
		}
		_, _ = w.Write([]byte(`{"from":"t0"}`))
	}))
	defer s0.Close()
	app, _ := failoverApp(t, &stub{srv: s0}, newStub(t, 200, `x`), nil, time.Hour)
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Body.String() != `{"from":"t0"}` || len(seen) != 2 || app.cooldowns.remaining(tier0CooldownKey) != 0 {
		t.Fatalf("got %q seen=%v", rec.Body.String(), seen)
	}
}

func TestLoadConfigTierCooldown(t *testing.T) {
	tiersYAML(t, "tiers:\n"+tierOK+"    cooldown: 90s\n"+strings.Replace(tierOK, "name: a", "name: b", 1)+"    cooldown: 0s\n"+strings.Replace(tierOK, "name: a", "name: c", 1))
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.Tiers[0].Cooldown; c == nil || *c != 90*time.Second {
		t.Fatalf("a: %v", c)
	}
	if c := cfg.Tiers[1].Cooldown; c == nil || *c != 0 {
		t.Fatalf("b: %v", c)
	}
	if cfg.Tiers[2].Cooldown != nil {
		t.Fatalf("c: %v", cfg.Tiers[2].Cooldown)
	}
}

func TestFailover404LargeBodyReachesClientIntact(t *testing.T) {
	big := strings.Repeat("x", 10000)
	for _, body := range []string{big, `{"error":"model not found"}` + big} {
		for _, model := range []string{`{"model":"a"}`, `{"messages":[]}`} {
			s0, s1 := newStub(t, 404, body), newStub(t, 200, `{"from":"t1"}`)
			app, _ := failoverApp(t, s0, s1, nil, time.Hour)
			rec := chatReq(app, model)
			got := rec.Body.String()
			if rec.Code == 404 && got != body {
				t.Fatalf("%q: 404 body truncated: %d of %d bytes", model, len(got), len(body))
			}
			if rec.Code == 200 && s0.hits.Load() != 1 {
				t.Fatalf("hits %d", s0.hits.Load())
			}
		}
	}
	// a failure response held as the last one is also served whole
	s0, s1 := newStub(t, 500, `x`), newStub(t, 429, big)
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	if rec := chatReq(app, `{"model":"a"}`); rec.Code != 429 || rec.Body.String() != big {
		t.Fatalf("held 429: %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestFailoverExtraTierNamedLikeTier0HasOwnCooldown(t *testing.T) {
	s0, s1 := newStub(t, 500, `x`), newStub(t, 200, `{"from":"t1"}`)
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	app.config.Tiers[0].Name = tier0Name
	app.config.Tiers[0].Cooldown = new(time.Duration) // extra tier: no cooldown
	for i := 1; i <= 2; i++ {
		if rec := chatReq(app, `{"model":"a"}`); rec.Code != 200 || s1.hits.Load() != int32(i) {
			t.Fatalf("req %d: %d hits=%d", i, rec.Code, s1.hits.Load())
		}
	}
	if s0.hits.Load() != 1 || app.cooldowns.remaining(tier0CooldownKey) == 0 || app.cooldowns.remaining(tier0Name) != 0 {
		t.Fatalf("tier 0 hits=%d, cooldowns %v / %v", s0.hits.Load(), app.cooldowns.remaining(tier0CooldownKey), app.cooldowns.remaining(tier0Name))
	}
}

func TestFailoverAllKeysExhaustedAndTiersFailStillAlerts(t *testing.T) {
	alerts := make(chan string, 4)
	old := sendMail
	sendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte, useTLS, useStartTLS bool) error {
		alerts <- string(msg)
		return nil
	}
	t.Cleanup(func() { sendMail = old })
	s0, s1 := newStub(t, 429, quotaBody), newStub(t, 503, `down`)
	app, _ := failoverApp(t, s0, s1, nil, time.Hour)
	app.sender = NewSMTPNotifier(SMTPConfig{Host: "h", Port: 25, From: "f@x", To: "t@x"})
	rec := chatReq(app, `{"model":"a"}`)
	if rec.Code != 503 || rec.Body.String() != "down" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case m := <-alerts:
			if strings.Contains(m, "exhausted all upstream keys") {
				return
			}
		case <-deadline:
			t.Fatal("no all-exhausted alert")
		}
	}
}

func TestFailoverRetryAfterFromExhaustedKeysWithoutTierCooldown(t *testing.T) {
	s0, s1 := newStub(t, 429, quotaBody), newStub(t, 200, `x`)
	app, now := failoverApp(t, s0, s1, nil, 5*time.Minute)
	app.config.Tiers = app.config.Tiers[:1]
	rec := chatReq(app, `{"model":"zzz"}`) // no tier maps it: only tier 0's exhausted keys count
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "300" {
		t.Fatalf("got %d RA=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	*now = now.Add(100 * time.Second)
	if rec := chatReq(app, `{"model":"zzz"}`); rec.Header().Get("Retry-After") != "200" {
		t.Fatalf("RA=%q", rec.Header().Get("Retry-After"))
	}
}

func TestLoadConfigMinContext(t *testing.T) {
	for name, c := range map[string]struct {
		yaml string
		want int
	}{"omitted": {"", 64000}, "zero": {"min_context: 0\n", 0}, "explicit": {"min_context: 128000\n", 128000}} {
		t.Run(name, func(t *testing.T) {
			tiersYAML(t, c.yaml)
			cfg, err := loadConfig()
			if err != nil || cfg.MinContext != c.want {
				t.Fatalf("min_context=%d err=%v, want %d", cfg.MinContext, err, c.want)
			}
		})
	}
	tiersYAML(t, "min_context: -1\n")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "min_context must be >= 0") {
		t.Fatalf("err = %v", err)
	}
}

func TestTierEligibilityByContextAndTools(t *testing.T) {
	const plain, withTools = `{"model":"a"}`, `{"model":"a","tools":[{"type":"function"}]}`
	for name, c := range map[string]struct {
		minCtx, ctx int
		tools       bool
		body        string
		served      bool
	}{
		"context below minimum":       {1000, 999, true, plain, false},
		"context undeclared":          {1000, 0, true, plain, false},
		"context at minimum":          {1000, 1000, true, plain, true},
		"tools needed, model without": {1000, 1000, false, withTools, false},
		"tools needed, model has":     {1000, 1000, true, withTools, true},
		"no tools, model without":     {1000, 1000, false, plain, true},
		"empty tools array":           {1000, 1000, false, `{"model":"a","tools":[]}`, true},
		"null tools":                  {1000, 1000, false, `{"model":"a","tools":null}`, true},
		"floor off, context 0":        {0, 0, true, plain, true},
		"floor off, tools still":      {0, 0, false, withTools, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTierFixture(t, false, false)
			f.app.config.Tiers = f.app.config.Tiers[:1]
			f.app.config.MinContext = c.minCtx
			f.app.config.Tiers[0].Models = []TierModel{{Name: "a", ID: "id-a", Context: c.ctx, Tools: c.tools}}
			rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", c.body, nil)
			want, hits := http.StatusTooManyRequests, int32(0)
			if c.served {
				want, hits = http.StatusOK, 1
			}
			if rec.Code != want || f.hits()[1] != hits {
				t.Fatalf("code=%d hits=%v", rec.Code, f.hits())
			}
		})
	}
}

func TestLocalRetryAfterIgnoresIneligibleTier(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers = f.app.config.Tiers[:1]
	f.app.config.MinContext = 1000
	f.app.config.Tiers[0].Models = []TierModel{{Name: "a", ID: "id-a", Context: 10, Tools: true}}
	tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"z"}`, nil) // exhaust tier 0
	f.app.cooldowns.set("t1", 10*time.Second)
	if secs, ok := f.app.localRetryAfter("a", APIStyleOpenAI, false); !ok || secs < 60 {
		t.Fatalf("ineligible tier counted: %d %v", secs, ok)
	}
	f.app.config.Tiers[0].Models[0].Context = 1000
	if secs, ok := f.app.localRetryAfter("a", APIStyleOpenAI, false); !ok || secs != 10 {
		t.Fatalf("eligible tier ignored: %d %v", secs, ok)
	}
	// enough context but no tool calling: only counts for requests that do not need tools
	f.app.config.Tiers[0].Models[0].Tools = false
	if secs, ok := f.app.localRetryAfter("a", APIStyleOpenAI, true); !ok || secs < 60 {
		t.Fatalf("tools-less tier counted for a tools request: %d %v", secs, ok)
	}
	if secs, ok := f.app.localRetryAfter("a", APIStyleOpenAI, false); !ok || secs != 10 {
		t.Fatalf("tools-less tier ignored without tools: %d %v", secs, ok)
	}
}

func TestIneligibleTiers(t *testing.T) {
	cfg := Config{MinContext: 64000, Tiers: []TierConfig{
		{Name: "ok", Models: []TierModel{{Name: "a"}, {Name: "b", Context: 64000}}},
		{Name: "undeclared", Models: []TierModel{{Name: "a"}}},
		{Name: "small", Models: []TierModel{{Name: "a", Context: 32000}}},
	}}
	if got := ineligibleTiers(cfg); !slices.Equal(got, []string{"undeclared", "small"}) {
		t.Fatalf("got %v", got)
	}
	cfg.MinContext = 0
	if got := ineligibleTiers(cfg); len(got) != 0 {
		t.Fatalf("floor off: got %v", got)
	}
}

func TestTier0ExemptFromEligibility(t *testing.T) {
	f := newTierFixture(t, true, false) // tier 0 model declares no context or tools
	f.app.config.MinContext = 64000
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a","tools":[{"type":"function"}]}`, nil)
	if h := f.hits(); rec.Code != http.StatusOK || h != [3]int32{1, 0, 0} {
		t.Fatalf("code=%d hits=%v", rec.Code, h)
	}
}

func TestLoadedDefaultMinContextThroughProxy(t *testing.T) {
	s0, small, big := newStub(t, 429, quotaBody), newStub(t, 200, `{"from":"small"}`), newStub(t, 200, `{"from":"big"}`)
	tier := func(name, url string, ctx int) string {
		return fmt.Sprintf("  - name: %s\n    base_url: %s\n    api_key: k\n    free: true\n    models:\n      a: {id: id-%s, context: %d, tools: true}\n", name, url, name, ctx)
	}
	tiersYAML(t, "tiers:\n"+tier("small", small.srv.URL, 32000)+tier("big", big.srv.URL, 131072))
	cfg, err := loadConfig()
	if err != nil || cfg.MinContext != 64000 {
		t.Fatalf("min_context=%d err=%v", cfg.MinContext, err)
	}
	cfg.UpstreamBaseURL = s0.srv.URL
	rec := chatReq(newApp(cfg), `{"model":"a"}`)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"from":"big"}` || small.hits.Load() != 0 {
		t.Fatalf("code=%d body=%q small hits=%d", rec.Code, rec.Body.String(), small.hits.Load())
	}
}

func TestToolsWhitespaceArrayIsNoTools(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers = f.app.config.Tiers[:1]
	f.app.config.Tiers[0].Models = []TierModel{{Name: "a", ID: "id-a", Tools: false}}
	rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a","tools": [ ]}`, nil)
	if rec.Code != http.StatusOK || f.hits()[1] != 1 {
		t.Fatalf("code=%d hits=%v", rec.Code, f.hits())
	}
}

func TestAnthropicToolsRequestNeedsToolsModel(t *testing.T) {
	f := newTierFixture(t, false, false)
	f.app.config.Tiers = f.app.config.Tiers[1:] // t2 speaks anthropic
	body := `{"model":"a","tools":[{"name":"x"}]}`
	f.app.config.Tiers[0].Models = []TierModel{{Name: "a", ID: "id-a2", Tools: false}}
	if rec := tierReq(f.app, http.MethodPost, "/v1/messages", body, nil); rec.Code != http.StatusTooManyRequests || f.hits()[2] != 0 {
		t.Fatalf("tools-less model: code=%d hits=%v", rec.Code, f.hits())
	}
	f.app.config.Tiers[0].Models[0].Tools = true
	if rec := tierReq(f.app, http.MethodPost, "/v1/messages", body, nil); rec.Code != http.StatusOK || f.hits()[2] != 1 {
		t.Fatalf("tools model: code=%d hits=%v", rec.Code, f.hits())
	}
}

func stateFileCfg(t *testing.T) Config {
	t.Helper()
	return Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"sk-secret-1", "sk-secret-2"}, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, StateFile: filepath.Join(t.TempDir(), "state.json")}
}

func TestStateSurvivesRestart(t *testing.T) {
	f := newTierFixture(t, false, true)
	f.app.config.StateFile = filepath.Join(t.TempDir(), "state.json")
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if f.app.cooldowns.remaining("t1") == 0 {
		t.Fatal("t1 should be in cooldown")
	}
	app2 := newApp(f.app.config)
	if app2.cooldowns.remaining("t1") == 0 || app2.cooldowns.remaining("t2") != 0 {
		t.Fatal("t1 cooldown not restored (or t2 wrongly set)")
	}
	if _, _, ok := app2.keys.Current(); ok {
		t.Fatal("exhausted tier 0 key not restored")
	}
	before := f.hits()
	if rec := tierReq(app2, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if after := f.hits(); after[0] != before[0] || after[1] != before[1] {
		t.Fatalf("restored app called a tier in cooldown: before=%v after=%v", before, after)
	}
}

func TestStateTier0CooldownSurvivesRestart(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(up.Close)
	cfg := stateFileCfg(t)
	cfg.UpstreamBaseURL = up.URL
	app := newApp(cfg)
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if newApp(cfg).cooldowns.remaining(tier0CooldownKey) == 0 {
		t.Fatal("tier 0 cooldown not restored")
	}
}

func TestStateExpiredNotRestored(t *testing.T) {
	cfg := stateFileCfg(t)
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"tier0":"` + past + `","tiers":{"t1":"` + past + `"},"keys":{"` + keyID("sk-secret-1") + `":"` + past + `"}}`
	if err := os.WriteFile(cfg.StateFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newApp(cfg)
	if app.cooldowns.remaining("t1") != 0 || app.cooldowns.remaining(tier0CooldownKey) != 0 {
		t.Fatal("expired cooldown restored")
	}
	if idx, _, ok := app.keys.Current(); !ok || idx != 0 {
		t.Fatal("expired exhausted key restored")
	}
}

func TestStateKeyFoundByHashNotValue(t *testing.T) {
	cfg := stateFileCfg(t)
	app := newApp(cfg)
	app.keys.MarkExhausted(0) // sk-secret-1 exhausted, 2 takes over
	app.saveState()
	b, err := os.ReadFile(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sk-secret") {
		t.Fatalf("state file leaks a key: %s", b)
	}
	cfg.UpstreamAPIKeys = []string{"sk-secret-2", "sk-secret-1"} // order changed
	app2 := newApp(cfg)
	if idx, key, ok := app2.keys.Current(); !ok || idx != 0 || key != "sk-secret-2" {
		t.Fatalf("idx=%d key=%q ok=%v", idx, key, ok)
	}
	if app2.keys.states[1] != KeyExhausted {
		t.Fatal("sk-secret-1 should be exhausted at its new position")
	}
}

func TestStateNoFileWithoutStateFile(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(up.Close)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour})
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written: %v", entries)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	warnNoStateFile(Config{})
	warnNoStateFile(Config{StateFile: "x"})
	if strings.Count(buf.String(), "state_file is not set") != 1 {
		t.Fatalf("log: %q", buf.String())
	}
}

func TestStateWriteFailureDoesNotAffectRequest(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(up.Close)
	cfg := stateFileCfg(t)
	cfg.UpstreamBaseURL = up.URL
	cfg.StateFile = filepath.Join(t.TempDir(), "missing", "state.json")
	var logs bytes.Buffer
	app := newApp(cfg)
	app.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != http.StatusTooManyRequests && rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d", rec.Code)
	}
	if !strings.Contains(logs.String(), "state file not saved") || strings.Contains(logs.String(), "sk-secret") {
		t.Fatalf("logs: %s", logs.String())
	}
}

func TestStateCorruptFileStartsEmpty(t *testing.T) {
	cfg := stateFileCfg(t)
	if err := os.WriteFile(cfg.StateFile, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newApp(cfg)
	if _, _, ok := app.keys.Current(); !ok {
		t.Fatal("keys should be available")
	}
	if app.cooldowns.remaining(tier0CooldownKey) != 0 {
		t.Fatal("unexpected cooldown")
	}
}

func TestStateSaveSkipsUnchangedBytes(t *testing.T) {
	cfg := stateFileCfg(t)
	app := newApp(cfg)
	app.saveState()
	if err := os.Remove(cfg.StateFile); err != nil {
		t.Fatal(err)
	}
	app.saveState() // same bytes as the last good write: no new write
	if _, err := os.Stat(cfg.StateFile); err == nil {
		t.Fatal("unchanged state was rewritten")
	}
}

func TestStateAllExhaustedNotNotifiedAgainAfterRestart(t *testing.T) {
	cfg := stateFileCfg(t)
	app := newApp(cfg)
	app.keys.MarkExhausted(0)
	app.keys.MarkExhausted(1)
	app.saveState()
	app2 := newApp(cfg)
	if !app2.keys.AllExhausted() {
		t.Fatal("exhausted keys not restored")
	}
	if app2.keys.ShouldNotifyAllExhausted() {
		t.Fatal("all-exhausted email would repeat after restart")
	}
}

func TestStateSaveRetriedAfterFailedWrite(t *testing.T) {
	cfg := stateFileCfg(t)
	cfg.StateFile = filepath.Join(t.TempDir(), "missing", "state.json")
	app := newApp(cfg)
	app.keys.MarkExhausted(0)
	app.saveState()
	if _, err := os.Stat(cfg.StateFile); err == nil {
		t.Fatal("write should have failed")
	}
	if err := os.Mkdir(filepath.Dir(cfg.StateFile), 0o700); err != nil {
		t.Fatal(err)
	}
	app.saveState() // same bytes as the failed attempt: must still be written
	if _, err := os.Stat(cfg.StateFile); err != nil {
		t.Fatalf("state not written on retry: %v", err)
	}
}

func TestStateAdminResetSurvivesRestart(t *testing.T) {
	for _, tc := range []struct {
		path, body string
		wantFree   []int
	}{
		{"/admin/reset-key", `{"index":0}`, []int{0}},
		{"/admin/reset-all-keys", "", []int{0, 1}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			cfg := stateFileCfg(t)
			app := newApp(cfg)
			app.keys.MarkExhausted(0)
			app.keys.MarkExhausted(1)
			app.saveState()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer p")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d body %s", rec.Code, rec.Body.String())
			}
			app2 := newApp(cfg)
			for i := range app2.keys.states {
				if got, want := app2.keys.states[i] == KeyExhausted, !slices.Contains(tc.wantFree, i); got != want {
					t.Fatalf("key %d exhausted=%v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestLoadConfigStateFile(t *testing.T) {
	tiersYAML(t, "state_file: /data/state.json\n")
	cfg, err := loadConfig()
	if err != nil || cfg.StateFile != "/data/state.json" {
		t.Fatalf("cfg=%q err=%v", cfg.StateFile, err)
	}
}
