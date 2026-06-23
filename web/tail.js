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
  function getStoredCred() {
    try {
      // 同源 localStorage 共享主页面
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
  let paused = false;
  let pendingLines = [];
  let flushTimer = null;
  let totalLines = 0;
  let lastRateAt = Date.now();
  let lastRateCount = 0;
  let rateEl = $('#rate');

  // 批量 append 优化：每 100ms 或累积 200 行 flush 一次，避免 textContent 拼接抖动
  function scheduleFlush() {
    if (flushTimer) return;
    flushTimer = setTimeout(flushTail, 100);
  }
  function flushTail() {
    flushTimer = null;
    if (paused || pendingLines.length === 0) return;
    const chunk = pendingLines.join('\n') + '\n';
    pendingLines = [];
    // 用 appendChild 节点而非 textContent +=，避免整段重排
    tailOut.appendChild(document.createTextNode(chunk));
    totalLines += chunk.split('\n').length - 1;
    $('#m-lines').textContent = totalLines + ' 行';
    // 自动滚动到底部
    tailOut.scrollTop = tailOut.scrollHeight;
    // 限速：5000 行上限
    const linesArr = tailOut.textContent.split('\n');
    if (linesArr.length > 5000) {
      tailOut.textContent = linesArr.slice(linesArr.length - 5000).join('\n');
      totalLines = 5000;
    }
    // 速率显示
    const now = Date.now();
    if (now - lastRateAt >= 2000) {
      const dt = (now - lastRateAt) / 1000;
      const dn = totalLines - lastRateCount;
      const rate = dn / dt;
      rateEl.textContent = '实时 · ' + rate.toFixed(1) + ' 行/秒';
      lastRateAt = now;
      lastRateCount = totalLines;
    }
  }
  setInterval(flushTail, 100);

  btnPause.addEventListener('click', () => {
    paused = !paused;
    btnPause.textContent = paused ? '继续' : '暂停';
    rateEl.textContent = paused ? '已暂停（缓冲 ' + pendingLines.length + ' 行）' : '实时显示中';
    if (!paused) flushTail();
  });
  btnClear.addEventListener('click', () => {
    tailOut.textContent = '';
    pendingLines = [];
    totalLines = 0;
    $('#m-lines').textContent = '0 行';
    rateEl.textContent = '实时显示中';
  });
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
      tailOut.textContent = '';
      pendingLines = [];
      totalLines = 0;
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

  function appendInfo(msg) {
    tailOut.appendChild(document.createTextNode('⟦info⟧ ' + msg + '\n'));
    tailOut.scrollTop = tailOut.scrollHeight;
  }
  function appendError(msg) {
    tailOut.appendChild(document.createTextNode('⟦error⟧ ' + msg + '\n'));
    tailOut.scrollTop = tailOut.scrollHeight;
  }

  start();
})();