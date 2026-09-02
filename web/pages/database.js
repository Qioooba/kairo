/* Database Workbench — read-only Oracle/MySQL/Redis console. */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { toast, escapeHtml, copyToClipboard, el } = Kairo.core;
  const { api, getPreference, putPreference, preferenceSaver } = Kairo.api;
  const LAST_SOURCE = 'kairo:database:last-source';
  const PREFS_KEY = 'kairo:database:workbench-prefs:v2';
  const HISTORY_KEY = 'kairo:database:sql-history:v1';
  const HISTORY_LIMIT = 20;
  const HISTORY_ENTRY_MAX = 2000;
  const BOOKMARK_LIMIT = 50;
  const BOOKMARK_SQL_MAX = 20000;
  const TAB_LIMIT = 6;
  const META_WIDTH_MIN = 220;
  const META_WIDTH_MAX = 720;
  const DEFAULT_PREFS = {
    expandKey: 'Space',
    shortcuts: { run: 'Ctrl+Enter', cancel: 'Escape', grid: 'Alt+1', record: 'Alt+2', explain: 'Ctrl+Shift+E' },
    gridRows: 16,
    snippets: [
      { key: 'sf', text: 'SELECT * FROM ', enabled: true },
      { key: 'sel', text: 'SELECT *\nFROM ${table}', enabled: true },
      { key: 'cnt', text: 'SELECT COUNT(*)\nFROM ${table}', enabled: true }
    ]
  };
  let persisted = { prefs: null, history: [], last_source: '', column_widths: {}, row_limits: {} };
  const persistPreference = preferenceSaver('database', 400);
  const state = {
    sources: [], source: null, rows: [], columns: [], controller: null, summary: null, cursor: 0,
    managing: false, workspaceToken: 0, lastSQL: '', lastMaxRows: 0, resultMode: 'grid',
    selectedRow: 0, localFilter: '', hiddenColumns: new Set(), columnWidths: {}, sort: null,
    prefs: normalizePrefs({}), lastError: null, inspectTab: 'fields', inspect: null, plan: [], gridReady: false,
    sessions: [], activeId: 0, redisKeyBase64: '', redisType: '', redisCursor: '0', redisNextCursor: '0', redisCursorHistory: [], redisOffset: 0, redisPageSize: 100, redisMembersHasNext: false
  };
  let tabSeq = 0;
  const acState = { open: false, items: [], index: 0, start: 0, end: 0 };
  const GRID_ROW_H = 31;
  const GRID_HEAD_H = 36;
  const GRID_BUFFER = 12;
  let gridIndexCache = { rows: null, len: -1, filter: '', sort: null, indexes: null };
  const gridView = { indexes: [], visible: [], start: -1, end: -1, length: -1, head: null, tail: null, mid: null, raf: 0 };

  function h(v) { return escapeHtml(String(v == null ? '' : v)); }
  function q(id) { return document.getElementById(id); }
  function kindLabel(kind) { return kind === 'oracle' ? 'Oracle' : kind === 'mysql' ? 'MySQL' : 'Redis'; }
  function copyDBText(value, successText) {
    let pending;
    try {
      pending = copyToClipboard(String(value == null ? '' : value));
    } catch (e) {
      toast('复制失败：' + (e && e.message ? e.message : '剪贴板不可用'), 'err');
      return;
    }
    Promise.resolve(pending).then(function () {
      toast(successText, 'ok');
    }).catch(function (e) {
      toast('复制失败：' + (e && e.message ? e.message : '剪贴板不可用'), 'err');
    });
  }
  function sess() { return state.sessions.find(function (s) { return s.id === state.activeId; }) || state.sessions[0] || null; }
  function effectiveSource() {
    const s = sess();
    if (s && s.sourceId) return state.sources.find(function (x) { return x.id === s.sourceId; }) || null;
    return state.source || null;
  }
  function sessionSourceState(s) {
    s = s || sess();
    if (!s || !s.sourceId) return { status: state.source ? 'ready' : 'removed', source: state.source || null };
    const found = state.sources.find(function (x) { return x.id === s.sourceId; }) || null;
    return found ? { status: 'ready', source: found } : { status: 'removed', source: null };
  }
  function bindSessionSource(s, source) {
    if (!s || !source) return;
    s.sourceId = source.id;
    s.sourceRef = Kairo.workbench && Kairo.workbench.resourceSession ? Kairo.workbench.resourceSession.snapshot(source) : { id: source.id, name: source.name, kind: source.kind };
  }
  function createSession(sql) {
    return {
      id: ++tabSeq, sql: sql || '', rows: [], columns: [], summary: null, lastError: null,
      lastSQL: '', lastMaxRows: 0, resultMode: 'grid', selectedRow: 0, localFilter: '',
      hiddenColumns: new Set(), sort: null, plan: [], gridReady: false, controller: null,
      runSeq: 0, status: '就绪', sourceId: state.source ? state.source.id : '', sourceRef: state.source && Kairo.workbench && Kairo.workbench.resourceSession ? Kairo.workbench.resourceSession.snapshot(state.source) : null
    };
  }
  function bindSession(s) {
    if (!s) return;
    state.rows = s.rows; state.columns = s.columns; state.controller = s.controller;
    state.summary = s.summary; state.lastSQL = s.lastSQL; state.lastMaxRows = s.lastMaxRows;
    state.resultMode = s.resultMode; state.selectedRow = s.selectedRow; state.localFilter = s.localFilter;
    state.hiddenColumns = s.hiddenColumns; state.sort = s.sort; state.lastError = s.lastError;
    state.plan = s.plan; state.gridReady = false;
  }
  function saveEditorSQL() {
    const s = sess(), ta = q('db-sql');
    if (s && ta) s.sql = ta.value;
    if (s) {
      s.resultMode = state.resultMode; s.selectedRow = state.selectedRow;
      s.localFilter = state.localFilter; s.hiddenColumns = state.hiddenColumns;
      s.sort = state.sort; s.lastError = state.lastError; s.plan = state.plan;
      s.rows = state.rows; s.columns = state.columns; s.summary = state.summary;
      s.lastSQL = state.lastSQL; s.lastMaxRows = state.lastMaxRows; s.controller = state.controller;
      if (!s.sourceId && state.source) bindSessionSource(s, state.source);
    }
  }
  function tabTitle(s) {
    const text = String(s.sql || '').replace(/\s+/g, ' ').trim();
    return text ? text.slice(0, 18) : ('查询 ' + s.id);
  }
  function abortAllSessions() {
    (state.sessions || []).forEach(function (s) {
      if (s.controller) { try { s.controller.abort(); } catch (_) {} }
      s.controller = null;
    });
    state.controller = null;
  }
  function refreshActiveQueryUI(s) {
    s = s || sess();
    renderTabs();
    if (!s || s.id !== state.activeId) return;
    bindSession(s);
    const run = q('db-run'), cancel = q('db-cancel'), exp = q('db-export'), st = q('db-query-status');
    if (run) run.disabled = !!s.controller;
    if (cancel) cancel.disabled = !s.controller;
    if (exp) exp.disabled = !s.lastSQL || !!s.controller || !(s.rows && s.rows.length);
    if (st) st.textContent = s.status || '就绪';
    updateDatabasePager();
  }
  function renderTabs() {
    const host = q('db-sql-tabs');
    if (!host) return;
    host.innerHTML = state.sessions.map(function (s) {
       const src = s.sourceId ? (state.sources.find(function (x) { return x.id === s.sourceId; }) || null) : null;
       const srcName = src ? ' [' + src.name + ']' : (s.sourceRef && s.sourceRef.name ? ' [' + s.sourceRef.name + ' · 已删除]' : ' [未绑定]');
       const tip = h((s.sourceId ? ((src && src.name) || (s.sourceRef && s.sourceRef.name) || s.sourceId) + ' · ' : '') + (s.sql || tabTitle(s)));
       return '<button type="button" class="db-editor-tab' + (s.id === state.activeId ? ' active' : '') + (s.controller ? ' running' : '') + (!src && s.sourceId ? ' orphan' : '') + '" data-tab="' + s.id + '" title="' + tip + '"><span class="db-tab-dot" aria-hidden="true"></span><span class="db-tab-name">' + h(tabTitle(s)) + h(srcName) + '</span>' + (state.sessions.length > 1 ? '<span class="db-tab-close" data-close="' + s.id + '" title="关闭页签">×</span>' : '') + '</button>';
    }).join('');
    host.querySelectorAll('[data-tab]').forEach(function (btn) {
      btn.onclick = function (e) {
        if (e.target.closest('[data-close]')) return;
        switchSession(Number(btn.dataset.tab));
      };
    });
    host.querySelectorAll('[data-close]').forEach(function (btn) {
      btn.onclick = function (e) {
        e.preventDefault(); e.stopPropagation();
        closeSession(Number(btn.dataset.close));
      };
    });
  }
  function addSession() {
    if (state.sessions.length >= TAB_LIMIT) return toast('最多 ' + TAB_LIMIT + ' 个查询页签', 'warn');
    saveEditorSQL();
    const s = createSession('');
    state.sessions.push(s);
    switchSession(s.id);
  }
  function switchSession(id) {
    if (id === state.activeId) return;
    hideComplete();
    saveEditorSQL();
    const s = state.sessions.find(function (x) { return x.id === id; });
    if (!s) return;
    cancelGridPaint();
    state.activeId = id;
    // #13：页签保存自己的数据源；切换到不同类型时必须重建可见工作区，
    // 否则 Redis 页签会继续显示 SQL DOM，或沿用上一数据源的元数据。
    const found = s.sourceId ? state.sources.find(function (x) { return x.id === s.sourceId; }) : null;
    if (found) {
      state.source = found;
      const sel = q('db-source');
      if (sel) sel.value = found.id;
      renderWorkspace(true);
      return;
    }
    renderWorkspace(true);
  }
  function closeSession(id) {
    if (state.sessions.length <= 1) return;
    const index = state.sessions.findIndex(function (x) { return x.id === id; });
    if (index < 0) return;
    const dying = state.sessions[index];
    if (dying.controller) { try { dying.controller.abort(); } catch (_) {} }
    if (id === state.activeId) saveEditorSQL();
    state.sessions.splice(index, 1);
    const next = state.sessions[Math.min(index, state.sessions.length - 1)];
    state.activeId = next.id;
    const nextSource = next.sourceId ? state.sources.find(function (x) { return x.id === next.sourceId; }) : null;
    if (nextSource) {
      state.source = nextSource;
      const select = q('db-source');
      if (select) select.value = nextSource.id;
      renderWorkspace(true);
    } else {
      renderWorkspace(true);
    }
  }
  function restoreSessionChrome(s) {
    const ta = q('db-sql');
    if (ta) ta.value = s.sql || '';
    syncSQLEditor();
    const filter = q('db-result-filter');
    if (filter) filter.value = s.localFilter || '';
    ['grid', 'record', 'plan'].forEach(function (name) {
      const btn = q('db-view-' + name);
      if (btn) btn.classList.toggle('active', s.resultMode === name);
    });
    if (s.lastError) showQueryMessage('error', s.lastError.title, s.lastError.message, s.lastError.sql);
    else hideQueryMessage();
    refreshActiveQueryUI(s);
    renderResult();
  }
  function normalizePrefs(x) {
    x = x && typeof x === 'object' ? x : {};
    return {
      expandKey: ['Space', 'Tab', 'Enter'].includes(x.expandKey) ? x.expandKey : 'Space',
      shortcuts: Object.assign({}, DEFAULT_PREFS.shortcuts, x.shortcuts || {}),
      gridRows: Math.max(6, Math.min(40, Number(x.gridRows) || DEFAULT_PREFS.gridRows)),
      snippets: Array.isArray(x.snippets) && x.snippets.length ? x.snippets : DEFAULT_PREFS.snippets.map(v => Object.assign({}, v))
    };
  }
  function legacyPrefs() {
    try { return normalizePrefs(JSON.parse(localStorage.getItem(PREFS_KEY) || '{}')); }
    catch (_) { return normalizePrefs({}); }
  }
  function savePersisted() { persistPreference(persisted); }
  function savePrefs() { persisted.prefs = state.prefs; savePersisted(); }
  function loadHistory() {
    return Array.isArray(persisted.history) ? persisted.history : [];
  }
  function pushHistory(sql) {
    const text = String(sql || '').trim();
    if (!text || text.length > HISTORY_ENTRY_MAX) return;
    const items = loadHistory().filter(x => x !== text);
    items.unshift(text);
    persisted.history = items.slice(0, HISTORY_LIMIT);
    savePersisted();
    refreshHistorySelect();
  }
  function refreshHistorySelect() {
    const select = q('db-history');
    if (!select) return;
    const items = loadHistory();
    const current = select.value;
    select.innerHTML = '<option value="">历史</option>' + items.map(function (sql, i) {
      return '<option value="' + i + '">' + h(sql.replace(/\s+/g, ' ').slice(0, 80)) + '</option>';
    }).join('');
    select.value = current && items[Number(current)] ? current : '';
  }
  function loadColumnWidths() {
    state.columnWidths = Object.assign({}, (state.source && persisted.column_widths[state.source.id]) || {});
  }
  function saveColumnWidths() {
    if (!state.source) return;
    persisted.column_widths[state.source.id] = Object.assign({}, state.columnWidths);
    savePersisted();
  }

  async function loadDatabasePreference() {
    let legacyHistory = [];
    try { legacyHistory = JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]'); } catch (_) {}
    const legacy = {
      prefs: legacyPrefs(), history: Array.isArray(legacyHistory) ? legacyHistory.filter(x => typeof x === 'string' && x.length <= HISTORY_ENTRY_MAX).slice(0, HISTORY_LIMIT) : [],
      last_source: localStorage.getItem(LAST_SOURCE) || '', column_widths: {}, row_limits: {}
    };
    try {
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i), prefix = 'kairo:database:column-widths:';
        if (key && key.indexOf(prefix) === 0) {
          try { legacy.column_widths[key.slice(prefix.length)] = JSON.parse(localStorage.getItem(key) || '{}'); } catch (_) {}
        }
      }
      const remote = await getPreference('database');
      persisted = remote.exists && remote.value && typeof remote.value === 'object' ? remote.value : legacy;
      if (!remote.exists) await putPreference('database', persisted);
      const remove = [];
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key && key.indexOf('kairo:database:column-widths:') === 0) remove.push(key);
      }
      remove.forEach(key => localStorage.removeItem(key));
      localStorage.removeItem(PREFS_KEY); localStorage.removeItem(HISTORY_KEY); localStorage.removeItem(LAST_SOURCE);
    } catch (_) { persisted = legacy; }
    persisted.prefs = normalizePrefs(persisted.prefs);
    persisted.history = Array.isArray(persisted.history) ? persisted.history.filter(x => typeof x === 'string' && x.length <= HISTORY_ENTRY_MAX).slice(0, HISTORY_LIMIT) : [];
    persisted.last_source = String(persisted.last_source || '');
    persisted.column_widths = persisted.column_widths && typeof persisted.column_widths === 'object' ? persisted.column_widths : {};
    persisted.row_limits = persisted.row_limits && typeof persisted.row_limits === 'object' ? persisted.row_limits : {};
    persisted.bookmarks = normalizeBookmarks(persisted.bookmarks);
    persisted.meta_width = Math.max(META_WIDTH_MIN, Math.min(META_WIDTH_MAX, Number(persisted.meta_width) || 300));
    persisted.inspect_height = Math.max(140, Math.min(480, Number(persisted.inspect_height) || 220));
    state.prefs = persisted.prefs;
  }
  function cellText(v) {
    if (v == null) return 'NULL';
    if (typeof v === 'object') {
      if (v.kind === 'text') return String(v.preview || '');
      if (v.kind === 'binary') return '[BINARY ' + v.bytes + ' B]';
      return JSON.stringify(v);
    }
    return String(v);
  }
  function fmtCell(v) {
    if (v == null) return '<span class="db-null">NULL</span>';
    if (typeof v === 'object') {
      if (v.kind === 'text') return h(v.preview) + '<span class="db-cut">…(' + h(v.bytes) + ' B)</span>';
      if (v.kind === 'binary') return '<span class="db-binary">BINARY ' + h(v.bytes) + ' B</span>';
      return h(JSON.stringify(v));
    }
    return h(v);
  }
  function fmtRedisLabel(v) {
    if (v == null) return '';
    if (typeof v !== 'object') return String(v);
    if (v.kind === 'binary') return '[BINARY ' + v.bytes + ' B] ' + (v.preview_base64 || '');
    if (v.kind === 'text') return String(v.preview || '') + '…';
    return JSON.stringify(v);
  }

  async function loadSources(preferred) {
    const data = await api('GET', '/api/database/sources');
    state.sources = data.sources || [];
    const wanted = preferred || persisted.last_source;
    state.source = state.sources.find(s => s.id === wanted) || state.sources[0] || null;
    if (state.source) { persisted.last_source = state.source.id; savePersisted(); }
  }
  function sourceOptions() {
    return state.sources.map(s => '<option value="' + h(s.id) + '"' + (state.source && s.id === state.source.id ? ' selected' : '') + '>' + h(s.name) + ' · ' + kindLabel(s.kind) + '</option>').join('');
  }
  function render(view) {
    cancelGridPaint();
    state.managing = false;
    state.resultMode = 'grid';
    state.gridReady = false;
    const user = Kairo.auth && Kairo.auth.getUser ? Kairo.auth.getUser() : '';
    const role = Kairo.auth && Kairo.auth.getRole ? Kairo.auth.getRole() : '';
    const canManage = !user || role === 'admin';
    view.innerHTML = '<div class="db-page"><header class="db-topbar card"><div class="db-source-select"><label for="db-source">数据源</label><select id="db-source">' + sourceOptions() + '</select></div><span id="db-source-badge" class="db-kind"></span><div class="db-topbar-actions"><button class="btn btn-sm" id="db-test">测试连接</button>' + (canManage ? '<button class="btn btn-sm" id="db-manage" aria-expanded="false">数据源管理</button>' : '') + '<button class="btn btn-sm" id="db-settings">工作台设置</button></div><span class="db-safe">只读会话 · 行数 / 时长 / 并发保护</span></header><div id="db-manager"></div><div id="db-workspace"></div></div>';
    q('db-source').onchange = function () {
      saveEditorSQL();
      const next = state.sources.find(s => s.id === this.value) || null;
      const cur = sess();
      if (cur && next) bindSessionSource(cur, next);
      state.source = next;
      if (next) { persisted.last_source = next.id; savePersisted(); }
      // #13：数据源属于当前页签。重建可见工作区但保留其它页签及其
      // 查询结果/运行状态，切换 Oracle/MySQL/Redis 时不会再复用错误 DOM。
      renderWorkspace(true);
    };
    q('db-test').onclick = testConnection;
    q('db-settings').onclick = openSettings;
    if (q('db-manage')) q('db-manage').onclick = function () {
      if (state.managing) { closeManager(); return; }
      state.managing = true;
      this.setAttribute('aria-expanded', 'true');
      renderManager();
    };
    renderWorkspace();
  }
  function field(label, id, value, type) {
    return '<label class="db-field"><span>' + h(label) + '</span><input id="' + id + '" type="' + (type || 'text') + '" value="' + h(value) + '"></label>';
  }
  function selectField(label, id, options, value) {
    return '<label class="db-field"><span>' + h(label) + '</span><select id="' + id + '">' + options.map(o => '<option value="' + o[0] + '"' + (o[0] === value ? ' selected' : '') + '>' + h(o[1]) + '</option>').join('') + '</select></label>';
  }
  function closeManager() {
    state.managing = false;
    if (q('db-manage')) q('db-manage').setAttribute('aria-expanded', 'false');
    renderManager();
  }
  function renderManager(edit) {
    const host = q('db-manager');
    if (!host) return;
    if (!state.managing) { host.innerHTML = ''; return; }
    const s = edit || { kind: 'oracle', port: 1521, oracle_connect_by: 'service_name', query_timeout_seconds: 30, max_rows: 1000, max_result_bytes: 16777216, max_open_connections: 4, max_idle_connections: 1, connection_max_minutes: 10, tls_mode: 'disabled' };
    host.innerHTML = '<section class="card db-manager"><div class="db-manager-head"><div><h3>' + (s.id ? '编辑数据源' : '新建数据源') + '</h3><div class="muted">连接信息保存在系统凭据库；保存后本面板自动收起。</div></div><select id="db-edit-existing"><option value="">＋ 新建</option>' + state.sources.map(x => '<option value="' + h(x.id) + '"' + (x.id === s.id ? ' selected' : '') + '>' + h(x.name) + '</option>').join('') + '</select></div><div class="db-form-grid">' + field('名称', 'dbf-name', s.name || '') + selectField('类型', 'dbf-kind', [['oracle', 'Oracle 11g+'], ['mysql', 'MySQL'], ['redis', 'Redis']], s.kind) + field('主机', 'dbf-host', s.host || '') + field('端口', 'dbf-port', s.port || '', 'number') + field('用户名', 'dbf-user', s.username || '') + field(s.has_password ? '密码（留空不改）' : '密码', 'dbf-pass', '', 'password') + '</div><div id="dbf-specific" class="db-form-specific"></div><div class="db-form-grid db-form-limits">' + selectField('TLS', 'dbf-tls', [['disabled', '关闭'], ['preferred', '优先（仅 MySQL）'], ['required', '必须且校验证书'], ['skip-verify', '必须但跳过校验']], s.tls_mode || 'disabled') + field('超时（秒）', 'dbf-timeout', s.query_timeout_seconds || 30, 'number') + field('最大行数', 'dbf-rows', s.max_rows || 1000, 'number') + field('最大连接', 'dbf-open', s.max_open_connections || 4, 'number') + field('空闲连接', 'dbf-idle', s.max_idle_connections == null ? 1 : s.max_idle_connections, 'number') + field('授权用户（逗号；*=全员）', 'dbf-users', (s.allowed_users || []).join(', ')) + '</div><div class="db-form-actions"><button class="btn btn-primary" id="dbf-save">保存并收起</button>' + (s.id ? '<button class="btn btn-danger" id="dbf-delete">删除</button>' : '') + '<button class="btn" id="dbf-close">取消</button><span class="hint">密码不会写入配置文件或返回页面。</span></div></section>';
    q('db-edit-existing').onchange = function () { renderManager(state.sources.find(x => x.id === this.value)); };
    q('dbf-kind').onchange = () => renderSpecific(s);
    q('dbf-save').onclick = () => saveSource(s);
    q('dbf-close').onclick = closeManager;
    if (q('dbf-delete')) q('dbf-delete').onclick = () => deleteSource(s);
    renderSpecific(s);
  }
  function renderSpecific(s) {
    const kind = q('dbf-kind').value, target = q('dbf-specific');
    if (kind === 'oracle') target.innerHTML = selectField('连接方式', 'dbf-oracle-by', [['service_name', 'Service Name'], ['sid', 'SID']], s.oracle_connect_by || 'service_name') + field('Service/SID', 'dbf-service', s.oracle_service || '') + field('客户端字符集', 'dbf-charset', s.oracle_client_charset || '');
    else if (kind === 'mysql') target.innerHTML = field('Database', 'dbf-database', s.database || '') + '<div class="db-field db-field-span-2"><span class="muted" style="margin-top:24px;font-size:12px;">MySQL 数据库名；留空将默认连接当前用户有权限的全部数据库。</span></div>';
      else {
      const mode = s.redis_mode || 'standalone';
      target.innerHTML = selectField('拓扑', 'dbf-redis-mode', [['standalone', '单机'], ['cluster', 'Cluster'], ['sentinel', 'Sentinel']], mode)
        + field('Redis DB', 'dbf-redis-db', s.redis_db || 0, 'number')
        + field('Master 名称', 'dbf-redis-master', s.redis_master_name || '')
        + '<label class="db-field db-field-span"><span>附加节点（每行 host:port）</span><textarea id="dbf-redis-nodes" rows="3" placeholder="127.0.0.1:6380">' + h((s.redis_nodes || []).join('\n')) + '</textarea></label>'
        + '<label class="db-field db-field-span db-redis-write-gate"><span><input id="dbf-redis-write" type="checkbox"' + (s.allow_redis_write ? ' checked' : '') + '> 开启受控 TTL 写操作（仅管理员，默认关闭）</span><small>只允许 EXPIRE / PEXPIRE / PERSIST，所有操作需要二次确认并写审计。</small></label>';
      const syncRedis = function () {
        const m = q('dbf-redis-mode').value;
        q('dbf-redis-master').closest('.db-field').hidden = m !== 'sentinel';
        q('dbf-redis-nodes').closest('.db-field').hidden = m === 'standalone';
        q('dbf-redis-db').disabled = m === 'cluster';
        if (m === 'cluster') q('dbf-redis-db').value = 0;
      };
      q('dbf-redis-mode').onchange = syncRedis;
      syncRedis();
    }
    const tls = q('dbf-tls');
    const tlsField = tls && tls.closest('.db-field');
    if (tlsField) tlsField.hidden = kind === 'oracle';
    if (kind === 'oracle' && tls) tls.value = 'disabled';
    const preferred = tls && tls.querySelector('option[value="preferred"]');
    if (preferred) preferred.disabled = kind !== 'mysql';
    if (preferred && preferred.disabled && tls.value === 'preferred') tls.value = 'required';
    const port = q('dbf-port');
    if (!port.value || ['1521', '3306', '6379'].includes(port.value)) port.value = kind === 'oracle' ? 1521 : kind === 'mysql' ? 3306 : 6379;
  }
  function sourceFromForm(old) {
    const kind = q('dbf-kind').value;
    const source = { name: q('dbf-name').value.trim(), kind, host: q('dbf-host').value.trim(), port: Number(q('dbf-port').value), username: q('dbf-user').value.trim(), tls_mode: q('dbf-tls').value, query_timeout_seconds: Number(q('dbf-timeout').value), max_rows: Number(q('dbf-rows').value), max_result_bytes: (old && old.max_result_bytes) || 16777216, max_open_connections: Number(q('dbf-open').value), max_idle_connections: Number(q('dbf-idle').value), connection_max_minutes: (old && old.connection_max_minutes) || 10, allowed_users: q('dbf-users').value.split(',').map(x => x.trim()).filter(Boolean) };
    if (kind === 'oracle') { source.oracle_connect_by = q('dbf-oracle-by').value; source.oracle_service = q('dbf-service').value.trim(); source.oracle_client_charset = q('dbf-charset').value.trim(); }
    if (kind === 'mysql') source.database = q('dbf-database').value.trim();
    if (kind === 'redis') {
      source.redis_mode = q('dbf-redis-mode').value;
      source.redis_db = Number(q('dbf-redis-db').value);
      source.redis_master_name = q('dbf-redis-master').value.trim();
      source.redis_nodes = q('dbf-redis-nodes').value.split(/[\n,]+/).map(function (x) { return x.trim(); }).filter(Boolean);
      source.allow_redis_write = !!(q('dbf-redis-write') && q('dbf-redis-write').checked);
    }
    return source;
  }
  async function saveSource(old) {
    const button = q('dbf-save'); button.disabled = true;
    try {
      const body = { source: sourceFromForm(old), password: q('dbf-pass').value };
      const method = old && old.id ? 'PUT' : 'POST';
      const path = '/api/database/sources' + (old && old.id ? '/' + encodeURIComponent(old.id) : '');
      const data = await api(method, path, body);
      await loadSources(data.source && data.source.id);
      refreshSourceSelect(); closeManager(); renderWorkspace(true);
      toast('数据源已保存，管理面板已收起', 'ok');
    } catch (e) { toast('保存失败：' + e.message, 'err'); button.disabled = false; }
  }
  async function deleteSource(source) {
    if (!source.id || !confirm('确定删除数据源“' + source.name + '”及其已保存密码？')) return;
    try {
      await api('DELETE', '/api/database/sources/' + encodeURIComponent(source.id));
      await loadSources(); refreshSourceSelect(); closeManager(); renderWorkspace(true);
      toast('数据源已删除', 'ok');
    } catch (e) { toast('删除失败：' + e.message, 'err'); }
  }
  function refreshSourceSelect() { const s = q('db-source'); if (s) s.innerHTML = sourceOptions(); }
  async function testConnection() {
    if (!state.source) return toast('请先创建数据源', 'warn');
    const b = q('db-test'); b.disabled = true; b.textContent = '连接中…';
    try {
      const r = await api('POST', '/api/database/sources/' + encodeURIComponent(state.source.id) + '/test', {});
      toast('连接成功 · ' + (r.topology && r.topology !== 'standalone' ? r.topology + (r.masters ? ' ×' + r.masters : '') + ' · ' : '') + (r.version || kindLabel(r.kind)) + ' · ' + r.latency_ms + ' ms', 'ok');
    } catch (e) { toast('连接失败：' + e.message, 'err'); }
    finally { b.disabled = false; b.textContent = '测试连接'; }
  }

  function renderWorkspace(preserveSessions) {
    const host = q('db-workspace'); if (!host) return;
    const keepSessions = preserveSessions === true;
    cancelGridPaint();
    gridIndexCache = { rows: null, len: -1, filter: '', sort: null, indexes: null };
    if (!keepSessions) {
      abortAllSessions();
      state.sessions = [];
      state.activeId = 0;
    }
    state.workspaceToken++;
    if (!state.source && !(sess() && sess().sourceId)) {
      q('db-source-badge').textContent = '未配置';
      host.innerHTML = '<div class="card empty-state"><div class="empty-title">尚无数据源</div><div class="empty-desc">打开“数据源管理”创建 Oracle、MySQL 或 Redis 连接。</div></div>';
      return;
    }
    if (!state.sessions.length) {
      const first = createSession('');
      state.sessions = [first];
      state.activeId = first.id;
    }
    const active = sess();
    if (active && !active.sourceId && state.source) bindSessionSource(active, state.source);
    const activeSource = effectiveSource();
    if (activeSource) state.source = activeSource;
    if (!activeSource && active && active.sourceId) {
      renderOrphanWorkspace(host, active);
      const sourceSelect = q('db-source'); if (sourceSelect) sourceSelect.value = '';
      const testButton = q('db-test'); if (testButton) { testButton.disabled = true; testButton.title = '当前页签绑定的数据源已删除'; }
      q('db-source-badge').textContent = '数据源已删除';
      return;
    }
    q('db-source-badge').textContent = state.source ? kindLabel(state.source.kind) : '未配置';
    if (keepSessions) {
      bindSession(active);
    } else {
      state.rows = []; state.columns = []; state.summary = null; state.lastError = null; state.plan = [];
      state.hiddenColumns = new Set(); state.sort = null; state.inspect = null; state.gridReady = false;
    }
    loadColumnWidths();
    state.source.kind === 'redis' ? renderRedis(host) : renderSQL(host);
    // 数据源切换或页签切换会重建工作区 DOM；把当前页签的编辑器、筛选器、
    // 结果视图和执行状态恢复回来，否则页签标题还在但 SQL 文本会变空。
    restoreSessionChrome(active);
  }

  function renderOrphanWorkspace(host, session) {
    host.innerHTML = '<section class="card db-orphan-workspace"><div class="empty-icon">⚠</div><h3>数据源已删除</h3><p>页签“' + h((session.sourceRef && session.sourceRef.name) || session.sourceId) + '”仍保留 SQL 和历史结果，但为避免误发到其他环境，当前已禁用执行、导出和元数据操作。</p><div class="db-orphan-actions"><label>重新绑定数据源<select id="db-orphan-source"><option value="">请选择数据源</option>' + sourceOptions() + '</select></label><button class="btn btn-primary" id="db-orphan-bind">绑定并继续</button></div><div class="db-orphan-sql"><label>当前 SQL</label><textarea id="db-sql" class="db-sql-editor mono" spellcheck="false"></textarea></div></section>';
    const ta = q('db-sql'); if (ta) { ta.value = session.sql || ''; ta.addEventListener('input', function () { session.sql = ta.value; }); }
    const select = q('db-orphan-source'); const button = q('db-orphan-bind');
    if (select && button) button.onclick = function () {
      const source = state.sources.find(function (x) { return x.id === select.value; });
      if (!source) return toast('请选择要绑定的数据源', 'warn');
      bindSessionSource(session, source); state.source = source; renderWorkspace(true); toast('已重新绑定：' + source.name, 'ok');
    };
  }

  function renderSQL(host) {
    const initial = state.source.kind === 'oracle' ? 'SELECT SYSDATE AS SERVER_TIME FROM DUAL' : 'SELECT NOW() AS server_time';
    const history = loadHistory();
    const savedRows = Math.max(1, Math.min(state.source.max_rows, Number(persisted.row_limits[state.source.id]) || Math.min(1000, state.source.max_rows)));
    const gridRows = Math.max(6, Math.min(40, Number(state.prefs.gridRows) || 16));
    host.innerHTML = '<div class="db-sql-layout"><aside class="card db-meta" id="db-meta-pane"><div class="db-pane-title"><span>数据库对象</span><button class="btn btn-xs" id="db-meta-refresh">刷新</button></div><label class="db-compact-label">Schema<select id="db-schema"><option>加载中…</option></select></label><input id="db-object-search" type="search" placeholder="搜索表、视图、函数、过程"><div id="db-objects" class="db-object-list"><div class="db-tree-loading">正在读取元数据…</div></div><div class="db-split-y" id="db-split-y" role="separator" title="上下拖动调整字段区高度"></div><div class="db-inspect"><div class="db-inspect-tabs"><button class="db-inspect-tab active" data-tab="fields">字段</button><button class="db-inspect-tab" data-tab="indexes">索引</button><button class="db-inspect-tab" data-tab="constraints">约束</button><button class="db-inspect-tab" data-tab="ddl">DDL</button></div><div id="db-inspect-body" class="db-field-list"><div class="hint">单击对象查看字段、索引、约束和 DDL；双击生成查询。</div></div></div></aside><div class="db-split-x" id="db-split-x" role="separator" title="左右拖动调整对象栏宽度"></div><main class="db-main"><section class="card db-editor-card"><div class="db-editor-tabs"><div id="db-sql-tabs" class="db-sql-tabs"></div><button type="button" class="btn btn-xs" id="db-tab-add" title="新建查询页签">＋ 页签</button><span class="db-dialect">' + kindLabel(state.source.kind) + '</span></div><div class="db-editor-bar"><button class="btn btn-primary" id="db-run">执行 <kbd>' + h(state.prefs.shortcuts.run) + '</kbd></button><button class="btn" id="db-explain">执行计划</button><button class="btn" id="db-format">格式化</button><button class="btn" id="db-cancel" disabled>取消</button><label>最多 <input id="db-max-rows" type="number" min="1" max="' + state.source.max_rows + '" value="' + savedRows + '"> 行</label><label>显示 <input id="db-grid-rows" type="number" min="6" max="40" value="' + gridRows + '"> 行</label><div class="db-editor-bar-right"><select id="db-export-format" title="导出格式"><option value="csv">CSV</option><option value="json">JSON</option><option value="xlsx">Excel</option><option value="insert">INSERT</option></select><button class="btn" id="db-export" disabled>导出</button><select id="db-bookmark" title="SQL 收藏夹"><option value="">收藏夹</option></select><button class="btn btn-xs" id="db-bookmark-save" title="把当前 SQL 存入收藏夹">收藏</button><button class="btn btn-xs" id="db-bookmark-del" title="删除当前收藏">删收藏</button><select id="db-history" title="查询历史"><option value="">历史</option>' + history.map(function (sql, i) { return '<option value="' + i + '">' + h(sql.replace(/\s+/g, ' ').slice(0, 80)) + '</option>'; }).join('') + '</select><button class="btn btn-xs" id="db-history-clear" title="查询历史按当前用户保存在 data/preferences.json">清空历史</button></div><span id="db-query-status" class="db-query-status">就绪</span></div><div class="db-sql-shell"><pre id="db-sql-highlight" class="db-sql-highlight" aria-hidden="true"></pre><textarea id="db-sql" class="db-sql-editor" spellcheck="false"></textarea><div id="db-sql-ac" class="db-sql-ac" hidden role="listbox" aria-label="SQL 补全"></div></div><div class="db-editor-help">多页签可并行查询 · 输入关键字弹出补全 · 光标停在括号上会匹配另一半 · 格式化 / 收藏夹 · 模板缩写按 ' + h(state.prefs.expandKey) + ' 展开 · ' + h(state.prefs.shortcuts.run) + ' 执行</div></section><section class="card db-results"><div class="db-result-toolbar"><div class="db-result-tabs"><button id="db-view-grid" class="db-view-btn active">网格</button><button id="db-view-record" class="db-view-btn">单行记录</button><button id="db-view-plan" class="db-view-btn">执行计划</button></div><input id="db-result-filter" type="search" placeholder="在当前结果中过滤"><button class="btn btn-xs" id="db-copy-columns" disabled>复制字段名</button><button class="btn btn-xs" id="db-column-manager" disabled>显示列</button><span class="db-copy-hint">单击选行 · 双击进单行 · 点字段名可复制</span><span id="db-result-meta">等待执行查询</span></div><div id="db-result-message" class="db-result-message" hidden></div><div id="db-result-grid" class="db-result-grid"></div></section></main></div>';
    q('db-sql').placeholder = initial;
    q('db-max-rows').value = savedRows;
    q('db-max-rows').title = '每页返回多少行，按数据源记住';
    q('db-grid-rows').title = '结果网格一次显示多少行，可自定义';
    installDatabasePagination();
    bindPaneSplitters(host);
    ensureWorkbenchKeys();
    q('db-run').onclick = runQuery;
    q('db-explain').onclick = runExplain;
    q('db-cancel').onclick = cancelQuery;
    q('db-export').onclick = exportResult;
    q('db-format').onclick = formatCurrentSQL;
    q('db-bookmark-save').onclick = saveBookmark;
    q('db-bookmark-del').onclick = deleteSelectedBookmark;
    q('db-bookmark').onchange = function () {
      const item = loadBookmarks()[Number(this.value)];
      if (item) { q('db-sql').value = item.sql; syncSQLEditor(); }
    };
    // #18 九宫格入口：动态插入，避免改动巨大 host.innerHTML 字符串
    (function () {
      const sel = q('db-bookmark');
      if (!sel) return;
      if (document.getElementById('db-bookmark-grid')) return;
      const gridBtn = document.createElement('button');
      gridBtn.type = 'button';
      gridBtn.id = 'db-bookmark-grid';
      gridBtn.className = 'btn btn-xs';
      gridBtn.title = '九宫格查看收藏（多条时更清晰）';
      gridBtn.textContent = '九宫格';
      gridBtn.onclick = openBookmarkGrid;
      sel.parentNode.insertBefore(gridBtn, sel.nextSibling);
    })();
    compactDatabaseToolbar();
    q('db-copy-columns').onclick = copyVisibleColumnNames;
    q('db-sql').onkeydown = handleEditorKeydown;
    bindSQLEditor();
    const active = sess();
    bindSession(active);
    renderTabs();
    q('db-tab-add').onclick = addSession;
    refreshBookmarkSelect();
    q('db-meta-refresh').onclick = () => loadSchemas(true);
    q('db-schema').onchange = loadObjects;
    q('db-object-search').oninput = debounce(loadObjects, 220);
    q('db-view-grid').onclick = () => setResultMode('grid');
    q('db-view-record').onclick = () => setResultMode('record');
    q('db-view-plan').onclick = () => setResultMode('plan');
    q('db-result-filter').oninput = debounce(function () {
      state.localFilter = this.value;
      const s = sess();
      if (s) s.localFilter = this.value;
      refreshVisibleResult();
    }, 120);
    q('db-column-manager').onclick = openColumnManager;
    q('db-max-rows').onchange = function () {
      const value = Math.max(1, Math.min(state.source.max_rows, Number(this.value) || 1));
      this.value = value;
      persisted.row_limits[state.source.id] = value;
      savePersisted();
    };
    q('db-grid-rows').onchange = function () {
      const value = Math.max(6, Math.min(40, Number(this.value) || 16));
      this.value = value;
      state.prefs.gridRows = value;
      savePrefs();
      applyGridHeight();
    };
    q('db-history').onchange = function () {
      const items = loadHistory();
      const sql = items[Number(this.value)];
      if (sql) { q('db-sql').value = sql; syncSQLEditor(); }
      this.value = '';
    };
    q('db-history-clear').onclick = function () {
      if (!persisted.history.length || confirm('清空当前用户保存的全部 SQL 查询历史？')) {
        persisted.history = [];
        savePersisted();
        refreshHistorySelect();
        toast('查询历史已清空', 'ok');
      }
    };
    host.querySelectorAll('.db-inspect-tab').forEach(btn => {
      btn.onclick = function () {
        state.inspectTab = this.dataset.tab;
        host.querySelectorAll('.db-inspect-tab').forEach(x => x.classList.toggle('active', x === this));
        renderInspect();
      };
    });
    loadSchemas();
  }

  async function loadSchemas(refresh) {
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/metadata/schemas?source_id=' + encodeURIComponent(source.id) + (refresh === true ? '&refresh=1' : ''));
      const schema = q('db-schema');
      if (token !== state.workspaceToken || !schema) return;
      schema.innerHTML = (data.schemas || []).map(x => '<option>' + h(x.name) + '</option>').join('');
      if (source.kind === 'mysql' && source.database && Array.from(schema.options).some(x => x.value === source.database)) schema.value = source.database;
      if (source.kind === 'oracle') {
        const preferred = (source.username || '').toUpperCase();
        if (preferred && Array.from(schema.options).some(x => x.value === preferred)) schema.value = preferred;
      }
      loadObjects();
    } catch (e) {
      const o = q('db-objects'), schema = q('db-schema');
      if (token === state.workspaceToken && schema) schema.innerHTML = '<option value="">加载失败</option>';
      if (token === state.workspaceToken && o) {
        o.innerHTML = inlineError('元数据加载失败', e.message) + '<button class="btn btn-xs db-meta-retry" id="db-meta-retry">重试</button>';
        q('db-meta-retry').onclick = () => loadSchemas(true);
      }
    }
  }

  async function loadObjects() {
    const token = state.workspaceToken, source = state.source, schema = q('db-schema') && q('db-schema').value, objects = q('db-objects');
    if (!schema) return;
    if (objects) objects.innerHTML = '<div class="db-tree-loading">正在读取对象…</div>';
    try {
      const search = q('db-object-search');
      const data = await api('GET', '/api/database/metadata/objects?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&search=' + encodeURIComponent(search ? search.value : ''));
      if (token !== state.workspaceToken || !objects) return;
      const groups = [['tables', '表'], ['views', '视图'], ['functions', '函数'], ['procedures', '存储过程 / 包'], ['triggers', '触发器'], ['other', '其他对象']];
      const by = {};
      (data.objects || []).forEach(x => (by[x.category || 'other'] = by[x.category || 'other'] || []).push(x));
      objects.innerHTML = groups.filter(g => by[g[0]] && by[g[0]].length).map((g, i) => '<details class="db-tree-group"' + (i < 4 ? ' open' : '') + '><summary><span>' + h(g[1]) + '</span><small>' + by[g[0]].length + '</small></summary><div>' + by[g[0]].map(x => '<button class="db-object" data-name="' + h(x.name) + '" data-type="' + h(x.type) + '"><span class="db-object-icon">' + h(String(x.type || '?').slice(0, 1)) + '</span><span class="db-object-name">' + h(x.name) + '</span><small>' + h(x.type) + '</small></button>').join('') + '</div></details>').join('') || '<div class="hint db-tree-empty">没有匹配对象</div>';
      objects.querySelectorAll('.db-object').forEach(btn => {
        btn.onclick = () => {
          objects.querySelectorAll('.db-object.active').forEach(x => x.classList.remove('active'));
          btn.classList.add('active');
          loadInspect(schema, btn.dataset.name, btn.dataset.type);
        };
        btn.ondblclick = () => insertObjectSQL(schema, btn.dataset.name, btn.dataset.type);
      });
    } catch (e) {
      if (token === state.workspaceToken && objects) objects.innerHTML = inlineError('对象加载失败', e.message);
    }
  }

  async function loadInspect(schema, object, type) {
    const token = state.workspaceToken, source = state.source, body = q('db-inspect-body');
    if (body) body.innerHTML = '<div class="db-selected-object">' + h(object) + '<small>' + h(type) + '</small></div><div class="db-tree-loading">正在读取详情…</div>';
    try {
      const data = await api('GET', '/api/database/metadata/inspect?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&object=' + encodeURIComponent(object) + '&type=' + encodeURIComponent(type || ''));
      if (token !== state.workspaceToken) return;
      state.inspect = Object.assign({ object: object, type: type, schema: schema }, data.inspect || {});
      renderInspect();
    } catch (e) {
      if (token === state.workspaceToken && body) body.innerHTML = inlineError('对象详情加载失败', e.message);
    }
  }

  function renderInspect() {
    const body = q('db-inspect-body');
    if (!body) return;
    const info = state.inspect;
    if (!info) { body.innerHTML = '<div class="hint">单击对象查看字段、索引、约束和 DDL；双击生成查询。</div>'; return; }
    const tab = state.inspectTab;
    let html = '<div class="db-selected-object"><span>' + h(info.object) + '</span><button class="btn btn-xs" id="db-copy-field-list">复制字段</button></div>';
    if (tab === 'indexes') {
      html += (info.indexes || []).length ? info.indexes.map(x => '<div class="db-field-row"><span>' + h(x.name) + (x.uniqueness === 'UNIQUE' ? ' <b>U</b>' : '') + '</span><small>' + h((x.columns || []).join(', ')) + '</small></div>').join('') : '<div class="hint">没有索引</div>';
    } else if (tab === 'constraints') {
      html += (info.constraints || []).length ? info.constraints.map(x => '<div class="db-field-row"><span>' + h(x.name) + '</span><small>' + h(x.type) + (x.columns ? ' · ' + x.columns : '') + '</small></div>').join('') : '<div class="hint">没有约束</div>';
    } else if (tab === 'ddl') {
      html += '<pre class="db-ddl">' + h(info.ddl || info.source_text || info.ddl_error || '没有 DDL') + '</pre>';
    } else {
      const fields = info.fields || [];
      html += fields.map(x => '<button class="db-field-row" data-field="' + h(x.name) + '"><span>' + h(x.name) + (x.primary_key ? ' <b title="主键">PK</b>' : '') + (x.nullable ? '' : ' <b title="非空">•</b>') + '</span><small>' + h(x.definition || x.data_type) + '</small></button>').join('') || '<div class="hint">没有字段</div>';
    }
    body.innerHTML = html;
    const names = (info.fields || []).map(x => x.name);
    const copy = q('db-copy-field-list');
    if (copy) copy.onclick = () => copyDBText(names.join(', '), '已复制 ' + names.length + ' 个字段名');
    body.querySelectorAll('.db-field-row[data-field]').forEach(btn => {
      btn.ondblclick = () => insertAtCursor(q('db-sql'), quoteIdentifier(btn.dataset.field));
    });
  }

  function quoteIdentifier(v) {
    const src = effectiveSource();
    const quote = src && src.kind === 'mysql' ? '`' : '"';
    return quote + String(v).split(quote).join(quote + quote) + quote;
  }
  function insertObjectSQL(schema, object, type) {
    const name = quoteIdentifier(schema) + '.' + quoteIdentifier(object);
    const upper = String(type).toUpperCase();
    const srcKind = effectiveSource() ? effectiveSource().kind : (state.source ? state.source.kind : 'oracle');
    let sql = 'SELECT *\nFROM ' + name;
    if (upper === 'FUNCTION') sql = srcKind === 'oracle' ? 'SELECT ' + name + '() AS result FROM DUAL' : 'SELECT ' + name + '() AS result';
    else if (upper === 'PROCEDURE' || upper === 'PACKAGE') sql = '-- 只读工作台不执行存储过程/Package。\n-- 请在右侧“DDL”查看定义；以下仅为人工执行模板，需在受控客户端确认后使用：\n-- ' + (srcKind === 'oracle' ? 'BEGIN ' + name + '(); END;' : 'CALL ' + name + '();');
    else if (upper === 'TRIGGER') sql = '-- Trigger: ' + name;
    const e = q('db-sql'); e.value = sql; e.focus(); syncSQLEditor();
  }
  function inlineError(title, message) {
    return '<div class="db-inline-error"><strong>' + h(title) + '</strong><span>' + h(message) + '</span></div>';
  }

  function bindPaneSplitters(host) {
    const meta = q('db-meta-pane');
    const inspect = host.querySelector('.db-inspect');
    if (meta) meta.style.width = persisted.meta_width + 'px';
    if (inspect) inspect.style.flexBasis = persisted.inspect_height + 'px';
    const splitX = q('db-split-x'), splitY = q('db-split-y');
    if (splitX) {
      splitX.onpointerdown = function (e) {
        e.preventDefault();
        const startX = e.clientX, startW = persisted.meta_width;
        const move = function (ev) {
          persisted.meta_width = Math.max(META_WIDTH_MIN, Math.min(META_WIDTH_MAX, startW + ev.clientX - startX));
          if (meta) meta.style.width = persisted.meta_width + 'px';
        };
        const up = function () { document.removeEventListener('pointermove', move); document.removeEventListener('pointerup', up); savePersisted(); };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      };
    }
    if (splitY) {
      splitY.onpointerdown = function (e) {
        e.preventDefault();
        const startY = e.clientY, startH = persisted.inspect_height;
        const move = function (ev) {
          persisted.inspect_height = Math.max(140, Math.min(480, startH + (startY - ev.clientY)));
          if (inspect) inspect.style.flexBasis = persisted.inspect_height + 'px';
        };
        const up = function () { document.removeEventListener('pointermove', move); document.removeEventListener('pointerup', up); savePersisted(); };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      };
    }
    applyGridHeight();
  }
  function applyGridHeight() {
    const rows = Math.max(6, Math.min(40, Number(q('db-grid-rows') && q('db-grid-rows').value) || state.prefs.gridRows || 16));
    const scroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-scroll');
    const plan = q('db-result-grid') && q('db-result-grid').querySelector('.db-plan-text, .db-plan-table-wrap');
    const height = (rows * GRID_ROW_H + GRID_HEAD_H) + 'px';
    if (scroll) scroll.style.height = height;
    if (plan) plan.style.maxHeight = height;
    if (scroll && state.gridReady && state.resultMode === 'grid') paintGridRows(scroll, false);
  }
  let workbenchKeysBound = false;
  function ensureWorkbenchKeys() {
    if (workbenchKeysBound) return;
    workbenchKeysBound = true;
    document.addEventListener('keydown', onWorkbenchKey, true);
  }
  function onWorkbenchKey(e) {
    if ((location.hash || '').indexOf('database') < 0) return;
    if (!q('db-sql') && !q('db-workspace')) return;
    if (document.body.classList.contains('has-open-overlay')) return;
    const id = e.target && e.target.id;
    if (id && String(id).indexOf('db-shortcut-') === 0) return;
    if (matchesShortcut(e, state.prefs.shortcuts.run)) { e.preventDefault(); runQuery(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.explain)) { e.preventDefault(); runExplain(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.cancel) && sess() && sess().controller) { e.preventDefault(); cancelQuery(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.grid)) { e.preventDefault(); setResultMode('grid'); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.record)) { e.preventDefault(); setResultMode('record'); return; }
  }

  function handleEditorKeydown(e) {
    if (acState.open) {
      if (e.key === 'ArrowDown') { e.preventDefault(); moveComplete(1); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); moveComplete(-1); return; }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); acceptComplete(); return; }
      if (e.key === 'Escape') { e.preventDefault(); hideComplete(); return; }
    }
    if (matchesShortcut(e, state.prefs.shortcuts.run) || matchesShortcut(e, state.prefs.shortcuts.explain) || matchesShortcut(e, state.prefs.shortcuts.cancel) || matchesShortcut(e, state.prefs.shortcuts.grid) || matchesShortcut(e, state.prefs.shortcuts.record)) {
      e.preventDefault();
      return;
    }
    const trigger = state.prefs.expandKey;
    if ((trigger === 'Space' && e.key === ' ') || (trigger === 'Tab' && e.key === 'Tab') || (trigger === 'Enter' && e.key === 'Enter')) {
      if (expandSnippet(e.currentTarget)) e.preventDefault();
    }
  }
  function matchesShortcut(e, spec) {
    if (!spec) return false;
    const parts = String(spec).toLowerCase().replace(/\s+/g, '').split('+').filter(Boolean);
    let wantCtrl = false, wantAlt = false, wantShift = false, wantMeta = false, key = '';
    parts.forEach(function (part) {
      if (part === 'ctrl' || part === 'control' || part === 'controlleft') wantCtrl = true;
      else if (part === 'alt') wantAlt = true;
      else if (part === 'shift') wantShift = true;
      else if (part === 'meta' || part === 'cmd' || part === 'command') wantMeta = true;
      else key = part;
    });
    const actual = String(e.key || '').toLowerCase(), code = String(e.code || '').toLowerCase();
    const keyOk = actual === key
      || (key === 'enter' && (actual === 'enter' || code === 'enter' || code === 'numpadenter'))
      || (key === 'esc' && actual === 'escape')
      || (key === 'escape' && actual === 'escape')
      || (key === 'space' && (actual === ' ' || actual === 'space' || code === 'space'));
    return keyOk && e.ctrlKey === wantCtrl && e.altKey === wantAlt && e.shiftKey === wantShift && e.metaKey === wantMeta;
  }
  function expandSnippet(editor) {
    const pos = editor.selectionStart, before = editor.value.slice(0, pos), match = before.match(/([A-Za-z0-9_.-]+)$/);
    if (!match) return false;
    const snippet = state.prefs.snippets.find(x => x.enabled !== false && x.key === match[1]);
    if (!snippet) return false;
    const schema = q('db-schema') ? q('db-schema').value : '', start = pos - match[1].length;
    let replacement = String(snippet.text).split('${schema}').join(schema || '').split('${table}').join('table_name');
    const cursor = replacement.indexOf('${cursor}');
    replacement = replacement.split('${cursor}').join('');
    editor.setRangeText(replacement, start, pos, 'end');
    if (cursor >= 0) editor.setSelectionRange(start + cursor, start + cursor);
    syncSQLEditor();
    return true;
  }
  function insertAtCursor(editor, text) { editor.setRangeText(text, editor.selectionStart, editor.selectionEnd, 'end'); editor.focus(); syncSQLEditor(); }
  const SQL_KEYWORDS = new Set(('select from where group by having order union all distinct as join left right inner outer full cross on using with values insert update delete create alter drop table view index into set limit offset fetch first next only rows rownum dual sysdate now case when then else end and or not in exists like between is null true false count sum avg min max cast nvl nvl2 decode coalesce ifnull substr substring instr length trim to_char to_date to_number varchar varchar2 number integer int date timestamp char clob blob begin procedure function package trigger connect start prior minus except intersect over partition desc asc inner show describe explain desc').split(/\s+/));
  const SQL_NEWLINE_BEFORE = new Set(['select', 'from', 'where', 'group', 'having', 'order', 'union', 'minus', 'except', 'intersect', 'join', 'left', 'right', 'inner', 'full', 'cross', 'outer', 'on', 'with', 'limit', 'offset', 'fetch', 'connect', 'start']);
  function tokenizeSQL(src) {
    const tokens = [];
    let i = 0;
    const text = String(src || '');
    while (i < text.length) {
      const c = text[i], next = text[i + 1];
      if (c === '-' && next === '-') {
        const end = text.indexOf('\n', i);
        const take = end < 0 ? text.length : end;
        tokens.push({ type: 'comment', value: text.slice(i, take), start: i });
        i = take;
        continue;
      }
      if (c === '/' && next === '*') {
        const end = text.indexOf('*/', i + 2);
        const take = end < 0 ? text.length : end + 2;
        tokens.push({ type: 'comment', value: text.slice(i, take), start: i });
        i = take;
        continue;
      }
      if (c === '\'' || c === '"' || c === '`') {
        let j = i + 1, out = c;
        while (j < text.length) {
          out += text[j];
          if (text[j] === c) {
            if (text[j + 1] === c) { out += text[j + 1]; j += 2; continue; }
            j++; break;
          }
          j++;
        }
        tokens.push({ type: 'string', value: out, start: i });
        i = j;
        continue;
      }
      if (c <= ' ') {
        let j = i + 1;
        while (j < text.length && text[j] <= ' ') j++;
        tokens.push({ type: 'space', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      if ((c >= '0' && c <= '9') || (c === '.' && next >= '0' && next <= '9')) {
        let j = i + 1;
        while (j < text.length && /[0-9.eE+-]/.test(text[j])) j++;
        tokens.push({ type: 'number', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      if (/[A-Za-z_$#]/.test(c) || c.charCodeAt(0) > 127) {
        let j = i + 1;
        while (j < text.length && /[A-Za-z0-9_$#]/.test(text[j])) j++;
        const value = text.slice(i, j);
        tokens.push({ type: SQL_KEYWORDS.has(value.toLowerCase()) ? 'kw' : 'ident', value: value, start: i });
        i = j;
        continue;
      }
      if (c === '<' || c === '>' || c === '!' || c === '=') {
        let j = i + 1;
        if (c === '<' && next === '>') j++;
        else if (next === '=') j++;
        tokens.push({ type: 'punct', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      tokens.push({ type: 'punct', value: c, start: i });
      i++;
    }
    return tokens;
  }
  function highlightSQL(src, match) {
    return tokenizeSQL(src).map(function (tok) {
      const body = h(tok.value);
      let cls = '';
      if (tok.type === 'kw') cls = 'db-sql-kw';
      else if (tok.type === 'string') cls = 'db-sql-str';
      else if (tok.type === 'comment') cls = 'db-sql-cmt';
      else if (tok.type === 'number') cls = 'db-sql-num';
      if (tok.type === 'punct' && match && (tok.start === match.open || tok.start === match.close)) {
        cls = (match.open < 0 || match.close < 0) ? 'db-sql-br-bad' : 'db-sql-br';
      }
      return cls ? '<span class="' + cls + '">' + body + '</span>' : body;
    }).join('') || ' ';
  }
  function completionPrefix(text, cursor) {
    const tokens = tokenizeSQL(text);
    for (let i = 0; i < tokens.length; i++) {
      const tok = tokens[i], end = tok.start + tok.value.length;
      if (cursor > tok.start && cursor <= end && (tok.type === 'string' || tok.type === 'comment')) return null;
    }
    const before = String(text || '').slice(0, cursor);
    const m = before.match(/[A-Za-z_$#][A-Za-z0-9_$#]*$/);
    if (!m) return null;
    return { prefix: m[0], start: cursor - m[0].length, end: cursor };
  }
  function suggestSQL(text, cursor, extras) {
    extras = extras || {};
    const ctx = completionPrefix(text, cursor);
    if (!ctx || ctx.prefix.length < 2) return { items: [], start: cursor, end: cursor };
    const needle = ctx.prefix.toLowerCase();
    const seen = new Set();
    const items = [];
    const add = function (label, kind, insert) {
      const key = String(label || '').toLowerCase();
      if (!key || seen.has(key) || key.indexOf(needle) !== 0) return;
      seen.add(key);
      items.push({ label: label, kind: kind, insert: insert || label });
    };
    (extras.snippets || []).forEach(function (s) {
      if (s && s.enabled !== false && s.key && String(s.key).toLowerCase() === needle) add(s.key, 'snippet', s.text);
    });
    SQL_KEYWORDS.forEach(function (k) { add(k.toUpperCase(), 'keyword'); });
    (extras.objects || []).forEach(function (n) { add(n, 'object'); });
    (extras.fields || []).forEach(function (n) { add(n, 'field'); });
    items.sort(function (a, b) {
      const rank = { snippet: 0, keyword: 1, object: 2, field: 3 };
      return (rank[a.kind] - rank[b.kind]) || a.label.localeCompare(b.label);
    });
    return { items: items.slice(0, 12), start: ctx.start, end: ctx.end };
  }
  function matchBrackets(text, cursor) {
    const tokens = tokenizeSQL(text);
    const brackets = [];
    tokens.forEach(function (tok) {
      if (tok.type === 'punct' && '()[]{}'.indexOf(tok.value) >= 0) brackets.push({ ch: tok.value, start: tok.start });
    });
    let idx = -1;
    for (let i = 0; i < brackets.length; i++) {
      const b = brackets[i];
      if (cursor === b.start || cursor === b.start + 1) { idx = i; break; }
    }
    if (idx < 0) return null;
    const pairs = { '(': ')', '[': ']', '{': '}' };
    const opens = { ')': '(', ']': '[', '}': '{' };
    const ch = brackets[idx].ch;
    if (pairs[ch]) {
      let depth = 0;
      for (let i = idx; i < brackets.length; i++) {
        if (brackets[i].ch === ch) depth++;
        else if (brackets[i].ch === pairs[ch]) {
          depth--;
          if (depth === 0) return { open: brackets[idx].start, close: brackets[i].start };
        }
      }
      return { open: brackets[idx].start, close: -1 };
    }
    if (opens[ch]) {
      let depth = 0;
      for (let i = idx; i >= 0; i--) {
        if (brackets[i].ch === ch) depth++;
        else if (brackets[i].ch === opens[ch]) {
          depth--;
          if (depth === 0) return { open: brackets[i].start, close: brackets[idx].start };
        }
      }
      return { open: -1, close: brackets[idx].start };
    }
    return null;
  }
  function formatSQL(src) {
    const tokens = tokenizeSQL(src);
    let out = '', newline = true;
    tokens.forEach(function (tok) {
      if (tok.type === 'space') return;
      if (tok.type === 'comment') {
        if (!newline && !out.endsWith('\n')) out += ' ';
        out += tok.value;
        if (tok.value.indexOf('\n') >= 0 || tok.value.slice(0, 2) === '--') { out += '\n'; newline = true; }
        else newline = false;
        return;
      }
      const lower = tok.value.toLowerCase();
      const breakBefore = tok.type === 'kw' && SQL_NEWLINE_BEFORE.has(lower) && out && !newline;
      if (breakBefore) { out = out.replace(/[ \t]+$/, ''); out += '\n'; newline = true; }
      if (tok.type === 'punct' && tok.value === ',') {
        out += ',\n  ';
        newline = true;
        return;
      }
      // 修复 #9：* 与运算符前后空格。原先只有 = 等四个算子会加空格，导致 "SELECT*" 粘连；
      // 扩大到所有常见算子，且 * 需空格，括号/逗号等仍不加前空格，且 '(' 后不加空格
      const spacedOp = tok.type === 'punct' && /^[=<>!+\-*\/%]+$/.test(tok.value);
      const noSpaceBefore = tok.type === 'punct' && /^[),.;]$/.test(tok.value);
      if (!newline && !noSpaceBefore && !out.endsWith('(') && !out.endsWith('[') && !out.endsWith('.') && (tok.type !== 'punct' || spacedOp)) out += ' ';
      out += tok.type === 'kw' ? tok.value.toUpperCase() : tok.value;
      newline = tok.value === ';';
      if (tok.value === ';') { out += '\n'; newline = true; }
    });
    return out.replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim();
  }
  function bindSQLEditor() {
    const ta = q('db-sql'), hl = q('db-sql-highlight');
    if (!ta || !hl) return;
    const sync = function () {
      const match = matchBrackets(ta.value, ta.selectionStart);
      hl.innerHTML = highlightSQL(ta.value, match) + '\n';
      hl.scrollTop = ta.scrollTop;
      hl.scrollLeft = ta.scrollLeft;
    };
    ta.addEventListener('input', function () {
      sync();
      updateComplete(ta);
      const s = sess();
      if (s) s.sql = ta.value;
    });
    ta.addEventListener('scroll', function () { hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft; });
    ta.addEventListener('keyup', sync);
    ta.addEventListener('click', sync);
    ta.addEventListener('select', sync);
    ta.addEventListener('blur', function () { setTimeout(hideComplete, 120); });
    ta._syncHighlight = sync;
    sync();
  }
  function completionExtras() {
    const objects = [];
    document.querySelectorAll('#db-objects .db-object-name').forEach(function (el) { objects.push(el.textContent); });
    const fields = ((state.inspect && state.inspect.fields) || []).map(function (f) { return f.name; });
    return { objects: objects, fields: fields, snippets: state.prefs.snippets || [] };
  }
  function hideComplete() {
    acState.open = false; acState.items = []; acState.index = 0;
    const box = q('db-sql-ac');
    if (box) { box.hidden = true; box.innerHTML = ''; }
  }
  function moveComplete(delta) {
    if (!acState.items.length) return;
    acState.index = (acState.index + delta + acState.items.length) % acState.items.length;
    renderComplete();
  }
  function acceptComplete() {
    const ta = q('db-sql');
    const item = acState.items[acState.index];
    if (!ta || !item) { hideComplete(); return; }
    const insert = String(item.insert || item.label);
    ta.setRangeText(insert, acState.start, acState.end, 'end');
    hideComplete();
    syncSQLEditor();
    const s = sess();
    if (s) s.sql = ta.value;
  }
  function updateComplete(ta) {
    const found = suggestSQL(ta.value, ta.selectionStart, completionExtras());
    if (!found.items.length) { hideComplete(); return; }
    acState.open = true;
    acState.items = found.items;
    acState.start = found.start;
    acState.end = found.end;
    acState.index = 0;
    renderComplete();
    positionComplete(ta, found.start);
  }
  function renderComplete() {
    const box = q('db-sql-ac');
    if (!box) return;
    box.hidden = false;
    box.innerHTML = acState.items.map(function (item, i) {
      return '<button type="button" class="db-sql-ac-item' + (i === acState.index ? ' active' : '') + '" data-ac="' + i + '" role="option"><span>' + h(item.label) + '</span><small>' + h({ keyword: '关键字', snippet: '模板', object: '对象', field: '字段' }[item.kind] || item.kind) + '</small></button>';
    }).join('');
    box.querySelectorAll('[data-ac]').forEach(function (btn) {
      btn.onmousedown = function (e) { e.preventDefault(); acState.index = Number(btn.dataset.ac); acceptComplete(); };
    });
  }
  function positionComplete(ta, start) {
    const box = q('db-sql-ac');
    if (!box) return;
    const style = window.getComputedStyle(ta);
    const lineH = parseFloat(style.lineHeight) || 21;
    const padT = parseFloat(style.paddingTop) || 13;
    const padL = parseFloat(style.paddingLeft) || 15;
    const before = ta.value.slice(0, start).split('\n');
    const row = before.length - 1;
    const col = before[before.length - 1].length;
    const canvas = positionComplete.canvas || (positionComplete.canvas = document.createElement('canvas'));
    const ctx = canvas.getContext('2d');
    ctx.font = style.font;
    const left = padL + ctx.measureText(before[before.length - 1]).width - ta.scrollLeft;
    const top = padT + (row + 1) * lineH - ta.scrollTop;
    box.style.left = Math.max(8, Math.min(left, ta.clientWidth - 220)) + 'px';
    box.style.top = Math.max(8, top) + 'px';
  }
  function syncSQLEditor() {
    const ta = q('db-sql');
    if (ta && ta._syncHighlight) ta._syncHighlight();
  }
  function formatCurrentSQL() {
    const ta = q('db-sql');
    if (!ta) return;
    const next = formatSQL(ta.value);
    if (!next) return toast('编辑器为空', 'warn');
    ta.value = next;
    syncSQLEditor();
    toast('SQL 已格式化', 'ok');
  }
  function normalizeBookmarks(list) {
    if (!Array.isArray(list)) return [];
    return list.filter(function (x) {
      return x && typeof x === 'object' && typeof x.name === 'string' && typeof x.sql === 'string' && x.sql.length <= BOOKMARK_SQL_MAX;
    }).slice(0, BOOKMARK_LIMIT);
  }
  function loadBookmarks() { return normalizeBookmarks(persisted.bookmarks); }
  function refreshBookmarkSelect() {
    const select = q('db-bookmark');
    if (!select) return;
    const items = loadBookmarks(), current = select.value;
    select.innerHTML = '<option value="">收藏夹</option>' + items.map(function (item, i) {
      return '<option value="' + i + '">' + h(item.name) + '</option>';
    }).join('');
    select.value = current && items[Number(current)] ? current : '';
  }
  async function saveBookmark() {
    const sql = q('db-sql') && q('db-sql').value.trim();
    if (!sql) return toast('先输入要收藏的 SQL', 'warn');
    if (sql.length > BOOKMARK_SQL_MAX) return toast('SQL 超过收藏上限', 'warn');
    const fallback = sql.replace(/\s+/g, ' ').slice(0, 40);
    const name = Kairo.overlays && Kairo.overlays.prompt
      ? await Kairo.overlays.prompt({ title: '收藏 SQL 查询', label: '请输入收藏名称：', defaultValue: fallback })
      : (window.prompt('收藏名称', fallback) || '').trim();
    if (!name) return;
    const eff = effectiveSource();
    const items = loadBookmarks().filter(function (x) { return x.name !== name; });
    items.unshift({ name: name.slice(0, 80), sql: sql, source_id: eff && eff.id || '', updated_at: new Date().toISOString() });
    persisted.bookmarks = items.slice(0, BOOKMARK_LIMIT);
    savePersisted();
    refreshBookmarkSelect();
    q('db-bookmark').value = '0';
    toast('已收藏 “' + name.slice(0, 40) + '”', 'ok');
  }
  function deleteSelectedBookmark() {
    const select = q('db-bookmark');
    if (!select || select.value === '') return toast('先在收藏夹里选一条', 'warn');
    const items = loadBookmarks();
    const removed = items.splice(Number(select.value), 1)[0];
    persisted.bookmarks = items;
    savePersisted();
    refreshBookmarkSelect();
    toast(removed ? '已删除收藏 “' + removed.name + '”' : '收藏已删除', 'ok');
  }
  function openBookmarkGrid() {
    const items = loadBookmarks();
    if (!items.length) return toast('收藏夹为空，先收藏几条 SQL', 'warn');
    const grid = el('div', { class: 'db-bookmark-grid' });
    const body = el('div', { class: 'db-bookmark-modal-body' }, [grid]);
    const footer = el('div', { class: 'db-bookmark-modal-actions' });
    const dlg = Kairo.overlays.modal({
      title: '收藏夹 · 九宫格（点击载入，右键删除）',
      width: 720,
      body: body,
      footer: footer
    });
    footer.appendChild(el('button', { class: 'btn', type: 'button', text: '关闭', onclick: function () { dlg.close(); } }));
    items.forEach(function (item) {
      const card = el('div', { class: 'db-bookmark-card', tabindex: '0', role: 'button', title: '点击载入，右键删除' });
      card.appendChild(el('strong', { text: item.name }));
      card.appendChild(el('pre', { text: item.sql.slice(0, 200) }));
      const meta = el('small', { text: (item.source_id ? (state.sources.find(function (s) { return s.id === item.source_id; }) || {}).name || item.source_id : '通用') + ' · ' + (item.updated_at ? new Date(item.updated_at).toLocaleDateString() : '') });
      card.appendChild(meta);
      card.onclick = function () {
        // 同步源至收藏的源（若存在）
        if (item.source_id) {
          const src = state.sources.find(function (s) { return s.id === item.source_id; });
          if (src) {
            const cur = sess();
            if (cur) cur.sourceId = src.id;
            state.source = src;
            const sel = q('db-source');
            if (sel) sel.value = src.id;
            renderWorkspace(true);
          }
        }
        const sql = q('db-sql');
        if (sql) { sql.value = item.sql; syncSQLEditor(); }
        dlg.close();
        toast('已载入收藏 “' + item.name + '”', 'ok');
      };
      card.onkeydown = function (e) { if (e.key === 'Enter') card.onclick(); };
      card.oncontextmenu = function (e) {
        e.preventDefault();
        if (confirm('删除收藏 “' + item.name + '”？')) {
          const cur = loadBookmarks();
          const index = cur.findIndex(function (x) { return x.name === item.name && x.sql === item.sql; });
          if (index >= 0) cur.splice(index, 1);
          persisted.bookmarks = cur;
          savePersisted();
          refreshBookmarkSelect();
          card.remove();
          if (!cur.length) dlg.close();
          toast('已删除', 'ok');
        }
      };
      grid.appendChild(card);
    });
  }

  function compactDatabaseToolbar() {
    const bar = q('db-editor-bar'), right = bar && bar.querySelector('.db-editor-bar-right');
    if (!bar || !right || q('db-toolbar-more')) return;
    const details = document.createElement('details'); details.id = 'db-toolbar-more'; details.className = 'db-toolbar-more';
    const summary = document.createElement('summary'); summary.textContent = '更多';
    const panel = document.createElement('div'); panel.className = 'db-toolbar-more-panel';
    const ids = ['db-explain', 'db-format', 'db-export-format', 'db-export', 'db-bookmark', 'db-bookmark-grid', 'db-bookmark-save', 'db-bookmark-del', 'db-history', 'db-history-clear'];
    ids.forEach(function (id) { const node = q(id); if (node) panel.appendChild(node); });
    details.append(summary, panel);
    right.remove(); bar.insertBefore(details, q('db-query-status'));
    const max = q('db-max-rows'); if (max && max.parentNode && max.parentNode.firstChild && max.parentNode.firstChild.nodeType === 3) max.parentNode.firstChild.textContent = '每页 ';
  }
  function editorSQL() {
    const box = q('db-sql');
    if (!box) return '';
    let sql = box.value.trim();
    if (sql && Kairo.workbench && Kairo.workbench.statementModel) {
      const picked = Kairo.workbench.statementModel.normalize(box.value, box.selectionStart, box.selectionStart, box.selectionEnd);
      sql = picked.text;
    }
    if (sql) return sql;
    const sample = String(box.placeholder || '').trim();
    if (!sample) return '';
    box.value = sample;
    syncSQLEditor();
    return sample;
  }

  async function runQuery() {
    const s = sess();
    if (!s) return;
    if (s.controller) {
      toast('当前页签查询还在执行，请先取消或新开页签', 'warn');
      return;
    }
    const sql = editorSQL();
    if (!sql) return showQueryError('SQL 为空', '请输入只读查询后再执行。');
    const maxRows = Number(q('db-max-rows').value);
    s.sql = q('db-sql') ? q('db-sql').value : sql;
    s.lastSQL = sql;
    s.lastMaxRows = maxRows;
    s.pageSize = maxRows;
    s.page = Math.max(1, Number(s.page) || 1);
    s.rows = [];
    s.columns = [];
    s.summary = null;
    s.lastError = null;
    s.sort = null;
    s.hiddenColumns = new Set();
    s.selectedRow = 0;
    s.gridReady = false;
    s.plan = [];
    s.resultMode = 'grid';
    s.status = '查询中…';
    s.runSeq += 1;
    const seq = s.runSeq;
    bindSession(s);
    ['grid', 'record', 'plan'].forEach(function (name) {
      const btn = q('db-view-' + name);
      if (btn) btn.classList.toggle('active', name === 'grid');
    });
    hideQueryMessage();
    renderResult();
    pushHistory(sql);
    s.controller = new AbortController();
    state.controller = s.controller;
    refreshActiveQueryUI(s);
    const effSrc = effectiveSource();
    if (!effSrc) {
      s.controller = null;
      state.controller = null;
      refreshActiveQueryUI(s);
      return showQueryError('未选择数据源', '请先在顶部选择数据源。');
    }
    try {
      const response = await fetch('/api/database/query', { method: 'POST', credentials: 'same-origin', signal: s.controller.signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source_id: effSrc.id, sql: sql, max_rows: maxRows, page: s.page, page_size: s.pageSize, count_mode: 'none' }) });
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(body.error || 'HTTP ' + response.status);
      }
      const reader = response.body.getReader(), decoder = new TextDecoder();
      let pending = '';
      while (true) {
        const part = await reader.read();
        pending += decoder.decode(part.value || new Uint8Array(), { stream: !part.done });
        const lines = pending.split('\n'); pending = lines.pop();
        for (const line of lines) if (line.trim()) consumeQueryEvent(s, seq, JSON.parse(line));
        if (part.done) break;
      }
      if (pending.trim()) consumeQueryEvent(s, seq, JSON.parse(pending));
    } catch (e) {
      if (s.runSeq !== seq) return;
      if (e.name !== 'AbortError') {
        s.lastError = { title: '查询执行失败', message: e.message, sql: sql };
        s.status = '执行失败';
        if (s.id === state.activeId) showQueryError('查询执行失败', e.message, sql);
        else toast('页签「' + tabTitle(s) + '」查询失败', 'err');
      } else {
        s.status = '已取消';
        if (s.id === state.activeId) showQueryMessage('warn', '查询已取消', '本次查询已终止，已接收的结果不会继续追加。');
      }
    } finally {
      if (s.runSeq === seq) {
        s.controller = null;
        if (!s.status || s.status === '查询中…') s.status = s.summary ? (s.summary.rows + ' 行') : (s.lastError ? '执行失败' : '就绪');
        refreshActiveQueryUI(s);
        if (s.id === state.activeId) {
          const exp = q('db-export');
          if (exp) exp.disabled = s.rows.length === 0;
        }
      }
    }
  }

  async function runExplain() {
    const s = sess();
    const sql = editorSQL();
    if (!sql) return showQueryError('SQL 为空', '请输入只读查询后再查看执行计划。');
    hideQueryMessage();
    if (s) s.status = '分析计划…';
    if (q('db-query-status')) q('db-query-status').textContent = '分析计划…';
    const effSrc2 = effectiveSource();
    if (!effSrc2) return showQueryError('未选择数据源', '请先在顶部选择数据源。');
    try {
      const data = await api('POST', '/api/database/explain', { source_id: effSrc2.id, sql: sql });
      if (s) { s.plan = data.plan || []; s.status = '执行计划 ' + s.plan.length + ' 行'; }
      state.plan = data.plan || [];
      setResultMode('plan');
      if (q('db-query-status')) q('db-query-status').textContent = '执行计划 ' + state.plan.length + ' 行';
    } catch (e) {
      showQueryError('执行计划失败', e.message, sql);
    }
  }

  function installDatabasePagination() {
    const bar = q('db-result-toolbar');
    if (!bar || q('db-page-nav')) return;
    const nav = el('div', { id: 'db-page-nav', class: 'db-page-nav', role: 'navigation', 'aria-label': '查询结果分页' });
    const prev = el('button', { class: 'btn btn-xs', text: '上一页', title: '上一页' });
    const pageInput = el('input', { class: 'db-page-input', type: 'number', min: '1', value: '1', 'aria-label': '页码' });
    const next = el('button', { class: 'btn btn-xs', text: '下一页', title: '下一页' });
    const size = el('select', { class: 'db-page-size', title: '每页行数', 'aria-label': '每页行数' });
    [50, 100, 200, 500, 1000].forEach(function (n) { size.appendChild(el('option', { value: String(n), text: n + ' 行/页' })); });
    function go(page) {
      const s = sess(); if (!s || s.controller) return;
      s.page = Math.max(1, Number(page) || 1); s.pageSize = Math.max(1, Number(size.value) || 100); s.lastMaxRows = s.pageSize;
      const max = q('db-max-rows'); if (max) max.value = s.pageSize;
      runQuery();
    }
    prev.onclick = function () { const s = sess(); if (s) go((s.page || 1) - 1); };
    next.onclick = function () { const s = sess(); if (s) go((s.page || 1) + 1); };
    pageInput.onkeydown = function (e) { if (e.key === 'Enter') go(pageInput.value); };
    size.onchange = function () { const s = sess(); if (s) { s.page = 1; go(1); } };
    nav.append(prev, el('span', { text: '第' }), pageInput, el('span', { text: '页' }), next, size, el('span', { id: 'db-page-hint', class: 'muted' }));
    bar.appendChild(nav);
    updateDatabasePager();
  }

  function updateDatabasePager() {
    const s = sess(), nav = q('db-page-nav'); if (!nav || !s) return;
    const summary = s.summary || {}, page = Math.max(1, Number(s.page) || Number(summary.page) || 1);
    const input = nav.querySelector('.db-page-input'), buttons = nav.querySelectorAll('button'), size = nav.querySelector('.db-page-size'), hint = q('db-page-hint');
    if (input) input.value = String(page);
    if (size && s.pageSize) size.value = String(s.pageSize);
    if (buttons[0]) buttons[0].disabled = !!s.controller || page <= 1;
    if (buttons[1]) buttons[1].disabled = !!s.controller || !summary.has_next;
    if (hint) hint.textContent = summary.rows != null && summary.page ? ('本页 ' + summary.rows + ' 行' + (summary.has_next ? ' · 还有下一页' : ' · 已到末页')) : '';
  }

  function consumeQueryEvent(s, seq, e) {
    if (!s || s.runSeq !== seq) return;
    if (e.type === 'meta') {
      s.columns = e.columns || [];
      s.gridReady = false;
      if (s.id === state.activeId) { bindSession(s); renderResult(); }
    } else if (e.type === 'rows') {
      s.rows.push.apply(s.rows, e.rows || []);
      if (s.id === state.activeId) {
        bindSession(s);
        if (state.resultMode === 'grid' && state.gridReady) refreshVisibleResult();
        else renderResult();
      }
    } else if (e.type === 'summary') {
      s.summary = e.summary;
      if (e.summary && e.summary.page) { s.page = e.summary.page; s.pageSize = e.summary.page_size || s.pageSize; }
      s.status = e.summary.rows + ' 行 · ' + e.summary.elapsed_ms + ' ms' + (e.summary.retry_count ? ' · 已自动重连' : '') + (e.summary.ordered === false ? ' · 未指定 ORDER BY' : '') + (e.summary.truncated ? ' · 已截断' : '');
      if (s.id === state.activeId) { bindSession(s); refreshVisibleResult(); updateDatabasePager(); }
      else renderTabs();
    } else if (e.type === 'error') throw new Error(e.error || '查询失败');
  }
  function cancelQuery() {
    const s = sess();
    if (s && s.controller) s.controller.abort();
    else if (state.controller) state.controller.abort();
  }
  function hideQueryMessage() { const box = q('db-result-message'); if (box) { box.hidden = true; box.innerHTML = ''; } }
  function showQueryError(title, message, sql) {
    const s = sess();
    if (s) { s.lastError = { title: title, message: message, sql: sql }; s.status = '执行失败'; }
    state.lastError = { title, message, sql };
    showQueryMessage('error', title, message, sql);
    if (q('db-query-status')) q('db-query-status').textContent = '执行失败';
  }
  function showQueryMessage(kind, title, message, sql) {
    const box = q('db-result-message');
    if (!box) { toast(title + '：' + message, kind === 'error' ? 'err' : 'warn'); return; }
    box.hidden = false;
    box.className = 'db-result-message ' + kind;
    box.innerHTML = '<div class="db-message-icon">!</div><div class="db-message-copy"><strong>' + h(title) + '</strong><pre>' + h(message) + '</pre>' + (sql ? '<details><summary>查看本次 SQL</summary><pre>' + h(sql) + '</pre></details>' : '') + '</div><button class="btn btn-xs" id="db-copy-error">复制详情</button>';
    q('db-copy-error').onclick = () => copyDBText(title + '\n' + message + (sql ? '\n\n' + sql : ''), '错误详情已复制');
  }

  function visibleColumns() { return state.columns.map((_, i) => i).filter(i => !state.hiddenColumns.has(i)); }
  function filteredRows() {
    const filter = state.localFilter, sort = state.sort;
    if (gridIndexCache.rows === state.rows && gridIndexCache.len === state.rows.length && gridIndexCache.filter === filter && gridIndexCache.sort === sort && gridIndexCache.indexes) {
      return gridIndexCache.indexes;
    }
    let indexes = state.rows.map((_, i) => i), needle = filter.trim().toLowerCase();
    if (needle) indexes = indexes.filter(i => state.rows[i].some(v => cellText(v).toLowerCase().includes(needle)));
    if (sort) {
      const { index, dir } = sort;
      indexes.sort((a, b) => cellText(state.rows[a][index]).localeCompare(cellText(state.rows[b][index]), undefined, { numeric: true, sensitivity: 'base' }) * dir);
    }
    gridIndexCache = { rows: state.rows, len: state.rows.length, filter: filter, sort: sort, indexes: indexes };
    return indexes;
  }
  function setResultMode(mode) {
    state.resultMode = mode;
    const s = sess();
    if (s) s.resultMode = mode;
    ['grid', 'record', 'plan'].forEach(name => {
      const btn = q('db-view-' + name);
      if (btn) btn.classList.toggle('active', mode === name);
    });
    state.gridReady = false;
    renderResult();
  }
  function updateResultMeta() {
    const indexes = filteredRows(), visible = visibleColumns(), meta = q('db-result-meta');
    if (meta) {
      if (state.resultMode === 'plan') meta.textContent = (state.plan || []).length + ' 步执行计划';
      else meta.textContent = state.columns.length ? visible.length + '/' + state.columns.length + ' 列 · ' + indexes.length + '/' + state.rows.length + ' 行' : '等待执行查询';
    }
    if (q('db-column-manager')) q('db-column-manager').disabled = !state.columns.length;
  }
  function refreshVisibleResult() {
    updateResultMeta();
    if (state.resultMode !== 'grid' || !state.gridReady) { renderResult(); return; }
    const scroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-scroll');
    if (!scroll) return;
    setGridSlice(filteredRows(), visibleColumns());
    paintGridRows(scroll, false);
  }
  function renderResult() {
    cancelGridPaint();
    const grid = q('db-result-grid'); if (!grid) return;
    updateResultMeta();
    if (state.resultMode === 'plan') { renderPlanView(grid); return; }
    if (!state.columns.length) { grid.innerHTML = '<div class="db-result-empty">执行查询后，结果和错误详情会显示在这里。</div>'; state.gridReady = false; return; }
    const indexes = filteredRows(), visible = visibleColumns();
    state.resultMode === 'record' ? renderRecordView(grid, indexes, visible) : renderGridView(grid, indexes, visible);
  }
  function renderPlanView(grid) {
    if (!state.plan.length) {
      grid.innerHTML = '<div class="db-result-empty">点击“执行计划”后，这里按 SQL*Plus / DBMS_XPLAN 原文展示优化器输出。</div>';
      return;
    }
    const tableRows = state.plan.filter(function (row) { return row.cells && row.column_order && row.column_order.length; });
    const raw = state.plan.map(function (row) { return row.raw || ''; }).join('\n');
    if (tableRows.length) {
      const cols = tableRows[0].column_order;
      const tsv = [cols.join('\t')].concat(tableRows.map(function (row) {
        return cols.map(function (col) { return row.cells[col] || ''; }).join('\t');
      })).join('\n');
      const head = cols.map(function (col) { return '<th>' + h(col) + '</th>'; }).join('');
      const body = tableRows.map(function (row) {
        return '<tr>' + cols.map(function (col) { return '<td>' + h(row.cells[col] || '') + '</td>'; }).join('') + '</tr>';
      }).join('');
      grid.innerHTML = '<div class="db-plan-wrap"><div class="db-plan-head"><strong>执行计划</strong><span>' + tableRows.length + ' 行 · MySQL EXPLAIN 表格</span><button class="btn btn-xs" id="db-copy-plan">复制</button></div><div class="db-plan-table-wrap"><table class="table db-plan-table"><thead><tr>' + head + '</tr></thead><tbody>' + body + '</tbody></table></div></div>';
      const copy = q('db-copy-plan');
      if (copy) copy.onclick = function () { copyDBText(tsv, '执行计划已复制'); };
    } else {
      grid.innerHTML = '<div class="db-plan-wrap"><div class="db-plan-head"><strong>执行计划</strong><span>' + state.plan.length + ' 行 · 保留数据库原始格式</span><button class="btn btn-xs" id="db-copy-plan">复制</button></div><pre class="db-plan-text">' + h(raw) + '</pre></div>';
      const copy = q('db-copy-plan');
      if (copy) copy.onclick = function () { copyDBText(raw, '执行计划已复制'); };
    }
    applyGridHeight();
  }
  function renderGridView(grid, indexes, visible) {
    cancelGridPaint();
    setGridSlice(indexes, visible);
    const cols = '<col style="width:54px">' + visible.map(i => '<col data-col="' + i + '" style="width:' + Math.max(72, state.columnWidths[i] || 150) + 'px">').join('');
    const headers = visible.map(i => {
      const c = state.columns[i], sort = state.sort && state.sort.index === i ? (state.sort.dir === 1 ? ' ▲' : ' ▼') : '';
      return '<th data-col="' + i + '" title="' + h(c.database_type || '') + '"><button class="db-column-title" data-sort="' + i + '">' + h(c.name) + h(sort) + '</button><span class="db-col-resizer" data-resize="' + i + '"></span></th>';
    }).join('');
    grid.innerHTML = '<div class="db-table-scroll"><table class="table db-table"><colgroup>' + cols + '</colgroup><thead><tr><th class="num">#</th>' + headers + '</tr></thead><tbody id="db-result-body"></tbody></table></div>';
    const scroll = grid.firstChild;
    const body = q('db-result-body');
    // #12 修复：小结果集直接渲染全部行，避免虚拟化在初始 clientHeight=0 时不显示
    if (indexes.length <= 600) {
      let html = '';
      for (let pos = 0; pos < indexes.length; pos++) {
        const ri = indexes[pos], row = state.rows[ri] || [];
        html += '<tr data-row="' + ri + '"' + (ri === state.selectedRow ? ' class="selected"' : '') + '><td class="num">' + (ri + 1) + '</td>' + visible.map(i => '<td data-row="' + ri + '" data-col="' + i + '" title="双击查看单行；右键复制或更多操作">' + fmtCell(row[i]) + '</td>').join('') + '</tr>';
      }
      body.innerHTML = html;
      bindGridBody(body);
      grid.querySelectorAll('[data-sort]').forEach(btn => btn.onclick = () => {
        const i = Number(btn.dataset.sort);
        state.sort = state.sort && state.sort.index === i ? (state.sort.dir === 1 ? { index: i, dir: -1 } : null) : { index: i, dir: 1 };
        state.gridReady = false; renderResult();
      });
      grid.querySelectorAll('th[data-col]').forEach(th => th.oncontextmenu = e => { e.preventDefault(); openResultMenu(e.clientX, e.clientY, Number(th.dataset.col), null); });
      grid.querySelectorAll('[data-resize]').forEach(x => bindColumnResize(x, Number(x.dataset.resize)));
      state.gridReady = true;
      applyGridHeight();
      return;
    }
    scroll.addEventListener('scroll', function () { scheduleGridPaint(scroll); }, { passive: true });
    bindGridBody(body);
    grid.querySelectorAll('[data-sort]').forEach(btn => btn.onclick = () => {
      const i = Number(btn.dataset.sort);
      state.sort = state.sort && state.sort.index === i ? (state.sort.dir === 1 ? { index: i, dir: -1 } : null) : { index: i, dir: 1 };
      state.gridReady = false; renderResult();
    });
    grid.querySelectorAll('th[data-col]').forEach(th => th.oncontextmenu = e => { e.preventDefault(); openResultMenu(e.clientX, e.clientY, Number(th.dataset.col), null); });
    grid.querySelectorAll('[data-resize]').forEach(x => bindColumnResize(x, Number(x.dataset.resize)));
    state.gridReady = true;
    applyGridHeight();
  }
  function setGridSlice(indexes, visible) {
    gridView.indexes = indexes;
    gridView.visible = visible;
  }
  function gridSliceMarks(indexes, start, end) {
    if (!indexes.length || start >= end) return { head: null, tail: null, mid: null };
    return { head: indexes[start], tail: indexes[end - 1], mid: indexes[start + ((end - start) >> 1)] };
  }
  function updateGridSpacers(body, start, end, total, colspan) {
    const first = body.firstElementChild, last = body.lastElementChild;
    if (start) {
      const hpx = (start * GRID_ROW_H) + 'px';
      if (first && first.classList.contains('db-spacer') && first.firstElementChild) first.firstElementChild.style.height = hpx;
      else body.insertAdjacentHTML('afterbegin', '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + hpx + '"></td></tr>');
    } else if (first && first.classList.contains('db-spacer')) first.remove();
    if (end < total) {
      const hpx = ((total - end) * GRID_ROW_H) + 'px';
      const spacer = body.lastElementChild;
      if (spacer && spacer.classList.contains('db-spacer') && spacer.firstElementChild) spacer.firstElementChild.style.height = hpx;
      else body.insertAdjacentHTML('beforeend', '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + hpx + '"></td></tr>');
    } else if (last && last.classList.contains('db-spacer') && !(start && last === first)) last.remove();
  }
  function cancelGridPaint() {
    if (!gridView.raf) return;
    cancelAnimationFrame(gridView.raf);
    gridView.raf = 0;
  }
  function scheduleGridPaint(scroll) {
    if (gridView.raf) return;
    gridView.raf = requestAnimationFrame(function () {
      gridView.raf = 0;
      if (scroll && scroll.isConnected) paintGridRows(scroll, true);
    });
  }
  function bindGridBody(body) {
    if (!body) return;
    body.addEventListener('click', function (e) {
      const tr = e.target.closest('tr[data-row]');
      if (!tr || !body.contains(tr)) return;
      state.selectedRow = Number(tr.dataset.row);
      body.querySelectorAll('tr.selected').forEach(function (x) { x.classList.remove('selected'); });
      tr.classList.add('selected');
    });
    body.addEventListener('dblclick', function (e) {
      const tr = e.target.closest('tr[data-row]');
      if (!tr || !body.contains(tr)) return;
      state.selectedRow = Number(tr.dataset.row);
      setResultMode('record');
    });
    body.addEventListener('contextmenu', function (e) {
      const td = e.target.closest('td[data-col]');
      if (!td || !body.contains(td)) return;
      e.preventDefault();
      state.selectedRow = Number(td.dataset.row);
      const tr = td.parentElement;
      if (tr) {
        body.querySelectorAll('tr.selected').forEach(function (x) { x.classList.remove('selected'); });
        tr.classList.add('selected');
      }
      openResultMenu(e.clientX, e.clientY, Number(td.dataset.col), Number(td.dataset.row));
    });
  }
  function paintGridRows(scroll, fromScroll) {
    const body = q('db-result-body'); if (!body || !scroll) return;
    const indexes = gridView.indexes, visible = gridView.visible, colspan = visible.length + 1;
    const count = Math.max(1, Math.ceil((scroll.clientHeight || 0) / GRID_ROW_H));
    const start = Math.max(0, Math.floor((scroll.scrollTop || 0) / GRID_ROW_H) - GRID_BUFFER);
    const end = Math.min(indexes.length, start + count + GRID_BUFFER * 2);
    const marks = gridSliceMarks(indexes, start, end);
    const sameWindow = gridView.start === start && gridView.end === end && gridView.head === marks.head && gridView.tail === marks.tail && gridView.mid === marks.mid;
    if (sameWindow && body.querySelector('tr[data-row]')) {
      if (fromScroll && gridView.length === indexes.length) return;
      if (gridView.length !== indexes.length) {
        updateGridSpacers(body, start, end, indexes.length, colspan);
        gridView.length = indexes.length;
      }
      return;
    }
    gridView.start = start;
    gridView.end = end;
    gridView.length = indexes.length;
    gridView.head = marks.head;
    gridView.tail = marks.tail;
    gridView.mid = marks.mid;
    let html = start ? '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + (start * GRID_ROW_H) + 'px"></td></tr>' : '';
    for (let pos = start; pos < end; pos++) {
      const ri = indexes[pos], row = state.rows[ri] || [];
      html += '<tr data-row="' + ri + '"' + (ri === state.selectedRow ? ' class="selected"' : '') + '><td class="num">' + (ri + 1) + '</td>' + visible.map(i => '<td data-row="' + ri + '" data-col="' + i + '" title="双击查看单行；右键复制或更多操作">' + fmtCell(row[i]) + '</td>').join('') + '</tr>';
    }
    if (end < indexes.length) html += '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + ((indexes.length - end) * GRID_ROW_H) + 'px"></td></tr>';
    body.innerHTML = html;
  }
  function bindColumnResize(handle, index) {
    handle.onpointerdown = e => {
      e.preventDefault(); e.stopPropagation();
      const th = handle.parentElement, start = e.clientX, width = th.getBoundingClientRect().width;
      const move = ev => {
        state.columnWidths[index] = Math.max(72, Math.min(640, width + ev.clientX - start));
        const col = q('db-result-grid').querySelector('col[data-col="' + index + '"]');
        if (col) col.style.width = state.columnWidths[index] + 'px';
      };
      const up = () => { document.removeEventListener('pointermove', move); document.removeEventListener('pointerup', up); saveColumnWidths(); };
      document.addEventListener('pointermove', move);
      document.addEventListener('pointerup', up);
    };
  }
  function renderRecordView(grid, indexes, visible) {
    if (!indexes.includes(state.selectedRow)) state.selectedRow = indexes[0] == null ? 0 : indexes[0];
    if (!indexes.length) { grid.innerHTML = '<div class="db-result-empty">当前过滤条件没有匹配记录。</div>'; return; }
    const pos = indexes.indexOf(state.selectedRow), row = state.rows[state.selectedRow];
    grid.innerHTML = '<div class="db-record-nav"><button class="btn btn-xs" id="db-record-prev"' + (pos <= 0 ? ' disabled' : '') + '>上一条</button><strong>记录 ' + (pos + 1) + ' / ' + indexes.length + '（网格第 ' + (state.selectedRow + 1) + ' 行）</strong><button class="btn btn-xs" id="db-record-next"' + (pos >= indexes.length - 1 ? ' disabled' : '') + '>下一条</button><button class="btn btn-xs" id="db-record-copy-fields">复制字段名</button><button class="btn btn-xs" id="db-record-copy">复制整行</button></div><div class="db-record-table">' + visible.map(i => '<div class="db-record-field"><button data-copy-field="' + i + '" title="复制字段名">' + h(state.columns[i].name) + '</button><pre>' + h(cellText(row[i])) + '</pre></div>').join('') + '</div>';
    q('db-record-prev').onclick = () => { state.selectedRow = indexes[pos - 1]; renderResult(); };
    q('db-record-next').onclick = () => { state.selectedRow = indexes[pos + 1]; renderResult(); };
    q('db-record-copy').onclick = () => copyRow(state.selectedRow, visible);
    q('db-record-copy-fields').onclick = copyVisibleColumnNames;
    grid.querySelectorAll('[data-copy-field]').forEach(btn => btn.onclick = () => copyDBText(state.columns[Number(btn.dataset.copyField)].name, '字段名已复制'));
  }
  function copyVisibleColumnNames() {
    const names = visibleColumns().map(i => state.columns[i].name);
    copyDBText(names.join('\t'), '已复制 ' + names.length + ' 个字段名');
  }
  function copyRow(row, columns) {
    copyDBText(columns.map(i => cellText(state.rows[row][i])).join('\t'), '已复制整行');
  }
  function closeMenus(e) { document.querySelectorAll('.db-popup-menu').forEach(m => { if (!e || !m.contains(e.target)) m.remove(); }); }
  function keepMenu(menu) {
    requestAnimationFrame(() => {
      const r = menu.getBoundingClientRect();
      if (r.right > innerWidth - 8) menu.style.left = Math.max(8, innerWidth - r.width - 8) + 'px';
      if (r.bottom > innerHeight - 8) menu.style.top = Math.max(8, innerHeight - r.height - 8) + 'px';
    });
  }
  function openResultMenu(x, y, column, row) {
    closeMenus();
    const menu = el('div', { class: 'db-popup-menu', style: 'left:' + x + 'px;top:' + y + 'px' });
    const add = (text, fn) => menu.appendChild(el('button', { text, onclick: () => { fn(); menu.remove(); } }));
    add('复制字段名', () => copyDBText(state.columns[column].name, '字段名已复制'));
    if (row != null) {
      add('复制单元格', () => copyDBText(cellText(state.rows[row][column]), '单元格已复制'));
      add('复制整行（TSV）', () => copyRow(row, visibleColumns()));
    }
    add('复制整列', () => copyDBText(filteredRows().map(i => cellText(state.rows[i][column])).join('\n'), '整列已复制'));
    add('隐藏此列', () => { state.hiddenColumns.add(column); state.gridReady = false; renderResult(); });
    document.body.appendChild(menu); keepMenu(menu);
    setTimeout(() => document.addEventListener('pointerdown', closeMenus, { once: true }), 0);
  }
  function openColumnManager(e) {
    closeMenus();
    const r = e.currentTarget.getBoundingClientRect();
    const menu = el('div', { class: 'db-popup-menu db-column-picker', style: 'left:' + r.left + 'px;top:' + r.bottom + 'px' });
    menu.appendChild(el('div', { class: 'db-popup-title', text: '显示列' }));
    state.columns.forEach((c, i) => {
      const check = el('input', { type: 'checkbox' });
      check.checked = !state.hiddenColumns.has(i);
      check.onchange = () => { check.checked ? state.hiddenColumns.delete(i) : state.hiddenColumns.add(i); state.gridReady = false; renderResult(); };
      menu.appendChild(el('label', {}, [check, el('span', { text: c.name })]));
    });
    document.body.appendChild(menu); keepMenu(menu);
    setTimeout(() => document.addEventListener('pointerdown', closeMenus, { once: true }), 0);
  }

  function shortcutField(label, key) {
    return '<label class="editor-label db-shortcut-field"><span>' + h(label) + '</span><input id="db-shortcut-' + key + '" class="editor-input db-shortcut-input" type="text" placeholder="点击后按下组合键" readonly autocomplete="off" data-capture-keys="1"></label>';
  }
  function captureShortcut(input) {
    input.addEventListener('keydown', function (e) {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') { input.value = 'Escape'; return; }
      if (e.key === 'Backspace') { input.value = ''; return; }
      const parts = [];
      if (e.ctrlKey) parts.push('Ctrl');
      if (e.altKey) parts.push('Alt');
      if (e.shiftKey) parts.push('Shift');
      if (e.metaKey) parts.push('Meta');
      if (['Control', 'Alt', 'Shift', 'Meta'].indexOf(e.key) >= 0) return;
      const key = e.key === ' ' ? 'Space' : (e.key.length === 1 ? e.key.toUpperCase() : e.key);
      parts.push(key);
      input.value = parts.join('+');
    });
  }
  function openSettings() {
    const body = el('div', { class: 'db-settings-body' });
    body.innerHTML = '<section><h4>快捷键</h4><p class="muted">点输入框后按下组合键即可，保存后立即生效。</p><div class="db-shortcut-grid">' + shortcutField('执行查询', 'run') + shortcutField('取消查询', 'cancel') + shortcutField('网格视图', 'grid') + shortcutField('单行记录', 'record') + shortcutField('执行计划', 'explain') + '</div></section><section><div class="db-setting-line"><h4>SQL 模板</h4><label class="editor-label-inline">展开键<select id="db-expand-key" class="editor-input db-expand-key"><option>Space</option><option>Tab</option><option>Enter</option></select></label></div><div id="db-snippet-list" class="db-snippet-list"></div><button type="button" class="btn btn-sm" id="db-snippet-add">＋ 添加模板</button></section>';
    const footer = el('div', { class: 'editor-footer db-settings-actions' }, [
      el('button', { class: 'btn', type: 'button', id: 'db-settings-reset', text: '恢复默认' }),
      el('button', { class: 'btn', type: 'button', id: 'db-settings-close', text: '关闭' }),
      el('button', { class: 'btn btn-primary', id: 'db-settings-save', text: '保存设置' })
    ]);
    const dlg = Kairo.overlays.modal({ title: 'SQL 模板与快捷键', width: 760, body: body, footer: footer });
    q('db-expand-key').value = state.prefs.expandKey;
    Object.keys(state.prefs.shortcuts).forEach(k => {
      const input = q('db-shortcut-' + k);
      if (input) { input.value = state.prefs.shortcuts[k]; captureShortcut(input); }
    });
    const draft = state.prefs.snippets.map(x => Object.assign({}, x));
    const draw = () => {
      const list = q('db-snippet-list'); list.innerHTML = '';
      draft.forEach((s, i) => {
        const row = el('div', { class: 'db-snippet-row' });
        row.innerHTML = '<label class="db-snippet-enable" title="启用"><input type="checkbox" data-enabled="' + i + '"' + (s.enabled !== false ? ' checked' : '') + '></label><input type="text" class="editor-input" data-key="' + i + '" value="' + h(s.key) + '" placeholder="缩写" autocomplete="off"><textarea class="editor-content" data-text="' + i + '" rows="2" placeholder="SQL 模板；支持 ${schema} 和 ${table}">' + h(s.text) + '</textarea><button type="button" class="btn btn-sm btn-danger" data-remove="' + i + '">删除</button>';
        list.appendChild(row);
      });
      list.querySelectorAll('[data-remove]').forEach(b => b.onclick = () => { draft.splice(Number(b.dataset.remove), 1); draw(); });
    };
    draw();
    q('db-snippet-add').onclick = () => { draft.push({ key: '', text: '', enabled: true }); draw(); };
    q('db-settings-close').onclick = () => dlg.close();
    q('db-settings-reset').onclick = () => { state.prefs = JSON.parse(JSON.stringify(DEFAULT_PREFS)); savePrefs(); dlg.close(); toast('工作台设置已恢复默认', 'ok'); if (state.source && state.source.kind !== 'redis') renderWorkspace(true); };
    q('db-settings-save').onclick = () => {
      body.querySelectorAll('[data-key]').forEach(x => draft[Number(x.dataset.key)].key = x.value.trim());
      body.querySelectorAll('[data-text]').forEach(x => draft[Number(x.dataset.text)].text = x.value);
      body.querySelectorAll('[data-enabled]').forEach(x => draft[Number(x.dataset.enabled)].enabled = x.checked);
      const items = draft.filter(x => x.key && x.text);
      if (new Set(items.map(x => x.key)).size !== items.length) return toast('模板缩写不能重复', 'warn');
      state.prefs.expandKey = q('db-expand-key').value;
      state.prefs.snippets = items;
      Object.keys(state.prefs.shortcuts).forEach(k => { if (q('db-shortcut-' + k)) state.prefs.shortcuts[k] = q('db-shortcut-' + k).value.trim() || DEFAULT_PREFS.shortcuts[k]; });
      savePrefs(); dlg.close(); toast('工作台设置已保存', 'ok');
      if (state.source && state.source.kind !== 'redis') renderWorkspace(true);
    };
  }

  async function exportResult() {
    if (!state.lastSQL || state.controller) return;
    const effSrc3 = effectiveSource();
    if (!effSrc3) return toast('未选择数据源', 'warn');
    const format = (q('db-export-format') && q('db-export-format').value) || 'csv';
    const ext = format === 'json' ? '.json' : format === 'xlsx' ? '.xlsx' : format === 'insert' ? '.sql' : '.csv';
    const types = format === 'json' ? [{ description: 'JSON', accept: { 'application/json': ['.json'] } }]
      : format === 'xlsx' ? [{ description: 'Excel', accept: { 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': ['.xlsx'] } }]
      : format === 'insert' ? [{ description: 'SQL', accept: { 'text/plain': ['.sql'] } }]
      : [{ description: 'CSV 文件', accept: { 'text/csv': ['.csv'] } }];
    const safe = (effSrc3.name || 'query').replace(/[\\/:*?"<>|]/g, '_');
    const filename = safe + '-' + new Date().toISOString().replace(/[:.]/g, '-') + ext;
    const button = q('db-export'); button.disabled = true;
    let handle = null, table = '';
    try {
      if (format === 'insert') {
        const inputTable = Kairo.overlays && Kairo.overlays.prompt
          ? await Kairo.overlays.prompt({ title: '导出 INSERT 语句', label: '请输入 INSERT 目标表名（可含 schema，留空自动从 SQL 推断）：', placeholder: '如 sys_user' })
          : (window.prompt('INSERT 目标表（可含 schema）', '') || '').trim();
        if (inputTable == null) { button.disabled = false; return; }
        table = (inputTable || '').trim();
      }
      if (window.showSaveFilePicker) handle = await window.showSaveFilePicker({ suggestedName: filename, types: types });
      const currentSession = sess();
      const response = await fetch('/api/database/export', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source_id: effSrc3.id, sql: state.lastSQL, max_rows: state.lastMaxRows, page: currentSession && currentSession.page || 1, page_size: currentSession && currentSession.pageSize || state.lastMaxRows, count_mode: 'none', format: format, table: table }) });
      if (!response.ok) { const body = await response.json().catch(() => ({})); throw new Error(body.error || 'HTTP ' + response.status); }
      if (handle && response.body) { const writable = await handle.createWritable(); await response.body.pipeTo(writable); }
      else { const blob = await response.blob(), a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = filename; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000); }
      toast((format === 'xlsx' ? 'Excel' : format.toUpperCase()) + ' 导出完成', 'ok');
    } catch (e) {
      if (e.name !== 'AbortError') toast('导出失败：' + e.message, 'err');
    } finally { button.disabled = state.rows.length === 0; }
  }

  function renderRedis(host) {
    state.cursor = '0'; state.redisKeyBase64 = ''; state.redisType = ''; state.redisCursor = '0'; state.redisNextCursor = '0'; state.redisCursorHistory = []; state.redisOffset = 0; state.redisMembersHasNext = false;
    const mode = state.source.redis_mode || 'standalone';
    host.innerHTML = '<div class="db-redis-workspace"><div class="db-editor-tabs"><div id="db-sql-tabs" class="db-sql-tabs"></div><button type="button" class="btn btn-xs" id="db-tab-add" title="新建查询页签">＋ 页签</button><span class="db-dialect">Redis · 只读命令</span></div><div class="db-redis-layout"><div class="card db-redis-keys"><div class="db-editor-bar"><span class="db-redis-topo">拓扑 ' + h(mode) + (mode === 'cluster' ? ' · 跨主节点 SCAN' : mode === 'sentinel' ? ' · ' + h(state.source.redis_master_name || '') : ' · 单机') + '</span><input id="db-redis-pattern" value="*" placeholder="Key pattern，例如 order:*"><button class="btn btn-primary" id="db-redis-scan">SCAN</button><button class="btn" id="db-redis-next">下一批</button></div><div id="db-redis-list" class="db-redis-list"></div></div><div class="db-redis-center"><div class="card db-redis-detail"><div class="db-pane-title"><span>Key 详情</span><span id="db-redis-type" class="tag"></span></div><div id="db-redis-controls" class="db-redis-controls"></div><pre id="db-redis-value">选择左侧 Key 查看类型、TTL 和内容</pre></div><div class="card db-redis-members"><div class="db-pane-title"><span>成员</span><div><button class="btn btn-xs" id="db-redis-members-prev" disabled>上一页</button><button class="btn btn-xs" id="db-redis-members-next" disabled>下一页</button></div></div><div id="db-redis-members-list" class="db-redis-members-list"><span class="hint">Hash / List / Set / ZSet 选择 Key 后在这里分页查看</span></div></div></div><div class="card db-redis-side"><div class="db-pane-title"><span>运行指标</span><button class="btn btn-xs" id="db-redis-info-refresh">刷新</button></div><div id="db-redis-info" class="db-redis-info"><span class="hint">点击刷新读取 INFO memory / clients / stats / keyspace</span></div><div class="db-pane-title db-redis-command-title">只读命令行</div><div class="db-redis-command-form"><input id="db-redis-command-line" class="db-redis-command-line" placeholder="例如 HGETALL user:1001 或 INFO memory"><select id="db-redis-command"><option>TYPE</option><option>TTL</option><option>PTTL</option><option>GET</option><option>HGET</option><option>HGETALL</option><option>HSCAN</option><option>LLEN</option><option>LRANGE</option><option>SCARD</option><option>SSCAN</option><option>ZCARD</option><option>ZRANGE</option><option>INFO</option><option>DBSIZE</option><option>PING</option></select><input id="db-redis-command-args" placeholder="参数：field 或其他只读参数"><button class="btn btn-primary" id="db-redis-command-run">执行</button></div><div class="hint">命令行仅执行只读白名单，参数按 Redis CLI 习惯拆分；危险命令会被后端拒绝。</div><pre id="db-redis-command-result" class="db-redis-command-result"></pre></div></div></div>';
    renderTabs();
    q('db-tab-add').onclick = addSession;
    q('db-redis-scan').onclick = () => { state.cursor = '0'; scanRedis(true); };
    q('db-redis-next').onclick = () => scanRedis(false);
    q('db-redis-info-refresh').onclick = loadRedisInfo;
    q('db-redis-command-run').onclick = runRedisCommand;
    q('db-redis-members-prev').onclick = function () {
      if (state.redisType === 'hash' || state.redisType === 'set') {
        if (!state.redisCursorHistory.length) return;
        state.redisCursor = state.redisCursorHistory.pop() || '0';
      } else {
        state.redisOffset = Math.max(0, state.redisOffset - state.redisPageSize);
      }
      loadRedisMembers(false);
    };
    q('db-redis-members-next').onclick = function () {
      if (state.redisType === 'hash' || state.redisType === 'set') {
        if (!state.redisNextCursor || state.redisNextCursor === '0') return;
        state.redisCursorHistory.push(state.redisCursor || '0');
        state.redisCursor = state.redisNextCursor;
      } else {
        state.redisOffset += state.redisPageSize;
      }
      loadRedisMembers(false);
    };
    scanRedis(true);
    loadRedisInfo();
  }
  async function scanRedis(reset) {
    const token = state.workspaceToken, source = state.source;
    try {
      const pattern = q('db-redis-pattern');
      const data = await api('GET', '/api/database/redis/scan?source_id=' + encodeURIComponent(source.id) + '&cursor=' + encodeURIComponent(state.cursor || '0') + '&count=200&pattern=' + encodeURIComponent(pattern ? pattern.value || '*' : '*'));
      const next = q('db-redis-next'), list = q('db-redis-list');
      if (token !== state.workspaceToken || !next || !list) return;
      state.cursor = String((data.result && data.result.next_cursor) || '0');
      next.disabled = state.cursor === '0' || state.cursor === '';
      const rows = ((data.result && data.result.keys) || []).map(k => '<button class="db-redis-key" data-key="' + h(k.key_base64) + '">' + h(fmtRedisLabel(k.display)) + '</button>').join('');
      reset ? list.innerHTML = rows || '<div class="hint">没有匹配 Key</div>' : list.insertAdjacentHTML('beforeend', rows);
      list.querySelectorAll('.db-redis-key').forEach(btn => btn.onclick = () => loadRedisKey(btn.dataset.key));
    } catch (e) { if (token === state.workspaceToken) toast('Redis SCAN 失败：' + e.message, 'err'); }
  }
  async function loadRedisKey(key) {
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/redis/key?source_id=' + encodeURIComponent(source.id) + '&key_base64=' + encodeURIComponent(key));
      const value = q('db-redis-value');
      if (token === state.workspaceToken && value) {
        const result = data.result || {};
        state.redisKeyBase64 = key; state.redisType = result.type || '';
        const type = q('db-redis-type'); if (type) type.textContent = result.type || '';
        value.textContent = JSON.stringify(result, null, 2);
        renderRedisTTL(result);
        state.redisCursor = '0'; state.redisNextCursor = '0'; state.redisCursorHistory = []; state.redisOffset = 0; state.redisMembersHasNext = false;
        loadRedisMembers(true);
      }
    } catch (e) { if (token === state.workspaceToken) toast('读取 Key 失败：' + e.message, 'err'); }
  }
  function renderRedisTTL(result) {
    const host = q('db-redis-controls'); if (!host) return;
    host.innerHTML = '';
    host.appendChild(el('span', { class: 'muted', text: 'TTL：' + (result.ttl_ms < 0 ? (result.ttl_ms === -1 ? '永不过期' : '不存在') : Math.ceil(result.ttl_ms / 1000) + ' 秒') }));
    if (!state.source || !state.source.allow_redis_write) return;
    const op = el('select', {}, [el('option', { value: 'EXPIRE', text: '设置 TTL（秒）' }), el('option', { value: 'PERSIST', text: '取消过期' })]);
    const seconds = el('input', { type: 'number', min: '0', max: '31536000', value: '3600', placeholder: '秒' });
    const save = el('button', { class: 'btn btn-xs btn-primary', text: '应用 TTL' });
    save.onclick = async function () {
      if (!confirm('确认修改该 Redis Key 的 TTL？')) return;
      try { await api('POST', '/api/database/redis/mutate', { source_id: state.source.id, key_base64: state.redisKeyBase64, operation: op.value, seconds: Number(seconds.value) || 0, confirm: true }); toast('TTL 已更新', 'ok'); loadRedisKey(state.redisKeyBase64); }
      catch (e) { toast('TTL 更新失败：' + e.message, 'err'); }
    };
    host.append(op, seconds, save);
  }
  async function loadRedisMembers(reset) {
    const source = state.source, key = state.redisKeyBase64, list = q('db-redis-members-list'); if (!source || !key || !list) return;
    if (reset) { state.redisCursor = '0'; state.redisNextCursor = '0'; state.redisCursorHistory = []; state.redisOffset = 0; state.redisMembersHasNext = false; }
    const requestCursor = state.redisCursor || '0';
    const requestOffset = state.redisOffset || 0;
    const params = new URLSearchParams({ source_id: source.id, key_base64: key, type: state.redisType || '', cursor: requestCursor, offset: String(requestOffset), page_size: String(state.redisPageSize || 100) });
    try {
      const data = await api('GET', '/api/database/redis/members?' + params.toString()), result = data.result || {};
      state.redisCursor = requestCursor; state.redisNextCursor = result.next_cursor || '0'; state.redisMembersHasNext = !!result.has_next; state.redisType = result.type || state.redisType;
      list.innerHTML = (result.items || []).map(function (item) { return '<div class="db-redis-member-row"><code>' + h(fmtRedisLabel(item.field || item.member || item)) + '</code>' + (item.value !== undefined ? '<span>' + h(fmtRedisLabel(item.value)) + '</span>' : item.score !== undefined ? '<span class="muted">score ' + h(item.score) + '</span>' : '') + '</div>'; }).join('') || '<span class="hint">没有成员</span>';
      const prev = q('db-redis-members-prev'), next = q('db-redis-members-next');
      if (prev) prev.disabled = (state.redisType === 'hash' || state.redisType === 'set') ? state.redisCursorHistory.length === 0 : state.redisOffset <= 0;
      if (next) next.disabled = (state.redisType === 'hash' || state.redisType === 'set') ? state.redisNextCursor === '0' : !state.redisMembersHasNext;
    } catch (e) { list.innerHTML = '<span class="text-err">成员读取失败：' + h(e.message) + '</span>'; }
  }
  async function loadRedisInfo() {
    const box = q('db-redis-info'), source = state.source; if (!box || !source) return;
    box.textContent = '读取中…';
    try {
      const data = await api('GET', '/api/database/redis/info?source_id=' + encodeURIComponent(source.id) + '&section=memory,clients,stats,keyspace');
      const sections = data.result && data.result.sections || {};
      const keys = ['used_memory_human', 'used_memory_peak_human', 'connected_clients', 'blocked_clients', 'instantaneous_ops_per_sec', 'expired_keys', 'db0'];
      box.innerHTML = keys.map(function (key) { let value = ''; Object.keys(sections).some(function (section) { if (sections[section] && sections[section][key] !== undefined) { value = sections[section][key]; return true; } return false; }); return value === '' ? '' : '<div><span>' + h(key) + '</span><strong>' + h(value) + '</strong></div>'; }).join('') || '<span class="hint">没有可显示的指标</span>';
    } catch (e) { box.innerHTML = '<span class="text-err">INFO 读取失败：' + h(e.message) + '</span>'; }
  }
  function parseRedisCommandLine(raw) {
    const out = [], text = String(raw || '').trim();
    let word = '', quote = '', escaped = false;
    for (let i = 0; i < text.length; i++) {
      const ch = text[i];
      if (escaped) { word += ch; escaped = false; continue; }
      if (ch === '\\') { escaped = true; continue; }
      if (quote) {
        if (ch === quote) quote = ''; else word += ch;
        continue;
      }
      if (ch === '"' || ch === "'") { quote = ch; continue; }
      if (/\s/.test(ch)) { if (word) { out.push(word); word = ''; } continue; }
      word += ch;
    }
    if (escaped) word += '\\';
    if (quote) throw new Error('命令行存在未闭合引号');
    if (word) out.push(word);
    return out;
  }
  function base64UTF8(value) {
    const bytes = new TextEncoder().encode(String(value || ''));
    let binary = '';
    for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    return btoa(binary);
  }
  async function runRedisCommand() {
    const source = state.source, command = q('db-redis-command'), lineInput = q('db-redis-command-line'), argsInput = q('db-redis-command-args'), out = q('db-redis-command-result'); if (!source || !command || !out) return;
    const lineMode = !!(lineInput && lineInput.value.trim());
    let tokens;
    try { tokens = lineMode ? parseRedisCommandLine(lineInput.value) : [command.value].concat((argsInput.value || '').trim() ? parseRedisCommandLine(argsInput.value) : []); }
    catch (e) { out.textContent = '命令行无效：' + e.message; return; }
    if (!tokens.length) return;
    const name = String(tokens[0]).toUpperCase();
    const keyCommands = new Set(['GET', 'TYPE', 'TTL', 'PTTL', 'STRLEN', 'HGET', 'HGETALL', 'HSCAN', 'LLEN', 'LRANGE', 'SCARD', 'SSCAN', 'ZCARD', 'ZRANGE']);
    let key = '';
    let args = tokens.slice(1);
    if (keyCommands.has(name) && lineMode && args.length) key = args.shift();
    const selectedKey = state.redisKeyBase64 && keyCommands.has(name) && (!lineMode || !key) ? state.redisKeyBase64 : '';
    const body = { source_id: source.id, command: name, args: args };
    if (selectedKey) body.key_base64 = selectedKey;
    else if (key) body.key_base64 = base64UTF8(key);
    out.textContent = '执行中…';
    try { const data = await api('POST', '/api/database/redis/command', body); out.textContent = JSON.stringify(data.result, null, 2); }
    catch (e) { out.textContent = '执行失败：' + e.message; }
  }
  function debounce(fn, wait) { let timer; return function () { const self = this, args = arguments; clearTimeout(timer); timer = setTimeout(() => fn.apply(self, args), wait); }; }
  async function renderDatabase(view) {
    const renderToken = view.dataset.renderToken;
    try {
      await loadDatabasePreference();
      await loadSources();
      if (view.dataset.renderToken === renderToken) render(view);
    }
    catch (e) {
      if (view.dataset.renderToken === renderToken) view.innerHTML = '<div class="card text-err">数据库工作台加载失败：' + h(e.message) + '</div>';
    }
  }
  Kairo.state.routes.database = renderDatabase;
  Kairo.state.routeNames.database = '数据库工作台';
  Kairo.state.routeSubs.database = 'Oracle 11g / MySQL / Redis · 安全只读查询';
  Kairo.database = {
    cancel: cancelQuery,
    formatSQL: formatSQL,
    highlightSQL: highlightSQL,
    tokenizeSQL: tokenizeSQL,
    suggestSQL: suggestSQL,
    matchBrackets: matchBrackets,
    completionPrefix: completionPrefix
  };
})();
