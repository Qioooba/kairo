'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const assert = require('assert');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT_DIR = path.resolve(__dirname, '../../test-results/screenshots/deep-verification');
fs.mkdirSync(SHOT_DIR, { recursive: true });

async function run() {
  console.log('========================================================');
  console.log('真实浏览器深度端到端测试开始 (Playwright Chromium)');
  console.log('目标服务:', BASE);
  console.log('截图目录:', SHOT_DIR);
  console.log('========================================================\n');

  let browser;
  try {
    browser = await chromium.launch({ headless: true, channel: 'msedge' });
    console.log('[Browser] 使用 Microsoft Edge');
  } catch (_) {
    browser = await chromium.launch({ headless: true });
    console.log('[Browser] 使用 Chromium');
  }

  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write']
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);

  // 捕获剪贴板内容与控制台
  await page.addInitScript(() => {
    window.__clipboardHistory = [];
    if (navigator.clipboard) {
      const origWriteText = navigator.clipboard.writeText.bind(navigator.clipboard);
      navigator.clipboard.writeText = async (text) => {
        window.__clipboardHistory.push(text);
        try {
          await origWriteText(text);
        } catch (_) {}
      };
    }
  });

  page.on('console', msg => {
    if (msg.type() === 'error') {
      console.log('[Browser Error]', msg.text());
    }
  });

  try {
    // -------------------------------------------------------------
    // PART 1: 数据库工作台 - 无论 SELECT 投影哪些字段，复制 UPDATE 必须按主键 WHERE
    // -------------------------------------------------------------
    console.log('\n--- [Test 1] 数据库工作台: SELECT 未投影主键时，复制 UPDATE 强制根据主键 WHERE ---');
    await page.goto(BASE + '/#/database', { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#db-source', { timeout: 15000 });

    // 选择真实 Oracle 数据源
    const opts = await page.$$eval('#db-source option', els => els.map(e => ({ v: e.value, t: e.textContent })));
    const labSource = opts.find(o => o.t && o.t.indexOf('KAIRO_LAB') >= 0);
    assert.ok(labSource, '未找到 KAIRO_LAB 数据源');
    await page.selectOption('#db-source', labSource.v);
    await page.waitForTimeout(1000);

    // 1.1 查询非主键字段 (DISPLAY_NAME, MONTHLY_SALARY)，故意不包含 ID
    console.log('1.1 执行只查询 DISPLAY_NAME, MONTHLY_SALARY 的 SQL (排除主键 ID)...');
    await page.fill('#db-sql', 'SELECT DISPLAY_NAME, MONTHLY_SALARY FROM KAIRO_LAB.EMPLOYEES WHERE ROWNUM <= 5');
    await page.click('#db-run');

    await page.waitForFunction(() => {
      const tbody = document.getElementById('db-result-body');
      return tbody && tbody.querySelectorAll('tr[data-row]').length > 0;
    }, null, { timeout: 20000 });
    await page.waitForTimeout(600);

    // 验证返回的列只有两列，且不包含主键 ID
    const colNames = await page.$$eval('#db-result-grid th .db-col-name', els => els.map(e => e.textContent.trim()));
    console.log('结果集列头:', colNames);
    assert.deepStrictEqual(colNames, ['DISPLAY_NAME', 'MONTHLY_SALARY']);

    // 右键第一行第一列的单元格
    console.log('1.2 单元格右键，弹出菜单并点击 "复制为 UPDATE 语句（单字段）"...');
    const firstCell = await page.$('#db-result-body tr[data-row="0"] td[data-col="0"]');
    assert.ok(firstCell, '第一行第一列单元格未找到');
    await firstCell.click({ button: 'right' });
    await page.waitForSelector('.db-popup-menu', { timeout: 5000 });

    await page.screenshot({ path: path.join(SHOT_DIR, '01-context-menu-single-update.png') });
    console.log('已截图: 01-context-menu-single-update.png');

    // 点击复制为 UPDATE 语句（单字段）
    const updateBtn = await page.waitForSelector('.db-popup-menu button:has-text("复制为 UPDATE 语句（单字段）")');
    await updateBtn.click();
    await page.waitForTimeout(1000);

    // 检查剪贴板历史
    const copiedSQL1 = await page.evaluate(() => window.__clipboardHistory[window.__clipboardHistory.length - 1]);
    console.log('生成的 UPDATE 语句 (单字段):', copiedSQL1);

    // 验证核心诉求 1：WHERE 必须是根据该表主键 ID 作为条件，而不是用 SELECT 出来的 DISPLAY_NAME 或 MONTHLY_SALARY
    assert.ok(copiedSQL1, '未获取到复制的 UPDATE 语句');
    assert.match(copiedSQL1, /^UPDATE\s+(KAIRO_LAB\.)?EMPLOYEES\s+SET\s+DISPLAY_NAME\s*=\s*/i, 'SET 语句应设置 DISPLAY_NAME');
    assert.match(copiedSQL1, /WHERE\s+ID\s*=\s*'?\d+'?/i, 'WHERE 条件必须且仅以主键 ID 作为条件！');
    assert.ok(!copiedSQL1.includes('WHERE DISPLAY_NAME'), 'WHERE 条件不应使用未指定为主键的投影字段');
    console.log('-> [PASS] 测试 1 通过: 未投影主键时，复制 UPDATE 语句成功回查并以主键 ID 作为 WHERE 条件！');

    // -------------------------------------------------------------
    // PART 2: 数据库工作台 - 多选几列 / Ctrl 多选几列，右键复制为 UPDATE 语句
    // -------------------------------------------------------------
    console.log('\n--- [Test 2] 数据库工作台: 多选几列 / Ctrl 多选，右键复制为包含多列 SET 的 UPDATE 语句 ---');
    // 查询包含更多列的表，例如 4 列
    await page.fill('#db-sql', 'SELECT DISPLAY_NAME, MONTHLY_SALARY, REMARK, DEPARTMENT_ID FROM KAIRO_LAB.EMPLOYEES WHERE ROWNUM <= 5');
    await page.click('#db-run');
    await page.waitForFunction(() => {
      const tbody = document.getElementById('db-result-body');
      return tbody && tbody.querySelectorAll('tr[data-row]').length > 0;
    }, null, { timeout: 20000 });
    await page.waitForTimeout(600);

    // 2.1 点击第 0 列表头 (DISPLAY_NAME)
    console.log('2.1 点击第 0 列 (DISPLAY_NAME) 表头...');
    const th0 = await page.$('#db-result-grid th[data-col="0"]');
    assert.ok(th0, '表头 0 未找到');
    await th0.click();
    await page.waitForTimeout(300);

    // 2.2 按住 Ctrl 点击第 1 列表头 (MONTHLY_SALARY)
    console.log('2.2 按住 Ctrl 点击第 1 列 (MONTHLY_SALARY) 表头，实现多列多选...');
    const th1 = await page.$('#db-result-grid th[data-col="1"]');
    assert.ok(th1, '表头 1 未找到');
    await page.keyboard.down('Control');
    await th1.click();
    await page.keyboard.up('Control');
    await page.waitForTimeout(300);

    // 2.3 再按住 Ctrl 点击第 2 列表头 (REMARK)
    console.log('2.3 再按住 Ctrl 点击第 2 列 (REMARK) 表头，共选 3 列...');
    const th2 = await page.$('#db-result-grid th[data-col="2"]');
    assert.ok(th2, '表头 2 未找到');
    await page.keyboard.down('Control');
    await th2.click();
    await page.keyboard.up('Control');
    await page.waitForTimeout(300);

    // 验证 3 列都有 col-selected 高亮样式
    const selectedHeaderCount = await page.$$eval('#db-result-grid th.col-selected', els => els.length);
    console.log('当前处于 col-selected 样式的表头数:', selectedHeaderCount);
    assert.strictEqual(selectedHeaderCount, 3, '应该有 3 列被选中高亮');

    const selectedCellsCount = await page.$$eval('#db-result-body td.col-selected', els => els.length);
    console.log('当前处于 col-selected 样式的单元格数 (5行 x 3列 = 15):', selectedCellsCount);
    assert.strictEqual(selectedCellsCount, 15, '单元格应该联动展示高亮');

    await page.screenshot({ path: path.join(SHOT_DIR, '02-multi-col-selected.png') });
    console.log('已截图: 02-multi-col-selected.png');

    // 2.4 在第一行的第 1 列单元格右键，打开菜单
    console.log('2.4 右键选中列，检查右键菜单文本...');
    const row0Cell1 = await page.$('#db-result-body tr[data-row="0"] td[data-col="1"]');
    await row0Cell1.click({ button: 'right' });
    await page.waitForSelector('.db-popup-menu', { timeout: 5000 });

    await page.screenshot({ path: path.join(SHOT_DIR, '03-context-menu-multi-update.png') });
    console.log('已截图: 03-context-menu-multi-update.png');

    // 验证菜单项是否显示 "复制为 UPDATE 语句（已选 3 列）"
    const multiUpdateBtn = await page.waitForSelector('.db-popup-menu button:has-text("复制为 UPDATE 语句（已选 3 列）")');
    assert.ok(multiUpdateBtn, '未找到多选列的 UPDATE 菜单按钮');

    await multiUpdateBtn.click();
    await page.waitForTimeout(1000);

    const copiedSQL2 = await page.evaluate(() => window.__clipboardHistory[window.__clipboardHistory.length - 1]);
    console.log('生成的多列 UPDATE 语句:', copiedSQL2);

    // 验证多列 UPDATE 包含所选 3 列，并且 WHERE 为主键 ID
    assert.ok(copiedSQL2, '未获取到复制的多列 UPDATE 语句');
    assert.match(copiedSQL2, /^UPDATE\s+(KAIRO_LAB\.)?EMPLOYEES\s+SET\s+/i, '必须是 UPDATE 表名 SET');
    assert.ok(copiedSQL2.includes('DISPLAY_NAME = '), 'SET 中必须包含 DISPLAY_NAME');
    assert.ok(copiedSQL2.includes('MONTHLY_SALARY = '), 'SET 中必须包含 MONTHLY_SALARY');
    assert.ok(copiedSQL2.includes('REMARK = '), 'SET 中必须包含 REMARK');
    assert.ok(!copiedSQL2.includes('DEPARTMENT_ID = '), 'SET 中不应包含未选中的 DEPARTMENT_ID');
    assert.match(copiedSQL2, /WHERE\s+ID\s*=\s*'?\d+'?/i, 'WHERE 必须依然唯一定位主键 ID！');
    console.log('-> [PASS] 测试 2 通过: 多列选择与复制多列 UPDATE 语句完全正确！');

    // -------------------------------------------------------------
    // PART 3: 数据库工作台 - Ctrl + 滑轮横向滚动支持
    // -------------------------------------------------------------
    console.log('\n--- [Test 3] 数据库工作台: 按住 Ctrl + 鼠标滚轮横向滚动测试 ---');
    // 查询全部列，使得表格产生横向滚动条
    await page.fill('#db-sql', 'SELECT * FROM KAIRO_LAB.EMPLOYEES WHERE ROWNUM <= 25');
    await page.click('#db-run');
    await page.waitForFunction(() => {
      const tbody = document.getElementById('db-result-body');
      return tbody && tbody.querySelectorAll('tr[data-row]').length > 0;
    }, null, { timeout: 20000 });
    await page.waitForTimeout(600);

    // 获取滚动容器
    const scrollInfoBefore = await page.evaluate(() => {
      const scrollEl = document.querySelector('.db-table-scroll');
      return {
        scrollLeft: scrollEl.scrollLeft,
        scrollWidth: scrollEl.scrollWidth,
        clientWidth: scrollEl.clientWidth,
        hasHScroll: scrollEl.scrollWidth > scrollEl.clientWidth
      };
    });
    console.log('滚动前容器状态:', scrollInfoBefore);
    assert.ok(scrollInfoBefore.hasHScroll, '表格列数足够多，应存在横向滚动');

    // 在滚动容器上模拟 Ctrl + Wheel 事件
    console.log('模拟在 .db-table-scroll 上触发 Ctrl + Wheel (deltaY = 120)...');
    const scrollResult = await page.evaluate(() => {
      const scrollEl = document.querySelector('.db-table-scroll');
      const event = new WheelEvent('wheel', {
        deltaY: 120,
        ctrlKey: true,
        bubbles: true,
        cancelable: true
      });
      const notPrevented = scrollEl.dispatchEvent(event);
      return {
        defaultPrevented: event.defaultPrevented,
        scrollLeft: scrollEl.scrollLeft
      };
    });
    console.log('Ctrl + Wheel 触发结果:', scrollResult);
    assert.ok(scrollResult.defaultPrevented, '必须调用 e.preventDefault() 阻止浏览器默认页面缩放');
    assert.ok(scrollResult.scrollLeft > 0, 'scrollLeft 必须大于 0，说明成功横向滚动！');

    // 反向滚动验证
    const backScrollResult = await page.evaluate(() => {
      const scrollEl = document.querySelector('.db-table-scroll');
      const event = new WheelEvent('wheel', {
        deltaY: -120,
        ctrlKey: true,
        bubbles: true,
        cancelable: true
      });
      scrollEl.dispatchEvent(event);
      return {
        scrollLeft: scrollEl.scrollLeft
      };
    });
    console.log('反向 Ctrl + Wheel (deltaY = -120) 结果:', backScrollResult);
    assert.strictEqual(backScrollResult.scrollLeft, 0, '反向滚动应滚回起点 0');

    await page.screenshot({ path: path.join(SHOT_DIR, '04-db-horizontal-scroll.png') });
    console.log('已截图: 04-db-horizontal-scroll.png');
    console.log('-> [PASS] 测试 3 通过: 数据库工作台列表 Ctrl+滑轮横向滚动正常，且阻止了浏览器缩放！');

    // -------------------------------------------------------------
    // PART 4: 文本比对页面 - 按住 Ctrl + 滑轮横向滚动测试
    // -------------------------------------------------------------
    console.log('\n--- [Test 4] 文本比对页面: 按住 Ctrl + 鼠标滚轮横向滚动测试 ---');
    await page.goto(BASE + '/#/compare', { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('.cmp-editor-input', { timeout: 15000 });
    await page.waitForTimeout(500);

    // 4.1 测试编辑框内的 Ctrl + Wheel
    const textareas = page.locator('.cmp-editor-input');
    assert.strictEqual(await textareas.count(), 2, '文本比对左右编辑框应存在且数量为 2');

    // 生成超宽单行文本 (1000 个字符)
    const longLineLeft = 'LEFT_PREFIX_' + 'A'.repeat(500) + '_MID_DIFFERENCE_1_' + 'B'.repeat(500);
    const longLineRight = 'RIGHT_PREFIX_' + 'A'.repeat(500) + '_MID_DIFFERENCE_2_' + 'B'.repeat(500);

    await textareas.nth(0).fill(longLineLeft);
    await textareas.nth(1).fill(longLineRight);

    // 测试输入框自身的 Ctrl+Wheel
    console.log('4.1 模拟在左侧输入框上触发 Ctrl + Wheel (deltaY = 100)...');
    const textareaScrollResult = await page.evaluate(() => {
      const ta = document.querySelectorAll('.cmp-editor-input')[0];
      const event = new WheelEvent('wheel', {
        deltaY: 100,
        ctrlKey: true,
        bubbles: true,
        cancelable: true
      });
      ta.dispatchEvent(event);
      return {
        defaultPrevented: event.defaultPrevented,
        scrollLeft: ta.scrollLeft
      };
    });
    console.log('输入框 Ctrl + Wheel 结果:', textareaScrollResult);
    assert.ok(textareaScrollResult.defaultPrevented, '输入框上的 Ctrl+Wheel 必须 preventDefault');
    assert.ok(textareaScrollResult.scrollLeft > 0, '输入框 scrollLeft 必须大于 0');

    // 4.2 点击 "文本比对" 按钮，生成差异对比视图
    console.log('4.2 执行文本差异比对...');
    const compareBtn = await page.$('[data-action="text-compare"]');
    assert.ok(compareBtn, '文本比对按钮未找到');
    await compareBtn.click();
    await page.waitForTimeout(800);

    // 检查 diff 结果容器与底部横向滚动条
    const diffView = await page.$('.cmp-vdiff');
    assert.ok(diffView, '未找到对比结果视口 .cmp-vdiff');

    const leftTrack = await page.$('.cmp-hscroll-left');
    assert.ok(leftTrack, '未找到左侧横向滚动轨 .cmp-hscroll-left');

    // 在 .cmp-vdiff 上模拟 Ctrl + Wheel
    console.log('4.3 模拟在 .cmp-vdiff 上触发 Ctrl + Wheel (deltaY = 150)...');
    const diffScrollResult = await page.evaluate(() => {
      const vdiff = document.querySelector('.cmp-vdiff');
      const track = document.querySelector('.cmp-hscroll-left');
      const event = new WheelEvent('wheel', {
        deltaY: 150,
        ctrlKey: true,
        bubbles: true,
        cancelable: true
      });
      vdiff.dispatchEvent(event);
      return {
        defaultPrevented: event.defaultPrevented,
        trackScrollLeft: track ? track.scrollLeft : -1
      };
    });
    console.log('比对视图 Ctrl + Wheel 结果:', diffScrollResult);
    assert.ok(diffScrollResult.defaultPrevented, '比对视图上的 Ctrl+Wheel 必须 preventDefault');
    assert.ok(diffScrollResult.trackScrollLeft > 0, '比对底部滚动条 leftTrack 必须大于 0');

    // 在滚动轨 .cmp-hscroll-left 上模拟 Ctrl + Wheel
    console.log('4.4 模拟在 .cmp-hscroll-left 滚动轨上触发 Ctrl + Wheel...');
    const trackScrollResult = await page.evaluate(() => {
      const track = document.querySelector('.cmp-hscroll-left');
      const start = track.scrollLeft;
      const event = new WheelEvent('wheel', {
        deltaY: 100,
        ctrlKey: true,
        bubbles: true,
        cancelable: true
      });
      track.dispatchEvent(event);
      return {
        defaultPrevented: event.defaultPrevented,
        delta: track.scrollLeft - start
      };
    });
    console.log('滚动轨 Ctrl + Wheel 结果:', trackScrollResult);
    assert.ok(trackScrollResult.defaultPrevented, '滚动轨上的 Ctrl+Wheel 必须 preventDefault');
    assert.ok(trackScrollResult.delta > 0, '滚动轨位置必须发生位移');

    await page.screenshot({ path: path.join(SHOT_DIR, '05-compare-horizontal-scroll.png') });
    console.log('已截图: 05-compare-horizontal-scroll.png');
    console.log('-> [PASS] 测试 4 通过: 文本比对页面输入框与差异视图的 Ctrl+滑轮横向滚动完美运作！');

    console.log('\n========================================================');
    console.log('所有 4 项真实浏览器端到端深度测试全部通过！[100% SUCCESS]');
    console.log('========================================================\n');
  } finally {
    await browser.close();
  }
}

run().catch(err => {
  console.error('\n[TEST FAILED]:', err);
  process.exit(1);
});
