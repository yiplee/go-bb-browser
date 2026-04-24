# Adapter 示例（bb-browser run）

三个端到端例子，覆盖三层复杂度。所有示例假设：

- 用户已在 Chrome（debugging port 9222）登录目标站；
- `bb-browserd` 已 attach 到该端口；
- 把 `.js` 存到任意路径（如 `~/adapters/…`），用 `bb-browser run <path>` 调用。

CLI 调用惯例（与 `SKILL.md` 的参数映射对应）：

```bash
bb-browser run ./reddit-search.js "claude code"
bb-browser run ./reddit-search.js "claude code" --limit 25 --sort new
bb-browser run ./reddit-search.js "claude code" --json            # 原始 RPC 包络
bb-browser run ./reddit-search.js "claude code" --tab <id>        # 跳过 domain 自动选 tab
```

---

## Tier 1 —— Reddit search（Cookie-only）

`reddit-search.js`：

```javascript
/* @meta
{
  "name": "reddit/search",
  "description": "Search Reddit posts with current login (subreddit/keyword).",
  "domain": "www.reddit.com",
  "args": {
    "query":     { "required": true,  "description": "搜索关键词" },
    "subreddit": { "required": false, "description": "限定 subreddit，如 'claude'" },
    "limit":     { "required": false, "description": "返回数量 1-100，默认 10" },
    "sort":      { "required": false, "description": "relevance|new|top，默认 relevance" }
  },
  "example": "bb-browser run ./reddit-search.js 'claude code' --limit 25 --sort new"
}
*/
async function(args) {
  if (!args.query) return { error: 'Missing argument: query' };

  const limit = Math.max(1, Math.min(100, parseInt(args.limit ?? 10, 10)));
  const sort  = ['relevance', 'new', 'top'].includes(args.sort) ? args.sort : 'relevance';

  const params = new URLSearchParams({ q: args.query, limit: String(limit), sort });
  const path = args.subreddit
    ? ('/r/' + encodeURIComponent(args.subreddit) + '/search.json?restrict_sr=1&' + params)
    : ('/search.json?' + params);

  let resp;
  try {
    resp = await fetch(path, { credentials: 'include' });
  } catch (e) {
    return { error: 'Network error: ' + String(e && e.message || e) };
  }
  if (!resp.ok) {
    return { error: 'HTTP ' + resp.status, hint: resp.status === 401 ? 'please log in' : '' };
  }
  const data = await resp.json();
  return (data?.data?.children || []).map(c => ({
    title:     c.data.title,
    author:    c.data.author,
    subreddit: c.data.subreddit,
    score:     c.data.score,
    comments:  c.data.num_comments,
    url:       'https://www.reddit.com' + c.data.permalink,
    created:   c.data.created_utc,
  }));
}
```

---

## Tier 2 —— Twitter search（Bearer + CSRF）

`twitter-search.js`：

```javascript
/* @meta
{
  "name": "twitter/search",
  "description": "Search tweets (legacy adaptive search endpoint) using current web session.",
  "domain": "twitter.com",
  "args": {
    "query":     { "required": true,  "description": "搜索关键词" },
    "count":     { "required": false, "description": "返回数量，默认 20" }
  },
  "example": "bb-browser run ./twitter-search.js 'Claude Code' --count 50"
}
*/
async function(args) {
  if (!args.query) return { error: 'Missing argument: query' };

  const csrf = document.cookie.match(/(?:^|;\s*)ct0=([^;]+)/)?.[1];
  if (!csrf) return { error: 'CSRF token (ct0) not found', hint: 'please log in to twitter first' };

  const count = Math.max(1, Math.min(100, parseInt(args.count ?? 20, 10)));
  const qs = new URLSearchParams({
    q: args.query,
    count: String(count),
    result_filter: 'top',
    tweet_search_mode: 'live',
    include_entities: '1',
  });

  const resp = await fetch('/i/api/2/search/adaptive.json?' + qs, {
    credentials: 'include',
    headers: {
      // 公开 web bundle 内嵌的 Bearer；逆向时从 network 头里复制一次即可
      'authorization': 'Bearer <PUBLIC_WEB_BEARER_TOKEN_FROM_BUNDLE>',
      'x-csrf-token': csrf,
      'x-twitter-active-user': 'yes',
      'x-twitter-client-language': 'en',
    },
  });
  if (!resp.ok) {
    return { error: 'HTTP ' + resp.status, hint: resp.status === 401 ? 'please log in' : '' };
  }
  const data = await resp.json();
  const tweets = data?.globalObjects?.tweets || {};
  const users  = data?.globalObjects?.users  || {};
  return Object.values(tweets).map(t => ({
    id: t.id_str,
    text: t.full_text || t.text,
    createdAt: t.created_at,
    user: users[t.user_id_str]?.screen_name,
    likes: t.favorite_count,
    retweets: t.retweet_count,
    url: 'https://twitter.com/' + (users[t.user_id_str]?.screen_name || 'i') + '/status/' + t.id_str,
  }));
}
```

> 如果 Twitter 把 `/i/api/2/search/adaptive.json` 换成 GraphQL（`SearchTimeline`），用 **模式 2**（`references/patterns.md`）的 “按 `operationName` 查 `queryId`” 切换过去。

---

## Tier 3 —— SPA 从 store 读当前用户（Vue + Pinia 示例）

`example-me.js`：

```javascript
/* @meta
{
  "name": "example/me",
  "description": "Read current user info from in-page Pinia store (fallback when no public API).",
  "domain": "www.example-spa.com",
  "args": {},
  "example": "bb-browser run ./example-me.js"
}
*/
async function(_args) {
  try {
    const mount = document.querySelector('#app');
    const app = mount?.__vue_app__;
    if (!app) {
      return {
        error: 'Vue app not found on #app',
        hint: 'the SPA bundle may not be loaded yet; try reloading the tab',
      };
    }

    const pinia = app.config.globalProperties.$pinia;
    const store = pinia?._s?.get('user') || pinia?._s?.get('auth');
    if (!store) {
      const keys = pinia ? [...pinia._s.keys()] : [];
      return {
        error: 'user/auth pinia store not found',
        hint: 'store key drifted',
        _debug: { availableStores: keys },
      };
    }
    if (!store.isLoggedIn && !store.user?.id) {
      return { error: 'Not logged in', hint: 'please log in to the site' };
    }
    return {
      id:    store.user?.id   ?? store.id,
      name:  store.user?.name ?? store.name,
      email: store.user?.email,
      roles: store.roles ?? store.user?.roles ?? [],
    };
  } catch (e) {
    return { error: 'Unexpected: ' + String(e && e.message || e) };
  }
}
```

> 写这种 adapter 前**务必**先在 DevTools Console 里 `document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.keys()` 亲眼看一遍，字段名漂移后一句话就能修。

---

## 复合用法：对每页循环调用（shell + jq）

```bash
page=1
while : ; do
  payload=$(bb-browser run ./list.js --page "$page" --json) || break
  echo "$payload" | jq '.result.result.items[]'
  next=$(echo "$payload" | jq -r '.result.result.nextPage')
  [ "$next" = "null" ] && break
  page=$next
done
```

对应的 `list.js` 见 `patterns.md` 模式 4。

---

## 调试模板（开发中常贴）

在 adapter 里快速加一段**自检返回**，快速确认参数 / 登录态 / 页面上下文：

```javascript
if (args.__probe === 'true') {
  return {
    _probe: true,
    href: location.href,
    origin: location.origin,
    hasCtCookie: !!document.cookie.match(/ct0=/),
    argsReceived: args,
  };
}
```

调用：

```bash
bb-browser run ./x.js --__probe true
```

确认无误后把这段 `if (args.__probe === 'true') { … }` 删掉或注释即可。
