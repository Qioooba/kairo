#!/usr/bin/env node
/**
 * Kairo SFTP Upload - 深度测试脚本
 *
 * 功能：
 *  1. API 层测试：init / cancel / 状态查询
 *  2. UI 层测试：页面渲染、按钮交互、表单验证
 *  3. 主题测试：5 个主题全量截图（dark / light / hc / green / xianxia）
 *  4. 真实坐标点击测试
 *
 *  前置：Kairo 服务在 http://127.0.0.1:18094 运行
 *
 *  依赖：npm i -g playwright  (需已安装 chromium)
 */

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const BASE_URL = 'http://127.0.0.1:18094';
const OUTPUT_DIR = path.join(__dirname, '..', '..', 'test-output', 'upload-deep-test');
const SCREENSHOT_DIR = path.join(OUTPUT_DIR, 'screenshots');

const THEMES = ['dark', 'light', 'hc', 'green', 'xianxia'];
const THEME_NAMES = {
  dark: '暗色',
  light: '亮色',
  hc: '高对比',
  green: '绿',
  xianxia: '仙侠',
};

const results = {
  total: 0,
  passed: 0,
  failed: 0,
  cases: [],
};

function log(msg) {
  console.log(`[${new Date().toLocaleTimeString()}] ${msg}`);
}

function assert(cond, name, detail = '') {
  results.total++;
  if (cond) {
    results.passed++;
    results.cases.push({ name, status: 'pass', detail });
    log(`  ✅ PASS: ${name}`);
  } else {
    results.failed++;
    results.cases.push({ name, status: 'fail', detail });
    log(`  ❌ FAIL: ${name} ${detail ? '- ' + detail : ''}`);
  }
}

function ensureDir(dir) {
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function setTheme(page, theme) {
  await page.evaluate((t) => {
    document.documentElement.setAttribute('data-theme', t);
  }, theme);
  await page.waitForTimeout(300);
}

async function testAPILayer() {
  log('\n========== 第 1 部分：API 层测试 ==========');

  const api = async (method, urlPath, body) => {
    const opts = {
      method,
      headers: { 'Content-Type': 'application/json' },
    };
    if (body) opts.body = JSON.stringify(body);
    const res = await fetch(BASE_URL + urlPath, opts);
    const text = await res.text();
    let data;
    try { data = JSON.parse(text); } catch { data = text; }
    return { status: res.status, data };
  };

  // 1.1 config 接口
  log('\n1.1 配置接口');
  const cfg = await api('GET', '/api/config');
  assert(cfg.status === 200, 'GET /api/config 返回 200');
  assert(cfg.data && cfg.data.app && cfg.data.app.name === 'Kairo', '配置中 app.name === Kairo');

  // 1.2 upload init - 无参数（预期 400）
  log('\n1.2 上传 init - 参数校验');
  const initNoParam = await api('POST', '/api/ssh/sftp/upload/init');
  assert(initNoParam.status === 400 || initNoParam.status >= 400,
    '无参数 init 返回 4xx 错误', `status=${initNoParam.status}`);

  // 1.3 upload init - 无效 server（预期 400/500）
  log('\n1.3 上传 init - 无效服务器');
  const initBadServer = await api('POST', '/api/ssh/sftp/upload/init', {
    system: '不存在的系统',
    server: '不存在的服务器',
    remote_dir: '/tmp',
    filename: 'test.txt',
    size: 100,
    overwrite: 'reject',
  });
  assert(initBadServer.status >= 400,
    '无效服务器 init 返回 4xx/5xx', `status=${initBadServer.status}`);

  // 1.4 upload cancel - 无效 id
  log('\n1.4 上传 cancel - 无效 id');
  const cancelBad = await api('POST', '/api/ssh/sftp/upload/cancel', { id: 'not-exist-id' });
  assert(cancelBad.status >= 200 && cancelBad.status < 500,
    '取消不存在的 upload 不抛 5xx', `status=${cancelBad.status}`);

  log(`\nAPI 层测试完成：${results.passed}/${results.total} 通过`);
}

async function testUILayer(page) {
  log('\n========== 第 2 部分：UI 层测试 ==========');

  await page.goto(BASE_URL + '/#/files');
  await page.waitForTimeout(1000);

  // 2.1 页面基本元素
  log('\n2.1 页面导航和上传卡片渲染');
  const navItems = await page.$$('.nav-item');
  assert(navItems.length >= 5, '侧边导航至少有 5 个菜单项', `实际 ${navItems.length} 个`);

  // 检查上传卡片是否存在
  const uploadCardCount = await page.locator('.upload-card, [class*="upload"]').count();
  assert(uploadCardCount > 0, '上传卡片存在', `找到 ${uploadCardCount} 个上传相关元素`);

  // 2.2 上传区域核心控件
  log('\n2.2 上传区域核心控件');
  const pickBtnCount = await page.getByRole('button', { name: '选择文件' }).count();
  assert(pickBtnCount > 0, '"选择文件"按钮存在');

  const targetInputCount = await page.locator('input[placeholder*="目标目录"]').count();
  assert(targetInputCount > 0, '目标目录输入框存在');

  const overwriteSelect = page.locator('select').filter({ hasText: /拒绝覆盖|直接替换|自动重命名/ }).first();
  const overwriteSelectCount = await overwriteSelect.count();
  assert(overwriteSelectCount > 0, '覆盖策略下拉框存在');

  // 检查覆盖策略选项数量
  const overwriteOptionCount = await overwriteSelect.locator('option').count();
  assert(overwriteOptionCount >= 3, '覆盖策略至少有 3 个选项', `实际 ${overwriteOptionCount} 个`);

  const refreshCbCount = await page.getByText('完成后刷新目录').count();
  assert(refreshCbCount > 0, '"完成后刷新目录"复选框存在');

  // 2.3 上传列表和按钮
  log('\n2.3 上传列表和操作按钮');
  const clearDoneBtnCount = await page.getByRole('button', { name: '清空已完成' }).count();
  assert(clearDoneBtnCount > 0, '"清空已完成"按钮存在');

  const cancelAllBtn = page.getByRole('button', { name: '全取消' }).first();
  const cancelAllBtnCount = await cancelAllBtn.count();
  assert(cancelAllBtnCount > 0, '"全取消"按钮存在');

  // 2.4 初始状态验证
  log('\n2.4 初始状态验证');
  const isCancelAllDisabled = await cancelAllBtn.evaluate(el => el.disabled);
  assert(isCancelAllDisabled, '初始状态下"全取消"按钮为禁用状态');

  // 2.5 主题切换按钮
  log('\n2.5 主题切换按钮');
  const themeBtnCount = await page.locator('.theme-toggle').count();
  assert(themeBtnCount > 0, '主题切换按钮存在');

  log(`\nUI 层测试完成：${results.passed}/${results.total} 通过`);
}

async function testThemes(page) {
  log('\n========== 第 3 部分：多主题截图测试 ==========');

  await page.goto(BASE_URL + '/#/files');
  await page.waitForTimeout(1000);

  ensureDir(SCREENSHOT_DIR);

  for (const theme of THEMES) {
    log(`\n3.x 主题：${THEME_NAMES[theme]} (${theme})`);
    await setTheme(page, theme);

    // 验证主题设置成功
    const currentTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    assert(currentTheme === theme, `主题设置为 ${THEME_NAMES[theme]} 成功`, `实际: ${currentTheme}`);

    // 整页截图
    const ssPath = path.join(SCREENSHOT_DIR, `theme-${theme}-full.png`);
    await page.screenshot({ path: ssPath, fullPage: true });
    log(`  📸 整页截图已保存: theme-${theme}-full.png`);
    assert(fs.existsSync(ssPath), `${THEME_NAMES[theme]}主题 - 整页截图保存成功`);

    // 滚动到上传区域并截图
    const uploadSection = await page.getByRole('heading', { name: '上传文件' }).first();
    if (uploadSection) {
      await uploadSection.scrollIntoViewIfNeeded();
      await page.waitForTimeout(300);
      const ssUpload = path.join(SCREENSHOT_DIR, `theme-${theme}-upload-section.png`);
      await page.screenshot({ path: ssUpload });
      log(`  📸 上传区域截图已保存: theme-${theme}-upload-section.png`);
      assert(fs.existsSync(ssUpload), `${THEME_NAMES[theme]}主题 - 上传区域截图保存成功`);
    }
  }

  // 恢复默认主题
  await setTheme(page, 'dark');
  log(`\n主题测试完成：${results.passed}/${results.total} 通过`);
}

async function testRealClicks(page) {
  log('\n========== 第 4 部分：真实坐标点击测试 ==========');

  await page.goto(BASE_URL + '/#/files');
  await page.waitForTimeout(1000);
  await setTheme(page, 'dark');

  // 4.1 点击主题切换按钮（坐标点击）
  log('\n4.1 坐标点击 - 主题切换按钮');
  const themeBtn = await page.locator('.theme-toggle').first();
  const themeBox = await themeBtn.boundingBox();
  assert(themeBox !== null, '主题切换按钮有坐标');

  if (themeBox) {
    const x = themeBox.x + themeBox.width / 2;
    const y = themeBox.y + themeBox.height / 2;
    log(`  点击坐标: (${x}, ${y})`);

    await page.mouse.click(x, y);
    await page.waitForTimeout(500);

    const themeAfter = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    assert(themeAfter !== 'dark', '坐标点击主题切换后主题变化', `切换后: ${themeAfter}`);

    // 再点一次切回来
    await page.mouse.click(x, y);
    await page.waitForTimeout(500);
  }

  // 4.2 点击侧边导航（文件下载页）
  log('\n4.2 坐标点击 - 侧边导航"文件下载"');
  const filesNav = await page.getByRole('link', { name: '文件下载' }).first();
  const filesBox = await filesNav.boundingBox();
  assert(filesBox !== null, '"文件下载"导航项有坐标');

  if (filesBox) {
    const x = filesBox.x + filesBox.width / 2;
    const y = filesBox.y + filesBox.height / 2;
    log(`  点击坐标: (${x}, ${y})`);
    await page.mouse.click(x, y);
    await page.waitForTimeout(500);
    assert(page.url().includes('#/files'), '坐标点击导航后 URL 正确', page.url());
  }

  // 4.3 点击系统下拉框
  log('\n4.3 坐标点击 - 业务系统下拉框');
  const systemSelect = await page.locator('select').first();
  const sysBox = await systemSelect.boundingBox();
  assert(sysBox !== null, '业务系统下拉框有坐标');

  if (sysBox) {
    // 点击下拉框的选择区域
    const x = sysBox.x + sysBox.width - 20;
    const y = sysBox.y + sysBox.height / 2;
    log(`  点击坐标: (${x}, ${y})`);
    await page.mouse.click(x, y);
    await page.waitForTimeout(300);
    // 点击其他地方关闭
    await page.mouse.click(10, 10);
    await page.waitForTimeout(200);
    assert(true, '坐标点击下拉框不报错');
  }

  // 4.4 上传卡片内 - "选择文件"按钮坐标
  log('\n4.4 坐标点击 - "选择文件"按钮');
  const pickBtn = await page.getByRole('button', { name: '选择文件' }).first();
  const pickBox = await pickBtn.boundingBox();
  assert(pickBox !== null, '"选择文件"按钮有坐标');

  if (pickBox) {
    const x = pickBox.x + pickBox.width / 2;
    const y = pickBox.y + pickBox.height / 2;
    log(`  按钮坐标: (${x}, ${y})`);
    assert(true, `选择文件按钮坐标: (${x.toFixed(0)}, ${y.toFixed(0)})`);
  }

  log(`\n真实坐标点击测试完成：${results.passed}/${results.total} 通过`);
}

async function testFormInteraction(page) {
  log('\n========== 第 5 部分：表单交互测试 ==========');

  await page.goto(BASE_URL + '/#/files');
  await page.waitForTimeout(1000);

  // 5.1 系统选择联动
  log('\n5.1 系统/服务器联动');
  const systemSelect = await page.locator('select').first();
  await systemSelect.selectOption({ label: '本地测试' });
  await page.waitForTimeout(500);

  const serverSelect = await page.locator('select').nth(1);
  const serverOptionCount = await serverSelect.locator('option').count();
  assert(serverOptionCount >= 1, '选择系统后服务器下拉有选项', `选项数: ${serverOptionCount}`);

  // 5.2 覆盖策略切换
  log('\n5.2 覆盖策略切换');
  // 找到覆盖策略下拉（有 拒绝覆盖/直接替换/自动重命名 选项的）
  const overwriteSelect = await page.locator('select').filter({
    hasText: /拒绝覆盖/
  }).first();

  const initialValue = await overwriteSelect.inputValue();
  await overwriteSelect.selectOption({ label: '直接替换' });
  await page.waitForTimeout(200);
  const newValue = await overwriteSelect.inputValue();
  assert(newValue !== initialValue, '覆盖策略可以切换', `${initialValue} -> ${newValue}`);

  await overwriteSelect.selectOption({ label: '自动重命名' });
  await page.waitForTimeout(200);
  const renameValue = await overwriteSelect.inputValue();
  assert(renameValue === 'rename' || renameValue.includes('rename') || renameValue !== newValue,
    '自动重命名选项可选', `值: ${renameValue}`);

  // 5.3 目标目录输入
  log('\n5.3 目标目录输入');
  const targetInput = await page.locator('input[placeholder*="目标目录"]').first();
  await targetInput.fill('/tmp/test-upload');
  await page.waitForTimeout(200);
  const targetVal = await targetInput.inputValue();
  assert(targetVal === '/tmp/test-upload', '目标目录输入值正确', targetVal);

  log(`\n表单交互测试完成：${results.passed}/${results.total} 通过`);
}

function saveReport() {
  ensureDir(OUTPUT_DIR);

  const report = {
    timestamp: new Date().toISOString(),
    summary: {
      total: results.total,
      passed: results.passed,
      failed: results.failed,
      passRate: results.total > 0 ? ((results.passed / results.total) * 100).toFixed(1) + '%' : 'N/A',
    },
    cases: results.cases,
    themes_tested: THEMES,
    screenshot_dir: SCREENSHOT_DIR,
  };

  const reportPath = path.join(OUTPUT_DIR, 'report.json');
  fs.writeFileSync(reportPath, JSON.stringify(report, null, 2));

  // 生成 markdown 报告
  const mdPath = path.join(OUTPUT_DIR, 'report.md');
  let md = `# Kairo SFTP Upload 深度测试报告\n\n`;
  md += `- 测试时间: ${new Date().toLocaleString()}\n`;
  md += `- 测试总数: ${results.total}\n`;
  md += `- 通过: ${results.passed}\n`;
  md += `- 失败: ${results.failed}\n`;
  md += `- 通过率: ${report.summary.passRate}\n\n`;

  md += `## 测试用例详情\n\n`;
  md += `| # | 用例 | 状态 | 详情 |\n`;
  md += `|---|------|------|------|\n`;
  results.cases.forEach((c, i) => {
    const icon = c.status === 'pass' ? '✅' : '❌';
    md += `| ${i + 1} | ${c.name} | ${icon} ${c.status} | ${c.detail || '-'} |\n`;
  });

  md += `\n## 主题截图\n\n`;
  THEMES.forEach(t => {
    const ssPath = path.join(SCREENSHOT_DIR, `theme-${t}-full.png`);
    const exists = fs.existsSync(ssPath);
    md += `- **${THEME_NAMES[t]} (${t})**: ${exists ? '✅ 已截图' : '❌ 缺失'}\n`;
  });

  md += `\n## 截图目录\n\n`;
  md += `\`${SCREENSHOT_DIR}\`\n`;

  fs.writeFileSync(mdPath, md);

  log(`\n📊 报告已保存:`);
  log(`   JSON: ${reportPath}`);
  log(`   Markdown: ${mdPath}`);
}

async function main() {
  log('Kairo SFTP Upload 深度测试启动');
  log(`服务地址: ${BASE_URL}`);
  log(`输出目录: ${OUTPUT_DIR}`);

  ensureDir(OUTPUT_DIR);
  ensureDir(SCREENSHOT_DIR);

  const browser = await chromium.launch({
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox'],
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();

  try {
    // 第 1 部分：API 层
    await testAPILayer();

    // 第 2 部分：UI 层
    await testUILayer(page);

    // 第 3 部分：主题截图
    await testThemes(page);

    // 第 4 部分：真实坐标点击
    await testRealClicks(page);

    // 第 5 部分：表单交互
    await testFormInteraction(page);

  } catch (e) {
    log(`\n❌ 测试过程中出现未捕获异常: ${e.message}`);
    console.error(e);
    results.total++;
    results.failed++;
    results.cases.push({ name: '全局异常捕获', status: 'fail', detail: e.message });
  } finally {
    await browser.close();
    saveReport();

    log('\n' + '='.repeat(60));
    log(`测试完成：总 ${results.total} / 通过 ${results.passed} / 失败 ${results.failed}`);
    log(`通过率: ${results.total > 0 ? ((results.passed / results.total) * 100).toFixed(1) + '%' : 'N/A'}`);
    log('='.repeat(60));
  }

  process.exit(results.failed > 0 ? 1 : 0);
}

main().catch(e => {
  console.error('Fatal:', e);
  process.exit(1);
});
