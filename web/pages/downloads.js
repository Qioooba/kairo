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

  const ICONS = {
    smFolder:  'M2 5a2 2 0 012-2h5l2 2h9a2 2 0 012 2v11a2 2 0 01-2 2H4a2 2 0 01-2-2V5z',
    smPackage: 'M3 7l9-4 9 4v10l-9 4-9-4zM3 7l9 4 9-4M12 11v10',
    smLink:    'M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71',
    // opener 图标渲染会传 'smFile'（外部打开器在 cfg 里存的 svg name），
    // 缺这个 fallback 就退到 smLink，所有打开器图标变一样，丢失颜色区分。
    smFile:    'M6 2h6l4 4v14a2 2 0 01-2 2H6a2 2 0 01-2-2V4a2 2 0 012-2zm6 0v4h4',
  };
  function svgIcon(name, size) {
    const d = ICONS[name];
    if (!d) return '';
    const s = size || 16;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

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
        // 收集服务器列表用于下拉（业务系统下拉保持顶部拉 cfg 后不变，不自动追加历史里出现的旧系统）
        files.forEach(f => {
          if (f.server) allServers.add(f.server);
          if (f.system) allSystems.add(f.system);
        });
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
        const fromServer = f.server || '';
        const fromDir    = f.dir    || '';
        const fromFile   = f.kind === 'zip'
          ? { html: svgIcon('smPackage', 14) + ' ' + (f.files && f.files.length ? f.files.length + ' 个文件' : 'zip') }
          : { text: f.file || '' };
        const del = el('button', { class: 'btn btn-sm btn-danger', text: '删除',
          onclick: () => doDelete(f, load) });
        const openDir = el('button', { class: 'btn btn-sm', style: 'display:inline-flex; align-items:center; gap:4px;',
          onclick: () => doOpenDir(f),
          unsafeHtml: svgIcon('smFolder', 14) + ' 打开所在目录' });
        // 修复 v0.12 UI-2：opener 图标字段兼容三种取值：
        //   - emoji（如 '📝'/'💡'）→ 直接当图标字符渲染
        //   - SVG icon name（如 'smFile'/'smFolder'）→ 调 svgIcon 渲染对应 SVG
        //   - 空字符串或对象 → 默认 smLink
        // 旧实现只判断 '非 emoji 字符串' 就当自定义文本，结果 'smFile' 被原样显示，
        // 按钮 3 个全显示成 'smFile'，而不是对应的软件名称。
        const openerBtns = (Kairo.state.downloadsOpeners || []).map(op => {
          const rawIcon = (typeof op.icon === 'string') ? op.icon.trim() : '';
          let iconHtml = '';
          if (rawIcon) {
            const isEmoji = /[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u.test(rawIcon);
            if (isEmoji) {
              iconHtml = rawIcon;
            } else if (ICONS[rawIcon]) {
              // 是 SVG icon name → 渲染 SVG
              iconHtml = svgIcon(rawIcon, 14);
            }
          }
          if (!iconHtml) iconHtml = svgIcon('smLink', 14);
          const tip = (op.name || '') + (op.path ? ' — ' + op.path : '');
          return el('button', {
            class: 'btn btn-sm',
            title: tip,
            style: 'display:inline-flex; align-items:center; gap:4px;',
            onclick: () => doOpenWith(op, f),
            unsafeHtml: iconHtml + ' ' + (op.name || '?')
          });
        });
        tbody.appendChild(el('tr', null, [
          buildNameCell(f),
          el('td', { class: 'num', style: 'text-align:right;', text: f.size_human || '-' }),
          // v0.11：「下载时间」列禁止换行（默认 td 107px 宽，时间会换行成 2 行，导致这列高度
          // 在不同行不一致；white-space:nowrap 强制单行后本列固定 1 行高）
          el('td', { class: 'muted', style: 'white-space:nowrap;', text: f.downloaded_at || f.mod_time || '-' }),
          buildFromCell(fromServer, fromDir, fromFile, f),
          // 修复 v0.12 UI-3：「来源」列内容 3 行（服务器/目录/文件）与「操作」列按钮组高度不齐 —
          // 旧实现 tr 默认 vertical-align middle，「操作」td flex 居中容器在由「来源」撑高的 tr 内
          // 视觉上偏上/居中且无内 padding，两个 td 的内容 baseline 错位，看上去像「中间有条横线」。
          // 改：两边都 vertical-align:middle + 内 padding 一致 + 来源 div 间加 line-height，
          // 这样两个 td 的内容在同一个垂直比例内居中，tr 整体看起来齐整。
          el('td', { class: 'actions', style: 'vertical-align:middle; padding:12px 14px; display:flex; gap:6px; flex-wrap:wrap; justify-content:flex-end; align-items:center;' },
            [openDir, ...openerBtns, del])
        ]));
      });
      tbl.appendChild(tbody);
      scrollWrap.appendChild(tbl);
      tableWrap.appendChild(scrollWrap);
    }

    // buildNameCell 构造"文件名"单元格：点文件名 → 新 tab 预览/下载。
    // v0.11：长文件名单行省略号 + max-width，避免一行文件名换行后整行 tr 高度不一致
    // （其他列如「大小」「下载时间」只有 1 行，被 middle 拉到中间，视觉上不齐）。
    function buildNameCell(f) {
      const td = el('td', { style: 'max-width: 280px;' });
      const nameParts = (f.name || '').split('/');
      const encodedPath = nameParts.map(p => encodeURIComponent(p)).join('/');
      const link = el('a', {
        href: '/downloads/' + encodedPath,
        target: '_blank',
        title: (f.name || '') + '（点击预览/下载）',
        style: 'text-decoration:none; color:inherit; display:inline-block; max-width: 240px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; vertical-align:bottom;'
      });
      link.appendChild(el('code', { text: nameParts[nameParts.length - 1] || '' }));
      td.appendChild(link);
      if (f.kind === 'zip') {
        td.appendChild(document.createTextNode(' '));
        td.appendChild(el('span', { class: 'tag', text: 'zip' }));
      }
      return td;
    }

    function buildFromCell(fromServer, fromDir, fromFile, f) {
      const td = el('td');
      // 修复 v0.13 bug-5：「来源」列原来 3 行（server / dir / file 各一 div），看上去错位 +
      // 中间一堆孤零零的「—」占位符。改为单行「server — file」，dir 走 title 悬停展示。
      // 与右侧操作列按钮组现在行高一致，tr 整体看起来齐整，不再有「中间横线」。
      td.style.verticalAlign = 'middle';
      td.style.padding = '12px 14px';
      td.style.whiteSpace = 'nowrap';

      const lineDiv = el('div', { style: 'display:flex; align-items:center; gap:6px; min-width:0;' });
      const fileNameHtml = fromFile.html
        ? fromFile.html
        : (fromFile.text ? escapeHtml(fromFile.text) : '');
      const fileLabel = fileNameHtml || (fromDir ? '<span class="text-dim">' + escapeHtml(fromDir) + '</span>' : '');
      const html = (fromServer ? escapeHtml(fromServer) : '')
        + (fromServer && fileLabel ? ' <span class="text-dim">—</span> ' : '')
        + fileLabel;
      const inner = el('span', { style: 'display:inline-flex; align-items:center; gap:6px; min-width:0; overflow:hidden; text-overflow:ellipsis;' });
      inner.innerHTML = html;
      lineDiv.appendChild(inner);

      // title：把完整的 server / dir / file 三段都摆出来，悬停看到全文（dir 在主显示里折叠掉）
      const parts = [];
      if (fromServer) parts.push('服务器：' + fromServer);
      if (fromDir)    parts.push('目录：' + fromDir);
      if (fromFile.text || fromFile.html) parts.push('文件：' + (fromFile.text || f.file || ''));
      if (parts.length) td.title = parts.join('\n');

      td.appendChild(lineDiv);
      return td;
    }

    function escapeHtml(s) {
      return String(s == null ? '' : s)
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
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
