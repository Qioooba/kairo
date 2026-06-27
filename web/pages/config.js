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
 * 修复后：state 挂到 OTB.state.configEditor，跨 re-render 存活；
 * 只有"还没加载过"或"用户点放弃改动"时才重新 fetch。
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, $, toast, validate, newSystem, newServer, newLogDir, kvTable } = OTB.core;
  const { api } = OTB.api;

  // 模块级 state：跨 tab 切换 / 跨 re-render 存活
  // 结构：{ systems, app, search, openers, retention, dirty, freeBrowserEnabled, loaded }
  // loaded=true 表示已经从服务器拉过；之后切回本页不再 fetch，避免覆盖未保存改动
  if (!OTB.state.configEditor) {
    OTB.state.configEditor = {
      systems: [],
      app: null,
      search: null,
      openers: [],
      retention: { retention_days: 7, max_count: 1000 },
      retentionLoaded: false,
      dirty: false,
      freeBrowserEnabled: true,
      loaded: false
    };
  }
  const state = OTB.state.configEditor;

  function renderConfig(view) {
    // 工具：标记 dirty + 通知 app.js 用于离开页面前确认
    // 【v0.5 #3】保存按钮现在有"顶部"+"底部"两个（共享 onclick handler 和 disabled 状态），
    // 通过 syncSaveBtns() 同步两个按钮。
    const markDirty = () => {
      state.dirty = true;
      syncSaveBtns();
      OTB.state.unsavedConfig = true;
    };

    // 同步两个保存按钮（顶部 #cfg-save-btn 和底部 .cfg-save-footer）的 disabled
    const syncSaveBtns = () => {
      const top = $('#cfg-save-btn');
      if (top) top.disabled = !state.dirty;
      document.querySelectorAll('.cfg-save-footer').forEach(b => { b.disabled = !state.dirty; });
    };

    // ---- 顶部警告 banner（项 14）：自由文件浏览器打开时 ----
    const bannerKey = 'free-file-browser-warning';
    const banner = el('div', { class: 'warn-banner', style: 'display:none' });
    banner.appendChild(el('div', { class: 'warn-banner-text' }, [
      el('strong', { text: '⚠ 文件浏览器任意路径下载已开启' }),
      el('div', { class: 'warn-banner-sub', text: '生产分发建议 config.yaml 设 app.enable_free_file_browser = false，或在 app.free_file_roots 里加白名单路径。' })
    ]));
    const closeBtn = el('button', { class: 'warn-banner-close', text: '×', title: '本会话不再显示', onclick: () => {
      OTB.state.dismiss(bannerKey);
      banner.style.display = 'none';
    }});
    banner.appendChild(closeBtn);

    // ---- 应用信息卡（只读展示） ----
    const appCard = el('div', { class: 'card' });
    const appKV = el('div');
    appCard.appendChild(el('h3', { text: '应用自身配置' }));
    appCard.appendChild(el('div', { class: 'card-desc', text: '应用名、端口、目录等由 config.yaml 管理，本页面只管服务器和日志目录。' }));
    appCard.appendChild(appKV);

    // ---- 搜索参数卡（只读展示） ----
    const searchCard = el('div', { class: 'card' });
    const searchKV = el('div');
    searchCard.appendChild(el('h3', { text: '搜索默认参数' }));
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
    const btnSaveFooter = el('button', { class: 'btn btn-primary cfg-save-footer', text: '保存', onclick: doSave });
    const btnResetFooter = el('button', { class: 'btn cfg-save-footer', text: '放弃改动', onclick: doReset });
    btnSaveFooter.disabled = true;
    btnResetFooter.disabled = true; // 没有改动时也禁用（避免误触重新 fetch）
    const footerBar = el('div', { class: 'btn-row cfg-save-footer-bar', style: 'justify-content: flex-end; margin-top: 16px; padding-top: 12px; border-top: 1px dashed var(--line);' }, [btnResetFooter, btnSaveFooter]);

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
          markDirty();
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

    view.appendChild(banner);
    view.appendChild(topBar);
    view.appendChild(appCard);
    view.appendChild(searchCard);
    view.appendChild(editorCard);
    view.appendChild(openersCard);
    view.appendChild(retentionCard);
    view.appendChild(footerBar);

    function maybeShowBanner(info) {
      // 仅当 free_file_browser === true（且 free_file_roots 为空，即"自由模式"）时显示
      const app = (info && info.app) || {};
      const enabled = app.enable_free_file_browser === undefined || app.enable_free_file_browser === true;
      const roots = Array.isArray(app.free_file_roots) ? app.free_file_roots : [];
      // 只在"自由模式"下提醒（白名单模式已经有 renderFiles 顶部提示，不重复）
      state.freeBrowserEnabled = enabled && roots.length === 0;
      if (state.freeBrowserEnabled && !OTB.state.isDismissed(bannerKey)) {
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
      const btnDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除系统', onclick: () => { if (confirm('确认删除业务系统 “' + (sys.name || '(未命名)') + '” 及其全部服务器？')) { state.systems.splice(si, 1); markDirty(); renderEditor(); } } });
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
          inp.addEventListener('input', () => {
            if (type === 'number') {
              const n = parseInt(inp.value, 10);
              srv[key] = isNaN(n) ? 0 : n;
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
        text: '📋 复制服务器（含日志目录）',
        title: '复制这台服务器（含所有日志目录）到下一行。适合 app01 → app02 改名后复用。复制后会重名（自动加 -copy 后缀提示）。',
        onclick: () => {
          const copy = JSON.parse(JSON.stringify(srv));
          if (copy.name && !copy.name.endsWith('-copy')) copy.name = copy.name + '-copy';
          sys.servers.splice(sri+1, 0, copy);
          onEdit(); renderEditor();
        }
      });
      const btnSrvDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除服务器', onclick: () => { if (confirm('确认删除服务器 “' + (srv.name || '(未命名)') + '” 及其日志目录？')) { sys.servers.splice(sri, 1); onEdit(); renderEditor(); } } });
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
      const btnDirDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除目录', onclick: () => { if (confirm('确认删除日志目录 "' + (ld.name || ld.path || '(未命名)') + '" ？')) { srv.log_dirs.splice(ldi, 1); onEdit(); renderEditor(); } } });
      btnDirUp.disabled = ldi === 0; btnDirDown.disabled = ldi === srv.log_dirs.length - 1;
      wrap.appendChild(el('div', { class: 'dir-actions' }, [btnDirDup, btnDirUp, btnDirDown, btnDirDel]));
      return wrap;
    }

    function inferIconFromPath(path, name) {
      const s = (path + ' ' + name).toLowerCase();
      if (s.includes('code') || s.includes('vscode') || s.includes('vs ')) return '💙';
      if (s.includes('notepad') || s.includes('npp')) return '📋';
      if (s.includes('idea') || s.includes('intellij')) return '🧡';
      if (s.includes('vim') || s.includes('nvim')) return '🖤';
      if (s.includes('sublime')) return '🟣';
      if (s.includes('terminal') || s.includes('cmd') || s.includes('powershell') || s.includes('iterm')) return '⌨️';
      if (s.includes('excel') || s.includes('xlsx')) return '📊';
      if (s.includes('word') || s.includes('docx')) return '📄';
      return '📎';
    }

    async function saveOpenerRow(op) {
      if (!op.name || !op.name.trim()) { toast('保存失败：打开器名称不能为空', 'err'); return; }
      if (!op.path || !op.path.trim()) { toast('保存失败：打开器路径不能为空', 'err'); return; }
      try {
        await api('PUT', '/api/admin/openers', { openers: state.openers });
        state.openersDirty = false;
        if (!state.dirty) { state.dirty = false; }
        syncSaveBtns();
        toast('已保存', 'ok');
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
        if (!op.icon) op.icon = inferIconFromPath(op.path || '', op.name || '');
        const iconSpan = el('span', { style: 'font-size:20px; width:28px; text-align:center; flex-shrink:0; cursor:default;', text: op.icon, title: '图标根据路径自动推断' });

        const row = el('div', { class: 'opener-row', style: 'display:flex; gap:8px; margin-bottom:8px; align-items:center;' });
        const nameInp = el('input', { type: 'text', value: op.name || '', placeholder: '名称（如 Notepad++）', style: 'flex:1;' });

        const pathWrap = el('div', { style: 'flex:2; display:flex; gap:4px; position:relative;' });
        const pathInp = el('input', { type: 'text', value: op.path || '', placeholder: '可执行文件路径（如 C:\\Windows\\notepad.exe 或 /usr/bin/code）', style: 'flex:1;' });
        const btnBrowse = el('button', { class: 'btn btn-sm', text: '📂', title: '选择文件', type: 'button', style: 'flex-shrink:0;' });
        btnBrowse.addEventListener('click', async () => {
          try {
            const r = await api('POST', '/api/choose-file');
            if (r && r.path) {
              pathInp.value = r.path;
              op.path = r.path;
              markDirty();
              updateIcon();
            }
          } catch (e) { /* ignore */ }
        });
        pathWrap.appendChild(pathInp);
        pathWrap.appendChild(btnBrowse);

        const btnSave = el('button', { class: 'btn btn-sm', text: '💾 保存', type: 'button', onclick: () => saveOpenerRow(op) });
        const btnDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除', type: 'button', onclick: () => {
          if (confirm('确认删除打开器 "' + (op.name || '(未命名)') + '"？')) {
            state.openers.splice(idx, 1);
            markDirty();
            renderOpeners();
          }
        }});

        function updateIcon() {
          op.icon = inferIconFromPath(op.path || '', op.name || '');
          iconSpan.textContent = op.icon;
        }

        nameInp.addEventListener('input', () => { op.name = nameInp.value; markDirty(); updateIcon(); });
        pathInp.addEventListener('input', () => { op.path = pathInp.value; markDirty(); updateIcon(); });

        row.appendChild(iconSpan);
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
        markDirty();
      });
      daysWrap.appendChild(daysInp);
      daysWrap.appendChild(el('span', { class: 'card-desc', style: 'font-size:12px;', text: '0 = 不按时间自动清理；默认 7 天' }));

      const countWrap = el('label', { style: 'display:flex; flex-direction:column; gap:4px; flex:1; min-width:200px;' });
      countWrap.appendChild(el('span', { class: 'lbl', text: '最大记录数' }));
      const countInp = el('input', { type: 'number', min: '0', value: String(countVal), placeholder: '1000' });
      countInp.addEventListener('input', () => {
        const n = parseInt(countInp.value, 10);
        state.retention.count = isNaN(n) ? 0 : n;
        markDirty();
      });
      countWrap.appendChild(countInp);
      countWrap.appendChild(el('span', { class: 'card-desc', style: 'font-size:12px;', text: '0 = 不限制数量；默认 1000 条' }));

      row.appendChild(daysWrap);
      row.appendChild(countWrap);
      retentionBody.appendChild(row);
      retentionBody.appendChild(el('div', { class: 'card-desc', style: 'margin-top:8px;', text: '提示：系统启动时自动清理一次；每次下载完成后也会触发检查。' }));
    }

    // ----- 保存 / 重置 -----
    async function doSave() {
      const err = validate(state.systems);
      if (err) { toast('保存失败：' + err, 'err'); return; }
      try {
        // 先存 systems（用现有 admin/servers 接口）
        const r = await api('PUT', '/api/admin/servers', { systems: state.systems });
        // 再存 openers（独立接口，因为 admin/servers 不接收 app 段）
        // 即使 openers 失败，也要把上面的 systems 成功反馈给用户——
        // 但要明确告诉用户"openers 部分失败"（不能吞错）。
        let openerErr = null;
        try {
          await api('PUT', '/api/admin/openers', { openers: state.openers || [] });
        } catch (e) {
          openerErr = e;
        }
        // 存下载清理策略
        let retentionErr = null;
        try {
          await api('PUT', '/api/admin/download-retention', {
            download_retention_days: state.retention.days,
            download_max_count: state.retention.count,
          });
        } catch (e) {
          retentionErr = e;
        }
        // P0-5 修复：保存成功后重新 GET 后端，用后端规范化后的数据覆盖前端 state，
        // 确保 encoding 归一等后端处理被前端确认（比如 gbk→gbk，utf-8→utf-8）。
        // 同时 toast 里显示"后端确认"让用户知道写盘成功。
        const info = await api('GET', '/api/admin/servers');
        state.app = info.app;
        state.search = info.search;
        state.systems = JSON.parse(JSON.stringify(info.systems));
        // 拉 openers 回填（GET /api/admin/openers，避免依赖 info.app 的字段稳定性）
        try {
          const opInfo = await api('GET', '/api/admin/openers');
          state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
        } catch (e) { /* 拉失败不影响主要保存提示 */ }
        // 拉 retention 配置回填
        try {
          const retInfo = await api('GET', '/api/admin/download-retention');
          state.retention.days = retInfo.effective.retention_days;
          state.retention.count = retInfo.effective.max_count;
        } catch (e) { /* 拉失败不影响 */ }
        state.dirty = false;
        state.loaded = true;
        OTB.state.unsavedConfig = false;
        syncSaveBtns();
        renderApp();
        renderSearch();
        renderEditor();
        renderOpeners();
        renderRetention();
        let errMsg = '';
        if (openerErr) errMsg += '打开器保存失败：' + openerErr.message + '；';
        if (retentionErr) errMsg += '清理策略保存失败：' + retentionErr.message + '；';
        if (errMsg) {
          toast('systems 已保存，但' + errMsg, 'err');
        } else {
          toast('已保存并后端确认：' + r.path, 'ok');
        }
      } catch (e) {
        toast('保存失败：' + e.message, 'err');
      }
    }
    function doReset() {
      if (!state.dirty || confirm('放弃所有未保存的改动？')) {
        api('GET', '/api/admin/servers').then(info => {
          state.app = info.app; state.search = info.search;
          state.systems = JSON.parse(JSON.stringify(info.systems));
          // 同时重拉 openers
          api('GET', '/api/admin/openers').then(opInfo => {
            state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
            renderOpeners();
          }).catch(() => { state.openers = []; renderOpeners(); });
          // 同时重拉 retention 配置
          api('GET', '/api/admin/download-retention').then(retInfo => {
            state.retention.days = retInfo.effective.retention_days;
            state.retention.count = retInfo.effective.max_count;
            renderRetention();
          }).catch(() => { renderRetention(); });
          state.dirty = false;
          state.loaded = true;
          OTB.state.unsavedConfig = false;
          syncSaveBtns();
          renderApp(); renderSearch(); renderEditor(); renderOpeners(); renderRetention();
          maybeShowBanner(info);
        }).catch(e => toast('加载失败：' + e.message, 'err'));
      }
    }

    async function doExport() {
      try {
        const resp = await fetch('/api/config/export', { credentials: 'same-origin' });
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
        toast('配置已导出为 config.yaml', 'ok');
      } catch (e) {
        toast('导出失败：' + e.message, 'err');
      }
    }

    function doImport() {
      if (state.dirty && !confirm('当前有未保存的改动，导入配置会丢弃这些改动。继续吗？')) {
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
        reader.onload = () => {
          const yamlText = reader.result;
          if (!yamlText || !String(yamlText).trim()) {
            toast('文件内容为空', 'err');
            return;
          }
          const preview = String(yamlText).substring(0, 200).replace(/</g, '&lt;');
          const confirmMsg = '确定要导入此配置文件吗？\n\n文件：' + file.name + ' (' + file.size + ' 字节)\n\n⚠ 警告：导入后将完全覆盖现有配置，旧配置会自动备份为 .bak 文件。\n\n文件预览（前200字符）：\n' + preview;
          if (!confirm(confirmMsg)) {
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
        } catch (_) { state.openers = []; }
        try {
          const retInfo = await api('GET', '/api/admin/download-retention');
          state.retention.days = retInfo.effective.retention_days;
          state.retention.count = retInfo.effective.max_count;
        } catch (_) { /* 使用默认值 */ }
        state.dirty = false;
        state.loaded = true;
        OTB.state.unsavedConfig = false;
        syncSaveBtns();
        renderApp();
        renderSearch();
        renderEditor();
        renderOpeners();
        renderRetention();
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
    function ensureOpenersLoaded(cb) {
      // 已加载过：直接 renderOpeners + cb（cb 通常是 maybeShowBanner）
      if (state.openers && state.openers.length) {
        renderOpeners();
        if (cb) cb();
        return;
      }
      api('GET', '/api/admin/openers').then(opInfo => {
        state.openers = Array.isArray(opInfo.openers) ? opInfo.openers : [];
        renderOpeners();
        if (cb) cb();
      }).catch(() => { state.openers = []; renderOpeners(); if (cb) cb(); });
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
    if (state.loaded && state.systems) {
      // 已加载过：直接 render，不再 fetch（除非用户点放弃改动）
      renderApp(); renderSearch(); renderEditor();
      ensureOpenersLoaded(() => {
        ensureRetentionLoaded(() => maybeShowBanner({ app: state.app }));
      });
    } else {
      api('GET', '/api/admin/servers').then(info => {
        state.app = info.app; state.search = info.search;
        state.systems = JSON.parse(JSON.stringify(info.systems));
        state.loaded = true;
        renderApp(); renderSearch(); renderEditor();
        ensureOpenersLoaded(() => {
          ensureRetentionLoaded(() => maybeShowBanner(info));
        });
      }).catch(e => toast('加载失败：' + e.message, 'err'));
    }
  }

  OTB.pages.config = renderConfig;
  OTB.state.routes.config = renderConfig;
  OTB.state.routeNames.config = '系统配置';
})();
