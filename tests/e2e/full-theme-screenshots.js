'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUTPUT_DIR = path.join(__dirname, '..', 'test-results', 'full-theme-screenshots');

const THEMES = [
  { id: 'light', name: '浅色' },
  { id: 'green', name: '护眼绿' },
  { id: 'hc', name: '高对比' },
  { id: 'xianxia', name: '武侠' },
  { id: 'dark', name: '深色' },
];

const TEST_PAGES = [
  { route: '', name: '首页' },
  { route: 'logs', name: '日志助手' },
  { route: 'files', name: '文件下载' },
  { route: 'ssh', name: 'SSH终端' },
  { route: 'formatter', name: '报文格式化' },
  { route: 'http', name: 'HTTP测试' },
  { route: 'websphere', name: 'WebService' },
  { route: 'cmds', name: '常用命令' },
  { route: 'envcheck', name: '环境自检' },
  { route: 'config', name: '系统配置' },
  { route: 'downloads', name: '下载历史' },
  { route: 'timestamp', name: '时间戳' },
  { route: 'cron', name: 'Cron解析' },
  { route: 'jsonpath', name: 'JSONPath' },
  { route: 'compare', name: '代码比对' },
  { route: 'reminders', name: '便笺提醒' },
  { route: 'about', name: '关于' },
];

function ensureDir(dir) {
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function main() {
  ensureDir(OUTPUT_DIR);

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
  });
  const page = await context.newPage();

  console.log('导航到首页...');
  await page.goto(BASE_URL + '/');
  await page.waitForTimeout(1500);

  const errors = [];
  let total = 0;
  let passed = 0;

  for (const theme of THEMES) {
    console.log('\n' + '='.repeat(50));
    console.log('主题: ' + theme.name + ' (' + theme.id + ')');
    console.log('='.repeat(50));

    const themeDir = path.join(OUTPUT_DIR, theme.id);
    ensureDir(themeDir);

    try {
      await page.evaluate(function (t) {
        if (window.Kairo && window.Kairo.theme && window.Kairo.theme.set) {
          window.Kairo.theme.set(t);
        } else {
          document.documentElement.setAttribute('data-theme', t);
        }
      }, theme.id);
      await page.waitForTimeout(300);
    } catch (e) {
      console.log('  ⚠️  设置主题失败: ' + e.message);
    }

    for (const pg of TEST_PAGES) {
      total++;
      try {
        process.stdout.write('  ' + pg.name + '...');

        await page.evaluate(function (r) {
          location.hash = r ? '#/' + r : '';
        }, pg.route);

        await page.waitForTimeout(600);

        const shotFile = path.join(themeDir, (pg.route || 'home') + '.png');
        await page.screenshot({ path: shotFile, fullPage: true });

        const contentOk = await page.evaluate(function () {
          const view = document.getElementById('view');
          return view && view.innerText.trim().length > 20;
        });

        if (!contentOk) {
          console.log(' ⚠️  内容少');
          errors.push({ theme: theme.id, page: pg.name, error: '内容过少' });
        } else {
          console.log(' ✅');
          passed++;
        }
      } catch (e) {
        console.log(' ❌ ' + e.message.substring(0, 60));
        errors.push({ theme: theme.id, page: pg.name, error: e.message });
      }
    }
  }

  console.log('\n' + '='.repeat(50));
  console.log('系统配置页面 - 外部打开器区域（各主题）');
  console.log('='.repeat(50));

  try {
    await page.evaluate(function () { location.hash = '#/config'; });
    await page.waitForTimeout(800);

    await page.evaluate(function () {
      const headings = document.querySelectorAll('h3');
      for (const h of headings) {
        if (h.innerText && h.innerText.includes('外部打开器')) {
          h.scrollIntoView({ behavior: 'auto', block: 'center' });
          break;
        }
      }
    });
    await page.waitForTimeout(300);

    for (const theme of THEMES) {
      total++;
      try {
        process.stdout.write('  ' + theme.name + '...');

        await page.evaluate(function (t) {
          if (window.Kairo && window.Kairo.theme && window.Kairo.theme.set) {
            window.Kairo.theme.set(t);
          } else {
            document.documentElement.setAttribute('data-theme', t);
          }
        }, theme.id);
        await page.waitForTimeout(300);

        const shotFile = path.join(OUTPUT_DIR, theme.id, 'config-openers-section.png');
        await page.screenshot({ path: shotFile, fullPage: false });
        console.log(' ✅');
        passed++;
      } catch (e) {
        console.log(' ❌ ' + e.message.substring(0, 60));
        errors.push({ theme: theme.id, page: 'config-openers', error: e.message });
      }
    }
  } catch (e) {
    console.log('  跳过: ' + e.message);
  }

  console.log('\n关闭浏览器...');
  await browser.close();

  console.log('\n' + '='.repeat(50));
  console.log('测试完成');
  console.log('='.repeat(50));
  console.log('总页面: ' + total);
  console.log('通过: ' + passed);
  console.log('失败/警告: ' + errors.length);
  console.log('输出目录: ' + OUTPUT_DIR);

  if (errors.length > 0) {
    console.log('\n问题详情:');
    for (const e of errors) {
      console.log('  - [' + e.theme + '] ' + e.page + ': ' + e.error.substring(0, 100));
    }
  }
}

main().catch(console.error);
