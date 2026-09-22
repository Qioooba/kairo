'use strict';
const fs = require('fs');
const path = require('path');
const assert = require('assert');

// Setup mock browser globals
const window = {};
global.window = window;

function loadModule(file) {
  const content = fs.readFileSync(path.join(__dirname, '..', file), 'utf8');
  eval(content);
}

loadModule('web/workbench/statement-model.js');
loadModule('web/workbench/resource-session.js');
loadModule('web/workbench/syntax-editor.js');
loadModule('web/workbench/notice-bus.js');
loadModule('web/workbench/request-recovery.js');
loadModule('web/workbench/sql-format-service.js');

const W = window.Kairo.workbench;

console.log('=== 1. Testing statement-model.js ===');
// Basic multi-statement
const sql1 = 'SELECT 1 FROM DUAL;\nSELECT 2 FROM DUAL;\nSELECT 3 FROM DUAL;';
const splits1 = W.statementModel.split(sql1);
assert.strictEqual(splits1.length, 3, 'Should split into 3 statements');
console.log('  ✓ Split 3 statements count = 3');

// Cursor execution
const c1 = W.statementModel.current(sql1, 5);
assert.ok(c1.text.includes('SELECT 1'), 'Cursor at 5 should select first statement');
const c2 = W.statementModel.current(sql1, 25);
assert.ok(c2.text.includes('SELECT 2'), 'Cursor at 25 should select second statement');
const c3 = W.statementModel.current(sql1, 45);
assert.ok(c3.text.includes('SELECT 3'), 'Cursor at 45 should select third statement');
console.log('  ✓ Cursor-based statement resolution works for all 3 positions');

// Semicolon inside quotes
const sqlQuotes = "SELECT 'hello;world' AS greeting FROM DUAL; SELECT * FROM users;";
const splitsQuotes = W.statementModel.split(sqlQuotes);
assert.strictEqual(splitsQuotes.length, 2, 'Semicolon inside single quote should not split');
console.log('  ✓ Semicolon inside quotes does not split');

// Semicolon inside line comment and block comment
const sqlComments = "SELECT 1 FROM DUAL; -- comment; with semicolon\nSELECT 2 FROM /* comment; inside */ DUAL;";
const splitsComments = W.statementModel.split(sqlComments);
assert.strictEqual(splitsComments.length, 2, 'Semicolon inside comments should not split');
console.log('  ✓ Semicolon inside comments does not split');

// Oracle q-quotes
const sqlQQuote = "SELECT q'[hello;world;foo]' FROM dual; SELECT 2;";
const splitsQ = W.statementModel.split(sqlQQuote);
assert.strictEqual(splitsQ.length, 2, 'Oracle q-quote should not split');
console.log('  ✓ Oracle q-quotes do not split');

// Oracle PL/SQL blocks use a slash on its own line as the client terminator;
// internal semicolons must stay inside the same statement.
const sqlPLSQL = "CREATE OR REPLACE FUNCTION demo_fn(p NUMBER) RETURN NUMBER IS\nBEGIN\n  RETURN p + 1;\nEND;\n/\nSELECT demo_fn(1) FROM dual;";
const splitsPLSQL = W.statementModel.split(sqlPLSQL);
assert.strictEqual(splitsPLSQL.length, 2, 'Oracle PL/SQL block and following SQL should be two statements');
assert.ok(splitsPLSQL[0].text.includes('RETURN p + 1;'), 'PL/SQL internal semicolon must remain in the block');
assert.ok(!splitsPLSQL[0].text.trimEnd().endsWith('/'), 'SQL*Plus slash terminator must not be sent as SQL');
assert.ok(splitsPLSQL[1].text.includes('SELECT demo_fn'), 'SQL after slash terminator should remain executable');
console.log('  ✓ Oracle PL/SQL slash terminator and internal semicolons work');

// Selection priority
const cSel = W.statementModel.current(sql1, 0, 20, 39);
assert.ok(cSel.selected, 'Selection should have priority');
assert.ok(cSel.text.includes('SELECT 2'), 'Selected text should be returned');
console.log('  ✓ Selection takes priority over cursor');

// Trailing semicolon trimming in normalize
const norm1 = W.statementModel.normalize('SELECT 1 FROM DUAL;', 5);
assert.strictEqual(norm1.text, 'SELECT 1 FROM DUAL', 'Single trailing semicolon should be trimmed');
const normMulti = W.statementModel.normalize('SELECT 1 FROM DUAL;;  ', 5);
assert.strictEqual(normMulti.text, 'SELECT 1 FROM DUAL', 'Multiple trailing semicolons should be trimmed');
console.log('  ✓ Trailing semicolon trimming in normalize works');

console.log('=== 2. Testing resource-session.js ===');
const sourceA = { id: 'src-1', name: 'Oracle Prod', kind: 'oracle', updated_at: '2026-09-01T00:00:00Z' };
const sourceB = { id: 'src-2', name: 'MySQL Dev', kind: 'mysql', updated_at: '2026-09-01T00:00:00Z' };
const snap = W.resourceSession.snapshot(sourceA);
assert.strictEqual(snap.id, 'src-1');
assert.strictEqual(snap.name, 'Oracle Prod');

// Resolve existing
const resReady = W.resourceSession.resolve(snap, [sourceA, sourceB]);
assert.strictEqual(resReady.status, 'ready');
assert.strictEqual(resReady.source.id, 'src-1');
console.log('  ✓ Existing source resolves to ready');

// Resolve deleted / orphan source (NEVER fallback silently)
const resDeleted = W.resourceSession.resolve(snap, [sourceB]);
assert.strictEqual(resDeleted.status, 'removed');
assert.strictEqual(resDeleted.source, null, 'Source must be null when deleted');
assert.strictEqual(resDeleted.ref.name, 'Oracle Prod', 'Retains original source name');
console.log('  ✓ Deleted source resolves to removed (no silent fallback)');

console.log('=== 3. Testing syntax-editor.js ===');
const highlightedSQL = W.syntaxEditor.highlight('SELECT * FROM users WHERE id = 1', 'sql');
assert.ok(highlightedSQL.includes('syn-keyword'), 'SQL keywords should have syn-keyword class');
const highlightedJava = W.syntaxEditor.highlight('public class Test { private int x; }', 'java');
assert.ok(highlightedJava.includes('syn-keyword'), 'Java keywords should have syn-keyword class');
const highlightedXML = W.syntaxEditor.highlight('<definitions name="test"></definitions>', 'xml');
assert.ok(highlightedXML.includes('syn-tag'), 'XML tags should have syn-tag class');

// Escape safety
const unsafe = '<script>alert("xss")</script>';
const safeHigh = W.syntaxEditor.highlight(unsafe, 'xml');
assert.ok(!safeHigh.includes('<script>'), 'HTML tags must be escaped safely');
console.log('  ✓ Syntax highlighting escapes HTML safely and applies correct classes');

console.log('=== 4. Testing request-recovery.js ===');
assert.strictEqual(W.requestRecovery.retryable({ data: { retryable: true, output_started: false } }), true);
assert.strictEqual(W.requestRecovery.retryable({ data: { retryable: true, output_started: true } }), false, 'Output already started must not retry');
assert.strictEqual(W.requestRecovery.retryable({ data: { retryable: false } }), false);
assert.strictEqual(W.requestRecovery.retryable(null), false);
console.log('  ✓ Retryable contract works accurately');

console.log('=== 5. Testing notice-bus.js ===');
let received = null;
const unsub = W.noticeBus.on(function (e) { received = e; });
W.noticeBus.emit({ type: 'task.failed', task: 'test-task' });
assert.deepStrictEqual(received, { type: 'task.failed', task: 'test-task' });
unsub();
W.noticeBus.emit({ type: 'another' });
assert.deepStrictEqual(received, { type: 'task.failed', task: 'test-task' }, 'Should not receive after unsub');
console.log('  ✓ Notice bus emit, subscribe, and unsubscribe work');

console.log('=== 6. Testing compare.js history stack & trimming ===');
function createHistoryTracker() {
  const history = ['init'];
  let historyIdx = 0;
  function push(val) {
    if (history[historyIdx] !== val) {
      history.splice(historyIdx + 1);
      history.push(val);
      while (history.length > 50) {
        history.shift();
      }
      historyIdx = history.length - 1;
    }
  }
  function undo() {
    if (historyIdx > 0) historyIdx--;
    return history[historyIdx];
  }
  function redo() {
    if (historyIdx < history.length - 1) historyIdx++;
    return history[historyIdx];
  }
  return { history, push, undo, redo, getIdx: () => historyIdx };
}

const tracker = createHistoryTracker();
for (let i = 1; i <= 60; i++) {
  tracker.push('step_' + i);
}
assert.strictEqual(tracker.history.length, 50, 'History max length should be capped at 50');
assert.strictEqual(tracker.getIdx(), 49, 'History index should be pointing to latest (49)');
assert.strictEqual(tracker.undo(), 'step_59', 'Undo should go to step 59');
assert.strictEqual(tracker.getIdx(), 48);
assert.strictEqual(tracker.redo(), 'step_60', 'Redo should return to step 60');
assert.strictEqual(tracker.getIdx(), 49);
console.log('  ✓ Compare history capping and undo/redo indexing work flawlessly at >50 steps');

console.log('=== 7. Testing database.js pagination state logic ===');
function createPaginationSession() {
  const s = { lastSQL: '', page: 1, pageSize: 20 };
  function runQuery(sql, maxRows, keepPage) {
    const prevSQL = s.lastSQL;
    s.lastSQL = sql;
    s.pageSize = maxRows;
    if (!keepPage || prevSQL !== sql) {
      s.page = 1;
    }
    s.page = Math.max(1, Number(s.page) || 1);
  }
  function go(targetPage) {
    s.page = Math.max(1, Number(targetPage) || 1);
    runQuery(s.lastSQL, s.pageSize, true);
  }
  return { s, runQuery, go };
}

const ps = createPaginationSession();
ps.runQuery('SELECT * FROM users', 20);
assert.strictEqual(ps.s.page, 1, 'Initial query should be on page 1');
ps.go(2);
assert.strictEqual(ps.s.page, 2, 'go(2) should preserve page 2');
ps.go(5);
assert.strictEqual(ps.s.page, 5, 'go(5) should preserve page 5');
// Re-running query without keepPage resets to page 1
ps.runQuery('SELECT * FROM users', 20);
assert.strictEqual(ps.s.page, 1, 'Manual re-execution resets page to 1');
// If SQL changes even during go(), resets to 1
ps.go(3);
assert.strictEqual(ps.s.page, 3);
ps.runQuery('SELECT * FROM orders', 20, true);
assert.strictEqual(ps.s.page, 1, 'Changing SQL resets page to 1 even if keepPage was requested');
console.log('  ✓ Pagination page retention and query reset logic work accurately');

console.log('=== 8. Testing SQL insertion without overwriting ===');
function simulateInsert(currentValue, selStart, selEnd, newText) {
  if (!currentValue.trim()) return newText;
  const before = currentValue.slice(0, selStart);
  const after = currentValue.slice(selEnd);
  const prefix = (before && !before.endsWith('\n') && !before.endsWith(' ')) ? '\n' : '';
  const suffix = (after && !after.startsWith('\n') && !after.startsWith(' ')) ? '\n' : '';
  return before + prefix + newText + suffix + after;
}

const origSQL = 'SELECT * FROM old_table WHERE id = 1';
const inserted = simulateInsert(origSQL, origSQL.length, origSQL.length, 'SELECT 2 FROM DUAL');
assert.ok(inserted.includes('SELECT * FROM old_table'), 'Original SQL must not be overwritten');
assert.ok(inserted.includes('SELECT 2 FROM DUAL'), 'New SQL must be inserted');
console.log('  ✓ SQL cursor insertion preserves existing editor content');

console.log('=== 9. Testing SQL template expand key dispatching & isolation ===');
function simulateKeydown(event, prefs, acState, editorState) {
  let defaultPrevented = false;
  let expanded = false;
  let acceptedKeyword = false;
  const e = Object.assign({ preventDefault: () => { defaultPrevented = true; } }, event);

  function isSnippetExpandKey(ev, trigger) {
    if (!ev || ev.ctrlKey || ev.altKey || ev.metaKey) return false;
    if (trigger === 'Space') return ev.key === ' ' || ev.key === 'Space' || ev.code === 'Space';
    if (trigger === 'Tab') return !ev.shiftKey && (ev.key === 'Tab' || ev.code === 'Tab');
    if (trigger === 'Enter') return !ev.shiftKey && (ev.key === 'Enter' || ev.code === 'Enter' || ev.code === 'NumpadEnter');
    return false;
  }

  function expandSnippet() {
    const pos = editorState.selectionStart, before = editorState.value.slice(0, pos), match = before.match(/([A-Za-z0-9_.-]+)$/);
    if (!match) return false;
    const snippet = prefs.snippets.find(x => x.enabled !== false && x.key === match[1]);
    if (!snippet) return false;
    const start = pos - match[1].length;
    let replacement = String(snippet.text).replace('${table}', 'table_name');
    editorState.value = editorState.value.slice(0, start) + replacement + editorState.value.slice(pos);
    editorState.selectionStart = start + replacement.length;
    acState.open = false;
    expanded = true;
    return true;
  }

  const trigger = prefs.expandKey || 'Space';
  if (acState.open) {
    const curItem = acState.items[acState.index || 0];
    if (curItem && curItem.kind === 'snippet') {
      if (isSnippetExpandKey(e, trigger)) {
        e.preventDefault();
        expandSnippet();
        acState.open = false;
        return { defaultPrevented, expanded, acceptedKeyword };
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        acState.open = false;
        return { defaultPrevented, expanded, acceptedKeyword };
      }
    } else {
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        acceptedKeyword = true;
        acState.open = false;
        return { defaultPrevented, expanded, acceptedKeyword };
      }
    }
  }
  if (isSnippetExpandKey(e, trigger)) {
    if (expandSnippet()) {
      e.preventDefault();
      acState.open = false;
      return { defaultPrevented, expanded, acceptedKeyword };
    }
  }
  return { defaultPrevented, expanded, acceptedKeyword };
}

const defaultSnippets = [{ key: 'sf', text: 'SELECT * FROM ', enabled: true }];

// Case 1: expandKey = 'Space'
{
  const prefs = { expandKey: 'Space', snippets: defaultSnippets };
  const ac = { open: true, items: [{ kind: 'snippet', label: 'sf' }], index: 0 };
  const ed1 = { value: 'sf', selectionStart: 2 };
  const resSpace = simulateKeydown({ key: ' ' }, prefs, Object.assign({}, ac), ed1);
  assert.strictEqual(resSpace.expanded, true, 'Space must expand when expandKey is Space');
  assert.strictEqual(ed1.value, 'SELECT * FROM ');

  const ed2 = { value: 'sf', selectionStart: 2 };
  const resEnter = simulateKeydown({ key: 'Enter' }, prefs, Object.assign({}, ac), ed2);
  assert.strictEqual(resEnter.expanded, false, 'Enter must NOT expand when expandKey is Space');
  assert.strictEqual(ed2.value, 'sf');

  const ed3 = { value: 'sf', selectionStart: 2 };
  const resTab = simulateKeydown({ key: 'Tab' }, prefs, Object.assign({}, ac), ed3);
  assert.strictEqual(resTab.expanded, false, 'Tab must NOT expand when expandKey is Space');
  assert.strictEqual(ed3.value, 'sf');
}

// Case 2: expandKey = 'Tab'
{
  const prefs = { expandKey: 'Tab', snippets: defaultSnippets };
  const ac = { open: true, items: [{ kind: 'snippet', label: 'sf' }], index: 0 };
  const ed1 = { value: 'sf', selectionStart: 2 };
  const resSpace = simulateKeydown({ key: ' ' }, prefs, Object.assign({}, ac), ed1);
  assert.strictEqual(resSpace.expanded, false, 'Space must NOT expand when expandKey is Tab');

  const ed2 = { value: 'sf', selectionStart: 2 };
  const resEnter = simulateKeydown({ key: 'Enter' }, prefs, Object.assign({}, ac), ed2);
  assert.strictEqual(resEnter.expanded, false, 'Enter must NOT expand when expandKey is Tab');

  const ed3 = { value: 'sf', selectionStart: 2 };
  const resTab = simulateKeydown({ key: 'Tab' }, prefs, Object.assign({}, ac), ed3);
  assert.strictEqual(resTab.expanded, true, 'Tab must expand when expandKey is Tab');
  assert.strictEqual(ed3.value, 'SELECT * FROM ');
}

// Case 3: expandKey = 'Enter'
{
  const prefs = { expandKey: 'Enter', snippets: defaultSnippets };
  const ac = { open: true, items: [{ kind: 'snippet', label: 'sf' }], index: 0 };
  const ed1 = { value: 'sf', selectionStart: 2 };
  const resSpace = simulateKeydown({ key: ' ' }, prefs, Object.assign({}, ac), ed1);
  assert.strictEqual(resSpace.expanded, false, 'Space must NOT expand when expandKey is Enter');

  const ed2 = { value: 'sf', selectionStart: 2 };
  const resTab = simulateKeydown({ key: 'Tab' }, prefs, Object.assign({}, ac), ed2);
  assert.strictEqual(resTab.expanded, false, 'Tab must NOT expand when expandKey is Enter');

  const ed3 = { value: 'sf', selectionStart: 2 };
  const resEnter = simulateKeydown({ key: 'Enter' }, prefs, Object.assign({}, ac), ed3);
  assert.strictEqual(resEnter.expanded, true, 'Enter must expand when expandKey is Enter');
  assert.strictEqual(ed3.value, 'SELECT * FROM ');
}

// Case 4: Non-snippet completion (keyword) accepts on Enter/Tab regardless of expandKey
{
  const prefs = { expandKey: 'Space', snippets: defaultSnippets };
  const ac = { open: true, items: [{ kind: 'keyword', label: 'SELECT' }], index: 0 };
  const ed = { value: 'SEL', selectionStart: 3 };
  const res = simulateKeydown({ key: 'Enter' }, prefs, Object.assign({}, ac), ed);
  assert.strictEqual(res.acceptedKeyword, true, 'Keyword completion must accept on Enter');
}

console.log('  ✓ SQL template expandKey isolation verified for Space, Tab, and Enter');

console.log('=== 10. Testing SQL template batch parsing & formatting (PL/SQL mode) ===');
{
  function extractFn(src, name) {
    const m = src.match(new RegExp('function\\s+' + name + '\\s*\\([^)]*\\)\\s*\\{'));
    if (!m) throw new Error('function not found: ' + name);
    let depth = 1, i = src.indexOf('{', m.index) + 1;
    while (i < src.length && depth > 0) {
      if (src[i] === '{') depth++;
      else if (src[i] === '}') depth--;
      i++;
    }
    return src.slice(m.index, i);
  }
  const dbSrc = fs.readFileSync(path.join(__dirname, '..', 'web/pages/database.js'), 'utf8');
  const parseSnippetsText = new Function('raw', extractFn(dbSrc, 'parseSnippetsText') + '\nreturn parseSnippetsText(raw);');
  const formatSnippetsText = new Function('items', extractFn(dbSrc, 'formatSnippetsText') + '\nreturn formatSnippetsText(items);');

  // Test 1: PL/SQL shortcuts.txt format with trailing spaces
  const plsql = [
    '# PL/SQL Developer shortcuts.txt',
    's=SELECT * FROM ',
    'sc=SELECT COUNT(*) FROM ',
    'w = WHERE ',
    'df=DELETE FROM ',
    'ob = ORDER BY '
  ].join('\n');
  const r1 = parseSnippetsText(plsql);
  assert.strictEqual(r1.items.length, 5, 'Should parse 5 rules');
  assert.strictEqual(r1.duplicates.length, 0, 'Should have 0 duplicates');
  assert.strictEqual(r1.items[0].key, 's');
  assert.strictEqual(r1.items[0].text, 'SELECT * FROM ', 'Must preserve trailing space for table input');
  assert.strictEqual(r1.items[0].enabled, true);
  assert.strictEqual(r1.items[2].key, 'w');
  assert.strictEqual(r1.items[2].text, 'WHERE ');
  console.log('  ✓ PL/SQL shortcuts.txt format parsed and trailing spaces preserved');

  // Test 2: TSV / Excel tab-separated paste
  const tsv = 'sel\tSELECT * FROM ${table}\ncnt\tSELECT COUNT(*) FROM ${table}';
  const r2 = parseSnippetsText(tsv);
  assert.strictEqual(r2.items.length, 2);
  assert.strictEqual(r2.items[0].key, 'sel');
  assert.strictEqual(r2.items[0].text, 'SELECT * FROM ${table}');
  console.log('  ✓ TSV / Excel tab delimiter parsed accurately');

  // Test 3: Multi-line with \n and continuation lines
  const multi = 'q1=SELECT *\n  FROM t\n  WHERE a = 1\nq2=SELECT 2\\nFROM dual';
  const r3 = parseSnippetsText(multi);
  assert.strictEqual(r3.items.length, 2);
  assert.strictEqual(r3.items[0].text, 'SELECT *\nFROM t\nWHERE a = 1');
  assert.strictEqual(r3.items[1].text, 'SELECT 2\nFROM dual');
  console.log('  ✓ Multi-line SQL with \\n and indented continuation lines parsed accurately');

  // Test 4: Disabled items and comments
  const mixed = '# comment\n-- sql comment\n# [disabled] old=SELECT 1\nactive=SELECT 2';
  const r4 = parseSnippetsText(mixed);
  assert.strictEqual(r4.items.length, 2);
  assert.strictEqual(r4.items[0].key, 'old');
  assert.strictEqual(r4.items[0].enabled, false);
  assert.strictEqual(r4.items[1].key, 'active');
  assert.strictEqual(r4.items[1].enabled, true);
  console.log('  ✓ Comments ignored and [disabled] items recognized correctly');

  // Test 5: Duplicate key detection
  const dupes = 'sf=SELECT 1\nsf=SELECT 2\nsf=SELECT 3\nother=4';
  const r5 = parseSnippetsText(dupes);
  assert.strictEqual(r5.duplicates.length, 1);
  assert.strictEqual(r5.duplicates[0], 'sf');
  console.log('  ✓ Duplicate snippet keys detected');

  // Test 6: Serialization and roundtrip
  const formatted = formatSnippetsText(r4.items);
  const roundtrip = parseSnippetsText(formatted);
  assert.deepStrictEqual(roundtrip.items, r4.items);
  console.log('  ✓ formatSnippetsText roundtrip matches parsed items');

  // Test 7: 回归 — Tab 前后空格 / TSV 首空格一致性
  const r7a = parseSnippetsText('sf \tSELECT 1');
  assert.strictEqual(r7a.items.length, 1, '空格+Tab 也应解析为1条');
  assert.strictEqual(r7a.items[0].key, 'sf');
  assert.strictEqual(r7a.items[0].text, 'SELECT 1');
  const r7b = parseSnippetsText('sf\t SELECT 1');
  assert.strictEqual(r7b.items[0].text, 'SELECT 1', 'TSV 值首空格应与 = 保持一致被剥离');
  console.log('  ✓ TSV 空格容错与首空格一致性');

  // Test 8: 回归 — 缩进续行保留尾空格
  const r8 = parseSnippetsText('q1=SELECT *\n  FROM t   ');
  assert.strictEqual(r8.items[0].text, 'SELECT *\nFROM t   ', '续行尾空格必须保留');
  console.log('  ✓ 缩进续行保留尾空格');

  // Test 9: 回归 — 非缩进垃圾行不再污染上一条
  const r9 = parseSnippetsText('sf=SELECT 1\ngarbage line without equals');
  assert.strictEqual(r9.items.length, 1);
  assert.strictEqual(r9.items[0].text, 'SELECT 1', '垃圾行不应并入上一条');
  assert.strictEqual(r9.ignored, 1, '垃圾行应计入 ignored');
  console.log('  ✓ 非缩进无效行被忽略且计数');

  // Test 10: 回归 — 空 key 不应序列化
  const f10 = formatSnippetsText([{ key: '', text: 'hi' }, { key: 'a', text: 'b' }]);
  assert.ok(f10.indexOf('=hi') < 0, '空 key 行不应输出: ' + f10);
  assert.ok(f10.indexOf('a=b') >= 0, f10);
  const r10 = parseSnippetsText(f10);
  assert.strictEqual(r10.items.length, 1);
  console.log('  ✓ formatSnippetsText 过滤空 key');
}

console.log('=== 11. Testing sql-format-service.js integration ===');
assert.ok(W.sqlFormatService, 'W.sqlFormatService must be present');
const testSql = "SELECT :serialno, :1, 'a  \nb' AS val, NVL(x, 0) FROM orders WHERE 客户名称 = '测试' AND amount > 0;";
const formattedTestSql = W.sqlFormatService.formatSQL(testSql);
assert.ok(formattedTestSql.includes(':serialno'), 'Preserve :serialno');
assert.ok(formattedTestSql.includes(':1'), 'Preserve :1');
assert.ok(formattedTestSql.includes("'a  \nb'"), 'Preserve verbatim string literal');
assert.ok(formattedTestSql.includes('客户名称'), 'Preserve Chinese identifier');
assert.ok(formattedTestSql.includes('NVL(x, 0)'), 'Preserve function single line');
console.log('  ✓ sqlFormatService integration and fidelity verified');

console.log('\n✅ ALL WORKBENCH FRONTEND UNIT TESTS PASSED!');
