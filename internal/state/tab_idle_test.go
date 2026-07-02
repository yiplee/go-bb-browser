package state

import (
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
)

func TestTabIdleTrackerMarkTouchForget(t *testing.T) {
	tracker := NewTabIdleTracker()
	id := target.ID("ABCDEF123456")
	tracker.MarkManaged(id)
	tracker.Touch(id)

	before := time.Now()
	tracker.Forget(id)
	if got := tracker.Expired(before.Add(time.Hour), time.Minute); len(got) != 0 {
		t.Fatalf("after forget: expired %#v want empty", got)
	}

	tracker.Touch(id)
	if got := tracker.Expired(before.Add(time.Hour), time.Minute); len(got) != 0 {
		t.Fatalf("touch on unknown tab should not register: %#v", got)
	}
}

func TestTabIdleTrackerExpired(t *testing.T) {
	tracker := NewTabIdleTracker()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	oldID := target.ID("old")
	newID := target.ID("new")

	tracker.mu.Lock()
	tracker.managed[oldID] = now.Add(-6 * time.Minute)
	tracker.managed[newID] = now.Add(-1 * time.Minute)
	tracker.mu.Unlock()

	got := tracker.Expired(now, 5*time.Minute)
	if len(got) != 1 || got[0] != oldID {
		t.Fatalf("Expired: %#v want [%s]", got, oldID)
	}
	if got := tracker.Expired(now, 0); len(got) != 0 {
		t.Fatalf("timeout 0: %#v want empty", got)
	}
}

func TestTabIdleTrackerSyncPresent(t *testing.T) {
	tracker := NewTabIdleTracker()
	keep := target.ID("keep")
	drop := target.ID("drop")
	tracker.MarkManaged(keep)
	tracker.MarkManaged(drop)

	present := map[target.ID]struct{}{keep: {}}
	tracker.SyncPresent(present)

	got := tracker.Expired(time.Now().Add(time.Hour), time.Minute)
	if len(got) != 1 || got[0] != keep {
		t.Fatalf("after sync: expired %#v want [%s]", got, keep)
	}
}

func TestTabIdleTrackerSnapshot(t *testing.T) {
	tracker := NewTabIdleTracker()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	id := target.ID("snap1234")
	tracker.MarkManagedAt(id, at)

	got := tracker.Snapshot()
	if len(got) != 1 {
		t.Fatalf("Snapshot len=%d want 1", len(got))
	}
	if !got[id].Equal(at) {
		t.Fatalf("Snapshot[%s]=%v want %v", id, got[id], at)
	}
}
