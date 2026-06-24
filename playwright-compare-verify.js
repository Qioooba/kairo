/* ===== playwright-compare-verify.js =====
 * 验证代码比对页改动：
 *   1. side-by-side 下勾选"只看左右不同"后，等同行被隐藏
 *   2. side-by-side 表格行内的 white-space 生效（空格不塌缩）
 *   3. cmp-pane-header 文件名有边框
 *
 * 注意：18090 是用户日常 dev 实例，不能 kill。
 * 这里直接读用户实例的 web 资源 + 调它 /api/diff/compare 做接口验证；
 * 前端层面用 puppeteer-like 的方式：把 web 静态资源 + js 加载起来，模拟一次完整比对。
 */
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = 'http://127.0.0.1:18091';

const LEFT = [
  'function foo() {',
  '    const a = 1;',
  '    const b = 2;',
  '    return a + b;',
  '}',
].join('\n');
const RIGHT = [
  'function foo() {',
  '    const a = 1;',
  '    const b = 99;',
  '    return a + b;',
  '}',
].join('\n');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  page.on('console', (msg) => {
    if (msg.type() === 'error') console.log('[browser:error]', msg.text());
  });
  page.on('pageerror', (err) => console.log('[pageerror]', err.message));

  console.log('1. goto', BASE);
  await page.goto(BASE, { waitUntil: 'networkidle' });

  // 进入"代码比对"路由
  console.log('2. open compare page');
  await page.evaluate(() => {
    if (typeof OTB === 'undefined') throw new Error('OTB not loaded');
    if (typeof OTB.state.setRoute !== 'function') {
      // 退化：直接调 router 注册的函数
      OTB.pages.compare(document.getElementById('view'));
    } else {
      OTB.state.setRoute('compare');
    }
  });
  await page.waitForSelector('#cmp-left', { timeout: 5000 });

  // 填文本
  console.log('3. fill text');
  await page.fill('#cmp-left', LEFT);
  await page.fill('#cmp-right', RIGHT);

  // 切到 side-by-side
  console.log('4. switch to side-by-side');
  await page.evaluate(() => {
    const r = document.querySelector('input[name="cmp-mode"][value="side"]');
    r.checked = true;
    r.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await page.waitForTimeout(300);

  // 触发比对
  console.log('5. click 比对');
  await page.click('button:has-text("开始比对")');
  await page.waitForTimeout(800);

  // 验证 1：side-by-side 已渲染
  const beforeRows = await page.$$eval('.d2h-wrapper .d2h-diff-tbody tr', (rows) => rows.length);
  console.log('   side-by-side 总行数（含 equal）=', beforeRows);

  // 验证 2：勾选"只看左右不同"
  console.log('6. toggle 只看左右不同');
  const hideEqualVisible = await page.evaluate(() => {
    // 找出"只看左右不同"那个 checkbox：它的父 label 文字含 "只看左右不同"
    const labels = Array.from(document.querySelectorAll('label.cmp-opt'));
    const target = labels.find((l) => l.textContent.includes('只看左右不同'));
    return target ? { found: true, display: target.style.display, checkbox: !!target.querySelector('input[type="checkbox"]') } : { found: false };
  });
  console.log('   cb 显示在 side 模式? ', JSON.stringify(hideEqualVisible));

  await page.evaluate(() => {
    const labels = Array.from(document.querySelectorAll('label.cmp-opt'));
    const target = labels.find((l) => l.textContent.includes('只看左右不同'));
    if (!target) throw new Error('hideEqual label not found');
    const cb = target.querySelector('input[type="checkbox"]');
    cb.checked = true;
    cb.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await page.waitForTimeout(500);

  // 调试：把生成出来的 unified diff 打到控制台
  // buildUnifiedDiffFromLines 是 compare.js 内的闭包函数，外部抓不到
  // 直接 inspect 当前渲染好的 d2h 内部 <tr> 内容
  const trSample = await page.$$eval('.d2h-wrapper .d2h-diff-tbody tr', (rows) =>
    rows.slice(0, 10).map((r) => r.innerText.replace(/\s+/g, ' ').slice(0, 80))
  );
  console.log('--- 前 10 行 tr 内容 ---');
  trSample.forEach((t, i) => console.log(`  [${i}]`, t));
  console.log('--- /前 10 行 ---');

  // 还要检查 statsBar 是否包含"已隐藏"
  const stats = await page.$eval('.diff-stats', (el) => el.textContent);
  console.log('   stats bar=', stats);

  // 直接在浏览器里跑一次 buildUnifiedDiffFromLines 通过 monkey-patch 拿到 lastResult.lines
  const builtDebug = await page.evaluate(() => {
    // 闭包函数外部抓不到，但 lastResult 是 module 级 let。
    // OTB.pages.compare 闭包内的 lastResult，外部也抓不到。
    // 只能从 .d2h-wrapper 的 data-* 看，或者直接读 d2h 内部生成的 text
    const table = document.querySelector('.d2h-wrapper .d2h-diff-table');
    return table ? table.outerHTML.length : 0;
  });
  console.log('   diff-table HTML length=', builtDebug);

  const afterRows = await page.$$eval('.d2h-wrapper .d2h-diff-tbody tr', (rows) => rows.length);
  console.log('   side-by-side 隐藏 equal 后行数=', afterRows);

  // 验证 3：行内 white-space
  console.log('7. check white-space on d2h side cell');
  const ws = await page.$eval('.d2h-wrapper .d2h-code-side-line', (el) => getComputedStyle(el).whiteSpace);
  console.log('   d2h-code-side-line white-space=', ws);

  // 验证 4：cmp-pane-header 文件名有边框
  const fileLabelStyle = await page.$eval('.cmp-pane-header .muted', (el) => {
    const cs = getComputedStyle(el);
    return { border: cs.border, bg: cs.backgroundColor, font: cs.fontFamily };
  });
  console.log('8. cmp-pane-header 文件名样式', fileLabelStyle);

  // 截图存档
  const outDir = '/tmp/otb-verify-shots';
  fs.mkdirSync(outDir, { recursive: true });
  await page.screenshot({ path: path.join(outDir, 'compare-side-hide-equal.png'), fullPage: true });
  console.log('9. screenshot ->', outDir);

  await browser.close();

  // 断言
  const fail = [];
  if (!beforeRows || beforeRows < 4) fail.push('side-by-side 行数太少：' + beforeRows);
  if (afterRows >= beforeRows) fail.push('勾选"只看左右不同"后行数没减少：before=' + beforeRows + ' after=' + afterRows);
  if (ws !== 'pre') fail.push('d2h side cell white-space 不是 pre：' + ws);
  if (!fileLabelStyle.border || fileLabelStyle.border === '0px none rgb(0, 0, 0)') fail.push('cmp-pane-header 文件名没边框');

  if (fail.length) {
    console.log('\nFAIL:');
    fail.forEach((f) => console.log(' -', f));
    process.exit(1);
  } else {
    console.log('\nALL OK');
  }
})().catch((e) => {
  console.error('verify error', e);
  process.exit(2);
});