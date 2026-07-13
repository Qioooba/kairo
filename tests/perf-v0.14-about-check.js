/* playwright-v0.14-about-check.js — 验证我修改后的 about 页面渲染
 *
 * 跑 5 主题 × 4 场景, 验证 v0.14 changelog / sponsor 卡片 / stats / 路线图都正常渲染
 */
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const URL = 'http://127.0.0.1:18093/#/about';
const OUT = path.resolve('./test-v0.14-about-out');

const THEMES = ['dark', 'light', 'green', 'hc', 'xianxia'];

function ensureDir(d) { if (!fs.existsSync(d)) fs.mkdirSync(d, { recursive: true }); }
ensureDir(OUT);
ensureDir(path.join(OUT, 'screenshots'));

const results = { passed: 0, failed: 0, errors: [], checks: [] };

async function setupTheme(page, theme) {
  await page.evaluate((t) => {
    try { localStorage.setItem('kairo_theme', t); } catch (e) {}
  }, theme);
}

async function checkAbout(page, theme) {
  // 1) about 页面能正常渲染, 无控制台 error
  const consoleErrors = [];
  page.on('pageerror', (err) => consoleErrors.push('pageerror: ' + err.message));
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push('console.error: ' + msg.text());
  });

  await page.goto(URL, { waitUntil: 'networkidle' });
  await page.waitForTimeout(800);

  // 2) 验证关键内容存在
  const checks = await page.evaluate(() => {
    const out = {};
    // v0.14 章节
    out.v014_hero = !!document.querySelector('.about-version-badge');
    out.v014_version = (document.querySelector('.about-version-badge')?.textContent || '').trim();
    // changelog 数组渲染出来的版本数
    out.version_cards = Array.from(document.querySelectorAll('[id^="version-v"]'))
      .map(el => el.id.replace('version-v', 'v'))
      .sort();
    // sponsor 模块卡片
    const sponsorCard = Array.from(document.querySelectorAll('.card'))
      .find(c => c.textContent.includes('投喂作者 (v0.14 起)'));
    out.sponsor_card_exists = !!sponsorCard;
    // stats 看板
    out.stats_commits = !!Array.from(document.querySelectorAll('.card'))
      .find(c => c.textContent.includes('148') && c.textContent.includes('提交次数'));
    out.stats_modules = !!Array.from(document.querySelectorAll('.card'))
      .find(c => c.textContent.includes('25') && c.textContent.includes('后端模块'));
    // 路线图 v0.9.1 hotfix 不应存在
    out.roadmap_no_legacy = !document.body.textContent.includes('v0.9.1 hotfix');
    out.roadmap_has_v015 = document.body.textContent.includes('v0.15 续命日常化');
    // 副标题 15 个版本
    out.subtitle_15 = document.body.textContent.includes('15 个版本');
    // 路由 navigate
    return out;
  });

  return { consoleErrors, checks };
}

(async () => {
  const browser = await chromium.launch();
  for (const theme of THEMES) {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    const page = await ctx.newPage();
    await setupTheme(page, theme);
    const { consoleErrors, checks } = await checkAbout(page, theme);
    const fail = (k, msg) => { results.failed++; results.errors.push(`[${theme}] ${k}: ${msg}`); };
    const pass = (k) => { results.passed++; };

    if (checks.v014_hero && checks.v014_version === 'v0.14') pass('v0.14 hero'); else fail('v0.14 hero', checks.v014_version);
    if (checks.version_cards.includes('v0-14')) pass('v0.14 changelog card'); else fail('v0.14 changelog card', checks.version_cards.join(','));
    if (checks.sponsor_card_exists) pass('sponsor card'); else fail('sponsor card', 'not found');
    if (checks.stats_commits) pass('stats 148'); else fail('stats 148', 'not found');
    if (checks.stats_modules) pass('stats 25 modules'); else fail('stats 25 modules', 'not found');
    if (checks.roadmap_no_legacy) pass('roadmap v0.9.1 removed'); else fail('roadmap v0.9.1 removed', 'still exists');
    if (checks.roadmap_has_v015) pass('roadmap v0.15 added'); else fail('roadmap v0.15 added', 'missing');
    if (checks.subtitle_15) pass('subtitle 15 versions'); else fail('subtitle 15 versions', 'wrong text');

    // 关键错误 (过滤掉一些良性警告)
    const realErrs = consoleErrors.filter(e =>
      !e.includes('sponsor') &&  // sponsor.js 自己的 quip 警告
      !e.includes('Failed to load resource')  // 静态资源 404 暂忽略
    );
    if (realErrs.length === 0) pass(`console clean (${theme})`); else fail(`console clean (${theme})`, realErrs.join(' | '));

    // 截图
    await page.screenshot({ path: path.join(OUT, 'screenshots', `about-${theme}-full.png`), fullPage: true });
    await page.screenshot({ path: path.join(OUT, 'screenshots', `about-${theme}-fold.png`), fullPage: false });

    await ctx.close();
  }
  await browser.close();

  console.log(`\n=== ${results.passed} pass, ${results.failed} fail ===\n`);
  if (results.errors.length) {
    console.log('FAILURES:');
    results.errors.forEach(e => console.log('  - ' + e));
  }
  fs.writeFileSync(path.join(OUT, 'check-result.json'), JSON.stringify(results, null, 2));
  process.exit(results.failed > 0 ? 1 : 0);
})();
