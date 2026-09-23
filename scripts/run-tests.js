#!/usr/bin/env node
'use strict';

/**
 * scripts/run-tests.js
 * 跨平台统一测试入口 (QA-01)
 *
 * 用法:
 *   node scripts/run-tests.js          # 默认执行所有前端/Node 单元测试
 *   node scripts/run-tests.js --unit   # 仅执行 Node 单元测试
 *   node scripts/run-tests.js --go     # 执行 Go 单元测试 (go test ./...)
 *   node scripts/run-tests.js --e2e    # 执行 E2E 测试 (无服务时明确 SKIPPED)
 *   node scripts/run-tests.js --all    # 执行全部可用测试
 */

const { spawnSync } = require('child_process');
const http = require('http');
const path = require('path');

const ROOT_DIR = path.resolve(__dirname, '..');

const UNIT_TESTS = [
  'web/app.test.js',
  'web/pages/webservice.test.js',
  'tests/workbench-unit-tests.js',
  'tests/database-features-unit.js',
  'tests/database-review-regression.js',
  'tests/database-orphan-source.test.js',
  'tests/database-shortcut-history.test.js',
  'tests/compare-folder-regression.js',
  'tests/compare-save-lifecycle.test.js',
  'tests/compare-tabs-lifecycle.test.js',
  'tests/files-path-identity.test.js',
  'tests/security-review-followup.test.js',
  'tests/tab-lifecycle-review-regression.test.js',
  'tests/database-update-multi-col-scroll.test.js',
  'tests/sql-format-service.test.js',
  'tests/grid-edit-plan-contract.test.js',
  'tests/grid-edit-plan-wire.test.js',
  'tests/e2e-registration.test.js',
  'tests/e2e-runner.test.js',
  'tests/grid-orderby-phase0-regression.test.js',
  'tests/database-sql-editor-performance.test.js'
];

function checkService(url) {
  return new Promise((resolve) => {
    const req = http.get(url, { timeout: 3000 }, (res) => {
      resolve({ reachable: true, statusCode: res.statusCode });
    });
    req.on('timeout', () => {
      req.destroy();
      resolve({ reachable: false, error: 'timeout' });
    });
    req.on('error', (err) => {
      resolve({ reachable: false, error: err.message });
    });
  });
}

function runCommand(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: ROOT_DIR,
    stdio: 'inherit',
    shell: process.platform === 'win32',
    ...options
  });
  return result.status === 0;
}

async function main() {
  const args = process.argv.slice(2);
  const runGo = args.includes('--go') || args.includes('--all');
  const runE2E = args.includes('--e2e') || args.includes('--all');
  const runUnit = args.includes('--unit') || args.includes('--all') || (!runGo && !runE2E);
  const headed = args.includes('--headed');

  console.log('='.repeat(60));
  console.log('Kairo 跨平台测试执行器 (QA-01)');
  console.log('='.repeat(60));

  let passed = 0;
  let failed = 0;
  let skipped = 0;

  if (runUnit) {
    console.log('\n[Phase 1] 运行 Node.js 单元测试集:');
    for (const testFile of UNIT_TESTS) {
      console.log(`\n--- 运行 ${testFile} ---`);
      const ok = runCommand(process.execPath, [path.join(ROOT_DIR, testFile)]);
      if (ok) {
        passed++;
        console.log(`[PASS] ${testFile}`);
      } else {
        failed++;
        console.log(`[FAIL] ${testFile}`);
      }
    }
  }

  if (runGo) {
    console.log('\n[Phase 2] 运行 Go 单元与回归测试集:');
    const ok = runCommand('go', ['test', './...']);
    if (ok) {
      passed++;
      console.log('[PASS] go test ./...');
    } else {
      failed++;
      console.log('[FAIL] go test ./...');
    }
  }

  if (runE2E) {
    console.log('\n[Phase 3] 运行 Playwright E2E 测试集:');
    const baseUrl = process.env.BASE_URL || 'http://127.0.0.1:18092';
    const svc = await checkService(baseUrl + '/');
    if (!svc.reachable) {
      skipped++;
      console.log(`[SKIPPED] E2E 测试跳过：本地服务未启动 (${baseUrl})。需先执行 './kairo' 或 'go run .' 启动服务。`);
    } else {
      const e2eArgs = [path.join(ROOT_DIR, 'tests/e2e/index.js')];
      if (headed) {
        e2eArgs.push('--headed');
      }
      const ok = runCommand(process.execPath, e2eArgs);
      if (ok) {
        passed++;
        console.log('[PASS] E2E Tests');
      } else {
        failed++;
        console.log('[FAIL] E2E Tests');
      }
    }
  }

  console.log('\n' + '='.repeat(60));
  console.log(`测试完成！汇总结果: Passed=${passed}, Failed=${failed}, Skipped=${skipped}`);
  console.log('='.repeat(60));

  if (failed > 0) {
    process.exit(1);
  }
}

main().catch((err) => {
  console.error('测试运行器异常:', err);
  process.exit(1);
});
