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
      filteredEntries: [], // 项 17：模糊过滤后的视图（前端 filter）
      selected: new Set(),
      sortKey: 'name',
      sortDesc: false,
      dlId: null,
      dlEvtSrc: null,
      fileStates: {},
      // 项 17：文件名模糊过滤（用户输入）
      filter: '',
      // 项 2：常用目录（每个 server 独立一组；存 localStorage）
      //   结构：{ "systemA/serverA": [{ name, path }, ...], ... }
      // 初始为空；loadCfg 完成后调 loadCommonDirs 填充。
      commonDirs: {},
      commonDirsKey: '', // 当前 system/server 拼出来的 key
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

    // v0.5 #2：常用目录栏（点即跳转；星标加入/管理）
    const commonDirsBar = el('div', { class: 'common-dirs-bar', style: 'margin-top: 10px; display: flex; flex-wrap: wrap; gap: 6px; align-items: center;' });
    const commonDirsLabel = el('span', { class: 'lbl', text: '常用目录：' });
    commonDirsBar.appendChild(commonDirsLabel);
    // ★ 收藏当前路径 按钮（连同当前 system+server 存进 localStorage）
    const btnBookmarkCurrent = el('button', { class: 'btn btn-sm', text: '⭐ 收藏当前路径', onclick: bookmarkCurrentPath });
    // ⚙ 管理 按钮（弹简单 inline 列表，可改别名/路径/删）
    const btnManageCommonDirs = el('button', { class: 'btn btn-sm', text: '⚙ 管理常用目录', onclick: openManageCommonDirs });
    pathCard.appendChild(commonDirsBar);
    pathCard.appendChild(el('div', { class: 'mt-1 row gap-2' }, [btnBookmarkCurrent, btnManageCommonDirs]));

    // ---- 文件列表区 ----
    const tableWrap = el('div', { class: 'file-table-wrap' });

    const btnSelAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllFiles(true) });
    const btnSelNone = el('button', { class: 'btn btn-sm', text: '取消选中', onclick: () => toggleAllFiles(false) });
    const selCount = el('span', { class: 'text-dim', text: '已选 0 个' });
    const dlZipChk = el('input', { type: 'checkbox', id: 'files-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('多文件打包 zip')]);
    // v0.5 #18：可选的本地下载目录。留空走默认 download_dir。
    // 写绝对路径（如 D:\ops-downloads）落到指定位置；写相对路径（如 backups）落到默认 download_dir 同级。
    const dlTargetDirInp = el('input', { type: 'text', id: 'files-target-dir', placeholder: '本地下载目录（留空走默认）', style: 'min-width: 240px;', title: '留空 → 走 cfg.download_dir。\n写绝对路径 → 落到指定目录（受 app.allowed_download_roots 白名单约束）。' });
    const btnDownload = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownload });
    const btnCancel = el('button', { class: 'btn btn-danger', text: '取消下载', onclick: doCancelDownload });
    btnDownload.disabled = true;
    btnCancel.disabled = true;

    const fileCard = el('div', { class: 'card' });
    fileCard.appendChild(el('h3', { text: '3. 选择并下载' }));
    // v0.5 #17：文件名模糊过滤（前端实时；支持子串 / *.log / log?）
    const filterInp = el('input', {
      type: 'text',
      id: 'files-filter',
      placeholder: '过滤文件名（子串 / 通配符 * ?, 例 SystemOut 或 *.log）',
      style: 'min-width: 280px;'
    });
    filterInp.addEventListener('input', () => {
      state.filter = filterInp.value || '';
      OTB.core.lastSet('files', 'filter', state.filter);
      renderTable();
    });
    const filterClearBtn = el('button', { class: 'btn btn-sm', text: '清空', onclick: () => {
      filterInp.value = '';
      state.filter = '';
      OTB.core.lastSet('files', 'filter', '');
      renderTable();
    }});
    const filterCountEl = el('span', { id: 'files-filter-count', class: 'text-dim' });
    fileCard.appendChild(el('div', { class: 'mt-2', style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap;' }, [
      el('span', { class: 'lbl', text: '过滤：' }),
      filterInp, filterClearBtn, filterCountEl
    ]));
    fileCard.appendChild(el('div', { class: 'file-toolbar' }, [
      btnSelAll, btnSelNone, selCount, dlZipLabel, btnDownload, btnCancel
    ]));
    fileCard.appendChild(el('div', { class: 'mt-2', style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap;' }, [
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
        // 恢复 filter lastGet
        const lastFilter = OTB.core.lastGet('files', 'filter');
        if (lastFilter) {
          state.filter = lastFilter;
          filterInp.value = lastFilter;
        }
        // 加载常用目录 + 渲染 bar
        state.commonDirs = loadCommonDirs();
        renderCommonDirsBar();
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
            // P1-13 修复：不再因为"未保存密码"就取消勾选（用户可能想保存新密码）
            // 只有 storeDisabled 时才强制取消；正常情况保持用户的选择。
            if (!storeDisabled && !r.has) {
              // 未保存密码时什么都不做（保持 rememberChk.checked 当前值）
            }
          }
        })
        .catch(() => { /* 凭据检查失败，保持当前状态 */ });
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
      if (!sys) { srvSel.disabled = true; persistSelection(); renderCommonDirsBar(); return; }
      (sys.servers || []).forEach(srv => {
        srvSel.appendChild(el('option', { value: srv.name, text: srv.name + ' · ' + srv.host + ':' + srv.port }));
      });
      srvSel.disabled = false;
      userInp.value = (sys.servers && sys.servers[0] && sys.servers[0].username) || '';
      refreshCredStatus();
      persistSelection();
      renderCommonDirsBar();
    }

    // P1-11：切换服务器时，把该服务器的 log_dirs 自动插入常用目录栏前面
    function prependLogDirsToCommonDirs(srv) {
      if (!srv || !srv.log_dirs || !srv.log_dirs.length) return;
      const k = currentCommonDirsKey();
      if (!k) return;
      const existing = state.commonDirs[k] || [];
      const logDirs = srv.log_dirs.filter(ld => {
        // 避免重复
        return !existing.find(e => e.path === ld.path);
      });
      if (!logDirs.length) return;
      state.commonDirs[k] = [...logDirs.map(ld => ({ name: ld.name || ld.path, path: ld.path })), ...existing];
      saveCommonDirs();
    }

    function setServer(name) {
      state.currentSrv = name;
      const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
      const srv = sys && (sys.servers || []).find(s => s.name === name);
      if (srv && srv.username && !userInp.value) {
        userInp.value = srv.username;
      }
      // P1-11：自动把服务器配置的 log_dirs 加进常用目录
      prependLogDirsToCommonDirs(srv);
      refreshCredStatus();
      persistSelection();
      renderCommonDirsBar();
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

    // ---- 文件预览（v0.5 项 1）----
    // 普通点击文件名 → 弹窗显示（modal 快速看）
    // Shift+点击 / 或显式调用 → 新窗口预览（独立页 preview.html）
    async function openPreview(filePath, fileName) {
      const c = creds();
      if (!c.username) { toast('请先输入 SSH 用户名', 'warn'); return; }
      // 拿这个 server 的目录默认 encoding
      let encoding = 'utf-8';
      try {
        const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
        const srv = sys && (sys.servers || []).find(s => s.name === state.currentSrv);
        const ld = srv && (srv.log_dirs || [])[0];
        if (ld && ld.encoding) encoding = String(ld.encoding).toLowerCase();
      } catch (e) { /* keep utf-8 */ }
      try {
        const r = await api('POST', '/api/files/preview', {
          system: state.currentSys, server: state.currentSrv,
          username: c.username, password: c.password,
          path: filePath, encoding: encoding, max_bytes: 1048576
        });
        showPreviewModal(r, fileName, filePath, encoding);
      } catch (e) {
        toast('预览失败：' + e.message, 'err');
      }
    }

    // openPreviewInNewWindow 开新窗口（preview.html），凭证走 OTB._previewCred 跨窗口传递
    function openPreviewInNewWindow(filePath, fileName) {
      const c = creds();
      if (!c.username) { toast('请先输入 SSH 用户名', 'warn'); return; }
      let encoding = 'utf-8';
      try {
        const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
        const srv = sys && (sys.servers || []).find(s => s.name === state.currentSrv);
        const ld = srv && (srv.log_dirs || [])[0];
        if (ld && ld.encoding) encoding = String(ld.encoding).toLowerCase();
      } catch (e) { /* keep utf-8 */ }
      // 凭证跨窗口传递（同源 opener 可直接读）
      OTB._previewCred = OTB._previewCred || {};
      OTB._previewCred[state.currentSys + '::' + state.currentSrv] = {
        username: c.username, password: c.password
      };
      const q = new URLSearchParams({
        system: state.currentSys, server: state.currentSrv,
        path: filePath, encoding: encoding
      }).toString();
      const w = window.open('/preview.html?' + q, '_blank');
      if (!w) {
        toast('浏览器拦截了新窗口（请允许弹窗）', 'warn');
        // fallback 到 modal
        openPreview(filePath, fileName);
      }
    }

    function showPreviewModal(r, fileName, filePath, encoding) {
      // 关闭已有
      const old = document.getElementById('files-preview-modal');
      if (old) old.remove();
      const overlay = el('div', { id: 'files-preview-modal', class: 'preview-overlay' });
      const box = el('div', { class: 'preview-box' });
      const head = el('div', { class: 'preview-head' });
      head.appendChild(el('div', { class: 'preview-title' }, [
        el('strong', { text: fileName }),
        el('span', { class: 'text-dim', text: ' · ' + filePath + ' · ' + encoding })
      ]));
      const meta = [];
      meta.push('大小 ' + formatBytes(r.size));
      meta.push('已读 ' + formatBytes(r.bytes_read || 0));
      if (r.truncated) meta.push('已截断（文件 > 1MB）');
      if (r.is_binary) meta.push('二进制文件');
      head.appendChild(el('div', { class: 'text-dim', text: meta.join('  ·  ') }));
      // 项 1：modal 里加 "在新窗口打开" 按钮（满足用户原话 "新浏览器窗口预览"）
      const btnOpenWin = el('button', { class: 'btn btn-sm', text: '↗ 在新窗口打开', onclick: () => {
        overlay.remove();
        openPreviewInNewWindow(filePath, fileName);
      }});
      const btnClose = el('button', { class: 'btn btn-sm', text: '关闭', onclick: () => overlay.remove() });
      head.appendChild(el('div', { style: 'display:flex;gap:6px;' }, [btnOpenWin, btnClose]));
      box.appendChild(head);
      const body = el('pre', { class: 'preview-body' });
      if (r.is_binary) {
        body.textContent = '⟦二进制文件不可预览（共 ' + r.size + ' 字节，前 ' + r.bytes_read + ' 字节）⟧';
        body.style.color = 'var(--text-dim)';
      } else {
        body.textContent = r.content || '(空)';
      }
      box.appendChild(body);
      overlay.appendChild(box);
      overlay.addEventListener('click', (e) => { if (e.target === overlay) overlay.remove(); });
      document.addEventListener('keydown', function onEsc(e) {
        if (e.key === 'Escape') { overlay.remove(); document.removeEventListener('keydown', onEsc); }
      });
      document.body.appendChild(overlay);
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

    // ========== v0.5 项 2：常用目录（localStorage，按 system/server 分组） ==========
    // 数据结构：
    //   state.commonDirs = {
    //     "systemA/serverA": [ { name: "server1 日志", path: "/opt/IBM/.../server1" }, ... ],
    //     ...
    //   }
    // 持久化键：localStorage["ops.files.common_dirs"] = JSON
    // 切换 system/server 时重新渲染 commonDirsBar
    const COMMON_DIRS_LS_KEY = 'ops.files.common_dirs';

    function loadCommonDirs() {
      try {
        const raw = localStorage.getItem(COMMON_DIRS_LS_KEY);
        if (!raw) return {};
        const obj = JSON.parse(raw);
        return (obj && typeof obj === 'object') ? obj : {};
      } catch (e) { return {}; }
    }
    function saveCommonDirs() {
      try { localStorage.setItem(COMMON_DIRS_LS_KEY, JSON.stringify(state.commonDirs)); }
      catch (e) { /* quota / private mode 静默 */ }
    }
    function currentCommonDirsKey() {
      if (!state.currentSys || !state.currentSrv) return '';
      return state.currentSys + '/' + state.currentSrv;
    }
    function getCurrentCommonDirs() {
      const k = currentCommonDirsKey();
      if (!k) return [];
      return state.commonDirs[k] || [];
    }
    function setCurrentCommonDirs(arr) {
      const k = currentCommonDirsKey();
      if (!k) return;
      state.commonDirs[k] = arr;
      saveCommonDirs();
    }

    function renderCommonDirsBar() {
      // 清空但保留 label（第 0 个子节点是 commonDirsLabel）
      while (commonDirsBar.children.length > 1) {
        commonDirsBar.removeChild(commonDirsBar.lastChild);
      }
      const list = getCurrentCommonDirs();
      if (!list.length) {
        commonDirsBar.appendChild(el('span', { class: 'text-dim', text: '（暂无，点「⭐ 收藏当前路径」添加）' }));
        return;
      }
      list.forEach((item, idx) => {
        const btn = el('button', {
          class: 'btn btn-sm',
          title: item.path,
          text: '📂 ' + item.name,
          onclick: (e) => {
            e.preventDefault();
            pathInp.value = item.path;
            doListDir(item.path, creds());
          }
        });
        commonDirsBar.appendChild(btn);
      });
    }

    function bookmarkCurrentPath() {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器', 'warn'); return;
      }
      const path = state.currentPath || '/';
      // 弹简单输入框：默认别名 = basename
      const defaultName = path.split('/').filter(Boolean).pop() || path || '/';
      const name = window.prompt('为这个常用目录起个名字（按钮显示用）:', defaultName);
      if (!name) return;
      const list = getCurrentCommonDirs().slice();
      // 同 path 覆盖
      const existIdx = list.findIndex(x => x.path === path);
      if (existIdx >= 0) {
        list[existIdx].name = name;
      } else {
        list.push({ name: name, path: path });
      }
      setCurrentCommonDirs(list);
      renderCommonDirsBar();
      toast('已收藏：' + name + ' → ' + path, 'ok');
    }

    function openManageCommonDirs() {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器', 'warn'); return;
      }
      const list = getCurrentCommonDirs().slice();
      // 简单 modal：列表 + 改别名/改路径/删除/上移下移
      const old = document.getElementById('files-common-dirs-modal');
      if (old) old.remove();
      const overlay = el('div', { id: 'files-common-dirs-modal', class: 'preview-overlay' });
      const box = el('div', { class: 'preview-box', style: 'max-width: 720px;' });
      const head = el('div', { class: 'preview-head' });
      head.appendChild(el('strong', { text: '管理常用目录 · ' + state.currentSys + ' / ' + state.currentSrv }));
      const btnClose = el('button', { class: 'btn btn-sm', text: '关闭', onclick: () => overlay.remove() });
      head.appendChild(btnClose);
      box.appendChild(head);

      const listEl = el('div');
      function rerenderList() {
        while (listEl.firstChild) listEl.removeChild(listEl.firstChild);
        if (!list.length) {
          listEl.appendChild(el('div', { class: 'text-dim', text: '（暂无常用目录）' }));
          return;
        }
        list.forEach((item, idx) => {
          const row = el('div', { style: 'display:flex; gap:6px; align-items:center; padding:6px 0; border-bottom: 1px solid var(--line);' });
          const nameInp = el('input', { type: 'text', value: item.name, style: 'flex: 0 0 200px;' });
          nameInp.addEventListener('change', () => { list[idx].name = nameInp.value; });
          const pathInp2 = el('input', { type: 'text', value: item.path, style: 'flex: 1 1 auto;' });
          pathInp2.addEventListener('change', () => { list[idx].path = pathInp2.value; });
          const upBtn = el('button', { class: 'btn btn-sm', text: '↑', disabled: idx === 0, onclick: () => {
            if (idx > 0) { const t = list[idx - 1]; list[idx - 1] = list[idx]; list[idx] = t; rerenderList(); }
          }});
          const downBtn = el('button', { class: 'btn btn-sm', text: '↓', disabled: idx === list.length - 1, onclick: () => {
            if (idx < list.length - 1) { const t = list[idx + 1]; list[idx + 1] = list[idx]; list[idx] = t; rerenderList(); }
          }});
          const delBtn = el('button', { class: 'btn btn-sm btn-danger', text: '删除', onclick: () => {
            list.splice(idx, 1); rerenderList();
          }});
          row.appendChild(nameInp);
          row.appendChild(pathInp2);
          row.appendChild(upBtn);
          row.appendChild(downBtn);
          row.appendChild(delBtn);
          listEl.appendChild(row);
        });
      }
      rerenderList();
      box.appendChild(listEl);

      const footer = el('div', { style: 'margin-top: 12px; display: flex; gap: 8px; justify-content: flex-end;' });
      footer.appendChild(el('button', { class: 'btn btn-sm', text: '+ 新增', onclick: () => {
        list.push({ name: '新目录', path: '/' });
        rerenderList();
      }}));
      footer.appendChild(el('button', { class: 'btn btn-primary', text: '保存', onclick: () => {
        setCurrentCommonDirs(list);
        renderCommonDirsBar();
        overlay.remove();
        toast('常用目录已保存', 'ok');
      }}));
      box.appendChild(footer);
      overlay.appendChild(box);
      overlay.addEventListener('click', (e) => { if (e.target === overlay) overlay.remove(); });
      document.body.appendChild(overlay);
    }

    function sortedEntries() {
      const q = (state.filter || filterInp.value || '').trim().toLowerCase();
      let arr = state.entries.slice();
      // v0.5 #17：按文件名过滤（前端实时；支持通配符 * 和 ?）
      if (q) {
        const re = globToRegex(q);
        arr = arr.filter(e => re.test((e.name || '').toLowerCase()));
      }
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

    // globToRegex 把 "system*.log" / "*.log" / "log?" 这样的 glob 转成正则（已 lowercase）
    function globToRegex(glob) {
      // 占位符承诺"子串 / 通配符 * ?"。
      // 含 * 或 ? → 通配语义（^...$）；否则 → 子串（去掉 ^...$）。
      // 顺序：先把 * ? 替换为占位符，再 escape 其余元字符，
      // 再把占位符还原为 .* / .。否则 * 会被字符类转义打乱。
      if (glob.indexOf('*') !== -1 || glob.indexOf('?') !== -1) {
        const wildcardsReplaced = glob.replace(/\*/g, '\u0001').replace(/\?/g, '\u0002');
        const escaped = wildcardsReplaced.replace(/[.+^${}()|[\]\\]/g, '\\$&')
          .replace(/\u0001/g, '.*').replace(/\u0002/g, '.');
        return new RegExp(escaped, 'i');
      }
      // 纯字串 → 退化为子串匹配（直接 in 一下，避免把普通文本误当正则）
      const lower = glob.toLowerCase();
      return { test: (s) => (s || '').toLowerCase().indexOf(lower) !== -1 };
    }

    function renderTable() {
      tableWrap.innerHTML = '';
      if (!state.entries.length) {
        tableWrap.appendChild(el('div', { class: 'text-dim', text: '（目录为空，或列表失败 — 看上面的提示）' }));
        filterCountEl.textContent = '';
        return;
      }
      // v0.5 #17：过滤后数量显示
      const filtered = sortedEntries();
      const q = (state.filter || filterInp.value || '').trim();
      if (q) {
        filterCountEl.textContent = '过滤后 ' + filtered.length + ' / ' + state.entries.length;
        if (filtered.length === 0) {
          tableWrap.appendChild(el('div', { class: 'text-dim', text: '（过滤 "' + q + '" 无匹配项）' }));
          return;
        }
      } else {
        filterCountEl.textContent = '共 ' + state.entries.length + ' 项';
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
          // P1-10 修复：点击文件名直接新窗口预览（用户原需求），
          // Shift+点击 / Ctrl+点击才弹 modal。modal 作为备用入口。
          const link = el('a', {
            href: '#',
            text: entry.name,
            title: '点击新窗口预览前 1MB 内容（Shift+点击 弹窗预览）',
            onclick: (e) => {
              e.preventDefault();
              if (e.shiftKey || e.ctrlKey || e.metaKey) {
                openPreview(fullPath, entry.name);
              } else {
                openPreviewInNewWindow(fullPath, entry.name);
              }
            }
          });
          nameCell.appendChild(link);
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
          // 项 16：下载完成用 notify 通知（标题 + 路径 + 操作按钮）
          showDownloadDoneNotify(o);
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

    // showDownloadDoneNotify 项 16：下载完成 → 右上角通知（标题 + 路径 + 操作按钮）
    function showDownloadDoneNotify(o) {
      const downloads = o.downloads || [];
      const folder = o.folder || '';
      const firstName = downloads[0] && downloads[0].local || downloads[0] && downloads[0].name || '';
      const fileList = downloads.slice(0, 3).map(d => d.local || d.name || '').filter(Boolean).join(', ');
      const more = downloads.length > 3 ? (' 等 ' + downloads.length + ' 个') : '';
      const title = '✓ 下载完成 · ' + downloads.length + ' 个文件';
      const body = folder + (fileList ? ('\n' + fileList + more) : '');
      const actions = [];
      if (firstName && folder) {
        actions.push({
          label: '📂 打开所在目录',
          callback: async () => {
            try {
              await api('POST', '/api/downloads/' + encodeURIComponent(firstName) + '/open-dir');
            } catch (e) {
              toast('打开目录失败：' + e.message, 'err');
            }
          }
        });
      }
      if (folder) {
        actions.push({
          label: '📋 复制路径',
          callback: () => {
            try {
              if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(folder);
                toast('路径已复制', 'ok');
              } else {
                // fallback：弹 textarea 让用户手动复制
                window.prompt('复制此路径：', folder);
              }
            } catch (e) { toast('复制失败：' + e.message, 'err'); }
          }
        });
      }
      actions.push({
        label: '📜 查看下载历史',
        callback: () => {
          if (location.hash !== '#/downloads') location.hash = '#/downloads';
        }
      });
      OTB.core.notify({
        id: 'download-' + (state.dlId || Date.now()),
        type: 'ok',
        title: title,
        body: body,
        actions: actions,
        duration: 8000
      });
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

    // 启动：从 localStorage 恢复常用目录 + 渲染常用目录栏
state.commonDirs = loadCommonDirs();
renderCommonDirsBar();
loadCfg().then(refreshCredStatus).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  OTB.pages.files = renderFiles;
  OTB.state.routes.files = renderFiles;
  OTB.state.routeNames.files = '文件下载（FTP 风格）';
})();