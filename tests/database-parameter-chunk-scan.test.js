'use strict';
// tests/database-parameter-chunk-scan.test.js
//
// 审核第 7 项（P2）回归：长 SQL 的绑定参数跨扫描块时被截断。
//
// 触发：让 `:serialno` 的冒号位于字符偏移 65534，正好跨越 65536 字符的扫描块边界。
// 旧实现只在当前块内读取参数名（`s`），却把扫描结果标记为完成 ——
// 之后 DBUI-04 的绑定裁剪会把用户填好的 serialno 绑定当成失效参数删掉。
//
// 约束：分块扫描的结果必须与整段扫描完全一致（跨块 token 要留到确认结束后再提交）。

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const window = { Kairo: { database: { markTransactionPending() {} } }, addEventListener() {} };
global.window = window;
const context = {
  window,
  console,
  URLSearchParams,
  Set,
  Map,
  setTimeout,
  clearTimeout,
  location: { hash: '', href: 'http://localhost/#/database' },
  navigator: {},
  document: { readyState: 'loading', addEventListener() {}, getElementById() { return null; }, body: {} }
};
const read = file => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');
for (const file of ['web/workbench/statement-model.js', 'web/workbench/syntax-editor.js', 'web/workbench/database-features.js']) {
  vm.runInNewContext(read(file), context);
}
const F = window.Kairo.databaseFeatures;
assert.ok(F && typeof F.scanParameters === 'function', 'databaseFeatures.scanParameters 必须可用');

const CHUNK = 64 * 1024; // PARAM_SCAN_CHUNK

function sqlWithParamAt(offset, param, tail) {
  const head = 'SELECT * FROM T WHERE C = ';
  const padLen = offset - head.length;
  assert.ok(padLen >= 0, 'offset 太小');
  return head + 'p'.repeat(padLen) + ':' + param + (tail || '');
}

function names(scan) {
  return scan.parameters.map(p => p.name);
}

// 1. 冒号落在块边界前两字节：参数名必须完整
{
  const sql = sqlWithParamAt(65534, 'serialno', ' AND D = :other;');
  const scan = F.scanParameters(sql, { chunkBudgetMs: 0 });
  assert.strictEqual(scan.status, 'ready', '整段扫描必须完成: ' + JSON.stringify(scan));
  assert.strictEqual(scan.complete, true);
  assert.ok(names(scan).includes('serialno'),
    '跨块参数名必须是完整的 serialno，实际: ' + JSON.stringify(names(scan)));
  assert.ok(!names(scan).includes('s'),
    '不得把被块边界截断的 ":s" 当成参数: ' + JSON.stringify(names(scan)));
  assert.ok(names(scan).includes('other'), '第二个参数也必须照常识别');
}

// 2. 已填写绑定不得被截断的参数名裁掉（DBUI-04 裁剪链路）
{
  const sql = sqlWithParamAt(65534, 'serialno', ' AND D = :other;');
  const scan = F.scanParameters(sql, { chunkBudgetMs: 0 });
  F.setBindings({ serialno: 'A001', other: 'x' });
  const pruned = F.pruneBindingsAgainst(scan);
  assert.strictEqual(pruned, true, '扫描完成后应当允许裁剪');
  const kept = F.getBindings();
  assert.strictEqual(kept.serialno, 'A001', '用户填写的 serialno 绑定必须保留: ' + JSON.stringify(kept));
  assert.strictEqual(kept.other, 'x');
}

// 3. 参数名恰好结束在块边界上（下一块以空格开头）也不能丢
{
  const name = 'serialno';
  const sql = sqlWithParamAt(CHUNK - name.length - 1, name, ' FROM DUAL');
  const scan = F.scanParameters(sql, { chunkBudgetMs: 0 });
  assert.strictEqual(scan.status, 'ready');
  assert.ok(names(scan).includes(name), '块边界处结束的参数名必须完整: ' + JSON.stringify(names(scan)));
}

// 4. 参数位于整个文本末尾（最后一块）时立即提交，不能悬空
{
  const sql = 'SELECT * FROM T WHERE A = :tail';
  const scan = F.scanParameters(sql, { chunkBudgetMs: 0 });
  assert.strictEqual(JSON.stringify(names(scan)), '["tail"]', '文本末尾的参数必须被识别');
}

// 5. 分块扫描结果与整段扫描一致（同一份长 SQL 两种路径对比）
{
  const sql = sqlWithParamAt(65534, 'serialno', ' AND D = :other AND E = :third');
  const whole = F.scanParameters(sql, { chunkBudgetMs: 0 });
  const viaFind = F.findParameters(sql, { chunkBudgetMs: 0 });
  assert.strictEqual(JSON.stringify(names(whole)), JSON.stringify(viaFind.map(p => p.name)),
    'findParameters 与 scanParameters 的结果必须一致');
  assert.strictEqual(JSON.stringify(names(whole)), JSON.stringify(['serialno', 'other', 'third']));
}

console.log('Database parameter chunk-scan regressions passed');
