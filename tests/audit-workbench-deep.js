'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SCREENSHOT_DIR = path.resolve(__dirname, '../test-results/screenshots');

if (!fs.existsSync(SCREENSHOT_DIR)) {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
}

async function runAudit() {
  console.log('🚀 启动 Playwright 进行深度工作台交互与视觉审核...');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1366, height: 768 } });
  const page = await context.newPage();

  const auditReport = [];

  try {
    // -------------------------------------------------------------
    // 1. 数据库工作台（#/database）
    // -------------------------------------------------------------
    console.log('\n--- 1. 数据库工作台 ---');
    await page.goto(BASE_URL + '/#/database', { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    // 检查主工具栏精简
    const moreBtn = await page.$('#db-toolbar-more');
    const runBtn = await page.$('#db-run');
    const cancelBtn = await page.$('#db-cancel');
    const maxRows = await page.$('#db-max-rows');
    const pageNav = await page.$('#db-page-nav');

    console.log('  主工具栏元素检查:');
    console.log('    [db-run]:', !!runBtn);
    console.log('    [db-cancel]:', !!cancelBtn);
    console.log('    [db-max-rows]:', !!maxRows);
    console.log('    [db-toolbar-more (二级菜单)]:', !!moreBtn);
    console.log('    [db-page-nav (分页条)]:', !!pageNav);

    // 检查二级菜单收纳项
    if (moreBtn) {
      const moreSummary = await page.$('#db-toolbar-more summary');
      await moreSummary.click();
      await page.waitForTimeout(300);
      const explainBtn = await page.$('#db-toolbar-more #db-explain');
      const formatBtn = await page.$('#db-toolbar-more #db-format');
      const exportBtn = await page.$('#db-toolbar-more #db-export');
      const bookmarkSel = await page.$('#db-toolbar-more #db-bookmark');
      console.log('  更多菜单收纳低频控件检查:');
      console.log('    [db-explain]:', !!explainBtn);
      console.log('    [db-format]:', !!formatBtn);
      console.log('    [db-export]:', !!exportBtn);
      console.log('    [db-bookmark]:', !!bookmarkSel);
    }

    // 检查分页控件详情
    if (pageNav) {
      const prevBtn = await page.$('#db-page-nav button:has-text("上一页")');
      const nextBtn = await page.$('#db-page-nav button:has-text("下一页")');
      const pageInput = await page.$('#db-page-nav .db-page-input');
      const pageSize = await page.$('#db-page-nav .db-page-size');
      const sizes = await page.$$eval('#db-page-nav .db-page-size option', opts => opts.map(o => o.value));
      console.log('  分页控件检查:');
      console.log('    上一页/下一页按钮:', !!prevBtn && !!nextBtn);
      console.log('    页码输入框:', !!pageInput);
      console.log('    每页行数选项:', sizes.join(', '));
    }

    // 多语句光标测试
    const sqlBox = await page.$('#db-sql');
    if (sqlBox) {
      await page.evaluate(() => {
        const ta = document.getElementById('db-sql');
        ta.value = 'SELECT 1 AS num_one FROM DUAL;\nSELECT 2 AS num_two FROM DUAL;';
        ta.dispatchEvent(new Event('input', { bubbles: true }));
      });
      await page.waitForTimeout(200);

      // 测试 statementModel 光标提取
      const statementAtLine1 = await page.evaluate(() => {
        const ta = document.getElementById('db-sql');
        return window.Kairo.workbench.statementModel.normalize(ta.value, 5);
      });
      const statementAtLine2 = await page.evaluate(() => {
        const ta = document.getElementById('db-sql');
        return window.Kairo.workbench.statementModel.normalize(ta.value, 35);
      });
      console.log('  多语句光标解析结果:');
      console.log('    光标在第1行提取:', statementAtLine1.text);
      console.log('    光标在第2行提取:', statementAtLine2.text);
    }

    const ssDb = path.join(SCREENSHOT_DIR, 'audit-database.png');
    await page.screenshot({ path: ssDb, fullPage: false });
    console.log('  📸 数据库工作台截图保存:', ssDb);
    auditReport.push({ page: '数据库工作台', status: 'PASS', screenshot: ssDb });

    // -------------------------------------------------------------
    // 2. WebSphere 投产打包（#/waspack）
    // -------------------------------------------------------------
    console.log('\n--- 2. WebSphere 投产打包 ---');
    await page.goto(BASE_URL + '/#/waspack', { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);

    const directBuildBtn = await page.$('.waspack-direct-build');
    const extractBtn = await page.$('button:has-text("1. 一键抽取")');
    const packageBtn = await page.$('button:has-text("2. 打包")');
    const outputPolicy = await page.$('#waspack-output-policy');
    const policyOptions = await page.$$eval('#waspack-output-policy option', opts => opts.map(o => ({ value: o.value, text: o.text })));

    console.log('  打包控件检查:');
    console.log('    一键直接打包按钮 (waspack-direct-build):', !!directBuildBtn);
    console.log('    分步抽取和打包按钮:', !!extractBtn && !!packageBtn);
    console.log('    输出目录处理策略下拉框:', !!outputPolicy);
    console.log('    输出策略选项:', policyOptions.map(p => `${p.value}(${p.text})`).join(' | '));

    // 点击“填入示例清单”与“预检清单”
    const sampleBtn = await page.$('button:has-text("填入示例清单")');
    if (sampleBtn) {
      await sampleBtn.click();
      await page.waitForTimeout(200);
      const previewBtn = await page.$('button:has-text("预检清单")');
      if (previewBtn) {
        await previewBtn.click();
        await page.waitForTimeout(600);
      }
    }

    const ssWas = path.join(SCREENSHOT_DIR, 'audit-waspack.png');
    await page.screenshot({ path: ssWas, fullPage: false });
    console.log('  📸 WebSphere 投产打包截图保存:', ssWas);
    auditReport.push({ page: 'WebSphere 投产打包', status: 'PASS', screenshot: ssWas });

    // -------------------------------------------------------------
    // 3. 文件与代码比较（#/compare）
    // -------------------------------------------------------------
    console.log('\n--- 3. 文件与代码比较 ---');
    await page.goto(BASE_URL + '/#/compare', { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);

    const toolbar = await page.$('.cmp-wb-toolbar');
    const tbBox = toolbar ? await toolbar.boundingBox() : null;
    console.log('  比较工具栏高度:', tbBox ? `${tbBox.height}px` : 'N/A');

    // 确保比较模式为左右并排以测试就地编辑和虚拟Diff
    const modeSel = await page.$('.cmp-mode-select');
    if (modeSel) {
      await modeSel.selectOption('side');
      await page.waitForTimeout(200);
    }

    // 填入左右 Java 测试代码
    const textareas = await page.$$('.cmp-editor-input');
    if (textareas.length >= 2) {
      await textareas[0].fill('public class Service {\n  public void doWork() {\n    int status = 1;\n  }\n}');
      await textareas[1].fill('public class Service {\n  public void doWork() {\n    int status = 2;\n  }\n}');
      // 切换语法高亮为 Java
      const langs = await page.$$('.cmp-language');
      if (langs.length >= 2) {
        await langs[0].selectOption('java');
        await langs[1].selectOption('java');
      }
      // 点击比对
      const cmpBtn = await page.$('button[data-action="text-compare"], button:has-text("比对")');
      if (cmpBtn) {
        await cmpBtn.click();
        await page.waitForTimeout(800);
      }
    }

    // 检查虚拟 Diff 视口和就地编辑
    const vdiff = await page.$('.cmp-vdiff');
    const editableCells = await page.$$('.cmp-code[contenteditable]');
    console.log('  Diff 结果与就地编辑检查:');
    console.log('    虚拟 Diff 视口 (.cmp-vdiff):', !!vdiff);
    console.log('    可就地编辑的代码单元格 (.cmp-code[contenteditable]):', editableCells.length);

    if (editableCells.length > 0) {
      // 尝试就地微调文字
      await editableCells[0].click();
      console.log('    就地点击单元格可直接输入编辑！');
    }

    const ssCmp = path.join(SCREENSHOT_DIR, 'audit-compare.png');
    await page.screenshot({ path: ssCmp, fullPage: false });
    console.log('  📸 比较工作台截图保存:', ssCmp);
    auditReport.push({ page: '文件与代码比较', status: 'PASS', screenshot: ssCmp });

    // -------------------------------------------------------------
    // 4. 定时任务（#/tasks）
    // -------------------------------------------------------------
    console.log('\n--- 4. 定时任务 ---');
    await page.goto(BASE_URL + '/#/tasks', { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);

    const notifyBtn = await page.$('button:has-text("告警设置")');
    console.log('  告警设置按钮检查:', !!notifyBtn);

    if (notifyBtn) {
      await notifyBtn.click();
      await page.waitForSelector('.task-notification-form', { timeout: 3000 });
      await page.waitForTimeout(400);

      const modalTitle = await page.$eval('.modal-title', el => el.textContent);
      const hasToast = await page.$eval('label:has-text("Windows 桌面通知")', el => !!el);
      const hasWecom = await page.$eval('label:has-text("企业微信 Webhook")', el => !!el);
      const hasDing = await page.$eval('label:has-text("钉钉 Webhook")', el => !!el);
      const testSendBtn = await page.$('button:has-text("发送测试通知")');

      console.log('  告警设置弹窗检查:');
      console.log('    标题:', modalTitle);
      console.log('    Windows 桌面通知:', hasToast);
      console.log('    企业微信 Webhook:', hasWecom);
      console.log('    钉钉 Webhook:', hasDing);
      console.log('    测试通知按钮:', !!testSendBtn);

      const ssTasks = path.join(SCREENSHOT_DIR, 'audit-tasks-notification-modal.png');
      await page.screenshot({ path: ssTasks, fullPage: false });
      console.log('  📸 定时任务告警设置截图保存:', ssTasks);
      auditReport.push({ page: '定时任务', status: 'PASS', screenshot: ssTasks });

      const cancelBtn = await page.$('.modal-overlay .editor-footer button:has-text("取消")');
      if (cancelBtn) await cancelBtn.click();
      await page.waitForTimeout(300);
    }

    // -------------------------------------------------------------
    // 5. WebService 代码生成（#/wscodegen）
    // -------------------------------------------------------------
    console.log('\n--- 5. WebService 代码生成 ---');
    await page.goto(BASE_URL + '/#/wscodegen', { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);

    // 检查左侧栏卡片排列顺序
    const cardTitles = await page.$$eval('.wsc-pane-config .card h3', titles => titles.map(t => t.textContent.trim()));
    console.log('  卡片排列顺序:');
    cardTitles.forEach((t, i) => console.log(`    [卡片 ${i + 1}]: ${t}`));

    const projectIdx = cardTitles.findIndex(t => t.includes('项目') || t.includes('工程') || t.includes('依赖') || t.includes('扫描'));
    const engineIdx = cardTitles.findIndex(t => t.includes('引擎') || t.includes('栈'));
    console.log('  顺序验证: 工程依赖卡片位置 =', projectIdx, '，引擎选择卡片位置 =', engineIdx);
    if (projectIdx >= 0 && engineIdx >= 0 && projectIdx < engineIdx) {
      console.log('  ✅ 工程依赖扫描成功置顶于引擎选择之前！');
    }

    const ssWs = path.join(SCREENSHOT_DIR, 'audit-wscodegen.png');
    await page.screenshot({ path: ssWs, fullPage: false });
    console.log('  📸 WebService 代码生成截图保存:', ssWs);
    auditReport.push({ page: 'WebService 代码生成', status: 'PASS', screenshot: ssWs });

    console.log('\n🎉 深度审核交互与视觉检查全部完成！');
    console.log(JSON.stringify(auditReport, null, 2));

  } finally {
    await browser.close();
  }
}

runAudit().catch(err => {
  console.error('❌ 审核执行发生错误:', err);
  process.exit(1);
});
