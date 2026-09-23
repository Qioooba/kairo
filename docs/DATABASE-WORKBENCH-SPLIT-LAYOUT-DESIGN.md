# 数据库工作台「左右分栏布局」交互设计与改造范围评估

- 状态：**已实施并通过真实验收**（2026-09-23）
- 范围：`web/pages/database.js`、`web/style.css`、`README.md`，同步 `web/app.test.js`、`tests/e2e/tests/14-database-workbench.js`、`tests/e2e/tests/43-sql-editor-large-text.js`
- 目标数据源：Oracle / MySQL 的 SQL 工作区（`renderSQL`）。Redis 工作区（`renderRedis`）不在本次范围。
- 实测证据见第 7 节；与初稿的差异见第 8 节。

---

## 1. 现状与问题

当前 SQL 工作区是**上下两段式**，由 `renderSQL()`（`web/pages/database.js` 约 L3089–L3199）一次性写出 DOM：

```
.db-sql-layout (flex row)
├── aside.card.db-meta            对象树，可拖宽（#db-split-x）
├── div.db-split-x
└── main.db-main (grid, 1 列 2 行)
    ├── section.card.db-editor-card   ← SQL 编辑器（页签 + 工具栏 + textarea）
    │     └── div.db-sql-resizer      ← 上下拖动改编辑器高度（L3637）
    └── section.card.db-results       ← 结果工具栏 + 网格/单行/执行计划
          └── div.db-grid-resizer     ← 上下拖动改表格显示行数（L3696）
```

关键 CSS：`.db-main`（style.css L7572）为 `grid-template-columns: minmax(0,1fr); grid-template-rows: auto minmax(560px,1fr)`；`.db-sql-layout`（L7517）`min-height: 860px`。

现存痛点（用户体验角度）：

1. 宽屏（≥1440px）下 SQL 编辑器只有 190px 左右高，而结果区固定 560px+，横向空间被浪费；
   SQL 一长就要在窄编辑器里反复上下滚动。
2. 已有两个"上下拖动条"（SQL 高度、表格行数）语义不同却长得一样，用户容易混用；
   调高编辑器会挤压结果区，二者相互争夺垂直空间。
3. 对照类工作（改一半 SQL、盯一半结果，例如逐条比对 WHERE 条件命中行）必须来回滚动页面。

因此增加**左右分栏布局**：左列 SQL 编辑器（含工具栏与页签），右列结果面板，中间一条可横向拖动的分隔条，并提供一个布局切换按钮在两种布局间切换。

---

## 2. 交互设计

### 2.1 线框

上下布局（默认，保持现状）：

```
┌───────────────────────────────────────────────────────────────┐
│ 数据源 [prod-oracle ▾]  测试连接  数据源管理  工作台设置   [只读] │
├──────────┬────────────────────────────────────────────────────┤
│ 数据库对象│ 页签1 页签2 ＋页签                    Oracle   [布局] │
│ Schema ▾ │ 执行 取消 │ 网格编辑 提交 回滚 │ 导出 计划 格式化 │…│
│ ▸ 表     │ ┌────────────────────────────────────────────────┐ │
│   T_ORDER│ │ SELECT * FROM t_order WHERE ...                │ │
│ ▸ 视图   │ └────────────────────────────────────────────────┘ │
│          │ ══════════ 拖动改编辑器高度（已有）═══════════════ │
├──────────┼────────────────────────────────────────────────────┤
│          │ 网格│单行记录│执行计划  [过滤…] 复制字段名 显示列  │
│          │ ┌────────────────────────────────────────────────┐ │
│          │ │ ID │ NAME │ AMOUNT │ STATUS │  ...             │ │
│          │ └────────────────────────────────────────────────┘ │
└──────────┴────────────────────────────────────────────────────┘
```

左右布局（新增）：

```
┌───────────────────────────────────────────────────────────────┐
│ 数据源 [prod-oracle ▾]  测试连接  数据源管理  工作台设置   [只读] │
├────────┬───────────────────────┬──────────────────────────────┤
│ 数据库 │ 页签1 页签2 ＋页签     │ 网格│单行记录│执行计划 [布局] │
│ 对象   │ 执行 取消 │ 网格编辑… │ [过滤当前结果…]  复制字段名   │
│ Scheme │ ┌───────────────────┐ │ ┌──────────────────────────┐ │
│ ▸ 表   │ │ SELECT *          │ │ │ ID │ NAME │ AMOUNT │ … │ │
│   T_OR │ │ FROM t_order      │ ┃ │ ├────┼──────┼────────┤ │ │
│ ▸ 视图 │ │ WHERE status=1    │ ┃ │ │  1 │ A001 │  120.5 │ │ │
│        │ │ ORDER BY id       │ ┃ │ │  2 │ A002 │   88.0 │ │ │
│        │ │                   │ ┃ │ │  3 │ A003 │   45.0 │ │ │
│        │ └───────────────────┘ │ └──────────────────────────┘ │
│        │ 事务说明…             │ 第 1 页 ▸ 100 行/页  已返回… │
└────────┴──────────┃──────────┴──────────────────────────────┘
           左右拖动分隔条（新增，6px）
```

### 2.2 布局切换按钮

| 决策点 | 结论 | 理由 |
| --- | --- | --- |
| 位置（推荐） | **结果工具栏左侧**，紧贴 `网格 / 单行记录 / 执行计划` 视图组右侧 | 与"视图模式"同族，用户找结果视图时顺带看到；结果工具栏比编辑器工具栏空闲（编辑器工具栏在 ≤1100px 已有折行压力，见 style.css L8236 的 BUG-02 修复） |
| 位置（备选 A） | 编辑器页签栏右侧、`Oracle` 方言徽标旁 | 离 SQL 更近，但页签多时会挤压页签换行 |
| 位置（备选 B） | `更多` 面板里新增"视图"分区 | 不占宽，但可发现性差，不建议作为唯一入口 |
| 控件形态 | 单按钮：图标 + 文案，文案表示**目标布局**（当前上下时显示"左右布局"，当前左右时显示"上下布局"） | 一次性点到位；文案与点击结果一致，不产生"点了会不会反了"的犹豫 |
| 状态语义 | `aria-pressed` = 当前是否左右布局；`title="切换到左右布局 (Alt+L)"` | 无障碍与悬停解释 |
| 图标 | `layout-rows`（上下）与 `layout-cols`（左右）两个 14×14 SVG，复用 `actionIcon()`（database.js L78–L104） | 与现有图标体系一致，无新增依赖 |
| 快捷键 | `Alt+L`（可在"工作台设置 → 快捷键"改），走既有命令注册表 `db.layout.toggle` | 与 `Alt+O` 对象栏、`Alt+1/2` 结果视图形成一套 |

### 2.3 分隔条（拖拽）行为

- 位置：`.db-main` 内、编辑器卡片与结果卡片之间，新增元素 `<div class="db-split-panes" id="db-split-panes" role="separator" aria-orientation="vertical" tabindex="0">`。
- 视觉：沿用 `.db-split-x` 的语言（6px 命中区，中线 2px，hover/拖动时变主题主色），但增加：
  - `touch-action: none`（平板/触屏可拖）；
  - 拖动中给 `body` 加 `.db-resizing`，全局 `user-select: none; cursor: col-resize`（避免拖着拖着把 textarea 里文字选蓝——现有两个分隔条没做这一步，属已知体验缺口）；
  - 拖动时在把手附近显示实时宽度（如 `460 px`）小标签，松开即隐，避免"拖完才知道多宽"。
- 键盘可达：`←/→` 步进 16px，`Shift+←/→` 步进 64px，`Home/End` 到最小/最大宽度。
- **双击分隔条**：恢复默认比例（编辑器列宽默认 46%）。
- 约束：编辑器列最小 **420px**、结果列最小 420px，两端夹紧后再拖不动（视觉上分隔条到达边界后不再跟手，但不报错、不弹窗）。
- 持久化：`persisted.editor_col_width`（px）；窗口尺寸变化后重新夹紧到合法区间，不写坏数据。

### 2.4 左右布局下的两个面板

| 元素 | 左右布局行为 | 理由 |
| --- | --- | --- |
| `.db-sql-shell` / textarea / 高亮层 | 由固定 px 高度改为**填满列高**（`flex:1`，最小 140px） | 高度已由列决定，再留一个高度拖拽条会与宽度拖拽语义打架 |
| `.db-sql-resizer`（编辑器高度拖拽） | **隐藏** | 冗余控件；保留会导致"拖了没反应"的困惑 |
| `.db-grid-resizer`（表格高度拖拽） | **隐藏** | 同上 |
| `显示 N 行` 输入（`.db-bar-limits-group`） | **禁用** + `title="左右布局下结果表格自动填满高度"` | 避免死控件；切回上下布局时恢复其原值与可用状态 |
| 编辑器工具栏 `.db-editor-bar` | 列宽 < **1000px**（`NARROW_EDITOR_W`）时切 4 行紧凑排布（复用"对象树展开"已验证的那套），按钮组允许换行 | 实测：默认 2 行 3 列网格在 587–850px 列宽下会把"每页返回 1000 行"等文字挤到重叠，必须换排布 |
| 结果表格 `.db-table-scroll` | `flex: 1 1 0` 填满列高，虚拟滚动按视口行数重绘 | 一行不多一行不少，纵向空间全用上 |
| `执行计划` 文本/表格 | `max-height: none; flex: 1`，面板内滚动 | 与网格一致 |
| `单行记录` 视图 | `.db-record-nav` / `.db-record-table` 不参与 flex 收缩，由 `#db-result-grid` 内部滚动 | 记录视图原本靠页面滚动，左右布局下必须自带滚动 |
| 空结果 / 错误提示 | 垂直居中填满列高 | 半屏空白更整齐 |
| `.db-copy-hint`（"单击选行…"提示） | 左右布局下隐藏 | 结果工具栏在这一列里有 9 个元素，窄列必然折行，优先保功能控件 |
| 工作区整体高度 | JS 依据工作区在文档中的真实位置写入精确 px（`innerHeight − topDoc − 24`，下限 560px），CSS 兜底 `calc(100vh - 168px)` → **页面不滚动，两个面板各自内部滚动** | 写死 topbar/内边距在不同主题下会失准；这才是 DBA 工具的预期手感 |
| 对象树 / `#db-inspect` | 不变（仍在上层左右拖动 `#db-split-x`） | 三栏（对象 | 编辑器 | 结果）是最终形态 |
| 对象详情页签（`#db-object-viewer` 可见时） | `.db-main` 加 `is-object`：编辑器独占整行，隐藏分隔条与结果列 | 否则结果列被隐藏后留下空轨道与幽灵分隔条 |

### 2.5 切换时的状态保持

DOM 不重建（只切 `.db-main` / `.db-sql-layout` 的 class 与 CSS 变量），因此 SQL 文本、页签、结果集、列宽、排序、筛选、编辑状态天然保留。需要显式补偿两点：

1. **网格滚动位置**：切换前记录首个可见行号 `row = scrollTop / 31`，切换后 `scrollTop = row * 31` 并立即 `paintGridRows()`；否则从"上下 810px 高"切到"左右 1200px 高"时视口内容会跳动。
2. **列宽夹紧**：从宽屏左右布局切到窄屏时，编辑器列宽按新容器重新夹紧并落盘。

### 2.6 响应式与降级

- 可用工作区宽度（`#db-main` 实测宽度，即工作区宽度 − 对象树）**< 1000px** 时：左右布局**自动降级为上下布局**显示，按钮保持"左右布局"文案但置灰，`title` 与点击提示为"当前窗口过窄，无法左右分栏（至少需要约 1000px 可用宽度）"。
- ≤860px 视口：沿用现有规则（style.css L7768）整个工作区竖排、`#db-split-x` 隐藏；布局按钮同样置灰。
- 判定放在 JS（对 `#db-main` 挂 `ResizeObserver`，只在**宽度**变化时重算，另有窗口 resize 兜底），CSS 保留一条 `@media (max-width: 980px)` 的纯 CSS 安全网。原因：对象树可展开/收起且可拖宽，单靠视口媒体查询会误判。
- 只使用 flex / grid / CSS 变量 / Pointer Events（Chrome 49+ 可用），**不使用**容器查询、`:has()`、`max()/clamp()` 等新特性，与仓库既有的 Chrome 70 兼容区策略（style.css L9363 起）保持一致。

---

## 3. 改造清单（按文件）

### 3.1 `web/pages/database.js`（337 KB，6.3k 行）

| # | 位置（锚点） | 改动 | 类型 |
| --- | --- | --- | --- |
| 1 | `DEFAULT_PREFS` L35–L44 | 新增 `layout: 'stacked'`；`shortcuts` 增加 `layout: 'Alt+L'` | 改 |
| 2 | `persisted` 初值 L45 | 新增 `layout: 'stacked'`、`editor_col_width: 0`（0 = 用默认比例） | 改 |
| 3 | `DB_ACTION_ICONS` L78–L104 | 新增 `layoutRows`、`layoutCols` 两个 SVG 路径 | 改 |
| 4 | 常量区 L15–L26 | 新增 `LAYOUT_STACKED/LAYOUT_COLUMNS`、`EDITOR_COL_MIN = 420`、`RESULT_COL_MIN = 420`、`EDITOR_COL_MAX = 1400`、`EDITOR_COL_DEFAULT_RATIO = 0.46`、`NARROW_EDITOR_W = 1000`、`SPLIT_MIN_WORKSPACE = 1000` | 改 |
| 5 | `normalizePrefs()` | 校验 `layout ∈ {'stacked','columns'}`，非法回落 `stacked` | 改 |
| 6 | 持久化恢复 | `editor_col_width` 数值夹紧（0 或 420–1400），`layout` 归一化 | 改 |
| 7 | `renderSQL()` 模板 | ① 根节点加 `id="db-sql-layout"`、`main` 加 `id="db-main"`；② `db-editor-card` 与 `db-results` 之间插入 `#db-split-panes`（含宽度提示节点）；③ 结果工具栏插入 `#db-layout-toggle`；④ `.db-sql-shell` 补 `id="db-sql-shell"`（原实现靠 `host.querySelector` 兜底，新代码需要稳定句柄） | 改 |
| 8 | `renderSQL()` 事件绑定 | `#db-layout-toggle` 绑定 `toggleLayout()` | 改 |
| 9 | `bindPaneSplitters()` | 尾部改为 `bindPaneSplitter()` + `ensureLayoutResizeWatch()` + `applyLayoutMode()`；`#db-grid-resizer` 的 pointerdown 在左右布局下直接 `return` | 改 |
| 10 | `applyGridHeight()` | 左右布局分支：清空 px 内联高度，交给 CSS flex；上下布局保持原 px 逻辑 | 改 |
| 11 | `onWorkbenchKey()` | 在 `format` 分支之后追加 `layout` 快捷键兜底分支（features 未加载时生效） | 改 |
| 12 | `workbenchCommands()` | 新增 `db.layout.toggle`（`spec('layout','Alt+L')`，scope `editor/workspace/outside`，priority 25） | 改 |
| 13 | `bindSQLEditor()` 的 `ResizeObserver` | 左右布局下**跳过** `shell.style.height` 写入与 `persisted.editor_height` 落盘（否则会把"整列高度"当成用户设定的编辑器高度覆盖掉） | 改 |
| 14 | `openSettings()` | 快捷键区新增 `shortcutField('布局切换','layout')`（保存逻辑按 `state.prefs.shortcuts` 的键自动收集，无需改保存分支） | 改 |
| 15 | `autoResize` 之外的布局函数块（新增 ~200 行） | `setCssVar`（兼容 stub DOM）/ `isColumnsLayout` / `workspaceWidth` / `canSplitColumns` / `editorColEdges` / `currentEditorColWidth` / `updateLayoutToggle` / `updateEditorNarrowState` / `syncObjectLayoutState` / `applyColumnsHeight` / `applyEditorHeightForMode` / `applyLayoutMode` / `rememberGridAnchor` + `restoreGridAnchor` / `toggleLayout` / `setEditorColumnWidth` / `bindPaneSplitter` / `ensureLayoutResizeWatch` / `scheduleLayoutReflow` / `scheduleGridReflow` | 新增 |
| 16 | `hideObjectSession()` / `renderObjectViewer()` | 两处补 `syncObjectLayoutState()`，让 `is-object` 与详情页签可见性同步 | 改 |

### 3.2 `web/style.css`（357 KB，9.5k 行）

新增规则集中在文件末尾一节「数据库工作台：左右分栏布局」（约 167 行），全部以 `.db-main.is-columns` / `.db-sql-layout.is-columns` 为前缀，默认上下布局零影响：

| # | 内容 |
| --- | --- |
| 1 | `.db-result-toolbar .db-layout-btn`（与结果工具栏 28–32px 控件对齐，disabled 视觉） |
| 2 | `.db-sql-layout.is-columns`（`min-height: 560px` + `height: calc(100vh - 168px)` 兜底） |
| 3 | `.db-main.is-columns` 两列网格 `var(--db-editor-col-w, 46%) 6px minmax(420px, 1fr)`；`.is-object` 单列；`#db-results-section[hidden]` 强制 `display:none !important` |
| 4 | `.db-split-panes`（默认 `display:none`，`.db-main.is-columns` 下才 `flex`）+ 中线 + `:focus-visible` + `.db-split-panes-hint` + `body.db-resizing` 全局禁选 |
| 5 | 左列：`.db-editor-card` flex 列、`.db-sql-shell` 填满、`#db-sql`/高亮层 `height:100%!important`、隐藏高度拖拽条、`.db-object-viewer` 填满 |
| 6 | 右列：`.db-results` flex 列、`#db-result-grid` flex + `overflow:auto`、`.db-table-scroll` `flex:1 1 0`、计划/单行记录视图自适应、`.db-result-empty` 填满、隐藏行数拖拽条与 `.db-copy-hint` |
| 7 | `.db-main.is-columns.is-narrow-editor` 编辑器工具栏 4 行紧凑排布（复用对象树展开时那套），按钮组 `flex-wrap` |
| 8 | `@media (max-width: 980px)` 纯 CSS 安全网：整个左右布局规则组回落为上下布局 |

> 主题适配：全部复用现有 `var(--line)/--primary/--bg-*` 变量，xianxia 特异规则无需新增；5 主题实测见第 7 节。

### 3.3 测试与文档

| 文件 | 改动 |
| --- | --- |
| `web/app.test.js`（`testDatabaseWorkbenchLazy`） | 新增 7 条源码契约断言：`#db-layout-toggle`、`#db-split-panes`、`db.layout.toggle`、`editor_col_width`、`SPLIT_MIN_WORKSPACE`、`isColumnsLayout()`，以及「ResizeObserver 必须带左右布局守卫」。注意源码顺序契约（`Escape+acState.open` 必须先于 `cancelQuery()`）——新增分支放在 `format` 分支之后，顺序不变 |
| `tests/e2e/tests/14-database-workbench.js` | 新增用例「左右分栏布局：切换、拖动、刷新记忆与窄屏自动降级」（147 行）：上下基线 → 切换 → 列关系/高度/死控件/页面不滚动/工具栏不溢出 → 双击复位 → 拖动（含 `body.db-resizing` 与宽度提示）→ 极限夹紧 → 刷新记忆 → 窄屏降级与按钮置灰 → 恢复宽屏；末尾把布局偏好复位为 stacked |
| `tests/e2e/tests/43-sql-editor-large-text.js` | `openDatabaseWorkbench()` 增加"若偏好残留为左右分栏则先复位"的前置步骤。该用例断言的是**上下布局**下的编辑器高度契约，左右布局下高度由列决定（内联 px 被 CSS 覆盖），不复位会误报 |
| `tests/database-*.test.js`（手写 DOM stub，`style` 只是普通对象） | 新增的 CSS 变量读写走 `setCssVar()` 防御式封装（无 `setProperty/removeProperty` 时降级），新增 `ResizeObserver` 全部保留 `typeof ResizeObserver !== 'undefined'` 守卫——这两个坑在实施时都真实触发过 |
| `README.md` | 「三段式工作台」能力行与首页卡片描述补一句左右分栏 |
| 本文档 | 设计基线 + 实测证据 |

---

## 4. 代码范围与工作量评估（含实测）

| 批次 | 内容 | 预估 | 实测 |
| --- | --- | --- | --- |
| **P0 核心可交付** | 按钮 + class 切换 + 左右布局 CSS（含两面板填满高度）+ 分隔条拖拽/夹紧/持久化 + 有效布局自动降级 + 网格自适应高度与重绘 + `ResizeObserver` 守卫 | ~180 行 JS / ~75 行 CSS | 已交付 |
| **P1 体验打磨** | `Alt+L` 命令注册 + 快捷键兜底分支 + 设置面板字段 + 分隔条键盘可达/双击复位/实时宽度提示 + 拖动期 `body.db-resizing` + 网格滚动锚点保持 + 死控件禁用与提示 + 窄列工具栏重排 + 对象详情独占整行 | ~80 行 JS / ~30 行 CSS | 已交付 |
| **P2 可选** | 布局按数据源分别记忆、三态（上下/左右/仅结果最大化）、命令面板入口、拖动吸附档位 | ~70 行 | 未做 |

实际 diff（`git diff --numstat`）：

| 文件 | 增 | 删 |
| --- | --- | --- |
| `web/pages/database.js` | 385 | 9 |
| `web/style.css` | 167 | 0 |
| `web/app.test.js` | 12 | 0 |
| `tests/e2e/tests/14-database-workbench.js` | 147 | 0 |
| `tests/e2e/tests/43-sql-editor-large-text.js` | 10 | 0 |
| `README.md` | 2 | 2 |
| **合计** | **722** | **11** |

即：**预估 260 行 JS + 105 行 CSS ≈ 365 行，实测核心+测试约 720 行**，超出部分主要来自三类"不做就会翻车"的加固：① 与内联 px 高度的双轨处理；② 窄列工具栏重排与对象详情独占整行；③ 测试 DOM stub 的兼容封装与 e2e 用例。**改动仍然不涉及后端、接口、权限与 SQL 执行链路**，风险面局限在前端布局层。

### 风险清单（实施后回填）

| 风险 | 等级 | 实际结果 |
| --- | --- | --- |
| 内联 px 高度与 `height:100%` 打架 | 中 | 已按双轨处理（JS 分路 + CSS `!important` 兜底）；另发现 `.db-sql-shell` **原本没有 id**，`q('db-sql-shell')` 一直是 null，导致"切回上下布局恢复编辑器高度"静默失效——补 id 后修复，属本次改造顺带修掉的老问题 |
| 虚拟滚动在高度变化后不重绘 | 中 | 已用 `ResizeObserver(#db-result-grid)` + rAF 去抖重绘；实测 5000 行下切换/拖动后仍只渲染 30–51 行，滚动到底部行号正确 |
| 结果工具栏在窄列折行 | 低 | 结果工具栏靠现有换行规则自适应，实测 420px 列宽下无控件溢出（已加自动化断言）；编辑器工具栏的挤压比预期严重（文字重叠），额外做了 4 行紧凑排布 |
| `editor_height` 记忆被污染 | 低 | 已按清单第 13 条守卫；实测切回上下布局后 `shellH` 恢复 190px（原记忆值） |
| 旧浏览器兼容 | 低 | 未引入容器查询/`:has()`/`max()`；只用 flex/grid/CSS 变量/Pointer Events |
| 既有测试回归 | 低 | `npm test` 21/21 通过；`web/app.test.js` 29/29；DB 三个 jsdom 场景套件全绿 |
| 5 套主题 + xianxia 反色 | 低 | 5 主题截图巡检通过，未新增色值 |

---

## 5. 验收标准（8 项，全部已验证）

1. 结果工具栏可见"左右布局"按钮；点击后编辑器在左、结果在右，分隔条居中可拖。✅
2. 拖动分隔条：实时跟手（+400px 误差 ≤6px）、双向夹紧（编辑器 ≥420px、结果 ≥420px）、拖动中 `body.db-resizing` 且页面文字不可被选中、宽度提示显示后自动消失；双击恢复 46%；键盘 `←/→` 可调。✅
3. 刷新页面后布局与列宽被记忆（`layout=columns`、`editor_col_width` 落库）；`Alt+L` 可取反，并可在设置里改键。✅
4. 左右布局下：编辑器与结果各自内部滚动、页面不滚动；"显示 N 行"与两条上下拖拽条不再可用；5000 行结果仍只渲染 30–51 行（虚拟滚动不破），滚到底部行号正确。✅
5. 窗口收窄到可用宽度 <1000px：自动回上下布局、按钮置灰并给出原因；恢复宽度后回到用户选择的左右布局，列宽仍被记忆。✅
6. 上下布局下的既有行为（对象树拖拽、编辑器高度拖拽、网格行数拖拽、执行/导出/事务/LOB/页签）无变化：`npm test` 21/21、`web/app.test.js` 29/29、DB jsdom 场景套件全绿。✅
7. 5 主题（dark/light/green/hc/xianxia）× 左右布局截图巡检通过，无 JS 运行期错误。✅
8. Redis 工作区不出现布局按钮/分隔条，SQL↔Redis 切换不残留布局类与内联高度。✅

---

## 6. 实施决策（原「待确认」项）

1. **按钮位置**：采纳「结果工具栏，紧贴视图组」。
2. **高度策略**：采纳「页面不滚动，两个面板各自内部滚动」，高度由 JS 按工作区真实位置计算。
3. **第三态（仅结果最大化）**：本轮不做，留 P2。

---

## 7. 实测证据（2026-09-23）

隔离实例：`tmp/split-layout-e2e`（端口 18099，`-web-dir D:\kairo\web`，独立 data/logs/downloads，未触碰 18092/18095 用户实例）。

> 说明：本节三个验收脚本位于被 gitignore 的 `tmp/`，属本轮一次性证据；沉淀进仓库的常驻回归是 7.4 里那条 e2e 用例（`tests/e2e/index.js --grep 左右分栏布局`）。截图保留在 `tmp/split-layout-e2e/shots/`。

### 7.1 定向浏览器验收（`tmp/split-layout-verify.js`，1600×1000）

**48/48 通过**，含：上下基线几何、切换后左右列关系与等高（764px/764px）、死控件禁用、页面不滚动（scrollHeight==innerHeight==1000）、虚拟滚动 51 行、拖动提示与 `db-resizing`、+400px 精确跟手、极限左夹紧 420px、双击复位 46%、刷新记忆 660px、`Alt+L` 双向切换、窄屏降级与按钮置灰、恢复宽屏、对象详情独占整行、`[hidden]` 覆盖、滚到底部行号正确、零 JS 错误。

关键实测值：
- 左列 420px 时：编辑器工具栏自动切 4 行紧凑排布，编辑器与结果工具栏**零控件溢出**；
- 左右布局下结果表格 `clientHeight=587 / scrollHeight=155031`（5000 行）→ 容器内滚动 + 虚拟渲染成立。

### 7.2 主题巡检（`tmp/split-layout-themes.js`）

5/5 通过：`columns=true, sideBySide=true, sameHeight=true, pageScroll=true, pageErrors=[]`；截图 `tmp/split-layout-e2e/shots/theme-{dark,light,green,hc,xianxia}.png`（含对象树展开的三栏形态）。

### 7.3 Redis 隔离回归（`tmp/split-layout-redis-check.js`）

6/6 通过：Redis 工作区无布局按钮/分隔条、无 `#db-main` 残留类；切回 SQL 源后按钮与分隔条恢复。

### 7.4 仓库测试

- `npm test`（`scripts/run-tests.js --unit`）：**Passed=21, Failed=0**；
- `web/app.test.js`：**29 pass, 0 fail**（含 7 条新契约断言）；
- `tests/database-orphan-source.test.js` / `database-shortcut-history.test.js`：全部场景通过；
- `tests/database-update-multi-col-scroll.test.js`、`database-sql-editor-performance.test.js`、`grid-edit-plan-contract.test.js`：通过；
- e2e 新增用例 `node tests/e2e/index.js --grep 左右分栏布局`：**1/1 通过**（1366×900 runner 视口）。

### 7.5 已知的**既有**（非本次引入）失败

在「无真实数据库」的隔离环境里整跑 `--grep 数据库工作台` 会失败 8 条，根因是环境而非本次改动，且已在**未改动的 HEAD 前端**上复现同样结果：

1. `.db-tree-folder` 超时 —— 元数据需要真实库连接；
2. 「工作台设置弹窗居中」等 7 条 `click` 超时 —— 前一条用例在无库环境下没能正常关闭 `#db-manager` 模态，遮罩挡住了后续点击（pristine 复现）。
3. `tests/e2e/tests/43-sql-editor-large-text.js` 的「3500 行大文本」用例 `page.fill` 10s 超时 —— 同样在 pristine 上复现（本机填充 ~110KB 文本超过 Playwright 默认 10s）。

这三类都不在本次改动范围内，未做修复，仅在此记录以免误记为回归。

---

## 8. 实施与初稿的差异

| 项 | 初稿 | 实施 | 原因 |
| --- | --- | --- | --- |
| 编辑器列最小宽度 | 320px | **420px** | 实测 320px 下编辑器工具栏即使重排也会过挤；420px 也正好与结果列最小值对称 |
| 窄列工具栏 | 未考虑 | 新增 `is-narrow-editor`（<1000px 切 4 行排布） | 初稿只算了结果工具栏折行，实测编辑器工具栏 2 行 3 列网格在 587–850px 下出现文字重叠 |
| 工作区高度 | CSS `calc(100vh - 132px)` | JS 按真实位置算 px + CSS 兜底 `calc(100vh - 168px)` | 写死 chrome 高度在不同主题/缩放/页面留白下失准 |
| 结果表格高度 | `height: 100%` | `flex: 1 1 0` | 百分比高度与同级横向滚动条（`.db-table-top-scroll`）会争高，flex 更确定 |
| 对象详情页签 | 未考虑 | 新增 `is-object` 状态 | 详情页签会隐藏结果区，不处理会留下空轨道与幽灵分隔条 |
| `.db-sql-shell` | 假设已有 id | 补 `id="db-sql-shell"` | 该元素原本只有 class，`q('db-sql-shell')` 恒为 null |
| CSS 安全网断点 | 1180px | 980px | 1180px 会与 JS 的 1000px 可用宽度判定在"对象树收起"场景下产生矛盾 |
| 单行记录视图 | 未列出 | 补 `flex: 0 0 auto` + 容器内滚动 | 记录视图原本依赖页面滚动 |
| 测试 DOM stub 兼容 | 未预料 | `setCssVar()` 防御式封装 | stub 的 `style` 是普通对象，没有 `setProperty/removeProperty`，首轮就崩了两个套件 |

初稿的 **P0+P1 工作量预估（2–3 人日）与实际相当**；真正被低估的是"细节加固"的行数（+约 100 行）与"既有实现里隐藏的坑"（无 id 的 shell、stub DOM、测试顺序耦合）。
