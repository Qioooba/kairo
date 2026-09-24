const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

// Extract database.js logic for unit testing
const dbJs = fs.readFileSync(path.join(__dirname, '../web/pages/database.js'), 'utf8');
const featJs = fs.readFileSync(path.join(__dirname, '../web/workbench/database-features.js'), 'utf8');

// 把真实的 database-features.js 载入沙箱（观察者保持 loading，不会执行 start()），
// 让绑定参数断言打在真实实现上，而不是正则抽取出来的源码片段上。
function loadDatabaseFeatures() {
  const editor = {
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    dataset: {},
    addEventListener() {},
    removeEventListener() {},
    focus() {},
    setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  };
  const document = {
    readyState: 'loading',
    addEventListener() {},
    getElementById(id) { return id === 'db-sql' ? editor : null; },
    querySelectorAll() { return []; },
    body: {}
  };
  const window = {
    Kairo: { core: { escapeHtml(value) { return String(value); } } },
    addEventListener() {}
  };
  vm.runInNewContext(codeOf('web/workbench/statement-model.js'),
    { window, document, console, Set, URLSearchParams, MutationObserver: function () {}, navigator: {}, location: { hash: '' }, setTimeout, clearTimeout });
  vm.runInNewContext(codeOf('web/workbench/database-features.js'),
    { window, document, console, Set, URLSearchParams, MutationObserver: function () {}, navigator: {}, location: { hash: '' }, setTimeout, clearTimeout });
  const features = window.Kairo.databaseFeatures;
  features.__editor = editor;
  return features;
}

function codeOf(relPath) {
  return fs.readFileSync(path.join(__dirname, '..', relPath), 'utf8');
}

// 从真实源文件里按大括号配平切出一个函数定义（跳过字符串/模板/注释里的括号）。
// 与仓库既有约定一致：直接消费真实实现，而不是把逻辑重写一遍当 mock。
function extractFunction(src, name) {
  const at = src.indexOf('\n  function ' + name + '(');
  assert.ok(at >= 0, '未在真实源码里找到函数 ' + name);
  let depth = 0, quote = null, escaped = false, line = false, block = false;
  for (let i = src.indexOf('{', at); i < src.length; i++) {
    const ch = src[i], next = src[i + 1];
    if (line) { if (ch === '\n') line = false; continue; }
    if (block) { if (ch === '*' && next === '/') { block = false; i++; } continue; }
    if (quote) {
      if (escaped) { escaped = false; continue; }
      if (ch === '\\') { escaped = true; continue; }
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') { quote = ch; continue; }
    if (ch === '/' && next === '/') { line = true; i++; continue; }
    if (ch === '/' && next === '*') { block = true; i++; continue; }
    if (ch === '{') depth++;
    else if (ch === '}') { depth--; if (!depth) return src.slice(at + 1, i + 1); }
  }
  throw new Error('无法解析函数 ' + name);
}

// DBUI-05 夹具：切出 database.js 的真实控制流
// （bindSession / consumeQueryEvent / refreshVisibleResult / renderResult / renderGridView），
// 只对 DOM 与渲染叶子打桩。renderResult 计数包装，paintGridRows 计增量重绘，
// 网格容器的 scroll 监听计虚拟滚动绑定 —— 这三点正是缺陷 A 的可观测面。
function makeGridHarness(options) {
  const cfg = options || {};
  const calls = { renderResult: 0, incrementalPaint: 0, fullRebuild: 0 };
  const listeners = { scroll: 0 };
  let clock = 1000;
  const performance = { now() { return (clock += 7); } };
  const state = {
    activeId: 1, rows: [], columns: [], controller: null, summary: null, resultMode: 'grid',
    selectedRow: 0, selectedCol: 0, colSelected: -1, selectedCols: new Set(), lastColSelected: -1,
    localFilter: '', hiddenColumns: new Set(), sort: null, lastError: null, plan: [], gridReady: false,
    dirtyCells: {}, isEditMode: false, lastSQL: '', lastMaxRows: 0
  };
  const gridView = { indexes: [], visible: [], start: -1, end: -1, length: -1, head: null, tail: null, mid: null, raf: 0, virtualized: false };
  const bodyEl = { innerHTML: '', addEventListener() {}, querySelector() { return null; } };
  const scrollEl = {
    scrollTop: 0, scrollLeft: 0, clientHeight: 620, clientWidth: 900, isConnected: true, style: {},
    addEventListener(type) { if (type === 'scroll') listeners.scroll++; }
  };
  let gridHTML = '';
  const gridEl = {
    get innerHTML() { return gridHTML; },
    set innerHTML(value) { gridHTML = String(value); calls.fullRebuild++; },
    querySelector(selector) { return selector === '.db-table-scroll' ? scrollEl : null; }
  };
  const Kairo = { databaseFeatures: { recordQueryResult() {} } };
  function q(idValue) {
    if (idValue === 'db-result-grid') return gridEl;
    if (idValue === 'db-result-body') return bodyEl;
    return null;
  }
  function h(value) { return String(value == null ? '' : value); }
  function fmtCell(value) { return h(value); }
  function gridColumnWidth() { return 120; }
  function isColSelected() { return false; }
  function cancelGridPaint() {}
  function bindGridBody() {}
  function bindGridHeaders() {}
  function bindScrollSync() {}
  function applyGridHeight() {}
  function scheduleGridPaint() {}
  function paintGridRows() { calls.incrementalPaint++; }
  function updateResultMeta() {}
  function updateDatabasePager() {}
  function updateTransactionControls() {}
  function showQueryMessage() {}
  function renderTabs() {}
  function toast() {}
  function renderPlanView() {}
  function renderRecordView() {}
  function filteredRows() { return state.rows.map(function (_, i) { return i; }); }
  const gridResultNeedsVirtual = eval('(' + extractFunction(dbJs, 'gridResultNeedsVirtual') + ')');
  const setGridSlice = eval('(' + extractFunction(dbJs, 'setGridSlice') + ')');
  const visibleColumns = eval('(' + extractFunction(dbJs, 'visibleColumns') + ')');
  const normalizeEditPlan = eval('(' + extractFunction(dbJs, 'normalizeEditPlan') + ')');
  const editPlanCapabilityChanged = eval('(' + extractFunction(dbJs, 'editPlanCapabilityChanged') + ')');
  const renderGridView = eval('(' + extractFunction(dbJs, 'renderGridView') + ')');
  const renderResultReal = eval('(' + extractFunction(dbJs, 'renderResult') + ')');
  function renderResult() { calls.renderResult++; return renderResultReal(); }
  const refreshVisibleResult = eval('(' + extractFunction(dbJs, 'refreshVisibleResult') + ')');
  const bindSessionReal = eval('(' + extractFunction(dbJs, 'bindSession') + ')');
  // legacyBindSession 复刻修复前的行为：bindSession 无条件把 gridReady 打回 false。
  const bindSession = cfg.legacyBindSession
    ? function (s) { return bindSessionReal(s, { invalidateGrid: true }); }
    : bindSessionReal;
  const consumeQueryEvent = eval('(' + extractFunction(dbJs, 'consumeQueryEvent') + ')');
  return {
    state, gridView, calls, listeners, bindSession, consumeQueryEvent, renderGridView,
    gridResultNeedsVirtual, normalizeEditPlan, editPlanCapabilityChanged
  };
}

// DBUI-06 夹具：带可计数的 localStorage 载入真实 database-features.js。
// kairo:database:history:v2 的 getItem = 整库读（localStorage + JSON.parse），setItem = 整库写。
function loadHistoryHarness() {
  const HISTORY_KEY = 'kairo:database:history:v2';
  const store = Object.create(null);
  let historyReads = 0, historyWrites = 0;
  const localStorage = {
    getItem(key) {
      if (key === HISTORY_KEY) historyReads++;
      return Object.prototype.hasOwnProperty.call(store, key) ? store[key] : null;
    },
    setItem(key, value) {
      if (key === HISTORY_KEY) historyWrites++;
      store[key] = String(value);
    },
    removeItem(key) { delete store[key]; }
  };
  function FakeMutationObserver() {
    this.observe = function () {};
    this.disconnect = function () {};
    this.takeRecords = function () { return []; };
  }
  const editor = {
    value: '', selectionStart: 0, selectionEnd: 0, dataset: {},
    addEventListener() {}, removeEventListener() {}, focus() {}, setSelectionRange() {}
  };
  const doc = {
    readyState: 'loading', body: {}, hidden: false,
    addEventListener() {}, removeEventListener() {},
    createEvent() { return { initCustomEvent() {} }; },
    getElementById(idValue) { return idValue === 'db-sql' ? editor : null; },
    querySelectorAll() { return []; }
  };
  const win = {
    Kairo: { core: { escapeHtml(v) { return String(v); } } },
    addEventListener() {}, removeEventListener() {}, dispatchEvent() { return {}; },
    requestAnimationFrame(fn) { return setTimeout(fn, 0); }
  };
  const sandbox = {
    window: win, document: doc, console, localStorage, Set, Map, URLSearchParams, JSON, Date, Math, isFinite,
    MutationObserver: FakeMutationObserver, navigator: {}, location: { hash: '' }, setTimeout, clearTimeout
  };
  vm.runInNewContext(codeOf('web/workbench/statement-model.js'), sandbox);
  vm.runInNewContext(codeOf('web/workbench/database-features.js'), sandbox);
  const features = win.Kairo.databaseFeatures;
  return {
    features,
    editor,
    seed(value) { store[HISTORY_KEY] = value; },
    resetCounts() { historyReads = 0; historyWrites = 0; },
    reads() { return historyReads; },
    writes() { return historyWrites; },
    disk() { return JSON.parse(store[HISTORY_KEY] || '{}'); }
  };
}

test('SQL Editor Performance & Safeguards', async (t) => {
  await t.test('isLargeSQL correctly flags large character length and line count', () => {
    const m = dbJs.match(/function isLargeSQL\(text\) \{[\s\S]*?\n  \}/);
    assert.ok(m, 'isLargeSQL definition found');
    const MAX_SQL_HIGHLIGHT_CHARS = 120000;
    const MAX_SQL_HIGHLIGHT_LINES = 2000;
    const isLargeSQL = eval('(' + m[0] + ')');

    // Small query
    assert.strictEqual(isLargeSQL('SELECT 1 FROM dual;'), false);

    // Moderate 500 lines
    const midLines = Array.from({ length: 500 }, (_, i) => `SELECT ${i};`).join('\n');
    assert.strictEqual(isLargeSQL(midLines), false);

    // Over 2000 lines
    const overLines = Array.from({ length: 2005 }, (_, i) => `SELECT ${i};`).join('\n');
    assert.strictEqual(isLargeSQL(overLines), true);

    // Over 120,000 characters
    const overChars = 'SELECT ' + 'x'.repeat(125000) + ' FROM dual;';
    assert.strictEqual(isLargeSQL(overChars), true);
  });

  await t.test('matchBrackets early exits when cursor is not adjacent to bracket', () => {
    const mKw = dbJs.match(/const SQL_KEYWORDS = [^\n]+;/);
    const mTok = dbJs.match(/function tokenizeSQL\(src\) \{[\s\S]*?\n  \}/);
    const mMatch = dbJs.match(/function matchBrackets\(text, cursor\) \{[\s\S]*?\n  \}/);
    assert.ok(mKw && mTok && mMatch, 'Extracted parser functions');

    eval(mKw[0].replace('const SQL_KEYWORDS', 'global.SQL_KEYWORDS'));
    const tokenizeSQL = eval('(' + mTok[0] + ')');
    const matchBrackets = eval('(' + mMatch[0] + ')');

    const sql = 'SELECT col1, col2 FROM (SELECT id FROM users WHERE status = 1)';
    // Cursor at word 'users' (not adjacent to any bracket)
    const cursor = sql.indexOf('users');
    const res = matchBrackets(sql, cursor);
    assert.strictEqual(res, null, 'Should return null immediately without bracket parsing');

    // Cursor right after '('
    const openParen = sql.indexOf('(');
    const resOpen = matchBrackets(sql, openParen + 1);
    assert.ok(resOpen, 'Should match opening parenthesis');
    assert.strictEqual(resOpen.open, openParen);
  });

  await t.test('highlightSQL gracefully falls back to plain escaped text for large SQL', () => {
    const mKw = dbJs.match(/const SQL_KEYWORDS = [^\n]+;/);
    const mTok = dbJs.match(/function tokenizeSQL\(src\) \{[\s\S]*?\n  \}/);
    const mHl = dbJs.match(/function highlightSQL\(src, match\) \{[\s\S]*?\n  \}/);
    const mLarge = dbJs.match(/function isLargeSQL\(text\) \{[\s\S]*?\n  \}/);
    const MAX_SQL_HIGHLIGHT_CHARS = 120000;
    const MAX_SQL_HIGHLIGHT_LINES = 2000;
    global.h = function (s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); };

    eval(mKw[0].replace('const SQL_KEYWORDS', 'global.SQL_KEYWORDS'));
    global.tokenizeSQL = eval('(' + mTok[0] + ')');
    const isLargeSQL = eval('(' + mLarge[0] + ')');
    const highlightSQL = eval('(' + mHl[0] + ')');

    // Small query gets formatted with highlight spans
    const smallSql = 'SELECT * FROM users WHERE id = 1;';
    const smallHl = highlightSQL(smallSql, null);
    assert.ok(smallHl.includes('db-sql-kw'), 'Small SQL should have syntax class');

    // Over-threshold query returns plain escaped text (no span explosion)
    const largeSql = Array.from({ length: 2500 }, (_, i) => `SELECT col_${i} FROM table_${i};`).join('\n');
    const largeHl = highlightSQL(largeSql, null);
    assert.ok(!largeHl.includes('<span class='), 'Large SQL must not contain span tags');
    assert.ok(largeHl.includes('SELECT col_0 FROM table_0;'), 'Content must be preserved');
  });

  // NOTE: 这个用例原来断言“超长 SQL 返回空参数”是正确行为，实际上它固化了 DBUI-04
  // 的功能回退（高亮降级把执行所需的绑定参数扫描一起关掉）。现已按正确行为重新锚定：
  // 高亮可以降级，绑定参数扫描不可以。
  await t.test('bind parameters must survive the large-text highlight threshold (DBUI-04)', () => {
    const features = loadDatabaseFeatures();
    const editor = features.__editor;

    // 普通参数化查询
    const sql = 'SELECT * FROM orders WHERE id = :order_id AND user_id = :user_id AND status = ?';
    const params = features.findParameters(sql);
    assert.strictEqual(params.length, 3);
    assert.strictEqual(params[0].name, 'order_id');
    assert.strictEqual(params[1].name, 'user_id');
    assert.strictEqual(params[2].name, '1');

    // 短查询 + 220KB 尾部注释：参数扫描不得被高亮阈值禁用
    const shortQuery = 'SELECT :id FROM dual;';
    const hugeSql = shortQuery + '\n/*' + 'x'.repeat(220000) + '*/';
    assert.ok(hugeSql.length > 220000, '样本必须超过高亮阈值');

    const hugeParams = features.findParameters(hugeSql);
    // 注意：vm 沙箱里的数组原型与测试 realm 不同，deepStrictEqual 会因原型不一致而失败。
    assert.strictEqual(hugeParams.map(p => p.name).join(','), 'id',
      'DBUI-04: 超过高亮阈值的文本仍必须扫描出绑定参数，实际 ' + JSON.stringify(hugeParams));

    // 执行路径：光标位于首条短语句时，绑定必须保留并随请求发送
    editor.value = hugeSql;
    editor.selectionStart = 6;
    editor.selectionEnd = 6;
    features.setBindings({ id: '123' });
    const bound = features.getBoundParameters();
    assert.strictEqual(bound.map(p => p.name).join(','), 'id',
      'DBUI-04: 执行路径必须保留首条语句的绑定参数，实际 ' + JSON.stringify(bound));
    assert.strictEqual(bound[0].value, '123');
    assert.strictEqual(features.getBindings().id, '123',
      'DBUI-04: 扫描可用时也不得删除仍然存在的绑定；扫描未完成时更不得破坏性删除');

    // 高亮降级与参数扫描互相独立：统一入口仍返回状态与参数
    const scan = features.scanParameters(hugeSql);
    assert.strictEqual(scan.status, 'ready');
    assert.strictEqual(scan.parameters.map(p => p.name).join(','), 'id');
  });

  await t.test('parameter scanning keeps the binding cache while a scan is not ready (DBUI-04)', () => {
    const features = loadDatabaseFeatures();
    const editor = features.__editor;

    editor.value = 'SELECT :a FROM dual;';
    editor.selectionStart = 7;
    editor.selectionEnd = 7;
    features.setBindings({ a: '1', b: '2' });

    // 未完成的扫描结果不得触发破坏性裁剪
    features.pruneBindingsAgainst({ status: 'pending', parameters: [], complete: false });
    assert.strictEqual(Object.keys(features.getBindings()).sort().join(','), 'a,b',
      '扫描未完成时必须保留全部绑定');

    features.pruneBindingsAgainst({ status: 'error', parameters: [], complete: false });
    assert.strictEqual(Object.keys(features.getBindings()).sort().join(','), 'a,b',
      '扫描失败时必须保留全部绑定');

    // 完成的扫描结果才允许删除确实不存在的参数
    features.pruneBindingsAgainst({ status: 'ready', parameters: [{ name: 'a' }], complete: true });
    assert.strictEqual(Object.keys(features.getBindings()).sort().join(','), 'a',
      '完成的扫描结果允许裁剪已删除的参数');
  });
});

// ---------------------------------------------------------------------------
// DBUI-05: 结果网格增量重绘（缺陷 A）
// 断言直接跑真实的 consumeQueryEvent / bindSession / refreshVisibleResult /
// renderResult / renderGridView 控制流，只对 DOM 与渲染叶子打桩。
// ---------------------------------------------------------------------------
test('Result grid incremental repaint (DBUI-05)', async (t) => {
  const PLAN = {
    result_id: 'rid-1', schema: 'S', table: 'T', identity_policy: 'pk',
    can_insert: false, can_update: false, can_delete: false, has_top_level_order: false, columns: []
  };
  function columnsOf(count) {
    return Array.from({ length: count }, (_, i) => ({ name: 'c' + i, database_type: 'VARCHAR2' }));
  }
  function rowsOf(colCount, count, offset) {
    return Array.from({ length: count }, (_, i) =>
      Array.from({ length: colCount }, (_, c) => 'v' + (offset + i) + '_' + c));
  }
  function newSession() {
    return {
      id: 1, runSeq: 3, rows: [], columns: [], summary: null, status: '就绪', runId: 'run-1',
      editPlan: null, type: 'sql', sourceId: 'src-1', sql: 'SELECT * FROM t',
      lastSQL: 'SELECT * FROM t', executedSQL: 'SELECT * FROM t', startTime: 500,
      firstRowsTime: 0, firstPaintTime: 0, resultId: '', page: 1, pageSize: 20, lastMaxRows: 20,
      resultMode: 'grid', selectedRow: 0, selectedCol: 0, colSelected: -1, selectedCols: new Set(),
      lastColSelected: -1, localFilter: '', hiddenColumns: new Set(), sort: null, plan: [],
      gridReady: false, controller: null, dirtyCells: {}, isEditMode: false, lastError: null
    };
  }
  function meta(h, s, colCount) {
    h.consumeQueryEvent(s, s.runSeq, { type: 'meta', columns: columnsOf(colCount), result_id: 'rid-1', edit_plan: PLAN });
  }
  // 1000+ 行结果按 5 + 10×100 分包流式到达（服务端真实节奏）。
  function streamStreamed(legacy) {
    const h = makeGridHarness({ legacyBindSession: legacy });
    const s = newSession();
    h.state.activeId = s.id;
    meta(h, s, 5);
    h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(5, 5, 0) });
    for (let i = 0; i < 10; i++) {
      h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(5, 100, 5 + i * 100) });
    }
    h.consumeQueryEvent(s, s.runSeq, {
      type: 'summary',
      summary: { rows: 1005, elapsed_ms: 120, prep_ms: 8, result_id: 'rid-1', edit_plan: PLAN }
    });
    return { h: h, s: s };
  }

  await t.test('5 + 10×100 行分包：整表重建从 13 次降到 2 次，其余全部增量重绘', () => {
    const fixed = streamStreamed(false);
    assert.strictEqual(fixed.s.rows.length, 1005, '全部行都必须进入 state.rows');
    // 修复前：meta + 11 个 rows 分包 + summary = 13 次 renderResult（整表重建）。
    const legacy = streamStreamed(true);
    assert.strictEqual(legacy.h.calls.renderResult, 13, '修复前基准：13 次整表重建');
    assert.strictEqual(legacy.h.calls.fullRebuild, 13, '修复前每个事件都重建整个网格容器（grid.innerHTML）');
    // 修复后：meta 建 DOM 1 次 + 小结果集长过阈值时强制重建 1 次。
    assert.strictEqual(fixed.h.calls.renderResult, 2,
      '修复后整表重建必须只有 2 次（meta + 小→大阈值切换），实际 ' + fixed.h.calls.renderResult);
    assert.strictEqual(fixed.h.calls.fullRebuild, 2, '网格容器只允许被替换 2 次');
    assert.strictEqual(fixed.h.calls.incrementalPaint, 12, '其余 12 个事件必须走增量重绘（只重绘可见窗口）');
    assert.strictEqual(fixed.h.listeners.scroll, 1, '虚拟滚动监听只能在重建时绑定一次');
    assert.strictEqual(fixed.h.gridView.virtualized, true);
    assert.strictEqual(fixed.h.state.gridReady, true);
  });

  await t.test('小结果集直出后越阈值必须整体重建，把虚拟滚动监听补上', () => {
    const h = makeGridHarness();
    const s = newSession();
    h.state.activeId = s.id;
    meta(h, s, 5);
    h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(5, 12, 0) });
    assert.strictEqual(h.gridView.virtualized, false, '12 行 × 5 列属于小结果集直出');
    assert.strictEqual(h.listeners.scroll, 0, '小结果集直出路径不绑定虚拟滚动监听');

    const before = h.calls.renderResult;
    h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(5, 20, 12) }); // 32 行 > 25
    assert.strictEqual(h.calls.renderResult, before + 1, '越过阈值必须整体重建恰好一次');
    assert.strictEqual(h.listeners.scroll, 1, '重建时必须补绑虚拟滚动监听');
    assert.strictEqual(h.gridView.virtualized, true);

    const after = h.calls.renderResult;
    h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(5, 50, 32) });
    assert.strictEqual(h.calls.renderResult, after, '越阈值之后不得再次整体重建');
    assert.strictEqual(h.gridView.virtualized, true, '增量重绘不得把网格退回非虚拟化');
  });

  await t.test('阈值同时看单元格总数：8×60=480 直出，9×60=540 触发重建', () => {
    const h = makeGridHarness();
    const s = newSession();
    h.state.activeId = s.id;
    meta(h, s, 60);
    h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(60, 8, 0) });
    assert.strictEqual(h.gridView.virtualized, false, '8 行 × 60 列 = 480 单元格，仍走直出');
    assert.strictEqual(h.listeners.scroll, 0);

    const before = h.calls.renderResult;
    h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(60, 1, 8) }); // 9 × 60 = 540
    assert.strictEqual(h.calls.renderResult, before + 1, '单元格总数过阈值也必须重建');
    assert.strictEqual(h.listeners.scroll, 1);
    assert.strictEqual(h.gridView.virtualized, true);
  });

  await t.test('阈值判定与真实源码同源（rowCount<=25 且 rowCount*colCount<=500 走直出）', () => {
    const h = makeGridHarness();
    assert.strictEqual(h.gridResultNeedsVirtual(25, 20), false, '25×20=500 仍是直出');
    assert.strictEqual(h.gridResultNeedsVirtual(0, 0), false);
    assert.strictEqual(h.gridResultNeedsVirtual(26, 1), true, '行数过 25 必须虚拟化');
    assert.strictEqual(h.gridResultNeedsVirtual(25, 21), true, '单元格过 500 必须虚拟化');
  });

  await t.test('summary 收尾：计划未变不得再整表重建，计划翻转才允许重建一次', () => {
    function streamWithSummary(summaryPlanFactory) {
      const h = makeGridHarness();
      const s = newSession();
      h.state.activeId = s.id;
      meta(h, s, 5);
      h.consumeQueryEvent(s, s.runSeq, { type: 'rows', rows: rowsOf(5, 200, 0) });
      const beforeSummary = h.calls.renderResult;
      const summary = { rows: 200, elapsed_ms: 90, result_id: 'rid-1' };
      if (summaryPlanFactory !== undefined) summary.edit_plan = summaryPlanFactory();
      h.consumeQueryEvent(s, s.runSeq, { type: 'summary', summary: summary });
      return { h: h, before: beforeSummary, after: h.calls.renderResult };
    }
    // meta 与 summary 计划完全一致（服务端已同步解析，这是常态）
    const same = streamWithSummary(() => Object.assign({}, PLAN));
    assert.strictEqual(same.after, same.before, '计划未变时 summary 不得再整表重建');
    // summary 不带 edit_plan（旧服务端）同样不得重建
    const absent = streamWithSummary(undefined);
    assert.strictEqual(absent.after, absent.before, 'summary 未携带 edit_plan 时不得重建');
    // 编辑能力翻转（只读 → 可更新）才允许重建一次
    const flipped = streamWithSummary(() => Object.assign({}, PLAN, { can_update: true }));
    assert.strictEqual(flipped.after, flipped.before + 1, '编辑能力翻转时必须恰好重建一次');
  });

  await t.test('状态行只保留一个数字，悬浮提示区分各段耗时', () => {
    const run = streamStreamed(false);
    assert.strictEqual(run.s.status, '1005 行 · 120 ms', '状态行仍然只有 elapsed_ms 一个数字');
    assert.ok(!/\d+\s*ms[\s\S]*\d+\s*ms/.test(run.s.status), '状态行不得出现第二个数字');
    assert.ok(run.s.statusTitle.startsWith('服务端 SQL 执行 120 ms'), '实际 ' + run.s.statusTitle);
    assert.ok(run.s.statusTitle.includes('元数据准备 8 ms'), 'prep_ms 必须出现在悬浮提示里');
    assert.ok(run.s.statusTitle.includes('端到端首包 '), '必须区分端到端首包');
    assert.ok(run.s.statusTitle.includes('首帧上屏 '), '必须给出首帧上屏');
    assert.ok(run.s.statusTitle.includes('总 '), '必须给出总耗时');
    assert.ok(run.s.firstPaintTime > run.s.firstRowsTime,
      '首帧上屏必须在渲染之后采样（晚于收到首包的采样点）');
    // 没有 prep_ms（旧服务端 / omitempty）时不得编造
    const noPrep = makeGridHarness();
    const s2 = newSession();
    noPrep.state.activeId = s2.id;
    meta(noPrep, s2, 5);
    noPrep.consumeQueryEvent(s2, s2.runSeq, { type: 'rows', rows: rowsOf(5, 3, 0) });
    noPrep.consumeQueryEvent(s2, s2.runSeq, { type: 'summary', summary: { rows: 3, elapsed_ms: 12, result_id: 'rid-1' } });
    assert.ok(!s2.statusTitle.includes('元数据准备'), '服务端没给 prep_ms 时不得显示该段');
    assert.ok(s2.statusTitle.includes('首帧上屏'), '即使只有 3 行也要量首帧');
  });

  await t.test('首帧采样点必须按查询重置，否则同一页签第二条查询会显示上一条的旧值', () => {
    // 结构守卫：runQuery 里 firstRowsTime 与 firstPaintTime 必须一起清零。
    // 只重置 firstRowsTime 会让第二次查询沿用上一次的时间戳（首帧上屏永远是旧值）。
    const runQueryStart = dbJs.indexOf('s.startTime = performance.now();');
    assert.ok(runQueryStart > 0, '未能在 database.js 里定位 runQuery 的计时起点');
    const resetBlock = dbJs.slice(runQueryStart, runQueryStart + 600);
    assert.ok(/s\.firstRowsTime\s*=\s*0\s*;/.test(resetBlock), 'runQuery 必须重置 firstRowsTime');
    assert.ok(/s\.firstPaintTime\s*=\s*0\s*;/.test(resetBlock),
      'runQuery 必须同步重置 firstPaintTime，否则首帧上屏会沿用上一条查询的时间戳');
  });
});

// ---------------------------------------------------------------------------
// DBUI-06: 查询历史写入只做一次整库读 + 一次写（缺陷 C）
// ---------------------------------------------------------------------------
test('History store single read/write per addHistory (DBUI-06)', async (t) => {
  const HISTORY_KEY = 'kairo:database:history:v2';
  function seededHarness(buckets, migration) {
    const env = loadHistoryHarness();
    env.seed(JSON.stringify({
      version: 2,
      migration: migration || { legacyStrings: 1 },
      buckets: buckets || {}
    }));
    env.resetCounts();
    return env;
  }
  function expectOneReadOneWrite(env, fn, label) {
    env.resetCounts();
    const result = fn();
    assert.strictEqual(env.reads(), 1, label + '：整库读必须恰好 1 次，实际 ' + env.reads());
    assert.strictEqual(env.writes(), 1, label + '：整库写必须恰好 1 次，实际 ' + env.writes());
    return result;
  }

  await t.test('addHistory 每次调用一次整库读 + 一次写，并原样返回当前列表', () => {
    const env = seededHarness({ 'src-1': [] });
    // 先登记 pending run，让 running 记录保持 running（真实执行顺序）
    expectOneReadOneWrite(env, () => env.features.recordQueryStart(
      'SELECT 2 FROM dual', 'run-A', { sourceId: 'src-1', sourceName: 'Src', dialect: 'oracle' }
    ), 'recordQueryStart');
    const list = expectOneReadOneWrite(env, () => env.features.addHistory({
      sql: 'SELECT 2 FROM dual', sourceId: 'src-1', runId: 'run-A', status: 'running'
    }), 'addHistory');
    assert.strictEqual(list.length, 1);
    assert.strictEqual(list[0].sql, 'SELECT 2 FROM dual');
    assert.strictEqual(list[0].runId, 'run-A');
    assert.strictEqual(list[0].sourceId, 'src-1');
    assert.strictEqual(list[0].status, 'running', 'pending run 不得被误标 interrupted');
    // 返回口径必须与旧实现（重新读盘再归一化）完全一致
    const viaDisk = env.features.listHistory('src-1');
    assert.deepStrictEqual(JSON.parse(JSON.stringify(list)), JSON.parse(JSON.stringify(viaDisk)));
  });

  await t.test('running → interrupted 标记与 runId 原地升级语义保持不变', () => {
    const env = seededHarness({
      'src-1': [{ id: 'q-stale', bucket: 'src-1', sourceId: 'src-1', sql: 'SELECT 0 FROM dual', status: 'running', runId: 'run-stale' }]
    });
    // 遗留 run（不在 pendingRuns 里）必须标成 interrupted
    const list = expectOneReadOneWrite(env, () => env.features.addHistory({
      sql: 'SELECT 1 FROM dual', sourceId: 'src-1', runId: 'run-A', status: 'running'
    }), 'addHistory');
    const stale = list.find(e => e.runId === 'run-stale');
    assert.ok(stale, '遗留记录必须仍然在列表里');
    assert.strictEqual(stale.status, 'interrupted', '刷新后遗留的 running 只能算 interrupted');
    // 写盘的 JSON 形状不变（标记只发生在内存侧，与修复前一致）
    assert.strictEqual(env.disk().buckets['src-1'].find(e => e.runId === 'run-stale').status, 'running');

    // 真实的一次查询：recordQueryStart 记 running，recordQueryResult 原地升级同一条
    const env2 = seededHarness({ 'src-1': [] });
    const started = expectOneReadOneWrite(env2, () => env2.features.recordQueryStart(
      'SELECT 3 FROM dual', 'run-B', { sourceId: 'src-1', sourceName: 'Src', dialect: 'oracle' }
    ), 'recordQueryStart');
    assert.strictEqual(started, 'run-B');
    assert.strictEqual(env2.features.listHistory('src-1').filter(e => e.runId === 'run-B').length, 1);
    expectOneReadOneWrite(env2, () => env2.features.recordQueryResult('run-B', {
      sql: 'SELECT 3 FROM dual', sourceId: 'src-1', status: 'success', rows: 42, elapsedMs: 9
    }), 'recordQueryResult');
    const stored = env2.disk().buckets['src-1'].filter(e => e.runId === 'run-B');
    assert.strictEqual(stored.length, 1, 'runId 必须原地升级，不得新增重复行');
    assert.strictEqual(stored[0].status, 'success');
    assert.strictEqual(stored[0].rows, 42);
  });

  await t.test('旧版桶保护 / 超长截断 / MAX_HISTORY 截断保持原样', () => {
    const env = seededHarness({ 'src-1': [] });
    // 旧版桶只读：既不写入也不返回
    env.resetCounts();
    assert.strictEqual(env.features.addHistory({ sql: 'SELECT 1', sourceId: env.features.LEGACY_HISTORY_BUCKET }), undefined);
    assert.strictEqual(env.reads(), 0, '旧版桶必须在读盘之前就被拒绝');
    assert.strictEqual(env.writes(), 0);
    // 超长 SQL 截断标记
    const longSql = 'SELECT ' + 'x'.repeat(31000);
    const list = expectOneReadOneWrite(env, () => env.features.addHistory({ sql: longSql, sourceId: 'src-1' }), '超长 SQL');
    assert.ok(list[0].sql.endsWith('/* -- [kairo: 历史记录超长截断] -- */'), '必须保留截断标记');
    assert.strictEqual(list[0].sql.length, 30000 + '\n/* -- [kairo: 历史记录超长截断] -- */'.length);
    // MAX_HISTORY 截断
    const many = Array.from({ length: 205 }, (_, i) => ({ id: 'q-' + i, sql: 'SELECT ' + i, status: 'success', runId: 'r' + i }));
    const env2 = seededHarness({ 'src-1': many });
    const capped = expectOneReadOneWrite(env2, () => env2.features.addHistory({ sql: 'SELECT newest', sourceId: 'src-1' }), 'MAX_HISTORY');
    assert.strictEqual(capped.length, 200, '内存列表必须按 MAX_HISTORY 截断');
    assert.strictEqual(env2.disk().buckets['src-1'].length, 200, '落盘桶同样按 MAX_HISTORY 截断');
    assert.strictEqual(capped[0].sql, 'SELECT newest');
  });

  await t.test('同一 SQL 去重与历史键写入保持一致', () => {
    const env = seededHarness({ 'src-1': [] });
    expectOneReadOneWrite(env, () => env.features.addHistory({ sql: 'SELECT 9 FROM dual', sourceId: 'src-1' }), '首次');
    const list = expectOneReadOneWrite(env, () => env.features.addHistory({ sql: 'SELECT 9 FROM dual', sourceId: 'src-1' }), '重复 SQL');
    assert.strictEqual(list.filter(e => e.sql === 'SELECT 9 FROM dual').length, 1, '同一 SQL 不得出现两条');
    assert.strictEqual(env.disk().version, 2, '存储版本号不得变化');
    assert.ok(env.disk().buckets['src-1'], '桶结构（on-disk JSON 形状）不得变化');
    assert.strictEqual(HISTORY_KEY, 'kairo:database:history:v2', '存储键不得变化');
  });
});
