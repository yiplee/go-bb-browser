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

// routeCDP is the worker's CDP entry. Tests replace it; the listen callback must not call it.
var routeCDP = runCDP

// listenTarget is chromedp.ListenTarget. Tests replace it to count listeners.
var listenTarget = chromedp.ListenTarget

type routeState struct {
	mu    sync.Mutex
	cdpMu sync.Mutex

	rules   map[target.ID][]NetworkRouteRule
	workers map[target.ID]*routeWorker
	gen     map[target.ID]uint64
}

type routeWorker struct {
	cancel context.CancelFunc
	wake   chan struct{}

	mu           sync.Mutex
	queue        []*fetch.EventRequestPaused
	listening    bool
	listenCancel context.CancelFunc
}

func newRouteState() *routeState {
	return &routeState{
		rules:   make(map[target.ID][]NetworkRouteRule),
		workers: make(map[target.ID]*routeWorker),
		gen:     make(map[target.ID]uint64),
	}
}

func (s *Session) routeStateLocked() *routeState {
	return s.routeState
}

// SetNetworkRoutes replaces all Fetch interception rules for a tab and syncs CDP.
func (s *Session) SetNetworkRoutes(tabID target.ID, rules []NetworkRouteRule) error {
	return s.SetNetworkRoutesContext(context.Background(), tabID, rules)
}

func (s *Session) SetNetworkRoutesContext(ctx context.Context, tabID target.ID, rules []NetworkRouteRule) error {
	if s == nil {
		return errNilSession()
	}
	rs := s.routeStateLocked()
	rs.mu.Lock()
	if len(rules) == 0 {
		delete(rs.rules, tabID)
	} else {
		cp := make([]NetworkRouteRule, len(rules))
		copy(cp, rules)
		rs.rules[tabID] = cp
	}
	rs.mu.Unlock()
	return s.syncRoutes(ctx, rs, tabID)
}

// AppendNetworkRoute adds one rule for a tab (existing rules preserved).
func (s *Session) AppendNetworkRoute(tabID target.ID, rule NetworkRouteRule) error {
	return s.AppendNetworkRouteContext(context.Background(), tabID, rule)
}

func (s *Session) AppendNetworkRouteContext(ctx context.Context, tabID target.ID, rule NetworkRouteRule) error {
	if s == nil {
		return errNilSession()
	}
	rs := s.routeStateLocked()
	rs.mu.Lock()
	rs.rules[tabID] = append(rs.rules[tabID], rule)
	rs.mu.Unlock()
	return s.syncRoutes(ctx, rs, tabID)
}

// RemoveNetworkRoutes removes rules whose URLPattern matches pattern; empty pattern clears all rules for the tab.
func (s *Session) RemoveNetworkRoutes(tabID target.ID, urlPattern string) error {
	return s.RemoveNetworkRoutesContext(context.Background(), tabID, urlPattern)
}

func (s *Session) RemoveNetworkRoutesContext(ctx context.Context, tabID target.ID, urlPattern string) error {
	if s == nil {
		return errNilSession()
	}
	rs := s.routeStateLocked()
	rs.mu.Lock()
	pat := strings.TrimSpace(urlPattern)
	if pat == "" {
		delete(rs.rules, tabID)
	} else {
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
	}
	rs.mu.Unlock()
	return s.syncRoutes(ctx, rs, tabID)
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

func (s *Session) syncRoutes(ctx context.Context, rs *routeState, tabID target.ID) error {
	tabCtx, err := s.tabChromeCtxContext(ctx, tabID)
	if err != nil {
		return err
	}
	rs.cdpMu.Lock()
	defer rs.cdpMu.Unlock()

	rules, gen := rs.snapshot(tabID)
	if len(rules) == 0 {
		err := runCDPWithContext(tabCtx, ctx, chromedp.ActionFunc(func(c context.Context) error {
			return fetch.Disable().Do(c)
		}))
		if w := rs.worker(tabID); w != nil {
			w.stopListening()
		}
		return err
	}

	w := rs.ensureWorker(tabID, tabCtx, gen)
	if w == nil || !w.startListening(tabCtx, rs, tabID, gen) {
		return nil
	}
	patterns := routePatterns(rules)
	if len(patterns) == 0 {
		return nil
	}
	return runCDPWithContext(tabCtx, ctx, chromedp.ActionFunc(func(c context.Context) error {
		return fetch.Enable().WithPatterns(patterns).Do(c)
	}))
}

func routePatterns(rules []NetworkRouteRule) []*fetch.RequestPattern {
	patterns := make([]*fetch.RequestPattern, 0, len(rules))
	for _, r := range rules {
		p := strings.TrimSpace(r.URLPattern)
		if p == "" {
			continue
		}
		patterns = append(patterns, &fetch.RequestPattern{URLPattern: p, RequestStage: fetch.RequestStageRequest})
	}
	return patterns
}

func (rs *routeState) snapshot(tabID target.ID) ([]NetworkRouteRule, uint64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []NetworkRouteRule
	if src := rs.rules[tabID]; len(src) > 0 {
		out = make([]NetworkRouteRule, len(src))
		copy(out, src)
	}
	return out, rs.gen[tabID]
}

func (rs *routeState) generation(tabID target.ID) uint64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.gen[tabID]
}

func (rs *routeState) worker(tabID target.ID) *routeWorker {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.workers[tabID]
}

func (rs *routeState) ensureWorker(tabID target.ID, tabCtx context.Context, gen uint64) *routeWorker {
	rs.mu.Lock()
	if rs.gen[tabID] != gen || len(rs.rules[tabID]) == 0 {
		rs.mu.Unlock()
		return nil
	}
	w := rs.workers[tabID]
	if w != nil {
		rs.mu.Unlock()
		return w
	}
	ctx, cancel := context.WithCancel(tabCtx)
	w = &routeWorker{
		cancel: cancel,
		wake:   make(chan struct{}, 1),
	}
	if rs.workers == nil {
		rs.workers = make(map[target.ID]*routeWorker)
	}
	rs.workers[tabID] = w
	rs.mu.Unlock()
	go w.loop(ctx, rs, tabID, tabCtx)
	return w
}

func (w *routeWorker) startListening(tabCtx context.Context, rs *routeState, tabID target.ID, gen uint64) bool {
	live := func() bool { return rs.generation(tabID) == gen }
	if !live() {
		return false
	}
	w.mu.Lock()
	if w.listening {
		w.mu.Unlock()
		if live() {
			return true
		}
		w.stopListening()
		return false
	}
	ctx, cancel := context.WithCancel(tabCtx)
	w.listenCancel = cancel
	w.listening = true
	w.mu.Unlock()
	if !live() {
		w.stopListening()
		return false
	}
	listenTarget(ctx, w.onPaused)
	if !live() {
		w.stopListening()
		return false
	}
	return true
}

func (w *routeWorker) stopListening() {
	w.mu.Lock()
	cancel := w.listenCancel
	w.listenCancel = nil
	w.listening = false
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (w *routeWorker) onPaused(ev any) {
	e, ok := ev.(*fetch.EventRequestPaused)
	if !ok || e == nil || e.Request == nil {
		return
	}
	w.mu.Lock()
	w.queue = append(w.queue, e)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *routeWorker) pop() *fetch.EventRequestPaused {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return nil
	}
	ev := w.queue[0]
	w.queue[0] = nil
	w.queue = w.queue[1:]
	if len(w.queue) == 0 {
		w.queue = nil
	}
	return ev
}

func (w *routeWorker) loop(ctx context.Context, rs *routeState, tabID target.ID, tabCtx context.Context) {
	defer w.stopListening()
	for {
		w.dispatchAll(rs, tabID, tabCtx)
		select {
		case <-ctx.Done():
			w.dispatchAll(rs, tabID, tabCtx)
			return
		case <-w.wake:
		}
	}
}

func (w *routeWorker) dispatchAll(rs *routeState, tabID target.ID, tabCtx context.Context) {
	for {
		ev := w.pop()
		if ev == nil {
			return
		}
		rules, _ := rs.snapshot(tabID)
		applyPaused(tabCtx, rules, ev)
	}
}

func applyPaused(tabCtx context.Context, rules []NetworkRouteRule, e *fetch.EventRequestPaused) {
	if e == nil || e.Request == nil {
		return
	}
	if e.ResponseStatusCode != 0 || e.ResponseErrorReason != "" {
		continuePaused(tabCtx, e.RequestID)
		return
	}
	matched, ok := matchRoute(rules, e.Request.URL)
	if !ok {
		continuePaused(tabCtx, e.RequestID)
		return
	}
	if matched.Abort {
		_ = routeCDP(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
		}))
		return
	}
	ct := strings.TrimSpace(matched.ContentType)
	if ct == "" {
		ct = "application/json"
	}
	st := matched.Status
	if st <= 0 {
		st = 200
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(matched.MockBody))
	headers := []*fetch.HeaderEntry{{Name: "Content-Type", Value: ct}}
	_ = routeCDP(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return fetch.FulfillRequest(e.RequestID, int64(st)).
			WithResponseHeaders(headers).
			WithBody(b64).
			Do(ctx)
	}))
}

func continuePaused(tabCtx context.Context, id fetch.RequestID) {
	_ = routeCDP(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return fetch.ContinueRequest(id).Do(ctx)
	}))
}

func matchRoute(rules []NetworkRouteRule, reqURL string) (NetworkRouteRule, bool) {
	for i := range rules {
		if urlMatch(rules[i].URLPattern, reqURL) {
			return rules[i], true
		}
	}
	return NetworkRouteRule{}, false
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
	delete(rs.rules, tabID)
	if rs.gen == nil {
		rs.gen = make(map[target.ID]uint64)
	}
	rs.gen[tabID]++
	w := rs.workers[tabID]
	delete(rs.workers, tabID)
	rs.mu.Unlock()
	if w != nil {
		w.stopListening()
		w.cancel()
	}
}
