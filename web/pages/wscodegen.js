/* ===== web/pages/wscodegen.js =====
 * WSDL → Java 客户端代码生成。
 *
 * 兼容策略写进页面，而不是藏在默认值里：
 *   - 优先用工程 JDK Home + 工程 lib 里的 axis/xfire/cxf jar 跑官方工具
 *   - 本机 PATH 上的 JDK 只作探测结果，不默默拿来生成
 *   - 内置模式永远可用来出 Java 1.6 源码；portable 零依赖
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard } = Kairo.core;
  const { api, postJSON, getJSON } = Kairo.api;
  const LS_KEY = 'kairo:wscodegen:form';
  const ENGINE_HINT = {
    portable: '零依赖，Java 1.6，编不过栈时用这个',
    jaxws: 'JDK 6/8 自带 javax.xml.ws',
    cxf: '工程 lib 里有 cxf-*.jar',
    axis1: 'axis.jar + jaxrpc，WebSphere 6/7',
    axis2: 'axis2-*.jar / axiom',
    xfire: 'xfire-all / xfire-core 1.2.6，信贷老工程'
  };

  const state = {
    engines: [],
    projects: [],
    jdks: [],
    scan: null,
    preview: null,
    selectedFile: 0,
    busy: false,
    form: {
      source: 'project',
      wsdl_project_id: '',
      wsdl_file: '',
      wsdl_url: '',
      wsdl_content: '',
      engine: 'portable',
      mode: 'builtin',
      package: '',
      java_source: '1.6',
      jaxws_target: '2.1',
      jdk_home: '',
      project_dir: '',
      output_dir: '',
      classpath_jars: [],
      extra_flags: '',
      include_main: true,
      overwrite: true,
      open_after: true
    }
  };

  function loadForm() {
    try {
      const saved = JSON.parse(localStorage.getItem(LS_KEY) || '{}');
      Object.keys(state.form).forEach(function (k) {
        if (saved[k] !== undefined) state.form[k] = saved[k];
      });
    } catch (e) { /* ignore */ }
  }
  function saveForm() {
    try { localStorage.setItem(LS_KEY, JSON.stringify(state.form)); } catch (e) { /* ignore */ }
  }

  function currentEngine() {
    for (let i = 0; i < state.engines.length; i++) {
      if (state.engines[i].id === state.form.engine) return state.engines[i];
    }
    return null;
  }

  function hashProjectId() {
    const hash = location.hash || '';
    const m = hash.match(/[?&]project=([^&]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  async function chooseDir(setter) {
    try {
      const r = await postJSON('/api/choose-dir', {});
      if (r && r.path) setter(r.path);
    } catch (e) {
      toast(e.message || '选择目录失败', 'err');
    }
  }
  async function chooseFile(setter) {
    try {
      const r = await postJSON('/api/choose-file', {});
      if (r && r.path) setter(r.path);
    } catch (e) {
      toast(e.message || '选择文件失败', 'err');
    }
  }

  function field(label, control, hint) {
    const kids = [el('div', { class: 'lbl', text: label }), control];
    if (hint) kids.push(el('div', { class: 'wsc-hint', text: hint }));
    return el('div', { class: 'field' }, kids);
  }

  function pathRow(input, onBrowse) {
    const row = el('div', { class: 'wsc-path-row' }, [
      input,
      el('button', { class: 'btn', type: 'button', text: '浏览…', onclick: onBrowse })
    ]);
    return row;
  }

  function bindInput(input, key) {
    input.value = state.form[key] || '';
    input.addEventListener('input', function () {
      state.form[key] = input.value;
      saveForm();
    });
    return input;
  }

  function radioGroup(label, options, current, onPick) {
    const group = el('div', { class: 'wsc-radio-row', role: 'radiogroup', 'aria-label': label });
    options.forEach(function (opt) {
      const on = current === opt.value;
      group.appendChild(el('button', {
        type: 'button',
        role: 'radio',
        'aria-checked': on ? 'true' : 'false',
        class: 'wsc-radio' + (on ? ' active' : ''),
        disabled: !!opt.disabled,
        title: opt.title || '',
        onclick: function () {
          if (opt.disabled) return;
          onPick(opt.value);
        }
      }, [
        el('span', { class: 'wsc-radio-dot', 'aria-hidden': 'true' }),
        el('span', { class: 'wsc-radio-label', text: opt.label })
      ]));
    });
    return group;
  }

  function renderIntro() {
    return el('div', { class: 'wsc-banner' }, [
      el('strong', { text: '按工程里的栈出客户端' }),
      el('span', { text: '引擎、生成方式、源码级别都是单选。不确定就扫 WEB-INF/lib，或选纯 JDK。' })
    ]);
  }

  function renderSourceCard() {
    const tabs = [
      { id: 'project', label: '已导入项目' },
      { id: 'file', label: '本地 WSDL' },
      { id: 'url', label: 'WSDL URL' },
      { id: 'paste', label: '粘贴内容' }
    ];
    const tabBar = el('div', { class: 'wsc-seg', role: 'radiogroup', 'aria-label': 'WSDL 来源' });
    tabs.forEach(function (t) {
      tabBar.appendChild(el('button', {
        type: 'button',
        role: 'radio',
        'aria-checked': state.form.source === t.id ? 'true' : 'false',
        class: 'wsc-seg-btn' + (state.form.source === t.id ? ' active' : ''),
        text: t.label,
        onclick: function () {
          state.form.source = t.id;
          saveForm();
          rerender();
        }
      }));
    });

    const body = el('div', { class: 'wsc-source-body' });
    if (state.form.source === 'project') {
      const sel = el('select');
      sel.appendChild(el('option', { value: '', text: state.projects.length ? '选择已导入的 WSDL 项目' : '还没有导入过 WSDL，请先去 WebService 页' }));
      state.projects.forEach(function (p) {
        const opt = el('option', { value: p.id, text: (p.name || p.id) + (p.source_url ? ' · ' + p.source_url : '') });
        sel.appendChild(opt);
      });
      sel.value = state.form.wsdl_project_id || '';
      sel.addEventListener('change', function () {
        state.form.wsdl_project_id = sel.value;
        saveForm();
      });
      body.appendChild(field('WSDL 项目', sel, '复用 WebService 调试中心已经解析好的项目，含 operation 树。'));
      body.appendChild(el('button', {
        class: 'btn btn-sm', type: 'button', text: '去 WebService 导入',
        onclick: function () { location.hash = '#/webservice'; }
      }));
    } else if (state.form.source === 'file') {
      const inp = bindInput(el('input', { type: 'text', placeholder: 'D:\\proj\\wsdl\\service.wsdl' }), 'wsdl_file');
      body.appendChild(field('本地 WSDL 文件', pathRow(inp, function () {
        chooseFile(function (p) { state.form.wsdl_file = p; saveForm(); rerender(); });
      }), '同目录的 .xsd 会自动带上。恒力这类拆 XSD 的 WSDL 必须用文件，或先在 WebService 页连 XSD 一起导入。'));
    } else if (state.form.source === 'url') {
      body.appendChild(field('WSDL URL', bindInput(el('input', { type: 'text', placeholder: 'http://10.0.0.1:8080/svc?wsdl' }), 'wsdl_url'),
        '内置模式会先拉取文本；官方工具模式可把 URL 直接交给生成器。'));
    } else {
      const ta = bindInput(el('textarea', { rows: '5', placeholder: '把 .wsdl 文本粘贴到这里…', spellcheck: 'false' }), 'wsdl_content');
      ta.className = 'wsc-paste';
      body.appendChild(field('WSDL 文本', ta));
    }

    return el('div', { class: 'card wsc-card-compact' }, [
      el('h3', { text: 'WSDL 来源' }),
      tabBar,
      body
    ]);
  }

  function renderEngineCard() {
    const grid = el('div', { class: 'wsc-engine-grid', role: 'radiogroup', 'aria-label': '目标引擎' });
    (state.engines.length ? state.engines : [{
      id: 'portable', name: '纯 JDK HttpURLConnection', summary: '零依赖 Java 1.6', when_to_use: ''
    }]).forEach(function (eng) {
      const active = state.form.engine === eng.id;
      const hint = ENGINE_HINT[eng.id] || eng.summary || '';
      const card = el('button', {
        type: 'button',
        role: 'radio',
        'aria-checked': active ? 'true' : 'false',
        class: 'wsc-engine' + (active ? ' active' : ''),
        'data-engine': eng.id,
        title: eng.when_to_use || eng.summary || '',
        onclick: function () {
          state.form.engine = eng.id;
          if (eng.id === 'portable') state.form.mode = 'builtin';
          if (eng.id === 'xfire' || eng.id === 'axis1') state.form.mode = 'builtin';
          saveForm();
          rerender();
        }
      }, [
        el('span', { class: 'wsc-radio-dot', 'aria-hidden': 'true' }),
        el('div', { class: 'wsc-engine-body' }, [
          el('div', { class: 'wsc-engine-name', text: eng.name }),
          el('div', { class: 'wsc-engine-sum', text: hint })
        ])
      ]);
      grid.appendChild(card);
    });

    const eng = currentEngine();
    const detail = el('div', { class: 'wsc-engine-detail' });
    if (state.form.engine === 'xfire') {
      detail.appendChild(el('div', { class: 'wsc-note', text: '信贷工程常见：xfire-all + 拆开的 core/aegis/spring/jaxb2。没有 xfire-generator 时用内置动态 Client，不要开官方工具。' }));
    } else if (eng && eng.compile_notes && eng.compile_notes[0]) {
      detail.appendChild(el('div', { class: 'wsc-note', text: eng.compile_notes[0] }));
    }

    const modeBar = radioGroup('生成方式', [
      { value: 'builtin', label: '内置生成' },
      { value: 'tool', label: '官方工具', disabled: state.form.engine === 'portable', title: '需要工程 JDK + generator / wsimport' }
    ], state.form.mode, function (v) {
      state.form.mode = v;
      saveForm();
      rerender();
    });

    if (state.form.engine === 'xfire' && state.form.mode === 'tool') {
      detail.appendChild(el('div', { class: 'wsc-warn', text: '这份工程通常没有 xfire-generator.jar，官方 Wsdl11Generator 容易失败。建议改回内置。' }));
    }

    return el('div', { class: 'card wsc-card-compact' }, [
      el('h3', { text: '目标引擎' }),
      el('div', { class: 'card-desc', text: '单选。选工程真正在用的栈，选错 import 一定编不过。' }),
      grid,
      el('div', { class: 'wsc-mode-label', text: '生成方式（单选）' }),
      modeBar,
      detail
    ]);
  }

  function renderProjectCard() {
    const projInp = bindInput(el('input', { type: 'text', placeholder: '工程根目录或 WEB-INF/lib 的上一级' }), 'project_dir');
    const kids = [
      el('h3', { text: '工程扫描' }),
      el('div', { class: 'card-desc', text: '扫 WEB-INF/lib 猜栈。XFire 拆包、没有 generator 时会提示用内置。' }),
      field('项目目录', pathRow(projInp, function () {
        chooseDir(function (p) { state.form.project_dir = p; saveForm(); rerender(); scanProject(); });
      })),
      el('div', { class: 'btn-row' }, [
        el('button', { class: 'btn btn-primary', type: 'button', text: '扫描 lib', onclick: scanProject })
      ])
    ];

    if (state.form.mode === 'tool') {
      const jdkSel = el('select', { id: 'wsc-jdk-select' });
      jdkSel.appendChild(el('option', { value: '', text: state.jdks.length ? '选择探测到的 JDK' : '尚未探测到 JDK' }));
      state.jdks.forEach(function (j, idx) {
        const label = (j.version || 'unknown') + ' · major ' + j.major + ' · ' + j.source + (j.has_wsimport ? ' · wsimport' : '') + ' · ' + j.home;
        jdkSel.appendChild(el('option', { value: String(idx), text: label }));
        if (state.form.jdk_home && j.home === state.form.jdk_home) jdkSel.value = String(idx);
      });
      jdkSel.addEventListener('change', function () {
        const j = state.jdks[Number(jdkSel.value)];
        state.form.jdk_home = j ? j.home : '';
        saveForm();
        rerender();
      });
      const jdkInp = bindInput(el('input', { type: 'text', placeholder: '例如 D:\\jdk1.6.0_45' }), 'jdk_home');
      kids.push(field('JDK Home（官方工具才需要）', pathRow(jdkInp, function () {
        chooseDir(function (p) { state.form.jdk_home = p; saveForm(); rerender(); detectJdk(p); });
      }), '优先工程自带的 jdk1.6 / jdk1.8，不要用本机 JDK 17。'));
      kids.push(field('探测到的 JDK', jdkSel));
      state.jdks.forEach(function (j) {
        if (j.home === state.form.jdk_home && j.notes) {
          kids.push(el('div', { class: 'wsc-warn', text: j.notes }));
        }
      });
      kids.push(el('div', { class: 'btn-row' }, [
        el('button', { class: 'btn', type: 'button', text: '重新探测 JDK', onclick: function () { detectJdk(state.form.jdk_home); } })
      ]));
    }

    if (state.scan) {
      const scanBox = el('div', { class: 'wsc-scan' });
      scanBox.appendChild(el('div', { class: 'wsc-scan-head', text: '建议引擎：' + (state.scan.suggested_engine || 'portable') + ' · ' + ((state.scan.jars || []).length) + ' 个相关 jar' }));
      (state.scan.notes || []).forEach(function (n) {
        scanBox.appendChild(el('div', { class: 'wsc-note', text: n }));
      });
      if (state.scan.missing_for_suggest && state.scan.missing_for_suggest.length) {
        scanBox.appendChild(el('div', { class: 'wsc-warn', text: '还缺：' + state.scan.missing_for_suggest.join('、') }));
      }
      const jarList = el('div', { class: 'wsc-jar-list' });
      (state.scan.jars || []).forEach(function (jar) {
        const checked = state.form.classpath_jars.indexOf(jar.path) >= 0;
        const cb = el('input', { type: 'checkbox' });
        cb.checked = checked;
        cb.addEventListener('change', function () {
          const next = state.form.classpath_jars.slice();
          const i = next.indexOf(jar.path);
          if (cb.checked && i < 0) next.push(jar.path);
          if (!cb.checked && i >= 0) next.splice(i, 1);
          state.form.classpath_jars = next;
          saveForm();
        });
        jarList.appendChild(el('label', { class: 'wsc-jar' }, [
          cb,
          el('span', { class: 'wsc-jar-kind', text: jar.kind }),
          el('span', { text: jar.file_name })
        ]));
      });
      scanBox.appendChild(jarList);
      kids.push(scanBox);
    }

    return el('div', { class: 'card wsc-card-compact' }, kids);
  }

  function renderOptionsCard() {
    const pkg = bindInput(el('input', { type: 'text', placeholder: 'com.example.ws' }), 'package');
    const kids = [
      el('h3', { text: '生成选项' }),
      field('Java 包名', pkg, '空则从 targetNamespace 反转'),
      el('div', { class: 'wsc-mode-label', text: '源码级别（单选）' }),
      radioGroup('源码级别', [
        { value: '1.6', label: 'Java 1.6' },
        { value: '1.8', label: 'Java 1.8' }
      ], state.form.java_source, function (v) {
        state.form.java_source = v;
        saveForm();
        rerender();
      })
    ];
    if (state.form.engine === 'jaxws') {
      kids.push(el('div', { class: 'wsc-mode-label', text: 'JAX-WS target（单选）' }));
      kids.push(radioGroup('JAX-WS target', [
        { value: '2.1', label: '2.1 · JDK 6' },
        { value: '2.2', label: '2.2 · JDK 8' }
      ], state.form.jaxws_target, function (v) {
        state.form.jaxws_target = v;
        saveForm();
        rerender();
      }));
    }
    if (state.form.mode === 'tool') {
      kids.push(field('官方工具额外参数', bindInput(el('input', { type: 'text', placeholder: '例如 -verbose' }), 'extra_flags')));
    }
    function chk(key, label) {
      const box = el('input', { type: 'checkbox' });
      box.checked = !!state.form[key];
      box.addEventListener('change', function () { state.form[key] = box.checked; saveForm(); });
      return el('label', { class: 'wsc-check' }, [box, document.createTextNode(label)]);
    }
    kids.push(el('div', { class: 'wsc-checks' }, [
      chk('include_main', '附带 Main'),
      chk('overwrite', '覆盖已有文件'),
      chk('open_after', '生成后打开目录')
    ]));
    return el('div', { class: 'card wsc-card-compact' }, kids);
  }

  function renderActionsAndPreview() {
    const log = el('pre', { class: 'wsc-log', id: 'wsc-log' });
    if (state.preview) {
      if (state.preview.command) log.textContent += '命令：\n' + state.preview.command + '\n\n';
      if (state.preview.tool_log) log.textContent += state.preview.tool_log + '\n';
      (state.preview.warnings || []).concat(state.preview.notes || []).forEach(function (n) {
        log.textContent += n + '\n';
      });
      if (!log.textContent) log.textContent = '已生成 ' + ((state.preview.files || []).length) + ' 个预览文件。';
    } else {
      log.textContent = '左侧配好后来这里预览。兼容性说明和官方工具日志会出现在上方。';
    }

    const fileCol = el('div', { class: 'wsc-files', id: 'wsc-files' });
    const files = (state.preview && state.preview.files) || [];
    files.forEach(function (f, idx) {
      fileCol.appendChild(el('button', {
        type: 'button',
        class: 'wsc-file' + (idx === state.selectedFile ? ' active' : ''),
        text: f.rel_path,
        onclick: function () { showFile(idx); }
      }));
    });
    const code = el('pre', { class: 'wsc-code', id: 'wsc-code' });
    if (files[state.selectedFile]) code.textContent = files[state.selectedFile].content || '';
    else code.textContent = '// 还没有预览';

    const outInp = bindInput(el('input', { type: 'text', placeholder: '例如 D:\\proj\\src' }), 'output_dir');
    const btnPreview = el('button', {
      class: 'btn btn-primary', type: 'button', id: 'wsc-preview-btn',
      text: state.busy ? '处理中…' : '预览代码',
      disabled: state.busy,
      onclick: function () { runGenerate(true); }
    });
    const btnGen = el('button', {
      class: 'btn btn-primary', type: 'button', id: 'wsc-generate-btn',
      text: '生成到目录',
      disabled: state.busy,
      onclick: function () { runGenerate(false); }
    });
    const btnCopy = el('button', {
      class: 'btn', type: 'button', text: '复制当前文件',
      onclick: function () {
        const f = files[state.selectedFile];
        if (!f) return;
        copyToClipboard(f.content || '').then(function () { toast('已复制', 'ok'); }, function () { toast('复制失败', 'err'); });
      }
    });

    return el('div', { class: 'card wsc-card-compact wsc-preview-card', id: 'wsc-preview-card' }, [
      el('h3', { text: '预览与写出' }),
      field('输出目录', pathRow(outInp, function () {
        chooseDir(function (p) { state.form.output_dir = p; saveForm(); rerender(); });
      })),
      el('div', { class: 'wsc-actions btn-row' }, [btnPreview, btnGen, btnCopy]),
      log,
      el('div', { class: 'wsc-preview' }, [fileCol, code])
    ]);
  }

  function renderShell() {
    const left = el('div', { class: 'wsc-pane wsc-pane-config' }, [
      renderIntro(),
      renderSourceCard(),
      renderEngineCard(),
      renderOptionsCard(),
      renderProjectCard()
    ]);
    const right = el('div', { class: 'wsc-pane wsc-pane-preview' }, [
      renderActionsAndPreview()
    ]);
    return el('div', { class: 'wsc-shell' }, [left, right]);
  }

  function collectPayload(dryRun) {
    const f = state.form;
    const extra = String(f.extra_flags || '').trim();
    const payload = {
      engine: f.engine,
      mode: f.mode,
      package: f.package,
      output_dir: f.output_dir,
      overwrite: !!f.overwrite,
      include_main: !!f.include_main,
      java_source: f.java_source,
      jaxws_target: f.jaxws_target,
      jdk_home: f.jdk_home,
      classpath_jars: f.classpath_jars || [],
      extra_flags: extra ? extra.split(/\s+/) : [],
      open_after: !dryRun && !!f.open_after,
      dry_run: !!dryRun
    };
    if (f.source === 'project') payload.wsdl_project_id = f.wsdl_project_id;
    if (f.source === 'file') payload.wsdl_file = f.wsdl_file;
    if (f.source === 'url') payload.wsdl_url = f.wsdl_url;
    if (f.source === 'paste') payload.wsdl_content = f.wsdl_content;
    return payload;
  }

  function missingSource() {
    const f = state.form;
    if (f.source === 'project' && !String(f.wsdl_project_id || '').trim()) {
      return '请先选择已导入的 WSDL 项目，或改用文件 / URL / 粘贴';
    }
    if (f.source === 'file' && !String(f.wsdl_file || '').trim()) {
      return '请选择本地 WSDL 文件';
    }
    if (f.source === 'url' && !String(f.wsdl_url || '').trim()) {
      return '请填写 WSDL URL';
    }
    if (f.source === 'paste' && !String(f.wsdl_content || '').trim()) {
      return '请粘贴 WSDL 文本';
    }
    return '';
  }

  function pickPreviewFile() {
    const files = (state.preview && state.preview.files) || [];
    let idx = 0;
    for (let i = 0; i < files.length; i++) {
      const p = files[i].rel_path || '';
      if (/Client\.java$/.test(p)) {
        idx = i;
        break;
      }
    }
    state.selectedFile = idx;
  }

  function showFile(idx) {
    state.selectedFile = idx;
    const files = (state.preview && state.preview.files) || [];
    const code = document.getElementById('wsc-code');
    if (code) code.textContent = (files[idx] && files[idx].content) || '// 还没有预览';
    const list = document.getElementById('wsc-files');
    if (list) {
      const buttons = list.querySelectorAll('.wsc-file');
      for (let i = 0; i < buttons.length; i++) {
        if (i === idx) buttons[i].classList.add('active');
        else buttons[i].classList.remove('active');
      }
    }
  }

  function setBusyUI(on) {
    state.busy = !!on;
    const preview = document.getElementById('wsc-preview-btn');
    const gen = document.getElementById('wsc-generate-btn');
    if (preview) {
      preview.disabled = state.busy;
      preview.textContent = state.busy ? '处理中…' : '预览代码';
    }
    if (gen) gen.disabled = state.busy;
  }

  async function runGenerate(dryRun) {
    if (state.busy) return;
    const miss = missingSource();
    if (miss) {
      toast(miss, 'warn');
      return;
    }
    const payload = collectPayload(dryRun);
    if (!dryRun && !payload.output_dir) {
      toast('请指定输出目录', 'warn');
      return;
    }
    if (payload.mode === 'tool' && payload.engine === 'portable') {
      toast('portable 没有官方工具，请用内置生成', 'warn');
      return;
    }
    setBusyUI(true);
    try {
      const path = dryRun ? '/api/wscodegen/preview' : '/api/wscodegen/generate';
      const r = await postJSON(path, payload);
      state.preview = r.result || r;
      pickPreviewFile();
      toast(dryRun ? '预览完成' : ('已写出 ' + ((state.preview.written || []).length) + ' 个文件'), 'ok');
    } catch (e) {
      toast(e.message || '生成失败', 'err');
    } finally {
      state.busy = false;
      rerender();
      const card = document.getElementById('wsc-preview-card');
      if (card && state.preview) card.scrollIntoView({ block: 'nearest' });
    }
  }

  function patchJdkSelect() {
    const sel = document.getElementById('wsc-jdk-select');
    if (!sel) return false;
    const prev = state.form.jdk_home;
    sel.innerHTML = '';
    sel.appendChild(el('option', { value: '', text: state.jdks.length ? '选择探测到的 JDK（可空，官方工具才需要）' : '尚未探测到 JDK' }));
    state.jdks.forEach(function (j, idx) {
      const label = (j.version || 'unknown') + ' · major ' + j.major + ' · ' + j.source + (j.has_wsimport ? ' · wsimport' : '') + ' · ' + j.home;
      sel.appendChild(el('option', { value: String(idx), text: label }));
      if (prev && j.home === prev) sel.value = String(idx);
    });
    return true;
  }

  async function detectJdk(home, silent) {
    try {
      const r = await postJSON('/api/wscodegen/detect-jdk', { jdk_home: home || '' });
      state.jdks = r.jdks || [];
      if (home && state.jdks.length) {
        state.form.jdk_home = home;
        saveForm();
      } else if (!state.form.jdk_home && state.jdks.length) {
        const jdk6 = state.jdks.filter(function (j) { return j.major === 6 && j.has_wsimport; })[0];
        const jdk8 = state.jdks.filter(function (j) { return j.major === 8 && j.has_wsimport; })[0];
        const pick = jdk6 || jdk8;
        if (pick && pick.source !== 'PATH') state.form.jdk_home = pick.home;
        saveForm();
      }
      if (!silent) toast('探测到 ' + state.jdks.length + ' 个 JDK', 'ok');
      if (silent) {
        if (patchJdkSelect() || state.form.mode !== 'tool') return;
      }
      rerender();
    } catch (e) {
      if (!silent) toast(e.message || '探测 JDK 失败', 'err');
    }
  }

  async function scanProject() {
    if (!state.form.project_dir) {
      toast('请先选择项目目录', 'warn');
      return;
    }
    try {
      const r = await postJSON('/api/wscodegen/scan-project', { project_dir: state.form.project_dir });
      state.scan = r.scan || r;
      const jars = (state.scan.jars || []).map(function (j) { return j.path; });
      state.form.classpath_jars = jars;
      if (state.scan.suggested_engine) {
        state.form.engine = state.scan.suggested_engine;
        if (state.form.engine === 'xfire' || state.form.engine === 'axis1') {
          state.form.mode = 'builtin';
        }
        toast('已按扫描结果选中引擎：' + state.form.engine + '。可手动改。', 'ok');
      }
      saveForm();
      rerender();
    } catch (e) {
      toast(e.message || '扫描失败', 'err');
    }
  }

  let root = null;
  function rerender() {
    if (!root) return;
    const cfg = root.querySelector('.wsc-pane-config');
    const y = cfg ? cfg.scrollTop : ((root.parentElement && root.parentElement.scrollTop) || window.scrollY || 0);
    root.innerHTML = '';
    root.appendChild(renderShell());
    const cfg2 = root.querySelector('.wsc-pane-config');
    if (cfg2) cfg2.scrollTop = y;
    else if (root.parentElement) root.parentElement.scrollTop = y;
  }

  async function renderWSCodegen(view) {
    loadForm();
    const pid = hashProjectId();
    if (pid) {
      state.form.source = 'project';
      state.form.wsdl_project_id = pid;
    }
    root = el('div', { class: 'wsc-page' });
    view.appendChild(root);
    rerender();
    try {
      const [eng, projects] = await Promise.all([
        getJSON('/api/wscodegen/engines'),
        getJSON('/api/wsdl/projects').catch(function () { return { projects: [] }; })
      ]);
      state.engines = (eng && eng.engines) || [];
      state.projects = (projects && projects.projects) || [];
      rerender();
    } catch (e) {
      toast(e.message || '加载引擎列表失败', 'err');
    }
    detectJdk(state.form.jdk_home, true).catch(function () { /* ignore */ });
  }

  Kairo.pages.wscodegen = renderWSCodegen;
  Kairo.state.routes.wscodegen = renderWSCodegen;
  Kairo.state.routeNames.wscodegen = 'WS 代码生成';
})();
