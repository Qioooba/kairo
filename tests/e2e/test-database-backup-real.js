'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT_DIR = path.resolve(__dirname, '../../test-results/screenshots/database-backup');
fs.mkdirSync(SHOT_DIR, { recursive: true });

async function run() {
  console.log('=== 启动数据库会话备份网络调用真实浏览器测试 ===');
  console.log('目标服务:', BASE);

  let browser;
  try {
    browser = await chromium.launch({ headless: true, channel: 'msedge' });
    console.log('使用系统已安装的 Microsoft Edge 浏览器');
  } catch (_) {
    browser = await chromium.launch({ headless: true });
    console.log('使用 Chromium 浏览器');
  }

  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 }
  });
  const page = await context.newPage();
  page.setDefaultTimeout(40000);

  const backupRequests = [];
  page.on('request', req => {
    if (req.url().indexOf('/api/database/sessions/backup') !== -1) {
      console.log('[Network Request]', req.method(), req.url(), new Date().toLocaleTimeString());
      backupRequests.push({ time: Date.now(), method: req.method() });
    }
  });

  try {
    // 1. 打开数据库工作台
    console.log('1. 正在访问数据库工作台...');
    await page.goto(BASE + '/#/database', { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#db-sql', { timeout: 15000 });
    await page.waitForTimeout(2000);
    await page.screenshot({ path: path.join(SHOT_DIR, '01-initial-workbench.png') });

    const initialReqCount = backupRequests.length;
    console.log(`初始阶段 /api/database/sessions/backup 调用次数: ${initialReqCount}`);

    // 2. 空闲等待 32 秒（跨越一个 30 秒轮询周期），验证空闲状态下绝不再发生重复发包
    console.log('2. 正在等待 32 秒以跨越定时周期，验证无变更状态下是否静默零请求...');
    await page.waitForTimeout(32000);

    const idleReqCount = backupRequests.length - initialReqCount;
    console.log(`空闲 32 秒期间触发的 /api/database/sessions/backup 请求增量: ${idleReqCount}`);
    if (idleReqCount > 0) {
      throw new Error(`优化失败：空闲状态下仍然触发了 ${idleReqCount} 次备份请求！`);
    }
    console.log('✓ 空闲状态零重复请求验证通过！');
    await page.screenshot({ path: path.join(SHOT_DIR, '02-idle-no-request.png') });

    // 3. 在 SQL 编辑器中输入新查询，验证防抖合并远端备份
    console.log('3. 在编辑器中输入测试 SQL...');
    await page.fill('#db-sql', 'SELECT SYSDATE, 100 AS TEST_NUM FROM DUAL');
    console.log('已输入 SQL，等待 4 秒以触发展开的防抖同步...');
    await page.waitForTimeout(4000);

    const afterInputCount = backupRequests.length - initialReqCount;
    console.log(`输入 SQL 后触发的备份请求增量: ${afterInputCount}`);
    if (afterInputCount !== 1) {
      throw new Error(`输入后预期触发 1 次防抖备份，实际增量为: ${afterInputCount}`);
    }
    console.log('✓ 编辑 SQL 触发单次防抖远程同步验证通过！');
    await page.screenshot({ path: path.join(SHOT_DIR, '03-sql-edited-synced.png') });

    // 4. 点击增加新页签
    console.log('4. 点击添加新页签按钮 #db-tab-add...');
    await page.click('#db-tab-add');
    await page.waitForTimeout(2500);

    const afterAddTabCount = backupRequests.length - initialReqCount;
    console.log(`添加页签后触发的备份请求总增量: ${afterAddTabCount}`);
    if (afterAddTabCount < 2) {
      throw new Error(`添加页签后预期触发新备份同步，实际总增量为: ${afterAddTabCount}`);
    }
    console.log('✓ 添加页签同步验证通过！');
    await page.screenshot({ path: path.join(SHOT_DIR, '04-tab-added-synced.png') });

    // 5. 再次空闲等待 32 秒，确认再次无变更时依然不发任何无意义请求
    console.log('5. 再次等待 32 秒，确认新会话状态下定时器再次静默...');
    await page.waitForTimeout(32000);
    const finalIdleReqCount = backupRequests.length - initialReqCount;
    if (finalIdleReqCount !== afterAddTabCount) {
      throw new Error(`第二次空闲等待出现多余请求，总增量由 ${afterAddTabCount} 变更为 ${finalIdleReqCount}`);
    }
    console.log('✓ 第二次空闲定时器静默验证通过！');
    await page.screenshot({ path: path.join(SHOT_DIR, '05-second-idle-verified.png') });

    console.log('\n=============================================');
    console.log('🎉 所有真实浏览器备份频率优化 E2E 测试全部通过！');
    console.log('=============================================\n');
  } finally {
    await browser.close();
  }
}

run().catch(err => {
  console.error('测试失败:', err);
  process.exit(1);
});
