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
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, $, toast, setStatus, cssEscape, pctText, formatBytes, formatTime, basenameOf, escapeHtml } = Kairo.core;
  const { api, pathRow } = Kairo.api;

  const ICONS = {
    smFolder:    'M2 5a2 2 0 012-2h5l2 2h9a2 2 0 012 2v11a2 2 0 01-2 2H4a2 2 0 01-2-2V5z',
    smFile:      'M6 2h6l4 4v14a2 2 0 01-2 2H6a2 2 0 01-2-2V4a2 2 0 012-2zm6 0v4h4',
    smLink:      'M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71',
    smClipboard: 'M9 2h6a2 2 0 012 2v16a2 2 0 01-2 2H9a2 2 0 01-2-2V4a2 2 0 012-2zm0 2v2h6V4zM8 12h8M8 16h8M8 8h4',
    smEye:       'M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8zM12 15a3 3 0 100-6 3 3 0 000 6z',
    smDownload:  'M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3',
    smScroll:    'M8 3h8M6 7h12M6 11h12M6 15h12M6 19h12M4 2a2 2 0 00-2 2v16a2 2 0 002 2h16a2 2 0 002-2V4a2 2 0 00-2-2z',
    smCheck:     'M5 13l4 4L19 7',
    smX:         'M6 18L18 6M6 6l12 12',
  };
  function svgIcon(name, size) {
    const d = ICONS[name];
    if (!d) return '';
    const s = size || 16;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

  function renderFiles(view) {

    // ---- 新建文件 / 新建文件夹 / 重命名 ----
    async function doNewFile() {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器并连接', 'warn'); return;
      }
      const name = prompt('请输入新文件名：');
      if (!name || !name.trim()) return;
      const fileName = name.trim();
      const targetPath = (state.currentPath === '/' ? '' : state.currentPath) + '/' + fileName;
      try {
        const c = creds();
        await api('POST', '/api/ssh/sftp/create', {
          system: state.currentSys,
          server: state.currentSrv,
          username: c.username,
          password: c.password,
          path: targetPath
        });
        toast('文件已创建：' + fileName, 'ok');
        doListDir(state.currentPath, c);
      } catch (err) {
        toast('创建文件失败：' + (err.message || err), 'err');
      }
    }

    async function doNewFolder() {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器并连接', 'warn'); return;
      }
      const name = prompt('请输入新文件夹名称：');
      if (!name || !name.trim()) return;
      const folderName = name.trim();
      const targetPath = (state.currentPath === '/' ? '' : state.currentPath) + '/' + folderName;
      try {
        const c = creds();
        await api('POST', '/api/ssh/sftp/mkdir', {
          system: state.currentSys,
          server: state.currentSrv,
          username: c.username,
          password: c.password,
          path: targetPath
        });
        toast('文件夹已创建：' + folderName, 'ok');
        doListDir(state.currentPath, c);
      } catch (err) {
        toast('创建文件夹失败：' + (err.message || err), 'err');
      }
    }

    async function doRename(oldName, fullPath, isDir) {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器并连接', 'warn'); return;
      }
      const name = prompt('请输入新的' + (isDir ? '文件夹' : '文件') + '名：', oldName);
      if (!name || !name.trim() || name.trim() === oldName) return;
      const newName = name.trim();
      const parent = state.currentPath === '/' ? '' : state.currentPath;
      const newPath = parent + '/' + newName;
      try {
        const c = creds();
        await api('POST', '/api/ssh/sftp/rename', {
          system: state.currentSys,
          server: state.currentSrv,
          username: c.username,
          password: c.password,
          old_path: fullPath,
          new_path: newPath
        });
        toast('重命名成功', 'ok');
        doListDir(state.currentPath, c);
      } catch (err) {
        toast('重命名失败：' + (err.message || err), 'err');
      }
    }

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
      // 右键菜单状态
      ctxMenu: null,
      ctxTarget: null,
      // 已下载文件的本地路径映射：remotePath -> localName
      downloadedFiles: {},
      // v1.1：上传任务列表（按 basename 索引）
      //   每项：{ name, size, status: 'pending'|'uploading'|'done'|'fail'|'cancel',
      //           progress: 0-100, bytes: 已上传字节, error: 错误信息, uploadId: 后端 id,
      //           finalName: 最终落盘名（rename 策略可能改）, xhr: XMLHttpRequest 引用 }
      uploadQueue: [],
      uploadInflight: false, // 是否正在传一个文件（串行队列控制）
    };

    // ---- 连接区 ----
    const sysSel = el('select', { id: 'files-sys' });
    const srvSel = el('select', { id: 'files-srv' });
    srvSel.appendChild(el('option', { value: '', text: '（先选系统）' }));
    srvSel.disabled = true;
    // P1-BUG-6 修复：remember 初始 disabled（没选 sys/srv 时不让勾）。
    const rememberChk = el('input', { type: 'checkbox', id: 'files-remember' });
    rememberChk.disabled = true;
    const rememberLabel = el('label', { class: 'inline remember-wrap' }, [rememberChk, document.createTextNode('记住密码')]);
    // P0-BUG-2 修复：添加 SSH 用户名 / 密码 输入框。
    // /api/config 不返回 password，所以 srv.password 永远空 → 没输入框用户没法连接。
    // creds() 优先用用户输入，没填才回落到 srv 默认。
    const userInput = el('input', { type: 'text', id: 'files-user-input', placeholder: 'SSH 用户名（默认取服务器配置）', autocomplete: 'username', style: 'flex: 1 1 auto; min-width: 180px;' });
    const passInput = el('input', { type: 'password', id: 'files-pass-input', placeholder: 'SSH 密码（留空 → keyring / 临时）', autocomplete: 'current-password', style: 'flex: 1 1 auto; min-width: 180px;' });
    const btnConnect = el('button', { class: 'btn btn-primary', text: '连接并浏览' });

    // ---- 文件浏览器启动警告（项 14）----
    const warnBox = el('div', { class: 'card', id: 'files-warn', style: 'display:none' });
    warnBox.appendChild(el('h3', { id: 'files-warn-title', text: '' }));
    const warnBody = el('div');
    warnBox.appendChild(warnBody);

    const connCard = el('div', { class: 'card' });
    connCard.appendChild(el('h3', { text: '选择目标服务器' }));
    connCard.appendChild(el('div', { class: 'card-desc', text: '支持任意路径浏览；下载权限以 SSH 账号实际权限为准。' }));
    connCard.appendChild(el('div', { class: 'grid-2' }, [
      el('label', null, [el('span', { class: 'lbl', text: '业务系统' }), sysSel]),
      el('label', null, [el('span', { class: 'lbl', text: '服务器' }), srvSel])
    ]));
    connCard.appendChild(el('div', { class: 'btn-row mt-2' }, [btnConnect]));

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
    const commonDirsBar = el('div', { class: 'common-dirs-bar', style: 'margin: 10px 0 12px 0; display: flex; flex-wrap: wrap; gap: 6px; align-items: center;' });
    const commonDirsLabel = el('span', { class: 'lbl', text: '常用目录：' });
    commonDirsBar.appendChild(commonDirsLabel);
    // ★ 收藏当前路径 按钮（连同当前 system+server 存进 localStorage）
    // UI-7 修复：加 btn-star class 提升 ⭐ 在暗背景下的对比度（淡黄底）。
    const btnBookmarkCurrent = el('button', { class: 'btn btn-sm btn-star', text: '⭐ 收藏当前路径', onclick: bookmarkCurrentPath });
    // ⚙ 管理 按钮（弹简单 inline 列表，可改别名/路径/删）
    const btnManageCommonDirs = el('button', { class: 'btn btn-sm', text: '⚙ 管理常用目录', onclick: openManageCommonDirs });
    pathCard.appendChild(commonDirsBar);
    // P1-BUG-7 修复：⭐ 收藏 和 ⚙ 管理 两个按钮挤一起，显式 gap:14px 把间距拉开。
    const bookmarkRow = el('div', { class: 'mt-1', style: 'display:flex; gap:14px; align-items:center; flex-wrap:wrap;' }, [
      btnBookmarkCurrent, btnManageCommonDirs
    ]);
    pathCard.appendChild(bookmarkRow);

    // ---- 文件列表区 ----
    const tableWrap = el('div', { class: 'file-table-wrap' });

    const btnSelAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllFiles(true) });
    const btnSelNone = el('button', { class: 'btn btn-sm', text: '取消选中', onclick: () => toggleAllFiles(false) });
    const selCount = el('span', { class: 'text-dim', style: 'margin-right:0;', text: '已选 0 个' });
    const dlZipChk = el('input', { type: 'checkbox', id: 'files-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('多文件打包 zip')]);
    // v0.5 #18：可选的本地下载目录。留空走默认 download_dir。
    // 写绝对路径（如 D:\ops-downloads）落到指定位置；写相对路径（如 backups）落到默认 download_dir 同级。
    const dlTargetDirInp = el('input', { type: 'text', id: 'files-target-dir', placeholder: '留空走默认', style: 'min-width: 180px; max-width: 320px; flex: 1 1 180px;', title: '留空 → 走 cfg.download_dir。\n写绝对路径 → 落到指定目录（受 app.allowed_download_roots 白名单约束）。' });
    const btnDownload = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownload });
    const btnCancel = el('button', { class: 'btn btn-danger', text: '取消下载', onclick: doCancelDownload });
    btnDownload.disabled = true;
    btnCancel.disabled = true;

    // ---- 上传控件（v1.3 还原）----
    // v1.2 曾把上传拆成独立第 4 张卡片，用户不要，回到 v1.1 状态：
    //   - 上传按钮（btnUploadPick）放在下载 toolbar 末尾，跟"下载"同 row 紧邻
    //   - 覆盖策略 / 刷新勾选 / 队列 在 fileCard 内部、table 之后（用分隔线划开，不挤）
    //   - 上传按钮挪回下载按钮右侧 —— 不再独立卡片、不再放到页面最下面
    const uploadFileInp = el('input', { type: 'file', multiple: 'multiple', style: 'display:none;' });
    const uploadOverwriteSel = el('select', { id: 'upload-overwrite', title: '同名文件已存在时的处理策略' });
    uploadOverwriteSel.appendChild(el('option', { value: 'reject', text: '拒绝覆盖（默认）' }));
    uploadOverwriteSel.appendChild(el('option', { value: 'replace', text: '直接替换' }));
    uploadOverwriteSel.appendChild(el('option', { value: 'rename', text: '自动重命名（追加 _1/_2）' }));
    const uploadRefreshChk = el('input', { type: 'checkbox', id: 'upload-refresh' });
    const btnUploadPick = el('button', { class: 'btn btn-primary', text: '📤 上传文件', onclick: () => uploadFileInp.click(), title: '上传文件到当前目录' });
    const btnUploadCancelAll = el('button', { class: 'btn btn-danger btn-sm', text: '全取消', onclick: cancelAllUploads, disabled: true });
    const btnUploadClear = el('button', { class: 'btn btn-sm', text: '清空已完成', onclick: clearFinishedUploads });

    const fileCard = el('div', { class: 'card' });
    fileCard.appendChild(el('h3', { text: '3. 文件传输（上传 / 下载）' }));
    fileCard.appendChild(el('div', { class: 'card-desc' }, [
      document.createTextNode('勾选文件后点击"下载选中"保存到本地；点击"上传文件"把本地文件传到当前目录。支持拖拽文件到列表区域上传。')
    ]));
    // v0.5 #17：文件名模糊过滤（前端实时；支持子串 / *.log / log?）
    // 修：项 1 — input 事件**只更新数据 + markDirty**，不重建 DOM。
    // 重建只在 debounce 后（200ms）触发一次；防 layout 抖动 + input 失焦。
    const filterInp = el('input', {
      type: 'text',
      id: 'files-filter',
      placeholder: '过滤文件名（子串 / 通配符 * ?, 例 SystemOut 或 *.log）',
      style: 'flex: 3 1 auto; min-width: 300px; width: auto;'
    });
    let filterDebounce = null;
    filterInp.addEventListener('input', () => {
      state.filter = filterInp.value || '';
      Kairo.core.lastSet('files', 'filter', state.filter);
      // 防抖：避免每个按键都重渲染整张表 → 200ms 静默后才 render
      if (filterDebounce) clearTimeout(filterDebounce);
      filterDebounce = setTimeout(() => {
        filterDebounce = null;
        // 关键：从当前 input 取值（state.filter 已是最新），不在事件里重建 input
        renderTable();
      }, 180);
    });
    const filterClearBtn = el('button', { class: 'btn btn-sm', text: '清空', onclick: () => {
      filterInp.value = '';
      state.filter = '';
      Kairo.core.lastSet('files', 'filter', '');
      renderTable();
    }});
    const filterCountEl = el('span', { id: 'files-filter-count', class: 'text-dim' });
    // 项 1 修复：filter 容器让 .lbl 用 inline-block（不撑成 block），避免 flex 里
    // 出现"过滤"两个字被竖排 / 换行的视觉错乱。
    // 第一行：过滤 + 本地下载目录（输入框）
    const filterLabel = el('label', { class: 'inline', style: 'display:inline-flex; align-items:center; gap:6px;' }, [document.createTextNode('过滤：'), filterInp]);
    fileCard.appendChild(el('div', { class: 'mt-2', style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap;' }, [
      filterLabel, filterClearBtn, filterCountEl,
      el('span', { style: 'flex:1 1 auto;' }),
      el('label', { class: 'inline', style: 'display:inline-flex; align-items:center; gap:6px; white-space:nowrap;' }, [
        document.createTextNode('本地下载目录：'),
        pathRow(dlTargetDirInp, { directory: true, compact: true, rowClass: 'path-field path-field-inline' })
      ])
    ]));
    // 第二行：选择操作 + 主按钮（下载/上传并排，按钮组不换行）
    const btnNewFile = el('button', { class: 'btn btn-sm', text: '➕ 新建文件', onclick: doNewFile, title: '在当前目录新建空白文件' });
    const btnNewFolder = el('button', { class: 'btn btn-sm', text: '📁 新建文件夹', onclick: doNewFolder, title: '在当前目录新建文件夹' });
    const actionGroup = el('div', { style: 'display:flex; gap:8px; align-items:center; flex:0 0 auto; flex-wrap:nowrap;' }, [
      btnNewFile, btnNewFolder, btnDownload, btnUploadPick, btnCancel
    ]);
    fileCard.appendChild(el('div', { class: 'file-toolbar', style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap; padding:8px 0; border-bottom:1px dashed var(--line);' }, [
      btnSelAll, btnSelNone, selCount,
      el('span', { style: 'flex:0 0 auto;', text: ' | ' }),
      dlZipLabel,
      el('span', { style: 'flex:1 1 auto;' }),
      actionGroup
    ]));
    fileCard.appendChild(tableWrap);

    // 上传区域（表格下方）：默认隐藏，有上传任务时显示
    // 顶部是上传选项栏：覆盖策略 / 完成后刷新 / 清空 / 全取消
    const uploadSection = el('div', { id: 'upload-section', style: 'display:none; margin-top:12px; padding-top:10px; border-top:1px dashed var(--line);' });
    const uploadOptsRow = el('div', {
      class: 'upload-opts-row',
      style: 'display:flex; gap:12px; align-items:center; flex-wrap:wrap; margin-bottom:8px;'
    }, [
      el('span', { class: 'text-dim', style: 'font-weight:600;', text: '📤 上传队列' }),
      el('span', { style: 'flex:1 1 auto;' }),
      el('label', { class: 'inline', style: 'display:inline-flex; align-items:center; gap:4px; white-space:nowrap;' }, [
        el('span', { class: 'lbl', text: '同名文件：' }), uploadOverwriteSel
      ]),
      el('label', { class: 'inline', style: 'display:inline-flex; align-items:center; gap:4px; white-space:nowrap; cursor:pointer;' }, [
        uploadRefreshChk, document.createTextNode('完成后刷新')
      ]),
      btnUploadClear,
      btnUploadCancelAll
    ]);
    uploadSection.appendChild(uploadOptsRow);

    // 上传队列 + 汇总（行为函数 renderUploadList 等依赖 uploadListWrap / uploadSummary 变量名）
    const uploadListWrap = el('div', { class: 'upload-list-wrap', style: 'max-height: 260px; overflow-y: auto;' });
    const uploadSummary = el('div', { class: 'upload-summary text-dim', style: 'margin-top: 6px; font-size: 12px;' });
    uploadSection.appendChild(uploadListWrap);
    uploadSection.appendChild(uploadSummary);
    fileCard.appendChild(uploadSection);

    // 隐藏的 file input 挂到 fileCard 内（跟着 fileCard 一起渲染/卸载）
    fileCard.appendChild(uploadFileInp);

    // 拖拽上传：fileCard / tableWrap 接受 drop，显示上传区域
    const showUploadSection = () => { uploadSection.style.display = ''; };
    fileCard.addEventListener('dragover', (e) => {
      if (e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files')) {
        e.preventDefault();
        e.stopPropagation();
        fileCard.classList.add('dragover');
        showUploadSection();
      }
    });
    fileCard.addEventListener('dragleave', (e) => {
      if (e.target === fileCard) fileCard.classList.remove('dragover');
    });
    fileCard.addEventListener('drop', (e) => {
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) {
        e.preventDefault();
        e.stopPropagation();
        fileCard.classList.remove('dragover');
        showUploadSection();
        addUploadFiles(e.dataTransfer.files);
      }
    });

    // 拖拽上传支持：文件列表区域接受 drop（保持兼容 — 用户拖到列表区也能上传）
    tableWrap.addEventListener('dragover', (e) => {
      if (e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files')) {
        e.preventDefault();
        e.stopPropagation();
        tableWrap.classList.add('dragover');
        showUploadSection();
      }
    });
    tableWrap.addEventListener('dragleave', (e) => {
      if (e.target === tableWrap) tableWrap.classList.remove('dragover');
    });
    tableWrap.addEventListener('drop', (e) => {
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) {
        e.preventDefault();
        e.stopPropagation();
        tableWrap.classList.remove('dragover');
        showUploadSection();
        addUploadFiles(e.dataTransfer.files);
      }
    });

    // 文件选择 input 变化 → 加入队列（点「上传文件」按钮触发）
    uploadFileInp.addEventListener('change', () => {
      if (uploadFileInp.files && uploadFileInp.files.length) {
        showUploadSection();
        addUploadFiles(uploadFileInp.files);
        uploadFileInp.value = ''; // 清空，允许重复选同一文件
      }
    });

    // 修复 warnBox 必须挂到 DOM，否则 renderFileBrowserWarning 里
    // $('#files-warn-title') 返回 null → textContent 抛异常 → 显示"配置加载失败"。
    // warnBox 初始 display:none，挂上去不会立即显示，只有 renderFileBrowserWarning
    // 里设置 warnBox.style.display='' 才显示。
    view.appendChild(warnBox);
    view.appendChild(connCard);
    view.appendChild(pathCard);
    view.appendChild(fileCard);
    // v1.3：uploadCard 已合并到 fileCard 内部（上传按钮挪回下载 toolbar 右侧），不再单独 append

    // =================== 行为 ===================

    function findSrv() {
      if (!state.cfg || !state.currentSys || !state.currentSrv) return null;
      const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
      if (!sys || !sys.servers) return null;
      return sys.servers.find(s => s.name === state.currentSrv) || null;
    }
    function getSrvUsername() {
      const srv = findSrv();
      return (srv && srv.username) || '';
    }
    function getSrvPassword() {
      const srv = findSrv();
      return (srv && srv.password) || '';
    }

    function loadCfg() {
      api('GET', '/api/admin/openers').then(r => {
        Kairo.state.downloadsOpeners = Array.isArray(r && r.openers) ? r.openers : [];
      }).catch(() => {
        // 网络抖动时保留旧数据：避免编辑按钮突然全禁用的连带故障。
        // 读取处已有 `(window.Kairo && Kairo.state && Kairo.state.downloadsOpeners) || []` 兜底。
      });

      return api('GET', '/api/config').then(info => {
        state.cfg = info;
        sysSel.innerHTML = '';
        sysSel.appendChild(el('option', { value: '', text: '（请选择）' }));
        (info.systems || []).forEach(sys => {
          sysSel.appendChild(el('option', { value: sys.name, text: sys.name + (sys.description ? ' · ' + sys.description : '') }));
        });
        // 恢复上次选择
        const lastSel = Kairo.core.lastGet('files', 'sel');
        if (lastSel && lastSel.system && (info.systems || []).find(s => s.name === lastSel.system)) {
          sysSel.value = lastSel.system;
          setSystem(lastSel.system);
          if (lastSel.server && (state.cfg.systems.find(s => s.name === lastSel.system) || {}).servers
              && (state.cfg.systems.find(s => s.name === lastSel.system).servers || []).find(s => s.name === lastSel.server)) {
            srvSel.value = lastSel.server;
            setServer(lastSel.server);
          }
          rememberChk.checked = true;
        }
        // 恢复 filter lastGet
        const lastFilter = Kairo.core.lastGet('files', 'filter');
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
      // 隐藏文件浏览器警告卡片
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
      // P0-BUG-2 修复：srv.password 永远是 ''（/api/config 不返回）。
      // 优先用用户在输入框里填的值，没填才回落到 srv 默认。
      const srvUser = getSrvUsername();
      const srvPass = getSrvPassword();
      return {
        username: (userInput && userInput.value && userInput.value.trim()) || srvUser,
        password: (passInput && passInput.value) || srvPass,
        remember: rememberChk.checked
      };
    }

    function persistSelection() {
      Kairo.core.lastSet('files', 'sel', {
        system: state.currentSys,
        server: state.currentSrv
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
      prependLogDirsToCommonDirs(srv);
      refreshCredStatus();
      persistSelection();
      renderCommonDirsBar();
    }

    sysSel.addEventListener('change', () => setSystem(sysSel.value));
    srvSel.addEventListener('change', () => setServer(srvSel.value));

    btnConnect.addEventListener('click', async () => {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器', 'warn'); return;
      }
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名', 'warn'); return; }
      if (c.remember && c.password) {
        try {
          await api('POST', '/api/credentials/save', {
            system: state.currentSys, server: state.currentSrv, username: c.username, password: c.password
          });
        } catch (e) { /* ignore */ }
      }
      const startPath = pickDefaultPath();
      await doListDir(startPath, c);
    });

    function pickDefaultPath() {
      try {
        const sys = (state.cfg && state.cfg.systems || []).find(s => s.name === state.currentSys);
        const srv = sys && (sys.servers || []).find(s => s.name === state.currentSrv);
        const ld = srv && (srv.log_dirs || [])[0];
        if (ld && ld.path) return ld.path;
      } catch (e) { /* ignore */ }
      const username = getSrvUsername();
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
        // P1-BUG-10 修复：后端 handlers_files.go 已经返回场景化错误信息（如
        // "SSH 账号没有该目录访问权限" / "该路径未在白名单中"），前端不要再 wrap
        // 一层"列出目录失败："——避免 "列出目录失败：列出目录失败: permission denied" 这种重复。
        toast(e.message || '列出目录失败', 'err');
      }
    }


    // ---- 文件预览（v0.5 项 1）----
    // 普通点击文件名 → 弹窗显示（modal 快速看）
    // Shift+点击 / 或显式调用 → 新窗口预览（独立页 preview.html）
    async function openPreview(filePath, fileName) {
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名', 'warn'); return; }
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

    // editFile 编辑远程文件：下载到临时目录 → 用外部编辑器打开 → 监控保存 → 自动上传
    async function editFile(filePath, fileName, openerName) {
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名', 'warn'); return; }
      try {
        const r = await api('POST', '/api/ssh/sftp/edit', {
          system: state.currentSys,
          server: state.currentSrv,
          username: c.username,
          password: c.password,
          path: filePath,
          opener: openerName
        });
        toast('已用 ' + openerName + ' 打开文件，保存后自动上传', 'success');
        if (r && r.id) {
          subscribeEditEvents(r.id, fileName);
        }
      } catch (e) {
        if (e.message && e.message.includes('未找到打开器')) {
          toast('未找到打开器 "' + openerName + '"，请先在系统配置中添加', 'warn');
        } else if (e.message && e.message.includes('文件超过大小限制')) {
          toast('文件超过 200MB 限制，无法编辑', 'warn');
        } else {
          toast('编辑失败: ' + e.message, 'error');
        }
      }
    }

    function subscribeEditEvents(taskId, fileName) {
      const SftpCommon = (window.Kairo && window.Kairo.SftpCommon) || {};
      const base = SftpCommon.buildSseBaseUrl ? SftpCommon.buildSseBaseUrl() : '';
      const url = base + '/api/ssh/sftp/edit/' + encodeURIComponent(taskId) + '/events';
      let es;
      try {
        es = new EventSource(url);
      } catch (e) {
        return;
      }
      es.addEventListener('upload_start', function () {
        toast('正在上传 ' + fileName + ' …', 'idle');
      });
      es.addEventListener('upload_ok', function (e) {
        let sizeText = '';
        try {
          const data = JSON.parse(e.data);
          if (data.bytes) {
            const fmt = SftpCommon.formatBytes || function (n) { return n + ' B'; };
            sizeText = '（' + fmt(data.bytes) + '）';
          }
        } catch (_) {}
        toast(fileName + ' 已上传' + sizeText, 'success');
      });
      es.addEventListener('upload_fail', function (e) {
        let msg = '';
        try { const data = JSON.parse(e.data); msg = data.message || ''; } catch (_) {}
        toast(fileName + ' 上传失败: ' + (msg || '未知错误'), 'error');
      });
      es.addEventListener('editor_closed', function () {
        toast('编辑器进程已退出，如仍在编辑，保存后仍会自动上传', 'idle');
      });
      es.addEventListener('done', function () {
        es.close();
      });
      es.onerror = function () {
        es.close();
      };
    }

    // openPreviewInNewWindow 开新窗口（preview.html），凭证走 Kairo._previewCred 跨窗口传递
    function openPreviewInNewWindow(filePath, fileName) {
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名或在上方填写', 'warn'); return; }
      let encoding = 'utf-8';
      try {
        const sys = (state.cfg.systems || []).find(s => s.name === state.currentSys);
        const srv = sys && (sys.servers || []).find(s => s.name === state.currentSrv);
        const ld = srv && (srv.log_dirs || [])[0];
        if (ld && ld.encoding) encoding = String(ld.encoding).toLowerCase();
      } catch (e) { /* keep utf-8 */ }
      // P0-BUG-3 修复：传给 preview.html 的 password 必须是"实际能用的密码"。
      // 优先级：用户在输入框里填的 > srv.password（通常空）> 空（让 preview.html 后端 keyring 兜底）。
      Kairo._previewCred = Kairo._previewCred || {};
      Kairo._previewCred[state.currentSys + '::' + state.currentSrv] = {
        username: c.username,
        password: (passInput && passInput.value) || c.password || '',
        has_keyring: !!rememberChk.checked
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
      // UI-2 修复：原文字"在新窗口打开"在 modal 上下文里有歧义（modal 本身就是窗口），
      // 改成"独立窗口打开"以明确这是浏览器新标签/新窗口。
      const btnOpenWin = el('button', { class: 'btn btn-sm', text: '↗ 独立窗口打开', onclick: () => {
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
      const rootLink = el('a', { href: '#', style: 'text-decoration:none;', onclick: (e) => {
        e.preventDefault(); doListDir('/', creds());
      }});
      rootLink.appendChild(el('span', { style: 'display:inline-flex; align-items:center; gap:4px;', unsafeHtml: svgIcon('smFolder', 14) + ' /' }));
      crumbsEl.appendChild(rootLink);
      let acc = '';
      parts.forEach((seg, i) => {
        crumbsEl.appendChild(el('span', { class: 'text-dim', style: 'margin: 0 4px;', text: '›' }));
        acc += '/' + seg;
        const target = acc;
        const isLast = i === parts.length - 1;
        if (isLast) {
          crumbsEl.appendChild(el('span', { style: 'font-weight:600;', text: seg }));
        } else {
          crumbsEl.appendChild(el('a', { href: '#', style: 'text-decoration:none;', text: seg, onclick: (e) => {
            e.preventDefault(); doListDir(target, creds());
          }}));
        }
      });
      pathInp.value = state.currentPath;
    }

    // ========== v0.5 项 2：常用目录（localStorage，按 system/server 分组） ==========
    // 数据结构：
    //   state.commonDirs = {
    //     "systemA/serverA": [ { name: "server1 日志", path: "/opt/IBM/.../server1" }, ... ],
    //     ...
    //   }
    // 持久化键：localStorage["kairo:files:common_dirs"] = JSON
    // v0.9 rebrand: 从 ops.files.common_dirs 和 dtb:files:common_dirs 迁移到 kairo:files:common_dirs（一次性，不删旧键）
    // 切换 system/server 时重新渲染 commonDirsBar
    const COMMON_DIRS_LS_KEY = 'kairo:files:common_dirs';
    const COMMON_DIRS_LS_KEY_OLD = 'ops.files.common_dirs';
    const COMMON_DIRS_LS_KEY_DTB = 'dtb:files:common_dirs';

    function loadCommonDirs() {
      try {
        // 一次性迁移：新键不存在但旧键存在时，复制旧值到新键（优先 dtb:，再 ops.）
        if (!localStorage.getItem(COMMON_DIRS_LS_KEY)) {
          const src = localStorage.getItem(COMMON_DIRS_LS_KEY_DTB) || localStorage.getItem(COMMON_DIRS_LS_KEY_OLD);
          if (src) localStorage.setItem(COMMON_DIRS_LS_KEY, src);
        }
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
          style: 'display:inline-flex; align-items:center; gap:4px;',
          unsafeHtml: svgIcon('smFolder', 14) + ' ' + item.name,
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
        el('th', { class: 'col-mode' }, [document.createTextNode('权限')]),
        el('th', { class: 'col-status' }, [document.createTextNode('状态')]),
        el('th', { class: 'col-actions' }, [document.createTextNode('操作')])
      ]));
      tbl.appendChild(thead);

      const tbody = el('tbody');
      sortedEntries().forEach(entry => {
        const fullPath = (state.currentPath === '/' ? '' : state.currentPath) + '/' + entry.name;
        const tr = el('tr', { 'data-path': fullPath, 'data-name': entry.name, 'data-isdir': entry.isDir ? '1' : '0' });
        tr.addEventListener('contextmenu', (e) => {
          e.preventDefault();
          showContextMenu(e, entry, fullPath);
        });
        const cb = el('input', { type: 'checkbox' });
        cb.checked = state.selected.has(entry.name);
        // v1.4：目录允许勾选（后端递归下载 + 强制 zip，见 doDownload zip 计算）
        cb.disabled = false;
        cb.addEventListener('change', () => {
          if (cb.checked) state.selected.add(entry.name);
          else state.selected.delete(entry.name);
          updateSelCount();
        });
        tr.appendChild(el('td', { class: 'col-check' }, [cb]));

        const iconName = entry.isDir ? 'smFolder' : 'smFile';
        const nameCell = el('td', { class: 'name-cell' });
        const inner = el('div', { class: 'name-cell-inner' });
        const iconSpan = el('span', { class: 'name-icon', style: 'width:18px; height:18px; display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon(iconName, 18) });
        inner.appendChild(iconSpan);
        if (entry.isDir) {
          inner.appendChild(el('a', { href: '#', text: entry.name, onclick: (e) => {
            e.preventDefault(); doListDir(fullPath, creds());
          }}));
        } else {
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
          inner.appendChild(link);
        }
        nameCell.appendChild(inner);
        tr.appendChild(nameCell);

        tr.appendChild(el('td', { class: 'num' }, [document.createTextNode(entry.isDir ? '—' : formatBytes(entry.size))]));
        tr.appendChild(el('td', null, [document.createTextNode(entry.mtime ? formatTime(entry.mtime) : '-')]));
        // 权限列：直接显示后端给的 ls -l 风格 10 字符串（"drwxr-xr-x"），
        // 与 SSH 终端 / macOS Finder 一致，不需要再简化成 octal 数字。
        // 文件类型图标已经在「名称」列里有，这里不再重复。
        tr.appendChild(el('td', { class: 'mode-cell', text: entry.mode || '-' }));
        tr.appendChild(el('td', { class: 'col-status status-cell', 'data-name': entry.name }, [document.createTextNode('')]));

        const actionsCell = el('td', { class: 'col-actions' });
        if (!entry.isDir) {
          const isText = (window.Kairo && window.Kairo.SftpCommon && Kairo.SftpCommon.isTextFileByExt) ? Kairo.SftpCommon.isTextFileByExt(entry.name) : true;
          if (isText) {
            const previewBtn = el('button', {
              class: 'btn btn-sm',
              text: '预览',
              title: '预览文件内容',
              onclick: (e) => {
                e.stopPropagation();
                openPreview(fullPath, entry.name);
              }
            });
            actionsCell.appendChild(previewBtn);

            const openers = (window.Kairo && Kairo.state && Kairo.state.downloadsOpeners) || [];
            if (!openers || openers.length === 0) {
              const editBtn = el('button', {
                class: 'btn btn-sm btn-disabled',
                text: '编辑',
                title: '请先在系统配置中添加外部打开器（如 Notepad++、VS Code）',
                disabled: true
              });
              actionsCell.appendChild(editBtn);
            } else {
              openers.forEach(op => {
                // v0.14：opener 图标统一走 Kairo.icons.openerIconHTML（emoji / exe 真实图标 / SVG fallback）。
                const iconHtml = (window.Kairo && Kairo.icons && Kairo.icons.openerIconHTML)
                  ? Kairo.icons.openerIconHTML(op, 14)
                  : ((Kairo.icons && Kairo.icons.innerHTML) ? Kairo.icons.innerHTML('file-text', 14) : '📝');
                const tip = (op.name || '') + (op.path ? ' — ' + op.path : '') + '\n保存后自动上传到服务器';
                const editBtn = el('button', {
                  class: 'btn btn-sm',
                  title: tip,
                  style: 'display:inline-flex; align-items:center; gap:4px;',
                  onclick: (e) => {
                    e.stopPropagation();
                    editFile(fullPath, entry.name, op.name);
                  },
                  // op.name 来自 /api/admin/openers（用户配置），未做服务端长度/字符限制。
                  // 必须 escapeHtml，否则 `<img src=x onerror=alert(1)>` 会执行。
                  unsafeHtml: iconHtml + ' ' + escapeHtml(op.name || '编辑')
                });
                actionsCell.appendChild(editBtn);
              });
            }
          }
        }
        const renameBtn = el('button', {
          class: 'btn btn-sm',
          text: '重命名',
          title: '重命名此项',
          onclick: (e) => {
            e.stopPropagation();
            doRename(entry.name, fullPath, entry.isDir);
          }
        });
        actionsCell.appendChild(renameBtn);

        tr.appendChild(actionsCell);

        tbody.appendChild(tr);
      });
      tbl.appendChild(tbody);
      tableWrap.appendChild(tbl);
      updateSelCount();
      Object.keys(state.fileStates).forEach(bn => {
        const st = state.fileStates[bn];
        if (!st) return;
        setRowStatusByName(bn, st);
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

    // 已删除：旧版把 ls -l 字符串简化成 octal 数字 + 图标的 formatShortMode。
    // 现在权限列直接显示后端给的 10 字符串（如 "drwxr-xr-x"），与 SSH 终端一致。

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
        const span = el('span', { class: 'dl-pct', style: 'color:#10b981; display:inline-flex; align-items:center; gap:4px;', unsafeHtml: svgIcon('smCheck', 14) + ' 完成 · ' + formatBytes(st.bytes || 0) });
        return span;
      }
      if (st.status === 'fail') {
        const span = el('span', { class: 'dl-pct', style: 'color:#ef4444; display:inline-flex; align-items:center; gap:4px;', unsafeHtml: svgIcon('smX', 14) + ' ' + (st.error || '失败') });
        return span;
      }
      return null;
    }

    async function doDownload() {
      if (state.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名', 'warn'); return; }
      const names = Array.from(state.selected);
      if (!names.length) { toast('请先勾选文件', 'warn'); return; }
      const paths = names.map(n => (state.currentPath === '/' ? '' : state.currentPath) + '/' + n);
      const wantZip = dlZipChk.checked;
      // v1.4：选中目录时后端会递归下载并强制打 zip（保留目录结构）
      const selectedHasDir = names.some(n => {
        const e = state.entries.find(en => en.name === n);
        return !!(e && e.isDir);
      });
      const zip = (wantZip && paths.length >= 2) || selectedHasDir;
      if (wantZip && paths.length < 2 && !selectedHasDir) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
      }
      if (selectedHasDir && !wantZip) {
        toast('包含目录，将递归下载并打包 zip', 'idle');
      }
      state.fileStates = {};
      paths.forEach(p => {
        const bn = p.split('/').pop();
        state.fileStates[bn] = { status: 'pending' };
        setRowStatusByName(bn, state.fileStates[bn]);
      });
      btnDownload.disabled = true;
      btnCancel.disabled = false;
      setStatus('busy', '下载中…');
      // 项 2 修复：每个下载任务开始时生成稳定的 notify id（"files-时间戳"），
      // 同任务即便多次重推 done 事件，notify 也会去重（见 Kairo.core.notify 的 id 去重逻辑）。
      // 多任务之间也不会互相覆盖。
      state.lastNotifyId = 'files-' + Date.now() + '-' + Math.random().toString(36).slice(2, 8);
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
        Kairo.core.setActiveDL({ id: r.id, evtsrc: es });
        let gotDone = false;
        // P1-BUG-4 修复：done 一旦见到，**立刻** es.close() + 清 onerror/onmessage，
        // 防止 EventSource 自动重连把 404 刷到 network log。
        const onDoneSeen = (reason) => {
          if (gotDone) return;
          gotDone = true;
          try { es.onerror = null; } catch (e) { /* ignore */ }
          try { es.onmessage = null; } catch (e) { /* ignore */ }
          try { es.close(); } catch (e) { /* ignore */ }
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
          // P1-BUG-4：已 gotDone 后再 fire 是 close 残留，忽略（不再 setTimeout）。
          if (gotDone) {
            try { es.close(); } catch (e) { /* ignore */ }
            return;
          }
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
          const bn = p.split('/').pop();
          state.fileStates[bn] = { status: 'fail', error: e.message };
          setRowStatusByName(bn, state.fileStates[bn]);
        });
        btnDownload.disabled = false;
        btnCancel.disabled = true;
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    function handleDownloadEvent(o) {
      if (o.kind === 'file_start') {
        const bn = (o.file || '').split('/').pop();
        state.fileStates[bn] = { status: 'downloading', written: 0, total: o.total || -1 };
        setRowStatusByName(bn, state.fileStates[bn]);
      } else if (o.kind === 'progress') {
        const bn = (o.file || '').split('/').pop();
        const st = state.fileStates[bn] || {};
        st.status = 'downloading'; st.written = o.written; st.total = o.total;
        state.fileStates[bn] = st;
        setRowStatusByName(bn, st);
      } else if (o.kind === 'file_done') {
        const bn = (o.file || '').split('/').pop();
        state.fileStates[bn] = { status: 'done', bytes: o.bytes };
        setRowStatusByName(bn, state.fileStates[bn]);
      } else if (o.kind === 'done') {
        btnDownload.disabled = state.selected.size === 0;
        btnCancel.disabled = true;
        state.dlId = null;
        if (o.ok) {
          (o.downloads || []).forEach(d => {
            if (d) {
              const baseName = d.name || (d.local ? d.local.split(/[\\/]/).pop() : '');
              if (baseName) {
                const remotePath = (state.currentPath === '/' ? '' : state.currentPath) + '/' + baseName;
                state.downloadedFiles[remotePath] = d.abs_path || (d.date ? d.date + '/' + d.local : d.local);
              }
            }
          });
          // v1.4：目录行（递归下载）不会有 file_start/file_done 事件落到行上，
          // done 时把仍 pending 的行标为完成，避免"等待…"卡死。
          Object.keys(state.fileStates).forEach(bn => {
            const st = state.fileStates[bn];
            if (st && st.status === 'pending') {
              st.status = 'done';
              st.bytes = 0;
              setRowStatusByName(bn, st);
            }
          });
          showDownloadDoneNotify(o);
        } else {
          toast('下载失败：' + (o.error || '未知错误'), 'err');
          Object.keys(state.fileStates).forEach(bn => {
            const st = state.fileStates[bn];
            if (st.status === 'pending' || st.status === 'downloading') {
              st.status = 'fail'; st.error = o.error || '';
              setRowStatusByName(bn, st);
            }
          });
        }
        setStatus('idle');
        state.dlEvtSrc = null;
      }
    }

    // showDownloadDoneNotify 项 16：下载完成 → 右上角通知（标题 + 路径 + 操作按钮）
    // 项 2 修复：notify id 改成"任务级别"（state.lastNotifyId，启动时生成一次）。
    // 原版用 state.dlId，但 dlId 在 done 事件里被立刻置 null，导致 id 退化成
    // Date.now() 随机值，同一 task 的多次 done 回调（旧连接残留 / 重连后
    // MarkFinished 重新推 done）会堆出多条通知。
    function showDownloadDoneNotify(o) {
      const downloads = o.downloads || [];
      const folder = o.folder || '';
      const firstAbsPath = downloads[0] && downloads[0].abs_path || '';
      const fileList = downloads.slice(0, 3).map(d => d.local || d.name || '').filter(Boolean).join(', ');
      const more = downloads.length > 3 ? (' 等 ' + downloads.length + ' 个') : '';
      const title = '✓ 下载完成 · ' + downloads.length + ' 个文件';
      const body = folder + (fileList ? ('\n' + fileList + more) : '');
      const actions = [];
      if (firstAbsPath) {
        actions.push({
          html: '<span style="display:inline-flex;align-items:center;gap:4px;">' + svgIcon('smFolder', 14) + ' 打开所在目录</span>',
          callback: async () => {
            try {
              await api('POST', '/api/local/reveal-file', { path: firstAbsPath });
            } catch (e) {
              toast('打开目录失败：' + e.message, 'err');
            }
          }
        });
      }
      if (folder) {
        actions.push({
          html: '<span style="display:inline-flex;align-items:center;gap:4px;">' + svgIcon('smClipboard', 14) + ' 复制路径</span>',
          callback: () => {
            Kairo.core.copyToClipboard(folder).then(() => {
              toast('路径已复制', 'ok');
            }).catch(() => {
              window.prompt('复制此路径：', folder);
            });
          }
        });
      }
      actions.push({
        html: '<span style="display:inline-flex;align-items:center;gap:4px;">' + svgIcon('smScroll', 14) + ' 查看下载历史</span>',
        callback: () => {
          if (location.hash !== '#/downloads') location.hash = '#/downloads';
        }
      });
      Kairo.core.notify({
        id: state.lastNotifyId || ('download-' + Date.now()),
        type: 'ok',
        title: title,
        body: body,
        actions: actions,
        duration: 8000
      });
      if (location.hash !== '#/downloads') {
        Kairo.core.bumpDlBadge(downloads.length);
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

    // =================== 上传（v1.1 新增）===================

    // addUploadFiles 把 FileList 加入上传队列，并启动串行传输（如未启动）。
    // 不去重：用户重复选同名文件是有意为之（覆盖策略由后端处理）。
    function addUploadFiles(fileList) {
      if (!state.currentSys || !state.currentSrv) {
        toast('请先选系统和服务器并连接', 'warn'); return;
      }
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名', 'warn'); return; }
      const targetDir = state.currentPath || '/';
      if (!targetDir || targetDir[0] !== '/') {
        toast('目标目录必须是绝对路径', 'warn'); return;
      }
      for (let i = 0; i < fileList.length; i++) {
        const f = fileList[i];
        // size 校验：浏览器已知 size，提前报错避免上传到一半才失败
        // maxSize 从后端 init 响应拿不到（init 时才返回），这里只做粗校验
        state.uploadQueue.push({
          name: f.name,
          size: f.size,
          status: 'pending',
          progress: 0,
          bytes: 0,
          error: '',
          uploadId: '',
          finalName: f.name,
          targetDir: targetDir,
          overwrite: uploadOverwriteSel.value,
          file: f, // 保留 File 引用
          xhr: null,
          creds: { username: c.username, password: c.password }
        });
      }
      renderUploadList();
      // 启动队列（如未启动）
      if (!state.uploadInflight) {
        startNextUpload();
      }
    }

    // startNextUpload 从队列取下一个 pending 任务发起上传。
    // 串行：一次只跑一个，完成后自动取下一个。
    function startNextUpload() {
      if (state.uploadInflight) return;
      const next = state.uploadQueue.find(t => t.status === 'pending');
      if (!next) {
        // 队列空，更新汇总
        renderUploadSummary();
        // 全部完成后按需刷新目录
        if (uploadRefreshChk.checked && state.currentPath) {
          const c = creds();
          if (c.username) doListDir(state.currentPath, c);
        }
        return;
      }
      state.uploadInflight = true;
      next.status = 'uploading';
      next.progress = 0;
      renderUploadList();
      doUploadOne(next).finally(() => {
        state.uploadInflight = false;
        // 继续下一个（不管成功失败）
        startNextUpload();
      });
    }

    // doUploadOne 单个文件上传：init → data（流式）。
    // 返回 Promise，resolve 时表示这个文件处理结束（成功/失败/取消）。
    async function doUploadOne(task) {
      try {
        // 1. init
        const initResp = await api('POST', '/api/ssh/sftp/upload/init', {
          system: state.currentSys,
          server: state.currentSrv,
          username: task.creds.username,
          password: task.creds.password,
          target_dir: task.targetDir,
          filename: task.name,
          size: task.size,
          overwrite: task.overwrite
        });
        task.uploadId = initResp.id;
        task.maxSize = initResp.max_size;
        // init 阶段如果 size 超限，后端会返回 400，进入 catch
        // 这里再次校验本地 size 与后端 max_size
        if (initResp.max_size && task.size > initResp.max_size) {
          throw new Error('文件大小 ' + formatBytes(task.size) + ' 超过服务端上限 ' + formatBytes(initResp.max_size));
        }
        renderUploadList();
        // 2. data（流式 POST，raw body）
        await uploadFileData(task);
        // 成功（uploadFileData 内部已更新 task.status）
      } catch (e) {
        // init / data 阶段的错误
        task.status = 'fail';
        task.error = e.message || String(e);
        if (task.xhr) {
          // XHR 已发出但被中断/报错
          try { task.xhr.abort(); } catch (e2) { /* ignore */ }
          task.xhr = null;
        }
        // init 阶段就 fail 了的话，task.file 还没传给 xhr，需要在这里也释放
        if (task.file) { try { task.file = null; } catch (e2) { /* ignore */ } }
        renderUploadList();
        toast('上传失败：' + task.name + ' — ' + task.error, 'err');
      } finally {
        updateUploadButtons();
      }
    }

    // uploadFileData 用 XHR 把文件作为 raw body 发到 /data 端点。
    // 用 XHR.upload.onprogress 算进度（Win 7 Chrome 109 兼容，不用 fetch stream）。
    function uploadFileData(task) {
      return new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        task.xhr = xhr;
        const url = '/api/ssh/sftp/upload/' + encodeURIComponent(task.uploadId) + '/data';
        xhr.open('POST', url, true);
        // 不设 Content-Type：让浏览器自动用文件原始类型 / 或省略
        // 服务端按 raw body 读，不看 Content-Type
        // 必须 set Content-Length？XHR 浏览器自动从 file.size 设置（通过 send(file)）
        xhr.upload.onprogress = (ev) => {
          if (ev.lengthComputable) {
            task.bytes = ev.loaded;
            task.progress = Math.min(100, Math.round((ev.loaded / ev.total) * 100));
            renderUploadRow(task);
          }
        };
        // 终态后清理 task.file 引用，避免大文件残留内存。
        // - done：上传成功，文件已写到服务端，本地引用可释放
        // - fail/cancel：同样不再需要本地文件了
        // File 对象本身不可清空（spec 限制），但把 task.file = null 让 GC 释放 task 对它的引用链。
        const releaseFile = () => {
          if (task.file) {
            try { task.file = null; } catch (e) { /* ignore */ }
          }
        };
        xhr.onload = () => {
          task.xhr = null;
          let resp = null;
          try { resp = JSON.parse(xhr.responseText || '{}'); } catch (e) { /* ignore */ }
          if (xhr.status >= 200 && xhr.status < 300 && resp && resp.ok) {
            task.status = 'done';
            task.bytes = resp.bytes || task.size;
            task.progress = 100;
            task.finalName = resp.final_name || task.name;
            renderUploadRow(task);
            renderUploadSummary();
            releaseFile();
            resolve();
          } else {
            // 后端返回错误
            const errMsg = (resp && (resp.error || resp.message)) || ('HTTP ' + xhr.status);
            task.status = 'fail';
            task.error = errMsg;
            renderUploadRow(task);
            releaseFile();
            reject(new Error(errMsg));
          }
        };
        xhr.onerror = () => {
          task.xhr = null;
          task.status = 'fail';
          task.error = '网络错误';
          renderUploadRow(task);
          releaseFile();
          reject(new Error('网络错误'));
        };
        xhr.onabort = () => {
          task.xhr = null;
          task.status = 'cancel';
          task.error = '已取消';
          renderUploadRow(task);
          releaseFile();
          // 取消不算失败，resolve 让队列继续
          resolve();
        };
        // send(File) 浏览器会自动设 Content-Length = file.size + Content-Type = file.type
        try {
          xhr.send(task.file);
        } catch (e) {
          task.xhr = null;
          task.status = 'fail';
          task.error = '发送失败：' + e.message;
          renderUploadRow(task);
          releaseFile();
          reject(e);
        }
      });
    }

    // cancelOneUpload 取消单个上传任务。
    //   - pending：直接标 cancel
    //   - uploading：调后端 cancel + abort XHR
    //   - done/fail/cancel：忽略
    async function cancelOneUpload(task) {
      if (task.status === 'done' || task.status === 'fail' || task.status === 'cancel') return;
      if (task.status === 'pending') {
        task.status = 'cancel';
        task.error = '已取消';
        // 释放 File 引用（pending 没真上传，但 task.file 也保留着）
        if (task.file) { try { task.file = null; } catch (e) { /* ignore */ } }
        renderUploadRow(task);
        renderUploadSummary();
        return;
      }
      // uploading
      if (task.uploadId) {
        try { await api('POST', '/api/ssh/sftp/upload/cancel', { id: task.uploadId }); }
        catch (e) { /* ignore */ }
      }
      if (task.xhr) {
        try { task.xhr.abort(); } catch (e) { /* onabort 会处理 */ }
      }
      // 注：task.file 释放走 xhr.onabort → releaseFile
    }

    // cancelAllUploads 取消所有 pending/uploading 任务
    async function cancelAllUploads() {
      const tasks = state.uploadQueue.filter(t => t.status === 'pending' || t.status === 'uploading');
      if (!tasks.length) return;
      // 先标 pending 为 cancel
      tasks.forEach(t => {
        if (t.status === 'pending') {
          t.status = 'cancel';
          t.error = '已取消';
        }
      });
      renderUploadList();
      // 取消 uploading 的那个（如果有）
      const uploading = tasks.find(t => t.status === 'uploading');
      if (uploading) {
        await cancelOneUpload(uploading);
      }
      renderUploadSummary();
      toast('已取消全部待上传任务', 'warn');
    }

    // retryUpload 失败后重试：重置状态为 pending，启动队列
    function retryUpload(task) {
      if (task.status !== 'fail' && task.status !== 'cancel') return;
      task.status = 'pending';
      task.progress = 0;
      task.bytes = 0;
      task.error = '';
      task.uploadId = '';
      renderUploadRow(task);
      if (!state.uploadInflight) startNextUpload();
    }

    // removeUploadRow 从队列移除一个任务（仅允许 done/fail/cancel）
    function removeUploadRow(task) {
      if (task.status === 'uploading' || task.status === 'pending') return;
      const idx = state.uploadQueue.indexOf(task);
      if (idx >= 0) state.uploadQueue.splice(idx, 1);
      renderUploadList();
    }

    // clearFinishedUploads 清空所有 done/fail/cancel 任务
    function clearFinishedUploads() {
      const before = state.uploadQueue.length;
      state.uploadQueue = state.uploadQueue.filter(t => t.status === 'pending' || t.status === 'uploading');
      if (state.uploadQueue.length !== before) renderUploadList();
    }

    // updateUploadButtons 根据队列状态更新按钮可用性
    function updateUploadButtons() {
      const hasActive = state.uploadQueue.some(t => t.status === 'pending' || t.status === 'uploading');
      btnUploadCancelAll.disabled = !hasActive;
    }

    // renderUploadList 重建整个上传列表 DOM
    function renderUploadList() {
      while (uploadListWrap.firstChild) uploadListWrap.removeChild(uploadListWrap.firstChild);
      state.uploadQueue.forEach(t => uploadListWrap.appendChild(buildUploadRow(t)));
      renderUploadSummary();
      updateUploadButtons();
    }

    // renderUploadRow 只更新单行（避免整列重建丢失滚动位置）
    function renderUploadRow(task) {
      const idx = state.uploadQueue.indexOf(task);
      if (idx < 0) return;
      const oldRow = uploadListWrap.children[idx];
      if (!oldRow) { renderUploadList(); return; }
      const newRow = buildUploadRow(task);
      uploadListWrap.replaceChild(newRow, oldRow);
      updateUploadButtons();
    }

    // buildUploadRow 构造单行 DOM
    function buildUploadRow(task) {
      const row = el('div', { class: 'upload-row', style: 'display:flex; align-items:center; gap:8px; padding:6px 4px; border-bottom:1px dashed var(--line);' });
      // 文件名
      const nameCell = el('div', { style: 'flex: 1 1 40%; min-width: 120px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;', title: task.name + (task.finalName && task.finalName !== task.name ? ' → ' + task.finalName : '') });
      nameCell.textContent = task.name;
      if (task.finalName && task.finalName !== task.name && task.status === 'done') {
        nameCell.appendChild(document.createTextNode('  → ' + task.finalName));
      }
      row.appendChild(nameCell);
      // 进度
      const progCell = el('div', { style: 'flex: 1 1 auto; min-width: 100px; display:flex; align-items:center; gap:6px;' });
      if (task.status === 'pending') {
        progCell.appendChild(el('span', { class: 'dl-pct', text: '等待…' }));
      } else if (task.status === 'uploading') {
        const bar = el('div', { class: 'dl-bar', style: 'width: 140px;' });
        const fill = el('div', { class: 'dl-bar-fill', style: 'width:' + task.progress + '%' });
        bar.appendChild(fill);
        progCell.appendChild(bar);
        progCell.appendChild(el('span', { class: 'dl-pct', text: task.progress + '%' }));
      } else if (task.status === 'done') {
        const span = el('span', { class: 'dl-pct', style: 'color:#10b981; display:inline-flex; align-items:center; gap:4px;', unsafeHtml: svgIcon('smCheck', 14) + ' 完成 · ' + formatBytes(task.bytes) });
        progCell.appendChild(span);
      } else if (task.status === 'fail') {
        const span = el('span', { class: 'dl-pct', style: 'color:#ef4444; display:inline-flex; align-items:center; gap:4px;', unsafeHtml: svgIcon('smX', 14) + ' ' + (task.error || '失败') });
        progCell.appendChild(span);
      } else if (task.status === 'cancel') {
        progCell.appendChild(el('span', { class: 'dl-pct', style: 'color:var(--text-dim);', text: '已取消' }));
      }
      row.appendChild(progCell);
      // 操作按钮
      const btnCell = el('div', { style: 'flex: 0 0 auto; display:flex; gap:4px;' });
      if (task.status === 'uploading') {
        btnCell.appendChild(el('button', { class: 'btn btn-sm btn-danger', text: '取消', onclick: () => cancelOneUpload(task) }));
      } else if (task.status === 'fail' || task.status === 'cancel') {
        btnCell.appendChild(el('button', { class: 'btn btn-sm', text: '重试', onclick: () => retryUpload(task) }));
        btnCell.appendChild(el('button', { class: 'btn btn-sm', text: '✕', title: '移除', onclick: () => removeUploadRow(task) }));
      } else if (task.status === 'done') {
        btnCell.appendChild(el('button', { class: 'btn btn-sm', text: '✕', title: '移除', onclick: () => removeUploadRow(task) }));
      }
      row.appendChild(btnCell);
      return row;
    }

    // renderUploadSummary 更新底部汇总
    function renderUploadSummary() {
      const total = state.uploadQueue.length;
      const done = state.uploadQueue.filter(t => t.status === 'done').length;
      const fail = state.uploadQueue.filter(t => t.status === 'fail').length;
      const cancel = state.uploadQueue.filter(t => t.status === 'cancel').length;
      const bytes = state.uploadQueue.reduce((s, t) => s + (t.status === 'done' ? t.bytes : 0), 0);
      const totalBytes = state.uploadQueue.reduce((s, t) => s + (t.size || 0), 0);
      let text = total === 0 ? '（队列为空）' : ('共 ' + total + ' 个 · 成功 ' + done + ' · 失败 ' + fail + ' · 取消 ' + cancel);
      if (total > 0) text += ' · ' + formatBytes(bytes) + ' / ' + formatBytes(totalBytes);
      uploadSummary.textContent = text;
    }

    // =================== 右键菜单 ===================

    function hideContextMenu() {
      if (state.ctxMenu) {
        state.ctxMenu.remove();
        state.ctxMenu = null;
        state.ctxTarget = null;
      }
    }

    function showContextMenu(e, entry, fullPath) {
      hideContextMenu();

      const menu = el('div', { class: 'file-context-menu' });
      state.ctxMenu = menu;
      state.ctxTarget = { entry, fullPath };

      const isDownloaded = !!state.downloadedFiles[fullPath];

      function addItem(iconName, label, onClick, disabled) {
        const item = el('div', {
          class: 'file-context-menu-item' + (disabled ? ' disabled' : '')
        });
        const iconEl = el('span', { class: 'file-context-menu-icon', style: 'width:16px; height:16px; display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon(iconName, 16) });
        item.appendChild(iconEl);
        item.appendChild(el('span', { class: 'file-context-menu-label', text: label }));
        if (!disabled) {
          item.addEventListener('click', () => {
            hideContextMenu();
            onClick();
          });
        }
        menu.appendChild(item);
        return item;
      }

      function addDivider() {
        menu.appendChild(el('div', { class: 'file-context-menu-divider' }));
      }

      addItem('smClipboard', '复制路径', () => {
        copyToClipboard(fullPath);
      });

      addItem('smClipboard', '重命名', () => {
        doRename(entry.name, fullPath, entry.isDir);
      });

      if (!entry.isDir) {
        addItem('smEye', '预览', () => {
          openPreviewInNewWindow(fullPath, entry.name);
        });
      }

      addDivider();
      addItem('smFile', '新建文件', doNewFile);
      addItem('smFolder', '新建文件夹', doNewFolder);

      addItem('smDownload', '下载', () => {
        doDownloadSingle(fullPath, entry.name);
      });

      const localName = state.downloadedFiles[fullPath];
      addItem('smFolder', '打开所在目录（本地）', () => {
        openLocalDir(localName);
      }, !isDownloaded);

      document.body.appendChild(menu);

      const rect = menu.getBoundingClientRect();
      let x = e.clientX;
      let y = e.clientY;
      if (x + rect.width > window.innerWidth - 10) {
        x = window.innerWidth - rect.width - 10;
      }
      if (y + rect.height > window.innerHeight - 10) {
        y = window.innerHeight - rect.height - 10;
      }
      menu.style.left = x + 'px';
      menu.style.top = y + 'px';
    }

    function copyToClipboard(text) {
      // 优先安全上下文 clipboard，http内网/老核走 execCommand，最后才 prompt 手动复制
      function legacyCopy(t) {
        try {
          const ta = document.createElement('textarea');
          ta.value = t; ta.style.position = 'fixed'; ta.style.opacity = '0';
          document.body.appendChild(ta); ta.select();
          const ok = document.execCommand('copy');
          document.body.removeChild(ta);
          return !!ok;
        } catch (_) { return false; }
      }
      try {
        if (navigator.clipboard && window.isSecureContext && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(() => {
            toast('路径已复制', 'ok');
          }).catch(() => {
            if (legacyCopy(text)) toast('路径已复制', 'ok');
            else window.prompt('复制此路径：', text);
          });
        } else if (legacyCopy(text)) {
          toast('路径已复制', 'ok');
        } else {
          window.prompt('复制此路径：', text);
        }
      } catch (e) {
        window.prompt('复制此路径：', text);
      }
    }

    async function openLocalDir(localPath) {
      if (!localPath) return;
      try {
        if (localPath.startsWith('/') || (localPath.length > 1 && localPath[1] === ':')) {
          await api('POST', '/api/local/reveal-file', { path: localPath });
        } else {
          await api('POST', '/api/downloads/open-dir?name=' + encodeURIComponent(localPath));
        }
      } catch (e) {
        toast('打开目录失败：' + e.message, 'err');
      }
    }

    async function doDownloadSingle(fullPath, fileName) {
      if (state.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const c = creds();
      if (!c.username) { toast('请在系统配置中设置 SSH 用户名', 'warn'); return; }

      state.fileStates = {};
      state.fileStates[fileName] = { status: 'pending' };
      setRowStatusByName(fileName, state.fileStates[fileName]);
      btnDownload.disabled = true;
      btnCancel.disabled = false;
      setStatus('busy', '下载中…');
      state.lastNotifyId = 'files-single-' + Date.now() + '-' + Math.random().toString(36).slice(2, 8);

      try {
        const r = await api('POST', '/api/files/download', {
          system: state.currentSys, server: state.currentSrv,
          username: c.username, password: c.password,
          paths: [fullPath], zip: false,
          target_dir: (dlTargetDirInp.value || '').trim()
        });
        state.dlId = r.id;
        if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); return; }
        const es = new EventSource('/api/files/download/' + r.id + '/events');
        state.dlEvtSrc = es;
        Kairo.core.setActiveDL({ id: r.id, evtsrc: es });
        let gotDone = false;
        // P1-BUG-4：参考上面 doDownload 的修复——done 见到立即卸监听器 + close。
        const onDoneSeen = (reason) => {
          if (gotDone) return;
          gotDone = true;
          try { es.onerror = null; } catch (e) { /* ignore */ }
          try { es.onmessage = null; } catch (e) { /* ignore */ }
          try { es.close(); } catch (e) { /* ignore */ }
          closeDownloadStream(reason);
        };
        es.onmessage = (ev) => {
          let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
          if (o && o.kind === 'done') {
            handleSingleDownloadEvent(o, fullPath, fileName);
            onDoneSeen('done');
            return;
          }
          handleDownloadEvent(o);
        };
        es.addEventListener('done', () => { onDoneSeen('done'); });
        es.onerror = () => {
          // P1-BUG-4：已 gotDone 后再 fire 是 close 残留，忽略。
          if (gotDone) {
            try { es.close(); } catch (e) { /* ignore */ }
            return;
          }
          setTimeout(() => {
            if (state.dlId && state.dlEvtSrc === es && !gotDone) {
              onDoneSeen('error');
              toast('SSE 连接异常（已强制收尾）', 'err');
            }
          }, 2000);
        };
      } catch (e) {
        toast('启动下载失败：' + e.message, 'err');
        state.fileStates[fileName] = { status: 'fail', error: e.message };
        setRowStatusByName(fileName, state.fileStates[fileName]);
        btnDownload.disabled = false;
        btnCancel.disabled = true;
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    function handleSingleDownloadEvent(o, fullPath, fileName) {
      handleDownloadEvent(o);
      if (o.kind === 'done' && o.ok) {
        const downloads = o.downloads || [];
        if (downloads.length > 0) {
          const d = downloads[0];
          state.downloadedFiles[fullPath] = d.abs_path || (d.date ? d.date + '/' + d.local : (d.local || d.name || fileName));
        }
      }
    }

    document.addEventListener('click', (e) => {
      if (state.ctxMenu && !state.ctxMenu.contains(e.target)) {
        hideContextMenu();
      }
    });
    document.addEventListener('scroll', hideContextMenu, true);
    window.addEventListener('resize', hideContextMenu);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') hideContextMenu();
    });

    // 启动：从 localStorage 恢复常用目录 + 渲染常用目录栏
state.commonDirs = loadCommonDirs();
renderCommonDirsBar();
loadCfg().then(refreshCredStatus).catch(e => toast('配置加载失败：' + e.message, 'err'));

    // v1.2 Bug 1 修复：注册上传 controller 到 Kairo.core，
    // navigate 切走 / beforeunload 时 core 会调 cancelAll / cancelAllBeacon 取消进行中的上传。
    // （防止后台 XHR 继续跑 + 回调访问已被卸载的 DOM）
    Kairo.core.setActiveUploads({
      cancelAll: () => {
        // 同步：abort XHR + 通知后端 cancel（异步）
        try { cancelAllUploads(); } catch (e) { /* ignore */ }
      },
      cancelAllBeacon: () => {
        // beforeunload 路径：用 sendBeacon 异步通知后端，不阻塞 unload
        const tasks = (state && state.uploadQueue) || [];
        tasks.forEach(t => {
          if (t.status === 'uploading' && t.uploadId && navigator.sendBeacon) {
            try {
              navigator.sendBeacon('/api/ssh/sftp/upload/cancel',
                new Blob([JSON.stringify({ id: t.uploadId })], { type: 'application/json' }));
            } catch (e) { /* ignore */ }
          }
          if (t.xhr) { try { t.xhr.abort(); } catch (e) { /* ignore */ } }
        });
      }
    });
  }

  Kairo.pages.files = renderFiles;
  Kairo.state.routes.files = renderFiles;
  Kairo.state.routeNames.files = '文件传输';
})();