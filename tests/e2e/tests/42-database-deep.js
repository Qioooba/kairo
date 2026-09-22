'use strict';
const assert = require('assert');

/**
 * 42-database-deep.js
 * 真实数据库深度功能与 SQL 生成完整性校验 (QA-01)
 *
 * 验证:
 * 1. 真实 Oracle 数据源可用性探测 (无数据源时明确 SKIP);
 * 2. 宽表查询、列选中、独立排序角标、顶部与底部双向联动滚动;
 * 3. 右键复制为 INSERT / UPDATE 语句的完整性校验:
 *    - UPDATE 语句必须非空且以 'UPDATE ' 开头;
 *    - 表名无库名前缀、无双引号;
 *    - SET 字段无双引号;
 *    - WHERE 必须包含有效的主键条件，且无双引号。
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('真实数据库深度功能与语句导出', function () {
    runner.it('检测 Oracle 数据源并执行深度网格及 UPDATE/INSERT 完整性校验', async function () {
      await page.goto(baseUrl + '/#/database', { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('#db-source', { timeout: 15000 });

      // 探测可用数据源
      const opts = await page.$$eval('#db-source option', els => els.map(e => ({ v: e.value, t: e.textContent })));
      const labSource = opts.find(o => o.t && o.t.indexOf('KAIRO_LAB') >= 0);

      if (!labSource) {
        console.log('未检测到 KAIRO_LAB 数据源配置，跳过 Oracle 深度实测');
        runner.skipTest('未检测到 KAIRO_LAB 数据源，已按环境能力明确跳过');
        return;
      }

      // 1. 选中真实数据源并测试连接
      await page.selectOption('#db-source', labSource.v);
      await page.waitForTimeout(600);

      await page.click('#db-test');
      await page.waitForFunction(() => {
        const b = document.getElementById('db-test');
        return b && !b.disabled && b.textContent === '测试连接';
      }, null, { timeout: 15000 });

      // 2. 执行宽表全字段查询
      await page.fill('#db-sql', 'SELECT * FROM kairo_lab.employees WHERE ROWNUM <= 25');
      await page.click('#db-run');
      await page.waitForFunction(() => {
        const tbody = document.getElementById('db-result-body');
        return tbody && tbody.querySelectorAll('tr[data-row]').length > 0;
      }, null, { timeout: 20000 });

      // 3. 字段名点击选中整列
      const colNameBtn = await page.$('.db-col-name[data-col="1"]');
      assert.ok(colNameBtn, '缺少第 1 列的 .db-col-name 按钮');
      await colNameBtn.click();
      await page.waitForTimeout(300);

      const thSelected = await page.$eval('th[data-col="1"]', el => el.classList.contains('col-selected'));
      const tdSelectedCount = await page.$$eval('td[data-col="1"].col-selected', els => els.length);
      assert.ok(thSelected && tdSelectedCount > 0, '字段名点击后未能成功选中整列');

      // 4. 独立排序角标
      const sortBtn = await page.$('.db-col-sort-btn[data-sort="1"]');
      assert.ok(sortBtn, '缺少排序角标 .db-col-sort-btn');
      await sortBtn.click();
      await page.waitForTimeout(400);

      const ascBadge = await page.$eval('.db-col-sort-btn[data-sort="1"]', el => el.innerText);
      assert.strictEqual(ascBadge, '▲', '点击排序角标后应切换为升序 ▲');

      // 5. 顶部左右滚动条双向联动
      const topScroll = await page.$('.db-table-top-scroll');
      const tableScroll = await page.$('.db-table-scroll');
      assert.ok(topScroll && tableScroll, '缺少表格滚动条元素');

      await topScroll.evaluate(el => { el.scrollLeft = 200; el.dispatchEvent(new Event('scroll')); });
      await page.waitForTimeout(200);
      const tableScrollLeft = await tableScroll.evaluate(el => el.scrollLeft);
      assert.ok(Math.abs(tableScrollLeft - 200) < 10, '顶部滚动条未同步至下方网格');

      // 6. 右键菜单: 复制为 INSERT 语句
      const firstCell = await page.$('td[data-row="0"][data-col="1"]');
      assert.ok(firstCell, '未找到首行数据单元格');
      await firstCell.click({ button: 'right' });
      await page.waitForSelector('.db-popup-menu', { timeout: 5000 });
      await page.click('.db-popup-menu >> text=复制为 INSERT 语句');
      await page.waitForTimeout(400);

      const insertSQL = await page.evaluate(async () => navigator.clipboard.readText());
      assert.ok(insertSQL && insertSQL.startsWith('INSERT INTO '), '复制的 INSERT 语句不合法: ' + insertSQL);
      assert.strictEqual(insertSQL.split(/VALUES/i)[0].includes('"'), false, 'INSERT 语句表头不应包含双引号');

      // 7. 右键菜单: 复制整行为 UPDATE 语句
      await page.evaluate(() => navigator.clipboard.writeText(''));
      await firstCell.click({ button: 'right' });
      await page.waitForSelector('.db-popup-menu', { timeout: 5000 });
      await page.click('.db-popup-menu >> text=复制整行为 UPDATE 语句');

      let updateSQL = '';
      for (let i = 0; i < 20; i++) {
        await page.waitForTimeout(400);
        updateSQL = await page.evaluate(() => navigator.clipboard.readText());
        if (updateSQL && updateSQL.trim().startsWith('UPDATE')) break;
      }

      // 断言非空且包含有效 UPDATE
      assert.ok(
        updateSQL && updateSQL.trim().startsWith('UPDATE '),
        '剪贴板未获取到有效 UPDATE 语句，内容为空或未写入成功'
      );

      // 验证表名无前缀、无引号
      const updateTarget = updateSQL.split(/SET/i)[0];
      assert.strictEqual(updateTarget.includes('"'), false, 'UPDATE 表名不应包含双引号: ' + updateTarget);
      assert.strictEqual(/UPDATE\s+[^.\s]+\./i.test(updateTarget), false, 'UPDATE 表名不应包含 schema 库名前缀');

      // 验证 SET 字段
      const setPart = updateSQL.replace(/^UPDATE\s+\w+\s+SET\s+/i, '').split(/\s+WHERE\s+/i)[0];
      const assignments = setPart.split(/,(?=(?:[^']*'[^']*')*[^']*$)/);
      assert.ok(assignments.length > 0 && assignments[0].trim(), 'UPDATE SET 字段列表不能为空');
      for (const assign of assignments) {
        const col = assign.split('=')[0].trim();
        assert.ok(col && !col.includes('"'), `SET 字段名 [${col}] 包含双引号或为空`);
      }

      // 验证 WHERE 主键条件存在且无双引号
      const wherePart = updateSQL.split(/\s+WHERE\s+/i)[1];
      assert.ok(wherePart && wherePart.trim(), 'UPDATE 语句必须包含 WHERE 主键过滤条件');
      const conditions = wherePart.replace(/;$/, '').split(/\s+AND\s+/i);
      for (const cond of conditions) {
        const col = cond.split('=')[0].trim();
        assert.ok(col && !col.includes('"'), `WHERE 条件字段 [${col}] 包含双引号或为空`);
      }
    });
  });
}

module.exports = { register };
