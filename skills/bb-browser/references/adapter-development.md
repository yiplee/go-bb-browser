# Adapter 开发（go-bb-browser）

推荐流程：**逆向网络 → eval 验证 → 写 adapter JS**。

下列命令对应 **go-bb-browser CLI**：

## 1. 逆向 API

```bash
bb-browser network clear --tab <短id>
bb-browser reload --tab <短id>
bb-browser network requests api --tab <短id>
bb-browser fetch /internal/api/foo.json --tab <短id>
```

（未传 `--tab` 时使用 daemon 当前焦点 tab。）

## 2. meta 格式

```javascript
/* @meta
{
  "name": "platform/command",
  "description": "...",
  "example": "bb-browser run ./x.js q1 --title \"t\"",
  "domain": "www.example.com",
  "args": {
    "query": {"required": true, "description": "..."}
  }
}
*/
(async function(args) {
  ...
})
```

可选字段 **`example`**：仅供文档/可复制示例；CLI 不解析。  
`args` 里各键在 JSON 中的**书写顺序**决定位置参数与 `@meta` 键名的对应关系（再额外映射 `arg1`、`arg2`、…）。

外层需可被包装为 **单一 async 表达式**（实现上会包一层 `(async function(__args) { … })(…)`；形如顶层的 `async function(args) { … }` 会被改写为可嵌套的表达式；紧跟在 `@meta` 块后或文件开头的 `export default` 会被剥掉以便 eval）。

`/* @meta … */` 取文件中**第一段**匹配块；请保持块内为合法 JSON（勿在字符串外嵌多余未转义 `}` 等，以免正则匹配截断）。

## 3. 返回值

推荐返回 **可 JSON 序列化对象**。错误：

```javascript
return { error: 'HTTP 401', hint: 'please log in in the browser first' };
```

CLI 在 `error`/`hint` 文本匹配到 401、403、unauthorized、login、sign in 等关键词时会提示先在 Chrome 中登录（见 `cmd/bb-browser` 中 `loginHintRe`）。

## CLI 行为（与实现一致）

- **`bb-browser run` 单次请求超时**为约 **180 秒**（长时间任务需拆步或增大实现中的超时后再编 skill）。
- **命名参数**：`--key value`、`--key=value` 均支持；**没有** shell 风格的纯 `--flag`（无值）布尔开关，缺值会报错。
- **`--tab`**：显式指定短 tab id 时，**忽略** `@meta.domain` 的自动选 tab / 新开 tab 逻辑。

## 4. 本地测试

```bash
bb-browser run my.js "hello" --json
```
