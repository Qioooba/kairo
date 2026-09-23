'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

// 1. Load statement-model.js and sql-format-service.js
const window = { Kairo: {} };
global.window = window;

function loadScript(relPath) {
  const code = fs.readFileSync(path.join(__dirname, '..', relPath), 'utf8');
  vm.runInThisContext(code);
}

loadScript('web/workbench/statement-model.js');
loadScript('web/workbench/sql-format-service.js');

const service = window.Kairo.workbench.sqlFormatService;
const statementModel = window.Kairo.workbench.statementModel;

assert.ok(service, 'sqlFormatService must be registered on Kairo.workbench');
assert.ok(typeof service.formatSQL === 'function', 'formatSQL must be a function');
assert.ok(typeof service.tokenizeSQL === 'function', 'tokenizeSQL must be a function');

console.log('--- Running sql-format-service test suite ---');

// Test 1: Strings with whitespace and newlines must be 100% verbatim
console.log('Test 1: String literal fidelity...');
{
  const strWithTrailingSpaces = "SELECT 'a  \nb' AS val FROM dual";
  const formatted = service.formatSQL(strWithTrailingSpaces);
  assert.ok(formatted.includes("'a  \nb'"), 'Spaces and newline inside string literal must be preserved verbatim: ' + formatted);

  const strWithTripleNewline = "SELECT 'a\n\n\nb' AS val FROM dual";
  const formattedTriple = service.formatSQL(strWithTripleNewline);
  assert.ok(formattedTriple.includes("'a\n\n\nb'"), '3+ consecutive newlines in string literal must not be collapsed: ' + formattedTriple);

  const strEscaped = "SELECT 'O''Reilly' AS author FROM dual";
  const formattedEsc = service.formatSQL(strEscaped);
  assert.ok(formattedEsc.includes("'O''Reilly'"), 'Escaped quotes must remain intact: ' + formattedEsc);
}
console.log('  ✓ String literal fidelity verified');

// Test 2: Oracle Q-quotes and National Character strings
console.log('Test 2: Oracle Q-quotes and N-strings...');
{
  const qQuote = "SELECT q'[O'Reilly; select from x]' AS title FROM dual";
  const formattedQ = service.formatSQL(qQuote);
  assert.ok(formattedQ.includes("q'[O'Reilly; select from x]'"), 'Oracle q-quote must not be broken or parsed as keywords: ' + formattedQ);

  const qParen = "SELECT q'(Hello (world))' AS greeting FROM dual";
  const formattedQParen = service.formatSQL(qParen);
  assert.ok(formattedQParen.includes("q'(Hello (world))'"), 'Oracle q-quote with parentheses preserved: ' + formattedQParen);

  const nString = "SELECT N'中文内容' AS nstr FROM dual";
  const formattedN = service.formatSQL(nString);
  assert.ok(formattedN.includes("N'中文内容'"), 'N-string prefix must not be split with space: ' + formattedN);

  const nqQuote = "SELECT nq'[special text]' FROM dual";
  const formattedNQ = service.formatSQL(nqQuote);
  assert.ok(formattedNQ.includes("nq'[special text]'"), 'nq-quote preserved: ' + formattedNQ);
}
console.log('  ✓ Oracle Q-quotes and N-strings verified');

// Test 3: Oracle bind variables (:serialno, :1)
console.log('Test 3: Oracle bind variables...');
{
  const namedBind = "SELECT * FROM orders WHERE serial_no = :serialno AND user_id = :1";
  const formattedBind = service.formatSQL(namedBind);
  assert.ok(formattedBind.includes(":serialno"), ':serialno must not become : serialno: ' + formattedBind);
  assert.ok(!formattedBind.includes(": serialno"), ':serialno must not have space after colon');
  assert.ok(formattedBind.includes(":1"), ':1 numeric bind must not become : 1: ' + formattedBind);
  assert.ok(!formattedBind.includes(": 1"), ':1 must not have space after colon');
}
console.log('  ✓ Oracle bind variables verified');

// Test 4: Chinese and Unicode identifiers
console.log('Test 4: Chinese and Unicode identifiers...');
{
  const chineseSql = "SELECT 客户名称, 订单编号 FROM 业务表 WHERE 客户名称 = '张三';";
  const formattedChinese = service.formatSQL(chineseSql);
  assert.ok(formattedChinese.includes("客户名称"), '客户名称 must not be split into 客 户 名 称: ' + formattedChinese);
  assert.ok(!formattedChinese.includes("客 户 名 称"), 'Multi-char Chinese identifier was wrongly split');
  assert.ok(formattedChinese.includes("订单编号"), '订单编号 preserved');
  assert.ok(formattedChinese.includes("业务表"), '业务表 preserved');
}
console.log('  ✓ Chinese identifiers verified');

// Test 5: PL/SQL composite operators
console.log('Test 5: Composite operators...');
{
  const opAssign = "BEGIN v := 1; END;";
  const formattedAssign = service.formatSQL(opAssign);
  assert.ok(formattedAssign.includes("v := 1"), ':= must not become : =: ' + formattedAssign);
  assert.ok(!formattedAssign.includes(": ="), ':= was split into : =');

  const opArrow = "CALL p(x => 1);";
  const formattedArrow = service.formatSQL(opArrow);
  assert.ok(formattedArrow.includes("x => 1"), '=> must not become = >: ' + formattedArrow);
  assert.ok(!formattedArrow.includes("= >"), '=> was split into = >');

  const opPower = "SELECT 2**3 AS res FROM dual;";
  const formattedPower = service.formatSQL(opPower);
  assert.ok(formattedPower.includes("2**3") || formattedPower.includes("2 ** 3"), '** must remain unbroken: ' + formattedPower);
  assert.ok(!formattedPower.includes("* *"), '** was split into * *');

  const opLabel = "BEGIN <<outer_block>> NULL; END;";
  const formattedLabel = service.formatSQL(opLabel);
  assert.ok(formattedLabel.includes("<<outer_block>>"), '<< and >> labels must remain unbroken: ' + formattedLabel);
  assert.ok(!formattedLabel.includes("< <"), '<< was split');

  const opConcat = "SELECT 'a' || 'b' FROM dual;";
  const formattedConcat = service.formatSQL(opConcat);
  assert.ok(formattedConcat.includes("||"), '|| operator intact');
  assert.ok(!formattedConcat.includes("| |"), '|| was split into | |');
}
console.log('  ✓ Composite operators verified');

// Test 6: Standalone slash / for Oracle scripts & split compatibility
console.log('Test 6: Standalone slash / and statement splitting...');
{
  const script = "BEGIN NULL; END;\n/\nBEGIN NULL; END;\n/";
  const formattedScript = service.formatSQL(script);
  assert.ok(formattedScript.includes("\n/\n"), 'Standalone / must be on its own line: ' + formattedScript);

  // Now verify that statementModel.split() can split this formatted script into 2 statements
  const splits = statementModel.split(formattedScript);
  assert.strictEqual(splits.length, 2, 'statementModel.split must find 2 statements after formatting, found ' + splits.length);
}
console.log('  ✓ Standalone slash / and statement splitting verified');

// Test 7: Function calls & short argument lists (NVL, SUBSTR)
console.log('Test 7: Function arguments on single line...');
{
  const funcSql = "SELECT NVL(a.amount, 0) AS amount, SUBSTR(b.name, 1, 10) AS short_name FROM orders a, customers b;";
  const formattedFunc = service.formatSQL(funcSql);
  assert.ok(formattedFunc.includes("NVL(a.amount, 0)"), 'NVL(a.amount, 0) should remain on one line: ' + formattedFunc);
  assert.ok(formattedFunc.includes("SUBSTR(b.name, 1, 10)"), 'SUBSTR(b.name, 1, 10) should remain on one line: ' + formattedFunc);
}
console.log('  ✓ Function arguments on single line verified');

// Test 8: Compound JOIN clauses (LEFT OUTER JOIN)
console.log('Test 8: Compound JOIN clauses...');
{
  const joinSql = "SELECT a.id, b.name FROM orders a LEFT OUTER JOIN customers b ON a.customer_id = b.id;";
  const formattedJoin = service.formatSQL(joinSql);
  assert.ok(formattedJoin.includes("LEFT OUTER JOIN"), 'LEFT OUTER JOIN must be on one line: ' + formattedJoin);
  assert.ok(!formattedJoin.includes("LEFT\nOUTER") && !formattedJoin.includes("LEFT\n  OUTER"), 'LEFT OUTER JOIN was split across lines');
}
console.log('  ✓ Compound JOIN clauses verified');

// Test 9: CASE WHEN THEN ELSE END formatting
console.log('Test 9: CASE WHEN expressions...');
{
  const caseSql = "SELECT id, CASE WHEN status = 1 THEN 'Active' WHEN status = 2 THEN 'Pending' ELSE 'Unknown' END AS status_text FROM users;";
  const formattedCase = service.formatSQL(caseSql);
  assert.ok(formattedCase.includes("CASE"), 'Contains CASE');
  assert.ok(formattedCase.includes("WHEN status = 1 THEN 'Active'"), 'WHEN THEN formatted: ' + formattedCase);
  assert.ok(formattedCase.includes("ELSE 'Unknown'"), 'ELSE formatted: ' + formattedCase);
  assert.ok(formattedCase.includes("END"), 'END formatted: ' + formattedCase);
}
console.log('  ✓ CASE WHEN expressions verified');

// Test 10: Idempotence: format(format(sql)) === format(sql)
console.log('Test 10: Idempotence...');
{
  const complexSql = "SELECT a.id, NVL(a.amount, 0) AS amount, b.name FROM orders a LEFT OUTER JOIN customers b ON a.customer_id = b.id WHERE a.amount > 0 AND b.name IS NOT NULL ORDER BY a.id;";
  const round1 = service.formatSQL(complexSql);
  const round2 = service.formatSQL(round1);
  assert.strictEqual(round1, round2, 'Formatting must be idempotent!');
}
console.log('  ✓ Idempotence verified');

// Test 11: Range formatting (formatRange)
console.log('Test 11: Range formatting...');
{
  const full = "SELECT 1 FROM dual; select id, name from users; SELECT 3 FROM dual;";
  const targetSub = "select id, name from users;";
  const start = full.indexOf(targetSub);
  const end = start + targetSub.length;

  const res = service.formatRange(full, start, end);
  assert.ok(res.changed, 'Range format should mark changed');
  assert.ok(res.text.startsWith("SELECT 1 FROM dual; "), 'Prefix before selection must be untouched: ' + res.text);
  assert.ok(res.text.endsWith(" SELECT 3 FROM dual;"), 'Suffix after selection must be untouched: ' + res.text);
  assert.ok(res.text.includes("SELECT\n  id,\n  name\nFROM users;"), 'Target range was formatted properly: ' + res.text);
}
console.log('  ✓ Range formatting verified');

// Test 12: Editor simulation with mock textarea
console.log('Test 12: Editor simulation & undo support...');
{
  let inputDispatched = false;
  const mockTextarea = {
    value: "select 1 from dual; select 2 from dual;",
    selectionStart: 0,
    selectionEnd: 0,
    focus() {},
    setSelectionRange(s, e) { this.selectionStart = s; this.selectionEnd = e; },
    setRangeText(text, s, e, mode) {
      this.value = this.value.slice(0, s) + text + this.value.slice(e);
      if (mode === 'select') {
        this.selectionStart = s;
        this.selectionEnd = s + text.length;
      }
    },
    dispatchEvent(ev) {
      if (ev.type === 'input') inputDispatched = true;
    }
  };

  // Cursor at pos 2 (inside first statement)
  mockTextarea.selectionStart = 2;
  mockTextarea.selectionEnd = 2;

  const editorResult = service.formatInEditor(mockTextarea);
  assert.strictEqual(editorResult.status, 'ok');
  assert.ok(inputDispatched, 'Input event must be dispatched to trigger backup and highlight');
  assert.ok(mockTextarea.value.includes('SELECT\n  1\nFROM DUAL;'), 'First statement formatted: ' + mockTextarea.value);
  assert.ok(mockTextarea.value.includes('select 2 from dual;'), 'Second statement untouched: ' + mockTextarea.value);
}
console.log('  ✓ Editor simulation verified');

// ---------------------------------------------------------------------------
// Test 13-16: DBUI-02 —— 格式化不得改变可执行语义
// ---------------------------------------------------------------------------

// 独立的参考扫描器：只提取字符串 / 引用标识符 / 参数 / 注释，按出现顺序比较。
// 这里故意不复用被测 lexer，避免“有缺陷的词法分析器证明自身正确”。
function referenceLiterals(text, options) {
  const opts = options || {};
  const mysql = String(opts.dialect || 'oracle').toLowerCase() === 'mysql';
  const mysqlBackslash = mysql && !/NO_BACKSLASH_ESCAPES/i.test(String(opts.sqlMode || ''));
  const out = [];
  const n = text.length;
  let i = 0;
  while (i < n) {
    const c = text[i];
    const d = text[i + 1] || '';
    if (c === '-' && d === '-') {
      let e = text.indexOf('\n', i);
      if (e < 0) e = n;
      out.push('comment:' + text.slice(i, e));
      i = e;
      continue;
    }
    if (mysql && c === '#' && (i === 0 || /\s/.test(text[i - 1]))) {
      let e = text.indexOf('\n', i);
      if (e < 0) e = n;
      out.push('comment:' + text.slice(i, e));
      i = e;
      continue;
    }
    if (c === '/' && d === '*') {
      const e = text.indexOf('*/', i + 2);
      const take = e < 0 ? n : e + 2;
      out.push('comment:' + text.slice(i, take));
      i = take;
      continue;
    }
    let qPrefix = 0;
    if ((c === 'q' || c === 'Q') && d === "'") qPrefix = 2;
    else if ((c === 'n' || c === 'N') && (d === 'q' || d === 'Q') && text[i + 2] === "'") qPrefix = 3;
    if (qPrefix) {
      const opener = text[i + qPrefix];
      const closer = ({ '[': ']', '{': '}', '(': ')', '<': '>' }[opener] || opener) + "'";
      const e = text.indexOf(closer, i + qPrefix + 1);
      const take = e < 0 ? n : e + closer.length;
      out.push('string:' + text.slice(i, take));
      i = take;
      continue;
    }
    const isNString = (c === 'n' || c === 'N') && d === "'";
    if (c === "'" || c === '"' || c === '`' || isNString) {
      const q = (c === '"' || c === '`') ? c : "'";
      let j = isNString ? i + 2 : i + 1;
      while (j < n) {
        if (text[j] === q) {
          if (text[j + 1] === q) { j += 2; continue; }
          j++;
          break;
        }
        if (mysqlBackslash && q !== '`' && text[j] === '\\') { j += 2; continue; }
        j++;
      }
      out.push((q === "'" ? 'string:' : 'quoted-ident:') + text.slice(i, j));
      i = j;
      continue;
    }
    if (c === ':' && /[A-Za-z0-9_$#\u0080-\uffff]/.test(d)) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9_$#\u0080-\uffff]/.test(text[j])) j++;
      out.push('param:' + text.slice(i, j));
      i = j;
      continue;
    }
    if (c === '?') { out.push('param:?'); i++; continue; }
    if (c === '{' && d === '{') {
      const e = text.indexOf('}}', i + 2);
      if (e >= 0) { out.push('param:' + text.slice(i, e + 2)); i = e + 2; continue; }
    }
    i++;
  }
  return out;
}

// SQLite 交叉核验：格式化的核心承诺是“执行结果不变”。
let sqliteModule = null;
let sqliteSkipReason = '';
try {
  sqliteModule = require('node:sqlite');
} catch (err) {
  sqliteSkipReason = err && err.message ? err.message : String(err);
}
function sqliteScalar(sql) {
  const db = new sqliteModule.DatabaseSync(':memory:');
  try {
    const row = db.prepare(sql).get();
    return row ? Object.values(row)[0] : undefined;
  } finally {
    db.close();
  }
}
// SQLite 没有 DUAL 伪表，去掉它不改变表达式语义。
function sqliteRunnable(sql) {
  return String(sql).replace(/\bfrom\s+dual\b/gi, '').replace(/;\s*$/, '');
}

console.log('Test 13: DBUI-02 executable semantics fidelity (counterexamples)...');
{
  const counterexamples = [
    {
      name: 'A: number adjacent to a line comment',
      sql: 'select 1--comment\n+2 from dual;',
      // 注释必须在行尾结束，`+ 2` 必须留在可执行位置（缩进属于格式偏好，语义不变）。
      expected: 'SELECT\n  1 --comment\n  + 2\nFROM DUAL;',
      sqlite: 3
    },
    {
      name: 'B: Oracle ordinary string containing a backslash',
      sql: "select '\\' || 'select' as txt from dual;",
      expected: "SELECT\n  '\\' || 'select' AS txt\nFROM DUAL;",
      sqlite: '\\select'
    }
  ];

  for (const item of counterexamples) {
    const formatted = service.formatSQL(item.sql, { dialect: 'oracle' });
    // 1) 必须真的发生了格式化（防止保真校验退化成“原样返回”而假绿）
    assert.strictEqual(formatted, item.expected,
      item.name + ': 期望的格式化输出\nactual:   ' + JSON.stringify(formatted) + '\nexpected: ' + JSON.stringify(item.expected));
    // 2) 字符串 / 引用标识符 / 参数 / 注释 token 必须逐字节一致且顺序一致
    assert.deepStrictEqual(
      referenceLiterals(formatted, { dialect: 'oracle' }),
      referenceLiterals(item.sql, { dialect: 'oracle' }),
      item.name + ': 字面量与注释必须在格式化前后逐字节一致');
    // 3) 执行结果必须一致
    if (sqliteModule) {
      const before = sqliteScalar(sqliteRunnable(item.sql));
      const after = sqliteScalar(sqliteRunnable(formatted));
      assert.deepStrictEqual(after, before,
        item.name + ': SQLite 执行结果必须不变 (before=' + JSON.stringify(before) + ', after=' + JSON.stringify(after) + ')');
      assert.deepStrictEqual(after, item.sqlite, item.name + ': SQLite 基准结果');
    }
  }
  if (!sqliteModule) {
    console.log('  [SKIPPED] SQLite 交叉核验不可用 (' + sqliteSkipReason + ')，仅使用独立 token 流校验');
  }
}
console.log('  ✓ DBUI-02 counterexamples verified');

console.log('Test 14: DBUI-02 number scanning state machine...');
{
  const tokenize = (sql, options) => service.tokenizeSQL(sql, options).filter(t => t.type !== 'space').map(t => t.type + ':' + t.value);

  assert.deepStrictEqual(tokenize('SELECT 1e-2 FROM dual'),
    ['keyword:SELECT', 'number:1e-2', 'keyword:FROM', 'keyword:dual'],
    '1e-2 必须是一个合法的带符号指数数字 token');

  assert.deepStrictEqual(tokenize('SELECT 1..10 FROM dual'),
    ['keyword:SELECT', 'number:1', 'operator:..', 'number:10', 'keyword:FROM', 'keyword:dual'],
    '“..” 必须终止数字 token');

  assert.deepStrictEqual(tokenize('SELECT 1+column FROM dual'),
    ['keyword:SELECT', 'number:1', 'operator:+', 'ident:column', 'keyword:FROM', 'keyword:dual'],
    '“+” 必须终止数字 token，且不能吞掉后续标识符');

  assert.deepStrictEqual(tokenize('SELECT 1/*c*/+2 FROM dual'),
    ['keyword:SELECT', 'number:1', 'comment:/*c*/', 'operator:+', 'number:2', 'keyword:FROM', 'keyword:dual'],
    '块注释必须终止数字 token');

  assert.deepStrictEqual(tokenize('SELECT 1--c\r\n+2 FROM dual'),
    ['keyword:SELECT', 'number:1', 'comment:--c\r', 'operator:+', 'number:2', 'keyword:FROM', 'keyword:dual'],
    'CRLF 行注释必须终止数字 token');

  // 显式状态机：正负号只能紧邻指数标记，十六进制按方言单独处理
  assert.deepStrictEqual(tokenize('SELECT 1e+2 FROM dual'),
    ['keyword:SELECT', 'number:1e+2', 'keyword:FROM', 'keyword:dual']);
  assert.deepStrictEqual(tokenize('SELECT 1.5e-2 FROM dual'),
    ['keyword:SELECT', 'number:1.5e-2', 'keyword:FROM', 'keyword:dual']);
  assert.deepStrictEqual(tokenize('SELECT .5 FROM dual'),
    ['keyword:SELECT', 'number:.5', 'keyword:FROM', 'keyword:dual']);
  assert.deepStrictEqual(tokenize('SELECT 1e FROM dual'),
    ['keyword:SELECT', 'number:1', 'ident:e', 'keyword:FROM', 'keyword:dual'],
    '指数标记后缺少数字时不能吞并标识符');
}
console.log('  ✓ number scanning verified');

console.log('Test 15: DBUI-02 Oracle path literal & MySQL backslash modes...');
{
  // Oracle 普通字符串只处理成对单引号，路径中的反斜杠不是转义符
  const pathSql = "select 'C:\\dir\\' as p from dual;";
  const pathLiterals = referenceLiterals(pathSql, { dialect: 'oracle' });
  assert.deepStrictEqual(pathLiterals, ["string:'C:\\dir\\'"], '参考扫描器必须把路径字面量视为一个整体');
  const formattedPath = service.formatSQL(pathSql, { dialect: 'oracle' });
  assert.deepStrictEqual(referenceLiterals(formattedPath, { dialect: 'oracle' }), pathLiterals,
    'Oracle 路径字面量必须在格式化前后逐字节一致: ' + JSON.stringify(formattedPath));
  if (sqliteModule) {
    assert.deepStrictEqual(sqliteScalar(sqliteRunnable(formattedPath)), 'C:\\dir\\',
      'Oracle 路径字面量执行结果必须不变');
  }

  const backslashSql = "SELECT 'a\\'b' FROM t";
  const mysqlDefault = service.tokenizeSQL(backslashSql, { dialect: 'mysql' })
    .filter(t => t.type === 'string').map(t => t.value);
  assert.deepStrictEqual(mysqlDefault, ["'a\\'b'"],
    'MySQL 默认模式下反斜杠转义引号，整段是一个字符串');

  const mysqlNoEscape = service.tokenizeSQL(backslashSql, { dialect: 'mysql', sqlMode: 'STRICT_TRANS_TABLES,NO_BACKSLASH_ESCAPES' })
    .filter(t => t.type === 'string').map(t => t.value);
  assert.deepStrictEqual(mysqlNoEscape, ["'a\\'", "' FROM t"],
    'NO_BACKSLASH_ESCAPES 下反斜杠不是转义符，字符串在第二个引号处结束: ' + JSON.stringify(mysqlNoEscape));

  const oracleMode = service.tokenizeSQL(backslashSql, { dialect: 'oracle' })
    .filter(t => t.type === 'string').map(t => t.value);
  assert.deepStrictEqual(oracleMode, ["'a\\'", "' FROM t"],
    'Oracle 普通字符串不处理反斜杠转义: ' + JSON.stringify(oracleMode));
}
console.log('  ✓ Oracle path literal & MySQL backslash modes verified');

console.log('Test 16: DBUI-02 dialect options are threaded through the whole chain...');
{
  // formatSQL / formatRange 必须把 dialect 传给词法分析
  const oracleSql = "select '\\' || 'select' as txt from dual;";
  assert.deepStrictEqual(
    referenceLiterals(service.formatSQL(oracleSql, { dialect: 'oracle' }), { dialect: 'oracle' }),
    referenceLiterals(oracleSql, { dialect: 'oracle' }),
    'formatSQL 必须透传 dialect');

  const range = service.formatRange(oracleSql, 0, oracleSql.length, { dialect: 'oracle' });
  assert.deepStrictEqual(
    referenceLiterals(range.text, { dialect: 'oracle' }),
    referenceLiterals(oracleSql, { dialect: 'oracle' }),
    'formatRange 必须透传 dialect');

  // MySQL 默认模式与 Oracle 模式对同一文本必须给出不同词法结果，
  // 证明 dialect 真的到达了词法分析而不是被忽略。
  assert.notDeepStrictEqual(
    service.tokenizeSQL(oracleSql, { dialect: 'mysql' }).map(t => t.type + ':' + t.value),
    service.tokenizeSQL(oracleSql, { dialect: 'oracle' }).map(t => t.type + ':' + t.value),
    'dialect 必须影响词法分析结果');
}
console.log('  ✓ dialect threading verified');

console.log('\nAll sql-format-service tests passed successfully! ✓');
