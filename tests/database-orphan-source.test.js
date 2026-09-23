'use strict';
/*
 * tests/database-orphan-source.test.js
 * DBUI-03 回归：失效页签自动改绑其他数据源，旧 SQL 与结果未隔离。
 *
 * 本测试不重写实现：它把 web/pages/database.js 整段放进 vm 执行，并用 DOM/API 替身
 * 驱动真实的 deleteSource / reconcileSessionsWithSources / renderWorkspace /
 * renderOrphanWorkspace / restoreLocalDBSessions / loadObjectTabInspect。
 * 唯一注入是在文件末尾前挂一个内部引用出口（仓库既有测试同样做法）。
 */
const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const ROOT = path.join(__dirname, '..');
const PAGE_SOURCE = fs.readFileSync(path.join(ROOT, 'web', 'pages', 'database.js'), 'utf8');
const RESOURCE_SESSION_SOURCE = fs.readFileSync(path.join(ROOT, 'web', 'workbench', 'resource-session.js'), 'utf8');

const HOOK_MARKER = '  Kairo.state.routes.database = renderDatabase;';
const HOOK_CODE = '  window.__dbInternals = {\n'
  + '    state: state, persisted: persisted, sess: sess, effectiveSource: effectiveSource,\n'
  + '    sessionSourceState: sessionSourceState, bindSessionSource: bindSessionSource,\n'
  + '    reconcileSessionsWithSources: reconcileSessionsWithSources, loadSources: loadSources,\n'
  + '    deleteSource: deleteSource, renderWorkspace: renderWorkspace, render: render,\n'
  + '    switchSession: switchSession,\n'
  + '    renderOrphanWorkspace: renderOrphanWorkspace, restoreLocalDBSessions: restoreLocalDBSessions,\n'
  + '    serializeDBSessions: serializeDBSessions, loadObjectTabInspect: loadObjectTabInspect,\n'
  + "    sessionSourceClass: typeof sessionSourceClass === 'function' ? sessionSourceClass : undefined,\n"
  + "    rebindOrphanedSession: typeof rebindOrphanedSession === 'function' ? rebindOrphanedSession : undefined\n"
  + '  };\n';

const SRC_A = { id: 'A', name: 'Production A', kind: 'oracle', username: 'APP', max_rows: 1000, environment: 'production' };
const SRC_B = { id: 'B', name: 'Production B', kind: 'oracle', username: 'APP', max_rows: 1000 };

function makeElement(id) {
  const listeners = {};
  const attrs = {};
  const classes = new Set();
  const el = {
    id: id || '', tagName: 'DIV', nodeType: 1,
    innerHTML: '', textContent: '', value: '', className: '', title: '', hidden: false,
    disabled: false, checked: false, required: false, placeholder: '', type: '', min: '', max: '',
    inputMode: '', spellcheck: true, scrollTop: 0, scrollLeft: 0, offsetHeight: 0, clientHeight: 0,
    selectionStart: 0, selectionEnd: 0, files: [], options: [], children: [], childNodes: [],
    style: {}, dataset: {},
    classList: {
      add: c => { classes.add(c); },
      remove: c => { classes.delete(c); },
      toggle: (c, force) => {
        if (force === undefined) { if (classes.has(c)) classes.delete(c); else classes.add(c); }
        else if (force) classes.add(c); else classes.delete(c);
      },
      contains: c => classes.has(c)
    },
    setAttribute: (k, v) => { attrs[k] = String(v); },
    getAttribute: k => (k in attrs ? attrs[k] : null),
    hasAttribute: k => k in attrs,
    removeAttribute: k => { delete attrs[k]; },
    setCustomValidity: () => {},
    checkValidity: () => true,
    reportValidity: () => {},
    addEventListener: (t, fn) => { (listeners[t] = listeners[t] || []).push(fn); },
    removeEventListener: (t, fn) => { if (listeners[t]) listeners[t] = listeners[t].filter(f => f !== fn); },
    dispatchEvent: ev => { (listeners[ev.type] || []).forEach(fn => fn(ev)); return true; },
    appendChild: c => { el.children.push(c); return c; },
    insertBefore: c => { el.children.push(c); return c; },
    replaceChildren: () => { el.children.length = 0; },
    removeChild: c => { const i = el.children.indexOf(c); if (i >= 0) el.children.splice(i, 1); return c; },
    remove: () => {},
    focus: () => {}, blur: () => {}, select: () => {}, click: () => {},
    setSelectionRange: () => {}, scrollIntoView: () => {},
    querySelector: () => null,
    querySelectorAll: () => [],
    closest: () => null,
    contains: () => false,
    getBoundingClientRect: () => ({ top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0 }),
    get isConnected() { return true; },
    parentElement: null, parentNode: null, firstChild: null, lastChild: null
  };
  return el;
}

function querySession(overrides) {
  return Object.assign({
    id: 1, type: 'query', sql: '', rows: [], columns: [], summary: null, lastError: null,
    lastSQL: '', lastMaxRows: 0, page: 1, pageSize: 20, resultMode: 'grid',
    selectedRow: 0, selectedCol: 0, colSelected: -1, selectedCols: new Set(), lastColSelected: -1,
    localFilter: '', hiddenColumns: new Set(), sort: null, plan: [], gridReady: false,
    controller: null, dirtyCells: {}, isEditMode: false, gridEditsStaged: false,
    transactionId: 'dbtab-seed', transactionPending: false, runSeq: 0, status: '就绪',
    sourceId: '', sourceRef: null, sourceState: 'unbound', orphan: false
  }, overrides || {});
}

function createDbEnv(options) {
  const opts = options || {};
  const elements = new Map();
  const el = id => { if (!elements.has(id)) elements.set(id, makeElement(id)); return elements.get(id); };
  const calls = [];
  const toasts = [];
  const clearedGridMutations = { count: 0 };
  const bridge = { state: null };

  function storage() {
    const data = {};
    return {
      _data: data,
      getItem: k => (k in data ? data[k] : null),
      setItem: (k, v) => { data[k] = String(v); },
      removeItem: k => { delete data[k]; },
      key: i => Object.keys(data)[i] || null,
      get length() { return Object.keys(data).length; }
    };
  }

  const api = async function (method, p, body) {
    calls.push({ method: method, path: p, body: body });
    const state = bridge.state;
    if (method === 'GET' && p === '/api/database/sources') return { sources: (state.sources || []).slice() };
    if (method === 'DELETE' && p.indexOf('/api/database/sources/') === 0) {
      const id = decodeURIComponent(p.slice('/api/database/sources/'.length));
      state.sources = state.sources.filter(s => String(s.id) !== id);
      return { ok: true };
    }
    if (p === '/api/database/transaction') {
      if (opts.failRollback) throw new Error('ORA-03113: 旧数据源连接已断开');
      return { ok: true };
    }
    if (p.indexOf('/api/database/metadata/inspect') === 0) return { inspect: { fields: [] } };
    return {};
  };

  const documentDouble = {
    readyState: 'complete', hidden: false, activeElement: null,
    body: makeElement('body'), documentElement: makeElement('html'),
    getElementById: id => el(id),
    createElement: tag => makeElement(tag),
    createTextNode: t => ({ nodeType: 3, textContent: String(t) }),
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener: () => {},
    removeEventListener: () => {}
  };

  const windowDouble = {
    Kairo: {
      core: {
        toast: (msg, type) => toasts.push({ msg: msg, type: type }),
        escapeHtml: value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
        copyToClipboard: () => {},
        el: () => null
      },
      api: {
        api: api,
        getPreference: async () => ({ exists: false, value: null }),
        putPreference: async () => {},
        preferenceSaver: () => function () {}
      },
      state: { routes: {}, routeNames: {}, routeSubs: {} },
      database: {},
      databaseFeatures: {
        clearGridMutations: () => { clearedGridMutations.count++; },
        pendingGridMutations: () => [],
        removeSource: () => {}
      },
      auth: { getUser: () => 'admin', getRole: () => 'admin' }
    },
    addEventListener: () => {},
    removeEventListener: () => {},
    innerWidth: 1366, innerHeight: 768,
    localStorage: storage(),
    sessionStorage: storage(),
    location: { hash: '#/database', href: 'http://localhost/#/database' },
    navigator: { userAgent: 'node' },
    document: documentDouble,
    confirm: () => true,
    fetch: async () => ({ ok: true, json: async () => ({}) }),
    requestAnimationFrame: () => 0,
    cancelAnimationFrame: () => {},
    setTimeout: setTimeout,
    clearTimeout: clearTimeout,
    setInterval: () => 0,
    clearInterval: () => {},
    MutationObserver: function () {},
    performance: { now: () => Date.now() },
    AbortController: AbortController,
    Blob: Blob,
    URLSearchParams: URLSearchParams,
    Set: Set, Map: Map, Promise: Promise, JSON: JSON, Math: Math, Date: Date,
    console: console
  };

  const ctx = {
    window: windowDouble,
    document: documentDouble,
    location: windowDouble.location,
    navigator: windowDouble.navigator,
    localStorage: windowDouble.localStorage,
    sessionStorage: windowDouble.sessionStorage,
    confirm: () => true,
    fetch: windowDouble.fetch,
    requestAnimationFrame: windowDouble.requestAnimationFrame,
    cancelAnimationFrame: windowDouble.cancelAnimationFrame,
    setTimeout: setTimeout, clearTimeout: clearTimeout,
    setInterval: windowDouble.setInterval, clearInterval: windowDouble.clearInterval,
    MutationObserver: windowDouble.MutationObserver,
    performance: windowDouble.performance,
    AbortController: AbortController, Blob: Blob, URLSearchParams: URLSearchParams,
    Set: Set, Map: Map, Promise: Promise, JSON: JSON, Math: Math, Date: Date,
    console: console
  };

  assert.ok(PAGE_SOURCE.indexOf(HOOK_MARKER) >= 0, 'database.js hook marker must exist');
  vm.runInNewContext(RESOURCE_SESSION_SOURCE, ctx);
  vm.runInNewContext(PAGE_SOURCE.replace(HOOK_MARKER, HOOK_CODE + HOOK_MARKER), ctx);

  const hooks = windowDouble.__dbInternals;
  assert.ok(hooks && hooks.state, 'internal hook must be exposed');
  bridge.state = hooks.state;

  return {
    hooks: hooks,
    state: hooks.state,
    el: el,
    calls: calls,
    toasts: toasts,
    clearedGridMutations: clearedGridMutations,
    failRollback: !!opts.failRollback,
    apiCallsFor: function (id) {
      return calls.filter(c => {
        if (c.body && c.body.source_id === id) return true;
        return typeof c.path === 'string' && c.path.indexOf('source_id=' + id) >= 0;
      });
    }
  };
}

function requireFn(env, name) {
  const fn = env.hooks[name];
  assert.strictEqual(typeof fn, 'function', 'database.js must expose ' + name + ' for this regression');
  return fn;
}

const failures = [];
async function scenario(name, fn) {
  try {
    await fn();
    console.log('  ✓ ' + name);
  } catch (error) {
    failures.push({ name: name, error: error });
    console.log('  ✗ ' + name + ' :: ' + (error && error.message));
  }
}

function orphanView(env) {
  return String(env.el('db-workspace').innerHTML || '');
}

// 通过真实的 renderOrphanWorkspace + “绑定并继续”按钮完成显式改绑
async function clickOrphanRebind(env, targetSourceId) {
  const host = env.el('db-workspace');
  const session = env.hooks.sess();
  requireFn(env, 'renderOrphanWorkspace')(host, session);
  const select = env.el('db-orphan-source');
  const button = env.el('db-orphan-bind');
  assert.ok(select && button, 'orphan view must render select + bind button');
  select.value = targetSourceId;
  assert.strictEqual(typeof button.onclick, 'function', 'orphan bind affordance must be wired');
  return await button.onclick();
}

async function main() {
  console.log('--- DBUI-03 orphan source binding regressions ---');

  // S1: 删除 A 后，A 页签必须保持指向失效 A，不得改绑 B
  await scenario('S1 删除 A 后 A 页签保持孤儿态、不自动改绑 B', async () => {
    const env = createDbEnv();
    const state = env.state;
    const bind = requireFn(env, 'bindSessionSource');
    const tabA = querySession({
      id: 11,
      sql: 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42',
      rows: [[42, 'testA']],
      columns: [{ name: 'ID' }, { name: 'STATUS' }],
      summary: { result_id: 'res-A', rows: 1 },
      resultId: 'res-A',
      plan: [{ id: 0, operation: 'UPDATE' }],
      editPlan: { result_id: 'res-A', schema: 'APP', table: 'JOBS', can_update: true, primary_keys: ['ID'] },
      dirtyCells: { '0_1': { rowIdx: 0, colIdx: 1, newVal: 1 } },
      transactionId: 'dbtab-old-a',
      transactionPending: true,
      sourceFingerprint: 'fp-a'
    });
    bind(tabA, SRC_A);
    const tabB = querySession({ id: 12, sql: 'SELECT 1 FROM DUAL' });
    bind(tabB, SRC_B);
    state.sources = [SRC_A, SRC_B];
    state.source = SRC_A;
    state.sessions = [tabA, tabB];
    state.activeId = tabA.id;

    await requireFn(env, 'deleteSource')(SRC_A);

    assert.strictEqual(tabA.sourceId, 'A', 'A 页签的 sourceId 必须仍是失效的 A（当前实现会改成 B）');
    assert.notStrictEqual(tabA.sourceId, 'B', 'A 页签不得自动改绑 B');
    assert.strictEqual(tabA.orphan, true, 'A 页签必须标记 orphan');
    assert.strictEqual(tabA.sourceState, 'orphaned', 'A 页签来源状态必须是 orphaned');
    assert.ok(tabA.sourceRef && tabA.sourceRef.name === 'Production A', '必须保留原数据源名称快照');
    assert.strictEqual(tabA.sql, 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42', '孤儿页签必须保留 SQL');
    assert.deepStrictEqual(tabA.rows, [[42, 'testA']], '孤儿页签保留原结果行（归属仍是 A）');
    assert.strictEqual(env.hooks.effectiveSource(), null, '孤儿页签不得解析出任何可执行数据源');
    assert.strictEqual(env.apiCallsFor('B').length, 0, '删除 A 的过程中不得向 B 发请求');
    assert.strictEqual(tabB.sourceId, 'B', 'B 页签自身绑定不受影响');
    const html = orphanView(env);
    assert.ok(html.indexOf('db-orphan-workspace') >= 0, '工作区必须渲染孤儿视图');
    assert.ok(html.indexOf('原数据源已删除') >= 0, '孤儿视图必须提示原数据源已删除');
    assert.ok(html.indexOf('db-run') < 0, '孤儿视图不得提供绑定到 B 的执行入口');
    // 修复后证据快照（对应审查报告的复现 JSON）
    console.log('    删除 A 后页签快照: ' + JSON.stringify({
      sourceId: tabA.sourceId,
      sql: tabA.sql,
      retainedResultRows: tabA.rows,
      sourceRef: tabA.sourceRef,
      orphan: tabA.orphan,
      sourceState: tabA.sourceState,
      effectiveSource: env.hooks.effectiveSource()
    }));
  });

  // S2: 全部数据源被删除时，也必须保留原 ID 与名称快照，而不是清空
  await scenario('S2 删除最后一个数据源后保留原 sourceId 与名称快照', async () => {
    const env = createDbEnv();
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    const tabA = querySession({ id: 21, sql: 'SELECT * FROM APP.JOBS', transactionId: 'dbtab-only-a' });
    env.hooks.bindSessionSource(tabA, SRC_A);
    state.sources = [SRC_A];
    state.source = SRC_A;
    state.sessions = [tabA];
    state.activeId = tabA.id;

    await requireFn(env, 'deleteSource')(SRC_A);

    assert.strictEqual(tabA.sourceId, 'A', '全部数据源删除后仍须记住原 sourceId，不能清空');
    assert.strictEqual(tabA.orphan, true, '必须保持孤儿态');
    assert.ok(tabA.sourceRef && tabA.sourceRef.name === 'Production A', '必须有名称快照供 UI 说明归属');
    const html = orphanView(env);
    assert.ok(html.indexOf('db-orphan-workspace') >= 0, '无数据源时仍应显示孤儿视图以便复制 SQL');
    assert.ok(html.indexOf('Production A') >= 0, '孤儿视图必须显示原数据源名称');
  });

  // S3: 合法便利路径：全新未绑定空页签仍自动使用当前数据源
  await scenario('S3 新建未绑定页签仍自动绑定当前数据源', async () => {
    const env = createDbEnv();
    const state = env.state;
    const fresh = querySession({ id: 31, sql: '', sourceId: '' });
    state.sources = [SRC_B];
    state.source = SRC_B;
    state.sessions = [fresh];
    state.activeId = fresh.id;

    requireFn(env, 'reconcileSessionsWithSources')({});

    assert.strictEqual(fresh.sourceId, 'B', '全新空页签应自动使用当前数据源');
    assert.strictEqual(!!fresh.orphan, false, '全新空页签不得标记 orphan');
    assert.strictEqual(fresh.sourceState, 'bound', '全新空页签绑定后状态为 bound');
  });

  // S4: 显式改绑必须重置结果/计划/result_id/网格队列/事务，并保留 SQL
  await scenario('S4 显式改绑 B 后重置结果与事务上下文、保留 SQL', async () => {
    const env = createDbEnv();
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    const tabA = querySession({
      id: 41,
      sql: 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42',
      rows: [[42, 'testA']],
      columns: [{ name: 'ID' }],
      summary: { result_id: 'res-A' },
      resultId: 'res-A',
      editPlan: { result_id: 'res-A', table: 'JOBS', can_update: true },
      plan: [{ id: 0 }],
      dirtyCells: { '0_0': { rowIdx: 0, colIdx: 0, newVal: 7 } },
      gridEditsStaged: true,
      transactionId: 'dbtab-old-a',
      transactionPending: true
    });
    env.hooks.bindSessionSource(tabA, SRC_A);
    state.sources = [SRC_B];
    state.source = SRC_B;
    state.sessions = [tabA];
    state.activeId = tabA.id;
    state.metadataCache = { APP: ['JOBS'] };
    requireFn(env, 'reconcileSessionsWithSources')({ deletedId: 'A', deletedRef: { id: 'A', name: 'Production A', kind: 'oracle' } });

    assert.strictEqual(tabA.orphan, true, '前置条件：页签应为孤儿态');
    assert.strictEqual(tabA.transactionId, 'dbtab-old-a', '前置条件：旧 transactionId 仍在');
    assert.strictEqual(JSON.stringify(tabA.rows), '[[42,"testA"]]', '前置条件：结果行仍在');
    const oldTransactionId = tabA.transactionId;

    let clickError = null;
    try { await clickOrphanRebind(env, 'B'); } catch (e) { clickError = e; }

    assert.strictEqual(tabA.sourceId, 'B', '显式改绑后页签应绑定 B');
    assert.strictEqual(!!tabA.orphan, false, '改绑后不再是孤儿');
    assert.strictEqual(tabA.sourceState, 'bound', '改绑后状态为 bound');
    assert.strictEqual(tabA.sql, 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42', '改绑必须保留 SQL 文本');
    // vm 里创建的值带有另一个领域的原型，这里用 JSON 比较避免误报
    assert.strictEqual(JSON.stringify(tabA.rows), '[]', '改绑必须清空结果行');
    assert.strictEqual(JSON.stringify(tabA.columns), '[]', '改绑必须清空列元数据');
    assert.strictEqual(tabA.summary, null, '改绑必须清空 result_id/summary');
    assert.strictEqual(tabA.resultId, '', '改绑必须清空 resultId');
    assert.strictEqual(tabA.editPlan, null, '改绑必须清空 editPlan');
    assert.strictEqual(JSON.stringify(tabA.plan), '[]', '改绑必须清空执行计划');
    assert.strictEqual(JSON.stringify(tabA.dirtyCells), '{}', '改绑必须清空未提交网格修改');
    assert.strictEqual(tabA.gridEditsStaged, false, '改绑必须清除已暂存网格事务标记');
    assert.strictEqual(tabA.transactionPending, false, '改绑不得把旧事务上下文带到新源');
    assert.notStrictEqual(tabA.transactionId, oldTransactionId, '改绑必须生成新的 transactionId');
    assert.ok(tabA.transactionId, '新 transactionId 不能为空');
    assert.ok(env.clearedGridMutations.count > 0, '改绑必须清空未提交网格变更队列');
    assert.ok(!state.metadataCache || !state.metadataCache.APP, '改绑必须丢弃旧源的元数据缓存');
    assert.strictEqual(tabA.sourceRef && tabA.sourceRef.name, 'Production B', '改绑后快照指向新源');
    const rollback = env.calls.filter(c => c.path === '/api/database/transaction' && c.body && c.body.action === 'ROLLBACK');
    assert.strictEqual(rollback.length, 1, '改绑前必须先回滚旧源事务');
    assert.strictEqual(rollback[0].body.source_id, 'A', '回滚必须发给旧源 A');
    assert.strictEqual(rollback[0].body.session_id, 'dbtab-old-a', '回滚必须使用旧事务 ID');
    if (clickError) throw clickError;
  });

  // S5: 旧源回滚失败时不得迁移事务上下文，并记录失败状态
  await scenario('S5 旧源回滚失败时保留孤儿态并记录失败', async () => {
    const env = createDbEnv({ failRollback: true });
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    const tabA = querySession({
      id: 51,
      sql: 'DELETE FROM APP.JOBS WHERE ID=42',
      rows: [[42]],
      transactionId: 'dbtab-tx-a',
      transactionPending: true
    });
    env.hooks.bindSessionSource(tabA, SRC_A);
    state.sources = [SRC_B];
    state.source = SRC_B;
    state.sessions = [tabA];
    state.activeId = tabA.id;
    requireFn(env, 'reconcileSessionsWithSources')({ deletedId: 'A', deletedRef: { id: 'A', name: 'Production A' } });

    let clickError = null;
    try { await clickOrphanRebind(env, 'B'); } catch (e) { clickError = e; }

    assert.strictEqual(tabA.sourceId, 'A', '回滚未确认前绝不迁移绑定');
    assert.strictEqual(tabA.orphan, true, '回滚失败后仍为孤儿态');
    assert.strictEqual(tabA.transactionId, 'dbtab-tx-a', '事务上下文不得被替换');
    assert.strictEqual(tabA.transactionPending, true, '未确认回滚前不得假定事务已结束');
    assert.ok(tabA.lastError && /回滚|失败|取消/.test(String(tabA.lastError.title || '') + String(tabA.lastError.message || '')), '必须为页签记录失败状态');
    assert.ok(env.toasts.some(t => t.type === 'err'), '必须向用户提示失败');
    assert.strictEqual(JSON.stringify(tabA.rows), '[[42]]', '失败时不得清空结果');
    if (clickError) throw clickError;
  });

  // S6: 旧源仍有执行中请求时不得迁移事务上下文
  await scenario('S6 旧源仍有执行中请求时保留孤儿态并记录失败', async () => {
    const env = createDbEnv();
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    let aborted = false;
    const tabA = querySession({
      id: 61,
      sql: 'SELECT * FROM APP.JOBS',
      rows: [[1]],
      transactionId: 'dbtab-inflight-a'
    });
    tabA.controller = { abort: () => { aborted = true; } };
    env.hooks.bindSessionSource(tabA, SRC_A);
    state.sources = [SRC_B];
    state.source = SRC_B;
    state.sessions = [tabA];
    state.activeId = tabA.id;
    requireFn(env, 'reconcileSessionsWithSources')({ deletedId: 'A', deletedRef: { id: 'A', name: 'Production A' } });

    let clickError = null;
    try { await clickOrphanRebind(env, 'B'); } catch (e) { clickError = e; }

    assert.strictEqual(aborted, true, '必须请求取消旧源执行中的请求');
    assert.strictEqual(tabA.sourceId, 'A', '请求未结束前绝不迁移绑定');
    assert.strictEqual(tabA.orphan, true, '仍为孤儿态');
    assert.ok(tabA.lastError, '必须记录等待取消的失败状态');
    if (clickError) throw clickError;
  });

  // S7: 备份恢复（本地/远端共用同一函数）后失效页签仍指向 A，新建页签才跟随当前源
  await scenario('S7 备份恢复后失效页签保持孤儿、未绑定页签跟随当前源', async () => {
    const env = createDbEnv();
    const state = env.state;
    state.sources = [SRC_B];
    state.source = SRC_B;
    const ok = requireFn(env, 'restoreLocalDBSessions')({
      activeId: 71,
      tabSeq: 72,
      sessions: [
        { id: 71, type: 'query', sql: 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42', sourceId: 'A', sourceRef: { id: 'A', name: 'Production A', kind: 'oracle' } },
        { id: 72, type: 'query', sql: '', sourceId: '' }
      ]
    });
    assert.strictEqual(ok, true, 'restoreLocalDBSessions 应成功');
    const restoredA = state.sessions.find(s => s.id === 71);
    const restoredFresh = state.sessions.find(s => s.id === 72);
    assert.strictEqual(restoredA.sourceId, 'A', '恢复备份后失效页签仍须指向 A（当前实现会写成 B）');
    assert.strictEqual(restoredA.orphan, true, '恢复出的失效页签必须是孤儿');
    assert.ok(restoredA.sourceRef && restoredA.sourceRef.name === 'Production A', '恢复后必须保留原名称快照');
    assert.strictEqual(restoredFresh.sourceId, 'B', '恢复出的未绑定页签可跟随当前数据源');
    assert.strictEqual(!!restoredFresh.orphan, false, '未绑定页签不是孤儿');
    assert.strictEqual(env.apiCallsFor('B').length, 0, '恢复过程不得向 B 发请求');
  });

  // S8: 对象页签的来源不存在时同样不得自动改绑
  await scenario('S8 对象页签恢复时来源已删除不得改绑当前源', async () => {
    const env = createDbEnv();
    const state = env.state;
    state.sources = [SRC_B];
    state.source = SRC_B;
    requireFn(env, 'restoreLocalDBSessions')({
      activeId: 81,
      tabSeq: 81,
      sessions: [
        { id: 81, type: 'object', schema: 'APP', objectName: 'JOBS', objectType: 'TABLE', sourceId: 'A', sourceRef: { id: 'A', name: 'Production A', kind: 'oracle' } }
      ]
    });
    const obj = state.sessions[0];
    assert.strictEqual(obj.sourceId, 'A', '对象页签必须保留失效的 A');
    assert.strictEqual(obj.sourceState, 'orphaned', '对象页签必须处于 orphaned');
    assert.ok(obj.sourceRef && obj.sourceRef.name === 'Production A', '对象页签应保留名称快照');
  });

  // S9: renderWorkspace 不得对孤儿页签执行 fallback 改绑
  await scenario('S9 renderWorkspace 不对孤儿页签做 fallback 改绑', async () => {
    const env = createDbEnv();
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    const tabA = querySession({ id: 91, sql: 'SELECT * FROM APP.JOBS', rows: [[1, 2]] });
    env.hooks.bindSessionSource(tabA, SRC_A);
    state.sources = [SRC_B];
    state.source = SRC_B;
    state.sessions = [tabA];
    state.activeId = tabA.id;
    tabA.sourceId = 'A';
    tabA.sourceRef = { id: 'A', name: 'Production A', kind: 'oracle', version: '' };

    requireFn(env, 'renderWorkspace')(true);

    assert.strictEqual(tabA.sourceId, 'A', 'renderWorkspace 不得把孤儿页签改绑到当前数据源');
    assert.strictEqual(tabA.orphan, true, 'renderWorkspace 后仍为孤儿');
    assert.strictEqual(env.hooks.effectiveSource(), null, '孤儿页签不得解析出 B 作为执行源');
    const html = orphanView(env);
    assert.ok(html.indexOf('db-orphan-workspace') >= 0, 'renderWorkspace 应渲染孤儿视图');
    assert.ok(html.indexOf('db-run') < 0, '孤儿视图不得出现执行按钮');
  });

  // S10: 孤儿对象页签的元数据请求不得发给当前数据源
  await scenario('S10 孤儿对象页签不向当前数据源请求元数据', async () => {
    const env = createDbEnv();
    const state = env.state;
    state.sources = [SRC_B];
    state.source = SRC_B;
    const obj = {
      id: 101, type: 'object', schema: 'APP', objectName: 'JOBS', objectType: 'TABLE',
      inspectTab: 'fields', inspectData: null, inspectLoading: false, inspectError: null, fieldFilter: '',
      sourceId: 'A', sourceRef: { id: 'A', name: 'Production A', kind: 'oracle' }, sourceState: 'orphaned'
    };
    state.sessions = [obj];
    state.activeId = 999;

    await requireFn(env, 'loadObjectTabInspect')(obj, true);

    assert.strictEqual(env.calls.filter(c => String(c.path).indexOf('/api/database/metadata/inspect') === 0).length, 0, '不得为失效来源请求元数据');
    assert.ok(obj.inspectError && /删除/.test(String(obj.inspectError)), '必须为用户记录来源已删除的失败状态');
  });

  // S11: 顶部数据源下拉框也不得把孤儿页签静默迁移过去
  await scenario('S11 顶部数据源下拉框切换时按显式改绑规则重置', async () => {
    const env = createDbEnv();
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    const SRC_C = { id: 'C', name: 'Production C', kind: 'oracle', username: 'APP', max_rows: 1000 };
    state.sources = [SRC_B, SRC_C];
    state.source = SRC_B;
    // 先执行真实 render() 以挂上顶部下拉框的 onchange 处理器（内部状态之后覆盖）
    requireFn(env, 'render')(env.el('view'));
    const tabA = querySession({
      id: 111,
      sql: 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42',
      rows: [[42, 'testA']],
      summary: { result_id: 'res-A' },
      resultId: 'res-A',
      editPlan: { result_id: 'res-A', table: 'JOBS' },
      plan: [{ id: 0 }],
      dirtyCells: { '0_0': { rowIdx: 0, colIdx: 0, newVal: 1 } },
      transactionId: 'dbtab-topbar-a',
      transactionPending: true
    });
    env.hooks.bindSessionSource(tabA, SRC_A);
    state.sessions = [tabA];
    state.activeId = tabA.id;
    requireFn(env, 'reconcileSessionsWithSources')({ deletedId: 'A', deletedRef: { id: 'A', name: 'Production A' } });
    assert.strictEqual(tabA.orphan, true, '前置条件：页签应为孤儿态');
    // 渲染孤儿视图（真实 textarea 替身会拿到 SQL），用户是在这个视图里改顶部下拉框的
    requireFn(env, 'renderWorkspace')(true);
    assert.ok(orphanView(env).indexOf('db-orphan-workspace') >= 0, '前置条件：应显示孤儿视图');
    assert.strictEqual(env.el('db-sql').value, 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42', '前置条件：孤儿视图 textarea 含 SQL');

    const select = env.el('db-source');
    select.value = 'C';
    let clickError = null;
    try { await select.onchange.call(select); } catch (e) { clickError = e; }

    assert.strictEqual(tabA.sourceId, 'C', '顶部下拉框的改绑也必须绑定到用户选择的源');
    assert.strictEqual(JSON.stringify(tabA.rows), '[]', '顶部下拉框改绑同样必须清空结果行');
    assert.strictEqual(tabA.resultId, '', '顶部下拉框改绑同样必须清空 result_id');
    assert.strictEqual(JSON.stringify(tabA.dirtyCells), '{}', '顶部下拉框改绑同样必须清空网格修改');
    assert.notStrictEqual(tabA.transactionId, 'dbtab-topbar-a', '顶部下拉框改绑必须换新事务 ID');
    assert.strictEqual(tabA.sql, 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42', '顶部下拉框改绑保留 SQL');
    const rollback = env.calls.filter(c => c.path === '/api/database/transaction' && c.body && c.body.action === 'ROLLBACK');
    assert.strictEqual(rollback.length, 1, '顶部下拉框改绑前必须先回滚旧源事务');
    if (clickError) throw clickError;
  });

  // S12: 切到孤儿页签后，state 必须镜像该页签自身的状态（不得把上一个页签的结果带进来）
  await scenario('S12 切到孤儿页签后 state 不得残留其他页签的结果', async () => {
    const env = createDbEnv({ failRollback: true });
    const state = env.state;
    requireFn(env, 'bindSessionSource');
    const SRC_C = { id: 'C', name: 'Production C', kind: 'oracle', username: 'APP', max_rows: 1000 };
    state.sources = [SRC_B, SRC_C];
    state.source = SRC_B;
    requireFn(env, 'render')(env.el('view'));

    const tabA = querySession({
      id: 121,
      sql: 'UPDATE APP.JOBS SET STATUS=1 WHERE ID=42',
      rows: [[42, 'testA']],
      columns: [{ name: 'ID' }],
      summary: { result_id: 'res-A' },
      resultId: 'res-A',
      transactionId: 'dbtab-switch-a',
      transactionPending: true
    });
    env.hooks.bindSessionSource(tabA, SRC_A);
    const tabB = querySession({
      id: 122,
      sql: 'SELECT 1 FROM DUAL',
      rows: [[1, 'b']],
      columns: [{ name: 'X' }],
      summary: { result_id: 'res-B' },
      resultId: 'res-B',
      transactionId: 'dbtab-switch-b'
    });
    env.hooks.bindSessionSource(tabB, SRC_B);
    state.sessions = [tabB, tabA];
    state.activeId = tabB.id;
    requireFn(env, 'reconcileSessionsWithSources')({ deletedId: 'A', deletedRef: { id: 'A', name: 'Production A' } });
    assert.strictEqual(tabA.orphan, true, '前置条件：A 页签为孤儿');

    // 切到孤儿页签（真实 switchSession）
    const switchSession = requireFn(env, 'switchSession');
    switchSession(tabA.id);

    assert.strictEqual(state.activeId, tabA.id, '活动页签应切换到孤儿页签');
    assert.strictEqual(JSON.stringify(state.rows), '[[42,"testA"]]', 'state.rows 必须镜像孤儿页签自己的结果');
    assert.ok(state.summary && state.summary.result_id === 'res-A', 'state.summary 必须镜像孤儿页签自己的 result_id');

    // 顶部下拉框改绑但旧事务回滚失败：结果必须仍是 A 自己的，不能被 B 的结果污染
    const select = env.el('db-source');
    state.source = SRC_B;
    select.value = 'C';
    let clickError = null;
    try { await select.onchange.call(select); } catch (e) { clickError = e; }
    assert.strictEqual(tabA.sourceId, 'A', '回滚失败时不得改绑');
    assert.strictEqual(JSON.stringify(tabA.rows), '[[42,"testA"]]', '失败后不得被其他页签的结果污染');
    assert.ok(tabA.summary && tabA.summary.result_id === 'res-A', '失败后不得被其他页签的 result_id 污染');
    assert.ok(state.summary && state.summary.result_id === 'res-A', 'state 仍须镜像孤儿页签');
    if (clickError) throw clickError;
  });

  console.log('--- DBUI-03 finished: ' + (failures.length ? failures.length + ' failing scenario(s)' : 'all scenarios passed') + ' ---');
  if (failures.length) {
    for (const f of failures) {
      console.error('\n[FAIL] ' + f.name);
      console.error(f.error && f.error.stack ? f.error.stack : f.error);
    }
    process.exitCode = 1;
  }
}

main().catch(error => {
  console.error(error && error.stack ? error.stack : error);
  process.exitCode = 1;
});
