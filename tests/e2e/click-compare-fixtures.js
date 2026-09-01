'use strict';
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const COMPARE_ROOT = process.env.COMPARE_LAB_ROOT || 'D:\\kairo-test-runtime';
const LEFT = process.env.COMPARE_LAB_ROOT ? path.join(COMPARE_ROOT, 'compare-左 源') : 'D:\\kairo-test-runtime\\compare-左 源';
const RIGHT = process.env.COMPARE_LAB_ROOT ? path.join(COMPARE_ROOT, 'compare-右 源') : 'D:\\kairo-test-runtime\\compare-右 源';
const SHOT = process.env.COMPARE_LAB_ROOT ? path.join(COMPARE_ROOT, 'shots') : 'D:\\kairo-test-runtime\\shots';
fs.mkdirSync(SHOT, { recursive: true });

function fail(msg) {
  throw new Error(msg);
}

function parseSummary(text) {
  const n = (label) => {
    const m = text.match(new RegExp(label + '\\s+(\\d+)'));
    return m ? Number(m[1]) : null;
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
  const browser = await chromium.launch({ headless: true, channel: 'msedge' });
  const page = await browser.newPage({ viewport: { width: 1366, height: 900 } });
  page.setDefaultTimeout(30000);
  await page.goto(BASE + '/#/compare', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);
  const lic = await page.$('#lic-title, .auth-overlay');
  if (lic) {
    const visible = await page.evaluate(() => {
      const el = document.querySelector('.auth-overlay');
      return el && getComputedStyle(el).display !== 'none';
    });
    if (visible) {
      await page.screenshot({ path: path.join(SHOT, 'blocked-overlay.png') });
      fail('activation/auth overlay is visible');
    }
  }

  const textareas = await page.$$('textarea');
  if (textareas.length < 2) fail('missing text editors');
  await textareas[0].fill('pending\nshared\nleft-only');
  await textareas[1].fill('paid\nshared\nright-only');
  await page.click('[data-action="text-compare"]');
  await page.waitForTimeout(800);
  const toRight = await page.$('button[title="用左侧替换右侧"]');
  const toLeft = await page.$('button[title="用右侧替换左侧"]');
  if (!toRight || !toLeft) fail('missing hunk merge buttons');
  await toRight.click();
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(SHOT, 'text-hunk-left-to-right.png') });
  const toLeftAfter = await page.$('button[title="用右侧替换左侧"]');
  if (toLeftAfter) await toLeftAfter.click();
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(SHOT, 'text-hunk-both-ways.png') });

  await page.click('button:has-text("文件夹比较")');
  await page.waitForTimeout(400);
  async function pick(i, dir) {
    const inputs = await page.$$('.cmp-folder-path');
    if (inputs.length < 2) fail('folder path inputs missing');
    await inputs[i].fill(dir);
    await page.waitForTimeout(200);
  }
  await pick(0, LEFT);
  await pick(1, RIGHT);
  const depth = await page.$('#cmp-scan-depth');
  if (depth) await page.selectOption('#cmp-scan-depth', '1');
  await page.click('[data-action="compare-test"]');
  await page.waitForFunction(() => document.body.innerText.includes('两侧来源可用') || (document.body.innerText.includes('左 正常') && document.body.innerText.includes('右 正常')), null, { timeout: 15000 });
  await page.screenshot({ path: path.join(SHOT, 'folder-test.png') });

  await page.click('[data-action="compare-scan"]');
  await page.waitForFunction(() => {
    const t = document.querySelector('.cmp-scan-progress');
    return t && t.textContent.indexOf('完成') === 0 && t.textContent.indexOf('仅左') >= 0;
  }, null, { timeout: 90000 });
  const firstSummaryText = await page.$eval('.cmp-scan-progress', el => el.textContent);
  const first = parseSummary(firstSummaryText);
  if (!first.leftOnly || first.leftOnly < 1) fail('expected left-only folder at current level, got ' + firstSummaryText);
  if (!first.rightOnly || first.rightOnly < 1) fail('expected right-only folder at current level, got ' + firstSummaryText);
  await page.screenshot({ path: path.join(SHOT, 'folder-scan.png') });

  async function cover(action, shotName) {
    await page.click('[data-action="' + action + '"]');
    await page.waitForSelector('button:has-text("开始覆盖")');
    const title = await page.$eval('.cmp-source-title, .cmp-sync-dialog', el => el.innerText);
    if (title.indexOf('不删除') < 0) fail('cover preview missing no-delete wording: ' + title);
    await page.screenshot({ path: path.join(SHOT, shotName + '-preview.png') });
    await page.click('button:has-text("开始覆盖")');
    await page.waitForFunction(() => (document.body.innerText || '').indexOf('覆盖完成') >= 0, null, { timeout: 180000 });
    await page.waitForFunction(() => !document.querySelector('.cmp-source-overlay'), null, { timeout: 90000 });
    await page.waitForFunction(() => {
      const t = document.querySelector('.cmp-scan-progress');
      return t && t.textContent.indexOf('完成') === 0 && t.textContent.indexOf('仅左') >= 0;
    }, null, { timeout: 90000 });
    const summary = parseSummary(await page.$eval('.cmp-scan-progress', el => el.textContent));
    await page.screenshot({ path: path.join(SHOT, shotName + '.png') });
    return summary;
  }

  const afterRight = await cover('cover-right', 'cover-right');
  if (afterRight.leftOnly !== 0) fail('cover → should copy left-only files, summary=' + JSON.stringify(afterRight));
  const rightHasLeft = fs.existsSync(path.join(RIGHT, '仅左', 'left_001.txt'));
  if (!rightHasLeft) fail('right tree missing copied left_001.txt');

  const afterLeft = await cover('cover-left', 'cover-left');
  if (afterLeft.rightOnly !== 0) fail('← cover should copy right-only files, summary=' + JSON.stringify(afterLeft));
  const leftHasRight = fs.existsSync(path.join(LEFT, '仅右', 'right_001.txt'));
  if (!leftHasRight) fail('left tree missing copied right_001.txt');

  const chinese = fs.existsSync(path.join(LEFT, '中文 目录', '空格 文件.txt')) && fs.existsSync(path.join(RIGHT, '中文 目录', '空格 文件.txt'));
  if (!chinese) fail('missing Chinese-path fixture after cover');

  console.log(JSON.stringify({ first, afterRight, afterLeft, shot: SHOT }));
  await browser.close();
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
