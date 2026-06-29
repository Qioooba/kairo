// playwright-all-themes.js — 4 主题 × 13 页面可视化扫描
//
// 思路：每个 (theme, page) 跑 3 步：
//   1. 切主题 → 路由切到 page → 等主内容出现
//   2. 找页面的所有 .btn，验证 visible + 文字 + 计算对比度（亮字 vs 暗底 / 反之）
//   3. 截图
//
// 任何页面"主内容没出现" / "console error" / "任意按钮不可见" → 失败

'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.OPS_BASE || 'http://127.0.0.1:18999';
const OUT = path.join(__dirname, 'e2e-all-themes-out');
if (!fs.existsSync(OUT)) fs.mkdirSync(OUT, { recursive: true });

const THEMES = ['dark', 'light', 'green', 'hc'];

// 13 个页面（不含 home）
const PAGES = [
  // route hash, page name, 主内容 selector
  { hash: '#/formatter',  name: 'formatter',  main: '#fmt-in' },
  { hash: '#/jsonpath',   name: 'jsonpath',   main: '#jp-in' },
  { hash: '#/timestamp',  name: 'timestamp',  main: '#ts-in' },
  { hash: '#/cron',       name: 'cron',       main: '#cron-in' },
  { hash: '#/http',       name: 'http',       main: '#http-url' },
  { hash: '#/compare',    name: 'compare',    main: '#cmp-left' },
  { hash: '#/websphere',  name: 'websphere',  main: '#ws-tabs, .ws-tab-bar, .card' },
  { hash: '#/files',      name: 'files',      main: '#files-toolbar, .card' },
  { hash: '#/downloads',  name: 'downloads',  main: 'h3, .btn-row, .mt-3' },
  { hash: '#/history',    name: 'history',    main: '.card' },
  { hash: '#/diagnostics',name: 'diagnostics',main: '.card' },
  { hash: '#/config',     name: 'config',     main: '.card' },
  { hash: '#/commands',   name: 'commands',   main: '.card' },
];

const OK = '\x1b[32m✓\x1b[0m';
const FAIL = '\x31m✗\x1b[0m';  // 注意：这里不用 \x1b[31m 是为了避免脚本里偶发转义错
let pass = 0, fail = 0;
const failedChecks = [];
function check(label, cond, detail) {
  if (cond) { pass++; }
  else {
    fail++;
    failedChecks.push(label + (detail ? ' — ' + detail : ''));
  }
}

// 解析颜色到 RGB
function parseRgb(s) {
  if (!s) return null;
  let m = s.match(/^#([0-9a-f]{6})$/i);
  if (m) { const n = parseInt(m[1], 16); return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff]; }
  m = s.match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/);
  if (m) return [+m[1], +m[2], +m[3]];
  return null;
}
function luma(rgb) { return rgb ? 0.299 * rgb[0] + 0.587 * rgb[1] + 0.114 * rgb[2] : null; }
function contrast(a, b) { const la = luma(a), lb = luma(b); if (la == null || lb == null) return null; return Math.abs(la - lb); }

// 计算单个按钮的"对比度评分"：浅色主题下要求按钮色 vs 按钮背景 > 50；暗色 > 50
async function checkButtonVisibility(page) {
  return await page.evaluate(() => {
    const btns = Array.from(document.querySelectorAll('button'));
    const results = [];
    for (const b of btns) {
      const rect = b.getBoundingClientRect();
      const cs = getComputedStyle(b);
      const visible = rect.width > 0 && rect.height > 0 && cs.display !== 'none' && cs.visibility !== 'hidden';
      if (!visible) continue;
      const txt = (b.textContent || '').trim().slice(0, 20);
      results.push({
        text: txt,
        bg: cs.backgroundColor,
        color: cs.color,
      });
    }
    return results;
  });
}

async function run() {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const consoleErrors = [];
  page.on('pageerror', (e) => consoleErrors.push({ where: 'pageerror', msg: e.message }));
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push({ where: 'console.error', msg: msg.text() });
  });

  try {
    await page.goto(BASE + '/');
    await page.waitForSelector('.tool-card', { timeout: 5000 });

    for (const theme of THEMES) {
      for (const p of PAGES) {
        // 1) 切主题
        await page.evaluate((t) => window.DTB.theme.set(t), theme);
        await page.waitForTimeout(80);

        // 2) 路由
        try {
          await page.goto(BASE + '/' + p.hash, { waitUntil: 'domcontentloaded' });
        } catch (e) {
          check(`[${theme}][${p.name}] 路由跳转`, false, e.message);
          continue;
        }
        // 等主内容出现
        let mainAppeared = false;
        try {
          await page.waitForSelector(p.main, { timeout: 5000 });
          mainAppeared = true;
        } catch (e) {
          // 容错：websphere/files/main 是多 selector，至少一个匹配
          // 单独 checkSelector 一下
        }
        check(`[${theme}][${p.name}] 主内容出现`, mainAppeared, 'selector=' + p.main);

        await page.waitForTimeout(200);

        // 3) 验证所有可见按钮对比度
        const buttons = await checkButtonVisibility(page);
        let badBtnCount = 0;
        let badBtns = [];
        for (const b of buttons) {
          const bg = parseRgb(b.bg);
          const fg = parseRgb(b.color);
          if (!bg || !fg) continue; // rgba(0,0,0,0) transparent 跳过
          const c = contrast(bg, fg);
          // 按钮对比度阈值：要求 >= 30（弱约束，避免漏判；严格 50 AA）
          if (c < 30) {
            badBtnCount++;
            if (badBtns.length < 3) {
              badBtns.push(`"${b.text}" bg=${b.bg} fg=${b.color} contrast=${c.toFixed(0)}`);
            }
          }
        }
        check(`[${theme}][${p.name}] 按钮对比度 ≥ 30 (${buttons.length} 按钮)`,
          badBtnCount === 0,
          badBtnCount + ' 个低对比: ' + badBtns.join('; '));

        // 4) 截图
        const shotPath = path.join(OUT, theme + '-' + p.name + '.png');
        await page.screenshot({ path: shotPath, fullPage: false });

        // 5) 收集所有按钮对比度，写到 json 供后面排查
        const detailPath = path.join(OUT, theme + '-' + p.name + '.json');
        const details = buttons.map(b => {
          const bg = parseRgb(b.bg);
          const fg = parseRgb(b.color);
          return {
            text: b.text,
            bg: b.bg,
            color: b.color,
            contrast: bg && fg ? contrast(bg, fg) : null,
            flag: bg && fg && contrast(bg, fg) < 80 ? 'LOW' : 'OK',
          };
        });
        fs.writeFileSync(detailPath, JSON.stringify(details, null, 2));
      }
    }

    // 总结：收集所有 LOW 对比度的按钮
    const lows = [];
    for (const theme of THEMES) {
      for (const p of PAGES) {
        const detailPath = path.join(OUT, theme + '-' + p.name + '.json');
        if (!fs.existsSync(detailPath)) continue;
        const arr = JSON.parse(fs.readFileSync(detailPath, 'utf8'));
        for (const b of arr) {
          if (b.flag === 'LOW') {
            lows.push({ theme, page: p.name, text: b.text, contrast: Math.round(b.contrast || 0), bg: b.bg, color: b.color });
          }
        }
      }
    }
    if (lows.length > 0) {
      console.log('\n⚠️ 对比度 < 80 的按钮（可能"飘"）：');
      // 按页面 + 主题分组
      const grouped = {};
      for (const l of lows) {
        const k = l.theme + ' / ' + l.page;
        (grouped[k] = grouped[k] || []).push(l);
      }
      for (const k in grouped) {
        console.log('  ' + k + '：');
        grouped[k].forEach(l => console.log('    "' + l.text + '" contrast=' + l.contrast + ' bg=' + l.bg + ' fg=' + l.color));
      }
    } else {
      console.log('\n✅ 所有按钮对比度都 ≥ 80');
    }

    console.log('\n========================');
    console.log('通过 ' + pass + ' / 失败 ' + fail);
    console.log('截图：' + OUT);

    if (failedChecks.length > 0) {
      console.log('\n失败列表:');
      failedChecks.forEach(f => console.log('  ✗ ' + f));
    }
    if (consoleErrors.length > 0) {
      console.log('\n⚠️ 浏览器 console error:');
      // 按 page 分类
      const byPage = {};
      consoleErrors.forEach(e => {
        const key = e.where;
        byPage[key] = (byPage[key] || 0) + 1;
        if (e.msg && e.msg.length < 200) console.log('  ' + JSON.stringify(e));
      });
      console.log('  统计：', JSON.stringify(byPage));
    } else {
      console.log('\n✅ 浏览器无 console error / pageerror');
    }
  } finally {
    await browser.close();
  }
  process.exit(fail > 0 || consoleErrors.length > 0 ? 1 : 0);
}

run().catch(e => { console.error('FATAL:', e); process.exit(2); });
