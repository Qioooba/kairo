/* ===== web/pages/downloads.js =====
 * 下载历史页 — 列出 downloads/ 目录的所有已下载文件
 *
 * 项 24 P3：每行加 "📂 打开所在目录" 按钮（调 /api/downloads/{id}/open-dir）
 * v0.8：每行再追加外部打开器按钮（来自 cfg.external_openers），
 *       调 /api/local/open-with 用用户配置的本地软件打开文件
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, confirmDialog } = Kairo.core;
  const { api } = Kairo.api;

  // v0.8：外部打开器缓存（module 级，跨 re-render 存活）。
  Kairo.state.downloadsOpeners = Kairo.state.downloadsOpeners || [];
  let openersLoaded = false;

  function renderDownloads(view) {
    const summaryEl = el('div', { class: 'text-dim', text: '加载中…' });
    const sysSel = el('select', null);
    sysSel.appendChild(el('option', { value: '', text: '全部系统' }));
    const srvSel = el('select', null);
    srvSel.appendChild(el('option', { value: '', text: '全部服务器' }));
    const allSystems = new Set();
    const allServers = new Set();
    const tableWrap = el('div', { class: 'mt-3' });
    const btnRefresh = el('button', { class: 'btn', text: '刷新', onclick: load });
    // v0.9 UI-1：原来用 .btn-danger（实心红渐变）太鲜艳，改成柔和 outline 红，
    // 跟整体暗色协调，但仍能跟"删除单文件"的 .btn-danger 区分开。
    const btnClearAll = el('button', { class: 'btn btn-danger-soft', text: '清空全部', onclick: doClearAll });

    // v0.8：拉一次外部打开器列表（缓存到 Kairo.state.downloadsOpeners）。
    // openers 异步加载，不阻塞列表 load()。
    if (!openersLoaded) {
      api('GET', '/api/admin/openers').then(r => {
        Kairo.state.downloadsOpeners = Array.isArray(r && r.openers) ? r.openers : [];
        openersLoaded = true;
      }).catch(() => {
        Kairo.state.downloadsOpeners = [];
        openersLoaded = true;
      });
    }
    load(); // 无论 openers 是否加载完都加载下载列表

    view.appendChild(el('div', { class: 'grid-2 mt-2' }, [
      el('div', null, [el('label', { text: '业务系统' }), sysSel]),
      el('div', null, [el('label', { text: '服务器' }), srvSel])
    ]));
    view.appendChild(el('div', { class: 'btn-row mt-2' }, [btnRefresh, btnClearAll]));
    view.appendChild(summaryEl);
    view.appendChild(tableWrap);

    sysSel.addEventListener('change', load);
    srvSel.addEventListener('change', load);

    // 启动时主动拉 /api/config 填系统下拉
    api('GET', '/api/config').then(info => {
      const cur = sysSel.value;
      (info.systems || []).forEach(s => {
        if (s.name) {
          sysSel.appendChild(el('option', { value: s.name, text: s.name + (s.description ? ' · ' + s.description : '') }));
        }
      });
      sysSel.value = cur;
    }).catch(() => { /* ignore */ });

    async function load() {
      summaryEl.textContent = '加载中…';
      tableWrap.innerHTML = '';
      try {
        const qs = new URLSearchParams();
        if (sysSel.value) qs.set('system', sysSel.value);
        if (srvSel.value) qs.set('server', srvSel.value);
        const r = await api('GET', '/api/downloads/list' + (qs.toString() ? '?' + qs : ''));
        const files = r.files || [];
        renderRows(files);
        files.forEach(f => {
          if (f.server) allServers.add(f.server);
          if (f.system) allSystems.add(f.system);
        });
        const curSys = sysSel.value;
        allSystems.forEach(s => {
          if (!Array.from(sysSel.options).find(o => o.value === s)) {
            sysSel.appendChild(el('option', { value: s, text: s + '（已下架）' }));
          }
        });
        sysSel.value = curSys;
        const curSrv = srvSel.value;
        srvSel.innerHTML = '';
        srvSel.appendChild(el('option', { value: '', text: '全部服务器' }));
        Array.from(allServers).sort().forEach(s => srvSel.appendChild(el('option', { value: s, text: s })));
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
      const scrollWrap = el('div', { class: 'table-scroll' });
      const tbl = el('table', { class: 'table' });
      tbl.appendChild(el('thead', null, el('tr', null, [
        el('th', { text: '文件' }),
        el('th', { text: '大小', style: 'text-align:right;' }),
        el('th', { text: '下载时间' }),
        el('th', { text: '来源' }),
        el('th', { text: '操作', style: 'text-align:right;' })
      ])));
      const tbody = el('tbody');
      files.forEach(f => {
        const fromServer = f.server || '—';
        const fromDir = f.dir || '—';
        const fromFile = f.kind === 'zip'
          ? '📦 ' + (f.files && f.files.length ? f.files.length + ' 个文件' : 'zip')
          : (f.file || '?');
        const del = el('button', { class: 'btn btn-sm btn-danger', text: '删除',
          onclick: () => doDelete(f, load) });
        const openDir = el('button', { class: 'btn btn-sm', text: '📂 打开所在目录',
          onclick: () => doOpenDir(f) });
        const openerBtns = (Kairo.state.downloadsOpeners || []).map(op => {
          const label = (op.icon && op.icon.trim()) ? op.icon.trim() : '🔗 ' + (op.name || '?');
          const tip = (op.name || '') + (op.path ? ' — ' + op.path : '');
          return el('button', {
            class: 'btn btn-sm',
            title: tip,
            text: label,
            onclick: () => doOpenWith(op, f)
          });
        });
        tbody.appendChild(el('tr', null, [
          buildNameCell(f),
          el('td', { class: 'num', style: 'text-align:right;', text: f.size_human || '-' }),
          el('td', { class: 'muted', text: f.downloaded_at || f.mod_time || '-' }),
          buildFromCell(fromServer, fromDir, fromFile),
          el('td', { class: 'actions', style: 'display:flex; gap:6px; flex-wrap:wrap; justify-content:flex-end; align-items:center;' },
            [openDir, ...openerBtns, del])
        ]));
      });
      tbl.appendChild(tbody);
      scrollWrap.appendChild(tbl);
      tableWrap.appendChild(scrollWrap);
    }

    // buildNameCell 构造"文件名"单元格：点文件名 → 新 tab 预览/下载。
    function buildNameCell(f) {
      const td = el('td');
      const nameParts = (f.name || '').split('/');
      const encodedPath = nameParts.map(p => encodeURIComponent(p)).join('/');
      const link = el('a', {
        href: '/downloads/' + encodedPath,
        target: '_blank',
        title: '点击预览/下载',
        style: 'text-decoration:none; color:inherit;'
      });
      link.appendChild(el('code', { text: nameParts[nameParts.length - 1] || '' }));
      td.appendChild(link);
      if (f.kind === 'zip') {
        td.appendChild(document.createTextNode(' '));
        td.appendChild(el('span', { class: 'tag', text: 'zip' }));
      }
      return td;
    }

    function buildFromCell(fromServer, fromDir, fromFile) {
      const td = el('td');
      td.appendChild(el('div', { text: fromServer }));
      td.appendChild(el('div', { class: 'text-dim', text: fromDir }));
      td.appendChild(el('div', { class: 'text-dim', text: fromFile }));
      return td;
    }

    async function doOpenWith(opener, f) {
      if (!opener || !opener.name) {
        toast('打开器配置异常', 'err');
        return;
      }
      try {
        await api('POST', '/api/local/open-with', {
          opener: opener.name,
          name: f.name
        });
        toast('已用 ' + opener.name + ' 打开', 'ok');
      } catch (e) {
        toast('打开失败：' + e.message, 'err');
      }
    }

    async function doDelete(f, cb) {
      if (!await confirmDialog('删除 ' + f.name + '？')) return;
      try {
        await api('DELETE', '/api/downloads/' + encodeURIComponent(f.name));
        toast('已删除', 'ok');
        if (cb) cb();
      } catch (e) {
        toast('删除失败：' + e.message, 'err');
      }
    }

    async function doOpenDir(f) {
      try {
        await api('POST', '/api/downloads/open-dir?name=' + encodeURIComponent(f.name));
        toast('已请求在文件管理器中打开', 'ok');
      } catch (e) {
        toast('打开目录失败：' + e.message, 'err');
      }
    }

    async function doClearAll() {
      if (!await confirmDialog('清空 downloads/ 里所有文件？此操作不可恢复。')) return;
      try {
        const r = await api('POST', '/api/downloads/all');
        toast('已清空 ' + r.deleted + ' 个文件', 'ok');
        load();
      } catch (e) {
        toast('清空失败：' + e.message, 'err');
      }
    }

    // openers 已加载过时直接 load；否则等 openers 回来再 load
    if (openersLoaded) load();
  }

  Kairo.pages.downloads = renderDownloads;
  Kairo.state.routes.downloads = renderDownloads;
  Kairo.state.routeNames.downloads = '下载历史';
  Kairo.state.routeSubs.downloads = 'downloads/ 目录里所有已下载的日志（zip / 单文件）+ 来源、删除';
})();
