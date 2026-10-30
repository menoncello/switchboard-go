package main

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historyApp(t *testing.T, status int, body, usageFile string) *App {
	t.Helper()
	return historyAppWith(t, status, body, func(c *Config) { c.UsageFile = usageFile })
}

func historyAppWith(t *testing.T, status int, body string, mod func(*Config)) *App {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(up.Close)
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, KeyNames: []string{"main"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour}
	mod(&cfg)
	return newApp(cfg)
}

const usageBody = `{"usage":{"prompt_tokens":10,"completion_tokens":3}}`

func post(app *App, project string) int {
	hdr := map[string]string{}
	if project != "" {
		hdr["X-Project"] = project
	}
	return tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"m"}`, hdr).Code
}

func TestHistoryAggregation(t *testing.T) {
	app := historyApp(t, 200, usageBody, "")
	post(app, "a")
	post(app, "b")
	post(app, "b")
	rows := app.history.snapshot()
	if len(rows) != 2 || rows[0].Project != "a" || rows[0].Requests != 1 || rows[0].TokensIn != 10 || rows[0].Key != "main" || rows[0].Tier != tier0Name ||
		rows[1].Project != "b" || rows[1].Requests != 2 || rows[1].TokensIn != 20 || rows[1].TokensOut != 6 {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestHistoryNoUsageAndNon2xx(t *testing.T) {
	app := historyApp(t, 200, `{}`, "")
	post(app, "")
	if r := app.history.snapshot(); len(r) != 1 || r[0].Requests != 1 || r[0].NoUsage != 1 || r[0].TokensIn != 0 {
		t.Fatalf("rows: %+v", r)
	}
	app = historyApp(t, 400, `{}`, "") // a 5xx or quota 429 is a tier failure, not recorded
	post(app, "")
	if r := app.history.snapshot(); len(r) != 1 || r[0].Requests != 1 || r[0].NoUsage != 0 || r[0].TokensIn != 0 {
		t.Fatalf("rows: %+v", r)
	}
}

func TestHistoryNoTierServed(t *testing.T) {
	app := historyApp(t, 200, usageBody, "")
	app.cooldowns.set(tier0CooldownKey, time.Hour)
	if code := post(app, ""); code != http.StatusTooManyRequests {
		t.Fatalf("code %d", code)
	}
	if r := app.history.snapshot(); len(r) != 0 {
		t.Fatalf("rows: %+v", r)
	}
}

func TestHistoryRestartKeepsTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app := historyApp(t, 200, usageBody, path)
	post(app, "a")
	app.history.flush()
	app2 := historyApp(t, 200, usageBody, path)
	if r := app2.history.snapshot(); len(r) != 1 || r[0].Requests != 1 || r[0].TokensIn != 10 || r[0].TokensOut != 3 {
		t.Fatalf("rows: %+v", r)
	}
}

func TestHistoryRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -91).Format("2006-01-02")
	keep := now.AddDate(0, 0, -90).Format("2006-01-02")
	data := `{"rows":[{"day":"` + old + `","project":"p","model":"m","key":"k","tier":"t","requests":1},{"day":"` + keep + `","project":"p","model":"m","key":"k","tier":"t","requests":1}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &usageHistory{path: path, loc: time.UTC, now: func() time.Time { return now }, delay: time.Hour, logger: slog.Default(), rows: map[usageKey]*usageCounts{}}
	h.load()
	if r := h.snapshot(); len(r) != 1 || r[0].Day != keep {
		t.Fatalf("after load: %+v", r)
	}
	// An old row that got in memory is dropped on write too.
	h.rows[usageKey{Day: old}] = &usageCounts{Requests: 1}
	h.flush()
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), old) || !strings.Contains(string(b), keep) {
		t.Fatalf("file: %s", b)
	}
}

func TestHistoryDayByTimezone(t *testing.T) {
	at := time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC)
	for tz, want := range map[string]string{"America/Sao_Paulo": "2026-10-29", "": "2026-10-30", "Marte/Olimpo": "2026-10-30"} {
		app := historyAppWith(t, 200, usageBody, func(c *Config) { c.Timezone = tz })
		app.history.now = func() time.Time { return at }
		post(app, "")
		if r := app.history.snapshot(); len(r) != 1 || r[0].Day != want {
			t.Fatalf("tz %q: %+v want %s", tz, r, want)
		}
	}
}

func TestValidateConfigTimezone(t *testing.T) {
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://x", MaxRequestBodyBytes: 1}
	for tz, ok := range map[string]bool{"": true, "America/Sao_Paulo": true, "Marte/Olimpo": false, "Local": false} {
		cfg.Timezone = tz
		if err := validateConfig(cfg); (err == nil) != ok {
			t.Fatalf("tz %q: %v", tz, err)
		}
	}
}

func TestHistoryNoUsageFile(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	app := historyApp(t, 200, usageBody, "")
	post(app, "")
	app.history.close()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written: %v", entries)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	warnNoUsageFile(Config{})
	warnNoUsageFile(Config{UsageFile: "x"})
	if strings.Count(buf.String(), "usage_file is not set") != 1 {
		t.Fatalf("log: %q", buf.String())
	}
}

func TestHistoryWriteFailureDoesNotAffectRequest(t *testing.T) {
	app := historyApp(t, 200, usageBody, filepath.Join(t.TempDir(), "missing", "usage.json"))
	var logs bytes.Buffer
	app.history.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	if code := post(app, ""); code != 200 {
		t.Fatalf("code %d", code)
	}
	app.history.flush()
	if !strings.Contains(logs.String(), "usage file not saved") || !app.history.dirty {
		t.Fatalf("log: %q dirty=%v", logs.String(), app.history.dirty)
	}
}

func TestHistoryDelayedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app := historyApp(t, 200, usageBody, path)
	app.history.delay = time.Hour
	post(app, "")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("file written before the delay")
	}
	app.history.close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryTimerFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app := historyApp(t, 200, usageBody, path)
	app.history.delay = 20 * time.Millisecond
	post(app, "")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("file never written")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHistoryCloseFlushes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app := historyApp(t, 200, usageBody, path)
	post(app, "")
	app.history.close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryInvalidFileMovedAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	orig := []byte(`{"rows": [oops`)
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	app := historyApp(t, 200, usageBody, path)
	post(app, "")
	app.history.flush()
	if b, err := os.ReadFile(path + ".bad"); err != nil || !bytes.Equal(b, orig) {
		t.Fatalf(".bad: %q %v", b, err)
	}
	b, _ := os.ReadFile(path)
	if !json.Valid(b) || !strings.Contains(string(b), `"requests":1`) {
		t.Fatalf("new file: %s", b)
	}
}

func TestHistorySkipsHeldFailureAndGET(t *testing.T) {
	app := historyApp(t, 429, quotaBody, "")
	if code := post(app, ""); code != http.StatusTooManyRequests {
		t.Fatalf("code %d", code)
	}
	if r := app.history.snapshot(); len(r) != 0 {
		t.Fatalf("held failure: %+v", r)
	}
	app = historyApp(t, 200, usageBody, "")
	if code := tierReq(app, http.MethodGet, "/v1/models", ``, nil).Code; code != 200 {
		t.Fatalf("code %d", code)
	}
	if r := app.history.snapshot(); len(r) != 0 {
		t.Fatalf("GET: %+v", r)
	}
}

func TestHistoryRowDimensions(t *testing.T) {
	app := historyApp(t, 200, usageBody, "")
	app.config.KeyNames = nil
	post(app, "")
	if r := app.history.snapshot(); len(r) != 1 || r[0].Key != "key-0" {
		t.Fatalf("unnamed key: %+v", r)
	}

	f := newTierFixture(t, false, false)
	if rec := tierReq(f.app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	if r := f.app.history.snapshot(); len(r) != 1 || r[0].Key != "" || r[0].Tier != "t1" {
		t.Fatalf("extra tier: %+v", r)
	}

	app = historyApp(t, 200, usageBody, "")
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"`+strings.Repeat("x", 300)+`"}`, nil)
	if r := app.history.snapshot(); len(r) != 1 || len(r[0].Model) == 0 || len(r[0].Model) > maxLoggedModel {
		t.Fatalf("long model: %+v", r)
	}

	app = historyApp(t, 200, usageBody, "")
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"messages":[]}`, nil)
	if r := app.history.snapshot(); len(r) != 0 {
		t.Fatalf("no model: %+v", r)
	}
}

func TestShutdownFlushesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app := historyApp(t, 200, usageBody, path)
	app.history.delay = time.Hour
	post(app, "")
	shutdown(&http.Server{}, app)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func statusBody(app *App) string { return getStatusPage(app).Body.String() }

// section returns the body between the "<h3>title</h3>" heading and the next <h3>.
func section(t *testing.T, body, title string) string {
	t.Helper()
	h := "<h3>" + title + "</h3>"
	i := strings.Index(body, h)
	if i < 0 {
		t.Fatalf("heading %q not found in %s", h, body)
	}
	s := body[i+len(h):]
	if j := strings.Index(s, "<h3>"); j >= 0 {
		s = s[:j]
	}
	return s
}

func TestStatusUsageSection(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	app := historyAppWith(t, 200, usageBody, func(c *Config) {
		c.Timezone = "America/Sao_Paulo"
		c.UpstreamAPIKeys = []string{"sk-SEGREDO1"}
		c.Tiers = []TierConfig{{Name: "gratis", BaseURL: "http://segredo-a.invalid", APIKey: "kt-SEGREDO2", Free: true, Models: []TierModel{{Name: "a", ID: "a"}}}}
	})
	if strings.Contains(statusBody(app), "<h2>Uso</h2>") {
		t.Fatal("usage section without requests")
	}
	app.history.now = func() time.Time { return at.AddDate(0, 0, -8) }
	post(app, "velho") // outside the window
	app.history.now = func() time.Time { return at }
	if strings.Contains(statusBody(app), "<h2>Uso</h2>") {
		t.Fatal("usage section for a row outside the window")
	}
	app.history.now = func() time.Time { return at.AddDate(0, 0, -1) }
	post(app, "ontem")
	app.history.now = func() time.Time { return at }
	post(app, "a")
	post(app, "b")
	post(app, "")
	body := statusBody(app)
	if strings.Index(body, "<h2>Uso</h2>") < strings.Index(body, "<h2>Tiers</h2>") || !strings.Contains(body, "<h2>Tiers</h2>") {
		t.Fatalf("usage not below tiers: %s", body)
	}
	if want := "Hoje (2026-10-29, America/Sao_Paulo): 3 requests, 30 tokens in, 9 tokens out, 0 tokens cache."; !strings.Contains(body, want) {
		t.Fatalf("missing %q in %s", want, body)
	}
	for title, cells := range map[string][]string{
		"Dia":     {"<td>2026-10-29</td><td>3</td>", "<td>2026-10-28</td><td>1</td>"},
		"Projeto": {"<td>a</td><td>1</td>", "<td>b</td><td>1</td>", "<td>sem projeto</td><td>1</td>", "<td>ontem</td>"},
		"Modelo":  {"<td>m</td><td>4</td>"},
		"Chave":   {"<td>main</td><td>4</td>"},
		"Tier":    {"<td>" + tier0Name + "</td><td>4</td>"},
	} {
		s := section(t, body, title)
		for _, want := range cells {
			if !strings.Contains(s, want) {
				t.Fatalf("%s: missing %q in %s", title, want, s)
			}
		}
	}
	if strings.Contains(body, "velho") || strings.Contains(body, "2026-10-21") {
		t.Fatalf("row outside the window shown: %s", body)
	}
	for _, secret := range []string{"SEGREDO1", "SEGREDO2", "segredo-a"} {
		if strings.Contains(body, secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
}

func TestStatusUsageDayBoundaryTimezone(t *testing.T) {
	app := historyAppWith(t, 200, usageBody, func(c *Config) { c.Timezone = "America/Sao_Paulo" })
	app.history.now = func() time.Time { return time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC) } // still 10-29 in Sao Paulo
	post(app, "")
	if body := statusBody(app); !strings.Contains(body, "Hoje (2026-10-29, America/Sao_Paulo): 1 requests") {
		t.Fatalf("day boundary: %s", body)
	}
}

func TestStatusUsageNoUsageAndHostileModel(t *testing.T) {
	app := historyApp(t, 200, `{}`, "")
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"<script>x</script>"}`, nil)
	body := statusBody(app)
	if strings.Contains(body, "<script>x") || !strings.Contains(body, "&lt;script&gt;x") {
		t.Fatalf("model not escaped: %s", body)
	}
	if !strings.Contains(body, "<td>1</td><td>0</td><td>0</td><td>0</td><td>1</td>") {
		t.Fatalf("no_usage count missing: %s", body)
	}
}

func TestStatusUsageWindowAndOrder(t *testing.T) {
	app := historyApp(t, 200, usageBody, "")
	app.history.now = func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
	add := func(day, project, model, key string, req, in, out, cache int) {
		app.history.rows[usageKey{day, project, model, key, "t"}] = &usageCounts{Requests: req, TokensIn: in, TokensOut: out, TokensCacheIn: cache}
	}
	add("2026-10-22", "p7", "m7", "k7", 9, 9, 9, 9) // today-7: outside
	add("2026-10-23", "p6", "m1", "", 1, 0, 0, 0)   // today-6: inside
	add("2026-10-29", "zeta", "m1", "k1", 1, 10, 1, 5)
	add("2026-10-29", "alfa", "m2", "k1", 1, 10, 1, 7)
	add("2026-10-29", "beta", "m2", "", 3, 30, 3, 0)
	body := statusBody(app)
	if !strings.Contains(body, "Hoje (2026-10-29, UTC): 5 requests, 50 tokens in, 5 tokens out, 12 tokens cache.") {
		t.Fatalf("day total: %s", body)
	}
	for _, gone := range []string{"p7", "m7", "k7", "2026-10-22"} {
		if strings.Contains(body, gone) {
			t.Fatalf("today-7 row shown (%q): %s", gone, body)
		}
	}
	inOrder := func(title string, cells ...string) {
		t.Helper()
		s, last := section(t, body, title), -1
		for _, c := range cells {
			i := strings.Index(s, "<td>"+c+"</td>")
			if i <= last {
				t.Fatalf("%s: %q out of order or missing in %s", title, c, s)
			}
			last = i
		}
	}
	inOrder("Dia", "2026-10-29", "2026-10-23")
	inOrder("Projeto", "beta", "alfa", "p6", "zeta") // requests desc, then label
	inOrder("Modelo", "m2", "m1")
	inOrder("Chave", "—", "k1") // empty key shows as a dash
	inOrder("Tier", "t")
}
