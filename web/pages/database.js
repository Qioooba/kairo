/* Database Workbench — read-only Oracle/MySQL/Redis console. */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { toast, escapeHtml, copyToClipboard, el } = Kairo.core;
  const { api } = Kairo.api;
  const LAST_SOURCE = 'kairo:database:last-source';
  const PREFS_KEY = 'kairo:database:workbench-prefs:v2';
  const HISTORY_KEY = 'kairo:database:sql-history:v1';
  const DEFAULT_PREFS = {
    expandKey: 'Space',
    shortcuts: { run: 'Ctrl+Enter', cancel: 'Escape', grid: 'Alt+1', record: 'Alt+2', explain: 'Ctrl+Shift+E' },
    snippets: [
      { key: 'sf', text: 'SELECT * FROM ', enabled: true },
      { key: 'sel', text: 'SELECT *\nFROM ${table}', enabled: true },
      { key: 'cnt', text: 'SELECT COUNT(*)\nFROM ${table}', enabled: true }
    ]
  };
  const state = {
    sources: [], source: null, rows: [], columns: [], controller: null, summary: null, cursor: 0,
    managing: false, workspaceToken: 0, lastSQL: '', lastMaxRows: 0, resultMode: 'grid',
    selectedRow: 0, localFilter: '', hiddenColumns: new Set(), columnWidths: {}, sort: null,
    prefs: loadPrefs(), lastError: null, inspectTab: 'fields', inspect: null, plan: [], gridReady: false
  };

  function h(v) { return escapeHtml(String(v == null ? '' : v)); }
  function q(id) { return document.getElementById(id); }
  function kindLabel(kind) { return kind === 'oracle' ? 'Oracle' : kind === 'mysql' ? 'MySQL' : 'Redis'; }
  function loadPrefs() {
    try {
      const x = JSON.parse(localStorage.getItem(PREFS_KEY) || '{}');
      return {
        expandKey: ['Space', 'Tab', 'Enter'].includes(x.expandKey) ? x.expandKey : 'Space',
        shortcuts: Object.assign({}, DEFAULT_PREFS.shortcuts, x.shortcuts || {}),
        snippets: Array.isArray(x.snippets) && x.snippets.length ? x.snippets : DEFAULT_PREFS.snippets.map(v => Object.assign({}, v))
      };
    } catch (_) {
      return JSON.parse(JSON.stringify(DEFAULT_PREFS));
    }
  }
  function savePrefs() { localStorage.setItem(PREFS_KEY, JSON.stringify(state.prefs)); }
  function loadHistory() {
    try { return JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]'); } catch (_) { return []; }
  }
  function pushHistory(sql) {
    const text = String(sql || '').trim();
    if (!text) return;
    const items = loadHistory().filter(x => x !== text);
    items.unshift(text);
    localStorage.setItem(HISTORY_KEY, JSON.stringify(items.slice(0, 50)));
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
    try { state.columnWidths = JSON.parse(localStorage.getItem('kairo:database:column-widths:' + (state.source ? state.source.id : 'none')) || '{}'); }
    catch (_) { state.columnWidths = {}; }
  }
  function saveColumnWidths() {
    if (state.source) localStorage.setItem('kairo:database:column-widths:' + state.source.id, JSON.stringify(state.columnWidths));
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
    const wanted = preferred || localStorage.getItem(LAST_SOURCE);
    state.source = state.sources.find(s => s.id === wanted) || state.sources[0] || null;
    if (state.source) localStorage.setItem(LAST_SOURCE, state.source.id);
  }
  function sourceOptions() {
    return state.sources.map(s => '<option value="' + h(s.id) + '"' + (state.source && s.id === state.source.id ? ' selected' : '') + '>' + h(s.name) + ' · ' + kindLabel(s.kind) + '</option>').join('');
  }
  function render(view) {
    const user = Kairo.auth && Kairo.auth.getUser ? Kairo.auth.getUser() : '';
    const role = Kairo.auth && Kairo.auth.getRole ? Kairo.auth.getRole() : '';
    const canManage = !user || role === 'admin';
    view.innerHTML = '<div class="db-page"><header class="db-topbar card"><div class="db-source-select"><label for="db-source">数据源</label><select id="db-source">' + sourceOptions() + '</select></div><span id="db-source-badge" class="db-kind"></span><button class="btn btn-sm" id="db-test">测试连接</button>' + (canManage ? '<button class="btn btn-sm" id="db-manage" aria-expanded="false">数据源管理</button>' : '') + '<button class="btn btn-sm" id="db-settings">工作台设置</button><span class="db-safe">只读会话 · 行数 / 时长 / 并发保护</span></header><div id="db-manager"></div><div id="db-workspace"></div></div>';
    q('db-source').onchange = function () {
      state.source = state.sources.find(s => s.id === this.value) || null;
      if (state.source) localStorage.setItem(LAST_SOURCE, state.source.id);
      renderWorkspace();
    };
    q('db-test').onclick = testConnection;
    q('db-settings').onclick = openSettings;
    if (q('db-manage')) q('db-manage').onclick = function () {
      state.managing = !state.managing;
      this.setAttribute('aria-expanded', String(state.managing));
      renderManager();
    };
    renderManager();
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
    host.innerHTML = '<section class="card db-manager"><div class="db-manager-head"><div><h3>' + (s.id ? '编辑数据源' : '新建数据源') + '</h3><div class="muted">连接信息保存在系统凭据库；保存后本面板自动收起。</div></div><select id="db-edit-existing"><option value="">＋ 新建</option>' + state.sources.map(x => '<option value="' + h(x.id) + '"' + (x.id === s.id ? ' selected' : '') + '>' + h(x.name) + '</option>').join('') + '</select></div><div class="db-form-grid">' + field('名称', 'dbf-name', s.name || '') + selectField('类型', 'dbf-kind', [['oracle', 'Oracle 11g+'], ['mysql', 'MySQL'], ['redis', 'Redis']], s.kind) + field('主机', 'dbf-host', s.host || '') + field('端口', 'dbf-port', s.port || '', 'number') + field('用户名', 'dbf-user', s.username || '') + field(s.has_password ? '密码（留空不改）' : '密码', 'dbf-pass', '', 'password') + '<div id="dbf-specific" class="db-form-specific"></div>' + selectField('TLS', 'dbf-tls', [['disabled', '关闭'], ['preferred', '优先（仅 MySQL）'], ['required', '必须且校验证书'], ['skip-verify', '必须但跳过校验']], s.tls_mode || 'disabled') + field('超时（秒）', 'dbf-timeout', s.query_timeout_seconds || 30, 'number') + field('最大行数', 'dbf-rows', s.max_rows || 1000, 'number') + field('最大连接', 'dbf-open', s.max_open_connections || 4, 'number') + field('空闲连接', 'dbf-idle', s.max_idle_connections == null ? 1 : s.max_idle_connections, 'number') + field('授权用户（逗号；*=全员）', 'dbf-users', (s.allowed_users || []).join(', ')) + '</div><div class="db-form-actions"><button class="btn btn-primary" id="dbf-save">保存并收起</button>' + (s.id ? '<button class="btn btn-danger" id="dbf-delete">删除</button>' : '') + '<button class="btn" id="dbf-close">取消</button><span class="hint">密码不会写入配置文件或返回页面。</span></div></section>';
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
    else if (kind === 'mysql') target.innerHTML = field('Database', 'dbf-database', s.database || '');
    else target.innerHTML = field('Redis DB', 'dbf-redis-db', s.redis_db || 0, 'number');
    const preferred = q('dbf-tls').querySelector('option[value="preferred"]');
    preferred.disabled = kind !== 'mysql';
    if (preferred.disabled && q('dbf-tls').value === 'preferred') q('dbf-tls').value = 'required';
    const port = q('dbf-port');
    if (!port.value || ['1521', '3306', '6379'].includes(port.value)) port.value = kind === 'oracle' ? 1521 : kind === 'mysql' ? 3306 : 6379;
  }
  function sourceFromForm(old) {
    const kind = q('dbf-kind').value;
    const source = { name: q('dbf-name').value.trim(), kind, host: q('dbf-host').value.trim(), port: Number(q('dbf-port').value), username: q('dbf-user').value.trim(), tls_mode: q('dbf-tls').value, query_timeout_seconds: Number(q('dbf-timeout').value), max_rows: Number(q('dbf-rows').value), max_result_bytes: (old && old.max_result_bytes) || 16777216, max_open_connections: Number(q('dbf-open').value), max_idle_connections: Number(q('dbf-idle').value), connection_max_minutes: (old && old.connection_max_minutes) || 10, allowed_users: q('dbf-users').value.split(',').map(x => x.trim()).filter(Boolean) };
    if (kind === 'oracle') { source.oracle_connect_by = q('dbf-oracle-by').value; source.oracle_service = q('dbf-service').value.trim(); source.oracle_client_charset = q('dbf-charset').value.trim(); }
    if (kind === 'mysql') source.database = q('dbf-database').value.trim();
    if (kind === 'redis') source.redis_db = Number(q('dbf-redis-db').value);
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
      refreshSourceSelect(); closeManager(); renderWorkspace();
      toast('数据源已保存，管理面板已收起', 'ok');
    } catch (e) { toast('保存失败：' + e.message, 'err'); button.disabled = false; }
  }
  async function deleteSource(source) {
    if (!source.id || !confirm('确定删除数据源“' + source.name + '”及其已保存密码？')) return;
    try {
      await api('DELETE', '/api/database/sources/' + encodeURIComponent(source.id));
      await loadSources(); refreshSourceSelect(); closeManager(); renderWorkspace();
      toast('数据源已删除', 'ok');
    } catch (e) { toast('删除失败：' + e.message, 'err'); }
  }
  function refreshSourceSelect() { const s = q('db-source'); if (s) s.innerHTML = sourceOptions(); }
  async function testConnection() {
    if (!state.source) return toast('请先创建数据源', 'warn');
    const b = q('db-test'); b.disabled = true; b.textContent = '连接中…';
    try {
      const r = await api('POST', '/api/database/sources/' + encodeURIComponent(state.source.id) + '/test', {});
      toast('连接成功 · ' + (r.version || kindLabel(r.kind)) + ' · ' + r.latency_ms + ' ms', 'ok');
    } catch (e) { toast('连接失败：' + e.message, 'err'); }
    finally { b.disabled = false; b.textContent = '测试连接'; }
  }

  function renderWorkspace() {
    const host = q('db-workspace'); if (!host) return;
    cancelQuery(); state.workspaceToken++;
    q('db-source-badge').textContent = state.source ? kindLabel(state.source.kind) : '未配置';
    if (!state.source) {
      host.innerHTML = '<div class="card empty-state"><div class="empty-title">尚无数据源</div><div class="empty-desc">打开“数据源管理”创建 Oracle、MySQL 或 Redis 连接。</div></div>';
      return;
    }
    state.rows = []; state.columns = []; state.summary = null; state.lastError = null; state.plan = [];
    state.hiddenColumns = new Set(); state.sort = null; state.inspect = null; state.gridReady = false;
    loadColumnWidths();
    state.source.kind === 'redis' ? renderRedis(host) : renderSQL(host);
  }

  function renderSQL(host) {
    const initial = state.source.kind === 'oracle' ? 'SELECT SYSDATE AS SERVER_TIME FROM DUAL' : 'SELECT NOW() AS server_time';
    const history = loadHistory();
    host.innerHTML = '<div class="db-sql-layout"><aside class="card db-meta"><div class="db-pane-title"><span>数据库对象</span><button class="btn btn-xs" id="db-meta-refresh">刷新</button></div><label class="db-compact-label">Schema<select id="db-schema"><option>加载中…</option></select></label><input id="db-object-search" type="search" placeholder="搜索表、视图、函数、过程"><div id="db-objects" class="db-object-list"><div class="db-tree-loading">正在读取元数据…</div></div><div class="db-inspect"><div class="db-inspect-tabs"><button class="db-inspect-tab active" data-tab="fields">字段</button><button class="db-inspect-tab" data-tab="indexes">索引</button><button class="db-inspect-tab" data-tab="constraints">约束</button><button class="db-inspect-tab" data-tab="ddl">DDL</button></div><div id="db-inspect-body" class="db-field-list"><div class="hint">单击对象查看字段、索引、约束和 DDL；双击生成查询。</div></div></div></aside><main class="db-main"><section class="card db-editor-card"><div class="db-editor-tabs"><span class="db-editor-tab active">SQL Console</span><span class="db-dialect">' + kindLabel(state.source.kind) + '</span></div><div class="db-editor-bar"><button class="btn btn-primary" id="db-run">执行 <kbd>' + h(state.prefs.shortcuts.run) + '</kbd></button><button class="btn" id="db-explain">执行计划</button><button class="btn" id="db-cancel" disabled>取消</button><label>最多 <input id="db-max-rows" type="number" min="1" max="' + state.source.max_rows + '" value="' + Math.min(1000, state.source.max_rows) + '"> 行</label><button class="btn" id="db-export" disabled>导出 CSV</button><select id="db-history" title="查询历史"><option value="">历史</option>' + history.map(function (sql, i) { return '<option value="' + i + '">' + h(sql.replace(/\s+/g, ' ').slice(0, 80)) + '</option>'; }).join('') + '</select><span id="db-query-status" class="db-query-status">就绪</span></div><textarea id="db-sql" class="db-sql-editor" spellcheck="false">' + h(initial) + '</textarea><div class="db-editor-help">模板：输入缩写后按 ' + h(state.prefs.expandKey) + ' 展开 · 可在“工作台设置”中自定义</div></section><section class="card db-results"><div class="db-result-toolbar"><div class="db-result-tabs"><button id="db-view-grid" class="db-view-btn active">网格</button><button id="db-view-record" class="db-view-btn">单行记录</button><button id="db-view-plan" class="db-view-btn">执行计划</button></div><input id="db-result-filter" type="search" placeholder="在当前结果中过滤"><button class="btn btn-xs" id="db-copy-columns" disabled>复制字段名</button><button class="btn btn-xs" id="db-column-manager" disabled>显示列</button><span id="db-result-meta">等待执行查询</span></div><div id="db-result-message" class="db-result-message" hidden></div><div id="db-result-grid" class="db-result-grid"></div></section></main></div>';
    q('db-run').onclick = runQuery;
    q('db-explain').onclick = runExplain;
    q('db-cancel').onclick = cancelQuery;
    q('db-export').onclick = exportCSV;
    q('db-sql').onkeydown = handleEditorKeydown;
    q('db-meta-refresh').onclick = () => loadSchemas(true);
    q('db-schema').onchange = loadObjects;
    q('db-object-search').oninput = debounce(loadObjects, 220);
    q('db-view-grid').onclick = () => setResultMode('grid');
    q('db-view-record').onclick = () => setResultMode('record');
    q('db-view-plan').onclick = () => setResultMode('plan');
    q('db-result-filter').oninput = debounce(function () { state.localFilter = this.value; refreshVisibleResult(); }, 120);
    q('db-copy-columns').onclick = copyVisibleColumnNames;
    q('db-column-manager').onclick = openColumnManager;
    q('db-history').onchange = function () {
      const items = loadHistory();
      const sql = items[Number(this.value)];
      if (sql) q('db-sql').value = sql;
      this.value = '';
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
      const o = q('db-objects');
      if (token === state.workspaceToken && o) o.innerHTML = inlineError('元数据加载失败', e.message);
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
    if (copy) copy.onclick = () => { copyToClipboard(names.join(', ')); toast('已复制 ' + names.length + ' 个字段名', 'ok'); };
    body.querySelectorAll('.db-field-row[data-field]').forEach(btn => {
      btn.ondblclick = () => insertAtCursor(q('db-sql'), quoteIdentifier(btn.dataset.field));
    });
  }

  function quoteIdentifier(v) {
    const quote = state.source.kind === 'mysql' ? '`' : '"';
    return quote + String(v).replaceAll(quote, quote + quote) + quote;
  }
  function insertObjectSQL(schema, object, type) {
    const name = quoteIdentifier(schema) + '.' + quoteIdentifier(object);
    const upper = String(type).toUpperCase();
    let sql = 'SELECT *\nFROM ' + name;
    if (upper === 'FUNCTION') sql = state.source.kind === 'oracle' ? 'SELECT ' + name + '() AS result FROM DUAL' : 'SELECT ' + name + '() AS result';
    else if (upper === 'PROCEDURE' || upper === 'PACKAGE') sql = state.source.kind === 'oracle' ? 'BEGIN\n  ' + name + '();\nEND;' : 'CALL ' + name + '();';
    else if (upper === 'TRIGGER') sql = '-- Trigger: ' + name;
    const e = q('db-sql'); e.value = sql; e.focus();
  }
  function inlineError(title, message) {
    return '<div class="db-inline-error"><strong>' + h(title) + '</strong><span>' + h(message) + '</span></div>';
  }

  function handleEditorKeydown(e) {
    if (matchesShortcut(e, state.prefs.shortcuts.run)) { e.preventDefault(); runQuery(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.explain)) { e.preventDefault(); runExplain(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.cancel) && state.controller) { e.preventDefault(); cancelQuery(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.grid)) { e.preventDefault(); setResultMode('grid'); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.record)) { e.preventDefault(); setResultMode('record'); return; }
    const trigger = state.prefs.expandKey;
    if ((trigger === 'Space' && e.key === ' ') || (trigger === 'Tab' && e.key === 'Tab') || (trigger === 'Enter' && e.key === 'Enter')) {
      if (expandSnippet(e.currentTarget)) e.preventDefault();
    }
  }
  function matchesShortcut(e, spec) {
    if (!spec) return false;
    const parts = String(spec).toLowerCase().split('+').map(x => x.trim());
    const key = parts.pop();
    const actual = String(e.key).toLowerCase();
    const ok = (key === 'space' && actual === ' ') || actual === key;
    return ok && e.ctrlKey === parts.includes('ctrl') && e.altKey === parts.includes('alt') && e.shiftKey === parts.includes('shift') && e.metaKey === parts.includes('meta');
  }
  function expandSnippet(editor) {
    const pos = editor.selectionStart, before = editor.value.slice(0, pos), match = before.match(/([A-Za-z0-9_.-]+)$/);
    if (!match) return false;
    const snippet = state.prefs.snippets.find(x => x.enabled !== false && x.key === match[1]);
    if (!snippet) return false;
    const schema = q('db-schema') ? q('db-schema').value : '', start = pos - match[1].length;
    let replacement = String(snippet.text).replaceAll('${schema}', schema || '').replaceAll('${table}', 'table_name');
    const cursor = replacement.indexOf('${cursor}');
    replacement = replacement.replaceAll('${cursor}', '');
    editor.setRangeText(replacement, start, pos, 'end');
    if (cursor >= 0) editor.setSelectionRange(start + cursor, start + cursor);
    return true;
  }
  function insertAtCursor(editor, text) { editor.setRangeText(text, editor.selectionStart, editor.selectionEnd, 'end'); editor.focus(); }

  async function runQuery() {
    if (state.controller) return;
    const sql = q('db-sql').value.trim();
    if (!sql) return showQueryError('SQL 为空', '请输入只读查询后再执行。');
    const maxRows = Number(q('db-max-rows').value);
    Object.assign(state, { lastSQL: sql, lastMaxRows: maxRows, rows: [], columns: [], summary: null, lastError: null, sort: null, hiddenColumns: new Set(), selectedRow: 0, gridReady: false, plan: [] });
    hideQueryMessage();
    renderResult();
    pushHistory(sql);
    state.controller = new AbortController();
    q('db-run').disabled = true; q('db-cancel').disabled = false; q('db-export').disabled = true;
    q('db-query-status').textContent = '查询中…';
    try {
      const response = await fetch('/api/database/query', { method: 'POST', credentials: 'same-origin', signal: state.controller.signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source_id: state.source.id, sql, max_rows: maxRows }) });
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
        for (const line of lines) if (line.trim()) consumeQueryEvent(JSON.parse(line));
        if (part.done) break;
      }
      if (pending.trim()) consumeQueryEvent(JSON.parse(pending));
    } catch (e) {
      if (e.name !== 'AbortError') showQueryError('查询执行失败', e.message, sql);
      else showQueryMessage('warn', '查询已取消', '本次查询已终止，已接收的结果不会继续追加。');
    } finally {
      state.controller = null;
      const run = q('db-run'), cancel = q('db-cancel'), exp = q('db-export');
      if (run) run.disabled = false;
      if (cancel) cancel.disabled = true;
      if (exp) exp.disabled = state.rows.length === 0;
    }
  }

  async function runExplain() {
    const sql = q('db-sql').value.trim();
    if (!sql) return showQueryError('SQL 为空', '请输入只读查询后再查看执行计划。');
    hideQueryMessage();
    q('db-query-status').textContent = '分析计划…';
    try {
      const data = await api('POST', '/api/database/explain', { source_id: state.source.id, sql });
      state.plan = data.plan || [];
      setResultMode('plan');
      q('db-query-status').textContent = '执行计划 ' + state.plan.length + ' 行';
    } catch (e) {
      showQueryError('执行计划失败', e.message, sql);
    }
  }

  function consumeQueryEvent(e) {
    if (e.type === 'meta') {
      state.columns = e.columns || [];
      state.gridReady = false;
      renderResult();
    } else if (e.type === 'rows') {
      state.rows.push.apply(state.rows, e.rows || []);
      if (state.resultMode === 'grid' && state.gridReady) refreshVisibleResult();
      else renderResult();
    } else if (e.type === 'summary') {
      state.summary = e.summary;
      q('db-query-status').textContent = e.summary.rows + ' 行 · ' + e.summary.elapsed_ms + ' ms' + (e.summary.truncated ? ' · 已截断' : '');
      refreshVisibleResult();
    } else if (e.type === 'error') throw new Error(e.error || '查询失败');
  }
  function cancelQuery() { if (state.controller) state.controller.abort(); }
  function hideQueryMessage() { const box = q('db-result-message'); if (box) { box.hidden = true; box.innerHTML = ''; } }
  function showQueryError(title, message, sql) {
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
    q('db-copy-error').onclick = () => { copyToClipboard(title + '\n' + message + (sql ? '\n\n' + sql : '')); toast('错误详情已复制', 'ok'); };
  }

  function visibleColumns() { return state.columns.map((_, i) => i).filter(i => !state.hiddenColumns.has(i)); }
  function filteredRows() {
    let indexes = state.rows.map((_, i) => i), needle = state.localFilter.trim().toLowerCase();
    if (needle) indexes = indexes.filter(i => state.rows[i].some(v => cellText(v).toLowerCase().includes(needle)));
    if (state.sort) {
      const { index, dir } = state.sort;
      indexes.sort((a, b) => cellText(state.rows[a][index]).localeCompare(cellText(state.rows[b][index]), undefined, { numeric: true, sensitivity: 'base' }) * dir);
    }
    return indexes;
  }
  function setResultMode(mode) {
    state.resultMode = mode;
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
    if (q('db-copy-columns')) q('db-copy-columns').disabled = !state.columns.length;
    if (q('db-column-manager')) q('db-column-manager').disabled = !state.columns.length;
  }
  function refreshVisibleResult() {
    updateResultMeta();
    if (state.resultMode !== 'grid' || !state.gridReady) { renderResult(); return; }
    const scroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-scroll');
    if (scroll) renderVisibleRows(scroll, filteredRows(), visibleColumns());
  }
  function renderResult() {
    const grid = q('db-result-grid'); if (!grid) return;
    updateResultMeta();
    if (state.resultMode === 'plan') { renderPlanView(grid); return; }
    if (!state.columns.length) { grid.innerHTML = '<div class="db-result-empty">执行查询后，结果和错误详情会显示在这里。</div>'; state.gridReady = false; return; }
    const indexes = filteredRows(), visible = visibleColumns();
    state.resultMode === 'record' ? renderRecordView(grid, indexes, visible) : renderGridView(grid, indexes, visible);
  }
  function renderPlanView(grid) {
    if (!state.plan.length) { grid.innerHTML = '<div class="db-result-empty">点击“执行计划”后，这里显示优化器步骤。不做图形化，便于对照 SQL*Plus / DBMS_XPLAN。</div>'; return; }
    grid.innerHTML = '<div class="db-table-scroll"><table class="table db-table db-plan-table"><thead><tr><th>ID</th><th>操作</th><th>对象</th><th>选项</th><th>行数</th><th>成本</th><th>原文</th></tr></thead><tbody>' + state.plan.map(row => '<tr><td>' + h(row.id) + '</td><td>' + h(row.operation) + '</td><td>' + h(row.object) + '</td><td>' + h(row.options) + '</td><td>' + h(row.cardinality) + '</td><td>' + h(row.cost) + '</td><td><pre>' + h(row.raw || row.extra) + '</pre></td></tr>').join('') + '</tbody></table></div>';
  }
  function renderGridView(grid, indexes, visible) {
    const cols = '<col style="width:54px">' + visible.map(i => '<col data-col="' + i + '" style="width:' + Math.max(72, state.columnWidths[i] || 150) + 'px">').join('');
    const headers = visible.map(i => {
      const c = state.columns[i], sort = state.sort && state.sort.index === i ? (state.sort.dir === 1 ? ' ▲' : ' ▼') : '';
      return '<th data-col="' + i + '" title="' + h(c.database_type || '') + '"><button class="db-column-title" data-sort="' + i + '">' + h(c.name) + h(sort) + '</button><span class="db-col-resizer" data-resize="' + i + '"></span></th>';
    }).join('');
    grid.innerHTML = '<div class="db-table-scroll"><table class="table db-table"><colgroup>' + cols + '</colgroup><thead><tr><th class="num">#</th>' + headers + '</tr></thead><tbody id="db-result-body"></tbody></table></div>';
    const scroll = grid.firstChild;
    scroll.onscroll = () => renderVisibleRows(scroll, indexes, visible);
    grid.querySelectorAll('[data-sort]').forEach(btn => btn.onclick = () => {
      const i = Number(btn.dataset.sort);
      state.sort = state.sort && state.sort.index === i ? (state.sort.dir === 1 ? { index: i, dir: -1 } : null) : { index: i, dir: 1 };
      state.gridReady = false; renderResult();
    });
    grid.querySelectorAll('th[data-col]').forEach(th => th.oncontextmenu = e => { e.preventDefault(); openResultMenu(e.clientX, e.clientY, Number(th.dataset.col), null); });
    grid.querySelectorAll('[data-resize]').forEach(x => bindColumnResize(x, Number(x.dataset.resize)));
    state.gridReady = true;
    renderVisibleRows(scroll, indexes, visible);
  }
  function renderVisibleRows(scroll, indexes, visible) {
    const body = q('db-result-body'); if (!body) return;
    const height = 31, buffer = 12, count = Math.ceil(scroll.clientHeight / height);
    const start = Math.max(0, Math.floor(scroll.scrollTop / height) - buffer);
    const end = Math.min(indexes.length, start + count + buffer * 2);
    let html = start ? '<tr class="db-spacer"><td colspan="' + (visible.length + 1) + '" style="height:' + (start * height) + 'px"></td></tr>' : '';
    for (let pos = start; pos < end; pos++) {
      const ri = indexes[pos], row = state.rows[ri];
      html += '<tr data-row="' + ri + '"' + (ri === state.selectedRow ? ' class="selected"' : '') + '><td class="num">' + (ri + 1) + '</td>' + visible.map(i => '<td data-row="' + ri + '" data-col="' + i + '" title="双击复制；右键更多操作">' + fmtCell(row[i]) + '</td>').join('') + '</tr>';
    }
    if (end < indexes.length) html += '<tr class="db-spacer"><td colspan="' + (visible.length + 1) + '" style="height:' + ((indexes.length - end) * height) + 'px"></td></tr>';
    body.innerHTML = html;
    body.querySelectorAll('tr[data-row]').forEach(tr => tr.onclick = () => {
      state.selectedRow = Number(tr.dataset.row);
      body.querySelectorAll('tr.selected').forEach(x => x.classList.remove('selected'));
      tr.classList.add('selected');
    });
    body.querySelectorAll('td[data-col]').forEach(td => {
      td.ondblclick = () => { copyToClipboard(cellText(state.rows[Number(td.dataset.row)][Number(td.dataset.col)])); toast('已复制单元格', 'ok'); };
      td.oncontextmenu = e => { e.preventDefault(); state.selectedRow = Number(td.dataset.row); openResultMenu(e.clientX, e.clientY, Number(td.dataset.col), Number(td.dataset.row)); };
    });
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
    grid.innerHTML = '<div class="db-record-nav"><button class="btn btn-xs" id="db-record-prev"' + (pos <= 0 ? ' disabled' : '') + '>上一条</button><strong>记录 ' + (pos + 1) + ' / ' + indexes.length + '</strong><button class="btn btn-xs" id="db-record-next"' + (pos >= indexes.length - 1 ? ' disabled' : '') + '>下一条</button><button class="btn btn-xs" id="db-record-copy">复制整行</button></div><div class="db-record-table">' + visible.map(i => '<div class="db-record-field"><button data-copy-field="' + i + '" title="复制字段名">' + h(state.columns[i].name) + '</button><pre>' + h(cellText(row[i])) + '</pre></div>').join('') + '</div>';
    q('db-record-prev').onclick = () => { state.selectedRow = indexes[pos - 1]; renderResult(); };
    q('db-record-next').onclick = () => { state.selectedRow = indexes[pos + 1]; renderResult(); };
    q('db-record-copy').onclick = () => copyRow(state.selectedRow, visible);
    grid.querySelectorAll('[data-copy-field]').forEach(btn => btn.onclick = () => { copyToClipboard(state.columns[Number(btn.dataset.copyField)].name); toast('字段名已复制', 'ok'); });
  }
  function copyVisibleColumnNames() {
    const names = visibleColumns().map(i => state.columns[i].name);
    copyToClipboard(names.join('\t')); toast('已复制 ' + names.length + ' 个字段名', 'ok');
  }
  function copyRow(row, columns) {
    copyToClipboard(columns.map(i => cellText(state.rows[row][i])).join('\t')); toast('已复制整行', 'ok');
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
    add('复制字段名', () => copyToClipboard(state.columns[column].name));
    if (row != null) {
      add('复制单元格', () => copyToClipboard(cellText(state.rows[row][column])));
      add('复制整行（TSV）', () => copyRow(row, visibleColumns()));
    }
    add('复制整列', () => copyToClipboard(filteredRows().map(i => cellText(state.rows[i][column])).join('\n')));
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

  function shortcutField(label, key) { return '<label><span>' + h(label) + '</span><input id="db-shortcut-' + key + '" placeholder="例如 Ctrl+Enter"></label>'; }
  function openSettings() {
    const overlay = el('div', { class: 'db-settings-overlay' });
    const dialog = el('section', { class: 'db-settings-dialog', role: 'dialog', 'aria-modal': 'true' });
    const close = () => overlay.remove();
    dialog.innerHTML = '<div class="db-settings-head"><div><h3>SQL 模板与快捷键</h3><p>模板缩写在编辑器中按展开键替换；设置仅保存在当前浏览器。</p></div><button class="btn btn-sm" id="db-settings-close">关闭</button></div><div class="db-settings-body"><section><h4>快捷键</h4><div class="db-shortcut-grid">' + shortcutField('执行查询', 'run') + shortcutField('取消查询', 'cancel') + shortcutField('网格视图', 'grid') + shortcutField('单行记录', 'record') + shortcutField('执行计划', 'explain') + '</div></section><section><div class="db-setting-line"><h4>SQL 模板</h4><label>展开键 <select id="db-expand-key"><option>Space</option><option>Tab</option><option>Enter</option></select></label></div><div id="db-snippet-list" class="db-snippet-list"></div><button class="btn btn-sm" id="db-snippet-add">＋ 添加模板</button></section></div><div class="db-settings-actions"><button class="btn" id="db-settings-reset">恢复默认</button><button class="btn btn-primary" id="db-settings-save">保存设置</button></div>';
    overlay.appendChild(dialog); document.body.appendChild(overlay);
    q('db-settings-close').onclick = close;
    overlay.onclick = e => { if (e.target === overlay) close(); };
    q('db-expand-key').value = state.prefs.expandKey;
    Object.keys(state.prefs.shortcuts).forEach(k => { if (q('db-shortcut-' + k)) q('db-shortcut-' + k).value = state.prefs.shortcuts[k]; });
    const draft = state.prefs.snippets.map(x => Object.assign({}, x));
    const draw = () => {
      const list = q('db-snippet-list'); list.innerHTML = '';
      draft.forEach((s, i) => {
        const row = el('div', { class: 'db-snippet-row' });
        row.innerHTML = '<input type="checkbox" data-enabled="' + i + '"' + (s.enabled !== false ? ' checked' : '') + '><input data-key="' + i + '" value="' + h(s.key) + '" placeholder="缩写"><textarea data-text="' + i + '" placeholder="SQL 模板；支持 ${schema} 和 ${table}">' + h(s.text) + '</textarea><button class="btn btn-xs btn-danger" data-remove="' + i + '">删除</button>';
        list.appendChild(row);
      });
      list.querySelectorAll('[data-remove]').forEach(b => b.onclick = () => { draft.splice(Number(b.dataset.remove), 1); draw(); });
    };
    draw();
    q('db-snippet-add').onclick = () => { draft.push({ key: '', text: '', enabled: true }); draw(); };
    q('db-settings-reset').onclick = () => { state.prefs = JSON.parse(JSON.stringify(DEFAULT_PREFS)); savePrefs(); close(); toast('工作台设置已恢复默认', 'ok'); if (state.source && state.source.kind !== 'redis') renderWorkspace(); };
    q('db-settings-save').onclick = () => {
      dialog.querySelectorAll('[data-key]').forEach(x => draft[Number(x.dataset.key)].key = x.value.trim());
      dialog.querySelectorAll('[data-text]').forEach(x => draft[Number(x.dataset.text)].text = x.value);
      dialog.querySelectorAll('[data-enabled]').forEach(x => draft[Number(x.dataset.enabled)].enabled = x.checked);
      const items = draft.filter(x => x.key && x.text);
      if (new Set(items.map(x => x.key)).size !== items.length) return toast('模板缩写不能重复', 'warn');
      state.prefs.expandKey = q('db-expand-key').value;
      state.prefs.snippets = items;
      Object.keys(state.prefs.shortcuts).forEach(k => { if (q('db-shortcut-' + k)) state.prefs.shortcuts[k] = q('db-shortcut-' + k).value.trim(); });
      savePrefs(); close(); toast('工作台设置已保存', 'ok');
      if (state.source && state.source.kind !== 'redis') renderWorkspace();
    };
  }

  async function exportCSV() {
    if (!state.lastSQL || state.controller) return;
    const safe = (state.source.name || 'query').replace(/[\\/:*?"<>|]/g, '_');
    const filename = safe + '-' + new Date().toISOString().replace(/[:.]/g, '-') + '.csv';
    const button = q('db-export'); button.disabled = true;
    let handle = null;
    try {
      if (window.showSaveFilePicker) handle = await window.showSaveFilePicker({ suggestedName: filename, types: [{ description: 'CSV 文件', accept: { 'text/csv': ['.csv'] } }] });
      const response = await fetch('/api/database/export', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source_id: state.source.id, sql: state.lastSQL, max_rows: state.lastMaxRows }) });
      if (!response.ok) { const body = await response.json().catch(() => ({})); throw new Error(body.error || 'HTTP ' + response.status); }
      if (handle && response.body) { const writable = await handle.createWritable(); await response.body.pipeTo(writable); }
      else { const blob = await response.blob(), a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = filename; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000); }
      toast('CSV 导出完成', 'ok');
    } catch (e) {
      if (e.name !== 'AbortError') toast('CSV 导出失败：' + e.message, 'err');
    } finally { button.disabled = state.rows.length === 0; }
  }

  function renderRedis(host) {
    state.cursor = 0;
    host.innerHTML = '<div class="db-redis-layout"><div class="card db-redis-keys"><div class="db-editor-bar"><input id="db-redis-pattern" value="*" placeholder="Key pattern，例如 order:*"><button class="btn btn-primary" id="db-redis-scan">SCAN</button><button class="btn" id="db-redis-next">下一批</button></div><div id="db-redis-list" class="db-redis-list"></div></div><div class="card db-redis-detail"><div class="db-pane-title">Key 详情</div><pre id="db-redis-value">选择左侧 Key 查看类型、TTL 和内容</pre></div></div>';
    q('db-redis-scan').onclick = () => { state.cursor = 0; scanRedis(true); };
    q('db-redis-next').onclick = () => scanRedis(false);
    scanRedis(true);
  }
  async function scanRedis(reset) {
    const token = state.workspaceToken, source = state.source;
    try {
      const pattern = q('db-redis-pattern');
      const data = await api('GET', '/api/database/redis/scan?source_id=' + encodeURIComponent(source.id) + '&cursor=' + state.cursor + '&count=200&pattern=' + encodeURIComponent(pattern ? pattern.value || '*' : '*'));
      const next = q('db-redis-next'), list = q('db-redis-list');
      if (token !== state.workspaceToken || !next || !list) return;
      state.cursor = data.result.next_cursor; next.disabled = state.cursor === 0;
      const rows = (data.result.keys || []).map(k => '<button class="db-redis-key" data-key="' + h(k.key_base64) + '">' + h(fmtRedisLabel(k.display)) + '</button>').join('');
      reset ? list.innerHTML = rows || '<div class="hint">没有匹配 Key</div>' : list.insertAdjacentHTML('beforeend', rows);
      list.querySelectorAll('.db-redis-key').forEach(btn => btn.onclick = () => loadRedisKey(btn.dataset.key));
    } catch (e) { if (token === state.workspaceToken) toast('Redis SCAN 失败：' + e.message, 'err'); }
  }
  async function loadRedisKey(key) {
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/redis/key?source_id=' + encodeURIComponent(source.id) + '&key_base64=' + encodeURIComponent(key));
      const value = q('db-redis-value');
      if (token === state.workspaceToken && value) value.textContent = JSON.stringify(data.result, null, 2);
    } catch (e) { if (token === state.workspaceToken) toast('读取 Key 失败：' + e.message, 'err'); }
  }
  function debounce(fn, wait) { let timer; return function () { const self = this, args = arguments; clearTimeout(timer); timer = setTimeout(() => fn.apply(self, args), wait); }; }
  async function renderDatabase(view) {
    try { await loadSources(); render(view); }
    catch (e) { view.innerHTML = '<div class="card text-err">数据库工作台加载失败：' + h(e.message) + '</div>'; }
  }
  Kairo.state.routes.database = renderDatabase;
  Kairo.state.routeNames.database = '数据库工作台';
  Kairo.state.routeSubs.database = 'Oracle 11g / MySQL / Redis · 安全只读查询';
  Kairo.database = { cancel: cancelQuery };
})();
