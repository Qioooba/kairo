'use strict';

class FilesPage {
  constructor(page, baseUrl, runner) {
    this.page = page;
    this.baseUrl = baseUrl;
    this.runner = runner;
  }

  async goto() {
    const targetHash = '#/files';
    const currentHash = await this.page.evaluate(() => location.hash);
    if (currentHash === targetHash) {
      try { await this.ensureNoOverlay(); } catch (e) {}
      await this.page.waitForTimeout(100);
      return;
    }
    await this.page.goto(this.baseUrl + targetHash, { waitUntil: 'load' });
    await this.page.waitForSelector('#files-sys', { timeout: 10000 });
  }

  async ensureNoOverlay() {
    try {
      const overlaySelectors = [
        '.kairo-dialog-overlay',
        '.kairo-modal-overlay',
        '.modal-overlay',
        '[class*="dialog-overlay"]',
      ];
      for (const sel of overlaySelectors) {
        const overlay = await this.page.$(sel);
        if (overlay) {
          const isVisible = await overlay.isVisible().catch(() => false);
          if (isVisible) {
            try { await this.page.keyboard.press('Escape'); } catch (e) {}
            await this.page.waitForTimeout(200);
          }
        }
      }
    } catch (e) {}
  }

  async waitForTimeout(ms) {
    await this.page.waitForTimeout(ms);
  }

  async selectFirstSystem() {
    const options = await this.page.$$eval('#files-sys option', function (els) {
      return els.map(function (e) { return e.textContent; });
    });
    if (options.length === 0) {
      throw new Error('系统选项为空');
    }
    await this.page.selectOption('#files-sys', options[0]);
    await this.page.waitForTimeout(200);
    return options[0];
  }

  async hasSystems() {
    try {
      const options = await this.page.$$eval('#files-sys option', function (els) {
        return els.filter(function (e) { return e.value || e.textContent; }).length;
      });
      return options > 0;
    } catch (e) {
      return false;
    }
  }

  async isServerEnabled() {
    try {
      const srv = await this.page.$('#files-srv');
      if (!srv) return false;
      const disabled = await srv.evaluate(function (el) { return el.disabled; });
      return !disabled;
    } catch (e) {
      return false;
    }
  }

  async hasServers() {
    try {
      const options = await this.page.$$eval('#files-srv option', function (els) {
        return els.filter(function (e) {
          if (!e.value) return false;
          var text = (e.textContent || '').trim();
          if (!text || text.indexOf('请选择') >= 0) return false;
          return true;
        }).length;
      });
      return options > 0;
    } catch (e) {
      return false;
    }
  }

  async waitForServerEnabled() {
    try {
      await this.page.waitForFunction(function () {
        const srv = document.getElementById('files-srv');
        return srv && !srv.disabled;
      }, null, { timeout: 5000 });
      return true;
    } catch (e) {
      return false;
    }
  }

  async selectFirstServer() {
    const options = await this.page.$$eval('#files-srv option', function (els) {
      return els.map(function (e) { return e.textContent; });
    });
    if (options.length === 0) {
      throw new Error('服务器选项为空');
    }
    await this.page.selectOption('#files-srv', options[0]);
    await this.page.waitForTimeout(200);
    return options[0];
  }

  async fillCredential(username, password) {
    await this.page.fill('#files-user-input', username);
    await this.page.fill('#files-pass-input', password);
  }

  async connectAndBrowse() {
    const connectBtn = await this.page.$('button.btn-primary:has-text("连接并浏览")');
    if (!connectBtn) {
      throw new Error('连接并浏览按钮未找到');
    }
    await connectBtn.click();
    await this.page.waitForSelector('table.table tbody tr, .file-list .file-item', { timeout: 15000 });
  }

  async isConnected() {
    const tableRows = await this.page.$$('table.table tbody tr');
    if (tableRows.length > 0) {
      const firstRow = tableRows[0];
      const cells = await firstRow.$$('td');
      if (cells.length >= 2) {
        return true;
      }
    }
    const fileItems = await this.page.$$('.file-list .file-item');
    if (fileItems.length > 0) return true;
    return false;
  }

  async getFileCount() {
    const tableRows = await this.page.$$('table.table tbody tr');
    if (tableRows.length > 0) {
      return tableRows.length;
    }
    const fileItems = await this.page.$$('.file-list .file-item');
    return fileItems.length;
  }

  async getCurrentPath() {
    const pathInput = await this.page.$('input[placeholder*="输入绝对路径后回车跳转"]');
    if (pathInput) {
      return await pathInput.inputValue();
    }
    const crumbs = await this.page.$$('.file-crumbs a, .breadcrumb a');
    if (crumbs.length > 0) {
      const lastCrumb = crumbs[crumbs.length - 1];
      return await lastCrumb.textContent();
    }
    return '';
  }

  async navigateToParent() {
    const parentBtn = await this.page.$('button.btn-sm:has-text("← 上级")');
    if (!parentBtn) {
      throw new Error('上级目录按钮未找到');
    }
    await parentBtn.click();
    await this.page.waitForTimeout(500);
  }

  async navigateToPath(targetPath) {
    const pathInput = await this.page.$('input[placeholder*="输入绝对路径后回车跳转"]');
    if (!pathInput) {
      throw new Error('路径输入框未找到');
    }
    await pathInput.fill(targetPath);
    await pathInput.press('Enter');
    await this.page.waitForTimeout(500);
  }

  async filterFiles(keyword) {
    const filterInput = await this.page.$('#files-filter');
    if (!filterInput) {
      throw new Error('文件过滤输入框未找到');
    }
    await filterInput.fill(keyword);
    await this.page.waitForTimeout(300);
  }

  async selectFirstFile() {
    const checkbox = await this.page.$(
      'table.table tbody tr input[type="checkbox"]:not([disabled]), .file-list .file-item input[type="checkbox"]:not([disabled])'
    );
    if (!checkbox) {
      throw new Error('文件复选框未找到');
    }
    await checkbox.check();
    await this.page.waitForTimeout(100);
  }

  async selectAllFiles() {
    const selectAllBtn = await this.page.$('button:has-text("全选")');
    if (!selectAllBtn) {
      throw new Error('全选按钮未找到');
    }
    await selectAllBtn.click();
    await this.page.waitForTimeout(100);
  }

  async deselectAllFiles() {
    const cancelBtn = await this.page.$('button:has-text("取消选中")');
    if (!cancelBtn) {
      throw new Error('取消选中按钮未找到');
    }
    await cancelBtn.click();
    await this.page.waitForTimeout(100);
  }

  async getSelectedCount() {
    const checked = await this.page.$$eval(
      'table.table tbody tr input[type="checkbox"]:checked, .file-list .file-item input[type="checkbox"]:checked',
      function (els) { return els.length; }
    );
    return checked;
  }

  async testPathTraversal(traversalPath) {
    const result = {
      inputValue: '',
      apiRejected: false,
      apiErrorStatus: null,
      hasErrorMessage: false,
      errorMessage: '',
    };

    let apiResponseStatus = null;

    var self = this;

    function responseHandler(res) {
      var url = res.url();
      if (url.indexOf('/api/files/list') >= 0 || url.indexOf('/api/files') >= 0) {
        apiResponseStatus = res.status();
        if (apiResponseStatus >= 400) {
          result.apiRejected = true;
          result.apiErrorStatus = apiResponseStatus;
        }
      }
    }

    self.page.on('response', responseHandler);

    try {
      const pathInput = await self.page.$('input[placeholder*="输入绝对路径后回车跳转"]');
      if (pathInput) {
        await pathInput.fill(traversalPath);
        await pathInput.press('Enter');
        await self.page.waitForTimeout(1000);
        result.inputValue = await pathInput.inputValue();
      }

      const toastSelectors = ['#toast', '.toast', '.notification', '[role="alert"]'];
      for (var i = 0; i < toastSelectors.length; i++) {
        const toast = await self.page.$(toastSelectors[i]);
        if (toast) {
          const visible = await toast.isVisible().catch(function () { return false; });
          if (visible) {
            const text = await toast.textContent();
            if (text && (text.indexOf('错误') >= 0 || text.indexOf('非法') >= 0 || text.indexOf('安全') >= 0 || text.indexOf('拒绝') >= 0 || text.indexOf('失败') >= 0)) {
              result.hasErrorMessage = true;
              result.errorMessage = text.trim();
              break;
            }
          }
        }
      }
    } finally {
      self.page.off('response', responseHandler);
    }

    return result;
  }

  async takeStateScreenshot(name) {
    if (this.runner && this.runner.screenshot) {
      return await this.runner.screenshot(this.page, name);
    }
    return null;
  }
}

module.exports = FilesPage;
