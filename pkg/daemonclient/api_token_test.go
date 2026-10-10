package daemonclient

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func TestWithAPIToken(t *testing.T) {
	srv, st := newFakeDaemon(t, "a")
	st.require = http.Header{"Authorization": {"Bearer client-secret"}}
	ctx := context.Background()
	c := NewClient(srv.URL, WithAPIToken("client-secret"))
	if _, err := c.TabNew(ctx, protocol.TabNewParams{}); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []func(context.Context) error{c.Live, c.Ready, c.Health} {
		if err := probe(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, opts := range [][]ClientOption{nil, {WithAPIToken("wrong")}} {
		_, err := NewClient(srv.URL, opts...).TabNew(ctx, protocol.TabNewParams{})
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("want HTTP 401, got %v", err)
		}
	}
}

func TestPoolBackendAPITokens(t *testing.T) {
	a, stA := newFakeDaemon(t, "a")
	b, stB := newFakeDaemon(t, "b")
	stA.require = http.Header{"Authorization": {"Bearer a-secret"}}
	stB.require = http.Header{"Authorization": {"Bearer b-secret"}}
	stA.listTab = "a1"
	stB.listTab = "b1"
	pool, err := NewPool(NewClient(a.URL, WithAPIToken("a-secret")), NewClient(b.URL, WithAPIToken("b-secret")))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		tab, err := pool.TabNew(ctx, protocol.TabNewParams{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Eval(ctx, protocol.EvalParams{Tab: tab.Tab, Script: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	tabs, err := pool.TabList(ctx, protocol.TabListParams{})
	if err != nil || len(tabs.Tabs) != 2 {
		t.Fatalf("TabList = %+v, %v", tabs, err)
	}
	stA.mu.Lock()
	nA := stA.tabNew
	stA.mu.Unlock()
	stB.mu.Lock()
	nB := stB.tabNew
	stB.mu.Unlock()
	if nA != 1 || nB != 1 {
		t.Fatalf("tabs opened on backends: %d, %d", nA, nB)
	}
}

func TestWithAPITokenTrimsAndIgnoresEmpty(t *testing.T) {
	c := NewClient("http://127.0.0.1:0", WithAPIToken("  secret \n"))
	if got := c.Headers.Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("Authorization = %q", got)
	}
	for _, token := range []string{"", " \t "} {
		c := NewClient("http://127.0.0.1:0", WithAPIToken(token))
		if got := c.Headers.Get("Authorization"); got != "" {
			t.Fatalf("empty token set Authorization = %q", got)
		}
	}
}
