# Snapshot 与 @ref（go-bb-browser）

## 目的

用短引用 **`@1` `@2`…** 代替长 CSS/XPath，减少 Agent 上下文体积；**`click` / `fill`** 可直接消费这些 ref。

## 工作流程

1. **`bb-browser snapshot`**（推荐 **`-i`** 仅可交互元素）。
2. 输出中含多行：`@N [tag ...] "可见文本"`。
3. **`bb-browser click @N`** / **`bb-browser fill @N "文本"`**。
4. **导航或 DOM 大改后重新 snapshot**，ref 与 DOM 绑定，旧 ref 可能失效。

## 实现要点

- `snapshot` 在页面内为节点设置属性 **`__bb_snap_ref`**（值为数字字符串）。
- Daemon 侧 **`click` / `fill`** 支持 **`ref`** 字段；CLI 传入 **`@N`** 会展开为选择器：`[__bb_snap_ref="N"]`。
- 仍可使用原始 **`selector`**（CSS）而不走 snapshot。

## CLI 选项（节选）

```bash
bb-browser snapshot -i          # 仅交互元素相关子树
bb-browser snapshot -c          # 尝试省略空壳结构
bb-browser snapshot -d 5        # 限制深度
bb-browser snapshot -s ".main"  # 限定根节点（CSS）
bb-browser snapshot --json      # JSON-RPC 原始结果
```

## JSON-RPC

方法名：**`snapshot`**。`result` 含 `text`（整段可读树）、`refs`（map：`"1"` → CSS 选择器字符串）、`title`、`url`、`tab`、`seq`。
