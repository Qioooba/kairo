const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('dialog', d => d.accept().catch(() => {}));

  await page.goto('http://127.0.0.1:18092/#/config', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  // 检查页面实际内容
  const btnTexts = await page.$$eval('.btn-row .btn', els => els.map(e => e.textContent.trim()));
  console.log('顶部按钮:', JSON.stringify(btnTexts));

  const syncErr = errors.filter(e => e.includes('syncSaveBtns'));
  console.log('syncSaveBtns错误数:', syncErr.length, syncErr.length === 0 ? 'PASS' : 'FAIL');

  // 用contains匹配
  const addBtn = page.locator('button', { hasText: '新增业务系统' });
  const addBtnCount = await addBtn.count();
  console.log('新增按钮数量:', addBtnCount);

  if (addBtnCount > 0) {
    const before = await page.$$eval('.sys-block', els => els.length);
    await addBtn.first().click();
    await page.waitForTimeout(300);
    const after = await page.$$eval('.sys-block', els => els.length);
    console.log('新增系统:', before, '->', after, after === before + 1 ? 'PASS' : 'FAIL');

    const saveDisabled = await page.$eval('#cfg-save-btn', b => b.disabled);
    console.log('保存按钮enabled:', !saveDisabled ? 'PASS' : 'FAIL');

    // 端口校验
    const portInp = page.locator('.sys-block:first-child .srv-block:first-child input[type="number"]');
    if (await portInp.count() > 0) {
      await portInp.fill('99999');
      await page.waitForTimeout(200);
      const borderRed = await portInp.evaluate(e => {
        const c = getComputedStyle(e).borderColor;
        return c.includes('239') || c.includes('ef4444') || c.includes('red');
      });
      console.log('端口99999标红:', borderRed ? 'PASS' : 'FAIL');

      await portInp.fill('22');
      await page.waitForTimeout(200);
      const borderNormal = await portInp.evaluate(e => {
        const c = getComputedStyle(e).borderColor;
        return !(c.includes('239') || c.includes('ef4444') || c.includes('red'));
      });
      console.log('端口22恢复:', borderNormal ? 'PASS' : 'FAIL');

      const min = await portInp.getAttribute('min');
      const max = await portInp.getAttribute('max');
      console.log('min/max:', min === '1' && max === '65535' ? 'PASS' : 'FAIL', '(min=' + min + ',max=' + max + ')');
    }

    // 底部按钮同步
    const footerBtns = await page.$$('.cfg-save-footer-bar .btn-primary');
    let footerOk = true;
    for (const b of footerBtns) { if (await b.isDisabled()) footerOk = false; }
    console.log('底部按钮同步:', footerOk ? 'PASS' : 'FAIL');

    // 系统复制
    const dupBtn = page.locator('.sys-block:first-child .sys-actions button', { hasText: '复制' });
    if (await dupBtn.count() > 0) {
      const beforeDup = await page.$$eval('.sys-block', els => els.length);
      await dupBtn.first().click();
      await page.waitForTimeout(300);
      const afterDup = await page.$$eval('.sys-block', els => els.length);
      console.log('系统复制:', beforeDup, '->', afterDup, afterDup === beforeDup + 1 ? 'PASS' : 'FAIL');
    }

    // 系统下移
    const downBtn = page.locator('.sys-block:first-child .sys-actions button', { hasText: '↓' });
    if (await downBtn.count() > 0) {
      const inputs = await page.$$('.sys-block .sys-name-input input');
      if (inputs.length >= 2) {
        await inputs[0].fill('AAA');
        await inputs[1].fill('BBB');
        await page.waitForTimeout(200);
        await downBtn.first().click();
        await page.waitForTimeout(300);
        const afterInputs = await page.$$('.sys-block .sys-name-input input');
        const firstName = await afterInputs[0].inputValue();
        console.log('系统下移:', firstName === 'BBB' ? 'PASS' : 'FAIL', '(第一=' + firstName + ')');
      }
    }

    // 服务器复制
    const srvDupBtn = page.locator('.sys-block:first-child .srv-block:first-child button', { hasText: '复制服务器' });
    if (await srvDupBtn.count() > 0) {
      const srvBefore = await page.$$eval('.sys-block:first-child .srv-block', els => els.length);
      await srvDupBtn.first().click();
      await page.waitForTimeout(300);
      const srvAfter = await page.$$eval('.sys-block:first-child .srv-block', els => els.length);
      console.log('服务器复制:', srvBefore, '->', srvAfter, srvAfter === srvBefore + 1 ? 'PASS' : 'FAIL');
    }

    // 日志目录复制
    const dirDupBtn = page.locator('.sys-block:first-child .srv-block:first-child .dir-block:first-child button', { hasText: '复制目录' });
    if (await dirDupBtn.count() > 0) {
      const dirBefore = await page.$$eval('.sys-block:first-child .srv-block:first-child .dir-block', els => els.length);
      await dirDupBtn.first().click();
      await page.waitForTimeout(300);
      const dirAfter = await page.$$eval('.sys-block:first-child .srv-block:first-child .dir-block', els => els.length);
      console.log('日志目录复制:', dirBefore, '->', dirAfter, dirAfter === dirBefore + 1 ? 'PASS' : 'FAIL');
    }

    // 编码切换
    const encSel = page.locator('.sys-block:first-child .srv-block:first-child .dir-block:first-child select');
    if (await encSel.count() > 0) {
      await encSel.selectOption('gbk');
      await page.waitForTimeout(200);
      const encVal = await encSel.inputValue();
      console.log('编码gbk:', encVal === 'gbk' ? 'PASS' : 'FAIL');
    }

    // 打开器
    const addOpenerBtn = page.locator('button', { hasText: '添加打开器' });
    if (await addOpenerBtn.count() > 0) {
      await addOpenerBtn.first().click();
      await page.waitForTimeout(300);
      const nameInp = page.locator('.opener-row input', { hasPlaceholder: 'Notepad' });
      const pathInp = page.locator('.opener-row input', { hasPlaceholder: 'notepad' });
      // 用placeholder属性选择
      const nameInput = page.locator(".opener-row input[placeholder*='Notepad']").last();
      const pathInput = page.locator(".opener-row input[placeholder*='notepad']").last();
      if (await nameInput.count() > 0) {
        await nameInput.fill('TestApp');
        await pathInput.fill('/usr/bin/testapp');
        await page.waitForTimeout(200);
        const saveOpenerBtn = page.locator('.opener-row button', { hasText: '保存' });
        await saveOpenerBtn.last().click();
        await page.waitForTimeout(1500);
        const toast = await page.textContent('#toast');
        console.log('打开器保存:', toast.includes('保存') ? 'PASS' : 'FAIL', '(' + toast.substring(0, 30) + ')');
      }
    }

    // 放弃改动
    const resetBtn = page.locator('button', { hasText: '放弃改动' });
    await resetBtn.first().click();
    await page.waitForTimeout(1500);
    const saveDisabled2 = await page.$eval('#cfg-save-btn', b => b.disabled);
    console.log('放弃后disabled:', saveDisabled2 ? 'PASS' : 'FAIL');

    // 页面切换
    await page.goto('http://127.0.0.1:18092/#/home', { waitUntil: 'networkidle' });
    await page.waitForTimeout(500);
    await page.goto('http://127.0.0.1:18092/#/config', { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    const cards = await page.$$('.card');
    console.log('切换后卡片:', cards.length >= 4 ? 'PASS' : 'FAIL', '(' + cards.length + '个)');
  }

  console.log('\n页面运行时错误数:', errors.length, errors.length === 0 ? 'PASS' : 'FAIL');
  if (errors.length > 0) console.log('错误:', errors.slice(0, 3).join(' | '));

  await browser.close();
})();
