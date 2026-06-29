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