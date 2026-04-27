# daemonclient

`daemonclient` 是 **bb-daemon** 的 Go HTTP 客户端：通过 **JSON-RPC 2.0** 调用 `POST /v1`，并通过 **GET /health** 做存活检查。协议字段与 **`pkg/protocol`**（包名 `protocol`）中的类型、方法名一致，与仓库根目录 `AGENTS.md` 中描述的守护进程行为对齐（短 tab id、全局单调 `seq`、观测类接口的 `cursor` 等）。

## 适用场景

- 需要类型安全地调用守护进程时，使用本包的封装方法，并导入 **`github.com/yiplee/go-bb-browser/pkg/protocol`** 获取 `Params` / `Result` 与方法名常量；该路径**可被模块外项目正常 `import`**。
- 若不想依赖 `protocol` 类型，仍可使用 `Call`，自行定义与 JSON 响应形状一致的 `result` 结构体，并把 `params` 设为 `map[string]any` 或可 `json.Marshal` 的任意值。

## 安装与导入

```go
import (
    "github.com/yiplee/go-bb-browser/pkg/daemonclient"
    "github.com/yiplee/go-bb-browser/pkg/protocol"
)
```

守护进程根地址示例：`http://127.0.0.1:8080`（不要带尾部路径；客户端会自行拼接 `/health` 与 `/v1`）。

## 构造客户端

```go
c := daemonclient.NewClient("http://127.0.0.1:8080")
```

`NewClient` 会去掉首尾空白，并去掉 `BaseURL` 末尾的 `/`。

可选字段：

| 字段 | 含义 |
|------|------|
| `BaseURL` | 守护进程 HTTP 根 URL |
| `HTTP` | 若非 `nil`，用于所有请求；否则使用 `http.DefaultClient` |

每个 `Client` 内部使用单调递增的 JSON-RPC `id`（`uint64` 序列化），与单次调用的业务 `seq` 无关。

## 健康检查

```go
if err := c.Health(ctx); err != nil {
    // 非 200：*daemonclient.HTTPError
}
```

- 请求：`GET {BaseURL}/health`
- 成功：HTTP 200（响应体内容当前未解析，仅校验状态码）

## 通用调用：`Call`

```go
err := c.Call(ctx, method, params, resultPtr)
```

| 参数 | 说明 |
|------|------|
| `method` | JSON-RPC `method` 字符串，与 `protocol.Method*` 常量一致（如 `protocol.MethodTabList`） |
| `params` | 任意可 `json.Marshal` 的值；`nil` 时等价于 `{}` |
| `result` | 成功时解码 `result` 字段的目标指针；若为 `nil`，在响应无 `error` 时忽略 `result`（若仍缺少 `result` 会报错，见下） |

行为要点：

- 请求：`POST {BaseURL}/v1`，`Content-Type: application/json`，体为单对象 JSON-RPC 请求（`jsonrpc`、`method`、`params`、`id`）。
- HTTP 层非 200：返回 `*HTTPError`（含状态码与响应体文本）。
- HTTP 200 且 JSON-RPC 含 `error`：返回 `*RPCError`。
- HTTP 200、`error` 为空但 `result` 缺失且调用方需要解码：返回 `fmt.Errorf("json-rpc: missing result")`。

## 已封装方法（类型化 RPC）

以下方法均为 `Call` 的薄封装，方法名与 `pkg/protocol/jsonrpc.go` 中的常量一致。参数与返回值含义以该文件中的注释为准。

### Tab 与导航

| 客户端方法 | JSON-RPC `method` | 参数类型 | 结果类型 |
|------------|-------------------|----------|----------|
| `TabList` | `tab_list` | `TabListParams` | `TabListResult` |
| `TabFocus` | `tab_focus` | `TabFocusParams` | `TabFocusResult` |
| `TabSelect` | `tab_select` | `TabSelectParams` | `TabSelectResult` |
| `TabNew` | `tab_new` | `TabNewParams`（可选 `url`） | `TabNewResult` |
| `TabClose` | `tab_close` | `TabCloseParams` | `TabCloseResult` |
| `Goto` | `goto` | `GotoParams` | `GotoResult` |
| `Reload` | `reload` | `ReloadParams` | `ReloadResult` |

### 页面操作与脚本

| 客户端方法 | JSON-RPC `method` | 参数类型 | 结果类型 |
|------------|-------------------|----------|----------|
| `Screenshot` | `screenshot` | `ScreenshotParams` | `ScreenshotResult`（`data` 为 base64） |
| `Eval` | `eval` | `EvalParams` | `EvalResult`（`result` 为 `json.RawMessage`） |
| `Click` | `click` | `ClickParams`（可选 `ref` 与 snapshot 联动） | `ClickResult` |
| `Fill` | `fill` | `FillParams` | `FillResult` |
| `Snapshot` | `snapshot` | `SnapshotParams` | `SnapshotResult`（含 `refs` 映射） |
| `Fetch` | `fetch` | `FetchParams` | `FetchResult` |

### 观测：网络 / 控制台 / 错误

| 客户端方法 | JSON-RPC `method` | 参数类型 | 结果类型 |
|------------|-------------------|----------|----------|
| `Network` | `network` | `ObsQueryParams`（`tab` + 可选 `since`） | `ObsQueryResult` |
| `Console` | `console` | 同上 | 同上 |
| `Errors` | `errors` | 同上 | 同上 |

`ObsQueryResult` 含 `events`、`cursor`、可选 `dropped`，与实现计划中的增量观测语义一致。

### 观测缓冲清理与网络拦截

| 客户端方法 | JSON-RPC `method` | 参数类型 | 结果类型 |
|------------|-------------------|----------|----------|
| `NetworkClear` | `network_clear` | `NetworkClearParams` | `NetworkClearResult` |
| `ConsoleClear` | `console_clear` | `ConsoleClearParams` | `ConsoleClearResult` |
| `ErrorsClear` | `errors_clear` | `ErrorsClearParams` | `ErrorsClearResult` |
| `NetworkRoute` | `network_route` | `NetworkRouteParams` | `NetworkRouteResult` |
| `NetworkUnroute` | `network_unroute` | `NetworkUnrouteParams` | `NetworkUnrouteResult` |

## 错误处理

### `*RPCError`（HTTP 200，业务或协议错误）

```go
var re *daemonclient.RPCError
if errors.As(err, &re) {
    // re.Code, re.Message, re.Data (json.RawMessage)
    var data protocol.ErrData
    if err := re.UnmarshalData(&data); err == nil {
        // data.Error, data.Hint, data.Method
    }
}
```

常用 JSON-RPC 错误码在 `protocol` 中定义，例如：`CodeMethodNotFound`（-32601）、`CodeInvalidParams`（-32602）等。

`UnmarshalData` 在 `data` 为空或为 JSON `null` 时返回错误。

### `*HTTPError`（非 2xx）

含 `StatusCode` 与 `Body`。`Error()` 在正文过长时会截断显示。

### 其它错误

网络失败、JSON 编解码失败、`marshal params` 等会以普通 `error` 链形式返回，可用 `errors.Unwrap` 追溯。

## 与本仓库其它部分的关系

- **协议真相源**：`pkg/protocol/jsonrpc.go`（方法名、params/result 形状、错误码、`ErrData`）。
- **守护进程**：`cmd/bb-daemon`；HTTP 与 JSON-RPC 分发在 `internal/daemon` 等包中实现。
- **测试**：见 `client_test.go`（`httptest` 模拟 `/health` 与 `/v1`）。

## 简短示例

```go
ctx := context.Background()
c := daemonclient.NewClient("http://127.0.0.1:8080")
if err := c.Health(ctx); err != nil {
    return err
}

tabs, err := c.TabList(ctx, protocol.TabListParams{})
if err != nil {
    return err
}
_ = tabs.Seq   // 全局单调 seq
_ = tabs.Focus // 当前焦点 tab（若有）

out, err := c.TabNew(ctx, protocol.TabNewParams{URL: "about:blank"})
if err != nil {
    return err
}
tabID := out.Tab

_, err = c.Goto(ctx, protocol.GotoParams{Tab: tabID, URL: "https://example.com"})
if err != nil {
    return err
}
```

使用 `errors.As` 区分 `RPCError` 与 `HTTPError` 便于在 CLI 或自动化脚本中给出可读输出。
