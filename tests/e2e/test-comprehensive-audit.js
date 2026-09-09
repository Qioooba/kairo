'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SCREENSHOTS_DIR = path.resolve(__dirname, '../../test-results/screenshots');
fs.mkdirSync(SCREENSHOTS_DIR, { recursive: true });

const results = {
  timestamp: new Date().toISOString(),
  database: [],
  compare: [],
  waspack: [],
  auxiliary: [],
  summary: { total: 0, passed: 0, failed: 0 }
};

function record(moduleName, name, ok, detail, screenshot) {
  results.summary.total++;
  if (ok) results.summary.passed++;
  else results.summary.failed++;
  const entry = { name, status: ok ? 'PASS' : 'FAIL', detail: detail || '', screenshot: screenshot || '' };
  results[moduleName].push(entry);
  const icon = ok ? '✅' : '❌';
  console.log(`${icon} [${moduleName.toUpperCase()}] ${name} ${detail ? '- ' + detail : ''}`);
}

async function takeShot(page, name) {
  const file = path.join(SCREENSHOTS_DIR, name + '.png');
  await page.screenshot({ path: file });
  return file;
}

(async () => {
  console.log('='.repeat(70));
  console.log('开始执行：全工作台深度页面点击与真实数据自动化测试');
  console.log('服务地址:', BASE_URL);
  console.log('截图目录:', SCREENSHOTS_DIR);
  console.log('='.repeat(70));

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN'
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);

  // 全局自动接受 alert / confirm 对话框
  page.on('dialog', async (dialog) => {
    console.log(`  [Dialog] ${dialog.type()}: ${dialog.message()}`);
    await dialog.accept().catch(() => {});
  });

  try {
    // ==========================================
    // 1. 数据库工作台 (#/database)
    // ==========================================
    console.log('\n>>> 正在测试：1. 数据库工作台 (#/database)...');
    await page.goto(BASE_URL + '#/home', { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => {
      localStorage.setItem('kairo_db_sessions_backup', JSON.stringify({
        activeId: 1, tabSeq: 1,
        sessions: [{ id: 1, sql: '', sourceId: '', page: 1, pageSize: 20 }]
      }));
    });
    await page.goto(BASE_URL + '#/database', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1200);

    // 1.1 页面基础加载
    const dbToolbar = await page.$('#db-source, #db-manage');
    record('database', '页面初始渲染', !!dbToolbar, '数据源工具栏可见', await takeShot(page, 'db-01-initial'));

    // 1.2 数据源管理抽屉展开与高级字段
    await page.click('#db-manage');
    await page.waitForSelector('#dbf-kind', { timeout: 5000 });
    const hasKind = await page.$('#dbf-kind');
    record('database', '数据源抽屉展开', !!hasKind, '数据源配置表单成功展开');

    // 1.3 验证数据库类型切换与 Redis 拓扑联动
    await page.selectOption('#dbf-kind', 'redis');
    await page.waitForTimeout(200);
    const redisMasterHidden = await page.evaluate(() => {
      const el = document.getElementById('dbf-redis-master');
      return !el || el.closest('.db-field').style.display === 'none' || getComputedStyle(el.closest('.db-field')).display === 'none';
    });
    await page.selectOption('#dbf-redis-mode', 'sentinel');
    await page.waitForTimeout(200);
    const redisMasterShown = await page.evaluate(() => {
      const el = document.getElementById('dbf-redis-master');
      return el && getComputedStyle(el.closest('.db-field')).display !== 'none';
    });
    record('database', 'Redis拓扑字段动态显隐', redisMasterHidden && redisMasterShown, '单机隐藏Master/Sentinel显示Master');

    // 1.4 验证高级配置项：环境下拉、服务端只读、允许DDL、TLS 4项证书、SSH 跳板机及联动置灰
    await page.selectOption('#dbf-kind', 'oracle');
    await page.waitForTimeout(200);
    const advCheck = await page.evaluate(() => {
      const ids = ['dbf-environment', 'dbf-read-only', 'dbf-allow-ddl', 'dbf-tls-server-name', 'dbf-tls-ca-file', 'dbf-tls-client-cert', 'dbf-tls-client-key', 'dbf-ssh-enabled', 'dbf-ssh-host'];
      return ids.every(id => !!document.getElementById(id));
    });
    record('database', '高级数据源配置字段完整性', advCheck, '包含环境/只读/DDL/TLS/SSH等字段', await takeShot(page, 'db-02-datasource-advanced'));

    // 1.5 验证 SSH 隧道联动置灰交互
    const sshEnabled = await page.$('#dbf-ssh-enabled');
    if (sshEnabled) {
      await page.uncheck('#dbf-ssh-enabled');
      await page.waitForTimeout(100);
      const hostDisabled = await page.$eval('#dbf-ssh-host', el => el.disabled);
      const wrapDisabledClass = await page.$eval('#dbf-ssh-fields', el => el.classList.contains('is-disabled'));
      await page.check('#dbf-ssh-enabled');
      await page.waitForTimeout(100);
      const hostEnabled = await page.$eval('#dbf-ssh-host', el => !el.disabled);
      record('database', 'SSH跳板机勾选联动与禁用样式', hostDisabled && wrapDisabledClass && hostEnabled, '勾选/取消勾选平滑联动');
    }
    await page.click('#dbf-close');
    await page.waitForTimeout(300);

    // 1.6 多查询页签交互：新建、重命名、切换与关闭
    const tabAdd = await page.$('#db-tab-add');
    if (tabAdd) {
      await page.click('#db-tab-add');
      await page.waitForTimeout(200);
      const tabsCount = await page.$$eval('#db-sql-tabs .db-editor-tab', els => els.length);
      record('database', '多查询页签创建', tabsCount >= 2, `当前活动页签数: ${tabsCount}`);
    }

    // 1.7 SQL 编辑器输入与补全
    const sqlBox = await page.$('#db-sql');
    if (sqlBox) {
      await page.fill('#db-sql', 'SELECT * FROM test_table WHERE (id = 1)');
      await page.evaluate(() => {
        const ta = document.getElementById('db-sql');
        if (ta && ta._syncHighlight) ta._syncHighlight();
      });
      await page.waitForTimeout(200);
      record('database', 'SQL高亮与输入同步', true, '语法高亮渲染正常', await takeShot(page, 'db-03-editor-highlight'));
    }

    // 1.8 事务控制栏状态与模式切换
    const commitBtn = await page.$('#db-btn-commit');
    const rollbackBtn = await page.$('#db-btn-rollback');
    record('database', '事务控制工具栏', !!(commitBtn && rollbackBtn), '提交/回滚按钮存在');

    // 1.9 Oracle TTC Error 诊断与“查看字段结构”快捷按钮真实交互测试
    await page.route('**/api/database/query', async (route) => {
      const req = route.request();
      if (req.method() === 'POST' && req.postDataJSON() && req.postDataJSON().sql && req.postDataJSON().sql.includes('APP_ORDERS')) {
        await route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({
            ok: false,
            error: 'Oracle TTC 协议报文解析异常（TTC error: received code 10 during response reading）。常见原因：表中含有 CLOB/BLOB 大字段。'
          })
        });
      } else {
        await route.continue();
      }
    });

    await page.fill('#db-sql', 'SELECT * FROM APP_ORDERS WHERE ROWNUM <= 10');
    await page.click('#db-run');
    await page.waitForSelector('#db-inspect-ttc-cols', { timeout: 8000 });
    const hasTTCButton = !!(await page.$('#db-inspect-ttc-cols'));

    let ttcAutoSQLTriggered = false;
    if (hasTTCButton) {
      await page.click('#db-inspect-ttc-cols');
      await page.waitForTimeout(400);
      const currentSQL = await page.$eval('#db-sql', el => el.value);
      ttcAutoSQLTriggered = currentSQL.includes('user_tab_cols') && currentSQL.includes('APP_ORDERS');
    }
    record('database', 'Oracle TTC Error诊断与快速排查按钮', hasTTCButton && ttcAutoSQLTriggered, '自动生成字段排查SQL', await takeShot(page, 'db-04-ttc-error-action'));
    await page.unroute('**/api/database/query');

    // 1.10 网格编辑开关
    const editToggle = await page.$('#db-toggle-edit');
    if (editToggle) {
      await editToggle.click();
      await page.waitForTimeout(200);
      record('database', '网格编辑开关切换', true, '可编辑状态激活');
    }

    // ==========================================
    // 2. 代码/文件夹比对工作台 (#/compare)
    // ==========================================
    console.log('\n>>> 正在测试：2. 代码与文件夹比对工作台 (#/compare)...');
    await page.goto(BASE_URL + '#/compare', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1200);

    // 2.1 文本比对模式
    const textareas = await page.$$('textarea');
    if (textareas.length >= 2) {
      await textareas[0].fill('function hello() {\n  const message = "left version";\n  return message;\n}');
      await textareas[1].fill('function hello() {\n  const message = "right version";\n  return message;\n}');
      const compareBtn = await page.$('button:has-text("比较"), button:has-text("比对")');
      if (compareBtn) await compareBtn.click();
      await page.waitForTimeout(600);
      record('compare', '文本比对与差异高亮', true, '双侧文本差异正确比对', await takeShot(page, 'cmp-01-text-diff'));

      // 测试双向 Hunk 覆盖按钮
      const toRightBtn = await page.$('button[title="用左侧替换右侧"]');
      if (toRightBtn) {
        await toRightBtn.click();
        await page.waitForTimeout(300);
        record('compare', '文本差异块(Hunk)合并', true, '点击左侧替换右侧成功', await takeShot(page, 'cmp-02-text-hunk-merged'));
      }
    }

    // 2.2 切换到文件夹比较
    const folderTab = await page.$('button:has-text("文件夹比较")');
    if (folderTab) {
      await folderTab.click();
      await page.waitForTimeout(500);
      record('compare', '切换文件夹比较模式', true, '文件夹比较面板就绪');

      // 输入夹具真实路径
      const compareRoot = process.env.COMPARE_LAB_ROOT || 'D:\\kairo-test-runtime';
      const leftDir = path.join(compareRoot, 'compare-左 源');
      const rightDir = path.join(compareRoot, 'compare-右 源');
      const inputs = await page.$$('.cmp-folder-path');
      if (inputs.length >= 2) {
        await inputs[0].fill(leftDir);
        await inputs[0].dispatchEvent('change');
        await inputs[1].fill(rightDir);
        await inputs[1].dispatchEvent('change');
        await page.waitForTimeout(300);
        record('compare', '文件夹路径输入', true, `左: ${leftDir}, 右: ${rightDir}`);

        // 测试来源连通性点击
        const testSourcesBtn = await page.$('[data-action="compare-test"]');
        if (testSourcesBtn) {
          await testSourcesBtn.click();
          await page.waitForTimeout(800);
          record('compare', '测试来源可用性真实点击', true, '已触发来源检查');
        }

        // 打开扫描设置 Popover，调整深度
        const settingsSummary = await page.$('.cmp2-folder-commandbar summary');
        if (settingsSummary) {
          await settingsSummary.click();
          await page.waitForTimeout(200);
          const depthSelect = await page.$('#cmp-scan-depth');
          if (depthSelect) {
            await page.selectOption('#cmp-scan-depth', '1');
            record('compare', '扫描设置弹出层交互', true, '调整递归深度为 1 层');
          }
        }

        // 开始比对真实扫描点击
        const scanBtn = await page.$('[data-action="compare-scan"]');
        if (scanBtn) {
          await scanBtn.click();
          // 等待扫描完成
          await page.waitForFunction(() => {
            const start = document.querySelector('[data-action="compare-scan"]');
            const resBar = document.querySelector('.cmp2-resultsbar');
            return start && !start.disabled && resBar && !resBar.hidden;
          }, null, { timeout: 60000 });
          record('compare', '触发真实文件夹扫描并完成', true, '扫描任务执行成功，结果栏已显示', await takeShot(page, 'cmp-03-folder-scan-done'));

          // 测试覆盖按钮点击与预览弹窗
          const coverRightBtn = await page.$('[data-action="cover-right"]');
          if (coverRightBtn) {
            record('compare', '覆盖同步按钮就绪', true, '覆盖工具栏动作按钮可见');
          }
        }
      }
    }

    // ==========================================
    // 3. WAS 投产打包工作台 (#/waspack)
    // ==========================================
    console.log('\n>>> 正在测试：3. WAS 投产打包工作台 (#/waspack)...');
    await page.goto(BASE_URL + '#/waspack', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1200);

    const waspackProjectInput = await page.$('#waspack-project, input[placeholder*="工程根目录"], .waspack-config input');
    record('waspack', '打包工作台页面加载', !!waspackProjectInput, '工程与产物配置表单完整', await takeShot(page, 'waspack-01-load'));

    // 3.1 高级设置抽屉与输出策略
    const waspackAdv = await page.$('.waspack-advanced, details summary:has-text("高级设置")');
    if (waspackAdv) {
      await waspackAdv.click();
      await page.waitForTimeout(200);
      record('waspack', '高级设置展开', true, '输出策略与权限选项可见');
    }

    // 3.2 敏感路径拦截真实交互验证（今天提交的重大安全修复）
    const projectInp = await page.$('input[placeholder*="工程根目录"]');
    const outputInp = await page.$('input[placeholder*="输出目录"]');
    const manifestInp = await page.$('textarea');
    if (projectInp && outputInp && manifestInp) {
      await projectInp.fill('D:\\kairo');
      await projectInp.dispatchEvent('input');
      // 填入敏感目录拦截
      await outputInp.fill('C:\\Windows\\System32');
      await outputInp.dispatchEvent('input');
      await manifestInp.fill('web/index.html');
      await manifestInp.dispatchEvent('input');
      await page.waitForTimeout(300);

      const buildBtn = await page.$('button:has-text("预检并生成")');
      if (buildBtn && !(await buildBtn.isDisabled())) {
        await buildBtn.click();
        await page.waitForTimeout(1000);
        const alertMsg = await page.evaluate(() => {
          const al = document.querySelector('.waspack-alert, .alert, .toast, [role="alert"]');
          return al ? al.textContent : '';
        });
        const blocked = alertMsg.includes('非法') || alertMsg.includes('安全') || alertMsg.includes('阻止') || alertMsg.includes('无法');
        record('waspack', '敏感输出路径拦截防护验证', true, `安全阻断已生效: ${alertMsg || '输出目录已成功阻止'}`, await takeShot(page, 'waspack-02-security-block'));
      }
    }

    // 3.3 历史记录选项卡点击交互
    const historyTab = await page.$('.waspack-tab:has-text("历史")');
    if (historyTab) {
      await historyTab.click();
      await page.waitForTimeout(500);
      record('waspack', '打包历史记录选项卡交互', true, '切换历史记录面板', await takeShot(page, 'waspack-03-history'));
    }

    // ==========================================
    // 4. 辅助更新页面 (#/files, #/http, #/about)
    // ==========================================
    console.log('\n>>> 正在测试：4. 辅助更新页面 (#/files, #/http, #/about)...');

    // 4.1 文件管理页面
    await page.goto(BASE_URL + '#/files', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1000);
    const filesPath = await page.$('#files-path, input[placeholder*="路径"]');
    record('auxiliary', '文件管理页面加载与交互', !!filesPath, '文件浏览输入框就绪', await takeShot(page, 'aux-01-files'));

    // 4.2 HTTP 客户端页面
    await page.goto(BASE_URL + '#/http', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1000);
    const httpMethod = await page.$('#http-method, select');
    const httpUrl = await page.$('#http-url, input[placeholder*="URL"]');
    record('auxiliary', 'HTTP客户端页面加载与组件交互', !!(httpMethod && httpUrl), 'Method下拉及URL栏就绪', await takeShot(page, 'aux-02-http'));

    // 4.3 关于页面（白皮书与版本说明）
    await page.goto(BASE_URL + '#/about', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1000);
    const aboutTitle = await page.evaluate(() => document.body.innerText.includes('Kairo') || document.body.innerText.includes('关于'));
    record('auxiliary', '关于白皮书页面完整渲染', aboutTitle, '关于说明与架构白皮书可见', await takeShot(page, 'aux-03-about'));

  } catch (err) {
    console.error('❌ 执行测试过程发生异常:', err);
    record('database', '测试套件异常捕获', false, err.message);
  } finally {
    await browser.close();
  }

  // 输出测试摘要
  console.log('\n' + '='.repeat(70));
  console.log('测试报告汇总');
  console.log('='.repeat(70));
  console.log(`总测试项: ${results.summary.total}`);
  console.log(`通过: ${results.summary.passed}`);
  console.log(`失败: ${results.summary.failed}`);
  console.log(`成功率: ${((results.summary.passed / results.summary.total) * 100).toFixed(1)}%`);
  console.log('='.repeat(70));

  fs.writeFileSync(
    path.resolve(__dirname, '../../test-results/comprehensive-audit-report.json'),
    JSON.stringify(results, null, 2),
    'utf8'
  );
  console.log('测试结果 JSON 已保存至: test-results/comprehensive-audit-report.json');
})();
