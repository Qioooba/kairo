'use strict';
const assert = require('assert');

function register(runner, ctx) {
  const { page, baseUrl } = ctx;
  runner.describe('数据库写入回归', function () {
    runner.it('固定结果集目标、保留原值、隔离页签队列并发送完整导入文件', async function () {
      const writes = [], imports = [];
      const source = { id: 'review-db', name: 'Review DB', kind: 'oracle', username: 'APP', has_password: true, max_rows: 200, query_timeout_seconds: 30, max_result_bytes: 16777216, allow_ddl: true };
      await page.route('**/api/database/**', async route => {
        const url = new URL(route.request().url()), pathname = url.pathname;
        let body = { ok: true };
        if (pathname === '/api/database/sources') body.sources = [source];
        else if (pathname.endsWith('/sessions/restore')) body.session = null;
        else if (pathname.endsWith('/metadata/schemas')) body.schemas = ['APP'];
        else if (pathname.endsWith('/metadata/objects')) body.objects = [];
        else if (pathname.endsWith('/metadata/fields')) body.fields = [{ name: 'ID', primary_key: true }, { name: 'NAME' }, { name: 'NOTE' }];
        else if (pathname === '/api/database/query') {
          const data = [
            { type: 'meta', columns: [{ name: 'ID', database_type: 'NUMBER' }, { name: 'NAME', database_type: 'VARCHAR2' }, { name: 'NOTE', database_type: 'VARCHAR2' }] },
            { type: 'rows', rows: [[1, ' before ', null]] },
            { type: 'summary', summary: { rows: 1, elapsed_ms: 1, transaction_pending: false } }
          ].map(x => JSON.stringify(x)).join('\n') + '\n';
          return route.fulfill({ status: 200, contentType: 'application/x-ndjson', body: data });
        } else if (pathname === '/api/database/grid') {
          writes.push(route.request().postDataJSON());
          body.result = { rows_affected: 1, transaction_pending: true };
        } else if (pathname.endsWith('/transaction/status')) body.transaction = { active: false, pending: false };
        else if (pathname.endsWith('/import/preview')) body.preview = {
          table: { headers: ['ID', 'NAME'], rows: [['1', 'Alice']] }, total_rows: 150, preview_rows: 1, truncated: true,
          fields: [{ name: 'ID' }, { name: 'NAME' }], suggested_mapping: [{ target: 'ID', type: 'int64' }, { target: 'NAME', type: 'string' }]
        };
        else if (pathname.endsWith('/import/apply')) {
          imports.push(route.request().postDataJSON());
          body.result = { processed: 150, rows_affected: 150, rows: [], transaction_pending: true };
        }
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
      });
      try {
        await page.goto(baseUrl + '/#/home');
        await page.evaluate(() => { localStorage.removeItem('kairo_db_sessions_backup'); });
        await page.goto(baseUrl + '/#/database');
        await page.reload();
        await page.waitForSelector('#db-sql');
        await page.fill('#db-sql', 'SELECT * FROM "APP"."USERS"');
        await page.click('#db-run');
        await page.waitForSelector('#db-result-body td[data-col="1"]');
        await page.click('#db-toggle-edit');
        await page.dblclick('#db-result-body td[data-col="1"]');
        await page.fill('#db-result-body td[data-col="1"] input', 'after');
        await page.keyboard.press('Enter');
        await page.fill('#db-sql', 'SELECT * FROM WRONG_TABLE');
        await page.click('#db-btn-commit');
        await page.waitForFunction(() => !window.Kairo.database.getActiveSession().transactionPending && document.getElementById('db-btn-commit').disabled);
        assert.strictEqual(writes.length, 1);
        assert.strictEqual(writes[0].table, 'USERS');
        assert.strictEqual(writes[0].schema, 'APP');
        assert.strictEqual(writes[0].mutations[0].values.NAME, 'after');
        assert.strictEqual(writes[0].mutations[0].original.NAME, ' before ');
        assert.strictEqual(writes[0].mutations[0].original.NOTE, null);
        assert.deepStrictEqual(writes[0].mutations[0].primary_key, ['ID']);
		await page.fill('#db-sql', 'SELECT * FROM "APP"."USERS"');
		await page.click('#db-run');
		await page.waitForSelector('#db-result-body td[data-col="1"]');

        const originalTab = await page.$eval('.db-editor-tab.active', el => el.dataset.tab);
        await page.click('#db-pro-grid-delete');
        await page.getByRole('button', { name: '加入删除队列', exact: true }).click();
        assert.strictEqual(await page.evaluate(() => window.Kairo.databaseFeatures.getPendingGridMutations().length), 1);
        await page.click('#db-tab-add');
        await page.fill('#db-sql', 'SELECT * FROM "APP"."OTHER_TABLE"');
        await page.click('#db-run');
        await page.waitForSelector('#db-result-body td[data-col="1"]');
        assert.strictEqual(await page.evaluate(() => window.Kairo.databaseFeatures.getPendingGridMutations().length), 0);
        await page.click('.db-editor-tab[data-tab="' + originalTab + '"]');
        assert.strictEqual(await page.evaluate(() => window.Kairo.databaseFeatures.getPendingGridMutations().length), 1);
        await page.click('#db-pro-grid-apply');
        await page.waitForFunction(() => window.Kairo.databaseFeatures.getPendingGridMutations().length === 0);
        assert.strictEqual(writes[1].table, 'USERS');
        assert.strictEqual(writes[1].mutations[0].original.NOTE, null);
        assert.strictEqual(writes[1].mutations[0].original.ID, 1);

        await page.evaluate(() => window.Kairo.databaseFeatures.openImportWizard());
        const csv = 'ID,NAME\n' + Array.from({ length: 150 }, (_, i) => (i + 1) + ',Alice').join('\n');
        await page.setInputFiles('#db-pro-import-file', { name: 'regression.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) });
        await page.waitForSelector('[data-import-map]');
        await page.getByRole('button', { name: '提交导入', exact: true }).click();
        await page.waitForSelector('#db-pro-import-results');
        assert.strictEqual(imports.length, 1);
        assert.strictEqual(imports[0].rows, undefined);
        assert.strictEqual(Buffer.from(imports[0].data_base64, 'base64').toString(), csv);
        assert.strictEqual(imports[0].format, 'csv');
        await runner.screenshot(page, '40-database-write-regression');
      } finally { await page.unroute('**/api/database/**'); }
    });
  });
}
module.exports = { register };
