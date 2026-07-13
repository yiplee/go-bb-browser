package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

type sequencePinger struct {
	mu   sync.Mutex
	errs []error
}

type dirtyMarker struct{ marked atomic.Bool }

func (m *dirtyMarker) MarkTargetDirty() { m.marked.Store(true) }

func TestInitialTargetEnumerationFailureSchedulesRetry(t *testing.T) {
	srv := newSupervisorTestServer(t, 3)
	marker := &dirtyMarker{}
	if snaps := srv.syncInitialTargets(marker, nil, errors.New("temporary target enumeration failure")); snaps != nil {
		t.Fatalf("snapshots = %#v, want nil", snaps)
	}
	if !marker.marked.Load() {
		t.Fatal("initial target enumeration failure did not schedule resync")
	}
}

func (p *sequencePinger) PingBrowserContext(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.errs) == 0 {
		return nil
	}
	err := p.errs[0]
	p.errs = p.errs[1:]
	return err
}

func newSupervisorTestServer(t *testing.T, failures int) *Server {
	t.Helper()
	cfg := Config{
		DebuggerURL:         "127.0.0.1:9222",
		ListenAddr:          "127.0.0.1:0",
		StateDir:            stateDirDisabled,
		CDPWatchdogInterval: time.Second,
		CDPWatchdogTimeout:  100 * time.Millisecond,
		CDPWatchdogFailures: failures,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.fatalCh = make(chan error, 1)
	srv.browserState = sessionReady
	return srv
}

func TestWatchdogConsecutiveFailuresBecomeFatal(t *testing.T) {
	srv := newSupervisorTestServer(t, 3)
	p := &sequencePinger{errs: []error{errors.New("one"), errors.New("two"), errors.New("three")}}
	for range 3 {
		srv.probeBrowserSession(context.Background(), p)
	}
	if srv.browserState != sessionFailed || srv.browserFailures != 3 {
		t.Fatalf("state=%v failures=%d", srv.browserState, srv.browserFailures)
	}
	select {
	case err := <-srv.fatalCh:
		if err == nil {
			t.Fatal("nil fatal error")
		}
	default:
		t.Fatal("expected fatal exit signal")
	}
}

func TestWatchdogRecoveryResetsFailures(t *testing.T) {
	srv := newSupervisorTestServer(t, 3)
	p := &sequencePinger{errs: []error{errors.New("one"), nil}}
	srv.probeBrowserSession(context.Background(), p)
	if srv.browserState != sessionSuspect || srv.browserFailures != 1 {
		t.Fatalf("after failure state=%v failures=%d", srv.browserState, srv.browserFailures)
	}
	srv.probeBrowserSession(context.Background(), p)
	if srv.browserState != sessionReady || srv.browserFailures != 0 {
		t.Fatalf("after recovery state=%v failures=%d", srv.browserState, srv.browserFailures)
	}
}

type blockingPinger struct {
	calls   atomic.Int64
	started chan struct{}
	release chan struct{}
}

func (p *blockingPinger) PingBrowserContext(ctx context.Context) error {
	if p.calls.Add(1) == 1 {
		close(p.started)
	}
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWatchdogCoalescesConcurrentProbes(t *testing.T) {
	srv := newSupervisorTestServer(t, 3)
	srv.cfg.CDPWatchdogTimeout = time.Second
	p := &blockingPinger{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		srv.probeBrowserSession(context.Background(), p)
		close(done)
	}()
	<-p.started
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.probeBrowserSession(context.Background(), p)
		}()
	}
	wg.Wait()
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want 1", got)
	}
	close(p.release)
	<-done
}

func TestTargetClosedIsNotGlobalFatal(t *testing.T) {
	srv := newSupervisorTestServer(t, 3)
	got := srv.cdpHint(errors.New("Target closed"))
	if got == "" {
		t.Fatal("empty hint")
	}
	if srv.browserState == sessionFailed {
		t.Fatal("target-local close marked browser failed")
	}
	select {
	case err := <-srv.fatalCh:
		t.Fatalf("unexpected fatal signal: %v", err)
	default:
	}
}

func TestFailedSupervisorRejectsBrowserRPCWithoutCDP(t *testing.T) {
	srv := newSupervisorTestServer(t, 3)
	fc := &fakeConn{infos: []*target.Info{{TargetID: "A", Type: "page"}}}
	srv.tabLive = fc
	srv.browserState = sessionFailed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(rpcReq(protocol.MethodTabList, map[string]any{}, 1)))
	srv.Handler().ServeHTTP(rec, req)
	var env struct {
		Error *protocol.ResponseError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != protocol.CodeServerError {
		t.Fatalf("response = %s", rec.Body.String())
	}
	if got := fc.pageCalls.Load(); got != 0 {
		t.Fatalf("failed session still sent %d CDP calls", got)
	}
}
