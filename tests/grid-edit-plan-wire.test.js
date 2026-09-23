'use strict';

/**
 * tests/grid-edit-plan-wire.test.js (DBUI-01 的失败优先回归 / QA-02)
 *
 * 目的: 用 Go 真实序列化产生的契约 fixture 驱动 web/pages/database.js 的
 * getGridContext(), 证明"能力字段命名断链"确实存在。
 *
 * 为什么必须用真实 fixture:
 *   tests/grid-orderby-phase0-regression.test.js 手写的 editPlan 同时塞了
 *   `can_update: true` 和 `canUpdate: true`。真实 Go 响应只有 snake_case,
 *   于是主路径失效被 mock 掩盖。本文件只读 tests/fixtures/grid-edit-plan-summary.json
 *   (由 internal/dbconsole/grid_contract_fixture_test.go 生成), 不含任何 camelCase 别名。
 *
 * 期望(修复后): 服务端 can_update=true 时 editable 必须是真正的 boolean true。
 * 现状: database.js 读的是 camelCase `canUpdate`, 真实响应里不存在 → 编辑入口被错误关闭。
 */

const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const read = file => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');

/** loadFixture 读取 Go 生成的真实契约 fixture。 */
function loadFixture() {
  const p = path.join(__dirname, 'fixtures', 'grid-edit-plan-summary.json');
  return JSON.parse(fs.readFileSync(p, 'utf8'));
}

/** buildGetGridContext 在 vm 里装载 database.js 的 getGridContext 源码。 */
function buildGetGridContext(session, source) {
  let toasts = [];
  const state = {
    isEditMode: true,
    selectedRow: 0,
    dirtyCells: {},
    source: { id: session.sourceId, fingerprint: session.sourceFingerprint, read_only: false, environment: 'dev' },
  };

  const dbContext = {
    window: { Kairo: {} },
    state,
    sess: () => session,
    effectiveSource: () => state.source,
    canWriteDatabase: () => true,
    toast: (msg, type) => toasts.push({ msg, type }),
    currentSchema: () => 'APP',
    isPlaceholderSchema: () => false,
    updateTransactionControls: () => {},
    cellText: String,
    fmtCell: String,
    q: () => null,
    Kairo: { databaseFeatures: null },
    document: { createElement: () => ({ focus() {}, select() {} }) },
  };

  const page = source || read('web/pages/database.js');
  const start = page.indexOf('  function getGridContext(');
  const end = page.indexOf('  async function commitPendingEdits(');
  assert.ok(start >= 0 && end > start, 'database.js 中必须能定位 getGridContext 源码片段');

  const code = page.slice(start, end);
  vm.runInNewContext(code + '\nwindow.getGridContext = getGridContext;', dbContext);
  return { ctx: dbContext, toasts: toasts };
}

function main() {
  const fixture = loadFixture();
  const page = read('web/pages/database.js');

  // ---- 用例 1: 主键定位, 服务端允许更新 → 必须可编辑 ----
  {
    const plan = fixture.cases.primary_key;
    assert.strictEqual(plan.can_update, true, 'fixture 前置条件: primary_key 样例应允许更新');

    const session = {
      type: 'query',
      sourceId: 'src-1',
      sourceFingerprint: 'fp-1',
      transactionId: 'tab-1',
      executedSQL: 'SELECT ID, NAME, EMAIL FROM APP.USERS',
      lastSQL: 'SELECT ID, NAME, EMAIL FROM APP.USERS',
      isEditMode: true,
      columns: [{ name: 'ID' }, { name: 'NAME' }, { name: 'EMAIL' }],
      rows: [[1, 'Alice', 'a@example.test']],
      summary: { ordered: false, result_id: plan.result_id },
      // 关键: 只放真实 Go 序列化出来的字段, 不放任何 camelCase 别名
      editPlan: plan,
    };

    const built = buildGetGridContext(session, page);
    const got = built.ctx.window.getGridContext();
    assert.ok(got, 'getGridContext 必须返回非空上下文');

    assert.strictEqual(
      typeof got.editable, 'boolean',
      `editable 必须是真正的 boolean (响应中 can_update=${plan.can_update}); got=${typeof got.editable} (${JSON.stringify(got.editable)})`
    );
    assert.strictEqual(
      got.editable, true,
      '服务端 can_update=true 但前端读的是 camelCase canUpdate → 编辑入口被错误关闭 (DBUI-01)'
    );
  }

  // ---- 用例 2: 只读计划 → 必须严格不可编辑且给出原因 ----
  {
    const plan = fixture.cases.read_only;
    const session = {
      type: 'query',
      sourceId: 'src-1',
      sourceFingerprint: 'fp-1',
      transactionId: 'tab-1',
      executedSQL: 'SELECT COUNT(*) AS CNT FROM APP.ORDERS',
      lastSQL: 'SELECT COUNT(*) AS CNT FROM APP.ORDERS',
      isEditMode: true,
      columns: [{ name: 'CNT' }],
      rows: [[7]],
      summary: { ordered: false, result_id: plan.result_id },
      editPlan: plan,
    };

    const built = buildGetGridContext(session, page);
    const got = built.ctx.window.getGridContext();
    assert.ok(got, 'getGridContext 必须返回非空上下文');
    assert.strictEqual(typeof got.editable, 'boolean', 'editable 必须是 boolean (即使不可编辑)');
    assert.strictEqual(got.editable, false, '只读计划必须不可编辑');
  }

  // ---- 用例 3: 无主键 Oracle 堆表的隐藏 ROWID 定位, 服务端允许更新 → 必须可编辑 ----
  {
    const plan = fixture.cases.oracle_rowid;
    assert.strictEqual(plan.can_update, true, 'fixture 前置条件: oracle_rowid 样例应允许更新');

    const session = {
      type: 'query',
      sourceId: 'src-ora',
      sourceFingerprint: 'fp-ora',
      transactionId: 'tab-ora',
      executedSQL: 'SELECT ID, NAME FROM LOG_TABLE',
      lastSQL: 'SELECT ID, NAME FROM LOG_TABLE',
      isEditMode: true,
      columns: [{ name: 'ID' }, { name: 'NAME' }],
      rows: [[1, 'Alice', 'AAASDMAABAAAL9DAAA']],
      summary: { ordered: false, result_id: plan.result_id },
      editPlan: plan,
    };

    const built = buildGetGridContext(session, page);
    const got = built.ctx.window.getGridContext();
    assert.ok(got, 'getGridContext 必须返回非空上下文');
    assert.strictEqual(typeof got.editable, 'boolean', 'editable 必须是 boolean');
    assert.strictEqual(got.editable, true, 'oracle_rowid 计划 (can_update=true) 必须允许网格编辑');
    // DB-06: 广告的隐藏行身份下标必须在列绑定里可取到，否则前端拿不到定位载荷。
    const hiddenBinding = (got.editColumns || []).find(function (c) { return c.index === plan.hidden_rowid_index; });
    assert.ok(hiddenBinding, 'hidden_rowid_index 必须能在列绑定中定位');
    assert.strictEqual(hiddenBinding.writable, false, '行尾隐藏定位列不得可写');
  }

  console.log('grid-edit-plan-wire passed: 真实契约下编辑能力判定与 boolean 严格性均正确');
}

try {
  main();
} catch (e) {
  console.error('grid-edit-plan-wire FAILED (DBUI-01 现状):', e && e.message ? e.message : e);
  process.exit(1);
}
