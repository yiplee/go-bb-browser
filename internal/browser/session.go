package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/yiplee/go-bb-browser/internal/state"
)

func errNilSession() error {
	return fmt.Errorf("browser session is nil")
}

func browserExecutor(ctx context.Context) cdp.Executor {
	c := chromedp.FromContext(ctx)
	if c == nil || c.Browser == nil {
		return nil
	}
	return c.Browser
}

// Session wraps a chromedp remote allocator + browser context for tab-scoped CDP APIs.
// Only NewRemoteAllocator is used — the daemon never launches Chrome (see IMPLEMENTATION_PLAN §5.1).
type Session struct {
	ctx    context.Context
	cancel context.CancelFunc // cancels chromedp context then allocator

	routeState *routeState

	obsMu     sync.Mutex
	observers map[target.ID]context.CancelFunc // per-page CDP listener lifetimes

	// tabPool holds one chromedp child context per page/tab target. Cancelling that child
	// (as chromedp.NewContext's cancel does) runs CloseTarget — so we must NOT defer-cancel
	// after each Navigate/Screenshot/etc. Reuse until pruneTabPool or CloseTarget drops it.
	poolMu  sync.Mutex
	tabPool map[target.ID]*tabPoolEntry
}

type tabPoolEntry struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// remoteAllocatorURL adapts debugger endpoints for chromedp.NewRemoteAllocator, which
// only accepts ws(s):// or http(s):// (bare host:port is not a valid URL for json/version).
func remoteAllocatorURL(debuggerURL string) string {
	s := strings.TrimSpace(debuggerURL)
	if s == "" || strings.Contains(s, "://") {
		return s
	}
	return "http://" + s
}

// httpDebuggerBase maps ws/wss debugger URLs to http(s) for the DevTools HTTP API (/json/list).
func httpDebuggerBase(debuggerURL string) string {
	s := remoteAllocatorURL(debuggerURL)
	switch {
	case strings.HasPrefix(s, "ws://"):
		return "http://" + strings.TrimPrefix(s, "ws://")
	case strings.HasPrefix(s, "wss://"):
		return "https://" + strings.TrimPrefix(s, "wss://")
	default:
		return s
	}
}

// firstExistingPageTabID returns the first page-like target id from GET /json/list, or ("", nil)
// if none exist (caller may let chromedp create a blank tab for INV-7).
func firstExistingPageTabID(ctx context.Context, debuggerBase string) (target.ID, error) {
	base := strings.TrimSpace(debuggerBase)
	if base == "" {
		return "", nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(base, "/")+"/json/list", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GET /json/list: HTTP %d", resp.StatusCode)
	}
	var entries []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return "", err
	}
	for _, e := range entries {
		switch e.Type {
		case "page", "tab":
			if e.ID != "" {
				return target.ID(e.ID), nil
			}
		}
	}
	return "", nil
}

// connectFailureHint adds short troubleshooting text for common attach failures.
func connectFailureHint(err error) string {
	if err == nil {
		return ""
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "eof"),
		strings.Contains(m, "connection refused"),
		strings.Contains(m, "connection reset"),
		strings.Contains(m, "no such host"),
		strings.Contains(m, "timeout"),
		strings.Contains(m, "timed out"),
		strings.Contains(m, "wsarefuse"): // Windows WSAECONNREFUSED text
		return " — is anything listening on that port? Quit Chrome completely, start it with --remote-debugging-port=9222, then open http://127.0.0.1:9222/json/version and confirm you see JSON (webSocketDebuggerUrl)."
	default:
		return ""
	}
}

// Connect opens a CDP session to an already-running Chrome instance.
// debuggerURL may be host:port, ws(s) URL, or http(s) URL (chromedp resolves to browser websocket).
func Connect(parent context.Context, debuggerURL string) (*Session, error) {
	if debuggerURL == "" {
		return nil, fmt.Errorf("debugger URL is empty")
	}
	base := remoteAllocatorURL(debuggerURL)
	attachID, err := firstExistingPageTabID(parent, httpDebuggerBase(debuggerURL))
	if err != nil {
		return nil, fmt.Errorf("devtools list targets: %w%s", err, connectFailureHint(err))
	}
	// Do not tie the allocator to parent cancellation (e.g. daemon SIGTERM): cancelling that
	// context makes chromedp.RemoteAllocator run Cancel → CloseTarget on open tabs. The
	// debugging Chrome process should keep running; TCP drops when this process exits.
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.WithoutCancel(parent), base)

	// First Run must use the long-lived chromedp ctx, not a child WithTimeout:
	// RemoteAllocator starts a goroutine on Allocate that waits on Run's ctx.Done()
	// then calls chromedp.Cancel — cancelling a timeout child after Connect returns
	// would tear down the browser session (symptom: later CDP ops return "context canceled").
	tryRun := func(targetID target.ID) (context.Context, context.CancelFunc, error) {
		var c context.Context
		var cancel context.CancelFunc
		if targetID != "" {
			// Attach to an existing page/tab; chromedp's default first Run otherwise
			// creates a new about:blank target (Target.createTarget).
			c, cancel = chromedp.NewContext(allocCtx, chromedp.WithTargetID(targetID))
		} else {
			c, cancel = chromedp.NewContext(allocCtx)
		}
		if err := chromedp.Run(c); err != nil {
			cancel()
			return nil, nil, err
		}
		return c, cancel, nil
	}

	ctx, ctxCancel, err := tryRun(attachID)
	if err != nil && attachID != "" && isStaleTargetErr(err) {
		// Some CDP servers (notably Obscura's /json/list) advertise a page-like
		// target that doesn't actually exist in their session map. Fall back to
		// letting chromedp create a fresh target via Target.createTarget so the
		// daemon can still attach.
		ctx, ctxCancel, err = tryRun("")
	}
	if err != nil {
		allocCancel()
		return nil, fmt.Errorf("cdp connect: %w%s", err, connectFailureHint(err))
	}
	cancel := func() {
		ctxCancel()
		allocCancel()
	}
	return &Session{ctx: ctx, cancel: cancel}, nil
}

// isStaleTargetErr matches CDP errors raised when attaching to a target id that the
// remote DevTools host claims exists but its session bookkeeping cannot resolve.
// Obscura reports "No page" / "No page for session" via its catch-all -32601 wrapper;
// real Chrome reports variants like "No target with given id" / "Target closed".
func isStaleTargetErr(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "no page"),
		strings.Contains(m, "no target"),
		strings.Contains(m, "target not found"),
		strings.Contains(m, "target closed"),
		strings.Contains(m, "no session with given id"),
		strings.Contains(m, "no such target"):
		return true
	default:
		return false
	}
}

// Context returns the browser-level chromedp context (lifetime matches the session).
func (s *Session) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.ctx
}

// Close drops references to CDP observers and the chromedp cancel func without invoking
// chromedp shutdown: chromedp.Cancel / child cancels run Target.CloseTarget on still-open
// tabs. The remote Chrome process should stay up; the OS closes the CDP TCP connection when
// this process exits.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.obsMu.Lock()
	s.observers = nil
	s.obsMu.Unlock()

	s.poolMu.Lock()
	s.tabPool = nil
	s.poolMu.Unlock()

	if s.cancel != nil {
		s.cancel = nil
	}
}

// PageTargets returns Target API targets filtered to page-like tabs.
func (s *Session) PageTargets() ([]*target.Info, error) {
	if s == nil {
		return nil, fmt.Errorf("browser session is nil")
	}
	all, err := chromedp.Targets(s.ctx)
	if err != nil {
		return nil, err
	}
	var pages []*target.Info
	for _, info := range all {
		if info == nil {
			continue
		}
		switch info.Type {
		case "page", "tab":
			pages = append(pages, info)
		default:
			continue
		}
	}
	present := make(map[target.ID]struct{}, len(pages))
	for _, info := range pages {
		present[info.TargetID] = struct{}{}
	}
	s.pruneTabPool(present)
	return pages, nil
}

// DetectForegroundShort returns the short id of the unique page target whose
// document.visibilityState is "visible". If zero or more than one target
// qualifies (typical with multiple visible browser windows), it returns "", false.
func (s *Session) DetectForegroundShort(snaps []state.TabSnapshot) (string, bool) {
	if s == nil || len(snaps) == 0 {
		return "", false
	}
	var picked string
	for _, sn := range snaps {
		if sn.TargetID == "" {
			continue
		}
		tabCtx, err := s.tabChromeCtx(sn.TargetID)
		if err != nil {
			continue
		}
		var vis string
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(`document.visibilityState`, &vis)); err != nil {
			continue
		}
		if vis != "visible" {
			continue
		}
		if picked != "" {
			return "", false
		}
		picked = sn.ShortID
	}
	if picked == "" {
		return "", false
	}
	return picked, true
}

func (s *Session) pruneTabPool(present map[target.ID]struct{}) {
	if s == nil {
		return
	}
	var drop []context.CancelFunc
	s.poolMu.Lock()
	for id, ent := range s.tabPool {
		if _, ok := present[id]; !ok {
			delete(s.tabPool, id)
			s.ClearRoutesForTarget(id)
			drop = append(drop, ent.cancel)
		}
	}
	s.poolMu.Unlock()
	for _, c := range drop {
		c()
	}
}

// tabChromeCtx returns a chromedp context attached to tabID, creating and caching it on first use.
func (s *Session) tabChromeCtx(tabID target.ID) (context.Context, error) {
	if s == nil {
		return nil, fmt.Errorf("browser session is nil")
	}
	if tabID == "" {
		return nil, fmt.Errorf("empty target id")
	}
	s.poolMu.Lock()
	if s.tabPool == nil {
		s.tabPool = make(map[target.ID]*tabPoolEntry)
	}
	if ent, ok := s.tabPool[tabID]; ok {
		s.poolMu.Unlock()
		return ent.ctx, nil
	}
	ctx, cancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(tabID))
	if err := chromedp.Run(ctx); err != nil {
		s.poolMu.Unlock()
		cancel()
		return nil, err
	}
	s.tabPool[tabID] = &tabPoolEntry{ctx: ctx, cancel: cancel}
	s.poolMu.Unlock()
	return ctx, nil
}

// CreatePageTarget opens a new page target (INV-7: works when zero tabs exist).
func (s *Session) CreatePageTarget(initialURL string) (target.ID, error) {
	if s == nil {
		return "", fmt.Errorf("browser session is nil")
	}
	ex := browserExecutor(s.ctx)
	if ex == nil {
		return "", fmt.Errorf("browser not available in context")
	}
	id, err := target.CreateTarget(initialURL).Do(cdp.WithExecutor(s.ctx, ex))
	if err != nil {
		return "", err
	}
	return id, nil
}

// CloseTarget closes a page target by CDP id.
func (s *Session) CloseTarget(id target.ID) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	var poolCancel context.CancelFunc
	s.poolMu.Lock()
	if ent, ok := s.tabPool[id]; ok {
		delete(s.tabPool, id)
		poolCancel = ent.cancel
	}
	s.poolMu.Unlock()
	if poolCancel != nil {
		poolCancel()
		return nil
	}
	ex := browserExecutor(s.ctx)
	if ex == nil {
		return fmt.Errorf("browser not available in context")
	}
	return target.CloseTarget(id).Do(cdp.WithExecutor(s.ctx, ex))
}

// Navigate navigates the given page target to url.
func (s *Session) Navigate(tabID target.ID, url string) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return err
	}
	return chromedp.Run(tabCtx, chromedp.Navigate(url))
}

// Reload performs a full navigation reload for the page target.
func (s *Session) Reload(tabID target.ID) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return err
	}
	return chromedp.Run(tabCtx, chromedp.Reload())
}

// Screenshot captures the viewport; format is "png" (default) or "jpeg".
func (s *Session) Screenshot(tabID target.ID, format string) ([]byte, string, error) {
	if s == nil {
		return nil, "", fmt.Errorf("browser session is nil")
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return nil, "", err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "jpeg" || format == "jpg" {
		var buf []byte
		err := chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatJpeg).
				WithQuality(85).
				Do(ctx)
			return err
		}))
		return buf, "image/jpeg", err
	}
	var buf []byte
	err = chromedp.Run(tabCtx, chromedp.CaptureScreenshot(&buf))
	return buf, "image/png", err
}

// Eval runs script in the page and returns JSON-marshalable result as RawMessage.
// awaitPromise is true so async expressions (including site adapters) resolve to
// JSON-serializable values instead of a Promise handle.
func (s *Session) Eval(tabID target.ID, script string) (json.RawMessage, error) {
	return s.EvalAwait(tabID, script, true)
}

// EvalAwait runs script; when awaitPromise is true, resolves Promises (same as bb-browser fetch).
func (s *Session) EvalAwait(tabID target.ID, script string, awaitPromise bool) (json.RawMessage, error) {
	if s == nil {
		return nil, errNilSession()
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = chromedp.Run(tabCtx, chromedp.Evaluate(script, &raw, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(awaitPromise).WithReturnByValue(true)
	}))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// Click performs a click on the first node matching selector.
func (s *Session) Click(tabID target.ID, selector string) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return err
	}
	return chromedp.Run(tabCtx, chromedp.Click(selector, chromedp.ByQuery))
}

// Fill sets the value of an input/textarea (see chromedp.SetValue).
func (s *Session) Fill(tabID target.ID, selector, text string) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return err
	}
	return chromedp.Run(tabCtx, chromedp.SetValue(selector, text, chromedp.ByQuery))
}
