package browser

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// The allocator blocks Run at initialization without starting Chrome. Each
// attempt exposes the real NewContext child so we can verify its cancellation.
type failedAttachAttempt struct {
	ctx     context.Context
	release chan struct{}
	once    sync.Once
}

func (a *failedAttachAttempt) finish() { a.once.Do(func() { close(a.release) }) }

type failedAttachAllocator struct {
	attempts chan *failedAttachAttempt
	calls    atomic.Int32
	err      error
}

func (a *failedAttachAllocator) Allocate(ctx context.Context, _ ...chromedp.BrowserOption) (*chromedp.Browser, error) {
	a.calls.Add(1)
	attempt := &failedAttachAttempt{ctx: ctx, release: make(chan struct{})}
	a.attempts <- attempt
	select {
	case <-attempt.release:
		return nil, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*failedAttachAllocator) Wait() {}

// Done is evaluated after a caller has obtained the shared entry and is about
// to wait on ready. This gives a deterministic barrier without timing sleeps.
type attachWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *attachWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func awaitAttachValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for attach test")
		var zero T
		return zero
	}
}

func TestFailedAttachIsNotSticky(t *testing.T) {
	for _, observer := range []bool{false, true} {
		name := "pool"
		if observer {
			name = "observer"
		}
		t.Run(name, func(t *testing.T) {
			const id target.ID = "TAB"
			attachErr := errors.New("attach failed")
			allocator := &failedAttachAllocator{attempts: make(chan *failedAttachAttempt, 16), err: attachErr}
			parent, cancel := chromedp.NewContext(context.Background(), func(c *chromedp.Context) { c.Allocator = allocator })
			t.Cleanup(cancel)
			s := &Session{ctx: parent}
			rec := &observationRecorder{}
			call := func(ctx context.Context) error {
				if observer {
					return s.EnsureObservation(ctx, id, ObservationNetwork, rec, nil)
				}
				_, err := s.tabChromeCtxContext(ctx, id)
				return err
			}
			results := make(chan error, 9)
			startCaller := func() {
				ctx := &attachWaitContext{Context: context.Background(), waiting: make(chan struct{})}
				go func() { results <- call(ctx) }()
				awaitAttachValue(t, ctx.waiting)
			}
			startCaller()
			first := awaitAttachValue(t, allocator.attempts)
			t.Cleanup(first.finish)
			for range 8 {
				startCaller()
			}
			if got := allocator.calls.Load(); got != 1 {
				t.Fatalf("concurrent callers initialized %d contexts, want 1", got)
			}
			first.finish()
			firstErr := awaitAttachValue(t, results)
			if !errors.Is(firstErr, attachErr) {
				t.Fatalf("error = %v, want attach failure", firstErr)
			}
			for range 8 {
				if err := awaitAttachValue(t, results); err != firstErr {
					t.Fatalf("waiter error = %v, want same error instance %v", err, firstErr)
				}
			}
			awaitAttachValue(t, first.ctx.Done())

			// Exercise the public lookup again: a new entry and child context
			// must be created, rather than returning the cached failure.
			startCaller()
			second := awaitAttachValue(t, allocator.attempts)
			t.Cleanup(second.finish)
			if second.ctx == first.ctx || allocator.calls.Load() != 2 {
				t.Fatal("retry did not create a fresh child context")
			}
			second.finish()
			if err := awaitAttachValue(t, results); !errors.Is(err, attachErr) || err == firstErr {
				t.Fatalf("retry error = %v, want a fresh attach failure", err)
			}
			awaitAttachValue(t, second.ctx.Done())
		})
	}
}

func TestFailedAttachReleasesAfterReadyOutsideLock(t *testing.T) {
	for _, observer := range []bool{false, true} {
		name := "pool"
		if observer {
			name = "observer"
		}
		t.Run(name, func(t *testing.T) {
			const id target.ID = "TAB"
			s := &Session{tabPool: make(map[target.ID]*tabPoolEntry), observers: make(map[target.ID]*tabObserver)}
			ready := make(chan struct{})
			cancelled := 0
			cancel := func() {
				cancelled++
				select {
				case <-ready:
				default:
					t.Error("cancel called before publishing the error to waiters")
				}
				mu := &s.poolMu
				if observer {
					mu = &s.obsMu
				}
				if !mu.TryLock() {
					t.Error("cleanup called while holding the map lock")
				} else {
					mu.Unlock()
				}
			}
			if observer {
				obs := &tabObserver{tid: id, ctx: context.Background(), cleanup: &attachCancellation{cancelChild: cancel}, ready: ready}
				s.observers[id] = obs
				s.initializeObserver(obs)
				if s.observers[id] != nil || obs.initErr == nil {
					t.Fatalf("observer = %v, error = %v", s.observers[id], obs.initErr)
				}
			} else {
				ent := &tabPoolEntry{ctx: context.Background(), cleanup: &attachCancellation{cancelChild: cancel}, ready: ready}
				s.tabPool[id] = ent
				s.initializeTabPoolEntry(id, ent)
				if s.tabPool[id] != nil || ent.err == nil {
					t.Fatalf("entry = %v, error = %v", s.tabPool[id], ent.err)
				}
			}
			if cancelled != 1 {
				t.Fatalf("cancel calls = %d, want 1", cancelled)
			}
		})
	}
}

func TestFailedAttachDoesNotReleaseReplacedEntry(t *testing.T) {
	for _, observer := range []bool{false, true} {
		name := "pool"
		if observer {
			name = "observer"
		}
		t.Run(name, func(t *testing.T) {
			const id target.ID = "TAB"
			allocator := &failedAttachAllocator{attempts: make(chan *failedAttachAttempt, 1), err: errors.New("attach failed")}
			ctx, stop := chromedp.NewContext(context.Background(), func(c *chromedp.Context) { c.Allocator = allocator })
			t.Cleanup(stop)
			attached := &chromedp.Target{TargetID: id, SessionID: "SESSION"}
			chromedp.FromContext(ctx).Target = attached
			s := &Session{tabPool: make(map[target.ID]*tabPoolEntry), observers: make(map[target.ID]*tabObserver)}
			cancelled := 0
			cancel := func() { cancelled++ }
			ent := &tabPoolEntry{ctx: ctx, cleanup: &attachCancellation{cancelChild: cancel}, ready: make(chan struct{})}
			obs := &tabObserver{tid: id, ctx: ctx, cleanup: &attachCancellation{cancelChild: cancel}, ready: make(chan struct{})}
			replacementEnt := &tabPoolEntry{}
			replacementObs := &tabObserver{}
			s.tabPool[id] = replacementEnt
			s.observers[id] = replacementObs
			done := make(chan struct{})
			go func() {
				defer close(done)
				if observer {
					s.initializeObserver(obs)
				} else {
					s.initializeTabPoolEntry(id, ent)
				}
			}()
			attempt := awaitAttachValue(t, allocator.attempts)
			t.Cleanup(attempt.finish)
			attempt.finish()
			awaitAttachValue(t, done)
			if cancelled != 0 || chromedp.FromContext(ctx).Target != attached {
				t.Fatal("initializer touched cleanup owned by target removal")
			}
			if s.tabPool[id] != replacementEnt || s.observers[id] != replacementObs {
				t.Fatal("initializer deleted a replacement entry")
			}
			ready, err := ent.ready, ent.err
			if observer {
				ready, err = obs.ready, obs.initErr
			}
			awaitAttachValue(t, ready)
			if err == nil {
				t.Fatal("waiters did not receive an error")
			}
		})
	}
}

type failedAttachExecutor struct {
	t             *testing.T
	ctx           context.Context
	calls         []string
	session       target.SessionID
	err           error
	detachStarted chan struct{}
	detachRelease chan struct{}
}

func (e *failedAttachExecutor) Execute(ctx context.Context, method string, params, _ any) error {
	e.calls = append(e.calls, method)
	if method != target.CommandDetachFromTarget {
		e.t.Errorf("cleanup sent %s, want only Target.detachFromTarget", method)
		return nil
	}
	if cdp.ExecutorFromContext(ctx) != e {
		e.t.Error("detach did not use the browser executor")
	}
	if ctx.Err() != nil || e.ctx.Err() != nil {
		e.t.Error("child canceled before detach completed")
	}
	if _, ok := ctx.Deadline(); !ok {
		e.t.Error("detach has no cleanup timeout")
	}
	if p, ok := params.(*target.DetachFromTargetParams); !ok || p.SessionID != e.session {
		e.t.Errorf("detach params = %#v, want session %q", params, e.session)
	}
	if chromedp.FromContext(e.ctx).Target == nil {
		e.t.Error("Target cleared before detach completed")
	}
	if e.detachStarted != nil {
		close(e.detachStarted)
		select {
		case <-e.detachRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return e.err
}

func TestReleaseFailedAttachOnlyDetaches(t *testing.T) {
	for _, detachErr := range []error{nil, errors.New("browser disconnected")} {
		name := "success"
		if detachErr != nil {
			name = "detach_error"
		}
		t.Run(name, func(t *testing.T) {
			parent, stop := chromedp.NewContext(context.Background())
			t.Cleanup(stop)
			// An inherited browser makes this a real child cancel watcher,
			// which would try to close the user's tab if Target stayed non-nil.
			chromedp.FromContext(parent).Browser = &chromedp.Browser{}
			ctx, cancel := chromedp.NewContext(parent)
			t.Cleanup(cancel)
			c := chromedp.FromContext(ctx)
			c.Target = &chromedp.Target{TargetID: "USER-TAB", SessionID: "SESSION"}
			ex := &failedAttachExecutor{t: t, ctx: ctx, session: "SESSION", err: detachErr}
			cancelled := 0
			releaseFailedAttach(ctx, &attachCancellation{cancelChild: func() {
				cancelled++
				if c.Target != nil {
					// Record the CloseTarget that chromedp's watcher would send,
					// then clear Target so a mutation fails without a nil-browser panic.
					_ = target.CloseTarget(c.Target.TargetID).Do(cdp.WithExecutor(context.Background(), ex))
					c.Target = nil
				}
				cancel()
			}}, ex)
			if cancelled != 1 || ctx.Err() != context.Canceled || c.Target != nil {
				t.Fatalf("cancel calls = %d, context error = %v, Target = %v", cancelled, ctx.Err(), c.Target)
			}
			if len(ex.calls) != 1 || ex.calls[0] != target.CommandDetachFromTarget {
				t.Fatalf("commands = %v, want only Target.detachFromTarget", ex.calls)
			}
		})
	}
}

// An inert browser lets the real child cancel watcher read Target without
// launching Chrome. Its CDP commands expire at the watcher's one-second timeout.
func failedAttachChild(t *testing.T) (context.Context, *attachCancellation, context.CancelFunc) {
	t.Helper()
	parent, cancelParent := context.WithCancel(context.Background())
	t.Cleanup(cancelParent)
	browserCtx, stop := chromedp.NewContext(parent)
	t.Cleanup(stop)
	chromedp.FromContext(browserCtx).Browser = &chromedp.Browser{}
	ctx, cleanup := newAttachContext(browserCtx, "USER-TAB")
	t.Cleanup(cleanup.cancel)
	chromedp.FromContext(ctx).Target = &chromedp.Target{TargetID: "USER-TAB", SessionID: "SESSION"}
	return ctx, cleanup, cancelParent
}

func TestReleaseFailedAttachAlreadyCancelled(t *testing.T) {
	ctx, cleanup, cancelParent := failedAttachChild(t)
	c := chromedp.FromContext(ctx)
	attached := c.Target
	ex := &failedAttachExecutor{t: t, ctx: ctx, session: "SESSION"}

	// Simulate a session disconnect after the child's Target is established.
	cancelParent()
	awaitAttachValue(t, ctx.Done())
	cancelChild := cleanup.cancelChild
	cancelled := 0
	cleanup.cancelChild = func() {
		cancelled++
		cancelChild() // Wait for the real cancel watcher before inspecting Target.
	}
	releaseFailedAttach(ctx, cleanup, ex)
	if cancelled != 1 || ctx.Err() != context.Canceled {
		t.Fatalf("cancel calls = %d, context error = %v", cancelled, ctx.Err())
	}
	if c.Target != attached {
		t.Fatal("cleanup changed Target after cancellation")
	}
	if len(ex.calls) != 0 {
		t.Fatalf("commands = %v, want no explicit detach after cancellation", ex.calls)
	}
}

func TestReleaseFailedAttachCancelledDuringDetach(t *testing.T) {
	ctx, cleanup, cancelParent := failedAttachChild(t)
	c := chromedp.FromContext(ctx)
	attached := c.Target
	ex := &failedAttachExecutor{
		t: t, ctx: ctx, session: "SESSION",
		detachStarted: make(chan struct{}), detachRelease: make(chan struct{}),
	}
	var unblock sync.Once
	releaseDetach := func() { unblock.Do(func() { close(ex.detachRelease) }) }
	t.Cleanup(releaseDetach)
	done := make(chan struct{})
	cancelChild := cleanup.cancelChild
	cancelled := 0
	cleanup.cancelChild = func() {
		cancelled++
		cancelChild()
	}
	go func() {
		defer close(done)
		releaseFailedAttach(ctx, cleanup, ex)
	}()
	awaitAttachValue(t, ex.detachStarted)
	// Cancellation starts chromedp's watcher while explicit detach is blocked.
	cancelParent()
	awaitAttachValue(t, ctx.Done())
	releaseDetach()
	awaitAttachValue(t, done)
	if cancelled != 1 || ctx.Err() != context.Canceled {
		t.Fatalf("cancel calls = %d, context error = %v", cancelled, ctx.Err())
	}
	if c.Target != attached {
		t.Fatal("cleanup changed Target after cancellation during detach")
	}
	if len(ex.calls) != 1 || ex.calls[0] != target.CommandDetachFromTarget {
		t.Fatalf("commands = %v, want one explicit Target.detachFromTarget", ex.calls)
	}
}

// The second lookup occurs after release has checked cancelled, immediately
// before it clears Target. Inject a session disconnect into that exact window.
type targetClearContext struct {
	context.Context
	lookups     int
	beforeClear func()
}

func (c *targetClearContext) Value(key any) any {
	c.lookups++
	if c.lookups == 2 {
		c.beforeClear()
	}
	return c.Context.Value(key)
}

func TestReleaseFailedAttachCancelledBeforeTargetClear(t *testing.T) {
	ctx, cleanup, cancelParent := failedAttachChild(t)
	c := chromedp.FromContext(ctx)
	// Keep the real watcher but avoid CDP commands against the inert browser.
	c.Target = &chromedp.Target{}
	wrapped := &targetClearContext{Context: ctx, beforeClear: func() {
		cancelParent()
		if ctx.Err() != nil {
			t.Error("session cancellation reached the child before Target was cleared")
		}
		if cleanup.mu.TryLock() {
			cleanup.mu.Unlock()
			t.Error("Target clear does not hold the cancellation mutex")
		}
	}}
	releaseFailedAttach(wrapped, cleanup, nil)
	if wrapped.lookups != 2 {
		t.Fatalf("context lookups = %d, want cancellation injected before Target clear", wrapped.lookups)
	}
	if c.Target != nil || ctx.Err() != context.Canceled {
		t.Fatalf("Target = %v, context error = %v", c.Target, ctx.Err())
	}
}

func TestReleaseFailedAttachConcurrentSessionCancellation(t *testing.T) {
	for range 256 {
		ctx, cleanup, cancelParent := failedAttachChild(t)
		c := chromedp.FromContext(ctx)
		attached := &chromedp.Target{}
		c.Target = attached
		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-start
			cancelParent()
		}()
		close(start)
		releaseFailedAttach(ctx, cleanup, nil)
		awaitAttachValue(t, done)
		if ctx.Err() != context.Canceled || (c.Target != nil && c.Target != attached) {
			t.Fatalf("context error = %v, Target = %v", ctx.Err(), c.Target)
		}
	}
}

func TestAttachCancellationStopsParentCallback(t *testing.T) {
	for _, action := range []string{"cancel", "release", "forget_pool", "forget_observer", "session_close"} {
		t.Run(action, func(t *testing.T) {
			parent, cancelParent := chromedp.NewContext(context.Background())
			t.Cleanup(cancelParent)
			chromedp.FromContext(parent).Browser = &chromedp.Browser{}
			ctx, cleanup := newAttachContext(parent, "TAB")
			t.Cleanup(cleanup.cancel)
			s := &Session{
				tabPool:   map[target.ID]*tabPoolEntry{"TAB": {ctx: ctx, cleanup: cleanup}},
				observers: map[target.ID]*tabObserver{"TAB": {ctx: ctx, cleanup: cleanup}},
			}
			switch action {
			case "cancel":
				cleanup.cancel()
			case "release":
				releaseFailedAttach(ctx, cleanup, nil)
			case "forget_pool":
				s.observers = nil
				s.ForgetTarget("TAB")
			case "forget_observer":
				s.tabPool = nil
				s.ForgetTarget("TAB")
			case "session_close":
				s.Close()
				if ctx.Err() != nil {
					t.Error("session close cancelled a still-open tab")
				}
			}
			// The live parent has not fired the callback, so false proves that
			// the lifecycle operation already unregistered it.
			if cleanup.stopParent() {
				t.Error("parent cancellation callback was still registered")
			}
		})
	}
}

func TestDisableObservationDomainsRetries(t *testing.T) {
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
	obs := &tabObserver{tid: "TAB", ctx: context.Background()}
	if err := obs.disableUntilLeased(context.Background()); err == nil || obs.domainsDisabled {
		t.Fatalf("err=%v disabled=%v", err, obs.domainsDisabled)
	}
	if err := obs.disableUntilLeased(context.Background()); err != nil || !obs.domainsDisabled || calls != 2 {
		t.Fatalf("err=%v disabled=%v calls=%d", err, obs.domainsDisabled, calls)
	}
}
