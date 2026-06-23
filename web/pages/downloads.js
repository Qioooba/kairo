/* ===== web/pages/downloads.js =====
 * 下载历史页 — 列出 downloads/ 目录的所有已下载文件
 *
 * 项 24 P3：每行加 "📂 打开所在目录" 按钮（调 /api/downloads/{id}/open-dir）
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast } = OTB.core;
  const { api } = OTB.api;

  function renderDownloads(view) {
    const summaryEl = el('div', { class: 'text-dim', text: '加载中…' });
    const sysSel = el('select', null);
    sysSel.appendChild(el('option', { value: '', text: '全部系统' }));
    sysSel.appendChild(el('option', { value: '信贷生产（模拟）', text: '信贷生产（模拟）' }));
    const srvSel = el('select', null);
    srvSel.appendChild(el('option', { value: '', text: '全部服务器' }));
    const tableWrap = el('div', { class: 'mt-3' });
    const btnRefresh = el('button', { class: 'btn', text: '刷新', onclick: load });
    const btnClearAll = el('button', { class: 'btn btn-danger', text: '清空全部', onclick: doClearAll });

    view.appendChild(el('h3', { text: '下载历史' }));
    view.appendChild(el('div', { class: 'card-desc', text: 'downloads/ 目录里所有已下载的日志。点文件可重新下载，点删除可移除。' }));
    view.appendChild(el('div', { class: 'grid-2 mt-2' }, [
      el('div', null, [el('label', { text: '业务系统' }), sysSel]),
      el('div', null, [el('label', { text: '服务器' }), srvSel])
    ]));
    view.appendChild(el('div', { class: 'btn-row mt-2' }, [btnRefresh, btnClearAll]));
    view.appendChild(summaryEl);
    view.appendChild(tableWrap);

    sysSel.addEventListener('change', load);
    srvSel.addEventListener('change', load);

    async function load() {
      summaryEl.textContent = '加载中…';
      tableWrap.innerHTML = '';
      try {
        const qs = new URLSearchParams();
        if (sysSel.value) qs.set('system', sysSel.value);
        if (srvSel.value) qs.set('server', srvSel.value);
        const r = await api('GET', '/api/downloads/list' + (qs.toString() ? '?' + qs : ''));
        renderRows(r.files || []);
        // 填充 server 下拉
        const srvSet = new Set((r.files || []).map(f => f.server).filter(Boolean));
        const curSrv = srvSel.value;
        srvSel.innerHTML = '';
        srvSel.appendChild(el('option', { value: '', text: '全部服务器' }));
        Array.from(srvSet).sort().forEach(s => srvSel.appendChild(el('option', { value: s, text: s })));
        srvSel.value = curSrv;
        summaryEl.textContent = r.count + ' 个文件 · 占用 ' + r.total_human + ' · 目录 ' + r.folder;
        summaryEl.className = 'text-dim mt-2';
      } catch (e) {
        summaryEl.textContent = '加载失败：' + e.message;
        summaryEl.className = 'text-err mt-2';
      }
    }

    function renderRows(files) {
      tableWrap.innerHTML = '';
      if (!files.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无下载文件。' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      tbl.appendChild(el('thead', null, el('tr', null, [
        el('th', { text: '文件' }),
        el('th', { text: '大小' }),
        el('th', { text: '下载时间' }),
        el('th', { text: '来源' }),
        el('th', { text: '操作' })
      ])));
      const tbody = el('tbody');
      files.forEach(f => {
        const fromServer = f.server || '（未记录）';
        const fromDir = f.dir || '（未记录）';
        const fromFile = f.kind === 'zip'
          ? '📦 ' + (f.files && f.files.length ? f.files.length + ' 个文件' : 'zip')
          : (f.file || '?');
        const dl = el('a', { href: '/downloads/' + encodeURIComponent(f.name), text: '⤓ 下载' });
        const del = el('button', { class: 'btn btn-sm btn-danger', text: '删除',
          onclick: () => doDelete(f, load) });
        const openDir = el('button', { class: 'btn btn-sm', text: '📂 打开所在目录',
          onclick: () => doOpenDir(f) });
        tbody.appendChild(el('tr', null, [
          buildNameCell(f),
          el('td', { class: 'num', text: f.size_human || '-' }),
          el('td', { class: 'muted', text: f.downloaded_at || f.mod_time || '-' }),
          buildFromCell(fromServer, fromDir, fromFile),
          el('td', { class: 'actions', style: 'display:flex; gap:6px;' }, [dl, openDir, del])
        ]));
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
    }

    // buildNameCell 构造"文件名"单元格的 DOM。
    // 默认点文件名 → 新 tab 预览（浏览器能直接显示文本/图片/日志；zip 会触发下载）。
    // 对于 .log / .txt / .json / .xml / .csv 等纯文本，浏览器直接渲染；对于二进制或 zip，右键另存。
    function buildNameCell(f) {
      const td = el('td');
      const href = '/downloads/' + encodeURIComponent(f.name);
      // 用 <a target="_blank">，浏览器会在新 tab 打开；纯文本自动渲染，二进制触发下载。
      // rel="noopener noreferrer" 防 window.opener 漏洞。
      const link = el('a', { href: href, target: '_blank', rel: 'noopener noreferrer', title: '点开新窗口预览（纯文本浏览器直接显示；zip/二进制会触发下载）' },
        el('code', { text: f.name || '' })
      );
      td.appendChild(link);
      if (f.kind === 'zip') {
        td.appendChild(document.createTextNode(' '));
        td.appendChild(el('span', { class: 'tag', text: 'zip' }));
      }
      return td;
    }

    // buildFromCell 构造"来源"单元格的 DOM：服务器（粗体）/ 远端目录 / 原始文件名。
    function buildFromCell(fromServer, fromDir, fromFile) {
      const td = el('td');
      td.appendChild(el('div', { text: fromServer }));
      td.appendChild(el('div', { class: 'text-dim', text: fromDir }));
      td.appendChild(el('div', { class: 'text-dim', text: fromFile }));
      return td;
    }

    async function doDelete(f, cb) {
      if (!confirm('删除 ' + f.name + '？')) return;
      try {
        await api('DELETE', '/api/downloads/' + encodeURIComponent(f.name));
        toast('已删除', 'ok');
        if (cb) cb();
      } catch (e) {
        toast('删除失败：' + e.message, 'err');
      }
    }

    async function doOpenDir(f) {
      // 调 /api/downloads/{name}/open-dir 让后端在系统文件管理器里打开 downloads/ 目录，
      // 失败给红条 toast 提示。
      try {
        await api('POST', '/api/downloads/' + encodeURIComponent(f.name) + '/open-dir');
        toast('已请求在文件管理器中打开', 'ok');
      } catch (e) {
        toast('打开目录失败：' + e.message, 'err');
      }
    }

    async function doClearAll() {
      if (!confirm('清空 downloads/ 里所有文件？此操作不可恢复。')) return;
      try {
        const r = await api('POST', '/api/downloads/all');
        toast('已清空 ' + r.deleted + ' 个文件', 'ok');
        load();
      } catch (e) {
        toast('清空失败：' + e.message, 'err');
      }
    }

    // 首次进入自动加载
    load();
  }

  OTB.pages.downloads = renderDownloads;
  OTB.state.routes.downloads = renderDownloads;
  OTB.state.routeNames.downloads = '下载历史';
  OTB.state.routeSubs.downloads = 'downloads/ 目录里所有已下载的日志（zip / 单文件）+ 来源、删除';
})();