# WebSphere 日志助手 v0.7 — 第二波+第三波改造 review

**日期**：2026-06-24
**作者**：Mavis
**范围**：`web/pages/websphere.js` + `web/style.css`
**服务**：`http://127.0.0.1:18090`（v0.7 binary, PID 85984）+ mock_sshd 2225
**前置**：v0.6 FINAL review 已通过（`docs/REVIEW-FIX-websphere-v0.6-FINAL.md`）

---

## 一、本轮目标

v0.6 改造完成后，剩余两件事（v0.6 FINAL 文档"六、待用户决定"中 #2 #3）：

| 波次 | 内容 | 解决什么问题 |
|---|---|---|
| **第二波** | 目标区（formCard）改造成**可折叠摘要面板** | 进入 websphere 默认就要面对一大坨"业务系统/服务器多选/目录多选/凭据"表单，视觉重量过大；用户进 files tab 只想点列出文件，目标区不是焦点 |
| **第三波** | 按钮**按 tab 归属**调整：[📋 列出文件] / [📥 下载最新 N + zip + 目录] 从 formCard 移走 → 落到 files tab 顶部 toolbar | 列出文件 / 下载最新 是 files tab 专属操作，堆在 formCard 底部要切换 tab 时来回滚动，UX 不合理 |

**核心设计原则**（沿用 v0.6）："目标选择"是**前置条件**，所有 tab 都依赖，所以保留常驻可见，但折叠成单行摘要，让用户一眼知道当前选了什么；想改就展开。

---

## 二、改造内容

### 第二波：目标区可折叠摘要

**前**：formCard 永远展开，进入页面就是一坨（业务系统 + 服务器多选 + 目录多选 + 凭据 + 按钮 ~ 6-8 个组件占满首屏）。

**后**：
- 默认折叠 → 顶部一行胶囊：`🎯 信贷生产（模拟） · mock-node-1 · 2 个目录 · 凭据已输入` + `✏️ 修改目标` 按钮
- 展开后 → 完整表单 + `▲ 收起` 按钮
- 状态记忆到 `kairo:last:websphere:target_collapsed`（collapsed / expanded），跨刷新保持
- 摘要**实时同步**：勾选 / 取消 server、勾选 / 取消 dir、输入凭据、保存凭据，都会触发 `renderTargetSummary()` 重渲

### 第三波：按 tab 归属调整按钮

**前**：formCard 底部堆了 `[🔌 测试连接] [📋 列出文件] [📥 下载 N + zip + 目录]`，所有 tab 共享。

**后**：
| 按钮 | 旧位置 | 新位置 | 理由 |
|---|---|---|---|
| 🔌 测试连接 | formCard 底部 | **保留**在 formCard 主体底部 | 通用前置动作（任何 tab 都要先 SSH 通畅才能用），留在 formCard（折叠时也能通过展开访问） |
| 📋 列出文件 | formCard 底部 | **files tab 顶部 toolbar** | files tab 专属 |
| 📥 下载最新 N | formCard 底部 | **files tab 顶部 toolbar** | files tab 专属 |
| 📦 打包为 zip | formCard 底部 | **files tab 顶部 toolbar** | 跟下载最新绑定 |
| 📂 目录 input | formCard 底部 | **files tab 顶部 toolbar** | 跟下载最新绑定 |
| 🔍 搜索 | formCard 底部 | 已在 searchCard（不动） | 已是 search tab 专属 |
| 📺 开始跟踪 / 停止 | formCard 底部 | 已在 tailCard（不动） | 已是 tail tab 专属 |

**files tab 顶部 toolbar 布局**：
```
[📋 列出文件]  下载最新：[最近 3 个文件 ▼]  [☐ 打包为 zip]  [📥 下载]  目录：[本地下载目录（留空走默认）]
```

### 视觉对比

| 区域 | v0.6 | v0.7 |
|---|---|---|
| 顶部 | 4 步走说明卡 | 4 步走说明卡（不变）|
| tab 条 | 3 tab | 3 tab（不变）|
| 目标区 | 永远展开（一坨）| **默认折叠**（单行摘要）|
| files tab | "文件列表"标题 + 空状态 | **顶部 toolbar** + "暂无文件..." placeholder |
| search tab | 搜索表单 | 不变 |
| tail tab | tail 表单 | 不变 |

---

## 三、自动化测试

| 套件 | 命令 | 结果 |
|---|---|---|
| Go test | `go test ./...` | 13/13 包通过 |
| Web unit | `node web/app.test.js` | 13/13 通过 |
| JS 语法 | `node --check web/pages/websphere.js` | OK |
| CSS 大括号 | 自检 `{` `}` | 258/258 平衡 |
| 浏览器 console | Playwright 监控 | 0 errors / 0 warnings |

---

## 四、浏览器交互验证（10 场景）

| # | 场景 | 期望 | 实际 |
|---|---|---|---|
| 1 | 默认折叠态 | 摘要行可见、body 隐藏、toggle="✏️ 修改目标"、aria-expanded=false、files toolbar 完整、placeholder 存在 | ✅ 全过 |
| 2 | 点 toggle 展开 | 摘要隐藏、body 可见、toggle="▲ 收起"、aria-expanded=true、含 [🔌 测试连接] + 系统/服务器/凭据 | ✅ 全过 |
| 3 | 再点 toggle 折叠 | 摘要可见、body 隐藏、toggle 文字回到"✏️ 修改目标"、localStorage 写入 "collapsed" | ✅ 全过 |
| 4 | 点 [📋 列出文件] | placeholder 消失、files card 渲染 2 个 server group 表格、files badge="10 文件" | ✅ 全过 |
| 5 | 切 search tab | search tab active、queryInp="Exception"、[🔍 搜索] 按钮在 search tab 内、files toolbar 隐藏 | ✅ 全过 |
| 6 | 切 tail tab | tail tab active、tailTargetSel 有值、`[📺 开始跟踪]`/`[停止]` 在 tail tab 内、hash 同步 | ✅ 全过 |
| 7 | 摘要实时更新（取消勾 dir） | 摘要从 "2 个目录" → "1 个目录 · 凭据已输入" | ✅ 实时同步 |
| 8 | 跑搜索 | search badge="10 命中"、hits table 渲染 | ✅ 全过 |
| 9 | 启动/停止 tail | tail badge: start 后 = "🟢"，stop 后清空 | ✅ 全过 |
| 10 | 刷新页面，折叠状态记忆 | expanded → reload → 仍展开；collapsed → reload → 仍折叠；localStorage key `kairo:last:websphere:target_collapsed` 正常 | ✅ 全过 |

### 视觉验证

截图 `/Users/qi/websphere-v0.7-final.png`：

```
[4 步走说明卡]
[📁 文件/下载] [🔍 搜索排障] [📺 实时跟踪]
[🎯 信贷生产（模拟） · mock-node-1 · 2 个目录    ✏️ 修改目标]    ← 单行摘要（折叠态）
[📋 列出文件]  下载最新：[最近 3 个文件 ▼]  [☐ 打包为 zip]  [📥 下载]  目录：[...]    ← files toolbar
[暂无文件，先点上方「📋 列出文件」拿到列表。]    ← placeholder
```

---

## 五、全量 9 页面功能测试

| 页面 | 加载 | 关键功能 | 备注 |
|---|---|---|---|
| /home | ✓ | 9 个功能卡片渲染 | viewChildren（不是 .card）|
| /files | ✓ | 连接 + 列 4 个文件 | text-err 是 v0.5 安全警告（设计如此）|
| /formatter | ✓ | JSON 格式化 | |
| /commands | ✓ | 占位页（"第二阶段规划"）| |
| /diagnostics | ✓ | 自检 6 个 card | |
| /config | ✓ | 10 个 input | |
| /downloads | ✓ | 0 行（暂无下载）| |
| /history | ✓ | 47 行审计 | |
| /websphere | ✓ | 完整流程（折叠/展开/3 tab/工具栏/badges）| 见上 10 场景 |

**所有 9 个页面功能正常，无 console 错误**。

---

## 六、最终改动清单

### web/pages/websphere.js（v0.7 增量 +222 行 / 改动 ~295 行）

**第二波新增**：
- `targetCollapsedKey` / `targetCollapsed` 状态 + localStorage 读取
- `targetSummaryBadge` / `targetToggleBtn` / `targetSummaryRow` DOM 元素
- `targetBody` 容器（formCard 主体）
- `toggleTargetPanel()` 函数（切换 + 持久化）
- `applyTargetPanelState()` 函数（应用折叠态 + aria-expanded）
- `renderTargetSummary()` 函数（生成单行摘要：系统/服务器/目录/凭据状态）
- `window.toggleTargetPanel` / `window.renderTargetSummary` 调试钩子

**第二波改**：
- `persistSelection()` 末尾追加 `renderTargetSummary()` 调用
- `api('/api/config').then` 末尾追加 `renderTargetSummary()` 调用
- 首次 view 拼装阶段追加 `applyTargetPanelState()` + `renderTargetSummary()`

**第二波重构**：
- formCard 拆成 `[targetSummaryRow, targetBody]` 两段
- formCardBody 内容（h3/desc/sys/dir/srv/dirs/user/pass/remember/btnTest）改 append 到 targetBody

**第三波改**：
- btnList / btnDownload / dlNSel / dlZipLabel / dlTargetDirInp 从 formCard 底部移除
- 新增 `filesToolbar`（含上述控件）+ `filesWrap` 容器
- `view.appendChild(makeTabContent('files', filesWrap))` 替代原来的 `makeTabContent('files', fileTableWrap)`

**第三波新增**：
- fileTableWrap 初始 append "ws-files-placeholder" 占位 div

### web/style.css（v0.7 增量 +114 行）

**新增**：
- `.ws-target-summary-row` - 摘要行（圆角胶囊、单行、flex 布局）
- `.ws-target-summary-badge` - 摘要文本（单行省略号）
- `.ws-target-toggle` - 切换按钮
- `.ws-target-body` - 折叠主体（margin-top:10px）
- `.ws-target-body > h3:first-child` - 主体内首 h3 margin-top:0
- `.files-toolbar` - files toolbar（flex column + 圆角 + 主题色）
- `.files-toolbar-row` - toolbar 内行（flex + 换行）
- `.files-toolbar-row .lbl` - label 样式
- `.files-toolbar-row #ws-dl-target-dir` - 目录 input 最小宽度
- `.files-wrap > .files-toolbar:first-child` - toolbar 置顶
- `.files-wrap > .card` - fileTableWrap 紧跟 toolbar
- `.ws-files-placeholder` - placeholder 文字 padding

---

## 七、潜在问题 & 已验证排除

| 问题 | 状态 |
|---|---|
| targetSummaryRow 初始没摘要（cfg 没加载完）| ✅ 已用 cfg.then 末尾 + persistSelection 末尾双重 refresh 覆盖 |
| 摘要刷新时用 renderTargetSummary 闭包变量 sysSel/getSelectedTargets/credStatus，会不会 stale？| ✅ 都是 Kairo.core 闭包内引用 + 每次调用读最新值，OK |
| formCard 折叠时，测试连接按钮还能用吗？| ✅ 用户展开 formCard 后才能点测试连接（设计上合理：展开 = 想改配置）|
| files tab toolbar 与 fileTableWrap 排序，谁先谁后？| ✅ toolbar 在前（top 操作）→ table 在后（结果展示）|
| toggle button 文字来回切换会不会引起 reflow？| ✅ 改 textContent + style.display 都是文本级操作，无重排 |
| 多个 tab 切换时 toolbar 会消失/重现？| ✅ 正常行为（files tab 不 active 时 toolbar 跟随 files tab content 隐藏）|
| placeholder 在 doList 后会不会和真实列表冲突？| ✅ renderFileTable 第一行 `innerHTML=''` 把 placeholder 清掉，正常 |
| localStorage key 命名冲突？| ✅ 用 Kairo.core.lastSet 走 `kairo:last:websphere:` 前缀，跟其它 sel/cred 等命名空间一致 |

---

## 八、待用户决定

| # | 事项 | 建议 |
|---|---|---|
| 1 | commit 当前 v0.7 改动 | 建议 commit：2 个文件、净增 363 行、10 场景测试全过 |
| 2 | 关掉测试服务（PID 85984 + mock_sshd）| 留着你随时验收 |
| 3 | /files 页面 `.text-err` 误用（v0.5 留下来的）| 独立任务，改 `.text-warn` |
| 4 | 是否继续 v0.8 / v1.0 | 等用户决定 |

---

## 九、回滚

```bash
# 回滚 v0.7 改动（保留 v0.6）
git checkout HEAD -- web/pages/websphere.js web/style.css
go build -o /tmp/opstb . && /tmp/opstb
```

---

## 十、交付物

- 源码改动：`web/pages/websphere.js` + `web/style.css`
- Review 文档：本文件 `docs/REVIEW-FIX-websphere-v0.7.md`
- 截图：`/Users/qi/websphere-v0.7-final.png`

---

## 十一、流程总结

```
v0.6 完成（3 tab + badge + tail 多 target 警告）
  ↓
用户催"第二波第三波全量测试开始"
  ↓
先 plan：第二波（折叠摘要）+ 第三波（按钮按 tab 归属）
  ↓
一次性写代码（formCard 拆 2 段 + files toolbar + 状态/摘要函数）
  ↓
build + restart 服务（embed FS 必须重启生效）
  ↓
全量测试（Go + web + JS + CSS + 浏览器 10 场景 + 9 页面）
  ↓
FINAL review（本文件）
```

**所有交付任务完成 ✓**