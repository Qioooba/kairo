'use strict';

// 从 VERSION 派生 README 徽章和 npm 版本。运行时版本不走这里：
// Go 用 go:embed VERSION，前端只读 /api/config。
const fs = require('fs');
const path = require('path');

const root = path.resolve(__dirname, '..');
const read = (file) => fs.readFileSync(path.join(root, file), 'utf8');
const write = (file, text) => fs.writeFileSync(path.join(root, file), text);

const version = read('VERSION').trim();
if (!/^v\d+\.\d+(?:\.\d+)?$/.test(version)) {
  throw new Error(`VERSION 格式非法: ${JSON.stringify(version)}，期望 vMAJOR.MINOR[.PATCH]`);
}

const semver = version.slice(1).split('.');
while (semver.length < 3) semver.push('0');
const npmVersion = semver.join('.');

const readmePath = 'README.md';
const readme = read(readmePath);
const nextReadme = readme.replace(
  /Status-v\d+\.\d+(?:\.\d+)?-success/,
  `Status-${version}-success`,
);
if (nextReadme === readme && !new RegExp(`Status-${version.replace(/\./g, '\\.')}-success`).test(readme)) {
  throw new Error('README.md 未找到 Status 徽章，无法同步版本');
}
if (nextReadme !== readme) write(readmePath, nextReadme);

const pkgPath = 'package.json';
const pkg = JSON.parse(read(pkgPath));
if (pkg.version !== npmVersion) {
  pkg.version = npmVersion;
  write(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
}

const lockPath = 'package-lock.json';
const lock = JSON.parse(read(lockPath));
let lockDirty = false;
if (lock.version !== npmVersion) {
  lock.version = npmVersion;
  lockDirty = true;
}
if (lock.packages && lock.packages[''] && lock.packages[''].version !== npmVersion) {
  lock.packages[''].version = npmVersion;
  lockDirty = true;
}
if (lockDirty) write(lockPath, JSON.stringify(lock, null, 2) + '\n');

console.log(`已从 VERSION 同步派生文件: ${version}（npm ${npmVersion}）`);
