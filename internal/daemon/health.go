package daemon

import (
	"context"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func (s *Server) healthResult(strict bool) protocol.HealthResult {
	return protocol.HealthResult{
		Status:  "ok",
		Browser: s.browserHealthField(strict),
	}
}

func (s *Server) browserHealthField(strict bool) string {
	if s.SkipBrowserAttach {
		return protocol.HealthBrowserSkipped
	}
	// Test hooks have no supervisor; retain their lightweight PingBrowser behavior.
	if s.tabHook != nil {
		if p, ok := s.tabHook.(interface{ PingBrowser() error }); ok && p.PingBrowser() != nil {
			return protocol.HealthBrowserDisconnected
		}
		return protocol.HealthBrowserConnected
	}
	s.tabMu.RLock()
	state := s.browserState
	live := s.tabLive
	s.tabMu.RUnlock()
	if live == nil || state == sessionFailed || state == sessionStarting {
		return protocol.HealthBrowserDisconnected
	}
	if strict && state == sessionSuspect {
		return protocol.HealthBrowserDisconnected
	}
	return protocol.HealthBrowserConnected
}

// pingTabConnContext is retained for injected backends and focused tests. Production
// health routes use supervisor state and never call it.
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
	}
	return protocol.HealthBrowserConnected
}
