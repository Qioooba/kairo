/* ===== web/pages/formatter.js =====
 * 报文格式化：JSON / XML / YAML / URL-form
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast } = OTB.core;
  const { api } = OTB.api;

  function renderFormatter(view) {
    const inTa = el('textarea', { id: 'fmt-in', placeholder: '在此粘贴报文（JSON / XML / YAML / form-encoded）…' });
    inTa.style.minHeight = '260px';
    const outTa = el('textarea', { id: 'fmt-out', placeholder: '结果会出现在这里', readonly: true });
    outTa.style.minHeight = '260px';

    // 行内小组标签样式
    function makeGroupTag(text) {
      const t = el('span', { text: text });
      t.style.display = 'inline-block';
      t.style.minWidth = '70px';
      t.style.marginRight = '8px';
      t.style.padding = '4px 8px';
      t.style.borderRadius = '4px';
      t.style.background = 'var(--tag-bg, rgba(127,127,127,0.15))';
      t.style.fontWeight = '600';
      t.style.fontSize = '12px';
      t.style.color = 'var(--muted, #888)';
      return t;
    }
    function makeSep() {
      const s = el('span', { text: '|' });
      s.style.color = 'var(--muted, #888)';
      s.style.margin = '0 6px';
      return s;
    }

    // ---- JSON 组 ----
    const btnFmtJSON = el('button', { class: 'btn btn-primary', text: '格式化 JSON', onclick: () => doJSON('format') });
    const btnMinJSON = el('button', { class: 'btn', text: '压缩 JSON', onclick: () => doJSON('minify') });
    const btnValJSON = el('button', { class: 'btn', text: '校验 JSON', onclick: () => doJSON('validate') });

    // ---- XML 组 ----
    const btnFmtXML = el('button', { class: 'btn btn-primary', text: '格式化 XML', onclick: () => doXML('format') });
    const btnMinXML = el('button', { class: 'btn', text: '压缩 XML', onclick: () => doXML('minify') });

    // ---- YAML 组 ----
    const btnFmtYAML = el('button', { class: 'btn btn-primary', text: '格式化 YAML', onclick: () => doYAML('format') });
    const btnMinYAML = el('button', { class: 'btn', text: '压缩 YAML', onclick: () => doYAML('minify') });
    const btnValYAML = el('button', { class: 'btn', text: '校验 YAML', onclick: () => doYAML('validate') });
    const btnYAMLToJSON = el('button', { class: 'btn', text: 'YAML → JSON', onclick: () => doYAML('to_json') });
    const btnJSONToYAML = el('button', { class: 'btn', text: 'JSON → YAML', onclick: () => doYAML('from_json') });

    // ---- URL-form 组 ----
    const btnFormEnc = el('button', { class: 'btn btn-primary', text: 'encode（map → a=1&b=2）', onclick: () => doURLForm('encode') });
    const btnFormDec = el('button', { class: 'btn', text: 'decode（a=1&b=2 → map）', onclick: () => doURLForm('decode') });

    // ---- 通用 ----
    const btnClear = el('button', { class: 'btn', text: '清空', onclick: () => { inTa.value = ''; outTa.value = ''; } });
    const btnCopy = el('button', { class: 'btn', text: '复制结果', onclick: () => copyOut() });

    async function doJSON(mode) {
      try {
        const r = await api('POST', '/api/format/json', { input: inTa.value, mode, indent: '  ' });
        if (mode === 'validate') toast('JSON 合法', 'ok');
        else outTa.value = r.output || '';
      } catch (e) { toast('JSON 处理失败：' + e.message, 'err'); }
    }
    async function doXML(mode) {
      try {
        const r = await api('POST', '/api/format/xml', { input: inTa.value, mode, indent: '  ' });
        outTa.value = r.output || '';
      } catch (e) { toast('XML 处理失败：' + e.message, 'err'); }
    }
    async function doYAML(mode) {
      try {
        const r = await api('POST', '/api/format/yaml', { input: inTa.value, mode, indent: '2' });
        if (mode === 'validate') toast('YAML 合法', 'ok');
        else outTa.value = r.output || '';
      } catch (e) { toast('YAML 处理失败：' + e.message, 'err'); }
    }
    async function doURLForm(mode) {
      try {
        const r = await api('POST', '/api/format/url-form', { input: inTa.value, mode });
        outTa.value = r.output || '';
        if (mode === 'decode') toast(`共 ${r.kv ? Object.keys(r.kv).length : 0} 个 key`, 'ok');
      } catch (e) { toast('form 处理失败：' + e.message, 'err'); }
    }
    function copyOut() {
      if (!outTa.value) { toast('结果为空', 'warn'); return; }
      outTa.select();
      try { document.execCommand('copy'); toast('已复制', 'ok'); }
      catch (e) { toast('复制失败', 'err'); }
    }

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '报文格式化' }),
      el('div', { class: 'card-desc', text: '纯本地处理，不上传任何内容。适合接口报文、日志报文、URL 参数。' }),
      el('div', { class: 'pane' }, [
        el('div', null, [el('label', { text: '输入' }), inTa]),
        el('div', null, [el('label', { text: '输出' }), outTa])
      ]),

      // JSON 组
      el('div', { class: 'btn-row mt-3' }, [
        makeGroupTag('JSON'),
        btnFmtJSON, btnMinJSON, btnValJSON,
      ]),

      // XML 组
      el('div', { class: 'btn-row mt-2' }, [
        makeGroupTag('XML'),
        btnFmtXML, btnMinXML,
      ]),

      // YAML 组
      el('div', { class: 'btn-row mt-2' }, [
        makeGroupTag('YAML'),
        btnFmtYAML, btnMinYAML, btnValYAML,
        makeSep(),
        btnYAMLToJSON, btnJSONToYAML,
      ]),

      // URL-form 组
      el('div', { class: 'btn-row mt-2' }, [
        makeGroupTag('URL-form'),
        btnFormEnc, btnFormDec,
      ]),

      // 通用
      el('div', { class: 'btn-row mt-3' }, [
        btnClear, btnCopy,
      ])
    ]));
  }

  OTB.pages.formatter = renderFormatter;
  OTB.state.routes.formatter = renderFormatter;
  OTB.state.routeNames.formatter = '报文格式化';
})();