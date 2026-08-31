/* ===== web/notes.js — application-scope note controller + floating editor ===== */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { el, toast } = Kairo.core;
  const { api } = Kairo.api;
  const LAYOUT_KEY = 'kairo:notes:layout';

  const state = {
    notes: [], initialized: false, loading: null, listeners: [], eventSource: null,
    dirty: {}, pending: {}, saving: {}, saveTimers: {}, activeId: '', pendingReminder: null
  };

  function clone(v) { return JSON.parse(JSON.stringify(v)); }
  function find(id) { return state.notes.find(function (n) { return n.id === id; }); }
  function emit() {
    const snapshot = state.notes.map(clone);
    state.listeners.slice().forEach(function (fn) { try { fn(snapshot); } catch (_) {} });
    syncFloating();
  }
  function replace(note) {
    const i = state.notes.findIndex(function (n) { return n.id === note.id; });
    if (i >= 0) state.notes[i] = note; else state.notes.push(note);
    state.notes.sort(function (a, b) {
      if (!!a.pinned !== !!b.pinned) return a.pinned ? -1 : 1;
      return String(b.updated_at).localeCompare(String(a.updated_at));
    });
  }

  async function load() {
    if (state.loading) return state.loading;
    state.loading = api('GET', '/api/notes').then(function (items) {
      state.notes = Array.isArray(items) ? items : [];
      state.initialized = true;
      const floating = state.notes.find(function (n) { return n.floating && !n.archived; });
      state.activeId = floating ? floating.id : '';
      emit();
      return state.notes;
    }).finally(function () { state.loading = null; });
    return state.loading;
  }

  async function create(initial) {
    initial = initial || {};
    const n = await api('POST', '/api/notes', {
      title: initial.title || '', body: initial.body || '', color: initial.color || 'yellow',
      pinned: !!initial.pinned, floating: initial.floating !== false,
      desktop: initial.desktop || null
    });
    replace(n);
    state.activeId = n.floating ? n.id : state.activeId;
    emit();
    return n;
  }

  async function update(id, fields, opts) {
    opts = opts || {};
    const n = find(id);
    if (!n) throw new Error('便笺不存在');
    const payload = Object.assign({ base_revision: n.revision }, fields || {});
    state.dirty[id] = true;
    try {
      const updated = await api('PATCH', '/api/notes/' + encodeURIComponent(id), payload);
      state.dirty[id] = !!(state.pending[id] && Object.keys(state.pending[id]).length);
      replace(updated);
      if (state.pending[id]) Object.assign(updated, state.pending[id]);
      if (updated.floating) state.activeId = updated.id;
      if (!opts.silent) emit(); else syncFloatingStatus('已保存');
      return updated;
    } catch (err) {
      state.dirty[id] = false;
      if (err.status === 409) {
        await load();
        showConflict(id, fields);
      }
      throw err;
    }
  }

  function scheduleSave(id, fields) {
    const n = find(id);
    if (!n) return;
    Object.keys(fields).forEach(function (k) { n[k] = fields[k]; });
    state.pending[id] = Object.assign(state.pending[id] || {}, fields);
    state.dirty[id] = true;
    syncFloatingStatus('保存中…');
    clearTimeout(state.saveTimers[id]);
    state.saveTimers[id] = setTimeout(function () { flushSave(id); }, 500);
  }

  function flushSave(id) {
    if (state.saving[id]) return;
    const fields = state.pending[id];
    if (!fields || !Object.keys(fields).length) return;
    delete state.pending[id];
    state.saving[id] = true;
    update(id, fields, { silent: true }).catch(function (err) {
      // 409 已由 update() 拉取最新版本并打开冲突对话框。这里不能把旧内容
      // 重新排队，否则下一轮会用新 revision 静默覆盖另一个窗口的修改。
      if (err.status === 409) {
        // 请求进行期间新输入的字段仍留在 pending，稍后可以基于最新版本保存；
        // 这里只丢弃本次冲突的 fields。
        state.dirty[id] = !!state.pending[id];
        syncFloatingStatus(state.pending[id] ? '保存中…' : '存在版本冲突');
        return;
      }
      state.pending[id] = Object.assign(fields, state.pending[id] || {});
      state.dirty[id] = true;
      syncFloatingStatus('保存失败');
      toast('便笺保存失败：' + err.message, 'err');
    }).finally(function () {
      state.saving[id] = false;
      if (state.pending[id]) {
        state.saveTimers[id] = setTimeout(function () { flushSave(id); }, 0);
      }
    });
  }

  async function remove(id) {
    await api('DELETE', '/api/notes/' + encodeURIComponent(id));
    state.notes = state.notes.filter(function (n) { return n.id !== id; });
    if (state.activeId === id) state.activeId = '';
    emit();
  }

  async function setFloating(id, visible) {
    const n = find(id);
    if (!n) return;
    const updated = await update(id, { floating: !!visible });
    state.activeId = visible ? updated.id : '';
    emit();
  }

  function subscribe(fn) {
    state.listeners.push(fn);
    if (state.initialized) fn(state.notes.map(clone));
    return function () { state.listeners = state.listeners.filter(function (x) { return x !== fn; }); };
  }

  function connectEvents() {
    if (!window.EventSource || state.eventSource) return;
    const es = new EventSource('/api/notes/events');
    state.eventSource = es;
    ['created', 'updated'].forEach(function (kind) {
      es.addEventListener(kind, function (ev) {
        try {
          const data = JSON.parse(ev.data || '{}');
          if (!data.note || state.dirty[data.note.id]) return;
          replace(data.note);
          const floating = state.notes.find(function (n) { return n.floating && !n.archived; });
          state.activeId = floating ? floating.id : '';
          emit();
        } catch (_) {}
      });
    });
    es.addEventListener('deleted', function (ev) {
      try {
        const data = JSON.parse(ev.data || '{}');
        state.notes = state.notes.filter(function (n) { return n.id !== data.id; });
        if (state.activeId === data.id) state.activeId = '';
        emit();
      } catch (_) {}
    });
    es.onerror = function () { /* EventSource reconnects automatically */ };
  }

  function showConflict(id, attempted) {
    const body = el('div', { class: 'note-conflict' }, [
      el('p', { text: '这条便笺已在另一个窗口更新。为避免覆盖，当前编辑没有自动提交。' }),
      el('pre', { class: 'mono note-conflict-copy', text: attempted.body || attempted.title || '' })
    ]);
    const copyBtn = el('button', { class: 'btn', text: '复制我的内容', onclick: function () {
      Kairo.core.copyToClipboard(attempted.body || attempted.title || '');
    }});
    const closeBtn = el('button', { class: 'btn btn-primary', text: '使用最新版本', onclick: function () { m.close(); emit(); } });
    const m = Kairo.overlays.modal({
      title: '便笺版本冲突', body,
      footer: el('div', { class: 'editor-footer' }, [copyBtn, closeBtn]), width: 460
    });
  }

  function readLayout() {
    try { return JSON.parse(localStorage.getItem(LAYOUT_KEY) || '{}'); } catch (_) { return {}; }
  }
  function saveLayout(panel) {
    try {
      const r = panel.getBoundingClientRect();
      localStorage.setItem(LAYOUT_KEY, JSON.stringify({ left: r.left, top: r.top, width: r.width, height: r.height }));
    } catch (_) {}
  }

  function syncFloatingStatus(text) {
    const e = document.querySelector('#kairo-note-layer .floating-note-status');
    if (e) e.textContent = text;
  }

  function syncFloating() {
    const layer = document.getElementById('kairo-note-layer');
    if (!layer) return;
    const note = find(state.activeId) || state.notes.find(function (n) { return n.floating && !n.archived; });
    if (!note || !note.floating || note.archived) {
      layer.innerHTML = '';
      return;
    }
    const current = layer.querySelector('.floating-note');
    if (current && current.dataset.noteId === note.id && document.activeElement && current.contains(document.activeElement)) {
      syncFloatingStatus(state.dirty[note.id] ? '保存中…' : '已保存');
      return;
    }
    layer.innerHTML = '';
    const panel = buildFloating(note);
    layer.appendChild(panel);
  }

  function buildFloating(note) {
    const title = el('input', { class: 'floating-note-title', type: 'text', maxlength: '80', 'aria-label': '便笺标题' });
    title.value = note.title || '';
    const body = el('textarea', { class: 'floating-note-body', maxlength: '20000', 'aria-label': '便笺正文', placeholder: '随手记下主机、路径、request id 或下一步…' });
    body.value = note.body || '';
    title.addEventListener('input', function () { scheduleSave(note.id, { title: title.value }); });
    body.addEventListener('input', function () { scheduleSave(note.id, { body: body.value }); });
    body.addEventListener('blur', function () {
      if (!state.dirty[note.id]) return;
      clearTimeout(state.saveTimers[note.id]);
      state.pending[note.id] = Object.assign(state.pending[note.id] || {}, { body: body.value });
      flushSave(note.id);
    });
    const color = el('select', { class: 'floating-note-color', title: '便笺颜色', 'aria-label': '便笺颜色' });
    [['yellow','黄'],['blue','蓝'],['green','绿'],['pink','粉'],['gray','灰']].forEach(function (pair) {
      const opt = el('option', { value: pair[0], text: pair[1] });
      opt.selected = note.color === pair[0]; color.appendChild(opt);
    });
    color.onchange = function () { update(note.id, { color: color.value }).catch(function (e) { toast(e.message, 'err'); }); };
    const header = el('div', { class: 'floating-note-header' }, [title,
      el('button', { class: 'floating-note-icon-btn', text: '—', title: '收起', onclick: function () { setFloating(note.id, false); } })
    ]);
    const footer = el('div', { class: 'floating-note-footer' }, [
      el('span', { class: 'floating-note-status muted', text: state.dirty[note.id] ? '保存中…' : '已保存' }),
      color,
      el('button', { class: 'btn btn-sm', text: '设提醒', onclick: function () {
        state.pendingReminder = clone(find(note.id));
        location.hash = '#/notes/reminders';
      }}),
      el('button', { class: 'btn btn-sm', text: '置顶到桌面', onclick: async function () {
        try {
          const layout = note.desktop || { visible: true, x_ratio: .72, y_ratio: .18, width: 340, height: 280 };
          layout.visible = true;
          await update(note.id, { desktop: layout });
          toast('已置顶到 Windows 桌面', 'ok');
        } catch (e) { toast('桌面置顶失败：' + e.message, 'err'); }
      }}),
      el('button', { class: 'btn btn-sm', text: '管理', onclick: function () { location.hash = '#/notes/list'; } })
    ]);
    const panel = el('section', { class: 'floating-note note-color-' + note.color, role: 'dialog', 'aria-label': '悬浮便笺' }, [header, body, footer]);
    panel.dataset.noteId = note.id;
    const layout = readLayout();
    if (layout.left != null) panel.style.left = Math.max(8, Math.min(window.innerWidth - 260, layout.left)) + 'px';
    if (layout.top != null) panel.style.top = Math.max(56, Math.min(window.innerHeight - 140, layout.top)) + 'px';
    if (layout.width) panel.style.width = Math.max(280, Math.min(700, layout.width)) + 'px';
    if (layout.height) panel.style.height = Math.max(220, Math.min(800, layout.height)) + 'px';
    bindDrag(panel, header);
    panel.addEventListener('mouseup', function () { saveLayout(panel); });
    return panel;
  }

  function bindDrag(panel, handle) {
    let dragging = false, startX = 0, startY = 0, left = 0, top = 0;
    handle.addEventListener('mousedown', function (ev) {
      if (ev.target && (ev.target.tagName === 'INPUT' || ev.target.tagName === 'BUTTON')) return;
      dragging = true; startX = ev.clientX; startY = ev.clientY;
      const r = panel.getBoundingClientRect(); left = r.left; top = r.top;
      panel.style.right = 'auto';
      ev.preventDefault();
    });
    document.addEventListener('mousemove', function (ev) {
      if (!dragging) return;
      panel.style.left = Math.max(8, Math.min(window.innerWidth - panel.offsetWidth - 8, left + ev.clientX - startX)) + 'px';
      panel.style.top = Math.max(56, Math.min(window.innerHeight - 80, top + ev.clientY - startY)) + 'px';
    });
    document.addEventListener('mouseup', function () { if (dragging) { dragging = false; saveLayout(panel); } });
  }

  async function toggleFromTopbar() {
    const selection = String(window.getSelection ? window.getSelection() : '').trim();
    await create({
      title: selection ? '页面摘录' : '', body: selection, floating: false,
      desktop: { visible: true, x_ratio: .72, y_ratio: .18, width: 340, height: 280 }
    });
    toast(selection ? '页面摘录已置顶到桌面' : '已新建桌面便笺', 'ok');
  }

  function init() {
    if (!document.getElementById('kairo-note-layer')) {
      document.body.appendChild(el('div', { id: 'kairo-note-layer' }));
    }
    const btn = document.getElementById('notes-toggle');
    if (btn && btn.dataset.bound !== '1') {
      btn.dataset.bound = '1';
      if (!btn.childNodes.length) {
        const icon = el('span', { class: 'icon' });
        icon.appendChild(Kairo.icons.svg('sticky-note'));
        btn.appendChild(icon);
        btn.appendChild(el('span', { class: 'theme-toggle-label', text: '新建便笺' }));
      }
      btn.title = '新建桌面便笺（Alt+Shift+N）';
      btn.setAttribute('aria-label', '新建桌面便笺');
      btn.onclick = function () { toggleFromTopbar().catch(function (e) { toast('打开便笺失败：' + e.message, 'err'); }); };
    }
    window.addEventListener('keydown', function (ev) {
      if (ev.altKey && ev.shiftKey && String(ev.key).toLowerCase() === 'n') {
        ev.preventDefault(); toggleFromTopbar().catch(function (e) { toast(e.message, 'err'); });
      }
    });
    load().then(connectEvents).catch(function (e) { toast('便笺初始化失败：' + e.message, 'err'); });
  }

  Kairo.notes = { state, init, load, create, update, remove, setFloating, subscribe, find };
})();
