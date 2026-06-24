/* ===== web/pages/history.js =====
 * 操作历史页 — 显示 audit.log 的最近记录
 *
 * 过滤：操作类型 / 结果 / 系统 / 服务器 + 自动刷新
 * 导出：CSV / JSON（带自定义过滤弹窗）
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast } = OTB.core;
  const { api, triggerDownload } = OTB.api;

  const OP_TYPES = ['', 'ssh.test', 'logs.list', 'logs.download', 'logs.search', 'logs.context', 'logs.tail', 'admin.servers.put'];

  function renderHistory(view) {
    const opSel = el('select', { id: 'hist-op' });
    OP_TYPES.forEach(v => {
      const o = el('option', { value: v, text: v || '全部操作' });
      if (v === '') o.selected = true;
      opSel.appendChild(o);
    });
    const resultSel = el('select', { id: 'hist-result' });
    ['', 'ok', 'fail'].forEach(v => {
      const o = el('option', { value: v, text: v || '全部状态' });
      if (v === '') o.selected = true;
      resultSel.appendChild(o);
    });
    const limitInp = el('input', { type: 'number', value: '100', min: '1', max: '5000', style: 'width: 100px' });
    const sysInp = el('input', { type: 'text', placeholder: '系统（包含匹配）' });
    const srvInp = el('input', { type: 'text', placeholder: '服务器（包含匹配）' });
    const btnRefresh = el('button', { class: 'btn btn-primary', text: '刷新', onclick: loadHistory });
    const btnExportCSV = el('button', { class: 'btn', text: '导出 CSV', onclick: () => openExportDialog('csv') });
    const btnExportJSON = el('button', { class: 'btn', text: '导出 JSON', onclick: () => openExportDialog('json') });
    const btnAuto = el('button', { class: 'btn', text: '自动刷新: 关', onclick: toggleAuto });
    const tableWrap = el('div', { class: 'card mt-3', style: 'padding: 0' });
    let autoTimer = null;

    async function loadHistory() {
      const q = new URLSearchParams();
      q.set('limit', String(Number(limitInp.value) || 100));
      if (opSel.value) q.set('op', opSel.value);
      if (resultSel.value) q.set('result', resultSel.value);
      if (sysInp.value) q.set('system', sysInp.value);
      if (srvInp.value) q.set('server', srvInp.value);
      try {
        const r = await api('GET', '/api/audit/recent?' + q.toString());
        renderTable(r.records || []);
      } catch (e) { toast('加载失败：' + e.message, 'err'); }
    }

    function openExportDialog(defaultType) {
      const overlay = el('div', { class: 'preview-overlay' });
      const box = el('div', { class: 'preview-box', style: 'width: min(520px, 92vw); max-height: 86vh;' });
      const head = el('div', { class: 'preview-head' });
      head.appendChild(el('div', { class: 'preview-title' }, el('strong', { text: '导出操作记录' })));
      const closeBtn = el('button', {
        class: 'btn btn-sm btn-ghost',
        text: '×',
        style: 'font-size: 18px; line-height: 1; padding: 2px 8px;',
        title: '关闭',
        onclick: closeDialog
      });
      head.appendChild(closeBtn);
      box.appendChild(head);

      const body = el('div', { style: 'padding: 16px 18px; overflow-y: auto; flex: 1;' });

      const timeRangeLabel = el('label', { text: '时间范围', style: 'font-weight: 600; font-size: 13px; color: var(--text); margin-bottom: 8px; display: block;' });
      body.appendChild(timeRangeLabel);
      const timeRow = el('div', { class: 'grid-2', style: 'gap: 10px; margin-bottom: 14px;' });
      const startInp = el('input', { type: 'datetime-local', id: 'export-start' });
      const endInp = el('input', { type: 'datetime-local', id: 'export-end' });
      timeRow.appendChild(
        el('div', null, [el('label', { text: '起始时间', style: 'font-size: 12px; color: var(--text-dim); margin-bottom: 4px;' }), startInp])
      );
      timeRow.appendChild(
        el('div', null, [el('label', { text: '截止时间', style: 'font-size: 12px; color: var(--text-dim); margin-bottom: 4px;' }), endInp])
      );
      body.appendChild(timeRow);

      const opLabel = el('label', { text: '操作类型', style: 'font-weight: 600; font-size: 13px; color: var(--text); margin-bottom: 8px; display: block;' });
      body.appendChild(opLabel);
      const opCheckWrap = el('div', {
        style: 'border: 1px solid var(--line); border-radius: 8px; background: var(--bg-2); padding: 8px 10px; max-height: 180px; overflow-y: auto; margin-bottom: 14px;'
      });
      const opChecks = [];
      const selectAllChk = el('input', { type: 'checkbox', id: 'export-op-all' });
      const opList = OP_TYPES.filter(v => v !== '');
      function updateSelectAllState() {
        const checkedCount = opChecks.filter(c => c.checked).length;
        selectAllChk.checked = checkedCount === opList.length;
        selectAllChk.indeterminate = checkedCount > 0 && checkedCount < opList.length;
      }
      selectAllChk.addEventListener('change', () => {
        const checked = selectAllChk.checked;
        opChecks.forEach(c => { c.checked = checked; });
      });
      const selectAllLabel = el('label', { class: 'inline', style: 'padding: 3px 0; font-weight: 600; border-bottom: 1px dashed var(--line); padding-bottom: 6px; margin-bottom: 4px; display: flex;' });
      selectAllLabel.appendChild(selectAllChk);
      selectAllLabel.appendChild(document.createTextNode('全选'));
      opCheckWrap.appendChild(selectAllLabel);
      opList.forEach(v => {
        const chk = el('input', { type: 'checkbox', value: v, id: 'export-op-' + v.replace(/\./g, '-') });
        const lbl = el('label', { class: 'inline', style: 'padding: 3px 0; display: flex;' });
        lbl.appendChild(chk);
        lbl.appendChild(document.createTextNode(v));
        chk.addEventListener('change', updateSelectAllState);
        opCheckWrap.appendChild(lbl);
        opChecks.push(chk);
        if (opSel.value === v) chk.checked = true;
      });
      if (!opSel.value) {
        opChecks.forEach(c => { c.checked = true; });
        selectAllChk.checked = true;
      } else {
        updateSelectAllState();
      }
      body.appendChild(opCheckWrap);

      const kwLabel = el('label', { text: '关键词', style: 'font-weight: 600; font-size: 13px; color: var(--text); margin-bottom: 8px; display: block;' });
      body.appendChild(kwLabel);
      const kwInp = el('input', { type: 'text', placeholder: '系统 / 服务器 / 详情关键词（包含匹配）', id: 'export-kw' });
      const kwParts = [];
      if (sysInp.value) kwParts.push(sysInp.value);
      if (srvInp.value) kwParts.push(srvInp.value);
      kwInp.value = kwParts.join(' ');
      body.appendChild(kwInp);

      const resultLabel = el('label', { text: '结果状态', style: 'font-weight: 600; font-size: 13px; color: var(--text); margin: 14px 0 8px; display: block;' });
      body.appendChild(resultLabel);
      const resultRow = el('div', { class: 'row', style: 'gap: 16px; margin-bottom: 4px;' });
      const resultAllChk = el('input', { type: 'checkbox', id: 'export-result-all', checked: !resultSel.value });
      const resultOkChk = el('input', { type: 'checkbox', value: 'ok', id: 'export-result-ok', checked: resultSel.value === 'ok' || !resultSel.value });
      const resultFailChk = el('input', { type: 'checkbox', value: 'fail', id: 'export-result-fail', checked: resultSel.value === 'fail' || !resultSel.value });
      function syncResultAll() {
        const allChecked = resultOkChk.checked && resultFailChk.checked;
        resultAllChk.checked = allChecked;
        resultAllChk.indeterminate = (resultOkChk.checked || resultFailChk.checked) && !allChecked;
      }
      resultAllChk.addEventListener('change', () => {
        resultOkChk.checked = resultAllChk.checked;
        resultFailChk.checked = resultAllChk.checked;
      });
      resultOkChk.addEventListener('change', syncResultAll);
      resultFailChk.addEventListener('change', syncResultAll);
      const lblAll = el('label', { class: 'inline' });
      lblAll.appendChild(resultAllChk); lblAll.appendChild(document.createTextNode('全部'));
      const lblOk = el('label', { class: 'inline' });
      lblOk.appendChild(resultOkChk); lblOk.appendChild(document.createTextNode('成功 (ok)'));
      const lblFail = el('label', { class: 'inline' });
      lblFail.appendChild(resultFailChk); lblFail.appendChild(document.createTextNode('失败 (fail)'));
      resultRow.appendChild(lblAll);
      resultRow.appendChild(lblOk);
      resultRow.appendChild(lblFail);
      body.appendChild(resultRow);

      box.appendChild(body);

      const foot = el('div', {
        style: 'padding: 12px 18px; border-top: 1px solid var(--line); background: var(--bg-2); display: flex; gap: 8px; justify-content: flex-end;'
      });
      const btnCancel = el('button', { class: 'btn', text: '取消', onclick: closeDialog });
      const btnDoCSV = el('button', { class: 'btn btn-primary', text: '导出 CSV', onclick: () => doExport('csv') });
      const btnDoJSON = el('button', { class: 'btn', text: '导出 JSON', onclick: () => doExport('json') });
      foot.appendChild(btnCancel);
      foot.appendChild(btnDoCSV);
      foot.appendChild(btnDoJSON);
      box.appendChild(foot);

      overlay.appendChild(box);
      overlay.addEventListener('click', (e) => {
        if (e.target === overlay) closeDialog();
      });
      document.addEventListener('keydown', onKey);
      document.body.appendChild(overlay);

      function onKey(e) {
        if (e.key === 'Escape') closeDialog();
      }
      function closeDialog() {
        document.removeEventListener('keydown', onKey);
        if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
      }

      function buildExportQueryString() {
        const q = new URLSearchParams();
        q.set('limit', '100000');
        if (startInp.value) q.set('start', startInp.value);
        if (endInp.value) q.set('end', endInp.value);
        const selectedOps = opChecks.filter(c => c.checked).map(c => c.value);
        selectedOps.forEach(op => q.append('op', op));
        if (kwInp.value.trim()) {
          const kw = kwInp.value.trim();
          q.set('q', kw);
          q.set('system', kw);
          q.set('server', kw);
        }
        const selectedResults = [];
        if (resultOkChk.checked) selectedResults.push('ok');
        if (resultFailChk.checked) selectedResults.push('fail');
        if (selectedResults.length === 1) q.set('result', selectedResults[0]);
        return q.toString();
      }

      function doExport(type) {
        const selectedOps = opChecks.filter(c => c.checked);
        if (selectedOps.length === 0) {
          toast('请至少选择一种操作类型', 'warn');
          return;
        }
        const selectedResults = [];
        if (resultOkChk.checked) selectedResults.push('ok');
        if (resultFailChk.checked) selectedResults.push('fail');
        if (selectedResults.length === 0) {
          toast('请至少选择一种结果状态', 'warn');
          return;
        }
        const qs = buildExportQueryString();
        const url = '/api/audit/export.' + type + '?' + qs;
        const filename = 'ops-toolbox-audit.' + type;
        closeDialog();
        triggerDownload(url, filename)
          .then(() => toast('已导出 ' + type.toUpperCase(), 'ok'))
          .catch(() => { /* triggerDownload 已经 toast */ });
      }

      setTimeout(() => {
        if (defaultType === 'json') btnDoJSON.focus();
        else btnDoCSV.focus();
      }, 50);
    }

    function renderTable(records) {
      tableWrap.innerHTML = '';
      tableWrap.appendChild(el('h3', { class: 'p-3', text: '共 ' + records.length + ' 条（按时间倒序）' }));
      if (!records.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim p-3', text: '没有匹配记录。' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { text: '时间' }),
        el('th', { text: '操作' }),
        el('th', { text: '系统/服务器' }),
        el('th', { text: '详情' }),
        el('th', { text: '结果' })
      ]));
      tbl.appendChild(thead);
      const tbody = el('tbody');
      records.forEach(r => {
        const detail = [];
        if (r.dir) detail.push('dir=' + r.dir);
        if (r.file) detail.push('file=' + r.file);
        if (r.query) detail.push('query=' + r.query);
        if (r.id) detail.push('id=' + r.id);
        if (r.lines) detail.push('lines=' + r.lines);
        if (r.hits) detail.push('hits=' + r.hits);
        if (r.bytes) detail.push('bytes=' + r.bytes);
        if (r.stage) detail.push('stage=' + r.stage);
        const errTxt = r.err || '';
        const detailTxt = detail.join(' · ') + (errTxt ? '\n⟦err⟧ ' + errTxt : '');
        const resultClass = r.result === 'ok' ? 'tag ready' : (r.result === 'fail' ? 'tag placeholder' : 'muted');
        tbody.appendChild(el('tr', null, [
          el('td', { class: 'muted mono', text: r.ts || '-' }),
          el('td', null, r.op || '-'),
          el('td', { class: 'mono', text: (r.system || '-') + (r.server ? ' · ' + r.server : '') }),
          el('td', { class: 'muted small', text: detailTxt }),
          el('td', null, el('span', { class: resultClass, text: r.result || '-' }))
        ]));
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
    }

    function toggleAuto() {
      if (autoTimer) {
        clearInterval(autoTimer);
        autoTimer = null;
        btnAuto.textContent = '自动刷新: 关';
      } else {
        loadHistory();
        autoTimer = setInterval(loadHistory, 3000);
        btnAuto.textContent = '自动刷新: 开 (3s)';
      }
    }

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '操作历史' }),
      el('div', { class: 'card-desc', text: '读 audit.log，过滤最近 N 条；不会写任何东西，不上传。' }),
      el('div', { class: 'grid-3' }, [
        el('div', null, [el('label', { text: '操作类型' }), opSel]),
        el('div', null, [el('label', { text: '结果' }), resultSel]),
        el('div', null, [el('label', { text: '返回条数' }), limitInp])
      ]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: '系统（包含匹配）' }), sysInp]),
        el('div', null, [el('label', { text: '服务器（包含匹配）' }), srvInp])
      ]),
      el('div', { class: 'btn-row mt-3' }, [btnRefresh, btnExportCSV, btnExportJSON, btnAuto])
    ]));
    view.appendChild(tableWrap);

    loadHistory();
  }

  OTB.pages.history = renderHistory;
  OTB.state.routes.history = renderHistory;
  OTB.state.routeNames.history = '操作历史';
  OTB.state.routeSubs.history = '审计日志（按操作类型 / 状态 / 关键字过滤；最近 N 条）';
})();
