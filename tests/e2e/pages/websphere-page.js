'use strict';

const BasePage = require('./base-page');

const TAB_NAMES = {
  files: '📁 文件 / 下载',
  search: '🔍 搜索排障',
  tail: '📺 实时跟踪',
};

class WebSpherePage extends BasePage {
  constructor(page, baseUrl) {
    super(page, baseUrl);
  }

  async goto() {
    await super.goto('/websphere');
  }

  async selectFirstSystem() {
    await this.ensureNoOverlay();
    try {
      const sysSelect = await this.findFirstVisible(['select#ws-sys', '#ws-sys']);
      if (!sysSelect) {
        await this.takeStateScreenshot('ws-select-system-error');
        const summary = await this.getDomSummary();
        throw new Error('系统选择下拉框未找到: ' + JSON.stringify(summary.inputs));
      }
      const options = await this.page.$$eval('#ws-sys option', (els) =>
        els.map((e) => e.value || e.textContent).filter(Boolean)
      );
      if (options.length === 0) {
        throw new Error('系统选项为空');
      }
      await this.page.selectOption('#ws-sys', options[0]);
      await this.waitForTimeout(300);
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-select-system-fail');
      throw e;
    }
  }

  async selectFirstServer() {
    await this.ensureNoOverlay();
    try {
      const checkboxSelectors = [
        'input[type="checkbox"][data-srv]',
        '#ws-server-list input[type="checkbox"]',
        '.server-list input[type="checkbox"]',
        '.ws-servers input[type="checkbox"]',
        'input[type="checkbox"][name="server"]',
      ];
      let firstCheckbox = null;
      for (const sel of checkboxSelectors) {
        const checkboxes = await this.page.$$(sel);
        if (checkboxes.length > 0) {
          firstCheckbox = checkboxes[0];
          break;
        }
      }
      if (!firstCheckbox) {
        await this.takeStateScreenshot('ws-select-server-error');
        const summary = await this.getDomSummary();
        throw new Error('服务器复选框未找到: ' + JSON.stringify(summary.inputs.slice(0, 10)));
      }
      await firstCheckbox.check();
      await this.waitForTimeout(200);
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-select-server-fail');
      throw e;
    }
  }

  async selectFirstDir() {
    await this.ensureNoOverlay();
    try {
      const dirSelect = await this.findFirstVisible(['select#ws-dir', '#ws-dir']);
      if (!dirSelect) {
        return false;
      }
      const options = await this.page.$$eval('#ws-dir option', (els) =>
        els.map((e) => e.value || e.textContent).filter(Boolean)
      );
      if (options.length > 0) {
        await this.page.selectOption('#ws-dir', options[0]);
        await this.waitForTimeout(200);
        return true;
      }
      return false;
    } catch (e) {
      await this.takeStateScreenshot('ws-select-dir-fail');
      throw e;
    }
  }

  async fillCredential(username, password) {
    await this.ensureNoOverlay();
    try {
      const userSelectors = [
        'input#ws-user',
        '#ws-user',
        'input[placeholder*="用户名"]',
        'input[name="username"]',
      ];
      const passSelectors = [
        'input#ws-pass',
        '#ws-pass',
        'input[placeholder*="密码"]',
        'input[type="password"]',
      ];
      const userFilled = await this.fillFirstVisible(userSelectors, username);
      const passFilled = await this.fillFirstVisible(passSelectors, password);
      if (!userFilled || !passFilled) {
        await this.takeStateScreenshot('ws-fill-cred-error');
        const summary = await this.getDomSummary();
        throw new Error('用户名或密码输入框未找到: ' + JSON.stringify(summary.inputs.slice(0, 10)));
      }
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-fill-cred-fail');
      throw e;
    }
  }

  async testConnection() {
    await this.ensureNoOverlay();
    try {
      const btnSelectors = [
        'button:has-text("测试连接")',
        'button:has-text("测试 SSH 连接")',
        '.ws-test-btn',
        '#ws-test-btn',
      ];
      const clicked = await this.clickFirstVisible(btnSelectors);
      if (!clicked) {
        await this.takeStateScreenshot('ws-test-conn-btn-error');
        const summary = await this.getDomSummary();
        throw new Error('测试连接按钮未找到: ' + JSON.stringify(summary.buttons.slice(0, 15)));
      }
      const successSelectors = [
        'span.dot-ok',
        '.dot-success',
        '.status-ok',
        '.text-success:has-text("成功")',
        '.toast:has-text("成功")',
        '[class*="success"]',
      ];
      for (const sel of successSelectors) {
        try {
          await this.page.waitForSelector(sel, { timeout: 15000 });
          const el = await this.page.$(sel);
          if (el) {
            const visible = await el.isVisible().catch(() => false);
            if (visible) {
              return true;
            }
          }
        } catch (e) {
          // continue
        }
      }
      await this.takeStateScreenshot('ws-test-conn-timeout');
      return false;
    } catch (e) {
      await this.takeStateScreenshot('ws-test-conn-fail');
      throw e;
    }
  }

  async listFiles() {
    await this.ensureNoOverlay();
    try {
      const btnSelectors = [
        'button:has-text("列出文件")',
        'button:has-text("查询文件")',
        '#ws-list-btn',
        '.ws-list-btn',
      ];
      const clicked = await this.clickFirstVisible(btnSelectors);
      if (!clicked) {
        await this.takeStateScreenshot('ws-list-files-btn-error');
        const summary = await this.getDomSummary();
        throw new Error('列出文件按钮未找到: ' + JSON.stringify(summary.buttons.slice(0, 15)));
      }
      const tableSelectors = [
        'table.table tbody tr',
        'table tbody tr',
        '.file-list .file-item',
        '#ws-file-list tr',
        '.ws-files tr',
      ];
      let found = false;
      for (const sel of tableSelectors) {
        try {
          await this.page.waitForSelector(sel, { timeout: 15000 });
          const rows = await this.page.$$(sel);
          if (rows.length > 0) {
            found = true;
            break;
          }
        } catch (e) {
          // continue
        }
      }
      if (!found) {
        await this.takeStateScreenshot('ws-list-files-timeout');
        return false;
      }
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-list-files-fail');
      throw e;
    }
  }

  async getFileCount() {
    const tableSelectors = [
      'table.table tbody tr',
      'table tbody tr',
      '.file-list .file-item',
      '#ws-file-list tr',
      '.ws-files tr',
    ];
    for (const sel of tableSelectors) {
      const rows = await this.page.$$(sel);
      if (rows.length > 0) {
        return rows.length;
      }
    }
    return 0;
  }

  async filterFiles(pattern) {
    await this.ensureNoOverlay();
    try {
      const filterSelectors = [
        'input#ws-file-pattern',
        '#ws-file-pattern',
        'input[placeholder*="文件名"]',
        'input[placeholder*="过滤"]',
        '.file-filter input',
        '.ws-filter input',
      ];
      const filled = await this.fillFirstVisible(filterSelectors, pattern);
      if (!filled) {
        return false;
      }
      await this.waitForTimeout(300);
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-filter-files-fail');
      throw e;
    }
  }

  async hasTab(tabName) {
    try {
      const displayName = TAB_NAMES[tabName] || tabName;
      const tabSelectors = [
        `.ws-tab-item:has-text("${displayName}")`,
        `.tab-item:has-text("${displayName}")`,
        `[role="tab"]:has-text("${displayName}")`,
        `.nav-tabs a:has-text("${displayName}")`,
        `.tabs [data-tab="${tabName}"]`,
      ];
      for (const sel of tabSelectors) {
        const el = await this.page.$(sel);
        if (el) {
          const visible = await el.isVisible().catch(() => false);
          if (visible) return true;
        }
      }
      return false;
    } catch (e) {
      return false;
    }
  }

  async switchTab(tabName) {
    await this.ensureNoOverlay();
    try {
      const displayName = TAB_NAMES[tabName] || tabName;
      const tabSelectors = [
        `.ws-tab-item:has-text("${displayName}")`,
        `.tab-item:has-text("${displayName}")`,
        `[role="tab"]:has-text("${displayName}")`,
        `.nav-tabs a:has-text("${displayName}")`,
        `.tabs [data-tab="${tabName}"]`,
        `text=${displayName}`,
      ];
      if (tabName === 'files') {
        tabSelectors.push('.ws-tab-item:has-text("文件")', '.ws-tab-item:has-text("下载")');
        tabSelectors.push('text=文件 / 下载', 'text=文件', 'text=下载');
      } else if (tabName === 'search') {
        tabSelectors.push('.ws-tab-item:has-text("搜索")');
        tabSelectors.push('text=搜索排障', 'text=搜索');
      } else if (tabName === 'tail') {
        tabSelectors.push('.ws-tab-item:has-text("跟踪")', '.ws-tab-item:has-text("tail")', '.ws-tab-item:has-text("Tail")');
        tabSelectors.push('text=实时跟踪', 'text=Tail', 'text=tail');
      }
      const clicked = await this.clickFirstVisible(tabSelectors);
      if (!clicked) {
        await this.takeStateScreenshot('ws-switch-tab-error');
        const summary = await this.getDomSummary();
        throw new Error('Tab 未找到: ' + displayName + ' - ' + JSON.stringify(summary.tabs));
      }
      await this.waitForTimeout(300);
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-switch-tab-fail');
      throw e;
    }
  }

  async searchLogs(keyword) {
    await this.ensureNoOverlay();
    try {
      await this.switchTab('search');
      const querySelectors = [
        'input#ws-query',
        '#ws-query',
        'input[placeholder*="关键字"]',
        'input[placeholder*="搜索"]',
        '.search-query input',
        '.ws-search input[type="text"]',
      ];
      const filled = await this.fillFirstVisible(querySelectors, keyword);
      if (!filled) {
        await this.takeStateScreenshot('ws-search-input-error');
        const summary = await this.getDomSummary();
        throw new Error('搜索输入框未找到: ' + JSON.stringify(summary.inputs.slice(0, 10)));
      }
      const btnSelectors = [
        'button:has-text("开始搜索")',
        'button:has-text("搜索")',
        '#ws-search-btn',
        '.ws-search-btn',
      ];
      const clicked = await this.clickFirstVisible(btnSelectors);
      if (!clicked) {
        await this.takeStateScreenshot('ws-search-btn-error');
        const summary = await this.getDomSummary();
        throw new Error('搜索按钮未找到: ' + JSON.stringify(summary.buttons.slice(0, 15)));
      }
      const resultSelectors = [
        '.server-group',
        '.search-result',
        '#ws-search-results',
        '.ws-search-result',
        'table.search-result',
      ];
      let found = false;
      for (const sel of resultSelectors) {
        try {
          await this.page.waitForSelector(sel, { timeout: 15000 });
          const el = await this.page.$(sel);
          if (el) {
            const visible = await el.isVisible().catch(() => false);
            if (visible) {
              found = true;
              break;
            }
          }
        } catch (e) {
          // continue
        }
      }
      return found;
    } catch (e) {
      await this.takeStateScreenshot('ws-search-fail');
      throw e;
    }
  }

  async startTail() {
    await this.ensureNoOverlay();
    try {
      await this.switchTab('tail');
      const btnSelectors = [
        'button:has-text("开始跟踪")',
        'button:has-text("启动")',
        'button:has-text("开始 tail")',
        '#ws-tail-start',
        '.ws-tail-start',
      ];
      const clicked = await this.clickFirstVisible(btnSelectors);
      if (!clicked) {
        await this.takeStateScreenshot('ws-tail-start-btn-error');
        const summary = await this.getDomSummary();
        throw new Error('开始跟踪按钮未找到: ' + JSON.stringify(summary.buttons.slice(0, 15)));
      }
      return true;
    } catch (e) {
      await this.takeStateScreenshot('ws-tail-start-fail');
      throw e;
    }
  }

  async stopTail() {
    await this.ensureNoOverlay();
    try {
      const btnSelectors = [
        'button:has-text("停止")',
        'button:has-text("停止跟踪")',
        '#ws-tail-stop',
        '.ws-tail-stop',
      ];
      let stopped = false;
      for (const sel of btnSelectors) {
        const btns = await this.page.$$(sel);
        for (const btn of btns) {
          try {
            const visible = await btn.isVisible();
            const disabled = await btn.evaluate((b) => b.disabled);
            if (visible && !disabled) {
              await btn.click();
              stopped = true;
              break;
            }
          } catch (e) {
            // continue
          }
        }
        if (stopped) break;
      }
      if (!stopped) {
        const stopBtn = await this.page.evaluateHandle(() => {
          const out = document.getElementById('ws-tail-out');
          if (!out) return null;
          const card = out.closest('.card') || out.closest('[class*="card"]');
          if (!card) return null;
          return Array.from(card.querySelectorAll('button')).find((b) => {
            return b.textContent.trim() === '停止' && !b.disabled;
          });
        });
        const el = stopBtn.asElement ? await stopBtn.asElement() : null;
        if (el) {
          await el.click();
          stopped = true;
        }
      }
      await this.waitForTimeout(300);
      return stopped;
    } catch (e) {
      await this.takeStateScreenshot('ws-tail-stop-fail');
      throw e;
    }
  }

  async hasTailOutput() {
    try {
      const outputSelectors = [
        '#ws-tail-out',
        '.ws-tail-output',
        '.tail-output',
        '[class*="tail-out"]',
      ];
      for (const sel of outputSelectors) {
        const el = await this.page.$(sel);
        if (el) {
          const text = await el.textContent();
          if (text && text.trim().length > 0) {
            return true;
          }
        }
      }
      const hasContent = await this.page.evaluate(() => {
        const out = document.getElementById('ws-tail-out');
        if (out && out.textContent && out.textContent.trim().length > 0) {
          return true;
        }
        const tailEls = document.querySelectorAll('[class*="tail"], [id*="tail"]');
        for (const el of tailEls) {
          if (el.textContent && el.textContent.trim().length > 50) {
            return true;
          }
        }
        return false;
      });
      return hasContent;
    } catch (e) {
      return false;
    }
  }

  async takeStateScreenshot(name) {
    const ts = Date.now();
    return await this.screenshot(`${name}-${ts}`);
  }

  async downloadLatest() {
    await this.ensureNoOverlay();
    try {
      const btnSelectors = [
        'button:has-text("下载最新")',
        'button:has-text("下载"):not(:has-text("下载选中"))',
        '#ws-dl-btn',
        '.ws-dl-btn',
      ];
      return await this.clickFirstVisible(btnSelectors);
    } catch (e) {
      await this.takeStateScreenshot('ws-download-fail');
      throw e;
    }
  }

  async selectAllServers() {
    await this.ensureNoOverlay();
    try {
      const btnSelectors = [
        'button:has-text("全选")',
        '.select-all-btn',
        '#ws-select-all',
      ];
      return await this.clickFirstVisible(btnSelectors);
    } catch (e) {
      return false;
    }
  }

  async deselectAllServers() {
    await this.ensureNoOverlay();
    try {
      const btnSelectors = [
        'button:has-text("全不选")',
        'button:has-text("取消全选")',
        '.deselect-all-btn',
        '#ws-deselect-all',
      ];
      return await this.clickFirstVisible(btnSelectors);
    } catch (e) {
      return false;
    }
  }

  async hasSystemSelect() {
    const sel = await this.findFirstVisible(['select#ws-sys', '#ws-sys']);
    return !!sel;
  }

  async hasServerCheckboxes() {
    const checkboxSelectors = [
      'input[type="checkbox"][data-srv]',
      '#ws-server-list input[type="checkbox"]',
      '.server-list input[type="checkbox"]',
    ];
    for (const sel of checkboxSelectors) {
      const boxes = await this.page.$$(sel);
      if (boxes.length > 0) return true;
    }
    return false;
  }
}

module.exports = WebSpherePage;
