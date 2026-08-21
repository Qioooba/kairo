/* ===== web/pages/pet.js =====
 * 宠物彩蛋（v3）：前端仅保留「解锁 + 排行榜联动」，宠物本体已改为原生桌面宠物
 * （internal/deskpet），网页内不再渲染漂浮宠物与皮肤面板。
 *
 * 入口：
 *   - app.js 启动后调 Kairo.pet.init()：拉一次状态，控制武林页宠物榜单 tab 显隐；
 *   - 首页「关于」卡片 10 秒内点 6 次 → Kairo.pet.unlock() 解锁；
 *   - 武林页 tab 切换时调 Kairo.pet.refreshBoardTab()。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const api = (Kairo.api && Kairo.api.api) || function () {
    return Promise.reject(new Error('Kairo.api.api 未初始化'));
  };

  var cur = null; // 最近一次 /api/pet/state 缓存（供榜单页读名字 / 上次同步时间）

  function toast(msg) {
    if (Kairo.core && Kairo.core.toast) {
      try { Kairo.core.toast(msg); return; } catch (e) { /* ignore */ }
    }
  }

  function isEnabled() {
    return !!(cur && cur.enabled);
  }

  function refreshBoardTab() {
    var tabs = document.querySelectorAll('.pet-board-tab');
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].style.display = isEnabled() ? '' : 'none';
    }
  }

  function init() {
    return api('GET', '/api/pet/state').then(function (resp) {
      cur = resp || null;
      refreshBoardTab();
    }).catch(function () { /* 静默 */ });
  }

  function unlock() {
    if (isEnabled()) {
      toast('宠物已开启，点击托盘「显示宠物」查看桌面宠物');
      return;
    }
    api('POST', '/api/pet/enable', {}).then(function (resp) {
      if (resp && resp.enabled) {
        cur = resp;
        refreshBoardTab();
        toast('已开启宠物，点击托盘「显示宠物」查看桌面宠物');
      } else {
        toast('开启失败：' + ((resp && resp.error) || '响应异常'));
      }
    }).catch(function (e) {
      toast('开启失败：' + ((e && e.message) || '网络错误'));
    });
  }

  Kairo.pet = {
    init: init,
    state: function () { return cur; },
    isEnabled: isEnabled,
    unlock: unlock,
    refreshBoardTab: refreshBoardTab
  };
})();
