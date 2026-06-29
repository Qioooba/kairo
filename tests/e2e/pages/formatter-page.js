'use strict';

const path = require('path');

class FormatterPage {
  constructor(page, baseUrl, runner) {
    this.page = page;
    this.baseUrl = baseUrl;
    this.runner = runner;
    this.screenshotsDir = runner ? runner.screenshotsDir : null;
  }

  async goto(options = {}) {
    const targetHash = '#/formatter';
    const currentHash = await this.page.evaluate(() => location.hash);
    if (currentHash === targetHash && !options.forceReload) {
      await this.ensureNoOverlay();
      await this.clearAll();
      await this.page.waitForTimeout(100);
      return;
    }
    const url = this.baseUrl + targetHash;
    await this.page.goto(url, { waitUntil: 'domcontentloaded' });
    await this.page.waitForFunction(() => location.hash === '#/formatter', { timeout: 10000 });
    try {
      await this.page.waitForLoadState('networkidle', { timeout: 10000 });
    } catch (e) {
    }
    await this.page.waitForSelector('#fmt-in', { timeout: 10000 });
    await this.ensureNoOverlay();
    await this.page.waitForTimeout(300);
  }

  async clearAll() {
    try {
      await this.page.evaluate(() => {
        const input = document.getElementById('fmt-in');
        const output = document.getElementById('fmt-out');
        if (input) input.value = '';
        if (output) output.value = '';
        const toast = document.getElementById('toast');
        if (toast) {
          toast.className = 'toast';
          toast.textContent = '';
        }
      });
    } catch (e) {}
  }

  async ensureNoOverlay() {
    try {
      const overlaySelectors = [
        '.otb-dialog-overlay',
        '.otb-modal-overlay',
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

  async setInput(text) {
    await this.ensureNoOverlay();
    await this.page.waitForSelector('#fmt-in', { timeout: 5000 });
    await this.page.fill('#fmt-in', text);
  }

  async getOutput() {
    return await this.page.$eval('#fmt-out', (el) => el.value || '');
  }

  async clearInput() {
    await this.ensureNoOverlay();
    await this.page.fill('#fmt-in', '');
  }

  async clickButton(buttonTexts) {
    await this.ensureNoOverlay();
    const texts = Array.isArray(buttonTexts) ? buttonTexts : [buttonTexts];
    const selectors = texts.map((t) => `button:has-text("${t}")`);
    const btn = await this.findFirstVisible(selectors);
    if (!btn) {
      throw new Error('按钮未找到: ' + texts.join(', '));
    }
    await btn.click();
    await this.page.waitForTimeout(300);
  }

  async formatJson() {
    await this.clickButton('格式化 JSON');
    await this.page.waitForFunction(() => {
      const out = document.getElementById('fmt-out');
      return out && out.value && out.value.length > 0;
    }, { timeout: 5000 });
  }

  async compressJson() {
    await this.clickButton('压缩 JSON');
    await this.page.waitForFunction(() => {
      const out = document.getElementById('fmt-out');
      return out && out.value && out.value.length > 0;
    }, { timeout: 5000 });
  }

  async validateJson() {
    await this.clickButton('校验 JSON');
    await this.page.waitForTimeout(500);
  }

  async formatXml() {
    await this.clickButton('格式化 XML');
    await this.page.waitForFunction(() => {
      const out = document.getElementById('fmt-out');
      return out && out.value && out.value.length > 0;
    }, { timeout: 5000 });
  }

  async formatYaml() {
    await this.clickButton('格式化 YAML');
    await this.page.waitForFunction(() => {
      const out = document.getElementById('fmt-out');
      return out && out.value && out.value.length > 0;
    }, { timeout: 5000 });
  }

  async urlEncode() {
    await this.clickButton(['encode（map → a=1&b=2）', 'encode', 'URL 编码']);
    try {
      await this.page.waitForFunction(() => {
        const out = document.getElementById('fmt-out');
        const toast = document.getElementById('toast');
        const hasOutput = out && out.value && out.value.length > 0;
        const hasToast = toast && toast.textContent && toast.textContent.length > 0 && (toast.className || '').indexOf('show') >= 0;
        return hasOutput || hasToast;
      }, { timeout: 5000 });
    } catch (e) {
    }
  }

  async urlDecode() {
    await this.clickButton(['decode（a=1&b=2 → map）', 'decode', 'URL 解码']);
    try {
      await this.page.waitForFunction(() => {
        const out = document.getElementById('fmt-out');
        return out && out.value && out.value.length > 0;
      }, { timeout: 5000 });
    } catch (e) {
    }
  }

  async hasError() {
    const errorIndicators = await this.page.evaluate(() => {
      const results = [];

      const toast = document.getElementById('toast');
      if (toast) {
        const toastText = toast.textContent || '';
        const toastClass = toast.className || '';
        const hasErrorText = (
          toastText.indexOf('错误') >= 0 ||
          toastText.indexOf('非法') >= 0 ||
          toastText.indexOf('无效') >= 0 ||
          toastText.indexOf('失败') >= 0 ||
          toastText.indexOf('Error') >= 0 ||
          toastText.indexOf('error') >= 0 ||
          toastText.indexOf('invalid') >= 0 ||
          toastText.indexOf('Invalid') >= 0
        );
        const isErrorClass = (
          toastClass.indexOf('err') >= 0 ||
          toastClass.indexOf('error') >= 0 ||
          toastClass.indexOf('danger') >= 0
        );
        const isVisible = toastClass.indexOf('show') >= 0;
        if (hasErrorText && isVisible) results.push(true);
        if (isErrorClass && isVisible) results.push(true);
      }

      const out = document.getElementById('fmt-out');
      if (out && out.value) {
        const outText = out.value;
        results.push(
          outText.indexOf('错误') >= 0 ||
          outText.indexOf('Error') >= 0 ||
          outText.indexOf('error') >= 0 ||
          outText.indexOf('invalid') >= 0 ||
          outText.indexOf('Invalid') >= 0 ||
          outText.indexOf('失败') >= 0
        );
      }

      const errorSelectors = [
        '.text-danger', '.error', '.alert-danger',
        '[class*="error"]', '[class*="err"]', '[role="alert"]'
      ];
      for (const sel of errorSelectors) {
        const els = document.querySelectorAll(sel);
        if (els.length > 0) {
          for (const el of els) {
            const style = getComputedStyle(el);
            const isVisible = style.display !== 'none' && style.visibility !== 'hidden';
            const hasText = el.textContent && el.textContent.trim().length > 0;
            if (isVisible && hasText) {
              results.push(true);
              break;
            }
          }
        }
      }

      return results;
    });

    return errorIndicators.some(Boolean);
  }

  async takeStateScreenshot(name) {
    if (!this.runner || !this.screenshotsDir) return null;
    const safeName = String(name).replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]/g, '_');
    return await this.runner.screenshot(this.page, 'formatter-' + safeName);
  }
}

module.exports = FormatterPage;
