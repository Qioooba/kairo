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
const DB_SOURCE_PATH = path.join(__dirname, '../web/pages/database.js');
const dbJs = fs.readFileSync(DB_SOURCE_PATH, 'utf8');

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

// 从真实源文件里按大括号配平切出一个函数定义（跳过字符串/模板/注释里的括号）。
function extractFunction(src, name) {
  const at = src.indexOf('\n  function ' + name + '(');
  assert.ok(at >= 0, '未在真实源码里找到函数 ' + name);
  let depth = 0, quote = null, escaped = false, line = false, block = false;
  for (let i = src.indexOf('{', at); i < src.length; i++) {
    const ch = src[i], next = src[i + 1];
    if (line) { if (ch === '\n') line = false; continue; }
    if (block) { if (ch === '*' && next === '/') { block = false; i++; } continue; }
    if (quote) {
      if (escaped) { escaped = false; continue; }
      if (ch === '\\') { escaped = true; continue; }
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') { quote = ch; continue; }
    if (ch === '/' && next === '/') { line = true; i++; continue; }
    if (ch === '/' && next === '*') { block = true; i++; continue; }
    if (ch === '{') depth++;
    else if (ch === '}') { depth--; if (!depth) return src.slice(at + 1, i + 1); }
  }
  throw new Error('无法解析函数 ' + name);
}

/**
 * DB-03: 编辑能力默认拒绝 —— 直接跑真实的 normalizeEditPlan / getGridContext /
 * 编辑模式开关判定式，断言「没有已解析计划时一律只读」。
 *
 * 为什么要有这个文件:
 *   旧实现有两处宽松回退：getGridContext 里 `plan ? plan.can_update === true : true`、
 *   编辑模式开关里 `plan && ...`。服务端对 JOIN / 聚合 / 派生表 / 视图不下发编辑计划时，
 *   这两处会一起把只读结果变成“可编辑”，用户改完才在提交阶段失败。
 */
function checkEditDenyByDefault() {
  // 1. 宽松回退必须从真实源码里消失
  assert.ok(!/plan \? plan\.can_update === true : true/.test(dbJs),
    'DB-03: planAllows 不得保留 plan 为 null 时的宽松回退');
  assert.ok(!/if \(plan && plan\.can_update !== true && plan\.can_insert !== true\)/.test(dbJs),
    'DB-03: 编辑模式开关不得保留 plan 为 null 时的宽松回退');

  // 2. normalizeEditPlan：prep_ms 透传；plan_pending 契约已取消，前端不得依赖
  const normalizeEditPlan = eval('(' + extractFunction(dbJs, 'normalizeEditPlan') + ')');
  assert.strictEqual(normalizeEditPlan({ can_update: true, prep_ms: 12 }).prep_ms, 12,
    'DB-08: prep_ms 必须原样透传');
  assert.strictEqual(normalizeEditPlan({ can_update: true }).prep_ms, null, '缺少 prep_ms 时归一为 null');
  assert.strictEqual(normalizeEditPlan({ prep_ms: 'x' }).prep_ms, null, '非数字 prep_ms 归一为 null');
  assert.ok(!('plan_pending' in normalizeEditPlan({ can_update: true, plan_pending: true })),
    'plan_pending 契约已取消：前端不得再引入或依赖该字段');
  assert.ok(!('plan_pending' in normalizeEditPlan({ can_update: false })));

  // 3. getGridContext：resolveGridTarget 只能做显示/schema 提示，不得成为开放编辑的依据
  function contextWith(planRaw, options) {
    const cfg = options || {};
    const state = { selectedRow: 0 };
    const source = { id: 'src-1', fingerprint: 'fp-1', read_only: !!cfg.readOnly, kind: 'oracle', environment: 'dev' };
    const current = {
      id: 1, type: 'sql', editPlan: planRaw, sourceId: 'src-1', sourceFingerprint: 'fp-1',
      resultSchema: 'S', lastSQL: 'SELECT a FROM t', executedSQL: 'SELECT a FROM t',
      columns: [{ name: 'a' }], rows: [['1']], summary: null, resultId: '', transactionId: 'tx-1',
      controller: null, transactionBusy: false, outcomeUnknown: false, isEditMode: cfg.isEditMode !== false
    };
    function sess() { return current; }
    function effectiveSource() { return source; }
    function canWriteDatabase() { return cfg.canWrite !== false; }
    function isPlaceholderSchema() { return false; }
    function currentSchema() { return 'S'; }
    // 与真实 features.resolveGridTarget 同形状：能从 SQL 文本里“猜出”单表目标
    const Kairo = { databaseFeatures: { resolveGridTarget() { return { schema: 'S', table: 'T' }; } } };
    const getGridContext = eval('(' + extractFunction(dbJs, 'getGridContext') + ')');
    return getGridContext();
  }

  // 3a. 没有计划：猜出目标表也不能编辑
  const noPlan = contextWith(null);
  assert.ok(noPlan, 'resolveGridTarget 仍应给出显示用的目标表');
  assert.strictEqual(noPlan.table, 'T', 'resolveGridTarget 只作为显示/schema 提示保留');
  assert.strictEqual(noPlan.editable, false, 'DB-03: 无编辑计划时必须只读');
  assert.strictEqual(noPlan.canUpdate, false);
  assert.strictEqual(noPlan.canInsert, false);
  assert.strictEqual(noPlan.canDelete, false);

  // 3b. 明确 can_update:false（JOIN / 聚合 / 派生表 / 视图）
  assert.strictEqual(contextWith({ can_update: false, can_insert: false, table: 'T' }).editable, false,
    'DB-03: can_update:false 必须只读');
  // 3c. can_insert 不推导 can_update
  assert.strictEqual(contextWith({ can_insert: true, can_update: false, table: 'T' }).editable, false,
    'DBUI-01: 允许插入绝不推导出允许改单元格');
  // 3d. can_update 缺失（undefined）同样只读
  assert.strictEqual(contextWith({ table: 'T' }).editable, false, 'DB-03: 缺失 can_update 必须只读');
  // 3e. 只读数据源 / 无写权限 / 未开启编辑模式
  assert.strictEqual(contextWith({ can_update: true, table: 'T' }, { readOnly: true }).editable, false,
    '只读数据源不得编辑');
  assert.strictEqual(contextWith({ can_update: true, table: 'T' }, { canWrite: false }).editable, false,
    '无可写权限不得编辑');
  assert.strictEqual(contextWith({ can_update: true, table: 'T' }, { isEditMode: false }).editable, false,
    '未开启编辑模式不得编辑');
  // 3f. 已解析计划明确 can_update:true 才允许
  const allowed = contextWith({
    can_update: true, table: 'T', result_id: 'rid-1',
    columns: [{ index: 0, result_name: 'a', writable: true }]
  });
  assert.strictEqual(allowed.editable, true, 'DB-03: can_update:true 必须允许改单元格');
  assert.strictEqual(allowed.canUpdate, true);
  assert.strictEqual(allowed.resultId, 'rid-1');
  assert.strictEqual(allowed.editColumns.length, 1);

  // 4. 编辑模式开关：从真实源码里切出判定式再验证行为
  const gateMatch = dbJs.match(/const plan = normalizeEditPlan\(current\.editPlan\);\s*\n\s*if \((.*)\) \{/);
  assert.ok(gateMatch, 'DB-03: 必须能从真实源码里切出编辑模式开关的判定式');
  const denyGate = eval('(function (plan) { return ' + gateMatch[1] + '; })');
  assert.strictEqual(denyGate(null), true, 'DB-03: 没有计划时必须拒绝进入编辑模式');
  assert.strictEqual(denyGate({ can_update: false, can_insert: false }), true,
    'DB-03: 明确只读时必须拒绝进入编辑模式');
  assert.strictEqual(denyGate({ can_update: true, can_insert: false }), false);
  assert.strictEqual(denyGate({ can_insert: true, can_update: false }), false, '允许插入时可进入编辑模式');

  console.log('grid-edit-plan deny-by-default passed: getGridContext / 编辑模式开关 / normalizeEditPlan 默认拒绝');
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
  checkEditDenyByDefault();
} catch (e) {
  console.error('grid-edit-plan-contract FAILED:', e && e.message ? e.message : e);
  process.exit(1);
}
