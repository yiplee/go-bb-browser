package store

import (
	"net"
	"net/http"
)

// ClientIP extracts the client IP from an HTTP request using RemoteAddr only.
// X-Forwarded-For is not trusted by default (avoids spoofed audit entries behind proxies).
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
