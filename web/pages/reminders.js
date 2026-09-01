/* ===== web/pages/reminders.js =====
 * 便笺提醒编辑页：单次 / 周循环 / 月循环 / Cron 四种类型，统一编辑界面。
 * 公共能力：提前 N 分钟（lead_minutes）+ 触发动作（弹窗 / 打开网址 / 执行命令）。
 *
 * 数据流：
 *   GET    /api/reminders          →  列表
 *   POST   /api/reminders          →  新增
 *   PUT    /api/reminders/{id}     →  更新
 *   DELETE /api/reminders/{id}     →  删除
 *   POST   /api/reminders/{id}/toggle → 启用切换
 *   POST   /api/reminders/{id}/fire   → 立即触发（测试）
 *   POST   /api/format/cron-parse     → cron 表达式实时预览
 *
 * 触发逻辑在后端 reminder.Manager（事件驱动 time.AfterFunc，非轮询）。
 * 本页只负责 CRUD + UI，触发由后端按 action 分发到 popup / 浏览器 / 命令执行。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, escapeHtml, copyToClipboard } = Kairo.core;
  const { api } = Kairo.api;

  const { modal } = Kairo.overlays;

  const WEEKDAY_LABELS = ['一', '二', '三', '四', '五', '六', '日']; // 1=周一
  const WEEKDAY_NAMES_FULL = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];

  // 四个 tab：'all' 显示全部；'once'/'weekly'/'monthly'/'cron' 按类型过滤
  const TABS = [
    { key: 'all',     label: '全部' },
    { key: 'once',    label: '单次' },
    { key: 'weekly',  label: '周循环' },
    { key: 'monthly', label: '月循环' },
    { key: 'cron',    label: 'Cron' },
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
      // 列表 + 暂停 banner 两个请求无依赖，并行拉取，避免串行等待拖慢交互。
      await Promise.all([
        (async () => {
          try {
            allItems = await api('GET', '/api/reminders') || [];
          } catch (e) {
            toast('加载提醒列表失败：' + e.message, 'err');
            allItems = [];
          }
        })(),
        refreshBanner(),
      ]);
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
        // 原来用 🔔 emoji，在无 emoji 字体环境会显示成方框，改用 icons.js 里的 bell SVG。
        const bellIcon = (Kairo.icons && Kairo.icons.svg) ? Kairo.icons.svg('bell', 56) : null;
        if (bellIcon) bellIcon.classList.add('empty-icon-svg');
        listWrap.appendChild(el('div', { class: 'empty-state' }, [
          el('div', { class: 'empty-icon' }, bellIcon ? [bellIcon] : ['🔔']),
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
      const typeLabel = { once: '单次', weekly: '周循环', monthly: '月循环', cron: 'Cron' }[r.type] || r.type;
      const typeClass = 'type-badge type-' + r.type;

        const meta = el('div', { class: 'reminder-meta' }, [
        el('span', { class: typeClass, text: typeLabel }),
        el('span', { class: 'reminder-schedule', text: humanSchedule(r) }),
        r.source_note_id ? el('button', {
          class: 'reminder-source', type: 'button', text: '来自便笺',
          title: '打开便笺中心',
          onclick: function (ev) { ev.stopPropagation(); location.hash = '#/notes/list'; }
        }) : null,
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
    const lead = r.lead_minutes > 0 ? ` · 提前 ${r.lead_minutes} 分钟` : '';
    if (r.type === 'once') {
      return (r.at || '').replace('T', ' ') + lead;
    }
    if (r.type === 'weekly') {
      const days = (r.weekdays || []).map(d => WEEKDAY_NAMES_FULL[d - 1]).join('、');
      return `${days} ${r.time || ''}${lead}`;
    }
    if (r.type === 'monthly') {
      return `每月 ${r.day_of_month} 号 ${r.time || ''}${lead}`;
    }
    if (r.type === 'cron') {
      return `Cron: ${r.cron || ''}${lead}`;
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
  function openEditor(existing, defaultType, onSaved, preset) {
    const isNew = !existing;
    preset = preset || {};
    const initType = isNew ? defaultType : existing.type;
    const act = existing && existing.action ? existing.action : {};

    // 字段 state
    const state = {
      type: initType,
      content: existing ? existing.content : (preset.content || ''),
      at: existing && existing.at ? existing.at : defaultOnceAt(),
      weekdays: existing && existing.weekdays ? existing.weekdays.slice() : [1, 2, 3, 4, 5],
      time: existing && existing.time ? existing.time : '17:00',
      day: existing && existing.day_of_month ? existing.day_of_month : 1,
      cron: existing && existing.cron ? existing.cron : '0 9 * * 1-5',
      lead: existing && existing.lead_minutes ? existing.lead_minutes : 0,
      actionKind: act.kind || 'popup',
      actionURL: act.url || '',
      actionCmd: act.command || '',
      actionArgs: act.args ? formatCommandArgs(act.args) : '',
      actionWorkDir: act.work_dir || '',
      sourceNoteId: existing ? (existing.source_note_id || '') : (preset.sourceNoteId || ''),
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
    const typeFieldsWrap = el('div', { class: 'editor-type-fields' });
    const contentInput = el('textarea', { class: 'editor-content', rows: 3, placeholder: '提醒内容（≤ 200 字）' });
    contentInput.value = state.content;
    contentInput.maxLength = 200;
    const contentLabel = el('label', { class: 'editor-label' }, [el('span', { text: '提醒内容' }), contentInput]);
    const hint = el('div', { class: 'editor-hint muted' });

    // 提前 N 分钟（所有类型通用）
    const leadInput = el('input', { type: 'number', min: 0, max: 1440, class: 'editor-input', value: String(state.lead) });
    leadInput.style.width = '110px';
    leadInput.onchange = () => { state.lead = Math.max(0, Math.min(1440, parseInt(leadInput.value, 10) || 0)); };
    const leadRow = el('div', { class: 'editor-row-inline' }, [
      el('label', { class: 'editor-label-inline' }, [el('span', { text: '提前提醒' }), leadInput, el('span', { text: '分钟' })]),
      el('span', { class: 'editor-hint muted', text: '如「会前 10 分钟」填 10；0 = 到点提醒' }),
    ]);

    // 触发动作（所有类型通用）
    const actionWrap = el('div', { class: 'editor-action-wrap' });
    renderActionRow();

    contentArea.appendChild(typeFieldsWrap);
    if (state.sourceNoteId) {
      contentArea.appendChild(el('div', { class: 'editor-hint muted', text: '提醒保存的是便笺内容快照，之后改便笺不会自动改这条提醒。' }));
    }
    contentArea.appendChild(contentLabel);
    contentArea.appendChild(leadRow);
    contentArea.appendChild(actionWrap);
    contentArea.appendChild(hint);

    let dayChips = [];
    let dayWrapEl = null;
    let cronPreviewTimer = null;
    let cronPreviewGen = 0; // renderFields 重建时 +1，丢弃过期预览响应

    function updateWeeklyChips() {
      if (!dayChips.length) return;
      dayChips.forEach((cb, i) => {
        const d = i + 1;
        cb.classList.toggle('active', state.weekdays.includes(d));
        const inp = cb.querySelector('input[type=checkbox]');
        if (inp) inp.checked = state.weekdays.includes(d);
      });
    }

    function switchType(newType) {
      if (state.type === newType) return;
      state.type = newType;
      Array.from(typeSelector.children).forEach(b => b.classList.toggle('active', b.dataset.type === newType));
      renderFields();
    }

    function renderActionRow() {
      actionWrap.innerHTML = '';
      const sel = el('select', { class: 'editor-input' });
      sel.style.width = '140px';
      [['popup', '弹窗提醒'], ['url', '打开网址'], ['command', '执行命令']].forEach(([k, label]) => {
        const opt = el('option', { value: k, text: label });
        opt.selected = (state.actionKind === k);
        sel.appendChild(opt);
      });
      sel.onchange = () => { state.actionKind = sel.value; renderActionRow(); };
      actionWrap.appendChild(el('label', { class: 'editor-label-inline' }, [el('span', { text: '触发动作' }), sel]));

      if (state.actionKind === 'url') {
        const urlIn = el('input', { type: 'text', class: 'editor-input', placeholder: 'https://example.com/meeting' });
        urlIn.value = state.actionURL;
        urlIn.oninput = () => { state.actionURL = urlIn.value; };
        actionWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '网址（http/https）' }), urlIn]));
      } else if (state.actionKind === 'command') {
        const cmdIn = el('input', { type: 'text', class: 'editor-input', placeholder: '如 notepad.exe 或 /path/to/script.sh' });
        cmdIn.value = state.actionCmd;
        cmdIn.oninput = () => { state.actionCmd = cmdIn.value; };
        const argsIn = el('input', { type: 'text', class: 'editor-input', placeholder: '参数，空格分隔，可留空' });
        argsIn.value = state.actionArgs;
        argsIn.oninput = () => { state.actionArgs = argsIn.value; };
        const wdIn = el('input', { type: 'text', class: 'editor-input', placeholder: '工作目录，可留空' });
        wdIn.value = state.actionWorkDir;
        wdIn.oninput = () => { state.actionWorkDir = wdIn.value; };
        actionWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '可执行文件 / 脚本' }), cmdIn]));
        actionWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '参数' }), argsIn]));
        actionWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '工作目录' }), wdIn]));
      }
    }

    async function updateCronPreview() {
      const gen = cronPreviewGen;
      const previewEl = typeFieldsWrap.querySelector('.cron-preview');
      if (!previewEl) return;
      const v = (state.cron || '').trim();
      if (!v) {
        previewEl.textContent = '';
        previewEl.className = 'cron-preview muted';
        return;
      }
      try {
        const res = await api('POST', '/api/format/cron-parse', { input: v, loc: '' });
        if (gen !== cronPreviewGen) return; // 过期响应丢弃（切类型/重建后）
        const info = res && res.res ? res.res : {};
        if (!info.valid) {
          previewEl.textContent = '无效: ' + (info.error || '表达式格式错误');
          previewEl.className = 'cron-preview text-err';
          return;
        }
        const next = (info.next_runs || []).slice(0, 3).map(n => String(n.cn || '').replace(/ [A-Za-z]+$/, '')).join('  ·  ');
        previewEl.textContent = info.field_desc + (next ? '；接下来: ' + next : '');
        previewEl.className = 'cron-preview muted';
      } catch (e) {
        if (gen !== cronPreviewGen) return;
        previewEl.textContent = '预览失败：' + e.message;
        previewEl.className = 'cron-preview muted';
      }
    }

    function scheduleCronPreview() {
      clearTimeout(cronPreviewTimer);
      cronPreviewTimer = setTimeout(updateCronPreview, 400);
    }

    function renderFields() {
      cronPreviewGen++;
      typeFieldsWrap.innerHTML = '';
      if (state.type === 'once') {
        const inAt = el('input', { type: 'datetime-local', class: 'editor-input' });
        inAt.value = state.at;
        inAt.onchange = () => { state.at = inAt.value; };
        typeFieldsWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '触发时间' }), inAt]));
        hint.textContent = '到点后执行触发动作一次，自动停用（保留记录）。';
      } else if (state.type === 'weekly') {
        dayWrapEl = el('div', { class: 'weekday-picker' });
        dayChips = [];
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
          dayChips.push(cb);
          dayWrapEl.appendChild(cb);
        });
        const quick = el('div', { class: 'weekday-quick muted' });
        [['工作日', [1,2,3,4,5]], ['周末', [6,7]], ['全选', [1,2,3,4,5,6,7]], ['清空', []]].forEach(([q, days]) => {
          const b = el('button', { class: 'btn btn-sm', text: q, type: 'button', onclick: (e) => {
            e.preventDefault();
            state.weekdays = days.slice();
            updateWeeklyChips();
          }});
          quick.appendChild(b);
        });

        const inTime = el('input', { type: 'time', class: 'editor-input', value: state.time });
        inTime.onchange = () => { state.time = inTime.value; };

        typeFieldsWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '星期几' }), dayWrapEl, quick]));
        typeFieldsWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: '时间' }), inTime]));
        hint.textContent = '勾选的每个星期几，到点执行触发动作。';
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
        typeFieldsWrap.appendChild(row);
        hint.textContent = '注意：31 号在 2 月会自动回退到当月最后一天。';
      } else if (state.type === 'cron') {
        const cronIn = el('input', { type: 'text', class: 'editor-input mono', placeholder: '如 0 9 * * 1-5（工作日 09:00）或 @daily' });
        cronIn.value = state.cron;
        cronIn.oninput = () => { state.cron = cronIn.value; scheduleCronPreview(); };
        const preview = el('div', { class: 'cron-preview muted', text: '输入后自动预览…' });
        typeFieldsWrap.appendChild(el('label', { class: 'editor-label' }, [el('span', { text: 'Cron 表达式（5 段或 6 段）' }), cronIn, preview]));
        hint.textContent = '支持 5/6 段 cron 与 @hourly/@daily/@weekly/@monthly/@yearly；日期段与周段为「同时满足」。';
        scheduleCronPreview();
      }
    }

    renderFields();

    const body = el('div', { class: 'editor-body' }, [typeSelector, contentArea]);

    const saveBtn = el('button', { class: 'btn btn-primary', text: isNew ? '添加' : '保存', onclick: doSave });
    const cancelBtn = el('button', { class: 'btn', text: '取消', onclick: () => m.close() });
    const footer = el('div', { class: 'editor-footer' }, [cancelBtn, saveBtn]);

    const m = modal({
      title: isNew ? (preset.sourceNoteId ? '从便笺创建提醒' : '新增提醒') : '编辑提醒',
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
    const payload = { type: state.type, content: state.content, lead_minutes: state.lead || 0 };
    if (state.sourceNoteId) payload.source_note_id = state.sourceNoteId;
    if (state.type === 'once') {
      if (!state.at) {
        toast('请选择触发时间', 'err');
        return null;
      }
      payload.at = state.at;
    } else if (state.type === 'weekly') {
      if (state.weekdays.length === 0) {
        toast('请至少勾选一天', 'err');
        return null;
      }
      if (!state.time) {
        toast('请选择时间', 'err');
        return null;
      }
      payload.weekdays = state.weekdays;
      payload.time = state.time;
    } else if (state.type === 'monthly') {
      if (!state.time) {
        toast('请选择时间', 'err');
        return null;
      }
      payload.day_of_month = state.day;
      payload.time = state.time;
    } else if (state.type === 'cron') {
      const cron = (state.cron || '').trim();
      if (!cron) {
        toast('请填写 cron 表达式', 'err');
        return null;
      }
      payload.cron = cron;
    } else {
      return null;
    }

    const action = { kind: state.actionKind === 'popup' ? 'popup' : state.actionKind };
    if (state.actionKind === 'url') {
      const u = (state.actionURL || '').trim();
      if (!/^https?:\/\//i.test(u)) {
        toast('网址需以 http:// 或 https:// 开头', 'err');
        return null;
      }
      action.url = u;
    } else if (state.actionKind === 'command') {
      const cmd = (state.actionCmd || '').trim();
      if (!cmd) {
        toast('请填写可执行文件/脚本路径', 'err');
        return null;
      }
      action.command = cmd;
      let args;
      try {
        args = parseCommandArgs(state.actionArgs || '');
      } catch (e) {
        toast(e.message, 'err');
        return null;
      }
      if (args.length) action.args = args;
      if ((state.actionWorkDir || '').trim()) action.work_dir = state.actionWorkDir.trim();
    }
    payload.action = action;
    return payload;
  }

  // 将命令参数文本解析成 exec.Command 需要的参数数组。
  // 同时支持单/双引号包裹的空格路径，且不会把 Windows 路径里的
  // 反斜杠当成通用转义符。只有紧跟当前引号时，反斜杠才转义该引号。
  function parseCommandArgs(input) {
    const args = [];
    let current = '';
    let quote = '';
    let hasToken = false;
    for (let i = 0; i < input.length; i++) {
      const ch = input[i];
      if (quote) {
        if (ch === quote) {
          quote = '';
        } else if (ch === '\\' && input[i + 1] === quote) {
          current += quote;
          i++;
        } else {
          current += ch;
        }
        hasToken = true;
        continue;
      }
      if (ch === '"' || ch === "'") {
        quote = ch;
        hasToken = true;
      } else if (/\s/.test(ch)) {
        if (hasToken) {
          args.push(current);
          current = '';
          hasToken = false;
        }
      } else {
        current += ch;
        hasToken = true;
      }
    }
    if (quote) throw new Error('参数中的引号未闭合');
    if (hasToken) args.push(current);
    return args;
  }

  function formatCommandArgs(args) {
    return args.map((arg) => {
      const value = String(arg);
      if (value !== '' && !/[\s"']/.test(value)) return value;
      return '"' + value.replace(/"/g, '\\"') + '"';
    }).join(' ');
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
  Kairo.reminders = { render: renderReminders, openEditor, parseCommandArgs, formatCommandArgs };
})();
