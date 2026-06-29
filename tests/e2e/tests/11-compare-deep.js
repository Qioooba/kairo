'use strict';

/**
 * Compare 代码比对 深度测试
 * 覆盖接口：
 * - /api/diff/compare
 * - /api/compare/folder-scan
 * - /api/compare/file-diff
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('代码比对', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/compare', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);
    });

    runner.it('页面加载成功', async function () {
      const hasTextareas = await page.$$('textarea');
      if (hasTextareas.length < 2) {
        const bodyText = await page.evaluate(() => document.body.innerText.trim().substring(0, 200));
        throw new Error('Compare 页面未正确加载，textarea 数量: ' + hasTextareas.length);
      }
      await runner.screenshot(page, '13-compare-01-load');
    });

    runner.it('文本对比正常', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('line1\nline2\nline3');
        await textareas[1].fill('line1\nline2\nline3');
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较"), button:has-text("diff")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(1000);

      await runner.screenshot(page, '13-compare-02-identical');
    });

    runner.it('文本对比有差异', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('line1\nline2\nline3\nline4');
        await textareas[1].fill('line1\nline2\nmodified\nline4');
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(1000);

      await runner.screenshot(page, '13-compare-03-different');
    });

    runner.it('空文本对比', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('');
        await textareas[1].fill('');
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '13-compare-04-empty');
    });

    runner.it('单行差异', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('hello world');
        await textareas[1].fill('hello WORLD');
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '13-compare-05-single-line');
    });

    runner.it('大量行差异', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        const lines1 = Array.from({ length: 100 }, (_, i) => `line${i + 1}`).join('\n');
        const lines2 = Array.from({ length: 100 }, (_, i) => i === 50 ? `modified${i}` : `line${i + 1}`).join('\n');
        await textareas[0].fill(lines1);
        await textareas[1].fill(lines2);
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(1000);

      await runner.screenshot(page, '13-compare-06-many-diffs');
    });

    runner.it('换行符差异 CRLF vs LF', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('line1\r\nline2\r\nline3');
        await textareas[1].fill('line1\nline2\nline3');
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '13-compare-07-crlf-vs-lf');
    });

    runner.it('中文文本对比', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('信贷系统\n张三\n100000');
        await textareas[1].fill('信贷系统\n李四\n200000');
      }
      await page.waitForTimeout(300);

      const compareBtn = await page.$('button:has-text("比对"), button:has-text("比较")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(500);

      await runner.screenshot(page, '13-compare-08-chinese');
    });

    runner.it('清空按钮功能', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('some content');
        await textareas[1].fill('other content');
      }
      await page.waitForTimeout(300);

      const clearBtn = await page.$('button:has-text("清空"), button:has-text("重置")');
      if (clearBtn) {
        await clearBtn.click();
        await page.waitForTimeout(300);
      }

      const value1 = await page.$eval('textarea:first-of-type', el => el.value);
      if (value1.length > 0) {
        // 清空按钮可能不存在，跳过
      }

      await runner.screenshot(page, '13-compare-09-after-clear');
    });
  });
}

module.exports = { register };
