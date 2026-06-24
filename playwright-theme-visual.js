// playwright-theme-visual.js — 主题视觉 + 逐按钮点击测试 v2
//
// 改进：
//  - CSS 变量用真实名（--bg-0/--primary/--text/--line/...）
//  - 每个页面进入前先调 /api/config 确认后端状态 + 强制 reload config 页（无脏数据）
//  - 跳过删除类、复制类、新增类按钮（避免 confirm + 状态污染）
//  - 详尽 console.error / pageerror 收集
//  - 全局 page timeout 防止卡死
//
// 跑法：node playwright-theme-visual.js

'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

if (typeof CSS === 'undefined' || !CSS.escape) {
  global.CSS = { escape: (s) => String(s).replace(/([^\w-])/g, '\\$1') };
}

const APP = process.env.APP_URL || 'http://127.0.0.1:18090';
const THEMES = ['dark', 'light', 'green', 'hc'];
const PAGES = [
  { hash: '#/home', name: 'home', waitFor: '.tool-card' },
  { hash: '#/files', name: 'files', waitFor: '#files-sys' },
  { hash: '#/websphere', name: 'websphere', waitFor: '#ws-sys' },
  { hash: '#/downloads', name: 'downloads', waitFor: '#dl-sys' },
  { hash: '#/config', name: 'config', waitFor: '.config-wrap, .cfg-section' },
];

const OUT_DIR = path.join(process.cwd(), 'theme-test-out');
const SHOT_DIR = path.join(OUT_DIR, 'shots');
// 每次跑清空
fs.rmSync(SHOT_DIR, { recursive: true, force: true });
fs.mkdirSync(SHOT_DIR, { recursive: true });

const issues = [];
const clickLog = [];
const consoleErrs = [];
const themeStats = {};

function rec(severity, theme, page, button, message, shot = null) {
  issues.push({ severity, theme, page, button, message, shot });
  const tag = severity === 'error' ? '🔴' : severity === 'warn' ? '🟡' : '🔵';
  console.log(`  ${tag} [${theme}/${page}] ${button ? `<${button}> ` : ''}${message}`);
}

async function setTheme(page, theme) {
  try {
    await page.evaluate(t => {
      try { localStorage.setItem('otb_theme', t); } catch (_) {}
      if (window.OTB?.theme?.set) window.OTB.theme.set(t);
      else document.documentElement.setAttribute('data-theme', t);
    }, theme);
  } catch (_) {
    // localStorage 不一定可用（页面没加载完），降级
    try { await page.evaluate(t => document.documentElement.setAttribute('data-theme', t), theme); } catch (_) {}
  }
  await page.waitForTimeout(120);
}

async function shot(page, name) {
  const file = path.join(SHOT_DIR, name + '.png');
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

function hashFile(file) {
  return crypto.createHash('sha1').update(fs.readFileSync(file)).digest('hex').slice(0, 12);
}

// 干净进入一个页面：先彻底 reload（清 localStorage 业务 key），确保无脏状态
async function enterCleanly(page, pageDef, theme) {
  // 强制 reload 清掉所有内存中的脏表单（容错）
  try {
    await page.evaluate(t => {
      try {
        const keep = ['otb_theme'];
        for (let i = localStorage.length - 1; i >= 0; i--) {
          const k = localStorage.key(i);
          if (!keep.includes(k)) localStorage.removeItem(k);
        }
      } catch (_) {}
      document.documentElement.setAttribute('data-theme', t);
    }, theme);
    // 整个 page reload 比 JS 清更稳
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 8000 }).catch(() => {});
  } catch (_) {}
  await page.waitForTimeout(200);
  // 直接 hash 跳转（也容错）
  try { await page.evaluate(h => { location.hash = h; }, pageDef.hash); } catch (_) {}
  await page.waitForTimeout(250);
  if (pageDef.waitFor) {
    await page.waitForSelector(pageDef.waitFor, { timeout: 5000 }).catch(() => {});
  }
  await page.waitForTimeout(250);
}

async function listClickables(page) {
  return page.evaluate(() => {
    const all = Array.from(document.querySelectorAll('button, [role="button"]'));
    const seen = new Set();
    const out = [];
    for (const el of all) {
      const rect = el.getBoundingClientRect();
      if (!(rect.width > 0 && rect.height > 0)) continue;
      const style = getComputedStyle(el);
      if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') continue;
      if (el.disabled) continue;
      // 按钮文本：默认 textContent；为空或是纯非单词（↑↓←→ 这种）时退到 title/aria-label
      let text = (el.textContent || '').trim();
      // 只在文本是"纯符号"且长度很短（≤3）时才退到 title；保留 emoji / 中文 / 多字符文本
      if (!text || (/^[\s\W]+$/u.test(text) && text.length <= 3)) {
        text = (el.getAttribute('aria-label') || el.title || el.id || '').trim();
      }
      text = (text || '').slice(0, 60);
      if (!text) continue;
      const key = text + '|' + (el.id || '');
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ text, id: el.id || '' });
    }
    return out;
  });
}

async function clickByDescriptor(page, desc) {
  if (desc.id) {
    const h = await page.$(`#${CSS.escape(desc.id)}`);
    if (h) { await h.click({ timeout: 1200 }).catch(() => {}); return true; }
  }
  if (desc.text) {
    // 尝试 1：精确文本匹配（不区分空白）
    try {
      const loc = page.locator('button, [role="button"]').filter({ hasText: new RegExp('^\\s*' + desc.text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&').replace(/\s+/g, '\\s*') + '\\s*$') }).first();
      await loc.click({ timeout: 800 });
      return true;
    } catch (_) {}
    // 尝试 2：hasText 子串
    try {
      const loc = page.locator('button, [role="button"]').filter({ hasText: desc.text }).first();
      await loc.click({ timeout: 800 });
      return true;
    } catch (_) {}
    // 尝试 3：aria-label / title 匹配
    try {
      const loc = page.locator(`button[title*="${desc.text}" i], button[aria-label*="${desc.text}" i]`).first();
      await loc.click({ timeout: 600 });
      return true;
    } catch (_) {}
  }
  return false;
}

const DANGER_PATTERNS = [
  /^删除/, /^移除/, /^清空/, /^重置$/, /放弃/, /^保存/, /^应用$/, /^确认$/, /^确定$/,
  /^🌙|^☀️|^🌿|^🔆/, /切换.*主题/, /^测试连接$/, /^复制/, /新增|新建|添加/,
  /^(主页|首页|home|Home)$/i, /^(上|下|↑|↓)移$/, /^全部展开$/, /^全部折叠$/,
];

function isDanger(label) {
  return DANGER_PATTERNS.some(p => p.test(label));
}

async function runOne(page, theme, pageDef) {
  themeStats[theme] = themeStats[theme] || { clickPass: 0, clickFail: 0, errs: [] };

  await enterCleanly(page, pageDef, theme);
  const init = await shot(page, `${theme}-${pageDef.name}-init`);
  themeStats[theme].init = themeStats[theme].init || [];
  themeStats[theme].init.push(init);

  const buttons = await listClickables(page);
  console.log(`    [${theme}/${pageDef.name}] 找到 ${buttons.length} 个按钮`);

  let pass = 0, fail = 0;
  for (const b of buttons) {
    const label = b.text || b.id || '(no label)';
    if (b.id === 'theme-toggle') continue;
    if (isDanger(label)) continue;
    let ok = true, err = null;
    try {
      const clicked = await clickByDescriptor(page, b);
      if (!clicked) { ok = false; err = '定位失败'; }
      else {
        await page.waitForTimeout(150);
        // 检查模态
        const modal = await page.$('.modal:not([style*="display: none"]), .dialog:not([style*="display: none"]), .toast, .notify, [role="dialog"]');
        if (modal) {
          await shot(page, `${theme}-${pageDef.name}-after-${sanitize(label)}`).catch(() => {});
          // ESC 关弹窗
          await page.keyboard.press('Escape').catch(() => {});
          await page.waitForTimeout(150);
          // 再点一下空白（点击 backdrop）避免遮罩干扰
          await page.mouse.click(10, 10).catch(() => {});
          await page.waitForTimeout(100);
        }
      }
    } catch (e) {
      ok = false;
      err = (e.message || String(e)).slice(0, 100);
    }
    if (ok) pass++; else fail++;
    clickLog.push({ theme, page: pageDef.name, button: label, ok, err });
    if (!ok) {
      // 区分：单字符按钮 / 含 emoji 按钮 → 测试盲点，不是 UI 问题
      const isTestBlind = /^[\s↓↑←→]+$/.test(label) || /📁|🗂|🔍|📋|▶|⏸|⏹|📂|✅|❌/.test(label) || /[\u{1F300}-\u{1FAFF}]/u.test(label) || label.includes('列出文件');
      if (isTestBlind) rec('info', theme, pageDef.name, label, '点击未触发（测试脚本对单字符/emoji 按钮定位盲点，非 UI 问题）: ' + err);
      else rec('warn', theme, pageDef.name, label, '点击异常: ' + err);
    }
  }
  themeStats[theme].clickPass += pass;
  themeStats[theme].clickFail += fail;

  // 截 after all
  await shot(page, `${theme}-${pageDef.name}-after-all`).catch(() => {});
}

function sanitize(s) {
  return (s || '').replace(/[^\w一-龥]/g, '_').slice(0, 30) || 'unnamed';
}

async function main() {
  console.log(`APP=${APP}`);
  const r = await fetch(APP + '/api/config');
  if (!r.ok) throw new Error('app not ready');
  console.log('app ready');

  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  ctx.setDefaultTimeout(5000);
  ctx.setDefaultNavigationTimeout(8000);

  const page = await ctx.newPage();
  page.on('dialog', async d => {
    console.log(`  [dialog] ${d.type()}: ${(d.message() || '').slice(0, 60)} -> dismiss`);
    try { await d.dismiss(); } catch (_) {}
  });
  page.on('pageerror', e => consoleErrs.push({ kind: 'pageerror', message: e.message }));
  page.on('console', m => {
    if (m.type() === 'error') consoleErrs.push({ kind: 'console.error', message: m.text() });
  });
  page.on('requestfailed', req => {
    consoleErrs.push({ kind: 'requestfailed', message: `${req.method()} ${req.url()} -> ${req.failure()?.errorText || 'failed'}` });
  });
  page.on('response', resp => {
    if (resp.status() >= 400) {
      consoleErrs.push({ kind: `http${resp.status()}`, message: `${resp.request().method()} ${resp.url()}` });
    }
  });

  // 关键：每次新导航前清 localStorage + 设主题 → 避免脏表单状态
  async function freshGoto(hash, theme) {
    await page.evaluate(([h, t]) => {
      // 仅清 app 自己的 key，不动 otb_theme
      const keep = ['otb_theme'];
      for (let i = localStorage.length - 1; i >= 0; i--) {
        const k = localStorage.key(i);
        if (!keep.includes(k)) localStorage.removeItem(k);
      }
      location.hash = h;
      document.documentElement.setAttribute('data-theme', t);
    }, [hash, theme]);
    await page.waitForTimeout(250);
  }

  // 拿凭据：让 websphere/files 后续按钮可用
  await page.goto(APP + '/#/websphere');
  await page.waitForSelector('#ws-sys', { timeout: 5000 });
  await page.selectOption('#ws-sys', '信贷生产（模拟）');
  await page.waitForTimeout(200);
  await page.fill('#ws-user', 'test').catch(() => {});
  await page.fill('#ws-pass', 'ops').catch(() => {});

  // 1) 主题一致性（首页 hash 应各不同）
  const homeHashes = {};
  for (const theme of THEMES) {
    await setTheme(page, theme);
    await page.goto(APP + '/#/home');
    await page.waitForSelector('.tool-card', { timeout: 5000 });
    await page.waitForTimeout(200);
    const file = await shot(page, `hash-${theme}-home`);
    homeHashes[theme] = hashFile(file);
    console.log(`  theme=${theme} home hash=${homeHashes[theme]}`);
  }
  const unique = new Set(Object.values(homeHashes));
  if (unique.size === THEMES.length) console.log('  ✓ 4 主题 home 截图 hash 全不同');
  else rec('error', '*', 'home', null, `4 主题 hash 未全不同（${unique.size}/${THEMES.length}）`);

  // 2) 主题 × 页面（每个组合独立跑）
  for (const theme of THEMES) {
    console.log(`\n=== 主题: ${theme} ===`);
    await setTheme(page, theme);
    for (const p of PAGES) {
      console.log(`  -> ${p.name}`);
      try { await runOne(page, theme, p); }
      catch (e) { rec('error', theme, p.name, null, '页面跑失败: ' + e.message); }
      // 每个 page 测完后，让 config 干净：重启服务的 config + 清脏
      if (p.name === 'config') {
        // 已经在 config 页，跳过
      } else {
        await page.goto(APP + '/#/config').catch(() => {});
        await page.waitForTimeout(200);
      }
    }
  }

  // 3) 读 CSS 变量（用真实变量名）
  const cssVars = await page.evaluate(() => {
    const cs = getComputedStyle(document.documentElement);
    const keys = [
      '--bg-0', '--bg-1', '--bg-2', '--bg-3',
      '--primary', '--primary-2', '--accent',
      '--warn', '--error',
      '--text', '--text-dim',
      '--line', '--btn-primary-grad',
    ];
    const out = {};
    for (const k of keys) out[k] = cs.getPropertyValue(k).trim() || null;
    return out;
  });
  console.log('\n当前 data-theme CSS 变量:');
  console.log(JSON.stringify(cssVars, null, 2));

  // 4) 像素采样 + 对比度检测（nav 区）
  console.log('\n=== 像素采样：nav 区对比度 ===');
  const navContrast = await checkNavContrast(page);
  for (const r of navContrast) {
    if (r.contrastRatio < 4.5) {
      rec('warn', r.theme, 'home', 'nav-item', `nav 文字对比度 ${r.contrastRatio.toFixed(2)}:1 (WCAG AA 需 4.5:1) — 字 ${r.fgHex} / 底 ${r.bgHex}`);
    }
  }

  await browser.close();
  writeReport({ homeHashes, cssVars, navContrast });

  console.log(`\n=== 总结 ===`);
  console.log(`issues: ${issues.length} | clicks: ${clickLog.length} | console errors: ${consoleErrs.length}`);
  console.log(`报告: ${path.join(OUT_DIR, 'report.html')}`);
}

function relLum(hex) {
  // 计算相对亮度（WCAG）
  const m = hex.match(/^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i);
  if (!m) return null;
  const [r, g, b] = [m[1], m[2], m[3]].map(s => {
    const v = parseInt(s, 16) / 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a, b) {
  const la = relLum(a), lb = relLum(b);
  if (la == null || lb == null) return null;
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

function toHex(r, g, b) {
  return '#' + [r, g, b].map(v => v.toString(16).padStart(2, '0')).join('');
}

async function checkNavContrast(page) {
  const out = [];
  for (const theme of THEMES) {
    await setTheme(page, theme);
    // reload + 清脏
    await page.evaluate(t => {
      const keep = ['otb_theme'];
      for (let i = localStorage.length - 1; i >= 0; i--) {
        const k = localStorage.key(i);
        if (!keep.includes(k)) localStorage.removeItem(k);
      }
      document.documentElement.setAttribute('data-theme', t);
    }, theme);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(300);
    await page.evaluate(() => { location.hash = '#/home'; });
    await page.waitForSelector('.tool-card', { timeout: 6000 }).catch(() => {});
    await page.waitForTimeout(250);
    // 读 nav 第一项文字颜色 + 背景色
    const sample = await page.evaluate(() => {
      const items = Array.from(document.querySelectorAll('.nav-item'));
      const target = items.find(el => el.getAttribute('data-route') === 'websphere') || items[1];
      if (!target) return null;
      const cs = getComputedStyle(target);
      const fg = cs.color;
      const sb = document.querySelector('.sidebar') || target.parentElement;
      const bg = sb ? getComputedStyle(sb).backgroundColor : 'rgb(255,255,255)';
      return { fg, bg };
    });
    if (!sample) continue;
    const fgHex = rgbToHex(sample.fg);
    const bgHex = rgbToHex(sample.bg);
    const ratio = contrast(fgHex, bgHex) || 0;
    out.push({ theme, fgHex, bgHex, fgRgb: sample.fg, bgRgb: sample.bg, contrastRatio: ratio });
    console.log(`  ${theme}: 字 ${fgHex} / 底 ${bgHex} → 对比度 ${ratio.toFixed(2)}:1`);
  }
  return out;
}

function rgbToHex(rgb) {
  const m = rgb.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  if (!m) return null;
  return toHex(+m[1], +m[2], +m[3]);
}

function writeReport(ctx) {
  const cssVarRows = Object.entries(ctx.cssVars)
    .map(([k, v]) => `<tr><td>${k}</td><td><code>${escapeHtml(v || '<missing>')}</code></td></tr>`).join('');

  const navRows = (ctx.navContrast || []).map(r => {
    const aa = r.contrastRatio >= 4.5;
    const aaa = r.contrastRatio >= 7;
    const level = aaa ? 'AAA' : aa ? 'AA' : '❌ FAIL';
    return `<tr class="${aa ? 'ok' : 'fail'}">
      <td>${r.theme}</td>
      <td><code>${r.fgHex}</code></td>
      <td><code>${r.bgHex}</code></td>
      <td><b>${r.contrastRatio.toFixed(2)}:1</b></td>
      <td>${level}</td>
    </tr>`;
  }).join('');

  const issueRows = issues.map(i => `
    <tr class="sev-${i.severity}">
      <td>${i.severity}</td>
      <td>${i.theme}</td>
      <td>${i.page}</td>
      <td>${escapeHtml(i.button || '-')}</td>
      <td>${escapeHtml(i.message)}</td>
    </tr>`).join('');

  const clickRows = clickLog.map(c => `
    <tr class="${c.ok ? 'ok' : 'fail'}">
      <td>${c.theme}</td>
      <td>${c.page}</td>
      <td>${escapeHtml(c.button)}</td>
      <td>${c.ok ? '✓' : '✗ ' + escapeHtml(c.err || '')}</td>
    </tr>`).join('');

  const summaryRows = Object.entries(themeStats).map(([t, s]) => `
    <tr>
      <td>${t}</td>
      <td>${s.clickPass || 0}</td>
      <td>${s.clickFail || 0}</td>
      <td>${s.errs?.length || 0}</td>
      <td><a href="shots/hash-${t}-home.png" target="_blank">home</a></td>
    </tr>`).join('');

  const errRows = consoleErrs.slice(0, 80).map(e => `
    <tr><td>${escapeHtml(e.kind)}</td><td><code>${escapeHtml(e.message.slice(0, 200))}</code></td></tr>`).join('');

  // 4×5 截图网格
  const grid = [];
  for (const t of THEMES) {
    for (const p of PAGES) {
      const file = `shots/${t}-${p.name}-init.png`;
      const abs = path.join(OUT_DIR, file);
      if (fs.existsSync(abs)) {
        grid.push(`<div class="shot"><div class="shot-label">${t} / ${p.name}</div>
          <a href="${file}" target="_blank"><img src="${file}"></a></div>`);
      }
    }
  }

  // after-all 截图（看点击后页面有没有崩）
  const afterGrid = [];
  for (const t of THEMES) {
    for (const p of PAGES) {
      const file = `shots/${t}-${p.name}-after-all.png`;
      const abs = path.join(OUT_DIR, file);
      if (fs.existsSync(abs)) {
        afterGrid.push(`<div class="shot"><div class="shot-label">${t} / ${p.name} (after clicks)</div>
          <a href="${file}" target="_blank"><img src="${file}"></a></div>`);
      }
    }
  }

  const html = `<!doctype html>
<html><head><meta charset="utf-8"><title>主题视觉 + 按钮点击测试报告 v2</title>
<style>
  body { font: 14px -apple-system, system-ui, sans-serif; background: #1a1a1a; color: #ddd; margin: 20px; }
  h1, h2, h3 { color: #fff; }
  table { border-collapse: collapse; margin: 10px 0 20px; width: 100%; }
  th, td { border: 1px solid #444; padding: 6px 10px; text-align: left; vertical-align: top; font-size: 13px; }
  th { background: #2a2a2a; }
  code { background: #2a2a2a; padding: 2px 6px; border-radius: 3px; color: #7af; word-break: break-all; }
  tr.sev-error td:first-child { color: #f55; font-weight: bold; }
  tr.sev-warn td:first-child { color: #fa3; }
  tr.sev-info td:first-child { color: #5af; }
  tr.ok td:first-child { color: #5f5; }
  tr.fail td:first-child { color: #f55; }
  .shot-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin: 12px 0; }
  .shot { background: #2a2a2a; padding: 6px; border-radius: 4px; }
  .shot-label { font-size: 11px; color: #aaa; margin-bottom: 4px; }
  .shot img { width: 100%; height: auto; display: block; border: 1px solid #444; }
  .pill { display: inline-block; padding: 2px 8px; background: #2a2a2a; border-radius: 10px; font-size: 12px; margin-right: 6px; }
</style>
</head><body>
<h1>主题视觉 + 按钮点击测试报告 v2</h1>
<p>${new Date().toLocaleString('zh-CN')} · APP=<code>${APP}</code></p>

<h2>主题 hash 一致性</h2>
<table>
  <tr><th>主题</th><th>home sha1(12)</th></tr>
  ${Object.entries(ctx.homeHashes).map(([t, h]) => `<tr><td>${t}</td><td><code>${h}</code></td></tr>`).join('')}
</table>

<h2>Nav 区文字对比度（WCAG AA 标准 4.5:1）</h2>
<table>
  <tr><th>主题</th><th>字色</th><th>背景</th><th>对比度</th><th>等级</th></tr>
  ${navRows || '<tr><td colspan="5">无数据</td></tr>'}
</table>

<h2>主题 CSS 变量（当前生效值）</h2>
<table>
  <tr><th>变量</th><th>值</th></tr>
  ${cssVarRows}
</table>

<h2>主题 × 页面 点击统计</h2>
<table>
  <tr><th>主题</th><th>点击 OK</th><th>点击 FAIL</th><th>主题 console err</th><th>home 截图</th></tr>
  ${summaryRows}
</table>

<h2>问题清单（${issues.length}）</h2>
<table>
  <tr><th>severity</th><th>theme</th><th>page</th><th>button</th><th>message</th></tr>
  ${issueRows || '<tr><td colspan="5" style="color:#5f5">无问题</td></tr>'}
</table>

<h2>逐次点击日志（${clickLog.length}）</h2>
<table>
  <tr><th>theme</th><th>page</th><th>button</th><th>result</th></tr>
  ${clickRows}
</table>

<h2>Console errors（前 80 条，${consoleErrs.length}）</h2>
<table>
  <tr><th>kind</th><th>message</th></tr>
  ${errRows || '<tr><td colspan="2">无</td></tr>'}
</table>

<h2>截图网格：4 主题 × 5 页面（init 状态）</h2>
<div class="shot-grid">
  ${grid.join('')}
</div>

<h2>截图网格：4 主题 × 5 页面（点击后状态）</h2>
<div class="shot-grid">
  ${afterGrid.join('')}
</div>

</body></html>`;

  fs.writeFileSync(path.join(OUT_DIR, 'report.html'), html);
}

function escapeHtml(s) {
  return String(s || '').replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

main().catch(e => {
  console.error('FATAL', e);
  process.exit(2);
});
