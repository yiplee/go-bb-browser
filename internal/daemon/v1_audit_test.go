package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func TestV1AuditListAfterTabList(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: "-"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.tabHook = &fakeConn{infos: []*target.Info{}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(rpcReq(protocol.MethodTabList, map[string]any{}, 1)))
	req.RemoteAddr = "203.0.113.9:12345"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tab_list status %d %s", rec.Code, rec.Body.String())
	}

	time.Sleep(20 * time.Millisecond)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(rpcReq(protocol.MethodAuditList, map[string]any{}, 2)))
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("audit_list status %d %s", rec2.Code, rec2.Body.String())
	}
	var env struct {
		Result protocol.AuditListResult `json:"result"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Result.Records) != 1 {
		t.Fatalf("records: %#v", env.Result.Records)
	}
	if env.Result.Records[0].Action != protocol.MethodTabList {
		t.Fatalf("action %q", env.Result.Records[0].Action)
	}
	if env.Result.Records[0].SenderIP != "203.0.113.9" {
		t.Fatalf("sender_ip %q", env.Result.Records[0].SenderIP)
	}
	if !env.Result.Records[0].OK {
		t.Fatalf("expected ok audit record")
	}
	if env.Result.Records[0].Seq == 0 {
		t.Fatal("expected seq in audit record")
	}

	time.Sleep(20 * time.Millisecond)
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(rpcReq(protocol.MethodAuditList, map[string]any{
		"since": env.Result.Cursor,
	}, 3)))
	srv.Handler().ServeHTTP(rec3, req3)
	var env3 struct {
		Result protocol.AuditListResult `json:"result"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &env3); err != nil {
		t.Fatal(err)
	}
	if len(env3.Result.Records) != 0 {
		t.Fatalf("expected no records after cursor, got %d", len(env3.Result.Records))
	}
	for _, rec := range env.Result.Records {
		if rec.Action == protocol.MethodAuditList {
			t.Fatal("audit_list should not appear in audit log")
		}
	}
}

func TestAuditListWithoutBrowserSession(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: "-"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SkipBrowserAttach = true

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(rpcReq(protocol.MethodAuditList, map[string]any{}, 1)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit_list without browser: status %d %s", rec.Code, rec.Body.String())
	}
}
