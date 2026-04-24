package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func TestV1NetworkSinceAndCursor(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page"},
	}}
	tid := target.ID("ABCDEF123456")
	srv.obsStore.PushNetwork(tid, srv.seq.Next(), json.RawMessage(`{"k":1}`))
	srv.obsStore.PushNetwork(tid, srv.seq.Next(), json.RawMessage(`{"k":2}`))

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
