/* ===== web/pages/waspack.js =====
 * WAS/WAR release packaging workbench.
 *
 * The page deliberately keeps packaging history and benign form preferences
 * separate. Output policy is per-operation and always starts at fail.
 */
(function () {
  'use strict';

  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const core = Kairo.core || {};
  const apiNs = Kairo.api || {};
  const el = core.el;
  const toast = core.toast || function () {};
  const copyToClipboard = core.copyToClipboard || function () { return Promise.reject(new Error('复制不可用')); };
  const confirmDialog = core.confirmDialog || function () { return Promise.resolve(false); };
  const api = apiNs.api;
  const getPreference = apiNs.getPreference;
  const preferenceSaver = apiNs.preferenceSaver;
  const pathRow = apiNs.pathRow;

  const SESSION_DRAFT_KEY = 'kairo:waspack:session-draft';
  const HANDOFF_PREFIX = 'kairo:waspack:compare-handoff:';
  const SAMPLE_APP = [
    './CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp',
    './CreditManage/CreditLine/ProductInfo.jsp',
    './src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java',
    './WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class'
  ].join('\n');
  const SAMPLE_BATCH = [
    './amargci/credit_2nd.sh', './amargci/newcbsdata.sh', './amargci/etc/gci_task_2nd.xml',
    './AmarExtract/etc/ext_metadata.xml', './AmarExtract/etc/ext_credit_task_ql.xml',
    './AmarExtract/etc/ext_credit_are_ql.xml', './AmarExtract/src/jsyh/CustomerSpecialInfo.java',
    './AmarExtract/classes/jsyh/CustomerSpecialInfo.class', './AmarExtract/get_customerspecialinfo.sh',
    './AmarExtract/run_ql.sh', './AmarExtract/src/jsyh/UpdateExchangeRate.java',
    './AmarExtract/classes/jsyh/UpdateExchangeRate.class'
  ].join('\n');

  function todayStamp() {
    const d = new Date();
    const pad = n => n < 10 ? '0' + n : String(n);
    return d.getFullYear() + pad(d.getMonth() + 1) + pad(d.getDate());
  }
  function fmtBytes(value) {
    let n = Number(value) || 0;
    if (n < 1024) return n + ' B';
    if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1073741824) return (n / 1048576).toFixed(2) + ' MB';
    return (n / 1073741824).toFixed(2) + ' GB';
  }
  function normalizeManifest(value) { return String(value == null ? '' : value).replace(/\r\n?/g, '\n'); }
  function duplicateManifestMessage(value) {
    const seen = new Map();
    const lines = normalizeManifest(value).split('\n');
    for (let i = 0; i < lines.length; i++) {
      const line = String(lines[i] || '').trim();
      if (!line || line.startsWith('#')) continue;
      const key = line.replace(/\\/g, '/').replace(/^\.\/+/, '').toLowerCase();
      const prior = seen.get(key);
      if (prior != null) return '第 ' + (i + 1) + ' 行与第 ' + (prior + 1) + ' 行重复。';
      seen.set(key, i);
    }
    return '';
  }
  function packageBaseName(value) { return String(value || '').trim().replace(/\.(tar|zip|sh)$/i, ''); }
  function kindLabel(kind) { return ({ java: 'Java', class: 'Class', jsp: 'JSP' })[kind] || '其他'; }
  function normalizeBuildResponse(response) {
    if (response && response.build && typeof response.build === 'object') {
      return { build: response.build, zip: response.zip && typeof response.zip === 'object' ? response.zip : null, partial: response.ok === false, error: response.error || '' };
    }
    return { build: response && typeof response === 'object' ? response : null, zip: null, partial: false, error: '' };
  }
  function canonicalStageSignature(body) {
    body = body || {};
    return JSON.stringify({
      project_dir: String(body.project_dir || '').trim(), output_dir: String(body.output_dir || '').trim(),
      package_name: packageBaseName(body.package_name), manifest: normalizeManifest(body.manifest),
      auto_pair: body.auto_pair !== false, pack_type: String(body.pack_type || 'app').trim().toLowerCase() || 'app',
      batch_base_dir: String(body.batch_base_dir || '').trim(), chmod_mode: String(body.chmod_mode || '').trim()
    });
  }
  function windowsPathKey(value) {
    let path = String(value || '').trim().replace(/\//g, '\\').replace(/\\{2,}/g, '\\');
    if (/^[A-Za-z]:\\$/.test(path)) return path.toLowerCase();
    return path.replace(/\\+$/, '').toLowerCase();
  }
  function sameWindowsPath(left, right) { const a = windowsPathKey(left), b = windowsPathKey(right); return !!a && !!b && a === b; }
  function comparableHistoryItem(item) { const status = String(item && item.status || '').toLowerCase(), operation = String(item && item.operation || '').toLowerCase(); return !!(item && status === 'success' && ['build', 'package', 'rebuild'].indexOf(operation) >= 0); }
  function operationLabel(value) { return ({ build: '预检并生成', extract: '抽取 WAR', package: '打包 WAR', zip: '生成 ZIP', rebuild: '历史重建' })[String(value || '').toLowerCase()] || '投产操作'; }
  function statusLabel(value) { return String(value || '').toLowerCase() === 'success' ? '成功' : '失败'; }
  function previousComparable(items, index) {
    items = Array.isArray(items) ? items : [];
    for (let i = Number(index) + 1; i < items.length; i++) if (comparableHistoryItem(items[i])) return items[i];
    return null;
  }
  function errorText(error) { return error && error.message ? String(error.message) : String(error || '操作失败'); }
  function readDraft() {
    try { const raw = sessionStorage.getItem(SESSION_DRAFT_KEY); return raw ? JSON.parse(raw) || {} : {}; } catch (_) { return {}; }
  }
  function makeHandoffPayload(previousPath, currentPath, createdAt) {
    return { left: { kind: 'local', path: String(previousPath || '') }, right: { kind: 'local', path: String(currentPath || '') }, created_at: createdAt || new Date().toISOString(), source: 'waspack-history' };
  }
  function createHandoff(previousPath, currentPath) {
    const nonce = 'wp-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
    return { nonce: nonce, payload: makeHandoffPayload(previousPath, currentPath) };
  }
  function handoffTargetURL(locationLike, nonce) {
    const location = locationLike || window.location || {};
    const origin = String(location.origin || ((location.protocol && location.host) ? location.protocol + '//' + location.host : '')).replace(/\/$/, '');
    if (!origin) throw new Error('无法确定比较工作台的同源地址。');
    return origin + (String(location.pathname || '/') || '/') + String(location.search || '') + '#/compare?handoff=' + encodeURIComponent(String(nonce || ''));
  }

  async function renderWASPack(view) {
    const renderToken = view.dataset.renderToken;
    view.replaceChildren(el('div', { class: 'card muted', text: '正在恢复投产打包设置…' }));
    let saved = {};
    try { const remote = await getPreference('waspack'); saved = remote && remote.value && typeof remote.value === 'object' ? remote.value : {}; } catch (_) { saved = {}; }
    if (view.dataset.renderToken !== renderToken) return;
    const legacyDraft = readDraft(), currentSavedType = saved.pack_type === 'batch' ? 'batch' : 'app';
    const state = { type: legacyDraft.pack_type === 'batch' || legacyDraft.pack_type === 'app' ? legacyDraft.pack_type : currentSavedType, stageToken: '', stageSignature: '', stageStale: false, preview: null, result: null, busy: false, disposed: false, previewLimit: 120, history: { items: [], loaded: false, loading: false, error: '', filter: '' } };
    const draft = { app: Object.assign({}, legacyDraft.app || {}), batch: Object.assign({}, legacyDraft.batch || {}) };
    if (legacyDraft.package_name || legacyDraft.manifest) draft[state.type] = { package_name: legacyDraft.package_name || '', manifest: legacyDraft.manifest || '' };
    const prefSaver = preferenceSaver('waspack', 450);
    let draftTimer = 0, lineTimer = 0, operationSeq = 0, historySeq = 0;
    function projectOf(type) { return saved[type === 'batch' ? 'project_dir_batch' : 'project_dir_app'] || (type === currentSavedType ? saved.project_dir || '' : ''); }
    function outputOf(type) { return saved[type === 'batch' ? 'output_dir_batch' : 'output_dir_app'] || (type === currentSavedType ? saved.output_dir || '' : ''); }
    function historyOf(type) { const key = type === 'batch' ? 'project_dir_history_batch' : 'project_dir_history_app'; return Array.isArray(saved[key]) ? saved[key].slice() : []; }
    function savePreferences() {
      const type = state.type; saved[type === 'batch' ? 'project_dir_batch' : 'project_dir_app'] = projectInput.value; saved[type === 'batch' ? 'output_dir_batch' : 'output_dir_app'] = outputInput.value;
      prefSaver({ project_dir: projectInput.value, output_dir: outputInput.value, project_dir_app: saved.project_dir_app || '', project_dir_batch: saved.project_dir_batch || '', output_dir_app: saved.output_dir_app || '', output_dir_batch: saved.output_dir_batch || '', project_dir_history_app: historyOf('app'), project_dir_history_batch: historyOf('batch'), auto_pair: autoPair.checked, pack_type: state.type, batch_base_dir: batchBaseInput.value, chmod_mode: chmodInput.value });
    }
    function saveDraft() {
      draft[state.type] = { package_name: packageInput.value, manifest: manifestInput.value }; clearTimeout(draftTimer);
      draftTimer = setTimeout(function () { try { sessionStorage.setItem(SESSION_DRAFT_KEY, JSON.stringify({ pack_type: state.type, app: draft.app, batch: draft.batch })); } catch (_) {} }, 320);
    }
    function rememberProject(value) { const path = String(value || '').trim(); if (!path) return; const key = state.type === 'batch' ? 'project_dir_history_batch' : 'project_dir_history_app'; const list = historyOf(state.type).filter(item => item !== path); list.unshift(path); saved[key] = list.slice(0, 12); savePreferences(); fillHistory(); }

    const appRadio = el('input', { type: 'radio', name: 'waspack-type', value: 'app', id: 'waspack-type-app' });
    const batchRadio = el('input', { type: 'radio', name: 'waspack-type', value: 'batch', id: 'waspack-type-batch' });
    const projectInput = el('input', { type: 'text', id: 'waspack-project', spellcheck: 'false', autocomplete: 'off', required: 'true' });
    const outputInput = el('input', { type: 'text', id: 'waspack-output', spellcheck: 'false', autocomplete: 'off', required: 'true' });
    const packageInput = el('input', { type: 'text', id: 'waspack-package', spellcheck: 'false', autocomplete: 'off', required: 'true' });
    const manifestInput = el('textarea', { id: 'waspack-manifest', spellcheck: 'false', autocomplete: 'off', required: 'true' });
    const autoPair = el('input', { type: 'checkbox', id: 'waspack-autopair' });
    const includeZipInput = el('input', { type: 'checkbox', id: 'waspack-include-zip' });
    const batchBaseInput = el('input', { type: 'text', id: 'waspack-batch-base', spellcheck: 'false', autocomplete: 'off' });
    const chmodInput = el('input', { type: 'text', id: 'waspack-chmod', spellcheck: 'false', autocomplete: 'off', inputmode: 'numeric' });
    const policyInput = el('select', { id: 'waspack-output-policy', 'aria-describedby': 'waspack-policy-help' }, [el('option', { value: 'fail', text: '严格失败：目标必须为空（推荐）' }), el('option', { value: 'clean_kairo_artifacts', text: '清理 Kairo 已知产物，保留其他文件' }), el('option', { value: 'replace', text: '覆盖：清空目标目录内全部文件' })]);
    policyInput.value = 'fail';
    projectInput.value = projectOf(state.type); outputInput.value = outputOf(state.type);
    const initialDraft = draft[state.type] || {};
    packageInput.value = String(initialDraft.package_name || '').trim() || (state.type === 'batch' ? 'DDD' : 'TT') + todayStamp() + 'qijunV1'; manifestInput.value = initialDraft.manifest || '';
    autoPair.checked = saved.auto_pair !== false; batchBaseInput.value = saved.batch_base_dir || '/batch/credit'; chmodInput.value = saved.chmod_mode || '777';
    const projectHistory = el('select', { class: 'waspack-history-select', 'aria-label': '工程路径历史' });
    const tarHint = el('span', { class: 'waspack-tar-hint' }), lineCount = el('span', { class: 'waspack-line-count', role: 'status', 'aria-live': 'polite' });
    const policyHelp = el('span', { class: 'waspack-help', id: 'waspack-policy-help', text: '策略只应用于本次操作，不写入偏好。覆盖会删除目标目录中所有文件。' });
    const stageBadge = el('span', { class: 'waspack-stage-badge is-empty', text: '未抽取' });
    const phase = el('div', { class: 'waspack-phase', role: 'status', 'aria-live': 'polite', text: '填写配置后先预检；预检发现缺失时不会生成产物。' });
    const alert = el('div', { class: 'waspack-alert', role: 'alert', hidden: true });
    const progress = el('div', { class: 'waspack-progress', hidden: true, role: 'progressbar', 'aria-label': '投产打包进度' });
    const previewPanel = el('div', { class: 'waspack-panel', role: 'tabpanel' }), resultPanel = el('div', { class: 'waspack-panel', role: 'tabpanel', hidden: true }), historyPanel = el('div', { class: 'waspack-panel waspack-placeholder-history', role: 'tabpanel', hidden: true });
    const workspaceTitle = el('h2', { text: '工作区' });
    const tabPreview = el('button', { class: 'waspack-tab is-active', type: 'button', role: 'tab', 'aria-selected': 'true', text: '预检' }), tabResult = el('button', { class: 'waspack-tab', type: 'button', role: 'tab', 'aria-selected': 'false', text: '结果' }), tabHistory = el('button', { class: 'waspack-tab', type: 'button', role: 'tab', 'aria-selected': 'false', text: '历史' });
    function field(label, input, help, extraClass) { const error = el('span', { class: 'waspack-error', hidden: true }); const labelNode = el('span', { class: 'waspack-field-label', text: label }); const wrap = el('label', { class: 'waspack-field' + (extraClass ? ' ' + extraClass : '') }, [labelNode, input]); if (help) wrap.appendChild(help); wrap.appendChild(error); wrap._error = error; wrap._input = input; return wrap; }
    function pathField(label, input, onPick, type) { const row = pathRow(input, { directory: true, rowClass: 'path-field waspack-path', onPick: function (path) { if (onPick) onPick(path); } }); const wrap = field(label, row, null); wrap._input = input; wrap._row = row; wrap._type = type; return wrap; }
    function fillHistory() { projectHistory.replaceChildren(el('option', { value: '', text: historyOf(state.type).length ? '选择历史工程路径…' : '暂无工程路径历史' })); historyOf(state.type).forEach(item => projectHistory.appendChild(el('option', { value: item, text: item }))); projectHistory.disabled = !historyOf(state.type).length; }
    fillHistory(); projectHistory.addEventListener('change', function () { if (this.value) { projectInput.value = this.value; markChanged(); } this.value = ''; });
    function packageHint() { const name = packageBaseName(packageInput.value) || '包名'; tarHint.textContent = name + '.tar · ' + name + '.sh · Bak' + name + '.sh · list.txt' + (state.type === 'batch' ? ' · chmod.txt' : '') + '；可选 ' + name + '.zip'; }
    function updateLineCount() { const lines = normalizeManifest(manifestInput.value).split('\n').filter(line => line.trim()).length; lineCount.textContent = lines + ' 行清单 · ' + manifestInput.value.length + ' 字符'; }
    function syncTypeUI() { const batch = state.type === 'batch'; appRadio.checked = !batch; batchRadio.checked = batch; appRadio.parentNode.classList.toggle('is-checked', !batch); batchRadio.parentNode.classList.toggle('is-checked', batch); batchBaseField.classList.toggle('is-visible', batch); chmodField.classList.toggle('is-visible', batch); projectInput.placeholder = batch ? '批量工程根目录，例如 D:\\projects\\batch' : 'WAS 工程根目录，例如 C:\\ideaSpaces\\credit'; outputInput.placeholder = '输出目录，例如 D:\\packs\\' + (state.type === 'batch' ? 'DDD' : 'TT') + todayStamp(); manifestInput.placeholder = state.type === 'batch' ? SAMPLE_BATCH : SAMPLE_APP; packageHint(); }
    function setFieldError(wrap, message) { wrap.classList.toggle('is-invalid', !!message); wrap._error.hidden = !message; wrap._error.textContent = message || ''; if (message) wrap._input.setAttribute('aria-invalid', 'true'); else wrap._input.removeAttribute('aria-invalid'); }
    function validate() { const body = payload(); let valid = true; setFieldError(projectField, body.project_dir ? '' : '必须填写工程根目录。'); setFieldError(outputField, body.output_dir ? '' : '必须填写输出目录。'); setFieldError(packageField, body.package_name && /^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$/.test(body.package_name) ? '' : '包名需以字母或数字开头，仅允许字母、数字、点、下划线和短横线。'); const manifestError = !body.manifest.trim() ? '请粘贴至少一行投产清单。' : duplicateManifestMessage(body.manifest); setFieldError(manifestField, manifestError); if (state.type === 'batch') setFieldError(batchBaseField, body.batch_base_dir ? '' : '批量模式需要部署根路径。'); if (state.type === 'batch') setFieldError(chmodField, /^(?:[0-7]{3,4})$/.test(body.chmod_mode) ? '' : '权限模式需为 3 或 4 位八进制数字，例如 777。'); [projectField, outputField, packageField, manifestField].forEach(w => { if (!w._error.hidden) valid = false; }); if (state.type === 'batch' && (!batchBaseField._error.hidden || !chmodField._error.hidden)) valid = false; return valid; }
    function validateZip() { const body = payload(); const pkgValid = !!body.package_name && /^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$/.test(body.package_name); setFieldError(outputField, body.output_dir ? '' : '必须填写输出目录。'); setFieldError(packageField, pkgValid ? '' : '包名需以字母或数字开头，仅允许字母、数字、点、下划线和短横线。'); return !!body.output_dir && pkgValid; }
    function payload() { return { project_dir: projectInput.value.trim(), output_dir: outputInput.value.trim(), package_name: packageBaseName(packageInput.value), manifest: normalizeManifest(manifestInput.value), auto_pair: !!autoPair.checked, output_policy: policyInput.value || 'fail', pack_type: state.type, batch_base_dir: batchBaseInput.value.trim(), chmod_mode: chmodInput.value.trim(), include_zip: false }; }
    function clearAlert() { alert.hidden = true; alert.className = 'waspack-alert'; alert.textContent = ''; }
    function showAlert(message, type) { alert.hidden = false; alert.className = 'waspack-alert' + (type ? ' is-' + type : ''); alert.textContent = String(message || ''); }
    function markChanged() { savePreferences(); saveDraft(); packageHint(); updateLineCount(); if (state.stageToken && state.stageSignature !== canonicalStageSignature(payload())) { state.stageStale = true; stageBadge.className = 'waspack-stage-badge is-stale'; stageBadge.textContent = '抽取已过期，请重新抽取'; } updateActionAvailability(); }
    function setStage(token) { state.stageToken = String(token || ''); state.stageSignature = canonicalStageSignature(payload()); state.stageStale = !state.stageToken; stageBadge.className = 'waspack-stage-badge ' + (state.stageToken ? 'is-ready' : 'is-empty'); stageBadge.textContent = state.stageToken ? 'WAR 已抽取，可打包' : '未抽取'; updateActionAvailability(); }
    function updateActionAvailability() { const complete = !!projectInput.value.trim() && !!outputInput.value.trim() && !!packageBaseName(packageInput.value) && !!manifestInput.value.trim(); buildButton.disabled = !complete || state.busy; packageButton.disabled = !state.stageToken || state.stageStale || !complete || state.busy; extractButton.disabled = !complete || state.busy; zipButton.disabled = !outputInput.value.trim() || !packageBaseName(packageInput.value) || state.busy; openButton.disabled = !outputInput.value.trim() || state.busy; previewButton.disabled = !complete || state.busy; }
    const tabPairs = [[tabPreview, previewPanel], [tabResult, resultPanel], [tabHistory, historyPanel]];
    const tabPrefix = 'waspack-' + Date.now().toString(36);
    tabPairs.forEach(function (pair, index) { const tabId = tabPrefix + '-tab-' + index; const panelId = tabPrefix + '-panel-' + index; pair[0].id = tabId; pair[1].id = panelId; pair[0].setAttribute('aria-controls', panelId); pair[1].setAttribute('aria-labelledby', tabId); pair[0].tabIndex = index === 0 ? 0 : -1; });
    function switchTab(which) { const active = { preview: tabPreview, result: tabResult, history: tabHistory }[which] || tabPreview; tabPairs.forEach(pair => { const on = pair[0] === active; pair[0].classList.toggle('is-active', on); pair[0].setAttribute('aria-selected', on ? 'true' : 'false'); pair[0].tabIndex = on ? 0 : -1; pair[1].hidden = !on; }); if (which === 'history' && !state.history.loaded && !state.history.loading) loadHistory(false); }
    tabPreview.onclick = () => switchTab('preview'); tabResult.onclick = () => switchTab('result'); tabHistory.onclick = () => switchTab('history');
    [tabPreview, tabResult, tabHistory].forEach((tab, index, tabs) => tab.addEventListener('keydown', function (event) { if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return; event.preventDefault(); const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length; tabs[next].focus(); switchTab(['preview', 'result', 'history'][next]); }));
    function renderEmpty(target, title, message) { target.replaceChildren(el('div', { class: 'waspack-empty' }, [el('div', {}, [el('strong', { text: title }), el('p', { text: message })])])); }
    function renderPreview(data) {
      state.preview = data || {}; state.previewLimit = 120; const st = data && data.stats || {}, missing = Array.isArray(data && data.missing) ? data.missing : [], files = Array.isArray(data && data.files) ? data.files : [], warnings = Array.isArray(data && data.warnings) ? data.warnings : [], paired = files.filter(file => file && file.source === 'paired').length;
      const summary = el('div', { class: 'waspack-preview-summary' }); [['清单文件', st.total || files.length || 0], ['总大小', fmtBytes(st.bytes)], ['自动补齐', st.paired_added || data.paired_added || paired], ['状态', missing.length ? '有缺失' : '可生成']].forEach(item => summary.appendChild(el('div', { class: 'waspack-metric' + (item[0] === '状态' ? (missing.length ? ' is-bad' : ' is-good') : '') }, [el('span', { class: 'waspack-metric-label', text: item[0] }), el('strong', { class: 'waspack-metric-value', text: String(item[1]) })])));
      const body = [summary]; if (warnings.length) body.push(el('div', { class: 'waspack-message is-warn', text: warnings.join('；') }));
      if (missing.length) { body.push(el('div', { class: 'waspack-section-title' }, [el('strong', { text: '缺失文件' }), el('small', { text: '生成已阻止' })])); const miss = el('ul', { class: 'waspack-file-list' }); missing.slice(0, 400).forEach(item => miss.appendChild(el('li', { class: 'waspack-file-row is-missing' }, [el('span', { class: 'waspack-file-kind', text: '缺失' }), el('code', { text: item && item.rel || item }), el('span', { class: 'waspack-file-size', text: '' })]))); body.push(miss); }
      body.push(el('div', { class: 'waspack-section-title' }, [el('strong', { text: '将写入包的路径' }), el('small', { text: files.length + ' 项' })])); const list = el('ul', { class: 'waspack-file-list' });
      function drawFiles() { list.replaceChildren(); files.slice(0, state.previewLimit).forEach(file => list.appendChild(el('li', { class: 'waspack-file-row' }, [el('span', { class: 'waspack-file-kind', text: kindLabel(file && file.kind) }), el('code', { text: file && file.rel || '' }), el('span', { class: 'waspack-file-size', text: fmtBytes(file && file.bytes) })]))); if (files.length > state.previewLimit) { const more = el('button', { class: 'btn waspack-show-more', type: 'button', text: '显示更多（剩余 ' + (files.length - state.previewLimit) + ' 项）' }); more.onclick = function () { state.previewLimit = Math.min(files.length, state.previewLimit + 400); drawFiles(); }; list.appendChild(el('li', {}, [more])); } }
      drawFiles(); body.push(list); previewPanel.replaceChildren(...body); switchTab('preview'); phase.textContent = missing.length ? '预检发现缺失文件，生成动作已停止。' : '预检通过，可以生成投产包。'; if (missing.length) showAlert('清单中有 ' + missing.length + ' 个文件未找到，已阻止生成。', 'warn'); else clearAlert();
    }
    function artifactList(data) { let artifacts = Array.isArray(data && data.artifacts) ? data.artifacts.slice() : []; if (!artifacts.length) [['tar', data && data.tar_file], ['list', data && data.list_file], ['chmod', data && data.chmod_file], ['backup', data && data.backup_script], ['execute', data && data.execute_script], ['zip', data && data.zip_file]].forEach(item => { if (item[1]) artifacts.push({ kind: item[0], path: item[1], size: 0, sha256: '' }); }); return artifacts; }
    function renderExtracted(data) {
      const summary = el('div', { class: 'waspack-result-summary' });
      [['文件数', data && data.files || 0], ['总大小', fmtBytes(data && data.bytes)], ['输出目录', data && data.output_dir || '-'], ['阶段凭据', data && data.stage_token ? '已生成' : '未返回']].forEach(item => summary.appendChild(el('div', { class: 'waspack-metric' }, [el('span', { class: 'waspack-metric-label', text: item[0] }), el('strong', { class: 'waspack-metric-value', text: String(item[1]) })])));
      const message = data && data.stage_token ? '已抽取到 war 文件夹。请在本地核对或修改内容；配置改变后需要重新抽取。' : 'WAR 已抽取，但服务端没有返回阶段凭据，打包动作受到保护，请重新抽取。';
      const node = el('section', { class: 'waspack-result-block' }, [el('h3', { class: 'waspack-result-title', text: 'WAR 抽取结果' }), summary, el('div', { class: 'waspack-message' }, [el('span', { text: message })]), el('ul', { class: 'waspack-artifact-list' }, [el('li', { class: 'waspack-artifact-row' }, [el('span', { class: 'waspack-artifact-kind', text: 'WAR 目录' }), el('code', { class: 'waspack-artifact-path', text: data && data.war_dir || '' }), el('span', { class: 'waspack-artifact-size', text: '' }), el('span', { class: 'waspack-artifact-hash', text: '阶段签名已绑定当前配置' }), el('span')])])]);
      resultPanel.replaceChildren(node); switchTab('result'); phase.textContent = message;
    }
    function renderResult(normalized, message) {
      const build = normalized && normalized.build || null, zip = normalized && normalized.zip || null; state.result = normalized; if (!build && !zip) { renderEmpty(resultPanel, '暂无结果', '完成预检并生成后，产物路径和完整性摘要会显示在这里。'); return; }
      const blocks = []; [build && { title: 'WAR / TAR 结果', data: build }, zip && { title: 'ZIP 结果', data: zip }].filter(Boolean).forEach(block => { const data = block.data, artifacts = artifactList(data), summary = el('div', { class: 'waspack-result-summary' }); [['文件数', data.files || 0], ['总大小', fmtBytes(data.bytes)], ['输出目录', data.output_dir || '-'], ['包名', data.package_name || packageBaseName(packageInput.value)]].forEach(item => summary.appendChild(el('div', { class: 'waspack-metric' }, [el('span', { class: 'waspack-metric-label', text: item[0] }), el('strong', { class: 'waspack-metric-value', text: String(item[1]) })]))); const rows = el('ul', { class: 'waspack-artifact-list' }); artifacts.forEach(item => { const path = String(item.path || ''), copy = el('button', { class: 'btn waspack-copy', type: 'button', text: '复制', title: '复制产物路径' }); copy.onclick = () => copyToClipboard(path).then(() => toast('已复制产物路径', 'ok')).catch(() => toast('复制失败', 'err')); rows.appendChild(el('li', { class: 'waspack-artifact-row' }, [el('span', { class: 'waspack-artifact-kind', text: String(item.kind || '产物') }), el('code', { class: 'waspack-artifact-path', text: path, title: path }), el('span', { class: 'waspack-artifact-size', text: fmtBytes(item.size) }), el('span', { class: 'waspack-artifact-hash', text: item.sha256 ? 'SHA256 ' + item.sha256 : 'SHA256 未返回' }), copy])); }); const node = el('section', { class: 'waspack-result-block' }, [el('h3', { class: 'waspack-result-title', text: block.title }), summary, rows]); if (Array.isArray(data.warnings) && data.warnings.length) node.appendChild(el('ul', { class: 'waspack-warning-list' }, data.warnings.map(w => el('li', { text: String(w) })))); blocks.push(node); }); resultPanel.replaceChildren(...blocks); switchTab('result'); phase.textContent = message || '操作完成。';
    }
    function historyItems(response) {
      const items = response && (response.items || response.records);
      return Array.isArray(items) ? items.filter(item => item && typeof item === 'object') : [];
    }
    function historyMatches(item, query) {
      if (!query) return true;
      const haystack = [item.operation, item.status, item.package_name, item.output_dir, item.error].concat(item.warnings || []).join(' ').toLowerCase();
      return haystack.indexOf(query.toLowerCase()) >= 0;
    }
    function historyRow(item, index) {
      const previous = previousComparable(state.history.items, index);
      const output = String(item.output_dir || '').trim(), previousOutput = previous && String(previous.output_dir || '').trim();
      const canCompare = comparableHistoryItem(item) && !!previousOutput && !!output && !sameWindowsPath(previousOutput, output);
      const article = el('article', { class: 'waspack-history-row' });
      const head = el('div', { class: 'waspack-history-row-head' });
      head.append(el('strong', { text: operationLabel(item.operation) }), el('span', { class: 'waspack-status-badge ' + (item.status === 'success' ? 'is-success' : 'is-failure'), text: statusLabel(item.status) }), el('span', { class: 'waspack-history-row-meta', text: item.created_at ? new Date(item.created_at).toLocaleString() : '时间未知' }));
      const meta = el('div', { class: 'waspack-history-row-meta' }); meta.append(el('span', { text: '包名：' + (item.package_name || '-') }), el('span', { text: '文件：' + (item.files || 0) }), el('span', { text: '大小：' + fmtBytes(item.bytes) }));
      const outputNode = el('div', { class: 'waspack-history-row-path', text: '输出：' + (item.output_dir || '-') }); const actions = el('div', { class: 'waspack-history-row-actions' });
      const detailButton = el('button', { class: 'btn', type: 'button', text: '查看详情', title: '查看本次请求、清单和产物哈希' }); detailButton.onclick = function () { showHistoryDetail(item); };
      const rebuildButton = el('button', { class: 'btn', type: 'button', text: '重新生成', title: '按这条历史的清单重新生成' }); rebuildButton.onclick = function () { showRebuild(item); };
      const compareButton = el('button', { class: 'btn', type: 'button', text: '与上次包对比' });
      if (!comparableHistoryItem(item)) { compareButton.disabled = true; compareButton.title = '仅成功的生成、WAR 打包或历史重建可比较。'; }
      else if (!previous) { compareButton.disabled = true; compareButton.title = '没有更早的成功生成、WAR 打包或历史重建记录。'; }
      else if (!output || !previousOutput) { compareButton.disabled = true; compareButton.title = '当前记录与上次记录必须都有输出目录。'; }
      else if (!canCompare) { compareButton.disabled = true; compareButton.title = '同一目录（目录被复用或清空时没有两个可比较快照）。'; }
      else { compareButton.title = '比较上一条成功记录与当前记录的输出目录'; compareButton.onclick = function () { openCompare(previous, item); }; }
      const deleteButton = el('button', { class: 'btn', type: 'button', text: '删除记录', title: '只删除历史记录，不删除输出文件' }); deleteButton.onclick = function () { deleteHistory(item); };
      actions.append(detailButton, rebuildButton, compareButton, deleteButton); article.append(head, meta, outputNode);
      if (item.error) article.appendChild(el('p', { class: 'waspack-history-row-error', text: '错误：' + item.error }));
      if (Array.isArray(item.warnings) && item.warnings.length) article.appendChild(el('p', { class: 'waspack-history-row-warning', text: '警告：' + item.warnings.join('；') }));
      article.appendChild(actions); return article;
    }
    function renderHistory() {
      if (state.history.loading) { historyPanel.replaceChildren(el('div', { class: 'waspack-empty' }, [el('div', {}, [el('strong', { text: '正在加载历史' }), el('p', { text: '正在读取最近 100 次投产操作。' })])])); return; }
      if (state.history.error) { const retry = el('button', { class: 'btn btn-primary', type: 'button', text: '重试' }); retry.onclick = function () { loadHistory(true); }; historyPanel.replaceChildren(el('div', { class: 'waspack-empty' }, [el('div', {}, [el('strong', { text: '历史加载失败' }), el('p', { text: state.history.error }), retry])])); return; }
      const search = el('input', { type: 'search', placeholder: '筛选操作、包名、路径或错误…', 'aria-label': '筛选打包历史', value: state.history.filter }); const refresh = el('button', { class: 'btn', type: 'button', text: '刷新', title: '重新读取历史' }); const toolbar = el('div', { class: 'waspack-history-toolbar' }, [search, refresh]); const list = el('div', { class: 'waspack-history-list' });
      function draw() { list.replaceChildren(); const filtered = state.history.items.map((item, index) => ({ item, index })).filter(entry => historyMatches(entry.item, state.history.filter)); if (!filtered.length) list.appendChild(el('div', { class: 'waspack-empty waspack-history-empty' }, [el('div', {}, [el('strong', { text: state.history.items.length ? '没有匹配的记录' : '暂无投产历史' }), el('p', { text: state.history.items.length ? '请修改筛选条件。' : '完成一次预检并生成后，操作摘要会保留在这里。' })])])); else filtered.forEach(entry => list.appendChild(historyRow(entry.item, entry.index))); }
      search.addEventListener('input', function () { state.history.filter = search.value; draw(); }); refresh.onclick = function () { loadHistory(true); }; draw(); historyPanel.replaceChildren(toolbar, list);
    }
    async function loadHistory(force) {
      if (state.history.loading) return; if (force) state.history.loaded = false; state.history.loading = true; state.history.error = ''; const seq = ++historySeq; renderHistory();
      try { const response = await api('GET', '/api/waspack/history?limit=100'); if (state.disposed || seq !== historySeq) return; state.history.items = historyItems(response); state.history.loaded = true; }
      catch (error) { if (state.disposed || seq !== historySeq) return; state.history.error = errorText(error); }
      finally { if (!state.disposed && seq === historySeq) { state.history.loading = false; renderHistory(); } }
    }
    function openCompare(previous, current) {
      if (!previous || !current || !previous.output_dir || !current.output_dir || sameWindowsPath(previous.output_dir, current.output_dir)) return;
      let handoff, popup;
      try {
        handoff = createHandoff(previous.output_dir, current.output_dir);
        localStorage.setItem(HANDOFF_PREFIX + handoff.nonce, JSON.stringify(handoff.payload));
        // Chromium may return null for a successful noopener navigation. Open
        // a blank tab first so null unambiguously means popup blocking, then
        // sever the opener synchronously before navigating to the same origin.
        popup = window.open('about:blank', '_blank');
        if (!popup) throw new Error('浏览器阻止了新窗口，请允许弹窗后重试。');
        popup.opener = null;
        popup.location.replace(handoffTargetURL(window.location, handoff.nonce));
      } catch (error) {
        if (popup && typeof popup.close === 'function') { try { popup.close(); } catch (_) {} }
        if (handoff) { try { localStorage.removeItem(HANDOFF_PREFIX + handoff.nonce); } catch (_) {} }
        showAlert(errorText(error), 'warn'); toast(errorText(error), 'warn');
      }
    }
    function showHistoryDetail(item) {
      if (!Kairo.overlays || !Kairo.overlays.modal) { toast('当前环境不支持详情弹窗', 'err'); return; }
      const body = el('div', { class: 'waspack-history-modal-body' });
      body.appendChild(el('p', { class: 'waspack-help', text: '正在读取完整历史记录…' }));
      Kairo.overlays.modal({ title: '投产历史详情', width: 780, body: body });
      api('GET', '/api/waspack/history/' + encodeURIComponent(item.id)).then(function (response) {
        const record = response && response.record || {}, request = record.request || {};
        body.replaceChildren();
        const config = el('dl', { class: 'waspack-history-config' });
        [['操作', operationLabel(record.operation)], ['状态', statusLabel(record.status)], ['创建时间', record.created_at || ''], ['完成时间', record.completed_at || ''], ['工程目录', request.project_dir || ''], ['输出目录', request.output_dir || ''], ['包名', request.package_name || ''], ['自动补齐', request.auto_pair ? '是' : '否'], ['代码类型', request.pack_type || 'app'], ['批量部署根路径', request.batch_base_dir || ''], ['chmod 模式', request.chmod_mode || ''], ['输出策略', request.output_policy || 'fail']].forEach(function (pair) {
          config.appendChild(el('div', {}, [el('dt', { text: pair[0] }), el('dd', { text: String(pair[1]) })]));
        });
        body.append(config, el('h3', { text: '投产清单' }), el('pre', { class: 'waspack-history-manifest', text: request.manifest || '' }), el('h3', { text: '产物哈希' }));
        const artifacts = el('ul', { class: 'waspack-artifact-list' });
        (Array.isArray(record.artifacts) ? record.artifacts : []).forEach(function (artifact) {
          const row = el('li', { class: 'waspack-artifact-row' });
          row.append(el('span', { class: 'waspack-artifact-kind', text: artifact.kind || '产物' }), el('code', { class: 'waspack-artifact-path', text: artifact.path || '' }), el('span', { class: 'waspack-artifact-size', text: fmtBytes(artifact.size) }), el('span', { class: 'waspack-artifact-hash', text: artifact.sha256 ? 'SHA256 ' + artifact.sha256 : 'SHA256 未返回' }), el('span'));
          artifacts.appendChild(row);
        });
        body.appendChild(artifacts);
        if (record.error) body.appendChild(el('p', { class: 'waspack-history-row-error', text: '错误：' + record.error }));
        if (Array.isArray(record.warnings) && record.warnings.length) body.appendChild(el('p', { class: 'waspack-history-row-warning', text: '警告：' + record.warnings.join('；') }));
      }).catch(function (error) { body.replaceChildren(el('div', { class: 'waspack-message is-error', text: errorText(error) })); });
    }
    async function showRebuild(item) {
      if (!Kairo.overlays || !Kairo.overlays.modal) { toast('当前环境不支持重新生成弹窗', 'err'); return; }
      const body = el('div', { class: 'waspack-rebuild-body' });
      const footer = el('div', { class: 'waspack-modal-footer' });
      body.appendChild(el('p', { class: 'waspack-help', text: '正在读取历史清单…' }));
      const dialog = Kairo.overlays.modal({ title: '重新生成投产包', width: 620, body: body, footer: footer });
      try {
        const response = await api('GET', '/api/waspack/history/' + encodeURIComponent(item.id));
        const record = response && response.record || {}, request = record.request || {};
        const output = el('input', { type: 'text', value: request.output_dir || item.output_dir || '', autocomplete: 'off', 'aria-label': '重新生成输出目录' });
        const policy = el('select', { 'aria-label': '重新生成输出策略' }, [el('option', { value: 'fail', text: '严格失败：目标必须为空（推荐）' }), el('option', { value: 'clean_kairo_artifacts', text: '清理 Kairo 已知产物，保留其他文件' }), el('option', { value: 'replace', text: '覆盖：清空目标目录内全部文件' })]);
        policy.value = 'fail';
        const include = el('input', { type: 'checkbox', checked: !!request.include_zip });
        const outputField = el('label', { class: 'waspack-field' }, [el('span', { class: 'waspack-field-label', text: '输出目录' }), output]);
        const policyField = el('label', { class: 'waspack-field' }, [el('span', { class: 'waspack-field-label', text: '输出策略（本次）' }), policy]);
        body.replaceChildren(el('p', { class: 'waspack-help', text: '本次重建从历史快照读取工程、包名和清单；输出目录可改，策略不会继承。' }), outputField, policyField, el('label', { class: 'waspack-check' }, [include, el('span', { text: '同时生成 ZIP' })]));
        const cancel = el('button', { class: 'btn', type: 'button', text: '取消' });
        const submit = el('button', { class: 'btn btn-primary', type: 'button', text: '重新生成' });
        footer.replaceChildren(cancel, submit); cancel.onclick = function () { dialog.close(); };
        submit.onclick = async function () {
          const outputDir = output.value.trim(); if (!outputDir) { toast('请填写输出目录', 'warn'); output.focus(); return; }
          let confirmReplace = false;
          if (policy.value === 'replace') { confirmReplace = await confirmDialog('精确目标目录：' + outputDir + '\n\n这是一次新的覆盖授权：会清空该目录内全部文件，且不会继承历史记录中的确认。请完成第二次明确确认。', { title: '第二次确认覆盖', okText: '确认覆盖并重建' }); if (!confirmReplace) return; }
          submit.disabled = true;
          try {
            const rebuildBody = { output_dir: outputDir, output_policy: policy.value || 'fail', confirm_replace: confirmReplace, include_zip: !!include.checked };
            await attachReplaceToken(rebuildBody);
            const result = await api('POST', '/api/waspack/history/' + encodeURIComponent(item.id) + '/rebuild', rebuildBody);
            dialog.close(); renderResult(normalizeBuildResponse(result), '历史重建完成。'); toast('历史重建完成', 'ok'); loadHistory(true);
          } catch (error) {
            if (error && error.data && error.data.build) { dialog.close(); renderResult({ build: error.data.build, zip: null, partial: true, error: error.data.error || errorText(error) }, '历史重建已生成投产包，但 ZIP 失败。'); showAlert('投产包已生成，但 ZIP 生成失败：' + (error.data.error || errorText(error)), 'warn'); }
            else { showAlert(errorText(error)); toast(errorText(error), 'err'); }
          } finally { submit.disabled = false; }
        };
      } catch (error) { body.replaceChildren(el('div', { class: 'waspack-message is-error', text: errorText(error) })); }
    }
    async function deleteHistory(item) { const ok = await confirmDialog('只删除这条打包历史记录，不会删除输出目录或其中任何文件。\n\n确认删除记录“' + (item.package_name || item.id || '未命名') + '”？', { title: '删除历史记录', okText: '删除记录' }); if (!ok) return; try { await api('DELETE', '/api/waspack/history/' + encodeURIComponent(item.id)); toast('历史记录已删除，输出文件保留不变', 'ok'); loadHistory(true); } catch (error) { showAlert(errorText(error)); toast(errorText(error), 'err'); } }
    async function askReplace(path, context, scope) {
      if (policyInput.value !== 'replace') return false;
      const consequence = scope === 'artifacts'
        ? '覆盖模式只会替换本次将写入的同名产物，不会删除目标目录中的其他文件。'
        : '覆盖模式会清空目标目录内的全部文件，并重新发布当前投产产物。此操作不可撤销。';
      return confirmDialog('目标目录：' + path + '\n\n' + consequence + '\n\n确认' + context + '？', { title: '确认覆盖输出目录', okText: '确认覆盖' });
    }
    async function attachReplaceToken(body) {
      if (!body || body.output_policy !== 'replace') return body;
      const grant = await api('POST', '/api/waspack/replace-token', { output_dir: body.output_dir });
      body.replace_token = grant && grant.replace_token || '';
      return body;
    }
    async function runAction(button, busyText, task) {
      if (state.busy || state.disposed) return;
      state.busy = true;
      const seq = ++operationSeq, old = button.textContent;
      const controls = Array.from(view.querySelectorAll('input, select, textarea, button')).map(node => ({ node, disabled: node.disabled }));
      phase.textContent = busyText; clearAlert(); progress.hidden = false; view.setAttribute('aria-busy', 'true');
      controls.forEach(item => { item.node.disabled = true; }); button.textContent = busyText;
      try { await task(seq); } catch (error) {
        if (seq === operationSeq && !state.disposed) { showAlert(errorText(error)); phase.textContent = errorText(error); toast(errorText(error), 'err'); }
      }
      controls.forEach(item => { item.node.disabled = item.disabled; });
      if (state.disposed || seq !== operationSeq) return;
      state.busy = false; progress.hidden = true; view.removeAttribute('aria-busy'); button.textContent = old; updateActionAvailability();
    }
    async function doPreview(seq) { if (!validate()) { phase.textContent = '配置校验失败，请修正标红字段。'; showAlert('请先修正配置中的必填项。', 'warn'); return; } const result = await api('POST', '/api/waspack/preview', payload()); if (state.disposed || seq !== operationSeq) return; renderPreview(result); toast('预检完成', result.missing && result.missing.length ? 'warn' : 'ok'); }
    async function doBuild(includeZip, seq) { if (!validate()) { phase.textContent = '配置校验失败，请修正标红字段。'; showAlert('请先修正配置中的必填项。', 'warn'); return; } const body = payload(), preview = await api('POST', '/api/waspack/preview', body); if (state.disposed || seq !== operationSeq) return; renderPreview(preview); if (preview.missing && preview.missing.length) return; const confirmed = await askReplace(body.output_dir, '一键生成', 'full'); if (body.output_policy === 'replace' && !confirmed) { phase.textContent = '已取消覆盖。'; return; } body.include_zip = !!includeZip; body.confirm_replace = !!confirmed; await attachReplaceToken(body); let response; try { response = await api('POST', '/api/waspack/build', body); } catch (error) { if (includeZip && error && error.data && error.data.build) { renderResult({ build: error.data.build, zip: null, partial: true, error: error.data.error || errorText(error) }, '投产包已生成，但 ZIP 失败。'); showAlert('投产包已生成，但 ZIP 生成失败：' + (error.data.error || errorText(error)), 'warn'); loadHistory(true); return; } throw error; } if (state.disposed || seq !== operationSeq) return; renderResult(normalizeBuildResponse(response), includeZip ? '投产包与 ZIP 均已生成。' : '投产包已生成。'); toast(includeZip ? '投产包与 ZIP 已生成' : '投产包已生成', 'ok'); loadHistory(true); }
    async function doExtract(seq) { if (!validate()) { phase.textContent = '配置校验失败，请修正标红字段。'; showAlert('请先修正配置中的必填项。', 'warn'); return; } const body = payload(), confirmed = await askReplace(body.output_dir, '抽取 WAR', 'full'); if (body.output_policy === 'replace' && !confirmed) { phase.textContent = '已取消覆盖。'; return; } body.confirm_replace = !!confirmed; await attachReplaceToken(body); setStage(''); const response = await api('POST', '/api/waspack/extract', body); if (state.disposed || seq !== operationSeq) return; setStage(response && response.stage_token); renderExtracted(response); toast('WAR 已抽取', 'ok'); rememberProject(body.project_dir); loadHistory(true); }
    async function doPackage(seq) { if (!validate()) { phase.textContent = '配置校验失败，请修正标红字段。'; showAlert('请先修正配置中的必填项。', 'warn'); return; } if (!state.stageToken || state.stageStale) { showAlert('当前 WAR 抽取凭据缺失或已过期，请重新抽取。', 'warn'); return; } const body = payload(); body.stage_token = state.stageToken; const confirmed = await askReplace(body.output_dir, '打包 WAR', 'artifacts'); if (body.output_policy === 'replace' && !confirmed) { phase.textContent = '已取消覆盖。'; return; } body.confirm_replace = !!confirmed; await attachReplaceToken(body); const response = await api('POST', '/api/waspack/package', body); if (state.disposed || seq !== operationSeq) return; renderResult(normalizeBuildResponse(response), '已按 war 当前内容生成投产包。'); toast('WAR 打包完成', 'ok'); loadHistory(true); }
    async function doZip(seq) { if (!validateZip()) { showAlert('请先修正 ZIP 所需的输出目录和包名。', 'warn'); return; } const body = payload(), confirmed = await askReplace(body.output_dir, '生成 ZIP', 'artifacts'); if (body.output_policy === 'replace' && !confirmed) { phase.textContent = '已取消覆盖。'; return; } body.confirm_replace = !!confirmed; await attachReplaceToken(body); const response = await api('POST', '/api/waspack/zip', body); if (state.disposed || seq !== operationSeq) return; renderResult(normalizeBuildResponse(response), 'ZIP 已生成。'); toast('ZIP 已生成', 'ok'); loadHistory(true); }
    async function doOpen() { const dir = outputInput.value.trim(); if (!dir) { showAlert('请先填写输出目录。', 'warn'); return; } await api('POST', '/api/waspack/open', { output_dir: dir }); toast('已打开输出目录', 'ok'); phase.textContent = '已请求系统打开输出目录。'; }

    const projectField = pathField('工程根目录', projectInput, rememberProject, 'project'); projectField.appendChild(projectHistory); const outputField = pathField('输出目录', outputInput, null, 'output');
    const packageField = field('包名', packageInput, null, 'package'); packageField.appendChild(tarHint); const manifestField = field('投产清单', manifestInput, null, 'manifest'); manifestField.appendChild(lineCount);
    const batchBaseField = field('批量部署根路径', batchBaseInput, el('span', { class: 'waspack-help', text: '用于生成 chmod.txt 中的远端路径。' }), 'waspack-batch-only'); const chmodField = field('chmod 权限模式', chmodInput, el('span', { class: 'waspack-help', text: '默认 777；仅批量模式使用。' }), 'waspack-batch-only');
    let buildButton, previewButton, extractButton, packageButton, zipButton, openButton;
    const modeSegment = el('div', { class: 'waspack-segmented', role: 'radiogroup', 'aria-label': '代码类型' }, [
      el('label', { class: 'waspack-segment' }, [appRadio, el('span', { text: '应用代码（WAS / WAR）' })]),
      el('label', { class: 'waspack-segment' }, [batchRadio, el('span', { text: '批量代码（Batch）' })])
    ]);
    syncTypeUI(); packageHint(); updateLineCount();
    const policyField = field('输出目录处理', policyInput, policyHelp);
    const advanced = el('details', { class: 'waspack-advanced' });
    advanced.append(el('summary', { text: '高级设置' }), el('div', { class: 'waspack-advanced-grid' }, [batchBaseField, chmodField, policyField, el('p', { class: 'waspack-advanced-note', text: '抽取 WAR 后，只有输入签名未变化时才允许执行“打包 WAR”。输出策略仅本次操作有效。' })]));
    const primaryActions = el('div', { class: 'waspack-primary-actions' });
    const buildBtnNode = el('button', { class: 'btn btn-primary', type: 'button', text: '预检并生成', title: '预检清单，通过后生成投产包', onclick: function () { runAction(buildButton, '预检中…', seq => doBuild(!!includeZipInput.checked, seq)); } });
    const includeZipLabel = el('label', { class: 'waspack-check' }, [includeZipInput, el('span', { text: '同时生成 ZIP' })]);
    primaryActions.append(buildBtnNode, includeZipLabel);
    const extraActions = el('div', { class: 'waspack-secondary-actions' });
    const extractBtnNode = el('button', { class: 'btn', type: 'button', text: '抽取 WAR', title: '抽取到 war 文件夹并生成阶段凭据', onclick: function () { runAction(extractButton, '抽取 WAR…', doExtract); } });
    const packageBtnNode = el('button', { class: 'btn', type: 'button', text: '打包 WAR', title: '使用当前 war 内容打包；输入变化后需重新抽取', onclick: function () { runAction(packageButton, '打包 WAR…', doPackage); } });
    const zipBtnNode = el('button', { class: 'btn', type: 'button', text: '仅生成 ZIP', title: '将当前输出内容生成 ZIP', onclick: function () { runAction(zipButton, '生成 ZIP…', doZip); } });
    const openBtnNode = el('button', { class: 'btn', type: 'button', text: '打开目录', title: '在系统文件管理器中打开输出目录', onclick: function () { runAction(openButton, '打开目录…', doOpen); } });
    extraActions.append(extractBtnNode, packageBtnNode, zipBtnNode, openBtnNode);
    const actions = el('div', { class: 'waspack-actions' });
    const previewBtnNode = el('button', { class: 'btn waspack-preview-action', type: 'button', text: '仅预检', title: '只检查清单，不创建或修改输出目录', onclick: function () { runAction(previewButton, '预检中…', doPreview); } });
    actions.append(primaryActions, previewBtnNode, extraActions);
    const statusNode = el('div', { class: 'waspack-status' }, [phase, progress, alert]);
    const configBody = el('div', { class: 'waspack-config-body' });
    configBody.append(modeSegment, projectField, outputField, packageField, el('label', { class: 'waspack-check' }, [autoPair, el('span', { text: '清单只有 .java 时自动补对应 .class（含内部类）；只有 .class 时补 src 下 .java' })]), manifestField, advanced, actions, statusNode);
    const config = el('section', { class: 'waspack-config', 'aria-label': '投产打包配置' });
    config.append(el('div', { class: 'waspack-config-head' }, [el('div', {}, [el('h2', { text: '配置' }), el('p', { text: '确认输入、预检，再发布可追溯产物。' })]), stageBadge]), configBody);
    buildButton = buildBtnNode; previewButton = previewBtnNode; extractButton = extractBtnNode; packageButton = packageBtnNode; zipButton = zipBtnNode; openButton = openBtnNode;
    [projectInput, outputInput, packageInput, manifestInput, batchBaseInput, chmodInput, autoPair].forEach(node => { node.addEventListener('input', markChanged); node.addEventListener('change', markChanged); }); manifestInput.addEventListener('input', function () { clearTimeout(lineTimer); lineTimer = setTimeout(updateLineCount, 160); });
    appRadio.addEventListener('change', function () { if (appRadio.checked && state.type !== 'app') switchType('app'); }); batchRadio.addEventListener('change', function () { if (batchRadio.checked && state.type !== 'batch') switchType('batch'); });
    function switchType(next) { draft[state.type] = { package_name: packageInput.value, manifest: manifestInput.value }; saved[state.type === 'batch' ? 'project_dir_batch' : 'project_dir_app'] = projectInput.value; saved[state.type === 'batch' ? 'output_dir_batch' : 'output_dir_app'] = outputInput.value; state.type = next; projectInput.value = projectOf(next); outputInput.value = outputOf(next); const nextDraft = draft[next] || {}; packageInput.value = nextDraft.package_name || ((next === 'batch' ? 'DDD' : 'TT') + todayStamp() + 'qijunV1'); manifestInput.value = nextDraft.manifest || ''; setStage(''); syncTypeUI(); markChanged(); }
    updateActionAvailability(); renderEmpty(previewPanel, '等待预检', '预检会对照工程目录检查清单；通过后才能创建投产包。'); renderEmpty(resultPanel, '暂无结果', '生成或抽取完成后，产物路径、大小和 SHA256 会显示在这里。'); renderEmpty(historyPanel, '正在准备历史记录', '切换到历史标签后会读取最近 100 条投产记录。');
    const workspace = el('section', { class: 'waspack-workspace', 'aria-label': '打包工作区' });
    const workspaceHead = el('div', { class: 'waspack-workspace-head' });
    workspaceHead.append(workspaceTitle, el('div', { class: 'waspack-tabs', role: 'tablist', 'aria-label': '打包信息' }, [tabPreview, tabResult, tabHistory]));
    workspace.append(workspaceHead, previewPanel, resultPanel, historyPanel);
    const page = el('div', { class: 'waspack-page' });
    page.append(el('header', { class: 'waspack-header' }, [el('div', {}, [el('h1', { text: 'WAS / WAR 投产打包' }), el('p', { text: '以清单为边界生成可核验的 tar、脚本和 ZIP 产物。' })]), el('div', { class: 'waspack-header-meta' }, [el('span', { class: 'waspack-header-badge', text: '受控输出' })])]), el('div', { class: 'waspack-layout' }, [config, workspace]));
    view.replaceChildren(page);
    return function () { state.disposed = true; clearTimeout(draftTimer); clearTimeout(lineTimer); operationSeq++; };
  }

  Kairo.pages.waspack = renderWASPack;
  Kairo.state = Kairo.state || {}; Kairo.state.routes = Kairo.state.routes || {}; Kairo.state.routeNames = Kairo.state.routeNames || {}; Kairo.state.routeSubs = Kairo.state.routeSubs || {};
  Kairo.state.routes.waspack = renderWASPack; Kairo.state.routeNames.waspack = '投产打包'; Kairo.state.routeSubs.waspack = 'credit → tar';
  Kairo.waspackTest = { canonicalStageSignature, windowsPathKey, sameWindowsPath, comparableHistoryItem, previousComparable, normalizeBuildResponse, makeHandoffPayload, createHandoff, handoffTargetURL, duplicateManifestMessage, HANDOFF_PREFIX };
})();
