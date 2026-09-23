// tests/compare-save-lifecycle.test.js
// Batch D 回归（CT01 / CT06 / CT02 / CT04）：
// 比较工作台的文档身份、行内草稿提交、保存快照推进与左右交换撤回。
//
// 做法与 tests/database-review-regression.js 一致：把 web/pages/compare.js 的真实函数
// 放进 vm + 极简 DOM 双中执行，断言真正发出的写请求（目标路径 / 正文 / expected 版本）
// 以及磁盘副作用，而不是断言内部标志位。
//
// 未验证项：真实浏览器布局、真实 blur 顺序、真实指针/滚动行为。这里只有 DOM 双。
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
  ['click', 'change', 'input', 'dblclick', 'submit', 'keydown', 'scroll'].forEach(type => {
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
const TEST_BRIDGE_EXPORTS = 'Kairo.compareTest = { buildTextWorkbench, buildFolderWorkbench, createVirtualDiff, makeDiffCell,';

function loadCompareModule() {
  const source = COMPARE_SRC.replace(TEST_BRIDGE_ANCHOR, TEST_BRIDGE_EXPORTS);
  assert.notStrictEqual(source, COMPARE_SRC, '必须能在 web/pages/compare.js 中定位 Kairo.compareTest 导出口');

  const requests = [];

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
    createRange: () => ({ selectNodeContents() {}, collapse() {}, selectNode() {}, setStart() {}, setEnd() {} }),
    getElementById: id => (id === 'crumb' ? document.crumb : null),
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener(type, fn) { (document.listeners[type] = document.listeners[type] || []).push(fn); },
    removeEventListener(type, fn) {
      if (!document.listeners[type]) return;
      document.listeners[type] = document.listeners[type].filter(item => item !== fn);
    },
    execCommand: () => true,
    removeEventListenerAll() {}
  };
  document.crumb = document.createElement('div');
  document.body.ownerDocumentRef = document;

  const window = {
    Kairo: null,
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    location: { hash: '' },
    confirm: () => true,
    isSecureContext: true,
    navigator: { clipboard: { writeText: async () => {} } },
    getSelection: () => ({ removeAllRanges() {}, addRange() {}, selectAllChildren() {}, toString: () => '' }),
    addEventListener(type, fn) { (window.listeners[type] = window.listeners[type] || []).push(fn); },
    removeEventListener() {},
    listeners: {}
  };
  window.window = window;

  const env = {
    document,
    window,
    requests,
    apiHandler: null,
    // 记录每个写请求，供用例断言真实 HTTP 负载。
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
      copyToClipboard: async () => {},
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
      // 来源对话框：记录最后一次弹层的 body，便于将来直接驱动对话框路径。
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
    tabs: { isActive: () => true, getActiveId: () => 'compare' },
    pages: {},
    state: { routes: {}, routeNames: {} }
  };
  window.Kairo = Kairo;
  env.toasts = [];
  env.confirmAnswer = true;
  env.modalBody = null;

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
  env.settle = async (rounds = 8) => {
    for (let i = 0; i < rounds; i++) await new Promise(resolve => setTimeout(resolve, 1));
  };
  env.waitFor = async (predicate, label) => {
    for (let i = 0; i < 400; i++) {
      if (predicate()) return true;
      await new Promise(resolve => setTimeout(resolve, 5));
    }
    throw new Error('等待超时：' + (label || 'condition'));
  };
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

// 模拟后端：本地文件表 + size/mtime 版本 + 条件写入（与 comparefs 现有语义一致）
function makeBackend(files) {
  let clock = 0;
  const nextMtime = () => { clock += 1000; return '2026-09-22T00:00:' + String(clock).padStart(2, '0') + 'Z'; };
  const filesystem = {};
  Object.keys(files).forEach(key => {
    filesystem[key] = { content: String(files[key].content), mtime: files[key].mtime || '2026-09-22T00:00:00Z' };
  });
  return {
    files: filesystem,
    versionOf(pathValue) {
      const entry = filesystem[pathValue];
      return { size: Buffer.byteLength(entry.content, 'utf8'), mtime: entry.mtime, path: pathValue };
    },
    handler(writeGate) {
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
          if (writeGate) await writeGate;
          const entry = filesystem[body.target.path];
          if (!entry) { const err = new Error('目标文件不存在'); err.status = 404; throw err; }
          if (body.expected) {
            const conflicts = body.expected.size !== Buffer.byteLength(entry.content, 'utf8') ||
              (body.expected.mtime && body.expected.mtime !== entry.mtime);
            if (conflicts) { const err = new Error('目标文件已变化，请重新比较后再保存'); err.status = 409; throw err; }
          }
          entry.content = String(body.content);
          entry.mtime = nextMtime();
          return {
            ok: true,
            path: body.target.path,
            document_id: body.document_id || '',
            digest: 'sha256:' + Buffer.from(entry.content, 'utf8').toString('hex').slice(0, 16),
            version: { size: Buffer.byteLength(entry.content, 'utf8'), mtime: entry.mtime, path: body.target.path }
          };
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

function buildTextWorkbench(env, state, options) {
  const panel = env.document.createElement('section');
  env.document.body.appendChild(panel);
  const cleanup = env.compareTest.buildTextWorkbench(panel, state, options || makeOptions());
  return { panel, state, cleanup };
}

function leftTextarea(panel) { return panel.querySelectorAll('textarea')[0]; }
function rightTextarea(panel) { return panel.querySelectorAll('textarea')[1]; }

function typeInto(textarea, value) {
  textarea.value = value;
  fireEvent(textarea, 'input');
}

const results = [];
function check(name, fn) {
  results.push({ name, fn });
}

// ---------------------------------------------------------------------------
// CT01：取消更换文件不得改变保存目标
// ---------------------------------------------------------------------------

check('CT01 取消打开 B 后，保存必须仍写入 A（且 B 字节不变）', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'AAA', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'BBB', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  // 文件夹比较与文本工作台共用同一个 state（真实 renderCompare 也是这样）。
  const text = buildTextWorkbench(env, state);
  const folderPanel = env.document.createElement('section');
  env.document.body.appendChild(folderPanel);
  const scanResult = {
    items: [
      { rel_path: 'A.txt', status: 'different', left: { path: '/left/A.txt', is_dir: false, size: 3, mtime: '2026-09-22T00:00:00Z' }, right: { path: '/right/A.txt', is_dir: false, size: 3, mtime: '2026-09-22T00:00:00Z' } },
      { rel_path: 'B.txt', status: 'different', left: { path: '/left/B.txt', is_dir: false, size: 3, mtime: '2026-09-22T00:00:00Z' }, right: { path: '/right/B.txt', is_dir: false, size: 3, mtime: '2026-09-22T00:00:00Z' } }
    ],
    summary: { same: 0, different: 2, left_newer: 0, right_newer: 0, left_only: 0, right_only: 0, errors: 0 }
  };
  env.apiHandler = async (method, url, body) => {
    if (url === '/api/compare/scan') return { job_id: 'job-1' };
    if (url === '/api/compare/jobs/job-1') return { status: 'completed', result: scanResult, total: 1, current: 1 };
    if (url === '/api/compare/scan-level') return { items: [] };
    return backend.handler()(method, url, body);
  };
  const switchTab = () => {};
  const folderWb = env.compareTest.buildFolderWorkbench(folderPanel, state, makeOptions(), switchTab, null);

  const pathInputs = folderPanel.querySelectorAll('.cmp-folder-path');
  assert.strictEqual(pathInputs.length, 2, '文件夹工作台应有左右两个目录输入');
  pathInputs[0].value = '/left'; fireEvent(pathInputs[0], 'change');
  pathInputs[1].value = '/right'; fireEvent(pathInputs[1], 'change');
  await folderWb.startScan();
  await env.waitFor(() => state.scan && (state.scan.items || []).length >= 2, '目录扫描完成');
  await env.settle(10); // 等 createFolderTree 的 requestAnimationFrame(render) 真正渲染行

  const rowFor = rel => folderPanel.querySelectorAll('.cmp-folder-row').find(node => node.getAttribute('data-rel') === rel);
  const viewport = folderPanel.querySelector('.cmp-folder-viewport');
  assert.ok(viewport, '文件夹比较应生成结果视图');

  // 1) 打开 A 对，编辑左侧但不保存。
  const rowA = rowFor('A.txt');
  assert.ok(rowA, '扫描结果应包含 A.txt 行');
  fireEvent(rowA, 'dblclick');
  await env.waitFor(() => state.left.source.path === '/left/A.txt' && state.left.loadState === 'ready', 'A 对被真实 loadPair 打开');
  assert.strictEqual(state.textWorkbench && typeof state.textWorkbench.loadPair, 'function', 'loadPair 必须是文本工作台的公开入口');
  typeInto(leftTextarea(text.panel), 'A edited');
  assert.strictEqual(state.left.dirty, true, '编辑后左侧应为脏');

  // 2) 尝试打开 B 对并取消确认。
  env.confirmAnswer = false;
  const rowB = rowFor('B.txt');
  assert.ok(rowB, '扫描结果应包含 B.txt 行');
  fireEvent(rowB, 'dblclick');
  await env.settle(20);

  assert.strictEqual(state.left.source.path, '/left/A.txt', '取消打开 B 之后，左侧来源必须仍是 A');
  assert.strictEqual(state.right.source.path, '/right/A.txt', '取消打开 B 之后，右侧来源必须仍是 A');
  assert.strictEqual(state.left.dirty, true, '取消打开不得清掉未保存修改');
  assert.strictEqual(backend.files['/left/B.txt'].content, 'BBB', '取消打开阶段绝不能改动 B 的字节');

  // 3) 取消后继续保存：必须发往 A，并且真的写进 A。
  const saveLeftBtn = text.panel.querySelector('[data-action="save-left"]');
  assert.ok(saveLeftBtn, '工具栏应有保存左侧按钮');
  fireEvent(saveLeftBtn, 'click');
  await env.waitFor(() => env.writes().length > 0, '发出写请求');
  await env.settle(20);

  const write = env.writes()[0];
  assert.strictEqual(write.body.target.path, '/left/A.txt', '写请求目标必须是 A，不能是 B');
  assert.strictEqual(write.body.content, 'A edited', '写请求正文必须是 A 的编辑稿');
  assert.strictEqual(write.body.expected.mtime, '2026-09-22T00:00:00Z', 'expected 必须是 A 读到的版本');
  assert.strictEqual(backend.files['/left/A.txt'].content, 'A edited', 'A 必须被写入');
  assert.strictEqual(backend.files['/left/B.txt'].content, 'BBB', 'B 必须保持 BBB 不被改写');
});

check('CT01 取消打开后版本/baseline 保持成套（新路径不与旧版本配对）', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'AAA', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'BBB', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);

  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/right/A.txt', encoding: 'auto' });
  const versionAtOpen = JSON.parse(JSON.stringify(state.left.version));
  const baselineAtOpen = state.left.baseline;
  typeInto(leftTextarea(text.panel), 'A draft');

  env.confirmAnswer = false;
  env.requests.length = 0;
  const cancelled = await state.textWorkbench.loadPair({ kind: 'local', path: '/left/B.txt', encoding: 'auto' }, { kind: 'local', path: '/right/B.txt', encoding: 'auto' });
  assert.strictEqual(cancelled, false, '取消必须返回 false');

  assert.strictEqual(state.left.source.path, '/left/A.txt', '取消不得提交新来源');
  assert.strictEqual(typeof env.compareTest.documentIdOf, 'function', '必须导出 documentIdOf：文档身份要能绑定读取、保存与撤回');
  assert.strictEqual(state.left.documentId, env.compareTest.documentIdOf(state.left.source), '取消后文档身份必须仍属于 A');
  assert.notStrictEqual(state.left.documentId, env.compareTest.documentIdOf({ kind: 'local', path: '/left/B.txt' }), 'B 的文档身份不得被提交到左侧');
  assert.deepStrictEqual(state.left.version, versionAtOpen, '取消不得改动版本');
  assert.strictEqual(state.left.baseline, baselineAtOpen, '取消不得改动 baseline');
  assert.strictEqual(leftTextarea(text.panel).value, 'A draft', '取消不得改动正文');
  assert.strictEqual(env.requests.filter(r => r.url === '/api/compare/read').length, 0, '取消后不得发起读取');
});

// ---------------------------------------------------------------------------
// CT06：行内草稿必须先提交再保存
// ---------------------------------------------------------------------------

check('CT06 行内草稿未 blur 时 Ctrl+S 必须写入最新草稿', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'A one\nA two\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'A one\nB two\n', mtime: '2026-09-22T00:00:00Z' }
  });
  let releaseWrite;
  const writeGate = new Promise(resolve => { releaseWrite = resolve; });
  let gateWrites = 0;
  const backendHandler = backend.handler();
  env.apiHandler = async (method, url, body) => {
    if (url === '/api/compare/write' && gateWrites++ === 0) await writeGate;
    return backendHandler(method, url, body);
  };
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });

  typeInto(leftTextarea(text.panel), 'A edited\nA two\n');
  // 触发真实比较，生成真实的虚拟差异单元格与 keydown handler。
  env.fire(text.panel, 'keydown', { key: 'Enter', ctrlKey: true });
  await env.settle(20);

  const codeCells = text.panel.querySelectorAll('.cmp-vdiff-canvas .cmp-diff-left .cmp-code');
  assert.ok(codeCells.length > 0, '差异视图应渲染左侧可编辑单元格');
  const code = codeCells[0];
  env.document.activeElement = code;
  env.fire(code, 'mousedown');
  assert.strictEqual(code.isContentEditable, true, '点击单元格应进入行内编辑');
  code.textContent = 'A inline latest';
  env.fire(code, 'input');

  // 不 blur、不按 Ctrl+Enter，直接 Ctrl+S。
  env.fire(code, 'keydown', { key: 's', ctrlKey: true });
  await env.waitFor(() => env.writes().length > 0, 'Ctrl+S 发出写请求');
  // 提交草稿不得立刻重比：写请求在途时正在编辑的节点必须仍在文档里。
  assert.strictEqual(text.panel.contains(code), true, '提交草稿时不得立即重比并删除正在编辑的节点');
  releaseWrite();
  await env.settle(30);

  const write = env.writes()[0];
  assert.ok(write.body.content.includes('A inline latest'), '保存正文必须包含尚未 blur 的行内草稿，实际 = ' + JSON.stringify(write.body.content));
  assert.ok(!write.body.content.includes('A edited'), '保存正文不得仍是旧的已提交文本');
  assert.strictEqual(backend.files['/left/A.txt'].content, 'A inline latest\nA two\n', '磁盘必须包含最新行内输入');
});

check('CT06 保存入口（按钮 / 双侧）也必须先提交行内草稿', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'A one\nA two\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'A one\nB two\n', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });

  typeInto(leftTextarea(text.panel), 'A edited\nA two\n');
  env.fire(text.panel, 'keydown', { key: 'Enter', ctrlKey: true });
  await env.settle(20);
  const code = text.panel.querySelectorAll('.cmp-vdiff-canvas .cmp-diff-left .cmp-code')[0];
  assert.ok(code, '差异视图应渲染左侧可编辑单元格');
  env.document.activeElement = code;
  env.fire(code, 'mousedown');
  code.textContent = 'A button latest';
  env.fire(code, 'input');

  const saveLeftBtn = text.panel.querySelector('[data-action="save-left"]');
  assert.strictEqual(saveLeftBtn.disabled, false, '存在未提交行内草稿时保存按钮必须可用');
  fireEvent(saveLeftBtn, 'click');
  await env.waitFor(() => env.writes().length > 0, '按钮保存发出写请求');
  await env.settle(20);
  assert.strictEqual(env.writes()[0].body.content, 'A button latest\nA two\n', '按钮保存的正文必须是行内最新草稿');
});

// ---------------------------------------------------------------------------
// CT02：保存期间继续编辑，成功后必须推进版本
// ---------------------------------------------------------------------------

check('CT02 保存期间编辑 C2：C1 成功后推进版本、保留 C2、下一次 expected 为新版本', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'A one\nA two\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'A one\nB two\n', mtime: '2026-09-22T00:00:00Z' }
  });
  let releaseWrite;
  const writeGate = new Promise(resolve => { releaseWrite = resolve; });
  let gateWrites = 0;
  const backendHandler = backend.handler();
  env.apiHandler = async (method, url, body) => {
    if (url === '/api/compare/write' && gateWrites++ === 0) await writeGate;
    return backendHandler(method, url, body);
  };
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  const versionV1 = JSON.parse(JSON.stringify(state.left.version));

  typeInto(leftTextarea(text.panel), 'C1\nA two\n');
  const saveLeftBtn = text.panel.querySelector('[data-action="save-left"]');
  fireEvent(saveLeftBtn, 'click');
  await env.waitFor(() => env.writes().length === 1, '第一次写请求已发出');
  assert.deepStrictEqual(env.writes()[0].body.expected, versionV1, '第一次写请求 expected 应为 V1');

  // 写请求未完成时继续编辑成 C2。
  typeInto(leftTextarea(text.panel), 'C2\nA two\n');
  assert.strictEqual(state.left.dirty, true, '等待期间继续编辑必须保持脏');
  saveLeftBtn.disabled = true; // 模拟保存中按钮被禁用；Ctrl+S 也不能绕过

  releaseWrite();
  await env.settle(30);

  assert.notDeepStrictEqual(state.left.version, versionV1, '写成功后版本必须前进到服务端返回的新版本');
  assert.strictEqual(state.left.version.mtime, backend.files['/left/A.txt'].mtime, '前端版本必须等于服务端返回的新版本');
  assert.strictEqual(leftTextarea(text.panel).value, 'C2\nA two\n', '等待期间的新输入必须保留');
  assert.strictEqual(state.left.dirty, true, '当前内容 C2 与已保存内容 C1 不同，必须仍为脏');
  const versionAfterFirstSave = JSON.parse(JSON.stringify(state.left.version));

  // 第二次保存：正文必须是 C2，expected 必须是新版本，且服务端不再 409。
  fireEvent(saveLeftBtn, 'click');
  await env.waitFor(() => env.writes().length === 2, '第二次写请求已发出');
  await env.settle(30);
  assert.strictEqual(env.writes()[1].body.content, 'C2\nA two\n', '第二次保存必须写 C2');
  assert.deepStrictEqual(env.writes()[1].body.expected, versionAfterFirstSave, '第二次 expected 必须是推进后的版本');
  assert.strictEqual(backend.files['/left/A.txt'].content, 'C2\nA two\n', '磁盘最终必须为 C2');
  assert.ok(!env.toasts.some(t => t.type === 'err'), '不应出现保存失败：' + JSON.stringify(env.toasts));
});

check('CT02 保存期间连按 Ctrl+S 不得重复发出写请求', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({ '/left/A.txt': { content: 'A one\n', mtime: '2026-09-22T00:00:00Z' }, '/left/B.txt': { content: 'B one\n', mtime: '2026-09-22T00:00:00Z' } });
  let releaseWrite;
  const writeGate = new Promise(resolve => { releaseWrite = resolve; });
  let gateWrites = 0;
  const backendHandler = backend.handler();
  env.apiHandler = async (method, url, body) => {
    if (url === '/api/compare/write' && gateWrites++ === 0) await writeGate;
    return backendHandler(method, url, body);
  };
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  typeInto(leftTextarea(text.panel), 'C1\n');
  env.document.activeElement = leftTextarea(text.panel);
  env.fire(text.panel, 'keydown', { key: 's', ctrlKey: true });
  await env.waitFor(() => env.writes().length === 1, '第一次写请求已发出');
  env.fire(text.panel, 'keydown', { key: 's', ctrlKey: true });
  env.fire(text.panel, 'keydown', { key: 's', ctrlKey: true });
  await env.settle(10);
  assert.strictEqual(env.writes().length, 1, '保存进行中不得绕过 if (item.saving) 重复发写');
  releaseWrite();
  await env.settle(30);
});

// ---------------------------------------------------------------------------
// CT04：交换之后必须能撤回
// ---------------------------------------------------------------------------

check('CT04 交换→撤回：两侧完整文档状态必须回原状', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'AAA\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'BBB\n', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });

  const before = {
    leftSource: JSON.parse(JSON.stringify(state.left.source)),
    rightSource: JSON.parse(JSON.stringify(state.right.source)),
    leftVersion: JSON.parse(JSON.stringify(state.left.version)),
    rightVersion: JSON.parse(JSON.stringify(state.right.version)),
    leftBaseline: state.left.baseline,
    rightBaseline: state.right.baseline,
    leftText: leftTextarea(text.panel).value,
    rightText: rightTextarea(text.panel).value
  };

  const swapBtn = text.panel.querySelector('[data-action="swap-sides"]');
  const undoBtn = text.panel.querySelector('[data-action="undo-diff"]');
  assert.ok(swapBtn && undoBtn, '工具栏应有交换与撤回按钮');
  fireEvent(swapBtn, 'click');
  assert.strictEqual(state.left.source.path, '/left/B.txt', '交换后左侧来源应为 B');
  assert.strictEqual(leftTextarea(text.panel).value, before.rightText, '交换后左侧正文应为原右侧正文');

  fireEvent(undoBtn, 'click');
  await env.settle(20);

  assert.strictEqual(state.left.source.path, '/left/A.txt', '撤回交换后左侧来源必须回到 A');
  assert.strictEqual(state.right.source.path, '/left/B.txt', '撤回交换后右侧来源必须回到 B');
  assert.strictEqual(leftTextarea(text.panel).value, before.leftText, '撤回交换后左侧正文必须回到 A 的正文');
  assert.strictEqual(rightTextarea(text.panel).value, before.rightText, '撤回交换后右侧正文必须回到 B 的正文');
  assert.deepStrictEqual(state.left.version, before.leftVersion, '撤回交换必须恢复 A 的版本');
  assert.deepStrictEqual(state.right.version, before.rightVersion, '撤回交换必须恢复 B 的版本');
  assert.strictEqual(state.left.baseline, before.leftBaseline, '撤回交换必须恢复 A 的 baseline');
  assert.strictEqual(state.right.baseline, before.rightBaseline, '撤回交换必须恢复 B 的 baseline');
  assert.strictEqual(state.left.loadState, 'ready', '撤回交换必须恢复加载状态');
  assert.strictEqual(state.right.loadState, 'ready', '撤回交换必须恢复加载状态');

  // 撤回后保存必须回到原目标 A。
  typeInto(leftTextarea(text.panel), 'AAA edited\n');
  const saveLeftBtn = text.panel.querySelector('[data-action="save-left"]');
  fireEvent(saveLeftBtn, 'click');
  await env.waitFor(() => env.writes().length > 0, '撤回后保存发出写请求');
  await env.settle(20);
  assert.strictEqual(env.writes()[0].body.target.path, '/left/A.txt', '撤回交换后保存必须写回 A');
});

check('CT04 交换后真正不匹配时必须阻止，且不得先 pop 掉快照', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/left/A.txt': { content: 'AAA\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/B.txt': { content: 'BBB\n', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });

  const swapBtn = text.panel.querySelector('[data-action="swap-sides"]');
  const undoBtn = text.panel.querySelector('[data-action="undo-diff"]');
  fireEvent(swapBtn, 'click'); // 交换：left=B, right=A，并压入 swap 命令
  assert.strictEqual(state.left.source.path, '/left/B.txt', '交换后左侧应为 B');
  assert.strictEqual(state.right.source.path, '/left/A.txt', '交换后右侧应为 A');
  const swappedLeftIdentity = state.left.documentId;

  // 模拟“绕过读取”的身份变化（例如外部代码把这一侧的文档身份换成了 C）：
  // 前置条件不再成立，撤回必须被阻止，而且记录不能被 pop 掉。
  state.left.source = { kind: 'local', path: '/left/C.txt', encoding: 'auto' };
  state.left.documentId = 'tampered-document-identity';
  env.toasts.length = 0;
  fireEvent(undoBtn, 'click');
  await env.settle(20);
  assert.strictEqual(state.left.source.path, '/left/C.txt', '前置条件不满足时不得应用旧的交换快照');
  assert.ok(env.toasts.some(t => /不匹配|阻止/.test(t.message)), '必须给出清晰的阻止提示，实际：' + JSON.stringify(env.toasts));

  // 恢复交换后的身份：被保留的记录必须仍可撤回。
  state.left.source = { kind: 'local', path: '/left/B.txt', encoding: 'auto' };
  state.left.documentId = swappedLeftIdentity;
  fireEvent(undoBtn, 'click');
  await env.settle(20);
  assert.strictEqual(state.left.source.path, '/left/A.txt', '恢复前置条件后，被保留的交换记录必须仍可撤回');
  assert.strictEqual(state.right.source.path, '/left/B.txt', '撤回交换后右侧必须回到 B');
});

// ---------------------------------------------------------------------------
// CT01 补充：读取失败与“载入并比对”入口
// ---------------------------------------------------------------------------

check('CT01 读取新文件失败时：新路径不得与旧正文/旧版本并存', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({ '/left/A.txt': { content: 'A one\nA two\n', mtime: '2026-09-22T00:00:00Z' }, '/left/B.txt': { content: 'B one\n', mtime: '2026-09-22T00:00:00Z' } });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });

  const opened = await state.textWorkbench.loadPair({ kind: 'local', path: '/left/missing.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  assert.strictEqual(opened, false, '读取失败时不得返回成功');
  assert.strictEqual(state.left.source.path, '/left/missing.txt', '失败后来源是新路径');
  assert.strictEqual(state.left.version, null, '失败后不得保留旧版本');
  assert.strictEqual(state.left.loadState, 'error', '失败后必须进入独立错误状态');
  assert.strictEqual(leftTextarea(text.panel).value, '', '失败后不得保留旧正文（避免旧文本配新路径）');
  assert.strictEqual(state.left.dirty, false, '读取失败不得把状态标成待保存');
});

check('CT01 “载入并比对”输入框入口也必须走同一个切换文档命令', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({ '/left/A.txt': { content: 'A one\n', mtime: '2026-09-22T00:00:00Z' }, '/left/B.txt': { content: 'B one\n', mtime: '2026-09-22T00:00:00Z' } });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);

  const barInputs = text.panel.querySelectorAll('.cmp2-file-inputs-bar input');
  assert.strictEqual(barInputs.length, 2, '文件输入栏应有左右两个路径输入');
  barInputs[0].value = '/left/A.txt';
  barInputs[1].value = '/left/B.txt';
  const loadBtn = text.panel.querySelector('.cmp2-file-load-btn');
  assert.ok(loadBtn, '应有“载入并比对”按钮');
  fireEvent(loadBtn, 'click');
  await env.waitFor(() => state.left.loadState === 'ready' && state.left.version && state.right.version, '两侧通过输入栏载入完成');
  assert.strictEqual(state.left.source.path, '/left/A.txt', '左侧来源必须提交为输入路径');
  assert.strictEqual(state.right.source.path, '/left/B.txt', '右侧来源必须提交为输入路径');
  assert.ok(state.left.documentId && state.right.documentId, '载入后必须提交文档身份');

  typeInto(leftTextarea(text.panel), 'A edited\n');
  const saveLeftBtn = text.panel.querySelector('[data-action="save-left"]');
  fireEvent(saveLeftBtn, 'click');
  await env.waitFor(() => env.writes().length > 0, '载入后保存发出写请求');
  await env.settle(20);
  assert.strictEqual(env.writes()[0].body.target.path, '/left/A.txt', '保存必须写输入框指定的路径');
  assert.strictEqual(env.writes()[0].body.document_id, state.left.documentId, '写请求必须带上文档身份');
  assert.ok(env.writes()[0].body.edit_seq >= 1, '写请求必须带上编辑序号');
});

check('CT01 右侧脏 + 文本来源混合时取消打开：两侧都保持原状，保存仍写原目标', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({
    '/right/B.txt': { content: 'B one\n', mtime: '2026-09-22T00:00:00Z' },
    '/left/A.txt': { content: 'A one\n', mtime: '2026-09-22T00:00:00Z' },
    '/right/A.txt': { content: 'A right\n', mtime: '2026-09-22T00:00:00Z' }
  });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  // 混合来源：左侧临时文本，右侧本地文件。
  await state.textWorkbench.loadPair({ kind: 'text', label: '左侧临时文本' }, { kind: 'local', path: '/right/B.txt', encoding: 'auto' });
  assert.strictEqual(state.left.source.kind, 'text', '左侧应为临时文本来源');
  typeInto(leftTextarea(text.panel), 'left text draft');
  typeInto(rightTextarea(text.panel), 'B edited\n');
  const rightBefore = {
    source: JSON.stringify(state.right.source),
    version: JSON.stringify(state.right.version),
    baseline: state.right.baseline
  };

  env.confirmAnswer = false;
  const cancelled = await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/right/A.txt', encoding: 'auto' });
  assert.strictEqual(cancelled, false, '取消必须返回 false');
  assert.strictEqual(state.left.source.kind, 'text', '取消后左侧必须仍是临时文本来源');
  assert.strictEqual(leftTextarea(text.panel).value, 'left text draft', '取消后左侧草稿必须保留');
  assert.strictEqual(JSON.stringify(state.right.source), rightBefore.source, '取消后右侧来源必须不变');
  assert.strictEqual(JSON.stringify(state.right.version), rightBefore.version, '取消后右侧版本必须不变');
  assert.strictEqual(state.right.baseline, rightBefore.baseline, '取消后右侧 baseline 必须不变');
  assert.strictEqual(rightTextarea(text.panel).value, 'B edited\n', '取消后右侧草稿必须保留');

  // 取消后保存右侧：必须仍写原来的 /right/B.txt。
  fireEvent(text.panel.querySelector('[data-action="save-right"]'), 'click');
  await env.waitFor(() => env.writes().length > 0, '右侧保存发出写请求');
  await env.settle(20);
  assert.strictEqual(env.writes()[0].body.target.path, '/right/B.txt', '取消后右侧保存必须写回 B');
  assert.strictEqual(backend.files['/right/B.txt'].content, 'B edited\n', 'B 必须被写入编辑稿');
});

// ---------------------------------------------------------------------------
// CT06 补充：草稿登记、保存后可见与编码保持
// ---------------------------------------------------------------------------

check('CT06 行内输入立即登记到文档草稿状态，保存后内容仍可见', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({ '/left/A.txt': { content: 'A one\nA two\n', mtime: '2026-09-22T00:00:00Z' }, '/left/B.txt': { content: 'A one\nB two\n', mtime: '2026-09-22T00:00:00Z' } });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  typeInto(leftTextarea(text.panel), 'A edited\nA two\n');
  env.fire(text.panel, 'keydown', { key: 'Enter', ctrlKey: true });
  await env.settle(20);
  const code = text.panel.querySelectorAll('.cmp-vdiff-canvas .cmp-diff-left .cmp-code')[0];
  env.document.activeElement = code;
  env.fire(code, 'mousedown');
  code.textContent = 'A draft without blur';
  env.fire(code, 'input');

  // 未 blur 的草稿必须已经进入文档状态（关闭保护 / 虚拟窗口重建据此判断）。
  assert.ok(state.left.draft, '行内输入必须立即登记文档草稿');
  assert.strictEqual(state.left.draft.text, 'A draft without blur', '草稿内容必须是可见文本');
  assert.strictEqual(state.left.draft.lineNo, 1, '草稿必须记录行号');
  assert.strictEqual(text.panel.querySelector('[data-action="save-left"]').disabled, false, '有草稿时保存按钮必须可用');

  const undoCountBefore = env.requests.filter(r => r.url === '/api/compare/undo').length;
  env.fire(code, 'keydown', { key: 's', ctrlKey: true });
  await env.waitFor(() => env.writes().length > 0, 'Ctrl+S 发出写请求');
  await env.settle(20);
  const write = env.writes()[0];
  assert.strictEqual(write.body.content, 'A draft without blur\nA two\n', '保存正文必须包含最新草稿');
  assert.strictEqual(write.body.encoding, 'utf-8', '保存必须沿用读取到的编码');
  assert.strictEqual(write.body.eol, 'lf', '保存必须沿用读取到的换行风格');
  assert.strictEqual(leftTextarea(text.panel).value, 'A draft without blur\nA two\n', '保存后最新内容必须仍然可见');
  assert.strictEqual(state.left.dirty, false, '保存后已无未保存修改');
  assert.strictEqual(undoCountBefore, 0);
});

// ---------------------------------------------------------------------------
// CT02 补充：外部 409 冲突
// ---------------------------------------------------------------------------

check('CT02 外部 409：必须保留本地草稿并明确提示，不得自动重读覆盖', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({ '/left/A.txt': { content: 'A one\n', mtime: '2026-09-22T00:00:00Z' }, '/left/B.txt': { content: 'B one\n', mtime: '2026-09-22T00:00:00Z' } });
  let releaseWrite;
  const writeGate = new Promise(resolve => { releaseWrite = resolve; });
  let gateWrites = 0;
  const backendHandler = backend.handler();
  env.apiHandler = async (method, url, body) => {
    if (url === '/api/compare/write' && gateWrites++ === 0) await writeGate;
    return backendHandler(method, url, body);
  };
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });
  typeInto(leftTextarea(text.panel), 'local draft\n');
  const readsBefore = env.requests.filter(r => r.url === '/api/compare/read').length;
  fireEvent(text.panel.querySelector('[data-action="save-left"]'), 'click');
  await env.waitFor(() => env.writes().length === 1, '写请求已发出');
  // 第三方在写入前改了磁盘：mtime 变化会让条件写入返回 409。
  backend.files['/left/A.txt'].mtime = '2026-09-22T09:09:09Z';
  backend.files['/left/A.txt'].content = 'external change\n';
  releaseWrite();
  await env.settle(30);

  assert.strictEqual(leftTextarea(text.panel).value, 'local draft\n', '冲突后必须保留本地草稿');
  assert.strictEqual(state.left.dirty, true, '冲突后必须仍是脏状态');
  assert.ok(state.left.externalConflict, '必须记录这是外部冲突');
  assert.strictEqual(env.requests.filter(r => r.url === '/api/compare/read').length, readsBefore, '冲突后不得自动重读覆盖草稿');
  assert.ok(env.toasts.some(t => t.type === 'warn' && /外部|冲突/.test(t.message)), '必须给出明确的冲突提示：' + JSON.stringify(env.toasts));
  assert.strictEqual(backend.files['/left/A.txt'].content, 'external change\n', '被拒绝的保存不得改动磁盘');
});

// ---------------------------------------------------------------------------
// CT04 补充：交换→编辑→撤回编辑→撤回交换
// ---------------------------------------------------------------------------

check('CT04 交换→编辑→撤回编辑→撤回交换的完整链', async () => {
  const env = loadCompareModule();
  const backend = makeBackend({ '/left/A.txt': { content: 'AAA\n', mtime: '2026-09-22T00:00:00Z' }, '/left/B.txt': { content: 'BBB\n', mtime: '2026-09-22T00:00:00Z' } });
  env.apiHandler = backend.handler();
  const state = makeState();
  const text = buildTextWorkbench(env, state);
  await state.textWorkbench.loadPair({ kind: 'local', path: '/left/A.txt', encoding: 'auto' }, { kind: 'local', path: '/left/B.txt', encoding: 'auto' });

  const swapBtn = text.panel.querySelector('[data-action="swap-sides"]');
  const undoBtn = text.panel.querySelector('[data-action="undo-diff"]');
  fireEvent(swapBtn, 'click'); // left=B, right=A
  // 交换后在左侧行内编辑并提交（这会压入一条 editLine 记录）。
  const code = text.panel.querySelectorAll('.cmp-vdiff-canvas .cmp-diff-left .cmp-code')[0];
  assert.ok(code, '交换后差异视图应渲染左侧可编辑单元格');
  env.document.activeElement = code;
  env.fire(code, 'mousedown');
  code.textContent = 'BBB swapped edit';
  env.fire(code, 'input');
  env.fire(code, 'keydown', { key: 'Enter', ctrlKey: true }); // Ctrl+Enter 完成编辑
  await env.settle(20);
  assert.strictEqual(leftTextarea(text.panel).value, 'BBB swapped edit\n', '行内编辑提交后左侧正文应为 B 的编辑稿');

  fireEvent(undoBtn, 'click'); // 先撤回编辑
  await env.settle(20);
  assert.strictEqual(leftTextarea(text.panel).value, 'BBB\n', '必须先撤回编辑，回到交换后的 B 正文');
  assert.strictEqual(state.left.source.path, '/left/B.txt', '撤回编辑不得改变文档身份');
  fireEvent(undoBtn, 'click'); // 再撤回交换
  await env.settle(20);
  assert.strictEqual(state.left.source.path, '/left/A.txt', '第二次撤回必须撤销交换');
  assert.strictEqual(leftTextarea(text.panel).value, 'AAA\n', '撤回交换后左侧正文回到 A');
  assert.strictEqual(state.right.source.path, '/left/B.txt', '撤回交换后右侧回到 B');
  assert.strictEqual(rightTextarea(text.panel).value, 'BBB\n', '撤回交换后右侧正文回到 B');
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
    }
  }
  console.log('compare-save-lifecycle: ' + passed + ' / ' + results.length + ' cases passed');
  if (failures.length) {
    console.error('失败用例：');
    failures.forEach(f => console.error('  - ' + f.name));
    process.exitCode = 1;
  }
})().catch(error => {
  console.error('测试运行器异常:', error);
  process.exit(1);
});
