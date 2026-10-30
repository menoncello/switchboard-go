package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	usageRetentionDays = 90
	usageFlushDelay    = 3 * time.Second
)

type usageKey struct {
	Day     string `json:"day"`
	Project string `json:"project"`
	Model   string `json:"model"`
	Key     string `json:"key"`
	Tier    string `json:"tier"`
}

type usageCounts struct {
	Requests      int `json:"requests"`
	TokensIn      int `json:"tokens_in"`
	TokensOut     int `json:"tokens_out"`
	TokensCacheIn int `json:"tokens_cache_in"`
	NoUsage       int `json:"no_usage"`
}

// usageRow is one line of the usage file: a dimension and its totals.
type usageRow struct {
	usageKey
	usageCounts
}

// usageHistory sums served requests per (day, project, model, key, tier) and
// saves them to path (JSON, delayed atomic write). Empty path keeps memory only.
// shortcut: the model comes from the client (cut at 128 bytes) and rows have no
// cap, so an authenticated client can inflate the file; 90-day retention is the
// only brake. Cap rows when there is more than one untrusted client.
type usageHistory struct {
	path   string
	loc    *time.Location
	now    func() time.Time
	delay  time.Duration
	logger *slog.Logger

	flushMu sync.Mutex // serializes flushes; taken before mu
	mu      sync.Mutex
	rows    map[usageKey]*usageCounts
	dirty   bool
	timer   *time.Timer
}

func newUsageHistory(path string, loc *time.Location, logger *slog.Logger) *usageHistory {
	if loc == nil {
		loc = time.UTC
	}
	h := &usageHistory{path: path, loc: loc, now: time.Now, delay: usageFlushDelay, logger: logger, rows: map[usageKey]*usageCounts{}}
	h.load()
	return h
}

// cutoff is the oldest day kept, as YYYY-MM-DD.
func (h *usageHistory) cutoff() string {
	return h.now().In(h.loc).AddDate(0, 0, -usageRetentionDays).Format("2006-01-02")
}

func (h *usageHistory) load() {
	if h.path == "" {
		return
	}
	b, err := os.ReadFile(h.path)
	if errors.Is(err, os.ErrNotExist) {
		h.logger.Info("no usage file yet, starting with empty usage history")
		return
	}
	var f struct {
		Rows []usageRow `json:"rows"`
	}
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err != nil {
		// Move the file aside so the first flush does not overwrite what could not be read.
		bad := h.path + ".bad"
		if rerr := os.Rename(h.path, bad); rerr != nil {
			h.logger.Error("usage file could not be moved aside, the next write will replace it", "error", rerr.Error())
		} else {
			h.logger.Warn("usage file not loaded, moved aside, starting empty", "error", err.Error(), "moved_to", bad)
		}
		return
	}
	cut := h.cutoff()
	for _, r := range f.Rows {
		if r.Day < cut {
			continue
		}
		c := h.rows[r.usageKey]
		if c == nil {
			c = &usageCounts{}
			h.rows[r.usageKey] = c
		}
		c.Requests += r.Requests
		c.TokensIn += r.TokensIn
		c.TokensOut += r.TokensOut
		c.TokensCacheIn += r.TokensCacheIn
		c.NoUsage += r.NoUsage
	}
}

func (h *usageHistory) record(project, model, key, tier string, u usageTokens, noUsage bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := usageKey{h.now().In(h.loc).Format("2006-01-02"), project, model, key, tier}
	c := h.rows[k]
	if c == nil {
		c = &usageCounts{}
		h.rows[k] = c
	}
	c.Requests++
	if u.found {
		c.TokensIn += u.in
		c.TokensOut += u.out
		c.TokensCacheIn += u.cache
	}
	if noUsage {
		c.NoUsage++
	}
	h.dirty = true
	if h.path != "" && h.timer == nil {
		h.timer = time.AfterFunc(h.delay, func() {
			h.mu.Lock()
			h.timer = nil
			h.mu.Unlock()
			h.flush()
		})
	}
}

func (h *usageHistory) snapshotLocked() []usageRow {
	rows := make([]usageRow, 0, len(h.rows))
	for k, c := range h.rows {
		rows = append(rows, usageRow{k, *c})
	}
	slices.SortFunc(rows, func(a, b usageRow) int {
		return strings.Compare(a.Day+"\x00"+a.Project+"\x00"+a.Model+"\x00"+a.Key+"\x00"+a.Tier, b.Day+"\x00"+b.Project+"\x00"+b.Model+"\x00"+b.Key+"\x00"+b.Tier)
	})
	return rows
}

// snapshot returns the rows sorted by day, project, model, key, tier.
func (h *usageHistory) snapshot() []usageRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotLocked()
}

// window returns today (YYYY-MM-DD in h.loc) and the rows of the last days days, today included.
func (h *usageHistory) window(days int) (string, []usageRow) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now().In(h.loc)
	today, from := now.Format("2006-01-02"), now.AddDate(0, 0, -(days-1)).Format("2006-01-02")
	var rows []usageRow
	for _, r := range h.snapshotLocked() {
		if r.Day >= from && r.Day <= today {
			rows = append(rows, r)
		}
	}
	return today, rows
}

// flush drops days past retention and writes the file. The write runs outside
// h.mu so requests are not blocked by the disk. A failure is only logged and
// dirty is set again for the next flush.
func (h *usageHistory) flush() {
	if h.path == "" {
		return
	}
	h.flushMu.Lock()
	defer h.flushMu.Unlock()
	h.mu.Lock()
	cut := h.cutoff()
	for k := range h.rows {
		if k.Day < cut {
			delete(h.rows, k)
			h.dirty = true
		}
	}
	if !h.dirty {
		h.mu.Unlock()
		return
	}
	h.dirty = false // a record that lands during the write sets it again
	b, err := json.Marshal(struct {
		Rows []usageRow `json:"rows"`
	}{h.snapshotLocked()})
	h.mu.Unlock()
	if err == nil {
		err = writeFileAtomic(h.path, b)
	}
	if err != nil {
		h.logger.Error("usage file not saved", "error", err.Error())
		h.mu.Lock()
		h.dirty = true
		h.mu.Unlock()
	}
}

func (h *usageHistory) close() {
	h.mu.Lock()
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	h.mu.Unlock()
	h.flush()
}
