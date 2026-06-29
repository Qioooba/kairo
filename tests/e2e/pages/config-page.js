'use strict';

class ConfigPage {
  constructor(page, baseUrl, runner) {
    this.page = page;
    this.baseUrl = baseUrl;
    this.runner = runner;
    this.screenshotsDir = runner ? runner.screenshotsDir : null;
  }

  async goto() {
    const targetHash = '#/config';
    const currentHash = await this.page.evaluate(() => location.hash);
    if (currentHash === targetHash) {
      await this.ensureNoOverlay();
      await this.page.waitForTimeout(100);
      return;
    }
    const url = this.baseUrl + targetHash;
    await this.page.goto(url, { waitUntil: 'domcontentloaded' });
    await this.page.waitForFunction(() => location.hash === '#/config', { timeout: 10000 });
    try {
      await this.page.waitForLoadState('networkidle', { timeout: 10000 });
    } catch (e) {
    }
    await this.ensureNoOverlay();
    await this.page.waitForTimeout(500);
  }

  async ensureNoOverlay() {
    try {
      const overlaySelectors = [
        '.dtb-dialog-overlay',
        '.dtb-modal-overlay',
        '.modal-overlay',
        '[class*="dialog-overlay"]',
        '[class*="modal-overlay"]',
      ];
      for (const sel of overlaySelectors) {
        const overlay = await this.page.$(sel);
        if (overlay) {
          const isVisible = await overlay.isVisible().catch(() => false);
          if (isVisible) {
            try { await this.page.keyboard.press('Escape'); } catch (e) {}
            await this.page.waitForTimeout(300);
          }
        }
      }
    } catch (e) {
    }
  }

  async findFirstVisible(selectors) {
    const list = Array.isArray(selectors) ? selectors : [selectors];
    for (const sel of list) {
      try {
        const el = await this.page.$(sel);
        if (el) {
          const visible = await el.isVisible().catch(() => false);
          if (visible) {
            return el;
          }
        }
      } catch (e) {
      }
    }
    return null;
  }

  async getSystemCount() {
    const sysBlocks = await this.page.$$('.sys-block');
    return sysBlocks.length;
  }

  async addSystem(name) {
    await this.ensureNoOverlay();
    const beforeCount = await this.getSystemCount();
    const addBtn = await this.findFirstVisible([
      'button:has-text("+ 新增业务系统")',
      'button:has-text("新增业务系统")',
      'button:has-text("+新增业务系统")',
    ]);
    if (!addBtn) throw new Error('新增业务系统按钮未找到');
    await addBtn.click();
    await this.page.waitForTimeout(500);

    const afterCount = await this.getSystemCount();
    const added = afterCount > beforeCount;

    if (name && added) {
      const sysSelector = `.sys-block:nth-of-type(${afterCount})`;
      try {
        await this.page.fill(`${sysSelector} .sys-name-input input`, name);
        await this.page.waitForTimeout(200);
      } catch (e) {}
    }

    const saveBtnEnabled = !await this.isSaveButtonDisabled();

    return { beforeCount, afterCount, added, saveBtnEnabled };
  }

  async removeLastSystem() {
    await this.ensureNoOverlay();
    const sysBlocks = await this.page.$$('.sys-block');
    if (sysBlocks.length === 0) return { confirmed: false, dialogShown: false };

    const lastSys = sysBlocks[sysBlocks.length - 1];
    const removeBtn = await lastSys.$(
      'button:has-text("删除系统"), button:has-text("删除"), button:has-text("移除"), .remove-sys-btn, [class*="delete"]'
    );

    if (!removeBtn) {
      return { confirmed: false, dialogShown: false };
    }

    await removeBtn.click();
    await this.page.waitForTimeout(500);

    const dialogShown = await this.isDialogVisible();

    if (dialogShown) {
      await this.clickDialogCancel();
    }

    return { confirmed: false, dialogShown };
  }

  async getServerCount(systemIndex) {
    const sysBlocks = await this.page.$$('.sys-block');
    if (systemIndex >= sysBlocks.length) return 0;
    const sysBlock = sysBlocks[systemIndex];
    const srvBlocks = await sysBlock.$$('.srv-block');
    return srvBlocks.length;
  }

  async addServer(systemIndex, serverData) {
    await this.ensureNoOverlay();
    const sysBlocks = await this.page.$$('.sys-block');
    if (systemIndex >= sysBlocks.length) {
      throw new Error('系统索引超出范围: ' + systemIndex);
    }

    const beforeCount = await this.getServerCount(systemIndex);

    const sysSelector = `.sys-block:nth-of-type(${systemIndex + 1})`;
    const addBtnSelector = `${sysSelector} button:has-text("+ 新增服务器")`;

    const addBtn = await this.findFirstVisible([addBtnSelector]);
    if (!addBtn) throw new Error('添加服务器按钮未找到');

    await addBtn.click();
    await this.page.waitForTimeout(500);

    const afterAddCount = await this.getServerCount(systemIndex);
    const added = afterAddCount > beforeCount;

    if (serverData && added) {
      const srvIndex = afterAddCount - 1;
      const srvSelector = `${sysSelector} .srv-block:nth-of-type(${srvIndex + 1})`;

      if (serverData.name) {
        try {
          await this.page.fill(`${srvSelector} input[placeholder*="服务器名"]`, serverData.name);
        } catch (e) {}
      }
      if (serverData.host) {
        try {
          await this.page.fill(`${srvSelector} input[placeholder*="IP / 主机"]`, serverData.host);
        } catch (e) {}
      }
      if (serverData.port) {
        try {
          await this.page.fill(`${srvSelector} input[placeholder*="SSH 端口"]`, String(serverData.port));
        } catch (e) {}
      }
      if (serverData.username) {
        try {
          await this.page.fill(`${srvSelector} input[placeholder*="SSH 用户名"]`, serverData.username);
        } catch (e) {}
      }
      await this.page.waitForTimeout(200);
    }

    const saveBtnEnabled = !await this.isSaveButtonDisabled();

    return { beforeCount, afterCount: afterAddCount, added, saveBtnEnabled };
  }

  async getDirCount(systemIndex) {
    const sysBlocks = await this.page.$$('.sys-block');
    if (systemIndex >= sysBlocks.length) return 0;
    const sysBlock = sysBlocks[systemIndex];
    const srvBlocks = await sysBlock.$$('.srv-block');
    if (srvBlocks.length === 0) return 0;

    let total = 0;
    for (const srv of srvBlocks) {
      const dirBlocks = await srv.$$('.dir-block');
      total += dirBlocks.length;
    }
    return total;
  }

  async addLogDir(systemIndex, serverIndex, dirData) {
    await this.ensureNoOverlay();
    const sysBlocks = await this.page.$$('.sys-block');
    if (systemIndex >= sysBlocks.length) {
      throw new Error('系统索引超出范围: ' + systemIndex);
    }

    const srvCount = await this.getServerCount(systemIndex);
    if (srvCount === 0) {
      throw new Error('该系统下没有服务器，无法添加日志目录');
    }

    const srvIdx = typeof serverIndex === 'number' ? serverIndex : 0;
    if (srvIdx >= srvCount) {
      throw new Error('服务器索引超出范围: ' + srvIdx);
    }

    const sysSelector = `.sys-block:nth-of-type(${systemIndex + 1})`;
    const srvSelector = `${sysSelector} .srv-block:nth-of-type(${srvIdx + 1})`;

    const beforeCount = await this.page.$$eval(`${srvSelector} .dir-block`, (els) => els.length);

    const addBtnSelector = `${srvSelector} button:has-text("+ 新增日志目录")`;
    const addBtn = await this.findFirstVisible([addBtnSelector]);
    if (!addBtn) throw new Error('添加日志目录按钮未找到');

    await addBtn.click();
    await this.page.waitForTimeout(500);

    const afterAddCount = await this.page.$$eval(`${srvSelector} .dir-block`, (els) => els.length);
    const added = afterAddCount > beforeCount;

    if (dirData && added) {
      const dirIndex = afterAddCount - 1;
      const dirSelector = `${srvSelector} .dir-block:nth-of-type(${dirIndex + 1})`;

      if (typeof dirData === 'string') {
        dirData = { path: dirData };
      }

      if (dirData.name) {
        try {
          await this.page.fill(`${dirSelector} input[placeholder*="目录别名"]`, dirData.name);
        } catch (e) {}
      }
      if (dirData.path) {
        try {
          await this.page.fill(`${dirSelector} input[placeholder*="远端绝对路径"]`, dirData.path);
        } catch (e) {}
      }
      await this.page.waitForTimeout(200);
    }

    const saveBtnEnabled = !await this.isSaveButtonDisabled();

    return { beforeCount, afterCount: afterAddCount, added, saveBtnEnabled };
  }

  async exportConfig() {
    await this.ensureNoOverlay();
    let downloadTriggered = false;
    const handler = (req) => {
      const url = req.url();
      if (url.indexOf('/api/config') >= 0 || url.indexOf('/export') >= 0 || url.indexOf('/download') >= 0) {
        downloadTriggered = true;
      }
    };

    this.page.on('request', handler);

    const exportBtn = await this.findFirstVisible([
      'button:has-text("导出配置")',
      'button:has-text("导出")',
    ]);
    if (!exportBtn) throw new Error('导出配置按钮未找到');
    await exportBtn.click();
    await this.page.waitForTimeout(1000);

    this.page.off('request', handler);
    return { triggered: downloadTriggered };
  }

  async isDialogVisible() {
    try {
      const overlay = await this.page.$('.dtb-dialog-overlay');
      if (!overlay) return false;
      const visible = await overlay.isVisible().catch(() => false);
      return visible;
    } catch (e) {
      return false;
    }
  }

  async clickDialogCancel() {
    try {
      const cancelBtn = await this.page.$('.dtb-dialog-actions button:has-text("取消")');
      if (cancelBtn) {
        await cancelBtn.click();
        await this.page.waitForTimeout(300);
        return true;
      }
    } catch (e) {}
    return false;
  }

  async clickReset() {
    await this.ensureNoOverlay();

    const resetBtn = await this.findFirstVisible([
      'button:has-text("放弃改动")',
      'button:has-text("取消修改")',
      'button:has-text("重置")',
    ]);
    if (!resetBtn) throw new Error('放弃改动按钮未找到');
    await resetBtn.click();
    await this.page.waitForTimeout(500);

    const dialogShown = await this.isDialogVisible();

    if (dialogShown) {
      await this.clickDialogCancel();
    }

    return { dialogShown, confirmed: false };
  }

  async hasSaveButton() {
    const saveBtn = await this.findFirstVisible([
      '#cfg-save-btn',
      'button:has-text("💾 保存")',
      'button:has-text("保存")',
    ]);
    return !!saveBtn;
  }

  async isSaveButtonDisabled() {
    const saveBtn = await this.findFirstVisible([
      '#cfg-save-btn',
      'button:has-text("💾 保存")',
      'button:has-text("保存")',
    ]);
    if (!saveBtn) return true;
    const disabled = await saveBtn.isDisabled().catch(() => true);
    return disabled;
  }

  async takeStateScreenshot(name) {
    if (!this.runner || !this.screenshotsDir) return null;
    const safeName = String(name).replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]/g, '_');
    return await this.runner.screenshot(this.page, 'config-' + safeName);
  }
}

module.exports = ConfigPage;
