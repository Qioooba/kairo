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

console.log('\nAll sql-format-service tests passed successfully! ✓');
