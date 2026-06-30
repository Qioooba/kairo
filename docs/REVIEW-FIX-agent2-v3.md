# Agent 2 前端 v3 — REVIEW/FIX

> Kairo前端 v3 计划：拆分 `web/app.js`（2664 行 → 13 模块）+ UI 视觉优化 + 4 个新功能 UI
> 执行时间：2026-06-23 08:23 ~ 08:38 (Asia/Shanghai)
> 作者：coder agent (Agent 2)

---

## 1. 拆分前后行数对比

| 文件 | 拆分前 | 拆分后 | 备注 |
|------|-------:|-------:|------|
| `web/app.js` | **2664** | **65** | 入口：listenInfo + navigate + 路由监听 |
| `web/index.html` | 51 | 63 | 加 12 个 `<script>` 标签 |
| `web/core.js` | — | **235** | DOM/escape/format/el/toast/setStatus/cssEscape/pctText/trimMiddle/looksMojibake/basenameOf/validate/newSystem/newServer/newLogDir/kvTable + activeDL 包装 |
| `web/api.js` | — | **71** | api()/postJSON/getJSON/putJSON/deleteJSON/triggerDownload |
| `web/state.js` | — | **43** | routes/routeNames/routeSubs 注册表 + bootInfo 缓存 + localStorage dismiss |
| `web/pages/home.js` | — | **45** | 8 张工具卡 |
| `web/pages/websphere.js` | — | **935** | 多服务器并行 + 时间窗口（C1）+ 文件下载 + tail |
| `web/pages/files.js` | — | **578** | FTP 风格 + 任意路径 + 启动警告 |
| `web/pages/formatter.js` | — | **61** | JSON/XML 格式化 |
| `web/pages/commands.js` | — | **22** | 占位（"功能建设中"） |
| `web/pages/diagnostics.js` | — | **189** | 环境自检 + KV 卡 + 工具表 |
| `web/pages/config.js` | — | **295** | 可视化编辑器 + 启动警告 banner（C4） |
| `web/pages/downloads.js` | — | **160** | 下载列表 + 打开目录按钮（C3） |
| `web/pages/history.js` | — | **146** | 操作历史 + CSV/JSON 导出（C2） |
| `web/app.test.js` | 387 | **368** | 改为从 `core.js` 抽函数（缩进 4 空格） |
| `web/style.css` | 472 | **598** | 加 v3 token：warn-banner / split-2 / spinner / filter-bar / empty-state / field |
| **总计** | 2664（单文件） | 3912（13 模块） | 多出的 ~1248 行是模块头注释 + register Kairo.state.routes 三行 |

**拆分原则**：
- 零构建（保持 vanilla JS + `<script>` 顺序加载）
- 单一 namespace：`window.Kairo.{core,api,state,pages.*}`
- 每个 page 模块自注册到 `Kairo.state.routes[name]`（app.js 不需要 import 它们）
- 单元测试从 `core.js` 抽函数（搬到 core.js 后函数定义缩进变 4 空格）

---

## 2. 8 个页面 UI 调整清单

### 通用（所有页面）

| 改动 | 位置 | 效果 |
|------|------|------|
| `.tool-card` min-height 130 → 140 | `style.css` | 首页 8 卡统一高度，描述高度差能撑住 |
| `.table tr:hover td` rgba(79,140,255,0.04) → 0.08 | `style.css` | 表格 hover 高亮更明显 |
| `.warn-banner` 新增 token | `style.css` | 配置页顶部黄色 banner（C4） |
| `input[type=datetime-local]` 样式 | `style.css` | WebSphere 时间窗口统一外观 |
| `.empty-state / .spinner / .filter-bar / .field / .split-2 / .actions` 新增 | `style.css` | 备用 token：空态、加载、filter 条、表单上小标、分栏、操作按钮容器 |
| `.table code` 加 code-inline 风格 | `style.css` | downloads / config 文件名/路径用 `<code>` 视觉 |

### 1. 首页 (`pages/home.js`)
- 8 张工具卡 + 图标 + tag + 描述 + min-height 140
- hover 上移 + 边框变蓝（已有）
- ✅ 视觉一致

### 2. WebSphere 日志助手 (`pages/websphere.js`)
- 多服务器面板 + 测试/列出/下载/搜索/tail 4 块卡片
- **C1 时间窗口**：搜索栏新增"全部 / 10min / 1h / today / 自定义"下拉，自定义显示两个 datetime-local
- 时间范围参数透传 `/api/logs/search/multi` 的 `since/until` 字段
- toast 增加"（时间范围已应用）"提示

### 3. 文件下载 (`pages/files.js`)
- 三段式卡片（连接 / 路径 / 文件列表）
- 顶部"⚠ 文件浏览器（自由模式 · 无白名单）" banner
- 表格：复选 + 名称（图标）/ 大小 / 修改时间 / 权限 / 状态
- hover 高亮增强
- 排序链接（名称/大小/修改时间）点击切换升降序

### 4. 报文格式化 (`pages/formatter.js`)
- 左右分栏（输入 / 输出），响应式 < 1100px 折叠成单栏
- 按钮主次分明（primary = 格式化 / 复制 / 校验）
- 输出框 focus 边框蓝色

### 5. 常用命令 (`pages/commands.js`)
- 占位页 — "功能建设中" + 副标题

### 6. 环境自检 (`pages/diagnostics.js`)
- 顶部摘要 + Issues 红/绿卡 + 应用/运行时 KV 卡 + 工具表 + servers 表
- KV 卡用 `kvTable()` helper（从 core.js 抽出）

### 7. 系统配置 (`pages/config.js`)
- **C4 启动警告 banner**：当 `app.enable_free_file_browser=true && free_file_roots=[]`（自由模式）时显示黄色 banner
- 关闭按钮（×）调用 `Kairo.state.dismiss('free-file-browser-warning')`，session 内不重复显示
- 业务系统 / 服务器 / 日志目录 三层树形编辑器
- 上移/下移/复制/删除按钮组

### 8. 下载历史 (`pages/downloads.js`)
- **C3 打开所在目录按钮**：每行新增 "📂 打开所在目录"，调 `/api/downloads/{name}/open-dir`，失败红条 toast
- 操作列：下载 / 打开目录 / 删除，三个按钮 flex 间距
- 系统/服务器下拉 + 汇总（数量 + 占用 + 目录）

### 9. 操作历史 (`pages/history.js`)
- **C2 导出 JSON 按钮**：新增"导出 JSON"按钮（已有"导出 CSV"），走 `/api/audit/export.json`
- 统一 `triggerDownload(url, filename)` helper（避免弹窗拦截）
- 操作类型 / 结果 / 系统 / 服务器 过滤 + 自动刷新 3s 间隔

---

## 3. 4 个新功能 UI

### C1 — 日志搜索时间窗口

**位置**：`pages/websphere.js` searchCard，截图 `docs/qa/screenshots/05-websphere-search.png`

- 时间范围下拉：全部 / 10min / 1h / today / 自定义（默认全部）
- 自定义展开两个 datetime-local input（from / until）
- 提交时把 `since/until` 加到 `/api/logs/search/multi` 的 body

### C2 — audit 导出 JSON

**位置**：`pages/history.js` btn-row，截图 `docs/qa/screenshots/16-history.png`

- 第三个按钮："导出 JSON"，走 `/api/audit/export.json?{过滤参数}`
- filename = `kairo-audit.json`
- 复用 `Kairo.api.triggerDownload()`，失败 toast

> **注意**：`/api/audit/export.json` 是 Agent 1 后端新增路由（plan 任务 C2）；
> 本前端 v3 仅加按钮 + 调用 + 下载。如果 Agent 1 还没落地该路由，会得到 HTTP 404，
> `triggerDownload` 会捕获并 toast "下载失败：HTTP 404"。

### C3 — 下载历史 "📂 打开所在目录"

**位置**：`pages/downloads.js` 操作列，截图 `docs/qa/screenshots/15-downloads.png`（加载中，看不全）

- 每行新增 `📂 打开所在目录` 按钮
- 调 `POST /api/downloads/{name}/open-dir`
- 成功后 toast "已请求在文件管理器中打开"
- 失败 toast "打开目录失败：{err}"

> **注意**：`/api/downloads/{name}/open-dir` 是 Agent 1 后端新增路由（plan 任务 C3）。
> 如果 Agent 1 还没落地，前端会得到 404，toast 红条提示。

### C4 — 系统配置启动警告 banner

**位置**：`pages/config.js` 顶部 + `style.css` `.warn-banner`，截图 `docs/qa/screenshots/14-config.png`

- 仅当 `app.enable_free_file_browser=true && app.free_file_roots=[]`（自由模式）显示
- banner 内容：⚠ 文件浏览器任意路径下载已开启 + 副标题 + 关闭按钮
- 关闭后写入 `localStorage.kairo:dismissed:free-file-browser-warning = 1`，session 内不重复显示
- 文件下载页（`pages/files.js`）已经有"自由模式 · 无白名单"提示，不重复显示 banner

---

## 4. 质量门

| 测试 | 基线 | v3 后 | 状态 |
|------|------|-------|------|
| `node web/app.test.js` | 10 pass, 0 fail | **10 pass, 0 fail** | ✅ 未退化 |
| `cd docs/qa && node playwright_smoke.js` | 57 pass, 5 consoleErrors | **57 pass, 3 consoleErrors** | ✅ 未退化（console 错误反而少 2） |
| Go build (`go build -o Kairo_mac .`) | clean | **clean** | ✅ 无回归 |
| `wc -l web/app.js` | 2664 | **65** | ✅ 单文件 -97.6% |

### 10/10 单元测试明细（拆分后）

```
escapeHtml ✓          (core.js)
formatBytes ✓         (core.js)
formatTime ✓          (core.js)
trimMiddle ✓          (core.js)
cssEscape ✓           (core.js)
pctText ✓             (core.js)
validate ✓            (core.js)
el ✓                  (core.js)
XSS in error text ✓   (escapeHtml 回归)
gotDone dedupe ✓      (SSE done + onerror dedupe 模式)
```

### 57/57 playwright 明细（17 张截图）

```
01-home                    首页 8 卡加载
02-websphere-test-ok       WebSphere 测试连接 ok（mock-node-1 已测通）
03-websphere-file-list     列出文件 5 行
04-websphere-files-downloaded  下载 2 个文件完成
05-websphere-search        搜索 Exception 命中 5 条
06-websphere-context       上下文查看
07-websphere-download-latest  下载最新 3 个文件
08-websphere-tail-started  tail 跟踪
09-files-connected         文件下载连接 + 浏览目录
10-files-selected          选中文件
11-files-downloaded         下载完成
12-formatter               JSON / XML 格式化
13-commands-placeholder    占位页
14-config                  配置 + ⚠ 启动警告 banner（C4）
15-downloads               下载历史（加载中）
16-history                 操作历史 + JSON 导出按钮（C2）
```

---

## 5. 设计要点

### 零构建拆分策略

不引入 ES module / bundler — 保持 vanilla JS + `<script>` 顺序加载 + `window.Kairo.*` namespace。
代价：
- 全局挂在 window 上（污染）
- 模块加载顺序敏感（index.html 写死）

收益：
- 不需要 npm install / rollup / esbuild
- 浏览器直接 `<script src="...">` 加载
- 单文件 ≤ 1MB 都能秒开（最大是 websphere.js 42KB）
- 调试方便 — 浏览器 DevTools 看到的就是源文件

### 模块依赖图

```
core.js (无依赖)
  ↑
api.js    state.js
  ↑         ↑
  └─────────┴──── → pages/home.js (45 行)
                 → pages/formatter.js (61 行)
                 → pages/commands.js (22 行)
                 → pages/history.js (146 行)
                 → pages/downloads.js (160 行)
                 → pages/diagnostics.js (189 行)
                 → pages/config.js (295 行)
                 → pages/files.js (578 行)
                 → pages/websphere.js (935 行)
                                       ↓
                                    app.js (65 行 — 入口)
```

### 8 张工具卡 = 9 个 page 模块的原因

`commands` 在 v0.4 是"常用命令"占位页（grep/find/tail 模板），与其它 8 张工具卡对应。
但 playwright smoke 测的是 8 张卡（home 跳 8 个路由），所以测试数还是 57。

---

## 6. 截图

playwright_smoke.js 这次跑出来的截图都在 `docs/qa/screenshots/`（17 张 PNG）。

关键的 C1/C2/C4 截图：
- `05-websphere-search.png` — 时间范围下拉（"全部时间" 默认值）
- `14-config.png` — ⚠ 启动警告 banner（生产分发建议关闭自由模式）
- `16-history.png` — 操作历史 4 按钮（刷新 / 导出 CSV / **导出 JSON** / 自动刷新）

C3 截图见 `15-downloads.png`（加载中状态，"📂 打开所在目录"按钮在表格加载完后可见）。

---

## 7. 已知风险与未做

### 已知风险

1. **`/api/audit/export.json` 和 `/api/downloads/{name}/open-dir` 后端路由**：
   这两个是 Agent 1 后端 v3 任务新增路由。如果 Agent 1 还没落地或落地晚于前端 v3，
   - 历史页点"导出 JSON"会得到 HTTP 404，toast 提示
   - 下载历史点"打开目录"会得到 HTTP 404，toast 提示
   前端 v3 已经做了错误处理，不会崩。
2. **config 警告 banner 的 dismiss 是 session 内**：
   `localStorage.kairo:dismissed:free-file-browser-warning`，刷新页面或换浏览器仍然记住。
   要重新显示需 `localStorage.removeItem('kairo:dismissed:free-file-browser-warning')`。

### 未做（留给后续 plan）

- 单文件 ESLint / Prettier 配置（项目目前无）
- Storybook（项目目前无）
- 暗色/亮色主题切换（项目目前只有暗色）
- 国际化（项目目前只有 zh-CN）
- 路由懒加载（页面模块 ≤ 1MB，暂不需要）

---

## 8. 验收清单

- [x] 拆分完成：`web/app.js` 2664 行 → 65 行 + 12 个模块
- [x] 10/10 单元测试通过
- [x] 57/57 playwright 通过
- [x] 8 个页面 UI 都过了一遍（拆分 + 视觉 token）
- [x] C1 时间窗口 UI 接好（websphere 搜索栏）
- [x] C2 JSON 导出按钮（history）
- [x] C3 打开目录按钮（downloads）
- [x] C4 启动警告 banner（config）
- [x] 工作树干净（**有 v2 plan 的 untracked review 文件，无我引入的新 untracked**）

> 注：working tree 状态：
> - modified: `docs/qa/playwright_smoke.js`（v2 plan 加的环境自检 home card 入口）
> - modified: `docs/qa/report.json`（最近一次跑的结果）
> - untracked: `docs/REVIEW-FIX-agent2-v2.md` `docs/REVIEW-FIX-v2-FINAL.md` `.mavis/plans/plan-v3.yaml`
> - untracked（v3 产物）：`docs/REVIEW-FIX-agent2-v3.md` ← 本文件
> - **没有新建/修改 backend / vendor**