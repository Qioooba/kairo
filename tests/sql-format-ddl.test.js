'use strict';
// tests/sql-format-ddl.test.js
//
// 审核/用户反馈第 5 项回归：有的表 DDL 点开后没被格式化。
//
// 现象与根因：
//   - formatTokens 是"子句式"格式化器（SELECT/INSERT/UPDATE 那套），
//     `CREATE TABLE T ("A" NUMBER, "B" VARCHAR2(10), CONSTRAINT ...) SEGMENT ... PCTFREE 10 ...`
//     会被拼成一整行 —— 看起来就是"没格式化"；
//   - `CREATE UNIQUE INDEX ...` 更是原样返回（changed=false，lastFormatDiagnostic 还是 null）。
//
// 约束：DDL 排版只能改空白与关键字大小写 —— 字符串/引号标识符/注释/绑定参数必须逐字节不变，
// 且必须幂等（format(format(x)) === format(x)）。

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
const fmt = sql => service.formatSQL(sql, { dialect: 'oracle' });

const TABLE = `CREATE TABLE "KAIRO_RO"."KAIRO_TX_TEST_0905" ( "ID" NUMBER NOT NULL ENABLE, "NOTE" VARCHAR2(200), CONSTRAINT "PK_KAIRO_TX" PRIMARY KEY ("ID") USING INDEX PCTFREE 10 INITRANS 2 MAXTRANS 255 COMPUTE STATISTICS STORAGE(INITIAL 65536 NEXT 1048576) TABLESPACE "USERS" ENABLE ) SEGMENT CREATION IMMEDIATE PCTFREE 10 PCTUSED 40 INITRANS 1 MAXTRANS 255 NOCOMPRESS LOGGING STORAGE(INITIAL 65536 NEXT 1048576) TABLESPACE "USERS"`;
const INDEX = `CREATE UNIQUE INDEX "KAIRO_RO"."IDX_T1" ON "KAIRO_RO"."T1" ("ID") PCTFREE 10 INITRANS 2 MAXTRANS 255 COMPUTE STATISTICS STORAGE(INITIAL 65536 NEXT 1048576) TABLESPACE "USERS"`;
const ALTER = `ALTER TABLE "KAIRO_RO"."T1" ADD ("C1" NUMBER(10,2) DEFAULT 0 NOT NULL, "C2" VARCHAR2(20))`;

// 1. 表 DDL：列清单必须逐行缩进（不再是一整行）
{
  const out = fmt(TABLE);
  const lines = out.split('\n');
  assert.ok(lines.length >= 6, '表 DDL 必须分行，实际 ' + lines.length + ' 行:\n' + out);
  assert.ok(/^CREATE TABLE .*\($/.test(lines[0]), '首行应为 CREATE TABLE … (，实际: ' + lines[0]);
  assert.ok(/^ {2}"ID" NUMBER NOT NULL ENABLE,$/.test(lines[1]), '列清单应 2 空格缩进且带逗号，实际: ' + lines[1]);
  assert.ok(/^ {2}"NOTE" VARCHAR2\(200\),$/.test(lines[2]), '第二列同样缩进，实际: ' + lines[2]);
  assert.ok(lines.some(l => /^ {2}CONSTRAINT "PK_KAIRO_TX" PRIMARY KEY \("ID"\)$/.test(l)), '约束应独立成行');
  assert.ok(lines.some(l => /^\) SEGMENT CREATION IMMEDIATE$/.test(l)), '闭合括号行应带首个存储子句');
  assert.ok(lines.some(l => /^TABLESPACE "USERS"$/.test(l)), '存储子句应各自成行');
  // 表名/列名必须逐字保留（含双引号与大小写）
  ['"KAIRO_RO"."KAIRO_TX_TEST_0905"', '"ID"', '"NOTE"', '"PK_KAIRO_TX"'].forEach(t => {
    assert.ok(out.includes(t), 'DDL 排版不得改动标识符: ' + t);
  });
  assert.strictEqual(service.lastFormatDiagnostic(), null, '不应触发保真回退');
  assert.strictEqual(fmt(out), out, 'DDL 排版必须幂等');
}

// 2. 索引 DDL：旧实现原样返回（等于没格式化）
{
  const out = fmt(INDEX);
  assert.notStrictEqual(out, INDEX, '索引 DDL 必须被格式化，不能原样返回');
  const lines = out.split('\n');
  assert.ok(/^CREATE UNIQUE INDEX .*\($/.test(lines[0]), '首行应为 CREATE UNIQUE INDEX … (，实际: ' + lines[0]);
  assert.ok(/^ {2}"ID"$/.test(lines[1]), '索引列应缩进成行，实际: ' + lines[1]);
  assert.ok(/^\)/.test(lines[2]), '闭合括号应独立成行，实际: ' + lines[2]);
  assert.strictEqual(fmt(out), out, '索引 DDL 排版必须幂等');
}

// 3. ALTER TABLE ... ADD (...) 同样分行
{
  const out = fmt(ALTER);
  const lines = out.split('\n');
  assert.ok(lines.length >= 4, 'ALTER TABLE 新增列必须分行:\n' + out);
  assert.ok(/^ALTER TABLE .* ADD \($/.test(lines[0]), '实际: ' + lines[0]);
  assert.strictEqual(fmt(out), out, 'ALTER 排版必须幂等');
}

// 4. 含注释的 DDL：不强行重排（回退原格式化器），但注释必须原样保留
{
  const sql = 'CREATE TABLE T1 (\n  ID NUMBER, -- 主键\n  NAME VARCHAR2(50)\n)';
  const out = fmt(sql);
  assert.ok(out.includes('-- 主键'), '注释必须原样保留: ' + out);
  assert.strictEqual(service.lastFormatDiagnostic(), null);
}

// 5. 非 DDL 语句的格式化行为不受影响（回归保护）
{
  const select = 'select id, name from emp where id = 1 order by id';
  const out = fmt(select);
  assert.ok(/^SELECT\n/.test(out), 'SELECT 仍走原有子句排版:\n' + out);
  assert.ok(out.includes('FROM emp') && out.includes('ORDER BY id'), out);
}

console.log('DDL formatting regressions passed');
