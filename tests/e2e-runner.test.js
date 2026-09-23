'use strict';

/**
 * tests/e2e-runner.test.js (QA-02)
 *
 * 覆盖 scripts 之外、但属于 E2E 可信度的 runner 行为 —— 不需要浏览器:
 *   1. discovered(total) 只统计被过滤后保留的用例; registered 统计原始注册数
 *   2. executed 只统计真正跑过的用例 (passed+failed), 声明式 skip 与 skipTest 都不算执行
 *   3. --grep 零匹配时 results.noMatch === true (上层据此判失败, 不允许"过滤跑空 = 通过")
 *   4. 每条 suite 结果带 index, 便于按模块归属统计
 *   5. 失败截图文件名绑定 SHA + viewport, 不同提交/分辨率不互相覆盖
 *   6. 用例结束后不残留超时定时器 (曾经每个用例泄漏一个存活 180s 的 timer,
 *      导致 run() 结束后事件循环被占住、进程无法正常退出)
 *
 * 这些行为是 Batch A 为 QA-02 新增的, 之前只有"模块能注册"的结构检查, 没有 runner 自身
 * 的回归。runner 只在 init() 时启动浏览器, 因此这里可以直接构造并 run()。
 */

const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');

const TestRunner = require('./e2e/utils/test-runner');

/** makeFakePage 记录截图调用, 不启动浏览器。 */
function makeFakePage() {
  const shots = [];
  return {
    shots: shots,
    async screenshot(opts) {
      shots.push(opts && opts.path);
      if (opts && opts.path) {
        fs.writeFileSync(opts.path, 'fake-png');
      }
    },
  };
}

function makeRunner(opts) {
  return new TestRunner(Object.assign({
    baseUrl: 'http://example.invalid',
    headless: true,
    viewport: { width: 1366, height: 900 },
    runMeta: { shortSha: 'abc1234', viewportLabel: '1366x900' },
  }, opts || {}));
}

async function main() {
  // ---- 1+2+4: 通过/失败/两种 skip 的统计 ----
  {
    const runner = makeRunner();
    runner.describe('suite-pass', function () {
      runner.it('ok-1', async function () {});
      runner.it('ok-2', async function () {});
    });
    runner.describe('suite-mixed', function () {
      runner.it('fails', async function () { throw new Error('boom'); });
      runner.it.skip('declared-skip', async function () {});
      runner.it('runtime-skip', async function () { runner.skipTest('环境不满足'); });
    });

    const page = makeFakePage();
    const results = await runner.run({ page: page });

    assert.strictEqual(results.noMatch, false, '有匹配用例时 noMatch 必须为 false');
    assert.strictEqual(results.summary.total, 5, `discovered 应为 5, got=${results.summary.total}`);
    assert.strictEqual(results.summary.passed, 2, `passed 应为 2, got=${results.summary.passed}`);
    assert.strictEqual(results.summary.failed, 1, `failed 应为 1, got=${results.summary.failed}`);
    assert.strictEqual(results.summary.skipped, 2, `skipped 应为 2, got=${results.summary.skipped}`);
    assert.strictEqual(
      results.summary.executed, 3,
      `executed 必须只含 passed+failed(3), 不含 skip; got=${results.summary.executed}`
    );

    // registered 是模块原始注册数; 无 grep 时与 discovered 相同
    for (const suite of results.suites) {
      assert.strictEqual(
        suite.summary.registered, suite.tests.length,
        `${suite.name}: registered 应等于该 suite 注册用例数`
      );
      assert.strictEqual(typeof suite.index, 'number', `${suite.name}: 必须带 index 以便按模块归属`);
    }
    const indexes = results.suites.map(function (s) { return s.index; });
    assert.deepStrictEqual(indexes, [0, 1], `suite index 应按注册顺序, got=${JSON.stringify(indexes)}`);
  }

  // ---- 3: grep 零匹配 ----
  {
    const runner = makeRunner({ grep: '绝不可能匹配的用例名-zzz' });
    runner.describe('suite-a', function () {
      runner.it('case-1', async function () {});
    });

    let executed = 0;
    runner.describe('suite-b', function () {
      runner.it('case-2', async function () { executed++; });
    });

    const results = await runner.run({ page: makeFakePage() });
    assert.strictEqual(results.summary.total, 0, '零匹配时 discovered 应为 0');
    assert.strictEqual(results.summary.executed, 0, '零匹配时不得执行任何用例');
    assert.strictEqual(executed, 0, '零匹配时用例函数不得被调用');
    assert.strictEqual(results.noMatch, true, '零匹配必须标记 noMatch, 否则会被误读成通过');
  }

  // ---- 3b: grep 命中时 noMatch 为 false 且 registered 保留原始数量 ----
  {
    const runner = makeRunner({ grep: 'case-keep' });
    runner.describe('suite-c', function () {
      runner.it('case-drop', async function () {});
      runner.it('case-keep', async function () {});
    });

    const results = await runner.run({ page: makeFakePage() });
    assert.strictEqual(results.noMatch, false, '命中时 noMatch 应为 false');
    assert.strictEqual(results.summary.total, 1, `过滤后 discovered 应为 1, got=${results.summary.total}`);
    assert.strictEqual(results.summary.executed, 1, '过滤后 executed 应为 1');
    assert.strictEqual(
      results.suites[0].summary.registered, 2,
      'registered 必须保留原始注册数(2), 以便看出被过滤掉了多少'
    );
  }

  // ---- 5: 失败截图绑定 SHA + viewport ----
  {
    const shotsDir = fs.mkdtempSync(path.join(os.tmpdir(), 'kairo-e2e-shots-'));
    const runner = makeRunner({ screenshotsDir: shotsDir });
    runner.describe('截图套件', function () {
      runner.it('会失败的用例', async function () { throw new Error('故意的失败'); });
    });

    const results = await runner.run({ page: makeFakePage() });
    assert.strictEqual(results.summary.failed, 1, '应记录 1 个失败');
    const shot = results.suites[0].tests[0].screenshots[0];
    assert.ok(shot, '失败时必须留下截图路径');
    const base = path.basename(shot);
    assert.ok(base.includes('abc1234'), `截图名必须含 SHA(abc1234), got=${base}`);
    assert.ok(
      base.includes('1366x900') || base.includes('1366_900'),
      `截图名必须含 viewport, got=${base}`
    );
    assert.ok(fs.existsSync(shot), '截图文件应真实写出');
    fs.rmSync(shotsDir, { recursive: true, force: true });
  }

  // ---- 6: 用例结束后不得残留超时定时器 ----
  //
  // 这是本测试首次运行时暴露的真实缺陷: 旧实现把 setTimeout 直接塞进 Promise.race
  // 且从不 clearTimeout, 于是每个用例都留下一个存活到 timeoutMs(默认 180s) 的定时器;
  // run() 结束后事件循环仍被占住, 进程无法自然退出(index.js 靠末尾 process.exit()
  // 掩盖了它)。这里用活动资源数的增量做断言: 跑完一批用例后 Timeout 数量不得增长。
  {
    const runner = makeRunner();
    runner.describe('timer-leak', function () {
      for (let i = 0; i < 5; i++) {
        runner.it('leak-case-' + i, async function () {});
      }
    });

    const countTimeouts = function () {
      if (typeof process.getActiveResourcesInfo !== 'function') return null; // 老 Node 跳过
      return process.getActiveResourcesInfo().filter(function (t) { return t === 'Timeout'; }).length;
    };

    const before = countTimeouts();
    await runner.run({ page: makeFakePage() });
    const after = countTimeouts();

    if (before !== null && after !== null) {
      assert.ok(
        after <= before,
        `用例结束后不得残留定时器: 运行前 Timeout=${before}, 运行后 Timeout=${after}` +
        ' (超时定时器必须在 finally 中 clearTimeout)'
      );
    }
  }

  console.log('e2e-runner passed: discovered/executed/skip 统计、noMatch 判定、SHA+viewport 截图命名均符合预期');
}

main().catch(function (e) {
  console.error('e2e-runner FAILED:', e && e.message ? e.message : e);
  process.exit(1);
});
