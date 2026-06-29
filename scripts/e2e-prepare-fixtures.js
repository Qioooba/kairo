#!/usr/bin/env node
/**
 * E2E 测试环境准备脚本
 *
 * 功能：
 * 1. 准备 Fake WebSphere 日志文件
 * 2. 准备测试文件目录（用于 Files 下载测试）
 * 3. 生成测试专用配置
 * 4. 检查 Mock SSH 服务状态
 *
 * 用法：
 *   node scripts/e2e-prepare-fixtures.js [--check-mock] [--with-mock]
 */

const fs = require('fs');
const path = require('path');
const http = require('http');
const { execSync, spawn } = require('child_process');

const PROJECT_ROOT = path.resolve(__dirname, '..');

// 测试专用目录
const E2E_ROOT = path.join(PROJECT_ROOT, 'tmp', 'e2e');
const FAKE_WS_DIR = path.join(E2E_ROOT, 'fake-websphere', 'opt', 'IBM', 'WebSphere', 'AppServer', 'profiles', 'AppSrv01', 'logs', 'server1');
const FAKE_FILES_DIR = path.join(E2E_ROOT, 'files');
const FAKE_DOWNLOADS_DIR = path.join(E2E_ROOT, 'downloads');

const MOCK_SSH = {
  host: '127.0.0.1',
  port: 2225,
  username: 'test',
  password: 'test'
};

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18092';

/**
 * 确保目录存在
 */
function ensureDir(dir) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
    console.log('  创建目录:', dir);
  }
}

/**
 * 生成日期字符串
 */
function dateStr(date) {
  const d = date || new Date();
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/**
 * 生成时间戳字符串
 */
function timestamp(date) {
  const d = date || new Date();
  const pad = (n) => String(n).padStart(2, '0');
  return `${dateStr(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, '0')}`;
}

/**
 * 写入文件
 */
function writeFile(filepath, content, encoding = 'utf8') {
  try {
    fs.writeFileSync(filepath, content, encoding);
    console.log('  创建:', path.relative(E2E_ROOT, filepath) || path.basename(filepath));
    return true;
  } catch (e) {
    console.error('  错误:', filepath, '-', e.message);
    return false;
  }
}

/**
 * 准备 Fake WebSphere 日志文件
 */
function prepareFakeWebSphereLogs() {
  console.log('\n==> 准备 Fake WebSphere 日志文件...');
  ensureDir(FAKE_WS_DIR);

  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);

  // 1. SystemOut.log
  const systemOutContent = generateSystemOutLog(today);
  writeFile(path.join(FAKE_WS_DIR, 'SystemOut.log'), systemOutContent);

  // 2. SystemOut_YYYY-MM-DD.log
  const systemOutDatedContent = generateSystemOutLog(yesterday);
  writeFile(path.join(FAKE_WS_DIR, `SystemOut_${dateStr(yesterday)}.log`), systemOutDatedContent);

  // 3. SystemErr.log
  const systemErrContent = generateSystemErrLog();
  writeFile(path.join(FAKE_WS_DIR, 'SystemErr.log'), systemErrContent);

  // 4. app.log
  const appLogContent = generateAppLog();
  writeFile(path.join(FAKE_WS_DIR, 'app.log'), appLogContent);

  // 5. empty.log
  writeFile(path.join(FAKE_WS_DIR, 'empty.log'), '');

  // 6. large-line.log (超长行测试)
  const longLine = 'A'.repeat(10000);
  writeFile(path.join(FAKE_WS_DIR, 'large-line.log'), generateSystemOutLog(today) + '\n' + longLine + '\n' + generateSystemOutLog(today));

  console.log('  ✅ WebSphere 日志文件准备完成');
}

/**
 * 生成 SystemOut 日志
 */
function generateSystemOutLog(date) {
  const lines = [];
  const baseTime = new Date(date);
  baseTime.setHours(9, 0, 0, 0);

  lines.push(`[${timestamp(baseTime)}] 00000050 SystemOut  INFO    WebSphere Application Server 8.5.5.20 启动完成`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 100))}] 00000050 user.Service  INFO    信贷系统初始化成功`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 500))}] 00000050 user.Service  INFO    用户查询 userid=1001 成功`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 1000))}] 00000050 user.Service  WARN    用户余额不足 userid=1002 amount=9999`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 2000))}] 00000050 user.Service  ERROR   查询失败 userid=1003`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 2100))}] 00000050 user.Service  ERROR   java.lang.NullPointerException`);
  lines.push(`\tat user.Service.query(Service.java:120)`);
  lines.push(`\tat java.lang.Thread.run(Thread.java:750)`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 3000))}] 00000050 user.Service  INFO    信贷审批通过 userid=1004 amount=50000`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 4000))}] 00000050 user.ScheduleJob  INFO    定时任务执行完成`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 5000))}] 00000050 user.ScheduleJob  ERROR   ORA-00060: deadlock detected`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 6000))}] 00000050 user.Service  INFO    中文日志测试：信贷系统正常运转，交易笔数=1234，金额总计=999999.99元`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 7000))}] 00000050 user.Service  DEBUG   credit-schedule tick begin`);

  return lines.join('\n');
}

/**
 * 生成 SystemErr 日志
 */
function generateSystemErrLog() {
  const lines = [];
  const baseTime = new Date();
  baseTime.setHours(9, 0, 0, 0);

  lines.push(`[${timestamp(baseTime)}] 00000001 SystemErr  SEVERE  WebSphere 初始化失败`);
  lines.push(`java.lang.ClassNotFoundException: com.ibm.ws.Transaction.TransactionManager`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 1000))}] 00000002 SystemErr  SEVERE  数据库连接池耗尽`);
  lines.push(`com.mysql.jdbc.exceptions.MySQLNonTransientConnectionException: Too many connections`);

  return lines.join('\n');
}

/**
 * 生成应用日志
 */
function generateAppLog() {
  const lines = [];
  const baseTime = new Date();
  baseTime.setHours(9, 0, 0, 0);

  lines.push(`[${timestamp(baseTime)}] APP INFO  应用启动成功`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 1000))}] APP INFO  配置加载完成`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 2000))}] APP INFO  HTTP 服务启动完成 port=8080`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 3000))}] APP WARN  内存使用率 85%`);
  lines.push(`[${timestamp(new Date(baseTime.getTime() + 4000))}] APP ERROR 连接超时 host=db.example.com`);

  return lines.join('\n');
}

/**
 * 准备测试文件目录
 */
function prepareFakeFilesDir() {
  console.log('\n==> 准备测试文件目录...');
  ensureDir(FAKE_FILES_DIR);
  ensureDir(path.join(FAKE_FILES_DIR, 'subdir'));
  ensureDir(path.join(FAKE_FILES_DIR, 'subdir', 'nested'));
  ensureDir(path.join(FAKE_FILES_DIR, '中文目录'));

  // 1. 小文本文件
  writeFile(path.join(FAKE_FILES_DIR, 'hello.txt'), 'Hello World!\n这是一个测试文件。\n包含中文内容。');

  // 2. 配置文件
  writeFile(path.join(FAKE_FILES_DIR, 'config.json'), JSON.stringify({
    name: 'test-config',
    version: '1.0.0',
    settings: { debug: true, port: 8080 }
  }, null, 2));

  // 3. 大文件
  writeFile(path.join(FAKE_FILES_DIR, 'large.txt'), 'X'.repeat(100000));

  // 4. 空文件
  writeFile(path.join(FAKE_FILES_DIR, 'empty.txt'), '');

  // 5. 二进制文件（模拟）
  const binaryContent = Buffer.alloc(1000);
  for (let i = 0; i < 1000; i++) {
    binaryContent[i] = Math.floor(Math.random() * 256);
  }
  fs.writeFileSync(path.join(FAKE_FILES_DIR, 'binary.bin'), binaryContent);

  // 6. 中文文件名
  writeFile(path.join(FAKE_FILES_DIR, '中文文件.txt'), '测试中文文件名功能。');
  writeFile(path.join(FAKE_FILES_DIR, '信贷合同.json'), JSON.stringify({ type: '信贷合同', amount: 100000 }));

  // 7. 子目录文件
  writeFile(path.join(FAKE_FILES_DIR, 'subdir', 'nested', 'deep-file.txt'), '深层目录文件内容');
  writeFile(path.join(FAKE_FILES_DIR, 'subdir', 'readme.md'), '# 子目录\n这是子目录的说明文件。');

  // 8. 特殊字符
  writeFile(path.join(FAKE_FILES_DIR, 'file with spaces.txt'), '文件名包含空格');
  writeFile(path.join(FAKE_FILES_DIR, 'file-with-dashes.txt'), '文件名包含短横线');

  // 9. 下载目录初始化
  ensureDir(FAKE_DOWNLOADS_DIR);
  writeFile(path.join(FAKE_DOWNLOADS_DIR, '.gitkeep'), '');

  console.log('  ✅ 测试文件目录准备完成');
}

/**
 * 生成测试专用配置
 */
function generateTestConfig() {
  console.log('\n==> 生成测试专用配置...');

  // 把 E2E_ROOT 路径转换为适合 YAML 的格式
  const wsRoot = path.join(E2E_ROOT, 'fake-websphere');
  const filesRoot = FAKE_FILES_DIR;
  const downloadsRoot = FAKE_DOWNLOADS_DIR;

  const config = `# E2E 测试专用配置
# 生成时间: ${new Date().toISOString()}

# 服务端口
port: 18092

# 认证（测试用）
auth:
  enabled: true
  token: "e2e-test-token-12345678"

# Mock SSH 服务器
ssh:
  mock_enabled: true
  default_host: "127.0.0.1"
  default_port: 2225
  default_username: "test"
  default_password: "test"

# 业务系统配置
systems:
  - name: "E2E测试系统"
    enabled: true
    servers:
      - name: "mock-websphere"
        type: "websphere"
        host: "127.0.0.1"
        port: 2225
        username: "test"
        password: "test"
        log_paths:
          - "/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1"
      - name: "mock-files"
        type: "ftp"
        host: "127.0.0.1"
        port: 2225
        username: "test"
        password: "test"
        root: "/"

# 文件浏览配置
file_browser:
  enabled: true
  free_mode: true
  free_file_roots:
    - "${filesRoot.replace(/\\/g, '/')}"
  allowed_download_roots:
    - "${downloadsRoot.replace(/\\/g, '/')}"

# WebSphere 日志路径映射（让 mock SSH 能访问）
websphere:
  log_root: "${wsRoot.replace(/\\/g, '/')}"
  path_mapping:
    "/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1":
      "${wsRoot.replace(/\\/g, '/')}/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1"

# 下载配置
downloads:
  dir: "${downloadsRoot.replace(/\\/g, '/')}"
  retention_days: 7

# 日志配置
logging:
  level: "warn"

# 开发模式
dev:
  enabled: true
  mock_ssh_auto_start: false
`;

  const configPath = path.join(E2E_ROOT, 'config.e2e.yaml');
  try {
    fs.writeFileSync(configPath, config, 'utf8');
    console.log('  创建: config.e2e.yaml');
    console.log('  ✅ 测试专用配置已生成');
    return configPath;
  } catch (e) {
    console.error('  错误: 无法写入配置 -', e.message);
    return null;
  }
}

/**
 * 检查 Mock SSH 服务
 */
async function checkMockSSH() {
  console.log('\n==> 检查 Mock SSH 服务...');

  return new Promise((resolve) => {
    const net = require('net');
    const client = new net.Socket();

    client.setTimeout(3000);

    client.on('connect', () => {
      console.log(`  ✅ Mock SSH 服务可连接 (${MOCK_SSH.host}:${MOCK_SSH.port})`);
      client.destroy();
      resolve(true);
    });

    client.on('timeout', () => {
      console.log(`  ⚠️  Mock SSH 服务连接超时`);
      console.log('  提示: 运行以下命令启动:');
      console.log(`    python3 ${path.join(PROJECT_ROOT, 'scripts', 'mock_sshd.py')} --port ${MOCK_SSH.port}`);
      client.destroy();
      resolve(false);
    });

    client.on('error', (err) => {
      console.log(`  ⚠️  Mock SSH 服务未运行 (${MOCK_SSH.host}:${MOCK_SSH.port})`);
      console.log(`  错误: ${err.message}`);
      client.destroy();
      resolve(false);
    });

    client.connect(MOCK_SSH.port, MOCK_SSH.host);
  });
}

/**
 * 检查主服务
 */
async function checkMainService() {
  console.log('\n==> 检查主服务...');

  return new Promise((resolve) => {
    try {
      const url = new URL(BASE_URL);
      const req = http.get({
        hostname: url.hostname,
        port: url.port,
        path: '/',
        timeout: 5000
      }, (res) => {
        console.log(`  ✅ 主服务可访问 (${BASE_URL}) HTTP ${res.statusCode}`);
        resolve(true);
      });

      req.on('error', (err) => {
        console.log(`  ⚠️  主服务不可访问 (${BASE_URL})`);
        console.log(`  错误: ${err.message}`);
        resolve(false);
      });

      req.on('timeout', () => {
        req.destroy();
        console.log(`  ⚠️  主服务连接超时`);
        resolve(false);
      });
    } catch (e) {
      console.log(`  ⚠️  主服务检查失败: ${e.message}`);
      resolve(false);
    }
  });
}

/**
 * 启动 Mock SSH
 */
function startMockSSH() {
  console.log('\n==> 启动 Mock SSH...');

  const mockScript = path.join(PROJECT_ROOT, 'scripts', 'mock_sshd.py');
  if (!fs.existsSync(mockScript)) {
    console.log('  ⚠️  mock_sshd.py 不存在');
    return null;
  }

  // 检查 Fake WebSphere 目录是否存在
  if (!fs.existsSync(path.join(E2E_ROOT, 'fake-websphere'))) {
    console.log('  ⚠️  Fake WebSphere 目录不存在，请先运行 --prepare');
    return null;
  }

  try {
    const proc = spawn('python3', [mockScript, '--port', String(MOCK_SSH.port)], {
      cwd: PROJECT_ROOT,
      detached: true,
      stdio: 'ignore'
    });

    proc.unref();

    // 等待服务启动
    return new Promise((resolve) => {
      setTimeout(() => {
        console.log(`  ✅ Mock SSH 已启动 (PID: ${proc.pid})`);
        resolve(proc);
      }, 1000);
    });
  } catch (e) {
    console.log(`  ⚠️  启动 Mock SSH 失败: ${e.message}`);
    return null;
  }
}

/**
 * 停止 Mock SSH
 */
function stopMockSSH() {
  console.log('\n==> 停止 Mock SSH...');

  return new Promise((resolve) => {
    const net = require('net');
    const client = new net.Socket();

    client.connect(MOCK_SSH.port, MOCK_SSH.host, () => {
      // 找到进程并杀掉
      try {
        execSync(`lsof -ti:${MOCK_SSH.port} | xargs kill 2>/dev/null || true`);
        console.log('  ✅ Mock SSH 已停止');
      } catch (e) {
        console.log('  停止命令执行失败（非致命）');
      }
      client.destroy();
      resolve();
    });

    client.on('error', () => {
      console.log('  Mock SSH 未运行（无需停止）');
      client.destroy();
      resolve();
    });

    client.setTimeout(1000);
  });
}

/**
 * 主函数
 */
async function main() {
  const args = process.argv.slice(2);
  const checkMock = args.includes('--check-mock');
  const checkService = args.includes('--check-service');
  const prepareOnly = args.includes('--prepare');
  const withMock = args.includes('--with-mock');
  const cleanup = args.includes('--cleanup');

  console.log('========================================');
  console.log('  E2E 测试环境准备');
  console.log('========================================');

  // 清理模式
  if (cleanup) {
    console.log('\n[清理模式] 删除 E2E 测试目录...');
    try {
      if (fs.existsSync(E2E_ROOT)) {
        execSync(`rm -rf "${E2E_ROOT}"`);
        console.log('  ✅ 已删除:', E2E_ROOT);
      }
    } catch (e) {
      console.error('  清理失败:', e.message);
    }
    return;
  }

  // 准备 fixture
  prepareFakeWebSphereLogs();
  prepareFakeFilesDir();
  const configPath = generateTestConfig();

  if (configPath) {
    console.log('\n  测试专用配置路径:', configPath);
    console.log('\n  启动 OpsToolbox 时使用:');
    console.log(`    CONFIG_PATH="${configPath}" npm start`);
  }

  // 检查 Mock SSH
  if (checkMock) {
    await checkMockSSH();
  }

  // 检查主服务
  if (checkService) {
    await checkMainService();
  }

  // --with-mock 模式
  if (withMock) {
    console.log('\n========================================');
    console.log('  --with-mock 模式');
    console.log('========================================');

    // 启动 Mock SSH
    await startMockSSH();

    // 等待 Mock SSH 就绪
    await new Promise(r => setTimeout(r, 2000));

    // 再次检查
    const mockReady = await checkMockSSH();
    if (!mockReady) {
      console.log('\n  ⚠️  Mock SSH 启动失败');
    }

    console.log('\n  下一步:');
    console.log(`    CONFIG_PATH="${configPath}" npm start`);
    console.log('\n  或运行:');
    console.log('    bash scripts/e2e.sh --with-mock');
  }

  console.log('\n========================================');
  console.log('  ✅ 环境准备完成');
  console.log('========================================');
}

// 如果是直接运行
if (require.main === module) {
  main().catch(console.error);
}

module.exports = {
  E2E_ROOT,
  FAKE_WS_DIR,
  FAKE_FILES_DIR,
  FAKE_DOWNLOADS_DIR,
  MOCK_SSH,
  prepareFakeWebSphereLogs,
  prepareFakeFilesDir,
  generateTestConfig,
  checkMockSSH,
  checkMainService,
  startMockSSH,
  stopMockSSH
};
