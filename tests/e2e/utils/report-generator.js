const fs = require('fs');
const path = require('path');

function loadJson(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf-8');
    return JSON.parse(content);
  } catch (err) {
    return null;
  }
}

function saveJson(filePath, data) {
  const dir = path.dirname(filePath);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
  fs.writeFileSync(filePath, JSON.stringify(data, null, 2), 'utf-8');
}

function matchApiPattern(pattern, url) {
  const patternParts = pattern.split('/').filter(Boolean);
  const urlObj = new URL(url, 'http://localhost');
  const urlParts = urlObj.pathname.split('/').filter(Boolean);

  if (patternParts.length !== urlParts.length) {
    return false;
  }

  for (let i = 0; i < patternParts.length; i++) {
    const patternPart = patternParts[i];
    const urlPart = urlParts[i];

    if (patternPart.startsWith('{') && patternPart.endsWith('}')) {
      continue;
    }

    if (patternPart !== urlPart) {
      return false;
    }
  }

  return true;
}

function calculateApiCoverage(networkLog, apiList) {
  const coveredApis = new Set();
  const uncoveredApis = [];

  for (const api of apiList) {
    let isCovered = false;
    for (const log of networkLog) {
      if (log.url && matchApiPattern(api.pattern || api.path || api, log.url)) {
        isCovered = true;
        break;
      }
    }
    if (isCovered) {
      coveredApis.add(api.pattern || api.path || api);
    } else {
      uncoveredApis.push(api);
    }
  }

  return {
    total: apiList.length,
    covered: coveredApis.size,
    uncovered: apiList.length - coveredApis.size,
    rate: apiList.length > 0 ? ((coveredApis.size / apiList.length) * 100).toFixed(2) : '0.00',
    coveredApis: Array.from(coveredApis),
    uncoveredApis: uncoveredApis
  };
}

function generateReport(testResults, options) {
  const suites = testResults.suites || [];
  const summary = testResults.summary || {};
  const buttonCoverage = testResults.buttonCoverage || {};
  const networkLog = testResults.networkLog || [];
  const consoleLog = testResults.consoleLog || [];
  const menuList = testResults.menuList || [];
  const pageMeta = testResults.pageMeta || testResults.pageList || [];
  const apiList = testResults.apiList || [];

  const { baseUrl = '', browserName = '', outputDir = '', screenshotsDir = '' } = options || {};

  const now = new Date();
  const testTime = now.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' });

  const totalTests = summary.total || 0;
  const passedTests = summary.passed || 0;
  const failedTests = summary.failed || 0;
  const skippedTests = summary.skipped || 0;
  const passRate = totalTests > 0 ? ((passedTests / totalTests) * 100).toFixed(2) : '0.00';
  const duration = summary.duration || 0;
  const durationStr = formatDuration(duration);

  let menuCoverage = testResults.menuCoverage;
  if (!menuCoverage || !menuCoverage.total) {
    const totalMenus = menuList.length;
    const testedMenus = menuList.filter((m) => m.tested || m.visited || m.clicked).length;
    menuCoverage = { tested: testedMenus, total: totalMenus, items: menuList };
  }

  let pageCoverage = testResults.pageCoverage;
  if (!pageCoverage || !pageCoverage.total) {
    const totalPages = pageMeta.length;
    const testedPages = pageMeta.filter((p) => p.tested || p.visited || p.screenshot).length;
    pageCoverage = { tested: testedPages, total: totalPages, items: pageMeta };
  }

  const apiCoverage = testResults.apiCoverage || calculateApiCoverage(networkLog, apiList);

  let md = '';

  md += '# Kairo E2E 测试报告\n\n';

  md += '## 基本信息\n\n';
  md += `- **测试时间**: ${testTime}\n`;
  md += `- **测试环境**: Node.js ${process.version}\n`;
  md += `- **服务地址**: ${baseUrl || '未指定'}\n`;
  md += `- **浏览器**: ${browserName || '未指定'}\n\n`;

  md += '## 测试概览\n\n';
  md += '| 指标 | 数值 |\n';
  md += '|------|------|\n';
  md += `| 总用例数 | ${totalTests} |\n`;
  md += `| 通过数 | ✅ ${passedTests} |\n`;
  md += `| 失败数 | ❌ ${failedTests} |\n`;
  md += `| 跳过数 | ⏭️ ${skippedTests} |\n`;
  md += `| 通过率 | ${passRate}% |\n`;
  md += `| 总耗时 | ${durationStr} |\n\n`;

  md += '## 覆盖率统计\n\n';
  md += '### 菜单覆盖率\n\n';
  md += `- 已测试: ${menuCoverage.tested || 0} / ${menuCoverage.total || 0}\n`;
  md += `- 覆盖率: ${menuCoverage.total > 0 ? ((menuCoverage.tested / menuCoverage.total) * 100).toFixed(2) : '0.00'}%\n\n`;

  md += '### 页面覆盖率\n\n';
  md += `- 已测试: ${pageCoverage.tested || 0} / ${pageCoverage.total || 0}\n`;
  md += `- 覆盖率: ${pageCoverage.total > 0 ? ((pageCoverage.tested / pageCoverage.total) * 100).toFixed(2) : '0.00'}%\n\n`;

  md += '### 按钮覆盖率\n\n';

  function isButtonSummaryObj(obj) {
    return obj && typeof obj === 'object' &&
      (typeof obj.scanned === 'number' || typeof obj.clicked === 'number');
  }

  function isPageGroupedObj(obj) {
    if (!obj || typeof obj !== 'object') return false;
    const keys = Object.keys(obj);
    if (keys.length === 0) return false;
    return keys.every(k => {
      const v = obj[k];
      return v && typeof v === 'object' &&
        (typeof v.scanned === 'number' || typeof v.clicked === 'number');
    });
  }

  let buttonSummary = null;
  let buttonPageDetails = [];

  if (isButtonSummaryObj(buttonCoverage)) {
    buttonSummary = {
      scanned: buttonCoverage.scanned || 0,
      clicked: buttonCoverage.clicked || 0,
      skippedDangerous: buttonCoverage.skippedDangerous || 0,
      skippedDisabled: buttonCoverage.skippedDisabled || 0,
      skippedInvisible: buttonCoverage.skippedInvisible || 0,
      failed: buttonCoverage.failed || 0,
    };
    if (Array.isArray(buttonCoverage.pages) && buttonCoverage.pages.length > 0) {
      buttonPageDetails = buttonCoverage.pages.map(p => ({
        name: p.name || p.route || '未知页面',
        route: p.route || '',
        buttonCount: p.buttonCount || 0,
        clickableCount: p.clickableCount || 0,
      }));
    }
  } else if (isPageGroupedObj(buttonCoverage)) {
    const pageKeys = Object.keys(buttonCoverage);
    buttonSummary = {
      scanned: 0,
      clicked: 0,
      skippedDangerous: 0,
      skippedDisabled: 0,
      skippedInvisible: 0,
      failed: 0,
    };
    buttonPageDetails = pageKeys.map(pageName => {
      const pageData = buttonCoverage[pageName];
      const scanned = pageData.scanned || pageData.total || 0;
      const clicked = pageData.clicked || 0;
      const skippedDangerous = pageData.skippedDangerous || 0;
      const skippedDisabled = pageData.skippedDisabled || 0;
      const skippedInvisible = pageData.skippedInvisible || 0;
      const failed = pageData.failed || 0;

      buttonSummary.scanned += scanned;
      buttonSummary.clicked += clicked;
      buttonSummary.skippedDangerous += skippedDangerous;
      buttonSummary.skippedDisabled += skippedDisabled;
      buttonSummary.skippedInvisible += skippedInvisible;
      buttonSummary.failed += failed;

      return {
        name: pageName,
        scanned,
        clicked,
        skippedDangerous,
        skippedDisabled,
        skippedInvisible,
        failed,
        rate: scanned > 0 ? ((clicked / scanned) * 100).toFixed(2) : '0.00',
      };
    });
  }

  if (!buttonSummary || buttonSummary.scanned === 0) {
    md += '暂无按钮覆盖率数据\n\n';
  } else {
    const totalRate = buttonSummary.scanned > 0
      ? ((buttonSummary.clicked / buttonSummary.scanned) * 100).toFixed(2)
      : '0.00';

    md += '| 指标 | 数量 |\n';
    md += '|------|------|\n';
    md += `| 扫描总数 | ${buttonSummary.scanned} |\n`;
    md += `| 已点击 | ${buttonSummary.clicked} |\n`;
    md += `| 跳过(危险) | ${buttonSummary.skippedDangerous} |\n`;
    md += `| 跳过(禁用) | ${buttonSummary.skippedDisabled} |\n`;
    md += `| 跳过(不可见) | ${buttonSummary.skippedInvisible} |\n`;
    md += `| 失败 | ${buttonSummary.failed} |\n`;
    md += `| 点击率 | ${totalRate}% |\n\n`;

    if (buttonPageDetails.length > 0) {
      md += '<details>\n';
      md += '<summary><strong>各页面明细</strong></summary>\n\n';

      if (buttonPageDetails[0].scanned !== undefined) {
        for (const page of buttonPageDetails) {
          md += `#### ${page.name}\n\n`;
          md += '| 指标 | 数量 |\n';
          md += '|------|------|\n';
          md += `| 扫描总数 | ${page.scanned} |\n`;
          md += `| 已点击 | ${page.clicked} |\n`;
          md += `| 跳过(危险) | ${page.skippedDangerous} |\n`;
          md += `| 跳过(禁用) | ${page.skippedDisabled} |\n`;
          md += `| 跳过(不可见) | ${page.skippedInvisible} |\n`;
          md += `| 失败 | ${page.failed} |\n`;
          md += `| 点击率 | ${page.rate}% |\n\n`;
        }
      } else {
        md += '| 页面 | 按钮数 | 可点击元素数 |\n';
        md += '|------|--------|-------------|\n';
        for (const page of buttonPageDetails) {
          md += `| ${page.name} | ${page.buttonCount} | ${page.clickableCount} |\n`;
        }
        md += '\n';
      }

      md += '</details>\n\n';
    }
  }

  md += '### 接口覆盖率\n\n';
  md += `- 已调用: ${apiCoverage.covered || 0} / ${apiCoverage.total || 0}\n`;
  md += `- 覆盖率: ${apiCoverage.rate || '0.00'}%\n\n`;

  const failedCases = [];
  const collectFailedFromSuites = (suiteList, parentName = '') => {
    for (const suite of suiteList) {
      const suiteName = parentName ? `${parentName} > ${suite.name}` : suite.name;
      if (suite.tests) {
        for (const test of suite.tests) {
          if (test.status === 'failed' || test.status === 'unexpected') {
            const screenshots = test.screenshots || (test.screenshot ? [test.screenshot] : []);
            failedCases.push({
              suite: suiteName,
              name: test.name || test.title || '',
              error: test.error || test.err || {},
              screenshots: screenshots,
              steps: test.steps || [],
              networkRequests: [],
              consoleErrors: [],
            });
          }
        }
      }
      if (suite.suites && suite.suites.length > 0) {
        collectFailedFromSuites(suite.suites, suiteName);
      }
    }
  };
  collectFailedFromSuites(suites);

  for (const failedCase of failedCases) {
    for (const log of networkLog) {
      if (log.type === 'response' && log.status >= 400) {
        failedCase.networkRequests.push(log);
      }
    }
    if (failedCase.networkRequests.length === 0) {
      const lastRequests = networkLog.slice(-20);
      failedCase.networkRequests = lastRequests;
    }

    for (const log of consoleLog) {
      if (log.type === 'error' || log.type === 'pageerror' || log.level === 'error') {
        failedCase.consoleErrors.push(log);
      }
    }
  }

  md += '## 失败用例详情\n\n';
  if (failedCases.length === 0) {
    md += '🎉 所有测试用例均通过！\n\n';
  } else {
    md += '| 序号 | 测试套件 | 用例名 | 错误信息 | 截图数量 |\n';
    md += '|------|----------|--------|----------|----------|\n';
    failedCases.forEach((c, i) => {
      const errorMsg = (c.error.message || '').replace(/\|/g, '\\|').replace(/\n/g, ' ').substring(0, 100);
      const screenshotCount = c.screenshots ? c.screenshots.length : 0;
      md += `| ${i + 1} | ${c.suite} | ${c.name} | ${errorMsg} | ${screenshotCount} |\n`;
    });
    md += '\n';

    md += '### 失败用例详细错误\n\n';
    failedCases.forEach((c, i) => {
      md += `<details>\n<summary><strong>${i + 1}. ${c.suite} - ${c.name}</strong></summary>\n\n`;
      md += '**错误信息:**\n\n';
      md += '```\n';
      md += (c.error.message || c.error.stack || '未知错误') + '\n';
      md += '```\n\n';
      if (c.error.stack) {
        md += '**错误堆栈:**\n\n';
        md += '```\n';
        md += c.error.stack + '\n';
        md += '```\n\n';
      }
      if (c.steps && c.steps.length > 0) {
        md += '**复现步骤:**\n\n';
        c.steps.forEach((step, j) => {
          md += `${j + 1}. ${step}\n`;
        });
        md += '\n';
      }
      if (c.screenshots && c.screenshots.length > 0) {
        md += '**截图:**\n\n';
        c.screenshots.forEach((s, idx) => {
          const relPath = outputDir ? path.relative(outputDir, s) : s;
          md += `${idx + 1}. ![${c.name}-${idx}](${relPath})\n`;
        });
        md += '\n';
      }
      if (c.networkRequests && c.networkRequests.length > 0) {
        md += '**相关网络请求:**\n\n';
        c.networkRequests.slice(0, 10).forEach((req, idx) => {
          const status = req.status || '-';
          const method = req.method || '-';
          md += `${idx + 1}. [${status}] ${method} ${req.url}\n`;
        });
        md += '\n';
      }
      if (c.consoleErrors && c.consoleErrors.length > 0) {
        md += '**相关 Console 错误:**\n\n';
        c.consoleErrors.slice(0, 5).forEach((err, idx) => {
          md += `${idx + 1}. ${(err.text || err.message || '').substring(0, 100)}\n`;
        });
        md += '\n';
      }
      md += '</details>\n\n';
    });
  }

  md += '## 页面截图索引\n\n';
  if (screenshotsDir && fs.existsSync(screenshotsDir)) {
    const screenshots = collectScreenshots(screenshotsDir);
    if (Object.keys(screenshots).length === 0) {
      md += '暂无截图\n\n';
    } else {
      for (const page of Object.keys(screenshots).sort()) {
        md += `### ${page}\n\n`;
        const themes = screenshots[page];
        for (const theme of Object.keys(themes).sort()) {
          md += `#### ${theme}\n\n`;
          const sizes = themes[theme];
          for (const size of Object.keys(sizes).sort()) {
            const files = sizes[size];
            md += `##### ${size}\n\n`;
            files.forEach(file => {
              const relPath = path.relative(outputDir || screenshotsDir, file);
              md += `- ![${path.basename(file)}](${relPath})\n`;
            });
            md += '\n';
          }
        }
      }
    }
  } else {
    md += '暂无截图\n\n';
  }

  md += '## 未覆盖的接口列表\n\n';
  if (apiCoverage.uncoveredApis && apiCoverage.uncoveredApis.length > 0) {
    apiCoverage.uncoveredApis.forEach((api, i) => {
      const apiPath = typeof api === 'string' ? api : (api.pattern || api.path || '');
      md += `${i + 1}. \`${apiPath}\`\n`;
    });
    md += '\n';
  } else {
    md += '✅ 所有接口均已覆盖\n\n';
  }

  md += '## 未覆盖的按钮列表\n\n';
  const uncoveredButtonsByPage = {};

  if (isPageGroupedObj(buttonCoverage)) {
    for (const page of Object.keys(buttonCoverage)) {
      const pageData = buttonCoverage[page];
      if (pageData.unclicked && pageData.unclicked.length > 0) {
        uncoveredButtonsByPage[page] = pageData.unclicked;
      }
    }
  }

  if (Object.keys(uncoveredButtonsByPage).length === 0) {
    md += '✅ 所有按钮均已覆盖\n\n';
  } else {
    for (const page of Object.keys(uncoveredButtonsByPage).sort()) {
      md += `### ${page}\n\n`;
      uncoveredButtonsByPage[page].forEach((btn, i) => {
        md += `${i + 1}. ${btn}\n`;
      });
      md += '\n';
    }
  }

  md += '## Console 错误汇总\n\n';
  const consoleErrors = (consoleLog || []).filter(log => log.type === 'error' || log.level === 'error');
  if (consoleErrors.length === 0) {
    md += '✅ 无 Console 错误\n\n';
  } else {
    md += `共 ${consoleErrors.length} 条 Console 错误\n\n`;
    consoleErrors.slice(0, 50).forEach((err, i) => {
      md += `<details>\n<summary><strong>${i + 1}. ${(err.text || err.message || '').substring(0, 80)}</strong></summary>\n\n`;
      md += '```\n';
      md += (err.text || err.message || '') + '\n';
      if (err.stack) {
        md += err.stack + '\n';
      }
      md += '```\n\n';
      md += `- 页面: ${err.page || '未知'}\n`;
      md += `- 时间: ${err.timestamp || '未知'}\n`;
      md += '\n</details>\n\n';
    });
    if (consoleErrors.length > 50) {
      md += `... 还有 ${consoleErrors.length - 50} 条错误\n\n`;
    }
  }

  return md;
}

function formatDuration(ms) {
  if (!ms || ms < 0) return '0ms';
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  if (hours > 0) {
    return `${hours}h ${minutes % 60}m ${seconds % 60}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds % 60}s`;
  }
  return `${seconds}s`;
}

function collectScreenshots(dir) {
  const result = {};
  if (!fs.existsSync(dir)) return result;

  const files = fs.readdirSync(dir);
  for (const file of files) {
    const fullPath = path.join(dir, file);
    const stat = fs.statSync(fullPath);
    if (stat.isDirectory()) {
      const subResult = collectScreenshots(fullPath);
      for (const page of Object.keys(subResult)) {
        if (!result[page]) result[page] = {};
        for (const theme of Object.keys(subResult[page])) {
          if (!result[page][theme]) result[page][theme] = {};
          for (const size of Object.keys(subResult[page][theme])) {
            if (!result[page][theme][size]) result[page][theme][size] = [];
            result[page][theme][size].push(...subResult[page][theme][size]);
          }
        }
      }
    } else if (/\.(png|jpg|jpeg|gif|webp)$/i.test(file)) {
      const parsed = parseScreenshotFilename(file);
      const page = parsed.page || '未分类';
      const theme = parsed.theme || '默认';
      const size = parsed.size || '默认尺寸';
      if (!result[page]) result[page] = {};
      if (!result[page][theme]) result[page][theme] = {};
      if (!result[page][theme][size]) result[page][theme][size] = [];
      result[page][theme][size].push(fullPath);
    }
  }
  return result;
}

function parseScreenshotFilename(filename) {
  const name = path.basename(filename, path.extname(filename));
  const parts = name.split('_');
  const result = {
    page: '',
    theme: '',
    size: ''
  };

  if (parts.length >= 1) {
    result.page = parts[0];
  }
  if (parts.length >= 2) {
    result.theme = parts[1];
  }
  if (parts.length >= 3) {
    result.size = parts[2];
  }

  return result;
}

if (require.main === module) {
  const projectRoot = path.resolve(__dirname, '../../..');
  const outputDir = path.join(projectRoot, 'test-results');
  const screenshotsDir = path.join(outputDir, 'screenshots');
  const resultsPath = path.join(outputDir, 'json-report', 'test-results.json');

  const testResults = loadJson(resultsPath) || {
    suites: [],
    summary: { total: 0, passed: 0, failed: 0, skipped: 0, duration: 0 },
    buttonCoverage: {},
    networkLog: [],
    consoleLog: [],
    menuList: [],
    menuCoverage: { tested: 0, total: 0 },
    pageMeta: [],
    pageList: [],
    pageCoverage: { tested: 0, total: 0 },
    apiList: [],
    apiCoverage: { total: 0, covered: 0, uncovered: 0, rate: '0.00', coveredApis: [], uncoveredApis: [] }
  };

  const options = {
    baseUrl: process.env.BASE_URL || 'http://127.0.0.1:18092',
    browserName: process.env.BROWSER || 'chromium',
    outputDir: outputDir,
    screenshotsDir: screenshotsDir
  };

  const report = generateReport(testResults, options);
  const reportPath = path.join(outputDir, 'report.md');

  if (!fs.existsSync(outputDir)) {
    fs.mkdirSync(outputDir, { recursive: true });
  }
  fs.writeFileSync(reportPath, report, 'utf-8');
  console.log(`✅ 报告已生成: ${reportPath}`);
}

module.exports = {
  generateReport,
  calculateApiCoverage,
  matchApiPattern,
  loadJson,
  saveJson
};
