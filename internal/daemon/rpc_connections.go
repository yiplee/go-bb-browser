package daemon

import (
	"context"
	"encoding/json"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// These narrow capability interfaces keep handlers independent from the concrete
// chromedp session and preserve compatibility with embedders and test doubles.
type tabConn interface {
	PageTargets() ([]*target.Info, error)
	CreatePageTarget(initialURL string, silent bool) (target.ID, error)
	CloseTarget(id target.ID) error
	Navigate(tabID target.ID, url string) error
	Reload(tabID target.ID) error
	Screenshot(tabID target.ID, format string) ([]byte, string, error)
	Eval(tabID target.ID, script string) (json.RawMessage, error)
	Click(tabID target.ID, selector string) error
	Fill(tabID target.ID, selector, text string) error
}

type fetchConn interface {
	FetchPage(tabID target.ID, rawURL, method string, headersJSON []byte, body string) (json.RawMessage, error)
}

type snapshotConn interface {
	Snapshot(tabID target.ID, opts browser.SnapshotOpts) (title, pageURL, text string, refs map[string]string, err error)
}

type routeConn interface {
	AppendNetworkRoute(tabID target.ID, rule browser.NetworkRouteRule) error
	RemoveNetworkRoutes(tabID target.ID, urlPattern string) error
	NetworkRouteCount(tabID target.ID) int
}

type pageTargetsContextConn interface {
	PageTargetsContext(context.Context) ([]*target.Info, error)
}
type createPageTargetContextConn interface {
	CreatePageTargetContext(context.Context, string, bool) (target.ID, error)
}
type closeTargetContextConn interface {
	CloseTargetContext(context.Context, target.ID) error
}
type navigateContextConn interface {
	NavigateContext(context.Context, target.ID, string) error
}
type reloadContextConn interface {
	ReloadContext(context.Context, target.ID) error
}
type screenshotContextConn interface {
	ScreenshotContext(context.Context, target.ID, string) ([]byte, string, error)
}
type evalContextConn interface {
	EvalContext(context.Context, target.ID, string) (json.RawMessage, error)
}
type clickContextConn interface {
	ClickContext(context.Context, target.ID, string) error
}
type fillContextConn interface {
	FillContext(context.Context, target.ID, string, string) error
}
type fetchContextConn interface {
	FetchPageContext(context.Context, target.ID, string, string, []byte, string) (json.RawMessage, error)
}
type snapshotContextConn interface {
	SnapshotContext(context.Context, target.ID, browser.SnapshotOpts) (string, string, string, map[string]string, error)
}
type appendNetworkRouteContextConn interface {
	AppendNetworkRouteContext(context.Context, target.ID, browser.NetworkRouteRule) error
}
type removeNetworkRoutesContextConn interface {
	RemoveNetworkRoutesContext(context.Context, target.ID, string) error
}

func tabLockTimeoutData() *protocol.ErrData {
	return &protocol.ErrData{Error: "tab operation lock deadline exceeded", Hint: "another operation is still running for this tab"}
}

func pageTargets(ctx context.Context, conn tabConn) ([]*target.Info, error) {
	if c, ok := conn.(pageTargetsContextConn); ok {
		return c.PageTargetsContext(ctx)
	}
	return conn.PageTargets()
}

func createPageTarget(ctx context.Context, conn tabConn, url string, silent bool) (target.ID, error) {
	if c, ok := conn.(createPageTargetContextConn); ok {
		return c.CreatePageTargetContext(ctx, url, silent)
	}
	return conn.CreatePageTarget(url, silent)
}

func closeTarget(ctx context.Context, conn tabConn, id target.ID) error {
	if c, ok := conn.(closeTargetContextConn); ok {
		return c.CloseTargetContext(ctx, id)
	}
	return conn.CloseTarget(id)
}

func navigate(ctx context.Context, conn tabConn, id target.ID, url string) error {
	if c, ok := conn.(navigateContextConn); ok {
		return c.NavigateContext(ctx, id, url)
	}
	return conn.Navigate(id, url)
}

func reload(ctx context.Context, conn tabConn, id target.ID) error {
	if c, ok := conn.(reloadContextConn); ok {
		return c.ReloadContext(ctx, id)
	}
	return conn.Reload(id)
}

func screenshot(ctx context.Context, conn tabConn, id target.ID, format string) ([]byte, string, error) {
	if c, ok := conn.(screenshotContextConn); ok {
		return c.ScreenshotContext(ctx, id, format)
	}
	return conn.Screenshot(id, format)
}

func eval(ctx context.Context, conn tabConn, id target.ID, script string) (json.RawMessage, error) {
	if c, ok := conn.(evalContextConn); ok {
		return c.EvalContext(ctx, id, script)
	}
	return conn.Eval(id, script)
}

func click(ctx context.Context, conn tabConn, id target.ID, selector string) error {
	if c, ok := conn.(clickContextConn); ok {
		return c.ClickContext(ctx, id, selector)
	}
	return conn.Click(id, selector)
}

func fill(ctx context.Context, conn tabConn, id target.ID, selector, text string) error {
	if c, ok := conn.(fillContextConn); ok {
		return c.FillContext(ctx, id, selector, text)
	}
	return conn.Fill(id, selector, text)
}

func fetchPage(ctx context.Context, conn fetchConn, id target.ID, url, method string, headers []byte, body string) (json.RawMessage, error) {
	if c, ok := conn.(fetchContextConn); ok {
		return c.FetchPageContext(ctx, id, url, method, headers, body)
	}
	return conn.FetchPage(id, url, method, headers, body)
}

func snapshot(ctx context.Context, conn snapshotConn, id target.ID, opts browser.SnapshotOpts) (string, string, string, map[string]string, error) {
	if c, ok := conn.(snapshotContextConn); ok {
		return c.SnapshotContext(ctx, id, opts)
	}
	return conn.Snapshot(id, opts)
}

func appendNetworkRoute(ctx context.Context, conn routeConn, id target.ID, rule browser.NetworkRouteRule) error {
	if c, ok := conn.(appendNetworkRouteContextConn); ok {
		return c.AppendNetworkRouteContext(ctx, id, rule)
	}
	return conn.AppendNetworkRoute(id, rule)
}

func removeNetworkRoutes(ctx context.Context, conn routeConn, id target.ID, pattern string) error {
	if c, ok := conn.(removeNetworkRoutesContextConn); ok {
		return c.RemoveNetworkRoutesContext(ctx, id, pattern)
	}
	return conn.RemoveNetworkRoutes(id, pattern)
}

// lockTab serializes CDP operations per short tab id. Entries are reference-counted
// so target removal can forget them without racing in-flight operations.
func (s *Server) lockTab(ctx context.Context, short string) (func(), bool) {
	if short == "" {
		return func() {}, true
	}
	s.tabMuOps.Lock()
	if s.tabCDPLocks == nil {
		s.tabCDPLocks = make(map[string]*tabLockEntry)
	}
	ent, ok := s.tabCDPLocks[short]
	if !ok {
		ent = &tabLockEntry{sem: make(chan struct{}, 1)}
		ent.sem <- struct{}{}
		s.tabCDPLocks[short] = ent
	}
	ent.refs++
	s.tabMuOps.Unlock()
	select {
	case <-ent.sem:
		return func() {
			ent.sem <- struct{}{}
			s.releaseTabLockRef(short, ent)
		}, true
	case <-ctx.Done():
		s.releaseTabLockRef(short, ent)
		return func() {}, false
	}
}

type tabLockEntry struct {
	sem       chan struct{}
	refs      int
	forgotten bool
}

func (s *Server) releaseTabLockRef(short string, ent *tabLockEntry) {
	s.tabMuOps.Lock()
	defer s.tabMuOps.Unlock()
	ent.refs--
	if ent.refs == 0 && ent.forgotten && s.tabCDPLocks[short] == ent {
		delete(s.tabCDPLocks, short)
	}
}

func (s *Server) forgetTabLock(short string) {
	if short == "" {
		return
	}
	s.tabMuOps.Lock()
	defer s.tabMuOps.Unlock()
	ent := s.tabCDPLocks[short]
	if ent == nil {
		return
	}
	ent.forgotten = true
	if ent.refs == 0 {
		delete(s.tabCDPLocks, short)
	}
}
