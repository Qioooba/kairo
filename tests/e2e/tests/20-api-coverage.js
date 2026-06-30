'use strict';

/**
 * API-level E2E 测试
 * 直接调用后端 API 来覆盖 UI 难以触发的接口
 */

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';
const TEST_TOKEN = process.env.AUTH_TOKEN || 'test-token';

function register(runner, ctx) {
  const { page, baseUrl, networkLogs, screenshotsDir } = ctx;

  runner.describe('API-level E2E 测试', function () {
    runner.describe('认证与状态', function () {
      runner.it('/api/auth/status - 获取认证状态', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/auth/status`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/auth/status`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-auth-status');
        }
      });

      runner.it('/api/preferences - 获取/保存用户偏好', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/preferences`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/preferences`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-preferences');
        }
      });
    });

    runner.describe('配置管理', function () {
      runner.it('/api/config/export - 导出配置', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/config/export`, { headers });
          const text = await res.text();
          return { status: res.status, ok: res.ok, isYaml: text.indexOf('---') >= 0 || text.indexOf(':') >= 0 };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/config/export`, status: response.status });

        if (response.ok) {
          await runner.screenshot(page, '20-api-config-export');
        }
      });

      runner.it('/api/config/import - 导入非法 YAML', async function () {
        const invalidYaml = `systems:
  - name: "test
    invalid: yaml: here`;

        const response = await page.evaluate(async ({baseUrl, token, yaml}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'text/plain'
          };
          const res = await fetch(`${baseUrl}/api/config/import`, {
            method: 'POST',
            headers,
            body: yaml
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN, yaml: invalidYaml});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/config/import`, status: response.status });

        if (response.status >= 400) {
          await runner.screenshot(page, '20-api-config-import-invalid');
        }
      });
    });

    runner.describe('格式化接口', function () {
      runner.it('/api/format/timestamp - 时间戳转换', async function () {
        const response = await page.evaluate(async ({baseUrl}) => {
          const res = await fetch(`${baseUrl}/api/format/timestamp`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ timestamp: '1751155200' })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/format/timestamp`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-format-timestamp');
        }
      });

      runner.it('/api/format/cron-parse - Cron 解析', async function () {
        const response = await page.evaluate(async ({baseUrl}) => {
          const res = await fetch(`${baseUrl}/api/format/cron-parse`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ expression: '0 0 * * *' })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/format/cron-parse`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-format-cron');
        }
      });

      runner.it('/api/format/jsonpath - JSONPath 查询', async function () {
        const response = await page.evaluate(async ({baseUrl}) => {
          const res = await fetch(`${baseUrl}/api/format/jsonpath`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              json: '{"store":{"book":[{"title":"书1"},{"title":"书2"}]}}',
              path: '$.store.book[*].title'
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/format/jsonpath`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-format-jsonpath');
        }
      });
    });

    runner.describe('代码比对', function () {
      runner.it('/api/diff/compare - 文本 diff', async function () {
        const response = await page.evaluate(async ({baseUrl}) => {
          const res = await fetch(`${baseUrl}/api/diff/compare`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              left: 'line1\nline2\nline3',
              right: 'line1\nmodified\nline3',
              type: 'unified'
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/diff/compare`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-diff-compare');
        }
      });

      runner.it('/api/compare/folder-scan - 目录扫描', async function () {
        const testDir = require('path').join(require('os').tmpdir(), 'e2e-test-dir');
        require('fs').mkdirSync(testDir, { recursive: true });
        require('fs').writeFileSync(require('path').join(testDir, 'test.txt'), 'test content');

        const response = await page.evaluate(async ({baseUrl, token, dir}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/compare/folder-scan`, {
            method: 'POST',
            headers,
            body: JSON.stringify({ path: dir, maxDepth: 3, maxFiles: 100 })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN, dir: testDir});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/compare/folder-scan`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-compare-folder-scan');
        }
      });

      runner.it('/api/compare/file-diff - 文件 diff', async function () {
        const testFile1 = require('path').join(require('os').tmpdir(), 'e2e-test-file1.txt');
        const testFile2 = require('path').join(require('os').tmpdir(), 'e2e-test-file2.txt');
        require('fs').writeFileSync(testFile1, 'content 1');
        require('fs').writeFileSync(testFile2, 'content 2');

        const response = await page.evaluate(async ({baseUrl, token, file1, file2}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/compare/file-diff`, {
            method: 'POST',
            headers,
            body: JSON.stringify({ file1, file2 })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN, file1: testFile1, file2: testFile2});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/compare/file-diff`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-compare-file-diff');
        }
      });
    });

    runner.describe('HTTP 测试接口', function () {
      runner.it('/api/http/cases - 获取测试用例列表', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/http/cases`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/http/cases`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-http-cases');
        }
      });

      runner.it('/api/http/envs - 获取环境列表', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/http/envs`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/http/envs`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-http-envs');
        }
      });

      runner.it('/api/http/request - 发送 GET 请求', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/http/request`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              method: 'GET',
              url: `${baseUrl}/api/config`,
              headers: {},
              body: ''
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/http/request`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-http-request-get');
        }
      });

      runner.it('/api/http/request - 发送 POST 请求', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/http/request`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              method: 'POST',
              url: `${baseUrl}/api/format/json`,
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ action: 'format', data: '{"test":1}' })
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/http/request`, status: response.status });

        if (response.ok || response.status >= 400) {
          await runner.screenshot(page, '20-api-http-request-post');
        }
      });
    });

    runner.describe('Admin 接口', function () {
      runner.it('/api/admin/servers - 获取服务器列表', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/admin/servers`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/admin/servers`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-admin-servers');
        }
      });

      runner.it('/api/admin/openers - 获取外部程序列表', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/admin/openers`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/admin/openers`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-admin-openers');
        }
      });

      runner.it('/api/admin/download-retention - 获取下载保留设置', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/admin/download-retention`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/admin/download-retention`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-admin-retention');
        }
      });
    });

    runner.describe('WebSphere 接口', function () {
      runner.it('/api/credentials/has - 检查凭据是否存在', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/credentials/has`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/credentials/has`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-credentials-has');
        }
      });

      runner.it('/api/ssh/test - SSH 连接测试', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/ssh/test`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              host: '127.0.0.1',
              port: 2222,
              username: 'test',
              password: 'test'
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/ssh/test`, status: response.status });

        await runner.screenshot(page, '20-api-ssh-test');
      });

      runner.it('/api/logs/list/targets - 列出日志文件', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/logs/list/targets`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              host: '127.0.0.1',
              port: 2222,
              username: 'test',
              password: 'test',
              path: '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1'
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/logs/list/targets`, status: response.status });

        await runner.screenshot(page, '20-api-logs-list-targets');
      });

      runner.it('/api/logs/search/multi - 多条件搜索', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/logs/search/multi`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              host: '127.0.0.1',
              port: 2222,
              username: 'test',
              password: 'test',
              path: '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1',
              keywords: ['ERROR', 'Exception'],
              filePatterns: ['*.log']
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/logs/search/multi`, status: response.status });

        await runner.screenshot(page, '20-api-logs-search-multi');
      });
    });

    runner.describe('Files 接口', function () {
      runner.it('/api/files/list - 列出文件', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/files/list`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              host: '127.0.0.1',
              port: 2222,
              username: 'test',
              password: 'test',
              path: '/'
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/files/list`, status: response.status });

        await runner.screenshot(page, '20-api-files-list');
      });

      runner.it('/api/files/preview - 预览文件', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = {
            'Authorization': `Bearer ${token}`,
            'Content-Type': 'application/json'
          };
          const res = await fetch(`${baseUrl}/api/files/preview`, {
            method: 'POST',
            headers,
            body: JSON.stringify({
              host: '127.0.0.1',
              port: 2222,
              username: 'test',
              password: 'test',
              path: '/README.md'
            })
          });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'POST', url: `${baseUrl}/api/files/preview`, status: response.status });

        await runner.screenshot(page, '20-api-files-preview');
      });

      runner.it('/api/downloads/list - 下载历史', async function () {
        const response = await page.evaluate(async ({baseUrl, token}) => {
          const headers = { 'Authorization': `Bearer ${token}` };
          const res = await fetch(`${baseUrl}/api/downloads/list`, { headers });
          return { status: res.status, ok: res.ok };
        }, {baseUrl, token: TEST_TOKEN});

        networkLogs.push({ method: 'GET', url: `${baseUrl}/api/downloads/list`, status: response.status });

        if (response.status >= 200 && response.status < 500) {
          await runner.screenshot(page, '20-api-downloads-list');
        }
      });
    });
  });
}

module.exports = { register };
