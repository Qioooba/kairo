/* Shared editor statement model.  It deliberately stays dependency-free so
 * the vanilla pages can use the same cursor semantics without a bundler. */
(function () {
  'use strict';
  const K = window.Kairo = window.Kairo || {};
  const W = K.workbench = K.workbench || {};

  function scanSegments(text) {
    text = String(text == null ? '' : text);
    const cuts = [];
    let state = 'normal';
    let quote = '';
    let start = 0;
    for (let i = 0; i < text.length; i++) {
      const c = text[i], n = text[i + 1];
      if (state === 'line') {
        if (c === '\n') state = 'normal';
        continue;
      }
      if (state === 'block') {
        if (c === '*' && n === '/') { state = 'normal'; i++; }
        continue;
      }
      if (state === 'quote') {
        if (c === quote) {
          if (n === quote) { i++; continue; }
          state = 'normal';
        } else if (c === '\\' && quote !== '"') {
          i++;
        }
        continue;
      }
      if (c === '-' && n === '-') { state = 'line'; i++; continue; }
      if (c === '/' && n === '*') { state = 'block'; i++; continue; }
      if (c === '/' && n === '/' && (i === 0 || /\s/.test(text[i - 1]))) { state = 'line'; i++; continue; }
      if (c === '\'' || c === '"' || c === '`') { state = 'quote'; quote = c; continue; }
      // Oracle q'[ ... ]', q'{ ... }', q'< ... >', q'( ... )', q'!...!' etc.
      if ((c === 'q' || c === 'Q') && n === '\'' && i + 2 < text.length) {
        const opener = text[i + 2];
        const closerMap = { '[': ']', '{': '}', '(': ')', '<': '>' };
        const closer = closerMap[opener] || opener;
        const end = text.indexOf(closer + '\'', i + 3);
        if (end >= 0) { i = end + 1; continue; }
      }
      if (c === ';') {
        cuts.push({ start: start, end: i + 1, delimiter: i });
        start = i + 1;
      }
    }
    if (start < text.length || !cuts.length) cuts.push({ start: start, end: text.length, delimiter: -1 });
    return cuts;
  }

  function nonEmptyRange(text, range) {
    const value = text.slice(range.start, range.end).replace(/(^|\n)\s*--.*(?=\n|$)/g, '').trim();
    return value.length > 0 && value !== ';';
  }

  function splitStatements(text) {
    text = String(text == null ? '' : text);
    return scanSegments(text).filter(function (r) { return nonEmptyRange(text, r); }).map(function (r, i) {
      return { index: i, start: r.start, end: r.end, text: text.slice(r.start, r.end), delimiter: r.delimiter };
    });
  }

  function currentStatement(text, cursor, selectionStart, selectionEnd) {
    text = String(text == null ? '' : text);
    const ss = Number.isFinite(selectionStart) ? selectionStart : 0;
    const se = Number.isFinite(selectionEnd) ? selectionEnd : 0;
    if (se > ss) {
      const selected = text.slice(ss, se).trim();
      if (selected) return { start: ss, end: se, text: selected, selected: true, index: -1 };
    }
    const pos = Math.max(0, Math.min(text.length, Number.isFinite(cursor) ? cursor : 0));
    const ranges = splitStatements(text);
    if (!ranges.length) return { start: 0, end: text.length, text: text.trim(), selected: false, index: 0 };
    let previous = null;
    for (let i = 0; i < ranges.length; i++) {
      const r = ranges[i];
      if (pos < r.start) return r;
      if (pos >= r.start && pos < r.end) return r;
      previous = r;
    }
    return previous || ranges[0];
  }

  W.statementModel = {
    split: splitStatements,
    current: currentStatement,
    normalize: function (text, cursor, selectionStart, selectionEnd) {
      const r = currentStatement(text, cursor, selectionStart, selectionEnd);
      let s = String(r.text || '').replace(/^\s+|\s+$/g, '');
      while (s.endsWith(';')) s = s.slice(0, -1).trim();
      return Object.assign({}, r, { text: s });
    }
  };
})();
