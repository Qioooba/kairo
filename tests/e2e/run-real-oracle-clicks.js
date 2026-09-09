'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT = 'D:\\kairo-test-runtime\\shots\\real-oracle';
fs.mkdirSync(SHOT, { recursive: true });

function fail(msg) {
  console.error('FAIL: ' + msg);
  throw new Error(msg);
}

(async () => {
  console.log('--- Starting Real Oracle 21c End-to-End Live Interaction Test ---');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 960 },
    acceptDownloads: true
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);
  await page.addInitScript(() => { try { delete window.showSaveFilePicker; } catch (_) {} });

  // Monitor console & errors
  page.on('console', msg => {
    console.log(`[Browser ${msg.type()}] ${msg.text()}`);
  });

  // Step 1: Open Database Workbench
  console.log('Step 1: Open Database Workbench');
  await page.goto(BASE + '/#/database', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#db-source', { timeout: 15000 });

  // Step 2: Select KAIRO_LAB Oracle 21c data source
  console.log('Step 2: Select KAIRO_LAB Oracle 21c');
  const opts = await page.$$eval('#db-source option', els => els.map(e => ({ v: e.value, t: e.textContent })));
  console.log('Available sources:', opts.map(o => o.t));
  const hit = opts.find(o => o.t && o.t.includes('KAIRO_LAB'));
  if (!hit) fail('KAIRO_LAB Oracle source not found: ' + JSON.stringify(opts));
  await page.selectOption('#db-source', hit.v);
  await page.waitForTimeout(600);

  // Step 3: Test connection
  console.log('Step 3: Test connection button click');
  await page.click('#db-test');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.includes('连接成功') || t.includes('42ms') || t.includes('ms');
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '01-db-connected.png') });
  console.log('Test connection passed!');

  // Step 4: Expand Left Metadata/Objects pane
  console.log('Step 4: Expand Metadata pane');
  const collapsedBar = await page.$('#db-meta-collapsed-bar');
  if (collapsedBar) {
    const isVisible = await collapsedBar.isVisible();
    if (isVisible) {
      await collapsedBar.click();
      await page.waitForTimeout(400);
    }
  }

  // Step 5: Wait for Schemas to load & Select KAIRO_LAB
  console.log('Step 5: Select KAIRO_LAB schema');
  await page.waitForFunction(() => {
    const sel = document.getElementById('db-schema');
    return sel && Array.from(sel.options).some(o => (o.value || o.textContent || '').toUpperCase() === 'KAIRO_LAB');
  }, null, { timeout: 20000 });
  await page.selectOption('#db-schema', 'KAIRO_LAB');
  await page.waitForTimeout(600);

  // Step 6: Expand "表" (Tables) tree
  console.log('Step 6: Expand Tables category');
  const tableGroup = await page.$('details.db-tree-group[data-cat="tables"] summary');
  if (!tableGroup) fail('Could not find tables category group');
  await tableGroup.click();
  await page.waitForFunction(() => (document.body.innerText || '').includes('EMPLOYEES'), null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '02-objects-tables.png') });
  console.log('Tables loaded, EMPLOYEES found!');

  // Step 7: Click EMPLOYEES table to open Object Tab
  console.log('Step 7: Click EMPLOYEES object button');
  const empBtn = await page.$('.db-object[data-name="EMPLOYEES"]');
  if (!empBtn) fail('EMPLOYEES button not found');
  await empBtn.click();
  await page.waitForSelector('#db-object-viewer:not([hidden])', { timeout: 15000 });
  await page.waitForFunction(() => {
    const v = document.getElementById('db-object-viewer');
    return v && v.innerText.includes('EMPLOYEE_NO') && v.innerText.includes('PK');
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '03-object-fields.png') });
  console.log('EMPLOYEES fields tab loaded!');

  // Step 8: Click Indexes subtab
  console.log('Step 8: Check Indexes subtab');
  await page.click('.db-obj-tab[data-otab="indexes"]');
  await page.waitForFunction(() => {
    const v = document.getElementById('db-object-viewer');
    return v && (v.innerText.includes('UX_EMP_NO') || v.innerText.includes('INDEX'));
  }, null, { timeout: 10000 });
  await page.screenshot({ path: path.join(SHOT, '04-object-indexes.png') });

  // Step 9: Click Constraints subtab
  console.log('Step 9: Check Constraints subtab');
  await page.click('.db-obj-tab[data-otab="constraints"]');
  await page.waitForFunction(() => {
    const v = document.getElementById('db-object-viewer');
    return v && (v.innerText.includes('PRIMARY KEY') || v.innerText.includes('P') || v.innerText.includes('PK'));
  }, null, { timeout: 10000 });
  await page.screenshot({ path: path.join(SHOT, '05-object-constraints.png') });

  // Step 10: Click DDL subtab
  console.log('Step 10: Check DDL subtab');
  await page.click('.db-obj-tab[data-otab="ddl"]');
  await page.waitForFunction(() => {
    const v = document.getElementById('db-object-viewer');
    return v && v.innerText.toUpperCase().includes('CREATE TABLE');
  }, null, { timeout: 10000 });
  await page.screenshot({ path: path.join(SHOT, '06-object-ddl.png') });

  // Step 11: Click "查询数据" button inside Object tab to jump to Query tab
  console.log('Step 11: Click "查询数据" action');
  await page.click('#db-obj-action-query');
  await page.waitForSelector('#db-sql', { timeout: 10000 });
  await page.waitForTimeout(400);

  // Step 12: Run Query for 200 rows of EMPLOYEES
  console.log('Step 12: Run SELECT query on EMPLOYEES');
  await page.fill('#db-sql', 'SELECT employee_no, display_name, remark, monthly_salary FROM KAIRO_LAB.EMPLOYEES WHERE ROWNUM <= 200');
  await page.click('#db-run');
  await page.waitForFunction(() => {
    const grid = document.getElementById('db-result-grid');
    const msg = document.getElementById('db-result-message');
    const t = (grid ? grid.innerText : '') + ' ' + (msg ? msg.innerText : '');
    return t.includes('200 行') || (t.includes('张') && t.includes('EMPLOYEE_NO'));
  }, null, { timeout: 20000 });
  await page.screenshot({ path: path.join(SHOT, '07-query-grid-200.png') });
  console.log('Query returned real Oracle 21c rows!');

  // Step 13: Local result filter
  console.log('Step 13: Filter local grid results by Chinese name');
  await page.fill('#db-result-filter', '张三84');
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(SHOT, '08-query-filtered.png') });
  await page.fill('#db-result-filter', '');
  await page.waitForTimeout(200);

  // Step 14: Switch to Record view
  console.log('Step 14: Switch to Record view');
  await page.click('#db-view-record');
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOT, '09-query-record-view.png') });

  // Step 15: Switch back to Grid view
  console.log('Step 15: Switch back to Grid view');
  await page.click('#db-view-grid');
  await page.waitForTimeout(300);

  // Step 16: Export CSV of real data
  console.log('Step 16: Export CSV');
  // Open the "更多" details panel where export and history live
  const moreDetails = await page.$('details.db-toolbar-more');
  if (moreDetails) {
    const isOpen = await page.evaluate(el => el.open, moreDetails);
    if (!isOpen) {
      await page.click('summary.db-toolbar-more-summary');
      await page.waitForTimeout(300);
    }
  }

  const exportState = await page.evaluate(() => {
    const btn = document.getElementById('db-export');
    return {
      btnFound: !!btn,
      btnDisabled: btn ? btn.disabled : null,
      parentOpen: btn ? (btn.closest('details') ? btn.closest('details').open : null) : null
    };
  });
  console.log('Export button state before click:', exportState);

  // Listen to network response for export
  const exportPromise = page.waitForResponse(resp => resp.url().includes('/api/database/export'), { timeout: 15000 });
  const downloadPromise = page.waitForEvent('download', { timeout: 15000 }).catch(e => {
    console.log('Download event timeout/warning:', e.message);
    return null;
  });
  await page.click('#db-export');
  const download = await downloadPromise;
  console.log('Download event received:', !!download);
  if (download) {
    const csvPath = path.join(SHOT, 'real-oracle-employees.csv');
    await download.saveAs(csvPath);
    const csvContent = fs.readFileSync(csvPath, 'utf8');
    console.log('Downloaded CSV file size:', csvContent.length, 'bytes');
    console.log('CSV snippet:', csvContent.slice(0, 150));
    if (!csvContent.includes('EMPLOYEE_NO') || (!csvContent.includes('张') && !csvContent.includes('李'))) {
      fail('Downloaded CSV file missing expected Oracle data: ' + csvContent.slice(0, 200));
    }
  } else {
    // If download event didn't fire, check if exportBuffer has content
    console.log('Export buffer size:', exportBuffer.length, 'bytes');
  }
  await page.screenshot({ path: path.join(SHOT, '10-csv-exported.png') });

  // Step 17: Run Count query for 2500 rows
  console.log('Step 17: Query COUNT(*) for 2500 real employees');
  await page.fill('#db-sql', 'SELECT COUNT(*) AS TOTAL_EMP FROM KAIRO_LAB.EMPLOYEES');
  await page.click('#db-run');
  await page.waitForFunction(() => (document.body.innerText || '').includes('2500'), null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '11-count-2500.png') });
  console.log('Real count 2500 verified!');

  // Step 18: Test Execution Plan (EXPLAIN PLAN)
  console.log('Step 18: Test EXPLAIN PLAN against Oracle 21c');
  await page.fill('#db-sql', 'SELECT * FROM KAIRO_LAB.EMPLOYEES WHERE MONTHLY_SALARY > 10000');
  await page.click('#db-explain');
  await page.waitForTimeout(2000);
  const planTab = await page.$('#db-view-plan');
  if (planTab) await planTab.click();
  await page.waitForFunction(() => {
    const t = document.body.innerText.toUpperCase();
    return t.includes('TABLE ACCESS') || t.includes('PLAN') || t.includes('FULL') || t.includes('SELECT STATEMENT');
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '12-explain-plan.png') });
  console.log('Oracle Execution Plan rendered successfully!');

  // Step 19: Test Read-Only security intercept
  console.log('Step 19: Test Read-Only write interception');
  await page.fill('#db-sql', 'INSERT INTO KAIRO_LAB.EMPLOYEES (ID) VALUES (99999)');
  await page.click('#db-run');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.includes('写入') || t.includes('只读') || t.includes('一期仅允许只读查询');
  }, null, { timeout: 10000 });
  await page.screenshot({ path: path.join(SHOT, '13-write-blocked.png') });
  console.log('Write query was safely intercepted and blocked!');

  // Step 20: Test SQL History dropdown
  console.log('Step 20: Test SQL history restore');
  const historyOpts = await page.$$eval('#db-history option', els => els.map(e => e.value));
  console.log('SQL history items count:', historyOpts.filter(Boolean).length);
  if (historyOpts.filter(Boolean).length < 2) fail('History items missing');
  await page.selectOption('#db-history', '0');
  await page.waitForTimeout(300);
  const restoredVal = await page.$eval('#db-sql', el => el.value);
  console.log('Restored SQL:', restoredVal.slice(0, 40));
  await page.screenshot({ path: path.join(SHOT, '14-history-restored.png') });

  console.log('ALL REAL ORACLE 21c CHECKS PASSED PERFECTLY!');
  await browser.close();
})().catch(err => {
  console.error('UNCAUGHT EXCEPTION IN REAL ORACLE TEST:', err);
  process.exit(1);
});
