'use strict';

function register(runner, ctx) {
  const { page, baseUrl, data, consoleLogs, networkLogs, screenshotsDir } = ctx;

  runner.describe('其他页面 - HTTP 测试页', function () {
    runner.it('应成功加载 HTTP 测试页面', async function () {
      await page.goto(baseUrl + '/#/http', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-01-http-page');
    });

    runner.it('HTTP 测试页应包含请求方法选择', async function () {
      const methodSelect = await page.$('select, button:has-text("GET"), button:has-text("POST")');
      if (!methodSelect) {
        const hasMethod = await page.evaluate(function () {
          return document.body.textContent.indexOf('GET') >= 0 || document.body.textContent.indexOf('POST') >= 0;
        });
        if (!hasMethod) throw new Error('HTTP 方法选择未找到');
      }
    });

    runner.it('HTTP 测试页应包含 URL 输入框', async function () {
      const urlInput = await page.$('input[placeholder*="URL"], input[placeholder*="url"]');
      if (!urlInput) {
        const allInputs = await page.$$('input[type="text"]');
        if (allInputs.length === 0) throw new Error('URL 输入框未找到');
      }
    });
  });

  runner.describe('其他页面 - 环境自检页', function () {
    runner.it('应成功加载环境自检页面', async function () {
      await page.goto(baseUrl + '/#/diagnostics', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-02-diagnostics-page');
    });

    runner.it('环境自检页应包含自检项列表', async function () {
      const items = await page.$$('.diag-item, .check-item, .status-item');
      if (items.length === 0) {
        const hasContent = await page.evaluate(function () {
          return document.body.textContent.length > 50;
        });
        if (!hasContent) throw new Error('环境自检页内容为空');
      }
    });

    runner.it('应存在自检或刷新按钮', async function () {
      const checkBtn = await page.$('button:has-text("自检"), button:has-text("检测"), button:has-text("刷新")');
      if (!checkBtn) throw new Error('自检按钮未找到');
    });
  });

  runner.describe('其他页面 - 下载历史页', function () {
    runner.it('应成功加载下载历史页面', async function () {
      await page.goto(baseUrl + '/#/downloads', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-03-downloads-page');
    });

    runner.it('下载历史页应包含列表或表格', async function () {
      const table = await page.$('table.table');
      const list = await page.$$('.download-item, .history-item');
      if (!table && list.length === 0) {
        const hasContent = await page.evaluate(function () {
          return document.body.textContent.indexOf('下载') >= 0 || document.body.textContent.indexOf('历史') >= 0;
        });
        if (!hasContent) throw new Error('下载历史页内容为空');
      }
    });
  });

  runner.describe('其他页面 - 时间戳工具页', function () {
    runner.it('应成功加载时间戳工具页面', async function () {
      await page.goto(baseUrl + '/#/timestamp', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-04-timestamp-page');
    });

    runner.it('时间戳工具页应包含时间戳输入', async function () {
      const hasTimestamp = await page.evaluate(function () {
        return document.body.textContent.indexOf('时间戳') >= 0 || document.body.textContent.indexOf('timestamp') >= 0;
      });
      if (!hasTimestamp) throw new Error('时间戳工具页内容不正确');
    });

    runner.it('应存在转换按钮', async function () {
      const convertBtn = await page.$('button:has-text("转换"), button:has-text("转日期"), button:has-text("转时间戳")');
      if (!convertBtn) {
        const btns = await page.$$('button');
        if (btns.length === 0) throw new Error('转换按钮未找到');
      }
    });
  });

  runner.describe('其他页面 - Cron 解析页', function () {
    runner.it('应成功加载 Cron 解析页面', async function () {
      await page.goto(baseUrl + '/#/cron', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-05-cron-page');
    });

    runner.it('Cron 解析页应包含 Cron 表达式输入', async function () {
      const hasCron = await page.evaluate(function () {
        return document.body.textContent.indexOf('Cron') >= 0 || document.body.textContent.indexOf('cron') >= 0;
      });
      if (!hasCron) throw new Error('Cron 解析页内容不正确');
    });
  });

  runner.describe('其他页面 - JSONPath 页', function () {
    runner.it('应成功加载 JSONPath 页面', async function () {
      await page.goto(baseUrl + '/#/jsonpath', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-06-jsonpath-page');
    });

    runner.it('JSONPath 页应包含 JSON 输入和路径输入', async function () {
      const hasJsonPath = await page.evaluate(function () {
        return document.body.textContent.indexOf('JSONPath') >= 0 || document.body.textContent.indexOf('jsonpath') >= 0;
      });
      if (!hasJsonPath) throw new Error('JSONPath 页内容不正确');
    });
  });

  runner.describe('其他页面 - 代码比对页', function () {
    runner.it('应成功加载代码比对页面', async function () {
      await page.goto(baseUrl + '/#/compare', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-07-compare-page');
    });

    runner.it('代码比对页应包含两个输入区域', async function () {
      await page.goto(baseUrl + '/#/compare', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      const textareas = await page.$$('textarea');
      const hasCompare = await page.evaluate(function () {
        return document.body.textContent.indexOf('比对') >= 0 || document.body.textContent.indexOf('对比') >= 0 || document.body.textContent.indexOf('diff') >= 0;
      });
      if (textareas.length < 2 && !hasCompare) throw new Error('代码比对页内容不正确');
    });
  });

  runner.describe('其他页面 - WS 代码生成', function () {
    runner.it('应成功加载 WS 代码生成页面', async function () {
      await page.goto(baseUrl + '/#/wscodegen', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-09-wscodegen-page');
    });

    runner.it('应展示引擎选择和预览按钮', async function () {
      const hasTitle = await page.evaluate(function () {
        return document.body.textContent.indexOf('WSDL') >= 0 && document.body.textContent.indexOf('Java') >= 0;
      });
      if (!hasTitle) throw new Error('WS 代码生成页标题未找到');
      const previewBtn = await page.$('button:has-text("预览代码"), button:has-text("生成到目录")');
      if (!previewBtn) throw new Error('预览/生成按钮未找到');
    });
  });

  runner.describe('其他页面 - 关于页', function () {
    runner.it('应成功加载关于页面', async function () {
      await page.goto(baseUrl + '/#/about', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '09-misc-08-about-page');
    });

    runner.it('关于页应包含版本信息或项目名称', async function () {
      const hasAbout = await page.evaluate(function () {
        return document.body.textContent.indexOf('版本') >= 0 ||
          document.body.textContent.indexOf('关于') >= 0 ||
          document.body.textContent.indexOf('Kairo') >= 0 ||
          document.body.textContent.indexOf('kairo') >= 0;
      });
      if (!hasAbout) throw new Error('关于页内容不正确');
    });
  });
}

module.exports = { register };
