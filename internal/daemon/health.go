package daemon

import (
	"context"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

const browserHealthTimeout = 3 * time.Second

func (s *Server) healthResult(ctx context.Context) protocol.HealthResult {
	return protocol.HealthResult{
		Status:  "ok",
		Browser: s.browserHealthField(ctx),
	}
}

func (s *Server) browserHealthField(ctx context.Context) string {
	if s.SkipBrowserAttach {
		return protocol.HealthBrowserSkipped
	}
	if s.tabHook != nil {
		return pingTabConnContext(ctx, s.tabHook)
	}
	if err := s.ensureBrowserSession(ctx); err != nil {
		return protocol.HealthBrowserDisconnected
	}
	conn := s.tabConn()
	if conn == nil {
		return protocol.HealthBrowserDisconnected
	}
	if field := pingTabConnContext(ctx, conn); field == protocol.HealthBrowserConnected {
		s.markBrowserOK()
		return field
	}
	return protocol.HealthBrowserDisconnected
}

func pingTabConnContext(ctx context.Context, conn tabConn) string {
	if p, ok := conn.(interface{ PingBrowserContext(context.Context) error }); ok {
		if err := p.PingBrowserContext(ctx); err != nil {
			return protocol.HealthBrowserDisconnected
		}
		return protocol.HealthBrowserConnected
	}
	if p, ok := conn.(interface{ PingBrowser() error }); ok {
		if err := p.PingBrowser(); err != nil {
			return protocol.HealthBrowserDisconnected
		}
		return protocol.HealthBrowserConnected
	}
	if p, ok := conn.(interface {
		PageTargetsContext(context.Context) ([]*target.Info, error)
	}); ok {
		if _, err := p.PageTargetsContext(ctx); err != nil {
			return protocol.HealthBrowserDisconnected
		}
		return protocol.HealthBrowserConnected
	}
	if _, err := conn.PageTargets(); err != nil {
		return protocol.HealthBrowserDisconnected
	}
	return protocol.HealthBrowserConnected
}

func (s *Server) markBrowserOK() {
	s.tabMu.Lock()
	s.lastBrowserOK = time.Now()
	s.tabMu.Unlock()
}
