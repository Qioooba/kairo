'use strict';

// Stable, per-control audit. Each control is exercised from a freshly rendered
// route so a previous click cannot invalidate the next control's locator.
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUT = path.resolve(__dirname, '../../../test-results/v014-v017-audit');
const SHOTS = path.join(OUT, 'controls-v2');
const VIEWPORT = { width: 1366, height: 768 };
const PAGES = [
  ['home', '首页'], ['websphere', '日志助手'], ['files', '文件下载'],
  ['waspack', '投产打包'], ['ssh', 'SSH 终端'], ['database', '数据库工作台'],
  ['http', 'HTTP 测试'], ['webservice', 'WebService'],
  ['wscodegen', 'WebService 代码生成'], ['diagnostics', '环境自检'],
  ['config', '系统配置'], ['downloads', '下载历史'], ['formatter', '报文格式化'],
  ['timestamp', '时间戳'], ['cron', 'Cron 解析'], ['jsonpath', 'JSONPath'],
  ['compare', '文件与文本比较'], ['commands', '常用命令'],
  ['notes/list', '便笺'], ['notes/reminders', '便笺提醒'], ['tasks', '定时任务'],
  ['sponsor', '投喂作者'], ['about', '关于'],
];
const SELECTORS = {
  button: '#view button',
  'role-button': '#view [role="button"]:not(button)',
  select: '#view select',
  checkable: '#view input[type="checkbox"], #view input[type="radio"]',
  textbox: '#view input[type="text"], #view input[type="password"], #view input[type="number"], #view input[type="search"], #view input[type="url"], #view input[type="email"], #view input[type="tel"], #view input[type="time"], #view input[type="date"], #view input[type="datetime-local"], #view textarea, #view [contenteditable="true"]',
};
const DESTRUCTIVE = /删除|清空|重置|停止|放弃|取消全部|终止|退出|格式化|恢复出厂|清除|卸载|Kill|Delete|Reset|Drop/;

function ensure(dir) { fs.mkdirSync(dir, { recursive: true }); }
function slug(s) { return String(s || 'control').replace(/[^\w\u4e00-\u9fff-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80) || 'control'; }
function url(route) { return `${BASE_URL}/#/${route}`; }

async function instrument(page) {
  return page.evaluate((selectors) => {
    const out = [];
    const visible = el => {
      const s = getComputedStyle(el), r = el.getBoundingClientRect();
      return s.display !== 'none' && s.visibility !== 'hidden' && Number(s.opacity || 1) > 0 && r.width > 0 && r.height > 0;
    };
    const name = el => (el.getAttribute('aria-label') || el.getAttribute('title') || el.innerText || el.textContent || el.getAttribute('placeholder') || el.id || el.tagName).replace(/\s+/g, ' ').trim().slice(0, 120);
    for (const [kind, selector] of Object.entries(selectors)) {
      Array.from(document.querySelectorAll(selector)).forEach((el, index) => {
        const id = `qa-${kind}-${index}`;
        el.setAttribute('data-qa-control', id);
        const r = el.getBoundingClientRect();
        out.push({ id, kind, index, name: name(el), tag: el.tagName.toLowerCase(), type: el.getAttribute('type') || '', disabled: Boolean(el.disabled), readOnly: Boolean(el.readOnly), visible: visible(el), left: Math.round(r.left), top: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) });
      });
    }
    return out;
  }, SELECTORS);
}

async function ready(page, route, state) {
  state.resetting = true;
  await page.goto(url(route), { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(450);
  await page.waitForFunction(() => Boolean(document.querySelector('#view')?.innerText?.trim()), null, { timeout: 7000 }).catch(() => {});
  state.resetting = false;
  return instrument(page);
}

async function setDark(page) {
  const toggle = page.locator('#theme-toggle');
  for (let i = 0; i < 6; i++) {
    const current = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    if (current === 'dark') return;
    await toggle.click().catch(() => {});
    await page.waitForTimeout(60);
  }
}

async function closeTransient(page) {
  for (let i = 0; i < 3; i++) {
    const overlays = page.locator('.modal-overlay, .kairo-modal-overlay, .kairo-dialog-overlay, .dialog-overlay, [class*="modal-overlay"], [class*="dialog-overlay"]');
    let closed = false;
    for (let n = 0; n < await overlays.count(); n++) {
      const ov = overlays.nth(n);
      if (!await ov.isVisible().catch(() => false)) continue;
      const btn = ov.locator('button').filter({ hasText: /^(关闭|取消|返回|稍后)$/ }).last();
      if (await btn.isVisible().catch(() => false)) await btn.click({ timeout: 800 }).catch(() => {});
      else await page.keyboard.press('Escape').catch(() => {});
      closed = true;
    }
    if (!closed) break;
    await page.waitForTimeout(80);
  }
}

async function main() {
  ensure(OUT); ensure(SHOTS);
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: VIEWPORT, locale: 'zh-CN' });
  const page = await context.newPage();
  page.setDefaultTimeout(1600);
  const state = { resetting: false };
  const runtime = { consoleErrors: [], pageErrors: [], badResponses: [], requestFailures: [], dialogs: [] };
  page.on('console', m => { if (m.type() === 'error') runtime.consoleErrors.push({ route: page.url(), text: m.text() }); });
  page.on('pageerror', e => runtime.pageErrors.push({ route: page.url(), text: e.message }));
  page.on('response', r => { if (r.status() >= 400) runtime.badResponses.push({ url: r.url(), status: r.status() }); });
  page.on('requestfailed', r => runtime.requestFailures.push({ url: r.url(), error: r.failure()?.errorText || '' }));
  page.on('dialog', async d => {
    runtime.dialogs.push({ type: d.type(), message: d.message() });
    if (state.resetting && d.type() === 'confirm' && /未保存的改动.*离开/.test(d.message())) await d.accept().catch(() => {});
    else await d.dismiss().catch(() => {});
  });

  const report = { generatedAt: new Date().toISOString(), baseUrl: BASE_URL, viewport: VIEWPORT, pages: [], controls: [], runtime };
  for (const [route, pageName] of PAGES) {
    const inventory = await ready(page, route, state);
    await setDark(page);
    const visible = inventory.filter(x => x.visible);
    report.pages.push({ route, pageName, inventory: inventory.length, visible: visible.length });
    for (const info of visible) {
      const record = { route, pageName, ...info, status: null, error: null, screenshot: null };
      const dir = path.join(SHOTS, slug(route)); ensure(dir);
      if (info.disabled || info.readOnly) { record.status = 'skipped-disabled-readonly'; report.controls.push(record); continue; }
      if ((info.kind === 'button' || info.kind === 'role-button') && DESTRUCTIVE.test(info.name)) { record.status = 'guarded-destructive'; report.controls.push(record); continue; }
      try {
        await ready(page, route, state); await setDark(page);
        const target = page.locator(`[data-qa-control="${info.id}"]`).first();
        await target.scrollIntoViewIfNeeded();
        if (info.kind === 'button' || info.kind === 'role-button') {
          await target.click(); await page.waitForTimeout(100); await closeTransient(page); record.status = 'passed';
        } else if (info.kind === 'textbox') {
          const value = info.type === 'number' ? '7' : info.type === 'time' ? '10:10' : info.type === 'date' ? '2026-09-02' : 'Kairo UI audit';
          if (info.tag === 'input' || info.tag === 'textarea') { await target.fill(value); const actual = await target.inputValue(); if (info.type !== 'number' && actual !== value) throw new Error(`输入值未保留: ${actual}`); }
          else { await target.click(); await page.keyboard.press('Control+A'); await page.keyboard.type(value); }
          record.status = 'passed';
        } else if (info.kind === 'select') {
          const values = await target.locator('option').evaluateAll(xs => xs.map(x => x.value));
          const original = await target.inputValue(); const candidate = values.find(x => x !== original);
          if (candidate !== undefined) await target.selectOption(candidate);
          record.status = 'passed';
        } else if (info.kind === 'checkable') {
          const before = await target.isChecked(); await target.click(); const after = await target.isChecked();
          if (before === after) throw new Error('勾选状态未发生变化');
          record.status = 'passed';
        }
        const file = path.join(dir, `${info.kind}-${info.index}-${record.status}-${slug(info.name)}.png`);
        await page.screenshot({ path: file, fullPage: false }); record.screenshot = file;
      } catch (e) {
        record.status = 'failed'; record.error = String(e.message || e);
        const file = path.join(dir, `${info.kind}-${info.index}-failed-${slug(info.name)}.png`);
        await page.screenshot({ path: file, fullPage: false }).catch(() => {}); record.screenshot = file;
      }
      report.controls.push(record);
    }
    console.log(`${route}: ${visible.length} visible controls processed`);
  }
  report.summary = report.controls.reduce((a, x) => { a.total++; a[x.status] = (a[x.status] || 0) + 1; a[x.kind] = (a[x.kind] || 0) + 1; return a; }, { total: 0 });
  fs.writeFileSync(path.join(OUT, 'control-audit-v2.json'), JSON.stringify(report, null, 2), 'utf8');
  console.log(JSON.stringify({ output: path.join(OUT, 'control-audit-v2.json'), summary: report.summary, runtime: { consoleErrors: runtime.consoleErrors.length, pageErrors: runtime.pageErrors.length, badResponses: runtime.badResponses.length, requestFailures: runtime.requestFailures.length } }, null, 2));
  await browser.close();
}
main().catch(e => { console.error(e.stack || e.message || e); process.exit(1); });
