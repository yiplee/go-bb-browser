package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

type fakeDaemon struct {
	tabPrefix  string
	failRPC    atomic.Bool
	failHealth atomic.Bool
	require    http.Header

	mu       sync.Mutex
	seq      uint64
	tabNew   int
	evalTabs []string
	methods  []string
	healthN  int
}

func newFakeDaemon(t *testing.T, tabPrefix string) (*httptest.Server, *fakeDaemon) {
	t.Helper()
	st := &fakeDaemon{tabPrefix: tabPrefix}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, vv := range st.require {
			if r.Header.Get(k) != vv[len(vv)-1] {
				http.Error(w, "missing header "+k, http.StatusUnauthorized)
				return
			}
		}
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/health" || r.URL.Path == "/ready"):
			st.mu.Lock()
			st.healthN++
			fail := st.failHealth.Load()
			st.mu.Unlock()
			if fail {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"error","browser":"disconnected"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","browser":"connected"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/live":
			if st.failHealth.Load() {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1":
			if st.failRPC.Load() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"error"}`))
				return
			}
			b, _ := io.ReadAll(r.Body)
			var req struct {
				Method string          `json:"method"`
				ID     json.RawMessage `json:"id"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(b, &req)
			st.mu.Lock()
			st.methods = append(st.methods, req.Method)
			st.seq++
			seq := st.seq
			tab := ""
			var peek struct {
				Tab string `json:"tab"`
			}
			_ = json.Unmarshal(req.Params, &peek)
			if req.Method == protocol.MethodTabNew {
				st.tabNew++
				tab = st.tabPrefix + strconv.Itoa(st.tabNew)
			} else {
				tab = peek.Tab
				if req.Method == protocol.MethodEval {
					st.evalTabs = append(st.evalTabs, tab)
				}
			}
			st.mu.Unlock()

			result := map[string]any{"seq": seq, "tab": tab}
			if req.Method == protocol.MethodTabList {
				result["tabs"] = []any{}
				result["focus"] = tab
			}
			if req.Method == protocol.MethodEval {
				result["result"] = json.RawMessage("1")
			}
			raw, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  result,
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

func TestNewPool_rejectsEmpty(t *testing.T) {
	_, err := NewPool()
	if !errors.Is(err, ErrEmptyPool) {
		t.Fatalf("got %v", err)
	}
	_, err = NewPool(nil)
	if err == nil {
		t.Fatal("expected nil client error")
	}
	_, err = NewPool(&Client{})
	if err == nil {
		t.Fatal("expected empty URL error")
	}
}

func TestNewPool_doesNotProbeAtStartup(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	sa.failHealth.Store(true)
	sb.failHealth.Store(true)
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	if p.Len() != 2 {
		t.Fatalf("len %d", p.Len())
	}
	sa.mu.Lock()
	ha := sa.healthN
	sa.mu.Unlock()
	sb.mu.Lock()
	hb := sb.healthN
	sb.mu.Unlock()
	if ha != 0 || hb != 0 {
		t.Fatalf("startup probed health: a=%d b=%d", ha, hb)
	}
}

func TestPool_tabNewEvenDistribution(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	c, sc := newFakeDaemon(t, "c")
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL), NewClient(c.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const n = 30
	for range n {
		if _, err := p.TabNew(ctx, protocol.TabNewParams{URL: "about:blank"}); err != nil {
			t.Fatal(err)
		}
	}
	got := []int{sa.tabNewCount(), sb.tabNewCount(), sc.tabNewCount()}
	for i, v := range got {
		if v != n/3 {
			t.Fatalf("backend %d tab_new=%d want %d (all=%v)", i, v, n/3, got)
		}
	}
}

func TestPool_tabAffinity(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := p.TabNew(ctx, protocol.TabNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.TabNew(ctx, protocol.TabNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Tab == second.Tab {
		t.Fatalf("expected distinct tabs, got %q", first.Tab)
	}
	c1, ok := p.ClientForTab(first.Tab)
	if !ok {
		t.Fatal("missing affinity for first tab")
	}
	c2, ok := p.ClientForTab(second.Tab)
	if !ok {
		t.Fatal("missing affinity for second tab")
	}
	if c1.BaseURL == c2.BaseURL {
		t.Fatalf("expected different backends, both %s", c1.BaseURL)
	}

	if _, err := p.Eval(ctx, protocol.EvalParams{Tab: first.Tab, Script: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval(ctx, protocol.EvalParams{Tab: second.Tab, Script: "1"}); err != nil {
		t.Fatal(err)
	}

	if tabs := sa.evalTabIDs(); len(tabs) > 0 && tabs[0] != first.Tab && tabs[0] != second.Tab {
		t.Fatalf("unexpected eval on a: %v", tabs)
	}
	// Each eval must land only on the daemon that created that tab.
	if sa.hasEval(first.Tab) == sa.hasEval(second.Tab) && sa.hasEval(first.Tab) {
		t.Fatalf("both tabs evaluated on daemon a")
	}
	aFirst := sa.hasEval(first.Tab)
	bFirst := sb.hasEval(first.Tab)
	aSecond := sa.hasEval(second.Tab)
	bSecond := sb.hasEval(second.Tab)
	if aFirst == bFirst || aSecond == bSecond {
		t.Fatalf("tab not pinned to a single daemon: first a=%v b=%v second a=%v b=%v", aFirst, bFirst, aSecond, bSecond)
	}
	if aFirst == aSecond {
		t.Fatalf("both evals on the same daemon")
	}
}

func TestPool_unknownTabNoFallback(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval(context.Background(), protocol.EvalParams{Tab: "nope", Script: "1"})
	var ut *UnknownTabError
	if !errors.As(err, &ut) || ut.Tab != "nope" {
		t.Fatalf("want UnknownTabError, got %v", err)
	}
	if sa.methodCount(protocol.MethodEval) != 0 || sb.methodCount(protocol.MethodEval) != 0 {
		t.Fatal("unknown tab must not hit any daemon")
	}
}

func TestPool_tabScopedRequiresTab(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	p, err := NewPool(NewClient(a.URL))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Call(context.Background(), protocol.MethodEval, protocol.EvalParams{Script: "1"}, new(protocol.EvalResult))
	if !errors.Is(err, ErrTabRequired) {
		t.Fatalf("got %v", err)
	}
	if sa.methodCount(protocol.MethodEval) != 0 {
		t.Fatal("empty tab must not be sent")
	}
}

func TestPool_skipFailedBackendOnTabNew(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	sa.failRPC.Store(true)
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 8 {
		out, err := p.TabNew(ctx, protocol.TabNewParams{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := p.ClientForTab(out.Tab); !ok {
			t.Fatalf("unpinned tab %q", out.Tab)
		}
		if _, err := p.Eval(ctx, protocol.EvalParams{Tab: out.Tab, Script: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	if sa.tabNewCount() != 0 {
		t.Fatalf("failed daemon got tab_new %d", sa.tabNewCount())
	}
	if sb.tabNewCount() != 8 {
		t.Fatalf("healthy daemon tab_new=%d", sb.tabNewCount())
	}
	if sa.hasAnyEval() {
		t.Fatal("failed daemon received eval")
	}
	if sb.methodCount(protocol.MethodEval) != 8 {
		t.Fatalf("eval count %d", sb.methodCount(protocol.MethodEval))
	}
}

func TestPool_boundCallDoesNotFailover(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := p.TabNew(ctx, protocol.TabNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := p.ClientForTab(out.Tab)
	if !ok {
		t.Fatal("missing owner")
	}
	// Fail the owner after the tab exists.
	if owner.BaseURL == a.URL {
		sa.failRPC.Store(true)
	} else {
		sb.failRPC.Store(true)
	}
	_, err = p.Eval(ctx, protocol.EvalParams{Tab: out.Tab, Script: "1"})
	if err == nil {
		t.Fatal("expected owner failure")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("want HTTPError from owner, got %v", err)
	}
	healthyEval := sb.methodCount(protocol.MethodEval)
	if owner.BaseURL == a.URL {
		healthyEval = sb.methodCount(protocol.MethodEval)
	} else {
		healthyEval = sa.methodCount(protocol.MethodEval)
	}
	if healthyEval != 0 {
		t.Fatal("eval must not fail over to the other daemon")
	}
}

func TestPool_healthSkipsUnhealthy(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, _ := newFakeDaemon(t, "b")
	sa.failHealth.Store(true)
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Live(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPool_allFailed(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	sa.failRPC.Store(true)
	sb.failRPC.Store(true)
	sa.failHealth.Store(true)
	sb.failHealth.Store(true)
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.TabNew(context.Background(), protocol.TabNewParams{})
	var af *AllFailedError
	if !errors.As(err, &af) || af.Op != protocol.MethodTabNew {
		t.Fatalf("want AllFailedError tab_new, got %v", err)
	}
	if len(af.Unwrap()) != 2 {
		t.Fatalf("errs %v", af.Unwrap())
	}
	err = p.Health(context.Background())
	if !errors.As(err, &af) {
		t.Fatalf("health: %v", err)
	}
}

func TestPool_perDaemonHeaders(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	sa.require = http.Header{"CF-Access-Client-Id": []string{"id-a"}}
	sb.require = http.Header{"CF-Access-Client-Id": []string{"id-b"}}
	p, err := NewPool(
		NewClient(a.URL, WithHeader("CF-Access-Client-Id", "id-a")),
		NewClient(b.URL, WithHeader("CF-Access-Client-Id", "id-b")),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 4 {
		out, err := p.TabNew(ctx, protocol.TabNewParams{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Eval(ctx, protocol.EvalParams{Tab: out.Tab, Script: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	if sa.tabNewCount()+sb.tabNewCount() != 4 {
		t.Fatalf("tab_new a=%d b=%d", sa.tabNewCount(), sb.tabNewCount())
	}
}

func TestPool_tabCloseUnbinds(t *testing.T) {
	a, _ := newFakeDaemon(t, "a")
	p, err := NewPool(NewClient(a.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := p.TabNew(ctx, protocol.TabNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.TabClose(ctx, protocol.TabCloseParams{Tab: out.Tab}); err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval(ctx, protocol.EvalParams{Tab: out.Tab, Script: "1"})
	var ut *UnknownTabError
	if !errors.As(err, &ut) {
		t.Fatalf("want unbound after close, got %v", err)
	}
}

func TestPool_concurrentTabNewAndEval(t *testing.T) {
	a, sa := newFakeDaemon(t, "a")
	b, sb := newFakeDaemon(t, "b")
	p, err := NewPool(NewClient(a.URL), NewClient(b.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const n = 40
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			out, err := p.TabNew(ctx, protocol.TabNewParams{})
			if err != nil {
				errCh <- err
				return
			}
			if _, err := p.Eval(ctx, protocol.EvalParams{Tab: out.Tab, Script: "1"}); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	total := sa.tabNewCount() + sb.tabNewCount()
	if total != n {
		t.Fatalf("tab_new total %d", total)
	}
	if sa.tabNewCount() == 0 || sb.tabNewCount() == 0 {
		t.Fatalf("expected both backends to receive work, a=%d b=%d", sa.tabNewCount(), sb.tabNewCount())
	}
	if sa.methodCount(protocol.MethodEval)+sb.methodCount(protocol.MethodEval) != n {
		t.Fatalf("eval total a=%d b=%d", sa.methodCount(protocol.MethodEval), sb.methodCount(protocol.MethodEval))
	}
}

func (st *fakeDaemon) tabNewCount() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.tabNew
}

func (st *fakeDaemon) methodCount(m string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, got := range st.methods {
		if got == m {
			n++
		}
	}
	return n
}

func (st *fakeDaemon) evalTabIDs() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := append([]string(nil), st.evalTabs...)
	return out
}

func (st *fakeDaemon) hasEval(tab string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, t := range st.evalTabs {
		if t == tab {
			return true
		}
	}
	return false
}

func (st *fakeDaemon) hasAnyEval() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.evalTabs) > 0
}
