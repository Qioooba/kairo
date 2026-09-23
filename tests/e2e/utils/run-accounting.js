'use strict';

/**
 * tests/e2e/utils/run-accounting.js (QA-02)
 *
 * 把 E2E runner 的"每个模块 discovered/executed/passed/failed/skipped"统计与
 * "什么才算通过"的判定抽成纯函数, 便于离线回归。
 *
 * 背景: 这两段逻辑原本内联在 tests/e2e/index.js 里 —— 而 index.js 一被 require
 * 就会执行 main() 并要求真实服务与浏览器, 所以它们事实上无法被测试。
 * 审查明确要求"每个模块记录 discovered/executed/passed/failed/skipped"以及
 * "选择器没有匹配用例时不能输出伪成功", 因此这里把它们变成可验证的纯函数。
 */

/**
 * buildModuleSummary 按模块归属汇总执行结果。
 *
 * moduleReports 里每项带 suiteIndexStart/suiteIndexEnd, 指向它在 runner.suites 中
 * 注册的顶层 suite 区间 (注册顺序即索引顺序)。suites 是本次 run() 的结果数组,
 * 每项带 index 字段。
 *
 * 注意: 被 --grep 过滤掉的 suite 不会出现在结果里, 因此其区间计数保持 0 ——
 * 这正是"没跑"与"跑了且通过"必须能区分的原因。
 */
function buildModuleSummary(moduleReports, suites) {
  const resultsByIndex = new Map();
  for (const suite of suites || []) {
    if (suite && typeof suite.index === 'number') {
      resultsByIndex.set(suite.index, suite);
    }
  }

  return (moduleReports || []).map(function (mr) {
    const agg = { discovered: 0, executed: 0, passed: 0, failed: 0, skipped: 0 };
    for (let i = mr.suiteIndexStart; i < mr.suiteIndexEnd; i++) {
      const suite = resultsByIndex.get(i);
      if (!suite) continue;
      const s = suite.summary || {};
      agg.discovered += s.total || 0;
      agg.executed += s.executed || 0;
      agg.passed += s.passed || 0;
      agg.failed += s.failed || 0;
      agg.skipped += s.skipped || 0;
    }
    return Object.assign({}, mr, agg);
  });
}

/**
 * evaluateRunVerdict 判定本次运行是否算通过。
 *
 * 返回 { ok, exitCode, reason }。以下情况一律**不算通过**, 避免"伪成功":
 *   - --grep 一个用例都没匹配 (过滤跑空)
 *   - 发现用例数 > 0 但实际执行为 0 (全部被跳过/空跑)
 *   - 有用例失败
 *   - 任何模块注册失败
 */
function evaluateRunVerdict(input) {
  const noMatch = !!(input && input.noMatch);
  const grep = input && input.grep;
  const executed = (input && input.executed) || 0;
  const failed = (input && input.failed) || 0;
  const discovered = (input && input.discovered) || 0;
  const registerErrors = (input && input.registerErrors) || 0;

  if (registerErrors > 0) {
    return {
      ok: false,
      exitCode: 1,
      reason: `有 ${registerErrors} 个测试模块加载或注册失败`,
    };
  }
  if (noMatch) {
    return {
      ok: false,
      exitCode: 1,
      reason: `--grep "${grep}" 没有匹配到任何用例 —— 不得视为通过`,
    };
  }
  if (failed > 0) {
    return { ok: false, exitCode: 1, reason: `有 ${failed} 个测试失败` };
  }
  if (executed === 0) {
    return {
      ok: false,
      exitCode: 1,
      reason: `没有任何用例被实际执行 (发现=${discovered}, 执行=0) —— 不得视为通过`,
    };
  }
  return {
    ok: true,
    exitCode: 0,
    reason: `所有测试通过 (发现 ${discovered}, 执行 ${executed})`,
  };
}

module.exports = { buildModuleSummary, evaluateRunVerdict };
