'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');

const TestRunner = require('./utils/test-runner');
const data = require('./utils/page-data');
const helpers = require('./utils/helpers');
const { generateReport } = require('./utils/report-generator');

const PROJECT_ROOT = path.resolve(__dirname, '../..');
const RESULTS_DIR = path.join(PROJECT_ROOT, 'test-results');
const SCREENSHOTS_DIR = path.join(RESULTS_DIR, 'screenshots');
const NETWORK_DIR = path.join(RESULTS_DIR, 'network');
const CONSOLE_DIR = path.join(RESULTS_DIR, 'console');
const REPORT_PATH = path.join(RESULTS_DIR, 'report.md');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const HEADLESS = process.env.HEADLESS !== 'false';

function parseArgs() {
  const args = process.argv.slice(2);
  const result = { grep: null, headed: false };
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--headed') {
      result.headed = true;
    } else if (args[i] === '--grep' && i + 1 < args.length) {
      result.grep = args[i + 1];
      i++;
    }
  }
  return result;
}

function checkService(url) {
  return new Promise((resolve) => {
    const req = http.get(url, { timeout: 5000 }, (res) => {
      resolve({ reachable: true, statusCode: res.statusCode });
    });
    req.on('timeout', () => { req.destroy(); resolve({ reachable: false, error: 'timeout' }); });
    req.on('error', (err) => { resolve({ reachable: false, error: err.message }); });
  });
}

function ensureDir(dir) {
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function main() {
  const args = parseArgs();
  const headless = args.headed ? false : HEADLESS;

  console.log('='.repeat(60));
  console.log('Kairo E2E 测试');
  console.log('='.repeat(60));
  console.log('服务地址:', BASE_URL);
  console.log('模式:', headless ? 'headless' : 'headed');
  console.log('');

  const svc = await checkService(BASE_URL + '/');
  if (!svc.reachable) {
    console.error('❌ 服务不可达:', BASE_URL);
    console.error('请先启动服务: ./kairo 或 go run .');
    process.exit(1);
  }
  console.log('✅ 服务可达 (HTTP ' + svc.statusCode + ')');

  ensureDir(RESULTS_DIR);
  ensureDir(SCREENSHOTS_DIR);
  ensureDir(NETWORK_DIR);
  ensureDir(CONSOLE_DIR);

  const runner = new TestRunner({
    baseUrl: BASE_URL,
    headless: headless,
    viewport: { width: 1366, height: 900 },
    screenshotsDir: SCREENSHOTS_DIR,
    grep: args.grep,
  });

  console.log('🚀 启动浏览器...');
  await runner.init();

  const page = runner.page;
  const consoleLogs = [];
  const networkLogs = [];
  const buttonCoverage = {};

  helpers.setupConsoleCapture(page, consoleLogs);
  helpers.setupNetworkCapture(page, networkLogs);

  runner.beforeEach(async function (ctx) {
    try {
      await helpers.clearTransientUI(ctx.page);
    } catch (e) {
      // ignore cleanup errors
    }
  });

  runner.afterEach(async function (ctx) {
    try {
      await helpers.clearTransientUI(ctx.page);
    } catch (e) {
      // ignore cleanup errors
    }
  });

  const ctx = {
    page,
    baseUrl: BASE_URL,
    consoleLogs,
    networkLogs,
    screenshotsDir: SCREENSHOTS_DIR,
    data,
    helpers,
    buttonCoverage,
  };

  const testModules = [
    './tests/01-menu-navigation',
    './tests/02-pages-screenshot',
    './tests/03-button-scan',
    './tests/04-websphere',
    './tests/05-files',
    './tests/06-formatter',
    './tests/07-commands',
    './tests/08-config',
    './tests/09-misc-pages',
    './tests/10-formatters-deep',
    './tests/11-compare-deep',
    './tests/12-http-deep',
    './tests/13-config-deep',
    './tests/20-api-coverage',
    './tests/30-tasks',
    './tests/31-pet',
    './tests/32-http-curl-ws',
    './tests/33-ssh-profiles',
    './tests/34-reminders-actions',
    './tests/35-sponsor-pet-board',
  ];

  console.log('📦 注册测试模块...');
  for (const mod of testModules) {
    try {
      const m = require(mod);
      if (typeof m.register === 'function') {
        m.register(runner, ctx);
        console.log('  ✓ ' + mod);
      } else if (typeof m.default === 'function') {
        m.default(runner, ctx);
        console.log('  ✓ ' + mod);
      } else {
        const keys = Object.keys(m);
        const regFn = keys.find(k => k.startsWith('register'));
        if (regFn && typeof m[regFn] === 'function') {
          m[regFn](runner, ctx);
          console.log('  ✓ ' + mod);
        } else {
          console.log('  ⚠ ' + mod + ' (no register function)');
        }
      }
    } catch (e) {
      console.error('  ✗ ' + mod + ': ' + e.message);
    }
  }

  console.log('');
  console.log('🏃 运行测试...');
  console.log('');

  const results = await runner.run(ctx);

  console.log('');
  console.log('='.repeat(60));
  console.log('测试结果');
  console.log('='.repeat(60));
  console.log('总用例:', results.summary.total);
  console.log('通过:', results.summary.passed);
  console.log('失败:', results.summary.failed);
  console.log('跳过:', results.summary.skipped);
  console.log('耗时:', helpers.formatDuration(results.summary.duration));
  console.log('');

  for (const suite of results.suites) {
    const icon = suite.summary.failed > 0 ? '❌' : (suite.summary.passed > 0 ? '✅' : '⏭');
    console.log(icon + ' ' + suite.name + '  (' + suite.summary.passed + '/' + suite.summary.total + ' 通过)');
    for (const test of suite.tests) {
      const tIcon = test.status === 'passed' ? '  ✓' : test.status === 'failed' ? '  ✗' : '  ⏭';
      console.log(tIcon + ' ' + test.name);
      if (test.status === 'failed' && test.error) {
        console.log('    错误: ' + test.error.message);
      }
    }
    if (suite.suites) {
      for (const sub of suite.suites) {
        const sIcon = sub.summary.failed > 0 ? '❌' : (sub.summary.passed > 0 ? '✅' : '⏭');
        console.log('  ' + sIcon + ' ' + sub.name + '  (' + sub.summary.passed + '/' + sub.summary.total + ' 通过)');
        for (const test of sub.tests) {
          const tIcon = test.status === 'passed' ? '    ✓' : test.status === 'failed' ? '    ✗' : '    ⏭';
          console.log(tIcon + ' ' + test.name);
          if (test.status === 'failed' && test.error) {
            console.log('      错误: ' + test.error.message);
          }
        }
      }
    }
  }

  console.log('');
  console.log('📝 生成报告...');

  const networkPath = path.join(NETWORK_DIR, 'network-log.json');
  const consolePath = path.join(CONSOLE_DIR, 'console-log.json');
  const buttonCovPath = path.join(RESULTS_DIR, 'button-coverage.json');

  fs.writeFileSync(networkPath, JSON.stringify(networkLogs, null, 2));
  fs.writeFileSync(consolePath, JSON.stringify(consoleLogs, null, 2));
  fs.writeFileSync(buttonCovPath, JSON.stringify(buttonCoverage, null, 2));

  const reportContent = generateReport({
    ...results,
    buttonCoverage: buttonCoverage,
    networkLog: networkLogs,
    consoleLog: consoleLogs,
    apiList: data.apiList,
    menuList: data.menuList,
    pageList: Object.keys(data.pageMeta),
    menuCoverage: { tested: data.menuList.length, total: data.menuList.length },
    pageCoverage: { tested: Object.keys(data.pageMeta).length, total: Object.keys(data.pageMeta).length },
  }, {
    baseUrl: BASE_URL,
    browserName: 'chromium',
    outputDir: RESULTS_DIR,
    screenshotsDir: SCREENSHOTS_DIR,
  });

  fs.writeFileSync(REPORT_PATH, reportContent);
  console.log('✅ 报告已生成:', REPORT_PATH);

  await runner.close();

  console.log('');
  if (results.summary.failed > 0) {
    console.log('❌ 有 ' + results.summary.failed + ' 个测试失败');
    process.exit(1);
  } else {
    console.log('🎉 所有测试通过!');
    process.exit(0);
  }
}

main().catch((e) => {
  console.error('Fatal error:', e);
  process.exit(1);
});
