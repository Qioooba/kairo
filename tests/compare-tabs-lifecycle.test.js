// tests/compare-tabs-lifecycle.test.js
// Batch E 回归（CT03 / CT05 / CT07）：
//   CT03 后台（隐藏）的比较 Tab 仍然处理全局 Ctrl+Z；
//   CT05 关闭比较 Tab 后文件夹扫描继续轮询，且旧实例会串扰新任务；
//   CT07 “全选内容”只覆盖虚拟窗口，复制结果被截断。
//
// 做法与 tests/compare-save-lifecycle.test.js 一致：把 web/pages/compare.js 的真实函数
// 放进 vm + 极简 DOM 双中执行，断言真实发出的请求、真实键盘/复制事件的处理结果，
// 而不是断言内部标志位。
//
// 未验证项：真实浏览器键盘焦点与 blur 顺序、真实虚拟化几何（layout/scrollHeight 由浏览器
// 计算）、真实网络取消语义（服务端是否真的停止任务）。这里只有 DOM 双。
'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const COMPARE_PATH = path.join(__dirname, '..', 'web', 'pages', 'compare.js');
const COMPARE_SRC = fs.readFileSync(COMPARE_PATH, 'utf8');

// ---------------------------------------------------------------------------
// 极简 DOM 双：只实现 compare.js 真正用到的接口
// ---------------------------------------------------------------------------

function escapeHtmlForDouble(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function makeStyle() {
  const props = {};
  return {
    setProperty(name, value) { props[name] = String(value); },
    getPropertyValue(name) { return props[name] || ''; },
    removeProperty(name) { delete props[name]; }
  };
}

function makeNode(tag) {
  const classes = new Set();
  const attrs = {};
  const listeners = {};
  let children = [];
  let ownText = '';
  let html = '';

  const node = {
    tagName: String(tag || 'div').toUpperCase(),
    nodeType: 1,
    style: makeStyle(),
    dataset: {},
    parentElement: null,
    parentNode: null,
    value: '',
    checked: false,
    disabled: false,
    hidden: false,
    title: '',
    type: '',
    tabIndex: 0,
    selectionStart: 0,
    selectionEnd: 0,
    scrollTop: 0,
    scrollLeft: 0,
    scrollHeight: 600,
    clientHeight: 600,
    clientWidth: 800,
    offsetWidth: 800,
    offsetHeight: 600,
    contentEditable: 'inherit',
    get children() { return children; },
    get childNodes() { return children; },
    get firstChild() { return children[0] || null; },
    get isConnected() { return true; },
    get ownerDocument() { return null; },
    get isContentEditable() { return node.contentEditable === 'true' || node.contentEditable === 'plaintext-only'; },
    get className() { return Array.from(classes).join(' '); },
    set className(value) {
      classes.clear();
      String(value || '').split(/\s+/).filter(Boolean).forEach(c => classes.add(c));
    },
    get id() { return attrs.id || ''; },
    set id(value) { attrs.id = String(value); },
    get textContent() {
      if (children.length) return ownText + children.map(c => c.textContent).join('');
      return ownText;
    },
    set textContent(value) {
      ownText = String(value == null ? '' : value);
      html = escapeHtmlForDouble(ownText);
      children.forEach(c => { c.parentElement = null; c.parentNode = null; });
      children = [];
    },
    get innerText() { return node.textContent; },
    set innerText(value) { node.textContent = value; },
    get innerHTML() { return html; },
    set innerHTML(value) {
      html = String(value == null ? '' : value);
      // 真实 DOM 会替换全部子节点；同时保留纯文本视图，便于 contenteditable 读取。
      children.forEach(c => { c.parentElement = null; c.parentNode = null; });
      children = [];
      ownText = html.replace(/<[^>]*>/g, '');
    },
    setAttribute(name, value) {
      attrs[name] = String(value);
      if (name === 'id') node.id = value;
      if (name === 'class') node.className = value;
      if (name === 'value') node.value = String(value);
    },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    removeAttribute(name) { delete attrs[name]; },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    appendChild(child) {
      if (!child) return child;
      child.parentElement = node;
      child.parentNode = node;
      children.push(child);
      return child;
    },
    append(...items) { items.forEach(item => node.appendChild(item)); },
    insertBefore(child, ref) {
      if (!child) return child;
      child.parentElement = node;
      child.parentNode = node;
      const idx = ref ? children.indexOf(ref) : -1;
      if (idx < 0) children.push(child);
      else children.splice(idx, 0, child);
      return child;
    },
    removeChild(child) {
      const idx = children.indexOf(child);
      if (idx >= 0) children.splice(idx, 1);
      if (child) { child.parentElement = null; child.parentNode = null; }
      return child;
    },
    replaceChild(next, prev) {
      const idx = children.indexOf(prev);
      if (idx < 0) return prev;
      next.parentElement = node;
      next.parentNode = node;
      children[idx] = next;
      prev.parentElement = null;
      prev.parentNode = null;
      return prev;
    },
    remove() {
      if (node.parentElement) node.parentElement.removeChild(node);
    },
    contains(other) {
      if (other === node) return true;
      return children.some(c => c.contains && c.contains(other));
    },
    addEventListener(type, fn) {
      listeners[type] = listeners[type] || [];
      listeners[type].push(fn);
    },
    removeEventListener(type, fn) {
      if (!listeners[type]) return;
      listeners[type] = listeners[type].filter(item => item !== fn);
    },
    dispatchEvent(ev) { return dispatchOn(node, ev); },
    get listeners() { return listeners; },
    focus() { node.ownerDocumentRef.activeElement = node; },
    blur() { if (node.ownerDocumentRef.activeElement === node) node.ownerDocumentRef.activeElement = node.ownerDocumentRef.body; },
    select() {},
    click() { fireEvent(node, 'click'); },
    getBoundingClientRect() { return { top: 0, left: 0, width: 100, height: 20 }; },
    classList: {
      add: c => classes.add(c),
      remove: c => classes.delete(c),
      contains: c => classes.has(c),
      toggle: (c, force) => {
        if (force === undefined) { if (classes.has(c)) classes.delete(c); else classes.add(c); }
        else if (force) classes.add(c); else classes.delete(c);
      }
    },
    closest(selector) {
      let current = node;
      while (current) {
        if (matchesSelector(current, selector)) return current;
        current = current.parentElement;
      }
      return null;
    },
    matches(selector) { return matchesSelector(node, selector); },
    querySelector(selector) { return queryAll(node, selector)[0] || null; },
    querySelectorAll(selector) { return queryAll(node, selector); }
  };
  // 真实 DOM 允许直接给 onX 赋值注册处理器（compare.js 多处这样用），
  // 因此把属性读写映射到同一个监听表。
  ['click', 'change', 'input', 'dblclick', 'submit', 'keydown', 'scroll', 'copy'].forEach(type => {
    let handler = null;
    Object.defineProperty(node, 'on' + type, {
      get() { return handler; },
      set(fn) {
        if (handler) node.removeEventListener(type, handler);
        handler = typeof fn === 'function' ? fn : null;
        if (handler) node.addEventListener(type, handler);
      }
    });
  });
  return node;
}

function matchesCompound(node, part) {
  if (!part) return false;
  const tagMatch = /^[A-Za-z][A-Za-z0-9-]*/.exec(part);
  if (tagMatch && node.tagName !== tagMatch[0].toUpperCase()) return false;
  const classRe = /\.([A-Za-z0-9_-]+)/g;
  let m;
  while ((m = classRe.exec(part))) {
    if (!node.classList.contains(m[1])) return false;
  }
  const attrRe = /\[([A-Za-z0-9_:-]+)(?:=(?:"([^"]*)"|'([^']*)'|([^\]]*)))?\]/g;
  while ((m = attrRe.exec(part))) {
    const name = m[1];
    const expected = m[2] != null ? m[2] : (m[3] != null ? m[3] : m[4]);
    if (!node.hasAttribute(name)) return false;
    if (expected != null && node.getAttribute(name) !== expected) return false;
  }
  return true;
}

function matchesSelector(node, selector) {
  return String(selector).split(',').some(group => group.trim().split(/\s+/).pop() && matchesCompound(node, group.trim().split(/\s+/).pop()));
}

function descendants(root, out) {
  (root.children || []).forEach(child => { out.push(child); descendants(child, out); });
  return out;
}

function queryAll(root, selector) {
  const all = descendants(root, []);
  const found = [];
  String(selector).split(',').map(s => s.trim()).filter(Boolean).forEach(group => {
    const parts = group.split(/\s+/).filter(Boolean);
    const last = parts[parts.length - 1];
    all.forEach(node => {
      if (!matchesCompound(node, last)) return;
      let idx = parts.length - 2;
      let parent = node.parentElement;
      while (idx >= 0) {
        let hit = false;
        while (parent) {
          if (matchesCompound(parent, parts[idx])) { hit = true; parent = parent.parentElement; break; }
          parent = parent.parentElement;
        }
        if (!hit) return;
        idx--;
      }
      found.push(node);
    });
  });
  return found;
}

function makeEvent(type, extra) {
  const ev = Object.assign({
    type,
    defaultPrevented: false,
    propagationStopped: false,
    preventDefault() { ev.defaultPrevented = true; },
    stopPropagation() { ev.propagationStopped = true; }
  }, extra || {});
  return ev;
}

function dispatchOn(node, ev) {
  const doc = node.ownerDocumentRef;
  let current = node;
  while (current) {
    (current.listeners[ev.type] || []).slice().forEach(fn => {
      if (ev.propagationStopped) return;
      fn(ev);
    });
    if (ev.propagationStopped) return ev;
    current = current.parentElement || current.parentNode;
  }
  if (doc) {
    (doc.listeners[ev.type] || []).slice().forEach(fn => {
      if (ev.propagationStopped) return;
      fn(ev);
    });
  }
  return ev;
}

function fireEvent(node, type, extra) {
  const ev = makeEvent(type, extra);
  if (!ev.target) ev.target = node;
  return dispatchOn(node, ev);
}

// ---------------------------------------------------------------------------
// 运行环境：真实 compare.js + 假 DOM + 记录式 API 双
// ---------------------------------------------------------------------------

const TEST_BRIDGE_ANCHOR = 'Kairo.compareTest = {';
const TEST_BRIDGE_EXPORTS = 'Kairo.compareTest = { buildTextWorkbench, buildFolderWorkbench, createVirtualDiff, renderCompare,';

function loadCompareModule() {
  const source = COMPARE_SRC.replace(TEST_BRIDGE_ANCHOR, TEST_BRIDGE_EXPORTS);
  assert.notStrictEqual(source, COMPARE_SRC, '必须能在 web/pages/compare.js 中定位 Kairo.compareTest 导出口');

  const requests = [];
  const clipboardWrites = [];
  const ranges = [];
  let activeTabId = 'compare';

  const document = {
    listeners: {},
    body: makeNode('body'),
    activeElement: null,
    createElement: tag => {
      const node = makeNode(tag);
      node.ownerDocumentRef = document;
      return node;
    },
    createTextNode: text => {
      const node = makeNode('text');
      node.nodeType = 3;
      node.tagName = '#TEXT';
      node.ownerDocumentRef = document;
      node.textContent = String(text);
      return node;
    },
    createRange: () => {
      const range = {
        target: null,
        selectNodeContents(target) { range.target = target; },
        collapse() {},
        selectNode(target) { range.target = target; },
        setStart() {},
        setEnd() {}
      };
      ranges.push(range);
      return range;
    },
    getElementById: id => (id === 'crumb' ? document.crumb : null),
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener(type, fn) { (document.listeners[type] = document.listeners[type] || []).push(fn); },
    removeEventListener(type, fn) {
      if (!document.listeners[type]) return;
      document.listeners[type] = document.listeners[type].filter(item => item !== fn);
    },
    execCommand: () => true
  };
  document.crumb = document.createElement('div');
  document.body.ownerDocumentRef = document;

  const selection = {
    ranges: [],
    removeAllRanges() { selection.ranges = []; },
    addRange(range) { selection.ranges.push(range); },
    selectAllChildren() {},
    toString: () => ''
  };

  const window = {
    Kairo: null,
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    location: { hash: '' },
    confirm: () => true,
    isSecureContext: true,
    navigator: { clipboard: { writeText: async () => {} } },
    getSelection: () => selection,
    addEventListener(type, fn) { (window.listeners[type] = window.listeners[type] || []).push(fn); },
    removeEventListener() {},
    listeners: {}
  };
  window.window = window;

  const env = {
    document,
    window,
    requests,
    clipboardWrites,
    ranges,
    selection,
    toasts: [],
    confirmAnswer: true,
    modalBody: null,
    apiHandler: null,
    // 记录每个请求（含方法/URL/正文），供用例断言真实 HTTP 行为。
    async api(method, url, body) {
      requests.push({ method, url, body });
      if (!env.apiHandler) throw new Error('no api handler for ' + url);
      return env.apiHandler(method, url, body);
    }
  };

  const Kairo = {
    core: {
      el(tag, attrs, children) {
        const node = document.createElement(tag);
        if (attrs) {
          for (const key in attrs) {
            const value = attrs[key];
            if (key === 'class') node.className = value;
            else if (key === 'text') node.textContent = value;
            else if (key === 'html' || key === 'unsafeHtml') node.innerHTML = value;
            else if (key.indexOf('on') === 0) node.addEventListener(key.slice(2), value);
            else if (value === false || value == null) node.removeAttribute(key);
            else node.setAttribute(key, value === true ? '' : value);
          }
        }
        if (children) {
          (Array.isArray(children) ? children : [children]).forEach(c => {
            if (c == null) return;
            if (typeof c === 'string') node.appendChild(document.createTextNode(c));
            else node.appendChild(c);
          });
        }
        return node;
      },
      toast(message, type) { env.toasts.push({ message: String(message), type: type || '' }); },
      copyToClipboard: async text => { env.clipboardWrites.push({ text: String(text == null ? '' : text) }); },
      confirmDialog: message => Promise.resolve(env.confirmAnswer !== false)
    },
    api: {
      api: (method, url, body) => env.api(method, url, body),
      getPreference: async () => ({ exists: false }),
      putPreference: async () => {},
      preferenceSaver: () => () => {},
      browseButton: () => document.createElement('button')
    },
    overlays: {
      modal: function openModal(options) {
        env.modalBody = options && options.body ? options.body : null;
        return { close() {} };
      }
    },
    workbench: {
      syntaxEditor: {
        languageFromPath: () => 'text',
        highlight: text => escapeHtmlForDouble(text)
      }
    },
    // 真实 tabs 接口：只有 isActive / getActiveId / getActive（没有 activeRoute）。
    tabs: {
      isActive: id => activeTabId === id,
      getActiveId: () => activeTabId,
      getActive: () => ({ id: activeTabId })
    },
    pages: {},
    state: { routes: {}, routeNames: {} }
  };
  window.Kairo = Kairo;

  const context = {
    window,
    document,
    Kairo,
    localStorage: window.localStorage,
    navigator: window.navigator,
    location: window.location,
    console,
    setTimeout,
    clearTimeout,
    requestAnimationFrame: fn => setTimeout(fn, 0),
    cancelAnimationFrame: clearTimeout,
    URL: { createObjectURL: () => 'blob:test', revokeObjectURL() {} },
    Blob: function Blob() {},
    Map, Set, Number, String, Object, Array, Math, JSON, Date, Promise, Error, Boolean, RegExp, isNaN, parseInt, parseFloat
  };

  vm.runInNewContext(source, context, { filename: 'web/pages/compare.js' });
  env.Kairo = Kairo;
  env.compareTest = Kairo.compareTest;
  env.fire = fireEvent;
  env.setActiveTab = id => { activeTabId = id; };
  env.settle = async (rounds = 8) => {
    for (let i = 0; i < rounds; i++) await new Promise(resolve => setTimeout(resolve, 1));
  };
  env.wait = ms => new Promise(resolve => setTimeout(resolve, ms));
  env.waitFor = async (predicate, label) => {
    for (let i = 0; i < 400; i++) {
      if (predicate()) return true;
      await new Promise(resolve => setTimeout(resolve, 5));
    }
    throw new Error('等待超时：' + (label || 'condition'));
  };
  env.gets = jobId => requests.filter(r => r.method === 'GET' && r.url === '/api/compare/jobs/' + jobId);
  env.deletes = jobId => requests.filter(r => r.method === 'DELETE' && r.url === '/api/compare/jobs/' + jobId);
  env.writes = () => requests.filter(r => r.url === '/api/compare/write');
  return env;
}

function makeSideState(side) {
  return {
    source: { kind: 'text', path: '', label: side === 'left' ? '左侧文本' : '右侧文本', encoding: 'auto' },
    version: null,
    codec: { encoding: 'utf-8', eol: 'lf', bom: false },
    dirty: false,
    baseline: '',
    loadState: 'ready',
    loadError: null,
    loadSeq: 0,
    editSeq: 0,
    saving: false
  };
}

function makeState() {
  return { left: makeSideState('left'), right: makeSideState('right'), diff: null, hunks: [], hunkIndex: -1, mode: 'text', scan: null };
}

function makeOptions() {
  return {
    trim_space: true, ignore_blank: false, ignore_case: false, mode: 'side', onlyDiff: false,
    backup: true, max_depth: 1, font_size: '12', line_height: '25'
  };
}

// 记录式后端：读/写/差异/连接。写与差异语义与 comparefs 及 /api/diff/compare 一致。
function makeBackend(files) {
  let clock = 0;
  const nextMtime = () => { clock += 1000; return '2026-09-22T00:00:' + String(clock).padStart(2, '0') + 'Z'; };
  const filesystem = {};
  Object.keys(files).forEach(key => {
    filesystem[key] = { content: String(files[key].content), mtime: files[key].mtime || '2026-09-22T00:00:00Z' };
  });
  return {
    files: filesystem,
    handler() {
      return async function handle(method, url, body) {
        if (url === '/api/compare/read') {
          const entry = filesystem[body.source.path];
          if (!entry) { const err = new Error('目标文件不存在'); err.status = 404; throw err; }
          return {
            text: entry.content,
            entry: { name: body.source.path.split('/').pop(), path: body.source.path, size: Buffer.byteLength(entry.content, 'utf8'), mtime: entry.mtime, is_dir: false },
            version: { size: Buffer.byteLength(entry.content, 'utf8'), mtime: entry.mtime, path: body.source.path },
            encoding: 'utf-8', eol: 'lf', bom: false, binary: false, truncated: false
          };
        }
        if (url === '/api/compare/write') {
          const entry = filesystem[body.target.path];
          if (!entry) { const err = new Error('目标文件不存在'); err.status = 404; throw err; }
          entry.content = String(body.content);
          entry.mtime = nextMtime();
          return { ok: true, path: body.target.path, document_id: body.document_id || '', version: { size: Buffer.byteLength(entry.content, 'utf8'), mtime: entry.mtime, path: body.target.path } };
        }
        if (url === '/api/diff/compare') {
          return { lines: simpleDiff(String(body.left).split('\n'), String(body.right).split('\n')), stats: {}, unified_diff: '' };
        }
        if (url === '/api/compare/connections') return { sftp: [], kinds: ['local', 'sftp', 'ftp'] };
        throw new Error('unexpected api ' + method + ' ' + url);
      };
    }
  };
}

function simpleDiff(leftLines, rightLines) {
  const lines = [];
  const count = Math.max(leftLines.length, rightLines.length);
  for (let i = 0; i < count; i++) {
    const leftText = leftLines[i];
    const rightText = rightLines[i];
    if (leftText === rightText) {
      lines.push({ op: 'equal', left_no: i + 1, right_no: i + 1, text: leftText });
    } else {
      if (leftText !== undefined) lines.push({ op: 'delete', left_no: i + 1, right_no: 0, text: leftText });
      if (rightText !== undefined) lines.push({ op: 'insert', left_no: 0, right_no: i + 1, text: rightText });
    }
  }
  return lines;
}

function buildTextWorkbench(env, state, panel, options) {
  panel = panel || env.document.createElement('section');
  if (!panel.parentElement) env.document.body.appendChild(panel);
  const cleanup = env.compareTest.buildTextWorkbench(panel, state, options || makeOptions());
  if (typeof cleanup === 'function') checkCleanups.push(cleanup);
  return { panel, state, cleanup };
}

function leftTextarea(panel) { return panel.querySelectorAll('textarea')[0]; }
function rightTextarea(panel) { return panel.querySelectorAll('textarea')[1]; }
function vdiffViewport(panel) { return panel.querySelector('.cmp-vdiff'); }
function vdiffCanvas(panel) { return panel.querySelector('.cmp-vdiff-canvas'); }

function typeInto(textarea, value) {
  textarea.value = value;
  fireEvent(textarea, 'input');
}

function findButton(root, label) {
  const match = descendants(root, []).filter(node => node.tagName === 'BUTTON' && node.textContent.trim() === label);
  return match[0] || null;
}

const results = [];
// 每个用例里真实打开的文本工作台都要在用例结束后 cleanup，避免编辑器定时器
// 让测试进程无法退出（run-tests.js 依赖脚本自行结束）。
const checkCleanups = [];
function check(name, fn) {
  results.push({ name, fn });
}

// ---------------------------------------------------------------------------
// CT03：隐藏的比较 Tab 不得处理全局 Ctrl+Z
// ---------------------------------------------------------------------------

async function openSwappedPair(env) {
  const backend = makeBackend({
    '/left/A.txt': { content: 'AAA\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'BBB\n', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  const swapBtn = text.panel.querySelector('[data-action="swap-sides"]');
  assert.ok(swapBtn, '工具栏应有交换按钮');
  fireEvent(swapBtn, 'click');
  assert.strictEqual(state.left.source.path, '/left/B.txt', '交换后左侧应为 B（撤回记录已压栈）');
  return { backend, state, text };
}

check('CT03 后台比较 Tab 必须忽略全局 Ctrl+Z：不撤回、不 preventDefault', async () => {
  const env = loadCompareModule();
  const { state } = await openSwappedPair(env);

  // 切到文件页签：比较页签只是被隐藏，事件处理器仍在 document 上。
  env.setActiveTab('files');
  env.toasts.length = 0;
  const ev = fireEvent(env.document.body, 'keydown', { key: 'z', ctrlKey: true });
  await env.settle(10);

  assert.strictEqual(ev.defaultPrevented, false, '后台比较 Tab 不得阻止前台 Ctrl+Z');
  assert.strictEqual(state.left.source.path, '/left/B.txt', '后台比较 Tab 不得执行撤回');
  assert.strictEqual(state.right.source.path, '/left/A.txt', '后台比较 Tab 不得交换回原状');
  assert.ok(!env.toasts.some(t => /已撤回操作/.test(t.message)), '后台比较 Tab 不得提示已撤回：' + JSON.stringify(env.toasts));
});

check('CT03 前台比较 Tab 的 Ctrl+Z 仍必须撤回（保护不能把功能关掉）', async () => {
  const env = loadCompareModule();
  const { state } = await openSwappedPair(env);

  env.setActiveTab('compare');
  env.toasts.length = 0;
  const ev = fireEvent(env.document.body, 'keydown', { key: 'z', ctrlKey: true });
  await env.settle(20);

  assert.strictEqual(ev.defaultPrevented, true, '前台比较 Tab 必须接管 Ctrl+Z');
  assert.strictEqual(state.left.source.path, '/left/A.txt', '前台比较 Tab 必须真正撤回交换');
  assert.strictEqual(state.right.source.path, '/left/B.txt', '前台比较 Tab 必须真正撤回交换');
  assert.ok(env.toasts.some(t => /已撤回操作/.test(t.message)), '必须提示已撤回：' + JSON.stringify(env.toasts));
});

check('CT03 焦点在 contenteditable 差异单元格时 Ctrl+Z 不得触发比对撤回', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({
    '/left/A.txt': { content: 'A one\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'B one\n', mtime: '2026-09-22T00:00:00Z' }
  }).handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  fireEvent(text.panel, 'keydown', { key: 'Enter', ctrlKey: true });
  await env.settle(20);

  const code = text.panel.querySelectorAll('.cmp-vdiff-canvas .cmp-diff-left .cmp-code')[0];
  assert.ok(code, '差异视图应渲染左侧可编辑单元格');
  fireEvent(code, 'mousedown');
  assert.strictEqual(code.isContentEditable, true, '点击单元格应进入行内编辑');
  env.document.activeElement = code;

  env.toasts.length = 0;
  const ev = fireEvent(code, 'keydown', { key: 'z', ctrlKey: true });
  await env.settle(10);
  assert.strictEqual(ev.defaultPrevented, false, 'contenteditable 内的 Ctrl+Z 必须交回浏览器原生行为');
  assert.ok(!env.toasts.some(t => /已撤回操作/.test(t.message)), 'contenteditable 内不得触发比对撤回：' + JSON.stringify(env.toasts));
});

check('CT03 关闭并重开比较 20 次：document 监听不累积，一次按键只撤回一次', async () => {
  const env = loadCompareModule();
  // 20 次真实打开 + 真实关闭（renderCompare cleanup 最终会调用 cleanupTextWorkbench）。
  for (let i = 0; i < 20; i++) {
    const state = makeState();
    const text = buildTextWorkbench(env, state);
    assert.strictEqual(typeof text.cleanup, 'function', '文本工作台必须返回 cleanup');
    text.cleanup();
  }
  const leaked = (env.document.listeners.keydown || []).length;
  assert.strictEqual(leaked, 0, '关闭后 document keydown 监听必须注销，实际剩余 ' + leaked);
  const leakedCopy = (env.document.listeners.copy || []).length;
  assert.strictEqual(leakedCopy, 0, '关闭后 document copy 监听必须注销，实际剩余 ' + leakedCopy);

  const { state } = await openSwappedPair(env);
  assert.strictEqual((env.document.listeners.keydown || []).length, 1, '同一时刻只能存在一个比较快捷键监听');
  assert.strictEqual((env.document.listeners.copy || []).length, 1, '同一时刻只能存在一个比较复制分发监听');
  env.setActiveTab('compare');
  env.toasts.length = 0;
  fireEvent(env.document.body, 'keydown', { key: 'z', ctrlKey: true });
  await env.settle(20);
  const undone = env.toasts.filter(t => /已撤回操作/.test(t.message)).length;
  assert.strictEqual(undone, 1, '一次按键只能撤回一次，实际 ' + undone);
  assert.strictEqual(state.left.source.path, '/left/A.txt', '撤回一次即回到交换前的 A');
});

// ---------------------------------------------------------------------------
// CT05：关闭比较 Tab 后必须停止轮询并取消后端任务
// ---------------------------------------------------------------------------

async function renderCompareTab(env, apiHandler) {
  env.apiHandler = apiHandler;
  const view = env.document.createElement('section');
  view.dataset.renderToken = '1';
  env.document.body.appendChild(view);
  const cleanup = await env.compareTest.renderCompare(view);
  assert.strictEqual(typeof cleanup, 'function', 'renderCompare 必须返回 Tab 关闭用的 cleanup');
  return { view, cleanup };
}

function setFolderPaths(view, left, right) {
  const inputs = view.querySelectorAll('.cmp-folder-path');
  assert.strictEqual(inputs.length, 2, '文件夹工作台应有左右两个目录输入');
  inputs[0].value = left; fireEvent(inputs[0], 'change');
  inputs[1].value = right; fireEvent(inputs[1], 'change');
}

function clickStartScan(view) {
  const btn = view.querySelector('[data-action="compare-scan"]');
  assert.ok(btn, '文件夹工作台应有“开始比较”按钮');
  fireEvent(btn, 'click');
}

check('CT05 切换页签（只是隐藏）必须保持扫描继续轮询', async () => {
  const env = loadCompareModule();
  const { view, cleanup } = await renderCompareTab(env, async (method, url) => {
    if (url === '/api/compare/connections') return { sftp: [] };
    if (url === '/api/compare/scan') return { job_id: 'job-1' };
    if (url === '/api/compare/jobs/job-1') {
      if (method === 'DELETE') return { ok: true };
      return { status: 'running', phase: '扫描中', message: '扫描中', current: 1, total: 10 };
    }
    throw new Error('unexpected ' + method + ' ' + url);
  });
  setFolderPaths(view, '/left', '/right');
  clickStartScan(view);
  await env.waitFor(() => env.gets('job-1').length >= 1, '第一轮 GET');

  env.setActiveTab('files'); // 仅切换页签：不得销毁比较工作台
  await env.wait(600);
  assert.ok(env.gets('job-1').length >= 2, '仅隐藏 Tab 时扫描必须继续轮询，实际 GET=' + env.gets('job-1').length);
  assert.strictEqual(env.deletes('job-1').length, 0, '仅隐藏 Tab 不得取消后端任务');
  cleanup(); // 收尾：真实关闭以停掉轮询，避免测试进程被定时器挂住
});

check('CT05 关闭比较 Tab：恰好一次取消，且不再发出新的 GET', async () => {
  const env = loadCompareModule();
  const { view, cleanup } = await renderCompareTab(env, async (method, url) => {
    if (url === '/api/compare/connections') return { sftp: [] };
    if (url === '/api/compare/scan') return { job_id: 'job-1' };
    if (url === '/api/compare/jobs/job-1') {
      if (method === 'DELETE') return { ok: true };
      return { status: 'running', phase: '扫描中', message: '扫描中', current: 1, total: 10 };
    }
    throw new Error('unexpected ' + method + ' ' + url);
  });
  setFolderPaths(view, '/left', '/right');
  clickStartScan(view);
  // 等到第一轮 GET 已返回并已安排下一次 350ms 轮询。
  await env.waitFor(() => /扫描中/.test(view.textContent), '第一轮进度已渲染');
  await env.settle(5);

  cleanup(); // 真实关闭 Tab（scope/cleanup 路径）
  const deletes = env.deletes('job-1').length;
  const getsAtClose = env.gets('job-1').length;
  assert.strictEqual(deletes, 1, '关闭必须恰好取消一次后端任务，实际 ' + deletes);
  await env.wait(900);
  assert.strictEqual(env.gets('job-1').length, getsAtClose, '关闭后不得再有新的 GET，实际 ' + env.gets('job-1').length + ' vs ' + getsAtClose);
  assert.strictEqual(env.deletes('job-1').length, 1, '关闭只能取消一次，实际 ' + env.deletes('job-1').length);
});

check('CT05 POST 响应到达前关闭：不得开始轮询，且必须取消已创建的任务', async () => {
  const env = loadCompareModule();
  let releasePost = null;
  const postGate = new Promise(resolve => { releasePost = resolve; });
  const { view, cleanup } = await renderCompareTab(env, async (method, url) => {
    if (url === '/api/compare/connections') return { sftp: [] };
    if (url === '/api/compare/scan') { await postGate; return { job_id: 'job-1' }; }
    if (url === '/api/compare/jobs/job-1') {
      if (method === 'DELETE') return { ok: true };
      return { status: 'running', message: '扫描中', current: 1, total: 10 };
    }
    throw new Error('unexpected ' + method + ' ' + url);
  });
  setFolderPaths(view, '/left', '/right');
  clickStartScan(view);
  await env.waitFor(() => env.requests.some(r => r.url === '/api/compare/scan'), 'POST 已发出');

  cleanup();            // 关闭发生在 POST 响应之前
  releasePost();        // 之后响应才回来
  await env.settle(30);

  assert.strictEqual(env.gets('job-1').length, 0, '关闭后不得开始轮询，实际 ' + env.gets('job-1').length);
  assert.strictEqual(env.deletes('job-1').length, 1, '已创建的后端任务必须被取消（不能泄漏），实际 ' + env.deletes('job-1').length);
});

check('CT05 GET 响应到达前关闭：不得恢复轮询，且只取消一次', async () => {
  const env = loadCompareModule();
  let releaseGet = null;
  const getGate = new Promise(resolve => { releaseGet = resolve; });
  const { view, cleanup } = await renderCompareTab(env, async (method, url) => {
    if (url === '/api/compare/connections') return { sftp: [] };
    if (url === '/api/compare/scan') return { job_id: 'job-1' };
    if (url === '/api/compare/jobs/job-1') {
      if (method === 'DELETE') return { ok: true };
      await getGate;
      return { status: 'running', message: '扫描中', current: 3, total: 10 };
    }
    throw new Error('unexpected ' + method + ' ' + url);
  });
  setFolderPaths(view, '/left', '/right');
  clickStartScan(view);
  await env.waitFor(() => env.gets('job-1').length === 1, '第一轮 GET 已发出');

  cleanup();            // 关闭发生在 GET 响应之前
  releaseGet();         // 之后响应才回来
  await env.settle(30);
  await env.wait(600);

  assert.strictEqual(env.gets('job-1').length, 1, '过期 GET 返回后不得再安排轮询，实际 ' + env.gets('job-1').length);
  assert.strictEqual(env.deletes('job-1').length, 1, '关闭只能取消一次，实际 ' + env.deletes('job-1').length);
});

check('CT05 后端取消失败时不得恢复轮询', async () => {
  const env = loadCompareModule();
  const { view, cleanup } = await renderCompareTab(env, async (method, url) => {
    if (url === '/api/compare/connections') return { sftp: [] };
    if (url === '/api/compare/scan') return { job_id: 'job-1' };
    if (url === '/api/compare/jobs/job-1') {
      if (method === 'DELETE') { const err = new Error('后端取消失败'); err.status = 500; throw err; }
      return { status: 'running', message: '扫描中', current: 1, total: 10 };
    }
    throw new Error('unexpected ' + method + ' ' + url);
  });
  setFolderPaths(view, '/left', '/right');
  clickStartScan(view);
  await env.waitFor(() => env.gets('job-1').length >= 1, '第一轮 GET');

  cleanup();
  await env.wait(700);
  assert.strictEqual(env.gets('job-1').length, 1, '取消失败也不能恢复轮询，实际 ' + env.gets('job-1').length);
  assert.strictEqual(env.deletes('job-1').length, 1, '取消尝试只能有一次，实际 ' + env.deletes('job-1').length);
});

check('CT05 旧 Tab 关闭后立即开新 job：旧回调不得改写新任务状态', async () => {
  const env = loadCompareModule();
  let releaseOldGet = null;
  const oldGetGate = new Promise(resolve => { releaseOldGet = resolve; });
  let postCount = 0;
  env.apiHandler = async (method, url) => {
    if (url === '/api/compare/connections') return { sftp: [] };
    if (url === '/api/compare/scan') { postCount++; return { job_id: postCount === 1 ? 'job-old' : 'job-new' }; }
    if (url === '/api/compare/jobs/job-old') {
      if (method === 'DELETE') return { ok: true };
      await oldGetGate; // 旧实例的 GET 一直挂在响应前
      return { status: 'running', message: '旧任务仍在跑', current: 5, total: 100 };
    }
    if (url === '/api/compare/jobs/job-new') {
      if (method === 'DELETE') return { ok: true };
      return { status: 'completed', result: { items: [], summary: {} }, total: 1, current: 1 };
    }
    throw new Error('unexpected ' + method + ' ' + url);
  };

  const panelA = env.document.createElement('section');
  env.document.body.appendChild(panelA);
  const stateA = makeState();
  const wbA = env.compareTest.buildFolderWorkbench(panelA, stateA, makeOptions(), () => {}, null);
  assert.strictEqual(typeof (wbA && wbA.dispose), 'function', 'buildFolderWorkbench 必须返回 dispose');
  setFolderPaths(panelA, '/old-left', '/old-right');
  clickStartScan(panelA);
  await env.waitFor(() => env.gets('job-old').length === 1, '旧实例 GET 已发出');

  wbA.dispose(); // 真实关闭旧 Tab

  // 立即重开新 Tab 并启动新 job。
  const panelB = env.document.createElement('section');
  env.document.body.appendChild(panelB);
  const stateB = makeState();
  const wbB = env.compareTest.buildFolderWorkbench(panelB, stateB, makeOptions(), () => {}, null);
  setFolderPaths(panelB, '/new-left', '/new-right');
  clickStartScan(panelB);
  await env.waitFor(() => env.gets('job-new').length === 1, '新实例 GET 已发出');
  await env.waitFor(() => stateB.scan, '新实例扫描完成');
  const newGetsBeforeLateReply = env.gets('job-new').length;

  releaseOldGet(); // 旧实例的过期响应此刻才返回
  await env.wait(700);

  assert.strictEqual(env.gets('job-new').length, newGetsBeforeLateReply, '旧实例的过期回调不得继续轮询新任务，实际 ' + env.gets('job-new').length);
  assert.strictEqual(stateA.scan, null, '旧实例的过期响应不得改写旧实例状态');
  assert.strictEqual(env.deletes('job-old').length, 1, '关闭旧实例必须取消旧 job，实际 ' + env.deletes('job-old').length);
  if (wbB && typeof wbB.dispose === 'function') wbB.dispose(); // 收尾：停掉新实例的定时器
});

// ---------------------------------------------------------------------------
// CT07：全选内容必须是数据级全选，复制不能被虚拟窗口截断
// ---------------------------------------------------------------------------

// 左右各 total 行：line 1 = FIRST-MARK，最后一行 = LAST-MARK，中间第 mid 行不同。
function makeMarkedTexts(total, mid) {
  const left = [];
  const right = [];
  for (let i = 1; i <= total; i++) {
    if (i === 1) { left.push('FIRST-MARK-' + total); right.push('FIRST-MARK-' + total); continue; }
    if (i === total) { left.push('LAST-MARK-' + total); right.push('LAST-MARK-' + total); continue; }
    if (i === mid) { left.push('MID-OLD-' + total); right.push('MID-NEW-' + total); continue; }
    left.push('same-' + total + '-' + i);
    right.push('same-' + total + '-' + i);
  }
  return { left: left.join('\n') + '\n', right: right.join('\n') + '\n' };
}

async function openDiff(env, leftText, rightText, options) {
  const state = makeState();
  const text = buildTextWorkbench(env, state, null, options);
  typeInto(leftTextarea(text.panel), leftText);
  typeInto(rightTextarea(text.panel), rightText);
  fireEvent(text.panel.querySelector('[data-action="text-compare"]'), 'click');
  await env.waitFor(() => state.diff && state.hunks, '差异计算完成');
  await env.settle(30); // 等 requestAnimationFrame 里的 renderWindow
  return { state, text };
}

function captureCopy(env, target) {
  const writes = [];
  const ev = fireEvent(target, 'copy', {
    clipboardData: { types: [], setData(type, text) { writes.push({ type, text }); } }
  });
  return { ev, writes };
}

check('CT07 1000 行中部滚动全选复制：必须包含首行与末行标记（虚拟化只渲染窗口）', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({}).handler();
  const total = 1000;
  const texts = makeMarkedTexts(total, 500);
  const { state, text } = await openDiff(env, texts.left, texts.right);

  const viewport = vdiffViewport(text.panel);
  const canvas = vdiffCanvas(text.panel);
  assert.ok(viewport && canvas, '结果区应有虚拟差异视图');
  viewport.clientHeight = 400; // 规格：1000 行 / 400px / 25px 行高
  viewport.scrollTop = Math.floor(total / 2) * 25;
  fireEvent(viewport, 'scroll');
  await env.settle(10);

  const renderedRows = canvas.querySelectorAll('.cmp-vrow').length;
  assert.ok(renderedRows > 0 && renderedRows < total, '虚拟化：DOM 中只应有当前窗口的行，实际 ' + renderedRows);
  assert.strictEqual(canvas.querySelector('.cmp-vrow[data-row="' + (total - 1) + '"]'), null, '最后一行不应出现在当前窗口的 DOM 中');

  const selectAllBtn = findButton(text.panel, '全选内容');
  assert.ok(selectAllBtn, '更多操作里应有“全选内容”按钮');
  fireEvent(selectAllBtn, 'click');

  const copied = captureCopy(env, canvas);
  assert.strictEqual(copied.ev.defaultPrevented, true, '数据级全选必须接管复制事件（否则复制到的是被截断的原生选区）');
  assert.strictEqual(copied.writes.length, 1, '复制事件必须写入剪贴板，实际 ' + copied.writes.length);
  const payload = copied.writes[0].text;
  const lines = payload.split('\n');
  // 正文以换行结尾，行模型里最后会多出一个空行（与编辑器/reconstructTargetLines 一致）。
  assert.strictEqual(state.alignedRows.length, total + 1, '行模型应为 ' + total + ' 行正文 + 尾部空行');
  assert.strictEqual(lines.length, state.alignedRows.length, '复制内容必须覆盖全部对齐行，实际 ' + lines.length);
  assert.ok(payload.includes('FIRST-MARK-' + total), '复制内容必须包含首行标记');
  assert.ok(payload.includes('LAST-MARK-' + total), '复制内容必须包含末行标记');
  assert.ok(payload.includes('MID-OLD-' + total) && payload.includes('MID-NEW-' + total), '差异行必须保留左右两侧正文');
});

check('CT07 50000 行中部滚动全选复制：首尾标记完整', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({}).handler();
  const total = 50000;
  const texts = makeMarkedTexts(total, 25000);
  const { state, text } = await openDiff(env, texts.left, texts.right);

  const viewport = vdiffViewport(text.panel);
  const canvas = vdiffCanvas(text.panel);
  viewport.clientHeight = 400;
  viewport.scrollTop = Math.floor(total / 2) * 25;
  fireEvent(viewport, 'scroll');
  await env.settle(10);
  assert.ok(canvas.querySelectorAll('.cmp-vrow').length < total, '虚拟化：DOM 中不应有全部行');

  fireEvent(findButton(text.panel, '全选内容'), 'click');
  const copied = captureCopy(env, canvas);
  assert.strictEqual(copied.ev.defaultPrevented, true, '数据级全选必须接管复制事件');
  const payload = copied.writes[0].text;
  assert.ok(payload.includes('FIRST-MARK-' + total), '复制内容必须包含首行标记');
  assert.ok(payload.includes('LAST-MARK-' + total), '复制内容必须包含末行标记');
  assert.strictEqual(payload.split('\n').length, state.alignedRows.length, '复制内容必须覆盖全部对齐行');
});

check('CT07 只差异模式（mode=changes）全选复制必须含全部差异行', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({}).handler();
  // 4000 行里偶数行都不同 → 2000 条差异行，远超一屏。
  const total = 4000;
  const left = [];
  const right = [];
  for (let i = 1; i <= total; i++) {
    if (i % 2 === 0) { left.push('EVEN-LEFT-' + i); right.push('EVEN-RIGHT-' + i); }
    else { left.push('SAME-' + i); right.push('SAME-' + i); }
  }
  const options = Object.assign(makeOptions(), { mode: 'changes' });
  const { text } = await openDiff(env, left.join('\n') + '\n', right.join('\n') + '\n', options);

  const viewport = vdiffViewport(text.panel);
  const canvas = vdiffCanvas(text.panel);
  viewport.clientHeight = 400;
  viewport.scrollTop = 20000;
  fireEvent(viewport, 'scroll');
  await env.settle(10);
  const renderedRows = canvas.querySelectorAll('.cmp-vrow').length;
  assert.ok(renderedRows > 0 && renderedRows < 2000, '只差异视图同样是虚拟化的，实际 ' + renderedRows);

  fireEvent(findButton(text.panel, '全选内容'), 'click');
  const copied = captureCopy(env, canvas);
  assert.strictEqual(copied.ev.defaultPrevented, true, '数据级全选必须接管复制事件');
  const payload = copied.writes[0].text;
  const lines = payload.split('\n');
  assert.strictEqual(lines.length, 2000, '只差异模式必须复制全部差异行，实际 ' + lines.length);
  assert.ok(payload.includes('EVEN-LEFT-2') && payload.includes('EVEN-RIGHT-2'), '首条差异行必须存在');
  assert.ok(payload.includes('EVEN-LEFT-' + total) && payload.includes('EVEN-RIGHT-' + total), '末条差异行必须存在');
  assert.ok(!payload.includes('SAME-'), '只差异模式不得混入相同行');
});

check('CT07 焦点仍在“全选内容”按钮上时 Ctrl+C 也必须复制完整内容', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({}).handler();
  const total = 1000;
  const texts = makeMarkedTexts(total, 500);
  const { text } = await openDiff(env, texts.left, texts.right);

  // 真实浏览器里点击按钮后焦点留在按钮（在结果区之外），Ctrl+C 的 copy 事件
  // 目标是按钮而不是画布；数据级全选必须仍能接管。
  const selectAllBtn = findButton(text.panel, '全选内容');
  fireEvent(selectAllBtn, 'click');
  const copied = captureCopy(env, selectAllBtn);
  assert.strictEqual(copied.ev.defaultPrevented, true, '按钮持焦时复制也必须由数据级全选接管');
  assert.strictEqual(copied.writes.length, 1, '必须写入剪贴板');
  const payload = copied.writes[0].text;
  assert.ok(payload.includes('FIRST-MARK-' + total) && payload.includes('LAST-MARK-' + total), '按钮持焦时复制内容必须包含首尾标记');
});

check('CT07 全选后的普通鼠标选区仍按原生选区复制（不接管）', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({}).handler();
  const total = 1000;
  const texts = makeMarkedTexts(total, 500);
  const { text } = await openDiff(env, texts.left, texts.right);
  const canvas = vdiffCanvas(text.panel);

  fireEvent(findButton(text.panel, '全选内容'), 'click');
  // 用户在结果区按下鼠标：数据级全选立即失效，之后的复制交回原生选区。
  fireEvent(canvas, 'mousedown');
  const copied = captureCopy(env, canvas);
  assert.strictEqual(copied.ev.defaultPrevented, false, '普通鼠标选区不得被数据级全选接管');
  assert.strictEqual(copied.writes.length, 0, '普通鼠标选区不得由数据级全选写入剪贴板');
});

check('CT07 现有“复制左侧全部 / 复制右侧全部”不退化（全文而非虚拟窗口）', async () => {
  const env = loadCompareModule();
  env.apiHandler = makeBackend({}).handler();
  const total = 1000;
  const texts = makeMarkedTexts(total, 500);
  const { text } = await openDiff(env, texts.left, texts.right);

  const leftAll = findButton(text.panel, '复制左侧全部');
  const rightAll = findButton(text.panel, '复制右侧全部');
  assert.ok(leftAll && rightAll, '更多操作里应保留左右全文复制按钮');

  fireEvent(leftAll, 'click');
  await env.settle(5);
  fireEvent(rightAll, 'click');
  await env.settle(5);

  assert.strictEqual(env.clipboardWrites.length, 2, '两次复制各写一次剪贴板，实际 ' + env.clipboardWrites.length);
  const [leftCopy, rightCopy] = env.clipboardWrites;
  assert.strictEqual(leftCopy.text, leftTextarea(text.panel).value, '复制左侧全部必须来自左侧编辑器全文');
  assert.strictEqual(rightCopy.text, rightTextarea(text.panel).value, '复制右侧全部必须来自右侧编辑器全文');
  assert.ok(leftCopy.text.includes('FIRST-MARK-' + total) && leftCopy.text.includes('LAST-MARK-' + total), '左侧全文必须含首尾标记');
  assert.ok(rightCopy.text.includes('FIRST-MARK-' + total) && rightCopy.text.includes('LAST-MARK-' + total), '右侧全文必须含首尾标记');
});

// ---------------------------------------------------------------------------
// 串行执行
// ---------------------------------------------------------------------------

(async function main() {
  let passed = 0;
  const failures = [];
  for (const item of results) {
    try {
      await item.fn();
      passed++;
      console.log('  ✓ ' + item.name);
    } catch (error) {
      failures.push({ name: item.name, error });
      console.error('  ✗ ' + item.name);
      console.error('    ' + (error && error.message ? error.message : String(error)));
      if (error && error.stack) console.error(String(error.stack).split('\n').slice(0, 4).join('\n'));
    } finally {
      while (checkCleanups.length) {
        const cleanup = checkCleanups.pop();
        try { cleanup(); } catch (_) {}
      }
    }
  }
  console.log('compare-tabs-lifecycle: ' + passed + ' / ' + results.length + ' cases passed');
  if (failures.length) {
    console.error('失败用例：');
    failures.forEach(f => console.error('  - ' + f.name));
    process.exitCode = 1;
  }
  // 兜底：任何遗留的扫描轮询定时器都不能挂住调用方 runner。
  // 正常路径下 Node 会在此之前自然退出，unref 的看门狗不参与保活。
  const watchdog = setTimeout(() => process.exit(process.exitCode || 0), 3000);
  if (watchdog.unref) watchdog.unref();
})().catch(error => {
  console.error('测试运行器异常:', error);
  process.exit(1);
});
