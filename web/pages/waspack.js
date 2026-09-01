/* ===== web/pages/waspack.js =====
 * credit 投产打包：只选工程根 + 粘贴清单 → 按 src / WebRoot 抽取，生成 list.txt、BakTT*.sh、TT*.sh、tar
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard, lastGet, lastSet, escapeHtml } = Kairo.core;
  const { api, getPreference, putPreference, preferenceSaver, pathRow: localPathRow } = Kairo.api;
  const SESSION_DRAFT_KEY = 'kairo:waspack:session-draft';

  const SAMPLE = [
    './CreditManage/CreditApply/FixPrice/MiniFixPriceApplyList.jsp',
    './CreditManage/CreditLine/ProductInfo.jsp',
    './src/cn/com/jscb/www/loan/LoanMeasure/CompanyFixPriceRequestInfo.java',
    './WEB-INF/classes/cn/com/jscb/www/loan/LoanMeasure/MiniFixPriceRequestInfo.class'
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

  function pathRow(label, input, opts) {
    return el('label', { class: 'waspack-field' }, [
      el('span', { class: 'waspack-label', text: label }),
      localPathRow(input, Object.assign({ directory: true, rowClass: 'path-field waspack-path' }, opts || {}))
    ]);
  }

  async function renderWASPack(view) {
    const renderToken = view.dataset.renderToken;
    view.innerHTML = '<div class="card muted">正在恢复 WAS 打包偏好…</div>';
    let saved = {}, legacy = lastGet('waspack', 'form') || {}, preferenceAvailable = false;
    try {
      const remote = await getPreference('waspack');
      saved = remote && remote.value && typeof remote.value === 'object' ? remote.value : {};
      if (!remote.exists && Object.keys(legacy).length) {
        saved = { project_dir: legacy.project_dir || '', output_dir: legacy.output_dir || '', auto_pair: legacy.auto_pair !== false };
        await putPreference('waspack', saved);
      }
      preferenceAvailable = true;
    } catch (e) {
      saved = { project_dir: legacy.project_dir || '', output_dir: legacy.output_dir || '', auto_pair: legacy.auto_pair !== false };
    }
    let sessionDraft = {};
    try { sessionDraft = JSON.parse(sessionStorage.getItem(SESSION_DRAFT_KEY) || '{}'); } catch (_) { sessionDraft = {}; }
    if (!Object.keys(sessionDraft).length && legacy && (legacy.package_name || legacy.manifest)) {
      sessionDraft = { package_name: legacy.package_name || '', manifest: legacy.manifest || '' };
      try { sessionStorage.setItem(SESSION_DRAFT_KEY, JSON.stringify(sessionDraft)); } catch (_) {}
    }
    if (preferenceAvailable) lastSet('waspack', 'form', null);
    else lastSet('waspack', 'form', { project_dir: saved.project_dir || '', output_dir: saved.output_dir || '', auto_pair: saved.auto_pair !== false });
    if (view.dataset.renderToken !== renderToken) return;
    view.innerHTML = '';
    const projectInp = el('input', {
      type: 'text', id: 'waspack-project',
      placeholder: '例如 C:\\ideaSpaces\\credit',
      spellcheck: 'false'
    });
    const outputInp = el('input', {
      type: 'text', id: 'waspack-output',
      placeholder: '例如 D:\\packs\\TT' + todayStamp() + '（必须不存在或为空）',
      spellcheck: 'false'
    });
    const pkgInp = el('input', {
      type: 'text', id: 'waspack-pkg',
      placeholder: '例如 TT20260709qijunV1',
      spellcheck: 'false',
      autocomplete: 'off'
    });
    const tarHint = el('span', { class: 'waspack-tar-hint', text: '' });
    const autoPair = el('input', { type: 'checkbox', id: 'waspack-autopair' });
    autoPair.checked = true;
    const manifestTa = el('textarea', {
      id: 'waspack-manifest',
      spellcheck: 'false',
      placeholder: SAMPLE
    });
    [projectInp, outputInp, pkgInp].forEach(function (n) {
      n.setAttribute('autocomplete', 'off');
    });

    function hasSaved(key) {
      return saved && Object.prototype.hasOwnProperty.call(saved, key);
    }
    if (hasSaved('project_dir')) projectInp.value = saved.project_dir || '';
    if (hasSaved('output_dir')) outputInp.value = saved.output_dir || '';
    if (sessionDraft.package_name && String(sessionDraft.package_name).trim()) pkgInp.value = sessionDraft.package_name;
    else pkgInp.value = 'TT' + todayStamp();
    if (typeof saved.auto_pair === 'boolean') autoPair.checked = saved.auto_pair;
    if (sessionDraft.manifest) manifestTa.value = sessionDraft.manifest;

    const savePreference = preferenceSaver('waspack', 500);

    function pkgBaseName() {
      return (pkgInp.value || '').trim().replace(/\.(tar|sh)$/i, '');
    }
    function updateTarHint() {
      const n = pkgBaseName();
      if (!n) {
        tarHint.textContent = '将生成 包名.tar、包名.sh、Bak包名.sh、list.txt';
        return;
      }
      tarHint.textContent = '将生成 ' + n + '.tar  ·  ' + n + '.sh  ·  Bak' + n + '.sh  ·  list.txt';
    }
    function persistPreferenceFields() {
      savePreference({
        project_dir: projectInp.value,
        output_dir: outputInp.value,
        auto_pair: autoPair.checked
      });
    }
    function persistSessionDraft() {
      try {
        sessionStorage.setItem(SESSION_DRAFT_KEY, JSON.stringify({ package_name: pkgInp.value, manifest: manifestTa.value }));
      } catch (_) { /* session draft is best effort */ }
      updateTarHint();
    }
    function persist() {
      persistPreferenceFields();
      persistSessionDraft();
    }
    [projectInp, outputInp].forEach(function (n) {
      n.addEventListener('input', persistPreferenceFields);
      n.addEventListener('change', persistPreferenceFields);
      n.addEventListener('blur', persistPreferenceFields);
    });
    [pkgInp, manifestTa].forEach(function (n) {
      n.addEventListener('input', persistSessionDraft);
      n.addEventListener('change', persistSessionDraft);
      n.addEventListener('blur', persistSessionDraft);
    });
    autoPair.addEventListener('change', persistPreferenceFields);
    updateTarHint();

    const status = el('div', { class: 'waspack-status', text: '粘贴清单后点「预检」，确认 java / class / jsp 都从工程里找到再生成。' });
    const previewBox = el('div', { class: 'waspack-preview', style: 'display:none' });
    const resultBox = el('div', { class: 'waspack-result', style: 'display:none' });

    function payload() {
      return {
        project_dir: projectInp.value.trim(),
        output_dir: outputInp.value.trim(),
        package_name: pkgBaseName(),
        auto_pair: autoPair.checked,
        manifest: manifestTa.value
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
      else status.textContent = '预检通过。输出目录必须是空文件夹（不存在则自动创建，不覆盖）。';
    }

    function renderResult(data) {
      resultBox.style.display = '';
      const items = [
        { k: '输出目录', v: data.output_dir },
        { k: '清单', v: data.list_file },
        { k: '备份脚本', v: data.backup_script },
        { k: '执行脚本', v: data.execute_script },
        { k: 'tar 包', v: data.tar_file }
      ];
      resultBox.innerHTML =
        '<div class="waspack-ok">已生成 ' + (data.files || 0) + ' 个文件 · ' + fmtBytes(data.bytes) +
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
      if (!body.project_dir) { toast('请选择 credit 工程目录', 'warn'); return; }
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
      if (!body.project_dir) { toast('请选择 credit 工程目录', 'warn'); return; }
      if (!body.output_dir) { toast('请填写要生成的文件夹路径', 'warn'); return; }
      if (!body.manifest.trim()) { toast('请粘贴清单', 'warn'); return; }
      status.textContent = '正在把清单文件抽取到目标目录的 war 文件夹…';
      try {
        const r = await api('POST', '/api/waspack/extract', body);
        renderExtracted(r);
        status.textContent = '抽取完成。可先在 war 目录核对或修改，再执行第 2 步。';
        toast('已抽取到 war 文件夹', 'ok');
        try {
          await api('POST', '/api/waspack/open', { output_dir: r.output_dir });
        } catch (_) { /* 打开失败不阻断 */ }
      } catch (e) {
        status.textContent = e.message || String(e);
        toast(e.message || String(e), 'err');
      }
    }

    async function doPackage() {
      persist();
      const body = payload();
      if (!body.output_dir) { toast('请填写目标目录', 'warn'); return; }
      status.textContent = '正在按 war 当前内容打包，包内路径保持不变…';
      try {
        const r = await api('POST', '/api/waspack/package', { output_dir: body.output_dir, package_name: body.package_name });
        renderResult(r);
        status.textContent = '打包完成。';
        toast('已生成 ' + (r.package_name || '') + '.tar', 'ok');
      } catch (e) {
        status.textContent = e.message || String(e);
        toast(e.message || String(e), 'err');
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
    const extractBtn = el('button', { type: 'button', class: 'btn btn-primary', text: '1. 一键抽取', onclick: doExtract });
    const packageBtn = el('button', { type: 'button', class: 'btn btn-primary', text: '2. 打包', onclick: doPackage });
    const openFolderBtn = el('button', { type: 'button', class: 'btn', text: '打开打包文件夹', onclick: doOpenFolder });
    const sampleBtn = el('button', { type: 'button', class: 'btn', text: '填入示例清单', onclick: function () {
      if (manifestTa.value.trim() && !confirm('覆盖当前清单？')) return;
      manifestTa.value = SAMPLE;
      persist();
    }});
    const copyListBtn = el('button', { type: 'button', class: 'btn', text: '复制清单', onclick: function () {
      copyToClipboard(manifestTa.value || '').then(
        function () { toast('已复制清单', 'ok'); },
        function () { toast('复制失败', 'err'); }
      );
    }});

    view.appendChild(el('div', { class: 'waspack-page' }, [
      el('div', { class: 'card waspack-hero' }, [
        el('div', { class: 'card-desc', text: '只选本地 credit 工程根（例如 C:\\ideaSpaces\\credit）。清单按投产相对路径写：JSP 相对 WebRoot，java 写 ./src/…，class 写 ./WEB-INF/classes/…。不必再配 src / WebRoot / 服务器路径。' })
      ]),
      el('div', { class: 'card' }, [
        el('div', { class: 'waspack-grid' }, [
          pathRow('本地 credit 工程', projectInp, { onPick: persist }),
          pathRow('目标目录（抽取到其 war 文件夹）', outputInp, { onPick: persist, title: '选择已有文件夹；新目录名可再手改' }),
          el('label', { class: 'waspack-field waspack-field-span' }, [
            el('span', { class: 'waspack-label', text: '包名（执行脚本名）' }),
            pkgInp,
            tarHint
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
        el('div', { class: 'waspack-actions' }, [previewBtn, extractBtn, packageBtn, openFolderBtn, sampleBtn, copyListBtn]),
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
