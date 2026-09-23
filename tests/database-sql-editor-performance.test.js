const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

// Extract database.js logic for unit testing
const dbJs = fs.readFileSync(path.join(__dirname, '../web/pages/database.js'), 'utf8');
const featJs = fs.readFileSync(path.join(__dirname, '../web/workbench/database-features.js'), 'utf8');

// 把真实的 database-features.js 载入沙箱（观察者保持 loading，不会执行 start()），
// 让绑定参数断言打在真实实现上，而不是正则抽取出来的源码片段上。
function loadDatabaseFeatures() {
  const editor = {
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    dataset: {},
    addEventListener() {},
    removeEventListener() {},
    focus() {},
    setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  };
  const document = {
    readyState: 'loading',
    addEventListener() {},
    getElementById(id) { return id === 'db-sql' ? editor : null; },
    querySelectorAll() { return []; },
    body: {}
  };
  const window = {
    Kairo: { core: { escapeHtml(value) { return String(value); } } },
    addEventListener() {}
  };
  vm.runInNewContext(codeOf('web/workbench/statement-model.js'),
    { window, document, console, Set, URLSearchParams, MutationObserver: function () {}, navigator: {}, location: { hash: '' }, setTimeout, clearTimeout });
  vm.runInNewContext(codeOf('web/workbench/database-features.js'),
    { window, document, console, Set, URLSearchParams, MutationObserver: function () {}, navigator: {}, location: { hash: '' }, setTimeout, clearTimeout });
  const features = window.Kairo.databaseFeatures;
  features.__editor = editor;
  return features;
}

function codeOf(relPath) {
  return fs.readFileSync(path.join(__dirname, '..', relPath), 'utf8');
}

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

  // NOTE: 这个用例原来断言“超长 SQL 返回空参数”是正确行为，实际上它固化了 DBUI-04
  // 的功能回退（高亮降级把执行所需的绑定参数扫描一起关掉）。现已按正确行为重新锚定：
  // 高亮可以降级，绑定参数扫描不可以。
  await t.test('bind parameters must survive the large-text highlight threshold (DBUI-04)', () => {
    const features = loadDatabaseFeatures();
    const editor = features.__editor;

    // 普通参数化查询
    const sql = 'SELECT * FROM orders WHERE id = :order_id AND user_id = :user_id AND status = ?';
    const params = features.findParameters(sql);
    assert.strictEqual(params.length, 3);
    assert.strictEqual(params[0].name, 'order_id');
    assert.strictEqual(params[1].name, 'user_id');
    assert.strictEqual(params[2].name, '1');

    // 短查询 + 220KB 尾部注释：参数扫描不得被高亮阈值禁用
    const shortQuery = 'SELECT :id FROM dual;';
    const hugeSql = shortQuery + '\n/*' + 'x'.repeat(220000) + '*/';
    assert.ok(hugeSql.length > 220000, '样本必须超过高亮阈值');

    const hugeParams = features.findParameters(hugeSql);
    // 注意：vm 沙箱里的数组原型与测试 realm 不同，deepStrictEqual 会因原型不一致而失败。
    assert.strictEqual(hugeParams.map(p => p.name).join(','), 'id',
      'DBUI-04: 超过高亮阈值的文本仍必须扫描出绑定参数，实际 ' + JSON.stringify(hugeParams));

    // 执行路径：光标位于首条短语句时，绑定必须保留并随请求发送
    editor.value = hugeSql;
    editor.selectionStart = 6;
    editor.selectionEnd = 6;
    features.setBindings({ id: '123' });
    const bound = features.getBoundParameters();
    assert.strictEqual(bound.map(p => p.name).join(','), 'id',
      'DBUI-04: 执行路径必须保留首条语句的绑定参数，实际 ' + JSON.stringify(bound));
    assert.strictEqual(bound[0].value, '123');
    assert.strictEqual(features.getBindings().id, '123',
      'DBUI-04: 扫描可用时也不得删除仍然存在的绑定；扫描未完成时更不得破坏性删除');

    // 高亮降级与参数扫描互相独立：统一入口仍返回状态与参数
    const scan = features.scanParameters(hugeSql);
    assert.strictEqual(scan.status, 'ready');
    assert.strictEqual(scan.parameters.map(p => p.name).join(','), 'id');
  });

  await t.test('parameter scanning keeps the binding cache while a scan is not ready (DBUI-04)', () => {
    const features = loadDatabaseFeatures();
    const editor = features.__editor;

    editor.value = 'SELECT :a FROM dual;';
    editor.selectionStart = 7;
    editor.selectionEnd = 7;
    features.setBindings({ a: '1', b: '2' });

    // 未完成的扫描结果不得触发破坏性裁剪
    features.pruneBindingsAgainst({ status: 'pending', parameters: [], complete: false });
    assert.strictEqual(Object.keys(features.getBindings()).sort().join(','), 'a,b',
      '扫描未完成时必须保留全部绑定');

    features.pruneBindingsAgainst({ status: 'error', parameters: [], complete: false });
    assert.strictEqual(Object.keys(features.getBindings()).sort().join(','), 'a,b',
      '扫描失败时必须保留全部绑定');

    // 完成的扫描结果才允许删除确实不存在的参数
    features.pruneBindingsAgainst({ status: 'ready', parameters: [{ name: 'a' }], complete: true });
    assert.strictEqual(Object.keys(features.getBindings()).sort().join(','), 'a',
      '完成的扫描结果允许裁剪已删除的参数');
  });
});
