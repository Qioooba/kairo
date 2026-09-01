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
      const tlsHidden = await page.evaluate(function () {
        const tls = document.getElementById('dbf-tls');
        const field = tls && tls.closest('.db-field');
        return !!(field && field.hidden);
      });
      if (!tlsHidden) throw new Error('Oracle 表单不应显示 TLS');
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
      const inspectTabs = await page.$$('.db-inspect-tab');
      if (inspectTabs.length < 4) throw new Error('缺少字段/索引/约束/DDL 页签');
      await inspectTabs[0].click();
      await page.waitForTimeout(200);
      await inspectTabs[1].click();
      await page.waitForTimeout(200);
      await inspectTabs[2].click();
      await page.waitForTimeout(200);
      await inspectTabs[3].click();
      await page.waitForTimeout(200);

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
      const explain = await page.$('#db-explain');
      if (explain) {
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
      const total = 5000;
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
            max.value = '5000';
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
          return body && body.querySelectorAll('tr[data-row]').length > 0 && meta && meta.textContent.indexOf('5000') >= 0;
        }, null, { timeout: 15000 });
        const top = await page.evaluate(function () {
          const body = document.getElementById('db-result-body');
          const rows = Array.from(body.querySelectorAll('tr[data-row]')).map(function (tr) { return Number(tr.dataset.row); });
          return { painted: rows.length, first: rows[0], last: rows[rows.length - 1] };
        });
        if (top.painted > 120) throw new Error('虚拟滚动渲染了过多行: ' + JSON.stringify(top));
        if (top.first !== 0) throw new Error('初始窗口应从头开始: ' + JSON.stringify(top));
        await page.fill('#db-result-filter', 'row-4999');
        await page.waitForFunction(function () {
          const meta = document.getElementById('db-result-meta');
          return meta && meta.textContent.indexOf('1/5000') >= 0;
        }, null, { timeout: 5000 });
        const filtered = await page.evaluate(function () {
          const rows = Array.from(document.querySelectorAll('#db-result-body tr[data-row]')).map(function (tr) { return tr.textContent; });
          return { painted: rows.length, text: rows.join('|') };
        });
        if (filtered.painted !== 1 || filtered.text.indexOf('row-4999') < 0) throw new Error('过滤未收敛到目标行: ' + JSON.stringify(filtered));
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
          return rows.length && Number(rows[rows.length - 1].dataset.row) >= 4900;
        }, null, { timeout: 5000 });
        const bottom = await page.evaluate(function () {
          const body = document.getElementById('db-result-body');
          const rows = Array.from(body.querySelectorAll('tr[data-row]')).map(function (tr) { return Number(tr.dataset.row); });
          return { painted: rows.length, first: rows[0], last: rows[rows.length - 1] };
        });
        if (bottom.painted > 120) throw new Error('滚动后仍渲染过多行: ' + JSON.stringify(bottom));
        if (bottom.last < 4900) throw new Error('滚动到底未露出末行: ' + JSON.stringify(bottom));
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
        ta.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true }));
      });
      const html = await page.$eval('#db-sql-highlight', function (el) { return el.innerHTML; });
      if (html.indexOf('db-sql-br') < 0) throw new Error('括号匹配高亮缺失');
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
  });
}

module.exports = { register };
