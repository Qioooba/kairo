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

  // copyToClipboard(text) → Promise
  // 优先 navigator.clipboard（HTTPS / localhost 才可用），否则走隐藏 textarea + execCommand 兜底。
  // 老 websphere.js / compare.js 自己实现了一份，现在统一到 core 里。
  function copyToClipboard(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).catch(() => fallbackCopyText(text));
    }
    return fallbackCopyText(text);
  }
  function fallbackCopyText(text) {
    return new Promise((resolve, reject) => {
      try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed'; ta.style.opacity = '0'; ta.style.top = '0'; ta.style.left = '0';
        document.body.appendChild(ta);
        ta.focus(); ta.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(ta);
        ok ? resolve() : reject(new Error('execCommand copy 返回 false'));
      } catch (e) { reject(e); }
    });
  }
  core.copyToClipboard = copyToClipboard;

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
      onclick: () => { card.remove(); updateClearAllBtn(); }
    }));
    stack.appendChild(card);
    updateClearAllBtn();
    if (opts.duration !== 0) {
      setTimeout(() => { if (card.parentNode) { card.remove(); updateClearAllBtn(); } }, opts.duration || 6000);
    }
    return card;
  }
  function ensureNotifyStack() {
    let stack = document.getElementById('otb-notify-stack');
    if (!stack) {
      stack = el('div', { id: 'otb-notify-stack', class: 'otb-notify-stack' });
      const clearAll = el('button', {
        id: 'otb-notify-clearall',
        class: 'otb-notify-clearall',
        text: '全部清空',
        onclick: () => {
          const cards = stack.querySelectorAll('.otb-notify');
          cards.forEach(c => c.remove());
          updateClearAllBtn();
        }
      });
      stack.appendChild(clearAll);
      document.body.appendChild(stack);
    }
    return stack;
  }
  function updateClearAllBtn() {
    const stack = document.getElementById('otb-notify-stack');
    if (!stack) return;
    const clearAll = document.getElementById('otb-notify-clearall');
    if (!clearAll) return;
    const cards = stack.querySelectorAll('.otb-notify');
    clearAll.style.display = cards.length >= 2 ? 'block' : 'none';
  }
  core.notify = notify;

  // -------- 下载 badge（侧边栏红点） --------
  let dlBadgeCount = 0;
  function bumpDlBadge(n) {
    dlBadgeCount += (n || 1);
    const el2 = document.getElementById('dl-badge');
    if (!el2) return;
    if (dlBadgeCount > 0) {
      el2.style.display = 'inline-block';
      el2.textContent = dlBadgeCount > 99 ? '99+' : String(dlBadgeCount);
    } else {
      el2.style.display = 'none';
    }
  }
  function clearDlBadge() {
    dlBadgeCount = 0;
    const el2 = document.getElementById('dl-badge');
    if (el2) el2.style.display = 'none';
  }
  core.bumpDlBadge = bumpDlBadge;
  core.clearDlBadge = clearDlBadge;

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

  // -------- Tail 高亮（共享纯函数） --------
  //
  // 设计：
  //   - 入参：纯文本行 + 高亮规则列表 [{keyword, bg, fg, caseSensitive?}, ...]
  //   - 出参：DOM 节点（DocumentFragment），由调用方 appendChild 到容器
  //   - 关键词匹配：大小写不敏感（默认）；空关键词跳过；按出现位置切段；
  //     多关键词重叠时取先匹配（按数组顺序），匹配段用 span 包 background/color。
  //
  // 安全：所有用户输入（关键词 + 文本内容）都走 escapeHtml 包裹，
  //       不引入 XSS。颜色用严格正则校验，过滤掉任意 css 注入路径。
  //
  // 性能：一行里切段最多 N 段（N = 关键词数 + 1），无循环嵌套；
  //       单行 100ms 量级，1 万行 ≈ 几秒——但 tail 流是缓冲+rAF 批量 append，
  //       实测不影响流畅。

  // 高亮规则的"合法颜色"：#RGB / #RRGGBB / #RRGGBBAA / rgb(...) / rgba(...) / 预定义名
  // 严格匹配，避免 "red; background:url(javascript:...)" 这种注入。
  const SAFE_COLOR_RE = /^(#[0-9a-fA-F]{3,8}|rgb\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*\)|rgba\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*(0|0?\.\d+|1(\.0+)?)\s*\))$/;
  // 预定义安全色名（CSS 标准 16 + 透明 + 几个常用），避开"system"等可能触发系统样式的值
  const SAFE_COLOR_NAMES = {
    red: '#ef4444', green: '#22c55e', blue: '#3b82f6', yellow: '#eab308',
    orange: '#f97316', purple: '#a855f7', pink: '#ec4899', cyan: '#06b6d4',
    gray: '#6b7280', black: '#000000', white: '#ffffff', transparent: 'transparent'
  };

  // normalizeHighlight 把用户填的颜色统一成"安全色字符串"。
  //   - 空 / 非法 → '#ef4444'（兜底红，醒目但不刺眼）
  //   - 'red'/'green' 等命名 → 查表替换
  //   - '#abc' / '#aabbcc' / rgb(...) → 校验通过保留原样，否则兜底
  function normalizeHighlightColor(c) {
    if (c == null) return '#ef4444';
    const raw = String(c).trim();
    if (!raw) return '#ef4444';
    // 命名色：lowercase 后查表（red → #ef4444）
    const low = raw.toLowerCase();
    if (SAFE_COLOR_NAMES[low]) return SAFE_COLOR_NAMES[low];
    // hex / rgb / rgba：校验通过则原样返回（保留用户的大小写）
    if (SAFE_COLOR_RE.test(raw)) return raw;
    return '#ef4444';
  }

  // buildHighlightRegex 把 highlights 数组编译成"一个大正则 + 区间标记"。
  //
  // 返回 {regex, map}：
  //   - regex.test(line) 判断是否有任意命中（性能）
  //   - regex.exec(line) 拿到 [whole, g1, g2, ...]，map[i] 是第 i 组对应的 highlight 索引
  //   - 无高亮规则 → regex = null
  //
  // 为什么用分组不用 split：split 拿不到"哪个关键词命中"，无法对应颜色。
  // 分组版本：把每个关键词包成非捕获 (?:) + 命名/编号捕获，正则一次扫完，
  // 命中段按组号查 map。
  function buildHighlightRegex(highlights) {
    if (!Array.isArray(highlights) || highlights.length === 0) return null;
    const valid = [];
    highlights.forEach((h, idx) => {
      const kw = (h && h.keyword) ? String(h.keyword) : '';
      if (!kw) return;
      // 转义正则元字符（关键词按字面匹配，不支持正则语法 —— 避免用户写 ".*" 误匹配全行）
      const escaped = kw.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      valid.push({ kw, escaped, idx, ci: !h.caseSensitive });
    });
    if (!valid.length) return null;
    const parts = valid.map((v, i) => {
      const flags = v.ci ? 'i' : '';
      return '(?:' + v.escaped + ')';
    });
    // 用一个巨大的非捕获组把多个候选并起来，里面再放捕获组（不能直接用 named 因为动态个数不好取）
    // 但 ES 允许 (?:(a)|(b)|(c)) —— 匹配后看哪个捕获组非 null 即可
    const re = new RegExp('(?:' + parts.join('|') + ')', valid[0].ci ? 'gi' : 'g');
    return { regex: re, valid };
  }

  // renderHighlightedLine 把一行纯文本按 highlights 规则切成 DocumentFragment。
  //
  //   - highlights: [{keyword, bg, fg, caseSensitive?}, ...]
  //   - 返回：DocumentFragment（外面 appendChild 即可）
  //   - 无高亮或无命中：返回一个 TextNode（纯文本，无 span 开销）
  //
  // 实现要点：
  //   - 用 buildHighlightRegex 拿到一个全局正则；
  //   - exec 循环收集 [{start, end, hi}] 区间（hi 是对应 highlight 配置）；
  //   - 把区间合并 / 按行顺序输出；区间外是纯 TextNode；
  //   - 区间内：background / color 来自 normalizeHighlightColor 后的字符串；
  //     text 走 escapeHtml（任何用户输入都安全）。
  function renderHighlightedLine(line, highlights) {
    const frag = document.createDocumentFragment();
    if (line == null) {
      frag.appendChild(document.createTextNode(''));
      return frag;
    }
    if (!highlights || !highlights.length) {
      frag.appendChild(document.createTextNode(String(line)));
      return frag;
    }
    const built = buildHighlightRegex(highlights);
    if (!built) {
      frag.appendChild(document.createTextNode(String(line)));
      return frag;
    }
    const text = String(line);
    // 收集所有命中区间
    const hits = [];
    built.regex.lastIndex = 0;
    let m;
    // exec 找到分组索引 → 查 built.valid[idx]
    while ((m = built.regex.exec(text)) !== null) {
      // m[0] 是整段匹配；找哪个 valid 命中
      // 因为并起来是 (?:(a)|(b)|(c))，m 只有 m[0]，但我们可以用 m.index + 长度反查
      // 简化：拿 m[0]，到 built.valid 里找第一个 kw === m[0]（按大小写）
      // —— 注意大小写不敏感时 m[0] 可能是原大小写，要忽略大小写比对
      const matched = m[0];
      let found = -1;
      for (let i = 0; i < built.valid.length; i++) {
        if (built.valid[i].ci) {
          if (built.valid[i].kw.toLowerCase() === matched.toLowerCase()) { found = i; break; }
        } else {
          if (built.valid[i].kw === matched) { found = i; break; }
        }
      }
      if (found === -1) continue; // 防御
      const hi = highlights[built.valid[found].idx];
      const bg = normalizeHighlightColor(hi && hi.bg);
      const fg = normalizeHighlightColor(hi && hi.fg);
      hits.push({ start: m.index, end: m.index + matched.length, bg, fg });
      // 防 0 长度死循环（不该发生，因为关键词非空）
      if (m.index === built.regex.lastIndex) built.regex.lastIndex++;
    }
    if (hits.length === 0) {
      frag.appendChild(document.createTextNode(text));
      return frag;
    }
    // 按 start 排序（exec 一般已经有序，但防万一）
    hits.sort((a, b) => a.start - b.start);
    // 合并相邻/重叠区间（重叠的取第一个的颜色，避免 span 嵌套）
    const merged = [];
    for (const h of hits) {
      const last = merged[merged.length - 1];
      if (last && h.start <= last.end) {
        // 重叠：扩展 last.end，但不换颜色（保留首个的颜色，避免重叠区域视觉混乱）
        if (h.end > last.end) last.end = h.end;
      } else {
        merged.push({ ...h });
      }
    }
    // 输出
    let cursor = 0;
    for (const seg of merged) {
      if (seg.start > cursor) {
        frag.appendChild(document.createTextNode(text.slice(cursor, seg.start)));
      }
      const span = document.createElement('span');
      span.style.background = seg.bg;
      span.style.color = seg.fg;
      span.textContent = text.slice(seg.start, seg.end);
      frag.appendChild(span);
      cursor = seg.end;
    }
    if (cursor < text.length) {
      frag.appendChild(document.createTextNode(text.slice(cursor)));
    }
    return frag;
  }

  // 预设 12 色调色板（颜色选择器用）
  const HIGHLIGHT_PALETTE = [
    { name: '红', bg: '#ef4444', fg: '#ffffff' },
    { name: '橙', bg: '#f97316', fg: '#000000' },
    { name: '黄', bg: '#eab308', fg: '#000000' },
    { name: '绿', bg: '#22c55e', fg: '#000000' },
    { name: '青', bg: '#06b6d4', fg: '#000000' },
    { name: '蓝', bg: '#3b82f6', fg: '#ffffff' },
    { name: '紫', bg: '#a855f7', fg: '#ffffff' },
    { name: '粉', bg: '#ec4899', fg: '#ffffff' },
    { name: '灰', bg: '#6b7280', fg: '#ffffff' },
    { name: '黑', bg: '#000000', fg: '#ffffff' },
    { name: '亮红', bg: '#fecaca', fg: '#7f1d1d' },
    { name: '亮黄', bg: '#fef08a', fg: '#713f12' }
  ];

  core.normalizeHighlightColor = normalizeHighlightColor;
  core.buildHighlightRegex = buildHighlightRegex;
  core.renderHighlightedLine = renderHighlightedLine;
  core.HIGHLIGHT_PALETTE = HIGHLIGHT_PALETTE;
  core.SAFE_COLOR_NAMES = SAFE_COLOR_NAMES;

  // -------- Tail 高亮面板（共享 UI 工厂） --------
  //
  // 用法：
  //   const panel = OTB.core.tailHighlightPanel({
  //     initial: OTB.state.tailHighlights || [],   // 初始规则
  //     onChange: async (list) => { ... },         // 规则变化时（持久化）
  //     onToggle: (enabled) => { ... }             // 启停切换（可选）
  //   });
  //   container.appendChild(panel.root);
  //
  // 返回 panel.root（DOM）+ panel.update(list) 方法（外部修改规则后同步 UI）。
  //
  // 面板布局：
  //   [☑ 启用高亮]  [关键词 input] [颜色 select+preview] [+ 添加] [清空]
  //   ┌──────── 规则列表（每行：颜色块 · 关键词 · ✕） ────────┐
  //
  // 持久化：onChange 回调里调 PUT /api/preferences 把 list 落到 data/preferences.json。
  //         onChange 失败时给 toast 提示，UI 不回滚（避免规则丢失）—— 调用方可决定策略。
  //
  // 启停：enabled 状态不写入 preferences（这是临时开关，不持久化）。
  //       重新打开页面默认 enabled=true（用户上次没主动关过）。

  function tailHighlightPanel(opts) {
    opts = opts || {};
    const initial = Array.isArray(opts.initial) ? opts.initial.slice() : [];
    const onChange = opts.onChange || function () {};
    // 内部状态：list 是"当前规则"，外部不直接改。
    let list = initial.map(normalizeRule);
    let enabled = true;
    let busy = false;

    function normalizeRule(r) {
      // 兼容老数据 / 半成品：补字段 + 颜色过 normalize
      const bg = normalizeHighlightColor(r && r.bg);
      const fg = normalizeHighlightColor(r && r.fg);
      return {
        keyword: (r && r.keyword) ? String(r.keyword) : '',
        bg, fg,
        caseSensitive: !!(r && r.caseSensitive)
      };
    }
    function commit() {
      // 浅拷贝，剔除空关键词
      const clean = list.filter(r => r.keyword).map(r => ({
        keyword: r.keyword, bg: r.bg, fg: r.fg, caseSensitive: r.caseSensitive
      }));
      try { onChange(clean); } catch (e) { /* ignore */ }
    }
    function renderRuleList() {
      listEl.innerHTML = '';
      if (!list.length) {
        const empty = document.createElement('div');
        empty.className = 'text-dim tail-highlight-empty';
        empty.textContent = '还没有高亮规则 —— 上方输入关键词 + 选个颜色，点"添加"';
        listEl.appendChild(empty);
        return;
      }
      list.forEach((r, idx) => {
        const row = document.createElement('div');
        row.className = 'tail-highlight-row';
        // 色块（预览）
        const swatch = document.createElement('span');
        swatch.className = 'tail-highlight-swatch';
        swatch.style.background = r.bg;
        swatch.style.color = r.fg;
        swatch.textContent = r.keyword ? r.keyword.charAt(0).toUpperCase() : '?';
        swatch.title = r.bg + ' / ' + r.fg;
        // 关键词（可点击编辑）
        const kwSpan = document.createElement('span');
        kwSpan.className = 'tail-highlight-kw';
        kwSpan.textContent = r.keyword + (r.caseSensitive ? '  [Aa]' : '  [aa]');
        kwSpan.title = r.caseSensitive ? '区分大小写' : '不区分大小写';
        // 大小写切换按钮
        const caseBtn = document.createElement('button');
        caseBtn.className = 'btn btn-sm tail-highlight-case';
        caseBtn.textContent = r.caseSensitive ? 'Aa' : 'aa';
        caseBtn.title = '切换大小写敏感';
        caseBtn.addEventListener('click', () => {
          r.caseSensitive = !r.caseSensitive;
          renderRuleList();
          commit();
        });
        // 删除
        const rmBtn = document.createElement('button');
        rmBtn.className = 'btn btn-sm btn-danger tail-highlight-rm';
        rmBtn.textContent = '✕';
        rmBtn.title = '删除该规则';
        rmBtn.addEventListener('click', () => {
          list.splice(idx, 1);
          renderRuleList();
          commit();
        });
        row.appendChild(swatch);
        row.appendChild(kwSpan);
        row.appendChild(caseBtn);
        row.appendChild(rmBtn);
        listEl.appendChild(row);
      });
    }

    // 根容器
    const root = document.createElement('div');
    root.className = 'tail-highlight-panel';

    // 顶部：启用 checkbox + 输入区
    const top = document.createElement('div');
    top.className = 'tail-highlight-top';

    const enableLabel = document.createElement('label');
    enableLabel.className = 'inline tail-highlight-enable';
    const enableChk = document.createElement('input');
    enableChk.type = 'checkbox';
    enableChk.checked = enabled;
    enableChk.addEventListener('change', () => {
      enabled = enableChk.checked;
      root.classList.toggle('disabled', !enabled);
      if (typeof opts.onToggle === 'function') {
        try { opts.onToggle(enabled); } catch (e) { /* ignore */ }
      }
    });
    enableLabel.appendChild(enableChk);
    enableLabel.appendChild(document.createTextNode('启用高亮'));
    top.appendChild(enableLabel);

    const kwInput = document.createElement('input');
    kwInput.type = 'text';
    kwInput.placeholder = '关键词（支持中文 / 特殊字符）';
    kwInput.className = 'tail-highlight-kw-input';
    top.appendChild(kwInput);

    // 颜色 select（用 palette 提供常用色 + 自定义 hex）
    const colorSel = document.createElement('select');
    colorSel.className = 'tail-highlight-color-sel';
    HIGHLIGHT_PALETTE.forEach(p => {
      const o = document.createElement('option');
      o.value = p.bg + '|' + p.fg;
      o.textContent = p.name + '  ' + p.bg;
      o.dataset.bg = p.bg;
      o.dataset.fg = p.fg;
      colorSel.appendChild(o);
    });
    // 默认选第一个（红）
    colorSel.value = HIGHLIGHT_PALETTE[0].bg + '|' + HIGHLIGHT_PALETTE[0].fg;
    top.appendChild(colorSel);

    // 自定义 hex
    const customColor = document.createElement('input');
    customColor.type = 'color';
    customColor.className = 'tail-highlight-custom';
    customColor.value = HIGHLIGHT_PALETTE[0].bg;
    customColor.title = '自定义颜色（背景）';
    top.appendChild(customColor);
    colorSel.addEventListener('change', () => {
      const [bg, fg] = colorSel.value.split('|');
      customColor.value = bg;
      customColor.dataset.fg = fg;
    });
    customColor.addEventListener('input', () => {
      // 自定义颜色时：前景文字按背景明度选黑/白
      const bg = customColor.value;
      const fg = pickFgForBg(bg);
      colorSel.value = '__custom__';
      customColor.dataset.fg = fg;
      // 让 select 显示"自定义"
      let opt = colorSel.querySelector('option[value="__custom__"]');
      if (!opt) {
        opt = document.createElement('option');
        opt.value = '__custom__';
        opt.textContent = '自定义 ' + bg;
        colorSel.appendChild(opt);
      }
      opt.dataset.bg = bg;
      opt.dataset.fg = fg;
      opt.textContent = '自定义 ' + bg;
      colorSel.value = '__custom__';
    });

    // 大小写勾选
    const caseLabel = document.createElement('label');
    caseLabel.className = 'inline';
    const caseChk = document.createElement('input');
    caseChk.type = 'checkbox';
    caseLabel.appendChild(caseChk);
    caseLabel.appendChild(document.createTextNode('Aa'));
    caseLabel.title = '区分大小写';
    top.appendChild(caseLabel);

    // 添加按钮
    const addBtn = document.createElement('button');
    addBtn.className = 'btn btn-sm btn-primary';
    addBtn.textContent = '添加';
    addBtn.addEventListener('click', () => {
      const kw = (kwInput.value || '').trim();
      if (!kw) {
        if (typeof toast === 'function') toast('请先输入关键词', 'warn');
        return;
      }
      let bg, fg;
      if (colorSel.value === '__custom__') {
        bg = customColor.value;
        fg = customColor.dataset.fg || pickFgForBg(bg);
      } else {
        const opt = colorSel.options[colorSel.selectedIndex];
        bg = opt.dataset.bg;
        fg = opt.dataset.fg;
      }
      list.push({
        keyword: kw, bg, fg, caseSensitive: caseChk.checked
      });
      kwInput.value = '';
      renderRuleList();
      commit();
    });
    top.appendChild(addBtn);

    // 清空按钮
    const clearBtn = document.createElement('button');
    clearBtn.className = 'btn btn-sm';
    clearBtn.textContent = '清空';
    clearBtn.addEventListener('click', () => {
      if (!list.length) return;
      if (!window.confirm('清空全部高亮规则？')) return;
      list = [];
      renderRuleList();
      commit();
    });
    top.appendChild(clearBtn);

    // 提示
    const hint = document.createElement('span');
    hint.className = 'text-dim tail-highlight-hint';
    hint.textContent = '匹配关键词的行会按颜色高亮 · 规则保存到本地 data/preferences.json';
    top.appendChild(hint);

    root.appendChild(top);

    // 规则列表
    const listEl = document.createElement('div');
    listEl.className = 'tail-highlight-list';
    root.appendChild(listEl);

    renderRuleList();

    // 暴露更新方法（外部修改规则后同步 UI；不常用，保留扩展点）
    const panel = {
      root,
      get enabled() { return enabled; },
      get list() { return list.slice(); },
      update(newList) {
        list = (Array.isArray(newList) ? newList : []).map(normalizeRule);
        renderRuleList();
      }
    };
    return panel;
  }

  // 工具：按背景色明度选黑白前景。WCAG 简单估算（0.299R + 0.587G + 0.114B）。
  function pickFgForBg(hex) {
    const m = /^#([0-9a-fA-F]{6})$/.exec(hex || '');
    if (!m) return '#000000';
    const n = parseInt(m[1], 16);
    const r = (n >> 16) & 0xff, g = (n >> 8) & 0xff, b = n & 0xff;
    const lum = 0.299 * r + 0.587 * g + 0.114 * b;
    return lum > 140 ? '#000000' : '#ffffff';
  }

  core.tailHighlightPanel = tailHighlightPanel;
  core.pickFgForBg = pickFgForBg;

  // -------- TailViewer（P0-2：环形 buffer + 行级 DOM 节点池） --------
  //
  // 旧实现问题：
  //   - <pre> + textContent 拼接所有行；超限时 textContent = arr.slice(N).join('\n')
  //     整字符串重建 → 5000 行截断一次 O(N) 字符串拷贝 + 浏览器重解析整 <pre>
  //   - 高亮 span 嵌在 pre 文本里，截断时 span 边界全断
  //
  // 新实现：
  //   - 容器 div，每行一个 <div class="tail-line">（不再用 <pre> 连续文本）
  //   - 内部维护 _lines: string[] + _nodes: HTMLElement[] 平行数组
  //   - 超 maxLines 时，**头部 removeChild + shift**（DOM 节点级 O(1)/行，
  //     浏览器无需重解析整个容器）
  //   - 高亮按行调 renderHighlightedLine（每行只算一次，进 DOM 后不再重算）
  //   - 暂停：push 直接丢行（不缓冲）
  //   - 滚动到底：追加行后检查用户是否在底部（80px 阈值），在则 scrollTop = scrollHeight
  //
  // 复制语义变化（取舍）：
  //   - 旧版"在容器里 Ctrl+A 复制"能拿所有可见文本
  //   - 新版"Ctrl+A 复制"只拿当前 DOM 里的行（最多 maxLines）
  //   - 用 viewer.getText() + 显式"复制全部"按钮拿完整 buffer（更明确）
  function tailViewer(opts) {
    opts = opts || {};
    const container = opts.container;
    if (!container || !container.appendChild) {
      throw new Error('tailViewer: container 必须是 DOM 元素');
    }
    const getHighlights = (typeof opts.getHighlights === 'function') ? opts.getHighlights : () => [];

    // 内部状态
    const _lines = [];      // 环形 buffer 的纯文本
    const _nodes = [];      // 平行 DOM 节点数组
    let _maxLines = Math.max(100, Math.min(50000, Number(opts.maxLines) || 1000));
    let _paused = false;
    let _totalEver = 0;     // 累计 push 的总行数（清屏不重置）
    let _disposed = false;

    if (!container.classList.contains('tail-out')) {
      container.classList.add('tail-out');
    }
    container.innerHTML = '';

    // ----- 内部：按 highlights 渲染一行 -----
    function makeLineNode(text, kind) {
      const div = document.createElement('div');
      div.className = 'tail-line' + (kind ? ' tail-line-' + kind : '');
      const highlights = getHighlights() || [];
      if (highlights.length) {
        div.appendChild(renderHighlightedLine(text, highlights));
      } else {
        div.appendChild(document.createTextNode(text));
      }
      return div;
    }

    // ----- 公开：推一行 -----
    function push(text, kind) {
      if (_disposed || _paused) return;
      const s = (text == null) ? '' : String(text);
      const node = makeLineNode(s, kind);
      _lines.push(s);
      _nodes.push(node);
      container.appendChild(node);
      _totalEver++;
      if (_lines.length > _maxLines) {
        _lines.shift();
        const old = _nodes.shift();
        if (old && old.parentNode) old.parentNode.removeChild(old);
      }
    }

    // ----- 公开：批量推（一次 appendChild 调用，浏览器只 reflow 一次） -----
    function pushBatch(arr, kind) {
      if (_disposed || _paused) return;
      if (!arr || !arr.length) return;
      // 先 trim 旧：splice 出要丢的节点 + 文本，再批量 append
      if (_lines.length + arr.length > _maxLines) {
        const needDrop = (_lines.length + arr.length) - _maxLines;
        const drop = Math.min(_lines.length, needDrop);
        if (drop > 0) {
          const toRemove = _nodes.splice(0, drop);
          for (let i = 0; i < toRemove.length; i++) {
            const n = toRemove[i];
            if (n && n.parentNode) n.parentNode.removeChild(n);
          }
          _lines.splice(0, drop);
        }
      }
      const highlights = getHighlights() || [];
      const frag = document.createDocumentFragment();
      for (let i = 0; i < arr.length; i++) {
        const s = (arr[i] == null) ? '' : String(arr[i]);
        const div = document.createElement('div');
        div.className = 'tail-line' + (kind ? ' tail-line-' + kind : '');
        if (highlights.length) {
          div.appendChild(renderHighlightedLine(s, highlights));
        } else {
          div.appendChild(document.createTextNode(s));
        }
        frag.appendChild(div);
        _lines.push(s);
        _nodes.push(div);
        _totalEver++;
      }
      container.appendChild(frag);
    }

    // ----- 公开：清屏 -----
    function clear() {
      _lines.length = 0;
      _nodes.length = 0;
      container.innerHTML = '';
    }

    // ----- 公开：暂停/继续 -----
    function setPaused(b) { _paused = !!b; }
    function isPaused() { return _paused; }

    // ----- 公开：滚动到底（如果用户已经在底部） -----
    function scrollToBottomIfNear() {
      const dist = container.scrollHeight - container.clientHeight - container.scrollTop;
      if (dist < 80) {
        container.scrollTop = container.scrollHeight;
      }
    }
    function scrollToBottom() {
      container.scrollTop = container.scrollHeight;
    }

    // ----- 公开：拿全部纯文本（用于"复制全部"按钮） -----
    function getText() { return _lines.join('\n'); }
    function lineCount() { return _lines.length; }
    function totalEver() { return _totalEver; }
    function getMaxLines() { return _maxLines; }
    function setMaxLines(n) {
      const v = Math.max(100, Math.min(50000, Number(n) || 1000));
      if (v === _maxLines) return;
      if (v < _maxLines) {
        // 新上限更小：立即 trim
        while (_lines.length > v) {
          _lines.shift();
          const nd = _nodes.shift();
          if (nd && nd.parentNode) nd.parentNode.removeChild(nd);
        }
      }
      // 新上限更大：不动 buffer，下次 push 触顶时 trim
      _maxLines = v;
    }

    function dispose() {
      _disposed = true;
      clear();
    }

    return {
      push,
      pushBatch,
      clear,
      setPaused,
      isPaused,
      scrollToBottomIfNear,
      scrollToBottom,
      getText,
      lineCount,
      totalEver,
      getMaxLines,
      setMaxLines,
      dispose
    };
  }

  core.tailViewer = tailViewer;
})();