'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT_DIR = path.resolve(__dirname, '../../test-results/screenshots/database-deep');
fs.mkdirSync(SHOT_DIR, { recursive: true });

async function run() {
  console.log('--- 启动真实浏览器端到端深度测试 ---');
  console.log('目标服务:', BASE);

  // 尝试启动 msedge 或 chromium
  let browser;
  try {
    browser = await chromium.launch({ headless: true, channel: 'msedge' });
    console.log('使用系统已安装的 Microsoft Edge 浏览器');
  } catch (_) {
    browser = await chromium.launch({ headless: true });
    console.log('使用 Chromium 浏览器');
  }

  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write']
  });
  const page = await context.newPage();
  page.setDefaultTimeout(25000);

  // 捕获控制台日志
  page.on('console', msg => console.log('[Browser Console]', msg.type(), msg.text()));
  page.on('pageerror', err => console.log('[Browser Page Error]', err));

  try {
    // 1. 打开数据库工作台
    console.log('1. 正在访问数据库工作台...');
    await page.goto(BASE + '/#/database', { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#db-source', { timeout: 15000 });

    // 2. 选择真实数据库数据源 (KAIRO_LAB Oracle 21c)
    console.log('2. 正在选择真实 Oracle 数据源...');
    const opts = await page.$$eval('#db-source option', els => els.map(e => ({ v: e.value, t: e.textContent })));
    console.log('可用数据源:', opts.map(o => o.t).join(', '));
    const labSource = opts.find(o => o.t && o.t.indexOf('KAIRO_LAB') >= 0);
    if (!labSource) throw new Error('未找到 KAIRO_LAB 数据源: ' + JSON.stringify(opts));
    await page.selectOption('#db-source', labSource.v);
    await page.waitForTimeout(1000);

    // 测试连接
    console.log('正在执行测试连接...');
    await page.click('#db-test');
    await page.waitForFunction(() => {
      const b = document.getElementById('db-test');
      return b && !b.disabled && b.textContent === '测试连接';
    }, null, { timeout: 15000 });
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(SHOT_DIR, '01-db-test-connection.png') });
    console.log('测试连接完成，已截图: 01-db-test-connection.png');

    // 3. 执行单表宽表真实查询
    console.log('3. 执行单表全字段查询: SELECT * FROM kairo_lab.employees WHERE ROWNUM <= 25...');
    await page.fill('#db-sql', 'SELECT * FROM kairo_lab.employees WHERE ROWNUM <= 25');
    await page.click('#db-run');
    await page.waitForFunction(() => {
      const tbody = document.getElementById('db-result-body');
      return tbody && tbody.querySelectorAll('tr[data-row]').length > 0;
    }, null, { timeout: 20000 });
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(SHOT_DIR, '02-query-result.png') });
    console.log('查询执行成功并已渲染网格，已截图: 02-query-result.png');

    // 4. 测试功能点 A：字段名点击选中整列
    console.log('4. 测试功能点 A: 字段名点击选中整列...');
    const colNameBtn = await page.$('.db-col-name[data-col="1"]');
    if (!colNameBtn) throw new Error('未找到第 1 列的 .db-col-name 按钮');
    const colNameText = await colNameBtn.innerText();
    console.log('点击字段名:', colNameText);
    await colNameBtn.click();
    await page.waitForTimeout(300);

    // 验证 th 和 td 是否有 col-selected
    const thSelected = await page.$eval('th[data-col="1"]', el => el.classList.contains('col-selected'));
    const tdSelectedCount = await page.$$eval('td[data-col="1"].col-selected', els => els.length);
    console.log(`验证列选中: th.col-selected=${thSelected}, td.col-selected 数量=${tdSelectedCount}`);
    if (!thSelected || tdSelectedCount === 0) {
      throw new Error(`字段名点击后未能成功选中整列! thSelected=${thSelected}, tdSelectedCount=${tdSelectedCount}`);
    }
    await page.screenshot({ path: path.join(SHOT_DIR, '03-column-selected.png') });
    console.log('列选中测试通过，已截图: 03-column-selected.png');

    // 点击某行单元格，验证列选中是否取消、行选中是否正常
    console.log('点击数据行，测试列选中取消与行选中...');
    await page.click('td[data-row="2"][data-col="0"]');
    await page.waitForTimeout(200);
    const thSelectedAfterRowClick = await page.$eval('th[data-col="1"]', el => el.classList.contains('col-selected'));
    const trSelected = await page.$eval('tr[data-row="2"]', el => el.classList.contains('selected'));
    console.log(`点击行后: th.col-selected=${thSelectedAfterRowClick}, tr.selected=${trSelected}`);
    if (thSelectedAfterRowClick || !trSelected) {
      throw new Error('点击行后列选中未取消或行未选中');
    }

    // 5. 测试功能点 B：独立排序角标 (向下箭头)
    console.log('5. 测试功能点 B: 独立排序角标 (向下箭头)...');
    const sortBtn = await page.$('.db-col-sort-btn[data-sort="1"]');
    if (!sortBtn) throw new Error('未找到第 1 列的排序角标 .db-col-sort-btn');
    const initialBadge = await sortBtn.innerText();
    console.log('初始角标文字:', initialBadge);

    // 点击排序角标切换升序
    await sortBtn.click();
    await page.waitForTimeout(500);
    const ascBtn = await page.$('.db-col-sort-btn[data-sort="1"]');
    const ascBadge = await ascBtn.innerText();
    const isAscActive = await ascBtn.evaluate(el => el.classList.contains('active') && el.classList.contains('is-asc'));
    const thSelectedAfterSort = await page.$eval('th[data-col="1"]', el => el.classList.contains('col-selected'));
    console.log(`升序排序后: 角标=${ascBadge}, isAscActive=${isAscActive}, thSelected=${thSelectedAfterSort}`);
    if (ascBadge !== '▲' || !isAscActive) {
      throw new Error(`点击排序角标后未能切换为升序 ▲! 实际=${ascBadge}`);
    }
    if (thSelectedAfterSort) {
      throw new Error('点击排序角标不应误触发列选中!');
    }
    await page.screenshot({ path: path.join(SHOT_DIR, '04-column-sorted-asc.png') });
    console.log('独立排序角标 (升序) 测试通过，已截图: 04-column-sorted-asc.png');

    // 再次点击排序角标切换降序
    await ascBtn.click();
    await page.waitForTimeout(500);
    const descBtn = await page.$('.db-col-sort-btn[data-sort="1"]');
    const descBadge = await descBtn.innerText();
    const isDescActive = await descBtn.evaluate(el => el.classList.contains('active') && el.classList.contains('is-desc'));
    console.log(`降序排序后: 角标=${descBadge}, isDescActive=${isDescActive}`);
    if (descBadge !== '▼' || !isDescActive) {
      throw new Error(`点击排序角标后未能切换为降序 ▼! 实际=${descBadge}`);
    }
    await page.screenshot({ path: path.join(SHOT_DIR, '05-column-sorted-desc.png') });
    console.log('独立排序角标 (降序) 测试通过，已截图: 05-column-sorted-desc.png');

    // 6. 测试功能点 C：字段名上方左右滚动条与双向联动
    console.log('6. 测试功能点 C: 字段名上方左右滚动条与双向联动...');
    const topScroll = await page.$('.db-table-top-scroll');
    const tableScroll = await page.$('.db-table-scroll');
    if (!topScroll || !tableScroll) throw new Error('缺少 .db-table-top-scroll 或 .db-table-scroll 元素');

    const topScrollDisplay = await topScroll.evaluate(el => window.getComputedStyle(el).display);
    console.log('顶部水平滚动条 display:', topScrollDisplay);
    if (topScrollDisplay === 'none') {
      throw new Error('宽表查询结果应显示顶部水平滚动条，但当前为 none');
    }

    // 滚动顶部滚动条，验证下方表格同步联动
    console.log('操作顶部水平滚动条向右滚动 240px...');
    await topScroll.evaluate(el => { el.scrollLeft = 240; el.dispatchEvent(new Event('scroll')); });
    await page.waitForTimeout(300);
    const tableScrollLeft = await tableScroll.evaluate(el => el.scrollLeft);
    console.log('下方表格当前 scrollLeft:', tableScrollLeft);
    if (Math.abs(tableScrollLeft - 240) > 5) {
      throw new Error(`顶部滚动未同步至下方表格! 期望~240, 实际=${tableScrollLeft}`);
    }

    // 滚动下方表格，验证顶部滚动条同步联动
    console.log('操作下方表格滚动至 100px...');
    await tableScroll.evaluate(el => { el.scrollLeft = 100; el.dispatchEvent(new Event('scroll')); });
    await page.waitForTimeout(300);
    const topScrollLeft = await topScroll.evaluate(el => el.scrollLeft);
    console.log('顶部滚动条当前 scrollLeft:', topScrollLeft);
    if (Math.abs(topScrollLeft - 100) > 5) {
      throw new Error(`下方表格滚动未同步至顶部滚动条! 期望~100, 实际=${topScrollLeft}`);
    }
    await page.screenshot({ path: path.join(SHOT_DIR, '06-top-scroll-synced.png') });
    console.log('顶部左右滚动条双向联动测试通过，已截图: 06-top-scroll-synced.png');

    // 7. 测试功能点 D：右键复制为 INSERT 和 UPDATE 语句（无双引号、无库名）
    console.log('7. 测试功能点 D: 右键复制去除双引号与库名...');
    // 右键第 1 行
    const firstCell = await page.$('td[data-row="0"][data-col="1"]');
    await firstCell.click({ button: 'right' });
    await page.waitForSelector('.db-popup-menu', { timeout: 5000 });
    await page.screenshot({ path: path.join(SHOT_DIR, '07-context-menu.png') });
    console.log('右键菜单已弹出，已截图: 07-context-menu.png');

    // 点击“复制为 INSERT 语句”
    console.log('点击“复制为 INSERT 语句”...');
    await page.click('.db-popup-menu >> text=复制为 INSERT 语句');
    await page.waitForTimeout(400);

    // 读取剪贴板内容
    let insertSQL = await page.evaluate(async () => {
      return await navigator.clipboard.readText();
    });
    console.log('剪贴板获取的 INSERT 语句:\n' + insertSQL);

    // 校验 INSERT:
    // 1. 表名不能包含 "JSXG2". 或 "KAIRO_LAB".
    // 2. 表名不能被双引号包裹，如 "EMPLOYEES"
    // 3. 列名不能被双引号包裹，如 "ID", "EMPLOYEE_NO"
    const insertHeader = insertSQL.split(/VALUES/i)[0];
    if (insertHeader.indexOf('"') >= 0) {
      throw new Error('INSERT 语句的表名或字段名中仍包含双引号: ' + insertHeader);
    }
    if (/INSERT INTO [^.\s]+\./i.test(insertHeader)) {
      throw new Error('INSERT 语句中仍包含 schema 库名前缀: ' + insertHeader);
    }
    if (!/^INSERT INTO \w+ \([^"]+\) VALUES \(/i.test(insertSQL)) {
      throw new Error('INSERT 语句结构不符合预期: ' + insertSQL);
    }
    console.log('INSERT 语句校验通过: 表名无库名前缀，表名及字段名均无双引号！');

    // 再次右键并点击“复制整行为 UPDATE 语句”
    console.log('右键点击“复制整行为 UPDATE 语句”...');
    await page.evaluate(() => navigator.clipboard.writeText(''));
    await firstCell.click({ button: 'right' });
    await page.waitForSelector('.db-popup-menu', { timeout: 5000 });
    await page.click('.db-popup-menu >> text=复制整行为 UPDATE 语句');
    
    // 等待异步剪贴板写入完成 (包含获取主键元数据网络请求)
    let updateSQL = '';
    for (let i = 0; i < 20; i++) {
      await page.waitForTimeout(400);
      updateSQL = await page.evaluate(() => navigator.clipboard.readText());
      if (updateSQL && updateSQL.startsWith('UPDATE')) break;
    }
    console.log('剪贴板获取的 UPDATE 语句:\n' + updateSQL);

    // 校验 UPDATE:
    if (!updateSQL || !updateSQL.trim().startsWith('UPDATE ')) {
      throw new Error('未在剪贴板中获取到有效的 UPDATE 语句，剪贴板内容为空或非 UPDATE: ' + JSON.stringify(updateSQL));
    }
    // 1. 表名无库名前缀、无双引号
    // 2. SET 与 WHERE 中的字段名无双引号
    const updateTarget = updateSQL.split(/SET/i)[0];
    if (updateTarget.indexOf('"') >= 0) {
      throw new Error('UPDATE 语句表名包含双引号: ' + updateTarget);
    }
    if (/UPDATE [^.\s]+\./i.test(updateTarget)) {
      throw new Error('UPDATE 语句包含 schema 库名前缀: ' + updateTarget);
    }
    // 检查字段名是否有双引号
    const setPart = updateSQL.replace(/^UPDATE\s+\w+\s+SET\s+/i, '').split(/\s+WHERE\s+/i)[0];
    const assignments = setPart.split(/,(?=(?:[^']*'[^']*')*[^']*$)/); // 按逗号分割，忽略字符串内部的逗号
    if (assignments.length === 0 || !assignments[0].trim()) {
      throw new Error('UPDATE 语句 SET 赋值列表为空: ' + updateSQL);
    }
    for (const assign of assignments) {
      const col = assign.split('=')[0].trim();
      if (!col || col.indexOf('"') >= 0) {
        throw new Error(`UPDATE 语句字段名 [${col}] 包含双引号: ${assign}`);
      }
    }
    const wherePart = updateSQL.split(/\s+WHERE\s+/i)[1];
    if (!wherePart || !wherePart.trim()) {
      throw new Error('UPDATE 语句缺少 WHERE 主键条件: ' + updateSQL);
    }
    const whereConditions = wherePart.replace(/;$/, '').split(/\s+AND\s+/i);
    for (const cond of whereConditions) {
      const col = cond.split('=')[0].trim();
      if (!col || col.indexOf('"') >= 0) {
        throw new Error(`UPDATE 语句 WHERE 字段名 [${col}] 包含双引号: ${cond}`);
      }
    }
    console.log('UPDATE 语句校验通过: 表名无库名前缀，表名及字段名均无双引号，包含完整 WHERE 条件！');

    console.log('\n========================================');
    console.log('所有深度真实浏览器与真实数据库测试项全部通过！');
    console.log('========================================');
  } finally {
    await browser.close();
  }
}

const { register } = require('./tests/42-database-deep');

if (require.main === module) {
  run().catch(err => {
    console.error('\n❌ 深度测试失败:', err);
    process.exit(1);
  });
}

module.exports = { run, register };
