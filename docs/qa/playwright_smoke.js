/*
 * 豆包工具箱 自动化 UI 冒烟测试
 *
 * 用 Playwright 启动 headless chromium，跑过所有 8 个页面
 * 的关键按钮，截图 + 收集 console / pageerror，最后输出 JSON 报告。
 *
 * 前置条件：
 *   - 豆包工具箱 二进制已经在 127.0.0.1:18090 跑起来
 *   - mock SSH 在 127.0.0.1:2225 跑起来（test/ops）
 *
 * 跑法：
 *   node docs/qa/playwright_smoke.js
 */

'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://127.0.0.1:18090';
const OUT_DIR = path.join(__dirname, 'screenshots');
const REPORT = path.join(__dirname, 'report.json');

const SYS = '信贷生产（模拟）';
const SRV = 'mock-node-1';
const DIR = '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1';
const USER = 'test';
const PASS = 'ops';

const VIEWPORT = { width: 1366, height: 900 };

if (!fs.existsSync(OUT_DIR)) fs.mkdirSync(OUT_DIR, { recursive: true });

// ---------- 工具 ----------
function nowIso() { return new Date().toISOString(); }
function shortErr(e) { return (e && e.message) ? e.message : String(e); }

class TestRunner {
  constructor() {
    this.results = [];        // {page, action, status, ms, detail, screenshot, errors[]}
    this.consoleErrors = [];  // 累积
    this.pageErrors = [];     // 累积
    this.actionCounter = 0;
  }

  async withStep(browser, page, fn) {
    const t0 = Date.now();
    const errs = [];
    const origCE = page.on('console', () => {});
    const errListener = (m) => {
      if (m.type() === 'error') errs.push({ where: page.url(), text: m.text() });
    };
    page.on('console', errListener);
    try {
      const r = await fn();
      return { ok: true, ms: Date.now() - t0, detail: r, errs };
    } catch (e) {
      return { ok: false, ms: Date.now() - t0, detail: null, errs, error: shortErr(e) };
    } finally {
      page.off('console', errListener);
    }
  }

  record(page, action, res, screenshot) {
    this.actionCounter++;
    this.results.push({
      idx: this.actionCounter,
      action,
      page: page ? new URL(page.url()).hash || '/' : '?',
      status: res.ok ? 'PASS' : 'FAIL',
      ms: res.ms,
      detail: res.detail,
      errors: res.errs,
      error: res.error,
      screenshot,
      ts: nowIso(),
    });
  }

  summary() {
    const total = this.results.length;
    const pass = this.results.filter(r => r.status === 'PASS').length;
    const fail = total - pass;
    return { total, pass, fail, consoleErrors: this.consoleErrors.length, pageErrors: this.pageErrors.length };
  }
}

// ---------- 截图工具 ----------
async function shoot(page, name) {
  const p = path.join(OUT_DIR, name + '.png');
  await page.screenshot({ path: p, fullPage: true });
  return p;
}

// ---------- 主流程 ----------
(async () => {
  const runner = new TestRunner();
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: VIEWPORT, locale: 'zh-CN' });
  const page = await context.newPage();

  // 全局错误累积
  page.on('console', (m) => {
    if (m.type() === 'error') runner.consoleErrors.push({ url: page.url(), text: m.text() });
  });
  page.on('pageerror', (e) => {
    runner.pageErrors.push({ url: page.url(), text: e.message, stack: e.stack });
  });

  // 对 confirm/alert 自动 dismiss（避免卡住）
  page.on('dialog', async (d) => { try { await d.dismiss(); } catch (e) { /* ignore */ } });

  console.log('==> 打开首页');
  await page.goto(BASE + '/', { waitUntil: 'load' });
  await page.waitForSelector('#nav .nav-item', { timeout: 5000 });
  // 等 /api/config 返回、home 渲染
  await page.waitForFunction(() => document.getElementById('listen-info') && document.getElementById('listen-info').textContent.indexOf('已启动') >= 0, null, { timeout: 5000 }).catch(() => {});

  // ========== 1. 首页 ==========
  console.log('==> [home] 首页');
  {
    const r = await runner.withStep(browser, page, async () => {
      // 截图首页
      await shoot(page, '01-home');
      // 检查 7 个工具卡片
      const cards = await page.$$('.tool-card');
      return { cards: cards.length };
    });
    runner.record(page, 'home/load', r, r.ok ? '01-home' : null);
  }

  // 遍历 8 个 home 卡片的跳转（v0.4 新增"环境自检"）
  const homeCards = [
    { sel: '.tool-card:has-text("WebSphere 日志助手")', route: 'websphere' },
    { sel: '.tool-card:has-text("文件下载")', route: 'files' },
    { sel: '.tool-card:has-text("报文格式化")', route: 'formatter' },
    { sel: '.tool-card:has-text("常用命令")', route: 'commands' },
    { sel: '.tool-card:has-text("环境自检")', route: 'diagnostics' },
    { sel: '.tool-card:has-text("系统配置")', route: 'config' },
    { sel: '.tool-card:has-text("下载历史")', route: 'downloads' },
    { sel: '.tool-card:has-text("操作历史")', route: 'history' },
  ];
  for (const c of homeCards) {
    const r = await runner.withStep(browser, page, async () => {
      await page.click(c.sel);
      await page.waitForFunction((h) => location.hash === h, '#/' + c.route);
      // 简单等一下渲染
      await page.waitForTimeout(150);
      return { hash: await page.evaluate(() => location.hash) };
    });
    runner.record(page, 'home/card-click/' + c.route, r, null);
    // 回首页
    await page.evaluate(() => { location.hash = '#/home'; });
    await page.waitForFunction(() => location.hash === '#/home');
    await page.waitForTimeout(100);
  }

  // ========== 2. WebSphere 日志助手 ==========
  console.log('==> [websphere] WebSphere 日志助手');
  {
    await page.click('a[data-route="websphere"]');
    await page.waitForFunction(() => location.hash === '#/websphere');
    await page.waitForSelector('#ws-sys', { timeout: 5000 });
    // 等服务器 checkbox 出现
    await page.waitForSelector('input[type="checkbox"][data-srv]', { timeout: 5000 });
  }

  // 2.1 系统选择
  {
    const r = await runner.withStep(browser, page, async () => {
      const opts = await page.$$eval('#ws-sys option', els => els.map(e => ({ v: e.value, t: e.textContent })));
      await page.selectOption('#ws-sys', SYS);
      await page.waitForTimeout(150);
      const srvCheckboxes = await page.$$('input[type="checkbox"][data-srv]');
      return { sysOptions: opts.length, serverCount: srvCheckboxes.length };
    });
    runner.record(page, 'websphere/select-system', r, null);
  }

  // 2.2 全选服务器
  {
    const r = await runner.withStep(browser, page, async () => {
      const before = await page.$$eval('input[type="checkbox"][data-srv]:checked', els => els.length);
      await page.click('button:has-text("全选")');
      await page.waitForTimeout(80);
      const after = await page.$$eval('input[type="checkbox"][data-srv]:checked', els => els.length);
      return { before, after };
    });
    runner.record(page, 'websphere/btn-pick-all', r, null);
  }
  // 2.3 全不选
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("全不选")');
      await page.waitForTimeout(80);
      const after = await page.$$eval('input[type="checkbox"][data-srv]:checked', els => els.length);
      return { after };
    });
    runner.record(page, 'websphere/btn-pick-none', r, null);
  }
  // 2.4 只选可用的（会空，因为还没测）
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("只选可用的")');
      await page.waitForTimeout(80);
      return { ok: true };
    });
    runner.record(page, 'websphere/btn-pick-online', r, null);
  }
  // 2.5 填密码、勾选 server
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#ws-user', USER);
      await page.fill('#ws-pass', PASS);
      // 勾选第一台
      await page.click('input[type="checkbox"][data-srv]');
      await page.waitForTimeout(80);
      return { checked: await page.$$eval('input[type="checkbox"][data-srv]:checked', els => els.length) };
    });
    runner.record(page, 'websphere/fill-creds', r, null);
  }
  // 2.6 测试连接
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("测试连接")');
      // 等状态变 ok
      await page.waitForSelector('span.dot-ok', { timeout: 8000 });
      await shoot(page, '02-websphere-test-ok');
      return { statusDot: 'ok' };
    });
    runner.record(page, 'websphere/btn-test', r, '02-websphere-test-ok');
  }
  // 2.7 列出文件
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("列出文件")');
      // 等文件表格
      await page.waitForSelector('table.table tbody tr', { timeout: 8000 });
      const rowCount = await page.$$eval('table.table tbody tr', els => els.length);
      await shoot(page, '03-websphere-file-list');
      return { rowCount };
    });
    runner.record(page, 'websphere/btn-list', r, '03-websphere-file-list');
  }
  // 2.8 全选 + 选前 N
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("全选")');
      await page.waitForTimeout(60);
      const allChecked = await page.$$eval('input[type="checkbox"][data-file]:checked', els => els.length);
      // 设选前 2
      await page.selectOption('#ws-dl-n', '2');
      await page.click('button:has-text("选前 ")');
      await page.waitForTimeout(60);
      const top2 = await page.$$eval('input[type="checkbox"][data-file]:checked', els => els.length);
      return { allChecked, top2 };
    });
    runner.record(page, 'websphere/file-pick-all-and-topn', r, null);
  }
  // 2.9 下载最近 N（指定文件版本，勾 zip 走指定下载）
  {
    const r = await runner.withStep(browser, page, async () => {
      // 走 /api/files/download：勾 zip + 已选 ≥2
      await page.check('#ws-dl-zip');
      await page.click('button:has-text("下载选中")');
      // 等 done（toast 或 status）
      await page.waitForSelector('span.dl-pct', { timeout: 8000 }).catch(() => {});
      // 等 row-done 出现
      await page.waitForSelector('tr.row-done', { timeout: 15000 }).catch(() => {});
      await page.waitForTimeout(2000);
      const doneCount = await page.$$eval('tr.row-done', els => els.length);
      await shoot(page, '04-websphere-files-downloaded');
      return { doneCount };
    });
    runner.record(page, 'websphere/btn-download-selected', r, '04-websphere-files-downloaded');
    // 取消按钮（如果还在跑）
    try { await page.click('button:has-text("停止")', { timeout: 500 }); } catch (e) { /* 已完成 */ }
  }
  // 2.10 搜索
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#ws-query', 'Exception');
      await page.selectOption('#ws-conc', '2');
      await page.click('button:has-text("搜索")');
      // 等搜索结果
      await page.waitForSelector('.server-group', { timeout: 10000 }).catch(() => {});
      await page.waitForTimeout(500);
      const groups = await page.$$('.server-group');
      const ctxButtons = await page.$$('button:has-text("上下文")');
      await shoot(page, '05-websphere-search');
      return { groups: groups.length, ctxButtons: ctxButtons.length };
    });
    runner.record(page, 'websphere/btn-search', r, '05-websphere-search');
  }
  // 2.11 上下文
  {
    const r = await runner.withStep(browser, page, async () => {
      const ctxBtn = await page.$('button:has-text("上下文")');
      if (!ctxBtn) return { skipped: 'no hit rows' };
      await ctxBtn.click();
      await page.waitForSelector('.context-view', { timeout: 8000 });
      const rows = await page.$$('.context-view .row');
      await shoot(page, '06-websphere-context');
      return { rows: rows.length };
    });
    runner.record(page, 'websphere/btn-context', r, '06-websphere-context');
  }
  // 2.12 下载最近 N（旧接口）
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.selectOption('#ws-dl-n', '1');
      await page.uncheck('#ws-dl-zip');
      // 上面第 2.9 步已构造过文件列表，但下载任务可能还在；先等清理
      await page.waitForTimeout(500);
      // 列表里要全选才能用「下载最近 N」吗？实际上下载最新是用 dlNSel.value 取 N，跟 fileTable 无关
      await page.click('button:has-text("下载"):not(:has-text("下载选中"))');
      // 这个按钮：textContent 包含「下载」但不包含「下载选中」也不是「下载最新」
      // 实际上代码里叫「下载」，点击会调 doDownload，按 dlNSel.value 取最新 N 个
      await page.waitForSelector('#ws-dl-results', { timeout: 12000 }).catch(() => {});
      await page.waitForTimeout(1500);
      const resultCards = await page.$$('.server-group');
      await shoot(page, '07-websphere-download-latest');
      return { serverGroupCount: resultCards.length };
    });
    runner.record(page, 'websphere/btn-download-latest', r, '07-websphere-download-latest');
  }
  // 2.13 实时 tail 启动 + 停止
  {
    const r = await runner.withStep(browser, page, async () => {
      // tailFileInp 默认 SystemOut.log
      await page.click('button:has-text("开始跟踪")');
      // 等 tailOut 出现⟦info⟧或⟦line⟧
      await page.waitForFunction(() => {
        const t = document.getElementById('ws-tail-out');
        return t && t.textContent.length > 0;
      }, null, { timeout: 8000 }).catch(() => {});
      await page.waitForTimeout(800);
      const txt = await page.$eval('#ws-tail-out', el => el.textContent);
      await shoot(page, '08-websphere-tail-started');
      // 停止：用 ws-tail-out 所在 card 内的「停止」按钮（避开下载的取消按钮）
      const stopInfo = await page.evaluate(() => {
        const out = document.getElementById('ws-tail-out');
        if (!out) return { error: 'no ws-tail-out' };
        const card = out.closest('.card');
        if (!card) return { error: 'no card' };
        const btns = Array.from(card.querySelectorAll('button'));
        const list = btns.map(b => ({ text: b.textContent, disabled: b.disabled }));
        return { btns: list };
      });
      // 找 tailCard 内未 disabled 且文本为「停止」的按钮
      const handle = await page.evaluateHandle(() => {
        const out = document.getElementById('ws-tail-out');
        const card = out.closest('.card');
        return Array.from(card.querySelectorAll('button')).find(b => b.textContent.trim() === '停止' && !b.disabled);
      });
      const el = handle.asElement();
      if (el) {
        await el.click();
        await page.waitForTimeout(300);
      } else {
        // 用 API 兜底：调 /api/logs/tail/<id>/stop
        try { await page.evaluate(async () => { try { await fetch('/api/logs/tail/stop', {method:'POST'}); } catch(e){} }); } catch (e) {}
      }
      return { tailOutLen: txt.length, preview: txt.slice(0, 120), stopInfo };
    });
    runner.record(page, 'websphere/tail-start-stop', r, '08-websphere-tail-started');
  }
  // 2.14 记住密码 + 忘记
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.check('#ws-remember');
      await page.waitForTimeout(150);
      const credStatus = await page.$('#ws-cred-status');
      const txt = credStatus ? await credStatus.textContent() : null;
      // 触发一次操作让保存走完
      await page.click('button:has-text("测试连接")');
      await page.waitForSelector('span.dot-ok', { timeout: 8000 });
      await page.waitForTimeout(300);
      // 忘记
      const forget = await page.$('button:has-text("忘记")');
      if (forget) {
        await forget.click();
        await page.waitForTimeout(300);
      }
      return { credStatus: txt };
    });
    runner.record(page, 'websphere/remember-and-forget', r, null);
  }

  // ========== 3. 文件下载（v0.3） ==========
  console.log('==> [files] 文件下载');
  {
    await page.click('a[data-route="files"]');
    await page.waitForFunction(() => location.hash === '#/files');
    await page.waitForSelector('#files-sys', { timeout: 5000 });
  }
  // 3.1 选系统 → 选服务器 → 填密码
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.selectOption('#files-sys', SYS);
      await page.waitForTimeout(150);
      const srvOpts = await page.$$eval('#files-srv option', els => els.map(e => e.value));
      await page.selectOption('#files-srv', SRV);
      await page.waitForTimeout(150);
      await page.fill('#files-user', USER);
      await page.fill('#files-pass', PASS);
      return { srvOpts };
    });
    runner.record(page, 'files/select-system-server', r, null);
  }
  // 3.2 连接并浏览
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("连接并浏览")');
      // 修复后：默认跳到 config 第一个 log_dirs[0].path = /opt/.../server1，第一次 connect 就 6 行
      await page.waitForSelector('table.table tbody tr', { timeout: 10000 });
      const rows = await page.$$('table.table tbody tr');
      // 验证默认路径正确（input 应显示 server1）
      const curPath = await page.$eval('input[placeholder^="输入绝对路径"]', el => el.value);
      await shoot(page, '09-files-connected');
      return { rowCount: rows.length, curPath, firstEntry: rows.length > 0 ? await rows[0].evaluate(el => el.querySelector('td:nth-child(2)').textContent) : null };
    });
    runner.record(page, 'files/btn-connect', r, '09-files-connected');
  }
  // 3.3 排序：点名称 / 大小（sortLink 触发 renderTable 重建，用 JS dispatch 绕过 stability 检查）
  {
    const r = await runner.withStep(browser, page, async () => {
      const getFirstName = () => page.$eval('table.table tbody tr:first-child td:nth-child(2)', el => el.textContent.trim());
      // 点名称 sortLink（升序）
      await page.evaluate(() => {
        const a = Array.from(document.querySelectorAll('th a')).find(x => x.textContent.indexOf('名称') >= 0);
        a.click();
      });
      await page.waitForTimeout(80);
      const firstNameAsc = await getFirstName();
      // 再点一次（降序）
      await page.evaluate(() => {
        const a = Array.from(document.querySelectorAll('th a')).find(x => x.textContent.indexOf('名称') >= 0);
        a.click();
      });
      await page.waitForTimeout(80);
      const firstNameDesc = await getFirstName();
      // 切到大小
      await page.evaluate(() => {
        const a = Array.from(document.querySelectorAll('th a')).find(x => x.textContent.indexOf('大小') >= 0);
        a.click();
      });
      await page.waitForTimeout(80);
      const firstSizeDesc = await getFirstName();
      return { firstNameAsc, firstNameDesc, firstSizeDesc };
    });
    runner.record(page, 'files/sort-name-size', r, null);
  }
  // 3.4 全选 / 取消选中
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("全选")');
      await page.waitForTimeout(60);
      const selText = await page.$eval('span.text-dim', el => el.textContent);
      const checked = await page.$$eval('input[type="checkbox"]:not([disabled]):checked', els => els.length);
      await page.click('button:has-text("取消选中")');
      await page.waitForTimeout(60);
      const after = await page.$$eval('input[type="checkbox"]:not([disabled]):checked', els => els.length);
      return { selText, checked, after };
    });
    runner.record(page, 'files/pick-all-none', r, null);
  }
  // 3.5 路径跳转（输入框 + 回车）
  {
    const r = await runner.withStep(browser, page, async () => {
      const pathInp = await page.$('input[placeholder^="输入绝对路径"]');
      await pathInp.fill('/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1');
      await pathInp.press('Enter');
      await page.waitForTimeout(400);
      const curPath = await page.$eval('input[placeholder^="输入绝对路径"]', el => el.value);
      const rows = await page.$$('table.table tbody tr');
      return { curPath, rowCount: rows.length };
    });
    runner.record(page, 'files/jump-path', r, null);
  }
  // 3.6 面包屑
  {
    const r = await runner.withStep(browser, page, async () => {
      // 面包屑里点 /
      const crumbLinks = await page.$$('.file-crumbs a');
      // 第一个 a 文本是 /
      await crumbLinks[0].click();
      await page.waitForTimeout(400);
      const curPath = await page.$eval('input[placeholder^="输入绝对路径"]', el => el.value);
      return { curPath };
    });
    runner.record(page, 'files/click-crumb-root', r, null);
  }
  // 3.7 上级目录
  {
    const r = await runner.withStep(browser, page, async () => {
      const pathInp = await page.$('input[placeholder^="输入绝对路径"]');
      await pathInp.fill('/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1');
      await pathInp.press('Enter');
      await page.waitForTimeout(400);
      await page.click('button:has-text("← 上级")');
      await page.waitForTimeout(400);
      const curPath = await page.$eval('input[placeholder^="输入绝对路径"]', el => el.value);
      return { curPath };
    });
    runner.record(page, 'files/btn-parent', r, null);
  }
  // 3.8 刷新
  {
    const r = await runner.withStep(browser, page, async () => {
      const before = await page.$$('table.table tbody tr');
      const beforeCount = before.length;
      await page.click('button:has-text("刷新")');
      await page.waitForTimeout(300);
      const afterCount = await page.$$eval('table.table tbody tr', els => els.length);
      return { beforeCount, afterCount };
    });
    runner.record(page, 'files/btn-refresh', r, null);
  }
  // 3.9 下载文件：勾一个 + 下载
  {
    const r = await runner.withStep(browser, page, async () => {
      // 跳到 server1 并选第一个文件
      const pathInp = await page.$('input[placeholder^="输入绝对路径"]');
      await pathInp.fill('/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1');
      await pathInp.press('Enter');
      await page.waitForTimeout(500);
      // 勾第一个非目录行
      const cb = await page.$('table.table tbody tr input[type="checkbox"]:not([disabled])');
      if (cb) await cb.click();
      await page.waitForTimeout(80);
      // 截图勾选后
      await shoot(page, '10-files-selected');
      // 下载
      const dlBtn = await page.$('button:has-text("下载选中")');
      const enabled = dlBtn && !(await dlBtn.isDisabled());
      if (enabled) {
        await dlBtn.click();
        await page.waitForSelector('span.dl-pct', { timeout: 8000 }).catch(() => {});
        await page.waitForSelector('tr.row-done, tr.row-fail', { timeout: 12000 }).catch(() => {});
        await page.waitForTimeout(1500);
        const done = await page.$$('tr.row-done');
        await shoot(page, '11-files-downloaded');
        return { enabled, done: done.length };
      }
      return { enabled: false };
    });
    runner.record(page, 'files/btn-download-selected', r, '11-files-downloaded');
  }
  // 3.10 取消下载按钮可见性
  {
    const r = await runner.withStep(browser, page, async () => {
      const cancelBtn = await page.$('button:has-text("取消下载")');
      const enabled = cancelBtn && !(await cancelBtn.isDisabled());
      return { cancelBtnExists: !!cancelBtn, enabled };
    });
    runner.record(page, 'files/cancel-button-state', r, null);
  }

  // ========== 4. 报文格式化 ==========
  console.log('==> [formatter] 报文格式化');
  {
    await page.click('a[data-route="formatter"]');
    await page.waitForFunction(() => location.hash === '#/formatter');
    await page.waitForSelector('#fmt-in', { timeout: 3000 });
  }
  // 4.1 JSON 格式化
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', '{"a":1,"b":[2,3],"c":{"d":4}}');
      await page.click('button:has-text("格式化 JSON")');
      await page.waitForFunction(() => document.getElementById('fmt-out').value.length > 0, null, { timeout: 5000 });
      const out = await page.$eval('#fmt-out', el => el.value);
      return { out };
    });
    runner.record(page, 'formatter/json-format', r, null);
  }
  // 4.2 JSON 压缩
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', '{\n  "a": 1,\n  "b": 2\n}');
      await page.click('button:has-text("压缩 JSON")');
      await page.waitForFunction(() => document.getElementById('fmt-out').value === '{"a":1,"b":2}', null, { timeout: 5000 });
      const out = await page.$eval('#fmt-out', el => el.value);
      return { out };
    });
    runner.record(page, 'formatter/json-minify', r, null);
  }
  // 4.3 JSON 校验
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', '{"a":1}');
      await page.click('button:has-text("校验 JSON")');
      // 校验不写 outTa，只弹 toast
      await page.waitForTimeout(400);
      const toastTxt = await page.$eval('#toast', el => el.textContent);
      return { toastTxt };
    });
    runner.record(page, 'formatter/json-validate', r, null);
  }
  // 4.4 JSON 校验失败
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', '{a:1}');
      await page.click('button:has-text("校验 JSON")');
      await page.waitForTimeout(400);
      const toastTxt = await page.$eval('#toast', el => el.textContent);
      const cls = await page.$eval('#toast', el => el.className);
      return { toastTxt, cls };
    });
    runner.record(page, 'formatter/json-validate-bad', r, null);
  }
  // 4.5 XML 格式化
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', '<a><b>1</b><c>2</c></a>');
      await page.click('button:has-text("格式化 XML")');
      await page.waitForFunction(() => document.getElementById('fmt-out').value.length > 0, null, { timeout: 5000 });
      const out = await page.$eval('#fmt-out', el => el.value);
      return { out };
    });
    runner.record(page, 'formatter/xml-format', r, null);
  }
  // 4.6 XML 压缩
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', '<a>\n  <b>1</b>\n  <c>2</c>\n</a>');
      await page.click('button:has-text("压缩 XML")');
      await page.waitForFunction(() => document.getElementById('fmt-out').value.length > 0, null, { timeout: 5000 });
      const out = await page.$eval('#fmt-out', el => el.value);
      return { out };
    });
    runner.record(page, 'formatter/xml-minify', r, null);
  }
  // 4.7 清空
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('#fmt-in', 'x');
      await page.click('button:has-text("清空")');
      await page.waitForTimeout(80);
      const inV = await page.$eval('#fmt-in', el => el.value);
      const outV = await page.$eval('#fmt-out', el => el.value);
      return { inV, outV };
    });
    runner.record(page, 'formatter/btn-clear', r, null);
  }
  // 4.8 复制结果（用 navigator.clipboard 兼容 fallback）
  {
    const r = await runner.withStep(browser, page, async () => {
      // 给 clipboard 权限
      await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE });
      await page.fill('#fmt-in', '{"k":1}');
      await page.click('button:has-text("格式化 JSON")');
      await page.waitForFunction(() => document.getElementById('fmt-out').value.length > 0, null, { timeout: 5000 });
      await page.click('button:has-text("复制结果")');
      await page.waitForTimeout(300);
      const toastTxt = await page.$eval('#toast', el => el.textContent);
      return { toastTxt };
    });
    runner.record(page, 'formatter/btn-copy', r, null);
  }
  // 4.9 整体截图
  {
    const r = await runner.withStep(browser, page, async () => {
      await shoot(page, '12-formatter');
      return { ok: true };
    });
    runner.record(page, 'formatter/screenshot', r, '12-formatter');
  }

  // ========== 5. 常用命令（占位） ==========
  console.log('==> [commands] 常用命令（占位）');
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('a[data-route="commands"]');
      await page.waitForFunction(() => location.hash === '#/commands');
      await page.waitForTimeout(150);
      const html = await page.$eval('#view', el => el.innerText);
      await shoot(page, '13-commands-placeholder');
      return { preview: html.slice(0, 100) };
    });
    runner.record(page, 'commands/placeholder', r, '13-commands-placeholder');
  }

  // ========== 6. 系统配置（不真的保存，只检查渲染和放弃改动） ==========
  console.log('==> [config] 系统配置');
  {
    await page.click('a[data-route="config"]');
    await page.waitForFunction(() => location.hash === '#/config');
    await page.waitForSelector('.sys-block', { timeout: 5000 });
  }
  // 6.1 整体结构
  {
    const r = await runner.withStep(browser, page, async () => {
      const sysBlocks = await page.$$('.sys-block');
      const srvBlocks = await page.$$('.srv-block');
      const dirBlocks = await page.$$('.dir-block');
      await shoot(page, '14-config');
      return { sys: sysBlocks.length, srv: srvBlocks.length, dir: dirBlocks.length };
    });
    runner.record(page, 'config/structure', r, '14-config');
  }
  // 6.2 新增业务系统按钮存在 + 验证能改
  {
    const r = await runner.withStep(browser, page, async () => {
      const before = await page.$$('.sys-block');
      // 这里**不点** + 新增业务系统（避免破坏数据），只检查按钮可点
      const btn = await page.$('button:has-text("+ 新增业务系统")');
      return { btnExists: !!btn, sysCount: before.length };
    });
    runner.record(page, 'config/check-add-system-btn', r, null);
  }
  // 6.3 输入框修改触发 dirty
  {
    const r = await runner.withStep(browser, page, async () => {
      const nameInp = await page.$('.sys-name-input input');
      await nameInp.fill(nameInp.value + ' (测试)');
      await page.waitForTimeout(100);
      const saveBtn = await page.$('#cfg-save-btn');
      const enabled = saveBtn && !(await saveBtn.isDisabled());
      // 立刻放弃改动
      await page.click('button:has-text("放弃改动")');
      // confirm 自动 dismiss（dismiss = cancel），所以应保持原值
      // 但代码里 doReset 是 if (!state.dirty || confirm(...))，dismiss 后 confirm=false，
      // state.dirty=true 时不进 if；先点放弃，确认弹窗 dialog 自动 dismiss，所以不会重置
      // 再点一次覆盖状态
      await page.click('button:has-text("放弃改动")');
      await page.waitForTimeout(200);
      return { saveEnabled: enabled };
    });
    runner.record(page, 'config/dirty-and-reset', r, null);
  }
  // 6.4 重新拉一次确保系统配置无破坏
  {
    const r = await runner.withStep(browser, page, async () => {
      // 强制重新进入
      await page.evaluate(() => { location.hash = '#/home'; });
      await page.waitForFunction(() => location.hash === '#/home');
      await page.click('a[data-route="config"]');
      await page.waitForFunction(() => location.hash === '#/config');
      await page.waitForSelector('.sys-block', { timeout: 5000 });
      const sysBlocks = await page.$$('.sys-block');
      return { sysCount: sysBlocks.length };
    });
    runner.record(page, 'config/reload', r, null);
  }

  // ========== 7. 下载历史 ==========
  console.log('==> [downloads] 下载历史');
  {
    await page.click('a[data-route="downloads"]');
    await page.waitForFunction(() => location.hash === '#/downloads');
    await page.waitForSelector('table.table, .text-dim', { timeout: 5000 });
  }
  // 7.1 加载列表
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.waitForSelector('table.table tbody tr', { timeout: 5000 }).catch(() => {});
      const rows = await page.$$('table.table tbody tr');
      const summary = await page.$eval('.text-dim', el => el.textContent).catch(() => null);
      await shoot(page, '15-downloads');
      return { rowCount: rows.length, summary };
    });
    runner.record(page, 'downloads/load', r, '15-downloads');
  }
  // 7.2 刷新
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.click('button:has-text("刷新")');
      await page.waitForTimeout(400);
      const rows = await page.$$('table.table tbody tr');
      return { rowCount: rows.length };
    });
    runner.record(page, 'downloads/btn-refresh', r, null);
  }
  // 7.3 系统过滤
  {
    const r = await runner.withStep(browser, page, async () => {
      // 选第一个非空 option
      const opts = await page.$$eval('select', els => els.map(e => Array.from(e.options).map(o => o.value)));
      // 第一个 select 是系统
      const sysOpts = opts[0] || [];
      if (sysOpts.length > 1) {
        await page.selectOption('select >> nth=0', sysOpts[1]);
        await page.waitForTimeout(400);
      }
      const rows = await page.$$('table.table tbody tr');
      return { sysOpts, rowCount: rows.length };
    });
    runner.record(page, 'downloads/filter-system', r, null);
  }
  // 7.4 清空全部按钮存在（不真的点，confirm 会自动 dismiss）
  {
    const r = await runner.withStep(browser, page, async () => {
      const btn = await page.$('button:has-text("清空全部")');
      return { btnExists: !!btn };
    });
    runner.record(page, 'downloads/check-clear-all-btn', r, null);
  }
  // 7.5 删除按钮存在
  {
    const r = await runner.withStep(browser, page, async () => {
      const delBtns = await page.$$('button:has-text("删除")');
      return { count: delBtns.length };
    });
    runner.record(page, 'downloads/check-delete-btns', r, null);
  }

  // ========== 8. 操作历史 ==========
  console.log('==> [history] 操作历史');
  {
    await page.click('a[data-route="history"]');
    await page.waitForFunction(() => location.hash === '#/history');
    await page.waitForSelector('table.table, .text-dim', { timeout: 5000 });
  }
  // 8.1 加载（进入时已自动 loadHistory）
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.waitForSelector('table.table tbody tr', { timeout: 5000 }).catch(() => {});
      const rows = await page.$$('table.table tbody tr');
      const headTxt = await page.$eval('.card h3, .p-3', el => el.textContent).catch(() => null);
      await shoot(page, '16-history');
      return { rowCount: rows.length, headTxt };
    });
    runner.record(page, 'history/load', r, '16-history');
  }
  // 8.2 操作类型过滤
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.selectOption('#hist-op', 'ssh.test');
      await page.click('button:has-text("刷新")');
      await page.waitForTimeout(400);
      const rows = await page.$$('table.table tbody tr');
      const opCells = await page.$$eval('table.table tbody tr td:nth-child(2)', els => els.map(e => e.textContent));
      return { rowCount: rows.length, ops: Array.from(new Set(opCells)) };
    });
    runner.record(page, 'history/filter-op', r, null);
  }
  // 8.3 自动刷新切换
  {
    const r = await runner.withStep(browser, page, async () => {
      const btn = await page.$('button:has-text("自动刷新")');
      const before = await btn.textContent();
      await btn.click();
      await page.waitForTimeout(150);
      const after1 = await page.$eval('button:has-text("自动刷新")', el => el.textContent);
      await page.click('button:has-text("自动刷新")');
      await page.waitForTimeout(150);
      const after2 = await page.$eval('button:has-text("自动刷新")', el => el.textContent);
      return { before, after1, after2 };
    });
    runner.record(page, 'history/toggle-auto', r, null);
  }
  // 8.4 系统/服务器过滤
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.fill('input[placeholder*="系统"]', '信贷');
      await page.click('button:has-text("刷新")');
      await page.waitForTimeout(400);
      const rows = await page.$$('table.table tbody tr');
      return { rowCount: rows.length };
    });
    runner.record(page, 'history/filter-system-server', r, null);
  }

  // ========== 全页截图最终 ==========
  {
    const r = await runner.withStep(browser, page, async () => {
      await page.evaluate(() => { location.hash = '#/home'; });
      await page.waitForFunction(() => location.hash === '#/home');
      await page.waitForTimeout(200);
      await shoot(page, '00-final-home');
      return { ok: true };
    });
    runner.record(page, 'final/home-screenshot', r, '00-final-home');
  }

  // ---------- 收尾 ----------
  await context.close();
  await browser.close();

  const sum = runner.summary();
  const report = {
    base: BASE,
    run_at: nowIso(),
    summary: sum,
    results: runner.results,
    consoleErrors: runner.consoleErrors,
    pageErrors: runner.pageErrors,
  };
  fs.writeFileSync(REPORT, JSON.stringify(report, null, 2));
  console.log('==> 报告已写入', REPORT);
  console.log(JSON.stringify(sum, null, 2));
  if (sum.fail > 0) {
    console.log('失败明细：');
    runner.results.filter(r => r.status === 'FAIL').forEach(r => {
      console.log(' -', r.action, '->', r.error, r.detail);
    });
  }
  process.exit(sum.fail > 0 ? 1 : 0);
})().catch(e => {
  console.error('测试脚本异常:', e);
  process.exit(2);
});
