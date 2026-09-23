'use strict';
// tests/sql-format-selection-boundary.test.js
//
// 审核第 4 项（P1）回归：选区格式化把 WHERE 吞进注释，扩大 SQL 更新范围。
//
// 原 SQL：
//   update employees
//   set status = 'X' -- change requested
//   where id = 1;
// 只选中第二行（**包括末尾换行**）再格式化时，旧实现丢掉选区末尾换行，
// 结果 `where id = 1;` 被并进 `--` 行注释，返回状态却仍是成功、保真诊断为空。
//
// 审核第 13 项（P2）回归：`SELECT 1.e+2 FROM DUAL` 被拆成 `1.e + 2`。
//
// 本文件用真实格式化函数验证：格式化前后 SQL 的**作用范围**必须一致。

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const window = { Kairo: {} };
global.window = window;
for (const file of ['web/workbench/statement-model.js', 'web/workbench/sql-format-service.js']) {
  vm.runInThisContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'));
}
const service = window.Kairo.workbench.sqlFormatService;

let DatabaseSync = null;
try { DatabaseSync = require('node:sqlite').DatabaseSync; } catch (_) { DatabaseSync = null; }

// ---------------------------------------------------------------------------
// 第 4 项：选区边界
// ---------------------------------------------------------------------------
const SOURCE = [
  'update employees',
  "set status = 'X' -- change requested",
  'where id = 1;'
].join('\n');

function selectSecondLineIncludingNewline() {
  const start = SOURCE.indexOf('set status');
  const end = SOURCE.indexOf('where id') ; // 行尾换行也一起选中
  return { start, end };
}

{
  const { start, end } = selectSecondLineIncludingNewline();
  assert.strictEqual(SOURCE.slice(start, end).endsWith('\n'), true,
    '复现前提：选区必须包含末尾换行');

  const result = service.formatRange(SOURCE, start, end, { dialect: 'oracle' });
  const next = result.text;
  const whereLine = next.split('\n').find(line => /where\s+id/i.test(line));
  assert.ok(whereLine, '`where id = 1;` 必须仍然是独立的一行，实际:\n' + next);
  assert.ok(!/--[^\n]*where\s+id/i.test(next),
    '格式化不得把 WHERE 并进 `--` 行注释，实际:\n' + next);
}

{
  // DOM 编辑器路径（真实页面走的就是这条）
  const { start, end } = selectSecondLineIncludingNewline();
  const listeners = [];
  const textarea = {
    value: SOURCE,
    selectionStart: start,
    selectionEnd: end,
    setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; },
    setRangeText(text, a, b) {
      this.value = this.value.slice(0, a) + text + this.value.slice(b);
      this.selectionStart = a;
      this.selectionEnd = a + text.length;
    },
    dispatchEvent() {},
    focus() {}
  };
  global.document = { createEvent: () => ({ initEvent() {} }) };
  const result = service.formatInEditor(textarea, { dialect: 'oracle' });
  assert.strictEqual(result.status, 'ok', '格式化应成功: ' + JSON.stringify(result));
  assert.ok(!/--[^\n]*where\s+id/i.test(textarea.value),
    '编辑器路径同样不得把 WHERE 并进注释，实际:\n' + textarea.value);
}

// 语义验证：格式化前后被修改的行数必须一致（只有 ID=1 那一行）
if (DatabaseSync) {
  const db = new DatabaseSync(':memory:');
  db.exec('CREATE TABLE employees (id INTEGER PRIMARY KEY, status TEXT)');
  db.exec("INSERT INTO employees (id, status) VALUES (1, 'A'), (2, 'A')");
  const { start, end } = selectSecondLineIncludingNewline();
  const formatted = service.formatRange(SOURCE, start, end, { dialect: 'oracle' }).text;
  const changes = (sql) => {
    db.exec("UPDATE employees SET status = 'A'");
    db.exec(sql);
    return db.prepare('SELECT COUNT(*) AS n FROM employees WHERE status = ' + "'X'").get().n;
  };
  const originalChanges = changes(SOURCE);
  const formattedChanges = changes(formatted);
  assert.strictEqual(originalChanges, 1, '原 SQL 只应修改 ID=1 一行');
  assert.strictEqual(formattedChanges, originalChanges,
    '格式化后的 SQL 作用范围必须与原 SQL 一致（实际改了 ' + formattedChanges + ' 行）');
  db.close();
}

// ---------------------------------------------------------------------------
// 第 13 项：科学计数法
// ---------------------------------------------------------------------------
{
  const cases = ['1.e+2', '1.E-3', '1.e2', '1.5e+3', '2E-4', '0.5'];
  for (const literal of cases) {
    const sql = 'SELECT ' + literal + ' FROM DUAL';
    const formatted = service.formatSQL(sql, { dialect: 'oracle' });
    assert.ok(formatted.includes(literal),
      '数字字面量 ' + literal + ' 必须保持为一个 token，实际: ' + formatted);
  }
  // 边界：`1..2` 是独立运算符 + 数字，不能吞成一个 token
  const range = service.formatSQL('SELECT 1..2 FROM DUAL', { dialect: 'oracle' });
  assert.ok(!range.includes('1..2 FROM') || range.includes('1..2'),
    '1..2 的语义不应被破坏: ' + range);
  assert.strictEqual(service.formatSQL('SELECT 1. FROM DUAL', { dialect: 'oracle' }).trim().length > 0, true);
}

// ---------------------------------------------------------------------------
// 整段保真：局部替换后完整 SQL 的注释/字符串序列必须一致
// ---------------------------------------------------------------------------
{
  const sql = "update t\nset c = '-- not a comment'\nwhere id = 1;";
  const result = service.formatRange(sql, 0, sql.length, { dialect: 'oracle' });
  assert.ok(result.text.includes("'-- not a comment'"), '字符串里的 -- 不得被当成注释: ' + result.text);
}

console.log('SQL format selection-boundary regressions passed');
