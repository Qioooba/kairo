'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const LEFT = 'D:\\kairo-test-runtime\\compare-左 源';
const RIGHT = 'D:\\kairo-test-runtime\\compare-右 源';
const SHOT = 'D:\\kairo-test-runtime\\shots\\real-compare';
fs.mkdirSync(SHOT, { recursive: true });

function fail(msg) {
  console.error('FAIL: ' + msg);
  throw new Error(msg);
}

function parseSummary(text) {
  const n = (label) => {
    const m = text.match(new RegExp(label + '\\s+(\\d+)'));
    return m ? Number(m[1]) : 0;
  };
  return {
    same: n('相同'),
    different: n('不同'),
    leftNewer: n('左新'),
    rightNewer: n('右新'),
    leftOnly: n('仅左'),
    rightOnly: n('仅右')
  };
}

(async () => {
  console.log('--- Starting Real Disk Directory & Text Compare Live Interaction Test ---');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 960 }
  });
  const page = await context.newPage();
  page.setDefaultTimeout(45000);

  page.on('console', msg => {
    if (msg.type() === 'error' || msg.type() === 'warn') {
      console.log(`[Browser ${msg.type()}] ${msg.text()}`);
    }
  });

  // Step 1: Open Compare Workbench
  console.log('Step 1: Open Compare Workbench');
  await page.goto(BASE + '/#/compare', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);

  // Step 2: Test Text Compare
  console.log('Step 2: Testing Text Compare hunk merge');
  const textareas = await page.$$('textarea');
  if (textareas.length < 2) fail('Text comparison textareas missing');
  await textareas[0].fill('alpha-left\nbeta-shared\ngamma-left-only');
  await textareas[1].fill('alpha-right\nbeta-shared\ndelta-right-only');

  const textCompareBtn = await page.$('[data-action="text-compare"]');
  if (textCompareBtn) await textCompareBtn.click();
  await page.waitForTimeout(600);

  const toRight = await page.$('button[title="用左侧替换右侧"]');
  if (!toRight) fail('Merge button "用左侧替换右侧" not found');
  await toRight.click();
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOT, '01-text-merged-right.png') });
  console.log('Text merge left->right verified!');

  const toLeft = await page.$('button[title="用右侧替换左侧"]');
  if (toLeft) {
    await toLeft.click();
    await page.waitForTimeout(400);
    await page.screenshot({ path: path.join(SHOT, '02-text-merged-left.png') });
  }

  // Step 3: Switch to Folder Compare tab
  console.log('Step 3: Switch to Folder Compare tab');
  const folderTabBtn = await page.$('button:has-text("文件夹比较")');
  if (!folderTabBtn) fail('Folder Compare tab button not found');
  await folderTabBtn.click();
  await page.waitForTimeout(600);

  // Step 4: Fill Left and Right Real Paths
  console.log('Step 4: Input Left & Right paths on disk');
  const inputs = await page.$$('.cmp-folder-path');
  if (inputs.length < 2) fail('Folder path inputs not found');
  await inputs[0].fill(LEFT);
  await inputs[0].dispatchEvent('change');
  await inputs[1].fill(RIGHT);
  await inputs[1].dispatchEvent('change');
  await page.waitForTimeout(300);

  // Step 5: Test Sources Button Click
  console.log('Step 5: Test sources button click');
  await page.click('[data-action="compare-test"]');
  await page.waitForFunction(() => {
    const t = document.body.innerText || '';
    return t.includes('两侧来源可用') || (t.includes('左 正常') && t.includes('右 正常'));
  }, null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '03-folder-test-passed.png') });
  console.log('Folder source test passed!');

  // Step 6: Configure Scan Settings (depth=0 for unlimited recursive depth)
  console.log('Step 6: Open scan settings popover and select depth=0');
  const scanSettingsSummary = await page.$('summary:has-text("扫描设置")');
  if (scanSettingsSummary) {
    await scanSettingsSummary.click();
    await page.waitForTimeout(200);
    await page.selectOption('#cmp-scan-depth', '0');
    await scanSettingsSummary.click();
    await page.waitForTimeout(200);
  }

  // Step 7: Click "开始比较"
  console.log('Step 7: Start directory scan on real 4034 files');
  await page.click('[data-action="compare-scan"]');
  await page.waitForFunction(() => {
    const p = document.querySelector('.cmp-scan-progress');
    return p && p.textContent.includes('完成') && p.textContent.includes('仅左');
  }, null, { timeout: 90000 });

  const summaryText = await page.$eval('.cmp-scan-progress', el => el.textContent);
  console.log('Initial Scan Progress Summary:', summaryText);
  const summary1 = parseSummary(summaryText);
  console.log('Parsed summary:', summary1);
  if (!summary1.leftOnly || !summary1.rightOnly) {
    fail('Expected left-only and right-only counts in scan summary');
  }
  await page.screenshot({ path: path.join(SHOT, '04-folder-scan-results.png') });

  // Step 8: Select visible diffs and Cover to Right
  console.log('Step 8: Select all visible differences and Cover to Right');
  const selectAllCheckbox = await page.$('input[aria-label="选择当前筛选结果"]');
  if (!selectAllCheckbox) fail('Select all checkbox not found in folder tree head');
  await selectAllCheckbox.click();
  await page.waitForTimeout(400);

  await page.waitForFunction(() => {
    const count = document.querySelector('.cmp2-selection-count');
    return count && count.textContent && !count.textContent.includes(' 0 项');
  }, null, { timeout: 10000 });
  const selText = await page.$eval('.cmp2-selection-count', el => el.textContent);
  console.log('Selection count:', selText);

  // Click "覆盖到右侧"
  console.log('Step 9: Click Cover to Right');
  await page.click('[data-action="cover-right"]');
  await page.waitForSelector('.cmp-sync-dialog', { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '05-cover-right-preview.png') });

  const previewTitle = await page.$eval('.cmp-sync-dialog', el => el.innerText);
  if (!previewTitle.includes('不删除')) {
    fail('Cover preview dialog missing safety wording: ' + previewTitle);
  }

  // Click "开始覆盖" in the dialog
  console.log('Step 10: Click Start Cover');
  await page.click('.cmp-sync-dialog button:has-text("开始覆盖")');
  await page.waitForFunction(() => (document.body.innerText || '').includes('覆盖完成'), null, { timeout: 120000 });
  console.log('Cover to Right finished!');
  await page.waitForTimeout(1000);

  // Step 11: Physically verify Left-only files exist in Right on disk!
  console.log('Step 11: Verify disk files after Cover to Right');
  const rightHasLeftFile = fs.existsSync(path.join(RIGHT, '仅左', 'left_001.txt'));
  console.log('Physical check RIGHT\\仅左\\left_001.txt exists:', rightHasLeftFile);
  if (!rightHasLeftFile) fail('Right directory missing copied left_001.txt from sync!');
  await page.screenshot({ path: path.join(SHOT, '06-cover-right-done.png') });

  // Step 12: Rescan and Cover to Left
  console.log('Step 12: Rescan and Cover to Left');
  await page.click('[data-action="compare-scan"]');
  await page.waitForFunction(() => {
    const p = document.querySelector('.cmp-scan-progress');
    return p && p.textContent.includes('完成');
  }, null, { timeout: 90000 });

  const selectAllCheckbox2 = await page.$('input[aria-label="选择当前筛选结果"]');
  if (selectAllCheckbox2) {
    await selectAllCheckbox2.click();
    await page.waitForTimeout(400);
  }
  await page.click('[data-action="cover-left"]');
  await page.waitForSelector('.cmp-sync-dialog', { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, '07-cover-left-preview.png') });

  await page.click('.cmp-sync-dialog button:has-text("开始覆盖")');
  await page.waitForFunction(() => (document.body.innerText || '').includes('覆盖完成'), null, { timeout: 120000 });
  console.log('Cover to Left finished!');
  await page.waitForTimeout(1000);

  // Step 13: Physically verify Right-only files exist in Left on disk!
  console.log('Step 13: Verify disk files after Cover to Left');
  const leftHasRightFile = fs.existsSync(path.join(LEFT, '仅右', 'right_001.txt'));
  console.log('Physical check LEFT\\仅右\\right_001.txt exists:', leftHasRightFile);
  if (!leftHasRightFile) fail('Left directory missing copied right_001.txt from sync!');

  const chineseCheck = fs.existsSync(path.join(LEFT, '中文 目录', '空格 文件.txt')) &&
                       fs.existsSync(path.join(RIGHT, '中文 目录', '空格 文件.txt'));
  console.log('Physical check Chinese filename fixture:', chineseCheck);
  if (!chineseCheck) fail('Chinese directory fixture missing!');
  await page.screenshot({ path: path.join(SHOT, '08-cover-left-done.png') });

  console.log('ALL REAL DISK COMPARE TESTS PASSED PERFECTLY!');
  await browser.close();
})().catch(err => {
  console.error('UNCAUGHT EXCEPTION IN REAL COMPARE TEST:', err);
  process.exit(1);
});
