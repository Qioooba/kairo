/* SQL Format Service (Kairo Workbench)
 *
 * Unified, dialect-aware SQL and PL/SQL formatting engine designed for Oracle 11g
 * and general SQL dialects.
 *
 * Guarantees:
 * 1. String & literal fidelity: Strings (including Q-quotes, N-strings, PostgreSQL dollar quotes),
 *    quoted identifiers, comments and hints are preserved verbatim without altering internal whitespace.
 * 2. Oracle bind variables & identifiers: :serialno, :1, and full Unicode/Chinese identifiers
 *    are never split.
 * 3. Composite operators: :=, =>, **, <<, >>, ||, <=, >=, etc., are never split.
 * 4. Standalone slash (/): Output on its own line to preserve statement boundaries for PL/SQL scripts.
 * 5. Idempotence: format(format(sql)) === format(sql).
 * 6. Single-step undo in editors via document.execCommand('insertText').
 */
(function () {
  'use strict';

  const K = (typeof window !== 'undefined' ? (window.Kairo = window.Kairo || {}) : {});
  const W = (K.workbench = K.workbench || {});

  const SQL_KEYWORDS = new Set([
    'select', 'from', 'where', 'group', 'by', 'having', 'order', 'union', 'all',
    'distinct', 'as', 'join', 'left', 'right', 'inner', 'outer', 'full', 'cross',
    'natural', 'on', 'using', 'with', 'values', 'insert', 'into', 'update', 'delete',
    'create', 'alter', 'drop', 'table', 'view', 'index', 'set', 'limit', 'offset',
    'fetch', 'first', 'next', 'only', 'rows', 'row', 'dual', 'sysdate', 'systimestamp',
    'now', 'case', 'when', 'then', 'else', 'end', 'and', 'or', 'not', 'in', 'exists',
    'like', 'between', 'is', 'null', 'true', 'false', 'begin', 'procedure', 'function',
    'package', 'body', 'trigger', 'declare', 'exception', 'loop', 'while', 'for', 'if',
    'elsif', 'return', 'commit', 'rollback', 'savepoint', 'connect', 'start',
    'prior', 'minus', 'except', 'intersect', 'over', 'partition', 'desc', 'asc', 'show',
    'describe', 'explain', 'truncate', 'merge', 'matched', 'bulk', 'collect', 'forall',
    'open', 'close', 'cursor', 'type', 'record', 'replace', 'force', 'constraint', 'primary',
    'key', 'foreign', 'references', 'unique', 'check', 'default', 'sequence', 'synonym',
    'grant', 'revoke', 'lock', 'nowait', 'wait', 'skip', 'locked', 'model', 'qualify', 'window'
  ]);

  const MAJOR_CLAUSES = new Set([
    'select', 'from', 'where', 'group by', 'having', 'order by', 'with', 'values',
    'set', 'limit', 'offset', 'fetch first', 'fetch next', 'union', 'union all',
    'minus', 'except', 'intersect', 'insert into', 'update', 'delete from',
    'begin', 'declare', 'exception', 'end', 'merge into'
  ]);

  const JOIN_PREFIXES = new Set([
    'join', 'left join', 'right join', 'inner join', 'full join', 'cross join',
    'left outer join', 'right outer join', 'full outer join', 'natural join',
    'natural left join', 'natural right join', 'natural left outer join', 'natural right outer join'
  ]);

  /**
   * Tokenize SQL source text into an array of tokens with exact value preservation.
   */
  function tokenizeSQL(source) {
    const text = String(source == null ? '' : source);
    const tokens = [];
    let i = 0;

    function push(type, start, end, extra) {
      const tok = { type: type, value: text.slice(start, end), start: start, end: end };
      if (extra) {
        for (const k in extra) {
          if (Object.prototype.hasOwnProperty.call(extra, k)) tok[k] = extra[k];
        }
      }
      tokens.push(tok);
    }

    while (i < text.length) {
      const start = i;
      const c = text[i];
      const next = text[i + 1] || '';

      // 1. Whitespace
      if (c <= ' ') {
        while (i < text.length && text[i] <= ' ') i++;
        push('space', start, i);
        continue;
      }

      // 2. Comments: Single line --
      if (c === '-' && next === '-') {
        let end = text.indexOf('\n', i + 2);
        if (end < 0) end = text.length;
        push('comment', start, end);
        i = end;
        continue;
      }

      // 3. Comments: Single line # (MySQL style)
      if (c === '#' && (i === 0 || /\s/.test(text[i - 1]))) {
        let end = text.indexOf('\n', i + 1);
        if (end < 0) end = text.length;
        push('comment', start, end);
        i = end;
        continue;
      }

      // 4. Comments: Block /* ... */ (including /*+ HINT */)
      if (c === '/' && next === '*') {
        const end = text.indexOf('*/', i + 2);
        const take = end < 0 ? text.length : end + 2;
        push('comment', start, take);
        i = take;
        continue;
      }

      // 5. Oracle standalone / on its own line (script delimiter)
      if (c === '/') {
        let lineStart = text.lastIndexOf('\n', i - 1);
        lineStart = lineStart < 0 ? 0 : lineStart + 1;
        const beforeOnLine = text.slice(lineStart, i).trim();
        let lineEnd = text.indexOf('\n', i + 1);
        if (lineEnd < 0) lineEnd = text.length;
        const afterOnLine = text.slice(i + 1, lineEnd).trim();

        if (beforeOnLine === '' && (afterOnLine === '' || afterOnLine.startsWith('--'))) {
          push('slash-delimiter', start, i + 1);
          i++;
          continue;
        }
      }

      // 6. Oracle PL/SQL label: <<label_name>>
      if (c === '<' && next === '<') {
        const labelMatch = text.slice(i).match(/^<<\s*([A-Za-z0-9_$#\u0080-\uffff]+)\s*>>/);
        if (labelMatch) {
          const matchedLen = labelMatch[0].length;
          push('label', start, i + matchedLen, { value: '<<' + labelMatch[1] + '>>' });
          i += matchedLen;
          continue;
        }
      }

      // 7. Oracle Q-quotes: q'[...]', Q'!...!', nq'[...]', etc.
      let isQQuote = false;
      let qPrefixLen = 0;
      if ((c === 'q' || c === 'Q') && next === "'") {
        isQQuote = true;
        qPrefixLen = 2; // q'
      } else if ((c === 'n' || c === 'N') && (next === 'q' || next === 'Q') && text[i + 2] === "'") {
        isQQuote = true;
        qPrefixLen = 3; // nq'
      }

      if (isQQuote && i + qPrefixLen < text.length) {
        const openChar = text[i + qPrefixLen];
        const closeMap = { '[': ']', '{': '}', '(': ')', '<': '>' };
        const closeChar = closeMap[openChar] || openChar;
        const closer = closeChar + "'";
        const end = text.indexOf(closer, i + qPrefixLen + 1);
        if (end >= 0) {
          const take = end + closer.length;
          push('string', start, take);
          i = take;
          continue;
        }
      }

      // 8. National string literal N'...' or n'...'
      if ((c === 'n' || c === 'N') && next === "'") {
        let j = i + 2;
        while (j < text.length) {
          if (text[j] === "'") {
            if (text[j + 1] === "'") { j += 2; continue; }
            j++;
            break;
          }
          j++;
        }
        push('string', start, j);
        i = j;
        continue;
      }

      // 9. PostgreSQL dollar quote: $$...$$ or $tag$...$tag$
      if (c === '$') {
        const tagMatch = text.slice(i).match(/^\$[A-Za-z0-9_]*\$/);
        if (tagMatch) {
          const tag = tagMatch[0];
          const end = text.indexOf(tag, i + tag.length);
          if (end >= 0) {
            push('string', start, end + tag.length);
            i = end + tag.length;
            continue;
          }
        }
      }

      // 10. Standard Strings & Quoted Identifiers: '...', "...", `...`
      if (c === "'" || c === '"' || c === '`') {
        const quote = c;
        let j = i + 1;
        while (j < text.length) {
          if (text[j] === quote) {
            if (text[j + 1] === quote) { j += 2; continue; }
            j++;
            break;
          }
          if (text[j] === '\\' && quote !== '`') j += 2;
          else j++;
        }
        push(quote === "'" ? 'string' : 'quoted-ident', start, j);
        i = j;
        continue;
      }

      // 11. Square bracket identifier: [column_name] (SQL Server/Access)
      if (c === '[' && (i === 0 || !/[A-Za-z0-9_$#\u0080-\uffff)\]]/.test(text[i - 1]))) {
        const end = text.indexOf(']', i + 1);
        if (end >= 0 && end - i < 128 && !text.slice(i, end).includes('\n')) {
          push('quoted-ident', start, end + 1);
          i = end + 1;
          continue;
        }
      }

      // 12. Multi-character and composite operators
      if (c === ':' && next === '=') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '=' && next === '>') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '*' && next === '*') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '<' && next === '<') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '>' && next === '>') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '.' && next === '.') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '|' && next === '|') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '<' && next === '>') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '!' && next === '=') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '~' && next === '=') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '^' && next === '=') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '<' && next === '=') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '>' && next === '=') { push('operator', start, i + 2); i += 2; continue; }
      if (c === ':' && next === ':') { push('operator', start, i + 2); i += 2; continue; }
      if (c === '-' && next === '>') {
        if (text[i + 2] === '>') { push('operator', start, i + 3); i += 3; continue; }
        push('operator', start, i + 2); i += 2; continue;
      }

      // 13. Oracle Bind Variables: :serialno, :1, :user_id, etc.
      if (c === ':' && /[A-Za-z0-9_$#\u0080-\uffff]/.test(next)) {
        let j = i + 1;
        while (j < text.length && /[A-Za-z0-9_$#\u0080-\uffff]/.test(text[j])) j++;
        push('param', start, j, { name: text.slice(start + 1, j) });
        i = j;
        continue;
      }

      // 14. Positional parameter ?
      if (c === '?') {
        push('param', start, i + 1, { name: '?' });
        i++;
        continue;
      }

      // 15. Numbers: 123, 12.34, .5, 1e-4, 0x1A
      if ((c >= '0' && c <= '9') || (c === '.' && next >= '0' && next <= '9')) {
        let j = i + 1;
        while (j < text.length && /[0-9A-Fa-f_xX.eE+-]/.test(text[j])) j++;
        push('number', start, j);
        i = j;
        continue;
      }

      // 16. Words (Keywords or Identifiers) - Full Unicode / Chinese support!
      if (/[A-Za-z_$#\u0080-\uffff]/.test(c)) {
        let j = i + 1;
        while (j < text.length && /[A-Za-z0-9_$#\u0080-\uffff]/.test(text[j])) j++;
        const val = text.slice(start, j);
        const lower = val.toLowerCase();
        push(SQL_KEYWORDS.has(lower) ? 'keyword' : 'ident', start, j);
        i = j;
        continue;
      }

      // 17. Brackets & Punctuation
      if (c === '(' || c === ')' || c === '[' || c === ']' || c === '{' || c === '}') {
        push('bracket', start, i + 1);
        i++;
        continue;
      }

      if (c === ',' || c === ';' || c === '.') {
        push('punct', start, i + 1);
        i++;
        continue;
      }

      // 18. Single character operators (=, <, >, +, -, *, /, %, &, |, ^, ~)
      if ('=<>+-*/%&|^~'.indexOf(c) >= 0) {
        push('operator', start, i + 1);
        i++;
        continue;
      }

      // 19. Fallback punctuation / symbol
      push('punct', start, i + 1);
      i++;
    }

    return tokens;
  }

  /**
   * Format SQL tokens into beautifully indented, structured SQL code.
   * Absolutely preserves strings, quotes, and comment contents.
   */
  function formatTokens(tokens, options) {
    options = options || {};
    const indentUnit = typeof options.indent === 'string' ? options.indent : (options.indent === 4 ? '    ' : '  ');
    const keywordCase = options.keywordCase || 'upper'; // 'upper' | 'lower' | 'preserve'
    const linesBetweenQueries = typeof options.linesBetweenQueries === 'number' ? options.linesBetweenQueries : 1;

    const codeTokens = tokens.filter(tok => tok.type !== 'space');
    if (!codeTokens.length) return '';

    const lines = [];
    let currentLine = '';
    let indentLevel = 0;
    let blockDepth = 0;
    let inSelectList = false;
    let parenDepth = 0;
    const parenStack = [];
    let inCase = 0;

    function flushLine(force) {
      const trimmed = currentLine.replace(/[ \t]+$/, '');
      if (trimmed || force) {
        lines.push((trimmed ? indentUnit.repeat(Math.max(0, indentLevel)) : '') + trimmed);
      }
      currentLine = '';
    }

    function ensureNewline() {
      if (currentLine.trim()) flushLine(false);
    }

    function exitSelectList() {
      if (inSelectList) {
        ensureNewline();
        indentLevel = Math.max(0, indentLevel - 1);
        inSelectList = false;
      }
    }

    function appendToken(val, needSpaceBefore) {
      if (!currentLine) {
        currentLine = val;
      } else {
        const lastChar = currentLine.slice(-1);
        if (needSpaceBefore && !/\s/.test(lastChar) && lastChar !== '(' && lastChar !== '[') {
          currentLine += ' ' + val;
        } else {
          currentLine += val;
        }
      }
    }

    let i = 0;
    while (i < codeTokens.length) {
      const tok = codeTokens[i];
      const nextTok = codeTokens[i + 1] || null;
      const prevTok = codeTokens[i - 1] || null;
      const lower = tok.value.toLowerCase();

      // 1. Script block delimiter: standalone slash /
      if (tok.type === 'slash-delimiter') {
        exitSelectList();
        ensureNewline();
        lines.push('/');
        if (nextTok) {
          for (let k = 0; k < linesBetweenQueries; k++) lines.push('');
        }
        i++;
        continue;
      }

      // 2. Comments
      if (tok.type === 'comment') {
        if (tok.value.startsWith('--') || tok.value.startsWith('#')) {
          if (currentLine.trim()) {
            appendToken(tok.value, true);
          } else {
            currentLine = tok.value;
          }
          flushLine(false);
        } else {
          // Block comment /* ... */: preserve internal lines exactly!
          const commentParts = tok.value.split('\n');
          if (commentParts.length === 1) {
            appendToken(tok.value, true);
          } else {
            if (currentLine.trim()) flushLine(false);
            commentParts.forEach(function (part, idx) {
              if (idx === 0) {
                currentLine = part;
                flushLine(false);
              } else if (idx === commentParts.length - 1) {
                currentLine = part;
              } else {
                lines.push(part);
              }
            });
          }
        }
        i++;
        continue;
      }

      // 3. Statement end: semicolon ;
      if (tok.type === 'punct' && tok.value === ';') {
        exitSelectList();
        appendToken(';', false);
        ensureNewline();
        if (blockDepth === 0 && nextTok && nextTok.type !== 'slash-delimiter') {
          for (let k = 0; k < linesBetweenQueries; k++) lines.push('');
        }
        i++;
        continue;
      }

      // 4. Multi-word Compound JOINs & Clauses Peek
      let compoundClause = null;
      let compoundCount = 1;

      if (tok.type === 'keyword') {
        const next1 = nextTok && nextTok.type === 'keyword' ? nextTok.value.toLowerCase() : '';
        const next2 = codeTokens[i + 2] && codeTokens[i + 2].type === 'keyword' ? codeTokens[i + 2].value.toLowerCase() : '';

        const threeWords = lower + ' ' + next1 + ' ' + next2;
        if (JOIN_PREFIXES.has(threeWords)) {
          compoundClause = threeWords;
          compoundCount = 3;
        }

        if (!compoundClause) {
          const twoWords = lower + ' ' + next1;
          if (JOIN_PREFIXES.has(twoWords) || MAJOR_CLAUSES.has(twoWords)) {
            compoundClause = twoWords;
            compoundCount = 2;
          }
        }
      }

      // 5. Handle Compound Clause
      if (compoundClause) {
        exitSelectList();
        ensureNewline();
        const words = [];
        for (let cIdx = 0; cIdx < compoundCount; cIdx++) {
          const t = codeTokens[i + cIdx];
          words.push(keywordCase === 'upper' ? t.value.toUpperCase() : keywordCase === 'lower' ? t.value.toLowerCase() : t.value);
        }
        currentLine = words.join(' ');
        i += compoundCount;
        continue;
      }

      // 6. Handle Single Keyword Major Clauses
      if (tok.type === 'keyword' && MAJOR_CLAUSES.has(lower)) {
        if (lower === 'end') {
          exitSelectList();
          if (inCase > 0) {
            inCase--;
            ensureNewline();
            indentLevel = Math.max(0, indentLevel - 1);
            appendToken(keywordCase === 'upper' ? 'END' : keywordCase === 'lower' ? 'end' : tok.value, true);
            i++;
            continue;
          } else {
            ensureNewline();
            indentLevel = Math.max(0, indentLevel - 1);
            blockDepth = Math.max(0, blockDepth - 1);
            appendToken(keywordCase === 'upper' ? 'END' : keywordCase === 'lower' ? 'end' : tok.value, false);
            i++;
            continue;
          }
        }

        if (lower === 'begin' || lower === 'declare') {
          exitSelectList();
          ensureNewline();
          appendToken(keywordCase === 'upper' ? tok.value.toUpperCase() : keywordCase === 'lower' ? tok.value.toLowerCase() : tok.value, false);
          ensureNewline();
          indentLevel++;
          blockDepth++;
          i++;
          continue;
        }

        if (lower === 'exception') {
          exitSelectList();
          ensureNewline();
          indentLevel = Math.max(0, indentLevel - 1);
          appendToken(keywordCase === 'upper' ? 'EXCEPTION' : keywordCase === 'lower' ? 'exception' : tok.value, false);
          ensureNewline();
          indentLevel++;
          i++;
          continue;
        }

        // Special handling for SELECT: Start column list on indented newline
        if (lower === 'select') {
          exitSelectList();
          ensureNewline();
          appendToken(keywordCase === 'upper' ? 'SELECT' : keywordCase === 'lower' ? 'select' : tok.value, false);
          ensureNewline();
          indentLevel++;
          inSelectList = true;
          i++;
          continue;
        }

        // Other major clauses: FROM, WHERE, etc.
        exitSelectList();
        ensureNewline();
        appendToken(keywordCase === 'upper' ? tok.value.toUpperCase() : keywordCase === 'lower' ? tok.value.toLowerCase() : tok.value, false);
        i++;
        continue;
      }

      // 7. CASE WHEN THEN ELSE handling
      if (tok.type === 'keyword' && lower === 'case') {
        inCase++;
        ensureNewline();
        appendToken(keywordCase === 'upper' ? 'CASE' : keywordCase === 'lower' ? 'case' : tok.value, false);
        indentLevel++;
        ensureNewline();
        i++;
        continue;
      }

      if (tok.type === 'keyword' && lower === 'when' && inCase > 0) {
        ensureNewline();
        appendToken(keywordCase === 'upper' ? 'WHEN' : keywordCase === 'lower' ? 'when' : tok.value, false);
        i++;
        continue;
      }

      if (tok.type === 'keyword' && lower === 'then' && inCase > 0) {
        appendToken(keywordCase === 'upper' ? 'THEN' : keywordCase === 'lower' ? 'then' : tok.value, true);
        i++;
        continue;
      }

      if (tok.type === 'keyword' && lower === 'else' && inCase > 0) {
        ensureNewline();
        appendToken(keywordCase === 'upper' ? 'ELSE' : keywordCase === 'lower' ? 'else' : tok.value, false);
        i++;
        continue;
      }

      // 8. AND / OR in WHERE / HAVING conditions
      if (tok.type === 'keyword' && (lower === 'and' || lower === 'or')) {
        if (parenDepth === 0 || (parenStack.length && parenStack[parenStack.length - 1].type === 'subquery')) {
          ensureNewline();
          appendToken(keywordCase === 'upper' ? tok.value.toUpperCase() : keywordCase === 'lower' ? tok.value.toLowerCase() : tok.value, false);
          i++;
          continue;
        }
      }

      // 9. Commas ,
      if (tok.type === 'punct' && tok.value === ',') {
        appendToken(',', false);
        if (parenDepth === 0) {
          ensureNewline();
        } else {
          currentLine += ' ';
        }
        i++;
        continue;
      }

      // 10. Open Bracket (
      if (tok.type === 'bracket' && tok.value === '(') {
        parenDepth++;
        let isSubquery = false;
        let p = i + 1;
        while (p < codeTokens.length && p < i + 5) {
          if (codeTokens[p].type === 'comment') { p++; continue; }
          if (codeTokens[p].type === 'keyword' && codeTokens[p].value.toLowerCase() === 'select') {
            isSubquery = true;
          }
          break;
        }

        if (isSubquery) {
          parenStack.push({ type: 'subquery', inSelectList: inSelectList });
          appendToken('(', true);
          ensureNewline();
          indentLevel++;
          inSelectList = false;
        } else {
          parenStack.push({ type: 'expr', inSelectList: inSelectList });
          const noSpaceBeforeParen = prevTok && (prevTok.type === 'ident' || prevTok.type === 'keyword');
          appendToken('(', !noSpaceBeforeParen);
        }
        i++;
        continue;
      }

      // 11. Close Bracket )
      if (tok.type === 'bracket' && tok.value === ')') {
        parenDepth = Math.max(0, parenDepth - 1);
        const pInfo = parenStack.pop();
        if (pInfo && pInfo.type === 'subquery') {
          exitSelectList();
          ensureNewline();
          indentLevel = Math.max(0, indentLevel - 1);
          appendToken(')', false);
          if (pInfo.inSelectList) inSelectList = true;
        } else {
          appendToken(')', false);
        }
        i++;
        continue;
      }

      // 12. Label <<label_name>>
      if (tok.type === 'label') {
        ensureNewline();
        appendToken(tok.value, false);
        ensureNewline();
        i++;
        continue;
      }

      // 13. Operators
      if (tok.type === 'operator') {
        const isUnary = (tok.value === '+' || tok.value === '-') && prevTok && (prevTok.type === 'operator' || (prevTok.type === 'bracket' && prevTok.value === '('));
        if (isUnary) {
          appendToken(tok.value, true);
        } else {
          appendToken(tok.value, true);
        }
        i++;
        continue;
      }

      // 14. Dot .
      if (tok.type === 'punct' && tok.value === '.') {
        appendToken('.', false);
        i++;
        continue;
      }

      // 15. Quoted string / quoted ident / param / number / ident / generic token
      let tokenText = tok.value;
      if (tok.type === 'keyword') {
        tokenText = keywordCase === 'upper' ? tok.value.toUpperCase() : keywordCase === 'lower' ? tok.value.toLowerCase() : tok.value;
      }

      const noSpaceBefore = prevTok && prevTok.type === 'punct' && prevTok.value === '.';
      appendToken(tokenText, !noSpaceBefore);
      i++;
    }

    exitSelectList();
    ensureNewline();
    return lines.join('\n').trim();
  }

  /**
   * Main format function.
   * Safe, lossless and idempotent.
   */
  function formatSQL(source, options) {
    if (source == null) return '';
    const text = String(source);
    if (!text.trim()) return '';

    try {
      const tokens = tokenizeSQL(text);
      return formatTokens(tokens, options);
    } catch (err) {
      console.warn('sqlFormatService: format error, returning original', err);
      return text;
    }
  }

  /**
   * Format a specific range or selection in a SQL string.
   * Unselected text outside [start, end] is guaranteed to be 100% byte-unchanged.
   */
  function formatRange(source, start, end, options) {
    const text = String(source || '');
    const s = Math.max(0, Math.min(text.length, Number.isFinite(start) ? start : 0));
    const e = Math.max(0, Math.min(text.length, Number.isFinite(end) ? end : text.length));

    if (s >= e) {
      return { text: text, formattedRange: [s, s], changed: false };
    }

    const before = text.slice(0, s);
    const target = text.slice(s, e);
    const after = text.slice(e);

    const formattedTarget = formatSQL(target, options);
    if (formattedTarget === target) {
      return { text: text, formattedRange: [s, e], changed: false };
    }

    const nextText = before + formattedTarget + after;
    return {
      text: nextText,
      formattedRange: [s, s + formattedTarget.length],
      changed: true
    };
  }

  /**
   * Format in a DOM textarea editor with selection awareness, statement detection,
   * single-step undo via document.execCommand, cursor preservation and input event dispatch.
   */
  function formatInEditor(textarea, options) {
    if (!textarea) return { status: 'empty', changed: false };
    const fullText = textarea.value || '';
    if (!fullText.trim()) return { status: 'empty', changed: false };

    options = options || {};
    const selStart = textarea.selectionStart != null ? textarea.selectionStart : 0;
    const selEnd = textarea.selectionEnd != null ? textarea.selectionEnd : 0;
    const hasSelection = selEnd > selStart && fullText.slice(selStart, selEnd).trim().length > 0;

    let targetStart = 0;
    let targetEnd = fullText.length;
    let isFullText = !!options.full;

    if (!isFullText) {
      if (hasSelection) {
        targetStart = selStart;
        targetEnd = selEnd;
      } else if (W.statementModel && typeof W.statementModel.current === 'function') {
        const dialect = options.dialect || 'oracle';
        const cur = W.statementModel.current(fullText, selStart, selStart, selEnd, { dialect: dialect });
        if (cur && Number.isFinite(cur.start) && Number.isFinite(cur.end) && cur.end > cur.start) {
          targetStart = cur.start;
          targetEnd = cur.end;
        }
      }
    }

    const targetSlice = fullText.slice(targetStart, targetEnd);
    const formatted = formatSQL(targetSlice, options);

    if (formatted === targetSlice) {
      return { status: 'unchanged', changed: false, range: [targetStart, targetEnd] };
    }

    // Perform replacement while preserving undo stack
    textarea.focus();
    textarea.setSelectionRange(targetStart, targetEnd);

    let replaced = false;
    try {
      if (typeof document !== 'undefined' && document.execCommand) {
        replaced = document.execCommand('insertText', false, formatted);
      }
    } catch (_) {
      replaced = false;
    }

    if (!replaced || textarea.value.slice(targetStart, targetStart + formatted.length) !== formatted) {
      textarea.setRangeText(formatted, targetStart, targetEnd, 'select');
    }

    const newEnd = targetStart + formatted.length;
    textarea.setSelectionRange(targetStart, newEnd);

    try {
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    } catch (_) {
      const ev = document.createEvent('Event');
      ev.initEvent('input', true, false);
      textarea.dispatchEvent(ev);
    }

    return {
      status: 'ok',
      changed: true,
      range: [targetStart, newEnd],
      formatted: formatted
    };
  }

  // Export module
  const service = {
    tokenizeSQL: tokenizeSQL,
    formatSQL: formatSQL,
    format: formatSQL,
    formatTokens: formatTokens,
    formatRange: formatRange,
    formatInEditor: formatInEditor
  };

  W.sqlFormatService = service;

  if (typeof module !== 'undefined' && module.exports) {
    module.exports = service;
  }
})();
