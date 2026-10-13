package browser

import (
	"context"
	"testing"

	"github.com/chromedp/cdproto/target"
)

func TestFailedAttachIsNotSticky(t *testing.T) {
	s := &Session{
		tabPool:   make(map[target.ID]*tabPoolEntry),
		observers: make(map[target.ID]*tabObserver),
	}
	cancelled := 0
	cancel := func() { cancelled++ }
	const id target.ID = "TAB"

	ent := &tabPoolEntry{ctx: context.Background(), cancel: cancel, ready: make(chan struct{})}
	s.tabPool[id] = ent
	s.initializeTabPoolEntry(id, ent)
	if _, ok := s.tabPool[id]; ok || ent.err == nil || cancelled != 0 {
		t.Fatalf("pool entry=%v err=%v cancelled=%d", s.tabPool[id], ent.err, cancelled)
	}

	obs := &tabObserver{tid: id, ctx: context.Background(), cancel: cancel, ready: make(chan struct{})}
	s.observers[id] = obs
	s.initializeObserver(obs)
	if _, ok := s.observers[id]; ok || obs.initErr == nil || cancelled != 0 {
		t.Fatalf("observer=%v err=%v cancelled=%d", s.observers[id], obs.initErr, cancelled)
	}

	var calls int
	orig := disableObservationDomains
	t.Cleanup(func() { disableObservationDomains = orig })
	disableObservationDomains = func(context.Context, context.Context) error {
		calls++
		if calls == 1 {
			return context.DeadlineExceeded
		}
		return nil
	}
	obs = &tabObserver{tid: id, ctx: context.Background()}
	if err := obs.disableUntilLeased(context.Background()); err == nil || obs.domainsDisabled {
		t.Fatalf("err=%v disabled=%v", err, obs.domainsDisabled)
	}
	if err := obs.disableUntilLeased(context.Background()); err != nil || !obs.domainsDisabled || calls != 2 {
		t.Fatalf("err=%v disabled=%v calls=%d", err, obs.domainsDisabled, calls)
	}
}
