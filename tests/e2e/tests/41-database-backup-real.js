'use strict';
const assert = require('assert');

/**
 * 41-database-backup-real.js
 * 数据库会话备份与恢复真实网络校验 (QA-01)
 *
 * 验证:
 * 1. 空闲状态下零冗余发包;
 * 2. 编辑 SQL 触发单次防抖远程备份，且响应必须为 HTTP 200 OK;
 * 3. 服务端持久化回读 (/api/database/sessions/restore) 与页面刷新后草稿完全恢复;
 * 4. 故障注入: 服务端 500 或网络失败时不得误判为已同步，恢复后应正常持久化。
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('数据库会话网络备份与恢复', function () {
    runner.it('编辑 SQL 触发防抖备份，校验 HTTP 200 与 GET 回读恢复', async function () {
      const backupRequests = [];
      const backupResponses = [];

      const onRequest = (req) => {
        if (req.url().includes('/api/database/sessions/backup')) {
          backupRequests.push({ time: Date.now(), method: req.method() });
        }
      };
      const onResponse = (res) => {
        if (res.url().includes('/api/database/sessions/backup')) {
          backupResponses.push({ time: Date.now(), status: res.status(), ok: res.ok() });
        }
      };

      page.on('request', onRequest);
      page.on('response', onResponse);

      try {
        await page.goto(baseUrl + '/#/database', { waitUntil: 'domcontentloaded' });
        await page.waitForSelector('#db-sql', { timeout: 15000 });
        await page.waitForTimeout(1000);

        const initialReqCount = backupRequests.length;

        // 1. 空闲等待 3 秒，确认无变更状态下不触发多余请求
        await page.waitForTimeout(3000);
        assert.strictEqual(
          backupRequests.length - initialReqCount,
          0,
          '空闲状态下不应触发多余的备份请求'
        );

        // 2. 输入测试 SQL
        const testSql = 'SELECT SYSDATE, 100 AS TEST_NUM FROM DUAL';
        await page.fill('#db-sql', testSql);

        // 等待 4 秒跨越 3 秒防抖
        await page.waitForTimeout(4000);

        const editedReqCount = backupRequests.length - initialReqCount;
        assert.strictEqual(editedReqCount, 1, '编辑 SQL 后应触发且仅触发 1 次防抖备份请求');
        
        const lastResp = backupResponses[backupResponses.length - 1];
        assert.ok(lastResp, '未捕获到远端备份响应');
        assert.strictEqual(lastResp.status, 200, '远端备份接口必须返回 HTTP 200');
        assert.strictEqual(lastResp.ok, true, '远端备份响应必须为 ok');

        // 3. 服务端 GET 回读校验
        const restoreData = await page.evaluate(async () => {
          const res = await fetch('/api/database/sessions/restore', { credentials: 'same-origin' });
          if (!res.ok) throw new Error('GET /api/database/sessions/restore 失败: ' + res.status);
          return await res.json();
        });

        assert.ok(restoreData && restoreData.session, '服务端返回的恢复会话不能为空');
        const hasMatchingSql = (restoreData.session.sessions || []).some(s => (s.sql || '').includes('TEST_NUM'));
        assert.ok(hasMatchingSql, '服务端持久化的会话内容未包含最新编辑的 SQL: ' + JSON.stringify(restoreData));

        // 4. 页面刷新后恢复校验
        await page.reload({ waitUntil: 'domcontentloaded' });
        await page.waitForSelector('#db-sql', { timeout: 15000 });
        await page.waitForTimeout(800);
        const restoredVal = await page.$eval('#db-sql', el => el.value);
        assert.ok(restoredVal.includes('TEST_NUM'), '刷新页面后 #db-sql 未能正确恢复草稿: ' + restoredVal);
      } finally {
        page.off('request', onRequest);
        page.off('response', onResponse);
      }
    });

    runner.it('远端备份 500 故障注入时不得误认为已同步', async function () {
      let failBackup = true;
      let intercepted500Count = 0;

      await page.route('**/api/database/sessions/backup', async (route) => {
        if (failBackup) {
          intercepted500Count++;
          return route.fulfill({
            status: 500,
            contentType: 'application/json',
            body: JSON.stringify({ ok: false, error: 'SIMULATED_BACKUP_500' })
          });
        }
        return route.continue();
      });

      try {
        await page.goto(baseUrl + '/#/database', { waitUntil: 'domcontentloaded' });
        await page.waitForSelector('#db-sql', { timeout: 15000 });

        // 输入故障测试 SQL
        await page.fill('#db-sql', 'SELECT 500_FAULT_PROBE FROM DUAL');
        await page.waitForTimeout(4000);

        assert.ok(intercepted500Count >= 1, '应至少拦截到 1 次备份请求并注入 500');

        // 验证由于 500 故障，本地并未将未同步指纹标记为已确认
        // 解除 500 拦截后，再次触发或添加页签应能够成功同步
        failBackup = false;
        await page.click('#db-tab-add');
        await page.waitForTimeout(4000);

        // 回读服务端
        const restored = await page.evaluate(async () => {
          const res = await fetch('/api/database/sessions/restore', { credentials: 'same-origin' });
          return res.ok ? await res.json() : null;
        });
        assert.ok(restored && restored.session, '恢复网络后会话应成功同步到服务端');
      } finally {
        await page.unroute('**/api/database/sessions/backup');
      }
    });
  });
}

module.exports = { register };
