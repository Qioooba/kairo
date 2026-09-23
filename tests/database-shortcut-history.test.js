'use strict';
/*
 * tests/database-shortcut-history.test.js
 * DBUI-05 / DBUI-06 回归。
 *
 * DBUI-05：Ctrl+Shift+F（格式化）被 database-features.js 的 capture 监听当成
 * Ctrl+F（查找）截获。这里把两个模块的真实源码放进同一个 vm，用 DOM 替身按
 * 三种真实的模块/监听挂载顺序驱动真实按键事件，断言哪一个处理器真正执行。
 *
 * DBUI-06：查询历史被合并到错误数据源、被伪标成功、清空后又被旧字符串导回。
 * 这里驱动真实的 loadHistory / deleteHistory / clearHistory / replayHistoryEntry
 * 与页面真实的 loadHistory / pushHistory，断言产出的记录（sourceId/status/elapsedMs）
 * 以及“清空 + 重载后不复活”。
 *
 * 本测试不重写实现：整段真实模块源码在 vm 中执行，DOM/API 替身只提供浏览器接口；
 * 唯一注入是在文件末尾前挂内部引用出口（与 tests/database-orphan-source.test.js
 * 同一做法），用于观察真实函数产出的记录。
 *
 * 复现修复前（2a5108e）的失败，不需要改动工作区：
 *   $tmp = Join-Path $env:TEMP 'kairo-prefix-2a5108e'; New-Item -ItemType Directory -Force $tmp
 *   git archive -o "$tmp\src.tar" HEAD -- web/pages/database.js web/workbench/database-features.js `
 *     web/workbench/statement-model.js web/workbench/sql-format-service.js web/workbench/resource-session.js
 *   tar -xf "$tmp\src.tar" -C $tmp
 *   $env:KAIRO_REGRESSION_ROOT = $tmp; node tests/database-shortcut-history.test.js
 * 修复前基线输出 14 个失败场景，其中 Ctrl+Shift+F 被 feature capture 截获为
 * {"ctrlShiftFInterceptedByFeatureCapture":true,...}，B 的历史里出现
 * {"sourceId":"B",...,"status":"success","elapsedMs":0,"sql":"SELECT A_ONLY FROM TEST_A"}。
 */
const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const ROOT = process.env.KAIRO_REGRESSION_ROOT
  ? path.resolve(process.env.KAIRO_REGRESSION_ROOT)
  : path.join(__dirname, '..');
const read = rel => fs.readFileSync(path.join(ROOT, rel), 'utf8');

const PAGE_SOURCE = read('web/pages/database.js');
const FEATURES_SOURCE = read('web/workbench/database-features.js');
const STATEMENT_SOURCE = read('web/workbench/statement-model.js');
const FORMAT_SOURCE = read('web/workbench/sql-format-service.js');
const RESOURCE_SESSION_SOURCE = read('web/workbench/resource-session.js');

const PAGE_HOOK_MARKER = '  Kairo.state.routes.database = renderDatabase;';
const PAGE_HOOK_CODE = '  window.__dbInternals = {\n'
  + '    state: state, persistGet: function () { return persisted; }, sess: sess, render: render, renderWorkspace: renderWorkspace,\n'
  + '    loadDatabasePreference: loadDatabasePreference, loadHistory: loadHistory, pushHistory: pushHistory,\n'
  + '    refreshHistorySelect: refreshHistorySelect, formatCurrentSQL: formatCurrentSQL,\n'
  + '    bindSessionSource: bindSessionSource, savePersisted: savePersisted, effectiveSource: effectiveSource\n'
  + '  };\n';
const FEATURES_HOOK_MARKER = '  F.tokenizeSQL = tokenizeSQL;';
const FEATURES_HOOK_CODE = '  F.__historyState = state;\n';

const SRC_A = { id: 'A', name: 'Source A', kind: 'oracle', username: 'APP', max_rows: 1000, allow_ddl: false, read_only: false };
const SRC_B = { id: 'B', name: 'Source B', kind: 'oracle', username: 'APP', max_rows: 1000, allow_ddl: false, read_only: false };

// ---------------------------------------------------------------- DOM 替身

// 所有由真实代码创建/查询到的元素（含 querySelector 返回的节点），用于检查
// 真实弹窗写入的 HTML。
const ALL_ELEMENTS = [];

function makeElement(id, tagName) {
  const attrs = {};
  const classes = new Set();
  const listeners = {};
  const queries = {};
  const el = {
    id: id || '', tagName: String(tagName || 'DIV').toUpperCase(), nodeType: 1,
    innerHTML: '', textContent: '', value: '', className: '', title: '', hidden: false,
    disabled: false, checked: false, required: false, placeholder: '', type: '',
    scrollTop: 0, scrollLeft: 0, offsetHeight: 0, clientHeight: 0, clientWidth: 400,
    selectionStart: 0, selectionEnd: 0, files: [], options: [], children: [], childNodes: [],
    style: {}, dataset: {}, isContentEditable: false,
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
    setCustomValidity: () => {}, checkValidity: () => true, reportValidity: () => {},
    addEventListener: (t, fn) => { (listeners[t] = listeners[t] || []).push(fn); },
    removeEventListener: (t, fn) => { if (listeners[t]) listeners[t] = listeners[t].filter(f => f !== fn); },
    dispatchEvent: ev => { (listeners[ev && ev.type] || []).forEach(fn => fn(ev)); return true; },
    appendChild: c => { el.children.push(c); el.childNodes.push(c); if (c) c.parentElement = el; return c; },
    insertBefore: c => { el.children.push(c); return c; },
    replaceChildren: () => { el.children.length = 0; },
    removeChild: c => { const i = el.children.indexOf(c); if (i >= 0) el.children.splice(i, 1); return c; },
    remove: () => {},
    focus: () => {}, blur: () => {}, select: () => {}, click: () => {},
    setSelectionRange: (s, e) => { el.selectionStart = s; el.selectionEnd = e; },
    setRangeText: (text, start, end) => {
      el.value = String(el.value).slice(0, start) + text + String(el.value).slice(end);
      el.selectionStart = start;
      el.selectionEnd = start + String(text).length;
    },
    scrollIntoView: () => {},
    getBoundingClientRect: () => ({ top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0 }),
    getContext: () => ({ font: '', measureText: () => ({ width: 0 }) }),
    closest: selector => {
      const parts = String(selector).split(',').map(s => s.trim()).filter(Boolean);
      let node = el;
      while (node) {
        for (const part of parts) {
          if (part.charAt(0) === '#' && node.id === part.slice(1)) return node;
          if (part.charAt(0) === '.' && node.classList.contains(part.slice(1))) return node;
          if (part.charAt(0) !== '#' && part.charAt(0) !== '.' && node.tagName === part.toUpperCase()) return node;
        }
        node = node.parentElement;
      }
      return null;
    },
    querySelector: selector => {
      if (!queries[selector]) {
        const child = makeElement('', 'DIV');
        child.parentElement = el;
        queries[selector] = child;
      }
      return queries[selector];
    },
    querySelectorAll: () => [],
    contains: () => false,
    get isConnected() { return true; },
    parentElement: null, parentNode: null, firstChild: null, lastChild: null
  };
  Object.defineProperty(el, 'selectedOptions', {
    get: function () {
      const options = Array.isArray(el.options) ? el.options : [];
      const match = options.filter(o => String(o.value) === String(el.value));
      return match.length ? match : (options.length ? [options[0]] : []);
    }
  });
  ALL_ELEMENTS.push(el);
  return el;
}

function createStorage(seed) {
  const data = Object.assign({}, seed || {});
  return {
    _data: data,
    getItem: k => (k in data ? data[k] : null),
    setItem: (k, v) => { data[k] = String(v); },
    removeItem: k => { delete data[k]; },
    key: i => Object.keys(data)[i] || null,
    get length() { return Object.keys(data).length; }
  };
}

function makeKeyEvent(spec) {
  return {
    type: 'keydown',
    key: spec.key || '',
    code: spec.code || '',
    ctrlKey: !!spec.ctrlKey, altKey: !!spec.altKey, shiftKey: !!spec.shiftKey, metaKey: !!spec.metaKey,
    isComposing: !!spec.isComposing, keyCode: spec.keyCode || 0,
    target: spec.target || null,
    defaultPrevented: false, __stopped: false,
    preventDefault: function () { this.defaultPrevented = true; },
    stopPropagation: function () { this.__propagationStopped = true; },
    stopImmediatePropagation: function () { this.__stopped = true; }
  };
}

function CustomEventDouble(type, init) { this.type = type; this.detail = init && init.detail; }
function EventDouble(type, init) { this.type = type; this.bubbles = !!(init && init.bubbles); }

function createEnv(options) {
  const opts = options || {};
  const elements = new Map();
  const created = [];
  const toasts = [];
  const apiCalls = [];
  const copies = [];
  const promptAnswers = [];
  const storage = opts.storage || createStorage(opts.storageSeed);
  const shared = { prefs: opts.prefs || null };
  const documentListeners = [];
  const windowListeners = {};

  const el = id => {
    if (!elements.has(id)) elements.set(id, makeElement(id));
    return elements.get(id);
  };

  const api = async function (method, p, body) {
    apiCalls.push({ method: method, path: p, body: body });
    if (method === 'GET' && p === '/api/database/sources') return { sources: (opts.sources || [SRC_A, SRC_B]).slice() };
    if (p && String(p).indexOf('/api/database/metadata/schemas') === 0) return { schemas: [{ name: 'APP' }] };
    if (p && String(p).indexOf('/api/database/metadata/objects') === 0) return { objects: [] };
    if (p === '/api/database/transaction') return { ok: true };
    return {};
  };

  const body = makeElement('body', 'BODY');
  const documentDouble = {
    readyState: 'loading', hidden: false, activeElement: null,
    body: body, documentElement: makeElement('html', 'HTML'),
    getElementById: id => el(id),
    createElement: tag => { const node = makeElement('', tag); created.push(node); return node; },
    createTextNode: t => ({ nodeType: 3, textContent: String(t) }),
    querySelector: () => null,
    querySelectorAll: () => [],
    createEvent: type => ({ type: type, initEvent: () => {}, initCustomEvent: () => {} }),
    addEventListener: (type, fn, capture) => { documentListeners.push({ type: type, fn: fn, capture: !!capture }); },
    removeEventListener: (type, fn, capture) => {
      const index = documentListeners.findIndex(l => l.type === type && l.fn === fn && l.capture === !!capture);
      if (index >= 0) documentListeners.splice(index, 1);
    }
  };
  // document 上的 capture 监听按注册顺序触发；stopImmediatePropagation 之后
  // 不再触发任何监听（含目标元素自身的 onkeydown），与真实捕获阶段一致。
  documentDouble.dispatchKeydown = function (event) {
    const list = documentListeners.filter(l => l.type === 'keydown');
    for (const entry of list) {
      if (event.__stopped) break;
      if (entry.capture) entry.fn(event);
    }
    if (!event.__stopped && event.target && typeof event.target.onkeydown === 'function') event.target.onkeydown(event);
    if (!event.__stopped) {
      for (const entry of list) {
        if (!entry.capture) entry.fn(event);
      }
    }
    return event;
  };
  documentDouble.keydownListenerCount = function () {
    return documentListeners.filter(l => l.type === 'keydown').length;
  };

  const windowDouble = {
    Kairo: {
      core: {
        toast: (msg, type) => toasts.push({ msg: String(msg), type: type || 'info' }),
        escapeHtml: value => String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
        copyToClipboard: value => { copies.push(String(value)); return Promise.resolve(true); },
        el: () => null
      },
      api: {
        api: api,
        getPreference: async () => ({ exists: !!shared.prefs, value: shared.prefs }),
        putPreference: async (key, value) => { shared.prefs = value; },
        preferenceSaver: () => function (value) { shared.prefs = value; }
      },
      state: { routes: {}, routeNames: {}, routeSubs: {} },
      database: {},
      databaseFeatures: {},
      workbench: {},
      overlays: {
        prompt: async request => {
          promptAnswers.push(request);
          return typeof opts.promptAnswer === 'function' ? opts.promptAnswer(request) : (opts.promptAnswer == null ? '' : opts.promptAnswer);
        }
      },
      auth: { getUser: () => 'admin', getRole: () => 'admin' }
    },
    addEventListener: (type, fn) => { (windowListeners[type] = windowListeners[type] || []).push(fn); },
    removeEventListener: (type, fn) => { if (windowListeners[type]) windowListeners[type] = windowListeners[type].filter(f => f !== fn); },
    dispatchEvent: event => {
      (windowListeners[event && event.type] || []).slice().forEach(fn => fn(event));
      return true;
    },
    innerWidth: 1366, innerHeight: 768,
    localStorage: storage,
    sessionStorage: createStorage(),
    location: { hash: '#/database', href: 'http://127.0.0.1:18092/#/database' },
    navigator: { platform: opts.platform || 'Win32', userAgent: opts.platform === 'MacIntel' ? 'Macintosh' : 'Windows', hardwareConcurrency: 8 },
    document: documentDouble,
    confirm: () => true,
    prompt: () => '',
    fetch: () => Promise.reject(new Error('offline test double')),
    requestAnimationFrame: () => 0,
    cancelAnimationFrame: () => {},
    setTimeout: setTimeout, clearTimeout: clearTimeout,
    setInterval: () => 0, clearInterval: () => {},
    MutationObserver: function () { this.observe = () => {}; this.disconnect = () => {}; this.takeRecords = () => []; },
    performance: { now: () => Date.now() },
    AbortController: AbortController,
    Blob: Blob,
    URL: URL,
    URLSearchParams: URLSearchParams,
    CustomEvent: CustomEventDouble,
    Event: EventDouble,
    getComputedStyle: () => ({ lineHeight: '21px' }),
    Set: Set, Map: Map, Promise: Promise, JSON: JSON, Math: Math, Date: Date, Number: Number,
    console: console
  };

  const ctx = {
    window: windowDouble,
    document: documentDouble,
    location: windowDouble.location,
    navigator: windowDouble.navigator,
    localStorage: storage,
    sessionStorage: windowDouble.sessionStorage,
    confirm: windowDouble.confirm,
    prompt: windowDouble.prompt,
    fetch: windowDouble.fetch,
    requestAnimationFrame: windowDouble.requestAnimationFrame,
    cancelAnimationFrame: windowDouble.cancelAnimationFrame,
    setTimeout: setTimeout, clearTimeout: clearTimeout,
    setInterval: windowDouble.setInterval, clearInterval: windowDouble.clearInterval,
    MutationObserver: windowDouble.MutationObserver,
    performance: windowDouble.performance,
    AbortController: AbortController, Blob: Blob, URL: URL, URLSearchParams: URLSearchParams,
    CustomEvent: CustomEventDouble, Event: EventDouble,
    getComputedStyle: windowDouble.getComputedStyle,
    Set: Set, Map: Map, Promise: Promise, JSON: JSON, Math: Math, Date: Date, Number: Number,
    console: console
  };
  vm.createContext(ctx);

  function loadFeaturesModule() {
    vm.runInContext(FEATURES_SOURCE.replace(FEATURES_HOOK_MARKER, FEATURES_HOOK_CODE + FEATURES_HOOK_MARKER), ctx);
    env.features = windowDouble.Kairo.databaseFeatures;
    return env.features;
  }
  function loadPageModule() {
    vm.runInContext(RESOURCE_SESSION_SOURCE, ctx);
    vm.runInContext(STATEMENT_SOURCE, ctx);
    vm.runInContext(FORMAT_SOURCE, ctx);
    vm.runInContext(PAGE_SOURCE.replace(PAGE_HOOK_MARKER, PAGE_HOOK_CODE + PAGE_HOOK_MARKER), ctx);
    env.hooks = windowDouble.__dbInternals;
    return env.hooks;
  }

  const env = {
    ctx: ctx,
    window: windowDouble,
    document: documentDouble,
    features: null,
    hooks: null,
    el: el,
    created: created,
    toasts: toasts,
    apiCalls: apiCalls,
    copies: copies,
    prompts: promptAnswers,
    storage: storage,
    shared: shared,
    loadFeatures: loadFeaturesModule,
    startFeatures: function () { env.features.start(); return env.features; },
    keydownListenerCount: () => documentDouble.keydownListenerCount(),
    pressKey: function (spec) {
      const event = makeKeyEvent(spec);
      documentDouble.dispatchKeydown(event);
      return event;
    },
    overlays: function () {
      return (body.children || []).filter(child => String(child.className || '').indexOf('db-pro-modal-overlay') >= 0);
    },
    findOpened: function () {
      return (body.children || []).some(child => String(child.className || '').indexOf('db-pro-modal-overlay') >= 0)
        || created.some(node => String(node.innerHTML || '').indexOf('db-pro-find') >= 0 && String(node.innerHTML || '').indexOf('查找') >= 0);
    },
    setSources: function () {
      const select = el('db-source');
      select.options = [
        { value: 'A', textContent: 'Source A', dataset: {}, getAttribute: () => null },
        { value: 'B', textContent: 'Source B', dataset: {}, getAttribute: () => null }
      ];
      select.value = (env.hooks.state.source && env.hooks.state.source.id) || 'A';
    }
  };

  assert.ok(PAGE_SOURCE.indexOf(PAGE_HOOK_MARKER) >= 0, 'database.js hook marker must exist');
  assert.ok(FEATURES_SOURCE.indexOf(FEATURES_HOOK_MARKER) >= 0, 'features hook marker must exist');

  // index.html 的真实顺序是 database.js 先、database-features.js 后；也可能相反。
  // page-render-first 表示工作台已经渲染完（页面已挂兜底 capture）之后才加载 features。
  if (opts.order === 'page-only' || opts.order === 'page-render-first' || opts.order === 'page-first') {
    loadPageModule();
    if (opts.order === 'page-first') loadFeaturesModule();
  } else {
    loadFeaturesModule();
    loadPageModule();
  }
  return env;
}

// 启动一个真实渲染出来的数据库工作台（页签已绑定数据源）。
function bootWorkspace(env, source, sql) {
  const state = env.hooks.state;
  state.sources = [SRC_A, SRC_B];
  state.source = source || SRC_A;
  state.prefs = Object.assign({}, state.prefs, {
    shortcuts: { run: 'Ctrl+Enter', cancel: 'Escape', grid: 'Alt+1', record: 'Alt+2', explain: 'Ctrl+Alt+P', objects: 'Alt+O', format: 'Ctrl+Shift+F' }
  });
  env.hooks.render(env.el('view'));
  env.setSources();
  const box = env.el('db-sql');
  box.value = sql == null ? '' : String(sql);
  box.selectionStart = 0;
  box.selectionEnd = 0;
  const session = env.hooks.sess();
  if (session) session.sql = box.value;
  const rows = env.el('db-max-rows');
  if (!rows.value) rows.value = '1000';
  return session;
}

function addRun(env, sql, runId, source, result) {
  const features = env.features;
  features.recordQueryStart(sql, runId, { sourceId: source.id, sourceName: source.name, dialect: source.kind });
  if (result) features.recordQueryResult(runId, Object.assign({ sql: sql, sourceId: source.id }, result));
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

function records(entries) {
  return entries.map(entry => ({
    sourceId: entry.sourceId, sourceName: entry.sourceName, status: entry.status,
    elapsedMs: entry.elapsedMs == null ? null : entry.elapsedMs,
    startedAt: entry.startedAt == null ? null : entry.startedAt,
    sql: entry.sql
  }));
}

// 打开真实结构化弹窗，并读取它此刻展示的记录（弹窗内部调用真实 loadHistory）。
function dialogHistory(env) {
  env.features.openHistory();
  return env.features.__historyState.history.slice();
}

// 点击真实弹窗底部以指定文字开头的动作按钮（真 handler + 真 onClick）。
function clickDialogAction(env, textPrefix) {
  const buttons = ALL_ELEMENTS.filter(node => node.tagName === 'BUTTON' && String(node.textContent || '').indexOf(textPrefix) === 0);
  assert.ok(buttons.length, '必须存在以“' + textPrefix + '”开头的真实弹窗按钮');
  const button = buttons[buttons.length - 1];
  button.dispatchEvent({ type: 'click' });
  return button;
}

// 三种真实的监听挂载顺序：features 先启动 / 页面先渲染 / 模块最后加载。
function mountVariants(scenarioName, body) {
  return [
    { label: 'features-first', prepare: env => { env.startFeatures(); bootWorkspace(env, SRC_A); }, order: 'features-first' },
    { label: 'page-first', prepare: env => { bootWorkspace(env, SRC_A); env.startFeatures(); }, order: 'page-first' },
    { label: 'page-render-first', prepare: env => { bootWorkspace(env, SRC_A); env.loadFeatures(); env.startFeatures(); }, order: 'page-render-first' }
  ].map(variant => scenario(scenarioName + '[' + variant.label + ']', () => body.bind(null, variant)()));
}

// ------------------------------------------------------------- DBUI-05 场景

async function shortcutScenarios() {
  console.log('--- DBUI-05 快捷键分发 ---');

  // 每个挂载顺序都必须在同一个真实事件上断言“哪个处理器执行了”。
  mountVariants('S1', async variant => {
    const env = createEnv({ order: variant.order });
    variant.prepare(env);
    const box = env.el('db-sql');
    box.value = 'select a,b from t where id=1';
    box.selectionStart = 0;
    box.selectionEnd = 0;
    const before = box.value;

    const shiftF = env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, target: box });
    const formatted = box.value !== before;
    const findOpenedOnShiftF = env.findOpened();
    console.log('    [' + variant.label + '] Ctrl+Shift+F 事件快照: ' + JSON.stringify({
      ctrlShiftFInterceptedByFeatureCapture: findOpenedOnShiftF,
      prevented: shiftF.defaultPrevented,
      stoppedBeforeWorkbenchFormatter: findOpenedOnShiftF || !formatted,
      formatted: formatted
    }));
    assert.strictEqual(findOpenedOnShiftF, false, 'Ctrl+Shift+F 不得打开查找框');
    assert.strictEqual(formatted, true, 'Ctrl+Shift+F 必须格式化编辑器内容');

    const valueAfterFormat = box.value;
    const find = env.pressKey({ key: 'f', code: 'KeyF', ctrlKey: true, target: box });
    assert.strictEqual(find.defaultPrevented, true, 'Ctrl+F 必须被分发器处理');
    assert.strictEqual(env.findOpened(), true, 'Ctrl+F 必须打开查找框');
    assert.strictEqual(box.value, valueAfterFormat, 'Ctrl+F 不得改动编辑器内容（不得触发格式化）');
    env.features.closeModal();
    if (env.prompts.length) throw new Error('不应询问数据源');
  });

  await scenario('S1-registry 两个模块的命令注册进同一个分发器且只保留一个 capture 监听', async () => {
    for (const order of ['features-first', 'page-first', 'page-render-first']) {
      const env = createEnv({ order: order });
      if (order === 'features-first') env.startFeatures();
      bootWorkspace(env, SRC_A);
      if (order !== 'features-first') {
        if (!env.features) env.loadFeatures();
        env.startFeatures();
      }
      const names = env.features.commands.names();
      assert.ok(names.indexOf('db.format') >= 0, '[' + order + '] 页面格式化命令必须注册进统一分发器：' + JSON.stringify(names));
      assert.ok(names.indexOf('db.run') >= 0, '[' + order + '] 执行命令必须注册进统一分发器：' + JSON.stringify(names));
      assert.ok(names.indexOf('db.find') >= 0, '[' + order + '] 查找命令必须注册进统一分发器：' + JSON.stringify(names));
      assert.strictEqual(env.keydownListenerCount(), 1, '[' + order + '] 最终只允许一个 document keydown 监听，实际 ' + env.keydownListenerCount());
    }
  });

  await scenario('S2 平台修饰键：macOS 用 Cmd，Windows 不接受 Meta，且修饰键精确匹配', async () => {
    const mac = createEnv({ order: 'features-first', platform: 'MacIntel' });
    mac.startFeatures();
    bootWorkspace(mac, SRC_A, 'select a from t');
    const macBox = mac.el('db-sql');
    const macBefore = macBox.value;
    mac.pressKey({ key: 'F', code: 'KeyF', metaKey: true, shiftKey: true, target: macBox });
    assert.notStrictEqual(macBox.value, macBefore, 'macOS Cmd+Shift+F 必须格式化');
    assert.strictEqual(mac.findOpened(), false, 'macOS Cmd+Shift+F 不得打开查找');
    mac.pressKey({ key: 'f', code: 'KeyF', metaKey: true, target: macBox });
    assert.strictEqual(mac.findOpened(), true, 'macOS Cmd+F 必须打开查找');
    mac.features.closeModal();

    const win = createEnv({ order: 'features-first', platform: 'Win32' });
    win.startFeatures();
    bootWorkspace(win, SRC_A, 'select a from t');
    const winBox = win.el('db-sql');
    const winBefore = winBox.value;
    win.pressKey({ key: 'f', code: 'KeyF', metaKey: true, target: winBox });
    assert.strictEqual(win.findOpened(), false, 'Windows 上 Meta+F 不得当作 Ctrl+F');
    win.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, altKey: true, shiftKey: true, target: winBox });
    assert.strictEqual(winBox.value, winBefore, 'Ctrl+Alt+Shift+F 必须因修饰键不精确匹配而不触发格式化');
    assert.strictEqual(win.findOpened(), false, 'Ctrl+Alt+Shift+F 也不得打开查找');

    const linux = createEnv({ order: 'features-first', platform: 'Linux x86_64' });
    linux.startFeatures();
    bootWorkspace(linux, SRC_A, 'select a from t');
    const linBox = linux.el('db-sql');
    const linBefore = linBox.value;
    linux.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, target: linBox });
    assert.notStrictEqual(linBox.value, linBefore, 'Linux Ctrl+Shift+F 必须格式化');
    assert.strictEqual(linux.findOpened(), false, 'Linux Ctrl+Shift+F 不得打开查找');
    linux.pressKey({ key: 'f', code: 'KeyF', ctrlKey: true, target: linBox });
    assert.strictEqual(linux.findOpened(), true, 'Linux Ctrl+F 必须打开查找');
  });

  await scenario('S3 IME 组合输入期间格式化与执行都不触发', async () => {
    const env = createEnv({ order: 'features-first' });
    env.startFeatures();
    bootWorkspace(env, SRC_A, 'select a from t');
    const box = env.el('db-sql');
    const before = box.value;
    const historyBefore = env.features.listHistory('A').length;

    env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, isComposing: true, target: box });
    env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, keyCode: 229, target: box });
    env.pressKey({ key: 'f', code: 'KeyF', ctrlKey: true, isComposing: true, target: box });
    env.pressKey({ key: 'Enter', code: 'Enter', ctrlKey: true, isComposing: true, target: box });

    assert.strictEqual(box.value, before, 'IME 组合中不得格式化');
    assert.strictEqual(env.findOpened(), false, 'IME 组合中不得打开查找');
    assert.strictEqual(env.features.listHistory('A').length, historyBefore, 'IME 组合中不得执行查询');
    env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, target: box });
    assert.notStrictEqual(box.value, before, '组合结束后 Ctrl+Shift+F 必须恢复格式化');
  });

  await scenario('S4 参数弹窗打开时格式化与执行都不误触', async () => {
    const env = createEnv({ order: 'features-first' });
    env.startFeatures();
    bootWorkspace(env, SRC_A, 'select * from t where id = :id');
    const box = env.el('db-sql');
    const before = box.value;

    // 正向对照：先证明执行快捷键真的会写入历史（否则下面的“没误触”没有意义）
    env.pressKey({ key: 'Enter', code: 'Enter', ctrlKey: true, target: box });
    const running = env.features.listHistory('A').filter(entry => entry.status === 'running').length;
    assert.strictEqual(running, 1, 'Ctrl+Enter 必须真的启动查询（前置条件），实际 ' + running);

    env.features.openParameterDialog();
    assert.ok(env.overlays().length > 0, '参数弹窗必须已打开');
    const historyDuringDialog = env.features.listHistory('A').length;
    box.value = before;

    env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, target: box });
    env.pressKey({ key: 'Enter', code: 'Enter', ctrlKey: true, target: box });
    env.pressKey({ key: 'f', code: 'KeyF', ctrlKey: true, target: box });

    assert.strictEqual(box.value, before, '参数弹窗打开时不得格式化编辑器');
    assert.strictEqual(env.features.listHistory('A').length, historyDuringDialog, '参数弹窗打开时不得执行查询');
    env.features.closeModal();
  });

  await scenario('S5 选区格式化只改选区，全文格式化按钮仍有效', async () => {
    const env = createEnv({ order: 'features-first' });
    env.startFeatures();
    const original = 'select a from t;select b,c from t where id=1';
    bootWorkspace(env, SRC_A, original);
    const box = env.el('db-sql');
    const start = original.indexOf('select b');
    box.selectionStart = start;
    box.selectionEnd = original.length;
    const service = env.window.Kairo.workbench.sqlFormatService;
    const expectedSlice = service.formatSQL(original.slice(start));

    env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, target: box });

    assert.strictEqual(box.value.slice(0, start), original.slice(0, start), '选区格式化不得改动选区之前的文本');
    assert.strictEqual(box.value.slice(start), expectedSlice, '选区格式化必须只把选区替换为格式化后的选区');
    assert.strictEqual(env.findOpened(), false, '选区格式化不得打开查找');

    const button = env.el('db-format-all');
    assert.strictEqual(typeof button.onclick, 'function', '全文格式化按钮必须已绑定真实处理器');
    button.onclick();
    box.selectionStart = 0;
    box.selectionEnd = 0;
    const fullExpected = service.formatSQL(box.value);
    assert.strictEqual(box.value, fullExpected, '全文格式化按钮必须格式化全文');
  });

  await scenario('S6 兜底路径：features 未加载时页面监听仍能格式化且不误开查找', async () => {
    const env = createEnv({ order: 'page-only' });
    bootWorkspace(env, SRC_A, 'select a from t');
    const box = env.el('db-sql');
    const before = box.value;
    assert.strictEqual(env.keydownListenerCount(), 1, '兜底路径必须挂上页面自己的 capture 监听');
    const event = env.pressKey({ key: 'F', code: 'KeyF', ctrlKey: true, shiftKey: true, target: box });
    assert.strictEqual(event.defaultPrevented, true, '兜底监听必须处理 Ctrl+Shift+F');
    assert.notStrictEqual(box.value, before, '兜底路径必须能格式化');
    assert.strictEqual(env.findOpened(), false, '兜底路径不得打开查找');
  });
}

// ------------------------------------------------------------- DBUI-06 场景

const LEGACY_STORE = {
  version: 2, migration: { legacyStrings: 1 },
  buckets: {
    '__legacy__': [{
      id: 'legacy-seed', sourceId: '', sourceName: '旧版历史／来源未知', status: 'unknown',
      startedAt: null, endedAt: null, elapsedMs: null, rows: null, error: '', sql: 'SELECT LEGACY FROM DUAL'
    }]
  }
};

async function historyScenarios() {
  console.log('--- DBUI-06 查询历史 ---');

  await scenario('S10 A 的旧字符串历史不得变成 B 的成功查询，且只迁移一次到“来源未知”桶', async () => {
    const storage = createStorage();
    const env = createEnv({
      order: 'features-first',
      storage: storage,
      prefs: { prefs: null, history: ['SELECT A_ONLY FROM TEST_A'], last_source: 'A', column_widths: {}, row_limits: {} }
    });
    await env.hooks.loadDatabasePreference();
    assert.strictEqual(env.hooks.persistGet().history.length, 1, '前置条件：旧版字符串历史含 A 的语句');
    env.startFeatures();
    bootWorkspace(env, SRC_A, '');
    addRun(env, 'SELECT A_ONLY FROM TEST_A', 'run-A1', SRC_A, { status: 'success', elapsedMs: 12, rows: 3 });

    env.hooks.state.source = SRC_B;
    env.el('db-source').value = 'B';
    // 驱动真实结构化弹窗（内部调用真实 loadHistory）读取 B 的历史
    env.features.openHistory();
    const bHistory = env.features.__historyState.history.slice();
    console.log('    加载 B 的结构化历史记录: ' + JSON.stringify(records(bHistory)));

    assert.strictEqual(bHistory.length, 0, 'B 的历史不得包含 A 的语句：' + JSON.stringify(records(bHistory)));
    assert.ok(!bHistory.some(entry => entry.status === 'success' && entry.elapsedMs === 0), '不得出现“零耗时 + 成功”的伪造记录');

    const legacyBucket = env.features.listHistory(env.features.LEGACY_HISTORY_BUCKET);
    assert.strictEqual(legacyBucket.length, 1, '旧版历史必须迁移进“旧版历史／来源未知”桶');
    assert.strictEqual(legacyBucket[0].sourceId, '', '旧版历史不得编造数据源');
    assert.strictEqual(legacyBucket[0].sourceName, '旧版历史／来源未知', '旧版历史必须标注来源未知');
    assert.strictEqual(legacyBucket[0].status, 'unknown', '旧版历史不得伪标成功，实际 ' + legacyBucket[0].status);
    assert.strictEqual(legacyBucket[0].elapsedMs, null, '旧版历史不得编造执行耗时');
    assert.strictEqual(legacyBucket[0].startedAt, null, '旧版历史不得编造执行时间');

    env.features.loadHistory('A');
    env.features.loadHistory('B');
    assert.strictEqual(env.features.listHistory(env.features.LEGACY_HISTORY_BUCKET).length, 1, '旧版历史只能迁移一次');

    const aHistory = env.features.loadHistory('A');
    assert.deepStrictEqual(aHistory.map(entry => entry.sql), ['SELECT A_ONLY FROM TEST_A'], 'A 自己执行过的语句仍归 A');
    assert.strictEqual(aHistory[0].status, 'success', 'A 的真实成功记录保持 success');
    assert.strictEqual(aHistory[0].elapsedMs, 12, 'A 的真实耗时必须保留');
  });

  await scenario('S11 清空 B 的历史并刷新页面后不得复活', async () => {
    const storage = createStorage();
    const prefs = { prefs: null, history: ['SELECT A_ONLY FROM TEST_A'], last_source: 'A', column_widths: {}, row_limits: {} };
    const env = createEnv({ order: 'features-first', storage: storage, prefs: prefs });
    await env.hooks.loadDatabasePreference();
    env.startFeatures();
    bootWorkspace(env, SRC_B, '');
    env.el('db-source').value = 'B';
    addRun(env, 'SELECT B_ONLY FROM TEST_B', 'run-B1', SRC_B, { status: 'success', elapsedMs: 5, rows: 1 });
    assert.ok(dialogHistory(env).some(entry => entry.sql === 'SELECT B_ONLY FROM TEST_B'), '前置条件：B 有一条自己的历史');

    // 真实结构化弹窗的“清空”动作按钮
    clickDialogAction(env, '清空');
    const cleared = dialogHistory(env);
    console.log('    清空后 B 的结构化历史: ' + JSON.stringify(records(cleared)));
    assert.strictEqual(cleared.length, 0, '清空后当前数据源的历史必须为空');
    if (typeof env.features.listHistory === 'function') {
      assert.strictEqual(env.features.listHistory('B').length, 0, '清空后结构化仓库必须为空');
    }
    assert.deepStrictEqual(env.hooks.loadHistory(), [], '页面旧版下拉必须同步为空');
    env.features.closeModal();
    assert.strictEqual(dialogHistory(env).length, 0, '重新打开弹窗不得复活已清空的历史');

    // 刷新页面：同一份 localStorage（v2 仓库 + 迁移标记）与同一份后端偏好
    const reloaded = createEnv({ order: 'features-first', storage: storage, prefs: prefs });
    await reloaded.hooks.loadDatabasePreference();
    reloaded.startFeatures();
    bootWorkspace(reloaded, SRC_B, '');
    reloaded.el('db-source').value = 'B';
    const afterReload = dialogHistory(reloaded);
    console.log('    刷新后 B 的结构化历史: ' + JSON.stringify(records(afterReload)));
    assert.strictEqual(afterReload.length, 0, '刷新后不得重新导入旧字符串历史');
    if (typeof reloaded.features.listHistory === 'function') {
      assert.strictEqual(reloaded.features.listHistory(reloaded.features.LEGACY_HISTORY_BUCKET).length, 1, '刷新后旧版桶仍只迁移一次');
      reloaded.features.loadHistory('A');
      assert.deepStrictEqual(reloaded.features.loadHistory('B'), [], '切源再回来不得复活 B 已清空的历史');
    }
  });

  await scenario('S12 失败与取消的执行不得被改写成成功', async () => {
    const env = createEnv({
      order: 'features-first',
      storage: createStorage(),
      prefs: { prefs: null, history: ['SELECT BAD FROM NOPE'], last_source: 'A', column_widths: {}, row_limits: {} }
    });
    await env.hooks.loadDatabasePreference();
    env.startFeatures();
    bootWorkspace(env, SRC_A, '');
    addRun(env, 'SELECT BAD FROM NOPE', 'run-F1', SRC_A, { status: 'error', error: 'ORA-00942: table or view does not exist' });
    const failed = env.features.loadHistory('A');
    assert.strictEqual(failed.length, 1, '前置条件：失败执行有一条记录');
    assert.strictEqual(failed[0].status, 'error', '失败执行必须保持 error，实际 ' + failed[0].status);
    assert.ok(failed[0].error.indexOf('ORA-00942') >= 0, '失败原因必须保留');

    env.features.deleteHistory(failed[0].id, 'A');
    const afterDelete = env.features.loadHistory('A');
    console.log('    删除失败记录后 loadHistory("A"): ' + JSON.stringify(records(afterDelete)));
    assert.ok(!afterDelete.some(entry => entry.status === 'success' && entry.sql === 'SELECT BAD FROM NOPE'), '删除后旧字符串不得以 success 重新出现');
    assert.strictEqual(afterDelete.length, 0, '删除后 A 不应再有该记录');

    addRun(env, 'SELECT SLOW FROM DUAL', 'run-C1', SRC_A, { status: 'canceled', error: '已取消' });
    const canceled = env.features.loadHistory('A').filter(entry => entry.sql === 'SELECT SLOW FROM DUAL');
    assert.strictEqual(canceled.length, 1, '取消的执行必须有一条记录');
    assert.strictEqual(canceled[0].status, 'canceled', '取消的执行不得被改写成成功，实际 ' + canceled[0].status);
  });

  await scenario('S13 历史重放必须保留原 sourceId，来源未知/已删除时必须显式选择', async () => {
    const env = createEnv({ order: 'features-first', promptAnswer: 'B' });
    env.startFeatures();
    bootWorkspace(env, SRC_B, '');
    env.el('db-source').value = 'B';
    addRun(env, 'SELECT B_ONLY FROM TEST_B', 'run-B2', SRC_B, { status: 'success', elapsedMs: 3, rows: 1 });
    const bEntry = env.features.loadHistory('B')[0];

    const urlB = await env.features.replayHistoryEntry(bEntry);
    assert.ok(urlB.indexOf('source=B') >= 0, '重放必须保留原 sourceId，实际 ' + urlB);
    assert.strictEqual(env.prompts.length, 0, '来源明确的记录不得再询问用户');
    assert.ok(env.copies[env.copies.length - 1].indexOf('source=B') >= 0, '复制的深链接必须带原 sourceId');

    // 来源未知（旧版桶）：必须让用户显式选择
    env.storage.setItem('kairo:database:history:v2', JSON.stringify(LEGACY_STORE));
    const unknownEntry = env.features.listHistory(env.features.LEGACY_HISTORY_BUCKET)[0];
    assert.strictEqual(unknownEntry.sourceId, '', '前置条件：该记录没有来源');
    const beforePrompt = env.prompts.length;
    const urlUnknown = await env.features.replayHistoryEntry(unknownEntry);
    assert.strictEqual(env.prompts.length, beforePrompt + 1, '来源未知必须要求用户显式选择');
    assert.ok(urlUnknown.indexOf('source=B') >= 0, '用户确认后必须使用用户选择的数据源');

    // 用户取消选择：不得生成任何执行链接
    const cancelling = createEnv({ order: 'features-first', promptAnswer: '' });
    cancelling.startFeatures();
    bootWorkspace(cancelling, SRC_B, '');
    cancelling.el('db-source').value = 'B';
    cancelling.storage.setItem('kairo:database:history:v2', JSON.stringify(LEGACY_STORE));
    const copiesBefore = cancelling.copies.length;
    const urlCancelled = await cancelling.features.replayHistoryEntry(cancelling.features.listHistory(cancelling.features.LEGACY_HISTORY_BUCKET)[0]);
    assert.strictEqual(urlCancelled, '', '用户取消后不得生成链接');
    assert.strictEqual(cancelling.copies.length, copiesBefore, '用户取消后不得复制任何链接');

    // 原数据源已删除：同样要求显式选择，且绝不带错误的 sourceId
    const deletedSource = createEnv({ order: 'features-first', promptAnswer: 'B' });
    deletedSource.startFeatures();
    bootWorkspace(deletedSource, SRC_B, '');
    deletedSource.el('db-source').value = 'B';
    const orphanEntry = { id: 'q-x', sourceId: 'DELETED', sourceName: '已删除的源', status: 'success', elapsedMs: 0, sql: 'SELECT 1 FROM DUAL' };
    const urlOrphan = await deletedSource.features.replayHistoryEntry(orphanEntry);
    assert.ok(deletedSource.prompts.length >= 1, '原数据源已删除时必须要求用户显式选择');
    assert.ok(urlOrphan.indexOf('source=B') >= 0, '必须使用用户确认的数据源');
    assert.ok(urlOrphan.indexOf('source=DELETED') < 0, '绝不生成带错误 sourceId 的链接');
  });

  await scenario('S14 页面旧版下拉与结构化弹窗读取同一份仓库、不串源', async () => {
    const env = createEnv({ order: 'features-first' });
    env.startFeatures();
    bootWorkspace(env, SRC_A, '');
    addRun(env, 'SELECT A_ONLY FROM TEST_A', 'run-A9', SRC_A, { status: 'success', elapsedMs: 9, rows: 1 });
    addRun(env, 'SELECT B_ONLY FROM TEST_B', 'run-B9', SRC_B, { status: 'success', elapsedMs: 4, rows: 1 });

    env.hooks.state.source = SRC_A;
    env.el('db-source').value = 'A';
    assert.deepStrictEqual(env.hooks.loadHistory(), ['SELECT A_ONLY FROM TEST_A'], 'A 的数据源下拉只能看到 A 的历史');
    env.hooks.state.source = SRC_B;
    env.el('db-source').value = 'B';
    assert.deepStrictEqual(env.hooks.loadHistory(), ['SELECT B_ONLY FROM TEST_B'], 'B 的数据源下拉只能看到 B 的历史');

    env.features.addHistory({ sql: 'SELECT ADDED FROM DUAL', sourceId: 'B', status: 'success', startedAt: Date.now(), elapsedMs: 1 });
    assert.ok(env.hooks.loadHistory().indexOf('SELECT ADDED FROM DUAL') >= 0, 'addHistory 后页面下拉必须能看到同一条记录');
    assert.ok(env.features.listHistory('B').some(entry => entry.sql === 'SELECT ADDED FROM DUAL'), 'addHistory 后结构化仓库必须能看到同一条记录');

    env.features.openHistory();
    const historyState = env.features.__historyState;
    assert.strictEqual(historyState.historyKey, 'B', '结构化弹窗必须按当前数据源读取');
    assert.ok(!historyState.history.some(entry => entry.sql === 'SELECT A_ONLY FROM TEST_A'), '结构化弹窗不得显示其他数据源的历史');
    const dialogHtml = ALL_ELEMENTS.map(node => String(node.innerHTML || '')).join('\n');
    assert.ok(dialogHtml.indexOf('db-pro-history-scope') >= 0, '结构化弹窗必须提供历史范围选择');
    assert.ok(dialogHtml.indexOf('旧版历史／来源未知') >= 0, '结构化弹窗必须显式暴露旧版历史桶');
    assert.ok(dialogHtml.indexOf('data-history-link') >= 0, '结构化弹窗必须保留新窗口链接入口');
    env.features.closeModal();
  });

  await scenario('S15 页面清空按钮走同一仓库，运行路径不再伪造成功记录、不再写旧字符串列表', async () => {
    const prefs = { prefs: null, history: ['SELECT OLD FROM DUAL'], last_source: 'A', column_widths: {}, row_limits: {} };
    const env = createEnv({ order: 'features-first', prefs: prefs });
    await env.hooks.loadDatabasePreference();
    env.startFeatures();
    bootWorkspace(env, SRC_A, '');
    env.el('db-source').value = 'A';
    addRun(env, 'SELECT A_ONLY FROM TEST_A', 'run-A10', SRC_A, { status: 'success', elapsedMs: 7, rows: 2 });

    // 页面运行路径的真实 pushHistory：不得写旧字符串列表，也不得伪造成功记录
    env.hooks.pushHistory('SELECT PUSHED FROM DUAL');
    assert.deepStrictEqual(env.hooks.persistGet().history, ['SELECT OLD FROM DUAL'], '迁移后旧字符串列表只读，运行路径不得再写入');
    assert.ok(!env.features.listHistory('A').some(entry => entry.sql === 'SELECT PUSHED FROM DUAL' && entry.status === 'success'), 'pushHistory 不得伪造成功记录');

    env.hooks.state.source = SRC_A;
    assert.deepStrictEqual(env.hooks.loadHistory(), ['SELECT A_ONLY FROM TEST_A'], '前置条件：下拉可见 A 的真实历史');
    const clear = env.el('db-history-clear');
    assert.strictEqual(typeof clear.onclick, 'function', '清空按钮必须已绑定真实处理器');
    clear.onclick();
    assert.deepStrictEqual(env.hooks.loadHistory(), [], '清空后下拉必须为空');
    assert.strictEqual(env.features.loadHistory('A').length, 0, '清空后结构化仓库也必须为空');
    assert.deepStrictEqual(env.hooks.persistGet().history, ['SELECT OLD FROM DUAL'], '清空走结构化仓库，不再改写旧字符串列表');
  });
}

async function main() {
  if (FEATURES_SOURCE.indexOf('LEGACY_HISTORY_BUCKET') < 0) console.log('[warn] features 源码未包含单一历史仓库（修复前基线）');
  if (PAGE_SOURCE.indexOf('kairo:database-commands-ready') < 0) console.log('[warn] database.js 未接入统一快捷键分发器（修复前基线）');
  await shortcutScenarios();
  await historyScenarios();
  console.log('--- DBUI-05 / DBUI-06 finished: ' + (failures.length ? failures.length + ' failing scenario(s)' : 'all scenarios passed') + ' ---');
  if (failures.length) {
    for (const failure of failures) {
      console.error('\n[FAIL] ' + failure.name);
      console.error(failure.error && failure.error.stack ? failure.error.stack : failure.error);
    }
    process.exitCode = 1;
  }
}

main().catch(error => {
  console.error(error && error.stack ? error.stack : error);
  process.exitCode = 1;
});
