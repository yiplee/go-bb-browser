package daemonclient

import (
	"context"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func (p *Pool) TabList(ctx context.Context, params protocol.TabListParams) (protocol.TabListResult, error) {
	return callTyped[protocol.TabListParams, protocol.TabListResult](ctx, p, protocol.MethodTabList, params)
}

func (p *Pool) TabFocus(ctx context.Context, params protocol.TabFocusParams) (protocol.TabFocusResult, error) {
	return callTyped[protocol.TabFocusParams, protocol.TabFocusResult](ctx, p, protocol.MethodTabFocus, params)
}

func (p *Pool) TabSelect(ctx context.Context, params protocol.TabSelectParams) (protocol.TabSelectResult, error) {
	return callTyped[protocol.TabSelectParams, protocol.TabSelectResult](ctx, p, protocol.MethodTabSelect, params)
}

func (p *Pool) TabNew(ctx context.Context, params protocol.TabNewParams) (protocol.TabNewResult, error) {
	return callTyped[protocol.TabNewParams, protocol.TabNewResult](ctx, p, protocol.MethodTabNew, params)
}

func (p *Pool) Goto(ctx context.Context, params protocol.GotoParams) (protocol.GotoResult, error) {
	return callTyped[protocol.GotoParams, protocol.GotoResult](ctx, p, protocol.MethodGoto, params)
}

func (p *Pool) Reload(ctx context.Context, params protocol.ReloadParams) (protocol.ReloadResult, error) {
	return callTyped[protocol.ReloadParams, protocol.ReloadResult](ctx, p, protocol.MethodReload, params)
}

func (p *Pool) TabClose(ctx context.Context, params protocol.TabCloseParams) (protocol.TabCloseResult, error) {
	return callTyped[protocol.TabCloseParams, protocol.TabCloseResult](ctx, p, protocol.MethodTabClose, params)
}

func (p *Pool) Screenshot(ctx context.Context, params protocol.ScreenshotParams) (protocol.ScreenshotResult, error) {
	return callTyped[protocol.ScreenshotParams, protocol.ScreenshotResult](ctx, p, protocol.MethodScreenshot, params)
}

func (p *Pool) Eval(ctx context.Context, params protocol.EvalParams) (protocol.EvalResult, error) {
	return callTyped[protocol.EvalParams, protocol.EvalResult](ctx, p, protocol.MethodEval, params)
}

func (p *Pool) Click(ctx context.Context, params protocol.ClickParams) (protocol.ClickResult, error) {
	return callTyped[protocol.ClickParams, protocol.ClickResult](ctx, p, protocol.MethodClick, params)
}

func (p *Pool) Fill(ctx context.Context, params protocol.FillParams) (protocol.FillResult, error) {
	return callTyped[protocol.FillParams, protocol.FillResult](ctx, p, protocol.MethodFill, params)
}

func (p *Pool) Network(ctx context.Context, params protocol.ObsQueryParams) (protocol.ObsQueryResult, error) {
	return callTyped[protocol.ObsQueryParams, protocol.ObsQueryResult](ctx, p, protocol.MethodNetwork, params)
}

func (p *Pool) Console(ctx context.Context, params protocol.ObsQueryParams) (protocol.ObsQueryResult, error) {
	return callTyped[protocol.ObsQueryParams, protocol.ObsQueryResult](ctx, p, protocol.MethodConsole, params)
}

func (p *Pool) Errors(ctx context.Context, params protocol.ObsQueryParams) (protocol.ObsQueryResult, error) {
	return callTyped[protocol.ObsQueryParams, protocol.ObsQueryResult](ctx, p, protocol.MethodErrors, params)
}

func (p *Pool) Fetch(ctx context.Context, params protocol.FetchParams) (protocol.FetchResult, error) {
	return callTyped[protocol.FetchParams, protocol.FetchResult](ctx, p, protocol.MethodFetch, params)
}

func (p *Pool) Snapshot(ctx context.Context, params protocol.SnapshotParams) (protocol.SnapshotResult, error) {
	return callTyped[protocol.SnapshotParams, protocol.SnapshotResult](ctx, p, protocol.MethodSnapshot, params)
}

func (p *Pool) NetworkRoute(ctx context.Context, params protocol.NetworkRouteParams) (protocol.NetworkRouteResult, error) {
	return callTyped[protocol.NetworkRouteParams, protocol.NetworkRouteResult](ctx, p, protocol.MethodNetworkRoute, params)
}

func (p *Pool) NetworkUnroute(ctx context.Context, params protocol.NetworkUnrouteParams) (protocol.NetworkUnrouteResult, error) {
	return callTyped[protocol.NetworkUnrouteParams, protocol.NetworkUnrouteResult](ctx, p, protocol.MethodNetworkUnroute, params)
}

func (p *Pool) NetworkClear(ctx context.Context, params protocol.NetworkClearParams) (protocol.NetworkClearResult, error) {
	return callTyped[protocol.NetworkClearParams, protocol.NetworkClearResult](ctx, p, protocol.MethodNetworkClear, params)
}

func (p *Pool) ConsoleClear(ctx context.Context, params protocol.ConsoleClearParams) (protocol.ConsoleClearResult, error) {
	return callTyped[protocol.ConsoleClearParams, protocol.ConsoleClearResult](ctx, p, protocol.MethodConsoleClear, params)
}

func (p *Pool) ErrorsClear(ctx context.Context, params protocol.ErrorsClearParams) (protocol.ErrorsClearResult, error) {
	return callTyped[protocol.ErrorsClearParams, protocol.ErrorsClearResult](ctx, p, protocol.MethodErrorsClear, params)
}
