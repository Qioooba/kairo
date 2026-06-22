/* ===== 内网运维工具箱 · 前端逻辑 =====
 * 纯原生 JS，不依赖外部资源。
 * 路由方式：hash 路由（#/home 等）。
 */

(function () {
  'use strict';

  // -------- 工具 --------
  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

  // el(tag, attrs, children)
  //
  // attrs 特殊键：
  //   - 'class'        → element.className
  //   - 'text'         → element.textContent（**安全**，自动 escape）
  //   - 'unsafeHtml'   → element.innerHTML（**危险**！仅用于硬编码 HTML，
  //                      或已经过 escapeHtml 处理的用户输入；
  //                      旧的 'html' 键名已废弃，避免误用）
  //   - 'on*'          → addEventListener
  //   - 其它           → setAttribute
  function el(tag, attrs, children) {
    const e = document.createElement(tag);
    if (attrs) {
      for (const k in attrs) {
        if (k === 'class') e.className = attrs[k];
        else if (k === 'text') e.textContent = attrs[k];
        else if (k === 'html') {
          // 兼容老调用：warn 但仍然执行，避免回归
          console.warn("[ops-toolbox] el(..., { html: ... }) is deprecated; use 'unsafeHtml' to make intent explicit, or 'text' to auto-escape.");
          e.innerHTML = attrs[k];
        } else if (k === 'unsafeHtml') e.innerHTML = attrs[k];
        else if (k.indexOf('on') === 0) e.addEventListener(k.slice(2), attrs[k]);
        else e.setAttribute(k, attrs[k]);
      }
    }
    if (children) {
      (Array.isArray(children) ? children : [children]).forEach(c => {
        if (c == null) return;
        if (typeof c === 'string') e.appendChild(document.createTextNode(c));
        else e.appendChild(c);
      });
    }
    return e;
  }

  // 防止 innerHTML 注入 — 简易转义
  function escapeHtml(s) {
    if (s == null) return '';
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function toast(msg, type) {
    const t = $('#toast');
    t.textContent = msg;
    t.className = 'toast show' + (type ? ' ' + type : '');
    clearTimeout(toast._timer);
    toast._timer = setTimeout(() => { t.className = 'toast'; }, 2400);
  }

  function setStatus(state, text) {
    const dot = $('#status-dot');
    const txt = $('#status-text');
    dot.className = 'dot dot-' + (state || 'idle');
    txt.textContent = text || (state === 'busy' ? '处理中…' : '就绪');
  }

  async function api(method, path, body) {
    setStatus('busy');
    try {
      const opts = { method, headers: {} };
      if (body !== undefined) {
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(body);
      }
      const resp = await fetch(path, opts);
      let data = null;
      try { data = await resp.json(); } catch (e) { /* ignore */ }
      if (!resp.ok) {
        const msg = (data && data.error) ? data.error : ('HTTP ' + resp.status);
        throw new Error(msg);
      }
      setStatus('ok', '完成');
      setTimeout(() => setStatus('idle'), 800);
      return data;
    } catch (err) {
      setStatus('err', '失败');
      setTimeout(() => setStatus('idle'), 1500);
      throw err;
    }
  }

  function formatBytes(n) {
    n = Number(n) || 0;
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return n.toFixed(i ? 1 : 0) + ' ' + u[i];
  }

  function formatTime(s) {
    if (!s) return '-';
    try { return new Date(s).toLocaleString(); } catch (e) { return s; }
  }

  // -------- 路由 --------
  const routes = {
    home: renderHome,
    websphere: renderWebsphere,
    files: renderFiles,
    formatter: renderFormatter,
    commands: renderPlaceholder,
    diagnostics: renderDiagnostics,
    config: renderConfig,
    downloads: renderDownloads,
    history: renderHistory
  };

  const routeNames = {
    home: '首页',
    websphere: 'WebSphere 日志助手',
    files: '文件下载（FTP 风格）',
    formatter: '报文格式化',
    commands: '常用命令',
    diagnostics: '环境自检',
    config: '系统配置',
    downloads: '下载历史',
    history: '操作历史'
  };

  const routeSubs = {
    commands: '功能建设中 — grep / find / tail / WebSphere 排查模板',
    downloads: 'downloads/ 目录里所有已下载的日志（zip / 单文件）+ 来源、删除',
    history: '审计日志（按操作类型 / 状态 / 关键字过滤；最近 N 条）',
    diagnostics: '本机 / 网络 / 配置 / 工具 / 每台 server 连通性快速体检'
  };

  // 全局进行中的下载任务（用于 navigate 清理）
  // 引用挂这里而不是闭包里，是因为 renderWebsphere 重建后旧 EventSource 没人能关。
  window.__opsActiveDL = null;

  function navigate() {
    const hash = (location.hash || '#/home').replace(/^#\//, '');
    const name = routes[hash] ? hash : 'home';
    // 离开 websphere 页面前：清理进行中的下载（关闭 SSE + 通知后端取消）
    if (window.__opsActiveDL) {
      const dl = window.__opsActiveDL;
      window.__opsActiveDL = null;
      try { dl.evtsrc && dl.evtsrc.close(); } catch (e) { /* ignore */ }
      if (dl.id) {
        // 异步取消，不阻塞 navigate
        api('POST', '/api/files/download/' + dl.id + '/cancel', {}).catch(() => {});
      }
    }
    const view = $('#view');
    view.innerHTML = '';
    try {
      routes[name](view);
    } catch (e) {
      view.appendChild(el('div', { class: 'card' }, [
        el('h3', { text: '页面渲染失败' }),
        el('div', { class: 'text-err', text: e.message })
      ]));
    }
    $('#crumbs').textContent = routeNames[name] || '未知';
    $$('.nav-item').forEach(a => {
      a.classList.toggle('active', a.getAttribute('data-route') === name);
    });
  }

  window.addEventListener('hashchange', navigate);
  window.addEventListener('load', async () => {
    try {
      const info = await api('GET', '/api/config');
      const appName = (info.app && info.app.name) || 'OpsToolbox';
      const dlFolder = info.paths && info.paths.download_dir;
      $('#listen-info').textContent = '已启动 · ' + appName + (dlFolder ? ' · 保存到 ' + dlFolder : '');
    } catch (e) { /* 忽略 */ }
    navigate();
  });

  // -------- 各页面 --------

  // 首页
  function renderHome(view) {
    view.appendChild(el('div', { class: 'mb-3' }, [
      el('div', { class: 'section-title', text: '内网运维工具箱' }),
      el('div', { class: 'section-sub', text: 'WebSphere 日志检索、报文格式化、常用运维辅助工具。' })
    ]));

    const tools = [
      { id: 'websphere', name: 'WebSphere 日志助手', desc: '多服务器日志并行搜索、上下文查看、日志下载', icon: '📜', tag: 'ready', tagText: '已就绪' },
      { id: 'files', name: '文件下载', desc: '按 SSH 账号权限浏览任意目录，像 FTP 一样层层进入并下载', icon: '📁', tag: 'ready', tagText: 'v0.3 新增' },
      { id: 'formatter', name: '报文格式化', desc: 'JSON / XML 格式化、压缩、校验', icon: '⌗', tag: 'ready', tagText: '已就绪' },
      { id: 'commands', name: '常用命令', desc: 'grep / find / tail / WebSphere 排查模板', icon: '$_', tag: 'placeholder', tagText: '规划中' },
      { id: 'diagnostics', name: '环境自检', desc: '一键体检：本机 / 网络 / 配置 / 工具 / 每台 server 连通性', icon: '🩺', tag: 'ready', tagText: 'v0.4 新增' },
      { id: 'config', name: '系统配置', desc: '在线编辑业务系统/服务器/日志目录,改完点保存即生效', icon: '⚙', tag: 'ready', tagText: '可视化' },
      { id: 'downloads', name: '下载历史', desc: '浏览/删除/重新下载 downloads/ 里所有已下载文件', icon: '⤓', tag: 'ready', tagText: '已就绪' },
      { id: 'history', name: '操作历史', desc: '本地审计日志的最近记录 + CSV 导出', icon: '⏱', tag: 'ready', tagText: '已就绪' }
    ];
    const grid = el('div', { class: 'grid-4' });
    tools.forEach(t => {
      const card = el('div', { class: 'tool-card', onclick: () => { location.hash = '#/' + t.id; } }, [
        el('div', { class: 'row between' }, [
          el('div', { class: 'icon', text: t.icon }),
          el('span', { class: 'tag ' + t.tag, text: t.tagText })
        ]),
        el('div', { class: 'name', text: t.name }),
        el('div', { class: 'desc', text: t.desc })
      ]);
      grid.appendChild(card);
    });
    view.appendChild(grid);
  }

  // WebSphere 日志助手 —— 多服务器并行版
  function renderWebsphere(view) {
    let cfg = null;
    // listState: 当前已列文件 + 下载任务状态
    //   files:    FileEntry[]
    //   serverName: 哪台服务器
    //   dlId:     当前进行中的下载任务 id（null = 无）
    //   dlEvtSrc: 当前 SSE 订阅句柄
    //   fileStates: { [name]: { status, written, total, error } }
    let listState = { files: [], serverName: '', dlId: null, dlEvtSrc: null, fileStates: {} };
    // 每台服务器的状态：'idle' | 'ok' | 'fail' | 'busy'
    const srvStatus = {}; // key: server name, value: { state, err }

    const sysSel = el('select', { id: 'ws-sys' });
    const dirSel = el('select', { id: 'ws-dir' });
    const userInp = el('input', { type: 'text', id: 'ws-user', placeholder: 'SSH 用户名（可留空，使用配置默认）' });
    const passInp = el('input', { type: 'password', id: 'ws-pass', placeholder: 'SSH 密码' });
    const queryInp = el('input', { type: 'text', id: 'ws-query', placeholder: '例: Exception && userinfo   或   !DEBUG', value: 'Exception' });
    const filesNSel = el('select', { id: 'ws-files-n' });
    [1, 3, 5, 10].forEach(n => {
      const o = el('option', { value: String(n), text: '最近 ' + n + '个文件' });
      if (n === 3) o.selected = true;
      filesNSel.appendChild(o);
    });
    const concSel = el('select', { id: 'ws-conc' });
    [1, 2, 4, 8, 16].forEach(n => {
      const o = el('option', { value: String(n), text: '并发 ' + n });
      if (n === 8) o.selected = true;
      concSel.appendChild(o);
    });

    // 单独一个 "下载最近 N 个文件" 的数量选择（默认 3，可拉到 5）
    const dlNSel = el('select', { id: 'ws-dl-n' });
    [1, 2, 3, 4, 5].forEach(n => {
      const o = el('option', { value: String(n), text: '最近 ' + n + ' 个文件' });
      if (n === 3) o.selected = true;
      dlNSel.appendChild(o);
    });
    const dlZipChk = el('input', { type: 'checkbox', id: 'ws-dl-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('打包为 zip')]);

    // --- 服务器多选面板 ---
    const srvPickWrap = el('div', { class: 'srv-pick' });
    const srvPickHint = el('div', { class: 'text-dim', text: '先选择系统' });
    srvPickWrap.appendChild(srvPickHint);
    // 操作栏：全选 / 全不选 / 选测试通过的
    const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllSrv(true) });
    const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => toggleAllSrv(false) });
    const btnPickOnline = el('button', { class: 'btn btn-sm', text: '只选可用的', onclick: () => toggleOnline() });
    const srvPickToolbar = el('div', { class: 'srv-pick-toolbar' }, [
      document.createTextNode('目标服务器:'), btnPickAll, btnPickNone, btnPickOnline
    ]);
    // 用于 doList/doDownload 决定"单一目标"的隐藏 select
    const dlTargetSel = el('select', { style: 'display:none' });

    function getCheckedServers() {
      const out = [];
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        if (cb.checked) out.push(cb.getAttribute('data-srv'));
      });
      return out;
    }
    function toggleAllSrv(on) {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => { cb.checked = on; });
      refreshCredStatus();
    }
    function toggleOnline() {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        cb.checked = srvStatus[cb.getAttribute('data-srv')] && srvStatus[cb.getAttribute('data-srv')].state === 'ok';
      });
      refreshCredStatus();
    }
    function renderSrvPick() {
      // 重渲染前先记住已勾选的服务器，重建后恢复，避免"测试连接后勾选丢失"的回归。
      const prevChecked = getCheckedServers();
      srvPickWrap.innerHTML = '';
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      if (!sys || !sys.servers || !sys.servers.length) {
        srvPickWrap.appendChild(el('div', { class: 'text-dim', text: '该业务系统下没有服务器。' }));
        return;
      }
      sys.servers.forEach(s => {
        const st = srvStatus[s.name] || { state: 'idle' };
        const dotCls = 'dot dot-' + (st.state === 'idle' ? 'idle' : st.state);
        const cb = el('input', { type: 'checkbox', 'data-srv': s.name, value: s.name });
        // 恢复勾选：之前勾过的、或当前状态是 ok（测通过的默认保留）
        if (prevChecked.indexOf(s.name) !== -1 || st.state === 'ok') {
          cb.checked = true;
        }
        cb.addEventListener('change', refreshCredStatus);
        const item = el('label', { class: 'srv-pick-item' }, [
          cb,
          el('span', { class: 'name', text: s.name }),
          el('span', { class: 'host', text: s.host + ':' + s.port }),
          el('span', { class: 'status' }, [
            el('span', { class: dotCls }),
            document.createTextNode(' ' + (st.state === 'ok' ? '已测通' : st.state === 'fail' ? '连接失败' : st.state === 'busy' ? '测试中' : '未测'))
          ])
        ]);
        srvPickWrap.appendChild(item);
      });
    }
    function refreshDirs() {
      dirSel.innerHTML = '';
      dlTargetSel.innerHTML = '';
      const sysName = sysSel.value;
      const srvName = getCheckedServers()[0] || (cfg && cfg.systems.find(s => s.name === sysName) && cfg.systems.find(s => s.name === sysName).servers[0] && cfg.systems.find(s => s.name === sysName).servers[0].name);
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      const srv = sys && sys.servers.find(s => s.name === srvName);
      // dirSel 显示第一台勾选服务器的目录
      (srv ? srv.log_dirs : []).forEach(d => {
        dirSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
        dlTargetSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
      });
    }
    sysSel.addEventListener('change', () => { renderSrvPick(); refreshDirs(); refreshCredStatus(); });
    userInp.addEventListener('change', refreshCredStatus);
    userInp.addEventListener('blur', refreshCredStatus);

    const btnTest = el('button', { class: 'btn', text: '测试连接', onclick: doTest });
    const btnList = el('button', { class: 'btn btn-primary', text: '列出文件', onclick: doList });
    const btnDownload = el('button', { class: 'btn', text: '下载', onclick: doDownload });
    const btnSearch = el('button', { class: 'btn btn-primary', text: '搜索', onclick: doSearch });

    const fileTableWrap = el('div', { class: 'card', style: 'display:none' });
    const hitTableWrap = el('div', { class: 'card', style: 'display:none' });
    const ctxCard = el('div', { class: 'card', style: 'display:none' });

    function credsOne(srvName) {
      return { system: sysSel.value, server: srvName, dir: dirSel.value,
               username: userInp.value, password: passInp.value };
    }
    function credsMulti() {
      return { system: sysSel.value, dir: dirSel.value,
               servers: getCheckedServers(),
               username: userInp.value, password: passInp.value };
    }

    // ---- 凭据保存（OS 钥匙串）----
    const rememberChk = el('input', { type: 'checkbox', id: 'ws-remember' });
    const rememberLbl = el('label', { class: 'inline' }, [rememberChk, document.createTextNode('记住密码（存进系统钥匙串）')]);
    const credStatus = el('div', { class: 'text-dim mt-1', id: 'ws-cred-status', text: '未保存密码' });
    const btnForget = el('button', { class: 'btn btn-sm', text: '忘记', onclick: doForget, style: 'display:none' });
    const credStatusRow = el('div', { class: 'text-dim mt-1', style: 'display:flex; gap:8px; align-items:center;' }, [
      credStatus, btnForget
    ]);

    // 当前检查/保存的 key（system, server, username）— server 用第一台勾选的
    function currentCredKey() {
      const srvs = getCheckedServers();
      const srv = srvs[0] || (sysSel.value && cfg ? (cfg.systems.find(s => s.name === sysSel.value) || {}).servers?.[0]?.name : '');
      return { system: sysSel.value, server: srv, username: userInp.value };
    }

    async function refreshCredStatus() {
      const k = currentCredKey();
      if (!k.system || !k.server || !k.username) {
        credStatus.textContent = '未保存密码';
        btnForget.style.display = 'none';
        return;
      }
      try {
        const r = await api('GET', '/api/credentials/has?system=' + encodeURIComponent(k.system)
          + '&server=' + encodeURIComponent(k.server)
          + '&username=' + encodeURIComponent(k.username));
        if (!r.ok) return;
        // 项 23：根据 credential_store 配置决定是否显示「记住密码」控件。
        // mode=disabled 或 mode=file(占位) → 完全隐藏。
        // mode=keyring 但 available=false（keyring 不可用）→ 仍显示勾选框，但提示用户不可用。
        const mode = r.mode || 'keyring';
        const storeDisabled = (mode === 'disabled') || (mode === 'file');
        if (storeDisabled) {
          rememberChk.checked = false;
          rememberChk.disabled = true;
          rememberLbl.style.display = 'none';
          credStatus.textContent = r.reason || ('凭据存储=' + mode);
          credStatus.style.color = '#999';
          btnForget.style.display = 'none';
          return;
        }
        // mode=keyring：恢复显示
        rememberChk.disabled = false;
        rememberLbl.style.display = '';
        if (!r.available) {
          credStatus.textContent = '⚠ 系统钥匙串不可用 — 当前无法「记住密码」';
          credStatus.style.color = '#c00';
          btnForget.style.display = 'none';
        } else if (r.has) {
          credStatus.textContent = '✓ 已为 ' + k.username + '@' + k.server + ' 保存密码（无需再次输入）';
          credStatus.style.color = '';
          btnForget.style.display = '';
        } else {
          credStatus.textContent = '未保存密码';
          credStatus.style.color = '';
          btnForget.style.display = 'none';
        }
      } catch (e) { /* ignore */ }
    }

    async function doForget() {
      const k = currentCredKey();
      if (!k.system || !k.server || !k.username) return;
      try {
        await api('POST', '/api/credentials/clear', { system: k.system, server: k.server, username: k.username });
        toast('已忘记 ' + k.server + ' 上的密码', 'ok');
        await refreshCredStatus();
      } catch (e) {
        toast('清除失败: ' + e.message, 'err');
      }
    }

    // 在成功的 SSH 操作后，如果勾了"记住密码"，就把当前输入的密码存起来
    async function maybeSaveCred(srvName) {
      if (!rememberChk.checked) return;
      const pw = passInp.value;
      if (!pw) return;
      const k = currentCredKey();
      const target = srvName || k.server;
      if (!k.system || !target || !k.username) return;
      try {
        await api('POST', '/api/credentials/save', {
          system: k.system, server: target, username: k.username, password: pw
        });
        await refreshCredStatus();
      } catch (e) {
        // 不可用时只静默提示，不阻塞主流程
        console.warn('save credential failed:', e);
      }
    }

    async function doTest() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要测试的服务器', 'warn'); return; }
      srvs.forEach(n => { srvStatus[n] = { state: 'busy' }; });
      renderSrvPick();
      // 并行测试
      await Promise.all(srvs.map(async (n) => {
        try {
          await api('POST', '/api/ssh/test', credsOne(n));
          srvStatus[n] = { state: 'ok' };
          await maybeSaveCred(n);
        } catch (e) {
          srvStatus[n] = { state: 'fail', err: e.message };
        } finally {
          renderSrvPick();
        }
      }));
      const okN = srvs.filter(n => srvStatus[n].state === 'ok').length;
      toast(okN + '/' + srvs.length + ' 台连接成功', okN === srvs.length ? 'ok' : 'warn');
      refreshCredStatus();
    }

    async function doList() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选服务器', 'warn'); return; }
      // 列出第一台的文件（单服务器列文件）
      const n = srvs[0];
      try {
        const r = await api('POST', '/api/logs/list', credsOne(n));
        listState.files = r.files || [];
        listState.serverName = n;
        renderFileTable();
        fileTableWrap.style.display = '';
        toast('[' + n + '] 共 ' + listState.files.length + ' 个文件', 'ok');
        await maybeSaveCred(n);
      } catch (e) { toast('列文件失败：' + e.message, 'err'); }
    }

    function renderFileTable() {
      fileTableWrap.innerHTML = '';
      fileTableWrap.appendChild(el('h3', { text: '文件列表 · ' + (listState.serverName || '') + '（按修改时间倒序）' }));
      if (!listState.files.length) {
        fileTableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无文件，先点击"列出文件"。' }));
        return;
      }

      // 工具条：全选/反选/选前 N 个 + 下载选中 + 停止
      const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => pickAll(true) });
      const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => pickAll(false) });
      const btnPickTop = el('button', { class: 'btn btn-sm', text: '选前 ' + (dlNSel.value) + ' 个', onclick: () => pickTop(Number(dlNSel.value) || 1) });
      const btnDownloadSel = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownloadSelected });
      const btnCancel = el('button', { class: 'btn', text: '停止', onclick: doCancelDownload, disabled: true });
      const summary = el('div', { class: 'text-dim', text: '已选 0 个' });
      const toolbar = el('div', { class: 'file-toolbar' }, [
        btnPickAll, btnPickNone, btnPickTop, btnDownloadSel, btnCancel, summary
      ]);
      fileTableWrap.appendChild(toolbar);

      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { class: 'col-check' }, [el('input', { type: 'checkbox', id: 'ws-file-checkall', onchange: (e) => pickAll(e.target.checked) })]),
        el('th', { text: '文件名' }),
        el('th', { text: '大小' }),
        el('th', { text: '修改时间' }),
        el('th', { text: '路径' }),
        el('th', { class: 'col-status', text: '状态' })
      ]));
      tbl.appendChild(thead);
      const tbody = el('tbody');
      listState.files.forEach(f => {
        const row = el('tr', { 'data-file': f.name });
        const cb = el('input', { type: 'checkbox', 'data-file': f.name, onchange: refreshSummary });
        const statusCell = el('td', { class: 'col-status', 'data-status': f.name });
        row.appendChild(el('td', { class: 'col-check' }, [cb]));
        row.appendChild(el('td', null, f.name));
        row.appendChild(el('td', { class: 'num', text: formatBytes(f.size) }));
        row.appendChild(el('td', { class: 'muted', text: formatTime(f.mod_time) }));
        row.appendChild(el('td', { class: 'muted', text: f.full_path }));
        row.appendChild(statusCell);
        tbody.appendChild(row);
      });
      tbl.appendChild(tbody);
      fileTableWrap.appendChild(tbl);

      // 缓存工具条节点到 fileTableWrap，方便后面改 summary 和按钮 disabled
      fileTableWrap._toolbar = { summary, btnDownloadSel, btnCancel, btnPickAll, btnPickNone, btnPickTop };
      refreshSummary();
      // 重新列出后清空旧状态
      listState.fileStates = {};
    }

    function pickAll(on) {
      fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]').forEach(cb => { cb.checked = on; });
      refreshSummary();
    }
    function pickTop(n) {
      const cbs = fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]');
      cbs.forEach((cb, i) => { cb.checked = i < n; });
      refreshSummary();
    }
    function getSelectedFiles() {
      const out = [];
      fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]').forEach(cb => {
        if (cb.checked) out.push(cb.getAttribute('data-file'));
      });
      return out;
    }
    function refreshSummary() {
      const tb = fileTableWrap._toolbar;
      if (!tb) return;
      const sel = getSelectedFiles();
      tb.summary.textContent = '已选 ' + sel.length + ' / ' + listState.files.length + ' 个';
      // 全选 checkbox 状态同步
      const checkAll = fileTableWrap.querySelector('#ws-file-checkall');
      if (checkAll) {
        checkAll.checked = listState.files.length > 0 && sel.length === listState.files.length;
        checkAll.indeterminate = sel.length > 0 && sel.length < listState.files.length;
      }
    }

    // 找到对应行的状态单元格，更新进度 / 状态文字。
    //
    // **XSS 安全**：所有用户可控字段（错误信息 / 文件名）都走 textContent，
    // 不会进 innerHTML。新签名：
    //
    //   setRowStatus(name, status, args?, rowClass?)
    //
    //   status 取值：
    //     - 'pending'      → "等待…"
    //     - 'downloading'  → args = { written, total } 渲染进度条 + 百分比
    //     - 'done'         → args = { bytes }            "✓ 完成 · 1.2 MB"
    //     - 'fail'         → args = { error }            "✗ <error>"（textContent）
    //     - 'startfail'    → "启动失败"
    //     - 'cancel'       → "已停止"
    function setRowStatus(name, status, args, rowClass) {
      const cell = fileTableWrap.querySelector('[data-status="' + cssEscape(name) + '"]');
      if (cell) {
        // 清空原内容（removeChild 循环比 innerHTML="" 慢一点点但避免引入 raw HTML）
        while (cell.firstChild) cell.removeChild(cell.firstChild);
        const node = buildStatusNode(status, args || {});
        if (node) cell.appendChild(node);
      }
      const row = fileTableWrap.querySelector('tr[data-file="' + cssEscape(name) + '"]');
      if (row && rowClass) {
        row.classList.remove('row-done', 'row-fail', 'row-active');
        row.classList.add(rowClass);
      }
    }

    // buildStatusNode 用 DOM 构造状态节点（不用 innerHTML，所以用户输入 100% 安全）。
    function buildStatusNode(status, args) {
      switch (status) {
        case 'pending':
          return el('span', { class: 'dl-pct', text: '等待…' });
        case 'downloading': {
          const bar = el('div', { class: 'dl-bar' });
          const totalKnown = args.total && args.total > 0;
          const pct = totalKnown ? Math.min(100, ((args.written || 0) / args.total) * 100) : 0;
          const fill = el('div', {
            class: 'dl-bar-fill' + (totalKnown ? '' : ' indeterminate'),
            style: totalKnown ? ('width:' + pct + '%') : ''
          });
          bar.appendChild(fill);
          const txt = el('span', { class: 'dl-pct', text: pctText(args.written || 0, args.total || -1) });
          const wrap = el('span');
          wrap.appendChild(bar);
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(txt);
          return wrap;
        }
        case 'done': {
          const span = el('span', { class: 'dl-pct', style: 'color:#10b981' });
          span.appendChild(document.createTextNode('✓ 完成 · ' + formatBytes(args.bytes || 0)));
          return span;
        }
        case 'fail': {
          const span = el('span', { class: 'dl-pct', style: 'color:#ef4444' });
          // 错误信息用 textContent 自动 escape（不信任后端 / SSH 服务器返回的字符串）
          span.appendChild(document.createTextNode('✗ ' + (args.error || '失败')));
          return span;
        }
        case 'startfail':
          return el('span', { class: 'dl-pct', style: 'color:#ef4444', text: '启动失败' });
        case 'cancel':
          return el('span', { class: 'dl-pct', style: 'color:#999', text: '已停止' });
        default:
          return el('span', { class: 'dl-pct', text: String(status) });
      }
    }
    function cssEscape(s) {
      return String(s).replace(/[\\"]/g, '\\$&');
    }
    // 后端 SSE 事件里 file 是完整远端路径，DOM 行用 basename 做 key，
    // 这里转一下。失败回退原值（罕见：basename 冲突时按原值也查不到，靠 rowClass 标记）。
    function basenameOf(p) {
      if (!p) return p;
      const s = String(p);
      const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
      return i >= 0 ? s.substring(i + 1) : s;
    }
    function pctText(w, t) {
      if (t > 0) return ((w / t) * 100).toFixed(1) + '%';
      if (w > 0) return formatBytes(w) + ' / ?';
      return '0%';
    }

    async function doDownloadSelected() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选服务器', 'warn'); return; }
      const files = getSelectedFiles();
      if (!files.length) { toast('请先勾选要下载的文件', 'warn'); return; }
      if (listState.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const srvName = srvs[0]; // 跟 doList 一致，只支持单服务器
      // 把选中文件名映射成完整远端路径（/api/files/download 需要绝对路径）
      const paths = files.map(name => {
        const ent = listState.files.find(f => f.name === name);
        return (ent && ent.full_path) ? ent.full_path : (dirSel.value + '/' + name);
      });
      const wantZip = dlZipChk.checked;
      const zip = wantZip && files.length >= 2;
      if (wantZip && files.length < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
      }
      // 重置 fileStates
      listState.fileStates = {};
      files.forEach(name => {
        listState.fileStates[name] = { status: 'pending' };
        setRowStatus(name, 'pending');
      });
      const tb = fileTableWrap._toolbar;
      if (tb) { tb.btnDownloadSel.disabled = true; tb.btnCancel.disabled = false; }
      setStatus('busy', '下载中…');
      let dlId;
      try {
        const r = await api('POST', '/api/files/download', Object.assign({}, credsOne(srvName), {
          paths: paths, zip: zip
        }));
        dlId = r.id;
        listState.dlId = dlId;
        // 订阅 SSE
        if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); return; }
        const es = new EventSource('/api/files/download/' + dlId + '/events');
        listState.dlEvtSrc = es;
        // 也挂到全局，navigate 切走时能清理（renderWebsphere 重建后会丢闭包引用）
        window.__opsActiveDL = { id: dlId, evtsrc: es };
        // gotDone 标志位：onmessage 推 done 数据（{"kind":"done",...}）和 SSE 的 'done' 事件
        // 都会触发收尾逻辑；用标志位保证只走一次。
        let gotDone = false;
        const onDoneSeen = (reason) => {
          if (gotDone) return;
          gotDone = true;
          closeDownloadStream(reason);
        };
        es.onmessage = (ev) => {
          let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
          if (o && o.kind === 'done') {
            handleDownloadEvent(o, files);
            onDoneSeen('done');
            return;
          }
          handleDownloadEvent(o, files);
        };
        es.addEventListener('done', () => { onDoneSeen('done'); });
        es.onerror = () => {
          // 网络异常：等 onmessage 推 done 收尾；这里只做兜底
          setTimeout(() => {
            if (listState.dlId === dlId && listState.dlEvtSrc === es && !gotDone) {
              // 兜底：如果 2s 内没收到 done（后端没机会发 / 网络断了），
              // 强制标 done 收尾，避免前端卡在 99% 假死。
              onDoneSeen('error');
              toast('SSE 连接异常（已强制收尾）', 'err');
            }
          }, 2000);
        };
      } catch (e) {
        toast('启动下载失败：' + e.message, 'err');
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
        if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
        files.forEach(name => {
          setRowStatus(name, 'startfail', null, 'row-fail');
        });
        listState.dlId = null;
        listState.dlEvtSrc = null;
      }
    }

    function handleDownloadEvent(o, files) {
      // 后端 SSE 事件 file 字段是完整远端路径；行用 basename 做 key，转一下。
      const key = basenameOf(o.file);
      if (o.kind === 'file_start') {
        listState.fileStates[key] = { status: 'downloading', written: 0, total: o.total || -1 };
        setRowStatus(key, 'downloading', { written: 0, total: o.total || -1 }, 'row-active');
      } else if (o.kind === 'progress') {
        const st = listState.fileStates[key] || {};
        st.status = 'downloading'; st.written = o.written; st.total = o.total;
        listState.fileStates[key] = st;
        setRowStatus(key, 'downloading', { written: o.written, total: o.total }, 'row-active');
      } else if (o.kind === 'file_done') {
        listState.fileStates[key] = { status: 'done', bytes: o.bytes };
        setRowStatus(key, 'done', { bytes: o.bytes }, 'row-done');
      } else if (o.kind === 'done') {
        // 终态
        const tb = fileTableWrap._toolbar;
        if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
        listState.dlId = null;
        if (o.ok) {
          toast('下载完成：' + (o.downloads || []).length + ' 个产物', 'ok');
          // 渲染结果卡（复用现有 renderDownloadResults）
          renderDownloadResults([{ server: listState.serverName, downloads: o.downloads, folder: o.folder }]);
        } else {
          toast('下载失败：' + (o.error || '未知错误'), 'err');
          // 把还没标的行标失败
          files.forEach(name => {
            const st = listState.fileStates[name];
            if (!st || st.status === 'pending' || st.status === 'downloading') {
              setRowStatus(name, 'fail', { error: o.error || '失败' }, 'row-fail');
            }
          });
        }
        setStatus('idle');
        listState.dlEvtSrc = null;
      }
    }

    function closeDownloadStream(reason) {
      if (listState.dlEvtSrc) {
        listState.dlEvtSrc.close();
        listState.dlEvtSrc = null;
      }
      // 任务正常结束 / 取消 / 出错 都清掉全局引用
      if (window.__opsActiveDL) {
        try { window.__opsActiveDL.evtsrc && window.__opsActiveDL.evtsrc.close(); } catch (e) { /* ignore */ }
        window.__opsActiveDL = null;
      }
      const tb = fileTableWrap._toolbar;
      if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
      if (reason === 'cancel') {
        toast('已停止下载', 'warn');
      }
      setStatus('idle');
    }

    async function doCancelDownload() {
      if (!listState.dlId) return;
      try { await api('POST', '/api/files/download/' + listState.dlId + '/cancel', {}); }
      catch (e) { /* ignore */ }
      closeDownloadStream('cancel');
    }

    async function doDownload() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要下载的服务器（多台会逐个下）', 'warn'); return; }
      const latest = Number(dlNSel.value) || 1;
      const wantZip = dlZipChk.checked;
      let zip = wantZip;
      if (zip && latest < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
        zip = false;
      }
      const allResults = [];
      let firstErr = null;
      for (const srvName of srvs) {
        try {
          const r = await api('POST', '/api/logs/download-latest',
            Object.assign({}, credsOne(srvName), { latest: latest, zip: zip }));
          allResults.push({ server: srvName, downloads: r.downloads || [], folder: r.folder });
        } catch (e) {
          allResults.push({ server: srvName, error: e.message });
          if (!firstErr) firstErr = e.message;
        }
      }
      renderDownloadResults(allResults);
      const okN = allResults.filter(x => !x.error).length;
      toast((okN === srvs.length ? '下载完成：' : '部分失败：') + okN + '/' + srvs.length, okN === srvs.length ? 'ok' : 'warn');
      // 每台成功的服务器都尝试保存凭据
      for (const r of allResults) {
        if (!r.error) await maybeSaveCred(r.server);
      }
      refreshCredStatus();
    }

    function renderDownloadResults(allResults) {
      // 插入在 fileTableWrap 后面
      const existing = $('#ws-dl-results');
      if (existing) existing.remove();
      const wrap = el('div', { id: 'ws-dl-results', class: 'card' }, [
        el('h3', { text: '下载结果' })
      ]);
      allResults.forEach(r => {
        const grp = el('div', { class: 'server-group ' + (r.error ? 'fail' : 'ok') });
        if (r.error) {
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'name', text: r.server }),
            el('span', { class: 'meta', text: '下载失败' })
          ]));
          grp.appendChild(el('div', { class: 'err-msg', text: r.error }));
        } else {
          const files = (r.downloads || []).filter(d => d.kind !== 'zip');
          const zips = (r.downloads || []).filter(d => d.kind === 'zip');
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'name', text: r.server }),
            el('span', { class: 'meta', text: files.length + ' 个文件 + ' + zips.length + ' 个 zip' })
          ]));
          // 不用 innerHTML 拼远程文件名：恶意远端文件名（含 < / > 等）会注入 HTML。
          // 改成 DOM 创建：每个文件名用 textContent，链接 href 走 encodeURIComponent。
          const inner = el('div', { style: 'padding: 8px 12px; font-size: 12.5px;' });
          if (files.length) {
            const block = el('div');
            files.forEach(d => {
              const row = el('div');
              row.appendChild(document.createTextNode('· ' + d.file + ' → '));
              row.appendChild(el('a', {
                href: '/downloads/' + encodeURIComponent(d.local),
                text: d.local,
              }));
              row.appendChild(document.createTextNode('（' + formatBytes(d.bytes) + '）'));
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          if (zips.length) {
            const block = el('div', { class: 'mt-2' });
            zips.forEach(d => {
              const row = el('div');
              row.appendChild(document.createTextNode('· 📦 '));
              row.appendChild(el('a', {
                href: '/downloads/' + encodeURIComponent(d.local),
                text: d.local,
              }));
              row.appendChild(document.createTextNode('（' + formatBytes(d.bytes) + '）'));
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          grp.appendChild(inner);
        }
        wrap.appendChild(grp);
      });
      const folder = (allResults.find(r => r.folder) || {}).folder || '-';
      wrap.appendChild(el('div', { class: 'text-dim mt-2', text: '本地保存目录：' + folder }));
      fileTableWrap.parentNode.insertBefore(wrap, fileTableWrap.nextSibling);
    }

    async function doSearch() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要搜索的服务器', 'warn'); return; }
      if (!queryInp.value.trim()) { toast('搜索表达式不能为空', 'warn'); return; }
      hitTableWrap.style.display = '';
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '并行搜索中…' }));
      const conc = Number(concSel.value) || 8;
      try {
        const r = await api('POST', '/api/logs/search/multi', Object.assign({}, credsMulti(), {
          query: queryInp.value,
          files: Number(filesNSel.value),
          max_concurrency: conc
        }));
        renderMultiResults(r);
        toast('命中 ' + r.total_hits + ' 条，' + r.ok_count + '/' + srvs.length + ' 成功', r.fail_count > 0 ? 'warn' : 'ok');
        // 每台成功的服务器都尝试保存凭据
        for (const srv of (r.servers || [])) {
          if (srv.ok) await maybeSaveCred(srv.server);
        }
        refreshCredStatus();
      } catch (e) {
        hitTableWrap.innerHTML = '';
        hitTableWrap.appendChild(el('div', { class: 'text-err', text: '搜索失败：' + e.message }));
        toast('搜索失败：' + e.message, 'err');
      }
    }

    function renderMultiResults(r) {
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '搜索结果 · ' + r.ok_count + '/' + r.servers.length + ' 成功，共 ' + r.total_hits + ' 条命中' }));
      hitTableWrap.appendChild(el('div', { class: 'text-dim mb-2', text: '并发 ' + r.max_concurrency + '，按服务器分组展示' }));

      if (!r.servers || !r.servers.length) {
        hitTableWrap.appendChild(el('div', { class: 'text-dim', text: '没有结果。' }));
        return;
      }

      r.servers.forEach(srv => {
        const grp = el('div', { class: 'server-group ' + (srv.ok ? 'ok' : 'fail') });
        const headChildren = [
          el('span', { class: 'dot dot-' + (srv.ok ? (srv.hits_count > 0 ? 'ok' : 'idle') : 'err') }),
          el('span', { class: 'name', text: srv.server + (srv.host ? '  ·  ' + srv.host : '') })
        ];
        if (srv.ok && srv.encoding) {
          headChildren.push(el('span', { class: 'tag tag-enc', title: '当前目录编码（影响中文显示）', text: srv.encoding }));
        }
        headChildren.push(el('span', { class: 'meta', text:
          (srv.ok
            ? (srv.hits_count + ' 命中 / ' + (srv.files || []).length + ' 文件 / ' + srv.elapsed_ms + 'ms')
            : '失败 / ' + srv.elapsed_ms + 'ms') }));
        const head = el('div', { class: 'server-group-head' }, headChildren);
        grp.appendChild(head);
        if (!srv.ok) {
          grp.appendChild(el('div', { class: 'err-msg', text: srv.error || '未知错误' }));
        } else if (srv.hits_count === 0) {
          grp.appendChild(el('div', { class: 'text-dim', style: 'padding: 8px 12px;', text: '本台无匹配' }));
        } else {
          const tbl = el('table', { class: 'table' });
          const thead = el('thead', null, el('tr', null, [
            el('th', { text: '文件' }),
            el('th', { text: '行号' }),
            el('th', { text: '内容' }),
            el('th', { text: '操作' })
          ]));
          tbl.appendChild(thead);
          const tbody = el('tbody');
          srv.hits.forEach(h => {
            const content = trimMiddle(h.content, 280);
            const cells = [
              el('td', { class: 'muted', text: h.file }),
              el('td', { class: 'num', text: h.line_no })
            ];
            const contentCell = el('td', { class: 'hit-line', text: content });
            if (looksMojibake(content)) {
              contentCell.appendChild(el('span', { class: 'tag tag-warn', title: '当前目录编码与文件实际编码不一致，中文可能错位。试试切换到「GBK」目录。', text: '⚠ 解码可能有误' }));
            }
            cells.push(contentCell);
            cells.push(el('td', { class: 'actions' }, [
              el('button', { class: 'btn btn-sm', text: '上下文', onclick: () => doContext(h) })
            ]));
            tbody.appendChild(el('tr', null, cells));
          });
          tbl.appendChild(tbody);
          grp.appendChild(tbl);
        }
        hitTableWrap.appendChild(grp);
      });
    }

    // 简易乱码检测：U+FFFD (�) 出现 ≥2 次，或连续控制字符过多。基本够用，避免引入完整启发式。
    function looksMojibake(s) {
      if (!s) return false;
      let bad = 0;
      for (let i = 0; i < s.length; i++) {
        if (s.charCodeAt(i) === 0xFFFD) bad++;
      }
      return bad >= 2;
    }

    function trimMiddle(s, max) {
      s = s || '';
      if (s.length <= max) return s;
      const keep = Math.floor((max - 1) / 2);
      return s.slice(0, keep) + '…' + s.slice(s.length - keep);
    }

    async function doContext(hit) {
      const body = Object.assign({}, credsOne(hit.server), {
        file: hit.file, line: hit.line_no,
        before: 30, after: 30
      });
      try {
        const r = await api('POST', '/api/logs/context', body);
        renderContext(r.lines || [], hit);
        ctxCard.style.display = '';
        ctxCard.scrollIntoView({ behavior: 'smooth' });
      } catch (e) { toast('上下文获取失败：' + e.message, 'err'); }
    }

    function renderContext(lines, hit) {
      ctxCard.innerHTML = '';
      ctxCard.appendChild(el('h3', { text: '上下文 · ' + hit.server + ' · ' + hit.file + ':' + hit.line_no }));
      const view = el('div', { class: 'context-view' });
      lines.forEach(l => {
        view.appendChild(el('div', { class: 'row' + (l.hit ? ' hit' : '') }, [
          el('div', { class: 'ln', text: l.line_no }),
          el('div', { class: 'ct', text: l.content })
        ]));
      });
      ctxCard.appendChild(view);
    }

    // 顶部表单卡片
    const formCard = el('div', { class: 'card' }, [
      el('h3', { text: 'WebSphere 日志助手 · 多服务器并行' }),
      el('div', { class: 'card-desc', text: '先选业务系统 → 勾选目标服务器（可全选/全不选/只选可用的）→ 输入凭据 → 测试 / 列出 / 搜索 / 下载。' }),
      el('div', { class: 'grid-2' }, [
        el('div', null, [el('label', { text: '业务系统' }), sysSel]),
        el('div', null, [el('label', { text: '日志目录（取自第一台勾选服务器）' }), dirSel])
      ]),
      el('div', { class: 'mt-2' }, [srvPickToolbar, srvPickWrap]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: 'SSH 用户名' }), userInp]),
        el('div', null, [el('label', { text: 'SSH 密码' }), passInp])
      ]),
      el('div', { class: 'mt-1' }, [rememberLbl, credStatusRow]),
      el('div', { class: 'btn-row mt-3' }, [btnTest, btnList]),
      el('div', { class: 'mt-3' }, [
        el('div', { class: 'text-dim mb-1', text: '下载最新日志（每台服务器分别下，可选 zip）' }),
        el('div', { class: 'btn-row' }, [
          dlNSel, dlZipLabel, btnDownload
        ])
      ])
    ]);

    const searchCard = el('div', { class: 'card' }, [
      el('h3', { text: '多服务器并行搜索' }),
      el('div', { class: 'card-desc', unsafeHtml: '语法：<span class="code-inline">A &amp;&amp; B</span>（同包含）、<span class="code-inline">A || B</span>（任一）、<span class="code-inline">!X</span>（排除）。结果按服务器分组。' }),
      el('div', { class: 'grid-3' }, [
        el('div', { style: 'grid-column: span 2' }, [el('label', { text: '搜索表达式' }), queryInp]),
        el('div', null, [el('label', { text: '并发' }), concSel])
      ]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: '搜索范围（每台）' }), filesNSel])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnSearch])
    ]);

    // ---- 实时 tail（单服务器，单文件，SSE 流）----
    const tailFileInp = el('input', { type: 'text', id: 'ws-tail-file', placeholder: '文件名（例：SystemOut.log）', value: 'SystemOut.log' });
    const tailLinesInp = el('input', { type: 'number', id: 'ws-tail-lines', placeholder: '起始行数', value: '100' });
    const tailSrvSel = el('select', { id: 'ws-tail-srv' });
    const tailOut = el('pre', { id: 'ws-tail-out', class: 'tail-out' });
    const btnTailStart = el('button', { class: 'btn btn-primary', text: '开始跟踪', onclick: doTailStart });
    const btnTailStop = el('button', { class: 'btn', text: '停止', onclick: doTailStop, disabled: true });
    let tailEvtSrc = null;
    let tailId = null;

    function refreshTailServers() {
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      tailSrvSel.innerHTML = '';
      (sys ? sys.servers : []).forEach(s => {
        tailSrvSel.appendChild(el('option', { value: s.name, text: s.name + '  (' + s.host + ')' }));
      });
    }
    sysSel.addEventListener('change', refreshTailServers);

    async function doTailStart() {
      const file = (tailFileInp.value || '').trim();
      if (!file) { toast('请输入文件名', 'warn'); return; }
      const serverName = tailSrvSel.value;
      if (!serverName) { toast('请选择服务器', 'warn'); return; }
      if (!dirSel.value) { toast('请选择日志目录', 'warn'); return; }
      const lines = Math.max(0, Math.min(1000, Number(tailLinesInp.value) || 0));
      tailOut.textContent = '';
      setStatus('busy', '跟踪中…');
      try {
        const r = await api('POST', '/api/logs/tail/start', Object.assign({}, creds(), {
          file: file, lines: lines, server: serverName,
        }));
        tailId = r.id;
        btnTailStart.disabled = true;
        btnTailStop.disabled = false;
        appendTailLine({ kind: 'info', msg: '已开启 tail，id=' + tailId });
        // SSE 订阅
        if (window.EventSource) {
          tailEvtSrc = new EventSource('/api/logs/tail/' + tailId + '/events');
          tailEvtSrc.onmessage = (ev) => {
            try { appendTailLine(JSON.parse(ev.data)); } catch (e) { appendTailLine({ kind: 'info', msg: ev.data }); }
          };
          tailEvtSrc.addEventListener('done', () => {
            appendTailLine({ kind: 'info', msg: 'SSE 通道关闭' });
            stopTailUI();
          });
          tailEvtSrc.onerror = () => {
            appendTailLine({ kind: 'error', msg: 'SSE 连接异常' });
          };
        } else {
          appendTailLine({ kind: 'error', msg: '浏览器不支持 EventSource' });
        }
      } catch (e) {
        toast('启动 tail 失败：' + e.message, 'err');
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    async function doTailStop() {
      if (!tailId) return;
      try { await api('POST', '/api/logs/tail/' + tailId + '/stop'); }
      catch (e) { /* ignore */ }
      stopTailUI();
    }

    function stopTailUI() {
      if (tailEvtSrc) { tailEvtSrc.close(); tailEvtSrc = null; }
      tailId = null;
      btnTailStart.disabled = false;
      btnTailStop.disabled = true;
      setStatus('idle');
    }

    function appendTailLine(o) {
      if (o.kind === 'line') {
        tailOut.textContent += o.line + '\n';
      } else if (o.kind === 'info') {
        tailOut.textContent += '⟦info⟧ ' + o.msg + '\n';
      } else if (o.kind === 'error') {
        tailOut.textContent += '⟦error⟧ ' + o.msg + '\n';
      } else if (o.kind === 'done') {
        tailOut.textContent += '⟦done⟧ ' + o.msg + '\n';
      }
      // 自动滚到底部
      tailOut.scrollTop = tailOut.scrollHeight;
      // 限制总行数，防止前端内存爆掉
      const MAX_LINES = 5000;
      const lines = tailOut.textContent.split('\n');
      if (lines.length > MAX_LINES) {
        tailOut.textContent = lines.slice(lines.length - MAX_LINES).join('\n');
      }
    }

    const tailCard = el('div', { class: 'card' }, [
      el('h3', { text: '实时 tail（单文件，SSE 流式）' }),
      el('div', { class: 'card-desc', text: '跟踪远程文件新增行（用 tail -F），关闭/刷新页面会自动停。' }),
      el('div', { class: 'grid-3' }, [
        el('div', null, [el('label', { text: '服务器' }), tailSrvSel]),
        el('div', null, [el('label', { text: '文件名' }), tailFileInp]),
        el('div', null, [el('label', { text: '起始行数（0=只追新增）' }), tailLinesInp])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnTailStart, btnTailStop]),
      el('div', { class: 'mt-2' }, tailOut)
    ]);

    view.appendChild(formCard);
    view.appendChild(searchCard);
    view.appendChild(tailCard);
    view.appendChild(fileTableWrap);
    view.appendChild(hitTableWrap);
    view.appendChild(ctxCard);

    // 加载配置
    api('GET', '/api/config').then(info => {
      cfg = info;
      sysSel.innerHTML = '';
      info.systems.forEach(s => sysSel.appendChild(el('option', { value: s.name, text: s.name })));
      renderSrvPick();
      refreshDirs();
      refreshCredStatus();
    }).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  // 文件下载（v0.3）：按 SSH 账号权限浏览任意目录并下载。
  //
  // 设计要点：
  //   - 顶部连接区：系统 / 服务器 / 用户名 / 密码（keyring 复用）；
  //   - 中部路径区：面包屑（可点击）+ 返回上级 + 手动输入路径跳转；
  //   - 主体：目录列表，目录可点击进入，文件可勾选；
  //   - 多选下载：复用现有 /api/files/download + SSE 进度机制。
  function renderFiles(view) {
    // state：当前连接的服务器 + 当前路径 + 当前目录条目 + 下载任务状态
    const state = {
      cfg: null,                  // /api/config 返回的完整配置
      currentSys: '',             // 选中的系统名
      currentSrv: '',             // 选中的服务器名
      currentPath: '/',           // 当前绝对路径
      parent: '',                 // 上级绝对路径（null = 已在根）
      entries: [],                // 当前目录条目
      selected: new Set(),        // 选中的文件 basename 集合
      sortKey: 'name',            // 'name' | 'size' | 'mtime'
      sortDesc: false,
      dlId: null,                 // 当前下载任务 id
      dlEvtSrc: null,             // 当前 SSE 句柄
      fileStates: {},             // { [path]: { status, written, total, bytes, error } }
    };

    // ---- 连接区 ----
    const sysSel = el('select', { id: 'files-sys' });
    const srvSel = el('select', { id: 'files-srv' });
    srvSel.appendChild(el('option', { value: '', text: '（先选系统）' }));
    srvSel.disabled = true;
    const userInp = el('input', { type: 'text', id: 'files-user', placeholder: 'SSH 用户名（可留空用配置默认）' });
    const passInp = el('input', { type: 'password', id: 'files-pass', placeholder: 'SSH 密码' });
    const rememberChk = el('input', { type: 'checkbox', id: 'files-remember' });
    const rememberLabel = el('label', { class: 'inline' }, [rememberChk, document.createTextNode('记住密码')]);
    const btnConnect = el('button', { class: 'btn btn-primary', text: '连接并浏览' });

    // ---- 文件浏览器启动警告（项 14）----
    //
    // cfg 在 loadCfg() 后才填充；这里先放占位 card，加载完后 fillWarn() 补具体内容。
    const warnBox = el('div', { class: 'card', id: 'files-warn', style: 'display:none' });
    warnBox.appendChild(el('h3', { id: 'files-warn-title', text: '' }));
    const warnBody = el('div');
    warnBox.appendChild(warnBody);

    const connCard = el('div', { class: 'card' });
    connCard.appendChild(el('h3', { text: '1. 选择目标服务器' }));
    connCard.appendChild(el('div', { class: 'card-desc', text: '支持任意路径浏览；下载权限以 SSH 账号实际权限为准（v0.3 自由模式）。' }));
    connCard.appendChild(el('div', { class: 'grid-3' }, [
      el('label', null, [el('span', { class: 'lbl', text: '业务系统' }), sysSel]),
      el('label', null, [el('span', { class: 'lbl', text: '服务器' }), srvSel]),
      el('label', null, [el('span', { class: 'lbl', text: '用户名' }), userInp])
    ]));
    connCard.appendChild(el('div', { class: 'grid-2 mt-2' }, [
      el('label', null, [el('span', { class: 'lbl', text: '密码' }), passInp]),
      el('div', { style: 'display:flex;align-items:flex-end;gap:10px' }, [rememberLabel, btnConnect])
    ]));

    // ---- 路径区 ----
    const crumbsEl = el('div', { class: 'file-crumbs', style: 'font-family: ui-monospace, monospace; font-size: 13px;' });
    const pathInp = el('input', { type: 'text', placeholder: '输入绝对路径后回车跳转（例 /var/log）' });
    const btnParent = el('button', { class: 'btn btn-sm', text: '← 上级' });
    const btnRefresh = el('button', { class: 'btn btn-sm', text: '刷新' });

    const pathCard = el('div', { class: 'card' });
    pathCard.appendChild(el('h3', { text: '2. 浏览目录' }));
    pathCard.appendChild(el('div', { class: 'card-desc' }, [
      document.createTextNode('面包屑可点击跳转；点目录名进入子目录；勾选文件后下载。')
    ]));
    pathCard.appendChild(el('div', { class: 'row gap-2 mb-2' }, [btnParent, btnRefresh, crumbsEl]));
    pathCard.appendChild(pathInp);

    // ---- 文件列表区 ----
    const tableWrap = el('div', { class: 'file-table-wrap' });

    // 工具栏：全选 / 取消 / 已选 N 个 / 下载选中
    const btnSelAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllFiles(true) });
    const btnSelNone = el('button', { class: 'btn btn-sm', text: '取消选中', onclick: () => toggleAllFiles(false) });
    const selCount = el('span', { class: 'text-dim', text: '已选 0 个' });
    const dlZipChk = el('input', { type: 'checkbox', id: 'files-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('多文件打包 zip')]);
    const btnDownload = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownload });
    const btnCancel = el('button', { class: 'btn btn-danger', text: '取消下载', onclick: doCancelDownload });
    btnDownload.disabled = true;
    btnCancel.disabled = true;

    const fileCard = el('div', { class: 'card' });
    fileCard.appendChild(el('h3', { text: '3. 选择并下载' }));
    fileCard.appendChild(el('div', { class: 'file-toolbar' }, [
      btnSelAll, btnSelNone, selCount, dlZipLabel, btnDownload, btnCancel
    ]));
    fileCard.appendChild(tableWrap);

    view.appendChild(warnBox);
    view.appendChild(connCard);
    view.appendChild(pathCard);
    view.appendChild(fileCard);

    // =================== 行为 ===================

    function loadCfg() {
      return api('GET', '/api/config').then(info => {
        state.cfg = info;
        // 填系统下拉
        sysSel.innerHTML = '';
        sysSel.appendChild(el('option', { value: '', text: '（请选择）' }));
        (info.systems || []).forEach(sys => {
          sysSel.appendChild(el('option', { value: sys.name, text: sys.name + (sys.description ? ' · ' + sys.description : '') }));
        });
        // 填充文件浏览器启动警告（项 14）
        renderFileBrowserWarning(info);
      });
    }

    // renderFileBrowserWarning 根据 /api/config 返回的 app.* 字段填充顶部警告卡。
    //
    // 规则：
    //   - enable_free_file_browser === false → 整个浏览器应被前端阻止访问
    //     （目前前端只在点连接时让后端返 403；这里加一个"已关闭"横幅，提示用户）
    //   - enable_free_file_browser !== false 且 free_file_roots 已配 → 白名单模式提示
    //   - enable_free_file_browser !== false 且 free_file_roots 为空 → 自由模式警告
    function renderFileBrowserWarning(info) {
      const app = (info && info.app) || {};
      const enabled = app.enable_free_file_browser === undefined || app.enable_free_file_browser === true;
      const roots = Array.isArray(app.free_file_roots) ? app.free_file_roots : [];

      // 清空旧内容（appendChild 循环比 innerHTML="" 安全）
      while (warnBody.firstChild) warnBody.removeChild(warnBody.firstChild);
      const titleEl = $('#files-warn-title');

      if (!enabled) {
        titleEl.textContent = '⛔ 文件浏览器已关闭';
        warnBody.appendChild(el('div', { class: 'text-dim', text:
          'config.yaml 里 app.enable_free_file_browser = false。整个文件浏览功能已停用。' }));
        warnBox.style.display = '';
        return;
      }
      if (roots.length > 0) {
        titleEl.textContent = '✓ 文件浏览器（白名单模式）';
        warnBody.appendChild(el('div', { class: 'text-dim', text:
          '仅以下前缀的路径可访问（其它路径会被服务端拒绝 403）：' }));
        const ul = el('ul', { style: 'margin: 6px 0 0 0; padding-left: 20px;' });
        roots.forEach(r => ul.appendChild(el('li', { text: r })));
        warnBody.appendChild(ul);
        warnBox.style.display = '';
        return;
      }
      // 自由模式：明显警告
      titleEl.textContent = '⚠ 文件浏览器（自由模式 · 无白名单）';
      warnBody.appendChild(el('div', { class: 'text-err', text:
        '当前可访问任何 SSH 账号有权限的路径（包括 /etc、/root 等敏感目录）。' }));
      warnBody.appendChild(el('div', { class: 'text-dim mt-1', style: 'font-size: 12px;', text:
        '建议在 config.yaml 加 app.free_file_roots（如 /var/log、/opt/websphere）以缩小可访问范围。' }));
      warnBox.style.display = '';
    }

    function refreshCredStatus() {
      if (!state.currentSys || !state.currentSrv) {
        rememberChk.checked = false;
        rememberChk.disabled = true;
        return;
      }
      rememberChk.disabled = false;
      api('GET', '/api/credentials/has?system=' + encodeURIComponent(state.currentSys) + '&server=' + encodeURIComponent(state.currentSrv))
        .then(r => {
          // 项 23：根据 credential_store 配置显隐「记住密码」控件
          const mode = r.mode || 'keyring';
          const storeDisabled = (mode === 'disabled') || (mode === 'file');
          if (storeDisabled) {
            rememberChk.checked = false;
            rememberChk.disabled = true;
            rememberLabel.style.display = 'none';
          } else {
            rememberChk.disabled = false;
            rememberLabel.style.display = '';
            rememberChk.checked = !!r.has;
          }
        })
        .catch(() => { rememberChk.checked = false; });
    }

    function creds() {
      const u = userInp.value.trim();
      return {
        username: u,
        password: passInp.value,
        remember: rememberChk.checked
      };
    }

    function setSystem(name) {
      state.currentSys = name;
      state.currentSrv = '';
      srvSel.innerHTML = '';
      srvSel.appendChild(el('option', { value: '', text: '（请选择）' }));
      const sys = (state.cfg.systems || []).find(s => s.name === name);
      if (!sys) { srvSel.disabled = true; return; }
      (sys.servers || []).forEach(srv => {
        srvSel.appendChild(el('option', { value: srv.name, text: srv.name + ' · ' + srv.host + ':' + srv.port }));
      });
      srvSel.disabled = false;
      // 默认用户名
      userInp.value = (sys.servers && sys.servers[0] && sys.servers[0].username) || '';
      refreshCredStatus();
    }

    function setServer(name) {
      state.currentSrv = name;
      const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
      const srv = sys && (sys.servers || []).find(s => s.name === name);
      if (srv && srv.username && !userInp.value) {
        userInp.value = srv.username;
      }
      refreshCredStatus();
    }

    sysSel.addEventListener('change', () => setSystem(sysSel.value));
    srvSel.addEventListener('change', () => setServer(srvSel.value));

    btnConnect.addEventListener('click', async () => {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器', 'warn'); return;
      }
      const c = creds();
      if (!c.username) { toast('请输入 SSH 用户名', 'warn'); return; }
      // 记住密码：先调一次保存接口（如果勾选）
      if (c.remember && c.password) {
        try {
          await api('POST', '/api/credentials/save', {
            system: state.currentSys, server: state.currentSrv, username: c.username, password: c.password
          });
        } catch (e) { /* 忽略，下载时再 fallback */ }
      }
      // 选默认浏览路径：优先 config.yaml 里这台 server 的第一个 log_dirs[0].path
      // （这是用户配过的白名单路径，最贴近真实运维场景），其次 SSH 用户的 home，
      // 最后根目录。
      const startPath = pickDefaultPath(c.username);
      await doListDir(startPath, c);
    });

    // 默认路径：config 第一个 log_dirs[0].path > /home/<user> > /
    function pickDefaultPath(username) {
      try {
        const sys = (state.cfg && state.cfg.systems || []).find(s => s.name === state.currentSys);
        const srv = sys && (sys.servers || []).find(s => s.name === state.currentSrv);
        const ld = srv && (srv.log_dirs || [])[0];
        if (ld && ld.path) return ld.path;
      } catch (e) { /* ignore */ }
      if (username) return '/home/' + username;
      return '/';
    }

    btnParent.addEventListener('click', () => {
      if (state.parent && state.parent !== state.currentPath) {
        const c = creds();
        doListDir(state.parent, c);
      }
    });
    btnRefresh.addEventListener('click', () => {
      const c = creds();
      doListDir(state.currentPath, c);
    });
    pathInp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        const p = pathInp.value.trim();
        if (p && p.startsWith('/')) {
          const c = creds();
          doListDir(p, c);
        } else {
          toast('路径必须是绝对路径（以 / 开头）', 'warn');
        }
      }
    });

    // ---- 列目录 ----
    async function doListDir(path, c) {
      try {
        const r = await api('POST', '/api/files/list', {
          system: state.currentSys, server: state.currentSrv,
          username: c.username, password: c.password, path: path
        });
        state.currentPath = r.path;
        state.parent = r.parent || '';
        state.entries = r.entries || [];
        state.selected.clear();
        renderCrumbs();
        renderTable();
      } catch (e) {
        // 列目录失败时，给个空状态 + 错误提示
        state.entries = [];
        state.selected.clear();
        renderCrumbs();
        renderTable();
        toast('列出目录失败：' + e.message, 'err');
      }
    }

    function renderCrumbs() {
      crumbsEl.innerHTML = '';
      const parts = state.currentPath.split('/').filter(Boolean);
      const head = el('a', { href: '#', text: '/', onclick: (e) => {
        e.preventDefault(); doListDir('/', creds());
      }});
      crumbsEl.appendChild(head);
      let acc = '';
      parts.forEach((seg, i) => {
        crumbsEl.appendChild(document.createTextNode(' / '));
        acc += '/' + seg;
        const target = acc;
        crumbsEl.appendChild(el('a', { href: '#', text: seg, onclick: (e) => {
          e.preventDefault(); doListDir(target, creds());
        }}));
      });
      pathInp.value = state.currentPath;
    }

    function sortedEntries() {
      const arr = state.entries.slice();
      const k = state.sortKey;
      const desc = state.sortDesc;
      arr.sort((a, b) => {
        // 目录优先
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        let av, bv;
        if (k === 'size') { av = a.size; bv = b.size; }
        else if (k === 'mtime') { av = a.mtime || ''; bv = b.mtime || ''; }
        else { av = (a.name || '').toLowerCase(); bv = (b.name || '').toLowerCase(); }
        if (av < bv) return desc ? 1 : -1;
        if (av > bv) return desc ? -1 : 1;
        return 0;
      });
      return arr;
    }

    function renderTable() {
      tableWrap.innerHTML = '';
      if (!state.entries.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim', text: '（目录为空，或列表失败 — 看上面的提示）' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      const thead = el('thead');
      const sortLink = (label, key) => {
        const isActive = state.sortKey === key;
        const arrow = isActive ? (state.sortDesc ? ' ↓' : ' ↑') : '';
        return el('a', { href: '#', text: label + arrow, onclick: (e) => {
          e.preventDefault();
          if (state.sortKey === key) state.sortDesc = !state.sortDesc;
          else { state.sortKey = key; state.sortDesc = false; }
          renderTable();
        }});
      };
      thead.appendChild(el('tr', null, [
        el('th', { class: 'col-check' }),
        el('th', null, [sortLink('名称', 'name')]),
        el('th', null, [sortLink('大小', 'size')]),
        el('th', null, [sortLink('修改时间', 'mtime')]),
        el('th', null, [document.createTextNode('权限')]),
        el('th', { class: 'col-status' }, [document.createTextNode('状态')])
      ]));
      tbl.appendChild(thead);

      const tbody = el('tbody');
      sortedEntries().forEach(entry => {
        const fullPath = (state.currentPath === '/' ? '' : state.currentPath) + '/' + entry.name;
        const tr = el('tr', { 'data-path': fullPath, 'data-name': entry.name });
        const cb = el('input', { type: 'checkbox' });
        cb.checked = state.selected.has(entry.name);
        cb.disabled = entry.isDir; // 目录不能直接下载（要先进去选文件）
        cb.addEventListener('change', () => {
          if (cb.checked) state.selected.add(entry.name);
          else state.selected.delete(entry.name);
          updateSelCount();
        });
        tr.appendChild(el('td', { class: 'col-check' }, [cb]));

        // 名称：目录可点击进入，文件显示图标
        const icon = entry.isDir ? '📁' : '📄';
        const nameCell = el('td');
        if (entry.isDir) {
          nameCell.appendChild(el('a', { href: '#', text: entry.name, onclick: (e) => {
            e.preventDefault(); doListDir(fullPath, creds());
          }}));
        } else {
          nameCell.appendChild(document.createTextNode(entry.name));
        }
        nameCell.insertBefore(document.createTextNode(icon + ' '), nameCell.firstChild);
        tr.appendChild(nameCell);

        tr.appendChild(el('td', { class: 'num' }, [document.createTextNode(entry.isDir ? '—' : formatBytes(entry.size))]));
        tr.appendChild(el('td', null, [document.createTextNode(entry.mtime ? formatTime(entry.mtime) : '-')]));
        tr.appendChild(el('td', null, [document.createTextNode(entry.mode || '-')]));
        // 状态列：先留空，下载时由 SSE 填充
        tr.appendChild(el('td', { class: 'col-status status-cell', 'data-name': entry.name }, [document.createTextNode('')]));

        tbody.appendChild(tr);
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
      updateSelCount();
      // 恢复下载进行中的进度条（刷新列表后）
      Object.keys(state.fileStates).forEach(p => {
        const st = state.fileStates[p];
        if (!st) return;
        const base = p.split('/').pop();
        setRowStatusByName(base, st);
      });
    }

    function updateSelCount() {
      const n = state.selected.size;
      selCount.textContent = '已选 ' + n + ' 个';
      btnDownload.disabled = n === 0 || !!state.dlId;
      btnDownload.textContent = n > 1 ? ('下载选中 (' + n + ')') : '下载选中';
    }

    function toggleAllFiles(on) {
      state.selected.clear();
      if (on) {
        state.entries.forEach(e => { if (!e.isDir) state.selected.add(e.name); });
      }
      renderTable();
    }

    // setRowStatusByName 把状态写入"按 name 索引"的状态单元格（renderFiles 表格）。
    //
    // **XSS 安全**：st.error / st.file 等用户可控字段都走 textContent，
    // 不会进 innerHTML。st 是 fileStates 的条目，结构：
    //   { status, written?, total?, bytes?, error? }
    function setRowStatusByName(name, st) {
      const cell = tableWrap.querySelector('tr[data-name="' + cssEscape(name) + '"] .status-cell')
                || tableWrap.querySelector('tr .status-cell[data-name="' + cssEscape(name) + '"]');
      if (!cell) return;
      while (cell.firstChild) cell.removeChild(cell.firstChild);
      const node = buildStatusNodeByName(st);
      if (node) cell.appendChild(node);
    }

    // buildStatusNodeByName 把 fileStates 条目转成 DOM 节点。
    // 状态值跟 buildStatusNode 一致（pending / downloading / done / fail / startfail / cancel）。
    function buildStatusNodeByName(st) {
      if (!st) return null;
      if (st.status === 'pending') {
        return el('span', { class: 'dl-pct', text: '等待…' });
      }
      if (st.status === 'downloading') {
        const wrap = el('span');
        const bar = el('div', { class: 'dl-bar' });
        const totalKnown = st.total && st.total > 0;
        const pct = totalKnown ? Math.min(100, ((st.written || 0) / st.total) * 100) : 0;
        const fill = el('div', {
          class: 'dl-bar-fill' + (totalKnown ? '' : ' indeterminate'),
          style: totalKnown ? ('width:' + pct + '%') : ''
        });
        bar.appendChild(fill);
        wrap.appendChild(bar);
        if (totalKnown) {
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(el('span', { class: 'dl-pct', text: pctText(st.written || 0, st.total) }));
        } else {
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(el('span', { class: 'dl-pct', text: '下载中' }));
        }
        return wrap;
      }
      if (st.status === 'done') {
        const span = el('span', { class: 'dl-pct', style: 'color:#10b981' });
        span.appendChild(document.createTextNode('✓ 完成 · ' + formatBytes(st.bytes || 0)));
        return span;
      }
      if (st.status === 'fail') {
        const span = el('span', { class: 'dl-pct', style: 'color:#ef4444' });
        span.appendChild(document.createTextNode('✗ ' + (st.error || '失败')));
        return span;
      }
      return null;
    }

    // ---- 下载 ----
    async function doDownload() {
      if (state.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const c = creds();
      if (!c.username) { toast('请输入 SSH 用户名', 'warn'); return; }
      const names = Array.from(state.selected);
      if (!names.length) { toast('请先勾选文件', 'warn'); return; }
      const paths = names.map(n => (state.currentPath === '/' ? '' : state.currentPath) + '/' + n);
      const wantZip = dlZipChk.checked;
      const zip = wantZip && paths.length >= 2;
      if (wantZip && paths.length < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
      }
      // 重置 fileStates
      state.fileStates = {};
      paths.forEach(p => {
        state.fileStates[p] = { status: 'pending' };
        setRowStatusByName(p.split('/').pop(), state.fileStates[p]);
      });
      btnDownload.disabled = true;
      btnCancel.disabled = false;
      setStatus('busy', '下载中…');
      try {
        const r = await api('POST', '/api/files/download', {
          system: state.currentSys, server: state.currentSrv,
          username: c.username, password: c.password,
          paths: paths, zip: zip
        });
        state.dlId = r.id;
        if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); return; }
        const es = new EventSource('/api/files/download/' + r.id + '/events');
        state.dlEvtSrc = es;
        // gotDone 标志位：onmessage 推 done 数据（{"kind":"done",...}）和 SSE 的 'done' 事件
        // 都会触发收尾；用标志位保证只走一次。
        let gotDone = false;
        const onDoneSeen = (reason) => {
          if (gotDone) return;
          gotDone = true;
          closeDownloadStream(reason);
        };
        es.onmessage = (ev) => {
          let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
          if (o && o.kind === 'done') {
            handleDownloadEvent(o);
            onDoneSeen('done');
            return;
          }
          handleDownloadEvent(o);
        };
        es.addEventListener('done', () => { onDoneSeen('done'); });
        es.onerror = () => {
          setTimeout(() => {
            if (state.dlId && state.dlEvtSrc === es && !gotDone) {
              onDoneSeen('error');
              toast('SSE 连接异常（已强制收尾）', 'err');
            }
          }, 2000);
        };
      } catch (e) {
        toast('启动下载失败：' + e.message, 'err');
        paths.forEach(p => {
          state.fileStates[p] = { status: 'fail', error: e.message };
          setRowStatusByName(p.split('/').pop(), state.fileStates[p]);
        });
        btnDownload.disabled = false;
        btnCancel.disabled = true;
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    function handleDownloadEvent(o) {
      if (o.kind === 'file_start') {
        state.fileStates[o.file] = { status: 'downloading', written: 0, total: o.total || -1 };
        setRowStatusByName(o.file.split('/').pop(), state.fileStates[o.file]);
      } else if (o.kind === 'progress') {
        const st = state.fileStates[o.file] || {};
        st.status = 'downloading'; st.written = o.written; st.total = o.total;
        state.fileStates[o.file] = st;
        setRowStatusByName(o.file.split('/').pop(), st);
      } else if (o.kind === 'file_done') {
        state.fileStates[o.file] = { status: 'done', bytes: o.bytes };
        setRowStatusByName(o.file.split('/').pop(), state.fileStates[o.file]);
      } else if (o.kind === 'done') {
        btnDownload.disabled = state.selected.size === 0;
        btnCancel.disabled = true;
        state.dlId = null;
        if (o.ok) {
          toast('下载完成：' + (o.downloads || []).length + ' 个产物（去「下载历史」页打开/管理）', 'ok');
        } else {
          toast('下载失败：' + (o.error || '未知错误'), 'err');
          // 把还没标的标失败
          Object.keys(state.fileStates).forEach(p => {
            const st = state.fileStates[p];
            if (st.status === 'pending' || st.status === 'downloading') {
              st.status = 'fail'; st.error = o.error || '';
              setRowStatusByName(p.split('/').pop(), st);
            }
          });
        }
        setStatus('idle');
        state.dlEvtSrc = null;
      }
    }

    function closeDownloadStream(reason) {
      if (state.dlEvtSrc) {
        state.dlEvtSrc.close();
        state.dlEvtSrc = null;
      }
      btnDownload.disabled = state.selected.size === 0;
      btnCancel.disabled = true;
      if (reason === 'cancel') toast('已停止下载', 'warn');
      setStatus('idle');
    }

    async function doCancelDownload() {
      if (!state.dlId) return;
      try { await api('POST', '/api/files/download/' + state.dlId + '/cancel', {}); }
      catch (e) { /* ignore */ }
      closeDownloadStream('cancel');
    }

    // ---- 启动 ----
    loadCfg().then(refreshCredStatus).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  // 简单的 CSS.escape polyfill（用于 selector 转义）
  function cssEscape(s) {
    if (window.CSS && CSS.escape) return CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_-]/g, c => '\\' + c);
  }

  // 简单的百分比文本
  function pctText(written, total) {
    if (!total || total < 0) return formatBytes(written || 0);
    const pct = Math.min(100, Math.round((written / total) * 100));
    return pct + '% · ' + formatBytes(written) + ' / ' + formatBytes(total);
  }

  // 报文格式化
  function renderFormatter(view) {
    const inTa = el('textarea', { id: 'fmt-in', placeholder: '在此粘贴 JSON 或 XML…' });
    const outTa = el('textarea', { id: 'fmt-out', placeholder: '结果会出现在这里', readonly: true });

    const btnFmtJSON = el('button', { class: 'btn btn-primary', text: '格式化 JSON', onclick: () => doJSON('format') });
    const btnMinJSON = el('button', { class: 'btn', text: '压缩 JSON', onclick: () => doJSON('minify') });
    const btnValJSON = el('button', { class: 'btn', text: '校验 JSON', onclick: () => doJSON('validate') });
    const btnFmtXML = el('button', { class: 'btn btn-primary', text: '格式化 XML', onclick: () => doXML('format') });
    const btnMinXML = el('button', { class: 'btn', text: '压缩 XML', onclick: () => doXML('minify') });
    const btnClear = el('button', { class: 'btn', text: '清空', onclick: () => { inTa.value = ''; outTa.value = ''; } });
    const btnCopy = el('button', { class: 'btn', text: '复制结果', onclick: () => copyOut() });

    async function doJSON(mode) {
      try {
        const r = await api('POST', '/api/format/json', { input: inTa.value, mode: mode, indent: '  ' });
        if (mode === 'validate') toast('JSON 合法', 'ok');
        else outTa.value = r.output || '';
      } catch (e) { toast('JSON 处理失败：' + e.message, 'err'); }
    }
    async function doXML(mode) {
      try {
        const r = await api('POST', '/api/format/xml', { input: inTa.value, mode: mode, indent: '  ' });
        outTa.value = r.output || '';
      } catch (e) { toast('XML 处理失败：' + e.message, 'err'); }
    }
    function copyOut() {
      if (!outTa.value) { toast('结果为空', 'warn'); return; }
      outTa.select();
      try { document.execCommand('copy'); toast('已复制', 'ok'); }
      catch (e) { toast('复制失败', 'err'); }
    }

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '报文格式化' }),
      el('div', { class: 'card-desc', text: '纯本地处理，不上传任何内容。适合接口报文、日志报文。' }),
      el('div', { class: 'pane' }, [
        el('div', null, [el('label', { text: '输入' }), inTa]),
        el('div', null, [el('label', { text: '输出' }), outTa])
      ]),
      el('div', { class: 'btn-row mt-3' }, [
        btnFmtJSON, btnMinJSON, btnValJSON,
        btnFmtXML, btnMinXML,
        btnClear, btnCopy
      ])
    ]));
  }

  // 操作历史 —— 显示本地 audit.log 的最近记录
  function renderHistory(view) {
    const opSel = el('select', { id: 'hist-op' },
      ['', 'ssh.test', 'logs.list', 'logs.download', 'logs.search', 'logs.context', 'logs.tail', 'admin.servers.put']
        .map(v => el('option', { value: v, text: v || '全部操作' }, v === '' ? 'selected' : null)));
    const resultSel = el('select', { id: 'hist-result' },
      ['', 'ok', 'fail'].map(v => el('option', { value: v, text: v || '全部状态' }, v === '' ? 'selected' : null)));
    const limitInp = el('input', { type: 'number', value: '100', min: '1', max: '5000', style: 'width: 100px' });
    const sysInp = el('input', { type: 'text', placeholder: '系统（包含匹配）' });
    const srvInp = el('input', { type: 'text', placeholder: '服务器（包含匹配）' });
    const btnRefresh = el('button', { class: 'btn btn-primary', text: '刷新', onclick: loadHistory });
    const btnExportCSV = el('button', { class: 'btn', text: '导出 CSV', onclick: exportCSV });
    const btnAuto = el('button', { class: 'btn', text: '自动刷新: 关', onclick: toggleAuto });
    const btnExport = el('button', { class: 'btn', text: '导出 CSV', onclick: exportCSV });
    const tableWrap = el('div', { class: 'card mt-3', style: 'padding: 0' });
    let autoTimer = null;

    async function loadHistory() {
      const q = new URLSearchParams();
      q.set('limit', String(Number(limitInp.value) || 100));
      if (opSel.value) q.set('op', opSel.value);
      if (resultSel.value) q.set('result', resultSel.value);
      if (sysInp.value) q.set('system', sysInp.value);
      if (srvInp.value) q.set('server', srvInp.value);
      try {
        const r = await api('GET', '/api/audit/recent?' + q.toString());
        renderTable(r.records || []);
      } catch (e) { toast('加载失败：' + e.message, 'err'); }
    }

    // 构造 CSV 导出 URL（同样的过滤参数），由浏览器触发下载。
    // 不走 api()：CSV 不是 JSON，会触发 api 的 JSON parse 失败；这里直接拼 URL。
    function buildExportCSVUrl() {
      const q = new URLSearchParams();
      // 导出给上限（5000），不受当前 limitInp 限制
      q.set('limit', '5000');
      if (opSel.value) q.set('op', opSel.value);
      if (resultSel.value) q.set('result', resultSel.value);
      if (sysInp.value) q.set('system', sysInp.value);
      if (srvInp.value) q.set('server', srvInp.value);
      return '/api/audit/export.csv?' + q.toString();
    }

    function exportCSV() {
      const url = buildExportCSVUrl();
      // 用 a 标签 + download 触发下载，避免 EventSource / fetch 影响
      const a = document.createElement('a');
      a.href = url;
      a.download = ''; // 让浏览器用 Content-Disposition 里的 filename
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      toast('已触发 CSV 下载', 'ok');
    }

    function renderTable(records) {
      tableWrap.innerHTML = '';
      tableWrap.appendChild(el('h3', { class: 'p-3', text: '共 ' + records.length + ' 条（按时间倒序）' }));
      if (!records.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim p-3', text: '没有匹配记录。' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { text: '时间' }),
        el('th', { text: '操作' }),
        el('th', { text: '系统/服务器' }),
        el('th', { text: '详情' }),
        el('th', { text: '结果' })
      ]));
      tbl.appendChild(thead);
      const tbody = el('tbody');
      records.forEach(r => {
        const detail = [];
        if (r.dir) detail.push('dir=' + r.dir);
        if (r.file) detail.push('file=' + r.file);
        if (r.query) detail.push('query=' + r.query);
        if (r.id) detail.push('id=' + r.id);
        if (r.lines) detail.push('lines=' + r.lines);
        if (r.hits) detail.push('hits=' + r.hits);
        if (r.bytes) detail.push('bytes=' + r.bytes);
        if (r.stage) detail.push('stage=' + r.stage);
        const errTxt = r.err || '';
        const detailTxt = detail.join(' · ') + (errTxt ? '\n⟦err⟧ ' + errTxt : '');
        const resultClass = r.result === 'ok' ? 'tag ready' : (r.result === 'fail' ? 'tag placeholder' : 'muted');
        tbody.appendChild(el('tr', null, [
          el('td', { class: 'muted mono', text: r.ts || '-' }),
          el('td', null, r.op || '-'),
          el('td', { class: 'mono', text: (r.system || '-') + (r.server ? ' · ' + r.server : '') }),
          el('td', { class: 'muted small', text: detailTxt }),
          el('td', null, el('span', { class: resultClass, text: r.result || '-' }))
        ]));
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
    }

    function toggleAuto() {
      if (autoTimer) {
        clearInterval(autoTimer);
        autoTimer = null;
        btnAuto.textContent = '自动刷新: 关';
      } else {
        loadHistory();
        autoTimer = setInterval(loadHistory, 3000);
        btnAuto.textContent = '自动刷新: 开 (3s)';
      }
    }

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '操作历史' }),
      el('div', { class: 'card-desc', text: '读 audit.log，过滤最近 N 条；不会写任何东西，不上传。' }),
      el('div', { class: 'grid-3' }, [
        el('div', null, [el('label', { text: '操作类型' }), opSel]),
        el('div', null, [el('label', { text: '结果' }), resultSel]),
        el('div', null, [el('label', { text: '返回条数' }), limitInp])
      ]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: '系统（包含匹配）' }), sysInp]),
        el('div', null, [el('label', { text: '服务器（包含匹配）' }), srvInp])
      ]),
      el('div', { class: 'btn-row mt-3' }, [btnRefresh, btnExportCSV, btnAuto])
    ]));
    view.appendChild(tableWrap);

    // 首次进入自动加载
    loadHistory();
  }

  // exportCSV 触发下载 CSV 文件（项 24 P3）。
  // 直接用 window.open 让浏览器走 attachment 下载，下载完后能自动关闭。
  function exportCSV() {
    const q = new URLSearchParams();
    q.set('limit', '5000');
    if (opSel.value) q.set('op', opSel.value);
    if (resultSel.value) q.set('result', resultSel.value);
    if (sysInp.value) q.set('system', sysInp.value);
    if (srvInp.value) q.set('server', srvInp.value);
    // 静默走 fetch 拿 blob + 触发下载，避免 window.open 被弹窗拦截
    fetch('/api/audit/export.csv?' + q.toString())
      .then(r => {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.blob();
      })
      .then(blob => {
        const a = document.createElement('a');
        const url = URL.createObjectURL(blob);
        a.href = url;
        a.download = 'ops-toolbox-audit.csv';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
        toast('已导出', 'ok');
      })
      .catch(e => toast('导出失败：' + e.message, 'err'));
  }

  // 下载历史页
  function renderDownloads(view) {
    const summaryEl = el('div', { class: 'text-dim', text: '加载中…' });
    const sysSel = el('select', null);
    sysSel.appendChild(el('option', { value: '', text: '全部系统' }));
    sysSel.appendChild(el('option', { value: '信贷生产（模拟）', text: '信贷生产（模拟）' }));
    const srvSel = el('select', null);
    srvSel.appendChild(el('option', { value: '', text: '全部服务器' }));
    const tableWrap = el('div', { class: 'mt-3' });
    const btnRefresh = el('button', { class: 'btn', text: '刷新', onclick: load });
    const btnClearAll = el('button', { class: 'btn btn-danger', text: '清空全部', onclick: doClearAll });

    view.appendChild(el('h3', { text: '下载历史' }));
    view.appendChild(el('div', { class: 'card-desc', text: 'downloads/ 目录里所有已下载的日志。点文件可重新下载，点删除可移除。' }));
    view.appendChild(el('div', { class: 'grid-2 mt-2' }, [
      el('div', null, [el('label', { text: '业务系统' }), sysSel]),
      el('div', null, [el('label', { text: '服务器' }), srvSel])
    ]));
    view.appendChild(el('div', { class: 'btn-row mt-2' }, [btnRefresh, btnClearAll]));
    view.appendChild(summaryEl);
    view.appendChild(tableWrap);

    sysSel.addEventListener('change', load);
    srvSel.addEventListener('change', load);

    async function load() {
      summaryEl.textContent = '加载中…';
      tableWrap.innerHTML = '';
      try {
        const qs = new URLSearchParams();
        if (sysSel.value) qs.set('system', sysSel.value);
        if (srvSel.value) qs.set('server', srvSel.value);
        const r = await api('GET', '/api/downloads/list' + (qs.toString() ? '?' + qs : ''));
        renderRows(r.files || []);
        // 填充 server 下拉
        const srvSet = new Set((r.files || []).map(f => f.server).filter(Boolean));
        const curSrv = srvSel.value;
        srvSel.innerHTML = '';
        srvSel.appendChild(el('option', { value: '', text: '全部服务器' }));
        Array.from(srvSet).sort().forEach(s => srvSel.appendChild(el('option', { value: s, text: s })));
        srvSel.value = curSrv;
        summaryEl.textContent = r.count + ' 个文件 · 占用 ' + r.total_human + ' · 目录 ' + r.folder;
        summaryEl.className = 'text-dim mt-2';
      } catch (e) {
        summaryEl.textContent = '加载失败：' + e.message;
        summaryEl.className = 'text-err mt-2';
      }
    }

    function renderRows(files) {
      tableWrap.innerHTML = '';
      if (!files.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无下载文件。' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      tbl.appendChild(el('thead', null, el('tr', null, [
        el('th', { text: '文件' }),
        el('th', { text: '大小' }),
        el('th', { text: '下载时间' }),
        el('th', { text: '来源' }),
        el('th', { text: '操作' })
      ])));
      const tbody = el('tbody');
      files.forEach(f => {
        const fromServer = f.server || '（未记录）';
        const fromDir = f.dir || '（未记录）';
        const fromFile = f.kind === 'zip'
          ? '📦 ' + (f.files && f.files.length ? f.files.length + ' 个文件' : 'zip')
          : (f.file || '?');
        const dl = el('a', { href: '/downloads/' + encodeURIComponent(f.name), text: '⤓ 下载' });
        const del = el('button', { class: 'btn btn-sm btn-danger', text: '删除',
          onclick: () => doDelete(f, load) });
        tbody.appendChild(el('tr', null, [
          buildNameCell(f),
          el('td', { class: 'num', text: f.size_human || '-' }),
          el('td', { class: 'muted', text: f.downloaded_at || f.mod_time || '-' }),
          buildFromCell(fromServer, fromDir, fromFile),
          el('td', null, [dl, document.createTextNode(' '), del])
        ]));
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
    }

    // buildNameCell 构造"文件名"单元格的 DOM（避免 innerHTML 注入）。
    //   - f.name 放进 <code> 块，textContent 安全
    //   - zip 类型加 <span class="tag">zip</span> 标签
    function buildNameCell(f) {
      const td = el('td');
      td.appendChild(el('code', { text: f.name || '' }));
      if (f.kind === 'zip') {
        td.appendChild(document.createTextNode(' '));
        td.appendChild(el('span', { class: 'tag', text: 'zip' }));
      }
      return td;
    }

    // buildFromCell 构造"来源"单元格的 DOM：服务器（粗体）/ 远端目录 / 原始文件名。
    // 所有用户/服务器可控字段都走 textContent，不进 innerHTML。
    function buildFromCell(fromServer, fromDir, fromFile) {
      const td = el('td');
      td.appendChild(el('div', { text: fromServer }));
      td.appendChild(el('div', { class: 'text-dim', text: fromDir }));
      td.appendChild(el('div', { class: 'text-dim', text: fromFile }));
      return td;
    }

    async function doDelete(f, cb) {
      if (!confirm('删除 ' + f.name + '？')) return;
      try {
        await api('DELETE', '/api/downloads/' + encodeURIComponent(f.name));
        toast('已删除', 'ok');
        if (cb) cb();
      } catch (e) {
        toast('删除失败：' + e.message, 'err');
      }
    }

    async function doClearAll() {
      if (!confirm('清空 downloads/ 里所有文件？此操作不可恢复。')) return;
      try {
        const r = await api('POST', '/api/downloads/all');
        toast('已清空 ' + r.deleted + ' 个文件', 'ok');
        load();
      } catch (e) {
        toast('清空失败：' + e.message, 'err');
      }
    }

    // 首次进入自动加载
    load();
  }

  // 环境自检（P3-1）：调用 /api/diagnostics，展示本机 / 网络 / 配置 / 工具 / 每台 server 连通性。
  //
  // 安全性：所有用户/网络可控字段都走 textContent，不进 innerHTML。
  function renderDiagnostics(view) {
    const summaryEl = el('div', { class: 'text-dim', text: '加载中…' });
    const btnRefresh = el('button', { class: 'btn btn-primary', text: '重新自检', onclick: load });
    const btnSkipServers = el('button', { class: 'btn', text: '只看本机（跳过 server 检查）', onclick: () => load(true) });
    const issuesCard = el('div');
    const sectionsWrap = el('div');
    const toolsWrap = el('div');
    const serversWrap = el('div');

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '环境自检' }),
      el('div', { class: 'card-desc', text: '本机环境、磁盘可写性、监听地址、凭据后端、本机命令工具、配置里每台 server 的 DNS + TCP 连通性。' }),
      el('div', { class: 'btn-row mt-2' }, [btnRefresh, btnSkipServers]),
      summaryEl
    ]));
    view.appendChild(issuesCard);
    view.appendChild(sectionsWrap);
    view.appendChild(toolsWrap);
    view.appendChild(serversWrap);

    async function load(skipServers) {
      summaryEl.textContent = '加载中…';
      issuesCard.innerHTML = '';
      sectionsWrap.innerHTML = '';
      toolsWrap.innerHTML = '';
      serversWrap.innerHTML = '';
      const qs = new URLSearchParams();
      if (skipServers) qs.set('check_servers', 'false');
      try {
        const r = await api('GET', '/api/diagnostics' + (qs.toString() ? '?' + qs : ''));
        render(r);
      } catch (e) {
        summaryEl.textContent = '加载失败：' + e.message;
        summaryEl.className = 'text-err mt-2';
      }
    }

    function render(r) {
      // 摘要 + 时间
      const okN = (r.servers || []).filter(s => s.dns && s.dns.ok && s.tcp && s.tcp.ok).length;
      const totalN = (r.servers || []).length;
      summaryEl.textContent = '生成于 ' + r.generated_at + ' · ' + okN + '/' + totalN + ' 台 server 网络可达';
      summaryEl.className = 'text-dim mt-2';

      // 顶部 Issues 红条（如果有）
      if (r.issues && r.issues.length) {
        issuesCard.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '需要关注 (' + r.issues.length + ')' }),
          el('div', {}, r.issues.map(t => el('div', { class: 'text-warn', text: t })))
        ]));
      } else {
        issuesCard.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '需要关注' }),
          el('div', { class: 'text-dim', text: '一切正常 ✓' })
        ]));
      }

      // 应用 / 运行时 / 编译 三张 KV 卡
      sectionsWrap.appendChild(kvCard('应用', [
        ['名称', r.app.name],
        ['监听', r.app.listen_addr + ' (' + r.app.host + ':' + r.app.port + ')'],
        ['凭据后端', r.app.credential_store],
        ['文件浏览器', r.app.free_file_browser ? '开' : '关'],
        ['文件浏览器白名单', (r.app.free_file_roots || []).join(', ') || '（未设，按账号权限放行）'],
        ['SSH 兼容 profile', r.app.ssh_compat_profile || 'auto'],
        ['SSH 调试日志', r.app.ssh_debug ? '开' : '关'],
        ['SSH 流量镜像', r.app.ssh_traffic_dump ? '开' : '关'],
        ['SSH 日志上限', r.app.ssh_log_max_mb + ' MB × ' + r.app.ssh_log_keep + ' 份'],
        ['下载目录', r.app.download_dir],
        ['日志目录', r.app.log_dir],
        ['数据目录', r.app.data_dir],
        ['配置路径', r.app.config_path]
      ]));
      sectionsWrap.appendChild(kvCard('运行时', [
        ['Go 版本', r.build.go_version],
        ['GOOS/GOARCH', r.build.goos + '/' + r.build.goarch],
        ['x/crypto/ssh', r.build.crypto_ssh_version],
        ['数据目录可写', yesNo(r.runtime.data_dir_writable)],
        ['下载目录可写', yesNo(r.runtime.download_dir_writable)],
        ['日志目录可写', yesNo(r.runtime.log_dir_writable)],
        ['工作目录', r.runtime.working_dir],
        ['PID', String(r.runtime.pid)],
        ['Goroutine 数', String(r.runtime.num_goroutine)]
      ]));

      // 工具表
      toolsWrap.appendChild(el('div', { class: 'card' }, [
        el('h3', { text: '本机命令工具' }),
        toolsTable(r.tools)
      ]));

      // servers 表
      if (r.servers && r.servers.length) {
        serversWrap.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '配置 server 连通性 (' + r.servers.length + ' 台)' }),
          serversTable(r.servers)
        ]));
      } else {
        serversWrap.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '配置 server 连通性' }),
          el('div', { class: 'text-dim', text: '（未勾选 check_servers，或配置里没有 server）' })
        ]));
      }
    }

    function yesNo(b) {
      return b ? '✓ 是' : '✗ 否';
    }
    function kvCard(title, rows) {
      const tbl = el('table', { class: 'table' });
      const tb = el('tbody');
      rows.forEach(r => {
        tb.appendChild(el('tr', null, [
          el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
          el('td', { text: r[1] == null || r[1] === '' ? '-' : r[1] })
        ]));
      });
      tbl.appendChild(tb);
      return el('div', { class: 'card' }, [
        el('h3', { text: title }),
        tbl
      ]);
    }
    function toolsTable(t) {
      const tbl = el('table', { class: 'table' });
      const tb = el('tbody');
      const rows = [
        ['find', t.find],
        ['grep', t.grep],
        ['sed', t.sed],
        ['tail', t.tail],
        ['unzip', t.unzip],
        ['ssh', t.ssh],
        ['find 支持 -printf', t.find_supports_printf ? '✓' : '✗（AIX / 老 BSD）']
      ];
      rows.forEach(r => {
        const tc = r[1];
        if (typeof tc === 'object' && tc !== null) {
          tb.appendChild(el('tr', null, [
            el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
            el('td', { text: tc.found ? (tc.path + (tc.version ? ' · ' + tc.version : '')) : '✗ 未找到' })
          ]));
        } else {
          tb.appendChild(el('tr', null, [
            el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
            el('td', { text: String(r[1]) })
          ]));
        }
      });
      tbl.appendChild(tb);
      return tbl;
    }
    function serversTable(arr) {
      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { text: '系统 / 服务器' }),
        el('th', { text: '目标' }),
        el('th', { text: 'DNS' }),
        el('th', { text: 'TCP' }),
        el('th', { text: '耗时' }),
        el('th', { text: '详情' })
      ]));
      tbl.appendChild(thead);
      const tb = el('tbody');
      arr.forEach(s => {
        const dnsDot = s.dns && s.dns.ok ? '✓' : '✗';
        const tcpDot = s.tcp && s.tcp.ok ? '✓' : '✗';
        const detail = [];
        if (s.dns && s.dns.message) detail.push('DNS: ' + s.dns.message);
        if (s.tcp && s.tcp.message) detail.push('TCP: ' + s.tcp.message);
        if (s.error) detail.push(s.error);
        tb.appendChild(el('tr', null, [
          el('td', { text: s.system + ' · ' + s.server }),
          el('td', { class: 'mono', text: s.host + ':' + s.port }),
          el('td', { text: dnsDot }),
          el('td', { text: tcpDot }),
          el('td', { class: 'num muted', text: s.elapsed_ms + ' ms' }),
          el('td', { class: 'muted small', text: detail.join(' · ') || '-' })
        ]));
      });
      tbl.appendChild(tb);
      return tbl;
    }

    // 首次自动加载
    load(false);
  }

// 占位页
  function renderPlaceholder(view) {
    const route = (location.hash || '#/home').replace(/^#\//, '');
    const name = routeNames[route] || '此模块';
    const sub = routeSubs[route] || '功能建设中 — 后续版本提供。';
    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: name }),
      el('div', { class: 'card-desc', text: sub }),
      el('div', { class: 'text-dim', text: '本模块属于第二阶段规划。第一阶段重点是首页导航、配置读取、SSH 测试连接、列出日志文件、JSON/XML 格式化。' })
    ]));
  }

  // 系统配置 —— 可视化编辑器
  // 数据模型：把 /api/config 返回的 systems 复制到本地 state，
  // 用户在页面里增删改后，点"保存"整体 PUT 回去。
  function renderConfig(view) {
    // state.systems 是当前正在编辑的树（与后端解耦）
    const state = { systems: [], dirty: false, app: null, search: null };

    // 工具：标记 dirty
    const markDirty = () => {
      state.dirty = true;
      const btn = $('#cfg-save-btn');
      if (btn) {
        btn.disabled = false;
        btn.classList.add('btn-primary');
      }
    };

    // ---- 应用信息卡（只读展示） ----
    const appCard = el('div', { class: 'card' });
    const appKV = el('div');
    appCard.appendChild(el('h3', { text: '应用自身配置' }));
    appCard.appendChild(el('div', { class: 'card-desc', text: '应用名、端口、目录等由 config.yaml 管理，本页面只管服务器和日志目录。' }));
    appCard.appendChild(appKV);

    // ---- 搜索参数卡（只读展示） ----
    const searchCard = el('div', { class: 'card' });
    const searchKV = el('div');
    searchCard.appendChild(el('h3', { text: '搜索默认参数' }));
    searchCard.appendChild(searchKV);

    // ---- 编辑器卡（动态） ----
    const editorCard = el('div', { class: 'card' });
    editorCard.appendChild(el('h3', { text: '业务系统 / 服务器 / 日志目录' }));
    editorCard.appendChild(el('div', { class: 'card-desc', text: '增删改后点右上角"保存"才生效；保存会原子改写 config.yaml，不需要重启。' }));

    const editorBody = el('div');
    editorCard.appendChild(editorBody);

    // 顶部操作条
    const btnAddSys = el('button', { class: 'btn', text: '+ 新增业务系统', onclick: () => { state.systems.push(newSystem()); markDirty(); renderEditor(); } });
    const btnSave = el('button', { class: 'btn', id: 'cfg-save-btn', text: '保存', onclick: doSave });
    const btnReset = el('button', { class: 'btn', text: '放弃改动', onclick: doReset });
    btnSave.disabled = true;
    const topBar = el('div', { class: 'btn-row', style: 'justify-content: flex-end; margin-bottom: 12px;' }, [btnAddSys, btnReset, btnSave]);
    view.appendChild(el('div', { class: 'btn-row', style: 'justify-content: flex-end; margin-bottom: 12px;' }, [btnAddSys, btnReset, btnSave]));
    view.appendChild(appCard);
    view.appendChild(searchCard);
    view.appendChild(editorCard);

    // ----- 渲染 -----
    function renderApp() {
      appKV.innerHTML = '';
      if (!state.app) return;
      appKV.appendChild(kvTable([
        ['应用名', state.app.name],
        ['监听', state.app.host + ':' + state.app.port],
        ['自动打开浏览器', String(state.app.auto_open_browser)],
        ['下载目录', state.app.download_dir],
        ['日志目录', state.app.log_dir],
        ['数据目录', state.app.data_dir]
      ]));
    }
    function renderSearch() {
      searchKV.innerHTML = '';
      if (!state.search) return;
      searchKV.appendChild(kvTable([
        ['默认最近文件数', String(state.search.default_latest_files)],
        ['最大匹配条数', String(state.search.max_matches)],
        ['默认上下文行数', String(state.search.default_context_lines)],
        ['搜索超时（秒）', String(state.search.timeout_seconds)],
        ['最大并发', String(state.search.max_concurrency)]
      ]));
    }
    function renderEditor() {
      editorBody.innerHTML = '';
      if (!state.systems.length) {
        editorBody.appendChild(el('div', { class: 'text-dim', text: '暂无业务系统，点上方"+ 新增业务系统"开始。' }));
        return;
      }
      state.systems.forEach((sys, si) => {
        editorBody.appendChild(renderSystemBlock(sys, si));
      });
    }

    function renderSystemBlock(sys, si) {
      const wrap = el('div', { class: 'sys-block' });
      // 头部：系统名 + 描述 + 操作
      const nameInp = el('input', { type: 'text', value: sys.name || '', placeholder: '业务系统名（必填）' });
      const descInp = el('input', { type: 'text', value: sys.description || '', placeholder: '描述（可选）' });
      nameInp.addEventListener('input', () => { sys.name = nameInp.value; markDirty(); });
      descInp.addEventListener('input', () => { sys.description = descInp.value; markDirty(); });
      const btnUp = el('button', { class: 'btn btn-sm', text: '↑', title: '上移', onclick: () => { if (si > 0) { [state.systems[si-1], state.systems[si]] = [state.systems[si], state.systems[si-1]]; markDirty(); renderEditor(); } } });
      const btnDown = el('button', { class: 'btn btn-sm', text: '↓', title: '下移', onclick: () => { if (si < state.systems.length - 1) { [state.systems[si+1], state.systems[si]] = [state.systems[si], state.systems[si+1]]; markDirty(); renderEditor(); } } });
      const btnDup = el('button', { class: 'btn btn-sm', text: '复制', onclick: () => { state.systems.splice(si+1, 0, JSON.parse(JSON.stringify(sys))); markDirty(); renderEditor(); } });
      const btnDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除系统', onclick: () => { if (confirm('确认删除业务系统 “' + (sys.name || '(未命名)') + '” 及其全部服务器？')) { state.systems.splice(si, 1); markDirty(); renderEditor(); } } });
      btnUp.disabled = si === 0; btnDown.disabled = si === state.systems.length - 1;

      wrap.appendChild(el('div', { class: 'sys-head' }, [
        el('span', { class: 'sys-tag', text: '系统' }),
        el('div', { class: 'sys-name-input' }, nameInp),
        el('div', { class: 'sys-desc-input' }, descInp),
        el('div', { class: 'sys-actions' }, [btnUp, btnDown, btnDup, btnDel])
      ]));

      // 服务器列表
      // onEdit 只触发 markDirty（不动 DOM），用于输入框这种纯文本编辑
      // 结构变更（增删上下移）由按钮内部显式调用 markDirty + renderEditor
      const srvList = el('div', { class: 'srv-list' });
      (sys.servers || []).forEach((srv, sri) => {
        srvList.appendChild(renderServerBlock(sys, srv, sri, markDirty));
      });
      srvList.appendChild(el('div', { class: 'mt-2' }, [
        el('button', { class: 'btn btn-sm', text: '+ 新增服务器', onclick: () => { sys.servers = sys.servers || []; sys.servers.push(newServer()); markDirty(); renderEditor(); } })
      ]));
      wrap.appendChild(srvList);
      return wrap;
    }

    function renderServerBlock(sys, srv, sri, onEdit) {
      const wrap = el('div', { class: 'srv-block' });
      const fields = [
        ['name', '服务器名（必填）', 'text'],
        ['host', 'IP / 主机', 'text'],
        ['port', 'SSH 端口', 'number'],
        ['username', 'SSH 用户名', 'text'],
        // auth_type 后端只接受 password（config.go:191 严格校验）。
        // 改下拉框避免用户误填"key"/"publickey" 等被后端 reject。
        // 等后端支持 publickey 时，把这个 select 的 options 加上。
        ['auth_type', '认证方式', 'select', [['password', 'password（密码）']]]
      ];
      const inputMap = {};
      const fieldRow = el('div', { class: 'srv-fields' });
      fields.forEach(([key, ph, type, options]) => {
        if (type === 'select') {
          const sel = el('select');
          (options || []).forEach(([v, label]) => {
            const o = el('option', { value: v, text: label });
            if (String(srv[key] || '') === v) o.selected = true;
            sel.appendChild(o);
          });
          // 旧 config 里如果存的是别的值（理论上不可能，后端会 reject），
          // 默认回落到 password，避免空选
          if (!sel.value && options && options.length) {
            sel.value = options[0][0];
            srv[key] = sel.value;
          }
          sel.addEventListener('change', () => { srv[key] = sel.value; onEdit(); });
          inputMap[key] = sel;
          fieldRow.appendChild(el('label', null, [
            el('span', { class: 'lbl', text: ph }),
            sel
          ]));
        } else {
          const inp = el('input', { type: type, value: srv[key] != null ? String(srv[key]) : '', placeholder: ph });
          inp.addEventListener('input', () => {
            if (type === 'number') {
              const n = parseInt(inp.value, 10);
              srv[key] = isNaN(n) ? 0 : n;
            } else {
              srv[key] = inp.value;
            }
            // 只更新数据 + markDirty，不重渲染 —— 否则 input 节点会被销毁，光标立刻丢失
            onEdit();
          });
          inputMap[key] = inp;
          fieldRow.appendChild(el('label', null, [
            el('span', { class: 'lbl', text: ph }),
            inp
          ]));
        }
      });
      wrap.appendChild(fieldRow);

      // 日志目录
      const dirList = el('div', { class: 'dir-list' });
      (srv.log_dirs || []).forEach((ld, ldi) => {
        dirList.appendChild(renderDirBlock(srv, ld, ldi, onEdit));
      });
      dirList.appendChild(el('div', { class: 'mt-1' }, [
        el('button', { class: 'btn btn-sm', text: '+ 新增日志目录', onclick: () => { srv.log_dirs = srv.log_dirs || []; srv.log_dirs.push(newLogDir()); onEdit(); /* 结构性变更需要重渲染以挂载新 dir 节点 */ renderEditor(); } })
      ]));
      wrap.appendChild(dirList);

      // 服务器操作（结构性变更：markDirty + renderEditor）
      const btnSrvUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (sri > 0) { [sys.servers[sri-1], sys.servers[sri]] = [sys.servers[sri], sys.servers[sri-1]]; onEdit(); renderEditor(); } } });
      const btnSrvDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (sri < sys.servers.length - 1) { [sys.servers[sri+1], sys.servers[sri]] = [sys.servers[sri], sys.servers[sri+1]]; onEdit(); renderEditor(); } } });
      const btnSrvDup = el('button', { class: 'btn btn-sm', text: '复制', onclick: () => { sys.servers.splice(sri+1, 0, JSON.parse(JSON.stringify(srv))); onEdit(); renderEditor(); } });
      const btnSrvDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除服务器', onclick: () => { if (confirm('确认删除服务器 “' + (srv.name || '(未命名)') + '” 及其日志目录？')) { sys.servers.splice(sri, 1); onEdit(); renderEditor(); } } });
      btnSrvUp.disabled = sri === 0; btnSrvDown.disabled = sri === sys.servers.length - 1;
      wrap.appendChild(el('div', { class: 'srv-actions' }, [btnSrvUp, btnSrvDown, btnSrvDup, btnSrvDel]));

      return wrap;
    }

    function renderDirBlock(srv, ld, ldi, onEdit) {
      const wrap = el('div', { class: 'dir-block' });
      const nameInp = el('input', { type: 'text', value: ld.name || '', placeholder: '目录别名（必填）' });
      const pathInp = el('input', { type: 'text', value: ld.path || '', placeholder: '远端绝对路径（必填）' });
      const encSel = el('select', null, [
        ['utf-8', 'utf-8'],
        ['gbk', 'gbk（远程是 GBK）']
      ].map(([v, t]) => el('option', { value: v, text: t }, (ld.encoding || 'utf-8').toLowerCase() === v ? 'selected' : null)));
      const patTa = el('textarea', { rows: '2', placeholder: '文件名规则，每行一条，例如：\nSystemOut*.log\n*.log' });
      patTa.value = (ld.patterns || []).join('\n');
      // 输入框只更新数据 + markDirty，不重渲染 —— 否则 input/textarea 会被销毁，光标立刻丢失
      nameInp.addEventListener('input', () => { ld.name = nameInp.value; onEdit(); });
      pathInp.addEventListener('input', () => { ld.path = pathInp.value; onEdit(); });
      encSel.addEventListener('change', () => { ld.encoding = encSel.value; onEdit(); });
      patTa.addEventListener('input', () => {
        ld.patterns = patTa.value.split('\n').map(s => s.trim()).filter(Boolean);
        onEdit();
      });
      wrap.appendChild(el('div', { class: 'dir-fields' }, [
        el('label', null, [el('span', { class: 'lbl', text: '目录别名' }), nameInp]),
        el('label', null, [el('span', { class: 'lbl', text: '远端路径' }), pathInp]),
        el('label', null, [el('span', { class: 'lbl', text: '编码' }), encSel])
      ]));
      wrap.appendChild(el('label', { class: 'block' }, [
        el('span', { class: 'lbl', text: '文件名规则（每行一条）' }),
        patTa
      ]));
      // 结构性变更：markDirty + renderEditor
      const btnDirUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (ldi > 0) { [srv.log_dirs[ldi-1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi-1]]; onEdit(); renderEditor(); } } });
      const btnDirDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (ldi < srv.log_dirs.length - 1) { [srv.log_dirs[ldi+1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi+1]]; onEdit(); renderEditor(); } } });
      const btnDirDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除目录', onclick: () => { if (confirm('确认删除日志目录 “' + (ld.name || ld.path || '(未命名)') + '” ？')) { srv.log_dirs.splice(ldi, 1); onEdit(); renderEditor(); } } });
      btnDirUp.disabled = ldi === 0; btnDirDown.disabled = ldi === srv.log_dirs.length - 1;
      wrap.appendChild(el('div', { class: 'dir-actions' }, [btnDirUp, btnDirDown, btnDirDel]));
      return wrap;
    }

    // ----- 保存 / 重置 -----
    async function doSave() {
      // 客户端预校验
      const err = validate(state.systems);
      if (err) { toast('保存失败：' + err, 'err'); return; }
      try {
        const r = await api('PUT', '/api/admin/servers', { systems: state.systems });
        toast('已保存到 ' + r.path, 'ok');
        state.dirty = false;
        const btn = $('#cfg-save-btn');
        if (btn) { btn.disabled = true; btn.classList.remove('btn-primary'); }
      } catch (e) {
        toast('保存失败：' + e.message, 'err');
      }
    }
    function doReset() {
      if (!state.dirty || confirm('放弃所有未保存的改动？')) {
        api('GET', '/api/admin/servers').then(info => {
          state.app = info.app; state.search = info.search;
          state.systems = JSON.parse(JSON.stringify(info.systems));
          state.dirty = false;
          const btn = $('#cfg-save-btn');
          if (btn) { btn.disabled = true; btn.classList.remove('btn-primary'); }
          renderApp(); renderSearch(); renderEditor();
        });
      }
    }

    // ----- 加载 -----
    api('GET', '/api/admin/servers').then(info => {
      state.app = info.app; state.search = info.search;
      state.systems = JSON.parse(JSON.stringify(info.systems));
      renderApp(); renderSearch(); renderEditor();
    }).catch(e => toast('加载失败：' + e.message, 'err'));
  }

  // ----- 编辑器辅助 -----
  function newSystem() {
    return { name: '', description: '', servers: [newServer()] };
  }
  function newServer() {
    return { name: '', host: '', port: 22, username: '', auth_type: 'password', log_dirs: [newLogDir()] };
  }
  function newLogDir() {
    return { name: '', path: '', patterns: ['*.log'], encoding: 'utf-8' };
  }
  function validate(systems) {
    if (!systems.length) return '至少需要 1 个业务系统';
    for (let si = 0; si < systems.length; si++) {
      const s = systems[si];
      if (!s.name || !s.name.trim()) return '系统 #' + (si+1) + ' 的名称不能为空';
      if (!s.servers || !s.servers.length) return '系统 ' + s.name + ' 至少需要 1 台服务器';
      for (let sri = 0; sri < s.servers.length; sri++) {
        const srv = s.servers[sri];
        if (!srv.name || !srv.name.trim()) return '系统 ' + s.name + ' 第 ' + (sri+1) + ' 台服务器名不能为空';
        if (!srv.host || !srv.host.trim()) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 缺少 IP / 主机';
        if (!(srv.port > 0 && srv.port < 65536)) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 端口非法';
        if (!srv.log_dirs || !srv.log_dirs.length) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 至少需要 1 个日志目录';
        for (let ldi = 0; ldi < srv.log_dirs.length; ldi++) {
          const ld = srv.log_dirs[ldi];
          if (!ld.name || !ld.name.trim()) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 第 ' + (ldi+1) + ' 个目录别名不能为空';
          if (!ld.path || !ld.path.trim()) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 第 ' + (ldi+1) + ' 个目录路径不能为空';
          if (!ld.patterns || !ld.patterns.length) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 目录 ' + ld.name + ' 至少要 1 条文件名规则';
          for (const p of ld.patterns) {
            if (/[\\\;\|`$(){}<>\"'\n\r\t]/.test(p)) return '目录 ' + ld.name + ' 的文件名规则含非法字符：' + p;
          }
        }
      }
    }
    return null;
  }

  function kvTable(rows) {
    const tbl = el('table', { class: 'table' });
    const tbody = el('tbody');
    rows.forEach(r => {
      tbody.appendChild(el('tr', null, [
        el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
        el('td', { text: r[1] })
      ]));
    });
    tbl.appendChild(tbody);
    return tbl;
  }
})();
