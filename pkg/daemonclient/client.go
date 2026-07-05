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

// Client talks to bb-daemon over HTTP (JSON-RPC POST /v1).
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Headers, if non-empty, are applied to every Health and Call request via [http.Header.Set]
	// (same key with multiple stored values keeps the last in slice order).
	// WithHeader / WithHeaders merge into this map via [http.Header.Add] (they do not replace each other).
	Headers http.Header

	id atomic.Uint64
}

// ClientOption configures a [Client] when passed to [NewClient].
type ClientOption func(*Client)

// WithHeader returns a [ClientOption] that merges a single header via [http.Header.Add].
func WithHeader(k, v string) ClientOption {
	return func(c *Client) {
		if c.Headers == nil {
			c.Headers = make(http.Header)
		}
		c.Headers.Add(k, v)
	}
}

// WithHeaders returns a [ClientOption] that merges all entries from h via [http.Header.Add]
// (including when combined with [WithHeader] or multiple WithHeaders; later options append).
func WithHeaders(h http.Header) ClientOption {
	return func(c *Client) {
		if len(h) == 0 {
			return
		}
		if c.Headers == nil {
			c.Headers = make(http.Header)
		}
		for key, vals := range h {
			for _, val := range vals {
				c.Headers.Add(key, val)
			}
		}
	}
}

// NewClient returns a client for the given daemon root URL (e.g. http://127.0.0.1:8080).
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) applyHeaders(req *http.Request) {
	if len(c.Headers) == 0 {
		return
	}
	for k, vv := range c.Headers {
		for _, v := range vv {
			req.Header.Set(k, v)
		}
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

// Health checks GET /health. The daemon must report browser connectivity (HTTP 200).
func (c *Client) Health(ctx context.Context) error {
	_, err := c.HealthResult(ctx)
	return err
}

// HealthResult performs GET /health and decodes the response body.
func (c *Client) HealthResult(ctx context.Context) (protocol.HealthResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return protocol.HealthResult{}, err
	}
	c.applyHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return protocol.HealthResult{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return protocol.HealthResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		var out protocol.HealthResult
		_ = json.Unmarshal(b, &out)
		return out, &HTTPError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	var out protocol.HealthResult
	if err := json.Unmarshal(b, &out); err != nil {
		return protocol.HealthResult{}, fmt.Errorf("decode health response: %w", err)
	}
	return out, nil
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
	c.applyHeaders(req)
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
