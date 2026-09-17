'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const read = file => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');
const marked = [];
const window = { Kairo: { database: { markTransactionPending: (...args) => marked.push(args) } }, addEventListener() {} };
const context = { window, console, URLSearchParams, Set, setTimeout, clearTimeout,
  location: { hash: '', href: 'http://localhost/#/database' }, navigator: {},
  document: { readyState: 'loading', addEventListener() {}, getElementById() { return null; }, body: {} } };
for (const file of ['statement-model', 'syntax-editor']) vm.runInNewContext(read('web/workbench/' + file + '.js'), context);
vm.runInNewContext(read('web/workbench/database-features.js').replace('  F.tokenizeSQL =', '  F.testSyncMutation = syncMutationTransaction;\n  F.tokenizeSQL ='), context);
const F = window.Kairo.databaseFeatures, W = window.Kairo.workbench;
assert.strictEqual(JSON.stringify(F.parseCSV('\n\r\na,b\n,\n1,2\n\n3,4')), JSON.stringify([['a', 'b'], ['1', '2'], ['3', '4']]));
assert.strictEqual(JSON.stringify(F.parseCSV('a,"x\ny"\n1,"a""b"')), JSON.stringify([['a', 'x\ny'], ['1', 'a"b']]));
F.testSyncMutation({ sessionId: 'tab' }, {}, true);
assert.strictEqual(marked.length, 0, 'missing transaction metadata must not invent a pending transaction');
F.testSyncMutation({ sessionId: 'tab' }, { transaction_pending: true }, true);
F.testSyncMutation({ sessionId: 'tab' }, { result: { rolled_back: true } }, false);
assert.deepStrictEqual(marked, [[true, 'tab'], [false, 'tab']]);
assert.ok(!F.buildDeepLink({ sql: 'SELECT 1', sourceId: 'one', autoRun: true }).includes('autorun'));
assert.ok(W.statementModel.normalize('BEGIN NULL; END;\n/', 5).text.endsWith('END;'));
const html = W.syntaxEditor.highlight('SELECT "FROM <img src=x>" FROM t', 'sql');
assert.ok(!html.includes('<img'));
assert.ok(html.includes('syn-string'));
assert.ok(!W.syntaxEditor.highlight('SELECT '.repeat(40000), 'sql').includes('<span'));
let writes = 0, callbacks = 0;
const listeners = {};
const textarea = { value: 'SELECT 1', scrollTop: 12, scrollLeft: 8,
  addEventListener: (name, fn) => { listeners[name] = fn; },
  removeEventListener: (name, fn) => { if (listeners[name] === fn) delete listeners[name]; } };
const code = { set innerHTML(value) { writes++; } };
const binding = W.syntaxEditor.bind(textarea, code, { language: 'sql', onInput() { callbacks++; } });
listeners.scroll(); listeners.scroll();
assert.strictEqual(writes, 1); assert.strictEqual(callbacks, 1);
textarea.value = 'SELECT 2'; listeners.input(); assert.strictEqual(writes, 2);
binding.dispose(); assert.strictEqual(Object.keys(listeners).length, 0);
const page = read('web/pages/database.js');
const guard = page.slice(page.indexOf('  function sqlGuardInfo('), page.indexOf('  async function runQuery('));
vm.runInNewContext(guard + '\nwindow.guard = sqlGuardInfo;', { window, Kairo: window.Kairo });
assert.strictEqual(window.guard("SELECT 'COMMIT' FROM dual").action, 'SELECT');
assert.strictEqual(window.guard("SELECT q'[WHERE; DROP TABLE t]' FROM dual").writes, false);
assert.strictEqual(window.guard('SELECT * FROM t FOR UPDATE').writes, true);
assert.strictEqual(window.guard('WITH c AS (SELECT 1) DELETE FROM t').action, 'DELETE');
assert.ok(F.formatSQL('SELECT SUBSTR(col, 1, 10) FROM t').includes('SUBSTR(col, 1, 10)'));
assert.strictEqual(F.resolveGridTarget('select * from hr.users', '', 'oracle').table, 'USERS');
assert.strictEqual(F.resolveGridTarget('select * from "hr"."users"', '', 'oracle').table, 'users');
assert.strictEqual(W.statementModel.split("SELECT '\\' FROM dual; SELECT 2 FROM dual").length, 2);
const cellState = { rows: [['hello']], columns: [{name:'NAME'}], dirtyCells: {}, isEditMode:true };
const td = { querySelector() { return null; }, replaceChildren(input) { this.input=input; }, classList: { add() {}, remove() {} } };
const editCode = page.slice(page.indexOf('  function startCellEdit('), page.indexOf('  function updateTransactionControls('));
const editContext = { window, state:cellState, sess:()=>({}), canWriteDatabase:()=>true, toast:()=>{},
  document:{createElement:()=>({focus(){},select(){}})}, cellText:String, fmtCell:String, updateTransactionControls(){} };
vm.runInNewContext(editCode+'\nwindow.edit=startCellEdit;',editContext);
window.edit(0,0,td,true); assert.strictEqual(cellState.dirtyCells['0_0'].newVal,null);
console.log('Database review regressions passed');
(async function () {
  const code = page.slice(page.indexOf('  async function boundedExportBlob('), page.indexOf('  async function exportResult('));
  vm.runInNewContext(code+'\nwindow.readExport=boundedExportBlob;', {window, Blob});
  let cancelled=false, released=false, index=0;
  const reader={async read(){return index++ < 2 ? {value:new Uint8Array(4),done:false}:{done:true};}, async cancel(){cancelled=true;}, releaseLock(){released=true;}};
  const response={headers:{get(){return null;}},body:{getReader(){return reader;}}};
  await assert.rejects(window.readExport(response,6), /内存下载上限/);
  assert.ok(cancelled && released);
  const dying={id:1, sourceId:'src', transactionId:'tab', transactionPending:true, dirtyCells:{one:{newVal:'changed'}}};
  const state={sessions:[dying,{id:2}],activeId:2};
  const closeCode=page.slice(page.indexOf('  async function closeSession('),page.indexOf('  function hideObjectSession('));
  vm.runInNewContext(closeCode+'\nwindow.closeTab=closeSession;',{window,state,confirm:()=>true,api:async()=>{throw new Error('offline');},toast(){},updateTransactionControls(){}});
  await window.closeTab(1);
  assert.strictEqual(state.sessions.length,2);
  assert.strictEqual(dying.transactionPending,true);
  assert.ok(dying.dirtyCells.one);

  // DB-09: outcome_unknown guards and state preservation
  const txSession = { id: 3, sourceId: 'src', transactionId: 'tab-tx', transactionPending: true, gridEditsStaged: true, dirtyCells: { '0_0': { newVal: 'updated' } } };
  let toasts = [], queryMsgs = [];
  const txContext = {
    sess: () => txSession,
    effectiveSource: () => ({ id: 'src', read_only: false }),
    canWriteDatabase: () => true,
    getGridContext: () => ({ schema: 'test', table: 'users' }),
    api: async (method, path, body) => {
      if (body && body.action === 'COMMIT') {
        const err = new Error('提交确认丢失或连接中断，事务最终状态未知');
        err.data = { ok: false, code: 'OUTCOME_UNKNOWN', outcome: 'outcome_unknown', effect_status: 'unknown' };
        throw err;
      }
      return { ok: true };
    },
    toast: (msg, type) => toasts.push({ msg, type }),
    showQueryMessage: (kind, title, msg) => queryMsgs.push({ kind, title, msg }),
    bindSession: () => {},
    refreshVisibleResult: () => {},
    updateTransactionControls: () => {},
    state: { dirtyCells: txSession.dirtyCells },
    window: {}
  };
  const commitCode = page.slice(page.indexOf('  async function commitPendingEdits('), page.indexOf('  // Public bridge for feature modules'));
  const rollbackCode = page.slice(page.indexOf('  async function rollbackPendingEdits('), page.indexOf('  /* 会话定时自动备份与防丢'));
  vm.runInNewContext(commitCode + '\n' + rollbackCode + '\nwindow.commitPending=commitPendingEdits;\nwindow.rollbackPending=rollbackPendingEdits;', txContext);

  await txContext.window.commitPending();
  assert.strictEqual(txSession.outcomeUnknown, true, 'must set outcomeUnknown to true on commit failure');
  assert.strictEqual(txSession.transactionPending, false, 'transaction on server is terminated');
  assert.ok(txSession.dirtyCells['0_0'], 'dirty cells must be preserved for user inspection');
  assert.ok(toasts.some(t => t.msg.includes('最终状态未知') || t.msg.includes('结果未知')), 'must warn user about unknown outcome');

  // Attempting to re-commit while outcomeUnknown is blocked
  let commitCalled = false;
  txContext.api = async () => { commitCalled = true; };
  await txContext.window.commitPending();
  assert.strictEqual(commitCalled, false, 'must block blind re-commit when outcome is unknown');

  // Rollback on outcomeUnknown resets state gracefully
  await txContext.window.rollbackPending();
  assert.strictEqual(txSession.outcomeUnknown, false, 'rollback must clear outcomeUnknown');
  assert.strictEqual(Object.keys(txSession.dirtyCells).length, 0, 'rollback must clear dirtyCells');

  console.log('Bounded export regression passed');
  console.log('DB-09 outcome_unknown regression passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
