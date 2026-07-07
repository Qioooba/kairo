/* ===== web/tail.js =====
 * 独立窗口版的实时 tail —— 从 query string 拿 system/server/dir/file/lines，
 * 自动启动 SSE，浏览器无 tab 卡顿影响。挂 window.tailStandalone。
 */
(function () {
  'use strict';
  const $ = (s) => document.querySelector(s);

  function toast(msg, type) {
    const t = $('#toast');
    t.textContent = msg;
    t.className = 'toast show' + (type ? ' ' + type : '');
    clearTimeout(toast._timer);
    toast._timer = setTimeout(() => { t.className = 'toast'; }, 2400);
  }

  function setConn(state, text) {
    const dot = $('#conn-dot');
    const txt = $('#conn-text');
    dot.className = 'dot dot-' + (state || 'idle');
    txt.textContent = text || (state === 'busy' ? '跟踪中…' : state === 'ok' ? '已连接' : state === 'err' ? '断开' : '就绪');
  }

  const params = new URLSearchParams(location.search);
  const system = params.get('system') || '';
  const server = params.get('server') || '';
  const dir = params.get('dir') || '';
  const file = params.get('file') || '';
  const lines = Math.max(0, Math.min(1000, Number(params.get('lines')) || 100));

  $('#m-system').textContent = system || '?';
  $('#m-server').textContent = server || '?';
  $('#m-dir').textContent = dir || '?';
  $('#m-file').textContent = file || '?';

  // 读取 opener 临时传来的当前凭据（用户在主页刚填的，最准）。
  // 不再从 localStorage 读取旧版明文密码；记住密码统一走后端系统钥匙串。
  function getOpenerCred() {
    try {
      const op = window.opener;
      if (op && op.Kairo && op.Kairo._tailCred && op.Kairo._tailCred[system + '::' + server]) {
        const c = op.Kairo._tailCred[system + '::' + server];
        if (c && c.password) return { username: c.username || '', password: c.password };
      }
    } catch (e) { /* ignore (跨源 opener 会抛) */ }
    return null;
  }

  const loginForm = $('#tail-login');
  const userInp = $('#tail-user');
  const passInp = $('#tail-pass');
  let pendingCredResolve = null;
  function showLogin(resolve) {
    if (!loginForm) return;
    pendingCredResolve = typeof resolve === 'function' ? resolve : null;
    loginForm.style.display = 'flex';
    setConn('idle', '等待凭据');
    setTimeout(() => userInp && userInp.focus(), 0);
  }
  function hideLogin() {
    if (loginForm) loginForm.style.display = 'none';
  }

  // keyring 没有命中时，用页面内表单兜底，避免 prompt 被嵌入式浏览器拦截。
  function askCred() {
    return new Promise((resolve) => showLogin(resolve));
  }

  const tailOut = $('#tail-out');
  const btnPause = $('#btn-pause');
  const btnClear = $('#btn-clear');
  const btnStop = $('#btn-stop');
  const btnClose = $('#btn-close');
  const showLineNumbersInp = $('#show-line-numbers');

  let evtSrc = null;
  let tailId = null;
  let pendingLines = [];
  let flushTimer = null;
  let lastRateAt = Date.now();
  let lastRateCount = 0;
  let rateEl = $('#rate');
  // 项 11 修复：行数上限从 UI 读，可配（默认 1000）。限制至少 100，最多 50000
  // —— 日志大场景下避免页面卡顿/内存爆。
  const maxLinesInp = $('#max-lines');
  function getMaxLines() {
    return Math.max(100, Math.min(50000, Number(maxLinesInp && maxLinesInp.value) || 1000));
  }

  // ---- Tail 高亮面板（独立窗口版） ----
  // 与 websphere.js 共享 Kairo.core.tailHighlightPanel 工厂。
  // 持久化策略：
  //   1) 先尝试从 opener 的 Kairo.state.tailHighlights 拿（项 9 修复：用户在主页刚设过）
  //   2) 兜底：本地 fetch GET /api/preferences 读 tail.highlights
  //   3) 都没有：空列表
  // onChange：PUT /api/preferences + 回写 opener 的 state（同进程多窗口同步）
  let highlightPanel = null;
  async function initHighlightPanel() {
    let initial = [];
    try {
      if (window.opener && window.opener.Kairo && Array.isArray(window.opener.Kairo.state && window.opener.Kairo.state.tailHighlights)) {
        initial = window.opener.Kairo.state.tailHighlights;
      }
    } catch (e) { /* ignore (跨源 opener 会抛) */ }
    if (!initial.length) {
      try {
        const prefs = await fetch('/api/preferences').then(r => r.ok ? r.json() : null);
        if (prefs && prefs.tail && Array.isArray(prefs.tail.highlights)) initial = prefs.tail.highlights;
      } catch (e) { /* ignore */ }
    }
    highlightPanel = Kairo.core.tailHighlightPanel({
      initial,
      onChange: async (list) => {
        try {
          if (window.opener && window.opener.Kairo) {
            window.opener.Kairo.state = window.opener.Kairo.state || {};
            window.opener.Kairo.state.tailHighlights = list;
          }
        } catch (e) { /* ignore */ }
        try {
          let cur = {};
          try { cur = await fetch('/api/preferences').then(r => r.ok ? r.json() : null) || {}; } catch (e) { /* ignore */ }
          cur.tail = Object.assign({}, cur.tail || {}, { highlights: list });
          await fetch('/api/preferences', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(cur)
          });
        } catch (e) {
          toast('保存高亮规则失败：' + e.message, 'err');
        }
      }
    });
    const wrap = document.getElementById('tail-highlight');
    if (wrap) wrap.appendChild(highlightPanel.root);
  }
  initHighlightPanel();

  // P0-2：TailViewer（环形 buffer + 行级 DOM 节点池）
  // viewer.pushBatch(arr) 内部会按当前 highlights 渲染每一行
  // —— 注意：viewer 创建时 highlightPanel 还没就绪（异步 initHighlightPanel 之后），
  // 但 getHighlights 用闭包读 highlightPanel 的 .enabled/.list，每次 push 重新读，所以没问题。
  const viewer = Kairo.core.tailViewer({
    container: tailOut,
    maxLines: getMaxLines(),
    getHighlights: () => (highlightPanel && highlightPanel.enabled) ? highlightPanel.list : []
  });

  // v0.6：行号显示开关。持久化到 localStorage（key 兼容 v0.6 前后页面刷新记忆），
  // 默认开启。
  //
  // v0.10 真实行号：把 (baselineOffset - viewer.droppedCount()) 写到 --tail-line-offset，
  // CSS counter 会让 buffer 第一行的真实文件行号 = offset + 1，trim 后依然连续。
  //   - baselineOffset = 后端 /start 响应带回来的真实文件行号 - 历史行数
  //     （即"启动前文件总行数"减去"要先吐的最后 N 行的起点"）。
  //   - 每次 viewer trim 时 droppedCount 上涨，offset 自动跟着减，第一行号不变。
  //   - baselineOffset < 0（baseline = -1 兜底 / baseline < lines）走 fallback：行号
  //     从 1 开始，"buffer 内序号"模式（旧行为，UI 仍可见但语义不真实）。
  //
  // 推导：
  //   设 baseline = 启动瞬间文件总行数；historyLines = 启动时先吐的 N（= lines 参数）。
  //   历史起点 = max(1, baseline - historyLines + 1)
  //   baselineOffset = history 起点 - 1 = max(0, baseline - historyLines)
  //   buffer 第一行行号 = (baselineOffset - droppedCount) + 1
  //                  = max(0, baseline - historyLines) - droppedCount + 1
  //   历史模式（droppedCount=0）：max(0, baseline - historyLines) + 1 ✓
  const LINE_NUM_KEY = 'kairo_tail_show_line_numbers';
  let showLineNumbers = (function () {
    try {
      const raw = localStorage.getItem(LINE_NUM_KEY);
      if (raw === null || raw === '') return true; // 默认开
      return raw === '1' || raw === 'true';
    } catch (e) { return true; }
  })();
  // baselineOffset 由 startWithCred 成功后在拿到 total_lines 后设置；
  // -1 = "baseline 拿不到"，走 fallback（viewer droppedCount 即可）。
  let baselineOffset = -1;
  function computeCurrentOffset() {
    // baseline 拿不到 → fallback 到 "viewer droppedCount"（旧行为，行号从 1 开始）。
    if (baselineOffset < 0) return viewer.droppedCount();
    return baselineOffset - viewer.droppedCount();
  }
  function applyLineNumberVisibility() {
    if (showLineNumbers) tailOut.classList.add('show-line-numbers');
    else tailOut.classList.remove('show-line-numbers');
    syncLineNumberOffset();
  }
  function syncLineNumberOffset() {
    if (!showLineNumbers) return;
    // buffer 第一行的"全局行号 - 1" = computeCurrentOffset()；
    // CSS counter `counter-reset: tail-line <N>` 让每行 ::before 渲染 N+1, N+2, ...
    tailOut.style.setProperty('--tail-line-offset', String(computeCurrentOffset()));
  }
  if (showLineNumbersInp) {
    showLineNumbersInp.checked = showLineNumbers;
    showLineNumbersInp.addEventListener('change', () => {
      showLineNumbers = !!showLineNumbersInp.checked;
      try { localStorage.setItem(LINE_NUM_KEY, showLineNumbers ? '1' : '0'); } catch (e) { /* ignore */ }
      applyLineNumberVisibility();
    });
  }
  // 初始化：保证 viewer 创建完就同步一次（首屏可能已有容器初始文本）
  applyLineNumberVisibility();

  // 批量 flush：100ms / 100 行
  function scheduleFlush() {
    if (flushTimer) return;
    flushTimer = setTimeout(flushTail, 100);
  }
  function flushTail() {
    flushTimer = null;
    if (viewer.isPaused() || pendingLines.length === 0) return;
    const wasNearBottom = (tailOut.scrollHeight - tailOut.clientHeight - tailOut.scrollTop) < 120;
    const lines = pendingLines;
    pendingLines = [];
    viewer.pushBatch(lines);
    if (wasNearBottom) {
      requestAnimationFrame(() => {
        tailOut.scrollTop = tailOut.scrollHeight;
      });
    }
    syncLineNumberOffset();
    updateMeters();
  }
  // FE-005：保存 interval id，停止 / 关闭窗口时 clearInterval，避免路由切换或窗口关闭后 interval 持续运行。
  const flushInterval = setInterval(flushTail, 100);

  function updateMeters() {
    const total = viewer.totalEver();
    const buf = viewer.lineCount();
    $('#m-lines').textContent = buf + ' / ' + total + ' 行';
    const now = Date.now();
    if (now - lastRateAt >= 2000) {
      const dt = (now - lastRateAt) / 1000;
      const dn = total - lastRateCount;
      const rate = dn / dt;
      rateEl.textContent = viewer.isPaused()
        ? ('已暂停（已显示 ' + buf + ' 行）')
        : ('实时 · ' + rate.toFixed(1) + ' 行/秒');
      lastRateAt = now;
      lastRateCount = total;
    }
  }
  const meterInterval = setInterval(updateMeters, 1000); // 每秒刷一次 meter 兜底（rate 计算兜底）

  btnPause.addEventListener('click', () => {
    const newPaused = !viewer.isPaused();
    viewer.setPaused(newPaused);
    btnPause.textContent = newPaused ? '继续' : '暂停';
    if (newPaused) {
      rateEl.textContent = '已暂停（缓冲 ' + pendingLines.length + ' 行）';
    } else {
      rateEl.textContent = '实时显示中';
      flushTail();
    }
    if (evtSrc) btnStop.disabled = false;
  });
  btnClear.addEventListener('click', () => {
    viewer.clear();
    pendingLines = [];
    // v0.10 真实行号：clear 后 baselineOffset 走 fallback（-1），
    // 即从"buffer 内序号 1"重新计数 —— 因为 baseline 是启动瞬间拿的，
    // 文件继续写入时已经过期；想重新对齐文件真实行号，需要重新 tail（重新拉 baseline）。
    baselineOffset = -1;
    rateEl.textContent = '实时显示中';
    toast('已清屏（行号重置为 buffer 内序号，如需真实文件行号请重新打开跟踪）', 'info');
    syncLineNumberOffset();
  });
  // 项 11 修复：max-lines 改动时立即 trim 到新上限
  if (maxLinesInp) {
    maxLinesInp.addEventListener('change', () => {
      viewer.setMaxLines(getMaxLines());
      syncLineNumberOffset(); // 缩小可能触发 dropped，需要重写 offset
    });
  }
  btnStop.addEventListener('click', async () => {
    if (tailId) {
      try { await fetch('/api/logs/tail/' + tailId + '/stop', { method: 'POST' }); } catch (e) {}
    }
    if (evtSrc) { evtSrc.close(); evtSrc = null; }
    clearInterval(flushInterval);
    clearInterval(meterInterval);
    setConn('idle', '已停止');
    btnStop.disabled = true;
  });
  btnClose.addEventListener('click', () => { window.close(); });
  window.addEventListener('beforeunload', () => {
    clearInterval(flushInterval);
    clearInterval(meterInterval);
    if (tailId) {
      try { navigator.sendBeacon('/api/logs/tail/' + tailId + '/stop'); } catch (e) {}
    }
  });

  async function start() {
    if (!system || !server || !dir || !file) {
      toast('参数缺失：system / server / dir / file', 'err');
      setConn('err', '参数缺失');
      return;
    }
    let cred = getOpenerCred();
    // 第一次尝试：优先用 opener 传来的凭据；如果没有，先用空凭据让后端从 keyring 取
    // 只有后端明确返回"缺少密码"时，才弹内联表单让用户输入
    await startWithCred(cred || { username: '', password: '' }, true);
  }

  async function startWithCred(cred, allowAsk) {
    hideLogin();
    setConn('busy', '启动中…');
    try {
      const r = await fetch('/api/logs/tail/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          system: system, server: server, dir: dir,
          username: cred.username, password: cred.password,
          file: file, lines: lines
        })
      });
      const data = await r.json();
      if (!r.ok) {
        const errMsg = (data && data.error) || ('HTTP ' + r.status);
        // 缺少密码 → 展示页面内凭据表单（如果允许）
        if (allowAsk && /缺少密码|密码|凭据/.test(errMsg)) {
          const newCred = await askCred();
          if (newCred) {
            await startWithCred(newCred, false);
            return;
          }
        }
        throw new Error(errMsg);
      }
      tailId = data.id;
      // v0.10 真实行号：拿后端的 total_lines 算 baselineOffset
      //   baselineOffset = max(0, baseline - lines)
      //   -1 → fallback（行号从 1 起的旧行为）
      const tlines = Number(data.total_lines);
      if (Number.isFinite(tlines) && tlines >= 0) {
        baselineOffset = Math.max(0, tlines - lines);
      } else {
        baselineOffset = -1;
      }
      setConn('ok', '已连接 · id=' + tailId);
      btnStop.disabled = false;
      viewer.clear();
      pendingLines = [];
      syncLineNumberOffset(); // 重连后 droppedCount 归零，offset 立刻同步（避免出现第一行号 = 旧值 + 1 的瞬间）
      appendInfo('已开启 tail · id=' + tailId + ' · 起始 ' + lines + ' 行');
      evtSrc = new EventSource('/api/logs/tail/' + tailId + '/events');
      evtSrc.onmessage = (ev) => {
        try {
          const o = JSON.parse(ev.data);
          if (o.kind === 'line') pendingLines.push(o.line);
          else if (o.kind === 'info') appendInfo(o.msg);
          else if (o.kind === 'error') appendError(o.msg);
          else if (o.kind === 'done') { appendInfo('⟦done⟧ ' + (o.msg || '')); }
          scheduleFlush();
        } catch (e) { /* ignore */ }
      };
      evtSrc.addEventListener('done', () => { setConn('idle', '已断开'); });
      evtSrc.onerror = () => {
        // 浏览器会自动重连，给提示但保留窗口
        setConn('busy', '重连中…');
      };
    } catch (e) {
      toast('启动失败：' + e.message, 'err');
      setConn('err', '失败');
    }
  }

  // P0-2：info/error 走 viewer.push 而不是直接操作 tailOut —— viewer 管 DOM
  function appendInfo(msg) {
    viewer.push('⟦info⟧ ' + msg, 'info');
    syncLineNumberOffset(); // 单行 push 也可能 trim，同步 offset
    requestAnimationFrame(() => { tailOut.scrollTop = tailOut.scrollHeight; });
  }
  function appendError(msg) {
    viewer.push('⟦error⟧ ' + msg, 'error');
    syncLineNumberOffset();
    requestAnimationFrame(() => { tailOut.scrollTop = tailOut.scrollHeight; });
  }

  if (loginForm) {
    loginForm.addEventListener('submit', (ev) => {
      ev.preventDefault();
      const cred = {
        username: userInp ? userInp.value : '',
        password: passInp ? passInp.value : ''
      };
      if (!cred.username || !cred.password) {
        toast('请输入 SSH 用户名和密码', 'warn');
        return;
      }
      hideLogin();
      if (pendingCredResolve) {
        const resolve = pendingCredResolve;
        pendingCredResolve = null;
        resolve(cred);
      } else {
        startWithCred(cred, false);
      }
    });
  }

  // ===== 页面内搜索 / 高亮（公共模块） =====
  const searchHl = Kairo.createSearchHighlighter({
    container: tailOut,
    input: document.getElementById('search-input'),
    countEl: document.getElementById('search-count'),
    prevBtn: document.getElementById('search-prev'),
    nextBtn: document.getElementById('search-next'),
    clearBtn: document.getElementById('search-clear'),
    skipEmptyText: true
  });

  // 监听 tail 容器变化（新行追加/清屏），自动对新增行应用搜索高亮
  // 用 rAF 防抖：批量 flush 期间多次 mutation 合并为一次 refresh
  //
  // ★ 修复：search-hl 的 refresh() 内部会清空 + 重建 mark（替换文本节点），
  // 这本身就是一次 childList mutation —— 不加保护的话 observer 会无限递归：
  //   applyHighlight → mutation → observer → rAF → refresh → applyHighlight → ...
  //   每秒约 60 次循环，且每次都把 searchActiveIdx 强制重置为 0，
  //   导致用户点 ↑/↓ 时索引立刻被刷新覆盖、永远停在第 1 个匹配。
  //
  // MutationObserver 的回调是 microtask 投递的，会在当前同步代码结束后才执行。
  // 所以 `_isRefreshing = false` 放在 finally 里仍然会被「自己 refresh 触发的 mutation」
  // 看到的 _isRefreshing=false。正确做法是在 refresh 期间直接 disconnect observer，
  // refresh 结束后重新 observe —— 这期间产生的 mutation 会被丢弃，但 search-hl 自己
  // 产生的 mutation 本来就不需要再 refresh 一次。
  let tailObserverPending = false;
  let _isRefreshing = false;
  const tailObserverConfig = { childList: true, subtree: true };
  const tailObserver = new MutationObserver(function() {
    if (_isRefreshing) return;
    if (!searchHl.term) return;
    if (tailOut.children.length === 0) {
      searchHl.clear();
      return;
    }
    if (tailObserverPending) return;
    tailObserverPending = true;
    requestAnimationFrame(function() {
      tailObserverPending = false;
      tailObserver.disconnect();
      _isRefreshing = true;
      try {
        searchHl.refresh();
      } finally {
        _isRefreshing = false;
        tailObserver.observe(tailOut, tailObserverConfig);
      }
    });
  });
  tailObserver.observe(tailOut, tailObserverConfig);

  start();
})();
