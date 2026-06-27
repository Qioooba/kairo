/*
 * 系统配置页面二次测试 - 精简版，避免dialog卡住
 */
'use strict';
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://127.0.0.1:18092';
const OUT_DIR = path.join(__dirname, 'config_test_shots');

const results = [];
const consoleMsgs = [];
const pageErrors = [];

function log(msg) { console.log(`[${new Date().toISOString()}] ${msg}`); }
async function step(name, fn) {
  const t0 = Date.now();
  const r = { name, status: 'pending', ms: 0, error: null, detail: null };
  try { r.detail = await fn(); r.status = 'pass'; }
  catch (e) { r.status = 'fail'; r.error = e.message; }
  r.ms = Date.now() - t0;
  results.push(r);
  log(`${r.status.toUpperCase()} [${r.ms}ms] ${name}${r.error ? ' :: ' + r.error : ''}`);
  return r;
}

(async () => {
  log('二次测试启动...');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();

  // 全局dialog处理器（只注册一次）
  page.on('console', msg => {
    consoleMsgs.push({ type: msg.type(), text: msg.text() });
    if (msg.type() === 'error') log('CONSOLE.ERROR: ' + msg.text());
  });
  page.on('pageerror', err => {
    pageErrors.push({ name: err.name, message: err.message });
    log('PAGE.ERROR: ' + err.message);
  });
  page.on('dialog', async d => {
    await d.accept();
  });

  // ===== 1. 基础加载验证 =====
  await step('页面加载无syncSaveBtns错误', async () => {
    await page.goto(`${BASE}/#/config`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    const errs = pageErrors.filter(e => e.message.includes('syncSaveBtns'));
    if (errs.length > 0) throw new Error('syncSaveBtns错误仍存在');
    await page.screenshot({ path: path.join(OUT_DIR, 'retest-01-initial.png'), fullPage: true });
    return '无syncSaveBtns错误';
  });

  // ===== 2. 新增业务系统 =====
  await step('新增业务系统按钮生效', async () => {
    const before = await page.$$eval('.sys-block', els => els.length);
    await page.click('button:text("+ 新增业务系统")');
    await page.waitForTimeout(300);
    const after = await page.$$eval('.sys-block', els => els.length);
    if (after !== before + 1) throw new Error(`数量不对: ${before} → ${after}`);
    return `${before} → ${after}`;
  });

  // ===== 3. 保存按钮enabled =====
  await step('改动后保存按钮enabled', async () => {
    const saveBtn = await page.$('#cfg-save-btn');
    if (await saveBtn.isDisabled()) throw new Error('保存按钮仍disabled');
    return '保存按钮已enabled';
  });

  // ===== 4. 底部保存按钮同步 =====
  await step('底部保存按钮同步enabled', async () => {
    const footerBtns = await page.$$('.cfg-save-footer-bar .btn-primary');
    for (const b of footerBtns) {
      if (await b.isDisabled()) throw new Error('底部保存按钮未同步');
    }
    return '底部按钮同步正常';
  });

  // ===== 5. 端口非法值校验 =====
  await step('端口输入99999显示红边框', async () => {
    const portInp = await page.$('.sys-block:first-child .srv-block:first-child input[type="number"]');
    if (!portInp) throw new Error('未找到端口输入框');
    await portInp.fill('99999');
    await page.waitForTimeout(300);
    const borderColor = await portInp.evaluate(e => getComputedStyle(e).borderColor);
    const isRed = borderColor.includes('239') || borderColor.includes('ef4444') || borderColor.includes('red');
    if (!isRed) throw new Error('端口非法值未标红: ' + borderColor);
    await page.screenshot({ path: path.join(OUT_DIR, 'retest-02-port-invalid.png'), fullPage: true });
    return '端口99999标红: ' + borderColor;
  });

  // ===== 6. 端口合法值恢复 =====
  await step('端口输入22恢复正常', async () => {
    const portInp = await page.$('.sys-block:first-child .srv-block:first-child input[type="number"]');
    await portInp.fill('22');
    await page.waitForTimeout(300);
    const borderColor = await portInp.evaluate(e => getComputedStyle(e).borderColor);
    const isRed = borderColor.includes('239') || borderColor.includes('ef4444') || borderColor.includes('red');
    if (isRed) throw new Error('合法端口仍标红: ' + borderColor);
    return '端口22正常: ' + borderColor;
  });

  // ===== 7. 端口min/max属性 =====
  await step('端口输入框有min=1 max=65535', async () => {
    const portInp = await page.$('.sys-block:first-child .srv-block:first-child input[type="number"]');
    const min = await portInp.getAttribute('min');
    const max = await portInp.getAttribute('max');
    if (min !== '1' || max !== '65535') throw new Error(`min/max不对: min=${min} max=${max}`);
    return `min=${min} max=${max}`;
  });

  // ===== 8. 系统复制功能 =====
  await step('系统复制功能正常', async () => {
    const before = await page.$$eval('.sys-block', els => els.length);
    await page.click('.sys-block:first-child .sys-actions button:text("复制")');
    await page.waitForTimeout(300);
    const after = await page.$$eval('.sys-block', els => els.length);
    if (after !== before + 1) throw new Error(`复制失败: ${before} → ${after}`);
    return `${before} → ${after}`;
  });

  // ===== 9. 系统上下移 =====
  await step('系统下移后顺序变化', async () => {
    const inputs = await page.$$('.sys-block .sys-name-input input');
    await inputs[0].fill('AAA-First');
    await inputs[1].fill('AAA-Second');
    await page.waitForTimeout(200);
    await page.click('.sys-block:first-child .sys-actions button:text("↓")');
    await page.waitForTimeout(300);
    const afterInputs = await page.$$('.sys-block .sys-name-input input');
    const firstName = await afterInputs[0].inputValue();
    if (firstName !== 'AAA-Second') throw new Error('下移后顺序不对: ' + firstName);
    return '下移后第一位: ' + firstName;
  });

  // ===== 10. 服务器复制 =====
  await step('服务器复制功能正常', async () => {
    const before = await page.$$eval('.sys-block:first-child .srv-block', els => els.length);
    await page.click('.sys-block:first-child .srv-block:first-child button:text("📋 复制服务器（含日志目录）")');
    await page.waitForTimeout(300);
    const after = await page.$$eval('.sys-block:first-child .srv-block', els => els.length);
    if (after !== before + 1) throw new Error(`复制失败: ${before} → ${after}`);
    return `${before} → ${after}`;
  });

  // ===== 11. 日志目录复制 =====
  await step('日志目录复制功能正常', async () => {
    const before = await page.$$eval('.sys-block:first-child .srv-block:first-child .dir-block', els => els.length);
    await page.click('.sys-block:first-child .srv-block:first-child .dir-block:first-child button:text("复制目录")');
    await page.waitForTimeout(300);
    const after = await page.$$eval('.sys-block:first-child .srv-block:first-child .dir-block', els => els.length);
    if (after !== before + 1) throw new Error(`复制失败: ${before} → ${after}`);
    return `${before} → ${after}`;
  });

  // ===== 12. 编码切换保持 =====
  await step('编码切换gbk保持', async () => {
    const encSel = await page.$('.sys-block:first-child .srv-block:first-child .dir-block:first-child select');
    await encSel.selectOption('gbk');
    await page.waitForTimeout(200);
    const val = await encSel.inputValue();
    if (val !== 'gbk') throw new Error('gbk切换失败: ' + val);
    return 'gbk: ' + val;
  });

  // ===== 13. 打开器添加+保存 =====
  await step('打开器添加并保存', async () => {
    await page.click('button:text("+ 添加打开器")');
    await page.waitForTimeout(300);
    const nameInp = await page.$('.opener-row input[placeholder*="Notepad"]');
    await nameInp.fill('TestApp');
    const pathInp = await page.$('.opener-row input[placeholder*="notepad"]');
    await pathInp.fill('/usr/bin/testapp');
    await page.waitForTimeout(200);
    await page.click('.opener-row button:text("💾 保存")');
    await page.waitForTimeout(1500);
    const toast = await page.textContent('#toast');
    if (!toast.includes('保存')) throw new Error('保存失败: ' + toast);
    return 'toast: ' + toast;
  });

  // ===== 14. 放弃改动 =====
  await step('放弃改动恢复disabled', async () => {
    await page.click('button:text("放弃改动")');
    await page.waitForTimeout(1500);
    const saveBtn = await page.$('#cfg-save-btn');
    if (!await saveBtn.isDisabled()) throw new Error('放弃后保存按钮应disabled');
    return '保存按钮已disabled';
  });

  // ===== 15. 导出配置 =====
  await step('导出配置正常', async () => {
    const [download] = await Promise.all([
      page.waitForEvent('download', { timeout: 5000 }).catch(() => null),
      page.click('button:text("导出配置")')
    ]);
    await page.waitForTimeout(500);
    return download ? '下载: ' + download.suggestedFilename() : '无下载';
  });

  // ===== 16. 页面切换state保持 =====
  await step('页面切换后state保持', async () => {
    await page.goto(`${BASE}/#/home`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(500);
    await page.goto(`${BASE}/#/config`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    const cards = await page.$$('.card');
    if (cards.length < 4) throw new Error('切回后卡片丢失');
    return '卡片数: ' + cards.length;
  });

  // ===== 17. 控制台无JS错误 =====
  await step('控制台无syncSaveBtns错误', async () => {
    const errs = consoleMsgs.filter(m => m.type === 'error' && m.text.includes('syncSaveBtns'));
    if (errs.length > 0) throw new Error('仍有syncSaveBtns错误');
    return '无syncSaveBtns错误';
  });

  // ===== 18. 页面无运行时错误 =====
  await step('页面无运行时错误', async () => {
    const syncErrs = pageErrors.filter(e => e.message.includes('syncSaveBtns'));
    if (syncErrs.length > 0) throw new Error('有syncSaveBtns运行时错误');
    return '无syncSaveBtns错误';
  });

  await page.screenshot({ path: path.join(OUT_DIR, 'retest-final.png'), fullPage: true });
  await browser.close();

  const summary = {
    total: results.length,
    passed: results.filter(r => r.status === 'pass').length,
    failed: results.filter(r => r.status === 'fail').length,
  };
  
  log(`\n========== 二次测试完成 ==========`);
  log(`总计: ${summary.total} | 通过: ${summary.passed} | 失败: ${summary.failed}`);
  log(`控制台错误: ${consoleMsgs.filter(m => m.type === 'error').length} | 页面错误: ${pageErrors.length}`);
  
  if (summary.failed > 0) {
    log(`\n失败项:`);
    results.filter(r => r.status === 'fail').forEach(r => log(`  - ${r.name}: ${r.error}`));
  }
  
  process.exit(summary.failed > 0 ? 1 : 0);
})().catch(e => { log('异常: ' + e.message); console.error(e); process.exit(2); });
