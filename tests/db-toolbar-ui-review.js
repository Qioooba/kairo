#!/usr/bin/env node
'use strict';
/**
 * playwright-db-toolbar-review.js —— 数据库工作台工具条（紧凑单排 + 彩色图标）UI 自审截图
 *
 * 前置：本地已启动 kairo（默认 http://127.0.0.1:18092，可用 KAIRO_BASE 覆盖），
 *       且存在可用的数据库数据源（默认用 KAIRO_LAB Oracle 21c；可用 KAIRO_SOURCE 覆盖源 id）。
 *
 * 用法：
 *   node playwright-db-toolbar-review.js [视口宽度=1600]
 *
 * 产物（默认 test-results/db-toolbar-ui/）：
 *   columns-full.png / columns-editor-bar.png / columns-result-bar.png
 *   stacked-full.png / stacked-editor-bar.png / stacked-result-bar.png
 *   tip-format.png / more-panel.png / metrics.json
 *
 * 检查点（自动断言并写入 metrics.json）：
 *   - 编辑器工具条与结果工具条都必须只有一排（高度 ≤ 48px 且不换行）
 *   - 编辑器工具条不得出现横向溢出（「更多」下拉不能被滚动容器裁切）
 *   - 运行期无 JS 报错
 */
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.KAIRO_BASE || 'http://127.0.0.1:18092';
const SOURCE = process.env.KAIRO_SOURCE || 'db_7fb38c257ff16cbaa1a2c886';
const WIDTH = Number(process.argv[2] || 1600);
const OUT = path.join(__dirname, '..', 'test-results', 'db-toolbar-ui');

async function prep(page) {
  await page.goto(`${BASE}/#/database?source=${SOURCE}`, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#db-run', { timeout: 40000 });
  await page.waitForTimeout(1200);
  await page.fill('#db-sql', 'SELECT ID, NOTE FROM KAIRO_TX_TEST_0905');
  await page.click('#db-run');
  await page.waitForTimeout(2200);
}

async function isColumns(page) {
  return page.evaluate(() => {
    const l = document.getElementById('db-sql-layout');
    return !!(l && l.classList.contains('is-columns'));
  });
}

async function ensureLayout(page, wantColumns) {
  for (let i = 0; i < 4; i++) {
    if ((await isColumns(page)) === wantColumns) return true;
    await page.click('#db-layout-toggle').catch(() => {});
    await page.waitForTimeout(900);
  }
  return (await isColumns(page)) === wantColumns;
}

async function measure(page) {
  return page.evaluate(() => {
    const rect = sel => { const el = document.querySelector(sel); if (!el) return null; const b = el.getBoundingClientRect(); return { w: Math.round(b.width), h: Math.round(b.height) }; };
    const eb = document.querySelector('.db-editor-bar');
    const rb = document.querySelector('.db-result-toolbar');
    const visible = sel => { const el = document.querySelector(sel); return !!(el && el.offsetParent !== null && getComputedStyle(el).display !== 'none'); };
    return {
      columns: (() => { const l = document.getElementById('db-sql-layout'); return !!(l && l.classList.contains('is-columns')); })(),
      editorBar: rect('.db-editor-bar'),
      editorBarWrap: eb ? getComputedStyle(eb).flexWrap : null,
      editorBarOverflowPx: eb ? eb.scrollWidth - eb.clientWidth : null,
      resultBar: rect('.db-result-toolbar'),
      resultBarWrap: rb ? getComputedStyle(rb).flexWrap : null,
      resultBarOverflowPx: rb ? rb.scrollWidth - rb.clientWidth : null,
      tabs: rect('.db-editor-tabs'),
      visible: { pageHint: visible('#db-page-hint'), resultMeta: visible('#db-result-meta'), limits: visible('.db-bar-limits-group') },
      iconButtons: document.querySelectorAll('.db-editor-bar .db-ic-btn, .db-result-toolbar .db-ic-btn, .db-view-btn').length
    };
  });
}

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: WIDTH, height: 950 }, deviceScaleFactor: 2 });
  const errors = [];
  page.on('pageerror', e => errors.push(String((e && e.message) || e)));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });

  await prep(page);
  const startColumns = await isColumns(page);

  await ensureLayout(page, true);
  const columns = await measure(page);
  await page.screenshot({ path: path.join(OUT, 'columns-full.png') });
  const eb = await page.$('.db-editor-bar'); if (eb) await eb.screenshot({ path: path.join(OUT, 'columns-editor-bar.png') });
  const rb = await page.$('.db-result-toolbar'); if (rb) await rb.screenshot({ path: path.join(OUT, 'columns-result-bar.png') });

  await ensureLayout(page, false);
  const stacked = await measure(page);
  await page.screenshot({ path: path.join(OUT, 'stacked-full.png') });
  const eb2 = await page.$('.db-editor-bar'); if (eb2) await eb2.screenshot({ path: path.join(OUT, 'stacked-editor-bar.png') });
  const rb2 = await page.$('.db-result-toolbar'); if (rb2) await rb2.screenshot({ path: path.join(OUT, 'stacked-result-bar.png') });

  await page.hover('#db-format').catch(() => {});
  await page.waitForTimeout(350);
  const card = await page.$('.db-editor-card'); if (card) await card.screenshot({ path: path.join(OUT, 'tip-format.png') });
  await page.click('.db-toolbar-more-summary').catch(() => {});
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(OUT, 'more-panel.png'), clip: { x: 0, y: 100, width: Math.min(WIDTH, 1200), height: 660 } });

  // 恢复进入页面时的布局，避免改动用户偏好
  await ensureLayout(page, startColumns);

  const singleBar = (bar, wrap) => !!bar && bar.h <= 48 && wrap === 'nowrap';
  const checks = {
    editorBarSingleRow: {
      columns: singleBar(columns.editorBar, columns.editorBarWrap),
      stacked: singleBar(stacked.editorBar, stacked.editorBarWrap)
    },
    resultBarSingleRow: {
      columns: singleBar(columns.resultBar, columns.resultBarWrap),
      stacked: singleBar(stacked.resultBar, stacked.resultBarWrap)
    },
    editorBarNoOverflow: { columns: columns.editorBarOverflowPx === 0, stacked: stacked.editorBarOverflowPx === 0 },
    noPageErrors: errors.length === 0,
    layoutRestored: (await isColumns(page)) === startColumns
  };
  const report = { base: BASE, viewport: WIDTH, columns, stacked, errors, checks };
  fs.writeFileSync(path.join(OUT, 'metrics.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  const failed = Object.entries(checks).filter(([, v]) => (typeof v === 'object' ? Object.values(v).some(x => x === false) : v === false));
  await browser.close();
  if (failed.length) { console.error('UI 自审未通过：' + failed.map(([k]) => k).join(', ')); process.exit(1); }
  console.log('数据库工作台工具条 UI 自审通过，截图已输出到 ' + OUT);
})().catch(e => { console.error(e); process.exit(1); });
