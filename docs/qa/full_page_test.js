'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://127.0.0.1:18092';
const OUT_DIR = path.join(__dirname, 'full-page-screenshots');
const REPORT = path.join(__dirname, 'full-page-test-report.json');

const VIEWPORT = { width: 1366, height: 900 };

if (!fs.existsSync(OUT_DIR)) fs.mkdirSync(OUT_DIR, { recursive: true });

const PAGES = [
  { route: 'home', name: '首页', expectedCrumb: '首页' },
  { route: 'websphere', name: '日志助手', expectedCrumb: 'WebSphere 日志助手' },
  { route: 'files', name: 'FTP文件下载', expectedCrumb: '文件下载' },
  { route: 'formatter', name: '报文格式化', expectedCrumb: '报文格式化' },
  { route: 'http', name: 'HTTP测试', expectedCrumb: 'HTTP 测试' },
  { route: 'commands', name: '常用命令', expectedCrumb: '常用命令' },
  { route: 'diagnostics', name: '环境自检', expectedCrumb: '环境自检' },
  { route: 'config', name: '系统配置', expectedCrumb: '系统配置' },
  { route: 'downloads', name: '下载历史', expectedCrumb: '下载历史' },
  { route: 'timestamp', name: '时间戳', expectedCrumb: '时间戳转换' },
  { route: 'cron', name: 'Cron解析', expectedCrumb: 'Cron 解析' },
  { route: 'jsonpath', name: 'JSONPath', expectedCrumb: 'JSONPath 查询' },
  { route: 'compare', name: '代码比对', expectedCrumb: '代码比对' },
  { route: 'about', name: '关于', expectedCrumb: '关于' },
  { route: 'history', name: '操作历史(死链检查)', expectedCrumb: '操作历史' },
];

function isElectronError(msg) {
  const lower = msg.toLowerCase();
  return (
    lower.includes('electron') ||
    lower.includes('preload') ||
    lower.includes('require is not defined') && lower.includes('electron') ||
    lower.includes('synchronous') && lower.includes('html') ||
    lower.includes('devtools') ||
    lower.includes('extension') ||
    lower.includes('cross-origin') && lower.includes('null') ||
    lower.includes('favicon')
  );
}

function isProjectError(msg) {
  return !isElectronError(msg) && msg.trim().length > 0;
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: VIEWPORT, locale: 'zh-CN' });
  const page = await context.newPage();

  const results = [];
  const allConsoleErrors = [];
  const allPageErrors = [];
  const issues = [];

  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      const text = msg.text();
      if (isProjectError(text)) {
        allConsoleErrors.push({ url: page.url(), text, location: msg.location() });
      }
    }
  });

  page.on('pageerror', (err) => {
    if (isProjectError(err.message)) {
      allPageErrors.push({ url: page.url(), message: err.message, stack: err.stack });
    }
  });

  page.on('dialog', async (d) => { try { await d.dismiss(); } catch (e) {} });

  console.log('==> 导航到首页');
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 10000 });
  await page.waitForTimeout(1000);

  for (let i = 0; i < PAGES.length; i++) {
    const pg = PAGES[i];
    const route = '#/' + pg.route;
    console.log(`==> [${i + 1}/${PAGES.length}] 测试 ${pg.name} (${route})`);

    const pageResult = {
      route,
      name: pg.name,
      status: 'success',
      issues: [],
      consoleErrors: [],
      pageErrors: [],
      crumbText: null,
      hasRememberPassword: false,
      hasHistoryDeadLink: false,
      hasContent: false,
      screenshot: null,
    };

    const pageErrorsBefore = allPageErrors.length;
    const consoleErrorsBefore = allConsoleErrors.length;

    try {
      await page.evaluate((r) => { location.hash = r; }, route);
      await page.waitForTimeout(800);

      const url = page.url();
      const actualHash = await page.evaluate(() => location.hash);

      if (actualHash !== route) {
        pageResult.issues.push(`路由未正确跳转，期望 ${route}，实际 ${actualHash}`);
        pageResult.status = 'warning';
      }

      await page.waitForTimeout(500);

      const viewContent = await page.$eval('#view', el => {
        return {
          html: el.innerHTML.substring(0, 5000),
          text: el.innerText.substring(0, 2000),
          hasChildren: el.children.length > 0,
          visible: el.offsetParent !== null,
        };
      }).catch(e => ({ error: e.message }));

      if (viewContent.error) {
        pageResult.issues.push(`#view 元素错误: ${viewContent.error}`);
        pageResult.status = 'fail';
      } else {
        pageResult.hasContent = viewContent.hasChildren && viewContent.text.trim().length > 0;
        if (!pageResult.hasContent) {
          pageResult.issues.push('页面内容区域为空或无可见内容');
          pageResult.status = 'fail';
        }
      }

      // v1.x 去顶栏后不再有面包屑 #crumbs，当前页名统一从 Tab 条的活动 Tab 读取
      const crumb = await page.$eval('.app-tab.active .app-tab-label', el => el.textContent.trim()).catch(() => null);
      pageResult.crumbText = crumb;
      if (!crumb) {
        pageResult.issues.push('活动 Tab 文案不存在或为空（原面包屑 #crumbs 已随顶栏移除）');
      } else if (pg.expectedCrumb && !crumb.includes(pg.expectedCrumb) && pg.expectedCrumb !== '操作历史') {
        pageResult.issues.push(`当前页名不匹配，期望包含 "${pg.expectedCrumb}"，实际 "${crumb}"`);
      }

      const rememberCheckbox = await page.$('input[type="checkbox"]#ws-remember, input[type="checkbox"][id*="remember"], label:has-text("记住密码")');
      pageResult.hasRememberPassword = !!rememberCheckbox;
      if (rememberCheckbox && pg.route !== 'websphere') {
        pageResult.issues.push('发现残留的"记住密码"复选框（非websphere页面）');
      }

      if (pg.route === 'history') {
        const bodyText = await page.evaluate(() => document.body.innerText);
        const hasContent = bodyText.includes('操作历史') || bodyText.includes('暂无数据') || bodyText.includes('刷新') || bodyText.includes('自动刷新');
        const navExists = await page.$('a[data-route="history"]');
        if (!hasContent && !viewContent.hasChildren) {
          pageResult.issues.push('操作历史页面可能存在死链：无有效内容渲染');
          pageResult.hasHistoryDeadLink = true;
          pageResult.status = 'fail';
        }
        const deadLinkCheck = await page.$$('a[href="#/history"]').then(els => els.length);
        pageResult.historyLinkCount = deadLinkCheck;
      }

      const brokenElements = await page.$$eval('*', els => {
        const issues = [];
        const emptyHeadings = Array.from(els).filter(el =>
          ['H1', 'H2', 'H3', 'H4'].includes(el.tagName) &&
          el.offsetParent !== null &&
          el.textContent.trim() === ''
        );
        if (emptyHeadings.length > 0) {
          issues.push(`存在 ${emptyHeadings.length} 个空标题元素`);
        }
        return issues;
      });
      pageResult.issues.push(...brokenElements);

      const visibleButtons = await page.$$eval('button', btns =>
        btns.filter(b => b.offsetParent !== null).map(b => b.textContent.trim()).filter(t => t)
      );
      pageResult.visibleButtons = visibleButtons.slice(0, 20);

      const navExists = await page.$(`a[data-route="${pg.route}"]`);
      if (!navExists && pg.route !== 'home') {
        pageResult.issues.push(`导航栏中找不到对应链接 a[data-route="${pg.route}"]`);
      }

      const shotName = `${String(i + 1).padStart(2, '0')}-${pg.route}`;
      const shotPath = path.join(OUT_DIR, shotName + '.png');
      await page.screenshot({ path: shotPath, fullPage: true });
      pageResult.screenshot = shotPath;

      const newPageErrors = allPageErrors.slice(pageErrorsBefore);
      const newConsoleErrors = allConsoleErrors.slice(consoleErrorsBefore);
      pageResult.pageErrors = newPageErrors.map(e => e.message);
      pageResult.consoleErrors = newConsoleErrors.map(e => e.text);

      if (newPageErrors.length > 0) {
        pageResult.issues.push(`发生 ${newPageErrors.length} 个页面JS错误: ${newPageErrors.map(e => e.message).join('; ')}`);
        pageResult.status = 'fail';
      }
      if (newConsoleErrors.length > 0) {
        pageResult.issues.push(`控制台输出 ${newConsoleErrors.length} 个错误: ${newConsoleErrors.map(e => e.text).join('; ')}`);
        if (pageResult.status === 'success') pageResult.status = 'warning';
      }

      if (pageResult.issues.length > 0) {
        issues.push({ page: pg.name, route, issues: [...pageResult.issues] });
      }

    } catch (e) {
      pageResult.status = 'fail';
      pageResult.issues.push(`页面加载异常: ${e.message}`);
      issues.push({ page: pg.name, route, issues: [e.message] });
    }

    results.push(pageResult);
    console.log(`   状态: ${pageResult.status}, 问题: ${pageResult.issues.length}`);
  }

  console.log('\n==> 检查导航栏所有菜单项');
  await page.evaluate(() => { location.hash = '#/home'; });
  await page.waitForTimeout(500);
  const navItems = await page.$$eval('#nav .nav-item', items =>
    items.map(it => ({
      text: it.textContent.trim(),
      route: it.getAttribute('data-route') || it.querySelector('a')?.getAttribute('data-route'),
      href: it.querySelector('a')?.getAttribute('href'),
    }))
  );

  console.log('\n==> 检查全局残留元素');
  await page.evaluate(() => { location.hash = '#/home'; });
  await page.waitForTimeout(500);
  const globalIssues = [];

  const globalRemember = await page.$$('label:has-text("记住密码"), #ws-remember').then(els => els.length);
  if (globalRemember > 0) {
    globalIssues.push(`首页发现 ${globalRemember} 个"记住密码"相关元素`);
  }

  const historyBtnCheck = await page.$$('button:has-text("操作历史"), a:has-text("操作历史")').then(els =>
    els.map(el => ({ tag: el.tagName, text: el.textContent.trim(), visible: el.offsetParent !== null }))
  );

  await context.close();
  await browser.close();

  const summary = {
    total: results.length,
    success: results.filter(r => r.status === 'success').length,
    warning: results.filter(r => r.status === 'warning').length,
    fail: results.filter(r => r.status === 'fail').length,
    totalIssues: issues.length,
    totalProjectConsoleErrors: allConsoleErrors.length,
    totalProjectPageErrors: allPageErrors.length,
  };

  const report = {
    base: BASE,
    run_at: new Date().toISOString(),
    summary,
    pages: results,
    allIssues: issues,
    navItems,
    globalIssues,
    historyLinks: historyBtnCheck,
    consoleErrors: allConsoleErrors,
    pageErrors: allPageErrors,
  };

  fs.writeFileSync(REPORT, JSON.stringify(report, null, 2));

  console.log('\n' + '='.repeat(60));
  console.log('全页面测试完成');
  console.log('='.repeat(60));
  console.log(`总计: ${summary.total} 页面`);
  console.log(`成功: ${summary.success}`);
  console.log(`警告: ${summary.warning}`);
  console.log(`失败: ${summary.fail}`);
  console.log(`发现问题: ${summary.totalIssues} 项`);
  console.log(`项目代码JS错误(控制台): ${summary.totalProjectConsoleErrors}`);
  console.log(`项目代码JS错误(页面异常): ${summary.totalProjectPageErrors}`);
  console.log('\n问题明细:');
  issues.forEach((iss, idx) => {
    console.log(`\n[${idx + 1}] ${iss.page} (${iss.route})`);
    iss.issues.forEach(i => console.log(`    - ${i}`));
  });
  console.log('\n报告已写入:', REPORT);
  console.log('截图目录:', OUT_DIR);

})().catch(e => {
  console.error('测试脚本异常:', e);
  process.exit(2);
});
