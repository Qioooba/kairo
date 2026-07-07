'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUTPUT_DIR = path.join(__dirname, '..', 'test-results', 'edit-feature');

function ensureDir(dir) {
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function main() {
  ensureDir(OUTPUT_DIR);

  console.log('启动浏览器...');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
  });
  const page = await context.newPage();

  const errors = [];

  console.log('导航到首页...');
  await page.goto(BASE_URL + '/');
  await page.waitForTimeout(2000);

  // ========== 1. 文件下载页面 - 连接浏览并查看编辑按钮 ==========
  console.log('\n=== 1. 文件下载页面 ===');
  try {
    await page.evaluate(function () { location.hash = '#/files'; });
    await page.waitForTimeout(500);

    await page.selectOption('select', { value: '本地测试' });
    await page.waitForTimeout(300);
    await page.click('button:has-text("连接并浏览")');
    await page.waitForTimeout(3000);

    await page.screenshot({ path: path.join(OUTPUT_DIR, 'files-connected.png'), fullPage: true });
    console.log('  ✅ 已连接，截图保存');

    // 检查页面内容
    const pageContent = await page.evaluate(function () {
      return document.body.innerText.substring(0, 2000);
    });
    console.log('  页面内容预览: ' + pageContent.substring(0, 200));

    // 查找编辑按钮
    const editButtons = await page.evaluate(function () {
      const btns = [];
      document.querySelectorAll('button').forEach(function (b) {
        if (b.innerText && (b.innerText.includes('编辑') || b.innerText.includes('Note') || b.innerText.includes('VS') || b.innerText.includes('IDE'))) {
          btns.push({ text: b.innerText.substring(0, 50), visible: b.offsetParent !== null });
        }
      });
      return btns;
    });
    console.log('  编辑按钮数量: ' + editButtons.length);
    editButtons.forEach(function (b, i) {
      console.log('    [' + i + '] ' + b.text + ' (visible: ' + b.visible + ')');
    });
  } catch (e) {
    console.log('  ❌ ' + e.message);
    errors.push({ step: 'files-page', error: e.message });
  }

  // ========== 2. SSH 终端页面 - SFTP 面板 ==========
  console.log('\n=== 2. SSH 终端 - SFTP 面板 ===');
  try {
    await page.evaluate(function () { location.hash = '#/ssh'; });
    await page.waitForTimeout(1000);

    await page.screenshot({ path: path.join(OUTPUT_DIR, 'ssh-panel.png'), fullPage: true });
    console.log('  ✅ SSH 终端页面截图');

    // 点击 Mock SSH 打开连接
    const mockHost = await page.evaluate(function () {
      const items = document.querySelectorAll('.host-item, .server-item, [class*="host"]');
      for (const item of items) {
        if (item.innerText && item.innerText.includes('Mock SSH')) {
          return item.innerText.substring(0, 100);
        }
      }
      return null;
    });
    console.log('  找到 Mock SSH: ' + mockHost);

    // 尝试双击 Mock SSH
    if (mockHost) {
      const items = await page.$$('text=Mock SSH');
      if (items.length > 0) {
        await items[0].dblclick();
        await page.waitForTimeout(3000);
        await page.screenshot({ path: path.join(OUTPUT_DIR, 'ssh-connected.png'), fullPage: true });
        console.log('  ✅ 已连接 SSH');

        // 点击文件按钮打开 SFTP
        const fileBtn = await page.$('button:has-text("文件")');
        if (fileBtn) {
          await fileBtn.click();
          await page.waitForTimeout(2000);
          await page.screenshot({ path: path.join(OUTPUT_DIR, 'ssh-sftp-panel.png'), fullPage: true });
          console.log('  ✅ SFTP 面板已打开');

          // 检查编辑按钮
          const sftpEditBtns = await page.evaluate(function () {
            const btns = [];
            document.querySelectorAll('button').forEach(function (b) {
              if (b.innerText && (b.innerText.includes('编辑') || b.innerText.includes('Note') || b.innerText.includes('VS') || b.innerText.includes('IDE'))) {
                btns.push({ text: b.innerText.substring(0, 50), visible: b.offsetParent !== null });
              }
            });
            return btns;
          });
          console.log('  SFTP 面板编辑按钮数量: ' + sftpEditBtns.length);
          sftpEditBtns.forEach(function (b, i) {
            console.log('    [' + i + '] ' + b.text + ' (visible: ' + b.visible + ')');
          });
        }
      }
    }
  } catch (e) {
    console.log('  ❌ ' + e.message);
    errors.push({ step: 'ssh-sftp', error: e.message });
  }

  // ========== 3. 系统配置 - 外部打开器配置 ==========
  console.log('\n=== 3. 系统配置 - 外部打开器 ===');
  try {
    await page.evaluate(function () { location.hash = '#/config'; });
    await page.waitForTimeout(1000);

    // 滚动到外部打开器部分
    await page.evaluate(function () {
      const headings = document.querySelectorAll('h3');
      for (const h of headings) {
        if (h.innerText && h.innerText.includes('外部打开器')) {
          h.scrollIntoView({ behavior: 'smooth', block: 'center' });
          break;
        }
      }
    });
    await page.waitForTimeout(500);

    await page.screenshot({ path: path.join(OUTPUT_DIR, 'config-external-openers.png'), fullPage: true });
    console.log('  ✅ 外部打开器配置截图');

    const openersInfo = await page.evaluate(function () {
      const info = [];
      const inputs = document.querySelectorAll('input[placeholder*="名称"]');
      inputs.forEach(function (input) {
        if (input.value) {
          info.push(input.value.substring(0, 50));
        }
      });
      return info;
    });
    console.log('  已配置打开器: ' + openersInfo.join(', '));
  } catch (e) {
    console.log('  ❌ ' + e.message);
    errors.push({ step: 'config-openers', error: e.message });
  }

  // ========== 4. 下载历史 - 打开器按钮 ==========
  console.log('\n=== 4. 下载历史 ===');
  try {
    await page.evaluate(function () { location.hash = '#/downloads'; });
    await page.waitForTimeout(1000);

    await page.screenshot({ path: path.join(OUTPUT_DIR, 'downloads-history.png'), fullPage: true });
    console.log('  ✅ 下载历史页面截图');

    const downloadsContent = await page.evaluate(function () {
      return document.body.innerText.substring(0, 500);
    });
    console.log('  下载历史内容预览: ' + downloadsContent.substring(0, 200));
  } catch (e) {
    console.log('  ❌ ' + e.message);
    errors.push({ step: 'downloads', error: e.message });
  }

  // ========== 5. 多主题下的文件页面 ==========
  console.log('\n=== 5. 多主题文件页面截图 ===');
  const themes = [
    { name: 'light', label: '浅色' },
    { name: 'green', label: '护眼绿' },
    { name: 'hc', label: '高对比' },
    { name: 'xianxia', label: '武侠' },
    { name: 'dark', label: '深色' },
  ];

  try {
    await page.evaluate(function () { location.hash = '#/files'; });
    await page.waitForTimeout(500);

    // 确保已连接
    await page.selectOption('select', { value: '本地测试' });
    await page.waitForTimeout(300);
    await page.click('button:has-text("连接并浏览")');
    await page.waitForTimeout(3000);

    for (const theme of themes) {
      try {
        await page.evaluate(function (t) {
          if (window.Kairo && window.Kairo.theme && window.Kairo.theme.set) {
            window.Kairo.theme.set(t);
          } else {
            document.documentElement.setAttribute('data-theme', t);
          }
        }, theme.name);
        await page.waitForTimeout(500);

        await page.screenshot({
          path: path.join(OUTPUT_DIR, 'files-' + theme.name + '.png'),
          fullPage: true
        });
        console.log('  ✅ ' + theme.label + '主题');
      } catch (e) {
        console.log('  ❌ ' + theme.label + ': ' + e.message);
        errors.push({ step: 'theme-' + theme.name, error: e.message });
      }
    }
  } catch (e) {
    console.log('  ❌ ' + e.message);
    errors.push({ step: 'themes-files', error: e.message });
  }

  // ========== 6. 控制台错误检查 ==========
  console.log('\n=== 6. 控制台错误检查 ===');
  const pageErrors = [];
  page.on('pageerror', function (err) {
    pageErrors.push(err.message);
  });

  const consoleErrors = await page.evaluate(function () {
    // 尝试从全局获取
    if (window.__consoleErrors) return window.__consoleErrors;
    return [];
  });

  if (pageErrors.length > 0) {
    console.log('  ⚠️  页面错误 (' + pageErrors.length + '):');
    pageErrors.forEach(function (e, i) {
      console.log('    [' + i + '] ' + e.substring(0, 200));
    });
  } else {
    console.log('  ✅ 无页面错误');
  }

  console.log('\n关闭浏览器...');
  await browser.close();

  console.log('\n=== 测试完成 ===');
  console.log('输出目录: ' + OUTPUT_DIR);
  console.log('总错误数: ' + errors.length);
  if (errors.length > 0) {
    console.log('错误详情:');
    for (const e of errors) {
      console.log('  - [' + e.step + '] ' + e.error.substring(0, 200));
    }
  }
}

main().catch(console.error);
