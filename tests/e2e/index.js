'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');

const TestRunner = require('./utils/test-runner');
const data = require('./utils/page-data');
const helpers = require('./utils/helpers');
const { buildModuleSummary, evaluateRunVerdict } = require('./utils/run-accounting');
const { generateReport } = require('./utils/report-generator');

const PROJECT_ROOT = path.resolve(__dirname, '../..');
const RESULTS_DIR = path.join(PROJECT_ROOT, 'test-results');
const SCREENSHOTS_DIR = path.join(RESULTS_DIR, 'screenshots');
const NETWORK_DIR = path.join(RESULTS_DIR, 'network');
const CONSOLE_DIR = path.join(RESULTS_DIR, 'console');
const TRACES_DIR = path.join(RESULTS_DIR, 'traces');
const REPORT_PATH = path.join(RESULTS_DIR, 'report.md');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const HEADLESS = process.env.HEADLESS !== 'false';
const VIEWPORT = { width: 1366, height: 900 };

/**
 * readGitMetadata 直接读 .git, 不 spawn git 子进程。
 *
 * QA-02 要求截图/报告绑定本次运行的 SHA。用子进程读 git 会在受限沙箱或缺少
 * git 可执行文件的环境里失败, 所以这里只读文件系统, 读不到就退化为 nosha。
 */
function readGitMetadata() {
  const meta = { sha: '', shortSha: 'nosha', branch: '' };
  try {
    const gitDir = path.join(PROJECT_ROOT, '.git');
    const head = fs.readFileSync(path.join(gitDir, 'HEAD'), 'utf8').trim();
    let sha = '';
    if (head.startsWith('ref:')) {
      const ref = head.slice(4).trim();
      meta.branch = ref.replace(/^refs\/heads\//, '');
      const refPath = path.join(gitDir, ref);
      if (fs.existsSync(refPath)) {
        sha = fs.readFileSync(refPath, 'utf8').trim();
      } else {
        const packedPath = path.join(gitDir, 'packed-refs');
        if (fs.existsSync(packedPath)) {
          const line = fs.readFileSync(packedPath, 'utf8')
            .split('\n')
            .find((l) => l.endsWith(' ' + ref));
          if (line) sha = line.split(' ')[0].trim();
        }
      }
    } else {
      sha = head; // detached HEAD
    }
    meta.sha = sha;
    meta.shortSha = sha ? sha.slice(0, 7) : 'nosha';
  } catch (e) {
    // 读不到就保持 nosha, 不影响测试执行
  }
  return meta;
}

/** countTests 递归统计一批 suite 里注册的用例数。 */
function countTests(suites) {
  return suites.reduce(function (n, s) {
    return n + s.tests.length + countTests(s.suites || []);
  }, 0);
}

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
  const git = readGitMetadata();
  const startedAt = new Date().toISOString();
  const viewportLabel = `${VIEWPORT.width}x${VIEWPORT.height}`;

  const runMeta = {
    sha: git.sha,
    shortSha: git.shortSha,
    branch: git.branch,
    startedAt: startedAt,
    baseUrl: BASE_URL,
    viewport: viewportLabel,
    browserName: 'chromium',
    headless: headless,
    node: process.version,
    platform: process.platform,
    grep: args.grep || null,
  };

  console.log('='.repeat(60));
  console.log('Kairo E2E 测试');
  console.log('='.repeat(60));
  console.log('服务地址:', BASE_URL);
  console.log('模式:', headless ? 'headless' : 'headed');
  console.log('SHA:', runMeta.shortSha, runMeta.branch ? '(' + runMeta.branch + ')' : '');
  console.log('Viewport:', viewportLabel);
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
  ensureDir(TRACES_DIR);

  const runner = new TestRunner({
    baseUrl: BASE_URL,
    headless: headless,
    viewport: VIEWPORT,
    screenshotsDir: SCREENSHOTS_DIR,
    // 失败用例的 Playwright trace 落盘位置 (文件名含 SHA + viewport + 时间)
    traceDir: TRACES_DIR,
    grep: args.grep,
    runMeta: runMeta,
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
    './tests/14-database-workbench',
    './tests/12-http-deep',
    './tests/13-config-deep',
    './tests/20-api-coverage',
    './tests/30-tasks',
    './tests/31-pet',
    './tests/32-http-curl-ws',
    './tests/33-ssh-profiles',
    './tests/34-reminders-actions',
    './tests/35-sponsor-pet-board',
    './tests/36-webservice-deep',
    './tests/37-wscodegen-deep',
    './tests/38-notes-deep',
    './tests/39-ui-workbench',
    './tests/40-database-write-regression',
    './tests/41-database-backup-real',
    './tests/42-database-deep',
    './tests/43-sql-editor-large-text',
  ];

  console.log('📦 注册测试模块...');
  let registerErrors = 0;
  // moduleReports 记录每个模块的"发现/注册"情况, 用于报告里区分
  // "没跑" 和 "跑了且通过" (QA-02)。
  const moduleReports = [];
  for (const mod of testModules) {
    const suiteStart = runner.suites.length;
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
          registerErrors++;
          console.log('  ✗ ' + mod + ' (no register function)');
        }
      }
    } catch (e) {
      registerErrors++;
      console.error('  ✗ ' + mod + ': ' + e.message);
    }
    const addedSuites = runner.suites.slice(suiteStart);
    moduleReports.push({
      module: mod,
      ok: addedSuites.length > 0,
      suitesRegistered: addedSuites.length,
      testsRegistered: countTests(addedSuites),
      suiteIndexStart: suiteStart,
      suiteIndexEnd: runner.suites.length,
    });
  }

  if (registerErrors > 0) {
    console.error(`\n❌ 测试套件初始化失败：有 ${registerErrors} 个模块加载或注册失败！`);
    process.exit(1);
  }

  console.log('');
  console.log('🏃 运行测试...');
  console.log('');

  const results = await runner.run(ctx);

  console.log('');
  console.log('='.repeat(60));
  console.log('测试结果');
  console.log('='.repeat(60));
  console.log('总用例(发现):', results.summary.total);
  console.log('实际执行:', results.summary.executed);
  console.log('通过:', results.summary.passed);
  console.log('失败:', results.summary.failed);
  console.log('跳过:', results.summary.skipped);
  console.log('耗时:', helpers.formatDuration(results.summary.duration));
  console.log('');

  // 按模块汇总 发现/执行/通过/失败/跳过, 明确区分"没跑"与"跑了且通过"。
  // 统计与判定都放在 tests/e2e/utils/run-accounting.js 里做成纯函数, 便于离线回归。
  const moduleSummary = buildModuleSummary(moduleReports, results.suites);

  console.log('按模块统计 (发现/执行/通过/失败/跳过):');
  for (const m of moduleSummary) {
    const flag = !m.ok ? '✗ 注册失败' : (m.failed > 0 ? '❌' : (m.executed > 0 ? '✅' : '⏭ 未执行'));
    console.log(
      `  ${flag} ${m.module}  ` +
      `${m.discovered}/${m.executed}/${m.passed}/${m.failed}/${m.skipped}` +
      `  (注册用例=${m.testsRegistered})`
    );
  }
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

  // QA-02: 运行元数据落盘, 与截图/报告一起构成可复核证据 (SHA+viewport+时间)。
  const runMetadataPath = path.join(RESULTS_DIR, 'run-metadata.json');
  fs.writeFileSync(runMetadataPath, JSON.stringify({
    run: Object.assign({}, runMeta, {
      finishedAt: new Date().toISOString(),
      durationMs: results.summary.duration,
    }),
    summary: results.summary,
    noMatch: !!results.noMatch,
    registerErrors: registerErrors,
    modules: moduleSummary,
  }, null, 2));
  console.log('✅ 运行元数据已生成:', runMetadataPath);
  console.log('   SHA=' + runMeta.shortSha + ' viewport=' + runMeta.viewport +
    ' grep=' + (runMeta.grep || '-'));
  console.log('   失败 trace: ' + runner.traceFiles.length + ' 个' +
    (runner.traceFiles.length ? ' → ' + TRACES_DIR : ''));

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
  const verdict = evaluateRunVerdict({
    noMatch: results.noMatch,
    grep: runMeta.grep,
    discovered: results.summary.total,
    executed: results.summary.executed,
    failed: results.summary.failed,
    registerErrors: registerErrors,
  });
  console.log((verdict.ok ? '🎉 ' : '❌ ') + verdict.reason);
  process.exit(verdict.exitCode);
}

main().catch((e) => {
  console.error('Fatal error:', e);
  process.exit(1);
});
