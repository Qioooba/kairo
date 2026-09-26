/*
 * 系统配置页面深度点击测试
 *
 * 测试目标：覆盖每个按钮、每个输入框、每个交互
 * 收集：console错误、页面截图、网络请求/响应、DOM状态
 */
'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://127.0.0.1:18092';
const OUT_DIR = path.join(__dirname, 'config_test_shots');
const REPORT = path.join(__dirname, 'config_test_report.json');

if (!fs.existsSync(OUT_DIR)) fs.mkdirSync(OUT_DIR, { recursive: true });

const results = [];
const consoleMsgs = [];
const pageErrors = [];
const networkLogs = [];

function log(msg) { console.log(`[${new Date().toISOString()}] ${msg}`); }

async function step(name, fn) {
  const t0 = Date.now();
  const r = { name, status: 'pending', ms: 0, error: null, detail: null };
  try {
    const detail = await fn();
    r.status = 'pass';
    r.detail = detail || null;
  } catch (e) {
    r.status = 'fail';
    r.error = e.message;
    r.detail = e.stack;
  }
  r.ms = Date.now() - t0;
  results.push(r);
  log(`${r.status.toUpperCase()} [${r.ms}ms] ${name}${r.error ? ' :: ' + r.error : ''}`);
  return r;
}

(async () => {
  log('启动浏览器...');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();

  // 收集 console
  page.on('console', msg => {
    consoleMsgs.push({ type: msg.type(), text: msg.text() });
    if (msg.type() === 'error') log('CONSOLE.ERROR: ' + msg.text());
  });
  page.on('pageerror', err => {
    pageErrors.push({ name: err.name, message: err.message, stack: err.stack });
    log('PAGE.ERROR: ' + err.message);
  });
  page.on('request', req => {
    if (req.url().includes('/api/')) {
      networkLogs.push({ method: req.method(), url: req.url(), postData: req.postData() });
    }
  });
  page.on('response', async resp => {
    if (resp.url().includes('/api/')) {
      let body = '';
      try { body = await resp.text(); } catch (e) {}
      networkLogs.push({ status: resp.status(), url: resp.url(), body: body.substring(0, 500) });
    }
  });

  // ===== 1. 进入页面 =====
  await step('打开系统配置页面', async () => {
    await page.goto(`${BASE}/#/config`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);
    await page.screenshot({ path: path.join(OUT_DIR, '01-initial.png'), fullPage: true });
    // v1.x 去顶栏后不再有面包屑 #crumbs，当前页名看 Tab 条的活动 Tab
    const crumbs = await page.textContent('.app-tab.active .app-tab-label');
    if (!crumbs || !crumbs.includes('系统配置')) throw new Error('当前页名不正确: ' + crumbs);
    return '当前页(Tab): ' + crumbs;
  });

  // ===== 2. 检查首屏各卡片 =====
  await step('检查首屏卡片结构', async () => {
    const cards = await page.$$eval('.card', els => els.map(e => {
      const h3 = e.querySelector('h3');
      return h3 ? h3.textContent : '(无标题)';
    }));
    if (cards.length < 4) throw new Error('卡片数量不足: ' + cards.length);
    return '卡片: ' + cards.join(' | ');
  });

  // ===== 3. 检查顶部操作条按钮 =====
  await step('检查顶部操作条按钮', async () => {
    const btns = await page.$$('.btn-row .btn');
    const texts = [];
    for (const b of btns) texts.push(await b.textContent());
    const expected = ['+ 新增业务系统', '导出配置', '导入配置', '放弃改动', '保存'];
    for (const e of expected) {
      if (!texts.find(t => t.includes(e))) throw new Error('缺少按钮: ' + e);
    }
    // 检查保存按钮初始disabled
    const saveBtn = await page.$('#cfg-save-btn');
    const isDisabled = await saveBtn.isDisabled();
    if (!isDisabled) throw new Error('保存按钮初始应disabled');
    return '按钮齐全: ' + texts.join(', ');
  });

  // ===== 4. 检查应用自身配置只读卡 =====
  await step('检查应用自身配置只读卡', async () => {
    const table = await page.$('.card .table');
    if (!table) throw new Error('应用配置表格未找到');
    const rows = await table.$$eval('tr', trs => trs.map(t => t.textContent.trim()));
    return '行数: ' + rows.length;
  });

  // ===== 5. 测试新增业务系统 =====
  await step('点击"+新增业务系统"按钮', async () => {
    const btn = await page.$('button:text("+ 新增业务系统")');
    if (!btn) throw new Error('未找到新增按钮');
    await btn.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT_DIR, '02-after-add-sys.png'), fullPage: true });
    const blocks = await page.$$('.sys-block');
    if (blocks.length < 2) throw new Error('新增后系统块数量不对: ' + blocks.length);
    // 检查保存按钮是否enabled
    const saveBtn = await page.$('#cfg-save-btn');
    const isDisabled = await saveBtn.isDisabled();
    if (isDisabled) throw new Error('改动后保存按钮应enabled');
    return '系统块数量: ' + blocks.length;
  });

  // ===== 6. 测试系统上移/下移 =====
  await step('测试系统下移按钮', async () => {
    // 获取第一个系统的名称
    const firstSys = await page.$('.sys-block .sys-name-input input');
    await firstSys.fill('TestSys-A');
    await page.waitForTimeout(200);
    
    // 点下移
    const btnDown = await page.$('.sys-block:first-child .sys-actions button:text("↓")');
    if (!btnDown) throw new Error('未找到下移按钮');
    const isDisabled = await btnDown.isDisabled();
    if (isDisabled) throw new Error('下移按钮不应disabled');
    await btnDown.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT_DIR, '03-after-move-down.png'), fullPage: true });
    return '下移完成';
  });

  await step('测试系统上移按钮', async () => {
    const btnUp = await page.$('.sys-block:nth-child(2) .sys-actions button:text("↑")');
    if (!btnUp) throw new Error('未找到上移按钮');
    await btnUp.click();
    await page.waitForTimeout(300);
    return '上移完成';
  });

  // ===== 7. 测试系统复制 =====
  await step('测试系统复制按钮', async () => {
    // 找到 TestSys-A 的复制按钮
    const sysBlocks = await page.$$('.sys-block');
    let targetIdx = -1;
    for (let i = 0; i < sysBlocks.length; i++) {
      const inp = await sysBlocks[i].$('.sys-name-input input');
      const val = await inp.inputValue();
      if (val === 'TestSys-A') { targetIdx = i; break; }
    }
    if (targetIdx === -1) throw new Error('未找到TestSys-A');
    
    const btnDup = await sysBlocks[targetIdx].$('.sys-actions button:text("复制")');
    await btnDup.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT_DIR, '04-after-dup-sys.png'), fullPage: true });
    
    const blocks = await page.$$('.sys-block');
    // 检查复制后的名称
    const newInp = await blocks[targetIdx + 1].$('.sys-name-input input');
    const newName = await newInp.inputValue();
    if (!newName.includes('-copy')) throw new Error('复制后名称未加-copy: ' + newName);
    return '复制后名称: ' + newName;
  });

  // ===== 8. 测试新增服务器 =====
  await step('测试新增服务器按钮', async () => {
    const btnAddSrv = await page.$('.sys-block:first-child button:text("+ 新增服务器")');
    if (!btnAddSrv) throw new Error('未找到新增服务器按钮');
    await btnAddSrv.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT_DIR, '05-after-add-srv.png'), fullPage: true });
    const srvBlocks = await page.$$('.sys-block:first-child .srv-block');
    return '服务器块数: ' + srvBlocks.length;
  });

  // ===== 9. 测试服务器上移/下移/复制 =====
  await step('测试服务器复制按钮', async () => {
    const btnSrvDup = await page.$('.sys-block:first-child .srv-block:first-child button:text("📋 复制服务器（含日志目录）")');
    if (!btnSrvDup) throw new Error('未找到服务器复制按钮');
    await btnSrvDup.click();
    await page.waitForTimeout(300);
    const srvBlocks = await page.$$('.sys-block:first-child .srv-block');
    return '复制后服务器块数: ' + srvBlocks.length;
  });

  // ===== 10. 测试新增日志目录 =====
  await step('测试新增日志目录按钮', async () => {
    const btnAddDir = await page.$('.sys-block:first-child .srv-block:first-child button:text("+ 新增日志目录")');
    if (!btnAddDir) throw new Error('未找到新增日志目录按钮');
    await btnAddDir.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT_DIR, '06-after-add-dir.png'), fullPage: true });
    const dirBlocks = await page.$$('.sys-block:first-child .srv-block:first-child .dir-block');
    return '日志目录块数: ' + dirBlocks.length;
  });

  // ===== 11. 测试日志目录复制 =====
  await step('测试日志目录复制按钮', async () => {
    const btnDirDup = await page.$('.sys-block:first-child .srv-block:first-child .dir-block:first-child button:text("复制目录")');
    if (!btnDirDup) throw new Error('未找到目录复制按钮');
    await btnDirDup.click();
    await page.waitForTimeout(300);
    const dirBlocks = await page.$$('.sys-block:first-child .srv-block:first-child .dir-block');
    return '复制后日志目录块数: ' + dirBlocks.length;
  });

  // ===== 12. 测试编码下拉框 =====
  await step('测试编码下拉框切换', async () => {
    const encSel = await page.$('.sys-block:first-child .srv-block:first-child .dir-block:first-child select');
    if (!encSel) throw new Error('未找到编码下拉框');
    await encSel.selectOption('gbk');
    await page.waitForTimeout(200);
    const val = await encSel.inputValue();
    if (val !== 'gbk') throw new Error('切换gbk失败: ' + val);
    await encSel.selectOption('utf-8');
    return '编码切换正常';
  });

  // ===== 13. 测试文件名规则输入 =====
  await step('测试文件名规则textarea', async () => {
    const ta = await page.$('.sys-block:first-child .srv-block:first-child .dir-block:first-child textarea');
    if (!ta) throw new Error('未找到文件名规则textarea');
    await ta.fill('*.log\nerror_*.txt\nSystemOut*.log');
    await page.waitForTimeout(200);
    const val = await ta.inputValue();
    if (!val.includes('*.log')) throw new Error('textarea内容不正确');
    return '规则行数: ' + val.split('\n').length;
  });

  // ===== 14. 测试放弃改动 =====
  await step('点击"放弃改动"按钮', async () => {
    // 监听dialog（只注册一次）
    page.removeAllListeners('dialog');
    page.on('dialog', d => d.accept());
    const btnReset = await page.$('button:text("放弃改动")');
    if (!btnReset) throw new Error('未找到放弃改动按钮');
    await btnReset.click();
    await page.waitForTimeout(1000); // 等待重新加载
    await page.screenshot({ path: path.join(OUT_DIR, '07-after-reset.png'), fullPage: true });
    
    // 检查保存按钮是否再次disabled
    const saveBtn = await page.$('#cfg-save-btn');
    const isDisabled = await saveBtn.isDisabled();
    if (!isDisabled) throw new Error('放弃改动后保存按钮应disabled');
    return '放弃成功，保存按钮已disabled';
  });

  // ===== 15. 测试导出配置 =====
  await step('点击"导出配置"按钮', async () => {
    const [download] = await Promise.all([
      page.waitForEvent('download', { timeout: 5000 }).catch(() => null),
      page.click('button:text("导出配置")')
    ]);
    await page.waitForTimeout(500);
    // 检查是否有toast
    const toast = await page.textContent('#toast');
    return download ? '下载触发: ' + (download.suggestedFilename()) : 'toast: ' + toast;
  });

  // ===== 16. 测试外部打开器-添加 =====
  await step('点击外部打开器"+添加打开器"', async () => {
    const btn = await page.$('button:text("+ 添加打开器")');
    if (!btn) throw new Error('未找到添加打开器按钮');
    await btn.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT_DIR, '08-after-add-opener.png'), fullPage: true });
    const rows = await page.$$('.opener-row');
    if (rows.length === 0) throw new Error('添加后无行');
    return '打开器行数: ' + rows.length;
  });

  // ===== 17. 测试打开器名称输入+图标推断 =====
  await step('测试打开器图标自动推断', async () => {
    const nameInp = await page.$('.opener-row input[placeholder*="Notepad++"]');
    if (!nameInp) throw new Error('未找到名称输入框');
    await nameInp.fill('Notepad++');
    await page.waitForTimeout(300);
    const pathInp = await page.$('.opener-row input[placeholder*="notepad.exe"]');
    await pathInp.fill('C:\\Windows\\notepad.exe');
    await page.waitForTimeout(300);
    const iconSpan = await page.$('.opener-row span[style*="font-size:20px"]');
    const icon = await iconSpan.textContent();
    return '图标: ' + icon;
  });

  // ===== 18. 测试打开器保存 =====
  await step('点击打开器保存按钮', async () => {
    const btnSave = await page.$('.opener-row button:text("💾 保存")');
    if (!btnSave) throw new Error('未找到保存按钮');
    await btnSave.click();
    await page.waitForTimeout(1000);
    await page.screenshot({ path: path.join(OUT_DIR, '09-after-save-opener.png'), fullPage: true });
    const toast = await page.textContent('#toast');
    return 'toast: ' + toast;
  });

  // ===== 19. 测试打开器保存空名称 =====
  await step('测试打开器保存空名称（应报错）', async () => {
    await page.click('button:text("+ 添加打开器")');
    await page.waitForTimeout(300);
    const saveBtns = await page.$$('.opener-row button:text("💾 保存")');
    await saveBtns[saveBtns.length - 1].click();
    await page.waitForTimeout(500);
    const toast = await page.textContent('#toast');
    if (!toast.includes('失败')) throw new Error('空名称应报错: ' + toast);
    return '正确报错: ' + toast;
  });

  // ===== 20. 测试下载历史清理-保留天数 =====
  await step('测试下载历史保留天数输入', async () => {
    const inputs = await page.$$('input[type="number"]');
    // 找到placeholder=7的
    let daysInp = null;
    for (const inp of inputs) {
      const ph = await inp.getAttribute('placeholder');
      if (ph === '7') { daysInp = inp; break; }
    }
    if (!daysInp) throw new Error('未找到保留天数输入框');
    await daysInp.fill('30');
    await page.waitForTimeout(200);
    const val = await daysInp.inputValue();
    if (val !== '30') throw new Error('输入失败: ' + val);
    // 检查保存按钮是否enabled
    const saveBtn = await page.$('#cfg-save-btn');
    const isDisabled = await saveBtn.isDisabled();
    if (isDisabled) throw new Error('改动后保存按钮应enabled');
    return '保留天数: ' + val;
  });

  // ===== 21. 测试保留天数为0 =====
  await step('测试保留天数设为0', async () => {
    const inputs = await page.$$('input[type="number"]');
    let daysInp = null;
    for (const inp of inputs) {
      const ph = await inp.getAttribute('placeholder');
      if (ph === '7') { daysInp = inp; break; }
    }
    await daysInp.fill('0');
    await page.waitForTimeout(200);
    return '设为0成功';
  });

  // ===== 22. 测试保存（会触发PUT /api/admin/download-retention） =====
  await step('点击保存按钮', async () => {
    const saveBtn = await page.$('#cfg-save-btn');
    if (await saveBtn.isDisabled()) throw new Error('保存按钮不应disabled');
    await saveBtn.click();
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(OUT_DIR, '10-after-save.png'), fullPage: true });
    const toast = await page.textContent('#toast');
    return 'toast: ' + toast;
  });

  // ===== 23. 测试警告横幅关闭 =====
  await step('检查警告横幅', async () => {
    const banner = await page.$('.warn-banner');
    if (!banner) return '无横幅显示';
    const display = await banner.evaluate(e => getComputedStyle(e).display);
    if (display === 'none') return '横幅隐藏';
    
    const closeBtn = await page.$('.warn-banner-close');
    if (closeBtn) {
      await closeBtn.click();
      await page.waitForTimeout(300);
      const displayAfter = await banner.evaluate(e => getComputedStyle(e).display);
      return '关闭后display: ' + displayAfter;
    }
    return '横幅显示但无关闭按钮';
  });

  // ===== 24. 测试底部保存按钮 =====
  await step('检查底部保存按钮', async () => {
    const footerBtns = await page.$$('.cfg-save-footer-bar .btn');
    if (footerBtns.length < 2) throw new Error('底部按钮数量不对: ' + footerBtns.length);
    const texts = [];
    for (const b of footerBtns) texts.push(await b.textContent());
    return '底部按钮: ' + texts.join(', ');
  });

  // ===== 25. 测试删除打开器 =====
  await step('测试删除打开器', async () => {
    page.removeAllListeners('dialog');
    page.on('dialog', d => d.accept());
    const delBtns = await page.$$('.opener-row button.btn-danger:text("删除")');
    if (delBtns.length === 0) return '无打开器可删';
    const beforeCount = await page.$$eval('.opener-row', els => els.length);
    await delBtns[delBtns.length - 1].click();
    await page.waitForTimeout(500);
    const afterCount = await page.$$eval('.opener-row', els => els.length);
    return '删除前: ' + beforeCount + ' → 删除后: ' + afterCount;
  });

  // ===== 26. 测试响应式布局 =====
  await step('测试窄屏响应式布局', async () => {
    await page.setViewportSize({ width: 800, height: 900 });
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(OUT_DIR, '11-narrow-screen.png'), fullPage: true });
    await page.setViewportSize({ width: 1440, height: 900 });
    return '已截图窄屏布局';
  });

  // ===== 27. 测试离开页面再回来（state持久化） =====
  await step('切换到首页再切回配置页', async () => {
    await page.goto(`${BASE}/#/home`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(500);
    await page.goto(`${BASE}/#/config`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    const cards = await page.$$('.card');
    if (cards.length < 4) throw new Error('切回后卡片丢失');
    return '切回后卡片数: ' + cards.length;
  });

  // ===== 28. 检查控制台错误 =====
  await step('检查控制台错误', async () => {
    const errors = consoleMsgs.filter(m => m.type === 'error');
    return 'console错误数: ' + errors.length + (errors.length > 0 ? ' :: ' + errors.map(e => e.text).join(' | ') : '');
  });

  await browser.close();

  // 写报告
  const report = {
    testTime: new Date().toISOString(),
    summary: {
      total: results.length,
      passed: results.filter(r => r.status === 'pass').length,
      failed: results.filter(r => r.status === 'fail').length,
    },
    consoleErrors: consoleMsgs.filter(m => m.type === 'error'),
    pageErrors,
    results,
    networkApiCalls: networkLogs.length,
  };
  fs.writeFileSync(REPORT, JSON.stringify(report, null, 2));
  
  log(`\n========== 测试完成 ==========`);
  log(`总计: ${report.summary.total} | 通过: ${report.summary.passed} | 失败: ${report.summary.failed}`);
  log(`控制台错误: ${report.consoleErrors.length} | 页面错误: ${pageErrors.length}`);
  log(`API调用次数: ${report.networkApiCalls}`);
  log(`报告已写入: ${REPORT}`);
  log(`截图目录: ${OUT_DIR}`);
  
  if (report.summary.failed > 0) {
    log(`\n失败项:`);
    results.filter(r => r.status === 'fail').forEach(r => {
      log(`  - ${r.name}: ${r.error}`);
    });
  }
  
  process.exit(report.summary.failed > 0 ? 1 : 0);
})().catch(e => {
  log('测试运行异常: ' + e.message);
  console.error(e);
  process.exit(2);
});
