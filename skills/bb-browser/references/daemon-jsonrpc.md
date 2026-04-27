# bb-daemon JSON-RPC（POST /v1）

单请求对象：`jsonrpc: "2.0"`、`method`、`params`（对象）、`id`。  
响应：`result` 或 `error`（含 `code`、`message`、可选 `data`）。

默认 **`Content-Type: application/json`**，HTTP 通常为 **200**，需解析 JSON 判断是否 RPC 报错。

环境变量：**`BB_BROWSER_URL`**（CLI 默认为 `http://127.0.0.1:8787`）。

## 已实现方法（节选）

| method | params 要点 | result 要点 |
|--------|-------------|-------------|
| `tab_list` | `{}` | `tabs[]`, `tab`, `seq`, `focus` |
| `tab_focus` | `{}` | 当前可操作 tab 元数据 |
| `tab_select` | `tab` | `tab`, `seq` |
| `tab_new` | `url?` | 新 `tab`, `seq` |
| `goto` | `tab`, `url` | `tab`, `seq` |
| `reload` | `tab` | `tab`, `seq` |
| `tab_close` | `tab` | `tab`, `seq` |
| `screenshot` | `tab`, `format?` | base64 `data`, `mime`, `seq` |
| `eval` | `tab`, `script` | `result`, `seq` |
| `snapshot` | `tab`, `interactive_only?`, `prune_empty?`, `max_depth?`, `selector_scope?` | `text`, `refs`, `title`, `url`, `seq` |
| `click` | `tab`, `selector` **或** `ref` | `seq` |
| `fill` | `tab`, `selector` **或** `ref`, `text` | `seq` |
| `fetch` | `tab`, `url`, `method?`, `headers?`, `body?` | 页面返回的 JSON，`seq` |
| `network` | `tab`, `since?` | `events`, `cursor`, `seq`, `dropped` |
| `network_clear` | `tab` | `seq` |
| `network_route` | `tab`, `url_pattern`, `abort?`, `body?`, `content_type?`, `status?` | `routes`（计数）, `seq` |
| `network_unroute` | `tab`, `url_pattern?` | `routes`, `seq` |
| `console` | `tab`, `since?` | 同观测结构 |
| `console_clear` | `tab` | `seq` |
| `errors` | `tab`, `since?` | 同观测结构 |
| `errors_clear` | `tab` | `seq` |

字段名以 **`pkg/protocol`** 为准；扩展新方法时同步更新本文件与 **SKILL.md**。

## 观测类语义

- **`since`**：仅返回 **`seq` > since** 的事件。
- **`cursor`**：常用于下一轮 **`since`** 的起点（见实现）。
- **`seq`**（全局）：单调递增，见 `AGENTS.md` INV-4。
