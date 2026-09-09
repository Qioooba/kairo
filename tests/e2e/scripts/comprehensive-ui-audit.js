'use strict';

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SCREENSHOT_BASE = path.resolve(__dirname, '../../../test-results/ui-audit-screenshots');

const RESOLUTIONS = [
  { id: '1080p', name: '1080p (FHD)', width: 1920, height: 1080 },
  { id: '2k', name: '2K (QHD)', width: 2560, height: 1440 }
];

const THEMES = [
  { id: 'dark', name: '深色' },
  { id: 'light', name: '浅色' },
  { id: 'green', name: '护眼绿' },
  { id: 'hc', name: '高对比' },
  { id: 'xianxia', name: '仙侠' }
];

const auditResults = {
  timestamp: new Date().toISOString(),
  resolutions: RESOLUTIONS.map(r => r.id),
  themes: THEMES.map(t => t.id),
  viewsAudited: 0,
  screenshots: [],
  issues: [],
  summary: { totalChecks: 0, passedChecks: 0, issuesFound: 0 }
};

function recordIssue(resId, themeId, viewName, type, description, elementSelector, details) {
  auditResults.summary.totalChecks++;
  auditResults.summary.issuesFound++;
  const issue = {
    resolution: resId,
    theme: themeId,
    view: viewName,
    type,
    description,
    elementSelector: elementSelector || '',
    details: details || {}
  };
  auditResults.issues.push(issue);
  console.warn(`  ⚠️  [${resId}][${themeId}][${viewName}] UI ISSUE (${type}): ${description}`);
}

function recordPass(resId, themeId, viewName, checkName) {
  auditResults.summary.totalChecks++;
  auditResults.summary.passedChecks++;
}

async function capture(page, resId, themeId, filename, viewName) {
  const dir = path.join(SCREENSHOT_BASE, resId, themeId);
  fs.mkdirSync(dir, { recursive: true });
  const fullPath = path.join(dir, filename + '.png');
  await page.screenshot({ path: fullPath, fullPage: false });
  auditResults.screenshots.push({
    resolution: resId,
    theme: themeId,
    view: viewName,
    filename: filename + '.png',
    path: fullPath
  });
  auditResults.viewsAudited++;
  return fullPath;
}

async function inspectLayout(page, resId, themeId, viewName) {
  // 1. 检查全局水平溢出
  const hOverflow = await page.evaluate(() => {
    const docEl = document.documentElement;
    const body = document.body;
    return {
      scrollWidth: Math.max(docEl.scrollWidth, body.scrollWidth),
      clientWidth: docEl.clientWidth,
      overflow: Math.max(docEl.scrollWidth, body.scrollWidth) > docEl.clientWidth + 2
    };
  });

  if (hOverflow.overflow) {
    recordIssue(resId, themeId, viewName, 'LAYOUT_OVERFLOW_X',
      `页面出现意外横向滚动条 (scrollWidth: ${hOverflow.scrollWidth}px, clientWidth: ${hOverflow.clientWidth}px)`,
      'html/body', hOverflow);
  } else {
    recordPass(resId, themeId, viewName, '页面无异常横向溢出');
  }

  // 2. 检查可见弹窗/抽屉的视口贴合与滚动
  const modalCheck = await page.evaluate(() => {
    const modals = Array.from(document.querySelectorAll('.db-manager, .db-pro-modal, .modal, .waspack-history-drawer, [role="dialog"]'));
    return modals.filter(el => {
      const style = window.getComputedStyle(el);
      return style.display !== 'none' && style.visibility !== 'hidden' && el.offsetHeight > 0;
    }).map(el => {
      const rect = el.getBoundingClientRect();
      const parent = el.closest('#db-manager') || el.parentElement;
      const parentOverflow = parent ? window.getComputedStyle(parent).overflowY : '';
      return {
        className: el.className,
        rect: { top: rect.top, bottom: rect.bottom, height: rect.height, width: rect.width },
        viewportHeight: window.innerHeight,
        exceedsViewport: rect.bottom > window.innerHeight + 10 && parentOverflow !== 'auto' && parentOverflow !== 'scroll',
        overflowY: window.getComputedStyle(el).overflowY
      };
    });
  });

  for (const m of modalCheck) {
    if (m.exceedsViewport && m.overflowY !== 'auto' && m.overflowY !== 'scroll') {
      recordIssue(resId, themeId, viewName, 'MODAL_OVERFLOW',
        `弹窗高度 (${Math.round(m.rect.height)}px) 超出视口高度 (${m.viewportHeight}px) 且缺乏滚动容器`,
        m.className, m);
    } else {
      recordPass(resId, themeId, viewName, '弹窗视口适配正常');
    }
  }

  // 3. 检查按钮可触达尺寸 (宽/高过小或文字截断)
  const buttonCheck = await page.evaluate(() => {
    const btns = Array.from(document.querySelectorAll('button:not([hidden]):not([style*="display: none"])'));
    const badBtns = [];
    for (const b of btns) {
      const rect = b.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) {
        // 忽略完全不可见的或者带图标的超小控制符，但常规有文本按钮高度不应低于 20px
        const text = (b.textContent || '').trim();
        if (text.length > 1 && rect.height < 20) {
          badBtns.push({ text, height: rect.height, width: rect.width, id: b.id, className: b.className });
        }
      }
    }
    return badBtns;
  });

  for (const b of buttonCheck) {
    recordIssue(resId, themeId, viewName, 'BUTTON_TOO_SMALL',
      `按钮尺寸过小（高度 ${Math.round(b.height)}px，文字 "${b.text}"）`,
      b.id ? `#${b.id}` : b.className, b);
  }
}

async function setTheme(page, themeId) {
  await page.evaluate((t) => {
    if (window.Kairo && window.Kairo.theme && window.Kairo.theme.set) {
      window.Kairo.theme.set(t);
    } else {
      document.documentElement.setAttribute('data-theme', t);
      localStorage.setItem('kairo_theme', t);
    }
  }, themeId);
  await page.waitForTimeout(200);
}

(async () => {
  console.log('='.repeat(70));
  console.log('🚀 启动全工作台深度 UI 审核（1080p & 2K / 5 套主题 / 真实点击）');
  console.log('目标服务:', BASE_URL);
  console.log('截图目录:', SCREENSHOT_BASE);
  console.log('='.repeat(70));

  fs.mkdirSync(SCREENSHOT_BASE, { recursive: true });

  const browser = await chromium.launch({ headless: true });

  for (const res of RESOLUTIONS) {
    console.log(`\n======================================================`);
    console.log(`💻 开始分辨率测试: ${res.name} (${res.width}x${res.height})`);
    console.log(`======================================================`);

    const context = await browser.newContext({
      viewport: { width: res.width, height: res.height },
      locale: 'zh-CN'
    });
    const page = await context.newPage();
    page.setDefaultTimeout(20000);

    // 监听与捕获控制台错误
    page.on('console', msg => {
      if (msg.type() === 'error') {
        const text = msg.text();
        if (!text.includes('Failed to load resource') && !text.includes('favicon')) {
          console.warn(`  [Console Error] ${text}`);
        }
      }
    });

    // 默认自动接受 dialog
    page.on('dialog', async dialog => {
      console.log(`  [Dialog] ${dialog.type()}: ${dialog.message()}`);
      await dialog.accept().catch(() => {});
    });

    for (const theme of THEMES) {
      console.log(`\n  🎨 主题切换: [${theme.name}] (${theme.id}) @ ${res.id}`);

      // =================================================================
      // 1. 数据库工作台 (#/database)
      // =================================================================
      await page.goto(BASE_URL + '#/home', { waitUntil: 'domcontentloaded' });
      await setTheme(page, theme.id);

      // 设置默认会话并进入数据库
      await page.evaluate(() => {
        localStorage.setItem('kairo_db_sessions_backup', JSON.stringify({
          activeId: 1, tabSeq: 1,
          sessions: [{ id: 1, sql: 'SELECT * FROM APP_ORDERS WHERE ORDER_STATUS = \'PAID\'', sourceId: '', page: 1, pageSize: 20 }]
        }));
      });
      await page.goto(BASE_URL + '#/database', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(600);
      await setTheme(page, theme.id);

      // 1.1 数据库主界面
      await inspectLayout(page, res.id, theme.id, 'db-01-main');
      await capture(page, res.id, theme.id, 'db-01-main', '数据库主界面');

      // 1.2 点击【数据源管理】抽屉
      const dbManageBtn = await page.$('#db-manage');
      if (dbManageBtn) {
        await dbManageBtn.click();
        await page.waitForSelector('#dbf-kind', { timeout: 4000 }).catch(() => {});
        await page.waitForTimeout(300);

        // 切换到 oracle 展示完整高级设置
        await page.selectOption('#dbf-kind', 'oracle').catch(() => {});
        await page.waitForTimeout(200);

        await inspectLayout(page, res.id, theme.id, 'db-02-datasource-drawer');
        await capture(page, res.id, theme.id, 'db-02-datasource-drawer', '数据源管理抽屉(高级TLS与SSH)');

        // 1.3 测试 SSH 联动勾选与置灰 UI
        const sshCheck = await page.$('#dbf-ssh-enabled');
        if (sshCheck) {
          // 取消勾选 -> 置灰
          await page.uncheck('#dbf-ssh-enabled');
          await page.waitForTimeout(200);
          await capture(page, res.id, theme.id, 'db-03-datasource-ssh-disabled', 'SSH隧道禁用置灰态');

          // 重新勾选
          await page.check('#dbf-ssh-enabled');
          await page.waitForTimeout(200);
        }

        // 关闭抽屉
        const closeBtn = await page.$('#dbf-close, #dbf-close-x');
        if (closeBtn) await closeBtn.click().catch(() => {});
        await page.waitForTimeout(300);
      }

      // 1.4 点击【工作台设置】弹窗
      const settingsBtn = await page.$('#db-settings');
      if (settingsBtn) {
        await settingsBtn.click();
        await page.waitForTimeout(300);
        await inspectLayout(page, res.id, theme.id, 'db-04-settings-modal');
        await capture(page, res.id, theme.id, 'db-04-settings-modal', '工作台设置对话框');

        // 关闭设置弹窗
        const modalClose = await page.$('#db-settings-close, .modal-close');
        if (modalClose) await modalClose.click().catch(() => {});
        await page.waitForTimeout(300);
      }

      // 1.5 模拟真实结果集渲染与网格编辑
      await page.evaluate(() => {
        if (window.Kairo && window.Kairo.database && window.Kairo.database.showQueryResult) {
          window.Kairo.database.showQueryResult('ok', '查询成功', '3 行 (耗时 18ms)', 'SELECT ORDER_ID, BUYER_NAME, AMOUNT, CREATED_AT, STATUS FROM APP_ORDERS', {
            columns: [
              { name: 'ORDER_ID', database_type: 'NUMBER' },
              { name: 'BUYER_NAME', database_type: 'VARCHAR2' },
              { name: 'AMOUNT', database_type: 'DECIMAL' },
              { name: 'CREATED_AT', database_type: 'TIMESTAMP' },
              { name: 'STATUS', database_type: 'VARCHAR2' }
            ],
            rows: [
              [100291, '张三丰 (武当科技)', '8999.00', '2026-09-07 10:20:15', 'SUCCESS'],
              [100292, '李寻欢 (飞刀集团)', '1540.50', '2026-09-07 11:45:00', 'PAID'],
              [100293, '令狐冲 (思过崖网络)', '320.00', '2026-09-07 14:10:22', 'REFUNDED']
            ]
          });
        }
      });
      await page.waitForTimeout(400);
      await inspectLayout(page, res.id, theme.id, 'db-05-query-grid');
      await capture(page, res.id, theme.id, 'db-05-query-grid', '查询结果集可读网格');

      // 切换网格编辑模式
      const editToggle = await page.$('#db-toggle-edit');
      if (editToggle) {
        await editToggle.click();
        await page.waitForTimeout(200);
        await inspectLayout(page, res.id, theme.id, 'db-06-grid-editing');
        await capture(page, res.id, theme.id, 'db-06-grid-editing', '网格实时编辑模式与暂存');
      }

      // 1.6 触发 Oracle TTC Error 诊断与新“查看字段结构”按钮
      await page.evaluate(() => {
        if (window.Kairo && window.Kairo.database && window.Kairo.database.showQueryResult) {
          window.Kairo.database.showQueryResult('error', '查询失败', 'ORA-00600: TTC error: received code 10 during response reading', 'SELECT * FROM APP_ORDERS WHERE STATUS = 1');
        }
      });
      await page.waitForTimeout(300);
      await inspectLayout(page, res.id, theme.id, 'db-07-ttc-error-action');
      await capture(page, res.id, theme.id, 'db-07-ttc-error-action', 'Oracle TTC Error诊断与字段结构快捷排查按钮');

      // 1.7 对象设计器弹窗 (Object Studio)
      await page.evaluate(() => {
        if (window.Kairo && window.Kairo.databaseDesigner && window.Kairo.databaseDesigner.openTableDesigner) {
          window.Kairo.databaseDesigner.openTableDesigner({
            schema: 'KAIRO_ADMIN',
            table: 'APP_ORDERS',
            columns: [
              { name: 'ORDER_ID', type: 'NUMBER', length: 18, pk: true, nullable: false },
              { name: 'BUYER_NAME', type: 'VARCHAR2', length: 128, pk: false, nullable: false },
              { name: 'AMOUNT', type: 'NUMBER', length: 12, precision: 2, nullable: true }
            ]
          });
        } else if (window.Kairo && window.Kairo.databaseFeatures && window.Kairo.databaseFeatures.createModal) {
          window.Kairo.databaseFeatures.createModal({
            title: '对象设计器 · APP_ORDERS',
            body: '<div style="padding:14px;color:var(--text);"><table class="table" style="width:100%;"><thead><tr><th>列名</th><th>数据类型</th><th>长度</th><th>主键</th><th>可为空</th></tr></thead><tbody><tr><td>ORDER_ID</td><td>NUMBER</td><td>18</td><td>✓</td><td>否</td></tr><tr><td>BUYER_NAME</td><td>VARCHAR2</td><td>128</td><td>-</td><td>否</td></tr></tbody></table></div>',
            actions: [{ text: '生成 DDL', className: 'btn btn-primary' }, { text: '关闭' }]
          });
        }
      });
      await page.waitForTimeout(400);
      const designerModal = await page.$('.db-pro-modal, .modal');
      if (designerModal) {
        await inspectLayout(page, res.id, theme.id, 'db-08-object-designer');
        await capture(page, res.id, theme.id, 'db-08-object-designer', '对象设计器(Table/View Designer)弹窗');
        // 关闭弹窗
        const closeBtn = await page.$('.db-pro-modal-close, button:has-text("关闭")');
        if (closeBtn) await closeBtn.click().catch(() => {});
        await page.waitForTimeout(200);
      }

      // =================================================================
      // 2. 文件与代码比对工作台 (#/compare)
      // =================================================================
      await page.goto(BASE_URL + '#/compare', { waitUntil: 'domcontentloaded' });
      await setTheme(page, theme.id);
      await page.waitForTimeout(500);

      // 2.1 文本比对模式与 Hunk 替换按钮
      const textareas = await page.$$('textarea');
      if (textareas.length >= 2) {
        await textareas[0].fill('// Kairo Compare Left v1.0\nfunction handleRequest(req) {\n  const auth = req.headers["authorization"];\n  if (!auth) throw new Error("Unauthorized");\n  return processV1(req);\n}');
        await textareas[1].fill('// Kairo Compare Right v2.0\nfunction handleRequest(req) {\n  const auth = req.headers["authorization"];\n  const token = parseBearer(auth);\n  if (!token) throw new Error("Invalid Bearer Token");\n  return processV2(req, token);\n}');
        const compareBtn = await page.$('button:has-text("比较"), button:has-text("比对")');
        if (compareBtn) await compareBtn.click();
        await page.waitForTimeout(500);

        await inspectLayout(page, res.id, theme.id, 'cmp-01-text-diff');
        await capture(page, res.id, theme.id, 'cmp-01-text-diff', '文本差异高亮与双向替换按钮');
      }

      // 2.2 文件夹比对模式
      const folderTab = await page.$('button:has-text("文件夹比较")');
      if (folderTab) {
        await folderTab.click();
        await page.waitForTimeout(400);

        const compareRoot = 'D:\\kairo-test-runtime';
        const leftDir = path.join(compareRoot, 'compare-左 源');
        const rightDir = path.join(compareRoot, 'compare-右 源');
        const inputs = await page.$$('.cmp-folder-path');
        if (inputs.length >= 2) {
          await inputs[0].fill(leftDir);
          await inputs[0].dispatchEvent('change');
          await inputs[1].fill(rightDir);
          await inputs[1].dispatchEvent('change');
        }

        // 打开扫描设置 Popover
        await page.evaluate(() => {
          const d = Array.from(document.querySelectorAll('details.cmp2-popover')).find(x => x.textContent.includes('扫描设置'));
          if (d) d.open = true;
        });
        await page.waitForTimeout(250);
        await inspectLayout(page, res.id, theme.id, 'cmp-02-scan-popover');
        await capture(page, res.id, theme.id, 'cmp-02-scan-popover', '文件夹比对高级扫描设置Popover');

        // 关闭 Popover
        await page.evaluate(() => {
          const d = Array.from(document.querySelectorAll('details.cmp2-popover')).find(x => x.textContent.includes('扫描设置'));
          if (d) d.open = false;
        });
        await page.waitForTimeout(150);

        // 触发文件夹扫描测试
        const scanBtn = await page.$('[data-action="compare-scan"]');
        if (scanBtn) {
          await scanBtn.click().catch(() => {});
          await page.waitForTimeout(800);
          await inspectLayout(page, res.id, theme.id, 'cmp-03-folder-results');
          await capture(page, res.id, theme.id, 'cmp-03-folder-results', '文件夹扫描与差异结果树');
        }
      }

      // =================================================================
      // 3. WAS 投产打包工作台 (#/waspack)
      // =================================================================
      await page.goto(BASE_URL + '#/waspack', { waitUntil: 'domcontentloaded' });
      await setTheme(page, theme.id);
      await page.waitForTimeout(500);

      // 3.1 主打包配置
      await inspectLayout(page, res.id, theme.id, 'waspack-01-main');
      await capture(page, res.id, theme.id, 'waspack-01-main', '投产打包主配置界面');

      // 3.2 展开高级设置
      const waspackAdv = await page.$('.waspack-advanced, details summary:has-text("高级设置")');
      if (waspackAdv) {
        await waspackAdv.click().catch(() => {});
        await page.waitForTimeout(250);
        await inspectLayout(page, res.id, theme.id, 'waspack-02-advanced');
        await capture(page, res.id, theme.id, 'waspack-02-advanced', '投产打包高级选项折叠面板');
      }

      // 3.3 触发敏感路径安全拦截阻断 UI
      const outputInput = await page.$('#waspack-output, input[placeholder*="输出"]');
      const previewBtn = await page.$('button:has-text("预检"), button:has-text("抽取"), button:has-text("打包")');
      if (outputInput && previewBtn) {
        await outputInput.fill('C:\\Windows\\System32');
        await outputInput.dispatchEvent('change');
        await previewBtn.click().catch(() => {});
        await page.waitForTimeout(500);

        await inspectLayout(page, res.id, theme.id, 'waspack-03-security-block');
        await capture(page, res.id, theme.id, 'waspack-03-security-block', '敏感输出路径拦截防护提示');
      }

      // 3.4 打开打包历史标签页
      const historyTab = await page.$('.waspack-tabs button:has-text("历史"), [role="tab"]:has-text("历史")');
      if (historyTab) {
        await historyTab.click().catch(() => {});
        await page.waitForTimeout(400);
        await inspectLayout(page, res.id, theme.id, 'waspack-04-history-tab');
        await capture(page, res.id, theme.id, 'waspack-04-history-tab', '打包历史记录面板');
      }

      // =================================================================
      // 4. 辅助更新页面 (#/files, #/http, #/about)
      // =================================================================
      // 4.1 文件浏览
      await page.goto(BASE_URL + '#/files', { waitUntil: 'domcontentloaded' });
      await setTheme(page, theme.id);
      await page.waitForTimeout(400);
      await inspectLayout(page, res.id, theme.id, 'aux-01-files');
      await capture(page, res.id, theme.id, 'aux-01-files', '文件下载与浏览页面');

      // 4.2 HTTP 接口测试
      await page.goto(BASE_URL + '#/http', { waitUntil: 'domcontentloaded' });
      await setTheme(page, theme.id);
      await page.waitForTimeout(400);
      await inspectLayout(page, res.id, theme.id, 'aux-02-http');
      await capture(page, res.id, theme.id, 'aux-02-http', 'HTTP客户端接口测试界面');

      // 4.3 关于白皮书
      await page.goto(BASE_URL + '#/about', { waitUntil: 'domcontentloaded' });
      await setTheme(page, theme.id);
      await page.waitForTimeout(400);
      await inspectLayout(page, res.id, theme.id, 'aux-03-about');
      await capture(page, res.id, theme.id, 'aux-03-about', '关于白皮书与架构');
    }

    await context.close();
  }

  await browser.close();

  // 保存测试审计结果
  const reportPath = path.resolve(__dirname, '../../../test-results/comprehensive-ui-audit-report.json');
  fs.writeFileSync(reportPath, JSON.stringify(auditResults, null, 2), 'utf8');

  // 同步覆盖至 brain artifacts screenshots 目录
  const brainDir = path.resolve('C:/Users/Qi/.gemini/antigravity/brain/8562f1e5-bb8c-458c-bea9-b8e615f4c58e/screenshots');
  try {
    fs.cpSync(SCREENSHOT_BASE, brainDir, { recursive: true, force: true });
    console.log(`✅ 已同步全量截图到 Artifacts 目录: ${brainDir}`);
  } catch (e) {
    console.warn(`⚠️ 同步 Artifacts 截图失败: ${e.message}`);
  }

  console.log('\n' + '='.repeat(70));
  console.log('🏁 全工作台 UI 审核与截图测试完成');
  console.log('='.repeat(70));
  console.log(`总截图生成: ${auditResults.screenshots.length} 张`);
  console.log(`页面检查项: ${auditResults.summary.totalChecks} 项`);
  console.log(`通过检查项: ${auditResults.summary.passedChecks} 项`);
  console.log(`发现UI瑕疵: ${auditResults.summary.issuesFound} 项`);
  console.log(`报告文件: ${reportPath}`);
  console.log('='.repeat(70));
})();
