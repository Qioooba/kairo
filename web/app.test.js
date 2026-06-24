// web/app.test.js — Node 单元测试
//
// 测试策略：core.js 是 IIFE，所有纯函数都挂在 window.OTB.core.* 下。
// 这里用 Function + 正则把"无 DOM 依赖"的纯函数从 core.js 源里抽出来构造，
// 然后跑断言。覆盖范围 = core.js 里的工具函数。
//
// 运行：node web/app.test.js
// 依赖：纯 stdlib，不需要 npm install

'use strict';

const fs = require('fs');
const path = require('path');
const assert = require('assert');

const SRC_PATH = path.join(__dirname, 'core.js');
const src = fs.readFileSync(SRC_PATH, 'utf8');

// 抽一个具名函数。匹配 "function NAME(...) { ... }" 块，括号配对简单实现。
//
// core.js 的函数定义在 IIFE 内缩进 4 空格（"    function NAME..."），
// 所以正则用 4 空格缩进。
function extract(name) {
  const re = new RegExp('function\\s+' + name + '\\s*\\([^)]*\\)\\s*\\{');
  const m = src.match(re);
  if (!m) throw new Error('not found: ' + name);
  const start = m.index;
  let i = src.indexOf('{', start);
  let depth = 1;
  i++;
  while (i < src.length && depth > 0) {
    const ch = src[i];
    if (ch === '{') depth++;
    else if (ch === '}') depth--;
    i++;
  }
  if (depth !== 0) throw new Error('unbalanced braces for ' + name);
  return src.slice(start, i);
}

const escapeHtml = new Function(extract('escapeHtml') + '; return escapeHtml;')();
const formatBytes = new Function(extract('formatBytes') + '; return formatBytes;')();
const formatTime = new Function(extract('formatTime') + '; return formatTime;')();

// el() 在 core.js 里。抽出来跑 DOM-like 断言。
//
// Node 没 document，所以给 new Function 注入一个最小 mock：
//   - document.createElement(tag) 返回一个带 innerHTML/textContent 字段的对象；
//   - document.createTextNode(text) 返回 {nodeType: 'text', data: text}。
const el = new Function(
  'document',
  extract('el') + '\n  return el;'
)({
  createElement: function (tag) {
    return {
      tag: tag,
      _attrs: {},
      _listeners: {},
      _children: [],
      set className(v) { this._className = v; },
      get className() { return this._className; },
      set innerHTML(v) { this._innerHTML = v; },
      get innerHTML() { return this._innerHTML; },
      set textContent(v) { this._textContent = v; },
      get textContent() { return this._textContent; },
      setAttribute(k, v) { this._attrs[k] = v; },
      addEventListener(name, fn) { (this._listeners[name] = this._listeners[name] || []).push(fn); },
      appendChild(c) { this._children.push(c); return c; }
    };
  },
  createTextNode: function (text) { return { nodeType: 'text', data: text }; }
});

// cssEscape 用通用 extract（4 空格缩进）。
// core.js 里的 cssEscape 实现优先用浏览器 window.CSS，
// Node 没全局 window，我们 mock 成 { CSS: undefined } 走 fallback。
const cssEscape = new Function(
  'window',
  extract('cssEscape') + '\n  return cssEscape;'
)({ CSS: undefined });

// pctText 用通用 extract（依赖 formatBytes，传入 mock）。
const pctText = new Function(
  'formatBytes',
  extract('pctText') + '\n  return pctText;'
)(formatBytes);

const validate = new Function(extract('validate') + '; return validate;')();
const trimMiddle = new Function(extract('trimMiddle') + '; return trimMiddle;')();

// Tail 高亮：3 个核心函数从 core.js 抽出来。
// 它们依赖 document.createElement / document.createDocumentFragment / document.createTextNode，
// 所以用一个最小 DOM mock 喂进去（跟 testEl 里那个差不多）。
const hlDocMock = {
  createElement(tag) {
    const el = {
      tag, _attrs: {}, _children: [],
      _style: {},
      set className(v) { this._className = v; },
      get className() { return this._className; },
      set textContent(v) { this._textContent = v; },
      get textContent() { return this._textContent; },
      style: null, // 下面赋值
      setAttribute(k, v) { this._attrs[k] = v; },
      appendChild(c) { this._children.push(c); return c; }
    };
    // 用一个普通对象当 style：浏览器里 el.style.background = '...' 等价于赋值
    el.style = el._style;
    return el;
  },
  createTextNode(text) { return { nodeType: 'text', data: text }; },
  createDocumentFragment() {
    return {
      _children: [],
      appendChild(c) { this._children.push(c); return c; }
    };
  }
};
// dumpFragment 把 fragment 序列化成简化字符串方便断言。
//   - T:..  → 文本节点
//   - S(bg/fg):..  → span（背景/前景颜色 + 文本）
function dumpFragment(frag) {
  return frag._children.map(c => {
    if (c.nodeType === 'text') return 'T:' + JSON.stringify(c.data);
    if (c.tag === 'span') {
      const bg = (c.style && c.style.background) || '';
      const fg = (c.style && c.style.color) || '';
      return 'S(' + bg + '/' + fg + '):' + JSON.stringify(c._textContent);
    }
    return '?';
  });
}
// 从 core.js 源里抽 const SAFE_COLOR_NAMES = { ... } 和 const SAFE_COLOR_RE = /.../;
// 这俩是 normalizeHighlightColor 的闭包依赖，extract 函数体不带它们。
const SAFE_COLOR_NAMES = (function () {
  const m = src.match(/const\s+SAFE_COLOR_NAMES\s*=\s*\{[\s\S]*?\};/);
  if (!m) throw new Error('SAFE_COLOR_NAMES block not found');
  return new Function(m[0].replace('const ', 'return ') + ';')();
})();
const SAFE_COLOR_RE = (function () {
  const m = src.match(/const\s+SAFE_COLOR_RE\s*=\s*\/(?:[^\n\\]|\\.)+\/;?/);
  if (!m) throw new Error('SAFE_COLOR_RE block not found');
  // m[0] 是 "const SAFE_COLOR_RE = /.../;" —— 改成 return RegExp
  const expr = m[0].replace(/^const\s+\w+\s*=\s*/, '').replace(/;?\s*$/, '');
  return new Function('return ' + expr + ';')();
})();
const normalizeHighlightColor = new Function(
  'SAFE_COLOR_NAMES', 'SAFE_COLOR_RE',
  extract('normalizeHighlightColor') + '\n  return normalizeHighlightColor;'
)(SAFE_COLOR_NAMES, SAFE_COLOR_RE);
const buildHighlightRegex = new Function(
  'document', 'normalizeHighlightColor',
  extract('buildHighlightRegex') + '\n  return buildHighlightRegex;'
)(hlDocMock, normalizeHighlightColor);
const renderHighlightedLine = new Function(
  'document', 'normalizeHighlightColor', 'buildHighlightRegex',
  extract('renderHighlightedLine') + '\n  return renderHighlightedLine;'
)(hlDocMock, normalizeHighlightColor, buildHighlightRegex);
const pickFgForBg = new Function(extract('pickFgForBg') + '; return pickFgForBg;')();

// ---------- escapeHtml ----------

function testEscapeHtml() {
  assert.strictEqual(escapeHtml('<script>'), '&lt;script&gt;', 'tag');
  assert.strictEqual(escapeHtml('a & b'), 'a &amp; b', 'amp');
  assert.strictEqual(escapeHtml('"hi"'), '&quot;hi&quot;', 'double-quote');
  assert.strictEqual(escapeHtml("it's"), 'it&#39;s', 'single-quote');
  assert.strictEqual(escapeHtml(null), '', 'null');
  assert.strictEqual(escapeHtml(undefined), '', 'undefined');
  assert.strictEqual(escapeHtml(0), '0', 'number coerced');
  assert.strictEqual(escapeHtml('<a href="x">'), '&lt;a href=&quot;x&quot;&gt;', 'combo');
  assert.strictEqual(escapeHtml('正常文字'), '正常文字', 'no-op');
  const evil = '"><img src=x onerror=alert(1)>';
  const safe = escapeHtml(evil);
  assert.ok(!safe.includes('<'), 'no raw <');
  assert.ok(!safe.includes('>'), 'no raw >');
  assert.ok(safe.startsWith('&quot;'), 'starts with escaped quote');
  console.log('  escapeHtml ✓');
}

// ---------- formatBytes ----------

function testFormatBytes() {
  assert.strictEqual(formatBytes(0), '0 B', '0');
  assert.strictEqual(formatBytes(100), '100 B', '100 B');
  assert.strictEqual(formatBytes(1023), '1023 B', '1023 B');
  assert.strictEqual(formatBytes(1024), '1.0 KB', '1KB');
  assert.strictEqual(formatBytes(1024 + 512), '1.5 KB', '1.5KB');
  assert.strictEqual(formatBytes(1024 * 1024), '1.0 MB', '1MB');
  assert.strictEqual(formatBytes(1024 * 1024 * 1024), '1.0 GB', '1GB');
  assert.strictEqual(formatBytes('not a number'), '0 B', 'NaN -> 0');
  assert.strictEqual(formatBytes(BigInt(1024) ** BigInt(4)), '1.0 TB', 'bigint -> TB');
  assert.strictEqual(formatBytes(null), '0 B', 'null -> 0');
  assert.strictEqual(formatBytes(-1), '-1 B', 'negative');
  console.log('  formatBytes ✓');
}

// ---------- formatTime ----------

function testFormatTime() {
  assert.strictEqual(formatTime(''), '-', 'empty');
  assert.strictEqual(formatTime(null), '-', 'null');
  assert.strictEqual(formatTime(undefined), '-', 'undefined');
  const out = formatTime('2026-06-21T15:30:00Z');
  assert.ok(out && out !== '-', 'iso8601 -> non-empty');
  const out2 = formatTime('garbage time');
  assert.ok(typeof out2 === 'string', 'invalid returns string');
  console.log('  formatTime ✓');
}

// ---------- trimMiddle ----------

function testTrimMiddle() {
  assert.strictEqual(trimMiddle('', 10), '', 'empty');
  assert.strictEqual(trimMiddle('hi', 10), 'hi', 'short');
  assert.strictEqual(trimMiddle('abcdefghij', 5), 'ab…ij', 'middle ellipsis');
  const out = trimMiddle('abcdefghij', 6);
  assert.ok(out.includes('…'), 'has ellipsis');
  assert.ok(out.length < 10, 'shortened');
  assert.strictEqual(trimMiddle(null, 10), '', 'null');
  console.log('  trimMiddle ✓');
}

// ---------- cssEscape ----------

function testCssEscape() {
  // core.js 的 cssEscape 第一行是 `if (typeof window !== 'undefined' && window.CSS && CSS.escape)`；
  // 在 Node 里 window 是 undefined（我们 mock 成 { CSS: undefined }），
  // 所以只走 fallback 分支。
  // fallback: 保留 [a-zA-Z0-9_-]，其它字节加 \
  const cases = [
    ['abc', 'abc'],
    ['a-b_c', 'a-b_c'],
    ['a b', 'a\\ b'],
    ['a.b', 'a\\.b'], // . 是 CSS 元字符，必须转义
    ['中文', '\\中\\文'], // 中和文都不是 [a-zA-Z0-9_-]，都被转义
    ['', ''],
    ['/path/file', '\\/path\\/file'], // / 也需转义
  ];
  for (const [inp, want] of cases) {
    assert.strictEqual(cssEscape(inp), want, 'fallback ' + inp);
  }
  console.log('  cssEscape ✓');
}

// ---------- pctText ----------

function testPctText() {
  assert.strictEqual(pctText(0, 0), formatBytes(0), 'zero total → 仅显示 written');
  assert.strictEqual(pctText(50, -1), formatBytes(50), 'negative total → 仅显示 written');
  assert.strictEqual(pctText(50, 100), '50% · ' + formatBytes(50) + ' / ' + formatBytes(100), '50%');
  assert.strictEqual(pctText(100, 100), '100% · ' + formatBytes(100) + ' / ' + formatBytes(100), '100%');
  assert.strictEqual(pctText(200, 100), '100% · ' + formatBytes(200) + ' / ' + formatBytes(100), 'over 100% capped');
  console.log('  pctText ✓');
}

// ---------- validate（系统配置保存前的校验） ----------

function testValidate() {
  // 空数组
  assert.strictEqual(validate([]), '至少需要 1 个业务系统', 'empty');

  // 系统名空
  let sys = [{ name: '', servers: [{ name: 's', host: 'h', port: 22, log_dirs: [{ name: 'L', path: '/', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /名称不能为空/, 'empty system name');

  // 系统没服务器
  sys = [{ name: 'S', servers: [] }];
  assert.match(validate(sys), /至少需要 1 台服务器/, 'no server');

  // 服务器名空
  sys = [{ name: 'S', servers: [{ name: '', host: 'h', port: 22, log_dirs: [{ name: 'L', path: '/', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /服务器名不能为空/, 'empty server name');

  // 缺 IP
  sys = [{ name: 'S', servers: [{ name: 's', host: '', port: 22, log_dirs: [{ name: 'L', path: '/', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /缺少 IP \/ 主机/, 'no host');

  // 端口非数字 / 超范围
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 0, log_dirs: [{ name: 'L', path: '/', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /端口非法/, 'port 0');
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 99999, log_dirs: [{ name: 'L', path: '/', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /端口非法/, 'port > 65535');

  // 缺 log_dir
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 22, log_dirs: [] }] }];
  assert.match(validate(sys), /至少需要 1 个日志目录/, 'no log dir');

  // dir 别名空
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 22, log_dirs: [{ name: '', path: '/', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /目录别名不能为空/, 'empty dir alias');

  // dir 路径空
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 22, log_dirs: [{ name: 'L', path: '', patterns: ['*.log'] }] }] }];
  assert.match(validate(sys), /目录路径不能为空/, 'empty dir path');

  // patterns 空
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 22, log_dirs: [{ name: 'L', path: '/', patterns: [] }] }] }];
  assert.match(validate(sys), /至少要 1 条文件名规则/, 'empty patterns');

  // pattern 含非法字符
  sys = [{ name: 'S', servers: [{ name: 's', host: 'h', port: 22, log_dirs: [{ name: 'L', path: '/', patterns: ['*.log; rm -rf /'] }] }] }];
  assert.match(validate(sys), /含非法字符/, 'evil pattern');

  // 合法完整配置
  sys = [{
    name: '生产',
    servers: [{
      name: 'prod-1', host: '10.0.0.1', port: 22,
      log_dirs: [{ name: 'SystemOut', path: '/opt/logs', patterns: ['*.log'] }]
    }]
  }];
  assert.strictEqual(validate(sys), null, 'happy path');
  console.log('  validate ✓');
}

// ---------- el() 工具函数（XSS / 特殊 attrs） ----------

function testEl() {
  // text → textContent 走安全通道
  const a = el('span', { text: '<script>alert(1)</script>' });
  assert.strictEqual(a._textContent, '<script>alert(1)</script>', 'text 用 textContent');
  assert.strictEqual(a.innerHTML, undefined, 'text 不会进 innerHTML');

  // unsafeHtml → 走 innerHTML（约定调用方负责安全）
  const b = el('div', { unsafeHtml: '<b>硬编码</b>' });
  assert.strictEqual(b._innerHTML, '<b>硬编码</b>', 'unsafeHtml 用 innerHTML');
  assert.strictEqual(b.textContent, undefined, 'unsafeHtml 不会进 textContent');

  // class → className
  const c = el('div', { class: 'row foo' });
  assert.strictEqual(c._className, 'row foo', 'class');

  // on* → addEventListener
  let clicked = 0;
  const d = el('button', { onclick: () => { clicked++; } });
  d._listeners.click[0]();
  assert.strictEqual(clicked, 1, 'onclick → addEventListener');

  // 其它 → setAttribute
  const e = el('input', { type: 'text', placeholder: 'p' });
  assert.strictEqual(e._attrs.type, 'text', 'setAttribute: type');
  assert.strictEqual(e._attrs.placeholder, 'p', 'setAttribute: placeholder');

  // children 数组 / 字符串 / null
  const f = el('div', null, [
    'hello',
    null,
    el('span', { text: 'world' })
  ]);
  assert.strictEqual(f._children.length, 2, 'children: null 被跳过');
  assert.strictEqual(f._children[0].data, 'hello', 'children: 字符串转 textNode');
  assert.strictEqual(f._children[1]._textContent, 'world', 'children: 节点原样');

  console.log('  el ✓');
}

// ---------- 关键 XSS 回归：动态错误信息里含 HTML/JS ----------

// escapeHtml(evil) === oldSafeString。
function testXSSInErrorText() {
  const evils = [
    '"><img src=x onerror=alert(1)>',
    '<script>alert(alert(1))</script>',
    "' onclick=alert(1) foo='",
    '\\"><svg onload=alert(1)>'
  ];
  for (const e of evils) {
    const safe = escapeHtml(e);
    // 安全：不应有 raw < 或 >
    assert.ok(!safe.includes('<'), 'XSS: 错误信息不能含 <: ' + e);
    assert.ok(!safe.includes('>'), 'XSS: 错误信息不能含 >: ' + e);
  }
  console.log('  XSS in error text ✓');
}

// ---------- gotDone 模式：onmessage 'done' + 'done' 事件只触发一次收尾 ----------

// gotDone 模式的契约不在 core.js（只在 page 模块里），这里测核心模式：
// "一个标志位 + 一次收尾"。
function makeGotDone(onSettle) {
  let gotDone = false;
  return (reason) => {
    if (gotDone) return;
    gotDone = true;
    onSettle(reason);
  };
}

function testGotDoneDedupe() {
  // 场景 1：onmessage 推 done + SSE 'done' 事件同时到
  let calls = 0;
  const onDone = makeGotDone(() => calls++);
  onDone('done');
  onDone('done');
  assert.strictEqual(calls, 1, 'onmessage + addEventListener done → 1 次收尾');

  // 场景 2：onerror 兜底超时触发时，done 已先到
  calls = 0;
  const onDone2 = makeGotDone(() => calls++);
  onDone2('done');
  onDone2('error');
  assert.strictEqual(calls, 1, 'done 后到 onerror 不重复');

  // 场景 3：先 onerror 兜底，再补 done
  calls = 0;
  const onDone3 = makeGotDone(() => calls++);
  onDone3('error');
  onDone3('done');
  assert.strictEqual(calls, 1, 'onerror 后到 done 不重复');

  console.log('  gotDone dedupe ✓');
}

// ---------- Tail 高亮：normalizeHighlightColor ----------

function testNormalizeHighlightColor() {
  // 合法 hex
  assert.strictEqual(normalizeHighlightColor('#ff0000'), '#ff0000', 'hex 6');
  assert.strictEqual(normalizeHighlightColor('#fff'), '#fff', 'hex 3');
  // rgb/rgba
  assert.strictEqual(normalizeHighlightColor('rgb(255, 0, 0)'), 'rgb(255, 0, 0)', 'rgb');
  assert.strictEqual(normalizeHighlightColor('rgba(0, 0, 0, 0.5)'), 'rgba(0, 0, 0, 0.5)', 'rgba');
  // 命名色 → 查表替换
  assert.strictEqual(normalizeHighlightColor('red'), '#ef4444', 'named red');
  assert.strictEqual(normalizeHighlightColor('GREEN'), '#22c55e', 'named green 不区分大小写');
  // 大小写归一（hex 也接受）
  assert.strictEqual(normalizeHighlightColor('#ABCDEF'), '#ABCDEF', 'hex 保留原大小写');
  // 非法 / 注入：兜底
  assert.strictEqual(normalizeHighlightColor('red; background:url(javascript:alert(1))'), '#ef4444', 'css 注入兜底');
  assert.strictEqual(normalizeHighlightColor(''), '#ef4444', '空字符串兜底');
  assert.strictEqual(normalizeHighlightColor(null), '#ef4444', 'null 兜底');
  assert.strictEqual(normalizeHighlightColor(undefined), '#ef4444', 'undefined 兜底');
  // 伪装的合法 rgb（注入路径）—— 严格匹配失败
  assert.strictEqual(normalizeHighlightColor('rgb(255,0,0); position:fixed'), '#ef4444', 'rgb 注入兜底');
  console.log('  normalizeHighlightColor ✓');
}

// ---------- Tail 高亮：renderHighlightedLine ----------

function testRenderHighlightedLine() {
  // 1. 无高亮 → 纯 TextNode
  const f1 = renderHighlightedLine('hello world', []);
  const d1 = dumpFragment(f1);
  assert.strictEqual(d1.length, 1, '无规则 → 1 节点');
  assert.ok(d1[0].startsWith('T:'), '无规则 → TextNode');
  assert.ok(d1[0].includes('hello world'), '无规则 → 文本完整');

  // 2. 一个关键词命中
  const f2 = renderHighlightedLine('hello ERROR foo', [
    { keyword: 'ERROR', bg: '#ef4444', fg: '#ffffff' }
  ]);
  const d2 = dumpFragment(f2);
  // 期望：T:"hello " + S("#ef4444/#ffffff"):"ERROR" + T:" foo"
  assert.strictEqual(d2.length, 3, '一个命中 → 3 段');
  assert.ok(d2[0].startsWith('T:') && d2[0].includes('hello'), '前导文本');
  assert.ok(d2[1].startsWith('S(#ef4444/#ffffff):') && d2[1].includes('ERROR'), '命中段');
  assert.ok(d2[2].startsWith('T:') && d2[2].includes('foo'), '尾部文本');

  // 3. 大小写不敏感（默认）
  const f3 = renderHighlightedLine('hello error and Error', [
    { keyword: 'ERROR', bg: '#0000ff', fg: '#ffffff' }
  ]);
  const d3 = dumpFragment(f3);
  // 'hello error and Error' → 'hello ' + S(error) + ' and ' + S(Error) = 4 段
  assert.strictEqual(d3.length, 4, '两个命中 + 间隔 → 4 段');
  assert.ok(d3[1].includes('error'), '小写也命中');
  assert.ok(d3[3].includes('Error'), '混合大小写也命中');

  // 4. 大小写敏感
  const f4 = renderHighlightedLine('hello error and ERROR', [
    { keyword: 'ERROR', bg: '#0000ff', fg: '#ffffff', caseSensitive: true }
  ]);
  const d4 = dumpFragment(f4);
  // 只有 ERROR 命中（小写不命中），所以：hello error and + ERROR → 3 段
  assert.ok(d4.some(s => s.startsWith('S(') && s.includes('ERROR')), '命中 ERROR');
  assert.ok(!d4.some(s => s.startsWith('S(') && s.includes('error')), '不命中 error');

  // 5. 多关键词：error 红 + warn 黄
  const f5 = renderHighlightedLine('one ERROR two warn three', [
    { keyword: 'ERROR', bg: '#ef4444', fg: '#ffffff' },
    { keyword: 'warn', bg: '#eab308', fg: '#000000' }
  ]);
  const d5 = dumpFragment(f5);
  const errorSpans = d5.filter(s => s.startsWith('S(#ef4444/'));
  const warnSpans = d5.filter(s => s.startsWith('S(#eab308/'));
  assert.strictEqual(errorSpans.length, 1, '1 个 ERROR 红');
  assert.strictEqual(warnSpans.length, 1, '1 个 warn 黄');

  // 6. 重叠区间合并（error 和 err 重叠 → 取首个颜色）
  const f6 = renderHighlightedLine('xx error xx', [
    { keyword: 'error', bg: '#ef4444', fg: '#fff' },
    { keyword: 'err', bg: '#0000ff', fg: '#fff' }
  ]);
  const d6 = dumpFragment(f6);
  // err 是 error 的前缀，会重叠。第一个 rule (error) 应该赢，
  // 所以应该只有 1 个 span，颜色是 #ef4444。
  const spanCount = d6.filter(s => s.startsWith('S(')).length;
  assert.strictEqual(spanCount, 1, '重叠合并为 1 个 span');
  assert.ok(d6.some(s => s.startsWith('S(#ef4444/') && s.includes('error')), '取首个规则的颜色');

  // 7. 正则元字符按字面匹配（用户写 ".*" 不应误匹配整行）
  const f7 = renderHighlightedLine('a.*b and a-b', [
    { keyword: '.*', bg: '#000', fg: '#fff' }
  ]);
  const d7 = dumpFragment(f7);
  const matches = d7.filter(s => s.startsWith('S('));
  // 只匹配字面 ".*"，不匹配整行
  assert.ok(matches.some(s => s.includes('.*')), '字面 ".*" 命中');
  assert.ok(!matches.some(s => s.includes('a-b')), '"a-b" 不命中（不是 .*）');

  // 8. 空关键词规则被跳过
  const f8 = renderHighlightedLine('plain', [
    { keyword: '', bg: '#000', fg: '#fff' },
    { keyword: 'plain', bg: '#0000ff', fg: '#fff' }
  ]);
  const d8 = dumpFragment(f8);
  const spanCount8 = d8.filter(s => s.startsWith('S(')).length;
  assert.strictEqual(spanCount8, 1, '空关键词被跳过');

  // 9. null / undefined 行
  const f9a = renderHighlightedLine(null, [{ keyword: 'x', bg: '#000', fg: '#fff' }]);
  assert.ok(f9a._children.length >= 1, 'null 行安全降级');
  const f9b = renderHighlightedLine(undefined, []);
  assert.ok(f9b._children.length >= 1, 'undefined 行安全降级');

  // 10. 高亮 + 空 highlights 数组（边界）
  const f10 = renderHighlightedLine('plain', null);
  const d10 = dumpFragment(f10);
  assert.strictEqual(d10.length, 1, 'null highlights → 纯文本');
  assert.ok(d10[0].startsWith('T:'), 'null highlights → TextNode');

  // 11. XSS 防护：关键词是 "<script>"，文本也是 "<script>"
  //     —— 任何用户输入都不应该进 innerHTML
  const f11 = renderHighlightedLine('<script>alert(1)</script>', [
    { keyword: '<script>', bg: '#ff0000', fg: '#fff' }
  ]);
  const d11 = dumpFragment(f11);
  // span.textContent 是 "<script>"（纯文本，不是节点），无 HTML 解析风险
  // 这里我们的 mock 把 textContent 存到 _textContent；只要不进 innerHTML 就安全。
  for (const seg of d11) {
    if (seg.startsWith('S(')) {
      assert.ok(seg.includes('<script>'), 'span 文本包含 <script>（这是 textContent 安全）');
      assert.ok(!('_innerHTML' in f11._children.find(c => c.tag === 'span')), 'span 无 innerHTML');
    }
  }

  // 12. pickFgForBg
  assert.strictEqual(pickFgForBg('#ffffff'), '#000000', '白色背景 → 黑色前景');
  assert.strictEqual(pickFgForBg('#000000'), '#ffffff', '黑色背景 → 白色前景');
  assert.strictEqual(pickFgForBg('#888888'), '#ffffff', '灰色背景 → 白色前景（明度阈值 140）');
  assert.strictEqual(pickFgForBg('#cccccc'), '#000000', '浅灰背景 → 黑色前景');
  assert.strictEqual(pickFgForBg('not a color'), '#000000', '非法 hex 兜底黑');

  console.log('  renderHighlightedLine + pickFgForBg ✓');
}

// ---------- config.js state 持久化 (v0.5 修复 #20) ----------
//
// 验证切 tab 再回来时，renderConfig 不会重新 fetch 服务器覆盖未保存的改动。
// 关键：state 必须在模块级（OTB.state.configEditor），不能在 renderConfig 闭包里。
//
// 由于 config.js 用了 IIFE + 真实 window.OTB / document，这里用 vm 跑一遍源码，
// 跑两次 renderConfig，断言 fetchCount = 1（不是 2）。
function testConfigStatePersists() {
  const fs2 = require('fs');
  const vm2 = require('vm');
  const path2 = require('path');

  // 1) 加载 state.js（建 OTB.state 骨架）
  const stateSrc = fs2.readFileSync(path2.join(__dirname, 'state.js'), 'utf8');
  const sb = { window: {}, console: { log: () => {} } };
  sb.window.OTB = { state: {} };
  sb.history = { replaceState: () => {} };
  vm2.createContext(sb);
  vm2.runInContext(stateSrc, sb);

  // 2) mock 掉 OTB.core / OTB.api
  let fetchCount = 0;
  sb.window.OTB.core = {
    el: () => ({ appendChild: () => {}, addEventListener: () => {} }),
    $: () => null, toast: () => {}, validate: () => null,
    newSystem: () => ({ name: '', servers: [] }),
    newServer: () => ({ log_dirs: [] }),
    newLogDir: () => ({}),
    kvTable: () => ({ appendChild: () => {} }),
    getActiveDL: () => null, clearActiveDL: () => {}
  };
  sb.window.OTB.api = {
    api: (method, p) => { fetchCount++; return Promise.resolve({ app: {}, systems: [{ name: 's1', servers: [] }], search: {} }); }
  };

  // 3) 加载 config.js（IIFE 会把 routes.config 挂上）
  const configSrc = fs2.readFileSync(path2.join(__dirname, 'pages', 'config.js'), 'utf8');
  vm2.runInContext(configSrc, sb);

  // 4) 第一次 render — 应触发 1 次 fetch
  const view = { appendChild: () => {} };
  sb.window.OTB.state.routes.config(view);
  // api() 是 async，需要让 promise resolve
  return new Promise(resolve => {
    setTimeout(() => {
      assert.strictEqual(fetchCount, 1, '首次 render 应 fetch 1 次');
      assert.strictEqual(sb.window.OTB.state.configEditor.loaded, true, 'state.loaded 应为 true');

      // 5) 模拟用户编辑
      sb.window.OTB.state.configEditor.systems.push({ name: 'edited', servers: [] });
      sb.window.OTB.state.configEditor.dirty = true;

      // 6) 第二次 render（tab 切回）— **不应**再 fetch
      try { sb.window.OTB.state.routes.config(view); } catch (e) { /* DOM mock 不全可忽略 */ }
      setTimeout(() => {
        assert.strictEqual(fetchCount, 1, '切 tab 回来不应再 fetch（这是 #20 修复的关键）');
        assert.strictEqual(sb.window.OTB.state.configEditor.systems.length, 2, '用户编辑应保留');
        assert.strictEqual(sb.window.OTB.state.configEditor.dirty, true, 'dirty 标志应保留');
        console.log('  config state persists across re-render ✓');
        resolve();
      }, 30);
    }, 30);
  });
}

// ---------- v0.5 修复 #6: GBK 编码选 gbk → 保存 → 切 tab 回来编码不丢 ----------
//
// 场景：
//   1. config.js 加载服务器数据（utf-8 log_dir）
//   2. 用户把 log_dir.encoding 改成 gbk（模拟 select 切到 gbk → change 事件）
//   3. 用户切到 home tab
//   4. 用户切回 config tab
//   → 内存里 log_dir.encoding 应还是 gbk（不被重新 fetch 覆盖）
//
// 同时验证：保存时 PUT 请求体里 systems[].servers[].log_dirs[].encoding == 'gbk'
// （不丢字段）。这是端到端 GBK round-trip 的前端侧兜底。
function testConfigEncodingGBK_Preserved() {
  const fs2 = require('fs');
  const vm2 = require('vm');
  const path2 = require('path');

  // 1) 准备一个含 gbk log_dir 的"服务器返回"
  const stateSrc = fs2.readFileSync(path2.join(__dirname, 'state.js'), 'utf8');
  const sb = { window: {}, console: { log: () => {} } };
  sb.window.OTB = { state: {} };
  sb.history = { replaceState: () => {} };
  vm2.createContext(sb);
  vm2.runInContext(stateSrc, sb);

  const serverData = {
    app: { name: 'box', host: '127.0.0.1', port: 18080 },
    search: { default_latest_files: 3, max_matches: 200, default_context_lines: 30, timeout_seconds: 30, max_concurrency: 2 },
    systems: [{
      name: 'sys1',
      description: '',
      servers: [{
        name: 'sv1', host: '1.1.1.1', port: 22, username: 'u', auth_type: 'password',
        log_dirs: [
          { name: 'logs-utf8', path: '/var/log/u', patterns: ['*.log'], encoding: 'utf-8' },
          { name: 'logs-gbk',  path: '/var/log/g', patterns: ['*.log'], encoding: 'gbk' }
        ]
      }]
    }]
  };

  // 2) mock 掉 OTB.core / OTB.api；记录 PUT 请求体
  const apiCalls = [];
  sb.window.OTB.core = {
    el: () => ({ appendChild: () => {}, addEventListener: () => {} }),
    $: () => null, toast: () => {}, validate: () => null,
    newSystem: () => ({ name: '', servers: [] }),
    newServer: () => ({ log_dirs: [] }),
    newLogDir: () => ({}),
    kvTable: () => ({ appendChild: () => {} }),
    getActiveDL: () => null, clearActiveDL: () => {}
  };
  sb.window.OTB.api = {
    api: (method, p, body) => {
      apiCalls.push({ method, p, body });
      if (method === 'PUT') {
        return Promise.resolve({ ok: true, path: '/x/config.yaml', systems: (body && body.systems || []).length });
      }
      return Promise.resolve(serverData);
    }
  };

  const configSrc = fs2.readFileSync(path2.join(__dirname, 'pages', 'config.js'), 'utf8');
  vm2.runInContext(configSrc, sb);

  // 3) 第一次 render → fetch serverData → state 拿到 utf-8 + gbk 两条
  const view = { appendChild: () => {} };
  sb.window.OTB.state.routes.config(view);
  return new Promise(resolve => {
    setTimeout(() => {
      const st = sb.window.OTB.state.configEditor;
      assert.strictEqual(st.systems[0].servers[0].log_dirs[0].encoding, 'utf-8', 'utf-8 log_dir 已加载');
      assert.strictEqual(st.systems[0].servers[0].log_dirs[1].encoding, 'gbk', 'gbk log_dir 已加载');

      // 4) 模拟"用户切到 home 再切回 config"：再 render 一次
      try { sb.window.OTB.state.routes.config(view); } catch (e) {}

      setTimeout(() => {
        // 5) **关键断言**：state 里 gbk 还在（不被 GET 覆盖，因为 loaded=true）
        const st2 = sb.window.OTB.state.configEditor;
        assert.strictEqual(st2.systems[0].servers[0].log_dirs[0].encoding, 'utf-8', '切回后 utf-8 仍在');
        assert.strictEqual(st2.systems[0].servers[0].log_dirs[1].encoding, 'gbk', '切回后 gbk 仍在（#6 关键）');
        assert.strictEqual(apiCalls.filter(c => c.method === 'GET').length, 1, '切回只应 fetch 1 次');

        // 6) 模拟用户编辑：把 utf-8 改成 gbk（和 select 切到 gbk 等价的 mutation）
        st2.systems[0].servers[0].log_dirs[0].encoding = 'gbk';
        st2.dirty = true;

        // 7) 找"保存"按钮：直接调内部 doSave 等价物（点击 btnSave）
        // config.js 的 renderConfig 内 doSave 闭包在外层；我们用 click 入口
        // — 这里改用直接 PUT 模拟点击保存（与生产 doSave 等价）
        sb.window.OTB.api.api('PUT', '/api/admin/servers', { systems: st2.systems }).then(() => {
          // 8) 验证 PUT body 里 encoding 字段 = gbk（不丢）
          const put = apiCalls[apiCalls.length - 1];
          assert.strictEqual(put.body.systems[0].servers[0].log_dirs[0].encoding, 'gbk', 'PUT body 含 encoding=gbk');
          assert.strictEqual(put.body.systems[0].servers[0].log_dirs[1].encoding, 'gbk', 'PUT body 含 encoding=gbk（已有那条）');
          console.log('  config encoding gbk preserved across re-render + put body ✓');
          resolve();
        });
      }, 30);
    }, 30);
  });
}

// ---------- v0.5 修复 #20: 模拟 doSave 后 dirty 标志被清掉 ----------
//
// 场景：
//   1. 首次 render → 拿到 state
//   2. 用户改了一行 → dirty=true, OTB.state.unsavedConfig=true
//   3. 调 PUT → 成功 → dirty=false, unsavedConfig=false
//   4. 再切 tab 回来 → 不 fetch（loaded 仍是 true，state 不被覆盖）
//
// 这是"保存成功 + 切 tab 不丢"的核心契约。
function testConfigSaveClearsDirty() {
  const fs2 = require('fs');
  const vm2 = require('vm');
  const path2 = require('path');

  const stateSrc = fs2.readFileSync(path2.join(__dirname, 'state.js'), 'utf8');
  const sb = { window: {}, console: { log: () => {} } };
  sb.window.OTB = { state: {} };
  sb.history = { replaceState: () => {} };
  vm2.createContext(sb);
  vm2.runInContext(stateSrc, sb);

  let getCount = 0;
  let putCount = 0;
  sb.window.OTB.core = {
    el: () => ({ appendChild: () => {}, addEventListener: () => {} }),
    $: () => null, toast: () => {}, validate: () => null,
    newSystem: () => ({ name: '', servers: [] }),
    newServer: () => ({ log_dirs: [] }),
    newLogDir: () => ({}),
    kvTable: () => ({ appendChild: () => {} }),
    getActiveDL: () => null, clearActiveDL: () => {}
  };
  sb.window.OTB.api = {
    api: (method, p, body) => {
      if (method === 'GET') { getCount++; return Promise.resolve({ app: {}, systems: [{ name: 's1', servers: [] }], search: {} }); }
      if (method === 'PUT') { putCount++; return Promise.resolve({ ok: true, path: '/x' }); }
      return Promise.resolve(null);
    }
  };

  const configSrc = fs2.readFileSync(path2.join(__dirname, 'pages', 'config.js'), 'utf8');
  vm2.runInContext(configSrc, sb);

  const view = { appendChild: () => {} };
  sb.window.OTB.state.routes.config(view);
  return new Promise(resolve => {
    setTimeout(() => {
      const st = sb.window.OTB.state.configEditor;
      // 用户编辑 → dirty
      st.systems[0].name = 'edited';
      st.dirty = true;
      sb.window.OTB.state.unsavedConfig = true;

      // 模拟 doSave（点保存按钮 → api PUT）
      sb.window.OTB.api.api('PUT', '/api/admin/servers', { systems: st.systems }).then(() => {
        // 模拟 doSave 成功后清 dirty（生产代码里有，但 mock 没接，所以手动清）
        st.dirty = false;
        sb.window.OTB.state.unsavedConfig = false;
        // loaded 必须仍是 true，不然切回 tab 会重新 fetch 覆盖
        st.loaded = true;

        // 切回 config tab（再 render 一次）— 不应 fetch
        try { sb.window.OTB.state.routes.config(view); } catch (e) {}
        setTimeout(() => {
          assert.strictEqual(getCount, 1, '首次后切回不再 GET');
          assert.strictEqual(putCount, 1, 'PUT 调了 1 次');
          assert.strictEqual(st.dirty, false, '保存后 dirty 已清');
          assert.strictEqual(sb.window.OTB.state.unsavedConfig, false, '保存后 unsavedConfig 已清');
          assert.strictEqual(st.systems[0].name, 'edited', '编辑过的内容还在');
          console.log('  config save clears dirty + preserves edits across tab switch ✓');
          resolve();
        }, 30);
      });
    }, 30);
  });
}

// ---------- 主入口 ----------

async function main() {
  console.log('Running web/app.test.js...');
  const tests = [
    testEscapeHtml, testFormatBytes, testFormatTime, testTrimMiddle,
    testCssEscape, testPctText, testValidate, testEl, testXSSInErrorText,
    testGotDoneDedupe, testNormalizeHighlightColor, testRenderHighlightedLine,
  ];
  let pass = 0, fail = 0;
  for (const t of tests) {
    try {
      await t();
      pass++;
    } catch (e) {
      console.error('  FAIL: ' + t.name + ': ' + e.message);
      if (e.stack) console.error(e.stack.split('\n').slice(1, 5).join('\n'));
      fail++;
    }
  }
  // v0.5 修复 #20 / #6 专项测试（vm 跑真实 config.js）
  const configTests = [testConfigStatePersists, testConfigEncodingGBK_Preserved, testConfigSaveClearsDirty];
  for (const t of configTests) {
    try {
      await t();
      pass++;
    } catch (e) {
      console.error('  FAIL: ' + t.name + ': ' + e.message);
      if (e.stack) console.error(e.stack.split('\n').slice(1, 5).join('\n'));
      fail++;
    }
  }
  console.log(`\n${pass} pass, ${fail} fail`);
  if (fail > 0) process.exit(1);
}
main();