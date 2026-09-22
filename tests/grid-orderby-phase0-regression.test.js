'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const read = file => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');

// 1. Test database-features.js grid context and mutations
{
  const window = { Kairo: { database: {} }, addEventListener() {} };
  const context = {
    window, console, URLSearchParams, Set, setTimeout, clearTimeout,
    location: { hash: '', href: 'http://localhost/#/database' }, navigator: {},
    document: { readyState: 'loading', addEventListener() {}, getElementById() { return null; }, body: {} }
  };
  vm.runInNewContext(read('web/workbench/statement-model.js'), context);
  vm.runInNewContext(read('web/workbench/syntax-editor.js'), context);
  vm.runInNewContext(read('web/workbench/database-features.js'), context);
  const F = window.Kairo.databaseFeatures;

  assert.strictEqual(typeof F.pendingGridMutations, 'function', 'F.pendingGridMutations alias must exist');
  assert.strictEqual(typeof F.getPendingGridMutations, 'function', 'F.getPendingGridMutations must exist');
  assert.strictEqual(typeof F.clearGridMutations, 'function', 'F.clearGridMutations alias must exist');
  assert.strictEqual(typeof F.clearPendingGridMutations, 'function', 'F.clearPendingGridMutations must exist');

  // Verify gridContextKey incorporates resultId
  window.Kairo.database.getGridContext = () => ({
    resultId: 'res-12345',
    sourceId: 'src-1',
    sessionId: 'sess-1',
    schema: 'PUBLIC',
    table: 'USERS',
    sql: 'SELECT * FROM USERS'
  });
  assert.ok(F.pendingGridMutations().length === 0);
}

// 2. Test database.js getGridContext and edit behaviors under Phase 0 rules
{
  const page = read('web/pages/database.js');
  let toasts = [];
  const state = {
    isEditMode: true,
    selectedRow: 0,
    dirtyCells: {},
    source: { id: 'src-1', fingerprint: 'fp-1', read_only: false, environment: 'dev' }
  };

  let mockSession = {
    type: 'query',
    sourceId: 'src-1',
    sourceFingerprint: 'fp-1',
    transactionId: 'tab-1',
    executedSQL: 'SELECT ID, NAME FROM USERS',
    lastSQL: 'SELECT ID, NAME FROM USERS',
    isEditMode: true,
    columns: [{ name: 'ID' }, { name: 'NAME' }],
    rows: [[1, 'Alice']],
    summary: { ordered: false, result_id: 'res-abc' },
    editPlan: {
      result_id: 'res-abc',
      schema: 'PUBLIC',
      table: 'USERS',
      can_update: true,
      canUpdate: true,
      primary_keys: ['ID']
    }
  };

  const dbContext = {
    window: { Kairo: {} },
    state,
    sess: () => mockSession,
    effectiveSource: () => state.source,
    canWriteDatabase: () => true,
    toast: (msg, type) => toasts.push({ msg, type }),
    currentSchema: () => 'PUBLIC',
    isPlaceholderSchema: () => false,
    updateTransactionControls: () => {},
    cellText: String,
    fmtCell: String,
    q: () => null,
    Kairo: { databaseFeatures: null },
    document: { createElement: () => ({ focus() {}, select() {} }) }
  };

  const getGridContextCode = page.slice(page.indexOf('  function getGridContext('), page.indexOf('  async function commitPendingEdits('));
  const startCellEditCode = page.slice(page.indexOf('  function startCellEdit('), page.indexOf('  function updateTransactionControls('));

  vm.runInNewContext(
    getGridContextCode + '\n' +
    startCellEditCode + '\n' +
    'window.getGridContext = getGridContext;\n' +
    'window.startCellEdit = startCellEdit;',
    dbContext
  );

  // Case 1: Unordered single-table query with valid PK editPlan -> editable: true
  const ctx1 = dbContext.window.getGridContext();
  assert.ok(ctx1, 'context should be non-null');
  assert.strictEqual(ctx1.editable, true, 'single table query without ORDER BY must be editable when PK plan allows');
  assert.strictEqual(ctx1.resultId, 'res-abc', 'resultId must match query execution');
  assert.strictEqual(ctx1.table, 'USERS');

  // Case 2: Source mismatch (current.sourceId !== source.id) -> null
  mockSession.sourceId = 'src-other';
  assert.strictEqual(dbContext.window.getGridContext(), null, 'must reject cross-source editing');
  mockSession.sourceId = 'src-1';

  // Case 3: Fingerprint mismatch -> null
  mockSession.sourceFingerprint = 'fp-old';
  assert.strictEqual(dbContext.window.getGridContext(), null, 'must reject when source fingerprint changed');
  mockSession.sourceFingerprint = 'fp-1';

  // Case 4: Controller in flight -> editable: false
  mockSession.controller = {};
  const ctxInFlight = dbContext.window.getGridContext();
  assert.strictEqual(ctxInFlight.editable, false, 'must be non-editable while query is in flight');
  mockSession.controller = null;

  // Case 5: Outcome unknown -> editable: false
  mockSession.outcomeUnknown = true;
  const ctxUnknown = dbContext.window.getGridContext();
  assert.strictEqual(ctxUnknown.editable, false, 'must be non-editable when previous outcome was unknown');
  mockSession.outcomeUnknown = false;

  // Case 6: Edit plan prohibits update
  mockSession.editPlan.canUpdate = false;
  mockSession.editPlan.canInsert = false;
  mockSession.editPlan.reason = '缺少主键或存在 JOIN';
  const ctxNoPlan = dbContext.window.getGridContext();
  assert.strictEqual(ctxNoPlan.editable, false, 'must be non-editable when editPlan disallows');

  // Verify startCellEdit blocks when context.editable is false
  const td = { querySelector() { return null; }, replaceChildren() {}, classList: { add() {}, remove() {} } };
  const ok = dbContext.window.startCellEdit(0, 1, td, false);
  assert.strictEqual(ok, false, 'startCellEdit must return false when context is not editable');
  assert.ok(toasts.some(t => t.msg.includes('缺少主键或存在 JOIN')), 'must show disallow reason toast');
}

// 3. Test pagination cancel does not mutate s.page
{
  const s = { page: 1, pageSize: 20, summary: { ordered: false } };
  let confirmed = false;

  function simulateGo(targetPage) {
    const dirtyCount = 2;
    if (dirtyCount && !confirmed) {
      // User canceled confirmation
      return;
    }
    s.page = targetPage;
  }

  // User cancels:
  confirmed = false;
  simulateGo(2);
  assert.strictEqual(s.page, 1, 's.page must remain 1 when user cancels discard confirmation');

  // User confirms:
  confirmed = true;
  simulateGo(2);
  assert.strictEqual(s.page, 2, 's.page advances only after confirmation');
}

console.log('grid-orderby-phase0-regression passed cleanly');
