---
name: bb-browser-adapter
description: 专门指导在 go-bb-browser 上开发 adapter 脚本（`bb-browser run ./x.js`）的技能。Use when the user authors, modifies, or debugs an adapter JavaScript file, writes a `/* @meta */` block, maps CLI positional / `--flag` arguments into adapter `args`, reverse-engineers a site's private API for reuse with browser login state, or needs complexity-tier / anti-change patterns (DOM-structural extraction, webpack module discovery, Vue/React state access) against `bb-browserd` JSON-RPC (eval, fetch, network, snapshot).
allowed-tools: Bash(bb-browser:*)
---

# bb-browser adapter 开发指南（go-bb-browser）

本 skill 专注 **adapter 脚本开发**：在 **go-bb-browser** 的 CLI / daemon 上，把一个网站的能力封装成一条 `bb-browser run` 命令。语义/CLI/超时等细节以 go 仓 (`cmd/bb-browser`, `internal/site`) 实现为准，同时沿用上游 bb-browser（TypeScript）skill 的方法论（逆向 → 三层复杂度 → 抗变更）。

通用的浏览器自动化（`open / snapshot / click / fill / fetch / network / console / errors / screenshot`）参见同仓 `skills/bb-browser/`；本 skill 只聚焦 adapter。

## 心智模型

- **adapter = 一个 `.js` 文件**，顶部可选 `/* @meta { … } */`，主体为 **匿名** async 函数：`async function(args) { … }`。
- **`bb-browser run ./path.js` 由 daemon 在目标页面上下文里 eval**（async IIFE 包装），因此你写的代码等同于在该站的 DevTools Console 里运行：**能直接用页面 Cookie / 登录态**，相对 URL 相对当前页面。
- 输出要求：**可 JSON 序列化**。错误用 `{ error, hint }` 对象。
- 单次 `run` 超时 **约 180 秒**（`cmd/bb-browser/main.go`）；长任务需要拆步或先改实现。
- 脚本**不是注册到命名空间的 site**，而是**文件系统路径**。没有 `bb-browser site list/search/update` 这类命令；纯粹是“把这个文件在那个 tab 上跑一次”。

## 开发工作流（5 步）

```
Task Progress:
- [ ] Step 1: 在目标站点登录 + 逆向 API / DOM（network + fetch + eval）
- [ ] Step 2: 在 eval / fetch 中最小化复现数据获取
- [ ] Step 3: 写 adapter JS + @meta + 参数 / 错误契约
- [ ] Step 4: `bb-browser run ./x.js …` 本地跑，核对结构与错误分支
- [ ] Step 5: 固化到常用目录并写 `example`（可选）
```

### Step 1：逆向 API / DOM

先在 Chrome 中登录目标站，然后：

```bash
bb-browser tab list                                    # 找 domain 对应的短 id（或 open 新开）
bb-browser network clear --tab <id>
bb-browser reload --tab <id>
bb-browser network requests api --tab <id>             # 位置参数 = URL 子串过滤
bb-browser network requests --since <n> --tab <id>     # 增量
bb-browser fetch /internal/api/foo.json --tab <id>     # 原样带 Cookie 验证
bb-browser console --tab <id>                          # 看前端打印
bb-browser errors  --tab <id>                          # 看 JS 错误
```

重点：**请求 URL + query + 方法 + 必须的头**（Authorization / x-csrf-token / x-xsrf-token / referer 等），以及**响应结构**。

### Step 2：最小复现

在 DevTools 和 adapter 统一的前提下，用 `eval` 或 `fetch` 最小复现一次成功调用：

```bash
bb-browser eval "(async()=>{const r=await fetch('/api/me.json',{credentials:'include'});return {ok:r.ok,s:r.status,j:await r.json()}})()" --tab <id>
bb-browser fetch /api/me.json --tab <id>               # 更短，CLI 内置 credentials:'include'
```

确认：**仅靠 Cookie 够不够？** 够 → Tier 1；需要显式 header → Tier 2；需要读页面内部状态 / webpack 模块 → Tier 3（见下）。

### Step 3：写 adapter

最小骨架（所有新 adapter 从这里长出来）：

```javascript
/* @meta
{
  "name": "example/search",
  "description": "Search example.com posts",
  "domain": "www.example.com",
  "args": {
    "query": { "required": true,  "description": "搜索关键词" },
    "count": { "required": false, "description": "返回数量上限（默认 10）" }
  },
  "example": "bb-browser run ./example-search.js 'hello' --count 20"
}
*/
async function(args) {
  if (!args.query) return { error: 'Missing argument: query' };
  const n = Math.max(1, Math.min(50, parseInt(args.count ?? 10, 10)));
  const resp = await fetch(
    '/api/search?q=' + encodeURIComponent(args.query) + '&n=' + n,
    { credentials: 'include' }
  );
  if (!resp.ok) return { error: 'HTTP ' + resp.status, hint: 'Not logged in?' };
  const data = await resp.json();
  return data.items.map(it => ({
    title: it.title, url: it.url, ts: it.publishedAt,
  }));
}
```

### Step 4：本地跑

```bash
bb-browser run ./example-search.js "hello" --count 20
bb-browser run ./example-search.js "hello" --json          # 原始 JSON-RPC 包络
bb-browser run ./example-search.js "hello" --tab <id>      # 强制指定 tab
```

### Step 5：写 example（可选，供文档）

`@meta.example` CLI **不解析**，仅供人类阅读；写一条能直接复制粘贴的最短调用。

## `@meta` 格式（以 go 实现为准）

匹配规则由 `internal/site/adapters.go`：**首个** `/* @meta { … } */` 块；**必须是合法 JSON**（不要在字符串外嵌未转义 `}`，否则贪婪/非贪婪正则会在错误位置截断）。

| 字段          | 类型           | 说明                                                                      |
|---------------|----------------|--------------------------------------------------------------------------|
| `name`        | string         | 逻辑 id（如 `twitter/search`），文档/排错用                                |
| `description` | string         | 一句话描述                                                                |
| `domain`      | string         | 主机名（如 `www.example.com`）。**省略 `--tab` 时**用它匹配/新开 tab        |
| `args`        | object         | 键即参数名；**键在 JSON 中的书写顺序** 决定位置参数映射                    |
| `example`     | string         | 仅文档；CLI 不解析                                                        |

`args` 的子对象支持 `required` / `description`（保留给未来的校验/帮助；当前 CLI 不强校验 `required`，请在脚本里判）。不要依赖 Go 端做参数校验。

## 参数如何进到 `args`

CLI 将命令行合成一个对象传给 adapter 最外层函数（见 `internal/site/run.go`）：

1. **位置参数**：第 i 个位置参数映射到 `@meta.args` 的**第 i 个键**（JSON 源顺序），**同时总是**写入 `argN`（`arg1`, `arg2`, …）。
2. **命名参数**：`--key value` 或 `--key=value` → `args.key = "value"`（**值永远是字符串**，脚本内需 `parseInt` / `JSON.parse`）。
3. **不支持** 纯 `--flag`（无值）布尔开关；缺值会报 `flag --foo needs a value`。想做布尔，请写 `--foo true` 并在脚本里 `args.foo === 'true'`。
4. `--tab <id>` 显式指定时，**忽略** `@meta.domain` 的自动选 tab / 新开 tab 逻辑。

示例（对应上面的 `example/search`）：

```bash
bb-browser run ./x.js hello --count 20
# args === { query: "hello", arg1: "hello", count: "20" }
```

## 自动 Tab（没传 `--tab` 时）

见 `cmd/bb-browser/main.go` 的 `pickTabForSite`：

1. 脚本有 `@meta.domain` → 在 `tab_list` 中找 URL hostname **等于 domain 或以 `.domain` 结尾**（含子域）的 tab，命中则用它。
2. 没 `@meta` 或 `domain` 空 → 用 daemon 当前焦点 tab（`tab_list.focus || tab_list.tab`）。
3. 有 `domain` 但没匹配 tab → `tab_new https://<domain>/`，**sleep 3s** 粗略等待加载，再取焦点 tab。

含义：**登录态必须已经在这个 Chrome profile 里存在**（daemon 只 attach，不重启、不注入 Cookie）。未登录就需要用户**先在浏览器里登录一次**。

## 外层函数形式 —— 只推荐匿名 async

daemon 包装后实际执行的 JS 等价于：

```javascript
(async function(__args) {
  // <你的 adapter 源码放这里（@meta 注释、export default 会被清洗）>
})( <argsJSON> )
```

对于 `async function(args) { … }`（**匿名**） `internal/site/run.go` 会改写为

```javascript
const __bb_run = async function(args) { … };
return await __bb_run(__args);
```

因此：

- ✅ **推荐**：`async function(args) { return …; }`（匿名；CLI 会重写并调用）。
- ✅ 也可：箭头 IIFE，例如整段源就是 `return (async (args) => { … })(__args);` 这种已能返回值的表达式 —— 但**不推荐**，心智负担高。
- ❌ **避免** 命名 async 声明（如 `async function main(args) { … }`）：CLI **不会**重写、也**不会**自动调用，外层 IIFE 会直接返回 `undefined`。
- ✅ `export default async function(args) { … }`：紧跟 `@meta` 或文件开头的 `export default` 会被剥掉，再走匿名改写。
- ❌ 顶层 ESM `import`：不支持（不是 module 上下文）。需要的话去页面全局找（`window.XXX`）或内联复制。

## 返回值与错误契约

返回 **可 JSON 序列化** 的对象/数组/字符串/数字/布尔。不要返回 DOM 节点、Promise 对象（实际上被 await 会吞 pending 状态）、循环引用。

错误统一用：

```javascript
return { error: '<短描述>', hint: '<可选提示>' };
```

CLI 会打印 `[error] …` 并在 `error/hint` 文本匹配 `401|403|unauthorized|forbidden|not logged|login required|sign in|auth`（`loginHintRe`，`cmd/bb-browser/main.go`）时加一行：

```
(Log in to the site in Chrome for this profile, then retry.)
```

所以：**401/403 请老老实实返回 `{ error: 'HTTP 401', hint: 'please log in' }`**，别把它当成空数据；登录提示才会触发。

## 三层复杂度（按需升级，不要跳级）

### Tier 1 —— Cookie-only（1 分钟级）

目标站的 XHR 只靠 `credentials: 'include'` 就 200。代表：Reddit / V2EX / 大部分自家 GraphQL。见 `Step 3` 骨架。**90% 的 adapter 应该停在这里**。

### Tier 2 —— 头/Token 拼接（3-10 分钟）

要额外头：`authorization: Bearer <常量>` / `x-csrf-token: <Cookie.ct0>` / 自定义 `x-xxx: …`。

```javascript
/* @meta { "name": "twitter/search", "domain": "twitter.com",
          "args": { "query": { "required": true } } } */
async function(args) {
  if (!args.query) return { error: 'Missing argument: query' };
  const csrf = document.cookie.match(/ct0=([^;]+)/)?.[1];
  if (!csrf) return { error: 'CSRF token not found', hint: 'Not logged in?' };
  const resp = await fetch(
    '/i/api/2/search/adaptive.json?q=' + encodeURIComponent(args.query),
    { credentials: 'include', headers: {
      'authorization': 'Bearer <PUBLIC_WEB_BEARER_TOKEN>',
      'x-csrf-token': csrf,
    } }
  );
  if (!resp.ok) return { error: 'HTTP ' + resp.status };
  return await resp.json();
}
```

技巧：

- Bearer 常量从 `bb-browser network requests api --with-body` 里拿第一次 `/i/api/…` 请求头中的值（多数 SPA 内置到 JS bundle；抄一次基本长期稳定）。
- CSRF/XSRF：优先从 `document.cookie` 读；cookie 是 `HttpOnly` 时从网络请求头复制。

### Tier 3 —— webpack / 框架内部状态（10 分钟+，最后手段）

接口加了动态签名、GraphQL `queryId` 每版本变、必须调 SDK 内部函数……需要**抄出 webpack 的 `__webpack_require__`、按稳定字符串特征发现模块**，或**读 Vue/Pinia / React fiber 的 store**。

具体模式、Twitter transaction-id / GraphQL queryId 的发现方法、Vue Pinia / React fiber 访问片段 → **见 [references/patterns.md](references/patterns.md)**。

> 原则：**能用 Tier 1 绝不 Tier 2，能 Tier 2 绝不 Tier 3**。Tier 3 是**最脆弱**的，每次目标站发版都可能挂；写时必须用“按源码特征查找”而非硬编码 module id。

## 常见坑（Go 实现特有）

- **超时 180s**：大页循环抓、翻页、滚动加载别在一次 `run` 里做完；分多条调用或回退到 `fetch` 直接打分页接口。
- **`args.*` 都是字符串**：命名参数没有类型；在脚本里 `parseInt` / `parseFloat` / `JSON.parse`。
- **没有 `--flag` 布尔**：`--dry` 报错，写成 `--dry true` 并 `args.dry === 'true'`。
- **`@meta` JSON 必须合法**：正则用非贪婪 `\{.*?\}` 匹配，字符串里出现 `}` 会误截断；把模板放到 adapter 主体里，`@meta` 里只写元信息。
- **命名 async 不会被调用**：总是用匿名 `async function(args) { … }`。
- **相对 URL 依赖当前 tab 的 origin**：`fetch('/api/x')` 会落到**当前目标 tab**；所以 `@meta.domain` 要对准。
- **登录态是 Chrome 的**，不是 daemon 的：没登录就先让用户手动登录一次。
- **返回 `undefined` / 抛出异常** 在 CLI 端都会呈现为奇怪的空结果；`try/catch` 包住并返回 `{ error: String(e) }`。
- **打印调试**：用 `console.log` 会进 daemon 的 console 观测（`bb-browser console`），不会自动出现在 `bb-browser run` 的 stdout。需要对照值请在返回对象里塞 `_debug` 字段。

## 本地测试清单

```bash
bb-browser health                                     # daemon 活着
bb-browser tab list                                   # 有对应 domain 的 tab
bb-browser run ./x.js "q"                             # 正常路径
bb-browser run ./x.js ""                              # 期望 { error: 'Missing …' }
bb-browser run ./x.js --json "q"                      # 看 RPC 原始包络（error.code/message 也能看）
bb-browser console                                    # 如果脚本里 console.log
bb-browser network requests api                       # 复查脚本发的请求是否和逆向一致
```

## 进阶参考

| 文档                                    | 内容                                                             |
|----------------------------------------|------------------------------------------------------------------|
| [references/patterns.md](references/patterns.md)   | 抗变更模式：结构化 DOM、webpack 模块按特征发现、Vue/Pinia 与 React fiber |
| [references/examples.md](references/examples.md)   | 三层复杂度的完整可运行 adapter 示例（Reddit / Twitter / SPA store） |
| `../bb-browser/SKILL.md`               | 通用 CLI / 自动化能力（open/snapshot/click/fill/fetch/network …）     |
| `../bb-browser/references/daemon-jsonrpc.md`       | 底层 JSON-RPC 方法表（adapter 内不会直连 daemon，但排错要看）       |
