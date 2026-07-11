/* perf-about.js — 关于页性能基线测试
 *
 * 用法:
 *   node tests/perf-about.js [out-dir]
 *
 * 输出: 写到 out-dir/ 下
 *   - metrics.json:  所有量化指标 (DOM数 / FCP / 长任务 / 控制台错误)
 *   - scroll.json:   滚动性能数据 (FPS / 长任务)
 *   - screenshots/:  5 主题 × 4 场景截图
 *   - console.log:   实时输出
 *
 * 前提: 18092 端口已起服务, 改的是当前 build 的二进制
 */
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const URL = 'http://127.0.0.1:18092/#/about';
const OUT = path.resolve(process.argv[2] || './test-output/perf-about');

const THEMES = ['dark', 'light', 'green', 'hc', 'xianxia'];

function ensureDir(d) { if (!fs.existsSync(d)) fs.mkdirSync(d, { recursive: true }); }
ensureDir(OUT);
ensureDir(path.join(OUT, 'screenshots'));

async function setupTheme(page, theme) {
  await page.evaluate((t) => {
    try { localStorage.setItem('kairo_theme', t); } catch (e) {}
  }, theme);
}

async function collectMetrics(page) {
  return await page.evaluate(() => {
    const domCount = document.querySelectorAll('*').length;
    const viewDomCount = document.querySelectorAll('#view *').length;
    const perf = performance.getEntriesByType('navigation')[0];
    const fcpEntry = performance.getEntriesByName('first-contentful-paint')[0];
    const paintEntries = performance.getEntriesByType('paint');
    const paints = {};
    paintEntries.forEach(p => { paints[p.name] = p.startTime; });
    return {
      domCount,
      viewDomCount,
      loadEvent: perf ? perf.loadEventEnd : null,
      domContentLoaded: perf ? perf.domContentLoadedEventEnd : null,
      fcp: fcpEntry ? fcpEntry.startTime : (paints['first-contentful-paint'] || null),
      paints,
      timing: perf ? {
        ttfb: perf.responseStart - perf.requestStart,
        download: perf.responseEnd - perf.responseStart,
        domInteractive: perf.domInteractive - perf.navigationStart,
        domComplete: perf.domComplete - perf.navigationStart
      } : null
    };
  });
}

async function getLongTasks(page) {
  return await page.evaluate(() => {
    const entries = performance.getEntriesByType('longtask') || [];
    return entries.map(e => ({
      startTime: e.startTime,
      duration: e.duration,
      name: e.name
    }));
  });
}

async function getMemory(page) {
  if (!page.evaluate(() => performance.memory)) return null;
  return await page.evaluate(() => ({
    usedJSHeapSize: performance.memory.usedJSHeapSize,
    totalJSHeapSize: performance.memory.totalJSHeapSize,
    jsHeapSizeLimit: performance.memory.jsHeapSizeLimit
  }));
}

async function collectConsoleErrors(page) {
  const errors = [];
  page.on('console', msg => {
    if (msg.type() === 'error') errors.push({ type: 'console.error', text: msg.text() });
  });
  page.on('pageerror', err => {
    errors.push({ type: 'pageerror', text: err.message, stack: err.stack });
  });
  return errors;
}

async function runScenario(browser, theme) {
  console.log(`\n=== 主题: ${theme} ===`);
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await context.newPage();
  const errors = await collectConsoleErrors(page);

  await setupTheme(page, theme);

  // === 场景 A: 首屏加载 ===
  const t0 = Date.now();
  await page.goto(URL, { waitUntil: 'networkidle' });
  const tLoad = Date.now() - t0;
  // 等所有 IO 懒渲染都触发完
  await page.waitForTimeout(1500);
  const metricsA = await collectMetrics(page);
  const longTasksA = await getLongTasks(page);
  const memA = await getMemory(page);
  console.log(`[A 首屏] load=${tLoad}ms dom=${metricsA.domCount} view=${metricsA.viewDomCount} fcp=${metricsA.fcp?.toFixed(0)}ms longTasks=${longTasksA.length}`);
  if (longTasksA.length > 0) {
    longTasksA.slice(0, 3).forEach(t => console.log(`   longtask: ${t.duration.toFixed(0)}ms @${t.startTime.toFixed(0)}`));
  }

  // 截图: 首屏
  await page.screenshot({ path: path.join(OUT, 'screenshots', `${theme}-A-top.png`), fullPage: false });

  // === 场景 B: 滚动到底 ===
  const scrollStart = Date.now();
  await page.evaluate(() => window.scrollTo({ top: document.body.scrollHeight, behavior: 'instant' }));
  await page.waitForTimeout(1500);
  const scrollDuration = Date.now() - scrollStart;
  const metricsB = await collectMetrics(page);
  const longTasksB = await getLongTasks(page);
  console.log(`[B 滚动] duration=${scrollDuration}ms dom=${metricsB.domCount} view=${metricsB.viewDomCount} longTasks=${longTasksB.length - longTasksA.length}`);

  // 截图: 底部
  await page.screenshot({ path: path.join(OUT, 'screenshots', `${theme}-B-bottom.png`), fullPage: false });

  // === 场景 C: 全页截图 (改前 vs 改后对比) ===
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(OUT, 'screenshots', `${theme}-C-fullpage.png`), fullPage: true });

  // === 场景 D: 展开 changelog 所有版本卡 ===
  await page.evaluate(() => {
    // 找 history section
    const sec = document.getElementById('sec-history');
    if (sec) sec.scrollIntoView({ behavior: 'instant', block: 'start' });
  });
  await page.waitForTimeout(800);
  const tExpand = Date.now();
  const expandResult = await page.evaluate(() => {
    let count = 0, errors = 0;
    const cards = document.querySelectorAll('#sec-history [id^="version-"]');
    cards.forEach(c => {
      try {
        // 模拟点击 header 展开
        const header = c.parentElement && c.parentElement.querySelector('div[onclick]');
        if (header) { header.click(); count++; }
      } catch (e) { errors++; }
    });
    return { cards: cards.length, clicked: count, errors };
  });
  await page.waitForTimeout(1500);
  const tExpandDuration = Date.now() - tExpand;
  const metricsD = await collectMetrics(page);
  const longTasksD = await getLongTasks(page);
  console.log(`[D 展开] cards=${expandResult.cards} clicked=${expandResult.clicked} errors=${expandResult.errors} duration=${tExpandDuration}ms`);

  // 截图: 展开后
  await page.screenshot({ path: path.join(OUT, 'screenshots', `${theme}-D-expanded.png`), fullPage: false });

  // === 场景 E: 快速点击 5 个 anchor ===
  const anchors = ['sec-architecture', 'sec-stack', 'sec-security', 'sec-modules', 'sec-history'];
  const tAnchor = Date.now();
  for (const a of anchors) {
    await page.evaluate((id) => {
      const el = document.getElementById(id);
      if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }, a);
    await page.waitForTimeout(80);
  }
  await page.waitForTimeout(1000);
  const tAnchorDuration = Date.now() - tAnchor;
  console.log(`[E Anchor] 5 clicks in ${tAnchorDuration}ms`);

  // === 汇总 ===
  const memFinal = await getMemory(page);
  await context.close();

  return {
    theme,
    scenarioA: { ...metricsA, loadMs: tLoad, longTaskCount: longTasksA.length, memA, longTasksTop3: longTasksA.slice(0, 3) },
    scenarioB: { ...metricsB, scrollMs: scrollDuration, newLongTasks: longTasksB.length - longTasksA.length, memFinal },
    scenarioD: { ...metricsD, expandMs: tExpandDuration, expandResult, memFinal },
    scenarioE: { anchorMs: tAnchorDuration },
    errors
  };
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const results = {};
  for (const theme of THEMES) {
    try {
      results[theme] = await runScenario(browser, theme);
    } catch (e) {
      console.error(`[FAIL] ${theme}: ${e.message}`);
      results[theme] = { error: e.message, stack: e.stack };
    }
  }
  await browser.close();

  fs.writeFileSync(path.join(OUT, 'metrics.json'), JSON.stringify(results, null, 2));
  console.log(`\n=== DONE ===`);
  console.log(`输出: ${OUT}`);
  console.log(`metrics.json + screenshots/`);
})();
