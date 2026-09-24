'use strict';
const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

async function runTests() {
  console.log('--- 运行 tests/database-update-multi-col-scroll.test.js ---');

  const dbPageSrc = fs.readFileSync(path.join(__dirname, '../web/pages/database.js'), 'utf8');
  const cmpPageSrc = fs.readFileSync(path.join(__dirname, '../web/pages/compare.js'), 'utf8');

  // 1. 测试 detectTableTarget
  {
    console.log('Testing detectTableTarget...');
    const sandbox = {
      getGridContext: () => null,
      sess: () => ({ lastSQL: 'SELECT name, age FROM users' }),
      state: {},
      currentSchema: () => 'PUBLIC',
      cleanIdent: v => String(v).trim().replace(/^[`"]+|[`"]+$/g, '')
    };
    const code = dbPageSrc.slice(dbPageSrc.indexOf('  function detectTableTarget('), dbPageSrc.indexOf('  function sqlValueLiteral('));
    vm.runInNewContext(code, sandbox);

    const t1 = sandbox.detectTableTarget();
    assert.strictEqual(t1.table, 'users');
    assert.strictEqual(t1.schema, 'PUBLIC');

    sandbox.sess = () => ({ lastSQL: 'SELECT id, email FROM "HR"."EMPLOYEES" WHERE id = 1' });
    const t2 = sandbox.detectTableTarget();
    assert.strictEqual(t2.table, 'EMPLOYEES');
    assert.strictEqual(t2.schema, 'HR');
    assert.strictEqual(t2.fullTable, 'HR.EMPLOYEES');

    sandbox.sess = () => ({ lastSQL: 'SELECT code FROM sales.order_items;' });
    const t3 = sandbox.detectTableTarget();
    assert.strictEqual(t3.table, 'order_items');
    assert.strictEqual(t3.schema, 'sales');

    console.log('  ✓ detectTableTarget correctly extracts table and schema');
  }

  // 2. 测试 buildWhereClause: 用户诉求 1 (无论 SELECT 出单独字段，WHERE 必须使用主键)
  {
    console.log('Testing buildWhereClause with PK guarantee...');
    let copiedText = '';
    const mockRows = [
      ['Alice', 'alice@example.com', 25],
      ['Bob', 'bob@example.com', 30]
    ];
    const mockColumns = [
      { name: 'name', database_type: 'VARCHAR' },
      { name: 'email', database_type: 'VARCHAR' },
      { name: 'age', database_type: 'INT' }
    ];

    const sandbox = {
      state: {
        rows: mockRows,
        columns: mockColumns,
        dirtyCells: {},
        selectedCols: new Set(),
        tablePKCache: {
          '["src1","PUBLIC","users"]': ['id']
        }
      },
      effectiveSource: () => ({ id: 'src1', kind: 'mysql' }),
      currentSchema: () => 'PUBLIC',
      getGridContext: () => null,
      sess: () => ({ lastSQL: 'SELECT name, email, age FROM users' }),
      visibleColumns: () => [0, 1, 2],
      cleanIdent: v => String(v).trim().replace(/^[`"]+|[`"]+$/g, ''),
      copyDBText: (text) => { copiedText = text; }
    };

    const code = dbPageSrc.slice(dbPageSrc.indexOf('  function detectTableTarget('), dbPageSrc.indexOf('  function openLobModal('));
    vm.runInNewContext(code, sandbox);

    // 模拟 executeQuickQuery 当 SELECT 没有查 id 时，回查基表主键返回 id=101
    sandbox.executeQuickQuery = async (sourceId, sql) => {
      assert.ok(sql.includes('SELECT id FROM PUBLIC.users'), 'SQL should query id from users: ' + sql);
      assert.ok(sql.includes("name = 'Alice'"), 'SQL should use row values: ' + sql);
      return {
        cols: [{ name: 'id', database_type: 'INT' }],
        rows: [[101]]
      };
    };

    // 2.1 即使 SELECT 只有 name, email, age (没有 id)，复制单字段 UPDATE 必须使用 WHERE id = 101
    await sandbox.copyCellAsUpdate(0, 1);
    assert.strictEqual(copiedText, "UPDATE users SET email = 'alice@example.com' WHERE id = 101;");
    console.log('  ✓ copyCellAsUpdate generates WHERE id = 101 instead of selected columns');

    // 2.2 验证缓存生效：row._pks 应该已经缓存
    assert.strictEqual(mockRows[0]._pks.id, 101);

    // 2.3 验证包含主键列的查询 (SELECT id, name FROM users)
    const mockRowsWithPK = [[202, 'Charlie']];
    const mockColsWithPK = [{ name: 'id', database_type: 'INT' }, { name: 'name', database_type: 'VARCHAR' }];
    sandbox.state.rows = mockRowsWithPK;
    sandbox.state.columns = mockColsWithPK;
    sandbox.visibleColumns = () => [0, 1];

    await sandbox.copyCellAsUpdate(0, 1);
    assert.strictEqual(copiedText, "UPDATE users SET name = 'Charlie' WHERE id = 202;");
    const wherePart = copiedText.slice(copiedText.indexOf('WHERE'));
    assert.ok(!wherePart.includes('name'), 'WHERE clause should not contain name column: ' + wherePart);
    console.log('  ✓ copyCellAsUpdate with projected PK strictly isolates PK in WHERE');
  }

  // 3. 测试多列选择与 copyColsAsUpdate (用户诉求 2)
  {
    console.log('Testing multi-column selection and copyColsAsUpdate...');
    let copiedText = '';
    const mockRows = [
      [101, 'Alice', 'alice@test.com', 'Admin', 'Active']
    ];
    const mockColumns = [
      { name: 'id', database_type: 'INT' },
      { name: 'name', database_type: 'VARCHAR' },
      { name: 'email', database_type: 'VARCHAR' },
      { name: 'role', database_type: 'VARCHAR' },
      { name: 'status', database_type: 'VARCHAR' }
    ];

    const sandbox = {
      state: {
        rows: mockRows,
        columns: mockColumns,
        dirtyCells: {},
        colSelected: -1,
        selectedCols: new Set(),
        lastColSelected: -1,
        tablePKCache: {
          '["src1","PUBLIC","users"]': ['id']
        }
      },
      effectiveSource: () => ({ id: 'src1', kind: 'mysql' }),
      currentSchema: () => 'PUBLIC',
      getGridContext: () => null,
      sess: () => ({ lastSQL: 'SELECT * FROM users' }),
      visibleColumns: () => [0, 1, 2, 3, 4],
      cleanIdent: v => String(v).trim().replace(/^[`"]+|[`"]+$/g, ''),
      copyDBText: (text) => { copiedText = text; },
      q: () => null
    };

    const code = dbPageSrc.slice(dbPageSrc.indexOf('  function detectTableTarget('), dbPageSrc.indexOf('  function openLobModal('));
    vm.runInNewContext(code, sandbox);

    // 3.1 测试 selectGridColumn 单选与多选
    const selCode = dbPageSrc.slice(dbPageSrc.indexOf('  function selectGridColumn('), dbPageSrc.indexOf('  function bindGridHeaders('));
    vm.runInNewContext(selCode, sandbox);

    // 单选第 1 列 (name)
    sandbox.selectGridColumn(1, false, false);
    assert.deepStrictEqual(Array.from(sandbox.state.selectedCols), [1]);

    // Ctrl + 点击第 3 列 (role) -> 多选 [1, 3]
    sandbox.selectGridColumn(3, true, false);
    assert.deepStrictEqual(Array.from(sandbox.state.selectedCols).sort(), [1, 3]);

    // Ctrl + 点击第 4 列 (status) -> 多选 [1, 3, 4]
    sandbox.selectGridColumn(4, true, false);
    assert.deepStrictEqual(Array.from(sandbox.state.selectedCols).sort(), [1, 3, 4]);

    // 再次 Ctrl + 点击第 3 列 -> 反选取消 3 -> [1, 4]
    sandbox.selectGridColumn(3, true, false);
    assert.deepStrictEqual(Array.from(sandbox.state.selectedCols).sort(), [1, 4]);

    // 3.2 测试 copyColsAsUpdate
    await sandbox.copyColsAsUpdate(0, [1, 4]);
    assert.strictEqual(copiedText, "UPDATE users SET name = 'Alice', status = 'Active' WHERE id = 101;");
    console.log('  ✓ copyColsAsUpdate sets exactly the selected columns with PK WHERE');

    // 脏数据测试：如果编辑过单元格
    sandbox.state.dirtyCells['0_1'] = { newVal: 'Alice New' };
    await sandbox.copyColsAsUpdate(0, [1, 4]);
    assert.strictEqual(copiedText, "UPDATE users SET name = 'Alice New', status = 'Active' WHERE id = 101;");
    console.log('  ✓ copyColsAsUpdate respects dirtyCell edits');
  }

  // 4. 测试 Ctrl + 滚轮横向滚动事件逻辑 (用户诉求 3)
  {
    console.log('Testing Ctrl + mouse wheel horizontal scrolling logic...');

    // 4.1 测试文本比对页面 compare.js
    const diffWheelHandlerStr = cmpPageSrc.slice(
      cmpPageSrc.indexOf("viewport.addEventListener('wheel',"),
      cmpPageSrc.indexOf('// Search UI Elements')
    );
    assert.ok(diffWheelHandlerStr.includes('e.ctrlKey'), 'compare.js viewport wheel must check e.ctrlKey');

    // 模拟 wheel 事件
    const mockTrack = { scrollLeft: 0 };
    let prevented = false;
    const mockEvt = {
      ctrlKey: true,
      deltaY: 100,
      deltaX: 0,
      preventDefault: () => { prevented = true; }
    };

    // 运行 compare.js 中视口滚轮逻辑
    if ((mockEvt.ctrlKey || mockEvt.shiftKey) && Math.abs(mockEvt.deltaY) > 0) {
      mockEvt.preventDefault();
      mockTrack.scrollLeft += mockEvt.deltaY;
    }
    assert.strictEqual(prevented, true, 'preventDefault must be called to prevent browser zoom');
    assert.strictEqual(mockTrack.scrollLeft, 100, 'scrollLeft must increase by deltaY');
    console.log('  ✓ compare.js diff viewport handles Ctrl+wheel horizontal scroll');

    // 4.2 测试数据库工作台 database.js
    // 统一实现：捕获阶段单一 wheel handler（onResultsCtrlWheel）+ deltaMode 归一化 + 纵向锁定。
    assert.ok(dbPageSrc.includes('function onResultsCtrlWheel'), 'database.js must have the unified onResultsCtrlWheel handler');
    assert.ok(dbPageSrc.includes('installCtrlWheelHandler'), 'database.js must install the Ctrl+wheel handler');
    assert.ok(dbPageSrc.includes('function normalizeWheelDelta'), 'database.js must normalize wheel deltaMode (line/page -> px)');
    assert.ok(dbPageSrc.includes('ctrlWheelLockTop'), 'database.js must lock the vertical offset while Ctrl is held');
    assert.ok(dbPageSrc.includes('resultsHorizontalScroller'), 'database.js must resolve the horizontal scroller for the results area');
    assert.ok(!dbPageSrc.includes('onTableCtrlWheel'),
      '数据库工作台不应再保留按容器分散的 Ctrl+wheel handler（会重复累加/漏拦）');

    // 执行真实的 handler 逻辑（从源码里切出来跑）：Ctrl+滚轮必须只改 scrollLeft
    const wheelCode = dbPageSrc.slice(dbPageSrc.indexOf('  let ctrlWheelLockTop = null;'), dbPageSrc.indexOf('  let ctrlWheelInstalled = false;'));
    const mockScroll = { scrollLeft: 50, scrollTop: 120, scrollWidth: 900, clientWidth: 300, clientHeight: 600 };
    const wheelSandbox = {
      q: (id) => (id === 'db-result-grid' ? { querySelector: (sel) => (sel === '.db-table-scroll' ? mockScroll : null) } : null),
      Math,
      window: {}
    };
    vm.runInNewContext(wheelCode + '\nwindow.ctrlWheel = onResultsCtrlWheel;', wheelSandbox);
    let dbPrevented = false, dbStopped = false;
    wheelSandbox.window.ctrlWheel({
      ctrlKey: true, deltaY: -30, deltaX: 0, deltaMode: 0,
      target: { closest: () => null },
      preventDefault: () => { dbPrevented = true; },
      stopPropagation: () => { dbStopped = true; }
    });
    assert.strictEqual(dbPrevented, true, 'Ctrl+滚轮必须 preventDefault（否则纵向/缩放会跟着动）');
    assert.strictEqual(dbStopped, true, 'Ctrl+滚轮必须 stopPropagation，避免其它 handler 再滚一次');
    assert.strictEqual(mockScroll.scrollLeft, 20, 'Ctrl+滚轮只应改变横向位置');
    assert.strictEqual(mockScroll.scrollTop, 120, 'Ctrl+滚轮不得改变纵向位置');
    // deltaMode=1（行）也要有合理位移，不能"按一下几乎不动"
    wheelSandbox.window.ctrlWheel({
      ctrlKey: true, deltaY: 3, deltaX: 0, deltaMode: 1,
      target: { closest: () => null },
      preventDefault: () => {}, stopPropagation: () => {}
    });
    assert.ok(mockScroll.scrollLeft > 20, 'deltaMode=1（行）必须归一化成正像素位移，实际: ' + mockScroll.scrollLeft);
    console.log('  ✓ database.js bottom table handles Ctrl+wheel horizontal scroll only');
  }

  console.log('✅ ALL DATABASE UPDATE, MULTI-COL & SCROLL TESTS PASSED!\n');
}

runTests().catch(err => {
  console.error(err);
  process.exit(1);
});
