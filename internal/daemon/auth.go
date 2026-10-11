package daemon

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// proxyHeaders disable the loopback exemption whenever present, even if empty.
// Tailscale-* headers are checked separately as a case-insensitive prefix.
var proxyHeaders = []string{
	"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP",
	"Forwarded", "CF-Connecting-IP", "CF-Ray", "True-Client-IP", "Via",
	"X-Forwarded-Server", "X-Forwarded-Port", "X-Forwarded-Scheme", "X-Original-Forwarded-For",
	"Forwarded-For", "X-Client-IP", "X-Cluster-Client-IP",
}

// LoadAPITokens merges flags, comma-separated environment tokens and a token file.
// File errors deliberately omit the path and contents, which may contain secrets.
func LoadAPITokens(flags []string, env, path string) ([]string, error) {
	tokens := append([]string(nil), flags...)
	tokens = append(tokens, strings.Split(env, ",")...)
	if path = strings.TrimSpace(path); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("cannot read API token file")
		}
		for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "#") {
				tokens = append(tokens, line)
			}
		}
	}
	return normalizeAPITokens(tokens), nil
}

func normalizeAPITokens(tokens []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token != "" && !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	return out
}

func (s *Server) authorized(r *http.Request) bool {
	if len(s.cfg.APITokens) == 0 {
		return true
	}
	if s.cfg.APITokenAllowLoopback && directLoopbackRequest(r) {
		return true
	}
	headers := r.Header.Values("Authorization")
	var token string
	valid := false
	if len(headers) == 1 {
		scheme, value, ok := strings.Cut(headers[0], " ")
		valid = ok && strings.EqualFold(scheme, "Bearer") && value != "" && !strings.ContainsAny(value, " \t\r\n,")
		if valid {
			token = value
		}
	}
	matched := 0
	for _, allowed := range s.cfg.APITokens {
		matched |= subtle.ConstantTimeCompare([]byte(token), []byte(allowed))
	}
	return valid && matched != 0
}

func directLoopbackRequest(r *http.Request) bool {
	host, port, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	// net/http always writes a numeric port; anything else is malformed, so fail closed.
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return false
	}
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "tailscale-") {
			return false
		}
		for _, proxyHeader := range proxyHeaders {
			if strings.EqualFold(name, proxyHeader) {
				return false
			}
		}
	}
	return true
}

func (s *Server) warnLoopbackAuthentication() {
	if !s.cfg.APITokenAllowLoopback {
		return
	}
	if len(s.cfg.APITokens) == 0 {
		s.logger.Info("API token loopback exemption has no effect because no API tokens are configured")
		return
	}
	s.logger.Warn("direct loopback requests without proxy headers are exempt from API tokens; do not enable with a local proxy that may omit headers")
}

func (s *Server) warnUnauthenticatedListen() {
	if len(s.cfg.APITokens) != 0 {
		return
	}
	addr, err := net.ResolveTCPAddr("tcp", s.cfg.ListenAddr)
	if err == nil && !addr.IP.IsLoopback() {
		s.logger.Warn("API token authentication is disabled on a non-loopback listen address")
	}
}
