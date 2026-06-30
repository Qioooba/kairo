/* ===== web/pages/http.js =====
 * HTTP 测试页 v0.7-Redesign（Postman 风格）
 *
 * 相对于 v0.7 的改进：
 *   - 键值对行编辑器：Headers / form-data / url-encoded 都是「enable + key + value + 删除」行；
 *     点 + 加行；粘文本自动解析为行；粘 cURL 命令自动展开。
 *   - Body 多模式：none / form-data / url-encoded / raw(json|xml|text|html|binary)
 *   - 响应区：Pretty(JSON/XML/HTML 自动高亮 + 折叠) / Raw / Preview(HTML iframe) 三视图
 *     + 复制响应 / 复制为 cURL / 复制响应头 / 下载 body 四按钮 + 自动按 Content-Type 检测语言
 *   - 用例管理：左侧栏搜索框 + 每行 hover 显示 复制 / 编辑 / 删除 三按钮 + dirty 标记
 *   - 防误覆盖：回填后改任意字段就标记为 dirty，标题旁显示「● 已修改」+ 保存按钮变橘色 + 提示文案
 *   - 复制用例：弹窗让改名字和分组 → 另存为新用例
 *   - 导出 / 导入：全部用例 + envs 一键 JSON；导入支持「合并」/「覆盖」两种模式
 *   - Method 配色：与 Postman 一致（GET 绿 / POST 橙 / PUT 蓝 / DELETE 红 / PATCH 紫 / HEAD/OPTIONS 灰）
 *   - URL 行：method 下拉 + url 输入 + Send 按钮 三合一卡片化
 *
 * 数据结构（向后兼容 v0.7）：
 *   case 仍为 {id, group, name, method, url, headers, body, timeout_ms, follow_redirect, insecure_tls}
 *   v0.7-Redesign 多存了：body_mode / body_type（form/x-www-form-urlencoded/raw 类型），向后兼容老用例
 *
 * 后端不变：/api/http/request、/api/http/cases、/api/http/envs。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, toast, copyToClipboard, confirmDialog, escapeHtml, notify } = Kairo.core;
  const { api } = Kairo.api;

  const LS_LAST = 'kairo:http:last:v2';

  // ---------- 工具 ----------

  function uid() {
    return 'c' + Date.now().toString(16) + Math.floor(Math.random() * 0xffffff).toString(16);
  }

  function fmtBytes(n) {
    if (!n || n < 0) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
  }

  // 解析「Key: Value」或「Key=Value」一行；返回 [key, value] 或 null
  function parseKvLine(line) {
    if (!line) return null;
    const idx = line.indexOf(':');
    if (idx < 0) return null;
    const k = line.slice(0, idx).trim();
    const v = line.slice(idx + 1).trim();
    if (!k) return null;
    return [k, v];
  }

  // 尝试从一段文本里提取键值对（多行 / cURL 命令 / Tab 分隔）
  // 支持格式：每行 "Key: Value"，cURL "-H 'Key: Value'"，cURL "--data 'k=v&k2=v2'"
  function parseBulkHeaders(text) {
    const out = [];
    if (!text) return out;
    const lines = text.split(/\r?\n/);
    for (let raw of lines) {
      let line = raw.trim();
      if (!line) continue;
      // cURL -H 'Key: Value' / -H "Key: Value"
      const m = line.match(/^-H\s+(['"])(.+?)\1/);
      if (m) {
        const kv = parseKvLine(m[2]);
        if (kv) { out.push({ enabled: true, key: kv[0], value: kv[1] }); continue; }
      }
      // cURL --header 'Key: Value'
      const m2 = line.match(/^--header\s+(['"])(.+?)\1/);
      if (m2) {
        const kv = parseKvLine(m2[2]);
        if (kv) { out.push({ enabled: true, key: kv[0], value: kv[1] }); continue; }
      }
      // cURL --data 'k=v&...' 或 -d 同理
      const m3 = line.match(/^(-d|--data|--data-raw)\s+(['"])(.+?)\2/);
      if (m3) {
        const pairs = m3[3].split('&');
        for (const p of pairs) {
          const idx = p.indexOf('=');
          if (idx < 0) continue;
          const k = decodeURIComponent(p.slice(0, idx).replace(/\+/g, ' '));
          const v = decodeURIComponent(p.slice(idx + 1).replace(/\+/g, ' '));
          out.push({ enabled: true, key: k, value: v });
        }
        continue;
      }
      // 标准 "Key: Value" / "Key\tValue"
      const kv = parseKvLine(line);
      if (kv) out.push({ enabled: true, key: kv[0], value: kv[1] });
    }
    return out;
  }

  // 将键值对数组序列化为 map（忽略 disabled 和空 key）
  function kvArrayToMap(arr) {
    const m = {};
    (arr || []).forEach(r => {
      if (!r || !r.enabled) return;
      const k = (r.key || '').trim();
      if (!k) return;
      if (Object.prototype.hasOwnProperty.call(m, k)) {
        toast('检测到重复的 key："' + k + '"，后值将覆盖前值', 'warn');
      }
      m[k] = r.value || '';
    });
    return m;
  }

  // 将 map 转为键值对数组（加载历史用例时用）
  function mapToKvArray(m) {
    if (!m || typeof m !== 'object') return [];
    return Object.keys(m).map(k => ({ enabled: true, key: k, value: m[k] }));
  }

  function mapToTextarea(m) {
    if (!m || typeof m !== 'object') return '';
    return Object.keys(m).map(k => k + ': ' + m[k]).join('\n');
  }

  // 环境变量替换
  function applyEnvs(text, envs) {
    if (text === undefined || text === null) return '';
    text = String(text);
    if (!envs) return text;
    return text.replace(/\{\{\s*([a-zA-Z0-9_.\-]+)\s*\}\}/g, function (m, k) {
      const v = envs[k];
      return (v === undefined || v === null) ? m : String(v);
    });
  }

  // ---------- 语法高亮 ----------
  // 简易 JSON / XML / HTML 高亮（不引外部依赖；按 token 切分后再用 innerHTML 注入）。
  // 用法：highlightJSON(text) → HTML 字符串；highlightXML(text) → HTML 字符串。

  function escapeHtmlLocal(s) { return escapeHtml(s); }

  function highlightJSON(text) {
    // 先转义
    let html = escapeHtmlLocal(text);
    // 顺序：键、字符串、数字、布尔、null、标点
    html = html.replace(/(&quot;[^"]*?&quot;)(\s*:)/g, '<span class="s-key">$1</span>$2');
    html = html.replace(/: (&quot;[^"]*?&quot;)/g, ': <span class="s-string">$1</span>');
    html = html.replace(/: (-?\d+\.?\d*([eE][+-]?\d+)?)/g, ': <span class="s-number">$1</span>');
    html = html.replace(/\b(true|false)\b/g, '<span class="s-bool">$1</span>');
    html = html.replace(/\b(null)\b/g, '<span class="s-null">$1</span>');
    // 顶级数组元素里的字符串
    html = html.replace(/(\[|, )(&quot;[^"]*?&quot;)/g, '$1<span class="s-string">$2</span>');
    return html;
  }

  function highlightXML(text) {
    let html = escapeHtmlLocal(text);
    // 注释
    html = html.replace(/(&lt;!--[\s\S]*?--&gt;)/g, '<span class="s-comment">$1</span>');
    // 标签 + 属性
    html = html.replace(/(&lt;\/?)([a-zA-Z][\w:-]*)/g, '$1<span class="s-tag">$2</span>');
    html = html.replace(/([a-zA-Z\-:]+)=(&quot;[^&]*?&quot;)/g, '<span class="s-attr">$1</span>=<span class="s-val">$2</span>');
    return html;
  }

  function highlightHTML(text) { return highlightXML(text); }

  // 自动检测语言
  function detectLang(contentType, body) {
    const ct = (contentType || '').toLowerCase();
    if (ct.includes('json')) return 'json';
    if (ct.includes('html')) return 'html';
    if (ct.includes('xml')) return 'xml';
    if (ct.includes('text/plain')) return 'text';
    // 没给 ct 时按内容猜
    const s = (body || '').trim();
    if (!s) return 'text';
    if (s[0] === '{' || s[0] === '[') {
      try { JSON.parse(s); return 'json'; } catch (e) { /* not json */ }
    }
    if (s.startsWith('<?xml')) return 'xml';
    if (s.startsWith('<!DOCTYPE html') || /<html[\s>]/i.test(s)) return 'html';
    return 'text';
  }

  // 尝试美化 JSON（成功返字符串，失败抛）
  function prettyJSON(text, indent) {
    const ind = indent || 2;
    return JSON.stringify(JSON.parse(text), null, ind);
  }

  // ---------- 视图 ----------

  function renderHTTP(view) {
    // ===== state =====
    let allCases = [];
    let allEnvs = [];
    let activeEnv = '';
    let lastLoadedCaseId = '';
    let lastLoadedCaseName = '';
    let lastLoadedCaseGroup = '';
    // 当前 UI 内容快照（用于 dirty 检测）
    let pristineSnapshot = '';
    // 当前响应
    let lastResponse = null; // httpRequestResp
    let responseView = 'pretty'; // pretty | raw | preview
    // 当前请求参数缓存（响应生成 cURL 用）
    let lastReqParams = null;
    // 保存区相关（markDirty 在多个 listener 里被调用，必须先声明占位避免 TDZ；
    // 真正 DOM 创建在 doSave 之后）
    let saveBtn = null;
    let saveMeta = null;
    let saveGroupInp = null;
    let saveNameInp = null;
    let saveRow = null;
    let isDirty = false;
    // 响应区辅助元素占位（renderRespBody 在初始 setRespView 时会被调用）
    let respLangBadge = null;

    // ===== 顶部：env 组下拉 + 用例侧栏 =====

    function refreshEnvSel() {
      envSel.innerHTML = '';
      envSel.appendChild(el('option', { value: '', text: '（无环境变量）' }));
      allEnvs.forEach(e => {
        envSel.appendChild(el('option', { value: e.name, text: e.name }));
      });
      envSel.value = activeEnv && allEnvs.find(e => e.name === activeEnv) ? activeEnv : '';
    }

    const envSel = el('select', { class: 'env-select', id: 'http2-env-sel' });
    envSel.addEventListener('change', () => { activeEnv = envSel.value; markDirty(); });

    // ---------- 加载 / 保存 ----------

    async function loadAll() {
      try {
        const r = await api('GET', '/api/http/cases');
        allCases = r.cases || [];
        allEnvs = r.envs || [];
        refreshCasesUI();
        refreshEnvSel();
        rebuildEnvMgr();
      } catch (e) {
        toast('加载用例失败：' + e.message, 'err');
      }
    }

    async function saveEnvsToBackend() {
      try {
        await api('POST', '/api/http/envs', { envs: allEnvs });
      } catch (e) {
        toast('保存 env 失败：' + e.message, 'err');
      }
    }

    // ---------- 键值对行编辑器（通用） ----------

    function buildKvEditor(initialArr, opts) {
      opts = opts || {};
      const wrap = el('div', { class: 'http2-kv-table' });
      const rows = []; // {enabled, key, value, els}

      function renderRow(rowData, addToRows) {
        if (addToRows === undefined) addToRows = true;
        const check = el('input', { type: 'checkbox' });
        check.checked = rowData.enabled !== false;
        check.setAttribute('aria-label', '启用/禁用该行');
        check.title = '启用/禁用该行';
        const keyInp = el('input', { type: 'text', class: 'kv-key', placeholder: opts.keyPh || 'key', value: rowData.key || '' });
        const valInp = el('input', { type: 'text', class: 'kv-val', placeholder: opts.valPh || 'value', value: rowData.value || '' });
        const delBtn = el('button', { class: 'kv-del', text: '×', title: '删除该行' });
        const row = el('div', { class: 'kv-row' });
        if (!check.checked) row.classList.add('is-disabled');
        row.appendChild(el('div', { class: 'kv-check' }, [check]));
        row.appendChild(keyInp);
        row.appendChild(valInp);
        row.appendChild(delBtn);
        wrap.appendChild(row);

        const entry = { enabled: check.checked, key: keyInp.value, value: valInp.value, row, check, keyInp, valInp, delBtn };
        check.addEventListener('change', () => {
          entry.enabled = check.checked;
          row.classList.toggle('is-disabled', !check.checked);
          markDirty();
        });
        keyInp.addEventListener('input', () => { entry.key = keyInp.value; markDirty(); });
        valInp.addEventListener('input', () => { entry.value = valInp.value; markDirty(); });
        // 粘贴文本到 key 字段：自动拆成多行
        keyInp.addEventListener('paste', (e) => {
          const txt = (e.clipboardData || window.clipboardData).getData('text');
          if (!txt || (!txt.includes('\n') && !txt.includes(':'))) return;
          e.preventDefault();
          const parsed = parseBulkHeaders(txt);
          if (parsed.length === 0) return;
          entry.key = keyInp.value;
          entry.value = valInp.value;
          const myIdx = rows.indexOf(entry);
          let insertBefore = entry.row.nextSibling;
          parsed.forEach((p, i) => {
            const newEntry = renderRow(p, false);
            wrap.insertBefore(newEntry.row, insertBefore);
            rows.splice(myIdx + 1 + i, 0, newEntry);
          });
          markDirty();
        });
        // 在 key 上回车 → 自动追加空行并聚焦
        keyInp.addEventListener('keydown', (e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            const newEntry = renderRow({ enabled: true, key: '', value: '' }, false);
            wrap.insertBefore(newEntry.row, row.nextSibling);
            rows.splice(rows.indexOf(entry) + 1, 0, newEntry);
            newEntry.keyInp.focus();
          }
          if (e.key === 'Backspace' && !keyInp.value && !valInp.value) {
            e.preventDefault();
            delBtn.click();
          }
        });
        valInp.addEventListener('keydown', (e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            const newEntry = renderRow({ enabled: true, key: '', value: '' }, false);
            wrap.insertBefore(newEntry.row, row.nextSibling);
            rows.splice(rows.indexOf(entry) + 1, 0, newEntry);
            newEntry.keyInp.focus();
          }
        });
        valInp.addEventListener('paste', (e) => {
          const txt = (e.clipboardData || window.clipboardData).getData('text');
          if (!txt || (!txt.includes('\n') && !txt.includes(':'))) return;
          e.preventDefault();
          const parsed = parseBulkHeaders(txt);
          if (parsed.length === 0) return;
          entry.key = keyInp.value;
          entry.value = valInp.value;
          const myIdx = rows.indexOf(entry);
          let insertBefore = entry.row.nextSibling;
          parsed.forEach((p, i) => {
            const newEntry = renderRow(p, false);
            wrap.insertBefore(newEntry.row, insertBefore);
            rows.splice(myIdx + 1 + i, 0, newEntry);
          });
          markDirty();
        });
        delBtn.addEventListener('click', () => {
          const idx = rows.indexOf(entry);
          if (idx >= 0) {
            rows.splice(idx, 1);
            row.remove();
            // 删除最后一行后自动补一个空行，避免编辑器被清空后无入口继续添加
            if (rows.length === 0) {
              renderRow({ enabled: true, key: '', value: '' });
            }
            markDirty();
          }
        });

        if (addToRows) {
          rows.push(entry);
        }
        return entry;
      }

      function setRows(arr) {
        // 清空
        rows.length = 0;
        while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
        (arr || []).forEach(r => renderRow(r));
        if (rows.length === 0) {
          renderRow({ enabled: true, key: '', value: '' });
        }
      }
      function getRows() {
        return rows.map(r => ({
          enabled: r.check.checked,
          key: r.keyInp.value,
          value: r.valInp.value,
        }));
      }
      function addRow(initial) {
        const e = renderRow(initial || { enabled: true, key: '', value: '' });
        return e;
      }
      function clearAll() {
        rows.length = 0;
        while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
      }

      setRows(initialArr || []);
      return { wrap, setRows, getRows, addRow, clearAll };
    }

    // ---------- 主请求区：URL 行 ----------

    const methodSel = el('select', { class: 'http2-method-sel', id: 'http2-method' });
    ['GET', 'POST', 'PUT', 'DELETE', 'HEAD', 'PATCH', 'OPTIONS'].forEach(m => {
      methodSel.appendChild(el('option', { value: m, text: m }));
    });
    methodSel.value = 'GET';
    methodSel.addEventListener('change', markDirty);

    const urlInp = el('input', { type: 'text', class: 'http2-url-input', id: 'http2-url',
      placeholder: '输入 URL，例如 https://api.example.com/v1/health' });
    urlInp.addEventListener('input', markDirty);
    urlInp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); doSend(); }
    });

    const btnSend = el('button', { class: 'http2-send-btn', onclick: doSend }, [
      el('span', { class: 'send-icon', text: '▶' }),
      el('span', { text: 'Send' }),
    ]);

    const urlRow = el('div', { class: 'http2-url-row' }, [methodSel, urlInp, btnSend]);

    // 选项行
    const timeoutInp = el('input', { type: 'number', value: '15000', min: '100', max: '300000', step: '100' });
    timeoutInp.addEventListener('input', markDirty);
    const cbFollow = el('input', { type: 'checkbox', checked: true });
    cbFollow.addEventListener('change', markDirty);
    const cbInsecure = el('input', { type: 'checkbox' });
    cbInsecure.addEventListener('change', markDirty);
    const cbPretty = el('input', { type: 'checkbox', checked: true });
    cbPretty.id = 'http2-pretty';

    const optRow = el('div', { class: 'http2-opt-row' }, [
      el('label', { class: 'opt-num' }, [el('span', { text: '超时' }), timeoutInp, el('span', { text: 'ms' })]),
      el('label', { class: 'opt' }, [cbFollow, document.createTextNode(' 跟随重定向')]),
      el('label', { class: 'opt' }, [cbInsecure, document.createTextNode(' 跳过 TLS 校验')]),
      el('label', { class: 'opt-num' }, [el('span', { text: 'env:' }), envSel]),
      el('label', { class: 'opt' }, [cbPretty, document.createTextNode(' 自动美化响应')]),
    ]);

    // ---------- Headers 编辑器 ----------

    const headersEditor = buildKvEditor([
      { enabled: true, key: 'Content-Type', value: 'application/json' },
    ], { keyPh: 'Header', valPh: 'Value' });
    headersEditor.wrap.addEventListener('input', markDirty);
    const headersAddBtn = el('button', { class: 'http2-kv-add', text: '+ 添加 Header（支持粘贴 cURL 多行）' });
    headersAddBtn.addEventListener('click', () => {
      headersEditor.addRow({ enabled: true, key: '', value: '' });
      markDirty();
    });

    // ---------- Body 模式切换 ----------

    let bodyMode = 'raw'; // none | formdata | urlencoded | raw
    let bodyRawType = 'json'; // json | text | xml | html
    const bodyTa = el('textarea', { class: 'http2-raw-textarea', id: 'http2-body',
      placeholder: '{"hello":"world"}', spellcheck: 'false' });
    bodyTa.style.minHeight = '240px';
    bodyTa.addEventListener('input', markDirty);

    const formdataEditor = buildKvEditor([], { keyPh: '字段名', valPh: '值' });
    const urlencEditor = buildKvEditor([], { keyPh: '字段名', valPh: '值' });
    [formdataEditor, urlencEditor].forEach(ed => ed.wrap.addEventListener('input', markDirty));

    const bodyTypeSel = el('select');
    [
      { v: 'json', t: 'JSON' }, { v: 'text', t: 'Text' },
      { v: 'xml', t: 'XML' }, { v: 'html', t: 'HTML' },
    ].forEach(o => bodyTypeSel.appendChild(el('option', { value: o.v, text: o.t })));
    bodyTypeSel.value = 'json';
    bodyTypeSel.addEventListener('change', () => {
      bodyRawType = bodyTypeSel.value;
      autoSetContentType();
      markDirty();
    });

    const bodyIndentSel = el('select');
    [2, 4].forEach(n => bodyIndentSel.appendChild(el('option', { value: String(n), text: n + ' 空格' })));
    bodyIndentSel.value = '2';

    const btnFmtBody = el('button', { class: 'btn btn-mini', text: '美化', onclick: () => {
      const text = bodyTa.value.trim();
      if (!text) { toast('请先输入要格式化的内容', 'warn'); return; }
      if (bodyRawType === 'json') {
        try { bodyTa.value = prettyJSON(text, parseInt(bodyIndentSel.value, 10)); toast('已美化 JSON', 'ok'); }
        catch (e) { toast('内容不是合法 JSON，请检查类型选择是否正确', 'err'); }
      } else if (bodyRawType === 'xml' || bodyRawType === 'html') {
        try {
          const pretty = text.replace(/>\s*</g, '><').replace(/></g, '>\n<');
          bodyTa.value = pretty;
          toast('已格式化', 'ok');
        } catch (e) { toast('格式化失败', 'err'); }
      } else {
        toast('Text 模式不支持美化', 'warn');
      }
      markDirty();
    }});

    const btnClearBody = el('button', { class: 'btn btn-mini', text: '清空', onclick: () => {
      bodyTa.value = ''; formdataEditor.setRows([]); urlencEditor.setRows([]);
      markDirty();
    }});

    const formAddBtn = el('button', { class: 'http2-kv-add', text: '+ 添加字段' });
    formAddBtn.addEventListener('click', () => { formdataEditor.addRow(); markDirty(); });
    const urlAddBtn = el('button', { class: 'http2-kv-add', text: '+ 添加字段' });
    urlAddBtn.addEventListener('click', () => { urlencEditor.addRow(); markDirty(); });

    const bodyTabs = el('div', { class: 'http2-tabs' });

    const bodyNone = el('div', { class: 'http2-mute', style: 'padding:12px 0;text-align:center;', text: '此请求不发送 Body' });

    const bodyForm = el('div');
    bodyForm.appendChild(el('div', { class: 'http2-section-desc', text: 'multipart/form-data（每行一个字段，key 留空删除）' }));
    bodyForm.appendChild(formdataEditor.wrap);
    bodyForm.appendChild(formAddBtn);

    const bodyUrl = el('div');
    bodyUrl.appendChild(el('div', { class: 'http2-section-desc', text: 'application/x-www-form-urlencoded（每行一个字段）' }));
    bodyUrl.appendChild(urlencEditor.wrap);
    bodyUrl.appendChild(urlAddBtn);

    const bodyRaw = el('div', { class: 'http2-raw-wrap' });
    const bodyMeta = el('div', { class: 'http2-raw-meta' }, [
      el('span', { text: '0 行 · 0 B' }),
    ]);
    const bodyToolbar = el('div', { class: 'http2-raw-toolbar' });
    bodyToolbar.appendChild(el('div', { class: 'http2-raw-toolbar-left' }, [
      el('span', { text: '类型:' }), bodyTypeSel,
      el('span', { text: ' 缩进:' }), bodyIndentSel,
    ]));
    bodyToolbar.appendChild(el('div', { class: 'http2-raw-toolbar-right' }, [btnFmtBody, btnClearBody]));
    bodyRaw.appendChild(bodyToolbar);
    bodyRaw.appendChild(bodyTa);
    bodyRaw.appendChild(bodyMeta);
    function updateBodyMeta() {
      const v = bodyTa.value;
      const lines = v.split('\n').length;
      const bytes = new Blob([v]).size;
      bodyMeta.children[0].textContent = lines + ' 行 · ' + fmtBytes(bytes);
    }
    bodyTa.addEventListener('input', updateBodyMeta);
    bodyTa.addEventListener('focus', () => bodyRaw.classList.add('focused'));
    bodyTa.addEventListener('blur', () => bodyRaw.classList.remove('focused'));
    updateBodyMeta();

    function setBodyMode(mode) {
      bodyMode = mode;
      Array.from(bodyTabs.children).forEach((b, i) => {
        const m = ['none', 'formdata', 'urlencoded', 'raw'][i];
        b.classList.toggle('active', m === mode);
      });
      bodyNone.style.display = mode === 'none' ? '' : 'none';
      bodyForm.style.display = mode === 'formdata' ? '' : 'none';
      bodyUrl.style.display = mode === 'urlencoded' ? '' : 'none';
      bodyRaw.style.display = mode === 'raw' ? '' : 'none';
      autoSetContentType();
      markDirty();
    }
    [['none', 'none'], ['formdata', 'form-data'], ['urlencoded', 'url-encoded'], ['raw', 'raw']].forEach(([m, t]) => {
      bodyTabs.appendChild(el('button', { class: 'http2-tab', text: t, onclick: () => setBodyMode(m) }));
    });
    setBodyMode('raw');

    // 根据 bodyMode + bodyRawType 自动设/清 Content-Type（除非用户已手动改）
    function autoSetContentType() {
      // 用户手动改过的标志：检测 Content-Type 是否存在
      const rows = headersEditor.getRows();
      const hasCT = rows.some(r => r.key.toLowerCase() === 'content-type');
      let ct = '';
      if (bodyMode === 'formdata') ct = ''; // 让 net/http 自动加 boundary
      else if (bodyMode === 'urlencoded') ct = 'application/x-www-form-urlencoded';
      else if (bodyMode === 'raw') {
        if (bodyRawType === 'json') ct = 'application/json';
        else if (bodyRawType === 'xml') ct = 'application/xml';
        else if (bodyRawType === 'html') ct = 'text/html';
        else ct = 'text/plain';
      }
      if (ct && !hasCT) {
        headersEditor.addRow({ enabled: true, key: 'Content-Type', value: ct });
      }
      // 不主动清；用户保留自己的 Content-Type
    }

    // ---------- 响应区 ----------

    const respStatus = el('div', { class: 'http2-resp-status', text: '未发送' });
    const respMeta = el('div', { class: 'http2-resp-meta' });
    const respSummary = el('div', { class: 'http2-resp-summary' }, [
      el('div', null, [respStatus, respMeta]),
    ]);

    const respHeadersPre = el('pre', { class: 'http2-resp-headers' });
    respHeadersPre.appendChild(el('span', { class: 'http2-mute', text: '（响应头会显示在这里）' }));

    const respBodyView = el('div', { class: 'http2-resp-body-wrap' });
    respBodyView.appendChild(el('div', { class: 'http2-resp-empty', text: '点击 Send 发送请求查看响应' }));

    const respViewWrap = el('div', { class: 'http2-resp-view' });
    const respTabBar = el('div', { class: 'http2-resp-tab-bar' });
    const respTabs = el('div', { class: 'http2-tabs' });
    function setRespView(v) {
      responseView = v;
      Array.from(respTabs.children).forEach(b => {
        b.classList.toggle('active', b.dataset.view === v);
      });
      renderRespBody();
    }
    ['pretty', 'raw', 'preview'].forEach(v => {
      const label = { pretty: 'Pretty', raw: 'Raw', preview: 'Preview' }[v];
      const btn = el('button', { class: 'http2-tab', text: label, onclick: () => setRespView(v) });
      btn.dataset.view = v;
      respTabs.appendChild(btn);
    });
    setRespView('pretty');

    respLangBadge = el('span', { class: 'http2-badge', text: '' });
    respTabBar.appendChild(respTabs);
    respTabBar.appendChild(respLangBadge);

    const respToolbar = el('div', { class: 'http2-resp-toolbar' });
    const respTrRight = el('div', { class: 'http2-raw-toolbar-right' });

    const btnCopyResp = el('button', { class: 'btn btn-mini', text: '复制 Body', onclick: () => {
      if (!lastResponse) { toast('响应为空', 'warn'); return; }
      copyToClipboard(lastResponse.body || '').then(() => toast('已复制', 'ok'), () => toast('复制失败', 'err'));
    }});
    const btnCopyCurl = el('button', { class: 'btn btn-mini', text: '复制为 cURL', onclick: () => {
      if (!lastReqParams) { toast('请先发送一次请求', 'warn'); return; }
      const curl = buildCurl(lastReqParams);
      copyToClipboard(curl).then(() => toast('已复制 cURL', 'ok'), () => toast('复制失败', 'err'));
    }});
    const btnCopyHeaders = el('button', { class: 'btn btn-mini', text: '复制 Headers', onclick: () => {
      if (!lastResponse || !lastResponse.headers) { toast('响应头为空', 'warn'); return; }
      const text = Object.keys(lastResponse.headers).map(k => k + ': ' + lastResponse.headers[k]).join('\n');
      copyToClipboard(text).then(() => toast('已复制', 'ok'), () => toast('复制失败', 'err'));
    }});
    const btnDownloadResp = el('button', { class: 'btn btn-mini', text: '下载 Body', onclick: () => {
      if (!lastResponse) { toast('响应为空', 'warn'); return; }
      const blob = new Blob([lastResponse.body || ''], { type: 'text/plain' });
      const url = URL.createObjectURL(blob);
      const a = el('a', { href: url, download: 'response-' + Date.now() + '.txt' });
      document.body.appendChild(a); a.click(); a.remove();
      URL.revokeObjectURL(url);
      toast('已下载', 'ok');
    }});

    respTrRight.appendChild(btnCopyHeaders);
    respTrRight.appendChild(btnCopyResp);
    respTrRight.appendChild(btnDownloadResp);
    respTrRight.appendChild(btnCopyCurl);
    respToolbar.appendChild(respTrRight);

    respViewWrap.appendChild(respTabBar);
    respViewWrap.appendChild(respToolbar);
    respViewWrap.appendChild(respHeadersPre);
    respViewWrap.appendChild(respBodyView);

    function renderRespBody() {
      respBodyView.innerHTML = '';
      if (!lastResponse) {
        respBodyView.appendChild(el('div', { class: 'http2-resp-empty', text: '点击 Send 发送请求查看响应' }));
        if (respLangBadge) respLangBadge.textContent = '';
        return;
      }
      if (!lastResponse.ok && lastResponse.error) {
        respBodyView.appendChild(el('div', { class: 'http2-resp-error', text: lastResponse.error }));
        if (respLangBadge) respLangBadge.textContent = 'error';
        return;
      }
      const body = lastResponse.body || '';
      const ct = (lastResponse.headers || {})['Content-Type'] || (lastResponse.headers || {})['content-type'] || '';
      const lang = detectLang(ct, body);
      if (respLangBadge) respLangBadge.textContent = lang.toUpperCase();

      if (responseView === 'preview' && lang !== 'html') {
        respBodyView.appendChild(el('div', { class: 'http2-resp-empty', text: '当前响应不是 HTML，Preview 视图仅对 HTML 有效。请切换到 Pretty 或 Raw。' }));
        return;
      }

      if (responseView === 'raw') {
        const ta = el('textarea', { class: 'http2-resp-body-raw', readonly: true, spellcheck: 'false' });
        ta.value = body;
        respBodyView.appendChild(ta);
        return;
      }

      if (responseView === 'preview' && lang === 'html') {
        const iframe = el('iframe', { class: 'http2-preview-frame', sandbox: 'allow-same-origin' });
        iframe.srcdoc = body;
        respBodyView.appendChild(iframe);
        return;
      }

      // Pretty
      if (lang === 'json') {
        let pretty = body;
        try { pretty = prettyJSON(body, 2); } catch (e) { /* 原样展示 */ }
        const pre = el('pre', { class: 'http2-syntax' });
        pre.innerHTML = highlightJSON(pretty);
        respBodyView.appendChild(pre);
      } else if (lang === 'xml' || lang === 'html') {
        const pre = el('pre', { class: 'http2-syntax' });
        pre.innerHTML = highlightHTML(body);
        respBodyView.appendChild(pre);
      } else {
        const pre = el('pre', { class: 'http2-syntax' });
        pre.textContent = body;
        respBodyView.appendChild(pre);
      }
    }

    function renderRespHeaders(headers) {
      respHeadersPre.innerHTML = '';
      if (!headers || Object.keys(headers).length === 0) {
        respHeadersPre.appendChild(el('span', { class: 'http2-mute', text: '（响应头会显示在这里）' }));
        return;
      }
      const lines = Object.keys(headers).sort().map(k => {
        return '<span class="hkey">' + escapeHtmlLocal(k) + '</span>: <span class="hval">' + escapeHtmlLocal(headers[k]) + '</span>';
      });
      respHeadersPre.innerHTML = lines.join('\n');
    }

    // ---------- 发送 ----------

    async function doSend() {
      const method = methodSel.value;
      const urlRaw = urlInp.value.trim();
      if (!urlRaw) { toast('URL 不能为空', 'warn'); urlInp.focus(); return; }
      const envsNow = activeEnv ? (allEnvs.find(e => e.name === activeEnv) || {}).vars || {} : {};
      const url = applyEnvs(urlRaw, envsNow);

      // headers
      const hdrsArr = headersEditor.getRows();
      const hdrs = kvArrayToMap(hdrsArr);
      const hdrsEnv = {};
      Object.keys(hdrs).forEach(k => { hdrsEnv[k] = applyEnvs(hdrs[k], envsNow); });

      // body
      let body = '';
      let contentTypeOverride = null;
      if (bodyMode === 'formdata') {
        // 真正的 multipart/form-data：生成 boundary，按 RFC 7578 拼装 parts
        const arr = formdataEditor.getRows().filter(r => r.enabled && r.key.trim());
        const boundary = '----opsFormBoundary' + Math.random().toString(36).slice(2, 12);
        const CRLF = '\r\n';
        const parts = arr.map(r => {
          const name = r.key;
          const value = r.value || '';
          const safeName = name.replace(/"/g, '%22');
          return '--' + boundary + CRLF
            + 'Content-Disposition: form-data; name="' + safeName + '"' + CRLF
            + 'Content-Type: text/plain; charset=utf-8' + CRLF
            + CRLF
            + value + CRLF;
        }).join('');
        body = parts + '--' + boundary + '--' + CRLF;
        contentTypeOverride = 'multipart/form-data; boundary=' + boundary;
      } else if (bodyMode === 'urlencoded') {
        const arr = urlencEditor.getRows().filter(r => r.enabled && r.key.trim());
        body = arr.map(r => encodeURIComponent(r.key) + '=' + encodeURIComponent(r.value || '')).join('&');
        contentTypeOverride = 'application/x-www-form-urlencoded';
      } else if (bodyMode === 'raw') {
        body = applyEnvs(bodyTa.value, envsNow);
        contentTypeOverride = null; // 走 headers 里的 Content-Type
      }
      // Content-Type 强制：先 headers 里有就尊重用户的，没有就补 contentTypeOverride
      if (contentTypeOverride && !Object.keys(hdrsEnv).some(k => k.toLowerCase() === 'content-type')) {
        hdrsEnv['Content-Type'] = contentTypeOverride;
      }

      const timeoutMs = parseInt(timeoutInp.value, 10) || 0;
      const followRedirect = cbFollow.checked;
      const insecureTLS = cbInsecure.checked;

      // UI busy
      btnSend.disabled = true;
      respStatus.className = 'http2-resp-status s-busy';
      respStatus.textContent = '';
      respStatus.appendChild(el('span', { class: 'dot-anim' }));
      respStatus.appendChild(document.createTextNode(' 请求中…'));
      respMeta.textContent = '';
      respHeadersPre.innerHTML = '';
      respBodyView.innerHTML = '';
      respBodyView.appendChild(el('div', { class: 'http2-resp-empty', text: '请求中…' }));

      lastReqParams = { method, url: urlRaw, headers: hdrs, body, timeoutMs, followRedirect, insecureTLS, envs: envsNow };

      const start = performance.now();
      try {
        const r = await api('POST', '/api/http/request', {
          method, url, headers: hdrsEnv, body, timeout_ms: timeoutMs,
          follow_redirect: followRedirect, insecure_tls: insecureTLS,
        });
        lastResponse = r;
        const total = Math.round(performance.now() - start);
        if (r.ok) {
          const cls = r.status >= 500 ? 's-5xx' : r.status >= 400 ? 's-4xx' : r.status >= 300 ? 's-3xx' : 's-2xx';
          respStatus.className = 'http2-resp-status ' + cls;
          respStatus.textContent = r.status + ' ' + (r.status_text || '');
          respMeta.innerHTML = '';
          respMeta.appendChild(el('span', null, [document.createTextNode('后端 '), el('b', { text: (r.elapsed_ms || 0) + 'ms' })]));
          respMeta.appendChild(el('span', null, [document.createTextNode('总耗时 '), el('b', { text: total + 'ms' })]));
          respMeta.appendChild(el('span', null, [document.createTextNode('大小 '), el('b', { text: fmtBytes(r.body_bytes || 0) })]));
          if (r.truncated) respMeta.appendChild(el('span', null, [document.createTextNode('⚠ 已截断（>1MB）')]));
          renderRespHeaders(r.headers || {});
          renderRespBody();
          if (r.final_url && r.final_url !== url) {
            respMeta.appendChild(el('span', { style: 'max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;', title: r.final_url, text: '→ ' + r.final_url }));
          }
        } else {
          respStatus.className = 'http2-resp-status s-err';
          respStatus.textContent = '错误';
          respStatus.title = r.error || '';
          renderRespHeaders(r.headers || {});
          renderRespBody();
        }
        try {
          const snap = { method, url: urlRaw };
          const hist = JSON.parse(localStorage.getItem('kairo:http:history') || '[]')
            .filter(s => !(s.method === method && s.url === urlRaw));
          hist.unshift(snap);
          localStorage.setItem('kairo:http:history', JSON.stringify(hist.slice(0, 20)));
        } catch (e) { /* ignore */ }
      } catch (e) {
        respStatus.className = 'http2-resp-status s-err';
        respStatus.textContent = '错误';
        respStatus.title = e.message || String(e);
        respBodyView.innerHTML = '';
        respBodyView.appendChild(el('div', { class: 'http2-resp-error', text: e.message || String(e) }));
        toast('请求失败：' + (e.message || e), 'err');
      } finally {
        btnSend.disabled = false;
      }
    }

    // ---------- 构造 cURL（复制为 cURL 用） ----------

    function buildCurl(p) {
      const lines = ['curl -X ' + p.method];
      const envs = p.envs || {};
      // url：环境变量替换后
      lines.push("  '" + applyEnvs(p.url, envs).replace(/'/g, "'\\''") + "'");
      Object.keys(p.headers).forEach(k => {
        lines.push("  -H '" + k + ': ' + applyEnvs(p.headers[k], envs).replace(/'/g, "'\\''") + "'");
      });
      if (p.body && p.method !== 'GET' && p.method !== 'HEAD') {
        const body = applyEnvs(p.body, envs).replace(/'/g, "'\\''");
        lines.push("  --data-raw '" + body + "'");
      }
      if (p.insecureTLS) lines.push('  --insecure');
      if (!p.followRedirect) lines.push("  --max-redirs 0");
      if (p.timeoutMs) lines.push('  --max-time ' + Math.ceil(p.timeoutMs / 1000));
      return lines.join(' \\\n');
    }

    // ---------- 用例管理（左侧栏） ----------

    const searchInp = el('input', { type: 'text', placeholder: '搜索用例名 / URL' });
    const searchIcon = el('span', { class: 'http2-sidebar-search-icon', text: '🔍' });
    const searchBox = el('div', { class: 'http2-sidebar-search' }, [searchIcon, searchInp]);
    searchInp.addEventListener('input', () => refreshCasesUI());

    const casesWrap = el('div', { class: 'http2-cases-wrap' });

    const btnExportAll = el('button', { class: 'btn', text: '导出', onclick: exportCases });
    const btnImportBtn = el('button', { class: 'btn', text: '导入', onclick: importCases });
    const btnNewCaseBtn = el('button', { class: 'btn', text: '+ 新用例', onclick: () => {
      // 清空 + 聚焦 URL 输入
      urlInp.value = '';
      methodSel.value = 'POST';
      headersEditor.setRows([{ enabled: true, key: 'Content-Type', value: 'application/json' }]);
      bodyTa.value = '';
      formdataEditor.setRows([]);
      urlencEditor.setRows([]);
      setBodyMode('raw');
      bodyTypeSel.value = 'json';
      autoSetContentType();
      // 重置保存元数据 + 清空分组/用例名输入框（避免误覆盖已有用例）
      saveGroupInp.value = '';
      saveNameInp.value = '';
      lastLoadedCaseId = '';
      lastLoadedCaseName = '';
      lastLoadedCaseGroup = '';
      pristineSnapshot = takeSnapshot();
      updateSaveMeta();
      urlInp.focus();
    }});
    const sidebarTools = el('div', { class: 'http2-sidebar-tools' }, [btnExportAll, btnImportBtn, btnNewCaseBtn]);

    const sidebar = el('div', { class: 'http2-sidebar' }, [
      el('h3', { text: '已保存用例' }),
      searchBox,
      sidebarTools,
      casesWrap,
    ]);

    function refreshCasesUI() {
      casesWrap.innerHTML = '';
      const q = (searchInp.value || '').trim().toLowerCase();
      const byGroup = {};
      allCases.forEach(c => {
        const g = c.group || 'default';
        if (!byGroup[g]) byGroup[g] = [];
        byGroup[g].push(c);
      });
      // 过滤
      Object.keys(byGroup).forEach(g => {
        byGroup[g] = byGroup[g].filter(c => {
          if (!q) return true;
          return (c.name || '').toLowerCase().includes(q)
            || (c.url || '').toLowerCase().includes(q)
            || (c.method || '').toLowerCase().includes(q);
        });
      });
      const groupNames = Object.keys(byGroup).filter(g => byGroup[g].length > 0).sort();
      if (allCases.length === 0) {
        casesWrap.appendChild(el('div', { class: 'http2-mute', style: 'padding: 16px 8px; text-align: center; font-size: 12px;',
          text: '暂无用例。配置好请求后点右下「保存」即可创建。' }));
        return;
      }
      if (groupNames.length === 0) {
        casesWrap.appendChild(el('div', { class: 'http2-mute', style: 'padding: 16px 8px; text-align: center; font-size: 12px;',
          text: '没有匹配的用例' }));
        return;
      }
      groupNames.forEach(g => {
        const head = el('div', { class: 'http2-case-group-head' });
        head.appendChild(el('span', { text: g }));
        head.appendChild(el('span', { class: 'http2-case-group-count', text: String(byGroup[g].length) }));
        const list = el('div', { class: 'http2-case-list' });
        const group = el('div', { class: 'http2-case-group' }, [head, list]);
        head.addEventListener('click', () => {
          list.style.display = list.style.display === 'none' ? '' : 'none';
        });
        byGroup[g].forEach(c => list.appendChild(buildCaseRow(c)));
        casesWrap.appendChild(group);
      });
    }

    function buildCaseRow(c) {
      const m = (c.method || 'GET').toUpperCase();
      const row = el('div', {
        class: 'http2-case-row' + (c.id === lastLoadedCaseId ? ' is-loaded' : ''),
        tabindex: '0',
        role: 'button',
        'aria-label': c.name || c.id
      });
      const methodChip = el('span', { class: 'http2-case-method m-' + m, text: m });
      const info = el('div', { class: 'http2-case-info' }, [
        el('div', { class: 'http2-case-name', text: c.name || c.id }),
        el('div', { class: 'http2-case-url', text: c.url || '' }),
      ]);
      const actions = el('div', { class: 'http2-case-actions' });
      const btnClone = el('button', { class: 'btn-icon', text: '⎘', title: '复制用例（改名另存）' });
      const btnEdit = el('button', { class: 'btn-icon', text: '✎', title: '改名 / 换分组' });
      const btnDel = el('button', { class: 'btn-icon del', text: '×', title: '删除' });
      actions.appendChild(btnClone);
      actions.appendChild(btnEdit);
      actions.appendChild(btnDel);
      row.appendChild(methodChip);
      row.appendChild(info);
      row.appendChild(actions);

      row.addEventListener('click', (e) => {
        if (e.target.closest('.http2-case-actions')) return;
        if (row.classList.contains('is-dirty')) {
          confirmDialog('当前请求有未保存修改，加载「' + c.name + '」会丢弃这些修改，确定？').then(ok => {
            if (ok) loadCase(c);
          });
        } else {
          loadCase(c);
        }
      });
      row.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          row.click();
        }
      });
      btnClone.addEventListener('click', (e) => {
        e.stopPropagation();
        openCloneCaseModal(c);
      });
      btnEdit.addEventListener('click', (e) => {
        e.stopPropagation();
        openRenameCaseModal(c);
      });
      btnDel.addEventListener('click', async (e) => {
        e.stopPropagation();
        if (!confirm('删除用例「' + (c.name || c.id) + '」？')) return;
        try {
          await api('DELETE', '/api/http/cases?id=' + encodeURIComponent(c.id));
          toast('已删除', 'ok');
          if (lastLoadedCaseId === c.id) {
            lastLoadedCaseId = '';
            lastLoadedCaseName = '';
            lastLoadedCaseGroup = '';
            pristineSnapshot = takeSnapshot();
            updateSaveMeta();
          }
          await loadAll();
        } catch (err) { toast('删除失败：' + err.message, 'err'); }
      });
      return row;
    }

    function loadCase(c) {
      methodSel.value = (c.method || 'GET').toUpperCase();
      urlInp.value = c.url || '';
      headersEditor.setRows(mapToKvArray(c.headers || {}));
      timeoutInp.value = String(c.timeout_ms || 15000);
      cbFollow.checked = c.follow_redirect !== false;
      cbInsecure.checked = !!c.insecure_tls;
      // body 模式
      const bm = c.body_mode || (c.body_type === 'form' ? 'formdata' : (c.body_type === 'urlencoded' ? 'urlencoded' : 'raw'));
      setBodyMode(bm);
      if (bm === 'raw') {
        bodyRawType = c.body_type || 'json';
        if (bodyRawType === 'json' || bodyRawType === 'xml' || bodyRawType === 'html' || bodyRawType === 'text') {
          bodyTypeSel.value = bodyRawType;
        }
        bodyTa.value = c.body || '';
        updateBodyMeta();
      } else if (bm === 'formdata') {
        formdataEditor.setRows(mapToKvArray(c.body_form || parseFormBody(c.body || '')));
      } else if (bm === 'urlencoded') {
        urlencEditor.setRows(mapToKvArray(c.body_form || parseFormBody(c.body || '')));
      }
      lastLoadedCaseId = c.id || '';
      lastLoadedCaseName = c.name || '';
      lastLoadedCaseGroup = c.group || '';
      pristineSnapshot = takeSnapshot();
      updateSaveMeta();
      refreshCasesUI();
      toast('已加载：' + c.name, 'ok');
    }

    function parseFormBody(s) {
      if (!s) return [];
      return s.split('&').map(p => {
        const idx = p.indexOf('=');
        if (idx < 0) return { enabled: true, key: decodeURIComponent(p), value: '' };
        return { enabled: true, key: decodeURIComponent(p.slice(0, idx)), value: decodeURIComponent(p.slice(idx + 1)) };
      });
    }

    // ---------- 保存区 ----------
    // saveBtn / saveMeta / saveGroupInp / saveNameInp / saveRow / isDirty 在顶部 state
    // 段已声明为 let 占位。markDirty 函数用 `if (saveBtn)` 守卫；真正 DOM 创建见下方
    // doSave 函数之后的"构造"段。

    function takeSnapshot() {
      return JSON.stringify({
        method: methodSel.value,
        url: urlInp.value,
        headers: headersEditor.getRows(),
        bodyMode, bodyRawType,
        body: bodyTa.value,
        formdata: formdataEditor.getRows(),
        urlencoded: urlencEditor.getRows(),
        timeoutMs: timeoutInp.value,
        follow: cbFollow.checked,
        insecure: cbInsecure.checked,
      });
    }

    function markDirty() {
      const cur = takeSnapshot();
      const dirty = cur !== pristineSnapshot;
      isDirty = dirty;
      // dirty 标记：侧栏
      document.querySelectorAll('.http2-case-row.is-loaded').forEach(r => {
        r.classList.toggle('is-dirty', dirty);
      });
      // 保存按钮 / 提示文本：saveBtn / saveMeta 可能还没创建（TDZ），延迟刷新
      if (saveBtn) updateSaveUI(dirty);
    }

    // 真正写 DOM 的部分：只允许在 saveBtn / saveMeta 已创建后调用
    function updateSaveUI(dirty) {
      saveBtn.classList.toggle('dirty', dirty);
      if (dirty) {
        if (lastLoadedCaseId) {
          saveMeta.textContent = '● 已修改 · 保存会覆盖「' + (lastLoadedCaseName || lastLoadedCaseId) + '」';
          saveMeta.classList.add('dirty');
        } else {
          saveMeta.textContent = '● 已修改 · 保存会创建新用例';
          saveMeta.classList.add('dirty');
        }
      } else {
        if (lastLoadedCaseId) {
          saveMeta.textContent = '当前：' + lastLoadedCaseGroup + ' / ' + lastLoadedCaseName;
          saveMeta.classList.remove('dirty');
        } else {
          saveMeta.textContent = '新建用例';
          saveMeta.classList.remove('dirty');
        }
      }
    }

    function updateSaveMeta() {
      // 直接重置 dirty 状态
      markDirty();
    }

    async function doSave() {
      const grp = (saveGroupInp.value || '').trim() || lastLoadedCaseGroup || 'default';
      const name = saveNameInp.value.trim();
      if (!name) {
        saveNameInp.value = lastLoadedCaseName || '';
        if (!saveNameInp.value) { toast('用例名不能为空', 'warn'); saveNameInp.focus(); return; }
      }
      // 拼 body
      let body = '', bodyModeOut = 'none', bodyForm = null;
      if (bodyMode === 'raw') {
        body = bodyTa.value;
        bodyModeOut = 'raw';
      } else if (bodyMode === 'formdata') {
        const arr = formdataEditor.getRows().filter(r => r.enabled && r.key.trim());
        body = arr.map(r => encodeURIComponent(r.key) + '=' + encodeURIComponent(r.value || '')).join('&');
        bodyModeOut = 'formdata';
        bodyForm = kvArrayToMap(arr);
      } else if (bodyMode === 'urlencoded') {
        const arr = urlencEditor.getRows().filter(r => r.enabled && r.key.trim());
        body = arr.map(r => encodeURIComponent(r.key) + '=' + encodeURIComponent(r.value || '')).join('&');
        bodyModeOut = 'urlencoded';
        bodyForm = kvArrayToMap(arr);
      }
      const payload = {
        case: {
          id: lastLoadedCaseId || '',
          group: grp,
          name: name,
          method: methodSel.value,
          url: urlInp.value,
          headers: kvArrayToMap(headersEditor.getRows()),
          body,
          body_mode: bodyModeOut,
          body_type: bodyRawType,
          body_form: bodyForm,
          timeout_ms: parseInt(timeoutInp.value, 10) || 0,
          follow_redirect: cbFollow.checked,
          insecure_tls: cbInsecure.checked,
        },
      };
      try {
        const r = await api('POST', '/api/http/cases', payload);
        lastLoadedCaseId = (r.case && r.case.id) || lastLoadedCaseId;
        lastLoadedCaseName = name;
        lastLoadedCaseGroup = grp;
        saveGroupInp.value = grp;
        saveNameInp.value = name;
        pristineSnapshot = takeSnapshot();
        updateSaveMeta();
        toast((r.replaced ? '已覆盖' : '已保存') + '：' + grp + ' / ' + name, 'ok');
        await loadAll();
      } catch (e) { toast('保存失败：' + e.message, 'err'); }
    }

    // 现在真正构造 saveBtn / saveMeta / saveRow（在 doSave 之后；
    // markDirty 通过 document.querySelector 懒查，TDZ 安全）
    saveGroupInp = el('input', { type: 'text', placeholder: '分组（默认 default）' });
    saveGroupInp.style.width = '160px';
    saveNameInp = el('input', { type: 'text', placeholder: '用例名' });
    saveNameInp.style.width = '180px';
    saveBtn = el('button', { class: 'http2-save-btn', text: '保存' });
    saveMeta = el('span', { class: 'save-meta', text: '新建用例' });
    saveBtn.addEventListener('click', doSave);
    saveRow = el('div', { class: 'http2-save-row' }, [
      el('span', { text: '保存为:', class: 'http2-mute', style: 'font-size:12px' }),
      saveGroupInp, saveNameInp, saveMeta, saveBtn,
    ]);
    // 补一次：把保存按钮 UI 同步成当前真实状态
    updateSaveUI(isDirty);

    // ---------- Modal（复制 / 改名） ----------

    function openModal(content) {
      const mask = el('div', { class: 'http2-modal-mask' });
      const modal = el('div', { class: 'http2-modal' });
      mask.appendChild(modal);
      modal.appendChild(content);
      document.body.appendChild(mask);
      const close = () => mask.remove();
      mask.addEventListener('click', (e) => { if (e.target === mask) close(); });
      return { mask, modal, close };
    }

    function openCloneCaseModal(c) {
      const h = el('h4', { text: '复制用例：' + c.name });
      const row1 = el('div', { class: 'modal-row' }, [
        el('label', { text: '分组' }),
        el('input', { type: 'text', value: c.group || 'default' }),
      ]);
      const row2 = el('div', { class: 'modal-row' }, [
        el('label', { text: '新名字' }),
        el('input', { type: 'text', value: (c.name || '') + ' 副本' }),
      ]);
      const actions = el('div', { class: 'modal-actions' });
      const cancelBtn = el('button', { class: 'btn', text: '取消' });
      const okBtn = el('button', { class: 'btn btn-primary', text: '复制并打开' });
      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      const m = openModal(el('div', null, [h, row1, row2, actions]));

      const grpInp = row1.children[1];
      const nameInp = row2.children[1];
      nameInp.focus(); nameInp.select();

      cancelBtn.addEventListener('click', m.close);
      okBtn.addEventListener('click', async () => {
        const newGrp = (grpInp.value || '').trim() || 'default';
        const newName = (nameInp.value || '').trim();
        if (!newName) { toast('名字不能为空', 'warn'); return; }
        try {
          const r = await api('POST', '/api/http/cases', {
            case: {
              id: '', group: newGrp, name: newName,
              method: c.method, url: c.url, headers: c.headers || {},
              body: c.body || '',
              body_mode: c.body_mode || 'raw',
              body_type: c.body_type || 'json',
              body_form: c.body_form || null,
              timeout_ms: c.timeout_ms || 0,
              follow_redirect: c.follow_redirect !== false,
              insecure_tls: !!c.insecure_tls,
            },
          });
          m.close();
          toast('已复制为「' + newGrp + ' / ' + newName + '」', 'ok');
          if (r.case && r.case.id) {
            await loadAll();
            const cloned = (r.case && r.case) || null;
            // 自动加载新用例
            const all = (await api('GET', '/api/http/cases')).cases || [];
            const found = all.find(x => x.id === r.case.id);
            if (found) loadCase(found);
          }
        } catch (e) { toast('复制失败：' + e.message, 'err'); }
      });
    }

    function openRenameCaseModal(c) {
      const h = el('h4', { text: '编辑用例信息' });
      const row1 = el('div', { class: 'modal-row' }, [
        el('label', { text: '分组' }),
        el('input', { type: 'text', value: c.group || 'default' }),
      ]);
      const row2 = el('div', { class: 'modal-row' }, [
        el('label', { text: '名字' }),
        el('input', { type: 'text', value: c.name || '' }),
      ]);
      const actions = el('div', { class: 'modal-actions' });
      const cancelBtn = el('button', { class: 'btn', text: '取消' });
      const okBtn = el('button', { class: 'btn btn-primary', text: '保存' });
      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      const m = openModal(el('div', null, [h, row1, row2, actions]));
      const grpInp = row1.children[1];
      const nameInp = row2.children[1];
      nameInp.focus(); nameInp.select();
      cancelBtn.addEventListener('click', m.close);
      okBtn.addEventListener('click', async () => {
        const newGrp = (grpInp.value || '').trim() || 'default';
        const newName = (nameInp.value || '').trim();
        if (!newName) { toast('名字不能为空', 'warn'); return; }
        try {
          await api('POST', '/api/http/cases', {
            case: { id: c.id, group: newGrp, name: newName,
              method: c.method, url: c.url, headers: c.headers || {},
              body: c.body || '',
              body_mode: c.body_mode || 'raw',
              body_type: c.body_type || 'json',
              body_form: c.body_form || null,
              timeout_ms: c.timeout_ms || 0,
              follow_redirect: c.follow_redirect !== false,
              insecure_tls: !!c.insecure_tls,
            },
          });
          m.close();
          toast('已更新', 'ok');
          await loadAll();
          if (lastLoadedCaseId === c.id) {
            lastLoadedCaseName = newName;
            lastLoadedCaseGroup = newGrp;
            saveGroupInp.value = newGrp;
            saveNameInp.value = newName;
            updateSaveMeta();
          }
        } catch (e) { toast('保存失败：' + e.message, 'err'); }
      });
    }

    // ---------- 导出 / 导入 ----------

    function exportCases() {
      const data = {
        version: 2,
        exported_at: new Date().toISOString(),
        cases: allCases,
        envs: allEnvs,
      };
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = el('a', { href: url, download: 'http-cases-' + Date.now() + '.json' });
      document.body.appendChild(a); a.click(); a.remove();
      URL.revokeObjectURL(url);
      toast('已导出 ' + allCases.length + ' 个用例 / ' + allEnvs.length + ' 组 env', 'ok');
    }

    function importCases() {
      const inp = el('input', { type: 'file', accept: '.json,application/json' });
      inp.style.display = 'none';
      document.body.appendChild(inp);
      inp.addEventListener('change', async () => {
        const f = inp.files && inp.files[0];
        if (!f) { inp.remove(); return; }
        try {
          const text = await f.text();
          const data = JSON.parse(text);
          const cases = Array.isArray(data.cases) ? data.cases : (Array.isArray(data) ? data : []);
          const envs = Array.isArray(data.envs) ? data.envs : [];
          if (cases.length === 0 && envs.length === 0) { toast('文件中没有可导入的用例', 'warn'); inp.remove(); return; }
          // 弹窗确认模式
          const h = el('h4', { text: '导入 ' + cases.length + ' 个用例 / ' + envs.length + ' 组 env' });
          const hint = el('div', { class: 'http2-section-desc',
            text: '合并：保留现有，同 group+name 的覆盖；覆盖：用导入文件替换全部。' });
          const actions = el('div', { class: 'modal-actions' });
          const cancelBtn = el('button', { class: 'btn', text: '取消' });
          const mergeBtn = el('button', { class: 'btn', text: '合并' });
          const replaceBtn = el('button', { class: 'btn btn-primary', text: '覆盖' });
          actions.appendChild(cancelBtn); actions.appendChild(mergeBtn); actions.appendChild(replaceBtn);
          const m = openModal(el('div', null, [h, hint, actions]));
          cancelBtn.addEventListener('click', m.close);
          const apply = async (mode) => {
            try {
              if (mode === 'replace') {
                // 全替换
                for (const c of cases) {
                  await api('POST', '/api/http/cases', { case: { ...c, id: '' } });
                }
                if (envs.length) await api('POST', '/api/http/envs', { envs });
              } else {
                // 合并：只 add 不覆盖（通过保留 ID 来让后端判断）
                for (const c of cases) {
                  const exists = allCases.find(x => x.group === c.group && x.name === c.name);
                  await api('POST', '/api/http/cases', { case: { ...c, id: exists ? exists.id : '' } });
                }
                // env 简单合并：去重
                if (envs.length) {
                  const merged = [...allEnvs];
                  envs.forEach(e => {
                    const i = merged.findIndex(m2 => m2.name === e.name);
                    if (i >= 0) merged[i] = e; else merged.push(e);
                  });
                  await api('POST', '/api/http/envs', { envs: merged });
                }
              }
              m.close();
              inp.remove();
              await loadAll();
              toast('导入完成', 'ok');
            } catch (err) { toast('导入失败：' + err.message, 'err'); }
          };
          mergeBtn.addEventListener('click', () => apply('merge'));
          replaceBtn.addEventListener('click', () => apply('replace'));
        } catch (e) {
          toast('JSON 解析失败：' + e.message, 'err');
          inp.remove();
        }
      });
      inp.click();
    }

    // ---------- Env 管理（底部） ----------

    const envMgrBox = el('div', { class: 'http2-env-mgr-card' });
    function rebuildEnvMgr() {
      envMgrBox.innerHTML = '';
      if (allEnvs.length === 0) {
        envMgrBox.appendChild(el('div', { class: 'http2-mute', text: '暂无 env 组。点下方"新增 env 组"创建。' }));
        return;
      }
      allEnvs.forEach((e, gi) => {
        const head = el('div', { class: 'http2-env-group-head' });
        head.appendChild(el('span', { text: e.name + '（' + Object.keys(e.vars || {}).length + ' 变量）' }));
        const delBtn = el('button', { class: 'btn btn-mini', text: '删除', onclick: async (ev) => {
          ev.stopPropagation();
          if (!confirm('删除 env 组「' + e.name + '」？')) return;
          allEnvs.splice(gi, 1);
          if (activeEnv === e.name) activeEnv = '';
          await saveEnvsToBackend();
          refreshEnvSel();
          rebuildEnvMgr();
        }});
        head.appendChild(delBtn);

        const varsBox = el('div', { class: 'http2-env-vars' });
        const editor = buildKvEditor(mapToKvArray(e.vars || {}), { keyPh: 'var', valPh: 'value' });
        varsBox.appendChild(editor.wrap);
        const addBtn = el('button', { class: 'http2-kv-add', text: '+ 添加变量' });
        addBtn.addEventListener('click', () => editor.addRow());
        varsBox.appendChild(addBtn);

        // 任意改动都同步到 allEnvs
        editor.wrap.addEventListener('input', () => {
          e.vars = kvArrayToMap(editor.getRows());
          head.firstChild.textContent = e.name + '（' + Object.keys(e.vars).length + ' 变量）';
          clearTimeout(editor._t);
          editor._t = setTimeout(saveEnvsToBackend, 500);
        });

        envMgrBox.appendChild(el('div', { class: 'http2-env-group' }, [head, varsBox]));
      });
    }
    const btnNewEnv = el('button', { class: 'btn btn-sm', text: '+ 新增 env 组', onclick: () => {
      const name = prompt('env 组名（如 dev / test / prod）：');
      if (!name) return;
      if (allEnvs.find(e => e.name === name)) { toast('已存在：' + name, 'warn'); return; }
      allEnvs.push({ name, vars: {} });
      saveEnvsToBackend();
      refreshEnvSel();
      rebuildEnvMgr();
    }});

    // ---------- 整体布局 ----------

    const headersCard = el('div', { class: 'http2-card' }, [
      el('div', { class: 'http2-card-head' }, [
        el('div', { class: 'http2-card-title', text: 'Headers' }),
        el('span', { class: 'http2-mute', style: 'font-size:11px', text: '支持粘贴 cURL -H 多行' }),
      ]),
      headersEditor.wrap,
      headersAddBtn,
    ]);

    const bodyCard = el('div', { class: 'http2-card' }, [
      el('div', { class: 'http2-card-head' }, [
        el('div', { class: 'http2-card-title', text: 'Body' }),
        bodyTabs,
      ]),
      bodyNone, bodyForm, bodyUrl, bodyRaw,
    ]);

    const respCard = el('div', { class: 'http2-card' }, [
      el('div', { class: 'http2-card-head' }, [
        el('div', { class: 'http2-card-title', text: 'Response' }),
      ]),
      respSummary,
      el('div', null, [
        el('div', { style: 'font-size: 12px; color: var(--text-dim); margin: 10px 0 4px', text: 'Headers' }),
        respHeadersPre,
      ]),
      el('div', null, [
        el('div', { style: 'font-size: 12px; color: var(--text-dim); margin: 10px 0 4px', text: 'Body' }),
        respViewWrap,
      ]),
    ]);

    const saveCard = el('div', { class: 'http2-card' }, [
      el('div', { class: 'http2-card-head' }, [
        el('div', { class: 'http2-card-title', text: '保存用例' }),
        el('span', { class: 'http2-mute', style: 'font-size:11px', text: '回填后修改会标 ●，点保存会覆盖原用例' }),
      ]),
      saveRow,
    ]);

    const envCard = el('details', { class: 'http2-card' }, [
      el('summary', null, [
        el('div', { class: 'http2-card-title', text: '管理环境变量组（点击展开/折叠）' }),
      ]),
      el('div', { style: 'margin-top: 8px' }, [envMgrBox, el('div', { style: 'margin-top: 8px' }, [btnNewEnv])]),
      el('div', { class: 'http2-mute', style: 'margin-top: 8px; font-size: 11.5px',
        text: '顶部下拉切换当前激活的 env 组；{{name}} 占位会按当前组的 vars 替换。' }),
    ]);

    const main = el('div', { class: 'http2-main' }, [
      el('div', { class: 'http2-card' }, [
        urlRow,
        optRow,
      ]),
      headersCard,
      bodyCard,
      respCard,
      saveCard,
      envCard,
    ]);

    view.appendChild(el('div', { class: 'http2-layout' }, [sidebar, main]));

    // 初始
    pristineSnapshot = takeSnapshot();
    updateSaveMeta();
    loadAll();

    // Ctrl/Cmd + Enter → Send
    document.addEventListener('keydown', (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
        if (view.contains(document.activeElement)) {
          e.preventDefault();
          doSend();
        }
      }
    });
  }

  Kairo.pages.http = renderHTTP;
  Kairo.state.routes.http = renderHTTP;
  Kairo.state.routeNames.http = 'HTTP 测试';
  Kairo.state.routeSubs.http = '键值对编辑器 · Body 多模式 · 响应高亮 · 用例管理';
})();
