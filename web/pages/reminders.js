/* ===== web/pages/reminders.js =====
 * 便笺提醒编辑页：单次 / 周循环 / 月循环三种类型，统一编辑界面。
 *
 * 数据流：
 *   GET    /api/reminders          →  列表
 *   POST   /api/reminders          →  新增
 *   PUT    /api/reminders/{id}     →  更新
 *   DELETE /api/reminders/{id}     →  删除
 *   POST   /api/reminders/{id}/toggle → 启用切换
 *   POST   /api/reminders/{id}/fire   → 立即触发（测试）
 *
 * 触发逻辑在后端 reminder.Manager（事件驱动 time.AfterFunc，非轮询）。
 * 本页只负责 CRUD + UI，触发由后端推到 popup 包显示。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, escapeHtml, copyToClipboard } = Kairo.core;
  const { api } = Kairo.api;

  // ---- minimal modal（core.js 文档说有 modal 但实际没暴露，这里 inline）----
  function modal({ title, body, footer, width }) {
    const overlay = el('div', { class: 'modal-overlay' });
    const card = el('div', { class: 'modal-card' });
    if (width) card.style.width = width + 'px';
    if (title) card.appendChild(el('div', { class: 'modal-title', text: title }));
    if (body) card.appendChild(body);
    if (footer) card.appendChild(footer);
    overlay.appendChild(card);
    document.body.appendChild(overlay);

    let closed = false;
    function close() {
      if (closed) return;
      closed = true;
      overlay.remove();
      document.removeEventListener('keydown', onKey);
    }
    function onKey(e) { if (e.key === 'Escape') close(); }
    overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
    document.addEventListener('keydown', onKey);

    return { close, el: card };
  }

  const WEEKDAY_LABELS = ['一', '二', '三', '四', '五', '六', '日']; // 1=周一
  const WEEKDAY_NAMES_FULL = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];

  // 三个 tab：'all' 显示全部；'once'/'weekly'/'monthly' 按类型过滤
  const TABS = [
    { key: 'all',     label: '全部' },
    { key: 'once',    label: '单次' },
    { key: 'weekly',  label: '周循环' },
    { key: 'monthly', label: '月循环' },
  ];

  function renderReminders(view) {
    view.innerHTML = '';

    let currentTab = 'all';
    let allItems = [];

    const banner = el('div', { class: 'reminders-banner', style: 'display:none' });
    const listWrap = el('div', { id: 'reminders-list', class: 'reminders-list' });
    view.appendChild(renderHeader());
    view.appendChild(banner);
    view.appendChild(listWrap);

    async function loadAndRender() {
      try {
        allItems = await api('GET', '/api/reminders') || [];
      } catch (e) {
        toast('加载提醒列表失败：' + e.message, 'err');
        allItems = [];
      }
      await refreshBanner();
      renderList();
    }

    async function refreshBanner() {
      try {
        const info = await api('GET', '/api/reminders/info') || {};
        if (info.paused && info.pause_until) {
          const until = new Date(info.pause_until);
          const untilStr = `${until.getMonth()+1}/${until.getDate()} ${String(until.getHours()).padStart(2,'0')}:${String(until.getMinutes()).padStart(2,'0')}`;
          banner.style.display = 'flex';
          banner.innerHTML = '';
          banner.appendChild(el('span', { class: 'banner-icon', text: '⏸' }));
          banner.appendChild(el('span', { class: 'banner-text', text: `今日提醒已暂停，恢复时间 ${untilStr}` }));
          const resumeBtn = el('button', { class: 'btn btn-sm', text: '立即恢复', onclick: resumePause });
          banner.appendChild(resumeBtn);
        } else {
          banner.style.display = 'none';
          banner.innerHTML = '';
        }
      } catch (e) {
        // ignore banner refresh failure
      }
    }

    async function resumePause() {
      try {
        await api('DELETE', '/api/reminders/pause');
        toast('已恢复提醒', 'ok');
        await loadAndRender();
      } catch (e) {
        toast('恢复失败：' + e.message, 'err');
      }
    }

    function renderList() {
      listWrap.innerHTML = '';
      const filtered = currentTab === 'all' ? allItems : allItems.filter(r => r.type === currentTab);
      const enabled = filtered.filter(r => r.enabled);
      const disabled = filtered.filter(r => !r.enabled);

      if (filtered.length === 0) {
        listWrap.appendChild(el('div', { class: 'empty-state' }, [
          el('div', { class: 'empty-icon', text: '🔔' }),
          el('div', { class: 'empty-title', text: '还没有提醒' }),
          el('div', { class: 'empty-desc', text: '点击右上角"新增"创建一条提醒。' }),
        ]));
        return;
      }

      if (enabled.length > 0) {
        listWrap.appendChild(el('div', { class: 'reminders-group-title', text: `启用中（${enabled.length}）` }));
        enabled.forEach(r => listWrap.appendChild(renderRow(r)));
      }
      if (disabled.length > 0) {
        listWrap.appendChild(el('div', { class: 'reminders-group-title', text: `已停用（${disabled.length}）` }));
        disabled.forEach(r => listWrap.appendChild(renderRow(r)));
      }
    }

    function renderHeader() {
      const tabBar = el('div', { class: 'tabs-bar' });
      TABS.forEach(t => {
        const btn = el('button', {
          class: 'tab-btn' + (t.key === currentTab ? ' active' : ''),
          text: t.label,
          onclick: () => {
            currentTab = t.key;
            Array.from(tabBar.children).forEach((b, i) => b.classList.toggle('active', TABS[i].key === currentTab));
            renderList();
          },
        });
        tabBar.appendChild(btn);
      });

      const newBtn = el('button', {
        class: 'btn btn-primary',
        text: '+ 新增提醒',
        onclick: () => openEditor(null, currentTab === 'all' ? 'weekly' : currentTab, loadAndRender),
      });

      return el('div', { class: 'reminders-header' }, [tabBar, newBtn]);
    }

    function renderRow(r) {
      const typeLabel = { once: '单次', weekly: '周循环', monthly: '月循环' }[r.type] || r.type;
      const typeClass = 'type-badge type-' + r.type;

      const meta = el('div', { class: 'reminder-meta' }, [
        el('span', { class: typeClass, text: typeLabel }),
        el('span', { class: 'reminder-schedule', text: humanSchedule(r) }),
        r.last_fired_at ? el('span', { class: 'reminder-fired', text: `已触发 ${r.fired_count} 次 · 上次 ${formatTime(r.last_fired_at)}` }) : null,
      ].filter(Boolean));

      const toggle = el('input', {
        type: 'checkbox',
        class: 'reminder-toggle',
        checked: r.enabled,
        title: r.enabled ? '点击停用' : '点击启用',
        onchange: async (e) => {
          try {
            await api('POST', `/api/reminders/${r.id}/toggle`);
            await loadAndRender();
          } catch (err) {
            toast('切换失败：' + err.message, 'err');
            e.target.checked = !e.target.checked;
          }
        },
      });

      const content = el('div', { class: 'reminder-content', text: r.content });

      const actions = el('div', { class: 'reminder-actions' }, [
        el('button', { class: 'btn btn-sm', text: '编辑', onclick: () => openEditor(r, r.type, loadAndRender) }),
        el('button', { class: 'btn btn-sm', text: '测试', title: '立即弹一次', onclick: () => doFire(r.id) }),
        el('button', { class: 'btn btn-sm btn-danger', text: '删除', onclick: () => doDelete(r.id) }),
      ]);

      const row = el('div', { class: 'reminder-row' + (r.enabled ? '' : ' disabled') }, [
        toggle,
        el('div', { class: 'reminder-main' }, [meta, content]),
        actions,
      ]);
      return row;
    }

    async function doDelete(id) {
      if (!window.confirm('确定删除这条提醒？')) return;
      try {
        await api('DELETE', `/api/reminders/${id}`);
        toast('已删除', 'ok');
        await loadAndRender();
      } catch (e) {
        toast('删除失败：' + e.message, 'err');
      }
    }

    async function doFire(id) {
      try {
        await api('POST', `/api/reminders/${id}/fire`);
        toast('已触发，看右下角', 'ok');
      } catch (e) {
        toast('触发失败：' + e.message, 'err');
      }
    }

    loadAndRender();
  }

  function humanSchedule(r) {
    if (r.type === 'once') {
      return (r.at || '').replace('T', ' ');
    }
    if (r.type === 'weekly') {
      const days = (r.weekdays || []).map(d => WEEKDAY_NAMES_FULL[d - 1]).join('、');
      return `${days} ${r.time || ''}`;
    }
    if (r.type === 'monthly') {
      return `每月 ${r.day_of_month} 号 ${r.time || ''}`;
    }
    return r.type;
  }

  function formatTime(rfc) {
    if (!rfc) return '';
    try {
      const d = new Date(rfc);
      const pad = n => String(n).padStart(2, '0');
      return `${d.getMonth() + 1}/${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
    } catch (_) { return rfc; }
  }

  // ---------- 编辑对话框 ----------
  function openEditor(existing, defaultType, onSaved) {
    const isNew = !existing;
    const initType = isNew ? defaultType : existing.type;

    // 字段 state
    const state = {
      type: initType,
      content: existing ? existing.content : '',
      at: existing && existing.at ? existing.at : defaultOnceAt(),
      weekdays: existing && existing.weekdays ? existing.weekdays.slice() : [1, 2, 3, 4, 5],
      time: existing && existing.time ? existing.time : '17:00',
      day: existing && existing.day_of_month ? existing.day_of_month : 1,
    };

    const typeSelector = el('div', { class: 'editor-type-tabs' });
    TABS.filter(t => t.key !== 'all').forEach(t => {
      const b = el('button', {
        class: 'tab-btn' + (t.key === state.type ? ' active' : ''),
        text: t.label,
        onclick: () => switchType(t.key),
      });
      b.dataset.type = t.key;
      typeSelector.appendChild(b);
    });

    const contentArea = el('div', { class: 'editor-fields' });
    const contentInput = el('textarea', { class: 'editor-content', rows: 3, placeholder: '提醒内容（≤ 200 字）' });
    contentInput.value = state.content;
    contentInput.maxLength = 200;

    const hint = el('div', { class: 'editor-hint muted' });

    function switchType(newType) {
      state.type = newType;
      Array.from(typeSelector.children).forEach(b => b.classList.toggle('active', b.dataset.type === newType));
      renderFields();
    }

    function renderFields() {
      contentArea.innerHTML = '';
      if (state.type === 'once') {
        const inAt = el('input', { type: 'datetime-local', class: 'editor-input' });
        inAt.value = state.at;
        inAt.onchange = () => { state.at = inAt.value; };
        contentArea.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '触发时间' }), inAt]));
        hint.textContent = '到点后弹窗一次，自动停用（保留记录）。';
      } else if (state.type === 'weekly') {
        const dayWrap = el('div', { class: 'weekday-picker' });
        WEEKDAY_LABELS.forEach((lab, i) => {
          const d = i + 1;
          const cb = el('label', { class: 'weekday-chip' + (state.weekdays.includes(d) ? ' active' : '') }, [
            (() => {
              const input = el('input', { type: 'checkbox' });
              input.checked = state.weekdays.includes(d);
              input.onchange = () => {
                if (input.checked && !state.weekdays.includes(d)) state.weekdays.push(d);
                else state.weekdays = state.weekdays.filter(x => x !== d);
                state.weekdays.sort((a, b) => a - b);
                cb.classList.toggle('active', input.checked);
              };
              return input;
            })(),
            el('span', { text: lab }),
          ]);
          dayWrap.appendChild(cb);
        });
        const quick = el('div', { class: 'weekday-quick muted' });
        ['工作日', '周末', '全选', '清空'].forEach(q => {
          const b = el('button', { class: 'btn btn-sm', text: q, onclick: () => {
            if (q === '工作日') state.weekdays = [1, 2, 3, 4, 5];
            else if (q === '周末') state.weekdays = [6, 7];
            else if (q === '全选') state.weekdays = [1, 2, 3, 4, 5, 6, 7];
            else state.weekdays = [];
            renderFields();
          }});
          quick.appendChild(b);
        });

        const inTime = el('input', { type: 'time', class: 'editor-input', value: state.time });
        inTime.onchange = () => { state.time = inTime.value; };

        contentArea.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '星期几' }), dayWrap, quick]));
        contentArea.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '时间' }), inTime]));
        hint.textContent = '勾选的每个星期几，到点弹窗。';
      } else if (state.type === 'monthly') {
        const inDay = el('input', { type: 'number', min: 1, max: 31, class: 'editor-input', value: String(state.day) });
        inDay.style.width = '80px';
        inDay.onchange = () => { state.day = Math.max(1, Math.min(31, parseInt(inDay.value, 10) || 1)); };

        const inTime = el('input', { type: 'time', class: 'editor-input', value: state.time });
        inTime.onchange = () => { state.time = inTime.value; };

        const row = el('div', { class: 'editor-row-inline' }, [
          el('label', { class: 'editor-label-inline' }, [el('span', { text: '每月' }), inDay, el('span', { text: '号' })]),
          el('label', { class: 'editor-label-inline' }, [el('span', { text: '时间' }), inTime]),
        ]);
        contentArea.appendChild(row);
        hint.textContent = '注意：31 号在 2 月会自动回退到当月最后一天。';
      }
      // 通用字段：提醒内容 + hint（每次 rebuild 都重新创建，确保存在）
      contentArea.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '提醒内容' }), contentInput]));
      contentArea.appendChild(hint);
    }

    renderFields();

    const body = el('div', { class: 'editor-body' }, [typeSelector, contentArea]);

    const saveBtn = el('button', { class: 'btn btn-primary', text: isNew ? '添加' : '保存', onclick: doSave });
    const cancelBtn = el('button', { class: 'btn', text: '取消', onclick: () => m.close() });
    const footer = el('div', { class: 'editor-footer' }, [cancelBtn, saveBtn]);

    const m = modal({
      title: isNew ? '新增提醒' : '编辑提醒',
      body,
      footer,
      width: 480,
    });

    async function doSave() {
      state.content = (contentInput.value || '').trim();
      const payload = buildPayload(state);
      if (!payload) return; // buildPayload 已经提示过错误

      try {
        if (isNew) {
          await api('POST', '/api/reminders', payload);
          toast('已添加', 'ok');
        } else {
          await api('PUT', `/api/reminders/${existing.id}`, payload);
          toast('已保存', 'ok');
        }
        m.close();
        onSaved && onSaved();
      } catch (e) {
        toast((isNew ? '添加失败：' : '保存失败：') + e.message, 'err');
      }
    }
  }

  function buildPayload(state) {
    if (!state.content) {
      toast('请填写提醒内容', 'err');
      return null;
    }
    if (state.content.length > 200) {
      toast('内容超过 200 字', 'err');
      return null;
    }
    if (state.type === 'once') {
      if (!state.at) {
        toast('请选择触发时间', 'err');
        return null;
      }
      return { type: 'once', content: state.content, at: state.at };
    }
    if (state.type === 'weekly') {
      if (state.weekdays.length === 0) {
        toast('请至少勾选一天', 'err');
        return null;
      }
      if (!state.time) {
        toast('请选择时间', 'err');
        return null;
      }
      return { type: 'weekly', content: state.content, weekdays: state.weekdays, time: state.time };
    }
    if (state.type === 'monthly') {
      if (!state.time) {
        toast('请选择时间', 'err');
        return null;
      }
      return { type: 'monthly', content: state.content, day_of_month: state.day, time: state.time };
    }
    return null;
  }

  function defaultOnceAt() {
    // 默认下一个整点
    const d = new Date();
    d.setMinutes(0, 0, 0);
    d.setHours(d.getHours() + 1);
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }

  Kairo.state.routes.reminders = renderReminders;
  Kairo.state.routeNames.reminders = '便笺提醒';
})();