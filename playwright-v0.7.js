// playwright-v0.7.js — v0.6/v0.7 新增页面的端到端浏览器验证
//
// 覆盖：
//   - /timestamp：输入 now / unix_s / ISO / 人类可读，验证 8 行输出
//   - /cron：输入 0 9 * * 1-5，验证未来 5 次 + 字段说明；样例按钮
//   - /http：保存用例 → 列表出现 → 加载回填；env 组保存 → 切换 → 占位替换
//
// 跑法：
//   1. 后端起：cd doubao-toolbox && go run . (或 prebuild 二进制)
//   2. node playwright-v0.7.js
//
// 依赖：playwright（先 npm i playwright）

'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const assert = require('assert');

const BASE = process.env.OPS_BASE || 'http://127.0.0.1:18999';
const SCREENSHOT_DIR = path.join(__dirname, 'e2e-v0.7-out');
if (!fs.existsSync(SCREENSHOT_DIR)) fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });

const OK = '\x1b[32m✓\x1b[0m';
const FAIL = '\x1b[31m✗\x1b[0m';
let pass = 0, fail = 0;
function check(label, cond, detail) {
  if (cond) { console.log('  ' + OK + ' ' + label); pass++; }
  else { console.log('  ' + FAIL + ' ' + label + (detail ? ' — ' + detail : '')); fail++; }
}

async function clearAll(page) {
  // 先清空后端 http_cases，保证 e2e 起点干净
  // 通过浏览器 fetch API（已登录本机，CORS 通过）
  try {
    await page.evaluate(async () => {
      const r1 = await fetch('/api/http/envs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ envs: [] }) });
      // cases 没有清空全部的接口，逐个删：先拉列表
      const r2 = await fetch('/api/http/cases');
      const j = await r2.json();
      for (const c of (j.cases || [])) {
        await fetch('/api/http/cases?id=' + encodeURIComponent(c.id), { method: 'DELETE' });
      }
    });
  } catch (e) { console.log('  (clearAll failed:', e.message + ')'); }
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
    // 1) 首页能开
    await page.goto(BASE + '/');
    await page.waitForSelector('.tool-card', { timeout: 5000 });
    const cardNames = await page.$$eval('.tool-card .name', els => els.map(e => e.textContent.trim()));
    check('首页加载，看到 11+ 张卡片', cardNames.length >= 10, '实际 ' + cardNames.length);
    check('首页含"时间戳"卡片', cardNames.includes('时间戳'));
    check('首页含"Cron 解析"卡片', cardNames.includes('Cron 解析'));
    check('首页含"JSONPath 提取"卡片', cardNames.includes('JSONPath 提取'));

    // ============================================================
    // 时间戳页
    // ============================================================
    console.log('\n[时间戳]');
    await page.goto(BASE + '/#/timestamp');
    await page.waitForSelector('#ts-in', { timeout: 5000 });
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '01-timestamp-initial.png') });

    // 1) 点 "现在" 按钮
    await page.click('button:has-text("现在")');
    await page.waitForTimeout(300);
    const inputTypeNow = await page.$eval('.ts-row:nth-of-type(1) .ts-v', e => e.textContent);
    const unixSecNow = await page.$eval('.ts-row:nth-of-type(5) .ts-v', e => e.textContent);
    check('"现在"按钮填充 now', inputTypeNow === 'now', 'input_type=' + inputTypeNow);
    check('"现在"返回 unix_sec > 0', /^\d+$/.test(unixSecNow) && parseInt(unixSecNow) > 1700000000,
      'unix_sec=' + unixSecNow);

    // 2) 输入 unix_s 1719240000
    await page.fill('#ts-in', '1719240000');
    // 选 from=UTC, to=UTC
    await page.selectOption('#ts-from', 'UTC');
    await page.selectOption('#ts-to', 'UTC');
    await page.click('button:has-text("转换")');
    await page.waitForTimeout(300);
    const inputTypeUs = await page.$eval('.ts-row:nth-of-type(1) .ts-v', e => e.textContent);
    const utcUs = await page.$eval('.ts-row:nth-of-type(2) .ts-v', e => e.textContent);
    const wdUs = await page.$eval('.ts-row:nth-of-type(8) .ts-v', e => e.textContent);
    check('输入 unix_s 1719240000，input_type=unix_s', inputTypeUs === 'unix_s', 'got=' + inputTypeUs);
    check('unix_s 1719240000 → 2024-06-24 周一', utcUs.startsWith('2024-06-24') && wdUs === '周一',
      'utc=' + utcUs + ' weekday=' + wdUs);

    // 3) 切 to=Asia/Shanghai
    await page.selectOption('#ts-to', 'Asia/Shanghai');
    await page.waitForTimeout(300);
    const target = await page.$eval('.ts-row:nth-of-type(4) .ts-v', e => e.textContent);
    check('to=Shanghai 偏移到 +08:00', target.includes('+08:00') && target.includes('22:40:00'),
      'target=' + target);

    // 4) 输入 ISO 字符串（带 Z）
    await page.fill('#ts-in', '2026-06-24T13:00:00Z');
    await page.selectOption('#ts-from', 'UTC');
    await page.selectOption('#ts-to', 'UTC');
    await page.click('button:has-text("转换")');
    await page.waitForTimeout(300);
    const inputTypeIso = await page.$eval('.ts-row:nth-of-type(1) .ts-v', e => e.textContent);
    const wdIso = await page.$eval('.ts-row:nth-of-type(8) .ts-v', e => e.textContent);
    check('ISO 字符串 input_type=iso', inputTypeIso === 'iso', 'got=' + inputTypeIso);
    check('2026-06-24 是周三', wdIso === '周三', 'got=' + wdIso);

    // 5) 输入非法
    await page.fill('#ts-in', 'not a time');
    await page.click('button:has-text("转换")');
    await page.waitForTimeout(300);
    const inputTypeBad = await page.$eval('.ts-row:nth-of-type(1) .ts-v', e => e.textContent);
    check('非法输入 → input_type=invalid', inputTypeBad === 'invalid', 'got=' + inputTypeBad);

    // 6) 「↔ 互换时区」按钮
    await page.fill('#ts-in', '1719240000');
    await page.selectOption('#ts-from', 'UTC');
    await page.selectOption('#ts-to', 'Asia/Shanghai');
    await page.click('button:has-text("↔  互换时区")');
    await page.waitForTimeout(300);
    const fromVal = await page.$eval('#ts-from', e => e.value);
    const toVal = await page.$eval('#ts-to', e => e.value);
    check('互换时区后 from=Shanghai, to=UTC', fromVal === 'Asia/Shanghai' && toVal === 'UTC',
      'from=' + fromVal + ' to=' + toVal);
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '02-timestamp-shanghai.png') });

    // ============================================================
    // Cron 页
    // ============================================================
    console.log('\n[Cron]');
    await page.goto(BASE + '/#/cron');
    await page.waitForSelector('#cron-in', { timeout: 5000 });
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '03-cron-initial.png') });

    // 1) 默认值 0 9 * * *，点解析
    await page.click('button:has-text("解析")');
    await page.waitForTimeout(500);
    const validTag = await page.$eval('.tag.tag-ok', e => e.textContent).catch(() => null);
    const nextCount = await page.$$eval('.cron-col:nth-of-type(2) .cron-run-row', els => els.length);
    const prevCount = await page.$$eval('.cron-col:nth-of-type(3) .cron-run-row', els => els.length);
    check('"0 9 * * *" 解析合法', validTag === '✓ 合法', 'tag=' + validTag);
    check('"0 9 * * *" 未来 5 次', nextCount === 5, 'count=' + nextCount);
    check('"0 9 * * *" 过去 3 次', prevCount === 3, 'count=' + prevCount);

    // 2) 样例按钮：每周一 8:30
    await page.click('button:has-text("每周一 8:30")');
    await page.waitForTimeout(500);
    const cronInVal = await page.$eval('#cron-in', e => e.value);
    check('点"每周一 8:30"样例 → 输入变 30 8 * * 1', cronInVal === '30 8 * * 1', 'got=' + cronInVal);

    // 3) 6 段
    await page.fill('#cron-in', '0 0 9 * * *');
    await page.click('button:has-text("解析")');
    await page.waitForTimeout(500);
    const secTag = await page.$$eval('.tag.tag-warn', els => els.map(e => e.textContent));
    check('6 段 → "6 段（带秒）" tag 出现', secTag.some(t => t.includes('带秒')), 'tags=' + JSON.stringify(secTag));

    // 4) 描述符 @hourly
    await page.click('button:has-text("每小时")');
    await page.waitForTimeout(500);
    const descInVal = await page.$eval('#cron-in', e => e.value);
    check('点"每小时"样例 → 输入变 @hourly', descInVal === '@hourly', 'got=' + descInVal);
    const firstNext = await page.$eval('.cron-col:nth-of-type(2) .cron-run-row.first .cron-cn', e => e.textContent);
    // @hourly 应该是整点：HH:00:00。匹配最后的 "MM:SS" 部分应是 00:00
    const m = firstNext.match(/(\d{2}:\d{2}:\d{2})/);
    check('@hourly 下次运行的秒是 :00', m && m[1].endsWith(':00'), 'first next=' + firstNext);
    // 整点 → 分钟和秒都 00
    const [hh, mm, ss] = m[1].split(':');
    check('@hourly 下次分钟秒都是 00', mm === '00' && ss === '00', 'hms=' + m[1]);

    // 5) 非法
    await page.fill('#cron-in', 'not a cron');
    await page.click('button:has-text("解析")');
    await page.waitForTimeout(500);
    const errTag = await page.$eval('.tag.tag-err', e => e.textContent).catch(() => null);
    check('非法 → "✗ 不合法" tag', errTag === '✗ 不合法', 'tag=' + errTag);

    // 6) 时区切换 Shanghai
    await page.click('button:has-text("每天 9 点")');
    await page.selectOption('#cron-loc', 'Asia/Shanghai');
    await page.waitForTimeout(500);
    const shNext = await page.$eval('.cron-col:nth-of-type(2) .cron-run-row.first .cron-cn', e => e.textContent);
    check('Shanghai 时区下次 9:00', /09:00:00/.test(shNext), 'got=' + shNext);
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '04-cron-shanghai.png') });

    // ============================================================
    // HTTP 测试页：保存 + 加载 + env 切换
    // ============================================================
    console.log('\n[HTTP 测试]');
    await page.goto(BASE + '/#/http');
    await page.waitForSelector('#http-url', { timeout: 5000 });
    // 等用例列表加载
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '05-http-initial.png') });

    // 先清空
    await clearAll(page);
    await page.waitForTimeout(300);
    await page.reload();
    await page.waitForSelector('#http-url', { timeout: 5000 });
    await page.waitForTimeout(500);

    // 1) 填一份请求
    await page.selectOption('#http-method', 'GET');
    await page.fill('#http-url', 'http://{{host}}/health');
    await page.fill('#http-headers', 'X-Token: {{token}}');

    // 2) 展开"保存为用例"折叠区
    await page.click('summary:has-text("保存为用例")');
    await page.waitForTimeout(200);
    await page.fill('input[placeholder="分组，如 信贷生产"]', '信贷生产');
    await page.fill('input[placeholder="用例名"]', '健康检查');
    await page.click('button:has-text("保存")');
    await page.waitForTimeout(800);
    const sidebarAfterSave = await page.$$eval('.http-case-item', els => els.length);
    check('保存后侧栏出现 1 个用例', sidebarAfterSave === 1, 'count=' + sidebarAfterSave);
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '06-http-after-save.png') });

    // 3) 改下 form 模拟"测试过"
    await page.fill('#http-url', 'http://DIFFERENT/changed');

    // 4) 点 sidebar 里的用例回填
    await page.click('.http-case-item .http-case-row');
    await page.waitForTimeout(300);
    const reloadedUrl = await page.$eval('#http-url', e => e.value);
    const reloadedMethod = await page.$eval('#http-method', e => e.value);
    check('点 sidebar 用例 → URL 回填到 {{host}}', reloadedUrl === 'http://{{host}}/health', 'url=' + reloadedUrl);
    check('点 sidebar 用例 → method 回到 GET', reloadedMethod === 'GET', 'method=' + reloadedMethod);

    // 5) 同名再保存，应该覆盖（replaced=true）
    await page.fill('input[placeholder="分组，如 信贷生产"]', '信贷生产');
    await page.fill('input[placeholder="用例名"]', '健康检查');
    await page.selectOption('#http-method', 'POST');  // 改下方法
    await page.fill('#http-url', 'http://{{host}}/check');
    await page.click('button:has-text("保存")');
    await page.waitForTimeout(800);
    const sidebarAfterReplace = await page.$$eval('.http-case-item', els => els.length);
    const sidebarMethod = await page.$eval('.http-case-item .http-case-method', e => e.textContent.trim());
    check('同名保存 → 仍是 1 个用例（覆盖）', sidebarAfterReplace === 1, 'count=' + sidebarAfterReplace);
    check('覆盖后 method=POST', sidebarMethod === 'POST', 'method=' + sidebarMethod);

    // 6) 删除用例
    page.once('dialog', d => d.accept());
    await page.click('.http-case-item .btn[title="删除用例"]');
    await page.waitForTimeout(800);
    const sidebarAfterDel = await page.$$eval('.http-case-item', els => els.length);
    check('删除后侧栏 0 个用例', sidebarAfterDel === 0, 'count=' + sidebarAfterDel);

    // 7) Env 组：展开"管理环境变量组"
    await page.click('summary:has-text("管理环境变量组")');
    await page.waitForTimeout(200);
    page.once('dialog', d => d.accept('dev'));
    await page.click('button:has-text("+ 新增 env 组")');
    await page.waitForTimeout(500);
    // 添加一个变量
    await page.click('button:has-text("+ 变量")');
    await page.waitForTimeout(200);
    const kInp = await page.$('.http-env-vars input[type=text]');
    if (kInp) {
      await kInp.fill('host');
      await kInp.press('Tab');
      const vInp = await page.$$('.http-env-vars input[type=text]');
      if (vInp[1]) {
        await vInp[1].fill('10.0.0.1');
        await vInp[1].press('Tab');
      }
    }
    await page.waitForTimeout(300);
    // 顶部下拉应出现 dev
    const envOptions = await page.$$eval('#http-env-sel option', els => els.map(e => e.value));
    check('顶部下拉出现 dev', envOptions.includes('dev'), 'options=' + JSON.stringify(envOptions));

    // 8) 切到 dev，{{host}} 应被替换
    await page.selectOption('#http-env-sel', 'dev');
    // 实际请求时由 doSend 调用 applyEnvs 替换，UI 不直接展示替换后的值
    // 验证：先发送请求看 URL 是否带替换（请求会失败因为 10.0.0.1 连不上，仅看 status_tag = '失败'）
    await page.fill('#http-url', 'http://{{host}}/health');
    // 把超时调小一点，避免 e2e 等 15s
    await page.fill('#http-timeout', '1000');
    await page.click('button:has-text("发送")');
    // 等到 status 不再是"请求中…"
    await page.waitForFunction(() => {
      const t = document.querySelector('.cmp-pane-header .tag');
      return t && t.textContent !== '请求中…';
    }, { timeout: 5000 });
    const status = await page.$eval('.cmp-pane-header .tag', e => e.textContent);
    check('发送请求后状态 tag 更新', status !== '—' && status !== '请求中…', 'status=' + status);
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '07-http-env-active.png') });

    // 9) 实际请求本地（开浏览器跑同源）：用占位 host=localhost 试一次
    await clearAll(page);
    await page.reload();
    await page.waitForSelector('#http-url', { timeout: 5000 });
    await page.waitForTimeout(500);
    await page.click('summary:has-text("管理环境变量组")');
    await page.waitForTimeout(200);
    page.once('dialog', d => d.accept('local'));
    await page.click('button:has-text("+ 新增 env 组")');
    await page.waitForTimeout(500);
    await page.click('button:has-text("+ 变量")');
    await page.waitForTimeout(200);
    const ki2 = await page.$('.http-env-vars input[type=text]');
    if (ki2) { await ki2.fill('host'); await ki2.press('Tab'); }
    const vi2 = await page.$$('.http-env-vars input[type=text]');
    if (vi2[1]) { await vi2[1].fill('127.0.0.1:18999'); await vi2[1].press('Tab'); }
    await page.waitForTimeout(200);
    await page.selectOption('#http-env-sel', 'local');
    await page.selectOption('#http-method', 'GET');
    await page.fill('#http-url', 'http://{{host}}/api/config');
    await page.click('button:has-text("发送")');
    await page.waitForTimeout(2000);
    const localStatus = await page.$eval('.cmp-pane-header .tag', e => e.textContent);
    check('用 env 占位访问本地 200', /^200 /.test(localStatus), 'status=' + localStatus);
    const respBody = await page.$eval('#http-resp-body', e => e.value);
    check('响应 body 含 "app" 字段', respBody.includes('"app"'), 'body[:60]=' + respBody.slice(0, 60));
    await page.screenshot({ path: path.join(SCREENSHOT_DIR, '08-http-self-test.png') });

    // ============================================================
    // JSONPath 页（顺手回归一下 v0.6）
    // ============================================================
    console.log('\n[JSONPath 回归]');
    await page.goto(BASE + '/#/jsonpath');
    await page.waitForSelector('#jp-in', { timeout: 5000 });
    await page.fill('#jp-in', '{"user":{"name":"alice","age":30}}');
    await page.fill('#jp-path', 'user.name');
    await page.click('button:has-text("提取")');
    await page.waitForTimeout(300);
    const jpOut = await page.$eval('#jp-out', e => e.value);
    check('JSONPath user.name → alice', jpOut === 'alice', 'got=' + jpOut);

    await page.fill('#jp-path', 'user.items.#');
    await page.fill('#jp-in', '{"user":{"items":[1,2,3]}}');
    await page.click('button:has-text("提取")');
    await page.waitForTimeout(300);
    const jpArr = await page.$eval('#jp-out', e => e.value);
    check('JSONPath user.items.# → 3', jpArr === '3', 'got=' + jpArr);

    // ============================================================
    // 总结
    // ============================================================
    console.log('\n========================');
    console.log('通过 ' + pass + ' / 失败 ' + fail);
    console.log('截图：' + SCREENSHOT_DIR);

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
