/* ===== web/pages/notes.js — note center composition page ===== */
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
      el('div', {}, [el('h2', { class: 'notes-center-title', text: '便笺中心' }), el('div', { class: 'muted', text: '随手记录、跨页面悬浮，需要时再设提醒。' })]),
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
          setTimeout(function () {
            Kairo.reminders.openEditor(null, 'once', function () {
              Kairo.reminders.render(content);
            }, {
              content: pending.title || String(pending.body || '').slice(0, 200),
              sourceNoteId: pending.id
            });
          }, 0);
        }
        return;
      }
      renderListShell();
    }

    function renderListShell() {
      const search = el('input', { type: 'search', class: 'notes-search', placeholder: '搜索标题、正文、主机或路径…' });
      search.value = query;
      search.oninput = function () { query = search.value; draw(Kairo.notes.state.notes); };
      const filterSel = el('select', { class: 'notes-filter' });
      [['active','未归档'],['floating','悬浮中'],['desktop','桌面置顶'],['archived','已归档'],['all','全部']].forEach(function (pair) {
        const opt = el('option', { value: pair[0], text: pair[1] }); opt.selected = filter === pair[0]; filterSel.appendChild(opt);
      });
      filterSel.onchange = function () { filter = filterSel.value; draw(Kairo.notes.state.notes); };
      const newBtn = el('button', { class: 'btn btn-primary', text: '+ 新建便笺', onclick: function () {
        Kairo.notes.create({ floating: true }).then(function (n) { openEditor(n); }).catch(function (e) { toast(e.message, 'err'); });
      }});
      const toolbar = el('div', { class: 'notes-toolbar' }, [search, filterSel, newBtn]);
      const list = el('div', { class: 'notes-list' });
      content.appendChild(toolbar); content.appendChild(list);

      function draw(items) {
        list.innerHTML = '';
        const q = query.trim().toLowerCase();
        const filtered = items.filter(function (n) {
          if (q && (String(n.title) + '\n' + String(n.body)).toLowerCase().indexOf(q) < 0) return false;
          if (filter === 'active' && n.archived) return false;
          if (filter === 'floating' && (!n.floating || n.archived)) return false;
          if (filter === 'desktop' && (!n.desktop || !n.desktop.visible)) return false;
          if (filter === 'archived' && !n.archived) return false;
          return true;
        });
        if (!filtered.length) {
          list.appendChild(el('div', { class: 'empty-state' }, [
            el('div', { class: 'empty-title', text: '没有符合条件的便笺' }),
            el('div', { class: 'empty-desc', text: '新建一条，或者调整搜索和筛选条件。' })
          ]));
          return;
        }
        filtered.forEach(function (n) { list.appendChild(renderRow(n)); });
      }

      function renderRow(n) {
        const title = n.title || firstLine(n.body) || '未命名便笺';
        const meta = [];
        if (n.pinned) meta.push('列表置顶');
        if (n.floating) meta.push('Kairo 内悬浮');
        if (n.desktop && n.desktop.visible) meta.push('桌面置顶');
        if (n.archived) meta.push('已归档');
        const main = el('button', { class: 'notes-row-main', type: 'button', onclick: function () { openEditor(n); } }, [
          el('div', { class: 'notes-row-title', text: title }),
          el('div', { class: 'notes-row-preview', text: String(n.body || '').slice(0, 240) || '空便笺' }),
          el('div', { class: 'notes-row-meta muted', text: meta.join(' · ') || ('更新于 ' + formatTime(n.updated_at)) })
        ]);
        const actions = el('div', { class: 'notes-row-actions' }, [
          actionBtn(n.floating ? '收起' : '悬浮', function () { Kairo.notes.setFloating(n.id, !n.floating); }),
          actionBtn(n.pinned ? '取消置顶' : '列表置顶', function () { Kairo.notes.update(n.id, { pinned: !n.pinned }); }),
          actionBtn('设提醒', function () { Kairo.notes.state.pendingReminder = n; switchTab('reminders'); }),
          actionBtn(n.archived ? '恢复' : '归档', function () { Kairo.notes.update(n.id, { archived: !n.archived }); }),
          actionBtn('删除', async function () {
            if (!await confirmDialog('确定删除这条便笺？此操作不能撤销。', { title: '删除便笺', okText: '删除' })) return;
            Kairo.notes.remove(n.id).catch(function (e) { toast(e.message, 'err'); });
          }, 'btn-danger')
        ]);
        return el('article', { class: 'notes-row note-color-' + n.color }, [main, actions]);
      }

      unsubscribe = Kairo.notes.subscribe(draw);
      Kairo.notes.load().then(draw).catch(function (e) { toast('加载便笺失败：' + e.message, 'err'); });
    }

    function actionBtn(text, fn, extra) {
      return el('button', { class: 'btn btn-sm ' + (extra || ''), text, onclick: function () {
        Promise.resolve(fn()).catch(function (e) { toast(e.message, 'err'); });
      }});
    }

    function openEditor(n) {
      const title = el('input', { class: 'editor-input', type: 'text', maxlength: '80', placeholder: '标题（可留空）' });
      title.value = n.title || '';
      const body = el('textarea', { class: 'note-center-editor mono', maxlength: '20000', placeholder: '记录主机、路径、日志片段或下一步…' });
      body.value = n.body || '';
      const color = el('select', { class: 'editor-input' });
      [['yellow','黄色'],['blue','蓝色'],['green','绿色'],['pink','粉色'],['gray','灰色']].forEach(function (pair) {
        const opt = el('option', { value: pair[0], text: pair[1] }); opt.selected = n.color === pair[0]; color.appendChild(opt);
      });
      const modal = Kairo.overlays.modal({
        title: '编辑便笺',
        body: el('div', { class: 'editor-fields' }, [title, body, color]),
        footer: el('div', { class: 'editor-footer' }, [
          el('button', { class: 'btn', text: '取消', onclick: function () { modal.close(); } }),
          el('button', { class: 'btn btn-primary', text: '保存', onclick: async function () {
            try {
              await Kairo.notes.update(n.id, { title: title.value, body: body.value, color: color.value });
              modal.close(); toast('便笺已保存', 'ok');
            } catch (e) { toast('保存失败：' + e.message, 'err'); }
          }})
        ]), width: 600
      });
      setTimeout(function () { body.focus(); }, 0);
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
