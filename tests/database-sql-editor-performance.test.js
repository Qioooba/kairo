const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const path = require('path');

// Extract database.js logic for unit testing
const dbJs = fs.readFileSync(path.join(__dirname, '../web/pages/database.js'), 'utf8');
const featJs = fs.readFileSync(path.join(__dirname, '../web/workbench/database-features.js'), 'utf8');

test('SQL Editor Performance & Safeguards', async (t) => {
  await t.test('isLargeSQL correctly flags large character length and line count', () => {
    const m = dbJs.match(/function isLargeSQL\(text\) \{[\s\S]*?\n  \}/);
    assert.ok(m, 'isLargeSQL definition found');
    const MAX_SQL_HIGHLIGHT_CHARS = 120000;
    const MAX_SQL_HIGHLIGHT_LINES = 2000;
    const isLargeSQL = eval('(' + m[0] + ')');

    // Small query
    assert.strictEqual(isLargeSQL('SELECT 1 FROM dual;'), false);

    // Moderate 500 lines
    const midLines = Array.from({ length: 500 }, (_, i) => `SELECT ${i};`).join('\n');
    assert.strictEqual(isLargeSQL(midLines), false);

    // Over 2000 lines
    const overLines = Array.from({ length: 2005 }, (_, i) => `SELECT ${i};`).join('\n');
    assert.strictEqual(isLargeSQL(overLines), true);

    // Over 120,000 characters
    const overChars = 'SELECT ' + 'x'.repeat(125000) + ' FROM dual;';
    assert.strictEqual(isLargeSQL(overChars), true);
  });

  await t.test('matchBrackets early exits when cursor is not adjacent to bracket', () => {
    const mKw = dbJs.match(/const SQL_KEYWORDS = [^\n]+;/);
    const mTok = dbJs.match(/function tokenizeSQL\(src\) \{[\s\S]*?\n  \}/);
    const mMatch = dbJs.match(/function matchBrackets\(text, cursor\) \{[\s\S]*?\n  \}/);
    assert.ok(mKw && mTok && mMatch, 'Extracted parser functions');

    eval(mKw[0].replace('const SQL_KEYWORDS', 'global.SQL_KEYWORDS'));
    const tokenizeSQL = eval('(' + mTok[0] + ')');
    const matchBrackets = eval('(' + mMatch[0] + ')');

    const sql = 'SELECT col1, col2 FROM (SELECT id FROM users WHERE status = 1)';
    // Cursor at word 'users' (not adjacent to any bracket)
    const cursor = sql.indexOf('users');
    const res = matchBrackets(sql, cursor);
    assert.strictEqual(res, null, 'Should return null immediately without bracket parsing');

    // Cursor right after '('
    const openParen = sql.indexOf('(');
    const resOpen = matchBrackets(sql, openParen + 1);
    assert.ok(resOpen, 'Should match opening parenthesis');
    assert.strictEqual(resOpen.open, openParen);
  });

  await t.test('highlightSQL gracefully falls back to plain escaped text for large SQL', () => {
    const mKw = dbJs.match(/const SQL_KEYWORDS = [^\n]+;/);
    const mTok = dbJs.match(/function tokenizeSQL\(src\) \{[\s\S]*?\n  \}/);
    const mHl = dbJs.match(/function highlightSQL\(src, match\) \{[\s\S]*?\n  \}/);
    const mLarge = dbJs.match(/function isLargeSQL\(text\) \{[\s\S]*?\n  \}/);
    const MAX_SQL_HIGHLIGHT_CHARS = 120000;
    const MAX_SQL_HIGHLIGHT_LINES = 2000;
    global.h = function (s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); };

    eval(mKw[0].replace('const SQL_KEYWORDS', 'global.SQL_KEYWORDS'));
    global.tokenizeSQL = eval('(' + mTok[0] + ')');
    const isLargeSQL = eval('(' + mLarge[0] + ')');
    const highlightSQL = eval('(' + mHl[0] + ')');

    // Small query gets formatted with highlight spans
    const smallSql = 'SELECT * FROM users WHERE id = 1;';
    const smallHl = highlightSQL(smallSql, null);
    assert.ok(smallHl.includes('db-sql-kw'), 'Small SQL should have syntax class');

    // Over-threshold query returns plain escaped text (no span explosion)
    const largeSql = Array.from({ length: 2500 }, (_, i) => `SELECT col_${i} FROM table_${i};`).join('\n');
    const largeHl = highlightSQL(largeSql, null);
    assert.ok(!largeHl.includes('<span class='), 'Large SQL must not contain span tags');
    assert.ok(largeHl.includes('SELECT col_0 FROM table_0;'), 'Content must be preserved');
  });

  await t.test('findParameters in database-features has length cutoff and single-pass optimization', () => {
    const mTok = featJs.match(/function tokenizeSQL\(source\) \{[\s\S]*?\n  \}/);
    const mFind = featJs.match(/function findParameters\(source\) \{[\s\S]*?\n  \}/);
    const SQL_WORD = /[A-Za-z_$#\u0080-\uffff]/;
    const SQL_WORD_CONT = /[A-Za-z0-9_$#\u0080-\uffff]/;
    const KEYWORDS = new Set(['select', 'from', 'where']);
    const MAX_SQL_HIGHLIGHT = 220000;

    const tokenizeSQL = eval('(' + mTok[0] + ')');
    const findParameters = eval('(' + mFind[0] + ')');

    // Normal parameterized query
    const sql = 'SELECT * FROM orders WHERE id = :order_id AND user_id = :user_id AND status = ?';
    const params = findParameters(sql);
    assert.strictEqual(params.length, 3);
    assert.strictEqual(params[0].name, 'order_id');
    assert.strictEqual(params[1].name, 'user_id');
    assert.strictEqual(params[2].name, '1');

    // Oversized SQL returns empty array immediately
    const hugeSql = 'SELECT * FROM t WHERE id = :id ' + 'x'.repeat(230000);
    const hugeParams = findParameters(hugeSql);
    assert.strictEqual(hugeParams.length, 0, 'Oversized SQL should bypass parameter scanning');
  });
});
