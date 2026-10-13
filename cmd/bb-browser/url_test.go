package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

var validURLs = []struct {
	input string
	want  string
}{
	{"google.com", "https://google.com"},
	{" google.com", "https://google.com"},
	{"google.com ", "https://google.com"},
	{" google.com ", "https://google.com"},
	{"\tgoogle.com\t", "https://google.com"},
	{"\ngoogle.com\n", "https://google.com"},
	{" \t\r\ngoogle.com\r\n\t ", "https://google.com"},
	{" \tHTTPS://Example.com/a%20b?q=1#section\n ", "HTTPS://Example.com/a%20b?q=1#section"},
	{"example.com/path?q=1", "https://example.com/path?q=1"},
	{"example.com:8443/path?q=1#section", "https://example.com:8443/path?q=1#section"},
	{"localhost", "https://localhost"},
	{"localhost:8080", "https://localhost:8080"},
	{"localhost:8080/path?q=1", "https://localhost:8080/path?q=1"},
	{"intranet:8080", "https://intranet:8080"},
	{"127.0.0.1", "https://127.0.0.1"},
	{"127.0.0.1:9222", "https://127.0.0.1:9222"},
	{"127.0.0.1:9222/path?q=1", "https://127.0.0.1:9222/path?q=1"},
	{"[::1]", "https://[::1]"},
	{"[::1]:8080", "https://[::1]:8080"},
	{"[::1]:8080/path?q=1", "https://[::1]:8080/path?q=1"},
	{"[2001:db8::1]:443", "https://[2001:db8::1]:443"},
	{"user@example.com", "https://user@example.com"},
	{"user@example.com:8080", "https://user@example.com:8080"},
	{"user:pass@example.com", "https://user:pass@example.com"},
	{"user:p:a:ss@example.com", "https://user:p:a:ss@example.com"},
	{"user:p%40ss@example.com", "https://user:p%40ss@example.com"},
	{"user:pass@sub-domain.example.com.", "https://user:pass@sub-domain.example.com."},
	{"user:pass@example.com:8080", "https://user:pass@example.com:8080"},
	{"user:pass@example.com:8080/path?q=1#section", "https://user:pass@example.com:8080/path?q=1#section"},
	{"user:pass@localhost", "https://user:pass@localhost"},
	{"user:pass@LOCALHOST:8080", "https://user:pass@LOCALHOST:8080"},
	{"user:pass@127.0.0.1:9222", "https://user:pass@127.0.0.1:9222"},
	{"user:pass@[::1]", "https://user:pass@[::1]"},
	{"user:pass@[::1]:8080", "https://user:pass@[::1]:8080"},
	{"user:p%40ss@[fe80::1%25eth0]:8080/path", "https://user:p%40ss@[fe80::1%25eth0]:8080/path"},
	{"user:pass@intranet:8080", "https://user:pass@intranet:8080"},
	{"http://example.com", "http://example.com"},
	{"https://google.com", "https://google.com"},
	{"HTTPS://Example.com/a%20b?q=1#section", "HTTPS://Example.com/a%20b?q=1#section"},
	{"http://localhost:8080", "http://localhost:8080"},
	{"https://[::1]:8080", "https://[::1]:8080"},
	{"https://user:pass@example.com:8080/path", "https://user:pass@example.com:8080/path"},
	{"about:blank", "about:blank"},
	{"file:/tmp/page.html", "file:/tmp/page.html"},
	{"file:///tmp/page.html", "file:///tmp/page.html"},
	{"chrome://settings", "chrome://settings"},
	{"chrome:settings", "chrome:settings"},
	{"data:text/plain,hello%20world", "data:text/plain,hello%20world"},
	{"mailto:user@example.com", "mailto:user@example.com"},
	{"tel:123456789", "tel:123456789"},
	{"sip:alice@example.com", "sip:alice@example.com"},
	{"sips:alice@example.com:5061", "sips:alice@example.com:5061"},
	{"xmpp:user@jabber.org", "xmpp:user@jabber.org"},
	{"sms:+123456789", "sms:+123456789"},
	{"magnet:?xt=urn:btih:abcdef", "magnet:?xt=urn:btih:abcdef"},
	{"MAGNET:?xt=urn:btih:abcdef", "MAGNET:?xt=urn:btih:abcdef"},
	{"irc:alice@irc.example.com", "irc:alice@irc.example.com"},
	{"ircs:alice@irc.example.com:6697", "ircs:alice@irc.example.com:6697"},
	{"news:message@example.com", "news:message@example.com"},
	{"nntp:message@news.example.com", "nntp:message@news.example.com"},
	{"geo:37.786971,-122.399677", "geo:37.786971,-122.399677"},
	{"webcal:calendar@example.com", "webcal:calendar@example.com"},
	{"facetime:alice@example.com", "facetime:alice@example.com"},
	{"skype:alice@example.com?call", "skype:alice@example.com?call"},
	{"SIP:alice@example.com", "SIP:alice@example.com"},
	{"javascript:alert(1)", "javascript:alert(1)"},
	{"javascript:alert('fe80::1')", "javascript:alert('fe80::1')"},
	{"data:text/plain,fe80::1", "data:text/plain,fe80::1"},
	{"ftp://example.com/file", "ftp://example.com/file"},
	{"custom:resource", "custom:resource"},
	{"custom://example.com/path", "custom://example.com/path"},
	{"my.scheme://x", "my.scheme://x"},
	{"my.scheme://example.com/path", "my.scheme://example.com/path"},
	{"MY.SCHEME://example.com/path?q=1#section", "MY.SCHEME://example.com/path?q=1#section"},
	{"localhost://example.com/path", "localhost://example.com/path"},
	{"fe80://[::1]/path", "fe80://[::1]/path"},
	{"custom:resource.name/path", "custom:resource.name/path"},
	{"custom:resource/path::part", "custom:resource/path::part"},
	{"user:pass@myhost", "user:pass@myhost"},
	{"custom:@example.com", "custom:@example.com"},
	{"custom:resource@@example.com", "custom:resource@@example.com"},
	{"custom:resource@not_a.hostname", "custom:resource@not_a.hostname"},
	{"custom:resource@example..com", "custom:resource@example..com"},
	{"custom:resource@-example.com", "custom:resource@-example.com"},
	{"custom:resource@example-.com", "custom:resource@example-.com"},
	{"custom:resource@not_a_host:8080", "custom:resource@not_a_host:8080"},
	{"custom:res[ource]@example.com", "custom:res[ource]@example.com"},
}

var invalidURLs = []string{
	"", " ", "\t", "\n", " \t\r\n ", "google .com", " google .com ",
	"example.com/a b", "example.com/?q=a b", "example.com/\tpath",
	"example.com/\npath", "example.com/\x00path", "example.com/\u00a0path",
	"/path", "?q=1", "#section", ":8080", "https://", "http:///path",
	"https://?q=1", "https://:443", "https:example.com", "http:8080",
	"about:", "file:", "chrome:", "data:", "magnet:", "magnet:?",
	"localhost:bad", "example.com:bad", "https://example.com:bad",
	"example.com:", "localhost:65536", "https://example.com:65536",
	"https://example.com:", "[::1", "[not-an-ip]:8080", "[::1]:bad",
	"[::1]:65536", "::1", "https://::1", "https://[::1]:",
	"example.com/%zz", "https://example.com/%zz",
	"2001:db8::1", "fe80::1", "fe80::1/path?q=1", "fe80::1%eth0",
	"fe80:0:0:0:0:0:0:1", "2001:db8:0:0:0:0:0:1",
	"user:pass@fe80::1", "user:pass@2001:db8::1",
	"user:pass@example.com:bad", "user:pass@example.com:65536",
	"user:pass@localhost:bad", "user:pass@[127.0.0.1]:8080",
	"user:pass@[::1]:bad", "user:pass@[::1]:65536",
	"http://[127.0.0.1]/", "my.scheme://example.com/%zz",
}

func TestNormalizeURL(t *testing.T) {
	for _, tc := range validURLs {
		t.Run(tc.input, func(t *testing.T) {
			got, err := normalizeURL(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("normalizeURL(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
			}
		})
	}
	for _, input := range invalidURLs {
		t.Run("invalid/"+input, func(t *testing.T) {
			got, err := normalizeURL(input)
			if err == nil || got != "" {
				t.Fatalf("normalizeURL(%q) = %q, %v; want empty URL and error", input, got, err)
			}
		})
	}
}

func TestNormalizeURLKnownOpaqueSchemes(t *testing.T) {
	for _, scheme := range []string{
		"sip", "sips", "xmpp", "tel", "sms", "mailto", "magnet", "irc", "ircs",
		"news", "nntp", "geo", "webcal", "facetime", "skype",
	} {
		for _, spelling := range []string{scheme, strings.ToUpper(scheme)} {
			// Known schemes must win even when their contents look like a bare
			// host:port, userinfo, or an address we would otherwise reject.
			for _, address := range []string{"8080", "alice@example.com", "pass@localhost:8080", "pass@[::1]:8080", "pass@fe80::1"} {
				input := spelling + ":" + address
				t.Run(input, func(t *testing.T) {
					got, err := normalizeURL(input)
					if err != nil || got != input {
						t.Fatalf("normalizeURL(%q) = %q, %v; want unchanged URL", input, got, err)
					}
				})
			}
		}
	}
}

func TestNormalizeURLUnbracketedIPv6(t *testing.T) {
	for _, input := range []string{"::1", "2001:db8::1", "fe80::1", "fe80::1/path", "user:pass@fe80::1", "fe80:0:0:0:0:0:0:1"} {
		t.Run(input, func(t *testing.T) {
			_, err := normalizeURL(input)
			if err == nil || !strings.Contains(err.Error(), "IPv6 addresses must be enclosed in brackets") {
				t.Fatalf("normalizeURL(%q) error = %v; want IPv6 brackets error", input, err)
			}
		})
	}
}

func TestNormalizeURLParseError(t *testing.T) {
	for _, input := range []string{
		"https://example.com/%zz", "http://[127.0.0.1]/", "https://example.com:bad",
		"my.scheme://example.com/%zz", "example.com/%zz", "[::1]:bad",
	} {
		t.Run(input, func(t *testing.T) {
			_, err := normalizeURL(input)
			var parseErr *url.Error
			if !errors.As(err, &parseErr) {
				t.Fatalf("normalizeURL(%q) error = %v; want wrapped url.Error", input, err)
			}
			if parseErr.URL != input {
				t.Fatalf("parse error URL = %q; want original input %q", parseErr.URL, input)
			}
			if _, originalErr := url.Parse(input); originalErr != nil && strings.Contains(input, "://") {
				if err.Error() != "invalid URL: "+originalErr.Error() {
					t.Fatalf("error = %q; want original parse error %q", err, originalErr)
				}
			}
		})
	}
}

func TestURLCommands(t *testing.T) {
	t.Setenv("BB_BROWSER_API_TOKEN", "")
	for _, command := range []struct {
		name    string
		args    []string
		methods []string
		silent  bool
	}{
		{"open", []string{"open"}, []string{protocol.MethodTabNew}, false},
		{"open silent", []string{"open", "--silent"}, []string{protocol.MethodTabNew}, true},
		{"open current", []string{"open", "--current"}, []string{protocol.MethodTabList, protocol.MethodGoto}, false},
		{"open current explicit tab", []string{"open", "--current", "--tab", "focused"}, []string{protocol.MethodGoto}, false},
		{"tab new", []string{"tab", "new"}, []string{protocol.MethodTabNew}, false},
		{"tab new silent", []string{"tab", "new", "--silent"}, []string{protocol.MethodTabNew}, true},
		{"goto", []string{"goto"}, []string{protocol.MethodTabList, protocol.MethodGoto}, false},
		{"goto explicit tab", []string{"goto", "--tab", "focused"}, []string{protocol.MethodGoto}, false},
		{"fetch", []string{"fetch"}, []string{protocol.MethodTabList, protocol.MethodFetch}, false},
		{"fetch explicit tab", []string{"fetch", "--tab", "focused"}, []string{protocol.MethodFetch}, false},
		{"fetch json", []string{"fetch", "--json"}, []string{protocol.MethodTabList, protocol.MethodFetch}, false},
	} {
		t.Run(command.name, func(t *testing.T) {
			for _, tc := range validURLs {
				t.Run(tc.input, func(t *testing.T) {
					requests, err := executeURLCommand(t, append(slices.Clone(command.args), tc.input))
					if err != nil {
						t.Fatal(err)
					}
					var methods []string
					for _, req := range requests {
						methods = append(methods, req.Method)
					}
					if !slices.Equal(methods, command.methods) {
						t.Fatalf("RPC methods = %v, want %v", methods, command.methods)
					}
					params := requests[len(requests)-1].Params
					if params.URL != tc.want || params.Silent != command.silent {
						t.Fatalf("RPC params = %+v, want url %q, silent %v", params, tc.want, command.silent)
					}
					if (methods[len(methods)-1] == protocol.MethodGoto || methods[len(methods)-1] == protocol.MethodFetch) && params.Tab != "focused" {
						t.Fatalf("RPC tab = %q, want focused", params.Tab)
					}
				})
			}
			for _, input := range invalidURLs {
				t.Run("invalid/"+input, func(t *testing.T) {
					requests, err := executeURLCommand(t, append(slices.Clone(command.args), input))
					if err == nil || !strings.Contains(err.Error(), "invalid URL") {
						t.Fatalf("Execute() = %v; want URL validation error (nonzero CLI exit)", err)
					}
					if len(requests) != 0 {
						t.Fatalf("invalid URL sent %d RPCs; want none", len(requests))
					}
				})
			}
		})
	}
}

func TestTabNewDefaultURL(t *testing.T) {
	requests, err := executeURLCommand(t, []string{"tab", "new"})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].Method != protocol.MethodTabNew || requests[0].Params.URL != "about:blank" {
		t.Fatalf("tab new without URL requests = %+v; want tab_new about:blank", requests)
	}
}

func TestTabNewRejectsExtraURLs(t *testing.T) {
	requests, err := executeURLCommand(t, []string{"tab", "new", "example.com", "google.com"})
	if err == nil || len(requests) != 0 {
		t.Fatalf("tab new with extra URL: err=%v, requests=%+v; want error and no RPC", err, requests)
	}
}

func TestLaunchRejectsInvalidStartURLBeforeSideEffects(t *testing.T) {
	for _, input := range invalidURLs {
		// An empty start URL explicitly asks Chrome to choose its startup page.
		if strings.TrimSpace(input) == "" {
			continue
		}
		t.Run(input, func(t *testing.T) {
			dir := t.TempDir()
			profile := filepath.Join(dir, "profile")
			cmd := newLaunchCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{"--start-url", input, "--chrome", filepath.Join(dir, "missing-chrome"), "--profile", profile})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid URL") {
				t.Fatalf("launch error = %v; want URL error before executable lookup", err)
			}
			if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("profile stat = %v; want no profile creation", err)
			}
		})
	}
}

type urlCommandRequest struct {
	Method string `json:"method"`
	Params struct {
		URL    string `json:"url"`
		Tab    string `json:"tab"`
		Silent bool   `json:"silent"`
	} `json:"params"`
}

// Exercise real Cobra commands against a fake daemon, capturing both focus
// lookup and navigation. Errors returned by Execute cause main to exit with 1.
func executeURLCommand(t *testing.T, args []string) ([]urlCommandRequest, error) {
	t.Helper()
	requests := make(chan urlCommandRequest, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req urlCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- req
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"seq":1,"tab":"created","focus":"focused","tabs":[]}}`)
	}))
	defer srv.Close()
	root := newRootCmd()
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetArgs(append([]string{"--url", srv.URL}, args...))
	err := root.Execute()
	var got []urlCommandRequest
	for len(requests) > 0 {
		got = append(got, <-requests)
	}
	return got, err
}
