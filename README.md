# go-bb-browser

Private experiment: a **Go** reimplementation inspired by [bb-browser](https://github.com/epiral/bb-browser) (CLI + local daemon controlling a real browser session). The daemon talks to **Google Chrome only** via **[chromedp](https://github.com/chromedp/chromedp)** (Chrome DevTools Protocol) — it **never launches Chrome**, only **attaches over CDP** to a browser you already started with remote debugging — **no Chrome extension**, **no other browsers**.

Implementation strategy and milestones are documented in:

- [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)

**Phase 0–1:** run the daemon with a debugger endpoint configured (Chrome must be started separately with remote debugging). The daemon attaches over CDP on startup and exposes tab APIs. Example:

```bash
go build -o bb-browserd ./cmd/bb-browserd
./bb-browserd --debugger-url 127.0.0.1:9222
# IPv6 loopback also works (bare ::1:9222 is normalized to [::1]:9222)
# GET http://127.0.0.1:8787/health → {"status":"ok"}
# POST http://127.0.0.1:8787/v1  Content-Type: application/json
#   {"action":"tab_list"}
#   {"action":"tab_select","tab":"<short id from tab_list>"}
```

See `AGENTS.md` for build and test commands.
