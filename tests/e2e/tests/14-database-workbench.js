'use strict';

/**
 * Database workbench: inspector tabs, explain, query history, grid/record.
 * Live Oracle clicks run when a source is already configured.
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('数据库工作台', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/home', { waitUntil: 'domcontentloaded' });
      // Keep every case independent from the workbench's intentional session
      // backup/restore feature. Otherwise accumulated tabs can hit the tab cap
      // and a later "new tab" assertion would unknowingly keep using the old tab.
      await page.evaluate(function () {
        localStorage.setItem('kairo_db_sessions_backup', JSON.stringify({
          activeId: 1,
          tabSeq: 1,
          sessions: [{ id: 1, sql: '', sourceId: '', page: 1, pageSize: 20 }]
        }));
      });
      await page.goto(baseUrl + '#/database', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
    });

    runner.it('页面提供数据源、测试连接和管理入口', async function () {
      const source = await page.$('#db-source, select');
      const testBtn = await page.$('button:has-text("测试连接"), #db-test');
      const manageBtn = await page.$('button:has-text("管理"), #db-manage');
      if (!source && !testBtn && !manageBtn) {
        throw new Error('数据库工作台未渲染数据源工具栏');
      }
      await runner.screenshot(page, '14-database-01-load');
    });

    runner.it('管理面板可展开 Oracle 表单', async function () {
      const manage = await page.$('#db-manage');
      if (!manage) return;
      await manage.click();
      await page.waitForSelector('#dbf-kind', { timeout: 4000 });
      const options = await page.$$eval('#dbf-kind option', els => els.map(e => e.value));
      if (!options.includes('oracle')) throw new Error('缺少 Oracle 类型');
      await page.waitForSelector('#dbf-environment', { timeout: 2000 });
      const advancedFields = await page.evaluate(function () {
        return ['dbf-environment', 'dbf-read-only', 'dbf-allow-ddl', 'dbf-tls-server-name', 'dbf-tls-ca-file', 'dbf-tls-client-cert', 'dbf-tls-client-key', 'dbf-ssh-enabled', 'dbf-ssh-host', 'dbf-ssh-port', 'dbf-ssh-user', 'dbf-ssh-remote-host', 'dbf-ssh-remote-port', 'dbf-ssh-host-key', 'dbf-ssh-profile', 'dbf-ssh-insecure'].every(function (id) { return !!document.getElementById(id); });
      });
      if (!advancedFields) throw new Error('环境、TLS 或 SSH 数据源字段未完整接入');
      const tlsVisible = await page.evaluate(function () {
        const tls = document.getElementById('dbf-tls');
        const field = tls && tls.closest('.db-field');
        return !!(field && !field.hidden);
      });
      if (!tlsVisible) throw new Error('Oracle 表单应显示 TLS 模式以支持 required/skip-verify');
      await runner.screenshot(page, '14-database-02-manager');
      const close = await page.$('#dbf-close');
      if (close) await close.click();
    });

    runner.it('Redis 单机隐藏 Cluster/Sentinel 字段，切换拓扑后显示', async function () {
      const manage = await page.$('#db-manage');
      if (!manage) return;
      if (!(await page.$('#dbf-kind'))) await manage.click();
      try {
        await page.waitForSelector('#dbf-kind', { timeout: 2500 });
      } catch (_) {
        await manage.click();
        await page.waitForSelector('#dbf-kind', { timeout: 4000 });
      }
      await page.selectOption('#dbf-kind', 'redis');
      await page.waitForTimeout(200);
      const vis = function (id) {
        return page.evaluate(function (elId) {
          const el = document.getElementById(elId);
          const field = el && el.closest('.db-field');
          if (!field) return false;
          const cs = getComputedStyle(field);
          return cs.display !== 'none' && cs.visibility !== 'hidden';
        }, id);
      };
      if (!(await page.$('#dbf-redis-mode'))) throw new Error('缺少 Redis 拓扑');
      if (await vis('dbf-redis-master')) throw new Error('单机不应显示 Master 名称');
      if (await vis('dbf-redis-nodes')) throw new Error('单机不应显示附加节点');
      await page.selectOption('#dbf-redis-mode', 'cluster');
      await page.waitForTimeout(120);
      if (!(await vis('dbf-redis-nodes'))) throw new Error('Cluster 应显示附加节点');
      if (await vis('dbf-redis-master')) throw new Error('Cluster 不应显示 Master 名称');
      await page.selectOption('#dbf-redis-mode', 'sentinel');
      await page.waitForTimeout(120);
      if (!(await vis('dbf-redis-master'))) throw new Error('Sentinel 应显示 Master 名称');
      if (!(await vis('dbf-redis-nodes'))) throw new Error('Sentinel 应显示附加节点');
    });

    runner.it('已配置数据源时可查询、查看对象和执行计划', async function () {
      const hasWorkspace = await page.$('#db-sql, .db-sql-editor');
      if (!hasWorkspace) {
        await runner.screenshot(page, '14-database-03-no-source');
        return;
      }
      const metaPane = await page.$('#db-meta-pane');
      if (metaPane && await metaPane.evaluate(function (el) { return el.classList.contains('is-collapsed'); })) {
        await page.click('#db-meta-collapsed-bar');
        await page.waitForTimeout(200);
      }
      if ((await page.$$('.db-inspect-tab')).length) throw new Error('左侧不应再重复展示字段/索引/约束/DDL');
      await page.waitForSelector('.db-tree-folder', { timeout: 5000 });
      const folders = await page.$$('.db-tree-folder');
      if (folders.length < 3) throw new Error('对象分类未使用文件夹外观');
      const firstGroup = page.locator('.db-tree-group > summary').first();
      if (await firstGroup.count()) {
        await firstGroup.click();
        await page.waitForTimeout(600);
        const firstObject = page.locator('.db-object').first();
        if (await firstObject.count()) {
          await firstObject.click();
          await page.waitForTimeout(500);
          if (!(await page.$('#db-sql-tabs .db-object-tab.active'))) throw new Error('单击对象没有在右侧打开详情页签');
          const queryTab = page.locator('#db-sql-tabs .db-editor-tab:not(.db-object-tab)').first();
          if (await queryTab.count()) { await queryTab.click(); await page.waitForTimeout(250); }
        }
      }

      const sql = await page.$('#db-sql');
      if (sql) {
        await sql.fill("SELECT '中文验证' AS text_value, CAST(NULL AS VARCHAR2(10)) AS empty_value FROM DUAL");
      }
      const run = await page.$('#db-run');
      if (run) {
        await run.click();
        await page.waitForTimeout(2500);
      }
      const record = await page.$('#db-view-record');
      if (record) {
        await record.click();
        await page.waitForTimeout(400);
      }
      const grid = await page.$('#db-view-grid');
      if (grid) await grid.click();
      const virt = await page.evaluate(async function () {
        const scroll = document.querySelector('.db-table-scroll');
        const body = document.getElementById('db-result-body');
        if (!scroll || !body) return null;
        const count = function () { return body.querySelectorAll('tr[data-row]').length; };
        const before = count();
        scroll.scrollTop = Math.max(scroll.scrollHeight, 20000);
        await new Promise(function (r) { requestAnimationFrame(function () { requestAnimationFrame(r); }); });
        return { before: before, after: count(), spacers: body.querySelectorAll('tr.db-spacer').length };
      });
      if (virt && (virt.before > 80 || virt.after > 80)) {
        throw new Error('网格应只渲染视口附近行，实际: ' + JSON.stringify(virt));
      }
      let explain = await page.$('#db-explain');
      if (explain) {
        if (!(await explain.isVisible())) {
          const moreSummary = await page.$('#db-toolbar-more summary');
          if (moreSummary) {
            await moreSummary.click();
            await page.waitForTimeout(300);
            explain = await page.$('#db-explain');
          }
        }
        await explain.click();
        await page.waitForTimeout(2000);
        const plan = await page.$('#db-view-plan');
        if (plan) await plan.click();
      }
      await runner.screenshot(page, '14-database-04-query-plan');
    });

    runner.it('工作台设置弹窗居中，快捷键可保存', async function () {
      const settings = await page.$('#db-settings');
      if (!settings) return;
      await settings.click();
      await page.waitForTimeout(400);
      const box = await page.evaluate(function () {
        const overlay = document.querySelector('.modal-overlay');
        const card = document.querySelector('.modal-card');
        if (!overlay || !card) return null;
        const o = overlay.getBoundingClientRect();
        const c = card.getBoundingClientRect();
        return { overlayTop: o.top, overlayLeft: o.left, cardTop: c.top, cardLeft: c.left, cardWidth: c.width, vw: innerWidth, vh: innerHeight };
      });
      if (!box) throw new Error('工作台设置没有使用居中弹层');
      if (box.overlayTop > 8 || box.overlayLeft > 8) throw new Error('设置遮罩没有铺满窗口: ' + JSON.stringify(box));
      if (box.cardTop < 24 || box.cardLeft < 24) throw new Error('设置弹窗仍在左上角: ' + JSON.stringify(box));
      await page.keyboard.press('Escape');
      await page.waitForTimeout(200);
    });

    runner.it('对象栏可拖动，结果区可自定义显示行数并复制字段名', async function () {
      if (!(await page.$('#db-sql'))) return;
      if (!(await page.$('#db-format'))) throw new Error('缺少 SQL 格式化');
      if (!(await page.$('#db-bookmark'))) throw new Error('缺少 SQL 收藏夹');
      if (!(await page.$('#db-export-format'))) throw new Error('缺少导出格式');
      if (!(await page.$('#db-tab-add'))) throw new Error('缺少查询页签');
      if (!(await page.$('#db-split-x'))) throw new Error('缺少左右拖动条');
      if (!(await page.$('#db-grid-rows'))) throw new Error('缺少显示行数');
      if (!(await page.$('#db-copy-columns'))) throw new Error('缺少复制字段名');
    });

    runner.it('结果网格虚拟滚动只渲染视口附近的行', async function () {
      if (!(await page.$('#db-sql'))) return;
      const total = 20000;
      await page.route('**/api/database/query', async function (route) {
        const columns = [{ name: 'ID', database_type: 'NUMBER' }, { name: 'NAME', database_type: 'VARCHAR2' }];
        const parts = [JSON.stringify({ type: 'meta', columns }) + '\n'];
        const batch = [];
        for (let i = 0; i < total; i++) {
          batch.push([i + 1, 'row-' + (i + 1)]);
          if (batch.length === 200) {
            parts.push(JSON.stringify({ type: 'rows', rows: batch.splice(0, batch.length) }) + '\n');
          }
        }
        if (batch.length) parts.push(JSON.stringify({ type: 'rows', rows: batch }) + '\n');
        parts.push(JSON.stringify({ type: 'summary', summary: { rows: total, elapsed_ms: 1, truncated: false, bytes: 0, query_limit: total } }) + '\n');
        await route.fulfill({ status: 200, contentType: 'application/x-ndjson; charset=utf-8', body: parts.join('') });
      });
      try {
        const gridTab = await page.$('#db-view-grid');
        if (gridTab) await gridTab.click();
        await page.evaluate(function (sql) {
          const ta = document.getElementById('db-sql');
          const max = document.getElementById('db-max-rows');
          if (max) {
            max.value = '20000';
            max.dispatchEvent(new Event('change', { bubbles: true }));
          }
          if (ta) {
            ta.value = sql;
            if (ta._syncHighlight) ta._syncHighlight();
            ta.dispatchEvent(new Event('input', { bubbles: true }));
          }
        }, 'SELECT id, name FROM virtual_scroll_probe');
        await page.click('#db-run');
        await page.waitForFunction(function () {
          const body = document.getElementById('db-result-body');
          const meta = document.getElementById('db-result-meta');
          return body && body.querySelectorAll('tr[data-row]').length > 0 && meta && meta.textContent.indexOf('20000') >= 0;
        }, null, { timeout: 15000 });
        const top = await page.evaluate(function () {
          const body = document.getElementById('db-result-body');
          const rows = Array.from(body.querySelectorAll('tr[data-row]')).map(function (tr) { return Number(tr.dataset.row); });
          return { painted: rows.length, first: rows[0], last: rows[rows.length - 1] };
        });
        if (top.painted > 120) throw new Error('虚拟滚动渲染了过多行: ' + JSON.stringify(top));
        if (top.first !== 0) throw new Error('初始窗口应从头开始: ' + JSON.stringify(top));
        await page.fill('#db-result-filter', 'row-19999');
        await page.waitForFunction(function () {
          const meta = document.getElementById('db-result-meta');
          return meta && meta.textContent.indexOf('1/20000') >= 0;
        }, null, { timeout: 5000 });
        const filtered = await page.evaluate(function () {
          const rows = Array.from(document.querySelectorAll('#db-result-body tr[data-row]')).map(function (tr) { return tr.textContent; });
          return { painted: rows.length, text: rows.join('|') };
        });
        if (filtered.painted !== 1 || filtered.text.indexOf('row-19999') < 0) throw new Error('过滤未收敛到目标行: ' + JSON.stringify(filtered));
        await page.fill('#db-result-filter', '');
        await page.waitForFunction(function () {
          return document.querySelectorAll('#db-result-body tr[data-row]').length > 10;
        }, null, { timeout: 5000 });
        await page.click('#db-result-grid [data-sort="1"]');
        await page.waitForFunction(function () {
          const btn = document.querySelector('#db-result-grid [data-sort="1"]');
          return btn && btn.textContent.indexOf('▲') >= 0;
        }, null, { timeout: 5000 });
        await page.fill('#db-grid-rows', '8');
        await page.evaluate(function () {
          const input = document.getElementById('db-grid-rows');
          input.dispatchEvent(new Event('change', { bubbles: true }));
        });
        const shortGrid = await page.evaluate(function () {
          return document.querySelectorAll('#db-result-body tr[data-row]').length;
        });
        await page.fill('#db-grid-rows', '30');
        await page.evaluate(function () {
          const input = document.getElementById('db-grid-rows');
          input.dispatchEvent(new Event('change', { bubbles: true }));
        });
        const tallGrid = await page.evaluate(function () {
          return document.querySelectorAll('#db-result-body tr[data-row]').length;
        });
        if (!(tallGrid > shortGrid)) throw new Error('显示行数变化后窗口未跟着变: short=' + shortGrid + ' tall=' + tallGrid);
        const clicked = await page.evaluate(function () {
          const tr = document.querySelector('#db-result-body tr[data-row]');
          return tr ? Number(tr.dataset.row) : null;
        });
        await page.click('#db-result-body tr[data-row] td.num');
        const selected = await page.$eval('#db-result-body tr.selected', function (tr) { return Number(tr.dataset.row); });
        if (selected !== clicked) throw new Error('单击未选中当前行');
        await page.dblclick('#db-result-body td[data-col]');
        await page.waitForSelector('.db-record-table, #db-record-next', { timeout: 5000 });
        await page.click('#db-view-grid');
        await page.waitForSelector('#db-result-body tr[data-row]');
        const menu = await page.evaluate(function () {
          const td = document.querySelector('#db-result-body td[data-col]');
          if (!td) return false;
          td.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 90, clientY: 140 }));
          return !!document.querySelector('.db-popup-menu');
        });
        if (!menu) throw new Error('右键菜单未打开');
        await page.evaluate(function () {
          const scroll = document.querySelector('#db-result-grid .db-table-scroll');
          scroll.scrollTop = scroll.scrollHeight;
        });
        await page.waitForFunction(function () {
          const rows = document.querySelectorAll('#db-result-body tr[data-row]');
          return rows.length && Number(rows[rows.length - 1].dataset.row) >= 19900;
        }, null, { timeout: 5000 });
        const bottom = await page.evaluate(function () {
          const body = document.getElementById('db-result-body');
          const rows = Array.from(body.querySelectorAll('tr[data-row]')).map(function (tr) { return Number(tr.dataset.row); });
          return { painted: rows.length, first: rows[0], last: rows[rows.length - 1] };
        });
        if (bottom.painted > 120) throw new Error('滚动后仍渲染过多行: ' + JSON.stringify(bottom));
        if (bottom.last < 19900) throw new Error('滚动到底未露出末行: ' + JSON.stringify(bottom));
        await runner.screenshot(page, '14-database-05-virtual-scroll');
      } finally {
        await page.unroute('**/api/database/query');
      }
    });

    runner.it('查询页签、关键字补全和括号匹配', async function () {
      if (!(await page.$('#db-sql'))) return;
      if (!(await page.$('#db-tab-add'))) throw new Error('缺少新建页签');
      await page.click('#db-tab-add');
      await page.waitForTimeout(200);
      const tabs = await page.$$('#db-sql-tabs .db-editor-tab');
      if (tabs.length < 2) throw new Error('新建页签失败: ' + tabs.length);
      const activeSession = await page.evaluate(function () { return window.Kairo.database.getActiveSession(); });
      if (!activeSession || !activeSession.sessionId || !activeSession.sourceId) throw new Error('增强功能未取得当前页签 session_id/source_id');
      await page.fill('#db-sql', 'se');
      await page.waitForTimeout(250);
      const open = await page.$('#db-sql-ac:not([hidden])');
      if (!open) throw new Error('输入 se 后应出现补全');
      const labels = await page.$$eval('#db-sql-ac .db-sql-ac-item span', function (els) { return els.map(function (e) { return e.textContent; }); });
      if (!labels.some(function (x) { return /SELECT/i.test(x); })) throw new Error('补全缺少 SELECT: ' + labels.join(','));
      await page.keyboard.press('Enter');
      await page.waitForTimeout(120);
      const accepted = await page.$eval('#db-sql', function (el) { return el.value; });
      if (accepted.trim() !== 'SELECT') throw new Error('回车未接受 SELECT: ' + accepted);
      await page.fill('#db-sql', 'SELECT * FROM t WHERE (id = 1)');
      await page.evaluate(function () {
        const ta = document.getElementById('db-sql');
        const pos = ta.value.indexOf('(');
        ta.focus();
        ta.setSelectionRange(pos, pos);
        if (typeof ta._syncHighlight !== 'function') throw new Error('SQL 高亮同步器未绑定');
        ta._syncHighlight();
      });
      await page.waitForFunction(function () {
        return !!document.querySelector('#db-sql-highlight .db-sql-br');
      }, null, { timeout: 1500 });
      await runner.screenshot(page, '14-database-06-tabs-complete');
    });

    runner.it('两个页签可同时发起查询', async function () {
      if (!(await page.$('#db-sql'))) return;
      let inflight = 0, maxInflight = 0;
      await page.route('**/api/database/query', async function (route) {
        inflight++;
        maxInflight = Math.max(maxInflight, inflight);
        await new Promise(function (r) { setTimeout(r, 700); });
        inflight--;
        const body = JSON.stringify({ type: 'meta', columns: [{ name: 'N' }] }) + '\n'
          + JSON.stringify({ type: 'rows', rows: [[1]] }) + '\n'
          + JSON.stringify({ type: 'summary', summary: { rows: 1, elapsed_ms: 1, truncated: false, bytes: 1, query_limit: 10 } }) + '\n';
        await route.fulfill({ status: 200, contentType: 'application/x-ndjson; charset=utf-8', body: body });
      });
      try {
        await page.fill('#db-sql', 'SELECT 1 FROM DUAL');
        await page.click('#db-run');
        await page.click('#db-tab-add');
        await page.fill('#db-sql', 'SELECT 2 FROM DUAL');
        await page.click('#db-run');
        await page.waitForTimeout(1100);
        if (maxInflight < 2) throw new Error('两个页签没有并行发出查询, max=' + maxInflight);
      } finally {
        await page.unroute('**/api/database/query');
      }
    });

    runner.it('未提交网格修改按页签隔离，切回后仍可放弃', async function () {
      if (!(await page.$('#db-sql'))) return;
      await page.route('**/api/database/query', async function (route) {
        const body = JSON.stringify({ type: 'meta', columns: [{ name: 'ID', database_type: 'NUMBER' }, { name: 'NAME', database_type: 'VARCHAR2' }] }) + '\n'
          + JSON.stringify({ type: 'rows', rows: [[1, 'before']] }) + '\n'
          + JSON.stringify({ type: 'summary', summary: { rows: 1, elapsed_ms: 1, truncated: false, bytes: 8, query_limit: 20 } }) + '\n';
        await route.fulfill({ status: 200, contentType: 'application/x-ndjson; charset=utf-8', body: body });
      });
      try {
        await page.fill('#db-sql', 'SELECT ID, NAME FROM tab_isolation_probe');
        await page.click('#db-run');
        await page.waitForSelector('#db-result-body td[data-col="1"]', { timeout: 5000 });
        const originalTab = await page.$eval('#db-sql-tabs .db-editor-tab.active', function (el) { return el.dataset.tab; });
        const editPressed = await page.$eval('#db-toggle-edit', function (el) { return el.getAttribute('aria-pressed') === 'true'; });
        if (!editPressed) await page.click('#db-toggle-edit');
        await page.dblclick('#db-result-body td[data-col="1"]');
        await page.fill('#db-result-body td[data-col="1"] input', 'changed');
        await page.keyboard.press('Enter');
        const pendingBefore = await page.$eval('#db-btn-commit', function (el) { return el.textContent.trim(); });
        if (pendingBefore.indexOf('网格 1') < 0) throw new Error('原页签没有记录待提交修改: ' + pendingBefore);

        await page.click('#db-tab-add');
        const newTab = await page.$eval('#db-sql-tabs .db-editor-tab.active', function (el) { return el.dataset.tab; });
        const isolated = await page.$eval('#db-btn-commit', function (el) { return { disabled: el.disabled, text: el.textContent.trim() }; });
        if (!isolated.disabled || isolated.text.indexOf('网格 1') >= 0) throw new Error('新页签泄漏了旧页签的网格修改: ' + JSON.stringify(isolated));

        await page.click('#db-sql-tabs .db-editor-tab[data-tab="' + originalTab + '"]');
        const restored = await page.evaluate(function () {
          return {
            pending: document.getElementById('db-btn-commit').textContent.trim(),
            cell: (document.querySelector('#db-result-body td[data-col="1"]') || {}).textContent || '',
            edit: document.getElementById('db-toggle-edit').getAttribute('aria-pressed')
          };
        });
        if (restored.pending.indexOf('网格 1') < 0 || restored.cell.indexOf('changed') < 0 || restored.edit !== 'true') {
          throw new Error('切回原页签后未完整恢复网格编辑状态: ' + JSON.stringify(restored));
        }
        await page.click('#db-btn-rollback');
        const rolledBack = await page.$eval('#db-result-body td[data-col="1"]', function (el) { return el.textContent.trim(); });
        if (rolledBack !== 'before') throw new Error('放弃修改后未恢复原值: ' + rolledBack);
        await page.evaluate(function (id) {
          const close = document.querySelector('#db-sql-tabs [data-close="' + id + '"]');
          if (close) close.click();
        }, newTab);
      } finally {
        await page.unroute('**/api/database/query');
      }
    });

    runner.it('V2 权限、事务提示、消息语义、对象栏快捷键和输入法保护一致', async function () {
      if (!(await page.$('#db-sql'))) return;
      const status = await page.$eval('.db-safe', function (el) { return { text: el.textContent, write: el.classList.contains('is-write') }; });
      if (status.write && (status.text.indexOf('DML 页签事务') < 0 || status.text.indexOf('手动提交') < 0)) {
        throw new Error('可写会话没有明确事务语义: ' + status.text);
      }
      if (status.write && status.text.indexOf('只读会话') >= 0) throw new Error('V2 可写会话仍显示只读文案');
      const editLabel = await page.$eval('#db-toggle-edit', function (el) { return el.textContent.trim(); });
      if (editLabel.indexOf('网格编辑') < 0) throw new Error('编辑开关没有限定为网格编辑: ' + editLabel);
      const restored = await page.evaluate(function () {
        const sql = (document.getElementById('db-sql').value || '').replace(/\s+/g, ' ').trim();
        const title = (document.querySelector('#db-sql-tabs .db-editor-tab.active .db-tab-name') || {}).textContent || '';
        return { sql: sql, title: title };
      });
      if (restored.sql && restored.title.indexOf(restored.sql.slice(0, 18)) < 0) {
        throw new Error('恢复后的页签标题与 SQL 正文不同步: ' + JSON.stringify(restored));
      }
      if (!restored.sql && restored.title.indexOf('查询 ') < 0) {
        throw new Error('恢复后的页签标题显示旧 SQL，但编辑器为空: ' + JSON.stringify(restored));
      }

      const collapsedBefore = await page.$eval('#db-meta-pane', function (el) { return el.classList.contains('is-collapsed'); });
      await page.keyboard.press('Alt+O');
      await page.waitForTimeout(180);
      const collapsedAfter = await page.$eval('#db-meta-pane', function (el) { return el.classList.contains('is-collapsed'); });
      if (collapsedAfter === collapsedBefore) throw new Error('Alt+O 未切换对象栏');
      await page.keyboard.press('Alt+O');

      let queryCalls = 0;
      let transactionCalls = 0;
      await page.route('**/api/database/query', async function (route) {
        queryCalls++;
        const body = JSON.stringify({ type: 'mutation', message: '已执行 UPDATE，影响 1 行，等待提交', summary: { rows: 1, rows_affected: 1, elapsed_ms: 2, statement_type: 'DML', transaction_pending: true } }) + '\n';
        await route.fulfill({ status: 200, contentType: 'application/x-ndjson; charset=utf-8', body: body });
      });
      await page.route('**/api/database/transaction', async function (route) {
        transactionCalls++;
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true, summary: { statement_type: 'TRANSACTION', message: '事务已提交' } }) });
      });
      try {
        await page.fill('#db-sql', 'UPDATE audit_table SET name = \'ok\' WHERE id = 1');
        await page.click('#db-run');
        await page.waitForSelector('#db-result-message.ok:not([hidden])', { timeout: 5000 });
        const message = await page.$eval('#db-result-message', function (el) {
          return { icon: el.querySelector('.db-message-icon').textContent.trim(), role: el.getAttribute('role'), color: getComputedStyle(el).color };
        });
        if (message.icon !== '✓' || message.role !== 'status') throw new Error('成功消息语义错误: ' + JSON.stringify(message));

        await page.fill('#db-sql', 'COMMIT');
        await page.click('#db-run');
        await page.waitForTimeout(150);
        if (queryCalls !== 1 || transactionCalls !== 1) throw new Error('COMMIT 应提交当前页签事务: query=' + queryCalls + ' transaction=' + transactionCalls);

        await page.fill('#db-sql', 'SELECT 1 FROM DUAL');
        await page.evaluate(function () {
          const el = document.getElementById('db-sql');
          const ev = new KeyboardEvent('keydown', { key: 'E', code: 'KeyE', ctrlKey: true, shiftKey: true, bubbles: true, cancelable: true, isComposing: true });
          el.dispatchEvent(ev);
        });
        await page.waitForTimeout(120);
        const imeSQL = await page.$eval('#db-sql', function (el) { return el.value; });
        if (imeSQL !== 'SELECT 1 FROM DUAL' || queryCalls !== 1) throw new Error('输入法组合期间不应污染 SQL 或触发执行');

        let dangerDialog = '';
        const rememberDialog = function (dialog) { dangerDialog = dialog.message(); };
        page.on('dialog', rememberDialog);
        await page.fill('#db-sql', 'DELETE FROM audit_table');
        await page.click('#db-run');
        await page.waitForTimeout(180);
        page.off('dialog', rememberDialog);
        if (dangerDialog.indexOf('没有 WHERE') < 0 || dangerDialog.indexOf('手动提交') < 0) {
          throw new Error('无 WHERE DELETE 未显示完整危险确认: ' + dangerDialog);
        }
        if (queryCalls !== 2) throw new Error('确认危险 SQL 后请求次数异常: ' + queryCalls);
      } finally {
        await page.unroute('**/api/database/query');
        await page.unroute('**/api/database/transaction');
      }
      await runner.screenshot(page, '14-database-07-v2-semantics');
    });

    runner.it('左右分栏布局：切换、拖动、刷新记忆与窄屏自动降级', async function (testCtx) {
      // 前置条件必须显式失败：早期写法用 `if (!(await page.$('#db-sql'))) return;`，
      // 结果是"工作台没渲染"时整条用例被判为 PASS（runner 里 return 等于通过），
      // 恰好把本用例要证明的东西全跳过。helpers.requireElement 就是为此提供的。
      const helpers = (testCtx && testCtx.helpers) || (ctx && ctx.helpers);
      if (helpers && typeof helpers.requireElement === 'function') {
        await helpers.requireElement(page, '#db-sql', '左右分栏用例需要已配置数据源的数据库工作台（#db-sql 未渲染）');
      } else if (!(await page.$('#db-sql'))) {
        throw new Error('左右分栏用例需要已配置数据源的数据库工作台（#db-sql 未渲染）');
      }
      if (!(await page.$('#db-layout-toggle'))) throw new Error('缺少布局切换按钮 #db-layout-toggle');
      if (!(await page.$('#db-split-panes'))) throw new Error('缺少左右分栏分隔条 #db-split-panes');
      // 本用例会改视口，必须还原：runner 用同一个 page 跑全部用例，且截图/trace/
      // run-metadata 都按启动视口（1366x900）命名，不还原会让后续用例的产物标签失真。
      const originalViewport = page.viewportSize();

      const readState = function () {
        return page.evaluate(function () {
          const main = document.getElementById('db-main');
          const card = document.querySelector('.db-editor-card').getBoundingClientRect();
          const res = document.getElementById('db-results-section').getBoundingClientRect();
          const split = document.getElementById('db-split-panes');
          const btn = document.getElementById('db-layout-toggle');
          const rows = document.getElementById('db-grid-rows');
          return {
            columns: main.classList.contains('is-columns'),
            narrow: main.classList.contains('is-narrow-editor'),
            splitDisplay: getComputedStyle(split).display,
            cardW: Math.round(card.width),
            resultsW: Math.round(res.width),
            sideBySide: card.right <= res.left + 1,
            stacked: card.bottom <= res.top + 1,
            rowsDisabled: !!(rows && rows.disabled),
            label: (document.getElementById('db-layout-label') || {}).textContent || '',
            pressed: btn.getAttribute('aria-pressed'),
            btnDisabled: !!btn.disabled,
            btnTitle: btn.title,
            pageScroll: document.documentElement.scrollHeight <= window.innerHeight + 8
          };
        });
      };

      try {
        // 对象树收起，保证左右分栏有足够可用宽度（可重复执行）
        await page.setViewportSize({ width: 1600, height: 1000 });
        await page.waitForTimeout(200);
        const metaCollapsed = await page.evaluate(function () {
          const m = document.getElementById('db-meta-pane');
          return !!(m && m.classList.contains('is-collapsed'));
        });
        if (!metaCollapsed) {
          const metaToggle = await page.$('#db-meta-toggle');
          if (metaToggle) { await metaToggle.click(); await page.waitForTimeout(350); }
        }

        let st = await readState();
        if (st.columns) { await page.click('#db-layout-toggle'); await page.waitForTimeout(450); st = await readState(); }
        if (st.columns || st.splitDisplay !== 'none' || !st.stacked) {
          throw new Error('上下布局基线异常: ' + JSON.stringify(st));
        }
        if (st.label !== '左右布局' || st.pressed !== 'false' || st.btnDisabled) {
          throw new Error('上下布局下按钮语义错误: ' + JSON.stringify(st));
        }
        if (st.rowsDisabled) throw new Error('上下布局下“显示行数”不应被禁用');

        // 切到左右布局
        await page.click('#db-layout-toggle');
        await page.waitForTimeout(500);
        st = await readState();
        if (!st.columns || st.splitDisplay === 'none' || !st.sideBySide) {
          throw new Error('未进入左右分栏: ' + JSON.stringify(st));
        }
        if (st.label !== '上下布局' || st.pressed !== 'true') {
          throw new Error('左右布局下按钮语义错误（文案应指向目标布局）: ' + JSON.stringify(st));
        }
        if (!st.rowsDisabled) throw new Error('左右布局下“显示行数”应禁用，避免死控件');
        if (!st.pageScroll) throw new Error('左右布局应让工作区占满视口，页面不滚动');
        if (!st.narrow) throw new Error('窄左列应切换编辑器工具栏紧凑排布');

        const noSpill = await page.evaluate(function () {
          const bar = document.querySelector('.db-editor-bar');
          const box = bar.getBoundingClientRect();
          return Array.prototype.every.call(bar.querySelectorAll('button, input, select, summary'), function (el) {
            const b = el.getBoundingClientRect();
            return !(b.width > 0 && b.height > 0 && (b.right > box.right + 1 || b.left < box.left - 1));
          });
        });
        if (!noSpill) throw new Error('左右布局下编辑器工具栏存在溢出控件');

        // 双击复位默认比例后再拖动，避免上次遗留的极限宽度影响断言
        await page.locator('#db-split-panes').dblclick();
        await page.waitForTimeout(350);
        st = await readState();
        const before = st.cardW;

        const box = await page.locator('#db-split-panes').boundingBox();
        if (!box) throw new Error('分隔条不可见，无法拖拽');
        await page.mouse.move(box.x + box.width / 2, box.y + 200);
        await page.mouse.down();
        await page.mouse.move(box.x + box.width / 2 + 120, box.y + 200, { steps: 8 });
        const dragging = await page.evaluate(function () {
          return {
            resizing: document.body.classList.contains('db-resizing'),
            hintVisible: document.getElementById('db-split-panes-hint').hidden === false
          };
        });
        await page.mouse.up();
        await page.waitForTimeout(450);
        if (!dragging.resizing || !dragging.hintVisible) {
          throw new Error('拖动期间应进入 resizing 状态并显示宽度提示: ' + JSON.stringify(dragging));
        }
        st = await readState();
        if (st.cardW - before < 100) throw new Error('拖动分隔条未改变左列宽度: ' + before + ' -> ' + st.cardW);
        if (!st.sideBySide || st.resultsW < 419) throw new Error('拖动后左右列关系/最小宽度异常: ' + JSON.stringify(st));
        const draggedWidth = st.cardW;
        await runner.screenshot(page, '14-database-08-split-columns');

        // 刷新后记忆布局与列宽
        await page.reload({ waitUntil: 'domcontentloaded' });
        await page.waitForSelector('#db-sql', { timeout: 8000 });
        await page.waitForTimeout(600);
        st = await readState();
        if (!st.columns || Math.abs(st.cardW - draggedWidth) > 8) {
          throw new Error('刷新后未记住布局/列宽: 期望宽 ' + draggedWidth + '，实际 ' + JSON.stringify(st));
        }

        // 窄屏自动降级为上下布局，且按钮置灰说明原因
        await page.setViewportSize({ width: 1024, height: 900 });
        await page.waitForTimeout(700);
        st = await readState();
        if (st.columns || st.splitDisplay !== 'none' || !st.stacked) {
          throw new Error('窄屏应自动降级为上下布局: ' + JSON.stringify(st));
        }
        if (!st.btnDisabled || st.btnTitle.indexOf('过窄') < 0) {
          throw new Error('窄屏下布局按钮应置灰并说明原因: ' + JSON.stringify(st));
        }
        if (st.rowsDisabled) throw new Error('窄屏降级后“显示行数”应恢复可用');

        // 恢复宽屏后回到用户选择的左右布局
        await page.setViewportSize({ width: 1600, height: 1000 });
        await page.waitForTimeout(800);
        st = await readState();
        if (!st.columns || !st.sideBySide) throw new Error('恢复宽屏后应回到左右布局: ' + JSON.stringify(st));
      } finally {
        // 清理 1：把布局偏好复位为上下布局，避免影响其他用例（例如 43 号编辑器高度同步）。
        try {
          const stillColumns = await page.evaluate(function () {
            const m = document.getElementById('db-main');
            return !!(m && m.classList.contains('is-columns'));
          });
          if (stillColumns && (await page.$('#db-layout-toggle'))) {
            await page.click('#db-layout-toggle');
            await page.waitForTimeout(400);
          }
        } catch (_) { /* 清理失败不影响断言结果 */ }
        // 清理 2：还原启动视口，避免后续用例与产物标签（截图/trace/run-metadata）失真。
        try {
          if (originalViewport) await page.setViewportSize(originalViewport);
        } catch (_) { /* 同上 */ }
      }
    });
  });
}

module.exports = { register };
