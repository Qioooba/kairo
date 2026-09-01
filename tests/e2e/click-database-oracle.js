'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT = 'D:\\kairo-test-runtime\\shots';
fs.mkdirSync(SHOT, { recursive: true });

function fail(msg) {
  throw new Error(msg);
}

(async () => {
  const browser = await chromium.launch({ headless: true, channel: 'msedge' });
  const page = await browser.newPage({ viewport: { width: 1366, height: 900 }, acceptDownloads: true });
  page.setDefaultTimeout(30000);
  await page.addInitScript(() => { try { delete window.showSaveFilePicker; } catch (_) {} });
  await page.goto(BASE + '/#/database', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#db-source', { timeout: 15000 });

  const opts = await page.$$eval('#db-source option', els => els.map(e => ({ v: e.value, t: e.textContent })));
  const hit = opts.find(o => o.t && o.t.indexOf('KAIRO_LAB') >= 0);
  if (!hit) fail('missing KAIRO_LAB source, options=' + JSON.stringify(opts));
  await page.selectOption('#db-source', hit.v);
  await page.waitForTimeout(800);

  await page.waitForFunction(() => Array.from(document.querySelectorAll('#db-schema option')).some(x => (x.value || x.textContent) === 'KAIRO_LAB'));
  await page.selectOption('#db-schema', 'KAIRO_LAB');
  await page.waitForFunction(() => (document.body.innerText || '').indexOf('EMPLOYEES') >= 0, null, { timeout: 20000 });

  await page.click('#db-test');
  await page.waitForTimeout(1800);
  await page.screenshot({ path: path.join(SHOT, 'db-connected.png') });

  await page.click('text=EMPLOYEES');
  await page.waitForFunction(() => {
    const body = document.getElementById('db-inspect-body');
    return body && body.innerText.indexOf('EMPLOYEE_NO') >= 0 && body.innerText.indexOf('PK') >= 0;
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, 'db-inspect-fields.png') });

  await page.click('.db-inspect-tab[data-tab="indexes"]');
  await page.waitForFunction(() => {
    const t = document.getElementById('db-inspect-body').innerText;
    return t.indexOf('UX_EMP_NO') >= 0 && t.indexOf('IX_EMP_DEPT') >= 0;
  }, null, { timeout: 8000 });
  await page.screenshot({ path: path.join(SHOT, 'db-inspect-indexes.png') });

  await page.click('.db-inspect-tab[data-tab="constraints"]');
  await page.waitForFunction(() => {
    const t = document.getElementById('db-inspect-body').innerText;
    return t.indexOf('PRIMARY KEY') >= 0 && (t.indexOf('FOREIGN KEY') >= 0 || t.indexOf('R') >= 0);
  }, null, { timeout: 8000 });
  await page.screenshot({ path: path.join(SHOT, 'db-inspect-constraints.png') });

  await page.click('.db-inspect-tab[data-tab="ddl"]');
  await page.waitForFunction(() => {
    const t = document.getElementById('db-inspect-body').innerText.toUpperCase();
    return t.indexOf('CREATE') >= 0 && t.indexOf('EMPLOYEES') >= 0;
  }, null, { timeout: 8000 });
  await page.screenshot({ path: path.join(SHOT, 'db-inspect-ddl.png') });
  await page.screenshot({ path: path.join(SHOT, 'db-inspect.png') });

  await page.fill('#db-sql', "INSERT INTO kairo_lab.employees(id) VALUES (1)");
  await page.click('#db-run');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.indexOf('查询包含写入') >= 0 || t.indexOf('一期仅允许只读查询') >= 0;
  }, null, { timeout: 10000 });
  await page.screenshot({ path: path.join(SHOT, 'db-readonly-reject.png') });

  await page.fill('#db-sql', 'SELECT COUNT(*) AS EMP_COUNT FROM kairo_lab.employees');
  await page.click('#db-run');
  await page.waitForFunction(() => (document.body.innerText || '').indexOf('2500') >= 0, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, 'db-count-2500.png') });

  await page.fill('#db-sql', "SELECT employee_no, display_name, remark, monthly_salary FROM kairo_lab.employees WHERE display_name LIKE '%张%' AND ROWNUM <= 30");
  await page.click('#db-run');
  await page.waitForFunction(() => (document.body.innerText || '').indexOf('张') >= 0 && (document.body.innerText || '').indexOf('中文备注') >= 0, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, 'db-query-grid.png') });

  await page.fill('#db-result-filter', '张三84');
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOT, 'db-query-filter.png') });
  await page.fill('#db-result-filter', '');

  await page.click('#db-view-record');
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOT, 'db-query-record.png') });

  await page.click('#db-view-grid');
  const [download] = await Promise.all([
    page.waitForEvent('download', { timeout: 20000 }),
    page.click('#db-export')
  ]);
  const csvPath = path.join(SHOT, 'employees-zhang.csv');
  await download.saveAs(csvPath);
  const csvText = fs.readFileSync(csvPath, 'utf8');
  if (csvText.indexOf('EMPLOYEE_NO') < 0 && csvText.indexOf('employee_no') < 0) fail('CSV missing column header');
  if (csvText.indexOf('张') < 0) fail('CSV missing Chinese rows');
  await page.waitForFunction(() => (document.body.innerText || '').indexOf('CSV 导出完成') >= 0, null, { timeout: 8000 });

  await page.fill('#db-sql', 'SELECT id, event_type, payload FROM kairo_lab.audit_events WHERE ROWNUM <= 400');
  await page.click('#db-run');
  await page.waitForFunction(() => (document.getElementById('db-query-status') || {}).textContent.indexOf('400 行') >= 0, null, { timeout: 20000 });
  await page.screenshot({ path: path.join(SHOT, 'db-audit-400.png') });

  await page.fill('#db-sql', 'SELECT employee_no FROM kairo_lab.employees WHERE 1 = 0');
  await page.click('#db-run');
  await page.waitForTimeout(1500);
  await page.screenshot({ path: path.join(SHOT, 'db-empty.png') });

  const historyInfo = await page.evaluate(() => {
    const select = document.getElementById('db-history');
    const values = select ? Array.from(select.options).map(o => o.getAttribute('value') || '') : [];
    return {
      values,
      store: localStorage.getItem('kairo:database:sql-history:v1') || '',
      html: select ? select.innerHTML.slice(0, 500) : 'missing'
    };
  });
  const historyCount = historyInfo.values.filter(v => v !== '').length;
  if (historyCount < 2) fail('SQL history did not record queries: ' + JSON.stringify(historyInfo));
  await page.selectOption('#db-history', '0');
  const restored = await page.$eval('#db-sql', el => el.value);
  if (!restored || restored.indexOf('SELECT') < 0) fail('history restore failed: ' + restored);
  await page.screenshot({ path: path.join(SHOT, 'db-history.png') });

  await page.click('#db-explain');
  await page.waitForTimeout(2000);
  const planTab = await page.$('#db-view-plan');
  if (planTab) await planTab.click();
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.indexOf('PLAN') >= 0 || t.indexOf('TABLE ACCESS') >= 0 || t.indexOf('SELECT STATEMENT') >= 0;
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, 'db-explain.png') });

  await page.setViewportSize({ width: 1024, height: 768 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOT, 'db-1024x768.png') });
  console.log('OK database clicks csv=' + csvPath + ' history=' + historyCount);
  await browser.close();
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
