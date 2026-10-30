package main

import (
	"bytes"
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
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(up.Close)
	return newApp(Config{ProxyAPIKey: "p", UpstreamAPIKeys: []string{"k"}, KeyNames: []string{"main"}, UpstreamBaseURL: up.URL, MaxRequestBodyBytes: 1 << 20, RetryExhaustedAfter: time.Hour, UsageFile: usageFile})
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
	app = historyApp(t, 500, `{}`, "")
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
	for tz, want := range map[string]string{"America/Sao_Paulo": "2026-10-29", "": "2026-10-30"} {
		app := historyApp(t, 200, usageBody, "")
		loc, _ := time.LoadLocation(tz)
		app.history = newUsageHistory("", loc, app.logger)
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
	if !strings.Contains(logs.String(), "usage file not saved") {
		t.Fatalf("log: %q", logs.String())
	}
}

func TestHistoryDelayedWrite(t *testing.T) {
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
