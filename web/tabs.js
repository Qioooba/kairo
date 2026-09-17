/* ===== web/tabs.js — TabManager（一等公民） =====
 *
 * 重构目标：把“单 view 切页即销毁”改为“多 Tab 保活”。
 *   - 每个路由最多 1 个 Tab（单实例保活）：点击左侧菜单 = 打开或激活，
 *     切回时 DOM / 输入 / 滚动 / SSE / WS 全部保留。
 *   - 首页只是普通落地页：有其他 Tab 时可关闭；从首页打开别的页面时自动收掉首页；
 *     仅剩一个 Tab 时拒绝关闭（保证总有活动 Tab）。
 *   - 切换 Tab 不做任何销毁、不弹任何确认（后台保持运行）；
 *     关闭 Tab / 关浏览器时才走守卫 + 资源释放。
 *
 * 职责边界：
 *   - 本模块拥有：tabs Map、pane DOM、scope、unmount、滚动位、Tab 条渲染、
 *     hash 同步、sessionStorage 持久化、closeGuards 注册表。
 *   - 资源释放委托：Kairo.core.releaseTabResources(tabId) 关 SSE/XHR/WS；
 *     Kairo.overlays.closeTabOverlays(tabId) 关该页弹层。
 *   - 页面生命周期契约：renderX(pane, routeState, scope)，返回 cleanup
 *     或 Promise<cleanup>（沿用 app.js 旧约定）；另有三个 window CustomEvent
 *     （detail = { id, route }），页内按需监听：
 *       'kairo:tab-show'  pane 从隐藏切回显示（如 ssh xterm refit）
 *       'kairo:tab-hide'  pane 被隐藏
 *       'kairo:tab-close' Tab 即将被销毁（pane/scope 仍有效，可做最后收尾；
 *                         如 database 刷会话备份、清模块级全局定时器）
 *
 * 加载顺序（index.html）：core → … → state → overlays → tabs → app。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const state = Kairo.state = Kairo.state || {};
  const LS_KEY = 'kairo:tabs:v1';

  const tabs = new Map(); // id(route) → tab record
  let order = [];         // Tab 条顺序（route id 数组）
  let activeId = null;
  let viewRoot = null;
  let barEl = null;
  const closeGuards = []; // fn(tab) → null(放行) | string(需 confirm 的文案) | false(静默阻止)

  function getRoutes() { return state.routes || {}; }
  function getNames() { return state.routeNames || {}; }

  function hashFor(route, routeState) {
    if (route === 'notes') {
      return '#/notes/' + ((routeState && routeState.tab === 'reminders') ? 'reminders' : 'list');
    }
    return '#/' + route;
  }

  // ---------- 持久化（只存 id 顺序 + active + notes 子状态，pane 懒渲染） ----------

  function persist() {
    try {
      const states = {};
      tabs.forEach((t, id) => { states[id] = t.routeState || {}; });
      sessionStorage.setItem(LS_KEY, JSON.stringify({ open: order.slice(), active: activeId, states }));
    } catch (_) { /* quota / privacy 忽略 */ }
  }

  function restore() {
    try {
      const raw = sessionStorage.getItem(LS_KEY);
      if (!raw) return null;
      const data = JSON.parse(raw);
      if (!data || !Array.isArray(data.open)) return null;
      return data;
    } catch (_) { return null; }
  }

  // ---------- scope 工厂（复用 workbench，降级内联） ----------

  function createScope(token) {
    if (Kairo.workbench && typeof Kairo.workbench.createRouteScope === 'function') {
      return Kairo.workbench.createRouteScope(token);
    }
    let disposed = false;
    const fns = [];
    return {
      renderToken: token,
      isCurrent: function (t) { return !disposed && (t === undefined || t === token); },
      add: function (fn) {
        if (typeof fn !== 'function') return function () {};
        if (disposed) { try { fn(); } catch (_) {} return function () {}; }
        let ran = false;
        const safe = function () { if (ran) return; ran = true; try { fn(); } catch (_) {} };
        fns.push(safe);
        return safe;
      },
      dispose: function () {
        if (disposed) return;
        disposed = true;
        while (fns.length) { const f = fns.pop(); try { f(); } catch (_) {} }
      }
    };
  }

  // ---------- Tab 条渲染 ----------

  function renderBar() {
    if (!barEl) return;
    barEl.innerHTML = '';
    const names = getNames();
    order.forEach((id) => {
      const t = tabs.get(id);
      if (!t) return;
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'app-tab' + (id === activeId ? ' active' : '');
      item.setAttribute('data-tab', id);
      item.setAttribute('role', 'tab');
      item.setAttribute('aria-selected', id === activeId ? 'true' : 'false');
      item.title = names[id] || id;
      const label = document.createElement('span');
      label.className = 'app-tab-label';
      label.textContent = names[id] || id;
      item.appendChild(label);
      if (t.badge) {
        const dot = document.createElement('span');
        dot.className = 'app-tab-badge' + (t.badge === 'warn' ? ' warn' : '');
        item.appendChild(dot);
      }
      // 仅剩一个 Tab 时不给 ×（保证总有活动 Tab）；有其他 Tab 时首页也可关
      if (order.length > 1) {
        const x = document.createElement('span');
        x.className = 'app-tab-close';
        x.textContent = '×';
        x.title = '关闭';
        x.setAttribute('role', 'button');
        x.setAttribute('aria-label', '关闭' + (names[id] || id));
        x.addEventListener('click', function (ev) {
          ev.stopPropagation();
          requestClose(id);
        });
        item.appendChild(x);
      }
      item.addEventListener('click', function () { activate(id); });
      // 中键关闭（桌面端习惯；最后一个 Tab 由 requestClose 兜底拒绝）
      item.addEventListener('auxclick', function (ev) {
        if (ev.button === 1) { ev.preventDefault(); requestClose(id); }
      });
      barEl.appendChild(item);
    });
    // 保证活动 Tab 可见
    try {
      const cur = barEl.querySelector('.app-tab.active');
      if (cur && cur.scrollIntoView) cur.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    } catch (_) {}
  }

  function dispatchTabEvent(name, tab) {
    try {
      window.dispatchEvent(new CustomEvent(name, { detail: { id: tab.id, route: tab.route } }));
    } catch (_) { /* ignore */ }
  }

  let renderingTabId = null;

  // ---------- 渲染单个 Tab pane ----------

  function renderTabPane(tab) {
    const routes = getRoutes();
    const render = routes[tab.route];
    const pane = document.createElement('section');
    pane.className = 'view tab-pane';
    pane.setAttribute('data-tab', tab.id);
    if (tab.route === 'config') pane.classList.add('has-sticky-footer');
    tab.pane = pane;
    tab.renderToken = (state.routeRenderToken || 0) + 1;
    state.routeRenderToken = tab.renderToken;
    pane.dataset.renderToken = String(tab.renderToken);
    viewRoot.appendChild(pane);

    const scope = createScope(tab.renderToken);
    scope.tabId = tab.id;
    scope.owner = tab.id;
    tab.scope = scope;
    tab.unmount = null;

    function storeCleanup(cleanup) {
      if (typeof cleanup !== 'function') return;
      // pane 仍在且 scope 有效 → 纳入 scope；否则（stale）立即执行防泄漏
      if (scope.isCurrent() && pane.isConnected) {
        tab.unmount = scope.add(cleanup);
      } else {
        try { cleanup(); } catch (e) { if (console && console.warn) console.warn('stale tab cleanup failed', e); }
      }
    }

    renderingTabId = tab.id;
    try {
      const ret = render(pane, tab.routeState || {}, scope);
      if (ret && typeof ret.then === 'function') {
        ret.then(function (cleanup) { storeCleanup(cleanup); }).catch(function (e) {
          if (!scope.isCurrent() || !pane.isConnected) return;
          showRenderError(pane, e);
        });
      } else {
        storeCleanup(ret);
      }
    } catch (e) {
      showRenderError(pane, e);
    } finally {
      renderingTabId = null;
    }
  }

  function showRenderError(pane, e) {
    try {
      pane.innerHTML = '';
      const core = Kairo.core || {};
      const card = core.el ? core.el('div', { class: 'card' }, [
        core.el('h3', { text: '页面渲染失败' }),
        core.el('div', { class: 'text-err', text: e && e.message ? e.message : String(e) })
      ]) : null;
      if (card) pane.appendChild(card);
      else pane.textContent = '页面渲染失败：' + (e && e.message ? e.message : String(e));
    } catch (_) {}
  }

  // ---------- 打开 / 激活 ----------

  function ensureTab(route, routeState) {
    let tab = tabs.get(route);
    if (!tab) {
      tab = {
        id: route, route: route,
        routeState: routeState || {},
        pane: null, scope: null, unmount: null,
        scrollTop: 0, badge: null, renderToken: 0
      };
      tabs.set(route, tab);
      order.push(route);
    } else if (routeState && JSON.stringify(routeState) !== JSON.stringify(tab.routeState || {})) {
      // 同路由新子状态（如 notes list↔reminders）：notes 页内自己会切 tab，
      // 这里只需更新记录 + hash；pane 不重建（保活）。
      tab.routeState = routeState;
      if (tab.pane) {
        try {
          tab.pane.dispatchEvent(new CustomEvent('kairo:route-state', { detail: routeState }));
        } catch (_) {}
      }
    }
    return tab;
  }

  function setRouteState(tabId, routeState) {
    const tab = tabs.get(tabId);
    if (!tab) return;
    tab.routeState = routeState || {};
    if (tab.pane) {
      try {
        tab.pane.dispatchEvent(new CustomEvent('kairo:route-state', { detail: routeState }));
      } catch (_) {}
    }
    if (tab.id === activeId) {
      syncChrome(tab);
    }
  }

  function openRoute(route, routeState, opts) {
    opts = opts || {};
    // 极端时序兜底（hashchange 早于 init）：懒解析容器，仍拿不到则跳过本次导航
    if (!viewRoot) {
      try { viewRoot = document.getElementById('view'); } catch (_) {}
      if (!viewRoot) return null;
    }
    if (!barEl) {
      try { barEl = document.getElementById('tab-bar'); } catch (_) {}
    }
    const routes = getRoutes();
    if (!routes[route]) route = 'home';
    const leavingHome = (activeId === 'home' && route !== 'home');
    const tab = ensureTab(route, routeState);
    if (!tab.pane) {
      renderTabPane(tab);
    } else if (routeState) {
      try {
        tab.pane.dispatchEvent(new CustomEvent('kairo:route-state', { detail: routeState }));
      } catch (_) {}
    }
    // notes 子状态变化时通知页内（页内 switchTab 用 replaceState 改 hash，
    // 外部 hash 进来时页内需跟进；页内监听该事件即可，v1 先保证 hash/记录一致）
    activate(tab.id, opts);
    // 首页只做落地页：从首页点开别的页面时自动收掉首页（后台的 home 不动，
    // 用户从别的页主动点回首页则保留多 Tab；home 无关闭守卫，直接销毁即可）
    if (leavingHome && tabs.has('home')) {
      destroyTab('home');
      renderBar();
      persist();
    }
    return tab.id;
  }

  function syncChrome(tab) {
    // 面包屑 + 左侧高亮 + hash + 兼容镜像（state.current* 供旧代码读取）
    try {
      const names = getNames();
      const crumbs = document.getElementById('crumbs');
      if (crumbs) crumbs.textContent = names[tab.route] || '未知';
      Array.from(document.querySelectorAll('.nav-item')).forEach(function (a) {
        a.classList.toggle('active', a.getAttribute('data-route') === tab.route);
      });
      if (tab.route === 'downloads' && Kairo.core && Kairo.core.clearDlBadge) {
        Kairo.core.clearDlBadge();
      }
    } catch (_) {}
    state.currentRoute = tab.route;
    state.currentRouteState = tab.routeState || {};
    state.currentScope = tab.scope || null;
    state.currentUnmount = tab.unmount || null;
    // hash 始终镜像活动 Tab（replaceState 不触发 hashchange，无循环）
    const h = hashFor(tab.route, tab.routeState);
    if (location.hash !== h) {
      try { history.replaceState(null, '', h); } catch (_) { location.hash = h; }
    }
    document.body.classList.remove('sidebar-open');
  }

  function activate(id, opts) {
    opts = opts || {};
    const tab = tabs.get(id);
    if (!tab) return;
    if (!tab.pane) renderTabPane(tab);
    if (id === activeId) { syncChrome(tab); renderBar(); persist(); return; }
    const prev = activeId ? tabs.get(activeId) : null;
    if (prev && prev.pane) {
      try { prev.scrollTop = prev.pane.scrollTop || 0; } catch (_) {}
      prev.pane.style.display = 'none';
      dispatchTabEvent('kairo:tab-hide', prev);
    }
    activeId = id;
    // 瞬态 portaled 菜单（便笺“更多”）挂在 body 上，不随 pane 隐藏而消失 → 切 Tab 时移除
    try {
      document.querySelectorAll('.notes-more-menu[data-portaled="1"]').forEach(function (m) { m.remove(); });
    } catch (_) {}
    tab.pane.style.display = '';
    try { tab.pane.scrollTop = tab.scrollTop || 0; } catch (_) {}
    if (Kairo.overlays && typeof Kairo.overlays.setActiveTab === 'function') {
      try { Kairo.overlays.setActiveTab(id); } catch (_) {}
    }
    dispatchTabEvent('kairo:tab-show', tab);
    syncChrome(tab);
    renderBar();
    persist();
  }

  // ---------- 关闭 ----------

  function addCloseGuard(fn) {
    if (typeof fn === 'function') closeGuards.push(fn);
  }

  // 收集某 Tab 的阻止关闭原因（不弹框，纯查询，供 badge/beforeunload 用）
  function closeBlockers(id) {
    const tab = tabs.get(id);
    if (!tab) return [];
    const out = [];
    // scope 级守卫：canLeave 返回 false 即静默阻止
    if (tab.scope && typeof tab.scope.canLeave === 'function') {
      try {
        if (tab.scope.canLeave(null) === false) out.push({ silent: true, message: '当前页面阻止关闭' });
      } catch (e) { if (console && console.warn) console.warn('scope.canLeave failed', e); }
    }
    closeGuards.forEach(function (g) {
      try {
        const r = g(tab);
        if (r === false) out.push({ silent: true, message: '当前页面有未保存的工作' });
        else if (typeof r === 'string' && r) out.push({ silent: false, message: r });
      } catch (e) { if (console && console.warn) console.warn('close guard failed', e); }
    });
    return out;
  }

  function destroyTab(id) {
    const tab = tabs.get(id);
    if (!tab) return;
    // 0. 先通知页内做关闭前收尾（如 database 刷一次会话备份、清全局定时器）。
    //    此时 pane/scope 仍有效，handler 还能读写 DOM。
    dispatchTabEvent('kairo:tab-close', tab);
    // 1. 关该页弹层
    if (Kairo.overlays && typeof Kairo.overlays.closeTabOverlays === 'function') {
      try { Kairo.overlays.closeTabOverlays(id); } catch (_) {}
    }
    // 2. 释放该 Tab 的资源（SSE/XHR/WS），不碰其他 Tab
    if (Kairo.core && typeof Kairo.core.releaseTabResources === 'function') {
      try { Kairo.core.releaseTabResources(id); } catch (_) {}
    }
    if (id === 'notes' && Kairo.notes && typeof Kairo.notes.clearInlineDrafts === 'function') {
      try { Kairo.notes.clearInlineDrafts(); } catch (_) {}
    }
    // 3. scope + unmount（unmount 经 scope.add 包过 once，dispose 后再调不会重复执行）
    const doomedScope = tab.scope;
    const doomedUnmount = tab.unmount;
    tab.scope = null;
    tab.unmount = null;
    if (doomedScope) { try { doomedScope.dispose(); } catch (_) {} }
    if (typeof doomedUnmount === 'function') { try { doomedUnmount(); } catch (_) {} }
    // 4. 移除 DOM + 记录 + 清兼容镜像（先快照再置空，避免“先置空后比较”恒为 false）
    try { if (tab.pane && tab.pane.parentNode) tab.pane.parentNode.removeChild(tab.pane); } catch (_) {}
    tab.pane = null;
    tabs.delete(id);
    order = order.filter(function (x) { return x !== id; });
    if (state.currentScope === doomedScope) state.currentScope = null;
    if (state.currentUnmount === doomedUnmount) state.currentUnmount = null;
  }

  function requestClose(id) {
    const tab = tabs.get(id);
    if (!tab) return;
    // 仅剩一个 Tab 时拒绝关闭（保证总有活动 Tab；× 此时也不渲染，此处防中键/程序化调用）
    if (order.length <= 1) return;
    const blockers = closeBlockers(id);
    const confirms = blockers.filter(function (b) { return !b.silent; });
    const silents = blockers.filter(function (b) { return b.silent; });
    if (silents.length && !confirms.length) {
      if (Kairo.core && Kairo.core.toast) Kairo.core.toast(silents[0].message || '当前页面阻止关闭', 'warn');
      return;
    }
    if (confirms.length) {
      const msg = confirms.map(function (b) { return b.message; }).join('\n');
      if (typeof window !== 'undefined' && window.confirm && !window.confirm(msg + '\n（点"取消"保留该 Tab）')) return;
      // 用户确认关闭：显式回滚 DB 事务类副作用（各 guard 自带 onConfirm 亦可，此处统一处理 database）
      if (Kairo.database && typeof Kairo.database.discardPendingWork === 'function' && id === 'database') {
        try { Kairo.database.discardPendingWork(); } catch (e) { if (console && console.warn) console.warn('discardPendingWork failed', e); }
      }
      if (Kairo.notes && typeof Kairo.notes.clearInlineDrafts === 'function' && id === 'notes') {
        try { Kairo.notes.clearInlineDrafts(); } catch (e) { if (console && console.warn) console.warn('clearInlineDrafts failed', e); }
      }
    }
    const wasActive = (id === activeId);
    // 切到邻居后再销毁，保证总有活动 Tab
    if (wasActive) {
      const idx = order.indexOf(id);
      const next = order[idx + 1] || order[idx - 1] || 'home';
      if (tabs.get(next) || getRoutes()[next]) {
        if (!tabs.get(next) && getRoutes()[next]) ensureTab(next, {});
        activate(next, { viaClose: true });
      }
    }
    destroyTab(id);
    renderBar();
    persist();
  }

  function setBadge(id, kind) {
    const tab = tabs.get(id);
    if (!tab) return;
    tab.badge = kind || null;
    renderBar();
  }

  // ---------- 初始化 ----------

  function init(opts) {
    opts = opts || {};
    viewRoot = opts.viewRoot || document.getElementById('view');
    barEl = opts.barEl || document.getElementById('tab-bar');
    if (viewRoot) viewRoot.classList.add('has-tabs');
    // 恢复上次打开的 Tabs（只恢复记录；pane 懒渲染，首屏只渲染活动 Tab）
    const saved = restore();
    const routes = getRoutes();
    if (saved && saved.open.length) {
      saved.open.forEach(function (id) {
        if (routes[id] && !tabs.has(id)) {
          tabs.set(id, {
            id: id, route: id,
            routeState: (saved.states && saved.states[id]) || {},
            pane: null, scope: null, unmount: null,
            scrollTop: 0, badge: null, renderToken: 0
          });
          order.push(id);
        }
      });
      if (saved.active && tabs.has(saved.active)) activeId = saved.active;
      // 新语义：首页只做落地页，历史会话里残留的后台 home 直接丢弃
      //（pane 懒渲染，此时只有记录无 DOM，直接删记录即可）
      if (activeId && activeId !== 'home' && tabs.has('home')) {
        tabs.delete('home');
        order = order.filter(function (x) { return x !== 'home'; });
      }
    }
    renderBar();
  }

  Kairo.tabs = {
    init, openRoute, activate, requestClose,
    addCloseGuard, closeBlockers,
    getActiveId: function () { return activeId; },
    getRenderingId: function () { return renderingTabId; },
    getActive: function () { return activeId ? tabs.get(activeId) : null; },
    getOpenIds: function () { return order.slice(); },
    has: function (id) { return tabs.has(id); },
    hasTab: function (id) { return tabs.has(id); },
    isActive: function (id) { return activeId === id; },
    setRouteState: setRouteState,
    setBadge, hashFor,
    // 供 beforeunload / 诊断用（只读快照）
    _debug: function () { return { open: order.slice(), active: activeId }; }
  };
})();
