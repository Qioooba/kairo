'use strict';

/**
 * tests/grid-edit-plan-contract.test.js (QA-02)
 *
 * 网格编辑能力契约测试 —— 直接消费 Go 侧真实序列化产生的 fixture:
 *   tests/fixtures/grid-edit-plan-summary.json
 *
 * 为什么要有这个文件:
 *   旧的前端回归测试手写 editPlan mock, 并且"新旧字段都填上"
 *   (`can_update: true` + `canUpdate: true`)。真实 Go 响应只有 snake_case,
 *   于是主流程的契约断链被 mock 掩盖。本文件只读 Go 生成的真实 fixture,
 *   Go 侧 json tag 一改, fixture 校验先红, 这里也跟着红。
 *
 * 生成端: internal/dbconsole/grid_contract_fixture_test.go
 *   UPDATE_FIXTURES=1 go test ./internal/dbconsole/ -run TestGridEditPlanSummaryContractFixture
 */

const assert = require('assert');
const fs = require('fs');
const path = require('path');

const FIXTURE_PATH = path.join(__dirname, 'fixtures', 'grid-edit-plan-summary.json');

// SNAKE_CASE 只允许小写字母/数字/下划线
const SNAKE_CASE = /^[a-z0-9_]+$/;
// camelCase 探测: 出现大写字母即视为驼峰
const HAS_UPPER = /[A-Z]/;

const IDENTITY_POLICIES = ['pk', 'oracle_rowid', 'unique', 'none'];

function loadFixture() {
  let raw;
  try {
    raw = fs.readFileSync(FIXTURE_PATH, 'utf8');
  } catch (e) {
    throw new Error(
      `无法读取真实契约 fixture: ${FIXTURE_PATH}\n` +
      '重新生成: UPDATE_FIXTURES=1 go test ./internal/dbconsole/ -run TestGridEditPlanSummaryContractFixture'
    );
  }
  return JSON.parse(raw);
}

function main() {
  const fixture = loadFixture();

  // 1. fixture 必须声明它来自 Go 真实序列化, 不能是手写 mock
  assert.ok(fixture._comment, 'fixture 必须带 _comment 说明来源');
  assert.ok(
    String(fixture.generated_by || '').includes('grid_contract_fixture_test.go'),
    `fixture.generated_by 必须指向 Go 生成端, got=${JSON.stringify(fixture.generated_by)}`
  );
  assert.ok(fixture.cases && typeof fixture.cases === 'object', 'fixture.cases 必须是对象');

  const caseNames = Object.keys(fixture.cases).sort();
  assert.ok(caseNames.length >= 3, `fixture 至少要有 3 个能力组合样例, got=${caseNames.length}`);
  for (const required of ['oracle_rowid', 'primary_key', 'read_only']) {
    assert.ok(caseNames.includes(required), `fixture 必须包含样例 ${required}, got=${caseNames.join(',')}`);
  }

  // 2. 逐样例校验线上契约形状
  for (const name of caseNames) {
    const plan = fixture.cases[name];
    assert.ok(plan && typeof plan === 'object', `${name}: 样例必须是对象`);

    // 2a. 只能出现 snake_case 字段 —— camelCase 一旦出现说明有人给前端加了第二套协议
    for (const key of Object.keys(plan)) {
      assert.ok(
        SNAKE_CASE.test(key),
        `${name}: 线上字段 ${JSON.stringify(key)} 不是 snake_case (前端必须统一读 snake_case, ` +
        '不得依赖 camelCase 别名)'
      );
    }

    // 2b. 明确禁止的 camelCase 别名
    for (const forbidden of ['canUpdate', 'canInsert', 'canDelete', 'resultId', 'identityPolicy',
      'hiddenRowidIndex', 'primaryKeys', 'uniqueKeys', 'hasTopLevelOrder']) {
      assert.ok(
        !(forbidden in plan),
        `${name}: 契约中不得存在 camelCase 字段 ${forbidden}`
      );
    }

    // 2c. 必需字段
    for (const required of ['result_id', 'schema', 'table', 'identity_policy',
      'can_insert', 'can_update', 'can_delete', 'has_top_level_order']) {
      assert.ok(
        Object.prototype.hasOwnProperty.call(plan, required),
        `${name}: 契约缺少必需字段 ${required}`
      );
    }

    // 2d. 能力字段必须是真正的 boolean, 不允许 undefined / 字符串 / null
    for (const boolField of ['can_insert', 'can_update', 'can_delete', 'has_top_level_order']) {
      assert.strictEqual(
        typeof plan[boolField], 'boolean',
        `${name}: ${boolField} 必须是 boolean, got ${typeof plan[boolField]} (${JSON.stringify(plan[boolField])})`
      );
    }

    // 2e. identity_policy 必须是已知枚举
    assert.ok(
      IDENTITY_POLICIES.includes(plan.identity_policy),
      `${name}: identity_policy=${JSON.stringify(plan.identity_policy)} 不在 ${IDENTITY_POLICIES.join('/')} 中`
    );

    // 2f. result_id 不能为空 —— 它是服务端不可变编辑上下文的句柄
    assert.ok(
      typeof plan.result_id === 'string' && plan.result_id.length > 0,
      `${name}: result_id 必须是非空字符串`
    );

    // 2g. 任何字段名都不允许含大写字母 (与 2a 互为冗余, 便于定位)
    for (const key of Object.keys(plan)) {
      assert.ok(!HAS_UPPER.test(key), `${name}: 字段 ${key} 含大写字母, 疑似驼峰协议混入`);
    }

    // 2h. DBUI-01: columns 是按结果索引关联的只读列绑定摘要, 必须存在且形状严格
    assert.ok(Array.isArray(plan.columns), `${name}: 必须携带 columns 列绑定数组`);
    assert.strictEqual(
      plan.columns.length,
      new Set(plan.columns.map(c => c.index)).size,
      `${name}: columns 的 index 不得重复`
    );
    for (const col of plan.columns) {
      assert.ok(col && typeof col === 'object', `${name}: columns 每一项必须是对象`);
      for (const key of Object.keys(col)) {
        assert.ok(SNAKE_CASE.test(key), `${name}: 列绑定字段 ${JSON.stringify(key)} 不是 snake_case`);
      }
      assert.strictEqual(typeof col.index, 'number', `${name}: 列绑定 index 必须是数字`);
      assert.strictEqual(typeof col.result_name, 'string', `${name}: 列绑定 result_name 必须是字符串`);
      assert.strictEqual(
        typeof col.writable, 'boolean',
        `${name}: 列绑定 writable 必须是真正的 boolean, got ${typeof col.writable}`
      );
      if (col.physical_name !== undefined) {
        assert.strictEqual(typeof col.physical_name, 'string', `${name}: physical_name 必须是字符串`);
      }
      if (col.writable === false) {
        assert.ok(
          typeof col.read_only_reason === 'string' && col.read_only_reason.length > 0,
          `${name}: 不可写列必须给出 read_only_reason (前端要当场显示原因)`
        );
      }
    }
    const indices = plan.columns.map(c => c.index);
    for (let i = 0; i < plan.columns.length; i++) {
      assert.ok(indices.includes(i), `${name}: columns 必须覆盖结果列索引 ${i}（按索引下标对齐）`);
    }
  }

  // 3. 定位能力与能力字段的一致性
  const pk = fixture.cases.primary_key;
  assert.strictEqual(pk.identity_policy, 'pk', 'primary_key 样例必须是 pk 定位');
  assert.ok(Array.isArray(pk.primary_keys) && pk.primary_keys.length > 0,
    'primary_key 样例必须带 primary_keys');
  assert.strictEqual(pk.can_update, true, 'primary_key 样例应允许更新');
  assert.ok(!('hidden_rowid_index' in pk),
    'primary_key 样例不应出现 hidden_rowid_index (omitempty: 0 必须被省略)');

  const rid = fixture.cases.oracle_rowid;
  assert.strictEqual(rid.identity_policy, 'oracle_rowid', 'oracle_rowid 样例必须是 rowid 定位');
  assert.strictEqual(typeof rid.hidden_rowid_index, 'number',
    'oracle_rowid 样例必须带数字类型的 hidden_rowid_index');
  assert.ok(rid.hidden_rowid_index > 0, 'oracle_rowid 样例的 hidden_rowid_index 应指向行尾追加列');
  assert.ok(!('primary_keys' in rid), 'oracle_rowid 样例不应带 primary_keys');
  // DB-06: 广告的隐藏下标必须真的落在列绑定数组内，否则前端拿不到行身份载荷。
  const hiddenBinding = rid.columns.find(c => c.index === rid.hidden_rowid_index);
  assert.ok(hiddenBinding, `hidden_rowid_index=${rid.hidden_rowid_index} 必须能在 columns 中找到对应绑定`);
  assert.strictEqual(hiddenBinding.writable, false, '行尾隐藏定位列不得可写');
  assert.strictEqual(rid.can_update, true, 'oracle_rowid 样例在身份载荷交付后才允许更新');

  const ro = fixture.cases.read_only;
  assert.strictEqual(ro.identity_policy, 'none', 'read_only 样例必须是 none 定位');
  assert.strictEqual(ro.can_insert, false, 'read_only 样例 must 禁止插入');
  assert.strictEqual(ro.can_update, false, 'read_only 样例必须禁止更新');
  assert.strictEqual(ro.can_delete, false, 'read_only 样例必须禁止删除');
  assert.ok(typeof ro.reason === 'string' && ro.reason.length > 0,
    'read_only 样例必须给出不可编辑原因');

  console.log(`grid-edit-plan-contract passed: ${caseNames.length} 个真实契约样例 (${caseNames.join(', ')})`);
}

try {
  main();
} catch (e) {
  console.error('grid-edit-plan-contract FAILED:', e && e.message ? e.message : e);
  process.exit(1);
}
