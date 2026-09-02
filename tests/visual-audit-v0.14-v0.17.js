/**
 * Kairo v0.14 -> v0.17 全页面 UI 审查测试
 * 测试所有页面在不同分辨率和主题下的表现
 * 
 * 分辨率: 1920x1080, 1366x768, 1280x720, 800x600
 * 主题: dark, light, green, hc (High Contrast)
 */

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

// 测试配置
const BASE_URL = 'http://localhost:18789';
const OUTPUT_DIR = path.join(__dirname, '..', 'output', 'visual-audit-v0.14-v0.17');
const SCREENSHOT_DIR = path.join(OUTPUT_DIR, 'screenshots');
const REPORT_FILE = path.join(OUTPUT_DIR, 'test-report.md');

// 页面列表 (从 web/pages/ 目录)
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
  { id: 'diagnostics', name: '诊断工具', path: '/diagnostics' },
  { id: 'websphere', name: 'WebSphere', path: '/websphere' },
];

// 分辨率配置
const VIEWPORTS = [
  { name: '1920x1080', width: 1920, height: 1080 },
  { name: '1366x768', width: 1366, height: 768 },
  { name: '1280x720', width: 1280, height: 720 },
  { name: '1024x768', width: 1024, height: 768 },
  { name: '800x600', width: 800, height: 600 },
];

// 主题配置
const THEMES = ['dark', 'light', 'green', 'hc'];

// 全局浏览器实例
let browser = null;
let browserContext = null;

// 测试结果收集
const testResults = {
  summary: {
    total: 0,
    passed: 0,
    failed: 0,
    warnings: 0,
    issues: []
  },
  pages: {}
};

// 确保输出目录存在
function ensureOutputDir() {
  if (!fs.existsSync(OUTPUT_DIR)) {
    fs.mkdirSync(OUTPUT_DIR, { recursive: true });
  }
  if (!fs.existsSync(SCREENSHOT_DIR)) {
    fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
  }
}

// 页面截图
async function capturePage(context, page, pageInfo, viewport, theme) {
  const screenshotPath = path.join(
    SCREENSHOT_DIR,
    `${pageInfo.id}_${viewport.name}_${theme}.png`
  );
  
  const issues = [];
  
  try {
    // 导航到页面
    const url = `${BASE_URL}${pageInfo.path}`;
    await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 });
    
    // 等待页面加载完成后再设置主题
    await page.waitForTimeout(500);
    
    // 设置主题 (在页面加载后执行)
    try {
      await page.evaluate((t) => {
        if (localStorage) {
          localStorage.setItem('kairo-theme', t);
        }
      }, theme);
    } catch (e) {
      // localStorage 可能不可用，忽略
    }
    
    // 等待页面加载
    await page.waitForTimeout(1000);
    
    // 检查页面基本元素
    const body = await page.$('body');
    if (!body) {
      issues.push('页面 body 元素不存在');
    }
    
    // 检查导航栏
    const nav = await page.$('nav, .nav, header');
    if (!nav) {
      issues.push('未找到导航栏');
    }
    
    // 检查主要容器
    const main = await page.$('main, .main, #content, .content');
    if (!main) {
      issues.push('未找到主要内容容器');
    }
    
    // 检查按钮
    const buttons = await page.$$('button');
    if (buttons.length === 0) {
      issues.push('页面无按钮元素');
    }
    
    // 检查文本框
    const inputs = await page.$$('input, textarea');
    if (inputs.length === 0 && !['about', 'sponsor'].includes(pageInfo.id)) {
      issues.push('页面无输入框元素（除纯展示页面外）');
    }
    
    // 检查可见性问题
    const visibleElements = await page.evaluate(() => {
      const elements = document.querySelectorAll('*');
      let invisibleCount = 0;
      let colorIssues = [];
      
      elements.forEach(el => {
        const style = window.getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        
        // 检查透明度
        if (parseFloat(style.opacity) < 0.1) {
          invisibleCount++;
        }
        
        // 检查颜色对比度问题（简单检查）
        if (style.color && style.backgroundColor) {
          const textColor = style.color;
          const bgColor = style.backgroundColor;
          if (textColor === bgColor || (textColor.includes('rgba') && bgColor.includes('rgba') && textColor === bgColor.replace(/[\d.]+\)$/, '1)'))) {
            colorIssues.push(el.tagName + (el.className ? '.' + el.className.split(' ')[0] : ''));
          }
        }
      });
      
      return { invisibleCount, colorIssues: colorIssues.slice(0, 5) };
    });
    
    if (visibleElements.invisibleCount > 50) {
      issues.push(`警告: ${visibleElements.invisibleCount} 个隐藏元素`);
    }
    
    if (visibleElements.colorIssues.length > 0) {
      issues.push(`颜色问题: ${visibleElements.colorIssues.join(', ')}`);
    }
    
    // 截图
    await page.screenshot({
      path: screenshotPath,
      fullPage: false
    });
    
    return {
      success: true,
      screenshotPath,
      issues
    };
    
  } catch (error) {
    return {
      success: false,
      screenshotPath,
      issues: [`错误: ${error.message}`]
    };
  }
}

// 测试单个页面的所有分辨率和主题
async function testPage(pageInfo) {
  const pageResults = {
    name: pageInfo.name,
    path: pageInfo.path,
    tests: []
  };
  
  for (const viewport of VIEWPORTS) {
    for (const theme of THEMES) {
      testResults.summary.total++;
      
      // 每个测试用例使用新的 context 和 page
      const context = await browser.newContext({
        viewport: { width: viewport.width, height: viewport.height },
        locale: 'zh-CN'
      });
      const page = await context.newPage();
      
      const result = await capturePage(context, page, pageInfo, viewport, theme);
      
      pageResults.tests.push({
        viewport: viewport.name,
        theme,
        ...result
      });
      
      if (result.success) {
        if (result.issues.length > 0) {
          testResults.summary.warnings++;
          testResults.summary.issues.push({
            page: pageInfo.id,
            viewport: viewport.name,
            theme,
            issues: result.issues
          });
        } else {
          testResults.summary.passed++;
        }
      } else {
        testResults.summary.failed++;
        testResults.summary.issues.push({
          page: pageInfo.id,
          viewport: viewport.name,
          theme,
          issues: result.issues,
          critical: true
        });
      }
      
      await context.close();
    }
  }
  
  testResults.pages[pageInfo.id] = pageResults;
}

// 生成报告
function generateReport() {
  let report = `# Kairo v0.14 → v0.17 UI 审查测试报告

> 测试时间: ${new Date().toISOString()}
> 测试范围: ${PAGES.length} 个页面 × ${VIEWPORTS.length} 种分辨率 × ${THEMES.length} 种主题
> 总测试用例: ${testResults.summary.total}

## 测试摘要

| 指标 | 数值 |
|------|------|
| 总测试数 | ${testResults.summary.total} |
| 通过 | ${testResults.summary.passed} |
| 警告 | ${testResults.summary.warnings} |
| 失败 | ${testResults.summary.failed} |
| 通过率 | ${((testResults.summary.passed / testResults.summary.total) * 100).toFixed(1)}% |

## 问题详情

`;

  // 按严重程度分组
  const criticalIssues = testResults.summary.issues.filter(i => i.critical);
  const warnings = testResults.summary.issues.filter(i => !i.critical);

  if (criticalIssues.length > 0) {
    report += `### 严重问题 (${criticalIssues.length} 项)

| 页面 | 分辨率 | 主题 | 问题 |
|------|--------|------|------|
`;
    criticalIssues.forEach(issue => {
      report += `| ${issue.page} | ${issue.viewport} | ${issue.theme} | ${issue.issues.join('; ')} |\n`;
    });
    report += '\n';
  }

  if (warnings.length > 0) {
    report += `### 警告信息 (${warnings.length} 项)

| 页面 | 分辨率 | 主题 | 问题 |
|------|--------|------|------|
`;
    warnings.forEach(issue => {
      report += `| ${issue.page} | ${issue.viewport} | ${issue.theme} | ${issue.issues.join('; ')} |\n`;
    });
    report += '\n';
  }

  // 页面详细结果
  report += `## 页面详细结果

### 页面覆盖情况

| 页面ID | 页面名称 | 测试数 | 通过 | 警告 | 失败 |
|--------|----------|--------|------|------|------|
`;

  for (const [pageId, pageResult] of Object.entries(testResults.pages)) {
    const passed = pageResult.tests.filter(t => t.success && t.issues.length === 0).length;
    const warnings_count = pageResult.tests.filter(t => t.success && t.issues.length > 0).length;
    const failed = pageResult.tests.filter(t => !t.success).length;
    report += `| ${pageId} | ${pageResult.name} | ${pageResult.tests.length} | ${passed} | ${warnings_count} | ${failed} |\n`;
  }

  report += `
## 截图存档

截图保存在: \`${SCREENSHOT_DIR}\`

文件命名格式: \`{页面ID}_{分辨率}_{主题}.png\`

## 测试配置

### 分辨率
${VIEWPORTS.map(v => `- ${v.name} (${v.width}x${v.height})`).join('\n')}

### 主题
${THEMES.map(t => `- ${t}`).join('\n')}

## 建议

`;

  if (criticalIssues.length > 0) {
    report += `1. **优先修复 ${criticalIssues.length} 个严重问题**
2. 检查所有 ${warnings.length} 个警告信息
`;
  } else if (warnings.length > 0) {
    report += `1. 检查 ${warnings.length} 个警告信息
2. 验证各页面在不同环境下的表现
`;
  } else {
    report += `1. 所有核心页面测试通过
2. 建议进行手动功能测试验证
`;
  }

  // 写入报告
  fs.writeFileSync(REPORT_FILE, report, 'utf8');
  
  return report;
}

// 主函数
async function main() {
  console.log('🚀 Kairo v0.14 → v0.17 UI 审查测试开始\n');
  
  ensureOutputDir();
  
  // 检查服务是否可用
  try {
    const response = await fetch(BASE_URL);
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    console.log('✅ Kairo 服务正常\n');
  } catch (error) {
    console.error(`❌ 无法连接到 Kairo 服务: ${error.message}`);
    console.log('请确保 Kairo 正在运行 (http://localhost:18789)');
    process.exit(1);
  }
  
  // 启动浏览器
  console.log('🔧 启动浏览器...');
  browser = await chromium.launch({ headless: true });
  
  try {
    // 逐个测试页面
    for (let i = 0; i < PAGES.length; i++) {
      const pageInfo = PAGES[i];
      console.log(`📋 [${i + 1}/${PAGES.length}] 测试页面: ${pageInfo.name} (${pageInfo.path})`);
      
      await testPage(pageInfo);
      
      const pageResult = testResults.pages[pageInfo.id];
      const passed = pageResult.tests.filter(t => t.success && t.issues.length === 0).length;
      const failed = pageResult.tests.filter(t => !t.success).length;
      const warnings = pageResult.tests.filter(t => t.success && t.issues.length > 0).length;
      
      console.log(`   结果: ✓ ${passed} | ⚠ ${warnings} | ✗ ${failed}`);
    }
    
    // 生成报告
    console.log('\n📊 生成测试报告...');
    const report = generateReport();
    
    console.log('\n' + '='.repeat(60));
    console.log('测试完成!');
    console.log('='.repeat(60));
    console.log(`总测试: ${testResults.summary.total}`);
    console.log(`通过: ${testResults.summary.passed}`);
    console.log(`警告: ${testResults.summary.warnings}`);
    console.log(`失败: ${testResults.summary.failed}`);
    console.log(`通过率: ${((testResults.summary.passed / testResults.summary.total) * 100).toFixed(1)}%`);
    console.log(`\n报告已保存: ${REPORT_FILE}`);
    console.log(`截图目录: ${SCREENSHOT_DIR}`);
    
  } finally {
    // 关闭浏览器
    if (browser) {
      await browser.close();
    }
  }
  
  // 返回退出码
  process.exit(testResults.summary.failed > 0 ? 1 : 0);
}

main().catch(console.error);
