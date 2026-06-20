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
      { id: 'websphere', name: 'WebSphere 日志助手', desc: '多服务器日志搜索、上下文查看、日志下载', icon: '📜', tag: 'ready', tagText: '已就绪' },
      { id: 'formatter', name: '报文格式化', desc: 'JSON / XML 格式化、压缩、校验', icon: '⌗', tag: 'ready', tagText: '已就绪' },
      { id: 'datetime', name: '日期 / 日历工具', desc: '时间戳转换、日期差、工作日辅助', icon: '🗓', tag: 'placeholder', tagText: '规划中' },
      { id: 'text', name: '文本处理', desc: '去重、排序、批量替换、编码辅助', icon: '✎', tag: 'placeholder', tagText: '规划中' },
      { id: 'commands', name: '常用命令', desc: 'grep / find / tail / WebSphere 排查模板', icon: '$_', tag: 'placeholder', tagText: '规划中' },
      { id: 'config', name: '系统配置', desc: '查看当前 config.yaml 中声明的服务器与目录', icon: '⚙', tag: 'ready', tagText: '只读' },
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

  // WebSphere 日志助手
  function renderWebsphere(view) {
    let cfg = null;
    let listState = { files: [] };

    const sysSel = el('select', { id: 'ws-sys' });
    const srvSel = el('select', { id: 'ws-srv' });
    const dirSel = el('select', { id: 'ws-dir' });
    const userInp = el('input', { type: 'text', id: 'ws-user', placeholder: 'SSH 用户名（可留空，使用配置默认）' });
    const passInp = el('input', { type: 'password', id: 'ws-pass', placeholder: 'SSH 密码' });
    const queryInp = el('input', { type: 'text', id: 'ws-query', placeholder: '例: Exception && userinfo   或   !DEBUG', value: 'Exception' });
    const filesNSel = el('select', { id: 'ws-files-n' },
      [1, 3, 5, 10].map(n => el('option', { value: String(n), text: '最近 ' + n + ' 个文件' }, n === 3 ? 'selected' : null)));

    const btnTest = el('button', { class: 'btn', text: '测试连接', onclick: doTest });
    const btnList = el('button', { class: 'btn btn-primary', text: '列出文件', onclick: doList });
    const btnDownload = el('button', { class: 'btn', text: '下载最新日志', onclick: doDownload });
    const btnSearch = el('button', { class: 'btn btn-primary', text: '搜索', onclick: doSearch });

    const fileTableWrap = el('div', { class: 'card', style: 'display:none' });
    const hitTableWrap = el('div', { class: 'card', style: 'display:none' });
    const ctxCard = el('div', { class: 'card', style: 'display:none' });

    function refreshServers() {
      srvSel.innerHTML = '';
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      (sys ? sys.servers : []).forEach(s => {
        srvSel.appendChild(el('option', { value: s.name, text: s.name + '  (' + s.host + ')' }));
      });
      refreshDirs();
    }
    function refreshDirs() {
      dirSel.innerHTML = '';
      const sysName = sysSel.value;
      const srvName = srvSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      const srv = sys && sys.servers.find(s => s.name === srvName);
      (srv ? srv.log_dirs : []).forEach(d => {
        dirSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
      });
    }
    sysSel.addEventListener('change', refreshServers);
    srvSel.addEventListener('change', refreshDirs);

    function creds() {
      return { system: sysSel.value, server: srvSel.value, dir: dirSel.value,
               username: userInp.value, password: passInp.value };
    }

    async function doTest() {
      try {
        const r = await api('POST', '/api/ssh/test', creds());
        toast('连接成功：' + r.server, 'ok');
      } catch (e) { toast('连接失败：' + e.message, 'err'); }
    }

    async function doList() {
      try {
        const r = await api('POST', '/api/logs/list', creds());
        listState.files = r.files || [];
        renderFileTable();
        fileTableWrap.style.display = '';
        toast('共 ' + listState.files.length + ' 个文件', 'ok');
      } catch (e) { toast('列文件失败：' + e.message, 'err'); }
    }

    function renderFileTable() {
      fileTableWrap.innerHTML = '';
      fileTableWrap.appendChild(el('h3', { text: '文件列表（按修改时间倒序）' }));
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
      try {
        const r = await api('POST', '/api/logs/download-latest', Object.assign({}, creds(), { latest: 1 }));
        const items = r.downloads || [];
        if (!items.length) { toast('没有可下载文件', 'warn'); return; }
        const links = items.map(d =>
          '· ' + d.file + ' → <a href="/downloads/' + encodeURIComponent(d.local) + '">' + d.local + '</a>（' + formatBytes(d.bytes) + '）'
        ).join('<br/>');
        toast('下载完成：' + items[0].local, 'ok');
        const card = el('div', { class: 'card' }, [
          el('h3', { text: '下载完成' }),
          el('div', { html: links, class: 'mt-2' }),
          el('div', { class: 'text-dim mt-2', text: '本地保存目录：' + r.folder })
        ]);
        fileTableWrap.parentNode.insertBefore(card, fileTableWrap.nextSibling);
      } catch (e) { toast('下载失败：' + e.message, 'err'); }
    }

    async function doSearch() {
      const body = Object.assign({}, creds(), {
        query: queryInp.value,
        files: Number(filesNSel.value)
      });
      try {
        const r = await api('POST', '/api/logs/search', body);
        renderHits(r.hits || [], r.files || []);
        hitTableWrap.style.display = '';
        toast('命中 ' + (r.hits || []).length + ' 条', 'ok');
      } catch (e) { toast('搜索失败：' + e.message, 'err'); }
    }

    function renderHits(hits, files) {
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '搜索结果' }));
      hitTableWrap.appendChild(el('div', { class: 'text-dim mb-2', text: '搜索范围：' + (files.join(', ') || '-') }));
      if (!hits.length) {
        hitTableWrap.appendChild(el('div', { class: 'text-dim', text: '没有匹配。' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { text: '服务器' }),
        el('th', { text: '文件' }),
        el('th', { text: '行号' }),
        el('th', { text: '内容' }),
        el('th', { text: '操作' })
      ]));
      tbl.appendChild(thead);
      const tbody = el('tbody');
      hits.forEach((h, idx) => {
        tbody.appendChild(el('tr', null, [
          el('td', null, h.server),
          el('td', { class: 'muted', text: h.file }),
          el('td', { class: 'num', text: h.line_no }),
          el('td', { class: 'hit-line', text: trimMiddle(h.content, 280) }),
          el('td', { class: 'actions' }, [
            el('button', { class: 'btn', text: '查看上下文', onclick: () => doContext(h) })
          ])
        ]));
      });
      tbl.appendChild(tbody);
      hitTableWrap.appendChild(tbl);
    }

    function trimMiddle(s, max) {
      s = s || '';
      if (s.length <= max) return s;
      const keep = Math.floor((max - 1) / 2);
      return s.slice(0, keep) + '…' + s.slice(s.length - keep);
    }

    async function doContext(hit) {
      const body = Object.assign({}, creds(), {
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
      ctxCard.appendChild(el('h3', { text: '上下文 · ' + hit.file + ':' + hit.line_no }));
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
      el('h3', { text: 'WebSphere 日志助手' }),
      el('div', { class: 'card-desc', text: '选择系统、服务器、目录 → 输入 SSH 凭据 → 列出 / 下载 / 搜索日志。' }),
      el('div', { class: 'grid-3' }, [
        el('div', null, [el('label', { text: '系统' }), sysSel]),
        el('div', null, [el('label', { text: '服务器' }), srvSel]),
        el('div', null, [el('label', { text: '日志目录' }), dirSel])
      ]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: 'SSH 用户名' }), userInp]),
        el('div', null, [el('label', { text: 'SSH 密码' }), passInp])
      ]),
      el('div', { class: 'btn-row mt-3' }, [btnTest, btnList, btnDownload])
    ]);

    const searchCard = el('div', { class: 'card' }, [
      el('h3', { text: '关键词搜索' }),
      el('div', { class: 'card-desc', html: '语法：<span class="code-inline">A &amp;&amp; B</span>（同包含）、<span class="code-inline">A || B</span>（任一）、<span class="code-inline">!X</span>（排除）。' }),
      el('div', { class: 'grid-3' }, [
        el('div', { style: 'grid-column: span 2' }, [el('label', { text: '搜索表达式' }), queryInp]),
        el('div', null, [el('label', { text: '搜索范围' }), filesNSel])
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
      refreshServers();
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

  // 系统配置
  function renderConfig(view) {
    api('GET', '/api/config').then(info => {
      view.appendChild(el('div', { class: 'card' }, [
        el('h3', { text: '应用配置' }),
        el('div', { class: 'card-desc', text: '读取自当前 config.yaml（密码不会出现在这里）。' }),
        kvTable([
          ['应用名', info.app.name],
          ['监听', info.app.host + ':' + info.app.port],
          ['自动打开浏览器', String(info.app.auto_open_browser)],
          ['下载目录', info.app.download_dir],
          ['日志目录', info.app.log_dir],
          ['数据目录', info.app.data_dir]
        ])
      ]));

      info.systems.forEach(sys => {
        const card = el('div', { class: 'card' });
        card.appendChild(el('h3', { text: sys.name + (sys.description ? ' · ' + sys.description : '') }));
        sys.servers.forEach(srv => {
          card.appendChild(el('div', { class: 'mt-2' }, [
            el('div', { class: 'row gap-2 mb-2' }, [
              el('strong', { text: srv.name }),
              el('span', { class: 'muted', text: srv.host + ':' + srv.port + '  ·  ' + srv.username })
            ])
          ]));
          const tbl = el('table', { class: 'table' });
          tbl.appendChild(el('thead', null, el('tr', null, [
            el('th', { text: '目录名' }),
            el('th', { text: '路径' }),
            el('th', { text: '文件规则' }),
            el('th', { text: '编码' })
          ])));
          const tbody = el('tbody');
          srv.log_dirs.forEach(d => {
            tbody.appendChild(el('tr', null, [
              el('td', null, d.name),
              el('td', { class: 'muted', text: d.path }),
              el('td', { class: 'muted', text: (d.patterns || []).join(', ') }),
              el('td', { class: 'muted', text: d.encoding || 'utf-8' })
            ]));
          });
          tbl.appendChild(tbody);
          card.appendChild(tbl);
        });
        view.appendChild(card);
      });

      view.appendChild(el('div', { class: 'card' }, [
        el('h3', { text: '搜索默认参数' }),
        kvTable([
          ['默认最近文件数', String(info.search.default_latest_files)],
          ['最大匹配条数', String(info.search.max_matches)],
          ['默认上下文行数', String(info.search.default_context_lines)],
          ['搜索超时（秒）', String(info.search.timeout_seconds)],
          ['最大并发', String(info.search.max_concurrency)]
        ])
      ]));
    });
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
