/* ===== web/overlays.js — application overlay manager ===== */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { el } = Kairo.core;
  const stack = [];

  function closeIcon() {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('width', '16');
    svg.setAttribute('height', '16');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('fill', 'none');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', '2');
    svg.setAttribute('stroke-linecap', 'round');
    svg.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', 'M6 6l12 12M18 6L6 18');
    svg.appendChild(path);
    return svg;
  }

  function modal(opts) {
    opts = opts || {};
    const overlay = el('div', { class: 'modal-overlay kairo-managed-overlay' });
    const card = el('div', { class: 'modal-card', role: 'dialog', 'aria-modal': 'true' });
    if (opts.width) card.style.width = opts.width + 'px';
    const head = el('div', { class: 'modal-head' });
    const titleEl = el('div', {
      class: 'modal-title',
      text: opts.title || '',
      id: 'modal-title-' + Date.now()
    });
    const closeBtn = el('button', {
      class: 'modal-close',
      type: 'button',
      title: '关闭',
      'aria-label': '关闭'
    });
    closeBtn.appendChild(closeIcon());
    head.append(titleEl, closeBtn);
    card.appendChild(head);
    card.setAttribute('aria-labelledby', titleEl.id);
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
      if (typeof opts.onClose === 'function') opts.onClose();
    }
    closeBtn.onclick = function (ev) { ev.preventDefault(); close(); };
    function onKey(ev) {
      if (stack[stack.length - 1] !== api) return;
      if (ev.key === 'Escape') {
        if (ev.target && ev.target.closest && ev.target.closest('[data-capture-keys]')) return;
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
      const nodes = focusable().filter(function (n) { return !n.classList.contains('modal-close'); });
      if (nodes[0]) nodes[0].focus(); else card.setAttribute('tabindex', '-1');
    }, 0);
    return api;
  }

  function prompt(opts) {
    opts = opts || {};
    return new Promise(function (resolve) {
      let resolved = false;
      const input = el('input', {
        type: opts.type || 'text',
        class: 'editor-input',
        style: 'width:100%;margin-top:8px;',
        value: opts.defaultValue || '',
        placeholder: opts.placeholder || '',
        autocomplete: 'off'
      });
      const body = el('div', { class: 'modal-prompt-body', style: 'padding:14px 16px;' });
      if (opts.message || opts.label) {
        body.appendChild(el('div', { class: 'muted', text: opts.message || opts.label }));
      }
      body.appendChild(input);
      const okBtn = el('button', { class: 'btn btn-primary', type: 'button', text: opts.okText || '确定' });
      const cancelBtn = el('button', { class: 'btn', type: 'button', text: opts.cancelText || '取消' });
      const footer = el('div', { class: 'editor-footer', style: 'padding:10px 16px;display:flex;justify-content:flex-end;gap:8px;' }, [cancelBtn, okBtn]);
      const dlg = modal({
        title: opts.title || '请输入',
        width: opts.width || 440,
        body: body,
        footer: footer,
        onClose: function () {
          if (!resolved) {
            resolved = true;
            resolve(null);
          }
        }
      });
      function submit() {
        const val = input.value.trim();
        if (typeof opts.validate === 'function') {
          const err = opts.validate(val);
          if (err) {
            if (Kairo.core && Kairo.core.toast) Kairo.core.toast(err, 'warn');
            input.focus();
            return;
          }
        }
        resolved = true;
        dlg.close();
        resolve(val);
      }
      okBtn.onclick = submit;
      cancelBtn.onclick = function () { dlg.close(); };
      input.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter') {
          ev.preventDefault();
          submit();
        }
      });
      setTimeout(function () { input.focus(); input.select(); }, 50);
    });
  }

  Kairo.overlays = {
    modal,
    prompt,
    closeTop: function () {
      const top = stack[stack.length - 1];
      if (top) top.close();
    }
  };
})();
