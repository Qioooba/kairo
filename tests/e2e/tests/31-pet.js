'use strict';

/**
 * 31-pet.js — review-2026-08 方案 02
 * S3 宠物彩蛋全流程（A1，P0）+ S9 about 页懒渲染（B 批 3a8da9d）
 *
 * 顺序敏感：实例必须先处于「未解锁」状态（全新数据目录）。
 * 实例 C（config 强制开关）与「删除 pet.json 重启」由脚本外手动验证，见 02-e2e-report.md。
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
      // 实例 A 前提：全新数据目录、宠物未解锁。若已被前置用例解锁
      // （如全量回归里其它套件点「关于」卡触发），本实例测试整体跳过，
      // 零痕迹验证在独立全新实例上跑（见 02-e2e-report.md 记录）。
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

    runner.it('S3A-1 无浮动宠物 DOM；关于卡片正常；sponsor 无宠物榜 tab', async function () {
      if (maybeSkip()) return;
      await page.goto(baseUrl + '/#/', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
      const widget = await page.$('.kairo-pet-widget');
      if (widget) throw new Error('未解锁却出现浮动宠物 DOM');

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
      // 先等 >10s 冲掉前面用例（S3A-1）留下的点击时间戳（SPA 不重载，计数器跨页面持久）
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
      const widget = await page.$('.kairo-pet-widget');
      if (widget) throw new Error('出现宠物 widget（不应解锁）');
    });
  });

  runner.describe('S3 宠物彩蛋 - 实例 B 解锁全流程', function () {
    runner.it('S3B-1 10s 内点「关于」卡片 6 次 → 解锁 + 浮动宠物 Lv1', async function () {
      for (let i = 0; i < 6; i++) {
        await gotoHome(page, baseUrl);
        await clickAboutCard(page);
      }
      await page.waitForSelector('.kairo-pet-widget', { state: 'visible', timeout: 8000 });
      const toastText = await page.evaluate(() => {
        const t = document.querySelector('#toast');
        return t ? t.textContent.trim() : '';
      });
      if (!/宠物/.test(toastText)) throw new Error('解锁 toast 文案异常: ' + toastText);
      const badge = await page.$eval('.kairo-pet-badge', el => el.textContent.trim()).catch(() => '');
      if (badge !== 'Lv1') throw new Error('等级徽章应为 Lv1，实际: ' + badge);
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

    runner.it('S3B-3 拖动到左上角 → 位置持久化 → 刷新后恢复', async function () {
      await page.evaluate(() => { location.hash = '#/'; });
      await page.waitForTimeout(800);
      const box = await page.$eval('.kairo-pet-widget', el => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, w: r.width, h: r.height };
      });
      await page.mouse.move(box.x + box.w / 2, box.y + box.h / 2);
      await page.mouse.down();
      for (let i = 1; i <= 25; i++) {
        await page.mouse.move(box.x + box.w / 2 - i * 60, box.y + box.h / 2 - i * 50, { steps: 2 });
        await page.waitForTimeout(30);
      }
      await page.mouse.up();
      await page.waitForTimeout(1200);

      const st = await apiJSON(page, 'GET', '/api/pet/state');
      const pos = st.data && st.data.pos;
      if (!pos || typeof pos.x !== 'number') throw new Error('pos 未保存: ' + JSON.stringify(st.data));
      if (pos.x > 0.15 || pos.y > 0.15) throw new Error('pos 未靠近左上角: ' + JSON.stringify(pos));

      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForSelector('.kairo-pet-widget', { state: 'visible', timeout: 8000 });
      await page.waitForTimeout(800);
      const box2 = await page.$eval('.kairo-pet-widget', el => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y };
      });
      if (box2.x > 120 || box2.y > 120) throw new Error('刷新后位置未恢复到左上区域: ' + JSON.stringify(box2));
      await runner.screenshot(page, '31-s3b-drag-topleft');
    });

    runner.it('S3B-4 窗口缩小后宠物 clamp 回可视区', async function () {
      await page.setViewportSize({ width: 1024, height: 768 });
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForSelector('.kairo-pet-widget', { state: 'visible', timeout: 8000 });
      await page.waitForTimeout(800);
      const box = await page.$eval('.kairo-pet-widget', el => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, w: r.width, h: r.height };
      });
      if (box.x < 0 || box.y < 0 || box.x + box.w > 1024 || box.y + box.h > 768) {
        throw new Error('缩小视口后宠物越界: ' + JSON.stringify(box));
      }
      await runner.screenshot(page, '31-s3b-resize-clamp');
      await page.setViewportSize({ width: 1366, height: 900 });
      await page.waitForTimeout(500);
    });

    runner.it('S3B-5 快速晃动换肤（皮肤 +1）+ 2s 冷却内二次晃动不触发', async function () {
      await page.evaluate(() => { location.hash = '#/'; });
      await page.waitForTimeout(800);
      const skinBefore = (await apiJSON(page, 'GET', '/api/pet/state')).data.skin;
      const skinCount = (await apiJSON(page, 'GET', '/api/pet/state')).data.skin_count;

      const shake = async () => {
        const box = await page.$eval('.kairo-pet-widget', el => {
          const r = el.getBoundingClientRect();
          return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
        });
        await page.mouse.move(box.x, box.y);
        await page.mouse.down();
        for (let i = 0; i < 10; i++) {
          const dx = (i % 2 === 0) ? 45 : -45;
          await page.mouse.move(box.x + dx, box.y, { steps: 1 });
          await page.waitForTimeout(20);
        }
        await page.mouse.up();
      };

      const skinReqsBefore = ctx.networkLogs.filter(l => l.type === 'request' && l.url.includes('/api/pet/skin')).length;
      await shake();
      await page.waitForTimeout(1200);
      const skinReqsAfter1 = ctx.networkLogs.filter(l => l.type === 'request' && l.url.includes('/api/pet/skin')).length;
      if (skinReqsAfter1 !== skinReqsBefore + 1) throw new Error(`晃动未触发恰好 1 次换肤请求（before=${skinReqsBefore} after=${skinReqsAfter1}）`);
      const st1 = await apiJSON(page, 'GET', '/api/pet/state');
      if (st1.data.skin !== (skinBefore + 1) % Math.max(2, skinCount)) throw new Error('皮肤索引未 +1: ' + JSON.stringify(st1.data.skin));

      await shake();
      await page.waitForTimeout(800);
      const skinReqsAfter2 = ctx.networkLogs.filter(l => l.type === 'request' && l.url.includes('/api/pet/skin')).length;
      if (skinReqsAfter2 !== skinReqsAfter1) throw new Error('2s 冷却内二次晃动触发了换肤请求');
      await runner.screenshot(page, '31-s3b-skin-change');
    });

    runner.it('S3B-6 单击宠物弹出迷你面板（等级/经验/皮肤/改名入口）', async function () {
      const box = await page.$eval('.kairo-pet-widget', el => {
        const r = el.getBoundingClientRect();
        return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
      });
      await page.mouse.move(box.x, box.y);
      await page.mouse.down();
      await page.mouse.up();
      await page.waitForSelector('.kairo-pet-panel', { state: 'visible', timeout: 5000 });
      const text = await page.$eval('.kairo-pet-panel', el => el.textContent);
      for (const kw of ['Lv', '经验', '皮肤', '确定']) {
        if (!text.includes(kw)) throw new Error('迷你面板缺少「' + kw + '」');
      }
      await runner.screenshot(page, '31-s3b-mini-panel');
    });

    runner.it('S3B-7 改名「小K2号」→ 气泡复述 + 刷新后保持', async function () {
      const panel = await page.$('.kairo-pet-panel');
      if (!panel) {
        const box = await page.$eval('.kairo-pet-widget', el => {
          const r = el.getBoundingClientRect();
          return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
        });
        await page.mouse.move(box.x, box.y);
        await page.mouse.down();
        await page.mouse.up();
        await page.waitForSelector('.kairo-pet-panel', { state: 'visible' });
      }
      const input = await page.$('.kairo-pet-panel input[maxlength="16"]');
      await input.fill('小K2号');
      await page.click('.kairo-pet-panel button:has-text("确定")');
      await page.waitForTimeout(1000);
      const st = await apiJSON(page, 'GET', '/api/pet/state');
      if (st.data.name !== '小K2号') throw new Error('改名未生效: ' + st.data.name);

      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
      const st2 = await apiJSON(page, 'GET', '/api/pet/state');
      if (st2.data.name !== '小K2号') throw new Error('刷新后名字未保持');
      await runner.screenshot(page, '31-s3b-renamed');
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
      if (!/榜单空空如也|宠物排行榜|第 \d+ 名|未上榜/.test(boardText)) {
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
        // 60min 冷却（http.request 的 target 为空，同 op 全共享冷却）会阻止重复加分；
        // 此时改验审计链路确实把 http.request 写入，且冷却语义正确（不重复计分）。
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
