package browser

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
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
