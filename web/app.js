/* ===== web/app.js — 入口 =====
 *
 * 拆分后的启动器。所有 page 模块已自己注册到 window.OTB.state.routes
 * 和 window.OTB.state.routeNames。本文件只剩：
 *   1. listenInfo（启动信息）从 /api/config 拿
 *   2. navigate 路由切换
 *   3. hashchange + load 监听
 *
 * 加载顺序（index.html）：
 *   core.js → api.js → state.js → pages/*.js → app.js
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  const { $ = () => null, $$ = () => [] } = OTB.core || {};
  const { api } = OTB.api || {};
  const state = OTB.state = OTB.state || {};

  function navigate() {
    const hash = (location.hash || '#/home').replace(/^#\//, '');
    const routes = state.routes || {};
    const names = state.routeNames || {};
    const name = routes[hash] ? hash : 'home';
    // 离开页面：清理进行中的下载（关闭 SSE + 通知后端取消）
    if (OTB.core && OTB.core.getActiveDL && OTB.core.getActiveDL()) {
      const dl = OTB.core.getActiveDL();
      try { dl.evtsrc && dl.evtsrc.close(); } catch (e) { /* ignore */ }
      if (dl.id) {
        api('POST', '/api/files/download/' + dl.id + '/cancel', {}).catch(() => {});
      }
      OTB.core.clearActiveDL();
    }
    const view = $('#view');
    if (!view) return;
    view.innerHTML = '';
    try {
      routes[name](view);
    } catch (e) {
      view.appendChild(OTB.core.el('div', { class: 'card' }, [
        OTB.core.el('h3', { text: '页面渲染失败' }),
        OTB.core.el('div', { class: 'text-err', text: e.message })
      ]));
    }
    const crumbs = $('#crumbs');
    if (crumbs) crumbs.textContent = names[name] || '未知';
    Array.from(document.querySelectorAll('.nav-item')).forEach(a => {
      a.classList.toggle('active', a.getAttribute('data-route') === name);
    });
  }

  window.addEventListener('hashchange', navigate);
  window.addEventListener('load', async () => {
    try {
      const info = await api('GET', '/api/config');
      state.bootInfo = info;
      const appName = (info.app && info.app.name) || 'OpsToolbox';
      const dlFolder = info.paths && info.paths.download_dir;
      const listenInfo = document.getElementById('listen-info');
      if (listenInfo) {
        listenInfo.textContent = '已启动 · ' + appName + (dlFolder ? ' · 保存到 ' + dlFolder : '');
      }
    } catch (e) { /* 忽略 */ }
    navigate();
  });
})();