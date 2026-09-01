'use strict';

/**
 * 便笺中心：真实排障数据、真实点击、整页截图。
 * 不 mock DOM；创建/搜索/悬浮/提醒/桌面/冲突都走 /api/notes 与页面按钮。
 */
const fs = require('fs');
const path = require('path');

const PREFIX = '__e2e_notes_';
const WAS_TITLE = PREFIX + 'WAS排障';
const HTTP_TITLE = PREFIX + 'HTTP对账';
const WAS_BODY = [
  'host=was-prod-01',
  'path=/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1',
  'request-id=HL202609010031-8821',
  'user=kairo_lab',
].join('\n');
const HTTP_BODY = [
  'POST /credit/apply',
  'seq_no=SEQ_NO_90012',
  'org=恒力',
  'http-status=500',
].join('\n');
const EXTRA_SHOT_DIR = path.resolve(__dirname, '../../../output/playwright');

async function apiJSON(page, method, url, body) {
  return page.evaluate(async ({ method, url, body }) => {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const response = await fetch(url, opts);
    let data = null;
    try { data = await response.json(); } catch (e) { data = null; }
    return { status: response.status, data };
  }, { method, url, body });
}

async function cleanupTagged(page) {
  const notes = await apiJSON(page, 'GET', '/api/notes');
  for (const item of (notes.data || [])) {
    const blob = String(item.title || '') + '\n' + String(item.body || '');
    if (blob.indexOf(PREFIX) < 0) continue;
    if (item.desktop && item.desktop.visible) {
      await apiJSON(page, 'PATCH', '/api/notes/' + encodeURIComponent(item.id), {
        base_revision: item.revision,
        desktop: Object.assign({}, item.desktop, { visible: false }),
      });
    }
    await apiJSON(page, 'DELETE', '/api/notes/' + encodeURIComponent(item.id));
  }
  const reminders = await apiJSON(page, 'GET', '/api/reminders');
  for (const item of (reminders.data || [])) {
    if (String(item.content || '').indexOf(PREFIX) < 0) continue;
    await apiJSON(page, 'DELETE', '/api/reminders/' + encodeURIComponent(item.id));
  }
}

async function listNotes(page) {
  const res = await apiJSON(page, 'GET', '/api/notes');
  return Array.isArray(res.data) ? res.data : [];
}

function findNote(items, title) {
  return (items || []).find(function (n) { return n.title === title; });
}

function card(page, title) {
  return page.locator('article.notes-row', { hasText: title }).first();
}

async function collapseFloat(page) {
  const close = page.locator('#kairo-note-layer button[aria-label="收起页面悬浮"]');
  if (await close.count()) {
    await close.click({ force: true });
    await page.waitForFunction(function () {
      return !document.querySelector('#kairo-note-layer .floating-note');
    }, null, { timeout: 8000 });
  }
}

async function gotoList(page, baseUrl) {
  const hash = await page.evaluate(function () { return location.hash; });
  if (hash === '#/notes/list') {
    await page.evaluate(function () { location.hash = '#/home'; });
    await page.waitForFunction(function () { return location.hash === '#/home'; });
  }
  await page.goto(baseUrl + '/#/notes/list', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.notes-center-title, h2', { timeout: 8000 });
  await page.waitForTimeout(400);
}

async function shot(runner, page, name) {
  await runner.screenshot(page, name);
  fs.mkdirSync(EXTRA_SHOT_DIR, { recursive: true });
  await page.screenshot({ path: path.join(EXTRA_SHOT_DIR, name + '.png'), fullPage: true });
}

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('便笺中心 - 真实数据与 UI', function () {
    runner.beforeAll(async function () {
      await page.goto(baseUrl + '/#/notes/list', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(400);
      await page.evaluate(function () {
        try {
          if (window.Kairo && window.Kairo.theme && window.Kairo.theme.set) window.Kairo.theme.set('dark');
          else document.documentElement.setAttribute('data-theme', 'dark');
        } catch (e) { /* ignore */ }
      });
      await cleanupTagged(page);
    });

    runner.beforeEach(async function () {
      await collapseFloat(page);
    });

    runner.afterAll(async function () {
      try { await cleanupTagged(page); } catch (e) { /* ignore */ }
      try { await page.setViewportSize({ width: 1366, height: 900 }); } catch (e) { /* ignore */ }
    });

    runner.it('空状态 CTA 点击新建并保存真实 WAS 排障便笺', async function () {
      await cleanupTagged(page);
      await gotoList(page, baseUrl);
      await page.waitForSelector('.notes-toolbar', { timeout: 8000 });
      await shot(runner, page, '38-notes-01-empty');

      const first = await page.$('button:has-text("新建第一条便笺")');
      if (first) await first.click();
      else await page.click('button:has-text("+ 新建便笺")');
      await page.waitForSelector('.notes-inline-body', { timeout: 8000 });
      await page.fill('.notes-inline-title', WAS_TITLE);
      await page.fill('.notes-inline-body', WAS_BODY);
      await page.click('.notes-inline-colors button[aria-label="蓝"]');
      await shot(runner, page, '38-notes-02-inline-edit');
      await page.click('.notes-row.editing button:has-text("保存")');
      await page.waitForFunction(function () {
        return !document.querySelector('.notes-row.editing');
      }, null, { timeout: 8000 });

      const items = await listNotes(page);
      const n = findNote(items, WAS_TITLE);
      if (!n) throw new Error('API 未落地 WAS 便笺: ' + JSON.stringify(items).slice(0, 400));
      if (n.floating) throw new Error('中心新建不应打开页面悬浮');
      if (n.desktop && n.desktop.visible) throw new Error('中心新建不应自动钉桌面');
      if (n.color !== 'blue') throw new Error('颜色未保存: ' + n.color);
      if (String(n.body).indexOf('HL202609010031-8821') < 0) throw new Error('正文未保存 request-id');
      const meta = await card(page, WAS_TITLE).locator('.notes-row-meta').innerText();
      if (meta.indexOf('更新于') < 0) throw new Error('卡片 meta 缺少更新于: ' + meta);
      await shot(runner, page, '38-notes-03-saved-card');
    });

    runner.it('搜索真实 request-id，页面悬浮后再从便笺设提醒', async function () {
      await gotoList(page, baseUrl);
      const seeded = findNote(await listNotes(page), HTTP_TITLE);
      if (!seeded) {
        const created = await apiJSON(page, 'POST', '/api/notes', {
          title: HTTP_TITLE, body: HTTP_BODY, color: 'green', floating: false,
        });
        if (created.status !== 201 || !created.data || !created.data.id) {
          throw new Error('seed HTTP 便笺失败: ' + JSON.stringify(created).slice(0, 300));
        }
      }
      await gotoList(page, baseUrl);
      await page.fill('.notes-search', 'HL202609010031-8821');
      await page.waitForTimeout(200);
      const visibleTitles = await page.locator('.notes-row-title').allTextContents();
      if (visibleTitles.join('\n').indexOf(WAS_TITLE) < 0) throw new Error('搜索未命中 WAS 便笺');
      if (visibleTitles.join('\n').indexOf(HTTP_TITLE) >= 0) throw new Error('搜索仍显示不相关便笺');
      await shot(runner, page, '38-notes-04-search');
      await page.fill('.notes-search', '');
      await page.waitForTimeout(200);

      await card(page, WAS_TITLE).locator('button', { hasText: '页面悬浮' }).click();
      await page.waitForSelector('#kairo-note-layer .floating-note', { timeout: 8000 });
      const floated = findNote(await listNotes(page), WAS_TITLE);
      if (!floated || !floated.floating) throw new Error('API floating 未打开');
      const floatBody = await page.locator('#kairo-note-layer .floating-note-body').inputValue();
      if (floatBody.indexOf('was-prod-01') < 0) throw new Error('悬浮层未带上真实正文');
      await shot(runner, page, '38-notes-05-floating');

      await page.locator('#kairo-note-layer button', { hasText: '设提醒' }).click();
      await page.waitForURL(/notes\/reminders/, { timeout: 8000 });
      await page.waitForSelector('.modal-card', { timeout: 8000 });
      const modalTitle = await page.locator('.modal-title, .modal-card').first().innerText();
      if (modalTitle.indexOf('从便笺创建提醒') < 0) throw new Error('提醒弹窗标题不对: ' + modalTitle.slice(0, 80));
      const reminder = await page.locator('.modal-card textarea.editor-content').inputValue();
      if (reminder.indexOf(WAS_TITLE) < 0 || reminder.indexOf('HL202609010031-8821') < 0) {
        throw new Error('提醒未预填便笺快照: ' + reminder);
      }
      await shot(runner, page, '38-notes-06-reminder');
      await page.click('.modal-card button:has-text("取消")');
      await page.waitForTimeout(200);
      await collapseFloat(page);
      await gotoList(page, baseUrl);
      await page.waitForSelector('article.notes-row', { timeout: 8000 });
      const stillFloat = findNote(await listNotes(page), WAS_TITLE);
      if (stillFloat && stillFloat.floating) throw new Error('收起悬浮后 API floating 仍为 true');
    });

    runner.it('置顶到桌面再收回，更多菜单归档后可恢复', async function () {
      await gotoList(page, baseUrl);
      await card(page, WAS_TITLE).locator('button', { hasText: '置顶到桌面' }).click();
      await page.waitForFunction(function (title) {
        const rows = document.querySelectorAll('article.notes-row');
        for (const row of rows) {
          if ((row.querySelector('.notes-row-title') || {}).textContent === title) {
            return (row.textContent || '').indexOf('桌面置顶') >= 0;
          }
        }
        return false;
      }, WAS_TITLE, { timeout: 8000 });
      const pinned = findNote(await listNotes(page), WAS_TITLE);
      if (!pinned || !pinned.desktop || !pinned.desktop.visible) {
        throw new Error('桌面可见未写入 API: ' + JSON.stringify(pinned));
      }
      await shot(runner, page, '38-notes-07-desktop');
      await card(page, WAS_TITLE).locator('button', { hasText: '从桌面拿走' }).click();
      await page.waitForFunction(function (title) {
        const rows = document.querySelectorAll('article.notes-row');
        for (const row of rows) {
          if ((row.querySelector('.notes-row-title') || {}).textContent === title) {
            return (row.textContent || '').indexOf('桌面置顶') < 0;
          }
        }
        return false;
      }, WAS_TITLE, { timeout: 8000 });

      await card(page, WAS_TITLE).locator('summary', { hasText: '更多' }).click();
      await page.waitForSelector('.notes-more-menu button:has-text("归档")', { timeout: 4000 });
      await shot(runner, page, '38-notes-08-more-menu');
      await page.click('.notes-more-menu button:has-text("归档")');
      await page.waitForTimeout(400);
      await page.selectOption('.notes-filter', 'archived');
      await page.waitForSelector('article.notes-row', { timeout: 8000 });
      if ((await card(page, WAS_TITLE).count()) < 1) throw new Error('已归档筛选看不到便笺');
      await shot(runner, page, '38-notes-09-archived');
      await card(page, WAS_TITLE).locator('summary', { hasText: '更多' }).click();
      await page.click('.notes-more-menu button:has-text("恢复")');
      await page.waitForTimeout(400);
      await page.selectOption('.notes-filter', 'active');
      await page.waitForSelector('article.notes-row', { timeout: 8000 });
    });

    runner.it('内联保存遇到版本冲突要弹出选择，不静默覆盖', async function () {
      await gotoList(page, baseUrl);
      await card(page, WAS_TITLE).locator('.notes-row-main').click();
      await page.waitForSelector('.notes-inline-body', { timeout: 8000 });
      await page.fill('.notes-inline-body', WAS_BODY + '\nlocal-edit=true');
      const current = findNote(await listNotes(page), WAS_TITLE);
      if (!current) throw new Error('冲突测试找不到便笺');
      const patched = await apiJSON(page, 'PATCH', '/api/notes/' + encodeURIComponent(current.id), {
        base_revision: current.revision,
        body: WAS_BODY + '\nserver-edit=true',
      });
      if (patched.status !== 200) throw new Error('制造冲突失败: ' + JSON.stringify(patched).slice(0, 300));
      await page.evaluate(function (id) {
        const n = window.Kairo.notes.find(id);
        if (n) n.revision = 1;
      }, current.id);
      await page.click('.notes-row.editing button:has-text("保存")');
      await page.waitForSelector('.modal-card', { timeout: 8000 });
      const text = await page.locator('.modal-card').innerText();
      if (text.indexOf('版本冲突') < 0) throw new Error('未出现冲突弹窗: ' + text.slice(0, 120));
      await shot(runner, page, '38-notes-10-conflict');
      await page.click('.modal-card button:has-text("使用最新版本")');
      await page.waitForTimeout(300);
      const cancelEdit = page.locator('.notes-row.editing button:has-text("取消")');
      if (await cancelEdit.count()) await cancelEdit.click();
      await collapseFloat(page);

      await page.setViewportSize({ width: 1920, height: 1080 });
      await gotoList(page, baseUrl);
      await shot(runner, page, '38-notes-11-1920');
      await page.setViewportSize({ width: 1366, height: 900 });
      await gotoList(page, baseUrl);
      await shot(runner, page, '38-notes-12-1366');

      await card(page, WAS_TITLE).locator('summary', { hasText: '更多' }).click();
      await page.click('.notes-more-menu button:has-text("删除")');
      await page.waitForSelector('.kairo-dialog button:has-text("删除")', { timeout: 4000 });
      await page.click('.kairo-dialog button:has-text("删除")');
      await page.waitForTimeout(400);
      if (findNote(await listNotes(page), WAS_TITLE)) throw new Error('删除后 API 仍有 WAS 便笺');
    });
  });
}

module.exports = { register };
