# Adapter 开发（go-bb-browser）

推荐流程：**逆向网络 → eval 验证 → 写 adapter JS → 放入 `~/.bb-browser/sites/`**。

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

外层需可被包装为 **单一 async 表达式**（`run` 会以 IIFE 调用）。

## 3. 返回值

推荐返回 **可 JSON 序列化对象**。错误：

```javascript
return { error: 'HTTP 401', hint: 'please log in in the browser first' };
```

CLI 会对常见鉴权失败文案给出登录提示。

## 4. 本地测试

```bash
cp my.js ~/.bb-browser/sites/demo/test.js
bb-browser run ~/.bb-browser/sites/demo/test.js "hello" --json
```

## 5. 分发

将 `.js` 放在任意路径，用 **`bb-browser run <路径>`** 调用即可；`~/.bb-browser/sites/` 仅作为常见存放目录约定。
