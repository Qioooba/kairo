/* ===== web/core.js =====
 * 通用工具：DOM/escape/格式化 + 校验/创建函数 + activeDL 管理
 *
 * 设计：
 *   - 零构建，纯 vanilla JS，挂到 window.OTB.core
 *   - 所有"无 DOM 依赖"的纯函数放在这里（escapeHtml / formatBytes / formatTime / cssEscape / pctText / trimMiddle / looksMojibake / basenameOf / validate）
 *   - 仍需要 document 的（el / $ / $$ / toast / setStatus / kvTable）也放这里（统一入口）
 *   - 测试 web/app.test.js 会从这里抽函数，所以命名必须保持稳定
 */

(function () {
  'use strict';

  if (!window.OTB) window.OTB = {};
  if (!window.OTB.core) window.OTB.core = {};
  const core = window.OTB.core;

  // -------- DOM helper --------

  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

  core.$ = $;
  core.$$ = $$;

  // el(tag, attrs, children)
  //
  // attrs 特殊键：
  //   - 'class'        → element.className
  //   - 'text'         → element.textContent（**安全**，自动 escape）
  //   - 'unsafeHtml'   → element.innerHTML（**危险**！仅用于硬编码 HTML，
  //                      或已经过 escapeHtml 处理的用户输入；
  //                      旧的 'html' 键名已废弃，避免误用）
  //   - 'on*'          → addEventListener
  //   - 其它           → setAttribute
  function el(tag, attrs, children) {
    const e = document.createElement(tag);
    if (attrs) {
      for (const k in attrs) {
        if (k === 'class') e.className = attrs[k];
        else if (k === 'text') e.textContent = attrs[k];
        else if (k === 'html') {
          // 兼容老调用：warn 但仍然执行，避免回归
          console.warn("[ops-toolbox] el(..., { html: ... }) is deprecated; use 'unsafeHtml' to make intent explicit, or 'text' to auto-escape.");
          e.innerHTML = attrs[k];
        } else if (k === 'unsafeHtml') e.innerHTML = attrs[k];
        else if (k.indexOf('on') === 0) e.addEventListener(k.slice(2), attrs[k]);
        else e.setAttribute(k, attrs[k]);
      }
    }
    if (children) {
      (Array.isArray(children) ? children : [children]).forEach(c => {
        if (c == null) return;
        if (typeof c === 'string') e.appendChild(document.createTextNode(c));
        else e.appendChild(c);
      });
    }
    return e;
  }
  core.el = el;

  // 防止 innerHTML 注入 — 简易转义
  function escapeHtml(s) {
    if (s == null) return '';
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }
  core.escapeHtml = escapeHtml;

  function toast(msg, type) {
    const t = $('#toast');
    t.textContent = msg;
    t.className = 'toast show' + (type ? ' ' + type : '');
    clearTimeout(toast._timer);
    toast._timer = setTimeout(() => { t.className = 'toast'; }, 2400);
  }
  core.toast = toast;

  // notify(opts) — 右上角持久通知（含路径 + 操作按钮），项 16 用
  //
  // opts:
  //   - title       (string)   标题
  //   - body        (string)   正文（路径 / 文件名）
  //   - type        ('ok'|'warn'|'err'|'')  颜色
  //   - actions     [{label, callback}]      操作按钮
  //   - duration    (ms, 默认 6000；0 = 不自动消失)
  //   - id          (string)   同 id 通知会替换旧的（避免堆叠）
  //
  // 通知堆在 body 右上角的 #otb-notify-stack 容器里，
  // 每条是一个 .otb-notify 卡片；点 × 关闭按钮立即移除。
  function notify(opts) {
    opts = opts || {};
    const stack = ensureNotifyStack();
    const id = opts.id;
    if (id) {
      const old = stack.querySelector('.otb-notify[data-id="' + cssEscape(id) + '"]');
      if (old) old.remove();
    }
    const card = el('div', { class: 'otb-notify' + (opts.type ? ' ' + opts.type : '') });
    if (id) card.setAttribute('data-id', id);
    if (opts.title) {
      card.appendChild(el('div', { class: 'otb-notify-title', text: opts.title }));
    }
    if (opts.body) {
      card.appendChild(el('div', { class: 'otb-notify-body', text: opts.body }));
    }
    if (opts.actions && opts.actions.length) {
      const actionsEl = el('div', { class: 'otb-notify-actions' });
      opts.actions.forEach(a => {
        actionsEl.appendChild(el('button', {
          class: 'btn btn-sm',
          text: a.label,
          onclick: (e) => {
            e.preventDefault();
            try { a.callback && a.callback(); } catch (err) { console.warn('notify action err', err); }
          }
        }));
      });
      card.appendChild(actionsEl);
    }
    card.appendChild(el('button', {
      class: 'otb-notify-close',
      text: '×',
      title: '关闭',
      onclick: () => card.remove()
    }));
    stack.appendChild(card);
    if (opts.duration !== 0) {
      setTimeout(() => { if (card.parentNode) card.remove(); }, opts.duration || 6000);
    }
    return card;
  }
  function ensureNotifyStack() {
    let stack = document.getElementById('otb-notify-stack');
    if (!stack) {
      stack = el('div', { id: 'otb-notify-stack', class: 'otb-notify-stack' });
      document.body.appendChild(stack);
    }
    return stack;
  }
  core.notify = notify;

  function setStatus(state, text) {
    const dot = $('#status-dot');
    const txt = $('#status-text');
    dot.className = 'dot dot-' + (state || 'idle');
    txt.textContent = text || (state === 'busy' ? '处理中…' : '就绪');
  }
  core.setStatus = setStatus;

  // -------- 格式化 --------

  function formatBytes(n) {
    n = Number(n) || 0;
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return n.toFixed(i ? 1 : 0) + ' ' + u[i];
  }
  core.formatBytes = formatBytes;

  function formatTime(s) {
    if (!s) return '-';
    try { return new Date(s).toLocaleString(); } catch (e) { return s; }
  }
  core.formatTime = formatTime;

  // CSS.escape polyfill：优先用浏览器原生，没有再走 fallback
  // 测试时 window.CSS = undefined，会走 fallback
  function cssEscape(s) {
    if (typeof window !== 'undefined' && window.CSS && CSS.escape) return CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_-]/g, c => '\\' + c);
  }
  core.cssEscape = cssEscape;

  // 百分比文本（用于下载进度）
  function pctText(written, total) {
    if (!total || total < 0) return formatBytes(written || 0);
    const pct = Math.min(100, Math.round((written / total) * 100));
    return pct + '% · ' + formatBytes(written) + ' / ' + formatBytes(total);
  }
  core.pctText = pctText;

  // 中间省略号（用于过长的 hit 行）
  function trimMiddle(s, max) {
    s = s || '';
    if (s.length <= max) return s;
    const keep = Math.floor((max - 1) / 2);
    return s.slice(0, keep) + '…' + s.slice(s.length - keep);
  }
  core.trimMiddle = trimMiddle;

  // 简易乱码检测：U+FFFD (�) 出现 ≥2 次。基本够用。
  function looksMojibake(s) {
    if (!s) return false;
    let bad = 0;
    for (let i = 0; i < s.length; i++) {
      if (s.charCodeAt(i) === 0xFFFD) bad++;
    }
    return bad >= 2;
  }
  core.looksMojibake = looksMojibake;

  // basename — 后端 SSE event 的 file 是完整路径，转 basename
  function basenameOf(p) {
    if (!p) return p;
    const s = String(p);
    const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
    return i >= 0 ? s.substring(i + 1) : s;
  }
  core.basenameOf = basenameOf;

  // -------- 配置校验（系统配置页保存前用）--------

  function validate(systems) {
    if (!systems.length) return '至少需要 1 个业务系统';
    for (let si = 0; si < systems.length; si++) {
      const s = systems[si];
      if (!s.name || !s.name.trim()) return '系统 #' + (si + 1) + ' 的名称不能为空';
      if (!s.servers || !s.servers.length) return '系统 ' + s.name + ' 至少需要 1 台服务器';
      for (let sri = 0; sri < s.servers.length; sri++) {
        const srv = s.servers[sri];
        if (!srv.name || !srv.name.trim()) return '系统 ' + s.name + ' 第 ' + (sri + 1) + ' 台服务器名不能为空';
        if (!srv.host || !srv.host.trim()) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 缺少 IP / 主机';
        if (!(srv.port > 0 && srv.port < 65536)) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 端口非法';
        if (!srv.log_dirs || !srv.log_dirs.length) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 至少需要 1 个日志目录';
        for (let ldi = 0; ldi < srv.log_dirs.length; ldi++) {
          const ld = srv.log_dirs[ldi];
          if (!ld.name || !ld.name.trim()) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 第 ' + (ldi + 1) + ' 个目录别名不能为空';
          if (!ld.path || !ld.path.trim()) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 第 ' + (ldi + 1) + ' 个目录路径不能为空';
          if (!ld.patterns || !ld.patterns.length) return '系统 ' + s.name + ' 服务器 ' + srv.name + ' 目录 ' + ld.name + ' 至少要 1 条文件名规则';
          for (const p of ld.patterns) {
            if (/[\\\;\|`$(){}<>\"'\n\r\t]/.test(p)) return '目录 ' + ld.name + ' 的文件名规则含非法字符：' + p;
          }
        }
      }
    }
    return null;
  }
  core.validate = validate;

  // -------- 编辑器辅助（系统配置）--------

  function newSystem() {
    return { name: '', description: '', servers: [newServer()] };
  }
  function newServer() {
    return { name: '', host: '', port: 22, username: '', auth_type: 'password', log_dirs: [newLogDir()] };
  }
  function newLogDir() {
    return { name: '', path: '', patterns: ['*.log'], encoding: 'utf-8' };
  }
  core.newSystem = newSystem;
  core.newServer = newServer;
  core.newLogDir = newLogDir;

  // -------- KV table（系统配置 / diagnostics 用）--------

  function kvTable(rows) {
    const tbl = el('table', { class: 'table' });
    const tbody = el('tbody');
    rows.forEach(r => {
      tbody.appendChild(el('tr', null, [
        el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
        el('td', { text: r[1] == null || r[1] === '' ? '-' : r[1] })
      ]));
    });
    tbl.appendChild(tbody);
    return tbl;
  }
  core.kvTable = kvTable;

  // -------- 全局 activeDL 管理（websphere / files 页下载时注册，navigate 切走时清理）--------

  // 全局进行中的下载任务（用于 navigate 清理）
  // 引用挂这里而不是闭包里，是因为 renderWebsphere 重建后旧 EventSource 没人能关。
  if (typeof window !== 'undefined') {
    window.__opsActiveDL = window.__opsActiveDL || null;
  }

  function setActiveDL(dl) {
    window.__opsActiveDL = dl;
  }
  function getActiveDL() {
    return window.__opsActiveDL;
  }
  function clearActiveDL() {
    if (window.__opsActiveDL) {
      try { window.__opsActiveDL.evtsrc && window.__opsActiveDL.evtsrc.close(); } catch (e) { /* ignore */ }
    }
    window.__opsActiveDL = null;
  }
  core.setActiveDL = setActiveDL;
  core.getActiveDL = getActiveDL;
  core.clearActiveDL = clearActiveDL;

  // -------- "上次选择" 记忆（系统 / 服务器 / 日志目录 / 凭据） --------
  //
  // 用 localStorage 跨页面跨刷新记，键格式 otb:last:<page>:<key>
  // 跨页面共享（websphere / files 都用同一份），自动 JSON 化。
  // 写入失败（隐私模式 / quota 超）静默忽略。
  const LAST_PREFIX = 'otb:last:';

  function lastGet(page, key) {
    try {
      const raw = localStorage.getItem(LAST_PREFIX + page + ':' + key);
      if (!raw) return null;
      return JSON.parse(raw);
    } catch (e) { return null; }
  }
  function lastSet(page, key, val) {
    try {
      if (val === null || val === undefined) {
        localStorage.removeItem(LAST_PREFIX + page + ':' + key);
      } else {
        localStorage.setItem(LAST_PREFIX + page + ':' + key, JSON.stringify(val));
      }
    } catch (e) { /* ignore */ }
  }
  core.lastGet = lastGet;
  core.lastSet = lastSet;
})();