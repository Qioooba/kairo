'use strict';

const FormatterPage = require('../pages/formatter-page');

function register(runner, ctx) {
  const { page, baseUrl } = ctx;
  let fmtPage;

  runner.describe('报文格式化测试', function () {
    runner.beforeAll(async function () {
      fmtPage = new FormatterPage(page, baseUrl, runner);
    });

    runner.beforeEach(async function () {
      await fmtPage.goto();
    });

    runner.describe('页面加载', function () {
      runner.it('应成功加载报文格式化页面', async function () {
        await fmtPage.takeStateScreenshot('01-page-load');
      });

      runner.it('应存在输入框和输出框', async function () {
        const input = await page.$('#fmt-in');
        const output = await page.$('#fmt-out');
        if (!input) throw new Error('输入框未找到');
        if (!output) throw new Error('输出框未找到');
      });
    });

    runner.describe('JSON 格式化', function () {
      runner.it('输入压缩 JSON 应能格式化输出', async function () {
        const compactJson = '{"name":"test","value":123,"nested":{"a":1,"b":[2,3]}}';
        await fmtPage.setInput(compactJson);
        await fmtPage.formatJson();
        const output = await fmtPage.getOutput();
        if (output.indexOf('"name"') < 0) throw new Error('格式化输出缺少 name 字段');
        if (output.indexOf('\n') < 0) throw new Error('格式化输出没有换行');
        await fmtPage.takeStateScreenshot('02-json-format');
      });
    });

    runner.describe('JSON 压缩', function () {
      runner.it('输入格式化 JSON 应能压缩输出', async function () {
        const prettyJson = '{\n  "name": "test",\n  "value": 123\n}';
        await fmtPage.setInput(prettyJson);
        await fmtPage.compressJson();
        const output = await fmtPage.getOutput();
        const hasNewline = output.indexOf('\n') >= 0;
        const hasIndent = output.indexOf('  ') >= 0;
        if (hasNewline || hasIndent) {
          throw new Error('压缩输出仍包含换行或缩进');
        }
        await fmtPage.takeStateScreenshot('03-json-minify');
      });
    });

    runner.describe('非法 JSON 输入', function () {
      runner.beforeEach(async function () {
        await fmtPage.goto({ forceReload: true });
      });

      runner.it('输入非法 JSON 校验应显示错误', async function () {
        const badJson = '{name: test, value: 123}';

        let apiError = false;
        const responseHandler = function (res) {
          if (res.url().indexOf('/api/format/') >= 0 && res.status() >= 400) {
            apiError = true;
          }
        };
        page.on('response', responseHandler);

        try {
          await fmtPage.setInput(badJson);
          await fmtPage.validateJson();

          let foundError = false;
          for (let i = 0; i < 10; i++) {
            await page.waitForTimeout(300);
            const hasError = await fmtPage.hasError();
            const output = await fmtPage.getOutput();
            if (hasError || output.length > 0 || apiError) {
              foundError = true;
              break;
            }
          }

          if (!foundError) {
            throw new Error('非法 JSON 校验未显示错误提示或输出');
          }
        } finally {
          page.off('response', responseHandler);
        }
        await fmtPage.takeStateScreenshot('04-invalid-json');
      });
    });

    runner.describe('XML 格式化', function () {
      runner.it('输入压缩 XML 应能格式化输出', async function () {
        const compactXml = '<root><name>test</name><value>123</value></root>';
        await fmtPage.setInput(compactXml);
        await fmtPage.formatXml();
        const output = await fmtPage.getOutput();
        if (output.indexOf('<root>') < 0) throw new Error('XML 格式化输出缺少 root 标签');
        if (output.indexOf('\n') < 0) throw new Error('XML 格式化输出没有换行');
        await fmtPage.takeStateScreenshot('05-xml-format');
      });
    });

    runner.describe('YAML 格式化', function () {
      runner.it('输入 JSON 应能转换为 YAML 格式', async function () {
        const jsonInput = '{"name":"test","value":123,"nested":{"a":1}}';
        await fmtPage.setInput(jsonInput);
        await fmtPage.formatYaml();
        const output = await fmtPage.getOutput();
        if (output.length === 0) throw new Error('YAML 格式化输出为空');
        await fmtPage.takeStateScreenshot('06-yaml-format');
      });
    });

    runner.describe('URL encode（map → query string）', function () {
      runner.it('应能将 JSON 对象编码为 URL query string', async function () {
        const jsonInput = '{"name":"test","value":"123","category":"demo"}';
        await fmtPage.setInput(jsonInput);
        await fmtPage.urlEncode();
        await page.waitForTimeout(500);
        const output = await fmtPage.getOutput();
        const hasError = await fmtPage.hasError();
        if (output.length === 0 && !hasError) {
          throw new Error('URL encode 输出为空且无错误提示');
        }
        await fmtPage.takeStateScreenshot('07-url-encode');
      });
    });

    runner.describe('URL decode（query string → map）', function () {
      runner.it('应能将 URL query string 解码为 JSON 对象', async function () {
        const formInput = 'name=test&value=123';
        await fmtPage.setInput(formInput);
        await fmtPage.urlDecode();
        const output = await fmtPage.getOutput();
        if (output.length === 0) throw new Error('URL decode 输出为空');
        await fmtPage.takeStateScreenshot('08-url-decode');
      });
    });
  });
}

module.exports = { register };
