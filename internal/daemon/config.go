package daemon

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Config holds daemon runtime settings. DebuggerURL is required from Phase 0 onward so
// misconfiguration is caught even before CDP attach is implemented.
type Config struct {
	// DebuggerURL is the Chrome DevTools debugging endpoint the daemon will attach to
	// (WebSocket URL, or http(s) URL to the debugger HTTP API, or host:port such as 127.0.0.1:9222).
	DebuggerURL string

	// ListenAddr is the TCP address for the HTTP API (default from flags).
	ListenAddr string

	// MaxBodyBytes caps incoming HTTP request bodies (POST /v1 and similar).
	MaxBodyBytes int64
}

const (
	DefaultListenAddr   = "127.0.0.1:8787"
	DefaultMaxBodyBytes = 1 << 20 // 1 MiB
)

// Validate checks required fields and normalizes DebuggerURL whitespace.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("daemon config is nil")
	}
	raw := strings.TrimSpace(c.DebuggerURL)
	if raw == "" {
		return fmt.Errorf("debugger URL is required (set --debugger-url or BB_BROWSER_DEBUGGER_URL)")
	}
	if err := validateDebuggerEndpoint(raw); err != nil {
		return err
	}
	c.DebuggerURL = raw

	if strings.TrimSpace(c.ListenAddr) == "" {
		c.ListenAddr = DefaultListenAddr
	}
	if _, err := net.ResolveTCPAddr("tcp", c.ListenAddr); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", c.ListenAddr, err)
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return nil
}

func validateDebuggerEndpoint(raw string) error {
	s := raw
	if !strings.Contains(s, "://") {
		host, port, err := net.SplitHostPort(raw)
		if err != nil {
			return fmt.Errorf("invalid debugger endpoint %q: expected host:port or ws/http(s) URL", raw)
		}
		if host == "" || port == "" {
			return fmt.Errorf("invalid debugger endpoint %q: missing host or port", raw)
		}
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid debugger URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid debugger URL %q: missing host", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss", "http", "https":
	default:
		return fmt.Errorf("invalid debugger URL %q: scheme must be ws, wss, http, or https", raw)
	}
	return nil
}
