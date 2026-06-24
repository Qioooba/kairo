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

  // 读取本地保存的密码（如果有），让用户不用再输入
  // 优先级：opener 的 OTB._tailCred（项 9 修复）> localStorage > 弹窗 prompt
  function getStoredCred() {
    // 1) 项 9：opener 实时传过来的当前凭据（用户在主页刚填的，最准）
    try {
      const op = window.opener;
      if (op && op.OTB && op.OTB._tailCred && op.OTB._tailCred[system + '::' + server]) {
        const c = op.OTB._tailCred[system + '::' + server];
        if (c && c.password) return { username: c.username || '', password: c.password };
      }
    } catch (e) { /* ignore (跨源 opener 会抛) */ }
    // 2) localStorage 兜底（旧版或者用户在主页点过"记住密码"）
    try {
      const key = 'otb:cred:' + system + '::' + server;
      const raw = localStorage.getItem(key);
      if (raw) {
        const o = JSON.parse(raw);
        if (o && o.password) return { username: o.username || '', password: o.password };
      }
    } catch (e) { /* ignore */ }
    return null;
  }

  // 让用户在弹窗里输入凭据（keyring 取不到密码时用）
  function askCred() {
    const u = prompt('SSH 用户名：');
    if (!u) return null;
    const p = prompt('SSH 密码（不勾"记住"则仅本次使用）：');
    if (p == null) return null;
    return { username: u, password: p };
  }

  const tailOut = $('#tail-out');
  const btnPause = $('#btn-pause');
  const btnClear = $('#btn-clear');
  const btnStop = $('#btn-stop');
  const btnClose = $('#btn-close');

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
  // 与 websphere.js 共享 OTB.core.tailHighlightPanel 工厂。
  // 持久化策略：
  //   1) 先尝试从 opener 的 OTB.state.tailHighlights 拿（项 9 修复：用户在主页刚设过）
  //   2) 兜底：本地 fetch GET /api/preferences 读 tail.highlights
  //   3) 都没有：空列表
  // onChange：PUT /api/preferences + 回写 opener 的 state（同进程多窗口同步）
  let highlightPanel = null;
  async function initHighlightPanel() {
    let initial = [];
    try {
      if (window.opener && window.opener.OTB && Array.isArray(window.opener.OTB.state && window.opener.OTB.state.tailHighlights)) {
        initial = window.opener.OTB.state.tailHighlights;
      }
    } catch (e) { /* ignore (跨源 opener 会抛) */ }
    if (!initial.length) {
      try {
        const prefs = await fetch('/api/preferences').then(r => r.ok ? r.json() : null);
        if (prefs && prefs.tail && Array.isArray(prefs.tail.highlights)) initial = prefs.tail.highlights;
      } catch (e) { /* ignore */ }
    }
    highlightPanel = OTB.core.tailHighlightPanel({
      initial,
      onChange: async (list) => {
        try {
          if (window.opener && window.opener.OTB) {
            window.opener.OTB.state = window.opener.OTB.state || {};
            window.opener.OTB.state.tailHighlights = list;
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
  const viewer = OTB.core.tailViewer({
    container: tailOut,
    maxLines: getMaxLines(),
    getHighlights: () => (highlightPanel && highlightPanel.enabled) ? highlightPanel.list : []
  });

  // 批量 flush：100ms / 100 行
  function scheduleFlush() {
    if (flushTimer) return;
    flushTimer = setTimeout(flushTail, 100);
  }
  function flushTail() {
    flushTimer = null;
    if (viewer.isPaused() || pendingLines.length === 0) return;
    const lines = pendingLines;
    pendingLines = [];
    viewer.pushBatch(lines);
    viewer.scrollToBottomIfNear();
    updateMeters();
  }
  setInterval(flushTail, 100);

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
  setInterval(updateMeters, 1000); // 每秒刷一次 meter 兜底（rate 计算兜底）

  btnPause.addEventListener('click', () => {
    const newPaused = !viewer.isPaused();
    viewer.setPaused(newPaused);
    btnPause.textContent = newPaused ? '继续' : '暂停';
    rateEl.textContent = newPaused ? '已暂停（缓冲 ' + pendingLines.length + ' 行）' : '实时显示中';
  });
  btnClear.addEventListener('click', () => {
    viewer.clear();
    pendingLines = [];
    rateEl.textContent = '实时显示中';
  });
  // 项 11 修复：max-lines 改动时立即 trim 到新上限
  if (maxLinesInp) {
    maxLinesInp.addEventListener('change', () => {
      viewer.setMaxLines(getMaxLines());
    });
  }
  btnStop.addEventListener('click', async () => {
    if (tailId) {
      try { await fetch('/api/logs/tail/' + tailId + '/stop', { method: 'POST' }); } catch (e) {}
    }
    if (evtSrc) { evtSrc.close(); evtSrc = null; }
    setConn('idle', '已停止');
    btnStop.disabled = true;
  });
  btnClose.addEventListener('click', () => { window.close(); });
  window.addEventListener('beforeunload', () => {
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
    let cred = getStoredCred();
    if (!cred || !cred.password) {
      cred = askCred();
      if (!cred) { setConn('err', '未提供凭据'); return; }
    }
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
      if (!r.ok) throw new Error(data && data.error || ('HTTP ' + r.status));
      tailId = data.id;
      setConn('ok', '已连接 · id=' + tailId);
      viewer.clear();
      pendingLines = [];
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
    viewer.scrollToBottomIfNear();
  }
  function appendError(msg) {
    viewer.push('⟦error⟧ ' + msg, 'error');
    viewer.scrollToBottomIfNear();
  }

  start();
})();