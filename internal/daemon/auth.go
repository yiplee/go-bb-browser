package daemon

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
)

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
		for _, line := range strings.Split(string(data), "\n") {
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

func (s *Server) warnUnauthenticatedListen() {
	if len(s.cfg.APITokens) != 0 {
		return
	}
	addr, err := net.ResolveTCPAddr("tcp", s.cfg.ListenAddr)
	if err == nil && !addr.IP.IsLoopback() {
		s.logger.Warn("API token authentication is disabled on a non-loopback listen address")
	}
}
