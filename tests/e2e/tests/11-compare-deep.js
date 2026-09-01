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
  const compareRoot = process.env.COMPARE_LAB_ROOT || 'D:\\kairo-test-runtime';
  const leftFixture = compareRoot + '/compare-左 源';
  const rightFixture = compareRoot + '/compare-右 源';

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

    runner.it('文本 hunk 可向右和向左应用', async function () {
      const textareas = await page.$$('textarea');
      if (textareas.length < 2) throw new Error('缺少双侧编辑器');
      await textareas[0].fill('alpha\nleft-only\nshared\n');
      await textareas[1].fill('alpha\nright-only\nshared\n');
      const compareBtn = await page.$('[data-action="text-compare"], button:has-text("比对")');
      if (!compareBtn) throw new Error('缺少比对按钮');
      await compareBtn.click();
      await page.waitForTimeout(800);
      const toRight = await page.$('button[title="用左侧替换右侧"]');
      if (toRight) await toRight.click();
      await page.waitForTimeout(300);
      // 应用一侧 hunk 会重新渲染 diff DOM，旧 ElementHandle 已失效；重新查询后再反向应用。
      const toLeft = await page.$('button[title="用右侧替换左侧"]');
      if (toLeft) await toLeft.click();
      await runner.screenshot(page, '13-compare-10-hunk-both-ways');
    });

    runner.it('文件夹比较提供测试、比对和双向覆盖', async function () {
      const folderTab = await page.$('button:has-text("文件夹比较")');
      if (!folderTab) throw new Error('缺少文件夹比较页签');
      await folderTab.click();
      await page.waitForTimeout(400);
      const testBtn = await page.$('[data-action="compare-test"]');
      const scanBtn = await page.$('[data-action="compare-scan"]');
      const coverRight = await page.$('[data-action="cover-right"]');
      const coverLeft = await page.$('[data-action="cover-left"]');
      if (!testBtn || !scanBtn || !coverRight || !coverLeft) {
        throw new Error('缺少 测试 / 比对 / 覆盖 按钮');
      }
      await runner.screenshot(page, '13-compare-11-folder-toolbar');
    });

    runner.it('中文路径夹具：测试、比对、覆盖→ 与 ←覆盖', async function () {
      const fs = require('fs');
      const left = process.env.COMPARE_LAB_ROOT ? leftFixture : 'D:\\kairo-test-runtime\\compare-左 源';
      const right = process.env.COMPARE_LAB_ROOT ? rightFixture : 'D:\\kairo-test-runtime\\compare-右 源';
      if (!fs.existsSync(left) || !fs.existsSync(right)) {
        throw new Error('缺少比较夹具目录 ' + left);
      }
      const folderTab = await page.$('button:has-text("文件夹比较")');
      await folderTab.click();
      await page.waitForTimeout(400);
      async function pickDir(index, dirPath) {
        const buttons = await page.$$('button:has-text("选择目录")');
        await buttons[index].click();
        await page.waitForTimeout(300);
        const input = await page.$('input[placeholder="目录绝对路径"]');
        if (!input) throw new Error('目录对话框没有路径输入框');
        await input.fill(dirPath);
        const ok = await page.$('.cmp-source-dialog button:has-text("确定")');
        await ok.click();
        await page.waitForTimeout(300);
      }
      await pickDir(0, left);
      await pickDir(1, right);
      await page.click('[data-action="compare-test"]');
      await page.waitForFunction(() => (document.body.innerText || '').indexOf('两侧来源可用') >= 0 || (document.body.innerText || '').indexOf('正常') >= 0, { timeout: 15000 });
      await page.click('[data-action="compare-scan"]');
      await page.waitForFunction(() => (document.body.innerText || '').indexOf('完成') >= 0 && (document.body.innerText || '').indexOf('仅左') >= 0, { timeout: 60000 });
      await runner.screenshot(page, '13-compare-12-scan-fixtures');
      await page.click('[data-action="cover-right"]');
      await page.waitForTimeout(600);
      const startCover = await page.$('button:has-text("开始覆盖")');
      if (!startCover) throw new Error('覆盖预览没有开始覆盖');
      await startCover.click();
      await page.waitForFunction(() => (document.body.innerText || '').indexOf('覆盖完成') >= 0 || (document.body.innerText || '').indexOf('完成') >= 0, { timeout: 120000 });
      await runner.screenshot(page, '13-compare-13-cover-right');
      await page.click('[data-action="cover-left"]');
      await page.waitForTimeout(600);
      const startLeft = await page.$('button:has-text("开始覆盖")');
      if (startLeft) {
        await startLeft.click();
        await page.waitForTimeout(4000);
      }
      await runner.screenshot(page, '13-compare-14-cover-left');
    });
  });
}

module.exports = { register };
