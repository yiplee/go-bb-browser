package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// Pool spreads JSON-RPC calls across multiple [Client] backends (one bb-daemon
// each). The existing single-daemon [Client] API is unchanged; Pool exposes the
// same Health / Call / typed RPC surface and picks a backend per request.
//
// Tab affinity (hard invariant): a tab lives on exactly one daemon. [Pool]
// records tab id → backend on successful tab_new and deletes that entry on
// successful tab_close. Every later tab-scoped RPC (Eval, Goto, …) is sent
// only to that backend. The pool never retries or forwards a pinned tab to a
// different daemon, even if the owner is down. tab_list / tab_focus do not
// write or rewrite this map. Short ids are unique per daemon only; bind never
// overwrites an existing owner. A colliding tab_new is closed on the creating
// daemon and treated as a failed create (failover may try another backend).
//
// Policy
//
//   - Startup: [NewPool] does not probe daemons. Construction succeeds even if
//     some or all backends are currently down (they are skipped later).
//   - Unbound requests (health checks, tab_new, tab_list, tab_focus, and Call
//     for methods without a tab id): pick the backend with the fewest in-flight
//     calls (round-robin among ties). On any error, try the next backend rather
//     than failing the whole pool. If the caller context is done, the walk
//     stops. If every backend fails, the error is [*AllFailedError]. Failover
//     here never moves an existing tab; tab_new creates a new tab on the
//     backend that succeeds.
//   - Bound requests: lookup in the tab map only. Unknown tab ids return
//     [*UnknownTabError] (no silent fallback).
type Pool struct {
	clients  []*Client
	inFlight []atomic.Int64
	rr       atomic.Uint64

	mu   sync.Mutex
	tabs map[string]int // tab id → backend that opened it; written by tab_new, deleted by tab_close
}

// NewPool wraps the given daemon clients. Each [Client] has its own BaseURL and
// headers (use [WithHeader] / [WithHeaders] for per-daemon credentials such as
// Cloudflare Access). Order is preserved for [Pool.Clients].
func NewPool(clients ...*Client) (*Pool, error) {
	if len(clients) == 0 {
		return nil, ErrEmptyPool
	}
	out := make([]*Client, len(clients))
	for i, c := range clients {
		if c == nil {
			return nil, fmt.Errorf("daemonclient: pool client %d is nil", i)
		}
		if strings.TrimSpace(c.BaseURL) == "" {
			return nil, fmt.Errorf("daemonclient: pool client %d has empty BaseURL", i)
		}
		out[i] = c
	}
	return &Pool{
		clients:  out,
		inFlight: make([]atomic.Int64, len(out)),
		tabs:     make(map[string]int),
	}, nil
}

// Len returns the number of backends.
func (p *Pool) Len() int {
	return len(p.clients)
}

// Clients returns the backends in constructor order. The slice and clients are
// shared with the pool (callers may set Headers / HTTP on a backend).
func (p *Pool) Clients() []*Client {
	return p.clients
}

// ClientForTab returns the backend pinned to tab, if any.
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
		tab := tabFromRawResult(raw)
		if tab == "" {
			lastErrs = append(lastErrs, fmt.Errorf("daemonclient: tab_new returned empty tab id"))
			continue
		}
		if err := p.bind(tab, i); err != nil {
			p.abandonCreated(ctx, i, tab)
			lastErrs = append(lastErrs, err)
			continue
		}
		if result != nil {
			if err := json.Unmarshal(raw, result); err != nil {
				p.unbind(tab)
				p.abandonCreated(ctx, i, tab)
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
	idx, ok := p.lookupTab(tab)
	if !ok {
		return &UnknownTabError{Tab: tab}
	}
	hold := p.track(idx)
	defer hold()
	// Owner only: never walk other backends (tabs are not forwarded).
	return p.clients[idx].Call(ctx, method, params, result)
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

// bind records tab → idx. It is a no-op overwrite when the same backend already
// owns tab, and returns [*TabCollisionError] when a different backend does.
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
// must not skip cleanup.
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
