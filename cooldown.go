package main

import (
	"sync"
	"time"
)

// tierCooldowns remembers, per tier name, until when the tier is skipped after a failure status.
type tierCooldowns struct {
	mu    sync.Mutex
	until map[string]time.Time
	// now is injectable so tests can drive the cooldown with a fake clock.
	now func() time.Time
}

func newTierCooldowns() *tierCooldowns {
	return &tierCooldowns{until: map[string]time.Time{}, now: time.Now}
}

// set puts name in cooldown for d; d <= 0 means no cooldown.
func (c *tierCooldowns) set(name string, d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.until[name] = c.now().Add(d)
}

// remaining is the time left of name's cooldown, 0 when it is not in cooldown.
func (c *tierCooldowns) remaining(name string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.until[name].Sub(c.now()); d > 0 {
		return d
	}
	delete(c.until, name)
	return 0
}
