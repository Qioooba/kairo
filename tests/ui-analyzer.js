/**
 * Kairo UI 问题分析器
 * 分析截图中的常见 UI 问题
 */

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE_URL = 'http://localhost:18789';
const SCREENSHOT_DIR = path.join(__dirname, '..', 'output', 'visual-audit-v0.14-v0.17', 'screenshots');
const REPORT_FILE = path.join(__dirname, '..', 'output', 'visual-audit-v0.14-v0.17', 'ui-analysis.md');

const PAGES = [
  { id: 'home', name: '首页/仪表盘', path: '/' },
  { id: 'about', name: '关于页面', path: '/about' },
  { id: 'http', name: 'HTTP 测试', path: '/http' },
  { id: 'webservice', name: 'WebService', path: '/webservice' },
  { id: 'wscodegen', name: 'WSDL Codegen', path: '/wscodegen' },
  { id: 'waspack', name: 'WAS Pack', path: '/waspack' },
  { id: 'ssh', name: 'SSH 终端', path: '/ssh' },
  { id: 'compare', name: '代码比对', path: '/compare' },
  { id: 'files', name: '文件浏览', path: '/files' },
  { id: 'database', name: '数据库工作台', path: '/database' },
  { id: 'notes', name: '便签笔记', path: '/notes' },
  { id: 'sponsor', name: '投喂作者', path: '/sponsor' },
  { id: 'pet', name: '桌面宠物', path: '/pet' },
  { id: 'tasks', name: '任务管理', path: '/tasks' },
  { id: 'reminders', name: '定时提醒', path: '/reminders' },
  { id: 'config', name: '配置中心', path: '/config' },
  { id: 'websphere', name: 'WebSphere', path: '/websphere' },
];

const THEMES = ['dark', 'light', 'green', 'hc'];

const issues = [];
const layoutIssues = [];
const colorIssues = [];

async function analyzePage(pageInfo) {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  page.setViewportSize({ width: 1366, height: 768 });
  
  for (const theme of THEMES) {
    try {
      await page.goto(`${BASE_URL}${pageInfo.path}`, { waitUntil: 'networkidle', timeout: 20000 });
      await page.waitForTimeout(1000);
      
      // 检查布局问题
      const layoutCheck = await page.evaluate(() => {
        const issues = [];
        
        // 检查是否有元素溢出
        const body = document.body;
        const html = document.documentElement;
        const scrollWidth = Math.max(body.scrollWidth, body.offsetWidth, html.clientWidth, html.scrollWidth, html.offsetWidth);
        const scrollHeight = Math.max(body.scrollHeight, body.offsetHeight, html.clientHeight, html.scrollHeight, html.offsetHeight);
        
        if (scrollWidth > window.innerWidth) {
          issues.push('水平溢出');
        }
        if (scrollHeight > window.innerHeight) {
          issues.push('垂直溢出');
        }
        
        // 检查按钮样式
        const buttons = document.querySelectorAll('button');
        buttons.forEach(btn => {
          const style = window.getComputedStyle(btn);
          if (style.backgroundColor === 'rgba(0, 0, 0, 0)' || style.backgroundColor === 'transparent') {
            // 检查是否有明显的边框
            if (!style.border || style.border === 'none') {
              issues.push('按钮无背景/边框');
            }
          }
        });
        
        // 检查输入框
        const inputs = document.querySelectorAll('input, textarea, select');
        inputs.forEach(input => {
          const style = window.getComputedStyle(input);
          if (style.width === '0px' || style.height === '0px') {
            issues.push('输入框尺寸为0');
          }
        });
        
        // 检查文字可见性
        const textElements = document.querySelectorAll('p, span, div, label, h1, h2, h3, h4, h5, h6');
        textElements.forEach(el => {
          const style = window.getComputedStyle(el);
          const rect = el.getBoundingClientRect();
          if (rect.width > 0 && rect.height > 0) {
            const color = style.color;
            const bg = style.backgroundColor;
            if (color === bg) {
              issues.push(`文字颜色与背景相同: ${el.className || el.tagName}`);
            }
          }
        });
        
        return {
          scrollWidth,
          scrollHeight,
          windowWidth: window.innerWidth,
          windowHeight: window.innerHeight,
          issues: [...new Set(issues)]
        };
      });
      
      if (layoutCheck.issues.length > 0) {
        layoutIssues.push({
          page: pageInfo.id,
          theme,
          issues: layoutCheck.issues
        });
      }
      
    } catch (e) {
      console.log(`⚠ ${pageInfo.id} (${theme}): ${e.message}`);
    }
  }
  
  await browser.close();
}

async function main() {
  console.log('🔍 开始 UI 分析...\n');
  
  for (let i = 0; i < PAGES.length; i++) {
    const page = PAGES[i];
    console.log(`分析 [${i+1}/${PAGES.length}]: ${page.name}`);
    await analyzePage(page);
  }
  
  // 生成报告
  let report = `# Kairo v0.14 → v0.17 UI 问题分析报告

> 分析时间: ${new Date().toISOString()}
> 分析页面: ${PAGES.length} 个
> 分析主题: ${THEMES.length} 个

## 布局问题汇总

`;
  
  if (layoutIssues.length > 0) {
    report += `发现 **${layoutIssues.length}** 个布局问题：

| 页面 | 主题 | 问题 |
|------|------|------|
`;
    layoutIssues.forEach(item => {
      report += `| ${item.page} | ${item.theme} | ${item.issues.join(', ')} |\n`;
    });
  } else {
    report += '✅ 未发现明显布局问题\n';
  }
  
  report += `
## 测试截图统计

`;
  
  const screenshots = fs.readdirSync(SCREENSHOT_DIR).filter(f => f.endsWith('.png'));
  const byPage = {};
  screenshots.forEach(s => {
    const pageId = s.split('_')[0];
    if (!byPage[pageId]) byPage[pageId] = [];
    byPage[pageId].push(s);
  });
  
  report += `| 页面 | 截图数量 |
|------|----------|
`;
  for (const [pageId, files] of Object.entries(byPage)) {
    const pageName = PAGES.find(p => p.id === pageId)?.name || pageId;
    report += `| ${pageName} | ${files.length} |\n`;
  }
  
  report += `
## 建议

1. 检查所有页面的水平/垂直溢出问题
2. 确保按钮在所有主题下都有清晰的视觉反馈
3. 验证输入框在各种分辨率下的可用性
4. 关注移动端视图下的布局适配

---
*本报告由 Mavis 自动生成*
`;
  
  fs.writeFileSync(REPORT_FILE, report, 'utf8');
  console.log(`\n✅ 报告已保存: ${REPORT_FILE}`);
}

main().catch(console.error);
