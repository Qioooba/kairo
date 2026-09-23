'use strict';
const fs = require('fs');
const path = require('path');

const { compareLabPaths } = require('./utils/compare-lab');

// QA-02: 夹具根目录不再写死 Windows 盘符路径, 与 11-compare-deep.js 共用同一解析入口。
const { left: LEFT, right: RIGHT } = compareLabPaths();

function writeFile(file, content, mtime) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, content);
  if (mtime) fs.utimesSync(file, mtime, mtime);
}

function resetDir(dir) {
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
}

function pad(n, w) {
  return String(n).padStart(w, '0');
}

function main() {
  resetDir(LEFT);
  resetDir(RIGHT);
  const now = new Date();
  const older = new Date(now.getTime() - 3600 * 1000);
  const newer = new Date(now.getTime() + 60 * 1000);

  for (let i = 1; i <= 1500; i++) {
    const rel = path.join('相同', 'same_' + pad(i, 4) + '.txt');
    const body = 'identical line ' + i + '\n中文相同内容\n';
    writeFile(path.join(LEFT, rel), body, now);
    writeFile(path.join(RIGHT, rel), body, now);
  }
  for (let i = 1; i <= 300; i++) {
    writeFile(path.join(LEFT, '仅左', 'left_' + pad(i, 3) + '.txt'), 'left only ' + i + '\n', now);
  }
  for (let i = 1; i <= 300; i++) {
    writeFile(path.join(RIGHT, '仅右', 'right_' + pad(i, 3) + '.txt'), 'right only ' + i + '\n', now);
  }
  for (let i = 1; i <= 80; i++) {
    const rel = 'newer_left_' + pad(i, 2) + '.txt';
    writeFile(path.join(LEFT, rel), 'left newer body ' + i + '\n', newer);
    writeFile(path.join(RIGHT, rel), 'left newer body ' + i + '\n', older);
  }
  for (let i = 1; i <= 80; i++) {
    const rel = 'newer_right_' + pad(i, 2) + '.txt';
    writeFile(path.join(LEFT, rel), 'right newer body ' + i + '\n', older);
    writeFile(path.join(RIGHT, rel), 'right newer body ' + i + '\n', newer);
  }
  for (let i = 1; i <= 40; i++) {
    const rel = path.join('内容差', 'diff_' + pad(i, 2) + '.txt');
    const shared = 'shared payload ' + i + '\n';
    writeFile(path.join(LEFT, rel), 'L' + shared, now);
    writeFile(path.join(RIGHT, rel), 'R' + shared, now);
  }
  const chunk = Buffer.alloc(256 * 1024, 0x41);
  for (let i = 1; i <= 15; i++) {
    const rel = path.join('大文件', 'large_' + pad(i, 2) + '.bin');
    const leftBuf = Buffer.from(chunk);
    const rightBuf = Buffer.from(chunk);
    if (i <= 8) rightBuf[0] = 0x42;
    else rightBuf[rightBuf.length - 1] = 0x43;
    writeFile(path.join(LEFT, rel), leftBuf, now);
    writeFile(path.join(RIGHT, rel), rightBuf, now);
  }
  writeFile(path.join(LEFT, '中文 目录', '空格 文件.txt'), 'unicode path fixture\n中文内容\n', now);
  writeFile(path.join(RIGHT, '中文 目录', '空格 文件.txt'), 'unicode path fixture\n中文内容\n', now);
  writeFile(path.join(LEFT, 'ignore.tmp'), 'left tmp\n', now);
  writeFile(path.join(RIGHT, 'ignore.tmp'), 'right tmp different\n', now);

  const count = (dir) => {
    let n = 0;
    const walk = (d) => {
      for (const name of fs.readdirSync(d)) {
        const p = path.join(d, name);
        if (fs.statSync(p).isDirectory()) walk(p);
        else n++;
      }
    };
    walk(dir);
    return n;
  };
  console.log(JSON.stringify({ left: LEFT, right: RIGHT, leftFiles: count(LEFT), rightFiles: count(RIGHT) }, null, 2));
}

main();
