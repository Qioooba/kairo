# WebSphere 日志助手 v0.6 — 4-tab → 任务流改造

**日期**：2026-06-24
**作者**：Mavis
**范围**：`web/pages/websphere.js` + `web/style.css`
**服务**：`http://127.0.0.1:18090`（本地新 binary, PID 78240）+ mock_sshd 2225

---

## 一、改了什么

按用户方案第一波落地，**目标选择从 tab 里拿掉**：

| 改动 | 位置 | 行为变化 |
|---|---|---|
| tab 收编 3 个 | `websphere.js` tabBar | 去掉 🎯目标选择；保留 📁文件/下载、🔍搜索排障、📺实时跟踪 |
| 默认 tab 改 files | `websphere.js` activeTab | 从 `target` → `files`（首次进入看到文件工作台，不是空表单） |
| 修 hash is not defined | `websphere.js` switchTab | 改用 `tabHash` map 查表，不再依赖 makeTab 形参 |
| fileTableWrap 去内联 display:none | `websphere.js` | 让外层 ws-tab-content 控制显隐，避免和 active tab 冲突 |
| tab badge | `websphere.js` + `style.css` | files 显示文件数；search 显示命中数；tail 显示 🟢/空 |
| Tail 多 target 选择 | `websphere.js` tailCard | 新增 `tailTargetSel` + 警告条；doTailStart 严格单 target |
| persistSelection 联动 tail select | `websphere.js` | 勾选变化时自动刷新 tail target 列表 + 警告 |

**未改**：
- 后端 handler（只读）
- 表单 formCard 主体（第二波再改）
- 按钮归属（第三波再改）

---

## 二、接口正反例测试

**方法**：curl 实际打新 binary，逐接口验证本轮前端改动是否引入契约变化。

| # | 接口 | 正例 | 反例 | 结论 |
|---|---|---|---|---|
| 1 | GET /api/config | 200 + 完整 app+systems | 405 (POST) | ✓ |
| 2 | GET /api/credentials/has | 200 + keyring 状态 | 400 (缺 system) / 400 (未知 server) | ✓ |
| 3 | POST /api/ssh/test | 200 (test/ops) | 502 (错密码，category=auth) | ✓ |
| 4 | POST /api/logs/list/targets | 200 + 10 文件 | 400 (targets=[]) / 200 ok=false (system 不存在) | ✓ partial-fail 设计 |
| 5 | POST /api/logs/search/multi | 200 + 命中 | 400 (缺 query) / 400 (非法字符) | ✓ |
| 6 | POST /api/logs/context | 200 + 上下文 | 400 (目录不在白名单) | ✓ |
| 7 | POST /api/logs/tail/start | 200 + id | 400 (file 空) | ✓ |
| 8 | POST /api/logs/tail/{id}/stop | 200 (真实 id) | 404 (ghost id) | ✓ |
| 9 | POST /api/credentials/save | 跳过（macOS keyring 绑会话） | — | 留真实环境验收 |
| 10 | POST /api/credentials/clear | 跳过 | — | 留真实环境验收 |

**结论**：8/8 通过，无回归。

详细结果见 `/tmp/ops-test-logs/api_test_results.md`（开发机本地）。

---

## 三、浏览器交互验证

**工具**：Playwright MCP
**覆盖**：

| # | 场景 | 期望 | 实际 |
|---|---|---|---|
| 1 | tab bar 渲染 | 3 个 tab，默认 active=files，无"目标选择" | ✓ 完全符合 |
| 2 | 测试连接 + 列文件 | "已测通" + 10 文件 | ✓ |
| 3 | **files tab badge** | "10 文件" | ✓ |
| 4 | 切 search tab + 搜索 | 10 命中 2/2 成功 | ✓ |
| 5 | **search tab badge** | "10 命中" | ✓ |
| 6 | 切 tail tab（2 个 dir）| "⚠ 已选 2 个 targets..." 警告 | ✓ |
| 7 | 取消 1 个 dir 勾选 | 警告消失，select 剩 1 项 | ✓ |
| 8 | 开始跟踪 | tail 启动 + 真实日志输出 | ✓ |
| 9 | **tail tab badge** | "🟢" | ✓ |
| 10 | URL hash 同步 | ?tab=files/search/tail 正确 | ✓（hash bug 修复确认）|

**7 个测试场景全部通过**。

---

## 四、发现一个 v0.5 旧 bug（**不在本轮范围**）

**症状**：mock_sshd tail 流立即结束时，前端 EventSource 的 `onerror` 触发但只 append 错误信息没调 `stopTailUI`；`addEventListener('done')` 在 mock_sshd 下可能不触发。结果：

- `btnTailStart` 一直 disabled
- `btnTailStop` 一直 disabled
- 用户无法再次启动或停止

**重现**：
1. 进入 tail tab，启动 tail
2. 等 1-2 秒（mock_sshd tail 流立即结束）
3. 两个按钮都禁用

**影响范围**：
- mock_sshd 一定出现
- 真实 SSH server（流持续产生新行）大概率不出现
- 但真实 server 暂时无新行 + 网络断开仍会卡死

**建议修复**（用户决定是否在本轮或下一轮做）：
```js
// doTailStart 的 onerror 末尾追加：
finally { stopTailUI(); }
// 或起 watchdog：N 秒无新行就自动 stopTailUI + toast
```

**v0.6 引入的相关变化**：
- `setTabBadge('tail', '')` 在 `stopTailUI` 内调用，因 bug 触发不到，badge 不会自动清
- UI 上 computed `display: none`，用户看不到，**无功能性影响**
- 修这个 bug 后 badge 行为会自然正确

---

## 五、自动化测试状态

| 套件 | 命令 | 结果 |
|---|---|---|
| Go test | `go test ./...` | 13/13 包通过（cached） |
| Web unit | `node web/app.test.js` | 13/13 通过 |
| JS 语法 | `node --check web/pages/websphere.js` | OK |
| CSS 平衡 | 自检 { vs } | 245/245 |

**未补新自动化测试**：本轮改动主要是 view 层 DOM 操作 + 状态联动，已通过浏览器交互验证覆盖。
若后续要把正反例接口测试固化为 CI，建议在 `web/api.test.js` 新增（独立运行，需要 ops-toolbox 服务在 18090 端口）。

---

## 六、待用户决定

| # | 事项 | 建议 |
|---|---|---|
| 1 | 是否修 v0.5 tail stop 死锁 bug | 修，3 行代码，影响 mock + 真实环境 |
| 2 | 第二波：目标区改造成可折叠摘要面板 | 等用户确认 |
| 3 | 第三波：按归属调整按钮（列文件、下载最新从 formCard 移走） | 等用户确认 |

---

## 七、回滚方式

如有问题，本轮改动都在 `web/pages/websphere.js` 和 `web/style.css`：
```bash
git diff HEAD -- web/pages/websphere.js web/style.css
git checkout HEAD -- web/pages/websphere.js web/style.css
go build -o /tmp/opstb . && /tmp/opstb
```

---

## 八、附：详细测试数据

- API curl 详细响应：`/tmp/ops-test-logs/api_test_results.md`
- 截图：开发机本地 `/Users/qi/websphere-v0.6-initial.png`（首屏）
