'use strict';

function register(runner, ctx) {
  const { page, baseUrl, consoleLogs, data } = ctx;
  const { menuList, homeCards } = data;

  runner.describe('菜单导航测试', function () {
    runner.describe('菜单项存在性检查', function () {
      runner.it('所有菜单项都存在于导航栏', async function () {
        await page.goto(baseUrl + '/', { waitUntil: 'domcontentloaded' });
        await page.waitForSelector('#nav .nav-item', { timeout: 5000 });

        const navItems = await page.$$eval('#nav .nav-item', function (items) {
          return items.map(function (item) {
            const link = item.querySelector('a');
            return {
              route: item.getAttribute('data-route') || (link && link.getAttribute('data-route')),
              text: item.textContent.trim(),
            };
          });
        });

        for (const menuItem of menuList) {
          const found = navItems.some(function (n) {
            return n.route === menuItem.route;
          });
          if (!found) {
            throw new Error('菜单项不存在: ' + menuItem.name + ' (route: ' + menuItem.route + ')');
          }
        }
      });

      runner.it('菜单项数量正确', async function () {
        const count = await page.$$eval('#nav .nav-item', function (items) {
          return items.length;
        });
        if (count < menuList.length) {
          throw new Error('菜单项数量不足，期望至少 ' + menuList.length + ' 个，实际 ' + count + ' 个');
        }
      });
    });

    runner.describe('菜单项点击与路由跳转', function () {
      for (const menuItem of menuList) {
        (function (item) {
          runner.it('点击菜单 "' + item.name + '" 路由跳转正确', async function () {
            const beforeErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;

            const selector = '#nav .nav-item[data-route="' + item.route + '"] a, #nav a[data-route="' + item.route + '"]';
            await page.waitForSelector(selector, { timeout: 5000 });
            await page.click(selector);

            await page.waitForFunction(function (route) {
              return location.hash === '#/' + route;
            }, item.route, { timeout: 5000 });

            const currentHash = await page.evaluate(function () {
              return location.hash;
            });
            if (currentHash !== '#/' + item.route) {
              throw new Error('路由跳转错误，期望 #/' + item.route + '，实际 ' + currentHash);
            }

            const afterErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;
            if (afterErrors > beforeErrors) {
              const newErrors = consoleLogs.slice(beforeErrors).filter(function (l) { return l.type === 'error'; });
              throw new Error('点击后出现 console error: ' + newErrors.map(function (e) { return e.text; }).join('; '));
            }
          });
        })(menuItem);
      }
    });

    runner.describe('菜单高亮状态', function () {
      for (const menuItem of menuList) {
        (function (item) {
          runner.it('菜单 "' + item.name + '" 激活后有 active 类', async function () {
            const selector = '#nav .nav-item[data-route="' + item.route + '"], #nav a[data-route="' + item.route + '"]';

            await page.evaluate(function (route) {
              location.hash = '#/' + route;
            }, item.route);

            await page.waitForFunction(function (route) {
              return location.hash === '#/' + route;
            }, item.route, { timeout: 5000 });

            await page.waitForTimeout(200);

            const hasActive = await page.evaluate(function (route) {
              const navItem = document.querySelector('.nav-item[data-route="' + route + '"]');
              if (navItem) {
                return navItem.classList.contains('active');
              }
              const link = document.querySelector('a[data-route="' + route + '"]');
              if (link) {
                const parent = link.closest('.nav-item');
                return parent && parent.classList.contains('active');
              }
              return false;
            }, item.route);

            if (!hasActive) {
              throw new Error('菜单项 "' + item.name + '" 激活后没有 active 类');
            }
          });
        })(menuItem);
      }
    });

    runner.describe('首页卡片点击跳转', function () {
      for (const card of homeCards) {
        (function (c) {
          runner.it('首页卡片 "' + c.name + '" 点击跳转正确', async function () {
            await page.evaluate(function () {
              location.hash = '#/home';
            });
            await page.waitForFunction(function () {
              return location.hash === '#/home';
            }, null, { timeout: 5000 });
            await page.waitForTimeout(300);

            const beforeErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;

            await page.waitForSelector('.tool-card', { timeout: 5000 });

            const cardExists = await page.evaluate(function (name) {
              const cards = Array.from(document.querySelectorAll('.tool-card'));
              return cards.some(function (card) {
                return card.textContent.includes(name);
              });
            }, c.name);

            if (!cardExists) {
              throw new Error('首页卡片不存在: ' + c.name);
            }

            await page.evaluate(function (name) {
              const cards = Array.from(document.querySelectorAll('.tool-card'));
              const target = cards.find(function (card) {
                return card.textContent.includes(name);
              });
              if (target) {
                target.click();
              }
            }, c.name);

            await page.waitForFunction(function (route) {
              return location.hash === '#/' + route;
            }, c.route, { timeout: 5000 });

            const currentHash = await page.evaluate(function () {
              return location.hash;
            });
            if (currentHash !== '#/' + c.route) {
              throw new Error('卡片点击跳转错误，期望 #/' + c.route + '，实际 ' + currentHash);
            }

            const afterErrors = consoleLogs.filter(function (l) { return l.type === 'error'; }).length;
            if (afterErrors > beforeErrors) {
              const newErrors = consoleLogs.slice(beforeErrors).filter(function (l) { return l.type === 'error'; });
              throw new Error('卡片点击后出现 console error: ' + newErrors.map(function (e) { return e.text; }).join('; '));
            }
          });
        })(card);
      }
    });
  });
}

module.exports = { register };
