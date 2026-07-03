package store

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP extracts the client IP from an HTTP request.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			xff = strings.TrimSpace(xff[:i])
		}
		if xff != "" {
			return xff
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
