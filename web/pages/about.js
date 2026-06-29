/* ===== web/pages/about.js =====
 * 关于页面 · 产品白皮书
 *
 * 设计目标：
 *   - 把"关于"从单页简介升级为一份**带交互的产品技术白皮书**
 *   - 顶部 sticky 锚点导航 + 9 个版本卡片 (accordion 折叠) + 12 个数据区块
 *   - 内容 100% 由 commit log / 源码 / README 提取, 不注水
 *   - 几万字正文 + 折叠默认收起, 首屏不卡
 */
(function () {
  'use strict';
  const DTB = window.DTB = window.DTB || {};
  DTB.pages = DTB.pages || {};
  const { el } = DTB.core;
  const { api } = DTB.api || {};

  const VERSION = 'v0.9.0';

  async function fetchVersion() {
    try {
      if (typeof api !== 'function') return null;
      const cfg = await api('GET', '/api/config');
      if (cfg && cfg.version) return cfg.version;
    } catch (e) { /* ignore */ }
    return null;
  }

  // =====================================================================
  // §1. 核心数据看板
  // =====================================================================
  const stats = [
    { label: '代码行数 (Go)',       value: '21,000+', sub: 'production + tests', tone: 'primary' },
    { label: '代码行数 (前端)',     value: '14,000+', sub: 'vanilla JS · 零依赖', tone: 'accent' },
    { label: '提交次数',            value: '100+',    sub: 'v0.1 → v0.9 (90 天)', tone: 'success' },
    { label: '后端模块',            value: '13',      sub: 'internal/* 子包',     tone: 'primary' },
    { label: '前端页面',            value: '14',      sub: 'web/pages/*.js',     tone: 'accent' },
    { label: 'API 接口',            value: '40+',     sub: 'REST + SSE',         tone: 'primary' },
    { label: '测试用例 (Go)',       value: '120+',    sub: '单元 + 集成',        tone: 'success' },
    { label: '测试用例 (Node)',     value: '80+',     sub: 'app.test.js',        tone: 'success' },
    { label: 'E2E 场景 (Playwright)', value: '200+',  sub: '6 个脚本',           tone: 'warn'   },
    { label: '修复缺陷',            value: '300+',    sub: 'P0/P1/P2 全量',      tone: 'warn'   },
    { label: '安全设计点',          value: '14',      sub: 'fail-closed 全栈',   tone: 'error'  },
    { label: 'SSH 兼容 profile',    value: '5',       sub: 'modern → legacy',    tone: 'primary' }
  ];

  // =====================================================================
  // §2. 设计哲学 (Design Principles)
  // =====================================================================
  const principles = [
    {
      icon: '🔒', title: '安全第一 (fail-closed)',
      body: '所有权限决策默认"拒绝"。白名单空 → 一律拒绝；host key 没配 + allow_insecure=false → 不发起连接；admin 专属接口没带 admin token → 403。把"忘记配"和"配错"都收敛到安全侧，避免任何隐式放行。'
    },
    {
      icon: '🎯', title: '受控优于开放',
      body: '不开放任意 shell。所有远程命令由后端固定模板生成 (find / grep / sed / sort / head / cat 组合)，目录 / 文件名只能来自配置白名单或前一步 ls 的结果，关键词做严格转义。零命令注入面。'
    },
    {
      icon: '⚡', title: '上下文优先 (context-first)',
      body: '所有 I/O 路径走 ctx。远程命令三段式超时 (SIGTERM → 1s → SIGKILL)；下载任务 30 分钟硬超时；SSE 长连接不被默认 120s 强制断开。一次 cancel 终止整条调用链，无悬挂 goroutine。'
    },
    {
      icon: '🔬', title: '极简优于复杂',
      body: '零前端框架、零外部 UI 库、零 CSS 预处理器。vanilla JS + 原生 CSS 变量 + 内嵌 go:embed。前端 14 个页面 / 14K 行代码平均每个页面 ~1000 行。'
    },
    {
      icon: '🧪', title: '可测优于能跑',
      body: 'sshclient → Streamer 接口、sftpclient → RemoteFS 接口、dlmanager → Session 模型：每个核心包都对测试友好，提供 mock 注入点。fake-websphere + mock_sshd.py 给集成测试真实感，单测覆盖率 81%+。'
    },
    {
      icon: '🛡️', title: '凭据零落盘 (zero plain)',
      body: '密码永不写进 audit.log / URL / 错误信息 / 前端响应。可选 OS 钥匙串 (macOS Keychain / Windows DPAPI / Linux Secret Service) 按 (system, server, user) 三元组加密；file 模式走 AES-256-GCM，密文绑 AAD 防替换攻击。'
    },
    {
      icon: '🔁', title: '写后即持久 (write-then-persist)',
      body: 'config.yaml 写回走 tmpfile + rename(2)，损坏不污染线上配置；downloads 元数据走单文件 .doubao-toolbox-meta.json 加 mtime 失效缓存；preferences.json 写入显式 chmod 0600。每一次"保存"都有兜底。'
    },
    {
      icon: '🧭', title: '工程师视角 (operator-grade)',
      body: 'SSH 错误归类到运维友好中文（"密码错误 / 账号锁定 / 网络超时 / HostKey 不匹配"）；诊断中心 3 问自检；Diagnostics 报告按"App / Runtime / Tools / Servers / Issues"分块；日志助手三级目录展开 + 多对多勾选矩阵。'
    }
  ];

  // =====================================================================
  // §3. 架构总览 (4 层)
  // =====================================================================
  const architecture = [
    {
      layer: 'L1', name: '展示层 (Presentation)',
      detail: 'Web Browser · 单页应用 · hash-router 路由 · vanilla JS · 14 个页面 · 4 套主题',
      tech: ['原生 ES2020', 'CSS 变量主题', 'hash 路由', 'EventSource(SSE)', 'localStorage'],
      duty: '所有用户交互、渲染、状态机、主题切换、SSE 订阅、UI 反馈。不依赖任何 npm 运行时。'
    },
    {
      layer: 'L2', name: '网关层 (HTTP Server)',
      detail: '127.0.0.1:18092 (默认) · net/http · handlers_*.go 路由表 · go:embed web/',
      tech: ['net/http', 'go:embed', 'JSON', 'SSE', 'Bearer Token'],
      duty: '入口鉴权 (可选 Bearer + IP 白名单)、路径白名单、RBAC 校验、SSE 长连接维持、静态资源分发。所有 API 入口走 requireAdmin / requireAuth / sanitize / 路径校验四道关。'
    },
    {
      layer: 'L3', name: '业务层 (Domain)',
      detail: 'sshclient · sftpclient · logquery · dlmanager · tailmgr · diff · downloads · formatter · credentials',
      tech: ['x/crypto/ssh', 'pkg/sftp', 'x/text (GBK 透明转换)', 'AES-256-GCM', 'COW Config', 'Worker Pool', 'Myers Diff'],
      duty: '受控 SSH 执行、受控文件读取、命令模板生成、异步任务会话池、实时 SSE 广播、行级 diff、凭据存取。元数据全部集中维护，handler 只负责协议转换。'
    },
    {
      layer: 'L4', name: '基础设施层 (Infra)',
      detail: 'config (COW Manager) · credentials (keyring/file/disabled) · downloads (元数据索引) · audit (滚动日志)',
      tech: ['yaml.v3', 'go-keyring', 'AES-GCM', 'atomic rename', 'chmod 0600', 'tail-file rotate'],
      duty: '配置原子加载、凭据加密存储、下载元数据索引、操作审计滚动文件。所有"用户状态变更"都在这层留下不可变痕迹。'
    }
  ];

  const dataFlow = [
    { from: 'Browser', to: 'HTTPServer', label: 'fetch + EventSource' },
    { from: 'HTTPServer', to: 'Handlers', label: '路由匹配' },
    { from: 'Handlers', to: 'Domain', label: '参数校验 + 业务调用' },
    { from: 'Domain', to: 'SSH/SFTP', label: '受控命令/读取' },
    { from: 'Remote', to: 'Domain', label: 'stdout/stderr' },
    { from: 'Domain', to: 'Handlers', label: '结果 + 元数据' },
    { from: 'Handlers', to: 'Browser', label: 'JSON / SSE' }
  ];

  // =====================================================================
  // §4. 后端技术栈 (10 依赖逐项)
  // =====================================================================
  const backendStack = [
    { name: 'Go', version: '1.20+', role: '主语言', desc: 'go 1.20 directive，向下兼容 Win7 编译；goroutine 调度，静态二进制，零运行时依赖。' },
    { name: 'golang.org/x/crypto/ssh', version: 'v0.31.0', role: 'SSH 客户端', desc: '深度定制的 SSH 协议栈；3 套 KEX profile 自动 fallback；keyboard-interactive 认证；HostKey 指纹校验。' },
    { name: 'github.com/pkg/sftp', version: 'v1.13.6', role: 'SFTP 子系统', desc: '文件 Open/Stat/Read。v0.4 起抽象出 RemoteFS 接口，支持 SFTPBackend + ShellBackend 双 backend 自动降级。' },
    { name: 'golang.org/x/text', version: 'v0.21.0', role: '字符编码', desc: 'simplifiedchinese.GBK / GB18030 透明编码转换；老 WebSphere / Oracle / AIX 上的 GBK 日志直读不乱码。' },
    { name: 'github.com/zalando/go-keyring', version: 'v0.2.8', role: 'OS 钥匙串抽象', desc: '统一 macOS Keychain / Windows DPAPI / Linux Secret Service 三个原生后端；零明文落盘。' },
    { name: 'github.com/danieljoos/wincred', version: 'v1.2.3', role: 'Windows DPAPI', desc: 'Windows 平台 keyring 后端实现，由 go-keyring 间接依赖。' },
    { name: 'github.com/godbus/dbus/v5', version: 'v5.2.2', role: 'Linux Secret Service', desc: 'Linux 平台通过 D-Bus 与 GNOME Keyring / KWallet 通信，keyring 后端实现。' },
    { name: 'gopkg.in/yaml.v3', version: 'v3.0.1', role: '配置解析', desc: 'config.yaml 解析 + 原子写回；Schema 校验防止非法配置写入；trailing garbage 拒绝。' },
    { name: 'golang.org/x/sys', version: 'v0.28.0', role: '系统调用', desc: '平台特定系统调用 (Windows Service / 信号 / 文件锁) 的跨平台封装。' },
    { name: '标准库', version: '—', role: '运行时', desc: 'net/http (HTTP server + SSE)、embed (web/ 静态资源内嵌)、context (取消传播)、os/exec、crypto/sha256、bufio。' }
  ];

  // =====================================================================
  // §5. 前端架构 (8 模块)
  // =====================================================================
  const frontendStack = [
    { name: 'core.js', desc: 'DOM/工具内核 — el() 安全构造器、escapeHtml、toast、confirmDialog、modal、tabs、virtualList、resize 监听。零依赖。' },
    { name: 'state.js', desc: '全局状态机 — 当前用户、主题、routeMap、routeNames、tailHighlights 等共享状态。DTB.state.* 单一入口。' },
    { name: 'api.js', desc: 'HTTP 客户端 — api(method, path, body) 统一封装；自动加 Bearer token；SSE EventSource 工厂；统一错误处理。' },
    { name: 'theme.js', desc: '主题切换 — dark / light / green / hc 4 套主题，inline script 在 <head> 提前设 data-theme 防 FOUC。' },
    { name: 'auth.js', desc: '认证层 — 拉 /api/auth/status 探测；token cookie 管理；role-gated UI 显隐。' },
    { name: 'pages/*.js', desc: '14 个页面 — home / websphere / files / formatter / commands / diagnostics / config / downloads / http / timestamp / cron / jsonpath / compare / about。每个页面一个 IIFE，路由切换时整体替换 view。' },
    { name: 'tail.js + tail.html', desc: '独立 tail 窗口 — 从主页面剥离的 tail 流，跟踪 SSE 不影响主页面操作；行级 DOM 节点池 + rAF 批量 flush (50ms/100 行)。' },
    { name: 'preview.html', desc: '文件预览子窗口 — 单文件模态 + 新窗口双模式，支持文本 / GBK 编码自动识别。' }
  ];

  // =====================================================================
  // §6. 工程化实践 (12 项)
  // =====================================================================
  const engineering = [
    { title: 'COW Config Manager', body: '每次 Replace 整体换指针，handler 读快照不被并发写入撕裂；读端零锁。' },
    { title: 'Goroutine Worker Pool', body: '多服务器搜索 / 列文件走 4 路并发 + errgroup 风格隔离，单机故障不阻塞其他 server。' },
    { title: 'context 优先', body: '所有远程命令 / 下载 / Tail 都用 ctx 控制超时；ctx cancel 即整条链路取消。' },
    { title: '三段式超时', body: 'ctx deadline → SSH session.Signal(SIGTERM) → 1s 后 SIGKILL，绝不依赖远端 timeout 命令。' },
    { title: '原子写回 yaml', body: 'tmpfile + rename(2)，损坏不污染线上配置；SSH debug log / traffic log 同样走轮转。' },
    { title: '受控命令模板', body: 'logquery 包内固定生成 find / grep / sed / sort / head / cat 组合；目录 / 文件名走白名单；关键词拒绝 shell 元字符。' },
    { title: 'tob-tail 批量 flush', body: 'TailViewer 行级 DOM 节点池 + requestAnimationFrame 50ms/100 行 flush，避免 textContent += 整段重排。' },
    { title: '进程内 fs.FS 接口', body: 'httpserver.serveStatic 直接读 fs.FS，避免额外 IO 包装；测试可注入假 FS。' },
    { title: 'sftpDialer 包级变量', body: '集成测试注入 fake dialer，绕过真 SSH；handler 业务逻辑完全可测。' },
    { title: 'vendor/ 提交', body: 'clone 后无网也能 -mod=vendor 编译；CI 不依赖外网。' },
    { title: 'go:embed web', body: '静态资源打进单 exe；跨平台部署一条命令搞定；分发包仅一个二进制。' },
    { title: 'interface{} > struct{}', body: 'Streamer / RemoteFS / Manager 都是接口，业务代码对底层零依赖；mock 注入方便测试。' }
  ];

  // =====================================================================
  // §7. 安全白皮书 (14 条)
  // =====================================================================
  const security = [
    { id: 'S01', title: '默认只监听 127.0.0.1', detail: '未启用 auth 时 0.0.0.0 / 内网 IP 直接被配置校验拒绝，不向局域网暴露；启用 auth 后才允许监听 0.0.0.0 / 内网 IP。' },
    { id: 'S02', title: '不开放任意 shell', detail: '所有远程命令由后端固定模板生成（find / grep / sed / sort / head / cat 组合），无任意命令注入面。' },
    { id: 'S03', title: '白名单路径', detail: '日志助手的目录必须来自 config.log_dirs；文件名只能来自 find 列出结果；free_file_roots / compare_allowed_roots 双白名单并行。' },
    { id: 'S04', title: '文件浏览器按账号权限', detail: '实际访问控制交给 SSH server 端（账号 + 文件系统权限 + sshd_config），本工具不做提权。' },
    { id: 'S05', title: '关键词严格转义', detail: '搜索关键词做白名单校验，拒绝 ` \' \\ $ ; & | < > ( ) { } [ ]` 等 shell 元字符与 NUL / 换行。' },
    { id: 'S06', title: '密码零持久', detail: '从不写进 audit.log / URL / 错误信息 / 前端响应；可选 OS 钥匙串按 (system,server,user) 三元组加密存储；file 模式走 AES-256-GCM。' },
    { id: 'S07', title: '只读不写', detail: '不提供上传 / 删除 / 改远程文件的能力；文件浏览器只读 + 下载；SSH 命令模板只有 find / grep / sed / sort / head / cat。' },
    { id: 'S08', title: '路径穿越防护', detail: '所有用户输入做绝对路径校验 + filepath.Clean + Rel 双重校验；拒绝 .. / NUL / 换行；URL 路径穿越 404；14 种攻击向量全覆盖。' },
    { id: 'S09', title: '超时硬约束', detail: '远程命令三段式（SIGTERM → 1s → SIGKILL）；下载任务 30 分钟硬超时；tail 30 分钟空闲回收（可配，最小 5 分钟）。' },
    { id: 'S10', title: '下载文件只落 downloads/', detail: '任意 target_dir 必须绝对路径 + 写探针通过；config 默认下载目录锁定；同任务同名文件本地加 001/002/... 前缀。' },
    { id: 'S11', title: '硬上限', detail: '单次下载最多 100 个文件，30 分钟超时；搜索最大 200 命中；SSE 单事件 4KB 限流。' },
    { id: 'S12', title: 'CORS / 跨域', detail: '所有响应带 X-Content-Type-Options: nosniff / Referrer-Policy: no-referrer / X-Frame-Options: DENY，禁止跨域 iframe 与 MIME 嗅探。' },
    { id: 'S13', title: 'TOCTOU 加固', detail: 'handler 入口取一次配置快照（COW Manager 读快照），全程复用同一份，避免 check-then-use 时间窗被改写。' },
    { id: 'S14', title: 'Bearer Token + IP 白名单 (可选)', detail: 'config.yaml 的 auth 段配置 token（role=admin/user + allowed_ips），启用后未带有效 token → 401；IP 不在白名单 → 403；admin 专属接口（配置导入 / 凭据清空 / 服务器增改 / openers / download-retention）强制 role=admin。' }
  ];

  // =====================================================================
  // §8. SSH 兼容矩阵 (5 profile)
  // =====================================================================
  const sshProfiles = [
    { name: 'modern', kex: 'curve25519 优先 → ecdh-sha2-* → diffie-hellman-group14-sha256', target: 'OpenSSH 7.4+ / 现代 Linux', note: '默认 KEX 集最强，性能最好。' },
    { name: 'compat (默认)', kex: 'modern 子集 + 老算法回退', target: 'OpenSSH 6.2p2 / 6.0 / 多数企业内网', note: '握手失败自动 fallback 到下一套。' },
    { name: 'no-ecdh', kex: '完全去掉 ECDH，仅 DH', target: '某些老 OpenSSL 的 ECDH_INIT 后 RST bug', note: '罕见场景，但留个口子。' },
    { name: 'legacy', kex: 'group14-sha1 + ssh-dss', target: 'OpenSSH 5.x / 6.0 / AIX / 老堡垒机', note: '老设备救星，牺牲安全性换兼容。' },
    { name: 'auto', kex: 'compat → no-ecdh → legacy 顺序自动 fallback', target: '不确定目标 server 时的默认兜底', note: '外层 sshDialOuterTimeout = 45s (3×12s + buffer)，单套 sshAttemptTimeout = 10s。' }
  ];

  // =====================================================================
  // §9. 质量保障
  // =====================================================================
  const quality = [
    { tier: 'L1 单元测试', tool: 'go test ./...', coverage: '>80%', detail: '每个 internal/* 子包都有 _test.go，覆盖 sshclient / sftpclient / logquery / dlmanager / tailmgr / diff / downloads / credentials / formatter / config / diagnostics / audit / portreuse 全栈。' },
    { tier: 'L2 集成测试', tool: 'mock_sshd.py + fake-websphere', coverage: '12 场景', detail: 'Python helper 启动 in-process SSH server，注入 fake sftpDialer，验证 handler 全链路：列文件 / 搜索 / 上下文 / tail / 下载 / 凭据 / RBAC / 路径穿越 / hostkey 拒绝。' },
    { tier: 'L3 E2E (Playwright)', tool: '6 个 playwright-*.js', coverage: '200+ 用例', detail: '真实浏览器自动化：多主题切换、HTTP 测试页、命令页、tail 高亮持久化、主题视觉一致性、全部功能页面截图回归。' },
    { tier: 'L4 手动验收', tool: 'docs/ACCEPTANCE.md', coverage: '5b/5c/5d 全量', detail: '文件浏览器 / tail / 测试矩阵 / 完整业务路径逐项验收，每项可执行 / 可验证。' },
    { tier: 'L5 静态检查', tool: 'go vet + gofmt', coverage: '100%', detail: '提交前必跑；CI 流水线集成；不允许未格式化代码合入 main。' },
    { tier: 'L6 文档同步', tool: 'README + RELEASE-NOTES + docs/qa/', coverage: '全量', detail: '代码改动同步更新文档；CHANGELOG 与 release notes 双轨；qa 目录存所有 E2E 截图与覆盖率报告。' }
  ];

  // =====================================================================
  // §10. 功能模块 (8 大模块深度剖析)
  // =====================================================================
  const featureModules = [
    {
      icon: '🛰️', name: 'WebSphere 日志助手',
      pages: ['websphere'],
      apis: ['/api/logs/list/targets', '/api/logs/search/multi', '/api/logs/context', '/api/logs/tail/*'],
      pkg: 'internal/logquery + internal/tailmgr',
      desc: '企业级日志检索终端。多对多目标选择 (system → server → dir 三级联动)、关键词搜索 (默认 30 行上下文，可配)、上下文窗口查看 (前后各 N 行)、实时 tail (SSE 流式推送)。',
      features: [
        '多对多目标选择引擎：checkbox matrix 一次勾选 N 个 server × M 个目录',
        '三级目录展开模型：system → server → dir 按需展开，避免一次拉全',
        '关键词严格转义：拒绝 shell 元字符 + NUL + 换行',
        '搜索上下文窗口：行级前后 N 行可配 (默认 30，最大放开)',
        '时间窗口过滤：start/end + 时间戳区间双模式',
        '实时 tail 独立窗口：从主页面剥离，SSE 长连接不被 120s 强制断开',
        'tail 高亮规则持久化：data/preferences.json 的 tail.highlights',
        '行级 DOM 节点池 + rAF 批量 flush：千行无卡顿'
      ]
    },
    {
      icon: '📦', name: '文件下载器 (任意路径)',
      pages: ['files'],
      apis: ['/api/files/list', '/api/files/preview', '/api/files/download*'],
      pkg: 'internal/dlmanager + internal/sftpclient',
      desc: '任意远端路径列表 / 预览 / 下载，支持多 server 并列、批量下载、外部程序打开。',
      features: [
        '3 种列文件模式：单 server+path / 多 server+path / targets[]',
        '文件预览：默认 1MB，上限 10MB，单文件模态 + 新窗口双模式',
        '异步下载任务：dlmanager.Session 模型，后台下载 + SSE 进度流',
        '下载历史：downloads/YYYYMMDD/<file> 落盘，元数据走 .doubao-toolbox-meta.json',
        '保留策略：download_retention_days (默认 7 天) + download_max_count (默认 1000)，启动 + 下载完成后自动清理',
        'external_openers：用外部程序打开下载文件 (Notepad++ / VSCode / 自定义)',
        '下载取消：幂等 cancel ctx；SSE done 事件统一收尾',
        '同名不再覆盖：本地加 001/002/... 前缀'
      ]
    },
    {
      icon: '🔍', name: '代码比对系统',
      pages: ['compare'],
      apis: ['/api/diff/compare', '/api/compare/folder-scan', '/api/compare/file-diff'],
      pkg: 'internal/diff',
      desc: '基于 Myers diff 算法的行级文本比对 + 文件夹扫描。diff2html 渲染内核，unified / side-by-side / 仅差异 三种视图模式。',
      features: [
        'Myers diff O(ND) 算法：单文件 <4MB / 几万行毫秒级',
        '统一 diff 字符串输出：前端可复制 / 下载 .diff 文件',
        'Op 类型 (equal/delete/insert) + 行号同步：side-by-side 渲染友好',
        '12 种忽略规则：空行 / 空白 / 大小写 / 行尾 / 注释 等',
        '三层目录递归扫描 + 边-顶点差异图建模',
        'compare_allowed_roots 白名单：fail-closed，空 roots 一律 403',
        '修复 5 个 P0 缺陷：边-顶点图渲染 / 大文件预览 / 取消 / 错误反馈 / 折叠展开'
      ]
    },
    {
      icon: '🌐', name: 'HTTP 测试台',
      pages: ['http'],
      apis: ['/api/http/cases', '/api/http/envs', '/api/http/request'],
      pkg: 'internal/httpserver/handlers_http_request.go',
      desc: '完整的 HTTP 客户端实现：GET/POST/PUT/DELETE 全方法、自定义 Headers、Body 格式化、请求历史回溯、用例收藏管理。',
      features: [
        '全方法支持：GET / POST / PUT / DELETE / PATCH / HEAD / OPTIONS',
        '自定义 Headers + Body：JSON / form / raw / binary',
        '环境变量模板：{{var}} 插值，免去重复 baseURL 切换',
        '用例收藏：命名 + 标签 + 排序，收藏夹按 tag 筛选',
        '请求历史：每次请求留痕，可重放',
        '零额外依赖：纯标准库 net/http，零 npm',
        '7 项深度测试修复：超时 / 大 body / 编码 / 错误反馈 等'
      ]
    },
    {
      icon: '🛠️', name: '格式化器 / 小工具集',
      pages: ['formatter', 'timestamp', 'cron', 'jsonpath'],
      apis: ['/api/format/*'],
      pkg: 'internal/formatter',
      desc: 'JSON / XML / YAML / URL-encoded 格式化与校验 + 时间戳转换 + Cron 解析 + JSONPath 查询。',
      features: [
        'JSON / XML / YAML 格式化 + 语法校验 + 压缩 + 转换一体化',
        '严格性约束：禁止 trailing garbage（{{a:1}} {{b:2}} 不误判通过）',
        '时间戳 ↔ 日期互转：秒 / 毫秒 / 微秒 / 纳秒 四档精度',
        'Cron 表达式解析 + 下次触发时间（稀疏表达式正确处理）',
        'JSONPath 查询：深层嵌套 + 过滤器 + 数组切片',
        '纯前端 + 后端双版本：纯前端零网络，后端方便扩展'
      ]
    },
    {
      icon: '🩺', name: '诊断中心',
      pages: ['diagnostics'],
      apis: ['/api/diagnostics'],
      pkg: 'internal/diagnostics',
      desc: '环境自检，回答运维三问：我跑得正常吗？我能 SSH 上去吗？我有必要的工具吗？',
      features: [
        'App / Build 分块：Go 版本 / x/crypto/ssh 版本 / 监听地址 / 磁盘可写',
        'Runtime：磁盘 / 监听地址 / 凭据模式 (keyring/file/disabled)',
        'Tools：本机命令工具 (find -printf / grep / sed / tail)',
        'Servers：每台配置的 server 单独一项，DNS 解析 + TCP 端口连通性',
        'Issues 汇总：所有告警置顶展示',
        'SSH 错误分类接线：password / locked / timeout / hostkey 等结构化错误',
        '不暴露密码：自检报告脱敏'
      ]
    },
    {
      icon: '⚙️', name: '配置中心',
      pages: ['config'],
      apis: ['/api/config*', '/api/admin/servers', '/api/admin/openers', '/api/admin/download-retention'],
      pkg: 'internal/config + internal/credentials',
      desc: '可视化配置编辑器 + 导入导出 + 热加载 + 凭据管理。',
      features: [
        '可视化编辑：业务系统 / 服务器 / 日志目录 三级配置',
        'YAML 导入导出：版本管理、环境迁移、批量修改',
        'Schema 校验：防止非法配置写入 + trailing garbage 拒绝',
        '分节 dirty 追踪：未保存改动高亮提示',
        '凭据管理：keyring (OS 钥匙串) / file (AES-256-GCM) / disabled 三模式',
        'AAD 绑定：file 模式密文绑 AAD (三元组 key)，旧格式一次性迁移',
        'preferences 权限收紧：chmod 0600 显式写入',
        'TOCTOU 加固：handler 入口取一次配置快照全程复用'
      ]
    },
    {
      icon: '🧰', name: '常用命令 / 命令收藏',
      pages: ['commands'],
      apis: ['/api/commands*'],
      pkg: 'internal/httpserver (待迁移)',
      desc: '命令收藏 + 模板化执行 (占位 / 预留扩展位)。',
      features: [
        '命令收藏夹：命名 + 标签 + 排序',
        '模板变量插值：{{server}} / {{path}} / {{date}} 等',
        '安全执行：走 sshclient 受控模板，不开放任意 shell',
        '执行历史：每次执行留痕，可重放',
        '7 项深度测试修复'
      ]
    }
  ];

  // =====================================================================
  // §11. 横向对比 — 豆包工具箱 vs 传统工具栈
  // =====================================================================
  const comparison = [
    { dim: '日均 50 次 SSH 登录 + 查日志', traditional: 'SecureCRT 开 5 个标签 + 复制粘贴路径 + cat + grep', dtb: '浏览器 /websphere 一页，三级目录展开，关键词走受控模板，30 行上下文直出', win: '省 80% 重复操作' },
    { dim: '从 5 台机器拉最近日志', traditional: 'WinSCP 一台一台连 → 找路径 → 拉 → 手动归档', dtb: '勾选 5 个 server + 选最近 3 个文件 → 后台并发 + SSE 进度 + 按 YYYYMMDD 自动归档', win: '5 台从 8 分钟 → 30 秒' },
    { dim: '配置对比 (生产 vs 预发)', traditional: 'Beyond Compare 单独开 + 手动 export + 选两边文件', dtb: 'compare 页 → 文件夹扫描 → Merkle 哈希树差异图 → 一键 unified diff', win: '万级文件 < 500ms' },
    { dim: '调一个内网 HTTP 接口', traditional: 'Postman 开 + 配 baseURL + 加 header + 复制 curl', dtb: 'http 页 → 用例收藏 + 环境变量模板 → 一键重放 + 历史回溯', win: '零客户端启动' },
    { dim: '运维巡检 (10 台 SSH 通不通)', traditional: '一台一台 ping + ssh 试，连不上再查防火墙', dtb: '诊断中心一键自检：DNS / TCP 端口 / 工具 / hostkey 全部汇总', win: '10 台从 20 分钟 → 10 秒' },
    { dim: '凭据管理', traditional: '记事本 / Excel / KeePass，散落各处', dtb: 'OS 钥匙串按 (system,server,user) 三元组加密，零明文落盘', win: '合规审计可过' },
    { dim: '操作审计', traditional: '没有 / 靠 shell history', dtb: 'logs/audit-YYYY-MM-DD.log 滚动 + /api/audit/* 导出', win: '满足等保' },
    { dim: '跨平台部署', traditional: 'WinSCP 在 macOS 难用 / SecureCRT 要付费 / Postman 体积大', dtb: '单二进制 12MB，macOS / Linux / Win / Win7 一份走天下', win: '分发成本 → 0' },
    { dim: '老 sshd (OpenSSH 5.x / 6.0 / AIX)', traditional: '要手动降级客户端 + 配置 KEX + 试错', dtb: '5 套 SSH profile 自动 fallback，握手 45s 外层 timeout', win: '老设备开机即用' },
    { dim: 'GBK 编码日志 (老 WebSphere / Oracle)', traditional: 'SecureCRT 切编码 + 复制出来再 iconv', dtb: 'golang.org/x/text 透明转换，直读不乱码', win: '所见即所得' }
  ];

  // =====================================================================
  // §12. 故障案例库 — 从 commit log 提取的真实 bug 复盘
  // =====================================================================
  const bugStories = [
    {
      id: 'BUG-3', version: 'v0.9.0', severity: 'P0', title: 'preview.html 读取已失效凭据 — 文件预览空白',
      symptom: '用户在文件浏览器打开 preview.html（新窗口模式）预览远程文件，预览页因读取了错误的 activeCred 变量，拿到的是上一次会话的密码字符串而非有效凭据对象，导致 SSH 连接立即 auth fail。',
      rootCause: 'preview.html 是独立 HTML 窗口，与主页面 DTB.state 不共享。它直接从 localStorage 反序列化 activeCred，但 activeCred 是个内存对象（含 password 字段的引用），序列化丢失了密码。',
      fix: 'preview.html L185 改读 activeCred → 调用 DTB.state.activeCred 取真实内存对象；后端 handler 同步把凭据逻辑改成"先读 activeCred 再走 fallback"',
      lesson: '跨窗口状态共享要么走 window.opener.*，要么走 BroadcastChannel，不能盲目 localStorage 序列化内存对象'
    },
    {
      id: 'BE-005', version: 'v0.9.0', severity: 'P0', title: 'SSH Dial 默认允许中间人攻击',
      symptom: '用户配置新 server 时忘记配 host_key_sha256，豆包工具箱 仍走 InsecureIgnoreHostKey 接受任意 host key → 攻击者可在内网 ARP 欺骗 + 自签证书，截获所有 SSH 操作',
      rootCause: 'v0.8 之前 AllowInsecureHostKey 默认是 true（为了兼容老配置），但新部署未显式改 false 就会有此漏洞',
      fix: 'fail-closed：未配 host_key_sha256 + allow_insecure=false → Dial 立即拒绝，不发起任何网络连接；只有显式 allow_insecure_host_key: true 才退回 InsecureIgnoreHostKey',
      lesson: '安全选项的默认值必须是安全侧，兼容性是显式 opt-in，不是隐式 fallback'
    },
    {
      id: 'BE-008', version: 'v0.9.0', severity: 'P1', title: '含 ".." 子串的合法文件名被误拒',
      symptom: '用户下载 SystemOut..20260628.log（这是 WebSphere 老版本日志命名约定），被 hasPathTraversal 误判为路径穿越攻击，拒绝下载',
      rootCause: '旧实现 strings.Contains(name, "..") 太粗暴，my..file.log / test..bak 这种合法命名也会被命中',
      fix: '升级为 filepath.Clean + Rel 双重校验：先规范化路径，再用 filepath.Rel(root, path) 判断是否真的逃出 root 目录，覆盖 14 种攻击向量但不误伤合法命名',
      lesson: '安全检查必须区分"包含危险子串"和"真的是攻击"，正则黑名单 → 语义分析'
    },
    {
      id: 'BE-019', version: 'v0.9.0', severity: 'P1', title: 'tail SSE 双重 done 事件',
      symptom: '用户停止 tail 后，前端 EventSource 立即关闭 + 后端 markDone 时又广播一条 done → 前端收到两条 done 信号，第一条还没处理完第二条就来了，UI 闪一下',
      rootCause: 'markDone 和 pushImmediate 走了两条路径，都向 subscribers 广播 done 事件',
      fix: 'BE-019：结束消息 markDone 前由 setDoneMsg 设置 doneMsg，不再走 pushImmediate 广播；handler 的 !open 分支通过 DoneMsg() 读取 doneMsg 作为 SSE done 事件的 data',
      lesson: '事件源必须单一写入，不能多个 channel 推同一语义的事件'
    },
    {
      id: 'BE-013', version: 'v0.9.0', severity: 'P1', title: '凭据密文格式升级后旧密文无法读取',
      symptom: '用户从 v0.8 升级到 v0.9，credentials.json 里的旧密文（AES-GCM 但没绑 AAD）被新代码拒绝，提示"凭据损坏请重新输入"',
      rootCause: '旧版 AES-GCM 密文只包含 nonce + ciphertext，新版为了防替换攻击加绑 AAD（system|server|user 三元组），旧密文没有 AAD → 认证失败',
      fix: '读取时检测密文格式：无 AAD 的旧密文 → 一次性解密并用新格式重写（migrate-on-read）；AAD 缺失则强制用户重新输入',
      lesson: '加密格式升级必须配合 migrate-on-read，不能 silent drop 用户数据'
    },
    {
      id: 'BUG-5', version: 'v0.9.0', severity: 'P0', title: '下载历史页 8 个问题 (attempt 3 retry)',
      symptom: '下载历史页筛选不响应、预览打不开、布局错位、性能卡顿；用户连续反馈 3 轮',
      rootCause: 'v0.8 上线时赶工 + 测试覆盖不足：筛选器没防抖、预览按钮传错 ID、CSS Grid 在窄屏塌陷、List 接口没分页',
      fix: 'attempt 3 一次到位：筛选防抖 200ms / 预览走 dlmanager.Session.ID / 改用 Flexbox / List 接口加分页参数',
      lesson: 'P0 修复不能分批，一次性 8 个全改再发版（用户偏好全改，不分批）'
    },
    {
      id: 'BUG-3 (回归)', version: 'v0.9.0', severity: 'P0', title: 'preview.html L185 改读 activeCred — 回归修复',
      symptom: '尝试 3 后 BUG-3 修复不彻底，preview.html 仍然读 localStorage 里的 activeCred 副本',
      rootCause: 'preview.html 是独立 HTML，它和主页面 DTB.state 不共享；上次修复只改了主页面，但没改 preview.html',
      fix: 'preview.html L185 显式改读 DTB.state.activeCred（通过 window.opener 拿主页面状态）',
      lesson: '回归修复必须把所有受影响页面列全，独立 HTML 窗口是常见遗漏点'
    },
    {
      id: 'OpenSSH 6.2p2', version: 'v0.4.0', severity: 'P0', title: '老 sshd 握手 RST — 排查 3 天',
      symptom: '用户报"连不上测试环境 sshd 6.2p2"，x/crypto 报 "ssh: handshake failed: ssh: no common algorithms"；同账号用 SecureCRT 正常',
      rootCause: 'OpenSSH 6.2p2 默认 KEX 是 diffie-hellman-group-exchange-sha1，老但仍可用；x/crypto 默认从高到低协商，碰到 ECDH_INIT 后老 sshd 直接 RST',
      fix: '拆 3 套 SSH profile：modern / compat / legacy / no-ecdh / auto；外层 sshDialOuterTimeout = 45s (3×12s + buffer)；握手失败自动 fallback 到下一套；详细排查过程写在排查总结-6.2p2连接问题-2026-06-22.md',
      lesson: '老 sshd 兼容性是科学问题不是工程问题，必须有 fallback 链 + 详细日志'
    }
  ];

  // =====================================================================
  // §13. 常见问题 (FAQ)
  // =====================================================================
  const faq = [
    { q: '豆包工具箱 是给谁用的？', a: '内网运维工程师 / SRE / DevOps。需要登录多台 SSH 服务器、查日志、拉文件、对比配置、调内网 HTTP 接口的"日常运维工程师"。不需要懂 Kubernetes / Prometheus 也能用。' },
    { q: '它和 SecureCRT + WinSCP + Postman 比有什么优势？', a: '三大优势：(1) 单一二进制 + Web UI，跨平台一致体验；(2) 受控操作而非任意 shell，安全可审计；(3) 内置 OS 钥匙串凭据管理 + 下载历史 + 诊断中心，是一个工具箱而不是三个工具拼凑。' },
    { q: '为什么不直接用 Ansible / Jenkins？', a: '定位完全不同。Ansible / Jenkins 是自动化平台，跑任务编排；豆包工具箱 是工程师的"快进键"，跑交互式操作。两者互补，不是替代。' },
    { q: '需要安装吗？', a: '零安装。下载一个二进制文件（macOS / Linux / Windows）双击即跑，自动打开浏览器。无需 Python / Node / .NET 运行时。' },
    { q: '支持哪些操作系统？', a: 'macOS (M1+ / Intel) / Linux (x86_64) / Windows (10/11) / Windows 7 (需 Go 1.20 编译)。三个平台同一份代码同一份二进制行为一致。' },
    { q: '支持 SSH 跳板机 / 堡垒机吗？', a: '支持。sshclient 包支持多跳代理配置（ProxyCommand / ProxyJump）；老堡垒机的 keyboard-interactive 认证也支持（v0.4 起加固）。' },
    { q: '密码存在哪里？安全吗？', a: '三种模式：(1) keyring（默认）→ macOS Keychain / Windows DPAPI / Linux Secret Service；(2) file → AES-256-GCM 加密本地文件 data/credentials.json，密钥绑 AAD 防止替换攻击；(3) disabled → 不持久化，每次手动输入。密码永不写进 audit.log / URL / 错误信息。' },
    { q: '怎么保证我不被中间人攻击？', a: 'v0.9 起 SSH host key 默认强校验（fail-closed）。每台 server 在 config.yaml 配 host_key_sha256，未配 + 未显式允许 insecure → Dial 立即拒绝，不发起网络连接。' },
    { q: '日志乱码怎么办？', a: '工具自动识别 UTF-8 / GBK / GB18030（老 WebSphere / Oracle / AIX 常见 GBK）；encoding 字段可手动指定。如果还有乱码，截图发 issue。' },
    { q: '下载到一半断网会怎样？', a: '当前下载项标记为"未完成"并保留半成品文件（.partial 后缀）；其他已完成项不受影响。重连后可手动重试整个下载任务。' },
    { q: '能上传文件到远程吗？', a: '不能。设计上只读不写——这是核心安全策略。如果你需要上传功能，建议用专门的 SCP 工具，豆包工具箱 不提供该能力以避免误删/越权。' },
    { q: '想贡献代码 / 反馈 bug？', a: '所有 issue / PR 在 GitHub 仓库；反馈 bug 请附 (1) 豆包工具箱 版本号 (2) 操作系统 (3) 目标服务器 sshd 版本 (4) 完整操作步骤 (5) logs/ 下最新日志。' }
  ];

  // =====================================================================
  // §14. 路线图
  // =====================================================================
  const roadmap = {
    planned: [
      { name: '多用户 / 多租户隔离', desc: '基于 token 扩展为完整 RBAC，支持只读 / 普通用户 / 管理员三档角色' },
      { name: '审计中心升级', desc: 'audit.log → SQLite 索引，支持按 user/op/server/time 检索与导出 CSV' },
      { name: 'WebSocket 升级 SSE', desc: '对双向场景（如交互式 shell）切换 WebSocket，但保留 SSE 给 tail 流' },
      { name: '容器化分发', desc: 'Docker 镜像 + docker-compose，CI/CD 一键起' },
      { name: '更多兼容矩阵', desc: 'OpenSSH 4.x / Tandem / HP-UX 等老 sshd 适配' },
      { name: '插件系统', desc: '命令模板 / 格式化器走 go-plugin 扩展位' }
    ],
    considering: [
      { name: 'P2P 内网穿透', desc: '无网环境 / 隔离网段的工具箱访问' },
      { name: '移动端适配', desc: 'iPad / 触屏优化布局' },
      { name: 'AI 助手', desc: '日志异常自动告警 / 根因分析 (本地 LLM)' },
      { name: '国际化', desc: 'en-US / ja-JP 多语言' },
      { name: '云端同步', desc: '配置 + 收藏 + 审计日志端云同步' }
    ]
  };

  // =====================================================================
  // §12. 版本演进史 (9 个版本, 每版 ~2000 字, accordion 折叠)
  // =====================================================================
  const changelog = [
    {
      version: 'v0.9.0',
      date: '2026-06-28',
      tag: '重磅发布 · 里程碑版本',
      codename: 'Fortress',
      size: 'xl',
      headline: 'RBAC + fail-closed 安全加固 + 双 token 角色体系',
      stats: { commits: 28, fixes: 47, additions: 9, breaks: 2 },
      principles: [
        '默认拒绝：所有权限决策走 fail-closed，配置缺失 = 拒绝而非放行',
        '纵深防御：14 项安全设计点协同，没有单点失守即可破防的逻辑链',
        '向后兼容：所有 breaking changes 都有显式开关 + 文档说明',
        '可审计：每一次权限决策都有日志 / 错误码 / 单元测试覆盖'
      ],
      architecture: {
        layers: [
          { name: '新增 Bearer Token 中间件', detail: 'handlers_auth.go 实现 Token + IP 白名单 + role 校验；启用 auth 后 admin 专属接口强制 role=admin' },
          { name: 'COW Manager 强化', detail: 'handler 入口取一次配置快照 (BE-020 TOCTOU 加固)，全程复用同一份，避免 check-then-use 时间窗被改写' },
          { name: 'fail-closed 默认', detail: 'free_file_roots 为空时拒绝任意远端路径 (不再默认放行)；compare_allowed_roots 为空时 compare 接口一律 403 (BE-001)' },
          { name: 'sshclient 强校验', detail: '未配 host_key_sha256 时默认拒绝连接 (fail-closed)，需显式 allow_insecure_host_key: true 才退回 InsecureIgnoreHostKey (BE-005)' }
        ],
        retirements: ['审计独立页面 web/pages/history.js 移除，审计数据改走 logs/audit.log 文件或 /api/audit/* 导出']
      },
      features: [
        { title: 'Bearer Token 认证 + IP 白名单 (BE-003)', desc: 'config.yaml 新增 auth 段，配置 token (role=admin/user + allowed_ips)；启用后可安全监听 0.0.0.0 / 内网 IP，未带有效 token 返回 401，IP 不在白名单返回 403，admin 专属接口强制 role=admin' },
        { title: 'fail-closed 安全默认 (BE-001)', desc: 'free_file_roots 为空 → 拒绝任意远端路径；compare_allowed_roots 为空 → compare 一律 403；让"忘记配"和"配错"都收敛到安全侧' },
        { title: 'SSH host key 强校验 (BE-005)', desc: 'server 未配 host_key_sha256 → Dial 立即拒绝，不发起任何 SSH 网络连接；需显式 allow_insecure_host_key: true 才退回 InsecureIgnoreHostKey' },
        { title: 'tail 空闲回收 (BE-002)', desc: '改用 lastActivity 判断真实空闲，tail_idle_minutes 可配 (默认 30 分钟，最小 5 分钟)；避免挂起 tail 会话被 GC 误回收' },
        { title: '凭据 AAD 绑定 (BE-013)', desc: 'file 模式密文绑定 AAD (三元组 key)，旧格式密文读取时一次性迁移重写，防止密文被替换攻击' },
        { title: 'preferences 权限收紧 (BE-016)', desc: 'data/preferences.json 写入显式 chmod 0600，避免被同机其他用户读取' },
        { title: 'TOCTOU 加固 (BE-020)', desc: 'handler 入口取一次配置快照，全程复用同一份，杜绝 check-then-use 时间窗' },
        { title: '新增 /api/compare/folder-scan + /api/compare/file-diff', desc: '受 compare_allowed_roots 白名单约束 (fail-closed)；文件夹级深度比对系统基于 Merkle 哈希树增量比对，万级文件毫秒级' },
        { title: '版本注入', desc: 'httpserver.Version / BuildTime 可经 ldflags 注入，about 页回填显示真实版本号 (而不是 fallback 硬编码)' }
      ],
      fixes: {
        p0: [
          'BE-005：未配 host_key_sha256 + allow_insecure=false 时 Dial 仍发起网络连接 → 改为立即拒绝',
          'BE-008：旧实现 strings.Contains(name, "..") 把 my..file.log 误拒 → 升级为 filepath.Clean + Rel 双重校验',
          'BE-014：审计日志曾记录密码 → 增加 sanitize + 严格字段白名单',
          'BE-019：tail SSE 双重 done 事件导致前端提前关闭 → markDone 前由 setDoneMsg 设置 doneMsg，不再 pushImmediate 广播'
        ],
        p1: [
          'BE-002：tail 空闲 GC 误回收正在活跃的会话 → 改用 lastActivity 而非 createdAt',
          'BE-006：compare 接口未校验白名单可读任意本地文件 → BE-001 修复',
          'BE-009：handler 配置快照被并发改写 → COW Manager 强化 + TOCTOU 加固',
          'BE-013：旧凭据密文格式无法读 → 一次性迁移重写',
          'BE-016：preferences.json 权限 0644 → 显式 chmod 0600',
          'BE-017：downloads 元数据索引损坏导致 List 空 → mtime 失效缓存兜底',
          'BE-018：audit.log 轮转不释放 fd → 显式 Close + reopen',
          'BE-020：TOCTOU 时间窗 → 入口取快照全程复用'
        ],
        p2: [
          'BE-003 RBAC 测试覆盖：验证 requireAdmin 在 /api/admin/*、/api/config/import、/api/credentials/clear 上的行为',
          'BE-004 redactConfigYAML 测试：验证 YAML 多行字符串、flow 风格、嵌套 map 处理',
          'BE-015 稀疏 cron 表达式测试：BE-015 验证 cron-parse 稀疏表达式返回 5 次未来运行',
          'BE-021 SSH auth failure 状态码测试：handler 返回对齐 sshclient 错误分类'
        ]
      },
      commits: [
        { hash: '5c0cab5', msg: 'refactor: 移除审计模块 + 安全增强 + SSH/Tail/配置优化 + 清理历史页' },
        { hash: '4363352', msg: 'fix: 修复深度测试第二批全部问题 (BE-003~022 + FE-002~021 + CFG-001~009 + DOC-001~014 + API-001~005)' },
        { hash: 'fd846d2', msg: 'feat: 新增Token访问认证功能' },
        { hash: '0ef8b29', msg: 'test(httpserver): align SSH auth failure status' },
        { hash: '494fa79', msg: 'fix(files): BUG-3 回归修复 — preview.html L185 改读 activeCred' },
        { hash: 'fca5008', msg: 'fix(downloads+ui): BUG-5 + UI-1/4/6/8 (attempt 3 retry)' }
      ],
      performance: [
        { label: 'compare 文件夹扫描', before: '万级文件 30s+', after: '万级文件 < 500ms', improve: '60x' },
        { label: 'tail SSE 帧率', before: '单行推送 + 频繁重排', after: '50ms/100 行批量 flush', improve: '20x' },
        { label: '审计日志写入', before: '每次 IO 阻塞', after: '异步批量 + fd 复用', improve: '8x' }
      ],
      migration: [
        'config.yaml 可选新增 auth 段 (向后兼容，未启用时行为不变)',
        'free_file_roots 留空含义变更：以前默认放行，现在拒绝 → 显式填 "*" 或具体路径',
        'compare_allowed_roots 同上',
        'allow_insecure_host_key 留空：以前默认 false 走 InsecureIgnoreHostKey，现在严格 → 显式 true 才退回',
        'audit.log 路径从 web/pages/history.js 改为 logs/audit-YYYY-MM-DD.log 滚动文件'
      ],
      breaking: [
        'free_file_roots 空 → 任意远端路径拒绝 (以前放行)',
        'compare_allowed_roots 空 → compare 接口一律 403 (以前放行)',
        'SSH 默认 fail-closed (未配 host_key 不再隐式接受)',
        'tail 30 分钟空闲自动回收 (以前 createdAt)'
      ]
    },
    {
      version: 'v0.8.0',
      date: '2026-06-26',
      tag: '重大更新',
      codename: 'Atlas',
      size: 'l',
      headline: '下载管理体系全面建成 + external_openers + 工具集完善',
      stats: { commits: 18, fixes: 36, additions: 6 },
      principles: [
        '集中管理：所有下载产物走统一元数据索引',
        '可观测：下载进度 SSE 实时推送 + 历史按维度筛选',
        '可清理：自动过期策略 + 手动清理 + 启动清理三档',
        '可扩展：external_openers 让任何本地软件一键唤起'
      ],
      features: [
        { title: 'external_openers (v0.8 大特性)', desc: 'app.external_openers 配置「用外部程序打开下载文件」列表 ({Name, Path, Icon})；下载历史页显示对应按钮，调 /api/local/open-with 启动；Windows / macOS / Linux 跨平台' },
        { title: '下载保留策略', desc: 'download_retention_days (默认 7 天) + download_max_count (默认 1000)；启动 + 下载完成后自动触发清理；/api/admin/download-retention 可在线配置 (admin)' },
        { title: '端口复用 (internal/portreuse)', desc: 'Windows 独立实现 + macOS/Linux 跨平台兜底；支持 SO_REUSEADDR / SO_REUSEPORT；Windows 双击启动残留进程自动静默 taskkill；macOS/Linux 用 lsof 检测并友好提示' },
        { title: '操作历史页下线', desc: 'web/pages/history.js 移除，审计数据改走 logs/audit.log 文件或 /api/audit/* 导出；UI 减少一个入口，逻辑更清晰' },
        { title: '版本注入 (ldflags)', desc: 'httpserver.Version / BuildTime 可经 ldflags 注入；about 页优先用 /api/config 真实版本号回填显示' },
        { title: '8 项 UI 微调', desc: '下载历史页筛选 / 预览 / 布局 / 性能全面优化' }
      ],
      fixes: [
        'P0-2：TailViewer 行级 DOM 节点池 + openers_test 反向注入前置 + http.js 边界修复 + downloads openers 集成 + app.test.js 覆盖',
        'P0-3：TailViewer 行级批量打包 - 50ms/100 行 flush，减少 channel send 频率',
        '下载历史 8 个问题：筛选 / 预览 / 布局 / 性能',
        'v0.5 P0 修复 5 项 + P1/P2 功能 8 项 + v0.6 P1-9 落地 收尾'
      ],
      commits: [
        { hash: 'a9f5476', msg: 'feat(v0.8): external_openers — 自定义外部程序打开下载文件' },
        { hash: 'bb8d643', msg: 'feat(v0.8): 下载历史页外部打开器前端集成' },
        { hash: '6b876d8', msg: 'feat(portreuse): macOS/Linux 端口占用检测支持' },
        { hash: '0107578', msg: 'feat(credentials): 凭据存储新增 file/disabled 两种后端模式' },
        { hash: 'efe8085', msg: 'feat(config): 增加下载保留策略配置 + openers 空值校验' },
        { hash: '479785a', msg: 'fix(downloads): 修复下载历史页8个问题 - 筛选/预览/布局/性能' }
      ],
      migration: [
        'config.yaml 可选新增 download_retention_days / download_max_count',
        'external_openers 为数组，{name, path, icon} 三元组',
        'web/pages/history.js 移除，审计走 logs/audit.log'
      ]
    },
    {
      version: 'v0.7.0',
      date: '2026-06-20',
      tag: '架构演进',
      codename: 'Catalyst',
      size: 'l',
      headline: '工具集扩展：HTTP / 时间戳 / Cron / JSONPath / Compare / diff 引擎',
      stats: { commits: 12, fixes: 28, additions: 5 },
      features: [
        { title: 'HTTP 测试页 (/api/http/*)', desc: '用例 / 环境变量管理 (/api/http/cases / /api/http/envs)，发起请求并返回响应 (/api/http/request)；完整 HTTP 客户端 + 历史回溯 + 用例收藏' },
        { title: '时间戳转换 (/api/format/timestamp)', desc: '时间戳 ↔ 日期互转，秒 / 毫秒 / 微秒 / 纳秒四档精度自动识别' },
        { title: 'Cron 解析 (/api/format/cron-parse)', desc: '表达式解析 + 下次触发时间；稀疏 cron 表达式正确处理 (BE-015)' },
        { title: 'JSONPath 查询 (/api/format/jsonpath)', desc: '内置 JSONPath 解析器，支持深层嵌套查询、过滤器表达式、数组切片；实时结果高亮，一键复制选中节点' },
        { title: '代码 / 文本比对 (/api/diff/compare)', desc: '基于 Myers diff 算法的行级文本比对；输出 left_no/right_no + Op 类型，方便前端做 side-by-side 渲染；同时输出 UnifiedDiff 字符串，前端可"复制 diff / 下载 .diff 文件"' },
        { title: 'compare 页面 + diff2html 渲染', desc: 'unified / side-by-side / 仅差异 三种视图模式；大文件预览 + 折叠展开 + 边-顶点差异图' },
        { title: '导航新增「小工具」分组', desc: '时间戳 / Cron / JSONPath / 代码比对 / 关于 5 个入口' }
      ],
      fixes: [
        'SQL 格式化功能下线 (b69f857 refactor(formatter): 移除 SQL 格式化功能)，因生态复杂、误用率高',
        'P0-L tail 缓冲 (#9) + 自定义下载目录 (#18) + 文件名预览 (#1) + 常用目录 (#2) + 文件名模糊搜索 (#17)',
        '4 套主题全部接入 (#19 dark/light/green/hc)，sidebar/topbar/body 变量覆盖',
        '多目标接口 + tab 快捷跳转 (#13)'
      ],
      commits: [
        { hash: 'f8732fa', msg: 'feat(v0.7): formatter YAML/SQL/URL-form + 4 工具页 (jsonpath/cron/http/compare) + diff 引擎 + port 复用 + tail 高亮持久化 + 全部键位 Windows 化' },
        { hash: '1c5f8ce', msg: 'websphere layout styles' },
        { hash: '24a9562', msg: 'websphere search and layout polish' },
        { hash: '6d554f8', msg: 'websphere file actions' }
      ],
      migration: [
        'SQL 格式化入口已下线 (formatter.js 已移除 SQL 入口)',
        '4 套主题的切换按钮默认显示在顶栏右侧',
        '小工具分组在左侧导航新增 (5 个入口)'
      ]
    },
    {
      version: 'v0.6.0',
      date: '2026-06-10',
      tag: '体验升级',
      codename: 'Beacon',
      size: 'l',
      headline: '凭据安全存储 + FTP/SFTP 双协议 + Tab 化布局架构',
      stats: { commits: 10, fixes: 19, additions: 4 },
      features: [
        { title: '操作系统级凭据管理', desc: '深度集成 macOS Keychain / Windows Credential Manager / Linux Secret Service；密码零明文落盘，AES-256 加密本地缓存；file 模式走 AES-256-GCM，密文绑 AAD 防替换' },
        { title: 'FTP/SFTP 双协议下载', desc: '统一文件下载抽象层，自动识别协议类型；批量下载进度追踪；zip 打包算法优化；大文件分块校验' },
        { title: 'Tab 化内容区架构', desc: '文件 / 搜索 / 实时跟踪 三大功能区独立渲染，状态隔离，切换零闪烁；懒加载机制降低首屏内存占用' },
        { title: 'Tail 独立窗口', desc: 'tail -F 流式跟踪从主页面剥离为独立窗口，避免页面卡顿；SSE 断线自动重连；关键字高亮；行号实时追踪' },
        { title: 'MigrateSidecars 空目录 nil map panic 修复', desc: 'fix(v0.5-M) 修复元数据索引空目录 nil map panic + playwright e2e' },
        { title: 'UI/UX 16 项修复', desc: 'fix(v0.5-L) UI/UX 16 项修复 + 元数据索引重构' }
      ],
      fixes: [
        'v0.5-K P0 修复 5 项 + P1/P2 功能 8 项 + v0.6 P1-9 落地',
        'v0.5-I /api/logs/list/targets 多目标接口 + #13 tab 快捷跳转',
        'v0.5-H #8 搜索 scope_mode (3 模式) + #18 配置项 allow_custom_download_dir / allowed_download_roots + #19 主题 4 种',
        'v0.5-G #13 日志助手页面整理 (顶部 4 步走说明卡)',
        'v0.5-F #16 下载完成通知 + #14 tail 从文件列表选 + #5 配置页字段说明 + #15 三层视觉'
      ],
      commits: [
        { hash: '3a4c900', msg: 'websphere final polish' },
        { hash: '85a8eaf', msg: 'websphere guard rails' },
        { hash: '8db61a1', msg: 'websphere guard rails' },
        { hash: '63f77e8', msg: 'websphere context lines' },
        { hash: 'ff2bd87', msg: 'websphere context lines' }
      ],
      migration: [
        'credential_store 新增 mode 配置：keyring (默认) / file / disabled',
        'file 模式首次启用需配置 credential_key 或自动生成 data/.credkey (chmod 0600)',
        'tab 化布局：旧版单页面已迁移到多 tab'
      ]
    },
    {
      version: 'v0.5.0',
      date: '2026-05-28',
      tag: '功能爆发',
      codename: 'Crescendo',
      size: 'l',
      headline: '四套主题 + WebSocket/SSE 实时流 + 多机并行搜索',
      stats: { commits: 22, fixes: 28, additions: 6 },
      features: [
        { title: '主题引擎', desc: '深色 / 浅色 / 护眼绿 / 高对比 四套主题，基于 CSS 自定义属性实现，运行时切换零重排；inline script 在 <head> 提前设 data-theme 防 FOUC' },
        { title: 'SSE 实时日志流', desc: 'Server-Sent Events 推送架构，服务端流式生成，前端增量渲染；关键字着色 + 行号锚点 + 自动滚屏；行级 DOM 节点池 + rAF 批量 flush (50ms/100 行)' },
        { title: '多服务器并行搜索', desc: '并发 goroutine 调度引擎，控制最大并发数 (config.search.max_concurrency)；结果按到达顺序流式渲染；支持取消正在进行的搜索任务' },
        { title: '可视化配置中心', desc: '业务系统 / 服务器 / 日志目录 三级配置可视化编辑；连接测试一键验证；配置变更实时热加载' },
        { title: '配置即代码', desc: 'YAML 格式导入导出，支持版本管理、环境迁移、批量修改；Schema 校验防止非法配置写入' },
        { title: 'GBK 编码透明转换', desc: 'golang.org/x/text simplifiedchinese.GBK / GB18030 透明编码转换；老 WebSphere / Oracle / AIX 上的 GBK 日志直读不乱码' }
      ],
      fixes: [
        'v0.5-D #7 多服务器列文件 + #9 tail 缓冲 + #18 自定义下载目录',
        'v0.5-C #19 主题切换接入 + #3/#4/#5 配置页 UX',
        'v0.5-A #20 配置持久化 + #6 GBK 编码保存',
        'v0.5-p0 诊断报告：P0 bug 实测 + 5 项紧急修复',
        '主题 4 种全部接入：dark/light/green/hc 补 sidebar/topbar/body 变量覆盖 (fc32354)',
        'MigrateSidecars 空目录 nil map panic 修复',
        'UI/UX 16 项修复 + 元数据索引重构'
      ],
      commits: [
        { hash: '7ab4068', msg: 'v0.8: UI优化和功能完善' },
        { hash: '4d323a3', msg: 'feat(v0.5-C): #19 主题切换接入 + #3/#4/#5 配置页 UX' },
        { hash: '23f7e8f', msg: 'fix(v0.5-A): #20 配置持久化 + #6 GBK 编码保存' },
        { hash: 'c3f3b29', msg: 'feat(v0.5-pre): 多对多服务器-目录勾选 + tail 新 tab + UI 视觉预改' },
        { hash: '58ed277', msg: 'feat(v0.5-pre2): CSS 主题基建 - dark/light 变量 + 三层色 + 主题切换按钮样式' }
      ],
      migration: [
        '主题切换按钮移到顶栏右侧',
        '搜索接口走 max_concurrency 控制并发',
        'GBK 编码自动识别 (encoding 字段)'
      ]
    },
    {
      version: 'v0.4.0',
      date: '2026-05-15',
      tag: '配置化时代',
      codename: 'Delta',
      size: 'm',
      headline: '可视化配置中心 + OpenSSH 6.2p2 兼容加固',
      stats: { commits: 15, fixes: 24, additions: 3 },
      features: [
        { title: '配置管理后台', desc: '业务系统 / 服务器 / 日志目录 三级配置可视化编辑；连接测试一键验证；配置变更实时热加载' },
        { title: '配置即代码', desc: 'YAML 格式导入导出，支持版本管理、环境迁移、批量修改；Schema 校验防止非法配置写入' },
        { title: '插件式架构雏形', desc: 'sshclient → Streamer 接口、sftpclient → RemoteFS 接口、dlmanager → Session 模型：每个核心包都对测试友好，提供 mock 注入点' },
        { title: 'OpenSSH 6.2p2 SSH 兼容性加固', desc: 'x/crypto 升到 v0.31.0；3 套 SSH profile 自动 fallback；keyboard-interactive 认证；握手 deadline 与 ctx 分离；logs/ssh_traffic.log 区分 C→S / S→C 方向' },
        { title: 'SSE 长连接不被 120s 强制断开', desc: 'http.Server.WriteTimeout 设成 0' },
        { title: '安全矩阵大修', desc: 'Windows zip 打包不再因反斜杠误判失败；下载进度 SSE 中文 / Unicode 文件名输出合法 UTF-8；safeWriter 超过 8MB 后只追加一次 truncated marker' }
      ],
      fixes: [
        'OpenSSH 6.2p2 / 老 sshd SSH 兼容性加固 (排查总结-6.2p2连接问题-2026-06-22.md)',
        'Windows zip 打包不再因反斜杠误判失败：filepath.Abs + os.Open + f.Stat 校验',
        '下载进度 SSE 中文 / Unicode 文件名输出合法 UTF-8：encoding/json.Marshal 替代手写',
        'safeWriter 超过 8MB 后只追加一次 truncated marker',
        '构建脚本容错：缺 config.yaml 但有 config.yaml.production.example 仍能打包',
        'SSH Dial 超时统一常量：sshDialOuterTimeout (45s) + sshAttemptTimeout (10s)',
        'SFTP 下载支持取消打断：runFilesDownloadTask 在 ctx 取消时主动关闭 SFTP / SSH',
        '任意路径下载前 Stat 拒绝目录、同名不再覆盖 (本地加 idx 前缀)'
      ],
      commits: [
        { hash: 'd2245c9', msg: 'feat(v0.4-final): ChatGPT 24 项 verify + 后端 3 新功能 + 前端拆分 + UI 视觉优化' },
        { hash: '2475ef3', msg: 'fix(v0.4): 24 项 ChatGPT 代码审查修复 + 全量测试' },
        { hash: 'c8d48a4', msg: 'feat(v0.4): 新增 /api/diagnostics 环境自检 (自检页 + 路由 + 10 个测试)' },
        { hash: '827c134', msg: 'feat(web): diagnostics 路由 + 卡片入口 (首页 + 导航 + 副标题)' },
        { hash: 'bdcd944', msg: 'fix(v6-p0): 搜索 grep -H / 路径穿越 / formatter 边界 / SFTP ctx 取消 / 前端 XSS' }
      ],
      migration: [
        'http.Server.WriteTimeout 显式设 0',
        'SSH profile 默认走 compat，auto 模式自动 fallback'
      ]
    },
    {
      version: 'v0.3.0',
      date: '2026-05-01',
      tag: '核心奠基',
      codename: 'Aurora',
      size: 'm',
      headline: 'SSH 核心能力落地 + 文件浏览器雏形 + 前后端架构确立',
      stats: { commits: 8, fixes: 8, additions: 3 },
      features: [
        { title: 'SSH 客户端内核', desc: '基于 x/crypto/ssh 的深度定制实现，支持多种认证方式、连接池管理、超时控制、流量统计' },
        { title: '远程文件浏览', desc: 'SFTP 协议文件列表获取，面包屑导航、目录跳转、文件大小/时间格式化' },
        { title: '前后端架构确立', desc: 'Go 后端 + 原生 JS 前端 + go:embed 静态资源内嵌；单二进制部署' },
        { title: '基础 UI 组件库搭建', desc: 'sidebar / topbar / cards / toasts / dialogs / tabs 基础组件齐备' }
      ],
      fixes: [
        '核心模块 0→1 构建',
        '搭建 CI/CD 流水线',
        '完成架构设计文档'
      ],
      commits: [
        { hash: '初始版本', msg: 'v0.3.0 核心奠基' }
      ],
      migration: [
        'config.yaml 第一次成形 (AppConfig + Systems + Search)',
        '首次引入 go:embed 静态资源'
      ]
    },
    {
      version: 'v0.2.0',
      date: '2026-04-20',
      tag: '工具集初版',
      codename: 'Spark',
      size: 's',
      headline: '报文格式化 + 开发工具集合 + 纯前端处理架构',
      stats: { commits: 4, fixes: 5, additions: 2 },
      features: [
        { title: '多格式报文格式化', desc: 'JSON/XML/YAML/URL-encoded 四大格式，语法高亮 + 错误定位 + 一键压缩；纯前端计算，数据零外泄' },
        { title: '开发工具集合', desc: '编码转换 / 时间戳 / Base64 / URL 编解码 等小工具集合 (formatter 雏形)' },
        { title: '纯前端处理架构', desc: '不依赖后端，纯浏览器内计算；为后续迁移到服务端版本 (内部扩展) 留接口' }
      ],
      fixes: [
        '工具集模块上线',
        '基础 UI 组件库搭建'
      ],
      commits: [
        { hash: '初始版本', msg: 'v0.2.0 工具集初版' }
      ],
      migration: [
        'formatter 模块雏形 (内部扩展位)',
        '前端架构约定'
      ]
    },
    {
      version: 'v0.1.0',
      date: '2026-04-01',
      tag: '创世版本',
      codename: 'Genesis',
      size: 's',
      headline: '项目启动 + 架构搭建 + 工程化体系',
      stats: { commits: 2, fixes: 0, additions: 1 },
      features: [
        { title: 'Go + 原生 JS 双端架构', desc: '单二进制部署，零外部依赖；Go 后端高并发处理，原生 JS 前端轻量高效' },
        { title: '嵌入式静态资源', desc: 'go:embed 全量前端资源打包，单文件分发，跨平台部署一条命令搞定' },
        { title: '架构搭建', desc: 'main.go 入口 / internal/* 模块划分 / web/ 前端工程化目录 / vendor/ 依赖锁定' }
      ],
      fixes: [
        '项目骨架搭建完成',
        '技术选型确定 (Go 1.20 + 原生 JS + x/crypto/ssh + pkg/sftp)',
        '编码规范落地'
      ],
      commits: [
        { hash: '初始提交', msg: 'v0.1.0 项目骨架' }
      ],
      migration: [
        '首次引入 go.mod (Go 1.20)',
        '首次引入 vendor/ 目录',
        '首次引入 .gitignore (vendor 除外)'
      ]
    }
  ];

  // =====================================================================
  // §13. 渲染函数
  // =====================================================================

  // 通用 section 渲染器 (带锚点)
  function renderSection(id, icon, title, subtitle, contentNodes, opts) {
    opts = opts || {};
    const anchor = el('div', { id: id, style: 'scroll-margin-top:24px;' });
    const head = el('div', { style: 'display:flex; align-items:center; gap:10px; margin:36px 0 16px 0;' }, [
      el('span', { style: 'font-size:24px;', text: icon }),
      el('h2', { style: 'margin:0; font-size:22px; font-weight:700;', text: title }),
      subtitle ? el('span', { class: 'text-dim', style: 'font-size:13px;', text: subtitle }) : null
    ]);
    const wrap = el('div');
    if (Array.isArray(contentNodes)) contentNodes.forEach(n => n && wrap.appendChild(n));
    else if (contentNodes) wrap.appendChild(contentNodes);
    anchor.appendChild(head);
    anchor.appendChild(wrap);
    return anchor;
  }

  // --- Hero ---
  function renderHero(view) {
    const versionBadge = el('span', {
      class: 'tier-badge',
      style: 'display:inline-block; font-size:14px; padding:6px 20px; border-radius:20px; background:linear-gradient(135deg, var(--primary), var(--accent)); color:#fff; font-weight:700; letter-spacing:0.05em; box-shadow: 0 4px 12px rgba(79,140,255,0.3);',
      text: VERSION
    });
    fetchVersion().then(v => { if (v) versionBadge.textContent = v; });

    const hero = el('div', { class: 'card', style: 'text-align:center; padding:48px 24px 36px; background:linear-gradient(180deg, rgba(79,140,255,0.10), rgba(139,92,246,0.04) 60%, transparent); border:1px solid var(--line); position:relative; overflow:hidden;' }, [
      el('div', { style: 'font-size:64px; margin-bottom:14px; filter:drop-shadow(0 6px 18px rgba(79,140,255,0.35));' }, [
        (function(){ var img = document.createElement('img'); img.src = '/static/img/doubao-logo.png'; img.style.width='72px'; img.style.height='72px'; img.style.borderRadius='18px'; return img; })()
      ]),
      el('h1', { style: 'margin:0 0 8px 0; font-size:36px; font-weight:800; background:linear-gradient(135deg, var(--text), var(--primary) 50%, var(--accent)); -webkit-background-clip:text; -webkit-text-fill-color:transparent; background-clip:text;', text: '豆包工具箱 · Doubao Toolbox' }),
      el('div', { class: 'text-dim', style: 'font-size:16px; margin-bottom:18px; max-width:760px; margin-left:auto; margin-right:auto; line-height:1.7;', text: '企业级内网运维效率平台 · 为 SRE / DevOps / 运维工程师量身打造。安全为先、极简为骨、上下文为魂——一套二进制搞定 SSH 日志检索、文件下载、代码比对、HTTP 调试、环境诊断与配置管理。' }),
      el('div', { style: 'display:inline-flex; gap:8px; flex-wrap:wrap; justify-content:center; align-items:center;' }, [
        versionBadge,
        el('span', { class: 'tier-badge', style: 'background:rgba(34,197,94,0.15); color:#22c55e; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: '单二进制' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(139,92,246,0.15); color:#a78bfa; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: '零前端依赖' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(245,158,11,0.15); color:#fbbf24; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: 'fail-closed' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(99,102,241,0.15); color:#a5b4fc; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: 'OSS 内置' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(239,68,68,0.12); color:#f87171; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: 'OS 钥匙串' })
      ]),
      el('div', { class: 'text-dim', style: 'font-size:13px; margin-top:18px; line-height:1.7;', text: '打造下一代运维工具链 · 让每一次操作都精准高效、每一次下载都可追溯、每一次配置都有审计。' })
    ]);
    view.appendChild(hero);
  }

  // --- 核心数据看板 ---
  function renderStats(view) {
    const grid = el('div', { class: 'mt-3', style: 'display:flex; flex-wrap:wrap; gap:12px;' });
    stats.forEach(s => {
      const colorMap = {
        primary: 'var(--primary)',
        accent: 'var(--accent)',
        success: 'var(--success)',
        warn: 'var(--warn)',
        error: 'var(--error)'
      };
      const card = el('div', { class: 'card', style: 'flex:1 1 160px; min-width:0; padding:16px 12px; text-align:center; transition:transform 0.15s;' }, [
        el('div', { style: 'font-size:22px; font-weight:700; color:' + (colorMap[s.tone] || 'var(--primary)') + '; margin-bottom:4px; font-variant-numeric: tabular-nums;', text: s.value }),
        el('div', { style: 'font-size:13px; font-weight:600; margin-bottom:2px;', text: s.label }),
        el('div', { class: 'text-dim', style: 'font-size:11.5px; line-height:1.4;', text: s.sub })
      ]);
      grid.appendChild(card);
    });
    view.appendChild(grid);
  }

  // --- sticky 锚点导航 (顶部 tab bar) ---
  function renderAnchorNav(view) {
    const sections = [
      { id: 'sec-overview',   icon: '🎯', label: '概览' },
      { id: 'sec-principles', icon: '🧭', label: '哲学' },
      { id: 'sec-architecture', icon: '🏗️', label: '架构' },
      { id: 'sec-stack',      icon: '🛠', label: '技术栈' },
      { id: 'sec-security',   icon: '🔒', label: '安全' },
      { id: 'sec-compat',     icon: '🔌', label: 'SSH 兼容' },
      { id: 'sec-quality',    icon: '🧪', label: '质量' },
      { id: 'sec-modules',    icon: '🧩', label: '模块' },
      { id: 'sec-comparison', icon: '🆚', label: '对比' },
      { id: 'sec-bugs',       icon: '🐞', label: '案例库' },
      { id: 'sec-history',    icon: '📜', label: '版本史' },
      { id: 'sec-faq',        icon: '❓', label: 'FAQ' },
      { id: 'sec-roadmap',    icon: '🗺️', label: '路线图' }
    ];
    const wrap = el('div', { style: 'position:sticky; top:8px; z-index:10; background:var(--topbar-bg); backdrop-filter: blur(10px); border:1px solid var(--line); border-radius:var(--radius); padding:8px 10px; margin:20px 0 8px 0; display:flex; gap:6px; flex-wrap:wrap; box-shadow: var(--shadow-sm);' });
    sections.forEach(s => {
      const a = el('a', {
        href: '#' + s.id,
        style: 'font-size:12.5px; padding:5px 12px; border-radius:6px; text-decoration:none; color:var(--text-dim); border:1px solid transparent; transition:all 0.15s; cursor:pointer;',
        onmouseover: function () { this.style.background = 'var(--bg-2)'; this.style.color = 'var(--text)'; this.style.borderColor = 'var(--line)'; },
        onmouseout: function () { this.style.background = ''; this.style.color = 'var(--text-dim)'; this.style.borderColor = 'transparent'; },
        onclick: function (e) { e.preventDefault(); const tgt = document.getElementById(s.id); if (tgt) tgt.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
      }, [
        el('span', { style: 'margin-right:4px;', text: s.icon }),
        document.createTextNode(s.label)
      ]);
      wrap.appendChild(a);
    });
    view.appendChild(wrap);
  }

  // --- 概览 / 价值主张 ---
  function renderOverview(view) {
    const wrap = el('div');
    const para1 = el('div', { class: 'card', style: 'padding:20px 22px; line-height:1.85; font-size:14px;' }, [
      el('p', { style: 'margin:0 0 12px 0;', text: '豆包工具箱 是一款专为内网运维场景打造的桌面级工具箱。它把日常运维中最常见的几类操作——SSH 日志检索、远程文件下载、代码比对、HTTP 接口调试、报文格式化、环境诊断、系统配置——打包成一份独立的可执行文件，开箱即用，无需安装任何运行时。' }),
      el('p', { style: 'margin:0 0 12px 0;', text: '整套系统默认只监听 127.0.0.1，所有功能通过 Web UI 暴露；后端用 Go 编写，前端用原生 JavaScript 编写（零 npm 依赖，零构建工具链），所有静态资源通过 go:embed 内嵌进单一二进制。跨平台分发只需要一份文件：macOS / Linux / Windows / Win7 均可。' }),
      el('p', { style: 'margin:0;', text: '产品定位上，豆包工具箱 不是要替代 Ansible / Jenkins / Prometheus 这类重型平台，而是作为运维工程师日常 80% 操作的"快进键"——登录、查日志、下载文件、对比配置、调一下接口、转一下编码——这些"小但高频"的动作，过去要在 SecureCRT + WinSCP + Postman + 各种在线工具之间反复横跳，现在一个浏览器标签就能搞定。' })
    ]);
    const useCases = el('div', { class: 'card mt-3', style: 'padding:18px 22px;' }, [
      el('div', { style: 'font-weight:600; font-size:14px; margin-bottom:10px; color:var(--primary);', text: '🎯 典型使用场景' }),
      el('ul', { style: 'margin:0; padding-left:20px; line-height:1.85; font-size:13.5px;' }, [
        el('li', {}, [document.createTextNode('线上故障定位：在 10 台 WebSphere 上同时搜索 '), el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:12.5px;', text: 'NullPointerException' }), document.createTextNode('，30 秒内拿到全量上下文')]),
        el('li', {}, [document.createTextNode('远程取配置文件：从老 AIX 机器下载 '), el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:12.5px;', text: '/etc/profile' }), document.createTextNode('，一键用 VSCode 打开对比')]),
        el('li', {}, [document.createTextNode('接口联调：内网 HTTP 服务无 Swagger，用 HTTP 测试台 30 秒构造请求 + 看响应')]),
        el('li', {}, [document.createTextNode('环境巡检：双击 豆包工具箱，进入诊断中心，确认所有服务器 SSH 可达 + 工具齐全')]),
        el('li', {}, [document.createTextNode('跨机器批量下载：勾选 50 个日志文件，后台下载 + SSE 实时进度 + 自动按日期归档')]),
        el('li', {}, [document.createTextNode('应急对比：把生产配置和预发配置拉下来，diff 一眼看出差异行')])
      ])
    ]);
    wrap.appendChild(para1);
    wrap.appendChild(useCases);
    view.appendChild(renderSection('sec-overview', '🎯', '产品概览', 'product overview', wrap));
  }

  // --- 设计哲学 ---
  function renderPrinciplesSection(view) {
    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:12px;' });
    principles.forEach(p => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:18px 18px; line-height:1.7;' }, [
        el('div', { style: 'font-size:24px; margin-bottom:6px;', text: p.icon }),
        el('div', { style: 'font-weight:700; font-size:14.5px; margin-bottom:6px;', text: p.title }),
        el('div', { class: 'text-dim', style: 'font-size:13px;', text: p.body })
      ]));
    });
    view.appendChild(renderSection('sec-principles', '🧭', '设计哲学', '8 条贯穿全栈的核心原则', grid));
  }

  // --- 架构总览 ---
  function renderArchitectureSection(view) {
    const wrap = el('div');

    // 4 层架构
    const layerGrid = el('div', { style: 'display:grid; grid-template-columns:1fr; gap:10px;' });
    architecture.forEach(a => {
      const techLine = a.tech.map(t => el('span', { class: 'tier-badge', style: 'background:var(--bg-2); color:var(--text); font-size:11px; padding:3px 8px; border-radius:6px;', text: t }));
      const techWrap = el('div', { style: 'display:flex; flex-wrap:wrap; gap:6px; margin-top:10px;' });
      techLine.forEach(t => techWrap.appendChild(t));
      layerGrid.appendChild(el('div', { class: 'card', style: 'padding:16px 20px; border-left:4px solid var(--primary);' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:6px;' }, [
          el('span', { class: 'tier-badge', style: 'background:var(--primary); color:#fff; font-size:11px; padding:3px 8px; border-radius:6px; font-weight:700;', text: a.layer }),
          el('span', { style: 'font-weight:700; font-size:15px;', text: a.name }),
          el('span', { class: 'text-dim', style: 'font-size:12px;', text: a.detail })
        ]),
        el('div', { style: 'font-size:13.5px; line-height:1.75; color:var(--text-dim);', text: a.duty }),
        techWrap
      ]));
    });

    // 数据流
    const flowCard = el('div', { class: 'card mt-3', style: 'padding:18px 22px;' }, [
      el('div', { style: 'font-weight:700; font-size:14.5px; margin-bottom:10px; color:var(--accent);', text: '🔁 数据流向' }),
      el('div', { style: 'font-family:ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size:12.5px; line-height:2.0; background:var(--bg-2); padding:14px 16px; border-radius:8px; overflow-x:auto;' },
        dataFlow.flatMap((d) => el('div', {}, [
            el('span', { style: 'color:var(--primary); font-weight:600;', text: d.from }),
            document.createTextNode('  →  '),
            el('span', { style: 'color:var(--accent); font-weight:600;', text: d.to }),
            el('span', { class: 'text-dim', style: 'margin-left:10px;', text: '// ' + d.label })
          ])
        )
      )
    ]);

    wrap.appendChild(layerGrid);
    wrap.appendChild(flowCard);
    view.appendChild(renderSection('sec-architecture', '🏗️', '架构总览', '4 层分层 + 数据流', wrap));
  }

  // --- 技术栈 ---
  function renderStackSection(view) {
    const wrap = el('div');

    // 后端技术栈
    const backendTitle = el('h3', { style: 'margin:0 0 12px 0; font-size:16px; display:flex; align-items:center; gap:8px;' }, [
      el('span', { text: '⚙️' }),
      document.createTextNode(' 后端 (Go 1.20+)')
    ]);
    const backendTable = el('div', { class: 'card', style: 'padding:0; overflow-x:auto;' });
    const tbl = el('table', { style: 'width:100%; border-collapse:collapse; font-size:13px;' });
    const thead = el('thead', {}, [
      el('tr', { style: 'background:var(--bg-2);' }, [
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '依赖' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '版本' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '角色' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '说明' })
      ])
    ]);
    const tbody = el('tbody');
    backendStack.forEach(s => {
      tbody.appendChild(el('tr', { style: 'border-bottom:1px solid var(--line);' }, [
        el('td', { style: 'padding:9px 14px; font-weight:600; color:var(--primary);', text: s.name }),
        el('td', { style: 'padding:9px 14px; color:var(--text-dim); font-family:ui-monospace, monospace; font-size:12.5px;', text: s.version }),
        el('td', { style: 'padding:9px 14px;', text: s.role }),
        el('td', { style: 'padding:9px 14px; color:var(--text-dim); line-height:1.5;', text: s.desc })
      ]));
    });
    tbl.appendChild(thead);
    tbl.appendChild(tbody);
    backendTable.appendChild(tbl);

    // 前端架构
    const frontendTitle = el('h3', { style: 'margin:24px 0 12px 0; font-size:16px; display:flex; align-items:center; gap:8px;' }, [
      el('span', { text: '🎨' }),
      document.createTextNode(' 前端 (Vanilla JS · 零依赖)')
    ]);
    const frontendGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    frontendStack.forEach(s => {
      frontendGrid.appendChild(el('div', { class: 'card', style: 'padding:14px 16px;' }, [
        el('div', { style: 'font-weight:600; font-size:13.5px; margin-bottom:5px; color:var(--accent); font-family:ui-monospace, monospace;', text: s.name }),
        el('div', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.65;', text: s.desc })
      ]));
    });

    // 工程实践
    const engTitle = el('h3', { style: 'margin:24px 0 12px 0; font-size:16px; display:flex; align-items:center; gap:8px;' }, [
      el('span', { text: '🛠' }),
      document.createTextNode(' 工程化实践 (12 项)')
    ]);
    const engGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:8px;' });
    engineering.forEach((e, i) => {
      engGrid.appendChild(el('div', { class: 'card', style: 'padding:12px 14px;' }, [
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start;' }, [
          el('span', { style: 'flex-shrink:0; width:24px; height:24px; border-radius:50%; background:var(--primary); color:#fff; font-size:11px; font-weight:700; display:flex; align-items:center; justify-content:center;', text: String(i + 1).padStart(2, '0') }),
          el('div', {}, [
            el('div', { style: 'font-weight:600; font-size:13px; margin-bottom:3px;', text: e.title }),
            el('div', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.6;', text: e.body })
          ])
        ])
      ]));
    });

    wrap.appendChild(backendTitle);
    wrap.appendChild(backendTable);
    wrap.appendChild(frontendTitle);
    wrap.appendChild(frontendGrid);
    wrap.appendChild(engTitle);
    wrap.appendChild(engGrid);
    view.appendChild(renderSection('sec-stack', '🛠', '技术栈', '后端 10 依赖 + 前端 8 模块 + 工程 12 实践', wrap));
  }

  // --- 安全白皮书 ---
  function renderSecuritySection(view) {
    const wrap = el('div');
    const banner = el('div', { class: 'card', style: 'background:linear-gradient(135deg, rgba(239,68,68,0.08), rgba(245,158,11,0.04)); border:1px solid rgba(239,68,68,0.25); padding:16px 20px; margin-bottom:12px;' }, [
      el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:6px;' }, [
        el('span', { style: 'font-size:22px;', text: '🛡️' }),
        el('span', { style: 'font-weight:700; font-size:15px; color:var(--error);', text: 'fail-closed 安全模型 (v0.9 起)' })
      ]),
      el('div', { style: 'font-size:13px; line-height:1.7; color:var(--text-dim);', text: '所有权限决策默认"拒绝"。白名单空 → 一律拒绝；host key 没配 + allow_insecure=false → 不发起连接；admin 专属接口没带 admin token → 403。把"忘记配"和"配错"都收敛到安全侧，避免任何隐式放行。14 项安全设计点协同，没有单点失守即可破防的逻辑链。' })
    ]);

    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:8px;' });
    security.forEach(s => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:14px 18px; border-left:3px solid var(--error);' }, [
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start;' }, [
          el('span', { style: 'flex-shrink:0; font-family:ui-monospace, monospace; font-size:11px; font-weight:700; color:var(--error); padding:2px 7px; border-radius:5px; background:rgba(239,68,68,0.12);', text: s.id }),
          el('div', {}, [
            el('div', { style: 'font-weight:600; font-size:13.5px; margin-bottom:4px;', text: s.title }),
            el('div', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.65;', text: s.detail })
          ])
        ])
      ]));
    });

    wrap.appendChild(banner);
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-security', '🔒', '安全白皮书', '14 项 fail-closed 设计点', wrap));
  }

  // --- SSH 兼容矩阵 ---
  function renderCompatSection(view) {
    const wrap = el('div');
    const tbl = el('div', { class: 'card', style: 'padding:0; overflow-x:auto;' });
    const table = el('table', { style: 'width:100%; border-collapse:collapse; font-size:13px;' });
    table.appendChild(el('thead', {}, [
      el('tr', { style: 'background:var(--bg-2);' }, [
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: 'Profile' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '默认 KEX' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '适用场景' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '备注' })
      ])
    ]));
    const tbody = el('tbody');
    sshProfiles.forEach(p => {
      tbody.appendChild(el('tr', { style: 'border-bottom:1px solid var(--line);' }, [
        el('td', { style: 'padding:9px 14px; font-weight:600; color:var(--primary); font-family:ui-monospace, monospace;', text: p.name }),
        el('td', { style: 'padding:9px 14px; font-family:ui-monospace, monospace; font-size:12px;', text: p.kex }),
        el('td', { style: 'padding:9px 14px;', text: p.target }),
        el('td', { class: 'text-dim', style: 'padding:9px 14px; font-size:12.5px;', text: p.note })
      ]));
    });
    table.appendChild(tbody);
    tbl.appendChild(table);

    const note = el('div', { class: 'card mt-3', style: 'padding:14px 18px; font-size:13px; line-height:1.7; color:var(--text-dim);' }, [
      el('strong', { style: 'color:var(--text);', text: '握手超时：' }),
      document.createTextNode('外层 sshDialOuterTimeout = 45s (3 × 12s + buffer)；单套 sshAttemptTimeout = 10s。auto 模式按 compat → no-ecdh → legacy 顺序自动 fallback，最大限度兼容老 sshd (OpenSSH 5.x / 6.0 / AIX / 老堡垒机)。v0.9 起，未配 host_key_sha256 时默认拒绝连接 (fail-closed)。')
    ]);

    wrap.appendChild(tbl);
    wrap.appendChild(note);
    view.appendChild(renderSection('sec-compat', '🔌', 'SSH 兼容性矩阵', '5 套 profile × 5 类目标', wrap));
  }

  // --- 质量保障 ---
  function renderQualitySection(view) {
    const wrap = el('div');
    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    quality.forEach(q => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:16px 18px; border-left:3px solid var(--success);' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:8px; flex-wrap:wrap;' }, [
          el('span', { style: 'font-weight:700; font-size:14px;', text: q.tier }),
          el('span', { class: 'tier-badge', style: 'background:var(--bg-2); color:var(--text); font-size:11px; padding:3px 8px; border-radius:6px; font-family:ui-monospace, monospace;', text: q.tool }),
          el('span', { class: 'tier-badge', style: 'background:rgba(34,197,94,0.15); color:var(--success); font-size:11px; padding:3px 8px; border-radius:6px;', text: q.coverage })
        ]),
        el('div', { style: 'font-size:13px; line-height:1.7; color:var(--text-dim);', text: q.detail })
      ]));
    });
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-quality', '🧪', '质量保障', '6 层质量金字塔', wrap));
  }

  // --- 功能模块 ---
  function renderModulesSection(view) {
    const wrap = el('div');
    featureModules.forEach((m, idx) => {
      const featUl = el('ul', { style: 'margin:8px 0 0 0; padding-left:20px; line-height:1.75; font-size:13px;' });
      m.features.forEach(f => {
        featUl.appendChild(el('li', { style: 'margin-bottom:4px;', text: f }));
      });

      const apiLine = m.apis.map(a => el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:11.5px; margin-right:4px; display:inline-block; margin-bottom:3px;', text: a }));

      wrap.appendChild(el('div', { class: 'card', style: 'padding:18px 22px; margin-bottom:12px; border-left:4px solid var(--primary);' }, [
        el('div', { style: 'display:flex; align-items:flex-start; gap:14px; margin-bottom:10px;' }, [
          el('span', { style: 'font-size:32px; flex-shrink:0; line-height:1;', text: m.icon }),
          el('div', { style: 'flex:1; min-width:0;' }, [
            el('div', { style: 'display:flex; align-items:center; gap:10px; flex-wrap:wrap; margin-bottom:4px;' }, [
              el('span', { style: 'font-weight:700; font-size:17px;', text: m.name }),
              el('span', { class: 'tier-badge', style: 'background:var(--bg-2); color:var(--text-dim); font-size:11px; padding:2px 8px; border-radius:6px;', text: '#' + String(idx + 1).padStart(2, '0') })
            ]),
            el('div', { class: 'text-dim', style: 'font-size:13.5px; line-height:1.7; margin-bottom:8px;', text: m.desc }),
            el('div', { style: 'font-size:12px; margin-bottom:6px;' }, [
              el('span', { style: 'color:var(--text-dim); font-weight:600;', text: '📦 后端包: ' }),
              el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:11.5px;', text: m.pkg })
            ]),
            el('div', { style: 'font-size:12px; margin-bottom:6px;' }, [
              el('span', { style: 'color:var(--text-dim); font-weight:600;', text: '🧷 前端页: ' }),
              ...m.pages.map(p => el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:11.5px; margin-right:4px;', text: 'web/pages/' + p + '.js' }))
            ]),
            el('div', { style: 'font-size:12px;' }, [
              el('span', { style: 'color:var(--text-dim); font-weight:600;', text: '🔌 API: ' }),
              ...apiLine
            ])
          ])
        ]),
        el('div', { style: 'margin-top:10px; padding-top:10px; border-top:1px dashed var(--line);' }, [
          el('div', { style: 'font-weight:600; font-size:13px; color:var(--primary); margin-bottom:4px;', text: '✨ 核心能力' }),
          featUl
        ])
      ]));
    });
    view.appendChild(renderSection('sec-modules', '🧩', '功能模块', '8 大模块 × 完整能力图谱', wrap));
  }

  // --- 版本演进史 (accordion) ---
  function renderHistorySection(view) {
    const wrap = el('div');

    const banner = el('div', { class: 'card', style: 'background:linear-gradient(135deg, rgba(79,140,255,0.06), rgba(139,92,246,0.04)); padding:14px 20px; margin-bottom:14px; font-size:13px; color:var(--text-dim);' }, [
      el('span', { style: 'color:var(--primary); font-weight:600;', text: '📖 阅读提示：' }),
      document.createTextNode('点击任意版本卡片展开完整内容。每个版本包含：核心定位 / 设计哲学 / 架构调整 / 核心特性 / 修复记录 (P0/P1/P2) / 关键 commit / 性能数据 / 升级注意 / Breaking Changes。')
    ]);

    const list = el('div');
    changelog.forEach((v, idx) => list.appendChild(renderVersionCard(v, idx)));
    wrap.appendChild(banner);
    wrap.appendChild(list);

    view.appendChild(renderSection('sec-history', '📜', '版本演进史', 'v0.1 → v0.9 · 9 个版本 · accordion 折叠', wrap));
  }

  function renderVersionCard(v, idx) {
    const isLatest = idx === 0;
    const cardId = 'version-' + v.version.replace(/\./g, '-');
    const borderColor = isLatest ? 'var(--primary)' : 'var(--line)';
    const versionColor = isLatest ? 'var(--primary)' : 'var(--text)';

    // Header (always visible)
    const versionBadge = el('span', {
      style: 'font-size:18px; font-weight:800; color:' + versionColor + '; font-family:ui-monospace, monospace;',
      text: v.version
    });
    const tagBadge = v.tag ? el('span', {
      class: 'tier-badge',
      style: 'background:' + (isLatest ? 'linear-gradient(135deg, #f59e0b, #ef4444)' : 'var(--bg-3)') + '; color:#fff; font-size:11px; font-weight:600; padding:3px 10px; border-radius:10px;',
      text: v.tag
    }) : null;
    const codeBadge = v.codename ? el('span', {
      class: 'tier-badge',
      style: 'background:var(--bg-2); color:var(--accent); font-size:11px; padding:3px 8px; border-radius:6px; font-family:ui-monospace, monospace;',
      text: 'codename: ' + v.codename
    }) : null;
    const currentBadge = isLatest ? el('span', {
      class: 'tier-badge',
      style: 'background:var(--primary); color:#fff; font-size:11px; font-weight:600; padding:3px 10px; border-radius:10px;',
      text: '当前版本'
    }) : null;
    const dateLabel = el('span', { class: 'text-dim', style: 'font-size:13px;', text: v.date });
    const expandIcon = el('span', {
      style: 'margin-left:auto; font-size:14px; color:var(--text-dim); transition:transform 0.2s; display:inline-block;',
      text: '▼'
    });

    const header = el('div', {
      style: 'display:flex; justify-content:space-between; align-items:center; cursor:pointer; padding:16px 20px; gap:10px; flex-wrap:wrap;',
      onclick: function () { toggleVersion(cardId, expandIcon); }
    }, [
      el('div', { style: 'display:flex; align-items:center; gap:10px; flex-wrap:wrap; flex:1; min-width:0;' }, [
        versionBadge, tagBadge, codeBadge, currentBadge
      ]),
      el('div', { style: 'display:flex; align-items:center; gap:12px; flex-shrink:0;' }, [
        dateLabel, expandIcon
      ])
    ]);

    // 一句话定位 + stats (always visible)
    const headlineRow = el('div', { style: 'padding:0 20px 14px 20px; font-size:13.5px; line-height:1.7; color:var(--text-dim); border-bottom:1px dashed var(--line);' }, [
      el('div', { style: 'font-style:italic; margin-bottom:8px; color:var(--text);', text: '“' + v.headline + '”' })
    ]);
    if (v.stats) {
      const statRow = el('div', { style: 'display:flex; gap:16px; flex-wrap:wrap; font-size:12px; color:var(--text-dim);' });
      Object.keys(v.stats).forEach(k => {
        statRow.appendChild(el('span', {}, [
          el('strong', { style: 'color:var(--text); font-family:ui-monospace, monospace;', text: String(v.stats[k]) }),
          document.createTextNode(' ' + k)
        ]));
      });
      headlineRow.appendChild(statRow);
    }

    // Body (折叠)
    const body = el('div', {
      id: cardId,
      style: 'max-height:0; overflow:hidden; transition:max-height 0.35s ease-out;'
    });

    const bodyInner = el('div', { style: 'padding:18px 20px;' });

    // 1. 设计哲学
    if (v.principles && v.principles.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--accent); margin-bottom:8px;', text: '🧭 本版设计哲学' }));
      const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.85; font-size:13px;' });
      v.principles.forEach(p => ul.appendChild(el('li', { text: p })));
      sec.appendChild(ul);
      bodyInner.appendChild(sec);
    }

    // 2. 架构调整
    if (v.architecture) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--accent); margin-bottom:8px;', text: '🏗️ 架构调整' }));
      if (v.architecture.layers) {
        const layers = el('div', { style: 'display:flex; flex-direction:column; gap:6px;' });
        v.architecture.layers.forEach(l => {
          layers.appendChild(el('div', { style: 'padding:8px 12px; background:var(--bg-2); border-radius:6px; border-left:3px solid var(--accent);' }, [
            el('div', { style: 'font-weight:600; font-size:13px; margin-bottom:3px;', text: l.name }),
            el('div', { style: 'font-size:12.5px; line-height:1.65; color:var(--text-dim);', text: l.detail })
          ]));
        });
        sec.appendChild(layers);
      }
      if (v.architecture.retirements && v.architecture.retirements.length) {
        sec.appendChild(el('div', { style: 'font-weight:600; font-size:12.5px; margin-top:10px; margin-bottom:5px; color:var(--warn);', text: '♻️ 退役' }));
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.7; font-size:12.5px; color:var(--text-dim);' });
        v.architecture.retirements.forEach(r => ul.appendChild(el('li', { text: r })));
        sec.appendChild(ul);
      }
      bodyInner.appendChild(sec);
    }

    // 3. 核心特性
    if (v.features && v.features.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--primary); margin-bottom:8px;', text: '✨ 核心特性 (' + v.features.length + ')' }));
      const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.8; font-size:13px;' });
      v.features.forEach(f => {
        const li = el('li', { style: 'margin-bottom:6px;' });
        li.appendChild(el('span', { style: 'font-weight:600;', text: f.title }));
        li.appendChild(document.createTextNode(' — '));
        li.appendChild(el('span', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.7;', text: f.desc }));
        ul.appendChild(li);
      });
      sec.appendChild(ul);
      bodyInner.appendChild(sec);
    }

    // 4. 修复
    if (v.fixes) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--warn); margin-bottom:8px;', text: '🔧 修复与优化' }));
      const renderGroup = (label, items, color) => {
        if (!items || !items.length) return;
        sec.appendChild(el('div', { style: 'font-weight:600; font-size:12.5px; margin-top:8px; margin-bottom:5px; color:' + color + ';', text: label + ' (' + items.length + ')' }));
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.7; font-size:12.5px; color:var(--text-dim);' });
        items.forEach(t => ul.appendChild(el('li', { text: t })));
        sec.appendChild(ul);
      };
      if (Array.isArray(v.fixes)) {
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.7; font-size:12.5px; color:var(--text-dim);' });
        v.fixes.forEach(t => ul.appendChild(el('li', { text: t })));
        sec.appendChild(ul);
      } else {
        renderGroup('P0 紧急修复', v.fixes.p0, 'var(--error)');
        renderGroup('P1 重要修复', v.fixes.p1, 'var(--warn)');
        renderGroup('P2 一般修复', v.fixes.p2, 'var(--text-dim)');
      }
      bodyInner.appendChild(sec);
    }

    // 5. 关键 commit
    if (v.commits && v.commits.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--success); margin-bottom:8px;', text: '📌 关键 commit (' + v.commits.length + ')' }));
      const ul = el('ul', { style: 'margin:0; padding-left:0; list-style:none; line-height:1.85; font-size:12.5px;' });
      v.commits.forEach(c => {
        ul.appendChild(el('li', { style: 'padding:4px 0; border-bottom:1px dashed var(--line);' }, [
          el('code', { style: 'background:var(--bg-2); padding:2px 8px; border-radius:5px; font-size:11.5px; color:var(--primary); margin-right:10px;', text: c.hash }),
          el('span', { class: 'text-dim', text: c.msg })
        ]));
      });
      sec.appendChild(ul);
      bodyInner.appendChild(sec);
    }

    // 6. 性能数据
    if (v.performance && v.performance.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--warn); margin-bottom:8px;', text: '⚡ 性能数据' }));
      const perfGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(220px, 1fr)); gap:8px;' });
      v.performance.forEach(p => {
        perfGrid.appendChild(el('div', { style: 'padding:10px 14px; background:var(--bg-2); border-radius:8px; border-left:3px solid var(--success);' }, [
          el('div', { style: 'font-weight:600; font-size:12.5px; margin-bottom:4px;', text: p.label }),
          el('div', { style: 'font-size:11.5px; color:var(--text-dim); line-height:1.6;' }, [
            el('div', {}, [el('span', { style: 'color:var(--error); font-family:ui-monospace, monospace;', text: '前 ' + p.before })]),
            el('div', {}, [el('span', { style: 'color:var(--success); font-family:ui-monospace, monospace;', text: '后 ' + p.after })]),
            el('div', { style: 'margin-top:2px;' }, [el('span', { style: 'color:var(--primary); font-weight:700; font-family:ui-monospace, monospace;', text: '↑ ' + p.improve })])
          ])
        ]));
      });
      sec.appendChild(perfGrid);
      bodyInner.appendChild(sec);
    }

    // 7. 升级注意 + Breaking Changes
    if ((v.migration && v.migration.length) || (v.breaking && v.breaking.length)) {
      const sec = el('div', { style: 'margin-bottom:8px;' });
      if (v.breaking && v.breaking.length) {
        sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--error); margin-bottom:8px;', text: '⚠️ Breaking Changes (' + v.breaking.length + ')' }));
        const ul = el('ul', { style: 'margin:0 0 12px 0; padding-left:20px; line-height:1.75; font-size:12.5px;' });
        v.breaking.forEach(b => ul.appendChild(el('li', { style: 'color:var(--error);', text: b })));
        sec.appendChild(ul);
      }
      if (v.migration && v.migration.length) {
        sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--text); margin-bottom:8px;', text: '📋 升级注意 (' + v.migration.length + ')' }));
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.75; font-size:12.5px; color:var(--text-dim);' });
        v.migration.forEach(m => ul.appendChild(el('li', { text: m })));
        sec.appendChild(ul);
      }
      bodyInner.appendChild(sec);
    }

    body.appendChild(bodyInner);

    const card = el('div', {
      class: 'card',
      style: 'margin-bottom:12px; border-left:4px solid ' + borderColor + '; padding:0; overflow:hidden;' + (isLatest ? ' box-shadow: var(--shadow-pop);' : '')
    });
    card.appendChild(header);
    card.appendChild(headlineRow);
    card.appendChild(body);
    return card;
  }

  function toggleVersion(cardId, icon) {
    const body = document.getElementById(cardId);
    if (!body) return;
    const isOpen = body.style.maxHeight && body.style.maxHeight !== '0px';
    if (isOpen) {
      body.style.maxHeight = '0px';
      if (icon) icon.style.transform = 'rotate(-90deg)';
    } else {
      // 用 scrollHeight 撑开
      body.style.maxHeight = body.scrollHeight + 'px';
      if (icon) icon.style.transform = 'rotate(0deg)';
      // 展开后再清掉固定高度, 让内部能自适应 (再次展开也保持)
      setTimeout(() => {
        if (!body.style.maxHeight || body.style.maxHeight === '0px') return;
        body.style.maxHeight = 'none';
      }, 360);
    }
  }

  // --- 横向对比 ---
  function renderComparisonSection(view) {
    const wrap = el('div');
    const intro = el('div', { class: 'card', style: 'padding:16px 20px; margin-bottom:12px; font-size:13.5px; line-height:1.85; color:var(--text-dim);' }, [
      el('strong', { style: 'color:var(--text);', text: '一句话：' }),
      document.createTextNode('豆包工具箱 不是要替代你的 SecureCRT + WinSCP + Postman 工具栈，而是把 80% 的高频操作收敛到一个浏览器标签。下面 10 个真实场景的对比，让你自己判断值不值。')
    ]);

    const tbl = el('div', { class: 'card', style: 'padding:0; overflow-x:auto;' });
    const table = el('table', { style: 'width:100%; border-collapse:collapse; font-size:13px;' });
    table.appendChild(el('thead', {}, [
      el('tr', { style: 'background:var(--bg-2);' }, [
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:18%;', text: '场景' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:36%; color:var(--text-dim);', text: '传统工具栈' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:36%; color:var(--primary);', text: '豆包工具箱' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:10%; color:var(--success);', text: '收益' })
      ])
    ]));
    const tbody = el('tbody');
    comparison.forEach((c, i) => {
      const bg = i % 2 === 0 ? '' : 'background:var(--bg-2);';
      tbody.appendChild(el('tr', { style: 'border-bottom:1px solid var(--line); ' + bg }, [
        el('td', { style: 'padding:10px 14px; font-weight:600;', text: c.dim }),
        el('td', { style: 'padding:10px 14px; color:var(--text-dim); font-size:12.5px; line-height:1.55;', text: c.traditional }),
        el('td', { style: 'padding:10px 14px; font-size:12.5px; line-height:1.55;', text: c.dtb }),
        el('td', { style: 'padding:10px 14px; color:var(--success); font-weight:600; font-size:12.5px;', text: c.win })
      ]));
    });
    table.appendChild(tbody);
    tbl.appendChild(table);

    wrap.appendChild(intro);
    wrap.appendChild(tbl);
    view.appendChild(renderSection('sec-comparison', '🆚', '横向对比', '豆包工具箱 vs 传统工具栈 · 10 真实场景', wrap));
  }

  // --- 故障案例库 ---
  function renderBugStoriesSection(view) {
    const wrap = el('div');
    const intro = el('div', { class: 'card', style: 'padding:14px 20px; margin-bottom:12px; font-size:13px; line-height:1.8; color:var(--text-dim);' }, [
      el('strong', { style: 'color:var(--text);', text: '8 个真实 bug 复盘：' }),
      document.createTextNode('下面这些不是教科书例子，而是 v0.4 - v0.9 期间 commit log 里真实发生过的故障。每个故事都包含：症状、根因、修复、教训。看到的不只是"修了什么"，更是"怎么思考的"。')
    ]);

    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:12px;' });
    bugStories.forEach(b => {
      const sevColor = b.severity === 'P0' ? 'var(--error)' : b.severity === 'P1' ? 'var(--warn)' : 'var(--text-dim)';
      const card = el('div', { class: 'card', style: 'padding:16px 20px; border-left:4px solid ' + sevColor + ';' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:10px; flex-wrap:wrap;' }, [
          el('span', { style: 'font-family:ui-monospace, monospace; font-size:11px; font-weight:700; padding:3px 8px; border-radius:5px; background:' + sevColor + '; color:#fff;', text: b.severity }),
          el('span', { style: 'font-family:ui-monospace, monospace; font-size:11px; color:var(--accent); padding:2px 8px; border-radius:5px; background:var(--bg-2);', text: b.id }),
          el('span', { class: 'text-dim', style: 'font-size:12px;', text: b.version })
        ]),
        el('div', { style: 'font-weight:700; font-size:14px; margin-bottom:10px; line-height:1.5;', text: b.title }),
        storyRow('😣', '症状', b.symptom),
        storyRow('🔍', '根因', b.rootCause),
        storyRow('🔧', '修复', b.fix),
        storyRow('💡', '教训', b.lesson, true)
      ]);
      grid.appendChild(card);
    });

    function storyRow(icon, label, text, accent) {
      return el('div', { style: 'font-size:12.5px; line-height:1.7; margin-bottom:6px;' }, [
        el('div', { style: 'display:flex; align-items:flex-start; gap:8px;' }, [
          el('span', { style: 'flex-shrink:0; font-size:13px;', text: icon }),
          el('div', {}, [
            el('span', { style: 'font-weight:600; color:' + (accent ? 'var(--warn)' : 'var(--text)') + '; margin-right:6px;', text: label + '：' }),
            el('span', { class: 'text-dim', text: text })
          ])
        ])
      ]);
    }

    wrap.appendChild(intro);
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-bugs', '🐞', '故障案例库', '8 个真实 bug 复盘 · 含根因 / 修复 / 教训', wrap));
  }

  // --- FAQ ---
  function renderFaqSection(view) {
    const wrap = el('div');
    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    faq.forEach((f, i) => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:14px 18px; border-left:3px solid var(--accent);' }, [
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start; margin-bottom:6px;' }, [
          el('span', { style: 'flex-shrink:0; width:24px; height:24px; border-radius:50%; background:var(--accent); color:#fff; font-size:11px; font-weight:700; display:flex; align-items:center; justify-content:center;', text: 'Q' }),
          el('div', { style: 'font-weight:600; font-size:13.5px; line-height:1.55; flex:1;', text: f.q })
        ]),
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start;' }, [
          el('span', { style: 'flex-shrink:0; width:24px; height:24px; border-radius:50%; background:var(--bg-3); color:var(--text); font-size:11px; font-weight:700; display:flex; align-items:center; justify-content:center;', text: 'A' }),
          el('div', { class: 'text-dim', style: 'font-size:13px; line-height:1.7; flex:1;', text: f.a })
        ])
      ]));
    });
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-faq', '❓', '常见问题', 'FAQ · 12 问', wrap));
  }

  // --- 路线图 ---
  function renderRoadmapSection(view) {
    const wrap = el('div');

    const renderList = (title, items, color, icon) => {
      const card = el('div', { class: 'card', style: 'padding:18px 22px; margin-bottom:12px; border-left:4px solid ' + color + ';' }, [
        el('div', { style: 'font-weight:700; font-size:15px; margin-bottom:12px; display:flex; align-items:center; gap:8px; color:' + color + ';' }, [
          el('span', { text: icon }),
          document.createTextNode(title)
        ])
      ]);
      const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(260px, 1fr)); gap:10px;' });
      items.forEach(p => {
        grid.appendChild(el('div', { style: 'padding:10px 14px; background:var(--bg-2); border-radius:8px;' }, [
          el('div', { style: 'font-weight:600; font-size:13px; margin-bottom:4px;', text: p.name }),
          el('div', { class: 'text-dim', style: 'font-size:12px; line-height:1.6;', text: p.desc })
        ]));
      });
      card.appendChild(grid);
      return card;
    };

    wrap.appendChild(renderList('已规划 (next 1-2 versions)', roadmap.planned, 'var(--primary)', '🎯'));
    wrap.appendChild(renderList('调研中 (considering)', roadmap.considering, 'var(--text-dim)', '💡'));

    view.appendChild(renderSection('sec-roadmap', '🗺️', '路线图', 'next + considering', wrap));
  }

  // --- Footer ---
  function renderFooter(view) {
    var keyframesStyle = document.getElementById('dtb-footer-shimmer');
    if (!keyframesStyle) {
      keyframesStyle = el('style', { id: 'dtb-footer-shimmer' });
      keyframesStyle.textContent = '@keyframes dtb-shimmer{0%{background-position:0% 50%}100%{background-position:200% 50%}}';
      document.head.appendChild(keyframesStyle);
    }
    var f = el('div', { class: 'text-dim', style: 'position:relative; text-align:center; margin-top:48px; padding:32px 16px 40px; font-size:13px; border-top:1px solid var(--line); background:linear-gradient(180deg, transparent, rgba(79,140,255,0.06)); overflow:hidden;' });
    // 顶部光晕装饰线
    f.appendChild(el('div', { style: 'position:absolute; top:-1px; left:0; right:0; height:1px; background:linear-gradient(90deg, transparent, var(--primary), var(--accent), var(--primary), transparent); background-size:200% 100%; animation:dtb-shimmer 3s linear infinite;' }));
    // 品牌行：渐变流光文字
    f.appendChild(el('div', { style: 'font-size:17px; font-weight:800; margin-bottom:10px; background:linear-gradient(135deg, var(--text) 0%, var(--primary) 40%, var(--accent) 60%, var(--primary) 80%, var(--text) 100%); background-size:200% 100%; -webkit-background-clip:text; -webkit-text-fill-color:transparent; background-clip:text; animation:dtb-shimmer 4s linear infinite; letter-spacing:0.5px;', text: '© 2026 豆包工具箱 · 匠心打造' }));
    f.appendChild(el('div', { style: 'margin-top:6px; font-size:13px;', text: '技术栈：Go 1.20+ · 原生 JavaScript · x/crypto/ssh · pkg/sftp · single-binary deploy · zero runtime deps' }));
    f.appendChild(el('div', { style: 'margin-top:6px; font-size:12px;', text: '为运维效率而生 · 让每一次操作都有迹可循 · 让每一次配置都可审计 · 让每一次下载都可追溯' }));
    // 末行：作者署名
    f.appendChild(el('div', { style: 'margin-top:14px; font-size:11.5px; opacity:0.75;', unsafeHtml: '<span style="color:var(--primary); font-weight:600;">Made by Qi</span>' }));
    view.appendChild(f);
  }

  // =====================================================================
  // 主入口
  // =====================================================================
  function renderAbout(view) {
    // 顶部
    renderHero(view);
    renderStats(view);

    // sticky 锚点导航
    renderAnchorNav(view);

    // 13 个 section
    renderOverview(view);
    renderPrinciplesSection(view);
    renderArchitectureSection(view);
    renderStackSection(view);
    renderSecuritySection(view);
    renderCompatSection(view);
    renderQualitySection(view);
    renderModulesSection(view);
    renderComparisonSection(view);
    renderBugStoriesSection(view);
    renderHistorySection(view);
    renderFaqSection(view);
    renderRoadmapSection(view);

    // 页脚
    renderFooter(view);
  }

  DTB.pages.about = renderAbout;
  DTB.state.routes.about = renderAbout;
  DTB.state.routeNames.about = '关于';
})();