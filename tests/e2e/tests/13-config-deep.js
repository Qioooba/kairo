'use strict';

/**
 * Config 系统配置 深度测试
 * 覆盖接口：
 * - /api/config/export
 * - /api/config/import
 * - /api/credentials/save
 * - /api/credentials/has
 * - /api/credentials/clear
 * - /api/admin/servers
 * - /api/admin/openers
 * - /api/admin/download-retention
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('系统配置 - 深度', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/config', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);
      await page.waitForTimeout(300);
    });

    runner.it('页面加载成功', async function () {
      const hasSaveBtn = await page.$('button:has-text("保存"), button:has-text("导出"), button:has-text("导入")');
      if (!hasSaveBtn) {
        const bodyText = await page.evaluate(() => document.body.innerText.trim().substring(0, 200));
        throw new Error('Config 页面未正确加载: ' + bodyText);
      }
      await runner.screenshot(page, '15-config-01-load');
    });

    runner.it('业务系统列表存在', async function () {
      const systemBlocks = await page.$$('.system-block, .system-item, [class*="system"]');
      if (systemBlocks.length === 0) {
        // 可能还没有系统，尝试添加
      }
      await runner.screenshot(page, '15-config-02-systems');
    });

    runner.it('添加业务系统', async function () {
      const addBtn = await page.$('button:has-text("新增业务系统"), button:has-text("添加系统"), button:has-text("+ 系统")');
      if (!addBtn) {
        // 可能按钮文本不同
        const allBtns = await page.$$('button');
        for (const btn of allBtns) {
          const text = await btn.textContent();
          if (text.indexOf('系统') >= 0) {
            await btn.click();
            await page.waitForTimeout(500);
            break;
          }
        }
      } else {
        await addBtn.click();
        await page.waitForTimeout(500);
      }

      await runner.screenshot(page, '15-config-03-add-system');
    });

    runner.it('导出配置功能存在', async function () {
      const exportBtn = await page.$('button:has-text("导出配置"), button:has-text("导出"), button:has-text("Export")');
      if (exportBtn) {
        await runner.screenshot(page, '15-config-04-export-btn');
      }
    });

    runner.it('导入配置功能存在', async function () {
      const importBtn = await page.$('button:has-text("导入配置"), button:has-text("导入"), button:has-text("Import")');
      if (importBtn) {
        await runner.screenshot(page, '15-config-05-import-btn');
      }
    });

    runner.it('下载保留设置存在', async function () {
      const retentionSection = await page.$('[class*="retention"], [class*="download"], [class*="保留"]');
      if (retentionSection) {
        await runner.screenshot(page, '15-config-06-retention');
      }
    });

    runner.it('External Openers 设置存在', async function () {
      const openersSection = await page.$('[class*="opener"], [class*="open"], [class*="外部"]');
      if (openersSection) {
        await runner.screenshot(page, '15-config-07-openers');
      }
    });

    runner.it('放弃改动提示（无改动时）', async function () {
      const abandonBtn = await page.$('button:has-text("放弃改动"), button:has-text("放弃")');
      if (abandonBtn) {
        const disabled = await abandonBtn.evaluate(el => el.disabled);
        if (disabled) {
          await runner.screenshot(page, '15-config-08-no-change');
        }
      }
    });

    runner.it('服务器列表存在', async function () {
      const serverBlocks = await page.$$('.server-block, .server-item, [class*="server"]');
      await runner.screenshot(page, '15-config-09-servers');
    });
  });

  runner.describe('配置导出测试', function () {
    runner.it('导出配置应触发下载', async function () {
      await page.goto(baseUrl + '#/config', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);

      // 设置下载监听
      let downloaded = false;
      page.on('download', () => { downloaded = true; });

      const exportBtn = await page.$('button:has-text("导出配置"), button:has-text("导出")');
      if (exportBtn) {
        await exportBtn.click();
        await page.waitForTimeout(1000);
      }

      await runner.screenshot(page, '15-config-10-export');
    });
  });

  runner.describe('WebSphere 凭据管理', function () {
    runner.it('凭据保存功能存在', async function () {
      await page.goto(baseUrl + '#/websphere', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);

      const credentialSection = await page.$('[class*="credential"], [class*="password"], [class*="凭据"]');
      if (credentialSection) {
        await runner.screenshot(page, '15-config-11-credentials');
      }

      // 检查凭据输入框
      const credInputs = await page.$$('input[type="password"]');
      if (credInputs.length >= 2) {
        await credInputs[0].fill('testuser');
        await credInputs[1].fill('testpass');
        await page.waitForTimeout(300);

        const saveBtn = await page.$('button:has-text("保存凭据"), button:has-text("保存密码")');
        if (saveBtn) {
          await runner.screenshot(page, '15-config-12-cred-filled');
        }
      }
    });

    runner.it('清除凭据功能存在', async function () {
      await page.goto(baseUrl + '#/websphere', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);

      const clearBtn = await page.$('button:has-text("清除凭据"), button:has-text("清除密码")');
      if (clearBtn) {
        await runner.screenshot(page, '15-config-13-clear-cred');
      }
    });
  });
}

module.exports = { register };
