/* ===== web/app.js — 入口（Tab 保活重构版） =====
 *
 * 重构说明（单 view 销毁 → 多 Tab 保活）：
 *   - 路由渲染所有权移交给 window.Kairo.tabs（web/tabs.js）：
 *     每个路由最多 1 个 Tab，pane 独立、scope 独立，切换只隐藏/显示。
 *   - 切换 Tab：不销毁、不释放资源（后台 tail/SSH/上传保持运行）、不弹确认。
 *     旧 navigate() 里的“切页即关 SSE/WS/XHR”逻辑已删除，由关闭 Tab 时
 *     按 Tab 维度释放（core.releaseTabResources）。
 *   - 确认守卫从“切换时拦截”改为“关闭时拦截”：通过 tabs.addCloseGuard 注册，
 *     由 TabManager.requestClose / beforeunload 统一执行。DOM 保活后切换
 *     不再丢失草稿/事务，拦截切换只会打断“边查日志边操作数据库”的体验。
 *   - hash 始终镜像活动 Tab（可分享/刷新恢复单页）；Tab 列表存 sessionStorage。
 *
 * 加载顺序（index.html）：
 *   core.js → api.js → state.js → overlays.js → tabs.js → pages/*.js → app.js
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { $ = () => null, $$ = () => [] } = Kairo.core || {};
  const { api } = Kairo.api || {};
  const state = Kairo.state = Kairo.state || {};
  const tabs = function () { return Kairo.tabs; };

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

  // ---------- 关闭守卫（只在关闭 Tab / 关浏览器时执行，不拦截切换） ----------

  function registerCloseGuards() {
    const T = tabs();
    if (!T || typeof T.addCloseGuard !== 'function') return;

    // 系统配置未保存改动
    T.addCloseGuard(function (tab) {
      if (tab.route === 'config' && state.unsavedConfig) {
        return '系统配置有未保存的改动，关闭将丢失修改。确定关闭吗？';
      }
      return null;
    });

    // 数据库工作台：未提交事务 / 脏网格（T046 语义保留到关闭时）
    T.addCloseGuard(function (tab) {
      if (tab.route !== 'database' || !Kairo.database || typeof Kairo.database.hasPendingWork !== 'function') return null;
      const dbWork = Kairo.database.hasPendingWork();
      if (!dbWork || (!dbWork.hasTransaction && !(dbWork.dirtyCount > 0))) return null;
      return (dbWork.hasTransaction
        ? '数据库工作台存在未提交的事务' + (dbWork.dirtyCount ? '及 ' + dbWork.dirtyCount + ' 处未保存的网格修改' : '')
        : '数据库工作台存在 ' + dbWork.dirtyCount + ' 处未保存的网格修改')
        + '。关闭将回滚事务并丢弃修改，确定关闭吗？';
    });

    // 纯前端上传任务（T047：前端直传无后台断点续传，关闭即取消）
    T.addCloseGuard(function (tab) {
      if (Kairo.core && typeof Kairo.core.hasActiveUploadsFor === 'function'
        && Kairo.core.hasActiveUploadsFor(tab.id)) {
        return '该页面有正在进行的上传任务。前端直传不支持后台断点续传，关闭将取消上传。确定关闭吗？';
      }
      // 兼容旧控制器（未按 Tab 注册时退回全局判断，仅当该 Tab 是活动 Tab）
      if (Kairo.core && typeof Kairo.core.hasActiveUploads === 'function'
        && tabs().isActive(tab.id) && Kairo.core.hasActiveUploads()) {
        return '该页面有正在进行的上传任务。前端直传不支持后台断点续传，关闭将取消上传。确定关闭吗？';
      }
      return null;
    });

    // SSH 活动终端会话
    T.addCloseGuard(function (tab) {
      if (tab.route !== 'ssh') return null;
      let active = false;
      if (Kairo.core && typeof Kairo.core.hasActiveShellsFor === 'function') {
        try { active = Kairo.core.hasActiveShellsFor(tab.id); } catch (_) {}
      }
      if (!active && Kairo.core && typeof Kairo.core.hasActiveShells === 'function'
        && tabs().isActive(tab.id)) {
        try { active = Kairo.core.hasActiveShells(); } catch (_) {}
      }
      if (active) {
        return '该页面存在活动的 SSH 终端会话。关闭将断开连接（远端后台进程若未配置 nohup/screen 可能随之终止）。确定关闭吗？';
      }
      return null;
    });

    // 便笺：未保存行内草稿（关闭将丢失页面输入的草稿）
    T.addCloseGuard(function (tab) {
      if (tab.route !== 'notes') return null;
      if (Kairo.notes && Kairo.notes.state && Kairo.notes.state.inlineDrafts) {
        const ids = Object.keys(Kairo.notes.state.inlineDrafts);
        if (ids.length > 0) {
          return '便笺页面有未保存的草稿，关闭将丢失修改。确定关闭吗？';
        }
      }
      return null;
    });

    // 文本/文件比对：未保存的修改
    T.addCloseGuard(function (tab) {
      if (tab.route !== 'compare' || !Kairo.compare || typeof Kairo.compare.hasPendingWork !== 'function') return null;
      if (Kairo.compare.hasPendingWork()) {
        return '比对页面存在未保存的修改，关闭将丢失改动。确定关闭吗？';
      }
      return null;
    });
  }

  // ---------- 导航：hash → 打开或激活 Tab（不销毁其他 Tab） ----------

  function navigate() {
    const T = tabs();
    if (!T) return;
    document.body.classList.remove('sidebar-open');
    const routes = state.routes || {};
    const resolved = routeFromHash(location.hash);
    const name = routes[resolved.name] ? resolved.name : 'home';
    T.openRoute(name, resolved.state || {});
  }

  // ---------- 页面卸载：释放所有 Tab 资源 + 守卫提示 ----------

  function collectUnloadBlockers() {
    const T = tabs();
    const msgs = [];
    if (T && typeof T.closeBlockers === 'function' && typeof T.getOpenIds === 'function') {
      T.getOpenIds().forEach(function (id) {
        try {
          T.closeBlockers(id).forEach(function (b) {
            if (b && b.message && msgs.indexOf(b.message) < 0) msgs.push(b.message);
          });
        } catch (_) {}
      });
    }
    return msgs;
  }

  function releaseAllForUnload() {
    const core = Kairo.core || {};
    try {
      const ids = (tabs() && typeof tabs().getOpenIds === 'function') ? tabs().getOpenIds() : [];
      // 下载：关 SSE + beacon 通知后端 cancel（fire-and-forget）
      ids.forEach(function (id) {
        const dl = core.getActiveDL ? core.getActiveDL(id) : null;
        try { dl && dl.evtsrc && dl.evtsrc.close(); } catch (_) {}
        if (dl && dl.id && navigator.sendBeacon) {
          try { navigator.sendBeacon('/api/files/download/' + dl.id + '/cancel', ''); } catch (_) {}
        }
        const tail = core.getActiveTail ? core.getActiveTail(id) : null;
        try { tail && tail.evtsrc && tail.evtsrc.close(); } catch (_) {}
        if (tail && tail.id && navigator.sendBeacon) {
          try { navigator.sendBeacon('/api/logs/tail/' + tail.id + '/stop', ''); } catch (_) {}
        }
      });
    } catch (_) {}
    try { if (core.releaseAllTabResources) core.releaseAllTabResources(); } catch (_) {}
    try { if (core.cancelAllUploadsBeacon) core.cancelAllUploadsBeacon(); } catch (_) {}
    try { if (Kairo.database && Kairo.database.cancel) Kairo.database.cancel(); } catch (_) {}
  }

  // 关闭守卫在模块加载时即注册（不依赖 load 事件，纯函数注册表）。
  registerCloseGuards();

  window.addEventListener('hashchange', navigate);
  window.addEventListener('beforeunload', function (ev) {
    const msgs = collectUnloadBlockers();
    if (msgs.length) {
      ev.preventDefault();
      ev.returnValue = msgs[0];
      return msgs[0];
    }
  });
  window.addEventListener('pagehide', function (ev) {
    // 若页面进入 bfcache（ev.persisted === true），不销毁页面状态
    if (ev && ev.persisted) return;
    releaseAllForUnload();
  });

  function initSidebarDrawer() {
    const toggle = document.getElementById('sidebar-toggle');
    const backdrop = document.getElementById('sidebar-backdrop');
    const nav = document.getElementById('nav');

    function isDesktop() {
      return window.innerWidth > 768;
    }

    function updateToggleUI(collapsed) {
      if (!toggle) return;
      toggle.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
      toggle.title = collapsed ? '展开侧边栏 (Ctrl+B)' : '收起侧边栏 (Ctrl+B)';
      toggle.setAttribute('aria-label', toggle.title);
    }

    function setDesktopCollapsed(collapsed) {
      if (collapsed) {
        document.documentElement.setAttribute('data-sidebar', 'collapsed');
        try { localStorage.setItem('kairo_sidebar_collapsed', 'true'); } catch (_) {}
      } else {
        document.documentElement.removeAttribute('data-sidebar');
        try { localStorage.setItem('kairo_sidebar_collapsed', 'false'); } catch (_) {}
      }
      updateToggleUI(collapsed);
      setTimeout(function () {
        window.dispatchEvent(new Event('resize'));
      }, 260);
    }

    function toggleSidebar() {
      if (isDesktop()) {
        const isCollapsed = document.documentElement.getAttribute('data-sidebar') === 'collapsed';
        setDesktopCollapsed(!isCollapsed);
      } else {
        document.body.classList.toggle('sidebar-open');
      }
    }

    if (toggle) {
      toggle.addEventListener('click', function (e) {
        e.stopPropagation();
        toggleSidebar();
      });
    }
    if (backdrop) {
      backdrop.addEventListener('click', function () {
        document.body.classList.remove('sidebar-open');
      });
    }
    if (nav) {
      nav.addEventListener('click', function (e) {
        if (e.target.closest('.nav-item') && !isDesktop()) {
          document.body.classList.remove('sidebar-open');
        }
      });
    }

    // 全局快捷键 Ctrl+B / Cmd+B (桌面端)
    window.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && (e.key === 'b' || e.key === 'B')) {
        const target = e.target;
        const isInput = target && (
          target.isContentEditable ||
          (target.tagName === 'INPUT' && !['checkbox', 'radio', 'button', 'submit'].includes(target.type)) ||
          target.tagName === 'TEXTAREA'
        );
        if (!isInput) {
          e.preventDefault();
          toggleSidebar();
        }
      }
    });

    // 初始化 UI 状态
    const initCollapsed = document.documentElement.getAttribute('data-sidebar') === 'collapsed';
    updateToggleUI(initCollapsed);
  }

  window.addEventListener('load', async () => {
    try {
      const info = await api('GET', '/api/config');
      state.bootInfo = info;
      const appName = (info.app && info.app.name) || 'Kairo';
      const appSubtitle = (info.app && info.app.subtitle) || '天命契机';
      const version = info.version || '';
      const dlFolder = info.paths && info.paths.download_dir;
      const listenInfo = document.getElementById('listen-info');
      if (listenInfo) {
        listenInfo.textContent = '已启动 · ' + appName + (version ? ' ' + version : '') + (dlFolder ? ' · 保存到 ' + dlFolder : '');
      }
      // 页脚版本只跟 /api/config，不再硬编码产品版本号。
      const footerVersion = document.getElementById('footer-version');
      if (footerVersion) {
        footerVersion.textContent = [version, appName, appSubtitle].filter(Boolean).join(' · ');
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
    // TabManager 初始化 → 按 hash 打开首个 Tab（关闭守卫已在模块加载时注册）
    try {
      if (Kairo.tabs && Kairo.tabs.init) Kairo.tabs.init({});
    } catch (e) { if (console && console.warn) console.warn('tabs init failed', e); }
    navigate();
    // 宠物彩蛋：静默初始化（未开启 / 失败都不影响主流程）
    if (window.Kairo && Kairo.pet && Kairo.pet.init) {
      try { Kairo.pet.init(); } catch (e) { /* 宠物彩蛋失败静默 */ }
    }
    bindAboutNavClickUnlock();
    initSidebarDrawer();
  });
})();
