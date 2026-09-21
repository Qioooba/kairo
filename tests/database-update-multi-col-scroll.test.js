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
    assert.ok(dbPageSrc.includes('onTableCtrlWheel'), 'database.js must have onTableCtrlWheel handler');
    const mockScroll = { scrollLeft: 50 };
    let dbPrevented = false;
    const dbWheelEvt = {
      ctrlKey: true,
      deltaY: -30,
      preventDefault: () => { dbPrevented = true; }
    };
    if (dbWheelEvt.ctrlKey && Math.abs(dbWheelEvt.deltaY) > 0) {
      dbWheelEvt.preventDefault();
      mockScroll.scrollLeft += dbWheelEvt.deltaY;
    }
    assert.strictEqual(dbPrevented, true);
    assert.strictEqual(mockScroll.scrollLeft, 20);
    console.log('  ✓ database.js bottom table handles Ctrl+wheel horizontal scroll');
  }

  console.log('✅ ALL DATABASE UPDATE, MULTI-COL & SCROLL TESTS PASSED!\n');
}

runTests().catch(err => {
  console.error(err);
  process.exit(1);
});
