'use strict';

/**
 * 34-reminders-actions.js — review-2026-08 方案 02
 * S4 提醒三动作（A6 + B 批 833d397 字段合并回归，P0）
 */

const fs = require('fs');
const path = require('path');

const RUN_DIR = process.env.KAIRO_RUN_DIR || '/tmp/kairo-review-2026-08/run';
const CMD_OUT_FILE = '/tmp/kairo-e2e-reminder.out';
const TOUCH_BIN = fs.existsSync('/usr/bin/touch') ? '/usr/bin/touch' : '/bin/touch';

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

function futureLocalAt(minutesAhead) {
  const d = new Date(Date.now() + minutesAhead * 60000);
  const pad = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

async function openNewEditor(page) {
  await page.click('button:has-text("+ 新增提醒")');
  await page.waitForSelector('.modal-card', { state: 'visible' });
}

async function selectOnceType(page) {
  const btn = await page.$('.editor-type-tabs .tab-btn[data-type="once"]');
  if (!btn) throw new Error('未找到「单次」类型按钮');
  await btn.click();
  await page.waitForTimeout(300);
}

async function toastText(page) {
  return page.evaluate(() => {
    const t = document.querySelector('#toast');
    return t ? t.textContent.trim() : '';
  });
}

function register(runner, ctx) {
  const { page, baseUrl } = ctx;
  const created = [];

  runner.describe('S4 提醒三动作（A6 + 833d397）', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '/#/reminders', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(800);
    });

    runner.afterAll(async function () {
      for (const id of created) {
        try { await apiJSON(page, 'DELETE', '/api/reminders/' + id); } catch (e) { /* ignore */ }
      }
    });

    runner.it('S4-1 动作=弹窗：编辑器只显示 popup 字段', async function () {
      await openNewEditor(page);
      await selectOnceType(page);
      await page.fill('.modal-card textarea.editor-content', 'e2e-popup-提醒');
      await page.fill('.modal-card input[type="datetime-local"]', futureLocalAt(10));
      const wrap = await page.$('.editor-action-wrap');
      const selVal = await wrap.$eval('select', el => el.value);
      if (selVal !== 'popup') throw new Error('默认动作不是 popup: ' + selVal);
      const extraInputs = await wrap.$$eval('input[type="text"]', els => els.length);
      if (extraInputs > 0) throw new Error('popup 模式出现多余输入框: ' + extraInputs);
      await runner.screenshot(page, '34-s4-editor-popup');
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(1000);

      const list = await apiJSON(page, 'GET', '/api/reminders');
      const r = (list.data || []).find(x => x.content === 'e2e-popup-提醒');
      if (!r) throw new Error('popup 提醒未创建');
      if (!r.action || r.action.kind !== 'popup') throw new Error('action.kind 异常: ' + JSON.stringify(r.action));
      created.push(r.id);
    });

    runner.it('S4-2 动作=打开网址：字段切换 + 保存成功', async function () {
      await openNewEditor(page);
      await selectOnceType(page);
      await page.fill('.modal-card textarea.editor-content', 'e2e-url-提醒');
      await page.fill('.modal-card input[type="datetime-local"]', futureLocalAt(10));
      await page.selectOption('.editor-action-wrap select', 'url');
      await page.waitForTimeout(300);
      const urlInput = await page.$('.editor-action-wrap input[placeholder*="meeting"]');
      if (!urlInput) throw new Error('url 动作未渲染网址输入框');
      await urlInput.fill('https://example.com/e2e');
      await runner.screenshot(page, '34-s4-editor-url');
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(1000);

      const list = await apiJSON(page, 'GET', '/api/reminders');
      const r = (list.data || []).find(x => x.content === 'e2e-url-提醒');
      if (!r) throw new Error('url 提醒未创建');
      if (!r.action || r.action.kind !== 'url' || r.action.url !== 'https://example.com/e2e') {
        throw new Error('url 动作保存异常: ' + JSON.stringify(r.action));
      }
      created.push(r.id);
      const rowText = await page.textContent('.reminders-list');
      if (!/e2e-url-提醒/.test(rowText)) throw new Error('列表未显示新提醒');
    });

    runner.it('S4-3 动作=执行命令：保存 + 立即触发 + 审计落盘', async function () {
      try { fs.unlinkSync(CMD_OUT_FILE); } catch (e) { /* ignore */ }
      await openNewEditor(page);
      await selectOnceType(page);
      await page.fill('.modal-card textarea.editor-content', 'e2e-cmd-提醒');
      await page.fill('.modal-card input[type="datetime-local"]', futureLocalAt(2));
      await page.selectOption('.editor-action-wrap select', 'command');
      await page.waitForTimeout(300);
      const inputs = await page.$$('.editor-action-wrap input[type="text"]');
      if (inputs.length < 3) throw new Error('command 动作未渲染三个输入框（命令/参数/工作目录）');
      await inputs[0].fill(TOUCH_BIN);
      await inputs[1].fill(CMD_OUT_FILE);
      await runner.screenshot(page, '34-s4-editor-command');
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(1000);

      const list = await apiJSON(page, 'GET', '/api/reminders');
      const r = (list.data || []).find(x => x.content === 'e2e-cmd-提醒');
      if (!r) throw new Error('command 提醒未创建');
      if (!r.action || r.action.kind !== 'command' || r.action.command !== TOUCH_BIN) {
        throw new Error('command 动作保存异常: ' + JSON.stringify(r.action));
      }
      created.push(r.id);
      ctx.cmdReminderId = r.id;

      const row = await page.evaluateHandle((content) => {
        const rows = document.querySelectorAll('.reminder-row');
        for (const x of rows) {
          const c = x.querySelector('.reminder-content');
          if (c && c.textContent.trim() === content) return x;
        }
        return null;
      }, 'e2e-cmd-提醒');
      const rowEl = row.asElement();
      if (!rowEl) throw new Error('列表未找到 e2e-cmd-提醒 行');
      await page.evaluate((el) => {
        const btns = el.querySelectorAll('.reminder-actions button');
        for (const b of btns) { if (b.textContent.trim() === '测试') { b.click(); return; } }
        throw new Error('未找到测试按钮');
      }, rowEl);
      await page.waitForTimeout(2500);

      if (!fs.existsSync(CMD_OUT_FILE)) throw new Error('命令动作未真实执行（文件不存在）');
      const auditPath = path.join(RUN_DIR, 'logs', 'audit.log');
      const audit = fs.existsSync(auditPath) ? fs.readFileSync(auditPath, 'utf8') : '';
      if (!/reminder\.fire/.test(audit)) throw new Error('audit.log 无 reminder.fire.* 记录');
      await runner.screenshot(page, '34-s4-command-fired');
    });

    runner.it('S4-4 编辑只改内容：时间/动作字段不被清空（833d397 回归）', async function () {
      const before = await apiJSON(page, 'GET', '/api/reminders');
      const r0 = (before.data || []).find(x => x.content === 'e2e-cmd-提醒');
      if (!r0) throw new Error('前置提醒不存在');

      await page.evaluate(() => {
        const rows = document.querySelectorAll('.reminder-row');
        for (const x of rows) {
          const c = x.querySelector('.reminder-content');
          if (c && c.textContent.trim() === 'e2e-cmd-提醒') {
            const btns = x.querySelectorAll('.reminder-actions button');
            for (const b of btns) { if (b.textContent.trim() === '编辑') { b.click(); return; } }
          }
        }
        throw new Error('未找到编辑按钮');
      });
      await page.waitForSelector('.modal-card', { state: 'visible' });
      await page.fill('.modal-card textarea.editor-content', 'e2e-cmd-提醒-已编辑');
      await runner.screenshot(page, '34-s4-edit-content-only');
      await page.click('.modal-card button:has-text("保存")');
      await page.waitForTimeout(1000);

      const after = await apiJSON(page, 'GET', '/api/reminders');
      const r1 = (after.data || []).find(x => x.id === r0.id);
      if (!r1) throw new Error('编辑后提醒丢失');
      if (r1.content !== 'e2e-cmd-提醒-已编辑') throw new Error('内容未更新');
      if (r1.at !== r0.at) throw new Error(`时间被改动: ${r0.at} → ${r1.at}`);
      if (!r1.action || r1.action.kind !== 'command' || r1.action.command !== TOUCH_BIN) {
        throw new Error('动作被清空/改动: ' + JSON.stringify(r1.action));
      }
      if (JSON.stringify(r1.action.args || []) !== JSON.stringify(r0.action.args || [])) {
        throw new Error('动作参数被改动');
      }
    });

    runner.it('S4-5 非法输入：空内容 / 非法 url 均被校验拦截', async function () {
      await openNewEditor(page);
      await selectOnceType(page);
      await page.fill('.modal-card input[type="datetime-local"]', futureLocalAt(10));
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(600);
      let t = await toastText(page);
      if (!/内容|请填写/.test(t)) throw new Error('空内容未校验: ' + t);

      await page.fill('.modal-card textarea.editor-content', 'e2e-bad-url');
      await page.selectOption('.editor-action-wrap select', 'url');
      await page.waitForTimeout(300);
      const urlInput = await page.$('.editor-action-wrap input[placeholder*="meeting"]');
      await urlInput.fill('not-a-url');
      await page.click('.modal-card button:has-text("添加")');
      await page.waitForTimeout(600);
      t = await toastText(page);
      if (!/http|网址/.test(t)) throw new Error('非法 url 未校验: ' + t);
      const stillOpen = await page.$('.modal-card');
      if (!stillOpen) throw new Error('校验失败却关闭了编辑器');

      const list = await apiJSON(page, 'GET', '/api/reminders');
      if ((list.data || []).some(x => x.content === 'e2e-bad-url')) throw new Error('非法输入被保存');
      await page.keyboard.press('Escape');
      await page.waitForTimeout(300);
      await runner.screenshot(page, '34-s4-invalid');
    });
  });
}

module.exports = { register };
