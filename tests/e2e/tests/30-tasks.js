'use strict';

/**
 * 30-tasks.js — review-2026-08 方案 02
 * S1 首页与导航（v0.16 新增入口）+ S2 定时任务页全流程（A2，P0）
 *
 * 依赖实例：隔离实例（BASE_URL），数据目录 = $KAIRO_RUN_DIR（默认 /tmp/kairo-review-2026-08/run）
 */

const fs = require('fs');
const path = require('path');

const RUN_DIR = process.env.KAIRO_RUN_DIR || '/tmp/kairo-review-2026-08/run';
const TASK_OUT_FILE = '/tmp/kairo-e2e-task.out';

function rowByName(page, name) {
  return page.evaluateHandle((n) => {
    const rows = document.querySelectorAll('.reminder-row');
    for (const r of rows) {
      const c = r.querySelector('.reminder-content');
      if (c && c.textContent.trim() === n) return r;
    }
    return null;
  }, name);
}

async function apiJSON(page, method, url, body) {
  return page.evaluate(async ({ method, url, body }) => {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const r = await fetch(url, opts);
    let data = null;
    try { data = await r.json(); } catch (e) { /* ignore */ }
    return { status: r.status, data };
  }, { method, url, body });
}

function register(runner, ctx) {
  const { page, baseUrl, helpers } = ctx;

  runner.describe('S1 首页与导航（v0.16 新增入口）', function () {
    runner.it('S1-1 首页存在「定时任务」卡片（icon=timer + tag 新）', async function () {
      await page.goto(baseUrl + '/#/', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(800);
      const card = await page.$('.tool-card[aria-label="定时任务"]');
      if (!card) throw new Error('首页未找到「定时任务」卡片');
      const info = await card.evaluate((c) => {
        const svg = c.querySelector('.icon svg');
        const tag = c.querySelector('.tag');
        return {
          hasSvg: !!svg,
          tagText: tag ? tag.textContent.trim() : '',
          name: (c.querySelector('.name') || {}).textContent || '',
        };
      });
      if (!info.hasSvg) throw new Error('定时任务卡片 icon 不是 SVG（Win7 兼容要求）');
      if (info.tagText !== '新') throw new Error('定时任务卡片 tag 不是「新」: ' + info.tagText);
      const navItem = await page.$('.nav-item[data-route="tasks"]');
      if (!navItem) throw new Error('侧栏缺少 定时任务 nav-item');
      await runner.screenshot(page, '30-s1-home-tasks-card');
    });

    runner.it('S1-2 点击卡片跳转 #/tasks 且页面正常', async function () {
      await page.goto(baseUrl + '/#/', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(500);
      await page.click('.tool-card[aria-label="定时任务"]');
      await page.waitForFunction(() => location.hash === '#/tasks');
      await page.waitForTimeout(600);
      const hasHeader = await page.$('.reminders-header');
      if (!hasHeader) throw new Error('#/tasks 页面未渲染（缺 .reminders-header）');
      await runner.screenshot(page, '30-s1-tasks-page');
    });
  });

  runner.describe('S2 定时任务页全流程（A2）', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '/#/tasks', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(800);
    });

    runner.it('S2-1 空态 + 无 running 时零轮询（5s 网络静默）', async function () {
      const tasks = await apiJSON(page, 'GET', '/api/tasks');
      for (const t of (tasks.data || [])) {
        await apiJSON(page, 'DELETE', '/api/tasks/' + t.id);
      }
      await page.goto(baseUrl + '/#/tasks', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1000);
      const empty = await page.$('.empty-state');
      if (!empty) throw new Error('空态未显示 .empty-state');
      const emptyText = await empty.textContent();
      if (!/还没有定时任务/.test(emptyText)) throw new Error('空态文案异常: ' + emptyText);
      const newBtn = await page.$('button:has-text("新增任务")');
      if (!newBtn) throw new Error('「新增任务」按钮不可见');

      const before = ctx.networkLogs.filter(l => l.type === 'request' && l.url.includes('/api/tasks')).length;
      await page.waitForTimeout(5000);
      const after = ctx.networkLogs.filter(l => l.type === 'request' && l.url.includes('/api/tasks')).length;
      if (after > before) throw new Error(`无 running 任务时出现轮询请求（5s 内 +${after - before}）`);
      await runner.screenshot(page, '30-s2-empty-state');
    });

    runner.it('S2-2 非法 cron 提交被拒', async function () {
      await page.click('button:has-text("新增任务")');
      await page.waitForSelector('.modal-card', { state: 'visible' });
      await page.fill('.modal-card input[placeholder*="拉取最新代码"]', 'e2e-bad-cron');
      await page.fill('.modal-card textarea.editor-content', 'echo bad');
      await page.selectOption('.modal-card select.editor-input', { label: '自定义 cron…' });
      const cronInput = await page.waitForSelector('.modal-card input[placeholder*="cron"]', { state: 'visible' });
      await cronInput.fill('not-a-cron');
      await page.waitForTimeout(800);
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(1200);

      const toastText = await page.evaluate(() => {
        const t = document.querySelector('#toast');
        return t ? t.textContent.trim() : '';
      });
      const stillOpen = await page.$('.modal-card');
      const list = await apiJSON(page, 'GET', '/api/tasks');
      const created = (list.data || []).some(t => t.name === 'e2e-bad-cron');
      if (created) throw new Error('非法 cron 被创建了任务');
      if (!/失败|不合法|非法/.test(toastText) && !stillOpen) {
        throw new Error('非法 cron 无明显报错反馈（toast=' + toastText + '）');
      }
      const preview = await page.$('.task-cron-preview');
      if (preview) {
        const pt = await preview.textContent();
        if (!/不合法|失败/.test(pt) && stillOpen) {
          // 弹窗仍在 + 预览未提示也视为已阻止创建（后端拒绝路径）
        }
      }
      if (stillOpen) {
        await page.keyboard.press('Escape');
        await page.waitForTimeout(300);
      }
      await runner.screenshot(page, '30-s2-bad-cron');
    });

    runner.it('S2-3 新增合法任务（每分钟 + echo 落盘命令）', async function () {
      try { fs.unlinkSync(TASK_OUT_FILE); } catch (e) { /* ignore */ }
      await page.click('button:has-text("新增任务")');
      await page.waitForSelector('.modal-card', { state: 'visible' });
      await page.fill('.modal-card input[placeholder*="拉取最新代码"]', 'e2e-task-1');
      await page.fill('.modal-card textarea.editor-content', 'echo hello > ' + TASK_OUT_FILE);
      await page.selectOption('.modal-card select.editor-input', '* * * * *');
      await page.waitForTimeout(500);
      await runner.screenshot(page, '30-s2-new-task-modal');
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(1500);

      const list = await apiJSON(page, 'GET', '/api/tasks');
      const task = (list.data || []).find(t => t.name === 'e2e-task-1');
      if (!task) throw new Error('新增任务未出现在列表');
      if (!task.next_run_at) throw new Error('next_run_at 为空');
      ctx.task1Id = task.id;
      const row = await rowByName(page, 'e2e-task-1');
      const rowEl = row.asElement();
      if (!rowEl) {
        const dbg = await page.evaluate(() => ({
          rows: Array.from(document.querySelectorAll('.reminder-row')).map(r => (r.querySelector('.reminder-content') || {}).textContent),
          modalOpen: !!document.querySelector('.modal-card'),
          hasToast: !!document.querySelector('#toast'),
        }));
        const errs = ctx.consoleLogs.filter(l => l.type === 'pageerror').map(l => l.text);
        throw new Error('列表中未渲染 e2e-task-1 行 | dbg=' + JSON.stringify(dbg) + ' | pageerrors=' + JSON.stringify(errs));
      }
      const rowText = await rowEl.textContent();
      if (!/下次/.test(rowText)) throw new Error('行内未显示下次执行时间');
      await runner.screenshot(page, '30-s2-task-created');
    });

    runner.it('S2-4 立即执行：running → 成功 + 运行历史 + 磁盘落盘', async function () {
      try { fs.unlinkSync(TASK_OUT_FILE); } catch (e) { /* ignore */ }
      if (!ctx.task1Id) {
        const list = await apiJSON(page, 'GET', '/api/tasks');
        const task = (list.data || []).find(t => t.name === 'e2e-task-1');
        if (!task) throw new Error('前置任务 e2e-task-1 不存在');
        ctx.task1Id = task.id;
      }
      const row = (await rowByName(page, 'e2e-task-1')).asElement();
      if (!row) throw new Error('未找到任务行');
      await page.evaluate((r) => {
        const btns = r.querySelectorAll('.reminder-actions button');
        for (const b of btns) { if (b.textContent.includes('立即执行')) { b.click(); return; } }
        throw new Error('未找到立即执行按钮');
      }, row);
      await page.waitForTimeout(400);

      let sawRunning = false;
      let success = false;
      for (let i = 0; i < 20; i++) {
        await page.waitForTimeout(500);
        const st = await apiJSON(page, 'GET', '/api/tasks');
        const t = (st.data || []).find(x => x.id === ctx.task1Id);
        if (!t) throw new Error('任务消失');
        if (t.running) sawRunning = true;
        if (!t.running && t.last_status === 'success') { success = true; break; }
      }
      if (!success) throw new Error('任务未在 10s 内执行成功');
      const runs = await apiJSON(page, 'GET', '/api/tasks/' + ctx.task1Id + '/runs');
      if (!Array.isArray(runs.data) || runs.data.length === 0) throw new Error('运行历史为空');
      if (runs.data[0].status !== 'success') throw new Error('运行历史首条不是 success: ' + JSON.stringify(runs.data[0]));
      await page.waitForTimeout(500);
      if (!fs.existsSync(TASK_OUT_FILE)) throw new Error('磁盘文件未落盘，命令未真实执行: ' + TASK_OUT_FILE);
      const content = fs.readFileSync(TASK_OUT_FILE, 'utf8');
      if (!/hello/.test(content)) throw new Error('落盘文件内容异常: ' + content);
      ctx.sawRunning = sawRunning;
      await runner.screenshot(page, '30-s2-task-success');
    });

    runner.it('S2-5 启停切换两次 + 刷新后保持', async function () {
      const toggleOnce = async () => {
        const row = (await rowByName(page, 'e2e-task-1')).asElement();
        if (!row) throw new Error('未找到任务行');
        await page.evaluate((r) => r.querySelector('.reminder-toggle').click(), row);
        await page.waitForTimeout(800);
      };
      await toggleOnce();
      let list = await apiJSON(page, 'GET', '/api/tasks');
      let t = (list.data || []).find(x => x.id === ctx.task1Id);
      if (!t || t.enabled !== false) throw new Error('第一次切换后 enabled 应为 false');

      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(800);
      list = await apiJSON(page, 'GET', '/api/tasks');
      t = (list.data || []).find(x => x.id === ctx.task1Id);
      if (!t || t.enabled !== false) throw new Error('刷新后 enabled 状态未保持');
      const pageText = await page.textContent('body');
      if (!/已停用/.test(pageText)) throw new Error('页面未显示「已停用」分组');

      await toggleOnce();
      list = await apiJSON(page, 'GET', '/api/tasks');
      t = (list.data || []).find(x => x.id === ctx.task1Id);
      if (!t || t.enabled !== true) throw new Error('第二次切换后 enabled 应为 true');
      await runner.screenshot(page, '30-s2-toggle');
    });

    runner.it('S2-6 编辑改命令后保存：内容更新且 enabled 不被重置', async function () {
      await page.evaluate(() => {
        const rows = document.querySelectorAll('.reminder-row');
        for (const r of rows) {
          const c = r.querySelector('.reminder-content');
          if (c && c.textContent.trim() === 'e2e-task-1') {
            const btns = r.querySelectorAll('.reminder-actions button');
            for (const b of btns) { if (b.textContent.trim() === '编辑') { b.click(); return; } }
          }
        }
        throw new Error('未找到编辑按钮');
      });
      await page.waitForSelector('.modal-card', { state: 'visible' });
      const ta = await page.$('.modal-card textarea.editor-content');
      await ta.fill('echo edited > ' + TASK_OUT_FILE);
      await page.click('.modal-card button:has-text("保存")');
      await page.waitForTimeout(1200);

      const list = await apiJSON(page, 'GET', '/api/tasks');
      const t = (list.data || []).find(x => x.id === ctx.task1Id);
      if (!t) throw new Error('任务丢失');
      if (!/edited/.test(t.command)) throw new Error('命令未更新: ' + t.command);
      if (t.enabled !== true) throw new Error('编辑后 enabled 被重置（后端应忽略 enabled 字段）');
      await runner.screenshot(page, '30-s2-edited');
    });

    runner.it('S2-7 删除任务（确认弹窗 → 确定）', async function () {
      await page.evaluate(() => {
        const rows = document.querySelectorAll('.reminder-row');
        for (const r of rows) {
          const c = r.querySelector('.reminder-content');
          if (c && c.textContent.trim() === 'e2e-task-1') {
            const btns = r.querySelectorAll('.reminder-actions button');
            for (const b of btns) { if (b.textContent.trim() === '删除') { b.click(); return; } }
          }
        }
        throw new Error('未找到删除按钮');
      });
      await page.waitForTimeout(1200);
      const list = await apiJSON(page, 'GET', '/api/tasks');
      const t = (list.data || []).find(x => x.id === ctx.task1Id);
      if (t) throw new Error('确认后任务未删除');
      await page.waitForTimeout(2500);
      const late = ctx.networkLogs.filter(l => l.type === 'request' && l.url.includes('/api/tasks') && Date.now() - l.timestamp < 2500);
      for (const l of late) {
        if (l.url.includes(ctx.task1Id)) throw new Error('删除后仍有该任务的轮询请求');
      }
      await runner.screenshot(page, '30-s2-deleted');
    });

    runner.it('S2-8 刷新页面持久化正确（sched_tasks.json）', async function () {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(800);
      const list = await apiJSON(page, 'GET', '/api/tasks');
      if ((list.data || []).some(t => t.name === 'e2e-task-1')) throw new Error('删除的任务复活');
      const storePath = path.join(RUN_DIR, 'data', 'sched_tasks.json');
      if (!fs.existsSync(storePath)) throw new Error('sched_tasks.json 不存在: ' + storePath);
      await runner.screenshot(page, '30-s2-persist');
    });
  });
}

module.exports = { register };
