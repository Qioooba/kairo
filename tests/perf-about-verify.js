/* perf-about-verify.js — 全面验证
 *
 * 测:
 *   1. 首屏节点数 (期望 ~ 改前 3187 → 改后 < 200)
 *   2. 200ms 兜底后全量 (期望 ≈ 改前 3187)
 *   3. 滚动后 view.scrollTop 真的变化 (anchor 跳转)
 *   4. 5 主题下都正常
 *   5. 折叠展开 changelog (用对的 selector)
 *   6. 0 控制台错误
 */
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const THEMES = ['dark', 'light', 'green', 'hc', 'xianxia'];

async function runTheme(browser, theme) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errs.push('console.error: ' + m.text()); });

  await page.addInitScript((t) => { try { localStorage.setItem('kairo_theme', t); } catch (e) {} }, theme);
  await page.goto('http://127.0.0.1:18092/#/about', { waitUntil: 'domcontentloaded' });

  // T+50ms: 真实首屏
  await page.waitForTimeout(50);
  const s50 = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazySectionsWithContent: Array.from(document.querySelectorAll('.about-lazy-section')).filter(p => p.children.length > 0).length
  }));

  // T+1000ms: 稳定
  await page.waitForTimeout(1000);
  const s1000 = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazySectionsWithContent: Array.from(document.querySelectorAll('.about-lazy-section')).filter(p => p.children.length > 0).length
  }));

  // anchor 跳转
  await page.evaluate(() => { const v = document.getElementById('view'); v.scrollTop = 0; });
  await page.waitForTimeout(200);
  await page.evaluate(() => {
    const a = document.querySelector('a[href="#sec-history"]');
    a.click();
  });
  await page.waitForTimeout(2000);
  const anchorR = await page.evaluate(() => ({ viewScrollTop: document.getElementById('view').scrollTop }));

  // 折叠展开 changelog (用对的 selector)
  await page.evaluate(() => { const v = document.getElementById('view'); v.scrollTop = 0; });
  await page.waitForTimeout(200);
  const expandR = await page.evaluate(() => {
    // el() 用 addEventListener 不是 setAttribute, 所以 onclick 属性不在 DOM 上
    // renderVersionCard: .card > div(header) + div(headlineRow) + div(body#version-xxx)
    const sec = document.getElementById('sec-history');
    if (!sec) return { error: 'no sec-history' };
    const cards = sec.querySelectorAll('.card');
    let clicked = 0;
    cards.forEach(c => {
      const first = c.firstElementChild;
      if (first) { first.click(); clicked++; }
    });
    return { cardsFound: cards.length, clicked };
  });
  await page.waitForTimeout(1500);
  const expandAfter = await page.evaluate(() => ({
    openCards: document.querySelectorAll('#sec-history [id^="version-"]').length,
    anyOpen: Array.from(document.querySelectorAll('#sec-history [id^="version-"]')).filter(b => {
      const s = b.style.maxHeight;
      return s && s !== '0px' && s !== '';
    }).length
  }));

  await ctx.close();
  return { theme, s50, s1000, anchorR, expandR, expandAfter, errors: errs.length, errs };
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const results = {};
  for (const t of THEMES) {
    try { results[t] = await runTheme(browser, t); }
    catch (e) { results[t] = { error: e.message }; }
  }
  await browser.close();

  console.log('\n=== 5 主题验证汇总 ===\n');
  for (const t of THEMES) {
    const r = results[t];
    if (r.error) { console.log(`${t}: ERROR ${r.error}`); continue; }
    console.log(`${t}:`);
    console.log(`  T+50ms       : view=${r.s50.view} (期望 < 300, IO 未触发)`);
    console.log(`  T+1000ms     : view=${r.s1000.view} (期望 ≈ 3200, 兜底已触发)`);
    console.log(`  anchor 跳转  : viewScrollTop=${r.anchorR.viewScrollTop} (期望 > 1000)`);
    console.log(`  展开测试     : headers=${r.expandR.headersFound} clicked=${r.expandR.clicked} anyOpen=${r.expandAfter.anyOpen}/${r.expandAfter.openCards}`);
    console.log(`  控制台错误   : ${r.errors}`);
    if (r.errs.length) r.errs.forEach(e => console.log(`    ${e}`));
  }
})();
