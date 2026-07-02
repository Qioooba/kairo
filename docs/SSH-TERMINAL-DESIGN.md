# SSH 终端菜单 — 设计方案（v0.10 起 · v0.11-rc1 文档同步）

> 目标：在 Kairo 内新增「SSH 终端」菜单，UI 风格向 Xshell 看齐
> （左：服务器列表，右：交互式终端），复用现有 SSH / 凭据 / 配置 / 主题栈
> 不引入新的二进制依赖。

---

## 1. 核心结论（一句话）

**前端 xterm.js + 后端 gorilla/websocket + ssh.Client.RequestPty/Shell/WindowChange**
（标准事实方案），WebSocket 全双工，**复用**现有 `sshclient.Dial` 的 3 套 compat
profile（6.2p2 / AIX / 老堡垒机天然兼容）。

---

## 2. 为什么是这个组合

| 维度 | 候选 | 选定 | 理由 |
| --- | --- | --- | --- |
| 前端渲染 | xterm.js 5.x / 自写 DOM | **xterm.js** | VS Code / Hyper / Theia 同款；ANSI / 颜色 / 光标 / 选区 / 复制粘贴现成；addon 生态完整 |
| 传输 | WebSocket / SSE / HTTP 轮询 | **WebSocket** | 终端是双向流，SSE 单向不可行；轮询延迟感人；xterm.js 的 `AttachAddon` 原生接 ws |
| WebSocket 库 | gorilla/websocket / gobwas/ws / nhooyr.io/websocket | **gorilla/websocket** | 生产环境事实标准；同公司维护 `gorilla/mux` 不需要；`CheckOrigin` 与本项目 `allowLocalOrigin` 思路一致 |
| SSH 层 | 复用 `internal/sshclient` / 单独新包 | **复用 + 增量** | 已有 3 套 compat profile（`auto` / `modern` / `compat` / `no-ecdh` / `legacy`），6.2p2 / AIX 自动 fallback；新增 `Shell` / `WindowChange` 即可 |
| 凭据 | 复用 `internal/credentials` | **复用** | keyring / file AES-256-GCM / disabled 三档已有；SSH test 端点已有 `resolveCreds` helper 可直接调用 |
| 会话池 | 复用 `tailmgr` / 新建 `sshshell.Manager` | **新建 `sshshell`** | tailmgr 是 SSE 单向流（行回调），结构跟 ws 双向转发差异较大；参考其设计但不复用 |

**前置依赖**（需 `go mod vendor`）：

```
github.com/gorilla/websocket v1.5.x
```

Web 前端走 `web/vendor/` 静态嵌入（已有 vendor 目录 + go:embed）：

```
xterm@5.5.0         → xterm.js + xterm.css
xterm-addon-fit     → 自适应 rows/cols
xterm-addon-webgl   → 大输出性能（默认开 Canvas，WebGL 可选）
xterm-addon-search  → 终端内搜索（Ctrl+Shift+F，对标 Xshell）
xterm-addon-web-links → 终端内 URL / IP 可点
```

> 体积评估：xterm.js core ~120KB + 4 addon ~50KB = ~170KB（gzip 后 ~60KB）。
> 比 diff2html（已有 ~250KB）小，go:embed 单 exe 无压力。

---

## 3. 整体架构

```
┌──────────────────────────────────────────────────────────────┐
│  Web Browser                                                 │
│  ┌────────┬─────────────────────────────────────────────┐    │
│  │ 主机   │  [Tab] prod-node-1 ×  prod-node-2 × ...      │    │
│  │ 列表   │  ┌─────────────────────────────────────┐    │    │
│  │        │  │                                     │    │    │
│  │ 🟢 信贷 │  │           xterm.js 渲染区          │    │    │
│  │   prod │  │                                     │    │    │
│  │   -1   │  │                                     │    │    │
│  │   -2   │  │                                     │    │    │
│  │ 🟡 信用卡│  └─────────────────────────────────────┘    │    │
│  │   ...  │  [重连] [清屏] [搜索] [↗新窗口] [×关闭]        │    │
│  └────────┴─────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
                          │ WebSocket (binary + JSON 控制帧)
                          ▼
┌──────────────────────────────────────────────────────────────┐
│  HTTP Server                                                 │
│  GET /api/ssh/shell/ws?system=&server=&cols=&rows=           │
│      ↓ upgrader.Upgrade (CheckOrigin = 同源白名单)            │
│  ┌──────────────────────────────────────────────────────┐    │
│  │ sshshell.Handler                                     │    │
│  │  ① auth token 校验（与现有 API 一致）                 │    │
│  │  ② config 快照取 server                              │    │
│  │  ③ credentials 取 password                           │    │
│  │  ④ sshclient.Dial (走 3 套 compat profile)          │    │
│  │  ⑤ Session.RequestPty("xterm-256color", rows, cols) │    │
│  │  ⑥ Session.Shell()                                   │    │
│  │  ⑦ 双 goroutine：                                    │    │
│  │     - SSH stdout/stderr → ws binary → 前端           │    │
│  │     - ws binary → SSH stdin；JSON 控制帧 resize      │    │
│  │  ⑧ ctx cancel / 客户端断开 / WindowClose → 清理      │    │
│  └──────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
                  ┌────────────────┐
                  │ sshclient      │  ← 现有，零改动
                  │ ssh shell pool │
                  └────────────────┘
```

---

## 4. 协议设计（最小可用集）

WebSocket 双通道，**binary frame 是字节流**（最高效，符合 SSH 终端语义），
**JSON text frame 是控制指令**（resize、ping、客户端主动断开带 reason）。

### 4.1 客户端 → 服务端

| 类型 | payload | 说明 |
| --- | --- | --- |
| Binary | 任意字节 | 直接写 SSH stdin（按键、粘贴、命令） |
| Text | `{"type":"resize","cols":120,"rows":40}` | 前端 fitAddon.fit() 后触发，调 `Session.WindowChange` |
| Text | `{"type":"ping"}` | 30s 心跳（连接保活 / 反向探测） |
| Text | `{"type":"signal","signal":"SIGINT"\|"SIGTERM"}` | 工具栏「Ctrl+C」按钮，调 `Session.Signal` |
| Close | — | 客户端正常退出，触发 server 端优雅清理 |

### 4.2 服务端 → 客户端

| 类型 | payload | 说明 |
| --- | --- | --- |
| Binary | 任意字节 | SSH stdout/stderr 合并（合并是惯例，xterm 渲染时不分） |
| Text | `{"type":"exit","code":0,"reason":"normal"\|"error"\|"timeout"}` | shell 退出（关闭 tab 时也用这个） |
| Text | `{"type":"error","message":"认证失败"}` | 连接阶段失败（在 binary 流开启前） |
| Text | `{"type":"pong"}` | 心跳响应 |

> **编码透传**：服务端**不**做 GBK → UTF-8 转换。SSH server 端
> `LC_ALL=zh_CN.GBK` 环境下，shell 会自动按 locale 设置编码；我们直接
> 把原始字节透传到 xterm.js 的 `term.write()`，让浏览器 UTF-8 解码。
>
> 若发现老 WebSphere 默认 GBK 输出到浏览器乱码，再加 `encoding` 透传
> 参数（参考 `logquery` 的 `encoding: gbk` 模式），不影响 v1 设计。

---

## 5. UI 设计（向 Xshell 致敬）

### 5.1 布局

```
┌── sidebar ──┐ ┌── view (#/ssh) ─────────────────────────────────┐
│ Kairo       │ │ 顶部工具栏：[重连] [清屏] [复制] [搜索] [⛶新窗口] │
│             │ ├────────────────────────────────────────────────┤
│ 首页        │ │ ┌────── Tab strip ─────┐                       │
│ 日志助手    │ │ │ prod-node-1 × │ test × │ +                  │
│ 文件下载    │ │ └──────────────────────────────────────────────┤
│ 报文格式化  │ │                                                │
│ HTTP 测试   │ │       ┌──────────────────────┐                 │
│ SSH 终端 ◀  │ │       │                      │                 │
│ 常用命令    │ │       │      xterm.js        │                 │
│ 环境自检    │ │       │                      │                 │
│ 系统配置    │ │       └──────────────────────┘                 │
│ 下载历史    │ │ 状态条：[已连接] prod-node-1 · 14:23:01 · 0.3s │
└─────────────┘ └────────────────────────────────────────────────┘
```

> 顶 tab 用现有 `.tabbar` 风格（参考 websphere tab / tail.js）；
> xterm 容器用 `flex: 1 1 auto`，`background: var(--bg-0)`，跟四套主题联动。

### 5.2 左侧主机列表

- **分组**：按 `systems[].name` 折叠 / 展开（沿用 config.js 的三层视觉语言）
- **状态点**：🟢 已连接 / 🟡 正在连接 / ⚪ 未连接 / 🔴 错误
- **单击**：切换 tab（已打开则激活，未打开则新建）
- **双击**：新建 tab（即使已存在也复制一份）
- **右键菜单**：
  - 「连接」「在 xterm 里跑命令…」（v1 先不做）
  - 「复制 IP」「复制 host:port」「编辑服务器（跳转系统配置）」
- **顶部过滤框**：子串 / glob（复用 config.js 已有 fuzzy 逻辑）
- **状态持久**：打开的 tab + 选中服务器 + 状态点 → `localStorage['kairo:ssh:tabs']`

### 5.3 凭据流（无侵入）

- 进 tab 时先看 `credentials.Has(system, server, username)`
  - 有 → 直接连
  - 没有 → 弹一次性输入框（与 tail.html 一致；**不**落 localStorage，
    用户勾选「记住密码」才走 `/api/credentials/save` 落 keyring/file）
- 已开启的 tab，密码入内存（不持久），关 tab 即清

### 5.4 主题同步

xterm.js 通过 `theme` 配置接受颜色。`theme.js` 当前已暴露
`Kairo.theme.current()` 返回 'dark'/'light'/'green'/'hc'，新建
`Kairo.theme.terminalTheme(name)` 返回对应色板。切换主题时同步：

```js
window.Kairo.theme.applyTermTheme(term);
```

> 这是工具类不强绑；v1 简单做：dark/light 用现成配色，green/hc 复用 dark 配色 + 前景色调亮一档。

---

## 6. 后端模块拆分

### 6.1 新增 `internal/sshshell/` 包

```
internal/sshshell/
├── manager.go          # Session 池（类 tailmgr）
├── manager_test.go
├── session.go          # 一个 session：client + ssh.Session + 双向 goroutine
├── session_test.go
└── README.md
```

**Manager 接口**（参考 tailmgr 简化版）：

```go
type Manager struct { ... }
func New() *Manager
func (m *Manager) Open(ctx, srv, creds, rows, cols, encoding) (sid string, sess *Session, err error)
func (m *Manager) Get(sid string) (*Session, bool)
func (m *Manager) Close(sid string) error   // 优雅关闭：ctx cancel + Session.Close
func (m *Manager) CloseAll()              // 服务退出时
func (m *Manager) IdleGC()                // 类 tailmgr：N 分钟无活动回收
```

### 6.2 新增 `internal/sshclient` 方法

在现有 `sshclient.go` 末尾追加（**不破坏** 现有 Run/Stream）：

```go
// Shell 在 ssh.Client 上开一个交互式 shell。
//   - ptyType: "xterm-256color"（v1 写死）
//   - rows, cols: 前端 fitAddon 算出
//   - env: 额外环境变量（TERM/LC_ALL）
// 返回 Session（带 Stdout/Stdin Pipe + WindowChange + Signal + Close）
type ShellSession struct {
    Ssh      *ssh.Session
    Stdin    io.WriteCloser
    Stdout   io.Reader
    Stderr   io.Reader  // 多数实现 stdout/stderr 合并；保留字段
    Rows     int
    Cols     int
}
func (c *Client) Shell(ctx context.Context, term string, rows, cols int, env []string) (*ShellSession, error)
func (s *ShellSession) WindowChange(rows, cols int) error
func (s *ShellSession) Signal(sig ssh.Signal) error  // 工具栏 Ctrl+C 用
func (s *ShellSession) Close() error
```

> 这些都是 `golang.org/x/crypto/ssh` 原生 API，直接包装即可。
> 用现有 `Client.conn`（已走 3 套 compat profile），**不**重复握手。

### 6.3 新增 handler

```
internal/httpserver/handlers_ssh_shell.go
```

- `GET /api/ssh/shell/ws`：upgrader.Upgrade → 拿 system/server/cols/rows
  → 走 `resolveCreds` → `sshshell.Manager.Open` → 启动双向转发
- `CheckOrigin`：复用 `allowLocalOrigin`（内网工具默认信任同源 + 127.0.0.1）
- **不**单独 audit 每次按键；只在 start/end 写一条

### 6.4 路由注册

在 `httpserver.go` 的 switch 加一条（按顺序放在 `/api/ssh/test` 附近）：

```go
case path == "/api/ssh/shell/ws":
    s.handleSSHShellWS(w, r)
```

---

## 7. 前端模块拆分

```
web/
├── ssh.html              # 可选独立窗口（复用 tail.html 模板）
├── pages/
│   └── ssh.js            # 主逻辑：列表 + tab + xterm 生命周期
└── vendor/
    ├── xterm.css
    ├── xterm.js
    ├── xterm-addon-fit.js
    ├── xterm-addon-search.js
    ├── xterm-addon-web-links.js
    └── xterm-addon-webgl.js
```

`pages/ssh.js` 暴露 `Kairo.pages.ssh = { mount(root), unmount() }`，
跟现有 home/websphere 等页面同形。

`web/index.html` 加一行 nav：

```html
<a href="#/ssh" class="nav-item" data-route="ssh">SSH 终端</a>
```

（位置放在「文件下载」之后、「报文格式化」之前，自然分组。）

`core.js` 路由分发（如果有 hash → page map）加一条 `ssh → ssh` 即可。

---

## 8. 安全 & 审计

| 项 | 策略 |
| --- | --- |
| 跨域 | 沿用 `allowLocalOrigin`；ws `CheckOrigin` 同源 |
| 认证 | 走现有 `auth.EffectiveEnabled()` + token 校验；启用 auth 后必须带 token |
| Host key | 沿用现有 `Server.HostKeySHA256` + `AllowInsecureHostKey` 强校验（BE-005） |
| 凭据 | 复用 `credentials`；ws 连接**不**接收前端传的 password 字段，靠 server 端 resolveCreds（避免明文出现在 ws upgrade 帧） |
| 审计 | `audit.log` 写 `op=ssh.shell.start/end`，含 system/server/user/result/elapsed_sec；**不**记录命令内容（输入流太密） |
| RBAC | 仅 `role=admin` 可连（v1：直接放开 user；后续按需收紧） |
| 超时 | Manager 维护 `idleTimeout = 30min`（同 tail），`maxLifetime = 8h`（防泄漏） |
| 大小 | 单 session ctx deadline 8h；ws 写缓冲 64KB |

---

## 9. 复用现状清单（必须复用，不要造轮子）

| 现有 | 用在哪 |
| --- | --- |
| `internal/sshclient.Dial` + 3 套 compat profile | shell 连接底层 |
| `internal/credentials` (keyring/file) + `resolveCreds` helper | 凭据流 |
| `internal/audit.Logger` | shell.start/end 审计 |
| `internal/config.Manager` (COW) | server 配置快照 |
| `httpserver.allowLocalOrigin` | ws CheckOrigin |
| `httpserver.authUser` 上下文 | ws 升级后的 token 校验 |
| `web/style.css` 4 套主题 + CSS 变量 | 左侧主机列表 / 工具栏 / tab 视觉 |
| `web/core.js` 的 `el / toast / confirmDialog / $ / $$` | DOM |
| `web/theme.js` | xterm theme 同步 |
| `web/pages/*.js` IIFE 模式 + `Kairo.pages.xxx.mount` | 新页面注册 |
| `web/tail.html` + `web/tail.js` | 独立新窗口模式（按需） |
| `web/vendor/` + `go:embed web` | xterm 静态嵌入 |

---

## 10. 实施分阶段（按用户节奏：单点跑通 + 加新东西）

| 阶段 | 范围 | 验收 |
| --- | --- | --- |
| **M1 后端骨架** | `internal/sshclient.Shell` + `WindowChange`；ws handler；单连接可跑 | `wscat` 连 `/api/ssh/shell/ws` 跑 `ls` |
| **M2 前端最小化** | `#/ssh` 单页 + 单个 xterm + 单连接 | 浏览器能连、敲命令有输出 |
| **M3 主机列表 + 状态点** | 左栏按 systems 分组 + 状态点 + 单/双击 | UX 接近 Xshell |
| **M4 Tab + 关闭/重连** | 多 tab + tabbar + 关 tab 优雅断开 | 5 个 tab 长期稳定 |
| **M5 凭据流 + 主题** | keyring 自动 + 一次性输入 + 主题同步 | 体验闭环 |
| **M6 工具栏 + addon** | fit / search / web-links / webgl | Ctrl+Shift+F 可用 |
| **M7 独立窗口（可选）** | `ssh.html` 仿 `tail.html` | 满足习惯 |
| **M8 审计 + idle GC + 集成测试** | `internal/sshshell/manager_test.go` + fake sshd | 测试通过 |

> v1 交付标准：M1~M6。M7/M8 是补丁。

---

## 11. 风险与对策

| 风险 | 概率 | 对策 |
| --- | --- | --- |
| 老 sshd（6.2p2）请求 PTY 后无法 Shell | 中 | Shell() 失败时回退到 `Run("exec /bin/bash -l")` 单次命令模式（不推荐，但兜底） |
| GBK 输出浏览器乱码 | 中 | 加 `encoding` URL 参数；v1 不上，v2 补丁（与 logquery 同策略） |
| WebGL addon 在某些浏览器崩溃 | 低 | 默认 Canvas 渲染；WebGL 作为可选加载；try/catch 失败回退 |
| 长会话内存泄漏 | 中 | `maxLifetime = 8h` 强制回收 + `idleTimeout = 30min` |
| 大输出撑爆内存（如 `cat /var/log/messages`） | 中 | 服务端限制：写入 ws 前 `MaxBytesReader`-style 流控；前端 xterm.js 自带滚动缓冲 |
| 心跳被中间代理吃掉 | 中 | 30s ping/pong，write deadline 90s |
| 同时开 20+ tab 内存压力 | 低 | 单 tab 平均 5MB xterm 缓冲 + 1MB ssh 缓冲；服务端 Manager 限制最大 32 个会话 |
| host key 校验失败（升级时） | 中 | 沿用 BE-005：未配 HostKeySHA256 + AllowInsecureHostKey=false → 直接拒绝，提示去系统配置 |

---

## 12. 与现有菜单的关系

- **不**取代「WebSphere 日志」「文件下载」：那是白名单模式（受控命令）
- **是**它们的扩展：当你拿到 ssh shell 后，那两个页能做的事 shell 都能做
- **互补**：日常 90% 用日志助手（搜索/tail/下载），剩下 10% 临时排障用 shell
- **不**开放到任意命令下发到任意服务器（仍是用户**主动**连自己的机器）
  → 不违反 README 的「任意命令执行（设计为禁止）」约束

---

## 13. 验收清单（建议写到 `docs/MANUAL-CLICK-CASES.md`）

1. 单连接：浏览器打开 `#/ssh`，点 prod-node-1，shell 出现 `$ ` 提示
2. 命令：`ls /`、`cat /etc/os-release` 输出正确
3. 颜色：`ls --color=auto` 颜色正常
4. Vim：`vim /tmp/x` 进入 insert/退出，颜色正确，光标移动对
5. 中文：`echo 你好` 中文正常（utf-8 服务器）；老 WebSphere GBK 服务器 echo 中文乱码 → 走 encoding 路径
6. 多 tab：开 5 个 tab 同时在线，状态点正确
7. 关 tab：右上 × 关闭，服务端 audit 写 end
8. 刷新页面：恢复上次打开的 tab + 自动重连
9. 凭据：第一次弹输入框；勾选记住后下次自动连；keyring 不可用时正确提示
10. 异常：密码错 → 弹错误，状态点红；网络断 → 自动尝试 3 次重连，失败标红
11. 主题：切 dark/light/green/hc，xterm 配色同步，无闪烁
12. RBAC：启用 auth 后非 admin token 连 ws 直接 403
13. Host key：新加 server 没配 HostKeySHA256 → 拒绝连接，提示去配置
14. 性能：连续 `yes abc` 1 万行，浏览器不卡（WebGL addon + 限速 flush）
15. 超时：8 小时强制回收；30 分钟空闲回收

---

## 14. 时间估算（个人体感）

| 阶段 | 估时 |
| --- | --- |
| M1 后端骨架 | 2-3h（含 compat 兼容 + 错误分类） |
| M2 前端最小化 | 1h |
| M3 主机列表 | 1.5h |
| M4 Tab + 重连 | 2h |
| M5 凭据 + 主题 | 1.5h |
| M6 工具栏 + addon | 1.5h |
| M8 测试 + 审计 | 2h |
| **合计 v1** | **~12h** |

---

## 15. 决策项（需要你拍板）

| # | 选项 | 我的建议 |
| --- | --- | --- |
| 1 | v1 是否就上 WebGL addon | **否**，默认 Canvas，需要时按主题开关（dark + 大输出场景收益明显） |
| 2 | Tab 嵌入式 vs 独立新窗口 | **嵌入式为主**（贴合 Xshell 习惯），右上角"⛶新窗口"复用 tail.html |
| 3 | 凭据弹层位置 | 跟 tail 一致：**当前页面弹**，不跳转 |
| 4 | RBAC 默认 | **不限制**，user 角色可连；admin 可连任意 server（含 `AllowInsecureHostKey=true`） |
| 5 | 审计粒度 | **不**记录命令内容（性能 + 噪音）；仅 start/end；后续可加"开关式"记录 |
| 6 | GBK 编码支持 | v1 **不**做；v2 再加 encoding 透传 |
| 7 | 菜单顺序 | 放在「文件下载」之后、「报文格式化」之前 |

---

**下一步**：你拍板 §15 的 7 个决策项，确认 M1 范围，我就动手。