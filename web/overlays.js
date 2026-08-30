/* ===== web/overlays.js — application overlay manager ===== */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { el } = Kairo.core;
  const stack = [];

  function modal(opts) {
    opts = opts || {};
    const overlay = el('div', { class: 'modal-overlay kairo-managed-overlay' });
    const card = el('div', { class: 'modal-card', role: 'dialog', 'aria-modal': 'true' });
    if (opts.width) card.style.width = opts.width + 'px';
    const titleEl = opts.title ? el('div', {
      class: 'modal-title', text: opts.title, id: 'modal-title-' + Date.now()
    }) : null;
    if (titleEl) {
      card.appendChild(titleEl);
      card.setAttribute('aria-labelledby', titleEl.id);
    }
    if (opts.body) card.appendChild(opts.body);
    if (opts.footer) card.appendChild(opts.footer);
    overlay.appendChild(card);

    const previousFocus = document.activeElement;
    let closed = false;
    function focusable() {
      return Array.from(card.querySelectorAll('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'));
    }
    function close() {
      if (closed) return;
      closed = true;
      const idx = stack.indexOf(api);
      if (idx >= 0) stack.splice(idx, 1);
      document.removeEventListener('keydown', onKey, true);
      overlay.remove();
      document.body.classList.toggle('has-open-overlay', stack.length > 0);
      if (previousFocus && previousFocus.focus) previousFocus.focus();
    }
    function onKey(ev) {
      if (stack[stack.length - 1] !== api) return;
      if (ev.key === 'Escape') {
        ev.preventDefault();
        close();
        return;
      }
      if (ev.key !== 'Tab') return;
      const nodes = focusable();
      if (!nodes.length) return;
      const first = nodes[0];
      const last = nodes[nodes.length - 1];
      if (ev.shiftKey && document.activeElement === first) {
        ev.preventDefault(); last.focus();
      } else if (!ev.shiftKey && document.activeElement === last) {
        ev.preventDefault(); first.focus();
      }
    }
    const api = { close, el: card, overlay };
    stack.push(api);
    overlay.style.zIndex = 'calc(var(--z-modal) + ' + stack.length + ')';
    overlay.addEventListener('click', function (ev) { if (ev.target === overlay) close(); });
    document.addEventListener('keydown', onKey, true);
    document.body.appendChild(overlay);
    document.body.classList.add('has-open-overlay');
    setTimeout(function () {
      const nodes = focusable();
      if (nodes[0]) nodes[0].focus(); else card.setAttribute('tabindex', '-1');
    }, 0);
    return api;
  }

  Kairo.overlays = { modal, closeTop: function () {
    const top = stack[stack.length - 1];
    if (top) top.close();
  }};
})();
