(function () {
  'use strict';
  const DTB = window.DTB = window.DTB || {};
  DTB.pages = DTB.pages || {};
  const { el, toast } = DTB.core;
  const { api } = DTB.api;

  let lastResult = null;
  let outputMode = 'unified';
  let hideEqualRows = false;
  const POPUP_NAME = 'dtb_compare_diff';

  let folderScanResult = null;
  let folderExpanded = new Set();
  let folderShowOnlyDiff = false;
  let folderFilter = '';
  let folderExpandedSnapshot = null;
  let selectedSuspects = new Set();
  let isScanning = false;
  let isDeepChecking = false;

  const LS_IGNORE = 'otb:compare:ignore';
  const LS_MODE = 'otb:compare:mode';
  const LS_HIDE_EQUAL = 'otb:compare:hideEqual';
  const LS_LEFT_FOLDER = 'otb:compare:left_folder';
  const LS_RIGHT_FOLDER = 'otb:compare:right_folder';
  const LS_SCAN_MODE = 'otb:compare:scan_mode';
  const LS_IGNORE_EXTS = 'otb:compare:ignore_exts';

  function loadIgnore() {
    try {
      const raw = localStorage.getItem(LS_IGNORE);
      if (!raw) return { trim_space: true, ignore_blank: false, ignore_case: false };
      const v = JSON.parse(raw);
      return {
        trim_space: v.trim_space !== false,
        ignore_blank: !!v.ignore_blank,
        ignore_case: !!v.ignore_case,
      };
    } catch (e) {
      return { trim_space: true, ignore_blank: false, ignore_case: false };
    }
  }
  function saveIgnore(v) {
    try { localStorage.setItem(LS_IGNORE, JSON.stringify(v)); } catch (e) {}
  }
  function loadMode() {
    try {
      const m = localStorage.getItem(LS_MODE);
      if (m === 'unified' || m === 'side' || m === 'changes') return m;
    } catch (e) {}
    return 'unified';
  }
  function saveMode(m) {
    try { localStorage.setItem(LS_MODE, m); } catch (e) {}
  }
  function loadHideEqual() {
    try { return localStorage.getItem(LS_HIDE_EQUAL) === '1'; } catch (e) { return false; }
  }
  function saveHideEqual(v) {
    try { localStorage.setItem(LS_HIDE_EQUAL, v ? '1' : '0'); } catch (e) {}
  }
  function loadFolderPath(side) {
    try {
      return localStorage.getItem(side === 'left' ? LS_LEFT_FOLDER : LS_RIGHT_FOLDER) || '';
    } catch (e) { return ''; }
  }
  function saveFolderPath(side, path) {
    try {
      if (path) localStorage.setItem(side === 'left' ? LS_LEFT_FOLDER : LS_RIGHT_FOLDER, path);
      else localStorage.removeItem(side === 'left' ? LS_LEFT_FOLDER : LS_RIGHT_FOLDER);
    } catch (e) {}
  }
  function loadScanMode() {
    try { return localStorage.getItem(LS_SCAN_MODE) === 'deep' ? 'deep' : 'fast'; } catch (e) { return 'fast'; }
  }
  function saveScanMode(m) {
    try { localStorage.setItem(LS_SCAN_MODE, m); } catch (e) {}
  }
  function loadIgnoreExts() {
    try { return localStorage.getItem(LS_IGNORE_EXTS) || ''; } catch (e) { return ''; }
  }
  function saveIgnoreExts(v) {
    try { localStorage.setItem(LS_IGNORE_EXTS, v); } catch (e) {}
  }

  function pickLocalFile(targetTextarea, onLoaded) {
    const input = el('input', { type: 'file' });
    input.style.display = 'none';
    document.body.appendChild(input);
    input.addEventListener('change', () => {
      const f = input.files && input.files[0];
      if (!f) { cleanup(); return; }
      if (f.size > 4 * 1024 * 1024) {
        toast('文件超过 4MB 上限：' + f.name, 'err');
        cleanup();
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        targetTextarea.value = String(reader.result || '');
        const filePath = f.path || f.name;
        if (typeof onLoaded === 'function') onLoaded(filePath);
        cleanup();
      };
      reader.onerror = () => {
        toast('读取文件失败：' + f.name, 'err');
        cleanup();
      };
      reader.readAsText(f, 'utf-8');
    });
    input.addEventListener('cancel', cleanup);
    input.click();
    function cleanup() {
      if (input.parentNode) input.parentNode.removeChild(input);
    }
  }

  function makeDropTarget(ta, fileLabelEl) {
    const overlay = el('div', { class: 'cmp-drop-hint', text: '松开以读入文件' });
    overlay.style.display = 'none';
    if (ta.parentNode) {
      ta.parentNode.insertBefore(overlay, ta);
    } else {
      queueMicrotask(() => {
        if (ta.parentNode) ta.parentNode.insertBefore(overlay, ta);
      });
    }

    let dragDepth = 0;
    ta.addEventListener('dragenter', (e) => {
      e.preventDefault();
      dragDepth++;
      overlay.style.display = 'flex';
    });
    ta.addEventListener('dragover', (e) => { e.preventDefault(); });
    ta.addEventListener('dragleave', () => {
      dragDepth = Math.max(0, dragDepth - 1);
      if (dragDepth === 0) overlay.style.display = 'none';
    });
    ta.addEventListener('drop', (e) => {
      e.preventDefault();
      dragDepth = 0;
      overlay.style.display = 'none';
      const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
      if (!f) return;
      if (f.size > 4 * 1024 * 1024) {
        toast('文件超过 4MB 上限：' + f.name, 'err');
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        ta.value = String(reader.result || '');
        const filePath = f.path || f.name;
        fileLabelEl.textContent = filePath;
        toast('已读入：' + f.name, 'ok');
      };
      reader.onerror = () => toast('读取失败：' + f.name, 'err');
      reader.readAsText(f, 'utf-8');
    });
  }

  function openDiffInNewWindow(unifiedDiff, diffTitle, stats, opts) {
    opts = opts || {};
    const mode = opts.mode || outputMode;
    const hideEq = opts.hideEqual != null ? !!opts.hideEqual : hideEqualRows;

    const win = window.open('', POPUP_NAME);
    if (!win) {
      toast('弹窗被浏览器阻止，请允许弹窗后重试', 'err');
      return null;
    }

    const theme = (document.documentElement.getAttribute('data-theme') || 'dark');
    const statsText = stats ? ('新增 ' + stats.added + ' 行·删除 ' + stats.removed + ' 行') : '';
    const primaryColor = theme === 'light' ? '#2563eb' : theme === 'hc' ? '#00ffff' : theme === 'green' ? '#3f7a3f' : '#4f8cff';
    const bgColor = theme === 'light' ? '#ffffff' : theme === 'hc' ? '#000000' : theme === 'green' ? '#fbfdf7' : '#11161f';
    const bg2Color = theme === 'light' ? '#f6f8fa' : theme === 'hc' ? '#0a0a0a' : theme === 'green' ? '#eef3e7' : '#1d2532';
    const lineColor = theme === 'light' ? '#d8dee4' : theme === 'hc' ? '#ffffff' : theme === 'green' ? '#cfd9c0' : '#232b3a';
    const textColor = theme === 'light' ? '#1f2328' : theme === 'hc' ? '#ffff00' : theme === 'green' ? '#1f2a1f' : '#e6edf3';
    const textDimColor = theme === 'light' ? '#5a6678' : theme === 'hc' ? '#ffffaa' : theme === 'green' ? '#4d5d4d' : '#8b97a8';
    const textMuteColor = theme === 'light' ? '#8b97a8' : theme === 'hc' ? '#ccc888' : theme === 'green' ? '#6b7a6b' : '#5a6678';

    const payload = {
      unified: unifiedDiff || '',
      title: diffTitle || 'left vs right',
      stats: stats || null,
      mode: mode,
      hideEqual: hideEq,
    };
    const payloadJson = JSON.stringify(payload);
    const payloadEscaped = payloadJson.replace(/</g, '\\u003c');
    const payloadJsLiteral = JSON.stringify(payloadEscaped);

    win.document.write('<!DOCTYPE html><html lang="zh-CN" data-theme="' + theme + '"><head><meta charset="utf-8"><title>Diff ' + (diffTitle || 'left vs right') + '</title>'
      + '<link rel="stylesheet" href="/static/vendor/diff2html.min.css">'
      + '<style>'
      + '*{box-sizing:border-box;margin:0;padding:0}'
      + 'html,body{height:100%;font-family:ui-monospace,SFMono-Regular,"Cascadia Mono",Menlo,Consolas,monospace;font-size:13px;background:' + bgColor + ';color:' + textColor + ';}'
      + '.diff-toolbar{position:sticky;top:0;z-index:100;display:flex;align-items:center;gap:12px;padding:8px 16px;background:' + bg2Color + ';border-bottom:1px solid ' + lineColor + ';flex-wrap:wrap;}'
      + '.diff-toolbar .diff-files{font-weight:600;font-size:13px;}'
      + '.diff-toolbar .diff-stats-info{color:' + textDimColor + ';font-size:12px;}'
      + '.diff-toolbar .diff-nav-btns{display:flex;gap:4px;margin:0 auto;}'
      + '.diff-toolbar .diff-nav-btns button,.diff-toolbar .diff-actions button{background:' + bgColor + ';border:1px solid ' + lineColor + ';color:' + textColor + ';padding:4px 10px;border-radius:6px;cursor:pointer;font-size:12px;font-family:inherit;}'
      + '.diff-toolbar .diff-nav-btns button:hover,.diff-toolbar .diff-actions button:hover{border-color:' + primaryColor + ';color:' + primaryColor + '}'
      + '.diff-toolbar .diff-actions{display:flex;gap:4px;}'
      + '.diff-toolbar .diff-actions .btn-close{color:' + (theme === 'hc' ? '#ff5555' : '#ef4444') + ';}'
      + '.diff-body{padding:0;overflow:auto;}'
      + '.diff-body .d2h-wrapper{margin:0;}'
      + '.diff-body .d2h-file-header{display:none!important;}'
      + '.diff-body .d2h-code-line,.diff-body .d2h-code-side-line{font-family:ui-monospace,SFMono-Regular,"Cascadia Mono",Menlo,Consolas,monospace!important;font-size:12.5px!important;}'
      + '.diff-current-hunk{outline:2px solid ' + primaryColor + '!important;outline-offset:-2px;}'
      + '.diff-kbd{display:inline-block;padding:1px 5px;border:1px solid ' + lineColor + ';border-radius:3px;font-size:11px;background:' + bgColor + ';color:' + textMuteColor + ';margin:0 1px;}'
      + '</style></head><body>'
      + '<div class="diff-toolbar">'
      + '<div><span class="diff-files">' + (diffTitle || 'left vs right') + '</span>'
      + '<span class="diff-stats-info" id="diffStatsInfo">' + (statsText || '') + '</span></div>'
      + '<div class="diff-nav-btns">'
      + '<button id="btnPrev" title="上一处差异 (P/↑)">&uarr; 上一处 <span class="diff-kbd">P</span></button>'
      + '<button id="btnNext" title="下一处差异 (N/↓)">&darr; 下一处 <span class="diff-kbd">N</span></button>'
      + '<span id="diffNavPos" style="color:' + textMuteColor + ';font-size:12px;align-self:center;min-width:60px;text-align:center;">-</span>'
      + '</div>'
      + '<div class="diff-actions">'
      + '<button id="btnCopy" title="复制 diff">复制 diff</button>'
      + '<button id="btnDownload" title="下载 .diff 文件">下载 .diff</button>'
      + '<button id="btnClose" class="btn-close" title="关闭窗口 (Esc)">关闭</button>'
      + '</div>'
      + '</div>'
      + '<div class="diff-body" id="diffBody"></div>'
      + '<script>(function() {'
      + 'window.PAYLOAD_JSON = ' + payloadJsLiteral + ';'
      + 'var PAYLOAD_JSON = window.PAYLOAD_JSON;'
      + 'var TITLE_FALLBACK = ' + JSON.stringify(diffTitle || 'left vs right') + ';'
      + 'function decodePayload() {'
      + '  try {'
      + '    return JSON.parse(PAYLOAD_JSON);'
      + '  } catch (e) {'
      + '    return { unified: "", title: TITLE_FALLBACK, stats: null, mode: "unified", hideEqual: false };'
      + '  }'
      + '}'
      + 'function escapeHtml(s) {'
      + '  var d = document.createElement("div");'
      + '  d.textContent = s;'
      + '  return d.innerHTML;'
      + '}'
      + 'function showToast(msg) {'
      + '  var t = document.createElement("div");'
      + '  t.textContent = msg;'
      + '  t.style.cssText = "position:fixed;bottom:20px;right:20px;background:rgba(0,0,0,0.8);color:#fff;padding:8px 14px;border-radius:6px;font-size:13px;z-index:9999;";'
      + '  document.body.appendChild(t);'
      + '  setTimeout(function () { if (t.parentNode) t.parentNode.removeChild(t); }, 1500);'
      + '}'
      + 'function renderUnifiedFromText(unified) {'
      + '  return `<pre style="padding:16px;white-space:pre-wrap;word-break:break-all;">${escapeHtml(unified)}</pre>`;'
      + '}'
      + 'function renderChanges(unified) {'
      + '  var lines = unified.split("\\n");'
      + '  var html = `<pre class="cmp-changes-only" style="padding:12px;line-height:1.6;font-size:12.5px;">`;'
      + '  for (var i = 0; i < lines.length; i++) {'
      + '    var ln = lines[i];'
      + '    var ch = ln.charAt(0);'
      + '    var color = "#e6edf3";'
      + '    var bg = "transparent";'
      + '    if (ch === "+") { color = "#a3e3a3"; bg = "rgba(46,160,67,0.15)"; }'
      + '    else if (ch === "-") { color = "#ffa198"; bg = "rgba(248,81,73,0.15)"; }'
      + '    else if (ch === "@") { color = "#79c0ff"; }'
      + '    html += `<div style="background:${bg};color:${color};padding:0 8px;">${escapeHtml(ln)}</div>`;'
      + '  }'
      + '  html += "</pre>";'
      + '  return html;'
      + '}'
      + 'function collectDiffRows() {'
      + '  diffRows = [];'
      + '  var all = document.querySelectorAll(".d2h-ins, .d2h-del, .d2h-cntx.d2h-change, .d2h-info");'
      + '  var seenTrs = new Set();'
      + '  all.forEach(function (el) {'
      + '    var tr = el.closest("tr");'
      + '    if (tr && !seenTrs.has(tr)) {'
      + '      var isChanged = tr.querySelector(".d2h-ins, .d2h-del");'
      + '      if (isChanged) { seenTrs.add(tr); diffRows.push(tr); }'
      + '    }'
      + '  });'
      + '}'
      + 'function highlightCurrent() {'
      + '  document.querySelectorAll(".diff-current-hunk").forEach(function (el) { el.classList.remove("diff-current-hunk"); });'
      + '  if (currentIdx >= 0 && currentIdx < diffRows.length) {'
      + '    var tr = diffRows[currentIdx];'
      + '    tr.querySelectorAll("td").forEach(function (td) { td.classList.add("diff-current-hunk"); });'
      + '    tr.scrollIntoView({ behavior: "smooth", block: "center" });'
      + '  }'
      + '}'
      + 'function updateNavPos() {'
      + '  var pos = document.getElementById("diffNavPos");'
      + '  if (!pos) return;'
      + '  if (diffRows.length === 0) pos.textContent = "无差异";'
      + '  else pos.textContent = (currentIdx + 1) + " / " + diffRows.length;'
      + '}'
      + 'function nextDiff() {'
      + '  if (diffRows.length === 0) return;'
      + '  currentIdx = (currentIdx + 1) % diffRows.length;'
      + '  highlightCurrent(); updateNavPos();'
      + '}'
      + 'function prevDiff() {'
      + '  if (diffRows.length === 0) return;'
      + '  currentIdx = (currentIdx - 1 + diffRows.length) % diffRows.length;'
      + '  highlightCurrent(); updateNavPos();'
      + '}'
      + 'function copyDiff() {'
      + '  if (navigator.clipboard && navigator.clipboard.writeText) {'
      + '    navigator.clipboard.writeText(data.unified).then(function () { showToast("已复制"); }, function () { showToast("复制失败"); });'
      + '  }'
      + '}'
      + 'function downloadDiff() {'
      + '  var blob = new Blob([data.unified], { type: "text/plain;charset=utf-8" });'
      + '  var url = URL.createObjectURL(blob);'
      + '  var a = document.createElement("a");'
      + '  a.href = url;'
      + '  a.download = "diff_" + Date.now() + ".diff";'
      + '  document.body.appendChild(a);'
      + '  a.click();'
      + '  setTimeout(function () { URL.revokeObjectURL(url); if (a.parentNode) a.parentNode.removeChild(a); }, 0);'
      + '}'
      + 'var data = decodePayload();'
      + 'var diffRows = [];'
      + 'var currentIdx = -1;'
      + 'function init() {'
      + '  var body = document.getElementById("diffBody");'
      + '  var fmt = (data.mode === "side") ? "side-by-side" : "line-by-line";'
      + '  var d2h = (typeof window.Diff2Html !== "undefined") ? window.Diff2Html : null;'
      + '  if (!d2h || data.mode === "changes") {'
      + '    body.innerHTML = (data.mode === "changes") ? renderChanges(data.unified) : renderUnifiedFromText(data.unified);'
      + '    updateNavPos();'
      + '    return;'
      + '  }'
      + '  try {'
      + '    var html = d2h.html(data.unified, {'
      + '      outputFormat: fmt,'
      + '      drawFileList: false,'
      + '      matching: "lines",'
      + '      renderNothingWhenEmpty: false,'
      + '      hideEqualRows: !!data.hideEqual'
      + '    });'
      + '    body.innerHTML = html;'
      + '    collectDiffRows();'
      + '    if (diffRows.length > 0) { currentIdx = 0; highlightCurrent(); }'
      + '    updateNavPos();'
      + '  } catch (e) {'
      + '    body.innerHTML = `<pre style="padding:16px;color:#f85149;">d2h 渲染失败: ${e.message || e}</pre><pre style="padding:16px;white-space:pre-wrap;">${escapeHtml(data.unified)}</pre>`;'
      + '    updateNavPos();'
      + '  }'
      + '}'
      + 'function boot() {'
      + '  if (typeof window.Diff2Html !== "undefined") { init(); return; }'
      + '  var tries = 0;'
      + '  var t = setInterval(function () {'
      + '    if (typeof window.Diff2Html !== "undefined" || tries++ > 40) {'
      + '      clearInterval(t);'
      + '      init();'
      + '    }'
      + '  }, 25);'
      + '}'
      + 'document.getElementById("btnPrev").addEventListener("click", prevDiff);'
      + 'document.getElementById("btnNext").addEventListener("click", nextDiff);'
      + 'document.getElementById("btnCopy").addEventListener("click", copyDiff);'
      + 'document.getElementById("btnDownload").addEventListener("click", downloadDiff);'
      + 'document.getElementById("btnClose").addEventListener("click", function () { window.close(); });'
      + 'document.addEventListener("keydown", function (e) {'
      + '  if (e.key === "Escape") { window.close(); return; }'
      + '  if (e.target && (e.target.tagName === "INPUT" || e.target.tagName === "TEXTAREA")) return;'
      + '  if (e.key === "n" || e.key === "N" || e.key === "ArrowDown") { e.preventDefault(); nextDiff(); }'
      + '  else if (e.key === "p" || e.key === "P" || e.key === "ArrowUp") { e.preventDefault(); prevDiff(); }'
      + '});'
      + 'if (document.readyState === "loading") {'
      + '  document.addEventListener("DOMContentLoaded", boot);'
      + '} else {'
      + '  boot();'
      + '}'
      + '})();<' + '/script>'
      + '<script src="/static/vendor/diff2html.min.js" async onerror="document.getElementById(\'diffBody\').innerHTML=\'<pre style=&quot;padding:16px;&quot;>diff2html.min.js 加载失败，请检查 /static/vendor/ 目录</pre>\'"><' + '/script>'
      + '</body></html>');
    win.document.close();
    return win;
  }

  async function doCompare(leftTa, rightTa) {
    const left = leftTa.value || '';
    const right = rightTa.value || '';
    if (!left && !right) { toast('左右都为空，没法比对', 'warn'); return; }
    const ignore = loadIgnore();
    try {
      const r = await api('POST', '/api/diff/compare', {
        left: left,
        right: right,
        left_label: 'left',
        right_label: 'right',
        ignore: ignore,
      });
      lastResult = r;
      openDiffInNewWindow(r.unified_diff || '', 'left vs right', r.stats, {
        mode: outputMode,
        hideEqual: hideEqualRows,
      });
    } catch (e) {
      toast('比对失败：' + (e.message || e), 'err');
    }
  }

  function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise((resolve, reject) => {
      const ta = el('textarea', { style: 'position:fixed;left:-9999px' });
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      try {
        const ok = document.execCommand('copy');
        document.body.removeChild(ta);
        ok ? resolve() : reject(new Error('execCommand 返回 false'));
      } catch (e) {
        document.body.removeChild(ta);
        reject(e);
      }
    });
  }
  function downloadAsFile(text, filename) {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = el('a', { href: url, download: filename });
    document.body.appendChild(a);
    a.click();
    setTimeout(() => {
      URL.revokeObjectURL(url);
      if (a.parentNode) a.parentNode.removeChild(a);
    }, 0);
  }
  function makeDiffFilename(leftLabel, rightLabel) {
    const stamp = new Date().toISOString().replace(/[:.]/g, '-').replace(/T/, '_').slice(0, 19);
    const safe = (s) => (s || '').replace(/[^a-zA-Z0-9_.\-]/g, '_').slice(-32) || 'side';
    return 'diff_' + safe(leftLabel) + '_vs_' + safe(rightLabel) + '_' + stamp + '.diff';
  }

  function getStatusIcon(status) {
    switch (status) {
      case 'same': return '✓';
      case 'different': return '≠';
      case 'suspect': return '?';
      case 'left_only': return '<';
      case 'right_only': return '>';
      default: return '  ';
    }
  }

  function getStatusColor(status) {
    switch (status) {
      case 'same': return '#22c55e';
      case 'different': return '#f97316';
      case 'suspect': return '#eab308';
      case 'left_only': return '#ef4444';
      case 'right_only': return '#3b82f6';
      default: return 'var(--text-dim)';
    }
  }

  function formatSize(bytes) {
    if (bytes == null) return '';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  }

  function formatTime(ts) {
    if (!ts) return '';
    try {
      const d = new Date(ts * 1000);
      const pad = n => String(n).padStart(2, '0');
      return pad(d.getMonth()+1) + '-' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
    } catch (e) { return ''; }
  }

  function isEntryVisible(entry, sideEntries) {
    if (entry.depth === 0) return true;
    const pathParts = entry.path.split('/');
    for (let d = entry.depth - 1; d >= 0; d--) {
      const parentPath = pathParts.slice(0, d + 1).join('/');
      if (!folderExpanded.has(parentPath)) return false;
    }
    return true;
  }

  function setAllExpanded(expanded) {
    folderExpanded = new Set();
    if (expanded && folderScanResult) {
      const allDirs = new Set();
      folderScanResult.left.tree.forEach(e => { if (e.is_dir) allDirs.add(e.rel_path); });
      folderScanResult.right.tree.forEach(e => { if (e.is_dir) allDirs.add(e.rel_path); });
      allDirs.forEach(d => folderExpanded.add(d));
    }
  }

  function buildTreeEntries(entries, side) {
    const map = new Map();
    entries.forEach(e => {
      map.set(e.rel_path, e);
    });
    const result = [];
    const allPaths = new Set();
    entries.forEach(e => {
      const parts = e.rel_path.split('/');
      for (let i = 1; i <= parts.length; i++) {
        allPaths.add(parts.slice(0, i).join('/'));
      }
    });
    const sortedPaths = Array.from(allPaths).sort((a, b) => {
      const aIsDir = entries.some(e => e.rel_path === a && e.is_dir) || !entries.some(e => e.rel_path === a);
      const bIsDir = entries.some(e => e.rel_path === b && e.is_dir) || !entries.some(e => e.rel_path === b);
      if (aIsDir && !bIsDir) return -1;
      if (!aIsDir && bIsDir) return 1;
      return a.localeCompare(b);
    });
    sortedPaths.forEach(relPath => {
      const parts = relPath.split('/');
      const name = parts[parts.length - 1];
      const depth = parts.length - 1;
      const entry = map.get(relPath);
      let isDir = true;
      let size = 0;
      let mtime = 0;
      let status = 'same';
      let fullPath = '';
      if (entry) {
        isDir = entry.is_dir;
        size = entry.size;
        mtime = entry.mtime;
        status = entry.status;
        fullPath = entry.path;
      } else {
        const otherSide = side === 'left' ? 'right' : 'left';
        const otherMap = new Map();
        (folderScanResult[otherSide].tree || []).forEach(e => otherMap.set(e.rel_path, e));
        const otherEntry = otherMap.get(relPath);
        if (otherEntry) {
          isDir = otherEntry.is_dir;
          size = otherEntry.size;
          mtime = otherEntry.mtime;
          status = otherEntry.status;
          fullPath = otherEntry.path;
        }
      }
      result.push({
        name,
        path: relPath,
        depth,
        isDir,
        size,
        mtime,
        status,
        fullPath,
      });
    });
    return result;
  }

  function renderFolderTree(container) {
    if (!container || !folderScanResult) return;
    container.innerHTML = '';

    let leftEntries = buildTreeEntries(folderScanResult.left.tree, 'left');
    let rightEntries = buildTreeEntries(folderScanResult.right.tree, 'right');

    if (folderFilter) {
      const q = folderFilter.toLowerCase();
      const filterEntries = (entries) => {
        const matchedPaths = new Set();
        entries.forEach(e => {
          if (e.name.toLowerCase().includes(q) || e.path.toLowerCase().includes(q)) {
            const parts = e.path.split('/');
            for (let d = 1; d <= parts.length; d++) {
              matchedPaths.add(parts.slice(0, d).join('/'));
            }
          }
        });
        return entries.filter(e => matchedPaths.has(e.path));
      };
      leftEntries = filterEntries(leftEntries);
      rightEntries = filterEntries(rightEntries);
      leftEntries.forEach(e => { if (e.isDir) folderExpanded.add(e.path); });
      rightEntries.forEach(e => { if (e.isDir) folderExpanded.add(e.path); });
    }

    if (folderShowOnlyDiff) {
      const hasDiffChild = (entries, entry) => {
        if (!entry.isDir) return entry.status === 'different' || entry.status === 'suspect' || entry.status === 'left_only' || entry.status === 'right_only';
        const prefix = entry.path + '/';
        return entries.some(e => !e.isDir && e.path.startsWith(prefix) && (e.status === 'different' || e.status === 'suspect' || e.status === 'left_only' || e.status === 'right_only'));
      };
      leftEntries = leftEntries.filter(e => e.isDir ? hasDiffChild(leftEntries, e) : (e.status === 'different' || e.status === 'suspect' || e.status === 'left_only' || e.status === 'right_only'));
      rightEntries = rightEntries.filter(e => e.isDir ? hasDiffChild(rightEntries, e) : (e.status === 'different' || e.status === 'suspect' || e.status === 'left_only' || e.status === 'right_only'));
    }

    const leftPanel = el('div', { class: 'cmp-tree-panel', style: 'flex:1; overflow:auto;' });
    const rightPanel = el('div', { class: 'cmp-tree-panel', style: 'flex:1; overflow:auto;' });

    const renderNode = (entry, panel, side) => {
      const isMissing = (side === 'left' && entry.status === 'right_only') || (side === 'right' && entry.status === 'left_only');
      const isSuspect = entry.status === 'suspect';
      let bgStyle = '';
      if (entry.status === 'different' && !entry.isDir) bgStyle = 'background:rgba(249,115,22,0.08);';
      else if (isSuspect && !entry.isDir) bgStyle = 'background:rgba(234,179,8,0.10);';
      else if (entry.status === 'left_only' && side === 'left' && !entry.isDir) bgStyle = 'background:rgba(239,68,68,0.08);';
      else if (entry.status === 'right_only' && side === 'right' && !entry.isDir) bgStyle = 'background:rgba(59,130,246,0.08);';

      const canClick = entry.isDir || (!isMissing && !entry.isDir && entry.status !== 'same');
      const row = el('div', {
        class: 'cmp-tree-row',
        style: 'display:flex; align-items:center; padding:2px 8px; cursor:' + (canClick ? 'pointer' : 'default') + '; font-size:12.5px; line-height:24px; white-space:nowrap; user-select:none;' +
          'padding-left:' + (entry.depth * 16 + 8) + 'px;' +
          (isMissing ? 'opacity:0.3;' : '') +
          bgStyle,
      });

      if (entry.isDir) {
        const expanded = folderExpanded.has(entry.path);
        const arrow = el('span', { style: 'width:14px; display:inline-block; text-align:center; color:var(--text-dim); font-size:9px; flex-shrink:0;', text: expanded ? '▼' : '▶' });
        const icon = el('span', { style: 'margin-right:4px;', text: '📁' });
        const nameEl = el('span', { style: 'flex:1; overflow:hidden; text-overflow:ellipsis; font-weight:500;', text: entry.name });
        row.appendChild(arrow);
        row.appendChild(icon);
        row.appendChild(nameEl);
        row.addEventListener('click', (e) => {
          e.stopPropagation();
          if (folderExpanded.has(entry.path)) folderExpanded.delete(entry.path);
          else folderExpanded.add(entry.path);
          renderFolderTree(container);
        });
      } else {
        if (isMissing) {
          row.appendChild(el('span', { style: 'width:14px; flex-shrink:0;' }));
          row.appendChild(el('span', { style: 'width:20px; flex-shrink:0;' }));
          row.appendChild(el('span', { style: 'flex:1;', text: '' }));
        } else {
          if (isSuspect) {
            const cb = el('input', { type: 'checkbox', style: 'width:14px; flex-shrink:0; margin-right:2px;' });
            cb.checked = selectedSuspects.has(entry.path);
            cb.title = '勾选后深度校验此文件';
            cb.addEventListener('click', (e) => {
              e.stopPropagation();
              if (cb.checked) selectedSuspects.add(entry.path);
              else selectedSuspects.delete(entry.path);
              updateDeepCheckButton();
            });
            row.appendChild(cb);
          } else {
            row.appendChild(el('span', { style: 'width:16px; flex-shrink:0;' }));
          }
          const statusDot = el('span', { style: 'width:16px; display:inline-block; text-align:center; flex-shrink:0; font-size:12px; color:' + getStatusColor(entry.status) + '; font-weight:bold;', text: getStatusIcon(entry.status) });
          if (isSuspect) {
            statusDot.title = '大小相同但修改时间不同，需深度校验';
          }
          const icon = el('span', { style: 'margin-right:4px;', text: '📄' });
          const nameEl = el('span', { style: 'flex:1; overflow:hidden; text-overflow:ellipsis; font-family:ui-monospace,monospace;', text: entry.name });
          const sizeEl = el('span', { style: 'margin-left:8px; color:var(--text-dim); font-size:11px; flex-shrink:0; min-width:60px; text-align:right;', text: formatSize(entry.size) });
          row.appendChild(statusDot);
          row.appendChild(icon);
          row.appendChild(nameEl);
          row.appendChild(sizeEl);
          if (entry.status === 'different') {
            row.addEventListener('click', (e) => {
              e.stopPropagation();
              viewFileDiff(entry.path);
            });
          } else if (entry.status === 'suspect') {
            row.title = '点击或勾选后深度校验';
            row.addEventListener('click', (e) => {
              e.stopPropagation();
              selectedSuspects.clear();
              selectedSuspects.add(entry.path);
              doDeepCheckSelected();
            });
          } else if (entry.status === 'same') {
            row.style.cursor = 'default';
          }
        }
      }
      panel.appendChild(row);
    };

    const visibleLeft = leftEntries.filter(e => isEntryVisible(e, leftEntries));
    const visibleRight = rightEntries.filter(e => isEntryVisible(e, rightEntries));
    visibleLeft.forEach(e => renderNode(e, leftPanel, 'left'));
    visibleRight.forEach(e => renderNode(e, rightPanel, 'right'));

    const leftCount = folderScanResult.left.tree.filter(e => !e.is_dir).length;
    const rightCount = folderScanResult.right.tree.filter(e => !e.is_dir).length;
    const diffObj = folderScanResult.diff || {};
    const sameCount = (diffObj.same || []).length;
    const diffCount = (diffObj.different || []).length;
    const suspectCount = (diffObj.suspect || []).length;
    const leftOnlyCount = (diffObj.left_only || []).length;
    const rightOnlyCount = (diffObj.right_only || []).length;

    const leftHeader = el('div', { style: 'padding:8px 12px; border-bottom:1px solid var(--line); font-weight:600; display:flex; justify-content:space-between; align-items:center; background:var(--bg2,#1d2532); flex-shrink:0; gap:8px;' }, [
      el('span', { style: 'overflow:hidden; text-overflow:ellipsis; white-space:nowrap;', text: '📁 ' + folderScanResult.left.root }),
      el('span', { style: 'font-size:11px; color:var(--text-dim); font-weight:normal; flex-shrink:0;', text: leftCount + ' 文件' })
    ]);
    const rightHeader = el('div', { style: 'padding:8px 12px; border-bottom:1px solid var(--line); border-left:1px solid var(--line); font-weight:600; display:flex; justify-content:space-between; align-items:center; background:var(--bg2,#1d2532); flex-shrink:0; gap:8px;' }, [
      el('span', { style: 'overflow:hidden; text-overflow:ellipsis; white-space:nowrap;', text: '📁 ' + folderScanResult.right.root }),
      el('span', { style: 'font-size:11px; color:var(--text-dim); font-weight:normal; flex-shrink:0;', text: rightCount + ' 文件' })
    ]);

    const leftWrap = el('div', { style: 'display:flex; flex-direction:column; flex:1; min-width:0; height:100%;' }, [leftHeader, leftPanel]);
    const rightWrap = el('div', { style: 'display:flex; flex-direction:column; flex:1; min-width:0; height:100%;' }, [rightHeader, rightPanel]);

    const scanModeText = folderScanResult.deep_mode ? '深度模式 (MD5校验)' : '快速模式 (大小+mtime)';
    const scanTimeText = folderScanResult.scan_ms ? '耗时 ' + (folderScanResult.scan_ms < 1000 ? folderScanResult.scan_ms + 'ms' : (folderScanResult.scan_ms/1000).toFixed(1) + 's') : '';
    const legendBar = el('div', { style: 'padding:4px 12px; border-top:1px solid var(--line); font-size:11px; color:var(--text-dim); display:flex; gap:16px; align-items:center; background:var(--bg2,#1d2532); flex-shrink:0; flex-wrap:wrap;' }, [
      el('span', { style: 'color:#22c55e;', text: '✓ 相同' }),
      el('span', { style: 'color:#eab308;', text: '? 待校验' }),
      el('span', { style: 'color:#f97316;', text: '≠ 不同' }),
      el('span', { style: 'color:#ef4444;', text: '< 仅左侧' }),
      el('span', { style: 'color:#3b82f6;', text: '> 仅右侧' }),
      el('span', { style: 'margin-left:auto;', text: scanModeText + (scanTimeText ? ' · ' + scanTimeText : '') }),
    ]);

    const statBar = el('div', { style: 'padding:4px 12px; font-size:11px; color:var(--text-dim); display:flex; gap:12px; align-items:center; background:var(--bg,#11161f); flex-shrink:0; flex-wrap:wrap; border-bottom:1px solid var(--line);' }, [
      el('span', { text: '相同: ' + sameCount }),
      el('span', { style: 'color:#eab308;', text: '待校验: ' + suspectCount }),
      el('span', { style: 'color:#f97316;', text: '不同: ' + diffCount }),
      el('span', { style: 'color:#ef4444;', text: '仅左: ' + leftOnlyCount }),
      el('span', { style: 'color:#3b82f6;', text: '仅右: ' + rightOnlyCount }),
    ]);

    const treeContent = el('div', { style: 'display:flex; flex:1; min-height:0;' }, [leftWrap, rightWrap]);
    const treeWrap = el('div', { class: 'cmp-tree-container', style: 'display:flex; flex-direction:column; border:1px solid var(--line); border-radius:6px; margin-top:8px; height:500px; background:var(--bg);' }, [statBar, treeContent, legendBar]);
    container.appendChild(treeWrap);
  }

  function updateDeepCheckButton() {
    const btn = document.getElementById('btnDeepCheck');
    if (!btn) return;
    const count = selectedSuspects.size;
    btn.disabled = count === 0 || isDeepChecking;
    btn.textContent = isDeepChecking ? '校验中...' : ('深度校验选中 (' + count + ')');
  }

  async function doDeepCheckSelected() {
    if (isDeepChecking) return;
    if (selectedSuspects.size === 0) {
      toast('请先勾选要深度校验的文件（? 标记）', 'warn');
      return;
    }
    isDeepChecking = true;
    updateDeepCheckButton();

    try {
      const files = [];
      selectedSuspects.forEach(relPath => {
        const leftEntry = folderScanResult.left.tree.find(e => e.rel_path === relPath);
        const rightEntry = folderScanResult.right.tree.find(e => e.rel_path === relPath);
        if (leftEntry && rightEntry) {
          files.push({
            left_path: leftEntry.path,
            right_path: rightEntry.path,
            rel_path: relPath,
          });
        }
      });
      if (files.length === 0) {
        toast('没有可校验的文件', 'warn');
        return;
      }
      const r = await api('POST', '/api/compare/deep-check', { files });
      let updated = 0;
      for (const [relPath, result] of Object.entries(r.results || {})) {
        const leftEntry = folderScanResult.left.tree.find(e => e.rel_path === relPath);
        const rightEntry = folderScanResult.right.tree.find(e => e.rel_path === relPath);
        if (leftEntry && rightEntry) {
          const newStatus = result === 'same' ? 'same' : result === 'different' ? 'different' : 'suspect';
          if (leftEntry.status !== newStatus) {
            leftEntry.status = newStatus;
            rightEntry.status = newStatus;
            leftEntry.checked = true;
            rightEntry.checked = true;
            updated++;
            const diffObj = folderScanResult.diff;
            const suspectIdx = (diffObj.suspect || []).indexOf(relPath);
            if (suspectIdx >= 0) diffObj.suspect.splice(suspectIdx, 1);
            if (newStatus === 'same') {
              if (!diffObj.same.includes(relPath)) diffObj.same.push(relPath);
              const diffIdx = diffObj.different.indexOf(relPath);
              if (diffIdx >= 0) diffObj.different.splice(diffIdx, 1);
            } else if (newStatus === 'different') {
              if (!diffObj.different.includes(relPath)) diffObj.different.push(relPath);
              const sameIdx = diffObj.same.indexOf(relPath);
              if (sameIdx >= 0) diffObj.same.splice(sameIdx, 1);
            } else {
              if (!diffObj.suspect.includes(relPath)) diffObj.suspect.push(relPath);
            }
          }
        }
      }
      selectedSuspects.clear();
      if (updated > 0) {
        toast('深度校验完成，更新了 ' + updated + ' 个文件状态', 'ok');
      } else {
        toast('深度校验完成', 'ok');
      }
      renderFolderTree(document.getElementById('folderTreeContainer'));
    } catch (e) {
      toast('深度校验失败：' + (e.message || e), 'err');
    } finally {
      isDeepChecking = false;
      updateDeepCheckButton();
    }
  }

  async function doFolderCompare(leftPathInp, rightPathInp, progressEl, treeContainer, scanMode, ignoreExtsInp, minSizeInp, maxSizeInp) {
    const leftPath = leftPathInp.value.trim();
    const rightPath = rightPathInp.value.trim();
    if (!leftPath || !rightPath) {
      toast('请输入或选择左右两个文件夹路径', 'warn');
      return;
    }
    saveFolderPath('left', leftPath);
    saveFolderPath('right', rightPath);
    saveScanMode(scanMode);
    saveIgnoreExts(ignoreExtsInp ? ignoreExtsInp.value : '');

    folderScanResult = null;
    selectedSuspects.clear();
    treeContainer.innerHTML = '';
    isScanning = true;
    if (progressEl) progressEl.innerHTML = '<span style="color:var(--primary,#4f8cff);">⏳ 正在' + (scanMode === 'deep' ? '深度扫描（计算MD5）' : '快速扫描') + '...</span>';

    try {
      const req = {
        left_path: leftPath,
        right_path: rightPath,
        deep_check: scanMode === 'deep',
      };
      if (ignoreExtsInp && ignoreExtsInp.value.trim()) {
        req.ignore_exts = ignoreExtsInp.value.split(/[,，\s]+/).filter(s => s.trim());
      }
      if (minSizeInp && minSizeInp.value) {
        const v = parseInt(minSizeInp.value);
        if (!isNaN(v) && v > 0) req.min_size = v;
      }
      if (maxSizeInp && maxSizeInp.value) {
        const v = parseInt(maxSizeInp.value);
        if (!isNaN(v) && v > 0) req.max_size = v * 1024 * 1024;
      }
      const r = await api('POST', '/api/compare/folder-scan', req);
      folderScanResult = r;
      setAllExpanded(true);
      const fileCount = r.left.tree.filter(e => !e.is_dir).length + r.right.tree.filter(e => !e.is_dir).length;
      const diffObj = r.diff || {};
      const diffCount = (diffObj.different || []).length + (diffObj.left_only || []).length + (diffObj.right_only || []).length;
      const suspectCount = (diffObj.suspect || []).length;
      const timeStr = r.scan_ms ? (' (' + (r.scan_ms < 1000 ? r.scan_ms + 'ms' : (r.scan_ms/1000).toFixed(1) + 's') + ')') : '';
      if (progressEl) {
        let msg = '扫描完成' + timeStr + '：共 ' + fileCount + ' 个文件，不同 ' + diffCount + ' 个';
        if (suspectCount > 0 && !r.deep_mode) {
          msg += '，<span style="color:#eab308;">' + suspectCount + ' 个待校验</span>';
        }
        if (r.truncated) {
          msg += ' <span style="color:#f97316;">(文件过多已截断)</span>';
        }
        progressEl.innerHTML = msg;
      }
      renderFolderTree(treeContainer);
    } catch (e) {
      if (progressEl) progressEl.textContent = '扫描失败';
      toast('扫描失败：' + (e.message || e), 'err');
    } finally {
      isScanning = false;
    }
  }

  async function viewFileDiff(relPath) {
    if (!folderScanResult) return;
    const leftEntry = folderScanResult.left.tree.find(e => e.rel_path === relPath);
    const rightEntry = folderScanResult.right.tree.find(e => e.rel_path === relPath);
    const leftPath = leftEntry ? leftEntry.path : '';
    const rightPath = rightEntry ? rightEntry.path : '';
    if (!leftPath || !rightPath) {
      toast('无法比对：一侧缺失该文件', 'warn');
      return;
    }
    try {
      const r = await api('POST', '/api/compare/file-diff', {
        left_path: leftPath,
        right_path: rightPath,
      });
      openDiffInNewWindow(r.unified || '', relPath, null, {
        mode: outputMode,
        hideEqual: hideEqualRows,
      });
    } catch (e) {
      toast('获取diff失败：' + (e.message || e), 'err');
    }
  }

  function renderCompare(view) {
    folderScanResult = null;
    folderExpanded = new Set();
    selectedSuspects = new Set();
    const ignore = loadIgnore();
    outputMode = loadMode();
    hideEqualRows = loadHideEqual();
    const scanMode = loadScanMode();
    const savedIgnoreExts = loadIgnoreExts();

    const modeTabBar = el('div', { class: 'cmp-mode-tabs' }, [
      el('button', {
        class: 'btn cmp-mode-tab active',
        'data-mode': 'text',
        text: '文本比对',
        onclick: function () { switchMode('text'); },
      }),
      el('button', {
        class: 'btn cmp-mode-tab',
        'data-mode': 'folder',
        text: '文件夹比对',
        onclick: function () { switchMode('folder'); },
      }),
    ]);

    const leftTa = el('textarea', {
      id: 'cmp-left',
      placeholder: '左侧文本：粘贴代码 / 配置 / 报文，或拖入本地文件 / 点下方"选择本地文件"读入…',
      spellcheck: 'false',
    });
    leftTa.dataset.cmpSide = 'left';
    const rightTa = el('textarea', {
      id: 'cmp-right',
      placeholder: '右侧文本：同上',
      spellcheck: 'false',
    });
    rightTa.dataset.cmpSide = 'right';
    leftTa.style.minHeight = '260px';
    rightTa.style.minHeight = '260px';

    const leftFileLabel = el('span', { class: 'muted', text: '未选择文件' });
    const rightFileLabel = el('span', { class: 'muted', text: '未选择文件' });

    const btnPickLeft = el('button', {
      class: 'btn',
      text: '选择本地文件',
      onclick: () => pickLocalFile(leftTa, (filePath) => { leftFileLabel.textContent = filePath; }),
    });
    const btnPickRight = el('button', {
      class: 'btn',
      text: '选择本地文件',
      onclick: () => pickLocalFile(rightTa, (filePath) => { rightFileLabel.textContent = filePath; }),
    });
    const btnClearLeft = el('button', {
      class: 'btn btn-sm',
      text: '清空',
      title: '清空左栏',
      style: 'border-radius:6px;',
      onclick: () => {
        leftTa.value = '';
        leftFileLabel.textContent = '未选择文件';
      },
    });
    const btnClearRight = el('button', {
      class: 'btn btn-sm',
      text: '清空',
      title: '清空右栏',
      style: 'border-radius:6px;',
      onclick: () => {
        rightTa.value = '';
        rightFileLabel.textContent = '未选择文件';
      },
    });

    const cbTrim = el('input', { type: 'checkbox' }); cbTrim.checked = ignore.trim_space;
    const cbBlank = el('input', { type: 'checkbox' }); cbBlank.checked = ignore.ignore_blank;
    const cbCase = el('input', { type: 'checkbox' }); cbCase.checked = ignore.ignore_case;
    function syncIgnore() {
      saveIgnore({
        trim_space: cbTrim.checked,
        ignore_blank: cbBlank.checked,
        ignore_case: cbCase.checked,
      });
    }
    cbTrim.addEventListener('change', syncIgnore);
    cbBlank.addEventListener('change', syncIgnore);
    cbCase.addEventListener('change', syncIgnore);

    const rdoUnified = el('input', { type: 'radio', name: 'cmp-mode', value: 'unified' });
    const rdoChanges = el('input', { type: 'radio', name: 'cmp-mode', value: 'changes' });
    const rdoSide = el('input', { type: 'radio', name: 'cmp-mode', value: 'side' });
    rdoUnified.checked = outputMode === 'unified';
    rdoChanges.checked = outputMode === 'changes';
    rdoSide.checked = outputMode === 'side';
    const cbHideEqual = el('input', { type: 'checkbox' });
    cbHideEqual.checked = hideEqualRows;
    const cbHideEqualWrap = el('label', { class: 'cmp-opt', style: 'display:flex; align-items:center; gap:4px;' }, [cbHideEqual, document.createTextNode(' 只看左右不同')]);
    function syncMode() {
      const checked = document.querySelector('input[name="cmp-mode"]:checked');
      if (checked) {
        outputMode = checked.value;
        saveMode(outputMode);
        cbHideEqualWrap.style.display = (outputMode === 'side') ? '' : 'none';
      }
    }
    rdoUnified.addEventListener('change', syncMode);
    rdoChanges.addEventListener('change', syncMode);
    rdoSide.addEventListener('change', syncMode);
    cbHideEqual.addEventListener('change', () => {
      hideEqualRows = cbHideEqual.checked;
      saveHideEqual(hideEqualRows);
    });
    cbHideEqualWrap.style.display = (outputMode === 'side') ? '' : 'none';

    const btnSwap = el('button', { class: 'btn', text: '交换左右', onclick: () => {
      const lt = leftTa.value, rt = rightTa.value;
      const ll = leftFileLabel.textContent, rl = rightFileLabel.textContent;
      leftTa.value = rt; rightTa.value = lt;
      leftFileLabel.textContent = (rl === '未选择文件' ? '未选择文件' : rl);
      rightFileLabel.textContent = (ll === '未选择文件' ? '未选择文件' : ll);
    }});
    const btnClear = el('button', { class: 'btn', text: '清空全部', onclick: () => {
      leftTa.value = ''; rightTa.value = '';
      leftFileLabel.textContent = '未选择文件';
      rightFileLabel.textContent = '未选择文件';
      lastResult = null;
    }});
    const btnCopy = el('button', { class: 'btn', text: '复制 diff', onclick: () => {
      if (!lastResult || !lastResult.unified_diff) { toast('暂无可复制的 diff', 'warn'); return; }
      copyToClipboard(lastResult.unified_diff).then(
        () => toast('已复制 unified diff 到剪贴板', 'ok'),
        () => toast('复制失败', 'err')
      );
    }});
    const btnDownload = el('button', { class: 'btn', text: '下载 .diff 文件', onclick: () => {
      if (!lastResult || !lastResult.unified_diff) { toast('暂无可下载的 diff', 'warn'); return; }
      const fname = makeDiffFilename(leftFileLabel.textContent, rightFileLabel.textContent);
      downloadAsFile(lastResult.unified_diff, fname);
    }});
    const btnGo = el('button', { class: 'btn btn-primary', text: '开始比对', onclick: () => doCompare(leftTa, rightTa) });

    [leftTa, rightTa].forEach((ta) => {
      ta.addEventListener('keydown', (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
          e.preventDefault();
          doCompare(leftTa, rightTa);
        }
      });
    });

    const leftFolderPathInp = el('input', {
      type: 'text',
      placeholder: '输入或选择文件夹路径',
      style: 'flex:1; min-width:200px;',
    });
    leftFolderPathInp.value = loadFolderPath('left');
    const rightFolderPathInp = el('input', {
      type: 'text',
      placeholder: '输入或选择文件夹路径',
      style: 'flex:1; min-width:200px;',
    });
    rightFolderPathInp.value = loadFolderPath('right');

    const rdoFast = el('input', { type: 'radio', name: 'folder-scan-mode', value: 'fast' });
    const rdoDeep = el('input', { type: 'radio', name: 'folder-scan-mode', value: 'deep' });
    rdoFast.checked = scanMode !== 'deep';
    rdoDeep.checked = scanMode === 'deep';
    function syncScanMode() {
      const checked = document.querySelector('input[name="folder-scan-mode"]:checked');
      if (checked) saveScanMode(checked.value);
    }
    rdoFast.addEventListener('change', syncScanMode);
    rdoDeep.addEventListener('change', syncScanMode);

    const ignoreExtsInp = el('input', {
      type: 'text',
      placeholder: '忽略扩展名: log,tmp,bak',
      style: 'max-width:180px; font-size:12px;',
      value: savedIgnoreExts,
    });

    const cbOnlyDiff = el('input', { type: 'checkbox' });
    cbOnlyDiff.checked = folderShowOnlyDiff;
    cbOnlyDiff.addEventListener('change', () => {
      folderShowOnlyDiff = cbOnlyDiff.checked;
      renderFolderTree(folderTreeContainer);
    });

    const folderSearchInput = el('input', {
      type: 'text',
      placeholder: '搜索文件名...',
      style: 'max-width:200px;',
    });
    folderSearchInput.addEventListener('input', () => {
      const newFilter = folderSearchInput.value;
      if (newFilter && !folderFilter) {
        folderExpandedSnapshot = new Set(folderExpanded);
      } else if (!newFilter && folderFilter && folderExpandedSnapshot) {
        folderExpanded = folderExpandedSnapshot;
        folderExpandedSnapshot = null;
      }
      folderFilter = newFilter;
      renderFolderTree(folderTreeContainer);
    });

    const btnExpandAll = el('button', { class: 'btn btn-sm', text: '展开全部', onclick: () => { setAllExpanded(true); renderFolderTree(folderTreeContainer); } });
    const btnCollapseAll = el('button', { class: 'btn btn-sm', text: '折叠全部', onclick: () => { setAllExpanded(false); renderFolderTree(folderTreeContainer); } });

    const btnDeepCheck = el('button', {
      id: 'btnDeepCheck',
      class: 'btn btn-sm',
      text: '深度校验选中 (0)',
      disabled: true,
      style: 'color:#eab308;',
      onclick: doDeepCheckSelected,
    });

    const folderProgressEl = el('div', { class: 'muted', style: 'font-size:12px;', text: '' });
    const folderTreeContainer = el('div', { id: 'folderTreeContainer' });

    const btnChooseLeftDir = el('button', {
      class: 'btn btn-sm',
      text: '选择文件夹',
      onclick: async () => {
        try {
          const r = await api('POST', '/api/choose-dir');
          if (r && r.path) {
            leftFolderPathInp.value = r.path;
            saveFolderPath('left', r.path);
          } else {
            toast('未选择文件夹', 'warn');
          }
        } catch (e) {
          toast('选择文件夹失败：' + (e.message || e), 'err');
        }
      },
    });
    const btnChooseRightDir = el('button', {
      class: 'btn btn-sm',
      text: '选择文件夹',
      onclick: async () => {
        try {
          const r = await api('POST', '/api/choose-dir');
          if (r && r.path) {
            rightFolderPathInp.value = r.path;
            saveFolderPath('right', r.path);
          } else {
            toast('未选择文件夹', 'warn');
          }
        } catch (e) {
          toast('选择文件夹失败：' + (e.message || e), 'err');
        }
      },
    });
    const btnClearLeftFolder = el('button', {
      class: 'btn btn-sm',
      text: '清空',
      style: 'border-radius:6px;',
      onclick: () => {
        leftFolderPathInp.value = '';
        saveFolderPath('left', '');
      },
    });
    const btnClearRightFolder = el('button', {
      class: 'btn btn-sm',
      text: '清空',
      style: 'border-radius:6px;',
      onclick: () => {
        rightFolderPathInp.value = '';
        saveFolderPath('right', '');
      },
    });

    const btnFolderGo = el('button', {
      class: 'btn btn-primary',
      text: '开始比对',
      onclick: () => {
        const mode = document.querySelector('input[name="folder-scan-mode"]:checked');
        doFolderCompare(
          leftFolderPathInp, rightFolderPathInp,
          folderProgressEl, folderTreeContainer,
          mode ? mode.value : 'fast',
          ignoreExtsInp, null, null
        );
      },
    });

    const cmpOptionsStyle = 'display:flex; align-items:center; gap:8px; flex-wrap:wrap;';
    const textArea = el('div', { class: 'cmp-text-area', id: 'cmp-text-area' }, [
      el('div', { class: 'cmp-options', style: cmpOptionsStyle }, [
        el('span', { class: 'cmp-label', text: '忽略：' }),
        makeCheckbox(cbTrim, '忽略行尾空白'),
        makeCheckbox(cbBlank, '忽略空行'),
        makeCheckbox(cbCase, '忽略大小写'),
        el('span', { class: 'cmp-spacer', style: 'width:16px;' }),
        el('span', { class: 'cmp-label', text: '输出：' }),
        makeRadio(rdoUnified, 'Unified'),
        makeRadio(rdoChanges, '仅差异行'),
        makeRadio(rdoSide, 'Side-by-side'),
        cbHideEqualWrap,
      ]),
      el('div', { class: 'pane' }, [
        el('div', null, [
          el('div', { class: 'cmp-pane-header', style: 'display:flex; align-items:center; gap:8px; flex-wrap:wrap;' }, [
            el('label', { text: '左：原始' }),
            leftFileLabel,
            btnPickLeft,
            btnClearLeft,
          ]),
          leftTa,
        ]),
        el('div', null, [
          el('div', { class: 'cmp-pane-header', style: 'display:flex; align-items:center; gap:8px; flex-wrap:wrap;' }, [
            el('label', { text: '右：修改' }),
            rightFileLabel,
            btnPickRight,
            btnClearRight,
          ]),
          rightTa,
        ]),
      ]),
      el('div', { class: 'btn-row mt-3' }, [btnGo, btnSwap, btnClear, btnCopy, btnDownload]),
    ]);

    const folderArea = el('div', { class: 'cmp-folder-area', id: 'cmp-folder-area', style: 'display:none;' }, [
      el('div', { class: 'cmp-options', style: cmpOptionsStyle }, [
        el('span', { class: 'cmp-label', text: '扫描模式：' }),
        makeRadio(rdoFast, '快速（大小+时间，推荐）'),
        makeRadio(rdoDeep, '深度（MD5校验，慢）'),
        el('span', { class: 'cmp-spacer', style: 'width:16px;' }),
        el('span', { class: 'muted', style: 'font-size:11px;', text: '💡 快速模式秒出结果，? 标记的文件可点击或勾选后深度校验' }),
      ]),
      el('div', { class: 'cmp-options', style: cmpOptionsStyle + '; margin-top:4px;' }, [
        makeCheckbox(cbOnlyDiff, '只显示不同/待校验'),
        el('span', { class: 'cmp-spacer', style: 'width:8px;' }),
        ignoreExtsInp,
        el('span', { class: 'cmp-spacer', style: 'width:8px;' }),
        btnExpandAll,
        btnCollapseAll,
        btnDeepCheck,
        el('span', { class: 'cmp-spacer', style: 'width:16px;' }),
        folderSearchInput,
      ]),
      el('div', { class: 'pane', style: 'min-height:auto;' }, [
        el('div', { class: 'cmp-folder-pane' }, [
          el('div', { class: 'cmp-pane-header', style: 'display:flex; align-items:center; gap:8px; flex-wrap:wrap;' }, [
            el('label', { text: '左侧文件夹' }),
            leftFolderPathInp,
            btnChooseLeftDir,
            btnClearLeftFolder,
          ]),
        ]),
        el('div', { class: 'cmp-folder-pane' }, [
          el('div', { class: 'cmp-pane-header', style: 'display:flex; align-items:center; gap:8px; flex-wrap:wrap;' }, [
            el('label', { text: '右侧文件夹' }),
            rightFolderPathInp,
            btnChooseRightDir,
            btnClearRightFolder,
          ]),
        ]),
      ]),
      el('div', { class: 'btn-row mt-3' }, [btnFolderGo]),
      el('div', { style: 'margin-top:8px;' }, [folderProgressEl]),
      folderTreeContainer,
    ]);

    function switchMode(mode) {
      const tabs = modeTabBar.querySelectorAll('.cmp-mode-tab');
      tabs.forEach((t) => {
        t.classList.toggle('active', t.getAttribute('data-mode') === mode);
      });
      textArea.style.display = mode === 'text' ? '' : 'none';
      folderArea.style.display = mode === 'folder' ? '' : 'none';
    }

    view.appendChild(el('div', { class: 'card' }, [
      modeTabBar,
      textArea,
      folderArea,
    ]));

    makeDropTarget(leftTa, leftFileLabel);
    makeDropTarget(rightTa, rightFileLabel);
  }

  function makeCheckbox(input, label) {
    return el('label', { class: 'cmp-opt', style: 'display:flex; align-items:center; gap:4px;' }, [input, document.createTextNode(' ' + label)]);
  }
  function makeRadio(input, label) {
    return el('label', { class: 'cmp-opt', style: 'display:flex; align-items:center; gap:4px;' }, [input, document.createTextNode(' ' + label)]);
  }

  DTB.pages.compare = renderCompare;
  DTB.state.routes.compare = renderCompare;
  DTB.state.routeNames.compare = '代码比对';
})();
