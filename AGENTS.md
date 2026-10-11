# AGENTS.md

Agent-facing notes for this repository (planning phase).

## Intent

Mirror the **bb-browser** shape — **CLI → HTTP daemon → Chrome via CDP only** — while implementing in **Go** and using **chromedp** as the CDP client library.

**Scope:** **Google Chrome only**; **no Chrome extension** and **no multi-browser support** — all automation goes through CDP from the daemon. The **daemon never starts the browser**; it only **attaches** to an already-running Chrome with a DevTools debugging endpoint (e.g. `--remote-debugging-port`).

Upstream reference architecture (TypeScript): CLI and MCP talk HTTP to a daemon; the daemon holds CDP connections, dispatches commands, and maintains per-tab state with ring buffers and a global monotonic `seq`.

Canonical planning document: `docs/IMPLEMENTATION_PLAN.md`.

## Design invariants (ported from bb-browser)

Before implementing handlers, preserve these invariants:

1. **INV-1:** Operational responses include **short tab id** and **`seq`**.
2. **INV-2:** Observation-style responses (network / console / errors) include a **cursor** for incremental reads.
3. **INV-3:** Invalid tab id → **hard error** (no silent fallback).
4. **INV-4:** **`seq` is globally monotonic** (never decreases).
5. **INV-5:** Events are **isolated per tab** in queries.
6. **INV-6:** Tab close → release short id and **clear** that tab’s buffers.
7. **INV-7:** `tab_new` must work when **zero** tabs exist (ordering vs `ensurePageTarget` matters).

## When code exists

- **Build:** `go build -o bb-daemon ./cmd/bb-daemon`
- **Test:** `go test ./...`
- **Daemon:** `bb-daemon` requires `--debugger-url` (or `BB_BROWSER_DEBUGGER_URL`) — e.g. host:port after starting Chrome with remote debugging (`127.0.0.1:9222`). On startup it **attaches via chromedp** (`NewRemoteAllocator` only). Optional **`--tab-idle-timeout`** / **`BB_BROWSER_TAB_IDLE_TIMEOUT`** (default `5m`, `0` disables) closes **daemon-created** tabs after idle; tab-related RPC lines (`action` + JSON-RPC request `body`) append to **`rpc.jsonl`** under **`--state-dir`** / **`BB_BROWSER_STATE_DIR`** (default `~/.local/state/bb-daemon`; `--state-dir -` uses in-memory mode). The global **`seq`** is seeded from the wall-clock nanosecond at startup and incremented in memory (no persisted counter). Idle recovery replays `rpc.jsonl` and intersects per-tab last-activity with the tabs currently present via CDP (short tab ids are deterministically derived from CDP target ids, so they are stable across restarts). `rpc.jsonl` auto-rotates past **`--rpc-log-max-bytes`** / **`BB_BROWSER_RPC_LOG_MAX_BYTES`** (default 8 MiB) into numbered backups (`rpc.jsonl.1`…, 3 kept); the fresh log is seeded with a snapshot of currently managed tabs (synthetic `tab_new` + last-activity time) so recovery only needs the current file. **`POST /v1`** accepts **JSON-RPC 2.0** bodies: **`method`** replaces the old **`action`** field (same string values); arguments live in **`params`**. Methods include **`tab_new`** (optional `url`, optional **`silent`** to open in background without changing focus), **`goto`** (`tab` + `url`), **`tab_close`**, **`tab_select`**, **`tab_list`**, plus **`screenshot`**, **`eval`**, **`click`**, **`fill`**, observation **`network`**, **`console`**, **`errors`** (`tab` + optional **`since`**; **`result`** includes **`events`**, **`cursor`**, optional **`dropped`** — INV-2). Successful **`result`** objects include **`seq`**; **`tab`** when applicable (INV-1). Errors use JSON-RPC **`error`** (`code`, `message`, optional **`data`**).

Layout: `cmd/bb-daemon` (daemon); `internal/daemon` (HTTP server, JSON-RPC dispatch); `internal/browser` (remote CDP session); `internal/store` (RPC log + seq); `pkg/protocol` (JSON-RPC + params/result types, importable by dependents); `pkg/daemonclient` (`NewClient` one daemon; `NewPool` many daemons with even spread, per-daemon headers, tab affinity; Pool tab ids are `<backendKey>:<daemonShortId>`); `internal/state` (tab registry, observation buffers).

**Optional API token:** daemon merges repeated `--api-token`, comma-separated `BB_BROWSER_API_TOKEN`, and `--api-token-file` / `BB_BROWSER_API_TOKEN_FILE` (flag overrides file environment path; one token per line, blank and `#` comment lines ignored). Trim, deduplicate, ignore empty entries; an empty whitelist disables auth. Unreadable files fail startup without exposing secrets. Configured `POST /v1` (except the explicitly enabled loopback exemption below) requires `Authorization: Bearer <token>`; compare every whitelist entry with `crypto/subtle.ConstantTimeCompare`, without early match returns. Failures return standard HTTP 401 (`unauthorized\n`, plain text, `WWW-Authenticate: Bearer`) before reading the body, RPC dispatch or audit, so no `rpc.jsonl` write. `/live`, `/ready`, `/health` remain public. Never log API tokens. With no tokens, non-loopback listening only emits a startup warn. CLI `--api-token` / `BB_BROWSER_API_TOKEN` sends one token (flag overrides environment); `daemonclient.WithAPIToken` adds the header per client, including independent Pool backend tokens. Whitelist changes require restart; TLS, roles and health auth are out of scope.

**Optional loopback exemption:** `--api-token-allow-loopback` / `BB_BROWSER_API_TOKEN_ALLOW_LOOPBACK` defaults to false (standard `strconv.ParseBool` values; invalid/empty environment values fall back to false; flag overrides environment). With tokens configured, direct TCP loopback `POST /v1` requests without proxy headers bypass Authorization entirely, including wrong tokens. Only `r.RemoteAddr` establishes loopback (`127.0.0.0/8`, `::1`, IPv4-mapped IPv6); never trust `Host` or forwarded addresses. The package-level `proxyHeaders` list contains `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Real-IP`, `Forwarded`, `CF-Connecting-IP`, `CF-Ray`, `True-Client-IP`, `Via`; any of these or any `Tailscale-` prefix header disables exemption (case-insensitive presence, even empty values). Normal token authentication then applies. Startup logs one warn with tokens, or an info explaining no effect without tokens; no per-request exemption logs or secrets. Health routes and clients are unchanged. Direct local callers and Tailscale HTTP forwarding (Serve/Funnel default mode, proxy headers preserved) may enable it; forwarded calls still require tokens. Never enable for TCP forwarding (including `tailscale serve --tcp` / `--tls-terminated-tcp`), local reverse proxies that may strip headers, Docker port mappings, cloudflared, or uncertain proxy paths. Header detection is only a fallback, not the primary defense. [Official Serve docs](https://tailscale.com/docs/features/tailscale-serve#identity-headers) document identity headers and their absence for tagged devices/Funnel; [official proxy source](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go) additionally confirms forwarded headers and `Tailscale-Funnel-Request` (not confirmed in official docs, not live-tested).

**Multi-daemon:** do not put a daemon pool inside callers (e.g. feedscrawler). Use `daemonclient.NewPool(NewClient(urlA, WithHeader(...)), NewClient(urlB, WithHeader(...)))`. Tabs are not shared across daemons. Pool-facing tab ids are `<backendKey>:<daemonShortId>` (backendKey defaults to the 0-based constructor index; `NewPoolBackends` can set a name). `TabNew`, `TabList`, and `TabFocus` return that form; later ops parse the prefix, strip it, and send only the daemon-native short id to that backend (no cross-daemon forward/failover). `TabList` queries every backend, prefixes each id, and merges (a down backend is skipped; `*AllFailedError` only if all fail). `TabFocus` is the first healthy backend in constructor order, prefixed — not a pool-wide focus. Malformed or unknown-prefix ids are rejected. `NewPool` does not health-check at startup. Unbound `tab_new` may fail over to another daemon only to **open a new tab** there.

## Cursor Cloud specific instructions

- **Toolchain:** This module targets **Go 1.26** (`go.mod`). The distro's default `go` is older, so Go 1.26 is installed at `/usr/local/go` and symlinked as `/usr/local/bin/go` (system dep, baked into the VM snapshot). The startup update script only runs `go mod download`. Standard build/test commands are in the README / "When code exists" section above.
- **Running end-to-end requires a real Chrome — the daemon never launches it.** Chrome is preinstalled (`google-chrome`). There is no display, so launch **headless**: `./bb-browser launch --headless --port 9222` (prints `host:port`), then `./bb-daemon --debugger-url 127.0.0.1:9222 --listen 127.0.0.1:8787`. Verify with `./bb-browser health` or `curl 127.0.0.1:8787/health` (`{"status":"ok","browser":"connected"}`; HTTP 503 when CDP is down). The CLI defaults to `http://127.0.0.1:8787`.
- **Gotcha:** `screenshot` on a `data:` URL tab hangs in headless Chrome. Use a real navigable page (`http(s)://` or `file://`) when capturing screenshots.
