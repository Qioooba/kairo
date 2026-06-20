/* ===== 内网运维工具箱 · 前端逻辑 =====
 * 纯原生 JS，不依赖外部资源。
 * 路由方式：hash 路由（#/home 等）。
 */

(function () {
  'use strict';

  // -------- 工具 --------
  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

  function el(tag, attrs, children) {
    const e = document.createElement(tag);
    if (attrs) {
      for (const k in attrs) {
        if (k === 'class') e.className = attrs[k];
        else if (k === 'html') e.innerHTML = attrs[k];
        else if (k === 'text') e.textContent = attrs[k];
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
    formatter: renderFormatter,
    datetime: renderPlaceholder,
    text: renderPlaceholder,
    commands: renderPlaceholder,
    config: renderConfig,
    history: renderPlaceholder
  };

  const routeNames = {
    home: '首页',
    websphere: 'WebSphere 日志助手',
    formatter: '报文格式化',
    datetime: '日期 / 日历',
    text: '文本处理',
    commands: '常用命令',
    config: '系统配置',
    history: '操作历史'
  };

  const routeSubs = {
    datetime: '功能建设中 — 时间戳 / 日期差 / 工作日计算',
    text: '功能建设中 — 去重 / 排序 / 批量替换 / 编码辅助',
    commands: '功能建设中 — grep / find / tail / WebSphere 排查模板',
    history: '功能建设中 — 后续在此展示操作历史'
  };

  function navigate() {
    const hash = (location.hash || '#/home').replace(/^#\//, '');
    const name = routes[hash] ? hash : 'home';
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
      $('#listen-info').textContent = '已启动 · ' + (info.app && info.app.name || 'OpsToolbox');
    } catch (e) { /* 忽略 */ }
    navigate();
  });

  // -------- 各页面 --------

  // 首页
  function renderHome(view) {
    view.appendChild(el('div', { class: 'mb-3' }, [
      el('div', { class: 'section-title', text: '内网运维工具箱' }),
      el('div', { class: 'section-sub', text: 'WebSphere 日志检索、报文格式化、文本处理、常用运维辅助工具。' })
    ]));

    const tools = [
      { id: 'websphere', name: 'WebSphere 日志助手', desc: '多服务器日志并行搜索、上下文查看、日志下载', icon: '📜', tag: 'ready', tagText: '已就绪' },
      { id: 'formatter', name: '报文格式化', desc: 'JSON / XML 格式化、压缩、校验', icon: '⌗', tag: 'ready', tagText: '已就绪' },
      { id: 'datetime', name: '日期 / 日历工具', desc: '时间戳转换、日期差、工作日辅助', icon: '🗓', tag: 'placeholder', tagText: '规划中' },
      { id: 'text', name: '文本处理', desc: '去重、排序、批量替换、编码辅助', icon: '✎', tag: 'placeholder', tagText: '规划中' },
      { id: 'commands', name: '常用命令', desc: 'grep / find / tail / WebSphere 排查模板', icon: '$_', tag: 'placeholder', tagText: '规划中' },
      { id: 'config', name: '系统配置', desc: '在线编辑业务系统/服务器/日志目录,改完点保存即生效', icon: '⚙', tag: 'ready', tagText: '可视化' },
      { id: 'history', name: '操作历史', desc: '本地审计日志的最近记录', icon: '⏱', tag: 'placeholder', tagText: '规划中' }
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
    let listState = { files: [] };
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
    }
    function toggleOnline() {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        cb.checked = srvStatus[cb.getAttribute('data-srv')] && srvStatus[cb.getAttribute('data-srv')].state === 'ok';
      });
    }
    function renderSrvPick() {
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
        const item = el('label', { class: 'srv-pick-item' }, [
          el('input', { type: 'checkbox', 'data-srv': s.name, value: s.name }),
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
    sysSel.addEventListener('change', () => { renderSrvPick(); refreshDirs(); });

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
        } catch (e) {
          srvStatus[n] = { state: 'fail', err: e.message };
        } finally {
          renderSrvPick();
        }
      }));
      const okN = srvs.filter(n => srvStatus[n].state === 'ok').length;
      toast(okN + '/' + srvs.length + ' 台连接成功', okN === srvs.length ? 'ok' : 'warn');
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
      } catch (e) { toast('列文件失败：' + e.message, 'err'); }
    }

    function renderFileTable() {
      fileTableWrap.innerHTML = '';
      fileTableWrap.appendChild(el('h3', { text: '文件列表 · ' + (listState.serverName || '') + '（按修改时间倒序）' }));
      if (!listState.files.length) {
        fileTableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无文件，先点击"列出文件"。' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { text: '文件名' }),
        el('th', { text: '大小' }),
        el('th', { text: '修改时间' }),
        el('th', { text: '路径' })
      ]));
      tbl.appendChild(thead);
      const tbody = el('tbody');
      listState.files.forEach(f => {
        tbody.appendChild(el('tr', null, [
          el('td', null, f.name),
          el('td', { class: 'num', text: formatBytes(f.size) }),
          el('td', { class: 'muted', text: formatTime(f.mod_time) }),
          el('td', { class: 'muted', text: f.full_path })
        ]));
      });
      tbl.appendChild(tbody);
      fileTableWrap.appendChild(tbl);
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
          const fileLinks = files.map(d =>
            '· ' + d.file + ' → <a href="/downloads/' + encodeURIComponent(d.local) + '">' + d.local + '</a>（' + formatBytes(d.bytes) + '）'
          ).join('<br/>');
          const zipLinks = zips.map(d =>
            '· 📦 <a href="/downloads/' + encodeURIComponent(d.local) + '">' + d.local + '</a>（' + formatBytes(d.bytes) + '）'
          ).join('<br/>');
          const inner = el('div', { style: 'padding: 8px 12px; font-size: 12.5px;' });
          if (fileLinks) inner.appendChild(el('div', { html: fileLinks }));
          if (zipLinks) inner.appendChild(el('div', { class: 'mt-2', html: zipLinks }));
          grp.appendChild(inner);
        }
        wrap.appendChild(grp);
      });
      wrap.appendChild(el('div', { class: 'text-dim mt-2', text: '本地保存目录：' + (allResults.find(r => r.folder) || {}).folder || '-' }));
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
        const head = el('div', { class: 'server-group-head' }, [
          el('span', { class: 'dot dot-' + (srv.ok ? (srv.hits_count > 0 ? 'ok' : 'idle') : 'err') }),
          el('span', { class: 'name', text: srv.server + (srv.host ? '  ·  ' + srv.host : '') }),
          el('span', { class: 'meta', text:
            (srv.ok
              ? (srv.hits_count + ' 命中 / ' + (srv.files || []).length + ' 文件 / ' + srv.elapsed_ms + 'ms')
              : '失败 / ' + srv.elapsed_ms + 'ms') })
        ]);
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
            tbody.appendChild(el('tr', null, [
              el('td', { class: 'muted', text: h.file }),
              el('td', { class: 'num', text: h.line_no }),
              el('td', { class: 'hit-line', text: trimMiddle(h.content, 280) }),
              el('td', { class: 'actions' }, [
                el('button', { class: 'btn btn-sm', text: '上下文', onclick: () => doContext(h) })
              ])
            ]));
          });
          tbl.appendChild(tbody);
          grp.appendChild(tbl);
        }
        hitTableWrap.appendChild(grp);
      });
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
      el('div', { class: 'card-desc', html: '语法：<span class="code-inline">A &amp;&amp; B</span>（同包含）、<span class="code-inline">A || B</span>（任一）、<span class="code-inline">!X</span>（排除）。结果按服务器分组。' }),
      el('div', { class: 'grid-3' }, [
        el('div', { style: 'grid-column: span 2' }, [el('label', { text: '搜索表达式' }), queryInp]),
        el('div', null, [el('label', { text: '并发' }), concSel])
      ]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: '搜索范围（每台）' }), filesNSel])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnSearch])
    ]);

    view.appendChild(formCard);
    view.appendChild(searchCard);
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
    }).catch(e => toast('配置加载失败：' + e.message, 'err'));
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
      const srvList = el('div', { class: 'srv-list' });
      (sys.servers || []).forEach((srv, sri) => {
        srvList.appendChild(renderServerBlock(sys, srv, sri, () => { markDirty(); renderEditor(); }));
      });
      srvList.appendChild(el('div', { class: 'mt-2' }, [
        el('button', { class: 'btn btn-sm', text: '+ 新增服务器', onclick: () => { sys.servers = sys.servers || []; sys.servers.push(newServer()); markDirty(); renderEditor(); } })
      ]));
      wrap.appendChild(srvList);
      return wrap;
    }

    function renderServerBlock(sys, srv, sri, onChange) {
      const wrap = el('div', { class: 'srv-block' });
      const fields = [
        ['name', '服务器名（必填）', 'text'],
        ['host', 'IP / 主机', 'text'],
        ['port', 'SSH 端口', 'number'],
        ['username', 'SSH 用户名', 'text'],
        ['auth_type', '认证方式（password）', 'text']
      ];
      const inputMap = {};
      const fieldRow = el('div', { class: 'srv-fields' });
      fields.forEach(([key, ph, type]) => {
        const inp = el('input', { type: type, value: srv[key] != null ? String(srv[key]) : '', placeholder: ph });
        inp.addEventListener('input', () => {
          if (type === 'number') {
            const n = parseInt(inp.value, 10);
            srv[key] = isNaN(n) ? 0 : n;
          } else {
            srv[key] = inp.value;
          }
          onChange();
        });
        inputMap[key] = inp;
        fieldRow.appendChild(el('label', null, [
          el('span', { class: 'lbl', text: ph }),
          inp
        ]));
      });
      wrap.appendChild(fieldRow);

      // 日志目录
      const dirList = el('div', { class: 'dir-list' });
      (srv.log_dirs || []).forEach((ld, ldi) => {
        dirList.appendChild(renderDirBlock(srv, ld, ldi, onChange));
      });
      dirList.appendChild(el('div', { class: 'mt-1' }, [
        el('button', { class: 'btn btn-sm', text: '+ 新增日志目录', onclick: () => { srv.log_dirs = srv.log_dirs || []; srv.log_dirs.push(newLogDir()); onChange(); /* 重新渲染整块以拿到新 dir 节点 */ renderEditor(); } })
      ]));
      wrap.appendChild(dirList);

      // 服务器操作
      const btnSrvUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (sri > 0) { [sys.servers[sri-1], sys.servers[sri]] = [sys.servers[sri], sys.servers[sri-1]]; onChange(); renderEditor(); } } });
      const btnSrvDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (sri < sys.servers.length - 1) { [sys.servers[sri+1], sys.servers[sri]] = [sys.servers[sri], sys.servers[sri+1]]; onChange(); renderEditor(); } } });
      const btnSrvDup = el('button', { class: 'btn btn-sm', text: '复制', onclick: () => { sys.servers.splice(sri+1, 0, JSON.parse(JSON.stringify(srv))); onChange(); renderEditor(); } });
      const btnSrvDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除服务器', onclick: () => { if (confirm('确认删除服务器 “' + (srv.name || '(未命名)') + '” 及其日志目录？')) { sys.servers.splice(sri, 1); onChange(); renderEditor(); } } });
      btnSrvUp.disabled = sri === 0; btnSrvDown.disabled = sri === sys.servers.length - 1;
      wrap.appendChild(el('div', { class: 'srv-actions' }, [btnSrvUp, btnSrvDown, btnSrvDup, btnSrvDel]));

      return wrap;
    }

    function renderDirBlock(srv, ld, ldi, onChange) {
      const wrap = el('div', { class: 'dir-block' });
      const nameInp = el('input', { type: 'text', value: ld.name || '', placeholder: '目录别名（必填）' });
      const pathInp = el('input', { type: 'text', value: ld.path || '', placeholder: '远端绝对路径（必填）' });
      const encSel = el('select', null, [
        ['utf-8', 'utf-8'],
        ['gbk', 'gbk（远程是 GBK）']
      ].map(([v, t]) => el('option', { value: v, text: t }, (ld.encoding || 'utf-8').toLowerCase() === v ? 'selected' : null)));
      const patTa = el('textarea', { rows: '2', placeholder: '文件名规则，每行一条，例如：\nSystemOut*.log\n*.log' });
      patTa.value = (ld.patterns || []).join('\n');
      nameInp.addEventListener('input', () => { ld.name = nameInp.value; onChange(); });
      pathInp.addEventListener('input', () => { ld.path = pathInp.value; onChange(); });
      encSel.addEventListener('change', () => { ld.encoding = encSel.value; onChange(); });
      patTa.addEventListener('input', () => {
        ld.patterns = patTa.value.split('\n').map(s => s.trim()).filter(Boolean);
        onChange();
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
      const btnDirUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (ldi > 0) { [srv.log_dirs[ldi-1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi-1]]; onChange(); renderEditor(); } } });
      const btnDirDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (ldi < srv.log_dirs.length - 1) { [srv.log_dirs[ldi+1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi+1]]; onChange(); renderEditor(); } } });
      const btnDirDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除目录', onclick: () => { if (confirm('确认删除日志目录 “' + (ld.name || ld.path || '(未命名)') + '” ？')) { srv.log_dirs.splice(ldi, 1); onChange(); renderEditor(); } } });
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
