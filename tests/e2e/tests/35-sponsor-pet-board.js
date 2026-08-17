'use strict';

/**
 * 35-sponsor-pet-board.js — review-2026-08 方案 02
 * S7 sponsor 页（A1+B 批交织，X2）
 *
 * 源码事实（以实际代码为准，方案 S7 建议值有偏差处在此记录）：
 *   - 5min 缓存：当前 handlers_sponsor.go 是「实时查询 + 失败兜底缓存」，没有 TTL 缓存
 *     （工作区已把 de0275f 的 5min 缓存改掉）。所以「二次进页无重复请求」改为验证
 *     「榜单正确渲染 + updated_at Java DATETIME 透传不崩」（b5f88e9 语义）。
 *   - 防重提交：de0275f 的 requestInFlight 锁在 WebService 发送按钮，sponsor 页无提交表单，
 *     S7-3 改为验证 WebService 发送按钮双击不重复提交（见 04-websphere 回归）。
 *   - 宠物榜未配置端点时的降级：pet_leaderboard 端点未配置 → /api/pet/sync 返 502
 *     → 前端回退 GET /api/pet/leaderboard（本地缓存）→ 显示「我的排名卡片 + 榜单空空如也」。
 *   - 依赖：实例宠物已解锁（31-pet 先跑）；mock-sponsor-server 在 18093。
 */

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

const INTERNAL_MARKERS = ['66.0.34', '66.2.43', '9080', 'httpInterface', 'TEST_TOKEN_123'];

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('S7 sponsor 页 + 宠物榜 tab（A1+B 交织，X2）', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '/#/sponsor', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1800);
      // suite 连续导航时偶发 hash 漂移（与 S5 同源），漂走后重新 goto 兜底
      const h = await page.evaluate(() => location.hash).catch(() => '');
      if (h !== '#/sponsor') {
        await page.goto(baseUrl + '/#/sponsor', { waitUntil: 'domcontentloaded' });
        await page.waitForTimeout(1500);
      }
    });

    runner.it('S7-1 武林榜渲染：昵称·真名前缀 + 杯数明细 + 奖杯 SVG + updated_at 透传', async function () {
      if (process.env.SPONSOR_OFFLINE === '1') {
        runner.skipTest('离线模式（mock 已停），跳过正常链路验证');
        return;
      }
      // S3B-8/S7-4 会切到宠物 tab 并写 sessionStorage 记忆，默认 tab 可能不是武林榜；
      // 先切回武林 tab 再验证榜单（避免 15s 等不到张三）。
      const wulinBtn = await page.$('.board-tab:not(.pet-board-tab)');
      if (wulinBtn && await wulinBtn.isVisible()) {
        const isActive = await wulinBtn.evaluate(el => el.classList.contains('active'));
        if (!isActive) {
          await wulinBtn.click();
          await page.waitForTimeout(800);
        }
      }
      await page.waitForFunction(() => {
        const body = document.body.innerText;
        return /张三|李四|王五/.test(body);
      }, null, { timeout: 15000 }).catch(async (e) => {
        const dbg = await page.evaluate(() => ({
          hash: location.hash,
          bodySnippet: document.body.innerText.substring(0, 400),
          sponsorReqs: performance.getEntriesByType('resource').filter(r => r.name.includes('sponsor')).map(r => r.name + ':' + r.duration.toFixed(0) + 'ms'),
        }));
        const net = ctx.networkLogs.filter(l => l.url.includes('/api/sponsor') && l.type === 'response').slice(-3).map(l => l.status);
        throw new Error('榜单 15s 未渲染 | ' + JSON.stringify(dbg) + ' | sponsorResp=' + JSON.stringify(net));
      });
      await page.waitForTimeout(300);
      const bodyText = await page.evaluate(() => document.querySelector('.honor-card, .board-container, #view') ?
        document.body.innerText : '');
      if (!/张三|李四|王五/.test(bodyText)) throw new Error('mock 返回的真实姓名未渲染（张三/李四/王五）');
      if (!/武林神话|一代武神/.test(bodyText)) throw new Error('昵称前缀未渲染（NICKNAMES 拼名）');
      if (!/库迪|瑞幸|奶茶/.test(bodyText)) throw new Error('品牌杯数明细未渲染');

      const hasTrophy = await page.evaluate(() => {
        const imgs = Array.from(document.querySelectorAll('img[src*="trophy"]'));
        return imgs.length >= 2;
      });
      if (!hasTrophy) throw new Error('Top3 奖杯 SVG 未渲染');

      const lb = await apiJSON(page, 'GET', '/api/sponsor/leaderboard');
      if (lb.status !== 200 || !lb.data || !lb.data.ok) throw new Error('leaderboard API 异常: ' + JSON.stringify(lb.data));
      const e0 = lb.data.entries[0];
      if (!e0 || !/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d$/.test(e0.updated_at || '')) {
        throw new Error('updated_at 非 Java DATETIME 字符串透传: ' + JSON.stringify(e0));
      }
      for (const e of lb.data.entries) {
        if (typeof e.rank !== 'number' || typeof e.cotti !== 'number') {
          throw new Error('数字字段应为 JSON number（非 string）: ' + JSON.stringify(e));
        }
      }
      const raw = JSON.stringify(lb.data);
      for (const m of INTERNAL_MARKERS) {
        if (raw.includes(m)) throw new Error('API 响应泄露内部地址/密钥标记: ' + m);
      }
      await runner.screenshot(page, '35-s7-1-leaderboard');
    });

    runner.it('S7-2 断 mock 后刷新：搞笑提示 + 页面无内部地址泄露', async function () {
      if (process.env.SPONSOR_OFFLINE !== '1') {
        runner.skipTest('需要先停掉 mock-sponsor-server 并设 SPONSOR_OFFLINE=1 才能执行');
        return;
      }
      const before = await apiJSON(page, 'GET', '/api/sponsor/leaderboard');
      if (before.status === 200 && before.data && before.data.ok && before.data.entries && before.data.entries.length) {
        runner.skipTest('mock 仍在线（有兜底缓存导致旧数据返回），无法构造纯失败态');
        return;
      }
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);
      await page.waitForFunction(() => {
        const body = document.body.innerText;
        return /买咖啡|休刊|闭关修榜|归隐山林|天机不可泄露/.test(body);
      }, null, { timeout: 20000 });
      const bodyText = await page.evaluate(() => document.body.innerText);
      const quipHits = /买咖啡|休刊|闭关修榜|归隐山林|天机不可泄露/.test(bodyText);
      if (!quipHits) throw new Error('未显示搞笑提示语: ' + bodyText.substring(0, 200));
      const hasRetry = await page.$('button:has-text("再试一次")');
      if (!hasRetry) throw new Error('缺少「再试一次」按钮');

      const leakInDom = INTERNAL_MARKERS.filter(m => bodyText.includes(m));
      if (leakInDom.length) throw new Error('页面 DOM 泄露内部地址标记: ' + leakInDom.join(','));
      const netLog = ctx.networkLogs.filter(l => l.type === 'response');
      const leakInNet = netLog.filter(l => INTERNAL_MARKERS.some(m => l.url.includes(m)));
      if (leakInNet.length) throw new Error('网络日志中出现内部地址请求: ' + leakInNet[0].url);
      await runner.screenshot(page, '35-s7-2-offline-quip');
    });

    runner.it('S7-4 宠物已解锁：出现 [武林排行榜][宠物排行榜] 双 tab；宠物榜本地降级（X5）', async function () {
      const st = await apiJSON(page, 'GET', '/api/pet/state');
      if (!st.data || !st.data.enabled) throw new Error('前置：宠物应已解锁（31 先跑）');
      const wulinBtn = await page.$('.board-tab:not(.pet-board-tab)');
      const petBtn = await page.$('.pet-board-tab');
      if (!wulinBtn || !petBtn) throw new Error('双 tab 缺失');
      if (!(await petBtn.isVisible())) throw new Error('宠物榜 tab 不可见');

      const netBefore = ctx.networkLogs.length;
      await petBtn.click();
      await page.waitForTimeout(2500);

      const boardText = await page.$eval('.pet-board', el => el.textContent).catch(() => '');
      if (!boardText) throw new Error('宠物榜容器无内容');
      if (!/我的宠物|榜单空空如也|宠物排行榜|第 \d+ 名|未上榜/.test(boardText)) {
        throw new Error('宠物榜内容异常: ' + boardText.substring(0, 160));
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
      if (tab !== 'pet') throw new Error('sessionStorage 未记住宠物 tab: ' + tab);
      await runner.screenshot(page, '35-s7-4-pet-board');
    });

    runner.it('S7-5 未解锁时无宠物榜 tab（与 31-S3A 互证，正向重验）', async function () {
      const st = await apiJSON(page, 'GET', '/api/pet/state');
      if (!st.data || !st.data.enabled) throw new Error('本实例应已解锁，此断言依赖 31 的解锁前状态验证');
      // 此实例已解锁，正向验证由 31-S3A-1 完成；这里只确认双 tab 切换记忆不丢
      await page.evaluate(() => { location.hash = '#/sponsor'; });
      await page.waitForTimeout(1500);
      const petBtn = await page.$('.pet-board-tab');
      if (!petBtn || !(await petBtn.isVisible())) throw new Error('解锁后宠物榜 tab 应在（refreshBoardTab 联动）');
      const tab = await page.evaluate(() => sessionStorage.getItem('kairo:pet:boardtab'));
      if (tab !== 'pet') throw new Error('再次进页 tab 记忆丢失: ' + tab);
      await runner.screenshot(page, '35-s7-5-tab-memory');
    });
  });
}

module.exports = { register };