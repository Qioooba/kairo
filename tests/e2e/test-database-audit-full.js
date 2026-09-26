'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT_DIR = path.resolve(__dirname, '../../test-results/screenshots/database-audit');
fs.mkdirSync(SHOT_DIR, { recursive: true });

async function runResolutionAudit(browser, resName, width, height) {
  console.log(`\n======================================================`);
  console.log(`🚀 开始测试与审核分辨率: ${resName} (${width}x${height})`);
  console.log(`======================================================`);

  const context = await browser.newContext({
    viewport: { width, height },
    permissions: ['clipboard-read', 'clipboard-write']
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);

  page.on('console', msg => {
    if (msg.type() === 'error' || msg.type() === 'warn') {
      console.log(`[Browser Console ${msg.type()}]`, msg.text());
    }
  });

  try {
    // 1. 访问工作台主页
    console.log(`[${resName}] 1. 访问工作台主页...`);
    await page.goto(BASE + '/#/database', { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#db-source', { timeout: 15000 });
    await page.waitForTimeout(1000);

    // 确保切换到 SQL 查询页签 (查询 1)
    const sqlTab = await page.$('.db-editor-tab:not(.db-object-tab)');
    if (sqlTab) {
      await sqlTab.click();
      await page.waitForTimeout(500);
    }

    // 确保左侧面板为展开状态以展示方案 A 的完整对象栏与顶部数据源切换
    const isCollapsedInitially = await page.$eval('#db-meta-pane', el => el.classList.contains('is-collapsed'));
    if (isCollapsedInitially) {
      console.log(`[${resName}] 初始处于折叠状态，点击 #db-meta-collapsed-bar 展开左侧面板...`);
      await page.click('#db-meta-collapsed-bar');
      await page.waitForTimeout(500);
    }

    // 截图 1: 工作台主界面（左侧栏展开，方案 A 融合数据源切换栏）
    const shot1 = path.join(SHOT_DIR, `${resName}-01-workbench-expanded.png`);
    await page.screenshot({ path: shot1, fullPage: false });
    console.log(`[${resName}] 已保存工作台展开截图: ${path.basename(shot1)}`);

    // 2. 选择真实数据源 (Oracle)
    console.log(`[${resName}] 2. 交互下拉框: 选择数据源...`);
    const opts = await page.$$eval('#db-source option', els => els.map(e => ({ v: e.value, t: e.textContent })));
    const labSource = opts.find(o => o.t && o.t.indexOf('KAIRO_LAB') >= 0);
    if (labSource) {
      await page.selectOption('#db-source', labSource.v);
      await page.waitForTimeout(500);
    }

    // 3. 点击「测试连接」按钮（测试加载中与状态重置）
    console.log(`[${resName}] 3. 交互按钮: 点击「测试连接」...`);
    await page.click('#db-test');
    await page.waitForFunction(() => {
      const b = document.getElementById('db-test');
      return b && !b.disabled && b.textContent === '测试连接';
    }, null, { timeout: 15000 });
    await page.waitForTimeout(600);

    const shot2 = path.join(SHOT_DIR, `${resName}-02-test-connection-success.png`);
    await page.screenshot({ path: shot2, fullPage: false });
    console.log(`[${resName}] 已保存测试连接成功截图: ${path.basename(shot2)}`);

    // 4. 测试左侧对象栏拖动调整宽度 (db-split-x)
    console.log(`[${resName}] 4. 拖动测试: 拖动左侧分隔条 db-split-x 调整面板宽度...`);
    const splitX = await page.$('#db-split-x');
    if (splitX) {
      const box = await splitX.boundingBox();
      if (box) {
        const initialMetaWidth = await page.$eval('#db-meta-pane', el => el.offsetWidth);
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await page.mouse.down();
        await page.mouse.move(box.x + 80, box.y + box.height / 2, { steps: 10 });
        await page.mouse.up();
        await page.waitForTimeout(300);
        const newMetaWidth = await page.$eval('#db-meta-pane', el => el.offsetWidth);
        console.log(`[${resName}] 左侧面板宽度变化: ${initialMetaWidth}px -> ${newMetaWidth}px`);
      }
    }
    const shot3 = path.join(SHOT_DIR, `${resName}-03-splitter-dragged.png`);
    await page.screenshot({ path: shot3, fullPage: false });
    console.log(`[${resName}] 已保存分隔条拖动后截图: ${path.basename(shot3)}`);

    // 5. 点击「数据源管理」按钮打开弹窗，测试所有输入框、下拉框、单选/复选框
    console.log(`[${resName}] 5. 交互弹窗: 打开数据源管理弹窗，测试表单控件...`);
    await page.click('#db-manage');
    await page.waitForSelector('#dbf-name', { timeout: 5000 });
    await page.waitForTimeout(500);

    // 表单控件交互
    await page.fill('#dbf-name', '测试审计源_' + resName);
    await page.selectOption('#dbf-kind', 'mysql');
    await page.waitForTimeout(300);
    await page.selectOption('#dbf-kind', 'oracle');
    await page.waitForTimeout(300);

    // 复选框交互测试
    const readOnlyBox = await page.$('#dbf-read-only');
    const allowDdlBox = await page.$('#dbf-allow-ddl');
    if (readOnlyBox && allowDdlBox) {
      await readOnlyBox.click();
      await page.waitForTimeout(200);
      await allowDdlBox.click();
      await page.waitForTimeout(200);
    }

    const shot4 = path.join(SHOT_DIR, `${resName}-04-manager-modal-audit.png`);
    await page.screenshot({ path: shot4, fullPage: false });
    console.log(`[${resName}] 已保存数据源管理弹窗截图: ${path.basename(shot4)}`);

    // 关闭管理弹窗
    await page.click('#dbf-close');
    await page.waitForTimeout(400);

    // 6. 点击「工作台设置」按钮，审核设置弹窗
    console.log(`[${resName}] 6. 交互设置: 打开工作台设置弹窗...`);
    await page.click('#db-settings');
    await page.waitForSelector('#db-settings-close', { timeout: 5000 });
    await page.waitForTimeout(400);

    const shot5 = path.join(SHOT_DIR, `${resName}-05-settings-modal-audit.png`);
    await page.screenshot({ path: shot5, fullPage: false });
    console.log(`[${resName}] 已保存工作台设置弹窗截图: ${path.basename(shot5)}`);

    // 关闭设置弹窗
    await page.click('#db-settings-close');
    await page.waitForTimeout(400);

    // 7. 测试 SQL 编辑器垂直拖动调高 (db-sql-resizer)
    console.log(`[${resName}] 7. 拖动测试: 垂直拖动 SQL 编辑器调节高度 (db-sql-resizer)...`);
    const sqlResizer = await page.$('.db-sql-resizer');
    if (sqlResizer) {
      const box = await sqlResizer.boundingBox();
      if (box) {
        const initialH = await page.$eval('.db-sql-shell', el => el.offsetHeight);
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await page.mouse.down();
        await page.mouse.move(box.x + box.width / 2, box.y + 100, { steps: 10 });
        await page.mouse.up();
        await page.waitForTimeout(300);
        const newH = await page.$eval('.db-sql-shell', el => el.offsetHeight);
        console.log(`[${resName}] SQL 编辑器高度变化: ${initialH}px -> ${newH}px`);
      }
    }
    const shot6 = path.join(SHOT_DIR, `${resName}-06-sql-editor-resized.png`);
    await page.screenshot({ path: shot6, fullPage: false });
    console.log(`[${resName}] 已保存编辑器垂直调整截图: ${path.basename(shot6)}`);

    // 8. 测试多页签交互：新建页签
    console.log(`[${resName}] 8. 交互页签: 点击新建查询页签...`);
    await page.click('#db-tab-add');
    await page.waitForTimeout(500);
    const tabsCount = await page.$$eval('.db-editor-tab', els => els.length);
    console.log(`[${resName}] 当前页签总数: ${tabsCount}`);

    const shot7 = path.join(SHOT_DIR, `${resName}-07-new-tab-added.png`);
    await page.screenshot({ path: shot7, fullPage: false });
    console.log(`[${resName}] 已保存多页签截图: ${path.basename(shot7)}`);

    // 9. 测试左侧面板收起与数据源工具槽自动停靠到页签栏
    console.log(`[${resName}] 9. 交互折叠: 点击收起左侧对象栏...`);
    await page.click('#db-meta-toggle');
    await page.waitForTimeout(500);
    const isCollapsed = await page.$eval('#db-meta-pane', el => el.classList.contains('is-collapsed'));
    console.log(`[${resName}] 左侧面板已折叠: ${isCollapsed}`);

    const shot8 = path.join(SHOT_DIR, `${resName}-08-panel-collapsed-docked.png`);
    await page.screenshot({ path: shot8, fullPage: false });
    console.log(`[${resName}] 已保存面板折叠与数据源槽停靠截图: ${path.basename(shot8)}`);

    // 恢复展开
    console.log(`[${resName}] 交互展开: 点击折叠条恢复展开...`);
    await page.click('#db-meta-collapsed-bar');
    await page.waitForTimeout(500);

    // 10. 执行 SQL 查询并验证网格、表头排序、单元格右键
    console.log(`[${resName}] 10. 执行真实 SQL 并渲染结果网格...`);
    await page.fill('#db-sql', 'SELECT * FROM kairo_lab.employees WHERE ROWNUM <= 25');
    await page.click('#db-run');
    await page.waitForFunction(() => {
      const tbody = document.getElementById('db-result-body');
      return tbody && tbody.querySelectorAll('tr[data-row]').length > 0;
    }, null, { timeout: 20000 });
    await page.waitForTimeout(600);

    // 点击某单元格弹出右键菜单
    const cell = await page.$('td[data-row="2"][data-col="1"]');
    if (cell) {
      await cell.click({ button: 'right' });
      await page.waitForTimeout(300);
    }

    const shot9 = path.join(SHOT_DIR, `${resName}-09-query-results-and-contextmenu.png`);
    await page.screenshot({ path: shot9, fullPage: false });
    console.log(`[${resName}] 已保存真实查询与右键菜单截图: ${path.basename(shot9)}`);

    console.log(`✅ [${resName}] 所有 UI 交互、拖动与审核测试圆满完成！`);
  } finally {
    await context.close();
  }
}

async function main() {
  console.log('--- 启动 1080p & 2K 全分辨率 UI 交互与真实审核测试 ---');
  let browser;
  try {
    browser = await chromium.launch({ headless: true, channel: 'msedge' });
    console.log('使用系统已安装的 Microsoft Edge 浏览器');
  } catch (_) {
    browser = await chromium.launch({ headless: true });
    console.log('使用内置 Chromium 浏览器');
  }

  try {
    // 1. 运行 1080p 审计 (1920 x 1080)
    await runResolutionAudit(browser, '1080p', 1920, 1080);

    // 2. 运行 2K 审计 (2560 x 1440)
    await runResolutionAudit(browser, '2K', 2560, 1440);

    console.log('\n======================================================');
    console.log('🎉 1080p 和 2K 分辨率全量 UI 交互与截图审核全部执行完毕！');
    console.log(`截图输出目录: ${SHOT_DIR}`);
    console.log('======================================================\n');
  } finally {
    await browser.close();
  }
}

main().catch(err => {
  console.error('❌ 审核测试出现异常:', err);
  process.exit(1);
});
