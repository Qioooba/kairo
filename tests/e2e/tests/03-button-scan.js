'use strict';

const fs = require('fs');
const path = require('path');

function ensureDir(dirPath) {
  if (!fs.existsSync(dirPath)) {
    fs.mkdirSync(dirPath, { recursive: true });
  }
}

function register(runner, ctx) {
  const { page, baseUrl, consoleLogs, networkLogs, screenshotsDir, data, helpers, buttonCoverage } = ctx;
  const { pageMeta, DANGEROUS_BUTTON_PATTERNS } = data;

  const pages = Object.keys(pageMeta).map(function (route) {
    return {
      route,
      ...pageMeta[route],
    };
  });

  function isDangerButton(text) {
    if (!text) return false;
    const trimmed = text.trim();
    return DANGEROUS_BUTTON_PATTERNS.some(function (pattern) {
      return trimmed.indexOf(pattern) >= 0;
    });
  }

  const stats = {
    scanned: 0,
    clicked: 0,
    skippedDangerous: 0,
    skippedDisabled: 0,
    skippedInvisible: 0,
    failed: 0,
  };

  async function closeAllDialogs(pg) {
    try {
      const dialogSelectors = [
        '.otb-dialog-overlay',
        '.otb-modal-overlay',
        '.modal-overlay',
        '.dialog-overlay',
        '[class*="dialog-overlay"]',
        '[class*="modal-overlay"]',
      ];

      for (let attempt = 0; attempt < 3; attempt++) {
        let dialogFound = false;

        for (const selector of dialogSelectors) {
          const overlay = await pg.$(selector);
          if (overlay) {
            const isVisible = await overlay.isVisible().catch(() => false);
            if (isVisible) {
              dialogFound = true;
              const closeSelectors = [
                '.otb-dialog .close',
                '.otb-modal .close',
                '.dialog-close',
                '.modal-close',
                'button:has-text("关闭")',
                'button:has-text("取消")',
                '.otb-dialog-footer button:last-child',
                '.modal-footer button:last-child',
              ];

              for (const closeSel of closeSelectors) {
                const closeBtn = await overlay.$(closeSel);
                if (closeBtn) {
                  try {
                    await closeBtn.click({ timeout: 1000 });
                    await pg.waitForTimeout(200);
                    break;
                  } catch (e) {}
                }
              }

              try {
                await pg.keyboard.press('Escape');
                await pg.waitForTimeout(200);
              } catch (e) {}
            }
          }
        }

        if (!dialogFound) break;
      }
    } catch (e) {}
  }

  async function hasOpenDialog(pg) {
    try {
      const dialogSelectors = [
        '.otb-dialog-overlay',
        '.otb-modal-overlay',
        '.modal-overlay',
        '.dialog-overlay',
        '[class*="dialog-overlay"]',
        '[class*="modal-overlay"]',
      ];

      for (const selector of dialogSelectors) {
        const overlay = await pg.$(selector);
        if (overlay) {
          const isVisible = await overlay.isVisible().catch(() => false);
          if (isVisible) return true;
        }
      }
      return false;
    } catch (e) {
      return false;
    }
  }

  async function restorePageState(pg, originalUrl, originalHash) {
    try {
      await closeAllDialogs(pg);

      const currentHash = await pg.evaluate(() => location.hash);
      if (currentHash !== originalHash) {
        await pg.evaluate((h) => { location.hash = h; }, originalHash);
        await pg.waitForFunction((h) => location.hash === h, originalHash, { timeout: 3000 });
        await pg.waitForTimeout(200);
      }

      await closeAllDialogs(pg);
    } catch (e) {}
  }

  buttonCoverage.scanned = 0;
  buttonCoverage.clicked = 0;
  buttonCoverage.skippedDangerous = 0;
  buttonCoverage.skippedDisabled = 0;
  buttonCoverage.skippedInvisible = 0;
  buttonCoverage.failed = 0;
  buttonCoverage.pages = [];
  buttonCoverage.generatedAt = null;
  buttonCoverage.totalPages = 0;

  runner.describe('按钮扫描与点击测试', function () {
    runner.describe('按钮扫描', function () {
      for (const pg of pages) {
        (function (pageInfo) {
          runner.it('扫描页面 "' + pageInfo.name + '" 的所有按钮', async function () {
            await page.evaluate(function (route) {
              location.hash = '#/' + route;
            }, pageInfo.route);

            await page.waitForFunction(function (route) {
              return location.hash === '#/' + route;
            }, pageInfo.route, { timeout: 5000 });

            await page.waitForTimeout(500);

            const buttonsInfo = await page.evaluate(function () {
              const view = document.getElementById('view');
              if (!view) return { buttons: [], clickable: [] };

              const buttons = Array.from(view.querySelectorAll('button'));
              const clickableSelectors = [
                'button',
                'a[role="button"]',
                '.btn',
                '.tool-card',
                '[role="button"]',
                'input[type="button"]',
                'input[type="submit"]',
              ];

              const clickable = Array.from(view.querySelectorAll(clickableSelectors.join(',')));

              function getElementInfo(el, index) {
                const text = el.textContent.trim().replace(/\s+/g, ' ').slice(0, 100);
                return {
                  index: index,
                  tag: el.tagName.toLowerCase(),
                  text: text,
                  className: el.className,
                  id: el.id,
                  disabled: el.disabled || false,
                  visible: el.offsetParent !== null,
                  type: el.getAttribute('type') || null,
                };
              }

              return {
                buttons: buttons.map(function (b, i) { return getElementInfo(b, i); }),
                clickable: clickable.map(function (c, i) { return getElementInfo(c, i); }),
              };
            });

            const pageButtonData = {
              route: pageInfo.route,
              name: pageInfo.name,
              buttonCount: buttonsInfo.buttons.length,
              clickableCount: buttonsInfo.clickable.length,
              buttons: buttonsInfo.buttons,
              clickable: buttonsInfo.clickable,
            };

            buttonCoverage.pages.push(pageButtonData);
            buttonCoverage.totalPages++;
            stats.scanned += buttonsInfo.buttons.length;
            buttonCoverage.scanned = stats.scanned;

            if (buttonsInfo.buttons.length === 0 && buttonsInfo.clickable.length === 0) {
              console.warn('  警告: 页面 "' + pageInfo.name + '" 未扫描到按钮或可点击元素');
            }
          });
        })(pg);
      }
    });

    runner.describe('按钮点击测试', function () {
      for (const pg of pages) {
        (function (pageInfo) {
          runner.describe('页面: ' + pageInfo.name, function () {
            let pageButtons = [];

            runner.beforeAll(async function () {
              await page.evaluate(function (route) {
                location.hash = '#/' + route;
              }, pageInfo.route);

              await page.waitForFunction(function (route) {
                return location.hash === '#/' + route;
              }, pageInfo.route, { timeout: 5000 });

              await page.waitForTimeout(500);

              const buttons = await page.evaluate(function () {
                const view = document.getElementById('view');
                if (!view) return [];

                const btns = Array.from(view.querySelectorAll('button'));
                return btns.map(function (btn, i) {
                  return {
                    index: i,
                    text: btn.textContent.trim().replace(/\s+/g, ' ').slice(0, 100),
                    disabled: btn.disabled || false,
                    visible: btn.offsetParent !== null,
                    className: btn.className,
                  };
                });
              });

              pageButtons = buttons;
            });

            for (let i = 0; i < 60; i++) {
              (function (btnIndex) {
                const testName = '按钮点击测试 #' + (btnIndex + 1);

                const testFn = async function () {
                  if (btnIndex >= pageButtons.length) {
                    stats.skippedInvisible++;
                    buttonCoverage.skippedInvisible = stats.skippedInvisible;
                    return;
                  }

                  const btn = pageButtons[btnIndex];
                  if (!btn) {
                    stats.skippedInvisible++;
                    buttonCoverage.skippedInvisible = stats.skippedInvisible;
                    return;
                  }

                  if (!btn.visible) {
                    stats.skippedInvisible++;
                    buttonCoverage.skippedInvisible = stats.skippedInvisible;
                    return;
                  }

                  if (btn.disabled) {
                    stats.skippedDisabled++;
                    buttonCoverage.skippedDisabled = stats.skippedDisabled;
                    return;
                  }

                  const isDanger = isDangerButton(btn.text);
                  if (isDanger) {
                    stats.skippedDangerous++;
                    buttonCoverage.skippedDangerous = stats.skippedDangerous;
                    return;
                  }

                  const originalHash = await page.evaluate(() => location.hash);
                  const originalUrl = page.url();

                  const beforeErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;

                  let clicked = false;
                  try {
                    const clickSuccess = await page.evaluate(function (idx) {
                      const view = document.getElementById('view');
                      if (!view) return false;
                      const btns = Array.from(view.querySelectorAll('button'));
                      if (idx >= btns.length) return false;
                      const targetBtn = btns[idx];
                      if (targetBtn.disabled) return false;
                      targetBtn.click();
                      return true;
                    }, btnIndex);

                    if (clickSuccess) {
                      clicked = true;
                      stats.clicked++;
                      buttonCoverage.clicked = stats.clicked;
                    }
                  } catch (e) {
                    stats.failed++;
                    buttonCoverage.failed = stats.failed;
                  }

                  await page.waitForTimeout(500);

                  const dialogOpened = await hasOpenDialog(page);

                  await restorePageState(page, originalUrl, originalHash);

                  const afterErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;
                  if (afterErrors > beforeErrors) {
                    const newErrors = consoleLogs.slice(beforeErrors).filter(function (l) { return l.type === 'error'; });
                    throw new Error('按钮点击后出现 console error: ' + newErrors.map(function (e) { return e.text; }).join('; '));
                  }
                };

                if (i < 50) {
                  runner.it(testName, testFn);
                } else {
                  runner.it.skip(testName + ' (超出限制)', testFn);
                }
              })(i);
            }

            runner.it('危险按钮标记检查', async function () {
              const dangerButtons = pageButtons.filter(function (b) {
                return isDangerButton(b.text);
              });

              if (dangerButtons.length > 0) {
                console.log('    发现危险按钮: ' + dangerButtons.map(function (b) { return b.text; }).join(', '));
              }
            });
          });
        })(pg);
      }
    });

    runner.describe('按钮覆盖率统计', function () {
      runner.it('生成按钮覆盖率数据', async function () {
        buttonCoverage.generatedAt = new Date().toISOString();
        buttonCoverage.scanned = stats.scanned;
        buttonCoverage.clicked = stats.clicked;
        buttonCoverage.skippedDangerous = stats.skippedDangerous;
        buttonCoverage.skippedDisabled = stats.skippedDisabled;
        buttonCoverage.skippedInvisible = stats.skippedInvisible;
        buttonCoverage.failed = stats.failed;

        const resultsDir = path.join(process.cwd(), 'test-results');
        ensureDir(resultsDir);

        const coveragePath = path.join(resultsDir, 'button-coverage.json');
        fs.writeFileSync(coveragePath, JSON.stringify(buttonCoverage, null, 2));

        console.log('    按钮覆盖率数据已写入: ' + coveragePath);
        console.log('    总页面数: ' + buttonCoverage.totalPages);
        console.log('    扫描总数: ' + stats.scanned);
        console.log('    成功点击: ' + stats.clicked);
        console.log('    危险跳过: ' + stats.skippedDangerous);
        console.log('    禁用跳过: ' + stats.skippedDisabled);
        console.log('    不可见跳过: ' + stats.skippedInvisible);
        console.log('    点击失败: ' + stats.failed);
      });
    });

    runner.afterAll(async function () {
      try {
        await page.evaluate(() => { location.hash = '#/home'; });
        await page.waitForFunction(() => location.hash === '#/home', { timeout: 3000 });
        await closeAllDialogs(page);
      } catch (e) {}
    });
  });
}

module.exports = { register };
