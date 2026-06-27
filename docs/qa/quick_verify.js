const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('dialog', d => d.accept().catch(() => {}));

  await page.goto('http://127.0.0.1:18092/#/config', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);

  // 1. 检查无syncSaveBtns错误
  const syncErr = errors.filter(e => e.includes('syncSaveBtns'));
  console.log('1. syncSaveBtns错误数:', syncErr.length, syncErr.length === 0 ? 'PASS' : 'FAIL');

  // 2. 新增系统
  const before = await page.$$eval('.sys-block', els => els.length);
  await page.click('button:text("+ 新增业务系统")');
  await page.waitForTimeout(300);
  const after = await page.$$eval('.sys-block', els => els.length);
  console.log('2. 新增系统:', before, '->', after, after === before + 1 ? 'PASS' : 'FAIL');

  // 3. 保存按钮enabled
  const saveDisabled = await page.$eval('#cfg-save-btn', b => b.disabled);
  console.log('3. 保存按钮enabled:', !saveDisabled ? 'PASS' : 'FAIL');

  // 4. 底部按钮同步
  const footerBtns = await page.$$('.cfg-save-footer-bar .btn-primary');
  let footerOk = true;
  for (const b of footerBtns) { if (await b.isDisabled()) footerOk = false; }
  console.log('4. 底部按钮同步:', footerOk ? 'PASS' : 'FAIL');

  // 5. 端口校验
  const portInp = await page.$('.sys-block:first-child .srv-block:first-child input[type="number"]');
  await portInp.fill('99999');
  await page.waitForTimeout(200);
  const borderRed = await portInp.evaluate(e => {
    const c = getComputedStyle(e).borderColor;
    return c.includes('239') || c.includes('ef4444') || c.includes('red');
  });
  console.log('5. 端口99999标红:', borderRed ? 'PASS' : 'FAIL');

  // 6. 端口恢复
  await portInp.fill('22');
  await page.waitForTimeout(200);
  const borderNormal = await portInp.evaluate(e => {
    const c = getComputedStyle(e).borderColor;
    return !(c.includes('239') || c.includes('ef4444') || c.includes('red'));
  });
  console.log('6. 端口22恢复:', borderNormal ? 'PASS' : 'FAIL');

  // 7. min/max
  const min = await portInp.getAttribute('min');
  const max = await portInp.getAttribute('max');
  console.log('7. min/max:', min === '1' && max === '65535' ? 'PASS' : 'FAIL', '(min=' + min + ', max=' + max + ')');

  // 8. 系统复制
  const beforeDup = await page.$$eval('.sys-block', els => els.length);
  await page.click('.sys-block:first-child .sys-actions button:text("复制")');
  await page.waitForTimeout(300);
  const afterDup = await page.$$eval('.sys-block', els => els.length);
  console.log('8. 系统复制:', beforeDup, '->', afterDup, afterDup === beforeDup + 1 ? 'PASS' : 'FAIL');

  // 9. 系统下移
  const inputs = await page.$$('.sys-block .sys-name-input input');
  await inputs[0].fill('AAA');
  await inputs[1].fill('BBB');
  await page.waitForTimeout(200);
  await page.click('.sys-block:first-child .sys-actions button:text("↓")');
  await page.waitForTimeout(300);
  const afterInputs = await page.$$('.sys-block .sys-name-input input');
  const firstName = await afterInputs[0].inputValue();
  console.log('9. 下移后顺序:', firstName === 'BBB' ? 'PASS' : 'FAIL', '(第一=' + firstName + ')');

  // 10. 服务器复制
  const srvBefore = await page.$$eval('.sys-block:first-child .srv-block', els => els.length);
  await page.click('.sys-block:first-child .srv-block:first-child button:text("📋 复制服务器（含日志目录）")');
  await page.waitForTimeout(300);
  const srvAfter = await page.$$eval('.sys-block:first-child .srv-block', els => els.length);
  console.log('10. 服务器复制:', srvBefore, '->', srvAfter, srvAfter === srvBefore + 1 ? 'PASS' : 'FAIL');

  // 11. 日志目录复制
  const dirBefore = await page.$$eval('.sys-block:first-child .srv-block:first-child .dir-block', els => els.length);
  await page.click('.sys-block:first-child .srv-block:first-child .dir-block:first-child button:text("复制目录")');
  await page.waitForTimeout(300);
  const dirAfter = await page.$$eval('.sys-block:first-child .srv-block:first-child .dir-block', els => els.length);
  console.log('11. 日志目录复制:', dirBefore, '->', dirAfter, dirAfter === dirBefore + 1 ? 'PASS' : 'FAIL');

  // 12. 编码切换
  const encSel = await page.$('.sys-block:first-child .srv-block:first-child .dir-block:first-child select');
  await encSel.selectOption('gbk');
  await page.waitForTimeout(200);
  const encVal = await encSel.inputValue();
  console.log('12. 编码切换gbk:', encVal === 'gbk' ? 'PASS' : 'FAIL');

  // 13. 打开器添加+保存
  await page.click('button:text("+ 添加打开器")');
  await page.waitForTimeout(300);
  const nameInp = await page.$('.opener-row input[placeholder*="Notepad"]');
  await nameInp.fill('TestApp');
  const pathInp = await page.$('.opener-row input[placeholder*="notepad"]');
  await pathInp.fill('/usr/bin/testapp');
  await page.waitForTimeout(200);
  await page.click('.opener-row button:text("💾 保存")');
  await page.waitForTimeout(1500);
  const toast = await page.textContent('#toast');
  console.log('13. 打开器保存:', toast.includes('保存') ? 'PASS' : 'FAIL', '(' + toast + ')');

  // 14. 放弃改动
  await page.click('button:text("放弃改动")');
  await page.waitForTimeout(1500);
  const saveDisabled2 = await page.$eval('#cfg-save-btn', b => b.disabled);
  console.log('14. 放弃后disabled:', saveDisabled2 ? 'PASS' : 'FAIL');

  // 15. 页面切换state保持
  await page.goto('http://127.0.0.1:18092/#/home', { waitUntil: 'networkidle' });
  await page.waitForTimeout(500);
  await page.goto('http://127.0.0.1:18092/#/config', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);
  const cards = await page.$$('.card');
  console.log('15. 切换后卡片:', cards.length >= 4 ? 'PASS' : 'FAIL', '(' + cards.length + '个)');

  // 16. 总错误数
  console.log('\n16. 页面运行时错误数:', errors.length, errors.length === 0 ? 'PASS' : 'FAIL');
  if (errors.length > 0) console.log('   错误:', errors.slice(0, 3).join(' | '));

  await browser.close();
})();
