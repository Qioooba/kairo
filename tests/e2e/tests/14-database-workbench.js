'use strict';

/**
 * Database workbench: inspector tabs, explain, query history, grid/record.
 * Live Oracle clicks run when a source is already configured.
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('数据库工作台', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/database', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
    });

    runner.it('页面提供数据源、测试连接和管理入口', async function () {
      const source = await page.$('#db-source, select');
      const testBtn = await page.$('button:has-text("测试连接"), #db-test');
      const manageBtn = await page.$('button:has-text("管理"), #db-manage');
      if (!source && !testBtn && !manageBtn) {
        throw new Error('数据库工作台未渲染数据源工具栏');
      }
      await runner.screenshot(page, '14-database-01-load');
    });

    runner.it('管理面板可展开 Oracle 表单', async function () {
      const manage = await page.$('#db-manage, button:has-text("管理数据源"), button:has-text("数据源")');
      if (manage) {
        await manage.click();
        await page.waitForTimeout(400);
      }
      const kind = await page.$('#dbf-kind');
      if (kind) {
        const options = await page.$$eval('#dbf-kind option', els => els.map(e => e.value));
        if (!options.includes('oracle')) throw new Error('缺少 Oracle 类型');
      }
      await runner.screenshot(page, '14-database-02-manager');
    });

    runner.it('已配置数据源时可查询、查看对象和执行计划', async function () {
      const hasWorkspace = await page.$('#db-sql, .db-sql-editor');
      if (!hasWorkspace) {
        await runner.screenshot(page, '14-database-03-no-source');
        return;
      }
      const inspectTabs = await page.$$('.db-inspect-tab');
      if (inspectTabs.length < 4) throw new Error('缺少字段/索引/约束/DDL 页签');
      await inspectTabs[0].click();
      await page.waitForTimeout(200);
      await inspectTabs[1].click();
      await page.waitForTimeout(200);
      await inspectTabs[2].click();
      await page.waitForTimeout(200);
      await inspectTabs[3].click();
      await page.waitForTimeout(200);

      const sql = await page.$('#db-sql');
      if (sql) {
        await sql.fill("SELECT '中文验证' AS text_value, CAST(NULL AS VARCHAR2(10)) AS empty_value FROM DUAL");
      }
      const run = await page.$('#db-run');
      if (run) {
        await run.click();
        await page.waitForTimeout(2500);
      }
      const record = await page.$('#db-view-record');
      if (record) {
        await record.click();
        await page.waitForTimeout(400);
      }
      const grid = await page.$('#db-view-grid');
      if (grid) await grid.click();
      const explain = await page.$('#db-explain');
      if (explain) {
        await explain.click();
        await page.waitForTimeout(2000);
        const plan = await page.$('#db-view-plan');
        if (plan) await plan.click();
      }
      await runner.screenshot(page, '14-database-04-query-plan');
    });
  });
}

module.exports = { register };
