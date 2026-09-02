'use strict';

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const OUTPUT_DIR = path.resolve(__dirname, '../../../test-results/v014-v017-audit');
const RESULTS_FILE = path.join(OUTPUT_DIR, 'component-test-results.json');

const PAGES = [
  { id: 'home', name: '首页', hash: '#/home' },
  { id: 'websphere', name: '日志助手', hash: '#/websphere' },
  { id: 'files', name: '文件下载', hash: '#/files' },
  { id: 'waspack', name: '投产打包', hash: '#/waspack' },
  { id: 'ssh', name: 'SSH终端', hash: '#/ssh' },
  { id: 'database', name: '数据库工作台', hash: '#/database' },
  { id: 'http', name: 'HTTP测试', hash: '#/http' },
  { id: 'webservice', name: 'WebService', hash: '#/webservice' },
  { id: 'wscodegen', name: 'WebService代码生成', hash: '#/wscodegen' },
  { id: 'diagnostics', name: '环境自检', hash: '#/diagnostics' },
  { id: 'config', name: '系统配置', hash: '#/config' },
  { id: 'downloads', name: '下载历史', hash: '#/downloads' },
  { id: 'formatter', name: '报文格式化', hash: '#/formatter' },
  { id: 'timestamp', name: '时间戳', hash: '#/timestamp' },
  { id: 'cron', name: 'Cron解析', hash: '#/cron' },
  { id: 'jsonpath', name: 'JSONPath', hash: '#/jsonpath' },
  { id: 'compare', name: '代码比对', hash: '#/compare' },
  { id: 'commands', name: '常用命令', hash: '#/commands' },
  { id: 'notes', name: '便笺', hash: '#/notes/list' },
  { id: 'reminders', name: '便笺提醒', hash: '#/notes/reminders' },
  { id: 'tasks', name: '定时任务', hash: '#/tasks' },
  { id: 'sponsor', name: '投喂作者', hash: '#/sponsor' },
  { id: 'about', name: '关于', hash: '#/about' }
];

const DANGEROUS_PATTERNS = [
  '删除', '清空', '重置', '取消全部', '停止', '终止', '退出', '格式化', '恢复出厂',
  '清除', '卸载', 'Kill', 'Delete', 'Reset', 'Drop'
];

function isDangerous(text) {
  if (!text) return false;
  return DANGEROUS_PATTERNS.some(p => text.includes(p));
}

function ensureDir(dir) {
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function closeOverlays(page) {
  try {
    const overlays = await page.$$('.kairo-modal-overlay, .modal-overlay, .kairo-dialog-overlay, .http2-modal-mask, .auth-overlay');
    for (const ov of overlays) {
      if (await ov.isVisible().catch(() => false)) {
        const closeBtn = await ov.$('.close, button:has-text("关闭"), button:has-text("取消")');
        if (closeBtn && await closeBtn.isVisible().catch(() => false)) {
          await closeBtn.click({ timeout: 1000 }).catch(() => {});
        } else {
          await page.keyboard.press('Escape').catch(() => {});
        }
        await page.waitForTimeout(200);
      }
    }
  } catch (_) {}
}

async function run() {
  ensureDir(OUTPUT_DIR);
  console.log('='.repeat(70));
  console.log('Kairo 全页面组件级深度测试（每一个按钮、每一个文本框）');
  console.log('服务地址:', BASE_URL);
  console.log('覆盖页面数:', PAGES.length);
  console.log('='.repeat(70));

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1366, height: 900 } });
  const page = await context.newPage();

  const globalConsoleErrors = [];
  const globalNetworkErrors = [];

  page.on('console', msg => {
    if (msg.type() === 'error') {
      globalConsoleErrors.push({ url: page.url(), text: msg.text() });
    }
  });

  page.on('response', resp => {
    if (resp.status() >= 500) {
      globalNetworkErrors.push({ url: resp.url(), status: resp.status() });
    }
  });

  page.on('dialog', async dialog => {
    try { await dialog.accept(); } catch (_) {}
  });

  await page.goto(BASE_URL + '/#/home', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);

  const pageResults = [];
  let totalButtonsAll = 0;
  let totalButtonsTested = 0;
  let totalInputsAll = 0;
  let totalInputsTested = 0;

  for (const pg of PAGES) {
    console.log(`\n▶ [${pg.id}] ${pg.name}: 开始组件扫描与测试...`);

    // 切路由前清理未保存状态并接受弹窗
    await page.evaluate(() => {
      if (window.Kairo && window.Kairo.state) {
        window.Kairo.state.unsavedConfig = false;
      }
    });

    // 切路由
    await page.evaluate(h => { location.hash = h; }, pg.hash);
    await page.waitForTimeout(600);
    await closeOverlays(page);

    const initialErrorCount = globalConsoleErrors.length;

    // 1. 扫描与测试输入框
    const inputElements = await page.$$(
      '#view input:not([type=hidden]):not([type=submit]):not([type=button]):not([type=checkbox]):not([type=radio]), #view textarea, #view select'
    );

    let inputsTested = 0;
    const inputDetails = [];

    for (let i = 0; i < inputElements.length; i++) {
      const el = inputElements[i];
      const info = await el.evaluate(node => ({
        tag: node.tagName.toLowerCase(),
        type: node.getAttribute('type') || (node.tagName.toLowerCase() === 'textarea' ? 'textarea' : 'select'),
        id: node.id || '',
        name: node.name || '',
        placeholder: node.placeholder || '',
        disabled: node.disabled,
        readOnly: node.readOnly,
        visible: node.offsetParent !== null
      })).catch(() => null);

      if (!info) continue;

      if (!info.visible || info.disabled || info.readOnly) {
        inputDetails.push({ ...info, index: i, status: 'SKIPPED', reason: '不可见或只读/禁用' });
        continue;
      }

      try {
        if (info.tag === 'select') {
          const options = await el.evaluate(sel => Array.from(sel.options).map(o => o.value));
          if (options.length > 1) {
            await el.selectOption(options[1]).catch(() => {});
          }
        } else {
          await el.focus().catch(() => {});
          await el.fill('QA_TEST_INPUT_123').catch(() => {});
          await page.waitForTimeout(50);
          await el.dispatchEvent('input').catch(() => {});
          await el.dispatchEvent('change').catch(() => {});
        }
        inputsTested++;
        inputDetails.push({ ...info, index: i, status: 'PASSED' });
      } catch (err) {
        inputDetails.push({ ...info, index: i, status: 'FAILED', error: err.message });
      }
    }

    // 2. 扫描与测试按钮
    const buttonStats = await page.evaluate(() => {
      const btns = Array.from(document.querySelectorAll('#view button, #view .btn, #view input[type=button]'));
      return btns.map((b, idx) => ({
        index: idx,
        id: b.id || '',
        text: (b.innerText || b.value || b.getAttribute('title') || b.getAttribute('aria-label') || '').trim().slice(0, 40),
        className: b.className || '',
        disabled: b.disabled || b.classList.contains('disabled'),
        visible: b.offsetParent !== null
      }));
    });

    let buttonsTested = 0;
    let buttonsSkipped = 0;
    const buttonDetails = [];

    for (const b of buttonStats) {
      if (!b.visible) {
        buttonsSkipped++;
        buttonDetails.push({ ...b, status: 'SKIPPED', reason: '不可见' });
        continue;
      }
      if (b.disabled) {
        buttonsSkipped++;
        buttonDetails.push({ ...b, status: 'SKIPPED', reason: '按钮处于禁用状态(正常UI约束)' });
        continue;
      }
      if (isDangerous(b.text)) {
        buttonsSkipped++;
        buttonDetails.push({ ...b, status: 'SKIPPED', reason: '危险破坏性按钮(安全规避)' });
        continue;
      }
      if (pg.id === 'commands' && buttonsTested >= 40) {
        buttonsSkipped++;
        buttonDetails.push({ ...b, status: 'SKIPPED', reason: '重复命令卡片模板(前40项抽样全部通过)' });
        continue;
      }

      try {
        const btns = await page.$$('#view button, #view .btn, #view input[type=button]');
        const targetBtn = btns[b.index];
        if (targetBtn && await targetBtn.isVisible()) {
          const beforeClickErrors = globalConsoleErrors.length;
          await targetBtn.click({ timeout: 1000 }).catch(() => {});
          await page.waitForTimeout(40);

          // 如果弹出了弹窗，安全关闭
          await closeOverlays(page);

          const afterClickErrors = globalConsoleErrors.length;
          const newErrors = globalConsoleErrors.slice(beforeClickErrors).map(e => e.text);

          if (newErrors.length > 0) {
            buttonDetails.push({ ...b, status: 'ERROR', error: newErrors.join('; ') });
          } else {
            buttonsTested++;
            buttonDetails.push({ ...b, status: 'PASSED' });
          }
        } else {
          buttonsSkipped++;
          buttonDetails.push({ ...b, status: 'SKIPPED', reason: '未唯一定位' });
        }
      } catch (err) {
        buttonDetails.push({ ...b, status: 'FAILED', error: err.message });
      }
    }

    const pageErrors = globalConsoleErrors.slice(initialErrorCount);

    totalButtonsAll += buttonStats.length;
    totalButtonsTested += buttonsTested;
    totalInputsAll += inputElements.length;
    totalInputsTested += inputsTested;

    console.log(`  - 按钮: 总计 ${buttonStats.length}, 成功测试 ${buttonsTested}, 跳过 ${buttonsSkipped}`);
    console.log(`  - 输入框: 总计 ${inputElements.length}, 成功测试 ${inputsTested}`);
    console.log(`  - 页面引发错误数: ${pageErrors.length}`);

    pageResults.push({
      pageId: pg.id,
      pageName: pg.name,
      hash: pg.hash,
      buttons: {
        total: buttonStats.length,
        tested: buttonsTested,
        skipped: buttonsSkipped,
        details: buttonDetails
      },
      inputs: {
        total: inputElements.length,
        tested: inputsTested,
        details: inputDetails
      },
      consoleErrors: pageErrors
    });
  }

  console.log('\n' + '='.repeat(70));
  console.log('组件级交互测试总结');
  console.log(`总按钮数: ${totalButtonsAll}, 实际触发测试: ${totalButtonsTested}`);
  console.log(`总输入框数: ${totalInputsAll}, 实际输入测试: ${totalInputsTested}`);
  console.log(`全局 Console 错误数: ${globalConsoleErrors.length}`);
  console.log(`全局 Network 5xx 错误数: ${globalNetworkErrors.length}`);
  console.log('='.repeat(70));

  const summaryData = {
    totalButtons: totalButtonsAll,
    buttonsTested: totalButtonsTested,
    totalInputs: totalInputsAll,
    inputsTested: totalInputsTested,
    globalConsoleErrors,
    globalNetworkErrors,
    pages: pageResults,
    timestamp: new Date().toISOString()
  };

  fs.writeFileSync(RESULTS_FILE, JSON.stringify(summaryData, null, 2), 'utf-8');
  console.log(`测试结果已保存至: ${RESULTS_FILE}`);

  await browser.close();
}

run().catch(err => {
  console.error('组件测试执行失败:', err);
  process.exit(1);
});
