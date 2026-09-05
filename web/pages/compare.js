(function () {
  'use strict';

  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard } = Kairo.core;
  const { api, getPreference, putPreference, preferenceSaver, browseButton } = Kairo.api;

  const MAX_INLINE_ROWS = 5000;
  // 文本工作台的性能护栏。超过自动比较阈值后仍允许用户手动比较，
  // 但输入过程中不再反复重建整份 Diff；超过高亮/行号阈值时改用轻量提示。
  const AUTO_COMPARE_MAX_CHARS = 200000;
  const AUTO_COMPARE_MAX_LINES = 12000;
  const HIGHLIGHT_MAX_CHARS = 120000;
  const GUTTER_MAX_LINES = 20000;
  const LS_OPTIONS = 'kairo:compare:workbench-options';
  const LS_SOURCES = 'kairo:compare:workbench-sources';
  const WASPACK_HANDOFF_PREFIX = 'kairo:waspack:compare-handoff:';
  const WASPACK_HANDOFF_MAX_AGE = 10 * 60 * 1000;
  const PATH_HISTORY_LIMIT = 12;
  let connections = [];
  let activeScanJob = '';
  let scanPollTimer = 0;
  let persisted = { options: {}, sources: {}, folder_history: { left: [], right: [] } };
  const persistPreference = preferenceSaver('compare', 400);

  function handoffPathKey(value) {
    let path = String(value || '').trim().replace(/\//g, '\\').replace(/\\{2,}/g, '\\');
    if (/^[A-Za-z]:\\$/.test(path)) return path.toLowerCase();
    return path.replace(/\\+$/, '').toLowerCase();
  }
  function validateWaspackHandoff(payload, now) {
    if (!payload || typeof payload !== 'object') return { ok: false, reason: '交接数据不存在或格式无效。' };
    if (payload.source !== 'waspack-history') return { ok: false, reason: '交接来源不受信任。' };
    const left = payload.left, right = payload.right;
    if (!left || !right || left.kind !== 'local' || right.kind !== 'local' || !String(left.path || '').trim() || !String(right.path || '').trim()) return { ok: false, reason: '交接数据缺少两个本地目录。' };
    if (handoffPathKey(left.path) === handoffPathKey(right.path)) return { ok: false, reason: '交接的两个目录相同，无法形成比较快照。' };
    const created = Date.parse(payload.created_at || '');
    if (!Number.isFinite(created)) return { ok: false, reason: '交接时间无效。' };
    const age = (now == null ? Date.now() : Number(now)) - created;
    if (age < -60000 || age > WASPACK_HANDOFF_MAX_AGE) return { ok: false, reason: '交接链接已过期，请从投产历史重新发起对比。' };
    return { ok: true, payload: { left: { kind: 'local', path: String(left.path) }, right: { kind: 'local', path: String(right.path) }, created_at: payload.created_at, source: 'waspack-history' } };
  }
  function consumeWaspackHandoff(nonce, storage, now) {
    nonce = String(nonce || '');
    if (!/^[A-Za-z0-9_-]{4,120}$/.test(nonce)) return { ok: false, reason: '交接标识无效。' };
    storage = storage || (typeof window !== 'undefined' ? window.localStorage : null);
    if (!storage) return { ok: false, reason: '当前浏览器不支持一次性交接存储。' };
    const key = WASPACK_HANDOFF_PREFIX + nonce;
    let raw = null;
    try { raw = storage.getItem(key); storage.removeItem(key); } catch (_) { return { ok: false, reason: '读取交接数据失败。' }; }
    if (!raw) return { ok: false, reason: '交接数据不存在、已使用或已过期。' };
    try { return validateWaspackHandoff(JSON.parse(raw), now); } catch (_) { return { ok: false, reason: '交接数据无法解析。' }; }
  }
  function handoffNonceFromHash(hash) {
    const match = /[?&]handoff=([^&#]+)/.exec(String(hash || ''));
    if (!match) return '';
    try { return decodeURIComponent(match[1]); } catch (_) { return ''; }
  }

  function icon(name) {
    const paths = {
      compare: 'M8 7h11M15 3l4 4-4 4M16 17H5M9 13l-4 4 4 4',
      prev: 'M12 19V5M5 12l7-7 7 7', next: 'M12 5v14M19 12l-7 7-7-7',
      arrowUp: 'M12 19V5M5 12l7-7 7 7', arrowDown: 'M12 5v14M19 12l-7 7-7-7',
      undo: 'M3 7v6h6M3 13C5.5 8 11.5 6 16.5 8.5 20 10.5 21.5 14 21 18',
      redo: 'M21 7v6h-6M21 13c-2.5-5-8.5-7-13.5-4.5C4 10.5 2.5 14 3 18',
      open: 'M3 7h6l2 2h10v10H3z', save: 'M5 3h12l2 2v16H5zM8 3v6h8V3M8 21v-7h8v7',
      swap: 'M7 7h12M15 3l4 4-4 4M17 17H5M9 13l-4 4 4 4',
      left: 'M19 12H5M11 18l-6-6 6-6', right: 'M5 12h14M13 6l6 6-6 6',
      close: 'M6 6l12 12M18 6L6 18', folder: 'M3 6h7l2 2h9v11H3z',
      expand: 'M9 18l6-6-6-6', collapse: 'M6 9l6 6 6-6',
      lineRight: 'M5 12h12M13 8l4 4-4 4', lineLeft: 'M19 12H7M11 8l-4 4 4 4',
      copy: 'M8 7H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-2M8 7a2 2 0 012-2h8a2 2 0 012 2v8a2 2 0 01-2 2H10a2 2 0 01-2-2V7z',
      download: 'M12 3v13M8 12l4 4 4-4M4 17v2a1 1 0 001 1h14a1 1 0 001-1v-2',
      check: 'M5 13l4 4L19 7',
      eye: 'M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6-10-6-10-6zm10 2a2 2 0 100-4 2 2 0 000 4z',
      selectAll: 'M3 7V5a2 2 0 012-2h2M17 3h2a2 2 0 012 2v2M21 17v2a2 2 0 01-2 2h-2M7 21H5a2 2 0 01-2-2v-2',
      edit: 'M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z',
      file: 'M6 3h8l4 4v14H6a2 2 0 01-2-2V5a2 2 0 012-2zm7 0v5h5M8 13h8M8 17h6',
      warning: 'M12 3l9 17H3L12 3zm0 5v5m0 3v.01',
      server: 'M4 4h16v5H4zM4 15h16v5H4zM7 6.5h.01M7 17.5h.01M10 6.5h7M10 17.5h7',
      shield: 'M12 3l7 3v5c0 4.4-2.9 8.3-7 10-4.1-1.7-7-5.6-7-10V6l7-3zM9 12l2 2 4-4'
    };
    return '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="' + (paths[name] || paths.compare) + '"/></svg>';
  }

  function loadLegacyOptions() {
    const defaults = { trim_space: true, ignore_blank: false, ignore_case: false, mode: 'side', onlyDiff: false, backup: true, max_depth: 1 };
    try { return Object.assign(defaults, JSON.parse(localStorage.getItem(LS_OPTIONS) || '{}')); } catch (_) { return defaults; }
  }
  function loadLegacySources() {
    try {
      const value = JSON.parse(localStorage.getItem(LS_SOURCES) || '{}');
      ['left', 'right'].forEach(k => { if (value[k]) value[k].password = ''; });
      return value;
    } catch (_) { return {}; }
  }
  async function loadWorkbenchPreference() {
    const legacy = { options: loadLegacyOptions(), sources: loadLegacySources() };
    try {
      const remote = await getPreference('compare');
      persisted = remote.exists && remote.value && typeof remote.value === 'object' ? remote.value : legacy;
      if (!remote.exists) await putPreference('compare', persisted);
      localStorage.removeItem(LS_OPTIONS);
      localStorage.removeItem(LS_SOURCES);
    } catch (_) { persisted = legacy; }
    persisted.options = Object.assign(loadLegacyOptions(), persisted.options || {});
    persisted.sources = persisted.sources || {};
    persisted.folder_history = persisted.folder_history && typeof persisted.folder_history === 'object'
      ? { left: Array.isArray(persisted.folder_history.left) ? persisted.folder_history.left : [], right: Array.isArray(persisted.folder_history.right) ? persisted.folder_history.right : [] }
      : { left: [], right: [] };
    ['left', 'right'].forEach(function (side) {
      persisted.folder_history[side] = persisted.folder_history[side]
        .map(normalizeFolderHistoryEntry)
        .filter(Boolean);
    });
    ['left', 'right'].forEach(function (key) {
      if (persisted.sources[key]) delete persisted.sources[key].password;
    });
    return persisted;
  }
  function saveOptions(options) {
    persisted.options = Object.assign({}, options);
    persistPreference(persisted);
  }
  function saveSources(state) {
    try {
      const clean = {};
      ['left', 'right'].forEach(k => { clean[k] = sourceIdentity(state[k].source); });
      persisted.sources = clean;
      persistPreference(persisted);
    } catch (_) {}
  }

  // Persist only the source identity. Credentials are intentionally excluded so a
  // remote history entry can be restored without leaking a password to preferences.
  // Keep both tls and tls_mode while migrating old records: the API uses tls_mode,
  // while the UI/history contract calls the field tls.
  function sourceIdentity(source) {
    source = source || {};
    const tls = source.tls || source.tls_mode || '';
    return {
      kind: source.kind || 'local',
      system: source.system || '',
      server: source.server || '',
      host: source.host || '',
      port: Number(source.port) || (source.kind === 'ftp' ? 21 : 22),
      tls: tls,
      tls_mode: source.tls_mode || tls,
      path: source.path || '',
      username: source.username || '',
      encoding: source.encoding || 'auto',
      label: source.label || ''
    };
  }

  function normalizeFolderHistoryEntry(entry) {
    if (typeof entry === 'string') return sourceIdentity({ kind: 'local', path: entry });
    if (!entry || typeof entry !== 'object') return null;
    return sourceIdentity(entry);
  }

  function sourceSecurityLabel(source) {
    source = source || {};
    if (source.kind === 'local' || source.kind === 'text') return '本地';
    if (source.kind === 'sftp') return 'SSH 安全连接';
    if (source.kind === 'ftp') {
      const tls = source.tls || source.tls_mode;
      return tls ? 'FTPS ' + (tls === 'implicit' ? '隐式' : '显式') : 'FTP 明文';
    }
    return '未连接';
  }

  function sourceProtocolLabel(source) {
    source = source || {};
    if (source.kind === 'text') return '文本';
    if (source.kind === 'local' || !source.kind) return '本地';
    return String(source.kind).toUpperCase();
  }

  function sourceIdentityLabel(source) {
    source = source || {};
    const protocol = sourceProtocolLabel(source);
    let endpoint = '';
    if (source.kind === 'sftp') endpoint = [source.system, source.server].filter(Boolean).join(' / ') || source.host || '未配置服务器';
    else if (source.kind === 'ftp') endpoint = (source.host || '未配置主机') + (source.port ? ':' + source.port : '');
    else if (source.kind === 'local') endpoint = '本地文件系统';
    else endpoint = source.label || '临时文本';
    return protocol + ' · ' + endpoint + ' · ' + sourceSecurityLabel(source);
  }

  function defaultSource(side) {
    return { kind: 'text', path: '', label: side === 'left' ? '左侧文本' : '右侧文本', system: '', server: '', host: '', port: 21, username: '', password: '', tls_mode: '', encoding: 'auto' };
  }
  function spec(source) { const out = Object.assign({}, source); delete out.label; return out; }
  function sourceLabel(source) {
    if (source.kind === 'text') return source.label || '未命名文本';
    if (source.kind === 'sftp') return (source.server || 'SFTP') + ':' + source.path;
    if (source.kind === 'ftp') return (source.host || 'FTP') + ':' + source.path;
    return source.path || '本地文件';
  }
  function joinPath(source, root, rel) {
    const isWin = source.kind === 'local' && /^([A-Za-z]:[\\/]|\\\\|[A-Za-z]:$)/.test(root);
    const sep = isWin ? '\\' : '/';
    const cleanRoot = isWin ? String(root || '').replace(/\//g, '\\').replace(/\\+$/, '') : String(root || '').replace(/\\/g, '/').replace(/\/+$/, '');
    const cleanRel = isWin ? String(rel || '').replace(/\//g, '\\').replace(/^\\+/, '') : String(rel || '').replace(/\\/g, '/').replace(/^\/+/, '');
    return cleanRoot + sep + cleanRel;
  }
  function makeButton(text, name, onclick, className) {
    return el('button', { class: className || 'btn btn-sm', type: 'button', title: text, 'aria-label': text, onclick, unsafeHtml: (name ? icon(name) : '') + '<span>' + text + '</span>' });
  }
  function makeCheck(label, checked, onchange) {
    const input = el('input', { type: 'checkbox' }); input.checked = !!checked;
    input.addEventListener('change', () => onchange(input.checked));
    return el('label', { class: 'cmp-wb-check' }, [input, document.createTextNode(label)]);
  }

  function textMetrics(value, lineLimit) {
    const text = String(value == null ? '' : value);
    if (text === '') return { chars: 0, lines: 1 };
    const limit = Number(lineLimit) > 0 ? Number(lineLimit) : 0;
    let lines = 1;
    // 一旦超过护栏就提前结束，输入事件不必为大文本扫描完整字符串。
    for (let i = 0; i < text.length; i++) {
      if (text.charCodeAt(i) === 10) {
        lines++;
        if (limit && lines > limit) return { chars: text.length, lines: limit + 1 };
      }
    }
    return { chars: text.length, lines };
  }

  function isLargeText(value, thresholds) {
    const limits = thresholds || {};
    const metrics = textMetrics(value, Number(limits.maxLines) || AUTO_COMPARE_MAX_LINES);
    return metrics.chars > (Number(limits.maxChars) || AUTO_COMPARE_MAX_CHARS) ||
      metrics.lines > (Number(limits.maxLines) || AUTO_COMPARE_MAX_LINES);
  }

  // 编辑器历史是“快照”而不是逐键记录：输入在短时间内合并，
  // 但撤销/重做前会立即 flush，因此第一次撤销永远回到加载后的内容，
  // 不会错误地回到空字符串。
  function createEditorHistory(initial, limit) {
    const max = Math.max(2, Number(limit) || 100);
    let values = [String(initial == null ? '' : initial)];
    let index = 0;
    let pending = null;
    let timer = 0;

    function commit(value) {
      if (timer) { clearTimeout(timer); timer = 0; }
      const next = String(value == null ? '' : value);
      if (values[index] === next) { pending = null; return false; }
      values = values.slice(0, index + 1);
      values.push(next);
      if (values.length > max) values.shift();
      index = values.length - 1;
      pending = null;
      return true;
    }
    function flush(value) {
      if (timer) { clearTimeout(timer); timer = 0; }
      if (pending !== null) {
        const next = pending;
        pending = null;
        return commit(next);
      }
      if (value !== undefined) return commit(value);
      return false;
    }
    function schedule(value, delay) {
      const next = String(value == null ? '' : value);
      if (values[index] === next && pending === null) return;
      pending = next;
      if (timer) clearTimeout(timer);
      timer = setTimeout(function () { timer = 0; flush(); }, Number(delay) || 150);
    }
    function reset(value) {
      if (timer) { clearTimeout(timer); timer = 0; }
      const next = String(value == null ? '' : value);
      values = [next];
      index = 0;
      pending = null;
    }
    function undo(current) {
      flush(current);
      if (index <= 0) return values[0];
      index--;
      return values[index];
    }
    function redo(current) {
      flush(current);
      if (index >= values.length - 1) return values[index];
      index++;
      return values[index];
    }
    function dispose() {
      if (timer) clearTimeout(timer);
      timer = 0;
      pending = null;
    }
    return {
      schedule,
      commit,
      flush,
      reset,
      undo,
      redo,
      dispose,
      canUndo: function () { flush(); return index > 0; },
      canRedo: function () { flush(); return index < values.length - 1; },
      snapshot: function () { flush(); return { values: values.slice(), index }; }
    };
  }

  function resolveSaveSide(activeElement) {
    let node = activeElement;
    while (node) {
      const cls = typeof node.getAttribute === 'function' ? node.getAttribute('class') || '' : node.className || '';
      if ((' ' + cls + ' ').indexOf(' cmp-diff-left ') >= 0) return 'left';
      if ((' ' + cls + ' ').indexOf(' cmp-diff-right ') >= 0) return 'right';
      if (typeof node.getAttribute === 'function') {
        const side = node.getAttribute('data-side');
        if (side === 'left' || side === 'right') return side;
      }
      node = node.parentElement || node.parentNode;
    }
    return null;
  }

  function makeCompareKey(left, right, options, leftLabel, rightLabel) {
    const opts = options || {};
    return JSON.stringify({
      left: String(left == null ? '' : left),
      right: String(right == null ? '' : right),
      left_label: String(leftLabel || ''),
      right_label: String(rightLabel || ''),
      ignore: {
        trim_space: !!opts.trim_space,
        ignore_blank: !!opts.ignore_blank,
        ignore_case: !!opts.ignore_case
      }
    });
  }

  function createEditor(side, onDirty) {
    const gutter = el('pre', { class: 'cmp-editor-gutter', text: '1' });
    const textarea = el('textarea', { class: 'cmp-editor-input', spellcheck: 'false', wrap: 'off', 'aria-label': side === 'left' ? '左侧文本编辑器' : '右侧文本编辑器', placeholder: side === 'left' ? '粘贴、拖入文本或文件；也可点打开' : '粘贴、拖入修改后的文本或文件' });
    const highlight = el('pre', { class: 'cmp-editor-highlight', 'aria-hidden': 'true' });
    let language = 'text';
    const syntax = Kairo.workbench && Kairo.workbench.syntaxEditor;
    const history = createEditorHistory(textarea.value || '', 100);
    const requestFrame = typeof requestAnimationFrame === 'function' ? requestAnimationFrame : function (fn) { return setTimeout(fn, 0); };
    const cancelFrame = typeof cancelAnimationFrame === 'function' ? cancelAnimationFrame : clearTimeout;
    let visualFrame = 0;
    let disposed = false;

    function refreshVisuals() {
      if (disposed) return;
      const value = textarea.value || '';
      const metrics = textMetrics(value, GUTTER_MAX_LINES);
      const compactGutter = metrics.lines > GUTTER_MAX_LINES;
      const compactHighlight = metrics.chars > HIGHLIGHT_MAX_CHARS;
      if (textarea.classList) textarea.classList.toggle('cmp-editor-large', compactGutter || compactHighlight);
      if (compactGutter) {
        gutter.textContent = '… ' + metrics.lines + ' 行';
      } else {
        let numbers = '';
        for (let i = 1; i <= metrics.lines; i++) numbers += i + '\n';
        gutter.textContent = numbers;
      }
      if (!syntax || compactHighlight) {
        highlight.textContent = '';
        highlight.setAttribute('data-state', compactHighlight ? 'disabled-large' : 'plain');
      } else {
        try {
          highlight.innerHTML = syntax.highlight(value, language);
          highlight.removeAttribute('data-state');
        } catch (_) {
          // 语法高亮是增强功能，解析失败时仍保持纯文本编辑可用。
          highlight.textContent = '';
          highlight.setAttribute('data-state', 'plain');
        }
      }
    }
    function scheduleVisuals() {
      if (disposed) return;
      if (visualFrame) cancelFrame(visualFrame);
      visualFrame = requestFrame(function () { visualFrame = 0; refreshVisuals(); });
    }
    function updateGutter() { scheduleVisuals(); }
    function normalizeValue(value) { return String(value == null ? '' : value).replace(/\r\n?/g, '\n'); }
    function setValue(value, opts) {
      const next = normalizeValue(value);
      textarea.value = next;
      if (opts && opts.resetHistory) history.reset(next);
      else history.commit(next);
      scheduleVisuals();
    }
    function replaceCurrentValue(value) {
      setValue(value);
    }
    function undo() {
      const next = history.undo(textarea.value);
      if (next === textarea.value) return;
      textarea.value = next;
      scheduleVisuals();
      onDirty();
    }
    function redo() {
      const next = history.redo(textarea.value);
      if (next === textarea.value) return;
      textarea.value = next;
      scheduleVisuals();
      onDirty();
    }
    textarea.addEventListener('input', () => { history.schedule(textarea.value); scheduleVisuals(); onDirty(); });
    textarea.addEventListener('keydown', function(e) {
      if ((e.ctrlKey || e.metaKey) && (e.key === 'z' || e.key === 'Z')) {
        e.preventDefault();
        if (e.shiftKey) redo();
        else undo();
      } else if ((e.ctrlKey || e.metaKey) && (e.key === 'y' || e.key === 'Y')) {
        e.preventDefault();
        redo();
      }
    });

    textarea.addEventListener('scroll', () => { gutter.scrollTop = textarea.scrollTop; highlight.scrollTop = textarea.scrollTop; highlight.scrollLeft = textarea.scrollLeft; });
    const editorLayer = el('div', { class: 'cmp-editor-layer' }, [highlight, textarea]);
    const root = el('div', { class: 'cmp-editor', 'data-side': side, role: 'group', 'aria-label': side === 'left' ? '左侧原文' : '右侧原文' }, [gutter, editorLayer]);
    ['dragenter', 'dragover'].forEach(function (type) {
      root.addEventListener(type, function (e) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; root.classList.add('cmp-drop-hover'); });
    });
    root.addEventListener('dragleave', function (e) { if (!root.contains(e.relatedTarget)) root.classList.remove('cmp-drop-hover'); });
    root.addEventListener('drop', function (e) {
      e.preventDefault();
      root.classList.remove('cmp-drop-hover');
      const file = e.dataTransfer.files && e.dataTransfer.files[0];
      if (file && file.type.indexOf('image') !== 0) {
        const reader = new FileReader();
        reader.onload = function () { if (disposed) return; setValue(String(reader.result || '')); onDirty(); };
        reader.readAsText(file);
        return;
      }
      const text = e.dataTransfer.getData('text/plain');
      if (text) { setValue(text); onDirty(); }
    });
    return { root: root, textarea,
      setValue,
      replaceCurrentValue,
      setLanguage(value) { language = value || 'text'; scheduleVisuals(); },
      getValue() { return textarea.value; },
      getMetrics() { return textMetrics(textarea.value, AUTO_COMPARE_MAX_LINES); },
      undo, redo, updateGutter,
      resetHistory() { history.reset(textarea.value); },
      canUndo() { return history.canUndo(); },
      canRedo() { return history.canRedo(); },
      dispose() { disposed = true; if (visualFrame) cancelFrame(visualFrame); visualFrame = 0; history.dispose(); }
    };
  }

  async function renderCompare(view) {
    const renderToken = view.dataset.renderToken;
    if (scanPollTimer) clearTimeout(scanPollTimer);
    activeScanJob = '';
    view.innerHTML = '<div class="card muted">正在恢复比较工作台偏好…</div>';
    // Consume the one-time WASPACK handoff before preference restoration can
    // yield. The storage entry is removed even when invalid, preventing replay.
    const handoffNonce = handoffNonceFromHash(window.location && window.location.hash);
    const handoffResult = handoffNonce ? consumeWaspackHandoff(handoffNonce) : null;
    const preference = await loadWorkbenchPreference();
    if (view.dataset.renderToken !== renderToken) return;
    view.innerHTML = '';
    const options = preference.options, saved = preference.sources;
    const state = {
      left: { source: Object.assign(defaultSource('left'), saved.left || {}), version: null, codec: { encoding: 'utf-8', eol: 'lf', bom: false }, dirty: false, loadState: 'ready', loadError: null, loadSeq: 0, editSeq: 0, saving: false },
      right: { source: Object.assign(defaultSource('right'), saved.right || {}), version: null, codec: { encoding: 'utf-8', eol: 'lf', bom: false }, dirty: false, loadState: 'ready', loadError: null, loadSeq: 0, editSeq: 0, saving: false },
      diff: null, hunks: [], hunkIndex: -1, mode: 'text', scan: null,
    };
    const crumb = document.getElementById('crumb'); if (crumb) crumb.textContent = '文件与文本比较';
    const compareID = 'cmp-' + Date.now().toString(36);
    const root = el('div', { class: 'cmp-workbench cmp2-workbench', 'data-compare-root': compareID });
    const title = el('h1', { class: 'cmp-sr-only', text: '文件与文本比较工作台' });
    if (handoffNonce) {
      const noticeText = handoffResult && handoffResult.ok
        ? '已载入投产历史的两个输出目录，已切换到文件夹比较，正在自动扫描目录快照。'
        : '投产历史对比交接不可用：' + ((handoffResult && handoffResult.reason) || '交接数据无效。');
      root.appendChild(el('div', {
        class: 'cmp-waspack-handoff-notice',
        role: handoffResult && handoffResult.ok ? 'status' : 'alert',
        'aria-live': handoffResult && handoffResult.ok ? 'polite' : 'assertive',
        text: noticeText
      }));
    }
    const textTab = el('button', {
      class: 'cmp-wb-tab active', text: '文本 / 文件比较', role: 'tab', type: 'button',
      id: compareID + '-tab-text', 'aria-controls': compareID + '-panel-text', 'aria-selected': 'true', tabindex: '0'
    });
    const folderTab = el('button', {
      class: 'cmp-wb-tab', text: '文件夹比较', role: 'tab', type: 'button',
      id: compareID + '-tab-folder', 'aria-controls': compareID + '-panel-folder', 'aria-selected': 'false', tabindex: '-1'
    });
    const tabs = el('div', { class: 'cmp-wb-tabs cmp2-tabs', role: 'tablist', 'aria-label': '比较类型' }, [textTab, folderTab]);
    const textPanel = el('section', { class: 'cmp-wb-panel', role: 'tabpanel', id: compareID + '-panel-text', 'aria-labelledby': textTab.id, tabindex: '0' });
    const folderPanel = el('section', { class: 'cmp-wb-panel', role: 'tabpanel', id: compareID + '-panel-folder', 'aria-labelledby': folderTab.id, tabindex: '0', hidden: true });
    root.insertBefore(title, root.firstChild || null);
    root.append(tabs, textPanel, folderPanel); view.appendChild(root);
    textTab.onclick = () => switchTab('text', true); folderTab.onclick = () => switchTab('folder', true);
    tabs.addEventListener('keydown', function (event) {
      const tabNodes = [textTab, folderTab];
      const current = tabNodes.indexOf(document.activeElement);
      if (current < 0) return;
      let next = current;
      if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (current + 1) % tabNodes.length;
      else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (current - 1 + tabNodes.length) % tabNodes.length;
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = tabNodes.length - 1;
      else if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); switchTab(current === 0 ? 'text' : 'folder', true); return; }
      else return;
      event.preventDefault();
      tabNodes[next].focus();
      switchTab(next === 0 ? 'text' : 'folder', false);
    });
    function switchTab(mode, moveFocus) {
      state.mode = mode; textTab.classList.toggle('active', mode === 'text'); folderTab.classList.toggle('active', mode === 'folder');
      textTab.setAttribute('aria-selected', mode === 'text' ? 'true' : 'false');
      folderTab.setAttribute('aria-selected', mode === 'folder' ? 'true' : 'false');
      textTab.tabIndex = mode === 'text' ? 0 : -1;
      folderTab.tabIndex = mode === 'folder' ? 0 : -1;
      textPanel.hidden = mode !== 'text'; folderPanel.hidden = mode !== 'folder';
      if (moveFocus) (mode === 'text' ? textPanel : folderPanel).focus({ preventScroll: true });
    }
    const textCleanup = buildTextWorkbench(textPanel, state, options);
    const folderWorkbench = buildFolderWorkbench(folderPanel, state, options, switchTab, handoffResult && handoffResult.ok ? handoffResult.payload : null);
    if (handoffResult && handoffResult.ok) {
      switchTab('folder', false);
      // The builder has created and populated both path inputs. Start only now,
      // after the DOM exists; this path intentionally skips preference/history
      // writes so receipt alone never persists over the user's compare setup.
      setTimeout(function () {
        if (view.dataset.renderToken !== renderToken || !folderWorkbench || typeof folderWorkbench.startScan !== 'function') return;
        folderWorkbench.startScan({ persist: false });
      }, 0);
    }
    ensureConnections();
    return function () { if (typeof textCleanup === 'function') textCleanup(); };
  }

  function buildTextWorkbench(panel, state, options) {
    let showingResult = false, syncLock = false, compareSeq = 0;
    let disposed = false, compareInFlight = null, lastCompareKey = '', recompareTimer = 0;
    const editorLeft = createEditor('left', () => { markDirty('left'); scheduleRecompare(); });
    const editorRight = createEditor('right', () => { markDirty('right'); scheduleRecompare(); });
    const editors = { left: editorLeft, right: editorRight };
    editorLeft.textarea.addEventListener('scroll', () => syncScroll(editorLeft, editorRight));
    editorRight.textarea.addEventListener('scroll', () => syncScroll(editorRight, editorLeft));
    function syncScroll(from, to) {
      if (syncLock) return; syncLock = true;
      const maxFrom = Math.max(1, from.textarea.scrollHeight - from.textarea.clientHeight);
      const maxTo = Math.max(0, to.textarea.scrollHeight - to.textarea.clientHeight);
      to.textarea.scrollTop = (from.textarea.scrollTop / maxFrom) * maxTo; to.textarea.scrollLeft = from.textarea.scrollLeft; syncLock = false;
    }
    const sourceHeaders = {};
    const status = el('div', { class: 'cmp-wb-status', text: '就绪 · Ctrl/⌘ + Enter 开始比较' });
    const resultHost = el('div', { class: 'cmp-result-host', style: 'display:none' });
    const editorGrid = el('div', { class: 'cmp-editor-grid' });
    ['left', 'right'].forEach(side => {
      const badge = el('span', { class: 'cmp-source-badge', text: side === 'left' ? '左' : '右' });
      const label = el('span', { class: 'cmp-source-label', text: sourceLabel(state[side].source), title: sourceLabel(state[side].source) });
      const meta = el('span', { class: 'cmp-source-meta', text: '' });
      const dirty = el('span', { class: 'cmp-dirty', text: '' });
      const language = el('select', { class: 'cmp-language', title: '语法高亮' }, [el('option', { value: 'text', text: '文本' }), el('option', { value: 'java', text: 'Java' }), el('option', { value: 'sql', text: 'SQL' }), el('option', { value: 'xml', text: 'XML' }), el('option', { value: 'json', text: 'JSON' })]);
      language.onchange = function () { editors[side].setLanguage(language.value); if (state.diff) renderResult(); };
      const undoBtn = makeButton('撤销', 'undo', () => editors[side].undo(), 'btn btn-sm');
      const redoBtn = makeButton('重做', 'redo', () => editors[side].redo(), 'btn btn-sm');
      const openBtn = makeButton('打开', 'open', () => openSourceDialog(state[side].source, false, async source => { state[side].source = source; saveSources(state); await loadSide(side); }), 'btn btn-sm');
      const saveBtn = makeButton('保存', 'save', () => saveSide(side), 'btn btn-sm'); saveBtn.disabled = true;
      sourceHeaders[side] = { badge, label, meta, dirty, saveBtn, language, undoBtn, redoBtn };
      const info = el('div', { class: 'cmp-source-info' }, [badge, label, meta, dirty]);
      const actions = el('div', { class: 'cmp-source-actions cmp2-source-actions' }, [language, undoBtn, redoBtn, openBtn, saveBtn]);
       editorGrid.appendChild(el('section', { class: 'cmp-editor-pane cmp2-editor-pane', 'data-side': side }, [el('div', { class: 'cmp-source-header cmp2-source-header' }, [info, actions]), editors[side].root]));
    });
    const compareBtn = makeButton('开始比较', 'compare', () => compareNow(false, true), 'btn btn-primary btn-sm');
    compareBtn.setAttribute('data-action', 'text-compare');
    const editBtn = makeButton('返回编辑', 'open', showEditors, 'btn btn-sm'); editBtn.style.display = 'none';
    const saveLeftBtn = makeButton('保存左侧', 'save', () => saveSide('left'), 'btn btn-sm'); saveLeftBtn.setAttribute('data-action', 'save-left'); saveLeftBtn.style.display = 'none';
    const saveRightBtn = makeButton('保存右侧', 'save', () => saveSide('right'), 'btn btn-sm'); saveRightBtn.setAttribute('data-action', 'save-right'); saveRightBtn.style.display = 'none';
    const dirtyBanner = el('span', { class: 'cmp-dirty-banner', text: '' });

    const toggleEditorsBtn = makeButton('展开原文件编辑', 'expand', toggleEditors, 'btn btn-sm');
    const copySelRightBtn = makeButton('覆盖到右侧 →', 'right', () => copySelectionToSide('right'), 'btn btn-sm');
    const copySelLeftBtn = makeButton('← 覆盖到左侧', 'left', () => copySelectionToSide('left'), 'btn btn-sm');
    const selectAllBtn = makeButton('全选内容', 'selectAll', selectAllDiff, 'btn btn-sm');
    const copyLeftAllBtn = makeButton('复制左侧全部', 'copy', () => copySideAll('left'), 'btn btn-sm');
    const copyRightAllBtn = makeButton('复制右侧全部', 'copy', () => copySideAll('right'), 'btn btn-sm');
    const diffCountBadge = el('span', { class: 'cmp-diff-count-badge', text: '未比较' });

    function toggleEditors() {
      const isHidden = editorGrid.style.display === 'none';
      editorGrid.style.display = isHidden ? '' : 'none';
      const label = toggleEditorsBtn.querySelector('span');
      if (label) label.textContent = isHidden ? '折叠原文件' : '展开原文件编辑';
      else toggleEditorsBtn.textContent = isHidden ? '折叠原文件' : '展开原文件编辑';
      // 切换图标：收起→展开
      const svg = toggleEditorsBtn.querySelector('svg');
      if (svg) {
        const useCollapse = isHidden;
        svg.innerHTML = '<path d="' + (useCollapse ? 'M6 9l6 6 6-6' : 'M9 18l6-6-6-6') + '"/>';
        svg.setAttribute('aria-hidden', 'true');
      }
    }

    function copySideAll(side) {
      const text = editors[side].getValue();
      if (!text) return toast((side === 'left' ? '左' : '右') + '侧内容为空', 'warn');
      const p = (typeof copyToClipboard === 'function')
        ? copyToClipboard(text)
        : (navigator.clipboard && window.isSecureContext
          ? navigator.clipboard.writeText(text)
          : Promise.reject(new Error('no clipboard')));
      p.then(
        () => toast('已复制' + (side === 'left' ? '左' : '右') + '侧全部内容', 'ok'),
        () => toast('复制失败', 'err')
      );
    }

    function selectAllDiff() {
      const vdiff = resultHost.querySelector('.cmp-vdiff');
      if (!vdiff) return;
      const sel = window.getSelection();
      if (!sel) return;
      sel.removeAllRanges();
      const range = document.createRange();
      range.selectNodeContents(vdiff.querySelector('.cmp-vdiff-canvas') || vdiff);
      sel.addRange(range);
      toast('已全选比对内容，可直接 Ctrl+C 复制', 'ok');
    }

    function copySelectionToSide(toSide) {
      const fromSide = toSide === 'right' ? 'left' : 'right';
      const fromTa = editors[fromSide].textarea;
      const toTa = editors[toSide].textarea;
      const winSel = window.getSelection() ? window.getSelection().toString() : '';
      let textToCopy = '';
      if (winSel && winSel.trim()) {
        textToCopy = winSel;
      } else if (fromTa.selectionStart !== fromTa.selectionEnd) {
        textToCopy = fromTa.value.substring(fromTa.selectionStart, fromTa.selectionEnd);
      } else {
        textToCopy = fromTa.value;
      }
      if (!textToCopy) return toast('源文本为空', 'warn');
      let nextValue;
      if (toTa.selectionStart != null && toTa.selectionStart !== toTa.selectionEnd) {
        nextValue = toTa.value.substring(0, toTa.selectionStart) + textToCopy + toTa.value.substring(toTa.selectionEnd);
      } else {
        nextValue = textToCopy;
      }
      editors[toSide].setValue(nextValue);
      markDirty(toSide);
      scheduleRecompare();
      toast('已覆盖到' + (toSide === 'right' ? '右侧' : '左侧'), 'ok');
    }

    const prevBtn = makeButton('上一处', 'arrowUp', () => navigateHunk(-1), 'btn btn-sm');
    prevBtn.title = '上一处差异 (Alt+↑ / F7)';
    const nextBtn = makeButton('下一处', 'arrowDown', () => navigateHunk(1), 'btn btn-sm');
    nextBtn.title = '下一处差异 (Alt+↓ / F8)';
    const prevLineBtn = makeButton('上一行', 'prev', () => navigateLine(-1), 'btn btn-sm');
    prevLineBtn.title = '移动到上一行 (↑)';
    const nextLineBtn = makeButton('下一行', 'next', () => navigateLine(1), 'btn btn-sm');
    nextLineBtn.title = '移动到下一行 (↓)';
    const swapBtn = makeButton('交换', 'swap', swapSides, 'btn btn-sm');
    const copyBtn = makeButton('复制 Diff', 'copy', copyDiff, 'btn btn-sm'), downloadBtn = makeButton('下载 Diff', 'download', downloadDiff, 'btn btn-sm');
    [prevBtn, nextBtn, prevLineBtn, nextLineBtn, copyBtn, downloadBtn].forEach(btn => btn.disabled = true);
    const modeSelect = el('select', { class: 'cmp-mode-select' }, [el('option', { value: 'side', text: '左右并排' }), el('option', { value: 'unified', text: 'Unified' }), el('option', { value: 'changes', text: '仅差异' })]);
    options.mode = options.mode || 'side';
    modeSelect.value = options.mode; modeSelect.onchange = () => { options.mode = modeSelect.value; saveOptions(options); if (state.diff) renderResult(); };
    const comparisonOptionChanged = function () {
      saveOptions(options);
      if (!state.diff) return;
      state.diffStale = true;
      scheduleRecompare(0, true);
    };
    const trim = makeCheck('忽略行尾空白', options.trim_space, value => { options.trim_space = value; comparisonOptionChanged(); });
    const blank = makeCheck('忽略空行', options.ignore_blank, value => { options.ignore_blank = value; comparisonOptionChanged(); });
    const ignoreCase = makeCheck('忽略大小写', options.ignore_case, value => { options.ignore_case = value; comparisonOptionChanged(); });
    const backup = makeCheck('替换前备份', options.backup, value => { options.backup = value; saveOptions(options); });
    const navGroup = el('div', { class: 'cmp2-segment cmp2-nav', 'aria-label': '差异导航' }, [prevBtn, diffCountBadge, nextBtn]);
    const optionDetails = el('details', { class: 'cmp2-popover' }, [
      el('summary', { text: '比较选项' }),
      el('div', { class: 'cmp2-popover-panel' }, [trim, blank, ignoreCase, backup])
    ]);
    const moreDetails = el('details', { class: 'cmp2-popover cmp2-more' }, [
      el('summary', { text: '更多操作' }),
      el('div', { class: 'cmp2-popover-panel cmp2-menu-panel' }, [
        toggleEditorsBtn, copySelRightBtn, copySelLeftBtn, selectAllBtn,
        copyLeftAllBtn, copyRightAllBtn, copyBtn, downloadBtn, swapBtn,
        saveLeftBtn, saveRightBtn, prevLineBtn, nextLineBtn
      ])
    ]);
    const toolbar = el('div', { class: 'cmp2-commandbar' }, [
      el('div', { class: 'cmp2-commandbar-primary' }, [compareBtn, navGroup, dirtyBanner]),
      el('div', { class: 'cmp2-commandbar-secondary' }, [
        el('label', { class: 'cmp2-field-inline' }, [el('span', { text: '视图' }), modeSelect]),
        optionDetails, moreDetails
      ])
    ]);
    const stickyHeader = el('div', { class: 'cmp-text-sticky-head cmp2-text-head' }, [toolbar]);
    panel.append(stickyHeader, editorGrid, resultHost, status);
    panel.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); compareNow(false, true); }
      if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
        e.preventDefault();
        const side = resolveSaveSide(document.activeElement);
        if (side) saveSide(side);
        else status.textContent = '请先把焦点放在左侧或右侧编辑器/差异行，再保存';
      }
      if ((e.altKey && e.key === 'ArrowUp') || e.key === 'F7') { e.preventDefault(); navigateHunk(-1); }
      if ((e.altKey && e.key === 'ArrowDown') || e.key === 'F8') { e.preventDefault(); navigateHunk(1); }
      if (document.activeElement && document.activeElement.closest && document.activeElement.closest('.cmp-vdiff') && !document.activeElement.isContentEditable) {
        if (e.key === 'ArrowUp') { e.preventDefault(); navigateLine(-1); }
        if (e.key === 'ArrowDown') { e.preventDefault(); navigateLine(1); }
        if ((e.ctrlKey || e.metaKey) && (e.key === 'a' || e.key === 'A')) { e.preventDefault(); selectAllDiff(); }
      }
    });

    function refreshSaveButtons() {
      saveLeftBtn.disabled = state.left.source.kind === 'text' || !state.left.dirty || state.left.saving;
      saveRightBtn.disabled = state.right.source.kind === 'text' || !state.right.dirty || state.right.saving;
      saveLeftBtn.style.display = '';
      saveRightBtn.style.display = '';
      const bits = [];
      if (state.left.dirty) bits.push('左侧已修改');
      if (state.right.dirty) bits.push('右侧已修改');
      dirtyBanner.textContent = bits.join(' · ');
    }
    function invalidateComparison(message) {
      state.diff = null;
      state.diffStale = false;
      state.diffSourceKey = '';
      state.hunks = [];
      state.hunkIndex = -1;
      compareSeq++;
      compareInFlight = null;
      currentVirtualDiff = null;
      resultHost.innerHTML = '';
      resultHost.style.display = 'none';
      showingResult = false;
      panel.classList.remove('cmp-panel-has-result');
      editorGrid.style.display = '';
      compareBtn.disabled = false;
      if (message) status.textContent = message;
      [prevBtn, nextBtn, prevLineBtn, nextLineBtn, copyBtn, downloadBtn].forEach(btn => btn.disabled = true);
      refreshSaveButtons();
    }
    function markDirty(side) {
      const item = state[side];
      item.dirty = true;
      item.editSeq = (item.editSeq || 0) + 1;
      // 读取失败后，用户主动输入代表明确的替代内容，可以重新参与比较；
      // 加载中的编辑器会被禁用，不会误把异步响应覆盖掉。
      if (item.loadState === 'error') { item.loadState = 'ready'; item.loadError = null; }
      if (state.diff) state.diffStale = true;
      sourceHeaders[side].dirty.textContent = '● 已修改';
      sourceHeaders[side].saveBtn.disabled = item.source.kind === 'text' || item.saving;
      refreshSaveButtons();
    }
    function updateHeader(side) {
      const source = state[side].source; sourceHeaders[side].badge.textContent = source.kind === 'text' ? '文本' : source.kind.toUpperCase();
      sourceHeaders[side].label.textContent = sourceLabel(source); sourceHeaders[side].label.title = sourceLabel(source);
      const codec = state[side].codec; sourceHeaders[side].meta.textContent = source.kind === 'text' ? '' : String(codec.encoding || 'utf-8').toUpperCase() + ' · ' + String(codec.eol || 'lf').toUpperCase() + (codec.bom ? ' · BOM' : '');
      sourceHeaders[side].dirty.textContent = state[side].dirty ? '● 已修改' : ''; sourceHeaders[side].saveBtn.disabled = source.kind === 'text' || !state[side].dirty || state[side].saving;
      refreshSaveButtons();
    }
    async function loadSide(side) {
      const item = state[side];
      const source = Object.assign({}, item.source);
      const loadNo = ++item.loadSeq;
      const inferredLanguage = Kairo.workbench && Kairo.workbench.syntaxEditor ? Kairo.workbench.syntaxEditor.languageFromPath(source.path || source.label, 'text') : 'text';
      sourceHeaders[side].language.value = inferredLanguage;
      editors[side].setLanguage(inferredLanguage);
      item.loadError = null;
      item.loadState = source.kind === 'text' ? 'ready' : 'loading';
      editors[side].textarea.disabled = source.kind !== 'text';
      if (source.kind !== 'text') {
        // 切换来源时立即清空旧文本，避免读取失败后仍拿旧内容继续比对。
        editors[side].setValue('', { resetHistory: true });
        item.version = null;
        item.codec = { encoding: 'utf-8', eol: 'lf', bom: false };
        item.dirty = false;
        invalidateComparison('正在读取 ' + sourceLabel(source) + '…');
      } else {
        editors[side].setValue('', { resetHistory: true });
        item.version = null;
        item.codec = { encoding: 'utf-8', eol: 'lf', bom: false };
        item.dirty = false;
        invalidateComparison('已切换为手动文本');
      }
      updateHeader(side); if (source.kind === 'text') return true;
      status.textContent = '正在读取 ' + sourceLabel(source) + '…';
      try {
        const response = await api('POST', '/api/compare/read', { source: spec(source) });
        if (disposed || item.loadSeq !== loadNo) return false;
        if (response.binary) throw new Error('检测到二进制文件，文本工作台不支持直接编辑');
        if (response.truncated) throw new Error('文件超过 8MB 文本编辑上限，请使用文件夹比较进行流式复制');
        // 服务端返回的 entry.path 是已解析的完整路径；以它为准，让比对视图始终显示绝对路径。
        if (response.entry && response.entry.path) source.path = response.entry.path;
        editors[side].setLanguage(inferredLanguage); editors[side].setValue(response.text, { resetHistory: true });
        item.source = Object.assign(item.source, source);
        item.version = response.version;
        item.codec = { encoding: response.encoding || 'utf-8', eol: response.eol || 'lf', bom: !!response.bom };
        item.dirty = false;
        item.loadState = 'ready';
        editors[side].textarea.disabled = false;
        updateHeader(side);
        status.textContent = '已读取 ' + sourceLabel(source) + ' · ' + formatBytes(response.entry && response.entry.size);
        return true;
      } catch (error) {
        if (disposed || item.loadSeq !== loadNo) return false;
        item.loadState = 'error';
        item.loadError = error && error.message ? error.message : String(error);
        item.version = null;
        item.dirty = false;
        editors[side].setValue('', { resetHistory: true });
        editors[side].textarea.disabled = false;
        updateHeader(side);
        toast('读取失败：' + item.loadError, 'err');
        status.textContent = (side === 'left' ? '左侧' : '右侧') + '读取失败，请修正来源后重试';
        return false;
      }
    }
    async function saveSide(side) {
      if (disposed) return false;
      const item = state[side];
      if (item.source.kind === 'text' || !item.dirty || item.saving) return false;
      const sourceAtSave = spec(item.source);
      const loadNo = item.loadSeq;
      const editNo = item.editSeq;
      const content = editors[side].getValue();
      item.saving = true;
      updateHeader(side);
      try {
        await api('POST', '/api/compare/write', { target: sourceAtSave, content, expected: item.version, backup: !!options.backup, encoding: item.codec.encoding, eol: item.codec.eol, bom: !!item.codec.bom });
        if (disposed || item.loadSeq !== loadNo || item.editSeq !== editNo || JSON.stringify(spec(item.source)) !== JSON.stringify(sourceAtSave)) return false;
        item.dirty = false;
        updateHeader(side);
        await loadSide(side);
        if (!disposed) toast('保存成功' + (options.backup ? '，原文件已备份' : ''), 'ok');
        return true;
      } catch (error) {
        if (!disposed) toast('保存失败：' + (error.message || error), 'err');
        return false;
      } finally {
        item.saving = false;
        if (!disposed) updateHeader(side);
      }
    }
    function comparisonBlocked() {
      const failed = ['left', 'right'].filter(side => state[side].loadState !== 'ready');
      if (!failed.length) return '';
      return failed.map(side => state[side].loadState === 'error' ? (side === 'left' ? '左侧读取失败' : '右侧读取失败') : (side === 'left' ? '左侧正在读取' : '右侧正在读取')).join('、');
    }
    function autoCompareBlocked() {
      const metrics = { left: editorLeft.getMetrics(), right: editorRight.getMetrics() };
      const large = ['left', 'right'].filter(side => metrics[side].chars > AUTO_COMPARE_MAX_CHARS || metrics[side].lines > AUTO_COMPARE_MAX_LINES);
      return large.length ? large.map(side => (side === 'left' ? '左侧' : '右侧') + ' ' + metrics[side].lines + ' 行').join('、') : '';
    }
    async function compareNow(isAuto, force) {
      const auto = !!isAuto;
      if (disposed) return false;
      const blocked = comparisonBlocked();
      if (blocked) {
        status.textContent = blocked + '，请先完成加载后再比较';
        if (!auto) toast(status.textContent, 'warn');
        return false;
      }
      const large = autoCompareBlocked();
      if (auto && large) {
        state.diffStale = !!state.diff;
        status.textContent = large + '，已暂停自动比较；请点击“重新比较”';
        return false;
      }
      const leftText = editorLeft.getValue();
      const rightText = editorRight.getValue();
      const leftLabel = sourceLabel(state.left.source);
      const rightLabel = sourceLabel(state.right.source);
      const key = makeCompareKey(leftText, rightText, options, leftLabel, rightLabel);
      if (!force && state.diff && !state.diffStale && state.diffSourceKey === key) return true;
      if (compareInFlight && compareInFlight.key === key) return compareInFlight.promise;
      const requestNo = ++compareSeq;
      const revealAutoResult = auto && showingResult;
      const payload = { left: leftText, right: rightText, left_label: leftLabel, right_label: rightLabel, ignore: { trim_space: !!options.trim_space, ignore_blank: !!options.ignore_blank, ignore_case: !!options.ignore_case } };
      compareBtn.disabled = true;
      status.textContent = '正在计算差异…';
      const request = (async function () {
        try {
          const response = await api('POST', '/api/diff/compare', payload);
          if (disposed || requestNo !== compareSeq) return false;
          const result = response || {};
          state.diff = result;
          state.diffSourceKey = key;
          state.diffStale = false;
          state.hunks = buildHunks(result.lines || []);
          state.hunkIndex = state.hunks.length ? 0 : -1;
          renderResult();
          if (auto && revealAutoResult) {
            // 自动刷新时不强制隐藏原文件编辑区，避免输入过程中光标丢失、无法继续输入
            showingResult = true;
            panel.classList.add('cmp-panel-has-result');
            resultHost.style.display = '';
            editBtn.style.display = 'none';
            compareBtn.style.display = '';
            refreshSaveButtons();
            const isHidden = editorGrid.style.display === 'none';
            const label = toggleEditorsBtn.querySelector('span');
            if (label) label.textContent = isHidden ? '展开原文件编辑' : '折叠原文件';
            else toggleEditorsBtn.textContent = isHidden ? '展开原文件编辑' : '折叠原文件';
            const svg = toggleEditorsBtn.querySelector('svg');
            if (svg) svg.innerHTML = '<path d="' + (isHidden ? 'M9 18l6-6-6-6' : 'M6 9l6 6 6-6') + '"/>';
            [prevBtn, nextBtn, prevLineBtn, nextLineBtn, copyBtn, downloadBtn].forEach(btn => btn.disabled = false);
          } else if (!auto) {
            showResult();
            [prevBtn, nextBtn, prevLineBtn, nextLineBtn, copyBtn, downloadBtn].forEach(btn => btn.disabled = false);
          }
          const stats = result.stats || {};
          const diffMsg = '共 ' + state.hunks.length + ' 处差异 (新增 ' + (Number(stats.added) || 0) + ' 行, 删除 ' + (Number(stats.removed) || 0) + ' 行)';
          diffCountBadge.textContent = state.hunks.length ? '1 / ' + state.hunks.length : '无差异';
          compareBtn.querySelector('span') ? compareBtn.querySelector('span').textContent = '重新比较' : compareBtn.textContent = '重新比较';
          status.textContent = diffMsg + ' · 可直接在下方编辑，点击箭头合并差异';
          return true;
        } catch (error) {
          if (disposed || requestNo !== compareSeq) return false;
          state.diffStale = true;
          toast('比对失败：' + (error.message || error), 'err');
          status.textContent = '比对失败，请重试';
          return false;
        } finally {
          if (requestNo === compareSeq && !disposed) compareBtn.disabled = false;
        }
      })();
      compareInFlight = { key, promise: request };
      request.then(function () {
        if (compareInFlight && compareInFlight.key === key && compareInFlight.promise === request) compareInFlight = null;
      }, function () {
        if (compareInFlight && compareInFlight.key === key && compareInFlight.promise === request) compareInFlight = null;
      });
      return request;
    }
    function showResult() { showingResult = true; panel.classList.add('cmp-panel-has-result'); editorGrid.style.display = 'none'; const label = toggleEditorsBtn.querySelector('span'); if (label) label.textContent = '展开原文件编辑'; else toggleEditorsBtn.textContent = '展开原文件编辑'; const svg = toggleEditorsBtn.querySelector('svg'); if (svg) svg.innerHTML = '<path d="M9 18l6-6-6-6"/>'; resultHost.style.display = ''; editBtn.style.display = 'none'; compareBtn.style.display = ''; refreshSaveButtons(); }
    function showEditors() { showingResult = false; panel.classList.remove('cmp-panel-has-result'); editorGrid.style.display = ''; resultHost.style.display = 'none'; editBtn.style.display = 'none'; compareBtn.style.display = ''; refreshSaveButtons(); }
    let currentVirtualDiff = null;
    function renderResult() {
      resultHost.innerHTML = ''; if (!state.diff) return;
      if (options.mode === 'unified' && (state.diff.lines || []).length <= MAX_INLINE_ROWS && window.Diff2Html) {
        const wrapper = el('div', { class: 'cmp-inline-unified' }); wrapper.innerHTML = window.Diff2Html.html(state.diff.unified_diff || '', { drawFileList: false, outputFormat: 'line-by-line', matching: 'lines', renderNothingWhenEmpty: false }); resultHost.appendChild(wrapper); return;
      }
      const effectiveMode = options.mode || 'side';
      const rows = buildAlignedRows(state.diff.lines || [], editorLeft.getValue(), editorRight.getValue(), state.hunks);
      const filtered = rows.filter(row => effectiveMode !== 'changes' || row.status !== 'equal');
      currentVirtualDiff = createVirtualDiff(filtered, state, effectiveMode, {
        hunk: applyHunk,
        line: applyLine,
        edit: commitLineEdit,
        language: { left: sourceHeaders.left.language.value, right: sourceHeaders.right.language.value },
        onHunkChange: function(idx, total) {
          diffCountBadge.textContent = total > 0 ? ('差异 ' + (idx + 1) + ' / ' + total) : '无差异';
        }
      });
      resultHost.appendChild(currentVirtualDiff.element);
      if (state.hunks && state.hunks.length) {
        currentVirtualDiff.jumpToHunk(Math.max(0, state.hunkIndex));
      }
    }
    function scheduleRecompare(delay, allowHidden) {
      if (disposed || !state.diff) return;
      // 未进入比对结果时，只有比较选项变化（allowHidden）才在后台刷新数据，
      // 避免用户编辑时被自动切到结果视图。
      if (!showingResult && !allowHidden) return;
      const large = autoCompareBlocked();
      if (large) {
        state.diffStale = true;
        if (showingResult) status.textContent = large + '，已暂停自动比较；请点击“重新比较”';
        return;
      }
      clearTimeout(recompareTimer);
      recompareTimer = setTimeout(() => { recompareTimer = 0; compareNow(true); }, delay == null ? 600 : Math.max(0, Number(delay) || 0));
    }
    function navigateHunk(delta) {
      if (currentVirtualDiff) currentVirtualDiff.navigateHunk(delta);
    }
    function navigateLine(delta) {
      if (currentVirtualDiff) currentVirtualDiff.navigateLine(delta);
    }
    function applyHunk(index, direction) {
      const hunk = state.hunks[index]; if (!hunk) return;
      const target = direction === 'right' ? editorRight : editorLeft, sourceLines = direction === 'right' ? hunk.leftText : hunk.rightText;
      const start = direction === 'right' ? hunk.rightStart : hunk.leftStart, count = direction === 'right' ? hunk.rightCount : hunk.leftCount;
      const lines = splitEditorLines(target.getValue()); lines.splice(start, count, ...sourceLines); target.setValue(lines.join('\n'));
      markDirty(direction === 'right' ? 'right' : 'left'); compareNow(true);
      toast('已' + (direction === 'right' ? '覆盖到右侧' : '覆盖到左侧'), 'ok');
    }
    function applyLine(row, direction) {
      if (!row) return;
      const targetSide = direction === 'right' ? 'right' : 'left';
      const sourceText = direction === 'right' ? row.leftText : row.rightText;
      const targetNo = direction === 'right' ? row.rightNo : row.leftNo;
      const otherNo = direction === 'right' ? row.leftNo : row.rightNo;
      const lines = splitEditorLines(editors[targetSide].getValue());
      if (row.status === 'changed') {
        if (targetNo > 0 && targetNo <= lines.length) lines[targetNo - 1] = sourceText;
      } else if (direction === 'right') {
        if (row.status === 'deleted') {
          const insertIdx = otherNo > 0 ? Math.min(otherNo - 1, lines.length) : lines.length;
          lines.splice(insertIdx, 0, sourceText);
        } else if (row.status === 'inserted') {
          if (targetNo > 0 && targetNo <= lines.length) lines.splice(targetNo - 1, 1);
        }
      } else {
        if (row.status === 'inserted') {
          const insertIdx = otherNo > 0 ? Math.min(otherNo - 1, lines.length) : lines.length;
          lines.splice(insertIdx, 0, sourceText);
        } else if (row.status === 'deleted') {
          if (targetNo > 0 && targetNo <= lines.length) lines.splice(targetNo - 1, 1);
        }
      }
      editors[targetSide].setValue(lines.join('\n'));
      markDirty(targetSide); compareNow(true);
      toast('已' + (direction === 'right' ? '覆盖本行到右侧' : '覆盖本行到左侧'), 'ok');
    }
    function commitLineEdit(side, lineNo, newText, row) {
      const current = editors[side].getValue();
      const otherNo = side === 'left' ? row.rightNo : row.leftNo;
      const next = replaceEditorLine(current, lineNo, newText, otherNo);
      if (next === current) return;
      editors[side].setValue(next);
      markDirty(side);
      scheduleRecompare();
    }
    function swapSides() {
      const leftText = editorLeft.getValue(); editorLeft.setValue(editorRight.getValue()); editorRight.setValue(leftText);
      const old = state.left; state.left = state.right; state.right = old; markDirty('left'); markDirty('right'); updateHeader('left'); updateHeader('right'); saveSources(state); if (state.diff) compareNow(true);
    }
    function copyDiff() {
      if (!state.diff) return;
      const text = state.diff.unified_diff || '';
      const p = (typeof copyToClipboard === 'function')
        ? copyToClipboard(text)
        : (navigator.clipboard && window.isSecureContext
          ? navigator.clipboard.writeText(text)
          : Promise.reject(new Error('no clipboard')));
      p.then(() => toast('Diff 已复制', 'ok'), () => {
        try {
          const ta = document.createElement('textarea');
          ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
          document.body.appendChild(ta); ta.select();
          if (document.execCommand('copy')) { toast('Diff 已复制', 'ok'); }
          else { toast('复制失败', 'err'); }
          document.body.removeChild(ta);
        } catch (_) { toast('复制失败', 'err'); }
      });
    }
    function downloadDiff() {
      if (!state.diff) return; const blob = new Blob([state.diff.unified_diff || ''], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob), a = el('a', { href: url, download: 'compare_' + Date.now() + '.diff' }); document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(url), 0);
    }
    state.textWorkbench = {
      loadPair: async (left, right) => {
        if (disposed) return false;
        state.left.source = Object.assign(defaultSource('left'), left || {});
        state.right.source = Object.assign(defaultSource('right'), right || {});
        updateHeader('left');
        updateHeader('right');
        const loaded = await Promise.all([loadSide('left'), loadSide('right')]);
        if (disposed || !loaded.every(Boolean)) return false;
        return compareNow(false, true);
      }
    };
    return function cleanupTextWorkbench() {
      if (disposed) return;
      disposed = true;
      clearTimeout(recompareTimer);
      recompareTimer = 0;
      compareSeq++;
      compareInFlight = null;
      editorLeft.dispose();
      editorRight.dispose();
      if (state.textWorkbench && state.textWorkbench.loadPair) delete state.textWorkbench.loadPair;
    };
  }

  function splitEditorLines(text) { return text === '' ? [] : String(text).replace(/\r\n/g, '\n').split('\n'); }
  function replaceEditorLine(value, lineNo, newText, otherNo) {
    const lines = splitEditorLines(value);
    const normalized = String(newText == null ? '' : newText).replace(/\r\n?/g, '\n');
    const replacement = normalized.split('\n');
    if (lineNo > 0) {
      lines.splice(Math.min(lineNo - 1, lines.length), 1, ...replacement);
    } else {
      if (!normalized) return value;
      const insertAt = Math.min(otherNo > 0 ? otherNo - 1 : lines.length, lines.length);
      lines.splice(insertAt, 0, ...replacement);
    }
    return lines.join('\n');
  }
  function buildHunks(lines) {
    const hunks = []; let current = null, lastLeft = 0, lastRight = 0;
    lines.forEach(line => {
      if (line.op === 'equal') { if (current) { hunks.push(current); current = null; } lastLeft = line.left_no; lastRight = line.right_no; return; }
      if (!current) current = { leftStart: lastLeft, rightStart: lastRight, leftCount: 0, rightCount: 0, leftText: [], rightText: [], rowIndex: 0 };
      if (line.op === 'delete') { current.leftCount++; current.leftText.push(line.text); lastLeft = line.left_no; }
      if (line.op === 'insert') { current.rightCount++; current.rightText.push(line.text); lastRight = line.right_no; }
    });
    if (current) hunks.push(current); return hunks;
  }
  function buildAlignedRows(lines, leftText, rightText, hunks) {
    const leftRaw = splitEditorLines(leftText), rightRaw = splitEditorLines(rightText), rows = []; let hunkIndex = -1;
    for (let i = 0; i < lines.length;) {
      const line = lines[i];
      if (line.op === 'equal') { rows.push({ status: 'equal', leftNo: line.left_no, rightNo: line.right_no, leftText: leftRaw[line.left_no - 1] || '', rightText: rightRaw[line.right_no - 1] || '', hunk: -1 }); i++; continue; }
      hunkIndex++; const deletes = [], inserts = [];
      while (i < lines.length && lines[i].op !== 'equal') { (lines[i].op === 'delete' ? deletes : inserts).push(lines[i]); i++; }
      const count = Math.max(deletes.length, inserts.length); if (hunks[hunkIndex]) hunks[hunkIndex].rowIndex = rows.length;
      for (let n = 0; n < count; n++) { const left = deletes[n], right = inserts[n]; rows.push({ status: left && right ? 'changed' : left ? 'deleted' : 'inserted', leftNo: left ? left.left_no : 0, rightNo: right ? right.right_no : 0, leftText: left ? left.text : '', rightText: right ? right.text : '', hunk: hunkIndex, first: n === 0 }); }
    }
    return rows;
  }

  function createVirtualDiff(rows, state, mode, actions) {
    const onMerge = actions && (typeof actions === 'function' ? actions : actions.hunk);
    const onLine = actions && actions.line;
    const onEdit = actions && actions.edit;
    const rowHeight = 25;

    // 准确映射各差异块在当前过滤视图下的起始行号与行数
    const hunkMap = {};
    rows.forEach((row, idx) => {
      row.displayIndex = idx;
      if (row.hunk >= 0) {
        if (!hunkMap[row.hunk]) {
          hunkMap[row.hunk] = { start: idx, count: 0 };
        }
        hunkMap[row.hunk].count++;
      }
    });
    const validHunkIndices = [];
    (state.hunks || []).forEach((hunk, idx) => {
      if (hunkMap[idx]) {
        hunk.displayIndex = hunkMap[idx].start;
        hunk.displayRowCount = hunkMap[idx].count;
        validHunkIndices.push(idx);
      } else {
        hunk.displayIndex = -1;
        hunk.displayRowCount = 0;
      }
    });

    let activeRowIndex = -1;
    const viewport = el('div', { class: 'cmp-vdiff', tabindex: '0', role: 'region', 'aria-label': '文本差异结果' });
    const canvas = el('div', { class: 'cmp-vdiff-canvas', role: 'list' });
    canvas.style.height = Math.max(1, rows.length * rowHeight) + 'px';
    viewport.appendChild(canvas);
    let renderedStart = -1, renderedEnd = -1;

    function renderWindow() {
      const start = Math.max(0, Math.floor(viewport.scrollTop / rowHeight) - 15);
      const end = Math.min(rows.length, start + Math.ceil((viewport.clientHeight || 600) / rowHeight) + 30);
      if (start === renderedStart && end === renderedEnd) return;
      renderedStart = start;
      renderedEnd = end;
      canvas.innerHTML = '';
      for (let i = start; i < end; i++) {
        const row = rows[i];
        if (mode === 'changes' && row.status === 'equal') continue;
        const isCurrentHunk = row.hunk >= 0 && row.hunk === state.hunkIndex;
        const isCurrentLine = i === activeRowIndex;
        const node = el('div', {
          class: 'cmp-vrow cmp-vrow-' + row.status + (isCurrentHunk ? ' is-active-hunk' : '') + (isCurrentLine ? ' is-active-line' : ''),
          role: 'listitem',
          'data-hunk': row.hunk >= 0 ? String(row.hunk) : '',
          'data-row': String(i),
          style: 'transform:translateY(' + (i * rowHeight) + 'px)'
        });
        node.addEventListener('click', function(e) {
          activeRowIndex = i;
          if (row.hunk >= 0) {
            state.hunkIndex = row.hunk;
            if (actions.onHunkChange) actions.onHunkChange(state.hunkIndex, state.hunks.length);
          }
          updateActiveHighlights();
        });
        const middle = el('div', { class: 'cmp-merge-cell' });
        if (row.hunk >= 0) {
          if (row.first) {
            middle.append(
              el('button', { class: 'cmp-merge-btn', title: '用左侧替换右侧', 'aria-label': '用左侧替换右侧', 'data-action': 'hunk-to-right', onclick: function (ev) { ev.stopPropagation(); onMerge && onMerge(row.hunk, 'right'); }, unsafeHtml: icon('right') }),
              el('button', { class: 'cmp-merge-btn', title: '用右侧替换左侧', 'aria-label': '用右侧替换左侧', 'data-action': 'hunk-to-left', onclick: function (ev) { ev.stopPropagation(); onMerge && onMerge(row.hunk, 'left'); }, unsafeHtml: icon('left') })
            );
          } else {
            middle.append(
              el('button', { class: 'cmp-merge-btn cmp-line-merge', title: '本行覆盖到右侧', 'aria-label': '本行覆盖到右侧', 'data-action': 'line-to-right', onclick: function (ev) { ev.stopPropagation(); onLine && onLine(row, 'right'); }, unsafeHtml: icon('lineRight') }),
              el('button', { class: 'cmp-merge-btn cmp-line-merge', title: '本行覆盖到左侧', 'aria-label': '本行覆盖到左侧', 'data-action': 'line-to-left', onclick: function (ev) { ev.stopPropagation(); onLine && onLine(row, 'left'); }, unsafeHtml: icon('lineLeft') })
            );
          }
        }
        node.append(
          makeDiffCell(row, 'left', onEdit, actions && actions.language && actions.language.left),
          middle,
          makeDiffCell(row, 'right', onEdit, actions && actions.language && actions.language.right)
        );
        canvas.appendChild(node);
      }
      updateMinimapThumb();
    }

    function updateActiveHighlights() {
      canvas.querySelectorAll('.cmp-vrow').forEach(node => {
        const hunkAttr = node.getAttribute('data-hunk');
        const rowAttr = node.getAttribute('data-row');
        const isHunkActive = hunkAttr !== '' && Number(hunkAttr) === state.hunkIndex;
        const isLineActive = rowAttr !== '' && Number(rowAttr) === activeRowIndex;
        node.classList.toggle('is-active-hunk', !!isHunkActive);
        node.classList.toggle('is-active-line', !!isLineActive);
      });
    }

    function jumpToHunk(index) {
      if (!state.hunks || !state.hunks.length) return;
      state.hunkIndex = (index + state.hunks.length) % state.hunks.length;
      const hunk = state.hunks[state.hunkIndex];
      if (hunk && hunk.displayIndex >= 0) {
        activeRowIndex = hunk.displayIndex;
        const targetScroll = (hunk.displayIndex * rowHeight) - Math.floor((viewport.clientHeight || 500) / 2) + Math.floor(rowHeight / 2);
        viewport.scrollTop = Math.max(0, targetScroll);
      }
      if (actions.onHunkChange) actions.onHunkChange(state.hunkIndex, state.hunks.length);
      renderedStart = -1;
      renderedEnd = -1;
      renderWindow();
      updateActiveHighlights();
    }

    function navigateHunk(delta) {
      if (!validHunkIndices.length) return;
      let curr = validHunkIndices.indexOf(state.hunkIndex);
      if (curr < 0) {
        curr = delta > 0 ? 0 : validHunkIndices.length - 1;
      } else {
        curr = (curr + delta + validHunkIndices.length) % validHunkIndices.length;
      }
      jumpToHunk(validHunkIndices[curr]);
    }

    function navigateLine(delta) {
      if (!rows.length) return;
      if (activeRowIndex < 0) activeRowIndex = delta > 0 ? 0 : rows.length - 1;
      else activeRowIndex = Math.max(0, Math.min(rows.length - 1, activeRowIndex + delta));
      const targetScroll = (activeRowIndex * rowHeight) - Math.floor((viewport.clientHeight || 500) / 2) + Math.floor(rowHeight / 2);
      viewport.scrollTop = Math.max(0, targetScroll);
      const currRow = rows[activeRowIndex];
      if (currRow && currRow.hunk >= 0) {
        state.hunkIndex = currRow.hunk;
        if (actions.onHunkChange) actions.onHunkChange(state.hunkIndex, state.hunks.length);
      }
      renderedStart = -1;
      renderedEnd = -1;
      renderWindow();
      updateActiveHighlights();
    }

    const container = el('div', { class: 'cmp-vdiff-container' });
    const minimap = el('div', { class: 'cmp-vdiff-minimap', title: '点击跳转定位差异' });
    const minimapThumb = el('div', { class: 'cmp-minimap-thumb' });
    minimap.appendChild(minimapThumb);
    const totalRows = Math.max(1, rows.length);

    (state.hunks || []).forEach(function(hunk, idx) {
      if (hunk.displayIndex < 0) return;
      const top = (hunk.displayIndex / totalRows) * 100;
      const h = Math.max(3, ((hunk.displayRowCount || 1) / totalRows) * 100);
      const marker = el('div', {
        class: 'cmp-minimap-marker',
        style: 'top:' + top + '%;height:' + h + '%;',
        title: '差异 #' + (idx + 1) + ' (第 ' + (hunk.displayIndex + 1) + ' 行)',
        'data-hunk': String(idx)
      });
      minimap.appendChild(marker);
    });

    function updateMinimapThumb() {
      const scrollH = Math.max(1, viewport.scrollHeight);
      const clientH = viewport.clientHeight || 1;
      const topPct = (viewport.scrollTop / scrollH) * 100;
      const heightPct = Math.max(4, (clientH / scrollH) * 100);
      minimapThumb.style.top = topPct + '%';
      minimapThumb.style.height = heightPct + '%';
    }

    minimap.addEventListener('click', function(e) {
      const marker = e.target.closest('.cmp-minimap-marker');
      if (marker && marker.dataset.hunk) {
        const hunkIdx = Number(marker.dataset.hunk);
        jumpToHunk(hunkIdx);
        return;
      }
      const rect = minimap.getBoundingClientRect();
      const ratio = Math.max(0, Math.min(1, (e.clientY - rect.top) / rect.height));
      const targetRow = Math.floor(ratio * totalRows);
      const targetScroll = (targetRow * rowHeight) - Math.floor((viewport.clientHeight || 500) / 2) + Math.floor(rowHeight / 2);
      viewport.scrollTop = Math.max(0, Math.min(viewport.scrollHeight - viewport.clientHeight, targetScroll));
    });

    viewport.addEventListener('scroll', () => {
      renderWindow();
      updateMinimapThumb();
    });
    requestAnimationFrame(() => {
      renderWindow();
      updateMinimapThumb();
    });
    container.append(viewport, minimap);
    return { element: container, jumpToHunk, navigateHunk, navigateLine };
  }

  function makeDiffCell(row, side, onEdit, language) {
    const lineNo = side === 'left' ? row.leftNo : row.rightNo;
    const text = side === 'left' ? row.leftText : row.rightText;
    const other = side === 'left' ? row.rightText : row.leftText;
    const code = el('span', { class: 'cmp-code', spellcheck: 'false', tabindex: '0', 'aria-readonly': 'true', 'aria-multiline': 'true', title: '按 Enter 或双击编辑；Enter 换行，Ctrl/⌘ + Enter 完成，Esc 取消' });
    const syntax = Kairo.workbench && Kairo.workbench.syntaxEditor;
    if (syntax && language && row.status !== 'changed') code.innerHTML = syntax.highlight(text || '', language);
    else appendWordDiff(code, text || '', other || '', row.status === 'changed');

    code.setAttribute('role', 'textbox');
    code.setAttribute('aria-label', (side === 'left' ? '左侧第' : '右侧第') + (lineNo || '新') + '行');
    code.dataset.side = side;
    code.dataset.line = String(lineNo || 0);
    let original = text || '';
    let skip = false, editing = false;

    function enableEdit() {
      if (editing) return;
      editing = true;
      original = text || '';
      code.textContent = original;
      try { code.contentEditable = 'plaintext-only'; } catch (_) { code.contentEditable = 'true'; }
      code.setAttribute('aria-readonly', 'false');
      code.focus();
      const sel = window.getSelection();
      if (sel) sel.selectAllChildren(code);
    }
    function finishEdit() {
      if (!editing) return;
      editing = false;
      code.contentEditable = 'false';
      code.setAttribute('aria-readonly', 'true');
      const raw = typeof code.innerText === 'string' ? code.innerText : code.textContent;
      const next = String(raw || '').replace(/\r\n?/g, '\n');
      if (!skip && onEdit && next !== original) {
        original = next;
        onEdit(side, lineNo, next, row);
      }
      skip = false;
    }

    code.addEventListener('dblclick', function (e) {
      e.stopPropagation();
      enableEdit();
    });
    code.addEventListener('keydown', function (e) {
      if (!editing) {
        if (e.key === 'Enter' || e.key === 'F2') {
          e.preventDefault();
          enableEdit();
        }
        return;
      }
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        e.stopPropagation();
        finishEdit();
        code.blur();
      } else if (e.key === 'Enter') {
        // 普通回车保留给编辑器插入换行，不再把它当成“完成编辑”。
        e.stopPropagation();
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        skip = true;
        code.textContent = original;
        editing = false;
        code.contentEditable = 'false';
        code.setAttribute('aria-readonly', 'true');
        code.blur();
      }
    });
    code.addEventListener('paste', function (e) {
      if (!code.isContentEditable) return;
      e.preventDefault();
      const pasted = String((e.clipboardData && e.clipboardData.getData('text/plain')) || '').replace(/\r\n?/g, '\n');
      document.execCommand('insertText', false, pasted);
    });
    code.addEventListener('blur', function () {
      finishEdit();
    });

    return el('div', { class: 'cmp-diff-cell cmp-diff-' + side }, [
      el('span', { class: 'cmp-line-no', text: lineNo ? String(lineNo) : '' }),
      el('span', { class: 'cmp-line-marker', text: !lineNo ? '' : row.status === 'equal' ? ' ' : side === 'left' ? '−' : '+' }),
      code
    ]);
  }
  function appendWordDiff(parent, text, other, enabled) {
    if (!enabled || !text || !other) { parent.textContent = text; return; }
    let prefix = 0; while (prefix < text.length && prefix < other.length && text[prefix] === other[prefix]) prefix++;
    let suffix = 0; while (suffix < text.length - prefix && suffix < other.length - prefix && text[text.length - 1 - suffix] === other[other.length - 1 - suffix]) suffix++;
    if (prefix) parent.appendChild(el('span', { text: text.slice(0, prefix) }));
    parent.appendChild(el('mark', { class: 'cmp-word-change', text: text.slice(prefix, text.length - suffix) }));
    if (suffix) parent.appendChild(el('span', { text: text.slice(text.length - suffix) }));
  }

  function rememberFolderPath(side, path) {
    const next = normalizeFolderHistoryEntry(path);
    if (!next || !String(next.path || '').trim()) return;
    const list = (persisted.folder_history[side] || []).map(normalizeFolderHistoryEntry).filter(function (item) {
      return item && !(item.kind === next.kind && item.system === next.system && item.server === next.server && item.host === next.host && Number(item.port || 0) === Number(next.port || 0) && item.path === next.path);
    });
    list.unshift(next);
    persisted.folder_history[side] = list.slice(0, PATH_HISTORY_LIMIT);
    persistPreference(persisted);
  }
  function fillHistoryList(listEl, side) {
    listEl.innerHTML = '';
    (persisted.folder_history[side] || []).map(normalizeFolderHistoryEntry).filter(Boolean).forEach(function (entry, index) {
      const option = el('option', { value: entry.path || '', text: sourceHistoryLabel(entry) });
      option.setAttribute('data-history-index', String(index));
      listEl.appendChild(option);
    });
  }

  function sourceHistoryLabel(entry) {
    entry = normalizeFolderHistoryEntry(entry) || sourceIdentity({ kind: 'local' });
    const endpoint = entry.kind === 'sftp'
      ? [entry.system, entry.server].filter(Boolean).join(' / ') || entry.host || 'SFTP'
      : entry.kind === 'ftp'
        ? (entry.host || 'FTP') + (entry.port ? ':' + entry.port : '')
        : '本地';
    return endpoint + ' · ' + (entry.path || '未设置路径');
  }

  function parentRel(rel) { const i = String(rel || '').lastIndexOf('/'); return i < 0 ? '' : rel.slice(0, i); }
  function baseName(rel) { const i = String(rel || '').lastIndexOf('/'); return i < 0 ? rel : rel.slice(i + 1); }
  function isDirItem(item) { return !!(item && ((item.left && item.left.is_dir) || (item.right && item.right.is_dir))); }
  function isTypeConflict(item) {
    return !!(item && (item.status === 'type_conflict' || item.status === 'type-conflict' || item.type_conflict || (item.left && item.right && !!item.left.is_dir !== !!item.right.is_dir)));
  }
  function isBulkActionable(item) {
    if (!item || item.status === 'same' || item.status === 'pending' || item.pending || item.status === 'error' || item.status === 'errors' || item.status === 'type_conflict' || item.status === 'type-conflict') return false;
    if (isTypeConflict(item)) return false;
    return !!(item.left || item.right);
  }

  // Only auto-open directories whose direct children are already present in
  // the scan result. Boundary directories stay collapsed so the first click
  // can trigger the lazy scan instead of merely closing a phantom expansion.
  function autoExpandDiffFolderRows(items, expanded, limit) {
    const children = new Set();
    (items || []).forEach(function (item) {
      if (item && item.rel_path) children.add(parentRel(item.rel_path));
    });
    let count = 0;
    (items || []).forEach(function (item) {
      if (count >= (limit || 300)) return;
      if (item && isDirItem(item) && item.status && item.status !== 'same' && children.has(item.rel_path)) {
        expanded.add(item.rel_path);
        count++;
      }
    });
  }

  function rollupAllFolders(items, loadedDirs) {
    if (!items || !items.length) return;
    const itemMap = new Map();
    const childrenMap = new Map();
    const dirRels = new Set();

    items.forEach(function (item) {
      itemMap.set(item.rel_path, item);
      const parent = parentRel(item.rel_path);
      if (!childrenMap.has(parent)) childrenMap.set(parent, []);
      childrenMap.get(parent).push(item);
      if (isDirItem(item)) {
        dirRels.add(item.rel_path);
      }
    });

    const sortedDirs = Array.from(dirRels).sort(function (a, b) {
      const depthA = (a.match(/\//g) || []).length;
      const depthB = (b.match(/\//g) || []).length;
      if (depthA !== depthB) return depthB - depthA;
      return b.length - a.length;
    });

    sortedDirs.forEach(function (dirRel) {
      const folder = itemMap.get(dirRel);
      if (!folder) return;
      if (folder.status === 'left_only' || folder.status === 'right_only') {
        folder.pending = false;
        return;
      }
      const kids = childrenMap.get(dirRel) || [];
      if (!kids.length) {
        // A directory at the scan boundary has not been verified yet. Never
        // collapse it to "same": that would hide a deeper difference from the
        // default difference filter and make bulk sync unsafe.
        if (loadedDirs && !loadedDirs.has(dirRel)) {
          folder.status = 'pending';
          folder.pending = true;
        }
        return;
      }
      const hasDiff = kids.some(function (k) { return k.status && k.status !== 'same' && k.status !== 'pending'; });
      const hasPending = kids.some(function (k) { return k.pending || k.status === 'pending'; });
      if (hasDiff) {
        folder.status = 'different';
        folder.pending = false;
      } else if (hasPending) {
        folder.status = 'pending';
        folder.pending = true;
      } else {
        folder.status = 'same';
        folder.pending = false;
      }
    });
  }

  function buildFolderWorkbench(panel, state, options, switchTab, handoff) {
    const sources = {
      left: Object.assign({ kind: 'local', path: '', label: '左侧文件夹' }, state.left.source && state.left.source.kind !== 'text' ? state.left.source : {}),
      right: Object.assign({ kind: 'local', path: '', label: '右侧文件夹' }, state.right.source && state.right.source.kind !== 'text' ? state.right.source : {})
    };
    ['left', 'right'].forEach(function (side) {
      const history = (persisted.folder_history[side] || []).map(normalizeFolderHistoryEntry).filter(Boolean);
      if (history.length) Object.assign(sources[side], history[0]);
    });
    if (handoff && handoff.left && handoff.right) {
      // Handoff paths win over saved folder history for this in-memory render.
      // They are not written until an explicit user scan (or not at all when
      // the automatic receipt scan below uses persist:false).
      sources.left = Object.assign({}, sources.left, handoff.left, { label: '投产历史（上一条）' });
      sources.right = Object.assign({}, sources.right, handoff.right, { label: '投产历史（当前）' });
    }
    const selected = new Set();
    const expanded = new Set();
    const loadedDirs = new Set(['']);
    const loadingDirs = new Set();
    let currentFolderTree = null;
    let activeFolderRel = '';
    let scanSequence = 0;
    let folderIndex = null;
    let filterRenderTimer = 0;

    const resultHost = el('div', { class: 'cmp-folder-results' });
    const scanAlert = el('div', { class: 'cmp-scan-alert', role: 'status', 'aria-live': 'polite', hidden: true });
    const progress = el('div', { class: 'cmp-scan-progress', role: 'status', 'aria-live': 'polite', text: '填入或选择两侧目录后开始比对；支持多层自动递归与展开内容比较' });
    const progressBar = el('div', { class: 'cmp-progress-track' }, [el('div', { class: 'cmp-progress-bar' })]);
    progressBar.setAttribute('role', 'progressbar');
    progressBar.setAttribute('aria-label', '比较任务进度');
    progressBar.setAttribute('aria-valuemin', '0');
    progressBar.setAttribute('aria-valuemax', '100');
    const cancelBtn = makeButton('取消扫描', 'close', cancelScan, 'btn btn-sm');
    cancelBtn.disabled = true;
    cancelBtn.hidden = true;

    const deep = el('select', { title: '内容比较只在当前展开的这一层执行' }, [
      el('option', { value: 'deep', text: '按需内容比较（展开目录时再比内容）' }),
      el('option', { value: 'fast', text: '极速元数据（只比大小/时间）' })
    ]);
    deep.value = persisted.options.deep === 'fast' ? 'fast' : 'deep';
    deep.addEventListener('change', function () {
      persisted.options.deep = deep.value;
      persistPreference(persisted);
    });

    const depth = el('select', { id: 'cmp-scan-depth', title: '限制递归深度，避免大目录扫很久' }, [
      el('option', { value: '1', text: '仅当前目录' }),
      el('option', { value: '3', text: '最多 3 层' }),
      el('option', { value: '0', text: '不限深度' })
    ]);
    const savedDepth = Number(persisted.options.max_depth);
    depth.value = savedDepth === 0 || savedDepth > 0 ? String(savedDepth) : '1';
    depth.addEventListener('change', function () {
      persisted.options.max_depth = Number(depth.value);
      persistPreference(persisted);
    });

    const tolerance = el('select', {}, [
      el('option', { value: '2', text: '时间容差 2 秒' }),
      el('option', { value: '60', text: '时间容差 1 分钟' }),
      el('option', { value: '3600', text: '时间容差 1 小时' })
    ]);
    const statusFilter = el('select', { 'aria-label': '按状态筛选' }, [
      el('option', { value: 'all', text: '全部状态' }),
      el('option', { value: 'different', text: '所有差异' }),
      el('option', { value: 'left_newer', text: '左侧较新' }),
      el('option', { value: 'right_newer', text: '右侧较新' }),
      el('option', { value: 'orphans', text: '仅单侧存在' }),
      el('option', { value: 'pending', text: '待验证/未展开' }),
      el('option', { value: 'blocked', text: '错误/类型冲突' })
    ]);
    statusFilter.value = 'different';
    const ignoreExt = el('input', { type: 'text', placeholder: '忽略扩展名：log,tmp,bak' });
    const search = el('input', { type: 'search', 'aria-label': '按文件名或路径筛选比较结果', placeholder: '筛选路径…' });
    const sourceGrid = el('div', { class: 'cmp-folder-source-grid cmp2-folder-sources' });
    const pathInputs = {};
    const sourceCardUpdates = {};

    ['left', 'right'].forEach(side => {
      const historySelect = el('select', { class: 'cmp-folder-history-select', title: '选择已保存的数据源身份' });
      const sourceMeta = el('span', { class: 'cmp2-source-identity', role: 'status' });
      const sourceCard = el('section', { class: 'cmp-folder-source cmp2-folder-source' });
      const fillSelect = function () {
        historySelect.innerHTML = '';
        historySelect.appendChild(el('option', { value: '', text: '选择历史来源…' }));
        (persisted.folder_history[side] || []).map(normalizeFolderHistoryEntry).filter(Boolean).forEach(function (entry, index) {
          const option = el('option', { value: entry.path || '', text: sourceHistoryLabel(entry) });
          option.setAttribute('data-history-index', String(index));
          historySelect.appendChild(option);
        });
      };
      fillSelect();
      historySelect.addEventListener('change', function () {
        if (!this.value) return;
        const option = this.options && this.options[this.selectedIndex];
        const index = option ? Number(option.getAttribute('data-history-index')) : -1;
        const history = (persisted.folder_history[side] || []).map(normalizeFolderHistoryEntry).filter(Boolean);
        const next = index >= 0 && history[index] ? history[index] : sourceIdentity({ kind: 'local', path: this.value });
        Object.assign(sources[side], next);
        pathInput.value = sources[side].path || '';
        rememberFolderPath(side, sources[side]);
        updateSourceCard();
        this.value = '';
      });
      const pathInput = el('input', {
        type: 'text',
        class: 'cmp-folder-path',
        value: sources[side].path || '',
        'aria-label': side === 'left' ? '左侧目录路径' : '右侧目录路径',
        placeholder: side === 'left' ? '左侧目录绝对路径，可粘贴' : '右侧目录绝对路径，可粘贴'
      });
      pathInputs[side] = pathInput;
      pathInput.addEventListener('change', function () {
        sources[side].kind = sources[side].kind === 'text' ? 'local' : (sources[side].kind || 'local');
        sources[side].path = pathInput.value.trim();
        rememberFolderPath(side, sources[side]);
        fillSelect();
        updateSourceCard();
      });
      const browse = browseButton({
        input: pathInput,
        directory: true,
        compact: true,
        onPick: function (path) {
          sources[side].kind = 'local';
          sources[side].path = path;
          rememberFolderPath(side, sources[side]);
          pathInput.value = path;
          fillSelect();
          updateSourceCard();
        }
      });
      const remote = makeButton('远程…', '', function () {
        openSourceDialog(sources[side], true, function (source) {
          // Keep the credential only in live memory for this scan. The history
          // writer below stores a sanitized identity and never persists it.
          sources[side] = Object.assign({}, source);
          pathInput.value = source.path || '';
          rememberFolderPath(side, sources[side]);
          fillSelect();
          updateSourceCard();
        });
      });
      remote.setAttribute('aria-label', (side === 'left' ? '配置左侧' : '配置右侧') + '远程数据源');
      const titleRow = el('div', { class: 'cmp2-folder-source-title' }, [
        el('span', { class: 'cmp-source-badge', text: side === 'left' ? '左' : '右' }),
        el('strong', { text: side === 'left' ? '左侧来源' : '右侧来源' }),
        sourceMeta
      ]);
      sourceCard.append(
        titleRow,
        el('div', { class: 'cmp2-folder-path-row' }, [pathInput, browse]),
        el('div', { class: 'cmp2-folder-source-foot' }, [historySelect, remote])
      );
      sourceGrid.appendChild(sourceCard);
      function updateSourceCard() {
        const source = sources[side];
        sourceMeta.textContent = sourceIdentityLabel(source);
        sourceMeta.title = sourceIdentityLabel(source);
        sourceMeta.setAttribute('data-kind', source.kind || 'local');
        sourceMeta.className = 'cmp2-source-identity cmp2-source-' + (source.kind || 'local');
        remote.querySelector('span') && (remote.querySelector('span').textContent = source.kind === 'local' ? '配置远程…' : '编辑来源…');
        pathInput.value = source.path || '';
      }
      updateSourceCard();
      sourceCardUpdates[side] = updateSourceCard;
    });

    const testBtn = makeButton('测试来源', 'check', testSources, 'btn btn-sm');
    testBtn.setAttribute('data-action', 'compare-test');
    const testStatus = el('span', { class: 'cmp2-test-status', text: '' });
    const startBtn = makeButton('开始比较', 'compare', startScan, 'btn btn-primary btn-sm');
    startBtn.setAttribute('data-action', 'compare-scan');

    const prevFolderDiffBtn = makeButton('上一处', 'arrowUp', () => navigateFolderDiff(-1), 'btn btn-sm');
    const nextFolderDiffBtn = makeButton('下一处', 'arrowDown', () => navigateFolderDiff(1), 'btn btn-sm');
    const folderDiffBadge = el('span', { class: 'cmp-diff-count-badge', text: '未比较' });
    prevFolderDiffBtn.disabled = true;
    nextFolderDiffBtn.disabled = true;

    const coverRightBtn = makeButton('覆盖到右侧', 'right', () => coverDirection('right'), 'btn btn-sm');
    coverRightBtn.setAttribute('data-action', 'cover-right');
    const coverLeftBtn = makeButton('覆盖到左侧', 'left', () => coverDirection('left'), 'btn btn-sm');
    coverLeftBtn.setAttribute('data-action', 'cover-left');
    const selectDiffBtn = makeButton('全选当前差异', 'check', selectVisible, 'btn btn-sm');
    const clearSelectBtn = makeButton('清空选择', 'close', clearSelection, 'btn btn-sm');

    search.placeholder = '输入文件名或路径过滤…';
    search.classList.add('cmp-folder-search');
    ignoreExt.classList.add('cmp-ignore-ext');
    deep.classList.add('cmp-toolbar-select');
    depth.classList.add('cmp-toolbar-select');
    tolerance.classList.add('cmp-toolbar-select');
    statusFilter.classList.add('cmp-toolbar-select');

    const scanSettings = el('details', { class: 'cmp2-popover' }, [
      el('summary', { text: '扫描设置' }),
      el('div', { class: 'cmp2-popover-panel cmp2-settings-grid' }, [
        el('label', {}, [el('span', { text: '比较方式' }), deep]),
        el('label', {}, [el('span', { text: '递归深度' }), depth]),
        el('label', {}, [el('span', { text: '时间容差' }), tolerance]),
        el('label', { class: 'cmp2-settings-wide' }, [el('span', { text: '忽略扩展名' }), ignoreExt])
      ])
    ]);
    const scanBar = el('div', { class: 'cmp2-commandbar cmp2-folder-commandbar' }, [
      el('div', { class: 'cmp2-commandbar-primary' }, [startBtn, testBtn, cancelBtn, testStatus]),
      el('div', { class: 'cmp2-commandbar-secondary' }, [scanSettings])
    ]);
    const navGroup = el('div', { class: 'cmp2-segment cmp2-nav', 'aria-label': '文件夹差异导航' }, [prevFolderDiffBtn, folderDiffBadge, nextFolderDiffBtn]);
    const resultCount = el('span', { class: 'cmp2-result-count', role: 'status', 'aria-live': 'polite', text: '' });
    const resultsBar = el('div', { class: 'cmp2-resultsbar' }, [
      el('div', { class: 'cmp2-results-filters' }, [statusFilter, el('div', { class: 'cmp2-search-wrap' }, [search]), resultCount]),
      navGroup
    ]);
    resultsBar.hidden = true;
    const selectionCount = el('strong', { class: 'cmp2-selection-count', text: '已选择 0 项' });
    const selectionBar = el('div', { class: 'cmp2-selectionbar', hidden: true }, [
      selectionCount,
      el('span', { class: 'cmp2-selection-hint', text: '覆盖前会显示完整预览，不会删除目标侧多余文件。' }),
      el('div', { class: 'cmp2-selection-actions' }, [coverLeftBtn, coverRightBtn, clearSelectBtn])
    ]);

    panel.append(sourceGrid, scanBar, progressBar, progress, scanAlert, resultsBar, selectionBar, resultHost);
    search.addEventListener('input', queueRenderScan);
    statusFilter.addEventListener('change', renderScan);

    function queueRenderScan() {
      clearTimeout(filterRenderTimer);
      filterRenderTimer = setTimeout(renderScan, 150);
    }

    function invalidateFolderIndex() { folderIndex = null; }

    function getFolderIndex() {
      const items = state.scan && state.scan.items || [];
      if (folderIndex && folderIndex.items === items) return folderIndex;
      const kids = { '': [] };
      items.forEach(function (item) {
        const parent = parentRel(item.rel_path);
        (kids[parent] || (kids[parent] = [])).push(item);
      });
      Object.keys(kids).forEach(function (key) {
        kids[key].sort(function (a, b) {
          const da = isDirItem(a), db = isDirItem(b);
          if (da !== db) return da ? -1 : 1;
          return String(a.rel_path).localeCompare(String(b.rel_path), 'zh');
        });
      });
      folderIndex = { items: items, kids: kids };
      return folderIndex;
    }

    function scanIntegrity(scan) {
      scan = scan || {};
      const reasons = [];
      if (scan.error) reasons.push('扫描失败：' + scan.error);
      if (scan.truncated) reasons.push('结果已截断，目录条目未完整加载');
      if (scan.incomplete) reasons.push('结果未完成，仍有内容未验证');
      const items = scan.items || [];
      const pending = items.filter(function (item) { return item.pending || item.status === 'pending'; }).length;
      const errors = items.filter(function (item) { return item.status === 'error' || item.status === 'errors'; }).length;
      const backendErrors = scan.summary && Number(scan.summary.errors || scan.summary.error_count || 0) || 0;
      if (pending) reasons.push('有 ' + pending + ' 个目录尚未展开验证');
      if (errors) reasons.push('有 ' + errors + ' 个项目扫描/比较失败');
      if (backendErrors > errors) reasons.push('后端报告 ' + backendErrors + ' 个项目扫描/比较失败');
      return { unsafe: reasons.length > 0, reasons: reasons, pending: pending, errors: Math.max(errors, backendErrors) };
    }

    function updateScanIntegrity() {
      const integrity = scanIntegrity(state.scan);
      scanAlert.innerHTML = '';
      if (integrity.unsafe) {
        scanAlert.setAttribute('role', 'alert');
        scanAlert.setAttribute('aria-live', 'assertive');
        scanAlert.appendChild(el('span', { class: 'cmp-alert-icon', unsafeHtml: icon('warning') }));
        scanAlert.appendChild(el('span', { text: integrity.reasons.join('；') + '。批量覆盖已禁用，请先重新扫描或展开待验证目录。' }));
        scanAlert.hidden = false;
        scanAlert.className = 'cmp-scan-alert is-blocking';
      } else {
        scanAlert.setAttribute('role', 'status');
        scanAlert.setAttribute('aria-live', 'polite');
        scanAlert.hidden = true;
        scanAlert.className = 'cmp-scan-alert';
      }
      const progress = state.scan && state.scan.progress;
      if (progress && typeof progress.percent === 'number') {
        progressBar.setAttribute('aria-valuenow', String(Math.max(0, Math.min(100, progress.percent))));
      }
      return integrity;
    }

    async function testSources() {
      sources.left.path = (pathInputs.left.value || '').trim();
      sources.right.path = (pathInputs.right.value || '').trim();
      sourceCardUpdates.left(); sourceCardUpdates.right();
      if (!sources.left.path || !sources.right.path) {
        toast('请先填入或选择左右两个目录', 'warn');
        return;
      }
      testBtn.disabled = true;
      testStatus.textContent = '正在连接…';
      testStatus.className = 'cmp2-test-status';
      try {
        const response = await api('POST', '/api/compare/test', { left: spec(sources.left), right: spec(sources.right) });
        const leftOK = response.left && response.left.ok && response.left.is_dir;
        const rightOK = response.right && response.right.ok && response.right.is_dir;
        if (leftOK && rightOK) {
          testStatus.textContent = '两侧来源可用';
          testStatus.className = 'cmp2-test-status is-ok';
          toast('来源测试通过', 'ok');
        } else {
          const failures = [];
          if (!leftOK) failures.push('左侧：' + ((response.left && response.left.error) || '不是目录'));
          if (!rightOK) failures.push('右侧：' + ((response.right && response.right.error) || '不是目录'));
          testStatus.textContent = '来源不可用';
          testStatus.className = 'cmp2-test-status is-error';
          toast(failures.join('；'), 'err');
        }
      } catch (error) {
        testStatus.textContent = '测试失败';
        testStatus.className = 'cmp2-test-status is-error';
        toast('来源测试失败：' + (error.message || error), 'err');
      } finally {
        testBtn.disabled = false;
      }
    }

    async function startScan(scanOptions) {
      const persistScan = !scanOptions || scanOptions.persist !== false;
      scanSettings.open = false;
      sources.left.path = (pathInputs.left.value || '').trim();
      sources.right.path = (pathInputs.right.value || '').trim();
      sourceCardUpdates.left(); sourceCardUpdates.right();
      if (!sources.left.path || !sources.right.path) {
        toast('请先填入或选择左右两个目录', 'warn');
        return;
      }
      if (persistScan) {
        rememberFolderPath('left', sources.left);
        rememberFolderPath('right', sources.right);
        persisted.sources.left = sourceIdentity(sources.left);
        persisted.sources.right = sourceIdentity(sources.right);
        persistPreference(persisted);
      }

      if (scanPollTimer) { clearTimeout(scanPollTimer); scanPollTimer = 0; }
      if (activeScanJob) {
        try { api('DELETE', '/api/compare/jobs/' + activeScanJob); } catch (_) {}
        activeScanJob = '';
      }
      const currentSeq = ++scanSequence;

      selected.clear();
      expanded.clear();
      loadedDirs.clear();
      loadedDirs.add('');
      loadingDirs.clear();
      activeFolderRel = '';

      startBtn.disabled = true;
      cancelBtn.disabled = false;
      cancelBtn.hidden = false;
      resultsBar.hidden = true;
      resultHost.innerHTML = '<div class="cmp-folder-loading card muted">正在扫描比较目录结构与文件…</div>';
      scanAlert.hidden = true;
      scanAlert.textContent = '';
      progress.textContent = '正在比较所选目录（不会扫描上级）…';
      progressBar.firstChild.style.width = '12%';
      progressBar.setAttribute('aria-valuenow', '12');

      const maxDepth = Number(depth.value) || 0;
      if (persistScan) {
        persisted.options.max_depth = maxDepth;
        persistPreference(persisted);
      }

      try {
        const response = await api('POST', '/api/compare/scan', {
          left: spec(sources.left),
          right: spec(sources.right),
          deep: deep.value === 'deep',
          max_depth: maxDepth,
          time_tolerance_seconds: Number(tolerance.value) || 2,
          ignore_exts: ignoreExt.value.split(/[,，\s]+/).filter(Boolean)
        });
        if (currentSeq !== scanSequence) return;
        activeScanJob = response.job_id;
        pollScan(currentSeq);
      } catch (error) {
        if (currentSeq !== scanSequence) return;
        startBtn.disabled = false;
        cancelBtn.disabled = true;
        cancelBtn.hidden = true;
        resultHost.innerHTML = '';
        toast('启动失败：' + (error.message || error), 'err');
      }
    }

    async function pollScan(seq) {
      if (seq !== scanSequence || !activeScanJob) return;
      try {
        const job = await api('GET', '/api/compare/jobs/' + activeScanJob);
        if (seq !== scanSequence) return;
        const percent = job.total ? Math.min(100, Math.round(job.current / job.total * 100)) : 16;
        progressBar.firstChild.style.width = percent + '%';
        progressBar.setAttribute('aria-valuenow', String(percent));
        progress.textContent = (job.message || job.phase) + (job.total ? ' · ' + job.current + ' / ' + job.total : '');

        if (job.status === 'completed') {
          state.scan = job.result || { items: [], summary: {}, incomplete: true, error: '服务端未返回比较结果' };
          activeScanJob = '';
          startBtn.disabled = false;
          cancelBtn.disabled = true;
          cancelBtn.hidden = true;
          progressBar.firstChild.style.width = '100%';
          progressBar.setAttribute('aria-valuenow', '100');
          invalidateFolderIndex();
          rollupAllFolders(state.scan.items, loadedDirs);
          recalculateSummary();
          autoExpandDiffFolders();
          renderScan();
          return;
        }
        if (job.status === 'failed' || job.status === 'cancelled') {
          throw new Error(job.error || '任务已取消');
        }
        scanPollTimer = setTimeout(() => pollScan(seq), 350);
      } catch (error) {
        if (seq !== scanSequence) return;
        activeScanJob = '';
        startBtn.disabled = false;
        cancelBtn.disabled = true;
        cancelBtn.hidden = true;
          progress.textContent = '比较未完成';
          scanAlert.setAttribute('role', 'alert');
          scanAlert.setAttribute('aria-live', 'assertive');
          scanAlert.textContent = error.message || String(error);
          scanAlert.className = 'cmp-scan-alert is-blocking';
          scanAlert.hidden = false;
          toast(error.message || String(error), 'err');
      }
    }

    async function cancelScan() {
      scanSequence++;
      if (scanPollTimer) { clearTimeout(scanPollTimer); scanPollTimer = 0; }
      if (activeScanJob) {
        try { await api('DELETE', '/api/compare/jobs/' + activeScanJob); } catch (_) {}
        activeScanJob = '';
      }
      cancelBtn.disabled = true;
      cancelBtn.hidden = true;
      startBtn.disabled = false;
      progress.textContent = '已取消';
      progressBar.firstChild.style.width = '0%';
    }

    function mergeScanItems(newItems) {
      const byRel = {};
      (state.scan.items || []).forEach(function (it) { byRel[it.rel_path] = it; });
      (newItems || []).forEach(function (it) { byRel[it.rel_path] = it; });
      state.scan.items = Object.keys(byRel).sort().map(function (key) { return byRel[key]; });
      // The merged array is the cache key for the structural index.
      invalidateFolderIndex();
      recalculateSummary();
    }

    function recalculateSummary() {
      if (!state.scan || !state.scan.items) return;
      const summary = { same: 0, different: 0, suspect: 0, left_newer: 0, right_newer: 0, left_only: 0, right_only: 0, errors: 0 };
      state.scan.items.forEach(function (item) {
        if (isDirItem(item)) return;
        if (summary[item.status] !== undefined) summary[item.status]++;
      });
      state.scan.summary = summary;
    }

    function autoExpandDiffFolders() {
      autoExpandDiffFolderRows(state.scan.items || [], expanded, 300);
    }

    async function toggleFolder(item) {
      const rel = item.rel_path;
      if (expanded.has(rel)) {
        expanded.delete(rel);
        renderScan();
        return;
      }
      expanded.add(rel);
      const hasKids = (state.scan.items || []).some(function (it) { return parentRel(it.rel_path) === rel; });
      if (!hasKids && !loadedDirs.has(rel)) {
        loadingDirs.add(rel);
        renderScan();
        try {
          const result = await api('POST', '/api/compare/scan-level', {
            left: spec(sources.left), right: spec(sources.right), rel_path: rel,
            deep: deep.value === 'deep', time_tolerance_seconds: Number(tolerance.value) || 2,
            ignore_exts: ignoreExt.value.split(/[,，\s]+/).filter(Boolean)
          });
          mergeScanItems(result.items || []);
          loadedDirs.add(rel);
          rollupAllFolders(state.scan.items, loadedDirs);
          recalculateSummary();
        } catch (error) {
          expanded.delete(rel);
          toast('展开失败：' + (error.message || error), 'err');
        } finally {
          loadingDirs.delete(rel);
          renderScan();
        }
        return;
      }
      renderScan();
    }

    function flattenFolderRows() {
      const kids = getFolderIndex().kids;
      const query = search.value.trim().toLowerCase();
      const filter = statusFilter.value;
      const rows = [];
      function pred(item) {
        if (!scanStatusVisible(item.status, filter, item)) return false;
        if (query && String(item.rel_path).toLowerCase().indexOf(query) < 0) return false;
        return true;
      }
      function walk(parent, depthValue) {
        (kids[parent] || []).forEach(function (item) {
          const dir = isDirItem(item);
          const open = dir && expanded.has(item.rel_path);
          let show = pred(item);
          // 修复：未展开的 pending 目录不再强制展示，避免“未展开”刷屏；由 pred 正常过滤，用户已知未展开，无需额外提示
          if (dir && filter === 'different' && item.status === 'same' && loadedDirs.has(item.rel_path) && !open) show = false;
          if (show) rows.push({ item: item, depth: depthValue, dir: dir, open: open, loading: loadingDirs.has(item.rel_path) });
          if (dir && open) walk(item.rel_path, depthValue + 1);
        });
      }
      walk('', 0);
      return rows;
    }

    function renderScan() {
      if (!state.scan) return;
      resultsBar.hidden = false;
      prevFolderDiffBtn.disabled = false;
      nextFolderDiffBtn.disabled = false;
      const integrity = updateScanIntegrity();
      const rows = flattenFolderRows();
      resultHost.innerHTML = '';
      if (rows.length) {
        currentFolderTree = createFolderTree(rows, sources, selected, compareFile, copyItem, updateSelectedStatus, toggleFolder, activeFolderRel, function (rel) {
          // 只更新选中状态，不重建虚拟列表；否则第一次点击会换掉 DOM，第二次点击无法触发 dblclick。
          activeFolderRel = rel;
        }, selectVisible);
        resultHost.appendChild(currentFolderTree.element);
      } else {
        currentFolderTree = null;
        resultHost.appendChild(el('div', {
          class: 'cmp-folder-empty',
          text: statusFilter.value === 'different' && !search.value.trim() ? '没有差异：两侧目录在当前比较模式下完全一致。' : '当前筛选条件没有匹配项目。'
        }));
      }
      const s = state.scan.summary || {};
      progress.textContent = (integrity.unsafe ? '结果未完全验证' : '完成') + ' · ' + (state.scan.elapsed_ms || 0) + 'ms · 相同 ' + (s.same || 0) + ' · 不同 ' + (s.different || 0) + ' · 左新 ' + (s.left_newer || 0) + ' · 右新 ' + (s.right_newer || 0) + ' · 仅左 ' + (s.left_only || 0) + ' · 仅右 ' + (s.right_only || 0);
      resultCount.textContent = '显示 ' + rows.length + ' 项 · 总计 ' + ((state.scan.items || []).length) + ' 项';
      if (integrity.unsafe) selectionCount.title = '结果不完整，批量覆盖已禁用';
      updateSelectedStatus();
      updateFolderDiffBadge();
    }

    function visibleItems() {
      return flattenFolderRows().map(function (row) { return row.item; });
    }

    function selectVisible(forceChecked) {
      const candidates = visibleItems().filter(item => isBulkActionable(item));
      const allSelected = candidates.length && candidates.every(item => selected.has(item.rel_path));
      const shouldSelect = typeof forceChecked === 'boolean' ? forceChecked : !allSelected;
      if (!allSelected && candidates.length > 2500) toast('为保证操作流畅，本次先选择前 2500 项，请分批覆盖', 'warn');
      (shouldSelect ? candidates.slice(0, 2500) : candidates).forEach(item => shouldSelect ? selected.add(item.rel_path) : selected.delete(item.rel_path));
      renderScan();
    }

    function clearSelection() {
      selected.clear();
      renderScan();
      updateSelectedStatus();
    }

    function updateSelectedStatus() {
      selectionCount.textContent = '已选择 ' + selected.size + ' 项';
      selectionBar.hidden = !selected.size;
      const integrity = scanIntegrity(state.scan);
      const hasBlockedSelection = Array.from(selected).some(function (rel) {
        const item = (state.scan && state.scan.items || []).find(function (entry) { return entry.rel_path === rel; });
        return !isBulkActionable(item);
      });
      coverRightBtn.disabled = coverLeftBtn.disabled = !state.scan || !selected.size || integrity.unsafe || hasBlockedSelection;
      const hint = selectionBar.querySelector('.cmp2-selection-hint');
      if (hint) hint.textContent = integrity.unsafe
        ? '结果不完整或包含错误，批量覆盖已禁用；请重新扫描并展开待验证目录。'
        : hasBlockedSelection
          ? '所选项目包含类型冲突或错误项，已禁用覆盖。'
          : '覆盖前会显示完整预览，不会删除目标侧多余文件。';
    }

    function updateFolderDiffBadge(activeIdx, totalCount) {
      if (!state.scan) {
        folderDiffBadge.textContent = '';
        return;
      }
      const rows = flattenFolderRows();
      const diffRows = rows.filter(r => r.item.status && r.item.status !== 'same' && r.item.status !== 'pending');
      const total = totalCount !== undefined ? totalCount : diffRows.length;
      if (!total) {
        folderDiffBadge.textContent = '两侧一致';
        return;
      }
      if (activeIdx !== undefined) {
        folderDiffBadge.textContent = '差异 ' + activeIdx + ' / ' + total;
      } else {
        const curr = diffRows.findIndex(d => d.item.rel_path === activeFolderRel);
        if (curr >= 0) {
          folderDiffBadge.textContent = '差异 ' + (curr + 1) + ' / ' + total;
        } else {
          folderDiffBadge.textContent = '共 ' + total + ' 处差异';
        }
      }
    }

    function navigateFolderDiff(delta) {
      if (!state.scan) { toast('请先比对两侧目录', 'warn'); return; }
      const rows = flattenFolderRows();
      if (!rows.length) return;
      const diffRows = [];
      rows.forEach((r, idx) => {
        if (r.item.status && r.item.status !== 'same' && r.item.status !== 'pending') {
          diffRows.push({ idx, row: r });
        }
      });
      if (!diffRows.length) {
        toast('当前筛选视图下没有可见差异项', 'info');
        return;
      }
      let currIdx = diffRows.findIndex(d => d.row.item.rel_path === activeFolderRel);
      if (currIdx < 0) {
        currIdx = delta > 0 ? 0 : diffRows.length - 1;
      } else {
        currIdx = (currIdx + delta + diffRows.length) % diffRows.length;
      }
      const target = diffRows[currIdx];
      activeFolderRel = target.row.item.rel_path;
      renderScan();
      if (currentFolderTree && currentFolderTree.scrollToRow) {
        currentFolderTree.scrollToRow(target.idx);
      }
      updateFolderDiffBadge(currIdx + 1, diffRows.length);
    }

    function coverDirection(direction) {
      if (!state.scan) { toast('请先比对两侧目录', 'warn'); return; }
      const integrity = scanIntegrity(state.scan);
      if (integrity.unsafe) {
        toast('结果不完整或包含错误，批量覆盖已禁用；请先重新扫描并展开待验证目录', 'warn');
        return;
      }
      const fromLeft = direction === 'right';
      const validForDirection = (state.scan.items || []).filter(item => selected.has(item.rel_path) && isBulkActionable(item) && (fromLeft ? item.left : item.right));

      if (!validForDirection.length) {
        toast('所选项目在' + (fromLeft ? '左侧' : '右侧') + '没有可覆盖的来源，请调整选择', 'warn');
        return;
      }
      syncSelected(direction);
    }

    async function syncSelected(direction) {
      if (!state.scan || !selected.size) return;
      const integrity = scanIntegrity(state.scan);
      if (integrity.unsafe) { toast('结果不完整或包含错误，批量覆盖已禁用', 'warn'); return; }
      if (selected.size > 2500) { toast('单次最多预览并覆盖 2500 项，请分批操作', 'warn'); return; }
      const fromLeft = direction === 'right';
      const plan = (state.scan.items || []).filter(item => selected.has(item.rel_path) && isBulkActionable(item)).filter(item => {
        const sourceEntry = fromLeft ? item.left : item.right;
        return !!sourceEntry;
      }).map(item => {
        const sourceEntry = fromLeft ? item.left : item.right, targetEntry = fromLeft ? item.right : item.left;
        return { item, sourceEntry, targetEntry, enabled: true, request: { rel_path: item.rel_path, expected: targetEntry ? { size: targetEntry.size, mtime: targetEntry.mtime } : null } };
      });
      if (!plan.length) {
        toast('所选项目在' + (fromLeft ? '左侧' : '右侧') + '没有可复制的源文件', 'warn');
        return;
      }
      showSyncPreview(direction, plan);
    }

    function showSyncPreview(direction, plan) {
      let jobID = '', pollTimer = 0, closed = false;
      const body = el('div', { class: 'cmp-source-body cmp-sync-body' });
      const actions = el('div', { class: 'cmp-source-actions' });
      const title = '覆盖预览 · ' + (direction === 'right' ? '左 → 右（更新 + 仅左，不删除右侧多余）' : '右 → 左（更新 + 仅右，不删除左侧多余）');
      const summary = el('div', { class: 'cmp-sync-summary', role: 'status', 'aria-live': 'polite' });
      const list = el('div', { class: 'cmp-sync-list', role: 'group', 'aria-label': '覆盖项目清单' });
      let dialog;

      function close() { if (dialog) dialog.close(); }
      function setButtonText(button, text) {
        const label = button && button.querySelector ? button.querySelector('span') : null;
        if (label) label.textContent = text;
        else if (button) button.textContent = text;
      }
      function updateSummary() {
        const enabled = plan.filter(row => row.enabled);
        const stats = enabled.reduce(function (out, row) {
          if (row.sourceEntry && row.sourceEntry.is_dir) out.directories++;
          else if (row.sourceEntry) { out.files++; if (Number.isFinite(row.sourceEntry.size)) out.bytes += Number(row.sourceEntry.size); else out.unknownBytes++; }
          return out;
        }, { files: 0, directories: 0, bytes: 0, unknownBytes: 0 });
        const parts = ['计划 ' + enabled.length + ' 项'];
        if (stats.files) parts.push(stats.files + ' 个文件');
        if (stats.directories) parts.push(stats.directories + ' 个目录（文件数待展开）');
        if (stats.bytes > 0 && !stats.directories && !stats.unknownBytes) parts.push(formatBytes(stats.bytes));
        else if (stats.directories || stats.unknownBytes) parts.push('字节数待计算');
        parts.push(options.backup ? '覆盖前备份' : '不创建备份');
        summary.textContent = parts.join(' · ');
        if (start) start.disabled = enabled.length === 0;
      }

      plan.forEach(row => {
        const input = el('input', { type: 'checkbox', 'aria-label': '选择 ' + row.item.rel_path });
        input.checked = true;
        input.onchange = () => { row.enabled = input.checked; updateSummary(); };
        const isDirectory = !!(row.sourceEntry && row.sourceEntry.is_dir);
        const bytesText = isDirectory ? '目录 · 文件数/字节数待展开计算' : (Number.isFinite(row.sourceEntry && row.sourceEntry.size) ? formatBytes(row.sourceEntry.size) : '大小待计算');
        list.appendChild(el('label', { class: 'cmp-sync-row' + (isDirectory ? ' is-directory' : '') }, [
          input,
          el('span', { class: 'cmp-sync-action', text: isDirectory ? '同步目录' : (row.targetEntry ? '覆盖' : '新增') }),
          el('span', { class: 'cmp-sync-path', text: row.item.rel_path, title: row.item.rel_path }),
          el('small', { text: bytesText })
        ]));
      });

      const cancel = makeButton('取消', 'close', close, 'btn');
      const start = makeButton('开始覆盖', 'right', startSync, 'btn btn-primary');
      actions.append(cancel, start);
      body.append(summary, list);
      dialog = Kairo.overlays.modal({ title: title, width: 760, body: body, footer: actions, onClose: function () {
        closed = true;
        if (pollTimer) clearTimeout(pollTimer);
        if (jobID) { const id = jobID; jobID = ''; api('DELETE', '/api/compare/jobs/' + id).catch(function () {}); }
        updateSelectedStatus();
      } });
      dialog.el.classList.add('cmp-sync-dialog');
      updateSummary();

      async function startSync() {
        const items = plan.filter(row => row.enabled).map(row => row.request);
        if (!items.length) return;
        start.disabled = true;
        setButtonText(cancel, '取消任务');
        list.innerHTML = '';
        const track = el('div', { class: 'cmp-progress-track cmp-sync-track', role: 'progressbar', 'aria-label': '覆盖任务进度', 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': '0' }, [el('div', { class: 'cmp-progress-bar' })]);
        const message = el('div', { class: 'cmp-sync-progress', role: 'status', 'aria-live': 'polite', text: '正在创建同步任务…' });
        body.innerHTML = '';
        body.append(summary, track, message);
        try {
          const response = await api('POST', '/api/compare/sync/start', { left: spec(sources.left), right: spec(sources.right), direction, items, backup: !!options.backup });
          jobID = response.job_id;
          poll();
        } catch (error) {
          message.className = 'cmp-sync-progress is-error';
          message.textContent = '启动失败：' + (error.message || error) + '。请检查来源连接后重试。';
          start.disabled = false;
          setButtonText(cancel, '关闭');
        }

        async function poll() {
          if (!jobID || closed) return;
          try {
            const job = await api('GET', '/api/compare/jobs/' + jobID);
            const percent = job.total ? Math.round(job.current / job.total * 100) : 5;
            track.firstChild.style.width = Math.min(100, percent) + '%';
            track.setAttribute('aria-valuenow', String(Math.max(0, Math.min(100, percent))));
            const progressParts = [];
            if (job.message || job.phase) progressParts.push(job.message || job.phase);
            if (job.total) progressParts.push(job.current + ' / ' + job.total + ' 项');
            if (Number.isFinite(job.bytes_current) && Number.isFinite(job.bytes_total) && job.bytes_total > 0) progressParts.push(formatBytes(job.bytes_current) + ' / ' + formatBytes(job.bytes_total));
            message.textContent = progressParts.join(' · ') || '正在覆盖…';
            if (job.status === 'completed') {
              jobID = '';
              const result = job.sync_result || {};
              selected.clear();
              toast('覆盖完成：成功 ' + (result.copied || 0) + '，失败 ' + (result.failed || 0) + (result.bytes ? ' · ' + formatBytes(result.bytes) : ''), result.failed ? 'warn' : 'ok');
              if (result.failed) {
                renderFailures(result);
              } else {
                dialog.close();
                await startScan();
              }
              return;
            }
            if (job.status === 'failed' || job.status === 'cancelled') throw new Error(job.error || '任务已取消');
            pollTimer = setTimeout(poll, 350);
          } catch (error) {
            jobID = '';
            message.className = 'cmp-sync-progress is-error';
            message.textContent = (error.message || String(error)) + '。可关闭后重新扫描。';
            setButtonText(cancel, '关闭');
            start.disabled = false;
          }
        }
      }

      function renderFailures(result) {
        body.innerHTML = '';
        body.appendChild(el('div', { class: 'cmp-sync-summary is-error', role: 'alert', text: '成功 ' + (result.copied || 0) + ' 项，失败 ' + (result.failed || 0) + ' 项；失败项目未被忽略，请检查后重试。' }));
        const failures = el('div', { class: 'cmp-sync-list', role: 'list', 'aria-label': '覆盖失败项目' });
        (result.failures || []).forEach(item => failures.appendChild(el('div', { class: 'cmp-sync-row failed', role: 'listitem' }, [
          el('span', { class: 'cmp-sync-action', text: '失败' }),
          el('span', { class: 'cmp-sync-path', text: item.rel_path, title: item.rel_path }),
          el('small', { text: item.error || '未知错误' })
        ])));
        body.appendChild(failures);
        actions.innerHTML = '';
        actions.appendChild(makeButton('关闭并重新扫描', 'compare', async function () { dialog.close(); await startScan(); }, 'btn btn-primary'));
      }
    }

    async function compareFile(item) {
      if (!item.left || !item.right || item.left.is_dir || item.right.is_dir) return;
      const left = Object.assign({}, sources.left, { path: item.left.path, label: item.rel_path });
      const right = Object.assign({}, sources.right, { path: item.right.path, label: item.rel_path });
      state.left.source = left;
      state.right.source = right;
      switchTab('text');
      if (state.textWorkbench && state.textWorkbench.loadPair) {
        await state.textWorkbench.loadPair(left, right);
      }
    }

    async function copyItem(item, direction) {
      const fromLeft = direction === 'right', sourceBase = fromLeft ? sources.left : sources.right, targetBase = fromLeft ? sources.right : sources.left;
      const sourceEntry = fromLeft ? item.left : item.right, targetEntry = fromLeft ? item.right : item.left;
      if (!sourceEntry) return;
      if (isTypeConflict(item)) { toast('文件与目录类型冲突，已禁止覆盖；请先处理目标侧同名项目', 'warn'); return; }
      if (item.pending || item.status === 'pending' || item.status === 'error' || item.status === 'errors') { toast('该项目尚未完成验证，暂不能覆盖', 'warn'); return; }

      if (sourceEntry.is_dir) {
        showSyncPreview(direction, [{
          item: item,
          sourceEntry: sourceEntry,
          targetEntry: targetEntry,
          enabled: true,
          request: { rel_path: item.rel_path, expected: targetEntry ? { size: targetEntry.size, mtime: targetEntry.mtime } : null }
        }]);
        return;
      }

      const source = Object.assign({}, sourceBase, { path: sourceEntry.path });
      const target = Object.assign({}, targetBase, { path: targetEntry ? targetEntry.path : joinPath(targetBase, targetBase.path, item.rel_path) });
      try {
        await api('POST', '/api/compare/copy', {
          source: spec(source), target: spec(target),
          expected: targetEntry ? { size: targetEntry.size, mtime: targetEntry.mtime } : null,
          backup: !!options.backup
        });
        toast('已复制：' + item.rel_path, 'ok');
        startScan();
      } catch (error) {
        toast('复制失败：' + (error.message || error), 'err');
      }
    }
    return { startScan: startScan };
  }

  function folderRowHeight() { return 48; }

  function createFolderTree(rows, sources, selected, onCompare, onCopy, onSelectionChange, onToggle, activeRel, onActiveChange, onSelectAll) {
    const selectable = rows.filter(function(row) { return isBulkActionable(row.item); });
    const selectAll = el('input', { type: 'checkbox', title: '选择当前筛选结果', 'aria-label': '选择当前筛选结果' });
    selectAll.checked = selectable.length > 0 && selectable.every(function(row) { return selected.has(row.item.rel_path); });
    selectAll.indeterminate = !selectAll.checked && selectable.some(function(row) { return selected.has(row.item.rel_path); });
    selectAll.onchange = function() { onSelectAll(!!selectAll.checked); };
    const header = el('div', { class: 'cmp-folder-table-head cmp-folder-tree-head', role: 'row', 'aria-rowindex': '1' }, [
      el('span', { role: 'columnheader', 'aria-label': '展开', text: '' }),
      el('span', { class: 'cmp2-table-select-head', role: 'columnheader' }, [selectAll, el('span', { text: '状态' })]),
      el('span', { role: 'columnheader', text: sourceLabel(sources.left) }),
      el('span', { role: 'columnheader', text: '操作' }),
      el('span', { role: 'columnheader', text: sourceLabel(sources.right) })
    ]);
    const viewport = el('div', { class: 'cmp-folder-viewport', role: 'rowgroup', 'aria-label': '比较结果行' });
    const canvas = el('div', { class: 'cmp-folder-canvas' });
    const height = folderRowHeight();
    canvas.style.height = Math.max(1, rows.length * height) + 'px';
    viewport.appendChild(canvas);
    let oldStart = -1, oldEnd = -1;
    let currentActiveRel = activeRel || '';
    let table = null;

    function syncActiveDescendant() {
      if (!table) return;
      const activeNode = Array.from(canvas.querySelectorAll('.cmp-folder-row')).find(function (node) { return node.getAttribute('data-rel') === currentActiveRel; });
      if (activeNode) table.setAttribute('aria-activedescendant', activeNode.id);
      else table.removeAttribute('aria-activedescendant');
    }

    function setActive(rel) {
      currentActiveRel = rel || '';
      canvas.querySelectorAll('.cmp-folder-row').forEach(function(node) {
        node.classList.toggle('is-active-row', node.getAttribute('data-rel') === currentActiveRel);
      });
      syncActiveDescendant();
      if (table && document.activeElement !== table && typeof table.focus === 'function') {
        try { table.focus({ preventScroll: true }); } catch (_) { table.focus(); }
      }
      if (onActiveChange) onActiveChange(currentActiveRel);
    }

    function scrollToFolderRow(idx) {
      if (idx < 0 || idx >= rows.length) return;
      const targetScroll = (idx * height) - Math.floor((viewport.clientHeight || 540) / 2) + Math.floor(height / 2);
      viewport.scrollTop = Math.max(0, targetScroll);
      render();
    }

    function render() {
      const start = Math.max(0, Math.floor(viewport.scrollTop / height) - 10);
      const end = Math.min(rows.length, start + Math.ceil((viewport.clientHeight || 540) / height) + 20);
      if (start === oldStart && end === oldEnd) return;
      oldStart = start;
      oldEnd = end;
      canvas.innerHTML = '';
      for (let i = start; i < end; i++) {
        const row = rows[i], item = row.item;
        const isActive = currentActiveRel && item.rel_path === currentActiveRel;
        const node = el('div', {
          class: 'cmp-folder-row cmp-folder-tree-row status-' + (isTypeConflict(item) ? 'type_conflict' : item.status) + (isActive ? ' is-active-row' : ''),
          style: 'transform:translateY(' + (i * height) + 'px); cursor:pointer;',
          'data-rel': item.rel_path,
          'data-dir': row.dir ? '1' : '0',
          role: 'row',
          id: 'cmp-folder-row-' + i,
          'aria-rowindex': String(i + 2),
          'aria-level': String(row.depth + 1),
          'aria-selected': selected.has(item.rel_path) ? 'true' : 'false'
        });
        if (row.dir) node.setAttribute('aria-expanded', row.open ? 'true' : 'false');

        node.onclick = function(e) {
          if (e.target.closest('button, input')) return;
          setActive(item.rel_path);
        };

        // 双击行为由 viewport 统一委托处理，避免虚拟滚动重建 DOM 导致 per-node ondblclick 失效
        if (row.dir) {
          node.title = row.open ? '双击折叠文件夹' : '双击展开并比较内容（或点左侧箭头）';
        } else if (item.left && item.right) {
          node.title = '双击比对文件差异（或点“比对”按钮）';
        } else {
          node.title = item.rel_path;
        }

        const status = isTypeConflict(item) ? '类型冲突' : folderStatusText(item.status, row.loading);
        node.setAttribute('aria-label', item.rel_path + '，' + status + (row.dir ? '，目录' : '，文件'));
        const ops = el('span', { class: 'cmp-folder-ops', role: 'gridcell', 'aria-label': '操作' });

        if (!row.dir && item.left && item.right && !isTypeConflict(item) && !item.pending && item.status !== 'pending' && item.status !== 'error' && item.status !== 'errors') {
          ops.appendChild(el('button', {
            class: 'cmp-row-compare-btn',
            type: 'button',
            title: '比对两侧文件差异',
            'aria-label': '比对 ' + item.rel_path + ' 两侧文件差异',
            onclick: function (e) { e.stopPropagation(); onCompare(item); },
            unsafeHtml: icon('compare') + '<span>比对</span>'
          }));
        }
        if (item.left && !item.left.is_dir && !isTypeConflict(item) && !item.pending && item.status !== 'pending' && item.status !== 'error' && item.status !== 'errors') {
          ops.appendChild(el('button', {
            class: 'cmp-row-copy-btn',
            type: 'button',
            title: '复制左侧文件到右侧',
            'aria-label': '复制左侧文件到右侧：' + item.rel_path,
            onclick: function (e) { e.stopPropagation(); onCopy(item, 'right'); },
            unsafeHtml: icon('right')
          }));
        }
        if (item.right && !item.right.is_dir && !isTypeConflict(item) && !item.pending && item.status !== 'pending' && item.status !== 'error' && item.status !== 'errors') {
          ops.appendChild(el('button', {
            class: 'cmp-row-copy-btn',
            type: 'button',
            title: '复制右侧文件到左侧',
            'aria-label': '复制右侧文件到左侧：' + item.rel_path,
            onclick: function (e) { e.stopPropagation(); onCopy(item, 'left'); },
            unsafeHtml: icon('left')
          }));
        }
        if (row.dir && !isTypeConflict(item) && !item.pending && item.status !== 'error' && item.status !== 'errors' && item.status !== 'pending') {
          if (item.left && item.left.is_dir) {
            ops.appendChild(el('button', {
              class: 'cmp-row-copy-btn',
              type: 'button',
              title: '同步左侧目录到右侧',
              'aria-label': '同步左侧目录到右侧：' + item.rel_path,
              onclick: function (e) { e.stopPropagation(); onCopy(item, 'right'); },
              unsafeHtml: icon('right')
            }));
          }
          if (item.right && item.right.is_dir) {
            ops.appendChild(el('button', {
              class: 'cmp-row-copy-btn',
              type: 'button',
              title: '同步右侧目录到左侧',
              'aria-label': '同步右侧目录到左侧：' + item.rel_path,
              onclick: function (e) { e.stopPropagation(); onCopy(item, 'left'); },
              unsafeHtml: icon('left')
            }));
          }
        }

        const checkbox = el('input', { type: 'checkbox', 'aria-label': '选择 ' + item.rel_path });
        checkbox.checked = selected.has(item.rel_path);
        checkbox.disabled = !isBulkActionable(item);
        checkbox.onchange = function () {
          checkbox.checked ? selected.add(item.rel_path) : selected.delete(item.rel_path);
          node.setAttribute('aria-selected', checkbox.checked ? 'true' : 'false');
          onSelectionChange();
        };

        const twist = el('button', { class: 'cmp-tree-twist', type: 'button', 'data-action': row.dir ? 'expand-folder' : undefined, title: row.dir ? (row.open ? '折叠目录' : '展开目录并比较内容') : '', 'aria-label': row.dir ? (row.open ? '折叠目录 ' : '展开目录 ') + item.rel_path : '无子目录', disabled: !row.dir });
        if (row.dir) {
          twist.innerHTML = icon(row.open ? 'collapse' : 'expand');
          if (row.loading) twist.classList.add('busy');
          twist.onclick = function (ev) { ev.stopPropagation(); onToggle(item); };
        }
        const pad = { style: 'padding-left:' + (8 + row.depth * 16) + 'px' };
        const statusBadge = el('span', { class: 'cmp-status-badge cmp-status-' + (isTypeConflict(item) ? 'type_conflict' : item.status), text: isTypeConflict(item) ? '类型冲突' : folderStatusText(item.status, row.loading) });
        node.append(
          el('span', { role: 'gridcell', class: 'cmp-folder-expander-cell' }, [twist]),
          el('span', { class: 'cmp-folder-status', role: 'gridcell', 'aria-label': status }, [checkbox, statusBadge]),
          folderCell(item.left, item.rel_path, pad),
          ops,
          folderCell(item.right, item.rel_path, pad)
        );
        canvas.appendChild(node);
      }
      syncActiveDescendant();
    }

    function handleTreegridKeydown(e) {
      if (e.target && e.target.closest && e.target.closest('button, input, select, textarea, [contenteditable="true"]')) return;
      if (!rows.length) return;
      let currIdx = rows.findIndex(r => r.item.rel_path === currentActiveRel);
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        currIdx = currIdx < 0 ? 0 : Math.min(rows.length - 1, currIdx + 1);
        setActive(rows[currIdx].item.rel_path);
        scrollToFolderRow(currIdx);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        currIdx = currIdx < 0 ? rows.length - 1 : Math.max(0, currIdx - 1);
        setActive(rows[currIdx].item.rel_path);
        scrollToFolderRow(currIdx);
      } else if (e.key === 'Home' || e.key === 'End') {
        e.preventDefault();
        currIdx = e.key === 'Home' ? 0 : rows.length - 1;
        setActive(rows[currIdx].item.rel_path);
        scrollToFolderRow(currIdx);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (currIdx >= 0) {
          const r = rows[currIdx];
          if (r.dir) onToggle(r.item);
          else if (r.item.left && r.item.right && !isTypeConflict(r.item)) onCompare(r.item);
          else if (isTypeConflict(r.item) && Kairo.core && Kairo.core.toast) Kairo.core.toast('类型冲突项目不能执行文件比对或覆盖', 'warn');
        }
      } else if (e.key === ' ') {
        e.preventDefault();
        if (currIdx >= 0) {
          const r = rows[currIdx];
          if (isBulkActionable(r.item)) {
            if (selected.has(r.item.rel_path)) selected.delete(r.item.rel_path);
            else selected.add(r.item.rel_path);
            onSelectionChange();
            render();
          } else if (isTypeConflict(r.item) && Kairo.core && Kairo.core.toast) Kairo.core.toast('类型冲突项目不可选择', 'warn');
        }
      }
    }

    viewport.addEventListener('scroll', render);
    // 委托式双击：解决虚拟滚动后 per-node ondblclick 因重建丢失的问题
    viewport.addEventListener('dblclick', function(e) {
      if (e.target.closest('button, input')) return;
      const rowEl = e.target.closest('.cmp-folder-row');
      if (!rowEl) return;
      const rel = rowEl.getAttribute('data-rel');
      if (!rel) return;
      const row = rows.find(function(r) { return r.item.rel_path === rel; });
      if (!row) return;
      if (row.dir) onToggle(row.item);
      else if (row.item.left && row.item.right) onCompare(row.item);
    });
    table = el('div', { class: 'cmp-folder-table', role: 'treegrid', tabindex: '0', 'aria-label': '文件夹比较结果', 'aria-colcount': '5', 'aria-rowcount': String(rows.length + 1), 'aria-multiselectable': 'true' }, [header, viewport]);
    table.addEventListener('keydown', handleTreegridKeydown);
    requestAnimationFrame(render);
    return {
      element: table,
      scrollToRow: scrollToFolderRow
    };
  }
  function folderStatusText(status, loading) {
    if (loading) return '校验中';
    if (status === 'pending') return '待验证';
    return { same: '相同', different: '不同', suspect: '待校验', pending: '待验证', left_newer: '左侧较新', right_newer: '右侧较新', left_only: '仅左', right_only: '仅右', error: '错误', errors: '错误', type_conflict: '类型冲突', 'type-conflict': '类型冲突' }[status] || status || '';
  }
  function scanStatusVisible(status, filter, item) {
    if (!filter || filter === 'all') return true;
    if (filter === 'different') return status !== 'same';
    if (filter === 'orphans') return status === 'left_only' || status === 'right_only';
    if (filter === 'pending') return status === 'pending' || status === 'suspect';
    if (filter === 'blocked') return status === 'error' || status === 'errors' || status === 'type_conflict' || status === 'type-conflict' || isTypeConflict(item);
    return status === filter;
  }
  function folderCell(entry, rel, pad) {
    if (!entry) return el('span', { class: 'cmp-folder-cell missing', role: 'gridcell', 'aria-label': '该侧不存在', text: '—' });
    const name = (function () { const i = String(rel || '').lastIndexOf('/'); return i < 0 ? rel : rel.slice(i + 1); })();
    const attrs = { class: 'cmp-folder-cell', title: entry.path, role: 'gridcell' };
    if (pad && pad.style) attrs.style = pad.style;
    const typeIcon = el('span', { class: 'cmp-entry-icon', unsafeHtml: icon(entry.is_dir ? 'folder' : 'file') });
    typeIcon.setAttribute('aria-hidden', 'true');
    const sizeLabel = entry.is_dir ? '目录 · 大小待展开' : (Number.isFinite(entry.size) ? formatBytes(entry.size) : '大小待计算');
    return el('span', attrs, [typeIcon, el('span', { class: 'cmp-folder-name', text: name }), el('small', { text: sizeLabel })]);
  }

  async function ensureConnections() { if (connections.length) return connections; try { const response = await api('GET', '/api/compare/connections'); connections = response.sftp || []; } catch (_) { connections = []; } return connections; }
  async function openSourceDialog(current, directory, onSelect) {
    await ensureConnections();
    const source = Object.assign(defaultSource('left'), current || {});
    source.tls_mode = source.tls_mode || source.tls || '';
    if (directory && source.kind === 'text') source.kind = 'local';
    const formId = 'cmp-source-dialog-form-' + Date.now().toString(36);
    const body = el('form', { id: formId, class: 'cmp-source-body', novalidate: true });
    const footer = el('div', { class: 'cmp-source-actions' });
    const dlg = Kairo.overlays.modal({ title: directory ? '选择比较目录' : '打开比较文件', width: 620, body: body, footer: footer });
    const choices = []; if (!directory) choices.push(el('option', { value: 'text', text: '临时文本' })); choices.push(el('option', { value: 'local', text: '本地' }), el('option', { value: 'sftp', text: 'SFTP / SSH' }), el('option', { value: 'ftp', text: 'FTP / FTPS' }));
    const kind = el('select', { 'aria-label': '数据源类型' }, choices); kind.value = source.kind;
    const pathInput = el('input', { type: 'text', value: source.path || '', 'aria-label': directory ? '目录路径' : '文件路径', placeholder: directory ? '目录绝对路径，可粘贴' : '文件绝对路径' });
    const connection = el('select', { 'aria-label': 'SFTP 服务器' }, [el('option', { value: '', text: '选择已配置的 SSH 服务器' })]);
    connections.forEach((item, index) => connection.appendChild(el('option', { value: String(index), text: (item.System || item.system) + ' / ' + (item.Name || item.name) + ' (' + (item.Host || item.host) + ')' })));
    if (source.kind === 'sftp') {
      const selectedConnection = connections.findIndex(function (item) {
        return (item.System || item.system || '') === (source.system || '') && (item.Name || item.name || '') === (source.server || '');
      });
      if (selectedConnection >= 0) connection.value = String(selectedConnection);
    }
    const host = el('input', { type: 'text', value: source.host || '', 'aria-label': 'FTP 主机', placeholder: 'FTP 主机' }), port = el('input', { type: 'number', value: source.port || 21, 'aria-label': 'FTP 端口', min: 1, max: 65535 });
    const username = el('input', { type: 'text', value: source.username || '', 'aria-label': '用户名', autocomplete: 'username', placeholder: '用户名' }), password = el('input', { type: 'password', value: '', 'aria-label': '密码', autocomplete: 'new-password', placeholder: '密码（不会保存到浏览器）' });
    const tlsMode = el('select', { 'aria-label': 'FTP 安全模式' }, [el('option', { value: '', text: '普通 FTP' }), el('option', { value: 'explicit', text: '显式 FTPS' }), el('option', { value: 'implicit', text: '隐式 FTPS' })]); tlsMode.value = source.tls_mode || '';
    const encoding = el('select', { 'aria-label': '文件编码' }, [el('option', { value: 'auto', text: '自动检测' }), el('option', { value: 'utf-8', text: 'UTF-8' }), el('option', { value: 'gb18030', text: 'GBK / GB18030' }), el('option', { value: 'utf-16le', text: 'UTF-16 LE' }), el('option', { value: 'utf-16be', text: 'UTF-16 BE' })]); encoding.value = source.encoding || 'auto';
    const localBrowse = browseButton({
      input: pathInput,
      directory: !!directory,
      compact: true,
      label: directory ? '浏览目录' : '浏览文件',
      title: directory ? '选择文件夹' : '选择文件'
    });
    const localRow = fieldRow('路径', [pathInput, localBrowse]), sftpRow = fieldRow('服务器', [connection]);
    const encodingRow = fieldRow('编码', [encoding]);
    const ftpRows = [fieldRow('主机', [host, port]), fieldRow('账户', [username, password]), fieldRow('安全', [tlsMode])];
    const warning = el('div', { class: 'cmp-source-warning', role: 'alert', text: '普通 FTP 会明文传输凭据，生产环境建议使用 SFTP 或 FTPS。' });
    const close = () => dlg.close();
    const submitSource = function (event) {
      if (event && event.preventDefault) event.preventDefault();
      source.kind = kind.value; source.path = pathInput.value.trim(); source.password = password.value; source.encoding = encoding.value;
      if (source.kind === 'sftp') { const item = connections[Number(connection.value)]; if (!item) { toast('请选择 SFTP 服务器', 'warn'); return; } source.system = item.System || item.system; source.server = item.Name || item.name; source.username = username.value || item.Username || item.username || ''; }
      if (source.kind === 'ftp') { source.host = host.value.trim(); source.port = Number(port.value) || 21; source.username = username.value; source.tls_mode = tlsMode.value; source.tls = tlsMode.value; }
      if (source.kind !== 'text' && !source.path) { toast('请输入路径', 'warn'); return; } close(); onSelect(source);
    };
    body.addEventListener('submit', submitSource);
    const confirm = makeButton('确定', 'open', submitSource, 'btn btn-primary');
    confirm.type = 'submit';
    confirm.setAttribute('form', formId);
    kind.onchange = update; tlsMode.onchange = update;
    function update() { const value = kind.value; sftpRow.style.display = value === 'sftp' ? '' : 'none'; ftpRows.forEach(row => row.style.display = value === 'ftp' ? '' : 'none'); localBrowse.style.display = value === 'local' ? '' : 'none'; pathInput.style.display = value === 'text' ? 'none' : ''; encodingRow.style.display = directory || value === 'text' ? 'none' : ''; warning.style.display = value === 'ftp' && !tlsMode.value ? '' : 'none'; }
    body.append(fieldRow('数据源', [kind]), sftpRow, ...ftpRows, localRow, encodingRow, warning);
    footer.append(makeButton('取消', '', close), confirm);
    update();
  }
  function fieldRow(label, children) { return el('label', { class: 'cmp-source-row' }, [el('span', { text: label }), el('div', { class: 'cmp-source-fields' }, children)]); }
  function formatBytes(bytes) { if (!Number.isFinite(bytes)) return ''; if (bytes < 1024) return bytes + ' B'; if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB'; return (bytes / 1048576).toFixed(1) + ' MB'; }

  Kairo.pages.compare = renderCompare;
  Kairo.state.routes.compare = renderCompare;
  Kairo.state.routeNames.compare = '文件与文本比较';
  Kairo.compareTest = {
    rollupAllFolders,
    parentRel,
    baseName,
    isDirItem,
    joinPath,
    isTypeConflict,
    isBulkActionable,
    sourceIdentity,
    normalizeFolderHistoryEntry,
    sourceIdentityLabel,
    sourceSecurityLabel,
    textMetrics,
    isLargeText,
    createEditorHistory,
    resolveSaveSide,
    makeCompareKey,
    handoffPathKey,
    validateWaspackHandoff,
    consumeWaspackHandoff,
    handoffNonceFromHash
  };
})();
