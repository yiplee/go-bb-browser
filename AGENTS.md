# AGENTS.md

Agent-facing notes for this repository (planning phase).

## Intent

Mirror the **bb-browser** shape — **CLI → HTTP daemon → Chrome via CDP only** — while implementing in **Go** and using **chromedp** as the CDP client library.

**Scope:** **Google Chrome only**; **no Chrome extension** and **no multi-browser support** — all automation goes through CDP from the daemon.

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

Record here: build / test / lint commands, module layout, and how to attach to a running Chrome (`--remote-debugging-port`, etc.).
