'use strict';

/**
 * WS 代码生成：恒力真实 WSDL+XSD、六引擎卡片、空来源 toast、点文件不滚顶。
 */
const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');
const { URL } = require('url');

const PREFIX = '__codex_wscodegen_e2e_';
const projectName = PREFIX + 'hengli';
const HENGLI = path.resolve(__dirname, '../../../internal/webservice/testdata/hengli');

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
  const list = await apiJSON(page, 'GET', '/api/wsdl/projects');
  for (const item of ((list.data && list.data.projects) || [])) {
    if (String(item.name || '').startsWith(PREFIX)) {
      await apiJSON(page, 'DELETE', '/api/wsdl/projects/' + encodeURIComponent(item.id));
    }
  }
}

function readHengli() {
  return {
    wsdl: fs.readFileSync(path.join(HENGLI, 'SuLianLoanHengLiService.wsdl'), 'utf8'),
    core: fs.readFileSync(path.join(HENGLI, 'SuLianLoanHengLiServiceCore.xsd'), 'utf8'),
    esb: fs.readFileSync(path.join(HENGLI, 'esb.xsd'), 'utf8'),
    wsdlPath: path.join(HENGLI, 'SuLianLoanHengLiService.wsdl'),
  };
}

function register(runner, ctx) {
  const { page, baseUrl } = ctx;
  let projectId = '';
  let outDir = '';

  runner.describe('WS 代码生成 - 恒力真实数据与 UI', function () {
    runner.beforeAll(async function () {
      if (!fs.existsSync(path.join(HENGLI, 'SuLianLoanHengLiService.wsdl'))) {
        throw new Error('missing hengli testdata: ' + HENGLI);
      }
      await page.goto(baseUrl + '/#/wscodegen', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(400);
      await page.evaluate(function () { try { localStorage.removeItem('kairo:wscodegen:form'); } catch (e) { /* ignore */ } });
      await cleanupTagged(page);

      const files = readHengli();
      const imported = await apiJSON(page, 'POST', '/api/wsdl/import-file', {
        content: files.wsdl,
        name: projectName,
        attachments: {
          'SuLianLoanHengLiServiceCore.xsd': files.core,
          'esb.xsd': files.esb,
        },
      });
      if (imported.status !== 200 || !imported.data || !imported.data.project) {
        throw new Error('import hengli failed: ' + JSON.stringify(imported).slice(0, 400));
      }
      const saved = await apiJSON(page, 'POST', '/api/wsdl/projects', { project: imported.data.project });
      if (saved.status !== 200 || !saved.data.project || !saved.data.project.id) {
        throw new Error('save hengli failed: ' + JSON.stringify(saved).slice(0, 400));
      }
      projectId = saved.data.project.id;
      outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'kairo-wscodegen-'));
    });

    runner.afterAll(async function () {
      try { await cleanupTagged(page); } catch (e) { /* best effort */ }
      if (outDir) {
        try { fs.rmSync(outDir, { recursive: true, force: true }); } catch (e) { /* ignore */ }
      }
    });

    runner.it('已导入恒力项目预览应展开 creditcode / SEQ_NO', async function () {
      await page.goto(baseUrl + '/#/wscodegen?project=' + encodeURIComponent(projectId), { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(900);
      const selected = await page.evaluate(function () {
        const sel = document.querySelector('.wsc-source-body select');
        return sel ? sel.value : '';
      });
      if (!selected) throw new Error('hash 项目未选中');

      await page.click('#wsc-preview-btn');
      await page.waitForSelector('#wsc-files .wsc-file', { timeout: 8000 });
      const names = await page.locator('#wsc-files .wsc-file').allTextContents();
      const reqFile = names.filter(function (n) { return /Request\.java$/.test(n); })[0];
      if (!reqFile) throw new Error('没有 Request bean: ' + names.join(','));
      await page.locator('#wsc-files .wsc-file', { hasText: reqFile.split('/').pop() }).click();
      const code = await page.locator('#wsc-code').innerText();
      for (const need of ['creditcode', 'custType', 'SEQ_NO']) {
        if (code.indexOf(need) < 0) throw new Error('Request bean 缺少 ' + need + ' files=' + names.join(','));
      }
      const client = names.filter(function (n) { return /Client\.java$/.test(n); })[0];
      if (!client) throw new Error('没有 Client.java');
      const engines = await page.locator('.wsc-engine').count();
      if (engines < 6) throw new Error('引擎卡片不足 6，实际 ' + engines);
      await runner.screenshot(page, '37-wscodegen-hengli-preview');
    });

    runner.it('六个引擎卡片在 1366 视口都能看到 Axis / XFire', async function () {
      await page.setViewportSize({ width: 1366, height: 900 });
      await page.goto(baseUrl + '/#/wscodegen?project=' + encodeURIComponent(projectId), { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('.wsc-engine[data-engine="xfire"]', { timeout: 8000 });
      await page.locator('.wsc-engine-grid').scrollIntoViewIfNeeded();
      await page.waitForTimeout(200);
      const vis = await page.evaluate(function () {
        const shell = document.querySelector('.wsc-shell');
        const cs = shell ? getComputedStyle(shell) : null;
        const cards = Array.from(document.querySelectorAll('.wsc-engine'));
        return {
          cols: cs ? cs.gridTemplateColumns : '',
          cards: cards.map(function (c) {
            const r = c.getBoundingClientRect();
            return {
              id: c.getAttribute('data-engine'),
              inView: r.top >= 0 && r.bottom <= window.innerHeight && r.height > 0,
              name: (c.querySelector('.wsc-engine-name') || {}).textContent || '',
            };
          }),
        };
      });
      const ids = vis.cards.map(function (v) { return v.id; }).sort().join(',');
      if (ids.indexOf('axis1') < 0 || ids.indexOf('xfire') < 0) {
        throw new Error('缺 Axis/XFire 卡片: ' + ids);
      }
      if ((vis.cols.match(/ /g) || []).length < 1) {
        throw new Error('1366 应为左右分栏, grid=' + vis.cols);
      }
      const missing = vis.cards.filter(function (v) { return !v.inView; });
      if (missing.length) {
        throw new Error('引擎卡片未全部露出: ' + JSON.stringify(missing));
      }
      await runner.screenshot(page, '37-wscodegen-engines-1366');
    });

    runner.it('切换 Axis 再预览，点文件不把滚动冲到顶', async function () {
      await page.goto(baseUrl + '/#/wscodegen?project=' + encodeURIComponent(projectId), { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('.wsc-engine[data-engine="axis1"]', { timeout: 8000 });
      await page.click('.wsc-engine[data-engine="axis1"]');
      await page.waitForTimeout(200);
      await page.click('#wsc-preview-btn');
      await page.waitForSelector('#wsc-files .wsc-file', { timeout: 8000 });
      await page.evaluate(function () {
        const pane = document.querySelector('.wsc-pane-config');
        if (pane) pane.scrollTop = Math.min(420, Math.max(0, pane.scrollHeight - pane.clientHeight));
        const v = document.querySelector('.view');
        if (v) v.scrollTop = Math.min(420, Math.max(0, v.scrollHeight - v.clientHeight));
      });
      const before = await page.evaluate(function () {
        const pane = document.querySelector('.wsc-pane-config');
        const v = document.querySelector('.view');
        return {
          pane: pane ? pane.scrollTop : 0,
          view: v ? v.scrollTop : 0,
          engine: (document.querySelector('.wsc-engine.active') || {}).getAttribute ? document.querySelector('.wsc-engine.active').getAttribute('data-engine') : '',
        };
      });
      const files = page.locator('#wsc-files .wsc-file');
      const n = await files.count();
      if (n < 2) throw new Error('预览文件太少: ' + n);
      await files.nth(n > 2 ? 2 : 1).click();
      await page.waitForTimeout(200);
      const after = await page.evaluate(function () {
        const pane = document.querySelector('.wsc-pane-config');
        const v = document.querySelector('.view');
        return {
          pane: pane ? pane.scrollTop : 0,
          view: v ? v.scrollTop : 0,
          engine: (document.querySelector('.wsc-engine.active') || {}).getAttribute ? document.querySelector('.wsc-engine.active').getAttribute('data-engine') : '',
        };
      });
      if (after.engine !== 'axis1') {
        throw new Error('点文件后引擎选择被冲掉: ' + after.engine);
      }
      if (before.pane > 80 && after.pane < 80) {
        throw new Error('点文件后配置栏滚回顶部 before=' + JSON.stringify(before) + ' after=' + JSON.stringify(after));
      }
      if (before.view > 80 && after.view < 80) {
        throw new Error('点文件后页面滚回顶部 before=' + JSON.stringify(before) + ' after=' + JSON.stringify(after));
      }
      const code = await page.locator('#wsc-code').innerText();
      if (code.indexOf('package ') < 0) {
        throw new Error('切换文件后代码区异常');
      }
    });

    runner.it('空粘贴点预览应 toast，不打出含糊后端错误', async function () {
      await page.goto(baseUrl + '/#/wscodegen', { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('button.wsc-seg-btn:has-text("粘贴内容")', { timeout: 8000 });
      await page.click('button.wsc-seg-btn:has-text("粘贴内容")');
      await page.waitForTimeout(200);
      await page.fill('.wsc-paste', '');
      await page.click('#wsc-preview-btn');
      await page.waitForTimeout(400);
      const toast = await page.evaluate(function () {
        const t = document.getElementById('toast');
        return t ? t.textContent : '';
      });
      if (toast.indexOf('粘贴') < 0) throw new Error('空粘贴未给出来源 toast: ' + toast);
    });

    runner.it('本地恒力 WSDL 文件路径预览应带上同目录 XSD', async function () {
      const files = readHengli();
      await page.goto(baseUrl + '/#/wscodegen', { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('button.wsc-seg-btn:has-text("本地 WSDL")', { timeout: 8000 });
      await page.click('button.wsc-seg-btn:has-text("本地 WSDL")');
      await page.waitForTimeout(200);
      await page.fill('.wsc-source-body input[type="text"]', files.wsdlPath);
      await page.click('.wsc-engine[data-engine="portable"]');
      await page.click('#wsc-preview-btn');
      await page.waitForSelector('#wsc-files .wsc-file', { timeout: 8000 });
      const names = await page.locator('#wsc-files .wsc-file').allTextContents();
      const reqFile = names.filter(function (n) { return /Request\.java$/.test(n); })[0];
      if (reqFile) {
        await page.locator('#wsc-files .wsc-file', { hasText: reqFile.split('/').pop() }).click();
      }
      const code = await page.locator('#wsc-code').innerText();
      const log = await page.locator('#wsc-log').innerText();
      if (code.indexOf('creditcode') < 0) throw new Error('文件路径未展开 creditcode');
      if (log.indexOf('XSD') < 0) throw new Error('未提示加载同目录 XSD: ' + log);
      await runner.screenshot(page, '37-wscodegen-file-xsd');
    });

    runner.it('生成 API 应把恒力 Client 写到临时目录', async function () {
      const payload = JSON.stringify({
        engine: 'portable',
        mode: 'builtin',
        include_main: true,
        java_source: '1.6',
        overwrite: true,
        open_after: false,
        output_dir: outDir,
        wsdl_project_id: projectId,
      });
      const u = new URL(baseUrl);
      const result = await new Promise(function (resolve, reject) {
        const req = http.request({
          hostname: u.hostname,
          port: u.port || 80,
          path: '/api/wscodegen/generate',
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) },
        }, function (res) {
          const chunks = [];
          res.on('data', function (c) { chunks.push(c); });
          res.on('end', function () {
            const raw = Buffer.concat(chunks).toString('utf8');
            let data = null;
            try { data = JSON.parse(raw); } catch (e) { data = raw; }
            resolve({ status: res.statusCode, data: data });
          });
        });
        req.on('error', reject);
        req.write(payload);
        req.end();
      });
      if (result.status !== 200 || !result.data || !result.data.result) {
        throw new Error('generate failed: ' + JSON.stringify(result).slice(0, 500));
      }
      const written = result.data.result.written || [];
      if (!written.length) throw new Error('未写出文件');
      const hasClient = written.some(function (p) { return /Client\.java$/.test(p); });
      if (!hasClient) throw new Error('缺少 Client.java: ' + written.join(','));
    });
  });
}

module.exports = { register };
