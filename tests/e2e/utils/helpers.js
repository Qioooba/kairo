'use strict';

const fs = require('fs');
const path = require('path');
const { DANGEROUS_BUTTON_PATTERNS } = require('./page-data');

function ensureDir(dir) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

function sanitizeFileName(name) {
  return name
    .replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]/g, '_')
    .replace(/_+/g, '_')
    .replace(/^_|_$/g, '');
}

function timestampForFile() {
  const now = new Date();
  const pad = (n) => String(n).padStart(2, '0');
  return (
    now.getFullYear() +
    pad(now.getMonth() + 1) +
    pad(now.getDate()) +
    '-' +
    pad(now.getHours()) +
    pad(now.getMinutes()) +
    pad(now.getSeconds()) +
    '-' +
    String(now.getMilliseconds()).padStart(3, '0')
  );
}

async function takeScreenshot(page, name, dir) {
  const safeName = sanitizeFileName(name || 'screenshot');
  const dirPath = dir || path.join(process.cwd(), 'test-results', 'screenshots');
  ensureDir(dirPath);

  const fileName = `${safeName}-${timestampForFile()}.png`;
  const filePath = path.join(dirPath, fileName);

  await page.screenshot({ path: filePath, fullPage: true });
  return filePath;
}

/**
 * requireElement 必须存在的元素 —— 选择器没匹配到就抛错。
 *
 * QA-02: 旧用例大量使用 `const el = await page.$(sel); if (!el) return;`,
 * 页面结构一旦变化, 用例会"零断言通过", 报告里看不出任何异常。
 * 新增/改造用例一律用本函数: 拿不到元素就是失败, 不允许伪成功。
 */
async function requireElement(page, selector, description) {
  const el = await page.$(selector);
  if (!el) {
    throw new Error(
      `选择器未匹配到元素, 用例不得视为通过: ${description ? description + ' ' : ''}selector=${selector}`
    );
  }
  return el;
}

async function setTheme(page, theme) {
  await page.evaluate((t) => {
    document.documentElement.setAttribute('data-theme', t);
  }, theme);
}

async function waitForPageReady(page) {
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(500);
}

async function gotoHash(page, hash, baseUrl) {
  const url = (baseUrl || 'http://127.0.0.1:18090') + '/#' + hash.replace(/^#?\/?/, '/');
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(
    (h) => location.hash === '#' + h,
    hash.replace(/^#?\/?/, '/')
  );
}

function getConsoleErrors(page) {
  if (!page._consoleErrors) {
    return [];
  }
  return page._consoleErrors;
}

function setupConsoleCapture(page, logArray) {
  const errors = logArray || [];

  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      errors.push({
        type: 'console',
        url: page.url(),
        text: msg.text(),
        timestamp: Date.now(),
      });
    }
  });

  page.on('pageerror', (err) => {
    errors.push({
      type: 'pageerror',
      url: page.url(),
      text: err.message,
      stack: err.stack,
      timestamp: Date.now(),
    });
  });

  page._consoleErrors = errors;
  return errors;
}

function setupNetworkCapture(page, requestArray) {
  const requests = requestArray || [];

  page.on('request', (req) => {
    requests.push({
      type: 'request',
      url: req.url(),
      method: req.method(),
      headers: req.headers(),
      timestamp: Date.now(),
    });
  });

  page.on('response', (res) => {
    requests.push({
      type: 'response',
      url: res.url(),
      status: res.status(),
      headers: res.headers(),
      timestamp: Date.now(),
    });
  });

  return requests;
}

function isDangerousButton(text) {
  if (!text) return false;
  const trimmed = text.trim();
  return DANGEROUS_BUTTON_PATTERNS.some((pattern) => trimmed.indexOf(pattern) >= 0);
}

async function getButtons(page) {
  const buttons = await page.evaluate(() => {
    const clickableSelectors = [
      'button',
      'a[href]',
      'input[type="button"]',
      'input[type="submit"]',
      '[role="button"]',
    ];

    const elements = document.querySelectorAll(clickableSelectors.join(','));
    const result = [];

    elements.forEach((el) => {
      const text = (el.textContent || el.value || '').trim();
      const tagName = el.tagName.toLowerCase();
      const disabled = el.disabled || el.getAttribute('aria-disabled') === 'true';

      let selector = tagName;
      if (el.id) {
        selector += '#' + el.id;
      } else if (el.className && typeof el.className === 'string') {
        const classes = el.className.split(/\s+/).filter(Boolean).slice(0, 3).join('.');
        if (classes) {
          selector += '.' + classes;
        }
      }

      result.push({
        text,
        tagName,
        selector,
        disabled: !!disabled,
      });
    });

    return result;
  });

  return buttons.map((b) => ({
    ...b,
    isDangerous: isDangerousButton(b.text),
  }));
}

function waitForTimeout(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function formatDuration(ms) {
  if (ms < 1000) {
    return ms + 'ms';
  }
  if (ms < 60000) {
    return (ms / 1000).toFixed(2) + 's';
  }
  const minutes = Math.floor(ms / 60000);
  const seconds = ((ms % 60000) / 1000).toFixed(1);
  return minutes + 'm' + seconds + 's';
}

function maskSensitiveData(obj) {
  if (obj === null || obj === undefined) {
    return obj;
  }

  if (typeof obj === 'string') {
    return obj;
  }

  if (Array.isArray(obj)) {
    return obj.map(maskSensitiveData);
  }

  if (typeof obj === 'object') {
    const result = {};
    const sensitiveKeys = ['password', 'passwd', 'pass', 'key', 'token', 'secret', 'private'];

    for (const [key, value] of Object.entries(obj)) {
      const lowerKey = key.toLowerCase();
      if (sensitiveKeys.some((sk) => lowerKey.includes(sk))) {
        result[key] = '***MASKED***';
      } else {
        result[key] = maskSensitiveData(value);
      }
    }

    return result;
  }

  return obj;
}

async function getPageTitle(page) {
  const selectors = [
    '.breadcrumb .active',
    '.breadcrumb-item.active',
    'h3',
    '.card-title',
    'title',
  ];

  for (const selector of selectors) {
    try {
      const el = await page.$(selector);
      if (el) {
        const text = await el.textContent();
        if (text && text.trim()) {
          return text.trim();
        }
      }
    } catch (e) {
      // continue
    }
  }

  return '';
}

async function checkPageHasContent(page) {
  const selectors = [
    '#view',
    '.main-content',
    '.content',
    '.card',
    'main',
  ];

  for (const selector of selectors) {
    try {
      const el = await page.$(selector);
      if (el) {
        const text = await el.textContent();
        if (text && text.trim().length > 10) {
          return true;
        }
      }
    } catch (e) {
      // continue
    }
  }

  return false;
}

async function getAllNavItems(page) {
  const items = await page.evaluate(() => {
    const navSelectors = [
      '#nav .nav-item',
      '.sidebar .nav-item',
      'nav a[data-route]',
      '.menu-item',
    ];

    const elements = document.querySelectorAll(navSelectors.join(','));
    const result = [];

    elements.forEach((el) => {
      const route = el.getAttribute('data-route') || '';
      const text = (el.textContent || '').trim();
      const href = el.getAttribute('href') || '';
      const active = el.classList.contains('active') || el.classList.contains('current');

      if (text || route) {
        result.push({ route, text, href, active });
      }
    });

    return result;
  });

  return items;
}

async function clickNavItem(page, route) {
  const selectors = [
    `a[data-route="${route}"]`,
    `.nav-item[data-route="${route}"]`,
    `a[href="#/${route}"]`,
  ];

  for (const selector of selectors) {
    try {
      const el = await page.$(selector);
      if (el) {
        await el.click();
        await page.waitForFunction(
          (r) => location.hash === '#/' + r,
          route
        );
        return true;
      }
    } catch (e) {
      // continue
    }
  }

  throw new Error(`Nav item not found: ${route}`);
}

async function getActiveNavItem(page) {
  const selectors = [
    '.nav-item.active',
    '.nav-item.current',
    'a.active[data-route]',
    'li.active a',
  ];

  for (const selector of selectors) {
    try {
      const el = await page.$(selector);
      if (el) {
        const text = await el.textContent();
        if (text && text.trim()) {
          return text.trim();
        }
      }
    } catch (e) {
      // continue
    }
  }

  return '';
}

async function checkToastVisible(page) {
  const toastSelectors = [
    '#toast',
    '.toast',
    '.notification',
    '[role="alert"]',
  ];

  for (const selector of toastSelectors) {
    try {
      const el = await page.$(selector);
      if (el) {
        const visible = await el.isVisible();
        if (visible) {
          return true;
        }
      }
    } catch (e) {
      // continue
    }
  }

  return false;
}

async function waitForToast(page, timeout = 5000) {
  const toastSelectors = [
    '#toast',
    '.toast',
    '.notification',
    '[role="alert"]',
  ];

  const startTime = Date.now();

  while (Date.now() - startTime < timeout) {
    for (const selector of toastSelectors) {
      try {
        const el = await page.$(selector);
        if (el) {
          const visible = await el.isVisible();
          if (visible) {
            const text = await el.textContent();
            return { visible: true, text: text ? text.trim() : '' };
          }
        }
      } catch (e) {
        // continue
      }
    }
    await waitForTimeout(100);
  }

  return { visible: false, text: '' };
}

async function closeAllDialogs(page) {
  const overlaySelectors = [
    '.kairo-dialog-overlay',
    '.kairo-modal-overlay',
    '.modal-overlay',
    '[class*="dialog-overlay"]',
  ];

  const closeButtonSelectors = [
    '.kairo-dialog-close',
    '.modal-close',
    '.close-btn',
    '[aria-label="Close"]',
    '[aria-label="close"]',
    '.btn-close',
    '.cancel-btn',
    '.kairo-dialog-footer .kairo-btn-default',
    '.modal-footer .btn-default',
    '.kairo-dialog-footer button:last-child',
    '.modal-footer button:last-child',
  ];

  for (let attempt = 0; attempt < 3; attempt++) {
    let hasVisibleOverlay = false;

    for (const overlaySel of overlaySelectors) {
      try {
        const overlays = await page.$$(overlaySel);
        for (const overlay of overlays) {
          const visible = await overlay.isVisible().catch(() => false);
          if (visible) {
            hasVisibleOverlay = true;
            break;
          }
        }
        if (hasVisibleOverlay) break;
      } catch (e) {
        // continue
      }
    }

    if (!hasVisibleOverlay) {
      return true;
    }

    for (const btnSel of closeButtonSelectors) {
      try {
        const buttons = await page.$$(btnSel);
        for (const btn of buttons) {
          const visible = await btn.isVisible().catch(() => false);
          if (visible) {
            await btn.click().catch(() => {});
            await waitForTimeout(200);
          }
        }
      } catch (e) {
        // continue
      }
    }

    try {
      await page.keyboard.press('Escape');
      await waitForTimeout(200);
    } catch (e) {
      // continue
    }
  }

  let hasVisibleOverlay = false;
  for (const overlaySel of overlaySelectors) {
    try {
      const overlays = await page.$$(overlaySel);
      for (const overlay of overlays) {
        const visible = await overlay.isVisible().catch(() => false);
        if (visible) {
          hasVisibleOverlay = true;
          break;
        }
      }
      if (hasVisibleOverlay) break;
    } catch (e) {
      // continue
    }
  }

  return !hasVisibleOverlay;
}

async function clearTransientUI(page) {
  let clearedCount = 0;

  const dialogClosed = await closeAllDialogs(page);
  if (dialogClosed) {
    clearedCount++;
  }

  const toastSelectors = ['.toast', '.toast-container', '#toast', '.notification'];
  for (const sel of toastSelectors) {
    try {
      const toasts = await page.$$(sel);
      for (const toast of toasts) {
        const visible = await toast.isVisible().catch(() => false);
        if (visible) {
          const isMainToast = await toast.evaluate((el) => el.id === 'toast').catch(() => false);
          if (isMainToast) {
            // 主 toast 节点是页面模板的一部分，只能隐藏不能 remove，
            // 否则后续 toast() 会因 $('#toast') 为 null 抛 "Cannot set properties of null"。
            await toast.evaluate((el) => {
              el.className = 'toast';
              el.textContent = '';
            }).catch(() => {});
          } else {
            await toast.evaluate((el) => el.remove()).catch(() => {});
          }
          clearedCount++;
        }
      }
    } catch (e) {
      // continue
    }
  }

  return clearedCount;
}

async function ensureNoOverlay(page) {
  const overlaySelectors = [
    '.kairo-dialog-overlay',
    '.kairo-modal-overlay',
    '.modal-overlay',
    '[class*="dialog-overlay"]',
    '.el-overlay',
    '[class*="mask"]',
  ];

  let hasOverlay = false;
  for (const sel of overlaySelectors) {
    try {
      const overlays = await page.$$(sel);
      for (const overlay of overlays) {
        const visible = await overlay.isVisible().catch(() => false);
        if (visible) {
          hasOverlay = true;
          break;
        }
      }
      if (hasOverlay) break;
    } catch (e) {
      // continue
    }
  }

  if (!hasOverlay) {
    return true;
  }

  const closed = await closeAllDialogs(page);
  if (closed) {
    return true;
  }

  try {
    await takeScreenshot(page, 'overlay-blocked');
  } catch (e) {
    // continue
  }

  throw new Error('Overlay is still visible after attempting to close all dialogs');
}

async function findFirstVisible(page, selectors, timeout = 500) {
  for (const selector of selectors) {
    try {
      const el = await page.waitForSelector(selector, {
        timeout,
        state: 'visible',
      }).catch(() => null);
      if (el) {
        return el;
      }
    } catch (e) {
      // continue
    }
  }
  return null;
}

async function clickFirstVisible(page, selectors, options = {}) {
  await ensureNoOverlay(page);

  const tried = [];
  for (const selector of selectors) {
    tried.push(selector);
    try {
      const el = await page.waitForSelector(selector, {
        timeout: options.timeout || 500,
        state: 'visible',
      }).catch(() => null);
      if (el) {
        await el.click(options);
        return true;
      }
    } catch (e) {
      // continue
    }
  }

  throw new Error(`No visible element found. Tried selectors: ${tried.join(', ')}`);
}

async function fillFirstVisible(page, selectors, value, options = {}) {
  await ensureNoOverlay(page);

  const tried = [];
  for (const selector of selectors) {
    tried.push(selector);
    try {
      const el = await page.waitForSelector(selector, {
        timeout: options.timeout || 500,
        state: 'visible',
      }).catch(() => null);
      if (el) {
        const disabled = await el.evaluate((e) => e.disabled || e.readOnly).catch(() => false);
        if (!disabled) {
          await el.fill(value, options);
          return true;
        }
      }
    } catch (e) {
      // continue
    }
  }

  throw new Error(`No visible input found. Tried selectors: ${tried.join(', ')}`);
}

async function selectFirstEnabled(page, selectors, value, options = {}) {
  await ensureNoOverlay(page);

  const tried = [];
  for (const selector of selectors) {
    tried.push(selector);
    try {
      const el = await page.waitForSelector(selector, {
        timeout: options.timeout || 500,
        state: 'visible',
      }).catch(() => null);
      if (el) {
        const disabled = await el.evaluate((e) => e.disabled).catch(() => true);
        if (!disabled) {
          await el.selectOption(value, options);
          return true;
        }
      }
    } catch (e) {
      // continue
    }
  }

  throw new Error(`No enabled select found. Tried selectors: ${tried.join(', ')}`);
}

async function getPageDomSummary(page) {
  try {
    const summary = await page.evaluate(() => {
      const dialogSelectors = [
        '.kairo-dialog-overlay',
        '.kairo-modal-overlay',
        '.modal-overlay',
        '[class*="dialog-overlay"]',
      ];

      let visibleDialogs = 0;
      for (const sel of dialogSelectors) {
        const els = document.querySelectorAll(sel);
        for (const el of els) {
          const style = window.getComputedStyle(el);
          if (style.display !== 'none' && style.visibility !== 'hidden' && el.offsetParent !== null) {
            visibleDialogs++;
          }
        }
      }

      const allButtons = document.querySelectorAll('button, [role="button"], input[type="button"], input[type="submit"]');
      let visibleButtons = 0;
      for (const btn of allButtons) {
        const style = window.getComputedStyle(btn);
        if (style.display !== 'none' && style.visibility !== 'hidden' && btn.offsetParent !== null) {
          visibleButtons++;
        }
      }

      const allInputs = document.querySelectorAll('input, textarea, select');
      let visibleInputs = 0;
      for (const inp of allInputs) {
        const style = window.getComputedStyle(inp);
        if (style.display !== 'none' && style.visibility !== 'hidden' && inp.offsetParent !== null) {
          visibleInputs++;
        }
      }

      return {
        visibleDialogs,
        visibleButtons,
        visibleInputs,
      };
    });

    return {
      url: page.url(),
      hash: await page.evaluate(() => location.hash).catch(() => ''),
      visibleDialogs: summary.visibleDialogs,
      visibleButtons: summary.visibleButtons,
      visibleInputs: summary.visibleInputs,
    };
  } catch (e) {
    return {
      url: page.url ? page.url() : 'unknown',
      hash: '',
      visibleDialogs: 0,
      visibleButtons: 0,
      visibleInputs: 0,
      error: e.message,
    };
  }
}

module.exports = {
  takeScreenshot,
  requireElement,
  sanitizeFileName,
  setTheme,
  waitForPageReady,
  gotoHash,
  getConsoleErrors,
  setupConsoleCapture,
  setupNetworkCapture,
  getButtons,
  isDangerousButton,
  waitForTimeout,
  formatDuration,
  maskSensitiveData,
  getPageTitle,
  checkPageHasContent,
  getAllNavItems,
  clickNavItem,
  getActiveNavItem,
  checkToastVisible,
  waitForToast,
  closeAllDialogs,
  clearTransientUI,
  ensureNoOverlay,
  findFirstVisible,
  clickFirstVisible,
  fillFirstVisible,
  selectFirstEnabled,
  getPageDomSummary,
};
