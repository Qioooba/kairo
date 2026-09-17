/* ===== web/workbench/route-scope.js =====
 *
 * UI-01: 统一路由生命周期管理与过期回调保护。
 * 为每个路由渲染分配独立生命周期作用域：
 *   1. 统一管理定时器、事件监听、AbortController 与清理函数（disposer）；
 *   2. 作用域销毁时（用户切走路由），所有资源自动逆序释放；
 *   3. 保证清理函数（cleanup）只执行一次（幂等）；
 *   4. 作用域销毁后，晚到达的异步回调通过 isCurrent() 快速拒绝，
 *      晚注册的资源立即释放，防止内存泄漏和过期 DOM 篡改。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const workbench = Kairo.workbench = Kairo.workbench || {};

  function once(fn) {
    let called = false;
    return function () {
      if (called) return;
      called = true;
      try {
        return fn.apply(this, arguments);
      } catch (e) {
        if (typeof console !== 'undefined' && console.warn) {
          console.warn('route disposer failed:', e);
        }
      }
    };
  }

  function createRouteScope(renderToken) {
    let disposed = false;
    const disposers = [];

    const scope = {
      renderToken: renderToken,
      isCurrent: function (token) {
        if (disposed) return false;
        if (token !== undefined && token !== null && token !== renderToken) return false;
        return true;
      },
      get disposed() {
        return disposed;
      },
      add: function (disposer) {
        if (typeof disposer !== 'function') return function () {};
        const safe = once(disposer);
        if (disposed) {
          safe();
          return function () {};
        }
        disposers.push(safe);
        return safe;
      },
      timeout: function (fn, ms) {
        if (typeof fn !== 'function') return function () {};
        if (disposed) return function () {};
        let id = setTimeout(function () {
          id = null;
          if (!disposed) fn();
        }, ms);
        return scope.add(function () {
          if (id !== null) {
            clearTimeout(id);
            id = null;
          }
        });
      },
      interval: function (fn, ms) {
        if (typeof fn !== 'function') return function () {};
        if (disposed) return function () {};
        let id = setInterval(function () {
          if (!disposed) fn();
        }, ms);
        return scope.add(function () {
          if (id !== null) {
            clearInterval(id);
            id = null;
          }
        });
      },
      event: function (target, type, listener, options) {
        if (!target || typeof target.addEventListener !== 'function' || typeof listener !== 'function') {
          return function () {};
        }
        if (disposed) return function () {};
        target.addEventListener(type, listener, options);
        return scope.add(function () {
          try {
            target.removeEventListener(type, listener, options);
          } catch (e) { /* ignore */ }
        });
      },
      controller: function () {
        if (typeof AbortController === 'undefined') return null;
        const ctrl = new AbortController();
        if (disposed) {
          try { ctrl.abort(); } catch (e) { /* ignore */ }
          return ctrl;
        }
        scope.add(function () {
          try { ctrl.abort(); } catch (e) { /* ignore */ }
        });
        return ctrl;
      },
      dispose: function () {
        if (disposed) return;
        disposed = true;
        while (disposers.length > 0) {
          const fn = disposers.pop();
          try { fn(); } catch (e) { /* ignore */ }
        }
      },
      setCanLeave: function (fn) {
        scope.canLeave = fn;
      },
      registerShells: function (c) {
        if (typeof window !== 'undefined' && window.Kairo && window.Kairo.core && window.Kairo.core.setActiveShells) {
          const owner = scope.tabId || scope.owner;
          window.Kairo.core.setActiveShells(c, owner);
          return scope.add(function () {
            window.Kairo.core.setActiveShells(null, owner, c);
          });
        }
        return function () {};
      },
      registerUploads: function (c) {
        if (typeof window !== 'undefined' && window.Kairo && window.Kairo.core && window.Kairo.core.setActiveUploads) {
          const owner = scope.tabId || scope.owner;
          window.Kairo.core.setActiveUploads(c, owner);
          return scope.add(function () {
            window.Kairo.core.setActiveUploads(null, owner, c);
          });
        }
        return function () {};
      },
      registerDL: function (dl) {
        if (typeof window !== 'undefined' && window.Kairo && window.Kairo.core && window.Kairo.core.setActiveDL) {
          const owner = scope.tabId || scope.owner;
          window.Kairo.core.setActiveDL(dl, owner);
          return scope.add(function () {
            window.Kairo.core.setActiveDL(null, owner, dl);
          });
        }
        return function () {};
      },
      registerTail: function (tail) {
        if (typeof window !== 'undefined' && window.Kairo && window.Kairo.core && window.Kairo.core.setActiveTail) {
          const owner = scope.tabId || scope.owner;
          window.Kairo.core.setActiveTail(tail, owner);
          return scope.add(function () {
            window.Kairo.core.setActiveTail(null, owner, tail);
          });
        }
        return function () {};
      }
    };

    return scope;
  }

  workbench.createRouteScope = createRouteScope;
  workbench.once = once;
})();
