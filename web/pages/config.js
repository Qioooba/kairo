/* ===== web/pages/config.js =====
 * 系统配置 — 可视化编辑器
 *
 * 数据模型：把 /api/config 返回的 systems 复制到本地 state，
 * 用户在页面里增删改后，点"保存"整体 PUT 回去。
 *
 * 顶部：根据 app.enable_free_file_browser 决定是否显示黄色警告 banner。
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, $, toast, validate, newSystem, newServer, newLogDir, kvTable } = OTB.core;
  const { api } = OTB.api;

  function renderConfig(view) {
    // state.systems 是当前正在编辑的树（与后端解耦）
    const state = { systems: [], dirty: false, app: null, search: null, freeBrowserEnabled: true };

    // 工具：标记 dirty
    const markDirty = () => {
      state.dirty = true;
      const btn = $('#cfg-save-btn');
      if (btn) {
        btn.disabled = false;
        btn.classList.add('btn-primary');
      }
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
    const editorCard = el('div', { class: 'card' });
    editorCard.appendChild(el('h3', { text: '业务系统 / 服务器 / 日志目录' }));
    editorCard.appendChild(el('div', { class: 'card-desc', text: '增删改后点右上角"保存"才生效；保存会原子改写 config.yaml，不需要重启。' }));

    const editorBody = el('div');
    editorCard.appendChild(editorBody);

    // 顶部操作条
    const btnAddSys = el('button', { class: 'btn', text: '+ 新增业务系统', onclick: () => { state.systems.push(newSystem()); markDirty(); renderEditor(); } });
    const btnSave = el('button', { class: 'btn', id: 'cfg-save-btn', text: '保存', onclick: doSave });
    const btnReset = el('button', { class: 'btn', text: '放弃改动', onclick: doReset });
    btnSave.disabled = true;
    const topBar = el('div', { class: 'btn-row', style: 'justify-content: flex-end; margin-bottom: 12px;' }, [btnAddSys, btnReset, btnSave]);
    view.appendChild(banner);
    view.appendChild(topBar);
    view.appendChild(appCard);
    view.appendChild(searchCard);
    view.appendChild(editorCard);

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
      const descInp = el('input', { type: 'text', value: sys.description || '', placeholder: '描述（可选）' });
      nameInp.addEventListener('input', () => { sys.name = nameInp.value; markDirty(); });
      descInp.addEventListener('input', () => { sys.description = descInp.value; markDirty(); });
      const btnUp = el('button', { class: 'btn btn-sm', text: '↑', title: '上移', onclick: () => { if (si > 0) { [state.systems[si-1], state.systems[si]] = [state.systems[si], state.systems[si-1]]; markDirty(); renderEditor(); } } });
      const btnDown = el('button', { class: 'btn btn-sm', text: '↓', title: '下移', onclick: () => { if (si < state.systems.length - 1) { [state.systems[si+1], state.systems[si]] = [state.systems[si], state.systems[si+1]]; markDirty(); renderEditor(); } } });
      const btnDup = el('button', { class: 'btn btn-sm', text: '复制', onclick: () => { state.systems.splice(si+1, 0, JSON.parse(JSON.stringify(sys))); markDirty(); renderEditor(); } });
      const btnDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除系统', onclick: () => { if (confirm('确认删除业务系统 “' + (sys.name || '(未命名)') + '” 及其全部服务器？')) { state.systems.splice(si, 1); markDirty(); renderEditor(); } } });
      btnUp.disabled = si === 0; btnDown.disabled = si === state.systems.length - 1;

      wrap.appendChild(el('div', { class: 'sys-head' }, [
        el('span', { class: 'sys-tag', text: '系统' }),
        el('div', { class: 'sys-name-input' }, nameInp),
        el('div', { class: 'sys-desc-input' }, descInp),
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
      const fields = [
        ['name', '服务器名（必填）', 'text'],
        ['host', 'IP / 主机', 'text'],
        ['port', 'SSH 端口', 'number'],
        ['username', 'SSH 用户名', 'text'],
        ['auth_type', '认证方式', 'select', [['password', 'password（密码）']]]
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
      const btnSrvDup = el('button', { class: 'btn btn-sm', text: '复制', onclick: () => { sys.servers.splice(sri+1, 0, JSON.parse(JSON.stringify(srv))); onEdit(); renderEditor(); } });
      const btnSrvDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除服务器', onclick: () => { if (confirm('确认删除服务器 “' + (srv.name || '(未命名)') + '” 及其日志目录？')) { sys.servers.splice(sri, 1); onEdit(); renderEditor(); } } });
      btnSrvUp.disabled = sri === 0; btnSrvDown.disabled = sri === sys.servers.length - 1;
      wrap.appendChild(el('div', { class: 'srv-actions' }, [btnSrvUp, btnSrvDown, btnSrvDup, btnSrvDel]));

      return wrap;
    }

    function renderDirBlock(srv, ld, ldi, onEdit) {
      const wrap = el('div', { class: 'dir-block' });
      const nameInp = el('input', { type: 'text', value: ld.name || '', placeholder: '目录别名（必填）' });
      const pathInp = el('input', { type: 'text', value: ld.path || '', placeholder: '远端绝对路径（必填）' });
      const encSel = el('select');
      [
        ['utf-8', 'utf-8'],
        ['gbk', 'gbk（远程是 GBK）']
      ].forEach(([v, t]) => {
        const o = el('option', { value: v, text: t });
        if ((ld.encoding || 'utf-8').toLowerCase() === v) o.selected = true;
        encSel.appendChild(o);
      });
      const patTa = el('textarea', { rows: '2', placeholder: '文件名规则，每行一条，例如：\nSystemOut*.log\n*.log' });
      patTa.value = (ld.patterns || []).join('\n');
      nameInp.addEventListener('input', () => { ld.name = nameInp.value; onEdit(); });
      pathInp.addEventListener('input', () => { ld.path = pathInp.value; onEdit(); });
      encSel.addEventListener('change', () => { ld.encoding = encSel.value; onEdit(); });
      patTa.addEventListener('input', () => {
        ld.patterns = patTa.value.split('\n').map(s => s.trim()).filter(Boolean);
        onEdit();
      });
      wrap.appendChild(el('div', { class: 'dir-fields' }, [
        el('label', null, [el('span', { class: 'lbl', text: '目录别名' }), nameInp]),
        el('label', null, [el('span', { class: 'lbl', text: '远端路径' }), pathInp]),
        el('label', null, [el('span', { class: 'lbl', text: '编码' }), encSel])
      ]));
      wrap.appendChild(el('label', { class: 'block' }, [
        el('span', { class: 'lbl', text: '文件名规则（每行一条）' }),
        patTa
      ]));
      const btnDirUp = el('button', { class: 'btn btn-sm', text: '↑', onclick: () => { if (ldi > 0) { [srv.log_dirs[ldi-1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi-1]]; onEdit(); renderEditor(); } } });
      const btnDirDown = el('button', { class: 'btn btn-sm', text: '↓', onclick: () => { if (ldi < srv.log_dirs.length - 1) { [srv.log_dirs[ldi+1], srv.log_dirs[ldi]] = [srv.log_dirs[ldi], srv.log_dirs[ldi+1]]; onEdit(); renderEditor(); } } });
      const btnDirDel = el('button', { class: 'btn btn-sm btn-danger', text: '删除目录', onclick: () => { if (confirm('确认删除日志目录 “' + (ld.name || ld.path || '(未命名)') + '” ？')) { srv.log_dirs.splice(ldi, 1); onEdit(); renderEditor(); } } });
      btnDirUp.disabled = ldi === 0; btnDirDown.disabled = ldi === srv.log_dirs.length - 1;
      wrap.appendChild(el('div', { class: 'dir-actions' }, [btnDirUp, btnDirDown, btnDirDel]));
      return wrap;
    }

    // ----- 保存 / 重置 -----
    async function doSave() {
      const err = validate(state.systems);
      if (err) { toast('保存失败：' + err, 'err'); return; }
      try {
        const r = await api('PUT', '/api/admin/servers', { systems: state.systems });
        toast('已保存到 ' + r.path, 'ok');
        state.dirty = false;
        const btn = $('#cfg-save-btn');
        if (btn) { btn.disabled = true; btn.classList.remove('btn-primary'); }
      } catch (e) {
        toast('保存失败：' + e.message, 'err');
      }
    }
    function doReset() {
      if (!state.dirty || confirm('放弃所有未保存的改动？')) {
        api('GET', '/api/admin/servers').then(info => {
          state.app = info.app; state.search = info.search;
          state.systems = JSON.parse(JSON.stringify(info.systems));
          state.dirty = false;
          const btn = $('#cfg-save-btn');
          if (btn) { btn.disabled = true; btn.classList.remove('btn-primary'); }
          renderApp(); renderSearch(); renderEditor();
          maybeShowBanner(info);
        });
      }
    }

    // ----- 加载 -----
    api('GET', '/api/admin/servers').then(info => {
      state.app = info.app; state.search = info.search;
      state.systems = JSON.parse(JSON.stringify(info.systems));
      renderApp(); renderSearch(); renderEditor();
      maybeShowBanner(info);
    }).catch(e => toast('加载失败：' + e.message, 'err'));
  }

  OTB.pages.config = renderConfig;
  OTB.state.routes.config = renderConfig;
  OTB.state.routeNames.config = '系统配置';
})();