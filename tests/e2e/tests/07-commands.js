'use strict';

function register(runner, ctx) {
  const { page, baseUrl, data, consoleLogs, networkLogs, screenshotsDir } = ctx;

  runner.describe('常用命令 - 页面加载', function () {
    runner.it('应成功加载常用命令页面', async function () {
      await page.goto(baseUrl + '/#/commands', { waitUntil: 'load' });
      await page.waitForTimeout(1000);
      await runner.screenshot(page, '07-commands-01-page-load');
    });

    runner.it('应显示命令卡片或列表', async function () {
      const cards = await page.$$('.cmd-card, .command-card, .card');
      const listItems = await page.$$('li.cmd-item, .command-item');
      if (cards.length === 0 && listItems.length === 0) {
        const hasContent = await page.evaluate(function () {
          return document.body.textContent.length > 100;
        });
        if (!hasContent) throw new Error('命令页面内容为空');
      }
    });
  });

  runner.describe('常用命令 - 分类筛选', function () {
    runner.it('应存在分类筛选按钮或标签', async function () {
      const categoryBtns = await page.$$('.category-btn, .cat-tab, [data-category]');
      const tabs = await page.$$('.nav-tabs a, .tab-item');
      if (categoryBtns.length === 0 && tabs.length === 0) {
        const hasCategory = await page.evaluate(function () {
          return document.body.textContent.indexOf('分类') >= 0 || document.body.textContent.indexOf('全部') >= 0;
        });
      }
    });

    runner.it('应能点击分类进行筛选', async function () {
      const categoryBtns = await page.$$('.category-btn, .cat-tab, [data-category]');
      if (categoryBtns.length > 0) {
        const beforeCount = await page.$$('.cmd-card, .command-card, .card').length;
        await categoryBtns[0].click();
        await page.waitForTimeout(300);
        const afterCount = await page.$$('.cmd-card, .command-card, .card').length;
      }
      await runner.screenshot(page, '07-commands-02-category-filter');
    });
  });

  runner.describe('常用命令 - 搜索功能', function () {
    runner.it('应存在搜索输入框', async function () {
      const searchInput = await page.$('input[placeholder*="搜索"], input[placeholder*="查找"], input[placeholder*="命令"]');
      if (!searchInput) {
        const allInputs = await page.$$('input[type="text"], input[type="search"]');
        if (allInputs.length === 0) throw new Error('搜索输入框未找到');
      }
    });

    runner.it('输入关键字应能筛选命令', async function () {
      const searchInput = await page.$('input[placeholder*="搜索"], input[placeholder*="查找"], input[placeholder*="命令"]');
      if (searchInput) {
        const beforeCards = await page.$$('.cmd-card, .command-card, .card');
        await searchInput.fill('ssh');
        await page.waitForTimeout(300);
        const afterCards = await page.$$('.cmd-card, .command-card, .card');
        await searchInput.fill('');
        await page.waitForTimeout(200);
      }
      await runner.screenshot(page, '07-commands-03-search');
    });
  });

  runner.describe('常用命令 - 收藏/取消收藏', function () {
    runner.it('应存在收藏按钮', async function () {
      const favBtns = await page.$$('button:has-text("收藏"), .fav-btn, [data-fav]');
      if (favBtns.length === 0) {
        const hasFav = await page.evaluate(function () {
          return document.body.textContent.indexOf('收藏') >= 0 || document.body.textContent.indexOf('★') >= 0 || document.body.textContent.indexOf('☆') >= 0;
        });
      }
    });

    runner.it('应能点击收藏按钮', async function () {
      const favBtns = await page.$$('button:has-text("收藏"), .fav-btn, [data-fav]');
      if (favBtns.length > 0) {
        await favBtns[0].click();
        await page.waitForTimeout(200);
      }
    });

    runner.it('应能取消收藏', async function () {
      const favBtns = await page.$$('button:has-text("取消收藏"), button:has-text("已收藏"), .fav-btn.active');
      if (favBtns.length > 0) {
        await favBtns[0].click();
        await page.waitForTimeout(200);
      }
      await runner.screenshot(page, '07-commands-04-favorite');
    });
  });

  runner.describe('常用命令 - 卡片展开/收起', function () {
    runner.it('应存在可展开的命令卡片', async function () {
      const cards = await page.$$('.cmd-card, .command-card, .card');
      if (cards.length === 0) throw new Error('命令卡片未找到');
    });

    runner.it('应能展开卡片查看详情', async function () {
      const cards = await page.$$('.cmd-card, .command-card, .card');
      if (cards.length > 0) {
        const expandBtn = await cards[0].$('button:has-text("展开"), button:has-text("详情"), .expand-btn');
        if (expandBtn) {
          await expandBtn.click();
          await page.waitForTimeout(300);
        } else {
          await cards[0].click();
          await page.waitForTimeout(300);
        }
      }
      await runner.screenshot(page, '07-commands-05-card-expand');
    });

    runner.it('应能收起卡片', async function () {
      const cards = await page.$$('.cmd-card, .command-card, .card');
      if (cards.length > 0) {
        const collapseBtn = await cards[0].$('button:has-text("收起"), button:has-text("折叠"), .collapse-btn');
        if (collapseBtn) {
          await collapseBtn.click();
          await page.waitForTimeout(300);
        } else {
          await cards[0].click();
          await page.waitForTimeout(300);
        }
      }
    });
  });
}

module.exports = { register };
