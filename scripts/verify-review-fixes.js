#!/usr/bin/env node
'use strict';

/**
 * scripts/verify-review-fixes.js
 *
 * 复跑 20260922 审查修复的分层验收（对应审查文档 §10「交付证据与复跑方法」）。
 *
 * 用法:
 *   node scripts/verify-review-fixes.js              # 全部层级 + 干净检出验证
 *   node scripts/verify-review-fixes.js --fast       # 跳过耗时的完整 Go 套件
 *   node scripts/verify-review-fixes.js --no-worktree# 跳过 detached worktree 验证
 *
 * 检验分层（与文档 §10 推荐的验收顺序一致）:
 *   1. 测试隔离门禁   —— 真实库测试在默认测试二进制中必须不存在, 加 tag 后才出现
 *   2. 静态检查       —— go vet ./...
 *   3. 离线测试       —— go test ./... 与 npm test
 *   4. 结构守卫       —— E2E 注册一致性、runner 行为、真实契约 fixture
 *   5. 干净检出验证   —— 在 detached worktree 中只对已提交内容跑 3+4
 *                        (证明提交自洽, 不依赖任何未提交文件)
 *
 * 真实 Oracle / 真实 SFTP / 浏览器点击 / Windows 原生 / AIX 需要独立环境,
 * 本脚本不复跑, 会在结尾明确列出为"未验证"。
 */

const { spawnSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const ROOT_DIR = path.resolve(__dirname, '..');
const args = process.argv.slice(2);
const FAST = args.includes('--fast');
const NO_WORKTREE = args.includes('--no-worktree');

const results = [];

/** record 记录一层结果并在控制台即时反馈。失败时打印捕获输出的尾部, 便于定位。 */
function record(layer, ok, detail, out) {
  results.push({ layer, ok, detail: detail || '' });
  console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${layer}${detail ? '  — ' + detail : ''}`);
  if (!ok && out) {
    const lines = String(out).trim().split('\n');
    const tail = lines.slice(-15);
    console.log('  ---- 输出尾部 ----');
    for (const l of tail) console.log('  | ' + l);
    console.log('  ------------------');
  }
}

/** run 同步执行命令, 返回 {ok, out}。inherit=true 时直接透传输出。 */
function run(command, cmdArgs, options) {
  const opts = Object.assign({
    cwd: ROOT_DIR,
    encoding: 'utf8',
    shell: process.platform === 'win32',
  }, options || {});
  if (opts.inherit) {
    const r = spawnSync(command, cmdArgs, Object.assign({}, opts, { stdio: 'inherit' }));
    return { ok: r.status === 0, out: '' };
  }
  const r = spawnSync(command, cmdArgs, opts);
  const out = String(r.stdout || '') + String(r.stderr || '');
  return { ok: r.status === 0, out };
}

function gitSha(cwd) {
  const r = run('git', ['rev-parse', 'HEAD'], { cwd: cwd || ROOT_DIR });
  return r.ok ? r.out.trim() : '';
}

function main() {
  const sha = gitSha();
  console.log('='.repeat(70));
  console.log('Kairo 20260922 审查修复 — 分层验收复跑');
  console.log('='.repeat(70));
  console.log('仓库:', ROOT_DIR);
  console.log('HEAD:', sha || '(无法读取)');
  console.log('');

  // ---- 1. 测试隔离门禁 ----
  console.log('[1] 测试隔离门禁');
  const gatedNames = ['TestOracleWorkbenchFullLifecycle', 'TestLOBRealOracle'];
  const defaultList = run('go', ['test', '-list', '.*', './internal/dbconsole/']);
  const taggedList = run('go', ['test', '-tags=integration', '-list', '.*', './internal/dbconsole/']);
  if (!defaultList.ok || !taggedList.ok) {
    record('门禁: 测试列表可读取', false, 'go test -list 失败');
  } else {
    const leaked = gatedNames.filter(function (n) { return defaultList.out.includes(n); });
    const present = gatedNames.filter(function (n) { return taggedList.out.includes(n); });
    record('真实库测试默认不编译进测试二进制', leaked.length === 0,
      leaked.length ? '泄漏: ' + leaked.join(', ') : gatedNames.length + ' 个测试均不存在');
    record('加 -tags=integration 后真实库测试出现', present.length === gatedNames.length,
      '出现 ' + present.length + '/' + gatedNames.length);
  }
  console.log('');

  // ---- 2 + 3. 静态检查与离线测试 ----
  console.log('[2] 静态检查');
  const vetRun = run('go', ['vet', './...'], { inherit: false });
  record('go vet ./...', vetRun.ok, '', vetRun.out);
  console.log('');

  console.log('[3] 离线测试');
  if (FAST) {
    record('go test ./internal/dbconsole/ ./internal/sftpclient/ ./internal/comparefs/ ./internal/upgrade/ ./internal/webservice/ ./internal/logquery/ (--fast)',
      run('go', ['test', './internal/dbconsole/', './internal/sftpclient/', './internal/comparefs/',
        './internal/upgrade/', './internal/webservice/', './internal/logquery/', '-count=1']).ok);
  } else {
    const goTest = run('go', ['test', './...', '-count=1']);
    record('go test ./... -count=1', goTest.ok, '', goTest.out);
  }
  const npmRun = run('npm', ['test'], { inherit: false });
  record('npm test', npmRun.ok, '', npmRun.out);
  console.log('');

  // ---- 4. 结构守卫 ----
  console.log('[4] 结构守卫 (E2E 注册一致性 / runner / 真实契约 fixture)');
  for (const t of ['tests/e2e-registration.test.js', 'tests/e2e-runner.test.js', 'tests/grid-edit-plan-contract.test.js']) {
    const tr = run(process.execPath, [path.join(ROOT_DIR, t)]);
    record(t, tr.ok, '', tr.out);
  }
  console.log('');

  // ---- 5. 干净检出验证 ----
  if (!NO_WORKTREE) {
    console.log('[5] 干净检出验证 (detached worktree, 只含已提交内容)');
    const wt = path.join(os.tmpdir(), 'kairo-verify-' + Date.now());
    let created = false;
    try {
      if (!sha) throw new Error('无法确定 HEAD');
      const add = run('git', ['worktree', 'add', '--detach', wt, sha]);
      if (!add.ok) throw new Error('git worktree add 失败: ' + add.out.trim());
      created = true;

      const dirty = run('git', ['status', '--porcelain'], { cwd: wt });
      record('worktree 内无未提交改动', dirty.ok && dirty.out.trim() === '',
        dirty.out.trim() === '' ? '0 条' : '仍有未提交改动');

      // node_modules 以 junction 接入, 使 JS 测试可跑
      try {
        fs.symlinkSync(path.join(ROOT_DIR, 'node_modules'), path.join(wt, 'node_modules'), 'junction');
      } catch (e) {
        // 已存在或平台不支持: 不致命
      }

      const wtGo = run('go', ['test', './...', '-count=1'], { cwd: wt });
      record('worktree: go test ./... -count=1', wtGo.ok, '', wtGo.out);
      const wtNpm = run('npm', ['test'], { cwd: wt });
      record('worktree: npm test', wtNpm.ok, '', wtNpm.out);
    } catch (e) {
      record('干净检出验证', false, e.message);
    } finally {
      if (created) run('git', ['worktree', 'remove', '--force', wt]);
    }
    console.log('');
  }

  // ---- 汇总 ----
  const failed = results.filter(function (r) { return !r.ok; });
  console.log('='.repeat(70));
  console.log(`复跑汇总: 通过 ${results.length - failed.length}/${results.length}`);
  if (failed.length) {
    console.log('失败项:');
    for (const f of failed) console.log('  - ' + f.layer + (f.detail ? '  (' + f.detail + ')' : ''));
  }
  console.log('');
  console.log('本脚本不复跑、仍需独立环境的验收项 (保留未验证状态):');
  console.log('  - 真实 Oracle 11g (堆表/IOT/视图/quoted 名称/无主键 LOB/并发会话/未提交事务)');
  console.log('  - 真实 SFTP 服务器的磁盘级 GBK fixture 与页面点击');
  console.log('  - 浏览器点击与截图矩阵 (审查文档 §9)');
  console.log('  - Windows 原生进程锁 / 托盘 / 便笺专项');
  console.log('  - AIX ksh 与真实目标服务器; Linux 侧仅编译验证');
  console.log('='.repeat(70));

  process.exit(failed.length ? 1 : 0);
}

main();
