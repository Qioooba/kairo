const { chromium } = require('d:/kairo/node_modules/playwright');
const path = require('path');
const assert = require('assert');

async function testSQLEditor() {
  console.log('Starting SQL Editor Browser Verification Test...');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();

  page.on('console', msg => console.log('PAGE LOG:', msg.text()));
  page.on('pageerror', err => console.error('PAGE ERROR:', err));

  await page.goto('http://127.0.0.1:18092/#/database');
  await page.waitForTimeout(2000);

  const taExists = await page.$('#db-sql');
  assert.ok(taExists, '#db-sql must exist on page');

  // ==========================================
  // Test 1: Resize Synchronization Verification
  // ==========================================
  console.log('\n--- Test 1: Resize Synchronization Verification ---');
  // Fill 40 lines of SQL
  const lines40 = Array.from({ length: 40 }, (_, i) => `SELECT col_${i}, 'value_${i}' FROM table_${i};`).join('\n');
  await page.evaluate((val) => {
    const ta = document.getElementById('db-sql');
    ta.value = val;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }, lines40);
  await page.waitForTimeout(300);

  // Resize textarea to 520px
  await page.evaluate(() => {
    const ta = document.getElementById('db-sql');
    ta.style.height = '520px';
  });
  // Wait for ResizeObserver to trigger
  await page.waitForTimeout(300);

  const resizeCheck = await page.evaluate(() => {
    const ta = document.getElementById('db-sql');
    const hl = document.getElementById('db-sql-highlight');
    const shell = document.querySelector('.db-sql-shell');
    return {
      taH: ta.clientHeight,
      hlH: hl.clientHeight,
      shellH: shell.clientHeight
    };
  });
  console.log('After textarea resize to 520px:', resizeCheck);
  assert.strictEqual(resizeCheck.taH, 520, 'Textarea height must be 520px');
  assert.strictEqual(resizeCheck.hlH, 520, 'Highlight layer height must sync to 520px');
  assert.strictEqual(resizeCheck.shellH, 520, 'Shell height must sync to 520px');

  const scrResize = path.join(__dirname, '../../test-results/screenshots/sql_editor_resize_synced.png');
  await page.screenshot({ path: scrResize });
  console.log(`Saved screenshot: ${scrResize}`);

  // ==========================================
  // Test 2: Large SQL Degradation to Plain Editor
  // ==========================================
  console.log('\n--- Test 2: Large SQL Degradation to Plain Editor ---');
  const countLarge = 3500;
  const sqlLarge = Array.from({ length: countLarge }, (_, i) => `SELECT col_${i}, 'large_${i}' FROM large_table_${i % 10} WHERE id = ${i};`).join('\n');

  const t0 = Date.now();
  await page.evaluate((val) => {
    const ta = document.getElementById('db-sql');
    ta.value = val;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }, sqlLarge);
  const inputDuration = Date.now() - t0;
  console.log(`Setting 3500 lines took: ${inputDuration}ms (instantaneous degradation)`);
  await page.waitForTimeout(300);

  const largeState = await page.evaluate(() => {
    const ta = document.getElementById('db-sql');
    const hl = document.getElementById('db-sql-highlight');
    const shell = document.querySelector('.db-sql-shell');
    const taStyle = window.getComputedStyle(ta);
    const hlStyle = window.getComputedStyle(hl);
    return {
      isPlain: ta.classList.contains('is-plain-editor'),
      shellPlain: shell.classList.contains('is-plain-editor'),
      hlDisplay: hlStyle.display,
      taColor: taStyle.color,
      taWebkitFill: taStyle.webkitTextFillColor,
      taScrollHeight: ta.scrollHeight
    };
  });
  console.log('Large SQL editor state:', largeState);
  assert.strictEqual(largeState.isPlain, true, 'Textarea must have is-plain-editor class');
  assert.strictEqual(largeState.hlDisplay, 'none', 'Highlight layer must be hidden');
  assert.notStrictEqual(largeState.taColor, 'rgba(0, 0, 0, 0)', 'Textarea color must NOT be transparent');

  const scrLarge = path.join(__dirname, '../../test-results/screenshots/sql_editor_large_plain.png');
  await page.screenshot({ path: scrLarge });
  console.log(`Saved screenshot: ${scrLarge}`);

  // ==========================================
  // Test 3: Return to Normal Size Restores Syntax Highlighting
  // ==========================================
  console.log('\n--- Test 3: Restoration of Normal Syntax Highlighting ---');
  const normalSql = `SELECT id, name, created_at\nFROM users\nWHERE status = 'ACTIVE' AND id = 101;`;
  await page.evaluate((val) => {
    const ta = document.getElementById('db-sql');
    ta.value = val;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }, normalSql);
  await page.waitForTimeout(300);

  const normalState = await page.evaluate(() => {
    const ta = document.getElementById('db-sql');
    const hl = document.getElementById('db-sql-highlight');
    const shell = document.querySelector('.db-sql-shell');
    const hlStyle = window.getComputedStyle(hl);
    return {
      isPlain: ta.classList.contains('is-plain-editor'),
      hlDisplay: hlStyle.display,
      hlKwCount: hl.querySelectorAll('.db-sql-kw').length,
      hlStrCount: hl.querySelectorAll('.db-sql-str').length
    };
  });
  console.log('Normal SQL editor state:', normalState);
  assert.strictEqual(normalState.isPlain, false, 'is-plain-editor should be removed for normal SQL');
  assert.notStrictEqual(normalState.hlDisplay, 'none', 'Highlight layer should be visible');
  assert.ok(normalState.hlKwCount > 0, 'Keyword highlights must be present');
  assert.ok(normalState.hlStrCount > 0, 'String highlights must be present');

  const scrNormal = path.join(__dirname, '../../test-results/screenshots/sql_editor_normal_restored.png');
  await page.screenshot({ path: scrNormal });
  console.log(`Saved screenshot: ${scrNormal}`);

  console.log('\n✅ ALL SQL EDITOR BROWSER VERIFICATION TESTS PASSED!');
  await browser.close();
}

testSQLEditor().catch(err => {
  console.error('Test failed:', err);
  process.exit(1);
});
