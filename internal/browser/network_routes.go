package browser

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// NetworkRouteRule is one URL interception rule for CDP Fetch (per tab).
type NetworkRouteRule struct {
	URLPattern  string // CDP wildcard pattern (see Fetch RequestPattern)
	Abort       bool
	MockBody    string // UTF-8 response body when not aborting
	ContentType string // e.g. application/json
	Status      int    // HTTP status for mock (default 200)
}

type routeState struct {
	mu sync.Mutex

	rules map[target.ID][]NetworkRouteRule

	listenerRegistered map[target.ID]struct{}
}

func newRouteState() *routeState {
	return &routeState{
		rules:              make(map[target.ID][]NetworkRouteRule),
		listenerRegistered: make(map[target.ID]struct{}),
	}
}

func (s *Session) routeStateLocked() *routeState {
	if s.routeState == nil {
		s.routeState = newRouteState()
	}
	return s.routeState
}

// SetNetworkRoutes replaces all Fetch interception rules for a tab and syncs CDP.
func (s *Session) SetNetworkRoutes(tabID target.ID, rules []NetworkRouteRule) error {
	if s == nil {
		return errNilSession()
	}
	rs := s.routeStateLocked()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rules) == 0 {
		delete(rs.rules, tabID)
		return s.syncFetchEnableLocked(rs, tabID)
	}
	cp := make([]NetworkRouteRule, len(rules))
	copy(cp, rules)
	rs.rules[tabID] = cp
	return s.syncFetchEnableLocked(rs, tabID)
}

// AppendNetworkRoute adds one rule for a tab (existing rules preserved).
func (s *Session) AppendNetworkRoute(tabID target.ID, rule NetworkRouteRule) error {
	if s == nil {
		return errNilSession()
	}
	rs := s.routeStateLocked()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.rules[tabID] = append(rs.rules[tabID], rule)
	return s.syncFetchEnableLocked(rs, tabID)
}

// RemoveNetworkRoutes removes rules whose URLPattern matches pattern; empty pattern clears all rules for the tab.
func (s *Session) RemoveNetworkRoutes(tabID target.ID, urlPattern string) error {
	if s == nil {
		return errNilSession()
	}
	rs := s.routeStateLocked()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	pat := strings.TrimSpace(urlPattern)
	if pat == "" {
		delete(rs.rules, tabID)
		return s.syncFetchEnableLocked(rs, tabID)
	}
	cur := rs.rules[tabID]
	var out []NetworkRouteRule
	for _, r := range cur {
		if r.URLPattern != pat {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		delete(rs.rules, tabID)
	} else {
		rs.rules[tabID] = out
	}
	return s.syncFetchEnableLocked(rs, tabID)
}

// NetworkRouteCount returns how many interception rules are active for a tab.
func (s *Session) NetworkRouteCount(tabID target.ID) int {
	if s == nil || s.routeState == nil {
		return 0
	}
	s.routeState.mu.Lock()
	defer s.routeState.mu.Unlock()
	return len(s.routeState.rules[tabID])
}

func (s *Session) syncFetchEnableLocked(rs *routeState, tabID target.ID) error {
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return err
	}
	rules := rs.rules[tabID]
	if len(rules) == 0 {
		delete(rs.listenerRegistered, tabID)
		return chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return fetch.Disable().Do(ctx)
		}))
	}

	if _, ok := rs.listenerRegistered[tabID]; !ok {
		rs.listenerRegistered[tabID] = struct{}{}
		chromedp.ListenTarget(tabCtx, func(ev interface{}) {
			e, ok := ev.(*fetch.EventRequestPaused)
			if !ok || e == nil || e.Request == nil {
				return
			}
			if e.ResponseStatusCode != 0 || e.ResponseErrorReason != "" {
				_ = chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					return fetch.ContinueRequest(e.RequestID).Do(ctx)
				}))
				return
			}
			reqURL := e.Request.URL

			rs.mu.Lock()
			curRules := rs.rules[tabID]
			rs.mu.Unlock()

			var matched *NetworkRouteRule
			for i := range curRules {
				if urlMatch(curRules[i].URLPattern, reqURL) {
					matched = &curRules[i]
					break
				}
			}
			if matched == nil {
				_ = chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					return fetch.ContinueRequest(e.RequestID).Do(ctx)
				}))
				return
			}
			if matched.Abort {
				_ = chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					return fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
				}))
				return
			}
			body := matched.MockBody
			ct := strings.TrimSpace(matched.ContentType)
			if ct == "" {
				ct = "application/json"
			}
			st := matched.Status
			if st <= 0 {
				st = 200
			}
			b64 := base64.StdEncoding.EncodeToString([]byte(body))
			headers := []*fetch.HeaderEntry{{Name: "Content-Type", Value: ct}}
			_ = chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return fetch.FulfillRequest(e.RequestID, int64(st)).
					WithResponseHeaders(headers).
					WithBody(b64).
					Do(ctx)
			}))
		})
	}

	patterns := make([]*fetch.RequestPattern, 0, len(rules))
	for _, r := range rules {
		p := strings.TrimSpace(r.URLPattern)
		if p == "" {
			continue
		}
		patterns = append(patterns, &fetch.RequestPattern{URLPattern: p, RequestStage: fetch.RequestStageRequest})
	}
	if len(patterns) == 0 {
		return nil
	}
	return chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return fetch.Enable().WithPatterns(patterns).Do(ctx)
	}))
}

func urlMatch(pattern, reqURL string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, "://") || strings.HasPrefix(pattern, "*") {
		return wildcardMatch(pattern, reqURL)
	}
	u, err := url.Parse(reqURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	path := u.Path
	if path == "" {
		path = "/"
	}
	patLower := strings.ToLower(pattern)
	if strings.Contains(patLower, "/") {
		full := host + path
		return wildcardMatch(strings.ToLower(pattern), strings.ToLower(full))
	}
	return host == patLower || strings.HasSuffix(host, "."+patLower)
}

func wildcardMatch(pattern, s string) bool {
	p := pattern
	str := s
	for len(p) > 0 {
		if p[0] == '*' {
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(str); i++ {
				if wildcardMatch(p[1:], str[i:]) {
					return true
				}
			}
			return false
		}
		if len(str) == 0 {
			return false
		}
		if p[0] == '?' {
			p, str = p[1:], str[1:]
			continue
		}
		if p[0] != str[0] {
			return false
		}
		p, str = p[1:], str[1:]
	}
	return len(str) == 0
}

// ClearRoutesForTarget removes routing rules when a tab target disappears.
func (s *Session) ClearRoutesForTarget(tabID target.ID) {
	if s == nil || s.routeState == nil {
		return
	}
	rs := s.routeState
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.rules, tabID)
	delete(rs.listenerRegistered, tabID)
}
