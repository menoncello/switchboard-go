package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	if want := "Hoje (2026-10-29, America/Sao_Paulo): 3 requests, 30 tokens in, 9 tokens out, 0 tokens cache, custo —."; !strings.Contains(body, want) {
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
	if !strings.Contains(body, "Hoje (2026-10-29, UTC): 5 requests, 50 tokens in, 5 tokens out, 12 tokens cache, custo —.") {
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

// pricedApp has tier 0 failing over to tier "gratis", which maps client model "a" to "a-id".
func pricedApp(t *testing.T, tierBody string, usageFile string, prices map[string]map[string]ModelPrice) *App {
	t.Helper()
	srv := func(status int, body string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(s.Close)
		return s
	}
	t1 := srv(200, tierBody)
	up := srv(503, `{}`)
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour,
		UsageFile: usageFile, Prices: prices,
		Tiers: []TierConfig{{Name: "gratis", BaseURL: t1.URL, APIKey: "kt", Free: true, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: "a", ID: "a-id"}}}}}
	return newApp(cfg)
}

var gratisPrices = map[string]map[string]ModelPrice{"gratis": {"a-id": {Input: 1, Output: 2}}}

func postModel(app *App, model string) {
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"`+model+`"}`, nil)
}

func TestCostPricedModel(t *testing.T) {
	app := pricedApp(t, `{"usage":{"prompt_tokens":1000,"completion_tokens":500}}`, "", gratisPrices)
	postModel(app, "a")
	r := app.history.snapshot()
	if len(r) != 1 || r[0].Tier != "gratis" || r[0].Priced != 1 || r[0].CostUSD < 0.0019999 || r[0].CostUSD > 0.0020001 {
		t.Fatalf("rows: %+v", r)
	}
	body := statusBody(app)
	if !strings.Contains(body, "custo US$ 0.0020.") || !strings.Contains(section(t, body, "Tier"), "<td>US$ 0.0020</td>") {
		t.Fatalf("status: %s", body)
	}
	postModel(app, "a") // record accumulates
	if r := app.history.snapshot(); len(r) != 1 || r[0].Requests != 2 || r[0].Priced != 2 || r[0].CostUSD < 0.0039999 || r[0].CostUSD > 0.0040001 {
		t.Fatalf("rows after second post: %+v", r)
	}
}

func TestCostMixedLineInStatus(t *testing.T) {
	app := pricedApp(t, `{"usage":{"prompt_tokens":1000,"completion_tokens":500}}`, "", gratisPrices)
	postModel(app, "a")
	postModel(app, "a")
	app.history.record("", "a", "", "gratis", usageTokens{found: true, in: 1000, out: 500}, false, nil, 0) // same line, no price
	body := statusBody(app)
	const cost = "US$ 0.0040 (2/3 com preço)"
	if !strings.Contains(body, "custo "+cost+".") || !strings.Contains(section(t, body, "Modelo"), "<td>"+cost+"</td>") {
		t.Fatalf("status: %s", body)
	}
}

func TestConfigPrice(t *testing.T) {
	cfg := Config{
		Tiers:  []TierConfig{{Name: "t", Models: []TierModel{{Name: "plain"}, {Name: "a", ID: "a-id"}}}, {Name: "bare", Models: []TierModel{{Name: "x"}}}},
		Prices: map[string]map[string]ModelPrice{"t": {"plain": {Input: 1, Output: 2}, "a-id": {Input: 3, Output: 4}}},
	}
	for name, tc := range map[string]struct {
		tier, model string
		want        ModelPrice
		ok          bool
	}{
		"empty id falls back to name": {"t", "plain", ModelPrice{1, 2}, true},
		"explicit id":                 {"t", "a", ModelPrice{3, 4}, true},
		"unknown model":               {"t", "nope", ModelPrice{}, false},
		"unknown tier":                {"nope", "a", ModelPrice{}, false},
		"tier without prices":         {"bare", "x", ModelPrice{}, false},
	} {
		if got, ok := cfg.price(tc.tier, tc.model); got != tc.want || ok != tc.ok {
			t.Fatalf("%s: got %v %v", name, got, ok)
		}
	}
}

func TestCostNoPriceAndTier0(t *testing.T) {
	app := pricedApp(t, `{"usage":{"prompt_tokens":1000,"completion_tokens":500}}`, "", map[string]map[string]ModelPrice{"gratis": {"outro": {Input: 1, Output: 2}}})
	postModel(app, "a") // tier gratis, no price for a-id
	if r := app.history.snapshot(); len(r) != 1 || r[0].Priced != 0 || r[0].CostUSD != 0 || r[0].TokensIn != 1000 {
		t.Fatalf("rows: %+v", r)
	}
	if s := section(t, statusBody(app), "Tier"); !strings.Contains(s, "<td>—</td></tr>") {
		t.Fatalf("tier section: %s", s)
	}
	t0 := historyApp(t, 200, usageBody, "")
	post(t0, "")
	if r := t0.history.snapshot(); len(r) != 1 || r[0].Priced != 0 {
		t.Fatalf("tier 0: %+v", r)
	}
	if s := section(t, statusBody(t0), "Tier"); !strings.Contains(s, "<td>—</td></tr>") {
		t.Fatalf("tier 0 section: %s", s)
	}
}

func TestCostNoUsagePriced(t *testing.T) {
	app := pricedApp(t, `{}`, "", gratisPrices)
	postModel(app, "a")
	if r := app.history.snapshot(); len(r) != 1 || r[0].Priced != 1 || r[0].CostUSD != 0 || r[0].NoUsage != 1 {
		t.Fatalf("rows: %+v", r)
	}
}

func TestCostMixedLine(t *testing.T) {
	h := &usageHistory{loc: time.UTC, now: time.Now, logger: slog.Default(), rows: map[usageKey]*usageCounts{}}
	u := usageTokens{found: true, in: 1000, out: 500}
	h.record("", "m", "", "t", u, false, &ModelPrice{Input: 1, Output: 2}, 0)
	h.record("", "m", "", "t", u, false, nil, 0)
	if got := (usageLine{Requests: 2, Priced: 1, CostUSD: 0.002}).Cost(); got != "US$ 0.0020 (1/2 com preço)" {
		t.Fatal(got)
	}
	if got := (usageLine{Requests: 1, Priced: 1, CostUSD: 0.00000123}).Cost(); got != "US$ 0.000001" {
		t.Fatal(got)
	}
	if r := h.snapshot(); len(r) != 1 || r[0].Requests != 2 || r[0].Priced != 1 {
		t.Fatalf("rows: %+v", r)
	}
}

func TestSpentIsPerTierAndToday(t *testing.T) {
	h := &usageHistory{loc: time.UTC, now: time.Now, logger: slog.Default(), rows: map[usageKey]*usageCounts{}}
	u, p := usageTokens{found: true, in: 1000, out: 500}, &ModelPrice{Input: 1, Output: 2}
	h.record("", "m", "", "a", u, false, p, 0)
	h.record("", "m", "", "a", u, false, p, 0)
	h.record("", "m", "", "b", u, false, p, 0)
	h.rows[usageKey{Day: h.now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), Model: "m", Tier: "a"}] = &usageCounts{CostUSD: 100}
	if a, b := h.spent("a"), h.spent("b"); math.Abs(a-0.004) > 1e-9 || math.Abs(b-0.002) > 1e-9 {
		t.Fatalf("spent a=%v b=%v, want 0.004 and 0.002", a, b)
	}
}

func TestUnreadUsageFileFailsStartupOnlyWithPaidTier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UsageFile: path, Tiers: []TierConfig{{Name: "gratis", Free: true}}}
	if err := newApp(cfg).startupErr(); err != nil {
		t.Fatalf("free tiers only: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Tiers = append(cfg.Tiers, TierConfig{Name: "pago", DailyCap: 5})
	if err := newApp(cfg).startupErr(); err == nil || !strings.Contains(err.Error(), path+".bad") {
		t.Fatalf("paid tier: %v", err)
	}
	if err := newApp(cfg).startupErr(); err != nil { // file is gone (moved to .bad): missing file is fine
		t.Fatalf("missing file: %v", err)
	}
}

func TestCostSurvivesRestartAndPriceChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app := pricedApp(t, `{"usage":{"prompt_tokens":1000,"completion_tokens":500}}`, path, gratisPrices)
	postModel(app, "a")
	app.history.flush()
	app2 := pricedApp(t, `{}`, path, map[string]map[string]ModelPrice{"gratis": {"a-id": {Input: 100, Output: 100}}})
	if r := app2.history.snapshot(); len(r) != 1 || r[0].Priced != 1 || r[0].CostUSD < 0.0019999 || r[0].CostUSD > 0.0020001 {
		t.Fatalf("rows: %+v", r)
	}
	if !strings.Contains(statusBody(app2), "custo US$ 0.0020.") {
		t.Fatal("cost missing from report after restart")
	}
}

func TestUsageFileWithoutCostFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	day := time.Now().UTC().Format("2006-01-02")
	data := `{"rows":[{"day":"` + day + `","project":"p","model":"m","key":"k","tier":"t","requests":2,"tokens_in":5}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	app := historyApp(t, 200, usageBody, path)
	if r := app.history.snapshot(); len(r) != 1 || r[0].Requests != 2 || r[0].CostUSD != 0 || r[0].Priced != 0 {
		t.Fatalf("rows: %+v", r)
	}
	if !strings.Contains(statusBody(app), "custo —.") {
		t.Fatal("expected custo —")
	}
}

func TestValidateConfigPrices(t *testing.T) {
	cfg := Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://x", MaxRequestBodyBytes: 1,
		Tiers: []TierConfig{{Name: "gratis", BaseURL: "http://t", APIKey: "k", Free: true, Models: []TierModel{{Name: "a"}}}}}
	for name, tc := range map[string]struct {
		prices map[string]map[string]ModelPrice
		ok     bool
	}{
		"ok":         {map[string]map[string]ModelPrice{"gratis": {"a": {Input: 1, Output: 2}}}, true},
		"zero":       {map[string]map[string]ModelPrice{"gratis": {"a": {}}}, true},
		"unmapped":   {map[string]map[string]ModelPrice{"gratis": {"b": {}}}, false},
		"neg input":  {map[string]map[string]ModelPrice{"gratis": {"a": {Input: -1}}}, false},
		"neg output": {map[string]map[string]ModelPrice{"gratis": {"a": {Output: -1}}}, false},
		"unknown":    {map[string]map[string]ModelPrice{"nope": {"a": {}}}, false},
		"inf":        {map[string]map[string]ModelPrice{"gratis": {"a": {Input: math.Inf(1)}}}, false},
		"tier 0":     {map[string]map[string]ModelPrice{tier0Name: {"a": {}}}, false},
	} {
		cfg.Prices = tc.prices
		if err := validateConfig(cfg); (err == nil) != tc.ok {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestLoadConfigPricesMissingKey(t *testing.T) {
	for name, price := range map[string]string{"output": "{input: 1}", "input": "{output: 1}", "typo": "{input: 1, outpt: 2}"} {
		tiersYAML(t, "prices:\n  a:\n    x/m1: "+price+"\ntiers:\n"+tierOK)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), `tier "a" model "x/m1"`) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	tiersYAML(t, "prices:\n  a:\n    x/m1: {input: 0, output: 0}\ntiers:\n"+tierOK)
	if _, err := loadConfig(); err != nil {
		t.Fatalf("explicit zero: %v", err)
	}
}

func TestMergeConfigPrices(t *testing.T) {
	p := map[string]map[string]ModelPrice{"t": {"a": {Input: 1}}}
	dst := Config{Prices: p}
	mergeConfig(&dst, Config{MinContext: -1, UpstreamResponseHeaderTimeout: -1, RetryExhaustedAfter: -1})
	if dst.Prices["t"]["a"].Input != 1 {
		t.Fatalf("prices lost: %v", dst.Prices)
	}
	mergeConfig(&dst, Config{Prices: map[string]map[string]ModelPrice{"u": {"b": {Output: 2}}}, MinContext: -1, UpstreamResponseHeaderTimeout: -1, RetryExhaustedAfter: -1})
	if len(dst.Prices) != 1 || dst.Prices["u"]["b"].Output != 2 {
		t.Fatalf("prices not taken: %v", dst.Prices)
	}
}

func TestLoadConfigPrices(t *testing.T) {
	tiersYAML(t, "prices:\n  a:\n    x/m1: {input: 1.5, output: 2}\ntiers:\n"+tierOK)
	cfg, err := loadConfig()
	if err != nil || cfg.Prices["a"]["x/m1"] != (ModelPrice{1.5, 2}) {
		t.Fatalf("prices=%v err=%v", cfg.Prices, err)
	}
}

// capUpstream is a tier upstream that records the bodies it got and answers status/body.
type capUpstream struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newCapUpstream(t *testing.T, status int, ctype, body string) *capUpstream {
	t.Helper()
	u := &capUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.bodies = append(u.bodies, string(b))
		u.mu.Unlock()
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *capUpstream) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.bodies)
}

// capNow is today 10:00 UTC (not a fixed date: load drops rows older than the retention).
var capNow = func() time.Time {
	y, m, d := time.Now().UTC().Date()
	return time.Date(y, m, d, 10, 0, 0, 0, time.UTC)
}()

// capApp has tier 0 down and a paid tier "pago" (cap US$ 5, price US$ 1e6 per 1M tokens, so US$ 1 per token: a 10/3 usage costs US$ 13)
// followed by the free tier "gratis" when free is not nil.
func capApp(t *testing.T, paid, free *capUpstream, usageFile string, styles ...APIStyle) *App {
	t.Helper()
	if styles == nil {
		styles = []APIStyle{APIStyleOpenAI}
	}
	tiers := []TierConfig{{Name: "pago", BaseURL: paid.srv.URL, APIKey: "kp", DailyCap: 5, Styles: styles, Models: []TierModel{{Name: "a", ID: "a-id"}}}}
	if free != nil {
		tiers = append(tiers, TierConfig{Name: "gratis", BaseURL: free.srv.URL, APIKey: "kg", Free: true, Styles: styles, Models: []TierModel{{Name: "a", ID: "a-id"}}})
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(down.Close)
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: down.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour,
		UsageFile: usageFile, Tiers: tiers, Prices: map[string]map[string]ModelPrice{"pago": {"a-id": {Input: 1e6, Output: 1e6}}}})
	app.history.now = func() time.Time { return capNow }
	return app
}

func TestValidatePaidTier(t *testing.T) {
	base := func() Config {
		return Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"u"}, UpstreamBaseURL: "http://x", MaxRequestBodyBytes: 1, UsageFile: "u.json",
			Tiers:  []TierConfig{{Name: "pago", BaseURL: "http://t", APIKey: "k", DailyCap: 5, Models: []TierModel{{Name: "a", ID: "a-id"}}}},
			Prices: map[string]map[string]ModelPrice{"pago": {"a-id": {Input: 1, Output: 2}}}}
	}
	for name, tc := range map[string]struct {
		mod  func(*Config)
		want string // "" accepts
	}{
		"valid":          {func(c *Config) {}, ""},
		"no cap":         {func(c *Config) { c.Tiers[0].DailyCap = 0 }, "daily_cap"},
		"negative cap":   {func(c *Config) { c.Tiers[0].DailyCap = -1 }, "daily_cap"},
		"NaN cap":        {func(c *Config) { c.Tiers[0].DailyCap = math.NaN() }, "daily_cap"},
		"Inf cap":        {func(c *Config) { c.Tiers[0].DailyCap = math.Inf(1) }, "daily_cap"},
		"no usage_file":  {func(c *Config) { c.UsageFile = "" }, "usage_file"},
		"model no price": {func(c *Config) { c.Prices = nil }, `model "a-id"`},
		"free with cap":  {func(c *Config) { c.Tiers[0].Free = true }, "only for paid"},
		"free no cap":    {func(c *Config) { c.Tiers[0].Free, c.Tiers[0].DailyCap = true, 0 }, ""},
		"id falls to name": {func(c *Config) {
			c.Tiers[0].Models[0].ID = ""
			c.Prices["pago"] = map[string]ModelPrice{"a": {Output: 1}}
		}, ""},
		"zero price": {func(c *Config) { c.Prices["pago"]["a-id"] = ModelPrice{} }, `model "a-id"`},
		"reserved name": {func(c *Config) {
			c.Tiers[0].Name = tier0Name
			c.Prices = map[string]map[string]ModelPrice{tier0Name: {"a-id": {Input: 1}}}
		}, "reserved"},
	} {
		c := base()
		tc.mod(&c)
		err := validateConfig(c)
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Fatalf("%s: err %v, want %q", name, err, tc.want)
		}
	}
}

func TestPaidTierCapSkipsToNextTier(t *testing.T) {
	paid, free := newCapUpstream(t, 200, "application/json", usageBody), newCapUpstream(t, 200, "application/json", `{"from":"free"}`)
	app := capApp(t, paid, free, "")
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != 200 || len(paid.seen()) != 1 {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	if s := app.history.spent("pago"); s != 13 {
		t.Fatalf("spent %v", s)
	}
	rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != 200 || rec.Body.String() != `{"from":"free"}` || len(paid.seen()) != 1 || len(free.seen()) != 1 {
		t.Fatalf("second: %d %s paid=%d free=%d", rec.Code, rec.Body, len(paid.seen()), len(free.seen()))
	}
	body := statusBody(app)
	if !strings.Contains(body, "US$ 13.0000 / US$ 5.0000") || !strings.Contains(body, "<td>teto</td><td>50400s</td>") {
		t.Fatalf("status: %s", body)
	}
}

func TestPaidTierCapRetryAfterUntilMidnight(t *testing.T) {
	paid := newCapUpstream(t, 200, "application/json", usageBody)
	app := capApp(t, paid, nil, "")
	app.cooldowns.set(tier0CooldownKey, 100*time.Hour)
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "50400" || len(paid.seen()) != 1 {
		t.Fatalf("code=%d retry=%q hits=%d", rec.Code, rec.Header().Get("Retry-After"), len(paid.seen()))
	}
	app.history.now = func() time.Time { return capNow.Add(14*time.Hour - 500*time.Millisecond) } // under 1s left: minimum 1
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("retry=%q", rec.Header().Get("Retry-After"))
	}
	app.history.now = func() time.Time { return capNow.Add(14 * time.Hour) } // next day: spend is 0 again
	if app.history.spent("pago") != 0 {
		t.Fatal("spend did not reset")
	}
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != 200 || len(paid.seen()) != 2 {
		t.Fatalf("after midnight: %d hits=%d", rec.Code, len(paid.seen()))
	}
}

func TestPaidTierCapSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	paid, free := newCapUpstream(t, 200, "application/json", usageBody), newCapUpstream(t, 200, "application/json", `{}`)
	app := capApp(t, paid, free, path)
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	app.history.close()
	app2 := capApp(t, paid, free, path)
	t.Cleanup(app2.history.close)
	tierReq(app2, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if len(paid.seen()) != 1 || len(free.seen()) != 1 {
		t.Fatalf("paid=%d free=%d", len(paid.seen()), len(free.seen()))
	}
}

func TestPaidTierStreamWithoutUsageChargesMaxPrice(t *testing.T) {
	paid := newCapUpstream(t, 200, "text/event-stream", "data: [DONE]\n\n")
	app := capApp(t, paid, nil, "")
	app.config.Prices["pago"]["other"] = ModelPrice{Input: 2e6, Output: 3e6} // max over the whole map is 3e6
	rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a","stream":true}`, nil)
	r := app.history.snapshot()
	if rec.Code != 200 || len(r) != 1 || r[0].CostUSD != 3e6 || r[0].NoUsage != 1 {
		t.Fatalf("code=%d rows: %+v", rec.Code, r)
	}
	if !strings.Contains(statusBody(app), "<td>1</td><td>US$") {
		t.Fatal("Sem usage column missing")
	}
}

func TestPaidTierStreamIncludeUsage(t *testing.T) {
	paid := newCapUpstream(t, 503, "application/json", `{}`)
	free := newCapUpstream(t, 200, "application/json", `{}`)
	app := capApp(t, paid, free, "")
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a","stream":true,"stream_options":{"x":1}}`, nil)
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a","stream":true}`, nil) // paid tier is in cooldown now
	pb, fb := paid.seen(), free.seen()
	if len(pb) != 1 || !strings.Contains(pb[0], `"include_usage":true`) || !strings.Contains(pb[0], `"x":1`) {
		t.Fatalf("paid body: %v", pb)
	}
	if len(fb) != 2 || strings.Contains(fb[0], "include_usage") || !strings.Contains(fb[0], `"stream_options":{"x":1}`) || strings.Contains(fb[1], "stream_options") {
		t.Fatalf("free bodies (must not inherit the paid edit): %v", fb)
	}
}

func TestPaidTierStreamOptionsOddShapes(t *testing.T) {
	for _, so := range []string{`null`, `"x"`, `{"include_usage":false,"x":1}`} {
		paid := newCapUpstream(t, 200, "application/json", `{}`)
		app := capApp(t, paid, nil, "")
		rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a","stream":true,"stream_options":`+so+`}`, nil)
		if pb := paid.seen(); rec.Code != 200 || len(pb) != 1 || !strings.Contains(pb[0], `"include_usage":true`) {
			t.Fatalf("stream_options %s: status %d, body %v", so, rec.Code, pb)
		}
	}
}

func TestPaidTierAnthropicBodyUnchanged(t *testing.T) {
	paid := newCapUpstream(t, 200, "application/json", `{}`)
	app := capApp(t, paid, nil, "", APIStyleAnthropic)
	const body = `{"model":"a","stream":true}`
	tierReq(app, http.MethodPost, "/v1/messages", body, nil)
	if got := paid.seen(); len(got) != 1 || strings.Contains(got[0], "stream_options") {
		t.Fatalf("body: %v", got)
	}
}

func TestPaidTierCapRetryAfterShorterCooldownWins(t *testing.T) {
	paid, free := newCapUpstream(t, 200, "application/json", usageBody), newCapUpstream(t, 200, "application/json", `{}`)
	app := capApp(t, paid, free, "")
	app.cooldowns.set(tier0CooldownKey, 100*time.Hour)
	tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil) // paid tier reaches its cap
	app.cooldowns.set("gratis", 60*time.Second)
	rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil)
	if got := rec.Header().Get("Retry-After"); rec.Code != http.StatusTooManyRequests || got != "60" {
		t.Fatalf("code=%d retry=%q", rec.Code, got)
	}
}

func TestLoadConfigPaidTier(t *testing.T) {
	paid := strings.Replace(tierOK, "free: true", "daily_cap: 5.5", 1)
	tiersYAML(t, "usage_file: /tmp/usage.json\nprices:\n  a:\n    x/m1: {input: 1, output: 2}\ntiers:\n"+paid)
	cfg, err := loadConfig()
	if err != nil || len(cfg.Tiers) != 1 || cfg.Tiers[0].Free || cfg.Tiers[0].DailyCap != 5.5 {
		t.Fatalf("tiers=%+v err=%v", cfg.Tiers, err)
	}
}

// alertApp has two paid tiers (p1 serves model a, p2 serves b) and a free one (f serves c); sendMail is replaced
// by a fake that reports each message on the returned channel (or fails with sendErr).
func alertApp(t *testing.T, usageFile string, sendErr error) (*App, chan string) {
	t.Helper()
	msgs := make(chan string, 16)
	old := sendMail
	sendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte, useTLS, useStartTLS bool) error {
		msgs <- string(msg)
		return sendErr
	}
	t.Cleanup(func() { sendMail = old })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(usageBody))
	}))
	t.Cleanup(up.Close)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })) // tier 0 fails so the extra tiers answer
	t.Cleanup(down.Close)
	tier := func(name, model string, free bool) TierConfig {
		dailyCap := 5.0
		if free {
			dailyCap = 0
		}
		return TierConfig{Name: name, BaseURL: up.URL, APIKey: "k" + name, Free: free, DailyCap: dailyCap, Styles: []APIStyle{APIStyleOpenAI}, Models: []TierModel{{Name: model, ID: model + "-id"}}}
	}
	prices := map[string]map[string]ModelPrice{"p1": {"a-id": {Input: 1, Output: 1}}, "p2": {"b-id": {Input: 1, Output: 1}}}
	app := newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, UpstreamBaseURL: down.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour,
		UsageFile: usageFile, Prices: prices, Tiers: []TierConfig{tier("p1", "a", false), tier("p2", "b", false), tier("f", "c", true)},
		SMTP: SMTPConfig{Host: "h", Port: 25, From: "f@x", To: "t@x"}})
	app.history.now = func() time.Time { return capNow }
	return app, msgs
}

func wantAlerts(t *testing.T, msgs chan string, want ...string) {
	t.Helper()
	for _, w := range want {
		select {
		case m := <-msgs:
			if !strings.Contains(m, w) {
				t.Fatalf("alert %q does not mention %q", m, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no alert for %q", w)
		}
	}
	select {
	case m := <-msgs:
		t.Fatalf("unexpected alert: %q", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPaidFirstUseAlert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	app, msgs := alertApp(t, path, nil)
	postModel(app, "a")
	wantAlerts(t, msgs, `"p1"`)
	if app.history.alerts["p1"] != capNow.Format("2006-01-02") {
		t.Fatalf("alerts: %v", app.history.alerts)
	}
	postModel(app, "a") // same tier, same day
	postModel(app, "c") // free tier
	postModel(app, "zzz")
	tierReq(app, http.MethodGet, "/v1/models", "", nil)
	wantAlerts(t, msgs)
	postModel(app, "b") // another paid tier
	wantAlerts(t, msgs, `"p2"`)
	app.history.flush()

	b, _ := os.ReadFile(path)
	var f struct {
		Rows   []usageRow        `json:"rows"`
		Alerts map[string]string `json:"alerts"`
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Alerts) != 2 || len(f.Rows) != 3 {
		t.Fatalf("file %s err=%v", b, err)
	}

	app2, msgs2 := alertApp(t, path, nil) // restart, same day
	postModel(app2, "a")
	wantAlerts(t, msgs2)
	next := capNow.Add(24 * time.Hour)
	app2.history.now = func() time.Time { return next }
	postModel(app2, "a")
	wantAlerts(t, msgs2, `"p1"`)
}

func TestPaidFirstUseAlertSMTPFailureNotRetried(t *testing.T) {
	app, msgs := alertApp(t, "", errors.New("boom"))
	if rec := tierReq(app, http.MethodPost, "/v1/chat/completions", `{"model":"a"}`, nil); rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	wantAlerts(t, msgs, `"p1"`)
	postModel(app, "a")
	wantAlerts(t, msgs)
}

func TestUsageFileWithoutAlerts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(`{"rows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app, msgs := alertApp(t, path, nil)
	postModel(app, "a")
	wantAlerts(t, msgs, `"p1"`)
}
