'use strict';

/**
 * tests/e2e/utils/compare-lab.js (QA-02)
 *
 * 比较工作台夹具根目录的唯一解析入口。
 *
 * 旧实现把默认值写死成 Windows 盘符路径 `D:\kairo-test-runtime`,
 * 并且 make-compare-lab.js 与 11-compare-deep.js 各写一份:
 *   - 别的 checkout / Linux / macOS 上默认路径根本不存在
 *   - 两份默认值一旦改动不一致, 夹具生成端与消费端就会指向不同目录
 *
 * 现在默认落在 os.tmpdir() 下, 三个平台都能跑; 需要固定位置时用
 * COMPARE_LAB_ROOT 显式覆盖。
 */

const os = require('os');
const path = require('path');

const LAB_DIR_NAME = 'kairo-test-runtime';

/** resolveCompareLabRoot 返回夹具根目录 (env 优先, 否则系统临时目录)。 */
function resolveCompareLabRoot() {
  const override = process.env.COMPARE_LAB_ROOT;
  if (override && String(override).trim() !== '') {
    return String(override).trim();
  }
  return path.join(os.tmpdir(), LAB_DIR_NAME);
}

/** compareLabPaths 返回根目录与左右夹具目录 (生成端与消费端共用)。 */
function compareLabPaths() {
  const root = resolveCompareLabRoot();
  return {
    root: root,
    left: path.join(root, 'compare-左 源'),
    right: path.join(root, 'compare-右 源'),
  };
}

module.exports = { resolveCompareLabRoot, compareLabPaths, LAB_DIR_NAME };
