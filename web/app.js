/* ===== web/app.js — 入口 =====
 *
 * 拆分后的启动器。所有 page 模块已自己注册到 window.Kairo.state.routes
 * 和 window.Kairo.state.routeNames。本文件只剩：
 *   1. listenInfo（启动信息）从 /api/config 拿
 *   2. navigate 路由切换
 *   3. hashchange + load 监听
 *
 * 加载顺序（index.html）：
 *   core.js → api.js → state.js → pages/*.js → app.js
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { $ = () => null, $$ = () => [] } = Kairo.core || {};
  const { api } = Kairo.api || {};
  const state = Kairo.state = Kairo.state || {};

  function routeFromHash(hash) {
    return (hash || '#/home').replace(/^#\//, '').split(/[?#]/)[0] || 'home';
  }

  function navigate() {
    const routes = state.routes || {};
    const names = state.routeNames || {};
    const requested = routeFromHash(location.hash);
    const name = routes[requested] ? requested : 'home';

    // 【v0.5 修复 #20】离开系统配置页时如果有未保存改动，弹 confirm 确认。
    // 走 confirm() 而不是阻止默认行为：浏览器原生 hashchange 没有"取消"语义，
    // confirm 取消后 hash 已被改了，但 view.innerHTML='' 还没执行就 return，
    // 用户感知到"没切走"。回到正确路由靠下面 hash 修正。
    if (state.unsavedConfig && state.currentRoute === 'config' && name !== 'config') {
      if (!window.confirm('系统配置有未保存的改动，确定要离开吗？\n（点"取消"留在配置页）')) {
        // 用户取消：把 hash 改回 config，让链接保持一致
        if (history && history.replaceState) {
          history.replaceState(null, '', '#/config');
        } else {
          location.hash = '#/config';
        }
        return;
      }
    }
    // 离开页面：清理进行中的下载（关闭 SSE + 通知后端取消）
    if (Kairo.core && Kairo.core.getActiveDL && Kairo.core.getActiveDL()) {
      const dl = Kairo.core.getActiveDL();
      try { dl.evtsrc && dl.evtsrc.close(); } catch (e) { /* ignore */ }
      if (dl.id) {
        api('POST', '/api/files/download/' + dl.id + '/cancel', {}).catch(() => {});
      }
      Kairo.core.clearActiveDL();
    }
    // 离开页面：清理进行中的 tail（关闭 SSE + 通知后端停止）
    if (Kairo.core && Kairo.core.getActiveTail && Kairo.core.getActiveTail()) {
      const tail = Kairo.core.getActiveTail();
      try { tail.evtsrc && tail.evtsrc.close(); } catch (e) { /* ignore */ }
      if (tail.id) {
        api('POST', '/api/logs/tail/' + tail.id + '/stop', {}).catch(() => {});
      }
      Kairo.core.clearActiveTail();
    }
    // 离开页面：清理 SSH 终端 WS 连接（v0.10）
    if (Kairo.core && Kairo.core.clearActiveShells) {
      Kairo.core.clearActiveShells();
    }
    const view = $('#view');
    if (!view) return;
    view.innerHTML = '';
    // v0.5 P2-14：配置页有 fixed 底部保存栏，给 view 留 padding-bottom 防遮挡
    view.classList.toggle('has-sticky-footer', name === 'config');
    try {
      routes[name](view);
    } catch (e) {
      view.appendChild(Kairo.core.el('div', { class: 'card' }, [
        Kairo.core.el('h3', { text: '页面渲染失败' }),
        Kairo.core.el('div', { class: 'text-err', text: e.message })
      ]));
    }
    const crumbs = $('#crumbs');
    if (crumbs) crumbs.textContent = names[name] || '未知';
    Array.from(document.querySelectorAll('.nav-item')).forEach(a => {
      a.classList.toggle('active', a.getAttribute('data-route') === name);
    });
    if (name === 'downloads' && Kairo.core.clearDlBadge) {
      Kairo.core.clearDlBadge();
    }
    state.currentRoute = name;
  }

  window.addEventListener('hashchange', navigate);
  window.addEventListener('beforeunload', () => {
    // 页面卸载时清理进行中的下载
    if (Kairo.core && Kairo.core.getActiveDL && Kairo.core.getActiveDL()) {
      const dl = Kairo.core.getActiveDL();
      try { dl.evtsrc && dl.evtsrc.close(); } catch (e) { /* ignore */ }
      if (dl.id && navigator.sendBeacon) {
        try { navigator.sendBeacon('/api/files/download/' + dl.id + '/cancel', ''); } catch (e) { /* ignore */ }
      }
      Kairo.core.clearActiveDL();
    }
    // 页面卸载时清理进行中的 tail
    if (Kairo.core && Kairo.core.getActiveTail && Kairo.core.getActiveTail()) {
      const tail = Kairo.core.getActiveTail();
      try { tail.evtsrc && tail.evtsrc.close(); } catch (e) { /* ignore */ }
      if (tail.id && navigator.sendBeacon) {
        try { navigator.sendBeacon('/api/logs/tail/' + tail.id + '/stop', ''); } catch (e) { /* ignore */ }
      }
      Kairo.core.clearActiveTail();
    }
    // 页面卸载时清理 SSH 终端 WS 连接
    if (Kairo.core && Kairo.core.clearActiveShells) {
      Kairo.core.clearActiveShells();
    }
  });
  window.addEventListener('load', async () => {
    try {
      const info = await api('GET', '/api/config');
      state.bootInfo = info;
      const appName = (info.app && info.app.name) || 'Kairo';
      const appSubtitle = (info.app && info.app.subtitle) || '天命契机';
      const version = info.version || 'v0.13';
      const dlFolder = info.paths && info.paths.download_dir;
      const listenInfo = document.getElementById('listen-info');
      if (listenInfo) {
        listenInfo.textContent = '已启动 · ' + appName + ' ' + version + (dlFolder ? ' · 保存到 ' + dlFolder : '');
      }
      // 更新底部版本号显示（与后端版本保持一致）
      const footerVersion = document.getElementById('footer-version');
      if (footerVersion) {
        footerVersion.textContent = version + ' · ' + appName + ' · ' + appSubtitle;
      }
    } catch (e) { /* 忽略 */ }
    // 启动时拉一次 preferences：把用户上次保存的 tail 高亮规则放到 Kairo.state.tailHighlights，
    // 让独立 tail.html / websphere tail tab 都直接用同一份（"页面上设置过的不要再让用户重设"）。
    // GET 失败（文件不存在 / 服务端 500）静默忽略 —— 没有高亮也能正常工作。
    try {
      const prefs = await api('GET', '/api/preferences');
      if (prefs && Array.isArray(prefs.tail && prefs.tail.highlights)) {
        state.tailHighlights = prefs.tail.highlights;
      }
    } catch (e) { /* ignore */ }
    state.tailHighlights = state.tailHighlights || [];
    navigate();
  });
})();
