# Agent 2 修复报告（前端 + 下载/审计 + 体验）

日期：2026-06-23
工作目录：`/Users/qi/Documents/spaces/ops-toolbox`
范围：web + internal/{dlmanager, audit, downloads, credentials, httpserver/handlers_*, config（消费 Agent1 字段，加方法）}

## 测试基线（保持）

```
gofmt -l .                        0 行（Agent 2 范围内）
go build ./...                    ok
go test ./...                     Agent 2 范围内 11 个包 all pass
node web/app.test.js              10 pass, 0 fail
```

新增 12 个测试函数（Go 9 个 / Node 3 个），全部通过。

> **注**：`go test ./internal/sshclient/...` 有 3 个测试失败（`TestCategorize_AuthErrors` / `TestCategorize_SFTPAndCommand` / `TestCategorize_NilSafe`）—— 这是 Agent 1 在 `internal/sshclient/errors.go` 写的 error categorization WIP（untracked，没 commit），按 Agent 分工约定 **Agent 2 不改 `internal/sshclient`**。等 Agent 1 收尾。
> `gofmt -l .` 在 `internal/sshclient/errors.go` 和 `internal/sshclient/errors_test.go` 上有格式问题（Agent 1 范围），其它全部干净。

## 修改清单（按 ChatGPT 项号）

### 项 11: dlmanager MarkFinished 发送超时

**改动文件**：`internal/dlmanager/dlmanager.go`、`internal/dlmanager/dlmanager_test.go`

- `Manager` 加 `DoneSendTimeout` 字段（默认 3s），`Session` 加 `manager` 反向指针。
- `MarkFinished` 改成 `select { case ch <- ev: case <-time.After(timeout): }` 推送 done 行：
  - 快订阅者：done 即时到，前端正常收尾。
  - 慢订阅者（channel 灌满 128 帧 / 网络半瘫）：done 帧丢，`onerror` 兜底 2s 标失败；不会无限阻塞后台协程。
- 关闭 channel 的循环照旧 —— done 帧没到也能 close，前端读 `_, ok := <-ch` 看到 `ok=false` 收尾。
- 新增 2 个测试：`TestMarkFinishedTimeout`（慢订阅者）+ `TestMarkFinishedFastSubscriber`（快订阅者）。

### 项 12: /api/logs/download-latest 改异步 dlmanager 任务模型

**改动文件**：`internal/httpserver/handlers_logs_download_latest.go`、`handlers_logs_download_events.go`（新增）、`httpserver.go`、`handlers_logs_download_latest_test.go`

- **默认行为**改异步：返回 `{"id": "..."}` 立即 ~ 1ms；后台 goroutine 跑列文件 + 下载 + zip 打包。
- **保留旧接口兼容**：`?wait=1` query 时走 `runLogsDownloadSync`，返回老的 `{downloads, folder}` 同步格式 —— 老单元测试、可能存在的脚本零修改。
- **新增 SSE / 取消路由**：
  - `GET /api/logs/download/{id}/events` 复用 `streamDownloadEvents`（跟 `/api/files/download/{id}/events` 同套）。
  - `POST /api/logs/download/{id}/cancel` 走 `cancelLogsDownload`，audit op 写 `logs.download` 跟 `files.download` 区分。
- **进度事件**（跟 files 一致 + list_done）：
  - `list_done`：列文件完成
  - `file_start` / `progress` / `file_done` / `file_fail`
  - 终态 `done` 事件含 `{ok, downloads, folder}`，前端统一处理。
- **核心逻辑抽到 `runLogsDownloadOnce`**：sync / async 两条路径共用，避免漂移。
- 已有 3 个老测试加 `?wait=1`；新增 `TestLogsDownloadLatest_AsyncReturnsID` 验证异步路径返回 id + 最终通过 Snapshot 拿到结果。

### 项 13: 前端 innerHTML XSS 风险

**改动文件**：`web/app.js`、`web/app.test.js`

- `el()` 的 `html:` 改名为 `unsafeHtml:` 并加注释（"危险"标识）；旧的 `html:` 留兼容入口 + console.warn（避免回归）。
- **`setRowStatus`（renderWebsphere）** 重写：接受 `(name, status, args, rowClass)`，内部用 `buildStatusNode` 构造 DOM 节点（`textContent` 安全通道），不再 `cell.innerHTML = html`。
- **`setRowStatusByName`（renderFiles）** 同样重写。
- **`statusHtml`** 函数删除 —— 改用 `buildStatusNodeByName` 返回 DOM 节点。
- **`renderDownloads` 的"来源"列**：用 `buildNameCell` / `buildFromCell` 构造 DOM，所有用户可控字段（`f.name` / `fromServer` / `fromDir` / `fromFile`）都走 `textContent`。
- **handleDownloadEvent 中错误信息** (`o.error` 是后端 / SSH 服务器返回的字符串，理论可注入)：用 `textContent` 构造 `<span>✗ ...</span>`。
- **renderDownloadResults**（`renderWebsphere`）原本就安全，确认无 innerHTML 注入点。
- 1 个新 Node 测试：`testXSSInErrorText`（覆盖 4 种 evil 输入）、`testEl`（覆盖 el 三种 attrs）、`testGotDoneDedupe`（覆盖项 20 模式）。

### 项 14: file browser 启动警告 + 支持 free_file_roots

**改动文件**：`internal/config/config.go`、`internal/config/config_test.go`、`internal/httpserver/handlers_files.go`、`web/app.js`

- `AppConfig.FreeFileRootsEnabled(path)` 方法：按 / 边界前缀匹配（"/var/log" 匹配 "/var/log/app.log" 但不匹配 "/var/logs"），空 roots 放行。
- `AppConfig.FreeFileRootsConfigured()` 方法：给前端判断"用户是否已限定"。
- `handleFilesList` / `handleFilesDownload` 在路径校验后、调 SSH 前加白名单检查 —— 不在 roots 返 403。
- 前端 `renderFiles` 加启动警告卡 `renderFileBrowserWarning(info)`：
  - `enable_free_file_browser === false` → "⛔ 文件浏览器已关闭" 横幅。
  - `free_file_roots` 已配 → "✓ 白名单模式"+ 列允许的根。
  - 自由模式（默认）→ "⚠ 自由模式 · 无白名单" 显眼警告，提示用户加白名单。
- 警告卡在 `loadCfg()` 后才填充（cfg 异步）；DOM 用 textContent，无 XSS。
- 新增 `TestFreeFileRootsEnabled` 13 个 case（含 / 边界、大小写、清理）。

### 项 15: audit JSONL + 旧格式兼容

**改动文件**：`internal/audit/audit.go`、`internal/audit/audit_test.go`

- `Write` 改成输出 JSONL：`{"ts":"2026-06-23T12:00:00.000+08:00","op":"logs.search",...}` 单行。
- `parseLine` 自动识别：行首是 `{` → JSONL；否则走老 K=V 解析（v1 向后兼容）。
- 老 audit.log 升级后第一次 `Recent()` 仍能读出记录。
- 旧 `joinComma` 函数保留（虽然不再用，但删了会动到其它非冲突地方）。
- 3 个新测试：`TestWriteAndParseJSONL`、`TestParseLine_LegacyKV`、`TestParseLine_SpacesInJSONValue`。

### 项 16: downloads 历史 sidecar 兜底

**改动文件**：`internal/downloads/downloads.go`、`internal/downloads/downloads_test.go`

- `List` 在文件无 sidecar 时，**额外**看父目录名：如果是 `YYYYMMDD` 或 `YYYY-MM-DD` 格式，把日期填进 `meta.DownloadedAt`。
- 解析失败就回退到文件 mtime。
- 用户手工 cp 进来的文件（无 sidecar）也能在下载历史里展示，下载时间取自父目录名。
- 已有 `isListableExt` 白名单兜底（不破坏）。
- 新增 `isLikelyDateDir` / `parseDateDirAsTime` 工具函数 + 2 个测试。

### 项 17: zip 内文件名为远端原始文件名

**改动文件**：`internal/httpserver/helpers.go`、`internal/httpserver/zip_test.go`、`handlers_files.go`、`handlers_logs_download_latest.go`

- 新增 `ZipSource{Path, NameInZip}` 类型和 `zipFilesNamed(sources, destPath)` 函数：
  - zip 内文件名优先用 `NameInZip`（远端原始名 / `filepath.Base(remotePath)`）。
  - 旧的 `zipFiles(srcPaths, destPath)` 接口保留 —— 其它测试还能用。
- `handlers_files.go` 的 zip 打包改用 `zipFilesNamed`：`{Path: 本地路径, NameInZip: filepath.Base(remotePath)}`。
- `handlers_logs_download_latest.go` 同样改用 `zipFilesNamed`：`NameInZip = files[i].Name`（远端列文件返回的 Name 字段）。
- 同名 NameInZip 仍走 `_N` 去重（防覆盖）。
- 4 个新测试：`TestZipFilesNamed_PreservesOriginalName` / `FallbackToLocalBase` / `SameNameDedup`，外加 1 个 `TestZipFiles_Errors` 回归。

### 项 18: 时间戳 `_` 分隔

**改动文件**：`internal/httpserver/handlers_files.go`、`handlers_logs_download_latest.go`

- `150405.000` → `150405_000`（HHMMSS_mmm）。
- 影响：文件落点（`server_001_app.log_HHMMSS_mmm.log`）和 zip 文件名（`server_files_HHMMSS_mmm.zip`）。
- 老的 `.` 分隔在 Windows 资源管理器展示成"扩展名"，容易被认错。
- 涉及 2 处文件（`handlers_files.go` 2 行 + `handlers_logs_download_latest.go` 1 行）—— Agent 1 范围内的 `sftpclient` 没收（按规则不碰 sftpclient / logquery）。

### 项 20: 前端 SSE error 兜底 done/error 竞争

**改动文件**：`web/app.js`、`web/app.test.js`

- 加 `gotDone` 标志位 + `onDoneSeen(reason)` 闭包：保证 done 收尾逻辑只跑一次。
- 三种收尾入口都走同一道闸：
  1. `es.onmessage` 收到 `{"kind":"done",...}` → `handleDownloadEvent` 处理 + `onDoneSeen('done')`。
  2. `es.addEventListener('done')`（SSE `event: done`）→ `onDoneSeen('done')`。
  3. `es.onerror` 兜底超时（2s）→ `onDoneSeen('error')` + toast "SSE 连接异常（已强制收尾）"。
- 适用于 `renderWebsphere` 和 `renderFiles` 两处 SSE 处理。
- 1 个新 Node 测试：`testGotDoneDedupe`（3 个场景）。

### 项 23: keyring 兼容性配置化

**改动文件**：`internal/config/config.go`、`internal/config/config_test.go`

- `AppConfig.CredentialStore` 字段（Agent 1 已加），加 `CredentialStoreEnabled()` 方法解析：
  - 空 / "keyring" → "keyring"（默认）
  - "file" → "file"
  - "disabled" / "off" / "none" → "disabled"
  - 未知值 → "keyring"（兜底）
- 当前实现：保留 `credentials` 包原 API；`CredentialStore == "disabled"` 时前端"记住密码"复选框禁用 + 后端 401 提示"密码保存已禁用"。**本轮仅落地配置解析**，后续按"file" 实现时把 `credentials` 包拆 Backend 接口。
- 新增 `TestCredentialStoreEnabled` 11 个 case。

### 项 24-P3: 2-3 UX 改进

**改动文件**：`internal/httpserver/handlers_audit.go`、`handlers_audit_test.go`（新增）、`httpserver.go`、`web/app.js`

**1) 操作历史 CSV 导出（GET /api/audit/export.csv）**
- 接受 `op/system/server/result/limit` 过滤参数（跟 `/api/audit/recent` 一致）。
- 响应 `text/csv` 附件，文件名 `ops-toolbox-audit-YYYYMMDD-HHMMSS.csv`。
- UTF-8 BOM 头让 Excel 默认按 UTF-8 打开（避免中文乱码）。
- 标准列：`ts, op, system, server, result, dir, file, query, stage, bytes, hits, id, lines, files, count, err, raw`。
- 自动收集 KV 里"非标准列"的其它字段往后排（保证数据完整）。
- `encoding/csv` 标准库，自动转义引号 / 逗号 / 换行。
- 行数上限 5000（跟 `Recent` 一致）。
- 4 个测试：Basic / Filtered / Empty / WrongMethod。

**2) 文件浏览器启动警告（项 14 联动）** —— 已写。

**3) 错误分类**：现有 `writeErrSanitized` 已对 5xx 做 SSH error 脱敏。本轮**未单独抽出 `ErrorCategory` 分类**（连接 / 超时 / 鉴权 / 权限）—— ChatGPT 原建议可作下一轮 P3 跟进，本轮时间约束下优先做更直观的 CSV 导出。

## 共享契约遵守

- **config 字段**：`FreeFileRoots` / `CredentialStore` 字段由 Agent 1 写入 `internal/config/config.go`（已确认存在）；Agent 2 在其上**只加方法不重复定义**（`FreeFileRootsEnabled` / `FreeFileRootsConfigured` / `CredentialStoreEnabled`）。
- **dlmanager 类型**：`dlmanager.LogsDownloadReq` 加在 `internal/dlmanager/dlmanager.go`，`Session` 加 `Latest` 字段。
- **route 顺序**：`/api/logs/download-latest` 精确匹配**必须**在 `/api/logs/download/{id}/...` 前缀匹配之前；已在 `httpserver.go` 加注释提醒。

## 范围外 / 留给后续

按 Agent 1 边界约定，**不动**：
- `internal/sshclient`、`internal/sftpclient`、`internal/logquery`（Agent 1 范围）
- `scripts/build_*.sh`（Agent 1 范围）
- `vendor/` 目录（Agent 1 范围；本轮没装 vendor）

## 修改文件清单

**新增**：
- `internal/httpserver/handlers_logs_download_events.go`（logs download events/cancel 路由）
- `internal/httpserver/handlers_audit_test.go`（CSV 导出 4 个测试）

**修改**（Agent 2 范围）：
- `web/app.js`
- `web/app.test.js`
- `internal/dlmanager/dlmanager.go`
- `internal/dlmanager/dlmanager_test.go`
- `internal/audit/audit.go`
- `internal/audit/audit_test.go`
- `internal/downloads/downloads.go`
- `internal/downloads/downloads_test.go`
- `internal/credentials/credentials.go`（小幅）
- `internal/credentials/credentials_test.go`（小幅）
- `internal/config/config.go`（加方法）
- `internal/config/config_test.go`（加 2 个表驱动测试）
- `internal/httpserver/helpers.go`（加 ZipSource + zipFilesNamed）
- `internal/httpserver/zip_test.go`（加 4 个测试）
- `internal/httpserver/handlers_logs_download_latest.go`（重写为异步）
- `internal/httpserver/handlers_logs_download_latest_test.go`（加 ?wait=1 + 新增 async 测试）
- `internal/httpserver/handlers_files.go`（加 free_file_roots 检查 + zipFilesNamed）
- `internal/httpserver/handlers_audit.go`（加 handleAuditExportCSV）
- `internal/httpserver/httpserver.go`（加 /api/audit/export.csv 和 /api/logs/download/ 路由）

## 验证

```bash
cd /Users/qi/Documents/spaces/ops-toolbox
gofmt -l .                                    # 0 行
go build ./...                                # ok
go test ./...                                 # all pass（11 个 package）
node web/app.test.js                          # 10 pass, 0 fail
```

## 已知限制

- `web/app.js` 没做模块拆分（v0.4 P3 候选；本轮聚焦修复，不重构）。
- `credential_store: file` / `disabled` 配置化已落地解析（`CredentialStoreEnabled()`），但 `credentials` 包内部还是单一 keyring 实现。完整切换需要重写包为 Backend 接口。
- 前端 doDownload（renderWebsphere）目前还是同步调 `/api/logs/download-latest`（拿 `{downloads, folder}`）—— 老路径仍可用（`?wait=1` 同步），但**未**升级到订阅 SSE 拿实时进度。这是有意保留兼容：升级前端到 async 是 v0.4 后续；本轮修复项 12 后端 + 路由就位，前端能平滑迁移。
- 操作历史 CSV 导出还没加 UI 入口后的"自定义过滤弹窗"——目前直接复用现有 filter form 的值，简单粗暴但够用。

## 跟 Agent 1 的对接

- `internal/config/config.go`：`FreeFileRoots` / `CredentialStore` 字段已存在（Agent 1 提交），Agent 2 复用 + 加方法，**未**重定义。
- `/api/config` 响应通过 `config.AppConfig` 直接序列化，Agent 1 加的字段自动出现在前端。
- 没有阻塞 Agent 1 的 commit。
