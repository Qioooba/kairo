/* Page-level notice bus.  System delivery is a backend concern; this keeps
 * the web UI responsive and gives the user a visible failure surface. */
(function () {
  'use strict';
  const K = window.Kairo = window.Kairo || {};
  const W = K.workbench = K.workbench || {};
  const listeners = [];
  function emit(event) { listeners.slice().forEach(function (fn) { try { fn(event); } catch (_) {} }); }
  function on(fn) { if (typeof fn === 'function') listeners.push(fn); return function () { const i = listeners.indexOf(fn); if (i >= 0) listeners.splice(i, 1); }; }
  W.noticeBus = { emit: emit, on: on };
})();
