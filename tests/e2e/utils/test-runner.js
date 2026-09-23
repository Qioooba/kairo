'use strict';

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');
const helpers = require('./helpers');

function nowIso() { return new Date().toISOString(); }
function shortErr(e) { return (e && e.message) ? e.message : String(e); }

class TestRunner {
  constructor(options = {}) {
    this.baseUrl = options.baseUrl || 'http://127.0.0.1:18090';
    this.headless = options.headless !== undefined ? options.headless : true;
    this.viewport = options.viewport || { width: 1366, height: 900 };
    this.screenshotsDir = options.screenshotsDir || null;
    this.grep = options.grep || null;
    // runMeta 绑定本次运行的 SHA / viewport / browser, 用于失败截图命名与报告。
    this.runMeta = options.runMeta || {};
    this.shortSha = this.runMeta.shortSha || 'nosha';
    this.viewportLabel = this.runMeta.viewportLabel ||
      `${this.viewport.width}x${this.viewport.height}`;

    this.timeouts = {
      action: options.actionTimeout || 10000,
      pageLoad: options.pageLoadTimeout || 15000,
      network: options.networkTimeout || 10000,
    };

    this.browser = null;
    this.context = null;
    this.page = null;

    this.suites = [];
    this.currentSuite = null;
    this.currentTest = null;

    this.beforeEachHooks = [];
    this.afterEachHooks = [];
    this.beforeAllHooks = [];
    this.afterAllHooks = [];

    this.helpers = helpers;
  }

  async init() {
    const launch = { headless: this.headless };
    if (process.env.PLAYWRIGHT_CHANNEL) launch.channel = process.env.PLAYWRIGHT_CHANNEL;
    this.browser = await chromium.launch(launch);
    this.context = await this.browser.newContext({
      viewport: this.viewport,
      locale: 'zh-CN',
    });
    this.page = await this.context.newPage();

    this.page.setDefaultTimeout(this.timeouts.action);
    this.page.setDefaultNavigationTimeout(this.timeouts.pageLoad);

    let dialogHandled = false;
    this.page.on('dialog', async (d) => {
      if (dialogHandled) return;
      try {
        dialogHandled = true;
        await d.accept();
      } catch (e) {
        // ignore if already handled
      } finally {
        setTimeout(() => { dialogHandled = false; }, 100);
      }
    });

    this.beforeEach(async (ctx) => {
      try {
        if (ctx.page) {
          await helpers.clearTransientUI(ctx.page);
        }
      } catch (e) {
        // ignore
      }
    });

    this.afterEach(async (ctx) => {
      try {
        if (ctx.page) {
          await helpers.clearTransientUI(ctx.page);
        }
      } catch (e) {
        // ignore
      }
    });
  }

  async close() {
    if (this.context) {
      await this.context.close();
      this.context = null;
    }
    if (this.browser) {
      await this.browser.close();
      this.browser = null;
    }
    this.page = null;
  }

  describe(name, fn) {
    const suite = {
      name,
      tests: [],
      suites: [],
      _fn: fn,
      _beforeAll: [],
      _afterAll: [],
      _beforeEach: [],
      _afterEach: [],
      parent: this.currentSuite,
    };

    if (this.currentSuite) {
      this.currentSuite.suites.push(suite);
    } else {
      this.suites.push(suite);
    }

    const prev = this.currentSuite;
    this.currentSuite = suite;
    try {
      fn.call(this);
    } finally {
      this.currentSuite = prev;
    }
  }

  it(name, fn) {
    if (!this.currentSuite) {
      throw new Error('it() must be called inside describe()');
    }
    this.currentSuite.tests.push({
      name,
      status: 'pending',
      duration: 0,
      error: null,
      screenshots: [],
      skip: false,
      _fn: fn,
    });
  }

  get it() {
    const self = this;
    const fn = function (name, testFn) {
      return self._it(name, testFn, false);
    };
    fn.skip = function (name, testFn) {
      return self._it(name, testFn, true);
    };
    return fn;
  }

  set it(value) {
    // setter is required for getter/setter pair, but we use the getter version
  }

  _it(name, fn, skip) {
    if (!this.currentSuite) {
      throw new Error('it() must be called inside describe()');
    }
    this.currentSuite.tests.push({
      name,
      status: 'pending',
      duration: 0,
      error: null,
      screenshots: [],
      skip: skip,
      _fn: fn,
    });
  }

  beforeAll(fn) {
    if (!this.currentSuite) {
      throw new Error('beforeAll() must be called inside describe()');
    }
    this.currentSuite._beforeAll.push(fn);
  }

  afterAll(fn) {
    if (!this.currentSuite) {
      throw new Error('afterAll() must be called inside describe()');
    }
    this.currentSuite._afterAll.push(fn);
  }

  beforeEach(fn) {
    if (!this.currentSuite) {
      this.beforeEachHooks.push(fn);
    } else {
      this.currentSuite._beforeEach.push(fn);
    }
  }

  afterEach(fn) {
    if (!this.currentSuite) {
      this.afterEachHooks.push(fn);
    } else {
      this.currentSuite._afterEach.push(fn);
    }
  }

  skipTest(reason) {
    if (this.currentTest) {
      this.currentTest._skipped = true;
      this.currentTest._skipReason = reason || 'skipped';
    }
  }

  async _runSuite(suite, ctx, parentHooks, ancestorMatched) {
    parentHooks = parentHooks || { beforeEach: [], afterEach: [] };
    ancestorMatched = !!ancestorMatched;
    const suiteResults = {
      name: suite.name,
      tests: [],
      suites: [],
      // discovered=注册后被本次过滤保留的用例数; registered=模块原始注册数;
      // executed=真正跑过的用例数 (passed+failed, 不含 skip)
      summary: { total: 0, passed: 0, failed: 0, skipped: 0, executed: 0, registered: suite.tests.length },
    };

    if (this.grep && !ancestorMatched) {
      const containsMatch = (candidate) => {
        if (candidate.name.includes(this.grep)) return true;
        if (candidate.tests.some((test) => test.name.includes(this.grep))) return true;
        return candidate.suites.some(containsMatch);
      };
      if (!containsMatch(suite)) return suiteResults;
    }

    const allBeforeEach = [...parentHooks.beforeEach, ...suite._beforeEach];
    const allAfterEach = [...suite._afterEach, ...parentHooks.afterEach];
    const suiteMatched = ancestorMatched || !this.grep || suite.name.includes(this.grep);

    for (const hook of suite._beforeAll) {
      try {
        await hook.call(this, ctx);
      } catch (e) {
        // ignore beforeAll errors?
      }
    }

    for (const test of suite.tests) {
      if (this.grep && !suiteMatched && !test.name.includes(this.grep)) {
        continue;
      }

      suiteResults.summary.total++;
      const testResult = {
        name: test.name,
        status: 'skipped',
        error: null,
        duration: 0,
        screenshots: [],
      };

      if (test.skip) {
        testResult.status = 'skipped';
        suiteResults.summary.skipped++;
      } else if (typeof test._fn === 'function') {
        const startTime = Date.now();
        this.currentTest = test;
        if (process.env.DEBUG_E2E) {
          console.log('  → 开始:', suite.name + ' > ' + test.name);
        }

        for (const hook of this.beforeEachHooks) {
          try { await hook.call(this, ctx); } catch (e) { /* ignore */ }
        }
        for (const hook of allBeforeEach) {
          try { await hook.call(this, ctx); } catch (e) { /* ignore */ }
        }

        try {
          const timeoutMs = Number(test.timeout || suite.timeout || process.env.TEST_TIMEOUT) || 180000;
          await Promise.race([
            test._fn.call(this, ctx),
            new Promise((_, reject) => setTimeout(() => reject(new Error('测试超时 (' + Math.round(timeoutMs / 1000) + 's)')), timeoutMs))
          ]);
          if (test._skipped) {
            testResult.status = 'skipped';
            testResult.error = { message: test._skipReason || 'skipped' };
            suiteResults.summary.skipped++;
          } else {
            testResult.status = 'passed';
            suiteResults.summary.passed++;
          }
        } catch (err) {
          if (test._skipped) {
            testResult.status = 'skipped';
            testResult.error = { message: test._skipReason || err.message };
            suiteResults.summary.skipped++;
          } else {
            testResult.status = 'failed';
            testResult.error = { message: err.message, stack: err.stack };
            suiteResults.summary.failed++;
          }

          try {
            if (ctx.page && this.screenshotsDir) {
              // 失败截图绑定 SHA + viewport, 避免不同提交/不同分辨率的截图互相覆盖。
              const safeName = helpers.sanitizeFileName(
                `${suite.name}-${test.name}-${this.shortSha}-${this.viewportLabel}`
              );
              const screenshotPath = await helpers.takeScreenshot(ctx.page, safeName, this.screenshotsDir);
              testResult.screenshots.push(screenshotPath);
              this.addScreenshot(screenshotPath);
            }
          } catch (screenshotErr) {
            // ignore screenshot errors
          }
        } finally {
          for (const hook of allAfterEach) {
            try { await hook.call(this, ctx); } catch (e) { /* ignore */ }
          }
          for (const hook of this.afterEachHooks) {
            try { await hook.call(this, ctx); } catch (e) { /* ignore */ }
          }
        }

        testResult.duration = Date.now() - startTime;
        this.currentTest = null;
      }

      if (testResult.status === 'passed' || testResult.status === 'failed') {
        suiteResults.summary.executed++;
      }
      suiteResults.tests.push(testResult);
    }

    for (const subSuite of suite.suites) {
      const subResult = await this._runSuite(subSuite, ctx, {
        beforeEach: allBeforeEach,
        afterEach: allAfterEach,
      }, suiteMatched);
      suiteResults.suites.push(subResult);
      suiteResults.summary.total += subResult.summary.total;
      suiteResults.summary.passed += subResult.summary.passed;
      suiteResults.summary.failed += subResult.summary.failed;
      suiteResults.summary.skipped += subResult.summary.skipped;
      suiteResults.summary.executed += subResult.summary.executed;
      suiteResults.summary.registered += subResult.summary.registered;
    }

    for (const hook of suite._afterAll) {
      try {
        await hook.call(this, ctx);
      } catch (e) {
        // ignore afterAll errors
      }
    }

    return suiteResults;
  }

  async run(ctx) {
    ctx = ctx || {};
    const page = ctx.page || this.page;
    const results = {
      suites: [],
      noMatch: false,
      summary: { total: 0, passed: 0, failed: 0, skipped: 0, executed: 0, duration: 0 },
    };

    const startTime = Date.now();
    const runCtx = {
      ...ctx,
      page,
      helpers: this.helpers,
      timeouts: this.timeouts,
      baseUrl: this.baseUrl,
    };

    for (let suiteIndex = 0; suiteIndex < this.suites.length; suiteIndex++) {
      const suite = this.suites[suiteIndex];
      const suiteResult = await this._runSuite(suite, runCtx);
      if (this.grep && suiteResult.summary.total === 0) continue;
      // index 保留顶层 suite 的注册序号, 便于把执行结果按模块归属统计 (QA-02)。
      suiteResult.index = suiteIndex;
      results.suites.push(suiteResult);
      results.summary.total += suiteResult.summary.total;
      results.summary.passed += suiteResult.summary.passed;
      results.summary.failed += suiteResult.summary.failed;
      results.summary.skipped += suiteResult.summary.skipped;
      results.summary.executed += suiteResult.summary.executed;
    }

    // QA-02: --grep 一个用例都没匹配上时必须显式标记, 不能输出"全绿"。
    // 否则选择器/用例改名后, 过滤跑空会被误读成通过。
    if (this.grep && results.summary.total === 0) {
      results.noMatch = true;
    }

    results.summary.duration = Date.now() - startTime;
    return results;
  }

  addScreenshot(screenshotPath) {
    if (this.currentTest) {
      this.currentTest.screenshots.push(screenshotPath);
    }
  }

  async screenshot(page, name) {
    if (!this.screenshotsDir) return null;
    if (!fs.existsSync(this.screenshotsDir)) {
      fs.mkdirSync(this.screenshotsDir, { recursive: true });
    }
    const p = path.join(this.screenshotsDir, name + '.png');
    await page.screenshot({ path: p, fullPage: true });
    this.addScreenshot(p);
    return p;
  }

  getResults() {
    const flattenSuites = (suites) => {
      let result = [];
      for (const s of suites) {
        result.push({
          name: s.name,
          tests: s.tests.map((t) => ({
            name: t.name,
            status: t.status,
            duration: t.duration,
            error: t.error,
            screenshots: t.screenshots,
          })),
        });
        if (s.suites && s.suites.length > 0) {
          result = result.concat(flattenSuites(s.suites));
        }
      }
      return result;
    };

    const flatSuites = flattenSuites(this.suites);
    let total = 0, passed = 0, failed = 0, skipped = 0;
    for (const s of flatSuites) {
      for (const t of s.tests) {
        total++;
        if (t.status === 'passed') passed++;
        else if (t.status === 'failed') failed++;
        else skipped++;
      }
    }

    return {
      suites: flatSuites,
      summary: { total, passed, failed, skipped },
    };
  }
}

module.exports = TestRunner;
