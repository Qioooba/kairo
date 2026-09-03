(function () {
  'use strict';

  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast } = Kairo.core;
  const { api, getPreference, putPreference, preferenceSaver, browseButton } = Kairo.api;

  const MAX_INLINE_ROWS = 5000;
  const LS_OPTIONS = 'kairo:compare:workbench-options';
  const LS_SOURCES = 'kairo:compare:workbench-sources';
  const PATH_HISTORY_LIMIT = 12;
  let connections = [];
  let activeScanJob = '';
  let scanPollTimer = 0;
  let persisted = { options: {}, sources: {}, folder_history: { left: [], right: [] } };
  const persistPreference = preferenceSaver('compare', 400);

  function icon(name) {
    const paths = {
      compare: 'M8 7h11M15 3l4 4-4 4M16 17H5M9 13l-4 4 4 4',
      prev: 'M12 19V5M5 12l7-7 7 7', next: 'M12 5v14M19 12l-7 7-7-7',
      open: 'M3 7h6l2 2h10v10H3z', save: 'M5 3h12l2 2v16H5zM8 3v6h8V3M8 21v-7h8v7',
      swap: 'M7 7h12M15 3l4 4-4 4M17 17H5M9 13l-4 4 4 4',
      left: 'M19 12H5M11 18l-6-6 6-6', right: 'M5 12h14M13 6l6 6-6 6',
      close: 'M6 6l12 12M18 6L6 18', folder: 'M3 6h7l2 2h9v11H3z',
      expand: 'M9 18l6-6-6-6', collapse: 'M6 9l6 6 6-6',
      lineRight: 'M5 12h12M13 8l4 4-4 4', lineLeft: 'M19 12H7M11 8l-4 4 4 4',
    };
    return '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="' + (paths[name] || paths.compare) + '"/></svg>';
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
      ['left', 'right'].forEach(k => { const source = Object.assign({}, state[k].source); delete source.password; clean[k] = source; });
      persisted.sources = clean;
      persistPreference(persisted);
    } catch (_) {}
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
    const sep = source.kind === 'local' && /^([A-Za-z]:\\|\\\\)/.test(root) ? '\\' : '/';
    return String(root || '').replace(/[\\/]$/, '') + sep + String(rel || '').replace(/^[\\/]/, '').replace(/[\\/]/g, sep);
  }
  function makeButton(text, name, onclick, className) {
    return el('button', { class: className || 'btn btn-sm', title: text, onclick, unsafeHtml: (name ? icon(name) : '') + '<span>' + text + '</span>' });
  }
  function makeCheck(label, checked, onchange) {
    const input = el('input', { type: 'checkbox' }); input.checked = !!checked;
    input.addEventListener('change', () => onchange(input.checked));
    return el('label', { class: 'cmp-wb-check' }, [input, document.createTextNode(label)]);
  }

  function createEditor(side, onDirty) {
    const gutter = el('pre', { class: 'cmp-editor-gutter', text: '1' });
    const textarea = el('textarea', { class: 'cmp-editor-input', spellcheck: 'false', wrap: 'off', placeholder: side === 'left' ? '粘贴、拖入文本或文件；也可点打开' : '粘贴、拖入修改后的文本或文件' });
    const highlight = el('pre', { class: 'cmp-editor-highlight', 'aria-hidden': 'true' });
    let language = 'text';
    const syntax = Kairo.workbench && Kairo.workbench.syntaxEditor;
    let raf = 0;
    const updateGutter = () => {
      cancelAnimationFrame(raf); raf = requestAnimationFrame(() => {
        const count = textarea.value === '' ? 1 : textarea.value.split('\n').length;
        let numbers = ''; for (let i = 1; i <= count; i++) numbers += i + '\n';
        gutter.textContent = numbers;
      });
    };
    textarea.addEventListener('input', () => { pushHistory(); updateGutter(); if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language); onDirty(); });

    const history = [textarea.value || ''];
    let historyIdx = 0;
    let historyTimer = 0;
    function pushHistory() {
      clearTimeout(historyTimer);
      historyTimer = setTimeout(() => {
        const val = textarea.value;
        if (history[historyIdx] !== val) {
          history.splice(historyIdx + 1);
          history.push(val);
          while (history.length > 50) {
            history.shift();
          }
          historyIdx = history.length - 1;
        }
      }, 150);
    }
    function undo() {
      if (historyIdx > 0) {
        historyIdx--;
        textarea.value = history[historyIdx];
        updateGutter();
        if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language);
        onDirty();
      }
    }
    function redo() {
      if (historyIdx < history.length - 1) {
        historyIdx++;
        textarea.value = history[historyIdx];
        updateGutter();
        if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language);
        onDirty();
      }
    }
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
    const root = el('div', { class: 'cmp-editor' }, [gutter, editorLayer]);
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
        reader.onload = function () { textarea.value = String(reader.result || ''); updateGutter(); if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language); onDirty(); };
        reader.readAsText(file);
        return;
      }
      const text = e.dataTransfer.getData('text/plain');
      if (text) { textarea.value = text; updateGutter(); if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language); onDirty(); }
    });
    return { root: root, textarea,
      setValue(value) { textarea.value = value || ''; updateGutter(); if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language); },
      setLanguage(value) { language = value || 'text'; if (syntax) highlight.innerHTML = syntax.highlight(textarea.value, language); },
      getValue() { return textarea.value; }, undo, redo, updateGutter };
  }

  async function renderCompare(view) {
    const renderToken = view.dataset.renderToken;
    if (scanPollTimer) clearTimeout(scanPollTimer);
    activeScanJob = '';
    view.innerHTML = '<div class="card muted">正在恢复比较工作台偏好…</div>';
    const preference = await loadWorkbenchPreference();
    if (view.dataset.renderToken !== renderToken) return;
    view.innerHTML = '';
    const options = preference.options, saved = preference.sources;
    const state = {
      left: { source: Object.assign(defaultSource('left'), saved.left || {}), version: null, codec: { encoding: 'utf-8', eol: 'lf', bom: false }, dirty: false },
      right: { source: Object.assign(defaultSource('right'), saved.right || {}), version: null, codec: { encoding: 'utf-8', eol: 'lf', bom: false }, dirty: false },
      diff: null, hunks: [], hunkIndex: -1, mode: 'text', scan: null,
    };
    const crumb = document.getElementById('crumb'); if (crumb) crumb.textContent = '文件与文本比较';
    const root = el('div', { class: 'cmp-workbench' });
    const textTab = el('button', { class: 'cmp-wb-tab active', text: '文本 / 文件比较' });
    const folderTab = el('button', { class: 'cmp-wb-tab', text: '文件夹比较' });
    const tabs = el('div', { class: 'cmp-wb-tabs' }, [textTab, folderTab]);
    const textPanel = el('div', { class: 'cmp-wb-panel' });
    const folderPanel = el('div', { class: 'cmp-wb-panel', style: 'display:none' });
    root.append(tabs, textPanel, folderPanel); view.appendChild(root);
    textTab.onclick = () => switchTab('text'); folderTab.onclick = () => switchTab('folder');
    function switchTab(mode) {
      state.mode = mode; textTab.classList.toggle('active', mode === 'text'); folderTab.classList.toggle('active', mode === 'folder');
      textPanel.style.display = mode === 'text' ? '' : 'none'; folderPanel.style.display = mode === 'folder' ? '' : 'none';
    }
    buildTextWorkbench(textPanel, state, options);
    buildFolderWorkbench(folderPanel, state, options, switchTab);
    ensureConnections();
  }

  function buildTextWorkbench(panel, state, options) {
    let showingResult = false, syncLock = false, compareSeq = 0;
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
      const badge = el('span', { class: 'cmp-source-badge', text: '文本' });
      const label = el('span', { class: 'cmp-source-label', text: sourceLabel(state[side].source), title: sourceLabel(state[side].source) });
      const meta = el('span', { class: 'cmp-source-meta', text: '' });
      const dirty = el('span', { class: 'cmp-dirty', text: '' });
      const language = el('select', { class: 'cmp-language', title: '语法高亮' }, [el('option', { value: 'text', text: '文本' }), el('option', { value: 'java', text: 'Java' }), el('option', { value: 'sql', text: 'SQL' }), el('option', { value: 'xml', text: 'XML' }), el('option', { value: 'json', text: 'JSON' })]);
      language.onchange = function () { editors[side].setLanguage(language.value); if (state.diff) renderResult(); };
      const undoBtn = makeButton('撤销', 'prev', () => editors[side].undo(), 'btn btn-xs');
      const redoBtn = makeButton('重做', 'next', () => editors[side].redo(), 'btn btn-xs');
      const openBtn = makeButton('打开', 'open', () => openSourceDialog(state[side].source, false, async source => { state[side].source = source; saveSources(state); await loadSide(side); }));
      const saveBtn = makeButton('保存', 'save', () => saveSide(side)); saveBtn.disabled = true;
      sourceHeaders[side] = { badge, label, meta, dirty, saveBtn, language, undoBtn, redoBtn };
      editorGrid.appendChild(el('section', { class: 'cmp-editor-pane' }, [el('div', { class: 'cmp-source-header' }, [badge, label, meta, dirty, language, undoBtn, redoBtn, openBtn, saveBtn]), editors[side].root]));
    });
    const compareBtn = makeButton('比对', 'compare', compareNow, 'btn btn-primary btn-sm');
    compareBtn.setAttribute('data-action', 'text-compare');
    const editBtn = makeButton('返回编辑', 'open', showEditors); editBtn.style.display = 'none';
    const saveLeftBtn = makeButton('保存左侧', 'save', () => saveSide('left')); saveLeftBtn.setAttribute('data-action', 'save-left'); saveLeftBtn.style.display = 'none';
    const saveRightBtn = makeButton('保存右侧', 'save', () => saveSide('right')); saveRightBtn.setAttribute('data-action', 'save-right'); saveRightBtn.style.display = 'none';
    const dirtyBanner = el('span', { class: 'cmp-dirty-banner', text: '' });

    const toggleEditorsBtn = makeButton('展开原文件编辑', 'open', toggleEditors, 'btn btn-xs');
    const copySelRightBtn = makeButton('选择覆盖到右侧 ➡', 'right', () => copySelectionToSide('right'), 'btn btn-xs');
    const copySelLeftBtn = makeButton('⬅ 选择覆盖到左侧', 'left', () => copySelectionToSide('left'), 'btn btn-xs');
    const diffCountBadge = el('span', { class: 'cmp-diff-count-badge', text: '' });

    function toggleEditors() {
      const isHidden = editorGrid.style.display === 'none';
      editorGrid.style.display = isHidden ? '' : 'none';
      toggleEditorsBtn.textContent = isHidden ? '折叠原文件' : '展开原文件编辑';
    }

    function copySelectionToSide(toSide) {
      const fromSide = toSide === 'right' ? 'left' : 'right';
      const fromTa = editors[fromSide].textarea;
      const toTa = editors[toSide].textarea;
      const sel = fromTa.value.substring(fromTa.selectionStart, fromTa.selectionEnd);
      const textToCopy = sel || fromTa.value;
      if (!textToCopy) return toast('源文本为空', 'warn');
      if (toTa.selectionStart != null && toTa.selectionStart !== toTa.selectionEnd) {
        toTa.setRangeText(textToCopy, toTa.selectionStart, toTa.selectionEnd, 'end');
      } else {
        toTa.value = textToCopy;
      }
      editors[toSide].updateGutter();
      markDirty(toSide);
      scheduleRecompare();
      toast('已覆盖到' + (toSide === 'right' ? '右侧' : '左侧'), 'ok');
    }

    const prevBtn = makeButton('上一处', 'prev', () => navigateHunk(-1));
    const nextBtn = makeButton('下一处', 'next', () => navigateHunk(1));
    const swapBtn = makeButton('交换', 'swap', swapSides);
    const copyBtn = makeButton('复制 Diff', '', copyDiff), downloadBtn = makeButton('下载 Diff', '', downloadDiff);
    [prevBtn, nextBtn, copyBtn, downloadBtn].forEach(btn => btn.disabled = true);
    const modeSelect = el('select', { class: 'cmp-mode-select' }, [el('option', { value: 'side', text: '左右并排' }), el('option', { value: 'unified', text: 'Unified' }), el('option', { value: 'changes', text: '仅差异' })]);
    options.mode = options.mode || 'side';
    modeSelect.value = options.mode; modeSelect.onchange = () => { options.mode = modeSelect.value; saveOptions(options); if (state.diff) renderResult(); };
    const onlyDiff = makeCheck('只显示差异', options.onlyDiff, value => { options.onlyDiff = value; saveOptions(options); if (state.diff) renderResult(); });
    const trim = makeCheck('忽略行尾空白', options.trim_space, value => { options.trim_space = value; saveOptions(options); });
    const blank = makeCheck('忽略空行', options.ignore_blank, value => { options.ignore_blank = value; saveOptions(options); });
    const ignoreCase = makeCheck('忽略大小写', options.ignore_case, value => { options.ignore_case = value; saveOptions(options); });
    const backup = makeCheck('替换前备份', options.backup, value => { options.backup = value; saveOptions(options); });
    panel.append(el('div', { class: 'cmp-wb-toolbar' }, [compareBtn, toggleEditorsBtn, copySelRightBtn, copySelLeftBtn, saveLeftBtn, saveRightBtn, dirtyBanner, diffCountBadge, el('span', { class: 'cmp-toolbar-sep' }), prevBtn, nextBtn, el('span', { class: 'cmp-toolbar-sep' }), swapBtn, copyBtn, downloadBtn, el('span', { class: 'cmp-toolbar-grow' }), modeSelect, onlyDiff]), el('div', { class: 'cmp-wb-options' }, [trim, blank, ignoreCase, backup]), editorGrid, resultHost, status);
    panel.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); compareNow(); }
      if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
        e.preventDefault();
        const side = document.activeElement && document.activeElement.closest('.cmp-diff-right') ? 'right' : 'left';
        saveSide(side);
      }
      if (e.key === 'F7') { e.preventDefault(); navigateHunk(-1); }
      if (e.key === 'F8') { e.preventDefault(); navigateHunk(1); }
    });

    let recompareTimer = 0;
    function refreshSaveButtons() {
      saveLeftBtn.disabled = state.left.source.kind === 'text' || !state.left.dirty;
      saveRightBtn.disabled = state.right.source.kind === 'text' || !state.right.dirty;
      saveLeftBtn.style.display = '';
      saveRightBtn.style.display = '';
      const bits = [];
      if (state.left.dirty) bits.push('左侧已修改');
      if (state.right.dirty) bits.push('右侧已修改');
      dirtyBanner.textContent = bits.join(' · ');
    }
    function markDirty(side) { state[side].dirty = true; sourceHeaders[side].dirty.textContent = '● 已修改'; sourceHeaders[side].saveBtn.disabled = state[side].source.kind === 'text'; refreshSaveButtons(); }
    function updateHeader(side) {
      const source = state[side].source; sourceHeaders[side].badge.textContent = source.kind === 'text' ? '文本' : source.kind.toUpperCase();
      sourceHeaders[side].label.textContent = sourceLabel(source); sourceHeaders[side].label.title = sourceLabel(source);
      const codec = state[side].codec; sourceHeaders[side].meta.textContent = source.kind === 'text' ? '' : String(codec.encoding || 'utf-8').toUpperCase() + ' · ' + String(codec.eol || 'lf').toUpperCase() + (codec.bom ? ' · BOM' : '');
      sourceHeaders[side].dirty.textContent = state[side].dirty ? '● 已修改' : ''; sourceHeaders[side].saveBtn.disabled = source.kind === 'text' || !state[side].dirty;
      refreshSaveButtons();
    }
    async function loadSide(side) {
      const source = state[side].source;
      const inferredLanguage = Kairo.workbench && Kairo.workbench.syntaxEditor ? Kairo.workbench.syntaxEditor.languageFromPath(source.path || source.label, 'text') : 'text';
      sourceHeaders[side].language.value = inferredLanguage;
      editors[side].setLanguage(inferredLanguage);
      updateHeader(side); if (source.kind === 'text') return;
      status.textContent = '正在读取 ' + sourceLabel(source) + '…';
      try {
        const response = await api('POST', '/api/compare/read', { source: spec(source) });
        if (response.binary) throw new Error('检测到二进制文件，文本工作台不支持直接编辑');
        if (response.truncated) throw new Error('文件超过 8MB 文本编辑上限，请使用文件夹比较进行流式复制');
        editors[side].setLanguage(inferredLanguage); editors[side].setValue(response.text); state[side].version = response.version; state[side].codec = { encoding: response.encoding || 'utf-8', eol: response.eol || 'lf', bom: !!response.bom }; state[side].dirty = false; updateHeader(side);
        status.textContent = '已读取 ' + sourceLabel(source) + ' · ' + formatBytes(response.entry.size);
      } catch (error) { toast('读取失败：' + (error.message || error), 'err'); status.textContent = '读取失败'; }
    }
    async function saveSide(side) {
      const item = state[side]; if (item.source.kind === 'text') return;
      try {
        await api('POST', '/api/compare/write', { target: spec(item.source), content: editors[side].getValue(), expected: item.version, backup: !!options.backup, encoding: item.codec.encoding, eol: item.codec.eol, bom: !!item.codec.bom });
        item.dirty = false; updateHeader(side); await loadSide(side); toast('保存成功' + (options.backup ? '，原文件已备份' : ''), 'ok');
      } catch (error) { toast('保存失败：' + (error.message || error), 'err'); }
    }
    async function compareNow() {
      const requestNo = ++compareSeq;
      compareBtn.disabled = true; status.textContent = '正在计算差异…';
      try {
        const response = await api('POST', '/api/diff/compare', { left: editorLeft.getValue(), right: editorRight.getValue(), left_label: sourceLabel(state.left.source), right_label: sourceLabel(state.right.source), ignore: { trim_space: !!options.trim_space, ignore_blank: !!options.ignore_blank, ignore_case: !!options.ignore_case } });
        if (requestNo !== compareSeq) return;
        state.diff = response; state.hunks = buildHunks(response.lines || []); state.hunkIndex = state.hunks.length ? 0 : -1;
        renderResult(); showResult(); [prevBtn, nextBtn, copyBtn, downloadBtn].forEach(btn => btn.disabled = false);
        const diffMsg = '共 ' + state.hunks.length + ' 处差异 (新增 ' + response.stats.added + ' 行, 删除 ' + response.stats.removed + ' 行)';
        diffCountBadge.textContent = diffMsg;
        status.textContent = diffMsg + ' · 可直接在下方编辑，点击箭头合并差异';
      } catch (error) {
        if (requestNo !== compareSeq) return;
        toast('比对失败：' + (error.message || error), 'err'); status.textContent = '比对失败';
      } finally { if (requestNo === compareSeq) compareBtn.disabled = false; }
    }
    function showResult() { showingResult = true; panel.classList.add('cmp-panel-has-result'); editorGrid.style.display = 'none'; toggleEditorsBtn.textContent = '展开原文件编辑'; resultHost.style.display = ''; editBtn.style.display = 'none'; compareBtn.style.display = ''; refreshSaveButtons(); }
    function showEditors() { showingResult = false; panel.classList.remove('cmp-panel-has-result'); editorGrid.style.display = ''; resultHost.style.display = 'none'; editBtn.style.display = 'none'; compareBtn.style.display = ''; refreshSaveButtons(); }
    function renderResult() {
      resultHost.innerHTML = ''; if (!state.diff) return;
      if (options.mode === 'unified' && (state.diff.lines || []).length <= MAX_INLINE_ROWS && window.Diff2Html) {
        const wrapper = el('div', { class: 'cmp-inline-unified' }); wrapper.innerHTML = window.Diff2Html.html(state.diff.unified_diff || '', { drawFileList: false, outputFormat: 'line-by-line', matching: 'lines', renderNothingWhenEmpty: false }); resultHost.appendChild(wrapper); return;
      }
      const effectiveMode = options.mode || 'side';
      const rows = buildAlignedRows(state.diff.lines || [], editorLeft.getValue(), editorRight.getValue(), state.hunks);
      resultHost.appendChild(createVirtualDiff(rows.filter(row => (!options.onlyDiff && effectiveMode !== 'changes') || row.status !== 'equal'), state, effectiveMode, { hunk: applyHunk, line: applyLine, edit: commitLineEdit, language: { left: sourceHeaders.left.language.value, right: sourceHeaders.right.language.value } }));
    }
    function scheduleRecompare() {
      clearTimeout(recompareTimer);
      recompareTimer = setTimeout(compareNow, 280);
    }
    function navigateHunk(delta) {
      if (!state.hunks.length) return; state.hunkIndex = (state.hunkIndex + delta + state.hunks.length) % state.hunks.length;
      const viewport = resultHost.querySelector('.cmp-vdiff'); if (viewport) viewport.scrollTop = (state.hunks[state.hunkIndex].rowIndex || 0) * 25;
      status.textContent = '差异 ' + (state.hunkIndex + 1) + ' / ' + state.hunks.length;
    }
    function applyHunk(index, direction) {
      const hunk = state.hunks[index]; if (!hunk) return;
      const target = direction === 'right' ? editorRight : editorLeft, sourceLines = direction === 'right' ? hunk.leftText : hunk.rightText;
      const start = direction === 'right' ? hunk.rightStart : hunk.leftStart, count = direction === 'right' ? hunk.rightCount : hunk.leftCount;
      const lines = splitEditorLines(target.getValue()); lines.splice(start, count, ...sourceLines); target.setValue(lines.join('\n'));
      markDirty(direction === 'right' ? 'right' : 'left'); compareNow();
    }
    function applyLine(row, direction) {
      if (!row) return;
      const targetSide = direction === 'right' ? 'right' : 'left';
      const sourceText = direction === 'right' ? row.leftText : row.rightText;
      const targetNo = direction === 'right' ? row.rightNo : row.leftNo;
      const otherNo = direction === 'right' ? row.leftNo : row.rightNo;
      const lines = splitEditorLines(editors[targetSide].getValue());
      if (targetNo > 0) lines[targetNo - 1] = sourceText;
      else lines.splice(Math.min(otherNo > 0 ? otherNo - 1 : lines.length, lines.length), 0, sourceText);
      editors[targetSide].setValue(lines.join('\n'));
      markDirty(targetSide); compareNow();
    }
    function commitLineEdit(side, lineNo, newText, row) {
      const lines = splitEditorLines(editors[side].getValue());
      if (lineNo > 0) {
        if ((lines[lineNo - 1] || '') === newText) return;
        lines[lineNo - 1] = newText;
      } else {
        if (!newText) return;
        const otherNo = side === 'left' ? row.rightNo : row.leftNo;
        lines.splice(Math.min(otherNo > 0 ? otherNo - 1 : lines.length, lines.length), 0, newText);
      }
      editors[side].setValue(lines.join('\n'));
      markDirty(side);
      scheduleRecompare();
    }
    function swapSides() {
      const leftText = editorLeft.getValue(); editorLeft.setValue(editorRight.getValue()); editorRight.setValue(leftText);
      const old = state.left; state.left = state.right; state.right = old; markDirty('left'); markDirty('right'); updateHeader('left'); updateHeader('right'); saveSources(state); if (state.diff) compareNow();
    }
    function copyDiff() { if (!state.diff) return; navigator.clipboard.writeText(state.diff.unified_diff || '').then(() => toast('Diff 已复制', 'ok'), () => toast('复制失败', 'err')); }
    function downloadDiff() {
      if (!state.diff) return; const blob = new Blob([state.diff.unified_diff || ''], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob), a = el('a', { href: url, download: 'compare_' + Date.now() + '.diff' }); document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(url), 0);
    }
    state.textWorkbench = { loadPair: async (left, right) => { state.left.source = left; state.right.source = right; updateHeader('left'); updateHeader('right'); await Promise.all([loadSide('left'), loadSide('right')]); compareNow(); } };
  }

  function splitEditorLines(text) { return text === '' ? [] : String(text).replace(/\r\n/g, '\n').split('\n'); }
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
    const viewport = el('div', { class: 'cmp-vdiff', tabindex: '0' }), canvas = el('div', { class: 'cmp-vdiff-canvas' }), rowHeight = 25;
    canvas.style.height = Math.max(1, rows.length * rowHeight) + 'px'; viewport.appendChild(canvas); let renderedStart = -1, renderedEnd = -1;
    function renderWindow() {
      const start = Math.max(0, Math.floor(viewport.scrollTop / rowHeight) - 15), end = Math.min(rows.length, start + Math.ceil((viewport.clientHeight || 600) / rowHeight) + 30);
      if (start === renderedStart && end === renderedEnd) return; renderedStart = start; renderedEnd = end; canvas.innerHTML = '';
      for (let i = start; i < end; i++) {
        const row = rows[i]; if (mode === 'changes' && row.status === 'equal') continue;
        const node = el('div', { class: 'cmp-vrow cmp-vrow-' + row.status, 'data-hunk': row.hunk >= 0 ? String(row.hunk) : '', style: 'transform:translateY(' + (i * rowHeight) + 'px)' });
        const middle = el('div', { class: 'cmp-merge-cell' });
        if (row.hunk >= 0) {
          if (row.first) {
            middle.append(
              el('button', { class: 'cmp-merge-btn', title: '用左侧替换右侧', 'data-action': 'hunk-to-right', onclick: function () { onMerge && onMerge(row.hunk, 'right'); }, unsafeHtml: icon('right') }),
              el('button', { class: 'cmp-merge-btn', title: '用右侧替换左侧', 'data-action': 'hunk-to-left', onclick: function () { onMerge && onMerge(row.hunk, 'left'); }, unsafeHtml: icon('left') })
            );
          } else {
            middle.append(
              el('button', { class: 'cmp-merge-btn cmp-line-merge', title: '本行复制到右侧', 'data-action': 'line-to-right', onclick: function () { onLine && onLine(row, 'right'); }, unsafeHtml: icon('lineRight') }),
              el('button', { class: 'cmp-merge-btn cmp-line-merge', title: '本行复制到左侧', 'data-action': 'line-to-left', onclick: function () { onLine && onLine(row, 'left'); }, unsafeHtml: icon('lineLeft') })
            );
          }
        }
        node.append(makeDiffCell(row, 'left', onEdit, actions && actions.language && actions.language.left), middle, makeDiffCell(row, 'right', onEdit, actions && actions.language && actions.language.right)); canvas.appendChild(node);
      }
    }
    
    const container = el('div', { class: 'cmp-vdiff-container' });
    const minimap = el('div', { class: 'cmp-vdiff-minimap', title: '点击跳转定位差异' });
    const totalRows = Math.max(1, rows.length);
    (state.hunks || []).forEach(function(hunk, idx) {
      const top = ((hunk.rowIndex || 0) / totalRows) * 100;
      const h = Math.max(2, ((hunk.rowCount || 1) / totalRows) * 100);
      const marker = el('div', {
        class: 'cmp-minimap-marker',
        style: 'top:' + top + '%;height:' + h + '%;',
        title: '差异 #' + (idx + 1)
      });
      minimap.appendChild(marker);
    });
    minimap.addEventListener('click', function(e) {
      const rect = minimap.getBoundingClientRect();
      const ratio = Math.max(0, Math.min(1, (e.clientY - rect.top) / rect.height));
      viewport.scrollTop = Math.floor(ratio * totalRows) * rowHeight;
    });
    viewport.addEventListener('scroll', renderWindow);
    requestAnimationFrame(renderWindow);
    container.append(viewport, minimap);
    return container;

  }
  function makeDiffCell(row, side, onEdit, language) {
    const lineNo = side === 'left' ? row.leftNo : row.rightNo;
    const text = side === 'left' ? row.leftText : row.rightText;
    const other = side === 'left' ? row.rightText : row.leftText;
    const code = el('span', { class: 'cmp-code', spellcheck: 'false' });
    const syntax = Kairo.workbench && Kairo.workbench.syntaxEditor;
    if (syntax && language && row.status !== 'changed') code.innerHTML = syntax.highlight(text || '', language);
    else appendWordDiff(code, text || '', other || '', row.status === 'changed');
    try { code.contentEditable = 'plaintext-only'; } catch (_) { code.contentEditable = 'true'; }
    code.setAttribute('role', 'textbox');
    code.setAttribute('aria-label', (side === 'left' ? '左侧第' : '右侧第') + (lineNo || '新') + '行');
    code.dataset.side = side;
    code.dataset.line = String(lineNo || 0);
    let original = text || '';
    let skip = false;
    code.addEventListener('focus', function () {
      original = text || '';
      code.textContent = original;
    });
    code.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        const next = String(code.textContent || '').replace(/\n/g, '');
        if (!skip && onEdit && next !== original) {
          original = next;
          onEdit(side, lineNo, next, row);
        }
        code.blur();
      }
      if (e.key === 'Escape') { e.preventDefault(); skip = true; code.textContent = original; code.blur(); }
    });
    code.addEventListener('paste', function (e) {
      e.preventDefault();
      const pasted = String((e.clipboardData && e.clipboardData.getData('text/plain')) || '').split(/\r?\n/)[0];
      document.execCommand('insertText', false, pasted);
    });
    code.addEventListener('blur', function () {
      const next = String(code.textContent || '').replace(/\n/g, '');
      if (!skip && onEdit && next !== original) onEdit(side, lineNo, next, row);
      skip = false;
    });
    return el('div', { class: 'cmp-diff-cell cmp-diff-' + side }, [el('span', { class: 'cmp-line-no', text: lineNo ? String(lineNo) : '' }), el('span', { class: 'cmp-line-marker', text: !lineNo ? '' : row.status === 'equal' ? ' ' : side === 'left' ? '−' : '+' }), code]);
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
    path = String(path || '').trim();
    if (!path) return;
    const list = (persisted.folder_history[side] || []).filter(function (item) { return item !== path; });
    list.unshift(path);
    persisted.folder_history[side] = list.slice(0, PATH_HISTORY_LIMIT);
    persistPreference(persisted);
  }
  function fillHistoryList(listEl, side) {
    listEl.innerHTML = '';
    (persisted.folder_history[side] || []).forEach(function (path) {
      listEl.appendChild(el('option', { value: path }));
    });
  }

  function buildFolderWorkbench(panel, state, options, switchTab) {
    const sources = {
      left: Object.assign({ kind: 'local', path: '', label: '左侧文件夹' }, state.left.source && state.left.source.kind !== 'text' ? state.left.source : {}),
      right: Object.assign({ kind: 'local', path: '', label: '右侧文件夹' }, state.right.source && state.right.source.kind !== 'text' ? state.right.source : {})
    };
    if (!sources.left.path) sources.left.path = (persisted.folder_history.left || [])[0] || '';
    if (!sources.right.path) sources.right.path = (persisted.folder_history.right || [])[0] || '';
    const selected = new Set();
    const expanded = new Set();
    const loadedDirs = new Set(['']);
    const loadingDirs = new Set();
    const resultHost = el('div', { class: 'cmp-folder-results' }), progress = el('div', { class: 'cmp-scan-progress', text: '填入或选择两侧目录后开始比较；默认只扫当前这一层，展开文件夹再往下比' });
    const progressBar = el('div', { class: 'cmp-progress-track' }, [el('div', { class: 'cmp-progress-bar' })]);
    const cancelBtn = makeButton('取消', 'close', cancelScan); cancelBtn.disabled = true;
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
    const tolerance = el('select', {}, [el('option', { value: '2', text: '时间容差 2 秒' }), el('option', { value: '60', text: '时间容差 1 分钟' }), el('option', { value: '3600', text: '时间容差 1 小时' })]);
    const statusFilter = el('select', {}, [el('option', { value: 'all', text: '全部状态' }), el('option', { value: 'different', text: '所有差异' }), el('option', { value: 'left_newer', text: '左侧较新' }), el('option', { value: 'right_newer', text: '右侧较新' }), el('option', { value: 'orphans', text: '仅单侧存在' })]);
    const ignoreExt = el('input', { type: 'text', placeholder: '忽略扩展名：log,tmp,bak' }), search = el('input', { type: 'search', placeholder: '筛选路径…' });
    const onlyDiff = makeCheck('只看差异', true, renderScan), sourceGrid = el('div', { class: 'cmp-folder-source-grid' });
    const pathInputs = {};
    ['left', 'right'].forEach(side => {
      const historySelect = el('select', { class: 'cmp-folder-history-select', title: '历史比较路径' });
      const fillSelect = function () {
        historySelect.innerHTML = '';
        historySelect.appendChild(el('option', { value: '', text: '历史' }));
        (persisted.folder_history[side] || []).forEach(function (path) {
          historySelect.appendChild(el('option', { value: path, text: path }));
        });
      };
      fillSelect();
      historySelect.addEventListener('change', function () {
        if (!this.value) return;
        pathInput.value = this.value;
        sources[side].kind = 'local';
        sources[side].path = this.value;
        rememberFolderPath(side, this.value);
        this.value = '';
      });
      const pathInput = el('input', { type: 'text', class: 'cmp-folder-path', value: sources[side].path || '', placeholder: side === 'left' ? '左侧目录绝对路径，可粘贴' : '右侧目录绝对路径，可粘贴' });
      pathInputs[side] = pathInput;
      pathInput.addEventListener('change', function () {
        sources[side].kind = sources[side].kind === 'text' ? 'local' : (sources[side].kind || 'local');
        sources[side].path = pathInput.value.trim();
        rememberFolderPath(side, sources[side].path);
        fillSelect();
      });
      const browse = browseButton({
        input: pathInput,
        directory: true,
        compact: true,
        onPick: function (path) {
          sources[side].kind = 'local';
          sources[side].path = path;
          rememberFolderPath(side, path);
          fillSelect();
        }
      });
      const remote = makeButton('远程…', '', function () {
        openSourceDialog(sources[side], true, function (source) {
          sources[side] = source;
          pathInput.value = source.path || '';
          rememberFolderPath(side, source.path);
          fillSelect();
        });
      });
      sourceGrid.appendChild(el('div', { class: 'cmp-folder-source' }, [
        el('span', { class: 'cmp-source-badge', text: side === 'left' ? '左' : '右' }),
        pathInput,
        historySelect,
        browse,
        remote
      ]));
    });
    // testBtn removed
    const startBtn = makeButton('比对', 'compare', startScan, 'btn btn-primary btn-sm');
    const selectDiffBtn = makeButton('选择全部差异', 'check', selectVisible);
    const coverRightBtn = makeButton('覆盖 →', 'right', () => coverDirection('right'));
    const coverLeftBtn = makeButton('← 覆盖', 'left', () => coverDirection('left'));
    
    startBtn.setAttribute('data-action', 'compare-scan');
    coverRightBtn.setAttribute('data-action', 'cover-right');
    coverLeftBtn.setAttribute('data-action', 'cover-left');
    const searchWrap = el('span', { class: 'cmp-search-wrap' }, [el('span', { class: 'cmp-search-label', text: '🔍 过滤' }), search]);
    search.placeholder = '输入文件名或路径过滤…';
    panel.append(sourceGrid, el('div', { class: 'cmp-wb-toolbar' }, [startBtn, cancelBtn, el('span', { class: 'cmp-toolbar-sep' }), deep, depth, tolerance, ignoreExt, el('span', { class: 'cmp-toolbar-sep' }), selectDiffBtn, coverLeftBtn, coverRightBtn, el('span', { class: 'cmp-toolbar-grow' }), statusFilter, onlyDiff, searchWrap]), progressBar, progress, resultHost); search.addEventListener('input', renderScan); statusFilter.addEventListener('change', renderScan);
    async function testSources() {
      sources.left.path = (pathInputs.left.value || '').trim();
      sources.right.path = (pathInputs.right.value || '').trim();
      if (!sources.left.path || !sources.right.path) { toast('请先填入或选择左右两个目录', 'warn'); return; }
      testBtn.disabled = true; progress.textContent = '正在测试两侧来源…';
      try {
        const result = await api('POST', '/api/compare/test', { left: spec(sources.left), right: spec(sources.right) });
        const sideText = (label, info) => info && info.ok ? label + ' 正常' + (info.is_dir ? '（目录）' : '') : label + ' 失败：' + ((info && info.error) || '未知错误');
        progress.textContent = sideText('左', result.left) + ' · ' + sideText('右', result.right);
        toast(result.ok ? '两侧来源可用' : '来源测试失败', result.ok ? 'ok' : 'err');
      } catch (error) { toast('测试失败：' + (error.message || error), 'err'); }
      finally { testBtn.disabled = false; }
    }
    async function startScan() {
      sources.left.path = (pathInputs.left.value || '').trim();
      sources.right.path = (pathInputs.right.value || '').trim();
      if (!sources.left.path || !sources.right.path) { toast('请先填入或选择左右两个目录', 'warn'); return; }
      rememberFolderPath('left', sources.left.path);
      rememberFolderPath('right', sources.right.path);
      selected.clear(); expanded.clear(); loadedDirs.clear(); loadedDirs.add(''); loadingDirs.clear();
      startBtn.disabled = true; cancelBtn.disabled = false; resultHost.innerHTML = ''; progress.textContent = '正在比较所选目录（不会扫描上级）…';
      const maxDepth = Number(depth.value) || 0;
      persisted.options.max_depth = maxDepth;
      persistPreference(persisted);
      try { const response = await api('POST', '/api/compare/scan', { left: spec(sources.left), right: spec(sources.right), deep: deep.value === 'deep', max_depth: maxDepth, time_tolerance_seconds: Number(tolerance.value) || 2, ignore_exts: ignoreExt.value.split(/[,，\s]+/).filter(Boolean) }); activeScanJob = response.job_id; pollScan(); }
      catch (error) { startBtn.disabled = false; cancelBtn.disabled = true; toast('启动失败：' + (error.message || error), 'err'); }
    }
    async function pollScan() {
      if (!activeScanJob) return;
      try {
        const job = await api('GET', '/api/compare/jobs/' + activeScanJob), percent = job.total ? Math.min(100, Math.round(job.current / job.total * 100)) : 8;
        progressBar.firstChild.style.width = percent + '%'; progress.textContent = (job.message || job.phase) + (job.total ? ' · ' + job.current + ' / ' + job.total : '');
        if (job.status === 'completed') { state.scan = job.result; activeScanJob = ''; startBtn.disabled = false; cancelBtn.disabled = true; progressBar.firstChild.style.width = '100%'; renderScan(); return; }
        if (job.status === 'failed' || job.status === 'cancelled') throw new Error(job.error || '任务已取消');
        scanPollTimer = setTimeout(pollScan, 400);
      } catch (error) { activeScanJob = ''; startBtn.disabled = false; cancelBtn.disabled = true; progress.textContent = '比较未完成'; toast(error.message || String(error), 'err'); }
    }
    async function cancelScan() { if (!activeScanJob) return; try { await api('DELETE', '/api/compare/jobs/' + activeScanJob); } catch (_) {} activeScanJob = ''; cancelBtn.disabled = true; startBtn.disabled = false; progress.textContent = '已取消'; }
    function parentRel(rel) { const i = String(rel || '').lastIndexOf('/'); return i < 0 ? '' : rel.slice(0, i); }
    function baseName(rel) { const i = String(rel || '').lastIndexOf('/'); return i < 0 ? rel : rel.slice(i + 1); }
    function isDirItem(item) { return !!(item && ((item.left && item.left.is_dir) || (item.right && item.right.is_dir))); }
    function mergeScanItems(newItems) {
      const byRel = {};
      (state.scan.items || []).forEach(function (it) { byRel[it.rel_path] = it; });
      (newItems || []).forEach(function (it) { byRel[it.rel_path] = it; });
      state.scan.items = Object.keys(byRel).sort().map(function (key) { return byRel[key]; });
      const summary = { same: 0, different: 0, suspect: 0, left_newer: 0, right_newer: 0, left_only: 0, right_only: 0, errors: 0 };
      state.scan.items.forEach(function (item) {
        if (isDirItem(item)) return;
        if (summary[item.status] !== undefined) summary[item.status]++;
      });
      state.scan.summary = summary;
    }
    function rollupFolder(rel) {
      const kids = (state.scan.items || []).filter(function (it) { return parentRel(it.rel_path) === rel; });
      const folder = (state.scan.items || []).find(function (it) { return it.rel_path === rel; });
      if (!folder || !kids.length) return;
      const pending = kids.some(function (k) { return k.pending && !loadedDirs.has(k.rel_path); });
      const differed = kids.some(function (k) { return k.status && k.status !== 'same' && k.status !== 'pending'; });
      folder.pending = pending;
      folder.status = pending ? 'pending' : (differed ? 'different' : 'same');
    }
    async function toggleFolder(item) {
      const rel = item.rel_path;
      if (expanded.has(rel)) { expanded.delete(rel); renderScan(); return; }
      expanded.add(rel);
      const hasKids = (state.scan.items || []).some(function (it) { return parentRel(it.rel_path) === rel; });
      if (!hasKids && !loadedDirs.has(rel)) {
        loadingDirs.add(rel); renderScan();
        try {
          const result = await api('POST', '/api/compare/scan-level', {
            left: spec(sources.left), right: spec(sources.right), rel_path: rel,
            deep: deep.value === 'deep', time_tolerance_seconds: Number(tolerance.value) || 2,
            ignore_exts: ignoreExt.value.split(/[,，\s]+/).filter(Boolean)
          });
          mergeScanItems(result.items || []);
          loadedDirs.add(rel);
          rollupFolder(rel);
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
      const items = state.scan.items || [];
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
      const query = search.value.trim().toLowerCase();
      const diffOnly = onlyDiff.querySelector('input').checked;
      const filter = statusFilter.value;
      const rows = [];
      function pred(item) {
        if (!scanStatusVisible(item.status, filter) && item.status !== 'pending') return false;
        if (diffOnly && item.status === 'same' && !isDirItem(item)) return false;
        if (query && String(item.rel_path).toLowerCase().indexOf(query) < 0) return false;
        return true;
      }
      function walk(parent, depthValue) {
        (kids[parent] || []).forEach(function (item) {
          const dir = isDirItem(item);
          const open = dir && expanded.has(item.rel_path);
          let show = pred(item);
          if (dir && item.pending && !query) show = true;
          if (dir && diffOnly && item.status === 'same' && loadedDirs.has(item.rel_path) && !open) show = false;
          if (show) rows.push({ item: item, depth: depthValue, dir: dir, open: open, loading: loadingDirs.has(item.rel_path) });
          if (dir && open) walk(item.rel_path, depthValue + 1);
        });
      }
      walk('', 0);
      return rows;
    }
    function renderScan() {
      if (!state.scan) return;
      const rows = flattenFolderRows();
      resultHost.innerHTML = '';
      if (rows.length) resultHost.appendChild(createFolderTree(rows, sources, selected, compareFile, copyItem, updateSelectedStatus, toggleFolder));
      else resultHost.appendChild(el('div', { class: 'cmp-folder-empty', text: onlyDiff.querySelector('input').checked ? '没有差异：两侧目录在当前比较模式下完全一致。' : '当前筛选条件没有匹配项目。' }));
      const s = state.scan.summary || {};
      progress.textContent = '完成 ' + (state.scan.elapsed_ms || 0) + 'ms · 本层/已展开 相同 ' + (s.same || 0) + ' · 不同 ' + (s.different || 0) + ' · 左新 ' + (s.left_newer || 0) + ' · 右新 ' + (s.right_newer || 0) + ' · 仅左 ' + (s.left_only || 0) + ' · 仅右 ' + (s.right_only || 0);
      updateSelectedStatus();
    }
    function visibleItems() {
      return flattenFolderRows().map(function (row) { return row.item; });
    }
    function selectVisible() {
      const candidates = visibleItems().filter(item => item.status !== 'same' && ((item.left && !item.left.is_dir) || (item.right && !item.right.is_dir) || isDirItem(item)));
      const allSelected = candidates.length && candidates.every(item => selected.has(item.rel_path));
      if (!allSelected && candidates.length > 2500) toast('为保证操作流畅，本次先选择前 2500 项，请分批覆盖', 'warn');
      (allSelected ? candidates : candidates.slice(0, 2500)).forEach(item => allSelected ? selected.delete(item.rel_path) : selected.add(item.rel_path));
      renderScan();
    }
    function updateSelectedStatus() {
      selectDiffBtn.textContent = selected.size ? '已选 ' + selected.size + ' 项' : '选择全部差异';
      coverRightBtn.disabled = coverLeftBtn.disabled = !state.scan;
    }
    function coverDirection(direction) {
      if (!state.scan) { toast('请先比对两侧目录', 'warn'); return; }
      if (!selected.size) {
        const fromLeft = direction === 'right';
        (state.scan.items || []).forEach(item => {
          const sourceEntry = fromLeft ? item.left : item.right;
          if (!sourceEntry) return;
          const status = item.status;
          const match = fromLeft
            ? (status === 'left_only' || status === 'left_newer' || status === 'different' || status === 'suspect')
            : (status === 'right_only' || status === 'right_newer' || status === 'different' || status === 'suspect');
          if (match) selected.add(item.rel_path);
        });
        if (!selected.size) { toast('该方向没有可覆盖的更新项或单侧文件', 'warn'); renderScan(); return; }
        renderScan();
      }
      syncSelected(direction);
    }
    async function syncSelected(direction) {
      if (!state.scan || !selected.size) return;
      if (selected.size > 2500) { toast('单次最多预览并覆盖 2500 项，请分批操作', 'warn'); return; }
      const fromLeft = direction === 'right', plan = (state.scan.items || []).filter(item => selected.has(item.rel_path)).filter(item => {
        const sourceEntry = fromLeft ? item.left : item.right;
        return !!sourceEntry;
      }).map(item => {
        const sourceEntry = fromLeft ? item.left : item.right, targetEntry = fromLeft ? item.right : item.left;
        return { item, sourceEntry, targetEntry, enabled: true, request: { rel_path: item.rel_path, expected: targetEntry ? { size: targetEntry.size, mtime: targetEntry.mtime } : null } };
      });
      if (!plan.length) { toast('所选项目在该方向没有可复制的源文件', 'warn'); return; }
      showSyncPreview(direction, plan);
    }
    function showSyncPreview(direction, plan) {
      const overlay = el('div', { class: 'cmp-source-overlay' }), card = el('div', { class: 'cmp-source-dialog cmp-sync-dialog', role: 'dialog', 'aria-modal': 'true' });
      let jobID = '', pollTimer = 0;
      const body = el('div', { class: 'cmp-source-body cmp-sync-body' }), actions = el('div', { class: 'cmp-source-actions' });
      const close = async () => { if (pollTimer) clearTimeout(pollTimer); if (jobID) { try { await api('DELETE', '/api/compare/jobs/' + jobID); } catch (_) {} } overlay.remove(); updateSelectedStatus(); };
      const closeBtn = el('button', { class: 'cmp-dialog-close', onclick: close, unsafeHtml: icon('close') });
      const title = el('strong', { text: '覆盖预览 · ' + (direction === 'right' ? '左 → 右（更新 + 仅左，不删除右侧多余）' : '右 → 左（更新 + 仅右，不删除左侧多余）') });
      const summary = el('div', { class: 'cmp-sync-summary' });
      const list = el('div', { class: 'cmp-sync-list' });
      function updateSummary() {
        const enabled = plan.filter(row => row.enabled), totalBytes = enabled.reduce((sum, row) => sum + (row.sourceEntry.size || 0), 0);
        summary.textContent = '计划 ' + enabled.length + ' 项 · ' + formatBytes(totalBytes) + ' · ' + (options.backup ? '覆盖前备份' : '不创建备份');
        start.disabled = enabled.length === 0;
      }
      plan.forEach(row => {
        const input = el('input', { type: 'checkbox' }); input.checked = true; input.onchange = () => { row.enabled = input.checked; updateSummary(); };
        list.appendChild(el('label', { class: 'cmp-sync-row' }, [input, el('span', { class: 'cmp-sync-action', text: row.targetEntry ? '覆盖' : '新增' }), el('span', { class: 'cmp-sync-path', text: row.item.rel_path, title: row.item.rel_path }), el('small', { text: formatBytes(row.sourceEntry.size) })]));
      });
      const cancel = makeButton('取消', '', close), start = makeButton('开始覆盖', 'right', startSync, 'btn btn-primary');
      actions.append(cancel, start); body.append(summary, list); card.append(el('div', { class: 'cmp-source-title' }, [title, closeBtn]), body, actions); overlay.appendChild(card); document.body.appendChild(overlay); updateSummary();
      async function startSync() {
        const items = plan.filter(row => row.enabled).map(row => row.request); if (!items.length) return;
        start.disabled = true; cancel.textContent = '取消任务'; list.innerHTML = '';
        const track = el('div', { class: 'cmp-progress-track cmp-sync-track' }, [el('div', { class: 'cmp-progress-bar' })]), message = el('div', { class: 'cmp-sync-progress', text: '正在创建同步任务…' }); body.innerHTML = ''; body.append(summary, track, message);
        try {
          const response = await api('POST', '/api/compare/sync/start', { left: spec(sources.left), right: spec(sources.right), direction, items, backup: !!options.backup }); jobID = response.job_id; poll();
        } catch (error) { message.textContent = '启动失败：' + (error.message || error); start.disabled = false; }
        async function poll() {
          if (!jobID) return;
          try {
            const job = await api('GET', '/api/compare/jobs/' + jobID), percent = job.total ? Math.round(job.current / job.total * 100) : 5;
            track.firstChild.style.width = Math.min(100, percent) + '%'; message.textContent = (job.message || job.phase) + (job.total ? ' · ' + job.current + ' / ' + job.total : '');
            if (job.status === 'completed') { jobID = ''; const result = job.sync_result || {}; selected.clear(); toast('覆盖完成：成功 ' + (result.copied || 0) + '，失败 ' + (result.failed || 0), result.failed ? 'warn' : 'ok'); if (result.failed) { renderFailures(result); } else { overlay.remove(); await startScan(); } return; }
            if (job.status === 'failed' || job.status === 'cancelled') throw new Error(job.error || '任务已取消');
            pollTimer = setTimeout(poll, 350);
          } catch (error) { jobID = ''; message.textContent = error.message || String(error); cancel.textContent = '关闭'; }
        }
      }
      function renderFailures(result) {
        body.innerHTML = ''; body.appendChild(el('div', { class: 'cmp-sync-summary', text: '成功 ' + (result.copied || 0) + ' 项，失败 ' + (result.failed || 0) + ' 项' }));
        const failures = el('div', { class: 'cmp-sync-list' }); (result.failures || []).forEach(item => failures.appendChild(el('div', { class: 'cmp-sync-row failed' }, [el('span', { class: 'cmp-sync-action', text: '失败' }), el('span', { class: 'cmp-sync-path', text: item.rel_path }), el('small', { text: item.error })]))); body.appendChild(failures);
        actions.innerHTML = ''; actions.appendChild(makeButton('关闭并重新扫描', '', async () => { overlay.remove(); await startScan(); }, 'btn btn-primary'));
      }
    }
    async function compareFile(item) {
      if (!item.left || !item.right || item.left.is_dir || item.right.is_dir) return;
      const left = Object.assign({}, sources.left, { path: item.left.path, label: item.rel_path }), right = Object.assign({}, sources.right, { path: item.right.path, label: item.rel_path });
      state.left.source = left; state.right.source = right; switchTab('text'); await state.textWorkbench.loadPair(left, right);
    }
    async function copyItem(item, direction) {
      const fromLeft = direction === 'right', sourceBase = fromLeft ? sources.left : sources.right, targetBase = fromLeft ? sources.right : sources.left;
      const sourceEntry = fromLeft ? item.left : item.right, targetEntry = fromLeft ? item.right : item.left; if (!sourceEntry) return;
      if (sourceEntry.is_dir) {
        try {
          const result = await api('POST', '/api/compare/sync', { left: spec(sources.left), right: spec(sources.right), direction: direction, items: [{ rel_path: item.rel_path, expected: targetEntry ? { size: targetEntry.size, mtime: targetEntry.mtime } : null }], backup: !!options.backup });
          toast('已复制目录：' + item.rel_path + '（' + (result.copied || 0) + ' 个文件）', result.failed ? 'warn' : 'ok');
          startScan();
        } catch (error) { toast('复制失败：' + (error.message || error), 'err'); }
        return;
      }
      const source = Object.assign({}, sourceBase, { path: sourceEntry.path }), target = Object.assign({}, targetBase, { path: targetEntry ? targetEntry.path : joinPath(targetBase, targetBase.path, item.rel_path) });
      try { await api('POST', '/api/compare/copy', { source: spec(source), target: spec(target), expected: targetEntry ? { size: targetEntry.size, mtime: targetEntry.mtime } : null, backup: !!options.backup }); toast('已复制：' + item.rel_path, 'ok'); startScan(); }
      catch (error) { toast('复制失败：' + (error.message || error), 'err'); }
    }
  }

  function createFolderTree(rows, sources, selected, onCompare, onCopy, onSelectionChange, onToggle) {
    const header = el('div', { class: 'cmp-folder-table-head cmp-folder-tree-head' }, [el('span', { text: '' }), el('span', { text: '选择 / 状态' }), el('span', { text: sourceLabel(sources.left) }), el('span', { text: '操作' }), el('span', { text: sourceLabel(sources.right) })]);
    const viewport = el('div', { class: 'cmp-folder-viewport' }), canvas = el('div', { class: 'cmp-folder-canvas' }), height = 34;
    canvas.style.height = Math.max(1, rows.length * height) + 'px'; viewport.appendChild(canvas); let oldStart = -1, oldEnd = -1;
    function render() {
      const start = Math.max(0, Math.floor(viewport.scrollTop / height) - 10), end = Math.min(rows.length, start + Math.ceil((viewport.clientHeight || 540) / height) + 20);
      if (start === oldStart && end === oldEnd) return; oldStart = start; oldEnd = end; canvas.innerHTML = '';
      for (let i = start; i < end; i++) {
        const row = rows[i], item = row.item;
        const node = el('div', {
          class: 'cmp-folder-row cmp-folder-tree-row status-' + item.status,
          style: 'transform:translateY(' + (i * height) + 'px); cursor:pointer;'
        });
        if (row.dir) {
          node.ondblclick = function() { onToggle(item); };
        } else if (item.left && item.right) {
          node.ondblclick = function() { onCompare(item); };
          node.title = '双击比对文件差异';
        }
        const status = { same: '相同', different: '不同', suspect: '待校验', pending: '未展开', left_newer: '左侧较新', right_newer: '右侧较新', left_only: '仅左', right_only: '仅右', error: '错误' }[item.status] || item.status;
        const ops = el('span', { class: 'cmp-folder-ops' });
        // '查看' button removed in favor of double clicking row
        if (item.left && !item.left.is_dir) ops.appendChild(el('button', { title: '复制到右侧', onclick: function () { onCopy(item, 'right'); }, unsafeHtml: icon('right') }));
        if (item.right && !item.right.is_dir) ops.appendChild(el('button', { title: '复制到左侧', onclick: function () { onCopy(item, 'left'); }, unsafeHtml: icon('left') }));
        if (row.dir) {
          if (item.left && item.left.is_dir) ops.appendChild(el('button', { title: '复制目录到右侧', onclick: function () { onCopy(item, 'right'); }, unsafeHtml: icon('right') }));
          if (item.right && item.right.is_dir) ops.appendChild(el('button', { title: '复制目录到左侧', onclick: function () { onCopy(item, 'left'); }, unsafeHtml: icon('left') }));
        }
        const checkbox = el('input', { type: 'checkbox' });
        checkbox.checked = selected.has(item.rel_path);
        checkbox.disabled = item.status === 'same';
        checkbox.onchange = function () { checkbox.checked ? selected.add(item.rel_path) : selected.delete(item.rel_path); onSelectionChange(); };
        const twist = el('button', { class: 'cmp-tree-twist', 'data-action': row.dir ? 'expand-folder' : undefined, title: row.dir ? (row.open ? '折叠' : '展开后比较内容') : '', disabled: !row.dir });
        if (row.dir) {
          twist.innerHTML = icon(row.open ? 'collapse' : 'expand');
          if (row.loading) twist.classList.add('busy');
          twist.onclick = function (ev) { ev.stopPropagation(); onToggle(item); };
        }
        const pad = { style: 'padding-left:' + (8 + row.depth * 16) + 'px' };
        node.append(
          twist,
          el('span', { class: 'cmp-folder-status' }, [checkbox, statusBadge]),
          folderCell(item.left, item.rel_path, pad),
          ops,
          folderCell(item.right, item.rel_path, pad)
        );
        canvas.appendChild(node);
      }
    }
    viewport.addEventListener('scroll', render); requestAnimationFrame(render); return el('div', { class: 'cmp-folder-table' }, [header, viewport]);
  }
  function scanStatusVisible(status, filter) {
    if (!filter || filter === 'all') return true;
    if (filter === 'different') return status !== 'same';
    if (filter === 'orphans') return status === 'left_only' || status === 'right_only';
    return status === filter;
  }
  function folderCell(entry, rel, pad) {
    if (!entry) return el('span', { class: 'cmp-folder-cell missing', text: '—' });
    const name = (function () { const i = String(rel || '').lastIndexOf('/'); return i < 0 ? rel : rel.slice(i + 1); })();
    const attrs = { class: 'cmp-folder-cell', title: entry.path };
    if (pad && pad.style) attrs.style = pad.style;
    const typeIcon = el('span', { class: 'cmp-entry-icon', text: entry.is_dir ? '📁 ' : '📄 ' });
    return el('span', attrs, [typeIcon, el('span', { class: 'cmp-folder-name', text: name }), el('small', { text: entry.is_dir ? '目录' : formatBytes(entry.size) })]);
  }

  async function ensureConnections() { if (connections.length) return connections; try { const response = await api('GET', '/api/compare/connections'); connections = response.sftp || []; } catch (_) { connections = []; } return connections; }
  async function openSourceDialog(current, directory, onSelect) {
    await ensureConnections(); const source = Object.assign(defaultSource('left'), current || {}); if (directory && source.kind === 'text') source.kind = 'local';
    const body = el('div', { class: 'cmp-source-body' });
    const footer = el('div', { class: 'cmp-source-actions' });
    const dlg = Kairo.overlays.modal({ title: directory ? '选择比较目录' : '打开比较文件', width: 620, body: body, footer: footer });
    const choices = []; if (!directory) choices.push(el('option', { value: 'text', text: '临时文本' })); choices.push(el('option', { value: 'local', text: '本地' }), el('option', { value: 'sftp', text: 'SFTP / SSH' }), el('option', { value: 'ftp', text: 'FTP / FTPS' }));
    const kind = el('select', {}, choices); kind.value = source.kind;
    const pathInput = el('input', { type: 'text', value: source.path || '', placeholder: directory ? '目录绝对路径，可粘贴' : '文件绝对路径' });
    const connection = el('select', {}, [el('option', { value: '', text: '选择已配置的 SSH 服务器' })]);
    connections.forEach((item, index) => connection.appendChild(el('option', { value: String(index), text: (item.System || item.system) + ' / ' + (item.Name || item.name) + ' (' + (item.Host || item.host) + ')' })));
    const host = el('input', { type: 'text', value: source.host || '', placeholder: 'FTP 主机' }), port = el('input', { type: 'number', value: source.port || 21, min: 1, max: 65535 });
    const username = el('input', { type: 'text', value: source.username || '', placeholder: '用户名' }), password = el('input', { type: 'password', value: '', placeholder: '密码（不会保存到浏览器）' });
    const tlsMode = el('select', {}, [el('option', { value: '', text: '普通 FTP' }), el('option', { value: 'explicit', text: '显式 FTPS' }), el('option', { value: 'implicit', text: '隐式 FTPS' })]); tlsMode.value = source.tls_mode || '';
    const encoding = el('select', {}, [el('option', { value: 'auto', text: '自动检测' }), el('option', { value: 'utf-8', text: 'UTF-8' }), el('option', { value: 'gb18030', text: 'GBK / GB18030' }), el('option', { value: 'utf-16le', text: 'UTF-16 LE' }), el('option', { value: 'utf-16be', text: 'UTF-16 BE' })]); encoding.value = source.encoding || 'auto';
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
    const warning = el('div', { class: 'cmp-source-warning', text: '普通 FTP 会明文传输凭据，生产环境建议使用 SFTP 或 FTPS。' });
    const close = () => dlg.close();
    const confirm = makeButton('确定', 'open', () => {
      source.kind = kind.value; source.path = pathInput.value.trim(); source.password = password.value; source.encoding = encoding.value;
      if (source.kind === 'sftp') { const item = connections[Number(connection.value)]; if (!item) { toast('请选择 SFTP 服务器', 'warn'); return; } source.system = item.System || item.system; source.server = item.Name || item.name; source.username = username.value || item.Username || item.username || ''; }
      if (source.kind === 'ftp') { source.host = host.value.trim(); source.port = Number(port.value) || 21; source.username = username.value; source.tls_mode = tlsMode.value; }
      if (source.kind !== 'text' && !source.path) { toast('请输入路径', 'warn'); return; } close(); onSelect(source);
    }, 'btn btn-primary');
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
})();
