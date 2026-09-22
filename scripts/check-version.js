'use strict';

// VERSION 是产品版本的唯一手改入口。
// 运行时：Go go:embed VERSION → /api/config；前端只读 API。
// 派生文件：README 徽章、package.json / package-lock.json（由 sync-version.js 写入）。
const fs = require('fs');
const path = require('path');

const root = path.resolve(__dirname, '..');
const read = (file) => fs.readFileSync(path.join(root, file), 'utf8');
const version = read('VERSION').trim();

if (!/^v\d+\.\d+(?:\.\d+)?$/.test(version)) {
  throw new Error(`VERSION 格式非法: ${JSON.stringify(version)}，期望 vMAJOR.MINOR[.PATCH]`);
}

const semver = version.slice(1).split('.');
while (semver.length < 3) semver.push('0');
const npmVersion = semver.join('.');
const failures = [];

const readme = read('README.md');
if (!new RegExp(`Status-${version.replace(/\./g, '\\.')}-success`).test(readme)) {
  failures.push(`README 状态徽章未与 VERSION 对齐，请先跑 node scripts/sync-version.js`);
}

for (const file of ['package.json', 'package-lock.json']) {
  const parsed = JSON.parse(read(file));
  if (parsed.version !== npmVersion) {
    failures.push(`${file} version=${parsed.version}，期望 ${npmVersion}（请先跑 node scripts/sync-version.js）`);
  }
}
const lockRoot = JSON.parse(read('package-lock.json')).packages[''];
if (!lockRoot || lockRoot.version !== npmVersion) {
  failures.push(`package-lock.json 根包版本未与 VERSION 对齐，期望 ${npmVersion}`);
}

const goSrc = read('internal/httpserver/httpserver.go');
if (!/Version\s*=\s*"dev"/.test(goSrc)) {
  failures.push('后端 Version 默认值必须是哨兵 "dev"，真实版本由 VERSION embed / ldflags 注入');
}

// 构建与打包脚本不得按 commit 信息自动拼版本号：VERSION 是唯一真相源，
// dev / 预发版本必须显式传参（./scripts/build_windows_amd64.sh v0.19-dev-xxx）。
for (const scriptPath of ['scripts/build_windows_amd64.sh', 'scripts/package_windows.sh']) {
  const scriptSrc = read(scriptPath);
  if (/git\s+(log|rev-parse)/.test(scriptSrc)) {
    failures.push(`${scriptPath} 不得按 commit 信息拼版本号，请只读 VERSION 或显式参数`);
  }
}

const aboutSrc = read('web/pages/about.js');
if (/const VERSION\s*=\s*'v[\d.]+'/.test(aboutSrc)) {
  failures.push('web/pages/about.js 不得再硬编码产品 VERSION 常量，请从 /api/config 读取');
}

const appSrc = read('web/app.js');
if (/info\.version\s*\|\|\s*'v[\d.]+'/.test(appSrc)) {
  failures.push('web/app.js 不得再硬编码 info.version fallback 产品版本');
}

const indexSrc = read('web/index.html');
if (/id="footer-version"[^>]*>v\d+/.test(indexSrc)) {
  failures.push('web/index.html #footer-version 不得硬编码产品版本，启动后由 /api/config 回填');
}

if (failures.length) {
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log(`版本一致性检查通过: ${version}（npm ${npmVersion}；运行时来自 VERSION embed）`);
