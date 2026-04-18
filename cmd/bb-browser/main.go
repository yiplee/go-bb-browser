package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	baseURL string
	jsonOut bool
	tabFlag string
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "bb-browser",
		Short: "HTTP client for bb-browserd",
		Long:  "Calls the local bb-browserd HTTP API (health + JSON-RPC on POST /v1).",
	}

	root.PersistentFlags().StringVar(&baseURL, "url", envOrDefault("BB_BROWSER_URL", "http://127.0.0.1:8787"), "bb-browserd base URL (no trailing slash)")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "print raw JSON (full JSON-RPC envelope for /v1; health body for /health)")
	root.PersistentFlags().StringVar(&tabFlag, "tab", "", "short tab id; when omitted, most commands use the daemon focused tab (tab_list / tab_focus); required for focus")

	root.AddCommand(
		newHealthCmd(),
		newListCmd(),
		newFocusCmd(),
		newTabNewCmd(),
		newCloseCmd(),
		newReloadCmd(),
		newGotoCmd(),
		newObsCmd("network", "Fetch buffered network observations for a tab"),
		newObsCmd("console", "Fetch buffered console observations for a tab"),
		newObsCmd("errors", "Fetch buffered error / log observations for a tab"),
	)

	return root
}

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "GET /health from the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			return cmdHealth(ctx, baseURL, jsonOut)
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "JSON-RPC tab_list",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			return cmdRPC(ctx, baseURL, jsonOut, "tab_list", map[string]any{})
		},
	}
}

func newFocusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "focus",
		Short: "JSON-RPC tab_select (switch daemon focused tab)",
		RunE: func(cmd *cobra.Command, args []string) error {
			tab := strings.TrimSpace(tabFlag)
			if tab == "" {
				return errors.New("focus requires --tab <short-id> (tab to select)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			return cmdRPC(ctx, baseURL, jsonOut, "tab_select", map[string]any{"tab": tab})
		},
	}
}

func newTabNewCmd() *cobra.Command {
	var initialURL string
	c := &cobra.Command{
		Use:   "new",
		Short: "JSON-RPC tab_new",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			return cmdRPC(ctx, baseURL, jsonOut, "tab_new", map[string]any{"url": initialURL})
		},
	}
	c.Flags().StringVar(&initialURL, "initial-url", "about:blank", `URL to open in the new tab (JSON-RPC "url" param)`)
	return c
}

func newCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close",
		Short: "JSON-RPC tab_close",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, "tab_close", map[string]any{"tab": tab})
		},
	}
}

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "JSON-RPC reload (refresh the page)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, "reload", map[string]any{"tab": tab})
		},
	}
}

func newGotoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "goto URL",
		Short: "JSON-RPC goto",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, "goto", map[string]any{"tab": tab, "url": args[0]})
		},
	}
}

func newObsCmd(method, short string) *cobra.Command {
	var since uint64
	c := &cobra.Command{
		Use:   method,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			params := map[string]any{"tab": tab}
			if cmd.Flags().Changed("since") {
				params["since"] = since
			}
			return cmdRPC(ctx, baseURL, jsonOut, method, params)
		},
	}
	c.Flags().Uint64Var(&since, "since", 0, "only return observations with seq greater than this value")
	return c
}

func postRPC(ctx context.Context, base string, method string, params map[string]any) ([]byte, error) {
	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      1,
		"params":  params,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}

func rpcEnvelopeError(b []byte) error {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil
	}
	if len(env.Error) == 0 || string(env.Error) == "null" {
		return nil
	}
	return fmt.Errorf("json-rpc error: %s", string(env.Error))
}

func daemonFocusedTab(ctx context.Context, base string) (string, error) {
	b, err := postRPC(ctx, base, "tab_list", map[string]any{})
	if err != nil {
		return "", err
	}
	if err := rpcEnvelopeError(b); err != nil {
		return "", err
	}
	var env struct {
		Result struct {
			Tab string `json:"tab"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return "", fmt.Errorf("decode tab_list result: %w", err)
	}
	tab := strings.TrimSpace(env.Result.Tab)
	if tab == "" {
		return "", errors.New("no focused tab: pass --tab or open a tab in Chrome")
	}
	return tab, nil
}

func effectiveTab(ctx context.Context, base, flag string) (string, error) {
	if t := strings.TrimSpace(flag); t != "" {
		return t, nil
	}
	return daemonFocusedTab(ctx, base)
}

func cmdHealth(ctx context.Context, base string, jsonOut bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: HTTP %d: %s", resp.StatusCode, string(b))
	}
	if jsonOut {
		fmt.Println(string(b))
		return nil
	}
	fmt.Println("ok")
	return nil
}

func cmdRPC(ctx context.Context, base string, jsonOut bool, method string, params map[string]any) error {
	b, err := postRPC(ctx, base, method, params)
	if err != nil {
		return err
	}
	if jsonOut {
		fmt.Println(string(b))
		if rpcHasError(b) {
			return errors.New("json-rpc error in response")
		}
		return nil
	}
	if err := rpcEnvelopeError(b); err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage  `json:"result"`
		Error  *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		fmt.Println(string(b))
		return fmt.Errorf("decode response: %w", err)
	}
	if env.Error != nil {
		return fmt.Errorf("json-rpc error: %s", string(*env.Error))
	}
	if len(env.Result) > 0 {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, env.Result, "", "  "); err != nil {
			fmt.Println(string(env.Result))
		} else {
			_, _ = pretty.WriteTo(os.Stdout)
			fmt.Println()
		}
	}
	return nil
}

func rpcHasError(body []byte) bool {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return false
	}
	return len(env.Error) > 0 && string(env.Error) != "null"
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
