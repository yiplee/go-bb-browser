---
name: bb-browser
description: 强大的信息获取与浏览器自动化工具（Go 实现）。CLI 连接本地 bb-browserd（JSON-RPC），daemon 仅通过 Chrome DevTools Protocol 附加用户已启动的 Chrome，复用登录态。支持快照与 @ref、带 Cookie 的 fetch、网络观测与拦截 mock、`run`（对 adapter `.js` 做页面内 eval）等。
allowed-tools: Bash(bb-browser:*)
---

# bb-browser — 浏览器自动化（Go + CDP）

仓库名为 **go-bb-browser**；CLI 二进制名为 **`bb-browser`**。

## 架构

```
CLI (bb-browser)  →  HTTP bb-browserd (POST /v1 JSON-RPC)  →  Chrome（仅 CDP 附加）
```

- **daemon 不启动 Chrome**：用户需自行用 `--remote-debugging-port`（等）启动 Chrome，再启动 `bb-browserd` 指向该调试端口。
- **仅 Google Chrome**，通过 **[chromedp](https://github.com/chromedp/chromedp)** 走 CDP；**无浏览器扩展**。
- 操作类与观测类响应遵循仓库 `AGENTS.md` 中的不变量：`tab` + 全局单调 `seq`；观测类带 `cursor` / `since`。

## 快速开始

```bash
# 终端 1：启动 daemon（示例端口见 README）
bb-browserd --debugger-url 127.0.0.1:9222

# 终端 2：CLI（默认连 http://127.0.0.1:8787）
export BB_BROWSER_URL=http://127.0.0.1:8787   # 可选

bb-browser open https://example.com
bb-browser snapshot -i
bb-browser click @3
bb-browser fill @2 "text"
bb-browser close
```

全局选项：`--url`（daemon 根 URL）、`--tab <短 id>`（多数命令）、`--json`（原始 JSON-RPC）。

## 文档（本仓库）

人类可读说明见本仓库 **`skills/bb-browser/`**（`SKILL.md` 与 `references/*.md`），供 Agent skill 或本地阅读。

## Adapter 脚本（`run`）

带可选 `/* @meta ... */` 的 JS，由 daemon 在**页面上下文**里 **`eval`**（async IIFE），可复用 Cookie / 登录态。

```bash
bb-browser run ./path/to/adapter.js "位置参数1" --title "x"
bb-browser run ~/.bb-browser/sites/demo/foo.js hello --json
```

脚本路径为**文件系统路径**（相对或绝对）；`@meta` 中的 **`domain`** 用于在未指定 `--tab` 时自动挑选或新建标签页（见 [references/site-system.md](references/site-system.md)）。

## fetch — 页面内 fetch（带登录态）

CLI 对应 daemon 的 **`fetch`**：在标签页里执行 `fetch()`，`credentials: 'include'`，相对路径相对当前页 origin。

```bash
bb-browser fetch https://example.com/api/me.json
bb-browser fetch /api/me.json
bb-browser fetch https://api.example.com/x --method POST --body '{"k":"v"}'
bb-browser fetch https://x.com/i.json --headers '{"Authorization":"Bearer ..."}' --json
```

详见 [references/fetch-and-network.md](references/fetch-and-network.md)。

## Tab 与导航

```bash
bb-browser open URL
bb-browser open URL --current          # 当前 tab 导航，不新建
bb-browser goto URL
bb-browser reload
bb-browser tab list
bb-browser tab new [url]
bb-browser tab select --id <短id> | --index N
bb-browser tab close [--id ... | --index N]
bb-browser close                       # 关闭当前（焦点）tab
```

## 快照与 @ref

```bash
bb-browser snapshot
bb-browser snapshot -i -c -d 5 -s ".main"
```

`snapshot` 会给节点打上 `__bb_snap_ref`，`click`/`fill` 可使用 **`@N`**（或 JSON-RPC 里传 `ref`）。

详见 [references/snapshot-refs.md](references/snapshot-refs.md)。

## 网络与调试

```bash
bb-browser network requests [filter] [--since N] [--with-body]
bb-browser network clear
bb-browser network route '*analytics*' --abort
bb-browser network route '*/api/user' --body '{"mock":true}'
bb-browser network unroute [pattern]

bb-browser console [filter] [--clear]
bb-browser errors [filter] [--clear]
```

## 其它

```bash
bb-browser eval 'document.title'
bb-browser screenshot [path.png]
bb-browser health
```

## HTTP API（Agent 直连 daemon）

底层为 **JSON-RPC 2.0**，`POST /v1`，方法名见 daemon 实现（如 `snapshot`、`fetch`、`network_route`）。详见 [references/daemon-jsonrpc.md](references/daemon-jsonrpc.md)。

## 当前 CLI 限制

- **无 `trace start/stop`**、**无 `--mcp`**：若需要，应在 go-bb-browser 仓库跟进实现后再更新本 skill。
- **`network requests --with-body`**：daemon 侧当前以精简字段为主；完整请求/响应体需后续 CDP 增强。

## 深入文档

| 文档 | 说明 |
|------|------|
| [references/script-system.md](references/script-system.md) | `run`、@meta、`domain` 与自动 tab |
| [references/adapter-development.md](references/adapter-development.md) | 自定义 adapter |
| [references/fetch-and-network.md](references/fetch-and-network.md) | `fetch` 与 `network` 子命令、JSON-RPC 对应关系 |
| [references/snapshot-refs.md](references/snapshot-refs.md) | `@ref` 与 `__bb_snap_ref` |
| [references/daemon-jsonrpc.md](references/daemon-jsonrpc.md) | `POST /v1` 方法表（Agent 直连） |

## 仓库内文档

- 实现计划：`docs/IMPLEMENTATION_PLAN.md`
- Agent 不变量：`AGENTS.md`
