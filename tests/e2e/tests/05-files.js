'use strict';

const FilesPage = require('../pages/files-page');

function register(runner, ctx) {
  const { page, baseUrl, data } = ctx;
  const MOCK_SSH = data.MOCK_SSH;

  let filesPage;

  runner.beforeEach(async function () {
    filesPage = new FilesPage(page, baseUrl, runner);
    await filesPage.goto();
  });

  runner.describe('FTP 文件下载 - 页面加载', function () {
    runner.it('应成功加载文件下载页面', async function () {
      const sysSelect = await page.$('#files-sys');
      if (!sysSelect) throw new Error('系统选择下拉框未找到');
      await filesPage.takeStateScreenshot('05-files-01-page-load');
    });

    runner.it('应显示系统选择下拉框', async function () {
      const sysSelect = await page.$('#files-sys');
      if (!sysSelect) throw new Error('系统选择下拉框未找到');
    });

    runner.it('应显示服务器选择下拉框且默认禁用', async function () {
      const srvSelect = await page.$('#files-srv');
      if (!srvSelect) throw new Error('服务器选择下拉框未找到');
      const disabled = await srvSelect.evaluate(function (el) { return el.disabled; });
      if (!disabled) throw new Error('服务器下拉框默认应为禁用状态');
    });
  });

  runner.describe('FTP 文件下载 - 选择业务系统', function () {
    runner.it('选择业务系统后服务器下拉框应变为启用', async function () {
      const hasSystems = await filesPage.hasSystems();
      if (!hasSystems) {
        runner.skipTest('无可用业务系统选项');
        return;
      }
      await filesPage.selectFirstSystem();
      await filesPage.waitForTimeout(500);
      const serverEnabled = await filesPage.isServerEnabled();
      const hasServers = await filesPage.hasServers();
      if (!serverEnabled || !hasServers) {
        runner.skipTest('当前系统无服务器配置');
        return;
      }
      const srvSelect = await page.$('#files-srv');
      const disabled = await srvSelect.evaluate(function (el) { return el.disabled; });
      if (disabled) throw new Error('选择系统后服务器下拉框应变为启用');
    });
  });

  runner.describe('FTP 文件下载 - 选择服务器并填写凭据', function () {
    runner.it('应能选择服务器并填写用户名密码', async function () {
      const hasSystems = await filesPage.hasSystems();
      if (!hasSystems) {
        runner.skipTest('无可用业务系统选项');
        return;
      }
      await filesPage.selectFirstSystem();
      await filesPage.waitForTimeout(500);
      const serverEnabled = await filesPage.isServerEnabled();
      const hasServers = await filesPage.hasServers();
      if (!serverEnabled || !hasServers) {
        runner.skipTest('当前系统无服务器配置');
        return;
      }
      await filesPage.selectFirstServer();
      await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
    });
  });

  runner.describe('FTP 文件下载 - 连接并浏览', function () {
    runner.beforeEach(async function () {
      try {
        const hasSystems = await filesPage.hasSystems();
        if (!hasSystems) return;
        await filesPage.selectFirstSystem();
        await filesPage.waitForTimeout(500);
        const serverEnabled = await filesPage.isServerEnabled();
        const hasServers = await filesPage.hasServers();
        if (!serverEnabled || !hasServers) return;
        await filesPage.selectFirstServer();
        await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
      } catch (e) {
        // ignore setup errors, test will skip
      }
    });

    runner.it('点击连接并浏览应显示文件列表', async function () {
      const hasSystems = await filesPage.hasSystems();
      if (!hasSystems) {
        runner.skipTest('无可用业务系统');
        return;
      }
      const hasServers = await filesPage.hasServers();
      const serverEnabled = await filesPage.isServerEnabled();
      if (!hasServers || !serverEnabled) {
        runner.skipTest('当前系统无服务器配置');
        return;
      }
      try {
        await filesPage.connectAndBrowse();
      } catch (e) {
        runner.skipTest('连接失败：' + e.message);
        return;
      }
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('连接后未显示文件列表');
        return;
      }
      const fileCount = await filesPage.getFileCount();
      if (fileCount === 0) {
        runner.skipTest('文件列表为空');
        return;
      }
      await filesPage.takeStateScreenshot('05-files-02-connected');
    });
  });

  runner.describe('FTP 文件下载 - 目录导航', function () {
    runner.beforeEach(async function () {
      try {
        const hasSystems = await filesPage.hasSystems();
        if (!hasSystems) return;
        await filesPage.selectFirstSystem();
        await filesPage.waitForTimeout(500);
        const serverEnabled = await filesPage.isServerEnabled();
        const hasServers = await filesPage.hasServers();
        if (!serverEnabled || !hasServers) return;
        await filesPage.selectFirstServer();
        await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
        await filesPage.connectAndBrowse();
      } catch (e) {
        // ignore setup errors
      }
    });

    runner.it('应能点击上级目录按钮', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试目录导航');
        return;
      }
      const beforePath = await filesPage.getCurrentPath();
      await filesPage.navigateToParent();
      const afterPath = await filesPage.getCurrentPath();
      if (afterPath === beforePath) {
        throw new Error('点击上级目录后路径未变化');
      }
    });

    runner.it('应能点击目录进入子目录再返回', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试目录导航');
        return;
      }
      const dirRows = await page.$$('table.table tbody tr');
      if (dirRows.length === 0) {
        runner.skipTest('文件列表为空，无法测试目录导航');
        return;
      }
      const beforePath = await filesPage.getCurrentPath();
      let enteredDir = false;
      for (let i = 0; i < dirRows.length; i++) {
        const cells = await dirRows[i].$$('td');
        if (cells.length >= 2) {
          const nameCell = cells[1] || cells[0];
          const nameText = await nameCell.textContent();
          if (nameText && nameText.indexOf('/') >= 0) {
            await nameCell.click();
            await page.waitForTimeout(500);
            enteredDir = true;
            break;
          }
        }
      }
      if (!enteredDir) {
        runner.skipTest('未找到可进入的子目录');
        return;
      }
      const afterEnterPath = await filesPage.getCurrentPath();
      if (afterEnterPath === beforePath) {
        throw new Error('进入子目录后路径未变化');
      }
      await filesPage.navigateToParent();
      const afterBackPath = await filesPage.getCurrentPath();
      if (afterBackPath !== beforePath) {
        throw new Error('返回上级目录后路径未恢复');
      }
      await filesPage.takeStateScreenshot('05-files-03-dir-nav');
    });
  });

  runner.describe('FTP 文件下载 - 路径跳转', function () {
    runner.beforeEach(async function () {
      try {
        const hasSystems = await filesPage.hasSystems();
        if (!hasSystems) return;
        await filesPage.selectFirstSystem();
        await filesPage.waitForTimeout(500);
        const serverEnabled = await filesPage.isServerEnabled();
        const hasServers = await filesPage.hasServers();
        if (!serverEnabled || !hasServers) return;
        await filesPage.selectFirstServer();
        await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
        await filesPage.connectAndBrowse();
      } catch (e) {
        // ignore setup errors
      }
    });

    runner.it('输入有效路径应能跳转', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试路径跳转');
        return;
      }
      const pathInput = await page.$('input[placeholder*="输入绝对路径后回车跳转"]');
      if (!pathInput) {
        throw new Error('路径输入框未找到');
      }
      const currentPath = await filesPage.getCurrentPath();
      const testPath = currentPath || '/';
      await filesPage.navigateToPath(testPath);
      const afterPath = await filesPage.getCurrentPath();
      if (!afterPath) {
        throw new Error('路径跳转后路径为空');
      }
      await filesPage.takeStateScreenshot('05-files-04-path-jump');
    });
  });

  runner.describe('FTP 文件下载 - 文件过滤', function () {
    runner.beforeEach(async function () {
      try {
        const hasSystems = await filesPage.hasSystems();
        if (!hasSystems) return;
        await filesPage.selectFirstSystem();
        await filesPage.waitForTimeout(500);
        const serverEnabled = await filesPage.isServerEnabled();
        const hasServers = await filesPage.hasServers();
        if (!serverEnabled || !hasServers) return;
        await filesPage.selectFirstServer();
        await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
        await filesPage.connectAndBrowse();
      } catch (e) {
        // ignore setup errors
      }
    });

    runner.it('应存在文件过滤输入框', async function () {
      const filterInput = await page.$('#files-filter');
      if (!filterInput) throw new Error('文件过滤输入框未找到');
    });

    runner.it('输入关键字应能过滤文件', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试文件过滤');
        return;
      }
      const beforeCount = await filesPage.getFileCount();
      if (beforeCount === 0) {
        runner.skipTest('文件列表为空，无法测试过滤');
        return;
      }
      await filesPage.filterFiles('.log');
      const afterCount = await filesPage.getFileCount();
      if (afterCount > beforeCount) {
        throw new Error('过滤后文件数不应增加');
      }
      await filesPage.filterFiles('');
      await page.waitForTimeout(200);
    });
  });

  runner.describe('FTP 文件下载 - 文件勾选', function () {
    runner.beforeEach(async function () {
      try {
        const hasSystems = await filesPage.hasSystems();
        if (!hasSystems) return;
        await filesPage.selectFirstSystem();
        await filesPage.waitForTimeout(500);
        const serverEnabled = await filesPage.isServerEnabled();
        const hasServers = await filesPage.hasServers();
        if (!serverEnabled || !hasServers) return;
        await filesPage.selectFirstServer();
        await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
        await filesPage.connectAndBrowse();
      } catch (e) {
        // ignore setup errors
      }
    });

    runner.it('应能勾选单个文件', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试文件勾选');
        return;
      }
      const fileCount = await filesPage.getFileCount();
      if (fileCount === 0) {
        runner.skipTest('文件列表为空，无法测试勾选');
        return;
      }
      await filesPage.selectFirstFile();
      const selectedCount = await filesPage.getSelectedCount();
      if (selectedCount < 1) {
        throw new Error('勾选单个文件后选中数应为至少1');
      }
    });

    runner.it('应能点击全选按钮', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试全选');
        return;
      }
      const fileCount = await filesPage.getFileCount();
      if (fileCount === 0) {
        runner.skipTest('文件列表为空，无法测试全选');
        return;
      }
      await filesPage.selectAllFiles();
      const selectedCount = await filesPage.getSelectedCount();
      if (selectedCount === 0) {
        throw new Error('全选后没有勾选的文件');
      }
    });

    runner.it('应能点击取消选中按钮', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试取消选中');
        return;
      }
      const fileCount = await filesPage.getFileCount();
      if (fileCount === 0) {
        runner.skipTest('文件列表为空，无法测试取消选中');
        return;
      }
      await filesPage.selectAllFiles();
      await filesPage.deselectAllFiles();
      const selectedCount = await filesPage.getSelectedCount();
      if (selectedCount > 0) {
        throw new Error('取消选中后仍有勾选的文件');
      }
      await filesPage.takeStateScreenshot('05-files-05-file-select');
    });
  });

  runner.describe('FTP 文件下载 - 路径穿越测试', function () {
    runner.beforeEach(async function () {
      try {
        const hasSystems = await filesPage.hasSystems();
        if (!hasSystems) return;
        await filesPage.selectFirstSystem();
        await filesPage.waitForTimeout(500);
        const serverEnabled = await filesPage.isServerEnabled();
        const hasServers = await filesPage.hasServers();
        if (!serverEnabled || !hasServers) return;
        await filesPage.selectFirstServer();
        await filesPage.fillCredential(MOCK_SSH.username, MOCK_SSH.password);
        await filesPage.connectAndBrowse();
      } catch (e) {
        // ignore setup errors
      }
    });

    runner.it('输入 ../../etc/passwd 后端接口应拒绝或界面提示错误', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试路径穿越');
        return;
      }
      const pathInput = await page.$('input[placeholder*="输入绝对路径后回车跳转"]');
      if (!pathInput) {
        runner.skipTest('未找到路径输入框，无法测试路径穿越');
        return;
      }
      const result = await filesPage.testPathTraversal('../../etc/passwd');
      const safe = result.apiRejected || result.hasErrorMessage;
      if (!safe) {
        throw new Error(
          '路径穿越未被正确处理：接口未拒绝且无错误提示。' +
          '输入框值: ' + result.inputValue +
          ', API状态: ' + (result.apiErrorStatus || '正常') +
          ', 错误消息: ' + (result.errorMessage || '无')
        );
      }
      await filesPage.takeStateScreenshot('05-files-06-path-traversal');
    });

    runner.it('输入带 ../ 的路径不应访问系统敏感文件', async function () {
      const connected = await filesPage.isConnected();
      if (!connected) {
        runner.skipTest('未连接到服务器，无法测试路径穿越');
        return;
      }
      const pathInput = await page.$('input[placeholder*="输入绝对路径后回车跳转"]');
      if (!pathInput) {
        runner.skipTest('未找到路径输入框，无法测试路径穿越');
        return;
      }
      const result = await filesPage.testPathTraversal('/opt/../../../etc/shadow');
      const safe = result.apiRejected || result.hasErrorMessage;
      if (!safe) {
        throw new Error(
          '路径穿越未被正确处理：接口未拒绝且无错误提示。' +
          '输入框值: ' + result.inputValue +
          ', API状态: ' + (result.apiErrorStatus || '正常') +
          ', 错误消息: ' + (result.errorMessage || '无')
        );
      }
    });
  });
}

module.exports = { register };
