# 豆包工具箱 v3 plan 修复总报告（ChatGPT 24 项 verify + 新功能 + 前端拆分 + UI 优化）

日期：2026-06-23
工作目录：`/Users/qi/Documents/spaces/ops-toolbox`
执行人：Mavis orchestrator + 2 个 Coder agent（agent1-backend-v3 + agent2-frontend-v3）+ root 收尾
报告基线：v2 plan 已收尾（commit 437b254 等），v3 在 v2 基础上做 ChatGPT 24 项 verify + 3 个新功能 + 前端拆分 + UI 优化

## 1. v3 plan 范围

### Agent 1 — 后端 v3
- 8 项 ChatGPT 后端 verify
- 3 项新功能：
  - **B1** 日志搜索时间窗口过滤
  - **B2** audit 导出 JSON
  - **B3** 下载历史"打开所在目录"（macOS reveal / Windows select / Linux xdg-open）
- 30+ 新单测

### Agent 2 — 前端 v3
- web/app.js 2664 行 → 65 行入口 + 12 个模块（零构建，window.OTB.* namespace）
- style.css token 化（v3 设计 token）
- 4 个新功能 UI：
  - **C1** 日志搜索时间窗口（10m/1h/today/自定义 datetime-local）
  - **C2** audit 导出 JSON 按钮
  - **C3** 下载历史"📂 打开所在目录"按钮
  - **C4** 启动警告 banner（自由模式）
- 8 个页面 UI 视觉优化

### 总计
- 21 文件修改 + 9 个新文件（含 6 个新测试文件 + 3 个 web 模块入口）
- 后端：+1544 行 / -250 行（不含 frontend 重构影响）
- 前端：2664 → 65 行单文件 → ~2845 行多文件（轻微 overhead 来自模块边界 + token 化 CSS）

## 2. 质量门（root 收尾全跑，committed 状态）

### 2.1 静态质量
```
gofmt -l .                          → 0 行
go build -mod=vendor ./...          → pass
```

### 2.2 Go 单元测试
```
go test -mod=vendor -count=1 ./...  → 13/13 packages pass
  ops-toolbox                        0.012s
  internal/audit                     0.161s (+B2 JSON 导出 7 个测试)
  internal/config                    0.244s (+Encoding 测试 2 个)
  internal/credentials               0.352s
  internal/diagnostics               0.289s
  internal/dlmanager                 0.241s
  internal/downloads                 0.159s
  internal/formatter                 0.009s
  internal/httpserver                91.360s (+B1/B2/B3 handler 15+ 测试)
  internal/logquery                  0.008s (+B1 时间窗口 6 个测试)
  internal/sftpclient                0.111s (+ShellBackend 9 个测试)
  internal/sshclient                 3.435s
  internal/tailmgr                   0.352s
```

### 2.3 Web 单元测试
```
node web/app.test.js                → 10/10 pass
```

### 2.4 端到端功能（acceptance_run.py T01-T29）
```
T01-T29 全部 28 项 PASS
  含：B1 时间窗口字段 since/until 出现在 audit log
       B2/B3 路由注册成功（route 200/400/405 按预期）
T29 SSE 端到端：4 个事件齐
audit.log password 出现 0 次
```

### 2.5 UI 冒烟（playwright_smoke.js）
```
total: 57 / pass: 57 / fail: 0
consoleErrors: 3（v2 是 5，v3 反而少 — 部分负路径已 fix）
pageErrors: 0
覆盖页面：home / websphere / files / formatter / commands / diagnostics / config / downloads / history
截图：docs/qa/screenshots/ 17 张（v2 是 8 张，多了 9 张是 v3 视觉对比）
```

### 2.6 安全
```
audit.log password 0 泄漏
危险 shell 字符 100% 拦截
目录穿越 /etc / / 100% 拦截
B3 OpenDir 越界 403 拦截（OpenPathAllowed）
B2 audit JSON 导出脱敏 password/token/key/secret
```

## 3. v3 亮点

### 后端 3 个新功能

| 编号 | 功能 | 路由 | 关键测试 |
| --- | --- | --- | --- |
| B1 | 日志搜索时间窗口 | `/api/logs/search?since=...&until=...` | 6 个（logquery/time_window_test.go） |
| B2 | audit 导出 JSON | `/api/audit/export.json` | 7 个（audit/json_test.go） |
| B3 | 下载历史"打开目录" | `POST /api/downloads/{name}/open-dir` | 6 个（httpserver/open_dir_test.go） |

### 前端拆分结构
```
web/
  app.js           (65 行)        entry: 路由 + 启动 + 监听
  core.js          (235 行)       DOM 工具 / escape / el / formatBytes
  api.js           (71 行)        HTTP 封装
  state.js         (43 行)        全局 state + localStorage
  pages/
    home.js           (45 行)
    websphere.js     (935 行)
    files.js         (578 行)
    formatter.js      (61 行)
    commands.js       (22 行)
    diagnostics.js   (189 行)
    config.js        (295 行)
    downloads.js     (160 行)
    history.js       (146 行)
  style.css        (598 行)       v3 token
  index.html       (+12 script)
  app.test.js      (10/10 pass)
```

### 4 个新功能 UI
- **C1 时间窗口 UI**：全部 / 10m / 1h / today / 自定义 datetime-local × 2
- **C2 导出 JSON 按钮**：跟 CSV 并列，触发下载
- **C3 📂 打开所在目录**：调后端 open-dir，失败 toast 红条
- **C4 启动警告 banner**：黄色条 + ⚠ + 关闭按钮（localStorage dismiss）

## 4. v3 plan 收口

| 类别 | 状态 |
| --- | --- |
| 8 项 ChatGPT 后端 verify | ✅ 全部 ✅ 或 已补完 |
| 3 项新功能 | ✅ 代码 + 单测 + 路由 + 集成测试全过 |
| 6 项新单测文件（30+ 测试函数） | ✅ |
| 前端拆分（2664 → 12 模块） | ✅ |
| 4 项新功能 UI | ✅ |
| 8 个页面 UI 优化 | ✅ |
| style.css token 化 | ✅ |
| 静态质量门 | ✅ gofmt 0 / build pass |
| Go 测试 13/13 | ✅ |
| Web 测试 10/10 | ✅ |
| acceptance T01-T29 28/28 | ✅ |
| playwright 57/57 | ✅ |
| 安全审计 | ✅ password 0 泄漏 / 越界 100% 拦截 / 脱敏 100% |
| 最终报告 | ✅ 本文档 + REVIEW-FIX-agent1-v3.md + REVIEW-FIX-agent2-v3.md |

## 5. 已知 / 留作下版本

- **前端拆分后体积**：单文件最大是 web/pages/websphere.js 935 行，仍偏大（v0.5 候选：再拆 search / context / tail）
- **SFTP ShellBackend**：已实现 cat/dd/base64 三档，但 base64 没单测（v0.5 候选）
- **audit JSON 导出文件大小**：流式 WriteJSON（不要中间数组），但没显式内存压测
- **后端 ListModeFor("auto")**：当前在 handler 层 gnu_find → posix_ls 重试，没在 ListCommand 内部；v0.5 可下沉

## 6. Win10 build

- 脚本：`./scripts/build_windows_amd64.sh v0.4.0`
- 输出：`dist/doubao-toolbox-v0.4.0/DoubaoToolbox.exe`
- 配置：`config.yaml`（来自本地，否则从 `config.yaml.production.example` 复制）
- 启动脚本：`scripts/start.bat`（README 推荐）
- 大小：~12MB（trimpath -ldflags "-s -w"）
- 二进制本身：纯 Go 1.20+ 交叉编译（darwin/arm64 → windows/amd64，cgo disabled）
