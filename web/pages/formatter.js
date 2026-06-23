/* ===== web/pages/formatter.js =====
 * 报文格式化：JSON / XML 格式化、压缩、校验
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast } = OTB.core;
  const { api } = OTB.api;

  function renderFormatter(view) {
    const inTa = el('textarea', { id: 'fmt-in', placeholder: '在此粘贴 JSON 或 XML…' });
    const outTa = el('textarea', { id: 'fmt-out', placeholder: '结果会出现在这里', readonly: true });

    const btnFmtJSON = el('button', { class: 'btn btn-primary', text: '格式化 JSON', onclick: () => doJSON('format') });
    const btnMinJSON = el('button', { class: 'btn', text: '压缩 JSON', onclick: () => doJSON('minify') });
    const btnValJSON = el('button', { class: 'btn', text: '校验 JSON', onclick: () => doJSON('validate') });
    const btnFmtXML = el('button', { class: 'btn btn-primary', text: '格式化 XML', onclick: () => doXML('format') });
    const btnMinXML = el('button', { class: 'btn', text: '压缩 XML', onclick: () => doXML('minify') });
    const btnClear = el('button', { class: 'btn', text: '清空', onclick: () => { inTa.value = ''; outTa.value = ''; } });
    const btnCopy = el('button', { class: 'btn', text: '复制结果', onclick: () => copyOut() });

    async function doJSON(mode) {
      try {
        const r = await api('POST', '/api/format/json', { input: inTa.value, mode: mode, indent: '  ' });
        if (mode === 'validate') toast('JSON 合法', 'ok');
        else outTa.value = r.output || '';
      } catch (e) { toast('JSON 处理失败：' + e.message, 'err'); }
    }
    async function doXML(mode) {
      try {
        const r = await api('POST', '/api/format/xml', { input: inTa.value, mode: mode, indent: '  ' });
        outTa.value = r.output || '';
      } catch (e) { toast('XML 处理失败：' + e.message, 'err'); }
    }
    function copyOut() {
      if (!outTa.value) { toast('结果为空', 'warn'); return; }
      outTa.select();
      try { document.execCommand('copy'); toast('已复制', 'ok'); }
      catch (e) { toast('复制失败', 'err'); }
    }

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '报文格式化' }),
      el('div', { class: 'card-desc', text: '纯本地处理，不上传任何内容。适合接口报文、日志报文。' }),
      el('div', { class: 'pane' }, [
        el('div', null, [el('label', { text: '输入' }), inTa]),
        el('div', null, [el('label', { text: '输出' }), outTa])
      ]),
      el('div', { class: 'btn-row mt-3' }, [
        btnFmtJSON, btnMinJSON, btnValJSON,
        btnFmtXML, btnMinXML,
        btnClear, btnCopy
      ])
    ]));
  }

  OTB.pages.formatter = renderFormatter;
  OTB.state.routes.formatter = renderFormatter;
  OTB.state.routeNames.formatter = '报文格式化';
})();