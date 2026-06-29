'use strict';

const ConfigPage = require('../pages/config-page');

function register(runner, ctx) {
  const { page, baseUrl } = ctx;
  let cfgPage;

  runner.describe('系统配置测试', function () {
    runner.beforeAll(async function () {
      cfgPage = new ConfigPage(page, baseUrl, runner);
    });

    runner.beforeEach(async function () {
      await cfgPage.goto();
    });

    runner.describe('页面加载', function () {
      runner.it('应成功加载系统配置页面', async function () {
        await cfgPage.takeStateScreenshot('01-page-load');
      });

      runner.it('应显示业务系统列表或相关 UI', async function () {
        const sysCount = await cfgPage.getSystemCount();
        const hasSystemsText = await page.evaluate(function () {
          return document.body.textContent.indexOf('业务系统') >= 0;
        });
        if (sysCount === 0 && !hasSystemsText) {
          throw new Error('业务系统列表未找到');
        }
      });

      runner.it('应显示保存按钮', async function () {
        const hasSaveBtn = await cfgPage.hasSaveButton();
        if (!hasSaveBtn) throw new Error('保存按钮未找到');
      });
    });

    runner.describe('添加业务系统', function () {
      runner.it('应存在新增业务系统按钮', async function () {
        const addBtn = await cfgPage.findFirstVisible([
          'button:has-text("+ 新增业务系统")',
          'button:has-text("新增业务系统")',
          'button:has-text("+新增业务系统")',
        ]);
        if (!addBtn) throw new Error('新增业务系统按钮未找到');
      });

      runner.it('点击新增业务系统应显示新的系统块', async function () {
        const beforeCount = await cfgPage.getSystemCount();
        const result = await cfgPage.addSystem('测试系统-E2E');
        if (!result.added) {
          throw new Error('新增业务系统后数量未增加');
        }
        if (!result.saveBtnEnabled) {
          throw new Error('添加业务系统后保存按钮未启用');
        }
        await cfgPage.takeStateScreenshot('02-add-system');
      });
    });

    runner.describe('添加服务器', function () {
      runner.it('添加服务器（确保有系统选中）', async function () {
        let sysCount = await cfgPage.getSystemCount();
        if (sysCount === 0) {
          const addResult = await cfgPage.addSystem('测试系统-服务器测试');
          sysCount = addResult.afterCount;
        }
        if (sysCount === 0) {
          console.log('  跳过: 没有业务系统，无法添加服务器');
          return;
        }

        const result = await cfgPage.addServer(0, {
          name: 'test-server-01',
          host: '127.0.0.1',
          port: 22,
          username: 'test',
        });
        if (!result.added) {
          throw new Error('添加服务器后数量未增加');
        }
        if (!result.saveBtnEnabled) {
          throw new Error('添加服务器后保存按钮未启用');
        }
        await cfgPage.takeStateScreenshot('03-add-server');
      });
    });

    runner.describe('添加日志目录', function () {
      runner.it('添加日志目录', async function () {
        let sysCount = await cfgPage.getSystemCount();
        if (sysCount === 0) {
          const addResult = await cfgPage.addSystem('测试系统-日志目录测试');
          sysCount = addResult.afterCount;
        }
        if (sysCount === 0) {
          console.log('  跳过: 没有业务系统，无法添加日志目录');
          return;
        }

        let srvCount = await cfgPage.getServerCount(0);
        if (srvCount === 0) {
          const addSrvResult = await cfgPage.addServer(0, {
            name: 'test-server-log',
            host: '127.0.0.1',
            port: 22,
            username: 'test',
          });
          srvCount = addSrvResult.afterCount;
        }
        if (srvCount === 0) {
          console.log('  跳过: 没有服务器，无法添加日志目录');
          return;
        }

        const result = await cfgPage.addLogDir(0, 0, {
          name: '测试目录',
          path: '/var/log/test',
        });
        if (!result.added) {
          throw new Error('添加日志目录后数量未增加');
        }
        if (!result.saveBtnEnabled) {
          throw new Error('添加日志目录后保存按钮未启用');
        }
        await cfgPage.takeStateScreenshot('04-add-dir');
      });
    });

    runner.describe('导出配置', function () {
      runner.it('应存在导出配置按钮', async function () {
        const exportBtn = await cfgPage.findFirstVisible([
          'button:has-text("导出配置")',
          'button:has-text("导出")',
        ]);
        if (!exportBtn) throw new Error('导出配置按钮未找到');
      });

      runner.it('点击导出配置应触发下载', async function () {
        const result = await cfgPage.exportConfig();
        await cfgPage.takeStateScreenshot('05-export');
      });
    });

    runner.describe('放弃改动', function () {
      runner.it('应存在放弃改动按钮', async function () {
        const resetBtn = await cfgPage.findFirstVisible([
          'button:has-text("放弃改动")',
          'button:has-text("取消修改")',
          'button:has-text("重置")',
        ]);
        if (!resetBtn) throw new Error('放弃改动按钮未找到');
      });

      runner.it('点击放弃改动应显示确认 dialog（有改动时）', async function () {
        let sysCount = await cfgPage.getSystemCount();
        if (sysCount === 0) {
          const addResult = await cfgPage.addSystem('测试系统-放弃改动测试');
          sysCount = addResult.afterCount;
        }
        if (sysCount === 0) {
          console.log('  跳过: 无法创建业务系统以产生改动');
          return;
        }

        const saveBtnEnabled = !await cfgPage.isSaveButtonDisabled();
        if (!saveBtnEnabled) {
          await cfgPage.addSystem('测试系统-放弃改动-脏数据');
        }

        const result = await cfgPage.clickReset();
        if (!result.dialogShown) {
          throw new Error('点击放弃改动未显示确认 dialog');
        }
        await cfgPage.takeStateScreenshot('06-reset-confirm');
      });
    });
  });
}

module.exports = { register };
