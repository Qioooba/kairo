/* ===== web/pages/notes.js — note center: list + reminders tabs ===== */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { el, toast, confirmDialog } = Kairo.core;

  function renderNotes(view, routeState) {
    routeState = routeState || {};
    let tab = routeState.tab === 'reminders' ? 'reminders' : 'list';
    let query = '';
    let filter = 'active';
    let unsubscribe = null;

    const tabs = el('div', { class: 'tabs-bar notes-center-tabs' });
    const content = el('div', { class: 'notes-center-content' });
    const listBtn = el('button', { class: 'tab-btn', text: '便笺', onclick: function () { switchTab('list'); } });
    const reminderBtn = el('button', { class: 'tab-btn', text: '提醒', onclick: function () { switchTab('reminders'); } });
    tabs.appendChild(listBtn); tabs.appendChild(reminderBtn);
    view.appendChild(el('div', { class: 'notes-center-head' }, [
      el('div', {}, [
        el('h2', { class: 'notes-center-title', text: '便笺中心' }),
        el('div', { class: 'muted', text: '随手记录，可页面悬浮或置顶到桌面，需要时再设提醒。' })
      ]),
      tabs
    ]));
    view.appendChild(content);

    function switchTab(next) {
      tab = next;
      const target = next === 'reminders' ? '#/notes/reminders' : '#/notes/list';
      if (location.hash !== target && history && history.replaceState) history.replaceState(null, '', target);
      renderTab();
    }

    function renderTab() {
      if (unsubscribe) { unsubscribe(); unsubscribe = null; }
      listBtn.classList.toggle('active', tab === 'list');
      reminderBtn.classList.toggle('active', tab === 'reminders');
      content.innerHTML = '';
      if (tab === 'reminders') {
        Kairo.reminders.render(content);
        const pending = Kairo.notes.state.pendingReminder;
        if (pending) {
          Kairo.notes.state.pendingReminder = null;
          const snapshot = Kairo.notes.reminderContent(pending);
          setTimeout(function () {
            Kairo.reminders.openEditor(null, 'once', function () {
              Kairo.reminders.render(content);
            }, {
              content: snapshot,
              sourceNoteId: pending.id
            });
            if (!snapshot) toast('便笺还没有内容，请填写提醒正文', 'warn');
          }, 0);
        }
        return;
      }
      renderListShell();
    }

    function renderListShell() {
      const search = el('input', { type: 'text', class: 'editor-input notes-search', placeholder: '搜索标题或正文…', 'aria-label': '搜索便笺', autocomplete: 'off' });
      search.value = query;
      search.oninput = function () { query = search.value; draw(Kairo.notes.state.notes); };
      const filterSel = el('select', { class: 'editor-input notes-filter', 'aria-label': '筛选便笺' });
      [['active', '未归档'], ['floating', '页面悬浮'], ['desktop', '桌面置顶'], ['archived', '已归档'], ['all', '全部']].forEach(function (pair) {
        const opt = el('option', { value: pair[0], text: pair[1] });
        opt.selected = filter === pair[0];
        filterSel.appendChild(opt);
      });
      filterSel.onchange = function () { filter = filterSel.value; draw(Kairo.notes.state.notes); };
      const newBtn = el('button', { class: 'btn btn-primary', text: '+ 新建便笺', onclick: function () { createAndEdit(); } });
      const toolbar = el('div', { class: 'notes-toolbar' }, [search, filterSel, newBtn]);
      const list = el('div', { class: 'notes-list' });
      content.appendChild(toolbar); content.appendChild(list);

      function filteredItems(items) {
        const q = query.trim().toLowerCase();
        return items.filter(function (n) {
          if (q && (String(n.title) + '\n' + String(n.body)).toLowerCase().indexOf(q) < 0) return false;
          if (filter === 'active' && n.archived) return false;
          if (filter === 'floating' && (!n.floating || n.archived)) return false;
          if (filter === 'desktop' && (n.archived || !n.desktop || !n.desktop.visible)) return false;
          if (filter === 'archived' && !n.archived) return false;
          return true;
        });
      }

      function emptyState(items) {
        const hasAny = items.some(function (n) { return !n.archived; });
        const firstTime = !hasAny && filter === 'active' && !query.trim();
        const box = el('div', { class: 'empty-state notes-empty' });
        box.appendChild(el('div', { class: 'empty-title', text: firstTime ? '还没有便笺' : '没有符合条件的便笺' }));
        box.appendChild(el('div', { class: 'empty-desc', text: firstTime
          ? '记下主机、路径、request id 或下一步要做的事。'
          : '调整搜索或筛选，也可以再新建一条。' }));
        const cta = el('button', { class: 'btn btn-primary', text: firstTime ? '新建第一条便笺' : '新建便笺', onclick: function () { createAndEdit(); } });
        box.appendChild(cta);
        return box;
      }

      function draw(items) {
        items = items || [];
        if (!Kairo.notes.state.initialized) {
          if (!list.querySelector('.notes-loading')) {
            list.innerHTML = '';
            list.appendChild(el('div', { class: 'empty-state notes-empty notes-loading', text: '加载便笺…' }));
          }
          return;
        }
        const filtered = filteredItems(items);
        const editing = list.querySelector('.notes-row.editing');
        const editingId = editing ? editing.dataset.noteId : '';
        if (!filtered.length && !editingId) {
          list.innerHTML = '';
          list.appendChild(emptyState(items));
          return;
        }
        const empty = list.querySelector('.notes-empty, .empty-state');
        if (empty) empty.remove();

        const keep = {};
        filtered.forEach(function (n) { keep[n.id] = true; });
        if (editingId) keep[editingId] = true;
        Array.from(list.children).forEach(function (node) {
          const id = node.dataset.noteId;
          if (!id || !keep[id]) node.remove();
        });

        filtered.forEach(function (n, i) {
          let node = list.querySelector('[data-note-id="' + n.id + '"]');
          if (node && node.classList.contains('editing')) {
            /* 编辑中的卡片不替换，避免桌面窗口自动保存把输入冲掉 */
          } else {
            const row = renderRow(n);
            if (node) node.replaceWith(row);
            else list.appendChild(row);
            node = row;
          }
          const current = list.children[i];
          if (node && current !== node) list.insertBefore(node, current || null);
        });
      }

      function metaText(n) {
        const bits = [];
        if (n.pinned) bits.push('列表置顶');
        if (n.floating && !n.archived) bits.push('页面悬浮');
        if (n.desktop && n.desktop.visible && !n.archived) bits.push('桌面置顶');
        if (n.archived) bits.push('已归档');
        bits.push('更新于 ' + formatTime(n.updated_at));
        return bits.join(' · ');
      }

      function renderRow(n) {
        const title = n.title || firstLine(n.body) || '未命名便笺';
        const article = el('article', { class: 'notes-row note-color-' + (n.color || 'yellow'), 'data-note-id': n.id });
        const main = el('div', {
          class: 'notes-row-main', tabindex: '0', title: '点击编辑',
          onclick: function (ev) { if (ev.target.closest && ev.target.closest('button, a, details')) return; beginInlineEdit(article, Kairo.notes.find(n.id) || n); },
          onkeydown: function (ev) { if (ev.key === 'Enter') beginInlineEdit(article, Kairo.notes.find(n.id) || n); }
        }, [
          el('div', { class: 'notes-row-title', text: title }),
          el('div', { class: 'notes-row-preview', text: String(n.body || '').slice(0, 240) || '空便笺' }),
          el('div', { class: 'notes-row-meta muted', text: metaText(n) })
        ]);
        const more = el('details', { class: 'notes-more' });
        more.appendChild(el('summary', { text: '更多' }));
        more.appendChild(el('div', { class: 'notes-more-menu' }, [
          actionBtn(n.pinned ? '取消列表置顶' : '列表置顶', function () { return Kairo.notes.update(n.id, { pinned: !n.pinned }); }),
          actionBtn('设提醒', function () { Kairo.notes.openReminder(Kairo.notes.find(n.id) || n); }),
          actionBtn(n.archived ? '恢复' : '归档', function () { return Kairo.notes.update(n.id, { archived: !n.archived }); }),
          actionBtn('删除', async function () {
            if (!await confirmDialog('确定删除这条便笺？此操作不能撤销。', { title: '删除便笺', okText: '删除' })) return;
            return Kairo.notes.remove(n.id);
          }, 'btn-danger')
        ]));
        more.addEventListener('toggle', function () {
          if (!more.open) return;
          list.querySelectorAll('.notes-more[open]').forEach(function (d) { if (d !== more) d.removeAttribute('open'); });
        });
        const actions = el('div', { class: 'notes-row-actions' }, [
          actionBtn(n.floating ? '收起悬浮' : '页面悬浮', function () {
            return Kairo.notes.setFloating(n.id, !n.floating);
          }),
          actionBtn(n.desktop && n.desktop.visible ? '从桌面拿走' : '置顶到桌面', function () {
            return Kairo.notes.setDesktop(n.id, !(n.desktop && n.desktop.visible));
          }),
          more
        ]);
        article.append(main, actions);
        return article;
      }

      function beginInlineEdit(article, n) {
        if (!article || article.classList.contains('editing') || !n) return;
        article.classList.add('editing');
        Kairo.notes.setInlineDraft(n.id, true);
        const main = article.querySelector('.notes-row-main');
        const actions = article.querySelector('.notes-row-actions');
        const title = el('input', { type: 'text', class: 'notes-inline-title', maxlength: '80', placeholder: '标题（可留空）', 'aria-label': '便笺标题', autocomplete: 'off' });
        title.value = n.title || '';
        const body = el('textarea', { class: 'notes-inline-body', maxlength: '20000', placeholder: '直接输入便笺内容…', 'aria-label': '便笺正文' });
        body.value = n.body || '';
        const colors = el('div', { class: 'notes-inline-colors', role: 'radiogroup', 'aria-label': '便笺颜色' });
        (Kairo.notes.colors || []).forEach(function (c) {
          const swatch = el('button', {
            class: 'note-color-option' + (n.color === c.id ? ' active' : ''),
            type: 'button', 'data-color': c.id, title: c.name, 'aria-label': c.name
          });
          swatch.onclick = function (ev) {
            ev.stopPropagation();
            colors.querySelectorAll('.active').forEach(function (x) { x.classList.remove('active'); });
            swatch.classList.add('active');
            article.className = 'notes-row editing note-color-' + c.id;
          };
          colors.appendChild(swatch);
        });
        main.innerHTML = '';
        main.append(title, body, colors);
        main.onclick = function (ev) { ev.stopPropagation(); };
        actions.innerHTML = '';
        function leaveEdit() {
          article.classList.remove('editing');
          Kairo.notes.setInlineDraft(n.id, false);
          draw(Kairo.notes.state.notes);
        }
        const closeEdit = el('button', { class: 'notes-inline-close', type: 'button', title: '关闭', 'aria-label': '关闭', text: '×' });
        closeEdit.onclick = function (ev) { ev.preventDefault(); ev.stopPropagation(); leaveEdit(); };
        article.appendChild(closeEdit);
        actions.append(
          actionBtn('取消', function () { leaveEdit(); }),
          actionBtn('保存', async function () {
            const active = colors.querySelector('.active');
            await Kairo.notes.update(n.id, {
              title: title.value,
              body: body.value,
              color: active ? active.dataset.color : n.color
            });
            Kairo.notes.setInlineDraft(n.id, false);
            article.classList.remove('editing');
            draw(Kairo.notes.state.notes);
            toast('便笺已保存', 'ok');
          })
        );
        body.addEventListener('keydown', function (ev) {
          if (ev.key === 'Escape') { ev.preventDefault(); leaveEdit(); }
        });
        title.addEventListener('keydown', function (ev) {
          if (ev.key === 'Escape') { ev.preventDefault(); leaveEdit(); }
        });
        body.focus();
      }

      async function createAndEdit() {
        try {
          if (filter === 'archived' || filter === 'floating' || filter === 'desktop') filter = 'active';
          filterSel.value = filter;
          const n = await Kairo.notes.create({ floating: false });
          draw(Kairo.notes.state.notes);
          const article = list.querySelector('[data-note-id="' + n.id + '"]');
          beginInlineEdit(article, n);
        } catch (e) { toast(e.message, 'err'); }
      }

      function closeMoreMenus(ev) {
        if (ev.target && ev.target.closest && ev.target.closest('.notes-more')) return;
        list.querySelectorAll('.notes-more[open]').forEach(function (d) { d.removeAttribute('open'); });
      }
      document.addEventListener('click', closeMoreMenus);
      const unsubNotes = Kairo.notes.subscribe(draw);
      unsubscribe = function () {
        unsubNotes();
        document.removeEventListener('click', closeMoreMenus);
      };
      draw(Kairo.notes.state.notes);
      Kairo.notes.load().catch(function (e) {
        toast('加载便笺失败：' + e.message, 'err');
        list.innerHTML = '';
        list.appendChild(el('div', { class: 'empty-state notes-empty', text: '加载失败，请刷新页面重试' }));
      });
    }

    function actionBtn(text, fn, extra) {
      return el('button', { class: 'btn btn-sm ' + (extra || ''), type: 'button', text: text, onclick: function (ev) {
        ev.stopPropagation();
        Promise.resolve(fn()).catch(function (e) { toast(e.message, 'err'); });
      }});
    }

    function firstLine(s) {
      return String(s || '').split('\n').map(function (x) { return x.trim(); }).find(Boolean) || '';
    }
    function formatTime(v) {
      try { return new Date(v).toLocaleString(); } catch (_) { return String(v || ''); }
    }

    renderTab();
    return function () { if (unsubscribe) unsubscribe(); };
  }

  Kairo.pages.notes = renderNotes;
  Kairo.state.routes.notes = renderNotes;
  Kairo.state.routeNames.notes = '便笺';
})();
