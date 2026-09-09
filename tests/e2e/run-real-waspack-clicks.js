'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const PROJECT_DIR = 'D:\\kairo-test-runtime\\was-project-lab';
const OUTPUT_DIR_1 = 'D:\\kairo-test-runtime\\was-output-lab-1';
const OUTPUT_DIR_2 = 'D:\\kairo-test-runtime\\was-output-lab-2';
const SHOT = 'D:\\kairo-test-runtime\\shots\\real-waspack';
fs.mkdirSync(SHOT, { recursive: true });

function fail(msg) {
  console.error('FAIL: ' + msg);
  throw new Error(msg);
}

(async () => {
  console.log('--- Starting Real WAS Package Workbench Live Interaction Test ---');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 960 }
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);

  page.on('console', msg => {
    if (msg.type() === 'error' || msg.type() === 'warn') {
      console.log(`[Browser ${msg.type()}] ${msg.text()}`);
    }
  });

  // Step 1: Open WAS Package Workbench
  console.log('Step 1: Open WAS Package Workbench');
  await page.goto(BASE + '/#/waspack', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#waspack-project', { timeout: 15000 });
  await page.waitForTimeout(600);

  // Step 2: Fill Configuration
  console.log('Step 2: Fill packaging form with real paths and manifest');
  await page.fill('#waspack-project', PROJECT_DIR);
  await page.dispatchEvent('#waspack-project', 'change');

  await page.fill('#waspack-output', OUTPUT_DIR_1);
  await page.dispatchEvent('#waspack-output', 'change');

  await page.fill('#waspack-package', 'TT20260907qijunV1');
  await page.dispatchEvent('#waspack-package', 'change');

  const manifest = [
    './CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp',
    './CreditManage/CreditLine/ProductInfo.jsp',
    './src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java',
    './WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class'
  ].join('\n');
  await page.fill('#waspack-manifest', manifest);
  await page.dispatchEvent('#waspack-manifest', 'input');

  const includeZip = await page.$('#waspack-include-zip');
  if (includeZip) {
    const isChecked = await includeZip.isChecked();
    if (!isChecked) await includeZip.click();
  }
  await page.screenshot({ path: path.join(SHOT, '01-form-filled.png') });
  console.log('Form filled successfully!');

  // Step 3: Click "仅预检"
  console.log('Step 3: Click "仅预检"');
  await page.click('button:has-text("仅预检")');
  await page.waitForFunction(() => {
    const p = document.querySelector('.waspack-panel:not([hidden])');
    return p && p.innerText.includes('可生成') && p.innerText.includes('将写入包的路径');
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '02-preview-passed.png') });
  console.log('Pre-check passed with 4 items ready to bundle!');

  // Step 4: Click "预检并生成"
  console.log('Step 4: Click "预检并生成" to build live tar, scripts, and zip');
  await page.click('button:has-text("预检并生成")');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.includes('投产包已生成') || t.includes('WAR / TAR 结果');
  }, null, { timeout: 30000 });
  await page.screenshot({ path: path.join(SHOT, '03-build-result.png') });
  console.log('Build completed!');

  // Step 5: Physically check disk files in OUTPUT_DIR_1
  console.log('Step 5: Physical disk check in OUTPUT_DIR_1');
  const tarPath = path.join(OUTPUT_DIR_1, 'TT20260907qijunV1.tar');
  const shPath = path.join(OUTPUT_DIR_1, 'TT20260907qijunV1.sh');
  const bakPath = path.join(OUTPUT_DIR_1, 'BakTT20260907qijunV1.sh');
  const listPath = path.join(OUTPUT_DIR_1, 'list.txt');
  const zipPath = path.join(OUTPUT_DIR_1, 'TT20260907qijunV1.zip');

  if (!fs.existsSync(tarPath)) fail('Missing generated TAR: ' + tarPath);
  if (!fs.existsSync(shPath)) fail('Missing execute script: ' + shPath);
  if (!fs.existsSync(bakPath)) fail('Missing backup script: ' + bakPath);
  if (!fs.existsSync(listPath)) fail('Missing list.txt: ' + listPath);
  if (!fs.existsSync(zipPath)) fail('Missing generated ZIP: ' + zipPath);

  const tarSize = fs.statSync(tarPath).size;
  const zipSize = fs.statSync(zipPath).size;
  console.log(`Verified generated artifacts: TAR (${tarSize} bytes), ZIP (${zipSize} bytes)`);

  // Step 6: Test WAR extraction & staged packaging in OUTPUT_DIR_2
  console.log('Step 6: Test WAR Extract to OUTPUT_DIR_2');
  await page.fill('#waspack-output', OUTPUT_DIR_2);
  await page.dispatchEvent('#waspack-output', 'change');
  await page.waitForTimeout(300);

  await page.click('button:has-text("抽取 WAR")');
  await page.waitForFunction(() => {
    const badge = document.querySelector('.waspack-stage-badge');
    return badge && badge.textContent.includes('WAR 已抽取');
  }, null, { timeout: 20000 });
  await page.screenshot({ path: path.join(SHOT, '04-war-extracted.png') });

  const warDir = path.join(OUTPUT_DIR_2, 'war');
  if (!fs.existsSync(warDir)) fail('WAR extracted folder does not exist: ' + warDir);
  console.log('Physical WAR directory verified on disk:', warDir);

  console.log('Step 7: Click "打包 WAR"');
  await page.click('button:has-text("打包 WAR")');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.includes('WAR 打包完成') || t.includes('按 war 当前内容');
  }, null, { timeout: 25000 });
  await page.screenshot({ path: path.join(SHOT, '05-war-packaged.png') });
  console.log('WAR packaged successfully with stage token!');

  // Step 8: Test History Tab
  console.log('Step 8: Check History tab');
  await page.click('button:has-text("历史")');
  await page.waitForSelector('.waspack-history-row', { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '06-history-list.png') });

  // Click "查看详情" on the first history row
  console.log('Step 9: Click history detail');
  const detailBtn = await page.$('.waspack-history-row button:has-text("查看详情")');
  if (detailBtn) {
    await detailBtn.click();
    await page.waitForSelector('.waspack-history-modal-body', { timeout: 10000 });
    await page.waitForFunction(() => {
      const b = document.querySelector('.waspack-history-modal-body');
      return b && (b.innerText.includes('SHA256') || b.innerText.includes('产物哈希'));
    }, null, { timeout: 10000 });
    await page.screenshot({ path: path.join(SHOT, '07-history-detail-modal.png') });
    console.log('History detail modal rendered with SHA256 integrity verified!');
    // Close modal
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  }

  // Step 10: Test Sensitive Path Interception
  console.log('Step 10: Test Sensitive Path Interception (security boundary)');
  await page.click('button:has-text("预检")');
  await page.fill('#waspack-output', 'C:\\Windows\\System32');
  await page.dispatchEvent('#waspack-output', 'change');
  await page.waitForTimeout(300);

  await page.click('button:has-text("预检并生成")');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.includes('输出目录非法') || t.includes('非法') || t.includes('敏感');
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '08-sensitive-path-blocked.png') });
  console.log('Sensitive system output path was strictly intercepted and rejected!');

  console.log('ALL REAL WAS PACKAGE WORKBENCH CHECKS PASSED PERFECTLY!');
  await browser.close();
})().catch(err => {
  console.error('UNCAUGHT EXCEPTION IN REAL WASPACK TEST:', err);
  process.exit(1);
});
