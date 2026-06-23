# Agent 2 修复报告（v2 plan 收尾：keyring + 自检 + 诊断 + CSV）

日期：2026-06-23
会话：mvs_c7eae85825bb4cd79e2e792d5046d16a（Coder）
任务 ID：`agent2-frontend-ux-v2`
工作目录：`/Users/qi/Documents/spaces/ops-toolbox`
父会话（root）：mvs_4fa818fa97154d3097ef3fac3f0c9409

> **本会话状态**：因 Token Plan 用量耗尽（errorCode 42212 / 2056）于 03:24 进入 `error` 状态并被 archive。
> 但 **4 项任务全部完成**：3 项已 commit，1 项在 working tree 验证通过。
> root Mavis 会话在本文件最后接手收尾（写报告 + 验证 + 通知 owner）。

## 1. 4 项任务完成情况

| 项 | 标题 | 状态 | commit / 文件 |
| --- | --- | --- | --- |
| 23 | keyring 兼容性配置化 | ✅ DONE | `2475ef3` — `internal/credentials/credentials.go` |
| P3-1 | 自检页 /api/diagnostics | ✅ DONE | `c8d48a4` + `827c134` — 后端 + 前端 |
| P3-2 | 连接诊断模式（Categorize） | ✅ DONE（部分 working tree） | `2475ef3`（errors.go 底层）+ `handlers_ssh.go`（wiring，未 commit） |
| P3-3 | audit CSV 导出 | ✅ DONE | `2475ef3` — `internal/audit/audit.go` WriteCSV + `handlers_audit.go` |

## 2. 测试基线（root 接手后跑的，working tree 含未 commit 改动）

```
gofmt -l .                                    → 0 行
go build -mod=vendor ./...                    → pass
go test -mod=vendor -count=1 ./...            → 13/13 packages pass
  （其中 internal/httpserver 91s 跑 3 个 dial-fail 测试，30s/个）
node web/app.test.js                          → 10/10 pass
git diff                                      → 1 文件 9 行
                                              internal/httpserver/handlers_ssh.go
                                              （P3-2 final touch：audit log + 502 响应带 category/reason/suggestion）
```

## 3. 各项细节

### 项 23：keyring 兼容性配置化

**commit**: `2475ef3`（与 P3-2 / P3-3 / 其他大改动一起提交）

**改动文件**：`internal/credentials/credentials.go` + `main.go` + 前端

- 读 `config.CredentialStore` 字段（`keyring` / `file` / `disabled`）做行为切换：
  - `keyring`（默认）：用 system keyring；失败 fallback 到 file 并打 warn
  - `file`：纯文件存储（无 keyring 依赖的离线环境）
  - `disabled`：完全不存密码（每次必须用户输入）
- `SetMode(...)` / `guardMode(...)` 公共 API：handler 层调用
- 前端 "记住密码" 按钮根据 `credential_store` 显隐（keyring mode 才显示 checkbox）
- 后端审计 + 启动时输出当前 mode + store path（密码不可见）

### P3-1：环境自检页 `/api/diagnostics`

**commits**: `c8d48a4`（后端）+ `827c134`（前端）

**改动文件**：
- `internal/diagnostics/diagnostics.go`（483 行，全新）
- `internal/diagnostics/diagnostics_test.go`（225 行，10 个测试）
- `internal/httpserver/handlers_diagnostics.go`（40 行）
- `internal/httpserver/httpserver.go`（注册路由 + 清理重复 case）
- `web/app.js`（199 行：routes / nav / 首页卡片 / `renderDiagnostics()`）
- `web/index.html`（nav 1 行）

**功能**：
- 聚合本机环境 / 网络 / 配置 / 工具 / 每台 server 连通性
- 应用：监听地址、凭据后端、文件浏览器白名单、SSH 兼容 profile、日志配置、目录
- 运行时：Go 版本 / GOOS-GOARCH / x/crypto 版本 / 磁盘可写 / Goroutine 数
- 本机命令工具：`find` / `grep` / `sed` / `tail` / `unzip` / `ssh` 存在性 + `find -printf` 支持
- 配置 server 连通性：每台 DNS + TCP 握手检查（默认 3s 超时）
- Query 参数：`?check_servers=false` 跳过 server 检查（CI / 不联网环境加速）
- 安全性：所有用户/网络可控字段都走 `textContent`，不进 `innerHTML`
- 路由入口：首页新增"环境自检"卡片（v0.4 新增 tag）

### P3-2：连接诊断模式（Categorize / Diagnose）

**commits**: `2475ef3`（`internal/sshclient/errors.go` 底层）+ `handlers_ssh.go`（未 commit，root 验证通过）

**底层（已 commit）**：`internal/sshclient/errors.go`（`Categorize` + `Diagnose` + 9 个测试）
- 类别：`auth` / `host_key` / `handshake` / `kex` / `network` / `dns` / `timeout` / `kbdint` / `sftp` / `command` / `unknown`
- `Diagnose(err) → {Category, Reason, Suggestion, Raw}`：结构化返回 + 自动脱敏
- 优先级：kbdint（keyboard-interactive / PAM）→ auth → host_key → handshake → kex → network/dns → timeout → sftp/command → unknown
- 兼容 `*net.DNSError` / `context.Canceled` / `context.DeadlineExceeded` / `net.Error.Timeout()`

**Wiring（未 commit）**：`internal/httpserver/handlers_ssh.go`
- `handleSSHTest` 失败时：
  - 审计日志多写 `category` + `reason` 两个字段
  - 502 响应从 `{"error": "..."}` 升级为 `{"error", "category", "reason", "suggestion"}`
- 让前端能拿到结构化原因 + 可读建议

### P3-3：audit CSV 导出

**commit**: `2475ef3`

**改动文件**：`internal/audit/audit.go`（`WriteCSV`）+ `internal/httpserver/handlers_audit.go`（`/api/audit/export.csv`）

- `audit.WriteCSV(w, filter)` 直接把 JSONL 行写成 CSV（不要中间数组，避免大文件爆内存）
- 过滤条件：时间范围 / 操作类型 / 状态 / 关键字（跟 `/api/audit/recent` 共用参数）
- HTTP：`Content-Type: text/csv; charset=utf-8` + `Content-Disposition: attachment; filename="audit-YYYYMMDD-HHMMSS.csv"`
- 安全：CSV 字段做 RFC 4180 转义（逗号 / 引号 / 换行）；password 字段不导出（与 JSONL 一致）
- 前端："操作历史"页加"导出 CSV"按钮，fetch + 触发下载

## 4. 报告后状态

- working tree 含 1 个未 commit 改动：`internal/httpserver/handlers_ssh.go`（P3-2 final touch）
- root 验证：`go build` + `go test` + `node web/app.test.js` + `gofmt -l .` 全绿
- 建议 owner：
  1. 决定 handlers_ssh.go 改动要不要一起 commit（diff 9 行，行为清晰）
  2. 跑最终质量门：acceptance + playwright
  3. 跑一次人工 / 机器混测验证 diagnostics 页
