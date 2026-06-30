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
  const { el, $, toast, cssEscape, escapeHtml } = Kairo.core;
  const { api } = Kairo.api;

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

  function renderSSH(view) {
    const pageState = {
      cfg: null,
      tabs: [],
      activeTabId: null,
      nextId: 1,
      disposed: false,
    };

    // ---- 布局 ----
    const pageEl = el('div', { class: 'ssh-page' });

    const sidebarEl = el('div', { class: 'ssh-sidebar' });
    sidebarEl.appendChild(el('div', { class: 'ssh-sidebar-title', text: '主机列表' }));
    const hostListEl = el('div', { class: 'ssh-host-list' });
    sidebarEl.appendChild(hostListEl);
    const sidebarHint = el('div', { class: 'ssh-sidebar-hint text-dim', text: '点击主机开 shell' });
    sidebarEl.appendChild(sidebarHint);

    const mainEl = el('div', { class: 'ssh-main' });

    const tabBarEl = el('div', { class: 'ssh-tabbar' });
    const toolbarEl = el('div', { class: 'ssh-toolbar' });
    const termAreaEl = el('div', { class: 'ssh-term-area' });

    const emptyHint = el('div', { class: 'ssh-empty' }, [
      el('div', { class: 'ssh-empty-icon', text: '>' }),
      el('div', { class: 'ssh-empty-text', text: '从左侧选择一台主机开始 SSH 会话' })
    ]);
    termAreaEl.appendChild(emptyHint);

    mainEl.appendChild(tabBarEl);
    mainEl.appendChild(toolbarEl);
    mainEl.appendChild(termAreaEl);

    pageEl.appendChild(sidebarEl);
    pageEl.appendChild(mainEl);
    view.appendChild(pageEl);

    // ---- 工具栏按钮 ----
    const btnCtrlC = el('button', { class: 'btn btn-sm', text: 'Ctrl+C', title: '发送 SIGINT', onclick: sendCtrlC, disabled: true });
    const btnClear = el('button', { class: 'btn btn-sm', text: '清屏', title: '清屏（clear）', onclick: clearActive, disabled: true });
    const btnReconnect = el('button', { class: 'btn btn-sm', text: '重连', title: '断开重连', onclick: reconnectActive, disabled: true });
    const btnNewWindow = el('button', { class: 'btn btn-sm', text: '↗新窗口', title: '在独立窗口打开', onclick: openInNewWindow, disabled: true });
    const btnCloseTab = el('button', { class: 'btn btn-sm btn-danger', text: '关闭 tab', title: '关闭当前 tab', onclick: closeActiveTab, disabled: true });
    const activeLabel = el('span', { class: 'ssh-active-label text-dim', text: '' });
    // 编码选择器：UTF-8 / GBK（老 WebSphere / Oracle 终端常见）
    const encodingSel = el('select', { class: 'btn btn-sm', style: 'max-width:90px;' });
    encodingSel.appendChild(el('option', { value: 'utf-8', text: 'UTF-8' }));
    encodingSel.appendChild(el('option', { value: 'gbk', text: 'GBK' }));
    encodingSel.addEventListener('change', function () {
      const tab = getActiveTab();
      if (tab && !tab.closed) {
        tab.encoding = encodingSel.value;
        // 编码变更时自动重连以新编码通信
        reconnectActive();
      }
    });
    toolbarEl.appendChild(btnCtrlC);
    toolbarEl.appendChild(btnClear);
    toolbarEl.appendChild(btnReconnect);
    toolbarEl.appendChild(encodingSel);
    toolbarEl.appendChild(btnNewWindow);
    toolbarEl.appendChild(btnCloseTab);
    toolbarEl.appendChild(activeLabel);

    function updateToolbarButtons() {
      const tab = getActiveTab();
      const hasActive = !!tab && !tab.closed;
      btnCtrlC.disabled = !hasActive;
      btnClear.disabled = !hasActive;
      btnReconnect.disabled = !hasActive;
      btnNewWindow.disabled = !hasActive;
      btnCloseTab.disabled = !hasActive;
      // 同步编码选择器
      if (tab && !tab.closed) {
        encodingSel.value = tab.encoding || 'utf-8';
      } else {
        encodingSel.value = 'utf-8';
      }
    }

    Kairo.core.setActiveShells({
      closeAll: function () { closeAllTabs(); }
    });

    function syncTermAreaBg() {
      const theme = currentXtermTheme();
      if (termAreaEl && theme.background) {
        termAreaEl.style.background = theme.background;
      }
    }

    // ---- 主题联动 ----
    function onThemeChange() {
      const theme = currentXtermTheme();
      pageState.tabs.forEach(function (tab) {
        if (tab.term && !tab.closed) {
          try { tab.term.options.theme = theme; } catch (e) { /* ignore */ }
        }
      });
      syncTermAreaBg();
    }
    window.addEventListener('kairo:themechange', onThemeChange);
    syncTermAreaBg();
    updateToolbarButtons();

    loadConfig();

    function loadConfig() {
      const cached = Kairo.state && Kairo.state.bootInfo;
      if (cached && cached.systems) {
        pageState.cfg = cached;
        renderHostList();
        return;
      }
      api('GET', '/api/config').then(function (info) {
        pageState.cfg = info;
        renderHostList();
      }).catch(function (e) {
        hostListEl.innerHTML = '';
        hostListEl.appendChild(el('div', { class: 'text-err', text: '加载配置失败：' + (e.message || e) }));
      });
    }

    function renderHostList() {
      hostListEl.innerHTML = '';
      const cfg = pageState.cfg;
      if (!cfg || !cfg.systems || !cfg.systems.length) {
        hostListEl.appendChild(el('div', { class: 'text-dim', text: '没有配置任何系统，请先去「系统配置」添加' }));
        return;
      }
      cfg.systems.forEach(function (sys) {
        const sysGroup = el('div', { class: 'ssh-sys-group' });
        const sysHeader = el('div', { class: 'ssh-sys-header', text: sys.name });
        sysGroup.appendChild(sysHeader);
        if (!sys.servers || !sys.servers.length) {
          sysGroup.appendChild(el('div', { class: 'ssh-srv-item text-dim', text: '（无服务器）' }));
          hostListEl.appendChild(sysGroup);
          return;
        }
        sys.servers.forEach(function (srv) {
          const srvItem = el('div', {
            class: 'ssh-srv-item',
            title: srv.host + ':' + (srv.port || 22) + ' (' + (srv.username || '-') + ')',
            onclick: function () { openTab(sys, srv); }
          }, [
            el('span', { class: 'ssh-srv-name', text: srv.name }),
            el('span', { class: 'ssh-srv-host text-dim', text: srv.host + ':' + (srv.port || 22) })
          ]);
          sysGroup.appendChild(srvItem);
        });
        hostListEl.appendChild(sysGroup);
      });
    }

    // ---- 开 tab ----
    function openTab(sys, srv) {
      const existing = pageState.tabs.find(function (t) {
        return t.system === sys.name && t.server === srv.name && !t.closed;
      });
      if (existing) {
        activateTab(existing.id);
        return;
      }

      const TerminalCtor = getTerminalCtor();
      if (!TerminalCtor) {
        toast('xterm.js 未加载，请刷新页面重试', 'err');
        return;
      }

      const tabId = pageState.nextId++;

      const termEl = el('div', { class: 'ssh-term', 'data-tab': String(tabId) });

      const tabBtn = el('div', {
        class: 'ssh-tab',
        'data-tab': String(tabId),
        onclick: function () { activateTab(tabId); }
      }, [
        el('span', { class: 'ssh-tab-name', text: srv.name }),
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
        encoding: 'utf-8', // 终端编码：UTF-8（默认）或 GBK
        term: null,
        fitAddon: null,
        searchAddon: null,
        ws: null,
        termEl: termEl,
        tabBtn: tabBtn,
        closed: false,
        reconnecting: false,
        cols: DEFAULT_COLS,
        rows: DEFAULT_ROWS,
        resizeObs: null,
        _pwdOverlay: null,
      };
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
          rendererType: 'canvas',
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
            if (tab.fitAddon && !tab.closed) {
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
        updateTabStatus(tab);
        updateToolbarButtons();
        return;
      }
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
          updateTabStatus(tab);
          updateToolbarButtons();
          break;
        case 'error':
          handleErrorFrame(tab, msg);
          break;
        case 'pong':
          break;
      }
    }

    function handleErrorFrame(tab, msg) {
      const reason = msg.reason || '';
      const message = msg.message || '';
      if (!tab.term) {
        tab.closed = true;
        updateTabStatus(tab);
        updateToolbarButtons();
        return;
      }
      if (reason === 'no_password') {
        tab.term.write('\r\n\x1b[36m需要 SSH 密码\x1b[0m\r\n');
        promptPassword(tab);
      } else if (reason === 'cred_resolve') {
        tab.term.write('\r\n\x1b[31m凭据解析失败: ' + escapeTermText(message) + '\x1b[0m\r\n');
        tab.closed = true;
      } else if (reason === 'dial_failed') {
        tab.term.write('\r\n\x1b[31mSSH 连接失败: ' + escapeTermText(message) + '\x1b[0m\r\n');
        tab.term.write('\r\n\x1b[2m  （检查 host/port/网络/host_key 配置）\x1b[0m\r\n');
        tab.closed = true;
      } else if (reason === 'shell_failed') {
        tab.term.write('\r\n\x1b[31m启动 shell 失败: ' + escapeTermText(message) + '\x1b[0m\r\n');
        tab.closed = true;
      } else if (reason === 'max_sessions') {
        tab.term.write('\r\n\x1b[31m' + escapeTermText(message || '并发会话已达上限') + '\x1b[0m\r\n');
        tab.closed = true;
      } else {
        tab.term.write('\r\n\x1b[31m错误: ' + escapeTermText(message || reason) + '\x1b[0m\r\n');
        tab.closed = true;
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
      updateActiveLabel();
      updateToolbarButtons();
    }

    function updateActiveLabel() {
      const tab = pageState.tabs.find(function (t) { return t.id === pageState.activeTabId; });
      if (!tab) {
        activeLabel.textContent = '';
        return;
      }
      var status = '';
      if (tab.closed) status = '（已断开）';
      else if (tab.ws && tab.ws.readyState === 0) status = '（连接中…）';
      else if (tab.ws && tab.ws.readyState === 1) status = '（已连接）';
      activeLabel.textContent = tab.systemName + ' / ' + tab.serverName + ' (' + (tab.username || '-') + '@' + tab.host + ':' + tab.port + ')' + status;
    }

    function updateTabStatus(tab) {
      if (!tab.tabBtn) return;
      if (tab.closed) {
        tab.tabBtn.classList.add('ssh-tab-closed');
      } else {
        tab.tabBtn.classList.remove('ssh-tab-closed');
      }
      updateActiveLabel();
    }

    function closeTab(tabId) {
      const idx = pageState.tabs.findIndex(function (t) { return t.id === tabId; });
      if (idx === -1) return;
      const tab = pageState.tabs[idx];
      tab.closed = true;
      if (tab.ws) { try { tab.ws.close(); } catch (e) { /* ignore */ } }
      if (tab.resizeObs) { try { tab.resizeObs.disconnect(); } catch (e) { /* ignore */ } }
      if (tab.term) { try { tab.term.dispose(); } catch (e) { /* ignore */ } }
      if (tab.tabBtn) tab.tabBtn.remove();
      if (tab.termEl) tab.termEl.remove();
      if (tab._pwdOverlay) tab._pwdOverlay.remove();
      pageState.tabs.splice(idx, 1);

      if (pageState.activeTabId === tabId) {
        if (pageState.tabs.length > 0) {
          activateTab(pageState.tabs[pageState.tabs.length - 1].id);
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
      const ids = pageState.tabs.map(function (t) { return t.id; });
      ids.forEach(closeTab);
      pageState.disposed = true;
      window.removeEventListener('kairo:themechange', onThemeChange);
      Kairo.core.setActiveShells(null);
    }

    // ---- 工具栏动作 ----
    function sendCtrlC() {
      const tab = getActiveTab();
      if (!tab || !tab.ws || tab.ws.readyState !== 1) { toast('未连接', 'warn'); return; }
      sendControl(tab, { type: 'signal', signal: 'SIGINT' });
    }

    function clearActive() {
      const tab = getActiveTab();
      if (!tab || !tab.term) { toast('没有活跃的 tab', 'warn'); return; }
      tab.term.clear();
    }

    function reconnectActive() {
      const tab = getActiveTab();
      if (!tab) { toast('没有活跃的 tab', 'warn'); return; }
      if (!tab.closed && tab.ws && tab.ws.readyState === 1) {
        sendControl(tab, { type: 'signal', signal: 'SIGKILL' });
        setTimeout(function () {
          if (tab.ws) try { tab.ws.close(); } catch (e) { /* ignore */ }
          tab.closed = false;
          tab.reconnecting = true;
          if (tab.term) tab.term.write('\r\n\x1b[36m重连中…\x1b[0m\r\n');
          connectWS(tab);
        }, 200);
        return;
      }
      tab.closed = false;
      tab.reconnecting = true;
      if (tab.term) tab.term.write('\r\n\x1b[36m重连中…\x1b[0m\r\n');
      if (tab.ws) { try { tab.ws.close(); } catch (e) { /* ignore */ } }
      connectWS(tab);
    }

    function openInNewWindow() {
      const tab = getActiveTab();
      if (!tab) { toast('没有活跃的 tab', 'warn'); return; }
      const url = '/static/ssh.html?system=' + encodeURIComponent(tab.system) +
        '&server=' + encodeURIComponent(tab.server) +
        '&cols=' + tab.cols + '&rows=' + tab.rows;
      window.open(url, '_blank');
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
