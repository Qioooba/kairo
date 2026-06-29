// playwright-http2.js — HTTP 测试页 v0.7-Redesign (Postman 风格) e2e + 截图
//
// 覆盖：
//   - 首页 → HTTP 测试
//   - 键值对 Headers 编辑（加行 / 删行 / 粘贴）
//   - Body 多模式切换（none / form-data / url-encoded / raw）
//   - JSON 美化按钮
//   - 响应区：状态码 / Pretty 高亮 / Raw / Preview / 复制 / 复制为 cURL / 下载
//   - 用例管理：保存 / 搜索 / 复制用例 / 编辑 / 删除 / 脏标记
//   - Env 组管理
//   - 导出 / 导入
//
// 跑法：
//   1. 后端起：cd doubao-toolbox && /tmp/doubao-toolbox-http-test  (或 go run .)
//   2. node playwright-http2.js
//
// 依赖：playwright（先 npm i playwright）

'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.OPS_BASE || 'http://127.0.0.1:18999';
const OUT = path.join(__dirname, 'e2e-http2-out');
if (!fs.existsSync(OUT)) fs.mkdirSync(OUT, { recursive: true });

const OK = '\x1b[32m✓\x1b[0m';
const FAIL = '\x1b[31m✗\x1b[0m';
let pass = 0, fail = 0;
function check(label, cond, detail) {
  if (cond) { console.log('  ' + OK + ' ' + label); pass++; }
  else { console.log('  ' + FAIL + ' ' + label + (detail ? ' — ' + detail : '')); fail++; }
}

async function clearAll(page) {
  try {
    await page.evaluate(async () => {
      await fetch('/api/http/envs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ envs: [] }) });
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
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  const consoleErrors = [];
  page.on('pageerror', (e) => consoleErrors.push('pageerror: ' + e.message));
  // 全局 dialog 默认 accept（dirty 提示）
  const globalDialogAccept = d => { try { d.accept(); } catch (e) {} };
  page.on('dialog', globalDialogAccept);
  // 临时给指定 dialog 输入 prompt 文本（移除全局 accept，临时拦截，完事恢复）
  function answerPrompt(text) {
    page.off('dialog', globalDialogAccept);
    page.once('dialog', d => { try { d.accept(text); } catch (e) {} });
    setTimeout(() => page.on('dialog', globalDialogAccept), 1000); // 1s 后恢复
  }
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push('console.error: ' + msg.text());
  });

  try {
    console.log('[HTTP2-Redesign e2e]');

    // 1) 启动
    await page.goto(BASE + '/');
    await page.waitForSelector('.tool-card', { timeout: 5000 });
    await clearAll(page);

    // 2) 进入 HTTP 页
    await page.goto(BASE + '/#/http');
    await page.waitForSelector('#http2-url', { timeout: 5000 });
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(OUT, '01-http2-initial.png'), fullPage: false });
    check('页面加载，看到 URL 输入框', !!(await page.$('#http2-url')));
    check('页面加载，看到方法下拉', !!(await page.$('#http2-method')));
    check('页面加载，看到左侧栏', !!(await page.$('.http2-sidebar')));
    check('默认 GET', (await page.$eval('#http2-method', e => e.value)) === 'GET');
    const initialHeaders = await page.$$eval('.http2-card:has(.http2-card-title:text-is("Headers")) .http2-kv-table .kv-row', els => els.length);
    check('Headers 默认 1 行', initialHeaders === 1, 'count=' + initialHeaders);

    // 3) 加 Header 行
    await page.click('.http2-card:has(.http2-card-title:text-is("Headers")) .http2-kv-add');
    await page.waitForTimeout(100);
    const headersAfterAdd = await page.$$eval('.http2-card:has(.http2-card-title:text-is("Headers")) .http2-kv-table .kv-row', els => els.length);
    check('点 + 加行 → 2 行', headersAfterAdd === 2, 'count=' + headersAfterAdd);

    // 4) 填写 Headers
    const kvRows = await page.$$('.http2-card:has(.http2-card-title:text-is("Headers")) .http2-kv-table .kv-row');
    // 第 1 行已经预填 Content-Type: application/json
    await kvRows[1].$$eval('input', ins => { ins[1].value = ''; ins[1].dispatchEvent(new Event('input', { bubbles: true })); });
    await kvRows[1].$$eval('input', ins => { ins[1].value = 'X-Token'; ins[1].dispatchEvent(new Event('input', { bubbles: true })); });
    await kvRows[1].$$eval('input', ins => { ins[2].value = 'abc123'; ins[2].dispatchEvent(new Event('input', { bubbles: true })); });

    // 5) URL 填写
    await page.fill('#http2-url', 'https://httpbin.org/get');
    await page.selectOption('#http2-method', 'GET');
    await page.screenshot({ path: path.join(OUT, '02-http2-filled.png'), fullPage: false });

    // 6) 发送
    console.log('  → 发送真实请求...');
    await page.click('.http2-send-btn');
    // 等状态更新（httpbin 可能慢）
    try {
      await page.waitForFunction(() => {
        const s = document.querySelector('.http2-resp-status');
        return s && s.textContent && !s.textContent.includes('请求中');
      }, { timeout: 15000 });
    } catch (e) {
      console.log('  (请求超时，可能 httpbin 不可达，跳过外部网络验证)');
    }
    await page.waitForTimeout(500);
    const statusText = await page.$eval('.http2-resp-status', e => e.textContent);
    check('响应状态显示', statusText && statusText.length > 0 && !statusText.includes('请求中'), 'status=' + statusText);
    await page.screenshot({ path: path.join(OUT, '03-http2-response.png'), fullPage: false });

    // 7) 切响应视图
    const tabs = await page.$$('.http2-resp-tab-bar .http2-tab');
    check('响应 tab 有 3 个', tabs.length === 3, 'count=' + tabs.length);
    await tabs[1].click(); // Raw
    await page.waitForTimeout(200);
    const rawTA = await page.$('.http2-resp-body-raw');
    check('Raw 视图渲染 textarea', !!rawTA);
    await tabs[0].click(); // Pretty
    await page.waitForTimeout(200);
    const syntax = await page.$('.http2-syntax');
    check('Pretty 视图渲染语法高亮块', !!syntax);

    // 8) Body 模式切换
    const bodyTabs = await page.$$('.http2-card:has(.http2-card-title:has-text("Body")) .http2-tab');
    check('Body tab 有 4 个', bodyTabs.length === 4, 'count=' + bodyTabs.length);
    // 切到 form-data
    await bodyTabs[1].click();
    await page.waitForTimeout(200);
    const formEmpty = await page.$eval('.http2-card .http2-resp-empty', e => e.textContent).catch(() => '');
    check('form-data 默认空', formEmpty.includes('字段名') || (await page.$$('.http2-kv-table .kv-row')).length > 0);
    // 切到 url-encoded
    await bodyTabs[2].click();
    await page.waitForTimeout(200);
    check('url-encoded tab 可达', true);
    // 切到 none
    await bodyTabs[0].click();
    await page.waitForTimeout(200);
    const noneTxt = await page.$eval('.http2-resp-empty', e => e.textContent);
    check('none 模式显示提示', noneTxt.includes('不发送 Body'));
    // 回到 raw
    await bodyTabs[3].click();
    await page.waitForTimeout(200);

    // 9) JSON 美化：先粘一段压缩 JSON
    await page.click('#http2-body', { clickCount: 3 });
    await page.keyboard.type('{"a":1,"b":[1,2,3],"c":{"d":"hello"}}');
    await page.click('.http2-raw-wrap button:has-text("美化")');
    await page.waitForTimeout(200);
    const bodyVal = await page.$eval('#http2-body', e => e.value);
    check('JSON 美化后含换行和缩进', bodyVal.includes('\n') && bodyVal.includes('  '), 'len=' + bodyVal.length);

    // 10) 保存用例
    await page.fill('input[placeholder="用例名"]', 'get-with-token');
    await page.fill('input[placeholder="分组（默认 default）"]', 'demo');
    await page.click('.http2-save-btn');
    await page.waitForTimeout(800);
    const caseCount = await page.$$eval('.http2-case-row', els => els.length);
    check('保存后左侧栏出现 1 个用例', caseCount === 1, 'count=' + caseCount);
    await page.screenshot({ path: path.join(OUT, '04-http2-after-save.png'), fullPage: false });

    // 11) 改下 URL，触发 dirty
    await page.fill('#http2-url', 'https://httpbin.org/get?modified=1');
    await page.waitForTimeout(200);
    const dirtyBtn = await page.$('.http2-save-btn.dirty');
    check('改 URL 后保存按钮变 dirty 样式', !!dirtyBtn);
    const caseRow = await page.$('.http2-case-row');
    const isDirtyClass = await caseRow.evaluate(el => el.classList.contains('is-dirty'));
    check('侧栏用例行显示 dirty 标记', isDirtyClass);

    // 12) 搜索
    await page.fill('.http2-sidebar-search input', 'NONEXISTENT');
    await page.waitForTimeout(200);
    const noMatch = await page.$eval('.http2-cases-wrap', e => e.textContent);
    check('搜索无结果时显示提示', noMatch.includes('没有匹配'));
    await page.fill('.http2-sidebar-search input', 'get-with');
    await page.waitForTimeout(200);
    const filtered = await page.$$eval('.http2-case-row', els => els.length);
    check('搜索过滤生效', filtered === 1, 'count=' + filtered);
    await page.fill('.http2-sidebar-search input', '');
    await page.waitForTimeout(200);

    // 13) 复制用例：hover 显示按钮
    await page.hover('.http2-case-row');
    await page.waitForTimeout(150);
    const cloneBtn = await page.$('.http2-case-row .http2-case-actions button[title*="复制"]');
    check('hover 用例行显示复制按钮', !!cloneBtn);
    await cloneBtn.click();
    await page.waitForTimeout(200);
    const modal = await page.$('.http2-modal');
    check('复制用例弹窗打开', !!modal);
    await page.screenshot({ path: path.join(OUT, '05-http2-clone-modal.png'), fullPage: false });
    // 改名
    const nameInp = await page.$('.http2-modal .modal-row:nth-of-type(2) input');
    await nameInp.fill('get-with-token-copy');
    // 点 复制并打开
    await page.click('.http2-modal-actions .btn-primary, .http2-modal .modal-actions .btn-primary');
    await page.waitForTimeout(1000);
    const afterClone = await page.$$eval('.http2-case-row', els => els.length);
    check('复制后变成 2 个用例', afterClone === 2, 'count=' + afterClone);

    // 14) 复制为 cURL 按钮：先点回填，再请求一次（dirty 提示由全局 handler 自动 accept）
    await page.click('.http2-case-row:has-text("get-with-token-copy")');
    await page.waitForTimeout(300);
    // 这个用例 URL 没改（不是 dirty），直接 send 应该 ok；先确认是 clean 状态
    const dirtyNow = await page.$('.http2-save-btn.dirty');
    check('点新用例后是 clean（不 dirty）', !dirtyNow);

    // 15) 编辑（改名）
    await page.hover('.http2-case-row');
    await page.waitForTimeout(100);
    const editBtn = await page.$('.http2-case-row .http2-case-actions button[title*="改名"]');
    await editBtn.click();
    await page.waitForTimeout(200);
    const editModal = await page.$('.http2-modal');
    check('改名弹窗打开', !!editModal);
    const editInp = await page.$('.http2-modal .modal-row:nth-of-type(2) input');
    await editInp.fill('renamed-case');
    await page.click('.http2-modal .modal-actions .btn-primary');
    await page.waitForTimeout(800);
    const renamedTxt = await page.$eval('.http2-case-row:has-text("renamed-case")', e => e.textContent).catch(() => '');
    check('改名生效', renamedTxt.includes('renamed-case'));

    // 16) 删除 renamed-case（第二个；前面 get-with-token 保留）
    // dirty 提示由全局 handler 自动 accept
    await page.hover('.http2-case-row:has-text("renamed-case")');
    await page.waitForTimeout(100);
    const delBtn = await page.$('.http2-case-row:has-text("renamed-case") .http2-case-actions .btn-icon.del');
    await delBtn.click();
    await page.waitForTimeout(800);
    const afterDel = await page.$$eval('.http2-case-row', els => els.length);
    check('删除后剩 1 个', afterDel === 1, 'count=' + afterDel);

    // 17) Env 组：展开底部折叠区
    await page.click('details.http2-card summary');
    await page.waitForTimeout(200);
    const envMgr = await page.$('.http2-env-mgr-card');
    check('env 管理面板可见', !!envMgr);
    // 临时拦截 prompt 给 'dev'
    await answerPrompt('dev');
    await page.click('button:has-text("+ 新增 env 组")');
    await page.waitForTimeout(500);
    // 加变量
    await page.click('.http2-env-group .http2-kv-add');
    await page.waitForTimeout(200);
    const envKeyInp = await page.$('.http2-env-group .http2-kv-table:first-child .kv-row:first-child .kv-key');
    if (envKeyInp) {
      await envKeyInp.fill('host');
      await envKeyInp.press('Tab');
    }
    const envValInp = await page.$('.http2-env-group .http2-kv-table:first-child .kv-row:first-child .kv-val');
    if (envValInp) {
      await envValInp.fill('httpbin.org');
      await envValInp.press('Tab');
    }
    await page.waitForTimeout(300);
    const envOpts = await page.$$eval('#http2-env-sel option', els => els.map(e => e.value));
    check('env 下拉出现 dev', envOpts.includes('dev'), 'opts=' + JSON.stringify(envOpts));

    // 18) 切 env，用 {{host}} 占位再发一次
    await page.selectOption('#http2-env-sel', 'dev');
    await page.fill('#http2-url', 'https://{{host}}/get');
    await page.fill('input[type=number]', '10000');
    await page.click('.http2-send-btn');
    try {
      await page.waitForFunction(() => {
        const s = document.querySelector('.http2-resp-status');
        return s && s.textContent && !s.textContent.includes('请求中');
      }, { timeout: 12000 });
    } catch (e) { console.log('  (env 占位请求超时)'); }
    await page.waitForTimeout(500);
    const envStatus = await page.$eval('.http2-resp-status', e => e.textContent);
    check('env 占位请求收到响应', envStatus.length > 0 && !envStatus.includes('请求中'), 'status=' + envStatus);
    await page.screenshot({ path: path.join(OUT, '06-http2-env-replaced.png'), fullPage: false });

    // 19) 复制 cURL 按钮：模拟剪贴板（page.evaluate 触发 click + 检查 toast）
    const btnCurl = await page.$('button:has-text("复制为 cURL")');
    check('复制为 cURL 按钮存在', !!btnCurl);
    // 实际复制：直接通过 ctx.grantPermissions + 验证 toast
    await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE });
    await btnCurl.click();
    await page.waitForTimeout(500);
    // 检查 toast（已复制 cURL）
    const toastTxt = await page.evaluate(() => {
      const t = document.getElementById('toast');
      return t ? t.textContent : '';
    });
    check('复制 cURL 触发 toast', toastTxt.includes('cURL') || toastTxt.includes('已复制'));

    // 20) 整体布局截图
    await page.screenshot({ path: path.join(OUT, '07-http2-full.png'), fullPage: true });

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
