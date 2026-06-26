/* ===== web/pages/websphere.js =====
 * WebSphere 日志助手 —— 多服务器并行版
 *
 * 4 块：
 *   1. 顶部表单（系统 / 服务器 / 凭据 / 测试 / 列表 / 下载最新 N）
 *   2. 多服务器并行搜索（含时间窗口）
 *   3. 实时 tail（单文件 SSE 流）
 *   4. 文件列表 / 搜索结果 / 上下文
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, $, toast, setStatus, cssEscape, pctText, formatBytes, formatTime, trimMiddle, looksMojibake, basenameOf } = OTB.core;
  const { api } = OTB.api;

  // ===== 搜索关键词历史（localStorage 持久化）=====
  // 行为：
  //   - 提交搜索时记录表达式；空 / 纯空白 / 仅 1 字符不记录；
  //   - 默认 placeholder 的 'Exception' 也不算历史（首次进入时不污染）；
  //   - 严格去重：相同字符串先删旧的再 push 头部；
  //   - 上限 20 条，超过截尾。
  // 设计动机：日志查询是高频操作，关键词经常重复（"Exception"、"NPE"、"订单超时" 等），
  // 鼠标点选比重新手敲快很多；localStorage 存避免改后端 schema。
  const SEARCH_HISTORY_KEY = 'otb:websphere:search-history';
  const SEARCH_HISTORY_MAX = 20;
  // 用户首次进入看到的默认值（写在 queryInp.value 里）—— 不要塞进历史。
  const SEARCH_DEFAULT_VALUE = 'Exception';

  function loadSearchHistory() {
    try {
      const raw = localStorage.getItem(SEARCH_HISTORY_KEY);
      if (!raw) return [];
      const arr = JSON.parse(raw);
      // 防御：非法数据静默回退到空
      if (!Array.isArray(arr)) return [];
      // 每项必须是 {q: string, ts: number}，缺字段的丢掉
      return arr.filter(x => x && typeof x.q === 'string' && typeof x.ts === 'number').slice(0, SEARCH_HISTORY_MAX);
    } catch (e) {
      // localStorage 可能因隐私模式 / 配额满抛错；不阻塞 UI
      return [];
    }
  }

  function saveSearchHistory(arr) {
    try {
      localStorage.setItem(SEARCH_HISTORY_KEY, JSON.stringify(arr.slice(0, SEARCH_HISTORY_MAX)));
    } catch (e) {
      // ignore：localStorage 写失败不影响搜索功能本身
    }
  }

  function pushSearchHistory(q) {
    const trimmed = (q || '').trim();
    // 跳过空白 / 太短 / 默认占位符
    if (!trimmed || trimmed.length < 2 || trimmed === SEARCH_DEFAULT_VALUE) return;
    const list = loadSearchHistory();
    // 严格去重（先删旧的同字符串，大小写敏感——搜索语法区分大小写）
    const filtered = list.filter(x => x.q !== trimmed);
    filtered.unshift({ q: trimmed, ts: Date.now() });
    saveSearchHistory(filtered);
  }

  // 把时间戳格式化成"X 分钟前 / YYYY-MM-DD HH:mm"
  // 注意：这里不复用 core.formatTime —— core.formatTime 走 toLocaleString() 输出
  // "2026/6/24 下午10:30:00" 这种形式，对 1 小时内的"刚刚 / N 分钟前"完全无能为力。
  // 自写是为了支持相对时间显示；>1 天的回退到固定格式而非 locale 字符串（更紧凑）。
  function formatHistoryTime(ts) {
    if (!ts) return '';
    const diff = Date.now() - Number(ts);
    if (diff < 60 * 1000) return '刚刚';
    if (diff < 60 * 60 * 1000) return Math.floor(diff / 60000) + ' 分钟前';
    if (diff < 24 * 60 * 60 * 1000) return Math.floor(diff / 3600000) + ' 小时前';
    const d = new Date(Number(ts));
    if (isNaN(d.getTime())) return '';
    const pad = n => (n < 10 ? '0' + n : '' + n);
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate())
      + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  function renderWebsphere(view) {
    let cfg = null;
    let listState = { files: [], serverName: '', dlId: null, dlEvtSrc: null, fileStates: {}, lastDownloadFolder: '', dlMode: null, dlApiBase: '', dlLatestUi: null, dlAbort: null };
    let searchSelectedFiles = [];
    const srvStatus = {};

    const sysSel = el('select', { id: 'ws-sys' });
    const dirSel = el('select', { id: 'ws-dir' });
    const userInp = el('input', { type: 'text', id: 'ws-user', placeholder: 'SSH 用户名（可留空，使用配置默认）' });
    const passInp = el('input', { type: 'password', id: 'ws-pass', placeholder: 'SSH 密码' });
    const queryInp = el('input', {
      type: 'text', id: 'ws-query',
      placeholder: '例: Exception && userinfo   或   !DEBUG', value: 'Exception',
      autocomplete: 'off', spellcheck: 'false',
      role: 'combobox',
      'aria-autocomplete': 'none',
      'aria-haspopup': 'listbox',
      'aria-expanded': 'false',
      'aria-controls': 'ws-query-history-popover',
      'aria-activedescendant': ''
    });

    // ===== 搜索关键词历史下拉面板 =====
    // 行为：
    //   - 聚焦 queryInp 时展开；blur 时延时收起（150ms 内点条目不会误关）；
    //   - ↑↓ 选中条目；Enter 直接搜；Esc 收起；
    //   - 点条目 = 填入 queryInp + 立即触发 doSearch；
    //   - 底部「清空历史」按钮，一键清空 localStorage + 重新渲染；
    //   - 历史为空时整个面板隐藏。
    // 用一个 inline-block 的 wrapper 把 queryInp 和 popover 包一起，
    // popover 用 position:absolute 定位到 wrapper 下方。
    const queryWrap = el('div', { class: 'ws-query-wrap', style: 'position:relative;' });
    queryWrap.appendChild(queryInp);
    const queryPopover = el('div', {
      id: 'ws-query-history-popover',
      class: 'ws-query-popover',
      role: 'listbox',
      style: 'display:none; position:absolute; top:100%; left:0; right:0; z-index:50;'
    });
    queryWrap.appendChild(queryPopover);

    // 渲染下拉面板内容（每次展开 / 历史变化时重画）
    let queryHistHighlightIdx = -1; // 当前键盘高亮的条目索引
    // suspendShow：选择条目 / 清空后需要临时屏蔽 focus 事件触发的 showQueryHistory。
    // 否则 hideQueryHistory() → queryInp.focus() 会触发 focus listener 再把面板显示出来，
    // 形成"瞬开瞬关"的闪烁。setTimeout 在 200ms 后释放锁（覆盖 blur 延时的 150ms）。
    let queryHistorySuspendUntil = 0;
    function isQueryHistorySuspended() {
      return Date.now() < queryHistorySuspendUntil;
    }
    function suspendQueryHistoryShow(ms) {
      queryHistorySuspendUntil = Date.now() + (ms || 200);
    }

    // 返回渲染时的 list（避免调用方再 loadSearchHistory 一次）
    function renderQueryHistory() {
      const list = loadSearchHistory();
      queryPopover.innerHTML = '';
      queryHistHighlightIdx = -1;
      // 重置 aria-activedescendant（无高亮）
      queryInp.setAttribute('aria-activedescendant', '');
      if (!list.length) {
        // 空历史：面板收起（不显示空状态，免得在 input 里第一次就弹个空框）
        queryPopover.style.display = 'none';
        queryInp.setAttribute('aria-expanded', 'false');
        return list;
      }
      list.forEach((item, idx) => {
        const row = el('div', {
          class: 'ws-query-popover-row',
          id: 'ws-query-history-option-' + idx,
          role: 'option',
          'aria-selected': 'false',
          'data-idx': String(idx),
          style: 'display:flex; justify-content:space-between; align-items:center; gap:10px; padding:6px 10px; cursor:pointer; border-bottom:1px solid var(--border, #eee);'
        }, [
          el('span', {
            class: 'ws-query-popover-q',
            style: 'font-family: var(--mono, monospace); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; flex:1;',
            text: item.q
          }),
          el('span', {
            class: 'ws-query-popover-ts text-dim',
            style: 'font-size:11.5px; white-space:nowrap;',
            text: formatHistoryTime(item.ts)
          })
        ]);
        row.addEventListener('mousedown', (ev) => {
          // 用 mousedown 而不是 click，防止 input 的 blur 先于 click 触发导致面板提前收起
          ev.preventDefault();
          selectHistoryItem(item.q);
        });
        row.addEventListener('mouseenter', () => {
          queryHistHighlightIdx = -1;
          updateHistoryHighlight();
        });
        queryPopover.appendChild(row);
      });
      // 「清空历史」分隔 + 按钮
      const sep = el('div', { style: 'height:1px; background:var(--border, #eee); margin-top:2px;' });
      queryPopover.appendChild(sep);
      const clearBtn = el('div', {
        class: 'ws-query-popover-clear',
        style: 'padding:6px 10px; cursor:pointer; color:var(--text-dim, #888); font-size:12px; text-align:center;',
        text: '🗑 清空搜索历史'
      });
      clearBtn.addEventListener('mousedown', (ev) => {
        ev.preventDefault();
        try { localStorage.removeItem(SEARCH_HISTORY_KEY); } catch (e) { /* ignore */ }
        hideQueryHistory();
        toast('搜索历史已清空', 'ok');
      });
      queryPopover.appendChild(clearBtn);
      return list;
    }

    function showQueryHistory() {
      // 屏蔽期（选择条目后 / 清空后）不允许重新展开
      if (isQueryHistorySuspended()) return;
      const list = renderQueryHistory();
      if (!list.length) return; // renderQueryHistory 已设 display:none
      queryPopover.style.display = '';
      queryInp.setAttribute('aria-expanded', 'true');
    }
    function hideQueryHistory() {
      queryPopover.style.display = 'none';
      queryInp.setAttribute('aria-expanded', 'false');
      queryHistHighlightIdx = -1;
      queryInp.setAttribute('aria-activedescendant', '');
      updateHistoryHighlight();
    }
    function isQueryHistoryVisible() {
      return queryPopover.style.display !== 'none';
    }
    function updateHistoryHighlight() {
      // -1 = 无高亮（鼠标 hover 模式）
      const rows = queryPopover.querySelectorAll('.ws-query-popover-row');
      rows.forEach((r, i) => {
        if (i === queryHistHighlightIdx) {
          r.style.background = 'var(--hover, rgba(0,0,0,0.06))';
          r.setAttribute('aria-selected', 'true');
        } else {
          r.style.background = '';
          r.setAttribute('aria-selected', 'false');
        }
      });
      // 同步 aria-activedescendant 让屏幕阅读器播报当前选中条目
      queryInp.setAttribute(
        'aria-activedescendant',
        queryHistHighlightIdx >= 0 ? 'ws-query-history-option-' + queryHistHighlightIdx : ''
      );
    }
    function selectHistoryItem(q) {
      queryInp.value = q;
      // 先屏蔽 showQueryHistory，再收起面板 + 重新聚焦：
      // 否则 hideQueryHistory() → queryInp.focus() 会触发 focus listener 把面板又显示出来。
      suspendQueryHistoryShow(250);
      hideQueryHistory();
      queryInp.focus();
      // 立即触发搜索（与点搜索按钮等价）
      doSearch();
    }
    function moveHistoryHighlight(delta) {
      const rows = queryPopover.querySelectorAll('.ws-query-popover-row');
      if (!rows.length) return;
      let next = queryHistHighlightIdx + delta;
      if (next < 0) next = rows.length - 1;
      if (next >= rows.length) next = 0;
      queryHistHighlightIdx = next;
      updateHistoryHighlight();
    }
    function confirmHistoryHighlight() {
      if (queryHistHighlightIdx < 0) return false;
      const list = loadSearchHistory();
      const item = list[queryHistHighlightIdx];
      if (item) selectHistoryItem(item.q);
      return true;
    }

    // 事件：聚焦展开 + 展开后再渲染一次（保证最新）
    let queryBlurTimer = null;
    queryInp.addEventListener('focus', showQueryHistory);
    queryInp.addEventListener('blur', () => {
      // 延时收起：让用户有时间点条目（mousedown 的 preventDefault 不会触发 blur 后立刻 hidden）
      // 每次 blur 先清掉上一次的 timer，避免快速 focus/blur 序列里多个 hideQueryHistory 排队触发
      if (queryBlurTimer) clearTimeout(queryBlurTimer);
      queryBlurTimer = setTimeout(hideQueryHistory, 150);
    });
    queryInp.addEventListener('keydown', (ev) => {
      // 仅当下拉可见时响应 ↑↓ Enter Esc
      if (!isQueryHistoryVisible()) {
        // Esc 在面板隐藏时也无副作用，保持一致
        return;
      }
      if (ev.key === 'ArrowDown') {
        ev.preventDefault();
        moveHistoryHighlight(1);
      } else if (ev.key === 'ArrowUp') {
        ev.preventDefault();
        moveHistoryHighlight(-1);
      } else if (ev.key === 'Enter') {
        if (confirmHistoryHighlight()) {
          ev.preventDefault(); // 阻止表单默认提交，避免和 doSearch 重复
        }
      } else if (ev.key === 'Escape') {
        ev.preventDefault();
        hideQueryHistory();
      }
    });
    const filesNSel = el('select', { id: 'ws-files-n' });
    [1, 3, 5, 10].forEach(n => {
      const o = el('option', { value: String(n), text: '最近 ' + n + '个文件' });
      if (n === 3) o.selected = true;
      filesNSel.appendChild(o);
    });
    const concSel = el('select', { id: 'ws-conc' });
    [1, 2, 4, 8, 16].forEach(n => {
      const o = el('option', { value: String(n), text: '并发 ' + n });
      if (n === 8) o.selected = true;
      concSel.appendChild(o);
    });

    // 时间窗口（项 C1：默认 "全部"，可选 10min / 1h / today / 自定义）
    const timeSel = el('select', { id: 'ws-time' });
    [
      ['', '全部时间'],
      ['10m', '最近 10 分钟'],
      ['1h', '最近 1 小时'],
      ['today', '今天'],
      ['custom', '自定义…']
    ].forEach(([v, t]) => timeSel.appendChild(el('option', { value: v, text: t })));
    const timeFromInp = el('input', { type: 'datetime-local', id: 'ws-time-from', style: 'display:none' });
    const timeToInp = el('input', { type: 'datetime-local', id: 'ws-time-to', style: 'display:none' });
    const timeHint = el('div', { class: 'text-dim', style: 'font-size:11.5px; margin-top:2px;', text: '按文件修改时间粗筛，不按日志行时间筛选' });

    function updateTimeCustomVisibility() {
      const isCustom = timeSel.value === 'custom';
      timeFromInp.style.display = isCustom ? '' : 'none';
      timeToInp.style.display = isCustom ? '' : 'none';
    }
    timeSel.addEventListener('change', updateTimeCustomVisibility);
    updateTimeCustomVisibility();

    // 构造时间范围参数（透传给 /api/logs/search/multi）
    function buildTimeRange() {
      if (!timeSel.value) return {};
      if (timeSel.value === 'custom') {
        const from = timeFromInp.value ? new Date(timeFromInp.value).toISOString() : '';
        const to = timeToInp.value ? new Date(timeToInp.value).toISOString() : '';
        return Object.assign({}, from && { since: from }, to && { until: to });
      }
      const now = Date.now();
      let sinceMs = null;
      if (timeSel.value === '10m') sinceMs = now - 10 * 60 * 1000;
      else if (timeSel.value === '1h') sinceMs = now - 60 * 60 * 1000;
      else if (timeSel.value === 'today') {
        const d = new Date();
        d.setHours(0, 0, 0, 0);
        sinceMs = d.getTime();
      }
      return sinceMs ? { since: new Date(sinceMs).toISOString() } : {};
    }

    const dlNSel = el('select', { id: 'ws-dl-n' });
    // P1-7：文件名过滤（多组文件列表实时过滤）
    let wsFileFilter = '';
    [1, 2, 3, 4, 5].forEach(n => {
      const o = el('option', { value: String(n), text: '最近 ' + n + ' 个文件' });
      if (n === 3) o.selected = true;
      dlNSel.appendChild(o);
    });
    const dlZipChk = el('input', { type: 'checkbox', id: 'ws-dl-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('打包为 zip')]);

    // --- 服务器多选面板 ---
    const srvPickWrap = el('div', { class: 'srv-pick' });
    const srvPickHint = el('div', { class: 'text-dim', text: '先选择系统' });
    srvPickWrap.appendChild(srvPickHint);
    const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllSrv(true) });
    const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => toggleAllSrv(false) });
    const btnPickOnline = el('button', { class: 'btn btn-sm', text: '只保留已测通', onclick: () => toggleOnline() });
    const srvPickToolbar = el('div', { class: 'srv-pick-toolbar' }, [
      document.createTextNode('目标服务器:'), btnPickAll, btnPickNone, btnPickOnline
    ]);

    // v0.5：每个勾选服务器下展开它的日志目录（二级勾选），用于 #10 多对多。
    const srvDirsWrap = el('div', { class: 'srv-dirs' });
    const srvDirsHint = el('div', { class: 'text-dim', text: '勾选服务器后会展开它的日志目录，可多选。' });
    srvDirsWrap.appendChild(srvDirsHint);
    const srvDirsToolbar = el('div', { class: 'srv-pick-toolbar' }, [
      document.createTextNode('目标日志目录（多对多勾选）:'),
      el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllDirs(true) }),
      el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => toggleAllDirs(false) })
    ]);
    const dlTargetSel = el('select', { style: 'display:none' });

    function getCheckedServers() {
      const out = [];
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        if (cb.checked) out.push(cb.getAttribute('data-srv'));
      });
      return out;
    }
    function toggleAllSrv(on) {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => { cb.checked = on; });
      renderSrvDirs();
      persistSelection();
      refreshCredStatus();
    }
    function toggleOnline() {
      const hasOk = Object.keys(srvStatus).some(k => srvStatus[k] && srvStatus[k].state === 'ok');
      if (!hasOk) {
        toast('请先测试连接，再只保留已测通服务器', 'warn');
        return;
      }
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        cb.checked = srvStatus[cb.getAttribute('data-srv')] && srvStatus[cb.getAttribute('data-srv')].state === 'ok';
      });
      renderSrvDirs();
      persistSelection();
      refreshCredStatus();
    }
    // 当前勾选的 (server, dir) targets 列表；页面已经渲染二级目录时，只认用户显式勾选。
    // 目录全不选就返回空，避免隐藏的默认目录替用户执行生产操作。
    function getSelectedTargets() {
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      if (!sys) return [];
      const out = [];
      const explicitDirs = srvDirsWrap.querySelectorAll('input[type="checkbox"][data-srv][data-dir]:checked');
      explicitDirs.forEach(cb => {
        out.push({ server: cb.getAttribute('data-srv'), dir: cb.getAttribute('data-dir') });
      });
      return out;
    }
    function describeTargets(targets) {
      const srvs = [...new Set((targets || []).map(t => t.server))];
      return srvs.length + ' 台服务器 / ' + (targets || []).length + ' 个目录';
    }
    async function confirmManyTargets(targets, actionText) {
      if (!targets || targets.length <= 5) return true;
      if (!OTB.core.confirmDialog) return window.confirm(actionText + '将作用于 ' + describeTargets(targets) + '，是否继续？');
      return OTB.core.confirmDialog(
        actionText + '将作用于 ' + describeTargets(targets) + '。请确认这是你想操作的范围。',
        { title: '确认批量操作', okText: '继续执行', cancelText: '取消' }
      );
    }
    function toggleAllDirs(on) {
      srvDirsWrap.querySelectorAll('input[type="checkbox"][data-srv][data-dir]').forEach(cb => { cb.checked = on; });
      persistSelection();
    }
    // 把"系统 + 勾选服务器 + 目录（含二级）"存 localStorage，下次打开自动恢复
    function persistSelection() {
      if (!sysSel.value) return;
      const checked = getCheckedServers();
      const explicitDirs = srvDirsWrap.querySelectorAll('input[type="checkbox"][data-srv][data-dir]:checked');
      const dirs = [];
      explicitDirs.forEach(cb => dirs.push({ srv: cb.getAttribute('data-srv'), dir: cb.getAttribute('data-dir') }));
      OTB.core.lastSet('websphere', 'sel', {
        system: sysSel.value,
        servers: checked,
        dir: dirSel.value,
        dirs: dirs,
        username: userInp.value
      });
      // P1-8：勾选变化时实时刷新目标摘要（按 server/dir 组合显示）
      try { updateTargetSummary(); } catch (e) { /* ignore */ }
      // v0.6-Redesign：tail tab 也要跟着 target 变化刷新（多 target 时显示警告）
      try { refreshTailTargetSel(); } catch (e) { /* ignore */ }
      // v0.7-Redesign（第二波）：目标区可折叠摘要行 —— 勾选变化要同步刷新单行摘要
      try { renderTargetSummary(); } catch (e) { /* ignore */ }
    }
    function renderSrvPick() {
      const prevChecked = getCheckedServers();
      // 第一次加载：尝试用上次记忆的服务器列表
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      const lastForSys = (lastSel && lastSel.system === sysSel.value && Array.isArray(lastSel.servers)) ? lastSel.servers : null;
      srvPickWrap.innerHTML = '';
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      if (!sys || !sys.servers || !sys.servers.length) {
        srvPickWrap.appendChild(el('div', { class: 'text-dim', text: '该业务系统下没有服务器。' }));
        return;
      }
      sys.servers.forEach((s, idx) => {
        const st = srvStatus[s.name] || { state: 'idle' };
        const dotCls = 'dot dot-' + (st.state === 'idle' ? 'idle' : st.state);
        const cb = el('input', { type: 'checkbox', 'data-srv': s.name, value: s.name });
        // 默认只选上次使用；没有历史时只选第一台，避免首次进入就扫全量生产节点。
        //   - 有上次选择 → 恢复
        //   - 无上次 → 默认勾第一台（用户可主动全选）
        const wasChecked = prevChecked.indexOf(s.name) !== -1
          || (lastForSys && lastForSys.indexOf(s.name) !== -1)
          || (prevChecked.length === 0 && !lastForSys && idx === 0);
        if (wasChecked) cb.checked = true;
        cb.addEventListener('change', () => { renderSrvDirs(); persistSelection(); refreshCredStatus(); });
        const item = el('label', { class: 'srv-pick-item' }, [
          cb,
          el('span', { class: 'name', text: s.name }),
          el('span', { class: 'host', text: s.host + ':' + s.port }),
          el('span', { class: 'status' }, [
            el('span', { class: dotCls }),
            document.createTextNode(' ' + (st.state === 'ok' ? '已测通' : st.state === 'fail' ? '连接失败' : st.state === 'busy' ? '测试中' : '未测'))
          ])
        ]);
        srvPickWrap.appendChild(item);
      });
      const hasOk = Object.keys(srvStatus).some(k => srvStatus[k] && srvStatus[k].state === 'ok');
      btnPickOnline.disabled = !hasOk;
      btnPickOnline.title = hasOk ? '取消未测通或连接失败的服务器' : '请先测试连接';
      // P0-BugFix #2：初次渲染 / 切系统后必须把已勾选服务器的目录区展开，
      // 否则 srvDirsWrap 永远停在 "勾选服务器后会展开它的日志目录，可多选"
      // 提示语，导致「列出文件 / 搜索」拿不到 (server, dir) targets。
      renderSrvDirs();
    }
    // renderSrvDirs v0.5：每个勾选服务器展开一个目录勾选区
    function renderSrvDirs() {
      srvDirsWrap.innerHTML = '';
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      const hasLastForSys = !!(lastSel && lastSel.system === sysName);
      const lastDirs = (hasLastForSys && Array.isArray(lastSel.dirs)) ? lastSel.dirs : [];
      const checkedSrvs = getCheckedServers();
      if (!checkedSrvs.length) {
        srvDirsWrap.appendChild(el('div', { class: 'text-dim', text: '先在「目标服务器」里勾选至少一台。' }));
        return;
      }
      if (!sys || !sys.servers) return;
      checkedSrvs.forEach(srvName => {
        const srv = sys.servers.find(s => s.name === srvName);
        if (!srv) return;
        // P2-19：srv-dirs-block 加 tier 色彩，跟 sys-block/srv-block/dir-block 保持一致
        const block = el('div', { class: 'srv-dirs-block dir-block' });
        const head = el('div', { class: 'srv-dirs-head' }, [
          el('span', { class: 'name', text: srv.name }),
          el('span', { class: 'text-dim', text: ' · ' + (srv.log_dirs || []).length + ' 个目录' })
        ]);
        block.appendChild(head);
        const list = el('div', { class: 'srv-dirs-list' });
        (srv.log_dirs || []).forEach((d, di) => {
          const cb = el('input', { type: 'checkbox', 'data-srv': srv.name, 'data-dir': d.path, value: d.path });
          // 默认只选上次使用；没有历史时只选每台已选服务器的第一个目录。
          //   - 有上次选择 → 恢复
          //   - 无上次 → 父级 server 勾选了 → 默认勾第一个目录
          //   - 无上次 + dirSel 命中 → 兜底勾上
          const dirHit = lastDirs.find(x => x.srv === srv.name && x.dir === d.path);
          const fallbackHit = (!hasLastForSys && dirSel.value === d.path && di === 0);
          const parentDefault = (!hasLastForSys && checkedSrvs.indexOf(srv.name) !== -1 && di === 0);
          if (dirHit || fallbackHit || parentDefault) cb.checked = true;
          cb.addEventListener('change', persistSelection);
          const enc = (d.encoding || 'utf-8').toLowerCase();
          const item = el('label', { class: 'srv-dirs-item' }, [
            cb,
            el('span', { class: 'name', text: d.name || d.path }),
            el('span', { class: 'path', text: d.path }),
            el('span', { class: 'enc', text: enc })
          ]);
          list.appendChild(item);
        });
        if (!(srv.log_dirs || []).length) {
          list.appendChild(el('div', { class: 'text-dim', text: '（该服务器未配置日志目录）' }));
        }
        block.appendChild(list);
        srvDirsWrap.appendChild(block);
      });
    }
    function refreshDirs() {
      dirSel.innerHTML = '';
      dlTargetSel.innerHTML = '';
      const sysName = sysSel.value;
      const srvs = getCheckedServers();
      const srvName = srvs[0] || (cfg && cfg.systems.find(s => s.name === sysName) && cfg.systems.find(s => s.name === sysName).servers[0] && cfg.systems.find(s => s.name === sysName).servers[0].name);
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      const srv = sys && sys.servers.find(s => s.name === srvName);
      (srv ? srv.log_dirs : []).forEach(d => {
        dirSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
        dlTargetSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
      });
      // 项 7 修复：默认选第一个目录（让 getSelectedTargets 在二级目录没勾时
      // 也能 fallback 到 dirSel 返回有效 targets）。lastSel.dir 已被 loadCfg 读进来，
      // 如果在配置里设过就走 lastSel.dir，否则走第一个。
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      if (lastSel && lastSel.dir && dirSel.querySelector('option[value="' + cssEscape(lastSel.dir) + '"]')) {
        dirSel.value = lastSel.dir;
      } else if (dirSel.options.length > 0 && !dirSel.value) {
        dirSel.value = dirSel.options[0].value;
      }
    }
    sysSel.addEventListener('change', () => { renderSrvPick(); refreshDirs(); refreshCredStatus(); persistSelection(); });
    // P1-8：renderSrvDirs / renderSrvPick 内部本来就会 persistSelection，所以这里
    // 不需要再额外调 updateTargetSummary。但因为 renderSrvPick 后会重建 srvPickWrap，
    // 摘要需要根据"新勾选列表"重新算；persistSelection 里调一次就够。
    dirSel.addEventListener('change', persistSelection);
    userInp.addEventListener('input', () => { clearTimeout(userInp._t); userInp._t = setTimeout(persistSelection, 500); });
    userInp.addEventListener('change', refreshCredStatus);
    userInp.addEventListener('blur', refreshCredStatus);

    const btnTest = el('button', { class: 'btn', text: '测试连接', onclick: doTest });
    const btnList = el('button', { class: 'btn btn-primary', text: '列出文件', onclick: doList });
    const btnDownload = el('button', { class: 'btn', text: '下载', onclick: doDownload });
    const btnSearch = el('button', { class: 'btn btn-primary', text: '搜索', onclick: doSearch });
    // P1-12：下载最新日志也支持 target_dir（自定义本地落点）。
    // 必须提前声明到 btnDownload 同一作用域，doDownload() 会读它的 value。
    const dlTargetDirInp = el('input', {
      type: 'text',
      id: 'ws-dl-target-dir',
      placeholder: '本地下载目录（留空走默认）',
      style: 'min-width:180px;',
      title: '留空 → 走 cfg.download_dir。写绝对路径 → 落到指定目录。'
    });

    // ===== v0.7-Redesign（第二波）：目标区可折叠摘要 =====
    // 默认折叠：顶部一行单行摘要 + 「✏️ 修改目标」按钮。
    // 展开后看到完整表单（业务系统/服务器/目录/凭据/测试连接）。
    // 第三波会把 [列出文件] [下载最新 N] 等"文件 tab 专属"按钮从 formCard 挪走。
    // 折叠状态记忆到 localStorage，跨刷新保持。
    const targetCollapsedKey = 'websphere_target_collapsed';
    // 项 14 修复：默认展开（v0.8 调整）。
    // 原版默认折叠 → 用户进入页面只看到一行摘要 + "✏️ 修改目标"按钮，看不到表单。
    // 第一次用的人会以为这是空状态、不知道要点按钮。改为默认展开：
    //   - 旧用户 lastSet 'collapsed' → 仍然折叠（保留个人偏好）
    //   - 旧用户 lastSet 'expanded' → 展开
    //   - 新用户 / 未设置 → 展开
    let targetCollapsed = false;
    try {
      const saved = OTB.core.lastGet('websphere', 'target_collapsed');
      if (saved === 'collapsed') targetCollapsed = true;
    } catch (e) { /* keep default (展开) */ }
    const targetSummaryBadge = el('span', { class: 'ws-target-summary-badge' });
    const targetToggleBtn = el('button', {
      class: 'btn btn-sm ws-target-toggle',
      id: 'ws-target-toggle',
      text: '✏️ 修改目标',
      title: '展开/收起目标选择区',
      'aria-expanded': 'false',
      'aria-controls': 'ws-target-body',
      onclick: toggleTargetPanel
    });
    const targetSummaryRow = el('div', {
      class: 'ws-target-summary-row',
      id: 'ws-target-summary-row'
    }, [
      targetSummaryBadge,
      targetToggleBtn
    ]);
    // 主体（默认折叠时整段隐藏）
    const targetBody = el('div', {
      class: 'ws-target-body',
      id: 'ws-target-body'
    });
    function toggleTargetPanel() {
      targetCollapsed = !targetCollapsed;
      try { OTB.core.lastSet('websphere', 'target_collapsed', targetCollapsed ? 'collapsed' : 'expanded'); } catch (e) { /* ignore */ }
      applyTargetPanelState();
    }
    function applyTargetPanelState() {
      if (targetCollapsed) {
        targetBody.style.display = 'none';
        targetToggleBtn.textContent = '✏️ 修改目标';
        targetToggleBtn.setAttribute('aria-expanded', 'false');
        targetSummaryRow.style.display = '';
      } else {
        targetBody.style.display = '';
        targetToggleBtn.textContent = '▲ 收起';
        targetToggleBtn.setAttribute('aria-expanded', 'true');
        targetSummaryRow.style.display = 'none';
      }
    }
    // 单行摘要渲染：🎯 业务系统 · N 台服务器 · M 个目录 (凭据已保存)
    function renderTargetSummary() {
      const sysName = sysSel.value || '未选业务系统';
      const targets = getSelectedTargets();
      const srvs = [...new Set(targets.map(t => t.server))];
      const srvTxt = srvs.length === 0 ? '未选服务器' : (srvs.length <= 2 ? srvs.join(', ') : (srvs.length + ' 台服务器'));
      const dirTxt = targets.length === 0 ? '未选目录' : (targets.length + ' 个目录');
      // 凭据状态（不阻塞渲染，没保存就显示"未保存"）
      const credTxt = passInp.value ? '· 凭据已输入' : (credStatus.textContent && credStatus.textContent.indexOf('已为') !== -1 ? '· 已保存凭据' : '');
      targetSummaryBadge.innerHTML = '';
      targetSummaryBadge.appendChild(document.createTextNode('🎯 ' + sysName + '  ·  ' + srvTxt + '  ·  ' + dirTxt + (credTxt ? '  ' + credTxt : '')));
    }
    // 让外层钩子能拿到这两个函数（保持 v0.6 的约定）
    if (typeof window !== 'undefined') {
      window.toggleTargetPanel = toggleTargetPanel;
      window.renderTargetSummary = renderTargetSummary;
    }

    // v0.6-Redesign：fileTableWrap 去掉内联 display:none —— 改由外层 ws-tab-content 控制显隐。
    // 进入页面默认 tab=files，要让用户能看到 files tab 的初始空状态（"先点「列出文件」"）。
    const fileTableWrap = el('div', { class: 'card' });
    // v0.7-Redesign（第三波）：files tab 工具栏移到 fileTableWrap 之外了，
    // fileTableWrap 现在初始就是空 card。给一个"请先点列出文件"占位，避免空白卡。
    fileTableWrap.appendChild(el('div', { class: 'ws-files-placeholder text-dim', text: '暂无文件，先点上方「📋 列出文件」拿到列表。' }));
    const hitTableWrap = el('div', { class: 'card', style: 'display:none' });
    const ctxCard = el('div', { class: 'card', style: 'display:none' });

    function credsOne(srvName) {
      return { system: sysSel.value, server: srvName, dir: dirSel.value,
               username: userInp.value, password: passInp.value };
    }
    function credsMulti() {
      return { system: sysSel.value, dir: dirSel.value,
               servers: getCheckedServers(),
               username: userInp.value, password: passInp.value };
    }

    // ---- 凭据保存（OS 钥匙串）----
    const rememberChk = el('input', { type: 'checkbox', id: 'ws-remember' });
    const rememberLbl = el('label', { class: 'inline' }, [rememberChk, document.createTextNode('记住密码（存进系统钥匙串）')]);
    const credStatus = el('div', { class: 'text-dim mt-1', id: 'ws-cred-status', text: '未保存密码' });
    const btnForget = el('button', { class: 'btn btn-sm', text: '忘记', onclick: doForget, style: 'display:none' });
    const credStatusRow = el('div', { class: 'text-dim mt-1', style: 'display:flex; gap:8px; align-items:center;' }, [
      credStatus, btnForget
    ]);

    function currentCredKey() {
      const srvs = getCheckedServers();
      const srv = srvs[0] || (sysSel.value && cfg ? (cfg.systems.find(s => s.name === sysSel.value) || {}).servers?.[0]?.name : '');
      return { system: sysSel.value, server: srv, username: userInp.value };
    }

    async function refreshCredStatus() {
      const k = currentCredKey();
      if (!k.system || !k.server || !k.username) {
        credStatus.textContent = '未保存密码';
        btnForget.style.display = 'none';
        return;
      }
      try {
        const r = await api('GET', '/api/credentials/has?system=' + encodeURIComponent(k.system)
          + '&server=' + encodeURIComponent(k.server)
          + '&username=' + encodeURIComponent(k.username));
        if (!r.ok) return;
        const mode = r.mode || 'keyring';
        const storeDisabled = (mode === 'disabled') || (mode === 'file');
        if (storeDisabled) {
          rememberChk.checked = false;
          rememberChk.disabled = true;
          rememberLbl.style.display = 'none';
          credStatus.textContent = r.reason || ('凭据存储=' + mode);
          credStatus.style.color = '#999';
          btnForget.style.display = 'none';
          return;
        }
        rememberChk.disabled = false;
        rememberLbl.style.display = '';
        if (!r.available) {
          credStatus.textContent = '⚠ 系统钥匙串不可用 — 当前无法「记住密码」';
          credStatus.style.color = '#c00';
          btnForget.style.display = 'none';
        } else if (r.has) {
          credStatus.textContent = '✓ 已为 ' + k.username + '@' + k.server + ' 保存密码（无需再次输入）';
          credStatus.style.color = '';
          btnForget.style.display = '';
        } else {
          credStatus.textContent = '未保存密码';
          credStatus.style.color = '';
          btnForget.style.display = 'none';
        }
      } catch (e) { /* ignore */ }
    }

    async function doForget() {
      const k = currentCredKey();
      if (!k.system || !k.server || !k.username) return;
      try {
        await api('POST', '/api/credentials/clear', { system: k.system, server: k.server, username: k.username });
        toast('已忘记 ' + k.server + ' 上的密码', 'ok');
        await refreshCredStatus();
      } catch (e) {
        toast('清除失败: ' + e.message, 'err');
      }
    }

    async function maybeSaveCred(srvName) {
      if (!rememberChk.checked) return;
      const pw = passInp.value;
      if (!pw) return;
      const k = currentCredKey();
      const target = srvName || k.server;
      if (!k.system || !target || !k.username) return;
      try {
        await api('POST', '/api/credentials/save', {
          system: k.system, server: target, username: k.username, password: pw
        });
        await refreshCredStatus();
      } catch (e) {
        console.warn('save credential failed:', e);
      }
    }

    async function doTest() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要测试的服务器', 'warn'); return; }
      srvs.forEach(n => { srvStatus[n] = { state: 'busy' }; });
      renderSrvPick();
      await Promise.all(srvs.map(async (n) => {
        try {
          await api('POST', '/api/ssh/test', credsOne(n));
          srvStatus[n] = { state: 'ok' };
          await maybeSaveCred(n);
        } catch (e) {
          srvStatus[n] = { state: 'fail', err: e.message };
        } finally {
          renderSrvPick();
        }
      }));
      const okN = srvs.filter(n => srvStatus[n].state === 'ok').length;
      toast(okN + '/' + srvs.length + ' 台连接成功', okN === srvs.length ? 'ok' : 'warn');
      refreshCredStatus();
    }

    async function doList() {
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      if (!(await confirmManyTargets(targets, '列出文件'))) return;
      // P1-6 修复：改用 /api/logs/list/targets 一次请求，由后端并发拉所有 targets，
      // 避免前端 N×/api/logs/list 的 N+1 延迟问题（10+ 台时明显）。
      listState.groups = [];
      listState.files = [];
      listState.serverName = targets.length === 1 ? (targets[0].server + ' · ' + (targets[0].dir.split('/').pop() || targets[0].dir)) : (targets.length + ' 组');
      renderFileTable();
      fileTableWrap.style.display = '';
      const t0 = Date.now();
      try {
        const r = await api('POST', '/api/logs/list/targets', {
          system: sysSel.value,
          targets: targets,
          username: userInp.value,
          password: passInp.value
        });
        const results = Array.isArray(r) ? r : (r.servers || r.results || []);
        results.forEach(srv => {
          listState.groups.push({
            server: srv.server,
            dir: srv.dir,
            files: (srv.files || []).map(f => ({
              name: f.name,
              full_path: f.full_path,
              size: f.size,
              mod_time: f.mod_time
            })),
            error: srv.ok ? null : (srv.error || '未知错误')
          });
        });
        const dt = Date.now() - t0;
        const okN = listState.groups.filter(g => !g.error).length;
        const totalFiles = listState.groups.reduce((a, g) => a + g.files.length, 0);
        toast((okN === listState.groups.length ? '列出完成：' : '部分失败：') + totalFiles + ' 个文件 / ' + okN + '/' + listState.groups.length + ' 组 · ' + dt + 'ms', okN === listState.groups.length ? 'ok' : 'warn');
        // v0.6：files tab badge 显示文件数
        setTabBadge('files', totalFiles > 0 ? (totalFiles + ' 文件') : '');
        const firstOk = listState.groups.find(g => !g.error);
        if (firstOk) await maybeSaveCred(firstOk.server);
      } catch (e) {
        toast('列出文件失败：' + e.message, 'err');
      }
      renderFileTable();
    }

    function renderFileTable() {
      fileTableWrap.innerHTML = '';
      const groups = listState.groups || [];
      const q = wsFileFilter.trim().toLowerCase();

      // P1-7：计算过滤后每组的文件列表
      // 占位符承诺"子串 / 通配符 * ?"。原代码用 ^...$ 全等匹配，导致
      // 普通子串（"System"）匹配不到文件名（SystemOut.log 等）。改成
      // 子串匹配 + 通配符 `*`/`?` 兜底；纯字串场景保留 substring 语义。
      function filterFiles(files) {
        if (!q) return files;
        // 含通配符 → 按通配语义匹配（* → 任意字符段，? → 单字符）
        if (q.indexOf('*') !== -1 || q.indexOf('?') !== -1) {
          // 顺序：先把 * ? 替换成正则元字符，再 escape 其余元字符。
          // （如果先 escape，* 会被转成 \*，后续替换 \\\\*/g 找不到。
          //  另外字符类里也不能放 *，否则会被误判为字面。）
          const wildcardsReplaced = q.replace(/\*/g, '\u0001').replace(/\?/g, '\u0002');
          const escaped = wildcardsReplaced.replace(/[.+^${}()|[\]\\]/g, '\\$&')
            .replace(/\u0001/g, '.*').replace(/\u0002/g, '.');
          try {
            const rx = new RegExp(escaped, 'i');
            return files.filter(f => rx.test((f.name || '').toLowerCase()));
          } catch (e) { return files; }
        }
        // 纯字串 → 大小写不敏感子串匹配
        return files.filter(f => (f.name || '').toLowerCase().indexOf(q) !== -1);
      }

      const filteredTotal = groups.reduce((a, g) => a + filterFiles(g.files || []).length, 0);
      const allTotal = groups.reduce((a, g) => a + (g.files || []).length, 0);
      const fileCountText = q ? ('（过滤后 ' + filteredTotal + ' / ' + allTotal + ' 个文件 / ' + groups.length + ' 组）') : ('（' + allTotal + ' 个文件 / ' + groups.length + ' 组）');
      fileTableWrap.appendChild(el('h3', { text: '文件列表 · ' + (listState.serverName || '') + fileCountText }));
      if (!groups.length && !listState.files.length) {
        fileTableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无文件，先点击「列出文件」。' }));
        return;
      }

      // P1-7：文件名过滤栏
      const filterInp = el('input', {
        type: 'text',
        placeholder: '过滤文件名（子串 / 通配符 * ?）',
        style: 'min-width: 200px;'
      });
      filterInp.value = wsFileFilter;
      filterInp.addEventListener('input', () => {
        wsFileFilter = filterInp.value;
        renderFileTable();
      });
      const filterClrBtn = el('button', { class: 'btn btn-sm', text: '清空', onclick: () => {
        wsFileFilter = '';
        filterInp.value = '';
        renderFileTable();
      }});
      const filterRow = el('div', { class: 'mt-1', style: 'display:flex; gap:8px; align-items:center;' }, [
        el('span', { class: 'lbl', text: '过滤：' }),
        filterInp, filterClrBtn
      ]);
      fileTableWrap.appendChild(filterRow);

      const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => pickAll(true) });
      const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => pickAll(false) });
      const btnPickTop = el('button', { class: 'btn btn-sm', text: '选前 ' + (dlNSel.value) + ' 个', onclick: () => pickTop(Number(dlNSel.value) || 1) });
      const btnDownloadSel = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownloadSelected });
      const btnCancel = el('button', { class: 'btn', text: '停止', onclick: doCancelDownload, disabled: true });
      const summary = el('div', { class: 'text-dim', text: '已选 0 个' });
      const toolbar = el('div', { class: 'file-toolbar' }, [
        btnPickAll, btnPickNone, btnPickTop, btnDownloadSel, btnCancel, summary
      ]);
      fileTableWrap.appendChild(toolbar);

      // 多组展示：每组一个 server-group（沿用搜索结果那套样式），组内是表格
      groups.forEach(g => {
        const grp = el('div', { class: 'server-group ' + (g.error ? 'fail' : 'ok') });
        const filteredFiles = filterFiles(g.files || []);
        grp.appendChild(el('div', { class: 'server-group-head' }, [
          el('span', { class: 'dot dot-' + (g.error ? 'err' : 'ok') }),
          el('span', { class: 'name', text: g.server }),
          el('span', { class: 'text-dim', text: ' · ' + g.dir }),
          el('span', { class: 'meta', text: g.error ? ('失败：' + g.error) : ((q ? ('过滤 ' + filteredFiles.length + ' / ' + g.files.length) : g.files.length) + ' 个文件') })
        ]));
        if (g.error) {
          grp.appendChild(el('div', { class: 'err-msg', text: g.error }));
          fileTableWrap.appendChild(grp);
          return;
        }
        if (q && filteredFiles.length === 0) {
          grp.appendChild(el('div', { class: 'text-dim', style: 'padding:6px 12px;', text: '过滤 "' + q + '" 无匹配' }));
          fileTableWrap.appendChild(grp);
          return;
        }
        const tbl = el('table', { class: 'table' });
        tbl.appendChild(el('thead', null, el('tr', null, [
          el('th', { class: 'col-check' }),
          el('th', { text: '文件名' }),
          el('th', { text: '大小' }),
          el('th', { text: '修改时间' }),
          el('th', { text: '操作' }),
          el('th', { class: 'col-status', text: '状态' })
        ])));
        const tbody = el('tbody');
        filteredFiles.forEach(f => {
          const key = (g.server || '') + '|' + (g.dir || '') + '|' + f.name;
          const row = el('tr', { 'data-key': key, 'data-file': f.name, 'data-srv': g.server, 'data-dir': g.dir });
          const cb = el('input', {
            type: 'checkbox',
            'data-file': f.name,
            'data-srv': g.server,
            'data-dir': g.dir,
            'data-full-path': f.full_path,
            onchange: refreshSummary
          });
          const statusCell = el('td', { class: 'col-status', 'data-status-key': key });
          // v0.5 #14：每行加 Tail / 新窗口 Tail 按钮（不用手输文件名）
          const tailBtn = el('button', {
            class: 'btn btn-sm',
            text: '📺 内嵌 Tail',
            title: '在下方 tail 区域跟踪此文件',
            onclick: () => startTailForFile(g.server, g.dir, f.name)
          });
          const tailNewWinBtn = el('button', {
            class: 'btn btn-sm',
            text: '↗ 新窗口 Tail',
            title: '在新窗口中跟踪此文件（避免本页卡死）',
            onclick: () => openTailForFileInNewTab(g.server, g.dir, f.name)
          });
          row.appendChild(el('td', { class: 'col-check' }, [cb]));
          row.appendChild(el('td', { title: f.full_path || f.name }, f.name));
          row.appendChild(el('td', { class: 'num', text: formatBytes(f.size) }));
          row.appendChild(el('td', { class: 'muted', text: formatTime(f.mod_time) }));
          row.appendChild(el('td', null, [
            tailBtn, ' ', tailNewWinBtn, ' ',
            el('button', {
              class: 'btn btn-sm',
              text: '复制路径',
              title: f.full_path || '',
              onclick: () => copyToClipboard(f.full_path || ((g.dir || '') + '/' + f.name))
            })
          ]));
          row.appendChild(statusCell);
          tbody.appendChild(row);
        });
        tbl.appendChild(tbody);
        grp.appendChild(el('div', { class: 'table-scroll' }, [tbl]));
        fileTableWrap.appendChild(grp);
      });

      fileTableWrap._toolbar = { summary, btnDownloadSel, btnCancel, btnPickAll, btnPickNone, btnPickTop };
      refreshSummary();
      listState.fileStates = {};
    }

    function pickAll(on) {
      fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]').forEach(cb => { cb.checked = on; });
      refreshSummary();
    }
    function pickTop(n) {
      const cbs = fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]');
      cbs.forEach((cb, i) => { cb.checked = i < n; });
      refreshSummary();
    }
    // P1-9 修复：selected_files 改成 target-aware，每项带 server/dir/file/full_path，
    // 解决多服务器多目录同名文件的歧义问题。
    // 返回结构：[{ server, dir, file, full_path }, ...]
    function getSelectedFiles() {
      const out = [];
      fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]').forEach(cb => {
        if (!cb.checked) return;
        out.push({
          server: cb.getAttribute('data-srv') || '',
          dir: cb.getAttribute('data-dir') || '',
          file: cb.getAttribute('data-file') || '',
          full_path: cb.getAttribute('data-full-path') || ''
        });
      });
      return out;
    }
    // 仅返回文件名（向后兼容 doSearch 等需要字符串数组的场景，且 selected 模式按
    // "每个 target 各自取自己的 files"处理时这个辅助方法不再被使用——见 doSearch 改造）
    function getSelectedFileNames() {
      return getSelectedFiles().map(x => x.file);
    }
    function refreshSummary() {
      const tb = fileTableWrap._toolbar;
      if (!tb) return;
      const sel = getSelectedFiles();
      // P1-9 修复：总数按 listState.groups 汇总，不再用 listState.files（多组模式下
      // listState.files 为空，会显示 0/x）
      const total = (listState.groups || []).reduce(
        (n, g) => n + ((g.files || []).length), 0
      );
      tb.summary.textContent = '已选 ' + sel.length + ' / ' + total + ' 个';
      const checkAll = fileTableWrap.querySelector('#ws-file-checkall');
      if (checkAll) {
        checkAll.checked = total > 0 && sel.length === total;
        checkAll.indeterminate = sel.length > 0 && sel.length < total;
      }
    }

    function setRowStatus(name, status, args, rowClass) {
      const cell = fileTableWrap.querySelector('[data-status-key="' + cssEscape(name) + '"]');
      if (cell) {
        while (cell.firstChild) cell.removeChild(cell.firstChild);
        const node = buildStatusNode(status, args || {});
        if (node) cell.appendChild(node);
      }
      const row = fileTableWrap.querySelector('tr[data-key="' + cssEscape(name) + '"]');
      if (row && rowClass) {
        row.classList.remove('row-done', 'row-fail', 'row-active');
        row.classList.add(rowClass);
      }
    }

    function ensureDlLatestFileRow(server, dir, file) {
      const key = (server || '') + '|' + (dir || '') + '|' + file;
      if (fileTableWrap.querySelector('tr[data-key="' + cssEscape(key) + '"]')) return;
      const groupKey = (server || '') + '||' + (dir || '');
      let tbody = fileTableWrap.querySelector('tbody[data-group-key="' + cssEscape(groupKey) + '"]');
      if (!tbody) {
        const grp = el('div', { class: 'server-group ok' });
        grp.appendChild(el('div', { class: 'server-group-head' }, [
          el('span', { class: 'dot dot-ok' }),
          el('span', { class: 'name', text: server }),
          el('span', { class: 'text-dim', text: ' · ' + dir }),
          el('span', { class: 'meta', text: '下载中…' })
        ]));
        const tbl = el('table', { class: 'table' });
        tbl.appendChild(el('thead', null, el('tr', null, [
          el('th', { text: '文件名' }),
          el('th', { text: '大小' }),
          el('th', { class: 'col-status', text: '状态' })
        ])));
        tbody = el('tbody', { 'data-group-key': groupKey });
        tbl.appendChild(tbody);
        grp.appendChild(tbl);
        const container = fileTableWrap.querySelector('#ws-dl-latest-files');
        if (container) container.appendChild(grp);
      }
      const row = el('tr', { 'data-key': key, 'data-srv': server, 'data-dir': dir });
      row.appendChild(el('td', null, file));
      row.appendChild(el('td', { class: 'num muted', text: '-' }));
      row.appendChild(el('td', { class: 'col-status', 'data-status-key': key }));
      tbody.appendChild(row);
    }

    function updateDlLatestGroupMeta(server, dir, text) {
      const groupKey = (server || '') + '||' + (dir || '');
      const tbody = fileTableWrap.querySelector('tbody[data-group-key="' + cssEscape(groupKey) + '"]');
      if (!tbody) return;
      let grp = tbody.parentNode;
      while (grp && !grp.classList.contains('server-group')) grp = grp.parentNode;
      if (!grp) return;
      const meta = grp.querySelector('.server-group-head .meta');
      if (meta) meta.textContent = text;
    }

    function buildStatusNode(status, args) {
      switch (status) {
        case 'pending':
          return el('span', { class: 'dl-pct', text: '等待…' });
        case 'downloading': {
          const bar = el('div', { class: 'dl-bar' });
          const totalKnown = args.total && args.total > 0;
          const pct = totalKnown ? Math.min(100, ((args.written || 0) / args.total) * 100) : 0;
          const fill = el('div', {
            class: 'dl-bar-fill' + (totalKnown ? '' : ' indeterminate'),
            style: totalKnown ? ('width:' + pct + '%') : ''
          });
          bar.appendChild(fill);
          const txt = el('span', { class: 'dl-pct', text: pctText(args.written || 0, args.total || -1) });
          const wrap = el('span');
          wrap.appendChild(bar);
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(txt);
          return wrap;
        }
        case 'done': {
          const span = el('span', { class: 'dl-pct', style: 'color:#10b981' });
          span.appendChild(document.createTextNode('✓ 完成 · ' + formatBytes(args.bytes || 0)));
          return span;
        }
        case 'fail': {
          const span = el('span', { class: 'dl-pct', style: 'color:#ef4444' });
          span.appendChild(document.createTextNode('✗ ' + (args.error || '失败')));
          return span;
        }
        case 'startfail':
          return el('span', { class: 'dl-pct', style: 'color:#ef4444', text: '启动失败' });
        case 'cancel':
          return el('span', { class: 'dl-pct', style: 'color:#999', text: '已停止' });
        default:
          return el('span', { class: 'dl-pct', text: String(status) });
      }
    }

    async function doDownloadSelected() {
      const items = getSelectedFiles(); // P1-9：target-aware [{server,dir,file,full_path}]
      if (!items.length) { toast('请先勾选要下载的文件', 'warn'); return; }
      if (listState.dlId) { toast('已有下载任务在进行中', 'warn'); return; }

      // P1-9 修复：按 (server, dir) 分组，每组起一个 download session。
      // 这样多服务器多目录同名文件不会混。
      const groups = new Map(); // key: server + '|' + dir
      items.forEach(it => {
        const k = (it.server || '') + '|' + (it.dir || '');
        if (!groups.has(k)) groups.set(k, { server: it.server, dir: it.dir, items: [] });
        groups.get(k).items.push(it);
      });

      const wantZip = dlZipChk.checked;
      const totalAll = items.length;
      const wantZipPerGroup = wantZip && totalAll >= 2;
      if (wantZip && totalAll < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
      }

      // 初始化所有行状态为 pending
      listState.fileStates = {};
      items.forEach(it => {
        const key = (it.server || '') + '|' + (it.dir || '') + '|' + it.file;
        listState.fileStates[key] = { status: 'pending' };
        setRowStatus(key, 'pending');
      });
      const tb = fileTableWrap._toolbar;
      if (tb) { tb.btnDownloadSel.disabled = true; tb.btnCancel.disabled = false; }
      setStatus('busy', '下载中…');

      // 当前活动 session（多组并发时只允许一个 cancel；新策略改为只支持一个 active DL，
      // 因此改成"串行下载"——一组的 done 事件触发后再启下一组，UX 更可控）
      listState.dlId = null;
      listState.dlEvtSrc = null;
      listState.dlMode = 'selected';
      listState.dlApiBase = '/api/files/download/';
      listState.dlLatestUi = null;
      const groupsArr = Array.from(groups.values());
      const results = [];
      const dlResults = []; // 收集各组成功下载的 result items，for 循环结束后统一渲染
      for (let gi = 0; gi < groupsArr.length; gi++) {
        const g = groupsArr[gi];
        // full_path 为空时用 dir + '/' + file 兜底
        const paths = g.items.map(it => it.full_path || ((it.dir || '') + '/' + it.file));
        const groupZip = wantZipPerGroup && g.items.length >= 2;
        try {
          const r = await api('POST', '/api/files/download', Object.assign({}, credsOne(g.server), {
            paths: paths, zip: groupZip,
            target_dir: (dlTargetDirInp.value || '').trim()
          }));
          const dlId = r.id;
          listState.dlId = dlId;
          if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); return; }
          const es = new EventSource(listState.dlApiBase + dlId + '/events');
          listState.dlEvtSrc = es;
          OTB.core.setActiveDL({ id: dlId, evtsrc: es });
          // 等待 done；done 事件的回调负责收集 result item，不自己渲染
          await new Promise((resolve) => {
            let gotDone = false;
            const onDoneSeen = (reason) => {
              if (gotDone) return;
              gotDone = true;
              closeDownloadStream(reason);
              resolve(reason);
            };
            const onDone = (err, item) => {
              if (!err && item) dlResults.push(item);
            };
            es.onmessage = (ev) => {
              let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
              if (o && o.kind === 'done') {
                handleDownloadEvent(o, g.items, g, onDone);
                onDoneSeen('done');
                return;
              }
              handleDownloadEvent(o, g.items, g, onDone);
            };
            es.addEventListener('done', () => { onDoneSeen('done'); });
            es.onerror = () => {
              setTimeout(() => {
                if (listState.dlId === dlId && listState.dlEvtSrc === es && !gotDone) {
                  onDoneSeen('error');
                  toast('SSE 连接异常（已强制收尾）', 'err');
                }
              }, 2000);
            };
          });
          results.push({ server: g.server, dir: g.dir, ok: true });
        } catch (e) {
          toast('下载 ' + g.server + ' / ' + g.dir + ' 失败：' + e.message, 'err');
          g.items.forEach(it => {
            const k = (g.server || '') + '|' + (g.dir || '') + '|' + it.file;
            setRowStatus(k, 'startfail', null, 'row-fail');
          });
          results.push({ server: g.server, dir: g.dir, ok: false, error: e.message });
        }
      }
      if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
      setStatus('idle');
      // 所有组结束后统一渲染下载结果（不再每组 done 单独渲染）
      if (dlResults.length) renderDownloadResults(dlResults);
      const okGroups = results.filter(r => r.ok).length;
      if (okGroups === results.length) {
        let totalDl = 0;
        results.forEach(r => { totalDl += (r.downloads && r.downloads.length) || 0; });
        if (totalDl > 0 && location.hash !== '#/downloads') OTB.core.bumpDlBadge(totalDl);
      }
      toast((okGroups === results.length ? '下载完成：' : '部分失败：') + okGroups + '/' + results.length + ' 组', okGroups === results.length ? 'ok' : 'warn');
    }

    function handleDownloadEvent(o, files, groupCtx, onDone) {
      // files 是 [{server, dir, file}] 数组；用 server|dir|file 做唯一 key
      // groupCtx 是当前组的 {server, dir}，用于构造准确的 result item label
      // onDone 是 done 事件的回调，由调用方提供（多组下载时用于统一收集结果）
      const fmap = {};
      (files || []).forEach(it => { fmap[(it.server || '') + '|' + (it.dir || '') + '|' + it.file] = true; });
      const key = (o.server || '') + '|' + (o.dir || '') + '|' + basenameOf(o.file);
      if (o.kind === 'file_start') {
        listState.fileStates[key] = { status: 'downloading', written: 0, total: o.total || -1 };
        setRowStatus(key, 'downloading', { written: 0, total: o.total || -1 }, 'row-active');
      } else if (o.kind === 'progress') {
        const st = listState.fileStates[key] || {};
        st.status = 'downloading'; st.written = o.written; st.total = o.total;
        listState.fileStates[key] = st;
        setRowStatus(key, 'downloading', { written: o.written, total: o.total }, 'row-active');
      } else if (o.kind === 'file_done') {
        listState.fileStates[key] = { status: 'done', bytes: o.bytes };
        setRowStatus(key, 'done', { bytes: o.bytes }, 'row-done');
      } else if (o.kind === 'done') {
        if (o.ok) {
          toast('下载完成：' + (o.downloads || []).length + ' 个产物', 'ok');
          const srvLabel = groupCtx
            ? (groupCtx.server + ' · ' + (groupCtx.dir.split('/').pop() || groupCtx.dir))
            : listState.serverName;
          const item = { server: srvLabel, downloads: o.downloads, folder: o.folder };
          if (onDone) { onDone(null, item); } else { renderDownloadResults([item]); }
        } else {
          toast('下载失败：' + (o.error || '未知错误'), 'err');
          Object.keys(listState.fileStates).forEach(k => {
            if (fmap[k] || fmap[basenameOf(k)]) {
              const st = listState.fileStates[k];
              if (!st || st.status === 'pending' || st.status === 'downloading') {
                setRowStatus(k, 'fail', { error: o.error || '失败' }, 'row-fail');
              }
            }
          });
          if (onDone) { onDone(o.error); }
        }
        listState.dlId = null;
        listState.dlEvtSrc = null;
        setStatus('idle');
        const tb = fileTableWrap._toolbar;
        if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
      }
    }

    function closeDownloadStream(reason) {
      if (listState.dlAbort) {
        try { listState.dlAbort(); } catch (_) { /* ignore */ }
        listState.dlAbort = null;
      }
      if (listState.dlEvtSrc) {
        listState.dlEvtSrc.close();
        listState.dlEvtSrc = null;
      }
      OTB.core.clearActiveDL();
      const tb = fileTableWrap._toolbar;
      if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
      if (listState.dlLatestUi) {
        btnDownload.disabled = false;
        if (listState.dlLatestUi.cancelBtn) listState.dlLatestUi.cancelBtn.disabled = true;
        if (listState.dlLatestUi.summary) listState.dlLatestUi.summary.textContent = reason === 'cancel' ? '已停止' : '完成';
      }
      listState.dlMode = null;
      listState.dlApiBase = '';
      listState.dlLatestUi = null;
      listState.dlId = null;
      if (reason === 'cancel') {
        toast('已停止下载', 'warn');
      }
      setStatus('idle');
    }

    async function doCancelDownload() {
      if (!listState.dlId) return;
      const base = listState.dlApiBase || '/api/files/download/';
      try { await api('POST', base + listState.dlId + '/cancel', {}); }
      catch (e) { /* ignore */ }
      closeDownloadStream('cancel');
    }

    async function doDownload() {
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      if (!(await confirmManyTargets(targets, '下载最新日志'))) return;
      if (listState.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const latest = Number(dlNSel.value) || 1;
      const wantZip = dlZipChk.checked;
      let zip = wantZip;
      if (zip && latest < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
        zip = false;
      }

      listState.fileStates = {};
      fileTableWrap.innerHTML = '';
      fileTableWrap.appendChild(el('h3', { text: '下载最新 ' + latest + ' 个文件 · 实时进度' }));
      const cancelBtn = el('button', { class: 'btn', text: '停止下载' });
      const summary = el('div', { class: 'text-dim', text: '准备中…' });
      const toolbar = el('div', { class: 'file-toolbar' }, [summary, cancelBtn]);
      fileTableWrap.appendChild(toolbar);
      const filesContainer = el('div', { id: 'ws-dl-latest-files' });
      fileTableWrap.appendChild(filesContainer);
      listState.dlLatestUi = { cancelBtn: cancelBtn, summary: summary };
      btnDownload.disabled = true;
      setStatus('busy', '下载中…');

      listState.dlMode = 'latest';
      listState.dlApiBase = '/api/logs/download/';
      listState.dlId = null;
      listState.dlEvtSrc = null;
      listState.dlAbort = null;

      let cancelled = false;
      const results = [];
      const dlResults = [];

      cancelBtn.onclick = async () => {
        if (cancelled) return;
        cancelled = true;
        summary.textContent = '正在停止…';
        cancelBtn.disabled = true;
        if (listState.dlId) {
          try { await api('POST', listState.dlApiBase + listState.dlId + '/cancel', {}); }
          catch (_) { /* ignore */ }
        }
        if (listState.dlAbort) {
          try { listState.dlAbort(); } catch (_) { /* ignore */ }
        }
        toast('已停止下载', 'warn');
      };

      for (let ti = 0; ti < targets.length; ti++) {
        if (cancelled) break;
        const tgt = targets[ti];
        const groupCtx = { server: tgt.server, dir: tgt.dir };
        const srvLabel = tgt.server + ' · ' + (tgt.dir.split('/').pop() || tgt.dir);
        if (listState.dlLatestUi) {
          listState.dlLatestUi.summary.textContent = '正在下载 (' + (ti + 1) + '/' + targets.length + ') · ' + srvLabel;
        }
        const currentGroupFiles = [];
        let runReason = 'done';
        try {
          const r = await api('POST', '/api/logs/download-latest', {
            system: sysSel.value, server: tgt.server, dir: tgt.dir,
            username: userInp.value, password: passInp.value,
            latest: latest, zip: zip,
            target_dir: (dlTargetDirInp.value || '').trim()
          });
          if (cancelled) break;
          const dlId = r.id;
          listState.dlId = dlId;
          if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); break; }
          const es = new EventSource(listState.dlApiBase + dlId + '/events');
          listState.dlEvtSrc = es;
          OTB.core.setActiveDL({ id: dlId, evtsrc: es });
          runReason = await new Promise((resolve) => {
            let gotDone = false;
            let active = true;
            const finish = (reason) => {
              if (gotDone) return;
              gotDone = true;
              active = false;
              listState.dlAbort = null;
              try { es.close(); } catch (_) { /* ignore */ }
              if (listState.dlEvtSrc === es) { listState.dlEvtSrc = null; }
              OTB.core.clearActiveDL();
              resolve(reason);
            };
            listState.dlAbort = () => finish('cancel');
            const onDone = (err, item) => {
              if (!err && item) {
                item.server = srvLabel;
                dlResults.push(item);
              }
            };
            es.onmessage = (ev) => {
              if (!active) return;
              let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
              if (o.kind === 'file_start') {
                const fileName = basenameOf(o.file);
                ensureDlLatestFileRow(o.server || tgt.server, o.dir || tgt.dir, fileName);
                const alreadyAdded = currentGroupFiles.some(f => f.file === fileName && f.server === (o.server || tgt.server) && f.dir === (o.dir || tgt.dir));
                if (!alreadyAdded) {
                  currentGroupFiles.push({ server: o.server || tgt.server, dir: o.dir || tgt.dir, file: fileName });
                }
              }
              if (o.kind === 'file_done') {
                updateDlLatestGroupMeta(o.server || tgt.server, o.dir || tgt.dir, '已完成 ' + currentGroupFiles.length + ' 个文件');
              }
              if (o && o.kind === 'done') {
                handleDownloadEvent(o, currentGroupFiles, groupCtx, onDone);
                finish('done');
                return;
              }
              handleDownloadEvent(o, currentGroupFiles, groupCtx, onDone);
            };
            es.addEventListener('done', () => finish('done'));
            es.onerror = () => {
              setTimeout(() => {
                if (active && !gotDone && !cancelled) {
                  finish('error');
                  toast('SSE 连接异常（已强制收尾）', 'err');
                } else if (active && !gotDone) {
                  finish('cancel');
                }
              }, 2000);
            };
          });
          if (runReason !== 'cancel') {
            results.push({ server: tgt.server, dir: tgt.dir, ok: runReason !== 'error' });
            updateDlLatestGroupMeta(tgt.server, tgt.dir, runReason === 'done' ? '完成' : '连接异常');
          } else {
            cancelled = true;
          }
        } catch (e) {
          if (cancelled) break;
          toast('下载 ' + srvLabel + ' 失败：' + e.message, 'err');
          currentGroupFiles.forEach(it => {
            const k = (it.server || '') + '|' + (it.dir || '') + '|' + it.file;
            setRowStatus(k, 'startfail', null, 'row-fail');
          });
          results.push({ server: tgt.server, dir: tgt.dir, ok: false, error: e.message });
          updateDlLatestGroupMeta(tgt.server, tgt.dir, '失败：' + e.message);
          const failTbody = fileTableWrap.querySelector('tbody[data-group-key="' + cssEscape(tgt.server + '||' + tgt.dir) + '"]');
          if (failTbody) {
            let failGrp = failTbody.parentNode;
            while (failGrp && !failGrp.classList.contains('server-group')) failGrp = failGrp.parentNode;
            if (failGrp) { failGrp.classList.remove('ok'); failGrp.classList.add('fail'); }
          }
        }
        if (runReason === 'cancel' || cancelled) break;
      }

      btnDownload.disabled = false;
      if (listState.dlLatestUi) {
        listState.dlLatestUi.cancelBtn.disabled = true;
        listState.dlLatestUi.summary.textContent = cancelled ? '已停止' : '下载完成';
      }
      listState.dlMode = null;
      listState.dlApiBase = '';
      listState.dlId = null;
      listState.dlEvtSrc = null;
      listState.dlLatestUi = null;
      listState.dlAbort = null;
      OTB.core.clearActiveDL();
      setStatus('idle');

      if (cancelled) {
        // 已在 cancelBtn.onclick 或 closeDownloadStream 中 toast
      } else {
        if (dlResults.length) renderDownloadResults(dlResults);
        const okGroups = results.filter(r => r.ok).length;
        let totalDl = 0;
        dlResults.forEach(r => { totalDl += (r.downloads && r.downloads.length) || 0; });
        if (totalDl > 0 && location.hash !== '#/downloads') OTB.core.bumpDlBadge(totalDl);
        toast((okGroups === results.length ? '下载完成：' : '部分失败：') + okGroups + '/' + results.length + ' 组', okGroups === results.length ? 'ok' : 'warn');
        for (const r of results) {
          if (r.ok) await maybeSaveCred(r.server);
        }
        refreshCredStatus();
      }
    }

    function renderDownloadResults(allResults) {
      // P0-4：记录本次下载的落地目录，供 openLocalFolder/revealLocal 使用（自定义 target_dir 时需要）
      const dlFolder = (allResults.find(r => r.folder) || {}).folder || '';
      listState.lastDownloadFolder = dlFolder;
      const existing = $('#ws-dl-results');
      if (existing) existing.remove();
      const wrap = el('div', { id: 'ws-dl-results', class: 'card' }, [
        el('h3', { text: '下载结果' })
      ]);
      allResults.forEach(r => {
        const grp = el('div', { class: 'server-group ' + (r.error ? 'fail' : 'ok') });
        if (r.error) {
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'name', text: r.server }),
            el('span', { class: 'meta', text: '下载失败' })
          ]));
          grp.appendChild(el('div', { class: 'err-msg', text: r.error }));
        } else {
          const files = (r.downloads || []).filter(d => d.kind !== 'zip');
          const zips = (r.downloads || []).filter(d => d.kind === 'zip');
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'name', text: r.server }),
            el('span', { class: 'meta', text: files.length + ' 个文件 + ' + zips.length + ' 个 zip' })
          ]));
          const inner = el('div', { style: 'padding: 8px 12px; font-size: 12.5px;' });
          if (files.length) {
            const block = el('div');
            files.forEach(d => {
              const row = el('div');
              row.appendChild(document.createTextNode('· ' + d.file + ' → '));
              row.appendChild(el('a', {
                href: '/downloads/' + encodeURIComponent(d.local),
                text: d.local,
              }));
              row.appendChild(document.createTextNode('（' + formatBytes(d.bytes) + '）'));
              // v0.5-F P1-12：在文件管理器中显示 + 复制绝对路径
              if (d.abs_path) {
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📂 打开', style: 'margin-left:8px;',
                  title: '在 Finder/Explorer 中显示（' + d.abs_path + '）',
                  onclick: () => revealLocal(d.abs_path)
                }));
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📋', style: 'margin-left:4px;',
                  title: '复制绝对路径：' + d.abs_path,
                  onclick: () => copyToClipboard(d.abs_path)
                }));
              }
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          if (zips.length) {
            const block = el('div', { class: 'mt-2' });
            zips.forEach(d => {
              const row = el('div');
              row.appendChild(document.createTextNode('· 📦 '));
              row.appendChild(el('a', {
                href: '/downloads/' + encodeURIComponent(d.local),
                text: d.local,
              }));
              row.appendChild(document.createTextNode('（' + formatBytes(d.bytes) + '）'));
              if (d.abs_path) {
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📂 打开', style: 'margin-left:8px;',
                  title: '在 Finder/Explorer 中显示（' + d.abs_path + '）',
                  onclick: () => revealLocal(d.abs_path)
                }));
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📋', style: 'margin-left:4px;',
                  title: '复制绝对路径：' + d.abs_path,
                  onclick: () => copyToClipboard(d.abs_path)
                }));
              }
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          grp.appendChild(inner);
        }
        wrap.appendChild(grp);
      });
      // v0.5-F P1-12：底部明确显示保存目录 + 一键打开 + 复制
      const folder = (allResults.find(r => r.folder) || {}).folder || '-';
      const folderRow = el('div', {
        class: 'text-dim mt-2',
        style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap;'
      }, [
        el('span', null, [document.createTextNode('本地保存目录：' + folder)])
      ]);
      if (folder && folder !== '-') {
        folderRow.appendChild(el('button', {
          class: 'btn btn-sm', text: '📁 打开目录',
          title: '在 Finder/Explorer 中打开目录',
          onclick: () => openLocalFolder(folder)
        }));
        folderRow.appendChild(el('button', {
          class: 'btn btn-sm', text: '📋 复制路径',
          title: '复制目录绝对路径到剪贴板',
          onclick: () => copyToClipboard(folder)
        }));
      }
      wrap.appendChild(folderRow);
      fileTableWrap.parentNode.insertBefore(wrap, fileTableWrap.nextSibling);
    }

    // v0.5-F P1-12：调后端 reveal / open folder 接口
    // P0-4 修复：传 folder 字段，让后端允许自定义 target_dir 的路径
    async function revealLocal(absPath) {
      try {
        await api('POST', '/api/local/reveal-file', { path: absPath, folder: listState.lastDownloadFolder || '' });
      } catch (e) {
        toast('打开失败：' + e.message, 'err');
      }
    }
    async function openLocalFolder(absDir) {
      try {
        await api('POST', '/api/local/open-folder', { path: absDir, folder: listState.lastDownloadFolder || '' });
      } catch (e) {
        toast('打开目录失败：' + e.message, 'err');
      }
    }
    // 复制文本到剪贴板（fallback：旧浏览器走 prompt）
    function copyToClipboard(text) {
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(
          () => toast('已复制：' + text, 'ok'),
          () => fallbackCopy(text)
        );
      } else {
        fallbackCopy(text);
      }
    }
    function fallbackCopy(text) {
      try {
        const ta = document.createElement('textarea');
        ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        toast('已复制：' + text, 'ok');
      } catch (e) {
        toast('复制失败：' + e.message, 'err');
      }
    }

    async function doSearch() {
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      if (!queryInp.value.trim()) { toast('搜索表达式不能为空', 'warn'); return; }
      if (!(await confirmManyTargets(targets, '搜索日志'))) return;
      pushSearchHistory(queryInp.value);
      const scope = Object.keys(scopeRadios).filter(k => !k.endsWith('Label') && scopeRadios[k].checked)[0] || 'latest';
      let selectedItems = null;
      if (scope === 'selected') {
        if (searchSelectedFiles.length > 0) {
          selectedItems = searchSelectedFiles;
        } else {
          selectedItems = getSelectedFiles();
        }
        if (!selectedItems || !selectedItems.length) {
          toast('「指定文件」模式：请先点击「选择文件…」按钮选定要搜索的文件', 'warn');
          return;
        }
      }
      hitTableWrap.style.display = '';
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '并行搜索中…' }));
      const conc = Number(concSel.value) || 8;
      const contextN = getContextLineCount();
      const timeRange = buildTimeRange();
      // v0.5 #8：文件名参数 — 单文件 / 多文件 / glob 模糊匹配
      const filePatternsRaw = (filePatternInp.value || '').trim();
      const filePatterns = filePatternsRaw
        ? filePatternsRaw.split(/[,\s\n]+/).map(s => s.trim()).filter(Boolean)
        : null;
      try {
        const qs = new URLSearchParams();
        if (timeRange.since) qs.set('since', timeRange.since);
        if (timeRange.until) qs.set('until', timeRange.until);
        const body = {
          system: sysSel.value,
          targets: targets,
          query: queryInp.value,
          files: Number(filesNSel.value),
          max_concurrency: conc,
          scope_mode: scope, // v0.5-G：latest/selected/glob
          username: userInp.value,
          password: passInp.value
        };
        if (filePatterns) body.file_patterns = filePatterns;
        if (contextN > 0) body.context = contextN;
        if (selectedItems) {
          // P1-9：传 selected_file_targets（新格式），按 (server, dir) 区分
          // 后端会优先用 per-target 列表，匹配不上再退到老的 selected_files。
          body.selected_file_targets = selectedItems.map(x => ({
            server: x.server, dir: x.dir, file: x.file
          }));
        }
        const url = '/api/logs/search/multi' + (qs.toString() ? '?' + qs : '');
        const r = await api('POST', url, body);
        renderMultiResults(r);
        let toastMsg = '命中 ' + r.total_hits + ' 条，' + r.ok_count + '/' + targets.length + ' 组成功';
        if (timeRange.since || timeRange.until) toastMsg += '（时间范围已应用）';
        if (filePatterns) toastMsg += '（文件名过滤：' + filePatterns.join(', ') + '）';
        toast(toastMsg, r.fail_count > 0 ? 'warn' : 'ok');
        // v0.6：search tab badge 显示命中数
        setTabBadge('search', r.total_hits > 0 ? (r.total_hits + ' 命中') : '0');
        for (const srv of (r.servers || [])) {
          if (srv.ok) await maybeSaveCred(srv.server);
        }
        refreshCredStatus();
      } catch (e) {
        hitTableWrap.innerHTML = '';
        hitTableWrap.appendChild(el('div', { class: 'text-err', text: '搜索失败：' + e.message }));
        toast('搜索失败：' + e.message, 'err');
      }
    }

    function renderMultiResults(r) {
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '搜索结果 · ' + r.ok_count + '/' + r.servers.length + ' 成功，共 ' + r.total_hits + ' 条命中' }));
      hitTableWrap.appendChild(el('div', { class: 'text-dim mb-2', text: '并发 ' + r.max_concurrency + '，按服务器分组展示' }));

      if (!r.servers || !r.servers.length) {
        hitTableWrap.appendChild(el('div', { class: 'text-dim', text: '没有结果。' }));
        return;
      }

      r.servers.forEach(srv => {
        const grp = el('div', { class: 'server-group ' + (srv.ok ? 'ok' : 'fail') });
        const headChildren = [
          el('span', { class: 'dot dot-' + (srv.ok ? (srv.hits_count > 0 ? 'ok' : 'idle') : 'err') }),
          el('span', { class: 'name', text: srv.server + (srv.host ? '  ·  ' + srv.host : '') })
        ];
        if (srv.ok && srv.encoding) {
          headChildren.push(el('span', { class: 'tag tag-enc', title: '当前目录编码（影响中文显示）', text: srv.encoding }));
        }
        headChildren.push(el('span', { class: 'meta', text:
          (srv.ok
            ? (srv.hits_count + ' 命中 / ' + (srv.files || []).length + ' 文件 / ' + srv.elapsed_ms + 'ms')
            : '失败 / ' + srv.elapsed_ms + 'ms') }));
        const head = el('div', { class: 'server-group-head' }, headChildren);
        grp.appendChild(head);
        if (!srv.ok) {
          grp.appendChild(el('div', { class: 'err-msg', text: srv.error || '未知错误' }));
        } else if (srv.hits_count === 0) {
          grp.appendChild(el('div', { class: 'text-dim', style: 'padding: 8px 12px;', text: '本台无匹配' }));
        } else {
          const tbl = el('table', { class: 'table' });
          const thead = el('thead', null, el('tr', null, [
            el('th', { text: '文件' }),
            el('th', { text: '行号' }),
            el('th', { text: '内容' }),
            el('th', { text: '操作' })
          ]));
          tbl.appendChild(thead);
          const tbody = el('tbody');
          srv.hits.forEach(h => {
            const isCtx = !!h.is_context;
            const fullContent = h.content || '';
            const shortContent = trimMiddle(fullContent, 280);
            const cells = [
              el('td', { class: 'muted' + (isCtx ? ' text-dim' : ''), text: h.file }),
              el('td', { class: 'num' + (isCtx ? ' text-dim' : ''), text: (isCtx ? '┊ ' : '') + h.line_no })
            ];
            const contentCell = el('td', { class: 'hit-line' + (isCtx ? ' ctx-line' : ''), text: (isCtx ? '┊ ' : '') + shortContent });
            if (!isCtx && looksMojibake(shortContent)) {
              contentCell.appendChild(el('span', { class: 'tag tag-warn', title: '当前目录编码与文件实际编码不一致，中文可能错位。试试切换到「GBK」目录。', text: '⚠ 解码可能有误' }));
            }
            cells.push(contentCell);
            if (isCtx) {
              cells.push(el('td', { class: 'actions text-dim', text: '' }));
            } else {
              const btnExpand = el('button', {
                class: 'btn btn-sm',
                text: '展开',
                onclick: () => {
                  const expanded = contentCell.getAttribute('data-expanded') === '1';
                  contentCell.textContent = expanded ? shortContent : fullContent;
                  contentCell.setAttribute('data-expanded', expanded ? '0' : '1');
                  btnExpand.textContent = expanded ? '展开' : '收起';
                }
              });
              cells.push(el('td', { class: 'actions' }, [
                btnExpand,
                el('button', { class: 'btn btn-sm', text: '复制', onclick: () => copyToClipboard(fullContent) }),
                el('button', { class: 'btn btn-sm', text: '上下文', onclick: () => doContext(h) }),
                el('button', { class: 'btn btn-sm', text: 'Tail', onclick: () => startTailForFile(h.server, h.dir, h.file) })
              ]));
            }
            const tr = el('tr', { class: isCtx ? 'ctx-row' : '' }, cells);
            tbody.appendChild(tr);
          });
          tbl.appendChild(tbody);
          grp.appendChild(el('div', { class: 'table-scroll' }, [tbl]));
        }
        hitTableWrap.appendChild(grp);
      });
    }

    async function doContext(hit) {
      const contextN = getContextLineCount();
      const body = Object.assign({}, credsOne(hit.server), {
        dir: hit.dir || dirSel.value,
        file: hit.file, line: hit.line_no,
        before: contextN, after: contextN
      });
      try {
        const r = await api('POST', '/api/logs/context', body);
        renderContext(r.lines || [], hit, contextN);
        ctxCard.style.display = '';
        ctxCard.scrollIntoView({ behavior: 'smooth' });
      } catch (e) { toast('上下文获取失败：' + e.message, 'err'); }
    }

    function renderContext(lines, hit, contextN) {
      ctxCard.innerHTML = '';
      const rangeText = contextN > 0 ? ('前后 ' + contextN + ' 行') : '仅命中行';
      ctxCard.appendChild(el('h3', { text: '上下文 · ' + hit.server + ' · ' + hit.file + ':' + hit.line_no + ' · ' + rangeText }));
      const view = el('div', { class: 'context-view' });
      lines.forEach(l => {
        view.appendChild(el('div', { class: 'row' + (l.hit ? ' hit' : '') }, [
          el('div', { class: 'ln', text: l.line_no }),
          el('div', { class: 'ct', text: l.content })
        ]));
      });
      ctxCard.appendChild(view);
    }

    // v0.7-Redesign（第二波+第三波）：
//   formCard 现在只承担"目标选择 + 凭据"两件事，折叠态下只剩一行摘要。
//   列出文件/下载最新 已经移到 files tab 顶部 toolbar。
//   测试连接 留在 formCard 底部 —— 它是通用的前置确认动作，所有 tab 都要先看它。
const formCard = el('div', { class: 'card' }, [
      targetSummaryRow,
      targetBody,
    ]);
    // targetBody 内部是完整表单（折叠时整段隐藏）
    targetBody.appendChild(el('h3', { text: 'WebSphere 日志助手 · 多服务器并行' }));
    targetBody.appendChild(el('div', {
      class: 'card-desc',
      text: '先选业务系统 → 勾选目标服务器 → 在下面展开的日志目录里多选要操作的目录 → 输入凭据 → 点「测试连接」确认 SSH 通畅。'
    }));
    targetBody.appendChild(el('div', { class: 'grid-2' }, [
      el('div', null, [el('label', { text: '业务系统' }), sysSel]),
      el('div', null, [el('label', { text: '默认目录（多目录勾选未选时回退到此）' }), dirSel])
    ]));
    targetBody.appendChild(el('div', { class: 'mt-2' }, [srvPickToolbar, srvPickWrap]));
    targetBody.appendChild(el('div', { class: 'mt-2' }, [srvDirsToolbar, srvDirsWrap]));
    targetBody.appendChild(el('div', { class: 'grid-2 mt-2' }, [
      el('div', null, [el('label', { text: 'SSH 用户名' }), userInp]),
      el('div', null, [el('label', { text: 'SSH 密码' }), passInp])
    ]));
    targetBody.appendChild(el('div', { class: 'mt-1' }, [rememberLbl, credStatusRow]));
    targetBody.appendChild(el('div', { class: 'btn-row mt-3' }, [btnTest]));

    // v0.5 #8：文件名参数 — 单文件 / 多文件 / glob 模糊匹配（空格或逗号分隔）
    // P1-08 改进：明确语义 — 填了 glob 后就只用 glob 匹配，N 仍控制"取最新 N 个匹配上的"
    const filePatternInp = el('input', { type: 'text', id: 'ws-file-pattern', placeholder: '可选 glob（逗号/空格分隔）：例 SystemOut*.log 或 *.log,*.txt' });
    const latestScopeField = el('div', null, [el('label', { text: '最近文件数（每台服务器每个目录）' }), filesNSel]);
    const globScopeField = el('div', null, [
      el('label', { text: '文件名 glob（逗号/空格分隔）' }),
      filePatternInp,
      el('div', { class: 'text-dim', style: 'font-size:11.5px; margin-top:2px;', text: '填 glob 后，N 仍限制"取匹配文件中的最新 N 个"' })
    ]);

    const contextInp = el('input', { type: 'number', id: 'ws-context', min: '0', max: '50', value: '0', style: 'width:100%;' });
    function getContextLineCount() {
      let n = Number(contextInp.value);
      if (!Number.isFinite(n)) n = 0;
      n = Math.floor(n);
      if (n < 0) n = 0;
      if (n > 50) n = 50;
      return n;
    }

    // v0.5-G P1-08：搜索范围三选一（latest / selected / glob）
    // - latest  默认，列最近 N 个
    // - glob    用 filePatternInp 当文件名 glob
    // - selected 用「先列文件再勾选」的多选区（listState.files 里勾）
    const scopeRadios = {};
    [
      ['latest', '最近 N 个'],
      ['glob', '按 glob 匹配'],
      ['selected', '指定文件']
    ].forEach(([v, lbl]) => {
      const r = el('input', { type: 'radio', name: 'ws-scope', value: v });
      if (v === 'latest') r.checked = true;
      r.addEventListener('change', updateScopeVisibility);
      scopeRadios[v] = r;
      scopeRadios[v + 'Label'] = el('label', { class: 'inline' }, [r, document.createTextNode(' ' + lbl)]);
    });
    const scopeRow = el('div', { class: 'mt-2', style: 'display:flex; gap:14px; align-items:center; flex-wrap:wrap;' }, [
      el('span', { class: 'lbl', text: '搜索文件范围：' }),
      scopeRadios.latestLabel, scopeRadios.globLabel, scopeRadios.selectedLabel
    ]);
    const searchSelSummary = el('div', { class: 'text-dim', id: 'ws-search-sel-summary', text: '尚未选择文件' });
    const btnSearchPickFiles = el('button', {
      class: 'btn btn-sm mt-1',
      text: '📋 选择文件…',
      onclick: openSearchFilePicker
    });
    const btnSearchClearFiles = el('button', {
      class: 'btn btn-sm mt-1',
      text: '✕ 清空选择',
      style: 'display:none',
      onclick: () => {
        searchSelectedFiles = [];
        updateSearchSelSummary();
      }
    });
    const fileListArea = el('div', { id: 'ws-file-list-area', style: 'display:none', class: 'mt-2' }, [
      el('div', { style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap;' }, [
        btnSearchPickFiles,
        btnSearchClearFiles,
        searchSelSummary
      ])
    ]);
    function updateSearchSelSummary() {
      if (!searchSelectedFiles.length) {
        searchSelSummary.textContent = '尚未选择文件';
        searchSelSummary.style.color = '';
        btnSearchClearFiles.style.display = 'none';
      } else {
        const bySrv = {};
        searchSelectedFiles.forEach(f => {
          const k = f.server + ' / ' + (f.dir.split('/').pop() || f.dir);
          bySrv[k] = (bySrv[k] || 0) + 1;
        });
        const parts = Object.keys(bySrv).map(k => k + ': ' + bySrv[k] + ' 个');
        searchSelSummary.textContent = '✓ 已选 ' + searchSelectedFiles.length + ' 个文件（' + parts.join('，') + '）';
        searchSelSummary.style.color = 'var(--success)';
        btnSearchClearFiles.style.display = '';
      }
    }
    function updateScopeVisibility() {
      const sel = Object.keys(scopeRadios).filter(k => !k.endsWith('Label') && scopeRadios[k].checked)[0];
      latestScopeField.style.display = (sel === 'latest') ? '' : 'none';
      globScopeField.style.display = (sel === 'glob') ? '' : 'none';
      fileListArea.style.display = (sel === 'selected') ? '' : 'none';
      const disableTime = sel === 'selected';
      timeSel.disabled = disableTime;
      timeFromInp.disabled = disableTime;
      timeToInp.disabled = disableTime;
      timeHint.textContent = disableTime
        ? '指定文件模式下不应用时间粗筛'
        : '按文件修改时间粗筛，不按日志行时间筛选';
    }

    async function openSearchFilePicker() {
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      setStatus('busy', '加载文件列表…');
      let pickerGroups = [];
      try {
        const r = await api('POST', '/api/logs/list/targets', {
          system: sysSel.value,
          targets: targets,
          username: userInp.value,
          password: passInp.value
        });
        const results = Array.isArray(r) ? r : (r.servers || r.results || []);
        pickerGroups = results.map(srv => ({
          server: srv.server,
          dir: srv.dir,
          files: (srv.files || []).map(f => ({
            name: f.name,
            full_path: f.full_path,
            size: f.size,
            mod_time: f.mod_time
          })),
          error: srv.ok ? null : (srv.error || '未知错误')
        }));
      } catch (e) {
        toast('列文件失败：' + e.message, 'err');
        setStatus('idle');
        return;
      }
      setStatus('idle');
      const totalFiles = pickerGroups.reduce((a, g) => a + (g.files || []).length, 0);
      if (!totalFiles) {
        toast('所选目标下没有文件', 'warn');
        return;
      }

      const old = document.getElementById('ws-search-pick-modal');
      if (old) old.remove();
      const overlay = el('div', { id: 'ws-search-pick-modal', class: 'preview-overlay' });
      const box = el('div', { class: 'preview-box', style: 'width: min(860px, 94vw); max-height: 88vh;' });
      const head = el('div', { class: 'preview-head' });
      head.appendChild(el('strong', { text: '选择要搜索的文件 · 共 ' + totalFiles + ' 个' }));
      const headBtns = el('div', { style: 'display:flex; gap:6px; margin-left:auto;' });
      const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => togglePickerAll(true) });
      const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => togglePickerAll(false) });
      const btnInvert = el('button', { class: 'btn btn-sm', text: '反选', onclick: togglePickerInvert });
      const btnConfirm = el('button', { class: 'btn btn-sm btn-primary', text: '确认选择', onclick: confirmPickerSelection });
      const btnClose = el('button', { class: 'btn btn-sm', text: '取消', onclick: () => overlay.remove() });
      headBtns.appendChild(btnPickAll);
      headBtns.appendChild(btnPickNone);
      headBtns.appendChild(btnInvert);
      headBtns.appendChild(btnConfirm);
      headBtns.appendChild(btnClose);
      head.appendChild(headBtns);
      box.appendChild(head);

      const filterInp = el('input', {
        type: 'text',
        placeholder: '过滤文件名（子串 / 通配符 * ?）',
        style: 'margin: 8px 12px; width: calc(100% - 24px);'
      });
      box.appendChild(filterInp);

      const pickerSummary = el('div', { class: 'text-dim', style: 'padding: 0 12px 6px; font-size:12px;', text: '已选 0 / ' + totalFiles });
      box.appendChild(pickerSummary);

      const listWrap = el('div', { style: 'max-height: calc(88vh - 160px); overflow:auto; padding: 0 8px 8px;' });

      const preSelected = new Set();
      searchSelectedFiles.forEach(f => preSelected.add(f.server + '|' + f.dir + '|' + f.file));

      function getFilterQ() {
        return (filterInp.value || '').trim().toLowerCase();
      }
      function matchFilter(name) {
        const q = getFilterQ();
        if (!q) return true;
        const nl = (name || '').toLowerCase();
        if (q.indexOf('*') !== -1 || q.indexOf('?') !== -1) {
          const wildcardsReplaced = q.replace(/\*/g, '\u0001').replace(/\?/g, '\u0002');
          const escaped = wildcardsReplaced.replace(/[.+^${}()|[\]\\]/g, '\\$&')
            .replace(/\u0001/g, '.*').replace(/\u0002/g, '.');
          try { return new RegExp(escaped, 'i').test(nl); } catch (e) { return true; }
        }
        return nl.indexOf(q) !== -1;
      }
      function refreshPickerSummary() {
        const checked = listWrap.querySelectorAll('input[type="checkbox"][data-pick-file]:checked').length;
        pickerSummary.textContent = '已选 ' + checked + ' / ' + totalFiles;
      }
      function togglePickerAll(on) {
        listWrap.querySelectorAll('input[type="checkbox"][data-pick-file]').forEach(cb => {
          const row = cb.closest('[data-pick-row]');
          if (row && row.style.display === 'none') return;
          cb.checked = on;
        });
        refreshPickerSummary();
      }
      function togglePickerInvert() {
        listWrap.querySelectorAll('input[type="checkbox"][data-pick-file]').forEach(cb => {
          const row = cb.closest('[data-pick-row]');
          if (row && row.style.display === 'none') return;
          cb.checked = !cb.checked;
        });
        refreshPickerSummary();
      }
      function renderPickerList() {
        listWrap.innerHTML = '';
        const q = getFilterQ();
        pickerGroups.forEach(g => {
          const grp = el('div', { class: 'server-group ' + (g.error ? 'fail' : 'ok') });
          const filtered = (g.files || []).filter(f => matchFilter(f.name));
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'dot dot-' + (g.error ? 'err' : 'ok') }),
            el('span', { class: 'name', text: g.server }),
            el('span', { class: 'text-dim', text: ' · ' + g.dir }),
            el('span', { class: 'meta', text: g.error ? ('失败：' + g.error) : (filtered.length + ' / ' + g.files.length + ' 个文件') })
          ]));
          if (g.error) {
            grp.appendChild(el('div', { class: 'err-msg', text: g.error }));
            listWrap.appendChild(grp);
            return;
          }
          if (q && filtered.length === 0) {
            grp.appendChild(el('div', { class: 'text-dim', style: 'padding:6px 12px;', text: '过滤 "' + q + '" 无匹配' }));
            listWrap.appendChild(grp);
            return;
          }
          const tbl = el('table', { class: 'table' });
          tbl.appendChild(el('thead', null, el('tr', null, [
            el('th', { class: 'col-check' }),
            el('th', { text: '文件名' }),
            el('th', { text: '大小' }),
            el('th', { text: '修改时间' })
          ])));
          const tbody = el('tbody');
          filtered.forEach(f => {
            const key = g.server + '|' + g.dir + '|' + f.name;
            const cb = el('input', {
              type: 'checkbox',
              'data-pick-file': f.name,
              'data-pick-srv': g.server,
              'data-pick-dir': g.dir,
              onchange: refreshPickerSummary
            });
            if (preSelected.has(key)) cb.checked = true;
            const row = el('tr', { 'data-pick-row': '1' });
            row.appendChild(el('td', { class: 'col-check' }, [cb]));
            row.appendChild(el('td', null, f.name));
            row.appendChild(el('td', { class: 'num muted', text: formatBytes(f.size) }));
            row.appendChild(el('td', { class: 'muted', text: formatTime(f.mod_time) }));
            tbody.appendChild(row);
          });
          tbl.appendChild(tbody);
          grp.appendChild(tbl);
          listWrap.appendChild(grp);
        });
        refreshPickerSummary();
      }
      filterInp.addEventListener('input', renderPickerList);

      function confirmPickerSelection() {
        const selected = [];
        listWrap.querySelectorAll('input[type="checkbox"][data-pick-file]:checked').forEach(cb => {
          selected.push({
            server: cb.getAttribute('data-pick-srv') || '',
            dir: cb.getAttribute('data-pick-dir') || '',
            file: cb.getAttribute('data-pick-file') || ''
          });
        });
        if (!selected.length) {
          toast('请至少勾选一个文件', 'warn');
          return;
        }
        searchSelectedFiles = selected;
        updateSearchSelSummary();
        overlay.remove();
        toast('已选择 ' + selected.length + ' 个文件', 'ok');
      }

      box.appendChild(listWrap);
      overlay.appendChild(box);
      overlay.addEventListener('click', (e) => { if (e.target === overlay) overlay.remove(); });
      document.body.appendChild(overlay);
      renderPickerList();
      filterInp.focus();
    }

    // P1-8：动态目标摘要（显示本次搜索将使用的 targets 数量和具体内容）。
    // 必须提前在 searchCard 外声明，否则写在数组里就是非法 JS。
    // 函数会引用 getSelectedTargets()，并写到 #ws-target-summary 节点（render 时挂到 searchCard）。
    const targetSummaryEl = el('div', { id: 'ws-target-summary', class: 'text-dim', style: 'padding-top:18px;' });
    function updateTargetSummary() {
      const targets = getSelectedTargets();
      if (!targets.length) {
        targetSummaryEl.textContent = '尚未勾选任何目标（请在「目标选择」里勾选）';
        targetSummaryEl.style.color = 'var(--text-dim)';
        return;
      }
      const srvs = [...new Set(targets.map(t => t.server))];
      const dirs = targets.length;
      targetSummaryEl.innerHTML = '';
      targetSummaryEl.style.color = '';
      const badge = el('span', { class: 'tag tag-ok', text: '已选 ' + srvs.length + ' 台服务器 / ' + dirs + ' 个 targets' });
      const expandBtn = el('button', {
        class: 'btn btn-sm', text: '▼ 展开查看', style: 'margin-left:6px;',
        onclick: () => {
          const detail = $('#ws-target-detail');
          if (detail) {
            const shown = detail.style.display !== 'none';
            detail.style.display = shown ? 'none' : '';
            expandBtn.textContent = shown ? '▼ 展开查看' : '▲ 收起';
          }
        }
      });
      targetSummaryEl.appendChild(badge);
      targetSummaryEl.appendChild(expandBtn);
      const detail = el('div', { id: 'ws-target-detail', style: 'display:none; margin-top:6px; font-size:12px; line-height:1.6;' });
      const srvMap = {};
      targets.forEach(t => {
        if (!srvMap[t.server]) srvMap[t.server] = [];
        srvMap[t.server].push(t.dir);
      });
      Object.keys(srvMap).forEach(srv => {
        srvMap[srv].forEach(dir => {
          detail.appendChild(el('div', { style: 'color:var(--text-dim);' }, [
            document.createTextNode('  · ' + srv + ' / ' + dir)
          ]));
        });
      });
      targetSummaryEl.appendChild(detail);
    }
    // 让 updateTargetSummary 在目标变化时也能调用（persistSelection 后会触发）
    // 这里把函数挂到 window 上方便从 OTB 钩子（如果有）或调试用，不强制。
    if (typeof window !== 'undefined') window.updateTargetSummary = updateTargetSummary;

    const searchCard = el('div', { class: 'card' }, [
      el('h3', { text: '多服务器并行搜索' }),
      el('div', { class: 'card-desc', unsafeHtml: '语法：<span class="code-inline">A &amp;&amp; B</span>（同包含）、<span class="code-inline">A || B</span>（任一）、<span class="code-inline">!X</span>（排除）。结果按服务器 / 目录分组。' }),
      el('div', { class: 'grid-3' }, [
        el('div', { style: 'grid-column: span 2' }, [el('label', { text: '搜索表达式' }), queryWrap]),
        el('div', null, [el('label', { text: '并发' }), concSel])
      ]),
      scopeRow,
      el('div', { class: 'grid-3 mt-2' }, [
        latestScopeField,
        globScopeField,
        el('div', null, [
          el('label', { text: '上下文行数（0=仅命中行）' }),
          contextInp,
          el('div', { class: 'text-dim', style: 'font-size:11.5px; margin-top:2px;', text: '每个命中行前后各 N 行，点击“上下文”也会用这里的值' })
        ])
      ]),
      fileListArea,
      el('div', { class: 'grid-3 mt-2' }, [
        el('div', null, [el('label', { text: '按文件修改时间粗筛' }), timeSel, timeHint]),
        el('div', { style: 'display:flex; gap:8px; align-items:flex-end;' }, [timeFromInp, timeToInp]),
        // P1-8：目标摘要节点（已在外层声明 + 实现 updateTargetSummary）
        targetSummaryEl
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnSearch])
    ]);
    // searchCard 渲染完成后做一次初始摘要
    updateTargetSummary();

    // ---- 实时 tail ----
    const tailFileInp = el('input', {
      type: 'text', id: 'ws-tail-file', placeholder: '文件名（例：SystemOut.log）', value: 'SystemOut.log',
      autocomplete: 'off', spellcheck: 'false',
      role: 'combobox',
      'aria-autocomplete': 'list',
      'aria-haspopup': 'listbox',
      'aria-expanded': 'false',
      'aria-controls': 'ws-tail-file-popover',
      'aria-activedescendant': ''
    });
    // ===== tail 文件路径自动补全下拉 =====
    // 行为：
    //   - 输入 ≥1 字符时，若已有文件列表缓存则前端过滤，否则拉取 /api/logs/list/targets；
    //   - ↑↓ 选中、Enter 确认、Esc 收起；blur 延时收起（150ms）；mousedown preventDefault 防止 blur 抢跑；
    //   - 切换 target（server/dir）时清空缓存，下次输入重新拉。
    const tailFileWrap = el('div', { class: 'ws-tail-file-wrap', style: 'position:relative; flex:1;' });
    tailFileWrap.appendChild(tailFileInp);
    const tailFilePopover = el('div', {
      id: 'ws-tail-file-popover',
      class: 'ws-tail-file-popover',
      role: 'listbox',
      style: 'display:none; position:absolute; top:100%; left:0; right:0; z-index:50; background:var(--bg, #fff); border:1px solid var(--border, #ddd); border-top:none; max-height:260px; overflow-y:auto; box-shadow:0 4px 12px rgba(0,0,0,0.12);'
    });
    tailFileWrap.appendChild(tailFilePopover);
    let tailFileCache = null; // { key: 'server|dir', files: [{name, size, mod_time, full_path}] }
    let tailFileLoading = false;
    let tailFileHighlightIdx = -1;
    let tailFileBlurTimer = null;
    let tailFileFetchTimer = null;

    function getTailTargetKey() {
      return tailTargetSel.value || '';
    }

    function isTailFilePopoverVisible() {
      return tailFilePopover.style.display !== 'none';
    }

    function hideTailFilePopover() {
      tailFilePopover.style.display = 'none';
      tailFileInp.setAttribute('aria-expanded', 'false');
      tailFileHighlightIdx = -1;
      tailFileInp.setAttribute('aria-activedescendant', '');
      updateTailFileHighlight();
    }

    function showTailFilePopover() {
      tailFilePopover.style.display = '';
      tailFileInp.setAttribute('aria-expanded', 'true');
    }

    function updateTailFileHighlight() {
      const rows = tailFilePopover.querySelectorAll('.ws-tail-file-row');
      rows.forEach((r, i) => {
        if (i === tailFileHighlightIdx) {
          r.style.background = 'var(--hover, rgba(0,0,0,0.06))';
          r.setAttribute('aria-selected', 'true');
        } else {
          r.style.background = '';
          r.setAttribute('aria-selected', 'false');
        }
      });
      tailFileInp.setAttribute(
        'aria-activedescendant',
        tailFileHighlightIdx >= 0 ? 'ws-tail-file-option-' + tailFileHighlightIdx : ''
      );
      if (tailFileHighlightIdx >= 0 && rows[tailFileHighlightIdx]) {
        rows[tailFileHighlightIdx].scrollIntoView({ block: 'nearest' });
      }
    }

    function renderTailFileMatches(files, query) {
      tailFilePopover.innerHTML = '';
      tailFileHighlightIdx = -1;
      tailFileInp.setAttribute('aria-activedescendant', '');
      const q = (query || '').trim().toLowerCase();
      let matched = files;
      if (q) {
        matched = files.filter(f => (f.name || '').toLowerCase().indexOf(q) !== -1);
      }
      if (!matched.length) {
        if (tailFileLoading) {
          tailFilePopover.appendChild(el('div', {
            style: 'padding:8px 12px; color:var(--text-dim, #888); font-size:12.5px;',
            text: '加载中…'
          }));
        } else {
          tailFilePopover.appendChild(el('div', {
            style: 'padding:8px 12px; color:var(--text-dim, #888); font-size:12.5px;',
            text: q ? '无匹配文件' : '目录为空'
          }));
        }
        showTailFilePopover();
        return;
      }
      const maxShow = 20;
      matched.slice(0, maxShow).forEach((f, idx) => {
        const row = el('div', {
          class: 'ws-tail-file-row',
          id: 'ws-tail-file-option-' + idx,
          role: 'option',
          'aria-selected': 'false',
          'data-name': f.name,
          style: 'display:flex; justify-content:space-between; align-items:center; gap:10px; padding:6px 10px; cursor:pointer; border-bottom:1px solid var(--border, #eee);'
        }, [
          el('span', {
            style: 'font-family: var(--mono, monospace); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; flex:1;',
            text: f.name
          }),
          el('span', {
            class: 'text-dim',
            style: 'font-size:11.5px; white-space:nowrap;',
            text: formatBytes(f.size || 0)
          })
        ]);
        row.addEventListener('mousedown', (ev) => {
          ev.preventDefault();
          selectTailFile(f.name);
        });
        row.addEventListener('mouseenter', () => {
          tailFileHighlightIdx = idx;
          updateTailFileHighlight();
        });
        tailFilePopover.appendChild(row);
      });
      if (matched.length > maxShow) {
        tailFilePopover.appendChild(el('div', {
          style: 'padding:4px 10px; color:var(--text-dim, #aaa); font-size:11px; text-align:center;',
          text: '…还有 ' + (matched.length - maxShow) + ' 个，继续输入以缩小范围'
        }));
      }
      showTailFilePopover();
    }

    function selectTailFile(name) {
      tailFileInp.value = name;
      hideTailFilePopover();
      tailFileInp.focus();
    }

    function moveTailFileHighlight(delta) {
      const rows = tailFilePopover.querySelectorAll('.ws-tail-file-row');
      if (!rows.length) return;
      let next = tailFileHighlightIdx + delta;
      if (next < 0) next = rows.length - 1;
      if (next >= rows.length) next = 0;
      tailFileHighlightIdx = next;
      updateTailFileHighlight();
    }

    function confirmTailFileHighlight() {
      if (tailFileHighlightIdx < 0) return false;
      const rows = tailFilePopover.querySelectorAll('.ws-tail-file-row');
      const row = rows[tailFileHighlightIdx];
      if (row) {
        selectTailFile(row.getAttribute('data-name') || '');
      }
      return true;
    }

    async function fetchTailFileList() {
      const key = getTailTargetKey();
      if (!key) return;
      if (tailFileCache && tailFileCache.key === key) return;
      const [serverName, dirPath] = key.split('|');
      tailFileLoading = true;
      try {
        const r = await api('POST', '/api/logs/list/targets', {
          system: sysSel.value,
          targets: [{ server: serverName, dir: dirPath }],
          username: userInp.value,
          password: passInp.value
        });
        const results = Array.isArray(r) ? r : (r.servers || r.results || []);
        const grp = results.find(s => s.server === serverName && (!s.dir || s.dir === dirPath));
        const files = (grp && grp.files) || [];
        tailFileCache = { key: key, files: files };
        if (isTailFilePopoverVisible()) {
          renderTailFileMatches(files, tailFileInp.value);
        }
      } catch (e) {
        // 静默失败，用户可以继续手动输入或点击「📋 选文件」按钮
      } finally {
        tailFileLoading = false;
      }
    }

    function triggerTailFileComplete() {
      const val = tailFileInp.value;
      if (val.length < 1) {
        hideTailFilePopover();
        return;
      }
      const key = getTailTargetKey();
      if (!key) {
        hideTailFilePopover();
        return;
      }
      if (tailFileCache && tailFileCache.key === key) {
        renderTailFileMatches(tailFileCache.files, val);
      } else {
        renderTailFileMatches([], val);
        if (tailFileFetchTimer) clearTimeout(tailFileFetchTimer);
        tailFileFetchTimer = setTimeout(fetchTailFileList, 200);
      }
    }

    tailFileInp.addEventListener('input', triggerTailFileComplete);
    tailFileInp.addEventListener('focus', triggerTailFileComplete);
    tailFileInp.addEventListener('blur', () => {
      if (tailFileBlurTimer) clearTimeout(tailFileBlurTimer);
      tailFileBlurTimer = setTimeout(hideTailFilePopover, 150);
    });
    tailFileInp.addEventListener('keydown', (ev) => {
      if (!isTailFilePopoverVisible()) return;
      if (ev.key === 'ArrowDown') {
        ev.preventDefault();
        moveTailFileHighlight(1);
      } else if (ev.key === 'ArrowUp') {
        ev.preventDefault();
        moveTailFileHighlight(-1);
      } else if (ev.key === 'Enter') {
        if (confirmTailFileHighlight()) {
          ev.preventDefault();
        }
      } else if (ev.key === 'Escape') {
        ev.preventDefault();
        hideTailFilePopover();
      }
    });
    const tailLinesInp = el('input', { type: 'number', id: 'ws-tail-lines', placeholder: '起始行数', value: '100' });
    const tailMaxLinesInp = el('input', { type: 'number', id: 'ws-tail-max-lines', min: '100', max: '50000', step: '100', value: '1000' });
    const tailOut = el('div', { id: 'ws-tail-out', class: 'tail-out' });
    // ---- Tail 高亮面板（共享 UI 工厂）----
    // 持久化策略：onChange 写 PUT /api/preferences，
    //             同时同步更新 OTB.state.tailHighlights（独立 tail.html 也会读到）。
    const tailHighlight = OTB.core.tailHighlightPanel({
      initial: OTB.state.tailHighlights || [],
      onChange: async (list) => {
        // 1) 内存同步：websphere tail tab 立即用新规则渲染后续行
        OTB.state.tailHighlights = list;
        // 2) 落盘：放到 preferences.json 的 tail.highlights 字段
        //    注意：PUT 是覆盖整个 prefs map，要保留其它字段（暂时没有，但留个口子）
        try {
          // 先 GET 一次拿到现有 prefs，避免覆盖其它未来字段
          let cur = {};
          try { cur = await OTB.api.api('GET', '/api/preferences') || {}; } catch (e) { /* ignore */ }
          cur.tail = Object.assign({}, cur.tail || {}, { highlights: list });
          await OTB.api.api('PUT', '/api/preferences', cur);
        } catch (e) {
          toast('保存高亮规则失败：' + e.message, 'err');
        }
      }
    });
    const btnTailStart = el('button', { class: 'btn btn-primary', text: '开始跟踪', onclick: doTailStart });
    const btnTailStop = el('button', { class: 'btn', text: '停止', onclick: doTailStop, disabled: true });
    const btnTailNewTab = el('button', { class: 'btn', text: '↗ 新窗口打开', onclick: openTailInNewTab, title: '在新窗口中跟踪，避免本页卡死' });
    // 项 12 修复：实时跟踪的"选文件"按钮 —— 拉当前 server+dir 下的文件列表，
    // 让用户点选而不是手输文件路径（避免打错字找不到文件）。
    const btnTailPickFile = el('button', { class: 'btn btn-sm', text: '📋 选文件', onclick: openTailFilePicker, title: '列出当前服务器/目录下的文件，点选填入文件名' });
    // v0.6-Redesign：实时 tail 严格单 target。多 target 时让用户选 1 个；
    // 单 target 时也明确显示当前选的是哪个（避免"我以为在跟 server-a 实际在跟 server-b"）。
    const tailTargetSel = el('select', { id: 'ws-tail-target', 'aria-label': '实时跟踪的目标服务器和目录' });
    const tailTargetWarn = el('div', {
      class: 'ws-tail-warn',
      role: 'alert',
      'aria-live': 'polite',
      style: 'display:none;'
    });
    const tailTargetInfo = el('div', { class: 'text-dim', id: 'ws-tail-target-info', style: 'padding:6px 0; font-size:12px;' });
    // 跨调用记住 tail target（仅 session 内；renderWebsphere 重新执行时重置，
    // 切走 websphere 路由再回来 target 会被刷成第一个，这是有意的 ——
    // "用户改完 dir 勾选自动刷新 target"是更重要的行为）
    let lastTailTargetKey = '';
    function refreshTailTargetSel() {
      const targets = getSelectedTargets();
      const prev = tailTargetSel.value || lastTailTargetKey;
      tailTargetSel.innerHTML = '';
      if (!targets.length) {
        tailTargetSel.appendChild(el('option', { value: '', text: '（请先在「目标」里勾选服务器+目录）' }));
        tailTargetSel.disabled = true;
        tailTargetWarn.style.display = 'none';
        tailTargetInfo.textContent = '';
        tailFileCache = null;
        hideTailFilePopover();
        setTabBadge('tail', '');
        return;
      }
      tailTargetSel.disabled = false;
      targets.forEach(t => {
        const k = t.server + '|' + t.dir;
        const opt = el('option', { value: k, text: t.server + '  ·  ' + (t.dir.split('/').pop() || t.dir) });
        tailTargetSel.appendChild(opt);
      });
      if (targets.length > 1) {
        tailTargetWarn.style.display = '';
        tailTargetWarn.textContent = '⚠ 你已选 ' + targets.length + ' 个 targets，实时跟踪只支持单服务器单目录。请从中选 1 个。';
      } else {
        tailTargetWarn.style.display = 'none';
      }
      // 恢复上次选中的 target；没有则取第一个
      let newKey = '';
      if (prev && targets.find(t => (t.server + '|' + t.dir) === prev)) {
        tailTargetSel.value = prev;
        newKey = prev;
      } else {
        newKey = targets[0].server + '|' + targets[0].dir;
        tailTargetSel.value = newKey;
        lastTailTargetKey = newKey;
      }
      if (tailFileCache && tailFileCache.key !== newKey) {
        tailFileCache = null;
        hideTailFilePopover();
      }
      const [s, d] = tailTargetSel.value.split('|');
      tailTargetInfo.textContent = '当前跟踪：' + s + '  /  ' + d;
    }
    tailTargetSel.addEventListener('change', () => {
      lastTailTargetKey = tailTargetSel.value;
      const [s, d] = tailTargetSel.value.split('|');
      tailTargetInfo.textContent = '当前跟踪：' + s + '  /  ' + d;
      tailFileCache = null;
      hideTailFilePopover();
    });
    let tailEvtSrc = null;
    let tailId = null;

    async function doTailStart() {
      const file = (tailFileInp.value || '').trim();
      if (!file) { toast('请输入文件名', 'warn'); return; }
      // v0.6-Redesign：实时 tail 严格单 target，从 tailTargetSel 选。
      // 不再 fallback 到 targets[0] —— 让用户清楚知道自己在跟哪台机器。
      const sel = tailTargetSel.value;
      if (!sel) { toast('请先在「目标」里选 1 个服务器/目录', 'warn'); return; }
      const [serverName, dirPath] = sel.split('|');
      const lines = Math.max(0, Math.min(1000, Number(tailLinesInp.value) || 0));
      tailOut.textContent = '';
      pendingTailLines = [];
      tailTotalLines = 0;
      grepShownLines = 0;
      grepPattern = '';
      grepCompiled = null;
      grepClearedNoticeShown = false;
      grepIsRegex = false;
      grepInp.value = '';
      grepRegexBtn.style.background = '';
      grepRegexBtn.style.color = '';
      grepInp.style.borderColor = '';
      updateGrepStatus();
      tailFlushScheduled = false;
      setStatus('busy', '跟踪中…');
      try {
        const r = await api('POST', '/api/logs/tail/start', {
          system: sysSel.value, server: serverName, dir: dirPath,
          username: userInp.value, password: passInp.value,
          file: file, lines: lines,
        });
        tailId = r.id;
        btnTailStart.disabled = true;
        btnTailStop.disabled = false;
        appendTailLine({ kind: 'info', msg: '已开启 tail · 服务器=' + serverName + ' · 目录=' + dirPath + ' · id=' + tailId });
        setTabBadge('tail', '🟢');
        if (window.EventSource) {
          tailEvtSrc = new EventSource('/api/logs/tail/' + tailId + '/events');
          // v0.6-fix：v0.5 旧 bug 修复 —— onerror 不再只是 append，必须调 stopTailUI
          // 否则 mock_sshd tail 流立即结束 / 真实环境网络断开时，按钮会一起卡死。
          tailEvtSrc.onmessage = (ev) => {
            try { appendTailLine(JSON.parse(ev.data)); } catch (e) { appendTailLine({ kind: 'info', msg: ev.data }); }
          };
          tailEvtSrc.addEventListener('done', () => {
            appendTailLine({ kind: 'info', msg: 'SSE 通道关闭' });
            stopTailUI();
          });
          tailEvtSrc.onerror = () => {
            appendTailLine({ kind: 'error', msg: 'SSE 连接异常' });
            // v0.6-fix：触发 stopTailUI 避免按钮卡死；同时给用户明确提示
            stopTailUI();
            toast('tail 连接已断开', 'warn');
          };
        } else {
          appendTailLine({ kind: 'error', msg: '浏览器不支持 EventSource' });
        }
      } catch (e) {
        toast('启动 tail 失败：' + e.message, 'err');
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    function openTailInNewTab() {
      const file = (tailFileInp.value || '').trim();
      if (!file) { toast('请输入文件名', 'warn'); return; }
      const sel = tailTargetSel.value;
      if (!sel) { toast('请先在「目标」里选 1 个服务器/目录', 'warn'); return; }
      const [serverName, dirPath] = sel.split('|');
      openTailForFileInNewTab(serverName, dirPath, file);
    }

    // 项 12 修复：实时跟踪"选文件"按钮 —— 拉当前 server+dir 下的文件列表，
    // 弹 inline picker 让用户点选填入 tailFileInp。复用 /api/logs/list/targets
    // （跟 files tab 的 doList 用同一接口，认证/并发/超时都现成）。
    async function openTailFilePicker() {
      const sel = tailTargetSel.value;
      if (!sel) { toast('请先在「目标」里选 1 个服务器/目录', 'warn'); return; }
      const [serverName, dirPath] = sel.split('|');
      // 复用 doList 的核心：调 /api/logs/list/targets
      // 这里 server+dir 一组，复用同样的入参
      try {
        const r = await api('POST', '/api/logs/list/targets', {
          system: sysSel.value,
          targets: [{ server: serverName, dir: dirPath }],
          username: userInp.value,
          password: passInp.value
        });
        const results = Array.isArray(r) ? r : (r.servers || r.results || []);
        const grp = results.find(s => s.server === serverName && (!s.dir || s.dir === dirPath));
        const files = (grp && grp.files) || [];
        tailFileCache = { key: sel, files: files };
        if (!files.length) {
          toast('当前目录里没文件（先确认「开始跟踪」是否需要起 SSH + 列文件）', 'warn');
          return;
        }
        // 弹 inline picker：复用 preview modal 样式
        const old = document.getElementById('ws-tail-pick-modal');
        if (old) old.remove();
        const overlay = el('div', { id: 'ws-tail-pick-modal', class: 'preview-overlay' });
        const box = el('div', { class: 'preview-box', style: 'max-width: 520px;' });
        const head = el('div', { class: 'preview-head' });
        head.appendChild(el('strong', { text: '选文件 · ' + serverName + ' / ' + (dirPath.split('/').pop() || dirPath) }));
        const btnClose = el('button', { class: 'btn btn-sm', text: '关闭', onclick: () => overlay.remove() });
        head.appendChild(btnClose);
        box.appendChild(head);
        const list = el('div', { style: 'max-height: 60vh; overflow:auto;' });
        files.forEach(f => {
          const row = el('div', {
            class: 'srv-pick-item',
            style: 'padding: 6px 10px; border-bottom: 1px solid var(--line); cursor: pointer;',
            onclick: () => {
              tailFileInp.value = f.name;
              overlay.remove();
              toast('已选：' + f.name, 'ok');
            }
          }, [
            el('span', { text: f.name, style: 'flex: 1;' }),
            el('span', { class: 'text-dim', style: 'font-size: 11px;', text: formatBytes(f.size || 0) + ' · ' + (f.mod_time || '') })
          ]);
          list.appendChild(row);
        });
        box.appendChild(list);
        overlay.appendChild(box);
        overlay.addEventListener('click', (e) => { if (e.target === overlay) overlay.remove(); });
        document.body.appendChild(overlay);
      } catch (e) {
        toast('列文件失败：' + e.message, 'err');
      }
    }

    // v0.5 #14：点文件列表里的文件 → 直接开新窗口 tail（无需手输文件名）
    function openTailForFileInNewTab(serverName, dirPath, fileName) {
      const params = new URLSearchParams({
        system: sysSel.value,
        server: serverName,
        dir: dirPath,
        file: fileName,
        lines: String(Math.max(0, Math.min(1000, Number(tailLinesInp.value) || 0)))
      });
      // 项 9 修复：把当前已填的凭据存到 OTB._tailCred，tail 页面 opener 拿
      // 避免用户每次开新窗口都被要求再输一次密码。
      try {
        OTB._tailCred = OTB._tailCred || {};
        OTB._tailCred[sysSel.value + '::' + serverName] = {
          username: userInp.value,
          password: passInp.value
        };
      } catch (e) { /* ignore */ }
      window.open('/static/tail.html?' + params.toString(), '_blank');
    }

    // v0.5 #14：点文件列表里的文件 → 在本页 tail 区域跟踪
    // 把 server/dir/file 推到 tail 输入区，调 doTailStart
    async function startTailForFile(serverName, dirPath, fileName) {
      try {
        tailFileInp.value = fileName;
        const targets = getSelectedTargets();
        const wantDir = dirPath;
        const wantSrv = serverName;
        if (!targets.find(t => t.server === wantSrv && t.dir === wantDir)) {
          toast('请先在「目标选择」里勾选 ' + wantSrv + ' / ' + wantDir, 'warn');
          return;
        }
        const key = wantSrv + '|' + wantDir;
        lastTailTargetKey = key;
        refreshTailTargetSel();
        tailTargetSel.value = key;
        tailTargetInfo.textContent = '当前跟踪：' + wantSrv + '  /  ' + wantDir;
        switchTab('tail');
        await doTailStart();
      } catch (e) {
        toast('启动 tail 失败：' + e.message, 'err');
      }
    }

    async function doTailStop() {
      if (!tailId) return;
      try { await api('POST', '/api/logs/tail/' + tailId + '/stop'); }
      catch (e) { /* ignore */ }
      stopTailUI();
    }

    function stopTailUI() {
      if (tailEvtSrc) { tailEvtSrc.close(); tailEvtSrc = null; }
      tailId = null;
      btnTailStart.disabled = false;
      btnTailStop.disabled = true;
      setStatus('idle');
      setTabBadge('tail', '');
    }

    function appendTailLine(o) {
      // v0.5 修复：实时 tail 改用 buffer + requestAnimationFrame 批量 append。
      // 原版每行都 textContent += + 全文 split/slice/join，日志一快直接卡死浏览器。
      // 与独立 tab tail.js 同样的优化策略：缓冲 N 行，rAF 一次性 appendChild TextNode。
      if (o.kind === 'line') {
        pendingTailLines.push(o.line);
      } else if (o.kind === 'info') {
        pendingTailLines.push('⟦info⟧ ' + o.msg);
      } else if (o.kind === 'error') {
        pendingTailLines.push('⟦error⟧ ' + o.msg);
      } else if (o.kind === 'done') {
        pendingTailLines.push('⟦done⟧ ' + o.msg);
      } else {
        pendingTailLines.push(String(o));
      }
      scheduleFlushTail();
    }

    // ----- tail 缓冲 + rAF 批量刷新（v0.5 修复卡死） -----
    // P0-2：切到 OTB.core.tailViewer（环形 buffer + 行级 DOM 节点池）。
    // 旧版每次 flush 都用 textContent split/slice/join 重建整 <pre>，
    // 5000 行截断一次 O(N) 字符串拷贝 + 浏览器重解析整容器。
    // 新版只 removeChild 头节点 + append 新节点，O(1)/行。
    let pendingTailLines = [];
    let tailFlushScheduled = false;
    const tailViewer = OTB.core.tailViewer({
      container: tailOut,
      maxLines: 1000, // max-lines UI 改值时通过 setMaxLines 同步
      getHighlights: () => tailHighlight.enabled ? tailHighlight.list : []
    });
    // 监听 max-lines UI 变化（页面已有 input id="ws-tail-max-lines"）
    if (tailMaxLinesInp) {
      tailMaxLinesInp.addEventListener('change', () => {
        const v = Math.max(100, Math.min(50000, Number(tailMaxLinesInp.value) || 1000));
        tailViewer.setMaxLines(v);
      });
    }

    // ===== Tail 工具栏：暂停/清屏/复制 + Grep 过滤 =====
    let tailTotalLines = 0;
    let grepPattern = '';
    let grepIsRegex = false;
    let grepCompiled = null;
    let grepShownLines = 0;
    let grepClearedNoticeShown = false;

    const btnTailPause = el('button', { class: 'btn btn-sm', text: '⏸ 暂停', title: '暂停/继续接收新日志' });
    const btnTailClear = el('button', { class: 'btn btn-sm', text: '🗑 清屏', title: '清空当前显示' });
    const btnTailCopy = el('button', { class: 'btn btn-sm', text: '📋 复制全部', title: '复制缓冲区所有文本' });
    const btnTailCopyShown = el('button', { class: 'btn btn-sm', text: '📋 复制显示', title: '复制过滤后显示的行', style: 'display:none' });

    const grepInp = el('input', {
      type: 'text',
      placeholder: 'Grep 过滤... (/ 聚焦)',
      style: 'flex: 1; min-width: 150px;',
      autocomplete: 'off',
      spellcheck: 'false'
    });
    const grepRegexBtn = el('button', {
      class: 'btn btn-sm',
      text: '.*',
      title: '切换正则/纯文本模式',
      style: 'font-family: monospace; font-weight: bold;'
    });
    const grepClearBtn = el('button', { class: 'btn btn-sm', text: '✕', title: '清除过滤 (Esc)' });
    const grepStatus = el('span', { class: 'text-dim', style: 'font-size: 12px; min-width: 120px; text-align: right;', text: '' });

    function updateGrepStatus() {
      if (!grepPattern) {
        grepStatus.textContent = tailTotalLines ? (tailTotalLines + ' 行') : '';
        btnTailCopyShown.style.display = 'none';
      } else {
        grepStatus.textContent = '过滤: ' + grepShownLines + ' / ' + tailTotalLines;
        btnTailCopyShown.style.display = '';
      }
    }

    function clearGrep() {
      if (grepPattern) {
        grepPattern = '';
        grepCompiled = null;
        grepInp.value = '';
        grepRegexBtn.style.background = '';
        grepRegexBtn.style.color = '';
        if (!grepClearedNoticeShown && tailTotalLines > 0) {
          tailViewer.push('⟦info⟧ 过滤已清除，新内容将全部显示（之前被过滤的行不会恢复）', 'info');
          grepClearedNoticeShown = true;
        }
        grepShownLines = tailTotalLines;
        updateGrepStatus();
        grepInp.focus();
      }
    }

    function compileGrep() {
      const pat = grepInp.value;
      grepPattern = pat;
      grepClearedNoticeShown = false;
      if (!pat) {
        grepCompiled = null;
        grepShownLines = tailTotalLines;
        updateGrepStatus();
        return;
      }
      try {
        if (grepIsRegex) {
          grepCompiled = new RegExp(pat);
        } else {
          const escaped = pat.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
          grepCompiled = new RegExp(escaped);
        }
        grepInp.style.borderColor = '';
      } catch (e) {
        grepCompiled = null;
        grepInp.style.borderColor = '#ef4444';
      }
      updateGrepStatus();
    }

    grepInp.addEventListener('input', compileGrep);
    grepInp.addEventListener('keydown', (ev) => {
      if (ev.key === 'Escape') {
        ev.preventDefault();
        clearGrep();
      }
    });
    grepRegexBtn.addEventListener('click', () => {
      grepIsRegex = !grepIsRegex;
      if (grepIsRegex) {
        grepRegexBtn.style.background = 'var(--primary)';
        grepRegexBtn.style.color = '#fff';
      } else {
        grepRegexBtn.style.background = '';
        grepRegexBtn.style.color = '';
      }
      compileGrep();
    });
    grepClearBtn.addEventListener('click', clearGrep);

    btnTailPause.addEventListener('click', () => {
      const newPaused = !tailViewer.isPaused();
      tailViewer.setPaused(newPaused);
      btnTailPause.textContent = newPaused ? '▶ 继续' : '⏸ 暂停';
    });
    btnTailClear.addEventListener('click', () => {
      tailViewer.clear();
      pendingTailLines = [];
      tailTotalLines = 0;
      grepShownLines = 0;
      grepClearedNoticeShown = false;
      updateGrepStatus();
    });
    btnTailCopy.addEventListener('click', () => {
      const text = tailViewer.getText();
      OTB.core.copyToClipboard(text).then(
        () => toast('已复制 ' + tailViewer.lineCount() + ' 行', 'ok'),
        (e) => toast('复制失败: ' + e.message, 'err')
      );
    });
    btnTailCopyShown.addEventListener('click', () => {
      const lines = [];
      tailOut.querySelectorAll('.tail-line').forEach(n => {
        lines.push(n.textContent);
      });
      OTB.core.copyToClipboard(lines.join('\n')).then(
        () => toast('已复制显示的 ' + lines.length + ' 行', 'ok'),
        (e) => toast('复制失败: ' + e.message, 'err')
      );
    });

    document.addEventListener('keydown', (ev) => {
      if (ev.key === '/' && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
        const active = document.activeElement;
        if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA')) return;
        ev.preventDefault();
        grepInp.focus();
        grepInp.select();
      }
    });

    const tailToolbar = el('div', {
      class: 'tail-toolbar',
      style: 'display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin: 8px 0; padding: 6px 8px; background: var(--bg-2); border: 1px solid var(--line); border-radius: 6px;'
    }, [
      btnTailPause, btnTailClear, btnTailCopy, btnTailCopyShown,
      el('span', { style: 'width: 1px; height: 20px; background: var(--line); margin: 0 4px;' }),
      el('span', { class: 'text-dim', style: 'font-size: 12px;', text: '🔍' }),
      grepInp,
      grepRegexBtn,
      grepClearBtn,
      el('span', { style: 'flex: 1;' }),
      grepStatus
    ]);

    function scheduleFlushTail() {
      if (tailFlushScheduled) return;
      tailFlushScheduled = true;
      // 优先 rAF（16ms 一帧），没有 rAF 的环境用 setTimeout 50ms 兜底
      const raf = typeof requestAnimationFrame === 'function' ? requestAnimationFrame : (fn) => setTimeout(fn, 50);
      raf(flushTailBuffer);
    }
    function flushTailBuffer() {
      tailFlushScheduled = false;
      if (!pendingTailLines.length) return;
      const lines = pendingTailLines;
      pendingTailLines = [];
      // Grep 过滤：匹配的才 push 到 viewer
      let toPush;
      if (grepPattern && grepCompiled) {
        toPush = [];
        for (let i = 0; i < lines.length; i++) {
          const line = lines[i];
          tailTotalLines++;
          if (grepCompiled.test(line)) {
            toPush.push(line);
            grepShownLines++;
          }
        }
      } else {
        toPush = lines;
        tailTotalLines += lines.length;
        grepShownLines += lines.length;
      }
      if (toPush.length > 0) {
        tailViewer.pushBatch(toPush);
        tailViewer.scrollToBottomIfNear();
      }
      updateGrepStatus();
    }

    const tailCard = el('div', { class: 'card' }, [
      el('h3', { text: '实时跟踪（单文件 SSE 流）' }),
      el('div', { class: 'card-desc', text: '跟踪远程文件新增行（用 tail -F）。实时跟踪本质只支持单服务器单目录 —— 选错机器就看不到你要的日志。日志量大卡顿时点「↗ 新窗口打开」切到独立窗口。' }),
      tailTargetWarn,
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [
          el('label', { text: '目标（服务器 / 目录）' }),
          tailTargetSel,
          tailTargetInfo
        ]),
        // grid-2 第二列空 div 撑位（让 label 与 select 对齐到左半边）
        el('div')
      ]),
      el('div', { class: 'grid-3 mt-2' }, [
        el('div', null, [
          el('label', { text: '文件名（相对日志目录）' }),
          el('div', { style: 'display:flex; gap:6px;' }, [tailFileWrap, btnTailPickFile])
        ]),
        el('div', null, [el('label', { text: '起始行数（0=只追新增）' }), tailLinesInp]),
        // 项 11 修复：可配"最多保留 N 行"上限（默认 1000）
        el('div', null, [
          el('label', { text: '最多保留行数（超过自动截断）' }),
          tailMaxLinesInp
        ])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnTailStart, btnTailStop, btnTailNewTab]),
      tailHighlight.root,
      tailToolbar,
      el('div', { class: 'mt-2' }, tailOut)
    ]);

    // v0.5 #13：页面顶部加说明卡（让用户知道页面分区和流程）
    // 项 6 修复：去掉 <strong> 里的 1./2./3./4. 前缀 —— <ol> 会自动生成序号，
    // 留着会变成 "1. 1. 选目标" 这种重复编号的丑陋显示。
    // 层级关系说明卡：业务系统 → 1 台或多台服务器 → 每台服务器下多个日志目录
    // 之前叫"4 步走"，但和 sticky tabBar 重叠（DOM 在前，被 sticky 浮在上面覆盖），
    // 而且层级关系没强调，新人看不懂"为什么有 业务系统 / 服务器 / 目录 三层"。
    // 改为：紧凑一行 + 4 步列表 + DOM 移到 tabBar 之后，避免被 sticky 覆盖。
    const introCard = el('div', { class: 'card ws-intro-card', style: 'background: var(--bg-2); border-left: 4px solid var(--primary); padding: 14px 18px; margin-top: 68px;' }, [
      el('div', { style: 'font-size: 13px; line-height: 1.65; color: var(--text); margin-bottom: 8px;' }, [
        el('strong', { text: '层级关系：' }),
        document.createTextNode('一个业务系统 '),
        el('span', { style: 'color: var(--primary); font-weight: 600;' }, '→'),
        document.createTextNode(' 多台服务器 '),
        el('span', { style: 'color: var(--primary); font-weight: 600;' }, '→'),
        document.createTextNode(' 每台服务器下多个日志目录。'),
        el('br'),
        el('span', { class: 'text-dim', text: '多选服务器 + 多选目录 = 一次操作多个目标（搜索/下载/Tail 全部并行）。' }),
      ]),
      el('ol', { style: 'margin: 4px 0 0 0; padding-left: 22px; font-size: 12.5px; line-height: 1.7; color: var(--text-dim);' }, [
        el('li', null, [
          el('strong', { style: 'color: var(--text);', text: '选目标 ' }),
          el('span', { text: '· 业务系统 → 服务器（多选）→ 日志目录（每个服务器下面多选）' })
        ]),
        el('li', null, [
          el('strong', { style: 'color: var(--text);', text: '列文件 / 下载 ' }),
          el('span', { text: '· 一次性把勾选 targets 下的文件全列出来，多选下载' })
        ]),
        el('li', null, [
          el('strong', { style: 'color: var(--text);', text: '搜索 ' }),
          el('span', { text: '· 在勾选 targets 里搜索关键词，支持最近 N 个 / 指定文件 / glob' })
        ]),
        el('li', null, [
          el('strong', { style: 'color: var(--text);', text: '实时 Tail ' }),
          el('span', { text: '· 从文件列表点 ↗ Tail 新窗口跟踪（避免本页卡死）' })
        ])
      ])
    ]);
    // v0.5-G #13：4 个 tab 快捷跳转按钮（点 → 真 tab 切换）
    // P2-14：真 tab 切换（show/hide 内容区），而不是 scrollIntoView
    // v0.6-Redesign：tab 收编为 3 个（文件/下载 / 搜索排障 / 实时跟踪），
    // 「目标选择」不再是 tab —— 它是所有功能的前置条件，顶部常驻。
    const tabBar = el('div', { class: 'ws-tab-bar', style: 'display:flex; gap:6px; margin-bottom: 12px; flex-wrap:wrap; align-items:center;' });
    let activeTab = 'files'; // 默认显示文件/下载（v0.6：'files' 替 'target'）

    // P2-14：把所有功能区包进 ws-tab-content div
    const tabContents = {};
    function makeTabContent(id, innerEl) {
      const wrap = el('div', { id: 'ws-tab-' + id, class: 'ws-tab-content' + (id === activeTab ? ' active' : '') });
      wrap.appendChild(innerEl);
      tabContents[id] = wrap;
      return wrap;
    }

    const tabBtns = {};
    const tabBadges = {};
    // v0.6 修复：原来用 makeTab 的 hash 形参在 switchTab 里访问导致 hash is not defined
    // —— switchTab 改用 tabHash map 显式查表。
    const tabHash = {
      files: 'files',
      search: 'search',
      tail: 'tail'
    };
    function makeTab(label, tabId) {
      const tabItem = el('span', { class: 'ws-tab-item', 'data-tab-id': tabId, style: 'display:inline-flex; align-items:center; gap:6px;' });
      const btn = el('button', {
        class: 'btn' + (tabId === activeTab ? ' active' : ''),
        text: label,
        onclick: () => switchTab(tabId)
      });
      const badge = el('span', { class: 'ws-tab-badge', 'data-tab-badge': tabId, text: '', 'aria-label': tabId + ' 状态' });
      tabItem.appendChild(btn);
      tabItem.appendChild(badge);
      tabBtns[tabId] = btn;
      tabBadges[tabId] = badge;
      return tabItem;
    }

    function setTabBadge(tabId, text) {
      const b = tabBadges[tabId];
      if (!b) return;
      // v0.6-fix：必须显式设 inline-block —— '' 会被 CSS 默认 .ws-tab-badge{display:none} 覆盖，
      // 导致 badge 永远看不见。空值清空时设 'none'。
      b.textContent = text == null ? '' : String(text);
      b.style.display = text ? 'inline-block' : 'none';
    }

    function switchTab(tabId) {
      if (!tabBtns[tabId]) return; // 防御：未知 tab 直接忽略
      activeTab = tabId;
      Object.keys(tabBtns).forEach(k => { tabBtns[k].classList.toggle('active', k === tabId); });
      Object.keys(tabContents).forEach(k => { tabContents[k].classList.toggle('active', k === tabId); });
      try { history.replaceState(null, '', '#/websphere?tab=' + (tabHash[tabId] || tabId)); } catch (e) { /* ignore */ }
      // 项 8 修复：用户从「搜索排障」点"列出文件"按钮过来时，自动 doList 一次
      if (tabId === 'files' && window.__otbSwitchTabAndList) {
        window.__otbSwitchTabAndList = false;
        // doList 依赖 getSelectedTargets 拿 targets；同时依赖 cfg 已加载
        if (cfg && sysSel.value && getSelectedTargets().length > 0) {
          // 异步触发，不阻塞 switchTab
          setTimeout(() => { doList().catch(() => { /* toast 已在 doList 内部 */ }); }, 50);
        } else {
          toast('请先在「目标」里勾选服务器 + 目录', 'warn');
        }
      }
    }

    tabBar.appendChild(makeTab('📁 文件 / 下载', 'files'));
    tabBar.appendChild(makeTab('🔍 搜索排障', 'search'));
    tabBar.appendChild(makeTab('📺 实时跟踪', 'tail'));

    formCard.id = 'ws-target-card';
    searchCard.id = 'ws-search-card';
    tailCard.id = 'ws-tail-card';
    fileTableWrap.id = 'ws-files-card';
    hitTableWrap.id = 'ws-hits-card';
    ctxCard.id = 'ws-context-card';

    // v0.7-Redesign（第三波）：files tab 顶部加 toolbar ——
    // [📋 列出文件] [📥 下载最新 N + zip + 目录] 两个按钮原属 formCard 底部，
    // 按归属改到 files tab 顶部（这是 files tab 专属功能，理应在它自己的 toolbar 里）。
    // 项 13 修复：之前把所有按钮/label/input 拍一行，label 跟按钮混在一起，dlTargetDirInp 还
    // 有"  目录："前缀空格（用空格硬挤很丑）。改成两组明确分组：
    //   第一组（左侧）：列出文件
    //   第二组（左侧后）：下载最新 [N] [zip] [下载]
    //   第三组（右侧）：本地目录 [input]
    const filesToolbar = el('div', { class: 'files-toolbar', id: 'ws-files-toolbar' }, [
      el('div', { class: 'files-toolbar-row' }, [
        btnList,
        el('span', { class: 'lbl', text: '下载最新：' }),
        dlNSel,
        dlZipLabel,
        btnDownload,
        el('div', { style: 'flex: 1;' }),
        el('label', { class: 'inline', style: 'gap: 6px;' }, [
          el('span', { class: 'lbl', text: '本地目录：' }),
          dlTargetDirInp
        ])
      ])
    ]);
    const filesWrap = el('div', { class: 'files-wrap' }, [filesToolbar, fileTableWrap]);
    // P2-14：目标选择永远显示；功能区包进 tab content
    // DOM 顺序：tabBar 必须先渲染、introCard 在它之后 —— 否则 sticky tabBar 会盖住 introCard 上半部分
    // （sticky 元素离开原位置后，原来的位置由下方元素填充；DOM 顺序在前的内容会被 sticky 浮在上面覆盖）
    view.appendChild(tabBar);
    view.appendChild(introCard);
    view.appendChild(formCard);
    view.appendChild(makeTabContent('files', filesWrap));
    view.appendChild(makeTabContent('search', searchCard));
    view.appendChild(makeTabContent('tail', tailCard));
    // 搜索结果和上下文嵌入 search tab 内（不再独立 tab）
    searchCard.appendChild(hitTableWrap);
    searchCard.appendChild(ctxCard);
    // v0.7（第二波）：应用折叠状态 + 首次渲染摘要
    applyTargetPanelState();
    renderTargetSummary();

    try {
      const params = new URLSearchParams(location.hash.split('?')[1] || '');
      const tab = params.get('tab');
      if (tab && tabBtns[tab]) { switchTab(tab); }
    } catch (e) { /* ignore */ }

    api('GET', '/api/config').then(info => {
      cfg = info;
      sysSel.innerHTML = '';
      info.systems.forEach(s => sysSel.appendChild(el('option', { value: s.name, text: s.name })));
      // 恢复上次选择
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      if (lastSel && lastSel.system && info.systems.find(s => s.name === lastSel.system)) {
        sysSel.value = lastSel.system;
        if (lastSel.username) userInp.value = lastSel.username;
      }
      renderSrvPick();
      refreshDirs();
      if (lastSel && lastSel.dir) dirSel.value = lastSel.dir;
      refreshCredStatus();
      if (info.search && typeof info.search.default_context_lines === 'number') {
        contextInp.value = String(Math.max(0, Math.min(50, info.search.default_context_lines)));
      }
      // 默认勾上"记住密码"（keyring 模式下；file/disabled 时由 refreshCredStatus 强制取消）
      rememberChk.checked = true;
      rememberChk.disabled = false;
      refreshCredStatus();
      // v0.6：首次加载后初始化 tail tab 的 target select（多 target 时显示警告）
      try { refreshTailTargetSel(); } catch (e) { /* ignore */ }
      // v0.7（第二波）：config 加载完后刷新一次目标摘要（之前 renderTargetSummary
      // 是在 sysSel.value 仍是 '' 时调的，现在 systems 列表 + 上次选择都已恢复，
      // 摘要才有准确内容）
      try { renderTargetSummary(); } catch (e) { /* ignore */ }
    }).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  OTB.pages.websphere = renderWebsphere;
  OTB.state.routes.websphere = renderWebsphere;
  OTB.state.routeNames.websphere = 'WebSphere 日志助手';
})();
