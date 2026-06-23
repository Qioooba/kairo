/* ===== web/pages/files.js =====
 * 文件下载（v0.3）：按 SSH 账号权限浏览任意目录并下载。
 *
 * 设计要点：
 *   - 顶部连接区：系统 / 服务器 / 用户名 / 密码（keyring 复用）
 *   - 中部路径区：面包屑 + 返回上级 + 手动输入路径跳转
 *   - 主体：目录列表，目录可点击进入，文件可勾选
 *   - 多选下载：复用现有 /api/files/download + SSE 进度机制
 *   - 顶部：app.enable_free_file_browser 警告（已关闭 / 白名单 / 自由模式）
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, $, toast, setStatus, cssEscape, pctText, formatBytes, formatTime, basenameOf } = OTB.core;
  const { api } = OTB.api;

  function renderFiles(view) {
    const state = {
      cfg: null,
      currentSys: '',
      currentSrv: '',
      currentPath: '/',
      parent: '',
      entries: [],
      selected: new Set(),
      sortKey: 'name',
      sortDesc: false,
      dlId: null,
      dlEvtSrc: null,
      fileStates: {},
    };

    // ---- 连接区 ----
    const sysSel = el('select', { id: 'files-sys' });
    const srvSel = el('select', { id: 'files-srv' });
    srvSel.appendChild(el('option', { value: '', text: '（先选系统）' }));
    srvSel.disabled = true;
    const userInp = el('input', { type: 'text', id: 'files-user', placeholder: 'SSH 用户名（可留空用配置默认）' });
    const passInp = el('input', { type: 'password', id: 'files-pass', placeholder: 'SSH 密码' });
    const rememberChk = el('input', { type: 'checkbox', id: 'files-remember' });
    const rememberLabel = el('label', { class: 'inline' }, [rememberChk, document.createTextNode('记住密码')]);
    const btnConnect = el('button', { class: 'btn btn-primary', text: '连接并浏览' });

    // ---- 文件浏览器启动警告（项 14）----
    const warnBox = el('div', { class: 'card', id: 'files-warn', style: 'display:none' });
    warnBox.appendChild(el('h3', { id: 'files-warn-title', text: '' }));
    const warnBody = el('div');
    warnBox.appendChild(warnBody);

    const connCard = el('div', { class: 'card' });
    connCard.appendChild(el('h3', { text: '1. 选择目标服务器' }));
    connCard.appendChild(el('div', { class: 'card-desc', text: '支持任意路径浏览；下载权限以 SSH 账号实际权限为准（v0.3 自由模式）。' }));
    connCard.appendChild(el('div', { class: 'grid-3' }, [
      el('label', null, [el('span', { class: 'lbl', text: '业务系统' }), sysSel]),
      el('label', null, [el('span', { class: 'lbl', text: '服务器' }), srvSel]),
      el('label', null, [el('span', { class: 'lbl', text: '用户名' }), userInp])
    ]));
    connCard.appendChild(el('div', { class: 'grid-2 mt-2' }, [
      el('label', null, [el('span', { class: 'lbl', text: '密码' }), passInp]),
      el('div', { style: 'display:flex;align-items:flex-end;gap:10px' }, [rememberLabel, btnConnect])
    ]));

    // ---- 路径区 ----
    const crumbsEl = el('div', { class: 'file-crumbs', style: 'font-family: ui-monospace, monospace; font-size: 13px;' });
    const pathInp = el('input', { type: 'text', placeholder: '输入绝对路径后回车跳转（例 /var/log）' });
    const btnParent = el('button', { class: 'btn btn-sm', text: '← 上级' });
    const btnRefresh = el('button', { class: 'btn btn-sm', text: '刷新' });

    const pathCard = el('div', { class: 'card' });
    pathCard.appendChild(el('h3', { text: '2. 浏览目录' }));
    pathCard.appendChild(el('div', { class: 'card-desc' }, [
      document.createTextNode('面包屑可点击跳转；点目录名进入子目录；勾选文件后下载。')
    ]));
    pathCard.appendChild(el('div', { class: 'row gap-2 mb-2' }, [btnParent, btnRefresh, crumbsEl]));
    pathCard.appendChild(pathInp);

    // ---- 文件列表区 ----
    const tableWrap = el('div', { class: 'file-table-wrap' });

    const btnSelAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllFiles(true) });
    const btnSelNone = el('button', { class: 'btn btn-sm', text: '取消选中', onclick: () => toggleAllFiles(false) });
    const selCount = el('span', { class: 'text-dim', text: '已选 0 个' });
    const dlZipChk = el('input', { type: 'checkbox', id: 'files-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('多文件打包 zip')]);
    // v0.5 #18：可选的本地下载目录。留空走默认 download_dir。
    // 写绝对路径（如 D:\ops-downloads）落到指定位置；写相对路径（如 backups）落到默认 download_dir 同级。
    const dlTargetDirInp = el('input', { type: 'text', id: 'files-target-dir', placeholder: '本地下载目录（留空走默认）', style: 'min-width: 240px;' });
    const btnDownload = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownload });
    const btnCancel = el('button', { class: 'btn btn-danger', text: '取消下载', onclick: doCancelDownload });
    btnDownload.disabled = true;
    btnCancel.disabled = true;

    const fileCard = el('div', { class: 'card' });
    fileCard.appendChild(el('h3', { text: '3. 选择并下载' }));
    fileCard.appendChild(el('div', { class: 'file-toolbar' }, [
      btnSelAll, btnSelNone, selCount, dlZipLabel, btnDownload, btnCancel
    ]));
    fileCard.appendChild(el('div', { class: 'mt-2', style: 'display:flex; gap:8px; align-items:center;' }, [
      el('span', { class: 'lbl', text: '本地目录：' }),
      dlTargetDirInp
    ]));
    fileCard.appendChild(tableWrap);

    view.appendChild(warnBox);
    view.appendChild(connCard);
    view.appendChild(pathCard);
    view.appendChild(fileCard);

    // =================== 行为 ===================

    function loadCfg() {
      return api('GET', '/api/config').then(info => {
        state.cfg = info;
        sysSel.innerHTML = '';
        sysSel.appendChild(el('option', { value: '', text: '（请选择）' }));
        (info.systems || []).forEach(sys => {
          sysSel.appendChild(el('option', { value: sys.name, text: sys.name + (sys.description ? ' · ' + sys.description : '') }));
        });
        // 恢复上次选择
        const lastSel = OTB.core.lastGet('files', 'sel');
        if (lastSel && lastSel.system && (info.systems || []).find(s => s.name === lastSel.system)) {
          sysSel.value = lastSel.system;
          setSystem(lastSel.system);
          if (lastSel.server && (state.cfg.systems.find(s => s.name === lastSel.system) || {}).servers
              && (state.cfg.systems.find(s => s.name === lastSel.system).servers || []).find(s => s.name === lastSel.server)) {
            srvSel.value = lastSel.server;
            setServer(lastSel.server);
          }
          if (lastSel.username) userInp.value = lastSel.username;
          // 默认勾上"记住密码"（keyring 模式下由 refreshCredStatus 决定是否禁用）
          rememberChk.checked = true;
        }
        renderFileBrowserWarning(info);
      });
    }

    function renderFileBrowserWarning(info) {
      const app = (info && info.app) || {};
      const enabled = app.enable_free_file_browser === undefined || app.enable_free_file_browser === true;
      const roots = Array.isArray(app.free_file_roots) ? app.free_file_roots : [];

      while (warnBody.firstChild) warnBody.removeChild(warnBody.firstChild);
      const titleEl = $('#files-warn-title');

      if (!enabled) {
        titleEl.textContent = '⛔ 文件浏览器已关闭';
        warnBody.appendChild(el('div', { class: 'text-dim', text:
          'config.yaml 里 app.enable_free_file_browser = false。整个文件浏览功能已停用。' }));
        warnBox.style.display = '';
        return;
      }
      if (roots.length > 0) {
        titleEl.textContent = '✓ 文件浏览器（白名单模式）';
        warnBody.appendChild(el('div', { class: 'text-dim', text:
          '仅以下前缀的路径可访问（其它路径会被服务端拒绝 403）：' }));
        const ul = el('ul', { style: 'margin: 6px 0 0 0; padding-left: 20px;' });
        roots.forEach(r => ul.appendChild(el('li', { text: r })));
        warnBody.appendChild(ul);
        warnBox.style.display = '';
        return;
      }
      titleEl.textContent = '⚠ 文件浏览器（自由模式 · 无白名单）';
      warnBody.appendChild(el('div', { class: 'text-err', text:
        '当前可访问任何 SSH 账号有权限的路径（包括 /etc、/root 等敏感目录）。' }));
      warnBody.appendChild(el('div', { class: 'text-dim mt-1', style: 'font-size: 12px;', text:
        '建议在 config.yaml 加 app.free_file_roots（如 /var/log、/opt/websphere）以缩小可访问范围。' }));
      warnBox.style.display = '';
    }

    function refreshCredStatus() {
      if (!state.currentSys || !state.currentSrv) {
        rememberChk.checked = false;
        rememberChk.disabled = true;
        return;
      }
      rememberChk.disabled = false;
      api('GET', '/api/credentials/has?system=' + encodeURIComponent(state.currentSys) + '&server=' + encodeURIComponent(state.currentSrv))
        .then(r => {
          const mode = r.mode || 'keyring';
          const storeDisabled = (mode === 'disabled') || (mode === 'file');
          if (storeDisabled) {
            rememberChk.checked = false;
            rememberChk.disabled = true;
            rememberLabel.style.display = 'none';
          } else {
            rememberChk.disabled = false;
            rememberLabel.style.display = '';
            rememberChk.checked = !!r.has;
          }
        })
        .catch(() => { rememberChk.checked = false; });
    }

    function creds() {
      const u = userInp.value.trim();
      return {
        username: u,
        password: passInp.value,
        remember: rememberChk.checked
      };
    }

    function persistSelection() {
      OTB.core.lastSet('files', 'sel', {
        system: state.currentSys,
        server: state.currentSrv,
        username: userInp.value
      });
    }

    function setSystem(name) {
      state.currentSys = name;
      state.currentSrv = '';
      srvSel.innerHTML = '';
      srvSel.appendChild(el('option', { value: '', text: '（请选择）' }));
      const sys = (state.cfg.systems || []).find(s => s.name === name);
      if (!sys) { srvSel.disabled = true; persistSelection(); return; }
      (sys.servers || []).forEach(srv => {
        srvSel.appendChild(el('option', { value: srv.name, text: srv.name + ' · ' + srv.host + ':' + srv.port }));
      });
      srvSel.disabled = false;
      userInp.value = (sys.servers && sys.servers[0] && sys.servers[0].username) || '';
      refreshCredStatus();
      persistSelection();
    }

    function setServer(name) {
      state.currentSrv = name;
      const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
      const srv = sys && (sys.servers || []).find(s => s.name === name);
      if (srv && srv.username && !userInp.value) {
        userInp.value = srv.username;
      }
      refreshCredStatus();
      persistSelection();
    }

    sysSel.addEventListener('change', () => setSystem(sysSel.value));
    srvSel.addEventListener('change', () => setServer(srvSel.value));
    userInp.addEventListener('input', () => { clearTimeout(userInp._t); userInp._t = setTimeout(persistSelection, 500); });

    btnConnect.addEventListener('click', async () => {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器', 'warn'); return;
      }
      const c = creds();
      if (!c.username) { toast('请输入 SSH 用户名', 'warn'); return; }
      if (c.remember && c.password) {
        try {
          await api('POST', '/api/credentials/save', {
            system: state.currentSys, server: state.currentSrv, username: c.username, password: c.password
          });
        } catch (e) { /* ignore */ }
      }
      const startPath = pickDefaultPath(c.username);
      await doListDir(startPath, c);
    });

    function pickDefaultPath(username) {
      try {
        const sys = (state.cfg && state.cfg.systems || []).find(s => s.name === state.currentSys);
        const srv = sys && (sys.servers || []).find(s => s.name === state.currentSrv);
        const ld = srv && (srv.log_dirs || [])[0];
        if (ld && ld.path) return ld.path;
      } catch (e) { /* ignore */ }
      if (username) return '/home/' + username;
      return '/';
    }

    btnParent.addEventListener('click', () => {
      if (state.parent && state.parent !== state.currentPath) {
        const c = creds();
        doListDir(state.parent, c);
      }
    });
    btnRefresh.addEventListener('click', () => {
      const c = creds();
      doListDir(state.currentPath, c);
    });
    pathInp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        const p = pathInp.value.trim();
        if (p && p.startsWith('/')) {
          const c = creds();
          doListDir(p, c);
        } else {
          toast('路径必须是绝对路径（以 / 开头）', 'warn');
        }
      }
    });

    async function doListDir(path, c) {
      try {
        const r = await api('POST', '/api/files/list', {
          system: state.currentSys, server: state.currentSrv,
          username: c.username, password: c.password, path: path
        });
        state.currentPath = r.path;
        state.parent = r.parent || '';
        state.entries = r.entries || [];
        state.selected.clear();
        renderCrumbs();
        renderTable();
      } catch (e) {
        state.entries = [];
        state.selected.clear();
        renderCrumbs();
        renderTable();
        toast('列出目录失败：' + e.message, 'err');
      }
    }

    function renderCrumbs() {
      crumbsEl.innerHTML = '';
      const parts = state.currentPath.split('/').filter(Boolean);
      const head = el('a', { href: '#', text: '/', onclick: (e) => {
        e.preventDefault(); doListDir('/', creds());
      }});
      crumbsEl.appendChild(head);
      let acc = '';
      parts.forEach((seg, i) => {
        crumbsEl.appendChild(document.createTextNode(' / '));
        acc += '/' + seg;
        const target = acc;
        crumbsEl.appendChild(el('a', { href: '#', text: seg, onclick: (e) => {
          e.preventDefault(); doListDir(target, creds());
        }}));
      });
      pathInp.value = state.currentPath;
    }

    function sortedEntries() {
      const arr = state.entries.slice();
      const k = state.sortKey;
      const desc = state.sortDesc;
      arr.sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        let av, bv;
        if (k === 'size') { av = a.size; bv = b.size; }
        else if (k === 'mtime') { av = a.mtime || ''; bv = b.mtime || ''; }
        else { av = (a.name || '').toLowerCase(); bv = (b.name || '').toLowerCase(); }
        if (av < bv) return desc ? 1 : -1;
        if (av > bv) return desc ? -1 : 1;
        return 0;
      });
      return arr;
    }

    function renderTable() {
      tableWrap.innerHTML = '';
      if (!state.entries.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim', text: '（目录为空，或列表失败 — 看上面的提示）' }));
        return;
      }
      const tbl = el('table', { class: 'table' });
      const thead = el('thead');
      const sortLink = (label, key) => {
        const isActive = state.sortKey === key;
        const arrow = isActive ? (state.sortDesc ? ' ↓' : ' ↑') : '';
        return el('a', { href: '#', text: label + arrow, onclick: (e) => {
          e.preventDefault();
          if (state.sortKey === key) state.sortDesc = !state.sortDesc;
          else { state.sortKey = key; state.sortDesc = false; }
          renderTable();
        }});
      };
      thead.appendChild(el('tr', null, [
        el('th', { class: 'col-check' }),
        el('th', null, [sortLink('名称', 'name')]),
        el('th', null, [sortLink('大小', 'size')]),
        el('th', null, [sortLink('修改时间', 'mtime')]),
        el('th', null, [document.createTextNode('权限')]),
        el('th', { class: 'col-status' }, [document.createTextNode('状态')])
      ]));
      tbl.appendChild(thead);

      const tbody = el('tbody');
      sortedEntries().forEach(entry => {
        const fullPath = (state.currentPath === '/' ? '' : state.currentPath) + '/' + entry.name;
        const tr = el('tr', { 'data-path': fullPath, 'data-name': entry.name });
        const cb = el('input', { type: 'checkbox' });
        cb.checked = state.selected.has(entry.name);
        cb.disabled = entry.isDir;
        cb.addEventListener('change', () => {
          if (cb.checked) state.selected.add(entry.name);
          else state.selected.delete(entry.name);
          updateSelCount();
        });
        tr.appendChild(el('td', { class: 'col-check' }, [cb]));

        const icon = entry.isDir ? '📁' : '📄';
        const nameCell = el('td');
        if (entry.isDir) {
          nameCell.appendChild(el('a', { href: '#', text: entry.name, onclick: (e) => {
            e.preventDefault(); doListDir(fullPath, creds());
          }}));
        } else {
          nameCell.appendChild(document.createTextNode(entry.name));
        }
        nameCell.insertBefore(document.createTextNode(icon + ' '), nameCell.firstChild);
        tr.appendChild(nameCell);

        tr.appendChild(el('td', { class: 'num' }, [document.createTextNode(entry.isDir ? '—' : formatBytes(entry.size))]));
        tr.appendChild(el('td', null, [document.createTextNode(entry.mtime ? formatTime(entry.mtime) : '-')]));
        tr.appendChild(el('td', null, [document.createTextNode(entry.mode || '-')]));
        tr.appendChild(el('td', { class: 'col-status status-cell', 'data-name': entry.name }, [document.createTextNode('')]));

        tbody.appendChild(tr);
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
      updateSelCount();
      // 恢复下载进行中的进度条
      Object.keys(state.fileStates).forEach(p => {
        const st = state.fileStates[p];
        if (!st) return;
        const base = p.split('/').pop();
        setRowStatusByName(base, st);
      });
    }

    function updateSelCount() {
      const n = state.selected.size;
      selCount.textContent = '已选 ' + n + ' 个';
      btnDownload.disabled = n === 0 || !!state.dlId;
      btnDownload.textContent = n > 1 ? ('下载选中 (' + n + ')') : '下载选中';
    }

    function toggleAllFiles(on) {
      state.selected.clear();
      if (on) {
        state.entries.forEach(e => { if (!e.isDir) state.selected.add(e.name); });
      }
      renderTable();
    }

    function setRowStatusByName(name, st) {
      const cell = tableWrap.querySelector('tr[data-name="' + cssEscape(name) + '"] .status-cell')
                || tableWrap.querySelector('tr .status-cell[data-name="' + cssEscape(name) + '"]');
      if (!cell) return;
      while (cell.firstChild) cell.removeChild(cell.firstChild);
      const node = buildStatusNodeByName(st);
      if (node) cell.appendChild(node);
    }

    function buildStatusNodeByName(st) {
      if (!st) return null;
      if (st.status === 'pending') {
        return el('span', { class: 'dl-pct', text: '等待…' });
      }
      if (st.status === 'downloading') {
        const wrap = el('span');
        const bar = el('div', { class: 'dl-bar' });
        const totalKnown = st.total && st.total > 0;
        const pct = totalKnown ? Math.min(100, ((st.written || 0) / st.total) * 100) : 0;
        const fill = el('div', {
          class: 'dl-bar-fill' + (totalKnown ? '' : ' indeterminate'),
          style: totalKnown ? ('width:' + pct + '%') : ''
        });
        bar.appendChild(fill);
        wrap.appendChild(bar);
        if (totalKnown) {
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(el('span', { class: 'dl-pct', text: pctText(st.written || 0, st.total) }));
        } else {
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(el('span', { class: 'dl-pct', text: '下载中' }));
        }
        return wrap;
      }
      if (st.status === 'done') {
        const span = el('span', { class: 'dl-pct', style: 'color:#10b981' });
        span.appendChild(document.createTextNode('✓ 完成 · ' + formatBytes(st.bytes || 0)));
        return span;
      }
      if (st.status === 'fail') {
        const span = el('span', { class: 'dl-pct', style: 'color:#ef4444' });
        span.appendChild(document.createTextNode('✗ ' + (st.error || '失败')));
        return span;
      }
      return null;
    }

    async function doDownload() {
      if (state.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const c = creds();
      if (!c.username) { toast('请输入 SSH 用户名', 'warn'); return; }
      const names = Array.from(state.selected);
      if (!names.length) { toast('请先勾选文件', 'warn'); return; }
      const paths = names.map(n => (state.currentPath === '/' ? '' : state.currentPath) + '/' + n);
      const wantZip = dlZipChk.checked;
      const zip = wantZip && paths.length >= 2;
      if (wantZip && paths.length < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
      }
      state.fileStates = {};
      paths.forEach(p => {
        state.fileStates[p] = { status: 'pending' };
        setRowStatusByName(p.split('/').pop(), state.fileStates[p]);
      });
      btnDownload.disabled = true;
      btnCancel.disabled = false;
      setStatus('busy', '下载中…');
      try {
        const r = await api('POST', '/api/files/download', {
          system: state.currentSys, server: state.currentSrv,
          username: c.username, password: c.password,
          paths: paths, zip: zip,
          target_dir: (dlTargetDirInp.value || '').trim()
        });
        state.dlId = r.id;
        if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); return; }
        const es = new EventSource('/api/files/download/' + r.id + '/events');
        state.dlEvtSrc = es;
        OTB.core.setActiveDL({ id: r.id, evtsrc: es });
        let gotDone = false;
        const onDoneSeen = (reason) => {
          if (gotDone) return;
          gotDone = true;
          closeDownloadStream(reason);
        };
        es.onmessage = (ev) => {
          let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
          if (o && o.kind === 'done') {
            handleDownloadEvent(o);
            onDoneSeen('done');
            return;
          }
          handleDownloadEvent(o);
        };
        es.addEventListener('done', () => { onDoneSeen('done'); });
        es.onerror = () => {
          setTimeout(() => {
            if (state.dlId && state.dlEvtSrc === es && !gotDone) {
              onDoneSeen('error');
              toast('SSE 连接异常（已强制收尾）', 'err');
            }
          }, 2000);
        };
      } catch (e) {
        toast('启动下载失败：' + e.message, 'err');
        paths.forEach(p => {
          state.fileStates[p] = { status: 'fail', error: e.message };
          setRowStatusByName(p.split('/').pop(), state.fileStates[p]);
        });
        btnDownload.disabled = false;
        btnCancel.disabled = true;
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    function handleDownloadEvent(o) {
      if (o.kind === 'file_start') {
        state.fileStates[o.file] = { status: 'downloading', written: 0, total: o.total || -1 };
        setRowStatusByName(o.file.split('/').pop(), state.fileStates[o.file]);
      } else if (o.kind === 'progress') {
        const st = state.fileStates[o.file] || {};
        st.status = 'downloading'; st.written = o.written; st.total = o.total;
        state.fileStates[o.file] = st;
        setRowStatusByName(o.file.split('/').pop(), st);
      } else if (o.kind === 'file_done') {
        state.fileStates[o.file] = { status: 'done', bytes: o.bytes };
        setRowStatusByName(o.file.split('/').pop(), state.fileStates[o.file]);
      } else if (o.kind === 'done') {
        btnDownload.disabled = state.selected.size === 0;
        btnCancel.disabled = true;
        state.dlId = null;
        if (o.ok) {
          toast('下载完成：' + (o.downloads || []).length + ' 个产物（去「下载历史」页打开/管理）', 'ok');
        } else {
          toast('下载失败：' + (o.error || '未知错误'), 'err');
          Object.keys(state.fileStates).forEach(p => {
            const st = state.fileStates[p];
            if (st.status === 'pending' || st.status === 'downloading') {
              st.status = 'fail'; st.error = o.error || '';
              setRowStatusByName(p.split('/').pop(), st);
            }
          });
        }
        setStatus('idle');
        state.dlEvtSrc = null;
      }
    }

    function closeDownloadStream(reason) {
      if (state.dlEvtSrc) {
        state.dlEvtSrc.close();
        state.dlEvtSrc = null;
      }
      btnDownload.disabled = state.selected.size === 0;
      btnCancel.disabled = true;
      if (reason === 'cancel') toast('已停止下载', 'warn');
      setStatus('idle');
    }

    async function doCancelDownload() {
      if (!state.dlId) return;
      try { await api('POST', '/api/files/download/' + state.dlId + '/cancel', {}); }
      catch (e) { /* ignore */ }
      closeDownloadStream('cancel');
    }

    loadCfg().then(refreshCredStatus).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  OTB.pages.files = renderFiles;
  OTB.state.routes.files = renderFiles;
  OTB.state.routeNames.files = '文件下载（FTP 风格）';
})();