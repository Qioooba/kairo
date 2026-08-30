'use strict';

// VERSION 是产品版本的权威来源。发布前检查所有用户可见入口和开发元数据，
// 防止 About、页脚、后端 API、README 与构建产物各自显示不同版本。
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

const contracts = [
  ['internal/httpserver/httpserver.go', new RegExp(`Version\\s*=\\s*"${version.replace(/\./g, '\\.')}"`), '后端 Version'],
  ['web/pages/about.js', new RegExp(`const VERSION = '${version.replace(/\./g, '\\.')}'`), 'About 版本'],
  ['web/index.html', new RegExp(`id="footer-version"[^>]*>${version.replace(/\./g, '\\.')}`), '页脚版本'],
  ['web/app.js', new RegExp(`info\\.version \\|\\| '${version.replace(/\./g, '\\.')}'`), '前端回退版本'],
  ['README.md', new RegExp(`Status-${version.replace(/\./g, '\\.')}-success`), 'README 状态徽章'],
];

const failures = [];
for (const [file, pattern, label] of contracts) {
  if (!pattern.test(read(file))) failures.push(`${label}未与 VERSION 对齐: ${file}`);
}

for (const file of ['package.json', 'package-lock.json']) {
  const parsed = JSON.parse(read(file));
  if (parsed.version !== npmVersion) failures.push(`${file} version=${parsed.version}，期望 ${npmVersion}`);
}
const lockRoot = JSON.parse(read('package-lock.json')).packages[''];
if (!lockRoot || lockRoot.version !== npmVersion) {
  failures.push(`package-lock.json 根包版本未与 VERSION 对齐，期望 ${npmVersion}`);
}

if (failures.length) {
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log(`版本一致性检查通过: ${version}（npm ${npmVersion}）`);
