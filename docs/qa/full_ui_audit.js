'use strict';

/*
 * Kairo 全站 UI 审计
 *
 * 目标：
 *   - 遍历所有菜单/页面
 *   - 扫描可见按钮、输入框、选择框、文本域、可点击卡片
 *   - 检查尺寸、可见性、悬停、聚焦、基础点击/输入/切换
 *   - 在 mock API 下尽量把页面功能路径跑通，输出 JSON 报告
 *
 * 运行：
 *   node docs/qa/full_ui_audit.js
 */

const http = require('http');
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const ROOT = path.join(__dirname, '..', '..', 'web');
const BASE = 'http://127.0.0.1:18090';
const OUT = path.join(__dirname, 'full-ui-audit-report.json');

const VIEWPORT = { width: 1440, height: 980 };
const ROUTES = [
  { hash: '#/home', key: 'home', ready: '.tool-card' },
  { hash: '#/websphere', key: 'websphere', ready: '#ws-sys' },
  { hash: '#/files', key: 'files', ready: '#files-sys' },
  { hash: '#/formatter', key: 'formatter', ready: '#fmt-in' },
  { hash: '#/http', key: 'http', ready: '.http2-layout' },
  { hash: '#/commands', key: 'commands', ready: '.cmd-card' },
  { hash: '#/diagnostics', key: 'diagnostics', ready: 'button:has-text("重新自检")' },
  { hash: '#/config', key: 'config', ready: '.sys-block' },
  { hash: '#/downloads', key: 'downloads', ready: 'select' },
  { hash: '#/history', key: 'history', ready: 'select' },
  { hash: '#/timestamp', key: 'timestamp', ready: '#ts-in' },
  { hash: '#/cron', key: 'cron', ready: '#cron-in' },
  { hash: '#/jsonpath', key: 'jsonpath', ready: '#jp-in' },
  { hash: '#/compare', key: 'compare', ready: '#cmp-left' },
];

const mock = {
  config: {
    app: {
      name: 'Kairo（审计）',
      host: '127.0.0.1',
      port: 18092,
      download_dir: './downloads',
      log_dir: './logs',
      data_dir: './data',
      listen_addr: '127.0.0.1:18092',
      credential_store: 'keyring',
      free_file_browser: true,
      free_file_roots: ['/opt/IBM/WebSphere', '/var/log'],
      ssh_compat_profile: 'auto',
      ssh_debug: false,
      ssh_traffic_dump: false,
      ssh_log_max_mb: 20,
      ssh_log_keep: 3,
    },
    systems: [
      {
        name: '信贷生产（模拟）',
        description: 'Mock',
        servers: [
          {
            name: 'mock-node-1',
            host: '127.0.0.1',
            port: 2225,
            username: 'test',
            auth_type: 'password',
            log_dirs: [
              { name: 'AppSrv01 server1', path: '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1', patterns: ['SystemOut*.log', 'SystemErr*.log', '*.log'], encoding: 'utf-8' },
              { name: 'AppSrv01 server1 GBK', path: '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1', patterns: ['gbk_*.log'], encoding: 'gbk' },
            ],
          },
        ],
      },
    ],
    search: { default_latest_files: 3, max_matches: 200, default_context_lines: 30, timeout_seconds: 30, max_concurrency: 2 },
  },
  prefs: { tail: { highlights: [{ keyword: 'ERROR', bg: '#5f1f1f', fg: '#ffffff' }] } },
  http: {
    cases: [
      { id: 'c1', group: 'default', name: '健康检查', method: 'GET', url: 'https://example.com/health', headers: { Accept: 'application/json' }, body: '', timeout_ms: 15000, follow_redirect: true, insecure_tls: false },
      { id: 'c2', group: 'default', name: '创建', method: 'POST', url: 'https://example.com/items', headers: { 'Content-Type': 'application/json' }, body: '{"name":"x"}', timeout_ms: 15000, follow_redirect: true, insecure_tls: false, body_mode: 'raw', body_type: 'json' },
    ],
    envs: [{ name: 'default', vars: [{ key: 'baseUrl', value: 'https://example.com', enabled: true }] }],
  },
  downloads: [
    { name: 'SystemOut-20260625.log', size_human: '12.4 MB', downloaded_at: '2026-06-25 10:00:00', system: '信贷生产（模拟）', server: 'mock-node-1', dir: '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1', file: 'SystemOut-20260625.log', kind: 'log' },
    { name: 'SystemErr-20260625.log', size_human: '1.2 MB', downloaded_at: '2026-06-25 10:01:00', system: '信贷生产（模拟）', server: 'mock-node-1', dir: '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1', file: 'SystemErr-20260625.log', kind: 'log' },
  ],
  audit: [
    { ts: '2026-06-25 10:00:00', op: 'ssh.test', system: '信贷生产（模拟）', server: 'mock-node-1', result: 'ok' },
    { ts: '2026-06-25 10:01:00', op: 'logs.search', system: '信贷生产（模拟）', server: 'mock-node-1', result: 'ok' },
  ],
};

function staticType(file) {
  if (file.endsWith('.css')) return 'text/css; charset=utf-8';
  if (file.endsWith('.html')) return 'text/html; charset=utf-8';
  if (file.endsWith('.js')) return 'application/javascript; charset=utf-8';
  return 'application/octet-stream';
}

function startStaticServer() {
  const server = http.createServer((req, res) => {
    const p = decodeURIComponent(req.url.split('?')[0]);
    const send = (status, body, type = 'text/plain; charset=utf-8') => {
      res.writeHead(status, { 'Content-Type': type });
      res.end(body);
    };
    if (p === '/' || p === '/index.html') return send(200, fs.readFileSync(path.join(ROOT, 'index.html')), 'text/html; charset=utf-8');
    if (p === '/preview.html' || p === '/tail.html') return send(200, fs.readFileSync(path.join(ROOT, p.slice(1))), 'text/html; charset=utf-8');
    if (p.startsWith('/static/')) {
      const rel = p.replace('/static/', '');
      const file = path.join(ROOT, rel);
      if (fs.existsSync(file)) return send(200, fs.readFileSync(file), staticType(file));
    }
    if (p.startsWith('/downloads/')) return send(200, 'download mocked', 'text/plain; charset=utf-8');
    return send(404, 'not found');
  });
  return new Promise(resolve => server.listen(18090, '127.0.0.1', () => resolve(server)));
}

function json(res, body, status = 200) {
  return res.fulfill({ status, contentType: 'application/json; charset=utf-8', body: JSON.stringify(body) });
}

function text(res, body, status = 200, contentType = 'text/plain; charset=utf-8') {
  return res.fulfill({ status, contentType, body });
}

function routePath(url) {
  return decodeURIComponent(new URL(url).pathname);
}

function buildDiagnostics() {
  return {
    generated_at: new Date().toISOString(),
    issues: [],
    app: {
      name: mock.config.app.name,
      listen_addr: mock.config.app.listen_addr,
      host: mock.config.app.host,
      port: mock.config.app.port,
      credential_store: mock.config.app.credential_store,
      free_file_browser: mock.config.app.free_file_browser,
      free_file_roots: mock.config.app.free_file_roots,
      ssh_compat_profile: mock.config.app.ssh_compat_profile,
      ssh_debug: mock.config.app.ssh_debug,
      ssh_traffic_dump: mock.config.app.ssh_traffic_dump,
      ssh_log_max_mb: mock.config.app.ssh_log_max_mb,
      ssh_log_keep: mock.config.app.ssh_log_keep,
      download_dir: mock.config.app.download_dir,
      log_dir: mock.config.app.log_dir,
      data_dir: mock.config.app.data_dir,
      config_path: '/Users/qi/Projects/kairo/config.yaml',
    },
    build: { go_version: 'go1.20', goos: 'darwin', goarch: 'arm64', crypto_ssh_version: 'v0.31.0' },
    runtime: { data_dir_writable: true, download_dir_writable: true, log_dir_writable: true, working_dir: ROOT, pid: 12345, num_goroutine: 6 },
    tools: { find: { found: true, path: '/usr/bin/find', version: 'BSD find' }, grep: { found: true, path: '/usr/bin/grep', version: 'GNU grep' }, sed: { found: true, path: '/usr/bin/sed', version: 'BSD sed' }, tail: { found: true, path: '/usr/bin/tail', version: 'BSD tail' }, unzip: { found: true, path: '/usr/bin/unzip', version: 'UnZip 6.0' }, ssh: { found: true, path: '/usr/bin/ssh', version: 'OpenSSH_9.0' }, find_supports_printf: true },
    servers: mock.config.systems[0].servers.map(srv => ({
      system: mock.config.systems[0].name,
      server: srv.name,
      host: srv.host,
      port: srv.port,
      elapsed_ms: 3,
      dns: { ok: true, message: 'ok' },
      tcp: { ok: true, message: 'ok' },
    })),
  };
}

function setupMockApi(page) {
  page.on('dialog', async d => { try { await d.accept(); } catch (_) {} });
  page.addInitScript(() => {
    class FakeEventSource {
      constructor(url) {
        this.url = url;
        this.readyState = 0;
        this._listeners = {};
        this.onmessage = null;
        this.onerror = null;
        setTimeout(() => {
          const msg = (data, type) => {
            const ev = { data: JSON.stringify(data), type };
            if (this.onmessage) this.onmessage(ev);
            (this._listeners[type || 'message'] || []).forEach(fn => fn(ev));
          };
          msg({ kind: 'progress', message: 'mock', written: 1, total: 1 });
          msg({ kind: 'done', ok: true, downloads: [{ name: 'mock.txt', abs_path: '/tmp/mock.txt' }], result: { ok: true } }, 'done');
        }, 50);
      }
      addEventListener(name, fn) { (this._listeners[name] = this._listeners[name] || []).push(fn); }
      close() { this.readyState = 2; }
    }
    window.EventSource = FakeEventSource;
    const oldOpen = window.open;
    window.open = function () {
      try { return oldOpen.apply(window, arguments); } catch (_) { return { closed: false }; }
    };
  });

  page.route('**/api/**', async route => {
    const req = route.request();
    const p = routePath(req.url());
    const method = req.method();
    const body = req.postDataJSON ? (() => { try { return req.postDataJSON(); } catch (_) { return null; } })() : null;

    if (p === '/api/config' && method === 'GET') return json(route, mock.config);
    if (p === '/api/preferences' && method === 'GET') return json(route, mock.prefs);
    if (p === '/api/preferences' && method === 'PUT') {
      mock.prefs = body || mock.prefs;
      return json(route, mock.prefs);
    }
    if (p === '/api/http/cases' && method === 'GET') return json(route, { cases: mock.http.cases });
    if (p === '/api/http/cases' && method === 'POST') return json(route, { ok: true, id: (body && body.case && body.case.id) || 'c-new' });
    if (p === '/api/http/cases' && method === 'DELETE') return json(route, { ok: true });
    if (p === '/api/http/envs' && method === 'GET') return json(route, { envs: mock.http.envs });
    if (p === '/api/http/envs' && method === 'POST') return json(route, { ok: true });
    if (p === '/api/http/request' && method === 'POST') return json(route, { status: 200, statusText: 'OK', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ ok: true, echo: body && body.url }), elapsed_ms: 32 });
    if (p === '/api/diagnostics' && method === 'GET') return json(route, buildDiagnostics());
    if (p === '/api/audit/recent' && method === 'GET') return json(route, { records: mock.audit });
    if (p === '/api/downloads/list' && method === 'GET') return json(route, { files: mock.downloads, count: mock.downloads.length, total_human: '13.6 MB', folder: '/mock/downloads' });
    if (p === '/api/downloads/open-dir' && method === 'POST') return json(route, { ok: true });
    if (p.startsWith('/api/downloads/') && method === 'DELETE') return json(route, { ok: true });
    if (p === '/api/local/open-with' && method === 'POST') return json(route, { ok: true });
    if (p === '/api/credentials/has' && method === 'GET') return json(route, { mode: 'keyring', has: true });
    if (p === '/api/ssh/test' && method === 'POST') return json(route, { ok: true });
    if (p === '/api/files/list' && method === 'POST') {
      const pathName = (body && body.path) || '/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1';
      return json(route, {
        path: pathName,
        parent: pathName === '/' ? '/' : path.dirname(pathName),
        entries: [
          { isDir: false, name: 'SystemOut.log', size: 1024 * 1024, mtime: '2026-06-25 10:00:00', mode: '-rw-r--r--' },
          { isDir: false, name: 'SystemErr.log', size: 256 * 1024, mtime: '2026-06-25 10:00:00', mode: '-rw-r--r--' },
          { isDir: true, name: 'nested', size: 0, mtime: '2026-06-25 10:00:00', mode: 'drwxr-xr-x' },
        ],
      });
    }
    if (p === '/api/files/preview' && method === 'POST') return json(route, { ok: true, text: 'preview mock\nline2', encoding: 'utf-8', binary: false, truncated: false });
    if (p === '/api/files/download' && method === 'POST') return json(route, { id: 'dl-mock' });
    if (p.startsWith('/api/files/download/') && (method === 'POST' || method === 'GET')) return json(route, { ok: true });
    if (p === '/api/logs/list/targets' && method === 'POST') {
      const targets = (body && body.targets) || [];
      return json(route, {
        servers: targets.map(t => ({
          server: t.server,
          dir: t.dir,
          ok: true,
          files: [
            { name: 'SystemOut.log', full_path: t.dir + '/SystemOut.log', size: 1024 * 1024, mod_time: '2026-06-25 10:00:00' },
            { name: 'SystemErr.log', full_path: t.dir + '/SystemErr.log', size: 256 * 1024, mod_time: '2026-06-25 10:00:00' },
          ],
        })),
      });
    }
    if (p === '/api/logs/search/multi' && (method === 'GET' || method === 'POST')) {
      return json(route, {
        results: [
          { server: 'mock-node-1', ok: true, elapsed_ms: 12, hits: [{ file: 'SystemOut.log', line_no: 1, line: 'ERROR mock line', ctx: [] }] },
        ],
      });
    }
    if (p === '/api/logs/context' && method === 'POST') return json(route, { lines: ['1 foo', '2 ERROR mock', '3 bar'] });
    if (p === '/api/logs/download-latest' && method === 'POST') return json(route, { id: 'log-dl-mock' });
    if (p === '/api/logs/tail/start' && method === 'POST') return json(route, { id: 'tail-mock' });
    if (p.startsWith('/api/logs/tail/') && (method === 'POST' || method === 'GET')) return json(route, { ok: true });
    if (p === '/api/format/json' && method === 'POST') return json(route, { output: JSON.stringify({ ok: true }, null, 2) });
    if (p === '/api/format/xml' && method === 'POST') return json(route, { output: '<root>\n  <ok>true</ok>\n</root>' });
    if (p === '/api/format/yaml' && method === 'POST') return json(route, { output: 'ok: true\n' });
    if (p === '/api/format/url-form' && method === 'POST') return json(route, { output: 'a=1&b=2' });
    if (p === '/api/format/timestamp' && method === 'POST') return json(route, { res: { input_type: 'unix_sec', utc: '2026-06-25T00:00:00Z', local: '2026-06-25 08:00:00', target: '2026-06-25 08:00:00', unix_sec: 1772390400, unix_milli: 1772390400000, year: 2026, month: 6, day: 25, hour: 8, minute: 0, second: 0, weekday_cn: '周四' } });
    if (p === '/api/format/cron-parse' && method === 'POST') return json(route, { res: { valid: true, has_seconds: false, field_desc: 'mock', next_runs: [{ cn: '2026-06-25 09:00:00', unix: 1772394000 }], prev_runs: [{ cn: '2026-06-24 09:00:00', unix: 1772307600 }] } });
    if (p === '/api/format/jsonpath' && method === 'POST') return json(route, { ok: true, found: true, result: 'mock-result', type: 'string', is_array: false, elapsed_ms: 3 });
    if (p === '/api/admin/openers' && method === 'GET') return json(route, { openers: [] });
    if (p === '/api/admin/servers' && method === 'GET') return json(route, { systems: mock.config.systems });
    if (p === '/api/config/export' && method === 'GET') return text(route, 'app:\n  name: mock\n', 200, 'text/yaml; charset=utf-8');
    if (p === '/api/config/import' && method === 'POST') return json(route, { ok: true });
    if (p === '/api/http/request' && method === 'POST') return json(route, { status: 200, body: '{"ok":true}', headers: { 'content-type': 'application/json' }, elapsed_ms: 10 });
    if (p === '/api/diff/compare' && method === 'POST') return json(route, { unified: '@@ -1 +1 @@\n-a\n+b\n', lines: [] });
    if (p === '/api/compare' && method === 'POST') return json(route, { unified: '@@ -1 +1 @@\n-a\n+b\n', lines: [] });
    if (p === '/api/format/markdown' && method === 'POST') return json(route, { output: body && body.input || '' });
    return json(route, { ok: true });
  });
}

async function collectMetrics(page) {
  const selectors = [
    'button', 'input', 'select', 'textarea', 'a.nav-item', '[role="button"]', '.tool-card', '.cmd-card', '.http2-case-row',
  ];
  return page.evaluate((sels) => {
    const items = [];
    sels.forEach(sel => {
      document.querySelectorAll(sel).forEach((el, idx) => {
        const style = getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        if (style.display === 'none' || style.visibility === 'hidden' || rect.width === 0 || rect.height === 0) return;
        items.push({
          sel,
          idx,
          text: (el.textContent || '').trim().slice(0, 80),
          tag: el.tagName.toLowerCase(),
          w: Math.round(rect.width),
          h: Math.round(rect.height),
          cursor: style.cursor,
        });
      });
    });
    return items;
  }, selectors);
}

async function hoverAndMaybeClick(page, handle) {
  try { await handle.hover({ force: true }); } catch (_) {}
  try { await handle.click({ force: true, timeout: 1000 }); return true; } catch (_) { return false; }
}

async function auditRoute(page, route) {
  const pageReport = { route: route.key, controls: [], tiny: [], clicks: 0, errors: [] };
  await page.goto(BASE + '/#/' + route.key, { waitUntil: 'load' });
  await page.waitForSelector(route.ready, { timeout: 10000 });
  await page.waitForTimeout(250);
  pageReport.controls = await collectMetrics(page);
  pageReport.tiny = pageReport.controls.filter(x => (x.tag === 'button' || x.tag === 'a' || x.sel === 'input' || x.sel === 'select' || x.sel === 'textarea') && (x.w < 24 || x.h < 24));

  const interactives = await page.locator('button, a[href], [role="button"], input:not([type="hidden"]), select, textarea').elementHandles();
  for (const h of interactives) {
    const box = await h.boundingBox().catch(() => null);
    if (!box || box.width === 0 || box.height === 0) continue;
    try {
      await h.hover({ force: true });
    } catch (_) {}
    const tag = await h.evaluate(el => el.tagName.toLowerCase()).catch(() => '');
    const txt = await h.evaluate(el => (el.textContent || el.value || '').trim()).catch(() => '');
    if (tag === 'input' || tag === 'textarea') {
      if (await h.getAttribute('type') === 'file') continue;
      try {
        await h.click({ force: true });
        if (tag === 'textarea') {
          await h.fill((txt ? txt : '') + ' audit');
        } else if (await h.getAttribute('type') === 'checkbox') {
          if (!(await h.isChecked())) await h.check({ force: true }).catch(() => {});
        } else if (await h.getAttribute('type') === 'radio') {
          await h.check({ force: true }).catch(() => {});
        } else if (tag === 'input') {
          await h.fill('audit');
        }
      } catch (_) {}
    } else if (tag === 'select') {
      try {
        const opts = await h.$$('option');
        if (opts.length > 1) await h.selectOption({ index: 1 }).catch(() => {});
      } catch (_) {}
    } else {
      await hoverAndMaybeClick(page, h);
      pageReport.clicks++;
    }
    await page.waitForTimeout(40);
    const bodyText = await page.locator('body').innerText().catch(() => '');
    if (/请确认|确认/.test(bodyText)) {
      const ok = page.locator('.kairo-dialog .btn-primary, .preview-overlay .btn-primary').first();
      if (await ok.count()) await ok.click({ force: true }).catch(() => {});
    }
    if (await page.locator('.kairo-dialog-overlay, .preview-overlay, .http2-modal-mask').count().catch(() => 0)) {
      const close = page.locator('.kairo-dialog .btn, .preview-overlay .btn, .http2-modal .btn, .http2-modal .btn-primary').first();
      if (await close.count()) await close.click({ force: true }).catch(() => {});
    }
  }
  return pageReport;
}

(async () => {
  const server = await startStaticServer();
  const browser = await chromium.launch({
    headless: true,
    executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  });
  const context = await browser.newContext({ viewport: VIEWPORT, locale: 'zh-CN', acceptDownloads: true });
  const page = await context.newPage();
  setupMockApi(page);

  const issues = [];
  page.on('pageerror', e => issues.push({ type: 'pageerror', message: e.message, url: page.url() }));
  page.on('console', m => { if (m.type() === 'error') issues.push({ type: 'console', message: m.text(), url: page.url() }); });

  await page.goto(BASE + '/', { waitUntil: 'load' });
  await page.waitForSelector('#nav .nav-item', { timeout: 10000 });
  await page.waitForTimeout(250);

  const report = { run_at: new Date().toISOString(), routes: [], issues };
  for (const route of ROUTES) {
    try {
      const r = await auditRoute(page, route);
      report.routes.push(r);
    } catch (e) {
      report.routes.push({ route: route.key, error: e.message });
      issues.push({ type: 'route', route: route.key, message: e.message });
    }
  }

  fs.writeFileSync(OUT, JSON.stringify(report, null, 2));
  console.log(JSON.stringify({
    routes: report.routes.length,
    issues: issues.length,
    tiny: report.routes.reduce((n, r) => n + (r.tiny ? r.tiny.length : 0), 0),
  }, null, 2));

  await context.close();
  await browser.close();
  server.close();

  process.exit(issues.length ? 1 : 0);
})().catch(err => {
  console.error(err);
  process.exit(2);
});
