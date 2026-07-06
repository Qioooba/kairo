# SSH + SFTP 合并视图 设计方案（v0.11 起 · FinalShell 布局）

> 目标：在 Kairo 内新增「**SSH 终端 + SFTP 文件浏览器**」合并菜单，
> 布局对标 **FinalShell**（上：终端，下：文件浏览器），连接复用同一 SSH 长连接，
> **配置文件 / keyring 已存密码时，前端不弹输入框**，自动建立会话。

---

## 1. 核心结论（一句话）

**前端纵向 flex 双栏 + 后端共享 sshshell.Session 上的 SFTP 子系统**
（`ssh.Client` 复用 + lazy open `sftp.Client`），web 文件浏览复用
`sftpclient.NewAuto`（含 shell 兜底），**不引入新二进制依赖、不引入新前端框架**。

---

## 2. 布局选型对比

| 维度 | 方案 A（MobaXterm） | **方案 B（FinalShell）** ✅ | 方案 C（抽屉） |
|---|---|---|---|
| 形态 | 左 SFTP + 右 SSH | **上 SSH + 下 SFTP** | SSH 全屏，按钮唤出 |
| 宽屏 | 优秀 | 优秀 | 优秀 |
| 笔记本 13 寸 | 左右都憋屈 | 终端视野宽 | 完美 |
| 写长命令 / tail -f | 一般 | **最佳**（终端纵向空间足） | 完美 |
| 浏览深层目录 | 高度宽 | 高度受限 | 一般 |
| 心智模型 | 运维主流 | **国服运维主流** | 偏离用户预期 |

**选 B**：用户明确指定；FinalShell 是国内运维的事实标准（vs MobaXterm 主战场是海外）。
后续想换 A 只改 CSS 的 `flex-direction`，代码零侵入。

---

## 3. 整体架构

```
┌─────────────────────────────────────────────────────────┐
│  Topbar  [系统/服务器 v] [连接状态●] [断开] [设置]      │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌─ Shell Panel (height: 60%) ─────────────────────┐   │
│  │  [📋 清屏] [↔ 重连] [📐 适应] [⚙ 终端设置]      │   │
│  │  ┌────────────────────────────────────────────┐ │   │
│  │  │ ops@server-01:/etc$ cd /var/log           │ │   │
│  │  │ ops@server-01:/var/log$ ls                │ │   │
│  │  │ syslog  nginx/  auth.log                  │ │   │
│  │  │ ops@server-01:/var/log$ █                 │ │   │
│  │  └────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────┘   │
│  ════════ 拖拽分隔条（记住位置，localStorage） ════════   │
│  ┌─ SFTP Panel (height: 40%) ──────────────────────┐   │
│  │  📁 /var/log          [⬆刷新] [⬇下载] [📤上传]  │   │
│  │  ┌────────┬────────┬───────────────────────┐   │   │
│  │  │ 名称   │ 大小   │ 修改时间              │   │   │
│  │  ├────────┼────────┼───────────────────────┤   │   │
│  │  │ 📁 ..  │  --    │ --                    │   │   │
│  │  │ 📁 ng… │ 4.0K   │ 2026-07-04 02:11      │   │   │
│  │  │ 📄 sy… │ 128M   │ 2026-07-04 01:55      │   │   │
│  │  │ 📄 au… │ 2.3M   │ 2026-07-04 01:50      │   │   │
│  │  └────────┴────────┴───────────────────────┘   │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

**核心交互点**：

| 动作 | 行为 |
|---|---|
| 终端 `cd /xxx` | **follow folder** 开关开 → SFTP 自动跳；关 → 不动 |
| SFTP 双击目录 | SFTP 进入该目录，**不联动**终端（避免误操作） |
| SFTP 单击文件 | 选中（多选用 Ctrl/Shift） |
| SFTP 双击文件 | 文本文件走 Monaco 预览；二进制走下载 |
| 拖拽文件到 SFTP 区域 | 上传（v1.2，v1 只做按钮） |
| 终端 resize | ws 发 WindowChange → 远端 SIGWINCH |
| 拖拽分隔条 | 改高度，比例持久化到 localStorage |

---

## 4. 连接复用（**最关键的设计点**）

### 4.1 现状（必须先看明白）

```
现在：两个独立入口，两次连接
─────────────────────────────
打开 SSH 终端页：
  前端 ws://.../api/ssh/shell/ws?system=X&server=Y
  → 后端 sshshell.Manager.Dial() → ssh.Client (短连/长连)
  → 前端 xterm ↔ ws ↔ ssh.Client.Shell

打开文件下载页：
  前端 POST /api/files/list {system, server, ...}
  → 后端 resolveCreds() → sshclient.Dial()  ← **又 dial 一次**
  → sftpclient.NewAuto(cli.RawConn())
  → 读 ReadDir() → 关掉（用完即丢）
```

**两个问题**：
1. 用户配过的密码 / keyring 里的密码，**SSH 终端页能自动登录**（handlers_ssh_shell.go 里 ws 不收 password，全靠 resolveCreds），但**文件下载页也得 resolveCreds 一次**——这个能力其实已经在了，只是 UI 上 files.js 还停留在「先填连接表单再操作」的旧范式。
2. 同一台服务器开两个 SSH 进程，**纯浪费**（虽然内网场景不重要，但 Moba/FinalShell 用户已经习惯「一个会话走天下」）。

### 4.2 复用策略：**一个 ws session 兜底所有 SFTP 操作**

```
合并视图：单一 SSH 长连接，SFTP 作为子系统挂在 ssh.Client 上
─────────────────────────────────────────────────────────
前端打开 /ssh-sftp.html?system=X&server=Y
  → 建立 ws://.../api/ssh/shell/ws?system=X&server=Y
  → 后端 sshshell.Manager.Dial() → ssh.Client (s1)
  → 拿到 sessionId=s1，缓存 (ssh.Client, expiresAt, writeMu)

前端要列文件：
  → POST /api/sftp/list {sessionId: "s1", path: "/var/log"}
  → 后端从 sshshell.Manager 拿 s1.RawConn() → sftp.NewClient() (子客户端，懒建)
  → ReadDir → 返回 → sftp 客户端缓存复用（不每次重建）

前端要下载文件：
  → POST /api/sftp/download {sessionId: "s1", path: "..."}
  → 后端同上 → 流式写回 HTTP 响应

前端关掉页面：
  → ws 断开 → sshshell.Manager.CloseSession("s1") → sftp 子客户端 Close + ssh.Client Close
```

**优势**：
- 一次登录，密码只用一次（即使是配置文件写的，也只 dial 一次）
- SFTP 操作几乎零延迟（子系统握手开销只一次）
- 跟 Moba/FinalShell 心智一致：**一个 tab = 一个会话**

**保底**：如果 ws 还没起来（比如用户第一次输密码），SFTP 操作走旧的「独立 dial」路径（复用 `resolveCreds`），等 ws 起来后再切换。

---

## 5. 后端实现

### 5.1 sshshell.Manager 增 3 个 API

文件：`internal/sshshell/manager.go`

```go
// 已有：Dial / Close / Write / Resize / 等
// 新增：

// GetRawConn 返回底层 ssh.Client，sftp 子系统在此基础上挂载。
// 调用方拿到 conn 后不能 Close（生命周期归 Manager 管）。
// sessionId 不存在 → 返回 ErrSessionNotFound。
func (m *Manager) GetRawConn(sessionID string) (*ssh.Client, error)

// ResolveBySystemServer 把 sessionId 跟具体的 system|server 绑定校验。
func (m *Manager) ResolveBySystemServer(system, server string) (sessionID string, ok bool)

// 加一个 SetOnClose 回调：session 关掉时清掉绑定的 sftp 子客户端
```

### 5.2 新增 handlers_sftp.go

文件：`internal/httpserver/handlers_sftp.go`（新）

```go
// 路径：/api/sftp/list | /api/sftp/download | /api/sftp/preview |
//       /api/sftp/mkdir | /api/sftp/rename | /api/sftp/rm |
//       /api/sftp/upload

type sftpReq struct {
    SessionID string `json:"session_id"`  // 优先：复用 ws 会话
    System    string `json:"system"`       // 兜底：独立 dial
    Server    string `json:"server"`
    Username  string `json:"username"`
    Password  string `json:"password"`
    Path      string `json:"path"`
}

// resolveClient 核心 helper：
//   1. 有 session_id → sshshell.Manager.GetRawConn + lazy sftp.NewClient
//   2. 没 session_id → 走旧的 sshclient.Dial + sftpclient.NewAuto
//   3. session 不存在 → 返 410 Gone，前端提示「会话已断开，请重新连接」
```

### 5.3 handlers_ssh_shell.go 增 cwd 推送（v1.2）

文件：`internal/httpserver/handlers_ssh_shell.go`（改）

在 shell 帧循环里加 hook：

```go
// 优先用 OSC 7（VTE / iTerm2 / Windows Terminal 都支持）。
// 比轮询 `pwd` 更可靠，**不走 SSH 命令通道**，不污染用户终端。
// 协议：\x1b]7;file://hostname/path\x07
```

### 5.4 API 清单

| 端点 | 方法 | 说明 | 复用 vs 新增 |
|---|---|---|---|
| `/ssh-sftp.html` | GET | 合并视图页面 | 新增 |
| `/static/pages/ssh-sftp.js` | GET | 合并视图前端 | 新增 |
| `/api/ssh/shell/ws` | WS | **不变**，加 cwd 帧 | 改 |
| `/api/ssh/sftp/list` | POST | 列目录（Phase 1 独立 dial；Phase 2 支持 sessionId 复用 ws） | 新增（不替代 /api/files/list，向后兼容） |
| `/api/ssh/sftp/pwd` | POST | 取 SSH 终端当前工作目录（让 SFTP 跳过去） | 新增 |
| `/api/ssh/sftp/preview` | POST | 文本预览（含编码嗅探） | 新增（参考 /api/files/preview） |
| `/api/ssh/sftp/download` | POST | 多文件打包下载（zip≥2 触发） | 新增 |
| `/api/ssh/sftp/download/{id}/events` | GET(SSE) | 下载进度推送 | 复用 dlmanager.Session |
| `/api/ssh/sftp/download/{id}/cancel` | POST | 取消下载 | 复用 dlmanager.Session |
| `/api/credentials/save` | POST | **不变** | 复用 |
| `/api/files/list` 等 | POST | **不变**，独立页继续用 | 复用 |

> 路由命名说明：用 `/api/ssh/sftp/*` 而非 `/api/sftp/*`，避免未来与独立的 SFTP 模块命名空间冲突。
>
> v1.0 阶段实际只实现了 list / pwd / preview / download 四个端点，mkdir / rename / rm / upload / chmod 推迟到 v1.3。

### 5.5 数据结构

```go
// 会话 → sftp 子客户端映射（lazy + cache）
type sftpSessionCache struct {
    mu      sync.Mutex
    clients map[string]*sftp.Client  // sessionID → sftp client
}

func (c *sftpSessionCache) getOrCreate(sessionID string, sshCli *ssh.Client) (*sftp.Client, error) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if cli, ok := c.clients[sessionID]; ok {
        // ping 一下，断了重建
        if _, err := cli.Stat("."); err == nil {
            return cli, nil
        }
        _ = cli.Close()
        delete(c.clients, sessionID)
    }
    cli, err := sftp.NewClient(sshCli)
    if err != nil {
        return nil, err
    }
    c.clients[sessionID] = cli
    return cli, nil
}
```

---

## 6. 前端实现

### 6.1 文件结构

```
web/
├── ssh-sftp.html           # 新增：合并视图页面骨架
├── ssh.html                # 保留：独立 SSH 终端页（不删，给特殊场景用）
└── pages/
    ├── ssh-sftp.js         # 新增：合并视图主入口（组装 shell + sftp panel）
    ├── ssh-sftp-shell.js   # 新增：从 ssh.js 抽出 xterm 初始化逻辑（v1 阶段内联，v2 抽）
    ├── ssh-sftp-sftp.js    # 新增：路径栏 + 列表 + 操作（参考 files.js 重写为面板版）
    └── ssh.js              # 保留：独立 SSH 终端逻辑
```

**v1 阶段先不拆文件**：`ssh-sftp.js` 一个文件搞定，复用 `Kairo.core` / `Kairo.api` / `ssh.js` 暴露的 xterm helper（v1 暂时在 ssh.js 里 export 出 `Kairo.pages.ssh.createTerminal()`，v2 再拆）。

### 6.2 ssh-sftp.html 骨架（v1 关键结构）

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <link rel="stylesheet" href="/static/vendor/xterm/xterm.css" />
  <link rel="stylesheet" href="/static/style.css" />
  <style>
    body { grid-template-columns: 1fr; }   /* 隐藏侧边栏，全宽 */
    .main { display: flex; flex-direction: column; height: 100vh; padding: 0; }

    .ssh-sftp-layout {
      flex: 1; display: flex; flex-direction: column;
      min-height: 0;
    }
    .shell-panel { flex: 0 0 60%; display: flex; flex-direction: column;
                   min-height: 120px; border-bottom: 1px solid var(--line); }
    .sftp-panel  { flex: 1 1 40%; display: flex; flex-direction: column;
                   min-height: 100px; overflow: hidden; }

    .splitter { height: 6px; cursor: row-resize; background: var(--bg-2);
                border-top: 1px solid var(--line); border-bottom: 1px solid var(--line); }
    .splitter:hover { background: var(--primary); }

    .shell-toolbar { /* 跟 ssh.html 同款 */ }
    .shell-host { flex: 1; }
    .shell-term-wrap { flex: 1; min-height: 0; background: var(--bg); padding: 4px; }
    .shell-term-wrap .xterm { height: 100%; }

    .sftp-toolbar { display: flex; gap: 8px; padding: 6px 12px;
                    background: var(--bg-2); border-bottom: 1px solid var(--line); }
    .sftp-path { flex: 1; font-family: ui-monospace, monospace; }
    .sftp-follow { /* follow terminal folder checkbox */ }

    .sftp-table { flex: 1; overflow: auto; }
    .sftp-table table { width: 100%; border-collapse: collapse; }
    .sftp-table th { position: sticky; top: 0; background: var(--bg-2); }
    .sftp-table tr:hover { background: var(--bg-hover); }
    .sftp-table tr.selected { background: var(--bg-sel); }

    .pwd-overlay { /* 复用 ssh.html 的密码弹层 */ }
  </style>
</head>
<body>
  <div class="main">
    <header class="topbar">
      <span>SSH + SFTP · <span id="host-label">未连接</span></span>
      <span class="spacer"></span>
      <span id="conn-status" class="badge">未连接</span>
      <button id="btn-disconnect">断开</button>
    </header>

    <div class="ssh-sftp-layout">
      <section class="shell-panel">
        <div class="shell-toolbar">…</div>
        <div class="shell-term-wrap"><div id="terminal"></div></div>
      </section>

      <div class="splitter" id="splitter"></div>

      <section class="sftp-panel">
        <div class="sftp-toolbar">
          <span class="sftp-path" id="sftp-path">/</span>
          <button id="btn-refresh">刷新</button>
          <button id="btn-upload">上传</button>
          <button id="btn-download">下载</button>
          <button id="btn-mkdir">建目录</button>
          <label><input type="checkbox" id="chk-follow"> 跟随终端</label>
        </div>
        <div class="sftp-table" id="sftp-table">…</div>
      </section>
    </div>
  </div>

  <div class="pwd-overlay" id="pwd-overlay" hidden>…</div>

  <script src="/static/vendor/xterm/xterm.js"></script>
  <script src="/static/vendor/xterm-addon-fit/lib/addon-fit.js"></script>
  <script src="/static/app.js"></script>
  <script src="/static/pages/ssh.js"></script>
  <script src="/static/pages/ssh-sftp.js"></script>
</body>
</html>
```

### 6.3 ssh-sftp.js 关键状态

```js
const state = {
  system: null,
  server: null,
  sessionId: null,        // ws 起来后才有
  ws: null,
  term: null,
  fitAddon: null,
  sftpCwd: '/',           // 当前 SFTP 路径
  followTerminal: false,  // 跟随 cd
  selected: new Set(),    // 多选文件名
  splitRatio: 0.6,        // 上下比例，存 localStorage
};
```

### 6.4 关键交互流程

**启动**：
```
1. 读 URL ?system=X&server=Y
2. 实例化 xterm + fitAddon，mount 到 #terminal
3. ws = new WebSocket(`/api/ssh/shell/ws?system=${X}&server=${Y}`)
4. ws.onopen → 连上后端开始 resolveCreds
5. ws.onmessage:
     type=ready     → 拿到 sessionId，写到 state.sessionId，调 listDir(state.sftpCwd)
     type=cwd       → {path:"/var/log"} → 若 followTerminal=true 则 listDir(path)
     type=error     → {reason:"no_password"} → 弹密码输入框
                     → POST /api/credentials/save → 重连 ws
     (data 帧)      → term.write(data)
6. term.onData(data) → ws.send({type:"input", data})
```

**目录同步（OSC 7，v1.2）**：
```
1. xterm.js 抓 OSC 7 序列：\x1b]7;file://hostname/path\x07
2. 解出 path，state.followTerminal 为 true → listDir(path)
3. SFTP 端 listDir 拿新目录后，term.write("\x1b]7;file://...新path\x07") 写回终端
   （让终端 prompt 也能反映当前路径 —— 这是细节，v1.2 再做）
```

**SFTP 操作（多选下载走 SSE）**：
```
1. 选中文件 → 加入 state.selected
2. 点下载 → POST /api/sftp/download {session_id, paths: [...]}  → 拿 download_id
3. new EventSource(`/api/files/download/${id}/events`)  → 复用现有 SSE 进度机制
4. 完成后弹出「打开文件夹 / 关闭」提示
```

**Splitter 拖拽**：
```
1. mousedown on #splitter → 进入 drag 模式
2. mousemove → 算新 ratio = mouseY / containerHeight
3. 应用到 .shell-panel 的 flex-basis
4. mouseup → localStorage.setItem('kairo_ssh_sftp_split', ratio)
5. 触发 fitAddon.fit() 让 xterm 重排 cols/rows
```

---

## 7. 凭证流程（**配置文件 / keyring 自动登录**）

`resolveCreds` 已经在 httpserver 里统一，**前端 ws 不传 password**，走三级降级：

```
1. 请求体 password（合并视图不用）           → 0.0ms
2. 系统 keyring（macOS Keychain / DPAPI）   → ~5ms
3. config.yaml 里 srvCfg.Password 字段     → 0.0ms
4. 都空 → error 帧，前端弹一次性输入框
   → 用户输 → POST /api/credentials/save 落 keyring
   → 自动重连 ws
```

**用户说的「配置文件里填过了」= 走第 3 级**，合并视图打开直接进，**0 弹窗**。

keyring / file / disabled 三档模式行为一致，由 `internal/credentials` 统一管，**合并视图零额外代码**。

---

## 8. follow terminal folder 实现细节（v1.2）

### 8.1 协议选择

| 方式 | 优点 | 缺点 |
|---|---|---|
| 后端轮询 `pwd` | 简单可靠 | 500ms 一次远端命令，污染输出 |
| 前端解析 `cd` 命令 | 不污染 | 用户用 `pushd` / 脚本切路径抓不到 |
| **OSC 7** ✅ | 标准协议、终端原生、零污染 | 需要 xterm.js hook |

### 8.2 xterm.js hook 实现

```js
// OSC 7 = \x1b]7;...\x07（BEL 终止）或 \x1b]7;...\x1b\\（ST 终止）
const OSC7_RE = /\x1b\]7;file:\/\/[^\x07\x1b]*(\x07|\x1b\\)/g;

term.onData(data => { /* 正常转发到 ws */ });
const origWrite = term.write.bind(term);
term.write = function (data, cb) {
  if (state.followTerminal && typeof data === 'string') {
    const m = data.match(OSC7_RE);
    if (m) {
      // 解出 path，触发 SFTP 跳转
      const u = new URL(m[0].slice(4, -1));  // 去掉 \x1b]7; 和终止符
      if (u.protocol === 'file:') {
        listDir(decodeURIComponent(u.pathname));
      }
    }
  }
  return origWrite(data, cb);
};
```

### 8.3 后端配合

`/etc/profile` / `~/.bashrc` 默认不带 OSC 7 输出。需要登录 shell 启动时 echo 一次：
- 方案 A：在 ws 起来后，前端调 `term.write('\x1b]7;file://' + encodeURI('/home/'+user) + '\x07')`，**只初始一次**，不依赖远端配置
- 方案 B：让 sshshell 在 ws 握手成功后，往 SSH session 注入一段 `PROMPT_COMMAND`（类似 bash 的 `PROMPT_COMMAND='printf "\033]7;file://$HOSTNAME$PWD\007"'`）—— 入侵性强，**不推荐**

**选 A**。

---

## 9. 分阶段实施路线

按「先单点跑通再加新东西」的节奏，**每阶段独立可发布、可回退**。

### v1.0 —— 合并视图 + 独立连接（**最快出活**）

**目标**：能在一个页面里看到 SSH 终端 + SFTP 列表，但两边走各自的连接。

| 模块 | 工作量 | 关键改动 |
|---|---|---|
| 前端骨架 | 0.5d | ssh-sftp.html + ssh-sftp.js |
| 复用现有 ssh.js | 0 | export `createTerminal()` |
| 复用现有 files.js 的列表渲染 | 0.5d | 抽出 panel 组件 |
| 后端 `/api/sftp/*` 系列 | 1d | 复用 resolveCreds + sftpclient，独立 dial |
| Splitter 拖拽 | 0.2d | 纯前端 |
| 主题适配 | 0.2d | 复用现有 CSS 变量 |

**测试**：playwright 模拟整流程（点开页面 → 输入密码 → 列表渲染 → 终端输命令 → 拖分隔条）。

### v1.1 —— 连接复用

| 模块 | 工作量 | 关键改动 |
|---|---|---|
| sshshell.Manager.GetRawConn | 0.3d | 加方法 + 测试 |
| sftp 子客户端缓存 | 0.3d | sftpSessionCache |
| `/api/sftp/*` 支持 session_id | 0.5d | 改 handler 优先走 session |
| 前端：先连 ws 拿 sessionId，SFTP 走 session | 0.3d | state 加 sessionId |

**测试**：密码只输一次、ws 断后 SFTP 报 410 提示重连、并发 5 个 tab 用同一会话不打架。

### v1.2 —— follow terminal folder（OSC 7）

| 模块 | 工作量 | 关键改动 |
|---|---|---|
| 前端 hook OSC 7 | 0.3d | xterm.write 拦截 |
| 初始路径 OSC 7 | 0.1d | ws 起来后写一次 |
| 「跟随终端」checkbox + localStorage 记忆 | 0.2d | 偏好持久化 |
| 双击 SFTP 目录反向更新终端 prompt | 0.3d | 后端写 OSC 7 回去（可选） |

### v1.3 —— 文件操作补全

mkdir / rename / rm / upload / chmod 五个端点 + 前端按钮。**纯增量**，不影响 v1.0/1.1。

---

## 10. 风险与取舍

### 10.1 已识别风险

| 风险 | 等级 | 应对 |
|---|---|---|
| sshshell.Manager 当前是「按需创建、用完即关」 | 中 | v1.1 改成长连接 + TTL 续期；详情见 10.2 |
| ws 断了 SFTP 跟着断，用户在 SFTP 操作中 | 中 | 410 Gone + 前端 toast「连接已断开」+ 「重新连接」按钮 |
| sftp.NewClient 在 ssh.Client.Close() 后还持有句柄 | 低 | sftpSessionCache 加 ping 检查，断了重建 |
| 远端 Linux 默认无 OSC 7 输出 | 低 | v1.2 用前端写一次兜底 |
| 大量文件目录渲染卡顿 | 低 | v1 先全量，>1000 行再加分页 / 虚拟滚动 |
| 中文文件名编码（远端 GBK vs UTF-8） | 中 | 复用 sftpclient 的编码嗅探逻辑（已有） |
| 大文件下载超时 | 中 | /api/sftp/download 走 chunked，前端 EventSource 进度复用 |
| 浏览器多 tab 同时操作同一会话 | 低 | session 加 writeMu，命令串行化；SFTP 操作走单独子锁 |

### 10.2 sshshell.Manager 当前生命周期需复核

我看到 manager.go 注释里写「不接受 password、靠 resolveCreds」，但**连接生命周期**是不是「按需创建-关闭」还是要写具体确认。在 v1.1 开工前必须先确认：

- 当前 Manager 是否已经做了「同一 system|server 复用连接」的逻辑？
- TTL 多长？空闲多久关？
- 多个 ws 同时连同一台服务器时怎么处理？

这个不在本设计范围，但**v1.1 实施前必须先 reply**：先读 manager.go + manager_test.go，看现状再决定改造方案。

### 10.3 取舍记录

| 取舍 | 选择 | 不选的原因 |
|---|---|---|
| 完整 Monaco 预览 | v1.2 再做 | 复用现有 files.js 的预览能力（POST /api/files/preview），v1.0 双击文本文件直接走现有端点 |
| 拖拽上传 | v1.3 再做 | multipart + 进度回调 + 取消逻辑复杂，独立成 1d 工作量 |
| 多服务器标签 | v1.x 不做 | 一台服务器一个 tab 已经够用，跟现有 ssh.html 风格一致 |
| Rz/Sz (zmodem) | 不做 | 内网运维场景 99% 用 scp/sftp，没人用 zmodem |
| X11 转发 | 不做 | 这是 MobaXterm 的桌面独门，运维不需要 |
| SSH 隧道管理 | 不做 | 跟 SSH 终端是平行功能，不在合并视图范围 |

---

## 11. 验收清单

v1.0 完成判定：
- [ ] 打开 `/ssh-sftp.html?system=X&server=Y`，无密码输入框弹出（假设 keyring 或 config 已有）
- [ ] 上方 xterm 正常显示 `$ █`，能输入命令、回显输出
- [ ] 下方 SFTP 默认显示用户家目录的列表
- [ ] 拖拽分隔条比例持久化
- [ ] 双击目录进入、双击文本文件弹出预览
- [ ] 多选 + 下载按钮触发下载，进度条走 SSE
- [ ] 主题切换（dark/light/green/hc/xianxia）下颜色统一
- [ ] 关页面后 ws 自动断、ssh 连接释放（看 manager 的 Close 路径）

v1.1 完成判定：
- [ ] 配置或 keyring 有密码时，**前后端都只 dial 一次**（看 sshshell.Manager 日志）
- [ ] ws 断后点 SFTP 刷新 → 弹「会话已断开，请重新连接」
- [ ] 重连后 SFTP 自动恢复

v1.2 完成判定：
- [ ] 终端 `cd /var/log` → SFTP 自动跳到 /var/log（开启跟随）
- [ ] 关闭跟随 → cd 不影响 SFTP
- [ ] 偏好持久化（关页面再开还是上次的选择）

---

## 12. 文件改动总览

**v1.0 实际改动**（与设计初稿有出入：未抽独立 ssh-sftp.html，改为在 ssh.js 内 toggle 面板）：

**新增**（3 个）：
- `docs/SSH-SFTP-INTEGRATED-DESIGN.md`
- `internal/httpserver/handlers_ssh_sftp.go`
- `web/pages/sftp-common.js`

**修改**（5 个）：
- `internal/httpserver/httpserver.go` — 注册 `/api/ssh/sftp/*` 路由
- `web/pages/ssh.js` — 新增 ~500 行 SFTP 面板逻辑（toggleFilesPanel / sftpList / sftpPreview / sftpDownload 等）
- `web/style.css` — 新增 ~220 行 SFTP 面板样式（FinalShell 风格上下分栏）
- `web/index.html` — 引入 `sftp-common.js`
- `web/preview.html` — 支持 `source=ssh-sftp` 走 `/api/ssh/sftp/preview`

**未做**（推迟到 v1.1+）：
- `internal/sshshell/manager.go` — `GetRawConn` 等连接复用方法（Phase 2）
- `internal/httpserver/handlers_ssh_shell.go` — ws 帧加 `cwd` 类型（v1.2 OSC 7）
- mkdir / rename / rm / upload / chmod 端点（v1.3）
- `handlers_ssh_sftp_test.go` — 单元测试（待补）

**不动**：
- `internal/sshclient/`、`internal/sftpclient/`、`internal/credentials/` —— **底层完全复用**
- `web/ssh.html`、`web/pages/files.js` —— 独立页继续保留，给特殊场景用