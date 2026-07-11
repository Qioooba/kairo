/* perf-about-lazy.js — 验证 IO 懒渲染是否真生效
 *
 * 不等待 200ms 兜底, 立即采样首屏节点数 (期望 = 4 块首屏 + 12 placeholder ≈ 500-600 节点)
 * 然后滚到中部, 看节点数增长 (期望 = 800-1000)
 * 滚到底, 节点数 (期望 ≈ 改前 3187)
 */
const { chromium } = require('playwright');

const URL = 'http://127.0.0.1:18092/#/about';

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(e.message));
  page.on('console', m => { if (m.type() === 'error') errs.push('console.error: ' + m.text()); });

  await page.goto(URL, { waitUntil: 'domcontentloaded' });

  // T+50ms: 立即采样, 看 IO 触发前的真实节点数
  await page.waitForTimeout(50);
  const s50 = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazyMounted: Array.from(document.querySelectorAll('.about-lazy-section')).map(p => p._lazyMounted === true).filter(Boolean).length
  }));
  console.log(`T+50ms   : dom=${s50.dom} view=${s50.view} lazyMounted=${s50.lazyMounted}/12`);

  // T+200ms: 兜底已经触发
  await page.waitForTimeout(150);
  const s200 = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazyMounted: Array.from(document.querySelectorAll('.about-lazy-section')).map(p => p._lazyMounted === true).filter(Boolean).length
  }));
  console.log(`T+200ms  : dom=${s200.dom} view=${s200.view} lazyMounted=${s200.lazyMounted}/12`);

  // T+1000ms: 完全稳定
  await page.waitForTimeout(800);
  const s1000 = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazyMounted: Array.from(document.querySelectorAll('.about-lazy-section')).map(p => p._lazyMounted === true).filter(Boolean).length
  }));
  console.log(`T+1000ms : dom=${s1000.dom} view=${s1000.view} lazyMounted=${s1000.lazyMounted}/12`);

  // 滚到中部, 看 IO 触发了哪些
  await page.evaluate(() => window.scrollTo(0, 3000));
  await page.waitForTimeout(300);
  const sMid = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazyMounted: Array.from(document.querySelectorAll('.about-lazy-section')).map(p => p._lazyMounted === true).filter(Boolean).length
  }));
  console.log(`scroll3000: dom=${sMid.dom} view=${sMid.view} lazyMounted=${sMid.lazyMounted}/12`);

  // 滚到底
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await page.waitForTimeout(300);
  const sEnd = await page.evaluate(() => ({
    dom: document.querySelectorAll('*').length,
    view: document.querySelectorAll('#view *').length,
    lazyMounted: Array.from(document.querySelectorAll('.about-lazy-section')).map(p => p._lazyMounted === true).filter(Boolean).length
  }));
  console.log(`scrollEnd: dom=${sEnd.dom} view=${sEnd.view} lazyMounted=${sEnd.lazyMounted}/12`);

  // 验证 anchor 跳转
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(200);
  const anchorRes = await page.evaluate(() => {
    const a = document.querySelector('a[href="#sec-history"]');
    if (!a) return { error: 'no anchor' };
    a.click();
    return new Promise(res => setTimeout(() => res({ ok: true, scrollY: window.scrollY }), 500));
  });
  console.log(`anchor click: ${JSON.stringify(anchorRes)}`);

  console.log(`\nerrors: ${errs.length}`);
  errs.slice(0, 5).forEach(e => console.log('  ' + e));

  await browser.close();
})();
