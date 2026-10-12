package browser

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func TestRoutePausedCallbackReturnsWithoutCDPOrRouteLock(t *testing.T) {
	rs := newRouteState()
	rs.rules["tab"] = []NetworkRouteRule{{
		URLPattern: "https://example.com/*",
		Abort:      true,
	}}
	var cdp atomic.Int32
	orig := routeCDP
	routeCDP = func(context.Context, ...chromedp.Action) error {
		cdp.Add(1)
		return nil
	}
	t.Cleanup(func() { routeCDP = orig })

	w := &routeWorker{wake: make(chan struct{}, 1)}
	ev := &fetch.EventRequestPaused{
		RequestID: "req-1",
		Request:   &network.Request{URL: "https://example.com/a"},
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	const n = 32
	done := make(chan struct{})
	go func() {
		for range n {
			w.onPaused(ev)
		}
		w.onPaused("not-a-paused-event")
		w.onPaused(&fetch.EventRequestPaused{RequestID: "no-request"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("paused callback blocked while routeState.mu was held")
	}
	if got := cdp.Load(); got != 0 {
		t.Fatalf("callback issued %d CDP calls", got)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) != n {
		t.Fatalf("queued %d paused events, want %d", len(w.queue), n)
	}
}

func TestRouteWorkerReleasesStateLockBeforeCDP(t *testing.T) {
	rs := newRouteState()
	rs.rules["tab"] = []NetworkRouteRule{{
		URLPattern: "https://example.com/*",
		Abort:      true,
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	orig := routeCDP
	routeCDP = func(context.Context, ...chromedp.Action) error {
		close(started)
		<-release
		return nil
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		routeCDP = orig
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &routeWorker{cancel: cancel, wake: make(chan struct{}, 1)}
	go w.loop(ctx, rs, "tab", context.Background())
	w.onPaused(&fetch.EventRequestPaused{
		RequestID: "req-1",
		Request:   &network.Request{URL: "https://example.com/a"},
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach CDP")
	}
	locked := make(chan struct{})
	go func() {
		rs.mu.Lock()
		rs.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("routeState.mu held across CDP")
	}
}

func armRoute(rs *routeState, tabCtx context.Context, tabID target.ID) bool {
	rules, gen := rs.snapshot(tabID)
	if len(rules) == 0 {
		return false
	}
	w := rs.ensureWorker(tabID, tabCtx, gen)
	if w == nil {
		return false
	}
	return w.startListening(tabCtx, rs, tabID, gen)
}

func TestRouteListenerLifecycle(t *testing.T) {
	var mu sync.Mutex
	var got []context.Context
	orig := listenTarget
	listenTarget = func(ctx context.Context, _ func(any)) {
		mu.Lock()
		got = append(got, ctx)
		mu.Unlock()
	}
	t.Cleanup(func() { listenTarget = orig })
	active := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, ctx := range got {
			if ctx.Err() == nil {
				n++
			}
		}
		return n
	}
	calls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(got)
	}

	rs := newRouteState()
	const tab target.ID = "tab"
	tabCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rule := []NetworkRouteRule{{URLPattern: "https://example.com/*"}}
	rs.rules[tab] = append([]NetworkRouteRule(nil), rule...)
	if !armRoute(rs, tabCtx, tab) || !armRoute(rs, tabCtx, tab) {
		t.Fatal("adding a rule did not keep a listener")
	}
	if calls() != 1 || active() != 1 {
		t.Fatalf("after add: calls=%d active=%d", calls(), active())
	}

	delete(rs.rules, tab)
	if w := rs.worker(tab); w != nil {
		w.stopListening()
	}
	if active() != 0 {
		t.Fatal("listener still active after the last rule was removed")
	}

	rs.rules[tab] = append([]NetworkRouteRule(nil), rule...)
	if !armRoute(rs, tabCtx, tab) {
		t.Fatal("re-add did not listen")
	}
	if calls() != 2 || active() != 1 {
		t.Fatalf("after re-add: calls=%d active=%d", calls(), active())
	}
}

func TestClearRoutesForTargetWinsOverListen(t *testing.T) {
	orig := listenTarget
	t.Cleanup(func() { listenTarget = orig })
	listenTarget = func(context.Context, func(any)) {}

	const tab target.ID = "tab"
	rule := []NetworkRouteRule{{URLPattern: "https://example.com/*"}}
	rs := newRouteState()
	rs.rules[tab] = append([]NetworkRouteRule(nil), rule...)
	_, gen := rs.snapshot(tab)
	(&Session{routeState: rs}).ClearRoutesForTarget(tab)
	if w := rs.ensureWorker(tab, context.Background(), gen); w != nil {
		t.Fatal("worker started after clear")
	}

	var mu sync.Mutex
	var ctxs []context.Context
	listenTarget = func(ctx context.Context, _ func(any)) {
		mu.Lock()
		ctxs = append(ctxs, ctx)
		mu.Unlock()
	}
	for range 100 {
		rs := newRouteState()
		rs.rules[tab] = append([]NetworkRouteRule(nil), rule...)
		s := &Session{routeState: rs}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			armRoute(rs, context.Background(), tab)
		}()
		go func() {
			defer wg.Done()
			s.ClearRoutesForTarget(tab)
		}()
		wg.Wait()
		if rs.worker(tab) != nil {
			t.Fatal("worker present after clear")
		}
		mu.Lock()
		for _, ctx := range ctxs {
			if ctx.Err() == nil {
				mu.Unlock()
				t.Fatal("listener active after clear")
			}
		}
		ctxs = nil
		mu.Unlock()
	}
}
