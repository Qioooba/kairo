'use strict';

const { chromium } = require('playwright');

const BASE = 'http://127.0.0.1:18092';

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1366, height: 900 }, locale: 'zh-CN' });
  const page = await context.newPage();

  const consoleMessages = [];
  const pageErrors = [];

  page.on('console', (msg) => {
    const text = msg.text();
    const lower = text.toLowerCase();
    const isElectron = lower.includes('electron') || lower.includes('preload') || lower.includes('favicon') || lower.includes('devtools');
    if (msg.type() === 'error' && !isElectron) {
      consoleMessages.push({ type: msg.type(), text, url: page.url() });
    }
  });
  page.on('pageerror', (err) => {
    pageErrors.push({ message: err.message, url: page.url() });
  });
  page.on('dialog', async (d) => { await d.dismiss(); });

  console.log('1. 打开首页');
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);

  console.log('\n2. 检查首页所有工具卡片');
  const cards = await page.$$eval('.tool-card', els => els.map(el => ({
    name: el.querySelector('.name')?.textContent,
    desc: el.querySelector('.desc')?.textContent,
    ariaLabel: el.getAttribute('aria-label'),
  })));
  console.log('首页卡片:', cards.map(c => c.name).join(', '));

  console.log('\n3. 点击"操作历史"卡片');
  const historyCard = await page.$('.tool-card[aria-label="操作历史"]');
  if (historyCard) {
    await historyCard.click();
    await page.waitForTimeout(800);
    const currentHash = await page.evaluate(() => location.hash);
    console.log('当前hash:', currentHash);

    // v1.x 去顶栏后不再有面包屑 #crumbs，改为读 Tab 条的活动 Tab 文案
    const crumb = await page.$eval('.app-tab.active .app-tab-label', el => el.textContent.trim());
    console.log('当前页(Tab):', crumb);

    const navActive = await page.$$eval('#nav .nav-item.active', els => els.map(el => el.textContent.trim()));
    console.log('导航栏active项:', navActive);

    const allNavItems = await page.$$eval('#nav .nav-item', els => els.map(el => ({
      text: el.textContent.trim(),
      route: el.getAttribute('data-route'),
      href: el.getAttribute('href'),
      visible: el.offsetParent !== null,
    })));
    console.log('导航栏所有可见项:', allNavItems.filter(n => n.visible).map(n => `${n.text}(${n.route})`).join(', '));

    const historyNavExists = allNavItems.find(n => n.route === 'history');
    console.log('导航栏中history项:', historyNavExists ? '存在' : '不存在(已被注释/隐藏)');

    const viewContent = await page.$eval('#view', el => ({
      hasH3: el.querySelector('h3') !== null,
      h3Text: el.querySelector('h3')?.textContent,
      hasTable: el.querySelector('table') !== null,
      hasButtons: el.querySelectorAll('button').length,
      buttonTexts: Array.from(el.querySelectorAll('button')).map(b => b.textContent.trim()).filter(t => t),
      textSnippet: el.innerText.substring(0, 300),
    }));
    console.log('页面内容:', JSON.stringify(viewContent, null, 2));
  } else {
    console.log('未找到操作历史卡片');
  }

  console.log('\n4. 检查所有hash路由直接访问');
  const routes = ['home','websphere','files','formatter','http','commands','diagnostics','config','downloads','timestamp','cron','jsonpath','compare','about','history'];
  for (const r of routes) {
    await page.evaluate((rt) => { location.hash = '#/' + rt; }, r);
    await page.waitForTimeout(400);
    const hash = await page.evaluate(() => location.hash);
    const crumb = await page.$eval('.app-tab.active .app-tab-label', el => el.textContent.trim()).catch(() => null);
    const viewHasChildren = await page.$eval('#view', el => el.children.length > 0).catch(() => false);
    const navActive = await page.$eval('#nav .nav-item.active', el => el.getAttribute('data-route')).catch(() => null);
    console.log(`#/${r}: hash=${hash}, crumb="${crumb}", hasContent=${viewHasChildren}, navActive=${navActive}`);
  }

  console.log('\n5. 检查是否有"记住密码"在非预期页面');
  for (const r of ['home', 'formatter', 'http', 'commands', 'diagnostics', 'config', 'downloads', 'timestamp', 'cron', 'jsonpath', 'compare', 'about', 'history']) {
    await page.evaluate((rt) => { location.hash = '#/' + rt; }, r);
    await page.waitForTimeout(400);
    const rememberEl = await page.$('label:has-text("记住密码")');
    if (rememberEl) {
      const txt = await rememberEl.textContent();
      console.log(`! ${r} 页面发现记住密码: ${txt}`);
    }
  }

  console.log('\n6. 错误汇总:');
  console.log('控制台错误:', consoleMessages.length);
  console.log('页面异常:', pageErrors.length);
  if (consoleMessages.length > 0) console.log(JSON.stringify(consoleMessages, null, 2));
  if (pageErrors.length > 0) console.log(JSON.stringify(pageErrors, null, 2));

  await context.close();
  await browser.close();
})();
