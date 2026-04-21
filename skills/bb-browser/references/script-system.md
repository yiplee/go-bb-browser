# Adapter 与 `run`（go-bb-browser）

## 概念

Adapter 为 **JavaScript 文件**，可内含 `/* @meta { ... } */` JSON 元数据。`bb-browser run` 由 daemon 在 **目标页面的 JS 环境**里执行（通过 **`eval`**，脚本为 async IIFE），从而使用页面 Cookie / 登录态。

元数据字段约定见下表（开发与调试另见 [adapter-development.md](adapter-development.md)）：

| 字段 | 说明 |
|------|------|
| `name` | 逻辑 id（如 `twitter/search`），便于文档与命名 |
| `description` | 简短描述 |
| `domain` | 用于自动匹配标签页的主机名（如 `twitter.com`） |

无 `@meta` 的脚本也可 `run`：此时不读 `domain`，tab 选择规则见下文。

## CLI

```bash
bb-browser run SCRIPT.js [位置参数...] [--flag value ...]
bb-browser run SCRIPT.js --query "x"              # --name=value
bb-browser run ./adapters/foo.js a b --json       # 原始 JSON-RPC 包络
```

全局选项：`--tab <短 id>` 强制指定标签页；省略时行为见「自动 Tab」。

## 自动 Tab

在未指定 `--tab` 时，`run` 会：

1. 若脚本含 **`@meta`** 且声明了 **`domain`**：在已有标签中查找 URL 主机名匹配该 domain（含子域）的页面；找到则用该 tab。
2. 否则（无 `@meta`、或 `@meta` 无 `domain`）：使用 daemon 当前焦点 tab（见 `tab_list` 的 `focus`/`tab` 语义）。
3. 若有 `domain` 但无匹配 tab：**新建 tab** 打开 `https://<domain>/`，并短暂等待加载后再执行脚本。

## 参数传入 JS

CLI 将参数合成一个对象传给 adapter 的最外层函数，例如：

- 位置参数 → 先按 `@meta` 里 **`args`** 对象的**键在 JSON 中的顺序**映射到同名属性，再总是附带 `arg1`, `arg2`, …
- `--title "x"` → `title: "x"`

adapter 内使用 `async function(args) { ... }` 或等价形式，读取 `args.arg1`、`args.title` 等。

## 错误与登录提示

若脚本返回 `{ "error": "...", "hint": "..." }`，CLI 会格式化输出；当 `error`/`hint` 含 401、login 等关键词时会提示先在浏览器中登录。
