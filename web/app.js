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

  // 侧边栏“关于”菜单：10 秒内点击 6 次，触发宠物解锁（配合隐藏彩蛋）
  function bindAboutNavClickUnlock() {
    const navAbout = document.querySelector('.nav-item[data-route="about"]');
    if (!navAbout || navAbout.dataset.petUnlockBound === '1') return;
    navAbout.dataset.petUnlockBound = '1';
    navAbout.addEventListener('click', function () {
      if (Kairo.pet && Kairo.pet.registerUnlockClick) {
        try { Kairo.pet.registerUnlockClick(); } catch (e) { /* ignore */ }
      }
    }, { passive: true });
  }

  function routeFromHash(hash) {
    const raw = (hash || '#/home').replace(/^#\//, '').split(/[?#]/)[0] || 'home';
    if (raw === 'reminders' || raw === 'notes/reminders') return { name: 'notes', state: { tab: 'reminders' } };
    if (raw === 'notes' || raw === 'notes/list') return { name: 'notes', state: { tab: 'list' } };
    return { name: raw, state: {} };
  }

  function navigate() {
    document.body.classList.remove('sidebar-open');
    const routes = state.routes || {};
    const names = state.routeNames || {};
    const resolved = routeFromHash(location.hash);
    const requested = resolved.name;
    const name = routes[requested] ? requested : 'home';

    function abortNavigation(prevRoute, prevState) {
      let query = '';
      if (prevState && typeof URLSearchParams !== 'undefined') {
        try {
          const qs = new URLSearchParams(prevState).toString();
          if (qs) query = '?' + qs;
        } catch (_) {}
      }
      const prevHash = prevRoute ? '#/' + prevRoute + query : '#/';
      if (typeof history !== 'undefined' && history.replaceState) {
        history.replaceState(null, '', prevHash);
      } else if (typeof location !== 'undefined') {
        location.hash = prevHash;
      }
    }

    // UI-02: 操作级导航策略与 canLeave 守卫
    // 1. 路由作用域级守卫 (RouteScope.canLeave)
    if (state.currentScope && typeof state.currentScope.canLeave === 'function') {
      try {
        if (state.currentScope.canLeave(name) === false) {
          abortNavigation(state.currentRoute, state.currentRouteState);
          return;
        }
      } catch (e) {
        console.warn('currentScope.canLeave failed', e);
      }
    }

    // 2. 系统配置未保存改动守卫
    if (state.unsavedConfig && state.currentRoute === 'config' && name !== 'config') {
      if (typeof window !== 'undefined' && window.confirm && !window.confirm('系统配置有未保存的改动，确定要离开吗？\n（点"取消"留在配置页）')) {
        abortNavigation('config', state.currentRouteState);
        return;
      }
    }

    // 3. 数据库工作台未提交事务 / 未保存修改守卫 (T046: 未提交事务/脏编辑；点击其他导航并选择留下 -> 路由不变，草稿与事务不被静默丢弃)
    if (state.currentRoute === 'database' && name !== 'database' && Kairo.database && typeof Kairo.database.hasPendingWork === 'function') {
      const dbWork = Kairo.database.hasPendingWork();
      if (dbWork && (dbWork.hasTransaction || dbWork.dirtyCount > 0)) {
        const desc = dbWork.hasTransaction
          ? '数据库工作台存在未提交的事务' + (dbWork.dirtyCount ? '及 ' + dbWork.dirtyCount + ' 处未保存的网格修改' : '')
          : '数据库工作台存在 ' + dbWork.dirtyCount + ' 处未保存的网格修改';
        if (typeof window !== 'undefined' && window.confirm && !window.confirm(desc + '。\n离开将回滚事务并丢弃修改，确定要离开吗？\n（点"取消"留在当前页，点"确定"回滚并离开）')) {
          abortNavigation('database', state.currentRouteState);
          return;
        }
        // 用户明确确认离开：显式回滚待处理事务，避免隐式提交或连接悬挂
        if (typeof Kairo.database.discardPendingWork === 'function') {
          try { Kairo.database.discardPendingWork(); } catch (e) { console.warn('discardPendingWork failed', e); }
        }
      }
    }

    // 4. 纯前端上传任务确认取消守卫 (T047: 仅前端直传不支持后台断点续传，离开前确认取消，不虚构续传)
    if (Kairo.core && typeof Kairo.core.hasActiveUploads === 'function' && Kairo.core.hasActiveUploads()) {
      if (typeof window !== 'undefined' && window.confirm && !window.confirm('当前有正在进行的上传任务。由于前端直传不支持后台断点续传，离开页面将取消上传。\n确定离开并取消上传吗？\n（点"取消"留在当前页）')) {
        abortNavigation(state.currentRoute, state.currentRouteState);
        return;
      }
    }

    // 5. SSH 活动终端会话确认断开守卫
    if (state.currentRoute === 'ssh' && name !== 'ssh' && Kairo.core && typeof Kairo.core.hasActiveShells === 'function' && Kairo.core.hasActiveShells()) {
      if (typeof window !== 'undefined' && window.confirm && !window.confirm('当前存在活动的 SSH 终端会话。离开页面将断开连接（远端后台进程若未配置 nohup/screen 可能随之终止）。\n确定离开吗？\n（点"取消"留在当前页）')) {
        abortNavigation('ssh', state.currentRouteState);
        return;
      }
    }

    // 6. 执行经确认后的清理动作：
    // (a) 上传：若有上传控制器则取消并清空
    if (Kairo.core && Kairo.core.cancelAllUploads) {
      try { Kairo.core.cancelAllUploads(); } catch (e) { /* ignore */ }
      try { window.__opsActiveUploads = null; } catch (e) { /* ignore */ }
    }
    // (b) SSH 终端：关闭活动连接
    if (Kairo.core && Kairo.core.clearActiveShells) {
      try { Kairo.core.clearActiveShells(); } catch (e) { /* ignore */ }
    }
    // (c) 离开页面：清理进行中的下载
    // UI-02 (T047): 已由后端 dlmanager 持有的下载任务在切页时保留后端执行，卸载前端 SSE 订阅即可，统一在“下载历史”找回；不调用后端 cancel 接口
    if (Kairo.core && Kairo.core.getActiveDL && Kairo.core.getActiveDL()) {
      const dl = Kairo.core.getActiveDL();
      try { dl.evtsrc && dl.evtsrc.close(); } catch (e) { /* ignore */ }
      Kairo.core.clearActiveDL();
    }
    // (d) 离开页面：清理进行中的 tail（关闭 SSE + 通知后端停止）
    if (Kairo.core && Kairo.core.getActiveTail && Kairo.core.getActiveTail()) {
      const tail = Kairo.core.getActiveTail();
      try { tail.evtsrc && tail.evtsrc.close(); } catch (e) { /* ignore */ }
      if (tail.id) {
        api('POST', '/api/logs/tail/' + tail.id + '/stop', {}).catch(() => {});
      }
      Kairo.core.clearActiveTail();
    }
    // (e) 离开数据库工作台时中止 fetch 流，后端 QueryContext 会同步收到取消信号。
    if (Kairo.database && Kairo.database.cancel) {
      try { Kairo.database.cancel(); } catch (e) { /* ignore */ }
    }
    const view = $('#view');
    if (!view) return;
    if (state.currentScope) {
      try { state.currentScope.dispose(); } catch (e) { console.warn('route scope dispose failed', e); }
      state.currentScope = null;
    }
    if (typeof state.currentUnmount === 'function') {
      try { state.currentUnmount(); } catch (e) { console.warn('route unmount failed', e); }
      state.currentUnmount = null;
    }
    // 路由切换时清理上一页的受控弹层，避免旧页面的遮罩/菜单残留到
    // 新页面上方（尤其是提醒编辑框、收藏夹等可跨页面导航的弹层）。
    if (Kairo.overlays && Kairo.overlays.closeTop) {
      for (let i = 0; i < 20 && document.querySelector('.kairo-managed-overlay'); i++) {
        Kairo.overlays.closeTop();
      }
    }
    // 便笺“更多”菜单为避免卡片裁剪会临时挂到 body；离开页面时也要移除，
    // 否则它不会随 #view 清空而消失，可能覆盖下一页内容。
    document.querySelectorAll('.notes-more-menu[data-portaled="1"]').forEach(menu => menu.remove());
    view.innerHTML = '';
    const renderToken = String((state.routeRenderToken || 0) + 1);
    state.routeRenderToken = Number(renderToken);
    view.dataset.renderToken = renderToken;
    const createScope = (Kairo.workbench && Kairo.workbench.createRouteScope) || function (token) {
      let d = false;
      const fns = [];
      return {
        renderToken: token,
        isCurrent: function (t) { return !d && (t === undefined || t === token); },
        add: function (fn) {
          if (typeof fn !== 'function') return function () {};
          if (d) { try { fn(); } catch (_) {} return function () {}; }
          let ran = false;
          const safe = function () { if (ran) return; ran = true; try { fn(); } catch (_) {} };
          fns.push(safe);
          return safe;
        },
        dispose: function () {
          if (d) return;
          d = true;
          while (fns.length) { const f = fns.pop(); try { f(); } catch (_) {} }
        }
      };
    };
    const scope = createScope(Number(renderToken));
    state.currentScope = scope;
    // v0.5 P2-14：配置页有 fixed 底部保存栏，给 view 留 padding-bottom 防遮挡
    view.classList.toggle('has-sticky-footer', name === 'config');
    try {
      const unmount = routes[name](view, resolved.state, scope);
      if (unmount && typeof unmount.then === 'function') {
        unmount.then(function (cleanup) {
          if (typeof cleanup !== 'function') return;
          if (scope.isCurrent() && view.dataset.renderToken === renderToken) {
            state.currentUnmount = scope.add(cleanup);
          } else {
            // UI-01: 页面已切走（stale 分支），立即执行旧路由返回的 cleanup 避免泄漏
            try { cleanup(); } catch (e) { console.warn('stale route cleanup failed', e); }
          }
        }).catch(function (e) {
          if (!scope.isCurrent() || view.dataset.renderToken !== renderToken) return;
          view.appendChild(Kairo.core.el('div', { class: 'card' }, [
            Kairo.core.el('h3', { text: '页面渲染失败' }),
            Kairo.core.el('div', { class: 'text-err', text: e && e.message ? e.message : String(e) })
          ]));
        });
      } else if (typeof unmount === 'function') {
        state.currentUnmount = scope.add(unmount);
      }
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
    state.currentRouteState = resolved.state;
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
    // 页面卸载时清理进行中的上传（v1.2）
    // 用 sendBeacon 异步通知后端 cancel，前端 fire-and-forget。
    if (Kairo.core && Kairo.core.cancelAllUploadsBeacon) {
      try { Kairo.core.cancelAllUploadsBeacon(); } catch (e) { /* ignore */ }
    }
    if (Kairo.database && Kairo.database.cancel) {
      try { Kairo.database.cancel(); } catch (e) { /* ignore */ }
    }
    if (state.currentScope) {
      try { state.currentScope.dispose(); } catch (e) { /* ignore */ }
      state.currentScope = null;
    }
    if (typeof state.currentUnmount === 'function') {
      try { state.currentUnmount(); } catch (e) { /* ignore */ }
      state.currentUnmount = null;
    }
  });
  function initSidebarDrawer() {
    const toggle = document.getElementById('sidebar-toggle');
    const backdrop = document.getElementById('sidebar-backdrop');
    if (toggle) {
      toggle.addEventListener('click', function (e) {
        e.stopPropagation();
        document.body.classList.toggle('sidebar-open');
      });
    }
    if (backdrop) {
      backdrop.addEventListener('click', function () {
        document.body.classList.remove('sidebar-open');
      });
    }
    const nav = document.getElementById('nav');
    if (nav) {
      nav.addEventListener('click', function (e) {
        if (e.target.closest('.nav-item')) {
          document.body.classList.remove('sidebar-open');
        }
      });
    }
  }

  window.addEventListener('load', async () => {
    try {
      const info = await api('GET', '/api/config');
      state.bootInfo = info;
      const appName = (info.app && info.app.name) || 'Kairo';
      const appSubtitle = (info.app && info.app.subtitle) || '天命契机';
      const version = info.version || '';
      const buildTime = info.build_time || '';
      const dlFolder = info.paths && info.paths.download_dir;
      const listenInfo = document.getElementById('listen-info');
      if (listenInfo) {
        listenInfo.textContent = '已启动 · ' + appName + (version ? ' ' + version : '') + (dlFolder ? ' · 保存到 ' + dlFolder : '');
      }
      // 页脚版本只跟 /api/config，不再硬编码产品版本号。
      const footerVersion = document.getElementById('footer-version');
      if (footerVersion) {
        footerVersion.textContent = [version, appName, appSubtitle].filter(Boolean).join(' · ');
        // 可观测：悬停显示构建时间，方便确认当前跑的是哪个包（版本跳变时一眼定位）。
        if (buildTime) footerVersion.title = '构建时间 ' + buildTime;
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
    if (Kairo.notes && Kairo.notes.init) Kairo.notes.init();
    navigate();
    // 宠物彩蛋：静默初始化（未开启 / 失败都不影响主流程）
    if (window.Kairo && Kairo.pet && Kairo.pet.init) {
      try { Kairo.pet.init(); } catch (e) { /* 宠物彩蛋失败静默 */ }
    }
    bindAboutNavClickUnlock();
    initSidebarDrawer();
  });
})();
