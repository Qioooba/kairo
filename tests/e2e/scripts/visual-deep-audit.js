'use strict';

/**
 * Detailed responsive/theme audit for the screenshots produced by
 * matrix-capture.js. It does not mutate application data and only changes
 * route, viewport, and theme through the visible theme button.
 */

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUTPUT_DIR = path.resolve(__dirname, '../../../test-results/v014-v017-audit');

const PAGES = [
  ['home', '首页'], ['websphere', '日志助手'], ['files', '文件下载'],
  ['waspack', '投产打包'], ['ssh', 'SSH终端'], ['database', '数据库工作台'],
  ['http', 'HTTP测试'], ['webservice', 'WebService'], ['wscodegen', 'WebService代码生成'],
  ['diagnostics', '环境自检'], ['config', '系统配置'], ['downloads', '下载历史'],
  ['formatter', '报文格式化'], ['timestamp', '时间戳'], ['cron', 'Cron解析'],
  ['jsonpath', 'JSONPath'], ['compare', '代码比对'], ['commands', '常用命令'],
  ['notes/list', '便笺'], ['notes/reminders', '便笺提醒'], ['tasks', '定时任务'],
  ['sponsor', '投喂作者'], ['about', '关于'],
];
const THEMES = ['dark', 'light', 'green', 'hc', 'xianxia'];
const VIEWPORTS = [
  ['1920x1080', 1920, 1080], ['1366x768', 1366, 768],
  ['1024x768', 1024, 768], ['768x1024', 768, 1024], ['375x812', 375, 812],
];

function ensureDir(dir) { fs.mkdirSync(dir, { recursive: true }); }

async function ready(page, route) {
  await page.goto(BASE_URL + '/#/' + route, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(350);
  await page.waitForFunction(() => {
    const view = document.getElementById('view');
    return Boolean(view && view.children.length && view.innerText.trim());
  }, null, { timeout: 6000 }).catch(() => {});
}

async function setThemeWithUI(page, theme) {
  const toggle = page.locator('#theme-toggle');
  for (let i = 0; i < 6; i++) {
    if (await page.evaluate(() => document.documentElement.getAttribute('data-theme')) === theme) return;
    await toggle.click();
    await page.waitForTimeout(60);
  }
  const actual = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
  if (actual !== theme) throw new Error(`主题切换失败：${theme} != ${actual}`);
}

async function collect(page, route, theme, res) {
  return page.evaluate(({ route, theme, res }) => {
    function visible(el) {
      const s = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      return s.display !== 'none' && s.visibility !== 'hidden' && Number(s.opacity || 1) > 0 && r.width > 0 && r.height > 0;
    }
    function labelFor(el) {
      const id = el.id;
      const labelled = el.getAttribute('aria-label') || el.getAttribute('title');
      const explicit = id && document.querySelector(`label[for="${CSS.escape(id)}"]`);
      const parentLabel = el.closest('label');
      const text = (explicit || parentLabel)?.innerText || '';
      return (labelled || text || el.getAttribute('placeholder') || id || el.tagName).replace(/\s+/g, ' ').trim().slice(0, 100);
    }
    function rgb(value) {
      const m = String(value || '').match(/rgba?\(([^)]+)\)/i);
      if (!m) return null;
      const p = m[1].split(',').map(x => Number.parseFloat(x.trim()));
      if (p.length < 3 || p.some(x => Number.isNaN(x))) return null;
      return [p[0], p[1], p[2], p.length > 3 && !Number.isNaN(p[3]) ? p[3] : 1];
    }
    function luminance(c) {
      const v = c.slice(0, 3).map(x => x / 255).map(x => x <= .03928 ? x / 12.92 : Math.pow((x + .055) / 1.055, 2.4));
      return .2126 * v[0] + .7152 * v[1] + .0722 * v[2];
    }
    function contrast(fg, bg) {
      if (!fg || !bg || fg[3] < .95 || bg[3] < .95) return null;
      const a = luminance(fg), b = luminance(bg);
      return (Math.max(a, b) + .05) / (Math.min(a, b) + .05);
    }
    function backgroundFor(el) {
      let node = el;
      while (node && node !== document.documentElement) {
        const s = getComputedStyle(node);
        const c = rgb(s.backgroundColor);
        if (c && c[3] >= .95) return c;
        node = node.parentElement;
      }
      return rgb(getComputedStyle(document.documentElement).backgroundColor) || [255, 255, 255, 1];
    }
    function rectInfo(el) {
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return {
        name: labelFor(el), tag: el.tagName.toLowerCase(), id: el.id || '',
        left: Math.round(r.left), top: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height),
        right: Math.round(r.right), bottom: Math.round(r.bottom),
        fontSize: s.fontSize, color: s.color, backgroundColor: s.backgroundColor,
      };
    }

    const root = document.documentElement;
    const view = document.getElementById('view');
    const main = document.getElementById('main');
    const sidebar = document.querySelector('.sidebar');
    const allControls = Array.from(document.querySelectorAll('#view button, #view input, #view textarea, #view select, #view [role="button"]')).filter(visible);
    const criticalControls = allControls.map(rectInfo);
    const textboxes = Array.from(document.querySelectorAll('#view input:not([type="hidden"]):not([type="button"]):not([type="submit"]):not([type="reset"]):not([type="checkbox"]):not([type="radio"]):not([type="file"]), #view textarea, #view [contenteditable="true"]')).filter(visible);
    const missingLabels = textboxes.filter(el => !el.getAttribute('aria-label') && !el.getAttribute('title') && !el.getAttribute('placeholder') && !(el.id && document.querySelector(`label[for="${CSS.escape(el.id)}"]`)) && !el.closest('label')).map(rectInfo);
    const altMissing = Array.from(document.querySelectorAll('#view img')).filter(el => visible(el) && !el.getAttribute('alt')).map(rectInfo);
    const offscreenControls = allControls.filter(el => {
      const r = el.getBoundingClientRect();
      return r.left < -1 || r.right > innerWidth + 1;
    }).map(rectInfo);
    const horizontalTextOverflow = Array.from(document.querySelectorAll('#view *')).filter(el => {
      if (!visible(el) || !el.innerText || el.children.length > 0) return false;
      const s = getComputedStyle(el);
      return el.scrollWidth > el.clientWidth + 3 && s.overflowX !== 'hidden' && s.textOverflow !== 'ellipsis';
    }).slice(0, 30).map(rectInfo);
    const smallTargets = allControls.filter(el => {
      const r = el.getBoundingClientRect();
      return res.width <= 768 && (r.width < 40 || r.height < 36);
    }).slice(0, 60).map(rectInfo);
    const contrastIssues = [];
    const contrastTargets = Array.from(document.querySelectorAll('#view button, #view input, #view textarea, #view select, #view label, #view .muted, #view .nav-item')).filter(visible);
    for (const el of contrastTargets) {
      const s = getComputedStyle(el);
      if (!el.innerText && !el.value && el.tagName !== 'INPUT' && el.tagName !== 'TEXTAREA') continue;
      if (s.backgroundImage && s.backgroundImage !== 'none') continue;
      const ratio = contrast(rgb(s.color), backgroundFor(el));
      if (ratio !== null && ratio < 4.5) contrastIssues.push({ ...rectInfo(el), ratio: Number(ratio.toFixed(2)) });
    }
    const mainRect = main ? main.getBoundingClientRect() : null;
    const sidebarRect = sidebar ? sidebar.getBoundingClientRect() : null;
    const layoutIssues = [];
    if (root.scrollWidth > root.clientWidth + 2) layoutIssues.push({ type: 'HORIZONTAL_OVERFLOW', detail: `${root.scrollWidth} > ${root.clientWidth}` });
    if (res.width <= 600 && mainRect && mainRect.width < 320) layoutIssues.push({ type: 'MOBILE_MAIN_TOO_NARROW', detail: `主区宽度 ${Math.round(mainRect.width)}px，侧栏未收起或未提供抽屉导航` });
    if (res.width <= 600 && sidebarRect && sidebarRect.width >= res.width * .45) layoutIssues.push({ type: 'MOBILE_SIDEBAR_DOMINATES', detail: `侧栏宽度 ${Math.round(sidebarRect.width)}px，占视口 ${res.width}px 的 ${Math.round(sidebarRect.width / res.width * 100)}%` });
    if (!view || !view.innerText.trim()) layoutIssues.push({ type: 'EMPTY_VIEW', detail: '#view 为空' });
    return {
      route, theme, resolution: res.id,
      viewport: { width: innerWidth, height: innerHeight, clientWidth: root.clientWidth, scrollWidth: root.scrollWidth },
      main: mainRect ? { left: Math.round(mainRect.left), width: Math.round(mainRect.width) } : null,
      sidebar: sidebarRect ? { width: Math.round(sidebarRect.width) } : null,
      counts: { controls: allControls.length, textboxes: textboxes.length, missingLabels: missingLabels.length, altMissing: altMissing.length, offscreenControls: offscreenControls.length, textOverflow: horizontalTextOverflow.length, smallTargets: smallTargets.length, contrastIssues: contrastIssues.length },
      layoutIssues, missingLabels, altMissing, offscreenControls, horizontalTextOverflow, smallTargets, contrastIssues,
      controls: criticalControls,
    };
  }, { route, theme, res: { id: res[0], width: res[1], height: res[2] } });
}

async function main() {
  ensureDir(OUTPUT_DIR);
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1366, height: 768 }, locale: 'zh-CN' });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  const consoleErrors = [];
  const pageErrors = [];
  page.on('console', msg => { if (msg.type() === 'error') consoleErrors.push({ url: page.url(), text: msg.text() }); });
  page.on('pageerror', err => pageErrors.push({ url: page.url(), text: err.message }));

  const records = [];
  const start = Date.now();
  await ready(page, 'home');
  await setThemeWithUI(page, 'dark');
  for (const [route, pageName] of PAGES) {
    process.stdout.write(`页面 ${route} ...`);
    await ready(page, route);
    for (const theme of THEMES) {
      await setThemeWithUI(page, theme);
      for (const res of VIEWPORTS) {
        await page.setViewportSize({ width: res[1], height: res[2] });
        await page.waitForTimeout(80);
        try {
          records.push({ pageName, ...(await collect(page, route, theme, res)) });
        } catch (error) {
          records.push({ pageName, route, theme, resolution: res[0], error: error.message });
        }
      }
    }
    process.stdout.write(' 完成\n');
  }
  const findings = [];
  for (const record of records) {
    for (const issue of record.layoutIssues || []) findings.push({ ...record, issue });
    for (const item of record.missingLabels || []) findings.push({ ...record, issue: { type: 'MISSING_INPUT_LABEL', detail: item.name, element: item } });
    for (const item of record.altMissing || []) findings.push({ ...record, issue: { type: 'IMAGE_MISSING_ALT', detail: item.name, element: item } });
    for (const item of record.offscreenControls || []) findings.push({ ...record, issue: { type: 'CONTROL_OFFSCREEN', detail: item.name, element: item } });
    for (const item of record.horizontalTextOverflow || []) findings.push({ ...record, issue: { type: 'TEXT_OVERFLOW', detail: item.name, element: item } });
    for (const item of record.contrastIssues || []) findings.push({ ...record, issue: { type: 'LOW_CONTRAST', detail: `${item.name} ratio=${item.ratio}`, element: item } });
  }
  const summary = {
    records: records.length,
    expectedRecords: PAGES.length * THEMES.length * VIEWPORTS.length,
    screenshotsAvailable: PAGES.length * THEMES.length * VIEWPORTS.length,
    layoutFindingCount: findings.length,
    byType: findings.reduce((acc, item) => { const type = item.issue && item.issue.type; acc[type] = (acc[type] || 0) + 1; return acc; }, {}),
    pagesWithFindings: [...new Set(findings.map(item => item.route))],
    durationSec: Number(((Date.now() - start) / 1000).toFixed(1)),
    consoleErrors: consoleErrors.length,
    pageErrors: pageErrors.length,
  };
  const output = { generatedAt: new Date().toISOString(), baseUrl: BASE_URL, summary, findings, records, runtime: { consoleErrors, pageErrors } };
  fs.writeFileSync(path.join(OUTPUT_DIR, 'visual-deep-audit.json'), JSON.stringify(output, null, 2), 'utf8');
  console.log(JSON.stringify({ output: path.join(OUTPUT_DIR, 'visual-deep-audit.json'), summary }, null, 2));
  await context.close();
  await browser.close();
}

main().catch(error => { console.error(error.stack || error.message || String(error)); process.exit(1); });

