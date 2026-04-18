package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
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

// Session wraps a chromedp remote allocator + browser context for tab-scoped CDP APIs.
// Only NewRemoteAllocator is used — the daemon never launches Chrome (see IMPLEMENTATION_PLAN §5.1).
type Session struct {
	ctx    context.Context
	cancel context.CancelFunc // cancels chromedp context then allocator

	obsMu     sync.Mutex
	observers map[target.ID]context.CancelFunc // per-page CDP listener lifetimes
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

// Context returns the browser-level chromedp context (lifetime matches the session).
func (s *Session) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.ctx
}

// Close releases per-tab observers then the CDP connection.
func (s *Session) Close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.obsMu.Lock()
	for _, cancel := range s.observers {
		cancel()
	}
	s.observers = nil
	s.obsMu.Unlock()
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

func (s *Session) tabCtx(tabID target.ID) (context.Context, context.CancelFunc) {
	return chromedp.NewContext(s.ctx, chromedp.WithTargetID(tabID))
}

// Screenshot captures the viewport; format is "png" (default) or "jpeg".
func (s *Session) Screenshot(tabID target.ID, format string) ([]byte, string, error) {
	if s == nil {
		return nil, "", fmt.Errorf("browser session is nil")
	}
	tabCtx, cancel := s.tabCtx(tabID)
	defer cancel()
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
	err := chromedp.Run(tabCtx, chromedp.CaptureScreenshot(&buf))
	return buf, "image/png", err
}

// Eval runs script in the page and returns JSON-marshalable result as RawMessage.
func (s *Session) Eval(tabID target.ID, script string) (json.RawMessage, error) {
	if s == nil {
		return nil, fmt.Errorf("browser session is nil")
	}
	tabCtx, cancel := s.tabCtx(tabID)
	defer cancel()
	var v interface{}
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(script, &v)); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// Click performs a click on the first node matching selector.
func (s *Session) Click(tabID target.ID, selector string) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	tabCtx, cancel := s.tabCtx(tabID)
	defer cancel()
	return chromedp.Run(tabCtx, chromedp.Click(selector, chromedp.ByQuery))
}

// Fill sets the value of an input/textarea (see chromedp.SetValue).
func (s *Session) Fill(tabID target.ID, selector, text string) error {
	if s == nil {
		return fmt.Errorf("browser session is nil")
	}
	tabCtx, cancel := s.tabCtx(tabID)
	defer cancel()
	return chromedp.Run(tabCtx, chromedp.SetValue(selector, text, chromedp.ByQuery))
}
