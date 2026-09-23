// tests/files-path-identity.test.js
// Batch F / OTH-05 前端回归：web/pages/files.js 的 path 身份贯通。
//
// 做法与 tests/compare-save-lifecycle.test.js 一致：把 web/pages/files.js 的真实源码
// 放进 vm，配极简 DOM 双 + 记录式 api 双，然后**调用真实页面函数**
// （doListDir / renderTable / doDownload / doDownloadSingle / openPreview / editFile /
//   doRename / doNewFile / showContextMenu），断言真正发出去的请求负载与真正渲染出来的
// 文案，而不是重新实现一遍逻辑。
//
// 注入方式：在 renderFiles 的闭合括号前插入一行测试桥调用，把闭包内部的函数导出给
// 测试（files.js 本身没有 Kairo.compareTest 那样的正式导出口）。web/pages/files.js 不被修改。
//
// 覆盖：
//   1. 下载请求的 path_ids 与 paths 并行、每项来自条目身份而不是展示名；
// 覆盖（web/pages/files.js 与 web/pages/ssh.js 各一组，全部调用真实页面函数）：
//   1. 下载请求的 path_ids 与 paths 并行、每项来自条目身份而不是展示名；
//   2. 身份 token 绝不被拼上 "/name"、".partial" 或任何子段（断言真实访问目标）；
//   3. 同显示名不同原始字节的两条记录可独立选中（选中键是身份，不是显示名）；
//   4. 地址栏 / 面包屑 / 表格文案永不出现 token，而身份确实发到了 API；
//   5. 从列表派生出的子条目访问目标使用各条目自己的身份
//      （即 pre-fix "下载了另一个物理条目" 的回归）。
//
// 未验证项：真实浏览器布局 / 真实点击 / 真实 SFTP 远端。这里只有 DOM 双与请求双。
// web/pages/ssh.js 的 SFTP 面板同样用注入测试桥驱动真实函数（见文件末尾说明）。

'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

// 默认指向仓库里的真实页面；KAIRO_FILES_JS 只用于变异测试
// （把刻意改坏的副本放到临时目录，验证这份回归真的能抓到缺陷）。
const FILES_PATH = process.env.KAIRO_FILES_JS
  ? path.resolve(process.env.KAIRO_FILES_JS)
  : path.join(__dirname, '..', 'web', 'pages', 'files.js');
const FILES_SRC = fs.readFileSync(FILES_PATH, 'utf8');

const PAGE_ANCHOR = 'Kairo.pages.files = renderFiles;';

// 需要从 renderFiles 闭包里导出的真实函数 / DOM 句柄。
const BRIDGE_EXPORTS = [
  'state',
  'entryPathId',
  'entryDisplayPath',
  'joinCurrentPath',
  'renderTable',
  'renderCrumbs',
  'toggleAllFiles',
  'doListDir',
  'doDownload',
  'doDownloadSingle',
  'openPreview',
  'editFile',
  'doRename',
  'doNewFile',
  'doNewFolder',
  'showContextMenu',
  'handleDownloadEvent',
  'dlKeyOf',
  'userInput',
  'passInput',
  'pathInp',
  'dlZipChk',
  'dlTargetDirInp',
  'tableWrap',
  'crumbsEl',
  'selCount',
  'btnDownload'
];

// ---------------------------------------------------------------------------
// 源码注入：在 renderFiles 闭合括号前导出内部函数
// ---------------------------------------------------------------------------

function buildInstrumentedSource(source) {
  const anchorIdx = source.indexOf(PAGE_ANCHOR);
  assert.ok(anchorIdx > 0, '必须能在 web/pages/files.js 中定位 ' + PAGE_ANCHOR);
  const closeIdx = source.lastIndexOf('}', anchorIdx);
  assert.ok(closeIdx > 0, '必须能定位 renderFiles 的闭合括号（注入测试桥用）');
  const bridge = 'window.__filesBridgeSink({ ' + BRIDGE_EXPORTS.join(', ') + ' });\n  ';
  const out = source.slice(0, closeIdx) + bridge + source.slice(closeIdx);
  assert.notStrictEqual(out, source, '测试桥必须真的被注入');
  return out;
}

const INSTRUMENTED_SRC = buildInstrumentedSource(FILES_SRC);

// ---------------------------------------------------------------------------
// 极简 DOM 双（只实现 files.js 真正用到的接口）
// ---------------------------------------------------------------------------

function unescapeAttrValue(raw) {
  return String(raw).replace(/\\(["\\])/g, '$1');
}

// cssEscape 双：files.js 只用它把 identity token 拼进属性选择器，
// 这里按 CSS 属性值转义规则只处理引号与反斜杠。
function cssEscapeDouble(value) {
  return String(value == null ? '' : value).replace(/(["\\])/g, '\\$1');
}

function escapeHtmlDouble(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

const ATTR_SELECTOR_RE = /\[([\w-]+)="((?:\\.|[^"\\])*)"\]/g;

function parseSimpleSelector(part) {
  const spec = { tag: null, id: null, classes: [], attrs: [] };
  const tagMatch = /^[a-zA-Z][\w-]*/.exec(part);
  if (tagMatch) spec.tag = tagMatch[0].toUpperCase();
  const idMatch = /#([\w-]+)/.exec(part);
  if (idMatch) spec.id = idMatch[1];
  let classMatch;
  const classRe = /\.([\w-]+)/g;
  while ((classMatch = classRe.exec(part)) !== null) spec.classes.push(classMatch[1]);
  ATTR_SELECTOR_RE.lastIndex = 0;
  let attrMatch;
  while ((attrMatch = ATTR_SELECTOR_RE.exec(part)) !== null) {
    spec.attrs.push([attrMatch[1], unescapeAttrValue(attrMatch[2])]);
  }
  return spec;
}

function matchesSimple(node, spec) {
  if (!node || node.nodeType !== 1) return false;
  if (spec.tag && node.tagName !== spec.tag) return false;
  if (spec.id && node.id !== spec.id) return false;
  for (const cls of spec.classes) {
    if (!node.classList || !node.classList.contains(cls)) return false;
  }
  for (const pair of spec.attrs) {
    if (node.getAttribute(pair[0]) !== pair[1]) return false;
  }
  return true;
}

function descendantsOf(root) {
  const out = [];
  (function walk(node) {
    (node.children || []).forEach(child => {
      out.push(child);
      walk(child);
    });
  })(root);
  return out;
}

function querySelectorDouble(root, selector) {
  const parts = String(selector).trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return null;
  const specs = parts.map(parseSimpleSelector);
  const last = specs[specs.length - 1];
  for (const candidate of descendantsOf(root)) {
    if (!matchesSimple(candidate, last)) continue;
    if (specs.length === 1) return candidate;
    let idx = specs.length - 2;
    let current = candidate.parentNode;
    while (idx >= 0 && current && current !== root) {
      if (matchesSimple(current, specs[idx])) idx--;
      current = current.parentNode;
    }
    if (idx < 0) return candidate;
  }
  return null;
}

function querySelectorAllDouble(root, selector) {
  const parts = String(selector).trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return [];
  const specs = parts.map(parseSimpleSelector);
  const last = specs[specs.length - 1];
  const out = [];
  for (const candidate of descendantsOf(root)) {
    if (!matchesSimple(candidate, last)) continue;
    if (specs.length === 1) { out.push(candidate); continue; }
    let idx = specs.length - 2;
    let current = candidate.parentNode;
    while (idx >= 0 && current && current !== root) {
      if (matchesSimple(current, specs[idx])) idx--;
      current = current.parentNode;
    }
    if (idx < 0) out.push(candidate);
  }
  return out;
}

function makeStyleDouble() {
  const props = {};
  return {
    setProperty(name, value) { props[name] = String(value); },
    getPropertyValue(name) { return props[name] || ''; },
    removeProperty(name) { delete props[name]; }
  };
}

function makeNodeDouble(documentRef, tagName) {
  const classes = new Set();
  const attrs = {};
  const listeners = {};
  let children = [];
  let ownText = '';
  let html = '';

  const node = {
    tagName: String(tagName || 'div').toUpperCase(),
    nodeType: 1,
    style: makeStyleDouble(),
    dataset: {},
    parentNode: null,
    parentElement: null,
    ownerDocument: documentRef,
    value: '',
    checked: false,
    disabled: false,
    hidden: false,
    title: '',
    type: '',
    placeholder: '',
    files: null,
    scrollTop: 0,
    className2: '',
    get children() { return children; },
    get childNodes() { return children; },
    get firstChild() { return children[0] || null; },
    get lastChild() { return children[children.length - 1] || null; },
    get firstElementChild() { return children.find(c => c.nodeType === 1) || null; },
    get lastElementChild() {
      for (let i = children.length - 1; i >= 0; i--) {
        if (children[i].nodeType === 1) return children[i];
      }
      return null;
    },
    get nextSibling() {
      if (!node.parentNode) return null;
      const sibs = node.parentNode.children;
      return sibs[sibs.indexOf(node) + 1] || null;
    },
    get previousSibling() {
      if (!node.parentNode) return null;
      const sibs = node.parentNode.children;
      const idx = sibs.indexOf(node);
      return idx > 0 ? sibs[idx - 1] : null;
    },
    get isConnected() { return true; },
    get className() { return Array.from(classes).join(' '); },
    set className(value) {
      classes.clear();
      String(value || '').split(/\s+/).filter(Boolean).forEach(c => classes.add(c));
    },
    get id() { return attrs.id || ''; },
    set id(value) { attrs.id = String(value); },
    get textContent() {
      if (!children.length) return ownText;
      return ownText + children.map(c => c.textContent).join('');
    },
    set textContent(value) {
      ownText = String(value == null ? '' : value);
      html = escapeHtmlDouble(ownText);
      children.forEach(c => { c.parentNode = null; c.parentElement = null; });
      children = [];
    },
    get innerText() { return node.textContent; },
    set innerText(value) { node.textContent = value; },
    get innerHTML() { return html; },
    set innerHTML(value) {
      html = String(value == null ? '' : value);
      children.forEach(c => { c.parentNode = null; c.parentElement = null; });
      children = [];
      ownText = html.replace(/<[^>]*>/g, '');
    },
    classList: {
      add(...names) { names.forEach(n => classes.add(String(n))); },
      remove(...names) { names.forEach(n => classes.delete(String(n))); },
      contains(name) { return classes.has(String(name)); },
      toggle(name, force) {
        const on = force === undefined ? !classes.has(String(name)) : !!force;
        if (on) classes.add(String(name)); else classes.delete(String(name));
        return on;
      }
    },
    setAttribute(name, value) {
      attrs[name] = String(value);
      if (name === 'class') node.className = value;
      if (name === 'id') attrs.id = String(value);
      if (name === 'value') node.value = String(value);
      if (name === 'disabled') node.disabled = true;
      if (name === 'checked') node.checked = true;
    },
    getAttribute(name) {
      return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null;
    },
    getAttributeNames() { return Object.keys(attrs); },
    removeAttribute(name) {
      delete attrs[name];
      if (name === 'disabled') node.disabled = false;
    },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    appendChild(child) {
      if (!child) return child;
      child.parentNode = node;
      child.parentElement = node;
      children.push(child);
      return child;
    },
    append(...items) { items.forEach(item => node.appendChild(item)); },
    insertBefore(child, ref) {
      const idx = ref ? children.indexOf(ref) : -1;
      if (idx < 0) return node.appendChild(child);
      child.parentNode = node;
      child.parentElement = node;
      children.splice(idx, 0, child);
      return child;
    },
    removeChild(child) {
      children = children.filter(c => c !== child);
      if (child) { child.parentNode = null; child.parentElement = null; }
      return child;
    },
    remove() { if (node.parentNode) node.parentNode.removeChild(node); },
    contains(other) {
      if (other === node) return true;
      return children.some(c => c.contains && c.contains(other));
    },
    addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
    removeEventListener(type, fn) {
      if (listeners[type]) listeners[type] = listeners[type].filter(item => item !== fn);
    },
    getListeners(type) { return (listeners[type] || []).slice(); },
    dispatchEvent(ev) { return dispatchOn(node, ev); },
    click() { return dispatchOn(node, { type: 'click', target: node }); },
    querySelector(selector) { return querySelectorDouble(node, selector); },
    querySelectorAll(selector) { return querySelectorAllDouble(node, selector); },
    getBoundingClientRect() { return { width: 200, height: 120, left: 8, top: 8, right: 208, bottom: 128 }; },
    focus() {}, blur() {}, select() {}, setSelectionRange() {}, scrollIntoView() {}
  };
  return node;
}

// 事件派发：把事件交给节点真实注册的监听器（files.js 的 onclick / change 都是真监听器）。
function dispatchOn(node, ev) {
  const event = ev || {};
  if (!event.type) event.type = 'event';
  if (!event.target) event.target = node;
  if (typeof event.preventDefault !== 'function') event.preventDefault = () => { event.defaultPrevented = true; };
  if (typeof event.stopPropagation !== 'function') event.stopPropagation = () => { event.propagationStopped = true; };
  if (typeof event.stopImmediatePropagation !== 'function') event.stopImmediatePropagation = () => { event.propagationStopped = true; };
  node.getListeners(event.type).forEach(fn => {
    if (!event.propagationStopped) fn(event);
  });
  return event;
}

function setCheckedDouble(checkbox, checked) {
  checkbox.checked = checked;
  dispatchOn(checkbox, { type: 'change', target: checkbox });
}

function clickDouble(node, extra) {
  const ev = Object.assign({
    type: 'click', target: node,
    clientX: 8, clientY: 8,
    shiftKey: false, ctrlKey: false, metaKey: false, key: ''
  }, extra || {});
  return dispatchOn(node, ev);
}

// ---------------------------------------------------------------------------
// 页面装载：真实 files.js + DOM 双 + 记录式 api 双
// ---------------------------------------------------------------------------

const DEFAULT_CONFIG = {
  app: { enable_free_file_browser: true, free_file_roots: ['*'] },
  systems: [{
    name: '信贷生产',
    servers: [{
      name: 'mock-1', host: '127.0.0.1', port: 22, username: 'ops', password: 'testpw',
      log_dirs: [{ name: 'SystemOut', path: '/opt/logs', pattern: '*.log', encoding: 'utf-8' }]
    }]
  }]
};

function makePageDoubles() {
  const env = {
    requests: [],
    toasts: [],
    statuses: [],
    listResponse: null,
    sshListResponse: null,
    sshDownloadPayloads: [],
    sshPreviewUrls: [],
    previewResponse: { size: 0, bytes_read: 0, truncated: false, is_binary: false, content: '', encoding: 'utf-8' },
    config: DEFAULT_CONFIG,
    promptAnswer: '',
    openWindowResult: null,
    bridge: null,
    sshBridge: null
  };

  const documentDouble = {
    body: null,
    activeElement: null,
    listeners: {},
    createElement(tag) { return makeNodeDouble(documentDouble, tag); },
    createTextNode(text) {
      const n = makeNodeDouble(documentDouble, 'text');
      n.nodeType = 3;
      n.tagName = '#TEXT';
      n.textContent = String(text);
      return n;
    },
    getElementById() { return null; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    addEventListener(type, fn) { (documentDouble.listeners[type] = documentDouble.listeners[type] || []).push(fn); },
    removeEventListener() {},
    createRange: () => ({ selectNodeContents() {}, collapse() {}, selectNode() {}, setStart() {}, setEnd() {} }),
    execCommand: () => true
  };
  documentDouble.body = makeNodeDouble(documentDouble, 'body');

  const windowDouble = {
    Kairo: null,
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    location: { hash: '' },
    innerWidth: 1366,
    innerHeight: 768,
    isSecureContext: true,
    confirm: () => true,
    prompt: () => env.promptAnswer,
    open: () => env.openWindowResult,
    navigator: { sendBeacon: () => true },
    listeners: {},
    addEventListener(type, fn) { (windowDouble.listeners[type] = windowDouble.listeners[type] || []).push(fn); },
    removeEventListener() {},
    __filesBridgeSink(obj) { env.bridge = obj; },
    __sshBridgeSink(obj) { env.sshBridge = obj; }
  };
  windowDouble.window = windowDouble;

  function recordRequest(method, url, body) {
    // 按"真正上线"的形态记录：JSON 序列化后再断言。
    // 这样跨 vm realm 的数组/对象原型差异不会污染断言，undefined 字段也按真实协议消失。
    let wireBody = body;
    if (body !== undefined) {
      wireBody = JSON.parse(JSON.stringify(body));
    }
    env.requests.push({ method, url, body: wireBody, rawBody: body });
  }

  async function apiDouble(method, url, body) {
    recordRequest(method, url, body);
    if (url === '/api/config') return env.config;
    if (url === '/api/admin/openers') return { openers: [] };
    if (url.indexOf('/api/credentials/has') === 0) return { mode: 'keyring', has: false };
    if (url === '/api/files/list') {
      if (!env.listResponse) throw new Error('测试未提供 /api/files/list 响应');
      return env.listResponse;
    }
    if (url === '/api/files/download') return { id: 'dl-test' };
    if (url === '/api/files/preview') return env.previewResponse;
    if (url === '/api/ssh/sftp/edit') return { id: 'edit-test' };
    if (url === '/api/ssh/sftp/list') {
      if (!env.sshListResponse) throw new Error('测试未提供 /api/ssh/sftp/list 响应');
      return env.sshListResponse;
    }
    return { ok: true };
  }

  const Kairo = {
    core: {
      el(tag, attrs, children) {
        const node = documentDouble.createElement(tag);
        if (attrs) {
          for (const key of Object.keys(attrs)) {
            const value = attrs[key];
            if (value === undefined || value === null || value === false) continue;
            if (key === 'class') node.className = value;
            else if (key === 'text') node.textContent = value;
            else if (key === 'html' || key === 'unsafeHtml') node.innerHTML = value;
            else if (key === 'style') node.style = Object.assign(makeStyleDouble(), { cssText: String(value) });
            else if (key.indexOf('on') === 0 && typeof value === 'function') node.addEventListener(key.slice(2), value);
            else node.setAttribute(key, value === true ? '' : value);
          }
        }
        if (children) {
          (Array.isArray(children) ? children : [children]).forEach(child => {
            if (child === null || child === undefined) return;
            if (typeof child === 'string') node.appendChild(documentDouble.createTextNode(child));
            else node.appendChild(child);
          });
        }
        return node;
      },
      $: () => null,
      toast(message) { env.toasts.push(String(message)); },
      setStatus(state) { env.statuses.push(String(state)); },
      cssEscape: cssEscapeDouble,
      pctText: (written, total) => String(written) + '/' + String(total),
      formatBytes: n => String(n) + 'B',
      formatTime: () => '2026-01-01 00:00:00',
      basenameOf: p => String(p || '').split('/').pop(),
      escapeHtml: escapeHtmlDouble,
      lastGet: () => null,
      lastSet: () => {},
      notify: () => {},
      bumpDlBadge: () => {},
      copyToClipboard: async () => {},
      setActiveDL: () => {},
      clearActiveDL: () => {},
      setActiveUploads: () => {},
      // ssh.js 需要（文件页不用），语义是"注册当前活跃 shell 控制器"。
      setActiveShells: () => {}
    },
    api: {
      api: (method, url, body) => apiDouble(method, url, body),
      // pathRow 只是把输入框包成一行（真实实现返回 DOM 片段），双里原样返回。
      pathRow: node => node
    },
    icons: { openerIconHTML: () => '', innerHTML: () => '', svg: () => '' },
    SftpCommon: {
      isTextFileByExt: () => true,
      formatBytes: n => String(n) + 'B',
      formatMTime: s => String(s),
      ROUTE_SSH_SFTP: 'ssh-sftp',
      // ssh.js 的 SFTP 下载走这里；记录真实负载（按线上 JSON 形态）。
      apiDownloadSshSftp: payload => {
        env.sshDownloadPayloads.push(JSON.parse(JSON.stringify(payload)));
        return Promise.resolve({ id: 'dl-ssh', target_dir: '/tmp/dl' });
      },
      subscribeDownload: () => ({ close() {} }),
      openPreviewWindow: url => { env.sshPreviewUrls.push(String(url)); return {}; }
    },
    pages: {},
    state: { downloadsOpeners: [], routes: {}, routeNames: {} }
  };
  windowDouble.Kairo = Kairo;

  const context = {
    window: windowDouble,
    document: documentDouble,
    Kairo,
    localStorage: windowDouble.localStorage,
    navigator: windowDouble.navigator,
    location: windowDouble.location,
    console,
    setTimeout,
    clearTimeout,
    requestAnimationFrame: fn => setTimeout(fn, 0),
    cancelAnimationFrame: clearTimeout,
    Map, Set, WeakMap, Number, String, Object, Array, Math, JSON, Date, Promise, Error,
    Boolean, RegExp, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    // files.js 直接调用裸 prompt(...)（不是 window.prompt），vm 全局必须有。
    prompt: () => env.promptAnswer,
    alert: () => {},
    confirm: () => true,
    EventSource: undefined
  };

  env.document = documentDouble;
  env.window = windowDouble;
  env.Kairo = Kairo;
  env.context = context;
  env.fire = { click: clickDouble, setChecked: setCheckedDouble, dispatch: dispatchOn };
  env.settle = async (rounds) => {
    for (let i = 0; i < (rounds || 10); i++) await new Promise(resolve => setTimeout(resolve, 1));
  };
  env.lastRequest = (method, url) => {
    for (let i = env.requests.length - 1; i >= 0; i--) {
      const r = env.requests[i];
      if (r.method === method && r.url === url) return r;
    }
    return null;
  };
  env.requestsTo = (method, url) => env.requests.filter(r => r.method === method && r.url === url);
  return env;
}

// 装载真实 files.js 并调用 renderFiles（拿到闭包内部函数）。
function mountFilesPage() {
  const env = makePageDoubles();
  vm.runInNewContext(INSTRUMENTED_SRC, env.context, { filename: 'web/pages/files.js' });
  assert.ok(typeof env.Kairo.pages.files === 'function', 'files.js 必须注册 Kairo.pages.files');
  env.view = env.document.createElement('div');
  env.Kairo.pages.files(env.view, {}, { tabId: 'files' });
  assert.ok(env.bridge, '必须通过测试桥拿到 renderFiles 内部函数（注入失败？）');
  return env;
}

// ---------------------------------------------------------------------------
// 夹具：真实字节的 path_id（kairo-raw:<hex> 就是原始字节路径的十六进制）
// ---------------------------------------------------------------------------

function pathIdFromBytes(bytes) {
  return 'kairo-raw:' + Buffer.from(bytes).toString('hex');
}

function bytesOf(str) {
  return Array.from(Buffer.from(str, 'utf8'));
}

const DISPLAY_DIR = '/tmp/中文';
// 磁盘上真实的 GBK 目录：/tmp/<D6 D0 CE C4>
const GBK_DIR_BYTES = [0x2f, 0x74, 0x6d, 0x70, 0x2f, 0xd6, 0xd0, 0xce, 0xc4];
// GBK 文件名 中文.log
const GBK_NAME_BYTES = [0xd6, 0xd0, 0xce, 0xc4, 0x2e, 0x6c, 0x6f, 0x67];

const DIR_TOKEN = pathIdFromBytes(GBK_DIR_BYTES);
const PARENT_TOKEN = pathIdFromBytes(bytesOf('/tmp'));
// UTF-8 中文.log 落在 UTF-8 目录里
const UTF8_FILE_TOKEN = pathIdFromBytes(bytesOf('/tmp/中文/中文.log'));
// GBK 中文.log 落在 GBK 目录里（展示名与上面完全相同）
const GBK_FILE_TOKEN = pathIdFromBytes(GBK_DIR_BYTES.concat([0x2f]).concat(GBK_NAME_BYTES));

const SAME_DISPLAY_PATH = '/tmp/中文/中文.log';

const ENTRY_UTF8 = {
  name: '中文.log',
  display_path: SAME_DISPLAY_PATH,
  path_id: UTF8_FILE_TOKEN,
  raw_name: '中文.log',
  encoding: 'utf-8',
  size: 4, isDir: false, mode: '-rw-r--r--', mtime: '2026-01-01T00:00:00Z'
};
// raw_name 在 JSON 里只能给合法 UTF-8（GBK 字节会被替换），这正说明展示名/原始名都不是安全主键。
const ENTRY_GBK = {
  name: '中文.log',
  display_path: SAME_DISPLAY_PATH,
  path_id: GBK_FILE_TOKEN,
  raw_name: '中文.log',
  encoding: 'gb18030',
  size: 7, isDir: false, mode: '-rw-r--r--', mtime: '2026-01-01T00:00:00Z'
};
const ENTRY_SUBDIR = {
  name: 'sub',
  display_path: '/tmp/中文/sub',
  path_id: pathIdFromBytes(GBK_DIR_BYTES.concat([0x2f]).concat(bytesOf('sub'))),
  raw_name: 'sub',
  encoding: 'utf-8',
  size: 0, isDir: true, mode: 'drwxr-xr-x', mtime: '2026-01-01T00:00:00Z'
};

function listResponse(entries, displayDir, dirToken) {
  return {
    path: displayDir || DISPLAY_DIR,
    display_path: displayDir || DISPLAY_DIR,
    parent: '/tmp',
    path_id: dirToken || DIR_TOKEN,
    parent_path_id: PARENT_TOKEN,
    entries: entries
  };
}

// 装载页面 + 走完一次真实列目录（列表响应里带身份）。
async function mountListedPage(entries, options) {
  const opts = options || {};
  const env = mountFilesPage();
  await env.settle();
  const bridge = env.bridge;
  bridge.state.currentSys = '信贷生产';
  bridge.state.currentSrv = 'mock-1';
  bridge.userInput.value = 'ops';
  bridge.passInput.value = 'testpw';
  env.listResponse = listResponse(entries, opts.displayDir, opts.dirToken);
  await bridge.doListDir(opts.displayDir || DISPLAY_DIR, { username: 'ops', password: 'testpw' }, opts.pathId);
  env.bridge = bridge;
  return env;
}

function rowsOf(env) {
  return env.bridge.tableWrap.querySelectorAll('tr').filter(tr => tr.getAttribute('data-id') !== null);
}

function rowCheckbox(row) {
  return row.querySelector('input[type="checkbox"]');
}

function tableText(env) {
  return env.bridge.tableWrap.textContent;
}

function contextMenuItem(env, label) {
  const items = env.document.body.querySelectorAll('.file-context-menu-item');
  return items.find(item => item.textContent.indexOf(label) !== -1) || null;
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

const results = [];
function test(name, fn) { results.push({ name, fn }); }

// 1) 列目录请求：展示路径与身份分离，身份原样下发
test('列目录请求同时下发展示 path 与身份 path_id（不拼接）', async () => {
  const env = await mountListedPage([ENTRY_UTF8], { pathId: DIR_TOKEN });
  const listReqs = env.requestsTo('POST', '/api/files/list');
  assert.strictEqual(listReqs.length, 1, '应只发一次列目录请求');
  assert.strictEqual(listReqs[0].body.path, DISPLAY_DIR, 'path 必须是展示路径');
  assert.strictEqual(listReqs[0].body.path_id, DIR_TOKEN, 'path_id 必须原样等于身份 token');
  assert.ok(String(listReqs[0].body.path_id).indexOf('/') === -1, 'token 内不能出现路径分隔符');
  assert.strictEqual(env.bridge.state.currentPath, DISPLAY_DIR, 'currentPath 必须是展示路径');
  assert.strictEqual(env.bridge.state.currentPathId, DIR_TOKEN, 'currentPathId 必须来自响应 path_id');
  assert.strictEqual(env.bridge.state.currentParentPathId, PARENT_TOKEN, '父目录身份必须来自响应');

  // 没有身份的老契约（首次进入 / 手输路径）不能凭空造 token
  const env2 = mountFilesPage();
  await env2.settle();
  env2.bridge.state.currentSys = '信贷生产';
  env2.bridge.state.currentSrv = 'mock-1';
  env2.bridge.userInput.value = 'ops';
  env2.listResponse = listResponse([], '/data', '');
  await env2.bridge.doListDir('/data', { username: 'ops', password: 'testpw' });
  const req2 = env2.lastRequest('POST', '/api/files/list');
  assert.strictEqual(req2.body.path, '/data');
  assert.ok(!('path_id' in req2.body), '无身份时必须留空（线上负载里没有 path_id），交给后端按展示路径解析');
});

// 2) 下载请求：path_ids 与 paths 并行，且每项来自条目身份
test('下载请求的 path_ids 与 paths 并行、每项来自条目身份（不是展示名）', async () => {
  const env = await mountListedPage([ENTRY_UTF8, ENTRY_GBK], { pathId: DIR_TOKEN });
  const bridge = env.bridge;
  bridge.toggleAllFiles(true);
  assert.strictEqual(bridge.state.selected.size, 2, '全选应选中两条记录');

  await bridge.doDownload();
  const req = env.lastRequest('POST', '/api/files/download');
  assert.ok(req, '必须发出下载请求');
  assert.ok(Array.isArray(req.body.paths) && Array.isArray(req.body.path_ids), 'paths / path_ids 都要是数组');
  assert.strictEqual(req.body.path_ids.length, req.body.paths.length, '两个数组必须等长');
  assert.deepStrictEqual(req.body.paths, [SAME_DISPLAY_PATH, SAME_DISPLAY_PATH], 'paths 是展示路径（可重复）');
  assert.deepStrictEqual(req.body.path_ids, [UTF8_FILE_TOKEN, GBK_FILE_TOKEN], 'path_ids 必须按条目顺序取各自身份');
  assert.notStrictEqual(req.body.path_ids[0], req.body.paths[0], '身份不能退化成展示路径');
  req.body.path_ids.forEach(id => {
    assert.ok(id.indexOf('kairo-raw:') === 0, '身份必须是 kairo-raw: token：' + id);
    assert.strictEqual(id.indexOf('中文.log'), -1, '身份里不应出现展示名（说明是按展示名拼的）');
  });
});

// 3) 身份绝不被拼上子段 / .partial
test('身份 token 不会被拼上 "/name"、".partial" 或任何子段', async () => {
  const env = await mountListedPage([ENTRY_UTF8, ENTRY_GBK, ENTRY_SUBDIR], { pathId: DIR_TOKEN });
  const bridge = env.bridge;

  // 单文件下载
  await bridge.doDownloadSingle(SAME_DISPLAY_PATH, '中文.log', GBK_FILE_TOKEN);
  const dl = env.lastRequest('POST', '/api/files/download');
  assert.deepStrictEqual(dl.body.paths, [SAME_DISPLAY_PATH], '单文件下载的 paths 是展示路径');
  assert.deepStrictEqual(dl.body.path_ids, [GBK_FILE_TOKEN], '单文件下载的 path_ids 必须是身份本身');
  assert.strictEqual(dl.body.path_ids[0].indexOf('.partial'), -1, '身份不能带 .partial');

  // 预览
  bridge.state.previewDone = null;
  await bridge.openPreview(SAME_DISPLAY_PATH, '中文.log', GBK_FILE_TOKEN);
  const pv = env.lastRequest('POST', '/api/files/preview');
  assert.strictEqual(pv.body.path, SAME_DISPLAY_PATH, '预览的 path 是展示路径');
  assert.strictEqual(pv.body.path_id, GBK_FILE_TOKEN, '预览的 path_id 必须是身份本身');

  // 编辑（外部打开器）
  await bridge.editFile(SAME_DISPLAY_PATH, '中文.log', 'notepad', GBK_FILE_TOKEN);
  const ed = env.lastRequest('POST', '/api/ssh/sftp/edit');
  assert.strictEqual(ed.body.path, SAME_DISPLAY_PATH);
  assert.strictEqual(ed.body.path_id, GBK_FILE_TOKEN, '编辑的 path_id 必须是身份本身');

  // 重命名：服务端组合 (parent_path_id + new_name)，前端不拼 token
  env.promptAnswer = '改名.log';
  await bridge.doRename('中文.log', SAME_DISPLAY_PATH, false, GBK_FILE_TOKEN, DIR_TOKEN);
  const rn = env.lastRequest('POST', '/api/ssh/sftp/rename');
  assert.strictEqual(rn.body.old_path_id, GBK_FILE_TOKEN, 'old_path_id 必须是身份本身');
  assert.strictEqual(rn.body.new_parent_path_id, DIR_TOKEN, 'new_parent_path_id 必须是目录身份');
  assert.strictEqual(rn.body.new_name, '改名.log', 'new_name 只给名字，服务端自己组合');
  ['old_path_id', 'new_parent_path_id'].forEach(k => {
    assert.strictEqual(String(rn.body[k]).indexOf('/'), -1, k + ' 不能含路径分隔符（说明被拼过）');
  });
  assert.strictEqual(rn.body.new_path, DISPLAY_DIR + '/改名.log', 'legacy new_path 仍是展示路径');

  // 新建文件 / 新建文件夹：父目录身份 + name
  env.promptAnswer = 'new.txt';
  await bridge.doNewFile();
  const cf = env.lastRequest('POST', '/api/ssh/sftp/create');
  assert.strictEqual(cf.body.parent_path_id, DIR_TOKEN, 'create 必须带父目录身份');
  assert.strictEqual(cf.body.name, 'new.txt', 'create 只给名字');
  assert.strictEqual(cf.body.path, DISPLAY_DIR + '/new.txt', 'legacy path 仍是展示路径');
  assert.strictEqual(String(cf.body.parent_path_id).indexOf('/'), -1, '父目录身份不能被拼接');

  env.promptAnswer = 'newdir';
  await bridge.doNewFolder();
  const mk = env.lastRequest('POST', '/api/ssh/sftp/mkdir');
  assert.strictEqual(mk.body.parent_path_id, DIR_TOKEN);
  assert.strictEqual(mk.body.name, 'newdir');
  assert.strictEqual(mk.body.path, DISPLAY_DIR + '/newdir', 'legacy path 仍是展示路径');
});

// 4) 同显示名不同原始字节：可独立选中
test('同显示名不同原始字节的两条记录可独立选中（选中键是身份）', async () => {
  const env = await mountListedPage([ENTRY_UTF8, ENTRY_GBK], { pathId: DIR_TOKEN });
  const bridge = env.bridge;
  const rows = rowsOf(env);
  assert.strictEqual(rows.length, 2, '同显示名的两条记录都要渲染出来');
  assert.deepStrictEqual(rows.map(r => r.getAttribute('data-name')), ['中文.log', '中文.log']);
  assert.deepStrictEqual(rows.map(r => r.getAttribute('data-path')), [SAME_DISPLAY_PATH, SAME_DISPLAY_PATH]);
  assert.deepStrictEqual(rows.map(r => r.getAttribute('data-id')), [UTF8_FILE_TOKEN, GBK_FILE_TOKEN],
    '每行的资源键必须是各自身份');

  const boxes = rows.map(rowCheckbox);
  setCheckedDouble(boxes[0], true);
  assert.strictEqual(bridge.state.selected.size, 1, '只选中一条');
  assert.ok(bridge.state.selected.has(UTF8_FILE_TOKEN), '选中的是 UTF-8 条目的身份');
  assert.ok(!bridge.state.selected.has(GBK_FILE_TOKEN), '不能连带选中 GBK 条目');
  assert.ok(!bridge.state.selected.has('中文.log'), '选中键绝不能是显示名');

  setCheckedDouble(boxes[1], true);
  assert.strictEqual(bridge.state.selected.size, 2, '两条可以同时选中');
  assert.ok(bridge.state.selected.has(GBK_FILE_TOKEN));

  setCheckedDouble(boxes[0], false);
  assert.strictEqual(bridge.state.selected.size, 1, '取消一条不影响另一条');
  assert.ok(bridge.state.selected.has(GBK_FILE_TOKEN), '剩下的必须是 GBK 条目身份');
  assert.ok(!bridge.state.selected.has(UTF8_FILE_TOKEN));

  // 重渲染后勾选状态按身份恢复（pre-fix 按显示名会让两行一起被勾上）
  bridge.renderTable();
  const boxesAfter = rowsOf(env).map(rowCheckbox);
  assert.strictEqual(boxesAfter[0].checked, false, 'UTF-8 行重渲染后仍未选中');
  assert.strictEqual(boxesAfter[1].checked, true, 'GBK 行重渲染后保持选中');
});

// 5) 展示层永不出现 token；身份确实发到了 API
test('地址栏 / 面包屑 / 表格文案不出现 token，而身份确实发到了 API', async () => {
  const env = await mountListedPage([ENTRY_UTF8, ENTRY_GBK, ENTRY_SUBDIR], { pathId: DIR_TOKEN });
  const bridge = env.bridge;

  assert.strictEqual(bridge.pathInp.value, DISPLAY_DIR, '地址栏必须是展示路径');
  assert.strictEqual(bridge.pathInp.value.indexOf('kairo-raw:'), -1, '地址栏不能出现 token');
  const crumbsText = bridge.crumbsEl.textContent;
  assert.ok(crumbsText.indexOf('中文') !== -1, '面包屑应显示展示名');
  assert.strictEqual(crumbsText.indexOf('kairo-raw:'), -1, '面包屑不能出现 token');
  const table = tableText(env);
  assert.ok(table.indexOf('中文.log') !== -1, '表格应显示展示名');
  assert.strictEqual(table.indexOf('kairo-raw:'), -1, '表格文案不能出现 token');
  assert.strictEqual(bridge.state.currentPath.indexOf('kairo-raw:'), -1, 'currentPath 不能是 token');
  assert.strictEqual(bridge.selCount.textContent, '已选 0 个');

  // token 只存在于属性（资源键），不存在于文案
  // 注意：表格按名称排序渲染（'sub' 会排在 '中文.log' 前面），所以按身份查行，不假设行序。
  const rows = rowsOf(env);
  rows.forEach(row => {
    assert.strictEqual(row.getAttribute('data-path').indexOf('kairo-raw:'), -1, 'data-path 是展示路径');
    assert.ok(row.getAttribute('data-path').indexOf('中文') !== -1 || row.getAttribute('data-path').indexOf('sub') !== -1,
      'data-path 应是人类可读展示路径');
  });
  const byId = new Map(rows.map(row => [row.getAttribute('data-id'), row]));
  assert.ok(byId.has(UTF8_FILE_TOKEN) && byId.has(GBK_FILE_TOKEN) && byId.has(ENTRY_SUBDIR.path_id),
    '三条记录都必须以身份作为行键');
  assert.strictEqual(byId.get(UTF8_FILE_TOKEN).getAttribute('data-path'), SAME_DISPLAY_PATH);

  // 同一时刻，API 请求里带的确实是身份
  bridge.toggleAllFiles(true);
  await bridge.doDownload();
  const req = env.lastRequest('POST', '/api/files/download');
  assert.deepStrictEqual(req.body.path_ids, [UTF8_FILE_TOKEN, GBK_FILE_TOKEN, ENTRY_SUBDIR.path_id],
    '发到 API 的 path_ids 是各条目身份');
});

// 6) 从列表派生的子条目访问目标用各自的身份（pre-fix 下会拿错物理条目）
test('子条目导航 / 下载 / 右键菜单都使用该条目自己的身份', async () => {
  // 6a) 点目录行 → 用子目录身份列目录
  const envA = await mountListedPage([ENTRY_SUBDIR], { pathId: DIR_TOKEN });
  envA.listResponse = listResponse([], ENTRY_SUBDIR.display_path, ENTRY_SUBDIR.path_id);
  const link = rowsOf(envA)[0].querySelector('a');
  assert.ok(link, '目录行必须有可点的名称链接');
  clickDouble(link);
  await envA.settle(12);
  const navReq = envA.lastRequest('POST', '/api/files/list');
  assert.strictEqual(navReq.body.path, ENTRY_SUBDIR.display_path, '导航 path 是子目录展示路径');
  assert.strictEqual(navReq.body.path_id, ENTRY_SUBDIR.path_id, '导航 path_id 必须是子目录自己的身份');
  assert.strictEqual(String(navReq.body.path_id).indexOf('sub'), -1, '身份里不能拼上子目录名');
  assert.strictEqual(envA.bridge.state.currentPath, ENTRY_SUBDIR.display_path);

  // 6b) 同显示名场景下，选中的是 GBK 条目 → 下载的必须是 GBK 条目身份
  const envB = await mountListedPage([ENTRY_UTF8, ENTRY_GBK], { pathId: DIR_TOKEN });
  const boxes = rowsOf(envB).map(rowCheckbox);
  setCheckedDouble(boxes[1], true);
  await envB.bridge.doDownload();
  const dl = envB.lastRequest('POST', '/api/files/download');
  assert.deepStrictEqual(dl.body.path_ids, [GBK_FILE_TOKEN],
    '只选 GBK 条目时必须下载 GBK 身份（pre-fix 会拿到另一个物理条目）');
  assert.notStrictEqual(dl.body.path_ids[0], UTF8_FILE_TOKEN, '不能落到 UTF-8 兄弟条目');
  assert.strictEqual(dl.body.paths[0], SAME_DISPLAY_PATH, '展示路径两侧相同是预期现象');

  // 6c) 右键菜单「下载」「重命名」走身份
  const envC = await mountListedPage([ENTRY_UTF8, ENTRY_GBK], { pathId: DIR_TOKEN });
  envC.bridge.showContextMenu({ clientX: 8, clientY: 8 }, ENTRY_GBK, SAME_DISPLAY_PATH);
  const dlItem = contextMenuItem(envC, '下载');
  assert.ok(dlItem, '右键菜单必须有「下载」项');
  clickDouble(dlItem);
  await envC.settle(6);
  const ctxDl = envC.lastRequest('POST', '/api/files/download');
  assert.deepStrictEqual(ctxDl.body.path_ids, [GBK_FILE_TOKEN], '右键下载必须用该条目身份');

  envC.bridge.state.dlId = null; // 模拟上一个任务已结束
  envC.promptAnswer = '菜单改名.log';
  envC.bridge.showContextMenu({ clientX: 8, clientY: 8 }, ENTRY_GBK, SAME_DISPLAY_PATH);
  const rnItem = contextMenuItem(envC, '重命名');
  assert.ok(rnItem, '右键菜单必须有「重命名」项');
  clickDouble(rnItem);
  await envC.settle(6);
  const ctxRn = envC.lastRequest('POST', '/api/ssh/sftp/rename');
  assert.strictEqual(ctxRn.body.old_path_id, GBK_FILE_TOKEN, '右键重命名必须用该条目身份');
  assert.strictEqual(ctxRn.body.new_parent_path_id, DIR_TOKEN, '右键重命名必须用目录身份');
  assert.strictEqual(ctxRn.body.new_name, '菜单改名.log');
});

// 7) 兜底：老后端（不发 path_id）时退回展示路径，但绝不自造 token
test('条目没有 path_id 时退回展示路径，不会伪造 kairo-raw token', async () => {
  const legacyEntry = {
    name: 'app.log', display_path: '/data/app.log',
    raw_name: 'app.log', encoding: 'utf-8',
    size: 1, isDir: false, mode: '-rw-r--r--', mtime: '2026-01-01T00:00:00Z'
  };
  const env = await mountListedPage([legacyEntry], { displayDir: '/data', dirToken: '' });
  const bridge = env.bridge;
  assert.strictEqual(bridge.entryPathId(legacyEntry, '/data/app.log'), '/data/app.log');
  bridge.toggleAllFiles(true);
  await bridge.doDownload();
  const req = env.lastRequest('POST', '/api/files/download');
  assert.deepStrictEqual(req.body.paths, ['/data/app.log']);
  assert.deepStrictEqual(req.body.path_ids, ['/data/app.log'], '无身份时回退展示路径（后端按展示名解析）');
  assert.strictEqual(req.body.path_ids[0].indexOf('kairo-raw:'), -1, '不能伪造身份 token');
});

// ---------------------------------------------------------------------------
// web/pages/ssh.js：SFTP 面板（真实函数，同样用测试桥导出闭包内部）
// ---------------------------------------------------------------------------

const SSH_PATH = process.env.KAIRO_SSH_JS
  ? path.resolve(process.env.KAIRO_SSH_JS)
  : path.join(__dirname, '..', 'web', 'pages', 'ssh.js');
const SSH_SRC = fs.readFileSync(SSH_PATH, 'utf8');

const SSH_BRIDGE_EXPORTS = [
  'pageState',
  'sftpList',
  'sftpDownloadPaths',
  'sftpDownloadOne',
  'sftpDownloadSelected',
  'sftpEntryPathId',
  'sftpEntryDisplayPath',
  'sftpPathIdToken',
  'sftpResetEntryIndex',
  'sftpPreview'
];

function buildInstrumentedSshSource(source) {
  const anchor = 'function escapeTermText(';
  const anchorIdx = source.indexOf(anchor);
  assert.ok(anchorIdx > 0, '必须能在 web/pages/ssh.js 中定位 ' + anchor);
  assert.strictEqual(source.indexOf(anchor, anchorIdx + 1), -1, 'ssh.js 注入锚点必须唯一');
  // renderSSH 的闭合括号 = 该锚点之前最近的一个 '}'（escapeTermText 是 renderSSH 之后的函数）。
  const closeIdx = source.lastIndexOf('}', anchorIdx);
  assert.ok(closeIdx > 0, '必须能定位 renderSSH 的闭合括号（注入测试桥用）');
  const bridge = 'window.__sshBridgeSink({ ' + SSH_BRIDGE_EXPORTS.join(', ') + ' });\n  ';
  const out = source.slice(0, closeIdx) + bridge + source.slice(closeIdx);
  assert.notStrictEqual(out, source, 'ssh.js 测试桥必须真的被注入');
  return out;
}

const SSH_INSTRUMENTED_SRC = buildInstrumentedSshSource(SSH_SRC);

function mountSshPage() {
  const env = makePageDoubles();
  vm.runInNewContext(SSH_INSTRUMENTED_SRC, env.context, { filename: 'web/pages/ssh.js' });
  const render = (env.Kairo.state.routes || {}).ssh;
  assert.strictEqual(typeof render, 'function', 'ssh.js 必须注册 Kairo.state.routes.ssh');
  env.view = env.document.createElement('div');
  render(env.view, {}, { tabId: 'ssh' });
  assert.ok(env.sshBridge, '必须通过测试桥拿到 renderSSH 内部函数（注入失败？）');
  return env;
}

// 造一个真实形状的 SFTP tab（字段与 ssh.js openTab 里的 tab 初始化保持一致；
// 这里直接放到 pageState.tabs，避免驱动 WebSocket / xterm 连接流程）。
function makeSshTab(env) {
  const tab = {
    id: 'ssh-tab-1',
    system: '信贷生产',
    server: 'mock-1',
    encoding: 'utf-8',
    sftpCwd: DISPLAY_DIR,
    sftpCurrentPathId: '',
    sftpParentPathId: '',
    sftpDisplayById: new Map(),
    sftpEntryById: new Map(),
    sftpSelected: new Set(),
    sftpEntries: [],
    sftpBackend: '',
    sftpLoading: false,
    sftpDlEvtSrc: null,
    sftpDlId: null,
    sftpDlProgress: null,
    sftpPanelInited: false,
    sftpLastDlNotifyId: null,
    sftpUploadQueue: [],
    sftpUploadInflight: false,
    sftpUploadOverwrite: 'reject',
    sftpUploadRefresh: true,
    closed: false
  };
  env.sshBridge.pageState.tabs.push(tab);
  env.sshBridge.pageState.activeTabId = tab.id;
  return tab;
}

async function sshListTab(env, tab, entries, display, pathId) {
  env.sshListResponse = {
    path: display,
    display_path: display,
    parent: '/tmp',
    path_id: pathId || '',
    parent_path_id: PARENT_TOKEN,
    entries: entries,
    backend: 'sftp',
    elapsed_ms: 1
  };
  env.sshBridge.sftpList(tab, display, pathId);
  await env.settle(12);
}

function sshRows(env) {
  return env.view.querySelectorAll('tr.sftp-row');
}

// 行上不渲染身份（只有 data-name = 展示名），所以用该行真实「下载」按钮的负载反查其身份。
async function sshRowIdentity(env, tab, row) {
  const button = row.querySelectorAll('button').find(b => b.textContent === '下载');
  assert.ok(button, '文件行必须有「下载」按钮');
  tab.sftpDlId = null;
  tab.sftpDlStarting = false;
  clickDouble(button);
  await env.settle(6);
  const payload = env.sshDownloadPayloads[env.sshDownloadPayloads.length - 1];
  assert.ok(payload, '点击「下载」必须发出下载负载');
  return payload.path_ids[0];
}

// S1) ssh 列目录：展示 path 与身份 path_id 分离；非 token 不下发 path_id
test('ssh 列目录：展示 path 与身份 path_id 分离，非 token 不下发', async () => {
  const env = mountSshPage();
  const tab = makeSshTab(env);
  await sshListTab(env, tab, [ENTRY_UTF8, ENTRY_GBK], DISPLAY_DIR, DIR_TOKEN);

  const req = env.lastRequest('POST', '/api/ssh/sftp/list');
  assert.ok(req, '必须发出列目录请求');
  assert.strictEqual(req.body.path, DISPLAY_DIR, 'path 是展示路径');
  assert.strictEqual(req.body.path_id, DIR_TOKEN, 'path_id 必须是目录身份');
  assert.strictEqual(req.body.display_path, DISPLAY_DIR, '带身份时 display_path 只作为展示/审计');
  assert.strictEqual(tab.sftpCwd, DISPLAY_DIR, 'sftpCwd 是展示路径');
  assert.strictEqual(tab.sftpCurrentPathId, DIR_TOKEN, '当前目录身份来自响应');
  assert.strictEqual(tab.sftpParentPathId, PARENT_TOKEN, '父目录身份来自响应');
  assert.strictEqual(tab.sftpDisplayById.size, 2, '每个条目都要登记身份 → 展示路径');
  assert.ok(!tab.sftpDisplayById.has('中文.log'), '身份索引的键不能是展示名');

  // 非 token（老后端 / 手输路径）不能当身份下发
  await sshListTab(env, tab, [ENTRY_UTF8], DISPLAY_DIR, DISPLAY_DIR);
  const req2 = env.lastRequest('POST', '/api/ssh/sftp/list');
  assert.strictEqual(req2.body.path, DISPLAY_DIR);
  assert.ok(!('path_id' in req2.body), '非 token 的 pathId 必须被门控掉，回退旧 path 契约');
});

// S2) ssh 面板：同显示名两条记录可独立选中（选中键是身份）
test('ssh 面板：同显示名两条记录可独立选中（选中键是身份）', async () => {
  const env = mountSshPage();
  const tab = makeSshTab(env);
  await sshListTab(env, tab, [ENTRY_UTF8, ENTRY_GBK], DISPLAY_DIR, DIR_TOKEN);

  const rows = sshRows(env);
  assert.strictEqual(rows.length, 2, '两条同显示名记录都要渲染');
  assert.deepStrictEqual(rows.map(r => r.getAttribute('data-name')), ['中文.log', '中文.log']);

  const idOfRow = [];
  idOfRow.push(await sshRowIdentity(env, tab, rows[0]));
  idOfRow.push(await sshRowIdentity(env, tab, rows[1]));
  assert.deepStrictEqual(new Set(idOfRow), new Set([UTF8_FILE_TOKEN, GBK_FILE_TOKEN]),
    '两行必须分别映射到两个不同的物理条目身份');
  assert.notStrictEqual(idOfRow[0], idOfRow[1], '同显示名也必须能区分');

  tab.sftpSelected = new Set();
  clickDouble(rows[0]);
  assert.strictEqual(tab.sftpSelected.size, 1, '单击只选中一行');
  assert.ok(tab.sftpSelected.has(idOfRow[0]), '选中的是这一行自己的身份');
  assert.ok(!tab.sftpSelected.has(idOfRow[1]), '不能连带选中同显示名的另一行');
  assert.ok(!tab.sftpSelected.has('中文.log'), '选中键不能是显示名');

  clickDouble(rows[1]);
  assert.strictEqual(tab.sftpSelected.size, 2, '两行可同时选中');
  clickDouble(rows[0]);
  assert.strictEqual(tab.sftpSelected.size, 1, '再点一次只取消自己');
  assert.ok(tab.sftpSelected.has(idOfRow[1]), '剩下的是另一行的身份');
});

// S3) ssh 下载：path_ids 与 paths 平行、等于各条目身份、无拼接
test('ssh 下载：path_ids 与 paths 平行且等于各条目身份（无 token 拼接）', async () => {
  const env = mountSshPage();
  const tab = makeSshTab(env);
  await sshListTab(env, tab, [ENTRY_UTF8, ENTRY_GBK], DISPLAY_DIR, DIR_TOKEN);
  const rows = sshRows(env);
  const idOfRow = [];
  idOfRow.push(await sshRowIdentity(env, tab, rows[0]));
  idOfRow.push(await sshRowIdentity(env, tab, rows[1]));
  const gbkRowIndex = idOfRow.indexOf(GBK_FILE_TOKEN);
  assert.ok(gbkRowIndex >= 0, '夹具里必须有一行是 GBK 条目');

  // 3a) 只选中 GBK 行后走"下载选中"路径
  tab.sftpSelected = new Set([GBK_FILE_TOKEN]);
  tab.sftpDlId = null;
  tab.sftpDlStarting = false;
  env.sshBridge.sftpDownloadSelected(tab);
  await env.settle(8);
  const payload = env.sshDownloadPayloads[env.sshDownloadPayloads.length - 1];
  assert.ok(Array.isArray(payload.paths) && Array.isArray(payload.path_ids), 'paths / path_ids 都要是数组');
  assert.strictEqual(payload.path_ids.length, payload.paths.length, '两个数组必须等长');
  assert.deepStrictEqual(payload.paths, [SAME_DISPLAY_PATH], 'paths 是展示路径');
  assert.deepStrictEqual(payload.path_ids, [GBK_FILE_TOKEN], 'path_ids 必须是该条目身份');
  payload.path_ids.forEach(id => {
    assert.ok(id.indexOf('kairo-raw:') === 0, 'path_ids 必须是身份 token：' + id);
    assert.strictEqual(id.indexOf('/'), -1, '身份不能被拼上子段');
    assert.strictEqual(id.indexOf('.partial'), -1, '身份不能带 .partial');
    assert.strictEqual(id.indexOf('中文'), -1, '身份不能含展示名（说明是按展示名拼的）');
  });

  // 3b) 行内「下载」按钮（单文件路径）
  const gbkRow = rows[gbkRowIndex];
  const dlButton = gbkRow.querySelectorAll('button').find(b => b.textContent === '下载');
  tab.sftpDlId = null;
  tab.sftpDlStarting = false;
  clickDouble(dlButton);
  await env.settle(8);
  const single = env.sshDownloadPayloads[env.sshDownloadPayloads.length - 1];
  assert.deepStrictEqual(single.paths, [SAME_DISPLAY_PATH]);
  assert.deepStrictEqual(single.path_ids, [GBK_FILE_TOKEN], '行内下载必须用该行身份');
  assert.notStrictEqual(single.path_ids[0], UTF8_FILE_TOKEN, '不能落到同显示名的兄弟条目');
});

// S4) ssh 身份门控 + 预览 URL
test('ssh 身份门控：只有 kairo-raw token 才下发，预览 URL 带身份', async () => {
  const env = mountSshPage();
  const tab = makeSshTab(env);
  await sshListTab(env, tab, [ENTRY_GBK], DISPLAY_DIR, DIR_TOKEN);

  const gate = env.sshBridge.sftpPathIdToken;
  assert.strictEqual(gate(GBK_FILE_TOKEN), GBK_FILE_TOKEN, 'token 原样下发');
  assert.strictEqual(gate(SAME_DISPLAY_PATH), '', '展示路径必须被门控成空串');
  assert.strictEqual(gate(''), '', '空串被门控');
  assert.strictEqual(gate(null), '', 'null 被门控');
  assert.strictEqual(gate(123), '', '非字符串被门控');

  // entryPathId 的兜底语义：有身份用身份，没有才退回展示路径（不伪造 token）
  assert.strictEqual(env.sshBridge.sftpEntryPathId(ENTRY_GBK, SAME_DISPLAY_PATH), GBK_FILE_TOKEN);
  assert.strictEqual(env.sshBridge.sftpEntryPathId({ name: 'app.log' }, '/data/app.log'), '/data/app.log');

  env.sshBridge.sftpPreview(tab, SAME_DISPLAY_PATH, GBK_FILE_TOKEN);
  const url = env.sshPreviewUrls[env.sshPreviewUrls.length - 1];
  assert.ok(url.indexOf('path_id=' + encodeURIComponent(GBK_FILE_TOKEN)) !== -1,
    '预览 URL 必须带上条目身份（preview.html 目前尚未消费，见文件末尾说明）');
  assert.ok(url.indexOf('path=' + encodeURIComponent(SAME_DISPLAY_PATH)) !== -1, '预览 URL 仍带展示路径');

  env.sshBridge.sftpPreview(tab, SAME_DISPLAY_PATH, SAME_DISPLAY_PATH);
  const url2 = env.sshPreviewUrls[env.sshPreviewUrls.length - 1];
  assert.strictEqual(url2.indexOf('path_id='), -1, '非 token 的 pathId 不能当身份塞进 URL');
});

// ---------------------------------------------------------------------------
// web/pages/ssh.js：SFTP 面板（真实函数，见上面的 mountSshPage / S1-S4 用例）
//
// 环境限制（未验证项）：
//   - 没有真实浏览器：DOM / 事件 / 布局都是双，真实点击、拖拽、滚动、xterm 渲染未验证；
//   - 没有真实 SFTP 远端：断言的是"前端发出的负载"，不是远端落盘的字节；
//   - /api/ssh/sftp/edit（handlers_edit.go）与 /api/ssh/sftp/upload/init 仍未消费 path_id，
//     所以 ssh 面板的"编辑""上传"目标仍是展示路径，这是已知的后续工作。
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
      if (error && error.stack) console.error(String(error.stack).split('\n').slice(0, 5).join('\n'));
    }
  }
  console.log('files-path-identity: ' + passed + ' / ' + results.length + ' cases passed');
  if (failures.length) {
    console.error('失败用例：');
    failures.forEach(f => console.error('  - ' + f.name));
    process.exitCode = 1;
  }
})().catch(error => {
  console.error('测试运行器异常:', error);
  process.exit(1);
});
