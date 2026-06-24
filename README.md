<div align="center">

# OpsToolbox · 内网运维工具箱

**Windows 绿色版 · 纯 Go 单 exe · 启动即用 · 默认仅本机访问**

面向内网运维 / DBA / SRE 的本地工具箱，把 **SSH 远程命令、WebSphere 日志排查、文件下载、报文格式化、审计追溯** 这些高频操作收敛到一个零依赖、绿色运行的单 exe 中。

[![Go Version](https://img.shields.io/badge/Go-1.20%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-4F4F4F)](#)
[![License](https://img.shields.io/badge/License-Internal%20Use-orange)](#)
[![Status](https://img.shields.io/badge/Status-v0.5%20Released-brightgreen)](#)
[![Dependencies](https://img.shields.io/badge/Deps-zero%20runtime-2ea44f)](#)
[![Binary](https://img.shields.io/badge/Single%20Exe-%E2%9C%93-success)](#)

[快速开始](#-快速开始) · [功能矩阵](#-功能矩阵) · [架构](#-架构) · [API 列表](#-api-列表) · [构建](#-构建与发布) · [更新日志](#-release-notes)

</div>

---

## ✨ 为什么是 OpsToolbox

| 痛点 | OpsToolbox 的解法 |
| --- | --- |
| 排查 WebSphere 老系统日志要 SSH 登十几台机器，循环 `grep` / `tail` | 浏览器里勾服务器 × 日志目录，1–16 路并发搜索，结果按主机分组 + 命中数 + 耗时 |
| 老 OpenSSH 6.2p2 / AIX / WebSphere 服务器 SSH 连不上 | 内置 3 套 SSH compat profile 自动 fallback（curve25519 → ECDH → DH-sha1），`x/crypto v0.31.0` 完整支持 `diffie-hellman-group14-sha256` |
| GBK 编码的 SystemOut.log 拉到本地变乱码 | 在配置里写一行 `encoding: gbk`，Go 侧 `x/text/encoding` 透明转换 |
| 每次远程命令都要等 `timeout` 命令 | 客户端 `context + SIGTERM(1s) → SIGKILL` 三段式超时，**远端不需要 `timeout`** |
| 一次要拉多个配置文件 / 报表 | 「文件下载」菜单按 SSH 账号实际权限浏览任意路径，多文件 zip，进度条 + 取消 |
| 配置文件改完忘了重启 | 「系统配置」页可视化编辑，**保存即原子改写 yaml，无需重启** |
| 想追溯谁什么时候拉了哪个文件 | `logs/audit.log` 全操作流水 + `downloads/*.meta.json` sidecar 元数据 |
| 密码写在 yaml 里很危险 | OS 钥匙串（Keychain / DPAPI / Secret Service）按 `(system,server,user)` 三元组存储 |
| 工具箱上线后没意识到任意路径下载开着 | 启动日志 WARNING 明确打 `enable_free_file_browser` + `free_file_roots` 状态 |

---

## 🚀 快速开始

### 方式 A：直接运行发版 exe（同事拿到包就这样用）

1. 解压发版 zip 到任意目录，比如 `D:\ops-toolbox\`
2. **不要双击 exe**，先用 `cmd` 启动看完整日志：

   ```bat
   cd /d D:\ops-toolbox
   OpsToolbox.exe
   ```

3. 看到 `工具箱已启动: http://127.0.0.1:18080` 后，浏览器会自动打开；没自动开就手动访问这个地址。

### 方式 B：开发模式

```bash
git clone <repo-url>
cd ops-toolbox
go run .
```

需要 Go 1.20+，会自动打开 `http://127.0.0.1:18080`。

### 第一次跑要做什么

1. 浏览器左侧菜单 → **系统配置**
2. 录入「业务系统 / 服务器 / 日志目录」（三层：系统 → 服务器 → 目录，支持可视化增删改）
3. 回到 **WebSphere 日志**，选目标 → 列文件 → 搜索 → Tail
4. 需要 properties / xml / jar 等不在白名单目录的文件？用 **文件下载**

---

## 🎯 功能矩阵

### 1. WebSphere 日志助手

定位：白名单模式下的安全日志检索，专为「生产环境固定日志目录」设计。

| 子功能 | 说明 |
| --- | --- |
| **多服务器 × 多目录 多对多** | 业务系统下每台服务器展开自己的日志目录 checkbox；所有功能（搜索/列文件/下载/tail）统一走 `targets = server × dir` 矩阵 |
| **多服务器并行搜索** | 1–16 路并发 `grep`，按主机分组返回 `{server, hits, elapsed_ms}`，失败不影响其他 |
| **搜索语法** | `Exception`、`A && B`、`A \|\| B`、`A && !B`，关键词白名单校验 |
| **上下文查看** | 命中行前后各 30 行（在配置里可调），用 `sed -n a,bp` 拿，绝不下整个文件 |
| **下载最新 N 个** | 默认最新 1 个，可配 1–5 个，带 sidecar 元数据 |
| **指定文件下载** | 勾选任意文件 → 异步任务 + SSE 进度 + 可取消 + 多文件 zip |
| **实时 Tail** | SSE 长连接，5000 行环形缓冲 + `requestAnimationFrame` 批量 flush，独立 `/tail.html` 全屏窗口 |
| **文件名模糊搜索** | 子串 / glob（`*.log` / `SystemOut*` / `log?`），实时显示 `显示 X / Y` |
| **文件名点击预览** | 新窗口 + modal 双模式；编码自动归一（utf-8 / gbk / gb18030）；NUL 字节检测防乱码；二进制文件提示 |

### 2. 文件下载（任意路径）

定位：跳出白名单，按 SSH 账号实际权限拿任意文件。

| 子功能 | 说明 |
| --- | --- |
| **类 FTP 浏览** | 面包屑导航、子目录进入、绝对路径直接输入跳转 |
| **多文件 zip 打包** | 进度条 + 可取消；同任务同名文件本地加 idx 前缀避免覆盖 |
| **常用目录** | 按 `(system, server)` 分组的本地收藏，localStorage 持久化，可改别名 / 上下移 / 删除 |
| **指定本地目录** | `target_dir` 字段任意指定落盘路径，`validateTargetDir` 校验（绝对路径 + mkdir -p + 写探针） |
| **完成通知 + 路径跳转** | 右上角浮动通知栈，含「打开所在目录 / 复制路径 / 查看下载历史」按钮 |
| **安全开关** | `app.enable_free_file_browser: false` 一键关闭整个 `/api/files/*`，白名单模式照常生效 |
| **可选前缀白名单** | `app.free_file_roots` 限定可访问的远端路径前缀（推荐生产开） |

**与日志助手的取舍**：

| 维度 | 日志助手 | 文件下载 |
| --- | --- | --- |
| 路径 | 仅 `log_dirs` 白名单 | 任意绝对路径（按 SSH 账号权限） |
| 用途 | 排查日志、搜索、上下文 | 拿 properties / xml / sql / jar 等 |
| 写权限 | 无 | 无（依然只读 + 下载） |
| 审计 | `op=logs.*` | `op=files.*` |

### 3. 报文格式化

完全本地工具，无网络：

- **JSON**：格式化（2 空格 / 4 空格 / Tab / 自定义）、压缩、严格校验（多余尾随字符报错）
- **XML**：格式化、压缩
- 单元测试覆盖 `decodeStrictJSON` / `FormatJSON` / `MinifyJSON` / `ValidateJSON` / `FormatXML` / `MinifyXML`

### 4. 系统配置（可视化编辑器）

- 业务系统 / 服务器 / 日志目录 **三层结构**：
  - 业务系统：4px primary border-left + 编号 badge
  - 服务器：3px success border-left + 编号 `1.1`
  - 日志目录：2px dashed warn border-left + 编号 `1.1.1`
- 字段说明 + placeholder + tooltip 全面提示
- 「📋 复制服务器（含日志目录）」一键复制并自动加 `-copy` 后缀
- 保存 = 原子改写 `config.yaml`，COW Manager 替换，**前端无感知**热替换
- 离开页面有 unsaved 提示
- 顶部 + 底部双保存按钮，sticky 保存栏
- 编码值归一：`gbk/gb18030 → gbk`；非法值（`shift-jis` 等）拒绝保存

### 5. 环境自检（Diagnostics）

一键收集：

- **App info**：版本、配置路径、工作目录、监听地址
- **Build info**：Go runtime 版本、嵌入的 `x/crypto` 实际版本、build commit
- **Runtime info**：GOOS / GOARCH、CPU 数、运行时长、监听状态
- **Tools info**：`find -printf` 是否可用（决定能否列日志文件）
- **Servers check**：每台配置服务器握手探测（独立 timeout，失败不影响其他），输出 hostname / 端口 / SSH 协议 / 错误摘要

### 6. 下载历史

- `downloads/` 目录所有文件 + 对应 `.meta.json`
- `meta.json` 包含 system / server / host / dir / dir_alias / file / encoding / kind / downloaded_at
- 卡片视图，单条删除、一键清空
- 任意路径下载的 `.properties` / `.xml` / `.gz` 等同样进历史

### 7. 操作历史（审计）

- `logs/audit.log` 全操作流水，按 `op / system / server / result` 过滤
- 支持导出 CSV / JSON
- **永不记录** SSH 密码、日志正文
- 字段示例：
  ```
  ts=2026-06-19 10:30:00.123 op=ssh.test system=信贷生产 server=prod-node-1 result=ok
  ts=2026-06-19 10:30:10.789 op=logs.search system=信贷生产 server=prod-node-1 dir=/opt/... query=Exception && userinfo result=ok hits=8
  ```

### 8. 常用命令（占位，预留扩展位）

当前为目录索引占位页，业务上暂未沉淀。

---

## 🛠 技术栈

### 后端

| 依赖 | 版本 | 用途 |
| --- | --- | --- |
| **Go** | 1.20+ | 主语言，`go 1.20` directive（兼容 Win7 编译） |
| [`github.com/pkg/sftp`](https://github.com/pkg/sftp) | v1.13.6 | SFTP 协议实现：列目录、Stat、下载、Open |
| [`golang.org/x/crypto/ssh`](https://pkg.go.dev/golang.org/x/crypto/ssh) | v0.31.0 | SSH 客户端 + 3 套 compat profile |
| [`golang.org/x/text`](https://pkg.go.dev/golang.org/x/text) | v0.21.0 | `simplifiedchinese.GBK` / `GB18030` 透明编码转换 |
| [`github.com/zalando/go-keyring`](https://github.com/zalando/go-keyring) | v0.2.8 | OS 钥匙串统一抽象 |
| [`github.com/danieljoos/wincred`](https://github.com/danieljoos/wincred) | v1.2.3 | Windows DPAPI（`go-keyring` 后端） |
| [`github.com/godbus/dbus/v5`](https://github.com/godbus/dbus) | v5.2.2 | Linux Secret Service / D-Bus |
| [`gopkg.in/yaml.v3`](https://gopkg.in/yaml.v3) | v3.0.1 | `config.yaml` 解析 + 原子写回 |
| [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys) | v0.28.0 | 平台特定系统调用 |
| **标准库** | — | `net/http`、`embed`（静态资源内嵌）、`context`、`os/exec`、`crypto/sha256` |

### 前端

| 选型 | 说明 |
| --- | --- |
| **原生 JavaScript (ES2020)** | 无 React / Vue 依赖，单文件 IIFE |
| **模块拆分** | `core.js` / `state.js` / `api.js` / `theme.js` + `pages/*.js`（home / websphere / files / formatter / commands / diagnostics / config / downloads / history） |
| **CSS 变量主题** | `:root[data-theme=...]` 4 套主题（dark / light / green / hc）；inline script 在 `<head>` 提前设 `data-theme` 防 FOUC |
| **Node 单测** | `web/app.test.js` 覆盖 `escapeHtml` / `formatBytes` / `formatTime` / `trimMiddle` / `cssEscape` / `pctText` / `validate` |
| **go:embed** | `web/` 整个目录内嵌进二进制，无外部静态文件 |
| **SSE (Server-Sent Events)** | Tail 流 + 下载进度流，长连接，`http.Server.WriteTimeout = 0` |
| **`requestAnimationFrame`** | Tail 行批量 flush，避免 `textContent +=` 整段重排 |
| **localStorage** | 主题 / 常用目录 / 表单偏好持久化 |

### 工程实践

- **`vendor/` 已 commit**：clone 后无网也能 `-mod=vendor` 构建
- **`go:embed web`**：静态资源打进单 exe
- **COW Config Manager**：每次 `Replace` 整体换指针，handler 读快照不被撕裂
- **Goroutine Worker Pool**：多服务器搜索/列文件走 4 路并发 + `errgroup` 风格隔离
- **`context` 优先**：所有远程命令 / 下载 / Tail 都用 ctx 控制超时
- **三段式超时**：`ctx deadline → SSH session.Signal(SIGTERM) → 1s 后 SIGKILL`
- **原子写回 yaml**：`tmpfile + rename(2)`，损坏不污染线上配置
- **接口风格**：进程内 `fs.FS` 接口（`httpserver.serveStatic`）+ `sftpDialer` 包级变量（集成测试注入 fake）
- **测试覆盖**：`internal/*` 每个包都有 `_test.go`，集成测试用 `mock_sshd.py` + injected `sftpDialer`

---

## 🏗 架构

### 分层

```
┌──────────────────────────────────────────────────────────────┐
│  Web Browser (单页应用，hash-router 路由)                    │
│  ┌─────────┬─────────┬─────────┬──────────┬─────────────┐    │
│  │ home    │ websphere│ files  │formatter │ diagnostics │   │
│  │ config  │ downloads│ history│ commands │             │   │
│  └─────────┴─────────┴─────────┴──────────┴─────────────┘    │
│           IIFE 风格 · vanilla JS · 4 套主题                  │
└──────────────────────────────────────────────────────────────┘
                          │ fetch + EventSource(SSE)
                          ▼
┌──────────────────────────────────────────────────────────────┐
│  HTTP Server (net/http, 127.0.0.1:18080)                     │
│  ┌──────────────────────────────────────────────────────┐    │
│  │ httpserver                                           │    │
│  │ ├── httpserver.go (路由表 + 配置/服务组装)            │    │
│  │ ├── handlers_ssh.go / handlers_logs_*.go / ...        │    │
│  │ ├── response.go / helpers.go                          │    │
│  │ └── *_test.go (含集成测试：fake SFTP backend)         │    │
│  └──────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
                          │
       ┌──────────────────┼───────────────────────┐
       ▼                  ▼                       ▼
┌─────────────┐    ┌────────────────┐    ┌─────────────────┐
│ config      │    │ sshclient      │    │ sftpclient      │
│ COW Manager │    │ 3 套 compat    │    │ Open/Stat/      │
│ 原子 yaml   │    │ profile 自动   │    │ ReadDir/        │
│ 热替换      │    │ fallback + ctx │    │ DownloadFile    │
│             │    │ 三段式超时     │    │ [WithProgress]  │
└─────────────┘    └────────────────┘    └─────────────────┘
       │                  │                       │
       ▼                  ▼                       ▼
┌─────────────────────────────────────────────────────────────┐
│  跨领域能力                                                  │
│  ├── audit       操作审计 (logs/audit.log)                   │
│  ├── credentials OS 钥匙串 (Keychain / DPAPI / libsecret)    │
│  ├── downloads   sidecar 元数据 (downloads/*.meta.json)      │
│  ├── dlmanager   异步下载任务池 + SSE 进度广播               │
│  ├── tailmgr     Tail 会话池 + Streamer 接口（可 mock）      │
│  ├── logquery    后端命令模板（find/grep/sed/sort/head）     │
│  ├── formatter   JSON / XML 格式化（本地）                   │
│  └── diagnostics 环境自检（App/Build/Runtime/Tools/Servers）│
└─────────────────────────────────────────────────────────────┘
```

### SSH 兼容性矩阵

| Profile | 默认 KEX | 适用场景 |
| --- | --- | --- |
| **modern (默认)** | curve25519 / ecdh-sha2-* / diffie-hellman-group14-sha256 | OpenSSH 7.4+ / 现代 Linux |
| **compat-dh-before-ecdh** | dh-group14-sha256 优先 | OpenSSH 6.2p2 / 6.5 |
| **no-ecdh** | 完全去掉 ECDH | 某些老 OpenSSL 的 ECDH_INIT 后 RST bug |
| **legacy-dh-sha1-first** | group14-sha1 + ssh-dss | OpenSSH 5.x / 6.0 / AIX / 老堡垒机 |

握手失败自动 fallback 到下一套，外层 `sshDialOuterTimeout = 45s`（3 × 12s + buffer），单套 `sshAttemptTimeout = 10s`。

---

## 📡 API 列表

所有接口只接受本机访问（`127.0.0.1`），handler 入口统一校验，跨域 / 路径穿越全面拒绝。

### SSH / 配置

| Method | Path | 用途 |
| --- | --- | --- |
| GET | `/api/config` | 读取配置（不含密码），返回快照 |
| POST | `/api/ssh/test` | 测试 SSH 连接（password 缺省时从 keyring 读） |
| GET | `/api/admin/servers` | 服务器清单（admin 用） |
| GET | `/api/preferences` | 用户偏好（前端持久化） |
| POST | `/api/preferences` | 写用户偏好 |

### 日志助手

| Method | Path | 用途 |
| --- | --- | --- |
| POST | `/api/logs/list` | 列日志目录文件 |
| POST | `/api/logs/list/targets` | 多 server × 多 dir 列文件 |
| POST | `/api/logs/search` | 单 server 关键词搜索 |
| POST | `/api/logs/search/multi` | 多 server 并行搜索（1–16 路） |
| POST | `/api/logs/context` | 查看某行上下文（前后各 N 行） |
| POST | `/api/logs/tail/start` | 启动实时 tail（返回 `{id}`） |
| GET | `/api/logs/tail/{id}/events` | SSE 实时流 |
| POST | `/api/logs/tail/{id}/stop` | 停止 tail |
| POST | `/api/logs/download-latest` | 下载最近 1–5 个日志 |
| POST | `/api/logs/download` | 启动指定文件下载任务 |
| GET | `/api/logs/download/{id}/events` | SSE 进度流 |
| POST | `/api/logs/download/{id}/cancel` | 取消任务 |

### 文件下载（任意路径）

| Method | Path | 用途 |
| --- | --- | --- |
| POST | `/api/files/list` | 列任意远端目录（3 种模式：单 server+path / 多 server+path / targets[]） |
| POST | `/api/files/preview` | 预览文件（默认 1MB，上限 10MB） |
| POST | `/api/files/download` | 启动下载任务，支持 `target_dir` 指定本地落盘路径 |
| GET | `/api/files/download/{id}/events` | SSE 进度流 |
| POST | `/api/files/download/{id}/cancel` | 取消任务 |

### 本地集成

| Method | Path | 用途 |
| --- | --- | --- |
| POST | `/api/local/open-folder` | 在资源管理器打开本地目录 |
| POST | `/api/local/reveal-file` | 定位本地文件 |

### 凭据 / 审计 / 下载 / 格式化 / 诊断

| Method | Path | 用途 |
| --- | --- | --- |
| POST | `/api/credentials/save` | 保存 SSH 密码到 OS 钥匙串 |
| GET | `/api/credentials/has` | 检查是否已存密码（不返回密码） |
| POST | `/api/credentials/clear` | 删除已存密码 |
| GET | `/api/audit/recent` | 操作历史（按 op/system/server/result 过滤） |
| GET | `/api/audit/export.csv` | 导出 CSV |
| GET | `/api/audit/export.json` | 导出 JSON |
| GET | `/api/downloads/list` | 下载历史（含 sidecar 元数据） |
| DELETE | `/api/downloads/<name>` | 删除单条下载 |
| POST | `/api/downloads/all` | 清空 downloads/ |
| POST | `/api/format/json` | JSON 格式化 / 压缩 / 校验 |
| POST | `/api/format/xml` | XML 格式化 / 压缩 |
| GET | `/downloads/<file>` | 取本地下载文件（RFC 5987 `filename*`） |
| GET | `/api/diagnostics` | 环境自检（App/Build/Runtime/Tools/Servers） |

---

## 🔒 安全设计

1. **只监听 127.0.0.1**：`0.0.0.0` 直接被配置校验拒绝，不向局域网暴露
2. **不开放任意 shell**：所有远程命令由后端固定模板生成（`find` / `grep` / `sed` / `sort` / `head` / `cat` 组合）
3. **白名单路径**：日志助手的目录必须来自 `log_dirs`；文件名只能来自 `find` 列出结果
4. **文件浏览器按账号权限**：实际访问控制交给 SSH server 端（账号 + 文件系统权限 + sshd_config）
5. **关键词严格转义**：搜索关键词做白名单校验，拒绝 ` ' \ $ ; & | < > ( ) { } [ ]` 等
6. **密码零持久**：从不写进 `audit.log`、URL、错误信息；可选 OS 钥匙串按 `(system,server,user)` 三元组加密存储
7. **只读不写**：不提供上传 / 删除 / 改远程文件的能力（文件浏览器同样只读 + 下载）
8. **路径穿越防护**：所有用户输入做绝对路径校验、拒绝 `..` / NUL / 换行；URL 路径穿越 404
9. **超时硬约束**：远程命令走 ctx + 三段式（SIGTERM → 1s → SIGKILL）；下载任务 30 分钟硬超时
10. **下载文件只落 `downloads/`**：任意 `target_dir` 必须绝对路径 + 写探针通过
11. **硬上限**：单次最多 100 个文件，30 分钟超时
12. **CORS / 跨域**：所有响应带 `X-Content-Type-Options: nosniff` / `Referrer-Policy: no-referrer` / `X-Frame-Options: DENY`，禁止跨域
13. **下载文件落地命名安全**：同任务同名文件本地加 `001/002/...` 前缀，不互相覆盖

---

## 🧪 测试与质量保障

### 后端

```bash
# 全包测试（包含 mock SSH server 集成测试）
go test ./...

# 集成测试会启动 mock_sshd / fake-websphere（Python helper）
# 覆盖率详见 docs/qa/
```

| 包 | 测试要点 |
| --- | --- |
| `internal/config` | `TestReplace_FullRestartRoundTrip` 5 阶段端到端；GBK 归一；非法 encoding 拒绝 |
| `internal/httpserver` | 多 server 列文件 4 个 case（happy / partial / empty / backward-compat）；fake SFTP 注入 |
| `internal/sshclient` | 兼容握手 / 关键词转义 / safeWriter 截断 / 错误分类 |
| `internal/dlmanager` | 任务生命周期 / SSE 广播 / GC 周期可调 |
| `internal/tailmgr` | Streamer 接口 mock / ctx 取消 / 流错 / GC |
| `internal/formatter` | JSON 严格解码 / 格式化 / 压缩 / 校验 / XML |
| `internal/diagnostics` | 环境收集 / 工具探测 / 服务器握手探测 |

### 前端

```bash
# Node 单测（pure 函数）
node --test web/app.test.js
```

覆盖 `escapeHtml` / `formatBytes` / `formatTime` / `trimMiddle` / `cssEscape` / `pctText` / `validate`。

### CI 辅助脚本

```bash
python scripts/acceptance_run.py   # 端到端验收清单（基于 docs/ACCEPTANCE.md）
python scripts/mock_sshd.py        # 假 sshd，给集成测试用
python scripts/fake-websphere/     # 假 WebSphere 日志布局
```

---

## 🏗 构建与发布

### macOS / Linux 上交叉编译 Windows exe

```bash
cd ops-toolbox

# 第一次 clone 后，把依赖固化到 vendor/
go mod vendor

# 主版本（Win10/11）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -mod=vendor -trimpath -ldflags "-s -w" \
  -o OpsToolbox.exe .

# 或用脚本（自动检测 vendor/ 是否存在）
./scripts/build_windows_amd64.sh v0.5.0
./scripts/package_windows.sh v0.5.0
# → dist/ops-toolbox-v0.5.0-windows.zip
```

### 编译 Win7 兼容版

Go 1.21+ **不再支持 Windows 7/8/Server 2008/2012**（要求 Win10/Server 2016+）。要兼容 Win7 **必须**用 Go 1.20.x 编译：

```bash
# 1) 准备独立 Go 1.20.x
curl -L -o /tmp/go1.20.14.darwin-amd64.tar.gz \
  https://go.dev/dl/go1.20.14.darwin-amd64.tar.gz
mkdir -p ~/sdk/go120
tar -C ~/sdk/go120 -xzf /tmp/go1.20.14.darwin-amd64.tar.gz --strip-components=1

# 2) 编译（脚本同样会自动 -mod=vendor）
export GO120_HOME=~/sdk/go120
./scripts/build_windows_amd64_win7_go120.sh v0.5.0
# → dist/ops-toolbox-v0.5.0-win7/OpsToolbox_win7.exe
```

### 发版给同事要打什么包

```
OpsToolbox.exe              # 主程序（Win10/11）
# 或 OpsToolbox_win7.exe   # Win7 兼容版
config.yaml                 # 配置文件
config.yaml.production.example  # 生产示例（首次部署参考）
README.md                   # 本文件
start.bat                   # 控制台启动脚本
downloads/                  # 下载保存目录
logs/                       # 程序 + 审计日志
data/                       # 预留数据目录
```

> 不要把源码、scripts、docs、dist 目录发出去。

---

## ⚙️ 配置详解

### `config.yaml` 完整结构

```yaml
app:
  name: 内网运维工具箱
  host: 127.0.0.1                # 必须是 127.0.0.1 或 localhost，0.0.0.0 启动会被拒
  port: 18080                    # 端口被占用就换一个
  auto_open_browser: true
  download_dir: ./downloads
  log_dir: ./logs
  data_dir: ./data
  enable_free_file_browser: true         # false → /api/files/* 全部 403
  free_file_roots:                       # 可选：文件浏览器的路径前缀白名单
    - /opt/IBM/WebSphere
    - /var/log
  credential_store: keyring             # keyring | none
  ssh_debug: false                      # 打开 logs/ssh_debug.log
  ssh_traffic_dump: false               # 打开 logs/ssh_traffic.log（C→S / S→C 分向）
  ssh_log_max_mb: 8                     # 单日志文件上限
  ssh_log_keep: 3                        # 滚动保留份数
  ssh_compat_profile: modern            # modern | compat-dh-before-ecdh | no-ecdh | legacy-dh-sha1-first

search:
  context_lines: 30              # 上下文查看前后行数
  multi_server_concurrency: 4    # 多服务器并发数（1–16）

systems:
  - name: 信贷生产
    description: WebSphere 生产集群
    servers:
      - name: prod-node-1
        host: 10.10.10.11
        port: 22
        username: wasadmin
        auth_type: password
        log_dirs:
          - name: server1日志
            path: /opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1
            patterns:
              - SystemOut*.log
              - SystemErr*.log
              - "*.log"
            encoding: utf-8             # utf-8 (默认) | gbk | gb18030
```

### 约束

- **密码不要写在这里** — 每次 Web 页面输入，或勾选「记住密码」存进 OS 钥匙串
- `log_dirs.path` 白名单，工具只访问声明过的目录
- `patterns` 禁止 `; | & \ ` $ ( ) < > " ' * ?` 等
- `encoding` 仅支持 `utf-8` / `gbk` / `gb18030`
- 远程 GBK 日志 → `encoding: gbk`，Go 侧自动转 UTF-8 给页面

---

## 📁 项目结构

```
ops-toolbox/
├── main.go                      # 入口：解析 -workdir / 加载 config / 起 HTTP server
├── debug_run.go                 # 调试辅助
├── config.yaml                  # 运行时配置
├── config.yaml.production.example
├── go.mod / go.sum              # 依赖锁定（go 1.20）
├── vendor/                      # 已固化依赖，clone 后无网可编
├── web/                         # 嵌入式前端
│   ├── index.html               # SPA 骨架 + 顶部菜单 + 主题 inline 防 FOUC
│   ├── preview.html             # 文件预览独立新窗口
│   ├── tail.html                # Tail 全屏窗口
│   ├── style.css                # CSS 变量主题（dark/light/green/hc）
│   ├── theme.js                 # 主题切换（localStorage 持久）
│   ├── core.js / api.js / state.js / app.js
│   ├── app.test.js              # Node 单测
│   └── pages/
│       ├── home.js              # 首页
│       ├── websphere.js         # 日志助手
│       ├── files.js             # 文件下载
│       ├── formatter.js         # JSON/XML 格式化
│       ├── commands.js          # 常用命令（占位）
│       ├── diagnostics.js       # 环境自检
│       ├── config.js            # 系统配置可视化编辑器
│       ├── downloads.js         # 下载历史
│       └── history.js           # 操作历史
├── internal/
│   ├── audit/                   # 审计日志（线程安全，不含密码）
│   ├── config/                  # config.yaml 加载 + 校验 + COW Manager
│   ├── credentials/             # OS 钥匙串抽象（Keychain/DPAPI/libsecret）
│   ├── diagnostics/             # 环境自检（App/Build/Runtime/Tools/Servers）
│   ├── dlmanager/               # 异步下载任务池 + SSE 广播（GC 周期可调）
│   ├── downloads/               # sidecar 元数据（*.meta.json）
│   ├── formatter/               # JSON / XML 格式化
│   ├── httpserver/
│   │   ├── httpserver.go        # 路由表 + 服务组装
│   │   ├── response.go / helpers.go / open_dir.go
│   │   ├── handlers_ssh.go / handlers_logs_*.go / handlers_files.go
│   │   ├── handlers_tail.go / handlers_admin.go / handlers_audit.go
│   │   ├── handlers_credentials.go / handlers_downloads.go / handlers_format.go
│   │   ├── handlers_diagnostics.go / handlers_preferences.go / handlers_local.go
│   │   └── *_test.go            # 单测 + 集成测试（fake SFTP）
│   ├── logquery/                # 后端命令模板
│   ├── sftpclient/              # SFTP 客户端封装
│   ├── sshclient/               # SSH 客户端（3 套 compat profile + ctx 超时）
│   └── tailmgr/                 # Tail 会话池 + Streamer 接口
├── scripts/
│   ├── build_windows_amd64.sh
│   ├── build_windows_amd64_win7_go120.sh
│   ├── package_windows.sh
│   ├── acceptance_run.py
│   ├── mock_sshd.py             # 假 sshd，给集成测试用
│   ├── fake-websphere/          # 假 WebSphere 日志布局
│   └── start.bat
└── docs/
    ├── ACCEPTANCE.md            # 验收清单
    ├── banner.svg               # 顶部 banner
    └── REVIEW-FIX-*.md / v0.5-*.md  # 修复与发版记录
```

---

## 🧭 路线图（已规划 / 未做）

| 状态 | 功能 |
| --- | --- |
| ✅ | 配置文件读取 + 白名单校验 |
| ✅ | 系统配置可视化编辑器（COW 热替换） |
| ✅ | 多服务器并行搜索（1–16 路） |
| ✅ | 实时 Tail（SSE） |
| ✅ | 指定文件下载 + SSE 进度 + 取消 |
| ✅ | 多文件 zip 打包下载 |
| ✅ | 任意路径下载（按 SSH 账号权限） |
| ✅ | 文件名点击预览（编码自动识别） |
| ✅ | 常用目录管理（localStorage） |
| ✅ | 文件名模糊搜索（glob / 子串） |
| ✅ | OS 钥匙串密码存储 |
| ✅ | 主题切换（4 套） |
| ✅ | OpenSSH 6.2p2 / AIX / WebSphere SSH 兼容 |
| ✅ | 环境自检（Diagnostics） |
| ✅ | 下载历史 + 操作历史 + CSV/JSON 导出 |
| ✅ | 「在资源管理器打开」「定位文件」 |
| 🚧 | 常用命令模块（当前占位） |
| 🚧 | 数据库连接（MySQL/PG/Redis） |
| ❌ | 任意命令执行（设计为禁止） |
| ❌ | 日期 / 日历 / 文本处理模块（已从导航移除） |

---

## ❓ 常见问题

<details>
<summary><b>Q: 启动后浏览器没自动打开？</b></summary>

检查 `config.yaml` 的 `auto_open_browser: true`；或手动访问 `http://127.0.0.1:18080`。
</details>

<details>
<summary><b>Q: 端口被占用？</b></summary>

修改 `app.port`，比如改成 `18090`。
</details>

<details>
<summary><b>Q: 搜索中文 / 特殊字符失败？</b></summary>

含 `; | & $ \ ` `` 等字符的关键词会被直接拒绝（不是过滤，是拒绝）；去掉这些字符。
</details>

<details>
<summary><b>Q: 中文乱码？</b></summary>

远端日志是 GBK 的话，在 `log_dirs` 里加 `encoding: gbk`。
</details>

<details>
<summary><b>Q: 改了 config.yaml 不生效？</b></summary>

v0.2 起在「系统配置」页直接编辑保存即可，无需重启。手编 yaml 仍有效，但需要重启进程。
</details>

<details>
<summary><b>Q: 想在多台服务器上并发搜索？</b></summary>

已在 WebSphere 日志页支持，勾选多台服务器（最多 16 路），按服务器分组返回结果。
</details>

<details>
<summary><b>Q: 想下载白名单目录之外的文件（比如 properties / xml / jar）？</b></summary>

用「文件下载」菜单，按 SSH 账号权限浏览任意目录；写操作依然禁止。建议生产环境配置 `free_file_roots` 限定前缀。
</details>

<details>
<summary><b>Q: Win7 上跑不起来？</b></summary>

必须用 Go 1.20.x 编译的 `OpsToolbox_win7.exe`，主版本在 Win7 上跑不起来（Go 1.21+ 不再支持 Win7）。
</details>

<details>
<summary><b>Q: 任意路径下载安全性怎么保证？</b></summary>

- 写权限全禁（无上传 / 删除 / 改权限）
- 实际访问控制交给 SSH server 端（账号 + 文件系统权限 + sshd_config）
- 推荐生产配置 `app.free_file_roots` 限定可访问的远端路径前缀
- 临时下线可用 `app.enable_free_file_browser: false` 一键关停
</details>

<details>
<summary><b>Q: SSH 连不上老服务器（OpenSSH 6.2p2 / AIX）？</b></summary>

启动时日志会显示 `SSH 默认 compat profile: ...`，可改成 `legacy-dh-sha1-first`（最兼容）或 `compat-dh-before-ecdh`（中间档）。也可以打开 `app.ssh_debug: true` 看握手细节（`logs/ssh_debug.log`）。
</details>

---

## 📜 Release Notes

### v0.5（当前）— P0 修复 + UX 大改造

#### P0 Bug Fix

- **#20 配置持久化**（重启 + tab 切换都不丢）
  - 前端 `OTB.state.configEditor` 提到模块级 + `loaded` flag，切 tab 不再 GET 覆盖未保存编辑
  - 离开页面 `unsavedConfig` 标志 + `window.confirm`
  - 后端 `Manager.Replace` 已验证正确
  - 测试：`TestReplace_FullRestartRoundTrip` 5 阶段端到端
- **#6 GBK 编码保存后仍显 GBK**：显式 `encSel.value = 'gbk'` + 后端 `Defaults()` 归一 `gbk/gb18030→gbk`、`Validate()` 拒绝 `shift-jis`
- **#7 多服务器列出文件全部展示**：`/api/files/list` 支持 3 种互斥模式；多 server 走 4 路并发；响应 `{servers:[{server,ok,files}], ok_count, fail_count, total_count}`
- **#9 实时 tail 卡死浏览器**：`appendTailLine` 用 `pendingTailLines` 缓冲 + `requestAnimationFrame` 批量 flush；`appendChild TextNode` 而非 `textContent +=`；限速 5000 行
- **#8 日志搜索 stale 30s 真连 10.0.0.1:22**：用 fake SSH server 替真 dial

#### 体验大改造

- **#19 背景主题切换**：4 套主题（dark / light / green / hc），CSS 变量抽取到 `:root[data-theme=...]`，右上角按钮 + localStorage 记忆
- **#3 配置页保存按钮位置**：sticky 保存栏 + 底部再加一个保存按钮
- **#4 紫色「系统」tag 横排显示**：`writing-mode: horizontal-tb; white-space: nowrap; min-width: 36px;`
- **#5 配置页字段说明 + 复制服务器按钮解释**
- **#15 业务系统 / 服务器 / 日志目录 三层视觉**：三色 border-left + 编号 badge
- **#11 默认勾选**：记住密码默认勾（keyring 不可用时禁用）；目标服务器默认全选
- **#10/#12 多服务器 × 多日志目录 多对多勾选**：每个服务器下面展开自己的日志目录 checkbox
- **#1 文件名点击预览**（新窗口 + modal 双模式）：后端 `POST /api/files/preview`；编码归一；二进制检测；8 个新 case
- **#2 常用目录**：按 `(system, server)` 分组的本地收藏，localStorage 持久化
- **#17 文件名模糊搜索**：子串 / glob，实时计数
- **#14 Tail 从日志目录的文件列表选择**：「📺 内嵌 Tail」「↗ 新窗口 Tail」按钮
- **#16 下载完成通知 + 路径跳转**：右上角浮动通知栈
- **#18 下载到指定本地目录**：`target_dir` 字段 + `validateTargetDir` 校验
- **#13 日志助手页面整理**：顶部「4 步走」说明卡

### v0.4 — 稳定性 + SSH 兼容性修复

- **OpenSSH 6.2p2 / 老 sshd SSH 兼容性加固**：`x/crypto` 升到 v0.31.0；3 套 SSH profile 自动 fallback；`keyboard-interactive` 认证；握手 deadline 与 ctx 分离；`logs/ssh_traffic.log` 区分 C→S / S→C 方向
- **SSE 长连接不被 120s 强制断开**：`http.Server.WriteTimeout` 设成 0
- **Windows zip 打包不再因反斜杠误判失败**：`filepath.Abs + os.Open + f.Stat` 校验
- **下载进度 SSE 中文 / Unicode 文件名输出合法 UTF-8**：`encoding/json.Marshal` 替代手写
- **safeWriter 超过 8MB 后只追加一次 truncated marker**
- **构建脚本容错**：缺 `config.yaml` 但有 `config.yaml.production.example` 仍能打包
- **SSH Dial 超时统一常量**：`sshDialOuterTimeout` (45s) + `sshAttemptTimeout` (10s)
- **SFTP 下载支持取消打断**：`runFilesDownloadTask` 在 ctx 取消时主动关闭 SFTP / SSH
- **任意路径下载前 Stat 拒绝目录**、**同名不再覆盖**（本地加 idx 前缀）
- **SSH Session 显式 Close**
- **文件浏览器配置开关**：`app.enable_free_file_browser: false` 一键关停
- **下载历史不限 .log/.zip**

### v0.3 — 文件浏览器（任意路径下载）

- 左侧菜单「文件下载」，按 SSH 账号权限浏览任意目录
- 多文件支持打包 zip；保留审计 + 进度条 + 取消
- WebSphere 日志助手 · 指定文件下载：勾选 + 多文件 zip

### v0.2 — 系统配置可视化编辑器 + 多服务器并发

- 在「系统配置」页直接增删改业务系统 / 服务器 / 日志目录
- 多服务器并行搜索（1–16 路）
- WebSphere 日志助手：测试连接、列出文件、下载最新日志、关键词搜索、查看上下文

### v0.1 — MVP

- 配置文件读取 + WebSphere 日志助手 + 报文格式化（JSON / XML）
- 本地审计日志（不写密码）

---

## 📄 License

Internal use only. Not for public distribution.
