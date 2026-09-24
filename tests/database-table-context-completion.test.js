'use strict';
// tests/database-table-context-completion.test.js
//
// 审核第 12 项（P2）回归：带 schema 的表名联想失效。
//
// 触发：输入 `SELECT * FROM APP.US`（缓存里已有 USERS）时，旧实现先把 `APP.US`
// 判成"别名 APP 的字段"，而该别名没有字段缓存 → 候选为空，强制补全也救不回来。
//
// 约束：表引用位置（FROM / JOIN / INTO / UPDATE / TABLE + 可选的 schema 前缀）
// 必须先于"别名.字段"判断。

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const page = fs.readFileSync(path.join(__dirname, '../web/pages/database.js'), 'utf8');

function buildEnv() {
  const sandbox = {
    window: {},
    state: { prefs: { snippets: [] } },
    document: { querySelectorAll: () => [] }
  };
  const code = page.slice(
    page.indexOf('  const SQL_KEYWORDS = new Set('),
    page.indexOf('  function matchBrackets(text, cursor) {')
  );
  vm.runInNewContext(
    code + '\nwindow.suggestSQL = suggestSQL;\nwindow.sqlTableContext = sqlTableContext;\nwindow.qualifierCompletion = qualifierCompletion;',
    sandbox
  );
  return sandbox.window;
}

const F = buildEnv();
const EXTRAS = { objects: ['USERS', 'T_ORDER', 'ORDERS'], functions: ['GETCUSTOMERID'], fields: [], snippets: [] };
const labels = result => JSON.stringify((result.items || []).map(item => item.label));

// 1. `FROM APP.US` 必须走表名联想
{
  const text = 'SELECT * FROM APP.US';
  assert.strictEqual(F.sqlTableContext(text, text.length), true,
    '`FROM APP.US` 必须被识别为表引用位置（旧实现跨不过 schema. 前缀）');
  const result = F.suggestSQL(text, text.length, EXTRAS, {});
  assert.ok(labels(result).includes('"USERS"'),
    '`APP.US` 必须给出 USERS 候选，实际: ' + labels(result));
  assert.strictEqual(text.slice(0, result.start) + 'USERS', 'SELECT * FROM APP.USERS',
    '候选替换范围必须只覆盖点号后的前缀');
}

// 1b. 联想必须"持续且完整"：输入 B → 所有 B* 表（不再被 12 条上限截断）；输入 BU → 仍有表名
{
  const bigPool = [];
  for (let i = 0; i < 30; i++) bigPool.push('BUSINESS_APPLY_' + String(i).padStart(2, '0'));
  bigPool.push('BUSINESS_APPLY', 'BATCH_LOG', 'B_ORDER');
  const extras = { objects: bigPool, functions: ['GETCUSTOMERID', 'GETBILLID'], fields: [], snippets: [] };

  const one = F.suggestSQL('SELECT * FROM B', 'SELECT * FROM B'.length, extras, {});
  const oneObjects = one.items.filter(i => i.kind === 'object');
  assert.ok(oneObjects.length > 12,
    '输入 1 个字符时表名候选不能被 12 条上限截断（用户反馈"不是联想出来所有的表"），实际 ' + oneObjects.length);
  assert.ok(oneObjects.every(i => i.label.indexOf('B') === 0), '表名必须按前缀过滤');

  const two = F.suggestSQL('SELECT * FROM BU', 'SELECT * FROM BU'.length, extras, {});
  assert.ok(labels(two).includes('"BUSINESS_APPLY"'),
    '输入第二个字符后必须继续给出表名（用户反馈"输入 BU 就没有联想了"），实际: ' + labels(two));

  const four = F.suggestSQL('SELECT * FROM BUSI', 'SELECT * FROM BUSI'.length, extras, {});
  assert.ok(labels(four).includes('"BUSINESS_APPLY"'), '前缀越长越要持续收敛，实际: ' + labels(four));
}

// 1c. 函数联想：表达式位置 1 个字符即出函数（且不夹杂关键字噪音）
{
  const extras = { objects: ['USERS'], functions: ['GETCUSTOMERID', 'GETBILLID'], fields: [], snippets: [] };
  const one = F.suggestSQL('SELECT g', 'SELECT g'.length, extras, {});
  assert.ok(labels(one).includes('"GETCUSTOMERID"'),
    '`select g` 应给出函数候选（PL/SQL Developer 手感），实际: ' + labels(one));
  assert.ok(one.items.every(i => i.kind !== 'keyword'), '1 个字符时不应夹杂关键字噪音');
  const four = F.suggestSQL("SELECT getC", "SELECT getC".length, extras, {});
  assert.ok(labels(four).includes('"GETCUSTOMERID"'), '`select getC` 必须持续联想函数，实际: ' + labels(four));
}

// 2. `FROM APP.` + 强制补全（Ctrl+Space）同样给出表名
{
  const text = 'SELECT * FROM APP.';
  assert.strictEqual(F.sqlTableContext(text, text.length), true, '`FROM APP.` 也是表引用位置');
  const forced = F.suggestSQL(text, text.length, EXTRAS, { force: true });
  assert.ok(labels(forced).includes('"USERS"'), 'Ctrl+Space 也必须能补出 USERS: ' + labels(forced));
  const aliased = F.suggestSQL('SELECT * FROM T_ORDER t JOIN APP.', 'SELECT * FROM T_ORDER t JOIN APP.'.length, EXTRAS, { force: true });
  assert.ok(labels(aliased).includes('"USERS"'), 'JOIN APP. 同样要给表名: ' + labels(aliased));
}

// 3. 未加 schema 的表名联想不受影响
{
  const text = 'SELECT * FROM US';
  assert.strictEqual(F.sqlTableContext(text, text.length), true);
  assert.ok(labels(F.suggestSQL(text, text.length, EXTRAS, {})).includes('"USERS"'), labels(F.suggestSQL(text, text.length, EXTRAS, {})));
}

// 4. 别名.字段 的行为不能被回退
{
  const text = 'SELECT * FROM T_ORDER t WHERE t.NA';
  assert.strictEqual(F.sqlTableContext(text, text.length), false,
    '`t.NA` 是字段位置，不是表引用位置');
  const qual = F.qualifierCompletion(text, text.length);
  assert.ok(qual && qual.qualifier === 't' && qual.prefix === 'NA', '限定符解析: ' + JSON.stringify(qual));
  const result = F.suggestSQL(text, text.length, Object.assign({}, EXTRAS, { fields: ['ID', 'NAME'] }), {});
  assert.strictEqual(labels(result), '["NAME"]', '别名.字段 仍只给该表字段');

  const dotOnly = 'SELECT * FROM T_ORDER t WHERE t.';
  assert.strictEqual(F.sqlTableContext(dotOnly, dotOnly.length), false);
  assert.strictEqual(
    labels(F.suggestSQL(dotOnly, dotOnly.length, Object.assign({}, EXTRAS, { fields: ['ID', 'NAME'] }), { force: true })),
    '["ID","NAME"]',
    'Ctrl+Space 在 t. 之后仍只给字段'
  );
}

// 5. 数字字面量与字符串里的点号不误判
{
  assert.strictEqual(F.sqlTableContext('SELECT 1.5 FROM DUAL', 'SELECT 1.5 FROM DUAL'.length), false);
  assert.strictEqual(F.qualifierCompletion('SELECT 1.', 'SELECT 1.'.length), null, '1. 不是限定符');
  assert.strictEqual(F.qualifierCompletion("SELECT 'x.'", "SELECT 'x.'".length), null, '字符串里的点号不是限定符');
}

console.log('Database table-context completion regressions passed');
