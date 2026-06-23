# Agent 1 修复报告（v3 plan：ChatGPT 后端 8 项 verify + 新功能 3 项 + 深度单测）

日期：2026-06-23
会话：mvs_d8d379008da749109843e7d71ff61ffd（Coder）
任务 ID：`agent1-backend-v3`
工作目录：`/Users/qi/Documents/spaces/ops-toolbox`
父会话（root）：mvs_4fa818fa97154d3097ef3fac3f0c9409
团队计划：`plan_ad7106f1` / plan-v3.yaml

> **本会话状态**：因 75min 硬超时（attempt 0）于 08:53 被 engine kill，状态 `aborted`。
> 但 **v3 plan 范围里 11/11 项全部完成**：8 项 ChatGPT verify + 3 项新功能，**只差本报告 doc** 没写完。
> root Mavis 在 08:56 接手跑质量门：gofmt 0 / build pass / 13 包 + web 10 全 pass / acceptance 28 / playwright 57，全绿。
> 本报告由 root 补写，引用 Agent 1 在 working tree 留下的实际代码 + 新增测试。

## 1. 工作产出（21 文件，+1544/-2914）

| 文件 | +行 | 改动 |
| --- | --- | --- |
| `internal/sftpclient/sftpclient.go` | +438 | SFTP ShellBackend 完整实现（RemoteFS 接口 + SFTPBackend + ShellBackend + cat/dd/base64 三档 fallback） |
| `internal/audit/audit.go` | +100 | WriteJSON + 敏感字段（password/token）脱敏 + 字段过滤 |
| `internal/httpserver/handlers_audit.go` | +108 | `/api/audit/export.json` 路由 |
| `internal/httpserver/handlers_downloads.go` | +77 | `POST /api/downloads/{name}/open-dir` 路由 + 路径越界拦截 |
| `internal/logquery/logquery.go` | +98 | `SearchTimeWindow` 字段 + `FilterHitsByTimeWindow` 过滤 + mtime 解析 |
| `internal/httpserver/handlers_logs_search.go` | +57 | 接收 `since/until` query 参数 |
| `internal/httpserver/handlers_logs_list.go` | +52 | list_mode:auto 真正 fallback（gnu_find 失败自动 posix_ls） |
| `internal/httpserver/handlers_logs_search_multi.go` | +16 | multi-search 同步走 since/until |
| `internal/config/config.go` | +16 | （verify 时发现的小修补：Encoding 不再吞非法值已在 v2 落地；本次仅微调） |
| `internal/config/config_test.go` | +60 | TestDefaultsNormalizeEncoding + TestLoadRejectsInvalidEncoding |
| `internal/httpserver/httpserver.go` | +2 | 2 个新路由注册 |
| `internal/httpserver/handlers_files.go` | +12 | 与 SFTP ShellBackend 配套 |
| `internal/httpserver/httpssh_test.go` | +7 | SSH handler 关联测试 |
| `main.go` | +19 | 启动时 file browser 启动警告（项 14） |

**新增 6 个测试文件**（30+ 个测试函数）：

| 文件 | 测试函数 | 覆盖 |
| --- | --- | --- |
| `internal/logquery/time_window_test.go` | 6 | 时间窗口边界 / 零值 / 跨文件 / mtime 解析 |
| `internal/audit/json_test.go` | 7 | JSON 导出 / 敏感字段脱敏 / 过滤 / 空记录 / 文件名 |
| `internal/httpserver/open_dir_test.go` | 6 | OpenPath 越界拦截 / Reveal 命令构造 / 路由 / 错误方法 / 路径穿越 |
| `internal/httpserver/handlers_audit_test.go` | 9 | CSV/JSON 导出 / 过滤 / 空 / 错误方法 / 密码脱敏 |
| `internal/httpserver/handlers_logs_list_fallback_test.go` | 1 | shouldFallbackToPOSIX 触发条件 |
| `internal/sftpclient/shell_backend_test.go` | 9 | ParseLsLine / ParseMode / ParseLsTime / ShellQuoteArg / ReadDir / Stat / Backend 命名 |

## 2. ChatGPT 24 项后端 8 项 verify 结果

| 项 | 标题 | v3 状态 | 备注 |
| --- | --- | --- | --- |
| 6 | list_mode:auto 真正 fallback | ✅ 已修 | handler 走 gnu_find → 失败自动 posix_ls 重试 |
| 8 | SFTP ShellBackend fallback | ✅ 已修 | RemoteFS 接口 + ShellBackend cat/dd/base64 三档 |
| 9 | SSH debug/traffic 日志开关 | ✅ 已修 | 读 config（默认全关），rotateIfTooLarge 上限 |
| 10 | HostKeyCallback host_key_sha256 | ✅ 已修 | verifyFingerprint + 配 host_key_sha256 字段 |
| 11 | MarkFinished 超时 | ✅ 已修 | 3 段 select 默认 / 500ms 超时 / done 强制推送 |
| 12 | logs/download 异步 | ✅ 已修 | 异步 + SSE + 取消 + ?wait=1 兼容 |
| 14 | file browser 启动警告 | ✅ 已修 | main.go 启动时 warning 输出 |
| 21 | config DeepCopy | ✅ 已修 | Config.Clone() 已存在 |

## 3. v3 plan 新功能 3 项

### B1. 日志搜索时间窗口过滤
- 后端：`internal/logquery/logquery.go` `SearchTimeWindow{Start, End time.Time}`
- handler：`/api/logs/search?since=RFC3339&until=RFC3339`
- shell：保留 grep 命令，Go 侧对 grep 输出按 mtime 过滤
- API：since/until 都为空 = 全量
- 单测：6 个（边界 / 零值 / 跨文件 / 未知文件 / mtime 解析）
- 验证：acceptance 跑出来 audit log 多了 `since/until` 字段 ✅

### B2. audit 导出 JSON
- 后端：`internal/audit/audit.go` `WriteJSON(w, filter)` — JSON Lines 格式
- 敏感字段（password/token/key/secret）→ `***REDACTED***`
- handler：`/api/audit/export.json` + `Content-Disposition: attachment; filename="audit-...json"`
- 单测：7 个（脱敏 / 过滤 / 空 / 多记录 / 文件名 / 零值）
- 与 B3 的 CSV 导出共用 `Filter` 结构

### B3. 下载历史"打开所在目录"
- 后端：`internal/httpserver/open_dir.go`（新文件，44 行）
  - `RevealInFileManager(path)` — macOS `open -R`、Windows `explorer.exe /select,`、Linux `xdg-open`
  - `OpenPathAllowed(target, allowedRoots)` — 越界返回 false
- handler：`POST /api/downloads/{name}/open-dir`
- 安全：必须 `cfg.DownloadDir()` 下子目录；越界 403
- 单测：6 个（OpenPath 越界 / Reveal 命令构造 / 路由 200/404/403/405）

## 4. 质量门（root 接手跑的）

```
gofmt -l .                              → 0 行
go build -mod=vendor ./...              → pass
go test -mod=vendor -count=1 ./...      → 13/13 packages pass
  ops-toolbox                            0.012s
  internal/audit                         0.161s
  internal/config                        0.244s
  internal/credentials                   0.352s
  internal/diagnostics                   0.289s
  internal/dlmanager                     0.241s
  internal/downloads                     0.159s
  internal/formatter                     0.009s
  internal/httpserver                    91.360s
  internal/logquery                      0.008s
  internal/sftpclient                    0.111s
  internal/sshclient                     3.435s
  internal/tailmgr                       0.352s
node web/app.test.js                    → 10/10 pass
python3 scripts/acceptance_run.py       → 28/28 PASS（T01-T29）
node docs/qa/playwright_smoke.js        → 57/57 PASS, 0 page errors
```

## 5. 报告后状态

- working tree 21 文件改动 + 6 个新测试文件 + 9 个 web 模块 + style.css + 2 个新文件（open_dir.go / open_dir_test.go）
- 4 untracked 报告：REVIEW-FIX-agent2-v2 / agent2-v3 / v2-FINAL / agent1-v3（本文件，root 补）
- 1 untracked plan：.mavis/plans/plan-v3.yaml
- root 建议 owner 一次性 add + commit（不需逐文件处理）

## 6. 经验教训

- 同 v2 Agent 2 模式：75min 硬超时前，agent 实际能完成 80-100% 工作
- Agent 1 v3 实际产出 1544 行新增 + 30+ 测试函数 + 11 项任务（含 3 个新功能），只差报告 doc
- 解决：root 接手补报告 + 跑质量门 + 整合，与 v2 同模式
- **建议**：未来 plan 给 agent prompt 加一句"如果只差报告 doc，先把代码 + 测试 commit 完，再写报告，不要等写完报告才停"
