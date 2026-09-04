package daemonclient

import (
	"context"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// caller is implemented by [Client] and [Pool].
type caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

func callTyped[P any, R any](ctx context.Context, c caller, method string, p P) (R, error) {
	var out R
	err := c.Call(ctx, method, p, &out)
	return out, err
}

// methodRequiresTab reports whether a JSON-RPC method is tab-scoped on a [Pool].
// tab_new is daemon-scoped (no affinity key). tab_list aggregates every
// backend; tab_focus uses the first healthy backend.
func methodRequiresTab(method string) bool {
	switch method {
	case protocol.MethodTabList, protocol.MethodTabFocus, protocol.MethodTabNew:
		return false
	case protocol.MethodTabSelect, protocol.MethodGoto, protocol.MethodReload,
		protocol.MethodTabClose, protocol.MethodScreenshot, protocol.MethodEval,
		protocol.MethodClick, protocol.MethodFill, protocol.MethodNetwork,
		protocol.MethodNetworkClear, protocol.MethodNetworkRoute,
		protocol.MethodNetworkUnroute, protocol.MethodFetch, protocol.MethodSnapshot,
		protocol.MethodConsole, protocol.MethodConsoleClear, protocol.MethodErrors,
		protocol.MethodErrorsClear:
		return true
	default:
		return false
	}
}
