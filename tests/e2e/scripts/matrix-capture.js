'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUTPUT_DIR = path.resolve(__dirname, '../../../test-results/v014-v017-audit');
const SCREENSHOTS_DIR = path.join(OUTPUT_DIR, 'screenshots');

const PAGES = [
  { id: 'home', name: '首页', hash: '#/home', selector: '#view > *' },
  { id: 'websphere', name: '日志助手', hash: '#/websphere', selector: '#ws-tabs, .ws-tab-bar, .card' },
  { id: 'files', name: '文件下载', hash: '#/files', selector: '#files-toolbar, .card' },
  { id: 'waspack', name: '投产打包', hash: '#/waspack', selector: '#waspack-project, .card' },
  { id: 'ssh', name: 'SSH终端', hash: '#/ssh', selector: '#ssh-host, .card' },
  { id: 'database', name: '数据库工作台', hash: '#/database', selector: '#db-sql, #db-source, .card' },
  { id: 'http', name: 'HTTP测试', hash: '#/http', selector: '#http-url, .card' },
  { id: 'webservice', name: 'WebService', hash: '#/webservice', selector: '#ws-url, #ws-method, .card' },
  { id: 'wscodegen', name: 'WebService代码生成', hash: '#/wscodegen', selector: '#wsc-url, .card' },
  { id: 'diagnostics', name: '环境自检', hash: '#/diagnostics', selector: '.card' },
  { id: 'config', name: '系统配置', hash: '#/config', selector: '.card' },
  { id: 'downloads', name: '下载历史', hash: '#/downloads', selector: '.card, .table-wrap' },
  { id: 'formatter', name: '报文格式化', hash: '#/formatter', selector: '#fmt-in, .card' },
  { id: 'timestamp', name: '时间戳', hash: '#/timestamp', selector: '#ts-in, .card' },
  { id: 'cron', name: 'Cron解析', hash: '#/cron', selector: '#cron-in, .card' },
  { id: 'jsonpath', name: 'JSONPath', hash: '#/jsonpath', selector: '#jp-in, .card' },
  { id: 'compare', name: '代码比对', hash: '#/compare', selector: '#cmp-left, .card' },
  { id: 'commands', name: '常用命令', hash: '#/commands', selector: '.card, #cmd-filter' },
  { id: 'notes', name: '便笺', hash: '#/notes/list', selector: '.card, #notes-list' },
  { id: 'reminders', name: '便笺提醒', hash: '#/notes/reminders', selector: '.card, #reminder-form' },
  { id: 'tasks', name: '定时任务', hash: '#/tasks', selector: '.card, #task-form' },
  { id: 'sponsor', name: '投喂作者', hash: '#/sponsor', selector: '.card, .sponsor-card' },
  { id: 'about', name: '关于', hash: '#/about', selector: '.card, .about-card' }
];

const RESOLUTIONS = [
  { id: '1920x1080', name: '桌面大屏 FHD', width: 1920, height: 1080 },
  { id: '1366x768', name: '标准笔记本', width: 1366, height: 768 },
  { id: '1024x768', name: '平板横屏', width: 1024, height: 768 },
  { id: '768x1024', name: '平板竖屏', width: 768, height: 1024 },
  { id: '375x812', name: '移动端窄屏', width: 375, height: 812 }
];

const THEMES = [
  { id: 'dark', name: '深色' },
  { id: 'light', name: '浅色' },
  { id: 'green', name: '护眼绿' },
  { id: 'hc', name: '高对比' },
  { id: 'xianxia', name: '仙侠' }
];

function ensureDir(dir) {
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function run() {
  ensureDir(OUTPUT_DIR);
  ensureDir(SCREENSHOTS_DIR);

  console.log('='.repeat(70));
  console.log('Kairo 全矩阵视觉截图与多端适配审核启动');
  console.log('目标服务:', BASE_URL);
  console.log('页面总数:', PAGES.length);
  console.log('分辨率数:', RESOLUTIONS.length);
  console.log('主题套数:', THEMES.length);
  console.log('预计采集截图数:', PAGES.length * RESOLUTIONS.length * THEMES.length);
  console.log('='.repeat(70));

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const page = await context.newPage();

  const consoleErrors = [];
  page.on('console', msg => {
    if (msg.type() === 'error') {
      consoleErrors.push({ url: page.url(), text: msg.text() });
    }
  });

  // 先进入首页
  await page.goto(BASE_URL + '/#/home', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);

  const auditFindings = [];
  let shotCount = 0;
  const startTime = Date.now();

  for (const p of PAGES) {
    const pageDir = path.join(SCREENSHOTS_DIR, p.id);
    ensureDir(pageDir);
    console.log(`\n▶ 正在处理页面: [${p.id}] ${p.name}`);

    // 路由切换
    await page.evaluate(hash => { location.hash = hash; }, p.hash);
    await page.waitForTimeout(600);

    for (const res of RESOLUTIONS) {
      await page.setViewportSize({ width: res.width, height: res.height });
      await page.waitForTimeout(150);

      for (const th of THEMES) {
        // 切换主题
        await page.evaluate(t => {
          if (window.Kairo && window.Kairo.theme && window.Kairo.theme.set) {
            window.Kairo.theme.set(t);
          } else {
            document.documentElement.setAttribute('data-theme', t);
            localStorage.setItem('kairo_theme', t);
          }
        }, th.id);
        await page.waitForTimeout(150);

        // 截图命名: <page>_<theme>_<resolution>.png
        const fileName = `${p.id}_${th.id}_${res.id}.png`;
        const filePath = path.join(pageDir, fileName);

        await page.screenshot({ path: filePath, fullPage: false });
        shotCount++;

        // 自动化布局与视觉指标审查
        const layoutAudit = await page.evaluate((contextData) => {
          const docEl = document.documentElement;
          const body = document.body;
          const issues = [];

          // 1. 横向滚动溢出检测 (主视图是否有意外横向滚动条)
          const hasHorizontalOverflow = docEl.scrollWidth > docEl.clientWidth + 5;
          if (hasHorizontalOverflow) {
            // 找出导致溢出的最大宽元素
            let maxRight = docEl.clientWidth;
            let offender = '';
            const allElements = document.querySelectorAll('#view *');
            allElements.forEach(el => {
              const r = el.getBoundingClientRect();
              if (r.right > maxRight + 10) {
                maxRight = r.right;
                offender = el.className || el.tagName;
              }
            });
            issues.push({
              type: 'HORIZONTAL_OVERFLOW',
              detail: `页面宽度溢出：可视区 ${docEl.clientWidth}px，实际滚动宽 ${docEl.scrollWidth}px (超出的代表元素: ${offender})`
            });
          }

          // 2. 检查 #view 渲染有效性
          const view = document.getElementById('view');
          if (!view || view.children.length === 0 || view.innerText.trim().length === 0) {
            issues.push({
              type: 'EMPTY_VIEW',
              detail: '页面主体 #view 为空或未渲染出内容'
            });
          }

          // 3. 检查是否有文字与背景相同色（导致文字隐形）
          const btns = Array.from(document.querySelectorAll('#view .btn, #view button'));
          btns.slice(0, 10).forEach(b => {
            const style = window.getComputedStyle(b);
            if (style.color && style.backgroundColor && style.color === style.backgroundColor && style.opacity !== '0') {
              issues.push({
                type: 'INVISIBLE_TEXT',
                detail: `按钮文字与背景色完全相同: ${b.innerText.trim().slice(0, 20)}`
              });
            }
          });

          return {
            clientWidth: docEl.clientWidth,
            scrollWidth: docEl.scrollWidth,
            issues
          };
        }, { page: p.id, theme: th.id, res: res.id });

        if (layoutAudit.issues && layoutAudit.issues.length > 0) {
          layoutAudit.issues.forEach(iss => {
            auditFindings.push({
              page: p.id,
              pageName: p.name,
              theme: th.id,
              themeName: th.name,
              resolution: res.id,
              resolutionName: res.name,
              type: iss.type,
              detail: iss.detail,
              screenshot: path.relative(OUTPUT_DIR, filePath)
            });
          });
        }
      }
    }
    process.stdout.write(`  ✓ 完成 ${p.name} 的 25 组组合截图与检查\n`);
  }

  const durationSec = ((Date.now() - startTime) / 1000).toFixed(1);
  console.log('\n' + '='.repeat(70));
  console.log(`截图与多端审查完成！耗时: ${durationSec}s`);
  console.log(`总截图数量: ${shotCount}`);
  console.log(`控制台错误数: ${consoleErrors.length}`);
  console.log(`发现的排版与视觉告警数: ${auditFindings.length}`);
  console.log('='.repeat(70));

  const resultData = {
    totalScreenshots: shotCount,
    durationSec: parseFloat(durationSec),
    consoleErrors,
    auditFindings,
    timestamp: new Date().toISOString()
  };

  fs.writeFileSync(
    path.join(OUTPUT_DIR, 'matrix-audit-results.json'),
    JSON.stringify(resultData, null, 2),
    'utf-8'
  );
  console.log(`结果已保存至: ${path.join(OUTPUT_DIR, 'matrix-audit-results.json')}`);

  await browser.close();
}

run().catch(err => {
  console.error('执行失败:', err);
  process.exit(1);
});
