package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	{"http://example.com", "http://example.com"},
	{"https://google.com", "https://google.com"},
	{"HTTPS://Example.com/a%20b?q=1#section", "HTTPS://Example.com/a%20b?q=1#section"},
	{"http://localhost:8080", "http://localhost:8080"},
	{"https://[::1]:8080", "https://[::1]:8080"},
	{"about:blank", "about:blank"},
	{"file:/tmp/page.html", "file:/tmp/page.html"},
	{"file:///tmp/page.html", "file:///tmp/page.html"},
	{"chrome://settings", "chrome://settings"},
	{"chrome:settings", "chrome:settings"},
	{"data:text/plain,hello%20world", "data:text/plain,hello%20world"},
	{"mailto:user@example.com", "mailto:user@example.com"},
	{"tel:123456789", "tel:123456789"},
	{"javascript:alert(1)", "javascript:alert(1)"},
	{"ftp://example.com/file", "ftp://example.com/file"},
	{"custom:resource", "custom:resource"},
	{"custom://example.com/path", "custom://example.com/path"},
}

var invalidURLs = []string{
	"", " ", "\t", "\n", " \t\r\n ", "google .com", " google .com ",
	"example.com/a b", "example.com/?q=a b", "example.com/\tpath",
	"example.com/\npath", "example.com/\x00path", "example.com/\u00a0path",
	"/path", "?q=1", "#section", ":8080", "https://", "http:///path",
	"https://?q=1", "https://:443", "https:example.com", "http:8080",
	"about:", "file:", "chrome:", "data:",
	"localhost:bad", "example.com:bad", "https://example.com:bad",
	"example.com:", "localhost:65536", "https://example.com:65536",
	"https://example.com:", "[::1", "[not-an-ip]:8080", "[::1]:bad",
	"[::1]:65536", "::1", "https://::1", "https://[::1]:",
	"example.com/%zz", "https://example.com/%zz",
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
					if methods[len(methods)-1] == protocol.MethodGoto && params.Tab != "focused" {
						t.Fatalf("goto tab = %q, want focused", params.Tab)
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
