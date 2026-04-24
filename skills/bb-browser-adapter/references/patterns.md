# 抗变更模式（bb-browser adapter）

上游站点会频繁改 CSS class、重排 webpack bundle、旋转 GraphQL `queryId`。下列模式是在多个 adapter 维护中沉淀下来、对 **go-bb-browser** `bb-browser run ./x.js` 同样适用的写法。

通用原则：**选语义，别选形状**。语义稳定（`h3`、`operationName`、业务域名字符串）；形状易变（CSS class、module id、变量名）。

---

## 模式 1：结构化 DOM 提取（代替 CSS class 选择器）

**问题**：搜索/列表页（Google、Bing、HackerNews、百度、头条等）每次改版 class 就挂。

**方案**：用语义元素 + 位置关系 + 向上回溯容器，完全不读 class。

```javascript
/* @meta { "name": "google/search", "domain": "www.google.com",
         "args": { "query": { "required": true } } } */
async function(args) {
  if (!args.query) return { error: 'Missing argument: query' };
  const resp = await fetch('/search?q=' + encodeURIComponent(args.query), {
    credentials: 'include'
  });
  if (!resp.ok) return { error: 'HTTP ' + resp.status };
  const doc = new DOMParser().parseFromString(await resp.text(), 'text/html');

  const results = [];
  for (const h3 of doc.querySelectorAll('h3')) {
    const a = h3.closest('a');
    if (!a) continue;
    const link = a.getAttribute('href') || '';
    if (!link.startsWith('http')) continue;

    // 向上回溯直到兄弟里有多个 h3 → 那就是结果列表层
    let container = a;
    while (container.parentElement && container.parentElement.tagName !== 'BODY') {
      const sibs = [...container.parentElement.children];
      if (sibs.filter(s => s.querySelector('h3')).length > 1) break;
      container = container.parentElement;
    }

    // 在容器里、标题块外找一段足够长的文本当 snippet
    const linkBlock = a.closest('div') || a;
    let snippet = '';
    for (const sp of container.querySelectorAll('span, div')) {
      if (linkBlock.contains(sp)) continue;
      const t = (sp.textContent || '').trim();
      if (t.length > 30 && t !== h3.textContent.trim()) { snippet = t; break; }
    }
    results.push({ title: h3.textContent.trim(), url: link, snippet });
  }
  return results.slice(0, 20);
}
```

> 适用：Google / Bing / DuckDuckGo / 搜狗 / HackerNews / Reddit old / 一切“列表页”。

---

## 模式 2：按源码特征发现 webpack 模块（代替硬编码 module id）

**问题**：SPA（Twitter/X、小红书、抖音、某些管理后台）的 `__webpack_require__.m` 里 module id 每次部署都变。`require(1234)` 很快报 undefined。

**方案**：拿到 `__webpack_require__` 后，**遍历 `m`，用源码中稳定的业务字符串**匹配。

```javascript
// 1) 抢出 __webpack_require__（以 Twitter 为例；其它站替换全局 chunk 数组名）
function grabWebpackRequire(chunkGlobal) {
  const chunk = window[chunkGlobal];
  if (!chunk || typeof chunk.push !== 'function') return null;
  let req;
  const key = '__bb_' + Date.now();
  chunk.push([[key], {}, r => { req = r; }]);
  return req || null;
}
const __req = grabWebpackRequire('webpackChunk_twitter_responsive_web');
if (!__req) return { error: 'webpack require not found', hint: 'bundle layout changed?' };

// 2) 按业务字符串 + export 名特征找模块
function findModule(matcher) {
  for (const id of Object.keys(__req.m)) {
    const src = __req.m[id].toString();
    if (matcher(src)) return __req(id);
  }
  return null;
}

const genTx = findModule(src =>
  src.includes('jf.x.com') &&   // 业务域名：改版也不太会改
  /jJ:/.test(src)               // 具名 export：minified export key 通常稳定
);
if (!genTx || typeof genTx.jJ !== 'function') {
  return { error: 'transaction id generator not found', hint: 'webpack signature drifted' };
}
```

```javascript
// 3) 按 operationName 查 GraphQL queryId（operationName 几乎不变；queryId 会变）
function findQueryIdByOp(operationName) {
  const rx = new RegExp('queryId:"([^"]+)",operationName:"' + operationName + '"');
  for (const id of Object.keys(__req.m)) {
    const m = rx.exec(__req.m[id].toString());
    if (m) return m[1];
  }
  return null;
}
const qid = findQueryIdByOp('CreateTweet');
if (!qid) return { error: 'CreateTweet queryId not found' };
```

**选签名的规则**：

- 选 **业务域名 / 业务术语**（`jf.x.com`, `CreateTweet`）；不要选变量名、参数名。
- **组合多个特征**（`includes('A') && includes('B')`）以防串号。
- GraphQL 一律 `operationName → queryId` 这个方向查。
- 对外 API 用 `operationName` 做 key 存到返回值（便于排错）。
- **命中失败就退化**：用 `{ error, hint }` 明确告诉调用者是 webpack 签名漂移，不要静默回空数组。

> 适用：Twitter/X、小红书 PC、抖音 PC、部分 Shopify 后台、公司内 SPA。

---

## 模式 3：Vue/Pinia & React fiber 内部状态

**适用场景**：站点把数据挂在前端 store 而不是暴露接口；逆向失败后的**第三退路**。

**注意**：这是**最脆弱**的模式（改版、SSR 水合时序都会让字段换位置）；优先做 Tier 1/2。

### Vue 3 + Pinia

```javascript
const app = document.querySelector('#app')?.__vue_app__;
if (!app) return { error: 'vue app not found on #app' };
const pinia = app.config.globalProperties.$pinia;
const userStore = pinia?._s?.get('user');
if (!userStore) return { error: 'pinia user store not found', hint: 'logged in?' };
return { id: userStore.id, name: userStore.name };
```

### Vue 2（旧项目）

```javascript
const root = document.querySelector('#app')?.__vue__;
const store = root?.$store;   // Vuex
if (!store) return { error: 'vuex store not found' };
return store.state.user;
```

### React fiber（Reddit new、部分 CMS 后台）

```javascript
const root = document.querySelector('#App') || document.getElementById('root');
const fiber = root?._reactRootContainer?._internalRoot?.current
           ?? root?.[Object.keys(root).find(k => k.startsWith('__reactContainer'))];
if (!fiber) return { error: 'react fiber not found' };

function walk(node, depth = 0) {
  if (!node || depth > 40) return null;
  const ms = node.memoizedState;
  // 每个项目具体字段不同；真机下打印 node.stateNode / node.memoizedProps 观察
  if (ms && ms.posts) return ms.posts;
  return walk(node.child, depth + 1) || walk(node.sibling, depth);
}
const posts = walk(fiber);
```

> 排错：把 `_debug` 塞到返回对象里输出一段 fiber 结构的关键字段（`child?.type?.name`, `memoizedState` 的 key 列表），再据此调整 walk 条件。

---

## 模式 4：分页 & 长任务的“每次一页”

`bb-browser run` 单次超时 180s。想抓 10 页 × 50 条不要在一次 run 里全拿：

```javascript
/* @meta { "name": "example/list", "domain": "www.example.com",
         "args": { "page": { "required": false, "description": "默认 1" } } } */
async function(args) {
  const page = Math.max(1, parseInt(args.page ?? 1, 10));
  const resp = await fetch('/api/list?page=' + page, { credentials: 'include' });
  if (!resp.ok) return { error: 'HTTP ' + resp.status };
  const data = await resp.json();
  return {
    page,
    hasMore: !!data.hasMore,
    nextPage: data.hasMore ? page + 1 : null,
    items: data.items,
  };
}
```

然后 shell 循环：

```bash
page=1
while : ; do
  out=$(bb-browser run ./list.js --page "$page" --json) || break
  echo "$out" | jq '.result.result.items'
  next=$(echo "$out" | jq -r '.result.result.nextPage')
  [ "$next" = "null" ] && break
  page=$next
done
```

---

## 模式 5：`fetch` + 抗速率限制

```javascript
async function(args) {
  async function safeFetch(url, init, attempt = 0) {
    const r = await fetch(url, { credentials: 'include', ...init });
    if (r.status === 429 && attempt < 2) {
      const retry = parseInt(r.headers.get('retry-after') || '2', 10);
      await new Promise(res => setTimeout(res, Math.min(10, retry) * 1000));
      return safeFetch(url, init, attempt + 1);
    }
    return r;
  }
  const r = await safeFetch('/api/x');
  if (!r.ok) return { error: 'HTTP ' + r.status };
  return await r.json();
}
```

> 不要无限重试；180s 预算耗尽就回 `{ error }` 让上层决定是否再 run。

---

## 失败时的诊断习惯

- 永远返回结构化错误：`{ error, hint, _debug? }`，而不是空数组 / throw。
- 把**逆向假设**（用了哪个 queryId、哪段 cookie、哪个 store 字段名）写进 `hint` 或 `_debug`，日后 3 秒就能定位漂移。
- 关键分支打 `console.log`（daemon 侧 `bb-browser console` 可见），不影响返回值。
