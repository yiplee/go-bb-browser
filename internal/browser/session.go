package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

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
	if err := chromedp.Run(ctx); err != nil {
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
