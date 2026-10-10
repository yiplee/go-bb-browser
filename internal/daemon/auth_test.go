package daemon

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func TestLoadAPITokens(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(file, []byte("\ufeff\n # comment\r\n file-token \r\nenv-token\nflag-token\n\t\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tokens, err := LoadAPITokens([]string{" flag-token ", "", "flag-token", "second-flag"}, " env-token, ,flag-token,,env-token ", file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"flag-token", "second-flag", "env-token", "file-token"}; !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte(" # comment\n\n\t\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tokens, err = LoadAPITokens([]string{" ", ""}, " , , ", empty)
	if err != nil || len(tokens) != 0 {
		t.Fatalf("empty whitelist: %v, %v", tokens, err)
	}
	_, err = LoadAPITokens(nil, "", filepath.Join(t.TempDir(), "secret-path"))
	if err == nil || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("file error leaked path or was missing: %v", err)
	}
}

func newAuthTestServer(t *testing.T, tokens []string, listen string) (*Server, *fakeConn, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: listen, StateDir: t.TempDir(), APITokens: tokens}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeConn{}
	srv.tabHook = fc
	t.Cleanup(func() {
		close(srv.auditCh)
		<-srv.auditDone
		if err := srv.store.Close(); err != nil {
			t.Error(err)
		}
	})
	return srv, fc, &logs
}

type unreadableAuthBody struct{}

func (unreadableAuthBody) Read([]byte) (int, error) { panic("unauthorized body was read") }
func (unreadableAuthBody) Close() error             { return nil }

func TestV1UnauthorizedDoesNotDispatchOrAudit(t *testing.T) {
	srv, fc, logs := newAuthTestServer(t, []string{"first-secret", "second-secret"}, "127.0.0.1:0")
	logPath := filepath.Join(srv.cfg.StateDir, "rpc.jsonl")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, headers := range map[string][]string{
		"missing":           nil,
		"wrong":             {"Bearer rejected-secret"},
		"basic":             {"Basic first-secret"},
		"no scheme":         {"first-secret"},
		"empty":             {"Bearer "},
		"double space":      {"Bearer  first-secret"},
		"tab":               {"Bearer\tfirst-secret"},
		"trailing space":    {"Bearer first-secret "},
		"extra field":       {"Bearer first-secret extra"},
		"comma":             {"Bearer first-secret,second-secret"},
		"duplicate headers": {"Bearer first-secret", "Bearer second-secret"},
		"proper prefix":     {"Bearer first-secre"},
		"proper extension":  {"Bearer first-secret-extra"},
		"second prefix":     {"Bearer second-secre"},
		"second extension":  {"Bearer second-secret-extra"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, body := range []string{rpcReq(protocol.MethodTabNew, map[string]any{}, 1), "invalid JSON"} {
				req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(body))
				req.Header["Authorization"] = headers
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized || rec.Body.String() != "unauthorized\n" || rec.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Fatalf("response: %d %s", rec.Code, rec.Body.String())
				}
			}
		})
	}
	req := httptest.NewRequest(http.MethodPost, "/v1", nil)
	req.Body = unreadableAuthBody{}
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	srv.auditWG.Wait()
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || fc.createSeq != 0 || fc.pageCalls.Load() != 0 || fc.pingCalls.Load() != 0 {
		t.Fatal("unauthorized request dispatched or changed the RPC log")
	}
	for _, secret := range []string{"first-secret", "second-secret", "rejected-secret"} {
		if strings.Contains(logs.String(), secret) || bytes.Contains(after, []byte(secret)) {
			t.Fatal("token appeared in logs")
		}
	}
}

func TestV1WhitelistAndDisabledAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tokens  []string
		headers []string
	}{
		{"whitelist", []string{"first-secret", "second-secret"}, []string{"Bearer first-secret", "Bearer second-secret", "bearer first-secret"}},
		{"disabled", nil, []string{"", "Basic anything", "Bearer anything"}},
		{"empty whitelist", []string{"", " "}, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, fc, logs := newAuthTestServer(t, tc.tokens, "127.0.0.1:0")
			for _, header := range tc.headers {
				req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(rpcReq(protocol.MethodTabNew, map[string]any{}, 1)))
				if header != "" {
					req.Header.Set("Authorization", header)
				}
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"error"`) {
					t.Fatalf("request failed: %d %s", rec.Code, rec.Body.String())
				}
			}
			srv.auditWG.Wait()
			data, err := os.ReadFile(filepath.Join(srv.cfg.StateDir, "rpc.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if fc.createSeq != len(tc.headers) || !bytes.Contains(data, []byte("tab_new")) {
				t.Fatal("authorized requests did not execute and audit")
			}
			for _, token := range tc.tokens {
				if token = strings.TrimSpace(token); token != "" && (bytes.Contains(data, []byte(token)) || strings.Contains(logs.String(), token)) {
					t.Fatal("authorized token appeared in logs")
				}
			}
		})
	}
}

func TestAuthHealthEndpointsExempt(t *testing.T) {
	srv, _, _ := newAuthTestServer(t, []string{"secret"}, "127.0.0.1:0")
	for _, disconnected := range []bool{false, true} {
		if disconnected {
			srv.tabHook = nil
		}
		for _, path := range []string{"/live", "/ready", "/health"} {
			for _, header := range []string{"", "Bearer wrong"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Authorization", header)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				want := http.StatusOK
				if disconnected && path != "/live" {
					want = http.StatusServiceUnavailable
				}
				if rec.Code != want {
					t.Fatalf("%s: status %d, want %d", path, rec.Code, want)
				}
			}
		}
	}
}

func TestUnauthenticatedNonLoopbackWarning(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0", "0.0.0.0:0", ":0", "[::]:0", "192.0.2.1:0"} {
		for _, enabled := range []bool{false, true} {
			var tokens []string
			if enabled {
				tokens = []string{"warning-secret"}
			}
			_, _, logs := newAuthTestServer(t, tokens, addr)
			wantWarn := !enabled && addr != "127.0.0.1:0" && addr != "[::1]:0" && addr != "localhost:0"
			if strings.Contains(logs.String(), "level=WARN") != wantWarn || strings.Contains(logs.String(), "warning-secret") {
				t.Fatalf("%s enabled=%v: unexpected startup log %s", addr, enabled, logs.String())
			}
		}
	}
}
