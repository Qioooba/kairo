/* ===== web/state.js =====
 * 全局 state：路由表 / 路由名 / 路由副标题 / 启动信息缓存
 *
 * 不引 ES module，挂 window.DTB.state
 */

(function () {
  'use strict';

  if (!window.DTB) window.DTB = {};
  if (!window.DTB.state) window.DTB.state = {};
  const state = window.DTB.state;

  // 路由表：route name → render 函数引用
  // 页面模块加载时会注册自己：
  //   window.DTB.state.routes.home = function(view) { ... }
  // app.js 在 navigate 时统一调用
  state.routes = state.routes || {};
  state.routeNames = state.routeNames || {};
  state.routeSubs = state.routeSubs || {};

  // 启动信息（/api/config 的 app.* / paths.* 缓存）
  // 让 websphere / files / downloads / diagnostics 等页面共用
  state.bootInfo = state.bootInfo || null;

  // localStorage 提示关闭键的统一前缀
  // v0.9 rebrand: 从 otb:dismissed:* 迁移到 dtb:dismissed:*（一次性，不删除旧键）
  try {
    var OLD_PREFIX = 'otb:dismissed:';
    for (var i = 0; i < localStorage.length; i++) {
      var k = localStorage.key(i);
      if (k && k.indexOf(OLD_PREFIX) === 0) {
        var newK = 'dtb:dismissed:' + k.slice(OLD_PREFIX.length);
        if (!localStorage.getItem(newK)) {
          localStorage.setItem(newK, localStorage.getItem(k));
        }
      }
    }
  } catch (_) { /* ignore migration errors */ }

  // v0.9 rebrand: 全局兜底迁移 —— 启动时扫一遍 localStorage 里所有 otb: 前缀的键，
  // 一次性复制到 dtb: 前缀（新键不存在时才复制，避免覆盖用户在迁移期间写入的新数据）。
  // 这样做的好处：未来再有遗漏的 otb: 键也能自动带过来，不需要每处单独写迁移代码。
  // 老键不删除，留作只读 fallback；如果新代码稳定后想清理，可以再加一个清理函数。
  try {
    if (typeof localStorage !== 'undefined') {
      var GLOBAL_OLD = 'otb:';
      var GLOBAL_NEW = 'dtb:';
      var seen = {};
      for (var gi = 0; gi < localStorage.length; gi++) {
        var gk = localStorage.key(gi);
        if (gk && gk.indexOf(GLOBAL_OLD) === 0) {
          var gn = GLOBAL_NEW + gk.slice(GLOBAL_OLD.length);
          if (!seen[gn] && !localStorage.getItem(gn)) {
            try { localStorage.setItem(gn, localStorage.getItem(gk)); } catch (_) { /* quota? */ }
          }
          seen[gn] = true;
        }
      }
    }
  } catch (_) { /* ignore migration errors */ }

  const LS_PREFIX = 'dtb:dismissed:';

  function isDismissed(key) {
    try { return localStorage.getItem(LS_PREFIX + key) === '1'; }
    catch (e) { return false; }
  }
  function dismiss(key) {
    try { localStorage.setItem(LS_PREFIX + key, '1'); }
    catch (e) { /* ignore */ }
  }
  function clearDismiss(key) {
    try { localStorage.removeItem(LS_PREFIX + key); }
    catch (e) { /* ignore */ }
  }
  state.isDismissed = isDismissed;
  state.dismiss = dismiss;
  state.clearDismiss = clearDismiss;
})();