'use strict';

// Pure-function smoke tests for the low-cost database editor layer.  The
// browser-facing observer is held in loading state so this stays dependency
// free and runs on the same Node binary used by CI.
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');

const source = fs.readFileSync(require('path').join(__dirname, '..', 'web/workbench/database-features.js'), 'utf8');
const document = {
  readyState: 'loading',
  addEventListener() {},
  getElementById() { return null; },
  querySelectorAll() { return []; },
  body: {}
};
const window = { Kairo: { core: { escapeHtml(value) { return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); } } }, addEventListener() {} };
vm.runInNewContext(source, { window, document, console, Set, URLSearchParams, MutationObserver: function () {}, navigator: {}, location: { hash: '' }, setTimeout, clearTimeout });
const features = window.Kairo.databaseFeatures;

const sql = "SELECT ':not_a_bind', q'[a;b]' AS txt, :user_id FROM users -- :ignored\nWHERE id=:user_id; SELECT 2;";
const tokens = features.tokenizeSQL(sql);
assert.strictEqual(features.findParameters(sql).map(item => item.name).join(','), 'user_id');
assert.strictEqual(features.findParameters('SELECT ? FROM users WHERE id = ?').map(item => item.name).join(','), '1,2');
assert.strictEqual(features.splitStatements(sql).length, 2);
assert.ok(tokens.some(token => token.type === 'string' && token.value.includes('a;b')));
assert.ok(features.highlightSQL(sql).includes('db-pro-sql-kw'));
const formatted = features.formatSQL("SELECT 'a; b' AS x -- keep;\nFROM users WHERE id=1;");
assert.ok(formatted.includes("'a; b'"), 'formatter must preserve string contents');
assert.ok(formatted.includes('-- keep;'), 'formatter must preserve comments');
const errorLocation = features.parseErrorLocation('ORA-06550: line 12, column 7');
assert.strictEqual(errorLocation.line, 12);
assert.strictEqual(errorLocation.column, 7);
assert.strictEqual(errorLocation.source, 'line-column');
assert.strictEqual(features.locationOffset('a\nbcd', { line: 2, column: 3 }), 4);
assert.strictEqual(JSON.stringify(features.parseCSV('a,"b,c"\n1,"x"')), JSON.stringify([['a', 'b,c'], ['1', 'x']]));
console.log('database-features-unit: ok');

for (const sql of ['SELECT * FROM users', 'SELECT id, name FROM users WHERE id=1', 'SELECT "ID" FROM "APP"."USERS"']) {
  assert.ok(features.resolveGridTarget(sql, 'app'), sql);
}
for (const sql of ['SELECT a.id FROM a JOIN b ON a.id=b.id', 'SELECT a.id FROM a,b', 'SELECT id AS other FROM users', 'SELECT count(*) FROM users', "SELECT 'FROM users' FROM dual", 'SELECT * FROM (SELECT * FROM users)', 'SELECT * FROM a UNION SELECT * FROM b']) {
  assert.strictEqual(features.resolveGridTarget(sql, ''), null, sql);
}
assert.strictEqual(features.resolveGridTarget('SELECT * FROM `app`.`users`', '').table, 'users');
console.log('grid target regression: ok');
