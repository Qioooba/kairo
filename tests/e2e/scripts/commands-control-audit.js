'use strict';

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUT = path.resolve(__dirname, '../../../test-results/v014-v017-audit');
const SHOTS = path.join(OUT, 'commands-controls');
const slug = s => String(s || 'control').replace(/[^\w\u4e00-\u9fff-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80) || 'control';
const visible = async loc => loc.isVisible().catch(() => false);

async function closeTransient(page) {
  for (const selector of ['.http2-modal-mask', '.modal-overlay', '.kairo-modal-overlay', '.kairo-dialog-overlay']) {
    const xs = page.locator(selector);
    for (let i = 0; i < await xs.count(); i++) {
      const x = xs.nth(i);
      if (!await visible(x)) continue;
      const close = x.locator('button').filter({ hasText: /^(关闭|取消|返回|稍后)$/ }).last();
      if (await visible(close)) await close.click({ timeout: 800 }).catch(() => {});
      else await page.keyboard.press('Escape').catch(() => {});
    }
  }
}

(async () => {
  fs.mkdirSync(SHOTS, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1366, height: 768 }, locale: 'zh-CN' });
  page.setDefaultTimeout(1500);
  const dialogs = [];
  page.on('dialog', async d => { dialogs.push({ type: d.type(), message: d.message() }); await d.dismiss().catch(() => {}); });
  const errors = [];
  await page.goto(`${BASE_URL}/#/commands`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  const count = await page.locator('#view button').count();
  const result = { generatedAt: new Date().toISOString(), route: 'commands', viewport: { width: 1366, height: 768 }, total: count, passed: 0, skipped: 0, failed: 0, errors: [], screenshots: 0 };
  for (let i = 0; i < count; i++) {
    await closeTransient(page);
    const target = page.locator('#view button').nth(i);
    const info = await target.evaluate(e => ({ text: (e.innerText || e.title || '').trim().slice(0, 80), title: e.title || '', disabled: Boolean(e.disabled), visible: (() => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0; })() })).catch(() => ({ text: '', title: '', disabled: false, visible: false }));
    if (!info.visible || info.disabled) { result.skipped++; continue; }
    try {
      await target.scrollIntoViewIfNeeded();
      await target.click();
      await page.waitForTimeout(20);
      await page.screenshot({ path: path.join(SHOTS, `${String(i).padStart(3, '0')}-${slug(info.title || info.text)}.png`), fullPage: false });
      result.passed++; result.screenshots++;
    } catch (e) {
      result.failed++; const item = { index: i, ...info, error: String(e.message || e) }; result.errors.push(item); errors.push(item);
    }
  }
  result.dialogs = dialogs;
  fs.writeFileSync(path.join(OUT, 'commands-control-audit.json'), JSON.stringify(result, null, 2), 'utf8');
  console.log(JSON.stringify({ output: path.join(OUT, 'commands-control-audit.json'), total: result.total, passed: result.passed, skipped: result.skipped, failed: result.failed, screenshots: result.screenshots, errors: errors.slice(0, 5) }, null, 2));
  await browser.close();
})().catch(e => { console.error(e.stack || e.message || e); process.exit(1); });
