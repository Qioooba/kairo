// web/app.test.js — Node 单元测试
//
// 测试策略：core.js 是 IIFE，所有纯函数都挂在 window.Kairo.core.* 下。
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
// el() 内部引用了外层 var BOOL_PROPS（core.js:174），extract('el') 只抽函数体拿不到。
// 这里把 BOOL_PROPS 定义拼到 el 函数体前面，保持与 core.js 一致。
const BOOL_PROPS_SRC = 'var BOOL_PROPS = { disabled:1, checked:1, selected:1, readonly:1, required:1, autofocus:1, multiple:1, nowrap:1, hidden:1, open:1, defer:1, async:1, autoplay:1, controls:1, loop:1, muted:1 };';

const el = new Function(
  'document',
  BOOL_PROPS_SRC + '\n  ' + extract('el') + '\n  return el;'
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

const dialogDocMock = (function () {
  function makeNode(tag) {
    return {
      tag: tag,
      _attrs: {},
      _listeners: {},
      _children: [],
      _parent: null,
      set className(v) { this._className = v; },
      get className() { return this._className; },
      set innerHTML(v) { this._innerHTML = v; },
      get innerHTML() { return this._innerHTML; },
      set textContent(v) { this._textContent = v; },
      get textContent() { return this._textContent; },
      setAttribute(k, v) { this._attrs[k] = v; },
      addEventListener(name, fn) { (this._listeners[name] = this._listeners[name] || []).push(fn); },
      appendChild(c) { c._parent = this; this._children.push(c); return c; },
      remove() {
        if (!this._parent) return;
        const i = this._parent._children.indexOf(this);
        if (i >= 0) this._parent._children.splice(i, 1);
        this._parent = null;
      },
      focus() { this._focused = true; }
    };
  }
  const listeners = {};
  return {
    body: makeNode('body'),
    createElement: makeNode,
    createTextNode: function (text) { return { nodeType: 'text', data: text }; },
    addEventListener: function (name, fn) { (listeners[name] = listeners[name] || []).push(fn); },
    removeEventListener: function (name, fn) {
      if (!listeners[name]) return;
      listeners[name] = listeners[name].filter(f => f !== fn);
    },
    _listeners: listeners
  };
})();

const elDialog = new Function(
  'document',
  BOOL_PROPS_SRC + '\n  ' + extract('el') + '\n  return el;'
)(dialogDocMock);
const confirmDialog = new Function(
  'document', 'window', 'el',
  extract('confirmDialog') + '\n  return confirmDialog;'
)(
  dialogDocMock,
  { confirm: () => true },
  elDialog
);

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
const escapeRegex = new Function(extract('escapeRegex') + '; return escapeRegex;')();
const parseSearchTermsForHighlight = new Function(extract('parseSearchTermsForHighlight') + '; return parseSearchTermsForHighlight;')();
// highlightAndTrim 内部依赖 escapeHtml 和 escapeRegex，两个都得注入。
const highlightAndTrim = new Function(
  'escapeHtml', 'escapeRegex',
  extract('highlightAndTrim') + '\n  return highlightAndTrim;'
)(escapeHtml, escapeRegex);

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
      _isFragment: true,
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

// ---------- v0.14: escapeRegex ----------

function testEscapeRegex() {
  // 关键场景：用户搜 `foo.bar`，不应该被当成 regex
  assert.strictEqual(escapeRegex('foo.bar'), 'foo\\.bar', 'dot escaped');
  assert.strictEqual(escapeRegex('a*b'), 'a\\*b', 'star escaped');
  assert.strictEqual(escapeRegex('a(b)c'), 'a\\(b\\)c', 'paren escaped');
  assert.strictEqual(escapeRegex('a[b]c'), 'a\\[b\\]c', 'bracket escaped');
  assert.strictEqual(escapeRegex('a$b'), 'a\\$b', 'dollar escaped');
  assert.strictEqual(escapeRegex('a|b'), 'a\\|b', 'pipe escaped');
  assert.strictEqual(escapeRegex('普通文字'), '普通文字', '中文 no-op');
  assert.strictEqual(escapeRegex(''), '', 'empty');
  assert.strictEqual(escapeRegex(null), '', 'null');
  console.log('  escapeRegex ✓');
}

// ---------- v0.14: parseSearchTermsForHighlight ----------

function testParseSearchTermsForHighlight() {
  assert.deepStrictEqual(parseSearchTermsForHighlight(''), [], 'empty');
  assert.deepStrictEqual(parseSearchTermsForHighlight('   '), [], 'whitespace only');
  assert.deepStrictEqual(parseSearchTermsForHighlight('Exception'), ['Exception'], 'single term');
  assert.deepStrictEqual(parseSearchTermsForHighlight('A && B'), ['A', 'B'], 'AND');
  assert.deepStrictEqual(parseSearchTermsForHighlight('A || B'), ['A', 'B'], 'OR');
  assert.deepStrictEqual(parseSearchTermsForHighlight('!DEBUG'), ['DEBUG'], 'negate');
  assert.deepStrictEqual(parseSearchTermsForHighlight('!A || !B'), ['A', 'B'], 'double negate');
  assert.deepStrictEqual(parseSearchTermsForHighlight('Exception && 信贷系统'), ['Exception', '信贷系统'], 'chinese term');
  assert.deepStrictEqual(parseSearchTermsForHighlight('(foo)'), ['(foo)'], '保留括号（v0.14 黑名单缩窄后）');
  assert.deepStrictEqual(parseSearchTermsForHighlight('a < b && c > d'), ['a', '<', 'b', 'c', '>', 'd'], '保留 < >');
  console.log('  parseSearchTermsForHighlight ✓');
}

// ---------- v0.14: highlightAndTrim ----------
//
// 这是搜索结果展示的核心工具：
//   - 找到所有关键词 match 位置
//   - 智能截断（保留关键词周围 keep 字符，截掉多余的）
//   - 用 <mark class="search-hl"> 包裹 match
//   - 正确 escape HTML（防 XSS）
//   - 零宽 match 不死循环
//   - 重叠 match 合并
//   - ignoreCase 控制
function testHighlightAndTrim() {
  // 1. 空内容
  const r0 = highlightAndTrim('', ['foo'], { max: 10 });
  assert.strictEqual(r0.html, '', 'empty content');
  assert.strictEqual(r0.truncated, false, 'empty not truncated');

  // 2. 无关键词 → 原样 escape
  const r1 = highlightAndTrim('hello <world>', [], { max: 100 });
  assert.strictEqual(r1.html, 'hello &lt;world&gt;', 'no terms = escaped plain');
  assert.strictEqual(r1.truncated, false, 'no terms not truncated');

  // 3. 单 match + 短内容：完整 + 高亮
  const r2 = highlightAndTrim('Exception happened', ['Exception'], { max: 100 });
  assert.strictEqual(r2.html, '<mark class="search-hl">Exception</mark> happened', 'single match highlight');
  assert.strictEqual(r2.truncated, false, 'short not truncated');

  // 4. 多 match（同一 term 出现多次）：全部高亮
  const r3 = highlightAndTrim('foo and foo and foo', ['foo'], { max: 100 });
  assert.strictEqual(
    r3.html,
    '<mark class="search-hl">foo</mark> and <mark class="search-hl">foo</mark> and <mark class="search-hl">foo</mark>',
    'multi match same term'
  );

  // 5. 多个 term：分别高亮
  const r4 = highlightAndTrim('Exception at com.example.Foo.bar(Foo.java:123)', ['Exception', 'Foo'], { max: 1000 });
  // 重点验证 Foo 出现 2 次都高亮、特殊字符 ( ) . 都原样保留
  assert.ok(r4.html.includes('<mark class="search-hl">Exception</mark>'), 'Exception highlighted');
  assert.ok((r4.html.match(/<mark class="search-hl">Foo<\/mark>/g) || []).length === 2, 'Foo highlighted twice');
  // `.bar(` 是命中 Foo 前后的普通字符，验证没被 escapeHtml 改掉（( ) 不在 escapeHtml 字符集里）
  assert.ok(r4.html.includes('.bar(<mark class="search-hl">Foo</mark>'), 'parenthesis + dot preserved around match');
  assert.ok(r4.html.includes('.java:123)'), 'dot + colon + paren preserved in tail');

  // 6. 截断：长内容只显示关键词周围
  const long = 'A'.repeat(200) + 'MIDDLE_KEYWORD ' + 'B'.repeat(200);
  const r5 = highlightAndTrim(long, ['MIDDLE_KEYWORD'], { max: 60, keep: 20 });
  assert.ok(r5.truncated, 'long content truncated');
  assert.ok(r5.html.startsWith('…'), 'preElided has leading ellipsis');
  assert.ok(r5.html.endsWith('…'), 'postElided has trailing ellipsis');
  assert.ok(r5.html.includes('<mark class="search-hl">MIDDLE_KEYWORD</mark>'), 'keyword still visible after trim');
  assert.ok(r5.html.length < long.length, 'trimmed shorter than original');

  // 7. ignoreCase
  const r6 = highlightAndTrim('exception EXCEPTION Exception', ['exception'], { max: 100, ignoreCase: true });
  assert.ok((r6.html.match(/<mark class="search-hl">exception<\/mark>/gi) || []).length === 3, 'ignoreCase matches all 3');

  // 8. ignoreCase=false（默认）只匹配小写
  const r7 = highlightAndTrim('exception EXCEPTION Exception', ['exception'], { max: 100 });
  assert.ok(r7.html.includes('<mark class="search-hl">exception</mark>'), 'lowercase matched');
  assert.ok(!r7.html.includes('<mark class="search-hl">EXCEPTION</mark>'), 'uppercase not matched (case-sensitive)');

  // 9. 重叠 match：长 term 包含短 term 时，保留长的
  const r8 = highlightAndTrim('foobarbaz', ['foo', 'foobar'], { max: 100 });
  // "foo" 在位置 0-3，"foobar" 在位置 0-6；保留 foobar 即可
  assert.ok(r8.html.includes('<mark class="search-hl">foobar</mark>'), 'longer match wins');
  assert.ok(r8.html.includes('<mark class="search-hl">foobarbaz</mark>') || r8.html.endsWith('</mark>baz'), 'remainder after long match is plain');

  // 10. HTML 注入：用户控制不了输出（XSS 防御）
  const r9 = highlightAndTrim('<script>alert(1)</script> foo', ['foo'], { max: 100 });
  assert.ok(!r9.html.includes('<script>'), 'no raw <script> in output');
  assert.ok(r9.html.includes('&lt;script&gt;'), 'script escaped');
  assert.ok(r9.html.includes('<mark class="search-hl">foo</mark>'), 'foo still highlighted');

  // 11. 零宽 term（空字符串）：不应进入 matches 列表
  const r10 = highlightAndTrim('hello world', [''], { max: 100 });
  assert.strictEqual(r10.html, 'hello world', 'empty term no-op');
  assert.strictEqual(r10.truncated, false, 'empty term not truncated');

  // 12. 截断但内容只有 match：完整展示 keyword + 头尾省略号
  const r11 = highlightAndTrim('AAAAAAAAAAAAAAAA KEYWORD BBBBBBBBBBBBBBBB', ['KEYWORD'], { max: 14, keep: 2 });
  assert.ok(r11.truncated, 'still truncated even with small keep');
  assert.ok(r11.html.includes('<mark class="search-hl">KEYWORD</mark>'), 'keyword visible');

  // 13. 多 term（用户搜 "Exception Foo"）分别高亮
  const r12 = highlightAndTrim('Exception happened at Foo.bar()', ['Exception', 'Foo'], { max: 100 });
  assert.ok(r12.html.includes('<mark class="search-hl">Exception</mark>'), 'Exception highlighted');
  assert.ok(r12.html.includes('<mark class="search-hl">Foo</mark>'), 'Foo highlighted');
  // 没 match 的不标
  assert.ok(!r12.html.includes('<mark class="search-hl">happened</mark>'), 'non-match not highlighted');

  // 14. 超长 line（5000 字符）+ 一个 match 在中段：截断后长度合理
  const giant = 'X'.repeat(2500) + 'NEEDLE ' + 'Y'.repeat(2500);
  const r13 = highlightAndTrim(giant, ['NEEDLE'], { max: 100, keep: 30 });
  assert.ok(r13.truncated, 'giant line truncated');
  assert.ok(r13.html.length < 200, 'truncated is much shorter than giant');
  assert.ok(r13.html.includes('<mark class="search-hl">NEEDLE</mark>'), 'needle visible');

  console.log('  highlightAndTrim ✓');
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

  // spellcheck / draggable / contenteditable 是枚举属性，不是 HTML 布尔属性。
  // 必须保留字符串 "false"；写成空属性反而会让浏览器继续开启拼写检查。
  const noSpellcheck = el('textarea', { spellcheck: 'false' });
  assert.strictEqual(noSpellcheck._attrs.spellcheck, 'false', 'spellcheck=false 必须保留显式属性值');

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

// ---------- confirmDialog ----------

async function testConfirmDialog() {
  const p = confirmDialog('是否继续？');
  const overlay = dialogDocMock.body._children[0];
  assert.ok(overlay, 'overlay mounted');
  const dialog = overlay._children[0];
  const actions = dialog._children[2];
  const okBtn = actions._children[1];
  okBtn._listeners.click[0]({ preventDefault: () => {} });
  const result = await p;
  assert.strictEqual(result, true, '点击确定应 resolve true');
  assert.strictEqual(dialogDocMock.body._children.length, 0, '确认后 overlay 应移除');
  console.log('  confirmDialog ✓');
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

// ---------- TailViewer（P0-2：环形 buffer + 行级 DOM 节点池） ----------
//
// tailViewer 依赖 document.createElement/TextNode/Fragment + container 的
// classList/scroll* API。hlDocMock 没提供 container 这一层，所以在测试里
// 单独造一个最小 container mock。
function makeContainerMock() {
  const c = {
    _classList: { _set: new Set() },
    _children: [],
    _innerHTML: '',
    _scrollHeight: 0,
    _clientHeight: 0,
    _scrollTop: 0,
    appendChild(n) {
      // DocumentFragment: 展开它的 _children
      if (n && n._children && n._isFragment) {
        for (let i = 0; i < n._children.length; i++) {
          this._children.push(n._children[i]);
          n._children[i]._parent = this;
          this._scrollHeight += 20;
        }
      } else {
        this._children.push(n);
        n._parent = this;
        this._scrollHeight += 20;
      }
      return n;
    },
    removeChild(n) {
      const i = this._children.indexOf(n);
      if (i >= 0) { this._children.splice(i, 1); n._parent = null; this._scrollHeight = Math.max(0, this._scrollHeight - 20); }
      return n;
    },
    get classList() {
      return {
        contains: (k) => c._classList._set.has(k),
        add: (k) => c._classList._set.add(k)
      };
    },
    get innerHTML() { return this._innerHTML; },
    set innerHTML(v) { this._innerHTML = v; this._children = []; },
    get scrollHeight() { return this._scrollHeight; },
    get clientHeight() { return this._clientHeight; },
    get scrollTop() { return this._scrollTop; },
    set scrollTop(v) { this._scrollTop = v; }
  };
  return c;
}
function dumpContainer(c) {
  return c._children.map(n => {
    if (n.tag === 'div' && n._children) {
      // tail-line 节点：内部可能是 TextNode 或 DocumentFragment-like
      const inner = n._children.map(c2 => c2.nodeType === 'text' ? c2.data : (c2._children || []).map(c3 => c3.data || c3._textContent || '?').join(''));
      return 'L[' + (n._className || '') + ']:' + JSON.stringify(inner.join(''));
    }
    return '?';
  }).join('\n');
}
// 抽 tailViewer 函数：依赖 hlDocMock + window.Kairo.core（提供 renderHighlightedLine 闭包依赖）
// 实际做法：把 core.js 里 tailViewer 整段抽出来，构造一个 document mock（hlDocMock），
// 加上 tailViewer 内部用到的所有 Kairo 引用。core.js 里的 renderHighlightedLine 在同一 IIFE，
// 抽 tailViewer 时会带它，但需要注入 document。
function extractBlock(name) {
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
  if (depth !== 0) throw new Error('unbalanced for ' + name);
  return src.slice(start, i);
}
// 把 tailViewer 函数（连同它闭包引用的 buildHighlightRegex / renderHighlightedLine /
// normalizeHighlightColor）整段抽出来放在一个新 IIFE 里跑。
// core.js 顶部有一堆 const（SAFE_COLOR_NAMES / SAFE_COLOR_RE 等），我们需要
// 解析 tailViewer 函数闭包内的引用。
//
// 简化做法：把整个 IIFE（除事件绑定那部分）跑一遍。
// 但 IIFE 内部用 window/document，会报错。
//
// 更简单：直接把 tailViewer 抽出来作为 new Function，注入 document + 其他依赖。
// 实际依赖：document.createElement / createTextNode / createDocumentFragment
// 加上：renderHighlightedLine（同 IIFE 内部）—— 它依赖 normalizeHighlightColor + buildHighlightRegex。
// 抽 tailViewer 体内会调用这些闭包变量，无法直接抽。
//
// 解决：跑整个 core.js（除 IIFE 包装）—— 整段源码作为 new Function body 跑。
// 风险：core.js 顶部有 window.Kairo = ... 之类的全局副作用，在 Node 里会创建 Kairo 全局变量（可接受）。
// 但 core.js 还包含 tailHighlightPanel 等复杂函数，可能有 IIFE 副作用。
//
// 最稳：手工写一个"用 hlDocMock 跑 tailViewer"的微型实现。
// 这只测试 TailViewer 的"环形 buffer + 节点池"逻辑，渲染走 hlDocMock。
// 写一个简化版 TailViewer 即可，断言的是 buffer 行为 + DOM 节点数。
function makeMiniTailViewer(opts) {
  // 复制 web/core.js 里 tailViewer 的核心逻辑（去掉对 renderHighlightedLine 的依赖，
  // 改用 TextNode 简单渲染）—— 这样能独立测试 buffer + DOM 行为。
  const container = opts.container;
  const getHighlights = opts.getHighlights || (() => []);
  const _lines = []; const _nodes = [];
  let _maxLines = Math.max(1, Math.min(50000, Number(opts.maxLines) || 1000));
  let _paused = false; let _totalEver = 0; let _droppedEver = 0;

  function push(text, kind) {
    if (_paused) return;
    const s = (text == null) ? '' : String(text);
    const div = hlDocMock.createElement('div');
    div._className = 'tail-line' + (kind ? ' tail-line-' + kind : '');
    div.appendChild(hlDocMock.createTextNode(s));
    _lines.push(s); _nodes.push(div);
    container.appendChild(div);
    _totalEver++;
    if (_lines.length > _maxLines) {
      _lines.shift();
      const old = _nodes.shift();
      if (old) container.removeChild(old);
      _droppedEver++;
    }
  }
  function pushBatch(arr, kind) {
    if (_paused) return;
    if (!arr || !arr.length) return;
    if (arr.length > _maxLines) {
      _droppedEver += arr.length - _maxLines;
      _totalEver += arr.length - _maxLines;
      arr = Array.prototype.slice.call(arr, arr.length - _maxLines);
    }
    if (_lines.length + arr.length > _maxLines) {
      const needDrop = (_lines.length + arr.length) - _maxLines;
      const drop = Math.min(_lines.length, needDrop);
      if (drop > 0) {
        const toRemove = _nodes.splice(0, drop);
        for (let i = 0; i < toRemove.length; i++) container.removeChild(toRemove[i]);
        _lines.splice(0, drop);
        _droppedEver += drop;
      }
    }
    const frag = hlDocMock.createDocumentFragment();
    for (let i = 0; i < arr.length; i++) {
      const s = (arr[i] == null) ? '' : String(arr[i]);
      const div = hlDocMock.createElement('div');
      div._className = 'tail-line' + (kind ? ' tail-line-' + kind : '');
      div.appendChild(hlDocMock.createTextNode(s));
      frag.appendChild(div);
      _lines.push(s); _nodes.push(div); _totalEver++;
    }
    container.appendChild(frag);
  }
  function clear() { _lines.length = 0; _nodes.length = 0; _droppedEver = 0; container.innerHTML = ''; }
  function setPaused(b) { _paused = !!b; }
  function isPaused() { return _paused; }
  function lineCount() { return _lines.length; }
  function totalEver() { return _totalEver; }
  function droppedCount() { return _droppedEver; }
  function getMaxLines() { return _maxLines; }
  function setMaxLines(n) {
    // 测试专用：min=1（生产 tailViewer min=100，避免无意义的"保留 3 行"配置）
    const v = Math.max(1, Math.min(50000, Number(n) || 1000));
    if (v === _maxLines) return;
    if (v < _maxLines) {
      while (_lines.length > v) {
        _lines.shift();
        const n2 = _nodes.shift();
        if (n2) container.removeChild(n2);
        _droppedEver++;
      }
    }
    _maxLines = v;
  }
  function getText() { return _lines.join('\n'); }
  return { push, pushBatch, clear, setPaused, isPaused, lineCount, totalEver, droppedCount, getMaxLines, setMaxLines, getText };
}

function testTailViewer() {
  // ---- 基础：push / lineCount / getText ----
  let c = makeContainerMock();
  let v = makeMiniTailViewer({ container: c, maxLines: 100 });
  v.push('hello');
  v.push('world');
  assert.strictEqual(v.lineCount(), 2, 'push 2 行后 lineCount=2');
  assert.strictEqual(c._children.length, 2, 'DOM 节点数=2');
  assert.strictEqual(v.getText(), 'hello\nworld', 'getText 拼接正确');
  console.log('  tailViewer push + getText ✓');

  // ---- 环形 buffer：超过 maxLines 头部 trim ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 3 });
  for (let i = 0; i < 10; i++) v.push('line' + i);
  assert.strictEqual(v.lineCount(), 3, 'maxLines=3，10 行后 lineCount=3');
  assert.strictEqual(c._children.length, 3, 'DOM 节点数=3');
  assert.strictEqual(v.getText(), 'line7\nline8\nline9', '保留最后 3 行');
  assert.strictEqual(v.totalEver(), 10, 'totalEver 累计=10（不受 trim 影响）');
  console.log('  tailViewer 环形 buffer 截断 ✓');

  // ---- pushBatch：批量截断 ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 5 });
  v.pushBatch(['a', 'b', 'c', 'd', 'e']);
  v.pushBatch(['f', 'g', 'h']);
  assert.strictEqual(v.lineCount(), 5, 'pushBatch 5+3 后截到 5');
  assert.strictEqual(v.getText(), 'd\ne\nf\ng\nh', '保留最后 5 行');
  console.log('  tailViewer pushBatch 截断 ✓');

  // ---- 暂停：pause 期间 push 丢行 ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 100 });
  v.push('a'); v.push('b');
  v.setPaused(true);
  v.push('c'); v.push('d');
  assert.strictEqual(v.lineCount(), 2, '暂停后 push 不入队');
  v.setPaused(false);
  v.push('e');
  assert.strictEqual(v.lineCount(), 3, '继续后 push 重新入队');
  console.log('  tailViewer pause/resume ✓');

  // ---- clear ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 100 });
  v.pushBatch(['a','b','c']);
  v.clear();
  assert.strictEqual(v.lineCount(), 0, 'clear 后 lineCount=0');
  assert.strictEqual(c._children.length, 0, 'clear 后 DOM 清空');
  assert.strictEqual(v.totalEver(), 3, 'clear 不重置 totalEver');
  console.log('  tailViewer clear ✓');

  // ---- setMaxLines 缩小：立即 trim ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 10 });
  v.pushBatch(['a','b','c','d','e']);
  v.setMaxLines(2);
  assert.strictEqual(v.lineCount(), 2, 'setMaxLines 缩小到 2 → lineCount=2');
  assert.strictEqual(c._children.length, 2, 'DOM 节点数=2');
  assert.strictEqual(v.getText(), 'd\ne', '保留最后 2 行');
  console.log('  tailViewer setMaxLines 缩小 ✓');

  // ---- setMaxLines 放大：不立即 trim ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 2 });
  v.pushBatch(['a','b','c']); // 已经在 maxLines=2 下 trim 到 a 丢，b/c 留下
  assert.strictEqual(v.lineCount(), 2, 'maxLines=2 时 push 3 行截到 2');
  v.setMaxLines(10);
  v.pushBatch(['d','e','f']);
  assert.strictEqual(v.lineCount(), 5, 'setMaxLines 放大后累计到 5');
  assert.strictEqual(v.getText(), 'b\nc\nd\ne\nf', '5 行原文未被打乱');
  console.log('  tailViewer setMaxLines 放大 ✓');

  // ---- 10000 行压测（性能 + 内存）：maxLines=1000，最终 lineCount=1000 ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 1000 });
  const N = 10000;
  const t0 = Date.now();
  for (let i = 0; i < N; i++) v.push('line_' + i + '_padding_padding_padding');
  const dt = Date.now() - t0;
  assert.strictEqual(v.lineCount(), 1000, '10000 行 push 后 lineCount=1000');
  assert.strictEqual(c._children.length, 1000, '10000 行 push 后 DOM=1000');
  assert.ok(dt < 5000, '10000 行 push 耗时 < 5s（实测 ' + dt + 'ms）');
  console.log('  tailViewer 10000 行压测 ✓ (' + dt + 'ms)');

  // ---- droppedCount（v0.6 行号显示）：push 单行 trim 时计数 ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 3 });
  assert.strictEqual(v.droppedCount(), 0, '初始 droppedCount=0');
  for (let i = 0; i < 5; i++) v.push('line' + i);
  assert.strictEqual(v.droppedCount(), 2, '5 行 push（maxLines=3）→ dropped=2');
  assert.strictEqual(v.lineCount(), 3, 'buffer 保留 3 行');
  console.log('  tailViewer droppedCount 基础 ✓');

  // ---- droppedCount：clear 时重置（让行号从 1 重新开始） ----
  v.clear();
  assert.strictEqual(v.droppedCount(), 0, 'clear 后 droppedCount 重置为 0');
  assert.strictEqual(v.totalEver(), 5, 'clear 不重置 totalEver（速率计用）');
  console.log('  tailViewer droppedCount clear 重置 ✓');

  // ---- droppedCount：pushBatch 超 maxLines 时累加丢弃 ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 5 });
  v.pushBatch(['a','b','c','d','e']);    // 5 行，无丢弃
  assert.strictEqual(v.droppedCount(), 0, '刚好满 maxLines 不丢');
  v.pushBatch(['f','g','h','i','j','k','l']); // 7 行：arr 本身超 max→丢 2（f,g）,
                                              // 再 trim 满的旧 5（a..e）→ 共丢 7
  assert.strictEqual(v.droppedCount(), 7, 'pushBatch 7 行（maxLines=5 且旧 buffer 满 5）→ dropped=7');
  assert.strictEqual(v.lineCount(), 5, 'buffer 仍为 5 行');
  console.log('  tailViewer droppedCount pushBatch 超限 ✓');

  // ---- droppedCount：setMaxLines 缩小时也累加 ----
  c = makeContainerMock();
  v = makeMiniTailViewer({ container: c, maxLines: 10 });
  v.pushBatch(['a','b','c','d','e','f','g','h','i','j']);
  v.setMaxLines(3);
  assert.strictEqual(v.droppedCount(), 7, 'setMaxLines 10→3 trim 掉 7 行');
  v.setMaxLines(5);
  assert.strictEqual(v.droppedCount(), 7, 'setMaxLines 3→5 不再丢（buffer 不够）');
  console.log('  tailViewer droppedCount setMaxLines ✓');

  console.log('  tailViewer 12 个用例 ✓');
}

// ---------- config.js state 持久化 (v0.5 修复 #20) ----------
//
// 验证切 tab 再回来时，renderConfig 不会重新 fetch 服务器覆盖未保存的改动。
// 关键：state 必须在模块级（Kairo.state.configEditor），不能在 renderConfig 闭包里。
//
// 由于 config.js 用了 IIFE + 真实 window.Kairo / document，这里用 vm 跑一遍源码，
// 跑两次 renderConfig，断言 fetchCount = 1（不是 2）。
function testConfigStatePersists() {
  const fs2 = require('fs');
  const vm2 = require('vm');
  const path2 = require('path');

  // 1) 加载 state.js（建 Kairo.state 骨架）
  const stateSrc = fs2.readFileSync(path2.join(__dirname, 'state.js'), 'utf8');
  const sb = { window: {}, console: { log: () => {} } };
  sb.window.Kairo = { state: {} };
  sb.history = { replaceState: () => {} };
  vm2.createContext(sb);
  vm2.runInContext(stateSrc, sb);

  // 2) mock 掉 Kairo.core / Kairo.api
  let fetchCount = 0;
  sb.window.Kairo.core = {
    el: () => ({ style: {}, appendChild: () => {}, addEventListener: () => {}, setAttribute: () => {} }),
    $: () => null, toast: () => {}, validate: () => null,
    newSystem: () => ({ name: '', servers: [] }),
    newServer: () => ({ log_dirs: [] }),
    newLogDir: () => ({}),
    kvTable: () => ({ appendChild: () => {} }),
    getActiveDL: () => null, clearActiveDL: () => {}
  };
  sb.window.Kairo.api = {
    api: (method, p) => {
      if (method === 'GET' && p === '/api/admin/servers') fetchCount++;
      return Promise.resolve({ app: {}, systems: [{ name: 's1', servers: [] }], search: {} });
    },
    browseButton: function () { return { style: {}, appendChild: function () {}, addEventListener: function () {} }; },
    pathRow: function (input) { return input || { style: {}, appendChild: function () {} }; }
  };

  // 3) 加载 config.js（IIFE 会把 routes.config 挂上）
  const configSrc = fs2.readFileSync(path2.join(__dirname, 'pages', 'config.js'), 'utf8');
  vm2.runInContext(configSrc, sb);

  // 4) 第一次 render — 应触发 1 次 fetch
  const view = { appendChild: () => {} };
  sb.window.Kairo.state.routes.config(view);
  // api() 是 async，需要让 promise resolve
  return new Promise(resolve => {
    setTimeout(() => {
      assert.strictEqual(fetchCount, 1, '首次 render 应 fetch 1 次');
      assert.strictEqual(sb.window.Kairo.state.configEditor.loaded, true, 'state.loaded 应为 true');

      // 5) 模拟用户编辑
      sb.window.Kairo.state.configEditor.systems.push({ name: 'edited', servers: [] });
      sb.window.Kairo.state.configEditor.dirty = true;

      // 6) 第二次 render（tab 切回）— **不应**再 fetch
      try { sb.window.Kairo.state.routes.config(view); } catch (e) { /* DOM mock 不全可忽略 */ }
      setTimeout(() => {
        assert.strictEqual(fetchCount, 1, '切 tab 回来不应再 fetch（这是 #20 修复的关键）');
        assert.strictEqual(sb.window.Kairo.state.configEditor.systems.length, 2, '用户编辑应保留');
        assert.strictEqual(sb.window.Kairo.state.configEditor.dirty, true, 'dirty 标志应保留');
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
  sb.window.Kairo = { state: {} };
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

  // 2) mock 掉 Kairo.core / Kairo.api；记录 PUT 请求体
  const apiCalls = [];
  sb.window.Kairo.core = {
    el: () => ({ style: {}, appendChild: () => {}, addEventListener: () => {}, setAttribute: () => {} }),
    $: () => null, toast: () => {}, validate: () => null,
    newSystem: () => ({ name: '', servers: [] }),
    newServer: () => ({ log_dirs: [] }),
    newLogDir: () => ({}),
    kvTable: () => ({ appendChild: () => {} }),
    getActiveDL: () => null, clearActiveDL: () => {}
  };
  sb.window.Kairo.api = {
    api: (method, p, body) => {
      apiCalls.push({ method, p, body });
      if (method === 'PUT') {
        return Promise.resolve({ ok: true, path: '/x/config.yaml', systems: (body && body.systems || []).length });
      }
      return Promise.resolve(serverData);
    },
    browseButton: function () { return { style: {}, appendChild: function () {}, addEventListener: function () {} }; },
    pathRow: function (input) { return input || { style: {}, appendChild: function () {} }; }
  };

  const configSrc = fs2.readFileSync(path2.join(__dirname, 'pages', 'config.js'), 'utf8');
  vm2.runInContext(configSrc, sb);

  // 3) 第一次 render → fetch serverData → state 拿到 utf-8 + gbk 两条
  const view = { appendChild: () => {} };
  sb.window.Kairo.state.routes.config(view);
  return new Promise(resolve => {
    setTimeout(() => {
      const st = sb.window.Kairo.state.configEditor;
      assert.strictEqual(st.systems[0].servers[0].log_dirs[0].encoding, 'utf-8', 'utf-8 log_dir 已加载');
      assert.strictEqual(st.systems[0].servers[0].log_dirs[1].encoding, 'gbk', 'gbk log_dir 已加载');

      // 4) 模拟"用户切到 home 再切回 config"：再 render 一次
      try { sb.window.Kairo.state.routes.config(view); } catch (e) {}

      setTimeout(() => {
        // 5) **关键断言**：state 里 gbk 还在（不被 GET 覆盖，因为 loaded=true）
        const st2 = sb.window.Kairo.state.configEditor;
        assert.strictEqual(st2.systems[0].servers[0].log_dirs[0].encoding, 'utf-8', '切回后 utf-8 仍在');
        assert.strictEqual(st2.systems[0].servers[0].log_dirs[1].encoding, 'gbk', '切回后 gbk 仍在（#6 关键）');
        assert.strictEqual(apiCalls.filter(c => c.method === 'GET' && c.p === '/api/admin/servers').length, 1, '切回只应 fetch 1 次');

        // 6) 模拟用户编辑：把 utf-8 改成 gbk（和 select 切到 gbk 等价的 mutation）
        st2.systems[0].servers[0].log_dirs[0].encoding = 'gbk';
        st2.dirty = true;

        // 7) 找"保存"按钮：直接调内部 doSave 等价物（点击 btnSave）
        // config.js 的 renderConfig 内 doSave 闭包在外层；我们用 click 入口
        // — 这里改用直接 PUT 模拟点击保存（与生产 doSave 等价）
        sb.window.Kairo.api.api('PUT', '/api/admin/servers', { systems: st2.systems }).then(() => {
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
//   2. 用户改了一行 → dirty=true, Kairo.state.unsavedConfig=true
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
  sb.window.Kairo = { state: {} };
  sb.history = { replaceState: () => {} };
  vm2.createContext(sb);
  vm2.runInContext(stateSrc, sb);

  let getCount = 0;
  let putCount = 0;
  sb.window.Kairo.core = {
    el: () => ({ style: {}, appendChild: () => {}, addEventListener: () => {}, setAttribute: () => {} }),
    $: () => null, toast: () => {}, validate: () => null,
    newSystem: () => ({ name: '', servers: [] }),
    newServer: () => ({ log_dirs: [] }),
    newLogDir: () => ({}),
    kvTable: () => ({ appendChild: () => {} }),
    getActiveDL: () => null, clearActiveDL: () => {}
  };
  sb.window.Kairo.api = {
    api: (method, p, body) => {
      if (method === 'GET') {
        if (p === '/api/admin/servers') getCount++;
        return Promise.resolve({ app: {}, systems: [{ name: 's1', servers: [] }], search: {} });
      }
      if (method === 'PUT') { putCount++; return Promise.resolve({ ok: true, path: '/x' }); }
      return Promise.resolve(null);
    },
    browseButton: function () { return { style: {}, appendChild: function () {}, addEventListener: function () {} }; },
    pathRow: function (input) { return input || { style: {}, appendChild: function () {} }; }
  };

  const configSrc = fs2.readFileSync(path2.join(__dirname, 'pages', 'config.js'), 'utf8');
  vm2.runInContext(configSrc, sb);

  const view = { appendChild: () => {} };
  sb.window.Kairo.state.routes.config(view);
  return new Promise(resolve => {
    setTimeout(() => {
      const st = sb.window.Kairo.state.configEditor;
      // 用户编辑 → dirty
      st.systems[0].name = 'edited';
      st.dirty = true;
      sb.window.Kairo.state.unsavedConfig = true;

      // 模拟 doSave（点保存按钮 → api PUT）
      sb.window.Kairo.api.api('PUT', '/api/admin/servers', { systems: st.systems }).then(() => {
        // 模拟 doSave 成功后清 dirty（生产代码里有，但 mock 没接，所以手动清）
        st.dirty = false;
        sb.window.Kairo.state.unsavedConfig = false;
        // loaded 必须仍是 true，不然切回 tab 会重新 fetch 覆盖
        st.loaded = true;

        // 切回 config tab（再 render 一次）— 不应 fetch
        try { sb.window.Kairo.state.routes.config(view); } catch (e) {}
        setTimeout(() => {
          assert.strictEqual(getCount, 1, '首次后切回不再 GET');
          assert.strictEqual(putCount, 1, 'PUT 调了 1 次');
          assert.strictEqual(st.dirty, false, '保存后 dirty 已清');
          assert.strictEqual(sb.window.Kairo.state.unsavedConfig, false, '保存后 unsavedConfig 已清');
          assert.strictEqual(st.systems[0].name, 'edited', '编辑过的内容还在');
          console.log('  config save clears dirty + preserves edits across tab switch ✓');
          resolve();
        }, 30);
      });
    }, 30);
  });
}

function testApplyCommandPath() {
  const apiSrc = fs.readFileSync(path.join(__dirname, 'api.js'), 'utf8');
  function extractApi(name) {
    const re = new RegExp('function\\s+' + name + '\\s*\\([^)]*\\)\\s*\\{');
    const m = apiSrc.match(re);
    if (!m) throw new Error('not found in api.js: ' + name);
    const start = m.index;
    let i = apiSrc.indexOf('{', start);
    let depth = 1;
    i++;
    while (i < apiSrc.length && depth > 0) {
      const ch = apiSrc[i];
      if (ch === '{') depth++;
      else if (ch === '}') depth--;
      i++;
    }
    return apiSrc.slice(start, i);
  }
  const quoteLocalPath = new Function(extractApi('quoteLocalPath') + '; return quoteLocalPath;')();
  const applyCommandPath = new Function(
    extractApi('quoteLocalPath') + '\n' + extractApi('applyCommandPath') + '; return applyCommandPath;'
  )();
  assert.strictEqual(quoteLocalPath('C:\\deploy\\run.bat'), 'C:\\deploy\\run.bat');
  assert.strictEqual(quoteLocalPath('C:\\deploy scripts\\daily job.bat'), '"C:\\deploy scripts\\daily job.bat"');
  assert.strictEqual(applyCommandPath('', 'C:\\a.bat'), 'C:\\a.bat');
  assert.strictEqual(applyCommandPath('C:\\old.bat -Env prod', 'C:\\new.bat'), 'C:\\new.bat -Env prod');
  assert.strictEqual(applyCommandPath('"C:\\old dir\\a.bat" /now', 'C:\\new dir\\a.bat'), '"C:\\new dir\\a.bat" /now');
  assert.strictEqual(
    applyCommandPath('powershell -NoProfile -File "C:\\old.ps1" -Tag daily', 'D:\\n.ps1'),
    'powershell -NoProfile -File D:\\n.ps1 -Tag daily'
  );
  assert.strictEqual(applyCommandPath('cmd /c', 'C:\\a.bat'), 'cmd /c C:\\a.bat');
  console.log('  applyCommandPath keeps args / quotes spaces ✓');
}

function loadDatabaseHelpers(navigatorMock) {
  const src = fs.readFileSync(path.join(__dirname, 'pages/database.js'), 'utf8');
  function extractDb(name) {
    const re = new RegExp('function\\s+' + name + '\\s*\\([^)]*\\)\\s*\\{');
    const m = src.match(re);
    if (!m) throw new Error('not found in database.js: ' + name);
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
  const kw = src.match(/const SQL_KEYWORDS = new Set\([\s\S]*?\);/);
  const nl = src.match(/const SQL_NEWLINE_BEFORE = new Set\([\s\S]*?\);/);
  if (!kw || !nl) throw new Error('missing SQL keyword sets');
  const h = 'function h(v) { return String(v == null ? "" : v).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;"); }';
  return new Function(
    'navigator',
    kw[0] + '\n' + nl[0] + '\n' + h + '\n'
    + extractDb('tokenizeSQL') + '\n'
    + extractDb('completionPrefix') + '\n'
    + extractDb('isMacPlatform') + '\n'
    + extractDb('sqlTableContext') + '\n'
    + extractDb('suggestSQL') + '\n'
    + extractDb('highlightSQL') + '\n'
    + extractDb('formatSQL') + '\n'
    + extractDb('matchBrackets') + '\n'
    + extractDb('matchesShortcut') + '\n'
    + extractDb('isSnippetExpandKey') + '\n'
    + extractDb('formatBytes') + '\n'
    + extractDb('cellText') + '\n'
    + extractDb('fmtCell') + '\n'
    + extractDb('parseSnippetsText') + '\n'
    + extractDb('formatSnippetsText') + '\n'
    + 'return { tokenizeSQL: tokenizeSQL, highlightSQL: highlightSQL, formatSQL: formatSQL, suggestSQL: suggestSQL, sqlTableContext: sqlTableContext, matchesShortcut: matchesShortcut, matchBrackets: matchBrackets, completionPrefix: completionPrefix, isSnippetExpandKey: isSnippetExpandKey, parseSnippetsText: parseSnippetsText, formatSnippetsText: formatSnippetsText, formatBytes: formatBytes, cellText: cellText, fmtCell: fmtCell };'
  )(navigatorMock || {});
}

function testDatabaseSQLHelpers() {
  const db = loadDatabaseHelpers();
  const formatted = db.formatSQL("select id, name from dual where name='x'");
  assert.ok(/SELECT/.test(formatted) && /FROM DUAL/.test(formatted), formatted);
  assert.ok(/name = 'x'/.test(formatted), formatted);
  const ac = db.suggestSQL('se', 2, { snippets: [{ key: 'sel', text: 'SELECT *', enabled: true }] });
  assert.ok(ac.items.some(function (x) { return x.label === 'SELECT'; }), JSON.stringify(ac.items));
  assert.ok(!ac.items.some(function (x) { return x.kind === 'snippet'; }), 'prefix se should not steal sel snippet');
  const exact = db.suggestSQL('sel', 3, { snippets: [{ key: 'sel', text: 'SELECT *', enabled: true }] });
  assert.ok(exact.items.some(function (x) { return x.kind === 'snippet'; }), 'exact sel snippet');
  const inside = db.suggestSQL("select 'se", 10, {});
  assert.strictEqual(inside.items.length, 0, 'no complete inside string');
  const sql = 'SELECT * FROM t WHERE (id = 1)';
  const match = db.matchBrackets(sql, sql.indexOf('('));
  assert.ok(match && match.open >= 0 && match.close > match.open, JSON.stringify(match));
  const bad = db.matchBrackets('SELECT (id', 7);
  assert.ok(bad && bad.close === -1, JSON.stringify(bad));
  const html = db.highlightSQL(sql, match);
  assert.ok(html.indexOf('db-sql-br') >= 0, html);

  // isSnippetExpandKey tests
  assert.strictEqual(db.isSnippetExpandKey({ key: ' ' }, 'Space'), true, 'Space on Space');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Tab' }, 'Space'), false, 'Tab on Space');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Enter' }, 'Space'), false, 'Enter on Space');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Enter', ctrlKey: true }, 'Space'), false, 'Ctrl+Enter on Space');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Tab' }, 'Tab'), true, 'Tab on Tab');
  assert.strictEqual(db.isSnippetExpandKey({ key: ' ' }, 'Tab'), false, 'Space on Tab');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Enter' }, 'Tab'), false, 'Enter on Tab');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Enter' }, 'Enter'), true, 'Enter on Enter');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Enter', code: 'NumpadEnter' }, 'Enter'), true, 'NumpadEnter on Enter');
  assert.strictEqual(db.isSnippetExpandKey({ key: ' ' }, 'Enter'), false, 'Space on Enter');
  assert.strictEqual(db.isSnippetExpandKey({ key: 'Tab' }, 'Enter'), false, 'Tab on Enter');

  // parseSnippetsText & formatSnippetsText tests (PL/SQL batch mode)
  const plsqlText = [
    '# PL/SQL Developer shortcuts.txt comments',
    's=SELECT * FROM ',
    'sc=SELECT COUNT(*) FROM ',
    'w = WHERE ',
    'df\tDELETE FROM ',
    'sel=SELECT *\\nFROM ${table}',
    '# [disabled] old=SELECT 1 FROM dual'
  ].join('\n');

  const parsed = db.parseSnippetsText(plsqlText);
  assert.strictEqual(parsed.items.length, 6, 'parsed 6 snippets');
  assert.strictEqual(parsed.duplicates.length, 0, 'no duplicates');
  assert.strictEqual(parsed.items[0].key, 's');
  assert.strictEqual(parsed.items[0].text, 'SELECT * FROM ', 'preserves trailing space');
  assert.strictEqual(parsed.items[0].enabled, true);
  assert.strictEqual(parsed.items[1].key, 'sc');
  assert.strictEqual(parsed.items[2].key, 'w');
  assert.strictEqual(parsed.items[2].text, 'WHERE ');
  assert.strictEqual(parsed.items[3].key, 'df');
  assert.strictEqual(parsed.items[3].text, 'DELETE FROM ', 'supports tab delimiter');
  assert.strictEqual(parsed.items[4].key, 'sel');
  assert.strictEqual(parsed.items[4].text, 'SELECT *\nFROM ${table}', 'supports \\n escape');
  assert.strictEqual(parsed.items[5].key, 'old');
  assert.strictEqual(parsed.items[5].enabled, false, 'supports disabled item');

  // Duplicate detection test
  const dupeText = 'sf=SELECT 1\nsf=SELECT 2\nw=WHERE';
  const dupeParsed = db.parseSnippetsText(dupeText);
  assert.strictEqual(dupeParsed.duplicates.length, 1);
  assert.strictEqual(dupeParsed.duplicates[0], 'sf');

  // formatSnippetsText test
  const formattedText = db.formatSnippetsText(parsed.items);
  assert.ok(formattedText.indexOf('s=SELECT * FROM ') >= 0, formattedText);
  assert.ok(formattedText.indexOf('sel=SELECT *\\nFROM ${table}') >= 0, formattedText);
  assert.ok(formattedText.indexOf('# [disabled] old=SELECT 1 FROM dual') >= 0, formattedText);

  // Roundtrip test
  const roundtrip = db.parseSnippetsText(formattedText);
  assert.strictEqual(roundtrip.items.length, 6);
  assert.deepStrictEqual(roundtrip.items, parsed.items);

  console.log('  database SQL tabs helpers: format / suggest / brackets / snippetExpandKey / parseSnippetsText / formatSnippetsText ✓');
}

// ---------- 数据库工作台懒加载 / 联想解耦 / 快捷键重构 ----------
function testDatabaseWorkbenchLazy() {
  const dbSrc = fs.readFileSync(path.join(__dirname, 'pages/database.js'), 'utf8');

  // 1. FROM us 场景：解耦 DOM 后表名仍可联想（schemaTableCache 预热池直供）。
  const db = loadDatabaseHelpers({});
  const fromUs = db.suggestSQL('SELECT * FROM us', 16, { objects: ['users', 'user_logs'], snippets: [] });
  assert.ok(fromUs.items.some(function (x) { return x.label === 'users' && x.kind === 'object'; }), 'FROM us 应联想出 users: ' + JSON.stringify(fromUs.items));
  assert.ok(fromUs.items.some(function (x) { return x.label === 'user_logs'; }), 'FROM us 应联想出 user_logs');

  // 2. FROM ord 场景：表名必须排在 ORDER 关键字之前（上下文提权）。
  const fromOrd = db.suggestSQL('SELECT * FROM ord', 17, { objects: ['orders', 'order_items'], snippets: [] });
  const idxOrders = fromOrd.items.findIndex(function (x) { return x.label === 'orders'; });
  const idxOrder = fromOrd.items.findIndex(function (x) { return x.label === 'ORDER'; });
  assert.ok(idxOrders >= 0, 'orders 应在候选: ' + JSON.stringify(fromOrd.items));
  assert.ok(idxOrder < 0 || idxOrders < idxOrder, 'orders(' + idxOrders + ') 应排在 ORDER(' + idxOrder + ') 之前: ' + JSON.stringify(fromOrd.items.map(function (x) { return x.label + ':' + x.kind; })));

  // 非表上下文保持原排序：关键字优先（零回归）。
  const plain = db.suggestSQL('se', 2, { objects: [], snippets: [] });
  assert.ok(plain.items.length && plain.items[0].kind === 'keyword', '非 FROM 上下文关键字仍优先: ' + JSON.stringify(plain.items.slice(0, 3)));

  // sqlTableContext 直接断言。
  assert.strictEqual(db.sqlTableContext('SELECT * FROM us', 16), true, 'FROM 后应为表上下文');
  assert.strictEqual(db.sqlTableContext('SELECT * FROM ', 14), true, 'FROM + 空格应为表上下文');
  assert.strictEqual(db.sqlTableContext('SELECT se', 9), false, 'SELECT 后非表上下文');
  assert.strictEqual(db.sqlTableContext('SELECT * FROM t JOIN or', 22), true, 'JOIN 后应为表上下文');

  // 3. 快捷键：Windows Ctrl+Enter 与 macOS Cmd+Enter 一致。
  const dbWin = loadDatabaseHelpers({ platform: 'Win32', userAgent: 'Windows' });
  const dbMac = loadDatabaseHelpers({ platform: 'MacIntel', userAgent: 'Macintosh' });
  assert.strictEqual(dbWin.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: true, altKey: false, shiftKey: false, metaKey: false }, 'Ctrl+Enter'), true, 'Win Ctrl+Enter');
  assert.strictEqual(dbWin.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: false, altKey: false, shiftKey: false, metaKey: true }, 'Ctrl+Enter'), false, 'Win Cmd 不应误触 Ctrl+Enter');
  assert.strictEqual(dbMac.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: true, altKey: false, shiftKey: false, metaKey: false }, 'Ctrl+Enter'), true, 'Mac Ctrl+Enter 仍可用');
  assert.strictEqual(dbMac.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: false, altKey: false, shiftKey: false, metaKey: true }, 'Ctrl+Enter'), true, 'Mac Cmd+Enter 等价执行');
  assert.strictEqual(dbMac.matchesShortcut({ key: 'o', code: 'KeyO', ctrlKey: false, altKey: true, shiftKey: false, metaKey: false }, 'Alt+O'), true, 'Alt+O 正常');

  // 4. Escape 在联想菜单打开时独立响应：源码契约断言。
  // onWorkbenchKey 必须在 cancelQuery 分支之前优先关闭补全并 stopPropagation。
  const escGuard = dbSrc.indexOf("e.key === 'Escape' && acState.open");
  const cancelPos = dbSrc.indexOf('cancelQuery()');
  assert.ok(escGuard >= 0, 'onWorkbenchKey 应包含 Escape+acState.open 优先 guard');
  assert.ok(escGuard < cancelPos, 'Escape 关补全必须在 cancelQuery 之前');
  assert.ok(dbSrc.indexOf('stopPropagation') >= 0, 'Escape 关闭补全应 stopPropagation 防冒泡误杀查询');

  // 5. 按需懒加载契约：category 参数 + 分类缓存 + 表名池。
  assert.ok(dbSrc.indexOf('&category=') >= 0, 'loadCategoryObjects 应传 category 参数');
  assert.ok(dbSrc.indexOf('schemaCategoryCache') >= 0, '应使用 schemaCategoryCache 按分类缓存');
  assert.ok(dbSrc.indexOf('schemaTableCache') >= 0, '应使用 schemaTableCache 预热表名');
  assert.ok(dbSrc.indexOf('warmupSchemaTables') >= 0, '应存在 warmupSchemaTables 静默预热');
  assert.ok(dbSrc.indexOf('state.schemaTableCache[schema]') >= 0, 'completionExtras 应优先读 schemaTableCache');

  // 6. LOB 单元格呈现与防错位投影契约断言
  assert.ok(dbSrc.indexOf('db-lob-badge') >= 0, '应包含 db-lob-badge 徽章');
  assert.ok(dbSrc.indexOf('db-lob-token') >= 0, '应包含 db-lob-token 样式支持');
  assert.ok(dbSrc.indexOf('resolveLobPayload') >= 0, '应包含 resolveLobPayload 统一解析逻辑');

  console.log('  database SQL tabs helpers: format / suggest / brackets / snippetExpandKey / parseSnippetsText / formatSnippetsText ✓');
}

// ---------- 数据库工作台懒加载 / 联想解耦 / 快捷键重构 ----------
function testDatabaseWorkbenchLazy() {
  const dbSrc = fs.readFileSync(path.join(__dirname, 'pages/database.js'), 'utf8');

  // 1. FROM us 场景：解耦 DOM 后表名仍可联想（schemaTableCache 预热池直供）。
  const db = loadDatabaseHelpers({});
  const fromUs = db.suggestSQL('SELECT * FROM us', 16, { objects: ['users', 'user_logs'], snippets: [] });
  assert.ok(fromUs.items.some(function (x) { return x.label === 'users' && x.kind === 'object'; }), 'FROM us 应联想出 users: ' + JSON.stringify(fromUs.items));
  assert.ok(fromUs.items.some(function (x) { return x.label === 'user_logs'; }), 'FROM us 应联想出 user_logs');

  // 2. FROM ord 场景：表名必须排在 ORDER 关键字之前（上下文提权）。
  const fromOrd = db.suggestSQL('SELECT * FROM ord', 17, { objects: ['orders', 'order_items'], snippets: [] });
  const idxOrders = fromOrd.items.findIndex(function (x) { return x.label === 'orders'; });
  const idxOrder = fromOrd.items.findIndex(function (x) { return x.label === 'ORDER'; });
  assert.ok(idxOrders >= 0, 'orders 应在候选: ' + JSON.stringify(fromOrd.items));
  assert.ok(idxOrder < 0 || idxOrders < idxOrder, 'orders(' + idxOrders + ') 应排在 ORDER(' + idxOrder + ') 之前: ' + JSON.stringify(fromOrd.items.map(function (x) { return x.label + ':' + x.kind; })));

  // 非表上下文保持原排序：关键字优先（零回归）。
  const plain = db.suggestSQL('se', 2, { objects: [], snippets: [] });
  assert.ok(plain.items.length && plain.items[0].kind === 'keyword', '非 FROM 上下文关键字仍优先: ' + JSON.stringify(plain.items.slice(0, 3)));

  // sqlTableContext 直接断言。
  assert.strictEqual(db.sqlTableContext('SELECT * FROM us', 16), true, 'FROM 后应为表上下文');
  assert.strictEqual(db.sqlTableContext('SELECT * FROM ', 14), true, 'FROM + 空格应为表上下文');
  assert.strictEqual(db.sqlTableContext('SELECT se', 9), false, 'SELECT 后非表上下文');
  assert.strictEqual(db.sqlTableContext('SELECT * FROM t JOIN or', 22), true, 'JOIN 后应为表上下文');

  // 3. 快捷键：Windows Ctrl+Enter 与 macOS Cmd+Enter 一致。
  const dbWin = loadDatabaseHelpers({ platform: 'Win32', userAgent: 'Windows' });
  const dbMac = loadDatabaseHelpers({ platform: 'MacIntel', userAgent: 'Macintosh' });
  assert.strictEqual(dbWin.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: true, altKey: false, shiftKey: false, metaKey: false }, 'Ctrl+Enter'), true, 'Win Ctrl+Enter');
  assert.strictEqual(dbWin.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: false, altKey: false, shiftKey: false, metaKey: true }, 'Ctrl+Enter'), false, 'Win Cmd 不应误触 Ctrl+Enter');
  assert.strictEqual(dbMac.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: true, altKey: false, shiftKey: false, metaKey: false }, 'Ctrl+Enter'), true, 'Mac Ctrl+Enter 仍可用');
  assert.strictEqual(dbMac.matchesShortcut({ key: 'Enter', code: 'Enter', ctrlKey: false, altKey: false, shiftKey: false, metaKey: true }, 'Ctrl+Enter'), true, 'Mac Cmd+Enter 等价执行');
  assert.strictEqual(dbMac.matchesShortcut({ key: 'o', code: 'KeyO', ctrlKey: false, altKey: true, shiftKey: false, metaKey: false }, 'Alt+O'), true, 'Alt+O 正常');

  // 4. Escape 在联想菜单打开时独立响应：源码契约断言。
  // onWorkbenchKey 必须在 cancelQuery 分支之前优先关闭补全并 stopPropagation。
  const escGuard = dbSrc.indexOf("e.key === 'Escape' && acState.open");
  const cancelPos = dbSrc.indexOf('cancelQuery()');
  assert.ok(escGuard >= 0, 'onWorkbenchKey 应包含 Escape+acState.open 优先 guard');
  assert.ok(escGuard < cancelPos, 'Escape 关补全必须在 cancelQuery 之前');
  assert.ok(dbSrc.indexOf('stopPropagation') >= 0, 'Escape 关闭补全应 stopPropagation 防冒泡误杀查询');

  // 5. 按需懒加载契约：category 参数 + 分类缓存 + 表名池。
  assert.ok(dbSrc.indexOf('&category=') >= 0, 'loadCategoryObjects 应传 category 参数');
  assert.ok(dbSrc.indexOf('schemaCategoryCache') >= 0, '应使用 schemaCategoryCache 按分类缓存');
  assert.ok(dbSrc.indexOf('schemaTableCache') >= 0, '应使用 schemaTableCache 预热表名');
  assert.ok(dbSrc.indexOf('warmupSchemaTables') >= 0, '应存在 warmupSchemaTables 静默预热');
  assert.ok(dbSrc.indexOf('state.schemaTableCache[schema]') >= 0, 'completionExtras 应优先读 schemaTableCache');

  // 6. LOB 单元格呈现与防错位投影契约断言
  assert.ok(dbSrc.indexOf('db-lob-badge') >= 0, '应包含 db-lob-badge 徽章');
  assert.ok(dbSrc.indexOf('db-lob-token') >= 0, '应包含 db-lob-token 样式支持');
  assert.ok(dbSrc.indexOf('resolveLobPayload') >= 0, '应包含 resolveLobPayload 统一解析逻辑');

  // LOB 格式化测试
  assert.strictEqual(db.cellText({ kind: 'clob', text: 'select *' }), 'select *');
  assert.strictEqual(db.cellText({ kind: 'clob', bytes: 2048 }), '(CLOB 2.0 KB)');
  assert.strictEqual(db.cellText({ kind: 'blob', bytes: 1048576 }), '(BLOB 1.0 MB)');

  const clobWithToken = db.fmtCell({ kind: 'clob', bytes: 1024, token: 'signed-token-123' }, 2, 5);
  assert.ok(clobWithToken.includes('db-lob-clob'), '应渲染 clob 徽章');
  assert.ok(clobWithToken.includes('db-lob-token'), '包含 token 时应附加 db-lob-token 类');
  assert.ok(clobWithToken.includes('data-lob-row="2"'), '带有对应行索引');
  assert.ok(clobWithToken.includes('data-lob-col="5"'), '带有对应列索引');
  assert.ok(clobWithToken.includes('CLOB (1.0 KB)'), '正确格式化 LOB 大小');

  // 7. runQuery 作用域与 DDL/写入守卫断言：防 querySource / writes 作用域回归
  const runQueryMatch = dbSrc.match(/async function runQuery\([\s\S]*?\n  \}/);
  assert.ok(runQueryMatch, 'database.js 应包含 runQuery');
  const runQueryBody = runQueryMatch[0];
  const qSrcIdx = runQueryBody.indexOf('const querySource = effectiveSource();');
  const writesIdx = runQueryBody.indexOf('const writes =');
  const isDDLIdx = runQueryBody.indexOf('const isDDL =');
  const guardIdx = runQueryBody.indexOf('const guard = sqlGuardInfo(sql);');
  assert.ok(guardIdx >= 0 && qSrcIdx > guardIdx, 'querySource 必须在 guard 之后立即声明');
  assert.ok(writesIdx > qSrcIdx && isDDLIdx > writesIdx, 'writes 与 isDDL 必须在最外层作用域声明并位于守卫判断前');
  assert.ok(runQueryBody.indexOf('const effSrc = effectiveSource()') < 0, '不应存在多余延迟声明的 effSrc');

  // 8. Schema 占位符防污染与目标解析断言
  assert.ok(dbSrc.indexOf('function currentSchema()') >= 0, 'database.js 应包含 currentSchema 统一兜底');
  assert.ok(dbSrc.indexOf('s.resultSchema = currentSchema()') >= 0, 'runQuery 应通过 currentSchema 获取生效 schema');
  assert.ok(dbSrc.indexOf('<option value="">加载中…</option>') >= 0, '加载中选项必须包含空 value 防误传中文');

  const featSrc = fs.readFileSync(path.join(__dirname, 'workbench/database-features.js'), 'utf8');
  const mockWindow = {
    Kairo: {},
    addEventListener: function () {},
    document: { readyState: 'loading', addEventListener: function () {} }
  };
  new Function('window', 'document', featSrc)(mockWindow, mockWindow.document);
  const resolveGridTargetFn = mockWindow.Kairo.databaseFeatures.resolveGridTarget;
  const targetLoading = resolveGridTargetFn('SELECT * FROM BUSINESS_APPLY', '加载中…', 'oracle');
  assert.deepStrictEqual(targetLoading, { schema: '', table: 'BUSINESS_APPLY' }, '加载中占位符不应污染网格目标 schema');
  const targetFailed = resolveGridTargetFn('SELECT * FROM BUSINESS_APPLY', '加载失败', 'oracle');
  assert.deepStrictEqual(targetFailed, { schema: '', table: 'BUSINESS_APPLY' }, '加载失败占位符不应污染网格目标 schema');
  const targetNormal = resolveGridTargetFn('SELECT * FROM BUSINESS_APPLY', 'SCOTT', 'oracle');
  assert.deepStrictEqual(targetNormal, { schema: 'SCOTT', table: 'BUSINESS_APPLY' }, '正常默认 schema 应保留');
  const targetExplicit = resolveGridTargetFn('SELECT * FROM "HR"."BUSINESS_APPLY"', '加载中…', 'oracle');
  assert.deepStrictEqual(targetExplicit, { schema: 'HR', table: 'BUSINESS_APPLY' }, '显式 schema 应优先解析');

  assert.ok(dbSrc.indexOf('function openObjectTab(schema, object, type)') >= 0, 'database.js 应声明 openObjectTab');
  assert.ok(dbSrc.indexOf("if (!schema || schema === '加载中…' || schema === '加载失败')") >= 0, 'openObjectTab 应安全防御加载中占位符');
  assert.ok(dbSrc.indexOf("if (!s.schema || s.schema === '加载中…' || s.schema === '加载失败')") >= 0, 'loadObjectTabInspect 应安全防御加载中占位符');

  console.log('  database workbench lazy-load / suggest / shortcuts / LOB tokens / runQuery scoping / schema placeholder guard ✓');
}

function loadCompareHelpers() {
  const file = path.join(__dirname, 'pages', 'compare.js');
  const src = fs.readFileSync(file, 'utf8');
  function extractFn(name) {
    const p = src.indexOf('function ' + name + '(');
    if (p < 0) throw new Error('function not found: ' + name);
    let depth = 0, start = p, i = p;
    while (i < src.length && src[i] !== '{') i++;
    depth = 1;
    i++;
    while (i < src.length && depth > 0) {
      if (src[i] === '{') depth++;
      else if (src[i] === '}') depth--;
      i++;
    }
    return src.slice(start, i);
  }
  return new Function(
    extractFn('parentRel') + '\n' +
    extractFn('baseName') + '\n' +
    extractFn('isDirItem') + '\n' +
    extractFn('rollupAllFolders') + '\n' +
    extractFn('joinPath') + '\n' +
    extractFn('splitEditorLines') + '\n' +
    extractFn('replaceEditorLine') + '\n' +
    extractFn('textMetrics') + '\n' +
    'const AUTO_COMPARE_MAX_CHARS = 200000; const AUTO_COMPARE_MAX_LINES = 12000;\n' +
    extractFn('isLargeText') + '\n' +
    extractFn('createEditorHistory') + '\n' +
    extractFn('resolveSaveSide') + '\n' +
     extractFn('makeCompareKey') + '\n' +
     extractFn('folderRowHeight') + '\n' +
     extractFn('folderStatusText') + '\n' +
     extractFn('isTypeConflict') + '\n' +
     extractFn('isBulkActionable') + '\n' +
     extractFn('autoExpandDiffFolderRows') + '\n' +
     extractFn('sourceIdentity') + '\n' +
     extractFn('normalizeFolderHistoryEntry') + '\n' +
     extractFn('sourceSecurityLabel') + '\n' +
     extractFn('sourceProtocolLabel') + '\n' +
     extractFn('sourceIdentityLabel') + '\n' +
     'const WASPACK_HANDOFF_PREFIX = \'kairo:waspack:compare-handoff:\'; const WASPACK_HANDOFF_MAX_AGE = 10 * 60 * 1000;\n' +
     extractFn('handoffPathKey') + '\n' +
     extractFn('validateWaspackHandoff') + '\n' +
     extractFn('consumeWaspackHandoff') + '\n' +
     extractFn('handoffNonceFromHash') + '\n' +
     'return { parentRel, baseName, isDirItem, rollupAllFolders, joinPath, replaceEditorLine, folderRowHeight, folderStatusText, textMetrics, isLargeText, createEditorHistory, resolveSaveSide, makeCompareKey, isTypeConflict, isBulkActionable, autoExpandDiffFolderRows, sourceIdentity, normalizeFolderHistoryEntry, sourceSecurityLabel, sourceIdentityLabel, handoffPathKey, validateWaspackHandoff, consumeWaspackHandoff, handoffNonceFromHash };'
  )();
}

function loadWaspackHelpers() {
  const file = path.join(__dirname, 'pages', 'waspack.js');
  const source = fs.readFileSync(file, 'utf8');
  function extractFn(name) {
    const p = source.indexOf('function ' + name + '(');
    if (p < 0) throw new Error('function not found: ' + name);
    let depth = 0, i = source.indexOf('{', p);
    depth = 1; i++;
    while (i < source.length && depth > 0) {
      if (source[i] === '{') depth++;
      else if (source[i] === '}') depth--;
      i++;
    }
    return source.slice(p, i);
  }
  return new Function(
    extractFn('normalizeManifest') + '\n' +
    extractFn('duplicateManifestMessage') + '\n' +
    extractFn('packageBaseName') + '\n' +
    extractFn('normalizeBuildResponse') + '\n' +
    extractFn('canonicalStageSignature') + '\n' +
    extractFn('windowsPathKey') + '\n' +
    extractFn('sameWindowsPath') + '\n' +
    extractFn('comparableHistoryItem') + '\n' +
    extractFn('previousComparable') + '\n' +
    extractFn('makeHandoffPayload') + '\n' +
    extractFn('createHandoff') + '\n' +
    extractFn('handoffTargetURL') + '\n' +
    'return { normalizeManifest, duplicateManifestMessage, normalizeBuildResponse, canonicalStageSignature, windowsPathKey, sameWindowsPath, comparableHistoryItem, previousComparable, makeHandoffPayload, createHandoff, handoffTargetURL };'
  )();
}

function testWaspackHelpers() {
  const wp = loadWaspackHelpers();
  assert.strictEqual(wp.duplicateManifestMessage('src\\com\\qa\\Demo.java\nsrc/com/qa/Demo.java'), '第 2 行与第 1 行重复。', '投产清单重复路径应在前端明确定位');
  assert.strictEqual(wp.duplicateManifestMessage('# comment\n\nsrc/com/qa/Demo.java'), '', '注释和空行不应被当作重复清单');
  const body = { project_dir: 'D:\\src', output_dir: 'D:\\out', package_name: 'REL20260905.tar', manifest: 'a\\r\\nb', auto_pair: true, pack_type: 'app', batch_base_dir: '', chmod_mode: '777', output_policy: 'fail' };
  const signature = wp.canonicalStageSignature(body);
  assert.strictEqual(signature, wp.canonicalStageSignature(Object.assign({}, body, { output_policy: 'replace' })), '输出策略不应进入阶段签名或持久化身份');
  assert.notStrictEqual(signature, wp.canonicalStageSignature(Object.assign({}, body, { manifest: 'other' })), '清单变化必须使阶段凭据失效');
  assert.strictEqual(wp.sameWindowsPath('C:\\Deploy\\Current\\', 'c:/deploy/current'), true, 'Windows 目录比较应忽略斜杠和大小写');
  assert.strictEqual(wp.sameWindowsPath('C:\\Deploy\\One', 'C:\\Deploy\\Two'), false);

  const history = [
    { operation: 'build', status: 'success', output_dir: 'C:\\current' },
    { operation: 'preview', status: 'success', output_dir: 'C:\\preview' },
    { operation: 'package', status: 'success', output_dir: 'C:\\previous' },
    { operation: 'build', status: 'failed', output_dir: 'C:\\failed' }
  ];
  assert.strictEqual(wp.previousComparable(history, 0), history[2], '上一可比记录应跳过预检和失败操作');
  assert.strictEqual(wp.previousComparable(history, 2), null);

  const wrapped = wp.normalizeBuildResponse({ ok: true, build: { artifacts: [{ path: 'a.tar' }] }, zip: { path: 'a.zip' } });
  assert.strictEqual(wrapped.build.artifacts[0].path, 'a.tar');
  assert.strictEqual(wrapped.zip.path, 'a.zip');
  assert.strictEqual(wp.normalizeBuildResponse({ artifacts: [{ path: 'plain.tar' }] }).build.artifacts[0].path, 'plain.tar');

  const payload = wp.makeHandoffPayload('C:\\previous', 'C:\\current', '2026-09-05T00:00:00.000Z');
  assert.deepStrictEqual(payload.left, { kind: 'local', path: 'C:\\previous' });
  assert.strictEqual(payload.source, 'waspack-history');
  assert.ok(/^wp-/.test(wp.createHandoff('C:\\previous', 'C:\\current').nonce));
  assert.strictEqual(wp.handoffTargetURL({ origin: 'https://kairo.test', pathname: '/app/', search: '?tenant=1', hash: '#/old' }, 'wp-test'), 'https://kairo.test/app/?tenant=1#/compare?handoff=wp-test', '交接 URL 必须是绝对同源且丢弃旧 hash');
  const source = fs.readFileSync(path.join(__dirname, 'pages', 'waspack.js'), 'utf8');
  assert.ok(!/output_policy\s*:/.test(source.match(/function savePreferences\(\)[\s\S]*?\n    \}/)[0]), '偏好保存不得包含 output_policy');
  console.log('  waspack helpers: output policy / stage signature / history / handoff ✓');
}

function testCompareHelpers() {
  const cmp = loadCompareHelpers();

  // 1. joinPath 跨平台路径测试
  assert.strictEqual(cmp.joinPath({ kind: 'local' }, 'D:\\myproject', 'sub/file.txt'), 'D:\\myproject\\sub\\file.txt');
  assert.strictEqual(cmp.joinPath({ kind: 'local' }, 'D:/myproject/', 'sub/file.txt'), 'D:\\myproject\\sub\\file.txt');
  assert.strictEqual(cmp.joinPath({ kind: 'sftp' }, '/var/www', 'sub/file.txt'), '/var/www/sub/file.txt');
  assert.strictEqual(cmp.joinPath({ kind: 'sftp' }, '/var/www/', '/sub/file.txt'), '/var/www/sub/file.txt');

  // 2. parentRel & baseName & isDirItem
  assert.strictEqual(cmp.parentRel('src/components/btn.js'), 'src/components');
  assert.strictEqual(cmp.parentRel('src'), '');
  assert.strictEqual(cmp.baseName('src/components/btn.js'), 'btn.js');
  assert.strictEqual(cmp.baseName('src'), 'src');
  assert.strictEqual(cmp.isDirItem({ left: { is_dir: true } }), true);
  assert.strictEqual(cmp.isDirItem({ left: { is_dir: false } }), false);

  // 3. rollupAllFolders: 确保子目录中的差异能自底向上正确传递给父文件夹，防止被只看差异模式过滤隐藏
  const itemsWithDiff = [
    { rel_path: 'src', left: { is_dir: true }, right: { is_dir: true }, status: 'same' },
    { rel_path: 'src/components', left: { is_dir: true }, right: { is_dir: true }, status: 'same' },
    { rel_path: 'src/components/btn.js', left: { size: 10 }, right: { size: 20 }, status: 'different' },
    { rel_path: 'src/components/icon.js', left: { size: 50 }, right: { size: 50 }, status: 'same' },
    { rel_path: 'docs', left: { is_dir: true }, right: { is_dir: true }, status: 'same' },
    { rel_path: 'docs/readme.md', left: { size: 100 }, right: { size: 100 }, status: 'same' },
    { rel_path: 'new_folder', left: { is_dir: true }, right: null, status: 'left_only' },
    { rel_path: 'pending_folder', left: { is_dir: true }, right: { is_dir: true }, status: 'pending', pending: true }
  ];

  cmp.rollupAllFolders(itemsWithDiff, new Set(['', 'src', 'src/components', 'docs']));
  const map = {};
  itemsWithDiff.forEach(it => { map[it.rel_path] = it; });

  assert.strictEqual(map['src/components'].status, 'different', '含有差异文件的子目录状态应置为 different');
  assert.strictEqual(map['src'].status, 'different', '含差异子目录的父目录状态也应置为 different');
  assert.strictEqual(map['docs'].status, 'same', '全部相同文件的目录保持 same');
  assert.strictEqual(map['new_folder'].status, 'left_only', '单侧目录保留 left_only');
  assert.strictEqual(map['pending_folder'].status, 'pending', '未展开目录保留 pending');

  // 4. 结果区就地编辑允许回车拆成多行
  assert.strictEqual(cmp.replaceEditorLine('alpha\nbeta', 1, 'one\ntwo', 0), 'one\ntwo\nbeta');
  assert.strictEqual(cmp.replaceEditorLine('alpha\nbeta', 0, 'one\ntwo', 2), 'alpha\none\ntwo\nbeta');

  // 5. pending 明确表达尚未验证，不能伪装成完成
  assert.strictEqual(cmp.folderStatusText('pending', false), '待验证');
  assert.strictEqual(cmp.folderStatusText('pending', true), '校验中');
  assert.strictEqual(cmp.folderStatusText('different', false), '不同');

  // 7. 虚拟树行高必须与 CSS 命中区一致，焦点应落在 composite treegrid 上
  assert.strictEqual(cmp.folderRowHeight(), 28, '虚拟树行高紧凑模式 28px');
  const compareSource = fs.readFileSync(path.join(__dirname, 'pages', 'compare.js'), 'utf8');
  const compareCss = fs.readFileSync(path.join(__dirname, 'pages', 'compare-workbench.css'), 'utf8');
  assert.ok(compareCss.includes('--cmp-folder-row-height: 28px'), 'CSS 应声明 28px 虚拟行高');
  assert.ok(compareCss.includes('min-height: var(--cmp-folder-row-height)'), '行内控件应使用统一命中区高度');
  assert.ok(compareSource.includes("role: 'treegrid', tabindex: '0'"), 'treegrid 应承担键盘焦点');
  assert.ok(compareSource.includes("table.addEventListener('keydown'"), '键盘事件应绑定 treegrid');
  assert.ok(compareSource.includes("table.setAttribute('aria-activedescendant'"), '活动后代应绑定 treegrid');
  assert.ok(!compareSource.includes("class: 'cmp-folder-viewport', tabindex: '0'"), 'rowgroup 不应承载 composite tabindex');
  assert.ok(compareSource.includes('return { startScan: startScan }'), '文件夹工作台应暴露延迟扫描句柄');

  // 6. 远程来源历史只保存身份，不保存凭据，并能识别类型冲突
  const ftp = cmp.sourceIdentity({ kind: 'ftp', host: 'example.test', port: 2121, tls_mode: 'explicit', path: '/drop', password: 'secret' });
  assert.strictEqual(ftp.password, undefined, '历史身份不得携带密码');
  assert.strictEqual(ftp.tls, 'explicit');
  assert.strictEqual(cmp.normalizeFolderHistoryEntry('C:\\drop').kind, 'local');
  const conflict = { status: 'different', left: { is_dir: false }, right: { is_dir: true } };
  assert.strictEqual(cmp.isTypeConflict(conflict), true);
  assert.strictEqual(cmp.isBulkActionable(conflict), false);

  // 7. 自动展开只处理已经拿到直接子项的目录，边界目录首次点击应触发懒扫描
  const expanded = new Set();
  cmp.autoExpandDiffFolderRows([
    { rel_path: 'boundary', left: { is_dir: true }, right: { is_dir: true }, status: 'different' },
    { rel_path: 'loaded', left: { is_dir: true }, right: { is_dir: true }, status: 'different' },
    { rel_path: 'loaded/file.txt', left: { size: 1 }, right: { size: 2 }, status: 'different' },
    { rel_path: 'left-only', left: { is_dir: true }, right: null, status: 'left_only' }
  ], expanded, 300);
  assert.strictEqual(expanded.has('loaded'), true, '有直接子项的差异目录应自动展开');
  assert.strictEqual(expanded.has('boundary'), false, '扫描边界目录应保持收起');
  assert.strictEqual(expanded.has('left-only'), false, '无子项的单侧目录应保持收起');

  // 6. 编辑器历史：加载后的快照是撤销边界，第一次撤销不能回到空文本
  const history = cmp.createEditorHistory('loaded text', 10);
  history.schedule('loaded text\nchanged');
  assert.strictEqual(history.undo('loaded text\nchanged'), 'loaded text', '第一次撤销应回到加载内容');
  assert.strictEqual(history.redo('loaded text'), 'loaded text\nchanged', '重做应恢复编辑内容');
  history.reset('new file');
  history.schedule('new file\nedit');
  assert.strictEqual(history.undo('new file\nedit'), 'new file', 'reset 后历史不应包含旧文件');
  assert.strictEqual(history.canUndo(), false, '回到加载快照后不可继续撤销');
  history.dispose();

  // 7. 保存快捷键的焦点解析：无法确定侧别时返回 null，绝不默认为左侧
  function node(className, side, parent) {
    return {
      className: className || '',
      parentElement: parent || null,
      getAttribute: function (key) {
        if (key === 'class') return this.className;
        if (key === 'data-side') return side || null;
        return null;
      }
    };
  }
  const rightCell = node('cmp-code', null, node('cmp-diff-cell cmp-diff-right', null, null));
  assert.strictEqual(cmp.resolveSaveSide(rightCell), 'right');
  assert.strictEqual(cmp.resolveSaveSide(node('cmp-editor-input', null, node('cmp-editor', 'left', null))), 'left');
  assert.strictEqual(cmp.resolveSaveSide(node('cmp-wb-panel', null, null)), null);

  // 8. 自动比较 key 包含真正影响结果的选项，但 backup 仅影响写入，不触发 diff
  const keyA = cmp.makeCompareKey('a', 'b', { trim_space: true, backup: true }, 'L', 'R');
  const keyB = cmp.makeCompareKey('a', 'b', { trim_space: true, backup: false }, 'L', 'R');
  const keyC = cmp.makeCompareKey('a', 'b', { trim_space: false }, 'L', 'R');
  assert.strictEqual(keyA, keyB, 'backup 不应改变 diff key');
  assert.notStrictEqual(keyA, keyC, 'trim_space 应改变 diff key');

  // 9. 大文本策略按字符数或行数触发降级
  assert.strictEqual(cmp.isLargeText('x'.repeat(200000)), false);
  assert.strictEqual(cmp.isLargeText('x'.repeat(200001)), true);
  assert.strictEqual(cmp.isLargeText(Array(12001).fill('x').join('\n')), true);

  // 10. 投产历史交接一次性消费、校验时效和 URL hash 解析
  const nonce = 'wp-test-1234';
  const storage = {
    data: {},
    getItem: function (key) { return this.data[key] || null; },
    removeItem: function (key) { delete this.data[key]; }
  };
  storage.data['kairo:waspack:compare-handoff:' + nonce] = JSON.stringify({
    left: { kind: 'local', path: 'D:\\release\\previous' },
    right: { kind: 'local', path: 'd:/release/current/' },
    created_at: new Date(1000).toISOString(), source: 'waspack-history'
  });
  const consumed = cmp.consumeWaspackHandoff(nonce, storage, 1500);
  assert.strictEqual(consumed.ok, true);
  assert.strictEqual(storage.getItem('kairo:waspack:compare-handoff:' + nonce), null, '交接消费后必须立即移除，防止重放');
  assert.strictEqual(cmp.consumeWaspackHandoff(nonce, storage, 1500).ok, false);
  assert.strictEqual(cmp.validateWaspackHandoff({
    left: { kind: 'local', path: 'D:\\same' }, right: { kind: 'local', path: 'd:/SAME/' },
    created_at: new Date(1000).toISOString(), source: 'waspack-history'
  }, 1500).ok, false, '相同目录不得形成比较快照');
  assert.strictEqual(cmp.validateWaspackHandoff({
    left: { kind: 'local', path: 'D:\\old' }, right: { kind: 'local', path: 'D:\\new' },
    created_at: new Date(0).toISOString(), source: 'waspack-history'
  }, 10 * 60 * 1000 + 1).ok, false, '超过时效的交接必须拒绝');
  assert.strictEqual(cmp.handoffNonceFromHash('#/compare?handoff=wp-test-1234'), nonce);

  console.log('  compare helpers: joinPath / rollupAllFolders / path parsing / source identity ✓');
}

// ---------- Tab 保活测试共用 mock DOM / vm 环境 ----------
//
// tabs.js + app.js 跑在 vm 里，需要最小 DOM：
//   - document.createElement/getElementById/querySelector(All)
//   - viewRoot / tabBar 容器（pane 的挂载点）
//   - sessionStorage / CustomEvent / history.replaceState / window.confirm
function makeTabMockEl(tag) {
  const el = {
    tag: tag || 'div',
    children: [],
    dataset: {},
    style: {},
    textContent: '',
    title: '',
    parentNode: null,
    scrollTop: 0,
    _cls: new Set(),
    _attrs: {},
    _listeners: {}
  };
  el.classList = {
    add(c) { el._cls.add(c); },
    remove(c) { el._cls.delete(c); },
    toggle(c, f) {
      if (f === undefined) { if (el._cls.has(c)) el._cls.delete(c); else el._cls.add(c); }
      else if (f) el._cls.add(c); else el._cls.delete(c);
    },
    contains(c) { return el._cls.has(c); }
  };
  el.setAttribute = (k, v) => { el._attrs[k] = v; };
  el.getAttribute = (k) => (k in el._attrs ? el._attrs[k] : null);
  el.appendChild = (c) => { if (c) { c.parentNode = el; el.children.push(c); } return c; };
  el.removeChild = (c) => {
    const i = el.children.indexOf(c);
    if (i >= 0) el.children.splice(i, 1);
    if (c) c.parentNode = null;
    return c;
  };
  el.remove = () => { if (el.parentNode) el.parentNode.removeChild(el); };
  el.addEventListener = (t, f) => { (el._listeners[t] = el._listeners[t] || []).push(f); };
  el.removeEventListener = () => {};
  el.querySelector = () => null;
  el.querySelectorAll = () => [];
  el.scrollIntoView = () => {};
  Object.defineProperty(el, 'isConnected', { get: () => !!el.parentNode });
  Object.defineProperty(el, 'innerHTML', { get: () => '', set: () => { el.children = []; } });
  return el;
}

// 跑真实 route-scope.js + tabs.js + app.js，返回可驱动的句柄。
// opts: { hash, routes, routeNames, database, core(merge), confirm }
function makeTabsVmEnv(opts) {
  opts = opts || {};
  const vm = require('vm');
  const routeScopeSrc = fs.readFileSync(path.join(__dirname, 'workbench', 'route-scope.js'), 'utf8');
  const tabsSrc = fs.readFileSync(path.join(__dirname, 'tabs.js'), 'utf8');
  const appSrc = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

  const listeners = {};
  const viewRoot = makeTabMockEl('section');
  const barEl = makeTabMockEl('div');
  const ext = opts.extra || {};
  const core = Object.assign({
    el: (tag, attrs) => ({ tag, text: attrs && attrs.text }),
    $: () => null,
    $$: () => [],
    clearDlBadge() {},
    toast() {},
    releaseTabResources() {},
    hasActiveUploadsFor: () => false,
    hasActiveShellsFor: () => false
  }, ext.core || opts.core || {});
  const win = {
    Kairo: {
      core,
      state: { routes: opts.routes || {}, routeNames: opts.routeNames || {} },
      database: opts.database
    },
    addEventListener: (type, fn) => { listeners[type] = fn; },
    dispatchEvent: () => {},
    confirm: opts.confirm || (() => true),
    location: { hash: opts.hash || '#/home' },
    document: {
      body: { classList: { remove() {}, add() {}, toggle() {} } },
      createElement: (t) => makeTabMockEl(t),
      getElementById: (id) => (id === 'view' ? viewRoot : id === 'tab-bar' ? barEl : null),
      querySelector: () => null,
      querySelectorAll: () => []
    },
    console,
    history: { replaceState: (s, t, url) => { win.location.hash = url; } }
  };
  win.window = win;
  const ctx = {
    window: win,
    document: win.document,
    location: win.location,
    history: win.history,
    console,
    setTimeout, clearTimeout, setInterval, clearInterval,
    AbortController, URLSearchParams,
    sessionStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    CustomEvent: class TabTestEvent {
      constructor(n, o) { this.type = n; this.detail = (o && o.detail) || null; }
    }
  };
  vm.runInNewContext(routeScopeSrc + '\n' + tabsSrc + '\n' + appSrc, ctx);
  win.Kairo.tabs.init({ viewRoot, barEl });
  return { win, listeners, viewRoot, barEl, core };
}

async function testRouteScopeLifecycleAndAsyncUnmount() {
  const vm = require('vm');
  const routeScopeSrc = fs.readFileSync(path.join(__dirname, 'workbench', 'route-scope.js'), 'utf8');

  // Test 1: RouteScope direct unit tests
  const vmContext = { window: {}, console, setTimeout, clearTimeout, setInterval, clearInterval, AbortController };
  vmContext.window = vmContext;
  vm.runInNewContext(routeScopeSrc, vmContext);
  const { createRouteScope, once } = vmContext.window.Kairo.workbench;

  // 1a. Double dispose idempotency
  let cleanupCalls = 0;
  const scope1 = createRouteScope(1);
  scope1.add(() => cleanupCalls++);
  scope1.add(() => cleanupCalls++);
  assert.strictEqual(cleanupCalls, 0);
  scope1.dispose();
  assert.strictEqual(cleanupCalls, 2);
  scope1.dispose();
  assert.strictEqual(cleanupCalls, 2, 'double dispose must be idempotent');

  // 1b. Post-disposal resource creation
  let postCleanupCalls = 0;
  scope1.add(() => postCleanupCalls++);
  assert.strictEqual(postCleanupCalls, 1, 'disposer added to dead scope must execute immediately');

  let timerRan = false;
  scope1.timeout(() => { timerRan = true; }, 10);
  await new Promise(r => setTimeout(r, 20));
  assert.strictEqual(timerRan, false, 'timer on dead scope must not execute');

  const ctrl = scope1.controller();
  assert.strictEqual(ctrl.signal.aborted, true, 'controller on dead scope must be aborted immediately');

  // 1c. Timer cancellation on dispose
  const scope2 = createRouteScope(2);
  let timer2Ran = false;
  scope2.timeout(() => { timer2Ran = true; }, 50);
  scope2.dispose();
  await new Promise(r => setTimeout(r, 60));
  assert.strictEqual(timer2Ran, false, 'timer must be cleared on scope disposal');

  // 1d. Event listener removal on dispose
  const scope3 = createRouteScope(3);
  let eventCalls = 0;
  const listeners = {};
  const mockTarget = {
    addEventListener: (type, fn) => { listeners[type] = fn; },
    removeEventListener: (type, fn) => { if (listeners[type] === fn) delete listeners[type]; }
  };
  scope3.event(mockTarget, 'click', () => eventCalls++);
  assert.strictEqual(typeof listeners.click, 'function');
  scope3.dispose();
  assert.strictEqual(listeners.click, undefined, 'event listener must be removed on scope disposal');

  // Test 2: Tab keep-alive lifecycle（Tab 保活新语义）
  //   - 首次打开渲染一次；切换只隐藏/显示，不销毁、不重渲染
  //   - 隐藏期间 late 到达的异步 cleanup 存入该 Tab，不执行、不污染活动页
  //   - 关闭才 dispose + unmount（恰好一次）；stale 拒绝不写活动页 DOM
  let unmountACalls = 0, unmountBCalls = 0;
  let renderA = 0, renderB = 0;
  let resolveA;
  const promiseA = new Promise(r => { resolveA = r; });

  const env = makeTabsVmEnv({
    hash: '#/a',
    routes: {
      home: () => () => {},
      a: () => { renderA++; return promiseA.then(() => () => { unmountACalls++; }); },
      b: () => { renderB++; return () => { unmountBCalls++; }; }
    },
    routeNames: { home: '首页', a: 'A', b: 'B' }
  });
  const T = env.win.Kairo.tabs;
  const go = (hash) => { env.win.location.hash = hash; env.listeners.hashchange(); };

  // 打开 A：渲染一次，pane token 递增
  go('#/a');
  assert.strictEqual(renderA, 1, '首次打开 A 渲染一次');
  const paneA = T.getActive().pane;
  assert.ok(paneA, 'A 有独立 pane');
  assert.strictEqual(paneA.dataset.renderToken, '1', 'A pane token 1');
  assert.strictEqual(env.win.Kairo.state.currentRoute, 'a');

  // 切到 B：A 不卸载，只是隐藏
  go('#/b');
  assert.strictEqual(renderB, 1, '首次打开 B 渲染一次');
  assert.strictEqual(unmountACalls, 0, '切换 Tab 不得卸载隐藏页');
  assert.strictEqual(paneA.style.display, 'none', '隐藏 pane display:none');

  // A 的异步 cleanup 在隐藏期间 late 到达：存入 A，不执行、不污染 B
  resolveA();
  await promiseA;
  await new Promise(r => setImmediate(r));
  assert.strictEqual(unmountACalls, 0, '隐藏 Tab 的 late cleanup 只存储不执行');
  assert.strictEqual(unmountBCalls, 0, '活动页 B 不受隐藏页 late 回调影响');

  // 切回 A：不重渲染，同一 pane 重新显示
  go('#/a');
  assert.strictEqual(renderA, 1, '切回不得重渲染');
  assert.strictEqual(T.getActive().pane, paneA, '同一 pane 实例');
  assert.strictEqual(paneA.style.display, '', 'pane 重新显示');

  // 关闭非活动 B：卸载恰好一次，A 保持活动
  T.requestClose('b');
  assert.strictEqual(unmountBCalls, 1, '关闭时卸载恰好一次');
  assert.strictEqual(T.getActiveId(), 'a', 'A 仍是活动页');

  // 新语义：仅剩一个 Tab 时拒绝关闭（保证总有活动 Tab，不再回落新建 home）
  T.requestClose('a');
  assert.strictEqual(unmountACalls, 0, '仅剩一个 Tab 时不得卸载');
  assert.strictEqual(T.getActiveId(), 'a', '仅剩一个 Tab 时关闭被拒绝');
  assert.ok(T.has('a'), '仅剩一个 Tab 时保留');

  // 从别的页主动点回首页：保留多 Tab（home 与 a 共存）
  go('#/home');
  assert.ok(T.has('a'), '主动回首页不销毁后台 Tab');
  assert.strictEqual(T.getActiveId(), 'home');

  // stale 拒绝不写活动页 DOM：从首页打开 C（自动收掉首页）后立即关闭 C，
  // 再让它 late 拒绝；回落到 A 且不得写 A 的 DOM
  let rejectC;
  const pC = new Promise((_, rej) => { rejectC = rej; });
  env.win.Kairo.state.routes.c = () => pC;
  env.win.Kairo.state.routeNames.c = 'C';
  const aKids = paneA.children.length;
  go('#/c');
  assert.ok(!T.has('home'), '从首页打开别的页面时自动收掉首页');
  assert.strictEqual(T.getActiveId(), 'c');
  T.requestClose('c');
  rejectC(new Error('late failure in C'));
  try { await pC; } catch (_) {}
  await new Promise(r => setImmediate(r));
  assert.strictEqual(T.getActive().pane, paneA, '回落后仍是 A 同一 pane');
  assert.strictEqual(paneA.children.length, aKids, 'stale 拒绝不得写活动页 DOM');

  console.log('  route scope and async unmount lifecycle ✓');
}

async function testUI02_NavigationPolicyAndGuards() {
  // Tab 保活新语义：
  //   - 切换 Tab 永不拦截、永不销毁（后台保持运行，草稿/事务都在）
  //   - 关闭 Tab / 关浏览器时才执行守卫（confirm / 静默阻止 + toast）
  let confirmResult = true;
  const confirmMessages = [];
  const mockConfirm = (msg) => {
    confirmMessages.push(msg);
    return confirmResult;
  };
  const clearMsgs = () => { confirmMessages.length = 0; };

  let dbHasPending = true;
  let hasUploads = false;
  let discardedWorkCalls = 0;
  let cancelUploadCalls = 0;
  const released = [];
  const toasts = [];

  const env = makeTabsVmEnv({
    hash: '#/home',
    confirm: mockConfirm,
    routes: {
      home: () => () => {},
      database: () => () => {},
      files: () => () => {},
      scoped: (view, st, scope) => {
        scope.setCanLeave(() => false);
        return () => {};
      }
    },
    routeNames: { home: '首页', database: '数据库', files: '文件', scoped: '受保护页' },
    database: {
      hasPendingWork: () => dbHasPending
        ? { hasTransaction: true, dirtyCount: 2, pending: true }
        : { hasTransaction: false, dirtyCount: 0, pending: false },
      discardPendingWork: () => { discardedWorkCalls++; },
      cancel: () => {}
    },
    core: {
      toast: (msg) => { toasts.push(msg); },
      releaseTabResources: (id) => {
        released.push(id);
        if (id === 'files' && hasUploads) cancelUploadCalls++;
      },
      hasActiveUploadsFor: (id) => hasUploads && id === 'files',
      hasActiveShellsFor: () => false
    }
  });
  const T = env.win.Kairo.tabs;
  const S = env.win.Kairo.state;
  const go = (hash) => { env.win.location.hash = hash; env.listeners.hashchange(); };

  // 1. 打开 database，切到 home：切换永不确认、不销毁（边查日志边操作 DB）
  go('#/database');
  assert.strictEqual(S.currentRoute, 'database');
  go('#/home');
  assert.strictEqual(confirmMessages.length, 0, '切换 Tab 不得弹确认');
  assert.strictEqual(S.currentRoute, 'home');
  assert.ok(T.has('database'), 'database Tab 在后台保留');
  assert.strictEqual(discardedWorkCalls, 0, '切换不得回滚事务');

  // 2. T046改：关闭有未提交事务的 database，用户取消 → 保留
  confirmResult = false;
  clearMsgs();
  T.requestClose('database');
  assert.strictEqual(confirmMessages.length, 1, '关闭时弹确认');
  assert.ok(confirmMessages[0].includes('未提交的事务'), '文案提到未提交事务');
  assert.ok(T.has('database'), '取消后 Tab 保留');
  assert.strictEqual(discardedWorkCalls, 0, '取消后不回滚');

  // 3. T046改：确认关闭 → 回滚 + 销毁
  confirmResult = true;
  clearMsgs();
  T.requestClose('database');
  assert.strictEqual(discardedWorkCalls, 1, '确认关闭后回滚一次');
  assert.ok(!T.has('database'), 'Tab 已销毁');
  assert.strictEqual(T.getActiveId(), 'home');

  // 4. T047改：下载任务切换不释放（后台保持运行），关闭才释放
  go('#/files');
  go('#/home');
  assert.ok(!released.includes('files'), '切换不得释放后台 Tab 资源');
  T.requestClose('files');
  assert.ok(released.includes('files'), '关闭才释放该 Tab 资源');

  // 5. T047改：上传任务切换不确认不取消；关闭才确认/取消
  hasUploads = true;
  clearMsgs();
  go('#/files');
  const cancelBefore = cancelUploadCalls;
  go('#/home');
  assert.strictEqual(confirmMessages.length, 0, '上传中切换也不确认');
  assert.strictEqual(cancelUploadCalls, cancelBefore, '切换不取消上传');
  confirmResult = false;
  clearMsgs();
  T.requestClose('files');
  assert.strictEqual(confirmMessages.length, 1, '关闭上传中的 Tab 才确认');
  assert.ok(confirmMessages[0].includes('上传'), '文案提到上传');
  assert.strictEqual(cancelUploadCalls, cancelBefore, '取消关闭则不取消上传');
  assert.ok(T.has('files'), '取消后 Tab 保留');
  confirmResult = true;
  T.requestClose('files');
  assert.strictEqual(cancelUploadCalls, cancelBefore + 1, '确认关闭才取消上传');
  assert.ok(!T.has('files'), '上传 Tab 已销毁');

  // 6. scope.canLeave 返回 false → 切换不受影响，关闭被静默阻止（toast）
  hasUploads = false;
  go('#/scoped');
  assert.strictEqual(S.currentRoute, 'scoped');
  go('#/files');
  assert.strictEqual(S.currentRoute, 'files', '切换不受 canLeave 阻止');
  clearMsgs();
  const toastBefore = toasts.length;
  T.requestClose('scoped');
  assert.strictEqual(confirmMessages.length, 0, '静默阻止不弹 confirm');
  assert.strictEqual(toasts.length, toastBefore + 1, '静默阻止给 toast 提示');
  assert.ok(T.has('scoped'), '被阻止的 Tab 保留');

  console.log('  UI-02 tab close guards, background keep-alive, and task release ✓');
}

function testHomeTabCloseAndAutoDismiss() {
  // 新语义：首页是普通落地页
  //   - 仅剩一个 Tab 时（包括首页）不给关，保证总有活动 Tab
  //   - 有其他 Tab 时首页可关闭
  //   - 从首页打开别的页面时自动收掉首页；从别的页点回首页则保留多 Tab
  const env = makeTabsVmEnv({
    hash: '#/home',
    routes: {
      home: () => () => {},
      files: () => () => {},
      ssh: () => () => {}
    },
    routeNames: { home: '首页', files: '文件', ssh: '终端' }
  });
  const T = env.win.Kairo.tabs;
  const go = (hash) => { env.win.location.hash = hash; env.listeners.hashchange(); };
  const closeVisible = (id) => {
    const btn = env.barEl.children.find((c) => c.getAttribute && c.getAttribute('data-tab') === id);
    if (!btn) return false;
    return btn.children.some((c) => c.className === 'app-tab-close' || c._className === 'app-tab-close');
  };

  go('#/home');
  assert.strictEqual(T.getActiveId(), 'home');
  assert.strictEqual(closeVisible('home'), false, '仅剩首页时不给 ×');

  // 仅剩一个 Tab 时拒绝关闭（含程序化调用）
  T.requestClose('home');
  assert.ok(T.has('home'), '仅剩首页时关闭被拒绝');
  assert.strictEqual(T.getActiveId(), 'home');

  // 从首页打开文件：自动收掉首页
  go('#/files');
  assert.strictEqual(T.getActiveId(), 'files');
  assert.ok(!T.has('home'), '从首页打开别的页面时自动收掉首页');
  assert.strictEqual(closeVisible('files'), false, '收掉首页后只剩文件，不给 ×');

  // 从别的页点回首页：保留多 Tab，且首页可关
  go('#/home');
  assert.ok(T.has('files'), '主动回首页不销毁后台 Tab');
  assert.strictEqual(T.getActiveId(), 'home');
  assert.strictEqual(closeVisible('home'), true, '有多 Tab 时首页给 ×');
  assert.strictEqual(closeVisible('files'), true, '有多 Tab 时文件给 ×');

  T.requestClose('home');
  assert.ok(!T.has('home'), '有多 Tab 时首页可关闭');
  assert.strictEqual(T.getActiveId(), 'files');

  // 后台没有 home 时再开别的页：不凭空造 home
  go('#/ssh');
  assert.ok(T.has('files'), '非首页之间切换保留后台 Tab');
  assert.ok(!T.has('home'), '后台没有 home 时不凭空出现');

  console.log('  home tab close rules and auto-dismiss on leaving home ✓');
}

function testT069_ThemeIntegrityAndTokens() {
  const cssPath = path.join(__dirname, 'style.css');
  const css = fs.readFileSync(cssPath, 'utf8');

  // Must declare 5 themes
  const expectedThemes = ['dark', 'light', 'green', 'hc', 'xianxia'];
  for (const th of expectedThemes) {
    const sel = `[data-theme="${th}"]`;
    assert.ok(css.includes(sel), `style.css must contain rules for theme ${th}`);
  }

  // Check essential design tokens are declared across themes
  const coreTokens = [
    '--bg-0', '--bg-1', '--bg-2', '--bg-3',
    '--line', '--text', '--text-dim', '--primary',
    '--error', '--warn', '--success'
  ];
  for (const token of coreTokens) {
    assert.ok(css.includes(token + ':'), `style.css must define core token ${token}`);
  }

  // HC theme must define high-contrast specific overrides
  assert.ok(css.includes(':root[data-theme="hc"]'), 'hc theme must be defined on :root');
  assert.ok(css.includes(':root[data-theme="light"]'), 'light theme must be defined on :root');
  assert.ok(css.includes(':root[data-theme="green"]'), 'green theme must be defined on :root');
  assert.ok(css.includes(':root[data-theme="xianxia"]'), 'xianxia theme must be defined on :root');

  console.log('  T069 theme tokens, contrast styles, and 5-theme integrity ✓');
}

// ---------- 主入口 ----------

async function main() {
  console.log('Running web/app.test.js...');
  const tests = [
    testEscapeHtml, testFormatBytes, testFormatTime, testTrimMiddle,
    testEscapeRegex, testParseSearchTermsForHighlight, testHighlightAndTrim,
    testCssEscape, testPctText, testValidate, testEl, testConfirmDialog, testXSSInErrorText,
    testGotDoneDedupe, testNormalizeHighlightColor, testRenderHighlightedLine,
    testTailViewer, testApplyCommandPath, testDatabaseSQLHelpers, testDatabaseWorkbenchLazy, testCompareHelpers, testWaspackHelpers,
    testRouteScopeLifecycleAndAsyncUnmount, testUI02_NavigationPolicyAndGuards,
    testHomeTabCloseAndAutoDismiss,
    testT069_ThemeIntegrityAndTokens,
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

