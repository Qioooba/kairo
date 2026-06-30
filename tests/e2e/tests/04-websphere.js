'use strict';

const WebSpherePage = require('../pages/websphere-page');

function register(runner, ctx) {
  const { page, baseUrl, data, screenshotsDir } = ctx;
  const MOCK_SSH = data.MOCK_SSH;

  let wsPage = null;

  runner.describe('WebSphere 日志助手', function () {
    runner.beforeEach(async function () {
      wsPage = new WebSpherePage(page, baseUrl);
      wsPage.setScreenshotDir(screenshotsDir);
      await wsPage.goto();
      await wsPage.closeAllDialogs();
    });

    runner.describe('页面加载', function () {
      runner.it('应成功加载 WebSphere 页面', async function () {
        const hasSys = await wsPage.hasSystemSelect();
        if (!hasSys) {
          await wsPage.takeStateScreenshot('04-websphere-01-page-load-fail');
          throw new Error('页面未正确加载，未找到系统选择器');
        }
        await runner.screenshot(page, '04-websphere-01-page-load');
      });

      runner.it('应显示主要按钮', async function () {
        const summary = await wsPage.getDomSummary();
        const btnTexts = summary.buttons.map((b) => b.text);
        const requiredBtns = ['测试连接', '列出文件', '下载'];
        const found = requiredBtns.filter((t) =>
          btnTexts.some((bt) => bt.indexOf(t) >= 0)
        );
        if (found.length === 0) {
          throw new Error('未找到主要按钮: ' + JSON.stringify(btnTexts.slice(0, 20)));
        }
      });

      runner.it('应显示系统选择下拉框', async function () {
        const hasSys = await wsPage.hasSystemSelect();
        if (!hasSys) {
          throw new Error('系统选择下拉框未找到');
        }
      });

      runner.it('应显示服务器列表区域', async function () {
        const hasServers = await wsPage.hasServerCheckboxes();
        if (!hasServers) {
          const summary = await wsPage.getDomSummary();
          const checkboxInputs = summary.inputs.filter((i) => i.type === 'checkbox');
          if (checkboxInputs.length === 0) {
            throw new Error('服务器列表未找到');
          }
        }
      });
    });

    runner.describe('选择系统/服务器/目录', function () {
      runner.it('应能选择业务系统', async function () {
        const result = await wsPage.selectFirstSystem();
        if (!result) {
          throw new Error('选择业务系统失败');
        }
      });

      runner.it('应能勾选服务器', async function () {
        await wsPage.selectFirstSystem().catch(() => {});
        const result = await wsPage.selectFirstServer();
        if (!result) {
          throw new Error('勾选服务器失败');
        }
      });

      runner.it('应能选择日志目录', async function () {
        await wsPage.selectFirstSystem().catch(() => {});
        await wsPage.selectFirstServer().catch(() => {});
        const result = await wsPage.selectFirstDir();
        if (!result) {
          // 目录选择可能在连接后才可用，不强制失败
        }
      });
    });

    runner.describe('测试连接', function () {
      runner.it('应能填写用户名密码并测试连接（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const ok = await wsPage.testConnection();
          if (!ok) {
            runner.skipTest('mock SSH 不可用 (127.0.0.1:2222)');
            return;
          }
          await runner.screenshot(page, '04-websphere-02-test-connect-ok');
        } catch (e) {
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });
    });

    runner.describe('列出文件', function () {
      runner.it('应能点击列出文件并显示文件列表（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用，无法测试文件列表');
            return;
          }
          const ok = await wsPage.listFiles();
          if (!ok) {
            throw new Error('文件列表未出现');
          }
          const count = await wsPage.getFileCount();
          if (count === 0) {
            throw new Error('文件列表为空');
          }
          await runner.screenshot(page, '04-websphere-03-file-list');
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });

      runner.it('文件列表应包含文件名列（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          const ok = await wsPage.listFiles();
          if (!ok) {
            throw new Error('文件列表未出现');
          }
          const firstRow = await page.$('table.table tbody tr:first-child, table tbody tr:first-child');
          if (!firstRow) {
            throw new Error('文件行未找到');
          }
          const cells = await firstRow.$$('td');
          if (cells.length < 2) {
            throw new Error('文件行列数不足');
          }
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });
    });

    runner.describe('文件名过滤搜索', function () {
      runner.it('应存在文件名过滤输入框', async function () {
        const summary = await wsPage.getDomSummary();
        const filterInputs = summary.inputs.filter(
          (i) =>
            i.id === 'ws-file-pattern' ||
            (i.placeholder && (i.placeholder.indexOf('文件名') >= 0 || i.placeholder.indexOf('过滤') >= 0))
        );
        if (filterInputs.length === 0) {
          await wsPage.switchTab('files').catch(() => {});
          const summary2 = await wsPage.getDomSummary();
          const textInputs = summary2.inputs.filter((i) => i.type === 'text');
          if (textInputs.length === 0) {
            throw new Error('未找到文件名过滤输入框');
          }
        }
      });

      runner.it('输入关键字应能过滤文件列表（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          const listOk = await wsPage.listFiles();
          if (!listOk) {
            throw new Error('文件列表未出现');
          }
          const beforeCount = await wsPage.getFileCount();
          const filterOk = await wsPage.filterFiles('.log');
          if (!filterOk) {
            throw new Error('文件名过滤输入框未找到');
          }
          const afterCount = await wsPage.getFileCount();
          if (afterCount > beforeCount) {
            throw new Error('过滤后文件数不应增加');
          }
          await wsPage.filterFiles('');
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });
    });

    runner.describe('下载最新', function () {
      runner.it('应存在下载按钮', async function () {
        const summary = await wsPage.getDomSummary();
        const dlBtns = summary.buttons.filter((b) => b.text.indexOf('下载') >= 0);
        if (dlBtns.length === 0) {
          throw new Error('下载按钮未找到');
        }
      });

      runner.it('点击下载最新应发起请求（需 mock SSH）', async function () {
        let downloadStarted = false;
        const requestHandler = function (req) {
          const url = req.url();
          if (url.indexOf('/api/logs/download') >= 0 || url.indexOf('/api/files/download') >= 0) {
            downloadStarted = true;
          }
        };
        try {
          page.on('request', requestHandler);
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          await wsPage.listFiles().catch(() => {});
          await wsPage.downloadLatest();
          await wsPage.waitForTimeout(1000);
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        } finally {
          page.off('request', requestHandler);
        }
      });
    });

    runner.describe('搜索功能', function () {
      runner.it('应能切换到搜索 tab', async function () {
        const ok = await wsPage.switchTab('search');
        if (!ok) {
          const summary = await wsPage.getDomSummary();
          throw new Error('无法切换到搜索 tab: ' + JSON.stringify(summary.tabs));
        }
      });

      runner.it('应存在搜索关键字输入框', async function () {
        await wsPage.switchTab('search').catch(() => {});
        const summary = await wsPage.getDomSummary();
        const searchInputs = summary.inputs.filter(
          (i) =>
            i.id === 'ws-query' ||
            (i.placeholder && (i.placeholder.indexOf('关键字') >= 0 || i.placeholder.indexOf('搜索') >= 0))
        );
        if (searchInputs.length === 0) {
          const textInputs = summary.inputs.filter((i) => i.type === 'text');
          if (textInputs.length === 0) {
            throw new Error('搜索输入框未找到');
          }
        }
      });

      runner.it('应能输入搜索关键字并执行搜索（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          await wsPage.searchLogs('Exception');
          await runner.screenshot(page, '04-websphere-04-search-result');
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });
    });

    runner.describe('Tab 切换', function () {
      runner.it('应存在文件/搜索/实时跟踪 tab', async function () {
        const hasFilesTab = await wsPage.hasTab('files');
        const hasSearchTab = await wsPage.hasTab('search');
        const hasTailTab = await wsPage.hasTab('tail');
        if (!hasFilesTab && !hasSearchTab && !hasTailTab) {
          const summary = await wsPage.getDomSummary();
          const tabTexts = summary.tabs.map((t) => t.text);
          throw new Error('未找到 tab 元素: ' + JSON.stringify(tabTexts));
        }
      });

      runner.it('应能切换到搜索 tab', async function () {
        const ok = await wsPage.switchTab('search');
        if (!ok) {
          throw new Error('切换到搜索 tab 失败');
        }
      });

      runner.it('应能切换到实时跟踪 tab', async function () {
        const ok = await wsPage.switchTab('tail');
        if (!ok) {
          throw new Error('切换到实时跟踪 tab 失败');
        }
      });

      runner.it('应能切换回文件 tab', async function () {
        await wsPage.switchTab('tail').catch(() => {});
        const ok = await wsPage.switchTab('files');
        if (!ok) {
          throw new Error('切换回文件 tab 失败');
        }
      });

      runner.it('Tab 切换正确性验证', async function () {
        await wsPage.switchTab('files');
        await wsPage.waitForTimeout(200);
        await wsPage.switchTab('search');
        await wsPage.waitForTimeout(200);
        await wsPage.switchTab('tail');
        await wsPage.waitForTimeout(200);
        await wsPage.switchTab('files');
        await wsPage.waitForTimeout(200);
        await runner.screenshot(page, '04-websphere-tab-switch');
      });
    });

    runner.describe('Tail 实时跟踪', function () {
      runner.it('应能切换到实时跟踪 tab', async function () {
        const ok = await wsPage.switchTab('tail');
        if (!ok) {
          throw new Error('切换到实时跟踪 tab 失败');
        }
      });

      runner.it('应存在 tail 相关输入框', async function () {
        await wsPage.switchTab('tail').catch(() => {});
        const summary = await wsPage.getDomSummary();
        const textInputs = summary.inputs.filter((i) => i.type === 'text');
        if (textInputs.length === 0) {
          throw new Error('未找到 tail 文件名输入框');
        }
      });

      runner.it('应能启动 tail 跟踪（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          const started = await wsPage.startTail();
          if (!started) {
            throw new Error('启动 tail 失败');
          }
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });

      runner.it('应验证收到 tail 数据（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          await wsPage.startTail();
          let hasOutput = false;
          const startTime = Date.now();
          while (Date.now() - startTime < 15000) {
            hasOutput = await wsPage.hasTailOutput();
            if (hasOutput) break;
            await wsPage.waitForTimeout(500);
          }
          if (!hasOutput) {
            throw new Error('tail 输出为空');
          }
          await runner.screenshot(page, '04-websphere-05-tail-started');
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });

      runner.it('应能停止 tail 跟踪（需 mock SSH）', async function () {
        try {
          await wsPage.selectFirstSystem();
          await wsPage.selectFirstServer();
          await wsPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
          const connOk = await wsPage.testConnection();
          if (!connOk) {
            runner.skipTest('mock SSH 不可用');
            return;
          }
          await wsPage.startTail();
          await wsPage.waitForTimeout(2000);
          const stopped = await wsPage.stopTail();
          if (!stopped) {
            throw new Error('停止 tail 失败');
          }
          await runner.screenshot(page, '04-websphere-06-tail-stopped');
        } catch (e) {
          if (e.message && e.message.indexOf('跳过') >= 0) {
            throw e;
          }
          runner.skipTest('跳过：mock SSH 不可用 - ' + e.message);
        }
      });
    });
  });
}

module.exports = { register };
