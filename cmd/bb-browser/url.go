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
// validates them before navigation or tab lookup. Explicit schemes and the
// remaining URL spelling are retained.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("invalid URL: empty input")
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", fmt.Errorf("invalid URL: whitespace or control character (use percent encoding)")
	}

	// Determine the scheme independently of the rest of the URL, which may
	// contain a parse error. A failed parse must not add HTTPS to an explicit URL.
	scheme := ""
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		if prefix, err := url.Parse(raw[:i+1]); err == nil {
			scheme = prefix.Scheme
		}
	}
	normalized := raw
	if scheme == "" || isBareHostPort(raw, scheme) {
		authority, _ := bareURLAuthority(raw)
		if isUnbracketedIPv6(authority) {
			return "", fmt.Errorf("invalid URL: IPv6 addresses must be enclosed in brackets")
		}
		normalized = "https://" + raw
	}
	u, err := url.Parse(normalized)
	if err != nil {
		if parseErr, ok := err.(*url.Error); ok {
			err = &url.Error{Op: parseErr.Op, URL: raw, Err: parseErr.Err}
		}
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
	return normalized, nil
}

// url.Parse treats localhost:8080 and example.com:443 as opaque URLs with a
// scheme. Treat an authority with a numeric port (or an obvious hostname) as a
// bare address instead. Browser schemes take precedence over that ambiguity.
func isBareHostPort(raw, scheme string) bool {
	if strings.HasPrefix(raw[len(scheme):], "://") {
		return false
	}
	switch strings.ToLower(scheme) {
	case "http", "https", "about", "file", "chrome", "chrome-extension", "data",
		"blob", "javascript", "mailto", "tel", "view-source", "ftp", "ftps", "ws", "wss":
		return false
	}
	authority, hasUserinfo := bareURLAuthority(raw)
	if isUnbracketedIPv6(authority) {
		return true // Let normalizeURL reject unbracketed IPv6, including fe80::1.
	}
	// A password's colon can look like a scheme. Inspect the host after the
	// userinfo, retaining the original spelling for the final parse.
	if hasUserinfo && (strings.EqualFold(authority, "localhost") ||
		strings.Contains(authority, ".") || strings.HasPrefix(authority, "[")) {
		return true
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

// bareURLAuthority extracts a bare address's host/port without its userinfo.
func bareURLAuthority(raw string) (authority string, hasUserinfo bool) {
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.LastIndexByte(raw, '@'); i >= 0 {
		return raw[i+1:], true
	}
	return raw, false
}

func isUnbracketedIPv6(authority string) bool {
	if strings.HasPrefix(authority, "[") {
		return false
	}
	if strings.Contains(authority, "::") {
		return true
	}
	ip, err := netip.ParseAddr(authority)
	return err == nil && ip.Is6()
}
