'use strict';

/**
 * tests/e2e-registration.test.js (QA-02)
 *
 * 验证 E2E "单一 runner 注册机制" 的结构完整性, 不需要浏览器或运行中的服务:
 *   1. tests/e2e/tests/ 下每个模块文件都必须在 tests/e2e/index.js 的 testModules 里注册
 *   2. testModules 里每个路径都必须真实存在
 *   3. 每个模块都能被 register(runner, ctx) 成功注册, 且至少注册 1 个用例
 *      ("零注册模块" 不允许被当成通过)
 *   4. 模块源码里不得再出现硬编码绝对路径 (d:/kairo、盘符路径) 或写死的服务地址,
 *      也不得按绝对路径 require('playwright')
 *   5. 新移植的 43-sql-editor-large-text 走 ctx.baseUrl 且注册 4 个用例
 *
 * 这是原脚本 `require('d:/kairo/node_modules/playwright')` + 固定 127.0.0.1:18092 +
 * 不在任何 runner 入口里的结构性回归守卫。
 */

const assert = require('assert');
const fs = require('fs');
const path = require('path');

const E2E_DIR = path.join(__dirname, 'e2e');
const MODULES_DIR = path.join(E2E_DIR, 'tests');
const INDEX_PATH = path.join(E2E_DIR, 'index.js');

const HARDCODED_PATH_PATTERNS = [
  { re: /d:\/kairo/i, label: 'd:/kairo' },
  { re: /d:\\\\kairo/i, label: 'd:\\kairo' },
  { re: /[a-z]:[\\/]kairo[\\/]node_modules/i, label: '盘符 + node_modules 绝对路径' },
  { re: /require\(\s*['"][a-z]:[\\/]/i, label: "按绝对路径 require" },
];

/**
 * findHardcodedServiceURLs 只报告"不可覆盖"的写死服务地址。
 *
 * 判定范围刻意收窄, 避免误伤合法的 mock 服务器地址 (例如 WSDL 夹具用的
 * 127.0.0.1:18094/18096 是测试自带的服务, 不是被测应用地址):
 *   - 端口等于 Kairo 默认服务端口 18092 的字面地址
 *   - 直接写死 URL 的页面跳转 (page.goto / gotoHash)
 * 两种情况都允许用 process.env.BASE_URL 做回退, 这就是 QA-02 要求的 BASE_URL 用法。
 */
const APP_DEFAULT_PORT_RE = /https?:\/\/(127\.0\.0\.1|localhost):18092/;
const LITERAL_NAVIGATION_RE = /(page\.goto|gotoHash)\(\s*['"]https?:\/\//;

function findHardcodedServiceURLs(source) {
  const hits = [];
  const lines = source.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/process\.env\.BASE_URL/.test(line)) continue; // 可被环境变量覆盖, 合规
    if (APP_DEFAULT_PORT_RE.test(line)) {
      hits.push(`第 ${i + 1} 行 (写死 Kairo 默认服务地址 18092): ${line.trim()}`);
    }
    if (LITERAL_NAVIGATION_RE.test(line)) {
      hits.push(`第 ${i + 1} 行 (跳转到写死的字面 URL): ${line.trim()}`);
    }
  }
  return hits;
}

/** readTestModules 静态解析 index.js 的 testModules 列表 (不执行 main())。 */
function readTestModules() {
  const source = fs.readFileSync(INDEX_PATH, 'utf8');
  const start = source.indexOf('const testModules = [');
  assert.ok(start >= 0, 'index.js 必须定义 testModules 数组');
  const end = source.indexOf('];', start);
  assert.ok(end > start, 'testModules 数组必须以 ]; 结束');
  const body = source.slice(start, end);
  const mods = [];
  const re = /'(\.\/tests\/[^']+)'/g;
  let m;
  while ((m = re.exec(body)) !== null) {
    mods.push(m[1]);
  }
  return mods;
}

/** makeStubRunner 最小 runner 替身, 只统计注册结果。 */
function makeStubRunner() {
  const suites = [];
  let current = null;

  function it(name) {
    if (!current) throw new Error('it() 必须在 describe() 内调用');
    current.tests.push({ name: name });
  }
  it.skip = it;

  const runner = {
    suites: suites,
    describe: function (name, fn) {
      const suite = { name: name, tests: [], suites: [] };
      if (current) {
        current.suites.push(suite);
      } else {
        suites.push(suite);
      }
      const prev = current;
      current = suite;
      try {
        fn.call(runner);
      } finally {
        current = prev;
      }
    },
    it: it,
    beforeAll: function () {},
    afterAll: function () {},
    beforeEach: function () {},
    afterEach: function () {},
    skipTest: function () {},
    addScreenshot: function () {},
    screenshot: async function () { return null; },
    baseUrl: 'http://example.invalid',
  };
  return runner;
}

function countTests(suites) {
  return suites.reduce(function (n, s) {
    return n + s.tests.length + countTests(s.suites || []);
  }, 0);
}

function main() {
  const registeredMods = readTestModules();
  assert.ok(registeredMods.length > 0, 'testModules 不得为空');

  // 1 + 2: 目录文件与注册列表双向一致
  // index.js 里的模块写成不带扩展名的 './tests/xx-name', 这里统一成同一形态再比较。
  const filesOnDisk = fs.readdirSync(MODULES_DIR)
    .filter(function (f) { return f.endsWith('.js'); })
    .map(function (f) { return './tests/' + f.replace(/\.js$/, ''); })
    .sort();
  const registeredSorted = registeredMods.slice().sort();

  const missingFromIndex = filesOnDisk.filter(function (f) { return !registeredSorted.includes(f); });
  assert.deepStrictEqual(
    missingFromIndex, [],
    'tests/e2e/tests/ 下有模块没有在 index.js 的 testModules 里注册 (会被静默忽略): ' +
    missingFromIndex.join(', ')
  );

  const missingOnDisk = registeredSorted.filter(function (m) { return !filesOnDisk.includes(m); });
  assert.deepStrictEqual(
    missingOnDisk, [],
    'index.js 注册了不存在的模块文件: ' + missingOnDisk.join(', ')
  );

  const duplicates = registeredSorted.filter(function (m, i) { return registeredSorted.indexOf(m) !== i; });
  assert.deepStrictEqual(duplicates, [], 'testModules 存在重复注册: ' + duplicates.join(', '));

  // 3 + 4 + 5: 逐个模块真实注册
  // ctx 用真实的 page-data / helpers (它们不依赖浏览器), 避免模块在注册期读
  // ctx.data.menuList 之类的数据时崩掉 —— 注册期崩掉在 runner 里会被算作失败。
  const stubCtx = {
    page: {},
    baseUrl: 'http://example.invalid',
    helpers: require(path.join(E2E_DIR, 'utils', 'helpers')),
    data: require(path.join(E2E_DIR, 'utils', 'page-data')),
    consoleLogs: [],
    networkLogs: [],
    screenshotsDir: null,
    buttonCoverage: {},
  };

  let totalRegistered = 0;
  const zeroRegistration = [];

  for (const mod of registeredSorted) {
    // index.js 中的路径不带 .js 后缀, 落盘读取时需要补上
    const absPath = path.join(E2E_DIR, mod.replace(/^\.\//, '') + '.js');

    // 4: 源码不得含硬编码路径/地址
    const source = fs.readFileSync(absPath, 'utf8');
    for (const p of HARDCODED_PATH_PATTERNS) {
      assert.ok(!p.re.test(source), `${mod}: 含硬编码绝对路径 (${p.label}), 不可移植`);
    }
    const urlHits = findHardcodedServiceURLs(source);
    assert.deepStrictEqual(
      urlHits, [],
      `${mod}: 含不可覆盖的写死服务地址, 必须改用 ctx.baseUrl / BASE_URL:\n  ` + urlHits.join('\n  ')
    );

    // 3: 真实调用 register
    const exported = require(absPath);
    const registerFn = typeof exported.register === 'function'
      ? exported.register
      : (typeof exported.default === 'function' ? exported.default : null);
    assert.ok(registerFn, `${mod}: 必须导出 register(runner, ctx)`);

    const runner = makeStubRunner();
    registerFn(runner, stubCtx);
    const registered = countTests(runner.suites);
    totalRegistered += registered;
    if (registered === 0) zeroRegistration.push(mod);
  }

  assert.deepStrictEqual(
    zeroRegistration, [],
    '以下模块注册了 0 个用例, 不得视为通过: ' + zeroRegistration.join(', ')
  );

  // 5: 移植后的 SQL 编辑器模块
  const sqlEditorMod = './tests/43-sql-editor-large-text';
  assert.ok(registeredSorted.includes(sqlEditorMod), `${sqlEditorMod} 必须注册进 testModules`);
  const sqlRunner = makeStubRunner();
  require(path.join(E2E_DIR, 'tests', '43-sql-editor-large-text.js')).register(sqlRunner, stubCtx);
  const sqlCases = countTests(sqlRunner.suites);
  assert.strictEqual(sqlCases, 4, `43-sql-editor-large-text 应注册 4 个用例, got=${sqlCases}`);

  console.log(
    `e2e-registration passed: ${registeredSorted.length} 个模块全部注册, 共发现 ${totalRegistered} 个用例`
  );
}

try {
  main();
} catch (e) {
  console.error('e2e-registration FAILED:', e && e.message ? e.message : e);
  process.exit(1);
}
