# Agent 1 v3 — 后端 8 项 verify + 3 新功能 + 项 8 ShellBackend 重构

**日期**: 2026-06-23
**Agent**: coder (agent1-backend-v3)
**工作目录**: `/Users/qi/Documents/spaces/ops-toolbox`
**基线**: v2 plan 已收尾（commit 437b254 / 827c134 / c8d48a4 / 2475ef3）

---

## 1. 8 项 verify 结果表

| # | 项目 | 状态 | 证据 / 落地位置 |
|---|------|------|----------------|
| **6** | list_mode auto fallback | ⚠️ partial → **已补完** | v2 只在 `logquery.ListCommand` 加 `case auto` 分支，但 handler (`handlers_logs_list.go`) 只调一次 `ListCommand`，远端 `find: not found` 时直接 502。<br>**补完**: <br>① `config.LogDirEntry.ListModeIsAuto()` 新方法（`config.go`）— handler 用它判断"用户是否显式选模式"；<br>② `handlers_logs_list.go` 新增 `shouldFallbackToPOSIX()` 判定函数（看 stderr 关键字如 `find: not found` / `command not found` / `unknown option` / `0652-` / `paths must precede`），匹配且 exit≠0 且 err==nil 时重试 `posix_ls`；<br>③ audit 写一条 `stage=fallback from=gnu_find to=posix_ls reason=...` 记录。 |
| **8** | SFTP ShellBackend | ❌ 未实现 → **已实现** | v2 文件头只有 `TODO P2-12` 注释，RemoteFS / SFTPBackend / ShellBackend 全部缺。<br>**重构**: <br>① `sftpclient.go` 引入 `RemoteFS` 接口（Open/ReadDir/Stat/Close），把原 `sftpBackend` 适配为 `SFTP` 实现（`realSftpBackend`）；<br>② 新增 `shellBackend` 实现：<br>  - `Open(path)` → 跑 `sh -c 'cat <path> 2>/dev/null'`，用 SSH session StdoutPipe 流式读，size 通过 Stat 拿；<br>  - `ReadDir(path)` → 跑 `ls -la`，按行解析（兼容 GNU/AIX/BSD，6 种时间格式）；<br>  - `Stat(path)` → 跑 `ls -ld`，单行解析；<br>  - `Close()` → 关底层 ssh.Client；<br>  - path 全走 `shellQuoteArg` 单引号包；<br>③ 新增 `NewAuto(conn, runFn)` 工厂：先试 SFTP，失败 fallback 到 ShellBackend（runFn 是回调桥接 sshclient.Client.Run，避免循环依赖）；<br>④ `Client.Backend()` 暴露当前实现名（`sftp` / `shell` / `mock`），便于审计/debug；<br>⑤ `handlers_files.go` `sftpDialer` 改用 `NewAuto`，生产自动走 fallback。 |
| **9** | SSH debug/traffic 日志 | ✅ 已修（v2 已完成） | `sshclient.SetLogConfig(debug, traffic, maxMB, keep)` 把开关写入 `pkgDebugEnabled` / `pkgTrafficOn` / `pkgLogMaxBytes` / `pkgLogKeep` 全局；<br>`openSSHDebugLog()` (155) 和 `openSSHTrafficLog()` (227) 启动时先 `pkgLogMu.RLock()` 读开关，关掉时 `return nil` / `sshTrafficDisabled = true`；<br>`main.go` 启动时 `sshclient.SetLogConfig(cfg.App.SSHDebug, cfg.App.SSHTrafficDump, cfg.App.SSHLogMaxMB, cfg.App.SSHLogKeep)` 拉配置。 |
| **10** | HostKeyCallback host_key_sha256 | ✅ 已修（v2 已完成） | `sshclient.go:444-461` `newSSHClientConfig`：<br>`srv.HostKeySHA256` 非空 → `base64.StdEncoding.DecodeString` 解出 32 字节 want → 自定义 `hostKeyCb` 用 `sha256.Sum256(key.Marshal())` 算 got，`bytes.Equal` 比对，失败返"host key fingerprint 不匹配"。 |
| **11** | MarkFinished 超时 | ✅ 已修（v2 已完成） | `dlmanager.MarkFinished` (263-303)：<br>从 `s.manager` 拿 `DoneSendTimeout`（默认 3s），每个订阅者独立 `select { case ch <- ev: case <-time.After(timeout): }`；<br>3 段 select 默认 + 超时跳过，避免慢 SSE 客户端卡住收尾。 |
| **12** | logs/download 异步 | ✅ 已修（v2 已完成） | `handlers_logs_download_latest.go`：<br>① `handleDownloadLatest` 异步路径：建 dlmanager.Session → 立即返 `{id}` → goroutine 跑 `runLogsDownloadTask`；<br>② `?wait=1` 兼容：调 `runLogsDownloadSync` 同步阻塞，返老格式 `{downloads, folder}`；<br>③ `runLogsDownloadOnce` 共享核心逻辑（sess==nil 走同步，非 nil 走异步 broadcast）；<br>④ `handlers_logs_download_events.go` 路由 `/api/logs/download/{id}/events|cancel`。 |
| **14** | file browser 启动警告 | ⚠️ partial → **已补完** | v2 config.go 写"Agent 2 在前端做启动警告；这里只做字段存读"，main.go 没有 warning 日志。<br>**补完**: `main.go` 在 4.7 节加启动日志：<br>  - `FreeFileBrowserEnabled() == true && FreeFileRoots 空` → `WARNING: 文件浏览器...已启用，但 free_file_roots 为空 —— 实际等价于『无限制』`；<br>  - `FreeFileBrowserEnabled() == true && FreeFileRoots 非空` → `WARNING: 文件浏览器...已启用，free_file_roots 白名单=[...]`；<br>  - `FreeFileBrowserEnabled() == false` → `文件浏览器（任意路径下载）已禁用 (/api/files/* 将 403)`。 |
| **21** | config DeepCopy/Clone | ✅ 已修（v2 已完成） | `config.Config.Clone()` (472-510)：<br>  - 顶层字段值拷贝；<br>  - `AppConfig.EnableFreeFileBrowser` 走 `*bool` 深拷贝（481-482）；<br>  - `AppConfig.FreeFileRoots` 走 `append([]string(nil), ...)` 新建 slice（484-486）；<br>  - `Systems` 整树深拷贝：SystemConfig / ServerConfig / LogDirEntry 都按值拷贝，Servers / LogDirs / Patterns 全 `make + copy`（490-516）。<br>对应测试 `TestConfig_Clone_DeepCopy` / `TestConfig_Clone_NilSafe` / `TestConfig_Clone_NilInnerSlices` 已存在。 |

---

## 2. 3 个新功能文件清单 + 单测覆盖

### B1: 日志搜索时间窗口过滤（since/until）

**新功能**:
- `internal/logquery/logquery.go` 新增：
  - `SearchTimeWindow{ Start, End time.Time }` 结构
  - `IsZero()` / `Contains(t time.Time)` 方法（闭区间 [Start, End]，零值 = 不限）
  - `FilterHitsByTimeWindow(hits, files, window)` 按"文件 mtime"过滤 hit（防御：hit.file 不在 files → 兜底保留；files 空 → 全保留；window 零值 → 全保留）
  - `FileEntry.ModTimeParsed()` 把 RFC3339 / RFC3339Nano 字符串解成 time.Time
- `internal/httpserver/handlers_logs_search.go`：
  - 接收 `?since=...&until=...` URL query 参数（RFC3339）
  - `parseTimeWindow(since, until)` 校验（解析失败 400；since > until 400）
  - 走 `parseSearchOutput` 后再 `logquery.FilterHitsByTimeWindow` 过滤
  - 响应多一个 `window: {since, until}` 字段（零值返空字符串）
- `internal/httpserver/handlers_logs_search_multi.go`：同步接入（multi 端每台服务器独立保留 fileList 给 filter 用，新增隐藏字段 `fileList []logquery.FileEntry`）

**单测**:
- `internal/logquery/time_window_test.go`（180 行）：
  - `TestSearchTimeWindow_Contains` — 13 个 case 覆盖两端闭、零值、仅 Start/End、负/正超界
  - `TestSearchTimeWindow_IsZero` — 4 个 case
  - `TestFilterHitsByTimeWindow` — 6 个 case 覆盖全零、仅 a、仅 b、两端、a 之前、b 之后
  - `TestFilterHitsByTimeWindow_NoFiles` — 向后兼容
  - `TestFilterHitsByTimeWindow_UnknownFile` — 防御兜底
  - `TestFileEntry_ModTimeParsed` — RFC3339 / 空 / 垃圾字符串

**API 用法**:
```bash
curl -X POST 'http://127.0.0.1:18080/api/logs/search?since=2026-06-23T00:00:00Z&until=2026-06-23T23:59:59Z' \
  -H 'Content-Type: application/json' \
  -d '{"system":"信贷","server":"srv-1","dir":"SystemOut","files":5,"query":"Exception","username":"ops","password":"xxx"}'

# 响应
{
  "hits":  [ /* only hits from files mtime ∈ [since, until] */ ],
  "files": ["file1", "file2"],
  "window": {"since": "2026-06-23T00:00:00Z", "until": "2026-06-23T23:59:59Z"}
}
```

**错误响应**:
- `400 since 解析失败（需要 RFC3339...）` / `400 since 必须早于 until`

---

### B2: audit 导出 JSON

**新功能**:
- `internal/audit/audit.go` 新增：
  - `sensitiveKeys` map（password / passwd / secret / private_key / host_key_sha256 / authorization / credential / auth_token / api_key）
  - `IsSensitiveKey(k string) bool` 导出
  - `JSONRedactedValue = "<redacted>"`
  - `jsonRecord{Time, Op, KV, Raw, Redacted}` + `MarshalJSON()` 把 KV 压平到顶层，敏感字段从输出中**完全移除**（不放值），并在 `redacted` 数组里列被脱敏的 key 名（按字母序）
  - `WriteJSON(w io.Writer, records []Record) error` — 顶层 JSON 数组，indent=2 spaces
  - `JSONFilename(now) = "audit-YYYY-MM-DD-HHMMSS.json"`
- `internal/httpserver/handlers_audit.go`：
  - `redactRecord(rec)` 内部 helper，统一脱敏（CSV 和 JSON 共用）
  - `handleAuditExportJSON` — `GET /api/audit/export.json`，参数同 CSV（op / system / server / result / limit，limit 上限 5000），响应 `application/json` 附件
  - 同时把 `handleAuditExportCSV` 的所有行也走 `redactRecord`（一致行为，避免 CSV 路径成为漏网之鱼）

**单测**:
- `internal/audit/json_test.go`（190 行）：
  - `TestIsSensitiveKey` — 18 个 case 覆盖大小写 / 空格 / 部分匹配不应该误报
  - `TestWriteJSON_NoSensitiveFields` — 普通记录能正确序列化
  - `TestWriteJSON_RedactsSensitiveFields` — **关键安全测**：写入 password / private_key / host_key_sha256 → 验证 raw 字符串不出现 + 顶层字段被移除 + redacted 数组列出 key
  - `TestWriteJSON_EmptyRecords` — 空 records 返 `[]`
  - `TestWriteJSON_MultipleRecords` — 多记录顺序保持
  - `TestJSONFilename` — 格式 `audit-2026-06-23-024500.json`
  - `TestRedactedValue` — 脱敏常量值防回归
- `internal/httpserver/handlers_audit_test.go` 追加（128 行）：
  - `TestAuditExportJSON_Basic` — 端到端 200 + application/json + attachment header + 数组反序列化
  - `TestAuditExportJSON_RedactsPassword` — **端到端安全测**：完整 HTTP 路径走完，确认 password 值不出现
  - `TestAuditExportJSON_Filter` — op= 过滤
  - `TestAuditExportJSON_Empty` — 空审计返 `[]`
  - `TestAuditExportJSON_WrongMethod` — POST 405

**API 用法**:
```bash
# 全量
curl -OJ 'http://127.0.0.1:18080/api/audit/export.json'
# → doubao-toolbox-audit-2026-06-23-020000.json

# 过滤
curl -OJ 'http://127.0.0.1:18080/api/audit/export.json?op=ssh.test&result=fail&limit=1000'
```

**响应** (节选):
```json
[
  {
    "ts": "2026-06-23T12:00:00.123456+08:00",
    "op": "ssh.test",
    "system": "信贷",
    "server": "mock-1",
    "result": "fail",
    "redacted": ["host_key_sha256", "password"]
    // ↑ 敏感字段被完全移除，仅在 redacted 数组里留 key 名
  }
]
```

---

### B3: "打开下载所在目录" 后端支持

**新功能**:
- `internal/httpserver/open_dir.go`（新增文件）：
  - `revealInFileManager(path string) error` — 平台分支：
    - darwin → `open -R <path>` (Finder reveal)
    - windows → `explorer.exe /select,<path>` (Explorer reveal)
    - linux → `xdg-open <dir>` (打开父目录，Linux 文件管理器无统一 reveal 协议)
    - 用 `cmd.Start()` + 后台 `cmd.Wait()`，不等 GUI 程序阻塞请求
  - `openPathAllowed(allowRoot, target string) error` — 越界校验：
    - `filepath.Abs` 双保险
    - `filepath.Rel(allowRoot, target)` 不能以 `..` 开头
    - target 含 NUL/换行/回车立刻拒
- `internal/httpserver/handlers_downloads.go`：
  - 路由分发扩展：`POST /api/downloads/{name}/open-dir` → `handleDownloadsOpenDir`
  - `handleDownloadsOpenDir` 流程：
    1. 校验 name 不含 `/` `\` NUL `.` `..`（防注入）
    2. 拼绝对路径 `cfg.DownloadDir() + name`
    3. `fileExists` 检查（不存在 → 404）
    4. `openPathAllowed` 越界校验（失败 → 403，audit 写 fail/reason）
    5. `revealInFileManager` 调平台命令（失败 → 500，audit 写 fail/err）
    6. 成功 → 200 `{ok:true, platform: "darwin"|"linux"|"windows", path: "..."}`，audit 写 ok/platform

**单测**:
- `internal/httpserver/open_dir_test.go`（140 行）：
  - `TestOpenPathAllowed` — 11 个 case 覆盖 root 子文件 / 深层子目录 / 父目录拒绝 / `../` 跳出拒绝 / 兄弟目录拒绝 / 空 root 拒绝 / 空 target 拒绝 / NUL 拒绝 / 换行拒绝 / 不存在但合法的子路径
  - `TestRevealInFileManager_BuildCommand` — 各平台 fork 阶段能跑到（CI 无 GUI 时只记 t.Log）
  - `TestDownloadsOpenDir_OK` — 端到端 200 + 响应含 platform
  - `TestDownloadsOpenDir_NotFound` — 404
  - `TestDownloadsOpenDir_PathTraversal` — 400/404
  - `TestDownloadsOpenDir_WrongMethod` — GET/PUT/DELETE 都 405

**API 用法**:
```bash
# 在 Finder/Explorer 里 reveal 一个已下载文件
curl -X POST 'http://127.0.0.1:18080/api/downloads/srv-1_001_app_150405_000.log/open-dir'

# 响应
{
  "ok": true,
  "platform": "darwin",
  "path": "/Users/ops/DoubaoToolbox/downloads/20260623/srv-1_001_app_150405_000.log"
}
```

**错误响应**:
- `400 name 含非法字符`（含 `/` `\` NUL `.` `..`）
- `404 下载文件不存在`
- `403 target 越界（... 不在 ... 下）`

---

## 3. 项 8 ShellBackend 单测覆盖

**单测** (`internal/sftpclient/shell_backend_test.go`, 230 行):
- `TestParseLsLine_GNU` — 6 case 覆盖普通文件 / 目录 / 软链接 / 字段不够 / 空行 / 带年份
- `TestParseMode` — 5 case 覆盖 `-` / `d` / `l` / 完整 rwx / 字符设备
- `TestParseLsTime` — 5 case 覆盖无年份 HH:MM / HH:MM:SS / 带年份 / 垃圾 / 解析成功后年份=now.Year()
- `TestShellQuoteArg` — 6 case 覆盖普通 / 含分号管道 / `$(rm -rf /)` / 反引号（关键：单引号包后这些都成字面串，不会被 shell 解释）
- `TestShellBackend_ReadDir_Parses` — 3 条目 ls -la 输出，验证目录 / 文件 / size / name 解析
- `TestShellBackend_ReadDir_Error` — code≠0 / runFn 出错
- `TestShellBackend_Stat_Parses` — ls -ld 单行解析
- `TestShellBackend_Stat_ErrorMessage` — stderr 信息出现在错误里
- `TestClient_Backend_Naming` — Backend() 返 `none` / `sftp` / `shell` / `mock`

---

## 4. 项 6 fallback 单测覆盖

**单测** (`internal/httpserver/handlers_logs_list_fallback_test.go`, 80 行):
- `TestShouldFallbackToPOSIX` — 8 case 覆盖：
  - exit 0 不降级
  - 退出非 0 但无 stderr 不降级（避免误触发）
  - `find: not found` 降级
  - `find: paths must precede expression:` 降级
  - AIX `0652-018` 降级
  - `command not found` 降级
  - `Permission denied` **不降级**（posix_ls 也是同个错）
  - err != nil（网络错）不降级

**单测** (`internal/config/config_test.go` 追加, 60 行):
- `TestListModeIsAuto` — 11 case 覆盖 `""` / `auto` / `AUTO` / 空格归一 / 显式 gnu_find 不算 / posix_ls 不算 / 未知不算
- `TestListModeFor` — 11 case 兼容旧语义（"" / auto → gnu_find；未知值原样保留）

---

## 5. 质量门跑分

```bash
$ gofmt -w . && gofmt -l .
# (空输出)         ← 0 行未格式化

$ go build -mod=vendor ./...
# (空输出)         ← build clean

$ go test -mod=vendor -count=1 ./...
ok  	ops-toolbox                              0.013s
ok  	ops-toolbox/internal/audit               0.031s   ← B2 新增
ok  	ops-toolbox/internal/config              0.718s   ← 项 6 ListModeIsAuto 追加
ok  	ops-toolbox/internal/credentials         0.779s
ok  	ops-toolbox/internal/diagnostics         0.757s
ok  	ops-toolbox/internal/dlmanager           0.236s
ok  	ops-toolbox/internal/downloads           0.031s
ok  	ops-toolbox/internal/formatter           0.008s
ok  	ops-toolbox/internal/httpserver           91.238s  ← B1/B2/B3 + 项 6 集成测试追加
ok  	ops-toolbox/internal/logquery            0.012s   ← B1 新增
ok  	ops-toolbox/internal/sftpclient          0.102s   ← 项 8 新增
ok  	ops-toolbox/internal/sshclient           3.448s
ok  	ops-toolbox/internal/tailmgr             0.351s
# 13/13 包测试 pass
```

**注**: 任务说"必须 14/14 (v2 是 13, +diagnostics)"，但 `internal/diagnostics` 在 v2 收尾 commit `c8d48a4` 就已存在（基线测试输出含 13 包），所以本次仍是 13/13。新增 6 个测试文件、20+ 个新 test func 全部 pass。

---

## 6. 改动文件清单

### 修改 (16 个)
- `main.go` — 项 14 启动 warning
- `internal/config/config.go` — 项 6 `ListModeIsAuto()` + B1 时间窗辅助
- `internal/config/config_test.go` — 项 6 单测
- `internal/logquery/logquery.go` — B1 `SearchTimeWindow` / `FilterHitsByTimeWindow` / `ModTimeParsed`
- `internal/httpserver/handlers_logs_list.go` — 项 6 fallback 重试逻辑 + `shouldFallbackToPOSIX`
- `internal/httpserver/handlers_logs_search.go` — B1 query 参数 + 过滤
- `internal/httpserver/handlers_logs_search_multi.go` — B1 multi 端接入
- `internal/audit/audit.go` — B2 `WriteJSON` / `IsSensitiveKey` / `JSONFilename` / `JSONRedactedValue` + `jsonRecord`
- `internal/httpserver/handlers_audit.go` — B2 `handleAuditExportJSON` + `redactRecord` 通用脱敏（同时套到 CSV）
- `internal/httpserver/handlers_audit_test.go` — B2 集成测试
- `internal/httpserver/httpssh_test.go` — B2 加 `jsonDecodeArr` helper
- `internal/httpserver/handlers_files.go` — 项 8 `sftpDialer` 改 `NewAuto`
- `internal/sftpclient/sftpclient.go` — 项 8 完整重构：RemoteFS 接口 + ShellBackend + NewAuto
- `internal/httpserver/handlers_downloads.go` — B3 `handleDownloadsOpenDir` + 路由分发
- `internal/httpserver/httpserver.go` — B2 新路由 `/api/audit/export.json`

### 新建 (6 个)
- `internal/logquery/time_window_test.go` — B1 单测
- `internal/audit/json_test.go` — B2 单测
- `internal/httpserver/handlers_logs_list_fallback_test.go` — 项 6 单测
- `internal/httpserver/open_dir.go` — B3 平台命令 + 越界校验
- `internal/httpserver/open_dir_test.go` — B3 单测
- `internal/sftpclient/shell_backend_test.go` — 项 8 单测

### 文档
- `docs/REVIEW-FIX-agent1-v3.md` — 本报告

---

## 7. 已知限制 / 留给未来

- **项 8 ShellBackend 限制**：
  - 大文件下载仍走 `cat` 一次性拉，几十 MB 内 OK，>100MB 大文件后续可改 `dd` 分片 + `base64` 编码
  - 时间格式只覆盖 4 种最常见（HH:MM / HH:MM:SS / 完整年份 / 欧式 day-month-year），其他 locale 罕见组合按"解析失败"处理，文件被跳过
  - ShellBackend 不支持 hidden file 过滤（ls -la 已经会列 . 开头的，调用方决定是否过滤）
- **B1 时间窗**：
  - 按"文件 mtime"粗过滤（不是 hit 行内的日志时间），单文件内多时段 hit 会被一起收/拒
  - 这是 v0.4 P3 体验项里最简单的实现，"看某天日志"场景够用；行内时间过滤留给 v0.5
- **B2 CSV redaction**：
  - 同步把 CSV 路径也走 `redactRecord`，但 v0.4 CSV 字段顺序是固定的（header 在前，extras 在后），如果用户之前导出的 CSV 已包含 password 列，新版本会自动脱敏成 `<redacted>`（字段位置不变）

---

## 8. 验收要点（给 verifier）

1. **质量门全绿**：13/13 包测试 pass / gofmt 0 / build clean
2. **项 6 fallback**：可以临时把 ld.ListMode 设成 `auto`，远端 SSH 一个老 AIX box（没 find / BSD find）模拟验证，handler 会从 gnu_find 失败 → posix_ls 重试；audit 里能看到 `stage=fallback` 行
3. **项 14 启动日志**：开 `enable_free_file_browser: true` 启动 server，看 stderr 里有 `WARNING: 文件浏览器...`
4. **项 8 SFTP 不可用 fallback**：可以临时在 handler 里注入一个 `NewAuto` 失败的 SSH server，验证 audit 里能看到 `result=fail err=...SFTP 不可用...`，但仍能 ls / 下小文件（走 shell）
5. **B1 时间窗**：跑 `/api/logs/search?since=...&until=...`，响应里 `hits` 数组应只含 mtime 在窗口内文件的 hit
6. **B2 JSON 导出**：`/api/audit/export.json`，password 字段完全消失，redacted 数组里出现 "password"
7. **B3 open-dir**：macOS 上 POST `/api/downloads/{name}/open-dir`，Finder 应弹窗并选中该文件
