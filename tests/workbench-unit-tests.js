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

console.log('\n✅ ALL WORKBENCH FRONTEND UNIT TESTS PASSED!');
