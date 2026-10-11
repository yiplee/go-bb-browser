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
	return newAuthTestServerConfig(t, Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: listen, StateDir: t.TempDir(), APITokens: tokens})
}

func newAuthTestServerConfig(t *testing.T, cfg Config) (*Server, *fakeConn, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
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
	for _, allow := range []bool{false, true} {
		srv, _, _ := newAuthTestServerConfig(t, Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: t.TempDir(), APITokens: []string{"secret"}, APITokenAllowLoopback: allow})
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

func TestV1LoopbackExemption(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:1234", "127.0.0.2:1234", "127.255.255.255:1234", "[::1]:1234", "[::ffff:127.0.0.1]:1234"} {
		t.Run(remote, func(t *testing.T) {
			srv, fc, logs := newAuthTestServerConfig(t, Config{
				DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: t.TempDir(),
				APITokens: []string{"loopback-secret"}, APITokenAllowLoopback: true,
			})
			startupLogs := logs.String()
			for _, header := range []string{"", "Bearer rejected-secret", "Basic malformed", "Bearer loopback-secret"} {
				req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(rpcReq(protocol.MethodTabNew, map[string]any{}, 1)))
				req.RemoteAddr = remote
				req.Header.Set("Authorization", header)
				req.Header.Set("User-Agent", "local-client")
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"error"`) {
					t.Fatalf("loopback request failed: %d %s", rec.Code, rec.Body.String())
				}
			}
			srv.auditWG.Wait()
			data, err := os.ReadFile(filepath.Join(srv.cfg.StateDir, "rpc.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if fc.createSeq != 4 || !bytes.Contains(data, []byte("tab_new")) {
				t.Fatal("loopback requests did not dispatch and audit")
			}
			if logs.String() != startupLogs {
				t.Fatal("loopback exemption logged per request")
			}
			for _, secret := range []string{"loopback-secret", "rejected-secret"} {
				if strings.Contains(logs.String(), secret) || bytes.Contains(data, []byte(secret)) {
					t.Fatal("token appeared in logs")
				}
			}
		})
	}
}

func TestV1LoopbackExemptionRequiresToken(t *testing.T) {
	type testCase struct {
		name, remote, proxyHeader string
		allowLoopback             bool
	}
	cases := []testCase{
		{"default IPv4", "127.0.0.1:1234", "", false},
		{"default IPv6", "[::1]:1234", "", false},
		{"private", "10.0.0.1:1234", "", true},
		{"public", "8.8.8.8:1234", "", true},
		{"non-loopback IPv6", "[2001:db8::1]:1234", "", true},
		{"mapped private", "[::ffff:10.0.0.1]:1234", "", true},
		{"forged loopback", "8.8.8.8:1234", "X-Forwarded-For", true},
		{"hostname", "localhost:1234", "", true},
		{"missing port", "127.0.0.1", "", true},
		{"invalid address", "invalid", "", true},
		{"empty address", "", "", true},
	}
	// Independent of the implementation's list, so removing a header cannot weaken coverage.
	for _, header := range []string{
		"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP",
		"Forwarded", "CF-Connecting-IP", "CF-Ray", "True-Client-IP", "Via",
		"Tailscale-User-Login", "Tailscale-Funnel-Request", "Tailscale-Unknown", "Tailscale-",
	} {
		for _, variant := range []string{header, strings.ToLower(header), strings.ToUpper(header)} {
			cases = append(cases, testCase{variant, "127.0.0.1:1234", variant, true})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, fc, logs := newAuthTestServerConfig(t, Config{
				DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: t.TempDir(),
				APITokens: []string{"required-secret"}, APITokenAllowLoopback: tc.allowLoopback,
			})
			logPath := filepath.Join(srv.cfg.StateDir, "rpc.jsonl")
			before, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			startupLogs := logs.String()
			for _, values := range [][]string{{"127.0.0.1"}, {""}, nil} {
				for _, auth := range []string{"", "Bearer rejected-secret"} {
					req := httptest.NewRequest(http.MethodPost, "/v1", nil)
					req.RemoteAddr = tc.remote
					req.Host = "localhost" // Host must never establish loopback trust.
					req.Body = unreadableAuthBody{}
					req.Header.Set("Authorization", auth)
					if tc.proxyHeader != "" {
						// Assign directly to preserve case and test presence with empty values.
						req.Header[tc.proxyHeader] = values
					}
					rec := httptest.NewRecorder()
					srv.Handler().ServeHTTP(rec, req)
					if rec.Code != http.StatusUnauthorized || rec.Body.String() != "unauthorized\n" || rec.Header().Get("WWW-Authenticate") != "Bearer" || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
						t.Fatalf("response: %d %s", rec.Code, rec.Body.String())
					}
				}
			}
			srv.auditWG.Wait()
			after, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || fc.createSeq != 0 || fc.pageCalls.Load() != 0 || fc.pingCalls.Load() != 0 {
				t.Fatal("unauthorized request dispatched or changed the RPC log")
			}
			if logs.String() != startupLogs || strings.Contains(logs.String(), "required-secret") || strings.Contains(logs.String(), "rejected-secret") {
				t.Fatal("unexpected request logging or token in startup logs")
			}
			// Detection disables only the exemption: a valid token still permits RPC.
			req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(rpcReq(protocol.MethodTabNew, map[string]any{}, 1)))
			req.RemoteAddr = tc.remote
			req.Header.Set("Authorization", "Bearer required-secret")
			if tc.proxyHeader != "" {
				req.Header[tc.proxyHeader] = []string{"127.0.0.1"}
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"error"`) || fc.createSeq != 1 {
				t.Fatalf("valid token failed: %d %s", rec.Code, rec.Body.String())
			}
			srv.auditWG.Wait()
			after, err = os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(after, []byte("tab_new")) || bytes.Contains(after, []byte("required-secret")) || strings.Contains(logs.String(), "required-secret") {
				t.Fatal("valid token audit missing or token exposed")
			}
		})
	}
}

func TestLoopbackAuthenticationStartupLogsAndNoTokens(t *testing.T) {
	for _, allow := range []bool{false, true} {
		for _, tokens := range [][]string{nil, {"", " "}, {"startup-secret"}} {
			srv, fc, logs := newAuthTestServerConfig(t, Config{
				DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: t.TempDir(),
				APITokens: tokens, APITokenAllowLoopback: allow,
			})
			if len(srv.cfg.APITokens) > 0 {
				if strings.Contains(logs.String(), "level=WARN") != allow || strings.Contains(logs.String(), "startup-secret") {
					t.Fatalf("unexpected startup logs: %s", logs.String())
				}
				if allow && (strings.Count(logs.String(), "level=WARN") != 1 || !strings.Contains(logs.String(), "local proxy")) {
					t.Fatalf("missing single proxy warning: %s", logs.String())
				}
				continue
			}
			if strings.Contains(logs.String(), "has no effect") != allow || strings.Contains(logs.String(), "level=WARN") {
				t.Fatalf("unexpected no-token startup logs: %s", logs.String())
			}
			for _, remote := range []string{"127.0.0.1:1234", "10.0.0.1:1234"} {
				req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(rpcReq(protocol.MethodTabNew, map[string]any{}, 1)))
				req.RemoteAddr = remote
				req.Header.Set("Tailscale-User-Login", "test")
				req.Header.Set("Authorization", "Bearer rejected-secret")
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"error"`) {
					t.Fatalf("no-token request failed: %d %s", rec.Code, rec.Body.String())
				}
			}
			if fc.createSeq != 2 {
				t.Fatal("no-token requests did not dispatch")
			}
		}
	}
}
