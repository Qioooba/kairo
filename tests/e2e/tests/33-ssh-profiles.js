'use strict';

/**
 * 33-ssh-profiles.js — review-2026-08 方案 02
 * S5 SSH 兼容 profile 自适应记忆（A4，P1）
 *
 * 源码事实（internal/sshclient/profilestore.go）：A4 是「每台 server 记住上次成功的
 * compat profile」，落盘 data/ssh_compat_profiles.json，只存 profile 名，无凭据；
 * 页面无 profile 选择器 UI（方案里的「保存为 profile」交互不存在，按源码行为验证）。
 *
 * 依赖：scripts/mock_sshd.py 在 127.0.0.1:2225（MOCK_SSHD_PORT），账号 test/任意密码=test。
 */

const fs = require('fs');
const path = require('path');

const RUN_DIR = process.env.KAIRO_RUN_DIR || '/tmp/kairo-review-2026-08/run';
const PROFILE_FILE = path.join(RUN_DIR, 'data', 'ssh_compat_profiles.json');

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

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('S5 SSH 连接 + 兼容 profile 记忆（A4）', function () {
    runner.beforeAll(async function () {
      // S3B-3 把宠物拖到了左上角，会遮挡左侧 SSH 主机列表的点击。
      // 先保存新位置再刷新页面重新挂载（pos 只影响挂载时位置）。
      try {
        await apiJSON(page, 'POST', '/api/pet/pos', { x: 0.85, y: 0.9 });
      } catch (e) { /* 未解锁时忽略 */ }
      try {
        await page.goto(baseUrl + '/#/ssh', { waitUntil: 'domcontentloaded' });
        await page.waitForTimeout(1200);
      } catch (e) { /* ignore */ }
    });

    runner.it('S5-1 主机列表渲染 Mock SSH（127.0.0.1:2225）', async function () {
      await page.goto(baseUrl + '/#/ssh', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
      const item = await page.$('.ssh-srv-item[title*="127.0.0.1:2225"]');
      if (!item) throw new Error('主机列表未找到 Mock SSH（127.0.0.1:2225）');
      const name = await item.$eval('.ssh-srv-name', el => el.textContent.trim()).catch(() => '');
      if (!name) throw new Error('主机项无名称');
      await runner.screenshot(page, '33-s5-host-list');
    });

    runner.it('S5-2 点击主机连接成功进终端', async function () {
      try { fs.unlinkSync(PROFILE_FILE); } catch (e) { /* 可能不存在 */ }
      // suite 连续导航时偶发 hash 漂移（观察过漂到 #/files），先确保停在 ssh 页再点
      if (await page.evaluate(() => location.hash) !== '#/ssh') {
        await page.goto(baseUrl + '/#/ssh', { waitUntil: 'domcontentloaded' });
        await page.waitForTimeout(1000);
      }
      await page.click('.ssh-srv-item[title*="127.0.0.1:2225"]');
      await page.waitForSelector('.ssh-tab', { state: 'visible', timeout: 10000 });
      let connected = false;
      for (let i = 0; i < 30; i++) {
        await page.waitForTimeout(500);
        const state = await page.evaluate(() => {
          const tab = document.querySelector('.ssh-tab');
          const termText = document.querySelector('.ssh-term') ? document.querySelector('.ssh-term').textContent : '';
          return { cls: tab ? tab.className : '', termText: termText.substring(0, 500) };
        });
        if (/state-(connected|ready|open)/.test(state.cls) || /\$|#|test@|bash|sh/.test(state.termText)) {
          connected = true;
          break;
        }
        if (/state-(error|failed|closed)/.test(state.cls)) {
          throw new Error('连接失败态: ' + state.cls);
        }
      }
      if (!connected) throw new Error('15s 内未进入终端');
      await runner.screenshot(page, '33-s5-terminal-connected');
    });

    runner.it('S5-3 profile 记忆落盘语义：默认 profile 连接不写文件；若已写则无凭据', async function () {
      // 源码事实（profilestore.go:74-76）：仅当 fallback 到「非默认」profile 成功时才落盘；
      // mock_sshd 是 modern 服务器，首次连接即成功 → 不写文件（无噪音写入）。
      // 这里验证：默认连接场景下文件要么不存在，要么（若存在）只含 profile 名、无凭据。
      await page.waitForTimeout(1000);
      if (fs.existsSync(PROFILE_FILE)) {
        const raw = fs.readFileSync(PROFILE_FILE, 'utf8');
        if (/password|passwd|secret|token/i.test(raw)) throw new Error('profile 记忆文件含凭据字段');
        let data = null;
        try { data = JSON.parse(raw); } catch (e) { throw new Error('profile 文件不是合法 JSON: ' + raw); }
        const keys = Object.keys(data);
        if (keys.length && !keys.every(k => /(modern|compat|legacy|no-ecdh|auto)/i.test(String(data[k])))) {
          throw new Error('profile 值应为 profile 名: ' + raw);
        }
      }
      // 无论如何，连接能力本身已由 S5-2 证明；这里只验证 profile 文件语义
      await runner.screenshot(page, '33-s5-profile-file');
    });

    runner.it('S5-4 重开页面再次连接成功（无需 profile 文件即可重连）', async function () {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
      if (await page.evaluate(() => location.hash) !== '#/ssh') {
        await page.goto(baseUrl + '/#/ssh', { waitUntil: 'domcontentloaded' });
        await page.waitForTimeout(1000);
      }
      await page.click('.ssh-srv-item[title*="127.0.0.1:2225"]');
      let connected = false;
      for (let i = 0; i < 30; i++) {
        await page.waitForTimeout(500);
        const state = await page.evaluate(() => {
          const tab = document.querySelector('.ssh-tab');
          const termText = document.querySelector('.ssh-term') ? document.querySelector('.ssh-term').textContent : '';
          return { cls: tab ? tab.className : '', termText: termText.substring(0, 500) };
        });
        if (/state-(connected|ready|open)/.test(state.cls) || /\$|#|test@|bash|sh/.test(state.termText)) {
          connected = true;
          break;
        }
      }
      if (!connected) throw new Error('二次连接失败');
      await runner.screenshot(page, '33-s5-reconnect');
    });

    runner.it('S5-5 /api/config 不下发 credential_key / kairo_internal_token（A7 联动）', async function () {
      const resp = await page.evaluate(async () => {
        const r = await fetch('/api/config');
        return { status: r.status, text: await r.text() };
      });
      if (resp.status !== 200) throw new Error('/api/config 状态码 ' + resp.status);
      if (/credential_key"\s*:\s*"[^"]+"/.test(resp.text)) throw new Error('credential_key 泄露');
      if (/kairo_internal_token"\s*:\s*"[^"]+"/.test(resp.text)) throw new Error('kairo_internal_token 泄露');
      if (resp.text.includes('111222')) throw new Error('dev bypass token 明文出现在 /api/config');
    });
  });
}

module.exports = { register };
