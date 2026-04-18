# go-bb-browser

Private experiment: a **Go** reimplementation inspired by [bb-browser](https://github.com/epiral/bb-browser) (CLI + local daemon controlling a real browser session). The daemon talks to **Google Chrome only** via **[chromedp](https://github.com/chromedp/chromedp)** (Chrome DevTools Protocol) — it **never launches Chrome**, only **attaches over CDP** to a browser you already started with remote debugging — **no Chrome extension**, **no other browsers**.

Implementation strategy and milestones are documented in:

- [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)

**Phase 0–2:** Intended workflow: **create a tab**, run **scoped actions** that require a tab id, then **close by id**:

1. `tab_new` (optional `"url"` for initial load, e.g. `"about:blank"`) → `{ "tab", "seq" }`
2. `goto` with `"tab"` + `"url"` → navigate that tab
3. Use `tab_list` with no tab (lists all page targets); other actions need `"tab"` where applicable (`tab_select`, `goto`, `tab_close`)
4. `tab_close` with `"tab"` when finished

```bash
go build -o bb-browserd ./cmd/bb-browserd
./bb-browserd --debugger-url 127.0.0.1:9222
# IPv6 loopback also works (bare ::1:9222 is normalized to [::1]:9222)
# GET http://127.0.0.1:8787/health → {"status":"ok"}
# POST http://127.0.0.1:8787/v1  Content-Type: application/json
#   {"action":"tab_new","url":"about:blank"}
#   {"action":"goto","tab":"<short>","url":"https://example.com"}
#   {"action":"tab_list"}
#   {"action":"tab_close","tab":"<short>"}
```

See `AGENTS.md` for build and test commands.
