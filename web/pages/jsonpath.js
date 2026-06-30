/* ===== web/pages/jsonpath.js =====
 * JSONPath 提取：左侧粘 JSON，右侧粘 gjson 路径（user.name / items.#.id / ..），
 * 点提取 → 后端跑 gjson，右侧出结果 + 类型标签。
 *
 * 路径语法走 gjson：
 *   - 点号 . 访问子字段：user.name
 *   - # 是数组通配：items.#.id（= items[*].id）
 *   - 单下标访问元素：items.1
 *   - 嵌套数组用 # 递归：friends.#(last=="Murphy")#.first
 *
 * 历史记录走 localStorage（key=kairo:jsonpath:history），最多 20 条。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard } = Kairo.core;
  const { api } = Kairo.api;

  const LS_HISTORY = 'kairo:jsonpath:history';
  const LS_LAST = 'kairo:jsonpath:last';
  const MAX_HIST = 20;

  // ---------- 历史 ----------

  function loadHistory() {
    try {
      const raw = localStorage.getItem(LS_HISTORY);
      if (!raw) return [];
      const arr = JSON.parse(raw);
      return Array.isArray(arr) ? arr : [];
    } catch (e) { return []; }
  }
  function saveHistory(arr) {
    try { localStorage.setItem(LS_HISTORY, JSON.stringify(arr.slice(0, MAX_HIST))); } catch (e) { /* ignore */ }
  }
  function pushHistory(path) {
    if (!path) return;
    const arr = loadHistory().filter(p => p !== path);
    arr.unshift(path);
    saveHistory(arr);
  }

  // ---------- 视图 ----------

  function renderJSONPath(view) {
    const last = (() => { try { return JSON.parse(localStorage.getItem(LS_LAST) || '{}'); } catch (e) { return {}; } })();

    const inTa = el('textarea', {
      id: 'jp-in',
      placeholder: '左侧：粘贴 JSON…',
      spellcheck: 'false',
    });
    inTa.value = last.input || '';
    inTa.style.minHeight = '320px';

    const pathInp = el('input', {
      type: 'text',
      id: 'jp-path',
      placeholder: 'gjson 路径，如 user.name / items.#.id / user.profile.city',
    });
    pathInp.value = last.path || 'user.name';
    pathInp.style.width = '100%';

    const outTa = el('textarea', {
      id: 'jp-out',
      placeholder: '结果会出现在这里…',
      readonly: true,
      spellcheck: 'false',
    });
    outTa.style.minHeight = '320px';

    const cbStrict = el('input', { type: 'checkbox' });
    cbStrict.checked = last.strict !== false; // 默认开
    const strictLabelText = document.createTextNode('  严格模式（JSON 不合法直接报错）');
    const strictStatusText = el('span', { class: 'muted', text: cbStrict.checked ? '严格模式：JSON 必须合法' : '宽松模式：尽量解析' });
    function updateStrictLabel() {
      const isStrict = cbStrict.checked;
      strictStatusText.textContent = isStrict ? '严格模式：JSON 必须合法' : '宽松模式：尽量解析';
      strictLabelText.textContent = isStrict ? '  严格模式（JSON 不合法直接报错）' : '  宽松模式（尽量解析，允许尾部垃圾）';
    }
    cbStrict.addEventListener('change', updateStrictLabel);

    const typeTag = el('span', { class: 'muted', text: '—' });
    const elapsedTag = el('span', { class: 'muted', text: '' });

    const historySel = el('select', { style: 'min-width: 180px' });
    historySel.appendChild(el('option', { value: '', text: '— 历史路径 —' }));
    function refreshHistory() {
      // 简单全删重建（历史最多 20 条，够用）
      while (historySel.children.length > 1) historySel.removeChild(historySel.lastChild);
      loadHistory().forEach(p => {
        historySel.appendChild(el('option', { value: p, text: p }));
      });
    }
    refreshHistory();
    historySel.addEventListener('change', () => {
      if (historySel.value) {
        pathInp.value = historySel.value;
        doExtract();
      }
      historySel.value = '';
    });

    async function doExtract() {
      const input = inTa.value;
      const path = pathInp.value.trim();
      if (!input) { toast('JSON 不能为空', 'warn'); return; }
      const start = performance.now();
      try {
        const r = await api('POST', '/api/format/jsonpath', {
          input, path, strict: cbStrict.checked,
        });
        const elapsed = Math.round(performance.now() - start);
        // r = { ok, found, result, type, is_array }
        outTa.value = r.result || '';
        typeTag.textContent = r.found ? ('类型：' + r.type + (r.is_array ? ' (array)' : '')) : '未找到';
        typeTag.className = r.found ? 'tag tag-ok' : 'tag tag-warn';
        elapsedTag.textContent = '后端 ' + (r.elapsed_ms || 0) + 'ms · 总耗时 ' + elapsed + 'ms';
        if (r.found) {
          pushHistory(path);
          refreshHistory();
          try { localStorage.setItem(LS_LAST, JSON.stringify({ input, path, strict: cbStrict.checked })); } catch (e) { /* ignore */ }
        } else {
          toast('路径未找到：' + path, 'warn');
        }
      } catch (e) {
        outTa.value = '';
        typeTag.textContent = '错误';
        typeTag.className = 'tag tag-err';
        elapsedTag.textContent = '';
        toast('提取失败：' + (e.message || e), 'err');
      }
    }

    const btnExtract = el('button', { class: 'btn btn-primary', text: '提取', onclick: doExtract });
    const btnClear = el('button', { class: 'btn', text: '清空', onclick: () => {
      inTa.value = ''; outTa.value = ''; pathInp.value = '';
      typeTag.textContent = '—'; typeTag.className = 'muted'; elapsedTag.textContent = '';
    }});
    const btnFormat = el('button', { class: 'btn', text: '先格式化 JSON', onclick: async () => {
      try {
        const r = await api('POST', '/api/format/json', { input: inTa.value, mode: 'format', indent: '  ' });
        if (r.output) inTa.value = r.output;
        toast('已格式化', 'ok');
      } catch (e) { toast('格式化失败：' + e.message, 'err'); }
    }});
    const btnCopy = el('button', { class: 'btn', text: '复制结果', onclick: async () => {
      if (!outTa.value) { toast('结果为空', 'warn'); return; }
      try {
        await copyToClipboard(outTa.value);
        toast('已复制', 'ok');
      } catch (e) { toast('复制失败', 'err'); }
    }});

    // 回车提交流程
    pathInp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); doExtract(); }
    });

    view.appendChild(el('div', { class: 'card' }, [
      el('div', { class: 'card-desc', text: 'gjson 路径语法（user.name / items.#.id / a.b.#.c）。后端解析，纯本地不外发。' }),
      el('div', { class: 'pane' }, [
        el('div', null, [
          el('div', { class: 'cmp-pane-header' }, [
            el('label', { text: '输入 JSON' }),
            strictStatusText,
          ]),
          inTa,
        ]),
        el('div', null, [
          el('div', { class: 'cmp-pane-header' }, [
            el('label', { text: '路径 + 提取结果' }),
            typeTag,
            elapsedTag,
          ]),
          el('div', { style: 'display:flex; gap:8px; margin-bottom:8px; align-items:center;' }, [
            el('label', { class: 'cmp-opt', style: 'flex:0 0 auto' }, [pathInp]),
            historySel,
          ]),
          el('label', { class: 'cmp-opt', style: 'display:block; margin-bottom:8px' }, [
            cbStrict,
            strictLabelText,
          ]),
          outTa,
        ]),
      ]),
      el('div', { class: 'btn-row mt-3' }, [
        btnExtract, btnFormat, btnCopy, btnClear,
      ]),
      el('div', { class: 'muted cmp-v2-hint', text: '提示：路径支持 # 数组通配（items.#.id）、下标（items.1）、查询过滤（friends.#(last=="Murphy")#.first）。详见 gjson 文档。' }),
    ]));
  }

  Kairo.pages.jsonpath = renderJSONPath;
  Kairo.state.routes.jsonpath = renderJSONPath;
  Kairo.state.routeNames.jsonpath = 'JSONPath';
  Kairo.state.routeSubs.jsonpath = 'gjson 路径提取';
})();
