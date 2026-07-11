/* perf-about-fallback.js — 验证降级路径
 *
 * 模拟两种老浏览器:
 *   1. 无 IntersectionObserver (远古浏览器)
 *   2. 无 content-visibility (Chrome 60-84)
 * 期望: 两种情况都正常渲染, 行为等同旧版, 0 错误
 */
const { chromium } = require('playwright');

async function runWithOverride(browser, label, initScript) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errs.push('console.error: ' + m.text()); });

  await page.addInitScript(initScript);
  await page.goto('http://127.0.0.1:18092/#/about', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const r = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    hasLazySection: document.querySelectorAll('.about-lazy-section').length,
    sectionsRendered: Array.from(document.querySelectorAll('[id^="sec-"]')).map(s => s.id),
    cssSupportsCV: (typeof CSS !== 'undefined') && CSS.supports && CSS.supports('content-visibility', 'auto')
  }));
  await ctx.close();
  return { label, r, errors: errs };
}

(async () => {
  const browser = await chromium.launch({ headless: true });

  // 1. 删掉 IntersectionObserver 模拟远古浏览器
  const r1 = await runWithOverride(browser, 'NO-IO', () => {
    // 删全局 IO
    window.IntersectionObserver = undefined;
  });

  // 2. 删掉 CSS.supports 模拟老 Chrome
  const r2 = await runWithOverride(browser, 'NO-CSS-SUPPORTS', () => {
    if (window.CSS && window.CSS.supports) {
      const old = window.CSS.supports;
      window.CSS.supports = function (prop, val) {
        if (prop === 'content-visibility') return false;
        return old.apply(this, arguments);
      };
    }
  });

  await browser.close();

  console.log('=== 降级路径验证 ===\n');
  for (const r of [r1, r2]) {
    console.log(`[${r.label}]`);
    console.log(`  view 节点     : ${r.r.view} (期望 ≈ 3200, 等同旧版)`);
    console.log(`  hasLazySection: ${r.r.hasLazySection}`);
    console.log(`  cssSupportsCV : ${r.r.cssSupportsCV}`);
    console.log(`  sections 渲染 : ${r.r.sectionsRendered.length}/13 (期望 13)`);
    console.log(`  控制台错误    : ${r.errors.length}`);
    if (r.errors.length) r.errors.forEach(e => console.log(`    ${e}`));
    console.log();
  }
})();
