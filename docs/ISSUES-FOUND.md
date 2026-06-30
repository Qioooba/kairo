# Kairo 深度测试问题汇总

> 审查范围：kairo v0.8 全量代码（后端 Go + 前端 JS + 配置 + 文档 + 打包产物）
> 审查方式：静态全量代码阅读 + gofmt/node --check/go test 实跑 + API 契约核对 + 配置/README 一致性比对
> 审查时间：2026-06-27
> 代码基线：本地工作目录 /Users/qi/Documents/spaces/ops-toolbox（含 vendor/ 和 web/vendor/）；同时核对 kairo-src-review.tar.gz 打包产物

---

## 执行摘要

| 类别 | P0 | P1 | P2 | P3 | 合计 |
|---|---|---|---|---|---|
| 打包产物（PKG） | 2 | 0 | 0 | 0 | 2 |
| 静态检查（STATIC） | 0 | 1 | 0 | 0 | 1 |
| 后端（BE） | 2 | 5 | 10 | 5 | 22 |
| 前端（FE） | 1 | 6 | 7 | 7 | 21 |
| API 契约（API） | 1 | 0 | 4 | 0 | 5 |
| 配置（CFG） | 0 | 7 | 2 | 0 | 9 |
| 文档（DOC） | 0 | 2 | 9 | 3 | 14 |
| **合计** | **6** | **21** | **32** | **15** | **74** |

**最高优先级处理顺序建议**：
1. BE-001（compare 任意文件读）+ BE-005（SSH host key 默认跳过）+ BE-007（白名单默认空=无限制）→ 安全红线
2. BE-002（tail 5 分钟强断）→ 用户已确认的核心稳定性 bug
3. FE-001（history.js 僵尸页）+ BE-014（audit 死路由）→ 功能一致性
4. PKG-001/002（tar.gz 缺 vendor/diff2html）→ 修复打包脚本
5. CFG-001~007 + DOC-008/009（配置示例缺失 + README 字段名错）→ 文档红线

---

## 一、静态检查基线

| 检查项 | 命令 | 结果 |
|---|---|---|
| Go 单测 | `go test -mod=vendor -count=1 ./...` | ✅ 全部通过（15 个包，httpserver 包 60.9s） |
| 前端单测 | `node web/app.test.js` | ✅ 17 pass / 0 fail |
| JS 语法 | `node --check web/*.js web/pages/*.js` | ✅ 全部通过 |
| gofmt | `gofmt -l $(find . -name '*.go' -not -path './vendor/*')` | ❌ 12 个文件未格式化（见 STATIC-001） |

**结论**：本地代码库（含 vendor/）可跑通全量测试基线；用户报告的 tar.gz 因缺 vendor/ 无法离线跑 go test，是打包脚本问题而非代码问题。

---

## 二、打包产物问题（PKG）

### PKG-001
- 编号：PKG-001
- 优先级：P0
- 模块：打包产物
- 页面/按钮/接口：kairo-src-review.tar.gz
- 复现步骤：`tar -tzf kairo-src-review.tar.gz | grep vendor` 返回空
- 实际结果：tar.gz 解包后没有 `vendor/` 目录，离线环境执行 `go test -mod=vendor ./...` 会因为找不到依赖直接失败
- 期望结果：发布包应包含完整 vendor/，或附带 `GOFLAGS=-mod=mod` + 完整 go.sum 让 `go mod download` 还原
- 影响：分发场景（内网同事拿到包后想自测）无法构建/测试
- 修改建议：打包脚本（`scripts/build_windows_amd64.sh` 或独立 release 脚本）增加 `tar -czf ... vendor/`；或在 README 标注「需要联网执行 `go mod vendor` 后才能跑测试」
- 涉及文件：打包脚本（未在仓库找到 release 打包脚本，可能为手动 `tar`）
- 是否需要补测试：否

### PKG-002
- 编号：PKG-002
- 优先级：P0
- 模块：打包产物
- 页面/按钮/接口：web/vendor/diff2html.min.{css,js}
- 复现步骤：`tar -tzf kairo-src-review.tar.gz | grep "web/vendor"` 返回空；解包后访问代码比对页
- 实际结果：tar.gz 缺 `web/vendor/diff2html.min.css` 和 `web/vendor/diff2html.min.js`，而 `web/index.html` 第 8、110 行直接引用 `/static/vendor/diff2html.min.{css,js}`。解包运行后代码比对页会 404 加载这两个资源，diff 渲染失败
- 期望结果：打包脚本应包含 web/vendor/；或前端加 CDN fallback
- 影响：代码比对页（用户 14 个菜单之一）在 tar.gz 解包运行场景下完全不可用
- 修改建议：打包脚本增加 `web/vendor/`；本地仓库实际有这两个文件，确认是打包遗漏
- 涉及文件：打包脚本、`web/index.html`（第 8、110 行引用）
- 是否需要补测试：是 — release 流水线应加「解包后访问 #/compare 页面无 404」的 smoke 测试

---

## 三、静态检查问题（STATIC）

### STATIC-001
- 编号：STATIC-001
- 优先级：P1
- 模块：静态检查
- 页面/按钮/接口：gofmt
- 复现步骤：`gofmt -l $(find . -name '*.go' -not -path './vendor/*')`
- 实际结果：12 个文件未格式化：
  - `internal/portreuse/portreuse_nonwindows.go`
  - `internal/portreuse/portreuse.go`
  - `internal/diff/diff_test.go`
  - `internal/formatter/formatter_test.go`
  - `internal/httpserver/handlers_openers.go`
  - `internal/httpserver/httpserver.go`
  - `internal/httpserver/handlers_openers_test.go`
  - `internal/httpserver/handlers_local_test.go`
  - `internal/httpserver/httpssh_test.go`
  - `internal/httpserver/handlers_compare.go`
  - `internal/httpserver/handlers_files_test.go`
  - `internal/httpserver/handlers_diff.go`
- 期望结果：`gofmt -l` 应返回空
- 影响：代码风格不一致；CI 若加 gofmt 检查会失败
- 修改建议：执行 `gofmt -w $(find . -name '*.go' -not -path './vendor/*')` 一次性修复
- 涉及文件：上述 12 个文件
- 是否需要补测试：否

---

## 四、后端问题（BE）

### BE-001
- 编号：BE-001
- 优先级：P0
- 模块：后端-代码比对
- 页面/按钮/接口：`POST /api/compare/file-diff`、`POST /api/compare/folder-scan`
- 复现步骤：`curl -X POST http://127.0.0.1:18080/api/compare/file-diff -d '{"left_path":"/etc/passwd","right_path":"/etc/shadow"}'`
- 实际结果：`internal/httpserver/handlers_compare.go:253-274` 的 `handleCompareFileDiff` 只校验 `left_path/right_path` 非空，直接 `exec.CommandContext(r.Context(), "diff", "-u", req.LeftPath, req.RightPath)`；`handleCompareFolderScan`（同文件 :109）的 `scanDir` 递归遍历任意本地目录、对每个文件算 MD5。两个 handler 完全没有路径白名单 / root 校验
- 期望结果：应校验 left_path / right_path 必须落在白名单根下（如 `cfg.App.FreeFileRoots` 或新增 `compare_roots`），并禁止 `/etc`、`/Users`、`/var`、`C:\Windows` 等系统目录
- 影响：安全 — 任意已认证用户可读取本机任意文件内容（通过 diff 输出回显）、枚举任意目录结构 + 文件大小 + MD5 哈希
- 修改建议：`handlers_compare.go:153` 和 `:253` 入口处增加 `openPathAllowed` 校验；对 `handleCompareFileDiff` 加文件大小上限（如 50MB）
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_compare.go`
- 是否需要补测试：是 — 测 `/api/compare/file-diff` 传 `/etc/passwd` 应返回 403

### BE-002
- 编号：BE-002
- 优先级：P0
- 模块：后端-tail 会话管理
- 页面/按钮/接口：`GET /api/logs/tail/{id}/events`（SSE 长连接）
- 复现步骤：在前端开启任意日志 tail，保持 SSE 连接不关，持续观看超过 5 分钟
- 实际结果：`internal/tailmgr/tailmgr.go:342` 的 `idleGC` 判定条件是 `time.Since(s.CreatedAt) > m.idleAfter`，其中 `idleAfter = 5 * time.Minute`（:222），`CreatedAt` 是会话创建时间而非"最后活动时间"。任何 tail 会话存活超过 5 分钟都会被 `s.killSSH()` 强制断开，无论用户是否在活跃观看
- 期望结果：idle 判定应基于"最后一条行输出时间"或"最后有订阅者的时间"，而非创建时间
- 影响：稳定性 — 用户盯长时间滚动日志（生产故障排查）会被意外断开，必须重新点 tail
- 修改建议：`tailmgr.go:39` 的 `Session` 结构增加 `lastActivity time.Time` 字段，在 `enqueueLine`（:128）和 `Subscribe`（:74）时更新；`idleGC`（:342）改判 `time.Since(s.lastActivity) > m.idleAfter`。同时把 `idleAfter` 改为可配置（如 30 分钟）
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/tailmgr/tailmgr.go`
- 是否需要补测试：是 — 测持续推 line 的 session 6 分钟后仍存活

### BE-003
- 编号：BE-003
- 优先级：P1
- 模块：后端-认证授权
- 页面/按钮/接口：`PUT /api/admin/servers`、`POST /api/config/import`、`POST /api/credentials/clear`、`PUT /api/admin/openers`、`PUT /api/admin/download-retention`
- 复现步骤：`auth.enabled=true` 且配置多 token 场景下，用任意普通用户 token 调 `POST /api/config/import` 覆盖整个 config.yaml
- 实际结果：`internal/httpserver/httpserver.go:147-166` 的认证逻辑只校验 token 有效 + IP 白名单，没有任何角色/权限分级。所有 `/api/admin/*`、`/api/config/import`、`/api/credentials/clear` 等敏感写接口对任意持 token 用户开放
- 期望结果：敏感管理接口应要求管理员角色 token
- 影响：安全 — 多用户共享时，任意同事可改写 server 配置、注入恶意 host_key_sha256、清空他人凭据、导入带后门的 config.yaml
- 修改建议：`internal/config/config.go` 的 `AuthToken` 增加 `Role string` 字段（`admin`/`user`）；`httpserver.go:147` 在调 `/api/admin/*`、`/api/config/import`、`/api/credentials/clear` 前校验 `token.Role == "admin"`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/httpserver.go`、`/Users/qi/Documents/spaces/kairo/internal/config/config.go`
- 是否需要补测试：是 — 测普通 token 调 `/api/config/import` 返回 403

### BE-004
- 编号：BE-004
- 优先级：P1
- 模块：后端-配置导出脱敏
- 页面/按钮/接口：`GET /api/config/export`
- 复现步骤：config.yaml 里写多行密码（`password: |-\n  multi\n  line\n  secret`）或 flow 风格（`servers: [{password: "abc123"}]`），调 `GET /api/config/export`
- 实际结果：`internal/httpserver/handlers_config_yaml.go:31` 的 `redactConfigYAML` 是逐行扫描，只在 `key: value` 单行格式下匹配敏感关键词。YAML 多行字符串、flow 风格、续行都不会被脱敏，密码会原样出现在下载的 config.yaml 里
- 期望结果：脱敏应基于结构化 YAML 解析（`yaml.Unmarshal` 到 `map[string]any` 后递归遍历，遇到敏感 key 替换 value）
- 影响：安全 — 导出的 config.yaml 可能泄露明文密码 / token / 私钥
- 修改建议：`handlers_config_yaml.go:31` 改用 `gopkg.in/yaml.v3` 解析为 `map[string]any`，递归遍历，命中 `password/passwd/secret/token/api_key/private_key/host_key_sha256` 等 key 时把 value 替换为 `"***"`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_config_yaml.go`
- 是否需要补测试：是 — 测多行密码、flow 风格、嵌套 map 的脱敏

### BE-005
- 编号：BE-005
- 优先级：P1
- 模块：后端-SSH 客户端安全
- 页面/按钮/接口：所有 SSH 相关接口（`/api/logs/*`、`/api/files/*`、`/api/ssh/test`、`/api/logs/tail/*`）
- 复现步骤：config.yaml 里配置一台 server 但不填 `host_key_sha256`，调任意 SSH 接口
- 实际结果：`internal/sshclient/sshclient.go` 的 `Dial` 在 `srv.HostKeySHA256 == ""` 时使用 `InsecureIgnoreHostKey`，完全跳过 host key 校验。工具会接受任意 SSH 服务器身份，可被中间人攻击截获密码
- 期望结果：默认应拒绝未配置 host key 的连接（fail-closed），或采用 TOFU 模式 + 审计
- 影响：安全 — MITM 可截获 SSH 密码，进而横向渗透内网
- 修改建议：`sshclient.go` 的 `Dial` 中，当 `HostKeySHA256 == ""` 时返回错误"未配置 host key 指纹，拒绝连接"；或新增 `app.allow_insecure_host_key: false` 默认值
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/sshclient/sshclient.go`
- 是否需要补测试：是 — 测未配 host key 时 Dial 返回错误

### BE-006
- 编号：BE-006
- 优先级：P1
- 模块：后端-配置校验
- 页面/按钮/接口：`PUT /api/admin/servers`、`POST /api/config/import`、启动校验
- 复现步骤：config.yaml 设 `app.host: "0.0.0.0"`、`auth.enabled: true`，启动服务
- 实际结果：`internal/config/config.go` 的 `Validate` 在 `auth.enabled=true` 时允许 `0.0.0.0` 作为 host。token 一旦泄露，任意外网/内网主机都能访问
- 期望结果：`0.0.0.0` 应被显式拒绝，或要求同时配置 IP 白名单；至少启动时打 WARNING
- 影响：安全 — 攻击面从 127.0.0.1 扩大到所有网卡
- 修改建议：`config.go` 的 `Validate` 中，`host == "0.0.0.0"` 时返回错误"禁止 0.0.0.0，请用具体网卡 IP 或 127.0.0.1"
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/config/config.go`
- 是否需要补测试：是 — 测 `host=0.0.0.0` + `auth.enabled=true` 时 Validate 返回错误

### BE-007
- 编号：BE-007
- 优先级：P1
- 模块：后端-文件浏览器白名单
- 页面/按钮/接口：`POST /api/files/list`、`POST /api/files/preview`、`POST /api/files/download`
- 复现步骤：config.yaml 不配 `app.free_file_roots`（默认空），调 `POST /api/files/list` 传 `path: "/etc"`
- 实际结果：`internal/config/config.go` 的 `FreeFileRootsEnabled` 在 `free_file_roots` 为空时返回 `true`（无限制），即任意远端路径都可列出/预览/下载。`handlers_files.go:213` 的白名单校验形同虚设。`app.allowed_download_roots` 默认空同理
- 期望结果：默认应为 fail-closed：`free_file_roots` 为空时拒绝任意路径浏览
- 影响：安全 — 默认配置下任意已认证用户可通过 SSH 账号权限浏览/下载远端任意文件（如 `/etc/shadow`、`~/.ssh/id_rsa`）
- 修改建议：`config.go` 的 `FreeFileRootsEnabled` 改为 `len(roots) == 0` 时返回 `false`；`Validate` 在 `enable_free_file_browser: true` 且 `free_file_roots` 为空时返回错误
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/config/config.go`
- 是否需要补测试：是 — 测空 `free_file_roots` 时 `/api/files/list` 返回 403

### BE-008
- 编号：BE-008
- 优先级：P2
- 模块：后端-本地文件操作
- 页面/按钮/接口：`POST /api/local/open-with`、`POST /api/downloads/{name}/open-dir`
- 复现步骤：下载一个名为 `my..file.log` 的合法文件，调 `POST /api/local/open-with` 传 `name: "my..file.log"`
- 实际结果：`internal/httpserver/handlers_local.go:162` 和 `handlers_downloads.go:160` 用 `strings.Contains(req.Name, "..")` 校验，会误拒所有含 `..` 子串的合法文件名
- 期望结果：应精确匹配路径分隔符形式的 `..`（如 `/../`、`..\`）
- 影响：功能 — 合法文件名被误拒
- 修改建议：改为 `strings.Contains(req.Name, "/../") || strings.HasPrefix(req.Name, "../") || strings.Contains(req.Name, "\\..\\")`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_local.go`、`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_downloads.go`
- 是否需要补测试：是 — 测 `name="my..file.log"` 应通过

### BE-009
- 编号：BE-009
- 优先级：P2
- 模块：后端-SSE 事件契约
- 页面/按钮/接口：`GET /api/files/download/{id}/events`、`GET /api/logs/download/{id}/events`
- 复现步骤：发起下载任务，等它完成后调 `GET /api/files/download/{id}/events`
- 实际结果：`internal/httpserver/handlers_files.go:1086-1087` 在已结束路径先写 `data: <done-payload>\n\n`，紧接着又写 `event: done\ndata: {}\n\n`。前者是 message 事件（携带真实 done 数据），后者是 `done` 命名事件（携带空数据）。两条路径行为不一致
- 期望结果：统一为：结束时只发一个 `event: done\ndata: <真实payload>\n\n`
- 影响：功能 — 前端需兼容两种 done 形态，易漏掉真实结果数据
- 修改建议：`handlers_files.go:1086-1087` 删掉 `data: <done-payload>` 那行，改为 `event: done\ndata: <done-payload>\n\n`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_files.go`
- 是否需要补测试：是 — 测已结束 session 的 SSE 流只发一个 `event: done` 且 data 含 `downloads/folder`

### BE-010
- 编号：BE-010
- 优先级：P2
- 模块：后端-代码比对
- 页面/按钮/接口：`POST /api/compare/file-diff`
- 复现步骤：传两个各 500MB 的本地大文件路径调 `/api/compare/file-diff`
- 实际结果：`handlers_compare.go:270` 的 `exec.CommandContext` 把整个 diff 输出捕获到 `bytes.Buffer`（:271），无大小上限。两个大文件可能产生 GB 级 diff 输出，导致内存爆炸
- 期望结果：应限制 diff 输出大小（`io.LimitReader` 包 `cmd.StdoutPipe`，超 10MB 截断）；或限制输入文件大小
- 影响：稳定性 — 单个请求可 OOM 整个进程
- 修改建议：`handlers_compare.go:268` 加输入文件大小校验（> 50MB 返回 413）；`:271` 改用 `cmd.StdoutPipe()` + `io.LimitReader`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_compare.go`
- 是否需要补测试：是 — 测两个 100MB 文件 diff 不 OOM、返回 truncated

### BE-011
- 编号：BE-011
- 优先级：P2
- 模块：后端-代码比对
- 页面/按钮/接口：`POST /api/compare/folder-scan`
- 复现步骤：传 `left_path: "/Users/qi/Documents"` 或含 10 万文件的目录调 `/api/compare/folder-scan`
- 实际结果：`handlers_compare.go:109` 的 `filepath.WalkDir` 递归遍历，对每个相同大小的文件对调 `hashFile` 整文件 MD5。无深度限制、无文件数上限、无大小上限、无超时
- 期望结果：应限制最大文件数（如 10000）、最大单文件大小、目录深度、总扫描时长超时
- 影响：稳定性 — 单请求可阻塞 worker 数分钟、占满磁盘 IO 和内存
- 修改建议：`handlers_compare.go:109` 的 `WalkDir` 回调里加计数器，超过阈值返回 `filepath.SkipDir` 并标 `truncated`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_compare.go`
- 是否需要补测试：是 — 测 10 万文件目录扫描在 10s 内返回且不 OOM

### BE-012
- 编号：BE-012
- 优先级：P2
- 模块：后端-下载管理
- 页面/按钮/接口：`POST /api/files/download`、`POST /api/logs/download-latest`
- 复现步骤：下载一个 5GB 大日志文件，慢速 SSH 连接（1MB/s），实际需要 80 分钟
- 实际结果：`internal/dlmanager/dlmanager.go` 的 `IdleTimeout = 30 * time.Minute`，下载 session 创建后 30 分钟会被 `IdleGC` 标记为过期并 cancel。即使下载正在活跃进行，也会被强制取消
- 期望结果：IdleTimeout 应基于"最后活动时间"，而非创建时间
- 影响：稳定性 — 大文件下载被意外中断
- 修改建议：`dlmanager.go` 的 `Session` 增加 `lastActivity` 字段，`BroadcastEvent` 时更新；`IdleGC` 改判 `time.Since(s.lastActivity) > IdleTimeout`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/dlmanager/dlmanager.go`
- 是否需要补测试：是 — 测下载进行中（每 10s 推 progress）的 session 30 分钟后不被 GC

### BE-013
- 编号：BE-013
- 优先级：P2
- 模块：后端-凭据存储
- 页面/按钮/接口：`POST /api/credentials/save`、内部 `credentials.Get`
- 复现步骤：观察 `internal/credentials/credentials.go` 的 `decrypt` 实现
- 实际结果：`decrypt` 同时支持 AAD 绑定的密文（新格式）和非 AAD 绑定的密文（旧格式兼容），通过尝试两种解密路径。即使新写入的密文绑定了 AAD，攻击者拿到密文后用旧格式路径仍可绕过 AAD 校验
- 期望结果：应只允许 AAD 绑定格式；旧格式密文应在读取时一次性迁移到新格式
- 影响：安全 — AAD 防篡改/防替换能力被弱化
- 修改建议：`credentials.go` 的 `decrypt` 收到非 AAD 格式密文时，解密成功后立即用新格式重写（一次性迁移）；下一版本删除旧格式支持分支
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/credentials/credentials.go`
- 是否需要补测试：是 — 测旧格式密文读取后会被重写为新格式

### BE-014
- 编号：BE-014
- 优先级：P2
- 模块：后端-路由注册
- 页面/按钮/接口：`GET /api/audit/recent`、`GET /api/audit/export.csv`、`GET /api/audit/export.json`
- 复现步骤：根据项目记忆前端菜单已注释掉"操作历史"入口，但调 `GET /api/audit/recent` 仍能返回数据
- 实际结果：`internal/httpserver/httpserver.go:197-202` 仍注册了 3 个 `/api/audit/*` 路由，handler（`handlers_audit.go`）仍工作。前端入口被注释但后端接口存活，形成"死路由"——配置不一致，且泄露审计日志给知道 URL 的用户
- 期望结果：要么前端恢复入口、要么后端注释路由（或加配置开关 `app.audit_enabled`）
- 影响：功能一致性 — 接口契约与 UI 不一致；安全 — 任意用户可导出全量审计 CSV/JSON
- 修改建议：若决定下线，注释 `httpserver.go:197-202` 的 3 个 case；若保留，前端恢复入口并在 `handlers_audit.go` 加管理员鉴权
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/httpserver.go`
- 是否需要补测试：否

### BE-015
- 编号：BE-015
- 优先级：P2
- 模块：后端-格式化工具
- 页面/按钮/接口：`POST /api/format/cron-parse`
- 复现步骤：传 `input: "0 0 0 1 1 *"`（每年 1 月 1 日执行一次），调 `/api/format/cron-parse`
- 实际结果：`internal/httpserver/handlers_format.go:678` 的 `collectCronRuns` 用 `for i := 0; i < 2000000` 暴力遍历找下 5 次执行。对 `0 0 0 1 1 *`（每年 1 次），5 次 = 5 年 ≈ 262.8 万次迭代，超过 200 万上限，导致 `next_runs` 返回不足 5 条
- 期望结果：应使用真正的 cron 解析算法（基于字段集合的位运算推进）
- 影响：功能 — 稀疏 cron 表达式预览结果不完整
- 修改建议：`handlers_format.go:665` 改用按字段递进算法（标准 cron 库如 `github.com/robfig/cron/v3` 的实现，vendor 已有）
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_format.go`
- 是否需要补测试：是 — 测 `0 0 0 1 1 *` 返回 5 条 next_runs；测 `0 0 0 29 2 *`（2 月 29 日）闰年处理

### BE-016
- 编号：BE-016
- 优先级：P2
- 模块：后端-本地存储权限
- 页面/按钮/接口：`PUT /api/preferences`、`POST /api/http/cases`、`POST /api/http/envs`
- 复现步骤：写入 preferences 后查看 `data/preferences.json` 文件权限
- 实际结果：`handlers_preferences.go:68` 和 `handlers_http_cases.go:307` 用 `os.CreateTemp` 创建临时文件，在 Unix 下默认 0600 安全，但 `config/manager.go` 的 `writeYAMLAtomic` 若没显式 chmod，config.yaml 可能是 0644（含 server 密码）
- 期望结果：所有含敏感数据的本地文件都应显式 0600
- 影响：安全 — 同机其他用户可读 config.yaml / preferences / http_cases
- 修改建议：`handlers_preferences.go:68`、`handlers_http_cases.go:307`、`config/manager.go` 的 `writeYAMLAtomic` 在 `tmp.Close()` 后、`os.Rename` 前显式 `os.Chmod(tmp, 0o600)`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_preferences.go`、`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_http_cases.go`、`/Users/qi/Documents/spaces/kairo/internal/config/manager.go`
- 是否需要补测试：否

### BE-017
- 编号：BE-017
- 优先级：P2
- 模块：后端-审计完整性
- 页面/按钮/接口：`POST /api/http/request`、`POST /api/compare/file-diff`、`POST /api/compare/folder-scan`
- 复现步骤：调 `POST /api/http/request` 发起一个外网 HTTP 请求；调 `POST /api/compare/file-diff` 读取两个本地文件内容
- 实际结果：`handlers_http_request.go` 的 `handleHTTPRequest` 完全不写审计日志；`handlers_compare.go` 的两个 handler 也不写审计。任意用户可发起外网请求 / 读取本地文件，审计日志里无痕迹
- 期望结果：所有"执行了实际副作用"的接口都应审计：HTTP 测试记录 method/url/target_host；compare 记录 left/right path
- 影响：安全 — 审计盲区，发生数据外泄时无法溯源
- 修改建议：`handlers_http_request.go:74` 在 `doHTTPRequest` 返回前加 `s.audit.Write("http.request", "method", method, "url", url, "status", resp.Status)`；`handlers_compare.go:153` 和 `:253` 入口加 `s.audit.Write("compare.file_diff", "left", req.LeftPath, "right", req.RightPath)`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_http_request.go`、`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_compare.go`
- 是否需要补测试：否

### BE-018
- 编号：BE-018
- 优先级：P3
- 模块：后端-HTTP 测试 SSRF
- 页面/按钮/接口：`POST /api/http/request`
- 复现步骤：传 `url: "http://127.0.0.1:18092/api/admin/servers"` 调 `/api/http/request`
- 实际结果：`handlers_http_request.go:258` 的 `isBlockedHTTPIP` 正确拦截了 loopback / private / link-local。`validateOutboundHTTPURL` 和 `safeHTTPDialContext` 各自独立解析 DNS。`follow_redirect` 路径下重定向 URL 的校验存在轻微 TOCTOU
- 期望结果：在 `safeHTTPDialContext` 里把解析出的 IP 直接用于连接（已基本做到），加注释说明防 DNS rebinding 的双重校验设计
- 影响：安全（轻微）— 理论 TOCTOU，实际难利用
- 修改建议：`handlers_http_request.go:225` 的 `ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)` 后，对 `ips[0].IP` 再调一次 `isBlockedHTTPIP` 显式校验
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_http_request.go`
- 是否需要补测试：是 — 测 `url: "http://127.0.0.1"` 被拒

### BE-019
- 编号：BE-019
- 优先级：P3
- 模块：后端-SSE tail 事件双重 done
- 页面/按钮/接口：`GET /api/logs/tail/{id}/events`
- 复现步骤：开 tail 后等 SSH 自然结束（远端 tail 进程退出）
- 实际结果：`internal/tailmgr/tailmgr.go:280` 在 session 结束时通过 `pushImmediate` 发了一个 `{"kind":"done","msg":"tail 结束 (exit=N)"}` 的 NDJSON；随后 `markDone`（:188）关闭所有订阅者 chan，触发 `handlers_tail.go:152` 的 `!open` 分支，再发一个 `event: done\ndata: {}\n\n`。前端会收到两个 done 信号
- 期望结果：统一为只发一个 `event: done\ndata: {"kind":"done","msg":"..."}\n\n`
- 影响：功能一致性 — 前端需兼容两种 done
- 修改建议：`tailmgr.go:280` 的 `pushImmediate(formatOutput(Output{Kind: "done", ...}))` 改为只更新 `s.doneMsg` 字段，不广播；`markDone` 关闭 chan 时让 `handlers_tail.go:152` 的 `!open` 分支读取 `s.doneMsg` 并发为 `event: done\ndata: <doneMsg>\n\n`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/tailmgr/tailmgr.go`、`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_tail.go`
- 是否需要补测试：是 — 测 SSE 流只收到一个 done 且含 exit 信息

### BE-020
- 编号：BE-020
- 优先级：P3
- 模块：后端-并发竞态
- 页面/按钮/接口：所有 handler（典型：`POST /api/files/list`、`POST /api/files/download`）
- 复现步骤：在 handler 执行期间，另一线程调 `PUT /api/admin/servers` 替换配置
- 实际结果：`httpserver.go:120` 的 `s.cur()` 每次调都返回 `s.cfg.Get()`（COW 快照）。但 `handlers_files.go` 在同一请求里多次调 `s.cur()`（:127 校验开关、:211 白名单、:582 preview 白名单、:762 download 白名单、:789 allow_custom_download_dir、:794 allowed_download_roots、:800 downloadRoot —— 一个 handler 调 6 次）。每次调用拿到的快照可能不同，形成 TOCTOU
- 期望结果：handler 入口处调一次 `cur := s.cur()`，后续全用 `cur` 局部变量
- 影响：稳定性 — 配置热替换时偶发行为不一致
- 修改建议：全局搜索所有 `s.cur()` 在同一 handler 内的多次调用，改为入口处 `cur := s.cur()` 一次取值
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_files.go`（及其他 handler）
- 是否需要补测试：否

### BE-021
- 编号：BE-021
- 优先级：P3
- 模块：后端-下载文件服务
- 页面/按钮/接口：`GET /downloads/<file>`
- 复现步骤：调 `GET /downloads/my..file.log`（合法文件名含 `..`）
- 实际结果：`internal/httpserver/handlers_misc.go:18` 用 `strings.Contains(name, "..")` 校验，会误拒所有含 `..` 子串的合法文件名（同 BE-008）
- 期望结果：`..` 检查改为精确匹配 `/../`
- 影响：功能 — 合法文件名含 `..` 被拒下载
- 修改建议：`handlers_misc.go:18` 改为 `strings.Contains(name, "/../") || strings.HasPrefix(name, "../")`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_misc.go`
- 是否需要补测试：是 — 测 `name="my..file.log"` 可下载

### BE-022
- 编号：BE-022
- 优先级：P3
- 模块：后端-portreuse Windows
- 页面/按钮/接口：启动时端口占用处理
- 复现步骤：Windows 上双击启动 Kairo.exe，端口被另一个 Kairo.exe 残留进程占用
- 实际结果：`internal/portreuse/portreuse_windows.go:158` 的 `promptKillOtherProcess` 用 `os.Stdin.Read` 读 Y/N，但 GUI 程序双击启动时没有 stdin，`os.Stdin.Read` 立即返回 EOF，默认不杀。同时 `notifyMsg`（:210）调 `msg.exe` 弹窗，但用户没法在 GUI 弹窗里输入 Y/N 给 stdin
- 期望结果：GUI 模式下应弹原生 Win32 MessageBox
- 影响：功能 — Windows 双击启动时端口占用自动恢复失效
- 修改建议：`portreuse_windows.go:158` 改用 `golang.org/x/sys/windows` 的 `MessageBox` API 弹原生对话框
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/portreuse/portreuse_windows.go`
- 是否需要补测试：否（GUI 交互难测）

---

## 五、前端问题（FE）

### FE-001
- 编号：FE-001
- 优先级：P0
- 模块：菜单 / 路由
- 页面/按钮/接口：#/history（操作历史）
- 复现步骤：打开主页 `#/home`，在首页卡片网格中找到第 8 张卡片「操作历史」（图标 ⏱，标签"已就绪"），点击该卡片
- 实际结果：页面进入"操作历史"页，调用 `GET /api/audit/recent`、`POST /api/audit/export.csv|json`。但侧边栏菜单项已被注释（`web/index.html:62`），用户从首页卡片误入后，若后端 audit 路由已下线（与项目记忆矛盾——当前代码 `httpserver.go:197-202` 仍注册了 audit 路由），页面会显示"加载失败"；即便路由还在，也是"半移除"的不一致状态
- 期望结果：要么彻底删除 history.js + 路由注册 + 首页卡片，要么恢复菜单项并保留后端路由
- 影响：用户从首页点击「操作历史」会进入一个半死页面，体验极差；且 `history.js` 仍被 `index.html:104` 加载，浪费网络请求 + 解析时间
- 修改建议：
  - `web/index.html:62` 取消注释或彻底删除该行
  - `web/index.html:104` 删除 `<script src="/static/pages/history.js"></script>`
  - `web/pages/home.js:48` 删除 history 卡片对象
  - 删除 `web/pages/history.js` 文件
  - 后端 `httpserver.go:197-202` 同步处理（删除或保留）
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/index.html`（第 62、104 行）、`/Users/qi/Documents/spaces/kairo/web/pages/home.js`（第 48 行）、`/Users/qi/Documents/spaces/kairo/web/pages/history.js`
- 是否需要补测试：是 — 应补一个路由 smoke 测试，确保菜单项与注册路由一一对应

### FE-002
- 编号：FE-002
- 优先级：P1
- 模块：主题 / 布局
- 页面/按钮/接口：preview.html（独立预览窗口）
- 复现步骤：在主窗口切换到 light（或 green / hc）主题，在「日志助手」或「文件下载」页点击文件预览，打开 preview.html 独立窗口
- 实际结果：`web/preview.html` 的 `<head>` 没有像 `web/index.html:28-38` 那样的内联主题初始化脚本，`<html>` 元素没有 `data-theme` 属性，CSS 回退到 `:root` 默认值（dark 主题）。独立窗口显示深色，与主窗口的浅色主题不一致
- 期望结果：preview.html 应读取 `localStorage.getItem('kairo_theme')` 并设置 `data-theme`，与主窗口保持一致
- 影响：用户在浅色主题下使用时，预览窗口突然变成深色，视觉割裂
- 修改建议：在 `web/preview.html` 的 `<head>` 中、`<link rel="stylesheet">` 之前，加入与 `index.html:28-38` 相同的内联脚本
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/preview.html`
- 是否需要补测试：否

### FE-003
- 编号：FE-003
- 优先级：P1
- 模块：安全 / 隐私
- 页面/按钮/接口：preview.html 的 doPreview / btn-download
- 复现步骤：主窗口登录后获取 token，打开 preview.html 独立窗口，preview.html 第 175 行 `doPreview` 和第 243 行 `btn-download` 直接调用 `fetch('/api/files/preview', ...)` 和 `fetch('/api/files/download', ...)`，不经过 `Kairo.api.api()` 封装
- 实际结果：如果后端鉴权依赖请求头 token（而非 cookie），裸 fetch 不携带 token → 返回 401 → 预览页显示"预览失败：HTTP 401"。即使后端用 cookie 鉴权，preview.html 也绕过了 `api.js` 的 401 自动弹登录逻辑
- 期望结果：preview.html 应使用 `Kairo.api.api()` 封装
- 影响：预览功能可能完全不可用（token 鉴权场景），或鉴权失败时无友好引导
- 修改建议：`preview.html:108` 的解构修复为 `const { api } = window.Kairo.api || {};`，将第 175、243 行的裸 fetch 替换为 `api('POST', '/api/files/preview', {...})`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/preview.html`（第 108、175、243 行）
- 是否需要补测试：是 — 测试 preview.html 在 401 场景下的行为

### FE-004
- 编号：FE-004
- 优先级：P1
- 模块：安全 / 隐私
- 页面/按钮/接口：config.js 的 doExport / doImportUpload
- 复现步骤：进入「系统配置」页，点击「导出配置」按钮 → `doExport`（`web/pages/config.js:685`）调用 `fetch('/api/config/export', { credentials: 'same-origin' })`；点击「导入配置」→ `doImportUpload`（:749）调用 `fetch('/api/config/import', ...)`
- 实际结果：与 FE-003 同理，绕过 `Kairo.api` 封装。token 鉴权场景下 401 不触发 auth overlay；且错误响应解析逻辑自行实现（:690、:758），与 `api.js` 的统一错误处理重复
- 期望结果：统一使用 `Kairo.api.api()`，或至少在 fetch 中注入 token
- 影响：鉴权失效时用户看到原始 HTTP 错误而非登录引导；代码重复
- 修改建议：`config.js:685` 和 `:749` 的裸 fetch 改为 `api()` 调用；导出场景需保留 blob 下载逻辑，可让 `api()` 支持 `responseType: 'blob'`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/config.js`（第 685-708、749-795 行）
- 是否需要补测试：否

### FE-005
- 编号：FE-005
- 优先级：P1
- 模块：资源加载 / 内存
- 页面/按钮/接口：tail.js 的 setInterval
- 复现步骤：在「日志助手」页开启实时 Tail（SSE），`web/tail.js:162` `setInterval(flushTail, 100)`、:180 `setInterval(updateMeters, 1000)` 启动；关闭 Tail 会话或切换路由
- 实际结果：tail.js 中没有任何 `clearInterval` 调用（已 grep 确认）。独立窗口场景下窗口关闭时浏览器自动回收；但如果 tail 嵌入主页面（非独立窗口），切换路由后 interval 持续运行，向已从 DOM 移除的元素写入，造成隐式内存泄漏 + 无效计算
- 期望结果：在 Tail 停止 / 路由切换 / 页面卸载时 clearInterval
- 影响：长时间使用主页面 tail 后切走，后台仍每 100ms 跑 flushTail，CPU 占用持续
- 修改建议：在 `web/tail.js` 的停止逻辑和 `window.addEventListener('beforeunload', ...)` 中加入 `clearInterval`；保存 interval id 以便清理
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/tail.js`（第 162、180 行）
- 是否需要补测试：否

### FE-006
- 编号：FE-006
- 优先级：P1
- 模块：异常状态 / 数据一致性
- 页面/按钮/接口：about.js 版本号
- 复现步骤：进入「关于」页，查看 headerCard 中显示的版本号（`web/pages/about.js:100` `text: VERSION`）
- 实际结果：`about.js:11` 硬编码 `const VERSION = 'v0.8';`。后端 `/api/config` 响应不含 Version/BuildTime 字段（当前 `configView` 结构只有 App/Systems/Search/Auth/Paths），about.js 完全不读取后端版本，版本号永远显示 v0.8
- 期望结果：about.js 应在渲染时调用 `api('GET', '/api/config')` 读取版本字段（需后端 `configView` 增加 Version/BuildTime），回填到 VERSION 变量
- 影响：版本发布后前端显示的版本号与后端实际版本脱节
- 修改建议：后端 `httpserver.go` 的 `configView` 增加 `Version`/`BuildTime` 字段（main.go 传入 ldflags）；前端 `about.js` 异步读取后渲染
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/about.js`（第 11 行）、`/Users/qi/Documents/spaces/kairo/internal/httpserver/httpserver.go`（configView 结构）
- 是否需要补测试：否

### FE-007
- 编号：FE-007
- 优先级：P1
- 模块：主题 / 布局
- 页面/按钮/接口：cfg-save-footer-bar（系统配置页底部保存栏）
- 复现步骤：进入「系统配置」页，滚动到表单底部，观察 fixed 底部保存栏
- 实际结果：`web/style.css:568` 中 `.cfg-save-footer-bar { left: 240px; }` 硬编码了 sidebar 宽度。而 `body` 的 `grid-template-columns: 240px 1fr`（:236）也硬编码了 240px。两处耦合：若响应式场景下 sidebar 折叠或宽度变化，footer 不会跟随
- 期望结果：footer 应使用 CSS 变量（如 `--sidebar-width`）或相对定位
- 影响：未来若调整 sidebar 宽度或做移动端折叠，footer 错位
- 修改建议：`web/style.css:236` 和 `:568` 引入 `--sidebar-width: 240px` 变量，两处引用变量；响应式媒体查询中调整变量值
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/style.css`（第 236、568 行）
- 是否需要补测试：否

### FE-008
- 编号：FE-008
- 优先级：P2
- 模块：按钮 / 交互
- 页面/按钮/接口：formatter.js copyOut / jsonpath.js btnCopy
- 复现步骤：进入「报文格式化」页，格式化一段 JSON，点击「复制结果」按钮 → `web/pages/formatter.js:218` `document.execCommand('copy')`；进入「JSONPath」页，提取后点「复制结果」→ `web/pages/jsonpath.js:144` 同样调用
- 实际结果：`document.execCommand('copy')` 已被 MDN 标记为 Deprecated。而 `web/core.js:149` 已实现基于 `navigator.clipboard.writeText` 的 `copyToClipboard`，且 `commands.js`、`timestamp.js`、`cron.js` 都已使用新 API，唯独 formatter.js 和 jsonpath.js 仍用旧 API
- 期望结果：统一使用 `Kairo.core.copyToClipboard`
- 影响：兼容性风险；代码不一致
- 修改建议：`formatter.js:215-220` 的 `copyOut` 改为调用 `copyToClipboard(outTa.value)`；`jsonpath.js:141-146` 的 `btnCopy` onclick 改为调用 `copyToClipboard(outTa.value)`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/formatter.js`（第 215-220 行）、`/Users/qi/Documents/spaces/kairo/web/pages/jsonpath.js`（第 141-146 行）
- 是否需要补测试：否

### FE-009
- 编号：FE-009
- 优先级：P2
- 模块：按钮 / 交互
- 页面/按钮/接口：downloads.js / config.js 的 confirm
- 复现步骤：进入「下载历史」页，点「删除」→ `downloads.js:196` `confirm('删除 ' + f.name + '？')`；进入「系统配置」页，删除业务系统 → `config.js:244` `confirm('确认删除业务系统…')`
- 实际结果：项目已在 `web/core.js:87` 实现了自定义 `confirmDialog(msg, opts) → Promise<boolean>`，但 downloads.js 和 config.js 仍用浏览器原生 `confirm()`，弹窗样式与主题割裂，且原生 confirm 在 Electron 内嵌浏览器中可能被禁用
- 期望结果：所有确认弹窗统一走 `confirmDialog`
- 影响：UI 风格不统一；Electron 环境下原生 confirm 可能不弹出
- 修改建议：`downloads.js:196`、`:216` 的 `confirm(...)` 改为 `await confirmDialog(...)`（需将函数改为 async）；`config.js:244`、`:350`、`:405`、`:486`、`:656`、`:711`、`:737` 同样替换
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/downloads.js`（第 196、216 行）、`/Users/qi/Documents/spaces/kairo/web/pages/config.js`（第 244、350、405、486、656、711、737 行）
- 是否需要补测试：否

### FE-010
- 编号：FE-010
- 优先级：P2
- 模块：代码质量
- 页面/按钮/接口：websphere.js copyToClipboard
- 复现步骤：在「日志助手」页搜索结果中点「复制路径」按钮 → 调用文件内局部 `copyToClipboard`（`websphere.js:1629` 定义）；同页"导出搜索结果"功能却调用 `Kairo.core.copyToClipboard`（:2936、:2946）
- 实际结果：`websphere.js:1629` 重新实现了一遍 `copyToClipboard`，而 `core.js:149` 已有完全等价的实现。同一文件内两种调用方式混用
- 期望结果：删除 `websphere.js:1629` 的局部 `copyToClipboard`，统一使用 `Kairo.core.copyToClipboard`
- 影响：维护负担（修一处忘另一处）；行为可能细微不一致
- 修改建议：`websphere.js:1629` 删除局部函数定义；第 971、1551、1577、1605、1793 行的 `copyToClipboard(...)` 改为 `Kairo.core.copyToClipboard(...)`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/websphere.js`（第 971、1551、1577、1605、1629、1793 行）
- 是否需要补测试：否

### FE-011
- 编号：FE-011
- 优先级：P2
- 模块：主题 / 布局
- 页面/按钮/接口：style.css input/textarea 规则重复定义
- 复现步骤：检查 `web/style.css:512-523`（第一处 input 定义，padding: 8px 10px，border-radius: 8px）；检查 `web/style.css:703-714`（第二处 input 定义，padding: 6px 9px，border-radius: 6px）
- 实际结果：两处选择器完全相同（`input[type="text"], input[type="password"], input[type="number"], select, textarea`），后者覆盖前者。第一处（512-523）成为死代码。同理 `input:focus` 在 524 行和 715 行重复
- 期望结果：合并为一处定义
- 影响：维护困惑（改了第一处不生效）；CSS 体积冗余
- 修改建议：删除 `web/style.css:512-533` 的第一处定义，保留 `:703-719` 的第二处
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/style.css`（第 512-533、703-719 行）
- 是否需要补测试：否

### FE-012
- 编号：FE-012
- 优先级：P2
- 模块：异常状态
- 页面/按钮/接口：compare.js 文件夹比对状态
- 复现步骤：进入「代码比对」页，切到「文件夹比对」tab，输入左右路径，点「开始比对」→ 扫描完成，显示文件树；切到其它页面；再切回「代码比对」页
- 实际结果：`web/pages/compare.js:24-27` 的 `folderScanResult`、`folderExpanded`、`folderShowOnlyDiff`、`folderFilter` 是模块级变量，跨 `renderCompare` 调用持久化。`renderCompare`（:712）不重置它们。用户切回后，如果点击「展开全部」/「折叠全部」按钮（:911-912），`renderFolderTree` 会用旧的 `folderScanResult` 渲染到新容器，显示陈旧数据
- 期望结果：`renderCompare` 开头应重置 `folderScanResult = null; folderExpanded = new Set();`
- 影响：用户看到上一次的比对结果残留，可能误以为是当前结果
- 修改建议：`compare.js:712` 的 `renderCompare` 函数开头加入 `folderScanResult = null; folderExpanded = new Set();`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/compare.js`（第 24-27、712、911-912 行）
- 是否需要补测试：否

### FE-013
- 编号：FE-013
- 优先级：P2
- 模块：按钮 / 交互
- 页面/按钮/接口：cron.js / timestamp.js 异步竞态
- 复现步骤：进入「Cron 解析」页，输入表达式，快速连续切换「时区」下拉（`locSel`），`locSel.addEventListener('change', doParse)`（`web/pages/cron.js:141`）每次触发 `doParse`（async）
- 实际结果：多个 `doParse` 并发执行，后发请求可能先返回，先发请求后返回 → 后返回的结果覆盖最新结果，显示错误时区下的运行时间。`web/pages/timestamp.js:132-133` 同理
- 期望结果：应使用请求取消（AbortController）或序列号（sequence number）丢弃过期响应
- 影响：快速操作时显示结果与当前选择不一致
- 修改建议：在 `doParse` / `doConvert` 中维护一个递增的 `reqId`，回调中检查 `if (myReqId !== reqId) return;`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/cron.js`（第 84-118、141 行）、`/Users/qi/Documents/spaces/kairo/web/pages/timestamp.js`（第 94-112、132-133 行）
- 是否需要补测试：否

### FE-014
- 编号：FE-014
- 优先级：P2
- 模块：异常状态 / 资源加载
- 页面/按钮/接口：history.js autoTimer
- 复现步骤：进入「操作历史」页（通过首页卡片，见 FE-001），点「自动刷新: 关」→ `toggleAuto`（`web/pages/history.js:277`）启动 `setInterval(loadHistory, 3000)`，存于 `autoTimer`；切换到其它页面
- 实际结果：`autoTimer` 是 `renderHistory` 闭包内的局部变量，路由切换时 `app.js` 的 `navigate()` 只做 `view.innerHTML = ''`，不调用任何清理钩子。`setInterval` 持续每 3 秒调用 `loadHistory()`，向已脱离 DOM 的 `tableWrap` 写入，且持续发起 `/api/audit/recent` 请求
- 期望结果：路由离开时应 `clearInterval(autoTimer)`
- 影响：后台持续网络请求 + 无效 DOM 操作；如果 audit 路由已删除（见 FE-001），每 3 秒报一次 404
- 修改建议：`app.js` 的 `navigate()` 应支持页面注册 `onLeave` 钩子；或 history.js 在 `window.addEventListener('hashchange', ...)` 中清理
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/history.js`（第 38、277-287 行）
- 是否需要补测试：否

### FE-015
- 编号：FE-015
- 优先级：P3
- 模块：资源加载
- 页面/按钮/接口：diff2html.min.{css,js} 无缓存版本号
- 复现步骤：查看 `web/index.html:8`：`<link rel="stylesheet" href="/static/vendor/diff2html.min.css" />`；对比 `index.html:9`：`style.css?v=20260627-auth`（有版本号）
- 实际结果：diff2html.min.css 无 `?v=` 查询参数，浏览器强缓存。升级 diff2html 后用户可能加载到旧 CSS，与新 JS 不匹配导致样式错乱。`diff2html.min.js`（:110）同样无版本号
- 期望结果：vendor 资源也应加版本号，或改用 SRI hash
- 影响：升级 diff2html 后用户看到样式错乱，需强制刷新
- 修改建议：`web/index.html:8` 和 `:110` 加 `?v=20260627`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/index.html`（第 8、110 行）
- 是否需要补测试：否

### FE-016
- 编号：FE-016
- 优先级：P3
- 模块：异常状态
- 页面/按钮/接口：http.js highlightJSON
- 复现步骤：在「HTTP 测试」页发送返回含 `&` 字符的 JSON（如 `{"name":"A & B"}`），查看响应区 Pretty 视图
- 实际结果：`web/pages/http.js:139-151` 的 `highlightJSON` 先用 `escapeHtml` 转义（`&` → `&amp;`），再用正则 `/(&quot;[^&]*?&quot;)(\s*:)/g` 匹配键。但 `[^&]*?` 排除了 `&`，导致值中含 `&amp;` 的字符串无法被正确匹配
- 期望结果：高亮应正确处理已转义的内容
- 影响：含特殊字符的 JSON 高亮缺失（纯视觉问题）
- 修改建议：正则改为 `[^"]*?` 配合转义后的 `&quot;` 边界
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/http.js`（第 139-151 行）
- 是否需要补测试：否

### FE-017
- 编号：FE-017
- 优先级：P3
- 模块：异常状态
- 页面/按钮/接口：http.js kvArrayToMap
- 复现步骤：在 HTTP 测试页 Headers 编辑器中添加两行，key 都是 `Content-Type`，发送请求
- 实际结果：`web/pages/http.js:100-109` 的 `kvArrayToMap` 遍历数组，后值覆盖前值，静默丢弃。HTTP 协议允许多个同名 header，但此处只发最后一个
- 期望结果：至少 toast 提示重复 key；或改为数组值支持多值 header
- 影响：用户误以为发了两个 header，实际只发一个
- 修改建议：`web/pages/http.js:100` 检测重复 key 时 toast 警告
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/http.js`（第 100-109 行）
- 是否需要补测试：否

### FE-018
- 编号：FE-018
- 优先级：P3
- 模块：代码质量
- 页面/按钮/接口：preview.html 加载 api.js 但从未使用
- 复现步骤：查看 `web/preview.html:98`：`<script src="/static/api.js"></script>`；查看 `preview.html:108`：`const { api } = window.Kairo || { api: null };`；全文搜索 `api(` 调用
- 实际结果：preview.html 加载了 api.js，但解构赋值写错（`window.Kairo` 的 `api` 属性是模块对象不是函数），且全文未实际调用 `api()`。属于死代码 + 误导
- 期望结果：要么正确使用 `api()`（见 FE-003），要么删除该 script 标签
- 影响：多一次网络请求；误导维护者以为已集成 auth
- 修改建议：配合 FE-003 修复，正确使用 `Kairo.api.api()`；或删除 `preview.html:98` 行
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/preview.html`（第 98、108 行）
- 是否需要补测试：否

### FE-019
- 编号：FE-019
- 优先级：P3
- 模块：安全 / 隐私
- 页面/按钮/接口：vendor 资源无 SRI
- 复现步骤：查看 `web/index.html:8` 和 `:110` 的 vendor 资源引用
- 实际结果：`<link>` 和 `<script>` 均无 `integrity` 属性（SRI）。若 vendor 文件被篡改（如 MITM 或文件系统被改），浏览器仍正常加载执行
- 期望结果：内网工具可酌情，但生产分发建议加 SRI
- 影响：供应链安全风险低（内网），但不符合最佳实践
- 修改建议：生成 `integrity="sha384-..."` 并加 `crossorigin="anonymous"`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/index.html`（第 8、110 行）
- 是否需要补测试：否

### FE-020
- 编号：FE-020
- 优先级：P3
- 模块：按钮 / 交互
- 页面/按钮/接口：home.js history 卡片标记"已就绪"
- 复现步骤：进入首页，查看第 8 张卡片「操作历史」（`web/pages/home.js:48`），`tagText: '已就绪'`
- 实际结果：卡片标签显示"已就绪"，但实际该页面后端 audit 路由处于半下线状态（见 FE-001），进入即报错或显示死页面
- 期望结果：标签应改为"已下线"或直接删除卡片
- 影响：误导用户以为功能可用
- 修改建议：配合 FE-001 一并处理，删除 `home.js:48` 的 history 卡片
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/home.js`（第 48 行）
- 是否需要补测试：否

### FE-021
- 编号：FE-021
- 优先级：P3
- 模块：代码质量
- 页面/按钮/接口：compare.js document.write + closure.toString
- 复现步骤：在「代码比对」页执行比对，触发 `openDiffInNewWindow`（`web/pages/compare.js:156`），查看新窗口源码
- 实际结果：`compare.js:176` 使用 `win.document.write(...)` 拼接 HTML，:213 将一个闭包 `.toString()` 后注入 `<script>` 标签。`document.write` 已被标记为不推荐；闭包内若引用外部变量会静默失效；难以调试
- 期望结果：改为通过 `postMessage` 向新窗口传递数据，新窗口加载独立的 JS 文件
- 影响：可维护性差，但当前功能正常
- 修改建议：长期重构 — 新建 `web/diff-window.html` + `web/diff-window.js`，主窗口 `window.open` 后 `postMessage` 传 diff 数据
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/pages/compare.js`（第 156-358 行）
- 是否需要补测试：否

---

## 六、API 契约问题（API）

### API-001
- 编号：API-001
- 优先级：P2
- 模块：API契约
- 页面/按钮/接口：`POST /api/logs/list`
- 复现步骤：搜索前端代码对 `/api/logs/list` 的调用
- 实际结果：后端 `httpserver.go:183` 注册了 `handleLogsList`，但前端 `web/pages/websphere.js` 所有列文件场景都改走 `/api/logs/list/targets`，不再调用单 server 版本
- 期望结果：要么删除后端死路由，要么在 README 标注「保留供脚本/SDK 使用」
- 影响：死路由，无直接功能影响；维护负担
- 修改建议：保留供 acceptance/外部脚本调用，但在 README API 表里明确标注
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/httpserver.go`、`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_logs_list.go`、`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否（已有 `httpssh_test.go:201` 覆盖）

### API-002
- 编号：API-002
- 优先级：P2
- 模块：API契约
- 页面/按钮/接口：`POST /api/logs/search`
- 复现步骤：grep 前端对 `/api/logs/search`（精确匹配单 server 版）的调用
- 实际结果：后端 `httpserver.go:189` 注册了 `handleLogsSearch`，但前端 `websphere.js:1706` 走的是 `/api/logs/search/multi`；单 server 版只剩 `scripts/acceptance_run.py` 和测试在用
- 期望结果：同 API-001
- 影响：死路由
- 修改建议：保留供 acceptance/SDK，README 标注
- 涉及文件：`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_logs_search.go`、`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### API-003
- 编号：API-003
- 优先级：P0
- 模块：API契约 / 文档
- 页面/按钮/接口：`POST /api/logs/download`（README 声称存在）
- 复现步骤：在 README 第 333 行声称 `POST /api/logs/download` 用于"启动指定文件下载任务"；查 `httpserver.go` 路由表
- 实际结果：后端只注册了 `/api/logs/download-latest`（精确匹配，:187）和 `strings.HasPrefix(path, "/api/logs/download/")`（:268，匹配 `/api/logs/download/{id}/events`、`/api/logs/download/{id}/cancel`）。**没有** plain `POST /api/logs/download` 路由，前端启动下载任务也走的是 `/api/files/download`（`websphere.js:1178`、`files.js:914`）
- 期望结果：README 删除该行，或补一个真的能启动下载的 `/api/logs/download` 接口
- 影响：用户照 README 调用会 404
- 修改建议：删除 README 第 333 行的错误条目
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### API-004
- 编号：API-004
- 优先级：P2
- 模块：API契约 / 文档
- 页面/按钮/接口：`/api/preferences` 方法
- 复现步骤：查 README 第 317-318 行 vs `handlers_preferences.go:16-25` vs 前端 `tail.js:123`、`websphere.js:2511`
- 实际结果：README 写 `POST /api/preferences` 写用户偏好；后端 `handlePreferences` switch 只接受 `GET / PUT`（其它方法 405「仅支持 GET / PUT」）；前端实际用 `PUT`
- 期望结果：README 改为 `PUT /api/preferences`
- 影响：用户照 README 用 POST 会 405
- 修改建议：改 README
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`、`/Users/qi/Documents/spaces/kairo/internal/httpserver/handlers_preferences.go`
- 是否需要补测试：否

### API-005
- 编号：API-005
- 优先级：P2
- 模块：API契约 / 文档
- 页面/按钮/接口：README API 列表整体遗漏（第 306-372 行）
- 复现步骤：把 README 列出的接口和 `httpserver.go` switch 比对
- 实际结果：README 漏列至少 13 个真实存在且被前端调用的接口：`/api/auth/status`、`/api/auth/logout`、`/api/config/export`、`/api/config/import`、`/api/admin/openers`、`/api/admin/download-retention`、`/api/local/open-with`、`/api/choose-file`、`/api/choose-dir`、`/api/downloads/open-dir`、`/api/format/timestamp`、`/api/format/cron-parse`、`/api/format/jsonpath`、`/api/diff/compare`、`/api/compare/folder-scan`、`/api/compare/file-diff`、`/api/http/cases`、`/api/http/envs`、`/api/http/request`
- 期望结果：补全 README API 表
- 影响：用户/集成方照 README 调用会以为接口不存在
- 修改建议：把上面接口按现有分组合并进 README「📡 API 列表」表
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

---

## 七、配置问题（CFG）

### CFG-001
- 编号：CFG-001
- 优先级：P1
- 模块：配置
- 页面/按钮/接口：`auth.enabled` / `auth.tokens`
- 复现步骤：在 `config.yaml.production.example` 里搜 `auth`
- 实际结果：`internal/config/config.go:30-42` 支持 `AuthConfig.Enabled` + `[]AuthToken{Name,Token,AllowedIPs}`，`httpserver.go:147-166` 实际跑 Bearer token 认证；但 `config.yaml.production.example` 完全没有 `auth:` 段
- 期望结果：production.example 给出注释掉的 `auth:` 示例
- 影响：用户不知道可以启用 token 认证
- 修改建议：在 production.example 末尾加注释段
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：是（加测试：把 production.example 解析后跑 Validate，确保示例合法）

### CFG-002
- 编号：CFG-002
- 优先级：P1
- 模块：配置
- 页面/按钮/接口：`app.credential_store` / `app.credential_key`
- 复现步骤：在 production.example 里搜 `credential`
- 实际结果：`config.go:70-74` 支持 `credential_store`（keyring/file/disabled/off/none）+ `credential_key`，`handlers_credentials.go` 整套接口依赖这两个；production.example 没有任何示例
- 期望结果：给出 `credential_store: keyring` 默认示例 + 注释说明 `file` / `disabled` 取值含义
- 影响：用户不知道有 `file` 模式（适合无 keyring 的 headless 服务器）
- 修改建议：在 `app:` 段加注释示例
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：是

### CFG-003
- 编号：CFG-003
- 优先级：P1
- 模块：配置
- 页面/按钮/接口：`app.ssh_compat_profile` + `app.ssh_debug` / `app.ssh_traffic_dump` / `app.ssh_log_max_mb` / `app.ssh_log_keep`
- 复现步骤：在 production.example 里搜 `ssh_`
- 实际结果：`config.go:79-86` 支持这 5 个字段，`Validate()` 校验 `ssh_compat_profile` 取值 `modern / compat / no-ecdh / legacy / auto`；production.example 全部缺失
- 期望结果：给出注释示例
- 影响：用户遇到 OpenSSH 6.2p2 / AIX 老服务器连不上时无文档可循
- 修改建议：加注释段
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

### CFG-004
- 编号：CFG-004
- 优先级：P1
- 模块：配置
- 页面/按钮/接口：`app.external_openers`
- 复现步骤：在 production.example 里搜 `external_openers`
- 实际结果：`config.go:108` 支持 `[]ExternalOpener{Name,Path,Icon}`，`handlers_openers.go` 提供 `/api/admin/openers` GET/PUT，`handlers_local.go:130` 的 `/api/local/open-with` 走它；production.example 完全无示例
- 期望结果：给出注释示例（如 Notepad++ / VS Code）
- 影响：用户不知道有「外部打开器」功能
- 修改建议：加注释段
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

### CFG-005
- 编号：CFG-005
- 优先级：P1
- 模块：配置
- 页面/按钮/接口：`app.download_retention_days` / `app.download_max_count`
- 复现步骤：在 production.example 里搜 `download_retention` / `download_max_count`
- 实际结果：`config.go:112-116` 支持这两个指针字段，`handlers_admin.go:81` 的 `/api/admin/download-retention` GET/PUT 走它，`httpserver.go:79-103` 的 `TriggerCleanup` 用默认值 7 天 / 1000 条清理；production.example 完全无示例
- 期望结果：给出注释示例，说明默认 7 天 / 1000 条
- 影响：用户不知道有自动清理，可能让 downloads 目录无限增长
- 修改建议：加注释段
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

### CFG-006
- 编号：CFG-006
- 优先级：P1
- 模块：配置
- 页面/按钮/接口：`app.free_file_roots`
- 复现步骤：在 production.example 里搜 `free_file_roots`
- 实际结果：`config.go:64` 支持远端路径前缀白名单 `FreeFileRoots []string`，`FreeFileRootsEnabled(path)` 默认空数组=任意路径放行；production.example 没有示例（虽然 `allowed_download_roots` 有注释示例）
- 期望结果：给出注释示例（如 `/opt/IBM/WebSphere` / `/var/log`）
- 影响：用户开了 `enable_free_file_browser: true` 但不知道配 `free_file_roots` 限定前缀；任意路径下载 = 走 SSH 账号全权限，内网分发场景风险高
- 修改建议：加注释段
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

### CFG-007
- 编号：CFG-007
- 优先级：P2
- 模块：配置
- 页面/按钮/接口：`servers[].ssh_profile` / `servers[].host_key_sha256`
- 复现步骤：在 production.example 里搜 `ssh_profile` / `host_key_sha256`
- 实际结果：`config.go:277,282` 支持单 server 覆盖 SSH profile + host key pinning；production.example 两个 server 示例都没用上
- 期望结果：至少有一个 server 给出注释示例
- 影响：高安全场景用户不知道可做 host key pinning
- 修改建议：在某个 server 里加注释示例
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

### CFG-008
- 编号：CFG-008
- 优先级：P2
- 模块：配置
- 页面/按钮/接口：`log_dirs[].list_mode`
- 复现步骤：在 production.example 里搜 `list_mode`
- 实际结果：`config.go:299` 支持 `gnu_find` / `posix_ls` / `auto`，`handlers_logs_list.go` 据此选 ls/find 命令；production.example 的 legacy-node-1（AIX 老系统）未配 `list_mode: posix_ls`，会触发 fallback
- 期望结果：legacy-node-1 这种 AIX 示例应该明确配 `list_mode: posix_ls`
- 影响：用户照搬示例遇到老 AIX 仍可能踩 fallback 路径
- 修改建议：在 legacy-node-1 的 log_dirs 里加 `list_mode: posix_ls`
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

### CFG-009
- 编号：CFG-009
- 优先级：P1
- 模块：配置 / 安全
- 页面/按钮/接口：production.example 默认值安全风险
- 复现步骤：把 production.example 所有注释项当作「用户不写」跑 `Defaults()`
- 实际结果：默认值组合是「全开」：`enable_free_file_browser` 默认 true（任意路径下载开）、`free_file_roots` 默认 []（远端路径不限制）、`allow_custom_download_dir` 默认 true（target_dir 允许）、`allowed_download_roots` 默认 []（target_dir 任意路径）、`auth.enabled` 默认 false（无认证仅本机）、`credential_store` 默认 keyring、`download_retention_days` 默认 7
- 期望结果：production.example 作为生产部署参考，应在文件顶部 WARNING 区块里明确列出这些默认行为
- 影响：用户复制 example 当 config.yaml 用，会在不知情情况下开放任意路径下载
- 修改建议：在 production.example 顶部注释段加「默认安全行为清单」+ 推荐「生产环境至少配 free_file_roots + allowed_download_roots」
- 涉及文件：`/Users/qi/Documents/spaces/kairo/config.yaml.production.example`
- 是否需要补测试：否

---

## 八、文档问题（DOC）

### DOC-001
- 编号：DOC-001
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README API 列表遗漏
- 复现步骤：把 README 列出的接口和 `httpserver.go` switch 比对
- 实际结果：README 漏列至少 13 个真实存在且被前端调用的接口（详见 API-005）
- 期望结果：补全 README API 表
- 影响：用户/集成方照 README 调用会以为接口不存在
- 修改建议：把漏掉的接口合并进 README「📡 API 列表」表
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-002
- 编号：DOC-002
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README 版本徽章
- 复现步骤：对比 README 第 12 行 `Status-v0.5 Released` vs `web/index.html:72` footer `v0.8 · 下载管理 + 工具集 + 全功能完善`
- 实际结果：README badge 停在 v0.5；代码已有 v0.6（tail.html 独立窗口）、v0.7（http/timestamp/cron/jsonpath/compare 页）、v0.8（external_openers / open-with）功能
- 期望结果：徽章升到 v0.8；Release Notes 补 v0.6 / v0.7 / v0.8 段落
- 影响：用户照 README 以为功能截止 v0.5
- 修改建议：改 badge + 补 Release Notes
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-003
- 编号：DOC-003
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README「项目结构」`web/pages/` 列表
- 复现步骤：对比 README 第 567-575 行的 pages 列表 vs `LS web/pages`
- 实际结果：README 只列 9 个（home/websphere/files/formatter/commands/diagnostics/config/downloads/history），实际有 15 个，遗漏：`about.js / compare.js / cron.js / http.js / jsonpath.js / timestamp.js`
- 期望结果：补全 6 个文件
- 影响：项目结构图与实际不符
- 修改建议：补到 README 项目结构树
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-004
- 编号：DOC-004
- 优先级：P1
- 模块：文档
- 页面/按钮/接口：README `ssh_compat_profile` 取值
- 复现步骤：对比 README 第 513、701 行 vs `config.go:587` Validate()
- 实际结果：README 写 `modern | compat-dh-before-ecdh | no-ecdh | legacy-dh-sha1-first`；代码只接受 `modern / compat / no-ecdh / legacy / auto`。README: `compat-dh-before-ecdh` ↔ 代码: `compat`；README: `legacy-dh-sha1-first` ↔ 代码: `legacy`；README 没提 `auto`
- 期望结果：README 改为 `modern | compat | no-ecdh | legacy | auto`
- 影响：用户照 README 配 `compat-dh-before-ecdh` 会被 `Validate()` 拒，启动失败
- 修改建议：改 README 第 513、701 行
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-005
- 编号：DOC-005
- 优先级：P1
- 模块：文档
- 页面/按钮/接口：README `search:` 字段名
- 复现步骤：对比 README 第 515-517 行 vs `config.go:378-384` SearchConfig
- 实际结果：README 配置详解写 `context_lines: 30` 和 `multi_server_concurrency: 4`；代码实际字段是 `default_latest_files / max_matches / default_context_lines / timeout_seconds / max_concurrency`，完全对不上
- 期望结果：README 改成实际字段名
- 影响：用户照 README 配 `context_lines: 30` 不生效（yaml 会忽略未知字段，默认值仍是 30 巧合没暴露问题，但配 `multi_server_concurrency` 就完全不生效）
- 修改建议：改 README 配置详解
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-006
- 编号：DOC-006
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README `credential_store` 取值
- 复现步骤：对比 README 第 508 行 vs `config.go:593` Validate()
- 实际结果：README 写 `# keyring | none`；代码接受 `keyring / file / disabled / off / none`
- 期望结果：README 改为 `# keyring | file | disabled | off | none`
- 影响：用户不知道有 `file` 模式
- 修改建议：改 README
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-007
- 编号：DOC-007
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README「patterns 禁止字符」描述
- 复现步骤：对比 README 第 542 行 vs `config.go:649` Validate()
- 实际结果：README 写「patterns 禁止 `; | & \ ` $ ( ) < > " ' * ?` 等」；代码实际禁的是 `;|&`$(){}[]<>\\\"' \n\r\t`。**代码不禁 `*` 和 `?`**（glob 必需），production.example 默认 patterns 就有 `*.log`
- 期望结果：README 改为「禁止 `; | & ` $ ( ) { } [ ] < > \ " ' 空白`，允许 `* ?`（glob）」
- 影响：用户照 README 以为不能写 `*.log`，被误导
- 修改建议：改 README 第 542 行
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-008
- 编号：DOC-008
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README「app.host 必须 127.0.0.1，0.0.0.0 启动会被拒」描述不完整
- 复现步骤：对比 README 第 498 行 vs `config.go:498-506` Validate()
- 实际结果：README 写「必须是 127.0.0.1 或 localhost，0.0.0.0 启动会被拒」；代码逻辑是：`auth.enabled=false` 时只允许 127.0.0.1/localhost；`auth.enabled=true` 时允许 0.0.0.0 / 内网IP / 127.0.0.1 / localhost
- 期望结果：README 补一句「启用 auth 后允许 0.0.0.0 / 内网 IP」
- 影响：用户想远程访问时不知道正确做法
- 修改建议：改 README 第 498 行
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-009
- 编号：DOC-009
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README「安全设计」缺 auth 段
- 复现步骤：读 README 第 376-391 行安全设计 13 条
- 实际结果：13 条安全设计没一条提到 `auth.enabled + auth.tokens` Bearer token 认证机制；但 `handlers_auth.go` + `httpserver.go:147-166` 实现了完整 token + IP 白名单认证
- 期望结果：安全设计补一条「可选启用 Bearer token 认证 + IP 白名单，启用后可监听 0.0.0.0」
- 影响：用户不知道工具有认证能力
- 修改建议：补一条
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-010
- 编号：DOC-010
- 优先级：P3
- 模块：文档
- 页面/按钮/接口：README 标语 vs 跨平台支持
- 复现步骤：读 README 第 5 行「Windows 绿色版 · 纯 Go 单 exe · 启动即用 · 默认仅本机访问」
- 实际结果：标语只提 Windows，但 badges（line 10）和实际构建脚本支持 macOS / Linux；`internal/httpserver/handlers_local.go:178` 还专门为 Linux 走 xdg-open
- 期望结果：标语改为「跨平台 · 纯 Go 单 exe · 启动即用 · 默认仅本机访问」
- 影响：轻微误导
- 修改建议：改标语
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-011
- 编号：DOC-011
- 优先级：P3
- 模块：文档
- 页面/按钮/接口：README「操作历史（审计）」vs 前端 nav
- 复现步骤：读 README 第 182 行「7. 操作历史（审计）」；查 `web/index.html:62`
- 实际结果：README 把「操作历史」作为正式功能介绍；但 `index.html:62` 该 nav 链接被注释掉，用户从菜单进不去（只能手敲 `#/history` hash）
- 期望结果：要么恢复 nav 链接，要么 README 注明「该功能仅通过 audit.log 文件 + 手敲 hash 访问」
- 影响：用户照 README 找不到入口
- 修改建议：恢复 nav 链接（推荐）或改 README
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/index.html`、`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-012
- 编号：DOC-012
- 优先级：P3
- 模块：文档
- 页面/按钮/接口：README「文件下载」vs 前端 nav 文案
- 复现步骤：对比 README 第 119 行「2. 文件下载（任意路径）」vs `index.html:52`「FTP文件下载」
- 实际结果：README 叫「文件下载」，前端 nav 叫「FTP文件下载」（其实不是真 FTP，是 SFTP）
- 期望结果：统一文案，建议改 nav 为「文件下载」避免误导
- 影响：轻微文案不一致
- 修改建议：改 `index.html:52` 文案
- 涉及文件：`/Users/qi/Documents/spaces/kairo/web/index.html`
- 是否需要补测试：否

### DOC-013
- 编号：DOC-013
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README「项目结构」`internal/` 描述
- 复现步骤：对比 README「项目结构」段 vs `LS internal/`
- 实际结果：README 项目结构树未提及 `internal/dlmanager`、`internal/downloads`、`internal/credentials`、`internal/tailmgr`、`internal/audit`、`internal/diagnostics`、`internal/portreuse`、`internal/logquery` 等多个真实存在的包
- 期望结果：补全 internal/ 子包说明
- 影响：用户读 README 看不到完整代码组织
- 修改建议：补到 README 项目结构树
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

### DOC-014
- 编号：DOC-014
- 优先级：P2
- 模块：文档
- 页面/按钮/接口：README「凭据管理」描述与代码不符
- 复现步骤：读 README 第 230-240 行凭据管理说明
- 实际结果：README 只提「keyring 存储密码」，但 `credentials.go` 实际支持 keyring/file/disabled 三种模式，且 file 模式有 AES-256 加密 + AAD 绑定（system+server）
- 期望结果：README 补充 file 模式 + AAD 绑定说明
- 影响：用户不知道 file 模式存在
- 修改建议：改 README
- 涉及文件：`/Users/qi/Documents/spaces/kairo/README.md`
- 是否需要补测试：否

---

## 九、修复优先级建议

### 第一波（P0，立即修）
1. BE-001：compare/file-diff 任意文件读 → 加路径白名单
2. BE-002：tail 5 分钟强制断开 → 改用 lastActivity
3. FE-001：history.js 僵尸页 → 删除或恢复
4. API-003：README 凭空捏造 `/api/logs/download` → 删行
5. PKG-001/002：tar.gz 缺 vendor + diff2html → 修打包脚本

### 第二波（P1，本周修）
6. BE-003：无 RBAC → 加 token.Role
7. BE-004：配置导出脱敏不全 → 改用 yaml 解析
8. BE-005：SSH host key 默认跳过 → fail-closed
9. BE-006：0.0.0.0 绑定放行 → 显式拒绝
10. BE-007：白名单默认空=无限制 → fail-closed
11. FE-002/003/004/005/006/007：preview 主题/auth、config 裸 fetch、tail setInterval、about 版本号、footer 硬编码
12. CFG-001~006+009：配置示例缺失
13. DOC-004/005：README 字段名错
14. STATIC-001：gofmt 12 文件未格式化

### 第三波（P2/P3，下版本修）
其余 P2/P3 按模块分批处理。
