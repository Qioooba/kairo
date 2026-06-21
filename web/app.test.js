// web/app.test.js — Node 单元测试
//
// 测试策略：app.js 是 IIFE 风格，所有逻辑都在一个闭包里。
// 这里用 Function + 正则把"纯函数"（无 DOM 依赖）抽出来构造，
// 然后跑断言。覆盖范围 = app.js 里无 DOM / no state 的辅助函数。
//
// 运行：node web/app.test.js
// 依赖：纯 stdlib，不需要 npm install

'use strict';

const fs = require('fs');
const path = require('path');
const assert = require('assert');

const APP_PATH = path.join(__dirname, 'app.js');
const src = fs.readFileSync(APP_PATH, 'utf8');

// 抽一个具名函数。匹配 "function NAME(...) { ... }" 块，括号配对简单实现。
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

// cssEscape 在 app.js 里有同名两个函数（renderWebsphere 闭包内一个、顶层一个），
// 取顶层版本（含 `window.CSS` 分支）。
function extractTopCssEscape() {
  const re = /\n  function cssEscape\(s\) \{[\s\S]*?\n  \}/;
  const m = src.match(re);
  if (!m) throw new Error('top cssEscape not found');
  return m[0].slice(1);
}
// 给 new Function 提供 window mock（Node 没全局 window）
const cssEscape = new Function(
  'window',
  extractTopCssEscape() + '\n  return cssEscape;'
)({ CSS: undefined });

// pctText 在 app.js 里有同名两个函数（一个在 renderWebsphere 闭包内，一个在顶层），
// 用更精确的正则只取顶层（2 空格缩进）的版本，并显式传入 formatBytes 引用。
function extractTopPctText() {
  // 顶层函数体只有 5 行（含签名），用非贪婪匹配到第一个 "^  }" 行（2 空格缩进的 }）
  const re = /\n  function pctText\(written, total\) \{[\s\S]*?\n  \}/;
  const m = src.match(re);
  if (!m) throw new Error('top pctText not found');
  return m[0].slice(1); // 去前导换行
}
const pctText = new Function(
  'formatBytes',
  extractTopPctText() + '\n  return pctText;'
)(formatBytes);

const validate = new Function(extract('validate') + '; return validate;')();

// trimMiddle 是内嵌在 renderMultiResults 里的，用更精确的正则：
function extractTrimMiddle() {
  const re = /function\s+trimMiddle\s*\([^)]*\)\s*\{[\s\S]*?\n\s{4}\}/;
  const m = src.match(re);
  if (!m) throw new Error('trimMiddle not found');
  return m[0];
}
const trimMiddle = new Function(extractTrimMiddle() + '; return trimMiddle;')();

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
  // app.js 的 cssEscape 第一行是 `if (window.CSS && CSS.escape)`；
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

// ---------- 主入口 ----------

function main() {
  console.log('Running web/app.test.js...');
  const tests = [
    testEscapeHtml, testFormatBytes, testFormatTime, testTrimMiddle,
    testCssEscape, testPctText, testValidate,
  ];
  let pass = 0, fail = 0;
  for (const t of tests) {
    try {
      t();
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