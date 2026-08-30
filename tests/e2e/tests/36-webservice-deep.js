'use strict';

/**
 * WebService 真实链路专项：WSDL URL 导入 → SOAP 1.1/1.2 → XML 安全格式化
 * → Header 多格式粘贴 → 模板 → Mock。所有持久化测试数据均带专用前缀并清理。
 */
const http = require('http');

const SOAP_PORT = parseInt(process.env.SOAP_E2E_PORT || '18097', 10);
const PREFIX = '__codex_ws_e2e_';
const projectName = PREFIX + 'mixed';
const templateName = PREFIX + 'soap12_template';
const mockName = PREFIX + 'mock';
const mockPath = '/mock/' + PREFIX + 'echo';

const wsdl = `<?xml version="1.0" encoding="GBK"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
 xmlns:soap12="http://schemas.xmlsoap.org/wsdl/soap12/"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:tns="urn:kairo:e2e" targetNamespace="urn:kairo:e2e">
 <wsdl:types><xs:schema targetNamespace="urn:kairo:e2e">
  <xs:element name="legacyEcho"><xs:complexType><xs:sequence><xs:element name="message" type="xs:string"/></xs:sequence></xs:complexType></xs:element>
  <xs:element name="modernEcho"><xs:complexType><xs:sequence><xs:element name="message" type="xs:string"/></xs:sequence></xs:complexType></xs:element>
 </xs:schema></wsdl:types>
 <wsdl:message name="LegacyIn"><wsdl:part name="p" element="tns:legacyEcho"/></wsdl:message>
 <wsdl:message name="ModernIn"><wsdl:part name="p" element="tns:modernEcho"/></wsdl:message>
 <wsdl:portType name="LegacyPT"><wsdl:operation name="legacyEcho"><wsdl:input message="tns:LegacyIn"/></wsdl:operation></wsdl:portType>
 <wsdl:portType name="ModernPT"><wsdl:operation name="modernEcho"><wsdl:input message="tns:ModernIn"/></wsdl:operation></wsdl:portType>
 <wsdl:binding name="LegacyBinding" type="tns:LegacyPT"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><wsdl:operation name="legacyEcho"><soap:operation soapAction="urn:legacyEcho"/><wsdl:input><soap:body use="literal"/></wsdl:input></wsdl:operation></wsdl:binding>
 <wsdl:binding name="ModernBinding" type="tns:ModernPT"><soap12:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><wsdl:operation name="modernEcho"><soap12:operation soapAction="urn:modernEcho"/><wsdl:input><soap12:body use="literal"/></wsdl:input></wsdl:operation></wsdl:binding>
 <wsdl:service name="MixedEchoService">
  <wsdl:port name="LegacyPort" binding="tns:LegacyBinding"><soap:address location="http://127.0.0.1:${SOAP_PORT}/soap11"/></wsdl:port>
  <wsdl:port name="ModernPort" binding="tns:ModernBinding"><soap12:address location="http://127.0.0.1:${SOAP_PORT}/soap12"/></wsdl:port>
 </wsdl:service>
</wsdl:definitions>`;

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
  for (const [listURL, key, deleteBase] of [
    ['/api/wsdl/projects', 'projects', '/api/wsdl/projects/'],
    ['/api/soap/templates', 'templates', '/api/soap/templates/'],
    ['/api/soap/mocks', 'mocks', '/api/soap/mocks/'],
  ]) {
    const list = await apiJSON(page, 'GET', listURL);
    for (const item of ((list.data && list.data[key]) || [])) {
      if (String(item.name || '').startsWith(PREFIX)) {
        await apiJSON(page, 'DELETE', deleteBase + encodeURIComponent(item.id));
      }
    }
  }
}

function register(runner, ctx) {
  const { page, baseUrl } = ctx;
  let server;
  const received = [];

  runner.describe('WebService - 真实格式与用户操作专项', function () {
    runner.beforeAll(async function () {
      server = http.createServer((req, res) => {
        if (req.url === '/mixed.wsdl') {
          res.writeHead(200, { 'Content-Type': 'text/xml; charset=GBK' });
          res.end(Buffer.from(wsdl, 'ascii'));
          return;
        }
        if (req.url === '/soap11' || req.url === '/soap12') {
          const chunks = [];
          req.on('data', c => chunks.push(c));
          req.on('end', () => {
            received.push({ url: req.url, headers: req.headers, body: Buffer.concat(chunks) });
            const tag = req.url === '/soap12' ? 'SOAP12-OK' : 'SOAP11-OK';
            res.writeHead(200, { 'Content-Type': 'application/soap+xml; charset=UTF-8' });
            res.end(`<?xml version="1.0" encoding="UTF-8"?><response><message>${tag}</message></response>`);
          });
          return;
        }
        res.writeHead(404); res.end();
      });
      await new Promise((resolve, reject) => {
        server.once('error', reject);
        server.listen(SOAP_PORT, '127.0.0.1', resolve);
      });
      await page.goto(baseUrl + '/#/webservice', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(700);
      await cleanupTagged(page);
    });

    runner.beforeEach(async function () {
      await page.goto(baseUrl + '/#/webservice', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(550);
      const project = page.locator('.svc-item').filter({ hasText: projectName }).first();
      if (await project.count()) {
        await project.click();
        await page.waitForTimeout(300);
      }
    });

    runner.afterAll(async function () {
      try { await cleanupTagged(page); } catch (e) { /* best effort */ }
      if (server) await new Promise(resolve => server.close(resolve));
    });

    runner.it('WSDL URL 导入兼容 GBK declaration，并识别混合 SOAP 1.1/1.2', async function () {
      await page.click('button:has-text("+ 导入 URL")');
      let input = await page.waitForSelector('.kairo-dialog-overlay input.form-control');
      await input.fill(`http://127.0.0.1:${SOAP_PORT}/mixed.wsdl`);
      await page.click('.kairo-dialog-overlay button:has-text("确定")');
      input = await page.waitForSelector('.kairo-dialog-overlay input.form-control');
      await input.fill(projectName);
      await page.click('.kairo-dialog-overlay button:has-text("确定")');
      await page.waitForFunction(name => document.body.innerText.includes(name), projectName, { timeout: 8000 });
      const text = await page.locator('.svc-main').innerText();
      if (!text.includes('legacyEcho') || !text.includes('modernEcho') || !text.includes('2 operations')) {
        throw new Error('混合 binding 未完整展示: ' + text.slice(0, 800));
      }
      await runner.screenshot(page, '36-ws-01-mixed-wsdl');
    });

    runner.it('切 operation 会保护已编辑报文，确认后版本/endpoint/body 整体切换', async function () {
      await page.click('.svc-op-item:has-text("modernEcho")');
      await page.waitForTimeout(300);
      if (await page.locator('#svc-soapver').inputValue() !== '1.2') throw new Error('modernEcho 未切到 SOAP 1.2');
      let body = await page.locator('#svc-body').inputValue();
      if (!body.includes('2003/05/soap-envelope') || !body.includes('modernEcho')) throw new Error('SOAP 1.2 envelope 不匹配');

      await page.locator('#svc-body').fill(body.replace('</message>', '已编辑</message>'));
      await page.click('.svc-op-item:has-text("legacyEcho")');
      await page.waitForSelector('.kairo-dialog-overlay:has-text("切换 operation")');
      await page.click('.kairo-dialog-overlay button:has-text("取消")');
      if (!(await page.locator('#svc-endpoint').inputValue()).endsWith('/soap12')) throw new Error('取消后 operation 仍被切换');

      await page.click('.svc-op-item:has-text("legacyEcho")');
      await page.waitForSelector('.kairo-dialog-overlay:has-text("切换 operation")');
      await page.click('.kairo-dialog-overlay button:has-text("确定")');
      await page.waitForTimeout(350);
      body = await page.locator('#svc-body').inputValue();
      if (!(await page.locator('#svc-endpoint').inputValue()).endsWith('/soap11') || !body.includes('legacyEcho') || body.includes('modernEcho')) {
        throw new Error('确认切换后 endpoint/body 没有同步');
      }
    });

    runner.it('格式化不改变定长空格/mixed content；SOAP 1.2 + GB18030 + curl Header 可发送且不落历史', async function () {
      const significant = '<Envelope><fixed>  A B  </fixed><mixed>Hello <b>world</b> !</mixed></Envelope>';
      await page.locator('#svc-body').fill(significant);
      await page.click('.svc-req-card button:has-text("格式化")');
      let body = await page.locator('#svc-body').inputValue();
      if (!body.includes('<fixed>  A B  </fixed>') || !body.includes('<mixed>Hello <b>world</b> !</mixed>')) {
        throw new Error('格式化改变了业务文本: ' + body);
      }

      await page.click('.svc-op-item:has-text("modernEcho")');
      await page.waitForSelector('.kairo-dialog-overlay:has-text("切换 operation")');
      await page.click('.kairo-dialog-overlay button:has-text("确定")');
      await page.waitForFunction(() => {
        const ver = document.querySelector('#svc-soapver');
        const body = document.querySelector('#svc-body');
        return ver && ver.value === '1.2' && body && body.value.includes('modernEcho');
      });
      await page.selectOption('#svc-encoding', 'GB18030');
      await page.fill('#svc-headers', `-H 'X-Trace: E2E-TRACE'\n--header "X-Mode: compat"`);
      await page.uncheck('#svc-save-history');
      body = await page.locator('#svc-body').inputValue();
      if (!body.includes('encoding="GB18030"')) throw new Error('编码选择未同步 XML declaration');
      const before = await apiJSON(page, 'GET', '/api/soap/history');
      const beforeCount = ((before.data && before.data.history) || []).length;
      await page.click('#svc-send-btn');
      await page.waitForFunction(() => document.body.innerText.includes('SOAP12-OK'), null, { timeout: 8000 });
      const after = await apiJSON(page, 'GET', '/api/soap/history');
      const afterCount = ((after.data && after.data.history) || []).length;
      if (afterCount !== beforeCount) throw new Error('关闭保存历史后仍新增了记录');
      const last = received[received.length - 1];
      if (!last || last.url !== '/soap12' || last.headers.soapaction) throw new Error('SOAP 1.2 路由/Header 错误');
      if (!/application\/soap\+xml/i.test(last.headers['content-type'] || '') || !/action="urn:modernEcho"/.test(last.headers['content-type'] || '')) throw new Error('SOAP 1.2 Content-Type/action 错误');
      if (last.headers['x-trace'] !== 'E2E-TRACE' || last.headers['x-mode'] !== 'compat') throw new Error('curl Header 未正确解析');
      if (!last.body.toString('ascii').includes('encoding="GB18030"')) throw new Error('线上 XML declaration 不是 GB18030');
      await runner.screenshot(page, '36-ws-02-soap12-response');
    });

    runner.it('SOAP 1.2 模板保存/加载保留版本，且加载后停留在模板页', async function () {
      await page.click('.svc-op-item:has-text("modernEcho")');
      await page.waitForFunction(() => document.querySelector('#svc-soapver') && document.querySelector('#svc-soapver').value === '1.2');
      await page.selectOption('#svc-encoding', 'GB18030');
      await page.click('button:has-text("存为模板")');
      const inputs = page.locator('.kairo-dialog-field input');
      await inputs.nth(0).fill(templateName);
      await inputs.nth(1).fill('e2e');
      await page.click('.kairo-dialog-overlay button:has-text("确定")');
      await page.waitForTimeout(500);
      await page.click('.svc-tab-btn:has-text("模板")');
      await page.click('.svc-item:has-text("' + templateName + '")');
      await page.waitForTimeout(250);
      const cls = await page.locator('.svc-tab-btn:has-text("模板")').getAttribute('class');
      if (!/active/.test(cls || '')) throw new Error('加载模板后意外跳离模板页');
      if (await page.locator('#svc-soapver').inputValue() !== '1.2') throw new Error('模板未保留 SOAP 1.2');
      if (await page.locator('#svc-encoding').inputValue() !== 'GB18030') throw new Error('模板未保留 GB18030');
    });

    runner.it('Mock 创建、测试访问和请求记录形成闭环', async function () {
      await page.click('.svc-tab-btn:has-text("Mock")');
      await page.click('button:has-text("+ 新 Mock")');
      await page.fill('#svc-mock-name', mockName);
      await page.fill('#svc-mock-path', mockPath);
      await page.fill('#svc-mock-op', 'mockEcho');
      await page.fill('#svc-mock-body', '<?xml version="1.0"?><mockResponse><code>0</code><msg>OK</msg></mockResponse>');
      await page.click('button:has-text("保存 Mock")');
      await page.waitForTimeout(400);
      await page.click('button:has-text("测试访问")');
      await page.waitForSelector('.kairo-dialog-wide:has-text("200 OK")');
      const modal = await page.locator('.kairo-dialog-wide').innerText();
      if (!modal.includes('<mockResponse>') || !modal.includes('<msg>OK</msg>')) throw new Error('Mock 测试响应不完整');
      await page.click('.kairo-dialog-wide button:has-text("关闭")');
      const records = await apiJSON(page, 'GET', '/api/soap/mocks/records');
      const hit = ((records.data && records.data.records) || []).some(r => r.path === mockPath);
      if (!hit) throw new Error('Mock 请求记录未落盘');
      await runner.screenshot(page, '36-ws-03-mock');
    });
  });
}

module.exports = { register };
