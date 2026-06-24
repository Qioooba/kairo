// playwright-themes.js — 4 个主题（dark/light/green/hc）下的 v0.6/v0.7 页面可视化验证
//
// 跑法：
//   1. 启后端（同 playwright-v0.7.js）
//   2. node playwright-themes.js
//
// 检查：每个主题下访问 3 个新页面（timestamp / cron / http），
// 验证 data-theme 切换成功 + 截 12 张图 + 几个关键 CSS 变量实际值合理（不是黑底黑字 / 白底白字）。

'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const assert = require('assert');

const BASE = process.env.OPS_BASE || 'http://127.0.0.1:18999';
const OUT = path.join(__dirname, 'e2e-themes-out');
if (!fs.existsSync(OUT)) fs.mkdirSync(OUT, { recursive: true });

const THEMES = ['dark', 'light', 'green', 'hc'];
const PAGES = [
  { hash: '#/timestamp', name: 'timestamp', sample: '#ts-in' },
  { hash: '#/cron',      name: 'cron',      sample: '#cron-in' },
  { hash: '#/http',      name: 'http',      sample: '#http-url' },
];

const OK = '\x1b[32m✓\x1b[0m';
const FAIL = '\x1b[31m✗\x1b[0m';
let pass = 0, fail = 0;
function check(label, cond, detail) {
  if (cond) { console.log('  ' + OK + ' ' + label); pass++; }
  else { console.log('  ' + FAIL + ' ' + label + (detail ? ' — ' + detail : '')); fail++; }
}

// 读 CSS 变量，验证"有主题感"
async function readThemeVars(page) {
  return await page.evaluate(() => {
    const cs = getComputedStyle(document.documentElement);
    const get = (k) => cs.getPropertyValue(k).trim();
    return {
      theme: document.documentElement.getAttribute('data-theme'),
      bg: get('--bg-0'),
      bg2: get('--bg-2'),
      text: get('--text'),
      textDim: get('--text-dim'),
      primary: get('--primary'),
      line: get('--line'),
      // 把背景色转 RGB 算亮度
      bgRgb: (() => {
        const s = get('--bg-0');
        if (!s) return null;
        let m = s.match(/^#([0-9a-f]{6})$/i);
        if (m) {
          const n = parseInt(m[1], 16);
          return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
        }
        m = s.match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/);
        if (m) return [+m[1], +m[2], +m[3]];
        return null;
      })(),
      textRgb: (() => {
        const s = get('--text');
        if (!s) return null;
        let m = s.match(/^#([0-9a-f]{6})$/i);
        if (m) {
          const n = parseInt(m[1], 16);
          return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
        }
        m = s.match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/);
        if (m) return [+m[1], +m[2], +m[3]];
        return null;
      })(),
    };
  });
}

// 验：背景和文字色对比度（简单亮度差），避免"白底白字"
function luma(rgb) {
  if (!rgb) return null;
  // 相对亮度近似
  return 0.299 * rgb[0] + 0.587 * rgb[1] + 0.114 * rgb[2];
}
function contrastOk(vars) {
  const bgL = luma(vars.bgRgb);
  const txL = luma(vars.textRgb);
  if (bgL == null || txL == null) return null; // 不知道
  return Math.abs(bgL - txL) > 50; // 至少 50 亮度差
}

async function run() {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const consoleErrors = [];
  page.on('pageerror', (e) => consoleErrors.push('pageerror: ' + e.message));
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push('console.error: ' + msg.text());
  });

  try {
    // 先在 default 加载
    await page.goto(BASE + '/');
    await page.waitForSelector('.tool-card', { timeout: 5000 });

    for (const theme of THEMES) {
      console.log('\n=== 主题: ' + theme + ' ===');
      // 通过 OTB.theme.set 切换
      await page.evaluate((t) => window.OTB.theme.set(t), theme);
      await page.waitForTimeout(150); // 让 CSS 应用

      // 读主题变量
      const vars = await readThemeVars(page);
      check('[' + theme + '] data-theme 切换成功', vars.theme === theme, 'actual=' + vars.theme);
      check('[' + theme + '] --bg 变量非空', !!vars.bg, 'bg=' + JSON.stringify(vars.bg));
      check('[' + theme + '] --text 变量非空', !!vars.text, 'text=' + JSON.stringify(vars.text));
      const okC = contrastOk(vars);
      if (okC !== null) {
        check('[' + theme + '] 背景/文字对比度足够（>50 亮度差）', okC,
          'bg luma=' + Math.round(luma(vars.bgRgb)) + ' text luma=' + Math.round(luma(vars.textRgb)));
      }

      // 按钮 emoji 检查
      const emoji = await page.$eval('#theme-toggle', e => e.textContent);
      check('[' + theme + '] 顶栏按钮 emoji 正确',
        ['🌙','☀️','🌿','🔆'].includes(emoji), 'emoji=' + emoji);

      // 每个新页面截一张图
      for (const p of PAGES) {
        await page.goto(BASE + '/' + p.hash);
        await page.waitForSelector(p.sample, { timeout: 5000 });
        // 等渲染稳定
        await page.waitForTimeout(200);
        // 重新 apply 主题（路由切换可能没影响，但保险）
        await page.evaluate((t) => window.OTB.theme.set(t), theme);
        await page.waitForTimeout(100);
        const shot = path.join(OUT, theme + '-' + p.name + '.png');
        await page.screenshot({ path: shot });
        // 验证页面有可见内容（背景色已经被 CSS 变量应用）
        // HTTP 页用 flex 布局，主体是 .http-main；其它页用 .card
        const cardBg = await page.evaluate(() => {
          const el = document.querySelector('.card') || document.querySelector('.http-main');
          if (!el) return null;
          return getComputedStyle(el).backgroundColor;
        });
        check('[' + theme + '][' + p.name + '] 主容器背景色非空', !!cardBg, 'bg=' + cardBg);
        // 输入框可见
        const inpVisible = await page.evaluate((sel) => {
          const el = document.querySelector(sel);
          if (!el) return null;
          const cs = getComputedStyle(el);
          return { color: cs.color, bg: cs.backgroundColor, display: cs.display };
        }, p.sample);
        check('[' + theme + '][' + p.name + '] 输入框 ' + p.sample + ' 可见',
          inpVisible && inpVisible.display !== 'none' && !!inpVisible.color && !!inpVisible.bg,
          JSON.stringify(inpVisible));
      }
    }

    // 额外：在 light 主题下，HTTP 页的 sidebar / 折叠区 / 按钮颜色要合理
    console.log('\n=== light 主题专项：HTTP 页细节 ===');
    await page.evaluate(() => window.OTB.theme.set('light'));
    await page.goto(BASE + '/#/http');
    await page.waitForSelector('#http-url', { timeout: 5000 });
    await page.waitForTimeout(300);
    // 展开保存折叠区
    await page.click('summary:has-text("保存为用例")');
    await page.waitForTimeout(200);
    const saveSectionBg = await page.evaluate(() => {
      const sec = document.querySelector('.http-save-section');
      return sec ? getComputedStyle(sec).backgroundColor : null;
    });
    check('[light][http] 保存折叠区背景色非空', !!saveSectionBg, 'bg=' + saveSectionBg);
    await page.screenshot({ path: path.join(OUT, 'light-http-expanded.png'), fullPage: true });

    // 在 hc 主题下：所有文字都该是高对比（白底黑字 / 黑底白字，对比度高）
    console.log('\n=== hc 主题专项 ===');
    await page.evaluate(() => window.OTB.theme.set('hc'));
    await page.goto(BASE + '/#/timestamp');
    await page.waitForSelector('#ts-in', { timeout: 5000 });
    await page.fill('#ts-in', 'now');
    await page.click('button:has-text("转换")');
    await page.waitForTimeout(400);
    const hcVars = await readThemeVars(page);
    const hcContrast = luma(hcVars.bgRgb) != null && luma(hcVars.textRgb) != null
      ? Math.abs(luma(hcVars.bgRgb) - luma(hcVars.textRgb)) : 0;
    check('[hc] 高对比主题亮度差 ≥ 150', hcContrast >= 150, 'contrast=' + Math.round(hcContrast));
    await page.screenshot({ path: path.join(OUT, 'hc-timestamp-filled.png') });

    // 在 green 主题下：检查背景色调（绿色系偏暖）
    console.log('\n=== green 主题专项 ===');
    await page.evaluate(() => window.OTB.theme.set('green'));
    await page.goto(BASE + '/#/cron');
    await page.waitForSelector('#cron-in', { timeout: 5000 });
    await page.fill('#cron-in', '0 9 * * 1-5');
    await page.click('button:has-text("解析")');
    await page.waitForTimeout(400);
    const greenVars = await readThemeVars(page);
    // green 主题的 --bg-0 应该是绿色系（G > B 至少 3 个值）
    if (greenVars.bgRgb) {
      const [r, g, b] = greenVars.bgRgb;
      const greenish = g > b + 3 || r > b + 3;  // 设计是浅米黄绿，差异较小
      check('[green] --bg 偏绿/暖（G 或 R > B）', greenish,
        'rgb=' + JSON.stringify(greenVars.bgRgb));
    } else {
      check('[green] --bg 解析', false, 'no rgb');
    }
    await page.screenshot({ path: path.join(OUT, 'green-cron.png') });

    console.log('\n========================');
    console.log('通过 ' + pass + ' / 失败 ' + fail);
    console.log('截图：' + OUT);

    if (consoleErrors.length) {
      console.log('\n⚠️ 浏览器控制台错误:');
      consoleErrors.forEach(e => console.log('  - ' + e));
    } else {
      console.log('✅ 浏览器无 console.error / pageerror');
    }
  } finally {
    await browser.close();
  }
  process.exit(fail > 0 ? 1 : 0);
}

run().catch(e => { console.error('FATAL:', e); process.exit(2); });
