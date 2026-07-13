/* playwright-v0.14-card-check.js — 单独截 v0.14 changelog 卡片
 */
const { chromium } = require('playwright');
const path = require('path');

const URL = 'http://127.0.0.1:18093/#/about';
const OUT = path.resolve('./test-v0.14-about-out-archive');

(async () => {
  // 启动服务 (需要的话) — 这里假设服务已经在跑
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 1280, height: 1200 } });
  const p = await ctx.newPage();
  await p.goto(URL, { waitUntil: 'networkidle' });
  await p.waitForTimeout(800);

  // 1. 截首屏 (dark 主题)
  await p.screenshot({ path: path.join(OUT, 'screenshots', 'v014-dark-fold.png') });

  // 2. 找到 v0.14 卡片并展开
  const cardEl = await p.locator('#version-v0-14').first().elementHandle();
  if (cardEl) {
    // 找到 header 触发 onclick
    await p.evaluate(() => {
      const el = document.getElementById('version-v0-14');
      if (el) {
        el.style.maxHeight = el.scrollHeight + 'px';
        // 找 sibling header click
        const card = el.closest('[id^="version-"]') || el.parentElement;
      }
    });
    // 实际是点击 sibling header — 找 version-v0-14 的 container
    await p.evaluate(() => {
      const el = document.getElementById('version-v0-14');
      if (el) {
        el.style.maxHeight = el.scrollHeight + 200 + 'px';
      }
    });
    await p.waitForTimeout(500);
  }

  // 3. 滚到 v0.14 卡片并截图
  await p.evaluate(() => {
    const v014 = document.getElementById('version-v0-14');
    if (v014) v014.scrollIntoView({ block: 'start' });
  });
  await p.waitForTimeout(500);
  await p.screenshot({ path: path.join(OUT, 'screenshots', 'v014-dark-expanded.png') });

  // 4. 截 sponsor 卡片
  await p.evaluate(() => {
    const cards = Array.from(document.querySelectorAll('.card'));
    const sponsor = cards.find(c => c.textContent.includes('投喂作者 (v0.14 起)'));
    if (sponsor) sponsor.scrollIntoView({ block: 'center' });
  });
  await p.waitForTimeout(500);
  await p.screenshot({ path: path.join(OUT, 'screenshots', 'v014-sponsor-card.png') });

  // 5. 截路线图
  await p.evaluate(() => {
    const r = document.getElementById('sec-roadmap');
    if (r) r.scrollIntoView({ block: 'start' });
  });
  await p.waitForTimeout(500);
  await p.screenshot({ path: path.join(OUT, 'screenshots', 'v014-roadmap.png') });

  await b.close();
  console.log('done');
})();
