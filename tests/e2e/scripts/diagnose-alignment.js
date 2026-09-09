'use strict';

const { chromium } = require('playwright');

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });

  console.log('='.repeat(70));
  console.log('🔍 启动全工作台“横平竖直、文本框与按钮齐平”深度排查');
  console.log('='.repeat(70));

  async function checkPageAlignments(pageName, hash, setupFn) {
    console.log(`\n--- 检查页面: ${pageName} (${hash}) ---`);
    await page.goto(BASE_URL + hash, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(500);
    if (setupFn) {
      await setupFn(page);
      await page.waitForTimeout(400);
    }

    const report = await page.evaluate(() => {
      const results = [];

      // 1. 查找所有横向排列的容器（flex-row 或 inline 排列，包含 input, select, button 中的至少两者）
      const allElements = Array.from(document.querySelectorAll('*'));
      const checkedParents = new Set();

      for (const el of allElements) {
        // 查找包含多个交互控件的容器
        const controls = Array.from(el.querySelectorAll(':scope > input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]), :scope > select, :scope > button, :scope > .btn, :scope > .field > input, :scope > .field > select, :scope > .field > button'));
        if (controls.length >= 2 && !checkedParents.has(el)) {
          checkedParents.add(el);
          const style = window.getComputedStyle(el);
          const isRow = style.display.includes('flex') && !style.flexDirection.includes('column') ||
                        style.display.includes('grid') && style.gridTemplateColumns.split(' ').length > 1 ||
                        style.display.includes('inline');

          if (isRow) {
            // 获取每个控件的实际渲染矩形
            const rects = controls.map(c => {
              const r = c.getBoundingClientRect();
              return {
                tag: c.tagName.toLowerCase(),
                type: c.getAttribute('type') || '',
                className: (c.className || '').slice(0, 30),
                id: c.id,
                text: (c.textContent || c.value || c.placeholder || '').trim().slice(0, 15),
                rect: { top: Math.round(r.top * 10) / 10, bottom: Math.round(r.bottom * 10) / 10, height: Math.round(r.height * 10) / 10, left: Math.round(r.left * 10) / 10 }
              };
            }).filter(item => item.rect.height > 0);

            // 检查同一行相邻的 input/select/button 是否齐平
            for (let i = 0; i < rects.length - 1; i++) {
              const a = rects[i];
              const b = rects[i + 1];
              // 判断它们是否在同一垂直带（大致在同一水平行）
              const verticalOverlap = Math.min(a.rect.bottom, b.rect.bottom) - Math.max(a.rect.top, b.rect.top);
              if (verticalOverlap > 10) { // 在同一行
                const topDiff = Math.abs(a.rect.top - b.rect.top);
                const bottomDiff = Math.abs(a.rect.bottom - b.rect.bottom);
                const heightDiff = Math.abs(a.rect.height - b.rect.height);

                if (topDiff > 1.5 || bottomDiff > 1.5 || heightDiff > 1.5) {
                  results.push({
                    parent: el.tagName.toLowerCase() + (el.className ? '.' + el.className.split(' ').join('.') : '') + (el.id ? '#' + el.id : ''),
                    itemA: `${a.tag}${a.id ? '#' + a.id : ''}(${a.className})[h:${a.rect.height}, top:${a.rect.top}]`,
                    itemB: `${b.tag}${b.id ? '#' + b.id : ''}(${b.className})[h:${b.rect.height}, top:${b.rect.top}]`,
                    topDiff,
                    bottomDiff,
                    heightDiff
                  });
                }
              }
            }
          }
        }
      }

      // 2. 特别专项检查：所有表单行 .field / .form-row / .path-field / .db-field
      const rows = Array.from(document.querySelectorAll('.path-field, .waspack-path, .cmp2-folder-path-row, .cmp-folder-source, .db-record-nav, .db-toolbar, .waspack-primary-actions, .waspack-package-line, .http-url-bar, .http-nav, .db-form-actions'));
      for (const row of rows) {
        const inputsAndBtns = Array.from(row.querySelectorAll('input:not([type="checkbox"]):not([type="radio"]), select, button, .btn'))
          .filter(c => {
            const r = c.getBoundingClientRect();
            return r.width > 0 && r.height > 0;
          });

        if (inputsAndBtns.length >= 2) {
          const first = inputsAndBtns[0].getBoundingClientRect();
          for (let k = 1; k < inputsAndBtns.length; k++) {
            const cur = inputsAndBtns[k].getBoundingClientRect();
            // 在同一行
            if (Math.min(first.bottom, cur.bottom) - Math.max(first.top, cur.top) > 8) {
              const topDiff = Math.abs(Math.round(first.top - cur.top));
              const heightDiff = Math.abs(Math.round(first.height - cur.height));
              if (topDiff > 1 || heightDiff > 1) {
                results.push({
                  specialRow: row.className,
                  first: `${inputsAndBtns[0].tagName.toLowerCase()}#${inputsAndBtns[0].id}(${inputsAndBtns[0].className}) h:${Math.round(first.height)} top:${Math.round(first.top)}`,
                  cur: `${inputsAndBtns[k].tagName.toLowerCase()}#${inputsAndBtns[k].id}(${inputsAndBtns[k].className}) h:${Math.round(cur.height)} top:${Math.round(cur.top)}`,
                  topDiff,
                  heightDiff
                });
              }
            }
          }
        }
      }

      return results;
    });

    if (report.length === 0) {
      console.log('  ✅ 未发现明显控件错位或高低不平');
    } else {
      console.log(`  ⚠️ 发现 ${report.length} 处控件可能未齐平:`);
      for (const r of report) {
        if (r.specialRow) {
          console.log(`    - [${r.specialRow}] 对比: ${r.first} vs ${r.cur} -> 高度差: ${r.heightDiff}px, 顶部差: ${r.topDiff}px`);
        } else {
          console.log(`    - [${r.parent}] ${r.itemA} vs ${r.itemB} -> 顶部差: ${r.topDiff}px, 高度差: ${r.heightDiff}px`);
        }
      }
    }
    return report;
  }

  // 1. 数据库主界面
  await checkPageAlignments('数据库工作台主页', '#/database', async (p) => {
    const runBtn = await p.$('#db-run');
    if (runBtn) await runBtn.click().catch(() => {});
  });

  // 2. 数据库管理抽屉
  await checkPageAlignments('数据库管理抽屉', '#/database', async (p) => {
    const manageBtn = await p.$('#db-manage');
    if (manageBtn) await manageBtn.click().catch(() => {});
  });

  // 3. 比较工作台
  await checkPageAlignments('比对工作台', '#/compare', async (p) => {
    const folderTab = await p.$('.cmp2-tab:has-text("文件夹"), button:has-text("文件夹比对")');
    if (folderTab) await folderTab.click().catch(() => {});
  });

  // 4. 打包工作台
  await checkPageAlignments('WAS 投产打包', '#/waspack', async (p) => {
    const adv = await p.$('.waspack-advanced summary');
    if (adv) await adv.click().catch(() => {});
  });

  // 5. HTTP 客户端
  await checkPageAlignments('HTTP 测试工具', '#/http', async (p) => {});

  // 6. 文件下载
  await checkPageAlignments('文件下载', '#/files', async (p) => {});

  await browser.close();
  console.log('\n排查结束。');
})();
