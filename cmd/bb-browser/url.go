package main

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// normalizeURL trims surrounding whitespace, adds HTTPS to bare addresses, and
// validates them before any RPC can create a tab. Explicit schemes and the
// remaining URL spelling are retained.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("invalid URL: empty input")
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", fmt.Errorf("invalid URL: whitespace or control character (use percent encoding)")
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || isBareHostPort(raw, u.Scheme) {
		raw = "https://" + raw
		u, err = url.Parse(raw)
	}
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" && u.Path == "" && u.Opaque == "" {
		return "", fmt.Errorf("invalid URL: missing host or scheme-specific address")
	}
	if strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https") {
		if u.Hostname() == "" {
			return "", fmt.Errorf("invalid URL: HTTP(S) requires a host")
		}
		if strings.HasPrefix(u.Host, "[") {
			ip, err := netip.ParseAddr(u.Hostname())
			if err != nil || !ip.Is6() {
				return "", fmt.Errorf("invalid URL: bracketed host must be an IPv6 address")
			}
		} else if strings.Contains(u.Hostname(), ":") {
			return "", fmt.Errorf("invalid URL: IPv6 addresses must be enclosed in brackets")
		}
		if strings.HasSuffix(u.Host, ":") {
			return "", fmt.Errorf("invalid URL: empty port")
		}
		if port := u.Port(); port != "" {
			if _, err := strconv.ParseUint(port, 10, 16); err != nil {
				return "", fmt.Errorf("invalid URL: port must be between 0 and 65535")
			}
		}
	}
	return raw, nil
}

// url.Parse treats localhost:8080 and example.com:443 as opaque URLs with a
// scheme. Treat an authority with a numeric port (or an obvious hostname) as a
// bare address instead. Browser schemes take precedence over that ambiguity.
func isBareHostPort(raw, scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https", "about", "file", "chrome", "chrome-extension", "data",
		"blob", "javascript", "mailto", "tel", "view-source", "ftp", "ftps", "ws", "wss":
		return false
	}
	authority := raw
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority = authority[:i]
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil || host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || strings.Contains(host, ".") {
		return true
	}
	if port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
