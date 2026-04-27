# go-bb-browser

Private experiment: a **Go** reimplementation inspired by [bb-browser](https://github.com/epiral/bb-browser) (CLI + local daemon controlling a real browser session). The daemon talks to **Google Chrome only** via **[chromedp](https://github.com/chromedp/chromedp)** (Chrome DevTools Protocol) — it **never launches Chrome**, only **attaches over CDP** to a browser you already started with remote debugging — **no Chrome extension**, **no other browsers**.

Implementation strategy and milestones are documented in:

- [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)

## HTTP API: JSON-RPC 2.0 on `POST /v1`

The body is a single [JSON-RPC 2.0](https://www.jsonrpc.org/specification) request: `jsonrpc`, `method`, `params` (object), and `id`. The **`method` string matches the former flat `action` field** (e.g. `tab_list`, `goto`). Success responses wrap the payload in **`result`**; failures use **`error`** with `code`, `message`, and optional **`data`** (`error`, `hint`, `method`). Operational **`result`** objects include **`tab`** and **`seq`** where applicable (INV-1).

**Phase 0–2 workflow:** create a tab, run scoped methods that require a tab id, then close by id. Additional methods: **`screenshot`**, **`eval`**, **`click`**, **`fill`**.

1. `tab_new` — `params`: optional `"url"` for initial load (e.g. `"about:blank"`). `result`: `{ "tab", "seq" }`.
2. `goto` — `params`: `"tab"`, `"url"`.
3. `tab_list` — `params`: `{}` or omit. Lists page targets.
4. `tab_select`, `tab_close` — `params`: `"tab"`.
5. `screenshot` — `params`: `"tab"`; optional `"format"` (`"png"` default, or `"jpeg"`). `result`: `"data"` (base64), `"mime"`, `"tab"`, `"seq"`.
6. `eval` — `params`: `"tab"`, `"script"`. `result`: `"result"` (JSON), `"tab"`, `"seq"`.
7. `click` — `params`: `"tab"`, `"selector"` (CSS).
8. `fill` — `params`: `"tab"`, `"selector"`, `"text"` (see chromedp `SetValue`).

All **`POST /v1`** responses use **HTTP 200** with a JSON-RPC object (check `error` vs `result`). Wrong HTTP method on `/v1` returns **405**.

**Migration** (breaking): replace `{ "action": "…", … }` with `{ "jsonrpc":"2.0", "method": "…", "params": { … }, "id": … }`. Example: old `{"action":"goto","tab":"3456","url":"https://example.com"}` → `method` `goto` and `params` `{ "tab":"3456", "url":"https://example.com" }`.

```bash
go build -o bb-daemon ./cmd/bb-daemon
go build -o bb-browser  ./cmd/bb-browser

# Optional helper: launch Google Chrome with CDP enabled and a persistent profile.
# Works on Linux, macOS, and Windows. Prints the debugger host:port on success.
./bb-browser launch                       # default port 9222, profile dir, --start-url about:blank
./bb-browser launch --port 9333 --profile /tmp/chrome-prof --start-url https://example.com

./bb-daemon --debugger-url 127.0.0.1:9222
# IPv6 loopback also works (bare ::1:9222 is normalized to [::1]:9222)
# GET http://127.0.0.1:8787/health → {"status":"ok"}
# POST http://127.0.0.1:8787/v1  Content-Type: application/json
#   {"jsonrpc":"2.0","method":"tab_new","params":{"url":"about:blank"},"id":1}
#   {"jsonrpc":"2.0","method":"goto","params":{"tab":"<short>","url":"https://example.com"},"id":2}
#   {"jsonrpc":"2.0","method":"tab_list","params":{},"id":3}
#   {"jsonrpc":"2.0","method":"tab_close","params":{"tab":"<short>"},"id":4}
```

See `AGENTS.md` for build and test commands.
