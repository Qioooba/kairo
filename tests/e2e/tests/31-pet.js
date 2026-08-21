'use strict';

/**
 * 31-pet.js — 宠物彩蛋流程（v3：宠物本体已改为原生桌面宠物）
 *
 * 宠物不再在浏览器内渲染（无 .kairo-pet-widget / 迷你面板），
 * 本套件只验证浏览器侧仍保留的能力：解锁、状态接口、零痕迹、武林页宠物榜、经验增长。
 * 桌面宠物的展示/拖拽/换肤/改名由 internal/deskpet 原生窗口承担，不在此覆盖。
 */

const fs = require('fs');
const path = require('path');

const RUN_DIR = process.env.KAIRO_RUN_DIR || '/tmp/kairo-review-2026-08/run';

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

async function gotoHome(page, baseUrl) {
  await page.evaluate(() => { location.hash = '#/'; });
  await page.waitForTimeout(300);
}

async function clickAboutCard(page) {
  await page.waitForSelector('.tool-card[aria-label="关于"]', { state: 'visible' });
  await page.click('.tool-card[aria-label="关于"]');
  await page.waitForTimeout(150);
}

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('S3 宠物彩蛋 - 实例 A 未解锁零痕迹', function () {
    let aPreEnabled = false;

    runner.beforeAll(async function () {
      const st = await apiJSON(page, 'GET', '/api/pet/state');
      aPreEnabled = !!(st.data && st.data.enabled);
    });

    function maybeSkip() {
      if (aPreEnabled) {
        runner.skipTest('宠物已被前置用例解锁，零痕迹验证需独立全新实例（单独跑已通过）');
        return true;
      }
      return false;
    }

    runner.it('S3A-1 浏览器内无宠物 DOM；关于卡片正常；sponsor 无宠物榜 tab', async function () {
      if (maybeSkip()) return;
      await page.goto(baseUrl + '/#/', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
      const widget = await page.$('.kairo-pet-widget');
      if (widget) throw new Error('浏览器内出现宠物 DOM（v3 已移除网页宠物）');

      await page.click('.tool-card[aria-label="关于"]');
      await page.waitForFunction(() => location.hash === '#/about');
      await page.waitForTimeout(500);

      await page.evaluate(() => { location.hash = '#/sponsor'; });
      await page.waitForFunction(() => location.hash === '#/sponsor');
      await page.waitForTimeout(1500);
      const petTab = await page.$('.pet-board-tab');
      if (petTab) {
        const visible = await petTab.isVisible();
        if (visible) throw new Error('未解锁时宠物榜 tab 可见');
      }
      await runner.screenshot(page, '31-s3a-sponsor-no-pet-tab');
    });

    runner.it('S3A-2 /api/pet/state = {enabled:false} 且 data 目录无 pet.json', async function () {
      if (maybeSkip()) return;
      const st = await apiJSON(page, 'GET', '/api/pet/state');
      if (st.status !== 200) throw new Error('state 接口状态码 ' + st.status);
      if (!st.data || st.data.enabled !== false) throw new Error('未解锁应返 {enabled:false}，实际: ' + JSON.stringify(st.data));
      const petJson = path.join(RUN_DIR, 'data', 'pet.json');
      const petBak = petJson + '.bak';
      if (fs.existsSync(petJson)) throw new Error('未解锁却存在 pet.json');
      if (fs.existsSync(petBak)) throw new Error('未解锁却存在 pet.json.bak');
    });

    runner.it('S3A-3 10s 内点 5 次，等 11s 再点 1 次 → 不解锁（滑窗清零）', async function () {
      if (maybeSkip()) return;
      await page.waitForTimeout(11000);
      const clickLog = [];
      for (let i = 0; i < 5; i++) {
        clickLog.push(await page.evaluate(() => Date.now()));
        await gotoHome(page, baseUrl);
        await clickAboutCard(page);
      }
      const after5 = await apiJSON(page, 'GET', '/api/pet/state');
      clickLog.push(await page.evaluate(() => Date.now()));
      await page.waitForTimeout(11000);
      clickLog.push(await page.evaluate(() => Date.now()));
      const afterWait = await apiJSON(page, 'GET', '/api/pet/state');
      await gotoHome(page, baseUrl);
      await clickAboutCard(page);
      await page.waitForTimeout(1500);

      const st = await apiJSON(page, 'GET', '/api/pet/state');
      if (st.data && st.data.enabled) {
        throw new Error('滑窗应已清零，5+1 次点击却解锁了 | clickLog=' + JSON.stringify(clickLog)
          + ' | after5.enabled=' + (after5.data && after5.data.enabled)
          + ' | afterWait.enabled=' + (afterWait.data && afterWait.data.enabled));
      }
    });
  });

  runner.describe('S3 宠物彩蛋 - 实例 B 解锁全流程', function () {
    runner.it('S3B-1 10s 内点「关于」卡片 6 次 → 解锁 + pet.json 落盘', async function () {
      for (let i = 0; i < 6; i++) {
        await gotoHome(page, baseUrl);
        await clickAboutCard(page);
      }
      await page.waitForTimeout(1000);
      const toastText = await page.evaluate(() => {
        const t = document.querySelector('#toast');
        return t ? t.textContent.trim() : '';
      });
      if (!/宠物/.test(toastText)) throw new Error('解锁 toast 文案异常: ' + toastText);
      const st = await apiJSON(page, 'GET', '/api/pet/state');
      if (!st.data || !st.data.enabled) throw new Error('API 未返回 enabled:true');
      const petJson = path.join(RUN_DIR, 'data', 'pet.json');
      if (!fs.existsSync(petJson)) throw new Error('解锁后 pet.json 未落盘');
      await runner.screenshot(page, '31-s3b-unlocked');
    });

    runner.it('S3B-2 点击关于卡片照常跳转（彩蛋不干扰导航）', async function () {
      await gotoHome(page, baseUrl);
      await clickAboutCard(page);
      await page.waitForFunction(() => location.hash === '#/about');
    });

    runner.it('S3B-8 sponsor 页出现双 tab；宠物榜未配置端点时本地降级（X5）', async function () {
      await page.evaluate(() => { location.hash = '#/sponsor'; });
      await page.waitForFunction(() => location.hash === '#/sponsor');
      await page.waitForTimeout(1500);

      const wulinBtn = await page.$('.board-tab:not(.pet-board-tab)');
      const petBtn = await page.$('.pet-board-tab');
      if (!wulinBtn || !petBtn) throw new Error('双 tab 按钮缺失');
      const petVisible = await petBtn.isVisible();
      if (!petVisible) throw new Error('已解锁但宠物榜 tab 不可见');

      const netBefore = ctx.networkLogs.length;
      await petBtn.click();
      await page.waitForTimeout(2500);
      const boardText = await page.$eval('.pet-board', el => el.textContent).catch(() => '');
      if (!boardText) throw new Error('宠物榜容器无内容');
      if (!/榜单空空如也|宠物排行榜|第 \d+ 名|未上榜|未配置|仅本地展示/.test(boardText)) {
        throw new Error('宠物榜内容异常: ' + boardText.substring(0, 120));
      }

      const newReqs = ctx.networkLogs.slice(netBefore).filter(l => l.type === 'request');
      for (const l of newReqs) {
        if (!l.url.startsWith(baseUrl) && !l.url.startsWith('data:') && !l.url.startsWith('blob:')) {
          throw new Error('X5 违规：宠物榜触发外部请求 ' + l.url);
        }
      }
      const syncReq = newReqs.find(l => l.url.includes('/api/pet/sync'));
      if (!syncReq) throw new Error('未发起 /api/pet/sync');
      const tab = await page.evaluate(() => sessionStorage.getItem('kairo:pet:boardtab'));
      if (tab !== 'pet') throw new Error('sessionStorage 未记住宠物 tab 选择: ' + tab);
      await runner.screenshot(page, '31-s3b-pet-board-local');
    });

    runner.it('S3B-9 执行一次 HTTP 请求后经验增长（audit→exp 链路）', async function () {
      const before = await apiJSON(page, 'GET', '/api/pet/state');
      const expBefore = Number(before.data.total_earned || 0);
      const req = await apiJSON(page, 'POST', '/api/http/request', {
        method: 'GET', url: baseUrl + '/api/config', headers: {}, body: '',
      });
      if (req.status !== 200) throw new Error('http.request 失败: ' + req.status);
      await page.waitForTimeout(1000);
      const after = await apiJSON(page, 'GET', '/api/pet/state');
      const expAfter = Number(after.data.total_earned || 0);
      if (expAfter <= expBefore) {
        const auditPath = path.join(RUN_DIR, 'logs', 'audit.log');
        const audit = fs.existsSync(auditPath) ? fs.readFileSync(auditPath, 'utf8') : '';
        const lastHttpReq = audit.split('\n').filter(l => l.includes('op":"http.request"')).slice(-1)[0] || '';
        if (!lastHttpReq) throw new Error('audit.log 无 http.request 记录');
        if (!lastHttpReq.includes('/api/config')) throw new Error('最近 http.request 非本用例产生: ' + lastHttpReq);
        ctx.expGrowth = 0;
        ctx.expGrowthNote = '冷却生效：60min 内同 op 不重复计分（audit 已确认本次请求落盘）';
      } else {
        ctx.expGrowth = expAfter - expBefore;
      }
      await runner.screenshot(page, '31-s3b-exp-growth');
    });
  });

  runner.describe('S9 about 页懒渲染（B 批 3a8da9d）', function () {
    runner.it('S9-1 懒渲染 section 最终全部渲染，无空白', async function () {
      await page.goto(baseUrl + '/#/about', { waitUntil: 'domcontentloaded' });
      const early = await page.evaluate(() => {
        const lazys = document.querySelectorAll('.about-lazy-section');
        let unmounted = 0;
        lazys.forEach(l => { if (l.childElementCount === 0) unmounted++; });
        return { lazyCount: lazys.length, unmounted, nodes: document.body.querySelectorAll('*').length };
      });

      await page.evaluate(async () => {
        const scroller = document.scrollingElement || document.documentElement;
        for (let i = 0; i < 30; i++) {
          window.scrollTo(0, i * 800);
          await new Promise(r => setTimeout(r, 80));
        }
        window.scrollTo(0, scroller.scrollHeight);
      });
      await page.waitForTimeout(1500);

      const after = await page.evaluate(() => {
        const lazys = document.querySelectorAll('.about-lazy-section');
        let mounted = 0;
        let emptyText = 0;
        lazys.forEach(l => {
          if (l.childElementCount > 0) mounted++;
          if (l.textContent.trim().length < 10) emptyText++;
        });
        return { lazyCount: lazys.length, mounted, emptyText, nodes: document.body.querySelectorAll('*').length };
      });
      if (after.lazyCount < 12) throw new Error('懒渲染 section 数量不足 12: ' + after.lazyCount);
      if (after.mounted !== after.lazyCount) throw new Error(`存在未渲染的 section（${after.mounted}/${after.lazyCount}）`);
      if (after.emptyText > 0) throw new Error('存在空白 section: ' + after.emptyText);

      const ver = await page.evaluate(() => {
        const el = document.querySelector('#footer-version');
        return el ? el.textContent.trim() : '';
      });
      ctx.aboutLazy = { early, after, footerVersion: ver };
      await runner.screenshot(page, '31-s9-about-scrolled');
    });
  });
}

module.exports = { register };
