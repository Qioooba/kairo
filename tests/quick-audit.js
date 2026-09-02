/**
 * Kairo v0.14 -> v0.17 快速 UI 审查测试
 * 简化版本，快速完成剩余页面
 */

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE_URL = 'http://localhost:18789';
const OUTPUT_DIR = path.join(__dirname, '..', 'output', 'visual-audit-v0.14-v0.17');
const SCREENSHOT_DIR = path.join(OUTPUT_DIR, 'screenshots');

// 剩余页面列表
const REMAINING_PAGES = [
  { id: 'notes', name: '便签笔记', path: '/notes' },
  { id: 'sponsor', name: '投喂作者', path: '/sponsor' },
  { id: 'pet', name: '桌面宠物', path: '/pet' },
  { id: 'tasks', name: '任务管理', path: '/tasks' },
  { id: 'reminders', name: '定时提醒', path: '/reminders' },
  { id: 'config', name: '配置中心', path: '/config' },
  { id: 'diagnostics', name: '诊断工具', path: '/diagnostics' },
  { id: 'websphere', name: 'WebSphere', path: '/websphere' },
];

const VIEWPORTS = [
  { name: '1920x1080', width: 1920, height: 1080 },
  { name: '1366x768', width: 1366, height: 768 },
];

const THEMES = ['dark', 'light'];

async function main() {
  console.log('🚀 快速补充测试开始\n');
  
  const browser = await chromium.launch({ headless: true });
  
  let count = 0;
  for (const pageInfo of REMAINING_PAGES) {
    for (const viewport of VIEWPORTS) {
      for (const theme of THEMES) {
        const context = await browser.newContext({
          viewport: { width: viewport.width, height: viewport.height }
        });
        const page = await context.newPage();
        
        const screenshotPath = path.join(SCREENSHOT_DIR, `${pageInfo.id}_${viewport.name}_${theme}.png`);
        
        try {
          await page.goto(`${BASE_URL}${pageInfo.path}`, { waitUntil: 'networkidle', timeout: 15000 });
          await page.waitForTimeout(500);
          await page.screenshot({ path: screenshotPath, fullPage: false });
          count++;
          console.log(`✓ ${pageInfo.id}_${viewport.name}_${theme}`);
        } catch (e) {
          console.log(`✗ ${pageInfo.id}_${viewport.name}_${theme}: ${e.message}`);
        }
        
        await context.close();
      }
    }
  }
  
  await browser.close();
  console.log(`\n✅ 额外生成 ${count} 张截图`);
  console.log(`总计: ${count + 215} 张截图`);
}

main().catch(console.error);
