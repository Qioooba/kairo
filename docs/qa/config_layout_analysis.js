/*
 * 系统配置页面布局分析 - 截图 + DOM结构 + 样式检查
 */
'use strict';
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://127.0.0.1:18092';
const OUT_DIR = path.join(__dirname, 'config_test_shots');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  
  await page.goto(`${BASE}/#/config`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  
  // 1. 首屏截图
  await page.screenshot({ path: path.join(OUT_DIR, 'analysis-01-fullpage.png'), fullPage: true });
  
  // 2. 收集布局信息
  const layoutInfo = await page.evaluate(() => {
    const info = { cards: [], btns: [], issues: [] };
    
    // 检查所有卡片
    document.querySelectorAll('.card').forEach((card, i) => {
      const rect = card.getBoundingClientRect();
      const h3 = card.querySelector('h3');
      const desc = card.querySelector('.card-desc');
      info.cards.push({
        idx: i,
        title: h3 ? h3.textContent : '(无)',
        top: Math.round(rect.top),
        height: Math.round(rect.height),
        width: Math.round(rect.width),
        hasDesc: !!desc,
        descText: desc ? desc.textContent.substring(0, 80) : null
      });
    });
    
    // 检查按钮间距和对齐
    document.querySelectorAll('.btn-row').forEach((row, i) => {
      const rect = row.getBoundingClientRect();
      const btns = row.querySelectorAll('.btn');
      const btnRects = Array.from(btns).map(b => {
        const r = b.getBoundingClientRect();
        return { text: b.textContent, w: Math.round(r.width), h: Math.round(r.height) };
      });
      info.btns.push({ rowIdx: i, top: Math.round(rect.top), btnCount: btns.length, btns: btnRects });
    });
    
    // 检查 sys-block 布局
    const sysBlocks = document.querySelectorAll('.sys-block');
    info.sysBlockCount = sysBlocks.length;
    if (sysBlocks.length > 0) {
      const first = sysBlocks[0].getBoundingClientRect();
      info.firstSysBlock = { top: Math.round(first.top), height: Math.round(first.height) };
    }
    
    // 检查是否有溢出
    const view = document.getElementById('view');
    if (view) {
      info.viewScrollHeight = view.scrollHeight;
      info.viewClientHeight = view.clientHeight;
      info.hasScroll = view.scrollHeight > view.clientHeight;
    }
    
    // 检查 warn-banner
    const banner = document.querySelector('.warn-banner');
    if (banner) {
      const style = getComputedStyle(banner);
      info.banner = { display: style.display, visible: style.display !== 'none' };
    }
    
    // 检查所有input是否有placeholder
    info.inputs = [];
    document.querySelectorAll('#view input').forEach(inp => {
      info.inputs.push({
        type: inp.type,
        placeholder: inp.placeholder,
        value: inp.value ? inp.value.substring(0, 20) : ''
      });
    });
    
    return info;
  });
  
  console.log('=== 布局信息 ===');
  console.log(JSON.stringify(layoutInfo, null, 2));
  
  // 3. 检查窄屏布局
  await page.setViewportSize({ width: 800, height: 900 });
  await page.waitForTimeout(500);
  await page.screenshot({ path: path.join(OUT_DIR, 'analysis-02-narrow.png'), fullPage: true });
  
  const narrowInfo = await page.evaluate(() => {
    const info = { issues: [] };
    document.querySelectorAll('.sys-head').forEach((head, i) => {
      const rect = head.getBoundingClientRect();
      const gridCols = getComputedStyle(head).gridTemplateColumns;
      info.issues.push({ sysHead: i, cols: gridCols, width: Math.round(rect.width) });
    });
    document.querySelectorAll('.srv-fields').forEach((f, i) => {
      const gridCols = getComputedStyle(f).gridTemplateColumns;
      info.issues.push({ srvFields: i, cols: gridCols });
    });
    document.querySelectorAll('.dir-fields').forEach((f, i) => {
      const gridCols = getComputedStyle(f).gridTemplateColumns;
      info.issues.push({ dirFields: i, cols: gridCols });
    });
    return info;
  });
  console.log('=== 窄屏布局 ===');
  console.log(JSON.stringify(narrowInfo, null, 2));
  
  // 4. 恢复宽屏，展开所有块截图
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.waitForTimeout(300);
  
  // 新增多个系统+服务器+目录，制造长表单
  await page.click('button:text("+ 新增业务系统")');
  await page.waitForTimeout(200);
  await page.click('.sys-block:first-child button:text("+ 新增服务器")');
  await page.waitForTimeout(200);
  await page.click('.sys-block:first-child .srv-block:first-child button:text("+ 新增日志目录")');
  await page.waitForTimeout(200);
  
  await page.screenshot({ path: path.join(OUT_DIR, 'analysis-03-expanded.png'), fullPage: true });
  
  // 5. 检查底部保存按钮在长表单中的位置
  const footerInfo = await page.evaluate(() => {
    const footer = document.querySelector('.cfg-save-footer-bar');
    if (!footer) return null;
    const rect = footer.getBoundingClientRect();
    const docHeight = document.documentElement.scrollHeight;
    return {
      top: Math.round(rect.top),
      bottom: Math.round(rect.bottom),
      docHeight: Math.round(docHeight),
      distanceFromBottom: Math.round(docHeight - rect.bottom)
    };
  });
  console.log('=== 底部按钮位置 ===');
  console.log(JSON.stringify(footerInfo, null, 2));
  
  await browser.close();
  console.log('\n截图已保存到: ' + OUT_DIR);
})();
