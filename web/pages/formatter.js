/* ===== web/pages/formatter.js =====
 * 报文格式化：JSON / XML / YAML / URL-form
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast, copyToClipboard } = OTB.core;
  const { api } = OTB.api;

  function groupLabel(text) {
    return el('span', { class: 'text-dim mt-2', style: 'font-size:12px;display:block;' }, [document.createTextNode(text)]);
  }

  function renderFormatter(view) {
    const inTa = el('textarea', { id: 'fmt-in', placeholder: '在此粘贴报文（JSON / XML / YAML / form-encoded）…' });
    inTa.style.minHeight = '320px';
    inTa.style.overflowY = 'auto';
    const outTa = el('textarea', { id: 'fmt-out', placeholder: '结果会出现在这里', readonly: true });
    outTa.style.minHeight = '320px';
    outTa.style.overflowY = 'auto';

    let searchMatches = [];
    let searchCurrentIdx = -1;

    const searchInp = el('input', {
      type: 'text',
      id: 'fmt-search',
      placeholder: '在结果中搜索...',
      style: 'flex:1; min-width:150px;'
    });
    const searchCount = el('span', { class: 'text-dim', style: 'font-size:12px; min-width:40px; text-align:center;', text: '0/0' });
    const btnPrev = el('button', { class: 'btn btn-sm', text: '↑', title: '上一个 (Shift+Enter)', onclick: () => searchPrev() });
    const btnNext = el('button', { class: 'btn btn-sm', text: '↓', title: '下一个 (Enter)', onclick: () => searchNext() });

    const searchBar = el('div', {
      class: 'fmt-search-bar',
      style: 'display:flex; gap:6px; align-items:center; margin-top:6px; padding:6px 8px; background:var(--bg-2); border-radius:6px; border:1px solid var(--line);'
    }, [searchInp, searchCount, btnPrev, btnNext]);
    searchBar.style.display = 'none';

    function updateSearchCount() {
      if (searchMatches.length === 0) {
        searchCount.textContent = '0/0';
        return;
      }
      searchCount.textContent = (searchCurrentIdx + 1) + '/' + searchMatches.length;
    }

    function doSearch(forward) {
      const q = searchInp.value;
      if (!q) {
        searchMatches = [];
        searchCurrentIdx = -1;
        updateSearchCount();
        return;
      }
      const content = outTa.value;
      if (!content) {
        searchMatches = [];
        searchCurrentIdx = -1;
        updateSearchCount();
        return;
      }
      const lowerQ = q.toLowerCase();
      const lowerContent = content.toLowerCase();
      const matches = [];
      let idx = 0;
      while (true) {
        const found = lowerContent.indexOf(lowerQ, idx);
        if (found === -1) break;
        matches.push(found);
        idx = found + 1;
      }
      searchMatches = matches;
      if (matches.length === 0) {
        searchCurrentIdx = -1;
        updateSearchCount();
        toast('未找到匹配', 'warn');
        return;
      }
      if (forward) {
        searchCurrentIdx = (searchCurrentIdx + 1) % matches.length;
      } else {
        searchCurrentIdx = searchCurrentIdx <= 0 ? matches.length - 1 : searchCurrentIdx - 1;
      }
      selectMatch();
      updateSearchCount();
    }

    function selectMatch() {
      if (searchCurrentIdx < 0 || searchCurrentIdx >= searchMatches.length) return;
      const q = searchInp.value;
      const start = searchMatches[searchCurrentIdx];
      const end = start + q.length;
      outTa.focus();
      outTa.setSelectionRange(start, end);
      const textBefore = outTa.value.substring(0, start);
      const lineHeight = 16;
      const linesBefore = textBefore.split('\n').length;
      const scrollPos = (linesBefore - 3) * lineHeight;
      if (outTa.scrollTo) {
        outTa.scrollTo({ top: Math.max(0, scrollPos), behavior: 'smooth' });
      } else {
        outTa.scrollTop = Math.max(0, scrollPos);
      }
    }

    function searchNext() { doSearch(true); }
    function searchPrev() { doSearch(false); }

    searchInp.addEventListener('input', () => {
      searchMatches = [];
      searchCurrentIdx = -1;
      if (searchInp.value && outTa.value) {
        doSearch(true);
      } else {
        updateSearchCount();
      }
    });
    searchInp.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        if (ev.shiftKey) {
          searchPrev();
        } else {
          searchNext();
        }
      } else if (ev.key === 'Escape') {
        searchInp.value = '';
        searchMatches = [];
        searchCurrentIdx = -1;
        updateSearchCount();
      }
    });

    outTa.addEventListener('input', () => {
      if (outTa.value) {
        searchBar.style.display = 'flex';
      } else {
        searchBar.style.display = 'none';
        searchMatches = [];
        searchCurrentIdx = -1;
        updateSearchCount();
      }
    });

    // ---- JSON 组 ----
    const btnFmtJSON = el('button', { class: 'btn', text: '格式化 JSON', onclick: () => doJSON('format') });
    const btnMinJSON = el('button', { class: 'btn', text: '压缩 JSON', onclick: () => doJSON('minify') });
    const btnValJSON = el('button', { class: 'btn', text: '校验 JSON', onclick: () => doJSON('validate') });

    // ---- XML 组 ----
    const btnFmtXML = el('button', { class: 'btn', text: '格式化 XML', onclick: () => doXML('format') });
    const btnMinXML = el('button', { class: 'btn', text: '压缩 XML', onclick: () => doXML('minify') });

    // ---- YAML 组 ----
    const btnFmtYAML = el('button', { class: 'btn', text: '格式化 YAML', onclick: () => doYAML('format') });
    const btnMinYAML = el('button', { class: 'btn', text: '压缩 YAML', onclick: () => doYAML('minify') });
    const btnValYAML = el('button', { class: 'btn', text: '校验 YAML', onclick: () => doYAML('validate') });
    const btnYAMLToJSON = el('button', { class: 'btn', text: 'YAML → JSON', onclick: () => doYAML('to_json') });
    const btnJSONToYAML = el('button', { class: 'btn', text: 'JSON → YAML', onclick: () => doYAML('from_json') });

    // ---- URL-form 组 ----
    const btnFormEnc = el('button', { class: 'btn', text: 'URL 编码', onclick: () => doURLForm('encode') });
    const btnFormDec = el('button', { class: 'btn', text: 'URL 解码', onclick: () => doURLForm('decode') });

    // ---- 通用 ----
    const btnClear = el('button', { class: 'btn', text: '清空', onclick: () => {
      inTa.value = '';
      outTa.value = '';
      searchBar.style.display = 'none';
      searchMatches = [];
      searchCurrentIdx = -1;
      updateSearchCount();
    }});
    const btnCopy = el('button', { class: 'btn', text: '复制结果', onclick: () => copyOut() });

    async function doJSON(mode) {
      try {
        const r = await api('POST', '/api/format/json', { input: inTa.value, mode, indent: '  ' });
        if (mode === 'validate') toast('JSON 合法', 'ok');
        else {
          outTa.value = r.output || '';
          searchBar.style.display = outTa.value ? 'flex' : 'none';
        }
      } catch (e) { toast('JSON 处理失败：' + e.message, 'err'); }
    }
    async function doXML(mode) {
      try {
        const r = await api('POST', '/api/format/xml', { input: inTa.value, mode, indent: '  ' });
        outTa.value = r.output || '';
        searchBar.style.display = outTa.value ? 'flex' : 'none';
      } catch (e) { toast('XML 处理失败：' + e.message, 'err'); }
    }
    async function doYAML(mode) {
      try {
        const r = await api('POST', '/api/format/yaml', { input: inTa.value, mode, indent: '2' });
        if (mode === 'validate') toast('YAML 合法', 'ok');
        else {
          outTa.value = r.output || '';
          searchBar.style.display = outTa.value ? 'flex' : 'none';
        }
      } catch (e) { toast('YAML 处理失败：' + e.message, 'err'); }
    }
    async function doURLForm(mode) {
      try {
        const r = await api('POST', '/api/format/url-form', { input: inTa.value, mode });
        outTa.value = r.output || '';
        searchBar.style.display = outTa.value ? 'flex' : 'none';
        if (mode === 'decode') toast(`共 ${r.kv ? Object.keys(r.kv).length : 0} 个 key`, 'ok');
      } catch (e) { toast('form 处理失败：' + e.message, 'err'); }
    }
    async function copyOut() {
      if (!outTa.value) { toast('结果为空', 'warn'); return; }
      try {
        await copyToClipboard(outTa.value);
        toast('已复制', 'ok');
      } catch (e) { toast('复制失败', 'err'); }
    }

    const outWrap = el('div', null, [
      el('label', { text: '输出' }),
      outTa,
      searchBar
    ]);

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '报文格式化' }),
      el('div', { class: 'card-desc', text: '纯本地处理，不上传任何内容。适合接口报文、日志报文、URL 参数。' }),
      el('div', { class: 'pane' }, [
        el('div', null, [el('label', { text: '输入' }), inTa]),
        outWrap
      ]),

      groupLabel('JSON'),
      el('div', { class: 'btn-row mt-2' }, [
        btnFmtJSON, btnMinJSON, btnValJSON,
      ]),

      groupLabel('XML'),
      el('div', { class: 'btn-row mt-2' }, [
        btnFmtXML, btnMinXML,
      ]),

      groupLabel('YAML'),
      el('div', { class: 'btn-row mt-2' }, [
        btnFmtYAML, btnMinYAML, btnValYAML,
        btnYAMLToJSON, btnJSONToYAML,
      ]),

      groupLabel('URL 编码'),
      el('div', { class: 'btn-row mt-2' }, [
        btnFormEnc, btnFormDec,
      ]),

      groupLabel('其他'),
      el('div', { class: 'btn-row mt-2' }, [
        btnClear, btnCopy,
      ])
    ]));
  }

  OTB.pages.formatter = renderFormatter;
  OTB.state.routes.formatter = renderFormatter;
  OTB.state.routeNames.formatter = '报文格式化';
})();
