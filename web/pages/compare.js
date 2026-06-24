/* ===== web/pages/compare.js =====
 * 代码比对：粘贴 / 选择本地文件，左右两栏做行级 diff。
 *
 * 渲染层用 diff2html（MIT，已嵌入 jsdiff）。
 *  - unified 模式：Diff2Html.html(unifiedDiffString, 'line-by-line')
 *  - side-by-side：Diff2Html.html(unifiedDiffString, 'side-by-side')
 *  - 仅差异行：手写（diff2html 不支持），但复用后端返回的 lines
 *
 * 后端只干一件事：行级 diff + 生成标准 unified diff 字符串。
 *
 * v2 计划：
 *   - 选两个目录挨个文件比对
 *   - 远端 SSH 文件比对
 */
(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast } = OTB.core;
  const { api } = OTB.api;

  // ---------- 模块级状态 ----------
  let lastResult = null;        // 后端 /api/diff/compare 返回
  let outputMode = 'unified';   // unified | side | changes
  let hideEqualRows = false;    // side-by-side 专属：勾选后只展示左右不同的内容
  let activeRenderResult = null;
  let d2h = null;               // Diff2Html 句柄（vendor 加载后即存在）

  const LS_IGNORE = 'otb:compare:ignore';
  const LS_MODE = 'otb:compare:mode';
  const LS_FILE_LEFT = 'otb:compare:file:left';
  const LS_FILE_RIGHT = 'otb:compare:file:right';
  const LS_HIDE_EQUAL = 'otb:compare:hideEqual';

  // ---------- localStorage ----------
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
    try { localStorage.setItem(LS_IGNORE, JSON.stringify(v)); } catch (e) { /* ignore */ }
  }
  function loadMode() {
    try {
      const m = localStorage.getItem(LS_MODE);
      if (m === 'unified' || m === 'side' || m === 'changes') return m;
    } catch (e) { /* ignore */ }
    return 'unified';
  }
  function saveMode(m) {
    try { localStorage.setItem(LS_MODE, m); } catch (e) { /* ignore */ }
  }
  function loadHideEqual() {
    try { return localStorage.getItem(LS_HIDE_EQUAL) === '1'; } catch (e) { return false; }
  }
  function saveHideEqual(v) {
    try { localStorage.setItem(LS_HIDE_EQUAL, v ? '1' : '0'); } catch (e) { /* ignore */ }
  }
  function loadFileLabel(side) {
    try {
      const v = localStorage.getItem(side === 'left' ? LS_FILE_LEFT : LS_FILE_RIGHT);
      return v || '';
    } catch (e) { return ''; }
  }
  function saveFileLabel(side, name) {
    try {
      if (name) localStorage.setItem(side === 'left' ? LS_FILE_LEFT : LS_FILE_RIGHT, name);
      else localStorage.removeItem(side === 'left' ? LS_FILE_LEFT : LS_FILE_RIGHT);
    } catch (e) { /* ignore */ }
  }

  // ---------- 本地文件读入 ----------
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
        if (typeof onLoaded === 'function') onLoaded(f.name);
        cleanup();
      };
      reader.onerror = () => {
        toast('读取文件失败：' + f.name, 'err');
        cleanup();
      };
      reader.readAsText(f, 'utf-8');
    });
    // 也支持拖拽：把拖拽文件读入同一个 textarea
    input.addEventListener('cancel', cleanup);
    input.click();
    function cleanup() {
      if (input.parentNode) input.parentNode.removeChild(input);
    }
  }

  // 给 textarea 增加拖拽支持（拖文件到 textarea 上方就自动读入）
  function makeDropTarget(ta, fileLabelEl) {
    const overlay = el('div', { class: 'cmp-drop-hint', text: '松开以读入文件' });
    overlay.style.display = 'none';
    // textarea 可能尚未挂到 DOM（renderCompare 调用顺序），用 queueMicrotask 延后插入
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
        fileLabelEl.textContent = f.name;
        saveFileLabel(ta.dataset.cmpSide, f.name);
        toast('已读入：' + f.name, 'ok');
      };
      reader.onerror = () => toast('读取失败：' + f.name, 'err');
      reader.readAsText(f, 'utf-8');
    });
  }

  // ---------- 视图渲染 ----------
  function renderCompare(view) {
    const ignore = loadIgnore();
    outputMode = loadMode();
    hideEqualRows = loadHideEqual();

    // left / right 两个 textarea
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

    const leftFileLabel = el('span', { class: 'muted', text: loadFileLabel('left') || '未选择文件' });
    const rightFileLabel = el('span', { class: 'muted', text: loadFileLabel('right') || '未选择文件' });

    const btnPickLeft = el('button', {
      class: 'btn',
      text: '选择本地文件',
      onclick: () => pickLocalFile(leftTa, (name) => { leftFileLabel.textContent = name; saveFileLabel('left', name); }),
    });
    const btnPickRight = el('button', {
      class: 'btn',
      text: '选择本地文件',
      onclick: () => pickLocalFile(rightTa, (name) => { rightFileLabel.textContent = name; saveFileLabel('right', name); }),
    });
    const btnClearLeft = el('button', {
      class: 'btn btn-mini',
      text: '清',
      title: '清空左栏',
      onclick: () => {
        leftTa.value = '';
        leftFileLabel.textContent = '未选择文件';
        saveFileLabel('left', '');
      },
    });
    const btnClearRight = el('button', {
      class: 'btn btn-mini',
      text: '清',
      title: '清空右栏',
      onclick: () => {
        rightTa.value = '';
        rightFileLabel.textContent = '未选择文件';
        saveFileLabel('right', '');
      },
    });

    // drop target 在 view.appendChild 之后再挂，确保 textarea 已入 DOM（见下方）

    // ignore 三个 checkbox
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

    // 输出模式三个 radio
    const rdoUnified = el('input', { type: 'radio', name: 'cmp-mode', value: 'unified' });
    var rdoChanges = el('input', { type: 'radio', name: 'cmp-mode', value: 'changes' });
    const rdoSide = el('input', { type: 'radio', name: 'cmp-mode', value: 'side' });
    rdoUnified.checked = outputMode === 'unified';
    rdoChanges.checked = outputMode === 'changes';
    rdoSide.checked = outputMode === 'side';
    // side-by-side 专属："只看左右不同"（隐藏两侧相同的行）
    const cbHideEqual = el('input', { type: 'checkbox' });
    cbHideEqual.checked = hideEqualRows;
    const cbHideEqualWrap = el('label', { class: 'cmp-opt' }, [cbHideEqual, document.createTextNode(' 只看左右不同')]);
    function syncMode() {
      const checked = document.querySelector('input[name="cmp-mode"]:checked');
      if (checked) {
        outputMode = checked.value;
        saveMode(outputMode);
        // 仅 side 模式展示"只看左右不同"开关
        cbHideEqualWrap.style.display = (outputMode === 'side') ? '' : 'none';
        renderResult();
      }
    }
    rdoUnified.addEventListener('change', syncMode);
    rdoChanges.addEventListener('change', syncMode);
    rdoSide.addEventListener('change', syncMode);
    cbHideEqual.addEventListener('change', () => {
      hideEqualRows = cbHideEqual.checked;
      saveHideEqual(hideEqualRows);
      renderResult();
    });
    // 初始显隐
    cbHideEqualWrap.style.display = (outputMode === 'side') ? '' : 'none';

    // 操作按钮
    const btnSwap = el('button', { class: 'btn', text: '交换左右', onclick: () => {
      const lt = leftTa.value, rt = rightTa.value;
      const ll = leftFileLabel.textContent, rl = rightFileLabel.textContent;
      leftTa.value = rt; rightTa.value = lt;
      leftFileLabel.textContent = (rl === '未选择文件' ? '未选择文件' : rl);
      rightFileLabel.textContent = (ll === '未选择文件' ? '未选择文件' : ll);
      saveFileLabel('left', leftFileLabel.textContent === '未选择文件' ? '' : leftFileLabel.textContent);
      saveFileLabel('right', rightFileLabel.textContent === '未选择文件' ? '' : rightFileLabel.textContent);
    }});
    const btnClear = el('button', { class: 'btn', text: '清空全部', onclick: () => {
      leftTa.value = ''; rightTa.value = '';
      leftFileLabel.textContent = '未选择文件';
      rightFileLabel.textContent = '未选择文件';
      saveFileLabel('left', '');
      saveFileLabel('right', '');
      lastResult = null;
      renderResult();
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

    // 键盘快捷键：Ctrl/Cmd + Enter 触发比对
    [leftTa, rightTa].forEach((ta) => {
      ta.addEventListener('keydown', (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
          e.preventDefault();
          doCompare(leftTa, rightTa);
        }
      });
    });

    // 结果展示区
    const statsBar = el('div', { class: 'diff-stats muted', text: '尚未比对' });
    const resultBox = el('div', { class: 'diff-result d2h-wrapper' });

    function renderResult() {
      if (!lastResult) {
        statsBar.textContent = '尚未比对';
        resultBox.innerHTML = '';
        resultBox.className = 'diff-result d2h-wrapper';
        return;
      }
      const s = lastResult.stats;
      statsBar.textContent =
        '共 ' + s.left_lines + ' → ' + s.right_lines + ' 行 · ' +
        '新增 ' + s.added + ' · 删除 ' + s.removed + ' · 共同 ' + s.common +
        (hideEqualRows && outputMode === 'side' ? ' · 已隐藏 ' + s.common + ' 行相同内容' : '');

      resultBox.innerHTML = '';

      if (!d2h) {
        // vendor 没加载好（极端情况），降级为 pre 展示 unified diff
        resultBox.appendChild(el('pre', { class: 'diff-fallback', text: lastResult.unified_diff || '' }));
        return;
      }

      try {
        if (outputMode === 'unified') {
          const html = d2h.html(lastResult.unified_diff || '', {
            outputFormat: 'line-by-line',
            drawFileList: false,
            matching: 'lines',
            renderNothingWhenEmpty: false,
          });
          resultBox.innerHTML = html;
        } else if (outputMode === 'side') {
          // 勾选"只看左右不同"时，重新生成一份只含差异 hunk 的 unified diff 喂给 d2h
          const diffText = (hideEqualRows && lastResult.lines && lastResult.lines.length)
            ? buildUnifiedDiffFromLines(lastResult.lines, 'left', 'right')
            : (lastResult.unified_diff || '');
          const html = d2h.html(diffText, {
            outputFormat: 'side-by-side',
            drawFileList: false,
            matching: 'lines',
            renderNothingWhenEmpty: false,
          });
          resultBox.innerHTML = html;
        } else {
          // 仅差异行：手写
          resultBox.appendChild(renderChangesOnly(lastResult.lines));
        }
      } catch (e) {
        // diff2html 解析失败时降级
        resultBox.appendChild(el('pre', { class: 'diff-fallback', text: lastResult.unified_diff || '' }));
        toast('diff 渲染失败，已降级为纯文本：' + (e.message || e), 'err');
      }
    }
    activeRenderResult = renderResult;

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '代码比对' }),
      el('div', { class: 'card-desc', text: '纯本地行级 diff，支持粘贴 / 选本地文件 / 拖拽。后端不会保存任何比对内容。' }),
      el('div', { class: 'cmp-options' }, [
        el('span', { class: 'cmp-label', text: '忽略：' }),
        makeCheckbox(cbTrim, '忽略行尾空白'),
        makeCheckbox(cbBlank, '忽略空行'),
        makeCheckbox(cbCase, '忽略大小写'),
        el('span', { class: 'cmp-spacer' }),
        el('span', { class: 'cmp-label', text: '输出：' }),
        makeRadio(rdoUnified, 'Unified'),
        makeRadio(rdoChanges, '仅差异行'),
        makeRadio(rdoSide, 'Side-by-side'),
        cbHideEqualWrap,
      ]),
      el('div', { class: 'pane' }, [
        el('div', null, [
          el('div', { class: 'cmp-pane-header' }, [
            el('label', { text: '左：原始' }),
            leftFileLabel,
            btnPickLeft,
            btnClearLeft,
          ]),
          leftTa,
        ]),
        el('div', null, [
          el('div', { class: 'cmp-pane-header' }, [
            el('label', { text: '右：修改' }),
            rightFileLabel,
            btnPickRight,
            btnClearRight,
          ]),
          rightTa,
        ]),
      ]),
      el('div', { class: 'btn-row mt-3' }, [btnGo, btnSwap, btnClear, btnCopy, btnDownload]),
      el('div', { class: 'muted cmp-hint', text: '快捷键：Ctrl/⌘ + Enter 触发比对。v2 将支持「选两个目录挨个文件比对」和「远端 SSH 文件比对」。' }),
      el('div', { class: 'diff-result-wrap' }, [statsBar, resultBox]),
    ]));

    // 此时 textarea 已挂到 DOM，再装拖拽 overlay
    makeDropTarget(leftTa, leftFileLabel);
    makeDropTarget(rightTa, rightFileLabel);

    renderResult();
  }

  // rdoChanges 在 renderCompare 内引用，需要 var hoisting
  var rdoChanges; // eslint-disable-line no-var

  function makeCheckbox(input, label) {
    return el('label', { class: 'cmp-opt' }, [input, document.createTextNode(' ' + label)]);
  }
  function makeRadio(input, label) {
    return el('label', { class: 'cmp-opt' }, [input, document.createTextNode(' ' + label)]);
  }

  // ---------- 仅差异行 ----------
  // diff2html 没原生"仅差异行"模式，我们手写：复用后端 lines，过滤 equal 行，
  // 然后给每个 delete 找最近的 insert 配对，把对应行内 highlight。
  function renderChangesOnly(lines) {
    const wrap = el('div', { class: 'diff-changes' });

    // 先把 delete 和 insert 配对（按出现顺序一对一），剩余的独自展示。
    // 注意：lines 顺序是混合的，equal/delete/insert 可能交错。
    let pendingDeletes = [];
    function flushPending(intoWrap) {
      pendingDeletes.forEach((d) => {
        intoWrap.appendChild(changeRow('del', d.left_no, d.text));
      });
      pendingDeletes = [];
    }

    for (const ln of lines) {
      if (ln.op === 'equal') continue;
      if (ln.op === 'delete') {
        pendingDeletes.push(ln);
        continue;
      }
      // insert：尝试与最早的 pending delete 配对，做行内字符级 highlight
      if (pendingDeletes.length) {
        const d = pendingDeletes.shift();
        const parts = wordDiff(d.text, ln.text);
        wrap.appendChild(pairedRow('del', d.left_no, parts.left, parts.right));
        wrap.appendChild(pairedRow('add', ln.right_no, parts.right, parts.left));
      } else {
        wrap.appendChild(changeRow('add', ln.right_no, ln.text));
      }
    }
    flushPending(wrap);

    if (!wrap.children.length) {
      wrap.appendChild(el('div', { class: 'muted diff-empty', text: '没有差异' }));
    }
    return wrap;
  }

  // ---------- 给 side-by-side 用的"只看不同"过滤 ----------
  // 把后端 lines 序列按 diff 算法 hunk 重新打包成 unified diff 文本：
  //   - 完全删除 equal 行（前后相邻的 delete/insert 各自合并成独立 hunk）
  //   - 让 d2h side-by-side 只渲染差异，不再被未变更行撑满屏幕
  //
  // 输出格式严格遵守 unified diff 头：
  //   --- a/<label>\t<ts>
  //   +++ b/<label>\t<ts>
  //   @@ -lStart,lCount +rStart,rCount @@
  //   ...
  function buildUnifiedDiffFromLines(lines, leftLabel, rightLabel) {
    const header = [
      '--- a/' + leftLabel,
      '+++ b/' + rightLabel,
    ];

    // 第一遍：找所有"差异段"（hunk）。每个 hunk 由一串连续的 delete+insert 组成，
    // 中间被 equal 切开的 equal 段完全丢弃。
    const hunks = [];
    let cur = null; // { del: [], ins: [], lStart, rStart }
    function closeHunk() {
      if (!cur) return;
      if (cur.del.length || cur.ins.length) hunks.push(cur);
      cur = null;
    }
    for (const ln of lines) {
      if (ln.op === 'equal') { closeHunk(); continue; }
      if (!cur) {
        cur = { del: [], ins: [], lStart: ln.left_no || 0, rStart: ln.right_no || 0 };
      }
      if (ln.op === 'delete') cur.del.push(ln);
      else cur.ins.push(ln);
    }
    closeHunk();

    if (!hunks.length) {
      // 没有差异时给一份"空 diff"，d2h 会渲染"files are identical"提示
      return header.join('\n') + '\n';
    }

    const out = header.slice();
    for (const h of hunks) {
      const lCount = h.del.length;
      const rCount = h.ins.length;
      out.push('@@ -' + h.lStart + ',' + lCount + ' +' + h.rStart + ',' + rCount + ' @@');
      for (const d of h.del) out.push('-' + (d.text || ''));
      for (const i of h.ins) out.push('+' + (i.text || ''));
    }
    return out.join('\n') + '\n';
  }

  function changeRow(op, no, text) {
    return el('div', { class: 'diff-line diff-' + op }, [
      el('span', { class: 'ln', text: no || '' }),
      el('span', { class: 'marker', text: op === 'del' ? '-' : '+' }),
      el('span', { class: 'text', text: text }),
    ]);
  }
  // 配对行：左右各显示一份（行内字符级高亮）
  // parts.left = [{same:bool, text:string}, ...] 在左行内的标记
  function pairedRow(op, no, selfParts, _otherPartsUnused) {
    const row = el('div', { class: 'diff-line diff-' + op });
    row.appendChild(el('span', { class: 'ln', text: no || '' }));
    row.appendChild(el('span', { class: 'marker', text: op === 'del' ? '-' : '+' }));
    const textWrap = el('span', { class: 'text' });
    for (const p of selfParts) {
      const span = el('span', { class: 'cmp-word-' + (p.same ? 'same' : 'diff') });
      span.textContent = p.text;
      textWrap.appendChild(span);
    }
    row.appendChild(textWrap);
    return row;
  }

  // 简易词级 diff：用最长公共子序列思路对"词元"做匹配
  // 词元 = 一段连续的非空白 或 一段连续的空白（保留对齐）
  // 返回 { left: [...], right: [...] }，每元素 {same, text}
  function wordDiff(leftText, rightText) {
    const leftTokens = tokenize(leftText);
    const rightTokens = tokenize(rightText);

    // LCS 长度矩阵
    const N = leftTokens.length, M = rightTokens.length;
    if (N === 0) return { left: [], right: [{ same: false, text: rightText }] };
    if (M === 0) return { left: [{ same: false, text: leftText }], right: [] };

    // 小文本用 O(NM) 没问题；超过 4000 token 时降级为整体标记
    if (N * M > 16000000) {
      return {
        left:  [{ same: false, text: leftText }],
        right: [{ same: false, text: rightText }],
      };
    }

    const dp = [];
    for (let i = 0; i <= N; i++) dp.push(new Int32Array(M + 1));
    for (let i = 1; i <= N; i++) {
      for (let j = 1; j <= M; j++) {
        if (leftTokens[i - 1] === rightTokens[j - 1]) dp[i][j] = dp[i - 1][j - 1] + 1;
        else dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
      }
    }

    // 回溯
    const leftParts = [];
    const rightParts = [];
    let i = N, j = M;
    while (i > 0 && j > 0) {
      if (leftTokens[i - 1] === rightTokens[j - 1]) {
        leftParts.unshift({ same: true, text: leftTokens[i - 1] });
        rightParts.unshift({ same: true, text: rightTokens[j - 1] });
        i--; j--;
      } else if (dp[i - 1][j] >= dp[i][j - 1]) {
        leftParts.unshift({ same: false, text: leftTokens[i - 1] });
        i--;
      } else {
        rightParts.unshift({ same: false, text: rightTokens[j - 1] });
        j--;
      }
    }
    while (i > 0) { leftParts.unshift({ same: false, text: leftTokens[--i + 1] }); i--; }
    while (j > 0) { rightParts.unshift({ same: false, text: rightTokens[--j + 1] }); j--; }

    return { left: leftParts, right: rightParts };
  }
  function tokenize(s) {
    // 词元 = 连续非空白 或 连续空白（保留空格、tab，便于对齐）
    const out = [];
    const re = /\S+|\s+/g;
    let m;
    while ((m = re.exec(s)) !== null) out.push(m[0]);
    return out;
  }

  // ---------- 调用后端 ----------
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
      if (activeRenderResult) activeRenderResult();
      else toast('结果已生成，但当前页未挂载，请重新打开"代码比对"菜单', 'warn');
    } catch (e) {
      toast('比对失败：' + (e.message || e), 'err');
    }
  }

  // ---------- 工具 ----------
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

  // ---------- 注册 ----------
  // vendor 加载检查 + 把 Diff2Html 句柄取出来
  function bootstrap() {
    d2h = (typeof window.Diff2Html !== 'undefined') ? window.Diff2Html : null;
  }
  bootstrap();

  OTB.pages.compare = renderCompare;
  OTB.state.routes.compare = renderCompare;
  OTB.state.routeNames.compare = '代码比对';
})();