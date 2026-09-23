'use strict';
// tests/database-row-identity-update.test.js
//
// 审核第 3 项（P1）回归：复制 / 导出 UPDATE 时把选中的第二行定位成第一行。
//
// 触发：表里两行 NAME 都是 'shared'，执行 `SELECT ROWID, NAME FROM APP.T` 后隐藏 ROWID 列，
// 选中第二行 → 旧实现发现结果未投影主键，就用可见列回查主键并带 `ROWNUM <= 1`，
// 于是拿到任意第一条匹配记录的 ID=1，生成的 UPDATE 指向错误的行（错误主键还会缓存进 _pks）。
//
// 三条约束：
//   1. 结果里已经携带 ROWID（或主键）时优先使用，不再回查猜测；
//   2. 禁止用非唯一字段 + ROWNUM<=1 / LIMIT 1 推断行身份（改为最多取两行、唯一才认账）；
//   3. 无法证明唯一行身份时拒绝生成 UPDATE，而不是退回"所有可见列拼 WHERE"的兜底。

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const page = fs.readFileSync(path.join(__dirname, '../web/pages/database.js'), 'utf8');

function buildSandbox(options) {
  const copied = [];
  const toasts = [];
  const queries = [];
  const sandbox = {
    state: {
      rows: options.rows,
      columns: options.columns,
      dirtyCells: {},
      hiddenColumns: new Set(options.hidden || []),
      tablePKCache: { '["src1","APP","T"]': options.pks || [] }
    },
    effectiveSource: () => ({ id: 'src1', kind: 'oracle' }),
    currentSchema: () => 'APP',
    getGridContext: () => null,
    sess: () => ({ lastSQL: 'SELECT ROWID, NAME FROM APP.T' }),
    visibleColumns: () => options.visible,
    cleanIdent: value => String(value).trim().replace(/^[`"]+|[`"]+$/g, ''),
    copyDBText: text => copied.push(text),
    toast: (msg, type) => toasts.push({ msg, type }),
    q: () => null,
    TextEncoder,
    api: async () => ({ fields: [] })
  };
  const code = page.slice(page.indexOf('  function detectTableTarget('), page.indexOf('  function openLobModal('));
  vm.runInNewContext(code, sandbox);
  sandbox.executeQuickQuery = async (sourceId, sql) => {
    queries.push(sql);
    return options.lookup ? options.lookup(sql) : { cols: [], rows: [] };
  };
  return { sandbox, copied, toasts, queries };
}

(async function run() {
  // 1. 结果已携带 ROWID → 必须直接用 ROWID，且不得再发起任何回查
  {
    const env = buildSandbox({
      rows: [['ROWID_A', 'shared'], ['ROWID_B', 'shared']],
      columns: [{ name: 'ROWID', database_type: 'VARCHAR' }, { name: 'NAME', database_type: 'VARCHAR' }],
      visible: [1], // ROWID 列被隐藏
      pks: ['ID'],
      // 旧实现的回查：NAME='shared' + ROWNUM<=1 会返回任意第一行（ID=1）
      lookup: () => ({ cols: [{ name: 'ID', database_type: 'INT' }], rows: [[1]] })
    });
    await env.sandbox.copyRowAsUpdate(1);
    assert.strictEqual(env.copied.length, 1, '应生成 UPDATE');
    assert.ok(env.copied[0].includes("WHERE ROWID = 'ROWID_B'"),
      '选中第二行必须用第二行的 ROWID，实际: ' + env.copied[0]);
    assert.ok(!env.copied[0].includes('ID = 1'),
      '不得使用非唯一字段回查出来的错误主键，实际: ' + env.copied[0]);
    assert.strictEqual(env.queries.length, 0, '结果已携带 ROWID 时不得再回查猜测');
  }

  // 2. 回查命中多行（同名记录）→ 拒绝生成 UPDATE，且不得缓存错误主键
  {
    const env = buildSandbox({
      rows: [['shared']],
      columns: [{ name: 'NAME', database_type: 'VARCHAR' }],
      visible: [0],
      pks: ['ID'],
      lookup: () => ({ cols: [{ name: 'ID', database_type: 'INT' }], rows: [[1], [2]] })
    });
    await env.sandbox.copyRowAsUpdate(0);
    assert.strictEqual(env.copied.length, 0, '无法唯一定位时必须拒绝生成 UPDATE');
    assert.ok(env.toasts.some(t => t.type === 'err' && t.msg.includes('无法唯一确定')),
      '必须给出"无法唯一确定行身份"的错误提示');
    assert.strictEqual(env.sandbox.state.rows[0]._pks, undefined, '歧义结果不得写入主键缓存');
  }

  // 3. 回查唯一命中 → 仍然可以生成（保留原有能力，不因修复而退化）
  {
    const env = buildSandbox({
      rows: [['shared']],
      columns: [{ name: 'NAME', database_type: 'VARCHAR' }],
      visible: [0],
      pks: ['ID'],
      lookup: () => ({ cols: [{ name: 'ID', database_type: 'INT' }], rows: [[101]] })
    });
    await env.sandbox.copyRowAsUpdate(0);
    assert.strictEqual(env.copied.length, 1);
    assert.ok(env.copied[0].includes('WHERE ID = 101'), '唯一命中时仍应使用主键定位: ' + env.copied[0]);
    assert.strictEqual(env.queries.length, 1);
    assert.ok(/ROWNUM\s*<=\s*2/i.test(env.queries[0]) || /LIMIT\s+2/i.test(env.queries[0]),
      '回查必须限定最多两行（唯一性判定），实际: ' + env.queries[0]);
  }

  // 4. 既没有主键也没有 ROWID、也没有 ID 列 → 拒绝生成（不再用可见列硬拼 WHERE）
  {
    const env = buildSandbox({
      rows: [['shared', 'x']],
      columns: [{ name: 'NAME', database_type: 'VARCHAR' }, { name: 'MEMO', database_type: 'VARCHAR' }],
      visible: [0, 1],
      pks: [],
      lookup: null
    });
    await env.sandbox.copyRowAsUpdate(0);
    assert.strictEqual(env.copied.length, 0,
      '无法证明唯一行身份时必须拒绝生成 UPDATE（旧实现会用所有可见列拼 WHERE）');
    assert.ok(env.toasts.some(t => t.type === 'err'));
  }

  // 5. 导出行为 UPDATE（导出文件链路）同样受闸门保护
  {
    const env = buildSandbox({
      rows: [['ROWID_A', 'shared'], ['ROWID_B', 'shared']],
      columns: [{ name: 'ROWID', database_type: 'VARCHAR' }, { name: 'NAME', database_type: 'VARCHAR' }],
      visible: [1],
      pks: ['ID'],
      lookup: () => ({ cols: [{ name: 'ID', database_type: 'INT' }], rows: [[1]] })
    });
    let blobContent = null;
    env.sandbox.downloadBlob = (blob) => { blobContent = blob && blob.content; };
    env.sandbox.Blob = function (parts) { this.content = parts.join(''); };
    await env.sandbox.exportRowAsUpdateFile(1);
    assert.ok(blobContent && blobContent.includes("WHERE ROWID = 'ROWID_B'"),
      '导出文件同样必须用第二行的 ROWID，实际: ' + blobContent);
  }

  console.log('Database row identity UPDATE regressions passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
