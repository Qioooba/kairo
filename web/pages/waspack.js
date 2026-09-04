/* ===== web/pages/waspack.js =====
 * 投产打包：支持「应用代码（WAS / WAR）」与「批量代码（Batch）」两种模式
 * 批量模式自动识别 .sh 生成 chmod.txt，并生成 Bak<包名>.sh、<包名>.sh 及 tar 包
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard, lastGet, lastSet, escapeHtml } = Kairo.core;
  const { api, getPreference, putPreference, preferenceSaver, pathRow: localPathRow } = Kairo.api;
  const SESSION_DRAFT_KEY = 'kairo:waspack:session-draft';

  const SAMPLE_APP = [
    './CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp',
    './CreditManage/CreditLine/ProductInfo.jsp',
    './src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java',
    './WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class'
  ].join('\n');

  const SAMPLE_BATCH = [
    './amargci/credit_2nd.sh',
    './amargci/newcbsdata.sh',
    './amargci/etc/gci_task_2nd.xml',
    './AmarExtract/etc/ext_metadata.xml',
    './AmarExtract/etc/ext_credit_task_ql.xml',
    './AmarExtract/etc/ext_credit_are_ql.xml',
    './AmarExtract/src/jsyh/CustomerSpecialInfo.java',
    './AmarExtract/classes/jsyh/CustomerSpecialInfo.class',
    './AmarExtract/get_customerspecialinfo.sh',
    './AmarExtract/run_ql.sh',
    './AmarExtract/src/jsyh/UpdateExchangeRate.java',
    './AmarExtract/classes/jsyh/UpdateExchangeRate.class'
  ].join('\n');

  function todayStamp() {
    const d = new Date();
    const p = function (n) { return n < 10 ? '0' + n : '' + n; };
    return '' + d.getFullYear() + p(d.getMonth() + 1) + p(d.getDate());
  }
  function kindLabel(kind) {
    if (kind === 'java') return 'Java';
    if (kind === 'class') return 'Class';
    if (kind === 'jsp') return 'JSP';
    return '其他';
  }
  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    return (n / (1024 * 1024)).toFixed(2) + ' MB';
  }

  function pathRow(label, input, opts, extra) {
    const row = localPathRow(input, Object.assign({ directory: true, rowClass: 'path-field waspack-path' }, opts || {}));
    if (extra) row.appendChild(extra);
    return el('label', { class: 'waspack-field' }, [
      el('span', { class: 'waspack-label', text: label }),
      row
    ]);
  }

  async function renderWASPack(view) {
    const renderToken = view.dataset.renderToken;
    view.innerHTML = '<div class="card muted">正在恢复投产打包偏好…</div>';
    let saved = {}, legacy = lastGet('waspack', 'form') || {}, preferenceAvailable = false;
    try {
      const remote = await getPreference('waspack');
      saved = remote && remote.value && typeof remote.value === 'object' ? remote.value : {};
      if (!remote.exists && Object.keys(legacy).length) {
        saved = { project_dir: legacy.project_dir || '', output_dir: legacy.output_dir || '', auto_pair: legacy.auto_pair !== false, pack_type: legacy.pack_type || 'app', batch_base_dir: legacy.batch_base_dir || '/batch/credit' };
        await putPreference('waspack', saved);
      }
      preferenceAvailable = true;
    } catch (e) {
      saved = { project_dir: legacy.project_dir || '', output_dir: legacy.output_dir || '', auto_pair: legacy.auto_pair !== false, pack_type: legacy.pack_type || 'app', batch_base_dir: legacy.batch_base_dir || '/batch/credit' };
    }
    let sessionDraft = {};
    try { sessionDraft = JSON.parse(sessionStorage.getItem(SESSION_DRAFT_KEY) || '{}'); } catch (_) { sessionDraft = {}; }
    if (!Object.keys(sessionDraft).length && legacy && (legacy.package_name || legacy.manifest)) {
      sessionDraft = { package_name: legacy.package_name || '', manifest: legacy.manifest || '', pack_type: legacy.pack_type || 'app' };
      try { sessionStorage.setItem(SESSION_DRAFT_KEY, JSON.stringify(sessionDraft)); } catch (_) {}
    }
    if (preferenceAvailable) lastSet('waspack', 'form', null);
    else lastSet('waspack', 'form', { project_dir: saved.project_dir || '', output_dir: saved.output_dir || '', auto_pair: saved.auto_pair !== false, pack_type: saved.pack_type || 'app', batch_base_dir: saved.batch_base_dir || '/batch/credit' });
    if (view.dataset.renderToken !== renderToken) return;
    view.innerHTML = '';

    let currentType = sessionDraft.pack_type || saved.pack_type || 'app';

    // 顶部模式切换单选按钮栏
    const typeAppRadio = el('input', { type: 'radio', name: 'waspack-type', id: 'type-app', value: 'app' });
    const typeBatchRadio = el('input', { type: 'radio', name: 'waspack-type', id: 'type-batch', value: 'batch' });
    if (currentType === 'batch') typeBatchRadio.checked = true;
    else typeAppRadio.checked = true;

    const heroDesc = el('div', { class: 'card-desc', text: '' });

    const projectInp = el('input', {
      type: 'text', id: 'waspack-project',
      placeholder: '',
      spellcheck: 'false'
    });
    const outputInp = el('input', {
      type: 'text', id: 'waspack-output',
      placeholder: '',
      spellcheck: 'false'
    });
    const pkgInp = el('input', {
      type: 'text', id: 'waspack-pkg',
      placeholder: '',
      spellcheck: 'false',
      autocomplete: 'off'
    });
    const batchBaseInp = el('input', {
      type: 'text', id: 'waspack-batch-base',
      placeholder: '例如 /batch/credit',
      value: saved.batch_base_dir || '/batch/credit',
      spellcheck: 'false',
      autocomplete: 'off'
    });
    const batchBaseRow = el('label', { class: 'waspack-field waspack-field-batch-base' }, [
      el('span', { class: 'waspack-label', text: '批量部署根路径（chmod.txt 路径前缀）' }),
      batchBaseInp,
      el('span', { class: 'hint', text: '清单中的 .sh 脚本将生成：chmod 777 <此路径>/<脚本路径>' })
    ]);

    const tarHint = el('span', { class: 'waspack-tar-hint', text: '' });
    const autoPair = el('input', { type: 'checkbox', id: 'waspack-autopair' });
    autoPair.checked = true;
    const manifestTa = el('textarea', {
      id: 'waspack-manifest',
      spellcheck: 'false',
      placeholder: ''
    });
    [projectInp, outputInp, pkgInp, batchBaseInp].forEach(function (n) {
      n.setAttribute('autocomplete', 'off');
    });

    function hasSaved(key) {
      return saved && Object.prototype.hasOwnProperty.call(saved, key);
    }
    if (hasSaved('project_dir')) projectInp.value = saved.project_dir || '';
    if (hasSaved('output_dir')) outputInp.value = saved.output_dir || '';
    if (sessionDraft.package_name && String(sessionDraft.package_name).trim()) pkgInp.value = sessionDraft.package_name;
    else pkgInp.value = (currentType === 'batch' ? 'DDD' : 'TT') + todayStamp() + 'qijunV1';
    if (typeof saved.auto_pair === 'boolean') autoPair.checked = saved.auto_pair;
    if (sessionDraft.manifest) manifestTa.value = sessionDraft.manifest;

    const outputPolicy = el('select', { id: 'waspack-output-policy' }, [
      el('option', { value: 'fail', text: '严格模式：目录必须为空（推荐首次打包）' }),
      el('option', { value: 'clean_kairo_artifacts', text: '清理 Kairo 上次产物后重做（保留其他文件）' }),
      el('option', { value: 'replace', text: '清空上次 Kairo 打包目录并覆盖 (需包含 Kairo 标记)' })
    ]);
    outputPolicy.value = saved.output_policy || 'fail';

    const savePreference = preferenceSaver('waspack', 500);

    const projectHistorySelect = el('select', { class: 'waspack-history-select', title: '选择历史工程根目录' });
    function fillProjectHistory() {
      projectHistorySelect.innerHTML = '';
      projectHistorySelect.appendChild(el('option', { value: '', text: '📁 历史工程…' }));
      const hist = Array.isArray(saved.project_dir_history) ? saved.project_dir_history : [];
      hist.forEach(function (dir) {
        projectHistorySelect.appendChild(el('option', { value: dir, text: dir }));
      });
      projectHistorySelect.style.display = hist.length ? '' : 'none';
    }
    fillProjectHistory();
    projectHistorySelect.addEventListener('change', function () {
      if (this.value) {
        projectInp.value = this.value;
        persist();
      }
      this.value = '';
    });
    function rememberProjectDir(dir) {
      dir = String(dir || '').trim();
      if (!dir) return;
      saved.project_dir_history = Array.isArray(saved.project_dir_history) ? saved.project_dir_history : [];
      saved.project_dir_history = saved.project_dir_history.filter(function (it) { return it !== dir; });
      saved.project_dir_history.unshift(dir);
      if (saved.project_dir_history.length > 12) saved.project_dir_history = saved.project_dir_history.slice(0, 12);
      fillProjectHistory();
      persistPreferenceFields();
    }

    function pkgBaseName() {
      return (pkgInp.value || '').trim().replace(/\.(tar|sh)$/i, '');
    }

    function updateTarHint() {
      const n = pkgBaseName();
      const isBatch = currentType === 'batch';
      const chmodPart = isBatch ? '  ·  chmod.txt' : '';
      if (!n) {
        tarHint.textContent = '将生成 包名.tar、包名.sh、Bak包名.sh、list.txt' + (isBatch ? '、chmod.txt' : '');
        return;
      }
      tarHint.textContent = '将生成 ' + n + '.tar  ·  ' + n + '.sh  ·  Bak' + n + '.sh  ·  list.txt' + chmodPart;
    }

    function syncTypeUI() {
      const isBatch = currentType === 'batch';
      if (isBatch) {
        heroDesc.textContent = '批量代码打包：直接使用各子模块相对路径（如 ./amargci/credit_2nd.sh、./AmarExtract/...）。自动抽取文件并生成 list.txt、chmod.txt、Bak*.sh、*.sh 和 tar 包。';
        projectInp.placeholder = '例如 D:\\projects\\batch 或 C:\\ideaSpaces\\batch';
        outputInp.placeholder = '例如 D:\\packs\\DDD' + todayStamp() + '（必须不存在或为空）';
        manifestTa.placeholder = SAMPLE_BATCH;
        batchBaseRow.style.display = '';
      } else {
        heroDesc.textContent = '应用代码打包：只选本地 credit 工程根（例如 C:\\ideaSpaces\\credit）。清单按投产相对路径写：JSP 相对 WebRoot，java 写 ./src/…，class 写 ./WEB-INF/classes/…。不必再配 src / WebRoot / 服务器路径。';
        projectInp.placeholder = '例如 C:\\ideaSpaces\\credit';
        outputInp.placeholder = '例如 D:\\packs\\TT' + todayStamp() + '（必须不存在或为空）';
        manifestTa.placeholder = SAMPLE_APP;
        batchBaseRow.style.display = 'none';
      }
      extractBtn.style.display = '';
      packageBtn.style.display = '';
      updateTarHint();
    }

    typeAppRadio.addEventListener('change', function () {
      if (typeAppRadio.checked) {
        currentType = 'app';
        if (!pkgInp.value || pkgInp.value.startsWith('DDD')) pkgInp.value = 'TT' + todayStamp() + 'qijunV1';
        syncTypeUI();
        persist();
      }
    });
    typeBatchRadio.addEventListener('change', function () {
      if (typeBatchRadio.checked) {
        currentType = 'batch';
        if (!pkgInp.value || pkgInp.value.startsWith('TT')) pkgInp.value = 'DDD' + todayStamp() + 'qijunV1';
        syncTypeUI();
        persist();
      }
    });

    function persistPreferenceFields() {
      savePreference({
        project_dir: projectInp.value,
        project_dir_history: saved.project_dir_history || [],
        output_dir: outputInp.value,
        auto_pair: autoPair.checked,
        output_policy: outputPolicy.value,
        pack_type: currentType,
        batch_base_dir: batchBaseInp.value
      });
    }
    function persistSessionDraft() {
      try {
        sessionStorage.setItem(SESSION_DRAFT_KEY, JSON.stringify({ package_name: pkgInp.value, manifest: manifestTa.value, pack_type: currentType }));
      } catch (_) { /* session draft is best effort */ }
      updateTarHint();
    }
    function persist() {
      persistPreferenceFields();
      persistSessionDraft();
    }
    [projectInp, outputInp, batchBaseInp].forEach(function (n) {
      n.addEventListener('input', persistPreferenceFields);
      n.addEventListener('change', persistPreferenceFields);
      n.addEventListener('blur', persistPreferenceFields);
    });
    [pkgInp, manifestTa].forEach(function (n) {
      n.addEventListener('input', persistSessionDraft);
      n.addEventListener('change', persistSessionDraft);
      n.addEventListener('blur', persistSessionDraft);
    });
    const status = el('div', { class: 'waspack-status', text: '粘贴清单后点「预检」，确认文件都从工程里找到再一键打包。' });
    const previewBox = el('div', { class: 'waspack-preview', style: 'display:none' });
    const resultBox = el('div', { class: 'waspack-result', style: 'display:none' });

    autoPair.addEventListener('change', persistPreferenceFields);
    outputPolicy.addEventListener('change', function () {
      persistPreferenceFields();
      if (outputPolicy.value === 'replace') status.textContent = '覆盖模式会清空目标目录；执行前仍需再次确认。';
    });

    function payload() {
      return {
        project_dir: projectInp.value.trim(),
        output_dir: outputInp.value.trim(),
        package_name: pkgBaseName(),
        auto_pair: autoPair.checked,
        manifest: manifestTa.value,
        output_policy: outputPolicy.value,
        pack_type: currentType,
        batch_base_dir: batchBaseInp.value.trim()
      };
    }

    function renderPreview(data) {
      previewBox.style.display = '';
      resultBox.style.display = 'none';
      const st = data.stats || {};
      const missing = data.missing || [];
      const files = data.files || [];
      const warns = data.warnings || [];
      const rows = files.slice(0, 400).map(function (f) {
        const tag = f.source === 'paired' ? '<span class="waspack-tag paired">补</span>' : '';
        return '<li><span class="waspack-kind ' + escapeHtml(f.kind) + '">' + kindLabel(f.kind) + '</span>' +
          tag + '<code>' + escapeHtml(f.rel) + '</code><span class="waspack-size">' + fmtBytes(f.bytes) + '</span></li>';
      }).join('');
      const missRows = missing.map(function (f) {
        return '<li class="missing"><code>' + escapeHtml(f.rel) + '</code></li>';
      }).join('');
      previewBox.innerHTML =
        '<div class="waspack-stats">' +
          '<span>将打包 <b>' + (st.total || 0) + '</b> 个</span>' +
          '<span>Java ' + (st.java || 0) + '</span>' +
          '<span>Class ' + (st.class || 0) + '</span>' +
          '<span>JSP ' + (st.jsp || 0) + '</span>' +
          '<span>其他 ' + (st.other || 0) + '</span>' +
          '<span>' + fmtBytes(st.bytes) + '</span>' +
          (missing.length ? '<span class="bad">缺失 ' + missing.length + '</span>' : '<span class="ok">清单文件均已找到</span>') +
        '</div>' +
        (warns.length ? '<div class="waspack-warn">' + warns.map(function (w) { return escapeHtml(w); }).join('<br>') + '</div>' : '') +
        (missing.length ? '<div class="waspack-miss-title">工程中找不到</div><ul class="waspack-filelist">' + missRows + '</ul>' : '') +
        '<div class="waspack-miss-title">将写入 tar / 脚本的相对路径</div>' +
        '<ul class="waspack-filelist">' + rows + (files.length > 400 ? '<li>…另有 ' + (files.length - 400) + ' 个</li>' : '') + '</ul>';
      if (missing.length) status.textContent = '预检有缺失文件，生成会被拒绝。请先在工程里编译 class，或改清单。';
      else status.textContent = '预检通过。当前输出策略：' + outputPolicy.options[outputPolicy.selectedIndex].text + '。';
    }

    function renderResult(data) {
      resultBox.style.display = '';
      const items = [
        { k: '输出目录', v: data.output_dir },
        { k: '清单文件', v: data.list_file }
      ];
      if (data.chmod_file) {
        items.push({ k: '赋权脚本', v: data.chmod_file });
      }
      items.push(
        { k: '备份脚本', v: data.backup_script },
        { k: '执行脚本', v: data.execute_script },
        { k: 'tar 压缩包', v: data.tar_file }
      );
      resultBox.innerHTML =
        '<div class="waspack-ok">✅ 投产打包完成：已打包 ' + (data.files || 0) + ' 个文件 · ' + fmtBytes(data.bytes) +
          (data.paired_added ? ' · 自动补了 ' + data.paired_added + ' 个 class/java' : '') + '</div>' +
        '<ul class="waspack-artifacts">' + items.map(function (it) {
          return '<li><span>' + escapeHtml(it.k) + '</span><code>' + escapeHtml(it.v || '') + '</code></li>';
        }).join('') + '</ul>';
    }

    function renderExtracted(data) {
      resultBox.style.display = '';
      resultBox.innerHTML = '<div class="waspack-ok">第 1 步完成：已抽取 ' + (data.files || 0) + ' 个文件 · ' + fmtBytes(data.bytes) + '</div>' +
        '<ul class="waspack-artifacts"><li><span>可核对/修改目录</span><code>' + escapeHtml(data.war_dir || '') + '</code></li></ul>' +
        '<div class="waspack-hint">确认 war 里的内容后，再点“2. 打包”。打包会读取 war 当前内容，因此本地修改会被保留。</div>';
    }

    async function doPreview() {
      persist();
      const body = payload();
      if (!body.project_dir) { toast('请选择本地工程目录', 'warn'); return; }
      if (!body.manifest.trim()) { toast('请粘贴清单', 'warn'); return; }
      status.textContent = '正在对照工程目录预检…';
      try {
        const r = await api('POST', '/api/waspack/preview', body);
        renderPreview(r);
        toast('预检完成：' + ((r.stats && r.stats.total) || 0) + ' 个文件', r.missing && r.missing.length ? 'warn' : 'ok');
      } catch (e) {
        previewBox.style.display = 'none';
        status.textContent = e.message || String(e);
        toast(e.message || String(e), 'err');
      }
    }

    async function doExtract() {
      persist();
      const body = payload();
      if (!body.project_dir) { toast('请选择工程目录', 'warn'); return; }
      if (!body.output_dir) { toast('请填写要生成的文件夹路径', 'warn'); return; }
      if (!body.manifest.trim()) { toast('请粘贴清单', 'warn'); return; }
      const confirmReplace = outputPolicy.value === 'replace' && window.confirm('覆盖模式会清空目标目录中的全部内容，确定继续吗？');
      if (outputPolicy.value === 'replace' && !confirmReplace) return;
      status.textContent = '正在把清单文件抽取到目标目录的 war 文件夹…';
      try {
        rememberProjectDir(body.project_dir);
        body.confirm_replace = confirmReplace;
        const r = await api('POST', '/api/waspack/extract', body);
        renderExtracted(r);
        status.textContent = '抽取完成。可先在 war 目录核对或修改，再执行第 2 步。';
        toast('已抽取到 war 文件夹', 'ok');
        try {
          await api('POST', '/api/waspack/open', { output_dir: r.output_dir });
        } catch (_) { /* 打开失败不阻断 */ }
      } catch (e) {
        const msg = e.message || String(e);
        status.textContent = msg;
        if (msg.indexOf('未包含 Kairo 产物标记') >= 0) {
          toast('目标目录非空且未包含 Kairo 产物标记。为保护文件安全，请选择空目录或新建目录。', 'warn');
        } else {
          toast(msg, 'err');
        }
      }
    }

    async function doPackage() {
      persist();
      const body = payload();
      if (!body.output_dir) { toast('请填写目标目录', 'warn'); return; }
      const confirmReplace = outputPolicy.value === 'replace' && window.confirm('覆盖模式会替换目标目录中的既有打包产物，确定继续吗？');
      if (outputPolicy.value === 'replace' && !confirmReplace) return;
      status.textContent = '正在按 war 当前内容打包，包内路径保持不变…';
      try {
        rememberProjectDir(body.project_dir);
        const r = await api('POST', '/api/waspack/package', { output_dir: body.output_dir, package_name: body.package_name, output_policy: body.output_policy, confirm_replace: confirmReplace, pack_type: body.pack_type, batch_base_dir: body.batch_base_dir });
        renderResult(r);
        status.textContent = '打包完成。';
        toast('已生成 ' + (r.package_name || '') + '.tar', 'ok');
      } catch (e) {
        const msg = e.message || String(e);
        status.textContent = msg;
        if (msg.indexOf('未包含 Kairo 产物标记') >= 0) {
          toast('目标目录非空且未包含 Kairo 产物标记。为保护文件安全，请选择空目录或新建目录。', 'warn');
        } else {
          toast(msg, 'err');
        }
      }
    }

    async function doBuild() {
      persist();
      const body = payload();
      if (!body.project_dir) { toast('请选择工程目录', 'warn'); return; }
      if (!body.output_dir) { toast('请填写要生成的文件夹路径', 'warn'); return; }
      if (!body.manifest.trim()) { toast('请粘贴清单', 'warn'); return; }
      const confirmReplace = outputPolicy.value === 'replace' && window.confirm('覆盖模式会清空目标目录中的全部内容，确定一键打包吗？');
      if (outputPolicy.value === 'replace' && !confirmReplace) return;
      status.textContent = '正在抽取并生成 tar / list / 脚本…';
      try {
        rememberProjectDir(body.project_dir);
        body.confirm_replace = confirmReplace;
        const r = await api('POST', '/api/waspack/build', body);
        previewBox.style.display = 'none';
        renderResult(r);
        status.textContent = '一键打包完成。';
        toast('一键打包完成：' + (r.package_name || '') + '.tar', 'ok');
      } catch (e) {
        const msg = e.message || String(e);
        status.textContent = msg;
        if (msg.indexOf('未包含 Kairo 产物标记') >= 0) {
          toast('目标目录非空且未包含 Kairo 产物标记。为保护文件安全，请选择空目录或新建目录。', 'warn');
        } else {
          toast(msg, 'err');
        }
      }
    }

    async function doOpenFolder() {
      persist();
      const dir = outputInp.value.trim();
      if (!dir) { toast('请先填写打包文件夹路径', 'warn'); return; }
      try {
        await api('POST', '/api/waspack/open', { output_dir: dir });
        toast('已打开 ' + dir, 'ok');
      } catch (e) {
        toast(e.message || String(e), 'err');
      }
    }

    const previewBtn = el('button', { type: 'button', class: 'btn', text: '预检清单', onclick: doPreview });
    const buildBtn = el('button', { type: 'button', class: 'btn btn-primary waspack-direct-build', text: '一键直接打包', onclick: doBuild, title: '自动抽取并直接生成 tar、list 和脚本' });
    const extractBtn = el('button', { type: 'button', class: 'btn btn-primary', text: '1. 一键抽取', onclick: doExtract });
    const packageBtn = el('button', { type: 'button', class: 'btn btn-primary', text: '2. 打包', onclick: doPackage });
    const openFolderBtn = el('button', { type: 'button', class: 'btn', text: '打开打包文件夹', onclick: doOpenFolder });
    const sampleBtn = el('button', { type: 'button', class: 'btn', text: '填入示例清单', onclick: function () {
      if (manifestTa.value.trim() && !confirm('覆盖当前清单？')) return;
      manifestTa.value = currentType === 'batch' ? SAMPLE_BATCH : SAMPLE_APP;
      persist();
    }});
    const copyListBtn = el('button', { type: 'button', class: 'btn', text: '复制清单', onclick: function () {
      copyToClipboard(manifestTa.value || '').then(
        function () { toast('已复制清单', 'ok'); },
        function () { toast('复制失败', 'err'); }
      );
    }});

    const typeSelector = el('div', { class: 'waspack-type-selector' }, [
      el('label', { class: 'waspack-type-option' }, [
        typeAppRadio,
        el('span', { class: 'waspack-type-text', text: '📱 应用代码（WAS / WAR）' })
      ]),
      el('label', { class: 'waspack-type-option' }, [
        typeBatchRadio,
        el('span', { class: 'waspack-type-text', text: '⚙️ 批量代码（Batch 批量工程）' })
      ])
    ]);

    syncTypeUI();

    view.appendChild(el('div', { class: 'waspack-page' }, [
      el('div', { class: 'card waspack-hero' }, [
        typeSelector,
        heroDesc
      ]),
      el('div', { class: 'card' }, [
        el('div', { class: 'waspack-grid' }, [
          pathRow('本地工程根目录', projectInp, { onPick: function (p) { rememberProjectDir(p); persist(); } }, projectHistorySelect),
          pathRow('打包目标目录', outputInp, { onPick: persist, title: '选择已有文件夹；新目录名可再手改' }),
          el('label', { class: 'waspack-field waspack-field-span' }, [
            el('span', { class: 'waspack-label', text: '包名（执行脚本名）' }),
            pkgInp,
            tarHint
          ]),
          batchBaseRow,
          el('label', { class: 'waspack-field waspack-output-policy' }, [
            el('span', { class: 'waspack-label', text: '输出目录处理' }),
            outputPolicy,
            el('span', { class: 'hint', text: '首次打包请指定空目录或不存在的目录；覆盖模式仅清空由 Kairo 既往生成的产物目录，避免误删非打包文件。' })
          ])
        ]),
        el('label', { class: 'waspack-check' }, [
          autoPair,
          document.createTextNode(' 清单只有 .java 时自动补对应 .class（含内部类）；只有 .class 时自动补 src 下 .java')
        ]),
        el('label', { class: 'waspack-field' }, [
          el('span', { class: 'waspack-label', text: '投产清单（每行一个相对路径）' }),
          manifestTa,
          el('span', { class: 'hint', text: '清单和当次包名仅保留在当前浏览器会话，不写入配置或偏好文件。' })
        ]),
        el('div', { class: 'waspack-actions' }, [buildBtn, previewBtn, extractBtn, packageBtn, openFolderBtn, sampleBtn, copyListBtn]),
        status,
        previewBox,
        resultBox
      ])
    ]));
    return persist;
  }

  Kairo.pages.waspack = renderWASPack;
  Kairo.state.routes.waspack = renderWASPack;
  Kairo.state.routeNames.waspack = '投产打包';
  Kairo.state.routeSubs.waspack = 'credit → tar';
})();
