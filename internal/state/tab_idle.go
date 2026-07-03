package state

import (
	"slices"
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"
)

// TabIdleTracker tracks last-activity time for daemon-created tabs eligible for idle cleanup.
type TabIdleTracker struct {
	mu      sync.Mutex
	managed map[target.ID]time.Time
}

// NewTabIdleTracker returns an empty idle tracker.
func NewTabIdleTracker() *TabIdleTracker {
	return &TabIdleTracker{
		managed: make(map[target.ID]time.Time),
	}
}

// MarkManaged registers a daemon-created tab and sets its last activity to now.
func (t *TabIdleTracker) MarkManaged(id target.ID) {
	if t == nil || id == "" {
		return
	}
	t.MarkManagedAt(id, time.Now())
}

// MarkManagedAt registers a daemon-created tab with an explicit last-activity time.
func (t *TabIdleTracker) MarkManagedAt(id target.ID, at time.Time) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.managed[id] = at
}

// MarkManagedIfAbsentAt registers a tab only if it is not already tracked, so
// repeated disk reconciliation never overwrites (and never re-clamps) the
// last-activity time of a tab that is already being tracked in memory.
func (t *TabIdleTracker) MarkManagedIfAbsentAt(id target.ID, at time.Time) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.managed[id]; ok {
		return
	}
	t.managed[id] = at
}

// Touch updates last activity for a managed tab.
func (t *TabIdleTracker) Touch(id target.ID) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.managed[id]; ok {
		t.managed[id] = time.Now()
	}
}

// Forget removes a tab from idle tracking.
func (t *TabIdleTracker) Forget(id target.ID) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.managed, id)
}

// SyncPresent removes managed entries whose target id is no longer present in the browser.
func (t *TabIdleTracker) SyncPresent(present map[target.ID]struct{}) {
	t.SyncPresentReturnRemoved(present)
}

// SyncPresentReturnRemoved removes absent tabs and returns the removed target ids.
func (t *TabIdleTracker) SyncPresentReturnRemoved(present map[target.ID]struct{}) []target.ID {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var removed []target.ID
	for id := range t.managed {
		if _, ok := present[id]; !ok {
			removed = append(removed, id)
			delete(t.managed, id)
		}
	}
	return removed
}

// Snapshot returns a copy of managed target ids and their last-activity times.
func (t *TabIdleTracker) Snapshot() map[target.ID]time.Time {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[target.ID]time.Time, len(t.managed))
	for id, at := range t.managed {
		out[id] = at
	}
	return out
}

// Expired returns managed target ids whose last activity is at or beyond timeout.
func (t *TabIdleTracker) Expired(now time.Time, timeout time.Duration) []target.ID {
	if t == nil || timeout <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]target.ID, 0)
	for id, last := range t.managed {
		if !now.Before(last.Add(timeout)) {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, func(a, b target.ID) int {
		if x, y := string(a), string(b); x < y {
			return -1
		} else if x > y {
			return 1
		}
		return 0
	})
	return out
}
