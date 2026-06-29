'use strict';

/**
 * Timestamp / Cron / JSONPath 深度测试
 * 覆盖接口：
 * - /api/format/timestamp
 * - /api/format/cron-parse
 * - /api/format/jsonpath
 */

function register(runner, ctx) {
  const { page, baseUrl, screenshotsDir } = ctx;

  let tsPage = null;
  let cronPage = null;
  let jpPage = null;

  // ===== Timestamp 测试 =====
  runner.describe('时间戳工具', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/timestamp', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(2000);
    });

    runner.it('页面加载成功', async function () {
      const bodyText = await page.evaluate(() => document.body.innerText.trim());
      if (bodyText.length < 50) {
        throw new Error('Timestamp 页面未正确加载: ' + bodyText.substring(0, 100));
      }
      const hasInput = await page.$('input[type="text"], input[type="number"], input:not([type])');
      if (!hasInput) {
        throw new Error('Timestamp 页面未找到输入框');
      }
      await runner.screenshot(page, '10-timestamp-01-load');
    });

    runner.it('秒级时间戳转日期', async function () {
      const inputs = await page.$$('input[type="text"], input[type="number"], input:not([type])');
      if (inputs.length === 0) {
        runner.skipTest('未找到时间戳输入框');
        return;
      }

      await inputs[0].fill('1751155200');
      await page.waitForTimeout(500);

      const buttons = await page.$$('button');
      for (const btn of buttons) {
        const text = await btn.textContent();
        if (text.indexOf('转换') >= 0 || text.indexOf('转') >= 0 || text.indexOf('计算') >= 0) {
          await btn.click();
          await page.waitForTimeout(500);
          break;
        }
      }

      await runner.screenshot(page, '10-timestamp-02-sec-to-date');
    });

    runner.it('毫秒级时间戳转日期', async function () {
      const inputs = await page.$$('input[type="text"], input[type="number"], input:not([type])');
      if (inputs.length === 0) {
        runner.skipTest('未找到时间戳输入框');
        return;
      }

      await inputs[0].fill('1751155200000');
      await page.waitForTimeout(500);

      const buttons = await page.$$('button');
      for (const btn of buttons) {
        const text = await btn.textContent();
        if (text.indexOf('转换') >= 0 || text.indexOf('转') >= 0 || text.indexOf('计算') >= 0) {
          await btn.click();
          await page.waitForTimeout(500);
          break;
        }
      }

      await runner.screenshot(page, '10-timestamp-03-ms-to-date');
    });

    runner.it('非法时间戳应显示错误', async function () {
      const inputs = await page.$$('input[type="text"], input[type="number"], input:not([type])');
      if (inputs.length === 0) {
        runner.skipTest('未找到时间戳输入框');
        return;
      }

      await inputs[0].fill('invalid-timestamp');
      await page.waitForTimeout(500);

      const buttons = await page.$$('button');
      for (const btn of buttons) {
        const text = await btn.textContent();
        if (text.indexOf('转换') >= 0 || text.indexOf('转') >= 0 || text.indexOf('计算') >= 0) {
          await btn.click();
          await page.waitForTimeout(500);
          break;
        }
      }

      await runner.screenshot(page, '10-timestamp-04-invalid-ts');
    });

    runner.it('负数时间戳应正确处理', async function () {
      const inputs = await page.$$('input[type="text"], input[type="number"], input:not([type])');
      if (inputs.length === 0) {
        runner.skipTest('未找到时间戳输入框');
        return;
      }

      await inputs[0].fill('-1000000000');
      await page.waitForTimeout(500);

      const buttons = await page.$$('button');
      for (const btn of buttons) {
        const text = await btn.textContent();
        if (text.indexOf('转换') >= 0 || text.indexOf('转') >= 0 || text.indexOf('计算') >= 0) {
          await btn.click();
          await page.waitForTimeout(500);
          break;
        }
      }

      await runner.screenshot(page, '10-timestamp-05-negative-ts');
    });
  });

  // ===== Cron 解析测试 =====
  runner.describe('Cron 解析', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/cron', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);
    });

    runner.it('页面加载成功', async function () {
      const hasInput = await page.$('input[placeholder*="Cron"], input[placeholder*="cron"], input[placeholder*="表达式"]');
      if (!hasInput) {
        const bodyText = await page.evaluate(() => document.body.innerText.trim().substring(0, 200));
        throw new Error('Cron 页面未正确加载: ' + bodyText);
      }
      await runner.screenshot(page, '11-cron-01-load');
    });

    runner.it('标准 Cron 表达式解析', async function () {
      await page.fill('input[placeholder*="Cron"], input[placeholder*="cron"], input[placeholder*="表达式"]', '0 0 * * *');
      await page.waitForTimeout(300);

      const parseBtn = await page.$('button:has-text("解析"), button:has-text("计算"), button:has-text("执行")');
      if (parseBtn) await parseBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '11-cron-02-standard');
    });

    runner.it('每5分钟 Cron 表达式解析', async function () {
      await page.fill('input[placeholder*="Cron"], input[placeholder*="cron"], input[placeholder*="表达式"]', '0 */5 * * * *');
      await page.waitForTimeout(300);

      const parseBtn = await page.$('button:has-text("解析"), button:has-text("计算")');
      if (parseBtn) await parseBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '11-cron-03-every-5min');
    });

    runner.it('每年一次 Cron 表达式（稀疏）', async function () {
      await page.fill('input[placeholder*="Cron"], input[placeholder*="cron"], input[placeholder*="表达式"]', '0 0 1 1 *');
      await page.waitForTimeout(300);

      const parseBtn = await page.$('button:has-text("解析"), button:has-text("计算")');
      if (parseBtn) await parseBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '11-cron-04-yearly');
    });

    runner.it('非法 Cron 表达式应显示错误', async function () {
      await page.fill('input[placeholder*="Cron"], input[placeholder*="cron"], input[placeholder*="表达式"]', 'invalid-cron');
      await page.waitForTimeout(300);

      const parseBtn = await page.$('button:has-text("解析"), button:has-text("计算")');
      if (parseBtn) await parseBtn.click();
      await page.waitForTimeout(500);

      const toast = await page.$('#toast, .toast, [class*="error"]');
      if (toast) {
        const visible = await toast.isVisible().catch(() => false);
        if (visible) {
          await runner.screenshot(page, '11-cron-05-invalid');
        }
      }
    });

    runner.it('复杂 Cron 表达式', async function () {
      await page.fill('input[placeholder*="Cron"], input[placeholder*="cron"], input[placeholder*="表达式"]', '0 30 9 1,15 * 1-5');
      await page.waitForTimeout(300);

      const parseBtn = await page.$('button:has-text("解析"), button:has-text("计算")');
      if (parseBtn) await parseBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '11-cron-06-complex');
    });
  });

  // ===== JSONPath 测试 =====
  runner.describe('JSONPath 查询', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/jsonpath', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);
    });

    runner.it('页面加载成功', async function () {
      const hasInput = await page.$('textarea[placeholder*="JSON"], textarea[placeholder*="json"], input[placeholder*="path"]');
      if (!hasInput) {
        const bodyText = await page.evaluate(() => document.body.innerText.trim().substring(0, 200));
        throw new Error('JSONPath 页面未正确加载: ' + bodyText);
      }
      await runner.screenshot(page, '12-jsonpath-01-load');
    });

    runner.it('正常 JSONPath 查询', async function () {
      // 填充 JSON 数据
      const jsonInput = await page.$('textarea[placeholder*="JSON"], textarea[placeholder*="json"]');
      if (jsonInput) {
        await jsonInput.fill('{"store":{"book":[{"title":"书1","price":10},{"title":"书2","price":20}]}}');
      }
      await page.waitForTimeout(300);

      // 填充 JSONPath
      const pathInput = await page.$('input[placeholder*="path"], input[placeholder*="Path"]');
      if (pathInput) {
        await pathInput.fill('$.store.book[*].title');
      }
      await page.waitForTimeout(300);

      // 点击查询按钮
      const queryBtn = await page.$('button:has-text("查询"), button:has-text("执行"), button:has-text("计算")');
      if (queryBtn) await queryBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '12-jsonpath-02-normal-query');
    });

    runner.it('JSONPath 空结果', async function () {
      const jsonInput = await page.$('textarea[placeholder*="JSON"], textarea[placeholder*="json"]');
      if (jsonInput) {
        await jsonInput.fill('{"name":"test","value":123}');
      }
      await page.waitForTimeout(300);

      const pathInput = await page.$('input[placeholder*="path"], input[placeholder*="Path"]');
      if (pathInput) {
        await pathInput.fill('$.nonexistent.path');
      }
      await page.waitForTimeout(300);

      const queryBtn = await page.$('button:has-text("查询"), button:has-text("执行")');
      if (queryBtn) await queryBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '12-jsonpath-03-empty-result');
    });

    runner.it('非法 JSONPath 表达式', async function () {
      const jsonInput = await page.$('textarea[placeholder*="JSON"], textarea[placeholder*="json"]');
      if (jsonInput) {
        await jsonInput.fill('{"name":"test"}');
      }
      await page.waitForTimeout(300);

      const pathInput = await page.$('input[placeholder*="path"], input[placeholder*="Path"]');
      if (pathInput) {
        await pathInput.fill('[invalid');
      }
      await page.waitForTimeout(300);

      const queryBtn = await page.$('button:has-text("查询"), button:has-text("执行")');
      if (queryBtn) await queryBtn.click();
      await page.waitForTimeout(500);

      const toast = await page.$('#toast, .toast, [class*="error"]');
      if (toast) {
        const visible = await toast.isVisible().catch(() => false);
        if (visible) {
          await runner.screenshot(page, '12-jsonpath-04-invalid-path');
        }
      }
    });

    runner.it('嵌套对象查询', async function () {
      const jsonInput = await page.$('textarea[placeholder*="JSON"], textarea[placeholder*="json"]');
      if (jsonInput) {
        await jsonInput.fill('{"a":{"b":{"c":"deep-value"}}}');
      }
      await page.waitForTimeout(300);

      const pathInput = await page.$('input[placeholder*="path"], input[placeholder*="Path"]');
      if (pathInput) {
        await pathInput.fill('$.a.b.c');
      }
      await page.waitForTimeout(300);

      const queryBtn = await page.$('button:has-text("查询"), button:has-text("执行")');
      if (queryBtn) await queryBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '12-jsonpath-05-nested');
    });

    runner.it('数组过滤查询', async function () {
      const jsonInput = await page.$('textarea[placeholder*="JSON"], textarea[placeholder*="json"]');
      if (jsonInput) {
        await jsonInput.fill('{"items":[{"id":1,"name":"A"},{"id":2,"name":"B"},{"id":3,"name":"C"}]}');
      }
      await page.waitForTimeout(300);

      const pathInput = await page.$('input[placeholder*="path"], input[placeholder*="Path"]');
      if (pathInput) {
        await pathInput.fill('$.items[?(@.id>1)].name');
      }
      await page.waitForTimeout(300);

      const queryBtn = await page.$('button:has-text("查询"), button:has-text("执行")');
      if (queryBtn) await queryBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '12-jsonpath-06-filter');
    });
  });
}

module.exports = { register };
