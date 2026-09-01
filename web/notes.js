/* ===== web/notes.js — application-scope note controller + floating editor ===== */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { el, toast } = Kairo.core;
  const { api } = Kairo.api;
  const LAYOUT_KEY = 'kairo:notes:layout';
  const COLORS = [
    { id: 'yellow', name: '黄' },
    { id: 'blue', name: '蓝' },
    { id: 'green', name: '绿' },
    { id: 'pink', name: '粉' },
    { id: 'gray', name: '灰' }
  ];

  const state = {
    notes: [], initialized: false, loading: null, listeners: [], eventSource: null,
    dirty: {}, pending: {}, saving: {}, saveTimers: {}, activeId: '', pendingReminder: null,
    inlineDrafts: {}, conflictOpen: {}, eventsEverOpen: false
  };

  function clone(v) { return JSON.parse(JSON.stringify(v)); }
  function find(id) { return state.notes.find(function (n) { return n.id === id; }); }
  function defaultDesktop() {
    return { visible: true, x_ratio: .72, y_ratio: .18, width: 340, height: 280 };
  }
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
  function setInlineDraft(id, on) {
    if (on) state.inlineDrafts[id] = true;
    else delete state.inlineDrafts[id];
  }
  function hasUnsaved() {
    return Object.keys(state.dirty).some(function (k) { return state.dirty[k]; })
      || Object.keys(state.pending).length > 0
      || Object.keys(state.inlineDrafts).length > 0;
  }
  function reminderContent(n) {
    const title = String(n && n.title || '').trim();
    const body = String(n && n.body || '').trim();
    if (title && body) return (title + '\n' + body).slice(0, 200);
    return (title || body).slice(0, 200);
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
      pinned: !!initial.pinned, floating: !!initial.floating,
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
    const payload = Object.assign({}, fields || {});
    delete payload.base_revision;
    payload.base_revision = n.revision;
    state.dirty[id] = true;
    try {
      const updated = await api('PATCH', '/api/notes/' + encodeURIComponent(id), payload);
      state.dirty[id] = !!(state.pending[id] && Object.keys(state.pending[id]).length);
      replace(updated);
      if (state.pending[id]) Object.assign(updated, state.pending[id]);
      if (updated.floating) state.activeId = updated.id;
      else if (state.activeId === id && !updated.floating) state.activeId = '';
      if (!opts.silent) emit(); else syncFloatingStatus(state.dirty[id] ? '保存中…' : '已保存');
      return updated;
    } catch (err) {
      state.dirty[id] = !!(state.pending[id] && Object.keys(state.pending[id]).length);
      if (err.status === 409) {
        const current = err.current || (err.data && err.data.current);
        if (current) replace(current);
        showConflict(id, fields, current);
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
    if (state.saving[id] || state.conflictOpen[id]) return;
    const fields = state.pending[id];
    if (!fields || !Object.keys(fields).length) return;
    delete state.pending[id];
    state.saving[id] = true;
    update(id, fields, { silent: true }).catch(function (err) {
      if (err.status === 409) {
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
      if (state.pending[id] && !state.conflictOpen[id]) {
        state.saveTimers[id] = setTimeout(function () { flushSave(id); }, 0);
      }
    });
  }

  async function remove(id) {
    await api('DELETE', '/api/notes/' + encodeURIComponent(id));
    state.notes = state.notes.filter(function (n) { return n.id !== id; });
    if (state.activeId === id) state.activeId = '';
    setInlineDraft(id, false);
    delete state.dirty[id]; delete state.pending[id];
    emit();
  }

  async function setFloating(id, visible) {
    const n = find(id);
    if (!n) return;
    const updated = await update(id, { floating: !!visible });
    state.activeId = visible ? updated.id : '';
    emit();
    return updated;
  }

  async function setDesktop(id, visible) {
    const n = find(id);
    if (!n) throw new Error('便笺不存在');
    const desktop = Object.assign(defaultDesktop(), n.desktop || {});
    desktop.visible = !!visible;
    return update(id, { desktop: desktop });
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
    es.addEventListener('ready', function () { state.eventsEverOpen = true; });
    ['created', 'updated'].forEach(function (kind) {
      es.addEventListener(kind, function (ev) {
        try {
          const data = JSON.parse(ev.data || '{}');
          if (!data.note) return;
          if (state.dirty[data.note.id] || state.inlineDrafts[data.note.id]) {
            const local = find(data.note.id);
            if (local) {
              local.revision = data.note.revision;
              local.updated_at = data.note.updated_at;
              local.desktop = data.note.desktop;
              local.floating = data.note.floating;
              local.archived = data.note.archived;
              local.pinned = data.note.pinned;
            }
            if (state.inlineDrafts[data.note.id]) emit();
            return;
          }
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
        setInlineDraft(data.id, false);
        emit();
      } catch (_) {}
    });
    es.onerror = function () {
      if (!state.eventsEverOpen) return;
      setTimeout(function () { load().catch(function () {}); }, 800);
    };
  }

  function showConflict(id, attempted, current) {
    if (state.conflictOpen[id]) return;
    state.conflictOpen[id] = true;
    const mine = String((attempted && (attempted.body != null ? attempted.body : attempted.title)) || '');
    const body = el('div', { class: 'note-conflict' }, [
      el('p', { text: '这条便笺已在另一个窗口更新。请选择保留哪一份，系统不会自动覆盖。' }),
      el('pre', { class: 'mono note-conflict-copy', text: mine || '（本次提交没有正文）' })
    ]);
    function finish() { m.close(); }
    const keepBtn = el('button', { class: 'btn', text: '保留我的版本', onclick: async function () {
      try {
        const base = (current && current.revision) || (find(id) && find(id).revision);
        const n = find(id);
        if (!n || !base) throw new Error('无法读取最新版本');
        n.revision = base;
        await update(id, attempted || {});
        toast('已保留当前编辑', 'ok');
        finish();
      } catch (e) { toast(e.message, 'err'); }
    }});
    const copyBtn = el('button', { class: 'btn', text: '复制两份', onclick: async function () {
      try {
        await create({
          title: ((current && current.title) || '冲突副本'),
          body: mine,
          color: (current && current.color) || 'yellow'
        });
        Kairo.core.copyToClipboard(mine);
        toast('已复制内容，并另存一份便笺', 'ok');
        finish();
        emit();
      } catch (e) { toast(e.message, 'err'); }
    }});
    const latestBtn = el('button', { class: 'btn btn-primary', text: '使用最新版本', onclick: function () {
      finish();
      emit();
    }});
    const m = Kairo.overlays.modal({
      title: '便笺版本冲突', body,
      footer: el('div', { class: 'editor-footer' }, [keepBtn, copyBtn, latestBtn]),
      width: 480,
      onClose: function () { state.conflictOpen[id] = false; }
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
    if (current && current.dataset.noteId === note.id) {
      current.className = 'floating-note note-color-' + note.color;
      if (current.contains(document.activeElement) || state.dirty[note.id] || state.inlineDrafts[note.id]) {
        syncFloatingStatus(state.dirty[note.id] ? '保存中…' : '已保存');
        return;
      }
    }
    layer.innerHTML = '';
    layer.appendChild(buildFloating(note));
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
    COLORS.forEach(function (c) {
      const opt = el('option', { value: c.id, text: c.name });
      opt.selected = note.color === c.id; color.appendChild(opt);
    });
    const panel = el('section', { class: 'floating-note note-color-' + note.color, role: 'dialog', 'aria-label': '悬浮便笺' });
    color.onchange = function () {
      panel.className = 'floating-note note-color-' + color.value;
      update(note.id, { color: color.value }, { silent: true }).catch(function (e) { toast(e.message, 'err'); });
    };
    const header = el('div', { class: 'floating-note-header' }, [title,
      el('button', { class: 'floating-note-icon-btn', type: 'button', text: '—', title: '收起页面悬浮', 'aria-label': '收起页面悬浮', onclick: function () { setFloating(note.id, false); } })
    ]);
    const footer = el('div', { class: 'floating-note-footer' }, [
      el('span', { class: 'floating-note-status muted', text: state.dirty[note.id] ? '保存中…' : '已保存' }),
      color,
      el('button', { class: 'btn btn-sm', text: '设提醒', onclick: function () { openReminder(find(note.id) || note); } }),
      el('button', { class: 'btn btn-sm', text: (note.desktop && note.desktop.visible) ? '从桌面拿走' : '置顶到桌面', onclick: async function () {
        try {
          const onDesktop = !!(find(note.id) && find(note.id).desktop && find(note.id).desktop.visible);
          await setDesktop(note.id, !onDesktop);
          toast(onDesktop ? '已从桌面收起' : '已置顶到 Windows 桌面', 'ok');
        } catch (e) { toast('桌面置顶失败：' + e.message, 'err'); }
      }}),
      el('button', { class: 'btn btn-sm', text: '管理', onclick: function () { location.hash = '#/notes/list'; } })
    ]);
    panel.append(header, body, footer);
    panel.dataset.noteId = note.id;
    const layout = readLayout();
    if (layout.left != null) panel.style.left = Math.max(8, Math.min(window.innerWidth - 260, layout.left)) + 'px';
    if (layout.top != null) panel.style.top = Math.max(56, Math.min(window.innerHeight - 140, layout.top)) + 'px';
    if (layout.width) panel.style.width = Math.max(280, Math.min(700, layout.width)) + 'px';
    if (layout.height) panel.style.height = Math.max(220, Math.min(800, layout.height)) + 'px';
    bindDrag(panel, header);
    panel.addEventListener('mouseup', function () { saveLayout(panel); });
    panel.addEventListener('keydown', function (ev) {
      if (ev.key === 'Escape') { ev.preventDefault(); setFloating(note.id, false); }
    });
    return panel;
  }

  const drag = { active: false, panel: null, startX: 0, startY: 0, left: 0, top: 0, bound: false };
  function bindDrag(panel, handle) {
    if (!drag.bound) {
      drag.bound = true;
      document.addEventListener('mousemove', function (ev) {
        if (!drag.active || !drag.panel) return;
        drag.panel.style.left = Math.max(8, Math.min(window.innerWidth - drag.panel.offsetWidth - 8, drag.left + ev.clientX - drag.startX)) + 'px';
        drag.panel.style.top = Math.max(56, Math.min(window.innerHeight - 80, drag.top + ev.clientY - drag.startY)) + 'px';
      });
      document.addEventListener('mouseup', function () {
        if (!drag.active) return;
        drag.active = false;
        if (drag.panel) saveLayout(drag.panel);
        drag.panel = null;
      });
    }
    handle.addEventListener('mousedown', function (ev) {
      if (ev.target && (ev.target.tagName === 'INPUT' || ev.target.tagName === 'BUTTON' || ev.target.tagName === 'SELECT')) return;
      drag.active = true; drag.panel = panel;
      drag.startX = ev.clientX; drag.startY = ev.clientY;
      const r = panel.getBoundingClientRect(); drag.left = r.left; drag.top = r.top;
      panel.style.right = 'auto';
      ev.preventDefault();
    });
  }

  function openReminder(n) {
    if (!n) return;
    state.pendingReminder = clone(n);
    location.hash = '#/notes/reminders';
  }

  async function capture(opts) {
    opts = opts || {};
    const text = String(opts.text || '').trim();
    if (!text) throw new Error('没有可加入便笺的文本');
    return create({
      title: opts.title || '页面摘录',
      body: text,
      floating: false,
      desktop: defaultDesktop()
    });
  }

  async function toggleFromTopbar() {
    const selection = String(window.getSelection ? window.getSelection() : '').trim();
    if (selection) {
      await capture({ text: selection, title: '页面摘录' });
      toast('选中内容已放到桌面便笺', 'ok');
      return;
    }
    await create({ floating: false, desktop: defaultDesktop() });
    toast('已新建桌面便笺', 'ok');
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
        btn.appendChild(el('span', { class: 'theme-toggle-label', text: '便笺' }));
      } else {
        const label = btn.querySelector('.theme-toggle-label');
        if (label) label.textContent = '便笺';
      }
      btn.title = '新建桌面便笺；有选中文本时摘录上去（Alt+Shift+N）';
      btn.setAttribute('aria-label', '新建桌面便笺');
      btn.onclick = function () { toggleFromTopbar().catch(function (e) { toast('打开便笺失败：' + e.message, 'err'); }); };
    }
    window.addEventListener('keydown', function (ev) {
      if (ev.altKey && ev.shiftKey && String(ev.key).toLowerCase() === 'n') {
        ev.preventDefault(); toggleFromTopbar().catch(function (e) { toast(e.message, 'err'); });
      }
    });
    window.addEventListener('beforeunload', function (ev) {
      if (!hasUnsaved()) return;
      ev.preventDefault();
      ev.returnValue = '';
    });
    load().then(connectEvents).catch(function (e) { toast('便笺初始化失败：' + e.message, 'err'); });
  }

  Kairo.notes = {
    state, colors: COLORS, init, load, create, update, remove,
    setFloating, setDesktop, subscribe, find, capture, openReminder,
    reminderContent, setInlineDraft, hasUnsaved, defaultDesktop
  };
})();
