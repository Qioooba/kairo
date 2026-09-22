/* Database Workbench V2 — Oracle/MySQL SQL editor and Redis operations console. */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const { toast, escapeHtml, copyToClipboard, el } = Kairo.core;
  const { api, getPreference, putPreference, preferenceSaver } = Kairo.api;
  const LAST_SOURCE = 'kairo:database:last-source';
  const PREFS_KEY = 'kairo:database:workbench-prefs:v2';
  const HISTORY_KEY = 'kairo:database:sql-history:v1';
  const HISTORY_LIMIT = 20;
  const HISTORY_ENTRY_MAX = 2000;
  const BOOKMARK_LIMIT = 50;
  const BOOKMARK_SQL_MAX = 20000;
  const TAB_LIMIT = 6;
  const META_WIDTH_MIN = 220;
  const META_WIDTH_MAX = 720;
  const MAX_SQL_HIGHLIGHT_CHARS = 120000;
  const MAX_SQL_HIGHLIGHT_LINES = 2000;

  function isLargeSQL(text) {
    if (!text) return false;
    const maxChars = typeof MAX_SQL_HIGHLIGHT_CHARS === 'number' ? MAX_SQL_HIGHLIGHT_CHARS : 120000;
    const maxLines = typeof MAX_SQL_HIGHLIGHT_LINES === 'number' ? MAX_SQL_HIGHLIGHT_LINES : 2000;
    if (text.length > maxChars) return true;
    let lineCount = 0;
    for (let i = 0; i < text.length; i++) {
      if (text[i] === '\n') {
        lineCount++;
        if (lineCount > maxLines) return true;
      }
    }
    return false;
  }

  const DEFAULT_PREFS = {
    expandKey: 'Space',
    shortcuts: { run: 'Ctrl+Enter', cancel: 'Escape', grid: 'Alt+1', record: 'Alt+2', explain: 'Ctrl+Alt+P', objects: 'Alt+O', format: 'Ctrl+Shift+F' },
    gridRows: 25,
    snippets: [
      { key: 'sf', text: 'SELECT * FROM ', enabled: true },
      { key: 'sel', text: 'SELECT *\nFROM ${table}', enabled: true },
      { key: 'cnt', text: 'SELECT COUNT(*)\nFROM ${table}', enabled: true }
    ]
  };
  let persisted = { prefs: null, history: [], last_source: '', column_widths: {}, row_limits: {}, meta_collapsed: true };
  const persistPreference = preferenceSaver('database', 400);
  const state = {
    sources: [], source: null, rows: [], columns: [], controller: null, summary: null, cursor: 0,
    managing: false, workspaceToken: 0, lastSQL: '', lastMaxRows: 0, resultMode: 'grid',
    selectedRow: 0, selectedCol: 0, colSelected: -1, selectedCols: new Set(), lastColSelected: -1, localFilter: '', hiddenColumns: new Set(), columnWidths: {}, sort: null,
    prefs: normalizePrefs({}), lastError: null, inspectTab: 'fields', inspect: null, plan: [], gridReady: false,
    sessions: [], activeId: 0, redisKeyBase64: '', redisType: '', redisCursor: '0', redisNextCursor: '0', redisCursorHistory: [], redisOffset: 0, redisPageSize: 100, redisMembersHasNext: false,
    dirtyCells: {}, isEditMode: false,
    schemaTableCache: {}, schemaCategoryCache: {}, metadataCache: {}
  };
  let tabSeq = 0;
  let editorComposing = false;
  let tabTitleTimer = 0;
  const acState = { open: false, items: [], index: 0, start: 0, end: 0 };
  const GRID_ROW_H = 31;
  const GRID_HEAD_H = 36;
  const GRID_BUFFER = 12;
  let gridIndexCache = { rows: null, len: -1, filter: '', sort: null, indexes: null };
  const gridView = { indexes: [], visible: [], start: -1, end: -1, length: -1, head: null, tail: null, mid: null, raf: 0 };

  function h(v) { return escapeHtml(String(v == null ? '' : v)); }
  function q(id) { return document.getElementById(id); }
  function kindLabel(kind) { return kind === 'oracle' ? 'Oracle' : kind === 'mysql' ? 'MySQL' : 'Redis'; }
  function canWriteDatabase() {
    const user = Kairo.auth && Kairo.auth.getUser ? Kairo.auth.getUser() : '';
    const role = Kairo.auth && Kairo.auth.getRole ? Kairo.auth.getRole() : '';
    return !user || role === 'admin';
  }
  function objectTypeLabel(type) {
    const upper = String(type || '').toUpperCase();
    return upper === 'TABLE' ? '表' : upper === 'VIEW' ? '视图' : upper === 'PROCEDURE' ? '过程' : upper === 'FUNCTION' ? '函数' : upper === 'PACKAGE' ? '包' : '对象';
  }
  const DB_ACTION_ICONS = {
    play: '<path d="M8 5l11 7-11 7V5z" fill="currentColor" stroke="none"/>',
    stop: '<rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor" stroke="none"/>',
    lock: '<rect x="5" y="10" width="14" height="10" rx="2"/><path d="M8 10V7a4 4 0 018 0v3"/>',
    unlock: '<rect x="5" y="10" width="14" height="10" rx="2"/><path d="M8 10V7a4 4 0 017.6-1.7"/>',
    commit: '<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v5h8V4M8 20v-7h8v7"/>',
    rollback: '<path d="M8 7H4V3"/><path d="M4.5 7.5A8 8 0 112 13"/>',
    download: '<path d="M12 3v12M7 10l5 5 5-5"/><path d="M5 20h14"/>',
    plan: '<path d="M5 4v16M5 8h6M11 8v4h7M11 8v8h7"/><circle cx="19" cy="12" r="1.5"/><circle cx="19" cy="16" r="1.5"/>',
    format: '<path d="M4 6h16M4 12h11M4 18h16"/>',
    more: '<circle cx="5" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="19" cy="12" r="1.4" fill="currentColor" stroke="none"/>',
    grid: '<rect x="4" y="4" width="6" height="6" rx="1"/><rect x="14" y="4" width="6" height="6" rx="1"/><rect x="4" y="14" width="6" height="6" rx="1"/><rect x="14" y="14" width="6" height="6" rx="1"/>',
    star: '<path d="M12 3l2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-3-5.6 3 1.1-6.2L3 9.6l6.2-.9z"/>',
    trash: '<path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13M10 11v5M14 11v5"/>',
    database: '<ellipse cx="12" cy="5" rx="7" ry="3"/><path d="M5 5v7c0 1.7 3.1 3 7 3s7-1.3 7-3V5M5 12v7c0 1.7 3.1 3 7 3s7-1.3 7-3v-7"/>',
    folder: '<path d="M3 7h7l2 2h9v10H3z"/><path d="M3 7V5h7l2 2"/>',
    search: '<circle cx="11" cy="11" r="6"/><path d="M16 16l4 4"/>',
    insert: '<path d="M6 3h9l4 4v14H6z"/><path d="M15 3v5h5M9 14h6M12 11v6"/>',
    copy: '<rect x="8" y="8" width="11" height="12" rx="2"/><path d="M16 8V6a2 2 0 00-2-2H6a2 2 0 00-2 2v9a2 2 0 002 2h2"/>',
    refresh: '<path d="M20 6v5h-5M4 18v-5h5"/><path d="M6.1 9a7 7 0 0111.8-2.2L20 11M4 13l2.1 4.2A7 7 0 0017.9 15"/>',
    panel: '<path d="M4 5h16v14H4zM9 5v14"/><path d="M7 10l-2 2 2 2"/>',
    external: '<path d="M14 4h6v6M20 4l-9 9"/><path d="M18 13v6H5V6h6"/>',
    close: '<path d="M6 6l12 12M18 6L6 18"/>'
  };
  function actionIcon(name) {
    return '<svg class="db-action-icon" viewBox="0 0 24 24" width="14" height="14" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">' + (DB_ACTION_ICONS[name] || '') + '</svg>';
  }
  function newTransactionID() {
    const random = Math.random().toString(36).slice(2, 10);
    return 'dbtab-' + Date.now().toString(36) + '-' + random + '-' + (tabSeq + 1);
  }
  function copyDBText(value, successText) {
    let pending;
    try {
      pending = copyToClipboard(String(value == null ? '' : value));
    } catch (e) {
      toast('复制失败：' + (e && e.message ? e.message : '剪贴板不可用'), 'err');
      return;
    }
    Promise.resolve(pending).then(function () {
      toast(successText, 'ok');
    }).catch(function (e) {
      toast('复制失败：' + (e && e.message ? e.message : '剪贴板不可用'), 'err');
    });
  }
  function sess() { return state.sessions.find(function (s) { return s.id === state.activeId; }) || state.sessions[0] || null; }
  function effectiveSource() {
    const s = sess();
    if (s && s.sourceId) return state.sources.find(function (x) { return x.id === s.sourceId; }) || null;
    return state.source || null;
  }
  function isPlaceholderSchema(v) {
    if (!v) return true;
    const s = String(v).trim();
    return s === '' || s === '加载中…' || s === '加载中...' || s === '加载失败' || s.indexOf('加载中') !== -1;
  }
  function currentSchema() {
    const select = q('db-schema');
    const val = select && select.value ? select.value.trim() : '';
    if (!isPlaceholderSchema(val)) {
      return val;
    }
    const source = effectiveSource();
    if (source) {
      if (source.kind === 'oracle' && source.username) return source.username.toUpperCase();
      if (source.kind === 'mysql' && source.database) return source.database;
    }
    return '';
  }
  function sessionSourceState(s) {
    s = s || sess();
    if (!s || !s.sourceId) return { status: state.source ? 'ready' : 'removed', source: state.source || null };
    const found = state.sources.find(function (x) { return x.id === s.sourceId; }) || null;
    return found ? { status: 'ready', source: found } : { status: 'removed', source: null };
  }
  function bindSessionSource(s, source) {
    if (!s || !source) return;
    s.sourceId = source.id;
    s.sourceRef = Kairo.workbench && Kairo.workbench.resourceSession ? Kairo.workbench.resourceSession.snapshot(source) : { id: source.id, name: source.name, kind: source.kind };
  }
  function createSession(sql) {
    const defaultPageSize = (persisted.row_limits && state.source && Number(persisted.row_limits[state.source.id])) || (q('db-max-rows') && Number(q('db-max-rows').value)) || 20;
    return {
      id: ++tabSeq, sql: sql || '', rows: [], columns: [], summary: null, lastError: null,
      lastSQL: '', lastMaxRows: 0, page: 1, pageSize: defaultPageSize, resultMode: 'grid', selectedRow: 0, selectedCol: 0, colSelected: -1, selectedCols: new Set(), lastColSelected: -1, localFilter: '',
      hiddenColumns: new Set(), sort: null, plan: [], gridReady: false, controller: null,
      dirtyCells: {}, isEditMode: false, gridEditsStaged: false,
      transactionId: newTransactionID(), transactionPending: false,
      runSeq: 0, status: '就绪', sourceId: state.source ? state.source.id : '', sourceRef: state.source && Kairo.workbench && Kairo.workbench.resourceSession ? Kairo.workbench.resourceSession.snapshot(state.source) : null
    };
  }
  function bindSession(s) {
    if (!s) return;
    state.rows = s.rows; state.columns = s.columns; state.controller = s.controller;
    state.summary = s.summary; state.lastSQL = s.lastSQL; state.lastMaxRows = s.lastMaxRows;
    state.resultMode = s.resultMode; state.selectedRow = s.selectedRow; state.localFilter = s.localFilter;
    state.colSelected = s.colSelected == null ? -1 : s.colSelected;
    state.selectedCols = s.selectedCols || new Set();
    state.lastColSelected = s.lastColSelected == null ? -1 : s.lastColSelected;
    state.hiddenColumns = s.hiddenColumns; state.sort = s.sort; state.lastError = s.lastError;
    state.plan = s.plan; state.gridReady = false;
    state.dirtyCells = s.dirtyCells || {};
    state.isEditMode = !!s.isEditMode;
  }
  function replaceDirtyCells(next) {
    state.dirtyCells = next || {};
    const s = sess();
    if (s && s.type !== 'object') s.dirtyCells = state.dirtyCells;
  }
  function saveEditorSQL(expectedSessionId) {
    const s = sess(), ta = q('db-sql');
    if (!s) return;
    if (expectedSessionId && s.id !== expectedSessionId) return;
    if (ta && s.type !== 'object') s.sql = ta.value;
    s.resultMode = state.resultMode; s.selectedRow = state.selectedRow; s.colSelected = state.colSelected;
    s.selectedCols = state.selectedCols; s.lastColSelected = state.lastColSelected;
    s.localFilter = state.localFilter; s.hiddenColumns = state.hiddenColumns;
    s.sort = state.sort; s.lastError = state.lastError; s.plan = state.plan;
    s.rows = state.rows; s.columns = state.columns; s.summary = state.summary;
    s.lastSQL = state.lastSQL; s.lastMaxRows = state.lastMaxRows; s.controller = state.controller;
    s.dirtyCells = state.dirtyCells || {}; s.isEditMode = !!state.isEditMode;
    if (!s.sourceId && state.source) bindSessionSource(s, state.source);
  }
  function extractFirstSQL(sql) {
    if (!sql) return '';
    let text = String(sql).trim();
    if (!text) return '';
    // Strip comments
    text = text.replace(/\/\*[\s\S]*?\*\//g, ' ');
    text = text.replace(/--.*$/gm, ' ');
    text = text.trim();
    if (!text) return '';
    // Take first statement up to first semicolon
    const semiIdx = text.indexOf(';');
    const firstStmt = (semiIdx >= 0 ? text.slice(0, semiIdx) : text).replace(/\s+/g, ' ').trim();
    if (!firstStmt) return '';
    return firstStmt.length > 22 ? firstStmt.slice(0, 22) + '…' : firstStmt;
  }
  function tabTitle(s) {
    if (s.type === 'object') {
      return objectTypeLabel(s.objectType) + ' ' + s.objectName;
    }
    const firstSQL = extractFirstSQL(s.sql);
    return firstSQL || ('查询 ' + s.id);
  }
  function abortAllSessions() {
    (state.sessions || []).forEach(function (s) {
      if (s.controller) { try { s.controller.abort(); } catch (_) {} }
      s.controller = null;
    });
    state.controller = null;
  }
  function refreshActiveQueryUI(s) {
    s = s || sess();
    renderTabs();
    if (!s || s.id !== state.activeId) return;
    if (s.type === 'object') return;
    bindSession(s);
    const run = q('db-run'), cancel = q('db-cancel'), exp = q('db-export'), st = q('db-query-status');
    if (run) run.disabled = !!s.controller;
    if (cancel) cancel.disabled = !s.controller;
    if (exp) exp.disabled = !s.lastSQL || !!s.controller || !(s.rows && s.rows.length);
    if (st) st.textContent = s.status || '就绪';
    updateDatabasePager();
  }
  function hideTabContextMenu() {
    const old = q('db-tab-context-menu');
    if (old) old.remove();
    document.removeEventListener('click', hideTabContextMenu);
    document.removeEventListener('keydown', handleTabMenuEsc);
  }
  function handleTabMenuEsc(ev) {
    if (ev.key === 'Escape') hideTabContextMenu();
  }
  function showTabContextMenu(e, tabId) {
    hideTabContextMenu();
    const idx = state.sessions.findIndex(function (s) { return s.id === tabId; });
    if (idx < 0) return;
    const total = state.sessions.length;

    const menu = document.createElement('div');
    menu.id = 'db-tab-context-menu';
    menu.className = 'db-tab-context-menu card';
    menu.setAttribute('role', 'menu');

    const items = [
      { id: 'close-cur', text: '关闭当前页签', disabled: total <= 1, action: function () { closeSession(tabId); } },
      { id: 'close-left', text: '关闭左侧页签', disabled: idx <= 0, action: function () { closeSessionsLeft(tabId); } },
      { id: 'close-right', text: '关闭右侧页签', disabled: idx >= total - 1, action: function () { closeSessionsRight(tabId); } },
      { id: 'close-other', text: '关闭其他页签', disabled: total <= 1, action: function () { closeSessionsOther(tabId); } },
      { id: 'close-all', text: '一键关闭全部', disabled: false, action: function () { closeSessionsAll(); } }
    ];

    items.forEach(function (item) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'db-tab-menu-item' + (item.disabled ? ' disabled' : '');
      btn.textContent = item.text;
      if (item.disabled) btn.disabled = true;
      btn.onclick = function (ev) {
        ev.stopPropagation();
        hideTabContextMenu();
        if (!item.disabled) item.action();
      };
      menu.appendChild(btn);
    });

    document.body.appendChild(menu);
    const x = Math.min(e.clientX, (window.innerWidth || 1200) - (menu.offsetWidth || 150) - 8);
    const y = Math.min(e.clientY, (window.innerHeight || 800) - (menu.offsetHeight || 160) - 8);
    menu.style.left = Math.max(8, x) + 'px';
    menu.style.top = Math.max(8, y) + 'px';

    setTimeout(function () {
      document.addEventListener('click', hideTabContextMenu);
      document.addEventListener('keydown', handleTabMenuEsc);
    }, 10);
  }
  async function closeSessionsList(list, targetActiveId) {
    if (!list || !list.length) return;
    saveEditorSQL(state.activeId);
    const actualList = list.filter(function (s) { return state.sessions.some(function (item) { return item.id === s.id; }); });
    if (!actualList.length) return;
    if (actualList.some(function (s) { return s.transactionBusy; })) {
      return toast('部分待关闭页签事务处理中，请稍候再操作', 'warn');
    }
    const dirtySessions = actualList.filter(function (s) { return Object.keys(s.dirtyCells || {}).length > 0 || !!s.transactionPending; });
    if (dirtySessions.length > 0) {
      if (!confirm('待关闭的页签中存在未提交的事务或网格修改，确定关闭并回滚吗？')) return;
    }
    const successfullyClosed = [];
    const failedRollbacks = [];
    for (const s of actualList) {
      if (s.transactionPending && s.sourceId) {
        s.transactionBusy = true;
        try {
          await api('POST', '/api/database/transaction', { source_id: s.sourceId, session_id: s.transactionId, action: 'ROLLBACK' });
          s.transactionPending = false; s.gridEditsStaged = false; s.dirtyCells = {};
          successfullyClosed.push(s);
        } catch (err) {
          failedRollbacks.push({ session: s, error: err });
        } finally {
          s.transactionBusy = false;
        }
      } else {
        successfullyClosed.push(s);
      }
      if (s.controller) {
        try { s.controller.abort(); } catch (_) {}
      }
    }
    if (failedRollbacks.length > 0) {
      toast(failedRollbacks.length + ' 个页签回滚失败，已保留原页签与事务状态', 'err');
    }
    const removeSet = new Set(successfullyClosed.map(function (s) { return s.id; }));
    state.sessions = state.sessions.filter(function (s) { return !removeSet.has(s.id); });
    if (!state.sessions.length) {
      const fresh = createSession('');
      state.sessions.push(fresh);
      state.activeId = fresh.id;
      renderTabs();
      restoreSessionChrome(fresh);
      backupDBSessions({ delay: 500 });
      return;
    }
    let nextActive = state.sessions.find(function (s) { return s.id === targetActiveId; });
    if (!nextActive) {
      nextActive = state.sessions.find(function (s) { return s.id === state.activeId; }) || state.sessions[0];
    }
    state.activeId = nextActive.id;
    if (nextActive.type === 'object') {
      renderTabs();
      renderObjectViewer(nextActive);
    } else {
      hideObjectSession();
      renderTabs();
      restoreSessionChrome(nextActive);
    }
    backupDBSessions({ delay: 500 });
  }
  function closeSessionsLeft(tabId) {
    const idx = state.sessions.findIndex(function (s) { return s.id === tabId; });
    if (idx <= 0) return;
    closeSessionsList(state.sessions.slice(0, idx), tabId);
  }
  function closeSessionsRight(tabId) {
    const idx = state.sessions.findIndex(function (s) { return s.id === tabId; });
    if (idx < 0 || idx >= state.sessions.length - 1) return;
    closeSessionsList(state.sessions.slice(idx + 1), tabId);
  }
  function closeSessionsOther(tabId) {
    closeSessionsList(state.sessions.filter(function (s) { return s.id !== tabId; }), tabId);
  }
  function closeSessionsAll() {
    closeSessionsList(state.sessions.slice(), null);
  }
  function renderTabs() {
    const host = q('db-sql-tabs');
    if (!host) return;
    host.innerHTML = state.sessions.map(function (s) {
       if (s.type === 'object') {
         const tip = h((s.schema ? s.schema + '.' : '') + s.objectName + ' · ' + (s.objectType || '对象'));
         return '<button type="button" class="db-editor-tab db-object-tab' + (s.id === state.activeId ? ' active' : '') + '" data-tab="' + s.id + '" title="' + tip + '"><span class="db-object-type-chip" aria-hidden="true">' + h(objectTypeLabel(s.objectType)) + '</span><span class="db-tab-name">' + h(s.objectName) + '</span><span class="db-tab-close" data-close="' + s.id + '" title="关闭页签" aria-label="关闭页签">×</span></button>';
       }
       const src = s.sourceId ? (state.sources.find(function (x) { return x.id === s.sourceId; }) || null) : null;
       const srcName = src ? ' [' + src.name + ']' : (s.sourceRef && s.sourceRef.name ? ' [' + s.sourceRef.name + ' · 已删除]' : ' [未绑定]');
       const tip = h((s.sourceId ? ((src && src.name) || (s.sourceRef && s.sourceRef.name) || s.sourceId) + ' · ' : '') + (s.sql || tabTitle(s)));
       return '<button type="button" class="db-editor-tab' + (s.id === state.activeId ? ' active' : '') + (s.controller ? ' running' : '') + (!src && s.sourceId ? ' orphan' : '') + '" data-tab="' + s.id + '" title="' + tip + '"><span class="db-tab-dot" aria-hidden="true"></span><span class="db-tab-name">' + h(tabTitle(s)) + h(srcName) + '</span>' + (state.sessions.length > 1 ? '<span class="db-tab-close" data-close="' + s.id + '" title="关闭页签" aria-label="关闭页签">×</span>' : '') + '</button>';
    }).join('');
    host.querySelectorAll('[data-tab]').forEach(function (btn) {
      btn.onclick = function (e) {
        if (e.target.closest('[data-close]')) return;
        switchSession(Number(btn.dataset.tab));
      };
      btn.oncontextmenu = function (e) {
        e.preventDefault();
        e.stopPropagation();
        showTabContextMenu(e, Number(btn.dataset.tab));
      };
    });
    host.querySelectorAll('[data-close]').forEach(function (btn) {
      btn.onclick = function (e) {
        e.preventDefault(); e.stopPropagation();
        closeSession(Number(btn.dataset.close));
      };
    });
    host.ondblclick = function (e) {
      if (e.target.closest('.db-editor-tab') || e.target.closest('button')) return;
      e.preventDefault();
      e.stopPropagation();
      addSession();
    };
    const bar = host.parentElement;
    if (bar && bar.dataset.dbTabsDblBound !== '1') {
      bar.dataset.dbTabsDblBound = '1';
      bar.addEventListener('dblclick', function (e) {
        if (e.target.closest('.db-editor-tab') || e.target.closest('button') || e.target.closest('.db-dialect')) return;
        e.preventDefault();
        addSession();
      });
    }
  }
  function addSession() {
    if (state.sessions.length >= 50) {
      toast('已达到最大页签上限 (50 个)', 'warn');
      return;
    }
    saveEditorSQL(state.activeId);
    const s = createSession('');
    state.sessions.push(s);
    switchSession(s.id);
    backupDBSessions({ delay: 500 });
  }
  function switchSession(id) {
    if (id === state.activeId) return;
    hideComplete();
    saveEditorSQL(state.activeId);
    const s = state.sessions.find(function (x) { return x.id === id; });
    if (!s) return;
    cancelGridPaint();
    state.activeId = id;
    renderTabs();
    if (s.type === 'object') {
      renderObjectViewer(s);
      return;
    }
    hideObjectSession();
    // #13：页签保存自己的数据源；切换到不同类型时必须重建可见工作区
    const found = s.sourceId ? state.sources.find(function (x) { return x.id === s.sourceId; }) : null;
    if (found && (!state.source || state.source.id !== found.id)) {
      state.source = found;
      const sel = q('db-source');
      if (sel) sel.value = found.id;
      renderWorkspace(true);
      return;
    }
    restoreSessionChrome(s);
    backupDBSessions({ delay: 1000 });
  }
  async function closeSession(id) {
    if (state.sessions.length <= 1) return;
    let index = state.sessions.findIndex(function (x) { return x.id === id; });
    if (index < 0) return;
    const dying = state.sessions[index];
    if (dying.transactionBusy) return toast('事务操作进行中，请稍后关闭页签', 'warn');
    if (dying.controller) { dying.controller.abort(); return toast('正在取消查询，请查询结束后再关闭页签', 'info'); }
    if (id === state.activeId) saveEditorSQL(id);
    const dirtyCount = Object.keys(dying.dirtyCells || {}).length;
    const hasTransaction = !!dying.transactionPending;
    if ((dirtyCount || hasTransaction) && !confirm('关闭该页签将回滚未提交事务' + (dirtyCount ? '并放弃 ' + dirtyCount + ' 处网格修改' : '') + '，确定继续吗？')) return;
    if (hasTransaction && dying.sourceId) {
      dying.transactionBusy = true;
      try {
        await api('POST', '/api/database/transaction', { source_id: dying.sourceId, session_id: dying.transactionId, action: 'ROLLBACK' });
        dying.transactionPending = false; dying.gridEditsStaged = false; dying.dirtyCells = {};
      } catch (error) { toast('回滚失败，已保留页签和修改：' + error.message, 'err'); return; }
      finally { dying.transactionBusy = false; updateTransactionControls(); }
    }
    if (dying.controller) { try { dying.controller.abort(); } catch (_) {} }
    index = state.sessions.indexOf(dying);
    if (index < 0 || state.sessions.length <= 1) return;
    state.sessions.splice(index, 1);
    const next = state.sessions[Math.min(index, state.sessions.length - 1)];
    state.activeId = next.id;
    if (next.type === 'object') {
      renderTabs();
      renderObjectViewer(next);
    } else {
      hideObjectSession();
      const nextSource = next.sourceId ? state.sources.find(function (x) { return x.id === next.sourceId; }) : null;
      if (nextSource && (!state.source || state.source.id !== nextSource.id)) {
        state.source = nextSource;
        const select = q('db-source');
        if (select) select.value = nextSource.id;
        renderWorkspace(true);
      } else {
        renderTabs();
        restoreSessionChrome(next);
      }
    }
    backupDBSessions({ delay: 500 });
  }
  function hideObjectSession() {
    const viewer = q('db-object-viewer');
    if (viewer) viewer.hidden = true;
    const sqlPanel = q('db-sql-panel') || q('db-editor-body');
    const resultsSec = q('db-results-section');
    if (sqlPanel) sqlPanel.hidden = false;
    if (resultsSec) resultsSec.hidden = false;
  }
  function openObjectTab(schema, object, type) {
    if (!schema || schema === '加载中…' || schema === '加载失败') {
      schema = currentSchema();
    }
    if (isPlaceholderSchema(schema)) {
      schema = currentSchema();
    }
    if (isPlaceholderSchema(schema)) {
      const src = effectiveSource();
      if (src && src.kind === 'oracle' && src.username) schema = src.username.toUpperCase();
      else if (src && src.kind === 'mysql' && src.database) schema = src.database;
      else schema = '';
    }
    let existing = state.sessions.find(function (s) {
      return s.type === 'object' && s.schema === schema && s.objectName === object;
    });
    if (existing) {
      switchSession(existing.id);
      return;
    }
    saveEditorSQL();
    const s = {
      id: ++tabSeq,
      type: 'object',
      schema: schema,
      objectName: object,
      objectType: (type || 'TABLE').toUpperCase(),
      inspectTab: 'fields',
      inspectData: null,
      inspectLoading: false,
      inspectError: null,
      fieldFilter: '',
      sourceId: state.source ? state.source.id : '',
      sourceRef: state.source && Kairo.workbench && Kairo.workbench.resourceSession ? Kairo.workbench.resourceSession.snapshot(state.source) : null
    };
    state.sessions.push(s);
    switchSession(s.id);
    loadObjectTabInspect(s);
  }
  async function loadObjectTabInspect(s, refresh) {
    s.inspectLoading = true;
    s.inspectError = null;
    renderTabs();
    if (state.activeId === s.id) renderObjectViewer(s);
    try {
      const source = s.sourceId ? (state.sources.find(x => x.id === s.sourceId) || state.source) : state.source;
      if (!source) throw new Error('未选择数据源');
      if (!s.schema || s.schema === '加载中…' || s.schema === '加载失败') {
        s.schema = currentSchema();
      }
      if (isPlaceholderSchema(s.schema)) {
        if (source.kind === 'oracle' && source.username) s.schema = source.username.toUpperCase();
        else if (source.kind === 'mysql' && source.database) s.schema = source.database;
        else s.schema = '';
      }
      const data = await api('GET', '/api/database/metadata/inspect?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(s.schema) + '&object=' + encodeURIComponent(s.objectName) + '&type=' + encodeURIComponent(s.objectType || '') + (refresh ? '&refresh=1' : ''));
      s.inspectData = data.inspect || {};
      s.inspectLoading = false;
    } catch (err) {
      s.inspectLoading = false;
      s.inspectError = err.message || '加载详情失败';
    }
    renderTabs();
    if (state.activeId === s.id) renderObjectViewer(s);
  }
  function renderObjectViewer(s) {
    const viewer = q('db-object-viewer');
    if (!viewer) return;
    viewer.hidden = false;
    const sqlPanel = q('db-sql-panel') || q('db-editor-body');
    const resultsSec = q('db-results-section');
    if (sqlPanel) sqlPanel.hidden = true;
    if (resultsSec) resultsSec.hidden = true;

    if (s.inspectLoading) {
      viewer.innerHTML = '<div class="db-obj-loading"><span class="spinner"></span><span>正在读取 ' + h(s.objectName) + ' 详情…</span></div>';
      return;
    }
    if (s.inspectError) {
      viewer.innerHTML = '<div class="db-obj-error">' + inlineError('读取失败', s.inspectError) + '<button class="btn btn-xs" id="db-obj-retry">重试</button></div>';
      const retry = q('db-obj-retry');
      if (retry) retry.onclick = () => loadObjectTabInspect(s, true);
      return;
    }
    const info = s.inspectData || {};
    const fields = info.fields || [];
    const indexes = info.indexes || [];
    const constraints = info.constraints || [];
    const activeTab = s.inspectTab || 'fields';

    let subTabNav = '<div class="db-obj-tabs">' +
      '<button class="db-obj-tab' + (activeTab === 'fields' ? ' active' : '') + '" data-otab="fields">字段 (' + fields.length + ')</button>' +
      '<button class="db-obj-tab' + (activeTab === 'indexes' ? ' active' : '') + '" data-otab="indexes">索引 (' + indexes.length + ')</button>' +
      '<button class="db-obj-tab' + (activeTab === 'constraints' ? ' active' : '') + '" data-otab="constraints">约束 (' + constraints.length + ')</button>' +
      '<button class="db-obj-tab' + (activeTab === 'ddl' ? ' active' : '') + '" data-otab="ddl">DDL 定义</button>' +
      '</div>';

    let headerActions = '<div class="db-obj-actions">' +
      '<button class="btn btn-xs btn-primary" id="db-obj-action-query">' + actionIcon('search') + '<span>查询数据</span></button>' +
      '<button class="btn btn-xs" id="db-obj-action-insert">' + actionIcon('insert') + '<span>INSERT 模板</span></button>' +
      '<button class="btn btn-xs" id="db-obj-action-copy-ddl">' + actionIcon('copy') + '<span>复制 DDL</span></button>' +
      '<button class="btn btn-xs" id="db-obj-action-refresh">' + actionIcon('refresh') + '<span>刷新</span></button>' +
      '</div>';

    let bodyHTML = '';
    if (activeTab === 'fields') {
      bodyHTML = '<div class="db-obj-field-tools">' +
        '<input type="search" id="db-obj-field-filter" placeholder="搜索字段名、类型或注释..." class="editor-input" value="' + h(s.fieldFilter || '') + '">' +
        '<button class="btn btn-xs" id="db-obj-copy-cols">复制所有列名</button>' +
        '</div>' +
        '<div class="db-obj-table-wrap"><table class="table db-obj-table" style="width:1078px"><colgroup><col style="width:44px"><col style="width:180px"><col style="width:170px"><col style="width:84px"><col style="width:100px"><col style="width:180px"><col style="width:320px"></colgroup>' +
        '<thead><tr><th style="width:40px">#</th><th>字段名</th><th>数据类型</th><th>主键</th><th>允许为空</th><th>默认值</th><th>注释 (Comment)</th></tr></thead>' +
        '<tbody>' +
        fields.filter(f => {
          if (!s.fieldFilter) return true;
          const q = s.fieldFilter.toLowerCase();
          return String(f.name || '').toLowerCase().includes(q) ||
                 String(f.definition || f.data_type || '').toLowerCase().includes(q) ||
                 String(f.comment || '').toLowerCase().includes(q);
        }).map((f, i) => {
          return '<tr>' +
            '<td class="num">' + (i + 1) + '</td>' +
            '<td class="db-field-name"><strong>' + h(f.name) + '</strong></td>' +
            '<td class="mono">' + h(f.definition || f.data_type || '') + '</td>' +
            '<td>' + (f.primary_key ? '<span class="tag tag-ok">PK</span>' : '-') + '</td>' +
            '<td>' + (f.nullable ? '<span class="hint">NULL</span>' : '<span class="tag tag-warn">NOT NULL</span>') + '</td>' +
            '<td class="mono hint">' + h(f.default_value || '-') + '</td>' +
            '<td class="db-field-comment">' + (f.comment ? '<span class="comment-text">' + h(f.comment) + '</span>' : '<span class="hint">-</span>') + '</td>' +
            '</tr>';
        }).join('') +
        '</tbody></table></div>';
    } else if (activeTab === 'indexes') {
      bodyHTML = '<div class="db-obj-table-wrap"><table class="table db-obj-table" style="width:100%;min-width:754px"><colgroup><col style="width:44px"><col style="width:240px"><col style="width:110px"><col style="width:360px"></colgroup>' +
        '<thead><tr><th style="width:40px">#</th><th>索引名</th><th>唯一性</th><th>包含列</th></tr></thead>' +
        '<tbody>' +
        (indexes.length ? indexes.map((idx, i) => '<tr><td class="num">' + (i + 1) + '</td><td><strong>' + h(idx.name) + '</strong>' + (idx.type && idx.type !== 'NORMAL' ? ' <small class="hint mono">(' + h(idx.type) + ')</small>' : '') + '</td><td>' + (idx.uniqueness === 'UNIQUE' ? '<span class="tag tag-ok">UNIQUE</span>' : '<span class="hint">NORMAL</span>') + '</td><td class="mono">' + h((idx.columns || []).join(', ')) + '</td></tr>').join('') : '<tr><td colspan="4" class="hint db-empty-td">无索引信息</td></tr>') +
        '</tbody></table></div>';
    } else if (activeTab === 'constraints') {
      bodyHTML = '<div class="db-obj-table-wrap"><table class="table db-obj-table" style="width:100%;min-width:804px"><colgroup><col style="width:44px"><col style="width:260px"><col style="width:140px"><col style="width:360px"></colgroup>' +
        '<thead><tr><th style="width:40px">#</th><th>约束名</th><th>约束类型</th><th>关联列 / 详情</th></tr></thead>' +
        '<tbody>' +
        (constraints.length ? constraints.map((c, i) => '<tr><td class="num">' + (i + 1) + '</td><td><strong>' + h(c.name) + '</strong></td><td><span class="tag">' + h(c.type) + '</span></td><td class="mono">' + h(c.columns || '-') + (c.detail ? '<div class="hint mono" style="font-size:11px;margin-top:2px">' + h(c.detail) + '</div>' : '') + '</td></tr>').join('') : '<tr><td colspan="4" class="hint db-empty-td">无约束信息</td></tr>') +
        '</tbody></table></div>';
    } else if (activeTab === 'ddl') {
      const rawDDL = info.ddl || info.source_text || info.ddl_error || '-- 无 DDL';
      viewer._rawDDL = rawDDL;
      const formatted = formatSQL(rawDDL);
      const highlighted = highlightSQL(formatted);
      bodyHTML = '<div class="db-obj-ddl-tools">' +
        '<button class="btn btn-xs" id="db-obj-ddl-copy">复制 DDL</button>' +
        '</div>' +
        '<pre class="db-obj-ddl-code mono" data-raw-ddl="' + h(rawDDL) + '">' + highlighted + '</pre>';
    }

    viewer.innerHTML = '<div class="db-obj-header">' +
      '<div class="db-obj-title">' +
      '<span class="db-obj-kind-tag">' + h(s.objectType || '对象') + '</span>' +
      '<h3>' + h((s.schema ? s.schema + '.' : '') + s.objectName) + '</h3>' +
      '</div>' +
      headerActions +
      '</div>' +
      subTabNav +
      '<div class="db-obj-body">' + bodyHTML + '</div>';

    viewer.querySelectorAll('[data-otab]').forEach(b => {
      b.onclick = () => {
        s.inspectTab = b.dataset.otab;
        renderObjectViewer(s);
      };
    });
    const filterInp = q('db-obj-field-filter');
    if (filterInp) {
      filterInp.oninput = debounce(function () {
        s.fieldFilter = this.value.trim();
        renderObjectViewer(s);
      }, 150);
    }
    const copyCols = q('db-obj-copy-cols');
    if (copyCols) {
      copyCols.onclick = () => {
        const names = fields.map(f => f.name).join(', ');
        copyDBText(names, '已复制 ' + fields.length + ' 个列名');
      };
    }
    const btnQuery = q('db-obj-action-query');
    if (btnQuery) {
      btnQuery.onclick = () => {
        let sch = s.schema;
        if (isPlaceholderSchema(sch)) sch = currentSchema();
        if (isPlaceholderSchema(sch)) sch = '';
        const sql = 'SELECT *\nFROM ' + (sch ? quoteIdentifier(sch) + '.' : '') + quoteIdentifier(s.objectName);
        const querySess = createSession(sql);
        state.sessions.push(querySess);
        switchSession(querySess.id);
        runQuery();
      };
    }
    const btnInsert = q('db-obj-action-insert');
    if (btnInsert) {
      btnInsert.onclick = () => {
        insertObjectSQL(s.schema, s.objectName, s.objectType);
      };
    }
    const btnCopyDDL = q('db-obj-action-copy-ddl') || q('db-obj-ddl-copy');
    if (btnCopyDDL) {
      btnCopyDDL.onclick = () => {
        const raw = (info.ddl || info.source_text || '');
        copyDBText(formatSQL(raw), '已复制格式化 DDL');
      };
    }
    const btnRefresh = q('db-obj-action-refresh');
    if (btnRefresh) {
      btnRefresh.onclick = () => loadObjectTabInspect(s, true);
    }
  }
  function restoreSessionChrome(s) {
    if (s && s.type === 'object') {
      renderObjectViewer(s);
      return;
    }
    if (s) bindSession(s);
    hideObjectSession();
    const ta = q('db-sql');
    if (ta) ta.value = (s && s.sql) || '';
    syncSQLEditor();
    const filter = q('db-result-filter');
    if (filter) filter.value = (s && s.localFilter) || '';
    ['grid', 'record', 'plan'].forEach(function (name) {
      const btn = q('db-view-' + name);
      if (btn) {
        const active = (s && s.resultMode) === name;
        btn.classList.toggle('active', active);
        btn.setAttribute('aria-selected', active ? 'true' : 'false');
      }
    });
    if (s && s.lastError) showQueryMessage('error', s.lastError.title, s.lastError.message, s.lastError.sql);
    else hideQueryMessage();
    updateTransactionControls();
    refreshActiveQueryUI(s);
    renderResult();
  }
  function normalizePrefs(x) {
    x = x && typeof x === 'object' ? x : {};
    const shortcuts = Object.assign({}, DEFAULT_PREFS.shortcuts, x.shortcuts || {});
    // V2 migration: Ctrl+Shift+E conflicts with common Windows Chinese IMEs.
    if (shortcuts.explain === 'Ctrl+Shift+E') shortcuts.explain = DEFAULT_PREFS.shortcuts.explain;
    let gridRows = Number(x.gridRows);
    if (!Number.isFinite(gridRows) || gridRows <= 0) {
      gridRows = 25;
    } else if (x.version !== 2 && gridRows === 16) {
      gridRows = 25;
    }
    return {
      version: 2,
      expandKey: ['Space', 'Tab', 'Enter'].includes(x.expandKey) ? x.expandKey : 'Space',
      shortcuts: shortcuts,
      gridRows: Math.max(6, Math.min(100, gridRows)),
      snippets: Array.isArray(x.snippets) ? x.snippets : DEFAULT_PREFS.snippets.map(v => Object.assign({}, v))
    };
  }
  function legacyPrefs() {
    try { return normalizePrefs(JSON.parse(localStorage.getItem(PREFS_KEY) || '{}')); }
    catch (_) { return normalizePrefs({}); }
  }
  function savePersisted() { persistPreference(persisted); }
  function savePrefs() { persisted.prefs = state.prefs; savePersisted(); }
  function loadHistory() {
    return Array.isArray(persisted.history) ? persisted.history : [];
  }
  Kairo.database = Kairo.database || {};
  Kairo.database.getHistory = function () {
    return Array.isArray(persisted.history) ? persisted.history.slice() : [];
  };
  Kairo.database.isLargeSQL = isLargeSQL;
  Kairo.database.MAX_SQL_HIGHLIGHT_CHARS = MAX_SQL_HIGHLIGHT_CHARS;
  Kairo.database.MAX_SQL_HIGHLIGHT_LINES = MAX_SQL_HIGHLIGHT_LINES;
  function pushHistory(sql) {
    const text = String(sql || '').trim();
    if (!text || text.length > HISTORY_ENTRY_MAX) return;
    const items = loadHistory().filter(x => x !== text);
    items.unshift(text);
    persisted.history = items.slice(0, HISTORY_LIMIT);
    savePersisted();
    refreshHistorySelect();
    if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.addHistory === 'function') {
      Kairo.databaseFeatures.addHistory({ sql: text, status: 'success' });
    }
  }
  function refreshHistorySelect() {
    const select = q('db-history');
    if (!select) return;
    const items = loadHistory();
    const current = select.value;
    select.innerHTML = '<option value="">历史</option>' + items.map(function (sql, i) {
      return '<option value="' + i + '">' + h(sql.replace(/\s+/g, ' ').slice(0, 80)) + '</option>';
    }).join('');
    select.value = current && items[Number(current)] ? current : '';
  }
  function loadColumnWidths() {
    state.columnWidths = Object.assign({}, (state.source && persisted.column_widths[state.source.id]) || {});
  }
  function saveColumnWidths() {
    if (!state.source) return;
    persisted.column_widths[state.source.id] = Object.assign({}, state.columnWidths);
    savePersisted();
  }

  async function loadDatabasePreference() {
    let legacyHistory = [];
    try { legacyHistory = JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]'); } catch (_) {}
    const legacy = {
      prefs: legacyPrefs(), history: Array.isArray(legacyHistory) ? legacyHistory.filter(x => typeof x === 'string' && x.length <= HISTORY_ENTRY_MAX).slice(0, HISTORY_LIMIT) : [],
      last_source: localStorage.getItem(LAST_SOURCE) || '', column_widths: {}, row_limits: {}
    };
    try {
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i), prefix = 'kairo:database:column-widths:';
        if (key && key.indexOf(prefix) === 0) {
          try { legacy.column_widths[key.slice(prefix.length)] = JSON.parse(localStorage.getItem(key) || '{}'); } catch (_) {}
        }
      }
      const remote = await getPreference('database');
      persisted = remote.exists && remote.value && typeof remote.value === 'object' ? remote.value : legacy;
      if (!remote.exists) await putPreference('database', persisted);
      const remove = [];
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key && key.indexOf('kairo:database:column-widths:') === 0) remove.push(key);
      }
      remove.forEach(key => localStorage.removeItem(key));
      localStorage.removeItem(PREFS_KEY); localStorage.removeItem(HISTORY_KEY); localStorage.removeItem(LAST_SOURCE);
    } catch (_) { persisted = legacy; }
    persisted.prefs = normalizePrefs(persisted.prefs);
    persisted.history = Array.isArray(persisted.history) ? persisted.history.filter(x => typeof x === 'string' && x.length <= HISTORY_ENTRY_MAX).slice(0, HISTORY_LIMIT) : [];
    persisted.last_source = String(persisted.last_source || '');
    persisted.column_widths = persisted.column_widths && typeof persisted.column_widths === 'object' ? persisted.column_widths : {};
    persisted.row_limits = persisted.row_limits && typeof persisted.row_limits === 'object' ? persisted.row_limits : {};
    persisted.bookmarks = normalizeBookmarks(persisted.bookmarks);
    persisted.meta_width = Math.max(META_WIDTH_MIN, Math.min(META_WIDTH_MAX, Number(persisted.meta_width) || 300));
    persisted.inspect_height = Math.max(140, Math.min(480, Number(persisted.inspect_height) || 220));
    persisted.meta_collapsed = persisted.meta_collapsed !== undefined ? Boolean(persisted.meta_collapsed) : true;
    state.prefs = persisted.prefs;
  }
  function cellText(v) {
    if (v == null) return 'NULL';
    if (typeof v === 'object') {
      if (v.kind === 'clob') return v.text !== undefined && v.text !== null ? String(v.text) : (v.display ? v.display : '(CLOB ' + formatBytes(v.bytes || 0) + ')');
      if (v.kind === 'blob') return v.display ? v.display : '(BLOB ' + formatBytes(v.bytes || 0) + ')';
      if (v.kind === 'text') return String(v.preview || '');
      if (v.kind === 'binary') return '[BINARY ' + (v.bytes || 0) + ' B]';
      return JSON.stringify(v);
    }
    return String(v);
  }

  function formatBytes(bytes) {
    if (!Number.isFinite(bytes) || bytes < 0) return '0 B';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / 1048576).toFixed(1) + ' MB';
  }

  function fmtCell(v, rowIdx, colIdx) {
    if (v == null) return '<span class="db-null">NULL</span>';
    if (typeof v === 'object') {
      if (v.kind === 'clob') {
        const sz = v.token ? (v.length != null ? (Number(v.length).toLocaleString() + ' 字符') : formatBytes(v.bytes || 0)) : (v.lazy ? '待加载' : formatBytes(v.bytes || 0));
        const lobLabel = v.database_type === 'NCLOB' ? 'NCLOB' : 'CLOB';
        const tokenCls = (v.token || v.lazy) ? ' db-lob-token' : '';
        const title = v.lazy ? '点击在线预览/流式加载完整 CLOB' : (v.token ? '点击查看/流式下载完整 CLOB (' + sz + ')' : '点击查看 CLOB 文本 (' + sz + ')');
        return '<button type="button" class="db-lob-badge db-lob-clob' + tokenCls + '" data-lob-type="clob" data-lob-row="' + rowIdx + '" data-lob-col="' + colIdx + '" title="' + h(title) + '">' + lobLabel + ' (' + sz + ')</button>';
      }
      if (v.kind === 'blob') {
        const sz = v.token ? formatBytes(v.length) : (v.lazy ? '待加载' : formatBytes(v.bytes || 0));
        const tokenCls = (v.token || v.lazy) ? ' db-lob-token' : '';
        const title = v.lazy ? '点击在线预览 Hex / 流式加载完整 BLOB' : (v.token ? '点击查看/流式下载完整 BLOB (' + sz + ')' : '点击查看十六进制检视器 (' + sz + ')');
        return '<button type="button" class="db-lob-badge db-lob-blob' + tokenCls + '" data-lob-type="blob" data-lob-row="' + rowIdx + '" data-lob-col="' + colIdx + '" title="' + h(title) + '">BLOB (' + sz + ')</button>';
      }
      if (v.kind === 'text') return h(v.preview) + '<span class="db-cut">(截断 ' + h(v.bytes) + ' B)</span>';
      if (v.kind === 'binary') return '<span class="db-binary">BINARY ' + h(v.bytes) + ' B</span>';
      return h(JSON.stringify(v));
    }
    return h(v);
  }

  function fmtRedisLabel(v) {
    if (v == null) return '';
    if (typeof v !== 'object') return String(v);
    if (v.kind === 'binary') return '[BINARY ' + (v.bytes || 0) + ' B] ' + (v.preview_base64 || '');
    if (v.kind === 'text') return String(v.preview || '') + '…';
    return JSON.stringify(v);
  }

  /* ---------- 数据库工作台高阶扩展能力 (LOB查看器 / 行内编辑 / 右键增强 / 会话防丢) ---------- */

  function cleanIdent(v) {
    if (v == null) return '';
    return String(v).trim().replace(/^[`"]+|[`"]+$/g, '');
  }

  function detectTableTarget() {
    const target = getGridContext();
    if (target && target.table) {
      let t = cleanIdent(target.table);
      if (t.indexOf('.') >= 0) {
        const parts = t.split('.');
        const s = cleanIdent(parts[0]);
        const tbl = cleanIdent(parts[1]);
        return { schema: s, table: tbl, fullTable: s + '.' + tbl };
      }
      const schema = target.schema || currentSchema();
      return { schema: schema, table: t, fullTable: schema ? schema + '.' + t : t };
    }
    const sql = (sess() && sess().lastSQL) || state.lastSQL || '';
    if (sql) {
      const m = sql.match(/\bFROM\s+([^\s,;()]+)/i);
      if (m && m[1]) {
        let raw = cleanIdent(m[1]);
        if (raw.indexOf('.') >= 0) {
          const parts = raw.split('.');
          const s = cleanIdent(parts[0]);
          const tbl = cleanIdent(parts[1]);
          return { schema: s, table: tbl, fullTable: s + '.' + tbl };
        }
        const s = currentSchema();
        return { schema: s, table: raw, fullTable: s ? s + '.' + raw : raw };
      }
    }
    const s = sess();
    if (s && s.objectName) {
      const raw = cleanIdent(s.objectName);
      const schema = currentSchema();
      return { schema: schema, table: raw, fullTable: schema ? schema + '.' + raw : raw };
    }
    return { schema: currentSchema(), table: 'TARGET_TABLE', fullTable: 'TARGET_TABLE' };
  }

  function detectTableName() {
    const target = detectTableTarget();
    return target && target.table ? target.table : 'TARGET_TABLE';
  }

  function sqlValueLiteral(val, dbType) {
    if (val === null || val === undefined) return 'NULL';
    if (typeof val === 'object') {
      if (val.kind === 'clob') return sqlValueLiteral(String(val.text || ''), dbType);
      if (val.kind === 'blob') {
        if (effectiveSource() && effectiveSource().kind === 'oracle') return "HEXTORAW('" + (val.hex || '') + "')";
        return "0x" + (val.hex || '');
      }
    }
    if (typeof val === 'number') return String(val);
    if (typeof val === 'boolean') return val ? '1' : '0';
    const str = String(val);
    if (effectiveSource() && effectiveSource().kind === 'mysql' && /[\\\u0000\r\n\u001a]/.test(str)) {
      const bytes = new TextEncoder().encode(str);
      const hex = Array.from(bytes, function (value) { return value.toString(16).padStart(2, '0'); }).join('');
      return "CONVERT(X'" + hex + "' USING utf8mb4)";
    }
    if (effectiveSource() && effectiveSource().kind === 'oracle' && /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}/.test(str)) {
      const dStr = str.replace('T', ' ').slice(0, 19);
      return "TO_DATE('" + dStr + "', 'YYYY-MM-DD HH24:MI:SS')";
    }
    return "'" + str.replace(/'/g, "''") + "'";
  }

  function copyRowAsInsert(rowIdx) {
    const row = state.rows[rowIdx];
    if (!row) return;
    const table = detectTableName();
    const cols = visibleColumns();
    const colNames = cols.map(i => cleanIdent(state.columns[i].name)).join(', ');
    const valLiterals = cols.map(i => {
      const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + i];
      const v = dirty ? dirty.newVal : row[i];
      return sqlValueLiteral(v, state.columns[i].database_type);
    }).join(', ');
    const insertSQL = 'INSERT INTO ' + table + ' (' + colNames + ') VALUES (' + valLiterals + ');';
    copyDBText(insertSQL, '已复制 INSERT 语句到剪贴板');
  }

  state.tablePKCache = state.tablePKCache || {};
  async function getTablePrimaryKeys(tableName) {
    const target = detectTableTarget();
    const source = effectiveSource();
    if (!source) return [];
    let schema = target.schema || currentSchema();
    let obj = (tableName && tableName !== 'TARGET_TABLE') ? tableName : target.table;
    if (!obj || obj === 'TARGET_TABLE') return [];
    if (obj.indexOf('.') >= 0) {
      const parts = obj.split('.');
      schema = cleanIdent(parts[0]);
      obj = cleanIdent(parts[1]);
    }
    const cacheKey = JSON.stringify([source.id, schema, obj]);
    if (state.tablePKCache[cacheKey]) return state.tablePKCache[cacheKey];
    try {
      const data = await api('GET', '/api/database/metadata/fields?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&object=' + encodeURIComponent(obj));
      const pks = (data.fields || []).filter(f => f.primary_key).map(f => f.name);
      state.tablePKCache[cacheKey] = pks;
      return pks;
    } catch (_) {
      return [];
    }
  }

  async function executeQuickQuery(sourceId, sql) {
    try {
      const res = await fetch('/api/database/query', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source_id: sourceId, sql: sql, max_rows: 1, fast: true })
      });
      if (!res.ok) return null;
      const text = await res.text();
      let cols = [];
      let rows = [];
      const lines = text.split('\n');
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i].trim();
        if (!line) continue;
        try {
          const ev = JSON.parse(line);
          if (ev.type === 'meta') cols = ev.columns || [];
          if (ev.type === 'rows' && ev.rows) rows.push(...ev.rows);
        } catch (_) {}
      }
      return { cols, rows };
    } catch (_) {
      return null;
    }
  }

  async function buildWhereClause(row, pks, tableName, rowIdx) {
    tableName = tableName || detectTableName();
    if ((!pks || !pks.length) && tableName && tableName !== 'TARGET_TABLE') {
      pks = await getTablePrimaryKeys(tableName);
    }
    // 1. 若表定义了主键约束：
    if (pks && pks.length) {
      const matched = [];
      const missingPKs = [];
      for (const pk of pks) {
        const colIdx = state.columns.findIndex(c => cleanIdent(c.name).toUpperCase() === cleanIdent(pk).toUpperCase());
        if (colIdx >= 0) {
          const colName = cleanIdent(state.columns[colIdx].name);
          const v = row[colIdx];
          if (v === null || v === undefined) matched.push(colName + ' IS NULL');
          else matched.push(colName + ' = ' + sqlValueLiteral(v, state.columns[colIdx].database_type));
        } else {
          missingPKs.push(pk);
        }
      }
      // 1.1 若查询结果包含完整主键列，严格仅以主键为 WHERE 条件
      if (matched.length === pks.length) {
        return matched.join(' AND ');
      }
      // 1.2 若 SELECT 只有部分字段、未投影主键列（用户核心诉求 1）：
      // 优先读取行上缓存的主键
      if (row && row._pks) {
        const cachedMatched = [];
        for (const pk of pks) {
          const cleanPk = cleanIdent(pk);
          if (row._pks[cleanPk] !== undefined) {
            const v = row._pks[cleanPk];
            if (v === null || v === undefined) cachedMatched.push(cleanPk + ' IS NULL');
            else cachedMatched.push(cleanPk + ' = ' + sqlValueLiteral(v));
          }
        }
        if (cachedMatched.length === pks.length) {
          return cachedMatched.join(' AND ');
        }
      }
      // 依据当前行已知字段动态回查基表对应主键
      const source = effectiveSource();
      if (source && tableName && tableName !== 'TARGET_TABLE') {
        const target = detectTableTarget();
        const fullTable = target.fullTable || tableName;
        const rowConditions = visibleColumns().map(i => {
          const colName = cleanIdent(state.columns[i].name);
          const v = row[i];
          if (v === null || v === undefined) return colName + ' IS NULL';
          return colName + ' = ' + sqlValueLiteral(v, state.columns[i].database_type);
        }).join(' AND ');

        let pkQuery = 'SELECT ' + pks.map(cleanIdent).join(', ') + ' FROM ' + fullTable;
        if (rowConditions) pkQuery += ' WHERE ' + rowConditions;
        if (source.kind === 'oracle') {
          pkQuery += (rowConditions ? ' AND ' : ' WHERE ') + 'ROWNUM <= 1';
        } else {
          pkQuery += ' LIMIT 1';
        }

        const res = await executeQuickQuery(source.id, pkQuery);
        if (res && res.rows && res.rows.length > 0) {
          const fetchedRow = res.rows[0];
          row._pks = row._pks || {};
          const dbMatched = [];
          pks.forEach((pk, idx) => {
            const val = fetchedRow[idx];
            row._pks[cleanIdent(pk)] = val;
            if (val === null || val === undefined) dbMatched.push(cleanIdent(pk) + ' IS NULL');
            else dbMatched.push(cleanIdent(pk) + ' = ' + sqlValueLiteral(val, res.cols[idx] ? res.cols[idx].database_type : ''));
          });
          if (dbMatched.length === pks.length) {
            return dbMatched.join(' AND ');
          }
        }
      }
    }

    // 2. Oracle 物理行 ROWID 检查
    const rowidIdx = state.columns.findIndex(c => cleanIdent(c.name).toUpperCase() === 'ROWID');
    if (rowidIdx >= 0 && row[rowidIdx]) {
      return 'ROWID = ' + sqlValueLiteral(row[rowidIdx], state.columns[rowidIdx].database_type);
    }

    // 3. 查找常规 ID 列
    const table = tableName || detectTableName();
    const idIdx = state.columns.findIndex(c => {
      const upper = cleanIdent(c.name).toUpperCase();
      return upper === 'ID' || upper === table.toUpperCase() + '_ID';
    });
    if (idIdx >= 0 && row[idIdx] != null) {
      return cleanIdent(state.columns[idIdx].name) + ' = ' + sqlValueLiteral(row[idIdx], state.columns[idIdx].database_type);
    }

    // 4. 兜底匹配行原值
    const conditions = visibleColumns().map(i => {
      const colName = cleanIdent(state.columns[i].name);
      const v = row[i];
      if (v === null || v === undefined) return colName + ' IS NULL';
      return colName + ' = ' + sqlValueLiteral(v, state.columns[i].database_type);
    });
    return conditions.join(' AND ');
  }

  async function copyCellAsUpdate(rowIdx, colIdx) {
    const row = state.rows[rowIdx];
    if (!row) return;
    const table = detectTableName();
    const colName = cleanIdent(state.columns[colIdx].name);
    const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + colIdx];
    const val = dirty ? dirty.newVal : row[colIdx];
    const valLit = sqlValueLiteral(val, state.columns[colIdx].database_type);
    const pks = await getTablePrimaryKeys(table);
    const whereClause = await buildWhereClause(row, pks, table, rowIdx);
    const updateSQL = 'UPDATE ' + table + ' SET ' + colName + ' = ' + valLit + ' WHERE ' + whereClause + ';';
    copyDBText(updateSQL, '已复制 UPDATE 语句到剪贴板');
  }

  async function copyColsAsUpdate(rowIdx, colIndices) {
    const row = state.rows[rowIdx];
    if (!row || !colIndices || !colIndices.length) return;
    const table = detectTableName();
    const pks = await getTablePrimaryKeys(table);
    const setClauses = colIndices.map(i => {
      const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + i];
      const val = dirty ? dirty.newVal : row[i];
      return cleanIdent(state.columns[i].name) + ' = ' + sqlValueLiteral(val, state.columns[i].database_type);
    }).join(', ');
    const whereClause = await buildWhereClause(row, pks, table, rowIdx);
    const updateSQL = 'UPDATE ' + table + ' SET ' + setClauses + ' WHERE ' + whereClause + ';';
    copyDBText(updateSQL, '已复制 ' + colIndices.length + ' 列 UPDATE 语句到剪贴板');
  }

  async function copyRowAsUpdate(rowIdx) {
    const row = state.rows[rowIdx];
    if (!row) return;
    const table = detectTableName();
    const pks = await getTablePrimaryKeys(table);
    const pkSet = new Set((pks || []).map(k => cleanIdent(k).toUpperCase()));
    const cols = visibleColumns().filter(i => !pkSet.has(cleanIdent(state.columns[i].name).toUpperCase()));
    const targetCols = cols.length ? cols : visibleColumns();
    const setClauses = targetCols.map(i => {
      const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + i];
      const val = dirty ? dirty.newVal : row[i];
      return cleanIdent(state.columns[i].name) + ' = ' + sqlValueLiteral(val, state.columns[i].database_type);
    }).join(', ');
    const whereClause = await buildWhereClause(row, pks, table, rowIdx);
    const updateSQL = 'UPDATE ' + table + ' SET ' + setClauses + ' WHERE ' + whereClause + ';';
    copyDBText(updateSQL, '已复制整行 UPDATE 语句到剪贴板');
  }

  async function exportRowAsUpdateFile(rowIdx) {
    const row = state.rows[rowIdx];
    if (!row) return;
    const table = detectTableName();
    const pks = await getTablePrimaryKeys(table);
    const pkSet = new Set((pks || []).map(k => cleanIdent(k).toUpperCase()));
    const cols = visibleColumns().filter(i => !pkSet.has(cleanIdent(state.columns[i].name).toUpperCase()));
    const targetCols = cols.length ? cols : visibleColumns();
    const setClauses = targetCols.map(i => {
      const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + i];
      const val = dirty ? dirty.newVal : row[i];
      return cleanIdent(state.columns[i].name) + ' = ' + sqlValueLiteral(val, state.columns[i].database_type);
    }).join(', ');
    const whereClause = await buildWhereClause(row, pks, table, rowIdx);
    const content = '-- Exported from Kairo Database Workbench\nUPDATE ' + table + ' SET ' + setClauses + ' WHERE ' + whereClause + ';\nCOMMIT;\n';
    downloadBlob(new Blob([content], { type: 'text/sql;charset=utf-8' }), table + '_row_' + (rowIdx + 1) + '_update.sql');
  }

  function exportRowAsInsertFile(rowIdx) {
    const row = state.rows[rowIdx];
    if (!row) return;
    const table = detectTableName();
    const cols = visibleColumns();
    const colNames = cols.map(i => cleanIdent(state.columns[i].name)).join(', ');
    const valLiterals = cols.map(i => {
      const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + i];
      const v = dirty ? dirty.newVal : row[i];
      return sqlValueLiteral(v, state.columns[i].database_type);
    }).join(', ');
    const content = '-- Exported from Kairo Database Workbench\nINSERT INTO ' + table + ' (' + colNames + ') VALUES (' + valLiterals + ');\nCOMMIT;\n';
    downloadBlob(new Blob([content], { type: 'text/sql;charset=utf-8' }), table + '_row_' + (rowIdx + 1) + '.sql');
  }

  function exportRowAsTxtFile(rowIdx) {
    const row = state.rows[rowIdx];
    if (!row) return;
    const table = detectTableName();
    const cols = visibleColumns();
    const header = cols.map(i => state.columns[i].name).join('\t');
    const values = cols.map(i => {
      const dirty = state.dirtyCells && state.dirtyCells[rowIdx + '_' + i];
      return cellText(dirty ? dirty.newVal : row[i]);
    }).join('\t');
    const content = header + '\n' + values + '\n';
    downloadBlob(new Blob([content], { type: 'text/plain;charset=utf-8' }), table + '_row_' + (rowIdx + 1) + '.txt');
  }

  function downloadBlob(blob, filename) {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    setTimeout(() => { document.body.removeChild(a); URL.revokeObjectURL(url); }, 500);
  }

  function openLobModal(val, colName, rowIdx, colIdx) {
    if (!val) return;
    const isClob = val.kind === 'clob';
    const sizeText = val.token && isClob ? Number(val.length || 0).toLocaleString() + ' 字符' : formatBytes(val.token ? val.length : val.bytes || 0);
    const truncNote = val.truncated ? '（预览已截断）' : '';
    const textType = val.database_type === 'NCLOB' ? 'NCLOB' : 'CLOB';
    const title = (isClob ? textType + ' 文本查看器' : 'BLOB / 二进制十六进制检视器') + ' · ' + (colName || '字段') + ' (' + sizeText + truncNote + ')';

    const body = el('div', { class: 'db-lob-viewer-body' });
    if (isClob) {
      const hasContent = val.text !== undefined && val.text !== null;
      if (hasContent) {
        const text = String(val.text || '');
        body.innerHTML = '<div class="db-lob-toolbar">' +
          '<label class="db-lob-check"><input type="checkbox" id="db-lob-wrap" checked> 自动换行</label>' +
          '<span class="muted">当前预览 ' + new TextEncoder().encode(text).byteLength.toLocaleString() + ' 字节 · 字段大小 ' + sizeText + ' ' + truncNote + '</span>' +
          '<button class="btn btn-xs" id="db-lob-copy">复制文本</button>' +
          '<button class="btn btn-xs" id="db-lob-download">下载预览</button>' +
          '<button class="btn btn-primary btn-xs" id="db-lob-full">下载完整内容</button>' +
          '</div>' +
          '<pre class="db-lob-text" id="db-lob-pre">' + h(text) + '</pre>' +
          '<div class="muted" style="margin-top:6px;font-size:11px">可下载当前预览；完整下载需要有效的单表浏览引用。</div>';
        const dlg = Kairo.overlays.modal({ title, width: 840, body });
        const pre = q('db-lob-pre');
        const wrap = q('db-lob-wrap');
        if (wrap && pre) wrap.onchange = function () { pre.style.whiteSpace = this.checked ? 'pre-wrap' : 'pre'; };
        if (q('db-lob-copy')) q('db-lob-copy').onclick = () => copyDBText(text, 'CLOB 文本已复制');
        if (q('db-lob-download')) q('db-lob-download').onclick = () => downloadBlob(new Blob([text], { type: 'text/plain;charset=utf-8' }), (colName || 'clob') + '.txt');
        if (q('db-lob-full')) q('db-lob-full').onclick = () => downloadFullLob(rowIdx, colIdx, colName, isClob, val);
      } else {
        // 虚拟投影 LOB：未预先拉取正文（阻断 TTC Code 10），提供在线预览与下载
        body.innerHTML = '<div class="db-lob-toolbar">' +
          '<span class="muted">字段大小：' + sizeText + '</span>' +
          '<button class="btn btn-primary btn-xs" id="db-lob-fetch-preview">在线预览文本</button>' +
          '<button class="btn btn-primary btn-xs" id="db-lob-full">下载完整内容</button>' +
          '</div>' +
          '<div class="db-lob-placeholder" id="db-lob-placeholder" style="padding:28px 16px;text-align:center;background:var(--bg-1);border:1px dashed var(--line);border-radius:6px;">' +
          '<div style="font-size:14px;font-weight:600;margin-bottom:8px;">CLOB 列在线流式预览</div>' +
          '<div class="muted" style="font-size:12px;line-height:1.6;max-width:540px;margin:0 auto 16px;">' +
          '正在为您流式加载前 256 KB 预览（支持任意超大文件毫秒级秒开）…' +
          '</div>' +
          '<div style="display:inline-flex;gap:10px;align-items:center;">' +
          '<button class="btn btn-sm btn-primary" id="db-lob-big-preview"><span class="spinner" style="vertical-align:middle;margin-right:6px;"></span>正在流式加载预览…</button>' +
          '<button class="btn btn-sm" id="db-lob-big-full">流式下载完整内容</button>' +
          '</div>' +
          '</div>' +
          '<pre class="db-lob-text" id="db-lob-pre" style="display:none;"></pre>' +
          '<div class="muted" style="margin-top:6px;font-size:11px">下载引用在 5 分钟后过期，过期后请重新查询。</div>';
        let closedByUser = false;
        const dlg = Kairo.overlays.modal({
          title,
          width: 840,
          body,
          onClose: () => { closedByUser = true; }
        });
        const loadPreview = async (btn) => {
          if (btn) { btn.disabled = true; btn.innerHTML = '<span class="spinner" style="vertical-align:middle;margin-right:6px;"></span>正在流式加载预览…'; }
          const capturedTabId = (sess() || {}).id;
          const capturedRunSeq = (sess() || {}).runSeq;
          const resolved = await resolveLobPayload(rowIdx, colIdx, colName, isClob, val);
          if (!resolved) { if (btn) { btn.disabled = false; btn.textContent = '重试在线预览'; } return; }
          try {
            const resp = await fetch('/api/database/lob', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(resolved.payload)
            });
            if (!resp.ok) {
              let msg = '加载预览失败：HTTP ' + resp.status;
              try { const j = await resp.json(); if (j && j.error) msg = j.error; } catch (_) {}
              toast(msg, 'err');
              if (btn) { btn.disabled = false; btn.textContent = '重试加载预览'; }
              return;
            }
            const reader = resp.body.getReader();
            const decoder = new TextDecoder();
            let text = '', total = 0;
            const LIMIT = 262144; // 256KB
            let truncated = false;
            while (true) {
              const { done, value } = await reader.read();
              if (done) { text += decoder.decode(); break; }
              const remain = LIMIT - total;
              const accepted = value.subarray(0, Math.max(0, remain));
              text += decoder.decode(accepted, { stream: true });
              total += accepted.length;
              if (value.length > remain || total === LIMIT) {
                truncated = true;
                await reader.cancel();
                break;
              }
            }
            const activeSess = sess();
            if (!activeSess || activeSess.id !== capturedTabId || activeSess.runSeq !== capturedRunSeq || closedByUser) {
              return;
            }
            val.text = text;
            val.truncated = truncated || total > LIMIT;
            dlg.close();
            openLobModal(val, colName, rowIdx, colIdx);
            toast('CLOB 预览文本已加载', 'ok');
          } catch (e) {
            toast('加载异常：' + (e && e.message ? e.message : e), 'err');
            if (btn) { btn.disabled = false; btn.textContent = '重试加载预览'; }
          }
        };
        if (q('db-lob-fetch-preview')) q('db-lob-fetch-preview').onclick = function () { loadPreview(this); };
        if (q('db-lob-big-preview')) q('db-lob-big-preview').onclick = function () { loadPreview(this); };
        if (q('db-lob-full')) q('db-lob-full').onclick = () => downloadFullLob(rowIdx, colIdx, colName, isClob, val);
        if (q('db-lob-big-full')) q('db-lob-big-full').onclick = () => downloadFullLob(rowIdx, colIdx, colName, isClob, val);
        // 打开弹窗自动触发流式首屏快速预览
        loadPreview(q('db-lob-big-preview'));
      }
    } else {
      const hasContent = !!val.loaded || (val.hex !== undefined && val.hex !== null) || (val.preview_base64 !== undefined && val.preview_base64 !== null);
      if (hasContent) {
        const hex = String(val.hex || '');
        const b64 = String(val.preview_base64 || '');
        body.innerHTML = '<div class="db-lob-toolbar"><span class="muted">共 ' + (val.bytes != null ? val.bytes : (hex.length / 2)) + ' 字节 ' + truncNote + '</span><button class="btn btn-xs" id="db-lob-copy-hex">复制 Hex</button><button class="btn btn-xs" id="db-lob-download-bin">下载预览</button><button class="btn btn-primary btn-xs" id="db-lob-full-blob">下载完整内容</button></div><div class="db-lob-hex-view" id="db-lob-hex"></div><div class="muted" style="margin-top:6px;font-size:11px">下载引用在 5 分钟后过期，过期后请重新查询。</div>';
        const dlg = Kairo.overlays.modal({ title, width: 900, body });
        renderHexDump(q('db-lob-hex'), hex, b64);
        if (q('db-lob-copy-hex')) q('db-lob-copy-hex').onclick = () => copyDBText(hex, '十六进制数据已复制');
        if (q('db-lob-download-bin')) q('db-lob-download-bin').onclick = () => {
          try {
            const bin = atob(b64);
            const buf = new Uint8Array(bin.length);
            for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
            downloadBlob(new Blob([buf], { type: 'application/octet-stream' }), (colName || 'blob') + '.bin');
          } catch (e) {
            toast('生成二进制下载失败：' + e.message, 'err');
          }
        };
        if (q('db-lob-full-blob')) q('db-lob-full-blob').onclick = () => downloadFullLob(rowIdx, colIdx, colName, isClob, val);
      } else {
        // 虚拟投影 BLOB
        body.innerHTML = '<div class="db-lob-toolbar">' +
          '<span class="muted">元数据大小：共 ' + (val.length || val.bytes || 0) + ' 字节 · ' + sizeText + '（单表安全投影模式）</span>' +
          '<button class="btn btn-primary btn-xs" id="db-lob-fetch-blob-preview">在线预览 Hex</button>' +
          '<button class="btn btn-primary btn-xs" id="db-lob-full-blob">下载完整内容</button>' +
          '</div>' +
          '<div class="db-lob-placeholder" id="db-lob-blob-placeholder" style="padding:28px 16px;text-align:center;background:var(--bg-1);border:1px dashed var(--line);border-radius:6px;">' +
          '<div style="font-size:14px;font-weight:600;margin-bottom:8px;">BLOB 列在线流式预览</div>' +
          '<div class="muted" style="font-size:12px;line-height:1.6;max-width:540px;margin:0 auto 16px;">' +
          '正在为您流式加载 Hex 预览（支持任意超大文件毫秒级秒开）…' +
          '</div>' +
          '<div style="display:inline-flex;gap:10px;align-items:center;">' +
          '<button class="btn btn-sm btn-primary" id="db-lob-big-blob-preview"><span class="spinner" style="vertical-align:middle;margin-right:6px;"></span>正在流式加载预览…</button>' +
          '<button class="btn btn-sm" id="db-lob-big-blob-full">流式下载完整内容</button>' +
          '</div>' +
          '</div>' +
          '<div class="db-lob-hex-view" id="db-lob-hex" style="display:none;"></div>' +
          '<div class="muted" style="margin-top:6px;font-size:11px">下载引用在 5 分钟后过期，过期后请重新查询。</div>';
        let closedByUser = false;
        const abortCtrl = (typeof AbortController !== 'undefined') ? new AbortController() : null;
        const dlg = Kairo.overlays.modal({
          title,
          width: 900,
          body,
          onClose: () => {
            closedByUser = true;
            if (abortCtrl) { try { abortCtrl.abort(); } catch (_) {} }
          }
        });
        const loadBlobPreview = async (btn) => {
          if (btn) { btn.disabled = true; btn.innerHTML = '<span class="spinner" style="vertical-align:middle;margin-right:6px;"></span>正在流式加载预览…'; }
          const capturedTabId = (sess() || {}).id;
          const capturedRunSeq = (sess() || {}).runSeq;
          const resolved = await resolveLobPayload(rowIdx, colIdx, colName, isClob, val);
          if (!resolved) { if (btn) { btn.disabled = false; btn.textContent = '重试在线预览'; } return; }
          try {
            const fetchOpts = {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(resolved.payload)
            };
            if (abortCtrl && abortCtrl.signal) fetchOpts.signal = abortCtrl.signal;
            const resp = await fetch('/api/database/lob', fetchOpts);
            if (!resp.ok) {
              let msg = '加载预览失败：HTTP ' + resp.status;
              try { const j = await resp.json(); if (j && j.error) msg = j.error; } catch (_) {}
              toast(msg, 'err');
              if (btn) { btn.disabled = false; btn.textContent = '重试在线预览'; }
              return;
            }
            const reader = resp.body.getReader();
            const chunks = [];
            let total = 0;
            const LIMIT = 65536; // 64KB
            let truncated = false;
            while (true) {
              const { done, value } = await reader.read();
              if (done) break;
              if (value) {
                total += value.length;
                if (total - value.length < LIMIT) {
                  const need = LIMIT - (total - value.length);
                  if (value.length > need) {
                    chunks.push(value.slice(0, need));
                    truncated = true;
                    reader.cancel();
                    break;
                  } else {
                    chunks.push(value);
                  }
                } else {
                  truncated = true;
                  reader.cancel();
                  break;
                }
              }
            }
            const activeSess = sess();
            if (!activeSess || activeSess.id !== capturedTabId || activeSess.runSeq !== capturedRunSeq || closedByUser) {
              return;
            }
            const allBytes = new Uint8Array(chunks.reduce((acc, c) => acc + c.length, 0));
            let offset = 0;
            for (const c of chunks) { allBytes.set(c, offset); offset += c.length; }
            let hex = '';
            for (let i = 0; i < allBytes.length; i++) {
              hex += allBytes[i].toString(16).padStart(2, '0');
            }
            val.loaded = true;
            val.hex = hex;
            let binStr = '';
            for (let i = 0; i < allBytes.length; i++) binStr += String.fromCharCode(allBytes[i]);
            val.preview_base64 = btoa(binStr);
            val.truncated = truncated || total > LIMIT;
            dlg.close();
            openLobModal(val, colName, rowIdx, colIdx);
            toast('BLOB 预览已加载', 'ok');
          } catch (e) {
            if (e.name === 'AbortError' || closedByUser) return;
            toast('加载异常：' + (e && e.message ? e.message : e), 'err');
            if (btn) { btn.disabled = false; btn.textContent = '重试在线预览'; }
          }
        };
        if (q('db-lob-fetch-blob-preview')) q('db-lob-fetch-blob-preview').onclick = function () { loadBlobPreview(this); };
        if (q('db-lob-big-blob-preview')) q('db-lob-big-blob-preview').onclick = function () { loadBlobPreview(this); };
        if (q('db-lob-full-blob')) q('db-lob-full-blob').onclick = () => downloadFullLob(rowIdx, colIdx, colName, isClob, val);
        if (q('db-lob-big-blob-full')) q('db-lob-big-blob-full').onclick = () => downloadFullLob(rowIdx, colIdx, colName, isClob, val);
        // 打开弹窗自动触发流式首屏快速预览
        loadBlobPreview(q('db-lob-big-blob-preview'));
      }
    }
  }

  // 统一解析 LOB 请求载荷（优先 Signed Token，其次 ROWID，最后主键）
  async function resolveLobPayload(rowIdx, colIdx, colName, isClob, valObj) {
    const targetVal = valObj || (state.rows[rowIdx] && state.rows[rowIdx][colIdx]);
    if (targetVal && typeof targetVal === 'object' && targetVal.token) {
      return {
        payload: {
          token: targetVal.token
        },
        table: targetVal.table || detectTableName(),
        useRowId: false,
        isToken: true
      };
    }
    if (targetVal && typeof targetVal === 'object' && targetVal.lazy) {
      const rowKeys = {};
      if (state.columns && state.rows[rowIdx]) {
        state.columns.forEach(function (c, idx) {
          if (c && c.name && state.rows[rowIdx][idx] !== undefined) {
            const cellVal = state.rows[rowIdx][idx];
            if (cellVal === null || typeof cellVal !== 'object') {
              rowKeys[c.name] = cellVal;
            }
          }
        });
      }
      let tName = targetVal.table || detectTableName();
      const effSrc = effectiveSource() || {};
      const activeS = sess() || {};
      if ((!tName || tName === 'TARGET_TABLE') && activeS && activeS.objectName) {
        tName = activeS.objectName;
      }
      let ownerVal = targetVal.owner;
      if (!ownerVal || isPlaceholderSchema(ownerVal)) {
        ownerVal = currentSchema();
      }
      if (isPlaceholderSchema(ownerVal)) {
        ownerVal = '';
      }
      let colType = targetVal.database_type || (isClob ? 'CLOB' : 'BLOB');
      const upperColType = String(colType || '').toUpperCase();
      if (upperColType.includes('CLOB')) {
        colType = 'CLOB';
      } else if (upperColType.includes('BLOB')) {
        colType = 'BLOB';
      }

      try {
        const tokenResp = await fetch('/api/database/lob/token', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            source_id: effSrc.id,
            session_id: activeS.transactionId || '',
            transaction_pending: !!activeS.transactionPending,
            owner: (ownerVal || '').toUpperCase(),
            table: (tName || '').toUpperCase(),
            column: (colName || '').toUpperCase(),
            column_type: colType.toUpperCase(),
            use_rowid: !!targetVal.rowid,
            rowid: targetVal.rowid || '',
            keys: rowKeys
          })
        });
        if (tokenResp.ok) {
          const tokenData = await tokenResp.json();
          if (tokenData && tokenData.token) {
            targetVal.token = tokenData.token;
            return {
              payload: { token: tokenData.token },
              table: tName,
              useRowId: false,
              isToken: true
            };
          }
        } else {
          const errData = await tokenResp.json().catch(() => ({}));
          if (errData && errData.error) {
            toast('获取 LOB 失败：' + errData.error, 'warn');
            return null;
          }
        }
      } catch (err) {
        console.error('按需获取 LOB Token 失败', err);
      }
    }
    toast('该查询结果没有稳定行标识，无法重新下载。请使用单表浏览获取完整内容。', 'warn');
    return null;
  }

  // 企业版：按 Token/主键/ROWID 重查并流式下载完整 LOB（结论 2.3/2.6/3.3）
  async function downloadFullLob(rowIdx, colIdx, colName, isClob, valObj) {
    const resolved = await resolveLobPayload(rowIdx, colIdx, colName, isClob, valObj);
    if (!resolved) return;
    const { payload, table, useRowId, isToken } = resolved;
    const activeS = sess() || {};
    const label = isToken ? 'Token' : (useRowId ? 'ROWID' : '主键');
    const isPendingTx = !!activeS.transactionPending;
    const actionDesc = isPendingTx ? '重查' : '按当前行重新读取';
    toast('正在按 ' + label + actionDesc + '并流式下载完整 ' + (isClob ? 'CLOB' : 'BLOB') + '…', 'info');
    try {
      const resp = await fetch('/api/database/lob', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!resp.ok) {
        let msg = '下载失败：HTTP ' + resp.status;
        try { const j = await resp.json(); if (j && j.error) msg = j.error; } catch (_) { try { msg = await resp.text(); } catch (_) {} }
        toast(msg, 'err');
        return;
      }
      const blob = await resp.blob();
      const cd = resp.headers.get('Content-Disposition') || '';
      let fname = ((table || 'lob') + '_' + colName + (isClob ? '.txt' : '.bin'));
      const m = cd.match(/filename*=UTF-8''([^;]+)/);
      if (m) try { fname = decodeURIComponent(m[1]); } catch (_) {}
      downloadBlob(blob, fname);
      toast('完整 ' + (isClob ? 'CLOB' : 'BLOB') + ' 已下载（' + formatBytes(blob.size) + '）', 'ok');
    } catch (e) {
      toast('下载异常：' + (e && e.message ? e.message : e), 'err');
    }
  }

    function renderHexDump(host, hexStr, b64) {
    if (!host) return;
    let rawBytes = [];
    if (b64) {
      try {
        const bin = atob(b64);
        for (let i = 0; i < bin.length; i++) rawBytes.push(bin.charCodeAt(i));
      } catch (_) {}
    }
    if (!rawBytes.length && hexStr) {
      for (let i = 0; i < hexStr.length; i += 2) {
        rawBytes.push(parseInt(hexStr.substr(i, 2), 16));
      }
    }
    if (!rawBytes.length) {
      host.innerHTML = '<div class="muted" style="padding:16px;text-align:center;">（0 字节空二进制内容）</div>';
      return;
    }
    let lines = [];
    const chunkSize = 16;
    for (let i = 0; i < rawBytes.length; i += chunkSize) {
      const chunk = rawBytes.slice(i, i + chunkSize);
      const offset = i.toString(16).padStart(8, '0').toUpperCase();
      const hexPart = chunk.map(b => b.toString(16).padStart(2, '0').toUpperCase()).join(' ');
      const paddedHex = hexPart.padEnd(chunkSize * 3, ' ');
      const asciiPart = chunk.map(b => (b >= 32 && b <= 126) ? String.fromCharCode(b) : '.').join('');
      lines.push(offset + '  ' + paddedHex + '  |' + asciiPart + '|');
    }
    host.innerHTML = '<pre class="db-hex-pre">' + h(lines.join('\n')) + '</pre>';
  }

  function startCellEdit(rowIdx, colIdx, td, setNull) {
    const s = sess();
    if (s && (s.transactionBusy || s.gridEditsStaged || s.controller || s.outcomeUnknown)) return false;
    if (!canWriteDatabase()) {
      toast('当前账号只有查询权限，不能编辑结果', 'warn');
      return false;
    }
    if (!state.isEditMode) {
      toast('网格编辑未开启；双击将打开单行记录，按工具栏按钮可开启编辑', 'warn');
      return false;
    }
    const context = getGridContext();
    if (!context || context.editable === false) {
      toast((context && context.editPlan && context.editPlan.reason) || '当前结果不支持网格编辑', 'warn');
      return false;
    }
    if (!state.rows || !state.rows[rowIdx] || !state.columns || !state.columns[colIdx]) return false;
    if (!td) {
      const grid = q('db-result-grid');
      td = grid && grid.querySelector('td[data-row="' + rowIdx + '"][data-col="' + colIdx + '"]');
    }
    if (!td || td.querySelector('input.db-cell-input')) return false;
    const dirtyKey = rowIdx + '_' + colIdx;
    const currentVal = (state.dirtyCells && state.dirtyCells[dirtyKey]) ? state.dirtyCells[dirtyKey].newVal : state.rows[rowIdx][colIdx];
    if (currentVal && typeof currentVal === 'object') return toast('复杂字段请使用专用查看器，不能直接编辑预览值', 'warn');
    const textVal = currentVal == null ? '' : cellText(currentVal);
    
    const input = document.createElement('input');
    input.className = 'db-cell-input'; input.value = textVal;
    input.title = 'Enter 保存；Escape 取消；Ctrl+Delete 设为 SQL NULL';
    input.placeholder = currentVal == null ? 'SQL NULL' : '';
    td.replaceChildren(input);
    input.focus();
    input.select();

    let committed = false, inputChanged = false;
    input.oninput = () => { inputChanged = true; };
    const finish = (save, asNull) => {
      if (committed) return;
      committed = true;
      const newVal = asNull || currentVal == null && !inputChanged && input.value === '' ? null : input.value;
      if (save && (newVal === null ? currentVal != null : currentVal == null || newVal !== textVal)) {
        if (!state.dirtyCells) state.dirtyCells = {};
        state.dirtyCells[dirtyKey] = {
          rowIdx, colIdx,
          oldVal: currentVal,
          newVal: newVal,
          colName: state.columns[colIdx].name,
          row: state.rows[rowIdx]
        };
        td.classList.add('db-cell-dirty');
        td.innerHTML = fmtCell(newVal, rowIdx, colIdx);
      } else if (!state.dirtyCells || !state.dirtyCells[dirtyKey]) {
        td.classList.remove('db-cell-dirty');
        td.innerHTML = fmtCell(currentVal, rowIdx, colIdx);
      } else {
        td.innerHTML = fmtCell(state.dirtyCells[dirtyKey].newVal, rowIdx, colIdx);
      }
      updateTransactionControls();
    };

    input.onkeydown = (e) => {
      if (e.ctrlKey && e.key === 'Delete') { e.preventDefault(); finish(true, true); }
      else if (e.key === 'Enter') { e.preventDefault(); finish(true); }
      else if (e.key === 'Escape') { e.preventDefault(); finish(false); }
    };
    input.onblur = () => finish(true);
    if (setNull) finish(true, true);
    return true;
  }

  function updateTransactionControls() {
    const dirtyCount = Object.keys(state.dirtyCells || {}).length;
    const current = sess();
    const transactionPending = !!(current && current.transactionPending);
    const hasPending = dirtyCount > 0 || transactionPending;
    const isUnknown = !!(current && current.outcomeUnknown);
    const commitBtn = q('db-btn-commit');
    const rollbackBtn = q('db-btn-rollback');
    const modeBtn = q('db-toggle-edit');
    if (commitBtn) {
      commitBtn.disabled = isUnknown || !hasPending || !!(sess() && sess().transactionBusy);
      commitBtn.innerHTML = actionIcon('commit') + '<span>' + (isUnknown ? '提交（结果未知待核对）' : '提交' + (dirtyCount ? '（网格 ' + dirtyCount + '）' : transactionPending ? '（有事务）' : '')) + '</span>';
    }
    if (rollbackBtn) {
      rollbackBtn.disabled = (!hasPending && !isUnknown) || !!(sess() && sess().transactionBusy);
      rollbackBtn.innerHTML = actionIcon('rollback') + '<span>' + (isUnknown ? '清除未知状态' : '回滚') + '</span>';
    }
    if (modeBtn) {
      modeBtn.innerHTML = actionIcon(state.isEditMode ? 'unlock' : 'lock') + '<span>' + (state.isEditMode ? '网格编辑开启' : '网格编辑关闭') + '</span>';
      modeBtn.classList.toggle('is-editing', !!state.isEditMode);
      modeBtn.disabled = !canWriteDatabase();
      modeBtn.setAttribute('aria-pressed', state.isEditMode ? 'true' : 'false');
    }
  }

  function getGridContext() {
    const current = sess(), source = effectiveSource(), features = Kairo.databaseFeatures;
    if (!current || current.type === 'object' || !source) return null;
    // 检查结果绑定的来源是否已失效（切源或配置变更）
    if (current.sourceId && current.sourceId !== source.id) return null;
    if (current.sourceFingerprint && source.fingerprint && current.sourceFingerprint !== source.fingerprint) return null;

    const plan = current.editPlan;
    let targetSchema = plan ? plan.schema : '';
    let targetTable = plan ? plan.table : '';
    if (!targetTable && features && features.resolveGridTarget) {
      let schemaHint = current.resultSchema;
      if (!schemaHint || isPlaceholderSchema(schemaHint)) schemaHint = currentSchema();
      const target = features.resolveGridTarget(current.lastSQL, schemaHint || '', source.kind);
      if (target) {
        targetSchema = target.schema;
        targetTable = target.table;
      }
    }
    if (!targetTable) return null;

    const planAllows = plan ? (plan.canUpdate || plan.canInsert) : true;
    const canEdit = planAllows && canWriteDatabase() && !source.read_only && !!current.isEditMode && !current.controller && !current.transactionBusy && !current.outcomeUnknown;

    return {
      resultId: (plan && plan.result_id) || (current.summary && current.summary.result_id) || current.resultId || '',
      sourceId: source.id,
      sourceFingerprint: source.fingerprint || '',
      sessionId: current.transactionId,
      schema: targetSchema,
      table: targetTable,
      sql: current.executedSQL || current.lastSQL,
      columns: current.columns.slice(),
      values: (current.rows[state.selectedRow] || []).slice(),
      rowIndex: state.selectedRow,
      editable: canEdit,
      editPlan: plan,
      production: String(source.environment || '').toLowerCase() === 'production'
    };
  }
  async function commitPendingEdits() {
    const current = sess();
    if (!current || current.transactionBusy) return;
    if (current.outcomeUnknown) {
      toast('当前页签上次提交结果未知，请先刷新核对数据或重新查询，切勿盲目重试提交！', 'warn');
      return;
    }
    const edits = Object.assign({}, current.dirtyCells), dirtyKeys = Object.keys(edits);
    if (!dirtyKeys.length && !current.transactionPending) return toast('当前页签没有待提交事务', 'warn');
    const source = effectiveSource(), context = getGridContext();
    if (!source) return toast('未绑定有效数据源', 'warn');
    if (!canWriteDatabase() || source.read_only) return toast('当前账号或数据源只有查询权限', 'warn');
    if (dirtyKeys.length && (!context || context.editable === false)) return toast((context && context.editPlan && context.editPlan.reason) || '仅支持直接查询单表列的网格修改，请重新查询目标表', 'warn');
    const confirmWrite = String(source.environment || '').toLowerCase() === 'production';
    if (dirtyKeys.length && confirmWrite && !confirm('当前为生产数据源，确认提交网格修改？')) return;
    current.transactionBusy = true;
    try {
      let affected = 0;
      if (dirtyKeys.length && !current.gridEditsStaged) {
        const schemaToUse = (context.schema && context.schema !== '加载中…' && context.schema !== '加载失败') ? context.schema : currentSchema();
        const primaryKey = (context.editPlan && context.editPlan.primary_keys) ? context.editPlan.primary_keys : [];
        const isOracleRowID = context.editPlan && context.editPlan.identity_policy === 'oracle_rowid';
        const rows = new Map();
        dirtyKeys.forEach(function (key) {
          const edit = edits[key], row = current.rows[edit.rowIdx], column = current.columns[edit.colIdx];
          if (!rows.has(edit.rowIdx)) {
            const original = {};
            current.columns.forEach(function (c, i) {
              if (row[i] != null && (typeof row[i] === 'object' || /CLOB|BLOB|LONG|XML|JSON|GEOMETRY/i.test(c.database_type || c.database || c.type || ''))) return;
              original[c.name] = row[i];
            });
            let rowidVal = '';
            if (isOracleRowID) {
              const ridIdx = context.editPlan.hidden_rowid_index;
              if (ridIdx != null && ridIdx >= 0 && row[ridIdx] != null) {
                rowidVal = String(row[ridIdx]);
              } else if (row.length > current.columns.length && row[current.columns.length] != null) {
                rowidVal = String(row[current.columns.length]);
              }
            }
            const mutationItem = { action: 'update', values: {}, original: original, key: original, primary_key: primaryKey };
            if (rowidVal) {
              mutationItem.rowid = rowidVal;
              mutationItem.use_rowid = true;
              original['__KAIRO_EDIT_RID__'] = rowidVal;
            }
            rows.set(edit.rowIdx, mutationItem);
          }
          rows.get(edit.rowIdx).values[column.name] = edit.newVal;
        });
        const response = await api('POST', '/api/database/grid', { result_id: context.resultId || '', source_id: source.id, session_id: current.transactionId, schema: schemaToUse, table: context.table, mutations: Array.from(rows.values()), confirm: confirmWrite });
        current.transactionPending = true;
        current.gridEditsStaged = true;
        affected = Number(response && response.result && response.result.rows_affected) || 0;
      }
      await api('POST', '/api/database/transaction', { source_id: source.id, session_id: current.transactionId, action: 'COMMIT' });
      dirtyKeys.forEach(function (key) { const item = edits[key]; current.rows[item.rowIdx][item.colIdx] = item.newVal; });
      current.transactionPending = false;
      current.gridEditsStaged = false;
      current.dirtyCells = {};
      if (current.summary) current.summary.transaction_pending = false;
      current.status = '事务已提交';
      if (sess() === current) {
        bindSession(current);
        refreshVisibleResult(true);
        showQueryMessage('ok', '事务已提交', dirtyKeys.length ? '网格修改已提交，共影响 ' + affected + ' 行。' : '当前页签的 DML 修改已经提交。');
      }
      toast('事务已提交', 'ok');
    } catch (e) {
      const data = (e && e.data) || {};
      const result = data.result;
      if (data.code === 'OUTCOME_UNKNOWN' || data.outcome === 'outcome_unknown' || data.effect_status === 'unknown' || (e.message && e.message.indexOf('最终状态未知') !== -1)) {
        current.outcomeUnknown = true;
        current.transactionPending = false;
        current.gridEditsStaged = false;
        current.status = '提交结果未知 (请核对数据，切勿盲目重试)';
        if (sess() === current) {
          bindSession(current);
          showQueryMessage('error', '事务提交结果未知', e.message || '提交确认丢失或连接中断，事务最终状态未知。请核对目标数据，切勿盲目重试写入！');
        }
        toast('事务提交结果未知，请核对实际数据，切勿盲目重试！', 'err');
      } else {
        if (result && result.rolled_back) { current.transactionPending = false; current.gridEditsStaged = false; }
        toast('提交失败，请检查事务状态后再操作：' + e.message, 'err');
      }
    } finally {
      current.transactionBusy = false;
      updateTransactionControls();
    }
  }
  // Public bridge for feature modules (script/grid/import).  The page remains
  // the source of truth for the active tab and updates the same commit/rollback
  // controls used by ordinary SQL DML.
  function markTransactionPending(pending, sessionId) {
    const current = sessionId ? state.sessions.find(function (item) { return item.transactionId === sessionId; }) : sess();
    if (!current) return false;
    current.transactionPending = !!pending;
    if (!pending) current.gridEditsStaged = false;
    if (current.summary) current.summary.transaction_pending = !!pending;
    if (sess() === current) { bindSession(current); updateTransactionControls(); }
    return true;
  }

  async function rollbackPendingEdits() {
    const current = sess(), source = effectiveSource();
    if (!current || current.transactionBusy) return;
    if (current.outcomeUnknown) {
      current.outcomeUnknown = false;
      current.transactionId = typeof newTransactionID === 'function' ? newTransactionID() : ('tx-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8));
      current.transactionPending = false;
      current.gridEditsStaged = false;
      current.dirtyCells = {};
      if (current.summary) current.summary.transaction_pending = false;
      current.status = '就绪';
      if (sess() === current) {
        bindSession(current);
        refreshVisibleResult(true);
        showQueryMessage('ok', '状态已重置', '本地未提交修改已丢弃，未知状态标记已清除，已切换为新事务上下文。');
      }
      toast('本地状态已重置为新事务上下文', 'ok');
      updateTransactionControls();
      return;
    }
    const dirtyCount = Object.keys(current.dirtyCells || {}).length;
    if (!dirtyCount && !current.transactionPending) return toast('当前页签没有可回滚的事务', 'warn');
    if (!source) return toast('未选择有效的数据源', 'warn');
    current.transactionBusy = true;
    try {
      if (current.transactionPending) {
        await api('POST', '/api/database/transaction', { source_id: source.id, session_id: current.transactionId, action: 'ROLLBACK' });
      }
      current.transactionPending = false;
      current.gridEditsStaged = false;
      current.dirtyCells = {};
      if (current.summary) current.summary.transaction_pending = false;
      current.status = '事务已回滚';
      if (sess() === current) {
        bindSession(current);
        refreshVisibleResult(true);
        showQueryMessage('ok', '事务已回滚', '当前页签所有未提交 DML 与网格修改已撤销。');
      }
      toast('当前页签事务已回滚，未提交修改已撤销', 'ok');
    } catch (e) { toast('回滚失败：' + e.message, 'err'); }
    finally { current.transactionBusy = false; updateTransactionControls(); }
  }

  /* 会话定时自动备份与防丢（Tab 保活：后台 Tab 也要继续备份，
     只在 Tab 关闭时停。旧的一次性 hashchange 会在首次切走后永久停掉备份。） */
  let lastLocalBackupFingerprint = '';
  let acknowledgedRemoteFingerprint = '';
  let inFlightRemoteFingerprint = null;
  let hasPendingRemoteFlush = false;
  let remoteBackupTimer = null;

  window.addEventListener('kairo:tab-close', function (ev) {
    if (!ev || !ev.detail || ev.detail.route !== 'database') return;
    try { backupDBSessions({ immediate: true, forceLocal: true }); } catch (_) {} // 关闭前最后刷一次，防丢
    if (window._dbBackupTimer) { clearInterval(window._dbBackupTimer); window._dbBackupTimer = null; }
    if (remoteBackupTimer) { clearTimeout(remoteBackupTimer); remoteBackupTimer = null; }
  });

  window.addEventListener('beforeunload', function () {
    try { backupDBSessions({ immediate: true, forceLocal: true }); } catch (_) {}
  });

  function serializeDBSessions() {
    return {
      activeId: state.activeId,
      tabSeq: tabSeq,
      sourceId: state.source ? state.source.id : '',
      editorHeight: Math.round(Number(persisted.editor_height) || 0),
      metaCollapsed: !!persisted.meta_collapsed,
      sessions: (state.sessions || []).slice(0, 50).map(x => {
        if (x.type === 'object') {
          return {
            id: x.id,
            type: 'object',
            schema: x.schema || '',
            objectName: x.objectName || '',
            objectType: x.objectType || 'TABLE',
            sourceId: x.sourceId || '',
            page: 1,
            pageSize: 20
          };
        }
        return {
          id: x.id,
          type: 'query',
          sql: x.sql || '',
          sourceId: x.sourceId || '',
          transactionId: x.transactionId || '',
          transactionPending: !!x.transactionPending,
          gridEditsStaged: !!x.gridEditsStaged,
          page: x.page || 1,
          pageSize: x.pageSize || 20
        };
      })
    };
  }

  function backupDBSessions(options) {
    const opts = options || {};
    const data = serializeDBSessions();
    const fingerprint = JSON.stringify(data);

    // 1. 本地 localStorage 保存：当数据变更或强制保存时，快速写入本地，防断电/崩盘
    if (opts.forceLocal || fingerprint !== lastLocalBackupFingerprint) {
      try {
        const full = Object.assign({}, data, { updatedAt: Date.now() });
        localStorage.setItem('kairo_db_sessions_backup', JSON.stringify(full));
        lastLocalBackupFingerprint = fingerprint;
      } catch (_) {}
    }

    // 2. 远端服务器备份：
    // 若当前内容与上次服务端已确认指纹一致，取消待发防抖任务并清理挂起
    if (fingerprint === acknowledgedRemoteFingerprint) {
      if (remoteBackupTimer) {
        clearTimeout(remoteBackupTimer);
        remoteBackupTimer = null;
      }
      return;
    }

    // 立即同步（如页面关闭前、切出前）
    if (opts.immediate) {
      flushRemoteDBSessionsBackup();
      return;
    }

    // 延迟防抖同步（默认 3 秒防抖），打字输入不频繁冲击后端接口
    if (remoteBackupTimer) clearTimeout(remoteBackupTimer);
    const delay = typeof opts.delay === 'number' ? opts.delay : 3000;
    remoteBackupTimer = setTimeout(() => {
      remoteBackupTimer = null;
      flushRemoteDBSessionsBackup();
    }, delay);
  }

  function flushRemoteDBSessionsBackup() {
    if (remoteBackupTimer) {
      clearTimeout(remoteBackupTimer);
      remoteBackupTimer = null;
    }
    if (inFlightRemoteFingerprint !== null) {
      hasPendingRemoteFlush = true;
      return;
    }
    const data = serializeDBSessions();
    const fingerprint = JSON.stringify(data);
    if (fingerprint === acknowledgedRemoteFingerprint) return;

    inFlightRemoteFingerprint = fingerprint;
    const payload = Object.assign({}, data, { updatedAt: Date.now() });
    try {
      fetch('/api/database/sessions/backup', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
      .then(res => {
        if (res.ok) {
          acknowledgedRemoteFingerprint = fingerprint;
        }
      })
      .catch(() => {})
      .finally(() => {
        inFlightRemoteFingerprint = null;
        if (hasPendingRemoteFlush) {
          hasPendingRemoteFlush = false;
          flushRemoteDBSessionsBackup();
        }
      });
    } catch (_) {
      inFlightRemoteFingerprint = null;
    }
  }

  function restoreLocalDBSessions(data) {
    if (!data || !data.sessions || !data.sessions.length) return false;
    state.sessions = data.sessions.slice(0, 50).map(saved => {
      if (saved.type === 'object') {
        const objSession = {
          id: saved.id,
          type: 'object',
          schema: saved.schema || '',
          objectName: saved.objectName || '',
          objectType: (saved.objectType || 'TABLE').toUpperCase(),
          inspectTab: 'fields',
          inspectData: null,
          inspectLoading: false,
          inspectError: null,
          fieldFilter: '',
          sourceId: saved.sourceId || '',
          sourceRef: null
        };
        const sourceExists = saved.sourceId && state.sources.some(s => s.id === saved.sourceId);
        objSession.sourceId = sourceExists ? saved.sourceId : (state.source ? state.source.id : '');
        const srcObj = state.sources.find(s => s.id === objSession.sourceId);
        if (srcObj) bindSessionSource(objSession, srcObj);
        return objSession;
      }
      const session = createSession(saved.sql);
      session.id = saved.id;
      const sourceExists = saved.sourceId && state.sources.some(s => s.id === saved.sourceId);
      session.sourceId = sourceExists ? saved.sourceId : (state.source ? state.source.id : '');
      session.transactionId = newTransactionID();
      session.transactionPending = false;
      session.gridEditsStaged = false;
      const srcObj = state.sources.find(s => s.id === session.sourceId);
      if (srcObj) bindSessionSource(session, srcObj);
      session.page = saved.page || 1;
      session.pageSize = saved.pageSize || 20;
      return session;
    });
    tabSeq = Math.max(tabSeq, data.tabSeq || data.sessions.length);
    state.activeId = data.activeId || state.sessions[0].id;
    if (data.editorHeight) persisted.editor_height = data.editorHeight;
    if (data.metaCollapsed !== undefined) persisted.meta_collapsed = data.metaCollapsed;

    // 纯模型指纹记录本地已存
    const currentFp = JSON.stringify(serializeDBSessions());
    lastLocalBackupFingerprint = currentFp;
    return true;
  }

  function restoreDBSessions() {
    let restored = false;
    try {
      const raw = localStorage.getItem('kairo_db_sessions_backup');
      if (raw) {
        const data = JSON.parse(raw);
        restored = restoreLocalDBSessions(data);
      }
    } catch (_) {}
    if (!restored && typeof fetch === 'function') {
      fetch('/api/database/sessions/restore', { credentials: 'same-origin' })
        .then(res => res.ok ? res.json() : null)
        .then(remote => {
          const sessData = remote && (remote.session || remote);
          if (sessData && sessData.sessions && sessData.sessions.length) {
            if (restoreLocalDBSessions(sessData)) {
              const currentFp = JSON.stringify(serializeDBSessions());
              acknowledgedRemoteFingerprint = currentFp;
              bindSession(sess());
              restoreSessionChrome(sess());
            }
          } else {
            // 服务端无历史，标记当前状态为已同步，避免刚打开就发空备份
            const initialFp = JSON.stringify(serializeDBSessions());
            lastLocalBackupFingerprint = initialFp;
            acknowledgedRemoteFingerprint = initialFp;
          }
        })
        .catch(() => {});
    }
    return restored;
  }

  function reconcileSessionsWithSources(options) {
    options = options || {};
    const hasSources = state.sources && state.sources.length > 0;

    // 1. 确定系统当前优先数据源 state.source
    if (options.savedSource) {
      state.source = state.sources.find(s => s.id === options.savedSource.id) || options.savedSource;
    } else if (options.targetSourceId) {
      state.source = state.sources.find(s => s.id === options.targetSourceId) || (hasSources ? state.sources[0] : null);
    } else if (!state.source || !state.sources.some(s => s.id === state.source.id)) {
      state.source = hasSources ? state.sources[0] : null;
    }

    if (state.source) {
      persisted.last_source = state.source.id;
      savePersisted();
    } else {
      persisted.last_source = '';
      savePersisted();
    }

    // 确保至少有一个页签
    if (!state.sessions || !state.sessions.length) {
      const first = createSession('');
      state.sessions = [first];
      state.activeId = first.id;
    }

    // 2. 遍历所有会话页签，对齐其数据源绑定状态
    state.sessions.forEach(function (s) {
      if (s.type === 'object') return;

      const isBoundValid = s.sourceId && state.sources.some(x => x.id === s.sourceId);

      if (isBoundValid) {
        // 当前绑定的数据源依然存在且有效，同步刷新快照信息
        const found = state.sources.find(x => x.id === s.sourceId);
        s.sourceRef = Kairo.workbench && Kairo.workbench.resourceSession
          ? Kairo.workbench.resourceSession.snapshot(found)
          : { id: found.id, name: found.name, kind: found.kind };
        s.orphan = false;
        return;
      }

      // 到这里说明 s.sourceId 已经失效或不存在（被删除 / 之前未绑定）
      if (hasSources) {
        // 系统中有可用数据源（刚新建了数据源，或有剩余可用数据源）
        const target = (options.savedSource && state.source) || state.source || state.sources[0];
        if (target) {
          bindSessionSource(s, target);
          s.orphan = false;
          // 如果 SQL 为空或默认方言模板，按目标数据库方言更新
          const isDefaultOrEmpty = !s.sql || s.sql.trim() === 'SELECT SYSDATE AS SERVER_TIME FROM DUAL' || s.sql.trim() === 'SELECT NOW() AS server_time';
          if (isDefaultOrEmpty && target.kind !== 'redis') {
            s.sql = target.kind === 'oracle' ? 'SELECT SYSDATE AS SERVER_TIME FROM DUAL' : 'SELECT NOW() AS server_time';
          }
        }
      } else {
        // 系统中已无任何可用数据源（全部被删除）
        s.sourceId = '';
        s.sourceRef = null;
        s.orphan = false;
      }
    });

    backupDBSessions();
  }

  async function loadSources(preferred) {
    const data = await api('GET', '/api/database/sources');
    state.sources = data.sources || [];
    const wanted = preferred || persisted.last_source;
    state.source = state.sources.find(s => s.id === wanted) || state.sources[0] || null;
    if (state.source) { persisted.last_source = state.source.id; savePersisted(); }
    else { persisted.last_source = ''; savePersisted(); }
  }
  function sourceOptions() {
    if (!state.sources || !state.sources.length) {
      return '<option value="">(无数据源)</option>';
    }
    return state.sources.map(s => '<option value="' + h(s.id) + '"' + (state.source && s.id === state.source.id ? ' selected' : '') + '>' + h(s.name) + ' · ' + kindLabel(s.kind) + '</option>').join('');
  }
  function render(view) {
    cancelGridPaint();
    state.managing = false;
    state.resultMode = 'grid';
    state.gridReady = false;
    const user = Kairo.auth && Kairo.auth.getUser ? Kairo.auth.getUser() : '';
    const role = Kairo.auth && Kairo.auth.getRole ? Kairo.auth.getRole() : '';
    const canManage = !user || role === 'admin';
    const accessText = canWriteDatabase() ? '可写权限 · DML 页签事务 · 手动提交' : '只读权限 · 行数 / 时长 / 并发保护';
    view.innerHTML = '<div class="db-page"><header class="db-topbar card"><div class="db-source-select"><label for="db-source">数据源</label><select id="db-source" aria-label="当前数据源">' + sourceOptions() + '</select></div><span id="db-source-badge" class="db-kind"></span><span id="db-oracle-client" class="db-client-badge" title="企业版 OCI 客户端自检"></span><div class="db-topbar-actions"><button class="btn btn-sm" id="db-test">测试连接</button>' + (canManage ? '<button class="btn btn-sm" id="db-manage" aria-expanded="false" aria-controls="db-manager">数据源管理</button>' : '') + '<button class="btn btn-sm" id="db-settings">工作台设置</button></div><span class="db-safe' + (canWriteDatabase() ? ' is-write' : ' is-readonly') + '" role="status">' + accessText + '</span></header><div id="db-manager"></div><div id="db-workspace"></div></div>';
    q('db-source').onchange = async function () {
      saveEditorSQL();
      const current = sess();
      if (current && current.transactionBusy) { this.value = current.sourceId; return; }
      const dirtyCount = Object.keys((current && current.dirtyCells) || {}).length;
      const hasTransaction = !!(current && current.transactionPending);
      if ((dirtyCount || hasTransaction) && !confirm('切换数据源将回滚当前页签未提交事务' + (dirtyCount ? '并放弃 ' + dirtyCount + ' 处网格修改' : '') + '，确定继续吗？')) {
        this.value = (state.source && state.source.id) || (current && current.sourceId) || '';
        return;
      }
      if (hasTransaction) {
        try {
          await api('POST', '/api/database/transaction', { source_id: current.sourceId, session_id: current.transactionId, action: 'ROLLBACK' });
          current.transactionPending = false;
        } catch (e) {
          this.value = (state.source && state.source.id) || current.sourceId || '';
          toast('切换前回滚失败：' + e.message, 'err');
          return;
        }
      }
      if (dirtyCount) replaceDirtyCells({});
      const next = state.sources.find(s => s.id === this.value) || null;
      if (current && next) bindSessionSource(current, next);
      state.source = next;
      if (next) { persisted.last_source = next.id; savePersisted(); }
      // #13：数据源属于当前页签。重建可见工作区但保留其它页签及其
      // 查询结果/运行状态，切换 Oracle/MySQL/Redis 时不会再复用错误 DOM。
      renderWorkspace(true);
    };
    q('db-test').onclick = testConnection;
    q('db-settings').onclick = openSettings;
    if (q('db-manage')) q('db-manage').onclick = function () {
      if (state.managing) { closeManager(); return; }
      state.managing = true;
      this.setAttribute('aria-expanded', 'true');
      renderManager();
    };
    renderWorkspace();
  }
  function field(label, id, value, type, placeholder) {
    return '<label class="db-field"><span>' + h(label) + '</span><input id="' + id + '" type="' + (type || 'text') + '" value="' + h(value) + '"' + (placeholder ? ' placeholder="' + h(placeholder) + '"' : '') + '></label>';
  }
  function selectField(label, id, options, value) {
    return '<label class="db-field"><span>' + h(label) + '</span><select id="' + id + '">' + options.map(o => '<option value="' + o[0] + '"' + (o[0] === value ? ' selected' : '') + '>' + h(o[1]) + '</option>').join('') + '</select></label>';
  }
  function closeManager() {
    state.managing = false;
    if (q('db-manage')) q('db-manage').setAttribute('aria-expanded', 'false');
    renderManager();
    if (q('db-manage')) q('db-manage').focus();
  }
  function renderManager(edit) {
    const host = q('db-manager');
    if (!host) return;
    if (!state.managing) { host.innerHTML = ''; return; }
    const s = edit || { kind: 'oracle', port: 1521, oracle_connect_by: 'service_name', query_timeout_seconds: 30, max_rows: 1000, max_result_bytes: 16777216, max_open_connections: 4, max_idle_connections: 1, connection_max_minutes: 10, tls_mode: 'disabled' };
    host.innerHTML = '<section class="card db-manager" role="dialog" aria-modal="true" aria-labelledby="db-manager-title"><div class="db-manager-head"><div><h3 id="db-manager-title">' + (s.id ? '编辑数据源' : '新建数据源') + '</h3><div class="muted">连接信息保存在系统凭据库；保存后自动关闭。</div></div><div class="db-manager-head-actions"><label class="sr-only" for="db-edit-existing">选择已有数据源</label><select id="db-edit-existing" aria-label="选择已有数据源"><option value="">新建数据源</option>' + state.sources.map(x => '<option value="' + h(x.id) + '"' + (x.id === s.id ? ' selected' : '') + '>' + h(x.name) + '</option>').join('') + '</select><button class="btn btn-xs db-manager-close" id="dbf-close-x" title="关闭" aria-label="关闭数据源管理">' + actionIcon('close') + '</button></div></div><div class="db-form-grid">' + field('名称', 'dbf-name', s.name || '') + selectField('类型', 'dbf-kind', [['oracle', 'Oracle 11g+'], ['mysql', 'MySQL'], ['redis', 'Redis']], s.kind) + field('主机', 'dbf-host', s.host || '') + field('端口', 'dbf-port', s.port || '', 'number') + field('用户名', 'dbf-user', s.username || '') + field(s.has_password ? '密码（留空不改）' : '密码', 'dbf-pass', '', 'password') + '</div><div id="dbf-specific" class="db-form-specific"></div><div class="db-form-grid db-form-limits">' + selectField('TLS', 'dbf-tls', [['disabled', '关闭'], ['preferred', '优先（仅 MySQL）'], ['required', '必须且校验证书'], ['skip-verify', '必须但跳过校验']], s.tls_mode || 'disabled') + field('超时（秒）', 'dbf-timeout', s.query_timeout_seconds || 30, 'number') + field('最大行数', 'dbf-rows', s.max_rows || 1000, 'number') + field('最大连接', 'dbf-open', s.max_open_connections || 4, 'number') + field('空闲连接', 'dbf-idle', s.max_idle_connections == null ? 1 : s.max_idle_connections, 'number') + field('授权用户（逗号；*=全员）', 'dbf-users', (s.allowed_users || []).join(', ')) + '</div><div class="db-form-grid db-security-grid"><label class="db-field db-pro-inline-check"><span><input id="dbf-read-only" type="checkbox"' + (s.read_only ? ' checked' : '') + '> 服务端只读</span><small>禁止 DML、网格写入与脚本修改</small></label><label class="db-field db-pro-inline-check"><span><input id="dbf-allow-ddl" type="checkbox"' + (s.allow_ddl ? ' checked' : '') + '> 支持 DDL</span><small>允许执行 CREATE / ALTER / DROP / TRUNCATE / COMMENT 及对象设计器</small></label></div><div class="db-form-actions"><button class="btn btn-primary" id="dbf-save">保存并关闭</button><button class="btn" id="dbf-test-draft">测试连接</button>' + (s.id ? '<button class="btn btn-danger" id="dbf-delete">删除</button>' : '') + '<button class="btn" id="dbf-close">取消</button><span class="hint">密码不会写入配置文件或返回页面。</span></div><div id="dbf-test-result" class="dbf-test-result" style="display:none;"></div></section>';
    q('db-edit-existing').onchange = function () { renderManager(state.sources.find(x => x.id === this.value)); };
    q('dbf-kind').onchange = () => renderSpecific(s);
    q('dbf-save').onclick = () => saveSource(s);
    if (q('dbf-test-draft')) q('dbf-test-draft').onclick = () => testDraftSource(s);
    q('dbf-close').onclick = closeManager;
    q('dbf-close-x').onclick = closeManager;
    const del = q('dbf-delete');
    if (del) del.onclick = () => deleteSource(s);
    const ro = q('dbf-read-only'), ddl = q('dbf-allow-ddl');
    if (ro && ddl) {
      ddl.onchange = function () { if (this.checked && ro) ro.checked = false; };
      ro.onchange = function () { if (this.checked && ddl) ddl.checked = false; };
    }
    renderSpecific(s);
    ['dbf-name', 'dbf-host', 'dbf-port'].forEach(function (id) { const input = q(id); if (input) input.required = true; });
    const port = q('dbf-port'); if (port) { port.min = '1'; port.max = '65535'; port.inputMode = 'numeric'; }
    const timeout = q('dbf-timeout'); if (timeout) { timeout.min = '1'; timeout.max = '600'; }
    const rows = q('dbf-rows'); if (rows) { rows.min = '1'; rows.max = '20000'; }
    const open = q('dbf-open'); if (open) { open.min = '1'; open.max = '64'; }
    const idle = q('dbf-idle'); if (idle) { idle.min = '0'; idle.max = '64'; }
    const first = q('dbf-name'); if (first) first.focus();
  }
  function renderSpecific(s) {
    const kind = q('dbf-kind').value, target = q('dbf-specific');
    if (kind === 'oracle') {
      const driverOptions = [
        ['', '智能自动（优先 OCI 加速，不兼容自动降级纯 Go）'],
        ['go-ora', '纯 Go 驱动 (go-ora，推荐，免配置无需本地 Oracle 客户端)'],
        ['godror', '原生 OCI 驱动 (godror，需安装 64 位 Instant Client 19c+)']
      ];
      target.innerHTML = selectField('连接方式', 'dbf-oracle-by', [['service_name', 'Service Name'], ['sid', 'SID']], s.oracle_connect_by || 'service_name')
        + field('Service/SID', 'dbf-service', s.oracle_service || '')
        + selectField('驱动模式', 'dbf-oracle-driver', driverOptions, s.oracle_driver || '')
        + field('客户端字符集', 'dbf-charset', s.oracle_client_charset || '')
        + field('自定义 OCI 目录 (选填)', 'dbf-oracle-lib-dir', s.oracle_lib_dir || '', 'text', '例如 C:\\oracle\\instantclient_19_24，留空自动检测');
    } else if (kind === 'mysql') target.innerHTML = field('Database', 'dbf-database', s.database || '') + '<div class="db-field db-field-span-2"><span class="muted" style="margin-top:24px;font-size:12px;">MySQL 数据库名；留空将默认连接当前用户有权限的全部数据库。</span></div>';
    else {
      const mode = s.redis_mode || 'standalone';
      target.innerHTML = selectField('拓扑', 'dbf-redis-mode', [['standalone', '单机'], ['cluster', 'Cluster'], ['sentinel', 'Sentinel']], mode)
        + field('Redis DB', 'dbf-redis-db', s.redis_db || 0, 'number')
        + field('Master 名称', 'dbf-redis-master', s.redis_master_name || '')
        + '<label class="db-field db-field-span"><span>附加节点（每行 host:port）</span><textarea id="dbf-redis-nodes" rows="3" placeholder="127.0.0.1:6380">' + h((s.redis_nodes || []).join('\n')) + '</textarea></label>'
        + '<label class="db-field db-field-span db-redis-write-gate"><span><input id="dbf-redis-write" type="checkbox"' + (s.allow_redis_write ? ' checked' : '') + '> 开启受控 TTL 写操作（仅管理员，默认关闭）</span><small>只允许 EXPIRE / PEXPIRE / PERSIST，所有操作需要二次确认并写审计。</small></label>';
      const syncRedis = function () {
        const m = q('dbf-redis-mode').value;
        q('dbf-redis-master').closest('.db-field').hidden = m !== 'sentinel';
        q('dbf-redis-nodes').closest('.db-field').hidden = m === 'standalone';
        q('dbf-redis-db').disabled = m === 'cluster';
        if (m === 'cluster') q('dbf-redis-db').value = 0;
      };
      q('dbf-redis-mode').onchange = syncRedis;
      syncRedis();
    }
    const tls = q('dbf-tls');
    const tlsField = tls && tls.closest('.db-field');
    // Oracle supports required/skip-verify TLS through the go-ora connector.
    // Keep the selector visible and preserve an existing source's mode; the
    // previous Oracle-only hide/reset silently disabled TLS on every edit.
    if (tlsField) tlsField.hidden = false;
    const preferred = tls && tls.querySelector('option[value="preferred"]');
    if (preferred) preferred.disabled = kind !== 'mysql';
    if (preferred && preferred.disabled && tls.value === 'preferred') tls.value = 'required';
    const port = q('dbf-port');
    if (!port.value || ['1521', '3306', '6379'].includes(port.value)) port.value = kind === 'oracle' ? 1521 : kind === 'mysql' ? 3306 : 6379;
  }
  function sourceFromForm(old) {
    const kind = q('dbf-kind').value;
    const source = { name: q('dbf-name').value.trim(), kind, host: q('dbf-host').value.trim(), port: Number(q('dbf-port').value), username: q('dbf-user').value.trim(), tls_mode: q('dbf-tls').value, query_timeout_seconds: Number(q('dbf-timeout').value), max_rows: Number(q('dbf-rows').value), max_result_bytes: (old && old.max_result_bytes) || 16777216, max_open_connections: Number(q('dbf-open').value), max_idle_connections: Number(q('dbf-idle').value), connection_max_minutes: (old && old.connection_max_minutes) || 10, allowed_users: q('dbf-users').value.split(',').map(x => x.trim()).filter(Boolean) };
    if (kind === 'oracle') {
      source.oracle_connect_by = q('dbf-oracle-by').value;
      source.oracle_service = q('dbf-service').value.trim();
      source.oracle_client_charset = q('dbf-charset').value.trim();
      if (q('dbf-oracle-driver')) source.oracle_driver = q('dbf-oracle-driver').value.trim();
      if (q('dbf-oracle-lib-dir')) source.oracle_lib_dir = q('dbf-oracle-lib-dir').value.trim();
    }
    if (kind === 'mysql') source.database = q('dbf-database').value.trim();
    if (kind === 'redis') {
      source.redis_mode = q('dbf-redis-mode').value;
      source.redis_db = Number(q('dbf-redis-db').value);
      source.redis_master_name = q('dbf-redis-master').value.trim();
      source.redis_nodes = q('dbf-redis-nodes').value.split(/[\n,]+/).map(function (x) { return x.trim(); }).filter(Boolean);
      source.allow_redis_write = !!(q('dbf-redis-write') && q('dbf-redis-write').checked);
    }
    // The optional professional feature layer injects environment, TLS and SSH
    // controls into this same form. Keep the base form serializer authoritative
    // so values shown to the user are never silently discarded on save. When a
    // cached/old feature bundle has not mounted its controls, retain the old
    // server values instead of clearing security settings.
    const oldTunnel = old && old.ssh_tunnel ? old.ssh_tunnel : null;
    const valueOrOld = function (name, fallback) { const input = q(name); return input ? input.value.trim() : (fallback == null ? '' : String(fallback)); };
    const checkedOrOld = function (name, fallback) { const input = q(name); return input ? !!input.checked : !!fallback; };
    source.environment = valueOrOld('dbf-environment', old && old.environment || 'production');
    source.read_only = checkedOrOld('dbf-read-only', old ? old.read_only : true);
    source.allow_ddl = checkedOrOld('dbf-allow-ddl', old && old.allow_ddl);
    if (source.allow_ddl) {
      source.read_only = false;
    }
    source.tls_server_name = valueOrOld('dbf-tls-server-name', old && old.tls_server_name);
    source.tls_ca_file = valueOrOld('dbf-tls-ca-file', old && old.tls_ca_file);
    source.tls_client_cert_file = valueOrOld('dbf-tls-client-cert', old && old.tls_client_cert_file);
    source.tls_client_key_file = valueOrOld('dbf-tls-client-key', old && old.tls_client_key_file);
    const tunnelEnabled = q('dbf-ssh-enabled') ? !!q('dbf-ssh-enabled').checked : !!(oldTunnel && oldTunnel.enabled);
    // Retain a configured tunnel object even when disabled so an edit does not
    // erase its host-key/profile fields. A new source only sends this object if
    // the user explicitly enables the tunnel.
    if (oldTunnel || tunnelEnabled) {
      source.ssh_tunnel = Object.assign({}, oldTunnel || {}, {
        enabled: tunnelEnabled,
        host: valueOrOld('dbf-ssh-host', oldTunnel && oldTunnel.host),
        port: Number(valueOrOld('dbf-ssh-port', oldTunnel && oldTunnel.port || 22)) || 22,
        username: valueOrOld('dbf-ssh-user', oldTunnel && oldTunnel.username),
        remote_host: valueOrOld('dbf-ssh-remote-host', oldTunnel && oldTunnel.remote_host || source.host),
        remote_port: Number(valueOrOld('dbf-ssh-remote-port', oldTunnel && oldTunnel.remote_port || source.port)) || source.port,
        host_key_sha256: valueOrOld('dbf-ssh-host-key', oldTunnel && oldTunnel.host_key_sha256),
        ssh_profile: valueOrOld('dbf-ssh-profile', oldTunnel && oldTunnel.ssh_profile || 'auto'),
        allow_insecure_host_key: checkedOrOld('dbf-ssh-insecure', oldTunnel && oldTunnel.allow_insecure_host_key)
      });
    }
    return source;
  }
  function defaultColumnWidth(column) {
    const name = String(column && column.name || '').toUpperCase();
    const type = String(column && column.database_type || '').toUpperCase();
    if (/(^|_)(ID|NO|SEQ|CODE)$/.test(name) || /NUMBER|INT|DECIMAL|FLOAT|DOUBLE/.test(type)) return 116;
    if (/DATE|TIME|TIMESTAMP/.test(type) || /(^|_)(DATE|TIME|AT)$/.test(name)) return 176;
    if (/BOOL|BIT/.test(type)) return 92;
    if (/CLOB|BLOB|TEXT|JSON|XML|LONG/.test(type)) return 260;
    if (/CHAR|VARCHAR|STRING/.test(type)) return 180;
    return 156;
  }
  function gridColumnWidth(index) {
    const saved = Number(state.columnWidths[index]);
    return Math.max(72, Math.min(420, saved || defaultColumnWidth(state.columns[index])));
  }
  function validateSourceForm() {
    const kind = q('dbf-kind') && q('dbf-kind').value;
    const required = ['dbf-name', 'dbf-host', 'dbf-port'];
    if (kind === 'oracle') required.push('dbf-user', 'dbf-service');
    if (kind === 'mysql') required.push('dbf-user');
    required.forEach(function (id) { const input = q(id); if (input) input.required = true; });
    const open = q('dbf-open'), idle = q('dbf-idle');
    if (idle) {
      idle.setCustomValidity(open && Number(idle.value) > Number(open.value) ? '空闲连接不能大于最大连接' : '');
    }
    const formControl = required.map(q).find(function (input) { return input && !input.checkValidity(); })
      || (idle && !idle.checkValidity() ? idle : null);
    if (formControl) {
      formControl.reportValidity();
      formControl.focus();
      return false;
    }
    return true;
  }
  async function saveSource(old) {
    if (!validateSourceForm()) return;
    const button = q('dbf-save'); button.disabled = true;
    try {
      const body = { source: sourceFromForm(old), password: q('dbf-pass').value, ssh_password: q('dbf-ssh-password') ? q('dbf-ssh-password').value : '' };
      const method = old && old.id ? 'PUT' : 'POST';
      const path = '/api/database/sources' + (old && old.id ? '/' + encodeURIComponent(old.id) : '');
      const data = await api(method, path, body);
      await loadSources(data.source && data.source.id);
      reconcileSessionsWithSources({ savedSource: data.source });
      refreshSourceSelect();
      // Refresh the feature-side catalog after the base select has been
      // rebuilt; otherwise its badge enhancer only sees the pre-save options.
      if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.syncSource === 'function') Kairo.databaseFeatures.syncSource(data.source);
      closeManager();
      renderWorkspace(true);
      toast('数据源已保存，管理面板已收起', 'ok');
    } catch (e) { toast('保存失败：' + e.message, 'err'); button.disabled = false; }
  }
  async function deleteSource(source) {
    if (!source.id || !confirm('确定删除数据源“' + source.name + '”及其已保存密码？')) return;
    try {
      await api('DELETE', '/api/database/sources/' + encodeURIComponent(source.id));
      await loadSources();
      reconcileSessionsWithSources({ deletedId: source.id });
      refreshSourceSelect();
      if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.removeSource === 'function') Kairo.databaseFeatures.removeSource(source.id);
      closeManager();
      renderWorkspace(true);
      toast('数据源已删除', 'ok');
    } catch (e) { toast('删除失败：' + e.message, 'err'); }
  }
  function refreshSourceSelect() {
    const s = q('db-source');
    if (s) {
      s.innerHTML = sourceOptions();
      if (state.source) s.value = state.source.id;
      else s.value = '';
    }
  }
  async function testConnection() {
    if (!state.source) return toast('请先创建数据源', 'warn');
    const b = q('db-test'); b.disabled = true; b.textContent = '连接中…';
    hideQueryMessage();
    try {
      const r = await api('POST', '/api/database/sources/' + encodeURIComponent(state.source.id) + '/test', {});
      toast('连接成功 · ' + (r.topology && r.topology !== 'standalone' ? r.topology + (r.masters ? ' ×' + r.masters : '') + ' · ' : '') + (r.version || kindLabel(r.kind)) + ' · ' + r.latency_ms + ' ms', 'ok');
      // 企业版 OCI 自检（仅 Oracle，直连时）
      if (state.source.kind === 'oracle') {
        try {
          const info = await api('GET', '/api/database/oracle/client-info?source_id=' + encodeURIComponent(state.source.id));
          const c = info && info.client;
          if (c) {
            const badge = q('db-oracle-client');
            if (badge) {
              badge.textContent = (c.backend || 'oracle') + ' · ' + (c.available ? '可用' : '不可用') + ' · ' + (c.bitness || '') + (c.client_version ? ' · ' + c.client_version : '');
              badge.className = 'db-client-badge ' + (c.available ? 'ok' : 'warn');
              badge.title = (c.detail || '') + (info.hint ? '\n' + info.hint : '') + '\nlibDir=' + (c.lib_dir || '-') + '\nconfigDir=' + (c.config_dir || '-');
            }
            if (!c.available) toast('Oracle Client 不可用：' + (c.detail || ''), 'warn');
          }
        } catch (_) {}
      }
    } catch (e) {
      showConnectionError(state.source, e.message);
    }
    finally { b.disabled = false; b.textContent = '测试连接'; }
  }

  function diagnoseConnectionError(message, source) {
    const msg = String(message || '');
    if (/未保存密码|password not saved|ErrNotSaved/i.test(msg)) {
      return {
        category: '凭据缺失',
        tips: [
          '本地凭据管理器中未找到该数据源的密码记录（或已被清理）。',
          '请在「数据源管理」中选中该数据源，在密码框中重新输入密码并点击「保存」。'
        ]
      };
    }
    if (/DPI-1072|DPI-1050|DPI-1047|unsupported Oracle Client|OCIEnvCreate/i.test(msg)) {
      return {
        category: 'Oracle 客户端库兼容性异常',
        tips: [
          '本地 Oracle Client / Instant Client 动态链接库版本不兼容或缺失依赖。',
          '推荐方案：编辑该数据源，将「驱动模式」切换为「纯 Go 驱动 (go-ora，推荐)」，无需本地客户端即可直连。',
          '若必须使用 OCI 原生驱动，请下载安装 64 位 Oracle Instant Client 19c+，并在数据源配置中填入 OCI 目录。'
        ]
      };
    }
    if (/ORA-12541|TNS:no listener/i.test(msg)) {
      return {
        category: 'Oracle 监听未启动 (ORA-12541)',
        tips: [
          '目标机器的 Oracle 监听程序 (Listener) 未在指定端口启动。',
          '请登录 Oracle 服务器执行 lsnrctl status 检查监听状态，若未启动请执行 lsnrctl start。',
          '请核对端口配置是否正确（默认通常为 1521）。'
        ]
      };
    }
    if (/ORA-12514|listener does not currently know of service/i.test(msg)) {
      return {
        category: '服务名不存在或未注册 (ORA-12514)',
        tips: [
          'Oracle 监听器未识别到请求的 Service Name。',
          '请检查 Service Name 拼写和大小写（例如 XE, ORCL, XEPDB1, FREEPDB1 等）。',
          '如果数据库使用的是 SID 而非 Service Name，请将连接方式切换为「SID」。',
          '可在服务器端执行 lsnrctl status 查看当前已注册的服务名列表 (Service Summary)。'
        ]
      };
    }
    if (/ORA-01017|invalid username\/password|Access denied for user/i.test(msg)) {
      return {
        category: '用户名或密码错误',
        tips: [
          '数据库鉴权失败，用户名或密码不匹配。',
          '注意密码字母大小写（Oracle 11g+ 默认区分大小写）。',
          '若使用的是管理员账号（如 SYS），需要以 SYSDBA 身份或确保对应远程登录权限已配置。',
          '请在数据源管理中重新核对用户名并重新输入密码保存。'
        ]
      };
    }
    if (/i\/o timeout|context deadline exceeded|timed out|连接超时/i.test(msg)) {
      return {
        category: '网络连接超时 (Timeout)',
        tips: [
          '无法在限定时间内与目标主机建立 TCP 连接，可能由于网络不通或防火墙拦截。',
          '若目标数据库部署在隔离的内网或私有云中，请在数据源配置中开启「SSH 隧道 / 跳板机」。',
          '请核对主机 IP / 域名是否能连通，确认 VPN 或相关内网专线已连接。',
          '可在本地命令行使用 ping 或 Test-NetConnection -ComputerName ' + ((source && source.host) || 'host') + ' -Port ' + ((source && source.port) || '1521') + ' 测试端口连通性。'
        ]
      };
    }
    if (/connection refused|积极拒绝|目标计算机积极拒绝|拒绝连接/i.test(msg)) {
      return {
        category: '目标端口拒绝连接 (Connection Refused)',
        tips: [
          '网络可达，但目标主机在该端口上未监听任何服务，或被服务器防火墙/安全组明确拒绝。',
          '请核对端口号是否填写正确（Oracle 默认 1521，MySQL 默认 3306，Redis 默认 6379）。',
          '请登录目标服务器确认数据库服务处于运行状态，且监听绑定的 IP 允许远程访问（避免绑定为 127.0.0.1）。'
        ]
      };
    }
    if (/no route to host|无法连接到网络/i.test(msg)) {
      return {
        category: '主机不可达 (No Route to Host)',
        tips: [
          '网络路由不可达，目标 IP 可能不存在或处于不可路由的网段。',
          '请核对主机 IP 拼写，并检查路由表或 VPN 拨号状态。'
        ]
      };
    }

    return {
      category: '连接异常',
      tips: [
        '请核对数据源配置中的主机、端口、用户名及数据库/服务名是否准确。',
        '请检查本机与目标数据库服务器之间的网络连通性及防火墙规则。',
        '若有详细错误日志，可点击「复制错误详情」发送给系统管理员或 DBA 进行排查。'
      ]
    };
  }

  function showConnectionError(source, message) {
    const src = source || state.source || {};
    const diag = diagnoseConnectionError(message, src);
    const box = q('db-result-message');
    const sourceLabel = src.name ? (src.name + ' (' + (src.host || '') + (src.port ? ':' + src.port : '') + ')') : '当前数据源';

    toast('连接失败：' + (diag.category || '请检查配置'), 'err');

    if (!box) return;
    box.hidden = false;
    box.className = 'db-result-message error';
    box.setAttribute('role', 'alert');

    const copyFull = '[数据源]: ' + (src.name || '未命名') + ' (' + (src.kind || '') + ' ' + (src.host || '') + ':' + (src.port || '') + ')'
      + '\n[诊断分类]: ' + diag.category
      + '\n[详细报错]: ' + message
      + '\n\n[排查建议]:\n' + diag.tips.map((t, i) => (i + 1) + '. ' + t).join('\n');

    box.innerHTML = '<div class="db-message-icon" aria-hidden="true">!</div>'
      + '<div class="db-message-copy">'
      +   '<div class="db-conn-err-header">'
      +     '<strong>连接失败 · ' + h(sourceLabel) + '</strong>'
      +     '<span class="db-conn-err-badge">' + h(diag.category) + '</span>'
      +   '</div>'
      +   '<div class="db-conn-err-section">'
      +     '<span class="db-conn-err-label">准确报错：</span>'
      +     '<pre class="db-conn-err-pre">' + h(message) + '</pre>'
      +   '</div>'
      +   '<div class="db-conn-err-section">'
      +     '<span class="db-conn-err-label">排查建议：</span>'
      +     '<ul class="db-conn-tips-list">' + diag.tips.map(t => '<li>' + h(t) + '</li>').join('') + '</ul>'
      +   '</div>'
      + '</div>'
      + '<div class="db-message-actions" style="display:flex;gap:6px;align-items:flex-start;flex-shrink:0;">'
      +   '<button class="btn btn-xs" id="db-copy-conn-err">复制错误详情</button>'
      + '</div>';

    const copyBtn = q('db-copy-conn-err');
    if (copyBtn) copyBtn.onclick = () => copyDBText(copyFull, '已复制连接报错与排查建议');
  }

  async function testDraftSource(old) {
    if (!validateSourceForm()) return;
    const passInput = q('dbf-pass');
    const pass = passInput ? passInput.value : '';
    const kind = q('dbf-kind') ? q('dbf-kind').value : 'oracle';
    if (!pass && kind !== 'redis' && (!old || !old.id || !old.has_password)) {
      toast('测试连接请先输入密码', 'warn');
      if (passInput) passInput.focus();
      return;
    }
    const btn = q('dbf-test-draft');
    const resultBox = q('dbf-test-result');
    if (btn) { btn.disabled = true; btn.textContent = '测试中…'; }
    if (resultBox) {
      resultBox.style.display = 'block';
      resultBox.className = 'dbf-test-result';
      resultBox.innerHTML = '正在发起连接测试，请稍候…';
    }
    const source = sourceFromForm(old);
    const sshPass = q('dbf-ssh-password') ? q('dbf-ssh-password').value : '';
    const body = { source: source, password: pass, ssh_password: sshPass };
    const path = (old && old.id) ? ('/api/database/sources/' + encodeURIComponent(old.id) + '/test') : '/api/database/sources/test';
    try {
      const r = await api('POST', path, body);
      if (resultBox) {
        resultBox.className = 'dbf-test-result ok';
        const info = (r.topology && r.topology !== 'standalone' ? r.topology + ' · ' : '') + (r.version || kindLabel(r.kind)) + ' · 延迟 ' + r.latency_ms + ' ms';
        resultBox.innerHTML = '<div style="font-weight:600;display:flex;align-items:center;gap:6px;">✓ 连接测试成功</div><div style="margin-top:2px;">' + h(info) + '</div>';
      }
      toast('连接测试成功 (' + r.latency_ms + ' ms)', 'ok');
    } catch (e) {
      const diag = diagnoseConnectionError(e.message, source);
      if (resultBox) {
        resultBox.className = 'dbf-test-result err';
        resultBox.innerHTML = '<div style="display:flex;justify-content:space-between;align-items:center;">'
          + '<strong style="font-size:12.5px;">✗ 连接测试失败 (' + h(diag.category) + ')</strong>'
          + '<button type="button" class="btn btn-xs" id="dbf-copy-test-err">复制报错</button>'
          + '</div>'
          + '<pre>' + h(e.message) + '</pre>'
          + '<div style="font-weight:600;margin-top:6px;">建议排查方案：</div>'
          + '<ul>' + diag.tips.map(t => '<li>' + h(t) + '</li>').join('') + '</ul>';
        const copyBtn = q('dbf-copy-test-err');
        if (copyBtn) copyBtn.onclick = () => copyDBText(e.message, '已复制报错信息');
      }
      toast('连接失败：' + (e.message.length > 60 ? e.message.slice(0, 60) + '…' : e.message), 'err');
    } finally {
      if (btn) { btn.disabled = false; btn.textContent = '测试连接'; }
    }
  }

  function renderWorkspace(preserveSessions) {
    const host = q('db-workspace'); if (!host) return;
    const keepSessions = preserveSessions === true;
    cancelGridPaint();
    gridIndexCache = { rows: null, len: -1, filter: '', sort: null, indexes: null };
    if (!keepSessions) {
      abortAllSessions();
      state.sessions = [];
      state.activeId = 0;
    }
    state.workspaceToken++;

    // 1. 无任何数据源时的干净空状态
    if (!state.sources || !state.sources.length) {
      state.source = null;
      q('db-source-badge').textContent = '未配置';
      const sourceSelect = q('db-source'); if (sourceSelect) sourceSelect.value = '';
      const testButton = q('db-test'); if (testButton) { testButton.disabled = true; testButton.title = '尚未配置数据源'; }
      host.innerHTML = '<div class="card empty-state"><div class="empty-title">尚无数据源</div><div class="empty-desc">打开“数据源管理”创建 Oracle、MySQL 或 Redis 连接。</div></div>';
      return;
    }

    if (!state.sessions.length) {
      const first = createSession('');
      state.sessions = [first];
      state.activeId = first.id;
    }

    const active = sess();
    if (active) {
      const activeBound = active.sourceId ? state.sources.find(x => x.id === active.sourceId) : null;
      if (!activeBound) {
        // 如果当前会话没有绑定有效数据源，但系统中有数据源可用：自动平滑绑定到当前有效数据源
        const fallback = state.source || state.sources[0];
        bindSessionSource(active, fallback);
        state.source = fallback;
      } else {
        state.source = activeBound;
      }
    }

    // 确保顶部数据源下拉框、Badge 和测试连接按钮状态同步
    const sourceSelect = q('db-source');
    if (sourceSelect && state.source) sourceSelect.value = state.source.id;
    const testButton = q('db-test');
    if (testButton) { testButton.disabled = !state.source; testButton.title = state.source ? '测试连接' : '尚未配置数据源'; }
    q('db-source-badge').textContent = state.source ? kindLabel(state.source.kind) : '未配置';

    if (keepSessions) {
      bindSession(active);
    } else {
      state.rows = []; state.columns = []; state.summary = null; state.lastError = null; state.plan = [];
      state.hiddenColumns = new Set(); state.sort = null; state.inspect = null; state.gridReady = false;
    }
    loadColumnWidths();
    if (state.source.kind !== 'redis') state.schemasLoaded = false;
    state.source.kind === 'redis' ? renderRedis(host) : renderSQL(host);
    // 数据源切换或页签切换会重建工作区 DOM；把当前页签的编辑器、筛选器、
    // 结果视图和执行状态恢复回来，否则页签标题还在但 SQL 文本会变空。
    restoreSessionChrome(active);
  }

  function renderOrphanWorkspace(host, session) {
    host.innerHTML = '<section class="card db-orphan-workspace"><div class="empty-icon">⚠</div><h3>数据源已删除</h3><p>页签“' + h((session.sourceRef && session.sourceRef.name) || session.sourceId) + '”仍保留 SQL 和历史结果，但为避免误发到其他环境，当前已禁用执行、导出和元数据操作。</p><div class="db-orphan-actions"><label>重新绑定数据源<select id="db-orphan-source"><option value="">请选择数据源</option>' + sourceOptions() + '</select></label><button class="btn btn-primary" id="db-orphan-bind">绑定并继续</button></div><div class="db-orphan-sql"><label>当前 SQL</label><textarea id="db-sql" class="db-sql-editor mono" spellcheck="false"></textarea></div></section>';
    const ta = q('db-sql'); if (ta) { ta.value = session.sql || ''; ta.addEventListener('input', function () { session.sql = ta.value; }); }
    const select = q('db-orphan-source'); const button = q('db-orphan-bind');
    if (select && button) button.onclick = function () {
      const source = state.sources.find(function (x) { return x.id === select.value; });
      if (!source) return toast('请选择要绑定的数据源', 'warn');
      bindSessionSource(session, source); state.source = source; renderWorkspace(true); toast('已重新绑定：' + source.name, 'ok');
    };
  }

  function renderSQL(host) {
    const initial = state.source.kind === 'oracle' ? 'SELECT SYSDATE AS SERVER_TIME FROM DUAL' : 'SELECT NOW() AS server_time';
    const history = loadHistory();
    const savedRows = Math.max(1, Math.min(state.source.max_rows, Number(persisted.row_limits[state.source.id]) || Math.min(1000, state.source.max_rows)));
    const gridRows = Math.max(6, Math.min(100, Number(state.prefs.gridRows) || 25));
    const defaultSchema = state.source ? (state.source.kind === 'oracle' ? (state.source.username || '').toUpperCase() : (state.source.database || '')) : '';
    host.innerHTML = '<div class="db-sql-layout">'
      + '<aside class="card db-meta" id="db-meta-pane">'
      + '<div class="db-pane-title"><span>数据库对象</span><div class="db-pane-actions"><button class="btn btn-xs" id="db-meta-refresh" title="刷新对象树">刷新</button><button class="btn btn-xs db-meta-toggle-btn" id="db-meta-toggle" title="收起对象栏 (' + h(state.prefs.shortcuts.objects || 'Alt+O') + ')" aria-label="收起数据库对象栏">' + actionIcon('panel') + '</button></div></div>'
      + '<button type="button" class="db-meta-collapsed-bar" id="db-meta-collapsed-bar" title="展开数据库对象 (' + h(state.prefs.shortcuts.objects || 'Alt+O') + ')" aria-label="展开数据库对象栏"><span class="db-meta-collapsed-icon">' + actionIcon('database') + '</span><span class="db-meta-collapsed-text">对象</span></button>'
      + '<label class="db-compact-label">Schema<select id="db-schema">'
      + (defaultSchema ? '<option value="' + h(defaultSchema) + '">' + h(defaultSchema) + '</option>' : '<option value="">加载中…</option>')
      + '</select></label>'
      + '<label class="sr-only" for="db-object-search">搜索数据库对象</label><input id="db-object-search" type="search" aria-label="搜索数据库对象" placeholder="搜索表、视图、函数、过程">'
      + '<div id="db-objects" class="db-object-list"><div class="db-tree-loading">正在读取元数据…</div></div>'
      + '</aside>'
      + '<div class="db-split-x" id="db-split-x" role="separator" title="左右拖动调整对象栏宽度"></div>'
      + '<main class="db-main">'
      + '<section class="card db-editor-card">'
      + '<div class="db-editor-tabs">'
      + '<div id="db-sql-tabs" class="db-sql-tabs"></div>'
      + '<button type="button" class="btn btn-xs" id="db-tab-add" title="新建查询页签">＋ 页签</button>'
      + '<span class="db-dialect">' + kindLabel(state.source.kind) + '</span>'
      + '</div>'
      + '<div id="db-sql-panel" class="db-sql-panel">'
      + '<div class="db-editor-bar">'
      + '<div class="db-bar-group db-bar-run-group">'
      + '<button class="btn db-btn-run" id="db-run" title="执行当前查询或选中 SQL (' + h(state.prefs.shortcuts.run || 'Ctrl+Enter') + ')">' + actionIcon('play') + '<span>执行</span><kbd class="db-run-kbd">' + h(state.prefs.shortcuts.run || 'Ctrl+Enter') + '</kbd></button>'
      + '<button class="btn" id="db-cancel" disabled title="取消查询 (Esc)">' + actionIcon('stop') + '<span>取消</span></button>'
      + '</div>'
      + '<div class="db-bar-divider"></div>'
      + '<div class="db-bar-group db-bar-trans-group">'
      + '<button class="btn db-btn-mode" id="db-toggle-edit" aria-pressed="false" title="开启或关闭结果网格编辑；不影响 SQL 编辑器的执行权限">' + actionIcon('lock') + '<span>网格编辑关闭</span></button>'
      + '<button class="btn db-btn-commit" id="db-btn-commit" disabled title="提交当前页签的 DML 事务和网格修改">' + actionIcon('commit') + '<span>提交</span></button>'
      + '<button class="btn db-btn-rollback" id="db-btn-rollback" disabled title="回滚当前页签的 DML 事务并放弃网格修改">' + actionIcon('rollback') + '<span>回滚</span></button>'
      + '</div>'
      + '<div class="db-bar-divider"></div>'
      + '<div class="db-bar-group db-bar-tools-group">'
      + '<button class="btn" id="db-btn-export-sql" title="导出当前页签 SQL 脚本">' + actionIcon('download') + '<span>导出 SQL</span></button>'
      + '<button class="btn" id="db-explain" title="查看执行计划">' + actionIcon('plan') + '<span>执行计划</span></button>'
      + '<button class="btn" id="db-format" title="格式化 SQL（选区优先/当前语句，Shift+点击格式化全文，Ctrl+Shift+F）">' + actionIcon('format') + '<span>格式化</span></button>'
      + '</div>'
      + '<div class="db-bar-divider"></div>'
      + '<div class="db-bar-group db-bar-limits-group">'
      + '<label class="db-bar-label">每页返回 <input id="db-max-rows" type="number" min="1" max="' + state.source.max_rows + '" value="' + savedRows + '" aria-label="当前查询每页返回行数"> 行</label>'
      + '<label class="db-bar-label">显示 <input id="db-grid-rows" type="number" min="6" max="100" value="' + gridRows + '"> 行</label>'
      + '</div>'
      + '<div class="db-bar-group db-bar-more-group">'
      + '<details id="db-toolbar-more" class="db-toolbar-more">'
      + '<summary class="db-toolbar-more-summary" title="扩展工具、收藏与历史">' + actionIcon('more') + '<span>更多</span><span class="db-more-chevron" aria-hidden="true">▾</span></summary>'
      + '<div class="db-toolbar-more-panel">'
      + '<div class="db-more-section">'
      + '<div class="db-more-title">SQL 美化</div>'
      + '<div class="db-more-row">'
      + '<button class="btn btn-xs" id="db-format-all" type="button" title="格式化整个编辑器的全部 SQL">' + actionIcon('format') + '<span>格式化全文</span></button>'
      + '</div>'
      + '</div>'
      + '<div class="db-more-section">'
      + '<div class="db-more-title">结果导出</div>'
      + '<div class="db-more-row">'
      + '<select id="db-export-format" title="导出格式" aria-label="结果导出格式"><option value="csv">CSV</option><option value="json">JSON</option><option value="xlsx">Excel</option><option value="insert">INSERT</option><option value="update">UPDATE</option></select>'
      + '<button class="btn" id="db-export" disabled>' + actionIcon('download') + '<span>导出结果</span></button>'
      + '</div>'
      + '</div>'
      + '<div class="db-more-section">'
      + '<div class="db-more-title">SQL 收藏夹</div>'
      + '<div class="db-more-row">'
      + '<select id="db-bookmark" title="SQL 收藏夹" aria-label="SQL 收藏夹"><option value="">选择收藏…</option></select>'
      + '<button class="btn btn-xs" id="db-bookmark-grid" type="button" title="九宫格查看收藏（多条时更清晰）">' + actionIcon('grid') + '<span>九宫格</span></button>'
      + '<button class="btn btn-xs" id="db-bookmark-save" title="把当前 SQL 存入收藏夹">' + actionIcon('star') + '<span>收藏</span></button>'
      + '<button class="btn btn-xs" id="db-bookmark-del" title="删除当前收藏">' + actionIcon('trash') + '<span>删除</span></button>'
      + '</div>'
      + '</div>'
      + '<div class="db-more-section">'
      + '<div class="db-more-title">查询历史</div>'
      + '<div class="db-more-row">'
      + '<select id="db-history" title="查询历史" aria-label="查询历史"><option value="">选择历史…</option>' + history.map(function (sql, i) { return '<option value="' + i + '">' + h(sql.replace(/\s+/g, ' ').slice(0, 80)) + '</option>'; }).join('') + '</select>'
      + '<button class="btn btn-xs" id="db-history-clear" title="清空当前用户保存的全部查询历史">' + actionIcon('trash') + '<span>清空</span></button>'
      + '</div>'
      + '</div>'
      + '</div>'
      + '</details>'
      + '</div>'
      + '<div class="db-bar-spacer"></div>'
      + '<span id="db-query-status" class="db-query-status">就绪</span>'
      + '</div>'
      + '<div class="db-sql-shell">'
      + '<pre id="db-sql-highlight" class="db-sql-highlight" aria-hidden="true"></pre>'
      + '<label class="sr-only" for="db-sql">SQL 编辑器</label><textarea id="db-sql" class="db-sql-editor" aria-label="SQL 编辑器" spellcheck="false"></textarea>'
      + '<div id="db-sql-ac" class="db-sql-ac" hidden role="listbox" aria-label="SQL 补全"></div>'
      + '</div>'
      + '<div class="db-sql-resizer" id="db-sql-resizer" role="separator" title="上下拖动调整 SQL 输入框高度"><div class="db-sql-resizer-line"></div></div>'
      + '<div class="db-editor-help"><strong>事务说明：</strong>DML 按页签保留事务，必须点击“提交”或执行 COMMIT 才落库；“回滚”/ROLLBACK 可撤销。Oracle/MySQL DDL 遵循数据库自身的隐式提交规则。多页签可并行 · ' + h(state.prefs.shortcuts.run) + ' 执行 · ' + h(state.prefs.shortcuts.explain) + ' 计划</div>'
      + '</div>'
      + '<div id="db-object-viewer" class="db-object-viewer" hidden></div>'
      + '</section>'
      + '<section class="card db-results" id="db-results-section">'
      + '<div class="db-result-toolbar">'
      + '<div class="db-result-tabs" role="tablist" aria-label="结果视图"><button id="db-view-grid" class="db-view-btn active" role="tab" aria-selected="true">网格</button><button id="db-view-record" class="db-view-btn" role="tab" aria-selected="false">单行记录</button><button id="db-view-plan" class="db-view-btn" role="tab" aria-selected="false">执行计划</button></div>'
      + '<div class="db-result-filter-wrap"><label class="sr-only" for="db-result-filter">过滤当前结果</label><input id="db-result-filter" type="search" aria-label="过滤当前结果" placeholder="在当前结果中过滤…"></div>'
      + '<div class="db-result-actions"><button class="btn btn-xs" id="db-copy-columns" disabled>复制字段名</button><button class="btn btn-xs" id="db-column-manager" disabled>显示列</button></div>'
      + '<span class="db-copy-hint">单击选行 · 网格编辑关闭时双击进单行 · 开启后双击改单元格</span>'
      + '<span id="db-result-meta" class="db-result-meta">等待执行查询</span>'
      + '<div id="db-page-nav" class="db-page-nav" role="navigation" aria-label="查询结果分页">'
      + '<button class="btn btn-xs" id="db-page-prev" title="上一页" disabled>上一页</button>'
      + '<span class="db-page-num-wrap">第 <input id="db-page-input" class="db-page-input" type="number" min="1" value="1" aria-label="页码"> 页</span>'
      + '<button class="btn btn-xs" id="db-page-next" title="下一页" disabled>下一页</button>'
      + '<select id="db-page-size" class="db-page-size" title="每页行数" aria-label="每页行数"><option value="20">20 行/页</option><option value="50">50 行/页</option><option value="100">100 行/页</option><option value="200">200 行/页</option><option value="500">500 行/页</option><option value="1000">1000 行/页</option></select>'
      + '<span id="db-page-hint" class="muted"></span>'
      + '</div>'
      + '</div>'
      + '<div id="db-result-message" class="db-result-message" hidden></div>'
      + '<div id="db-result-grid" class="db-result-grid"></div>'
      + '<div class="db-grid-resizer" id="db-grid-resizer" role="separator" title="上下拖动调整结果表格高度"><div class="db-grid-resizer-line"></div></div>'
      + '</section>'
      + '</main>'
      + '</div>';
    q('db-sql').placeholder = initial;
    q('db-max-rows').value = savedRows;
    q('db-max-rows').title = '当前查询每页返回行数（数据源上限 ' + state.source.max_rows + '）';
    q('db-grid-rows').title = '结果网格一次显示多少行，可自定义';
    installDatabasePagination();
    bindPaneSplitters(host);
    ensureWorkbenchKeys();
    q('db-run').onclick = runQuery;
    q('db-explain').onclick = runExplain;
    q('db-cancel').onclick = cancelQuery;
    q('db-export').onclick = exportResult;
    q('db-format').onclick = (e) => formatCurrentSQL({ full: !!(e && e.shiftKey) });
    if (q('db-format-all')) q('db-format-all').onclick = () => formatCurrentSQL({ full: true });
    if (q('db-toggle-edit')) q('db-toggle-edit').onclick = function () {
      if (!canWriteDatabase()) { toast('当前账号只有查询权限', 'warn'); return; }
      const current = sess();
      if (!state.isEditMode && current) {
        if (current.sourceId && state.source && current.sourceId !== state.source.id) {
          toast('当前结果属于其他数据源，请在当前数据源重新查询后编辑', 'warn');
          return;
        }
        if (current.editPlan && !current.editPlan.canUpdate && !current.editPlan.canInsert) {
          toast(current.editPlan.reason || '当前查询结果不支持网格编辑', 'warn');
          return;
        }
      }
      state.isEditMode = !state.isEditMode;
      if (current) current.isEditMode = state.isEditMode;
      updateTransactionControls();
      refreshVisibleResult(true);
      toast(state.isEditMode ? '网格编辑已开启：双击单元格修改，完成后提交或放弃' : '网格编辑已关闭', 'ok');
    };
    if (q('db-btn-commit')) q('db-btn-commit').onclick = commitPendingEdits;
    if (q('db-btn-rollback')) q('db-btn-rollback').onclick = rollbackPendingEdits;
    if (q('db-btn-export-sql')) q('db-btn-export-sql').onclick = function () {
      const s = sess();
      const sql = (s && s.sql) || (q('db-sql') && q('db-sql').value) || '';
      if (!sql.trim()) { toast('当前页签 SQL 内容为空', 'warn'); return; }
      const sName = state.source ? state.source.name : 'db';
      const fname = sName + '_' + tabTitle(s || { id: 1 }) + '_' + Date.now() + '.sql';
      downloadBlob(new Blob([sql], { type: 'text/sql;charset=utf-8' }), fname);
      toast('SQL 脚本已导出：' + fname, 'ok');
    };
    q('db-bookmark-save').onclick = saveBookmark;
    if (q('db-bookmark-del')) q('db-bookmark-del').onclick = deleteSelectedBookmark;
    if (q('db-bookmark-grid')) q('db-bookmark-grid').onclick = openBookmarkGrid;
    if (q('db-bookmark')) {
      q('db-bookmark').onchange = function () {
        const item = loadBookmarks()[Number(this.value)];
        if (item) {
          insertSQLToEditor(item.sql);
          toast('已插入收藏 “' + item.name + '” 到光标处', 'ok');
        }
        this.value = '';
      };
    }
    q('db-copy-columns').onclick = copyVisibleColumnNames;
    q('db-sql').onkeydown = handleEditorKeydown;
    q('db-sql').addEventListener('compositionstart', function () { editorComposing = true; });
    q('db-sql').addEventListener('compositionend', function () { editorComposing = false; });
    q('db-sql').addEventListener('input', debounce(function () {
      backupDBSessions({ delay: 3000 });
    }, 800));
    if (!window._dbBackupTimer) {
      window._dbBackupTimer = setInterval(function () {
        if (typeof document !== 'undefined' && document.hidden) return;
        backupDBSessions({ delay: 0 });
      }, 30000);
    }
    bindSQLEditor();
    if (!state.sessions || state.sessions.length <= 1) restoreDBSessions();
    const active = sess();
    bindSession(active);
    renderTabs();
    updateTransactionControls();
    q('db-tab-add').onclick = addSession;
    refreshBookmarkSelect();
    q('db-meta-refresh').onclick = () => {
      state.schemaObjectsCache = {};
      state.schemaCategoryCache = {};
      state.schemaTableCache = {};
      loadSchemas(true);
    };
    q('db-schema').onchange = () => {
      state.schemaObjectsCache = {};
      renderCategoryList();
      const cur = q('db-schema') && q('db-schema').value;
      if (cur) warmupSchemaTables(cur);
    };
    q('db-object-search').oninput = debounce(function () {
      if (this.value.trim()) {
        loadObjects();
      } else {
        renderCategoryList();
      }
    }, 220);
    q('db-view-grid').onclick = () => setResultMode('grid');
    q('db-view-record').onclick = () => setResultMode('record');
    q('db-view-plan').onclick = () => setResultMode('plan');
    const resultGrid = q('db-result-grid');
    if (resultGrid) {
      resultGrid.addEventListener('wheel', function (e) {
        if (e.ctrlKey && Math.abs(e.deltaY) > 0) {
          const sc = resultGrid.querySelector('.db-table-scroll') || resultGrid.querySelector('.db-plan-table-wrap');
          if (sc && sc.scrollWidth > sc.clientWidth) {
            e.preventDefault();
            sc.scrollLeft += e.deltaY;
          }
        }
      }, { passive: false });
    }
    q('db-result-filter').oninput = debounce(function () {
      state.localFilter = this.value;
      const s = sess();
      if (s) s.localFilter = this.value;
      refreshVisibleResult();
    }, 120);
    q('db-column-manager').onclick = openColumnManager;
    q('db-max-rows').onchange = function () {
      const value = Math.max(1, Math.min(state.source.max_rows, Number(this.value) || 1));
      this.value = value;
      persisted.row_limits[state.source.id] = value;
      savePersisted();
      const s = sess();
      if (s) {
        s.pageSize = value;
        s.lastMaxRows = value;
      }
      const pageNav = q('db-page-nav');
      if (pageNav) {
        const sz = pageNav.querySelector('.db-page-size');
        if (sz) sz.value = String(value);
      }
    };
    q('db-grid-rows').onchange = function () {
      const value = Math.max(6, Math.min(100, Number(this.value) || 25));
      this.value = value;
      state.prefs.gridRows = value;
      savePrefs();
      applyGridHeight();
    };
    q('db-history').onchange = function () {
      const items = loadHistory();
      const sql = items[Number(this.value)];
      if (sql) { q('db-sql').value = sql; syncSQLEditor(); }
      this.value = '';
    };
    q('db-history-clear').onclick = function () {
      if (!persisted.history.length || confirm('清空当前用户保存的全部 SQL 查询历史？')) {
        persisted.history = [];
        savePersisted();
        refreshHistorySelect();
        toast('查询历史已清空', 'ok');
      }
    };
    loadSchemas();
  }

  async function loadSchemas(refresh) {
    state.schemasLoaded = true;
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/metadata/schemas?source_id=' + encodeURIComponent(source.id) + (refresh === true ? '&refresh=1' : ''));
      const schema = q('db-schema');
      if (token !== state.workspaceToken || !schema) return;
      schema.innerHTML = (data.schemas || []).map(x => '<option value="' + h(x.name) + '">' + h(x.name) + '</option>').join('');
      if (source.kind === 'mysql' && source.database && Array.from(schema.options).some(x => x.value === source.database)) schema.value = source.database;
      if (source.kind === 'oracle') {
        const preferred = (source.username || '').toUpperCase();
        if (preferred && Array.from(schema.options).some(x => x.value === preferred)) schema.value = preferred;
      }
      renderCategoryList();
      // 选库后静默轻量预热表名（仅 category=tables 单列索引），与左侧 DOM 解耦。
      if (schema.value) warmupSchemaTables(schema.value);
    } catch (e) {
      const o = q('db-objects'), schema = q('db-schema');
      if (token === state.workspaceToken && schema) schema.innerHTML = '<option value="">加载失败</option>';
      if (token === state.workspaceToken && o) {
        o.innerHTML = inlineError('元数据加载失败', e.message) + '<button class="btn btn-xs db-meta-retry" id="db-meta-retry">重试</button>';
        if (q('db-meta-retry')) q('db-meta-retry').onclick = () => loadSchemas(true);
      }
      showConnectionError(source, '读取数据库元数据失败：' + e.message);
    }
  }

  function renderCategoryList() {
    const objects = q('db-objects');
    if (!objects) return;
    const groups = [['tables', '表'], ['views', '视图'], ['functions', '函数'], ['procedures', '存储过程 / 包'], ['triggers', '触发器'], ['other', '其他对象']];
    objects.innerHTML = '<div class="db-cat-list">' + groups.map(g =>
      '<details class="db-tree-group" data-cat="' + g[0] + '"><summary><span class="db-tree-folder">' + actionIcon('folder') + '<span>' + h(g[1]) + '</span></span><small class="db-cat-badge">展开</small></summary><div class="db-cat-content"><div class="db-tree-loading">点击文件夹展开…</div></div></details>'
    ).join('') + '</div>';
    objects.querySelectorAll('.db-tree-group').forEach(group => {
      group.addEventListener('toggle', function () {
        if (this.open && !this._loaded) {
          loadCategoryObjects(this, this.dataset.cat);
        }
      });
    });
  }

  // 左侧仅承担对象导航：单击在右侧打开对象详情页签，双击生成常用 SQL。
  let objectClickTimer = null;
  function markObjectActive(btn) {
    const host = q('db-objects');
    if (host) host.querySelectorAll('.db-object.active').forEach(function (x) { x.classList.remove('active'); });
    if (btn) btn.classList.add('active');
  }
  function bindObjectButton(btn, schema) {
    btn.onclick = function () {
      if (objectClickTimer) clearTimeout(objectClickTimer);
      objectClickTimer = setTimeout(function () {
        objectClickTimer = null;
        markObjectActive(btn);
        openObjectTab(schema, btn.dataset.name, btn.dataset.type);
      }, 240);
    };
    btn.ondblclick = function (e) {
      if (e && e.preventDefault) e.preventDefault();
      if (objectClickTimer) { clearTimeout(objectClickTimer); objectClickTimer = null; }
      markObjectActive(btn);
      insertObjectSQL(schema, btn.dataset.name, btn.dataset.type);
    };
  }

  // 表名联想与左侧 DOM 完全解耦：选库后静默轻量预热 category=tables，
  // 存入全局内存 state.schemaTableCache[schema]，编辑器打字即时可用。
  async function warmupSchemaTables(schema) {
    const source = state.source;
    if (!source || !schema) return;
    state.schemaTableCache = state.schemaTableCache || {};
    if (state.schemaTableCache[schema]) return;
    const token = state.workspaceToken;
    try {
      const data = await api('GET', '/api/database/metadata/objects?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&category=tables');
      if (token !== state.workspaceToken) return;
      const names = (data.objects || []).map(x => x.name).filter(Boolean);
      state.schemaTableCache[schema] = names;
      state.metadataCache = state.metadataCache || {};
      state.metadataCache[schema] = Array.from(new Set([].concat(state.metadataCache[schema] || [], names)));
      // 同步预填分类缓存，展开表文件夹时可零请求秒开。
      state.schemaCategoryCache = state.schemaCategoryCache || {};
      state.schemaCategoryCache[schema + ':tables'] = data.objects || [];
    } catch (e) {
      // 静默预热失败不打扰用户：联想降级为空，展开文件夹时再按需重试。
    }
  }

  function renderCategoryItems(groupEl, schema, category, items) {
    const content = groupEl && groupEl.querySelector('.db-cat-content');
    const badge = groupEl && groupEl.querySelector('.db-cat-badge');
    groupEl._loaded = true;
    if (badge) badge.textContent = items.length;
    if (!items.length) {
      if (content) content.innerHTML = '<div class="hint db-tree-empty">无 ' + h(category) + '</div>';
      return;
    }
    if (content) {
      content.innerHTML = items.map(x => '<button class="db-object" data-name="' + h(x.name) + '" data-type="' + h(x.type) + '"><span class="db-object-icon">' + h(String(x.type || '?').slice(0, 1)) + '</span><span class="db-object-name">' + h(x.name) + '</span><small>' + h(x.type) + '</small></button>').join('');
      content.querySelectorAll('.db-object').forEach(btn => {
        bindObjectButton(btn, schema);
      });
    }
  }

  async function loadCategoryObjects(groupEl, category) {
    const token = state.workspaceToken, source = state.source, schema = currentSchema();
    if (!schema || !source || !groupEl) return;
    const content = groupEl.querySelector('.db-cat-content');
    if (content) content.innerHTML = '<div class="db-tree-loading">正在查询 ' + h(category) + '…</div>';
    try {
      state.schemaCategoryCache = state.schemaCategoryCache || {};
      const cacheKey = schema + ':' + category;
      let items = state.schemaCategoryCache[cacheKey];
      if (!items) {
        // 真·按需懒加载：仅针对该类别发起轻量查询，各自缓存。
        const data = await api('GET', '/api/database/metadata/objects?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&category=' + encodeURIComponent(category));
        const curSchema = currentSchema();
        if (token !== state.workspaceToken || curSchema !== schema || (state.source && state.source.id !== source.id)) return;
        items = data.objects || [];
        state.schemaCategoryCache[cacheKey] = items;
        if (category === 'tables') {
          state.schemaTableCache = state.schemaTableCache || {};
          const names = items.map(x => x.name).filter(Boolean);
          if (!state.schemaTableCache[schema] || !state.schemaTableCache[schema].length) state.schemaTableCache[schema] = names;
          state.metadataCache = state.metadataCache || {};
          state.metadataCache[schema] = Array.from(new Set([].concat(state.metadataCache[schema] || [], names)));
        }
      }
      const curSchema = currentSchema();
      if (token !== state.workspaceToken || curSchema !== schema || (state.source && state.source.id !== source.id)) return;
      renderCategoryItems(groupEl, schema, category, items);
    } catch (e) {
      groupEl._loaded = false;
      const curSchema = currentSchema();
      if (token === state.workspaceToken && curSchema === schema && content) content.innerHTML = inlineError('查询失败', e.message);
    }
  }

  async function loadObjects() {
    state.metaSeq = (state.metaSeq || 0) + 1;
    const seq = state.metaSeq;
    const token = state.workspaceToken, source = state.source, schema = currentSchema(), objects = q('db-objects');
    if (!schema || !source) return;
    if (objects) objects.innerHTML = '<div class="db-tree-loading">正在读取对象…</div>';
    try {
      const search = q('db-object-search');
      const hasSearch = !!(search && search.value.trim());
      const data = await api('GET', '/api/database/metadata/objects?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&search=' + encodeURIComponent(hasSearch ? search.value : ''));
      const curSchema = currentSchema();
      if (seq !== state.metaSeq || token !== state.workspaceToken || !objects || curSchema !== schema || (state.source && state.source.id !== source.id)) return;
      const groups = [['tables', '表'], ['views', '视图'], ['functions', '函数'], ['procedures', '存储过程 / 包'], ['triggers', '触发器'], ['other', '其他对象']];
      const by = {};
      (data.objects || []).forEach(x => (by[x.category || 'other'] = by[x.category || 'other'] || []).push(x));
      objects.innerHTML = groups.filter(g => by[g[0]] && by[g[0]].length).map(g => '<details class="db-tree-group"' + (hasSearch ? ' open' : '') + '><summary><span class="db-tree-folder">' + actionIcon('folder') + '<span>' + h(g[1]) + '</span></span><small>' + by[g[0]].length + '</small></summary><div class="db-cat-content">' + by[g[0]].map(x => '<button class="db-object" data-name="' + h(x.name) + '" data-type="' + h(x.type) + '"><span class="db-object-icon">' + h(String(x.type || '?').slice(0, 1)) + '</span><span class="db-object-name">' + h(x.name) + '</span><small>' + h(x.type) + '</small></button>').join('') + '</div></details>').join('') || '<div class="hint db-tree-empty">没有匹配对象</div>';
      objects.querySelectorAll('.db-object').forEach(btn => {
        bindObjectButton(btn, schema);
      });
    } catch (e) {
      const curSchema = currentSchema();
      if (seq === state.metaSeq && token === state.workspaceToken && objects && curSchema === schema) objects.innerHTML = inlineError('对象加载失败', e.message);
    }
  }

  async function loadInspect(schema, object, type) {
    if (!schema || schema === '加载中…' || schema === '加载失败' || isPlaceholderSchema(schema)) {
      schema = currentSchema();
    }
    state.inspectSeq = (state.inspectSeq || 0) + 1;
    const seq = state.inspectSeq;
    const token = state.workspaceToken, source = state.source, body = q('db-inspect-body');
    if (!source) return;
    if (body) body.innerHTML = '<div class="db-selected-object">' + h(object) + '<small>' + h(type) + '</small></div><div class="db-tree-loading">正在读取详情…</div>';
    try {
      const data = await api('GET', '/api/database/metadata/inspect?source_id=' + encodeURIComponent(source.id) + '&schema=' + encodeURIComponent(schema) + '&object=' + encodeURIComponent(object) + '&type=' + encodeURIComponent(type || ''));
      if (seq !== state.inspectSeq || token !== state.workspaceToken || (state.source && state.source.id !== source.id)) return;
      state.inspect = Object.assign({ object: object, type: type, schema: schema }, data.inspect || {});
      renderInspect();
    } catch (e) {
      if (seq === state.inspectSeq && token === state.workspaceToken && body) body.innerHTML = inlineError('对象详情加载失败', e.message);
    }
  }

  function renderInspect() {
    const body = q('db-inspect-body');
    if (!body) return;
    const info = state.inspect;
    if (!info) { body.innerHTML = '<div class="hint">单击对象查看字段、索引、约束和 DDL；双击生成查询。</div>'; return; }
    const tab = state.inspectTab;
    let html = '<div class="db-selected-object"><span>' + h(info.object) + '</span><div class="db-selected-actions"><button class="btn btn-xs" id="db-open-object-tab" title="在右侧打开大窗口">' + actionIcon('external') + '<span>大窗口</span></button><button class="btn btn-xs" id="db-copy-field-list">复制字段</button></div></div>';
    if (tab === 'indexes') {
      html += (info.indexes || []).length ? info.indexes.map(x => '<div class="db-field-row"><span>' + h(x.name) + (x.uniqueness === 'UNIQUE' ? ' <b>U</b>' : '') + '</span><small>' + h((x.columns || []).join(', ')) + '</small></div>').join('') : '<div class="hint">没有索引</div>';
    } else if (tab === 'constraints') {
      html += (info.constraints || []).length ? info.constraints.map(x => '<div class="db-field-row"><span>' + h(x.name) + '</span><small>' + h(x.type) + (x.columns ? ' · ' + x.columns : '') + '</small></div>').join('') : '<div class="hint">没有约束</div>';
    } else if (tab === 'ddl') {
      const raw = info.ddl || info.source_text || info.ddl_error || '没有 DDL';
      html += '<pre class="db-ddl mono">' + highlightSQL(formatSQL(raw)) + '</pre>';
    } else {
      const fields = info.fields || [];
      html += fields.map(x => '<button class="db-field-row" data-field="' + h(x.name) + '"><span>' + h(x.name) + (x.primary_key ? ' <b title="主键">PK</b>' : '') + (x.nullable ? '' : ' <b title="非空">•</b>') + (x.comment ? ' <small class="db-field-cmt" title="' + h(x.comment) + '">[' + h(x.comment) + ']</small>' : '') + '</span><small>' + h(x.definition || x.data_type) + '</small></button>').join('') || '<div class="hint">没有字段</div>';
    }
    body.innerHTML = html;
    const openBtn = q('db-open-object-tab');
    if (openBtn) openBtn.onclick = () => openObjectTab(info.schema || currentSchema(), info.object, info.type || '');
    const names = (info.fields || []).map(x => x.name);
    const copy = q('db-copy-field-list');
    if (copy) copy.onclick = () => copyDBText(names.join(', '), '已复制 ' + names.length + ' 个字段名');
    body.querySelectorAll('.db-field-row[data-field]').forEach(btn => {
      btn.ondblclick = () => insertAtCursor(q('db-sql'), quoteIdentifier(btn.dataset.field));
    });
  }

  function quoteIdentifier(v) {
    const src = effectiveSource();
    const quote = src && src.kind === 'mysql' ? '`' : '"';
    return quote + String(v).split(quote).join(quote + quote) + quote;
  }
  function insertObjectSQL(schema, object, type) {
    if (!schema || schema === '加载中…' || schema === '加载失败' || isPlaceholderSchema(schema)) {
      schema = currentSchema();
    }
    const name = (schema ? quoteIdentifier(schema) + '.' : '') + quoteIdentifier(object);
    const upper = String(type).toUpperCase();
    const srcKind = effectiveSource() ? effectiveSource().kind : (state.source ? state.source.kind : 'oracle');
    let sql = 'SELECT *\nFROM ' + name;
    if (upper === 'FUNCTION') sql = srcKind === 'oracle' ? 'SELECT ' + name + '() AS result FROM DUAL' : 'SELECT ' + name + '() AS result';
    else if (upper === 'PROCEDURE' || upper === 'PACKAGE') sql = '-- 为避免误执行，双击对象只生成调用模板。\n-- 请确认参数和目标环境后，移除下一行注释并执行：\n-- ' + (srcKind === 'oracle' ? 'BEGIN ' + name + '(); END;' : 'CALL ' + name + '();');
    else if (upper === 'TRIGGER') sql = '-- Trigger: ' + name;
    const e = q('db-sql'); e.value = sql; e.focus(); syncSQLEditor();
  }
  function inlineError(title, message) {
    return '<div class="db-inline-error"><strong>' + h(title) + '</strong><span>' + h(message) + '</span></div>';
  }

  function bindPaneSplitters(host) {
    const meta = q('db-meta-pane');
    const inspect = host.querySelector('.db-inspect');
    if (meta) {
      meta.style.width = (persisted.meta_width || 260) + 'px';
      if (persisted.meta_collapsed) meta.classList.add('is-collapsed');
    }
    if (inspect) inspect.style.flexBasis = (persisted.inspect_height || 220) + 'px';
    
    // Left panel collapse toggle
    const toggleBtn = q('db-meta-toggle');
    const collapsedBar = q('db-meta-collapsed-bar');
    const splitX = q('db-split-x'), splitY = q('db-split-y');

    function toggleMeta(collapsed) {
      if (!meta) return;
      if (collapsed === undefined) collapsed = !meta.classList.contains('is-collapsed');
      meta.classList.toggle('is-collapsed', collapsed);
      if (splitX) splitX.style.display = collapsed ? 'none' : '';
      persisted.meta_collapsed = collapsed;
      savePersisted();
      if (!collapsed && !state.schemasLoaded) {
        loadSchemas();
      }
    }
    if (toggleBtn) toggleBtn.onclick = () => toggleMeta();
    if (collapsedBar) collapsedBar.onclick = () => toggleMeta(false);
    if (persisted.meta_collapsed) toggleMeta(true);

    // SQL Editor vertical resizer
    const resizer = q('db-sql-resizer');
    const shell = q('db-sql-shell') || host.querySelector('.db-sql-shell');
    if (persisted.editor_height && shell) {
      shell.style.height = persisted.editor_height + 'px';
      const ta = q('db-sql'), hl = q('db-sql-highlight');
      if (ta) ta.style.height = persisted.editor_height + 'px';
      if (hl) hl.style.height = persisted.editor_height + 'px';
    }
    if (resizer && shell) {
      resizer.onpointerdown = function (e) {
        e.preventDefault();
        resizer.classList.add('dragging');
        const startY = e.clientY;
        const startH = shell.getBoundingClientRect().height;
        const move = function (ev) {
          const newH = Math.round(Math.max(80, Math.min(650, startH + (ev.clientY - startY))));
          shell.style.height = newH + 'px';
          const ta = q('db-sql'), hl = q('db-sql-highlight');
          if (ta) ta.style.height = newH + 'px';
          if (hl) hl.style.height = newH + 'px';
          persisted.editor_height = newH;
        };
        const up = function () {
          resizer.classList.remove('dragging');
          document.removeEventListener('pointermove', move);
          document.removeEventListener('pointerup', up);
          savePersisted();
        };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      };
    }
    if (splitX) {
      splitX.onpointerdown = function (e) {
        e.preventDefault();
        const startX = e.clientX, startW = meta ? meta.getBoundingClientRect().width : (persisted.meta_width || 260);
        const move = function (ev) {
          persisted.meta_width = Math.round(Math.max(META_WIDTH_MIN, Math.min(META_WIDTH_MAX, startW + ev.clientX - startX)));
          if (meta) meta.style.width = persisted.meta_width + 'px';
        };
        const up = function () { document.removeEventListener('pointermove', move); document.removeEventListener('pointerup', up); savePersisted(); };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      };
    }
    if (splitY) {
      splitY.onpointerdown = function (e) {
        e.preventDefault();
        const startY = e.clientY, startH = inspect ? inspect.getBoundingClientRect().height : (persisted.inspect_height || 220);
        const move = function (ev) {
          persisted.inspect_height = Math.round(Math.max(180, Math.min(560, startH + (startY - ev.clientY))));
          if (inspect) inspect.style.flexBasis = persisted.inspect_height + 'px';
        };
        const up = function () { document.removeEventListener('pointermove', move); document.removeEventListener('pointerup', up); savePersisted(); };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      };
    }
    // Result Grid vertical resizer
    const gridResizer = q('db-grid-resizer');
    if (gridResizer) {
      gridResizer.onpointerdown = function (e) {
        e.preventDefault();
        gridResizer.classList.add('dragging');
        const startY = e.clientY;
        const scroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-scroll');
        const startH = scroll ? scroll.getBoundingClientRect().height : ((Number(state.prefs.gridRows) || 25) * GRID_ROW_H + GRID_HEAD_H);
        const move = function (ev) {
          const delta = ev.clientY - startY;
          const newH = Math.max(220, Math.min(3200, startH + delta));
          const rows = Math.max(6, Math.min(100, Math.round((newH - GRID_HEAD_H) / GRID_ROW_H)));
          const input = q('db-grid-rows');
          if (input) input.value = rows;
          state.prefs.gridRows = rows;
          applyGridHeight();
        };
        const up = function () {
          gridResizer.classList.remove('dragging');
          document.removeEventListener('pointermove', move);
          document.removeEventListener('pointerup', up);
          savePrefs();
        };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      };
    }
    applyGridHeight();
  }
  function applyGridHeight() {
    const rows = Math.max(6, Math.min(100, Number(q('db-grid-rows') && q('db-grid-rows').value) || state.prefs.gridRows || 25));
    const scroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-scroll');
    const plan = q('db-result-grid') && q('db-result-grid').querySelector('.db-plan-text, .db-plan-table-wrap');
    const height = (rows * GRID_ROW_H + GRID_HEAD_H) + 'px';
    if (scroll) scroll.style.height = height;
    if (plan) plan.style.maxHeight = height;
    const topScroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-top-scroll');
    if (topScroll && scroll) {
      const needs = scroll.scrollWidth > scroll.clientWidth;
      topScroll.style.display = needs ? 'block' : 'none';
      if (needs && topScroll.firstElementChild) {
        topScroll.firstElementChild.style.width = scroll.scrollWidth + 'px';
      }
    }
    if (scroll && state.gridReady && state.resultMode === 'grid') paintGridRows(scroll, false);
  }
  let workbenchKeysBound = false;
  function ensureWorkbenchKeys() {
    if (workbenchKeysBound) return;
    workbenchKeysBound = true;
    document.addEventListener('keydown', onWorkbenchKey, true);
  }
  function onWorkbenchKey(e) {
    if ((location.hash || '').indexOf('database') < 0) return;
    if (!q('db-sql') && !q('db-workspace')) return;
    if (e.isComposing || editorComposing || e.keyCode === 229) return;
    if (state.managing && e.key === 'Escape') {
      e.preventDefault();
      closeManager();
      return;
    }
    if (document.body.classList.contains('has-open-overlay')) return;
    const id = e.target && e.target.id;
    if (id && String(id).indexOf('db-shortcut-') === 0) return;
    // 防冲突：补全浮层打开时 Escape 仅关闭浮层，严禁误杀正在运行的查询。
    if (e.key === 'Escape' && acState.open) {
      e.preventDefault();
      if (e.stopPropagation) e.stopPropagation();
      hideComplete();
      return;
    }
    if (e.key === 'F2' && state.resultMode === 'grid' && !e.target.closest('input, textarea, select')) {
      e.preventDefault();
      if (state.isEditMode) startCellEdit(state.selectedRow, state.selectedCol || 0);
      else toast('请先开启“网格编辑”再按 F2 修改单元格', 'warn');
      return;
    }
    if (matchesShortcut(e, state.prefs.shortcuts.run)) { e.preventDefault(); runQuery(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.explain)) { e.preventDefault(); runExplain(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.cancel) && sess() && sess().controller) { e.preventDefault(); cancelQuery(); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.grid)) { e.preventDefault(); setResultMode('grid'); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.record)) { e.preventDefault(); setResultMode('record'); return; }
    if (matchesShortcut(e, state.prefs.shortcuts.objects)) {
      e.preventDefault();
      const meta = q('db-meta-pane');
      const control = meta && meta.classList.contains('is-collapsed') ? q('db-meta-collapsed-bar') : q('db-meta-toggle');
      if (control) control.click();
      return;
    }
    if (matchesShortcut(e, (state.prefs.shortcuts && state.prefs.shortcuts.format) || 'Ctrl+Shift+F')) {
      e.preventDefault();
      formatCurrentSQL({ full: e.altKey });
      return;
    }
  }

  function isSnippetExpandKey(e, trigger) {
    if (!e || e.ctrlKey || e.altKey || e.metaKey) return false;
    if (trigger === 'Space') return e.key === ' ' || e.key === 'Space' || e.code === 'Space';
    if (trigger === 'Tab') return !e.shiftKey && (e.key === 'Tab' || e.code === 'Tab');
    if (trigger === 'Enter') return !e.shiftKey && (e.key === 'Enter' || e.code === 'Enter' || e.code === 'NumpadEnter');
    return false;
  }

  function parseSnippetsText(raw) {
    const lines = String(raw || '').split(/\r?\n/);
    const items = [];
    const seenKeys = new Set();
    const duplicates = [];
    let ignored = 0;
    let cur = null;

    for (let i = 0; i < lines.length; i++) {
      const rawLine = lines[i];
      const trimmed = rawLine.trim();

      if (!trimmed) continue;

      if (trimmed.startsWith('#') || trimmed.startsWith('--') || trimmed.startsWith('//') || trimmed.startsWith(';')) {
        const disMatch = trimmed.match(/^(?:#|--|\/\/|;)\s*\[(?:disabled|off|禁用)\]\s*([A-Za-z0-9_.-]+)(?:\s*[:=]\s*|\s*\t+\s*)(.*)$/i);
        if (disMatch) {
          const k = disMatch[1].trim();
          const v = disMatch[2].replace(/\\n/g, '\n');
          if (seenKeys.has(k) && !duplicates.includes(k)) duplicates.push(k);
          seenKeys.add(k);
          cur = { key: k, text: v, enabled: false };
          items.push(cur);
          continue;
        }
        continue;
      }

      const match = rawLine.match(/^\s*([A-Za-z0-9_.-]+)(?:\s*[:=]\s*|\s*\t+\s*)(.*)$/);
      if (match) {
        const k = match[1].trim();
        const v = match[2].replace(/\\n/g, '\n');
        if (seenKeys.has(k) && !duplicates.includes(k)) duplicates.push(k);
        seenKeys.add(k);
        cur = { key: k, text: v, enabled: true };
        items.push(cur);
      } else if (cur && (rawLine.startsWith(' ') || rawLine.startsWith('\t'))) {
        // 仅缩进行才视为上一条的多行续行：去首缩进、保留尾空格。
        // 非缩进的无分隔符行视为无效行计入 ignored，不再静默并入上一条，避免拼写错误污染模板。
        cur.text += '\n' + rawLine.replace(/^\s+/, '');
      } else {
        ignored++;
      }
    }

    return { items: items, duplicates: duplicates, ignored: ignored };
  }

  function formatSnippetsText(items) {
    if (!Array.isArray(items)) return '';
    return items
      .filter(x => x && String(x.key || '').trim())
      .map(x => {
        const k = String(x.key || '').trim();
        const rawText = String(x.text || '');
        const v = rawText.replace(/\r?\n/g, '\\n');
        const prefix = (x.enabled === false) ? '# [disabled] ' : '';
        return prefix + k + '=' + v;
      })
      .join('\n');
  }

  const SAMPLE_SNIPPETS_TEXT = [
    '# PL/SQL 格式常用 SQL 模板（每行一条：缩写=模板内容）',
    '# 支持 ${schema}、${table}、${cursor} 变量；多行 SQL 可用 \\n 表示',
    'sf=SELECT * FROM ',
    'sc=SELECT COUNT(*) FROM ',
    'w=WHERE ',
    'df=DELETE FROM ',
    'ob=ORDER BY ',
    'gb=GROUP BY ',
    'sel=SELECT *\\nFROM ${table}',
    'cnt=SELECT COUNT(*)\\nFROM ${table}',
    'ins=INSERT INTO ${table} () VALUES ()',
    'upd=UPDATE ${table} SET  WHERE '
  ].join('\n');

  function handleEditorKeydown(e) {
    if (e.isComposing || editorComposing || e.keyCode === 229) return;
    if (matchesShortcut(e, state.prefs.shortcuts.run) || matchesShortcut(e, state.prefs.shortcuts.explain) || matchesShortcut(e, state.prefs.shortcuts.cancel) || matchesShortcut(e, state.prefs.shortcuts.grid) || matchesShortcut(e, state.prefs.shortcuts.record) || matchesShortcut(e, state.prefs.shortcuts.objects)) {
      if (acState.open) hideComplete();
      e.preventDefault();
      return;
    }
    const trigger = state.prefs.expandKey || 'Space';
    if (acState.open) {
      if (e.key === 'ArrowDown') { e.preventDefault(); moveComplete(1); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); moveComplete(-1); return; }
      if (e.key === 'Escape') { e.preventDefault(); if (e.stopPropagation) e.stopPropagation(); hideComplete(); return; }
      const curItem = acState.items[acState.index];
      if (curItem && curItem.kind === 'snippet') {
        if (isSnippetExpandKey(e, trigger)) {
          e.preventDefault();
          expandSnippet(e.currentTarget);
          hideComplete();
          return;
        }
        if (e.key === 'Enter' || e.key === 'Tab') {
          hideComplete();
          return;
        }
      } else {
        if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault();
          acceptComplete();
          return;
        }
      }
    }
    if (isSnippetExpandKey(e, trigger)) {
      if (expandSnippet(e.currentTarget)) {
        e.preventDefault();
        hideComplete();
        return;
      }
    }
  }
  function isMacPlatform() {
    try {
      const probe = String((typeof navigator !== 'undefined' && (navigator.platform || '')) || '') + ' ' + String((typeof navigator !== 'undefined' && (navigator.userAgent || '')) || '');
      return /mac|iphone|ipad|darwin/i.test(probe);
    } catch (err) { return false; }
  }
  function matchesShortcut(e, spec) {
    if (!spec || !e) return false;
    const parts = String(spec).toLowerCase().replace(/\s+/g, '').split('+').filter(Boolean);
    let wantCtrl = false, wantAlt = false, wantShift = false, wantMeta = false, key = '';
    parts.forEach(function (part) {
      if (part === 'ctrl' || part === 'control' || part === 'controlleft') wantCtrl = true;
      else if (part === 'alt') wantAlt = true;
      else if (part === 'shift') wantShift = true;
      else if (part === 'meta' || part === 'cmd' || part === 'command') wantMeta = true;
      else key = part;
    });
    const actual = String(e.key || '').toLowerCase(), code = String(e.code || '').toLowerCase();
    const keyOk = actual === key
      || (key === 'enter' && (actual === 'enter' || code === 'enter' || code === 'numpadenter'))
      || (key === 'esc' && actual === 'escape')
      || (key === 'escape' && actual === 'escape')
      || (key === 'space' && (actual === ' ' || actual === 'space' || code === 'space'));
    if (!keyOk) return false;
    const altOk = !!e.altKey === wantAlt;
    const shiftOk = !!e.shiftKey === wantShift;
    // macOS 兼容：设置要求 Ctrl 时允许 Cmd（meta）匹配，保证 Cmd+Enter 可执行查询。
    if (wantCtrl && !wantMeta && isMacPlatform()) {
      const ctrlOk = !!(e.ctrlKey || e.metaKey);
      return ctrlOk && altOk && shiftOk;
    }
    return keyOk && !!e.ctrlKey === wantCtrl && altOk && shiftOk && !!e.metaKey === wantMeta;
  }
  function expandSnippet(editor) {
    const pos = editor.selectionStart, before = editor.value.slice(0, pos), match = before.match(/([A-Za-z0-9_.-]+)$/);
    if (!match) return false;
    const snippet = state.prefs.snippets.find(x => x.enabled !== false && x.key === match[1]);
    if (!snippet) return false;
    const schema = currentSchema(), start = pos - match[1].length;
    let replacement = String(snippet.text).split('${schema}').join(schema || '').split('${table}').join('table_name');
    const cursor = replacement.indexOf('${cursor}');
    replacement = replacement.split('${cursor}').join('');
    editor.setRangeText(replacement, start, pos, 'end');
    if (cursor >= 0) editor.setSelectionRange(start + cursor, start + cursor);
    hideComplete();
    syncSQLEditor();
    const s = sess();
    if (s) s.sql = editor.value;
    return true;
  }
  function insertAtCursor(editor, text) { editor.setRangeText(text, editor.selectionStart, editor.selectionEnd, 'end'); editor.focus(); syncSQLEditor(); }
  function insertSQLToEditor(text) {
    const ta = q('db-sql');
    if (!ta) return;
    const current = ta.value;
    if (!current.trim()) {
      ta.value = text;
    } else {
      const start = ta.selectionStart != null ? ta.selectionStart : current.length;
      const end = ta.selectionEnd != null ? ta.selectionEnd : start;
      const before = current.slice(0, start);
      const after = current.slice(end);
      const prefix = (before && !before.endsWith('\n') && !before.endsWith(' ')) ? '\n' : '';
      const suffix = (after && !after.startsWith('\n') && !after.startsWith(' ')) ? '\n' : '';
      ta.value = before + prefix + text + suffix + after;
      const nextPos = (before + prefix + text).length;
      ta.setSelectionRange(nextPos, nextPos);
    }
    syncSQLEditor();
    const s = sess();
    if (s) s.sql = ta.value;
    ta.focus();
  }
  const SQL_KEYWORDS = new Set(('select from where group by having order union all distinct as join left right inner outer full cross on using with values insert update delete create alter drop table view index into set limit offset fetch first next only rows rownum dual sysdate now case when then else end and or not in exists like between is null true false count sum avg min max cast nvl nvl2 decode coalesce ifnull substr substring instr length trim to_char to_date to_number varchar varchar2 number integer int date timestamp char clob blob begin procedure function package trigger connect start prior minus except intersect over partition desc asc inner show describe explain desc').split(/\s+/));
  const SQL_NEWLINE_BEFORE = new Set(['select', 'from', 'where', 'group', 'having', 'order', 'union', 'minus', 'except', 'intersect', 'join', 'left', 'right', 'inner', 'full', 'cross', 'outer', 'on', 'with', 'limit', 'offset', 'fetch', 'connect', 'start']);
  function tokenizeSQL(src) {
    const tokens = [];
    let i = 0;
    const text = String(src || '');
    while (i < text.length) {
      const c = text[i], next = text[i + 1] || '';
      if (c === '-' && next === '-') {
        const end = text.indexOf('\n', i);
        const take = end < 0 ? text.length : end;
        tokens.push({ type: 'comment', value: text.slice(i, take), start: i });
        i = take;
        continue;
      }
      if (c === '/' && next === '*') {
        const end = text.indexOf('*/', i + 2);
        const take = end < 0 ? text.length : end + 2;
        tokens.push({ type: 'comment', value: text.slice(i, take), start: i });
        i = take;
        continue;
      }
      // Oracle standalone / script delimiter
      if (c === '/') {
        let lineStart = text.lastIndexOf('\n', i - 1);
        lineStart = lineStart < 0 ? 0 : lineStart + 1;
        const before = text.slice(lineStart, i).trim();
        let lineEnd = text.indexOf('\n', i + 1);
        if (lineEnd < 0) lineEnd = text.length;
        const after = text.slice(i + 1, lineEnd).trim();
        if (before === '' && (after === '' || after.startsWith('--'))) {
          tokens.push({ type: 'punct', value: '/', start: i });
          i++;
          continue;
        }
      }
      // Oracle Q-quotes: q'[...]', Q'!...!', nq'[...]'
      let isQ = false, qLen = 0;
      if ((c === 'q' || c === 'Q') && next === "'") { isQ = true; qLen = 2; }
      else if ((c === 'n' || c === 'N') && (next === 'q' || next === 'Q') && text[i + 2] === "'") { isQ = true; qLen = 3; }
      if (isQ && i + qLen < text.length) {
        const op = text[i + qLen];
        const cl = ({ '[': ']', '{': '}', '(': ')', '<': '>' })[op] || op;
        const marker = cl + "'";
        const end = text.indexOf(marker, i + qLen + 1);
        if (end >= 0) {
          tokens.push({ type: 'string', value: text.slice(i, end + marker.length), start: i });
          i = end + marker.length;
          continue;
        }
      }
      // National character string: N'...' or n'...'
      if ((c === 'n' || c === 'N') && next === "'") {
        let j = i + 2;
        while (j < text.length) {
          if (text[j] === "'") {
            if (text[j + 1] === "'") { j += 2; continue; }
            j++; break;
          }
          j++;
        }
        tokens.push({ type: 'string', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      if (c === '\'' || c === '"' || c === '`') {
        let j = i + 1;
        while (j < text.length) {
          if (text[j] === c) {
            if (text[j + 1] === c) { j += 2; continue; }
            j++; break;
          }
          j++;
        }
        tokens.push({ type: 'string', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      if (c <= ' ') {
        let j = i + 1;
        while (j < text.length && text[j] <= ' ') j++;
        tokens.push({ type: 'space', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      if ((c >= '0' && c <= '9') || (c === '.' && next >= '0' && next <= '9')) {
        let j = i + 1;
        while (j < text.length && /[0-9.eE+-]/.test(text[j])) j++;
        tokens.push({ type: 'number', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      // Composite operators: :=, =>, **, <<, >>, ||, .., <=, >=, !=, <>
      if (c === ':' && next === '=') { tokens.push({ type: 'punct', value: ':=', start: i }); i += 2; continue; }
      if (c === '=' && next === '>') { tokens.push({ type: 'punct', value: '=>', start: i }); i += 2; continue; }
      if (c === '*' && next === '*') { tokens.push({ type: 'punct', value: '**', start: i }); i += 2; continue; }
      if (c === '<' && next === '<') { tokens.push({ type: 'punct', value: '<<', start: i }); i += 2; continue; }
      if (c === '>' && next === '>') { tokens.push({ type: 'punct', value: '>>', start: i }); i += 2; continue; }
      if (c === '|' && next === '|') { tokens.push({ type: 'punct', value: '||', start: i }); i += 2; continue; }
      if (c === '.' && next === '.') { tokens.push({ type: 'punct', value: '..', start: i }); i += 2; continue; }
      if (c === '<' && next === '=') { tokens.push({ type: 'punct', value: '<=', start: i }); i += 2; continue; }
      if (c === '>' && next === '=') { tokens.push({ type: 'punct', value: '>=', start: i }); i += 2; continue; }
      if (c === '!' && next === '=') { tokens.push({ type: 'punct', value: '!=', start: i }); i += 2; continue; }
      if (c === '<' && next === '>') { tokens.push({ type: 'punct', value: '<>', start: i }); i += 2; continue; }
      // Oracle Bind Variables: :serialno, :1
      if (c === ':' && (/[A-Za-z0-9_$#]/.test(next) || (next && next.charCodeAt(0) > 127))) {
        let j = i + 1;
        while (j < text.length && (/[A-Za-z0-9_$#]/.test(text[j]) || text.charCodeAt(j) > 127)) j++;
        tokens.push({ type: 'ident', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      // Identifiers / Keywords with Unicode / Chinese support throughout
      if (/[A-Za-z_$#]/.test(c) || c.charCodeAt(0) > 127) {
        let j = i + 1;
        while (j < text.length && (/[A-Za-z0-9_$#]/.test(text[j]) || text.charCodeAt(j) > 127)) j++;
        const value = text.slice(i, j);
        tokens.push({ type: SQL_KEYWORDS.has(value.toLowerCase()) ? 'kw' : 'ident', value: value, start: i });
        i = j;
        continue;
      }
      if (c === '<' || c === '>' || c === '!' || c === '=') {
        let j = i + 1;
        if (c === '<' && next === '>') j++;
        else if (next === '=') j++;
        tokens.push({ type: 'punct', value: text.slice(i, j), start: i });
        i = j;
        continue;
      }
      tokens.push({ type: 'punct', value: c, start: i });
      i++;
    }
    return tokens;
  }

  function highlightSQL(src, match) {
    if (typeof isLargeSQL === 'function' && isLargeSQL(src)) return h(src);
    try {
      return tokenizeSQL(src).map(function (tok) {
        const body = h(tok.value);
        let cls = '';
        if (tok.type === 'kw') cls = 'db-sql-kw';
        else if (tok.type === 'string') cls = 'db-sql-str';
        else if (tok.type === 'comment') cls = 'db-sql-cmt';
        else if (tok.type === 'number') cls = 'db-sql-num';
        if (tok.type === 'punct' && match && (tok.start === match.open || tok.start === match.close)) {
          cls = (match.open < 0 || match.close < 0) ? 'db-sql-br-bad' : 'db-sql-br';
        }
        return cls ? '<span class="' + cls + '">' + body + '</span>' : body;
      }).join('') || ' ';
    } catch (_) {
      return h(src);
    }
  }
  function completionPrefix(text, cursor) {
    const tokens = tokenizeSQL(text);
    for (let i = 0; i < tokens.length; i++) {
      const tok = tokens[i], end = tok.start + tok.value.length;
      if (cursor > tok.start && cursor <= end && (tok.type === 'string' || tok.type === 'comment')) return null;
    }
    const before = String(text || '').slice(0, cursor);
    const m = before.match(/[A-Za-z_$#][A-Za-z0-9_$#]*$/);
    if (!m) return null;
    return { prefix: m[0], start: cursor - m[0].length, end: cursor };
  }
  // SQL 上下文感知：FROM / JOIN / INTO / UPDATE / TABLE 后表名提权至最高，
  // 解决 SELECT * FROM ord 时 ORDER 压制 orders 的问题。
  function sqlTableContext(text, cursor) {
    const tokens = tokenizeSQL(String(text || '').slice(0, cursor));
    for (let i = tokens.length - 1; i >= 0; i--) {
      const tok = tokens[i];
      if (tok.type === 'space' || tok.type === 'comment') continue;
      // 当前前缀本身是一个 ident，往前再找一个有效 token。
      if (tok.type === 'ident' && tok.start + tok.value.length >= cursor - 64) {
        for (let j = i - 1; j >= 0; j--) {
          const prev = tokens[j];
          if (prev.type === 'space' || prev.type === 'comment') continue;
          const w = String(prev.value || '').toLowerCase();
          if (w === 'from' || w === 'join' || w === 'into' || w === 'update' || w === 'table') return true;
          return false;
        }
        return false;
      }
      const w = String(tok.value || '').toLowerCase();
      return w === 'from' || w === 'join' || w === 'into' || w === 'update' || w === 'table';
    }
    return false;
  }
  function suggestSQL(text, cursor, extras) {
    extras = extras || {};
    const ctx = completionPrefix(text, cursor);
    if (!ctx || ctx.prefix.length < 2) return { items: [], start: cursor, end: cursor };
    const needle = ctx.prefix.toLowerCase();
    const seen = new Set();
    const items = [];
    const add = function (label, kind, insert) {
      const key = String(label || '').toLowerCase();
      if (!key || seen.has(key) || key.indexOf(needle) !== 0) return;
      seen.add(key);
      items.push({ label: label, kind: kind, insert: insert || label });
    };
    (extras.snippets || []).forEach(function (s) {
      if (s && s.enabled !== false && s.key && String(s.key).toLowerCase() === needle) add(s.key, 'snippet', s.text);
    });
    SQL_KEYWORDS.forEach(function (k) { add(k.toUpperCase(), 'keyword'); });
    (extras.objects || []).forEach(function (n) { add(n, 'object'); });
    (extras.fields || []).forEach(function (n) { add(n, 'field'); });
    const tableCtx = sqlTableContext(text, cursor);
    items.sort(function (a, b) {
      const rank = tableCtx
        ? { snippet: 1, object: 0, field: 2, keyword: 3 }
        : { snippet: 0, keyword: 1, object: 2, field: 3 };
      return (rank[a.kind] - rank[b.kind]) || a.label.localeCompare(b.label);
    });
    return { items: items.slice(0, 12), start: ctx.start, end: ctx.end };
  }
  function matchBrackets(text, cursor) {
    if (!text || cursor < 0) return null;
    const cBefore = text[cursor - 1] || '';
    const cAt = text[cursor] || '';
    if ('()[]{}'.indexOf(cBefore) < 0 && '()[]{}'.indexOf(cAt) < 0) return null;
    const tokens = tokenizeSQL(text);
    const brackets = [];
    tokens.forEach(function (tok) {
      if (tok.type === 'punct' && '()[]{}'.indexOf(tok.value) >= 0) brackets.push({ ch: tok.value, start: tok.start });
    });
    let idx = -1;
    for (let i = 0; i < brackets.length; i++) {
      const b = brackets[i];
      if (cursor === b.start || cursor === b.start + 1) { idx = i; break; }
    }
    if (idx < 0) return null;
    const pairs = { '(': ')', '[': ']', '{': '}' };
    const opens = { ')': '(', ']': '[', '}': '{' };
    const ch = brackets[idx].ch;
    if (pairs[ch]) {
      let depth = 0;
      for (let i = idx; i < brackets.length; i++) {
        if (brackets[i].ch === ch) depth++;
        else if (brackets[i].ch === pairs[ch]) {
          depth--;
          if (depth === 0) return { open: brackets[idx].start, close: brackets[i].start };
        }
      }
      return { open: brackets[idx].start, close: -1 };
    }
    if (opens[ch]) {
      let depth = 0;
      for (let i = idx; i >= 0; i--) {
        if (brackets[i].ch === ch) depth++;
        else if (brackets[i].ch === opens[ch]) {
          depth--;
          if (depth === 0) return { open: brackets[i].start, close: brackets[idx].start };
        }
      }
      return { open: -1, close: brackets[idx].start };
    }
    return null;
  }
  function formatSQL(src, options) {
    if (typeof Kairo !== 'undefined' && Kairo.workbench && Kairo.workbench.sqlFormatService && typeof Kairo.workbench.sqlFormatService.formatSQL === 'function') {
      return Kairo.workbench.sqlFormatService.formatSQL(src, options);
    }
    const tokens = tokenizeSQL(src);
    let out = '', newline = true;
    tokens.forEach(function (tok) {
      if (tok.type === 'space') return;
      if (tok.type === 'comment') {
        if (!newline && !out.endsWith('\n')) out += ' ';
        out += tok.value;
        if (tok.value.indexOf('\n') >= 0 || tok.value.slice(0, 2) === '--') { out += '\n'; newline = true; }
        else newline = false;
        return;
      }
      const lower = tok.value.toLowerCase();
      const breakBefore = tok.type === 'kw' && SQL_NEWLINE_BEFORE.has(lower) && out && !newline;
      if (breakBefore) { out = out.replace(/[ \t]+$/, ''); out += '\n'; newline = true; }
      if (tok.type === 'punct' && tok.value === ',') {
        out += ',\n  ';
        newline = true;
        return;
      }
      const spacedOp = tok.type === 'punct' && /^[=<>!+\-*\/%]+$/.test(tok.value);
      const noSpaceBefore = tok.type === 'punct' && /^[),.;]$/.test(tok.value);
      if (!newline && !noSpaceBefore && !out.endsWith('(') && !out.endsWith('[') && !out.endsWith('.') && (tok.type !== 'punct' || spacedOp)) out += ' ';
      out += tok.type === 'kw' ? tok.value.toUpperCase() : tok.value;
      newline = tok.value === ';';
      if (tok.value === ';') { out += '\n'; newline = true; }
    });
    return out.trim();
  }
  function bindSQLEditor() {
    const ta = q('db-sql'), hl = q('db-sql-highlight');
    if (!ta || !hl) return;
    const shell = ta.closest('.db-sql-shell') || q('db-sql-shell');

    const setPlainMode = function (isPlain) {
      if (isPlain) {
        ta.classList.add('is-plain-editor');
        if (shell) shell.classList.add('is-plain-editor');
        hl.style.display = 'none';
        hl.innerHTML = '';
      } else {
        ta.classList.remove('is-plain-editor');
        if (shell) shell.classList.remove('is-plain-editor');
        hl.style.display = '';
      }
    };

    let syncTimer = 0;
    const doSync = function () {
      syncTimer = 0;
      try {
        const val = ta.value || '';
        if (isLargeSQL(val)) {
          setPlainMode(true);
          return;
        }
        setPlainMode(false);
        const match = matchBrackets(val, ta.selectionStart);
        hl.innerHTML = highlightSQL(val, match) + '\n';
        hl.scrollTop = ta.scrollTop;
        hl.scrollLeft = ta.scrollLeft;
      } catch (_) {
        // Fallback to plain mode so text is NEVER transparent / invisible
        setPlainMode(true);
      }
    };

    const sync = function () {
      if (syncTimer) cancelAnimationFrame(syncTimer);
      syncTimer = requestAnimationFrame(doSync);
    };

    // ResizeObserver: ensures shell and hl heights always stay 100% synchronized with ta
    if (typeof ResizeObserver !== 'undefined' && !ta._resizeObserver) {
      const ro = new ResizeObserver(function (entries) {
        for (const entry of entries) {
          const h = Math.round(ta.offsetHeight || (entry.borderBoxSize && entry.borderBoxSize[0] ? entry.borderBoxSize[0].blockSize : ta.clientHeight));
          if (h > 0) {
            if (shell && shell.style.height !== h + 'px') shell.style.height = h + 'px';
            if (hl.style.height !== h + 'px') hl.style.height = h + 'px';
            persisted.editor_height = h;
          }
        }
      });
      ro.observe(ta);
      ta._resizeObserver = ro;
    }

    ta.addEventListener('input', function () {
      sync();
      updateComplete(ta);
      const s = sess();
      if (s) s.sql = ta.value;
      clearTimeout(tabTitleTimer);
      tabTitleTimer = setTimeout(renderTabs, 160);
    });
    ta.addEventListener('scroll', function () {
      if (hl.style.display !== 'none') {
        hl.scrollTop = ta.scrollTop;
        hl.scrollLeft = ta.scrollLeft;
      }
    });
    ta.addEventListener('keyup', sync);
    ta.addEventListener('click', sync);
    ta.addEventListener('select', sync);
    ta.addEventListener('blur', function () { setTimeout(hideComplete, 120); });
    ta._syncHighlight = function () { doSync(); };
    doSync();
  }
  function completionExtras() {
    const objSet = new Set();
    // 表名联想与 DOM 完全解耦：优先从后台预热的 schemaTableCache 取数，
    // 无论左侧对象栏折叠与否、是否展开过表文件夹，始终就绪。
    const schema = currentSchema();
    if (schema && state.schemaTableCache && state.schemaTableCache[schema]) {
      state.schemaTableCache[schema].forEach(function (name) { if (name) objSet.add(name); });
    }
    if (schema && state.metadataCache && state.metadataCache[schema]) {
      state.metadataCache[schema].forEach(function (name) { if (name) objSet.add(name); });
    }
    // DOM 仅作兜底补充（例如搜索模式下全量渲染的对象名）。
    document.querySelectorAll('#db-objects .db-object-name').forEach(function (el) { if (el.textContent) objSet.add(el.textContent); });
    const fields = ((state.inspect && state.inspect.fields) || []).map(function (f) { return f.name; });
    return { objects: Array.from(objSet), fields: fields, snippets: state.prefs.snippets || [] };
  }
  function hideComplete() {
    acState.open = false; acState.items = []; acState.index = 0;
    const box = q('db-sql-ac');
    if (box) { box.hidden = true; box.innerHTML = ''; }
  }
  function moveComplete(delta) {
    if (!acState.items.length) return;
    acState.index = (acState.index + delta + acState.items.length) % acState.items.length;
    renderComplete();
  }
  function acceptComplete() {
    const ta = q('db-sql');
    const item = acState.items[acState.index];
    if (!ta || !item) { hideComplete(); return; }
    if (item.kind === 'snippet') {
      expandSnippet(ta);
      hideComplete();
      return;
    }
    const insert = String(item.insert || item.label);
    ta.setRangeText(insert, acState.start, acState.end, 'end');
    hideComplete();
    syncSQLEditor();
    const s = sess();
    if (s) s.sql = ta.value;
  }
  function updateComplete(ta) {
    if (!ta || !ta.value) { hideComplete(); return; }
    const val = ta.value;
    const pos = ta.selectionStart;
    const windowStart = Math.max(0, pos - 2048);
    const windowEnd = Math.min(val.length, pos + 256);
    const isBig = val.length > 30000;
    const sliceText = isBig ? val.slice(windowStart, windowEnd) : val;
    const slicePos = isBig ? (pos - windowStart) : pos;
    const found = suggestSQL(sliceText, slicePos, completionExtras());
    if (!found.items.length) { hideComplete(); return; }
    const actualStart = isBig ? (found.start + windowStart) : found.start;
    const actualEnd = isBig ? (found.end + windowStart) : found.end;
    acState.open = true;
    acState.items = found.items;
    acState.start = actualStart;
    acState.end = actualEnd;
    acState.index = 0;
    renderComplete();
    positionComplete(ta, actualStart);
  }
  function renderComplete() {
    const box = q('db-sql-ac');
    if (!box) return;
    box.hidden = false;
    box.innerHTML = acState.items.map(function (item, i) {
      return '<button type="button" class="db-sql-ac-item' + (i === acState.index ? ' active' : '') + '" data-ac="' + i + '" role="option"><span>' + h(item.label) + '</span><small>' + h({ keyword: '关键字', snippet: '模板', object: '对象', field: '字段' }[item.kind] || item.kind) + '</small></button>';
    }).join('');
    box.querySelectorAll('[data-ac]').forEach(function (btn) {
      btn.onmousedown = function (e) { e.preventDefault(); acState.index = Number(btn.dataset.ac); acceptComplete(); };
    });
  }
  function positionComplete(ta, start) {
    const box = q('db-sql-ac');
    if (!box) return;
    const style = window.getComputedStyle(ta);
    const lineH = parseFloat(style.lineHeight) || 21;
    const padT = parseFloat(style.paddingTop) || 13;
    const padL = parseFloat(style.paddingLeft) || 15;
    const before = ta.value.slice(0, start).split('\n');
    const row = before.length - 1;
    const col = before[before.length - 1].length;
    const canvas = positionComplete.canvas || (positionComplete.canvas = document.createElement('canvas'));
    const ctx = canvas.getContext('2d');
    ctx.font = style.font;
    const left = padL + ctx.measureText(before[before.length - 1]).width - ta.scrollLeft;
    const top = padT + (row + 1) * lineH - ta.scrollTop;
    box.style.left = Math.max(8, Math.min(left, ta.clientWidth - 220)) + 'px';
    box.style.top = Math.max(8, top) + 'px';
  }
  function syncSQLEditor() {
    const ta = q('db-sql');
    if (ta) {
      const s = sess();
      if (s && s.type !== 'object') s.sql = ta.value;
      if (ta._syncHighlight) ta._syncHighlight();
      clearTimeout(tabTitleTimer);
      tabTitleTimer = setTimeout(renderTabs, 160);
    }
  }
  function formatCurrentSQL(options) {
    const ta = q('db-sql');
    if (!ta) return;
    if (!ta.value.trim()) return toast('编辑器为空', 'warn');
    options = options || {};
    if (typeof Kairo !== 'undefined' && Kairo.workbench && Kairo.workbench.sqlFormatService && typeof Kairo.workbench.sqlFormatService.formatInEditor === 'function') {
      const eff = effectiveSource() || {};
      const res = Kairo.workbench.sqlFormatService.formatInEditor(ta, {
        dialect: eff.kind || 'oracle',
        full: options.full
      });
      syncSQLEditor();
      if (res.status === 'empty') return toast('编辑器为空', 'warn');
      if (res.status === 'unchanged') return toast('SQL 无需格式化', 'info');
      toast(options.full ? '全文 SQL 已格式化' : (ta.selectionEnd > ta.selectionStart ? '选区 SQL 已格式化' : '当前语句已格式化'), 'ok');
      return;
    }
    const next = formatSQL(ta.value, options);
    if (!next) return toast('编辑器为空', 'warn');
    if (next === ta.value) return toast('SQL 无需格式化', 'info');
    ta.value = next;
    syncSQLEditor();
    toast('SQL 已格式化', 'ok');
  }
  function normalizeBookmarks(list) {
    if (!Array.isArray(list)) return [];
    return list.filter(function (x) {
      return x && typeof x === 'object' && typeof x.name === 'string' && typeof x.sql === 'string' && x.sql.length <= BOOKMARK_SQL_MAX;
    }).slice(0, BOOKMARK_LIMIT);
  }
  function loadBookmarks() { return normalizeBookmarks(persisted.bookmarks); }
  function refreshBookmarkSelect() {
    const select = q('db-bookmark');
    if (!select) return;
    const items = loadBookmarks(), current = select.value;
    select.innerHTML = '<option value="">收藏夹</option>' + items.map(function (item, i) {
      return '<option value="' + i + '">' + h(item.name) + '</option>';
    }).join('');
    select.value = current && items[Number(current)] ? current : '';
  }
  async function saveBookmark() {
    const sql = q('db-sql') && q('db-sql').value.trim();
    if (!sql) return toast('先输入要收藏的 SQL', 'warn');
    if (sql.length > BOOKMARK_SQL_MAX) return toast('SQL 超过收藏上限', 'warn');
    const fallback = sql.replace(/\s+/g, ' ').slice(0, 40);
    const name = Kairo.overlays && Kairo.overlays.prompt
      ? await Kairo.overlays.prompt({ title: '收藏 SQL 查询', label: '请输入收藏名称：', defaultValue: fallback })
      : (window.prompt('收藏名称', fallback) || '').trim();
    if (!name) return;
    const eff = effectiveSource();
    const items = loadBookmarks().filter(function (x) { return x.name !== name; });
    items.unshift({ name: name.slice(0, 80), sql: sql, source_id: eff && eff.id || '', updated_at: new Date().toISOString() });
    persisted.bookmarks = items.slice(0, BOOKMARK_LIMIT);
    savePersisted();
    refreshBookmarkSelect();
    q('db-bookmark').value = '0';
    toast('已收藏 “' + name.slice(0, 40) + '”', 'ok');
  }
  function deleteSelectedBookmark() {
    const select = q('db-bookmark');
    if (!select || select.value === '') return toast('先在收藏夹里选一条', 'warn');
    const items = loadBookmarks();
    const removed = items.splice(Number(select.value), 1)[0];
    persisted.bookmarks = items;
    savePersisted();
    refreshBookmarkSelect();
    toast(removed ? '已删除收藏 “' + removed.name + '”' : '收藏已删除', 'ok');
  }
  function openBookmarkGrid() {
    const items = loadBookmarks();
    if (!items.length) return toast('收藏夹为空，先收藏几条 SQL', 'warn');
    const grid = el('div', { class: 'db-bookmark-grid' });
    const body = el('div', { class: 'db-bookmark-modal-body' }, [grid]);
    const footer = el('div', { class: 'db-bookmark-modal-actions' });
    const dlg = Kairo.overlays.modal({
      title: 'SQL 收藏夹 · 九宫格',
      width: 720,
      body: body,
      footer: footer
    });
    footer.appendChild(el('button', { class: 'btn', type: 'button', text: '关闭', onclick: function () { dlg.close(); } }));
    items.forEach(function (item) {
      const card = el('div', { class: 'db-bookmark-card', tabindex: '0', role: 'button', title: '点击插入或载入，右键删除' });
      // Preserve the source captured with this bookmark. Feature-layer
      // actions use it for deep links/new windows instead of the active tab's
      // current source.
      card.dataset.sourceId = item.source_id || '';
      card.appendChild(el('strong', { text: item.name }));
      card.appendChild(el('pre', { text: item.sql.slice(0, 200) }));
      const meta = el('small', { text: (item.source_id ? (state.sources.find(function (s) { return s.id === item.source_id; }) || {}).name || item.source_id : '通用') + ' · ' + (item.updated_at ? new Date(item.updated_at).toLocaleDateString() : '') });
      card.appendChild(meta);

      function applySourceAndLoad() {
        if (item.source_id) {
          const src = state.sources.find(function (s) { return s.id === item.source_id; });
          if (src) {
            const cur = sess();
            if (cur) cur.sourceId = src.id;
            state.source = src;
            const sel = q('db-source');
            if (sel) sel.value = src.id;
            renderWorkspace(true);
          }
        }
        const sql = q('db-sql');
        if (sql) {
          sql.value = item.sql;
          syncSQLEditor();
          const cur = sess();
          if (cur) cur.sql = item.sql;
        }
      }

      function deleteItem() {
        if (confirm('删除收藏 “' + item.name + '”？')) {
          const cur = loadBookmarks();
          const index = cur.findIndex(function (x) { return x.name === item.name && x.sql === item.sql; });
          if (index >= 0) cur.splice(index, 1);
          persisted.bookmarks = cur;
          savePersisted();
          refreshBookmarkSelect();
          card.remove();
          if (!cur.length) dlg.close();
          toast('已删除', 'ok');
        }
      }

      const actions = el('div', { class: 'db-bookmark-card-actions' }, [
        el('button', { class: 'btn btn-xs', type: 'button', text: '复制', onclick: function (e) {
          e.stopPropagation();
          copyDBText(item.sql, '已复制到剪贴板');
        } }),
        el('button', { class: 'btn btn-xs', type: 'button', text: '插入到光标', onclick: function (e) {
          e.stopPropagation();
          insertSQLToEditor(item.sql);
          dlg.close();
          toast('已插入收藏 “' + item.name + '” 到光标处', 'ok');
        } }),
        el('button', { class: 'btn btn-xs btn-primary', type: 'button', text: '载入', onclick: function (e) {
          e.stopPropagation();
          applySourceAndLoad();
          dlg.close();
          toast('已载入收藏 “' + item.name + '”', 'ok');
        } }),
        el('button', { class: 'btn btn-xs btn-danger', type: 'button', text: '删除', onclick: function (e) {
          e.stopPropagation();
          deleteItem();
        } })
      ]);
      card.appendChild(actions);

      card.onclick = function (e) {
        if (e.target.closest('button')) return;
        const ta = q('db-sql');
        if (ta && ta.value.trim()) {
          insertSQLToEditor(item.sql);
          toast('已插入收藏 “' + item.name + '” 到光标处', 'ok');
        } else {
          applySourceAndLoad();
          toast('已载入收藏 “' + item.name + '”', 'ok');
        }
        dlg.close();
      };
      card.onkeydown = function (e) { if (e.key === 'Enter') card.onclick(e); };
      card.oncontextmenu = function (e) {
        e.preventDefault();
        deleteItem();
      };
      grid.appendChild(card);
    });
  }

  function compactDatabaseToolbar() {
    // Deprecated: toolbar layout is natively structured in renderSQL template
  }
  function editorSQL() {
    const box = q('db-sql');
    if (!box) return '';
    let sql = box.value.trim();
    if (sql && Kairo.workbench && Kairo.workbench.statementModel) {
      const picked = Kairo.workbench.statementModel.normalize(box.value, box.selectionStart, box.selectionStart, box.selectionEnd, { dialect: (effectiveSource() || {}).kind });
      sql = picked.text;
    }
    if (sql) return sql;
    const sample = String(box.placeholder || '').trim();
    if (!sample) return '';
    box.value = sample;
    syncSQLEditor();
    return sample;
  }

  function sqlGuardInfo(sql) {
    const features = Kairo.databaseFeatures;
    const normalized = features && features.tokenizeSQL ? features.tokenizeSQL(String(sql || '')).map(function (token) {
      return /^(comment|string|quoted-ident)$/.test(token.type) ? ' ' : token.value;
    }).join('').trim() : String(sql || '').trim();
    const first = ((normalized.match(/^([A-Za-z]+)/) || [])[1] || '').toUpperCase();
    let action = first;
    let actionIndex = 0;
    if (first === 'WITH') {
      const matches = Array.from(normalized.matchAll(/\b(COMMIT|ROLLBACK|DROP|TRUNCATE|INSERT|UPDATE|DELETE|MERGE|REPLACE)\b/gi));
      const last = matches[matches.length - 1];
      if (last) { action = last[1].toUpperCase(); actionIndex = last.index || 0; }
    }
    if (action === 'DROP' || action === 'TRUNCATE') {
      return { action: action, warning: action + ' 属于 DDL，Oracle/MySQL 会按数据库规则隐式提交，并可能造成不可恢复的数据或对象删除。' };
    }
    if ((action === 'UPDATE' || action === 'DELETE') && !/\bWHERE\b/i.test(normalized.slice(actionIndex + action.length))) {
      return { action: action, warning: action + ' 没有 WHERE 条件，将影响目标表的全部匹配行；执行后仍需手动提交。' };
    }
    let explainWrites = false;
    if (first === 'EXPLAIN') {
      const rest = normalized.slice(actionIndex + action.length).trim();
      if (/\b(INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|REPLACE|MERGE)\b/i.test(rest) || (/\bANALYZE\b/i.test(rest) && !/^(SELECT|SHOW|WITH|DESC|DESCRIBE)\b/i.test(rest.replace(/\bANALYZE\b/i, '').trim()))) {
        explainWrites = true;
      }
    }
    return { action: action, writes: explainWrites || !/^(SELECT|WITH|SHOW|DESC|DESCRIBE|EXPLAIN)$/.test(action) || /\bFOR\s+UPDATE\b/i.test(normalized) };
  }

  async function runQuery(keepPage) {
    const s = sess();
    if (!s || s.transactionBusy) return;
    if (s.controller) {
      toast('当前页签查询还在执行，请先取消或新开页签', 'warn');
      return;
    }
    const sql = editorSQL();
    if (!sql) return showQueryError('SQL 为空', '请输入要执行的 SQL 语句。');
    const guard = sqlGuardInfo(sql);
    if (guard.action === 'COMMIT') return commitPendingEdits();
    if (guard.action === 'ROLLBACK') return rollbackPendingEdits();
    const querySource = effectiveSource();
    if (!querySource) return showQueryError('未选择数据源', '请先在顶部选择数据源。');
    const writes = guard.writes || /^(INSERT|UPDATE|DELETE|MERGE|REPLACE)$/.test(guard.action);
    const isDDL = /^(CREATE|ALTER|DROP|TRUNCATE|RENAME|COMMENT)$/i.test(guard.action);
    if (isDDL) {
      if (!querySource.allow_ddl || querySource.read_only) {
        return showQueryError('未开启 DDL', '当前数据源未开启 DDL 支持（或处于服务端只读锁定）。请在“数据源管理”中勾选“支持 DDL”并取消“服务端只读”。');
      }
    } else {
      if (writes && (!canWriteDatabase() || querySource.read_only)) return showQueryError('只有查询权限', '当前账号或数据源不允许写入或加锁查询。');
    }
    if (writes && s.outcomeUnknown) {
      return showQueryError('提交结果未知', '当前页签上次提交结果未知，请先执行 SELECT 核对目标数据实际状态，切勿盲目再次写入！');
    }
    if (guard.warning && !confirm('危险 SQL 确认\n\n' + guard.warning + '\n\n确定继续执行吗？')) {
      toast('已取消危险 SQL', 'warn');
      return;
    }
    const dirtyCount = Object.keys(state.dirtyCells || {}).length;
    if (dirtyCount && !confirm('执行新查询将放弃当前页签的 ' + dirtyCount + ' 处未提交网格修改，确定继续吗？')) return;
    if (dirtyCount) {
      replaceDirtyCells({});
      updateTransactionControls();
    }
    const maxAllowed = Math.max(1, (querySource && querySource.max_rows) || 1000);
    const inputRows = Number(q('db-max-rows') && q('db-max-rows').value);
    const maxRows = Math.max(1, Math.min(maxAllowed, Number.isFinite(inputRows) && inputRows > 0 ? Math.floor(inputRows) : Math.min(1000, maxAllowed)));
    const prevSQL = s.lastSQL;
    s.sql = q('db-sql') ? q('db-sql').value : sql;
    s.lastSQL = sql;
    s.executedSQL = sql;
    s.resultSchema = currentSchema();
    s.lastMaxRows = maxRows;
    s.pageSize = maxRows;
    if (!keepPage || prevSQL !== sql) { s.page = 1; }
    s.page = Math.max(1, Number(s.page) || 1);
    s.rows = [];
    s.columns = [];
    s.summary = null;
    s.lastError = null;
    s.sort = null;
    s.hiddenColumns = new Set();
    s.selectedRow = 0;
    s.gridReady = false;
    s.plan = [];
    s.resultMode = 'grid';
    s.status = '查询中…';
    s.runSeq += 1;
    const seq = s.runSeq;
    bindSession(s);
    ['grid', 'record', 'plan'].forEach(function (name) {
      const btn = q('db-view-' + name);
      if (btn) {
        btn.classList.toggle('active', name === 'grid');
        btn.setAttribute('aria-selected', name === 'grid' ? 'true' : 'false');
      }
    });
    hideQueryMessage();
    renderResult();
    pushHistory(sql);
    s.runId = 'run-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7);
    if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.recordQueryStart === 'function') {
      Kairo.databaseFeatures.recordQueryStart(sql, s.runId, { sourceId: querySource.id, sourceName: querySource.name, dialect: querySource.kind });
    }
    // 内核兼容：IE/极老核无 AbortController/fetch 时给明确提示而非首行抛错
    if (typeof fetch !== 'function') {
      return showQueryError('浏览器过旧', '当前内核不支持 fetch，请用 360极速模式 / Chrome 打开。');
    }
    if (typeof AbortController !== 'undefined') {
      s.controller = new AbortController();
      state.controller = s.controller;
    } else {
      s.controller = null;
      state.controller = null;
    }
    refreshActiveQueryUI(s);
    const productionDML = String(querySource.environment || '').toLowerCase() === 'production' && (writes || isDDL);
    let productionConfirm = false;
    if (productionDML) {
      if (!confirm('当前数据源标记为生产环境，确认执行写入或加锁操作？\n\nDML 需要手动提交，DDL 可能由数据库隐式提交。')) {
        s.controller = null;
        state.controller = null;
        refreshActiveQueryUI(s);
        toast('已取消生产环境 DML', 'warn');
        return;
      }
      productionConfirm = true;
    }
    try {
      s.startTime = performance.now();
      s.firstRowsTime = 0;
      const queryBody = { source_id: querySource.id, session_id: s.transactionId, sql: sql, max_rows: maxRows, page: s.page, page_size: s.pageSize, count_mode: 'none', fast: true };
      // Bound values are optional so older servers remain compatible for
      // ordinary queries. New query handlers consume this typed array for
      // SELECT :name / {{name}} without exposing raw UI state.
      const boundParameters = Kairo.databaseFeatures && typeof Kairo.databaseFeatures.getBoundParameters === 'function' ? Kairo.databaseFeatures.getBoundParameters() : [];
      s.lastParameters = boundParameters || [];
      if (boundParameters && boundParameters.length) queryBody.parameters = boundParameters;
      if (productionConfirm) queryBody.confirm = true;
      const fetchOpts = { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(queryBody) };
      if (s.controller && s.controller.signal) fetchOpts.signal = s.controller.signal;
      const response = await fetch('/api/database/query', fetchOpts);
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(body.error || 'HTTP ' + response.status);
      }
      // 老核/兼容模式 response.body 可能为 null：降级为整包 JSON 解析
      if (!response.body || typeof response.body.getReader !== 'function') {
        const data = await response.json().catch(() => ({}));
        if (data && data.events && Array.isArray(data.events)) {
          data.events.forEach(function (ev) { consumeQueryEvent(s, seq, ev); });
        } else if (data && data.error) {
          throw new Error(data.error);
        } else {
          throw new Error('当前浏览器不支持流式读取(response.body)，请用极速模式/Chrome 重试。');
        }
      } else {
        const reader = response.body.getReader();
        let decoder;
        try { decoder = new TextDecoder(); } catch (_) { throw new Error('当前浏览器不支持 TextDecoder，请用极速模式/Chrome。'); }
        let pending = '';
        while (true) {
          const part = await reader.read();
          pending += decoder.decode(part.value || new Uint8Array(), { stream: !part.done });
          const lines = pending.split('\n'); pending = lines.pop();
          for (const line of lines) if (line.trim()) consumeQueryEvent(s, seq, JSON.parse(line));
          if (part.done) break;
        }
        if (pending.trim()) consumeQueryEvent(s, seq, JSON.parse(pending));
      }
    } catch (e) {
      if (s.runSeq !== seq) return;
      if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.recordQueryResult === 'function') {
        Kairo.databaseFeatures.recordQueryResult(s.runId, {
          sql: s.executedSQL || sql,
          sourceId: querySource ? querySource.id : s.sourceId,
          status: e.name === 'AbortError' ? 'canceled' : 'error',
          error: e.name === 'AbortError' ? '已取消' : (e.message || '查询失败')
        });
      }
      if (e.name !== 'AbortError') {
        s.lastError = { title: '查询执行失败', message: e.message, sql: sql };
        s.status = '执行失败';
        if (s.id === state.activeId) showQueryError('查询执行失败', e.message, sql);
        else toast('页签「' + tabTitle(s) + '」查询失败', 'err');
      } else {
        s.status = '已取消';
        if (s.id === state.activeId) showQueryMessage('warn', '查询已取消', '本次查询已终止，已接收的结果不会继续追加。');
      }
    } finally {
      if (s.runSeq === seq) {
        s.controller = null;
        if (s.id === state.activeId) state.controller = null;
        if (!s.status || s.status === '查询中…') s.status = s.summary ? (s.summary.rows + ' 行') : (s.lastError ? '执行失败' : '就绪');
        refreshActiveQueryUI(s);
        if (s.id === state.activeId) {
          const exp = q('db-export');
          if (exp) exp.disabled = s.rows.length === 0;
        }
      }
    }
  }

  async function runExplain() {
    const s = sess();
    const sql = editorSQL();
    if (!sql) return showQueryError('SQL 为空', '请输入查询语句后再查看执行计划。');
    hideQueryMessage();
    if (s) s.status = '分析计划…';
    if (q('db-query-status')) q('db-query-status').textContent = '分析计划…';
    const effSrc2 = effectiveSource();
    if (!effSrc2) return showQueryError('未选择数据源', '请先在顶部选择数据源。');
    try {
      const data = await api('POST', '/api/database/explain', { source_id: effSrc2.id, sql: sql });
      if (s) { s.plan = data.plan || []; s.status = '执行计划 ' + s.plan.length + ' 行'; }
      state.plan = data.plan || [];
      setResultMode('plan');
      if (q('db-query-status')) q('db-query-status').textContent = '执行计划 ' + state.plan.length + ' 行';
    } catch (e) {
      showQueryError('执行计划失败', e.message, sql);
    }
  }

  function installDatabasePagination() {
    const nav = q('db-page-nav');
    if (!nav) return;
    const prev = q('db-page-prev');
    const next = q('db-page-next');
    const pageInput = q('db-page-input');
    const size = q('db-page-size');

    function go(page) {
      const s = sess(); if (!s || s.controller) return;
      const targetPage = Math.max(1, Number(page) || 1);
      const dirtyCount = Object.keys(state.dirtyCells || {}).length;
      if (dirtyCount && !confirm('翻页将放弃当前页签的 ' + dirtyCount + ' 处未提交网格修改，确定继续吗？')) {
        updateDatabasePager();
        return;
      }
      if (dirtyCount) {
        replaceDirtyCells({});
        updateTransactionControls();
      }
      s.page = targetPage;
      s.pageSize = Math.max(1, Number(s.pageSize) || 20);
      s.lastMaxRows = s.pageSize;
      const max = q('db-max-rows'); if (max) max.value = s.pageSize;
      if (s.summary && s.summary.ordered === false && targetPage > 1) {
        toast('当前查询未指定 ORDER BY，跨页浏览可能出现重复或遗漏；已加载记录按原始身份提交。', 'info');
      }
      runQuery(true);
    }

    if (prev) prev.onclick = function () { const s = sess(); if (s) go((s.page || 1) - 1); };
    if (next) next.onclick = function () { const s = sess(); if (s) go((s.page || 1) + 1); };
    if (pageInput) {
      pageInput.onkeydown = function (e) { if (e.key === 'Enter') go(pageInput.value); };
      pageInput.onchange = function () { go(pageInput.value); };
    }
    if (size) {
      size.onchange = function () {
        const s = sess();
        if (s) {
          s.page = 1;
          s.pageSize = Math.max(1, Number(size.value) || 20);
          s.lastMaxRows = s.pageSize;
          const max = q('db-max-rows');
          if (max) max.value = s.pageSize;
          if (state.source && state.source.id) {
            persisted.row_limits[state.source.id] = s.pageSize;
            savePersisted();
          }
          go(1);
        }
      };
      const initialPageSize = (sess() && sess().pageSize) || (persisted.row_limits && state.source && persisted.row_limits[state.source.id]) || (q('db-max-rows') && Number(q('db-max-rows').value)) || 20;
      const curS = sess();
      if (curS && !curS.pageSize) curS.pageSize = Number(initialPageSize);
    }
    updateDatabasePager();
  }

  function updateDatabasePager() {
    const s = sess(), nav = q('db-page-nav'); if (!nav || !s) return;
    const summary = s.summary || {}, page = Math.max(1, Number(s.page) || Number(summary.page) || 1);
    const input = q('db-page-input') || nav.querySelector('.db-page-input');
    const prev = q('db-page-prev') || nav.querySelector('#db-page-prev');
    const next = q('db-page-next') || nav.querySelector('#db-page-next');
    const size = q('db-page-size') || nav.querySelector('.db-page-size');
    const hint = q('db-page-hint');
    if (input) input.value = String(page);
    if (size && s.pageSize) {
      const value = String(s.pageSize);
      Array.from(size.querySelectorAll('option[data-custom]')).forEach(option => option.remove());
      if (!Array.from(size.options).some(option => option.value === value)) {
        const option = document.createElement('option');
        option.value = value;
        option.textContent = value + ' 行/页';
        option.dataset.custom = 'true';
        size.appendChild(option);
      }
      size.value = value;
    }
    if (prev) prev.disabled = !!s.controller || page <= 1;
    if (next) next.disabled = !!s.controller || !summary.has_next;
    if (hint) hint.textContent = summary.rows != null && summary.page ? ('本页 ' + summary.rows + ' 行' + (summary.has_next ? ' · 还有下一页' : ' · 已到末页')) : '';
  }

  function consumeQueryEvent(s, seq, e) {
    if (!s || s.runSeq !== seq) return;
    if (e.type === 'meta') {
      s.columns = e.columns || [];
      s.resultId = e.result_id || '';
      s.editPlan = e.edit_plan || null;
      s.gridReady = false;
      if (s.id === state.activeId) { bindSession(s); renderResult(); }
    } else if (e.type === 'rows') {
      if (!s.firstRowsTime) {
        s.firstRowsTime = performance.now();
      }
      s.rows.push.apply(s.rows, e.rows || []);
      const firstPacketTime = Math.round(s.firstRowsTime - (s.startTime || s.firstRowsTime));
      s.status = '已返回 ' + s.rows.length + ' 行（首包 ' + firstPacketTime + ' ms）…';
      if (s.id === state.activeId) {
        bindSession(s);
        if (state.resultMode === 'grid' && state.gridReady) refreshVisibleResult();
        else renderResult();
        const st = q('db-query-status');
        if (st) st.textContent = s.status;
      }
    } else if (e.type === 'mutation') {
      s.rows = [];
      s.columns = [];
      s.summary = e.summary;
      s.transactionPending = !!(e.summary && e.summary.transaction_pending);
      s.status = e.message || '执行成功';
      if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.recordQueryResult === 'function') {
        Kairo.databaseFeatures.recordQueryResult(s.runId, {
          sql: s.executedSQL || s.lastSQL || s.sql,
          sourceId: s.sourceId,
          status: 'success',
          elapsedMs: (e.summary && e.summary.elapsed_ms != null) ? e.summary.elapsed_ms : (s.startTime ? Math.round(performance.now() - s.startTime) : 0),
          rows: (e.summary && e.summary.rows_affected != null) ? e.summary.rows_affected : 0
        });
      }
      if (s.id === state.activeId) {
        bindSession(s);
        const ddlAutoCommit = e.summary && (e.summary.statement_type === 'DDL_AUTOCOMMIT' || e.summary.statement_type === 'DDL');
        showQueryMessage('ok', ddlAutoCommit ? 'DDL 已执行（已自动提交）' : (s.transactionPending ? '执行成功（等待提交）' : '执行成功'), e.message || '语句执行完成');
        updateTransactionControls();
        if (q('db-query-status')) q('db-query-status').textContent = s.status;
      }
    } else if (e.type === 'notice') {
      // FOR UPDATE 等行锁提示：仅展示不中断流程
      if (s.id === state.activeId) {
        toast(e.message || 'FOR UPDATE 行锁状态已更新', 'warn');
        showQueryMessage('warn', '行锁提示', e.message || 'FOR UPDATE 行锁由当前页签事务管理，提交或回滚后释放。');
      }
    } else if (e.type === 'summary') {
      s.summary = e.summary;
      if (e.summary && e.summary.result_id) s.resultId = e.summary.result_id;
      if (e.summary && e.summary.edit_plan) s.editPlan = e.summary.edit_plan;
      if (e.summary && e.summary.page) { s.page = e.summary.page; s.pageSize = e.summary.page_size || s.pageSize; }
      const totalTime = s.startTime ? Math.round(performance.now() - s.startTime) : null;
      const firstPacketTime = (s.firstRowsTime && s.startTime) ? Math.round(s.firstRowsTime - s.startTime) : null;
      let timingStr = e.summary.elapsed_ms + ' ms';
      if (firstPacketTime !== null && totalTime !== null) {
        timingStr = e.summary.elapsed_ms + ' ms（首包 ' + firstPacketTime + ' ms / 总 ' + totalTime + ' ms）';
      }
      s.status = e.summary.rows + ' 行 · ' + timingStr + (e.summary.retry_count ? ' · 已自动重连' : '') + (e.summary.ordered === false ? ' · 未指定 ORDER BY' : '') + (e.summary.truncated ? ' · 已截断' : '');
      if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.recordQueryResult === 'function') {
        Kairo.databaseFeatures.recordQueryResult(s.runId, {
          sql: s.executedSQL || s.lastSQL || s.sql,
          sourceId: s.sourceId,
          status: 'success',
          elapsedMs: e.summary.elapsed_ms,
          rows: e.summary.rows
        });
      }
      if (s.id === state.activeId) {
        bindSession(s);
        refreshVisibleResult();
        updateDatabasePager();
        const st = q('db-query-status');
        if (st) st.textContent = s.status;
        if (e.summary && e.summary.message && e.summary.message.indexOf('FOR UPDATE') >= 0) {
          showQueryMessage('warn', '行锁提示', e.summary.message);
        }
      } else renderTabs();
    } else if (e.type === 'error') throw new Error(e.error || '查询失败');
  }
  function cancelQuery() {
    const s = sess();
    if (s && s.controller) s.controller.abort();
    else if (state.controller) state.controller.abort();
  }
  function hideQueryMessage() { const box = q('db-result-message'); if (box) { box.hidden = true; box.innerHTML = ''; } }
  function showQueryError(title, message, sql) {
    const s = sess();
    if (s) { s.lastError = { title: title, message: message, sql: sql }; s.status = '执行失败'; }
    state.lastError = { title, message, sql };
    showQueryMessage('error', title, message, sql);
    if (q('db-query-status')) q('db-query-status').textContent = '执行失败';
  }
  function showQueryMessage(kind, title, message, sql) {
    const box = q('db-result-message');
    if (!box) { toast(title + '：' + message, kind === 'error' ? 'err' : kind === 'ok' ? 'ok' : 'warn'); return; }
    const icon = kind === 'ok' ? '✓' : kind === 'warn' ? 'i' : '!';
    const copyLabel = kind === 'error' ? '复制错误详情' : '复制详情';
    box.hidden = false;
    box.className = 'db-result-message ' + kind;
    box.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    let extraActions = '';
    const isTTC = (message && (message.indexOf('TTC error') !== -1 || message.indexOf('TTC 协议') !== -1));
    let tableName = '', tableOwner = '';
    if (isTTC && sql) {
      const features = Kairo.databaseFeatures;
      if (features && features.resolveGridTarget(sql, '')) {
        const tokens = features.tokenizeSQL(sql).filter(function (token) { return token.type !== 'space' && token.type !== 'comment'; });
        const from = tokens.findIndex(function (token) { return token.type === 'keyword' && token.value.toUpperCase() === 'FROM'; });
        const name = function (token) { return token.type === 'quoted-ident' ? token.value.slice(1, -1).replace(/""/g, '"') : token.value.toUpperCase(); };
        tableName = name(tokens[from + 1]);
        if (tokens[from + 2] && tokens[from + 2].value === '.') { tableOwner = tableName; tableName = name(tokens[from + 3]); }
      }
    }
    if (tableName) {
      extraActions = '<button class="btn btn-xs btn-primary" id="db-inspect-ttc-cols" title="在编辑器自动生成查看该表字段结构的查询，排查致错大字段">查看 ' + h(tableName) + ' 字段结构</button>';
    }
    let tipsHTML = '';
    let diag = null;
    if (kind === 'error' && message) {
      diag = diagnoseConnectionError(message, state.source);
      if (diag && diag.category !== '连接异常') {
        tipsHTML = '<div class="db-conn-err-section" style="margin-top:8px;">'
          + '<span class="db-conn-err-label" style="display:flex;align-items:center;gap:6px;">排查建议 <span class="db-conn-err-badge">' + h(diag.category) + '</span></span>'
          + '<ul class="db-conn-tips-list">' + diag.tips.map(t => '<li>' + h(t) + '</li>').join('') + '</ul>'
          + '</div>';
      }
    }
    box.innerHTML = '<div class="db-message-icon" aria-hidden="true">' + icon + '</div><div class="db-message-copy"><strong>' + h(title) + '</strong><pre>' + h(message) + '</pre>' + (sql ? '<details><summary>查看本次 SQL</summary><pre>' + h(sql) + '</pre></details>' : '') + tipsHTML + '</div><div class="db-message-actions" style="display:flex;gap:6px;align-items:center;flex-shrink:0;">' + extraActions + '<button class="btn btn-xs" id="db-copy-message">' + copyLabel + '</button></div>';
    q('db-copy-message').onclick = () => copyDBText(title + '\n' + message + (sql ? '\n\n' + sql : '') + (diag && diag.category !== '连接异常' ? '\n\n[排查建议 (' + diag.category + ')]\n' + diag.tips.map((t, i) => (i + 1) + '. ' + t).join('\n') : ''), '详情已复制');
    if (tableName && q('db-inspect-ttc-cols')) {
      q('db-inspect-ttc-cols').onclick = () => {
        const inspectSQL = "SELECT column_name, data_type, data_length, nullable FROM all_tab_cols WHERE owner = " + (tableOwner ? "'" + tableOwner.replace(/'/g, "''") + "'" : 'USER') + " AND table_name = '" + tableName.replace(/'/g, "''") + "' ORDER BY column_id";
        const sqlBox = q('db-sql');
        if (sqlBox) {
          sqlBox.value = inspectSQL;
          if (typeof syncSQLEditor === 'function') syncSQLEditor();
          const s = sess();
          if (s) s.sql = inspectSQL;
          runQuery();
        }
      };
    }
  }

  function visibleColumns() { return state.columns.map((_, i) => i).filter(i => !state.hiddenColumns.has(i)); }
  function filteredRows() {
    const filter = state.localFilter, sort = state.sort;
    if (gridIndexCache.rows === state.rows && gridIndexCache.len === state.rows.length && gridIndexCache.filter === filter && gridIndexCache.sort === sort && gridIndexCache.indexes) {
      return gridIndexCache.indexes;
    }
    let indexes = state.rows.map((_, i) => i), needle = filter.trim().toLowerCase();
    if (needle) indexes = indexes.filter(i => state.rows[i].some(v => cellText(v).toLowerCase().includes(needle)));
    if (sort) {
      const { index, dir } = sort;
      indexes.sort((a, b) => cellText(state.rows[a][index]).localeCompare(cellText(state.rows[b][index]), undefined, { numeric: true, sensitivity: 'base' }) * dir);
    }
    gridIndexCache = { rows: state.rows, len: state.rows.length, filter: filter, sort: sort, indexes: indexes };
    return indexes;
  }
  function setResultMode(mode) {
    state.resultMode = mode;
    const s = sess();
    if (s) s.resultMode = mode;
    ['grid', 'record', 'plan'].forEach(name => {
      const btn = q('db-view-' + name);
      if (btn) {
        btn.classList.toggle('active', mode === name);
        btn.setAttribute('aria-selected', mode === name ? 'true' : 'false');
      }
    });
    state.gridReady = false;
    renderResult();
  }
  function updateResultMeta() {
    const indexes = filteredRows(), visible = visibleColumns(), meta = q('db-result-meta');
    if (meta) {
      if (state.resultMode === 'plan') meta.textContent = (state.plan || []).length + ' 步执行计划';
      else meta.textContent = state.columns.length ? visible.length + '/' + state.columns.length + ' 列 · ' + indexes.length + '/' + state.rows.length + ' 行' : '等待执行查询';
    }
    if (q('db-column-manager')) q('db-column-manager').disabled = !state.columns.length;
  }
  function refreshVisibleResult(force) {
    updateResultMeta();
    if (state.resultMode !== 'grid' || !state.gridReady) { renderResult(); return; }
    const scroll = q('db-result-grid') && q('db-result-grid').querySelector('.db-table-scroll');
    if (!scroll) return;
    setGridSlice(filteredRows(), visibleColumns());
    if (force) gridView.start = -1;
    paintGridRows(scroll, false);
  }
  function renderResult() {
    cancelGridPaint();
    const grid = q('db-result-grid'); if (!grid) return;
    updateResultMeta();
    if (state.resultMode === 'plan') { renderPlanView(grid); return; }
    if (!state.columns.length) { grid.innerHTML = '<div class="db-result-empty">执行查询后，结果和错误详情会显示在这里。</div>'; state.gridReady = false; return; }
    const indexes = filteredRows(), visible = visibleColumns();
    state.resultMode === 'record' ? renderRecordView(grid, indexes, visible) : renderGridView(grid, indexes, visible);
  }
  function renderPlanView(grid) {
    if (!state.plan.length) {
      grid.innerHTML = '<div class="db-result-empty">点击“执行计划”后，这里按 SQL*Plus / DBMS_XPLAN 原文展示优化器输出。</div>';
      return;
    }
    const tableRows = state.plan.filter(function (row) { return row.cells && row.column_order && row.column_order.length; });
    const raw = state.plan.map(function (row) { return row.raw || ''; }).join('\n');
    if (tableRows.length) {
      const cols = tableRows[0].column_order;
      const tsv = [cols.join('\t')].concat(tableRows.map(function (row) {
        return cols.map(function (col) { return row.cells[col] || ''; }).join('\t');
      })).join('\n');
      const head = cols.map(function (col) { return '<th>' + h(col) + '</th>'; }).join('');
      const body = tableRows.map(function (row) {
        return '<tr>' + cols.map(function (col) { return '<td>' + h(row.cells[col] || '') + '</td>'; }).join('') + '</tr>';
      }).join('');
      grid.innerHTML = '<div class="db-plan-wrap"><div class="db-plan-head"><strong>执行计划</strong><span>' + tableRows.length + ' 行 · MySQL EXPLAIN 表格</span><button class="btn btn-xs" id="db-copy-plan">复制</button></div><div class="db-plan-table-wrap"><table class="table db-plan-table"><thead><tr>' + head + '</tr></thead><tbody>' + body + '</tbody></table></div></div>';
      const copy = q('db-copy-plan');
      if (copy) copy.onclick = function () { copyDBText(tsv, '执行计划已复制'); };
    } else {
      grid.innerHTML = '<div class="db-plan-wrap"><div class="db-plan-head"><strong>执行计划</strong><span>' + state.plan.length + ' 行 · 保留数据库原始格式</span><button class="btn btn-xs" id="db-copy-plan">复制</button></div><pre class="db-plan-text">' + h(raw) + '</pre></div>';
      const copy = q('db-copy-plan');
      if (copy) copy.onclick = function () { copyDBText(raw, '执行计划已复制'); };
    }
    applyGridHeight();
  }
  let isSyncingScroll = false;
  function isColSelected(i) {
    return (state.selectedCols && state.selectedCols.has(i)) || state.colSelected === i;
  }
  function selectGridColumn(colIdx, isMulti, isRange) {
    if (!state.selectedCols) state.selectedCols = new Set();
    if (colIdx == null || colIdx < 0) {
      state.selectedCols.clear();
      state.colSelected = -1;
      state.lastColSelected = -1;
    } else if (isRange && state.lastColSelected >= 0) {
      const min = Math.min(state.lastColSelected, colIdx);
      const max = Math.max(state.lastColSelected, colIdx);
      state.selectedCols.clear();
      for (let i = min; i <= max; i++) {
        state.selectedCols.add(i);
      }
      state.colSelected = colIdx;
    } else if (isMulti) {
      if (state.selectedCols.has(colIdx)) {
        state.selectedCols.delete(colIdx);
        state.colSelected = state.selectedCols.size > 0 ? Array.from(state.selectedCols)[state.selectedCols.size - 1] : -1;
      } else {
        state.selectedCols.add(colIdx);
        state.colSelected = colIdx;
      }
      state.lastColSelected = colIdx;
    } else {
      state.selectedCols.clear();
      state.selectedCols.add(colIdx);
      state.colSelected = colIdx;
      state.lastColSelected = colIdx;
    }
    state.selectedCol = colIdx >= 0 ? colIdx : state.selectedCol;
    const grid = q('db-result-grid');
    if (!grid) return;
    grid.querySelectorAll('th.col-selected, td.col-selected').forEach(el => el.classList.remove('col-selected'));
    if (state.selectedCols && state.selectedCols.size > 0) {
      state.selectedCols.forEach(c => {
        grid.querySelectorAll('th[data-col="' + c + '"], td[data-col="' + c + '"]').forEach(el => el.classList.add('col-selected'));
      });
    }
  }
  function bindGridHeaders(grid) {
    grid.querySelectorAll('.db-col-sort-btn[data-sort]').forEach(btn => {
      btn.onclick = e => {
        e.stopPropagation();
        const i = Number(btn.dataset.sort);
        state.sort = state.sort && state.sort.index === i ? (state.sort.dir === 1 ? { index: i, dir: -1 } : null) : { index: i, dir: 1 };
        state.gridReady = false;
        renderResult();
      };
    });
    grid.querySelectorAll('.db-col-name[data-col]').forEach(btn => {
      btn.onclick = e => {
        e.stopPropagation();
        selectGridColumn(Number(btn.dataset.col), e.ctrlKey || e.metaKey, e.shiftKey);
      };
    });
    grid.querySelectorAll('th[data-col]').forEach(th => {
      th.onclick = e => {
        if (e.target.closest('.db-col-sort-btn') || e.target.closest('.db-col-resizer')) return;
        selectGridColumn(Number(th.dataset.col), e.ctrlKey || e.metaKey, e.shiftKey);
      };
      th.oncontextmenu = e => {
        e.preventDefault();
        const col = Number(th.dataset.col);
        if (!state.selectedCols || !state.selectedCols.has(col)) {
          if (!e.ctrlKey && !e.metaKey) {
            selectGridColumn(col, false, false);
          }
        }
        openResultMenu(e.clientX, e.clientY, col, null);
      };
    });
    const numTh = grid.querySelector('th.num');
    if (numTh) {
      numTh.onclick = () => selectGridColumn(-1);
    }
    grid.querySelectorAll('[data-resize]').forEach(x => bindColumnResize(x, Number(x.dataset.resize)));
  }
  function bindScrollSync(grid) {
    const topScroll = grid.querySelector('.db-table-top-scroll');
    const scroll = grid.querySelector('.db-table-scroll');
    if (!topScroll || !scroll) return;

    function onTableCtrlWheel(e) {
      if (e.ctrlKey && Math.abs(e.deltaY) > 0) {
        e.preventDefault();
        scroll.scrollLeft += e.deltaY;
      }
    }
    scroll.addEventListener('wheel', onTableCtrlWheel, { passive: false });
    topScroll.addEventListener('wheel', onTableCtrlWheel, { passive: false });

    function syncVisibility() {
      const needs = scroll.scrollWidth > scroll.clientWidth;
      topScroll.style.display = needs ? 'block' : 'none';
      if (needs && topScroll.firstElementChild) {
        topScroll.firstElementChild.style.width = scroll.scrollWidth + 'px';
        topScroll.scrollLeft = scroll.scrollLeft;
      }
    }
    syncVisibility();

    topScroll.onscroll = function () {
      if (isSyncingScroll) return;
      isSyncingScroll = true;
      scroll.scrollLeft = topScroll.scrollLeft;
      isSyncingScroll = false;
    };

    scroll.addEventListener('scroll', function () {
      if (!isSyncingScroll) {
        isSyncingScroll = true;
        topScroll.scrollLeft = scroll.scrollLeft;
        isSyncingScroll = false;
      }
    }, { passive: true });
  }
  function renderGridView(grid, indexes, visible) {
    cancelGridPaint();
    setGridSlice(indexes, visible);
    const cols = '<col style="width:54px">' + visible.map(i => '<col data-col="' + i + '" style="width:' + gridColumnWidth(i) + 'px">').join('');
    const tableWidth = 54 + visible.reduce(function (sum, i) { return sum + gridColumnWidth(i); }, 0);
    const headers = visible.map(i => {
      const c = state.columns[i];
      const isSorted = state.sort && state.sort.index === i;
      const isAsc = isSorted && state.sort.dir === 1;
      const isDesc = isSorted && state.sort.dir === -1;
      const sortBadge = isAsc ? '▲' : '▼';
      const sortCls = isSorted ? (' active' + (isAsc ? ' is-asc' : ' is-desc')) : '';
      const sortTitle = isAsc ? '当前升序，点击切换为降序' : (isDesc ? '当前降序，点击取消排序' : '点击按此列排序');
      const thCls = isColSelected(i) ? ' class="col-selected"' : '';
      return '<th data-col="' + i + '"' + thCls + ' title="' + h(c.database_type || '') + '">'
        + '<div class="db-col-header">'
        + '<button type="button" class="db-col-name" data-col="' + i + '" title="点击选中整列: ' + h(c.name) + '">' + h(c.name) + '</button>'
        + '<button type="button" class="db-col-sort-btn' + sortCls + '" data-sort="' + i + '" title="' + sortTitle + '">' + sortBadge + '</button>'
        + '</div>'
        + '<span class="db-col-resizer" data-resize="' + i + '"></span>'
        + '</th>';
    }).join('');
    grid.innerHTML = '<div class="db-table-top-scroll"><div class="db-table-top-scroll-inner" style="width:' + tableWidth + 'px"></div></div>'
      + '<div class="db-table-scroll"><table class="table db-table" style="width:' + tableWidth + 'px"><colgroup>' + cols + '</colgroup><thead><tr><th class="num" title="点击取消列选择">#</th>' + headers + '</tr></thead><tbody id="db-result-body"></tbody></table></div>';
    const scroll = grid.querySelector('.db-table-scroll');
    const body = q('db-result-body');
    // 小结果集（行数<=25 且单元格总数<=500）直接渲染全部行；
    // 宽表（列数多）或较大行集强制进入虚拟化，避免几百列造成浏览器卡死
    if (indexes.length <= 25 && indexes.length * visible.length <= 500) {
      let html = '';
      for (let pos = 0; pos < indexes.length; pos++) {
        const ri = indexes[pos], row = state.rows[ri] || [];
        html += '<tr data-row="' + ri + '"' + (ri === state.selectedRow ? ' class="selected"' : '') + '><td class="num">' + (ri + 1) + '</td>' + visible.map(i => {
          const dirtyKey = ri + '_' + i;
          const isDirty = state.dirtyCells && state.dirtyCells[dirtyKey];
          const cellVal = isDirty ? isDirty.newVal : row[i];
          const dirtyCls = isDirty ? ' db-cell-dirty' : '';
          const colSelectedCls = isColSelected(i) ? ' col-selected' : '';
          const cls = (dirtyCls + colSelectedCls).trim();
          return '<td data-row="' + ri + '" data-col="' + i + '"' + (cls ? ' class="' + cls + '"' : '') + ' title="' + (state.isEditMode ? '双击行内修改此单元格' : '双击单行；右键更多操作') + '">' + fmtCell(cellVal, ri, i) + '</td>';
        }).join('') + '</tr>';
      }
      body.innerHTML = html;
      bindGridBody(body);
      bindGridHeaders(grid);
      bindScrollSync(grid);
      state.gridReady = true;
      applyGridHeight();
      return;
    }
    scroll.addEventListener('scroll', function () { scheduleGridPaint(scroll); }, { passive: true });
    bindGridBody(body);
    bindGridHeaders(grid);
    bindScrollSync(grid);
    state.gridReady = true;
    applyGridHeight();
    paintGridRows(scroll, false);
  }
  function setGridSlice(indexes, visible) {
    gridView.indexes = indexes;
    gridView.visible = visible;
  }
  function gridSliceMarks(indexes, start, end) {
    if (!indexes.length || start >= end) return { head: null, tail: null, mid: null };
    return { head: indexes[start], tail: indexes[end - 1], mid: indexes[start + ((end - start) >> 1)] };
  }
  function updateGridSpacers(body, start, end, total, colspan) {
    const first = body.firstElementChild, last = body.lastElementChild;
    if (start) {
      const hpx = (start * GRID_ROW_H) + 'px';
      if (first && first.classList.contains('db-spacer') && first.firstElementChild) first.firstElementChild.style.height = hpx;
      else body.insertAdjacentHTML('afterbegin', '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + hpx + '"></td></tr>');
    } else if (first && first.classList.contains('db-spacer')) first.remove();
    if (end < total) {
      const hpx = ((total - end) * GRID_ROW_H) + 'px';
      const spacer = body.lastElementChild;
      if (spacer && spacer.classList.contains('db-spacer') && spacer.firstElementChild) spacer.firstElementChild.style.height = hpx;
      else body.insertAdjacentHTML('beforeend', '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + hpx + '"></td></tr>');
    } else if (last && last.classList.contains('db-spacer') && !(start && last === first)) last.remove();
  }
  function cancelGridPaint() {
    if (!gridView.raf) return;
    cancelAnimationFrame(gridView.raf);
    gridView.raf = 0;
  }
  function scheduleGridPaint(scroll) {
    if (gridView.raf) return;
    gridView.raf = requestAnimationFrame(function () {
      gridView.raf = 0;
      if (scroll && scroll.isConnected) paintGridRows(scroll, true);
    });
  }
  function bindGridBody(body) {
    if (!body) return;
    body.addEventListener('click', function (e) {
      const lob = e.target.closest('.db-lob-badge');
      if (lob && body.contains(lob)) {
        e.stopPropagation();
        const r = Number(lob.dataset.lobRow), c = Number(lob.dataset.lobCol);
        if (state.rows[r] && state.columns[c]) {
          openLobModal(state.rows[r][c], state.columns[c].name, r, c);
        }
        return;
      }
      const td = e.target.closest('td[data-col]');
      if (td && body.contains(td)) {
        const c = Number(td.dataset.col);
        state.selectedCol = c;
        if (e.ctrlKey || e.metaKey) {
          selectGridColumn(c, true, false);
        } else if (!e.shiftKey && state.selectedCols && state.selectedCols.size > 0 && !state.selectedCols.has(c)) {
          selectGridColumn(-1);
        }
      }
      const tr = e.target.closest('tr[data-row]');
      if (!tr || !body.contains(tr)) return;
      state.selectedRow = Number(tr.dataset.row);
      body.querySelectorAll('tr.selected').forEach(function (x) { x.classList.remove('selected'); });
      tr.classList.add('selected');
      if (!e.ctrlKey && !e.metaKey && !e.shiftKey && (!td || !td.hasAttribute('data-col'))) {
        if (state.colSelected >= 0) {
          selectGridColumn(-1);
        }
      }
    });
    body.addEventListener('dblclick', function (e) {
      const td = e.target.closest('td[data-col]');
      if (td && body.contains(td)) {
        e.stopPropagation();
        const r = Number(td.dataset.row), c = Number(td.dataset.col);
        state.selectedRow = r;
        state.selectedCol = c;
        if (state.isEditMode && canWriteDatabase()) startCellEdit(r, c, td);
        else setResultMode('record');
        return;
      }
      const tr = e.target.closest('tr[data-row]');
      if (!tr || !body.contains(tr)) return;
      state.selectedRow = Number(tr.dataset.row);
      setResultMode('record');
    });
    body.addEventListener('contextmenu', function (e) {
      const td = e.target.closest('td[data-col]');
      if (!td || !body.contains(td)) return;
      e.preventDefault();
      const col = Number(td.dataset.col);
      state.selectedRow = Number(td.dataset.row);
      state.selectedCol = col;
      const tr = td.parentElement;
      if (tr) {
        body.querySelectorAll('tr.selected').forEach(function (x) { x.classList.remove('selected'); });
        tr.classList.add('selected');
      }
      if (!state.selectedCols || !state.selectedCols.has(col)) {
        if (!e.ctrlKey && !e.metaKey) {
          selectGridColumn(col, false, false);
        }
      }
      openResultMenu(e.clientX, e.clientY, col, Number(td.dataset.row));
    });
    body.addEventListener('keydown', function (e) {
      if (e.key === 'F2') {
        e.preventDefault();
        if (state.isEditMode && canWriteDatabase()) startCellEdit(state.selectedRow, state.selectedCol || 0);
        else toast('请先开启“网格编辑”再按 F2 修改单元格', 'warn');
      }
    });
  }
  function paintGridRows(scroll, fromScroll) {
    const body = q('db-result-body'); if (!body || !scroll) return;
    const indexes = gridView.indexes, visible = gridView.visible, colspan = visible.length + 1;
    const count = Math.max(1, Math.ceil((scroll.clientHeight || 0) / GRID_ROW_H));
    const vBuffer = visible.length > 50 ? 4 : GRID_BUFFER;
    const start = Math.max(0, Math.floor((scroll.scrollTop || 0) / GRID_ROW_H) - vBuffer);
    const end = Math.min(indexes.length, start + count + vBuffer * 2);
    const marks = gridSliceMarks(indexes, start, end);

    // 水平虚拟化 / 可见列窗口（针对 600+ 列宽表优化，只渲染视口内可见列）
    let cStart = 0, cEnd = visible.length;
    if (visible.length > 40) {
      const scrollLeft = scroll.scrollLeft || 0;
      const clientWidth = scroll.clientWidth || 1000;
      let cumX = 54; // # 序号列宽度
      let foundStart = false;
      for (let ci = 0; ci < visible.length; ci++) {
        const colIdx = visible[ci];
        const w = gridColumnWidth(colIdx);
        if (!foundStart && (cumX + w >= scrollLeft - 250)) {
          cStart = Math.max(0, ci - 2);
          foundStart = true;
        }
        if (foundStart && (cumX > scrollLeft + clientWidth + 250)) {
          cEnd = Math.min(visible.length, ci + 3);
          break;
        }
        cumX += w;
      }
    }

    const sameWindow = gridView.start === start && gridView.end === end &&
      gridView.cStart === cStart && gridView.cEnd === cEnd &&
      gridView.head === marks.head && gridView.tail === marks.tail && gridView.mid === marks.mid;
    if (sameWindow && body.querySelector('tr[data-row]')) {
      if (fromScroll && gridView.length === indexes.length) return;
      if (gridView.length !== indexes.length) {
        updateGridSpacers(body, start, end, indexes.length, colspan);
        gridView.length = indexes.length;
      }
      return;
    }
    gridView.start = start;
    gridView.end = end;
    gridView.cStart = cStart;
    gridView.cEnd = cEnd;
    gridView.length = indexes.length;
    gridView.head = marks.head;
    gridView.tail = marks.tail;
    gridView.mid = marks.mid;

    let html = start ? '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + (start * GRID_ROW_H) + 'px"></td></tr>' : '';
    const sliceCols = (visible.length > 40) ? visible.slice(cStart, cEnd) : visible;
    const colSpanBefore = (visible.length > 40 && cStart > 0) ? cStart : 0;
    const colSpanAfter = (visible.length > 40 && cEnd < visible.length) ? (visible.length - cEnd) : 0;

    for (let pos = start; pos < end; pos++) {
      const ri = indexes[pos], row = state.rows[ri] || [];
      html += '<tr data-row="' + ri + '"' + (ri === state.selectedRow ? ' class="selected"' : '') + '><td class="num">' + (ri + 1) + '</td>';
      if (colSpanBefore > 0) html += '<td colspan="' + colSpanBefore + '" style="padding:0;border:none;"></td>';
      html += sliceCols.map(i => {
        const dirty = state.dirtyCells && state.dirtyCells[ri + '_' + i];
        const val = dirty ? dirty.newVal : row[i];
        const dirtyCls = dirty ? ' db-cell-dirty' : '';
        const colSelectedCls = isColSelected(i) ? ' col-selected' : '';
        const cls = (dirtyCls + colSelectedCls).trim();
        const cellTitle = state.isEditMode ? '双击编辑；右键复制或更多操作' : '双击打开单行记录；右键复制或更多操作';
        return '<td data-row="' + ri + '" data-col="' + i + '"' + (cls ? ' class="' + cls + '"' : '') + ' title="' + cellTitle + '">' + fmtCell(val, ri, i) + '</td>';
      }).join('');
      if (colSpanAfter > 0) html += '<td colspan="' + colSpanAfter + '" style="padding:0;border:none;"></td>';
      html += '</tr>';
    }
    if (end < indexes.length) html += '<tr class="db-spacer"><td colspan="' + colspan + '" style="height:' + ((indexes.length - end) * GRID_ROW_H) + 'px"></td></tr>';
    body.innerHTML = html;
  }
  function bindColumnResize(handle, index) {
    handle.onpointerdown = e => {
      e.preventDefault(); e.stopPropagation();
      const th = handle.parentElement, start = e.clientX, width = th.getBoundingClientRect().width;
      const move = ev => {
        state.columnWidths[index] = Math.max(72, Math.min(420, width + ev.clientX - start));
        const grid = q('db-result-grid');
        if (!grid) return;
        const col = grid.querySelector('col[data-col="' + index + '"]');
        if (col) col.style.width = state.columnWidths[index] + 'px';
        const visible = visibleColumns();
        const totalW = 54 + visible.reduce((sum, ci) => sum + gridColumnWidth(ci), 0);
        const tbl = grid.querySelector('.db-table');
        if (tbl) tbl.style.width = totalW + 'px';
        const topInner = grid.querySelector('.db-table-top-scroll-inner');
        if (topInner) topInner.style.width = totalW + 'px';
        const topScroll = grid.querySelector('.db-table-top-scroll');
        const scroll = grid.querySelector('.db-table-scroll');
        if (topScroll && scroll) {
          topScroll.style.display = scroll.scrollWidth > scroll.clientWidth ? 'block' : 'none';
        }
      };
      const up = () => { document.removeEventListener('pointermove', move); document.removeEventListener('pointerup', up); saveColumnWidths(); };
      document.addEventListener('pointermove', move);
      document.addEventListener('pointerup', up);
    };
  }
  function renderRecordView(grid, indexes, visible) {
    if (!indexes.includes(state.selectedRow)) state.selectedRow = indexes[0] == null ? 0 : indexes[0];
    if (!indexes.length) { grid.innerHTML = '<div class="db-result-empty">当前过滤条件没有匹配记录。</div>'; return; }
    const pos = indexes.indexOf(state.selectedRow), row = state.rows[state.selectedRow];
    grid.innerHTML = '<div class="db-record-nav"><button class="btn btn-xs" id="db-record-prev"' + (pos <= 0 ? ' disabled' : '') + '>上一条</button><strong>记录 ' + (pos + 1) + ' / ' + indexes.length + '（网格第 ' + (state.selectedRow + 1) + ' 行）</strong><button class="btn btn-xs" id="db-record-next"' + (pos >= indexes.length - 1 ? ' disabled' : '') + '>下一条</button><button class="btn btn-xs" id="db-record-copy-fields">复制字段名</button><button class="btn btn-xs" id="db-record-copy">复制整行</button></div><div class="db-record-table">' + visible.map(i => '<div class="db-record-field"><button data-copy-field="' + i + '" title="复制字段名">' + h(state.columns[i].name) + '</button><pre>' + h(cellText(row[i])) + '</pre></div>').join('') + '</div>';
    q('db-record-prev').onclick = () => { state.selectedRow = indexes[pos - 1]; renderResult(); };
    q('db-record-next').onclick = () => { state.selectedRow = indexes[pos + 1]; renderResult(); };
    q('db-record-copy').onclick = () => copyRow(state.selectedRow, visible);
    q('db-record-copy-fields').onclick = copyVisibleColumnNames;
    grid.querySelectorAll('[data-copy-field]').forEach(btn => btn.onclick = () => copyDBText(state.columns[Number(btn.dataset.copyField)].name, '字段名已复制'));
  }
  function copyVisibleColumnNames() {
    const names = visibleColumns().map(i => state.columns[i].name);
    copyDBText(names.join('\t'), '已复制 ' + names.length + ' 个字段名');
  }
  function copyRow(row, columns) {
    copyDBText(columns.map(i => cellText(state.rows[row][i])).join('\t'), '已复制整行');
  }
  function closeMenus(e) { document.querySelectorAll('.db-popup-menu').forEach(m => { if (!e || !m.contains(e.target)) m.remove(); }); }
  function keepMenu(menu) {
    requestAnimationFrame(() => {
      const r = menu.getBoundingClientRect();
      if (r.right > innerWidth - 8) menu.style.left = Math.max(8, innerWidth - r.width - 8) + 'px';
      if (r.bottom > innerHeight - 8) menu.style.top = Math.max(8, innerHeight - r.height - 8) + 'px';
    });
  }
  function openResultMenu(x, y, column, row) {
    closeMenus();
    const menu = el('div', { class: 'db-popup-menu', style: 'left:' + x + 'px;top:' + y + 'px' });
    const add = (text, fn) => menu.appendChild(el('button', { text, onclick: () => { fn(); menu.remove(); } }));

    // 获取当前选中的所有列索引（若用户多选了列并且右键点击的列在多选中）
    let activeCols = [];
    if (state.selectedCols && state.selectedCols.size > 1 && (column == null || state.selectedCols.has(column))) {
      activeCols = Array.from(state.selectedCols).filter(ci => state.columns[ci]).sort((a, b) => a - b);
    } else if (column != null && state.columns[column]) {
      activeCols = [column];
    }
    const isMultiCol = activeCols.length > 1;

    if (isMultiCol) {
      add('复制字段名（已选 ' + activeCols.length + ' 列）', () => copyDBText(activeCols.map(i => state.columns[i].name).join(', '), '已复制 ' + activeCols.length + ' 个字段名'));
    } else if (column != null && state.columns[column]) {
      add('复制字段名', () => copyDBText(state.columns[column].name, '字段名已复制'));
    }

    if (row != null) {
      if (isMultiCol) {
        add('复制单元格（TSV）', () => copyDBText(activeCols.map(i => cellText(state.rows[row][i])).join('\t'), '单元格已复制'));
        add('复制为 UPDATE 语句（已选 ' + activeCols.length + ' 列）', () => copyColsAsUpdate(row, activeCols));
      } else {
        add('复制单元格', () => copyDBText(cellText(state.rows[row][column]), '单元格已复制'));
        add('复制为 UPDATE 语句（单字段）', () => copyCellAsUpdate(row, column));
      }
      add('复制整行（TSV）', () => copyRow(row, visibleColumns()));
      add('复制为 INSERT 语句', () => copyRowAsInsert(row));
      add('复制整行为 UPDATE 语句', () => copyRowAsUpdate(row));
      add('导出选中行为 INSERT (.sql)', () => exportRowAsInsertFile(row));
      add('导出选中行为 UPDATE (.sql)', () => exportRowAsUpdateFile(row));
      add('导出选中行为 TXT (.txt)', () => exportRowAsTxtFile(row));
      if (state.isEditMode && canWriteDatabase()) {
        add('编辑此单元格 (F2)', () => startCellEdit(row, column));
        add('设为 SQL NULL', () => startCellEdit(row, column, null, true));
      }
    } else {
      // 表头右键
      const targetRow = (state.selectedRow != null && state.rows[state.selectedRow]) ? state.selectedRow : 0;
      if (state.rows && state.rows.length) {
        if (isMultiCol) {
          add('复制为 UPDATE 语句（已选 ' + activeCols.length + ' 列）', () => copyColsAsUpdate(targetRow, activeCols));
        } else if (column != null) {
          add('复制为 UPDATE 语句（单字段）', () => copyCellAsUpdate(targetRow, column));
        }
      }
    }

    if (isMultiCol) {
      add('复制已选整列（' + activeCols.length + ' 列 TSV）', () => {
        const lines = filteredRows().map(r => activeCols.map(c => cellText(state.rows[r][c])).join('\t'));
        copyDBText(lines.join('\n'), '已复制选中整列');
      });
      add('隐藏已选列', () => {
        activeCols.forEach(i => state.hiddenColumns.add(i));
        selectGridColumn(-1);
        state.gridReady = false;
        renderResult();
      });
    } else if (column != null) {
      add('复制整列', () => copyDBText(filteredRows().map(i => cellText(state.rows[i][column])).join('\n'), '整列已复制'));
      add('隐藏此列', () => { state.hiddenColumns.add(column); selectGridColumn(-1); state.gridReady = false; renderResult(); });
    }

    document.body.appendChild(menu); keepMenu(menu);
    setTimeout(() => document.addEventListener('pointerdown', closeMenus, { once: true }), 0);
  }
  function openColumnManager(e) {
    closeMenus();
    const r = e.currentTarget.getBoundingClientRect();
    const menu = el('div', { class: 'db-popup-menu db-column-picker', style: 'left:' + r.left + 'px;top:' + r.bottom + 'px' });
    menu.appendChild(el('div', { class: 'db-popup-title', text: '显示列' }));
    state.columns.forEach((c, i) => {
      const check = el('input', { type: 'checkbox' });
      check.checked = !state.hiddenColumns.has(i);
      check.onchange = () => { check.checked ? state.hiddenColumns.delete(i) : state.hiddenColumns.add(i); state.gridReady = false; renderResult(); };
      menu.appendChild(el('label', {}, [check, el('span', { text: c.name })]));
    });
    document.body.appendChild(menu); keepMenu(menu);
    setTimeout(() => document.addEventListener('pointerdown', closeMenus, { once: true }), 0);
  }

  function shortcutField(label, key) {
    return '<label class="editor-label db-shortcut-field"><span>' + h(label) + '</span><input id="db-shortcut-' + key + '" class="editor-input db-shortcut-input" type="text" placeholder="点击后按下组合键" readonly autocomplete="off" data-capture-keys="1"></label>';
  }
  function captureShortcut(input) {
    input.addEventListener('keydown', function (e) {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') { input.value = 'Escape'; return; }
      if (e.key === 'Backspace') { input.value = ''; return; }
      const parts = [];
      if (e.ctrlKey) parts.push('Ctrl');
      if (e.altKey) parts.push('Alt');
      if (e.shiftKey) parts.push('Shift');
      if (e.metaKey) parts.push('Meta');
      if (['Control', 'Alt', 'Shift', 'Meta'].indexOf(e.key) >= 0) return;
      const key = e.key === ' ' ? 'Space' : (e.key.length === 1 ? e.key.toUpperCase() : e.key);
      parts.push(key);
      input.value = parts.join('+');
    });
  }
  function openSettings() {
    let currentMode = 'list';
    try {
      const saved = localStorage.getItem('kairo:database:snippet-mode');
      if (saved === 'text' || saved === 'list') currentMode = saved;
    } catch (_) {}

    const body = el('div', { class: 'db-settings-body' });
    body.innerHTML = '<section><h4>快捷键</h4><p class="muted">点输入框后按下组合键即可，保存后立即生效；输入法正在组合文字时不会触发工作台快捷键。</p><div class="db-shortcut-grid">'
      + shortcutField('执行查询', 'run') + shortcutField('取消查询', 'cancel') + shortcutField('网格视图', 'grid')
      + shortcutField('单行记录', 'record') + shortcutField('执行计划', 'explain') + shortcutField('对象栏', 'objects')
      + '</div></section><section>'
      + '<div class="db-setting-line">'
      + '  <div class="db-snippet-heading">'
      + '    <h4>SQL 模板</h4>'
      + '    <div class="db-snippet-tabs">'
      + '      <button type="button" class="btn btn-xs db-snippet-tab' + (currentMode === 'list' ? ' active' : '') + '" id="db-snippet-tab-list">列表编辑</button>'
      + '      <button type="button" class="btn btn-xs db-snippet-tab' + (currentMode === 'text' ? ' active' : '') + '" id="db-snippet-tab-text">批量文本框 (PL/SQL)</button>'
      + '    </div>'
      + '  </div>'
      + '  <label class="editor-label-inline">展开键<select id="db-expand-key" class="editor-input db-expand-key"><option value="Space">Space</option><option value="Tab">Tab</option><option value="Enter">Enter</option></select></label>'
      + '</div>'
      + '<div id="db-snippet-panel-list" class="db-snippet-panel"' + (currentMode === 'list' ? '' : ' style="display:none;"') + '>'
      + '  <div id="db-snippet-list" class="db-snippet-list"></div>'
      + '  <div class="db-snippet-actions">'
      + '    <button type="button" class="btn btn-sm" id="db-snippet-add">＋ 添加模板</button>'
      + '    <button type="button" class="btn btn-sm" id="db-snippet-go-text" title="切换到多行文本框进行批量编辑或粘贴">批量粘贴 / 文本模式</button>'
      + '  </div>'
      + '</div>'
      + '<div id="db-snippet-panel-text" class="db-snippet-panel"' + (currentMode === 'text' ? '' : ' style="display:none;"') + '>'
      + '  <p class="muted db-snippet-help">支持像 PL/SQL (shortcuts.txt) 直接粘贴多条规则（每行一条：<code>缩写=SQL模板</code>）。<br>支持 <code>${schema}</code>、<code>${table}</code>、<code>${cursor}</code> 变量；单行内多行内容可用 <code>\\n</code> 或后续行缩进；以 <code>#</code> 开头为注释。</p>'
      + '  <textarea id="db-snippet-textarea" class="editor-content db-snippet-textarea" rows="12" spellcheck="false" placeholder="sf=SELECT * FROM \nsc=SELECT COUNT(*) FROM \nw=WHERE \ndf=DELETE FROM \nsel=SELECT *\\nFROM ${table}"></textarea>'
      + '  <div class="db-snippet-text-footer">'
      + '    <span id="db-snippet-text-count" class="muted db-snippet-count">已解析 0 条模板</span>'
      + '    <div class="db-snippet-btn-row">'
      + '      <button type="button" class="btn btn-xs" id="db-snippet-copy-text" title="复制文本框中的所有模板规则">复制全部</button>'
      + '      <button type="button" class="btn btn-xs" id="db-snippet-load-sample" title="填入常用的 PL/SQL 快捷替换模板示例">填入示例</button>'
      + '      <button type="button" class="btn btn-xs" id="db-snippet-clear-text" title="清空文本框内容">清空</button>'
      + '      <button type="button" class="btn btn-xs btn-primary" id="db-snippet-sync-list" title="将文本内容解析并同步到列表模式">同步到列表</button>'
      + '    </div>'
      + '  </div>'
      + '</div></section>';

    const footer = el('div', { class: 'editor-footer db-settings-actions' }, [
      el('button', { class: 'btn', type: 'button', id: 'db-settings-reset', text: '恢复默认' }),
      el('button', { class: 'btn', type: 'button', id: 'db-settings-close', text: '关闭' }),
      el('button', { class: 'btn btn-primary', id: 'db-settings-save', text: '保存设置' })
    ]);
    const dlg = Kairo.overlays.modal({ title: 'SQL 模板与快捷键', width: 760, body: body, footer: footer });
    const expandSelect = body.querySelector('#db-expand-key') || q('db-expand-key');
    if (expandSelect) expandSelect.value = state.prefs.expandKey || 'Space';
    Object.keys(state.prefs.shortcuts).forEach(k => {
      const input = q('db-shortcut-' + k);
      if (input) { input.value = state.prefs.shortcuts[k]; captureShortcut(input); }
    });

    let draft = state.prefs.snippets.map(x => Object.assign({}, x));
    const ta = body.querySelector('#db-snippet-textarea');
    const countEl = body.querySelector('#db-snippet-text-count');

    function syncFromListToDraft() {
      body.querySelectorAll('[data-key]').forEach(x => {
        const idx = Number(x.dataset.key);
        if (draft[idx]) draft[idx].key = x.value.trim();
      });
      body.querySelectorAll('[data-text]').forEach(x => {
        const idx = Number(x.dataset.text);
        if (draft[idx]) draft[idx].text = x.value;
      });
      body.querySelectorAll('[data-enabled]').forEach(x => {
        const idx = Number(x.dataset.enabled);
        if (draft[idx]) draft[idx].enabled = x.checked;
      });
    }

    function updateTextCounter() {
      if (!ta || !countEl) return null;
      const parsed = parseSnippetsText(ta.value);
      const ignoredSuffix = (parsed.ignored ? '，已忽略 ' + parsed.ignored + ' 行无效内容' : '');
      if (parsed.duplicates.length > 0) {
        countEl.className = 'db-snippet-count has-warning';
        countEl.textContent = '已解析 ' + parsed.items.length + ' 条模板（⚠️ 存在重复缩写: ' + parsed.duplicates.join(', ') + '）' + ignoredSuffix;
      } else {
        countEl.className = 'muted db-snippet-count';
        countEl.textContent = '已解析 ' + parsed.items.length + ' 条模板' + ignoredSuffix;
      }
      return parsed;
    }

    const draw = () => {
      const list = body.querySelector('#db-snippet-list') || q('db-snippet-list');
      if (!list) return;
      list.innerHTML = '';
      draft.forEach((s, i) => {
        const row = el('div', { class: 'db-snippet-row' });
        row.innerHTML = '<label class="db-snippet-enable" title="启用"><input type="checkbox" data-enabled="' + i + '"' + (s.enabled !== false ? ' checked' : '') + '></label><input type="text" class="editor-input" data-key="' + i + '" value="' + h(s.key) + '" placeholder="缩写" autocomplete="off"><textarea class="editor-content" data-text="' + i + '" rows="2" placeholder="SQL 模板；支持 ${schema} 和 ${table}">' + h(s.text) + '</textarea><button type="button" class="btn btn-sm btn-danger" data-remove="' + i + '">删除</button>';
        list.appendChild(row);
      });
      list.querySelectorAll('[data-remove]').forEach(b => b.onclick = () => {
        syncFromListToDraft();
        draft.splice(Number(b.dataset.remove), 1);
        draw();
      });
    };

    function switchMode(newMode) {
      if (currentMode === newMode) return;
      if (newMode === 'text') {
        syncFromListToDraft();
        if (ta) {
          ta.value = formatSnippetsText(draft);
          updateTextCounter();
        }
      } else {
        if (ta) {
          const parsed = parseSnippetsText(ta.value);
          if (parsed.duplicates.length > 0) {
            toast('存在重复缩写: ' + parsed.duplicates.join(', ') + '，已为您标注', 'warn');
          }
          draft = parsed.items;
          draw();
        }
      }
      currentMode = newMode;
      try { localStorage.setItem('kairo:database:snippet-mode', newMode); } catch (_) {}
      const listTab = body.querySelector('#db-snippet-tab-list');
      const textTab = body.querySelector('#db-snippet-tab-text');
      const listPanel = body.querySelector('#db-snippet-panel-list');
      const textPanel = body.querySelector('#db-snippet-panel-text');
      if (listTab) listTab.classList.toggle('active', currentMode === 'list');
      if (textTab) textTab.classList.toggle('active', currentMode === 'text');
      if (listPanel) listPanel.style.display = currentMode === 'list' ? '' : 'none';
      if (textPanel) {
        textPanel.style.display = currentMode === 'text' ? '' : 'none';
        if (currentMode === 'text' && ta) setTimeout(() => ta.focus(), 50);
      }
    }

    draw();
    if (ta) {
      ta.value = formatSnippetsText(draft);
      updateTextCounter();
      ta.addEventListener('input', updateTextCounter);
    }

    const tabListBtn = body.querySelector('#db-snippet-tab-list');
    const tabTextBtn = body.querySelector('#db-snippet-tab-text');
    const goTextBtn = body.querySelector('#db-snippet-go-text');
    const addBtn = body.querySelector('#db-snippet-add');
    const copyBtn = body.querySelector('#db-snippet-copy-text');
    const sampleBtn = body.querySelector('#db-snippet-load-sample');
    const clearBtn = body.querySelector('#db-snippet-clear-text');
    const syncListBtn = body.querySelector('#db-snippet-sync-list');

    if (tabListBtn) tabListBtn.onclick = () => switchMode('list');
    if (tabTextBtn) tabTextBtn.onclick = () => switchMode('text');
    if (goTextBtn) goTextBtn.onclick = () => switchMode('text');
    if (addBtn) addBtn.onclick = () => {
      syncFromListToDraft();
      draft.push({ key: '', text: '', enabled: true });
      draw();
    };

    if (copyBtn) copyBtn.onclick = () => {
      if (!ta || !ta.value.trim()) return toast('文本框内容为空', 'warn');
      copyToClipboard(ta.value)
        .then(() => toast('已复制全部模板规则到剪贴板', 'ok'))
        .catch(() => toast('复制失败', 'err'));
    };

    if (sampleBtn) sampleBtn.onclick = () => {
      if (!ta) return;
      if (!ta.value.trim()) {
        ta.value = SAMPLE_SNIPPETS_TEXT;
      } else {
        // 去重追加：已存在的缩写不再重复填入，避免一点就制造重复导致保存被拦。
        const existingKeys = new Set(parseSnippetsText(ta.value).items.map(x => x.key));
        const missing = parseSnippetsText(SAMPLE_SNIPPETS_TEXT).items.filter(x => !existingKeys.has(x.key));
        if (!missing.length) {
          toast('示例模板已存在，无需重复填入', 'warn');
          return;
        }
        ta.value = ta.value.trimEnd() + '\n' + formatSnippetsText(missing);
      }
      updateTextCounter();
      toast('已填入常用示例模板', 'ok');
    };

    if (clearBtn) clearBtn.onclick = () => {
      if (!ta) return;
      ta.value = '';
      updateTextCounter();
    };

    if (syncListBtn) syncListBtn.onclick = () => {
      const parsed = parseSnippetsText(ta ? ta.value : '');
      if (parsed.duplicates.length > 0) {
        return toast('模板缩写不能重复: ' + parsed.duplicates.join(', '), 'warn');
      }
      draft = parsed.items;
      draw();
      switchMode('list');
      toast('已同步到列表视图（共 ' + draft.length + ' 条' + (parsed.ignored ? '，已忽略 ' + parsed.ignored + ' 行无效内容' : '') + '）', 'ok');
    };

    q('db-settings-close').onclick = () => dlg.close();
    q('db-settings-reset').onclick = () => {
      state.prefs = JSON.parse(JSON.stringify(DEFAULT_PREFS));
      savePrefs();
      dlg.close();
      toast('工作台设置已恢复默认', 'ok');
      if (state.source && state.source.kind !== 'redis') renderWorkspace(true);
    };

    q('db-settings-save').onclick = () => {
      let finalItems = [];
      let ignoredNote = '';
      if (currentMode === 'text' && ta) {
        const parsed = parseSnippetsText(ta.value);
        if (parsed.duplicates.length > 0) {
          return toast('模板缩写不能重复: ' + parsed.duplicates.join(', '), 'warn');
        }
        finalItems = parsed.items.filter(x => x.key && x.text);
        const dropped = (parsed.items.length - finalItems.length) + (parsed.ignored || 0);
        if (dropped > 0) ignoredNote = '（已忽略 ' + dropped + ' 条空行/无效行）';
      } else {
        syncFromListToDraft();
        const before = draft.length;
        finalItems = draft.filter(x => x.key && x.text);
        if (before > finalItems.length) ignoredNote = '（已忽略 ' + (before - finalItems.length) + ' 条空行）';
        const keys = finalItems.map(x => x.key);
        if (new Set(keys).size !== keys.length) {
          const dupes = keys.filter((k, idx) => keys.indexOf(k) !== idx);
          return toast('模板缩写不能重复: ' + Array.from(new Set(dupes)).join(', '), 'warn');
        }
      }
      const curExpandSelect = body.querySelector('#db-expand-key') || q('db-expand-key');
      state.prefs.expandKey = (curExpandSelect && curExpandSelect.value) || 'Space';
      state.prefs.snippets = finalItems;
      Object.keys(state.prefs.shortcuts).forEach(k => {
        if (q('db-shortcut-' + k)) state.prefs.shortcuts[k] = q('db-shortcut-' + k).value.trim() || DEFAULT_PREFS.shortcuts[k];
      });
      savePrefs();
      dlg.close();
      toast('工作台设置已保存（共 ' + finalItems.length + ' 条模板）' + ignoredNote, 'ok');
      if (state.source && state.source.kind !== 'redis') renderWorkspace(true);
    };
  }

  async function boundedExportBlob(response, maxBytes) {
    const size = Number(response.headers.get('Content-Length'));
    if (size > maxBytes) throw new Error('导出超过浏览器内存下载上限，请使用支持直接保存文件的浏览器或缩小结果范围');
    if (!response.body || typeof response.body.getReader !== 'function') {
      if (!size) throw new Error('当前浏览器无法安全读取未知大小的导出，请使用支持流式下载的浏览器');
      const blob = await response.blob();
      if (blob.size > maxBytes) throw new Error('导出文件超过内存下载上限');
      return blob;
    }
    const reader = response.body.getReader(), chunks = [];
    let bytes = 0;
    try {
      while (true) {
        const item = await reader.read();
        if (item.done) break;
        bytes += item.value.byteLength;
        if (bytes > maxBytes) { await reader.cancel(); throw new Error('导出超过 64MB 内存下载上限，请直接保存文件或缩小结果范围'); }
        chunks.push(item.value);
      }
      return new Blob(chunks, { type: response.headers.get('Content-Type') || 'application/octet-stream' });
    } finally { reader.releaseLock(); }
  }
  async function exportResult() {
    if (!state.lastSQL || state.controller) return;
    const effSrc3 = effectiveSource();
    if (!effSrc3) return toast('未选择数据源', 'warn');
    const format = (q('db-export-format') && q('db-export-format').value) || 'csv';
    const ext = format === 'json' ? '.json' : format === 'xlsx' ? '.xlsx' : (format === 'insert' || format === 'update') ? '.sql' : '.csv';
    const types = format === 'json' ? [{ description: 'JSON', accept: { 'application/json': ['.json'] } }]
      : format === 'xlsx' ? [{ description: 'Excel', accept: { 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': ['.xlsx'] } }]
      : (format === 'insert' || format === 'update') ? [{ description: 'SQL', accept: { 'text/plain': ['.sql'] } }]
      : [{ description: 'CSV 文件', accept: { 'text/csv': ['.csv'] } }];
    const safe = (effSrc3.name || 'query').replace(/[\\/:*?"<>|]/g, '_');
    const filename = safe + '-' + new Date().toISOString().replace(/[:.]/g, '-') + ext;
    const button = q('db-export'); button.disabled = true;
    let handle = null, table = '';
    const exportController = new AbortController();
    const cancelExportOnLeave = function () { if (String(location.hash || '').indexOf('#/database') !== 0) exportController.abort(); };
    window.addEventListener('hashchange', cancelExportOnLeave);
    try {
      if (format === 'insert' || format === 'update') {
        const title = format === 'update' ? '导出 UPDATE 语句' : '导出 INSERT 语句';
        const label = format === 'update' ? '请输入 UPDATE 目标表名（可含 schema，留空自动从 SQL 推断）：' : '请输入 INSERT 目标表名（可含 schema，留空自动从 SQL 推断）：';
        const inputTable = Kairo.overlays && Kairo.overlays.prompt
          ? await Kairo.overlays.prompt({ title: title, label: label, placeholder: '如 sys_user' })
          : (window.prompt(label, '') || '').trim();
        if (inputTable == null) { button.disabled = false; return; }
        table = (inputTable || '').trim();
      }
      if (window.showSaveFilePicker) handle = await window.showSaveFilePicker({ suggestedName: filename, types: types });
      const currentSession = sess();
      if (currentSession && currentSession.summary && currentSession.summary.ordered === false && (currentSession.page || 1) > 1) {
        toast('无序分页在第 2 页及之后可能存在重复或漏行，请在 SQL 中补充 ORDER BY 确保导出数据完整准确', 'warn');
        button.disabled = false;
        return;
      }
      const response = await fetch('/api/database/export', {
        method: 'POST',
        signal: exportController.signal,
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          source_id: effSrc3.id,
          session_id: currentSession && currentSession.transactionId || '',
          parameters: currentSession && currentSession.lastParameters || [],
          sql: state.lastSQL,
          max_rows: state.lastMaxRows,
          page: currentSession && currentSession.page || 1,
          page_size: currentSession && currentSession.pageSize || state.lastMaxRows,
          count_mode: 'none',
          format: format,
          table: table
        })
      });
      if (!response.ok) { const body = await response.json().catch(() => ({})); throw new Error(body.error || 'HTTP ' + response.status); }
      if (handle && response.body) { const writable = await handle.createWritable(); await response.body.pipeTo(writable, { signal: exportController.signal }); }
      else { const blob = await boundedExportBlob(response, 64 * 1024 * 1024); downloadBlob(blob, filename); }
      toast((format === 'xlsx' ? 'Excel' : format.toUpperCase()) + ' 导出完成', 'ok');
    } catch (e) {
      if (e.name !== 'AbortError') toast('导出失败：' + e.message, 'err');
    } finally { window.removeEventListener('hashchange', cancelExportOnLeave); button.disabled = state.rows.length === 0; }
  }

  function renderRedis(host) {
    state.cursor = '0'; state.redisKeyBase64 = ''; state.redisType = ''; state.redisCursor = '0'; state.redisNextCursor = '0'; state.redisCursorHistory = []; state.redisOffset = 0; state.redisMembersHasNext = false;
    const mode = state.source.redis_mode || 'standalone';
    host.innerHTML = '<div class="db-redis-workspace"><div class="db-editor-tabs"><div id="db-sql-tabs" class="db-sql-tabs"></div><button type="button" class="btn btn-xs" id="db-tab-add" title="新建查询页签">＋ 页签</button><span class="db-dialect">Redis · 只读命令</span></div><div class="db-redis-layout"><div class="card db-redis-keys"><div class="db-editor-bar"><span class="db-redis-topo">拓扑 ' + h(mode) + (mode === 'cluster' ? ' · 跨主节点 SCAN' : mode === 'sentinel' ? ' · ' + h(state.source.redis_master_name || '') : ' · 单机') + '</span><input id="db-redis-pattern" value="*" placeholder="Key pattern，例如 order:*"><button class="btn btn-primary" id="db-redis-scan">SCAN</button><button class="btn" id="db-redis-next">下一批</button></div><div id="db-redis-list" class="db-redis-list"></div></div><div class="db-redis-center"><div class="card db-redis-detail"><div class="db-pane-title"><span>Key 详情</span><span id="db-redis-type" class="tag"></span></div><div id="db-redis-controls" class="db-redis-controls"></div><pre id="db-redis-value">选择左侧 Key 查看类型、TTL 和内容</pre></div><div class="card db-redis-members"><div class="db-pane-title"><span>成员</span><div><button class="btn btn-xs" id="db-redis-members-prev" disabled>上一页</button><button class="btn btn-xs" id="db-redis-members-next" disabled>下一页</button></div></div><div id="db-redis-members-list" class="db-redis-members-list"><span class="hint">Hash / List / Set / ZSet 选择 Key 后在这里分页查看</span></div></div></div><div class="card db-redis-side"><div class="db-pane-title"><span>运行指标</span><button class="btn btn-xs" id="db-redis-info-refresh">刷新</button></div><div id="db-redis-info" class="db-redis-info"><span class="hint">点击刷新读取 INFO memory / clients / stats / keyspace</span></div><div class="db-pane-title db-redis-command-title">只读命令行</div><div class="db-redis-command-form"><input id="db-redis-command-line" class="db-redis-command-line" placeholder="例如 HSCAN user:1001 0 COUNT 100"><select id="db-redis-command"><option>TYPE</option><option>TTL</option><option>PTTL</option><option>GET</option><option>HGET</option><option>HSCAN</option><option>LLEN</option><option>LRANGE</option><option>SCARD</option><option>SSCAN</option><option>ZCARD</option><option>ZRANGE</option><option>INFO</option><option>DBSIZE</option><option>PING</option></select><input id="db-redis-command-args" placeholder="参数：field 或其他只读参数"><button class="btn btn-primary" id="db-redis-command-run">执行</button></div><div class="hint">命令行仅执行只读白名单，参数按 Redis CLI 习惯拆分；危险命令会被后端拒绝。</div><pre id="db-redis-command-result" class="db-redis-command-result"></pre></div></div></div>';
    renderTabs();
    q('db-tab-add').onclick = addSession;
    q('db-redis-scan').onclick = () => { state.cursor = '0'; scanRedis(true); };
    q('db-redis-next').onclick = () => scanRedis(false);
    q('db-redis-info-refresh').onclick = loadRedisInfo;
    q('db-redis-command-run').onclick = runRedisCommand;
    q('db-redis-members-prev').onclick = function () {
      if (state.redisType === 'hash' || state.redisType === 'set') {
        if (!state.redisCursorHistory.length) return;
        state.redisCursor = state.redisCursorHistory.pop() || '0';
      } else {
        state.redisOffset = Math.max(0, state.redisOffset - state.redisPageSize);
      }
      loadRedisMembers(false);
    };
    q('db-redis-members-next').onclick = function () {
      if (state.redisType === 'hash' || state.redisType === 'set') {
        if (!state.redisNextCursor || state.redisNextCursor === '0') return;
        state.redisCursorHistory.push(state.redisCursor || '0');
        state.redisCursor = state.redisNextCursor;
      } else {
        state.redisOffset += state.redisPageSize;
      }
      loadRedisMembers(false);
    };
    scanRedis(true);
    loadRedisInfo();
  }
  async function scanRedis(reset) {
    const token = state.workspaceToken, source = state.source;
    try {
      const pattern = q('db-redis-pattern');
      const data = await api('GET', '/api/database/redis/scan?source_id=' + encodeURIComponent(source.id) + '&cursor=' + encodeURIComponent(state.cursor || '0') + '&count=200&pattern=' + encodeURIComponent(pattern ? pattern.value || '*' : '*'));
      const next = q('db-redis-next'), list = q('db-redis-list');
      if (token !== state.workspaceToken || !next || !list) return;
      state.cursor = String((data.result && data.result.next_cursor) || '0');
      next.disabled = state.cursor === '0' || state.cursor === '';
      const rows = ((data.result && data.result.keys) || []).map(k => '<button class="db-redis-key" data-key="' + h(k.key_base64) + '">' + h(fmtRedisLabel(k.display)) + '</button>').join('');
      reset ? list.innerHTML = rows || '<div class="hint">没有匹配 Key</div>' : list.insertAdjacentHTML('beforeend', rows);
      list.querySelectorAll('.db-redis-key').forEach(btn => btn.onclick = () => loadRedisKey(btn.dataset.key));
    } catch (e) { if (token === state.workspaceToken) toast('Redis SCAN 失败：' + e.message, 'err'); }
  }
  async function loadRedisKey(key) {
    const token = state.workspaceToken, source = state.source;
    try {
      const data = await api('GET', '/api/database/redis/key?source_id=' + encodeURIComponent(source.id) + '&key_base64=' + encodeURIComponent(key));
      const value = q('db-redis-value');
      if (token === state.workspaceToken && value) {
        const result = data.result || {};
        state.redisKeyBase64 = key; state.redisType = result.type || '';
        const type = q('db-redis-type'); if (type) type.textContent = result.type || '';
        value.textContent = JSON.stringify(result, null, 2);
        renderRedisTTL(result);
        state.redisCursor = '0'; state.redisNextCursor = '0'; state.redisCursorHistory = []; state.redisOffset = 0; state.redisMembersHasNext = false;
        loadRedisMembers(true);
      }
    } catch (e) { if (token === state.workspaceToken) toast('读取 Key 失败：' + e.message, 'err'); }
  }
  function renderRedisTTL(result) {
    const host = q('db-redis-controls'); if (!host) return;
    host.innerHTML = '';
    host.appendChild(el('span', { class: 'muted', text: 'TTL：' + (result.ttl_ms < 0 ? (result.ttl_ms === -1 ? '永不过期' : '不存在') : Math.ceil(result.ttl_ms / 1000) + ' 秒') }));
    if (!state.source || !state.source.allow_redis_write) return;
    const op = el('select', {}, [el('option', { value: 'EXPIRE', text: '设置 TTL（秒）' }), el('option', { value: 'PERSIST', text: '取消过期' })]);
    const seconds = el('input', { type: 'number', min: '0', max: '31536000', value: '3600', placeholder: '秒' });
    const save = el('button', { class: 'btn btn-xs btn-primary', text: '应用 TTL' });
    save.onclick = async function () {
      if (!confirm('确认修改该 Redis Key 的 TTL？')) return;
      try { await api('POST', '/api/database/redis/mutate', { source_id: state.source.id, key_base64: state.redisKeyBase64, operation: op.value, seconds: Number(seconds.value) || 0, confirm: true }); toast('TTL 已更新', 'ok'); loadRedisKey(state.redisKeyBase64); }
      catch (e) { toast('TTL 更新失败：' + e.message, 'err'); }
    };
    host.append(op, seconds, save);
  }
  async function loadRedisMembers(reset) {
    const source = state.source, key = state.redisKeyBase64, list = q('db-redis-members-list'); if (!source || !key || !list) return;
    if (reset) { state.redisCursor = '0'; state.redisNextCursor = '0'; state.redisCursorHistory = []; state.redisOffset = 0; state.redisMembersHasNext = false; }
    const requestCursor = state.redisCursor || '0';
    const requestOffset = state.redisOffset || 0;
    const params = new URLSearchParams({ source_id: source.id, key_base64: key, type: state.redisType || '', cursor: requestCursor, offset: String(requestOffset), page_size: String(state.redisPageSize || 100) });
    try {
      const data = await api('GET', '/api/database/redis/members?' + params.toString()), result = data.result || {};
      state.redisCursor = requestCursor; state.redisNextCursor = result.next_cursor || '0'; state.redisMembersHasNext = !!result.has_next; state.redisType = result.type || state.redisType;
      list.innerHTML = (result.items || []).map(function (item) { return '<div class="db-redis-member-row"><code>' + h(fmtRedisLabel(item.field || item.member || item)) + '</code>' + (item.value !== undefined ? '<span>' + h(fmtRedisLabel(item.value)) + '</span>' : item.score !== undefined ? '<span class="muted">score ' + h(item.score) + '</span>' : '') + '</div>'; }).join('') || '<span class="hint">没有成员</span>';
      const prev = q('db-redis-members-prev'), next = q('db-redis-members-next');
      if (prev) prev.disabled = (state.redisType === 'hash' || state.redisType === 'set') ? state.redisCursorHistory.length === 0 : state.redisOffset <= 0;
      if (next) next.disabled = (state.redisType === 'hash' || state.redisType === 'set') ? state.redisNextCursor === '0' : !state.redisMembersHasNext;
    } catch (e) { list.innerHTML = '<span class="text-err">成员读取失败：' + h(e.message) + '</span>'; }
  }
  async function loadRedisInfo() {
    const box = q('db-redis-info'), source = state.source; if (!box || !source) return;
    box.textContent = '读取中…';
    try {
      const data = await api('GET', '/api/database/redis/info?source_id=' + encodeURIComponent(source.id) + '&section=memory,clients,stats,keyspace');
      const sections = data.result && data.result.sections || {};
      const keys = ['used_memory_human', 'used_memory_peak_human', 'connected_clients', 'blocked_clients', 'instantaneous_ops_per_sec', 'expired_keys', 'db0'];
      box.innerHTML = keys.map(function (key) { let value = ''; Object.keys(sections).some(function (section) { if (sections[section] && sections[section][key] !== undefined) { value = sections[section][key]; return true; } return false; }); return value === '' ? '' : '<div><span>' + h(key) + '</span><strong>' + h(value) + '</strong></div>'; }).join('') || '<span class="hint">没有可显示的指标</span>';
    } catch (e) { box.innerHTML = '<span class="text-err">INFO 读取失败：' + h(e.message) + '</span>'; }
  }
  function parseRedisCommandLine(raw) {
    const out = [], text = String(raw || '').trim();
    let word = '', quote = '', escaped = false;
    for (let i = 0; i < text.length; i++) {
      const ch = text[i];
      if (escaped) { word += ch; escaped = false; continue; }
      if (ch === '\\') { escaped = true; continue; }
      if (quote) {
        if (ch === quote) quote = ''; else word += ch;
        continue;
      }
      if (ch === '"' || ch === "'") { quote = ch; continue; }
      if (/\s/.test(ch)) { if (word) { out.push(word); word = ''; } continue; }
      word += ch;
    }
    if (escaped) word += '\\';
    if (quote) throw new Error('命令行存在未闭合引号');
    if (word) out.push(word);
    return out;
  }
  function base64UTF8(value) {
    const bytes = new TextEncoder().encode(String(value || ''));
    let binary = '';
    for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    return btoa(binary);
  }
  async function runRedisCommand() {
    const source = state.source, command = q('db-redis-command'), lineInput = q('db-redis-command-line'), argsInput = q('db-redis-command-args'), out = q('db-redis-command-result'); if (!source || !command || !out) return;
    const lineMode = !!(lineInput && lineInput.value.trim());
    let tokens;
    try { tokens = lineMode ? parseRedisCommandLine(lineInput.value) : [command.value].concat((argsInput.value || '').trim() ? parseRedisCommandLine(argsInput.value) : []); }
    catch (e) { out.textContent = '命令行无效：' + e.message; return; }
    if (!tokens.length) return;
    const name = String(tokens[0]).toUpperCase();
    const keyCommands = new Set(['GET', 'TYPE', 'TTL', 'PTTL', 'STRLEN', 'HGET', 'HGETALL', 'HSCAN', 'LLEN', 'LRANGE', 'SCARD', 'SSCAN', 'ZCARD', 'ZRANGE']);
    let key = '';
    let args = tokens.slice(1);
    if (keyCommands.has(name) && lineMode && args.length) key = args.shift();
    const selectedKey = state.redisKeyBase64 && keyCommands.has(name) && (!lineMode || !key) ? state.redisKeyBase64 : '';
    const body = { source_id: source.id, command: name, args: args };
    if (selectedKey) body.key_base64 = selectedKey;
    else if (key) body.key_base64 = base64UTF8(key);
    out.textContent = '执行中…';
    try { const data = await api('POST', '/api/database/redis/command', body); out.textContent = JSON.stringify(data.result, null, 2); }
    catch (e) { out.textContent = '执行失败：' + e.message; }
  }
  function debounce(fn, wait) { let timer; return function () { const self = this, args = arguments; clearTimeout(timer); timer = setTimeout(() => fn.apply(self, args), wait); }; }
  async function renderDatabase(view) {
    const renderToken = view.dataset.renderToken;
    try {
      await loadDatabasePreference();
      await loadSources();
      if (view.dataset.renderToken === renderToken) render(view);
    }
    catch (e) {
      if (view.dataset.renderToken === renderToken) view.innerHTML = '<div class="card text-err">数据库工作台加载失败：' + h(e.message) + '</div>';
    }
  }
  function hasPendingWork() {
    let hasTx = false;
    let dirtyCount = 0;
    let mutationCount = 0;
    for (let i = 0; i < state.sessions.length; i++) {
      const s = state.sessions[i];
      if (s && s.transactionPending) hasTx = true;
      const d = Object.keys((s && s.dirtyCells) || {}).length;
      if (d > 0) dirtyCount += d;
    }
    if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.pendingGridMutations === 'function') {
      mutationCount = Kairo.databaseFeatures.pendingGridMutations().length;
    }
    return { hasTransaction: hasTx, dirtyCount: dirtyCount, mutationCount: mutationCount, pending: hasTx || dirtyCount > 0 || mutationCount > 0 };
  }
  function discardPendingWork() {
    for (let i = 0; i < state.sessions.length; i++) {
      const s = state.sessions[i];
      if (s && s.transactionPending && s.sourceId) {
        api('POST', '/api/database/transaction', { source_id: s.sourceId, session_id: s.transactionId, action: 'ROLLBACK' }).catch(() => {});
        s.transactionPending = false;
        s.gridEditsStaged = false;
      }
      if (s) s.dirtyCells = {};
    }
    if (Kairo.databaseFeatures && typeof Kairo.databaseFeatures.clearGridMutations === 'function') {
      Kairo.databaseFeatures.clearGridMutations();
    }
    state.dirtyCells = {};
    updateTransactionControls();
  }
  Kairo.state.routes.database = renderDatabase;
  Kairo.state.routeNames.database = '数据库工作台';
  Kairo.state.routeSubs.database = 'Oracle 11g / MySQL / Redis · 安全只读查询';
  Kairo.database = Object.assign(Kairo.database || {}, {
    getHistory: function () {
      return Array.isArray(persisted.history) ? persisted.history.slice() : [];
    },
    cancel: cancelQuery,
    hasPendingWork: hasPendingWork,
    discardPendingWork: discardPendingWork,
    markTransactionPending: markTransactionPending,
    refreshTransactionState: function () { updateTransactionControls(); return !!(sess() && sess().transactionPending); },
    currentSchema: currentSchema,
    getGridContext: getGridContext,
    getActiveSession: function () {
      const active = sess();
      if (!active || active.type === 'object') return null;
      return {
        id: active.id,
        sessionId: active.transactionId || '',
        transactionId: active.transactionId || '',
        sourceId: active.sourceId || (state.source && state.source.id) || '',
        transactionPending: !!active.transactionPending,
        type: active.type || 'query'
      };
    },
    formatSQL: formatSQL,
    highlightSQL: highlightSQL,
    tokenizeSQL: tokenizeSQL,
    suggestSQL: suggestSQL,
    sqlTableContext: sqlTableContext,
    matchesShortcut: matchesShortcut,
    completionExtras: completionExtras,
    warmupSchemaTables: warmupSchemaTables,
    loadCategoryObjects: loadCategoryObjects,
    matchBrackets: matchBrackets,
    completionPrefix: completionPrefix,
    isSnippetExpandKey: isSnippetExpandKey,
    parseSnippetsText: parseSnippetsText,
    formatSnippetsText: formatSnippetsText
  });
})();
