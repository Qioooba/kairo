/* ===== web/pages/ssh.js =====
 * SSH 终端（v0.10）：在浏览器里开交互式 shell，类似 Xshell 的 Web 版。
 *
 * 设计要点（docs/SSH-TERMINAL-DESIGN.md）：
 *   - 左侧：业务系统 / 服务器列表（从 /api/config 拉）
 *   - 右侧：tab 栏 + xterm.js 终端区
 *   - 每个 tab = 1 个 WebSocket → /api/ssh/shell/ws，1:1 对应一个 SSH shell
 *   - 协议：binary frame = 字节流透传，text frame = JSON 控制（resize/signal/ping）
 *   - 凭据：WS 不接受前端传 password；server 端 resolveCreds 失败时发 no_password error，
 *     前端弹一次性密码框，用户输入后 POST /api/credentials/save，再重连 WS
 *   - 主题联动：监听 kairo:themechange 事件，同步 xterm 主题
 *   - 离开页面：clearActiveShells → closeAll 关闭所有 WS + dispose 所有 terminal
 *
 * 安全：
 *   - 用户主动连自己的机器，不违反 README「禁止任意命令执行」约束；
 *   - 审计由后端写 start/end（不含命令内容）；
 *   - 密码不经过 WS upgrade 帧，只走 /api/credentials/save（HTTPS 同源 POST）。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, $, toast, cssEscape, escapeHtml, formatBytes } = Kairo.core;
  const { api } = Kairo.api;

  const ICONS_SVG = {
    smFolder:    'M2 5a2 2 0 012-2h5l2 2h9a2 2 0 012 2v11a2 2 0 01-2 2H4a2 2 0 01-2-2V5z',
    smClipboard: 'M9 2h6a2 2 0 012 2v16a2 2 0 01-2 2H9a2 2 0 01-2-2V4a2 2 0 012-2zm0 2v2h6V4zM8 12h8M8 16h8M8 8h4',
    smScroll:    'M8 3h8M6 7h12M6 11h12M6 15h12M6 19h12M4 2a2 2 0 00-2 2v16a2 2 0 002 2h16a2 2 0 002-2V4a2 2 0 00-2-2z',
    smRefresh:   'M23 4v6h-6M1 20v-6h6M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15',
    smHome:      'M3 9l9-7 9 7v11a2 2 0 01-2 2H5a2 2 0 01-2-2z M9 22V12h6v10',
  };
  function svgIcon(name, size) {
    const d = ICONS_SVG[name];
    if (!d) return '';
    const s = size || 14;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

  const DEFAULT_COLS = 80;
  const DEFAULT_ROWS = 24;

  // xterm 5 套主题，跟 Kairo 主题（dark/light/green/hc/xianxia）联动
  const XTERM_THEMES = {
    dark: {
      background: '#0f172a', foreground: '#e2e8f0', cursor: '#e2e8f0',
      selectionBackground: '#334155', black: '#1e293b', red: '#f87171',
      green: '#4ade80', yellow: '#facc15', blue: '#60a5fa', magenta: '#c084fc',
      cyan: '#22d3ee', white: '#e2e8f0', brightBlack: '#64748b', brightRed: '#fca5a5',
      brightGreen: '#86efac', brightYellow: '#fde047', brightBlue: '#93c5fd',
      brightMagenta: '#d8b4fe', brightCyan: '#67e8f9', brightWhite: '#f8fafc'
    },
    light: {
      background: '#f8fafc', foreground: '#1e293b', cursor: '#1e293b',
      selectionBackground: '#cbd5e1', black: '#1e293b', red: '#dc2626',
      green: '#16a34a', yellow: '#ca8a04', blue: '#2563eb', magenta: '#9333ea',
      cyan: '#0891b2', white: '#f1f5f9', brightBlack: '#64748b', brightRed: '#ef4444',
      brightGreen: '#22c55e', brightYellow: '#eab308', brightBlue: '#3b82f6',
      brightMagenta: '#a855f7', brightCyan: '#06b6d4', brightWhite: '#ffffff'
    },
    green: {
      background: '#0c1a0c', foreground: '#c8e6c9', cursor: '#a5d6a7',
      selectionBackground: '#1b3a1b', black: '#0c1a0c', red: '#ef9a9a',
      green: '#81c784', yellow: '#fff59d', blue: '#90caf9', magenta: '#ce93d8',
      cyan: '#80deea', white: '#c8e6c9', brightBlack: '#4e6e4e', brightRed: '#ffcdd2',
      brightGreen: '#a5d6a7', brightYellow: '#fff9c4', brightBlue: '#bbdefb',
      brightMagenta: '#e1bee7', brightCyan: '#b2ebf2', brightWhite: '#e8f5e9'
    },
    hc: {
      background: '#000000', foreground: '#ffffff', cursor: '#ffffff',
      selectionBackground: '#444444', black: '#000000', red: '#ff0000',
      green: '#00ff00', yellow: '#ffff00', blue: '#0000ff', magenta: '#ff00ff',
      cyan: '#00ffff', white: '#ffffff', brightBlack: '#666666', brightRed: '#ff6666',
      brightGreen: '#66ff66', brightYellow: '#ffff66', brightBlue: '#6666ff',
      brightMagenta: '#ff66ff', brightCyan: '#66ffff', brightWhite: '#ffffff'
    },
    xianxia: {
      background: '#f5f8f7', foreground: '#1a2320', cursor: '#4a7a6a',
      selectionBackground: 'rgba(74,122,106,0.2)', black: '#2a3530', red: '#a82820',
      green: '#5a8e4c', yellow: '#b8923e', blue: '#4a6a8a', magenta: '#7a5a8a',
      cyan: '#4a7a6a', white: '#5a6a60', brightBlack: '#7a8a80', brightRed: '#c84840',
      brightGreen: '#7aac6c', brightYellow: '#d8b25e', brightBlue: '#6a8aaa',
      brightMagenta: '#9a7aac', brightCyan: '#6a9a8a', brightWhite: '#1a2320'
    }
  };

  function currentXtermTheme() {
    const t = (Kairo.theme && Kairo.theme.get) ? Kairo.theme.get() : 'dark';
    return XTERM_THEMES[t] || XTERM_THEMES.dark;
  }

  function getTerminalCtor() {
    return window.Terminal;
  }
  function getFitAddonCtor() {
    return window.FitAddon && window.FitAddon.FitAddon;
  }
  function getSearchAddonCtor() {
    return window.SearchAddon && window.SearchAddon.SearchAddon;
  }
  function getWebLinksAddonCtor() {
    return window.WebLinksAddon && window.WebLinksAddon.WebLinksAddon;
  }

  function renderSSH(view, routeState, scope) {
    const pageState = {
      cfg: null,
      tabs: [],
      activeTabId: null,
      nextId: 1,
      disposed: false,
      filter: '',  // Bug#18 主机列表过滤
    };
    // Bug#19：tab 持久化的 localStorage key（设计文档 §5.2）。
    // 必须在 renderSSH 顶部就声明 —— 下面 loadConfig → tryRestoreTabs → restoreTabs
    // 会同步访问 TABS_KEY；如果把 const TABS_KEY 放太下面（比如定义在 restoreTabs 之前），
    // 同步执行链会撞上 const 的 TDZ（暂时性死区），restoreTabs 抛 ReferenceError，
    // try/catch 静默吃掉，tab 永远恢复不出来 —— 用户看到的现象是"刷新页面 tab 没了"。
    const TABS_KEY = 'kairo:ssh:tabs';

    // ---- 布局 ----
    const pageEl = el('div', { class: 'ssh-page' });

    const sidebarEl = el('div', { class: 'ssh-sidebar' });
    sidebarEl.appendChild(el('div', { class: 'ssh-sidebar-title', text: '主机列表' }));
    // Bug#18：主机列表过滤框（设计文档 §5.2）。子串匹配 system/server/host。
    const filterWrap = el('div', { class: 'ssh-filter-wrap' });
    const filterInput = el('input', {
      type: 'text', class: 'ssh-filter-input', placeholder: '过滤主机（子串）',
      title: '按系统名 / 服务器名 / 主机名/IP 子串过滤', autocomplete: 'off'
    });
    filterInput.addEventListener('input', function () {
      pageState.filter = filterInput.value || '';
      renderHostList();
    });
    // Esc 清空过滤
    filterInput.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { filterInput.value = ''; pageState.filter = ''; renderHostList(); filterInput.blur(); }
    });
    filterWrap.appendChild(filterInput);
    sidebarEl.appendChild(filterWrap);
    const hostListEl = el('div', { class: 'ssh-host-list' });
    sidebarEl.appendChild(hostListEl);
    const sidebarHint = el('div', { class: 'ssh-sidebar-hint text-dim', text: '点击主机开 shell · 双击重复打开 · Ctrl+Shift+F 搜索终端' });
    sidebarEl.appendChild(sidebarHint);

    const mainEl = el('div', { class: 'ssh-main' });

    const tabBarEl = el('div', { class: 'ssh-tabbar' });
    const toolbarEl = el('div', { class: 'ssh-toolbar' });
    const termAreaEl = el('div', { class: 'ssh-term-area' });
    // v0.11+：SSH 终端 + SFTP 二合一面板（默认隐藏，工具栏 toggle 显示）
    // 设计：FinalShell 风格（上终端 / 下文件），固定 40% 高度，每 tab 独立 SFTP 状态
    const filesPanelEl = el('div', { class: 'ssh-files-panel', style: 'display:none' });

    // v1.4+：SFTP 面板拖拽上传（DnD）。
    // 监听器挂在 filesPanelEl 上（容器常驻，不随 renderSftpPanelContent 重建），
    // 拖拽深度计数避免子元素 dragenter/dragleave 抖动；drop 后走 addUploadFiles。
    let sftpDragDepth = 0;
    let sftpDropHintEl = null;
    function sftpHasFiles(e) {
      return !!(e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files'));
    }
    function sftpShowDropHint() {
      if (!sftpDropHintEl) {
        sftpDropHintEl = el('div', { class: 'sftp-drop-hint' });
        sftpDropHintEl.appendChild(el('div', {
          unsafeHtml: svgIcon('smFolder', 26) + '<div style="margin-top:6px;font-weight:600;">松手上传到当前目录</div>'
        }));
      }
      if (!sftpDropHintEl.parentNode) filesPanelEl.appendChild(sftpDropHintEl);
    }
    function sftpHideDropHint() {
      if (sftpDropHintEl && sftpDropHintEl.parentNode) sftpDropHintEl.remove();
    }
    filesPanelEl.addEventListener('dragenter', function (e) {
      if (!sftpHasFiles(e)) return;
      e.preventDefault();
      e.stopPropagation();
      sftpDragDepth++;
      sftpShowDropHint();
    });
    filesPanelEl.addEventListener('dragover', function (e) {
      if (!sftpHasFiles(e)) return;
      e.preventDefault();
      e.stopPropagation();
      if (e.dataTransfer) {
        try { e.dataTransfer.dropEffect = 'copy'; } catch (err) { /* ignore */ }
      }
    });
    filesPanelEl.addEventListener('dragleave', function (e) {
      if (!sftpHasFiles(e)) return;
      sftpDragDepth = Math.max(0, sftpDragDepth - 1);
      if (sftpDragDepth === 0) sftpHideDropHint();
    });
    filesPanelEl.addEventListener('drop', function (e) {
      if (!sftpHasFiles(e) || !e.dataTransfer.files || !e.dataTransfer.files.length) return;
      e.preventDefault();
      e.stopPropagation();
      sftpDragDepth = 0;
      sftpHideDropHint();
      const tab = getActiveTab();
      if (!tab || tab.closed) { toast('当前没有可用的 SSH tab', 'warn'); return; }
      addUploadFiles(tab, e.dataTransfer.files);
    });

    const emptyHint = el('div', { class: 'ssh-empty' }, [
      el('div', { class: 'ssh-empty-icon', text: '>' }),
      el('div', { class: 'ssh-empty-text', text: '从左侧选择一台主机开始 SSH 会话' })
    ]);
    termAreaEl.appendChild(emptyHint);

    mainEl.appendChild(tabBarEl);
    mainEl.appendChild(toolbarEl);
    mainEl.appendChild(termAreaEl);
    mainEl.appendChild(filesPanelEl);

    pageEl.appendChild(sidebarEl);
    pageEl.appendChild(mainEl);
    view.appendChild(pageEl);

    // ---- 工具栏按钮 ----
    const btnCtrlC = el('button', { class: 'btn btn-sm', text: 'Ctrl+C', title: '发送 SIGINT', onclick: sendCtrlC, disabled: true });
    // v0.13+：终端背景模式切换（跟随主题 / 强制深色）
    // 用 svg + 文本 span 替代原来的 🌓 / 🌙 emoji（Win 7 无字体支持会显示成方框）。
    const btnBgToggle = el('button', {
      class: 'btn btn-sm ssh-bg-toggle',
      title: '切换终端背景：跟随主题 / 强制深色', onclick: toggleBgMode
    });
    btnBgToggle.appendChild(el('span', { class: 'bg-ico' }));
    btnBgToggle.appendChild(el('span', { class: 'bg-label', text: '跟随主题' }));
    const btnClear = el('button', { class: 'btn btn-sm', text: '清屏', title: '清屏（clear）', onclick: clearActive, disabled: true });
    const btnReconnect = el('button', { class: 'btn btn-sm', text: '重连', title: '断开重连', onclick: reconnectActive, disabled: true });
    // 原来用 🔍 emoji，Win 7 无字体支持；改成 icons.search SVG（lucide-style 放大镜）
const btnSearch = el('button', {
  class: 'btn btn-sm', title: '在终端输出中搜索（Ctrl+Shift+F）',
  onclick: searchInTerminal, disabled: true
});
btnSearch.appendChild((Kairo.icons && Kairo.icons.svg) ? Kairo.icons.svg('search', 14) : document.createTextNode('🔍'));
btnSearch.appendChild(el('span', { text: '搜索' }));
    const btnCloseTab = el('button', { class: 'btn btn-sm btn-danger', text: '关闭 tab', title: '关闭当前 tab', onclick: closeActiveTab, disabled: true });
    // v0.11+：[📁 文件] toggle 按钮 — 显示/隐藏底部 SFTP 文件面板
    // 行为：toggle .ssh-files-panel 显隐；记忆 localStorage（每 system:server 独立）
    // 首次显示时自动调 pwd 接口跳转到当前 SSH 终端的工作目录
    const btnToggleFiles = el('button', { class: 'btn btn-sm', text: '📁 文件', title: '显示/隐藏 SFTP 文件面板', onclick: toggleFilesPanel, disabled: true });
    const activeLabel = el('span', { class: 'ssh-active-label text-dim', text: '' });
    // 编码选择器：UTF-8 / GBK（老 WebSphere / Oracle 终端常见）
    // UI-修复：之前用 .btn .btn-sm 让它"看起来像按钮"，结果用户看不出来是下拉。
    // 改成专用 .ssh-encoding-sel 类，下拉箭头 + 边框都按 <select> 原生样式渲染。
    // 持久化：用户为某台机器选过 GBK 后，下次开同 (system, server) 的 tab 自动用 GBK。
    // 用 localStorage 存，key 形如 "ssh_encoding:<system>:<server>"，避免污染全局。
    const encodingSel = el('select', { class: 'ssh-encoding-sel', title: '终端输出编码（UTF-8 / GBK），切换后自动重连。已为当前主机记住选择。' });
    encodingSel.appendChild(el('option', { value: 'utf-8', text: 'UTF-8' }));
    encodingSel.appendChild(el('option', { value: 'gbk', text: 'GBK' }));
    encodingSel.addEventListener('change', function () {
      const tab = getActiveTab();
      // Bug#11：没 active tab 时编码切换应该给反馈（之前 silently noop）。
      // select.value 在 change 时已经变了，用户搞不清是生效了还是没生效。
      if (!tab) {
        toast('请先开一个 SSH tab 再切换编码', 'warn');
        // 还原 select 到 utf-8 默认（不要让 select 卡在一个没生效的值上）
        encodingSel.value = 'utf-8';
        return;
      }
      if (tab.closed) {
        // tab 已断开 — 改 encoding 但不立即重连（用户可能还没准备好重连）
        tab.encoding = encodingSel.value;
        try { localStorage.setItem('ssh_encoding:' + tab.system + ':' + tab.server, tab.encoding); } catch (_) { /* ignore */ }
        toast('已切换编码为 ' + (tab.encoding === 'gbk' ? 'GBK' : 'UTF-8') + '（下次连接生效）', 'idle');
        return;
      }
      tab.encoding = encodingSel.value;
      // 写 localStorage：key = ssh_encoding:<system>:<server>
      // 同一台机器（不管开多少 tab）共享一个偏好；删 user data 自动清空。
      try {
        localStorage.setItem('ssh_encoding:' + tab.system + ':' + tab.server, tab.encoding);
      } catch (_) { /* localStorage 不可用（隐私模式 / quota）不阻断流程 */ }
      // UI-修复：之前切换无声无息，用户看不到反馈。加 toast + 自动重连（终端会刷"重连中…"）。
      toast('已切换编码为 ' + (tab.encoding === 'gbk' ? 'GBK' : 'UTF-8') + '，正在重连…', 'idle');
      reconnectActive();
    });
    // 读持久化的 helper：在 openTab 创建 tab 时调，把 saved encoding 赋给 tab.encoding
    function readPersistedEncoding(system, server) {
      try {
        const v = localStorage.getItem('ssh_encoding:' + system + ':' + server);
        // 只接受 utf-8 / gbk，外部篡改的非法值忽略
        if (v === 'utf-8' || v === 'gbk') return v;
      } catch (_) { /* ignore */ }
      return null;
    }
    toolbarEl.appendChild(btnCtrlC);
    toolbarEl.appendChild(encodingSel);
    toolbarEl.appendChild(btnClear);
    toolbarEl.appendChild(btnReconnect);
    toolbarEl.appendChild(btnSearch);
    // Bug#22：顶部工具栏视觉分组 — 终端操作组 [Ctrl+C / 编码 / 清屏 / 重连 / 搜索]
    // 和 tab 操作组 [关闭 tab / 📁 文件 / ↗ 新窗口] 之间加分隔条。
    // 之前 8 个按钮挤一行，用户看不出哪个跟哪个是一组。
    toolbarEl.appendChild(el('span', { class: 'ssh-toolbar-sep' }));
    toolbarEl.appendChild(btnCloseTab);
    toolbarEl.appendChild(btnToggleFiles);
    // Bug#1 / Bug#16：↗新窗口按钮 — 把当前 active tab 拆到独立窗口（/ssh.html），
    // 复用 tail.html / preview.html 同款模式。设计文档 §5.2。
    // 注：原本用 ⛶ (U+26F6 SQUARE FOUR CORNERS) 在部分字体（如思源宋体、
    // Windows 中文系统默认字体）里显示成空心方框；改用更通用的 ↗ (U+2197)。
    const btnPopOut = el('button', {
      class: 'btn btn-sm', text: '↗ 新窗口', title: '把当前 tab 拆到独立窗口',
      onclick: popOutActive, disabled: true
    });
    toolbarEl.appendChild(btnPopOut);
    // v0.13+：背景切换按钮放工具栏最右（紧贴 activeLabel 左边）
    toolbarEl.appendChild(btnBgToggle);
    toolbarEl.appendChild(activeLabel);

    function updateToolbarButtons() {
      const tab = getActiveTab();
      const hasTab = !!tab;
      const hasActive = !!tab && !tab.closed;  // 已连接（用于 Ctrl+C / 清屏 / 文件面板）
      // 重连 / 关闭tab / 编码选择：有 tab 就可用（即使已断开）
      btnCtrlC.disabled = !hasActive;
      // 清屏只清 xterm 缓冲，跟 SSH 死活无关——连接失败时用户也想清掉错误信息
      btnClear.disabled = !hasTab;
      btnReconnect.disabled = !hasTab;
      btnSearch.disabled = !hasActive;
      btnCloseTab.disabled = !hasTab;
      btnToggleFiles.disabled = !hasActive;
      // Bug#1: 新窗口按钮 — 有 tab 就能拆（哪怕已断开，新窗口可独立重连）
      btnPopOut.disabled = !hasTab;
      // 同步编码选择器
      if (tab) {
        encodingSel.value = tab.encoding || 'utf-8';
        encodingSel.disabled = false;
      } else {
        encodingSel.value = 'utf-8';
        encodingSel.disabled = true;
      }
      // 同步 [📁 文件] 按钮激活态（按当前 tab 是否显示 SFTP 面板）
      if (tab && !tab.closed) {
        btnToggleFiles.classList.toggle('btn-active', !!tab.filesPanelVisible);
      } else {
        btnToggleFiles.classList.remove('btn-active');
      }
    }

    const owner = (scope && (scope.tabId || scope.owner)) || (view && view.getAttribute && view.getAttribute('data-tab')) || 'ssh';

    const shellController = {
      hasActive: function () {
        return pageState.tabs.some(function (t) { return t.ws && t.ws.readyState === 1 && !t.closed; });
      },
      closeAll: function () { closeAllTabs(); }
    };
    Kairo.core.setActiveShells(shellController, owner);

    // v1.4+：注册上传 controller 到 Kairo.core（与 files.js 同机制）。
    // 路由切走（app.js navigate）时 core 调 cancelAll 取消所有 tab 上传；
    // beforeunload 时走 cancelAllBeacon（sendBeacon 异步通知后端）。
    const uploadController = {
      hasActive: function () {
        return pageState.tabs.some(function (t) {
          return (t.uploadQueue || []).some(function (u) { return u.status === 'uploading' || u.status === 'pending'; });
        });
      },
      cancelAll: function () { cancelAllTabsUploads(); },
      cancelAllBeacon: function () { cancelAllTabsUploads(); }
    };
    Kairo.core.setActiveUploads(uploadController, owner);

    // ===== v0.13+ 终端背景模式切换（跟随主题 / 强制深色） =====
    const BG_MODE_KEY = 'kairo_ssh_bg_mode';
    let bgMode = (function () {
      try {
        const v = localStorage.getItem(BG_MODE_KEY);
        if (v === 'dark' || v === 'theme') return v;
      } catch (_) {}
      return 'theme';
    })();
    // 强制深色时覆盖当前主题；其他情况跟随主题
    function effectiveXtermTheme() {
      return bgMode === 'dark' ? XTERM_THEMES.dark : currentXtermTheme();
    }
    function applyBgMode() {
      const theme = effectiveXtermTheme();
      // 同步所有 tab 的 xterm 内部主题
      pageState.tabs.forEach(function (tab) {
        if (tab.term && !tab.closed) {
          try { tab.term.options.theme = theme; } catch (e) { /* ignore */ }
        }
      });
      // 同步外面 termAreaEl 的背景（让未连上的区域也是对应色）
      if (termAreaEl && theme.background) {
        termAreaEl.style.background = theme.background;
      }
    }
    function paintBgToggleBtn() {
      // 替换原来的 emoji：用 Kairo.icons 生成 sun/moon SVG
      const icoBox = btnBgToggle.querySelector('.bg-ico');
      const labelBox = btnBgToggle.querySelector('.bg-label');
      const moonSvg = (Kairo.icons && Kairo.icons.svg) ? Kairo.icons.svg('moon', 14) : null;
      const sunSvg = (Kairo.icons && Kairo.icons.svg) ? Kairo.icons.svg('sun', 14) : null;
      if (bgMode === 'dark') {
        if (icoBox) { icoBox.innerHTML = ''; if (moonSvg) icoBox.appendChild(moonSvg); }
        if (labelBox) labelBox.textContent = '强制深色';
        btnBgToggle.title = '当前：强制深色。点击切换回跟随主题';
        btnBgToggle.classList.add('is-dark');
      } else {
        if (icoBox) { icoBox.innerHTML = ''; if (sunSvg) icoBox.appendChild(sunSvg); }
        if (labelBox) labelBox.textContent = '跟随主题';
        btnBgToggle.title = '当前：跟随主题。点击切换到强制深色（适合亮色主题对比度不够的场景）';
        btnBgToggle.classList.remove('is-dark');
      }
    }
    function toggleBgMode() {
      bgMode = (bgMode === 'dark') ? 'theme' : 'dark';
      try { localStorage.setItem(BG_MODE_KEY, bgMode); } catch (_) {}
      applyBgMode();
      paintBgToggleBtn();
    }

    // v0.13+：光标行高亮 marker（左侧主题色条，跟踪光标所在行）
    // xterm.js 没有"输入行 vs 输出行"语义（输入输出同流），
    // 退而求其次在光标所在行左侧画主题色条 + 柔光，视觉锚定"正在编辑的行"。
    // 通过 rAF 读 buffer.cursorY / viewportY 算位置，自动跟 buffer 滚动。
    function setupCursorMarker(tab, term) {
      const xtermEl = term.element;  // .xterm 容器
      if (!xtermEl) return;
      const marker = document.createElement('div');
      marker.className = 'ssh-cursor-marker';
      xtermEl.appendChild(marker);
      let rafId = 0;
      function tick() {
        rafId = requestAnimationFrame(tick);
        if (tab.closed) return;
        let buf, cell;
        try {
          buf = term.buffer && term.buffer.active;
          const dim = term._core && term._core._renderService && term._core._renderService.dimensions;
          cell = dim && dim.css && dim.css.cell;
        } catch (_) { return; }
        if (!buf || !cell || !cell.height) {
          marker.classList.remove('visible');
          return;
        }
        const cursorY = (typeof buf.cursorY === 'number') ? buf.cursorY : -1;
        const viewportY = (typeof buf.viewportY === 'number') ? buf.viewportY : 0;
        const screenY = cursorY - viewportY;
        // xterm 内部 padding: 4px；用 css cell 尺寸精确定位
        marker.style.top = (4 + screenY * cell.height) + 'px';
        marker.style.height = cell.height + 'px';
        if (screenY < 0 || screenY >= term.rows) {
          marker.classList.remove('visible');
        } else {
          marker.classList.add('visible');
        }
      }
      rafId = requestAnimationFrame(tick);
      tab.cursorMarkerRaf = function () { return rafId; };
      tab.cursorMarkerStop = function () {
        if (rafId) { cancelAnimationFrame(rafId); rafId = 0; }
      };
    }

    // ---- 主题联动 ----
    function onThemeChange() {
      // 背景模式 = dark 时强制保持深色（不跟随主题）；其他情况跟随主题
      applyBgMode();
    }
    window.addEventListener('kairo:themechange', onThemeChange);
    // Tab 保活：外层 Tab 从隐藏切回显示时，xterm 画布在 display:none 期间尺寸为 0，
    // 必须 refit 否则终端显示错乱。复用内部 activateTab 的 fit 逻辑（只 fit 不改 active）。
    function onOuterTabShow(ev) {
      if (!ev || !ev.detail || ev.detail.route !== 'ssh') return;
      if (pageState.disposed) return;
      const tab = getActiveTab();
      if (tab && tab.fitAddon && !tab.closed) {
        setTimeout(function () {
          if (pageState.disposed || tab.closed) return;
          try { tab.fitAddon.fit(); } catch (e) { /* ignore */ }
          if (tab.term) {
            tab.cols = tab.term.cols || tab.cols;
            tab.rows = tab.term.rows || tab.rows;
          }
        }, 30);
      }
    }
    window.addEventListener('kairo:tab-show', onOuterTabShow);
    // Ctrl+Shift+F：终端搜索快捷键（仅 SSH 页面激活时生效）
    // 具名函数：Tab 关闭时在 closeAllTabs 里移除，避免每次重开叠加一个监听。
    function onGlobalKeydown(e) {
      if (e.ctrlKey && e.shiftKey && (e.key === 'F' || e.key === 'f')) {
        // 只在 SSH 路由激活时响应（避免拦截其他页面的搜索）
        const route = (Kairo.state && Kairo.state.currentRoute) || '';
        if (route !== 'ssh') return;
        e.preventDefault();
        searchInTerminal();
      }
    }
    window.addEventListener('keydown', onGlobalKeydown);
    applyBgMode();
    updateToolbarButtons();
    paintBgToggleBtn();

    loadConfig();

    function loadConfig() {
      api('GET', '/api/admin/openers').then(r => {
        const prevLen = (Kairo.state.downloadsOpeners || []).length;
        Kairo.state.downloadsOpeners = Array.isArray(r && r.openers) ? r.openers : [];
        if (Kairo.state.downloadsOpeners.length !== prevLen) {
          refreshActiveSftpList();
        }
      }).catch(() => {
        // 网络抖动时保留旧数据：避免编辑按钮异常禁用。
      });

      const cached = Kairo.state && Kairo.state.bootInfo;
      if (cached && cached.systems) {
        pageState.cfg = cached;
        renderHostList();
        tryRestoreTabs();
        return;
      }
      api('GET', '/api/config').then(function (info) {
        pageState.cfg = info;
        renderHostList();
        tryRestoreTabs();
      }).catch(function (e) {
        hostListEl.innerHTML = '';
        hostListEl.appendChild(el('div', { class: 'text-err', text: '加载配置失败：' + (e.message || e) }));
      });
    }

    // Bug#19：config 加载完后尝试恢复 tab。包一层防止 restoreTabs 抛错炸页面。
    // 关键：catch 里把异常打到 console —— 之前静默吞错，用户看到"tab 没恢复"找不出原因，
    // 排查时只能靠猜。把错误打 console 才能定位 TABS_KEY TDZ 这种诡异 bug。
    function tryRestoreTabs() {
      try {
        restoreTabs();
      } catch (e) {
        console.error('[ssh] restoreTabs failed:', e && (e.stack || e.message || e));
        clearPersistedTabs();
      }
    }

    function renderHostList() {
      hostListEl.innerHTML = '';
      const cfg = pageState.cfg;
      if (!cfg || !cfg.systems || !cfg.systems.length) {
        hostListEl.appendChild(el('div', { class: 'text-dim', text: '没有配置任何系统，请先去「系统配置」添加' }));
        return;
      }
      const filter = (pageState.filter || '').toLowerCase();
      cfg.systems.forEach(function (sys) {
        // 过滤：name / server.name / server.host 任一命中即保留
        const matchedServers = (sys.servers || []).filter(function (srv) {
          if (!filter) return true;
          return (sys.name + ' ' + srv.name + ' ' + srv.host).toLowerCase().indexOf(filter) >= 0;
        });
        if (filter && matchedServers.length === 0) return; // 整组无匹配则隐藏

        const sysGroup = el('div', { class: 'ssh-sys-group' });
        // 系统组折叠状态：默认展开；按 system name 持久化
        const collapseKey = 'ssh_sys_collapsed:' + sys.name;
        let collapsed = false;
        try { collapsed = localStorage.getItem(collapseKey) === '1'; } catch (_) { /* ignore */ }
        const sysHeader = el('div', {
          class: 'ssh-sys-header' + (collapsed ? ' ssh-sys-collapsed' : ''),
          title: '点击折叠 / 展开'
        }, [
          el('span', { class: 'ssh-sys-caret', text: collapsed ? '▸' : '▾' }),
          el('span', { class: 'ssh-sys-name', text: sys.name }),
          el('span', { class: 'ssh-sys-count text-dim', text: '(' + matchedServers.length + ')' })
        ]);
        sysHeader.addEventListener('click', function () {
          collapsed = !collapsed;
          sysHeader.classList.toggle('ssh-sys-collapsed', collapsed);
          sysHeader.querySelector('.ssh-sys-caret').textContent = collapsed ? '▸' : '▾';
          srvListWrap.style.display = collapsed ? 'none' : '';
          try { localStorage.setItem(collapseKey, collapsed ? '1' : '0'); } catch (_) { /* ignore */ }
        });
        sysGroup.appendChild(sysHeader);

        const srvListWrap = el('div', { class: 'ssh-srv-list', style: collapsed ? 'display:none' : '' });
        if (matchedServers.length === 0) {
          srvListWrap.appendChild(el('div', { class: 'ssh-srv-item text-dim', text: '（无服务器）' }));
          hostListEl.appendChild(sysGroup);
          return;
        }
        matchedServers.forEach(function (srv) {
          const srvItem = el('div', {
            class: 'ssh-srv-item',
            title: srv.host + ':' + (srv.port || 22) + ' (' + (srv.username || '-') + ')',
            // Bug#17：双击主机"新建 tab（即使已存在也复制）"—— 设计文档 §5.2
            onclick: function () { openTab(sys, srv); },
            ondblclick: function () { openTab(sys, srv, true /* forceNew */); }
          }, [
            el('span', { class: 'ssh-srv-name', text: srv.name }),
            el('span', { class: 'ssh-srv-host text-dim', text: srv.host + ':' + (srv.port || 22) })
          ]);
          srvListWrap.appendChild(srvItem);
        });
        sysGroup.appendChild(srvListWrap);
        hostListEl.appendChild(sysGroup);
      });
      if (!hostListEl.children.length) {
        hostListEl.appendChild(el('div', { class: 'text-dim ssh-no-match', text: '没有匹配的主机' }));
      }
    }

    // ---- 开 tab ----
    function openTab(sys, srv, forceNew) {
      if (!forceNew) {
        const existing = pageState.tabs.find(function (t) {
          return t.system === sys.name && t.server === srv.name && !t.closed;
        });
        if (existing) {
          activateTab(existing.id);
          return;
        }
      }

      const TerminalCtor = getTerminalCtor();
      if (!TerminalCtor) {
        toast('xterm.js 未加载，请刷新页面重试', 'err');
        return;
      }

      const tabId = pageState.nextId++;

      const termEl = el('div', { class: 'ssh-term', 'data-tab': String(tabId) });

      const sameCount = pageState.tabs.filter(function (t) {
        return t.system === sys.name && t.server === srv.name && !t.closed;
      }).length;
      const tabTitle = sameCount > 0 ? srv.name + ' (' + (sameCount + 1) + ')' : srv.name;

      const tabBtn = el('div', {
        class: 'ssh-tab ssh-tab-state-idle',
        'data-tab': String(tabId),
        onclick: function () { activateTab(tabId); }
      }, [
        // Bug#4：tab 状态点（5 状态：idle/connecting/connected/closed/err）
        el('span', { class: 'ssh-tab-status-dot', title: '未连接' }),
        el('span', { class: 'ssh-tab-name', text: tabTitle }),
        el('span', { class: 'ssh-tab-close', text: '×', title: '关闭', onclick: function (e) { e.stopPropagation(); closeTab(tabId); } })
      ]);

      const tab = {
        id: tabId,
        system: sys.name,
        server: srv.name,
        systemName: sys.name,
        serverName: srv.name,
        host: srv.host,
        port: srv.port || 22,
        username: srv.username,
        // 终端编码：优先用 localStorage 里为这台机器记下的偏好，否则默认 utf-8。
        // 切换编码时（见 encodingSel change handler）会回写 localStorage。
        encoding: readPersistedEncoding(sys.name, srv.name) || 'utf-8',
        term: null,
        fitAddon: null,
        searchAddon: null,
        ws: null,
        termEl: termEl,
        tabBtn: tabBtn,
        closed: false,
        reconnecting: false,
        // Bug#4：tab 状态点用的错误 reason（区分用户主动 close vs 连接失败）
        lastErrorReason: '',
        cols: DEFAULT_COLS,
        rows: DEFAULT_ROWS,
        resizeObs: null,
        _pwdOverlay: null,
        // v0.11+ SFTP 面板状态（每 tab 独立）
        filesPanelVisible: false,   // 是否显示 SFTP 面板
        sftpCwd: '/',                // 当前 SFTP 路径
        sftpSelected: new Set(),    // 选中文件名（多选）
        sftpBackend: '',            // 'sftp' | 'shell'（兜底），用于显示徽章
        sftpLoading: false,         // 列表加载中
        sftpDlEvtSrc: null,         // 当前下载 SSE EventSource
        sftpDlId: null,             // 当前下载任务 id
        sftpDlProgress: null,       // 下载进度 {total, done, current, fileWritten, fileTotal}
        sftpPanelInited: false,     // 面板 DOM 是否已初始化
        sftpCwdQueryId: null,       // 当前 cwd 查询 ID（用于 WS 响应匹配）
        sftpCwdQueryTimer: null,    // cwd 查询超时定时器
        sftpLastDlNotifyId: null,   // 上次下载通知 ID（去重）
        // v1.4+ SFTP 上传状态（每 tab 独立，DnD + 文件选择共用）
        sftpUploadQueue: [],        // 上传队列：{name,size,status,progress,bytes,error,uploadId,finalName,overwrite,file,xhr}
        sftpUploadInflight: false,  // 串行上传：一次只传一个
        sftpUploadOverwrite: 'reject', // 覆盖策略：reject | replace | rename
        sftpUploadRefresh: true,    // 全部完成后自动刷新列表
      };
      // 恢复持久化的面板显隐偏好（必须在 activateTab 前读，
      // 否则 syncFilesPanelForActiveTab 看不到 true 值，永远走隐藏分支）
      try {
        const v = localStorage.getItem('ssh_show_files:' + sys.name + ':' + srv.name);
        if (v === '1') tab.filesPanelVisible = true;
      } catch (_) { /* ignore */ }
      pageState.tabs.push(tab);

      tabBarEl.appendChild(tabBtn);
      termAreaEl.appendChild(termEl);
      emptyHint.style.display = 'none';

      activateTab(tabId);

      function initAndConnect() {
        const termOpts = {
          theme: currentXtermTheme(),
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
          fontSize: 13,
          cursorBlink: true,
          scrollback: 5000,
          allowProposedApi: true,
          cols: DEFAULT_COLS,
          rows: DEFAULT_ROWS,
          // Chrome 下 canvas 渲染器有已知 bug：连接后输入命令回车，输出只显示当前行
          // 或整屏不刷新（360 浏览器因 GPU/合成配置不同未触发）。DOM 渲染器不依赖
          // canvas 2D 上下文，兼容性最好，SSH 终端场景下性能也完全够用。
          rendererType: 'dom',
        };
        const term = new TerminalCtor(termOpts);

        const FitAddonCtor = getFitAddonCtor();
        const SearchAddonCtor = getSearchAddonCtor();
        const WebLinksAddonCtor = getWebLinksAddonCtor();

        const fitAddon = FitAddonCtor ? new FitAddonCtor() : null;
        const searchAddon = SearchAddonCtor ? new SearchAddonCtor() : null;
        if (fitAddon) term.loadAddon(fitAddon);
        if (searchAddon) term.loadAddon(searchAddon);
        if (WebLinksAddonCtor) term.loadAddon(new WebLinksAddonCtor());

        tab.term = term;
        tab.fitAddon = fitAddon;
        tab.searchAddon = searchAddon;

        term.open(termEl);

        // v0.13+：光标行高亮 marker（左侧主题色条，跟踪光标所在行）
        setupCursorMarker(tab, term);

        setTimeout(function () {
          if (tab.closed) return;
          if (fitAddon) {
            try { fitAddon.fit(); } catch (e) { /* ignore */ }
          }
          tab.cols = term.cols || DEFAULT_COLS;
          tab.rows = term.rows || DEFAULT_ROWS;

          const encoder = new TextEncoder();
          term.onData(function (data) {
            if (tab.ws && tab.ws.readyState === 1) {
              tab.ws.send(encoder.encode(data));
            }
          });

          term.onResize(function (size) {
            if (tab.closed) return;
            tab.cols = size.cols;
            tab.rows = size.rows;
            sendControl(tab, { type: 'resize', cols: size.cols, rows: size.rows });
          });

          const resizeObs = new ResizeObserver(function () {
            // 【bug-修复】tab 隐藏时（termEl display:none）offsetWidth=0，
            // 此时 fit() 会把 cols 拉成 0/1，buffer 中后续写入的字符按 0 列 wrap，
            // 切回 tab 时 buffer 显示成"竖排乱码"（用户报告：credit@ces... / hi199:/...）。
            // 修复：hidden 状态跳过 fit，让 cols 维持上次有效值。
            // 切回时由 activateTab 里的 setTimeout(50) 统一 fit + focus。
            if (tab.fitAddon && !tab.closed && termEl.offsetWidth > 0) {
              try {
                tab.fitAddon.fit();
              } catch (e) { /* ignore */ }
            }
          });
          resizeObs.observe(termEl);
          tab.resizeObs = resizeObs;

          connectWS(tab);
        }, 50);
      }

      setTimeout(initAndConnect, 0);
      persistTabs();
    }

    // ---- WebSocket 连接 ----
    function connectWS(tab) {
      if (tab.closed) return;
      if (tab.ws) {
        try { tab.ws.close(); } catch (e) { /* ignore */ }
        tab.ws = null;
      }
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const encoding = tab.encoding || 'utf-8';
      const url = proto + '//' + location.host + '/api/ssh/shell/ws' +
        '?system=' + encodeURIComponent(tab.system) +
        '&server=' + encodeURIComponent(tab.server) +
        '&cols=' + tab.cols + '&rows=' + tab.rows +
        '&encoding=' + encoding;

      try {
        tab.ws = new WebSocket(url);
      } catch (e) {
        if (tab.term) tab.term.write('\r\n\x1b[31m连接失败: ' + escapeTermText(e.message || String(e)) + '\x1b[0m\r\n');
        tab.closed = true;
        tab.lastErrorReason = 'ws_construct_failed';
        updateTabStatus(tab);
        updateToolbarButtons();
        return;
      }
      // Bug#4：ws 开始握手 → 切到 connecting 状态点
      updateTabStatus(tab);
      tab.ws.binaryType = 'arraybuffer';

      tab.ws.onopen = function () {
        if (tab.closed) return;
        tab.reconnecting = false;
        updateTabStatus(tab);
        if (tab.term) {
          tab.term.focus();
        }
      };

      tab.ws.onmessage = function (ev) {
        if (tab.closed || !tab.term) return;
        if (typeof ev.data === 'string') {
          handleControl(tab, ev.data);
        } else {
          const bytes = new Uint8Array(ev.data);
          tab.term.write(bytes);
        }
      };

      tab.ws.onerror = function () {
      };

      tab.ws.onclose = function () {
        if (tab.ws !== this) return;
        if (tab.closed) return;
        if (tab.term) tab.term.write('\r\n\x1b[33m— 连接已断开 —\x1b[0m\r\n');
        tab.closed = true;
        // 区分主动 close（用户点 × / 离开页面）vs 网络断开
        // 这里没法精准判断；只要是 ws.onclose 就标 closed（灰色），不算 err
        tab.lastErrorReason = '';
        updateTabStatus(tab);
        updateToolbarButtons();
      };
      updateToolbarButtons();
    }

    function handleControl(tab, raw) {
      let msg;
      try { msg = JSON.parse(raw); } catch (e) { return; }
      if (!msg || !msg.type) return;
      switch (msg.type) {
        case 'exit':
          if (tab.term) tab.term.write('\r\n\x1b[33m— shell 已退出（code=' + (msg.code != null ? msg.code : '?') + '）—\x1b[0m\r\n');
          tab.closed = true;
          // shell 退出不算 error（用户主动 exit / 命令完成）
          tab.lastErrorReason = '';
          updateTabStatus(tab);
          updateToolbarButtons();
          break;
        case 'error':
          handleErrorFrame(tab, msg);
          break;
        case 'pong':
          break;
        case 'cwd':
          // 后端返回了 shell 的当前工作目录（query_cwd 的响应）
          if (tab.sftpCwdQueryTimer) {
            clearTimeout(tab.sftpCwdQueryTimer);
            tab.sftpCwdQueryTimer = null;
          }
          tab.sftpCwdQueryId = null;
          if (msg.path && typeof msg.path === 'string') {
            sftpList(tab, msg.path);
          } else {
            toast('获取终端目录失败：返回路径为空', 'warn');
          }
          break;
      }
    }

    function handleErrorFrame(tab, msg) {
      const reason = msg.reason || '';
      const message = msg.message || '';
      if (!tab.term) {
        tab.closed = true;
        tab.lastErrorReason = reason;
        updateTabStatus(tab);
        updateToolbarButtons();
        return;
      }
      if (reason === 'no_password') {
        tab.term.write('\r\n\x1b[36m需要 SSH 密码\x1b[0m\r\n');
        promptPassword(tab);
        // 注意：no_password 不算 closed，等待用户输入
      } else if (reason === 'cred_resolve') {
        tab.term.write('\r\n\x1b[31m凭据解析失败: ' + escapeTermText(message) + '\x1b[0m\r\n');
        tab.closed = true;
        tab.lastErrorReason = reason;
      } else if (reason === 'dial_failed') {
        tab.term.write('\r\n\x1b[31mSSH 连接失败: ' + escapeTermText(message) + '\x1b[0m\r\n');
        tab.term.write('\r\n\x1b[2m  （检查 host/port/网络/host_key 配置）\x1b[0m\r\n');
        tab.closed = true;
        tab.lastErrorReason = reason;
      } else if (reason === 'shell_failed') {
        tab.term.write('\r\n\x1b[31m启动 shell 失败: ' + escapeTermText(message) + '\x1b[0m\r\n');
        tab.closed = true;
        tab.lastErrorReason = reason;
      } else if (reason === 'max_sessions') {
        tab.term.write('\r\n\x1b[31m' + escapeTermText(message || '并发会话已达上限') + '\x1b[0m\r\n');
        tab.closed = true;
        tab.lastErrorReason = reason;
      } else {
        tab.term.write('\r\n\x1b[31m错误: ' + escapeTermText(message || reason) + '\x1b[0m\r\n');
        tab.closed = true;
        tab.lastErrorReason = reason;
      }
      updateTabStatus(tab);
      updateToolbarButtons();
    }

    // ---- 密码输入弹层 ----
    function promptPassword(tab) {
      if (tab._pwdOverlay) return;

      const overlay = el('div', { class: 'ssh-pwd-overlay' });
      const card = el('div', { class: 'ssh-pwd-card' });
      card.appendChild(el('div', { class: 'ssh-pwd-title', text: '输入 SSH 密码' }));
      card.appendChild(el('div', { class: 'ssh-pwd-desc text-dim', text: tab.systemName + ' / ' + tab.serverName + ' (' + (tab.username || '-') + ')' }));

      const passInput = el('input', {
        type: 'password', class: 'ssh-pwd-input', autocomplete: 'current-password',
        placeholder: 'SSH 密码', autofocus: ''
      });
      card.appendChild(passInput);

      const rememberLabel = el('label', { class: 'ssh-pwd-remember inline' }, [
        el('input', { type: 'checkbox', checked: true }),
        document.createTextNode('记住密码（存到系统 keyring）')
      ]);
      card.appendChild(rememberLabel);

      const btnRow = el('div', { class: 'ssh-pwd-btnrow' });
      const cancelBtn = el('button', { class: 'btn', type: 'button', text: '取消' });
      const okBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '连接' });
      btnRow.appendChild(cancelBtn);
      btnRow.appendChild(okBtn);
      card.appendChild(btnRow);
      overlay.appendChild(card);
      document.body.appendChild(overlay);
      tab._pwdOverlay = overlay;
      setTimeout(function () { passInput.focus(); }, 50);

      function close() {
        if (tab._pwdOverlay === overlay) tab._pwdOverlay = null;
        overlay.remove();
      }
      function submit() {
        const pw = passInput.value;
        if (!pw) { toast('密码不能为空', 'warn'); return; }
        const remember = rememberLabel.querySelector('input').checked;
        okBtn.textContent = '保存中…';
        okBtn.disabled = true;

        function reconnectWS() {
          close();
          tab.closed = false;
          tab.reconnecting = true;
          connectWS(tab);
        }

        if (remember) {
          api('POST', '/api/credentials/save', {
            system: tab.system, server: tab.server, username: tab.username, password: pw
          }).then(reconnectWS).catch(function (e) {
            okBtn.textContent = '连接';
            okBtn.disabled = false;
            toast('保存密码失败：' + (e.message || e), 'err');
          });
        } else {
          toast('Web 终端需要保存密码到 keyring 才能连接，请勾选「记住密码」', 'warn');
          okBtn.textContent = '连接';
          okBtn.disabled = false;
        }
      }
      okBtn.addEventListener('click', submit);
      cancelBtn.addEventListener('click', function () {
        close();
        if (tab.term) tab.term.write('\r\n\x1b[2m已取消\x1b[0m\r\n');
        tab.closed = true;
        updateTabStatus(tab);
        updateToolbarButtons();
      });
      passInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') { e.preventDefault(); submit(); }
        if (e.key === 'Escape') {
          e.preventDefault();
          close();
          if (tab.term) tab.term.write('\r\n\x1b[2m已取消\x1b[0m\r\n');
          tab.closed = true;
          updateTabStatus(tab);
          updateToolbarButtons();
        }
      });
      overlay.addEventListener('click', function (e) {
        if (e.target === overlay) {
          close();
          if (tab.term) tab.term.write('\r\n\x1b[2m已取消\x1b[0m\r\n');
          tab.closed = true;
          updateTabStatus(tab);
          updateToolbarButtons();
        }
      });
    }

    // ---- tab 管理 ----
    function activateTab(tabId) {
      pageState.activeTabId = tabId;
      // Bug#26：切换 tab 时关闭旧 tab 的 inline 搜索条，避免出现在新 tab 上空、
      // 也不出现"切回来还开着但搜索的是旧 tab 缓冲"的错位。
      if (searchInTerminal._bar && searchInTerminal._barTab !== tabId) {
        try { searchInTerminal._bar.remove(); } catch (_) { /* ignore */ }
        searchInTerminal._bar = null;
        searchInTerminal._barTab = null;
      }
      pageState.tabs.forEach(function (t) {
        const isActive = t.id === tabId;
        t.termEl.style.display = isActive ? 'block' : 'none';
        if (t.tabBtn) t.tabBtn.classList.toggle('active', isActive);
        if (isActive && t.fitAddon) {
          setTimeout(function () {
            if (!t.closed && t.fitAddon && t.id === pageState.activeTabId) {
              try { t.fitAddon.fit(); } catch (e) { /* ignore */ }
              if (t.term) {
                t.cols = t.term.cols || t.cols;
                t.rows = t.term.rows || t.rows;
                t.term.focus();
              }
            }
          }, 50);
        }
      });
      // v0.11+：切换 tab 时同步 SFTP 面板状态（每 tab 独立）
      syncFilesPanelForActiveTab();
      updateActiveLabel();
      updateToolbarButtons();
      persistTabs();  // Bug#19: active 切换也要持久化
    }

    // syncFilesPanelForActiveTab 把 SFTP 面板的 DOM 状态同步到当前 active tab 的状态。
    // 每 tab 独立维护 SFTP 状态（路径/选中/显隐），切换 tab 时面板内容跟着切。
    // 注意：localStorage 偏好在 openTab 时已读取并写入 tab.filesPanelVisible，这里只读状态。
    function syncFilesPanelForActiveTab() {
      const tab = getActiveTab();
      if (!tab) {
        filesPanelEl.style.display = 'none';
        return;
      }
      if (tab.filesPanelVisible) {
        if (!tab.sftpPanelInited) {
          initSftpPanelForTab(tab);
          tab.sftpPanelInited = true;
        }
        renderSftpPanelContent(tab);
        filesPanelEl.style.display = 'flex';
        setTimeout(function () {
          if (tab.fitAddon && !tab.closed) {
            try { tab.fitAddon.fit(); } catch (e) { /* ignore */ }
          }
        }, 16);
        // 首次显示且未加载过文件列表时，先列当前目录（/ 或上次记忆路径）
        if ((!tab.sftpEntries || tab.sftpEntries.length === 0) && !tab.sftpLoading && !tab.sftpCwdQueryId) {
          setTimeout(function () { sftpList(tab, tab.sftpCwd || '/'); }, 50);
        }
      } else {
        filesPanelEl.style.display = 'none';
        setTimeout(function () {
          if (tab.fitAddon && !tab.closed) {
            try { tab.fitAddon.fit(); } catch (e) { /* ignore */ }
          }
        }, 16);
      }
    }

    function updateActiveLabel() {
      const tab = pageState.tabs.find(function (t) { return t.id === pageState.activeTabId; });
      if (!tab) {
        activeLabel.textContent = '';
        activeLabel.removeAttribute('title');
        return;
      }
      var status = '';
      if (tab.closed) status = '（已断开）';
      else if (tab.ws && tab.ws.readyState === 0) status = '（连接中…）';
      else if (tab.ws && tab.ws.readyState === 1) status = '（已连接）';
      // Bug#22：缩短 label 文案，只显示 server @ host:port (status)，
      // 完整 system/server/username 放 title tooltip。
      // 之前太长（"本地测试 / Mock SSH (test@127.0.0.1:2225)（已连接）"），
      // 在窄屏被 ellipsis 截掉。
      const short = tab.serverName + ' @ ' + tab.host + ':' + tab.port + ' ' + status;
      activeLabel.textContent = short;
      const full = tab.systemName + ' / ' + tab.serverName + ' (' + (tab.username || '-') + '@' + tab.host + ':' + tab.port + ') ' + status;
      activeLabel.title = full;
    }

    // Bug#4：tab 连接状态点（设计文档 §5.2 要求"🟢 已连接 / 🟡 正在连接 / ⚪ 未连接 / 🔴 错误"）。
// 之前只有 .ssh-tab-closed 表示断开，其余状态完全没视觉区分。
// 状态机：
//   - 'connecting' (busy)：刚发 ws / 重连中 → 黄点
//   - 'connected' (idle)：ws 已开 + shell 活着 → 绿点
//   - 'closed' (err)：用户主动 close / shell 退出 / 连接失败 → 红点
//   - 'idle'：默认初始（点开但还没连）→ 灰点
function updateTabStatus(tab) {
      if (!tab.tabBtn) return;
      // 旧的 closed 类只在最终断开时挂上（保持原 line-through 视觉），状态点单独处理
      if (tab.closed) {
        tab.tabBtn.classList.add('ssh-tab-closed');
      } else {
        tab.tabBtn.classList.remove('ssh-tab-closed');
      }
      // 计算 status
      let status = 'idle';
      if (tab.closed) {
        // 区分"用户主动 close" vs "连接失败"——通过 lastErrorReason 区分
        status = tab.lastErrorReason ? 'err' : 'closed';
      } else if (tab.reconnecting) {
        status = 'connecting';
      } else if (tab.ws && tab.ws.readyState === 0) {
        status = 'connecting';
      } else if (tab.ws && tab.ws.readyState === 1) {
        status = 'connected';
      }
      // 替换 class（4 选 1）
      tab.tabBtn.classList.remove('ssh-tab-state-idle', 'ssh-tab-state-connecting', 'ssh-tab-state-connected', 'ssh-tab-state-closed', 'ssh-tab-state-err');
      tab.tabBtn.classList.add('ssh-tab-state-' + status);
      // 同步更新 status dot 的 tooltip
      const dot = tab.tabBtn.querySelector('.ssh-tab-status-dot');
      if (dot) {
        const titles = {
          idle: '未连接', connecting: '连接中…', connected: '已连接',
          closed: '已关闭', err: '错误'
        };
        dot.title = titles[status] || '';
      }
      tab._lastStatus = status;
      updateActiveLabel();
    }

    function closeTab(tabId) {
      const idx = pageState.tabs.findIndex(function (t) { return t.id === tabId; });
      if (idx === -1) return;
      const tab = pageState.tabs[idx];
      tab.closed = true;
      if (tab.ws) { try { tab.ws.close(); } catch (e) { /* ignore */ } }
      if (tab.resizeObs) { try { tab.resizeObs.disconnect(); } catch (e) { /* ignore */ } }
      // v0.13+：先停光标行高亮 rAF，再 dispose term（dispose 后 term._core 不可用）
      if (tab.cursorMarkerStop) { try { tab.cursorMarkerStop(); } catch (e) { /* ignore */ } }
      if (tab.term) { try { tab.term.dispose(); } catch (e) { /* ignore */ } }
      // v0.11+：清理 SFTP 资源（关闭进行中的下载 SSE）
      if (tab.sftpDlEvtSrc) {
        try { tab.sftpDlEvtSrc.close(); } catch (e) { /* ignore */ }
        tab.sftpDlEvtSrc = null;
        // 若下载仍在进行，提示用户后台任务不会中断（透明性）
        if (tab.sftpDlId) {
          toast('下载任务 #' + tab.sftpDlId + ' 仍在后台进行，可在「下载历史」查看', 'idle');
        }
        tab.sftpDlId = null;
        tab.sftpDlProgress = null;
      }
      // v1.4+：关闭 tab 时取消该 tab 进行中的上传
      cancelTabUploads(tab);
      if (tab.tabBtn) tab.tabBtn.remove();
      if (tab.termEl) tab.termEl.remove();
      if (tab._pwdOverlay) tab._pwdOverlay.remove();
      pageState.tabs.splice(idx, 1);
      persistTabs();  // Bug#19

      if (pageState.activeTabId === tabId) {
        if (pageState.tabs.length > 0) {
          // Bug#9：关闭 active tab 时优先激活"左边"的 tab（IDE/浏览器普遍行为），
          // 而不是硬编码最右一个。规则：
          //   - 关闭的是最右 → 激活倒数第二个
          //   - 关闭的不是最右 → 激活原 tab 位置的新 tab（视觉上紧贴原位左侧）
          const closedIdx = idx; // splice 之前 idx 就是被关的 tab 位置
          let nextIdx;
          if (closedIdx >= pageState.tabs.length) {
            // 被关的就是最右 → 倒数第二（即当前 length-1）
            nextIdx = pageState.tabs.length - 1;
          } else {
            // 激活原位置的新 tab（splice 已把 closed tab 移除，索引保持）
            nextIdx = closedIdx;
          }
          activateTab(pageState.tabs[nextIdx].id);
        } else {
          pageState.activeTabId = null;
          emptyHint.style.display = '';
          updateActiveLabel();
          updateToolbarButtons();
        }
      }
    }

    function closeActiveTab() {
      if (pageState.activeTabId != null) closeTab(pageState.activeTabId);
    }

    function closeAllTabs() {
      // Bug#19：closeAllTabs 在路由切换时被调（用户离开 SSH 页）。
      // 这种"切换"不应该清掉持久化数据——用户回来时希望自动重开之前开的 tab。
      // 设个标志位让 persistTabs 在 closeAllTabs 期间静默，避免 activateTab 等
      // 链式调用把"已清空"的 tabs 列表写进 localStorage。
      pageState._suppressPersist = true;
      try {
        const snap = pageState.tabs.slice();
        snap.forEach(function (t) { closeTab(t.id); });
      } finally {
        pageState._suppressPersist = false;
      }
      pageState.disposed = true;
      window.removeEventListener('kairo:themechange', onThemeChange);
      window.removeEventListener('kairo:tab-show', onOuterTabShow);
      window.removeEventListener('keydown', onGlobalKeydown);
      Kairo.core.setActiveShells(null, owner, shellController);
      Kairo.core.setActiveUploads(null, owner, uploadController);
    }

    // Bug#19：tab 持久化（设计文档 §5.2）。
    // 简化策略：只存 (system, server, activeTabId) 列表，刷新页面后自动重开 + 自动重连。
    // localStorage key: kairo:ssh:tabs → [{system, server, active?}, ...]
    // 不存 SFTP 路径/选中文件 — 那些不在设计文档的 v0.10 MVP 范围里。
    // TABS_KEY 已声明在 renderSSH 顶部（避免 TDZ）。
    function persistTabs() {
      // Bug#19：closeAllTabs 路由切换期间静默，避免把"已清空"的 tabs 写进 localStorage。
      // 路由切回时由 restoreTabs 重新恢复。
      if (pageState._suppressPersist) return;
      try {
        const data = pageState.tabs.map(function (t) {
          return { system: t.system, server: t.server, active: t.id === pageState.activeTabId };
        });
        localStorage.setItem(TABS_KEY, JSON.stringify(data));
      } catch (_) { /* ignore quota / privacy */ }
    }
    function restoreTabs() {
      let saved;
      try { saved = JSON.parse(localStorage.getItem(TABS_KEY) || '[]'); } catch (_) { saved = []; }
      if (!Array.isArray(saved) || saved.length === 0) return;
      const cfg = pageState.cfg;
      if (!cfg || !cfg.systems) return;
      let activeFound = false;
      saved.forEach(function (entry) {
        const sys = cfg.systems.find(function (s) { return s.name === entry.system; });
        if (!sys) return;
        const srv = (sys.servers || []).find(function (sv) { return sv.name === entry.server; });
        if (!srv) return;
        openTab(sys, srv);
        if (entry.active) {
          const opened = pageState.tabs.find(function (t) {
            return t.system === entry.system && t.server === entry.server && !t.closed;
          });
          if (opened) { activeFound = true; activateTab(opened.id); }
        }
      });
      if (!activeFound && pageState.tabs.length > 0) {
        activateTab(pageState.tabs[pageState.tabs.length - 1].id);
      }
    }
    function clearPersistedTabs() {
      try { localStorage.removeItem(TABS_KEY); } catch (_) { /* ignore */ }
    }

    // ---- 工具栏动作 ----
    function sendCtrlC() {
      const tab = getActiveTab();
      if (!tab || !tab.ws || tab.ws.readyState !== 1) { toast('未连接', 'warn'); return; }
      // v0.10-修复：之前用 SSH signal channel 发 SIGINT，结果只给 bash 自己，
      // 当前景命令（比如 `sleep 10`）不是 bash 进程组里的前台进程时就收不到，
      // 用户看着 ^C 标记一刷新没反应就以为按钮坏了。
      //
      // 正确做法：发 0x03 (Ctrl+C 字节) 到 ssh stdin，让对端 PTY 的 line discipline
      // 走 ISIG 处理 —— 这样 SIGINT 会发给 fg 进程组，sleep 等子命令才能被打断。
      // 0x03 走 PTY 时 line discipline 自己会回显 ^C 到 xterm，不需要前端再写一遍。
      tab.ws.send(new Uint8Array([0x03]));
    }

    function clearActive() {
      const tab = getActiveTab();
      if (!tab || !tab.term) { toast('没有活跃的 tab', 'warn'); return; }
      tab.term.clear();
    }

    // searchInTerminal 用 SearchAddon 在终端缓冲区里搜索关键词。
    // Bug#3：之前用 window.prompt()，阻塞 UI + 浏览器样式不统一 + 没有大小写/整词配置。
    // 改成自绘 inline 搜索条：term 区上方浮一层，输入框 + 大小写 + 整词 + 上/下/关闭按钮，
    // Enter / Shift+Enter 在匹配项之间跳转。比 prompt 体验好一截。
    function searchInTerminal() {
      const tab = getActiveTab();
      if (!tab || !tab.term) { toast('没有活跃的 tab', 'warn'); return; }
      if (!tab.searchAddon) { toast('搜索组件未加载', 'err'); return; }
      // 已有搜索条：聚焦输入框（再次按 Ctrl+Shift+F 复用）
      if (searchInTerminal._bar && searchInTerminal._barTab === tab.id) {
        const input = searchInTerminal._bar.querySelector('.ssh-search-input');
        if (input) { input.focus(); input.select(); }
        return;
      }
      buildSearchBar(tab);
    }

    function buildSearchBar(tab) {
      // 拆掉旧的（如果有跨 tab 的）
      if (searchInTerminal._bar) {
        try { searchInTerminal._bar.remove(); } catch (_) { /* ignore */ }
        searchInTerminal._bar = null;
      }
      const bar = el('div', { class: 'ssh-search-bar' });
      const input = el('input', {
        type: 'text', class: 'ssh-search-input', placeholder: '搜索终端输出…',
        autocomplete: 'off', title: 'Enter 下一个 / Shift+Enter 上一个 / Esc 关闭'
      });
      const caseBtn = el('button', { class: 'btn btn-xs ssh-search-opt', text: 'Aa', title: '区分大小写' });
      const wordBtn = el('button', { class: 'btn btn-xs ssh-search-opt', text: '\\b', title: '整词匹配' });
      const regexBtn = el('button', { class: 'btn btn-xs ssh-search-opt', text: '.*', title: '正则表达式' });
      const prevBtn = el('button', { class: 'btn btn-xs', text: '↑', title: '上一个 (Shift+Enter)' });
      const nextBtn = el('button', { class: 'btn btn-xs', text: '↓', title: '下一个 (Enter)' });
      const closeBtn = el('button', { class: 'btn btn-xs btn-danger', text: '✕', title: '关闭 (Esc)' });
      bar.appendChild(input);
      bar.appendChild(caseBtn);
      bar.appendChild(wordBtn);
      bar.appendChild(regexBtn);
      bar.appendChild(prevBtn);
      bar.appendChild(nextBtn);
      bar.appendChild(closeBtn);

      const opts = { caseSensitive: false, wholeWord: false, regex: false };
      function updateOpts() {
        caseBtn.classList.toggle('btn-active', opts.caseSensitive);
        wordBtn.classList.toggle('btn-active', opts.wholeWord);
        regexBtn.classList.toggle('btn-active', opts.regex);
      }
      function find(dir) {
        const q = input.value;
        if (!q) return;
        if (opts.regex) {
          try {
            new RegExp(q, opts.caseSensitive ? '' : 'i');
          } catch (e) {
            toast('正则语法错误：' + e.message, 'err');
            return;
          }
        }
        const searchOpts = {
          caseSensitive: opts.caseSensitive,
          wholeWord: opts.wholeWord,
          regex: opts.regex,
        };
        try {
          if (dir === 'prev') tab.searchAddon.findPrevious(q, searchOpts);
          else tab.searchAddon.findNext(q, searchOpts);
        } catch (e) {
          toast('搜索失败：' + e.message, 'err');
        }
      }
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          find(e.shiftKey ? 'prev' : 'next');
        } else if (e.key === 'Escape') {
          e.preventDefault();
          closeBar();
        }
      });
      input.addEventListener('input', function () { if (input.value) find('next'); });
      caseBtn.addEventListener('click', function () { opts.caseSensitive = !opts.caseSensitive; updateOpts(); if (input.value) find('next'); });
      wordBtn.addEventListener('click', function () { opts.wholeWord = !opts.wholeWord; updateOpts(); if (input.value) find('next'); });
      regexBtn.addEventListener('click', function () { opts.regex = !opts.regex; updateOpts(); if (input.value) find('next'); });
      prevBtn.addEventListener('click', function () { find('prev'); });
      nextBtn.addEventListener('click', function () { find('next'); });
      function closeBar() {
        try { tab.searchAddon.clearDecorations && tab.searchAddon.clearDecorations(); } catch (_) { /* ignore */ }
        bar.remove();
        if (searchInTerminal._bar === bar) { searchInTerminal._bar = null; searchInTerminal._barTab = null; }
        if (tab.term) tab.term.focus();
      }
      closeBtn.addEventListener('click', closeBar);

      // 挂到 mainEl 顶部（覆盖在 toolbar 下方、term 区上方）
      const mainEl = document.querySelector('.ssh-main');
      if (mainEl) {
        const termArea = mainEl.querySelector('.ssh-term-area');
        if (termArea) mainEl.insertBefore(bar, termArea);
        else mainEl.appendChild(bar);
      } else {
        document.body.appendChild(bar);
      }
      searchInTerminal._bar = bar;
      searchInTerminal._barTab = tab.id;
      setTimeout(function () { input.focus(); }, 50);
    }

    // ============================================================================
    // v0.11+ SFTP 文件面板（SSH 终端 + SFTP 二合一）
    // 设计：FinalShell 风格（上终端 / 下文件），独立连接（每次操作独立 Dial），
    //       严格只读（list/preview/download），目录跟随用「📌 当前目录」按钮。
    // ============================================================================

    // toggleFilesPanel 工具栏 [📁 文件] 按钮：toggle SFTP 面板显隐
    function toggleFilesPanel() {
      const tab = getActiveTab();
      if (!tab || tab.closed) { toast('没有活跃的 tab', 'warn'); return; }
      tab.filesPanelVisible = !tab.filesPanelVisible;
      // 持久化偏好（每 system:server 独立）
      try {
        localStorage.setItem('ssh_show_files:' + tab.system + ':' + tab.server,
          tab.filesPanelVisible ? '1' : '0');
      } catch (_) { /* ignore */ }
      syncFilesPanelForActiveTab();
      updateToolbarButtons();
    }

    function refreshActiveSftpList() {
      const tab = getActiveTab();
      if (!tab || !tab.sftpEntries || !tab.filesPanelVisible) return;
      renderSftpPanelContent(tab);
    }

    // initSftpPanelForTab 首次显示时构建面板 DOM（懒初始化）。
    // 面板 DOM 复用单例（filesPanelEl），切换 tab 时只换内容，不重建。
    // 注意：localStorage 偏好已在 openTab 时读取，此处无需再读。
    function initSftpPanelForTab(tab) {
      // 占位：未来可放初始化逻辑（如恢复面板宽度等）
    }

    // renderSftpPanelContent 把 tab 的 SFTP 状态渲染到面板 DOM
    function renderSftpPanelContent(tab) {
      filesPanelEl.innerHTML = '';

      // ---- 头部：路径输入框 + 操作按钮 + backend 徽章 ----
      const header = el('div', { class: 'sftp-header' });

      // 路径输入栏（替换旧面包屑：既可看又可编辑）
      const pathBar = el('div', { class: 'sftp-pathbar' });
      const pathInput = el('input', {
        type: 'text', class: 'sftp-path-input-visible',
        placeholder: '输入绝对路径（如 /var/log），回车跳转',
        value: tab.sftpCwd || '/',
        autocomplete: 'off', spellcheck: 'false'
      });
      function goPath() {
        const p = (pathInput.value || '').trim();
        if (!p) return;
        let norm;
        if (p.startsWith('/')) {
          norm = p;
        } else {
          norm = joinPath(tab.sftpCwd || '/', p);
        }
        sftpList(tab, norm);
      }
      pathInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') { e.preventDefault(); goPath(); }
      });
      const btnGo = el('button', { class: 'btn btn-sm btn-primary sftp-path-go', text: '跳转', title: '跳转到输入的路径', onclick: goPath });
      const btnCopyPath = el('button', {
        class: 'btn btn-sm sftp-path-copy', title: '复制当前路径',
        onclick: function () {
          Kairo.core.copyToClipboard(tab.sftpCwd || '/').then(function () {
            toast('路径已复制', 'ok');
          }).catch(function () {
            window.prompt('复制此路径：', tab.sftpCwd || '/');
          });
        }
      });
      btnCopyPath.innerHTML = svgIcon('smClipboard', 13);
      pathBar.appendChild(pathInput);
      pathBar.appendChild(btnGo);
      pathBar.appendChild(btnCopyPath);
      header.appendChild(pathBar);

      const actions = el('div', { class: 'sftp-actions' });
      // 导航组：📂进入当前目录 / ↕上级 / 🔄刷新
      const btnSyncCwd = el('button', {
        class: 'btn btn-sm btn-primary',
        title: '进入 SSH 终端当前所在目录（会向终端发送一条 pwd 命令）',
        onclick: function () { gotoCurrentDir(tab); }
      });
      btnSyncCwd.innerHTML = '<span style="display:inline-flex;align-items:center;gap:3px;">📂 进入当前目录</span>';
      const btnUp = el('button', { class: 'btn btn-sm', text: '↕ 上级', title: '跳到上一级目录', onclick: function () { sftpGoUp(tab); } });
      const btnRefresh = el('button', { class: 'btn btn-sm', text: '🔄 刷新', title: '刷新当前目录', onclick: function () { sftpList(tab, tab.sftpCwd); } });

      const navSep = el('span', { class: 'sftp-actions-sep' });
      const btnDownload = el('button', { class: 'btn btn-sm btn-primary', text: '下载', title: '下载选中文件（多选）', onclick: function () { sftpDownloadSelected(tab); }, disabled: true });
      // v1.4+：上传按钮 + 隐藏 file input（每次渲染重建，change 后进队列）
      const btnUploadPick = el('button', { class: 'btn btn-sm', text: '📤 上传', title: '上传文件到当前目录（支持拖拽到面板）', onclick: function () { uploadFileInp.click(); } });
      const uploadFileInp = el('input', { type: 'file', multiple: 'multiple', style: 'display:none;' });
      uploadFileInp.addEventListener('change', function () {
        if (uploadFileInp.files && uploadFileInp.files.length) {
          addUploadFiles(tab, uploadFileInp.files);
          uploadFileInp.value = '';
        }
      });
      const btnSelectAll = el('button', { class: 'btn btn-sm sftp-select-all', text: '全选', title: '全选 / 取消全选',
        onclick: function () {
          if (!tab.sftpEntries || tab.sftpEntries.length === 0) return;
          const allSelected = tab.sftpSelected.size === tab.sftpEntries.length;
          if (allSelected) {
            tab.sftpSelected = new Set();
          } else {
            tab.sftpSelected = new Set(tab.sftpEntries.map(function (e) { return e.name; }));
          }
          btnDownload.disabled = tab.sftpSelected.size === 0;
          renderSftpPanelContent(tab);
          btnSelectAll.textContent = allSelected ? '全选' : '取消';
        },
        disabled: true });
      const btnClose = el('button', { class: 'btn btn-sm sftp-close', text: '×', title: '隐藏文件面板', onclick: function () { toggleFilesPanel(); } });
      actions.appendChild(btnSyncCwd);
      actions.appendChild(btnUp);
      actions.appendChild(btnRefresh);
      actions.appendChild(navSep);
      actions.appendChild(btnDownload);
      actions.appendChild(btnUploadPick);
      actions.appendChild(uploadFileInp);
      actions.appendChild(btnSelectAll);
      actions.appendChild(btnClose);
      header.appendChild(actions);

      const badge = el('div', { class: 'sftp-backend-badge' });
      if (tab.sftpBackend === 'sftp') {
        badge.textContent = 'SFTP';
        badge.className = 'sftp-backend-badge badge-ok';
        badge.title = '使用标准 SFTP 子系统';
      } else if (tab.sftpBackend === 'shell') {
        badge.textContent = 'Shell 兜底';
        badge.className = 'sftp-backend-badge badge-warn';
        badge.title = '远端不支持 SFTP 子系统，已降级为 shell 命令（cat/ls）模拟';
      } else {
        badge.textContent = '-';
        badge.className = 'sftp-backend-badge badge-dim';
      }
      header.appendChild(badge);

      filesPanelEl.appendChild(header);

      // ---- 文件列表表格 ----
      const listWrap = el('div', { class: 'sftp-filelist-wrap' });
      if (tab.sftpLoading) {
        listWrap.appendChild(el('div', { class: 'sftp-empty text-dim', text: '加载中…' }));
      } else if (tab.sftpLastError) {
        const errBox = el('div', { class: 'sftp-empty sftp-empty-err' });
        errBox.appendChild(el('div', { class: 'sftp-empty-err-title', text: '加载失败' }));
        errBox.appendChild(el('div', { class: 'sftp-empty-err-msg', text: tab.sftpLastError }));
        listWrap.appendChild(errBox);
      } else if (!tab.sftpEntries || tab.sftpEntries.length === 0) {
        listWrap.appendChild(el('div', { class: 'sftp-empty text-dim', text: '目录为空' }));
      } else {
        const table = el('table', { class: 'sftp-table' });
        const thead = el('thead', {}, [
          el('tr', {}, [
            el('th', { class: 'sftp-col-name', text: '名称' }),
            el('th', { class: 'sftp-col-size', text: '大小' }),
            el('th', { class: 'sftp-col-mtime', text: '修改时间' }),
            el('th', { class: 'sftp-col-mode', text: '权限' }),
            el('th', { class: 'sftp-col-actions', text: '操作' })
          ])
        ]);
        table.appendChild(thead);
        const tbody = el('tbody');
        if (tab.sftpCwd && tab.sftpCwd !== '/') {
          tbody.appendChild(el('tr', { class: 'sftp-row-parent' }, [
            el('td', { colspan: '5' }, [
              el('span', {
                class: 'sftp-row-parent-link', text: '📁 ..', title: '返回上一级',
                onclick: function () { sftpGoUp(tab); }
              })
            ])
          ]));
        }
        const dlInProgress = !!tab.sftpDlId;
        tab.sftpEntries.forEach(function (entry) {
          const SftpCommon = (window.Kairo && window.Kairo.SftpCommon) || {};
          const fmtBytes = SftpCommon.formatBytes || function (n) { return n; };
          const fmtMTime = SftpCommon.formatMTime || function (s) { return s; };
          const isText = SftpCommon.isTextFileByExt || function () { return false; };
          const isDir = !!entry.isDir;
          const icon = isDir ? '📁' : '📄';
          const sizeText = isDir ? '-' : fmtBytes(entry.size);
          const fullPath = joinPath(tab.sftpCwd, entry.name);
          const row = el('tr', {
            class: 'sftp-row' + (tab.sftpSelected.has(entry.name) ? ' sftp-row-selected' : ''),
            'data-name': entry.name
          }, [
            el('td', { class: 'sftp-col-name' }, [
              el('span', { class: 'sftp-icon', text: icon }),
              el('span', { class: 'sftp-name', text: entry.name, title: entry.name })
            ]),
            el('td', { class: 'sftp-col-size text-dim', text: sizeText }),
            el('td', { class: 'sftp-col-mtime text-dim', text: fmtMTime(entry.mtime) }),
            el('td', { class: 'sftp-col-mode text-dim', text: entry.mode || '-' }),
            el('td', { class: 'sftp-col-actions' }, isDir ? [
              el('button', { class: 'btn btn-sm', text: '打包下载', disabled: dlInProgress, title: dlInProgress ? '当前已有下载任务进行中' : '递归下载目录并打包 zip', onclick: function (e) { e.stopPropagation(); sftpDownloadPaths(tab, [fullPath]); } }),
              el('button', { class: 'btn btn-sm', text: '打开', onclick: function (e) { e.stopPropagation(); sftpList(tab, fullPath); } })
            ] : (isText(entry.name) ? [
              el('button', { class: 'btn btn-sm', text: '预览', onclick: function (e) { e.stopPropagation(); sftpPreview(tab, fullPath); } }),
              el('button', { class: 'btn btn-sm', text: '下载', disabled: dlInProgress, title: dlInProgress ? '当前已有下载任务进行中' : '下载此文件', onclick: function (e) { e.stopPropagation(); sftpDownloadOne(tab, fullPath); } }),
              ...buildEditButtons(entry.name, fullPath, tab)
            ] : [
              el('button', { class: 'btn btn-sm', text: '下载', disabled: dlInProgress, title: dlInProgress ? '当前已有下载任务进行中' : '下载此文件', onclick: function (e) { e.stopPropagation(); sftpDownloadOne(tab, fullPath); } })
            ]))
          ]);
          row.addEventListener('click', function (e) {
            if (e.target.tagName === 'BUTTON' || e.target.tagName === 'INPUT') return;
            if (tab.sftpSelected.has(entry.name)) {
              tab.sftpSelected.delete(entry.name);
              row.classList.remove('sftp-row-selected');
            } else {
              tab.sftpSelected.add(entry.name);
              row.classList.add('sftp-row-selected');
            }
            btnDownload.disabled = tab.sftpSelected.size === 0;
            updateSftpStatusBar(tab);
          });
          row.addEventListener('dblclick', function (e) {
            if (e.target.tagName === 'BUTTON') return;
            if (isDir) {
              sftpList(tab, fullPath);
            } else if (isText(entry.name)) {
              sftpPreview(tab, fullPath);
            } else {
              sftpDownloadOne(tab, fullPath);
            }
          });
          tbody.appendChild(row);
        });
        table.appendChild(tbody);
        listWrap.appendChild(table);
        btnDownload.disabled = tab.sftpSelected.size === 0;
        const allSelected = tab.sftpEntries.length > 0 && tab.sftpSelected.size === tab.sftpEntries.length;
        btnSelectAll.disabled = tab.sftpEntries.length === 0;
        btnSelectAll.textContent = allSelected ? '取消' : '全选';
        btnSelectAll.classList.toggle('active', allSelected);
      }
      filesPanelEl.appendChild(listWrap);

      // ---- v1.4+ 上传区（队列为空时隐藏）----
      filesPanelEl.appendChild(buildUploadSection(tab));

      // ---- 状态栏 ----
      const status = el('div', { class: 'sftp-status' });
      const count = tab.sftpEntries ? tab.sftpEntries.length : 0;
      const selCount = tab.sftpSelected.size;
      status.appendChild(el('span', { text: '路径: ' + (tab.sftpCwd || '/') }));
      status.appendChild(el('span', { class: 'sftp-status-sep', text: ' | ' }));
      status.appendChild(el('span', { text: '文件数: ' + count }));
      if (selCount > 0) {
        status.appendChild(el('span', { class: 'sftp-status-sep', text: ' | ' }));
        status.appendChild(el('span', { text: '已选: ' + selCount }));
      }
      if (tab.sftpDlId && tab.sftpDlProgress) {
        const p = tab.sftpDlProgress;
        const cur = p.current ? ' · ' + p.current : '';
        const filePct = (p.fileTotal > 0) ? Math.min(100, Math.round(p.fileWritten / p.fileTotal * 100)) : 0;
        const fileInfo = (p.fileTotal > 0 && p.current) ? ' ' + formatBytes(p.fileWritten) + '/' + formatBytes(p.fileTotal) + ' (' + filePct + '%)' : '';
        status.appendChild(el('span', { class: 'sftp-status-sep', text: ' | ' }));
        status.appendChild(el('span', { class: 'sftp-status-dl',
          text: '⏳ 下载中 ' + p.done + '/' + p.total + cur + fileInfo }));
        if (p.fileTotal > 0) {
          const barWrap = el('span', { class: 'sftp-dl-bar-wrap' });
          const bar = el('span', { class: 'sftp-dl-bar' });
          bar.style.width = filePct + '%';
          barWrap.appendChild(bar);
          status.appendChild(barWrap);
        }
        status.appendChild(el('button', {
          class: 'btn btn-sm btn-danger', text: '取消', title: '取消下载任务',
          onclick: function () { cancelSftpDownload(tab); }
        }));
      }
      filesPanelEl.appendChild(status);
    }

    // updateSftpStatusBar 只更新状态栏文本，不全量重绘面板（避免大目录闪烁）。
    // 下载进度 SSE 事件（file_start/file_done）用这个，不走 renderSftpPanelContent。
    function updateSftpStatusBar(tab) {
      if (tab.id !== pageState.activeTabId) return;
      const statusEl = filesPanelEl.querySelector('.sftp-status');
      if (!statusEl) return;
      const esc = Kairo.core.escapeHtml;
      const count = tab.sftpEntries ? tab.sftpEntries.length : 0;
      const selCount = tab.sftpSelected.size;
      let html = '<span>路径: ' + esc(tab.sftpCwd || '/') + '</span>' +
        '<span class="sftp-status-sep"> | </span>' +
        '<span>文件数: ' + count + '</span>';
      if (selCount > 0) {
        html += '<span class="sftp-status-sep"> | </span>' +
          '<span>已选: ' + selCount + '</span>';
      }
      if (tab.sftpDlId && tab.sftpDlProgress) {
        const p = tab.sftpDlProgress;
        const cur = p.current ? ' · ' + esc(p.current) : '';
        const filePct = (p.fileTotal > 0) ? Math.min(100, Math.round(p.fileWritten / p.fileTotal * 100)) : 0;
        const fileInfo = (p.fileTotal > 0 && p.current) ? ' ' + formatBytes(p.fileWritten) + '/' + formatBytes(p.fileTotal) + ' (' + filePct + '%)' : '';
        html += '<span class="sftp-status-sep"> | </span>' +
          '<span class="sftp-status-dl">⏳ 下载中 ' + p.done + '/' + p.total + cur + fileInfo + '</span>';
        if (p.fileTotal > 0) {
          html += '<span class="sftp-dl-bar-wrap"><span class="sftp-dl-bar" style="width:' + filePct + '%"></span></span>';
        }
        html += ' <button class="btn btn-sm btn-danger" id="sftp-dl-cancel-btn" title="取消下载任务">取消</button>';
      }
      statusEl.innerHTML = html;
      const cancelBtn = statusEl.querySelector('#sftp-dl-cancel-btn');
      if (cancelBtn) {
        cancelBtn.addEventListener('click', function () { cancelSftpDownload(tab); });
      }
    }

    // ============================================================================
    // v1.4+ SFTP 上传（DnD + 文件选择，串行队列，后端 /api/ssh/sftp/upload/*）
    // 参照 files.js 上传实现，按 tab 隔离状态。
    // ============================================================================

    // buildUploadSection 构建上传区 DOM（renderSftpPanelContent 每次全量重建时调用）。
    // 队列为空 → display:none；有任务 → 显示。
    function buildUploadSection(tab) {
      const section = el('div', { class: 'sftp-upload-section', style: 'display:none; border-top:1px dashed var(--line);' });
      const optsRow = el('div', { class: 'sftp-upload-opts', style: 'display:flex; align-items:center; gap:8px; padding:6px 10px; flex-wrap:wrap;' });
      optsRow.appendChild(el('span', { class: 'text-dim', text: '上传：', style: 'font-size:12px;' }));
      const overwriteSel = el('select', { title: '同名文件已存在时的处理策略' });
      overwriteSel.appendChild(el('option', { value: 'reject', text: '拒绝覆盖（默认）' }));
      overwriteSel.appendChild(el('option', { value: 'replace', text: '直接替换' }));
      overwriteSel.appendChild(el('option', { value: 'rename', text: '自动重命名（_1/_2）' }));
      overwriteSel.value = tab.sftpUploadOverwrite || 'reject';
      overwriteSel.addEventListener('change', function () { tab.sftpUploadOverwrite = overwriteSel.value; });
      const refreshChk = el('input', { type: 'checkbox', id: 'sftp-upload-refresh' });
      refreshChk.checked = !!tab.sftpUploadRefresh;
      refreshChk.addEventListener('change', function () { tab.sftpUploadRefresh = refreshChk.checked; });
      const btnCancelAll = el('button', { class: 'btn btn-sm btn-danger', text: '全取消', onclick: function () { cancelAllUploads(tab); } });
      const btnClear = el('button', { class: 'btn btn-sm', text: '清空已完成', onclick: function () { clearFinishedUploads(tab); } });
      optsRow.appendChild(overwriteSel);
      optsRow.appendChild(el('label', { class: 'inline', style: 'gap:4px; align-items:center; display:inline-flex;' }, [refreshChk, document.createTextNode('完成后刷新')]));
      optsRow.appendChild(el('span', { style: 'flex:1 1 auto;' }));
      optsRow.appendChild(btnCancelAll);
      optsRow.appendChild(btnClear);
      const listWrap = el('div', { class: 'sftp-upload-list', style: 'max-height:180px; overflow-y:auto; padding:0 10px;' });
      const summary = el('div', { class: 'sftp-upload-summary text-dim', style: 'padding:4px 10px 8px; font-size:12px;' });
      section.appendChild(optsRow);
      section.appendChild(listWrap);
      section.appendChild(summary);
      // 全量重建时也要从队列状态渲染行（不只 updateUploadQueueUI 做增量），
      // 否则列表刷新（renderSftpPanelContent）后行会消失只剩空框。
      tab.sftpUploadQueue.forEach(function (t) { listWrap.appendChild(buildUploadRow(tab, t)); });
      summary.textContent = uploadSummaryText(tab);
      if (tab.sftpUploadQueue.length > 0) section.style.display = '';
      return section;
    }

    // updateUploadQueueUI 只更新上传区内容（进度事件高频触发，避免全量重绘面板）。
    // 仅当 tab 是 active 时才碰 DOM（面板内容属于 active tab）。
    function updateUploadQueueUI(tab) {
      if (tab.id !== pageState.activeTabId) return;
      const section = filesPanelEl.querySelector('.sftp-upload-section');
      if (!section) return;
      const listWrap = section.querySelector('.sftp-upload-list');
      if (!listWrap) return;
      while (listWrap.firstChild) listWrap.removeChild(listWrap.firstChild);
      tab.sftpUploadQueue.forEach(function (t) { listWrap.appendChild(buildUploadRow(tab, t)); });
      section.style.display = tab.sftpUploadQueue.length > 0 ? '' : 'none';
      const summary = section.querySelector('.sftp-upload-summary');
      if (summary) summary.textContent = uploadSummaryText(tab);
      const hasActive = tab.sftpUploadQueue.some(function (t) { return t.status === 'pending' || t.status === 'uploading'; });
      const btns = section.querySelectorAll('.sftp-upload-opts button');
      if (btns[0]) btns[0].disabled = !hasActive; // 全取消
    }

    function uploadSummaryText(tab) {
      const q = tab.sftpUploadQueue;
      if (!q.length) return '';
      const done = q.filter(function (t) { return t.status === 'done'; }).length;
      const fail = q.filter(function (t) { return t.status === 'fail'; }).length;
      const cancel = q.filter(function (t) { return t.status === 'cancel'; }).length;
      const bytes = q.reduce(function (s, t) { return s + (t.status === 'done' ? (t.bytes || 0) : 0); }, 0);
      const totalBytes = q.reduce(function (s, t) { return s + (t.size || 0); }, 0);
      return '共 ' + q.length + ' 个 · 成功 ' + done + ' · 失败 ' + fail + ' · 取消 ' + cancel +
        ' · ' + formatBytes(bytes) + ' / ' + formatBytes(totalBytes);
    }

    // buildUploadRow 单行任务 DOM（与 files.js 上传行一致的结构，图标用文本）。
    function buildUploadRow(tab, task) {
      const row = el('div', { class: 'upload-row', style: 'display:flex; align-items:center; gap:8px; padding:6px 4px; border-bottom:1px dashed var(--line);' });
      const nameCell = el('div', { style: 'flex: 1 1 40%; min-width: 120px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;', title: task.name + (task.finalName && task.finalName !== task.name ? ' → ' + task.finalName : '') });
      nameCell.textContent = task.name;
      if (task.finalName && task.finalName !== task.name && task.status === 'done') {
        nameCell.appendChild(document.createTextNode('  → ' + task.finalName));
      }
      row.appendChild(nameCell);
      const progCell = el('div', { style: 'flex: 1 1 auto; min-width: 100px; display:flex; align-items:center; gap:6px;' });
      if (task.status === 'pending') {
        progCell.appendChild(el('span', { class: 'dl-pct', text: '等待…' }));
      } else if (task.status === 'uploading') {
        const bar = el('div', { class: 'dl-bar', style: 'width: 140px;' });
        bar.appendChild(el('div', { class: 'dl-bar-fill', style: 'width:' + task.progress + '%' }));
        progCell.appendChild(bar);
        progCell.appendChild(el('span', { class: 'dl-pct', text: task.progress + '%' }));
      } else if (task.status === 'done') {
        progCell.appendChild(el('span', { class: 'dl-pct', style: 'color:#10b981;', text: '✓ 完成 · ' + formatBytes(task.bytes || 0) }));
      } else if (task.status === 'fail') {
        progCell.appendChild(el('span', { class: 'dl-pct', style: 'color:#ef4444;', title: task.error || '', text: '✕ ' + (task.error || '失败') }));
      } else if (task.status === 'cancel') {
        progCell.appendChild(el('span', { class: 'dl-pct', text: '已取消' }));
      }
      row.appendChild(progCell);
      const btnCell = el('div', { style: 'flex: 0 0 auto; display:flex; gap:4px;' });
      if (task.status === 'uploading') {
        btnCell.appendChild(el('button', { class: 'btn btn-sm btn-danger', text: '取消', onclick: function () { cancelOneUpload(tab, task); } }));
      } else if (task.status === 'fail' || task.status === 'cancel') {
        btnCell.appendChild(el('button', { class: 'btn btn-sm', text: '重试', onclick: function () { retryUpload(tab, task); } }));
        btnCell.appendChild(el('button', { class: 'btn btn-sm', text: '✕', title: '移除', onclick: function () { removeUploadRow(tab, task); } }));
      } else if (task.status === 'done') {
        btnCell.appendChild(el('button', { class: 'btn btn-sm', text: '✕', title: '移除', onclick: function () { removeUploadRow(tab, task); } }));
      }
      row.appendChild(btnCell);
      return row;
    }

    // addUploadFiles 把 FileList 加入 tab 上传队列，并启动串行传输（如未启动）。
    function addUploadFiles(tab, fileList) {
      if (!tab || tab.closed) { toast('tab 已关闭，无法上传', 'warn'); return; }
      if (!tab.sftpCwd) tab.sftpCwd = '/';
      const targetDir = tab.sftpCwd;
      if (!targetDir || targetDir[0] !== '/') {
        toast('目标目录必须是绝对路径', 'warn');
        return;
      }
      for (let i = 0; i < fileList.length; i++) {
        const f = fileList[i];
        tab.sftpUploadQueue.push({
          name: f.name,
          size: f.size,
          status: 'pending',
          progress: 0,
          bytes: 0,
          error: '',
          uploadId: '',
          finalName: f.name,
          targetDir: targetDir,
          overwrite: tab.sftpUploadOverwrite || 'reject',
          file: f,
          xhr: null
        });
      }
      updateUploadQueueUI(tab);
      if (!tab.sftpUploadInflight) startNextUpload(tab);
    }

    // startNextUpload 串行取下一个 pending 任务上传；队列空时按需刷新列表。
    function startNextUpload(tab) {
      if (tab.closed || tab.sftpUploadInflight) return;
      const next = tab.sftpUploadQueue.find(function (t) { return t.status === 'pending'; });
      if (!next) {
        updateUploadQueueUI(tab);
        if (tab.sftpUploadRefresh && tab.sftpCwd) {
          sftpList(tab, tab.sftpCwd);
        }
        return;
      }
      tab.sftpUploadInflight = true;
      next.status = 'uploading';
      next.progress = 0;
      updateUploadQueueUI(tab);
      doUploadOne(tab, next).finally(function () {
        tab.sftpUploadInflight = false;
        startNextUpload(tab);
      });
    }

    // doUploadOne 单文件上传：init → data（XHR 流式）。失败 toast（全局，不限 active tab）。
    async function doUploadOne(tab, task) {
      try {
        const initResp = await api('POST', '/api/ssh/sftp/upload/init', {
          system: tab.system,
          server: tab.server,
          target_dir: task.targetDir,
          filename: task.name,
          size: task.size,
          overwrite: task.overwrite
        });
        task.uploadId = initResp.id;
        if (initResp.max_size && task.size > initResp.max_size) {
          throw new Error('文件大小 ' + formatBytes(task.size) + ' 超过服务端上限 ' + formatBytes(initResp.max_size));
        }
        updateUploadQueueUI(tab);
        await uploadFileData(tab, task);
      } catch (e) {
        task.status = 'fail';
        task.error = e.message || String(e);
        if (task.xhr) {
          try { task.xhr.abort(); } catch (e2) { /* ignore */ }
          task.xhr = null;
        }
        // 注意：失败时保留 task.file，让「重试」能重新发起上传；
        // 文件引用只在 done / 移除 / 关 tab 时释放。
        updateUploadQueueUI(tab);
        toast('上传失败：' + task.name + ' — ' + task.error, 'err');
      }
    }

    // uploadFileData XHR 把 File 作为 raw body 发到 /data，onprogress 算进度。
    function uploadFileData(tab, task) {
      return new Promise(function (resolve, reject) {
        const xhr = new XMLHttpRequest();
        task.xhr = xhr;
        const url = '/api/ssh/sftp/upload/' + encodeURIComponent(task.uploadId) + '/data';
        xhr.open('POST', url, true);
        xhr.upload.onprogress = function (ev) {
          if (ev.lengthComputable) {
            task.bytes = ev.loaded;
            task.progress = Math.min(100, Math.round((ev.loaded / ev.total) * 100));
            updateUploadQueueUI(tab);
          }
        };
        const releaseFile = function () {
          // 仅 done 时释放（fail/cancel 保留 file 供「重试」重新发起）
          if (task.file) {
            try { task.file = null; } catch (e) { /* ignore */ }
          }
        };
        xhr.onload = function () {
          task.xhr = null;
          let resp = null;
          try { resp = JSON.parse(xhr.responseText || '{}'); } catch (e) { /* ignore */ }
          if (xhr.status >= 200 && xhr.status < 300 && resp && resp.ok) {
            task.status = 'done';
            task.bytes = resp.bytes || task.size;
            task.progress = 100;
            task.finalName = resp.final_name || task.name;
            updateUploadQueueUI(tab);
            releaseFile();
            resolve();
          } else {
            const errMsg = (resp && (resp.error || resp.message)) || ('HTTP ' + xhr.status);
            task.status = 'fail';
            task.error = errMsg;
            updateUploadQueueUI(tab);
            reject(new Error(errMsg));
          }
        };
        xhr.onerror = function () {
          task.xhr = null;
          task.status = 'fail';
          task.error = '网络错误';
          updateUploadQueueUI(tab);
          reject(new Error('网络错误'));
        };
        xhr.onabort = function () {
          task.xhr = null;
          task.status = 'cancel';
          task.error = '已取消';
          updateUploadQueueUI(tab);
          resolve();
        };
        try {
          xhr.send(task.file);
        } catch (e) {
          task.xhr = null;
          task.status = 'fail';
          task.error = '发送失败：' + (e.message || e);
          updateUploadQueueUI(tab);
          reject(e);
        }
      });
    }

    // cancelOneUpload 取消单个任务（pending 直接标 cancel；uploading 后端 cancel + abort）。
    // 注意：不释放 task.file —— 「重试」可以重新发起。
    async function cancelOneUpload(tab, task) {
      if (task.status === 'done' || task.status === 'fail' || task.status === 'cancel') return;
      if (task.status === 'pending') {
        task.status = 'cancel';
        task.error = '已取消';
        updateUploadQueueUI(tab);
        return;
      }
      if (task.uploadId) {
        try { await api('POST', '/api/ssh/sftp/upload/cancel', { id: task.uploadId }); }
        catch (e) { /* ignore */ }
      }
      if (task.xhr) {
        try { task.xhr.abort(); } catch (e) { /* onabort 会处理 */ }
      }
    }

    // cancelAllUploads 取消 tab 所有 pending/uploading 任务。
    async function cancelAllUploads(tab) {
      const tasks = tab.sftpUploadQueue.filter(function (t) { return t.status === 'pending' || t.status === 'uploading'; });
      if (!tasks.length) return;
      tasks.forEach(function (t) {
        if (t.status === 'pending') {
          t.status = 'cancel';
          t.error = '已取消';
        }
      });
      updateUploadQueueUI(tab);
      const uploading = tasks.find(function (t) { return t.status === 'uploading'; });
      if (uploading) await cancelOneUpload(tab, uploading);
      updateUploadQueueUI(tab);
      toast('已取消全部待上传任务', 'warn');
    }

    // clearFinishedUploads 清空 done/fail/cancel 任务（移除时释放 File 引用）。
    function clearFinishedUploads(tab) {
      const before = tab.sftpUploadQueue.length;
      const removed = tab.sftpUploadQueue.filter(function (t) { return t.status !== 'pending' && t.status !== 'uploading'; });
      removed.forEach(function (t) {
        if (t.file) { try { t.file = null; } catch (e) { /* ignore */ } }
      });
      tab.sftpUploadQueue = tab.sftpUploadQueue.filter(function (t) { return t.status === 'pending' || t.status === 'uploading'; });
      if (tab.sftpUploadQueue.length !== before) updateUploadQueueUI(tab);
    }

    // retryUpload 失败/取消任务重置为 pending 重新入队。
    function retryUpload(tab, task) {
      if (task.status !== 'fail' && task.status !== 'cancel') return;
      task.status = 'pending';
      task.progress = 0;
      task.bytes = 0;
      task.error = '';
      task.uploadId = '';
      updateUploadQueueUI(tab);
      if (!tab.sftpUploadInflight) startNextUpload(tab);
    }

    // removeUploadRow 从队列移除终态任务（移除时释放 File 引用）。
    function removeUploadRow(tab, task) {
      if (task.status === 'uploading' || task.status === 'pending') return;
      const idx = tab.sftpUploadQueue.indexOf(task);
      if (idx >= 0) {
        if (task.file) { try { task.file = null; } catch (e) { /* ignore */ } }
        tab.sftpUploadQueue.splice(idx, 1);
      }
      updateUploadQueueUI(tab);
    }

    // cancelTabUploads 关闭 tab 时兜底取消该 tab 的上传（abort XHR + beacon 通知后端）。
    function cancelTabUploads(tab) {
      if (!tab || !tab.sftpUploadQueue || !tab.sftpUploadQueue.length) return;
      tab.sftpUploadQueue.forEach(function (t) {
        if (t.status === 'uploading' && t.uploadId && navigator.sendBeacon) {
          try {
            navigator.sendBeacon('/api/ssh/sftp/upload/cancel',
              new Blob([JSON.stringify({ id: t.uploadId })], { type: 'application/json' }));
          } catch (e) { /* ignore */ }
        }
        if (t.xhr) { try { t.xhr.abort(); } catch (e) { /* ignore */ } }
        if (t.file) { try { t.file = null; } catch (e) { /* ignore */ } }
        t.status = 'cancel';
      });
      tab.sftpUploadInflight = false;
    }

    // cancelAllTabsUploads 路由切走（app.js navigate）时取消所有 tab 的上传。
    function cancelAllTabsUploads() {
      pageState.tabs.forEach(function (t) { cancelTabUploads(t); });
    }

    // joinPath 拼接 SFTP 路径：'/a' + 'b' → '/a/b'；'/' + 'b' → '/b'
    function joinPath(dir, name) {
      if (!dir || dir === '/') return '/' + name;
      if (dir.endsWith('/')) return dir + name;
      return dir + '/' + name;
    }

    // sftpList 调后端列目录接口，刷新 tab.sftpEntries
    function sftpList(tab, path) {
      if (tab.closed) { toast('tab 已关闭', 'warn'); return; }
      if (tab.sftpLoading) return;
      tab.sftpLoading = true;
      tab.sftpSelected = new Set();
      tab.sftpLastError = null;
      // 优化 UX：先渲染一次显示「加载中…」
      if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
      api('POST', '/api/ssh/sftp/list', {
        system: tab.system, server: tab.server,
        path: path
      }).then(function (resp) {
        tab.sftpLoading = false;
        tab.sftpCwd = resp.path || path;
        tab.sftpEntries = resp.entries || [];
        tab.sftpBackend = resp.backend || '';
        // 排序：目录在前，文件在后；同类按名称
        tab.sftpEntries.sort(function (a, b) {
          if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
          return a.name.localeCompare(b.name);
        });
        if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
      }).catch(function (e) {
        tab.sftpLoading = false;
        // 修复 UX：失败时保留旧 entries/cwd（用户能看到"上次成功加载的内容"），但记下错误信息
        // 让空状态文案显示成"加载失败：XXX"而不是误导的"目录为空"。
        // 之前版本清空 entries + 显示"目录为空"，让用户以为是空目录，
        // 实际是路径错 / 权限错 / 服务挂了。
        tab.sftpLastError = (e && e.message) ? e.message : String(e);
        toast('列目录失败：' + tab.sftpLastError, 'err');
        if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
      });
    }

    // sftpGoUp 跳到上一级目录
    function sftpGoUp(tab) {
      if (!tab.sftpCwd || tab.sftpCwd === '/') {
        toast('已在根目录', 'idle');
        return;
      }
      const parts = tab.sftpCwd.split('/').filter(Boolean);
      parts.pop();
      const parent = '/' + parts.join('/');
      sftpList(tab, parent || '/');
    }

    // gotoCurrentDir 通过 WS 向活跃 shell 查询真实 cwd（不再用独立连接的 /api/ssh/sftp/pwd，
    // 那样只会拿到新连接的 home 目录，不是终端当前路径）。
    // 流程：发 query_cwd 控制帧 → 后端注入 printf 命令输出 OSC 999 私有序列包裹 $PWD →
    // 扫描 stdout 中的起止标记 → 提取路径 → 发回 cwd 事件。
    // v0.13：OSC 999 序列对用户不可见（xterm.js 忽略未知 OSC），仅 printf 命令行会被 shell 回显；
    //        若 SSH server/PTY 剥离 OSC 序列（老 AIX/WebSphere），后端 2.5s 后自动降级为字面标记重试。
    function gotoCurrentDir(tab) {
      if (tab.closed) { toast('tab 已关闭', 'warn'); return; }
      if (!tab.ws || tab.ws.readyState !== 1) { toast('SSH 未连接', 'warn'); return; }
      if (tab.sftpCwdQueryId) { toast('正在获取目录，请稍候…', 'idle'); return; }
      const qid = 'cwd_' + tab.id + '_' + Date.now() + '_' + Math.random().toString(36).slice(2, 6);
      tab.sftpCwdQueryId = qid;
      try {
        tab.ws.send(JSON.stringify({ type: 'query_cwd', cwd_id: qid }));
      } catch (e) {
        tab.sftpCwdQueryId = null;
        toast('发送目录查询失败：' + (e.message || e), 'err');
        return;
      }
      toast('正在获取终端当前目录…', 'idle');
      tab.sftpCwdQueryTimer = setTimeout(function () {
        if (tab.sftpCwdQueryId === qid) {
          tab.sftpCwdQueryId = null;
          tab.sftpCwdQueryTimer = null;
          toast('获取终端目录超时，请重试', 'warn');
        }
      }, 5000);
    }

    // sftpPreview 在新 tab 打开预览（文本文件）
    function sftpPreview(tab, fullPath) {
      const url = '/preview.html?system=' + encodeURIComponent(tab.system) +
        '&server=' + encodeURIComponent(tab.server) +
        '&path=' + encodeURIComponent(fullPath) +
        '&encoding=' + (tab.encoding || 'utf-8') +
        '&source=ssh-sftp';
      const SftpCommon = (window.Kairo && window.Kairo.SftpCommon) || {};
      if (SftpCommon.openPreviewWindow) {
        SftpCommon.openPreviewWindow(url);
      } else {
        window.open(url, '_blank');
      }
    }

    // buildEditButtons 构建编辑按钮（使用用户配置的外部打开器）
    // v0.14：opener 图标统一走 Kairo.icons.openerIconHTML（emoji / exe 真实图标 / SVG fallback）。
    function buildEditButtons(fileName, fullPath, tab) {
      const openers = Kairo.state.downloadsOpeners || [];
      if (!openers || openers.length === 0) {
        return [
          el('button', {
            class: 'btn btn-sm btn-disabled',
            text: '编辑',
            title: '请先在系统配置中添加外部打开器（如 Notepad++、VS Code）',
            disabled: true
          })
        ];
      }
      return openers.map(op => {
        const iconHtml = (window.Kairo && Kairo.icons && Kairo.icons.openerIconHTML)
          ? Kairo.icons.openerIconHTML(op, 14)
          : '📝';
        const tip = (op.name || '') + (op.path ? ' — ' + op.path : '') + '\n保存后自动上传到服务器';
        return el('button', {
          class: 'btn btn-sm',
          title: tip,
          style: 'display:inline-flex; align-items:center; gap:4px;',
          onclick: function (e) {
            e.stopPropagation();
            sftpEdit(tab, fullPath, op.name);
          },
          // op.name 来自 /api/admin/openers（用户配置），未做服务端长度/字符限制。
          // 必须 escapeHtml，否则 `<img src=x onerror=alert(1)>` 会执行。
          unsafeHtml: iconHtml + ' ' + escapeHtml(op.name || '编辑')
        });
      });
    }

    // sftpEdit 编辑远程文件：下载到临时目录 → 用外部编辑器打开 → 监控保存 → 自动上传
    function sftpEdit(tab, fullPath, openerName) {
      const creds = getCreds(tab.system, tab.server);
      api('POST', '/api/ssh/sftp/edit', {
        system: tab.system,
        server: tab.server,
        username: creds.username || '',
        password: creds.password || '',
        path: fullPath,
        opener: openerName
      }).then(function (r) {
        toast('已用 ' + openerName + ' 打开文件，保存后自动上传', 'success');
        if (r && r.id) {
          subscribeEditEvents(r.id, fullPath);
        }
      }).catch(function (e) {
        if (e.message && e.message.includes('未找到打开器')) {
          toast('未找到打开器 "' + openerName + '"，请先在系统配置中添加', 'warn');
        } else if (e.message && e.message.includes('文件超过大小限制')) {
          toast('文件超过 200MB 限制，无法编辑', 'warn');
        } else {
          toast('编辑失败: ' + e.message, 'error');
        }
      });
    }

    function subscribeEditEvents(taskId, filePath) {
      const SftpCommon = (window.Kairo && window.Kairo.SftpCommon) || {};
      const base = SftpCommon.buildSseBaseUrl ? SftpCommon.buildSseBaseUrl() : '';
      const url = base + '/api/ssh/sftp/edit/' + encodeURIComponent(taskId) + '/events';
      let es;
      try {
        es = new EventSource(url);
      } catch (e) {
        return;
      }
      const fileName = filePath.split('/').pop() || filePath;
      es.addEventListener('upload_start', function () {
        toast('正在上传 ' + fileName + ' …', 'idle');
      });
      es.addEventListener('upload_ok', function (e) {
        let sizeText = '';
        try {
          const data = JSON.parse(e.data);
          if (data.bytes) {
            const fmt = SftpCommon.formatBytes || function (n) { return n + ' B'; };
            sizeText = '（' + fmt(data.bytes) + '）';
          }
        } catch (_) {}
        toast(fileName + ' 已上传' + sizeText, 'success');
      });
      es.addEventListener('upload_fail', function (e) {
        let msg = '';
        try { const data = JSON.parse(e.data); msg = data.message || ''; } catch (_) {}
        toast(fileName + ' 上传失败: ' + (msg || '未知错误'), 'error');
      });
      es.addEventListener('editor_closed', function () {
        toast('编辑器进程已退出，如仍在编辑，保存后仍会自动上传', 'idle');
      });
      es.addEventListener('done', function () {
        es.close();
      });
      es.onerror = function () {
        es.close();
      };
    }

    // sftpDownloadOne 下载单个文件
    function sftpDownloadOne(tab, fullPath) {
      sftpDownloadPaths(tab, [fullPath]);
    }

    // sftpDownloadSelected 下载所有选中文件
    function sftpDownloadSelected(tab) {
      if (tab.sftpSelected.size === 0) {
        toast('请先选中文件（单击文件行）', 'warn');
        return;
      }
      const paths = [];
      tab.sftpSelected.forEach(function (name) {
        paths.push(joinPath(tab.sftpCwd, name));
      });
      sftpDownloadPaths(tab, paths);
    }

    // sftpDownloadPaths 启动下载任务 + 订阅 SSE 进度
    function sftpDownloadPaths(tab, paths) {
      // 防双击：用 starting flag + sftpDlId 双重保护，
      // 避免 POST 还没返回时快速点击发两次请求。
      if (tab.sftpDlId || tab.sftpDlStarting) {
        toast('当前已有下载任务进行中，请等待完成或取消', 'warn');
        return;
      }
      tab.sftpDlStarting = true;
      // 同步禁用顶部下载按钮
      const btnDownload = filesPanelEl.querySelector('.sftp-actions .btn-primary');
      if (btnDownload) btnDownload.disabled = true;
      const SftpCommon = (window.Kairo && window.Kairo.SftpCommon) || {};
      const payload = {
        system: tab.system, server: tab.server,
        paths: paths, zip: paths.length >= 2
      };
      SftpCommon.apiDownloadSshSftp(payload).then(function (r) {
        tab.sftpDlStarting = false;
        tab.sftpDlId = r.id;
        tab.sftpDlProgress = { total: paths.length, done: 0, current: '', fileWritten: 0, fileTotal: 0 };
        const targetDir = r.target_dir || '';
        tab.sftpLastDlNotifyId = 'ssh-sftp-dl-' + r.id;
        toast('已启动下载（' + paths.length + ' 个文件）' + (targetDir ? ' → ' + targetDir : ''), 'idle');
        if (tab.id === pageState.activeTabId) updateSftpStatusBar(tab);
        tab.sftpDlEvtSrc = SftpCommon.subscribeDownload(r.id, SftpCommon.ROUTE_SSH_SFTP, {
          onMessage: function (o) {
            if (!o) return;
            if (o.kind === 'file_start') {
              if (tab.sftpDlProgress) {
                tab.sftpDlProgress.current = (o.file || '').split('/').pop();
                tab.sftpDlProgress.fileWritten = 0;
                tab.sftpDlProgress.fileTotal = 0;
                // v1.4：目录递归下载时后端展开成 N 个文件，用事件里的 total 校准
                // 进度分母（前端初始按"选中条目数"计，目录会偏低）。
                if (o.total && o.total > 0) tab.sftpDlProgress.total = o.total;
              }
              if (tab.id === pageState.activeTabId) updateSftpStatusBar(tab);
            } else if (o.kind === 'file_done') {
              if (tab.sftpDlProgress) {
                tab.sftpDlProgress.done = (tab.sftpDlProgress.done || 0) + 1;
                tab.sftpDlProgress.current = '';
                tab.sftpDlProgress.fileWritten = 0;
                tab.sftpDlProgress.fileTotal = 0;
              }
              if (tab.id === pageState.activeTabId) updateSftpStatusBar(tab);
            } else if (o.kind === 'progress') {
              if (tab.sftpDlProgress) {
                tab.sftpDlProgress.fileWritten = parseInt(o.written, 10) || 0;
                tab.sftpDlProgress.fileTotal = parseInt(o.total, 10) || 0;
              }
              if (tab.id === pageState.activeTabId) updateSftpStatusBar(tab);
            }
          },
          onDone: function (o) {
            tab.sftpDlStarting = false;
            tab.sftpDlId = null;
            tab.sftpDlEvtSrc = null;
            tab.sftpDlProgress = null;
            if (o && o.ok) {
              const downloads = o.downloads || [];
              const folder = o.folder || '';
              const fileCount = downloads.length;
              const firstAbsPath = downloads[0] && downloads[0].abs_path || '';
              const fileList = downloads.slice(0, 3).map(function (d) { return d.local || d.name || ''; }).filter(Boolean).join(', ');
              const more = downloads.length > 3 ? (' 等 ' + downloads.length + ' 个') : '';
              const title = '✓ 下载完成 · ' + fileCount + ' 个文件';
              const body = folder + (fileList ? ('\n' + fileList + more) : '');
              const actions = [];
              if (firstAbsPath) {
                actions.push({
                  html: '<span style="display:inline-flex;align-items:center;gap:4px;">' + svgIcon('smFolder', 14) + ' 打开所在目录</span>',
                  callback: function () {
                    api('POST', '/api/local/reveal-file', { path: firstAbsPath }).catch(function (e) {
                      toast('打开目录失败：' + e.message, 'err');
                    });
                  }
                });
              }
              if (folder) {
                actions.push({
                  html: '<span style="display:inline-flex;align-items:center;gap:4px;">' + svgIcon('smClipboard', 14) + ' 复制路径</span>',
                  callback: function () {
                    Kairo.core.copyToClipboard(folder).then(function () {
                      toast('路径已复制', 'ok');
                    }).catch(function () {
                      window.prompt('复制此路径：', folder);
                    });
                  }
                });
              }
              actions.push({
                html: '<span style="display:inline-flex;align-items:center;gap:4px;">' + svgIcon('smScroll', 14) + ' 查看下载历史</span>',
                callback: function () {
                  if (location.hash !== '#/downloads') location.hash = '#/downloads';
                }
              });
              Kairo.core.notify({
                id: tab.sftpLastDlNotifyId || ('ssh-sftp-dl-' + Date.now()),
                type: 'ok',
                title: title,
                body: body,
                actions: actions,
                duration: 8000
              });
              if (location.hash !== '#/downloads') {
                Kairo.core.bumpDlBadge(fileCount);
              }
              try { window.dispatchEvent(new CustomEvent('kairo:downloads-updated')); } catch (e) { /* ignore */ }
            } else {
              toast('下载失败：' + (o && o.error ? o.error : '未知错误'), 'err');
            }
            if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
          },
          onError: function (reason) {
            if (reason === 'error') {
              toast('SSE 连接异常，下载状态可能不一致（请查看下载历史）', 'warn');
              tab.sftpDlStarting = false;
              tab.sftpDlId = null;
              tab.sftpDlEvtSrc = null;
              tab.sftpDlProgress = null;
              if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
            }
          }
        });
      }).catch(function (e) {
        tab.sftpDlStarting = false;
        toast('启动下载失败：' + (e.message || e), 'err');
        if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
      });
    }

    // cancelSftpDownload 取消当前 tab 的下载任务
    function cancelSftpDownload(tab) {
      if (!tab.sftpDlId) {
        toast('当前没有进行中的下载', 'warn');
        return;
      }
      const id = tab.sftpDlId;
      api('POST', '/api/ssh/sftp/download/' + id + '/cancel', {}).then(function () {
        toast('已发送取消请求', 'idle');
        // 后端会触发 done 事件，SSE onDone 会清理 tab.sftpDlId
      }).catch(function (e) {
        toast('取消失败：' + (e.message || e), 'err');
        // 失败也强制本地清理（防止状态卡死）
        tab.sftpDlId = null;
        tab.sftpDlProgress = null;
        if (tab.sftpDlEvtSrc) {
          try { tab.sftpDlEvtSrc.close(); } catch (err) { /* ignore */ }
          tab.sftpDlEvtSrc = null;
        }
        if (tab.id === pageState.activeTabId) renderSftpPanelContent(tab);
      });
    }

    function reconnectActive() {
      const tab = getActiveTab();
      if (!tab) { toast('没有活跃的 tab', 'warn'); return; }
      // Bug#8：重连前先清屏。专业终端（Xshell/iTerm）惯例：
      // 重连 = 新会话 = 旧输出没意义。多 tab 用户经常重连找老 prompt，结果
      // 看到一堆过时的"重连中…"和重复 zsh 欢迎语以为连接坏了。
      if (tab.term) {
        try { tab.term.clear(); } catch (e) { /* ignore */ }
        tab.term.write('\r\n\x1b[36m重连中…\x1b[0m\r\n');
      }
      // Bug#10：toast 反馈一致。重连/关闭 tab 这类主动操作补个 toast，
      // 用户不需要回头看 xterm 才知道操作生效了。
      toast('正在重连 ' + tab.systemName + ' / ' + tab.serverName + '…', 'idle');
      if (!tab.closed && tab.ws && tab.ws.readyState === 1) {
        sendControl(tab, { type: 'signal', signal: 'SIGKILL' });
        setTimeout(function () {
          if (tab.ws) try { tab.ws.close(); } catch (e) { /* ignore */ }
          tab.closed = false;
          tab.reconnecting = true;
          connectWS(tab);
        }, 200);
        return;
      }
      tab.closed = false;
      tab.reconnecting = true;
      if (tab.ws) { try { tab.ws.close(); } catch (e) { /* ignore */ } }
      connectWS(tab);
    }

    // Bug#1 / Bug#16：把当前 active tab 拆到独立窗口。
    // 行为：开新 tab 跳 /ssh.html?system=...&server=...，URL 编码跟现有 ws 一致。
    // 原 tab 保留不动（用户可能想一边 SSH 一边 SFTP 浏览），所以用 window.open
    // 而不是迁移。重复点会重复开窗，避免用户困惑。
    function popOutActive() {
      const tab = getActiveTab();
      if (!tab) { toast('没有活跃的 tab', 'warn'); return; }
      const url = '/ssh.html?system=' + encodeURIComponent(tab.system) +
        '&server=' + encodeURIComponent(tab.server) +
        '&cols=' + (tab.cols || 80) +
        '&rows=' + (tab.rows || 24) +
        '&encoding=' + encodeURIComponent(tab.encoding || 'utf-8');
      const w = window.open(url, '_blank');
      if (!w) {
        toast('浏览器拦截了新窗口，请允许弹出窗后重试', 'err');
      } else {
        toast('已在新窗口打开 ' + tab.systemName + ' / ' + tab.serverName, 'ok');
      }
    }

    function getActiveTab() {
      return pageState.tabs.find(function (t) { return t.id === pageState.activeTabId; });
    }

    function sendControl(tab, obj) {
      if (tab.ws && tab.ws.readyState === 1) {
        tab.ws.send(JSON.stringify(obj));
      }
    }
  }

  function escapeTermText(s) {
    if (!s) return '';
    return String(s).replace(/\x1b\[[0-9;]*m/g, '');
  }

  Kairo.state = Kairo.state || {};
  Kairo.state.routes = Kairo.state.routes || {};
  Kairo.state.routeNames = Kairo.state.routeNames || {};
  Kairo.state.routes.ssh = renderSSH;
  Kairo.state.routeNames.ssh = 'SSH 终端';
})();
