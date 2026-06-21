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
  // 注入攻击样本
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
  // 非数字
  assert.strictEqual(formatBytes('not a number'), '0 B', 'NaN -> 0');
  // bigint 通过 Number() 转成普通 number，仍能格式化（1.0 TB）
  assert.strictEqual(formatBytes(BigInt(1024) ** BigInt(4)), '1.0 TB', 'bigint -> TB');
  assert.strictEqual(formatBytes(null), '0 B', 'null -> 0');
  // 负数（不会出现，但别 panic）
  assert.strictEqual(formatBytes(-1), '-1 B', 'negative');
  console.log('  formatBytes ✓');
}

// ---------- formatTime ----------

function testFormatTime() {
  assert.strictEqual(formatTime(''), '-', 'empty');
  assert.strictEqual(formatTime(null), '-', 'null');
  assert.strictEqual(formatTime(undefined), '-', 'undefined');
  // 真实时间：toLocaleString 的格式依赖环境，只验证非空且不抛
  const out = formatTime('2026-06-21T15:30:00Z');
  assert.ok(out && out !== '-', 'iso8601 -> non-empty');
  // 无效输入 → 走 try/catch 返回原值
  const out2 = formatTime('garbage time');
  assert.ok(typeof out2 === 'string', 'invalid returns string');
  console.log('  formatTime ✓');
}

// ---------- trimMiddle ----------

function testTrimMiddle() {
  assert.strictEqual(trimMiddle('', 10), '', 'empty');
  assert.strictEqual(trimMiddle('hi', 10), 'hi', 'short');
  assert.strictEqual(trimMiddle('abcdefghij', 5), 'ab…ij', 'middle ellipsis');
  // 偶数 keep
  const out = trimMiddle('abcdefghij', 6);
  assert.ok(out.includes('…'), 'has ellipsis');
  assert.ok(out.length < 10, 'shortened');
  // null/undefined
  assert.strictEqual(trimMiddle(null, 10), '', 'null');
  console.log('  trimMiddle ✓');
}

// ---------- 主入口 ----------

function main() {
  console.log('Running web/app.test.js...');
  const tests = [testEscapeHtml, testFormatBytes, testFormatTime, testTrimMiddle];
  let pass = 0, fail = 0;
  for (const t of tests) {
    try {
      t();
      pass++;
    } catch (e) {
      console.error('  FAIL: ' + t.name + ': ' + e.message);
      fail++;
    }
  }
  console.log(`\n${pass} pass, ${fail} fail`);
  if (fail > 0) process.exit(1);
}

main();
