package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// Client talks to bb-browserd over HTTP (JSON-RPC POST /v1).
type Client struct {
	BaseURL string
	HTTP    *http.Client

	id atomic.Uint64
}

// NewClient returns a client for the given daemon root URL (e.g. http://127.0.0.1:8080).
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTP:    nil,
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) nextID() json.RawMessage {
	n := c.id.Add(1)
	b, err := json.Marshal(n)
	if err != nil {
		// uint64 always marshals
		return json.RawMessage("1")
	}
	return json.RawMessage(b)
}

// Health checks GET /health.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	return nil
}

// Call performs a single JSON-RPC request on POST /v1. result must be a non-nil pointer
// to decode the result field on success; if nil, the result payload is ignored when there is no error.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	var paramsJSON json.RawMessage
	if params == nil {
		paramsJSON = []byte("{}")
	} else {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		paramsJSON = b
	}

	reqObj := protocol.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      c.nextID(),
	}
	raw, err := json.Marshal(reqObj)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var env protocol.Response
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode json-rpc response: %w", err)
	}
	if env.Error != nil {
		return rpcErrorFrom(env.Error)
	}
	if result == nil {
		return nil
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("json-rpc: missing result")
	}
	return json.Unmarshal(env.Result, result)
}

// --- Typed RPC methods ---

func (c *Client) TabList(ctx context.Context, p protocol.TabListParams) (protocol.TabListResult, error) {
	var out protocol.TabListResult
	err := c.Call(ctx, protocol.MethodTabList, p, &out)
	return out, err
}

func (c *Client) TabFocus(ctx context.Context, p protocol.TabFocusParams) (protocol.TabFocusResult, error) {
	var out protocol.TabFocusResult
	err := c.Call(ctx, protocol.MethodTabFocus, p, &out)
	return out, err
}

func (c *Client) TabSelect(ctx context.Context, p protocol.TabSelectParams) (protocol.TabSelectResult, error) {
	var out protocol.TabSelectResult
	err := c.Call(ctx, protocol.MethodTabSelect, p, &out)
	return out, err
}

func (c *Client) TabNew(ctx context.Context, p protocol.TabNewParams) (protocol.TabNewResult, error) {
	var out protocol.TabNewResult
	err := c.Call(ctx, protocol.MethodTabNew, p, &out)
	return out, err
}

func (c *Client) Goto(ctx context.Context, p protocol.GotoParams) (protocol.GotoResult, error) {
	var out protocol.GotoResult
	err := c.Call(ctx, protocol.MethodGoto, p, &out)
	return out, err
}

func (c *Client) Reload(ctx context.Context, p protocol.ReloadParams) (protocol.ReloadResult, error) {
	var out protocol.ReloadResult
	err := c.Call(ctx, protocol.MethodReload, p, &out)
	return out, err
}

func (c *Client) TabClose(ctx context.Context, p protocol.TabCloseParams) (protocol.TabCloseResult, error) {
	var out protocol.TabCloseResult
	err := c.Call(ctx, protocol.MethodTabClose, p, &out)
	return out, err
}

func (c *Client) Screenshot(ctx context.Context, p protocol.ScreenshotParams) (protocol.ScreenshotResult, error) {
	var out protocol.ScreenshotResult
	err := c.Call(ctx, protocol.MethodScreenshot, p, &out)
	return out, err
}

func (c *Client) Eval(ctx context.Context, p protocol.EvalParams) (protocol.EvalResult, error) {
	var out protocol.EvalResult
	err := c.Call(ctx, protocol.MethodEval, p, &out)
	return out, err
}

func (c *Client) Click(ctx context.Context, p protocol.ClickParams) (protocol.ClickResult, error) {
	var out protocol.ClickResult
	err := c.Call(ctx, protocol.MethodClick, p, &out)
	return out, err
}

func (c *Client) Fill(ctx context.Context, p protocol.FillParams) (protocol.FillResult, error) {
	var out protocol.FillResult
	err := c.Call(ctx, protocol.MethodFill, p, &out)
	return out, err
}

func (c *Client) Network(ctx context.Context, p protocol.ObsQueryParams) (protocol.ObsQueryResult, error) {
	var out protocol.ObsQueryResult
	err := c.Call(ctx, protocol.MethodNetwork, p, &out)
	return out, err
}

func (c *Client) Console(ctx context.Context, p protocol.ObsQueryParams) (protocol.ObsQueryResult, error) {
	var out protocol.ObsQueryResult
	err := c.Call(ctx, protocol.MethodConsole, p, &out)
	return out, err
}

func (c *Client) Errors(ctx context.Context, p protocol.ObsQueryParams) (protocol.ObsQueryResult, error) {
	var out protocol.ObsQueryResult
	err := c.Call(ctx, protocol.MethodErrors, p, &out)
	return out, err
}

func (c *Client) Fetch(ctx context.Context, p protocol.FetchParams) (protocol.FetchResult, error) {
	var out protocol.FetchResult
	err := c.Call(ctx, protocol.MethodFetch, p, &out)
	return out, err
}

func (c *Client) Snapshot(ctx context.Context, p protocol.SnapshotParams) (protocol.SnapshotResult, error) {
	var out protocol.SnapshotResult
	err := c.Call(ctx, protocol.MethodSnapshot, p, &out)
	return out, err
}

func (c *Client) NetworkRoute(ctx context.Context, p protocol.NetworkRouteParams) (protocol.NetworkRouteResult, error) {
	var out protocol.NetworkRouteResult
	err := c.Call(ctx, protocol.MethodNetworkRoute, p, &out)
	return out, err
}

func (c *Client) NetworkUnroute(ctx context.Context, p protocol.NetworkUnrouteParams) (protocol.NetworkUnrouteResult, error) {
	var out protocol.NetworkUnrouteResult
	err := c.Call(ctx, protocol.MethodNetworkUnroute, p, &out)
	return out, err
}

func (c *Client) NetworkClear(ctx context.Context, p protocol.NetworkClearParams) (protocol.NetworkClearResult, error) {
	var out protocol.NetworkClearResult
	err := c.Call(ctx, protocol.MethodNetworkClear, p, &out)
	return out, err
}

func (c *Client) ConsoleClear(ctx context.Context, p protocol.ConsoleClearParams) (protocol.ConsoleClearResult, error) {
	var out protocol.ConsoleClearResult
	err := c.Call(ctx, protocol.MethodConsoleClear, p, &out)
	return out, err
}

func (c *Client) ErrorsClear(ctx context.Context, p protocol.ErrorsClearParams) (protocol.ErrorsClearResult, error) {
	var out protocol.ErrorsClearResult
	err := c.Call(ctx, protocol.MethodErrorsClear, p, &out)
	return out, err
}
