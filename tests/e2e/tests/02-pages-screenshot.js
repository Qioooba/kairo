'use strict';

const fs = require('fs');
const path = require('path');

function ensureDir(dirPath) {
  if (!fs.existsSync(dirPath)) {
    fs.mkdirSync(dirPath, { recursive: true });
  }
}

function register(runner, ctx) {
  const { page, baseUrl, consoleLogs, screenshotsDir, data } = ctx;
  const { pageMeta, themes } = data;

  const pages = Object.keys(pageMeta).map(function (route) {
    return {
      route,
      ...pageMeta[route],
    };
  });

  runner.describe('全页面截图测试', function () {
    runner.describe('页面加载与内容检查', function () {
      for (const pg of pages) {
        (function (pageInfo) {
          runner.it('页面 "' + pageInfo.name + '" 加载测试', async function () {
            const beforeErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;

            await page.evaluate(function (route) {
              location.hash = '#/' + route;
            }, pageInfo.route);

            await page.waitForFunction(function (route) {
              return location.hash === '#/' + route;
            }, pageInfo.route, { timeout: 5000 });

            await page.waitForTimeout(500);

            const viewContent = await page.evaluate(function () {
              const view = document.getElementById('view');
              if (!view) {
                return { exists: false };
              }
              return {
                exists: true,
                hasChildren: view.children.length > 0,
                textLength: view.innerText.trim().length,
                visible: view.offsetParent !== null,
              };
            });

            if (!viewContent.exists) {
              throw new Error('页面 #view 容器不存在');
            }
            if (!viewContent.hasChildren) {
              throw new Error('页面内容区域无任何子元素');
            }
            if (viewContent.textLength === 0) {
              throw new Error('页面内容区域文本为空');
            }

            // v1.x 去顶栏：面包屑 #crumbs 已随 topbar 一并移除，
            // 当前页名改由 Tab 条的活动 Tab 承载，这里断言它非空即可。
            const crumbText = await page.evaluate(function () {
              const label = document.querySelector('.app-tab.active .app-tab-label');
              return label ? label.textContent.trim() : null;
            });

            if (!crumbText) {
              throw new Error('活动 Tab 文案不存在或为空（当前页名标识缺失）');
            }

            const bodyHasContent = await page.evaluate(function () {
              const body = document.body;
              const text = body.innerText.trim();
              return text.length > 100;
            });

            if (!bodyHasContent) {
              throw new Error('页面 body 内容过少，可能存在白屏问题');
            }

            const pageDir = path.join(screenshotsDir, pageInfo.route);
            ensureDir(pageDir);

            const shotPath = path.join(pageDir, pageInfo.route + '-default.png');
            await page.screenshot({ path: shotPath, fullPage: true });

            const afterErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;
            if (afterErrors > beforeErrors) {
              const newErrors = consoleLogs.slice(beforeErrors).filter(function (l) { return l.type === 'error'; });
              throw new Error('页面加载后出现 console error: ' + newErrors.map(function (e) { return e.text; }).join('; '));
            }
          });
        })(pg);
      }
    });

    runner.describe('多主题截图测试', function () {
      for (const theme of themes) {
        (function (themeName) {
          runner.describe('主题: ' + themeName, function () {
            for (const pg of pages) {
              (function (pageInfo) {
                runner.it('页面 "' + pageInfo.name + '" 截图', async function () {
                  const beforeErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;

                  await page.evaluate(function (theme) {
                    if (window.Kairo && window.Kairo.theme) {
                      window.Kairo.theme.set(theme);
                    } else {
                      document.documentElement.setAttribute('data-theme', theme);
                    }
                  }, themeName);

                  await page.waitForTimeout(200);

                  await page.evaluate(function (route) {
                    location.hash = '#/' + route;
                  }, pageInfo.route);

                  await page.waitForFunction(function (route) {
                    return location.hash === '#/' + route;
                  }, pageInfo.route, { timeout: 5000 });

                  await page.waitForTimeout(500);

                  const viewExists = await page.evaluate(function () {
                    const view = document.getElementById('view');
                    return view && view.children.length > 0;
                  });

                  if (!viewExists) {
                    throw new Error('页面 #view 内容未渲染');
                  }

                  const pageDir = path.join(screenshotsDir, pageInfo.route);
                  ensureDir(pageDir);

                  const shotPath = path.join(pageDir, pageInfo.route + '-' + themeName + '.png');
                  await page.screenshot({ path: shotPath, fullPage: true });

                  const afterErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;
                  if (afterErrors > beforeErrors) {
                    const newErrors = consoleLogs.slice(beforeErrors).filter(function (l) { return l.type === 'error'; });
                    throw new Error('主题切换后出现 console error: ' + newErrors.map(function (e) { return e.text; }).join('; '));
                  }
                });
              })(pg);
            }
          });
        })(theme);
      }
    });

    runner.describe('页面白屏检测', function () {
      for (const pg of pages) {
        (function (pageInfo) {
          runner.it('页面 "' + pageInfo.name + '" 非白屏检查', async function () {
            await page.evaluate(function (route) {
              location.hash = '#/' + route;
            }, pageInfo.route);

            await page.waitForFunction(function (route) {
              return location.hash === '#/' + route;
            }, pageInfo.route, { timeout: 5000 });

            await page.waitForTimeout(1500);

            const bodyCheck = await page.evaluate(function () {
              const body = document.body;
              const html = document.documentElement;

              const bodyText = body.innerText.trim();
              const view = document.getElementById('view');
              const nav = document.getElementById('nav');

              const bodyBg = getComputedStyle(body).backgroundColor;
              const htmlBg = getComputedStyle(html).backgroundColor;

              const viewHasContent = view && view.children.length > 0 && view.innerText.trim().length > 0;
              const navHasContent = nav && nav.children.length > 0;

              const mainSelectors = [
                '.card', '.tool-card', 'table', 'form', '.section-title',
                'button', 'input', 'textarea', 'select',
                '[class*="grid-"]', '.mb-3', '.mt-4',
                'pre', 'code', '.logs-container', '.file-list'
              ];
              let mainCount = 0;
              for (const sel of mainSelectors) {
                mainCount += document.querySelectorAll(sel).length;
              }
              const hasMainContent = mainCount > 0;

              return {
                bodyTextLength: bodyText.length,
                viewHasContent: viewHasContent,
                navHasContent: navHasContent,
                hasMainContent: hasMainContent,
                mainElementCount: mainCount,
                bodyBg: bodyBg,
                htmlBg: htmlBg,
              };
            });

            if (bodyCheck.bodyTextLength < 50) {
              throw new Error('页面 body 文本过少 (' + bodyCheck.bodyTextLength + ' 字符)，疑似白屏');
            }

            if (!bodyCheck.viewHasContent) {
              throw new Error('页面 #view 区域无内容');
            }

            if (!bodyCheck.hasMainContent) {
              throw new Error('页面未检测到主要内容元素');
            }
          });
        })(pg);
      }
    });
  });
}

module.exports = { register };
