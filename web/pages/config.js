/* ===== web/pages/config.js =====
 * 系统配置 — 可视化编辑器
 *
 * 数据模型：把 /api/config 返回的 systems 复制到本地 state，
 * 用户在页面里增删改后，点"保存"整体 PUT 回去。
 *
 * 顶部：根据 app.enable_free_file_browser 决定是否显示黄色警告 banner。
 *
 * 【v0.5 修复 #20】state 必须放在模块级，**不能放 renderConfig 闭包里**。
 * 原因：app.js 的 navigate() 在每次 hashchange 都会 view.innerHTML = ''
 * 然后重新调用 routes[name](view)。如果 state 放闭包里，每次切 tab 再回来
 * 都会被覆盖，**未保存的改动全丢**，用户报告"切 tab 内容没了/保存没写盘"。
 * 修复后：state 挂到 Kairo.state.configEditor，跨 re-render 存活；
 * 只有"还没加载过"或"用户点放弃改动"时才重新 fetch。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, $, toast, validate, newSystem, newServer, newLogDir, kvTable, confirmDialog, escapeHtml } = Kairo.core;
  const { api } = Kairo.api;

  const ICONS = {
    smClipboard: 'M9 2h6a2 2 0 012 2v16a2 2 0 01-2 2H9a2 2 0 01-2-2V4a2 2 0 012-2zm0 2v2h6V4zM8 12h8M8 16h8M8 8h4',
    smFolder:    'M2 5a2 2 0 012-2h5l2 2h9a2 2 0 012 2v11a2 2 0 01-2 2H4a2 2 0 01-2-2V5z',
    smSave:      'M19 21H5a2 2 0 01-2-2V5a2 2 0 012-2h11l5 5v11a2 2 0 01-2 2zM17 21v-7H7v7M7 3v4h10V3',
    smWarn:      'M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0zM12 9v4M12 17h.01',
    // 通用文件图标 —— inferIconFromPath 给所有打开器返回的 svg name 都是 smFile，
    // 然后用 color 区分。path d 跟 websphere.js / about.js 保持一致。
    smFile:      'M6 2h6l4 4v14a2 2 0 01-2 2H6a2 2 0 01-2-2V4a2 2 0 012-2zm6 0v4h4',
  };
  function svgIcon(name, size) {
    const d = ICONS[name];
    if (!d) return '';
    const s = size || 16;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

  // FE-004：doExport（blob 下载）/ doImportUpload（text/yaml body）必须用裸 fetch
  // —— api() 只支持 JSON body 且只返回 JSON，无法承载 blob 或 yaml 文本。
  // 这里统一处理 401：触发登录引导，避免 token 鉴权场景下静默失败。
  function handleAuth401(resp) {
    if (resp.status === 401) {
      if (window.Kairo.auth && window.Kairo.auth.requireLogin) window.Kairo.auth.requireLogin();
      return true;
    }
    return false;
  }

  // 模块级 state：跨 tab 切换 / 跨 re-render 存活
  // 结构：{ systems, app, search, openers, retention, dirty*, loaded* }
  // dirty=true 是"任一节脏"的总标记（用于按钮 disabled / 离开确认）；
  // sectionsDirty = { systems, openers, retention } 是分节脏标记，
  // doSave 只 PUT 真正改过的节；saveOpenerRow 只清 openersDirty，
  // 不影响 systemsDirty（修 #B1）。
  if (!Kairo.state.configEditor) {
    Kairo.state.configEditor = {
      systems: [],
      app: null,
      search: null,
      openers: [],
      openersLoaded: false,
      retention: { retention_days: 7, max_count: 1000 },
      retentionLoaded: false,
      // v1.0：自启开关 — {enabled, actual, platform, supported, keyName, error}
      // actual 是注册表实际值，可能跟 enabled 不一致（用户手动 regedit 清了 / 拷贝到 macOS）
      autostart: { enabled: false, actual: false, platform: '', supported: false, keyName: '', error: '' },
      autostartLoaded: false,
      // 总 dirty = 任一节脏（同步给保存按钮 + 离开确认）
      dirty: false,
      // 分节 dirty：doSave 只 PUT 真正脏的节；单行保存按钮只清自己那节
      sectionsDirty: { systems: false, openers: false, retention: false, autostart: false },
      freeBrowserEnabled: true,
      loaded: false
    };
  }
  const state = Kairo.state.configEditor;

  // 同步两个保存按钮（顶部 #cfg-save-btn 和底部 .cfg-save-footer）的 disabled
  // 【修复】原 syncSaveBtns 定义在 renderConfig 闭包里，而 recomputeDirty 在模块级
  // 调用它，导致 ReferenceError: syncSaveBtns is not defined，所有按钮点击都报错。
  // 提到模块级，用 querySelectorAll 兜底（按钮未渲染时静默跳过）。
  const syncSaveBtns = () => {
    const top = $('#cfg-save-btn');
    if (top) top.disabled = !state.dirty;
    document.querySelectorAll('.cfg-save-footer').forEach(b => { b.disabled = !state.dirty; });
  };

  // 计算总 dirty + 同步给 Kairo.state.unsavedConfig + 按钮 disabled
  const recomputeDirty = () => {
    const any = state.sectionsDirty.systems || state.sectionsDirty.openers || state.sectionsDirty.retention || state.sectionsDirty.autostart;
    state.dirty = any;
    Kairo.state.unsavedConfig = any;
    syncSaveBtns();
  };

  function renderConfig(view) {
    // 工具：标记 dirty + 通知 app.js 用于离开页面前确认
    // 【v0.5 #3】保存按钮现在有"顶部"+"底部"两个（共享 onclick handler 和 disabled 状态），
    // 通过 syncSaveBtns() 同步两个按钮。
    // 【v0.x 修复 #B1】markDirty 必须指定节（默认 'systems'）；openers 单行保存只清自己那节。
    const markDirty = (section = 'systems') => {
      state.sectionsDirty[section] = true;
      recomputeDirty();
    };

    // ---- 顶部警告 banner（项 14）：自由文件浏览器打开时 ----
    const bannerKey = 'free-file-browser-warning';
    const banner = el('div', { class: 'warn-banner', style: 'display:none' });
    banner.appendChild(el('div', { class: 'warn-banner-text' }, [
      el('strong', { style: 'display:inline-flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smWarn', 16) + ' 文件浏览器任意路径下载已开启' }),
      el('div', { class: 'warn-banner-sub', text: '生产分发建议 config.yaml 设 app.enable_free_file_browser = false，或在 app.free_file_roots 里加白名单路径。' })
    ]));
    const closeBtn = el('button', { class: 'warn-banner-close', text: '×', title: '本会话不再显示', onclick: () => {
      Kairo.state.dismiss(bannerKey);
      banner.style.display = 'none';
    }});
    banner.appendChild(closeBtn);

    // ---- 应用信息卡（只读展示） ----
    const appCard = el('div', { class: 'card' });
    const appKV = el('div');
    appCard.appendChild(el('h3', { text: '应用自身配置', title: '只读展示，编辑请改 config.yaml' }));
    appCard.appendChild(el('div', { class: 'card-desc readonly-note', text: 'ⓘ 只读：应用名、监听端口、目录等需要在 config.yaml 修改后重启服务。' }));
    appCard.appendChild(appKV);

    // ---- 搜索参数卡（只读展示） ----
    const searchCard = el('div', { class: 'card' });
    const searchKV = el('div');
    searchCard.appendChild(el('h3', { text: '搜索默认参数', title: '只读展示，编辑请改 config.yaml' }));
    searchCard.appendChild(el('div', { class: 'card-desc readonly-note', text: 'ⓘ 只读：默认最近文件数、最大匹配条数等需要在 config.yaml 修改后重启服务。' }));
    searchCard.appendChild(searchKV);

    // ---- 编辑器卡（动态） ----
    // 【v0.5 #5】编辑器卡描述：解释增删改流程 + 复制按钮的语义（之前的版本用户抱怨"复制日志目录"含义不清）。
    const editorCard = el('div', { class: 'card' });
    editorCard.appendChild(el('h3', { text: '业务系统 / 服务器 / 日志目录' }));
    editorCard.appendChild(el('div', { class: 'card-desc' }, [
      el('div', { text: '增删改后点顶部或底部"保存"才生效；保存会原子改写 config.yaml，不需要重启。' }),
      el('div', { class: 'card-desc-extra', style: 'margin-top:4px; font-size:12px; color:var(--text-dim);' }, [
        el('strong', { text: '复制按钮说明：' }),
        el('span', { text: '系统行的"复制"=复制整个业务系统（含所有服务器和日志目录）；' }),
        el('span', { text: '服务器行的"复制"=复制该服务器（含其全部日志目录），方便在多台机器复用配置后再修改主机名/端口。' })
      ])
    ]));

    const editorBody = el('div');
    editorCard.appendChild(editorBody);

    // 顶部操作条
    // 【v0.5 #3】btnSave 一律带 btn-primary 样式（不论 dirty 与否都明显），
    // 用 disabled 表示"无未保存改动"。底部再放一个副本按钮处理长表单滚动到底部保存。
    const btnAddSys = el('button', { class: 'btn', text: '+ 新增业务系统', onclick: () => { state.systems.push(newSystem()); markDirty(); renderEditor(); } });
    const btnExport = el('button', { class: 'btn', text: '导出配置', title: '下载当前 config.yaml 备份', onclick: doExport });
    const btnImport = el('button', { class: 'btn', text: '导入配置', title: '从 .yaml 文件导入配置（会覆盖当前配置）', onclick: doImport });
    const btnSave = el('button', { class: 'btn btn-primary', id: 'cfg-save-btn', text: '保存', onclick: doSave });
    const btnReset = el('button', { class: 'btn', text: '放弃改动', onclick: doReset });
    btnSave.disabled = true;
    const topBar = el('div', { class: 'btn-row', style: 'justify-content: flex-end; margin-bottom: 12px;' }, [btnAddSys, btnExport, btnImport, btnReset, btnSave]);

    // 【v0.5 #3】底部操作条：复制"放弃改动"+"保存"按钮，长表单滚到底也能直接保存。
    // 顶部 + 底部按钮共用同一份 doSave / doReset 处理逻辑（共享 state.dirty）。
    // 【v0.x 修复 #L3】底部"放弃改动"按钮不带 cfg-save-footer class，
    // 让 syncSaveBtns 只禁用保存按钮，不禁用重置按钮——
    // 顶部和底部的"放弃改动"行为应该一致（都始终可点，重置时也只是 re-fetch，无副作用）。
    const btnSaveFooter = el('button', { class: 'btn btn-primary cfg-save-footer', text: '保存', onclick: doSave });
    const btnResetFooter = el('button', { class: 'btn', text: '放弃改动', onclick: doReset });
    btnSaveFooter.disabled = true;
    const footerBar = el('div', { class: 'btn-row cfg-save-footer-bar', style: 'justify-content: flex-end; margin-top: 16px; padding-top: 12px; border-top: 1px dashed var(--line);' }, [btnResetFooter, btnSaveFooter]);

    // op.icon 始终是 string（emoji 或空），与后端 ExternalOpener.Icon 字段一致，
    // 直接透传即可。历史数据里若混入 object（旧渲染产物），归一化成空串避免 400。
    function serializeOpenersForSave(openers) {
      return (openers || []).map(o => {
        const iconStr = (typeof o.icon === 'string') ? o.icon : '';
        return { name: o.name, path: o.path, icon: iconStr };
      });
    }

    // ---- v0.8：外部打开器（external_openers）卡 ----
    //
    // 让用户在页面里增删常用打开器（Notepad++ / IDEA / VS Code ...），
    // 每个 opener 含 {name, path, icon(emoji)}。
    // 保存走独立的 PUT /api/admin/openers（不复用 admin/servers 接口，
    // 因为后者不接收 app 段）。
    const openersCard = el('div', { class: 'card' });
    openersCard.appendChild(el('h3', { text: '外部打开器' }));
    openersCard.appendChild(el('div', { class: 'card-desc', text: '配置本地软件（如 Notepad++ / IDEA / VS Code）的可执行文件路径，保存后在「下载历史」页每行会显示对应图标按钮，点一下就用该软件打开文件。' }));
    const openersBody = el('div');
    openersCard.appendChild(openersBody);
    openersCard.appendChild(el('div', { class: 'mt-2' }, [
      el('button', { class: 'btn btn-sm', text: '+ 添加打开器',
        onclick: () => {
          state.openers.push({ name: '', path: '', icon: '' });
          markDirty('openers');
          renderOpeners();
        }
      })
    ]));

    // ---- v0.x：下载历史清理策略卡 ----
    const retentionCard = el('div', { class: 'card' });
    retentionCard.appendChild(el('h3', { text: '下载历史自动清理' }));
    retentionCard.appendChild(el('div', { class: 'card-desc', text: '配置下载文件和历史记录的自动清理策略。设为 0 表示不限制/不自动清理。' }));
    const retentionBody = el('div');
    retentionCard.appendChild(retentionBody);

    // ---- v1.0：开机自启动卡 ----
    //
    // 用户勾选「开机自动启动」→ 保存按钮立即写 Windows 注册表
    // (HKCU\Software\Microsoft\Windows\CurrentVersion\Run\Kairo)，
    // 下次开机 Kairo 会自动启动。不勾选则删除注册表值。
    // 兼容 Win7 / Win10 / Win11（用户级 Run 键三代行为一致）。
    //
    // 显示三态：✓ 已启用 / ⚠ 配置与实际不一致 / ✗ 平台不支持。
    // 平台不支持（macOS / Linux）时勾选框 disabled + 提示文案，PUT 也会被后端 400 挡掉。
    const startupCard = el('div', { class: 'card' });
    startupCard.appendChild(el('h3', { text: '开机自启' }));
    startupCard.appendChild(el('div', { class: 'card-desc', text: '勾选后下次开机 Kairo 自动启动（写入用户注册表，无需管理员权限）。' }));
    const startupBody = el('div');
    startupCard.appendChild(startupBody);

    view.appendChild(banner);
    view.appendChild(topBar);
    view.appendChild(appCard);
    view.appendChild(searchCard);
    view.appendChild(editorCard);
    view.appendChild(openersCard);
    view.appendChild(retentionCard);
    view.appendChild(startupCard);
    view.appendChild(footerBar);

    function maybeShowBanner(info) {
      // 仅当 free_file_browser === true（且 free_file_roots 为空，即"自由模式"）时显示
      const app = (info && info.app) || {};
      const enabled = app.enable_free_file_browser === undefined || app.enable_free_file_browser === true;
      const roots = Array.isArray(app.free_file_roots) ? app.free_file_roots : [];
      // 只在"自由模式"下提醒（白名单模式已经有 renderFiles 顶部提示，不重复）
      state.freeBrowserEnabled = enabled && roots.length === 0;
      if (state.freeBrowserEnabled && !Kairo.state.isDismissed(bannerKey)) {
        banner.style.display = '';
      }
    }

    // ----- 渲染 -----
    function renderApp() {
      appKV.innerHTML = '';
      if (!state.app) return;
      appKV.appendChild(kvTable([
        ['应用名', state.app.name],
        ['监听', state.app.host + ':' + state.app.port],
        ['自动打开浏览器', String(state.app.auto_open_browser)],
        ['下载目录', state.app.download_dir],
        ['日志目录', state.app.log_dir],
        ['数据目录', state.app.data_dir]
      ]));
    }
    function renderSearch() {
      searchKV.innerHTML = '';
      if (!state.search) return;
      searchKV.appendChild(kvTable([
        ['默认最近文件数', String(state.search.default_latest_files)],
        ['最大匹配条数', String(state.search.max_matches)],
        ['默认上下文行数', String(state.search.default_context_lines)],
        ['搜索超时（秒）', String(state.search.timeout_seconds)],
        ['最大并发', String(state.search.max_concurrency)]
      ]));
    }
    function renderEditor() {
      editorBody.innerHTML = '';
      if (!state.systems.length) {
        editorBody.appendChild(el('div', { class: 'text-dim', text: '暂无业务系统，点上方"+ 新增业务系统"开始。' }));
        return;
      }
      state.systems.forEach((sys, si) => {
        editorBody.appendChild(renderSystemBlock(sys, si));
      });
    }

    function renderSystemBlock(sys, si) {
      const wrap = el('div', { class: 'sys-block' });
      const nameInp = el('input', { type: 'text', value: sys.name || '', placeholder: '业务系统名（必填）' });
      nameInp.addEventListener('input', () => { sys.name = nameInp.value; markDirty(); });
      const btnUp = el('button', { class: 'btn btn-sm', text: '↑', title: '上移', onclick: () => { if (si > 0) { [state.systems[si-1], state.systems[si]] = [state.systems[si], state.systems[si-1]]; markDirty(); renderEditor(); } } });
      // P0-2 修复：下移逻辑写反了（右侧用了 si-1 而非 si+1），导致数组越位破坏
      const btnDown = el('button', { class: 'btn btn-sm', text: '↓', title: '下移', onclick: () => { if (si < state.systems.length - 1) { [state.systems[si+1], state.systems[si]] = [state.systems[si], state.systems[si+1]]; markDirty(); renderEditor(); } } });
      const btnDup = el('button', { class: 'btn btn-sm', text: '复制', title: '复制整个业务系统（含所有服务器和日志目录），自动加 -copy 后缀避免重名',
        onclick: () => {
          const dup = JSON.parse(JSON.stringify(sys));
          dup.name = (sys.name || '未命名') + '-copy';
          // 复制里的服务器也加 -copy 后缀，保持一致性（参考 server 复制行为）
          if (Array.isArray(dup.servers)) {
            dup.servers.forEach(s => { if (s && s.name) s.name = s.name + '-copy'; });
          }
          state.systems.splice(si + 1, 0, dup);
          markDirty();
          renderEditor();
        } });
      const btnDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除系统', onclick: async () => { if (await confirmDialog('确认删除业务系统 “' + (sys.name || '(未命名)') + '” 及其全部服务器？')) { state.systems.splice(si, 1); markDirty(); renderEditor(); } } });
      btnUp.disabled = si === 0; btnDown.disabled = si === state.systems.length - 1;

      // v0.5 #15：业务系统 badge + 编号
      const sysBadge = el('span', { class: 'tier-badge tier-sys', text: '业务系统 ' + (si + 1) });
      wrap.appendChild(el('div', { class: 'sys-head' }, [
        sysBadge,
        el('span', { class: 'sys-tag', text: '系统' }),
        el('div', { class: 'sys-name-input' }, nameInp),
        el('div', { class: 'sys-actions' }, [btnUp, btnDown, btnDup, btnDel])
      ]));

      const srvList = el('div', { class: 'srv-list' });
      (sys.servers || []).forEach((srv, sri) => {
        srvList.appendChild(renderServerBlock(sys, srv, sri, markDirty));
      });
      srvList.appendChild(el('div', { class: 'mt-2' }, [
        el('button', { class: 'btn btn-sm', text: '+ 新增服务器', onclick: () => { sys.servers = sys.servers || []; sys.servers.push(newServer()); markDirty(); renderEditor(); } })
      ]));
      wrap.appendChild(srvList);
      return wrap;
    }

    function renderServerBlock(sys, srv, sri, onEdit) {
      const wrap = el('div', { class: 'srv-block' });
      // v0.5 #15：服务器 badge + 编号
      wrap.appendChild(el('div', { class: 'tier-badge tier-srv', text: '服务器 ' + (sri + 1) }));
      const fields = [
        ['name', '服务器名（必填）', 'text'],
        ['host', 'IP / 主机', 'text'],
        ['port', 'SSH 端口', 'number'],
        ['username', 'SSH 用户名', 'text'],
        ['auth_type', '认证方式', 'select', [['password', '密码']]],
        ['password', 'SSH密码', 'password']
      ];
      const inputMap = {};
      const fieldRow = el('div', { class: 'srv-fields' });
      fields.forEach(([key, ph, type, options]) => {
        if (type === 'select') {
          const sel = el('select');
          (options || []).forEach(([v, label]) => {
            const o = el('option', { value: v, text: label });
            if (String(srv[key] || '') === v) o.selected = true;
            sel.appendChild(o);
          });
          if (!sel.value && options && options.length) {
            sel.value = options[0][0];
            srv[key] = sel.value;
          }
          sel.addEventListener('change', () => { srv[key] = sel.value; onEdit(); });
          inputMap[key] = sel;
          fieldRow.appendChild(el('label', null, [
            el('span', { class: 'lbl', text: ph }),
            sel
          ]));
        } else {
          const inp = el('input', { type: type, value: srv[key] != null ? String(srv[key]) : '', placeholder: ph });
          if (key === 'port') { inp.min = '1'; inp.max = '65535'; }
          inp.addEventListener('input', () => {
            if (type === 'number') {
              const n = parseInt(inp.value, 10);
              // 端口范围校验：1-65535，非法值标红边框提示
              if (key === 'port') {
                const valid = !isNaN(n) && n > 0 && n < 65536;
                inp.style.borderColor = valid || inp.value === '' ? '' : 'var(--error, #ef4444)';
                inp.title = valid ? '' : '端口范围 1-65535';
                srv[key] = isNaN(n) ? 0 : n;
              } else {
                srv[key] = isNaN(n) ? 0 : n;
              }
            } else {
              srv[key] = inp.value;
            }
            onEdit();
          });
          inputMap[key] = inp;
          fieldRow.appendChild(el('label', null, [
            el('span', { class: 'lbl', text: ph }),
            inp
          ]));
        }
      });
      wrap.appendChild(fieldRow);

      const dirList = el('div', { class: 'dir-list' });
      (srv.log_dirs || []).forEach((ld, ldi) => {
        dirList.appendChild(renderDirBlock(srv, ld, ldi, onEdit));
      });
      dirList.appendChild(el('div', { class: 'mt-1' }, [
        el('button', { class: 'btn btn-sm', text: '+ 新增日志目录', onclick: () => { srv.log_dirs = srv.log_dirs || []; srv.log_dirs.push(newLogDir()); onEdit(); renderEditor(); } })
      ]));
      wrap.appendChild(dirList);

      const btnSrvUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (sri > 0) { [sys.servers[sri-1], sys.servers[sri]] = [sys.servers[sri], sys.servers[sri-1]]; onEdit(); renderEditor(); } } });
      const btnSrvDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (sri < sys.servers.length - 1) { [sys.servers[sri+1], sys.servers[sri]] = [sys.servers[sri], sys.servers[sri+1]]; onEdit(); renderEditor(); } } });
      const btnSrvDup = el('button', {
        class: 'btn btn-sm',
        style: 'display:inline-flex; align-items:center; gap:4px;',
        title: '复制这台服务器（含所有日志目录）到下一行。适合 app01 → app02 改名后复用。复制后会重名（自动加 -copy 后缀提示）。',
        onclick: () => {
          const copy = JSON.parse(JSON.stringify(srv));
          if (copy.name && !copy.name.endsWith('-copy')) copy.name = copy.name + '-copy';
          sys.servers.splice(sri+1, 0, copy);
          onEdit(); renderEditor();
        },
        unsafeHtml: svgIcon('smClipboard', 14) + ' 复制服务器（含日志目录）'
      });
      const btnSrvDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除服务器', onclick: async () => { if (await confirmDialog('确认删除服务器 “' + (srv.name || '(未命名)') + '” 及其日志目录？')) { sys.servers.splice(sri, 1); onEdit(); renderEditor(); } } });
      btnSrvUp.disabled = sri === 0; btnSrvDown.disabled = sri === sys.servers.length - 1;
      wrap.appendChild(el('div', { class: 'srv-actions' }, [btnSrvUp, btnSrvDown, btnSrvDup, btnSrvDel]));

      return wrap;
    }

    function renderDirBlock(srv, ld, ldi, onEdit) {
      const wrap = el('div', { class: 'dir-block' });
      // v0.5 #15：日志目录 badge + 编号
      wrap.appendChild(el('div', { class: 'tier-badge tier-dir', text: '日志目录 ' + (ldi + 1) }));
      const nameInp = el('input', { type: 'text', value: ld.name || '', placeholder: '目录别名（必填）' });
      const pathInp = el('input', { type: 'text', value: ld.path || '', placeholder: '远端绝对路径（必填）' });
      const encSel = el('select');
      encSel.title = '日志文件的字符编码：中文乱码时改 gbk。';
      [
        ['utf-8', 'utf-8'],
        ['gbk', 'gbk']
      ].forEach(([v, t]) => {
        encSel.appendChild(el('option', { value: v, text: t }));
      });
      // v0.5 修复 #6：显式设 encSel.value + 同步 ld.encoding，
      // 比"option.selected = true"更可靠（避免 select.selectedIndex 不更新的边角 case）。
      // 后端 Validate 接受 utf-8 / gbk / ""，这里统一规一到小写。
      const wantEnc = (ld.encoding || 'utf-8').toLowerCase();
      encSel.value = (wantEnc === 'gbk') ? 'gbk' : 'utf-8';
      if (ld.encoding !== encSel.value) ld.encoding = encSel.value;
      const patTa = el('textarea', { rows: '4', placeholder: '文件名规则，每行一条，支持 glob 通配符 (* ? [abc])。\n例如：\n  SystemOut*.log\n  *.log\n  error_*.txt' });
      patTa.value = (ld.patterns || []).join('\n');
      function autoResizeTa() {
        patTa.style.height = 'auto';
        patTa.style.height = patTa.scrollHeight + 'px';
      }
      patTa.addEventListener('input', () => {
        ld.patterns = patTa.value.split('\n').map(s => s.trim()).filter(Boolean);
        onEdit();
        autoResizeTa();
      });
      setTimeout(autoResizeTa, 0);
      nameInp.addEventListener('input', () => { ld.name = nameInp.value; onEdit(); });
      pathInp.addEventListener('input', () => { ld.path = pathInp.value; onEdit(); });
      encSel.addEventListener('change', () => { ld.encoding = encSel.value; onEdit(); });
      wrap.appendChild(el('div', { class: 'dir-fields' }, [
        el('label', null, [el('span', { class: 'lbl', text: '目录别名' }), nameInp]),
        el('label', null, [el('span', { class: 'lbl', text: '远端路径' }), pathInp]),
        el('label', null, [el('span', { class: 'lbl', text: '编码' }), encSel])
      ]));
      wrap.appendChild(el('label', { class: 'block' }, [
        el('span', { class: 'lbl', text: '文件名规则（每行一条）' }),
        patTa
      ]));
      // P2-16：新增"复制当前日志目录"按钮
      const btnDirDup = el('button', { class: 'btn btn-sm', text: '复制目录', title: '复制此日志目录（含别名/路径/编码/文件名规则）', onclick: () => { srv.log_dirs = srv.log_dirs || []; srv.log_dirs.splice(ldi + 1, 0, JSON.parse(JSON.stringify(ld))); onEdit(); renderEditor(); } });
      const btnDirUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (ldi > 0) { [srv.log_dirs[ldi-1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi-1]]; onEdit(); renderEditor(); } } });
      const btnDirDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (ldi < srv.log_dirs.length - 1) { [srv.log_dirs[ldi+1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi+1]]; onEdit(); renderEditor(); } } });
      const btnDirDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除目录', onclick: async () => { if (await confirmDialog('确认删除日志目录 "' + (ld.name || ld.path || '(未命名)') + '" ？')) { srv.log_dirs.splice(ldi, 1); onEdit(); renderEditor(); } } });
      btnDirUp.disabled = ldi === 0; btnDirDown.disabled = ldi === srv.log_dirs.length - 1;
      wrap.appendChild(el('div', { class: 'dir-actions' }, [btnDirUp, btnDirDown, btnDirDup, btnDirDel]));
      return wrap;
    }

    // 渲染打开器图标：v0.14 起统一调 Kairo.icons.openerIconHTML
    //   - emoji 优先（用户在配置页手填）
    //   - 选 exe 后临时预览（op._iconPreviewBase64）→ data URL 直接显示
    //   - 否则 <img src=/api/local/opener-icon> 显示后端缓存的 exe 真实图标
    //   - 失败时 onerror 自动 fallback 到按名推断颜色的 SVG 占位
    // 配置页 size 用 20px（比列表里的 14px 大一档，方便用户辨识）。
    function renderOpenerIcon(op) {
      // 选 exe 后实时预览：extract-icon API 返回的 base64（未保存前走 data URL）
      if (op && op._iconPreviewBase64) {
        return '<img src="data:image/png;base64,' + op._iconPreviewBase64 + '" alt="" width="20" height="20" ' +
          'style="width:20px; height:20px; vertical-align:middle; object-fit:contain;" ' +
          'onerror="this.outerHTML=\'\'">';
      }
      if (window.Kairo && Kairo.icons && Kairo.icons.openerIconHTML) {
        return Kairo.icons.openerIconHTML(op, 20);
      }
      // icons.js 未加载（理论不会发生，兜底防黑屏）：fallback 到原 SVG 占位
      const color = (op && op.path && op.path.toLowerCase().includes('notepad')) ? '#f59e0b' : '#64748b';
      return svgIcon('smFile', 20).replace('stroke="currentColor"', 'stroke="' + escapeHtml(color) + '"');
    }

    // previewOpenerIcon 选 exe 后调 extract-icon API 拿 PNG base64，
    // 临时存到 op._iconPreviewBase64，再 updateIcon() 触发重渲染。
    // 失败/平台不支持时静默跳过（renderOpenerIcon 会走 SVG fallback）。
    async function previewOpenerIcon(op) {
      if (!op || !op.path) return;
      try {
        const r = await api('POST', '/api/admin/openers/extract-icon', { path: op.path });
        if (r && r.png_base64) {
          op._iconPreviewBase64 = r.png_base64;
        } else {
          // 平台不支持 / 提取失败 → 清掉旧的 preview，避免改 path 后还显示旧图
          delete op._iconPreviewBase64;
        }
      } catch (_) {
        delete op._iconPreviewBase64;
      }
    }

    async function saveOpenerRow(op) {
      // 【v0.x 修复 #B1 + #B3】前端预校验 + 只清 openers 节 dirty。
      // 后端仍然会兜底校验（不能信任前端），这里只是把明显错误提前到客户端。
      if (!op.name || !op.name.trim()) {
        toast('保存失败：打开器名称不能为空', 'err');
        return;
      }
      if (!op.path || !op.path.trim()) {
        toast('保存失败：打开器路径不能为空', 'err');
        return;
      }
      // 名称唯一性（前端先查，能避免一次往返；后端仍会兜底校验）
      const dup = state.openers.find(o => o !== op && String(o.name || '').trim() === String(op.name || '').trim());
      if (dup) {
        toast('保存失败：打开器名称 "' + op.name + '" 已存在', 'err');
        return;
      }
      // v0.11：和 doSave 一致 —— 先把完全空的行（没 name 也没 path）剔掉，
      // 否则后端校验会拒（"external_openers[i].name 不能为空"）。
      // doSave 本来就 clean，但 saveOpenerRow 是单行保存不跑 doSave，留了空行
      // 在 state.openers 里就会让单行保存也 400。
      const cleaned = (state.openers || []).filter(o => (o.name && o.name.trim()) || (o.path && o.path.trim()));
      if (cleaned.length !== state.openers.length) {
        state.openers = cleaned;
        renderOpeners();
      }
      try {
        // v0.11：op.icon 是渲染用 object，PUT 前序列化成 string 避免后端 unmarshal 400
        await api('PUT', '/api/admin/openers', { openers: serializeOpenersForSave(state.openers) });
        // 只清 openers 这节 dirty —— 不影响 systemsDirty / retentionDirty。
        // 旧实现直接 state.dirty = false，会把业务系统的未保存改动也清掉。
        state.sectionsDirty.openers = false;
        recomputeDirty();
        toast('打开器已保存', 'ok');
      } catch (e) {
        toast('保存失败：' + e.message, 'err');
      }
    }

    function renderOpeners() {
      openersBody.innerHTML = '';
      if (!state.openers || state.openers.length === 0) {
        openersBody.appendChild(el('div', { class: 'text-dim', text: '暂无外部打开器，点下方"+ 添加打开器"按钮添加。' }));
        return;
      }
      const list = el('div');
      state.openers.forEach((op, idx) => {
        if (op.icon != null && typeof op.icon !== 'string') op.icon = '';

        const row = el('div', { class: 'opener-row', style: 'display:flex; gap:8px; margin-bottom:8px; align-items:center;' });
        const iconWrap = el('div', { style: 'width:34px; height:34px; flex-shrink:0; border:1px solid var(--line); border-radius:8px; display:inline-flex; align-items:center; justify-content:center; background:var(--bg-2); position:relative; cursor:pointer;', title: '点击修改图标 emoji' });
        const iconSpan = el('span', { style: 'font-size:18px; display:flex; align-items:center; justify-content:center; width:100%; height:100%;', unsafeHtml: renderOpenerIcon(op) });
        iconWrap.appendChild(iconSpan);
        const iconInp = el('input', { type: 'text', value: (typeof op.icon === 'string') ? op.icon : '', placeholder: '📝', title: '图标 emoji（留空则使用 exe 图标）', maxlength: '4', style: 'position:absolute; inset:0; width:100%; height:100%; border:none; border-radius:8px; background:var(--bg-1); text-align:center; font-size:16px; padding:0; display:none; outline:none; box-sizing:border-box;' });
        iconWrap.appendChild(iconInp);
        iconWrap.addEventListener('click', () => {
          iconSpan.style.display = 'none';
          iconInp.style.display = 'block';
          iconInp.focus();
          iconInp.select();
        });
        iconInp.addEventListener('blur', () => {
          iconSpan.style.display = '';
          iconInp.style.display = 'none';
        });
        iconInp.addEventListener('keydown', (ev) => {
          if (ev.key === 'Enter' || ev.key === 'Escape') { ev.preventDefault(); iconInp.blur(); }
        });

        const nameInp = el('input', { type: 'text', value: op.name || '', placeholder: '名称（如 Notepad++）', style: 'flex:1;' });

        const pathWrap = el('div', { style: 'flex:2; display:flex; gap:4px; position:relative;' });
        const pathInp = el('input', { type: 'text', value: op.path || '', placeholder: '可执行文件路径（如 C:\\Windows\\notepad.exe 或 /usr/bin/code）', style: 'flex:1;' });
        const btnBrowse = el('button', { class: 'btn btn-sm', title: '选择文件', type: 'button', style: 'flex-shrink:0; display:inline-flex; align-items:center; gap:0; width:32px; justify-content:center; padding-left:0; padding-right:0;', unsafeHtml: svgIcon('smFolder', 14) });
        btnBrowse.addEventListener('click', async () => {
          try {
            const r = await api('POST', '/api/choose-file');
            if (r && r.path) {
              pathInp.value = r.path;
              op.path = r.path;
              markDirty('openers');
              updateIcon();
              // 选完 exe 后实时预览图标（调后端 extract-icon）
              await previewOpenerIcon(op);
              updateIcon();
            }
          } catch (e) { /* ignore */ }
        });
        pathWrap.appendChild(pathInp);
        pathWrap.appendChild(btnBrowse);

        const btnSave = el('button', { class: 'btn btn-sm', type: 'button', style: 'display:inline-flex; align-items:center; gap:4px;', onclick: () => saveOpenerRow(op), unsafeHtml: svgIcon('smSave', 14) + ' 保存' });
        const btnDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除', type: 'button', onclick: async () => {
          if (await confirmDialog('确认删除打开器 "' + (op.name || '(未命名)') + '"？')) {
            state.openers.splice(idx, 1);
            markDirty('openers');
            renderOpeners();
          }
        }});

        function updateIcon() {
          iconSpan.innerHTML = renderOpenerIcon(op);
        }

        // 防抖：手动输入 path 时不要每次按键都调 extract-icon（用户可能还在敲）
        let pathPreviewTimer = null;
        function schedulePathPreview() {
          if (pathPreviewTimer) clearTimeout(pathPreviewTimer);
          pathPreviewTimer = setTimeout(async () => {
            await previewOpenerIcon(op);
            updateIcon();
          }, 500);
        }

        iconInp.addEventListener('input', () => { op.icon = iconInp.value; markDirty('openers'); updateIcon(); });
        nameInp.addEventListener('input', () => { op.name = nameInp.value; markDirty('openers'); updateIcon(); });
        pathInp.addEventListener('input', () => {
          op.path = pathInp.value;
          // path 变了，旧的 preview 失效，先清掉避免显示错图标
          delete op._iconPreviewBase64;
          markDirty('openers');
          updateIcon();
          schedulePathPreview();
        });

        row.appendChild(iconWrap);
        row.appendChild(nameInp);
        row.appendChild(pathWrap);
        row.appendChild(btnSave);
        row.appendChild(btnDel);
        list.appendChild(row);
      });
      openersBody.appendChild(list);
    }

    function renderRetention() {
      retentionBody.innerHTML = '';
      const row = el('div', { style: 'display:flex; gap:16px; flex-wrap:wrap;' });

      const daysVal = state.retention.days != null ? state.retention.days : 7;
      const countVal = state.retention.count != null ? state.retention.count : 1000;

      const daysWrap = el('label', { style: 'display:flex; flex-direction:column; gap:4px; flex:1; min-width:200px;' });
      daysWrap.appendChild(el('span', { class: 'lbl', text: '保留天数' }));
      const daysInp = el('input', { type: 'number', min: '0', value: String(daysVal), placeholder: '7' });
      daysInp.addEventListener('input', () => {
        const n = parseInt(daysInp.value, 10);
        state.retention.days = isNaN(n) ? 0 : n;
        markDirty('retention');
      });
      daysWrap.appendChild(daysInp);
      daysWrap.appendChild(el('span', { class: 'card-desc', style: 'font-size:12px;', text: '0 = 不按时间自动清理；默认 7 天' }));

      const countWrap = el('label', { style: 'display:flex; flex-direction:column; gap:4px; flex:1; min-width:200px;' });
      countWrap.appendChild(el('span', { class: 'lbl', text: '最大记录数' }));
      const countInp = el('input', { type: 'number', min: '0', value: String(countVal), placeholder: '1000' });
      countInp.addEventListener('input', () => {
        const n = parseInt(countInp.value, 10);
        state.retention.count = isNaN(n) ? 0 : n;
        markDirty('retention');
      });
      countWrap.appendChild(countInp);
      countWrap.appendChild(el('span', { class: 'card-desc', style: 'font-size:12px;', text: '0 = 不限制数量；默认 1000 条' }));

      row.appendChild(daysWrap);
      row.appendChild(countWrap);
      retentionBody.appendChild(row);
      retentionBody.appendChild(el('div', { class: 'card-desc', style: 'margin-top:8px;', text: '提示：系统启动时自动清理一次；每次下载完成后也会触发检查。' }));
    }

    // renderAutoStart v1.0：渲染「开机自启」卡的 UI。
    //
    // 三态展示：
    //   - enabled === actual === true            → 绿色「✓ 已启用」
    //   - enabled === actual === false           → 灰色「未启用」
    //   - enabled !== actual                     → 黄色「⚠ 配置与实际不一致」+ 同步按钮
    //   - supported === false（macOS / Linux）  → 灰，勾选框 disabled，文案「该平台不支持」
    //
    // 勾选后走 markDirty('autostart')，由顶部 / 底部保存按钮统一 PUT。
    function renderAutoStart() {
      startupBody.innerHTML = '';
      const a = state.autostart;

      // 状态徽章：根据 enabled / actual / supported 计算
      let badge, badgeColor;
      if (!a.supported) {
        badge = '✗ 当前平台不支持';
        badgeColor = '#9ca3af';
      } else if (a.enabled && a.actual) {
        badge = '✓ 已启用';
        badgeColor = '#16a34a';
      } else if (!a.enabled && !a.actual) {
        badge = '未启用';
        badgeColor = '#9ca3af';
      } else {
        badge = '⚠ 配置与实际不一致';
        badgeColor = '#f59e0b';
      }
      const badgeEl = el('span', {
        style: 'display:inline-flex; align-items:center; padding:2px 10px; border-radius:10px; font-size:12px; color:#fff; background:' + badgeColor + ';',
        text: badge
      });

      // 勾选框 + 标签
      const checkWrap = el('label', { style: 'display:flex; align-items:center; gap:8px; cursor:' + (a.supported ? 'pointer' : 'not-allowed') + ';' });
      const check = el('input', { type: 'checkbox' });
      check.checked = !!a.enabled;
      check.disabled = !a.supported;
      check.style.cursor = a.supported ? 'pointer' : 'not-allowed';
      check.addEventListener('change', () => {
        state.autostart.enabled = check.checked;
        markDirty('autostart');
        // 勾选变化时刷新徽章（不重渲整个卡，避免输入焦点丢失）
        updateBadge();
      });
      checkWrap.appendChild(check);
      checkWrap.appendChild(el('span', { text: '开机自动启动 Kairo', style: 'font-weight:500;' }));

      const badgeWrap = el('div', { style: 'display:flex; align-items:center; gap:8px;' }, [badgeEl]);
      // 「配置与实际不一致」时多放一个"立即同步"按钮（不重渲）
      const btnSync = el('button', {
        class: 'btn btn-sm',
        text: '立即同步',
        title: '按当前 config 偏好重新写注册表',
        style: 'display:none;',
        onclick: async () => {
          btnSync.disabled = true;
          try {
            await api('PUT', '/api/admin/autostart', { enabled: a.enabled });
            const fresh = await api('GET', '/api/admin/autostart');
            state.autostart = { ...state.autostart, ...fresh };
            renderAutoStart();
            toast('同步成功：注册表已按 ' + (a.enabled ? '启用' : '禁用') + ' 更新', 'ok');
          } catch (e) {
            toast('同步失败：' + e.message, 'err');
          } finally {
            btnSync.disabled = false;
          }
        }
      });

      function updateBadge() {
        const a2 = state.autostart;
        let b, c;
        if (!a2.supported) { b = '✗ 当前平台不支持'; c = '#9ca3af'; }
        else if (a2.enabled && a2.actual) { b = '✓ 已启用'; c = '#16a34a'; }
        else if (!a2.enabled && !a2.actual) { b = '未启用'; c = '#9ca3af'; }
        else { b = '⚠ 配置与实际不一致'; c = '#f59e0b'; }
        badgeEl.textContent = b;
        badgeEl.style.background = c;
        // 不一致时显示「立即同步」按钮
        btnSync.style.display = (a2.enabled !== a2.actual) ? '' : 'none';
      }

      const top = el('div', { style: 'display:flex; align-items:center; justify-content:space-between; gap:12px; flex-wrap:wrap;' }, [
        checkWrap,
        el('div', { style: 'display:flex; align-items:center; gap:8px;' }, [badgeEl, btnSync])
      ]);
      startupBody.appendChild(top);

      // 提示文案
      const hint = el('div', { class: 'card-desc', style: 'margin-top:10px; font-size:12px;' });
      if (!a.supported) {
        hint.appendChild(el('span', { text: '当前平台（' + (a.platform || 'unknown') + '）暂不支持开机自启；此设置仅在 Windows 上生效。' }));
      } else if (a.error) {
        // IsAutoStartEnabled 读注册表失败（罕见：注册表被锁 / 权限问题）
        hint.appendChild(el('span', { style: 'color:var(--error, #ef4444);', text: '读取注册表状态失败：' + a.error + '。勾选「开机自动启动」保存后可重试。' }));
      } else {
        const platformLabel = a.platform === 'windows' ? 'Windows' : a.platform;
        const works = a.platform === 'windows' ? '（Win7 / Win10 / Win11 通用，无需管理员权限）' : '';
        hint.appendChild(el('span', { text: '注册表项：' + (a.keyName || '—') + '，当前平台 ' + platformLabel + ' ' + works }));
      }
      startupBody.appendChild(hint);
    }

    // ----- 保存 / 重置 -----
    // 【v0.x 修复 #B4】doSave 只 PUT 真正脏的节（sectionsDirty）。
    // 旧实现无条件 PUT 三段，未改的段也写盘 + 多一条 audit 日志。
    async function doSave() {
      const err = validate(state.systems);
      if (err) { toast('保存失败：' + err, 'err'); return; }

      // 至少有一节脏才允许保存（按钮 disabled 也应挡住，这里再兜底）
      if (!state.dirty) {
        toast('没有需要保存的改动', '');
        return;
      }

      // 收集待保存的节 + 错误
      const tasks = [];        // [{name, run}]
      const errs = [];         // [{name, err}]
      let path = '';

      if (state.sectionsDirty.systems) {
        tasks.push({
          name: 'systems',
          run: async () => {
            const r = await api('PUT', '/api/admin/servers', { systems: state.systems });
            path = r.path || path;
          }
        });
      }
      if (state.sectionsDirty.openers) {
        tasks.push({
          name: 'openers',
          run: async () => {
            // 保存前过滤掉完全空的行（名称和路径都为空），避免后端校验报错
            const cleaned = (state.openers || []).filter(o => (o.name && o.name.trim()) || (o.path && o.path.trim()));
            if (cleaned.length !== state.openers.length) {
              state.openers = cleaned;
              renderOpeners();
            }
            // v0.11：同上，icon object → string 后再 PUT
            await api('PUT', '/api/admin/openers', { openers: serializeOpenersForSave(cleaned) });
          }
        });
      }
      if (state.sectionsDirty.retention) {
        tasks.push({
          name: 'retention',
          run: async () => {
            await api('PUT', '/api/admin/download-retention', {
              download_retention_days: state.retention.days,
              download_max_count: state.retention.count,
            });
          }
        });
      }
      if (state.sectionsDirty.autostart) {
        tasks.push({
          name: 'autostart',
          run: async () => {
            // 自启走的不是 config 树，是独立端点。
            // PUT 失败会抛异常被外层 catch，config.yaml 不会被改（因为 auto_start 字段就是从 config 读的）——
            // 实际不严谨：若 PUT 成功但 config 落盘失败，main 下次启动会按旧 config 重新同步，反而能自愈。
            const r = await api('PUT', '/api/admin/autostart', { enabled: state.autostart.enabled });
            // 回写 actual（PUT 后端会回填）
            if (r && typeof r.actual === 'boolean') {
              state.autostart.actual = r.actual;
            }
            if (r && r.key_name) {
              state.autostart.keyName = r.key_name;
            }
          }
        });
      }

      // 顺序执行：前面的失败不阻断后面的（避免一处错挡住其他改动）
      for (const t of tasks) {
        try {
          await t.run();
          // 单节成功后立即清那节 dirty（即便后续节失败，用户至少能看到"这一节已经保存"）
          state.sectionsDirty[t.name] = false;
          recomputeDirty();
        } catch (e) {
          errs.push({ name: t.name, err: e });
        }
      }

      // 重新 GET 后端拉规范化数据回填（只 GET 改过的节 + 依赖的 app/search）
      if (state.sectionsDirty.systems) {
        try {
          const info = await api('GET', '/api/admin/servers');
          state.app = info.app;
          state.search = info.search;
          state.systems = JSON.parse(JSON.stringify(info.systems));
          renderApp(); renderSearch(); renderEditor();
        } catch (e) { /* 回填失败不影响"保存成功"的提示 */ }
      }
      if (state.sectionsDirty.openers) {
        try {
          const opInfo = await api('GET', '/api/admin/openers');
          state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
          renderOpeners();
        } catch (e) { /* 回填失败不影响 */ }
      }
      if (state.sectionsDirty.retention) {
        try {
          const retInfo = await api('GET', '/api/admin/download-retention');
          state.retention.days = retInfo.effective.retention_days;
          state.retention.count = retInfo.effective.max_count;
          renderRetention();
        } catch (e) { /* 回填失败不影响 */ }
      }
      if (state.sectionsDirty.autostart) {
        // 自启是独立端点，回填直接拉一次 GET 拿到最新的 actual / keyName
        try {
          const aInfo = await api('GET', '/api/admin/autostart');
          state.autostart = { ...state.autostart, ...aInfo };
          renderAutoStart();
        } catch (e) { /* 回填失败不影响 */ }
      }
      state.loaded = true;

      // 错误汇总：区分"部分失败"和"全部成功"
      // 成功的节不展示（避免信息过载），只突出失败的节
      const successNames = tasks.map(t => t.name).filter(n => !errs.find(e => e.name === n));
      if (errs.length) {
        const failed = errs.map(e => {
          // 中文名称映射，让用户更容易理解
          const label = { systems: '业务系统', openers: '外部打开器', retention: '下载清理' }[e.name] || e.name;
          return label + '：' + e.err.message;
        }).join('；');
        const okPart = successNames.length ? '（' + successNames.map(n => ({ systems: '业务系统', openers: '外部打开器', retention: '下载清理' }[n] || n)).join('、') + '已保存）' : '（无成功项）';
        toast('部分保存失败 — ' + failed + okPart, 'err');
      } else if (path) {
        toast('已保存并后端确认：' + path, 'ok');
      } else {
        toast('已保存', 'ok');
      }
    }
    async function doReset() {
      if (!state.dirty || await confirmDialog('放弃所有未保存的改动？')) {
        api('GET', '/api/admin/servers').then(info => {
          state.app = info.app; state.search = info.search;
          state.systems = JSON.parse(JSON.stringify(info.systems));
          // 同时重拉 openers
          api('GET', '/api/admin/openers').then(opInfo => {
            state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
            state.openersLoaded = true;
            renderOpeners();
          }).catch(() => { state.openers = []; state.openersLoaded = true; renderOpeners(); });
          // 同时重拉 retention 配置
          api('GET', '/api/admin/download-retention').then(retInfo => {
            state.retention.days = retInfo.effective.retention_days;
            state.retention.count = retInfo.effective.max_count;
            state.retentionLoaded = true;
            renderRetention();
          }).catch(() => { state.retentionLoaded = true; renderRetention(); });
          // 同时重拉自启状态
          api('GET', '/api/admin/autostart').then(aInfo => {
            state.autostart = { ...state.autostart, ...aInfo };
            state.autostartLoaded = true;
            renderAutoStart();
          }).catch(() => { state.autostartLoaded = true; renderAutoStart(); });
          // 重置后清所有 dirty（含分节 dirty）
          state.sectionsDirty.systems = false;
          state.sectionsDirty.openers = false;
          state.sectionsDirty.retention = false;
          state.sectionsDirty.autostart = false;
          state.loaded = true;
          recomputeDirty();
          renderApp(); renderSearch(); renderEditor(); renderOpeners(); renderRetention();
          renderAutoStart();
          maybeShowBanner(info);
        }).catch(e => toast('加载失败：' + e.message, 'err'));
      }
    }

    async function doExport() {
      try {
        const resp = await fetch('/api/config/export', { credentials: 'same-origin' });
        if (handleAuth401(resp)) { toast('需要登录', 'err'); return; }
        if (!resp.ok) {
          let msg = '导出失败: HTTP ' + resp.status;
          try { const j = await resp.json(); if (j.error) msg = j.error; } catch (_) {}
          toast(msg, 'err');
          return;
        }
        const blob = await resp.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'config.yaml';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
        // 【v0.x 修复 #U1】明确告知用户"密码已脱敏为 ***"，避免误以为导出了完整密码。
        toast('配置已导出为 config.yaml（密码等敏感字段已脱敏为 ***）', 'ok');
      } catch (e) {
        toast('导出失败：' + e.message, 'err');
      }
    }

    async function doImport() {
      if (state.dirty && !await confirmDialog('当前有未保存的改动，导入配置会丢弃这些改动。继续吗？')) {
        return;
      }
      const input = document.createElement('input');
      input.type = 'file';
      input.accept = '.yaml,.yml';
      input.onchange = () => {
        const file = input.files && input.files[0];
        if (!file) return;
        if (!/\.ya?ml$/i.test(file.name)) {
          toast('请选择 .yaml 或 .yml 文件', 'err');
          return;
        }
        const reader = new FileReader();
        reader.onload = async () => {
          const yamlText = reader.result;
          if (!yamlText || !String(yamlText).trim()) {
            toast('文件内容为空', 'err');
            return;
          }
          // 【v0.x 修复 #U2】预览从 200 → 2000 字符，方便用户确认内容。
          // confirm() 自带的消息框没有滚动条，2000 字符在大多数浏览器还能装下；
          // 再长会触发浏览器自动截断，反而不如当前。
          const preview = String(yamlText).substring(0, 2000).replace(/</g, '&lt;');
          const ellipsis = String(yamlText).length > 2000 ? '\n…（省略 ' + (String(yamlText).length - 2000) + ' 字符）' : '';
          const confirmMsg = '确定要导入此配置文件吗？\n\n文件：' + file.name + ' (' + file.size + ' 字节)\n\n⚠ 警告：导入后将完全覆盖现有配置，旧配置会自动备份为 .bak 文件。\n\n文件预览（前 2000 字符）：\n' + preview + ellipsis;
          if (!await confirmDialog(confirmMsg)) {
            toast('已取消导入', '');
            return;
          }
          doImportUpload(String(yamlText));
        };
        reader.onerror = () => toast('读取文件失败', 'err');
        reader.readAsText(file, 'utf-8');
      };
      input.click();
    }

    async function doImportUpload(yamlText) {
      try {
        const resp = await fetch('/api/config/import', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'text/yaml' },
          body: yamlText
        });
        if (handleAuth401(resp)) { toast('需要登录', 'err'); return; }
        let result;
        try { result = await resp.json(); } catch (_) { result = {}; }
        if (!resp.ok) {
          toast('导入失败：' + (result.error || ('HTTP ' + resp.status)), 'err');
          return;
        }
        // 导入成功：重新加载完整配置
        const info = await api('GET', '/api/admin/servers');
        state.app = info.app;
        state.search = info.search;
        state.systems = JSON.parse(JSON.stringify(info.systems));
        try {
          const opInfo = await api('GET', '/api/admin/openers');
          state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
          state.openersLoaded = true;
        } catch (_) { state.openers = []; state.openersLoaded = true; }
        try {
          const retInfo = await api('GET', '/api/admin/download-retention');
          state.retention.days = retInfo.effective.retention_days;
          state.retention.count = retInfo.effective.max_count;
          state.retentionLoaded = true;
        } catch (_) { state.retentionLoaded = true; /* 使用默认值 */ }
        // 导入后重新拉自启状态
        try {
          const aInfo = await api('GET', '/api/admin/autostart');
          state.autostart = { ...state.autostart, ...aInfo };
          state.autostartLoaded = true;
        } catch (_) { state.autostartLoaded = true; /* 保持默认值 */ }
        // 重置所有 dirty（含分节 dirty）
        state.sectionsDirty.systems = false;
        state.sectionsDirty.openers = false;
        state.sectionsDirty.retention = false;
        state.sectionsDirty.autostart = false;
        state.loaded = true;
        recomputeDirty();
        renderApp();
        renderSearch();
        renderEditor();
        renderOpeners();
        renderRetention();
        renderAutoStart();
        maybeShowBanner(info);
        toast('配置导入成功！旧配置已自动备份。', 'ok');
      } catch (e) {
        toast('导入失败：' + e.message, 'err');
      }
    }

    // ----- 加载 -----
    //
    // 【v0.5 修复 #20】**只在首次 / 重置后**才重新 fetch。
    // 切 tab 再回来（navigate() 重新调用 renderConfig）时不再 fetch，
    // 直接用内存里的 state.systems —— 这是"切 tab 内容还在"的关键。
    // v0.8：openers 跟 systems 走相同的"首次 fetch + 切回不重 fetch"策略。
    // 【v0.x 修复 #B2】用 state.openersLoaded 标记避免每次切回本页都重复 GET
    // （旧实现用 state.openers.length 判断，空数组会被反复 fetch）。
    function ensureOpenersLoaded(cb) {
      if (state.openersLoaded) {
        renderOpeners();
        if (cb) cb();
        return;
      }
      api('GET', '/api/admin/openers').then(opInfo => {
        state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
        state.openersLoaded = true;
        renderOpeners();
        if (cb) cb();
      }).catch(() => { state.openers = []; state.openersLoaded = true; renderOpeners(); if (cb) cb(); });
    }
    function ensureRetentionLoaded(cb) {
      if (state.retentionLoaded) {
        renderRetention();
        if (cb) cb();
        return;
      }
      api('GET', '/api/admin/download-retention').then(retInfo => {
        state.retention.days = retInfo.effective.retention_days;
        state.retention.count = retInfo.effective.max_count;
        state.retentionLoaded = true;
        renderRetention();
        if (cb) cb();
      }).catch(() => {
        state.retention.days = 7;
        state.retention.count = 1000;
        state.retentionLoaded = true;
        renderRetention();
        if (cb) cb();
      });
    }
    // ensureAutoStartLoaded v1.0：自启状态加载（同 retention 模式：首次 fetch + 切回不重 fetch）。
    function ensureAutoStartLoaded(cb) {
      if (state.autostartLoaded) {
        renderAutoStart();
        if (cb) cb();
        return;
      }
      api('GET', '/api/admin/autostart').then(aInfo => {
        state.autostart = { ...state.autostart, ...aInfo };
        state.autostartLoaded = true;
        renderAutoStart();
        if (cb) cb();
      }).catch(() => {
        // GET 失败：保持默认空对象 + supported=false，UI 显示"不支持"提示
        state.autostart.supported = false;
        state.autostart.error = '无法读取自启状态';
        state.autostartLoaded = true;
        renderAutoStart();
        if (cb) cb();
      });
    }
    if (state.loaded && state.systems) {
      // 已加载过：直接 render，不再 fetch（除非用户点放弃改动）
      renderApp(); renderSearch(); renderEditor();
      ensureOpenersLoaded(() => {
        ensureRetentionLoaded(() => {
          ensureAutoStartLoaded(() => maybeShowBanner({ app: state.app }));
        });
      });
    } else {
      api('GET', '/api/admin/servers').then(info => {
        state.app = info.app; state.search = info.search;
        state.systems = JSON.parse(JSON.stringify(info.systems));
        state.loaded = true;
        renderApp(); renderSearch(); renderEditor();
        ensureOpenersLoaded(() => {
          ensureRetentionLoaded(() => {
            ensureAutoStartLoaded(() => maybeShowBanner(info));
          });
        });
      }).catch(e => toast('加载失败：' + e.message, 'err'));
    }
  }

  Kairo.pages.config = renderConfig;
  Kairo.state.routes.config = renderConfig;
  Kairo.state.routeNames.config = '系统配置';
})();
