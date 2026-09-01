/* ===== web/notes-md.js — safe Markdown subset + task-list toggle ===== */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};

  const TASK_RE = /^(\s*)([-*+]|\d+[.)])\s+\[([ xX])\]\s?(.*)$/;
  const UL_RE = /^(\s*)([-*+])\s+(.*)$/;
  const OL_RE = /^(\s*)(\d+)[.)]\s+(.*)$/;
  const HEADING_RE = /^(#{1,3})\s+(.*)$/;
  const FENCE_RE = /^```/;
  const HR_RE = /^(?:-{3,}|\*{3,}|_{3,})\s*$/;
  const BQ_RE = /^>\s?(.*)$/;

  function isSafeURL(href) {
    const s = String(href || '').trim();
    return /^(https?:\/\/|mailto:)/i.test(s);
  }

  function appendText(parent, text) {
    if (text) parent.appendChild(document.createTextNode(text));
  }

  function renderInline(text, parent) {
    text = String(text || '');
    let i = 0;
    while (i < text.length) {
      if (text[i] === '`') {
        const end = text.indexOf('`', i + 1);
        if (end > i) {
          const code = document.createElement('code');
          code.appendChild(document.createTextNode(text.slice(i + 1, end)));
          parent.appendChild(code);
          i = end + 1;
          continue;
        }
      }
      if (text[i] === '[' ) {
        const close = text.indexOf(']', i + 1);
        if (close > i && text[close + 1] === '(' ) {
          const endParen = text.indexOf(')', close + 2);
          if (endParen > close) {
            const label = text.slice(i + 1, close);
            const href = text.slice(close + 2, endParen);
            if (isSafeURL(href)) {
              const a = document.createElement('a');
              a.href = href;
              a.target = '_blank';
              a.rel = 'noopener noreferrer';
              renderInline(label, a);
              parent.appendChild(a);
            } else {
              appendText(parent, label);
            }
            i = endParen + 1;
            continue;
          }
        }
      }
      if (text.slice(i, i + 2) === '**') {
        const end = text.indexOf('**', i + 2);
        if (end > i) {
          const strong = document.createElement('strong');
          renderInline(text.slice(i + 2, end), strong);
          parent.appendChild(strong);
          i = end + 2;
          continue;
        }
      }
      if (text[i] === '*' && text[i + 1] !== '*') {
        const end = text.indexOf('*', i + 1);
        if (end > i) {
          const em = document.createElement('em');
          renderInline(text.slice(i + 1, end), em);
          parent.appendChild(em);
          i = end + 1;
          continue;
        }
      }
      let next = text.length;
      const ticks = [
        text.indexOf('`', i + 1),
        text.indexOf('[', i + 1),
        text.indexOf('**', i + 1),
        text.indexOf('*', i + 1)
      ];
      ticks.forEach(function (p) { if (p >= 0 && p < next) next = p; });
      appendText(parent, text.slice(i, next));
      i = next;
    }
  }

  function collectTasks(src) {
    const out = [];
    String(src || '').split('\n').forEach(function (line, lineIndex) {
      const m = line.match(TASK_RE);
      if (!m) return;
      out.push({
        line: lineIndex,
        indent: m[1],
        bullet: m[2],
        checked: m[3] !== ' ',
        text: m[4]
      });
    });
    return out;
  }

  function toggleTask(src, index) {
    const lines = String(src || '').split('\n');
    let n = 0;
    for (let i = 0; i < lines.length; i++) {
      const m = lines[i].match(TASK_RE);
      if (!m) continue;
      if (n === index) {
        const next = m[3] === ' ' ? 'x' : ' ';
        lines[i] = m[1] + m[2] + ' [' + next + '] ' + m[4];
        return lines.join('\n');
      }
      n++;
    }
    return src;
  }

  function render(src, opts) {
    opts = opts || {};
    const root = document.createElement('div');
    root.className = 'notes-md' + (opts.compact ? ' notes-md-compact' : '');
    const lines = String(src || '').split('\n');
    let i = 0;
    let taskIndex = 0;
    let chars = 0;
    const maxChars = opts.maxChars > 0 ? opts.maxChars : 0;
    let truncated = false;

    function budget(chunk) {
      if (!maxChars) return chunk;
      if (chars >= maxChars) {
        truncated = true;
        return null;
      }
      chars += chunk.length;
      if (chars > maxChars) {
        truncated = true;
        return chunk.slice(0, chunk.length - (chars - maxChars));
      }
      return chunk;
    }

    function addTask(m) {
      const label = document.createElement('label');
      label.className = 'notes-md-task' + (m[3] !== ' ' ? ' is-done' : '');
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.checked = m[3] !== ' ';
      const idx = taskIndex++;
      if (typeof opts.onToggle === 'function') {
        box.addEventListener('click', function (ev) { ev.stopPropagation(); });
        box.addEventListener('change', function (ev) {
          ev.stopPropagation();
          opts.onToggle(idx, box.checked);
        });
      } else {
        box.disabled = true;
      }
      const span = document.createElement('span');
      renderInline(m[4], span);
      label.append(box, span);
      label.addEventListener('click', function (ev) { ev.stopPropagation(); });
      return label;
    }

    while (i < lines.length) {
      if (truncated) break;
      const raw = lines[i];
      if (FENCE_RE.test(raw)) {
        i++;
        const buf = [];
        while (i < lines.length && !FENCE_RE.test(lines[i])) {
          buf.push(lines[i]);
          i++;
        }
        if (i < lines.length) i++;
        const pre = document.createElement('pre');
        const code = document.createElement('code');
        const body = budget(buf.join('\n'));
        if (body == null) break;
        code.appendChild(document.createTextNode(body));
        pre.appendChild(code);
        root.appendChild(pre);
        continue;
      }
      if (HR_RE.test(raw)) {
        if (budget(raw) == null) break;
        root.appendChild(document.createElement('hr'));
        i++;
        continue;
      }
      const heading = raw.match(HEADING_RE);
      if (heading) {
        const text = budget(heading[2]);
        if (text == null) break;
        const h = document.createElement('h' + heading[1].length);
        renderInline(text, h);
        root.appendChild(h);
        i++;
        continue;
      }
      const bq = raw.match(BQ_RE);
      if (bq) {
        const quote = document.createElement('blockquote');
        while (i < lines.length) {
          const qm = lines[i].match(BQ_RE);
          if (!qm) break;
          const text = budget(qm[1]);
          if (text == null) break;
          if (quote.childNodes.length) quote.appendChild(document.createElement('br'));
          renderInline(text, quote);
          i++;
        }
        root.appendChild(quote);
        continue;
      }
      const task = raw.match(TASK_RE);
      if (task) {
        const list = document.createElement('ul');
        list.className = 'notes-md-tasks';
        while (i < lines.length) {
          const tm = lines[i].match(TASK_RE);
          if (!tm) break;
          if (budget(lines[i]) == null) break;
          const li = document.createElement('li');
          li.appendChild(addTask(tm));
          list.appendChild(li);
          i++;
        }
        root.appendChild(list);
        continue;
      }
      const ul = raw.match(UL_RE);
      if (ul) {
        const list = document.createElement('ul');
        while (i < lines.length) {
          const um = lines[i].match(UL_RE);
          if (!um || TASK_RE.test(lines[i])) break;
          if (budget(um[3]) == null) break;
          const li = document.createElement('li');
          renderInline(um[3], li);
          list.appendChild(li);
          i++;
        }
        root.appendChild(list);
        continue;
      }
      const ol = raw.match(OL_RE);
      if (ol) {
        const list = document.createElement('ol');
        while (i < lines.length) {
          const om = lines[i].match(OL_RE);
          if (!om || TASK_RE.test(lines[i])) break;
          if (budget(om[3]) == null) break;
          const li = document.createElement('li');
          renderInline(om[3], li);
          list.appendChild(li);
          i++;
        }
        root.appendChild(list);
        continue;
      }
      if (!String(raw).trim()) {
        i++;
        continue;
      }
      const p = document.createElement('p');
      while (i < lines.length) {
        const line = lines[i];
        if (!String(line).trim()) break;
        if (FENCE_RE.test(line) || HEADING_RE.test(line) || HR_RE.test(line) || BQ_RE.test(line) || TASK_RE.test(line) || UL_RE.test(line) || OL_RE.test(line)) break;
        const text = budget(line);
        if (text == null) break;
        if (p.childNodes.length) p.appendChild(document.createElement('br'));
        renderInline(text, p);
        i++;
      }
      if (p.childNodes.length) root.appendChild(p);
    }
    if (!root.childNodes.length) {
      const empty = document.createElement('p');
      empty.className = 'notes-md-empty';
      empty.textContent = opts.emptyText || '空便笺';
      root.appendChild(empty);
    } else if (truncated) {
      const more = document.createElement('p');
      more.className = 'notes-md-more';
      more.textContent = '…';
      root.appendChild(more);
    }
    return root;
  }

  function insertTaskLine(src, cursor) {
    src = String(src || '');
    cursor = Math.max(0, Math.min(src.length, cursor == null ? src.length : cursor));
    const before = src.slice(0, cursor);
    const after = src.slice(cursor);
    const nl = before.length && !before.endsWith('\n') ? '\n' : '';
    const line = '- [ ] ';
    return { text: before + nl + line + after, cursor: before.length + nl.length + line.length };
  }

  Kairo.notesMd = {
    render: render,
    toggleTask: toggleTask,
    collectTasks: collectTasks,
    insertTaskLine: insertTaskLine,
    isSafeURL: isSafeURL
  };
})();
