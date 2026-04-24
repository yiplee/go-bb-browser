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

- **Build:** `go build -o bb-browserd ./cmd/bb-browserd`
- **Test:** `go test ./...`
- **Daemon:** `bb-browserd` requires `--debugger-url` (or `BB_BROWSER_DEBUGGER_URL`) — e.g. host:port after starting Chrome with remote debugging (`127.0.0.1:9222`). On startup it **attaches via chromedp** (`NewRemoteAllocator` only). **`POST /v1`** accepts **JSON-RPC 2.0** bodies: **`method`** replaces the old **`action`** field (same string values); arguments live in **`params`**. Methods include **`tab_new`** (optional `url` in `params`), **`goto`** (`tab` + `url`), **`tab_close`**, **`tab_select`**, **`tab_list`**, plus **`screenshot`**, **`eval`**, **`click`**, **`fill`**, and observation **`network`**, **`console`**, **`errors`** (`tab` + optional **`since`**; **`result`** includes **`events`**, **`cursor`**, optional **`dropped`** — INV-2). Successful **`result`** objects include **`seq`**; **`tab`** when applicable (INV-1). Errors use JSON-RPC **`error`** (`code`, `message`, optional **`data`**).

Layout: `cmd/bb-browserd` (daemon); `internal/daemon` (HTTP server, JSON-RPC dispatch); `internal/browser` (remote CDP session); `pkg/protocol` (JSON-RPC + params/result types, importable by dependents); `internal/state` (seq, tab registry).
