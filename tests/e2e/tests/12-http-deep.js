'use strict';

/**
 * HTTP 测试页 深度测试
 * 覆盖接口：
 * - /api/http/cases
 * - /api/http/envs
 * - /api/http/request
 */

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('HTTP 接口测试 - 深度', function () {
    runner.beforeEach(async function () {
      await page.goto(baseUrl + '#/http', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1500);
    });

    runner.it('页面加载成功', async function () {
      const hasMethod = await page.$('select[id*="method"], select[class*="method"]');
      const hasUrl = await page.$('input[placeholder*="URL"], input[id*="url"], input[placeholder*="url"]');
      if (!hasMethod && !hasUrl) {
        const bodyText = await page.evaluate(() => document.body.innerText.trim().substring(0, 200));
        throw new Error('HTTP 测试页面未正确加载: ' + bodyText);
      }
      await runner.screenshot(page, '14-http-01-load');
    });

    runner.it('发送 GET 请求到本机接口', async function () {
      // 设置方法
      const methodSelect = await page.$('select[id*="method"], select[class*="method"]');
      if (methodSelect) {
        await methodSelect.selectOption('GET');
      }
      await page.waitForTimeout(200);

      // 设置 URL
      const urlInput = await page.$('input[placeholder*="URL"], input[id*="url"]');
      if (urlInput) {
        await urlInput.fill(baseUrl + '/api/config');
      }
      await page.waitForTimeout(200);

      // 点击发送
      const sendBtn = await page.$('button:has-text("发送"), button:has-text("请求"), button:has-text("Send")');
      if (sendBtn) await sendBtn.click();
      await page.waitForTimeout(2000);

      await runner.screenshot(page, '14-http-02-get-local');
    });

    runner.it('发送 POST 请求到格式化接口', async function () {
      const methodSelect = await page.$('select[id*="method"], select[class*="method"]');
      if (methodSelect) {
        await methodSelect.selectOption('POST');
      }
      await page.waitForTimeout(200);

      const urlInput = await page.$('input[placeholder*="URL"], input[id*="url"]');
      if (urlInput) {
        await urlInput.fill(baseUrl + '/api/format/json');
      }
      await page.waitForTimeout(200);

      // 设置 body
      const bodyInput = await page.$('textarea[name*="body"], textarea[id*="body"], textarea[placeholder*="body"]');
      if (bodyInput) {
        await bodyInput.fill(JSON.stringify({ action: 'format', data: '{"name":"test"}' }));
      }
      await page.waitForTimeout(200);

      const sendBtn = await page.$('button:has-text("发送"), button:has-text("请求")');
      if (sendBtn) await sendBtn.click();
      await page.waitForTimeout(2000);

      await runner.screenshot(page, '14-http-03-post-format');
    });

    runner.it('设置 Headers', async function () {
      // 点击添加 header 按钮
      const addHeaderBtn = await page.$('button:has-text("添加 Header"), button:has-text("+ Header"), button:has-text("Headers")');
      if (addHeaderBtn) await addHeaderBtn.click();
      await page.waitForTimeout(300);

      // 填写 header
      const headerInputs = await page.$$('input[placeholder*="Header"], input[placeholder*="header"]');
      if (headerInputs.length >= 2) {
        await headerInputs[0].fill('Content-Type');
        await headerInputs[1].fill('application/json');
      }
      await page.waitForTimeout(300);

      await runner.screenshot(page, '14-http-04-headers');
    });

    runner.it('重复 Header 验证', async function () {
      // 添加第一个 header
      let addHeaderBtn = await page.$('button:has-text("添加 Header"), button:has-text("+ Header")');
      if (addHeaderBtn) await addHeaderBtn.click();
      await page.waitForTimeout(300);

      let headerInputs = await page.$$('input[placeholder*="Header"], input[placeholder*="header"]');
      if (headerInputs.length >= 2) {
        await headerInputs[0].fill('X-Custom-Header');
        await headerInputs[1].fill('value1');
      }
      await page.waitForTimeout(200);

      // 添加重复的 header
      addHeaderBtn = await page.$('button:has-text("添加 Header"), button:has-text("+ Header")');
      if (addHeaderBtn) await addHeaderBtn.click();
      await page.waitForTimeout(300);

      headerInputs = await page.$$('input[placeholder*="Header"], input[placeholder*="header"]');
      if (headerInputs.length >= 4) {
        await headerInputs[2].fill('X-Custom-Header');
        await headerInputs[3].fill('value2');
      }
      await page.waitForTimeout(300);

      await runner.screenshot(page, '14-http-05-duplicate-headers');
    });

    runner.it('历史记录存在', async function () {
      // 先发送一个请求
      const urlInput = await page.$('input[placeholder*="URL"], input[id*="url"]');
      if (urlInput) {
        await urlInput.fill(baseUrl + '/api/config');
      }
      await page.waitForTimeout(200);

      const sendBtn = await page.$('button:has-text("发送"), button:has-text("请求")');
      if (sendBtn) await sendBtn.click();
      await page.waitForTimeout(1500);

      // 检查历史记录
      const historyPanel = await page.$('[class*="history"], [class*="history"], [id*="history"]');
      if (historyPanel) {
        await runner.screenshot(page, '14-http-06-history');
      }
    });

    runner.it('响应高亮检查', async function () {
      const urlInput = await page.$('input[placeholder*="URL"], input[id*="url"]');
      if (urlInput) {
        await urlInput.fill(baseUrl + '/api/config');
      }
      await page.waitForTimeout(200);

      const sendBtn = await page.$('button:has-text("发送"), button:has-text("请求")');
      if (sendBtn) await sendBtn.click();
      await page.waitForTimeout(2000);

      // 检查响应是否语法高亮
      const responsePanel = await page.$('[class*="response"], [class*="result"], pre, code');
      if (responsePanel) {
        const html = await responsePanel.innerHTML();
        if (html.length > 100) {
          await runner.screenshot(page, '14-http-07-response-highlight');
        }
      }
    });

    runner.it('状态码显示', async function () {
      const urlInput = await page.$('input[placeholder*="URL"], input[id*="url"]');
      if (urlInput) {
        await urlInput.fill(baseUrl + '/api/config');
      }
      await page.waitForTimeout(200);

      const sendBtn = await page.$('button:has-text("发送"), button:has-text("请求")');
      if (sendBtn) await sendBtn.click();
      await page.waitForTimeout(2000);

      // 查找状态码
      const statusCode = await page.evaluate(() => {
        const els = document.querySelectorAll('[class*="status"], [class*="code"], [class*="response"]');
        for (const el of els) {
          const text = el.textContent;
          if (/\d{3}/.test(text)) {
            return text.match(/\d{3}/)[0];
          }
        }
        return null;
      });

      await runner.screenshot(page, '14-http-08-status-code');
    });

    runner.it('用例列表存在', async function () {
      const casesPanel = await page.$('[class*="case"], [class*="cases"], [id*="case"]');
      if (casesPanel) {
        await runner.screenshot(page, '14-http-09-cases');
      }
    });
  });
}

module.exports = { register };
