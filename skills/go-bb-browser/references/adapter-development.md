# Adapter 开发（go-bb-browser）

开发与 [bb-browser adapter 指南](https://github.com/epiral/bb-browser) 的流程一致：**逆向网络 → eval 验证 → 写 adapter JS → 放入 `~/.bb-browser/sites/`**。

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

外层需可被包装为 **单一 async 表达式**（`site run` 会以 IIFE 调用）。

## 3. 返回值

推荐返回 **可 JSON 序列化对象**。错误：

```javascript
return { error: 'HTTP 401', hint: 'please log in in the browser first' };
```

CLI 会对常见鉴权失败文案给出登录提示。

## 4. 本地测试

```bash
cp my.js ~/.bb-browser/sites/demo/test.js
bb-browser site run demo/test "hello" --json
```

## 5. 共享到社区

向 [epiral/bb-sites](https://github.com/epiral/bb-sites) 提 PR；本地只认 **`~/.bb-browser/sites/`**。
