'use strict';

function register(runner, ctx) {
  const { page, baseUrl } = ctx;

  runner.describe('UI17 工作台与导航', function () {
    runner.it('报文格式化在小工具分组，WebService 代码生成不缩写', async function () {
      await page.goto(baseUrl + '/', { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('#nav', { timeout: 5000 });
      const info = await page.evaluate(function () {
        const items = Array.from(document.querySelectorAll('#nav .nav-item')).map(function (a) {
          return { route: a.getAttribute('data-route'), text: a.textContent.trim(), small: a.classList.contains('small') };
        });
        const seps = Array.from(document.querySelectorAll('#nav .nav-sep')).map(function (el) { return el.textContent.trim(); });
        return { items: items, seps: seps };
      });
      const formatter = info.items.find(function (x) { return x.route === 'formatter'; });
      const codegen = info.items.find(function (x) { return x.route === 'wscodegen'; });
      if (!formatter || !formatter.small) throw new Error('报文格式化应在小工具里');
      if (!codegen || codegen.text.indexOf('WS ') === 0 || codegen.text.indexOf('WebService') < 0) {
        throw new Error('代码生成菜单应写全称 WebService，实际: ' + (codegen && codegen.text));
      }
    });

    runner.it('比较页支持路径输入、深度限制和拖入提示', async function () {
      await page.goto(baseUrl + '/#/compare', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(800);
      const folderTab = await page.$('button:has-text("文件夹比较")');
      if (!folderTab) throw new Error('没有文件夹比较页签');
      await folderTab.click();
      await page.waitForTimeout(300);
      const paths = await page.$$('.cmp-folder-path');
      if (paths.length < 2) throw new Error('文件夹比较应直接提供左右路径输入框');
      const depth = await page.$('#cmp-scan-depth');
      if (!depth) throw new Error('文件夹比较应提供扫描深度选择');
      await runner.screenshot(page, '39-compare-folder-paths');
    });

    runner.it('日志助手目标服务器名称与 IP 紧挨着', async function () {
      await page.goto(baseUrl + '/#/websphere', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1200);
      const gap = await page.evaluate(function () {
        const item = document.querySelector('.srv-pick-item');
        if (!item) return null;
        const name = item.querySelector('.name');
        const host = item.querySelector('.host');
        if (!name || !host) return null;
        const nr = name.getBoundingClientRect();
        const hr = host.getBoundingClientRect();
        return { gap: Math.round(hr.left - nr.right), name: name.textContent, host: host.textContent };
      });
      if (gap && gap.gap > 80) throw new Error('服务器名称与 IP 间距过大: ' + JSON.stringify(gap));
      const statusGap = await page.evaluate(function () {
        const item = document.querySelector('.srv-pick-item');
        if (!item) return null;
        const host = item.querySelector('.host');
        const status = item.querySelector('.status');
        if (!host || !status) return null;
        const hr = host.getBoundingClientRect();
        const sr = status.getBoundingClientRect();
        return Math.round(sr.left - hr.right);
      });
      if (statusGap != null && statusGap > 40) throw new Error('IP 与状态间距过大: ' + statusGap);
      await runner.screenshot(page, '39-websphere-servers');
    });
  });
}

module.exports = { register };
