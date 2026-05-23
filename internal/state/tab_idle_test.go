package state

import (
	"testing"
	"time"
)

func TestTabIdleTrackerMarkTouchForget(t *testing.T) {
	tracker := NewTabIdleTracker()
	tracker.MarkManaged("abcd")
	tracker.Touch("abcd")

	before := time.Now()
	tracker.Forget("abcd")
	if got := tracker.Expired(before.Add(time.Hour), time.Minute); len(got) != 0 {
		t.Fatalf("after forget: expired %#v want empty", got)
	}

	tracker.Touch("abcd")
	if got := tracker.Expired(before.Add(time.Hour), time.Minute); len(got) != 0 {
		t.Fatalf("touch on unknown tab should not register: %#v", got)
	}
}

func TestTabIdleTrackerExpired(t *testing.T) {
	tracker := NewTabIdleTracker()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	tracker.mu.Lock()
	tracker.managed["old"] = now.Add(-6 * time.Minute)
	tracker.managed["new"] = now.Add(-1 * time.Minute)
	tracker.mu.Unlock()

	got := tracker.Expired(now, 5*time.Minute)
	if len(got) != 1 || got[0] != "old" {
		t.Fatalf("Expired: %#v want [old]", got)
	}
	if got := tracker.Expired(now, 0); len(got) != 0 {
		t.Fatalf("timeout 0: %#v want empty", got)
	}
}

func TestTabIdleTrackerSyncPresent(t *testing.T) {
	tracker := NewTabIdleTracker()
	tracker.MarkManaged("keep")
	tracker.MarkManaged("drop")

	present := map[string]struct{}{"keep": {}}
	tracker.SyncPresent(present)

	got := tracker.Expired(time.Now().Add(time.Hour), time.Minute)
	if len(got) != 1 || got[0] != "keep" {
		t.Fatalf("after sync: expired %#v want [keep]", got)
	}
}
