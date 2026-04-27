package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/spf13/cobra"
	"github.com/yiplee/go-bb-browser/internal/site"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

var (
	baseURL string
	jsonOut bool
	tabFlag string

	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func versionString(name string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", name, version, commit, date)
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "bb-browser",
		Version: versionString("bb-browser"),
		Short:   "CLI for bb-daemon (bb-browser–style UX over JSON-RPC)",
		Long: strings.TrimSpace(`
HTTP client for the local bb-daemon daemon. Commands mirror the bb-browser skill:
open/snapshot/click/fill with @refs, fetch (in-page), network route/unroute/clear, and run (eval adapter JS from a file path).

Requires Chrome with remote debugging and a running bb-daemon (see README).`),
	}

	root.PersistentFlags().StringVar(&baseURL, "url", envOrDefault("BB_BROWSER_URL", "http://127.0.0.1:8787"), "bb-daemon base URL (no trailing slash)")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "print raw JSON-RPC result (or full envelope for some commands)")
	root.PersistentFlags().StringVar(&tabFlag, "tab", "", "short tab id; when omitted, uses daemon focused tab from tab_list")

	root.AddCommand(
		newHealthCmd(),
		newLaunchCmd(),
		newOpenCmd(),
		newTabCmd(),
		newSnapshotCmd(),
		newEvalCmd(),
		newClickCmd(),
		newFillCmd(),
		newFetchCmd(),
		newScreenshotCmd(),
		newHtmlCmd(),
		newReloadCmd(),
		newRefreshAlias(),
		newCloseCmd(),
		newGotoCmd(),
		newNetworkCmd(),
		newObsCmd("console", protocol.MethodConsole, protocol.MethodConsoleClear, "Console log buffer (or --clear)"),
		newObsCmd("errors", protocol.MethodErrors, protocol.MethodErrorsClear, "JS error / log buffer (or --clear)"),
		newRunCmd(),
	)

	root.SetVersionTemplate("{{.Version}}\n")
	root.Flags().BoolP("version", "v", false, "print version and exit")

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

func newOpenCmd() *cobra.Command {
	var current bool
	var waitSec int
	c := &cobra.Command{
		Use:   "open URL",
		Short: "Open URL in a new tab (or navigate in the current tab)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			rawURL := strings.TrimSpace(args[0])
			if current {
				tab, err := effectiveTab(ctx, baseURL, tabFlag)
				if err != nil {
					return err
				}
				if err := cmdRPC(ctx, baseURL, jsonOut, protocol.MethodGoto, map[string]any{"tab": tab, "url": rawURL}); err != nil {
					return err
				}
				fmt.Println(tab)
				return nil
			}
			if err := cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabNew, map[string]any{"url": rawURL}); err != nil {
				return err
			}
			if !jsonOut && waitSec > 0 {
				time.Sleep(time.Duration(waitSec) * time.Second)
			}
			// Print new short id from last response — re-fetch tab_list to get focused tab after tab_new
			if jsonOut {
				return nil
			}
			b, err := postRPC(ctx, baseURL, protocol.MethodTabList, map[string]any{})
			if err != nil {
				return err
			}
			var env struct {
				Result struct {
					Tab   string `json:"tab"`
					Focus string `json:"focus"`
				} `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return nil
			}
			out := strings.TrimSpace(env.Result.Focus)
			if out == "" {
				out = strings.TrimSpace(env.Result.Tab)
			}
			if out != "" {
				fmt.Println(out)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&current, "current", false, "navigate in the current/focused tab instead of opening a new tab")
	c.Flags().IntVar(&waitSec, "wait", 0, "after opening a new tab, sleep this many seconds (helps slow pages)")
	return c
}

func newTabCmd() *cobra.Command {
	tab := &cobra.Command{
		Use:   "tab",
		Short: "List, create, select, or close tabs",
	}

	tab.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "JSON-RPC tab_list",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabList, map[string]any{})
		},
	})

	tabNew := &cobra.Command{
		Use:   "new [url]",
		Short: "JSON-RPC tab_new",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			u := "about:blank"
			if len(args) > 0 {
				u = strings.TrimSpace(args[0])
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabNew, map[string]any{"url": u})
		},
	}

	var selID string
	var selIdx int
	tabSel := &cobra.Command{
		Use:   "select",
		Short: "Switch daemon focused tab (--id short id or --index 1-based)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			if cmd.Flags().Changed("id") {
				id := strings.TrimSpace(selID)
				if id == "" {
					return errors.New("empty --id")
				}
				return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabSelect, map[string]any{"tab": id})
			}
			if !cmd.Flags().Changed("index") {
				return errors.New("need --id or --index")
			}
			tab, err := tabByIndex(ctx, baseURL, selIdx)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabSelect, map[string]any{"tab": tab})
		},
	}
	tabSel.Flags().StringVar(&selID, "id", "", "short tab id")
	tabSel.Flags().IntVar(&selIdx, "index", 0, "1-based index into sorted tab_list")

	var closeID string
	var closeIdx int
	tabClose := &cobra.Command{
		Use:   "close",
		Short: "JSON-RPC tab_close",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			var tab string
			var err error
			switch {
			case cmd.Flags().Changed("id"):
				tab = strings.TrimSpace(closeID)
				if tab == "" {
					return errors.New("empty --id")
				}
			case cmd.Flags().Changed("index"):
				tab, err = tabByIndex(ctx, baseURL, closeIdx)
				if err != nil {
					return err
				}
			default:
				tab, err = effectiveTab(ctx, baseURL, tabFlag)
				if err != nil {
					return err
				}
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabClose, map[string]any{"tab": tab})
		},
	}
	tabClose.Flags().StringVar(&closeID, "id", "", "short tab id")
	tabClose.Flags().IntVar(&closeIdx, "index", 0, "1-based index into sorted tab_list")

	tab.AddCommand(tabNew, tabSel, tabClose)
	return tab
}

func newSnapshotCmd() *cobra.Command {
	var interactive, prune bool
	var depth int
	var scope string
	c := &cobra.Command{
		Use:   "snapshot",
		Short: "Compact page tree with @ref → CSS selectors (snapshot JSON-RPC)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			params := map[string]any{
				"tab":              tab,
				"interactive_only": interactive,
				"prune_empty":      prune,
				"selector_scope":   strings.TrimSpace(scope),
			}
			if cmd.Flags().Changed("depth") {
				params["max_depth"] = depth
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodSnapshot, params)
		},
	}
	c.Flags().BoolVarP(&interactive, "interactive", "i", false, "only interactive elements (+ ancestors)")
	c.Flags().BoolVarP(&prune, "compact", "c", false, "omit empty structural nodes")
	c.Flags().IntVarP(&depth, "depth", "d", 0, "max tree depth (0 = unlimited)")
	c.Flags().StringVarP(&scope, "scope", "s", "", "CSS selector limiting snapshot root")
	return c
}

func newEvalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "eval SCRIPT",
		Short: "JSON-RPC eval",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			script := strings.Join(args, " ")
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodEval, map[string]any{"tab": tab, "script": script})
		},
	}
}

func newClickCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "click SELECTOR_OR_@REF",
		Short: "JSON-RPC click (CSS selector or @N from snapshot)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			s := strings.TrimSpace(args[0])
			params := map[string]any{"tab": tab}
			if strings.HasPrefix(s, "@") {
				params["ref"] = strings.TrimPrefix(s, "@")
			} else {
				params["selector"] = s
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodClick, params)
		},
	}
}

func newFillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fill SELECTOR_OR_@REF TEXT",
		Short: "JSON-RPC fill / SetValue",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			sel := strings.TrimSpace(args[0])
			text := strings.TrimSpace(strings.Join(args[1:], " "))
			params := map[string]any{"tab": tab, "text": text}
			if strings.HasPrefix(sel, "@") {
				params["ref"] = strings.TrimPrefix(sel, "@")
			} else {
				params["selector"] = sel
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodFill, params)
		},
	}
}

func newFetchCmd() *cobra.Command {
	var method, headers, output, body string
	c := &cobra.Command{
		Use:   "fetch URL",
		Short: "In-page fetch() with credentials (fetch JSON-RPC)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			rawURL := strings.TrimSpace(args[0])
			params := map[string]any{
				"tab": tab,
				"url": rawURL,
			}
			if method != "" {
				params["method"] = method
			}
			if headers != "" {
				params["headers"] = headers
			}
			if body != "" {
				params["body"] = body
			}
			if jsonOut {
				return cmdRPC(ctx, baseURL, true, protocol.MethodFetch, params)
			}
			b, err := postRPC(ctx, baseURL, protocol.MethodFetch, params)
			if err != nil {
				return err
			}
			if err := rpcEnvelopeError(b); err != nil {
				return err
			}
			var env struct {
				Result protocol.FetchResult `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return err
			}
			out := env.Result.Result
			if output != "" {
				var wrap map[string]json.RawMessage
				if err := json.Unmarshal(out, &wrap); err != nil {
					return fmt.Errorf("decode fetch result: %w", err)
				}
				var blob []byte
				switch {
				case wrap["json"] != nil:
					var buf bytes.Buffer
					if err := json.Indent(&buf, wrap["json"], "", "  "); err != nil {
						blob = wrap["json"]
					} else {
						blob = buf.Bytes()
					}
				case wrap["bodyText"] != nil:
					var s string
					_ = json.Unmarshal(wrap["bodyText"], &s)
					blob = []byte(s)
				default:
					var buf bytes.Buffer
					if err := json.Indent(&buf, out, "", "  "); err != nil {
						blob = out
					} else {
						blob = buf.Bytes()
					}
				}
				return os.WriteFile(output, blob, 0o644)
			}
			var buf bytes.Buffer
			if err := json.Indent(&buf, out, "", "  "); err != nil {
				fmt.Println(string(out))
			} else {
				_, _ = buf.WriteTo(os.Stdout)
				fmt.Println()
			}
			return nil
		},
	}
	c.Flags().StringVar(&method, "method", "GET", "HTTP method")
	c.Flags().StringVar(&headers, "headers", "", `extra headers as JSON object, e.g. '{"Authorization":"Bearer x"}'`)
	c.Flags().StringVar(&body, "body", "", "request body (non-GET)")
	c.Flags().StringVar(&output, "output", "", "write response body to file")
	return c
}

func newScreenshotCmd() *cobra.Command {
	var format string
	var outPath string
	c := &cobra.Command{
		Use:   "screenshot [path.png]",
		Short: "JSON-RPC screenshot (optional file path)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			params := map[string]any{"tab": tab}
			if format != "" {
				params["format"] = format
			}
			if jsonOut {
				return cmdRPC(ctx, baseURL, true, protocol.MethodScreenshot, params)
			}
			b, err := postRPC(ctx, baseURL, protocol.MethodScreenshot, params)
			if err != nil {
				return err
			}
			if err := rpcEnvelopeError(b); err != nil {
				return err
			}
			var env struct {
				Result struct {
					Data string `json:"data"`
				} `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return err
			}
			raw, err := decodeBase64(env.Result.Data)
			if err != nil {
				return err
			}
			path := outPath
			if len(args) > 0 {
				path = args[0]
			}
			if path == "" {
				path = fmt.Sprintf("screenshot-%d.png", time.Now().Unix())
			}
			return os.WriteFile(path, raw, 0o644)
		},
	}
	c.Flags().StringVar(&format, "format", "png", "png or jpeg")
	c.Flags().StringVar(&outPath, "output", "", "output path (default auto name)")
	return c
}

// jsHTMLVisibleOnly builds a new <html> tree by cloning only nodes that are not CSS-hidden
// (display:none, visibility:hidden/collapse, opacity:0). Skips script/style/noscript/template;
// always keeps TITLE. Not viewport-based (does not use IntersectionObserver).
const jsHTMLVisibleOnly = `(function(){
function isSkipped(el){
if(el.nodeType!==1)return false;
var t=el.tagName;
if(t==="TITLE")return false;
if(t==="SCRIPT"||t==="STYLE"||t==="NOSCRIPT"||t==="TEMPLATE")return true;
var s=getComputedStyle(el);
if(s.display==="none"||s.visibility==="hidden"||s.visibility==="collapse")return true;
if(parseFloat(s.opacity)===0)return true;
return false;
}
function appendVisibleChildren(srcParent,dstParent){
for(var i=0;i<srcParent.childNodes.length;i++){
var child=srcParent.childNodes[i];
if(child.nodeType===3){dstParent.appendChild(child.cloneNode(false));continue;}
if(child.nodeType!==1)continue;
if(isSkipped(child))continue;
var clone=child.cloneNode(false);
dstParent.appendChild(clone);
appendVisibleChildren(child,clone);
}
}
var html=document.createElement("html");
for(var j=0;j<document.documentElement.attributes.length;j++){
var a=document.documentElement.attributes[j];
html.setAttribute(a.name,a.value);
}
appendVisibleChildren(document.documentElement,html);
return html.outerHTML;
})()`

func newHtmlCmd() *cobra.Command {
	var outPath, format string
	var visibleOnly bool
	c := &cobra.Command{
		Use:   "html",
		Short: "Print the tab's rendered HTML (outerHTML via eval; optional --visible-only)",
		Long: strings.TrimSpace(`
Print document.documentElement as HTML via eval. With --visible-only, rebuild the DOM tree in-page, omitting subtrees hidden by CSS (display:none, visibility:hidden or collapse, opacity:0) and dropping script/style/noscript/template; TITLE is always kept. This is not viewport-based cropping.

Markdown output (--format markdown) is converted locally in the CLI after eval.`),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			outFormat := strings.ToLower(strings.TrimSpace(format))
			if outFormat != "html" && outFormat != "markdown" {
				return fmt.Errorf("invalid --format %q (want html or markdown)", format)
			}
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			script := `(function(){return document.documentElement.outerHTML})()`
			if visibleOnly {
				script = jsHTMLVisibleOnly
			}
			if jsonOut {
				return cmdRPC(ctx, baseURL, true, protocol.MethodEval, map[string]any{"tab": tab, "script": script})
			}
			b, err := postRPC(ctx, baseURL, protocol.MethodEval, map[string]any{"tab": tab, "script": script})
			if err != nil {
				return err
			}
			if err := rpcEnvelopeError(b); err != nil {
				return err
			}
			var env struct {
				Result protocol.EvalResult `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return err
			}
			var html string
			if err := json.Unmarshal(env.Result.Result, &html); err != nil {
				return fmt.Errorf("decode eval result as HTML string: %w", err)
			}
			body := html
			if outFormat == "markdown" {
				md, err := htmltomarkdown.ConvertString(html)
				if err != nil {
					return fmt.Errorf("convert html to markdown: %w", err)
				}
				body = md
			}
			if outPath != "" {
				return os.WriteFile(outPath, []byte(body), 0o644)
			}
			fmt.Print(body)
			if !strings.HasSuffix(body, "\n") {
				fmt.Println()
			}
			return nil
		},
	}
	c.Flags().StringVarP(&outPath, "output", "o", "", "write result to file instead of stdout (HTML or markdown per --format)")
	c.Flags().StringVar(&format, "format", "html", "output format: html or markdown (ignored with --json, which returns raw eval HTML)")
	c.Flags().BoolVar(&visibleOnly, "visible-only", false, "omit CSS-hidden subtrees (see command long description); applies to eval script and thus to --json output")
	return c
}

func runReload(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	tab, err := effectiveTab(ctx, baseURL, tabFlag)
	if err != nil {
		return err
	}
	return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodReload, map[string]any{"tab": tab})
}

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "JSON-RPC reload (refresh the page)",
		RunE:  runReload,
	}
}

func newRefreshAlias() *cobra.Command {
	return &cobra.Command{
		Use:    "refresh",
		Short:  "Alias for reload",
		Hidden: true,
		RunE:   runReload,
	}
}

func newCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close",
		Short: "JSON-RPC tab_close (current/focused tab)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodTabClose, map[string]any{"tab": tab})
		},
	}
}

func newGotoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "goto URL",
		Short: "JSON-RPC goto",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodGoto, map[string]any{"tab": tab, "url": args[0]})
		},
	}
}

func newObsCmd(use, method, clearMethod, short string) *cobra.Command {
	var since uint64
	var filter string
	var argFilter string
	var withBody bool
	var clearObs bool
	c := &cobra.Command{
		Use:   use + " [filter]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			if clearObs {
				return cmdRPC(ctx, baseURL, jsonOut, clearMethod, map[string]any{"tab": tab})
			}
			params := map[string]any{"tab": tab}
			if cmd.Flags().Changed("since") {
				params["since"] = since
			}
			f := strings.TrimSpace(filter)
			if f == "" && len(args) > 0 {
				f = strings.TrimSpace(args[0])
			}
			if jsonOut {
				return cmdRPC(ctx, baseURL, true, method, params)
			}
			b, err := postRPC(ctx, baseURL, method, params)
			if err != nil {
				return err
			}
			if err := rpcEnvelopeError(b); err != nil {
				return err
			}
			var env struct {
				Result protocol.ObsQueryResult `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return err
			}
			for _, ev := range env.Result.Events {
				rawLine := formatObsLine(method, ev.Data)
				if argFilter != "" && !strings.Contains(strings.ToLower(rawLine), strings.ToLower(argFilter)) {
					continue
				}
				if f != "" && !strings.Contains(strings.ToLower(rawLine), strings.ToLower(f)) {
					continue
				}
				fmt.Println(rawLine)
				if withBody {
					fmt.Printf("  seq=%d raw=%s\n", ev.Seq, string(ev.Data))
				}
			}
			return nil
		},
	}
	c.Flags().Uint64Var(&since, "since", 0, "only observations with seq greater than this")
	c.Flags().StringVar(&filter, "filter", "", "substring filter (client-side)")
	c.Flags().StringVar(&argFilter, "grep", "", "additional substring filter")
	c.Flags().BoolVar(&withBody, "with-body", false, "print full JSON observation lines")
	c.Flags().BoolVar(&clearObs, "clear", false, "clear buffered observations for this stream")
	return c
}

func formatObsLine(_ string, data json.RawMessage) string {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return strings.TrimSpace(string(data))
	}
	urlStr := ""
	for _, k := range []string{"url", "URL"} {
		if v, ok := obj[k].(string); ok {
			urlStr = v
			break
		}
	}
	kind, _ := obj["kind"].(string)
	hmethod := ""
	if m, ok := obj["method"].(string); ok {
		hmethod = m
	}
	status := ""
	if st, ok := obj["status"].(float64); ok {
		status = fmt.Sprintf("%.0f", st)
	}
	if urlStr != "" || kind != "" {
		line := fmt.Sprintf("%s %s", hmethod, urlStr)
		if status != "" {
			line += fmt.Sprintf(" → %s", status)
		}
		if kind != "" {
			line = fmt.Sprintf("[%s] %s", kind, line)
		}
		return strings.TrimSpace(line)
	}
	if typ, ok := obj["type"].(string); ok {
		return typ + " " + string(data)
	}
	if txt, ok := obj["text"].(string); ok {
		return txt
	}
	return strings.TrimSpace(string(data))
}

func newNetworkCmd() *cobra.Command {
	net := &cobra.Command{
		Use:   "network",
		Short: "Network observations, buffer clear, and Fetch interception",
	}

	var obsSince uint64
	var obsWithBody bool
	netRequests := &cobra.Command{
		Use:   "requests [FILTER]",
		Short: "Buffered network activity (optional URL substring filter)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			params := map[string]any{"tab": tab}
			if cmd.Flags().Changed("since") {
				params["since"] = obsSince
			}
			filter := ""
			if len(args) > 0 {
				filter = strings.TrimSpace(args[0])
			}
			if jsonOut {
				return cmdRPC(ctx, baseURL, true, protocol.MethodNetwork, params)
			}
			b, err := postRPC(ctx, baseURL, protocol.MethodNetwork, params)
			if err != nil {
				return err
			}
			if err := rpcEnvelopeError(b); err != nil {
				return err
			}
			var env struct {
				Result protocol.ObsQueryResult `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return err
			}
			for _, ev := range env.Result.Events {
				var obj map[string]any
				if err := json.Unmarshal(ev.Data, &obj); err != nil {
					continue
				}
				urlStr := ""
				for _, k := range []string{"url", "URL"} {
					if v, ok := obj[k].(string); ok {
						urlStr = v
						break
					}
				}
				if filter != "" && !strings.Contains(strings.ToLower(urlStr), strings.ToLower(filter)) {
					continue
				}
				kind, _ := obj["kind"].(string)
				method := ""
				if m, ok := obj["method"].(string); ok {
					method = m
				}
				status := ""
				if st, ok := obj["status"].(float64); ok {
					status = fmt.Sprintf("%.0f", st)
				}
				line := fmt.Sprintf("%s %s", method, urlStr)
				if status != "" {
					line += fmt.Sprintf(" → %s", status)
				}
				if kind != "" {
					line = fmt.Sprintf("[%s] %s", kind, line)
				}
				fmt.Println(strings.TrimSpace(line))
				if obsWithBody {
					fmt.Printf("  seq=%d raw=%s\n", ev.Seq, string(ev.Data))
				}
			}
			return nil
		},
	}
	netRequests.Flags().Uint64Var(&obsSince, "since", 0, "only observations with seq greater than this")
	netRequests.Flags().BoolVar(&obsWithBody, "with-body", false, "print full JSON observation lines")

	netClear := &cobra.Command{
		Use:   "clear",
		Short: "Clear buffered network observations for the tab",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodNetworkClear, map[string]any{"tab": tab})
		},
	}

	var abort bool
	var body string
	var ctype string
	var status int
	route := &cobra.Command{
		Use:   `route URL_PATTERN [--abort | --body '{}' ]`,
		Short: "Intercept URLs (CDP Fetch); --abort blocks, --body mocks JSON/text",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			pat := strings.TrimSpace(args[0])
			params := map[string]any{
				"tab":          tab,
				"url_pattern":  pat,
				"abort":        abort,
				"body":         body,
				"content_type": ctype,
			}
			if cmd.Flags().Changed("status") {
				params["status"] = status
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodNetworkRoute, params)
		},
	}
	route.Flags().BoolVar(&abort, "abort", false, "fail matched requests")
	route.Flags().StringVar(&body, "body", "", "mock response body")
	route.Flags().StringVar(&ctype, "content-type", "application/json", "mock Content-Type")
	route.Flags().IntVar(&status, "status", 200, "mock HTTP status")

	unroute := &cobra.Command{
		Use:   "unroute [URL_PATTERN]",
		Short: "Remove one rule by pattern, or all rules when pattern omitted",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			tab, err := effectiveTab(ctx, baseURL, tabFlag)
			if err != nil {
				return err
			}
			pat := ""
			if len(args) > 0 {
				pat = strings.TrimSpace(args[0])
			}
			return cmdRPC(ctx, baseURL, jsonOut, protocol.MethodNetworkUnroute, map[string]any{
				"tab": tab, "url_pattern": pat,
			})
		},
	}

	net.AddCommand(netRequests, netClear, route, unroute)
	return net
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run SCRIPT.js [args...]",
		Short: "Run an adapter JS file in the page via eval (async)",
		Long: strings.TrimSpace(`
First argument is a path to a .js file. Remaining tokens are passed to the script:
positional arguments map to @meta "args" keys in JSON source order, then arg1, arg2, …;
use --name value or --name=value for named flags (see references/script-system.md).`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 180*time.Second)
			defer cancel()
			scriptPath := strings.TrimSpace(args[0])
			raw, meta, err := site.ReadAdapterFile(scriptPath)
			if err != nil {
				return err
			}

			positional := []string{}
			named := map[string]string{}
			rest := args[1:]
			for i := 0; i < len(rest); i++ {
				a := rest[i]
				if strings.HasPrefix(a, "--") {
					part := strings.TrimPrefix(a, "--")
					idx := strings.IndexByte(part, '=')
					if idx >= 0 {
						named[strings.TrimSpace(part[:idx])] = strings.TrimSpace(part[idx+1:])
						continue
					}
					key := strings.TrimSpace(part)
					if key == "" {
						continue
					}
					if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
						named[key] = rest[i+1]
						i++
						continue
					}
					return fmt.Errorf("flag --%s needs a value", key)
				}
				positional = append(positional, a)
			}
			argsJSON, err := site.ArgsObject(positional, named, site.ArgKeysFromAdapterSource(raw))
			if err != nil {
				return err
			}

			tab, err := pickTabForSite(ctx, meta.Domain)
			if err != nil {
				return err
			}

			script := site.RunScript(raw, string(argsJSON))

			b, err := postRPC(ctx, baseURL, protocol.MethodEval, map[string]any{"tab": tab, "script": script})
			if err != nil {
				return err
			}
			if jsonOut {
				fmt.Println(string(b))
				return rpcEnvelopeError(b)
			}
			if err := rpcEnvelopeError(b); err != nil {
				return err
			}
			var env struct {
				Result protocol.EvalResult `json:"result"`
			}
			if err := json.Unmarshal(b, &env); err != nil {
				return err
			}
			printSiteResult(env.Result.Result)
			return nil
		},
	}
}

var loginHintRe = regexp.MustCompile(`(?i)401|403|unauthorized|forbidden|not\s*logged|login\s*required|sign\s*in|auth`)

func printSiteResult(raw json.RawMessage) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		fmt.Println(string(raw))
		return
	}
	if obj, ok := v.(map[string]any); ok {
		if errStr, ok := obj["error"].(string); ok && errStr != "" {
			fmt.Fprintf(os.Stderr, "[error] %s\n", errStr)
			combined := errStr
			if h, ok := obj["hint"].(string); ok {
				combined += " " + h
				fmt.Fprintf(os.Stderr, "  Hint: %s\n", h)
			}
			if loginHintRe.MatchString(combined) {
				fmt.Fprintln(os.Stderr, "  (Log in to the site in Chrome for this profile, then retry.)")
			}
			return
		}
	}
	// json.Indent preserves \uXXXX from CDP; round-trip through MarshalIndent prints UTF-8.
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(string(raw))
		return
	}
	fmt.Println(string(pretty))
}

func pickTabForSite(ctx context.Context, domain string) (string, error) {
	tab := strings.TrimSpace(tabFlag)
	if tab != "" {
		return tab, nil
	}
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return effectiveTab(ctx, baseURL, "")
	}
	b, err := postRPC(ctx, baseURL, protocol.MethodTabList, map[string]any{})
	if err != nil {
		return "", err
	}
	if err := rpcEnvelopeError(b); err != nil {
		return "", err
	}
	var env struct {
		Result struct {
			Tabs []protocol.TabListItem `json:"tabs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return "", err
	}
	hostOf := func(pageURL string) string {
		u, err := url.Parse(pageURL)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	for _, t := range env.Result.Tabs {
		h := hostOf(t.URL)
		if h == domain || strings.HasSuffix(h, "."+domain) {
			return t.Tab, nil
		}
	}
	// Open new tab — wait for load (best-effort)
	u := "https://" + domain + "/"
	if err := cmdRPC(ctx, baseURL, true, protocol.MethodTabNew, map[string]any{"url": u}); err != nil {
		return "", err
	}
	time.Sleep(3 * time.Second)
	b2, err := postRPC(ctx, baseURL, protocol.MethodTabList, map[string]any{})
	if err != nil {
		return "", err
	}
	var env2 struct {
		Result struct {
			Focus string `json:"focus"`
			Tab   string `json:"tab"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b2, &env2); err != nil {
		return "", err
	}
	out := strings.TrimSpace(env2.Result.Focus)
	if out == "" {
		out = strings.TrimSpace(env2.Result.Tab)
	}
	if out == "" {
		return "", errors.New("could not resolve tab after opening domain tab")
	}
	return out, nil
}

func tabByIndex(ctx context.Context, base string, idx int) (string, error) {
	if idx <= 0 {
		return "", fmt.Errorf("index must be >= 1")
	}
	b, err := postRPC(ctx, base, protocol.MethodTabList, map[string]any{})
	if err != nil {
		return "", err
	}
	if err := rpcEnvelopeError(b); err != nil {
		return "", err
	}
	var env struct {
		Result struct {
			Tabs []protocol.TabListItem `json:"tabs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return "", err
	}
	tabs := append([]protocol.TabListItem(nil), env.Result.Tabs...)
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].Tab < tabs[j].Tab })
	if idx > len(tabs) {
		return "", fmt.Errorf("index %d out of range (have %d tabs)", idx, len(tabs))
	}
	return tabs[idx-1].Tab, nil
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
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
	b, err := postRPC(ctx, base, protocol.MethodTabList, map[string]any{})
	if err != nil {
		return "", err
	}
	if err := rpcEnvelopeError(b); err != nil {
		return "", err
	}
	var env struct {
		Result struct {
			Tab   string `json:"tab"`
			Focus string `json:"focus"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return "", fmt.Errorf("decode tab_list result: %w", err)
	}
	tab := strings.TrimSpace(env.Result.Focus)
	if tab == "" {
		tab = strings.TrimSpace(env.Result.Tab)
	}
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
