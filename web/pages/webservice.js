/* ===== web/pages/webservice.js =====
 * WebService 调试中心（v0.12）
 *
 * 一个轻量 SoapUI，面向老 Java / WebSphere / XFire / SOAP WebService 场景。
 *
 * 布局：
 *   左侧 sidebar：4 个 tab（WSDL 项目 / 模板 / 历史 / Mock）+ 搜索
 *   右侧 main：
 *     - 顶部操作区（导入 WSDL URL / 上传文件 / 创建 Mock 等）
 *     - 中间请求编辑器（endpoint / SOAPAction / encoding / timeout / headers / body）
 *     - 底部响应区（status / elapsed / headers / body / 建议关键词）
 *
 * 数据持久化全在后端：data/wsdl_projects.json / soap_templates.json /
 * soap_history.json / soap_mocks.json / soap_mock_records.json
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard, confirmDialog, escapeHtml, notify } = Kairo.core;
  const { api, postJSON, getJSON, deleteJSON, putJSON } = Kairo.api;

  // 当前页面状态（路由内闭包，不污染全局）
  const state = {
    activeTab: 'wsdl',          // wsdl / templates / history / mocks
    search: '',                 // 左侧搜索关键字
    wsdlProjects: [],           // 所有已保存的 WSDL 项目
    templates: [],
    history: [],
    mocks: [],
    mockRecords: [],
    currentProject: null,       // 当前选中的 WSDL 项目（null 表示未选）
    currentOperation: null,     // 当前选中的 operation
    currentTemplate: null,      // 当前编辑的模板（null 表示新建）
    currentMock: null,
    response: null,             // 最近一次响应
    keywords: [],
    opDetailExpanded: false,    // operation 参数详情是否展开（默认收起）
    opListCollapsed: false,     // operation 列表是否折叠（operation 多时可手动收起）
    // 各 tab 的编辑状态快照，切 tab 时保存/恢复，避免互相覆盖
    draftSnapshots: {},
    // 请求编辑器草稿：所有字段从 state 读取、oninput 回写 state，
    // 避免切 tab 重渲染丢失用户正在编辑的内容（不再依赖 setTimeout 填值）。
    draft: {
      endpoint: '',
      soapAction: '',
      soapVersion: '1.1',
      encoding: 'UTF-8',
      timeoutMs: 30000,
      headers: '',
      body: '',
      bodyDirty: false, // 用户手动编辑过 body 后置 true，切 operation 时不自动覆盖
      saveHistory: true,
    },
    requestInFlight: false, // 发送请求进行中标志，防止重复提交
  };

  // ---------- 工具 ----------

  // 自定义 prompt 弹层（替代 window.prompt，兼容 Electron/WebView 等禁用 prompt 的环境）
  // opts: { title, placeholder, defaultValue }
  // 返回 Promise<string|null>
  function promptDialog(msg, opts) {
    opts = opts || {};
    if (typeof document === 'undefined' || !document.body) {
      var v = window.prompt ? window.prompt(msg, opts.defaultValue || '') : null;
      return Promise.resolve(v);
    }
    return new Promise(function (resolve) {
      var overlay = el('div', { class: 'kairo-dialog-overlay' });
      var dialog = el('div', { class: 'kairo-dialog' });
      var titleEl = el('div', { class: 'kairo-dialog-title', text: opts.title || '请输入' });
      var bodyEl = el('div', { class: 'kairo-dialog-body' });
      if (msg) {
        var p = el('p', { text: msg });
        bodyEl.appendChild(p);
      }
      var input = el('input', {
        type: 'text', class: 'form-control',
        placeholder: opts.placeholder || '',
        value: opts.defaultValue || ''
      });
      bodyEl.appendChild(input);
      var actions = el('div', { class: 'kairo-dialog-actions' });
      var cancelBtn = el('button', {
        class: 'btn', type: 'button', text: '取消',
        onclick: function () { close(null); }
      });
      var okBtn = el('button', {
        class: 'btn btn-primary', type: 'button', text: '确定',
        onclick: function () { close(input.value); }
      });
      var settled = false;
      function close(v) {
        if (settled) return;
        settled = true;
        document.removeEventListener('keydown', onKeyDown, true);
        try { overlay.remove(); } catch (e) { /* ignore */ }
        resolve(v);
      }
      function onKeyDown(ev) {
        if (ev.key === 'Escape') { ev.preventDefault(); close(null); }
        else if (ev.key === 'Enter') { ev.preventDefault(); close(input.value); }
      }
      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      dialog.appendChild(titleEl);
      dialog.appendChild(bodyEl);
      dialog.appendChild(actions);
      overlay.appendChild(dialog);
      overlay.addEventListener('click', function (ev) {
        if (ev.target === overlay) close(null);
      });
      document.addEventListener('keydown', onKeyDown, true);
      document.body.appendChild(overlay);
      setTimeout(function () { input && input.focus && input.focus(); }, 0);
    });
  }

  function fmtBytes(n) {
    if (!n || n < 0) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
  }

  function fmtTime(rfc3339) {
    if (!rfc3339) return '';
    // 直接显示 UTC ISO；前端不强制转时区，避免误读
    return rfc3339.replace('T', ' ').replace('Z', ' UTC');
  }

  function shortId(id) {
    if (!id) return '';
    return id.length > 12 ? id.slice(0, 12) : id;
  }

  function operationKey(op) {
    if (!op) return '';
    return [op.name || '', op.endpoint || '', op.soap_version || '1.1', op.soap_action || ''].join('\u0001');
  }

  function syncXMLDeclaration(body, encoding) {
    if (!body) return body || '';
    return body.replace(/(<\?xml\b[^>]*\bencoding\s*=\s*["'])[^"']+(["'])/i, '$1' + (encoding || 'UTF-8') + '$2');
  }

  function syncSOAPEnvelopeVersion(body, version) {
    if (!body) return body || '';
    const ns = version === '1.2'
      ? 'http://www.w3.org/2003/05/soap-envelope'
      : 'http://schemas.xmlsoap.org/soap/envelope/';
    return body.replace(/http:\/\/schemas\.xmlsoap\.org\/soap\/envelope\/?|http:\/\/www\.w3\.org\/2003\/05\/soap-envelope\/?/g, ns);
  }

  // 兼容用户常见的三种粘贴习惯：逐行 Key: Value、JSON 对象、curl -H/--header。
  function parseHeadersText(raw) {
    const text = String(raw || '').trim();
    if (!text) return { headers: {}, invalid: [] };
    if (text.charAt(0) === '{') {
      try {
        const obj = JSON.parse(text);
        if (!obj || Array.isArray(obj) || typeof obj !== 'object') throw new Error('not object');
        const headers = {};
        Object.keys(obj).forEach(k => { headers[k] = String(obj[k]); });
        return { headers, invalid: [] };
      } catch (e) {
        return { headers: {}, invalid: ['JSON Header 不是合法对象'] };
      }
    }
    const headers = {};
    const invalid = [];
    text.split(/\r?\n/).forEach(function (original) {
      let line = original.trim();
      if (!line || line.charAt(0) === '#') return;
      line = line.replace(/^--header\s+/i, '').replace(/^-H\s+/i, '');
      if ((line.charAt(0) === '"' && line.charAt(line.length - 1) === '"') ||
          (line.charAt(0) === "'" && line.charAt(line.length - 1) === "'")) {
        line = line.slice(1, -1);
      }
      const idx = line.indexOf(':');
      if (idx <= 0) { invalid.push(original); return; }
      const k = line.slice(0, idx).trim();
      const v = line.slice(idx + 1).trim();
      if (!k || /[\s\r\n:]/.test(k)) { invalid.push(original); return; }
      headers[k] = v;
    });
    return { headers, invalid };
  }

  function highlight(text) {
    // 简易 XML 高亮：标签名 / 属性名 / 属性值 / 注释
    if (!text) return '';
    const esc = escapeHtml(text);
    return esc.replace(/(&lt;\/?)([\w:.-]+)/g, '$1<span style="color:var(--primary)">$2</span>')
      .replace(/([\w:.-]+)=(&quot;.*?&quot;)/g, '<span style="color:var(--warn)">$1</span>=<span style="color:var(--success)">$2</span>');
  }

  // ---------- 工具：textarea 自动撑高 ----------
  // - 跟 envelope/header 整体长度走：高度 = 内容 scrollHeight（封顶视口相关上限）
  // - oninput 时把 height 重置为 auto 再设成 scrollHeight，避免一直累积
  // - 封顶 MAX_H，超过后允许纵向滚动（不撑爆页面）
  // - 模板恢复 / 外部 value 变化：MutationObserver 监听 value attribute 变化
  // - 切 tab 重渲染后也要重新触发一次（因为 DOM 刚挂载、初始值可能没生效）
  function textareaMaxH() {
    // 视口高度的 60%，下限 400，上限 800
    const vh = window.innerHeight || 800;
    return Math.max(400, Math.min(800, Math.floor(vh * 0.6)));
  }
  function autoResizeTextarea(ta) {
    if (!ta) return;
    ta.style.height = 'auto';
    const max = textareaMaxH();
    const h = Math.min(ta.scrollHeight, max);
    ta.style.height = h + 'px';
    ta.style.overflowY = ta.scrollHeight > max ? 'auto' : 'hidden';
  }
  // 全局 resize 监听：只注册一次，迭代所有已注册的 textarea
  // （避免每个 textarea 都挂一个 window resize 监听导致内存泄漏 —— M-2 修复）
  var _autoResizeTextareas = new Set();
  var _autoResizeInited = false;
  function _initGlobalAutoResize() {
    if (_autoResizeInited) return;
    _autoResizeInited = true;
    window.addEventListener('resize', function () {
      _autoResizeTextareas.forEach(function (ta) {
        // 顺带清理已从 DOM 移除的 textarea，避免 Set 无限增长
        if (ta.isConnected) {
          autoResizeTextarea(ta);
        } else {
          _autoResizeTextareas.delete(ta);
        }
      });
    }, { passive: true });
  }
  // 挂监听：oninput + 下一帧 + 监听外部 value 变化（如模板恢复直接赋值）
  function bindAutoResize(ta) {
    if (!ta) return;
    _initGlobalAutoResize();
    _autoResizeTextareas.add(ta);
    ta.addEventListener('input', () => autoResizeTextarea(ta));
    // 下一帧触发（确保 DOM 已挂载、scrollHeight 准）
    requestAnimationFrame(() => autoResizeTextarea(ta));
    // 监听 value attribute 变化（脚本赋值 ta.value = ... 不触发 input 事件）
    // __mavis_observed 守卫防止同一 textarea 重复挂多个 observer（M-3：限制 observer 数量）
    if (typeof MutationObserver === 'function' && !ta.__mavis_observed) {
      ta.__mavis_observed = true;
      const obs = new MutationObserver(() => autoResizeTextarea(ta));
      obs.observe(ta, { attributes: true, attributeFilter: ['value'] });
    }
    // window resize 由全局监听统一处理（见 _initGlobalAutoResize），不再逐 textarea 挂监听
  }

  // ---------- 左侧 Sidebar ----------

  function renderSidebar() {
    const sidebar = el('div', { class: 'svc-sidebar' });

    // tab bar
    const tabBar = el('div', { class: 'svc-tab-bar' });
    const tabs = [
      { id: 'wsdl', label: 'WSDL 项目', count: state.wsdlProjects.length },
      { id: 'templates', label: '模板', count: state.templates.length },
      { id: 'history', label: '历史', count: state.history.length },
      { id: 'mocks', label: 'Mock', count: state.mocks.length },
    ];
    tabs.forEach(t => {
      const btn = el('button', {
        class: 'svc-tab-btn' + (state.activeTab === t.id ? ' active' : ''),
        onclick: () => switchTab(t.id)
      }, [
        el('span', { text: t.label }),
        el('span', { class: 'svc-tab-count', text: String(t.count) }),
      ]);
      tabBar.appendChild(btn);
    });
    sidebar.appendChild(tabBar);

    // 搜索框
    const searchInp = el('input', {
      type: 'text', class: 'svc-search', placeholder: '搜索 ' + state.activeTab + '...',
      value: state.search,
      oninput: (e) => { state.search = e.target.value; rerenderSidebarList(); }
    });
    sidebar.appendChild(searchInp);

    // 列表容器（rerenderSidebarList 只刷新这部分）
    const listWrap = el('div', { class: 'svc-list-wrap' });
    sidebar.appendChild(listWrap);
    rerenderSidebarList(listWrap);

    // 底部操作按钮（按当前 tab 显示）
    const ops = el('div', { class: 'svc-sidebar-ops' });
    if (state.activeTab === 'wsdl') {
      ops.appendChild(el('button', { class: 'btn', text: '+ 导入 URL', onclick: openImportURLDialog }));
      ops.appendChild(el('button', { class: 'btn', text: '+ 上传文件', onclick: triggerUploadFile }));
      const fileInp = el('input', { type: 'file', id: 'svc-upload-inp', accept: '.wsdl,.xsd,.xml', multiple: true, style: 'display:none', onchange: handleUploadFile });
      ops.appendChild(fileInp);
    } else if (state.activeTab === 'templates') {
      ops.appendChild(el('button', { class: 'btn', text: '+ 新模板', onclick: () => newTemplate() }));
    } else if (state.activeTab === 'history') {
      ops.appendChild(el('button', { class: 'btn', text: '一键清空', onclick: clearHistory }));
    } else if (state.activeTab === 'mocks') {
      ops.appendChild(el('button', { class: 'btn', text: '+ 新 Mock', onclick: () => newMock() }));
      ops.appendChild(el('button', { class: 'btn', text: '查看请求记录', onclick: viewMockRecords }));
    }
    sidebar.appendChild(ops);

    return sidebar;
  }

  function rerenderSidebar() {
    const sidebar = document.querySelector('.svc-sidebar');
    if (!sidebar) return;
    const newSidebar = renderSidebar();
    sidebar.replaceWith(newSidebar);
  }

  function rerenderSidebarList(optWrap) {
    const wrap = optWrap || document.querySelector('.svc-list-wrap');
    if (!wrap) return;
    wrap.replaceChildren();
    const kw = state.search.trim().toLowerCase();

    if (state.activeTab === 'wsdl') {
      let list = state.wsdlProjects;
      if (kw) {
        list = list.filter(p =>
          (p.name || '').toLowerCase().includes(kw) ||
          (p.target_ns || '').toLowerCase().includes(kw) ||
          (p.parse_error || '').toLowerCase().includes(kw));
      }
      if (list.length === 0) {
        // 区分两种状态：完全没项目 vs 搜了关键字没匹配
        const empty = el('div', { class: 'svc-empty' });
        if (state.wsdlProjects.length === 0) {
          empty.textContent = '暂无 WSDL 项目，点下方按钮导入';
        } else {
          empty.textContent = '无匹配项（搜了 "' + state.search + '"）';
        }
        wrap.appendChild(empty);
        return;
      }
      list.forEach(p => {
        const item = el('div', {
          class: 'svc-item' + (state.currentProject && state.currentProject.id === p.id ? ' active' : ''),
          onclick: () => selectProject(p.id)
        }, [
            el('div', { class: 'svc-item-title', text: p.name || '(未命名)' }),
            el('div', { class: 'svc-item-sub', text: p.target_ns || '无 namespace' }),
            el('div', { class: 'svc-item-meta' }, [
              el('span', { text: (p.operations || []).length + ' ops' }),
              el('span', { text: fmtTime(p.updated_at) }),
            ]),
        ]);
        // 删除按钮
        const del = el('button', { class: 'svc-item-del', text: '×', title: '删除', onclick: (e) => { e.stopPropagation(); deleteProject(p.id); } });
        item.appendChild(del);
        wrap.appendChild(item);
      });
    } else if (state.activeTab === 'templates') {
      let list = state.templates;
      if (kw) {
        list = list.filter(t =>
          (t.name || '').toLowerCase().includes(kw) ||
          (t.group || '').toLowerCase().includes(kw) ||
          (t.operation || '').toLowerCase().includes(kw));
      }
      if (list.length === 0) {
        const empty = el('div', { class: 'svc-empty' });
        if (state.templates.length === 0) {
          empty.textContent = '暂无模板，发送请求后可保存为模板';
        } else {
          empty.textContent = '无匹配项（搜了 "' + state.search + '"）';
        }
        wrap.appendChild(empty);
        return;
      }
      list.forEach(t => {
        const item = el('div', {
          class: 'svc-item' + (state.currentTemplate && state.currentTemplate.id === t.id ? ' active' : ''),
          onclick: () => loadTemplate(t.id)
        }, [
          el('div', { class: 'svc-item-title', text: t.name }),
          el('div', { class: 'svc-item-sub', text: t.group || '默认分组' }),
          el('div', { class: 'svc-item-meta' }, [
            el('span', { text: t.operation || '' }),
            el('span', { text: fmtTime(t.updated_at) }),
          ]),
        ]);
        const del = el('button', { class: 'svc-item-del', text: '×', title: '删除', onclick: (e) => { e.stopPropagation(); deleteTemplate(t.id); } });
        item.appendChild(del);
        wrap.appendChild(item);
      });
    } else if (state.activeTab === 'history') {
      let list = state.history;
      if (kw) {
        list = list.filter(h =>
          (h.endpoint || '').toLowerCase().includes(kw) ||
          (h.operation || '').toLowerCase().includes(kw) ||
          (h.soap_action || '').toLowerCase().includes(kw) ||
          (h.error || '').toLowerCase().includes(kw));
      }
      if (list.length === 0) {
        const empty = el('div', { class: 'svc-empty' });
        if (state.history.length === 0) {
          empty.textContent = '暂无历史，发送请求后自动记录';
        } else {
          empty.textContent = '无匹配项（搜了 "' + state.search + '"）';
        }
        wrap.appendChild(empty);
        return;
      }
      list.forEach(h => {
        const okCls = h.success ? 'ok' : 'fail';
        const item = el('div', {
          class: 'svc-item' + (state.response && state.response._historyId === h.id ? ' active' : ''),
          onclick: () => viewHistory(h.id)
        }, [
          el('div', { class: 'svc-item-title' }, [
            el('span', { class: 'svc-dot ' + okCls }),
            el('span', { text: h.operation || '(无操作名)' }),
          ]),
          el('div', { class: 'svc-item-sub', text: h.endpoint }),
          el('div', { class: 'svc-item-meta' }, [
            el('span', { text: h.status_code ? (h.status_code + '') : (h.success ? 'OK' : 'FAIL') }),
            el('span', { text: h.duration_ms != null ? (h.duration_ms + 'ms') : '' }),
            el('span', { text: fmtTime(h.time) }),
          ]),
        ]);
        wrap.appendChild(item);
      });
    } else if (state.activeTab === 'mocks') {
      let list = state.mocks;
      if (kw) {
        list = list.filter(m =>
          (m.name || '').toLowerCase().includes(kw) ||
          (m.path || '').toLowerCase().includes(kw) ||
          (m.operation || '').toLowerCase().includes(kw));
      }
      if (list.length === 0) {
        const empty = el('div', { class: 'svc-empty' });
        if (state.mocks.length === 0) {
          empty.textContent = '暂无 Mock，点下方按钮新建';
        } else {
          empty.textContent = '无匹配项（搜了 "' + state.search + '"）';
        }
        wrap.appendChild(empty);
        return;
      }
      list.forEach(m => {
        const item = el('div', {
          class: 'svc-item' + (state.currentMock && state.currentMock.id === m.id ? ' active' : ''),
          onclick: () => editMock(m.id)
        }, [
          el('div', { class: 'svc-item-title' }, [
            el('span', { class: 'svc-dot ' + (m.enabled ? 'ok' : 'idle') }),
            el('span', { text: m.name }),
          ]),
          el('div', { class: 'svc-item-sub', text: m.path }),
          el('div', { class: 'svc-item-meta' }, [
            el('span', { text: m.status_code || 200 }),
            el('span', { text: m.delay_ms ? (m.delay_ms + 'ms delay') : 'no delay' }),
          ]),
        ]);
        const del = el('button', { class: 'svc-item-del', text: '×', title: '删除', onclick: (e) => { e.stopPropagation(); deleteMock(m.id); } });
        item.appendChild(del);
        wrap.appendChild(item);
      });
    }
  }

  // ---------- 主区 ----------

  function renderMain() {
    const main = el('div', { class: 'svc-main' });

    // 1) 顶部：WSDL 项目概览 + operation 列表（仅 wsdl tab 显示）
    if (state.activeTab === 'wsdl') {
      main.appendChild(renderWSDLOpsArea());
    }

    // 2) 请求编辑区（wsdl / templates / history 都用同一个）
    if (state.activeTab !== 'mocks') {
      main.appendChild(renderRequestEditor());
    } else {
      main.appendChild(renderMockEditor());
    }

    // 3) 响应区（wsdl / templates / history 显示）
    if (state.activeTab !== 'mocks') {
      main.appendChild(renderResponseArea());
    }

    return main;
  }

  function rerenderMain() {
    const main = document.querySelector('.svc-main');
    if (!main) return;
    const newMain = renderMain();
    main.replaceWith(newMain);
  }

  // ---------- WSDL operation 列表 ----------

  function renderWSDLOpsArea() {
    const card = el('div', { class: 'card svc-ops-card' });
    if (!state.currentProject) {
      card.appendChild(el('div', { class: 'svc-empty', text: '左侧选择或导入一个 WSDL 项目，下方会列出 operations' }));
      return card;
    }
    const p = state.currentProject;
    card.appendChild(el('div', { class: 'svc-proj-header' }, [
      el('div', { class: 'svc-proj-name', text: p.name }),
      el('div', { class: 'svc-proj-meta' }, [
        el('span', { text: 'SOAP ' + (p.soap_version || '1.1') }),
        el('span', { text: p.target_ns || '无 namespace' }),
        el('span', { text: (p.operations || []).length + ' operations' }),
      ]),
    ]));

    // Warnings / ParseError
    if (p.parse_error) {
      console.warn('parse error:', p.parse_error);
      card.appendChild(el('div', { class: 'svc-warn', text: '解析失败, 请检查输入格式' }));
    }
    if (p.warnings && p.warnings.length > 0) {
      const w = el('div', { class: 'svc-warn' });
      w.appendChild(el('div', { text: '提示：' }));
      p.warnings.forEach(line => w.appendChild(el('div', { class: 'svc-warn-line', text: '· ' + line })));
      if (p.warnings.some(line => /(?:import|include|外部\s*XSD|\.xsd)/i.test(line))) {
        w.appendChild(el('div', {
          class: 'svc-warn-line',
          text: '· 建议点「上传文件」，一次选中 WSDL 和它引用的全部 XSD；缺少 XSD 时只能生成可用的报文骨架。',
        }));
      }
      card.appendChild(w);
    }

    // Services / Ports
    if (p.services && p.services.length > 0) {
      const svcBox = el('div', { class: 'svc-svc-box' });
      p.services.forEach(svc => {
        svcBox.appendChild(el('div', { class: 'svc-svc-name', text: 'Service: ' + svc.name }));
        (svc.ports || []).forEach(port => {
          svcBox.appendChild(el('div', { class: 'svc-port' }, [
            el('span', { class: 'svc-port-name', text: port.name }),
            el('span', { class: 'svc-port-ep', text: port.endpoint || '(无 endpoint)' }),
            el('span', { class: 'svc-port-ver', text: 'SOAP ' + (port.soap_version || p.soap_version || '1.1') }),
          ]));
        });
      });
      card.appendChild(svcBox);
    }

    // Operations
    const ops = p.operations || [];
    if (ops.length === 0) {
      card.appendChild(el('div', { class: 'svc-empty', text: '未解析到 operation' }));
      return card;
    }
    // Operations 列表标题行（可折叠）
    const opsHeader = el('div', { class: 'svc-op-list-header' }, [
      el('span', { class: 'svc-op-list-toggle' + (state.opListCollapsed ? ' collapsed' : ''), text: state.opListCollapsed ? '▶' : '▼' }),
      el('span', { class: 'svc-op-list-title', text: 'Operations（' + ops.length + '）' }),
    ]);
    opsHeader.style.cursor = 'pointer';
    opsHeader.onclick = function () { state.opListCollapsed = !state.opListCollapsed; rerenderMain(); };
    card.appendChild(opsHeader);

    if (!state.opListCollapsed) {
      const opsList = el('div', { class: 'svc-op-list' });
      ops.forEach(op => {
        const isActive = operationKey(state.currentOperation) === operationKey(op);
        const opItem = el('div', {
          class: 'svc-op-item' + (isActive ? ' active' : ''),
          onclick: () => selectOperation(op)
        }, [
          el('div', { class: 'svc-op-name', text: op.name }),
          el('div', { class: 'svc-op-sub' }, [
            el('span', { text: op.soap_action ? ('SOAPAction: ' + op.soap_action) : '无 SOAPAction' }),
            el('span', { text: op.endpoint || '(无 endpoint)' }),
          ]),
          el('div', { class: 'svc-op-params' }, [
            el('span', { class: 'svc-op-params-label', text: '输入：' }),
            el('span', { text: (op.input_params && op.input_params.length ? op.input_params.length : 0) + ' 字段' }),
            el('span', { class: 'svc-op-params-label', text: '输出：' }),
            el('span', { text: (op.output_params && op.output_params.length ? op.output_params.length : 0) + ' 字段' }),
          ]),
        ]);
        opsList.appendChild(opItem);
      });
      card.appendChild(opsList);
    }

    // 选中 operation 的参数详情（可折叠，默认收起）
    if (state.currentOperation) {
      // 详情标题行（带展开/收起按钮）
      const detailHeader = el('div', { class: 'svc-op-detail-header' }, [
        el('span', { class: 'svc-op-detail-toggle' + (state.opDetailExpanded ? '' : ' collapsed'), text: state.opDetailExpanded ? '▼' : '▶' }),
        el('span', { class: 'svc-op-detail-title-inline', text: 'Operation 详情：' + state.currentOperation.name }),
      ]);
      detailHeader.style.cursor = 'pointer';
      detailHeader.onclick = function () { state.opDetailExpanded = !state.opDetailExpanded; rerenderMain(); };
      card.appendChild(detailHeader);
      if (state.opDetailExpanded) {
        card.appendChild(renderOpParamDetails(state.currentOperation));
      }
    }
    return card;
  }

  function renderOpParamDetails(op) {
    const box = el('div', { class: 'svc-op-detail' });
    box.appendChild(el('div', { class: 'svc-op-detail-row' }, [
      el('span', { class: 'svc-k', text: 'namespace' }), el('span', { class: 'svc-v', text: op.namespace || '(无)' }),
    ]));
    box.appendChild(el('div', { class: 'svc-op-detail-row' }, [
      el('span', { class: 'svc-k', text: 'SOAPAction' }), el('span', { class: 'svc-v', text: op.soap_action || '(空)' }),
    ]));
    box.appendChild(el('div', { class: 'svc-op-detail-row' }, [
      el('span', { class: 'svc-k', text: 'endpoint' }), el('span', { class: 'svc-v', text: op.endpoint || '(无)' }),
    ]));
    box.appendChild(el('div', { class: 'svc-op-detail-row' }, [
      el('span', { class: 'svc-k', text: 'SOAP版本' }), el('span', { class: 'svc-v', text: op.soap_version || '1.1' }),
    ]));

    // 输入参数
    box.appendChild(el('div', { class: 'svc-op-detail-title small', text: '输入参数' }));
    if (op.input_params && op.input_params.length > 0) {
      box.appendChild(renderParamTree(op.input_params, 0));
    } else if (op.input_raw) {
      box.appendChild(el('pre', { class: 'svc-raw', text: op.input_raw }));
    } else {
      box.appendChild(el('div', { class: 'svc-empty', text: '无输入参数（解析失败时此处显示原始片段）' }));
    }

    // 输出参数
    box.appendChild(el('div', { class: 'svc-op-detail-title small', text: '输出参数' }));
    if (op.output_params && op.output_params.length > 0) {
      box.appendChild(renderParamTree(op.output_params, 0));
    } else if (op.output_raw) {
      box.appendChild(el('pre', { class: 'svc-raw', text: op.output_raw }));
    } else {
      box.appendChild(el('div', { class: 'svc-empty', text: '无输出参数' }));
    }
    return box;
  }

  function renderParamTree(params, depth) {
    const ul = el('div', { class: 'svc-param-tree' + (depth > 0 ? ' nested' : '') });
    params.forEach(p => {
      const row = el('div', { class: 'svc-param-row' }, [
        el('span', { class: 'svc-param-name', text: p.name || '?' }),
        el('span', { class: 'svc-param-type', text: p.type || 'any' }),
        el('span', { class: 'svc-param-occurs', text: (p.min_occurs || '0') + '..' + (p.max_occurs || '1') }),
        p.nillable ? el('span', { class: 'svc-param-nil', text: 'nillable' }) : null,
      ]);
      ul.appendChild(row);
      if (p.children && p.children.length > 0 && depth < 4) {
        ul.appendChild(renderParamTree(p.children, depth + 1));
      }
    });
    return ul;
  }

  // ---------- 请求编辑器 ----------

  function renderRequestEditor() {
    const card = el('div', { class: 'card svc-req-card' });
    card.appendChild(el('div', { class: 'svc-section-title', text: '请求编辑器' }));

    // 所有输入字段从 state.draft 读取；oninput/onchange 同步回 state.draft，
    // 这样切 tab 重渲染不会丢失用户正在编辑的内容。
    const d = state.draft;
    const endpointInp = el('input', { type: 'text', class: 'svc-inp-endpoint', id: 'svc-endpoint', placeholder: 'http://host:port/services/Foo', value: d.endpoint, oninput: (e) => { state.draft.endpoint = e.target.value; } });
    const soapActionInp = el('input', { type: 'text', class: 'svc-inp-action', id: 'svc-soapaction', placeholder: 'SOAPAction', value: d.soapAction, oninput: (e) => { state.draft.soapAction = e.target.value; } });
    const soapVerSel = el('select', { id: 'svc-soapver', onchange: (e) => {
      state.draft.soapVersion = e.target.value;
      state.draft.body = syncSOAPEnvelopeVersion(state.draft.body, e.target.value);
      const bodyInp = document.getElementById('svc-body');
      if (bodyInp) { bodyInp.value = state.draft.body; autoResizeTextarea(bodyInp); }
    } }, [
      el('option', { value: '1.1', text: 'SOAP 1.1 (text/xml)' }),
      el('option', { value: '1.2', text: 'SOAP 1.2 (application/soap+xml)' }),
    ]);
    soapVerSel.value = d.soapVersion || '1.1';
    const encodingSel = el('select', { id: 'svc-encoding', onchange: (e) => {
      state.draft.encoding = e.target.value;
      state.draft.body = syncXMLDeclaration(state.draft.body, e.target.value);
      const bodyInp = document.getElementById('svc-body');
      if (bodyInp) { bodyInp.value = state.draft.body; autoResizeTextarea(bodyInp); }
    } }, [
      el('option', { value: 'UTF-8', text: 'UTF-8' }),
      el('option', { value: 'GBK', text: 'GBK' }),
      el('option', { value: 'GB2312', text: 'GB2312' }),
      el('option', { value: 'GB18030', text: 'GB18030' }),
    ]);
    encodingSel.value = d.encoding || 'UTF-8';
    const timeoutInp = el('input', { type: 'number', class: 'svc-inp-timeout', id: 'svc-timeout', placeholder: '超时(ms)', value: String(d.timeoutMs || 30000), min: '1000', max: '300000', oninput: (e) => { const v = parseInt(e.target.value, 10); if (!isNaN(v)) state.draft.timeoutMs = v; } });
    const saveHistoryChk = el('input', { type: 'checkbox', id: 'svc-save-history', checked: d.saveHistory !== false, onchange: (e) => { state.draft.saveHistory = !!e.target.checked; } });
    const saveHistoryCtl = el('label', { class: 'svc-inline-check', title: '关闭后本次请求不会写入历史，适合敏感报文或临时探测' }, [
      saveHistoryChk, el('span', { text: '保存本次请求' }),
    ]);

    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-1col' }, [
      el('label', { text: 'endpoint' }), endpointInp,
    ]));
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-2col' }, [
      el('label', { text: 'SOAPAction' }), soapActionInp,
      el('label', { text: '版本' }), soapVerSel,
    ]));
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-3col' }, [
      el('label', { text: '编码' }), encodingSel,
      el('label', { text: '超时(ms)' }), timeoutInp,
      el('label', { text: '历史' }), saveHistoryCtl,
    ]));


    // 自定义 headers
    const headersArea = el('textarea', { class: 'svc-headers', id: 'svc-headers', placeholder: '支持 Key: Value、JSON 对象、curl -H / --header', rows: '3', oninput: (e) => { state.draft.headers = e.target.value; autoResizeTextarea(e.target); } });
    headersArea.value = d.headers || '';
    bindAutoResize(headersArea);
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-5col' }, [
      el('label', { text: 'Headers' }), headersArea,
    ]));

    // 请求 body
    const bodyArea = el('textarea', { class: 'svc-body', id: 'svc-body', placeholder: '请求 XML（Envelope）', rows: '12', spellcheck: 'false', oninput: (e) => { state.draft.body = e.target.value; state.draft.bodyDirty = true; autoResizeTextarea(e.target); } });
    bodyArea.value = d.body || '';
    bindAutoResize(bodyArea);
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-5col' }, [
      el('label', { text: '请求 XML' }), bodyArea,
    ]));

    // 操作按钮：格式化 / 压缩 / 校验 / 复制 / 发送 / 存模板
    // v0.14.1 去掉「生成 Envelope」按钮 —— 选 op 时已自动生成全空值 envelope，
    // 按钮点击会写入同样内容，UI 看上去无反应。
    const btnRow = el('div', { class: 'svc-btn-row' });
    const btnFormat = el('button', { class: 'btn', text: '格式化', onclick: () => xmlAction('format') });
    const btnMinify = el('button', { class: 'btn', text: '压缩', onclick: () => xmlAction('minify') });
    const btnValidate = el('button', { class: 'btn', text: '校验', onclick: () => xmlAction('validate') });
    const btnCopy = el('button', { class: 'btn', text: '复制', onclick: () => {
      copyToClipboard(state.draft.body || '').then(() => toast('已复制请求体', 'ok'), () => toast('复制失败', 'err'));
    }});
    const btnSaveTpl = el('button', { class: 'btn', text: '存为模板', onclick: saveCurrentAsTemplate });
    const btnSend = el('button', { id: 'svc-send-btn', class: 'btn btn-primary', text: state.requestInFlight ? '发送中...' : '发送', disabled: state.requestInFlight, onclick: sendRequest });
    btnRow.appendChild(btnFormat);
    btnRow.appendChild(btnMinify);
    btnRow.appendChild(btnValidate);
    btnRow.appendChild(btnCopy);
    btnRow.appendChild(btnSaveTpl);
    btnRow.appendChild(el('span', { style: 'flex:1' }));
    btnRow.appendChild(btnSend);
    card.appendChild(btnRow);

    return card;
  }

  // ---------- 响应区 ----------

  function renderResponseArea() {
    const card = el('div', { class: 'card svc-resp-card' });
    card.appendChild(el('div', { class: 'svc-section-title' }, [
      el('span', { text: '响应结果' }),
      state.response ? el('span', { class: 'svc-resp-meta', text: ' · ' + (state.response.status || 0) + ' ' + (state.response.status_text || '') + ' · ' + (state.response.elapsed_ms || 0) + 'ms · ' + fmtBytes(state.response.body_bytes || 0) }) : null,
    ]));

    if (!state.response) {
      card.appendChild(el('div', { class: 'svc-empty', text: '发送请求后这里显示响应' }));
      return card;
    }

    const r = state.response;
    if (r.error) {
      card.appendChild(el('div', { class: 'svc-warn', text: '错误：' + r.error }));
    } else if (!r.ok) {
      card.appendChild(el('div', { class: 'svc-warn', text: 'HTTP 状态非 2xx（' + r.status + '）' }));
    }
	if (r.truncated) {
	  card.appendChild(el('div', { class: 'svc-warn', text: '响应体超过 2MB，当前只展示并保存前 2MB' }));
	}

    // 历史重放按钮：仅当响应来自历史（_historyId 存在）时显示
    if (r._historyId) {
      const replayBar = el('div', { class: 'svc-replay-bar' });
      // 用文字 + 简单 ASCII 符号，不用 emoji（Win7/老 360 浏览器可能渲染为方块）
      replayBar.appendChild(el('span', { class: 'svc-replay-tip', text: '[历史回显] 来自历史记录 #' + r._historyId.slice(0, 8) + ' —— 如需重新发送请点右侧按钮' }));
      const btnReplay = el('button', { class: 'btn btn-mini btn-primary', text: '重放', onclick: replayCurrentHistory, title: '按历史记录里的 endpoint/headers/body 重新发送一遍' });
      replayBar.appendChild(btnReplay);
      card.appendChild(replayBar);
    }

    // 响应 headers
    if (r.headers && Object.keys(r.headers).length > 0) {
      const hBox = el('div', { class: 'svc-resp-headers' });
      hBox.appendChild(el('div', { class: 'svc-resp-headers-title', text: '响应 Headers' }));
      Object.keys(r.headers).forEach(k => {
        hBox.appendChild(el('div', { class: 'svc-resp-header-row' }, [
          el('span', { class: 'svc-k', text: k }),
          el('span', { class: 'svc-v', text: r.headers[k] }),
        ]));
      });
      const btnCopyH = el('button', { class: 'btn btn-mini', text: '复制 Headers', onclick: () => {
        const txt = Object.keys(r.headers).map(k => k + ': ' + r.headers[k]).join('\n');
        copyToClipboard(txt).then(() => toast('已复制', 'ok'), () => toast('复制失败', 'err'));
      }});
      hBox.appendChild(btnCopyH);
      card.appendChild(hBox);
    }

    // 响应 body
    const bodyBox = el('div', { class: 'svc-resp-body-wrap' });
    const bodyBar = el('div', { class: 'svc-resp-body-bar' });
    const btnFmt = el('button', { class: 'btn btn-mini', text: '格式化', onclick: () => formatResponseBody() });
    const btnMin = el('button', { class: 'btn btn-mini', text: '压缩', onclick: () => minifyResponseBody() });
    const btnCopy = el('button', { class: 'btn btn-mini', text: '复制', onclick: () => {
      copyToClipboard(r.body || '').then(() => toast('已复制响应体', 'ok'), () => toast('复制失败', 'err'));
    }});
    bodyBar.appendChild(btnFmt);
    bodyBar.appendChild(btnMin);
    bodyBar.appendChild(btnCopy);
    bodyBox.appendChild(bodyBar);

    // 高亮显示 body
    const bodyPre = el('pre', { class: 'svc-resp-body', unsafeHtml: highlight(r.body || '') });
    bodyBox.appendChild(bodyPre);
    card.appendChild(bodyBox);

    // 建议关键词（日志联动）
    if (state.keywords && state.keywords.length > 0) {
      const kwBox = el('div', { class: 'svc-keywords' });
      kwBox.appendChild(el('div', { class: 'svc-keywords-title', text: '建议日志搜索关键词：' }));
      state.keywords.forEach(kw => {
        const chip = el('span', { class: 'svc-kw-chip', text: kw, title: '点击复制' });
        chip.onclick = () => {
          copyToClipboard(kw).then(() => toast('已复制：' + kw, 'ok'), () => toast('复制失败', 'err'));
        };
        kwBox.appendChild(chip);
      });
      card.appendChild(kwBox);
    }
    return card;
  }

  // ---------- Mock 编辑器 ----------

  function renderMockEditor() {
    const card = el('div', { class: 'card svc-mock-card' });
    const m = state.currentMock || { enabled: true, status_code: 200, delay_ms: 0 };

    card.appendChild(el('div', { class: 'svc-section-title', text: state.currentMock ? ('编辑 Mock：' + m.name) : '新建 Mock' }));

    const nameInp = el('input', { type: 'text', id: 'svc-mock-name', placeholder: 'Mock 名称（如：客户查询成功）', value: m.name || '' });
    const pathInp = el('input', { type: 'text', id: 'svc-mock-path', placeholder: '路径（如 /mock/customerQuery 或 customerQuery）', value: m.path || '' });
    const opInp = el('input', { type: 'text', id: 'svc-mock-op', placeholder: 'operation（可选）', value: m.operation || '' });
    const statusInp = el('input', { type: 'number', id: 'svc-mock-status', placeholder: 'HTTP 状态码', value: String(m.status_code || 200), min: '100', max: '599' });
    const delayInp = el('input', { type: 'number', id: 'svc-mock-delay', placeholder: '延迟(ms)', value: String(m.delay_ms || 0), min: '0', max: '300000' });
    const enabledChk = el('input', { type: 'checkbox', id: 'svc-mock-enabled', checked: m.enabled });
    const bodyArea = el('textarea', { class: 'svc-mock-body', id: 'svc-mock-body', placeholder: '固定响应 XML', rows: '10', spellcheck: 'false', oninput: (e) => autoResizeTextarea(e.target) });
    bodyArea.value = m.body || '';
    bindAutoResize(bodyArea);

    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-1col' }, [el('label', { text: '名称*' }), nameInp]));
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-1col' }, [el('label', { text: '路径*' }), pathInp]));
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-1col' }, [el('label', { text: 'operation' }), opInp]));
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-4col' }, [
      el('label', { text: '状态码' }), statusInp,
      el('label', { text: '延迟(ms)' }), delayInp,
      el('label', { text: '启用' }), enabledChk,
    ]));
    card.appendChild(el('div', { class: 'svc-form-grid svc-form-grid-5col' }, [el('label', { text: '响应 XML' }), bodyArea]));

    const btnRow = el('div', { class: 'svc-btn-row' });
    const btnSave = el('button', { class: 'btn btn-primary', text: '保存 Mock', onclick: saveMock });
    const btnCancel = el('button', { class: 'btn', text: '取消', onclick: () => { state.currentMock = null; rerenderMain(); } });
    const btnTest = el('button', { class: 'btn', text: '测试访问', onclick: testMock });
    btnRow.appendChild(btnSave);
    btnRow.appendChild(btnCancel);
    btnRow.appendChild(el('span', { style: 'flex:1' }));
    btnRow.appendChild(btnTest);
    card.appendChild(btnRow);

    // 提示
    card.appendChild(el('div', { class: 'muted', style: 'font-size:12px;margin-top:8px', text: '提示：保存后立即生效。外部系统直接请求 http://<本机IP>:<端口>' + (m.path || '/mock/xxx') + ' 即可收到上面的固定响应。延迟超过客户端超时即可模拟超时。' }));
    return card;
  }

  // ---------- 动作：WSDL 导入 ----------

  async function openImportURLDialog() {
    const url = await promptDialog('请输入 WSDL URL：', { title: '导入 WSDL', placeholder: 'http://example.com/service.wsdl' });
    if (!url) return;
    const name = await promptDialog('为这个项目命名（可空）：', { title: '项目命名', defaultValue: '' }) || '';
    try {
      toast('正在拉取并解析 WSDL...', 'info');
      const r = await postJSON('/api/wsdl/import-url', { url: url.trim(), name: name.trim() });
      if (r && r.project) {
        // 保存到本地
        const saved = await postJSON('/api/wsdl/projects', { project: r.project });
        if (saved && saved.project) {
          toast('WSDL 已导入并保存', 'ok');
          await refreshAll();
          await selectProject(saved.project.id);
        }
      }
    } catch (e) {
      toast('导入失败：' + (e.message || e), 'err');
    }
  }

  function triggerUploadFile() {
    const inp = document.getElementById('svc-upload-inp');
    if (inp) inp.click();
  }

  // 读取 WSDL/XSD：兼容 UTF-8(BOM)、UTF-16LE/BE、GBK/GB2312/GB18030。
  // 老 Java 工程导出的 WSDL 很多不是 UTF-8，直接 readAsText(file) 会把中文和
  // QName 附近内容读成乱码。解码后统一把 XML declaration 改成 UTF-8 再传后端。
  function detectXMLFileEncoding(bytes) {
    if (bytes.length >= 3 && bytes[0] === 0xEF && bytes[1] === 0xBB && bytes[2] === 0xBF) return 'utf-8';
    if (bytes.length >= 2 && bytes[0] === 0xFF && bytes[1] === 0xFE) return 'utf-16le';
    if (bytes.length >= 2 && bytes[0] === 0xFE && bytes[1] === 0xFF) return 'utf-16be';
    if (bytes.length >= 4 && bytes[0] === 0x3C && bytes[1] === 0 && bytes[2] === 0x3F && bytes[3] === 0) return 'utf-16le';
    if (bytes.length >= 4 && bytes[0] === 0 && bytes[1] === 0x3C && bytes[2] === 0 && bytes[3] === 0x3F) return 'utf-16be';
    let ascii = '';
    const n = Math.min(bytes.length, 1024);
    for (let i = 0; i < n; i++) ascii += String.fromCharCode(bytes[i]);
    const m = ascii.match(/<\?xml\b[^>]*\bencoding\s*=\s*["']\s*([^"']+)\s*["']/i);
    return m ? m[1].trim().toLowerCase() : 'utf-8';
  }

  function readFileText(file) {
    return new Promise(function (resolve, reject) {
      var reader = new FileReader();
      reader.onload = function () {
        try {
          var bytes = new Uint8Array(reader.result);
          var encoding = detectXMLFileEncoding(bytes);
          if (typeof TextDecoder === 'function') {
            var text = new TextDecoder(encoding).decode(bytes);
            resolve(syncXMLDeclaration(text.replace(/^\uFEFF/, ''), 'UTF-8'));
            return;
          }
          // 旧 WebView 没有 TextDecoder：FileReader 本身支持指定字符集。
          var fallback = new FileReader();
          fallback.onload = function () { resolve(syncXMLDeclaration(String(fallback.result || '').replace(/^\uFEFF/, ''), 'UTF-8')); };
          fallback.onerror = function () { reject(fallback.error || new Error('读取文件失败')); };
          fallback.readAsText(file, encoding);
        } catch (err) {
          reject(err);
        }
      };
      reader.onerror = function () { reject(reader.error || new Error('读取文件失败')); };
      reader.readAsArrayBuffer(file);
    });
  }

  async function handleUploadFile(e) {
    const files = e.target.files;
    if (!files || files.length === 0) return;

    const maxSize = 4 * 1024 * 1024;
    const maxTotalSize = 16 * 1024 * 1024;
    let totalSize = 0;
    for (let i = 0; i < files.length; i++) {
      totalSize += files[i].size;
      if (files[i].size > maxSize) {
        toast('文件过大（最大 4MB）：' + files[i].name, 'err');
        e.target.value = '';
        return;
      }
    }
    if (totalSize > maxTotalSize) {
      toast('所选文件合计超过 16MB，请减少附件后重试', 'err');
      e.target.value = '';
      return;
    }

    try {
      const fileContents = {};
      let wsdlFile = null;
      for (let i = 0; i < files.length; i++) {
        const f = files[i];
        const text = await readFileText(f);
        fileContents[f.name] = text;
        if (/\.wsdl$/i.test(f.name)) {
          if (!wsdlFile) wsdlFile = f;
        }
      }

      if (!wsdlFile) {
        for (let i = 0; i < files.length; i++) {
          if (/\.xml$/i.test(files[i].name) || files[i].name.toLowerCase().indexOf('wsdl') >= 0) {
            wsdlFile = files[i];
            break;
          }
        }
      }
      if (!wsdlFile) {
        wsdlFile = files[0];
      }

      const mainContent = fileContents[wsdlFile.name];
      const attachments = {};
      for (const name in fileContents) {
        if (name !== wsdlFile.name) {
          attachments[name] = fileContents[name];
        }
      }

      const name = wsdlFile.name.replace(/\.(wsdl|xsd|xml)$/i, '');
      const payload = { content: mainContent, name: name };
      if (Object.keys(attachments).length > 0) {
        payload.attachments = attachments;
      }

      const r = await postJSON('/api/wsdl/import-file', payload);
      if (r && r.project) {
        const saved = await postJSON('/api/wsdl/projects', { project: r.project });
        if (saved && saved.project) {
          const extra = Object.keys(attachments).length > 0
            ? ('（含 ' + Object.keys(attachments).length + ' 个附件）')
            : '';
          toast('文件已导入并保存' + extra, 'ok');
          await refreshAll();
          await selectProject(saved.project.id);
        }
      }
    } catch (err) {
      console.warn('import failed:', err.message || err);
      toast('导入失败, 请检查文件格式', 'err');
    }
    e.target.value = '';
  }

  // 切 tab：保存当前 tab 的编辑状态快照，恢复目标 tab 的快照，清空搜索词
  function switchTab(newTab) {
    if (state.activeTab === newTab) return;
    // 保存当前 tab 的编辑状态（浅拷贝 draft 即可，字段都是基本类型）
    state.draftSnapshots[state.activeTab] = {
      draft: Object.assign({}, state.draft),
      currentOperation: state.currentOperation,
      opDetailExpanded: state.opDetailExpanded,
      opListCollapsed: state.opListCollapsed,
      response: state.response,
    };
    // 切到目标 tab
    state.activeTab = newTab;
    // 恢复目标 tab 的快照（首次切换时无快照，保持默认空状态）
    const snap = state.draftSnapshots[newTab];
    if (snap) {
      state.draft = Object.assign({}, snap.draft);
      state.currentOperation = snap.currentOperation;
      state.opDetailExpanded = snap.opDetailExpanded;
      state.opListCollapsed = snap.opListCollapsed;
      state.response = snap.response;
    }
    // 切 tab 时清空搜索词，避免跨 tab 误过滤
    state.search = '';
    rerenderSidebar();
    rerenderMain();
  }

  async function selectProject(id) {
    if (state.draft.bodyDirty && state.currentProject && state.currentProject.id !== id) {
      if (!await confirmDialog('当前请求 XML 已修改。切换 WSDL 项目会重新生成报文，是否继续？')) return;
    }
    try {
      const r = await getJSON('/api/wsdl/projects/' + encodeURIComponent(id));
      if (r && r.project) {
        state.currentProject = r.project;
        state.currentOperation = null;
        // 自动选中第一个 operation，让用户导入后立即可见报文
        const ops = r.project.operations || [];
        if (ops.length > 0) {
          const firstOp = ops[0];
          state.currentOperation = firstOp;
          state.draft.endpoint = firstOp.endpoint || state.draft.endpoint || '';
          state.draft.soapAction = firstOp.soap_action || state.draft.soapAction || '';
          state.draft.soapVersion = firstOp.soap_version || state.draft.soapVersion || '1.1';
          // 自动生成 Envelope（不弹 toast，静默）
          if (!state.draft.bodyDirty) {
            await generateEnvelope(true);
          }
        }
        rerenderSidebar();
        rerenderMain();
      }
    } catch (e) {
      toast('加载项目失败：' + (e.message || e), 'err');
    }
  }

  async function deleteProject(id) {
    if (!await confirmDialog('删除这个 WSDL 项目？')) return;
    try {
      await deleteJSON('/api/wsdl/projects/' + encodeURIComponent(id));
      if (state.currentProject && state.currentProject.id === id) {
        state.currentProject = null;
        state.currentOperation = null;
      }
      await refreshAll();
      toast('已删除', 'ok');
    } catch (e) {
      toast('删除失败：' + (e.message || e), 'err');
    }
  }

  async function selectOperation(op) {
    if (operationKey(state.currentOperation) === operationKey(op)) return;
    if (state.draft.bodyDirty && state.currentOperation) {
      if (!await confirmDialog('当前请求 XML 已修改。切换 operation 会按新接口重新生成报文，是否继续？')) return;
    }
    state.currentOperation = op;
    // 切换 operation 时重置详情折叠状态（默认收起）
    state.opDetailExpanded = false;
    if (op) {
      state.draft.endpoint = op.endpoint || state.draft.endpoint || '';
      state.draft.soapAction = op.soap_action || state.draft.soapAction || '';
      state.draft.soapVersion = op.soap_version || state.draft.soapVersion || '1.1';
    }
    // operation、endpoint、SOAPAction 和 body 必须作为一个整体切换，
    // 不能把上一个 operation 的已编辑报文静默发到新 endpoint。
    state.draft.bodyDirty = false;
    await generateEnvelope(true);
    rerenderMain();
  }

  async function generateEnvelope(silent) {
    const op = state.currentOperation;
    if (!op) {
      if (!silent) toast('请先选择一个 operation', 'warn');
      return;
    }
    const ver = state.draft.soapVersion || op.soap_version || '1.1';
    try {
      const r = await postJSON('/api/soap/generate', { operation: op, soap_version: ver });
      if (r && r.envelope != null) {
        state.draft.body = r.envelope;
        state.draft.bodyDirty = false; // 程序生成的 body 不算用户编辑
        // 如果编辑器已渲染，同步到 DOM
        const bodyInp = document.getElementById('svc-body');
        if (bodyInp) {
          bodyInp.value = r.envelope;
          autoResizeTextarea(bodyInp);
        }
        if (!silent) toast('已生成 Envelope', 'ok');
      }
    } catch (e) {
      if (!silent) toast('生成失败：' + (e.message || e), 'err');
    }
  }


  // ---------- 动作：发送 / XML 操作 ----------

  async function sendRequest() {
    if (state.requestInFlight) return; // 防止重复提交
    const d = state.draft;
    const endpoint = d.endpoint || '';
    const soapAction = d.soapAction || '';
    const soapVer = d.soapVersion || '1.1';
    const encoding = d.encoding || 'UTF-8';
    const timeoutMs = d.timeoutMs || 30000;
    const headersRaw = d.headers || '';
    const body = d.body || '';

    if (!endpoint.trim()) { toast('endpoint 不能为空', 'warn'); return; }
    if (!body.trim()) { toast('请求 XML 不能为空', 'warn'); return; }

    // 解析 headers
    const parsedHeaders = parseHeadersText(headersRaw);
    if (parsedHeaders.invalid.length > 0) {
      toast('Header 格式错误：' + parsedHeaders.invalid[0], 'warn');
      return;
    }
    const headers = parsedHeaders.headers;

    const req = {
      endpoint, soap_action: soapAction, soap_version: soapVer,
      encoding, timeout_ms: timeoutMs, headers, body,
      operation: state.currentOperation ? state.currentOperation.name : '',
      save_history: d.saveHistory !== false,
    };

    state.requestInFlight = true;
    const sendBtn = document.getElementById('svc-send-btn');
    if (sendBtn) { sendBtn.disabled = true; sendBtn.textContent = '发送中...'; }
    try {
      toast('发送中...', 'info');
      const r = await postJSON('/api/soap/send', req);
      if (r) {
        state.response = r.response || {};
        state.keywords = r.keywords || [];
        rerenderMain();
        // 自动刷新历史侧栏
        await refreshHistory();
        toast('请求完成', state.response.ok ? 'ok' : 'warn');
      }
    } catch (e) {
      // 后端错误细节不直接暴露给用户（M-6），仅记到控制台
      console.warn('send failed:', e.message || e);
      toast('发送失败, 请稍后重试', 'err');
    } finally {
      state.requestInFlight = false;
      const btn = document.getElementById('svc-send-btn');
      if (btn) { btn.disabled = false; btn.textContent = '发送'; }
    }
  }

  async function xmlAction(mode) {
    const input = state.draft.body || '';
    if (!input.trim()) { toast('请求 XML 为空', 'warn'); return; }
    try {
      if (mode === 'validate') {
        const r = await postJSON('/api/ws/xml/validate', { input });
        if (r.ok) toast('XML well-formed', 'ok');
        else toast(r.error || 'XML 非法', 'err'); // 后端已含"XML 非法"前缀，不再重复
      } else {
        const r = await postJSON('/api/ws/xml/' + mode, { input });
        if (r && r.output != null) {
          state.draft.body = r.output;
          const bodyInp = document.getElementById('svc-body');
          if (bodyInp) bodyInp.value = r.output;
          toast(mode === 'format' ? '已格式化' : '已压缩', 'ok');
        }
      }
    } catch (e) {
      toast('操作失败：' + (e.message || e), 'err');
    }
  }

  async function formatResponseBody() {
    if (!state.response || !state.response.body) return;
    try {
      const r = await postJSON('/api/ws/xml/format', { input: state.response.body });
      if (r && r.output != null) {
        state.response.body = r.output;
        rerenderMain();
        toast('已格式化', 'ok');
      }
    } catch (e) { toast('格式化失败：' + (e.message || e), 'err'); }
  }

  async function minifyResponseBody() {
    if (!state.response || !state.response.body) return;
    try {
      const r = await postJSON('/api/ws/xml/minify', { input: state.response.body });
      if (r && r.output != null) {
        state.response.body = r.output;
        rerenderMain();
        toast('已压缩', 'ok');
      }
    } catch (e) { toast('压缩失败：' + (e.message || e), 'err'); }
  }

  // ---------- 动作：模板 ----------

  // UX 改进：原版 "+ 新模板" 弹个 toast 让用户去填编辑器再点"存为模板"，
  // 跟 WSDL/Mock 的"+ 导入/新建"直接弹表单 UX 不一致。
  // 改为：弹一个简化模板表单（只填名字和分组），body 让用户后续在编辑器里填。
  async function newTemplate() {
    const result = await multiFieldDialog({
      title: '新建模板',
      fields: [
        { key: 'name', label: '模板名称', placeholder: '如：恒力查询', required: true },
        { key: 'group', label: '分组（可空）', placeholder: '如：恒力/银行/内部' },
      ],
    });
    if (!result) return;
    const d = state.draft;
    const parsedHeaders = parseHeadersText(d.headers || '');
    if (parsedHeaders.invalid.length > 0) { toast('Header 格式错误：' + parsedHeaders.invalid[0], 'warn'); return; }
    const tpl = {
      name: result.name.trim(),
      group: (result.group || '').trim(),
      endpoint: d.endpoint || '',
      operation: state.currentOperation ? state.currentOperation.name : '',
      soap_action: d.soapAction || '',
      soap_version: d.soapVersion || '1.1',
      headers: parsedHeaders.headers,
      body: d.body || '',
      encoding: d.encoding || 'UTF-8',
      timeout_ms: d.timeoutMs || 30000,
    };
    try {
      const r = await postJSON('/api/soap/templates', { template: tpl });
      if (r && r.template) {
        toast('模板已创建，可在编辑器里继续编辑后点「存为模板」覆盖', 'ok');
        state.currentTemplate = r.template;
        await refreshTemplates();
        state.activeTab = 'templates';
        rerenderSidebar();
        rerenderMain();
      }
    } catch (e) { toast('创建失败：' + (e.message || e), 'err'); }
  }

  // 多字段输入对话框（替代连弹 N 个 promptDialog 的糟糕体验）
  // opts: { title, fields: [{key, label, placeholder, defaultValue, required}] }
  // 返回 Promise<Record<key, value> | null>
  function multiFieldDialog(opts) {
    opts = opts || {};
    if (typeof document === 'undefined' || !document.body) {
      // 后备：用 prompt 逐个问
      return (async () => {
        const out = {};
        for (const f of opts.fields || []) {
          const v = window.prompt(f.label, f.defaultValue || '');
          if (v === null) return null;
          out[f.key] = v;
        }
        return out;
      })();
    }
    return new Promise(function (resolve) {
      const overlay = el('div', { class: 'kairo-dialog-overlay' });
      const dialog = el('div', { class: 'kairo-dialog' });
      dialog.appendChild(el('div', { class: 'kairo-dialog-title', text: opts.title || '请输入' }));
      const bodyEl = el('div', { class: 'kairo-dialog-body' });
      const inputs = {};
      (opts.fields || []).forEach(f => {
        const row = el('div', { class: 'kairo-dialog-field' });
        row.appendChild(el('label', { text: f.label + (f.required ? ' *' : '') }));
        const inp = el('input', { type: 'text', class: 'form-control', placeholder: f.placeholder || '', value: f.defaultValue || '' });
        row.appendChild(inp);
        bodyEl.appendChild(row);
        inputs[f.key] = inp;
      });
      dialog.appendChild(bodyEl);
      const actions = el('div', { class: 'kairo-dialog-actions' });
      const cancelBtn = el('button', { class: 'btn', type: 'button', text: '取消', onclick: function () { close(null); } });
      const okBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '确定', onclick: function () {
        const out = {};
        for (const f of opts.fields || []) {
          out[f.key] = (inputs[f.key] && inputs[f.key].value) || '';
          if (f.required && !out[f.key].trim()) { toast(f.label + '不能为空', 'warn'); return; }
        }
        close(out);
      }});
      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      dialog.appendChild(actions);
      overlay.appendChild(dialog);
      overlay.addEventListener('click', function (ev) { if (ev.target === overlay) close(null); });
      const onKey = function (ev) {
        if (ev.key === 'Escape') { ev.preventDefault(); close(null); }
        else if (ev.key === 'Enter') { ev.preventDefault(); okBtn.click(); }
      };
      document.addEventListener('keydown', onKey, true);
      let settled = false;
      function close(v) {
        if (settled) return;
        settled = true;
        document.removeEventListener('keydown', onKey, true);
        try { overlay.remove(); } catch (e) { /* ignore */ }
        resolve(v);
      }
      document.body.appendChild(overlay);
      setTimeout(function () { const first = Object.values(inputs)[0]; if (first && first.focus) first.focus(); }, 0);
    });
  }

  async function saveCurrentAsTemplate() {
    const d = state.draft;
    const body = d.body || '';
    if (!body.trim()) { toast('请求 XML 为空，无法保存为模板', 'warn'); return; }
    // UX 改进：原来要弹 2 个对话框（名字 + 分组），合并成一个，分组可空
    const result = await multiFieldDialog({
      title: '保存为模板',
      fields: [
        { key: 'name', label: '模板名称', placeholder: '如：恒力查询', defaultValue: state.currentOperation ? state.currentOperation.name : '新模板', required: true },
        { key: 'group', label: '分组（可空）', placeholder: '如：恒力/银行/内部', defaultValue: '' },
      ],
    });
    if (!result) return;
    const name = result.name.trim();
    const group = result.group.trim();
    const parsedHeaders = parseHeadersText(d.headers || '');
    if (parsedHeaders.invalid.length > 0) { toast('Header 格式错误：' + parsedHeaders.invalid[0], 'warn'); return; }
    const headers = parsedHeaders.headers;
    const tpl = {
      name, group,
      endpoint: d.endpoint, operation: state.currentOperation ? state.currentOperation.name : '',
      soap_action: d.soapAction, soap_version: d.soapVersion || '1.1', headers, body,
      encoding: d.encoding, timeout_ms: d.timeoutMs || 30000,
    };
    try {
      const r = await postJSON('/api/soap/templates', { template: tpl });
      if (r && r.template) {
        toast('模板已保存', 'ok');
        state.currentTemplate = r.template;
        await refreshTemplates();
        rerenderSidebar();
      }
    } catch (e) { toast('保存失败：' + (e.message || e), 'err'); }
  }

  async function loadTemplate(id) {
    const t = state.templates.find(x => x.id === id);
    if (!t) return;
    state.currentTemplate = t;
    state.currentOperation = t.operation ? { name: t.operation, endpoint: t.endpoint, soap_action: t.soap_action } : null;
    // 直接写入 draft，渲染时从 state 读取，无需 setTimeout 等 DOM
    state.draft.endpoint = t.endpoint || '';
    state.draft.soapAction = t.soap_action || '';
    state.draft.soapVersion = t.soap_version || '1.1';
    state.draft.encoding = t.encoding || 'UTF-8';
    state.draft.timeoutMs = t.timeout_ms || 30000;
    state.draft.headers = t.headers ? Object.keys(t.headers).map(k => k + ': ' + t.headers[k]).join('\n') : '';
    state.draft.body = t.body || '';
    state.draft.bodyDirty = false; // 加载模板的 body 不算用户编辑
    // 保持在模板 tab：用户点模板就是为了浏览/切换模板，加载后不应突然跳回 WSDL。
    rerenderSidebar();
    rerenderMain();
    toast('已加载模板：' + t.name, 'info');
  }

  async function deleteTemplate(id) {
    if (!await confirmDialog('删除这个模板？')) return;
    try {
      await deleteJSON('/api/soap/templates/' + encodeURIComponent(id));
      if (state.currentTemplate && state.currentTemplate.id === id) state.currentTemplate = null;
      await refreshTemplates();
      rerenderSidebar();
      toast('已删除', 'ok');
    } catch (e) { toast('删除失败：' + (e.message || e), 'err'); }
  }

  // ---------- 动作：历史 ----------

  async function viewHistory(id) {
    const h = state.history.find(x => x.id === id);
    if (!h) return;
    // 把历史请求体填入 draft，响应展示历史响应
    state.response = {
      ok: h.success,
      status: h.status_code,
      status_text: '',
      headers: {},
      body: h.response_body || '',
      body_bytes: (h.response_body || '').length,
      elapsed_ms: h.duration_ms,
      error: h.error,
      _historyId: h.id,
    };
    state.keywords = [];
    state.currentOperation = h.operation ? { name: h.operation, endpoint: h.endpoint, soap_action: h.soap_action } : null;
    // 直接写入 draft，渲染时从 state 读取，无需 setTimeout 等 DOM
    state.draft.endpoint = h.endpoint || '';
    state.draft.soapAction = h.soap_action || '';
    state.draft.soapVersion = h.soap_version || '1.1';
    state.draft.encoding = h.encoding || 'UTF-8';
    state.draft.timeoutMs = h.timeout_ms || 30000;
    state.draft.headers = h.headers ? Object.keys(h.headers).map(function(k) { return k + ': ' + h.headers[k]; }).join('\n') : '';
    state.draft.body = h.request_body || '';
    rerenderSidebar();
    rerenderMain();
  }

  async function replayCurrentHistory() {
    const r = state.response;
    if (!r || !r._historyId) { toast('请先在左侧选中一条历史', 'warn'); return; }
    try {
      const resp = await postJSON('/api/soap/history/' + encodeURIComponent(r._historyId) + '/replay', {});
      if (resp) {
        state.response = resp.response || {};
        state.keywords = resp.keywords || [];
        rerenderMain();
        await refreshHistory();
        toast('重放完成', state.response.ok ? 'ok' : 'warn');
      }
    } catch (e) { toast('重放失败：' + (e.message || e), 'err'); }
  }

  async function clearHistory() {
    if (!await confirmDialog('确定清空全部历史？此操作不可恢复')) return;
    try {
      await deleteJSON('/api/soap/history');
      await refreshHistory();
      rerenderSidebar();
      toast('历史已清空', 'ok');
    } catch (e) { toast('清空失败：' + (e.message || e), 'err'); }
  }

  // ---------- 动作：Mock ----------

  function newMock() {
    state.currentMock = { enabled: true, status_code: 200, delay_ms: 0, body: '<?xml version="1.0" encoding="UTF-8"?>\n<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">\n  <soapenv:Body>\n    <mockResponse>\n      <code>0</code>\n      <msg>OK</msg>\n    </mockResponse>\n  </soapenv:Body>\n</soapenv:Envelope>' };
    rerenderMain();
  }

  function editMock(id) {
    const m = state.mocks.find(x => x.id === id);
    if (!m) return;
    state.currentMock = m;
    rerenderMain();
  }

  async function saveMock() {
    const m = state.currentMock || {};
    m.name = (document.getElementById('svc-mock-name') || {}).value || '';
    m.path = (document.getElementById('svc-mock-path') || {}).value || '';
    m.operation = (document.getElementById('svc-mock-op') || {}).value || '';
    m.status_code = parseInt((document.getElementById('svc-mock-status') || {}).value || '200', 10) || 200;
    m.delay_ms = parseInt((document.getElementById('svc-mock-delay') || {}).value || '0', 10) || 0;
    m.enabled = !!(document.getElementById('svc-mock-enabled') || {}).checked;
    m.body = (document.getElementById('svc-mock-body') || {}).value || '';
    if (!m.name.trim()) { toast('name 不能为空', 'warn'); return; }
    if (!m.path.trim()) { toast('path 不能为空', 'warn'); return; }
    try {
      const r = await postJSON('/api/soap/mocks', { mock: m });
      if (r && r.mock) {
        state.currentMock = r.mock;
        await refreshMocks();
        rerenderSidebar();
        toast('Mock 已保存并生效', 'ok');
      }
    } catch (e) { toast('保存失败：' + (e.message || e), 'err'); }
  }

  async function deleteMock(id) {
    if (!await confirmDialog('删除这个 Mock？')) return;
    try {
      await deleteJSON('/api/soap/mocks/' + encodeURIComponent(id));
      if (state.currentMock && state.currentMock.id === id) state.currentMock = null;
      await refreshMocks();
      rerenderSidebar();
      rerenderMain();
      toast('已删除', 'ok');
    } catch (e) { toast('删除失败：' + (e.message || e), 'err'); }
  }

  // 测试访问 mock 端点。原版只用 toast 显示 200 字预览，UX 反馈不够；
  // 改为弹模态框显示 status / headers / 完整 body。
  async function testMock() {
    const m = state.currentMock || {};
    const path = m.path || '/mock/test';
    const url = location.origin + (path.startsWith('/mock/') ? path : '/mock/' + path.replace(/^\/+/, ''));
    let resp, text, fetchErr = null;
    try {
      resp = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'text/xml' }, body: '<soapenv:Envelope/>' });
      text = await resp.text();
    } catch (e) {
      fetchErr = e;
    }

    const overlay = el('div', { class: 'kairo-dialog-overlay' });
    const dialog = el('div', { class: 'kairo-dialog kairo-dialog-wide' });
    dialog.appendChild(el('div', { class: 'kairo-dialog-title', text: '测试访问：' + path }));

    if (fetchErr) {
      dialog.appendChild(el('div', { class: 'svc-warn', text: '请求失败：' + (fetchErr.message || fetchErr) }));
    } else {
      const statusCls = resp.ok ? 'ok' : 'err';
      const statusLine = el('div', { class: 'svc-mock-test-status ' + statusCls });
      statusLine.appendChild(el('span', { text: resp.status + ' ' + resp.statusText }));
      statusLine.appendChild(el('span', { text: ' · ' + text.length + 'B' }));
      dialog.appendChild(statusLine);

      // headers
      const hBox = el('div', { class: 'svc-mock-test-headers' });
      hBox.appendChild(el('div', { class: 'svc-section-title', text: '响应 Headers' }));
      resp.headers.forEach((v, k) => {
        const row = el('div', { class: 'svc-mock-test-header-row' });
        row.appendChild(el('span', { class: 'svc-k', text: k }));
        row.appendChild(el('span', { class: 'svc-v', text: v }));
        hBox.appendChild(row);
      });
      dialog.appendChild(hBox);

      // body
      const bBox = el('div', { class: 'svc-mock-test-body' });
      bBox.appendChild(el('div', { class: 'svc-section-title', text: '响应 Body' }));
      const pre = el('pre', { class: 'svc-mock-test-body-pre' });
      pre.textContent = text;
      bBox.appendChild(pre);
      dialog.appendChild(bBox);
    }

    const actions = el('div', { class: 'kairo-dialog-actions' });
    const copyBtn = el('button', { class: 'btn', type: 'button', text: '复制 body', onclick: () => {
      if (text) copyToClipboard(text).then(() => toast('已复制', 'ok'), () => toast('复制失败', 'err'));
    } });
    const closeBtn = el('button', { class: 'btn btn-primary', type: 'button', text: '关闭', onclick: close });
    if (!fetchErr) actions.appendChild(copyBtn);
    actions.appendChild(closeBtn);
    dialog.appendChild(actions);

    overlay.appendChild(dialog);
    overlay.addEventListener('click', (ev) => { if (ev.target === overlay) close(); });
    const onKey = (ev) => { if (ev.key === 'Escape') close(); };
    document.addEventListener('keydown', onKey, true);
    let settled = false;
    function close() {
      if (settled) return;
      settled = true;
      document.removeEventListener('keydown', onKey, true);
      try { overlay.remove(); } catch (e) { /* ignore */ }
    }
    document.body.appendChild(overlay);
  }

  async function viewMockRecords() {
    try {
      const r = await getJSON('/api/soap/mocks/records');
      const records = (r && r.records) || [];
      if (records.length === 0) { toast('暂无 mock 请求记录', 'info'); return; }

      // 弹模态框显示记录列表（不只复制到剪贴板，原版 UX 不直观）
      const overlay = el('div', { class: 'kairo-dialog-overlay' });
      const dialog = el('div', { class: 'kairo-dialog kairo-dialog-wide' });
      dialog.appendChild(el('div', { class: 'kairo-dialog-title', text: 'Mock 请求记录（共 ' + records.length + ' 条）' }));

      // 顶部操作栏：刷新 + 清空
      const topBar = el('div', { class: 'svc-mock-records-bar' });
      const btnRefresh = el('button', { class: 'btn btn-mini', text: '刷新', onclick: () => { close(); viewMockRecords(); } });
      const btnClear = el('button', { class: 'btn btn-mini', text: '清空全部', onclick: async () => {
        if (!await confirmDialog('清空全部 mock 请求记录？')) return;
        try {
          await deleteJSON('/api/soap/mocks/records');
          toast('记录已清空', 'ok');
          close();
        } catch (e) { toast('清空失败：' + (e.message || e), 'err'); }
      }});
      topBar.appendChild(btnRefresh);
      topBar.appendChild(btnClear);
      dialog.appendChild(topBar);

      // 记录列表（最多显示 200 条，剩下的提示去 data/ 查）
      const list = el('div', { class: 'svc-mock-records-list' });
      const display = records.slice(0, 200);
      display.forEach((rec, i) => {
        const item = el('div', { class: 'svc-mock-record-item' });
        const head = el('div', { class: 'svc-mock-record-head' });
        head.appendChild(el('span', { class: 'svc-mock-record-time', text: fmtTime(rec.time) || '-' }));
        head.appendChild(el('span', { class: 'svc-mock-record-method', text: rec.method || 'POST' }));
        head.appendChild(el('span', { class: 'svc-mock-record-path', text: rec.path || '-' }));
        if (rec.body) head.appendChild(el('span', { class: 'svc-mock-record-size', text: 'body ' + rec.body.length + 'B' }));
        item.appendChild(head);
        if (rec.body) {
          const bodyPre = el('pre', { class: 'svc-mock-record-body' });
          bodyPre.textContent = rec.body.length > 2000 ? rec.body.slice(0, 2000) + '\n... (truncated)' : rec.body;
          item.appendChild(bodyPre);
        }
        list.appendChild(item);
      });
      dialog.appendChild(list);

      if (records.length > 200) {
        dialog.appendChild(el('div', { class: 'muted', style: 'font-size:12px;margin-top:8px', text: '只显示前 200 条，全部记录在 data/soap_mock_records.json' }));
      }

      const actions = el('div', { class: 'kairo-dialog-actions' });
      const closeBtn = el('button', { class: 'btn', type: 'button', text: '关闭', onclick: close });
      actions.appendChild(closeBtn);
      dialog.appendChild(actions);

      overlay.appendChild(dialog);
      overlay.addEventListener('click', (ev) => { if (ev.target === overlay) close(); });
      const onKey = (ev) => { if (ev.key === 'Escape') close(); };
      document.addEventListener('keydown', onKey, true);

      let settled = false;
      function close() {
        if (settled) return;
        settled = true;
        document.removeEventListener('keydown', onKey, true);
        try { overlay.remove(); } catch (e) { /* ignore */ }
      }
      document.body.appendChild(overlay);
    } catch (e) { toast('读取失败：' + (e.message || e), 'err'); }
  }

  // ---------- 数据刷新 ----------

  async function refreshAll() {
    await Promise.all([refreshProjects(), refreshTemplates(), refreshHistory(), refreshMocks()]);
    rerenderSidebar();
  }
  async function refreshProjects() {
    try {
      const r = await getJSON('/api/wsdl/projects');
      state.wsdlProjects = (r && r.projects) || [];
    } catch (e) { toast('加载 WSDL 项目失败：' + (e.message || e), 'err'); }
  }
  async function refreshTemplates() {
    try {
      const r = await getJSON('/api/soap/templates');
      state.templates = (r && r.templates) || [];
    } catch (e) { toast('加载模板失败：' + (e.message || e), 'err'); }
  }
  async function refreshHistory() {
    try {
      const r = await getJSON('/api/soap/history');
      state.history = (r && r.history) || [];
      // 顺手修：发送请求后 sidebar 历史计数要立即刷新，不要等切到历史 tab 才更新
      rerenderSidebar();
    } catch (e) { toast('加载历史失败：' + (e.message || e), 'err'); }
  }
  async function refreshMocks() {
    try {
      const r = await getJSON('/api/soap/mocks');
      state.mocks = (r && r.mocks) || [];
    } catch (e) { toast('加载 Mock 列表失败：' + (e.message || e), 'err'); }
  }

  // ---------- 入口 ----------

  async function renderWebService(view) {
    // 初次进入：拉取所有数据
    await refreshAll();
    const layout = el('div', { class: 'svc-layout' }, [renderSidebar(), renderMain()]);
    view.appendChild(layout);
  }

  Kairo.pages.webservice = renderWebService;
  Kairo.state.routes.webservice = renderWebService;
  Kairo.state.routeNames.webservice = 'WebService';
  Kairo.state.routeSubs.webservice = 'SOAP/WSDL 调试 · Mock 服务';
})();
