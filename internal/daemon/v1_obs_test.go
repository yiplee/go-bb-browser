package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// obsStatsConn reaches the optional ObservationStats path without a real session.
type obsStatsConn struct {
	fakeConn
	stats browser.ObservationStats
}

func (c *obsStatsConn) ObservationStats(target.ID, browser.ObservationKind) browser.ObservationStats {
	return c.stats
}

func TestV1NetworkSinceAndCursor(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: stateDirDisabled}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.tabHook = &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page"},
	}}
	tid := target.ID("ABCDEF123456")
	seq1, err := srv.store.NextSeq()
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := srv.store.NextSeq()
	if err != nil {
		t.Fatal(err)
	}
	srv.obsStore.PushNetwork(tid, seq1, json.RawMessage(`{"k":1}`))
	srv.obsStore.PushNetwork(tid, seq2, json.RawMessage(`{"k":2}`))

	body := bytes.NewBufferString(rpcReq(protocol.MethodNetwork, map[string]any{
		"tab":   "3456",
		"since": 0,
	}, 1))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result protocol.ObsQueryResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Result.Events) != 2 {
		t.Fatalf("events %#v", env.Result.Events)
	}
	if env.Result.Cursor < env.Result.Events[1].Seq {
		t.Fatalf("cursor %d vs last seq %d", env.Result.Cursor, env.Result.Events[1].Seq)
	}

	body2 := bytes.NewBufferString(rpcReq(protocol.MethodNetwork, map[string]any{
		"tab":   "3456",
		"since": env.Result.Events[0].Seq,
	}, 2))
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1", body2))
	if rec2.Code != http.StatusOK {
		t.Fatal(rec2.Body.String())
	}
	var env2 struct {
		Result protocol.ObsQueryResult `json:"result"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &env2); err != nil {
		t.Fatal(err)
	}
	if len(env2.Result.Events) != 1 {
		t.Fatalf("filtered len %d", len(env2.Result.Events))
	}
}

// A pre-enqueue lastSeen must not become the query cursor. Clients that
// continue with since=cursor would otherwise skip events still in the queue.
func TestObsQueryIgnoresPreEnqueueCursor(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: stateDirDisabled}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	tid := target.ID("ABCDEF123456")
	srv.tabHook = &obsStatsConn{
		fakeConn: fakeConn{infos: []*target.Info{{TargetID: tid, Type: "page"}}},
		stats:    browser.ObservationStats{Cursor: 5000, Dropped: 4},
	}
	srv.obsStore.PushNetwork(tid, 100, json.RawMessage(`{"k":1}`))

	body := bytes.NewBufferString(rpcReq(protocol.MethodNetwork, map[string]any{
		"tab":   "3456",
		"since": 0,
	}, 1))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result protocol.ObsQueryResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.Cursor != 100 {
		t.Fatalf("cursor = %d, want 100", env.Result.Cursor)
	}
	if env.Result.Dropped != 4 {
		t.Fatalf("dropped = %d, want 4", env.Result.Dropped)
	}
	if len(env.Result.Events) != 1 || env.Result.Events[0].Seq != 100 {
		t.Fatalf("events %#v", env.Result.Events)
	}

	srv.obsStore.PushNetwork(tid, 200, json.RawMessage(`{"k":2}`))
	body2 := bytes.NewBufferString(rpcReq(protocol.MethodNetwork, map[string]any{
		"tab":   "3456",
		"since": env.Result.Cursor,
	}, 2))
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1", body2))
	if rec2.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec2.Code, rec2.Body.String())
	}
	var env2 struct {
		Result protocol.ObsQueryResult `json:"result"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &env2); err != nil {
		t.Fatal(err)
	}
	if len(env2.Result.Events) != 1 || env2.Result.Events[0].Seq != 200 {
		t.Fatalf("since=%d events %#v, want seq 200", env.Result.Cursor, env2.Result.Events)
	}
}
