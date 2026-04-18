package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func browserExecutor(ctx context.Context) cdp.Executor {
	c := chromedp.FromContext(ctx)
	if c == nil || c.Browser == nil {
		return nil
	}
	return c.Browser
}

const defaultConnectTimeout = 30 * time.Second

// Session wraps a chromedp remote allocator + browser context for Phase 1 tab APIs.
// Only NewRemoteAllocator is used — the daemon never launches Chrome (see IMPLEMENTATION_PLAN §5.1).
type Session struct {
	ctx    context.Context
	cancel context.CancelFunc // cancels chromedp context then allocator
}

// Connect opens a CDP session to an already-running Chrome instance.
// debuggerURL may be host:port, ws(s) URL, or http(s) URL (chromedp resolves to browser websocket).
func Connect(parent context.Context, debuggerURL string) (*Session, error) {
	if debuggerURL == "" {
		return nil, fmt.Errorf("debugger URL is empty")
	}
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(parent, debuggerURL)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	runCtx, cancelRun := context.WithTimeout(ctx, defaultConnectTimeout)
	defer cancelRun()
	if err := chromedp.Run(runCtx); err != nil {
		ctxCancel()
		allocCancel()
		return nil, fmt.Errorf("cdp connect: %w", err)
	}
	cancel := func() {
		ctxCancel()
		allocCancel()
	}
	return &Session{ctx: ctx, cancel: cancel}, nil
}

// Close releases the CDP connection.
func (s *Session) Close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
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
	return pages, nil
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
	tabCtx, cancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(tabID))
	defer cancel()
	return chromedp.Run(tabCtx, chromedp.Navigate(url))
}
