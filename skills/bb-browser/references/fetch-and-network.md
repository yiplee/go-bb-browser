# Fetch 与 Network（go-bb-browser）

## fetch（daemon：`fetch`；CLI：`bb-browser fetch`）

在**页面上下文**执行 `fetch()`，`credentials: 'include'`，自动携带 Cookie。

### CLI 示例

```bash
bb-browser fetch https://www.reddit.com/api/me.json
bb-browser fetch /api/me.json                              # 相对当前 tab
bb-browser fetch URL --method POST --body '{"a":1}'
bb-browser fetch URL --headers '{"Content-Type":"application/json"}'
bb-browser fetch URL --output out.json
bb-browser fetch URL --json                               # 原始 JSON-RPC
```

### JSON-RPC（节选）

请求：

```json
{
  "jsonrpc": "2.0",
  "method": "fetch",
  "params": {
    "tab": "<短 id>",
    "url": "https://... 或 /path",
    "method": "GET",
    "headers": "{\"Authorization\":\"...\"}",
    "body": ""
  },
  "id": 1
}
```

`result.result` 为页面内脚本返回的对象（含 `ok`、`status`、`bodyText` 或解析后的 `json` 等，见实现）。

### 路由到哪个 tab

- **`--tab` / `params.tab`**：显式指定。
- 相对 URL：依赖**该 tab** 当前页面的 `location` 解析。
- 绝对 URL：必须在**已有**的、能代表该站点登录态的标签页上操作；需要时可先 `open` 或 `goto` 再 `fetch`。

---

## network

### 观测缓冲（daemon：`network`）

事件来自 CDP 监听，经 ring buffer；查询时带 **`since`** 做增量，`result` 含 **`cursor`**、`events`、`dropped`。

CLI：

```bash
bb-browser network requests           # 人类可读摘要行
bb-browser network requests api       # 位置参数：URL 子串过滤
bb-browser network requests --filter x
bb-browser network requests --since 100
bb-browser network requests --with-body
bb-browser network clear               # JSON-RPC：network_clear
```

### 拦截（CDP Fetch 域）

CLI：

```bash
bb-browser network route '*track*' --abort
bb-browser network route '*/api/x' --body '{"ok":true}' --content-type application/json --status 200
bb-browser network unroute            # 移除该 tab 全部规则
bb-browser network unroute '*track*'  # 按创建时相同的 url_pattern 移除
```

JSON-RPC：`network_route`、`network_unroute`，参数含 `tab`、`url_pattern`；mock 时使用 `body`、`content_type`、`status`，阻止时使用 `abort: true`。

### 限制

当前实现以**可观测 + 可拦截**为主；**详细请求/响应体**的全程 dump 依赖后续 CDP（如 `Network.getResponseBody` 或与 Fetch 域结合）扩展。

---

## console / errors

```bash
bb-browser console [--since N] [filter]
bb-browser console --clear             # JSON-RPC：console_clear

bb-browser errors [--since N] [filter]
bb-browser errors --clear              # JSON-RPC：errors_clear
```
