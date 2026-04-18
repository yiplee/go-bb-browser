# Site 系统（go-bb-browser）

## 概念

Adapter 为 **JavaScript 文件**，内含 `/* @meta { ... } */` JSON 元数据。执行时由 daemon 在 **目标页面的 JS 环境**里跑（通过 **`eval`**，脚本为 async IIFE），从而使用页面 Cookie / 登录态。

元数据字段与 [bb-browser adapter 文档](https://github.com/epiral/bb-browser) 一致，常用字段：

| 字段 | 说明 |
|------|------|
| `name` | 唯一 id，如 `twitter/search` |
| `description` | 简短描述 |
| `domain` | 用于自动匹配标签页的主机名（如 `twitter.com`） |

## CLI

```bash
bb-browser site list
bb-browser site search <关键词>
bb-browser site run <name> [位置参数...] [--flag value ...]
bb-browser site <name> [args...]          # 省略 run
bb-browser site update                     # 打印 clone epiral/bb-sites 的路径说明
```

## 目录

```
~/.bb-browser/
├── sites/           # 私有 adapter（优先级高）
│   └── platform/command.js
└── bb-sites/        # 社区仓库克隆（site update / 手动 git clone）
    └── ...
```

同名 **`name`** 时，**`sites/` 覆盖 `bb-sites/`**。

## 自动 Tab

在未指定 `--tab` 时，`site run` 会：

1. 若 adapter 声明了 **`domain`**：在已有标签中查找 URL 主机名匹配该 domain（含子域）的页面；找到则用该 tab。
2. 否则：使用 daemon 当前焦点 tab（见 `tab_list` 的 `focus`/`tab` 语义）。
3. 若有 domain 但无匹配 tab：**新建 tab** 打开 `https://<domain>/`，并短暂等待加载后再执行脚本。

## 参数传入 JS

CLI 将参数合成一个对象传给 adapter 的最外层函数，例如：

- 位置参数 → `arg1`, `arg2`, …
- `--title "x"` → `title: "x"`

adapter 内使用 `async function(args) { ... }` 或等价形式，读取 `args.arg1`、`args.title` 等。

## 错误与登录提示

若脚本返回 `{ "error": "...", "hint": "..." }`，CLI 会格式化输出；当 `error`/`hint` 含 401、login 等关键词时会提示先在浏览器中登录。

## 与 bb-sites 的关系

社区仓库：<https://github.com/epiral/bb-sites>  
将仓库克隆到 `~/.bb-browser/bb-sites` 即可被 `site list` 扫描到。
