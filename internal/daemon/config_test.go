package daemon

import "testing"

func TestConfigValidate_DebuggerRequired(t *testing.T) {
	c := Config{ListenAddr: "127.0.0.1:0"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when DebuggerURL is empty")
	}
}

func TestConfigValidate_DebuggerWhitespaceTrimmed(t *testing.T) {
	c := Config{DebuggerURL: "  127.0.0.1:9222  ", ListenAddr: "127.0.0.1:0"}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.DebuggerURL != "127.0.0.1:9222" {
		t.Fatalf("got %q", c.DebuggerURL)
	}
}

func TestConfigValidate_DefaultListenAndBodyLimit(t *testing.T) {
	c := Config{DebuggerURL: "127.0.0.1:9222"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != DefaultListenAddr {
		t.Fatalf("listen: got %q want %q", c.ListenAddr, DefaultListenAddr)
	}
	if c.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Fatalf("max body: got %d want %d", c.MaxBodyBytes, DefaultMaxBodyBytes)
	}
}

func TestValidateDebuggerEndpoint(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1:9222",
		"http://127.0.0.1:9222",
		"ws://127.0.0.1:9222/devtools/browser/foo",
	} {
		if err := validateDebuggerEndpoint(raw); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "not_a_url", "http://"} {
		if err := validateDebuggerEndpoint(raw); err == nil {
			t.Errorf("%q: expected error", raw)
		}
	}
}
