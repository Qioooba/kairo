/* ===== web/pages/history.js =====
 * 操作历史页 — 显示 audit.log 的最近记录
 *
 * 过滤：操作类型 / 结果 / 系统 / 服务器 + 自动刷新
 * 导出：CSV / JSON
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast } = OTB.core;
  const { api, triggerDownload } = OTB.api;

  function renderHistory(view) {
    const opSel = el('select', { id: 'hist-op' },
      ['', 'ssh.test', 'logs.list', 'logs.download', 'logs.search', 'logs.context', 'logs.tail', 'admin.servers.put']
        .map(v => el('option', { value: v, text: v || '全部操作' }, v === '' ? 'selected' : null)));
    const resultSel = el('select', { id: 'hist-result' },
      ['', 'ok', 'fail'].map(v => el('option', { value: v, text: v || '全部状态' }, v === '' ? 'selected' : null)));
    const limitInp = el('input', { type: 'number', value: '100', min: '1', max: '5000', style: 'width: 100px' });
    const sysInp = el('input', { type: 'text', placeholder: '系统（包含匹配）' });
    const srvInp = el('input', { type: 'text', placeholder: '服务器（包含匹配）' });
    const btnRefresh = el('button', { class: 'btn btn-primary', text: '刷新', onclick: loadHistory });
    const btnExportCSV = el('button', { class: 'btn', text: '导出 CSV', onclick: exportCSV });
    const btnExportJSON = el('button', { class: 'btn', text: '导出 JSON', onclick: exportJSON });
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

    // 构造导出 URL（同样的过滤参数）
    function buildExportQs() {
      const q = new URLSearchParams();
      q.set('limit', '5000');
      if (opSel.value) q.set('op', opSel.value);
      if (resultSel.value) q.set('result', resultSel.value);
      if (sysInp.value) q.set('system', sysInp.value);
      if (srvInp.value) q.set('server', srvInp.value);
      return q.toString();
    }

    function exportCSV() {
      const url = '/api/audit/export.csv?' + buildExportQs();
      triggerDownload(url, 'ops-toolbox-audit.csv')
        .then(() => toast('已导出 CSV', 'ok'))
        .catch(() => { /* triggerDownload 已经 toast */ });
    }

    function exportJSON() {
      const url = '/api/audit/export.json?' + buildExportQs();
      triggerDownload(url, 'ops-toolbox-audit.json')
        .then(() => toast('已导出 JSON', 'ok'))
        .catch(() => { /* triggerDownload 已经 toast */ });
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

    // 首次进入自动加载
    loadHistory();
  }

  OTB.pages.history = renderHistory;
  OTB.state.routes.history = renderHistory;
  OTB.state.routeNames.history = '操作历史';
  OTB.state.routeSubs.history = '审计日志（按操作类型 / 状态 / 关键字过滤；最近 N 条）';
})();