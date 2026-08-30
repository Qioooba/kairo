/* Database Workbench v1 — Oracle 11g / MySQL read-only SQL + Redis browser. */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { toast, escapeHtml, copyToClipboard } = Kairo.core;
  const { api } = Kairo.api;
  const state = { sources: [], source: null, rows: [], columns: [], controller: null, summary: null, cursor: 0, managing: false, workspaceToken: 0, lastSQL: '', lastMaxRows: 0 };
  const LAST_SOURCE = 'kairo:database:last-source';

  function h(value) { return escapeHtml(String(value == null ? '' : value)); }
  function q(id) { return document.getElementById(id); }
  function kindLabel(kind) { return kind === 'oracle' ? 'Oracle' : kind === 'mysql' ? 'MySQL' : 'Redis'; }
  function fmtCell(value) {
    if (value == null) return '<span class="db-null">NULL</span>';
    if (typeof value === 'object') {
      if (value.kind === 'text') return h(value.preview) + '<span class="db-cut">…(' + h(value.bytes) + ' B)</span>';
      if (value.kind === 'binary') return '<span class="db-binary">BINARY ' + h(value.bytes) + ' B</span>';
      return h(JSON.stringify(value));
    }
    return h(value);
  }
  function fmtRedisLabel(value) {
    if (value == null) return '';
    if (typeof value !== 'object') return String(value);
    if (value.kind === 'binary') return '[BINARY ' + value.bytes + ' B] ' + (value.preview_base64 || '');
    if (value.kind === 'text') return String(value.preview || '') + '…';
    return JSON.stringify(value);
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
    const authUser = Kairo.auth && Kairo.auth.getUser ? Kairo.auth.getUser() : '';
    const authRole = Kairo.auth && Kairo.auth.getRole ? Kairo.auth.getRole() : '';
    const canManage = !authUser || authRole === 'admin';
    view.innerHTML = '<div class="db-page">' +
      '<div class="db-topbar card"><div class="db-source-select"><label>数据源</label><select id="db-source">' + sourceOptions() + '</select></div>' +
      '<span id="db-source-badge" class="db-kind"></span><button class="btn btn-sm" id="db-test">测试连接</button>' +
      (canManage ? '<button class="btn btn-sm" id="db-manage">数据源管理</button>' : '') + '<span class="db-safe">只读模式 · 有行数/时长/并发保护</span></div>' +
      '<div id="db-manager"></div><div id="db-workspace"></div></div>';
    q('db-source').addEventListener('change', function () {
      state.source = state.sources.find(s => s.id === this.value) || null;
      if (state.source) localStorage.setItem(LAST_SOURCE, state.source.id);
      renderWorkspace();
    });
    q('db-test').addEventListener('click', testConnection);
    if (q('db-manage')) q('db-manage').addEventListener('click', function () { state.managing = !state.managing; renderManager(); });
    renderManager();
    renderWorkspace();
  }

  function renderManager(edit) {
    const host = q('db-manager');
    if (!host) return;
    if (!state.managing) { host.innerHTML = ''; return; }
    const s = edit || { kind: 'oracle', port: 1521, oracle_connect_by: 'service_name', query_timeout_seconds: 30, max_rows: 1000, max_result_bytes: 16777216, max_open_connections: 4, max_idle_connections: 1, connection_max_minutes: 10, tls_mode: 'disabled' };
    host.innerHTML = '<div class="card db-manager"><div class="db-manager-head"><h3>' + (s.id ? '编辑数据源' : '新建数据源') + '</h3><select id="db-edit-existing"><option value="">＋ 新建</option>' + state.sources.map(x => '<option value="' + h(x.id) + '"' + (x.id === s.id ? ' selected' : '') + '>' + h(x.name) + '</option>').join('') + '</select></div>' +
      '<div class="db-form-grid">' +
      field('名称', 'dbf-name', s.name || '') + selectField('类型', 'dbf-kind', [['oracle','Oracle 11g+'],['mysql','MySQL'],['redis','Redis']], s.kind) +
      field('主机', 'dbf-host', s.host || '') + field('端口', 'dbf-port', s.port || '', 'number') +
      field('用户名', 'dbf-user', s.username || '') + field(s.has_password ? '密码（留空不改）' : '密码', 'dbf-pass', '', 'password') +
      '<div id="dbf-specific" class="db-form-specific"></div>' +
      selectField('TLS', 'dbf-tls', [['disabled','关闭'],['preferred','优先（仅 MySQL）'],['required','必须且校验证书'],['skip-verify','必须但跳过校验']], s.tls_mode || 'disabled') +
      field('超时（秒）', 'dbf-timeout', s.query_timeout_seconds || 30, 'number') + field('最大行数', 'dbf-rows', s.max_rows || 1000, 'number') +
      field('最大连接', 'dbf-open', s.max_open_connections || 4, 'number') + field('空闲连接', 'dbf-idle', s.max_idle_connections == null ? 1 : s.max_idle_connections, 'number') +
      field('授权用户（逗号；*=全员；留空=仅管理员）', 'dbf-users', (s.allowed_users || []).join(', ')) +
      '</div><div class="db-form-actions"><button class="btn btn-primary" id="dbf-save">保存</button>' + (s.id ? '<button class="btn btn-danger" id="dbf-delete">删除</button>' : '') + '<span class="hint">密码不会写入配置文件或返回页面。</span></div></div>';
    q('db-edit-existing').addEventListener('change', function () { renderManager(state.sources.find(x => x.id === this.value)); });
    q('dbf-kind').addEventListener('change', function () { renderSpecific(s); });
    q('dbf-save').addEventListener('click', function () { saveSource(s); });
    if (q('dbf-delete')) q('dbf-delete').addEventListener('click', function () { deleteSource(s); });
    renderSpecific(s);
  }

  function field(label, id, value, type) {
    return '<label class="db-field"><span>' + h(label) + '</span><input id="' + id + '" type="' + (type || 'text') + '" value="' + h(value) + '"></label>';
  }
  function selectField(label, id, options, value) {
    return '<label class="db-field"><span>' + h(label) + '</span><select id="' + id + '">' + options.map(o => '<option value="' + o[0] + '"' + (o[0] === value ? ' selected' : '') + '>' + h(o[1]) + '</option>').join('') + '</select></label>';
  }
  function renderSpecific(s) {
    const kind = q('dbf-kind').value;
    const target = q('dbf-specific');
    if (kind === 'oracle') target.innerHTML = selectField('连接方式', 'dbf-oracle-by', [['service_name','Service Name'],['sid','SID']], s.oracle_connect_by || 'service_name') + field('Service/SID', 'dbf-service', s.oracle_service || '') + field('客户端字符集', 'dbf-charset', s.oracle_client_charset || '');
    else if (kind === 'mysql') target.innerHTML = field('Database', 'dbf-database', s.database || '');
    else target.innerHTML = field('Redis DB', 'dbf-redis-db', s.redis_db || 0, 'number');
    const preferred = q('dbf-tls').querySelector('option[value="preferred"]');
    preferred.disabled = kind !== 'mysql';
    if (preferred.disabled && q('dbf-tls').value === 'preferred') q('dbf-tls').value = 'required';
    const port = q('dbf-port');
    if (!port.value || ['1521','3306','6379'].indexOf(port.value) >= 0) port.value = kind === 'oracle' ? 1521 : kind === 'mysql' ? 3306 : 6379;
  }

  function sourceFromForm(old) {
    const kind = q('dbf-kind').value;
    const source = {
      name: q('dbf-name').value.trim(), kind: kind, host: q('dbf-host').value.trim(), port: Number(q('dbf-port').value),
      username: q('dbf-user').value.trim(), tls_mode: q('dbf-tls').value,
      query_timeout_seconds: Number(q('dbf-timeout').value), max_rows: Number(q('dbf-rows').value),
      max_result_bytes: (old && old.max_result_bytes) || 16777216, max_open_connections: Number(q('dbf-open').value),
      max_idle_connections: Number(q('dbf-idle').value), connection_max_minutes: (old && old.connection_max_minutes) || 10,
      allowed_users: q('dbf-users').value.split(',').map(x => x.trim()).filter(Boolean)
    };
    if (kind === 'oracle') { source.oracle_connect_by = q('dbf-oracle-by').value; source.oracle_service = q('dbf-service').value.trim(); source.oracle_client_charset = q('dbf-charset').value.trim(); }
    if (kind === 'mysql') source.database = q('dbf-database').value.trim();
    if (kind === 'redis') source.redis_db = Number(q('dbf-redis-db').value);
    return source;
  }

  async function saveSource(old) {
    try {
      const source = sourceFromForm(old);
      const body = { source: source, password: q('dbf-pass').value };
      const method = old && old.id ? 'PUT' : 'POST';
      const path = '/api/database/sources' + (old && old.id ? '/' + encodeURIComponent(old.id) : '');
      const data = await api(method, path, body);
      toast('数据源已保存', 'ok');
      await loadSources(data.source && data.source.id);
      renderManager(data.source);
      refreshSourceSelect(); renderWorkspace();
    } catch (e) { toast('保存失败：' + e.message, 'err'); }
  }

  async function deleteSource(source) {
    if (!source.id || !confirm('确定删除数据源“' + source.name + '”及其已保存密码？')) return;
    try {
      await api('DELETE', '/api/database/sources/' + encodeURIComponent(source.id));
      await loadSources(); toast('数据源已删除', 'ok'); renderManager(); refreshSourceSelect(); renderWorkspace();
    } catch (e) { toast('删除失败：' + e.message, 'err'); }
  }

  function refreshSourceSelect() { const sel = q('db-source'); if (sel) sel.innerHTML = sourceOptions(); }

  async function testConnection() {
    if (!state.source) return toast('请先创建数据源', 'warn');
    const btn = q('db-test'); btn.disabled = true; btn.textContent = '连接中…';
    try {
      const result = await api('POST', '/api/database/sources/' + encodeURIComponent(state.source.id) + '/test', {});
      toast('连接成功 · ' + (result.version || kindLabel(result.kind)) + ' · ' + result.latency_ms + ' ms', 'ok');
    } catch (e) { toast('连接失败：' + e.message, 'err'); }
    finally { btn.disabled = false; btn.textContent = '测试连接'; }
  }

  function renderWorkspace() {
    const host = q('db-workspace'); if (!host) return;
    cancelQuery();
    state.workspaceToken++;
    q('db-source-badge').textContent = state.source ? kindLabel(state.source.kind) : '未配置';
    if (!state.source) { host.innerHTML = '<div class="card empty-state"><div class="empty-text">尚无数据源，请打开“数据源管理”创建 Oracle、MySQL 或 Redis 连接。</div></div>'; return; }
    state.rows = []; state.columns = []; state.summary = null;
    if (state.source.kind === 'redis') renderRedis(host); else renderSQL(host);
  }

  function renderSQL(host) {
    const initial = state.source.kind === 'oracle' ? 'SELECT SYSDATE AS SERVER_TIME FROM DUAL' : 'SELECT NOW() AS server_time';
    host.innerHTML = '<div class="db-sql-layout"><aside class="card db-meta"><div class="db-pane-title">对象浏览器 <button class="btn btn-xs" id="db-meta-refresh">刷新</button></div><select id="db-schema"><option>加载中…</option></select><input id="db-object-search" placeholder="搜索表/视图"><div id="db-objects" class="db-object-list"></div><div id="db-fields" class="db-field-list"></div></aside>' +
      '<main class="db-main"><div class="card db-editor-card"><div class="db-editor-bar"><button class="btn btn-primary" id="db-run">执行 Ctrl/⌘+Enter</button><button class="btn" id="db-cancel" disabled>取消</button><label>最多 <input id="db-max-rows" type="number" min="1" max="' + state.source.max_rows + '" value="' + Math.min(1000, state.source.max_rows) + '"> 行</label><button class="btn" id="db-export" disabled>流式导出 CSV</button><span id="db-query-status"></span></div><textarea id="db-sql" class="db-sql-editor" spellcheck="false">' + h(initial) + '</textarea></div>' +
      '<div class="card db-results"><div id="db-result-meta" class="db-result-meta">等待执行查询</div><div id="db-result-grid" class="db-result-grid"></div></div></main></div>';
    q('db-run').addEventListener('click', runQuery); q('db-cancel').addEventListener('click', cancelQuery); q('db-export').addEventListener('click', exportCSV);
    q('db-sql').addEventListener('keydown', function (e) { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); runQuery(); } });
    q('db-meta-refresh').addEventListener('click', () => loadSchemas(true)); q('db-schema').addEventListener('change', loadObjects);
    q('db-object-search').addEventListener('input', debounce(loadObjects, 250)); loadSchemas();
  }

  async function loadSchemas(refresh) {
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/metadata/schemas?source_id=' + encodeURIComponent(source.id) + (refresh === true ? '&refresh=1' : ''));
      const schema = q('db-schema');
      if (token !== state.workspaceToken || !schema) return;
      schema.innerHTML = (data.schemas || []).map(x => '<option>' + h(x.name) + '</option>').join(''); loadObjects();
    } catch (e) { const objects = q('db-objects'); if (token === state.workspaceToken && objects) objects.textContent = '元数据加载失败：' + e.message; }
  }
  async function loadObjects() {
    const token = state.workspaceToken, source = state.source;
    const schema = q('db-schema') && q('db-schema').value; if (!schema) return;
    try {
      const search = q('db-object-search');
      const data = await api('GET', '/api/database/metadata/objects?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&search=' + encodeURIComponent(search ? search.value : ''));
      const objects = q('db-objects');
      if (token !== state.workspaceToken || !objects) return;
      objects.innerHTML = (data.objects || []).map(x => '<button class="db-object" data-name="' + h(x.name) + '"><span>' + h(x.name) + '</span><small>' + h(x.type) + '</small></button>').join('') || '<div class="hint">没有匹配对象</div>';
      Array.from(objects.querySelectorAll('.db-object')).forEach(btn => { btn.onclick = () => loadFields(schema, btn.dataset.name); btn.ondblclick = () => insertSelect(schema, btn.dataset.name); });
    } catch (e) { const objects = q('db-objects'); if (token === state.workspaceToken && objects) objects.textContent = '对象加载失败：' + e.message; }
  }
  async function loadFields(schema, object) {
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/metadata/fields?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&object=' + encodeURIComponent(object));
      const fields = q('db-fields');
      if (token !== state.workspaceToken || !fields) return;
      fields.innerHTML = '<div class="db-selected-object">' + h(object) + '</div>' + (data.fields || []).map(x => '<div class="db-field-row"><span>' + h(x.name) + '</span><small>' + h(x.definition || x.data_type) + '</small></div>').join('');
    } catch (e) { if (token === state.workspaceToken) toast('字段加载失败：' + e.message, 'err'); }
  }
  function insertSelect(schema, object) {
    const quote = state.source.kind === 'mysql' ? '`' : '"';
    q('db-sql').value = 'SELECT *\nFROM ' + quote + schema + quote + '.' + quote + object + quote;
  }

  async function runQuery() {
    if (state.controller) return;
    const sql = q('db-sql').value.trim(); if (!sql) return toast('请输入 SQL', 'warn');
    const maxRows = Number(q('db-max-rows').value);
    state.lastSQL = sql; state.lastMaxRows = maxRows;
    state.rows = []; state.columns = []; state.summary = null; renderResultGrid();
    state.controller = new AbortController(); q('db-run').disabled = true; q('db-cancel').disabled = false; q('db-export').disabled = true; q('db-query-status').textContent = '查询中…';
    try {
      const response = await fetch('/api/database/query', { method: 'POST', credentials: 'same-origin', signal: state.controller.signal, headers: {'Content-Type':'application/json'}, body: JSON.stringify({ source_id: state.source.id, sql: sql, max_rows: maxRows }) });
      if (!response.ok) { const body = await response.json().catch(() => ({})); throw new Error(body.error || 'HTTP ' + response.status); }
      const reader = response.body.getReader(); const decoder = new TextDecoder(); let pending = '';
      while (true) {
        const part = await reader.read(); pending += decoder.decode(part.value || new Uint8Array(), {stream: !part.done});
        const lines = pending.split('\n'); pending = lines.pop();
        for (const line of lines) if (line.trim()) consumeQueryEvent(JSON.parse(line));
        if (part.done) break;
      }
      if (pending.trim()) consumeQueryEvent(JSON.parse(pending));
    } catch (e) { if (e.name !== 'AbortError') toast('查询失败：' + e.message, 'err'); else toast('查询已取消', 'warn'); }
    finally {
      state.controller = null;
      const run = q('db-run'), cancel = q('db-cancel'), exportBtn = q('db-export');
      if (run) run.disabled = false;
      if (cancel) cancel.disabled = true;
      if (exportBtn) exportBtn.disabled = state.rows.length === 0;
    }
  }
  function consumeQueryEvent(event) {
    if (event.type === 'meta') { state.columns = event.columns || []; renderResultGrid(true); }
    else if (event.type === 'rows') { state.rows.push.apply(state.rows, event.rows || []); renderResultGrid(false); }
    else if (event.type === 'summary') { state.summary = event.summary; const status = q('db-query-status'); if (status) status.textContent = event.summary.rows + ' 行 · ' + event.summary.elapsed_ms + ' ms' + (event.summary.truncated ? ' · 已截断' : ''); }
    else if (event.type === 'error') throw new Error(event.error || '查询失败');
  }
  function cancelQuery() { if (state.controller) state.controller.abort(); }

  function renderResultGrid(reset) {
    const grid = q('db-result-grid'); if (!grid) return;
    q('db-result-meta').textContent = state.columns.length ? state.columns.length + ' 列 · 已接收 ' + state.rows.length + ' 行' : '等待查询结果';
    if (!state.columns.length) { grid.innerHTML = ''; return; }
    let scroll = grid.firstChild;
    if (reset || !scroll) {
      grid.innerHTML = '<div class="db-table-scroll"><table class="table db-table"><thead><tr><th>#</th>' + state.columns.map(c => '<th title="' + h(c.database_type || '') + '">' + h(c.name) + '</th>').join('') + '</tr></thead><tbody id="db-result-body"></tbody></table></div>';
      scroll = grid.firstChild;
      scroll.addEventListener('scroll', () => renderVisibleRows(scroll));
    }
    renderVisibleRows(scroll);
  }
  function renderVisibleRows(scroll) {
    const body = q('db-result-body'); if (!body) return;
    const rowHeight = 31, buffer = 12, visible = Math.ceil(scroll.clientHeight / rowHeight);
    const start = Math.max(0, Math.floor(scroll.scrollTop / rowHeight) - buffer), end = Math.min(state.rows.length, start + visible + buffer * 2);
    let html = start ? '<tr class="db-spacer"><td colspan="' + (state.columns.length + 1) + '" style="height:' + (start * rowHeight) + 'px"></td></tr>' : '';
    for (let i = start; i < end; i++) html += '<tr><td class="num">' + (i + 1) + '</td>' + state.rows[i].map(v => '<td title="双击复制">' + fmtCell(v) + '</td>').join('') + '</tr>';
    if (end < state.rows.length) html += '<tr class="db-spacer"><td colspan="' + (state.columns.length + 1) + '" style="height:' + ((state.rows.length - end) * rowHeight) + 'px"></td></tr>';
    body.innerHTML = html;
    Array.from(body.querySelectorAll('tr:not(.db-spacer) td:not(.num)')).forEach(td => td.ondblclick = () => { copyToClipboard(td.textContent); toast('已复制单元格', 'ok'); });
  }

  async function exportCSV() {
    if (!state.lastSQL || state.controller) return;
    const safeName = (state.source.name || 'query').replace(/[\\/:*?"<>|]/g, '_');
    const filename = safeName + '-' + new Date().toISOString().replace(/[:.]/g, '-') + '.csv';
    const button = q('db-export'); if (button) button.disabled = true;
    let fileHandle = null;
    try {
      if (window.showSaveFilePicker) fileHandle = await window.showSaveFilePicker({ suggestedName: filename, types: [{ description: 'CSV 文件', accept: {'text/csv':['.csv']} }] });
      const response = await fetch('/api/database/export', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ source_id: state.source.id, sql: state.lastSQL, max_rows: state.lastMaxRows }) });
      if (!response.ok) { const body = await response.json().catch(() => ({})); throw new Error(body.error || 'HTTP ' + response.status); }
      if (fileHandle && response.body) {
        const writable = await fileHandle.createWritable();
        await response.body.pipeTo(writable);
      } else {
        const blob = await response.blob();
        const a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = filename; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
      }
      toast('CSV 导出完成', 'ok');
    } catch (e) {
      if (e.name !== 'AbortError') toast('CSV 导出失败：' + e.message, 'err');
    } finally { if (button) button.disabled = state.rows.length === 0; }
  }

  function renderRedis(host) {
    state.cursor = 0;
    host.innerHTML = '<div class="db-redis-layout"><div class="card db-redis-keys"><div class="db-editor-bar"><input id="db-redis-pattern" value="*" placeholder="Key pattern，例如 order:*"><button class="btn btn-primary" id="db-redis-scan">SCAN</button><button class="btn" id="db-redis-next">下一批</button></div><div id="db-redis-list" class="db-redis-list"></div></div><div class="card db-redis-detail"><div class="db-pane-title">Key 详情</div><pre id="db-redis-value">选择左侧 Key 查看类型、TTL 和内容</pre></div></div>';
    q('db-redis-scan').onclick = () => { state.cursor = 0; scanRedis(true); }; q('db-redis-next').onclick = () => scanRedis(false); scanRedis(true);
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
      if (reset) list.innerHTML = rows || '<div class="hint">没有匹配 Key</div>'; else list.insertAdjacentHTML('beforeend', rows);
      Array.from(list.querySelectorAll('.db-redis-key')).forEach(btn => btn.onclick = () => loadRedisKey(btn.dataset.key));
    } catch (e) { if (token === state.workspaceToken) toast('Redis SCAN 失败：' + e.message, 'err'); }
  }
  async function loadRedisKey(keyBase64) {
    const token = state.workspaceToken, source = state.source;
    try { const data = await api('GET', '/api/database/redis/key?source_id=' + encodeURIComponent(source.id) + '&key_base64=' + encodeURIComponent(keyBase64)); const value = q('db-redis-value'); if (token === state.workspaceToken && value) value.textContent = JSON.stringify(data.result, null, 2); }
    catch (e) { if (token === state.workspaceToken) toast('读取 Key 失败：' + e.message, 'err'); }
  }

  function debounce(fn, wait) { let timer; return function () { clearTimeout(timer); timer = setTimeout(fn, wait); }; }

  async function renderDatabase(view) {
    try { await loadSources(); render(view); }
    catch (e) { view.innerHTML = '<div class="card text-err">数据库工作台加载失败：' + h(e.message) + '</div>'; }
  }
  Kairo.state.routes.database = renderDatabase;
  Kairo.state.routeNames.database = '数据库工作台';
  Kairo.state.routeSubs.database = 'Oracle 11g / MySQL / Redis · 安全只读查询';
  Kairo.database = { cancel: cancelQuery };
})();
