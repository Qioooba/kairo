'use strict';

/**
 * SQL 编辑器大文本降级与高度同步 (QA-02 移植)
 *
 * 原脚本 tests/e2e/test-sql-editor-large-text.js 的问题:
 *   1. 按开发机绝对路径 require playwright (盘符 + node_modules 全写死), 别的 checkout 直接崩
 *   2. 固定服务地址 (127.0.0.1:18092 字面量), 不走 BASE_URL
 *   3. 位于统一 runner 注册目录之外, 不在 npm test / e2e runner 的任何入口里,
 *      既不出现"发现/执行/通过"统计, 也无法在 CI 里被跑起来
 *   4. 直接改 `ta.value`, 绕过真实输入路径; 选择器缺失时只有 assert 兜底
 *
 * 本模块改为统一 runner 注册机制: 用 ctx.baseUrl、helpers.requireElement,
 * 输入走 page.fill (真实输入事件), 拖拽走真实鼠标事件; 程序化设定高度只作为
 * 低层补充, 并明确标注。
 */

const assert = require('assert');

function register(runner, ctx) {
  const { page, baseUrl, helpers } = ctx;

  const SQL_SELECTOR = '#db-sql';
  const HL_SELECTOR = '#db-sql-highlight';
  const SHELL_SELECTOR = '.db-sql-shell';

  async function openDatabaseWorkbench() {
    await page.goto(baseUrl + '/#/database', { waitUntil: 'domcontentloaded' });
    await helpers.requireElement(page, SQL_SELECTOR, 'SQL 编辑区 (#db-sql) 必须存在');
    await helpers.requireElement(page, HL_SELECTOR, '高亮层 (#db-sql-highlight) 必须存在');
    await helpers.requireElement(page, SHELL_SELECTOR, '编辑器外壳 (.db-sql-shell) 必须存在');
    await page.waitForTimeout(500);
    // 本用例断言的是上下布局下的编辑器高度契约；若工作台偏好残留为左右分栏
    // （列宽决定高度、内联 px 被 CSS 覆盖），先复位为上下布局。
    const columns = await page.evaluate(function () {
      const main = document.getElementById('db-main');
      return !!(main && main.classList.contains('is-columns'));
    });
    if (columns && (await page.$('#db-layout-toggle'))) {
      await page.click('#db-layout-toggle');
      await page.waitForTimeout(400);
    }
  }

  async function readEditorState() {
    return page.evaluate(function (sels) {
      const ta = document.querySelector(sels.sql);
      const hl = document.querySelector(sels.hl);
      const shell = document.querySelector(sels.shell);
      return {
        taHeight: ta.clientHeight,
        hlHeight: hl.clientHeight,
        shellHeight: shell.clientHeight,
        isPlain: ta.classList.contains('is-plain-editor'),
        shellPlain: shell.classList.contains('is-plain-editor'),
        hlDisplay: window.getComputedStyle(hl).display,
        taColor: window.getComputedStyle(ta).color,
        highlightKeywordCount: hl.querySelectorAll('.db-sql-kw').length,
        highlightStringCount: hl.querySelectorAll('.db-sql-str').length,
      };
    }, { sql: SQL_SELECTOR, hl: HL_SELECTOR, shell: SHELL_SELECTOR });
  }

  function assertHeightsSynced(state, label) {
    assert.strictEqual(
      state.hlHeight, state.taHeight,
      `${label}: 高亮层高度(${state.hlHeight}) 必须与正文层(${state.taHeight}) 同步`
    );
    assert.strictEqual(
      state.shellHeight, state.taHeight,
      `${label}: 外壳高度(${state.shellHeight}) 必须与正文层(${state.taHeight}) 同步`
    );
  }

  runner.describe('SQL 编辑器大文本与高度拉伸', function () {
    runner.beforeEach(async function () {
      await openDatabaseWorkbench();
    });

    runner.it('40 行文本拉伸到 520px 后正文/高亮/外壳三层高度同步', async function () {
      const lines40 = Array.from({ length: 40 }, function (_, i) {
        return `SELECT col_${i}, 'value_${i}' FROM table_${i};`;
      }).join('\n');

      // 真实 fill: 走输入事件, 不是直接改 value
      await page.fill(SQL_SELECTOR, lines40);
      await page.waitForTimeout(300);

      // 低层补充: 程序化设定高度, 专门验证 ResizeObserver 的三层同步
      await page.evaluate(function (sels) {
        document.querySelector(sels.sql).style.height = '520px';
      }, { sql: SQL_SELECTOR });
      await page.waitForTimeout(300);

      const state = await readEditorState();
      assert.strictEqual(state.taHeight, 520, `正文层高度应为 520, got=${state.taHeight}`);
      assertHeightsSynced(state, '程序化拉伸 520px');

      await runner.screenshot(page, '43-sql-01-resize-synced');
    });

    runner.it('真实鼠标拖拽拉伸后三层高度保持同步', async function () {
      const handle = await helpers.requireElement(page, SQL_SELECTOR, '拖拽目标必须是 SQL 编辑区');
      const box = await handle.boundingBox();
      assert.ok(box, 'SQL 编辑区必须有可见的 bounding box 才能拖拽');

      await page.mouse.move(box.x + box.width - 3, box.y + box.height - 3);
      await page.mouse.down();
      await page.mouse.move(box.x + box.width - 3, box.y + box.height + 120, { steps: 10 });
      await page.mouse.up();
      await page.waitForTimeout(400);

      // 只断言同步不变式, 不强制具体像素: UA 是否允许 user-resize 由环境决定,
      // 但"三层必须一致"是本缺陷的核心契约。
      const state = await readEditorState();
      assertHeightsSynced(state, '真实拖拽之后');

      await runner.screenshot(page, '43-sql-02-resize-drag-synced');
    });

    runner.it('3500 行大文本立即降级为纯文本编辑器', async function () {
      const countLarge = 3500;
      const sqlLarge = Array.from({ length: countLarge }, function (_, i) {
        return `SELECT col_${i}, 'large_${i}' FROM large_table_${i % 10} WHERE id = ${i};`;
      }).join('\n');

      const t0 = Date.now();
      await page.fill(SQL_SELECTOR, sqlLarge);
      const inputDuration = Date.now() - t0;
      console.log(`  [43] 填入 3500 行耗时 ${inputDuration}ms`);
      await page.waitForTimeout(400);

      const state = await readEditorState();
      assert.strictEqual(state.isPlain, true, '大文本时正文层必须带 is-plain-editor');
      assert.strictEqual(state.shellPlain, true, '大文本时外壳必须带 is-plain-editor');
      assert.strictEqual(state.hlDisplay, 'none', '大文本时高亮层必须隐藏');
      assert.notStrictEqual(state.taColor, 'rgba(0, 0, 0, 0)', '大文本时正文颜色不得透明(否则文字不可见)');

      await runner.screenshot(page, '43-sql-03-large-plain');
    });

    runner.it('恢复为普通长度后语法高亮与透明正文层恢复', async function () {
      await page.fill(SQL_SELECTOR, 'placeholder line');
      await page.waitForTimeout(200);

      const normalSql = "SELECT id, name, created_at\nFROM users\nWHERE status = 'ACTIVE' AND id = 101;";
      await page.fill(SQL_SELECTOR, normalSql);
      await page.waitForTimeout(400);

      const state = await readEditorState();
      assert.strictEqual(state.isPlain, false, '普通长度时必须移除 is-plain-editor');
      assert.notStrictEqual(state.hlDisplay, 'none', '普通长度时高亮层必须可见');
      assert.ok(state.highlightKeywordCount > 0, '普通 SQL 必须有关键字高亮');
      assert.ok(state.highlightStringCount > 0, '普通 SQL 必须有字符串高亮');

      await runner.screenshot(page, '43-sql-04-normal-restored');
    });
  });
}

module.exports = { register };
