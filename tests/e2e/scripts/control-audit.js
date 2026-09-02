'use strict';

/**
 * v0.14 -> HEAD UI control audit.
 *
 * This is intentionally a plain Playwright script (not a test runner spec):
 * it exercises visible controls through locator actions and stores a compact
 * inventory plus one viewport screenshot after each exercised control.
 * Destructive controls are inspected and documented but never confirmed.
 */

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUTPUT_DIR = path.resolve(__dirname, '../../../test-results/v014-v017-audit');
const CONTROL_DIR = path.join(OUTPUT_DIR, 'controls');
const VIEWPORT = { width: 1366, height: 768 };

const PAGES = [
  ['home', '首页'],
  ['websphere', '日志助手'],
  ['files', '文件下载'],
  ['waspack', '投产打包'],
  ['ssh', 'SSH 终端'],
  ['database', '数据库工作台'],
  ['http', 'HTTP 测试'],
  ['webservice', 'WebService'],
  ['wscodegen', 'WebService 代码生成'],
  ['diagnostics', '环境自检'],
  ['config', '系统配置'],
  ['downloads', '下载历史'],
  ['formatter', '报文格式化'],
  ['timestamp', '时间戳'],
  ['cron', 'Cron 解析'],
  ['jsonpath', 'JSONPath'],
  ['compare', '文件与文本比较'],
  ['commands', '常用命令'],
  ['notes/list', '便笺'],
  ['notes/reminders', '便笺提醒'],
  ['tasks', '定时任务'],
  ['sponsor', '投喂作者'],
  ['about', '关于'],
];

const BUTTON_SELECTOR = '#view button';
const ROLE_BUTTON_SELECTOR = '#view [role="button"]:not(button)';
const SELECT_SELECTOR = '#view select';
const CHECKABLE_SELECTOR = '#view input[type="checkbox"], #view input[type="radio"]';
const TEXTBOX_SELECTOR = [
  '#view input[type="text"]',
  '#view input[type="password"]',
  '#view input[type="number"]',
  '#view input[type="search"]',
  '#view input[type="url"]',
  '#view input[type="email"]',
  '#view input[type="tel"]',
  '#view input[type="time"]',
  '#view input[type="date"]',
  '#view input[type="datetime-local"]',
  '#view textarea',
  '#view [contenteditable="true"]',
].join(',');

const DESTRUCTIVE = /删除|清空|重置|停止|放弃/;

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function slug(text) {
  return String(text || 'control')
    .replace(/[^\w\u4e00-\u9fff-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80) || 'control';
}

function routeUrl(route) {
  return BASE_URL + '/#/' + route;
}

async function ready(page, route) {
  await page.goto(routeUrl(route), { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(450);
  await page.waitForFunction(() => {
    const view = document.getElementById('view');
    return Boolean(view && view.children.length && view.innerText.trim());
  }, null, { timeout: 7000 }).catch(() => {});
}

async function setThemeWithUI(page, theme) {
  const toggle = page.locator('#theme-toggle');
  for (let i = 0; i < 6; i++) {
    const current = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    if (current === theme) return;
    await toggle.click();
    await page.waitForTimeout(80);
  }
  const actual = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
  if (actual !== theme) throw new Error(`主题切换失败，期望 ${theme}，实际 ${actual}`);
}

async function visible(locator) {
  try {
    return await locator.isVisible();
  } catch (_) {
    return false;
  }
}

async function controlInventory(page) {
  return page.evaluate(() => {
    function isVisible(el) {
      const style = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      return style.display !== 'none' && style.visibility !== 'hidden' &&
        Number(style.opacity || 1) > 0 && r.width > 0 && r.height > 0;
    }
    function name(el) {
      const labelled = el.getAttribute('aria-label') || el.getAttribute('title');
      const text = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
      const placeholder = el.getAttribute('placeholder') || '';
      return (labelled || text || placeholder || el.id || el.tagName).slice(0, 120);
    }
    function base(el, index, kind, selector) {
      const r = el.getBoundingClientRect();
      return {
        kind, selector, index, name: name(el), tag: el.tagName.toLowerCase(),
        type: el.getAttribute('type') || '', id: el.id || '',
        disabled: Boolean(el.disabled),
        width: Math.round(r.width), height: Math.round(r.height),
        left: Math.round(r.left), top: Math.round(r.top),
        visible: isVisible(el),
      };
    }
    const result = [];
    const add = (selector, kind) => Array.from(document.querySelectorAll(selector)).forEach((el, index) => {
      result.push(base(el, index, kind, selector));
    });
    add('#view button', 'button');
    add('#view [role="button"]:not(button)', 'role-button');
    add('#view select', 'select');
    add('#view input[type="checkbox"], #view input[type="radio"]', 'checkable');
    add('#view input[type="text"], #view input[type="password"], #view input[type="number"], #view input[type="search"], #view input[type="url"], #view input[type="email"], #view input[type="tel"], #view input[type="time"], #view input[type="date"], #view input[type="datetime-local"], #view textarea, #view [contenteditable="true"]', 'textbox');
    return result;
  });
}

async function closeTransient(page) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const overlays = page.locator('.modal-overlay, .kairo-modal-overlay, .kairo-dialog-overlay, .dialog-overlay, [class*="modal-overlay"], [class*="dialog-overlay"]');
    const count = await overlays.count();
    let closed = false;
    for (let i = 0; i < count; i++) {
      const overlay = overlays.nth(i);
      if (!await visible(overlay)) continue;
      const close = overlay.locator('button').filter({ hasText: /^(关闭|取消|返回|稍后)$/ }).last();
      if (await visible(close)) {
        await close.click().catch(() => {});
        closed = true;
      } else {
        await page.keyboard.press('Escape').catch(() => {});
        closed = true;
      }
    }
    if (!closed) break;
    await page.waitForTimeout(120);
  }
}

function isDestructive(name) {
  return DESTRUCTIVE.test(name || '');
}

function inputValueFor(info) {
  if (info.type === 'number') return '7';
  if (info.type === 'time') return '10:10';
  if (info.type === 'date') return '2026-09-02';
  if (info.type === 'datetime-local') return '2026-09-02T10:10';
  return 'Kairo UI audit';
}

async function getElement(page, info) {
  const locator = page.locator(info.selector).nth(info.index);
  await locator.scrollIntoViewIfNeeded().catch(() => {});
  return locator;
}

async function saveShot(page, dir, prefix) {
  ensureDir(dir);
  const target = path.join(dir, `${slug(prefix)}.png`);
  await page.screenshot({ path: target, fullPage: false });
  return target;
}

async function main() {
  ensureDir(OUTPUT_DIR);
  ensureDir(CONTROL_DIR);

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: VIEWPORT, locale: 'zh-CN' });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);

  const consoleErrors = [];
  const pageErrors = [];
  const requestFailures = [];
  const badResponses = [];
  const dialogs = [];
  page.on('console', msg => {
    if (msg.type() === 'error') consoleErrors.push({ url: page.url(), text: msg.text() });
  });
  page.on('pageerror', err => pageErrors.push({ url: page.url(), text: err.message }));
  page.on('requestfailed', req => requestFailures.push({ url: req.url(), error: req.failure() && req.failure().errorText }));
  page.on('response', res => {
    if (res.status() >= 400) badResponses.push({ url: res.url(), status: res.status() });
  });
  page.on('dialog', async dialog => {
    dialogs.push({ type: dialog.type(), message: dialog.message() });
    // Route changes can be blocked by the app's "unsaved config" guard after
    // an earlier control interaction. Accept only while ready() is restoring
    // the route; dismiss dialogs raised by the control under test.
    if (dialog.type() === 'confirm' && /未保存的改动.*离开/.test(dialog.message())) {
      await dialog.accept().catch(() => {});
    } else {
      await dialog.dismiss().catch(() => {});
    }
  });

  const report = {
    generatedAt: new Date().toISOString(),
    baseUrl: BASE_URL,
    viewport: VIEWPORT,
    themeSetThrough: 'visible #theme-toggle clicks',
    pages: [],
    controls: [],
    summary: {
      pages: PAGES.length,
      visibleButtons: 0,
      clickedButtons: 0,
      guardedDestructiveButtons: 0,
      disabledButtons: 0,
      failedButtons: 0,
      visibleRoleButtons: 0,
      visibleTextboxes: 0,
      exercisedTextboxes: 0,
      disabledTextboxes: 0,
      failedTextboxes: 0,
      visibleSelects: 0,
      exercisedSelects: 0,
      visibleCheckables: 0,
      exercisedCheckables: 0,
    },
  };

  await ready(page, 'home');
  await setThemeWithUI(page, 'dark');

  // Check every menu item with a real click; this also verifies route reachability.
  const navResults = [];
  const navItems = await page.locator('#nav .nav-item[data-route]').count();
  for (let i = 0; i < navItems; i++) {
    await ready(page, 'home');
    const nav = page.locator('#nav .nav-item[data-route]').nth(i);
    const route = await nav.getAttribute('data-route');
    const label = (await nav.innerText()).replace(/\s+/g, ' ').trim();
    try {
      await nav.click();
      await page.waitForTimeout(350);
      const actual = await page.evaluate(() => location.hash.replace(/^#\//, ''));
      navResults.push({ route, label, passed: actual === route || (route === 'notes' && actual === 'notes/list'), actual });
    } catch (error) {
      navResults.push({ route, label, passed: false, error: error.message });
    }
  }
  report.navigation = navResults;

  for (const [route, pageName] of PAGES) {
    await ready(page, route);
    await setThemeWithUI(page, 'dark');
    const inventory = await controlInventory(page);
    const visibleItems = inventory.filter(item => item.visible);
    report.pages.push({ route, name: pageName, inventoryCount: inventory.length, visibleCount: visibleItems.length });

    for (const info of inventory) {
      if (!info.visible) continue;
      const dir = path.join(CONTROL_DIR, slug(route));
      const key = `${route}-${info.kind}-${info.index}-${info.name}`;
      if (info.kind === 'button' || info.kind === 'role-button') {
        if (info.kind === 'button') report.summary.visibleButtons++;
        else report.summary.visibleRoleButtons++;
        if (info.disabled) {
          report.summary.disabledButtons++;
          report.controls.push({ route, pageName, ...info, status: 'disabled' });
          continue;
        }
        if (isDestructive(info.name)) {
          report.summary.guardedDestructiveButtons++;
          const screenshot = await saveShot(page, dir, `${info.kind}-${info.index}-guarded-${info.name}`);
          report.controls.push({ route, pageName, ...info, status: 'guarded-destructive', screenshot });
          continue;
        }
        let status = 'passed';
        let error = null;
        let screenshot = null;
        try {
          const locator = await getElement(page, info);
          await locator.click();
          await page.waitForTimeout(250);
          screenshot = await saveShot(page, dir, `${info.kind}-${info.index}-clicked-${info.name}`);
          report.summary.clickedButtons++;
          await closeTransient(page);
        } catch (e) {
          status = 'failed';
          error = e.message;
          report.summary.failedButtons++;
          screenshot = await saveShot(page, dir, `${info.kind}-${info.index}-failed-${info.name}`).catch(() => null);
        }
        report.controls.push({ route, pageName, ...info, status, error, screenshot, dialogCount: dialogs.length });
        await ready(page, route);
        await setThemeWithUI(page, 'dark');
      } else if (info.kind === 'textbox') {
        report.summary.visibleTextboxes++;
        if (info.disabled) {
          report.summary.disabledTextboxes++;
          report.controls.push({ route, pageName, ...info, status: 'disabled' });
          continue;
        }
        let status = 'passed';
        let error = null;
        let screenshot = null;
        try {
          const locator = await getElement(page, info);
          const original = info.tag === 'textarea' || info.tag === 'input' ? await locator.inputValue().catch(() => '') : await locator.textContent();
          const nextValue = inputValueFor(info);
          if (info.tag === 'input' || info.tag === 'textarea') {
            await locator.fill(nextValue);
            await locator.press('Tab').catch(() => {});
            const actual = await locator.inputValue();
            if (actual !== nextValue && info.type !== 'number') throw new Error(`输入值未保留，实际为 ${actual}`);
            screenshot = await saveShot(page, dir, `textbox-${info.index}-filled-${info.name}`);
            await locator.fill(original || '');
          } else {
            await locator.click();
            await page.keyboard.press('Control+A');
            await page.keyboard.type(nextValue);
            screenshot = await saveShot(page, dir, `textbox-${info.index}-filled-${info.name}`);
            await locator.fill(original || '');
          }
          report.summary.exercisedTextboxes++;
        } catch (e) {
          status = 'failed';
          error = e.message;
          report.summary.failedTextboxes++;
          screenshot = await saveShot(page, dir, `textbox-${info.index}-failed-${info.name}`).catch(() => null);
        }
        report.controls.push({ route, pageName, ...info, status, error, screenshot });
        await ready(page, route);
        await setThemeWithUI(page, 'dark');
      } else if (info.kind === 'select') {
        report.summary.visibleSelects++;
        if (info.disabled) {
          report.controls.push({ route, pageName, ...info, status: 'disabled' });
          continue;
        }
        let status = 'passed';
        let error = null;
        let screenshot = null;
        try {
          const locator = await getElement(page, info);
          const values = await locator.locator('option').evaluateAll(options => options.map(o => o.value));
          const original = await locator.inputValue();
          const candidate = values.find(value => value !== original);
          if (candidate !== undefined) {
            await locator.selectOption(candidate);
            screenshot = await saveShot(page, dir, `select-${info.index}-changed-${info.name}`);
            await locator.selectOption(original).catch(() => {});
          } else {
            screenshot = await saveShot(page, dir, `select-${info.index}-unchanged-${info.name}`);
          }
          report.summary.exercisedSelects++;
        } catch (e) {
          status = 'failed';
          error = e.message;
          screenshot = await saveShot(page, dir, `select-${info.index}-failed-${info.name}`).catch(() => null);
        }
        report.controls.push({ route, pageName, ...info, status, error, screenshot });
        await ready(page, route);
        await setThemeWithUI(page, 'dark');
      } else if (info.kind === 'checkable') {
        report.summary.visibleCheckables++;
        if (info.disabled) {
          report.controls.push({ route, pageName, ...info, status: 'disabled' });
          continue;
        }
        let status = 'passed';
        let error = null;
        let screenshot = null;
        try {
          const locator = await getElement(page, info);
          const original = await locator.isChecked();
          await locator.click();
          screenshot = await saveShot(page, dir, `checkable-${info.index}-toggled-${info.name}`);
          if (await locator.isChecked() === original) throw new Error('勾选状态未发生变化');
          if (await locator.isChecked() !== original) await locator.click().catch(() => {});
          report.summary.exercisedCheckables++;
        } catch (e) {
          status = 'failed';
          error = e.message;
          screenshot = await saveShot(page, dir, `checkable-${info.index}-failed-${info.name}`).catch(() => null);
        }
        report.controls.push({ route, pageName, ...info, status, error, screenshot });
        await ready(page, route);
        await setThemeWithUI(page, 'dark');
      }
    }
  }

  report.runtime = {
    consoleErrors,
    pageErrors,
    requestFailures,
    badResponses,
    dialogs,
  };
  fs.writeFileSync(path.join(OUTPUT_DIR, 'control-audit.json'), JSON.stringify(report, null, 2), 'utf8');
  console.log(JSON.stringify({
    output: path.join(OUTPUT_DIR, 'control-audit.json'),
    summary: report.summary,
    navigationFailures: navResults.filter(item => !item.passed).length,
    consoleErrors: consoleErrors.length,
    pageErrors: pageErrors.length,
    requestFailures: requestFailures.length,
    badResponses: badResponses.length,
  }, null, 2));
  await context.close();
  await browser.close();
}

main().catch(error => {
  console.error(error.stack || error.message || String(error));
  process.exit(1);
});
