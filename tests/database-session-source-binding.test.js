'use strict';
// tests/database-session-source-binding.test.js
//
// 审核第 1 项（P1）回归：关闭其他页签后"运行脚本"可能执行到错误的数据源。
//
// 触发链：批量关闭页签后活动页签变成 A，但页面保存的数据源与顶部 #db-source 仍是 B；
// 脚本执行从下拉框取 source_id、从活动页签取事务 session_id，于是把
// { source_id: 'B', session_id: 'tx_A' } 发给后端，SQL 落到 B 上执行。
//
// 这里用真实的页面函数（从 web/pages/database.js、web/workbench/database-features.js
// 里切出来的原文）配合 DOM/API 替身复现并守住两条约束：
//   1. 批量关闭页签必须复用完整激活流程（同步页面数据源 + 顶部下拉框 + 重建工作区）；
//   2. 脚本执行必须取活动页签的数据源，且在它与顶部下拉框不一致时拒绝执行。

const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const read = file => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');
const page = read('web/pages/database.js');
const features = read('web/workbench/database-features.js');

function slice(src, from, to) {
  const start = src.indexOf(from);
  const end = src.indexOf(to);
  assert.ok(start >= 0, 'slice start not found: ' + from);
  assert.ok(end > start, 'slice end not found: ' + to);
  return src.slice(start, end);
}

function makeSession(id, sourceId, extra) {
  return Object.assign({ id, sourceId, sql: '' }, extra || {});
}

// ---------------------------------------------------------------------------
// 1. 批量关闭页签后的激活流程
// ---------------------------------------------------------------------------
function buildTabEnv() {
  const sourceA = { id: 'src-A', name: '源A' };
  const sourceB = { id: 'src-B', name: '源B' };
  const state = { sessions: [], activeId: '', sources: [sourceA, sourceB], source: sourceB };
  const calls = { renderWorkspace: 0, restoreSessionChrome: [], bindSession: [], orphan: 0 };
  const els = { 'db-source': { value: 'src-B' } };
  const context = {
    state,
    calls,
    q: id => els[id] || null,
    sessionSourceClass: () => 'ok',
    renderTabs: () => {},
    renderWorkspace: () => { calls.renderWorkspace++; },
    restoreSessionChrome: s => { calls.restoreSessionChrome.push(s && s.id); },
    renderObjectViewer: () => {},
    renderOrphanWorkspace: () => { calls.orphan++; },
    markSessionOrphan: () => {},
    bindSession: s => { calls.bindSession.push(s && s.id); },
    hideObjectSession: () => {},
    hideComplete: () => {},
    saveEditorSQL: () => {},
    backupDBSessions: () => {},
    cancelGridPaint: () => {},
    updateTransactionControls: () => {},
    createSession: () => makeSession('fresh', ''),
    api: async () => ({ ok: true }),
    toast: () => {},
    confirm: () => true,
    window: {}
  };
  context.window = context;
  const code = slice(page, '  async function closeSessionsList(', '  function renderTabs(')
    + slice(page, '  function switchSession(', '  async function closeSession(');
  vm.runInNewContext(code + '\nwindow.closeSessionsList = closeSessionsList;\nwindow.activateSessionTab = activateSessionTab;\nwindow.switchSession = switchSession;', context);
  return { context, state, calls, els };
}

(async function run() {
  // 1a. 关闭其他页签：活动页签回到 A（绑 src-A），页面数据源必须同步
  {
    const env = buildTabEnv();
    env.state.sessions = [makeSession('tab-A', 'src-A'), makeSession('tab-B', 'src-B')];
    env.state.activeId = 'tab-B';
    await env.context.window.closeSessionsList([env.state.sessions[1]], 'tab-A');
    assert.strictEqual(env.state.activeId, 'tab-A', '活动页签必须回到 A');
    assert.strictEqual(env.state.source && env.state.source.id, 'src-A',
      '活动页签换成 A 后页面数据源必须同步到 A（否则脚本会打到 B）');
    assert.strictEqual(env.els['db-source'].value, 'src-A', '顶部数据源下拉框必须同步到 A');
    assert.strictEqual(env.calls.renderWorkspace > 0, true, '数据源变化时必须重建工作区');
    assert.deepStrictEqual(env.calls.restoreSessionChrome, [],
      '数据源变化时不能再走"只恢复 chrome"的旧路径');
  }

  // 1b. 同源页签之间切换：仍然只恢复 chrome，不重建工作区（避免无谓重绘）
  {
    const env = buildTabEnv();
    env.state.sessions = [makeSession('tab-A1', 'src-A'), makeSession('tab-A2', 'src-A')];
    env.state.activeId = 'tab-A2';
    env.state.source = env.state.sources[0];
    env.els['db-source'].value = 'src-A';
    await env.context.window.closeSessionsList([env.state.sessions[1]], 'tab-A1');
    assert.strictEqual(env.state.activeId, 'tab-A1');
    assert.strictEqual(env.calls.renderWorkspace, 0, '同源切换不应重建工作区');
    assert.deepStrictEqual(env.calls.restoreSessionChrome, ['tab-A1'], '同源切换应恢复页签 chrome');
  }

  // 1c. 关闭全部页签：新建的空白页签同样走统一激活流程
  {
    const env = buildTabEnv();
    env.state.sessions = [makeSession('tab-A', 'src-A')];
    env.state.activeId = 'tab-A';
    await env.context.window.closeSessionsList([env.state.sessions[0]], null);
    assert.strictEqual(env.state.sessions.length, 1);
    assert.strictEqual(env.state.activeId, 'fresh');
    assert.ok(env.calls.renderWorkspace + env.calls.restoreSessionChrome.length > 0,
      '新建的空白页签也必须经过统一激活流程');
  }

  // -------------------------------------------------------------------------
  // 2. 脚本执行的数据源必须来自活动页签，并与顶部下拉框交叉校验
  // -------------------------------------------------------------------------
  function buildScriptEnv(activeSession, dropdownSourceId) {
    const captures = { modal: null, toasts: [], requests: [] };
    const dropdown = dropdownSourceId || 'src-B';
    const els = {
      'db-sql': { value: 'UPDATE T SET FLAG=1 WHERE ID=7;', dataset: {} },
      'db-source': { value: dropdown, selectedOptions: [{ value: dropdown, textContent: '源' + dropdown }] },
      'db-source-badge': { textContent: 'Oracle' }
    };
    const state = { bindings: {}, adapters: {}, sourceCatalog: { 'src-B': { id: 'src-B', name: '源B' } } };
    const context = {
      state,
      K: { database: { getActiveSession: () => activeSession, markTransactionPending: () => {} } },
      id: key => els[key] || null,
      editor: () => els['db-sql'],
      sqlText: () => els['db-sql'].value,
      splitStatements: text => [{ text }],
      esc: value => String(value),
      typedParameters: values => values,
      dialect: () => 'oracle',
      sourceIsProduction: () => false,
      document: { createElement: () => ({ innerHTML: '' }) },
      createModal: opts => { captures.modal = opts; },
      toast: (msg, type) => { captures.toasts.push({ msg, type }); },
      emit: () => {},
      window: { confirm: () => true },
      api: async (method, url, body) => { captures.requests.push({ method, url, body }); return { ok: true }; },
      syncMutationTransaction: () => {}
    };
    const code = slice(features, '  function sourceId() {', '  function editor() {')
      + slice(features, '  function activeSession() {', '  function syncMutationTransaction(')
      + slice(features, '  function runScript() {', '  function buildDeepLink(');
    vm.runInNewContext(code + '\nwindow.runScript = runScript;', context);
    return { context, captures };
  }

  async function clickRun(env) {
    assert.ok(env.captures.modal, '运行脚本必须弹出确认框');
    const action = env.captures.modal.actions[0];
    assert.strictEqual(action.text, '执行脚本');
    const button = { disabled: false, textContent: '' };
    await action.onClick(button);
    return button;
  }

  // 2a. 页签 A + 下拉框 B（错误状态）→ 必须拒绝执行，绝不能把 A 的事务拼到 B 上
  {
    const env = buildScriptEnv({ sessionId: 'tx_A', sourceId: 'src-A' }, 'src-B');
    env.context.window.runScript();
    const button = await clickRun(env);
    assert.strictEqual(env.captures.requests.length, 0,
      '数据源不一致时不得发出任何请求（旧实现会发出 source_id=B + session_id=tx_A）');
    assert.ok(env.captures.toasts.some(t => t.type === 'err' && t.msg.includes('不一致')),
      '必须给出数据源不一致的错误提示');
    assert.strictEqual(button.disabled, false, '被拒绝后按钮必须恢复可点');
  }

  // 2b. 页签 A + 下拉框 A → 使用页签绑定的数据源与会话
  {
    const env = buildScriptEnv({ sessionId: 'tx_A', sourceId: 'src-A' }, 'src-A');
    env.context.state.bindings = {};
    env.context.window.runScript();
    const modal = env.captures.modal;
    await modal.actions[0].onClick({ disabled: false, textContent: '' });
    assert.strictEqual(env.captures.requests.length, 1, '一致时必须正常发起脚本请求');
    const body = env.captures.requests[0].body;
    assert.strictEqual(body.source_id, 'src-A', '脚本必须使用活动页签的数据源');
    assert.strictEqual(body.session_id, 'tx_A', '脚本必须使用活动页签的事务会话');
  }

  // 2c. 旧契约（页签没有 sourceId）：回退到顶部下拉框
  {
    const env = buildScriptEnv({ sessionId: 'tx_legacy' });
    env.context.window.runScript();
    await env.captures.modal.actions[0].onClick({ disabled: false, textContent: '' });
    assert.strictEqual(env.captures.requests.length, 1);
    assert.strictEqual(env.captures.requests[0].body.source_id, 'src-B',
      '页签未绑定数据源时回退到顶部下拉框');
  }

  console.log('Database session/source binding regressions passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
