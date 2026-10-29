package main

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sync"
	"time"
)

type KeyState string

const (
	KeyUnknown   KeyState = "unknown"
	KeyAvailable KeyState = "available"
	KeyExhausted KeyState = "exhausted"
)

type KeyManager struct {
	mu             sync.Mutex
	keys           []string
	states         []KeyState
	last429        map[int]time.Time
	current        int
	allNotified    bool
	notifiedSwitch map[int]bool
	// cooldown is how long an exhausted key waits before it becomes eligible for
	// an automatic retry. Zero means exhausted keys are immediately eligible
	// again (retry on the next request).
	cooldown time.Duration
	// now is injectable so tests can drive the cooldown with a fake clock.
	now func() time.Time
}

func NewKeyManager(keys []string, cooldown time.Duration) *KeyManager {
	states := make([]KeyState, len(keys))
	for i := range states {
		states[i] = KeyUnknown
	}
	return &KeyManager{keys: append([]string(nil), keys...), states: states, last429: map[int]time.Time{}, notifiedSwitch: map[int]bool{}, cooldown: cooldown, now: func() time.Time { return time.Now().UTC() }}
}

// eligibleLocked reports whether key i may be handed out right now. A key that is
// not exhausted is always eligible; an exhausted key becomes eligible again once
// its cooldown has elapsed since the last quota error (so the next real request
// acts as a probe).
func (m *KeyManager) eligibleLocked(i int) bool {
	if m.states[i] != KeyExhausted {
		return true
	}
	t, ok := m.last429[i]
	if !ok {
		return true
	}
	return !m.now().Before(t.Add(m.cooldown))
}

func (m *KeyManager) Current() (int, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.keys) == 0 {
		return 0, "", false
	}
	if !m.eligibleLocked(m.current) {
		m.advanceLocked()
	}
	if !m.eligibleLocked(m.current) {
		return 0, "", false
	}
	return m.current, m.keys[m.current], true
}

func (m *KeyManager) MarkExhausted(i int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 || i >= len(m.keys) {
		return
	}
	m.states[i] = KeyExhausted
	m.last429[i] = m.now()
	m.advanceLocked()
}

func (m *KeyManager) ShouldNotifySwitch(i int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifiedSwitch[i] {
		return false
	}
	m.notifiedSwitch[i] = true
	return true
}

func (m *KeyManager) ShouldNotifyAllExhausted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.allNotified {
		return false
	}
	if !m.allExhaustedLocked() {
		return false
	}
	m.allNotified = true
	return true
}

func (m *KeyManager) advanceLocked() {
	if len(m.keys) == 0 {
		return
	}
	start := m.current
	for step := 1; step <= len(m.keys); step++ {
		next := (start + step) % len(m.keys)
		if m.eligibleLocked(next) {
			m.current = next
			return
		}
	}
	m.current = start
}

// RetryAfterSeconds returns the number of seconds until the soonest exhausted key
// becomes eligible for a retry, for use as a Retry-After header on the local
// all-exhausted 429. The second return value is false when a Retry-After hint is
// not meaningful: the cooldown is disabled, or some key is already usable.
func (m *KeyManager) RetryAfterSeconds() (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cooldown <= 0 {
		return 0, false
	}
	var soonest time.Time
	found := false
	for i := range m.keys {
		if m.states[i] != KeyExhausted {
			return 0, false
		}
		t, ok := m.last429[i]
		if !ok {
			return 0, false
		}
		eligibleAt := t.Add(m.cooldown)
		if !found || eligibleAt.Before(soonest) {
			soonest = eligibleAt
			found = true
		}
	}
	if !found {
		return 0, false
	}
	secs := int(math.Ceil(soonest.Sub(m.now()).Seconds()))
	if secs < 1 {
		secs = 1
	}
	return secs, true
}

// CooldownRemaining returns the real time until the soonest key becomes eligible,
// without RetryAfterSeconds' 1s floor, so the status page can tell an idle proxy
// with already-elapsed cooldowns apart from a live one. ok is false when nothing is waiting.
func (m *KeyManager) CooldownRemaining() (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cooldown <= 0 {
		return 0, false
	}
	var soonest time.Time
	found := false
	for i := range m.keys {
		if m.states[i] != KeyExhausted {
			return 0, false
		}
		t, ok := m.last429[i]
		if !ok {
			return 0, false
		}
		eligibleAt := t.Add(m.cooldown)
		if !found || eligibleAt.Before(soonest) {
			soonest = eligibleAt
			found = true
		}
	}
	if d := soonest.Sub(m.now()); found && d > 0 {
		return d, true
	}
	return 0, false
}

func (m *KeyManager) AllExhausted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allExhaustedLocked()
}

func (m *KeyManager) allExhaustedLocked() bool {
	for _, st := range m.states {
		if st != KeyExhausted {
			return false
		}
	}
	return true
}

func (m *KeyManager) Status() StatusResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := make([]PerKeyStatus, len(m.keys))
	for i := range m.keys {
		state := m.states[i]
		display := state
		if i == m.current && state != KeyExhausted {
			display = KeyAvailable
		}
		eligible := m.eligibleLocked(i)
		ps := PerKeyStatus{Index: i, State: string(display), Last429Time: m.last429String(i), Current: i == m.current, Eligible: eligible}
		if state == KeyExhausted && m.cooldown > 0 && !eligible {
			if t, ok := m.last429[i]; ok {
				secs := int(math.Ceil(t.Add(m.cooldown).Sub(m.now()).Seconds()))
				if secs < 0 {
					secs = 0
				}
				ps.RetryAfterSeconds = secs
			}
		}
		states[i] = ps
	}
	return StatusResponse{CurrentKeyIndex: m.current, Keys: states, RetryExhaustedAfterSeconds: int(m.cooldown / time.Second), Note: "unknown means the key has not yet been validated or used since startup; an exhausted key becomes eligible for an automatic retry once retry_exhausted_after_seconds has elapsed since last_429_time (0 disables the cooldown); remaining usage is unavailable from opencode-go API."}
}

func (m *KeyManager) SetState(i int, state KeyState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 || i >= len(m.states) {
		return
	}
	m.states[i] = state
	if state == KeyExhausted {
		m.last429[i] = m.now()
		return
	}
	// Recovery: the key is usable again. Drop its cooldown timestamp and re-arm
	// the notification flags so a future depletion round alerts again instead of
	// staying silent.
	delete(m.last429, i)
	m.notifiedSwitch[i] = false
	m.allNotified = false
}

func (m *KeyManager) MarkAvailable(i int) { m.SetState(i, KeyAvailable) }

func (m *KeyManager) last429String(i int) string {
	if t, ok := m.last429[i]; ok && !t.IsZero() {
		return t.Format(time.RFC3339)
	}
	return ""
}

type PerKeyStatus struct {
	Index       int    `json:"index"`
	State       string `json:"state"`
	Last429Time string `json:"last_429_time,omitempty"`
	Current     bool   `json:"current"`
	// Eligible reports whether the key may be handed out on the next request. An
	// exhausted key is eligible again once its cooldown has elapsed.
	Eligible bool `json:"eligible"`
	// RetryAfterSeconds is the remaining cooldown for an exhausted key that is not
	// yet eligible. Omitted for eligible keys and when the cooldown is disabled.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
}
type StatusResponse struct {
	CurrentKeyIndex int            `json:"current_key_index"`
	Keys            []PerKeyStatus `json:"keys"`
	// RetryExhaustedAfterSeconds is the configured cooldown before an exhausted
	// key is retried automatically. Zero means the cooldown is disabled.
	RetryExhaustedAfterSeconds int    `json:"retry_exhausted_after_seconds"`
	Note                       string `json:"note"`
}

type ValidateKeyResult struct {
	Index  int    `json:"index"`
	State  string `json:"state"`
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

type ValidateKeysResponse struct {
	Results []ValidateKeyResult `json:"results"`
}

// keyID identifies a key in the state file without revealing it: a prefix of its SHA-256.
func keyID(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:8])
}

// snapshot is the exhausted keys still inside their cooldown, by keyID, with the
// time of their last quota error. With the cooldown off nothing is kept.
func (m *KeyManager) snapshot() map[string]time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]time.Time{}
	for i, k := range m.keys {
		if t, ok := m.last429[i]; ok && m.states[i] == KeyExhausted && m.cooldown > 0 && m.now().Before(t.Add(m.cooldown)) {
			out[keyID(k)] = t
		}
	}
	return out
}

// restore marks exhausted the keys saved by snapshot whose cooldown has not ended.
func (m *KeyManager) restore(in map[string]time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cooldown <= 0 {
		return
	}
	for i, k := range m.keys {
		if t, ok := in[keyID(k)]; ok && m.now().Before(t.Add(m.cooldown)) {
			m.states[i] = KeyExhausted
			m.last429[i] = t
		}
	}
	if !m.eligibleLocked(m.current) {
		m.advanceLocked()
	}
	// All keys exhausted before the restart: the "all exhausted" email already went out.
	if m.allExhaustedLocked() {
		m.allNotified = true
	}
}
