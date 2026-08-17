/* ===== web/pages/tasks.js =====
 * 定时任务：按 cron 调度在本地执行 shell 命令。
 *
 * 典型场景：SVN 定时更新、Git 定时拉取 / 定时提交、本地脚本自动运行。
 *
 * 数据流：
 *   GET    /api/tasks            → 列表（含 next_run_at / running / last_status）
 *   POST   /api/tasks            → 新增
 *   PUT    /api/tasks/{id}       → 更新（enabled 字段被忽略，启停走 toggle）
 *   DELETE /api/tasks/{id}       → 删除
 *   POST   /api/tasks/{id}/toggle → 启停切换
 *   POST   /api/tasks/{id}/run   → 立即执行（异步）
 *   GET    /api/tasks/{id}/runs  → 运行历史（新的在前，最多 20 条）
 *
 * 调度在后端 schedtask.Manager（事件驱动 time.AfterFunc，非轮询）。
 * 页面只在有任务处于 running 时每 2s 轮询刷新，空闲零请求。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast } = Kairo.core;
  const { api } = Kairo.api;

  // ---- minimal modal（与 reminders.js 同款 inline 实现）----
  function modal({ title, body, footer, width }) {
    const overlay = el('div', { class: 'modal-overlay' });
    const card = el('div', { class: 'modal-card', role: 'dialog', 'aria-modal': 'true' });
    if (width) card.style.width = width + 'px';
    const titleEl = title ? el('div', { class: 'modal-title', text: title, id: 'modal-title-' + Date.now() }) : null;
    if (titleEl) {
      card.appendChild(titleEl);
      card.setAttribute('aria-labelledby', titleEl.id);
    }
    if (body) card.appendChild(body);
    if (footer) card.appendChild(footer);
    overlay.appendChild(card);
    document.body.appendChild(overlay);

    const focusable = card.querySelectorAll('button, input, textarea, [href], select');
    const firstFocusable = focusable[0];
    const lastFocusable = focusable[focusable.length - 1];
    if (firstFocusable) firstFocusable.focus();

    let closed = false;
    function close() {
      if (closed) return;
      closed = true;
      overlay.remove();
      document.removeEventListener('keydown', onKey);
    }
    function onKey(e) {
      if (e.key === 'Escape') close();
      if (e.key === 'Tab') {
        if (e.shiftKey && document.activeElement === firstFocusable) {
          e.preventDefault();
          lastFocusable.focus();
        } else if (!e.shiftKey && document.activeElement === lastFocusable) {
          e.preventDefault();
          firstFocusable.focus();
        }
      }
    }
    overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
    document.addEventListener('keydown', onKey);
    return { close, el: card };
  }

  // ---- 状态徽章 ----
  const STATUS_META = {
    success: { label: '成功',   cls: 'tag-ok' },
    failed:  { label: '失败',   cls: 'tag-err' },
    timeout: { label: '超时',   cls: 'tag-warn' },
    skipped: { label: '跳过',   cls: '' },
    running: { label: '运行中', cls: 'tag-busy' },
  };

  // ---- 调度预设：label 给人看，expr 落地存储 ----
  const SCHEDULE_PRESETS = [
    { label: '每分钟',           expr: '* * * * *' },
    { label: '每 5 分钟',        expr: '*/5 * * * *' },
    { label: '每 30 分钟',       expr: '*/30 * * * *' },
    { label: '每小时整点',        expr: '0 * * * *' },
    { label: '每天 09:00',       expr: '0 9 * * *' },
    { label: '每天 18:00',       expr: '0 18 * * *' },
    { label: '工作日 09:00',     expr: '0 9 * * 1-5' },
    { label: '每周一 09:00',     expr: '0 9 * * 1' },
    { label: '自定义 cron…',     expr: '' },
  ];

  // cronLabel：预设命中显示中文，否则原样显示表达式
  function cronLabel(expr) {
    const hit = SCHEDULE_PRESETS.find(p => p.expr && p.expr === expr);
    if (hit) return hit.label;
    const desc = { '@hourly': '每小时', '@daily': '每天 00:00', '@weekly': '每周日 00:00', '@monthly': '每月 1 号 00:00' }[expr];
    return desc || expr;
  }

  // ---- 任务模板：一键预填名称 + 命令（调度仍由用户选）----
  const TEMPLATES = [
    {
      key: 'svn', label: 'SVN 更新',
      name: 'SVN 定时更新',
      command: 'svn update',
      workdirHint: '填 SVN 工作副本路径，如 D:\\work\\project',
      cron: '*/30 * * * *',
    },
    {
      key: 'git-pull', label: 'Git 拉取',
      name: 'Git 定时拉取',
      command: 'git pull --ff-only',
      workdirHint: '填仓库本地路径，如 D:\\work\\repo',
      cron: '*/30 * * * *',
    },
    {
      key: 'git-commit', label: 'Git 提交推送',
      name: 'Git 定时提交推送',
      // 无变更时 commit 会非 0 退出 → 用 diff --cached --quiet 先判空，避免误报失败
      command: 'git add -A && (git diff --cached --quiet || git commit -m "auto: scheduled commit") && git push',
      workdirHint: '填仓库本地路径；注意定时提交会包含全部未提交改动',
      cron: '0 18 * * *',
    },
    {
      key: 'script', label: '自定义脚本',
      name: '自定义脚本',
      command: '',
      workdirHint: '脚本所在目录（可空）',
      cron: '0 9 * * *',
    },
  ];

  function renderTasks(view) {
    view.innerHTML = '';
    let allItems = [];
    let pollTimer = null;
    let disposed = false;

    const listWrap = el('div', { class: 'reminders-list' });
    view.appendChild(renderHeader());
    view.appendChild(listWrap);

    // 页面卸载检测：hash 变了就停止轮询（app.js 无页面级清理钩子）
    function isAlive() { return !disposed && location.hash.replace(/^#\//, '').split(/[?#]/)[0] === 'tasks'; }

    async function loadAndRender(silent) {
      try {
        allItems = await api('GET', '/api/tasks') || [];
      } catch (e) {
        if (!silent) toast('加载任务列表失败：' + e.message, 'err');
        allItems = allItems || [];
      }
      if (!isAlive()) return;
      renderList();
      schedulePoll();
    }

    // 有 running 任务时每 2s 静默刷新一次；没有就停（零请求空闲）
    function schedulePoll() {
      clearTimeout(pollTimer);
      if (!allItems.some(t => t.running)) return;
      pollTimer = setTimeout(() => { if (isAlive()) loadAndRender(true); }, 2000);
    }

    function renderHeader() {
      const desc = el('div', { class: 'card-desc', style: 'margin-bottom:0', text: '按 cron 调度在本地执行命令：SVN / Git 定时同步、脚本自动运行。写操作需要本机管理员（开启鉴权时）。' });
      const newBtn = el('button', {
        class: 'btn btn-primary',
        text: '+ 新增任务',
        onclick: () => openEditor(null, loadAndRender),
      });
      const left = el('div', { style: 'flex:1;min-width:0' }, [desc]);
      return el('div', { class: 'reminders-header' }, [left, newBtn]);
    }

    function renderList() {
      listWrap.innerHTML = '';
      const enabled = allItems.filter(t => t.enabled);
      const disabled = allItems.filter(t => !t.enabled);

      if (allItems.length === 0) {
        const timerIcon = (Kairo.icons && Kairo.icons.svg) ? Kairo.icons.svg('timer', 56) : null;
        if (timerIcon) timerIcon.classList.add('empty-icon-svg');
        listWrap.appendChild(el('div', { class: 'empty-state' }, [
          el('div', { class: 'empty-icon' }, timerIcon ? [timerIcon] : ['⏱']),
          el('div', { class: 'empty-title', text: '还没有定时任务' }),
          el('div', { class: 'empty-desc', text: '点击右上角"新增任务"，内置 SVN / Git / 脚本模板一键预填。' }),
        ]));
        return;
      }
      if (enabled.length > 0) {
        listWrap.appendChild(el('div', { class: 'reminders-group-title', text: `启用中（${enabled.length}）` }));
        enabled.forEach(t => listWrap.appendChild(renderRow(t)));
      }
      if (disabled.length > 0) {
        listWrap.appendChild(el('div', { class: 'reminders-group-title', text: `已停用（${disabled.length}）` }));
        disabled.forEach(t => listWrap.appendChild(renderRow(t)));
      }
    }

    function statusBadge(t) {
      if (t.running) return el('span', { class: 'tag tag-busy', text: '运行中' });
      const meta = STATUS_META[t.last_status];
      if (!meta) return null;
      return el('span', { class: 'tag ' + meta.cls, text: meta.label });
    }

    function renderRow(t) {
      const badge = statusBadge(t);

      const metaParts = [
        el('span', { class: 'type-badge type-cron', text: cronLabel(t.cron) }),
        el('span', { class: 'reminder-schedule mono', text: t.cron }),
      ];
      if (t.next_run_at) metaParts.push(el('span', { class: 'reminder-fired', text: '下次 ' + formatTime(t.next_run_at) }));
      if (badge) metaParts.push(badge);
      if (t.last_run_at && !t.running) {
        let lastTxt = `上次 ${formatTime(t.last_run_at)}`;
        if (t.last_duration_ms) lastTxt += ` · ${formatDur(t.last_duration_ms)}`;
        metaParts.push(el('span', { class: 'reminder-fired', text: lastTxt }));
      }
      if (t.last_error) {
        metaParts.push(el('span', { class: 'task-err-text', title: t.last_error, text: t.last_error }));
      }
      metaParts.push(el('span', { class: 'reminder-fired', text: `已运行 ${t.run_count || 0} 次` }));

      const toggle = el('input', {
        type: 'checkbox',
        class: 'reminder-toggle',
        checked: t.enabled,
        title: t.enabled ? '点击停用' : '点击启用',
        onchange: async (e) => {
          try {
            await api('POST', `/api/tasks/${t.id}/toggle`);
            await loadAndRender(true);
          } catch (err) {
            toast('切换失败：' + err.message, 'err');
            e.target.checked = !e.target.checked;
          }
        },
      });

      const cmdPreview = el('div', { class: 'task-cmd mono', title: t.command, text: t.command });
      if (t.work_dir) {
        cmdPreview.appendChild(el('span', { class: 'task-workdir', text: '  @ ' + t.work_dir }));
      }

      const runBtn = el('button', {
        class: 'btn btn-sm', text: t.running ? '运行中…' : '立即执行',
        onclick: () => doRun(t),
      });
      if (t.running) runBtn.disabled = true;

      const actions = el('div', { class: 'reminder-actions' }, [
        runBtn,
        el('button', { class: 'btn btn-sm', text: '日志', onclick: () => openRuns(t) }),
        el('button', { class: 'btn btn-sm', text: '编辑', onclick: () => openEditor(t, loadAndRender) }),
        el('button', { class: 'btn btn-sm btn-danger', text: '删除', onclick: () => doDelete(t) }),
      ]);

      return el('div', { class: 'reminder-row' + (t.enabled ? '' : ' disabled') }, [
        toggle,
        el('div', { class: 'reminder-main' }, [
          el('div', { class: 'reminder-meta' }, metaParts),
          el('div', { class: 'reminder-content', text: t.name }),
          cmdPreview,
        ]),
        actions,
      ]);
    }

    async function doRun(t) {
      try {
        await api('POST', `/api/tasks/${t.id}/run`);
        toast('已启动执行', 'ok');
        // 立即刷新一次让"运行中"状态亮起来，轮询由 renderList 接管
        setTimeout(() => { if (isAlive()) loadAndRender(true); }, 300);
      } catch (e) {
        toast('启动失败：' + e.message, 'err');
      }
    }

    async function doDelete(t) {
      if (!window.confirm(`确定删除任务「${t.name}」？\n运行历史会一起删除。`)) return;
      try {
        await api('DELETE', `/api/tasks/${t.id}`);
        toast('已删除', 'ok');
        await loadAndRender(true);
      } catch (e) {
        toast('删除失败：' + e.message, 'err');
      }
    }

    loadAndRender(false);
  }

  // ---------- 时间格式化 ----------
  function formatTime(rfc) {
    if (!rfc) return '';
    try {
      const d = new Date(rfc);
      const pad = n => String(n).padStart(2, '0');
      return `${d.getMonth() + 1}/${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
    } catch (_) { return rfc; }
  }
  function formatDur(ms) {
    if (ms < 1000) return ms + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    return Math.floor(ms / 60000) + 'm' + Math.round((ms % 60000) / 1000) + 's';
  }

  // ---------- 编辑对话框 ----------
  function openEditor(existing, onSaved) {
    const isNew = !existing;
    const state = {
      name: existing ? existing.name : '',
      command: existing ? existing.command : '',
      workDir: existing ? (existing.work_dir || '') : '',
      cron: existing ? existing.cron : '*/30 * * * *',
      timeoutSec: existing && existing.timeout_sec ? existing.timeout_sec : 300,
    };
    // 命中预设则选中预设，否则选"自定义"
    const presetHit = SCHEDULE_PRESETS.find(p => p.expr && p.expr === state.cron);
    state.preset = presetHit ? presetHit.expr : '';

    // 模板行（仅新增时显示）
    let templateRow = null;
    if (isNew) {
      templateRow = el('div', { class: 'task-templates' });
      TEMPLATES.forEach(tp => {
        templateRow.appendChild(el('button', {
          class: 'btn btn-sm', type: 'button', text: tp.label,
          title: tp.workdirHint,
          onclick: (e) => {
            e.preventDefault();
            state.name = tp.name;
            state.command = tp.command;
            state.cron = tp.cron;
            nameInput.value = state.name;
            cmdInput.value = state.command;
            cronInput.value = state.cron;
            presetSel.value = tp.cron;
            syncCronVisibility();
            refreshPreview();
          },
        }));
      });
      templateRow.appendChild(el('span', { class: 'muted', style: 'font-size:12px', text: '模板只预填，保存前都可改' }));
    }

    const nameInput = el('input', { type: 'text', class: 'editor-input', placeholder: '如：每天下班前拉取最新代码', maxlength: '60' });
    nameInput.value = state.name;

    const cmdInput = el('textarea', { class: 'editor-content mono', rows: 4, placeholder: 'shell 命令，可多行。Windows 走 cmd /C，macOS/Linux 走 sh -c' });
    cmdInput.value = state.command;

    const dirInput = el('input', { type: 'text', class: 'editor-input mono', placeholder: '工作目录（留空 = 工具运行目录）；git/svn 任务必填仓库路径' });
    dirInput.value = state.workDir;

    const presetSel = el('select', { class: 'editor-input' });
    SCHEDULE_PRESETS.forEach(p => presetSel.appendChild(el('option', { value: p.expr, text: p.label })));
    presetSel.value = state.preset;

    const cronInput = el('input', { type: 'text', class: 'editor-input mono', placeholder: '5 段 cron，如 */10 * * * *' });
    cronInput.value = state.cron;

    const previewBox = el('div', { class: 'task-cron-preview muted', text: '' });

    function syncCronVisibility() {
      cronInput.style.display = presetSel.value === '' ? '' : 'none';
      previewBox.style.display = '';
    }

    let previewReq = 0;
    async function refreshPreview() {
      const expr = (presetSel.value === '' ? cronInput.value : presetSel.value).trim();
      if (!expr) { previewBox.textContent = ''; return; }
      const my = ++previewReq;
      try {
        const r = await api('POST', '/api/format/cron-parse', { input: expr, loc: 'Local' });
        if (my !== previewReq) return;
        const res = r.res || {};
        if (!res.valid) {
          previewBox.textContent = '表达式不合法：' + (res.error || '');
          previewBox.classList.add('text-err');
          return;
        }
        previewBox.classList.remove('text-err');
        const nexts = (res.next_runs || []).slice(0, 3).map(x => x.cn).join('、');
        previewBox.textContent = nexts ? `未来 3 次：${nexts}` : '表达式合法';
      } catch (_) { /* 预览失败不打扰 */ }
    }

    presetSel.onchange = () => { syncCronVisibility(); refreshPreview(); };
    cronInput.addEventListener('input', refreshPreview);
    syncCronVisibility();
    refreshPreview();

    const timeoutInput = el('input', { type: 'number', min: '0', max: '3600', class: 'editor-input' });
    timeoutInput.style.width = '110px';
    timeoutInput.value = String(state.timeoutSec);

    const fields = el('div', { class: 'editor-fields' }, [
      el('label', { class: 'editor-label' }, [el('span', { text: '任务名称' }), nameInput]),
      el('label', { class: 'editor-label' }, [el('span', { text: '执行命令' }), cmdInput]),
      el('label', { class: 'editor-label' }, [el('span', { text: '工作目录' }), dirInput]),
      el('label', { class: 'editor-label' }, [el('span', { text: '调度' }), presetSel, cronInput, previewBox]),
      el('label', { class: 'editor-label' }, [
        el('span', { text: '超时（秒）' }),
        el('div', { class: 'editor-row-inline' }, [
          timeoutInput,
          el('span', { class: 'muted', text: '0 = 默认 300 秒；超时进程会被强制结束' }),
        ]),
      ]),
    ]);

    const bodyParts = [];
    if (templateRow) bodyParts.push(templateRow);
    bodyParts.push(fields);
    const body = el('div', { class: 'editor-body' }, bodyParts);

    const saveBtn = el('button', { class: 'btn btn-primary', text: isNew ? '添加' : '保存', onclick: doSave });
    const cancelBtn = el('button', { class: 'btn', text: '取消', onclick: () => m.close() });
    const footer = el('div', { class: 'editor-footer' }, [cancelBtn, saveBtn]);

    const m = modal({ title: isNew ? '新增定时任务' : '编辑定时任务', body, footer, width: 560 });

    async function doSave() {
      const cronExpr = (presetSel.value === '' ? cronInput.value : presetSel.value).trim();
      const payload = {
        name: nameInput.value.trim(),
        command: cmdInput.value,
        work_dir: dirInput.value.trim(),
        cron: cronExpr,
        timeout_sec: Math.max(0, Math.min(3600, parseInt(timeoutInput.value, 10) || 0)),
      };
      if (!payload.name) { toast('请填写任务名称', 'err'); return; }
      if (!payload.command.trim()) { toast('请填写执行命令', 'err'); return; }
      if (!payload.cron) { toast('请填写调度表达式', 'err'); return; }

      try {
        if (isNew) {
          await api('POST', '/api/tasks', payload);
          toast('已添加', 'ok');
        } else {
          await api('PUT', `/api/tasks/${existing.id}`, payload);
          toast('已保存', 'ok');
        }
        m.close();
        onSaved && onSaved(true);
      } catch (e) {
        toast((isNew ? '添加失败：' : '保存失败：') + e.message, 'err');
      }
    }
  }

  // ---------- 运行历史对话框 ----------
  async function openRuns(t) {
    const listBox = el('div', { class: 'task-runs-list' }, [
      el('div', { class: 'muted', text: '加载中…' }),
    ]);
    const body = el('div', { class: 'editor-body' }, [
      el('div', { class: 'muted', style: 'font-size:12.5px' }, [
        document.createTextNode('任务：'),
        el('span', { class: 'mono', text: t.name }),
        document.createTextNode('（最多保留最近 20 次）'),
      ]),
      listBox,
    ]);
    const closeBtn = el('button', { class: 'btn', text: '关闭', onclick: () => m.close() });
    const m = modal({ title: '运行历史', body, footer: el('div', { class: 'editor-footer' }, [closeBtn]), width: 640 });

    let runs = [];
    try {
      runs = await api('GET', `/api/tasks/${t.id}/runs`) || [];
    } catch (e) {
      listBox.innerHTML = '';
      listBox.appendChild(el('div', { class: 'text-err', text: '加载失败：' + e.message }));
      return;
    }
    listBox.innerHTML = '';
    if (runs.length === 0) {
      listBox.appendChild(el('div', { class: 'muted', text: '还没有运行记录' }));
      return;
    }
    runs.forEach(r => listBox.appendChild(renderRunRow(r)));
  }

  function renderRunRow(r) {
    const meta = STATUS_META[r.status] || { label: r.status, cls: '' };
    const head = el('div', { class: 'task-run-head' }, [
      el('span', { class: 'tag ' + meta.cls, text: meta.label }),
      el('span', { text: formatTime(r.started_at) }),
      el('span', { class: 'muted', text: formatDur(r.duration_ms || 0) }),
      el('span', { class: 'muted mono', text: 'exit ' + r.exit_code }),
      el('span', { class: 'type-badge', text: r.trigger === 'manual' ? '手动' : '调度' }),
    ]);
    const outputBox = el('pre', { class: 'task-run-output mono', text: r.output || '(无输出)' });
    outputBox.style.display = 'none';
    head.style.cursor = 'pointer';
    head.title = '点击展开 / 收起输出';
    head.onclick = () => {
      outputBox.style.display = outputBox.style.display === 'none' ? '' : 'none';
    };
    return el('div', { class: 'task-run-row' }, [head, outputBox]);
  }

  Kairo.pages.tasks = renderTasks;
  Kairo.state.routes.tasks = renderTasks;
  Kairo.state.routeNames.tasks = '定时任务';
  Kairo.state.routeSubs.tasks = 'SVN / Git / 脚本定时执行';
})();
