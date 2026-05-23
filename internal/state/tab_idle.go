package state

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// TabIdleTracker tracks last-activity time for daemon-created tabs eligible for idle cleanup.
type TabIdleTracker struct {
	mu      sync.Mutex
	managed map[string]time.Time // short id -> last activity
}

// NewTabIdleTracker returns an empty idle tracker.
func NewTabIdleTracker() *TabIdleTracker {
	return &TabIdleTracker{
		managed: make(map[string]time.Time),
	}
}

// MarkManaged registers a daemon-created tab and sets its last activity to now.
func (t *TabIdleTracker) MarkManaged(short string) {
	short = strings.TrimSpace(short)
	if short == "" || t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.managed[short] = time.Now()
}

// Touch updates last activity for a managed tab.
func (t *TabIdleTracker) Touch(short string) {
	short = strings.TrimSpace(short)
	if short == "" || t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.managed[short]; ok {
		t.managed[short] = time.Now()
	}
}

// Forget removes a tab from idle tracking.
func (t *TabIdleTracker) Forget(short string) {
	short = strings.TrimSpace(short)
	if short == "" || t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.managed, short)
}

// SyncPresent removes managed entries whose short id is no longer present in the browser.
func (t *TabIdleTracker) SyncPresent(present map[string]struct{}) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for short := range t.managed {
		if _, ok := present[short]; !ok {
			delete(t.managed, short)
		}
	}
}

// Expired returns managed short ids whose last activity is at or beyond timeout.
func (t *TabIdleTracker) Expired(now time.Time, timeout time.Duration) []string {
	if t == nil || timeout <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0)
	for short, last := range t.managed {
		if !now.Before(last.Add(timeout)) {
			out = append(out, short)
		}
	}
	slices.Sort(out)
	return out
}
