package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// Pool spreads JSON-RPC calls across multiple [Client] backends (one bb-daemon
// each). The existing single-daemon [Client] API is unchanged; Pool exposes the
// same Health / Call / typed RPC surface and picks a backend per request.
//
// Tab IDs (Pool-only): callers see `<backendKey>:<daemonShortId>`. The default
// backend key is the 0-based constructor index as a decimal string ("0", "1",
// …); [NewPoolBackends] can set a stable name instead. Pool strips the prefix
// before talking to that daemon and prefixes TabNew, TabList, TabFocus, and
// bound-method results. Cross-daemon native short-id collisions are therefore
// distinct at this API. Malformed ids and unknown keys are rejected; routing
// is derived from the prefix. An explicit tab map records tabs created via
// TabNew or observed via TabList/TabFocus (for [Pool.ClientForTab],
// [Pool.ForgetTab], and close/unbind).
//
// Tab affinity (hard invariant): a tab lives on exactly one daemon. Later
// tab-scoped RPCs (Eval, Goto, …) go only to the backend named by the prefix.
// The pool never retries or forwards a pinned tab to a different daemon, even
// if the owner is down.
//
// Policy
//
//   - Startup: [NewPool] does not probe daemons. Construction succeeds even if
//     some or all backends are currently down (they are skipped later).
//   - Unbound requests (health checks, tab_new, and Call for methods without a
//     tab id, other than tab_list / tab_focus): pick the backend with the
//     fewest in-flight calls (round-robin among ties). On any error, try the
//     next backend rather than failing the whole pool. If the caller context
//     is done, the walk stops. If every backend fails, the error is
//     [*AllFailedError]. Failover here never moves an existing tab; tab_new
//     creates a new tab on the backend that succeeds.
//   - tab_list: query every backend in constructor order, prefix each id, and
//     merge. A down backend is skipped (its tabs are omitted). If every
//     backend fails, the error is [*AllFailedError]. Listed ids are bound so
//     later tab ops can use them. There is no pool-wide focus: Tab and Focus
//     are the first non-empty values in constructor order.
//   - tab_focus: first successful backend in constructor order (skip down);
//     return prefixed ids. Not a pool-wide focus.
//   - Bound requests: parse the prefix, then lookup in the tab map. Unknown
//     tab ids return [*UnknownTabError]; malformed / unknown-prefix ids return
//     [*InvalidTabIDError] (no silent fallback).
type Pool struct {
	clients  []*Client
	keys     []string
	byKey    map[string]int
	inFlight []atomic.Int64
	rr       atomic.Uint64

	mu   sync.Mutex
	tabs map[string]int // external tab id → backend that opened it
}

// PoolBackend is one named member of a [Pool]. Key is the prefix in external
// tab ids (`<key>:<daemonShortId>`). If Key is empty, [NewPoolBackends] uses
// the 0-based index as a decimal string. Keys must be unique and must not
// contain ':'.
type PoolBackend struct {
	Key    string
	Client *Client
}

// NewPool wraps the given daemon clients. Each [Client] has its own BaseURL and
// headers (use [WithHeader] / [WithHeaders] for per-daemon credentials such as
// Cloudflare Access). Order is preserved for [Pool.Clients]. Backend keys
// default to "0", "1", … in that order.
func NewPool(clients ...*Client) (*Pool, error) {
	backends := make([]PoolBackend, len(clients))
	for i, c := range clients {
		backends[i] = PoolBackend{Client: c}
	}
	return NewPoolBackends(backends...)
}

// NewPoolBackends is [NewPool] with explicit backend keys for Pool tab ids.
func NewPoolBackends(backends ...PoolBackend) (*Pool, error) {
	if len(backends) == 0 {
		return nil, ErrEmptyPool
	}
	clients := make([]*Client, len(backends))
	keys := make([]string, len(backends))
	byKey := make(map[string]int, len(backends))
	for i, b := range backends {
		if b.Client == nil {
			return nil, fmt.Errorf("daemonclient: pool client %d is nil", i)
		}
		if strings.TrimSpace(b.Client.BaseURL) == "" {
			return nil, fmt.Errorf("daemonclient: pool client %d has empty BaseURL", i)
		}
		key := strings.TrimSpace(b.Key)
		if key == "" {
			key = strconv.Itoa(i)
		}
		if strings.Contains(key, ":") {
			return nil, fmt.Errorf("daemonclient: pool backend key %q must not contain ':'", key)
		}
		if _, dup := byKey[key]; dup {
			return nil, fmt.Errorf("daemonclient: duplicate pool backend key %q", key)
		}
		clients[i] = b.Client
		keys[i] = key
		byKey[key] = i
	}
	return &Pool{
		clients:  clients,
		keys:     keys,
		byKey:    byKey,
		inFlight: make([]atomic.Int64, len(clients)),
		tabs:     make(map[string]int),
	}, nil
}

// FormatTabID builds a Pool-facing tab id: `<backendKey>:<daemonShortId>`.
func FormatTabID(backendKey, daemonShortID string) string {
	return backendKey + ":" + daemonShortID
}

// SplitTabID parses a Pool-facing tab id. ok is false when tab is not
// `<backendKey>:<daemonShortId>` with both sides non-empty. The key is the
// substring before the first ':'; the rest is the daemon-native short id.
func SplitTabID(tab string) (backendKey, daemonShortID string, ok bool) {
	tab = strings.TrimSpace(tab)
	key, short, found := strings.Cut(tab, ":")
	if !found || key == "" || short == "" {
		return "", "", false
	}
	return key, short, true
}

// Len returns the number of backends.
func (p *Pool) Len() int {
	return len(p.clients)
}

// BackendKeys returns constructor-order keys used in Pool tab ids.
func (p *Pool) BackendKeys() []string {
	out := make([]string, len(p.keys))
	copy(out, p.keys)
	return out
}

// Clients returns the backends in constructor order. The slice and clients are
// shared with the pool (callers may set Headers / HTTP on a backend).
func (p *Pool) Clients() []*Client {
	return p.clients
}

// ClientForTab returns the backend pinned to tab, if any. tab must be a
// Pool-facing id (`<backendKey>:<daemonShortId>`) created via TabNew or
// observed via TabList / TabFocus.
func (p *Pool) ClientForTab(tab string) (*Client, bool) {
	idx, ok := p.lookupTab(tab)
	if !ok {
		return nil, false
	}
	return p.clients[idx], true
}

// ForgetTab drops tab affinity without calling tab_close (for example after a
// daemon-side idle close). Subsequent ops for tab return [*UnknownTabError].
func (p *Pool) ForgetTab(tab string) {
	p.unbind(tab)
}

// Health succeeds if at least one backend's [Client.Health] succeeds.
func (p *Pool) Health(ctx context.Context) error {
	_, err := p.HealthResult(ctx)
	return err
}

// HealthResult returns the first successful backend health body.
func (p *Pool) HealthResult(ctx context.Context) (protocol.HealthResult, error) {
	var last protocol.HealthResult
	err := p.walkUnbound(ctx, "health", func(ctx context.Context, c *Client) error {
		var err error
		last, err = c.HealthResult(ctx)
		return err
	})
	if err != nil {
		return protocol.HealthResult{}, err
	}
	return last, nil
}

// Ready succeeds if at least one backend's [Client.Ready] succeeds.
func (p *Pool) Ready(ctx context.Context) error {
	_, err := p.ReadyResult(ctx)
	return err
}

func (p *Pool) ReadyResult(ctx context.Context) (protocol.HealthResult, error) {
	var last protocol.HealthResult
	err := p.walkUnbound(ctx, "ready", func(ctx context.Context, c *Client) error {
		var err error
		last, err = c.ReadyResult(ctx)
		return err
	})
	if err != nil {
		return protocol.HealthResult{}, err
	}
	return last, nil
}

// Live succeeds if at least one backend's [Client.Live] succeeds.
func (p *Pool) Live(ctx context.Context) error {
	return p.walkUnbound(ctx, "live", func(ctx context.Context, c *Client) error {
		return c.Live(ctx)
	})
}

// Call routes a JSON-RPC method the same way typed Pool methods do.
func (p *Pool) Call(ctx context.Context, method string, params any, result any) error {
	tab := tabIDFromParams(params)
	switch method {
	case protocol.MethodTabNew:
		return p.callCreate(ctx, method, params, result)
	case protocol.MethodTabClose:
		return p.callClose(ctx, tab, method, params, result)
	case protocol.MethodTabList:
		return p.callTabList(ctx, params, result)
	case protocol.MethodTabFocus:
		return p.callTabFocus(ctx, params, result)
	}
	if tab != "" {
		return p.callBound(ctx, tab, method, params, result)
	}
	if methodRequiresTab(method) {
		return ErrTabRequired
	}
	return p.callUnbound(ctx, method, params, result)
}

func (p *Pool) callCreate(ctx context.Context, method string, params any, result any) error {
	if err := checkCreateResult(result); err != nil {
		return err
	}
	var lastErrs []error
	for _, i := range p.order() {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Decode internally so bind does not depend on the caller's result pointer
		// (nil, map, or a typed struct are all valid Client.Call shapes).
		var raw json.RawMessage
		hold := p.track(i)
		err := p.clients[i].Call(ctx, method, params, &raw)
		hold()
		if err != nil {
			lastErrs = append(lastErrs, err)
			continue
		}
		native := tabFromRawResult(raw)
		if native == "" {
			lastErrs = append(lastErrs, fmt.Errorf("daemonclient: tab_new returned empty tab id"))
			continue
		}
		ext := FormatTabID(p.keys[i], native)
		if err := p.bind(ext, i); err != nil {
			p.abandonCreated(ctx, i, native)
			lastErrs = append(lastErrs, err)
			continue
		}
		if result != nil {
			raw = setJSONTab(raw, ext)
			if err := json.Unmarshal(raw, result); err != nil {
				p.unbind(ext)
				p.abandonCreated(ctx, i, native)
				return fmt.Errorf("daemonclient: decode tab_new result: %w", err)
			}
		}
		return nil
	}
	return &AllFailedError{Op: method, Errs: lastErrs}
}

func (p *Pool) callClose(ctx context.Context, tab, method string, params any, result any) error {
	if tab == "" {
		return ErrTabRequired
	}
	err := p.callBound(ctx, tab, method, params, result)
	if err == nil {
		p.unbind(tab)
	}
	return err
}

func (p *Pool) callBound(ctx context.Context, tab, method string, params any, result any) error {
	idx, short, err := p.resolveTab(tab)
	if err != nil {
		return err
	}
	stripped, err := replaceJSONTab(params, short)
	if err != nil {
		return err
	}
	hold := p.track(idx)
	defer hold()
	if result == nil {
		return p.clients[idx].Call(ctx, method, stripped, nil)
	}
	var raw json.RawMessage
	if err := p.clients[idx].Call(ctx, method, stripped, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(setJSONTab(raw, tab), result); err != nil {
		return fmt.Errorf("daemonclient: decode %s result: %w", method, err)
	}
	return nil
}

func (p *Pool) callTabList(ctx context.Context, params any, result any) error {
	merged := protocol.TabListResult{Tabs: []protocol.TabListItem{}}
	var lastErrs []error
	anyOK := false
	for i := range p.clients {
		if err := ctx.Err(); err != nil {
			return err
		}
		var one protocol.TabListResult
		hold := p.track(i)
		err := p.clients[i].Call(ctx, protocol.MethodTabList, params, &one)
		hold()
		if err != nil {
			lastErrs = append(lastErrs, err)
			continue
		}
		anyOK = true
		one = prefixTabListResult(p.keys[i], one)
		ids := append([]string{one.Tab, one.Focus}, tabIDsFromList(one)...)
		p.bindObservedTabs(i, ids...)
		merged.Tabs = append(merged.Tabs, one.Tabs...)
		if one.Seq > merged.Seq {
			merged.Seq = one.Seq
		}
		if merged.Tab == "" && one.Tab != "" {
			merged.Tab = one.Tab
		}
		if merged.Focus == "" && one.Focus != "" {
			merged.Focus = one.Focus
		}
	}
	if !anyOK {
		return &AllFailedError{Op: protocol.MethodTabList, Errs: lastErrs}
	}
	return copyResult(result, merged)
}

func (p *Pool) callTabFocus(ctx context.Context, params any, result any) error {
	var lastErrs []error
	for i := range p.clients {
		if err := ctx.Err(); err != nil {
			return err
		}
		var one protocol.TabFocusResult
		hold := p.track(i)
		err := p.clients[i].Call(ctx, protocol.MethodTabFocus, params, &one)
		hold()
		if err != nil {
			lastErrs = append(lastErrs, err)
			continue
		}
		one.Tab = prefixPoolTab(p.keys[i], one.Tab)
		one.Focus = prefixPoolTab(p.keys[i], one.Focus)
		p.bindObservedTabs(i, one.Tab, one.Focus)
		return copyResult(result, one)
	}
	return &AllFailedError{Op: protocol.MethodTabFocus, Errs: lastErrs}
}

func (p *Pool) callUnbound(ctx context.Context, method string, params any, result any) error {
	var lastErrs []error
	for _, i := range p.order() {
		if err := ctx.Err(); err != nil {
			return err
		}
		hold := p.track(i)
		err := p.clients[i].Call(ctx, method, params, result)
		hold()
		if err != nil {
			lastErrs = append(lastErrs, err)
			continue
		}
		return nil
	}
	return &AllFailedError{Op: method, Errs: lastErrs}
}

func (p *Pool) walkUnbound(ctx context.Context, op string, fn func(context.Context, *Client) error) error {
	var lastErrs []error
	for _, i := range p.order() {
		if err := ctx.Err(); err != nil {
			return err
		}
		hold := p.track(i)
		err := fn(ctx, p.clients[i])
		hold()
		if err != nil {
			lastErrs = append(lastErrs, err)
			continue
		}
		return nil
	}
	return &AllFailedError{Op: op, Errs: lastErrs}
}

func (p *Pool) track(i int) func() {
	p.inFlight[i].Add(1)
	return func() { p.inFlight[i].Add(-1) }
}

// order returns backend indexes: least in-flight first, round-robin among ties,
// then the remaining backends in ring order (failover walk).
func (p *Pool) order() []int {
	n := len(p.clients)
	start := int(p.rr.Add(1)-1) % n
	best := start
	bestN := p.inFlight[start].Load()
	for i := 1; i < n; i++ {
		idx := (start + i) % n
		if v := p.inFlight[idx].Load(); v < bestN {
			best, bestN = idx, v
		}
	}
	out := make([]int, n)
	for i := 0; i < n; i++ {
		out[i] = (best + i) % n
	}
	return out
}

// bind records external tab → idx. It is a no-op overwrite when the same
// backend already owns tab, and returns [*TabCollisionError] if a different
// backend does (defensive: prefixed ids make cross-daemon collisions
// impossible at this layer).
func (p *Pool) bind(tab string, idx int) error {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return fmt.Errorf("daemonclient: empty tab id")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if prev, ok := p.tabs[tab]; ok && prev != idx {
		return &TabCollisionError{Tab: tab}
	}
	p.tabs[tab] = idx
	return nil
}

// abandonCreated closes a tab on idx without touching the pool map (the id may
// already belong to another backend). Best-effort: a cancelled caller context
// must not skip cleanup. tab is the daemon-native short id.
func (p *Pool) abandonCreated(ctx context.Context, idx int, tab string) {
	tab = strings.TrimSpace(tab)
	if tab == "" || idx < 0 || idx >= len(p.clients) {
		return
	}
	hold := p.track(idx)
	defer hold()
	_, _ = p.clients[idx].TabClose(context.WithoutCancel(ctx), protocol.TabCloseParams{Tab: tab})
}

func (p *Pool) unbind(tab string) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return
	}
	p.mu.Lock()
	delete(p.tabs, tab)
	p.mu.Unlock()
}

func (p *Pool) lookupTab(tab string) (int, bool) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	idx, ok := p.tabs[tab]
	return idx, ok
}

// resolveTab parses a Pool-facing tab id, checks the prefix against this pool,
// and requires the id to be in the created-tab map. The prefix selects the
// backend (source of truth); the map is "created via this pool".
func (p *Pool) resolveTab(tab string) (idx int, short string, err error) {
	tab = strings.TrimSpace(tab)
	key, short, ok := SplitTabID(tab)
	if !ok {
		return 0, "", &InvalidTabIDError{Tab: tab, Reason: "malformed; want <backendKey>:<daemonShortId>"}
	}
	idx, ok = p.byKey[key]
	if !ok {
		return 0, "", &InvalidTabIDError{Tab: tab, Reason: fmt.Sprintf("unknown backend key %q", key)}
	}
	mapped, inMap := p.lookupTab(tab)
	if !inMap {
		return 0, "", &UnknownTabError{Tab: tab}
	}
	if mapped != idx {
		// Prefix wins if the map ever disagrees (should not happen).
		return idx, short, nil
	}
	return idx, short, nil
}

func tabIDFromParams(params any) string {
	return jsonTabField(params)
}

func tabFromRawResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var peek struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return ""
	}
	return strings.TrimSpace(peek.Tab)
}

func jsonTabField(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return tabFromRawResult(b)
}

func replaceJSONTab(params any, short string) (any, error) {
	if params == nil {
		return map[string]any{"tab": short}, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode params: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	m["tab"] = short
	return m, nil
}

func prefixPoolTab(backendKey, daemonShortID string) string {
	daemonShortID = strings.TrimSpace(daemonShortID)
	if daemonShortID == "" {
		return ""
	}
	return FormatTabID(backendKey, daemonShortID)
}

func prefixTabListResult(key string, r protocol.TabListResult) protocol.TabListResult {
	r.Tab = prefixPoolTab(key, r.Tab)
	r.Focus = prefixPoolTab(key, r.Focus)
	if r.Tabs == nil {
		r.Tabs = []protocol.TabListItem{}
		return r
	}
	out := make([]protocol.TabListItem, len(r.Tabs))
	for i, item := range r.Tabs {
		item.Tab = prefixPoolTab(key, item.Tab)
		out[i] = item
	}
	r.Tabs = out
	return r
}

func tabIDsFromList(r protocol.TabListResult) []string {
	ids := make([]string, 0, len(r.Tabs))
	for _, item := range r.Tabs {
		ids = append(ids, item.Tab)
	}
	return ids
}

func (p *Pool) bindObservedTabs(idx int, tabs ...string) {
	for _, tab := range tabs {
		tab = strings.TrimSpace(tab)
		if tab == "" {
			continue
		}
		_ = p.bind(tab, idx)
	}
}

func copyResult(dst any, src any) error {
	if dst == nil {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("daemonclient: decode result: %w", err)
	}
	return nil
}

func setJSONTab(raw json.RawMessage, tab string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return raw
	}
	m["tab"] = tab
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// checkCreateResult rejects result shapes that cannot hold a tab_new object
// before any RPC is sent, so a successful create is never left unbound.
func checkCreateResult(result any) error {
	if result == nil {
		return nil
	}
	rv := reflect.ValueOf(result)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ErrUnusableTabNewResult
	}
	probe := reflect.New(rv.Type().Elem()).Interface()
	if err := json.Unmarshal([]byte(`{"tab":"x","seq":1}`), probe); err != nil {
		return fmt.Errorf("%w: %v", ErrUnusableTabNewResult, err)
	}
	return nil
}
