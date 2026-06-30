'use strict';

const path = require('path');
const fs = require('fs');

class BasePage {
  constructor(page, baseUrl) {
    this.page = page;
    this.baseUrl = baseUrl || 'http://127.0.0.1:18090';
    this._screenshotDir = null;
  }

  setScreenshotDir(dir) {
    this._screenshotDir = dir;
  }

  async goto(hash, options = {}) {
    const cleanHash = hash.replace(/^#?\/?/, '/');
    const targetHash = '#' + cleanHash;

    const currentHash = await this.page.evaluate(() => location.hash);
    if (currentHash === targetHash && !options.forceReload) {
      await this.ensureNoOverlay();
      await this.page.waitForTimeout(100);
      return;
    }

    const url = this.baseUrl + targetHash;
    await this.page.goto(url, { waitUntil: 'domcontentloaded' });
    await this.page.waitForFunction(
      (h) => location.hash === h,
      targetHash,
      { timeout: 10000 }
    );
    await this.waitForReady();
  }

  async waitForReady() {
    try {
      await this.page.waitForLoadState('networkidle', { timeout: 10000 });
    } catch (e) {
      // ignore networkidle timeout
    }
    await this.page.waitForTimeout(300);
  }

  async screenshot(name) {
    if (!this._screenshotDir) return null;
    if (!fs.existsSync(this._screenshotDir)) {
      fs.mkdirSync(this._screenshotDir, { recursive: true });
    }
    const safeName = name.replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]/g, '_');
    const p = path.join(this._screenshotDir, safeName + '.png');
    await this.page.screenshot({ path: p, fullPage: true });
    return p;
  }

  async closeAllDialogs() {
    try {
      const overlaySelectors = [
        '.kairo-dialog-overlay',
        '.kairo-modal-overlay',
        '.modal-overlay',
        '[class*="dialog-overlay"]',
        '[class*="modal-overlay"]',
      ];

      for (const sel of overlaySelectors) {
        const overlay = await this.page.$(sel);
        if (overlay) {
          const isVisible = await overlay.isVisible().catch(() => false);
          if (isVisible) {
            const closeSelectors = [
              '.kairo-dialog .close',
              '.kairo-modal .close',
              '.dialog-close',
              '.modal-close',
              'button:has-text("关闭")',
              'button:has-text("取消")',
              '.kairo-dialog-footer button:last-child',
              '.modal-footer button:last-child',
            ];
            for (const closeSel of closeSelectors) {
              const btn = await this.page.$(closeSel);
              if (btn) {
                try { await btn.click({ timeout: 2000 }); } catch (e) {}
                break;
              }
            }
            try { await this.page.keyboard.press('Escape'); } catch (e) {}
            await this.page.waitForTimeout(300);
          }
        }
      }
    } catch (e) {
      // ignore cleanup errors
    }
  }

  async ensureNoOverlay() {
    try {
      await this.closeAllDialogs();
      const overlaySelectors = [
        '.kairo-dialog-overlay',
        '.kairo-modal-overlay',
        '.modal-overlay',
        '[class*="dialog-overlay"]',
        '[class*="modal-overlay"]',
      ];
      for (const sel of overlaySelectors) {
        const overlay = await this.page.$(sel);
        if (overlay) {
          const isVisible = await overlay.isVisible().catch(() => false);
          if (isVisible) {
            return false;
          }
        }
      }
      return true;
    } catch (e) {
      return false;
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
        // continue
      }
    }
    return null;
  }

  async clickFirstVisible(selectors) {
    await this.ensureNoOverlay();
    const el = await this.findFirstVisible(selectors);
    if (el) {
      await el.click();
      return true;
    }
    return false;
  }

  async fillFirstVisible(selectors, value) {
    await this.ensureNoOverlay();
    const el = await this.findFirstVisible(selectors);
    if (el) {
      await el.fill(value);
      return true;
    }
    return false;
  }

  async getDomSummary() {
    try {
      return await this.page.evaluate(() => {
        const result = {
          title: document.title,
          url: location.href,
          hash: location.hash,
          forms: [],
          buttons: [],
          inputs: [],
          tables: [],
          tabs: [],
          dialogs: [],
        };

        const formEls = document.querySelectorAll('form');
        formEls.forEach((f, i) => {
          result.forms.push({
            index: i,
            id: f.id || '',
            className: f.className || '',
            inputCount: f.querySelectorAll('input, select, textarea').length,
          });
        });

        const buttonEls = document.querySelectorAll('button, input[type="button"], input[type="submit"], [role="button"]');
        buttonEls.forEach((b, i) => {
          const text = (b.textContent || b.value || '').trim().substring(0, 50);
          if (text) {
            result.buttons.push({
              index: i,
              text,
              id: b.id || '',
              tag: b.tagName.toLowerCase(),
            });
          }
        });

        const inputEls = document.querySelectorAll('input, select, textarea');
        inputEls.forEach((inp, i) => {
          const type = inp.type || inp.tagName.toLowerCase();
          result.inputs.push({
            index: i,
            id: inp.id || '',
            type,
            name: inp.name || '',
            placeholder: inp.placeholder || '',
          });
        });

        const tableEls = document.querySelectorAll('table');
        tableEls.forEach((t, i) => {
          const rows = t.querySelectorAll('tbody tr').length;
          result.tables.push({
            index: i,
            id: t.id || '',
            className: t.className || '',
            rows,
          });
        });

        const tabSelectors = ['.ws-tab-item', '.nav-tabs a', '.tab-item', '[role="tab"]', '.tabs [data-tab]'];
        for (const sel of tabSelectors) {
          const tabs = document.querySelectorAll(sel);
          if (tabs.length > 0) {
            tabs.forEach((t, i) => {
              const text = (t.textContent || '').trim().substring(0, 30);
              if (text) {
                result.tabs.push({
                  index: i,
                  text,
                  selector: sel,
                });
              }
            });
            break;
          }
        }

        const dialogSelectors = ['.kairo-dialog', '.kairo-modal', '.modal', '.dialog'];
        for (const sel of dialogSelectors) {
          const dialogs = document.querySelectorAll(sel);
          dialogs.forEach((d, i) => {
            result.dialogs.push({
              index: i,
              selector: sel,
              id: d.id || '',
              visible: d.offsetParent !== null,
            });
          });
        }

        return result;
      });
    } catch (e) {
      return { error: e.message, url: this.page.url() };
    }
  }

  async waitForSelector(selector, options = {}) {
    return await this.page.waitForSelector(selector, options);
  }

  async waitForTimeout(ms) {
    await this.page.waitForTimeout(ms);
  }

  async $(selector) {
    return await this.page.$(selector);
  }

  async $$(selector) {
    return await this.page.$$(selector);
  }
}

module.exports = BasePage;
