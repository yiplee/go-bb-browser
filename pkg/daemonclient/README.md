# daemonclient

`daemonclient` 是 **bb-daemon** 的 Go HTTP 客户端：通过 **JSON-RPC 2.0** 调用 `POST /v1`，并提供 `Live`、`Ready`、`Health` 三种健康检查。一台 daemon 用 `NewClient`；多台用 `NewPool`（见下文）。协议字段与 **`pkg/protocol`**（包名 `protocol`）中的类型、方法名一致，与仓库根目录 `AGENTS.md` 中描述的守护进程行为对齐（短 tab id、全局单调 `seq`、观测类接口的 `cursor` 等）。

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

守护进程根地址示例：`http://127.0.0.1:8080`（不要带尾部路径；客户端会自行拼接 `/live`、`/ready`、`/health` 与 `/v1`）。

## 构造客户端

```go
c := daemonclient.NewClient("http://127.0.0.1:8080")
```

`NewClient` 会去掉首尾空白，并去掉 `BaseURL` 末尾的 `/`。第二个及之后的参数为可选的 `ClientOption`，例如为所有请求附加 HTTP 头（鉴权、自定义网关等）：

```go
import "net/http"

c := daemonclient.NewClient("http://127.0.0.1:8080",
    daemonclient.WithHeaders(http.Header{
        "Authorization": []string{"Bearer token"},
    }),
    daemonclient.WithHeader("X-Request-Id", "abc"),
)
```

`WithHeader` / `WithHeaders` 以及多个同类 option 之间均为**合并**（内部使用 `Header.Add`），不会彼此覆盖。也可在构造后直接操作 `c.Headers`（与 `HTTP` 字段一样，由调用方自行维护该 map）。

可选字段：

| 字段 | 含义 |
|------|------|
| `BaseURL` | 守护进程 HTTP 根 URL |
| `HTTP` | 若非 `nil`，用于所有请求；否则使用 `http.DefaultClient` |
| `Headers` | 若非空，发往 `Live` / `Ready` / `Health` / `Call` 时用 `Header.Set` 写入（同一键在 `Headers` 中存多条时，取 slice 中最后一项）；`Call` 随后会 `Set` `Content-Type: application/json`，因此调用方无法覆盖 JSON-RPC 所需的 Content-Type |

`WithHeader(k, v)` 与 `WithHeaders(h)` 把条目合并进 `Headers`（`h` 为空或 `len(h)==0` 时不做任何事）。

每个 `Client` 内部使用单调递增的 JSON-RPC `id`（`uint64` 序列化），与单次调用的业务 `seq` 无关。

## 多 daemon：`Pool`

若干独立的 `bb-daemon`（各自 Chrome / `--debugger-url`）不能共享 tab。`Pool` 在客户端做负载分担，并把 **tab 钉在创建它的那一个 daemon 上**。

**Pool 对外 tab id**（仅 Pool 表面；单 `Client` 仍返回 daemon 短 id）格式为：

```text
<backendKey>:<daemonShortId>
```

- `backendKey` 默认是 `NewPool` 参数顺序的 **0-based 下标**（`"0"`、`"1"`、…）。也可用 `NewPoolBackends` 配稳定名字（不可含 `:`，不可重复），例如 `"west:abcd"`。
- 调用方把 `TabNew` 返回的 id 原样传回后续 tab 操作。Pool **按前缀路由**到对应后端，发给 daemon 时只带原生短 id。
- 因此跨 daemon 的原生短 id 碰撞在 Pool API 边界上不可能混淆。格式错误或未知 `backendKey` 会返回 `*InvalidTabIDError`；格式正确但不是本池创建（或已关闭 / `ForgetTab`）的 id 仍为 `*UnknownTabError`。

```go
pool, err := daemonclient.NewPool(
    daemonclient.NewClient("https://daemon-a.example",
        daemonclient.WithHeader("CF-Access-Client-Id", idA),
        daemonclient.WithHeader("CF-Access-Client-Secret", secretA),
    ),
    daemonclient.NewClient("https://daemon-b.example",
        daemonclient.WithHeader("CF-Access-Client-Id", idB),
        daemonclient.WithHeader("CF-Access-Client-Secret", secretB),
    ),
)
if err != nil {
    return err
}

out, err := pool.TabNew(ctx, protocol.TabNewParams{URL: "https://example.com"})
// out.Tab 形如 "0:abcd"；Eval / Goto / TabClose / … 必须带这个 id
_, err = pool.Eval(ctx, protocol.EvalParams{Tab: out.Tab, Script: "document.title"})
```

`NewClient` 的单 daemon API 不变。`Pool` 提供同一套 `Health` / `Call` / 类型化 RPC 方法。

| 策略 | 行为 |
|------|------|
| **启动** | `NewPool` **不**探测后端。部分 daemon 当时不可用也可以构造成功。 |
| **无 tab 的请求** | `Health` / `Ready` / `Live`、`tab_new`、`tab_list`、`tab_focus`：选 **in-flight 最少**的后端（并列时 round-robin），失败则试下一个；全部失败返回 `*AllFailedError`。`tab_new` 的 failover 只表示「新 tab 开在另一个 daemon」，**不会**把已有 tab 挪走。`tab_list` / `tab_focus` **不**写入 tab 映射。 |
| **有 tab 的请求** | **硬性**：必须打到 **开出该 tab 的同一个 daemon**。池对外 id 为 `<backendKey>:<daemonShortId>`（`TabNew` 成功时写入映射，`TabClose` 成功后删除）；发给 daemon 时去掉前缀。对端失败 **不会**改道到其它 daemon，也不做跨 daemon 转发。未知（未创建/已关闭）tab 返回 `*UnknownTabError`；格式错误或未知前缀返回 `*InvalidTabIDError`。池没有跨 daemon 的焦点 tab，省略 `tab` 会得到 `ErrTabRequired`。 |

`Pool.Clients()` 可拿到各 `*Client`（每端独立 URL / `Headers`）。`TabClose` 成功后解除亲和；daemon 空闲关 tab 时可用 `ForgetTab`。`BackendKeys()` 返回构造顺序下的前缀。

各 daemon 独立派生短 tab id，不同 daemon 可能返回相同短 id。Pool 用前缀把它们变成互不混淆的对外 id，**不再**靠先到先得的 close-on-collide 作为主策略（`TabCollisionError` 仅作防御）。`Call(ctx, "tab_new", params, nil)` 与非 `protocol.TabNewResult` 的 result 指针只要能解码 JSON 对象，成功时同样写入映射并把 `tab` 改写成前缀形式；无法解码的 result 会在发请求前被拒绝。`tab_list` / `tab_focus` 仍是无绑定的单 daemon 调用，返回该 daemon 的原生短 id，**不会**写入 Pool 映射。

## 健康检查

```go
if err := c.Live(ctx); err != nil { // 进程 liveness，不访问 CDP
    return err
}
if err := c.Ready(ctx); err != nil { // 接流量前的 CDP readiness
    // 非 200：*daemonclient.HTTPError
}
```

- `Live` 请求 `/live`，只接受 `{"status":"ok"}`，适合进程 supervisor。
- `Ready` / `ReadyResult` 请求 `/ready`；`suspect` 或 `failed` 时为 HTTP 503，适合流量 readiness。
- `Health` / `HealthResult` 请求兼容的 `/health`；`suspect` 仍为 connected/200，confirmed failed 才为 disconnected/503。
- `ReadyResult` / `HealthResult` 成功时解析并返回 `protocol.HealthResult`；非 200 返回包含响应体的 `*daemonclient.HTTPError`。

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

- 请求：`POST {BaseURL}/v1`，先用 `Header.Set` 写入 `Client.Headers` 中的项，再设置 `Content-Type: application/json`，体为单对象 JSON-RPC 请求（`jsonrpc`、`method`、`params`、`id`）。
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
| `TabNew` | `tab_new` | `TabNewParams`（可选 `url`、`silent`） | `TabNewResult` |
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
- **测试**：见 `client_test.go`、`pool_test.go`（`httptest` 模拟 `/health` 与 `/v1`）。

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
