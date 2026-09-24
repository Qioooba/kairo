/* Database Workbench feature layer.
 *
 * This module intentionally sits beside pages/database.js instead of reaching
 * into its private state.  The page owns query execution and result rendering;
 * this layer owns editor ergonomics, local drafts and UI contracts.  Backends
 * can subscribe to the custom events (or install an adapter) without forcing a
 * second frontend implementation.
 */
(function () {
  'use strict';

  const K = window.Kairo = window.Kairo || {};
  const W = K.workbench = K.workbench || {};
  const F = K.databaseFeatures = K.databaseFeatures || {};
  const STORAGE_HISTORY = 'kairo:database:history:v2';
  const STORAGE_DRAFTS = 'kairo:database:file-drafts:v1';
  const MAX_HISTORY = 200;
  const MAX_HISTORY_SQL_LEN = 30000;
  const MAX_SQL_HIGHLIGHT = 220000;
  const MAX_IMPORT_ROWS = 10000;
  const SQL_WORD = /[A-Za-z_$#\u0080-\uffff]/;
  const SQL_WORD_CONT = /[A-Za-z0-9_$#\u0080-\uffff]/;
  const KEYWORDS = new Set((
    'select from where with as distinct all union intersect minus except into values insert update delete merge '
    + 'set returning output join left right full inner outer cross natural on using group by having order asc desc '
    + 'limit offset fetch first next rows only for share lock wait nowait skip locked connect start prior '
    + 'case when then else end exists in not and or is null like between regexp escape over partition window '
    + 'create alter drop truncate table view materialized index unique primary key foreign references constraint '
    + 'sequence synonym trigger procedure function package body declare begin exception loop while repeat until '
    + 'if elsif else raise return cursor type record bulk collect forall open close execute immediate commit rollback '
    + 'grant revoke analyze explain describe show use database schema call begin end '
    + 'count sum avg min max cast convert coalesce nvl nvl2 decode ifnull nullif to_char to_date to_number '
    + 'varchar varchar2 nvarchar nchar number numeric decimal integer int bigint smallint tinyint date datetime '
    + 'timestamp time clob blob raw json xml boolean true false sysdate systimestamp current_date current_timestamp '
    + 'dual rownum rowid regexp_substr regexp_replace substr substring instr length trim ltrim rtrim lower upper '
    + 'listen notify returning limit offset fetch next now '
  ).split(/\s+/).filter(Boolean));
  const CLAUSE_BREAKS = new Set((
    'select from where group having order union intersect minus except join left right full inner outer cross '
    + 'on using values set returning into limit offset fetch connect start model qualify window'
  ).split(/\s+/));
  const BLOCK_OPEN = new Set(['begin', 'declare', 'loop', 'if', 'case', 'package', 'procedure', 'function', 'trigger']);
  const BLOCK_CLOSE = new Set(['end', 'exception', 'elsif', 'else']);
  const TYPE_NUMBER = /^(number|numeric|decimal|int(?:eger)?|bigint|smallint|tinyint|float|double|real|binary_float|binary_double|serial)/i;
  const TYPE_DATE = /^(date|datetime|timestamp|time)/i;
  const TYPE_BOOL = /^(bool(?:ean)?|bit)$/i;

  // 深链接初始 hash 快照：SPA 的页签管理器在激活路由时会同步把 hash 规范化成
  // `#/database`（tabs.hashFor + history.replaceState），等工作区渲染完再调
  // applyDeepLink 时 location.hash 里的 ?sql=… 已经没了 —— 这正是"新窗口打开后没有 SQL"
  // 的根因。本模块在 <script> 解析期就执行，早于路由改写，因此在这里快照。
  const initialDeepHash = (function () {
    try { return String(location.hash || ''); } catch (_) { return ''; }
  })();
  const state = {
    observed: false,
    view: null,
    root: null,
    editor: null,
    highlight: null,
    ac: null,
    acItems: [],
    acIndex: 0,
    acStart: 0,
    acEnd: 0,
    acRequest: 0,
    raf: 0,
    pendingRun: null,
    runObserver: null,
    runObserverRoot: null,
    editorInput: null,
    fileInput: null,
    importInput: null,
    modal: null,
    focusedBeforeModal: null,
    history: [],
    historyKey: '',
    bindings: Object.create(null),
    parameterScanPending: '',
    parameterScanText: '',
    gridMutations: [],
    fieldCache: Object.create(null),
    sourceCatalog: Object.create(null),
    sourceCatalogLoaded: false,
    deepLinkApplied: '',
    adapters: {
      script: null,
      bind: null,
      grid: null,
      import: null,
      compile: null,
      loadSource: null,
      ddl: null
    }
  };

  function esc(value) {
    const fn = K.core && K.core.escapeHtml;
    if (fn) return fn(String(value == null ? '' : value));
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c];
    });
  }
  function id(name) { return document.getElementById(name); }
  function toast(message, type) {
    if (K.core && K.core.toast) K.core.toast(String(message), type || 'info');
  }
  function api(method, path, body) {
    if (K.api && K.api.api) return K.api.api(method, path, body);
    return fetch(path, {
      method: method,
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body)
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        if (!response.ok) {
          const error = new Error(data && data.error ? data.error : 'HTTP ' + response.status);
          error.status = response.status;
          error.data = data;
          throw error;
        }
        return data;
      });
    });
  }
  // Keep endpoint churn out of the feature UI.  Newer servers expose the
  // typed script/grid routes while older workbench builds only have batch;
  // a 404 is the one safe signal that permits a compatibility fallback.
  function apiFallback(method, paths, body) {
    const candidates = Array.isArray(paths) ? paths : [paths];
    let index = 0;
    const next = function () {
      const path = candidates[index++];
      return api(method, path, body).catch(function (error) {
        if (error && Number(error.status) === 404 && index < candidates.length) return next();
        throw error;
      });
    };
    return next();
  }
  function emit(type, detail) {
    let event;
    try { event = new CustomEvent(type, { detail: detail }); }
    catch (_) { event = document.createEvent('CustomEvent'); event.initCustomEvent(type, false, false, detail); }
    window.dispatchEvent(event);
    return event;
  }
  function sourceId() {
    const select = id('db-source');
    return select && select.value ? String(select.value) : '';
  }
  function sourceName() {
    const select = id('db-source');
    if (!select || !select.selectedOptions || !select.selectedOptions[0]) return '';
    const item = state.sourceCatalog[select.value];
    return item && item.name ? String(item.name) : select.selectedOptions[0].textContent.trim();
  }
  function sourceCatalogItem() {
    const select = id('db-source'), option = select && select.selectedOptions && select.selectedOptions[0];
    const item = option && state.sourceCatalog[option.value];
    return item || (option && option.dataset ? { id: option.value, name: option.textContent.trim(), environment: option.dataset.environment || '', read_only: option.dataset.readOnly === '1', allow_ddl: option.dataset.allowDdl === '1' } : null);
  }
  function sourceIsProduction() {
    const item = sourceCatalogItem();
    return !!(item && String(item.environment || '').toLowerCase() === 'production');
  }
  function sourceIsReadOnly() {
    const item = sourceCatalogItem();
    return !!(item && (item.read_only === true || item.readOnly === true));
  }
  function dialect() {
    const badge = id('db-source-badge');
    const value = badge && badge.textContent ? badge.textContent.trim().toLowerCase() : '';
    return value.indexOf('mysql') >= 0 ? 'mysql' : value.indexOf('redis') >= 0 ? 'redis' : 'oracle';
  }
  function editor() { return id('db-sql'); }
  function sqlText() { const e = editor(); return e ? String(e.value || '') : ''; }
  function activeSession() {
    const getter = K.database && K.database.getActiveSession;
    if (typeof getter === 'function') {
      try {
        const value = getter();
        if (value) return value;
      } catch (_) {}
    }
    const e = editor();
    return {
      sessionId: (e && e.dataset && e.dataset.sessionId) || (state.root && state.root.dataset && state.root.dataset.sessionId) || '',
      sourceId: sourceId()
    };
  }
  function syncMutationTransaction(detail, result, forceSuccess) {
    const database = K.database;
    if (!database || typeof database.markTransactionPending !== 'function' || !detail || !detail.sessionId) return;
    const payload = result && result.result ? result.result : result;
    if (!payload || (payload.transaction_pending == null && !payload.committed && !payload.rolled_back)) return;
    const pending = detail.commit ? false : payload && payload.transaction_pending != null ? !!payload.transaction_pending : !(payload && (payload.committed || payload.rolled_back));
    database.markTransactionPending(pending, detail.sessionId);
    if (typeof database.refreshTransactionState === 'function') database.refreshTransactionState();
  }
  function dispatchEditorInput(e) {
    if (!e) return;
    try { e.dispatchEvent(new Event('input', { bubbles: true })); }
    catch (_) { const ev = document.createEvent('Event'); ev.initEvent('input', true, false); e.dispatchEvent(ev); }
  }
  function safeJSONParse(value, fallback) {
    try { return JSON.parse(value); } catch (_) { return fallback; }
  }
  function schedule(fn) {
    if (window.requestAnimationFrame) return window.requestAnimationFrame(fn);
    return setTimeout(fn, 0);
  }

  /** Tokenize SQL without ever interpreting text inside strings/comments. */
  function tokenizeSQL(source) {
    const text = String(source == null ? '' : source);
    const tokens = [];
    let i = 0;
    function push(type, start, end, extra) {
      const token = { type: type, value: text.slice(start, end), start: start, end: end };
      if (extra) Object.keys(extra).forEach(function (key) { token[key] = extra[key]; });
      tokens.push(token);
    }
    while (i < text.length) {
      const start = i;
      const c = text[i], n = text[i + 1];
      if (c === '-' && n === '-') {
        i += 2;
        while (i < text.length && text[i] !== '\n') i++;
        push('comment', start, i);
        continue;
      }
      if (c === '#' && (i === 0 || /\s/.test(text[i - 1]))) {
        i++;
        while (i < text.length && text[i] !== '\n') i++;
        push('comment', start, i);
        continue;
      }
      if (c === '/' && n === '*') {
        i += 2;
        while (i < text.length && !(text[i] === '*' && text[i + 1] === '/')) i++;
        if (i < text.length) i += 2;
        push('comment', start, i);
        continue;
      }
      // Oracle q'[ ... ]' and the other paired delimiters.
      if ((c === 'q' || c === 'Q') && n === "'" && i + 2 < text.length) {
        const open = text[i + 2];
        const close = ({ '[': ']', '{': '}', '(': ')', '<': '>' })[open] || open;
        const marker = close + "'";
        const end = text.indexOf(marker, i + 3);
        if (end >= 0) {
          i = end + marker.length;
          push('string', start, i);
          continue;
        }
      }
      // PostgreSQL dollar quote, e.g. $$body$$ or $tag$body$tag$.
      if (c === '$') {
        const markerMatch = text.slice(i).match(/^\$[A-Za-z_0-9]*\$/);
        if (markerMatch) {
          const marker = markerMatch[0], end = text.indexOf(marker, i + marker.length);
          if (end >= 0) {
            i = end + marker.length;
            push('string', start, i);
            continue;
          }
        }
      }
      if (c === "'" || c === '"' || c === '`') {
        const quote = c;
        i++;
        while (i < text.length) {
          if (text[i] === quote) {
            if (text[i + 1] === quote) { i += 2; continue; }
            i++;
            break;
          }
          if (text[i] === '\\' && quote !== '`') i += 2;
          else i++;
        }
        push(quote === "'" ? 'string' : 'quoted-ident', start, i);
        continue;
      }
      if (c === ':' && (n === ':' || n === '=')) { i += 2; push('operator', start, i); continue; }
      if (c === ':' && SQL_WORD_CONT.test(text[i + 1] || '')) {
        i += 1;
        while (i < text.length && SQL_WORD_CONT.test(text[i])) i++;
        push('param', start, i, { name: text.slice(start + 1, i) });
        continue;
      }
      if (c === '?' || (c === '{' && n === '{')) {
        if (c === '{') {
          const end = text.indexOf('}}', i + 2);
          if (end >= 0) { i = end + 2; push('param', start, i, { name: text.slice(start + 2, end).trim() }); continue; }
        }
        i++;
        push('param', start, i, { name: '?' });
        continue;
      }
      if (/\s/.test(c)) {
        i++;
        while (i < text.length && /\s/.test(text[i])) i++;
        push('space', start, i);
        continue;
      }
      if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(n || ''))) {
        i++;
        while (i < text.length && /[0-9A-Fa-f_xX.eE+-]/.test(text[i])) i++;
        push('number', start, i);
        continue;
      }
      if (SQL_WORD.test(c)) {
        i++;
        while (i < text.length && SQL_WORD_CONT.test(text[i])) i++;
        const value = text.slice(start, i);
        push(KEYWORDS.has(value.toLowerCase()) ? 'keyword' : 'ident', start, i);
        continue;
      }
      if ('()[]{}'.indexOf(c) >= 0) { i++; push('bracket', start, i); continue; }
      if ('=<>!+-*/%|&^~'.indexOf(c) >= 0) {
        i++;
        if (i < text.length && '=<>|&'.indexOf(text[i]) >= 0) i++;
        push('operator', start, i);
        continue;
      }
      i++;
      push('punct', start, i);
    }
    return tokens;
  }

  function completionPrefix(text, cursor) {
    const source = String(text || ''), pos = Math.max(0, Math.min(source.length, Number(cursor) || 0));
    const tokens = tokenizeSQL(source);
    for (let i = 0; i < tokens.length; i++) {
      const token = tokens[i];
      if ((token.type === 'string' || token.type === 'comment' || token.type === 'quoted-ident') && pos > token.start && pos <= token.end) return null;
    }
    const before = source.slice(0, pos);
    const match = before.match(/(?:([A-Za-z_$#\u0080-\uffff][A-Za-z0-9_$#\u0080-\uffff]*)\.)?([A-Za-z_$#\u0080-\uffff][A-Za-z0-9_$#\u0080-\uffff]*)$/);
    if (!match) return null;
    return { qualifier: match[1] || '', prefix: match[2], start: pos - match[2].length, end: pos };
  }

  function identifierSets() {
    const objects = new Set(), fields = new Set();
    document.querySelectorAll('#db-objects .db-object-name, .db-object-name').forEach(function (node) { if (node.textContent.trim()) objects.add(node.textContent.trim()); });
    document.querySelectorAll('.db-field-name, .db-field-row strong, .db-obj-table .db-field-name').forEach(function (node) { if (node.textContent.trim()) fields.add(node.textContent.trim()); });
    return { objects: objects, fields: fields };
  }
  function highlightSQL(source, options) {
    options = options || {};
    const text = String(source == null ? '' : source);
    if (text.length > MAX_SQL_HIGHLIGHT) return esc(text);
    const known = identifierSets();
    const tokens = tokenizeSQL(text);
    const chunks = [];
    tokens.forEach(function (token, index) {
      const body = esc(token.value);
      let cls = '';
      if (token.type === 'keyword') cls = 'db-pro-sql-kw';
      else if (token.type === 'string' || token.type === 'quoted-ident') cls = token.type === 'string' ? 'db-pro-sql-string' : 'db-pro-sql-quoted';
      else if (token.type === 'comment') cls = 'db-pro-sql-comment';
      else if (token.type === 'number') cls = 'db-pro-sql-number';
      else if (token.type === 'param') cls = 'db-pro-sql-param';
      else if (token.type === 'bracket') cls = 'db-pro-sql-bracket';
      else if (token.type === 'operator') cls = 'db-pro-sql-operator';
      else if (token.type === 'ident') {
        const next = tokens[index + 1];
        const valueLower = token.value.toLowerCase();
        if (next && next.type === 'punct' && next.value === '(') cls = 'db-pro-sql-function';
        else if (known.objects.has(token.value) || known.objects.has(valueLower)) cls = 'db-pro-sql-object';
        else if (known.fields.has(token.value) || known.fields.has(valueLower)) cls = 'db-pro-sql-field';
      }
      chunks.push(cls ? '<span class="' + cls + '">' + body + '</span>' : body);
    });
    return chunks.join('') || ' ';
  }

  function appendSpace(line) {
    if (!line) return '';
    const last = line.slice(-1);
    return /\s/.test(last) || last === '(' || last === '[' || last === '.' ? line : line + ' ';
  }
  function formatSQL(source, options) {
    if (W.sqlFormatService && typeof W.sqlFormatService.formatSQL === 'function') {
      return W.sqlFormatService.formatSQL(source, options);
    }
    const text = String(source == null ? '' : source).replace(/\r\n?/g, '\n');
    if (!text.trim()) return '';
    const tokens = tokenizeSQL(text);
    const lines = [], indentUnit = '  ';
    let indent = 0, line = '', bracketDepth = 0;
    let lastType = '', lastValue = '';
    function flush(force) {
      const value = line.replace(/[ \t]+$/g, '');
      if (value || force) lines.push(indentUnit.repeat(Math.max(0, indent)) + value);
      line = '';
    }
    function word(value) { return /^[A-Za-z_$#\u0080-\uffff]/.test(value); }
    function beforeWord() {
      if (line) line = appendSpace(line);
    }
    tokens.forEach(function (token, index) {
      const value = token.value, lower = value.toLowerCase();
      if (token.type === 'space') return;
      if (token.type === 'comment') {
        if (line.trim()) beforeWord();
        line += value;
        if (value.indexOf('\n') >= 0 || value.indexOf('--') === 0 || value.indexOf('#') === 0) {
          const pieces = line.split('\n');
          pieces.forEach(function (piece, i) { if (i < pieces.length - 1) { line = piece; flush(false); } else line = piece; });
          flush(false);
        } else line += ' ';
        lastType = token.type; lastValue = value;
        return;
      }
      if (token.type === 'keyword' && BLOCK_CLOSE.has(lower)) {
        if (lower === 'end' || lower === 'exception' || lower === 'else' || lower === 'elsif') {
          if (line.trim()) flush(false);
          if (lower === 'end') indent = Math.max(0, indent - 1);
        }
        if (lower === 'exception' || lower === 'else' || lower === 'elsif') flush(false);
      }
      if (token.type === 'keyword' && CLAUSE_BREAKS.has(lower) && line.trim()) flush(false);
      if (token.type === 'punct' && value === ';') {
        line = line.replace(/[ \t]+$/g, '') + ';';
        flush(false);
        lastType = token.type; lastValue = value;
        return;
      }
      if (token.type === 'punct' && value === ',') {
        line = line.replace(/[ \t]+$/g, '') + ',';
        if (bracketDepth === 0) flush(false); else line += ' ';
        lastType = token.type; lastValue = value;
        return;
      }
      if (token.type === 'bracket' && (value === ')' || value === ']' || value === '}')) {
        bracketDepth = Math.max(0, bracketDepth - 1);
        line = line.replace(/[ \t]+$/g, '') + value;
      } else if (token.type === 'bracket' && (value === '(' || value === '[' || value === '{')) {
        bracketDepth++;
        if (word(lastValue) || lastValue === ')') line = line.replace(/[ \t]+$/g, '') + value;
        else line += value;
      } else if (token.type === 'operator') {
        line = line.replace(/[ \t]+$/g, '');
        if (line) line += ' ';
        line += value + ' ';
      } else if (token.type === 'punct' && value === '.') {
        line = line.replace(/[ \t]+$/g, '') + '.';
      } else {
        if (token.type === 'keyword') beforeWord();
        else if (token.type === 'ident' || token.type === 'number' || token.type === 'string' || token.type === 'quoted-ident' || token.type === 'param') beforeWord();
        line += token.type === 'keyword' ? value.toUpperCase() : value;
      }
      if (token.type === 'keyword' && BLOCK_OPEN.has(lower) && String(lastValue).toLowerCase() !== 'end') indent++;
      lastType = token.type; lastValue = value;
      // Keep long SELECT lists readable but do not split function arguments.
      const next = tokens[index + 1];
      if (token.type === 'keyword' && lower === 'select' && next && next.type !== 'space') line += ' ';
    });
    if (line.trim()) flush(false);
    // The standalone Oracle slash is a block delimiter; never leave it glued
    // to END or a comment when formatting a PL/SQL script.
    return lines.join('\n')
      .replace(/[ \t]+\n/g, '\n')
      .replace(/\n{3,}/g, '\n\n')
      .replace(/\n[ \t]*\//g, '\n/')
      .trim();
  }

  function splitStatements(source) {
    if (W.statementModel && W.statementModel.split) return W.statementModel.split(String(source || ''), { dialect: dialect() });
    const text = String(source || ''), result = [], tokens = tokenizeSQL(text);
    let start = 0;
    tokens.forEach(function (token) {
      if (token.type === 'punct' && token.value === ';') {
        const value = text.slice(start, token.end);
        if (value.trim()) result.push({ start: start, end: token.end, text: value, index: result.length });
        start = token.end;
      }
    });
    if (text.slice(start).trim() || !result.length) result.push({ start: start, end: text.length, text: text.slice(start), index: result.length });
    return result;
  }
  // ---- 绑定参数扫描（DBUI-04）----
  // 高亮可以按 MAX_SQL_HIGHLIGHT 降级；执行所需的参数语义扫描不能降级成“没有参数”。
  const PARAM_SCAN_CHUNK = 64 * 1024;
  const PARAM_SCAN_SYNC_BUDGET_MS = 24;
  const PARAM_NAME = /[A-Za-z0-9_$#\u0080-\uffff]/;

  function nowMs() {
    if (typeof performance !== 'undefined' && performance && typeof performance.now === 'function') return performance.now();
    return Date.now();
  }
  function lexicalOptions() {
    return { dialect: dialect() };
  }
  // 增量参数扫描器：只识别字符串/注释边界与 :name、:1、?、{{name}}，
  // 状态可跨块续扫，因此分块扫描与整段扫描结果完全一致。
  function createParameterScanner() {
    const lex = lexicalOptions();
    const mysql = lex.dialect === 'mysql';
    return {
      mysql: mysql,
      mode: 'normal', // normal | line | block | single | double | backtick | qquote
      quote: '',
      qCloseChar: '',
      qClosePending: false,
      bracePending: false,
      // P2（审核第 7 项）：被块边界截断、尚未结束的 `:name` 参数。
      pendingParam: null
    };
  }
  function scanParameterChunk(chunk, scanner, onParameter, isLastChunk) {
    let startIndex = 0;
    // P2（审核第 7 项）：上一块末尾未结束的参数名在本块续接，确认结束后再提交结果。
    // 否则 `:serialno` 的冒号落在块边界附近时会得到被截断的 ":s"，
    // 而扫描状态仍被标记为完成，用户已填好的 serialno 绑定会被当成失效参数删掉。
    if (scanner.pendingParam) {
      const pending = scanner.pendingParam;
      scanner.pendingParam = null;
      let j = 0;
      while (j < chunk.length && PARAM_NAME.test(chunk[j])) j++;
      pending.name += chunk.slice(0, j);
      pending.token += chunk.slice(0, j);
      onParameter({ name: pending.name, token: pending.token });
      startIndex = j;
    }
    for (let i = startIndex; i < chunk.length; i++) {
      const c = chunk[i], n = chunk[i + 1] || '';
      if (scanner.mode === 'line') {
        if (c === '\n') scanner.mode = 'normal';
        continue;
      }
      if (scanner.mode === 'block') {
        if (c === '*' && n === '/') { scanner.mode = 'normal'; i++; }
        continue;
      }
      if (scanner.mode === 'qquote') {
        // q-quote 的结束符是两个字符（例如 ]'），跨块时用 pending 标记衔接。
        if (scanner.qClosePending) {
          scanner.qClosePending = false;
          if (c === "'") { scanner.mode = 'normal'; continue; }
        }
        if (c === scanner.qCloseChar) {
          if (n === "'") { scanner.mode = 'normal'; i++; continue; }
          if (n === '') { scanner.qClosePending = true; continue; }
        }
        continue;
      }
      if (scanner.mode === 'single' || scanner.mode === 'double' || scanner.mode === 'backtick') {
        const quote = scanner.quote;
        if (c === quote) {
          if (n === quote) { i++; continue; }
          scanner.mode = 'normal';
          continue;
        }
        if (scanner.mysql && quote !== '`' && c === '\\') { i++; continue; }
        continue;
      }
      // normal
      if (c === '-' && n === '-') { scanner.mode = 'line'; i++; continue; }
      if (scanner.mysql && c === '#' && (i === 0 || /\s/.test(chunk[i - 1]))) { scanner.mode = 'line'; continue; }
      if (c === '/' && n === '*') { scanner.mode = 'block'; i++; continue; }
      if ((c === 'q' || c === 'Q') && n === "'" && !scanner.mysql && i + 2 < chunk.length) {
        const opener = chunk[i + 2];
        scanner.mode = 'qquote';
        scanner.qCloseChar = ({ '[': ']', '{': '}', '(': ')', '<': '>' }[opener] || opener);
        i += 2;
        continue;
      }
      if ((c === 'n' || c === 'N') && (n === 'q' || n === 'Q') && chunk[i + 2] === "'" && !scanner.mysql && i + 3 < chunk.length) {
        const opener = chunk[i + 3];
        scanner.mode = 'qquote';
        scanner.qCloseChar = ({ '[': ']', '{': '}', '(': ')', '<': '>' }[opener] || opener);
        i += 3;
        continue;
      }
      if ((c === 'n' || c === 'N') && n === "'") {
        scanner.mode = 'single';
        scanner.quote = "'";
        i++;
        continue;
      }
      if (c === "'" || c === '"' || c === '`') {
        scanner.mode = c === "'" ? 'single' : c === '"' ? 'double' : 'backtick';
        scanner.quote = c;
        continue;
      }
      if (scanner.bracePending) {
        scanner.bracePending = false;
        if (c === '{') {
          const end = chunk.indexOf('}}', i + 1);
          if (end >= 0) {
            const name = chunk.slice(i + 1, end).trim();
            if (name) onParameter({ name: name, token: '{{' + name + '}}' });
            i = end + 1;
            continue;
          }
        }
      }
      if (c === '{' && n === '{') {
        const end = chunk.indexOf('}}', i + 2);
        if (end >= 0) {
          const name = chunk.slice(i + 2, end).trim();
          if (name) onParameter({ name: name, token: chunk.slice(i, end + 2) });
          i = end + 1;
          continue;
        }
        continue;
      }
      if (c === '{' && n === '') { scanner.bracePending = true; continue; }
      if (c === ':' && PARAM_NAME.test(n)) {
        let j = i + 1;
        while (j < chunk.length && PARAM_NAME.test(chunk[j])) j++;
        if (j >= chunk.length && !isLastChunk) {
          // 名字一直延伸到块末尾且后面还有内容：无法确认参数名是否结束，
          // 留到下一块续扫（审核第 7 项）。
          scanner.pendingParam = { name: chunk.slice(i + 1, j), token: chunk.slice(i, j) };
          i = j - 1;
          continue;
        }
        onParameter({ name: chunk.slice(i + 1, j), token: chunk.slice(i, j) });
        i = j - 1;
        continue;
      }
      if (c === '?') {
        onParameter({ name: '?', token: '?' });
        continue;
      }
    }
  }

  // 扫描绑定参数。返回至少 {status:'ready'|'pending'|'error', parameters}。
  // 选项：cursor/selectionStart/selectionEnd 决定只扫描将要执行的那条语句；
  //      chunkBudgetMs 控制单次同步预算（0 表示不限时，用于后台续扫）。
  function scanParameters(source, options) {
    const opts = options || {};
    const text = String(source == null ? '' : source);
    const scanner = createParameterScanner();
    const seen = new Set(), result = [];
    let qCount = 0;
    let failed = null;
    const collect = function (param) {
      if (!param || !param.name) return;
      if (param.name === '?') { qCount++; return; }
      const key = String(param.name).toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      result.push({ name: param.name, token: param.token });
    };

    const budget = typeof opts.chunkBudgetMs === 'number' ? opts.chunkBudgetMs : PARAM_SCAN_SYNC_BUDGET_MS;
    const started = nowMs();
    let offset = 0;
    let status = 'ready';
    try {
      while (offset < text.length) {
        const end = Math.min(text.length, offset + PARAM_SCAN_CHUNK);
        scanParameterChunk(text.slice(offset, end), scanner, collect, end >= text.length);
        offset = end;
        if (offset < text.length && budget > 0 && nowMs() - started > budget) {
          status = 'pending';
          break;
        }
      }
    } catch (error) {
      failed = error;
      status = 'error';
    }
    if (failed) {
      return { status: 'error', parameters: [], complete: false, scanned: offset, total: text.length, error: String(failed && failed.message ? failed.message : failed) };
    }
    // 位置参数 ? 与 Oracle 的 :1、:2 使用同一套纯数字键；? 只保留来源 token。
    for (let i = 0; i < qCount; i++) result.push({ name: String(i + 1), token: '?' });
    return { status: status, parameters: result, complete: status === 'ready', scanned: offset, total: text.length };
  }

  // 真正要执行的那条语句（与 database.js 的 editorSQL() 使用同一套 statementModel 语义）。
  function executionText() {
    const text = sqlText();
    const model = W.statementModel;
    const target = editor();
    if (!text || !model || (typeof model.normalize !== 'function' && typeof model.current !== 'function')) return text;
    const cursor = target && Number.isFinite(target.selectionStart) ? target.selectionStart : 0;
    const selectionEnd = target && Number.isFinite(target.selectionEnd) ? target.selectionEnd : cursor;
    try {
      if (typeof model.normalize === 'function') {
        const picked = model.normalize(text, cursor, cursor, selectionEnd, { dialect: dialect() });
        if (picked && typeof picked.text === 'string') return picked.text;
      }
      const current = model.current(text, cursor, cursor, selectionEnd, { dialect: dialect() });
      if (current && typeof current.text === 'string') return current.text;
    } catch (_) {}
    return text;
  }

  function findParameters(source, options) {
    return scanParameters(source, options).parameters;
  }
  function typedParameters(values) {
    values = values || {};
    return Object.keys(values).map(function (name) {
      const value = values[name], text = String(value == null ? '' : value).trim();
      return {
        name: name,
        type: text === '' ? 'null' : /^[-+]?\d+$/.test(text) ? 'int64' : /^[-+]?(?:\d+\.\d*|\.\d+)(?:e[-+]?\d+)?$/i.test(text) ? 'number' : /^(true|false)$/i.test(text) ? 'bool' : 'string',
        value: value,
        null: text === ''
      };
    });
  }
  function allowedParameterNames(params) {
    const allowed = Object.create(null);
    (params || []).forEach(function (param) { allowed[String(param.name).toLowerCase()] = param.name; });
    return allowed;
  }
  // 只有“已完成且对应当前 SQL”的扫描结果才允许删除绑定（DBUI-04）。
  function applyParameterPrune(params) {
    const allowed = allowedParameterNames(params), current = state.bindings || {};
    const next = Object.create(null);
    Object.keys(current).forEach(function (name) {
      const canonical = allowed[String(name).toLowerCase()];
      if (canonical) next[canonical] = current[name];
    });
    state.bindings = next;
  }
  function pruneBindingsAgainst(scan) {
    if (!scan || scan.status !== 'ready') return false;
    applyParameterPrune(Array.isArray(scan.parameters) ? scan.parameters : []);
    return true;
  }
  function scheduleParameterScanContinuation(text) {
    if (state.parameterScanPending === text) return;
    state.parameterScanPending = text;
    schedule(function () {
      if (state.parameterScanPending !== text) return;
      state.parameterScanPending = '';
      // 修订号或编辑器内容已经变化：旧文本的扫描结果不得用于裁剪绑定。
      if (state.parameterScanText !== text || sqlText() !== text) return;
      const scan = scanParameters(text, { chunkBudgetMs: 0 });
      pruneBindingsAgainst(scan);
    });
  }
  function pruneBindings() {
    const text = sqlText();
    state.parameterScanText = text;
    const scan = scanParameters(text);
    if (scan.status !== 'ready') {
      // 扫描未完成：保留原绑定，后台分块续扫后再裁剪。
      scheduleParameterScanContinuation(text);
      return;
    }
    applyParameterPrune(scan.parameters);
  }
  // 执行路径：只按“将要执行的语句”取参数；扫描不可用时保留已有绑定，绝不静默清空。
  function getBoundParametersForExecution() {
    const scan = scanParameters(executionText());
    const bindings = state.bindings || {};
    if (scan.status !== 'ready') return typedParameters(bindings);
    const allowed = allowedParameterNames(scan.parameters);
    const selected = Object.create(null);
    Object.keys(bindings).forEach(function (name) {
      const canonical = allowed[String(name).toLowerCase()];
      if (canonical) selected[canonical] = bindings[name];
    });
    return typedParameters(selected);
  }
  function parseErrorLocation(message) {
    const text = String(message || '');
    let match = text.match(/(?:line|行)\s*[:：]?\s*(\d+)(?:\s*[,，]\s*(?:column|列|col)\s*[:：]?\s*(\d+))?/i);
    if (match) return { line: Number(match[1]), column: Number(match[2] || 1), source: 'line-column' };
    match = text.match(/(?:position|位置)\s*[:：]?\s*(\d+)/i);
    if (match) return { offset: Math.max(0, Number(match[1]) - 1), source: 'position' };
    match = text.match(/at\s+line\s+(\d+)/i);
    if (match) return { line: Number(match[1]), column: 1, source: 'line' };
    return null;
  }
  function locationOffset(source, location) {
    if (!location) return -1;
    if (location.offset != null) return Math.max(0, Math.min(String(source || '').length, location.offset));
    const lines = String(source || '').split('\n'), line = Math.max(1, Number(location.line) || 1), column = Math.max(1, Number(location.column) || 1);
    let offset = 0;
    for (let i = 0; i < line - 1 && i < lines.length; i++) offset += lines[i].length + 1;
    return Math.max(0, Math.min(String(source || '').length, offset + column - 1));
  }
  function jumpToLocation(target, location) {
    const source = target && typeof target.value === 'string' ? target.value : '';
    const offset = locationOffset(source, location);
    if (!target || offset < 0) return false;
    target.focus();
    target.setSelectionRange(offset, Math.min(source.length, offset + 1));
    const line = source.slice(0, offset).split('\n').length;
    const style = window.getComputedStyle ? getComputedStyle(target) : null;
    const lineH = style ? parseFloat(style.lineHeight) || 21 : 21;
    target.scrollTop = Math.max(0, (line - 3) * lineH);
    if (target._syncHighlight) target._syncHighlight();
    scheduleHighlight();
    return true;
  }

  // ---- 查询历史单一仓库（DBUI-06） ----
  // 新增、删除、清空、旧版下拉、结构化弹窗全部读写同一份结构化历史。
  // 无来源的旧版字符串历史只在首次迁移时读取一次，落到显式的
  // “旧版历史／来源未知” 桶并记 unknown；不得编造来源、时间或成功结果。
  const LEGACY_HISTORY_BUCKET = '__legacy__';
  const LEGACY_HISTORY_LABEL = '旧版历史／来源未知';
  const HISTORY_STORE_VERSION = 2;

  function emptyHistoryStore() {
    return { version: HISTORY_STORE_VERSION, migration: {}, buckets: {} };
  }
  function readHistoryStore() {
    let raw = '{}';
    try { raw = localStorage.getItem(STORAGE_HISTORY) || '{}'; } catch (_) {}
    const parsed = safeJSONParse(raw, {});
    const store = emptyHistoryStore();
    if (parsed && typeof parsed === 'object' && parsed.buckets && typeof parsed.buckets === 'object') {
      store.version = Number(parsed.version) || HISTORY_STORE_VERSION;
      store.migration = parsed.migration && typeof parsed.migration === 'object' ? parsed.migration : {};
      store.buckets = parsed.buckets;
    } else if (parsed && typeof parsed === 'object') {
      // 兼容早期 v2 的扁平结构 { [sourceKey]: entries[] }，读取时升级为桶结构。
      Object.keys(parsed).forEach(function (key) { if (Array.isArray(parsed[key])) store.buckets[key] = parsed[key]; });
    }
    return store;
  }
  function writeHistoryStore(store) {
    try { localStorage.setItem(STORAGE_HISTORY, JSON.stringify(store)); } catch (_) { /* storage may be disabled */ }
    return store;
  }
  function normalizeHistoryEntry(entry, bucket) {
    const legacy = bucket === LEGACY_HISTORY_BUCKET;
    return {
      id: (entry && entry.id) || ('q-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7)),
      bucket: bucket,
      sourceId: legacy ? '' : String((entry && entry.sourceId) || bucket || ''),
      sourceName: (entry && entry.sourceName) || (legacy ? LEGACY_HISTORY_LABEL : ''),
      dialect: (entry && entry.dialect) || '',
      startedAt: entry && entry.startedAt != null ? entry.startedAt : null,
      endedAt: entry && entry.endedAt != null ? entry.endedAt : null,
      elapsedMs: entry && entry.elapsedMs != null ? entry.elapsedMs : null,
      status: (entry && entry.status) || 'unknown',
      rows: entry && entry.rows != null ? entry.rows : null,
      error: (entry && entry.error) || '',
      runId: (entry && entry.runId) || '',
      sql: String((entry && entry.sql) || '')
    };
  }
  // 迁移只能发生一次：完成后写入版本标记。清空/删除历史不得清除该标记，
  // 否则旧字符串会在下次加载时被重新导入，形成“清空后自动恢复”。
  function migrateLegacyHistory(store) {
    let strings = [];
    const getter = K.database && typeof K.database.getHistory === 'function' ? K.database.getHistory : null;
    if (getter) { try { strings = getter(); } catch (_) { strings = []; } }
    if (!Array.isArray(strings)) strings = [];
    const bucket = Array.isArray(store.buckets[LEGACY_HISTORY_BUCKET]) ? store.buckets[LEGACY_HISTORY_BUCKET] : [];
    const known = new Set(bucket.map(function (item) { return String((item && item.sql) || ''); }));
    strings.forEach(function (value) {
      const text = String(value == null ? '' : value).trim();
      if (!text || known.has(text)) return;
      known.add(text);
      bucket.push(normalizeHistoryEntry({
        id: 'legacy-' + Math.random().toString(36).slice(2, 9),
        sourceName: LEGACY_HISTORY_LABEL, status: 'unknown',
        startedAt: null, endedAt: null, elapsedMs: null, rows: null, error: '', sql: text
      }, LEGACY_HISTORY_BUCKET));
    });
    store.buckets[LEGACY_HISTORY_BUCKET] = bucket.slice(0, MAX_HISTORY);
    store.migration = Object.assign({}, store.migration, { legacyStrings: 1, legacyMigratedAt: Date.now() });
    return store;
  }
  function historyStore() {
    const store = readHistoryStore();
    if (store.migration && store.migration.legacyStrings) return store;
    migrateLegacyHistory(store);
    return writeHistoryStore(store);
  }
  function historyKeyFor(targetSourceId) { return String(targetSourceId || sourceId() || '*'); }
  // 桶 → 列表的唯一归一化点（过滤空语句 + 截断 + 逐条归一化）。
  // 同时供 listStoredHistory（整库读之后）和 addHistory（已持有 store 时）复用，
  // 避免为了同一份数据反复 readHistoryStore()。
  function normalizeHistoryBucket(rawList, key) {
    return (Array.isArray(rawList) ? rawList : []).filter(function (entry) { return entry && entry.sql; })
      .slice(0, MAX_HISTORY).map(function (entry) { return normalizeHistoryEntry(entry, key); });
  }
  function listStoredHistory(targetSourceId) {
    const key = historyKeyFor(targetSourceId);
    return normalizeHistoryBucket(historyStore().buckets[key], key);
  }
  // targetStore：调用方已经读过整库时可以直接复用，避免 writeStoredHistory 内部再读一遍。
  function writeStoredHistory(key, list, targetStore) {
    const store = targetStore || historyStore();
    store.buckets[key] = (Array.isArray(list) ? list : []).slice(0, MAX_HISTORY)
      .map(function (entry) { return normalizeHistoryEntry(entry, key); });
    return writeHistoryStore(store);
  }
  // DBUI-06: 「标记 running 遗留 + 发布到内存」只保留这一份实现。
  // 刷新后遗留的 running 记录只能标记为 interrupted，绝不算成功。
  // loadHistory 从盘上读完后用它发布；addHistory 已经持有写盘用的列表，直接复用即可，
  // 不必再把整库 JSON 读回来解析第二遍。
  function adoptHistoryList(list, key) {
    list.forEach(function (entry) {
      if (entry.status !== 'running') return;
      const isPending = state.pendingRuns && state.pendingRuns.has(entry.runId);
      if (!isPending) entry.status = 'interrupted';
    });
    state.historyKey = key;
    state.history = list;
    return state.history;
  }
  function loadHistory(targetSourceId) {
    const key = historyKeyFor(targetSourceId);
    return adoptHistoryList(listStoredHistory(key), key);
  }
  function saveHistory() {
    return writeStoredHistory(state.historyKey || historyKeyFor(), state.history);
  }
  function deleteHistoryEntry(id, targetSourceId) {
    const key = historyKeyFor(targetSourceId || state.historyKey);
    writeStoredHistory(key, listStoredHistory(key).filter(function (entry) { return entry.id !== id; }));
    return loadHistory(key);
  }
  function clearHistory(targetSourceId) {
    const key = historyKeyFor(targetSourceId || state.historyKey);
    writeStoredHistory(key, []);
    return loadHistory(key);
  }
  function addHistory(entry) {
    if (!entry || !String(entry.sql || '').trim()) return;
    const targetSourceId = String(entry.sourceId || sourceId() || '*');
    // 旧版桶只读：不允许把新记录写进去，也不允许借它伪造成当前数据源的历史。
    if (targetSourceId === LEGACY_HISTORY_BUCKET) return;
    let sql = String(entry.sql).trim();
    if (sql.length > MAX_HISTORY_SQL_LEN) {
      sql = sql.slice(0, MAX_HISTORY_SQL_LEN) + '\n/* -- [kairo: 历史记录超长截断] -- */';
    }
    // DBUI-06: 一次 addHistory 只允许一次整库读 + 一次写。
    // 旧实现 listStoredHistory() 读一遍（localStorage.getItem + JSON.parse），
    // writeStoredHistory() 内部的 historyStore() 又读一遍，最后 loadHistory() 还要读第三遍；
    // 配 recordQueryStart + recordQueryResult 的两次调用，每次查询要整库解析 6 次。
    const key = historyKeyFor(targetSourceId);
    const store = historyStore();
    const list = normalizeHistoryBucket(store.buckets[key], key);
    const runId = entry.runId || '';
    const existingIndex = runId ? list.findIndex(function (item) { return item && item.runId === runId; }) : -1;
    const defaults = Object.assign({
      id: 'q-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7),
      sourceId: targetSourceId, sourceName: sourceName(), dialect: dialect(),
      startedAt: null, endedAt: null, elapsedMs: null, status: 'unknown', rows: null, error: ''
    }, entry, { sql: sql, sourceId: targetSourceId });
    if (existingIndex >= 0) {
      // 一次执行先记 running，结果到达后升级同一条，不新增重复行。
      list[existingIndex] = Object.assign({}, list[existingIndex], defaults, { id: list[existingIndex].id });
      writeStoredHistory(key, list, store);
    } else {
      const next = list.filter(function (item) { return item.sql !== sql; });
      next.unshift(defaults);
      writeStoredHistory(key, next, store);
    }
    // 复用刚写盘的那份列表刷新内存历史，语义与 loadHistory 完全一致（含 running→interrupted 标记）。
    return adoptHistoryList(normalizeHistoryBucket(store.buckets[key], key), key);
  }
  function recordQueryStart(sql, runId, sourceInfo) {
    if (!sql || !String(sql).trim()) return null;
    runId = runId || ('run-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7));
    const targetSourceId = (sourceInfo && sourceInfo.sourceId) || sourceId();
    const targetSourceName = (sourceInfo && sourceInfo.sourceName) || sourceName();
    const targetDialect = (sourceInfo && sourceInfo.dialect) || dialect();
    state.pendingRuns = state.pendingRuns || new Map();
    state.pendingRuns.set(runId, {
      sql: String(sql).trim(),
      runId: runId,
      startedAt: Date.now(),
      sourceId: targetSourceId,
      sourceName: targetSourceName,
      dialect: targetDialect
    });
    state.pendingRun = state.pendingRuns.get(runId);
    addHistory({
      sql: String(sql).trim(),
      runId: runId,
      startedAt: Date.now(),
      status: 'running',
      sourceId: targetSourceId,
      sourceName: targetSourceName,
      dialect: targetDialect
    });
    watchRunState();
    return runId;
  }
  function recordQueryResult(runId, result) {
    result = result || {};
    state.pendingRuns = state.pendingRuns || new Map();
    const pending = state.pendingRuns.get(runId) || (state.pendingRun && state.pendingRun.runId === runId ? state.pendingRun : null);
    if (pending) state.pendingRuns.delete(runId);
    if (state.pendingRun && state.pendingRun.runId === runId) state.pendingRun = null;
    if (state.runObserver) {
      state.runObserver.disconnect();
      state.runObserver.takeRecords();
    }
    const sql = result.sql || (pending && pending.sql);
    if (!sql) return;
    // 没有可靠起点时保持 null，不编造执行时间（DBUI-06）。
    const startedAt = (pending && pending.startedAt) || result.startedAt || null;
    addHistory({
      sql: sql,
      runId: runId,
      sourceId: result.sourceId || (pending && pending.sourceId) || sourceId(),
      sourceName: (pending && pending.sourceName) || sourceName(),
      dialect: (pending && pending.dialect) || dialect(),
      startedAt: startedAt,
      endedAt: startedAt != null ? Date.now() : null,
      elapsedMs: result.elapsedMs != null ? result.elapsedMs : (startedAt != null ? Date.now() - startedAt : null),
      status: result.status || (result.error ? 'error' : 'success'),
      rows: result.rows != null ? result.rows : null,
      error: result.error || ''
    });
  }
  function parseRowsMeta(value) {
    const match = String(value || '').match(/([0-9][0-9,]*)\s*(?:行|rows?)/i);
    return match ? Number(match[1].replace(/,/g, '')) : null;
  }
  function watchRunState() {
    const root = id('db-workspace') || document.body;
    if (state.runObserver && state.runObserverRoot === root) return;
    if (state.runObserver) state.runObserver.disconnect();
    state.runObserver = new MutationObserver(function () {
      if (!state.pendingRun) {
        if (state.runObserver) {
          state.runObserver.disconnect();
          state.runObserver.takeRecords();
        }
        return;
      }
      const status = (id('db-query-status') && id('db-query-status').textContent || '').trim();
      const result = id('db-result-meta');
      const message = id('db-result-message');
      const done = message && !message.hidden ? 'error' : /(?:完成|成功|失败|取消|就绪|\d+\s*行|ms)/.test(status) && !/执行中|查询中|加载|运行/.test(status);
      if (!done) return;
      const pending = state.pendingRun;
      state.pendingRun = null;
      if (state.runObserver) {
        state.runObserver.disconnect();
        state.runObserver.takeRecords();
      }
      addHistory({
        sql: pending.sql,
        runId: pending.runId,
        startedAt: pending.startedAt,
        endedAt: Date.now(),
        elapsedMs: Date.now() - pending.startedAt,
        status: message && !message.hidden ? 'error' : /失败|取消/.test(status) ? 'error' : 'success',
        rows: parseRowsMeta(result && result.textContent),
        error: message && !message.hidden ? message.textContent.trim().slice(0, 500) : ''
      });
    });
    state.runObserverRoot = root;
    state.runObserver.observe(root, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['hidden', 'class'] });
  }
  function beginRunCapture(target) {
    const e = editor();
    if (!e || (target && target.disabled)) return;
    recordQueryStart(e.value);
  }

  function createModal(options) {
    closeModal();
    options = options || {};
    const overlay = document.createElement('div');
    overlay.className = 'db-pro-modal-overlay';
    overlay.setAttribute('role', 'presentation');
    const card = document.createElement('section');
    card.className = 'db-pro-modal';
    card.setAttribute('role', 'dialog');
    card.setAttribute('aria-modal', 'true');
    card.setAttribute('aria-labelledby', 'db-pro-modal-title');
    card.innerHTML = '<header class="db-pro-modal-head"><h2 id="db-pro-modal-title">' + esc(options.title || '数据库工作台') + '</h2><button type="button" class="btn btn-xs db-pro-modal-close" aria-label="关闭">×</button></header><div class="db-pro-modal-body"></div><footer class="db-pro-modal-foot"></footer>';
    const body = card.querySelector('.db-pro-modal-body'), foot = card.querySelector('.db-pro-modal-foot');
    if (typeof Node !== 'undefined' && options.body instanceof Node) body.appendChild(options.body); else body.innerHTML = String(options.body || '');
    (options.actions || [{ text: '关闭', className: 'btn', close: true }]).forEach(function (action) {
      const button = document.createElement('button');
      button.type = 'button'; button.className = action.className || 'btn'; button.textContent = action.text || '关闭';
      if (action.id) button.id = action.id;
      if (action.title) button.title = action.title;
      button.addEventListener('click', function () {
        if (action.onClick) action.onClick(button, { overlay: overlay, card: card, body: body });
        if (action.close !== false && !action.keepOpen) closeModal();
      });
      foot.appendChild(button);
    });
    overlay.appendChild(card);
    document.body.appendChild(overlay);
    state.focusedBeforeModal = document.activeElement;
    state.modal = overlay;
    const close = function () { closeModal(); };
    card.querySelector('.db-pro-modal-close').addEventListener('click', close);
    overlay.addEventListener('pointerdown', function (event) { if (event.target === overlay) close(); });
    overlay.addEventListener('keydown', function (event) {
      if (event.key === 'Escape') { event.preventDefault(); close(); return; }
      if (event.key !== 'Tab') return;
      const focusables = Array.from(card.querySelectorAll('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'));
      if (!focusables.length) return;
      const first = focusables[0], last = focusables[focusables.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    });
    const focusTarget = card.querySelector('[autofocus]') || card.querySelector('input, textarea, select, button');
    if (focusTarget) schedule(function () { focusTarget.focus(); });
    return { overlay: overlay, card: card, body: body, foot: foot, close: close };
  }
  function closeModal() {
    if (!state.modal) return;
    const modal = state.modal;
    state.modal = null;
    if (modal.parentNode) modal.parentNode.removeChild(modal);
    if (state.focusedBeforeModal && state.focusedBeforeModal.focus) {
      try { state.focusedBeforeModal.focus(); } catch (_) {}
    }
  }

  function setEditorValue(value, focus) {
    const e = editor();
    if (!e) return false;
    e.value = String(value == null ? '' : value);
    dispatchEditorInput(e);
    pruneBindings();
    if (focus !== false) e.focus();
    scheduleHighlight();
    return true;
  }
  function downloadText(text, filename, mime) {
    const blob = new Blob([String(text || '')], { type: mime || 'text/plain;charset=utf-8' });
    const link = document.createElement('a'), url = URL.createObjectURL(blob);
    link.href = url; link.download = filename || 'query.sql'; link.rel = 'noopener';
    document.body.appendChild(link); link.click(); link.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  }
  function currentFilename() {
    const text = sqlText().trim(), first = text.replace(/\s+/g, ' ').slice(0, 36).replace(/[^A-Za-z0-9_-]+/g, '_');
    return (first || 'query') + '.sql';
  }
  async function saveSQL(asFile) {
    const text = sqlText();
    if (!text.trim()) { toast('编辑器为空，无法保存 SQL', 'warn'); return; }
    const filename = currentFilename();
    try {
      if (asFile && window.showSaveFilePicker) {
        const handle = await window.showSaveFilePicker({ suggestedName: filename, types: [{ description: 'SQL 文件', accept: { 'text/plain': ['.sql'] } }] });
        const writable = await handle.createWritable(); await writable.write(text); await writable.close();
        persistDraft(handle.name || filename, text);
      } else {
        downloadText(text, filename, 'text/sql;charset=utf-8');
        persistDraft(filename, text);
      }
      toast('SQL 已保存：' + filename, 'ok');
    } catch (error) {
      if (error && error.name === 'AbortError') return;
      toast('保存 SQL 失败：' + (error && error.message ? error.message : error), 'err');
    }
  }
  function persistDraft(name, text) {
    const drafts = safeJSONParse(localStorage.getItem(STORAGE_DRAFTS) || '[]', []);
    drafts.unshift({ name: name, text: String(text || ''), updatedAt: Date.now() });
    try { localStorage.setItem(STORAGE_DRAFTS, JSON.stringify(drafts.slice(0, 10))); } catch (_) {}
  }
  function openSQLInput() {
    if (!state.fileInput) {
      state.fileInput = document.createElement('input');
      state.fileInput.type = 'file'; state.fileInput.accept = '.sql,.pls,.pkb,.pks,.prc,.fnc,.trg,text/plain';
      state.fileInput.className = 'db-pro-visually-hidden';
      document.body.appendChild(state.fileInput);
      state.fileInput.addEventListener('change', function () {
        const file = state.fileInput.files && state.fileInput.files[0];
        if (!file) return;
        const reader = new FileReader();
        reader.onload = function () { setEditorValue(reader.result || '', true); persistDraft(file.name, reader.result || ''); toast('已打开 SQL：' + file.name, 'ok'); };
        reader.onerror = function () { toast('读取 SQL 文件失败', 'err'); };
        reader.readAsText(file);
        state.fileInput.value = '';
      });
    }
    state.fileInput.click();
  }
  function openFindReplace(replaceMode) {
    const body = document.createElement('div');
    body.innerHTML = '<div class="db-pro-find-grid"><label>查找<input id="db-pro-find" class="editor-input" type="search" autocomplete="off" autofocus></label><label>替换为<input id="db-pro-replace" class="editor-input" type="text" autocomplete="off"></label><label class="db-pro-check"><input id="db-pro-find-case" type="checkbox"> 区分大小写</label><label class="db-pro-check"><input id="db-pro-find-regex" type="checkbox"> 正则表达式</label></div><div class="db-pro-find-status" id="db-pro-find-status" role="status"></div>';
    const modal = createModal({
      title: replaceMode ? '查找与替换' : '查找 SQL',
      body: body,
      actions: [
        { id: 'db-pro-find-next', text: '查找下一个', className: 'btn btn-primary', close: false, onClick: function () { findNext(false); } },
        { id: 'db-pro-replace-one', text: '替换', className: 'btn', close: false, onClick: function () { replaceOne(); } },
        { id: 'db-pro-replace-all', text: '全部替换', className: 'btn btn-danger', close: false, onClick: function () { replaceAll(); } },
        { text: '关闭', className: 'btn', close: true }
      ]
    });
    const find = modal.body.querySelector('#db-pro-find'), replacement = modal.body.querySelector('#db-pro-replace'), caseBox = modal.body.querySelector('#db-pro-find-case'), regexBox = modal.body.querySelector('#db-pro-find-regex'), status = modal.body.querySelector('#db-pro-find-status');
    if (!replaceMode) { replacement.parentElement.hidden = true; modal.card.querySelector('#db-pro-replace-one').hidden = true; modal.card.querySelector('#db-pro-replace-all').hidden = true; }
    function matcher() {
      const needle = find.value;
      if (!needle) return null;
      try { return regexBox.checked ? new RegExp(needle, caseBox.checked ? 'g' : 'gi') : new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), caseBox.checked ? 'g' : 'gi'); }
      catch (error) { status.textContent = '正则表达式无效：' + error.message; return null; }
    }
    function countMatches(text, re) { if (!re) return 0; const flags = re.flags.indexOf('g') >= 0 ? re.flags : re.flags + 'g'; re = new RegExp(re.source, flags); return (String(text).match(re) || []).length; }
    function findNext(announce) {
      const e = editor(), re = matcher(); if (!e || !re) return;
      const text = e.value, start = e.selectionEnd || 0, flags = re.flags.replace('g', '') + 'g', all = new RegExp(re.source, flags);
      all.lastIndex = start; let match = all.exec(text); if (!match) { all.lastIndex = 0; match = all.exec(text); }
      if (match) { e.focus(); e.setSelectionRange(match.index, match.index + match[0].length); status.textContent = '已定位到第 ' + (text.slice(0, match.index).split('\n').length) + ' 行'; }
      else status.textContent = '未找到匹配内容';
      if (announce) toast(status.textContent, match ? 'ok' : 'warn');
    }
    function replaceOne() {
      const e = editor(), re = matcher(); if (!e || !re) return;
      if (e.selectionStart !== e.selectionEnd && new RegExp(re.source, re.flags.replace('g', '')).test(e.value.slice(e.selectionStart, e.selectionEnd))) {
        e.setRangeText(replacement.value, e.selectionStart, e.selectionEnd, 'end'); dispatchEditorInput(e);
      }
      findNext(true);
    }
    function replaceAll() {
      const e = editor(), re = matcher(); if (!e || !re) return;
      const count = countMatches(e.value, re); if (!count) { status.textContent = '未找到匹配内容'; return; }
      e.value = e.value.replace(re, replacement.value); dispatchEditorInput(e); status.textContent = '已替换 ' + count + ' 处'; toast(status.textContent, 'ok');
    }
    find.addEventListener('input', function () { if (find.value) findNext(false); });
    find.addEventListener('keydown', function (event) { if (event.key === 'Enter') { event.preventDefault(); findNext(true); } });
    if (!replaceMode) modal.card.querySelector('#db-pro-find-next').focus();
  }

  function openParameterDialog() {
    const scan = scanParameters(executionText());
    if (scan.status === 'pending' || scan.status === 'error') {
      // 扫描未完成时不能把“还没扫完”说成“没有参数”，更不能清空已有绑定。
      toast(scan.status === 'pending' ? 'SQL 较长，绑定参数仍在扫描，请稍后重试' : '绑定参数扫描失败：' + (scan.error || '未知错误'), 'warn');
      return;
    }
    const params = scan.parameters;
    if (!params.length) { toast('当前 SQL 没有发现绑定参数（支持 :name、{{name}} 和 ?）', 'info'); return; }
    const body = document.createElement('div');
    body.innerHTML = '<p class="db-pro-modal-note">绑定值会发送到 <code>kairo:database-bindings</code> 事件。后端接入绑定变量后可直接执行；默认不把值拼回 SQL。</p><div class="db-pro-bind-grid">' + params.map(function (param, index) { return '<label>' + esc(param.name) + '<input class="editor-input db-pro-bind-value" data-name="' + esc(param.name) + '" data-index="' + index + '" type="text" autocomplete="off"></label>'; }).join('') + '</div><label class="db-pro-check"><input id="db-pro-bind-apply" type="checkbox"> 仅本地预览：将值安全转义后替换到编辑器</label>';
    createModal({
      title: '绑定参数', body: body,
      actions: [
        { text: '应用绑定', className: 'btn btn-primary', close: false, onClick: function () {
          const values = {}; body.querySelectorAll('.db-pro-bind-value').forEach(function (input) { values[input.dataset.name] = input.value; });
          state.bindings = values;
          const detail = { sourceId: sourceId(), sql: sqlText(), parameters: params, values: values, dialect: dialect() };
          emit('kairo:database-bindings', detail);
          if (body.querySelector('#db-pro-bind-apply').checked) setEditorValue(applyBindings(sqlText(), values), true);
          toast('绑定参数已准备，可由后端适配器执行', 'ok');
        } },
        { text: '关闭', className: 'btn', close: true }
      ]
    });
  }
  function literal(value) {
    if (value == null || value === '') return 'NULL';
    if (/^(true|false)$/i.test(String(value))) return String(value).toUpperCase();
    if (/^-?(?:\d+\.?\d*|\.\d+)$/.test(String(value).trim())) return String(value).trim();
    return "'" + String(value).replace(/'/g, "''") + "'";
  }
  function applyBindings(source, values) {
    return tokenizeSQL(source).map(function (token) {
      if (token.type === 'param' && token.name && token.name !== '?') return literal(values[token.name] == null ? values[String(token.name).toLowerCase()] : values[token.name]);
      return token.value;
    }).join('');
  }

  function runScript() {
    const text = sqlText(), statements = splitStatements(text).map(function (item) { return String(item.text || '').trim(); }).filter(Boolean);
    if (!statements.length) { toast('脚本为空', 'warn'); return; }
    const body = document.createElement('div');
    body.innerHTML = '<p class="db-pro-modal-note">将按顺序执行 <strong>' + statements.length + '</strong> 条语句。字符串、注释和 Oracle q-quote 内的分号不会拆分。</p><ol class="db-pro-script-list">' + statements.map(function (statement) { return '<li><pre>' + esc(statement.slice(0, 1200)) + (statement.length > 1200 ? '\n…' : '') + '</pre></li>'; }).join('') + '</ol><div class="db-pro-conflict-note">写入脚本需要后端提供页签 session_id；执行结果会通过 <code>kairo:database-script-result</code> 回传。</div>';
    createModal({
      title: '运行脚本', body: body,
      actions: [
        { text: '执行脚本', className: 'btn btn-primary', close: false, onClick: async function (button) {
          button.disabled = true; button.textContent = '执行中…';
          const session = activeSession();
          // P1（审核第 1 项）：脚本执行的数据源必须来自**活动页签**的绑定信息，
          // 并与顶部下拉框交叉校验。页签事务 ID 与数据源必须属于同一条绑定，
          // 否则（例如批量关闭页签后状态没同步）会把 A 的会话拼上 B 的数据源，
          // 让这条 SQL 落到 B 上执行。
          const sessionSourceId = session && session.sourceId ? String(session.sourceId) : '';
          const dropdownSourceId = sourceId();
          if (sessionSourceId && dropdownSourceId && sessionSourceId !== dropdownSourceId) {
            button.disabled = false; button.textContent = '执行脚本';
            toast('当前页签的数据源与顶部选择的数据源不一致，请重新点击该页签后再执行脚本', 'err');
            return;
          }
          const effectiveSourceId = sessionSourceId || dropdownSourceId;
          if (!effectiveSourceId) {
            button.disabled = false; button.textContent = '执行脚本';
            toast('没有可用的数据源，请先选择数据源页签', 'warn');
            return;
          }
          const boundValues = state.bindings || {};
          const boundParameters = typedParameters(boundValues);
          const detail = { sourceId: effectiveSourceId, sourceName: sourceName(), sessionId: session.sessionId || '', statements: statements, script: text, dialect: dialect(), options: { failure_policy: 'rollback', transaction_mode: session.sessionId ? 'session' : 'auto', commit: false }, parameters: boundParameters };
          if (sourceIsProduction()) { const productionConfirm = window.confirm('当前数据源标记为生产环境，确认执行脚本？'); if (!productionConfirm) { button.disabled = false; button.textContent = '执行脚本'; return; } detail.confirm = true; }
          emit('kairo:database-script', detail);
          if (state.adapters.script) {
              try { const result = await state.adapters.script(detail); syncMutationTransaction(detail, result, true); emit('kairo:database-script-result', { ok: true, result: result, request: detail }); toast('脚本执行完成', 'ok'); }
            catch (error) { emit('kairo:database-script-result', { ok: false, error: error, request: detail }); toast('脚本执行失败：' + error.message, 'err'); }
          } else {
            try {
              let result;
              try {
                // The typed endpoint uses DisallowUnknownFields, so keep this
                // payload exact.  Do not mix its fields with the legacy batch
                // contract when falling back after a 404.
                result = await api('POST', '/api/database/script', {
                  source_id: detail.sourceId,
                  session_id: detail.sessionId,
                  sql: detail.script,
                  parameters: detail.parameters,
                  options: detail.options,
                  confirm: detail.confirm
                });
              } catch (error) {
                if (!error || Number(error.status) !== 404) throw error;
                result = await api('POST', '/api/database/batch', {
                  source_id: detail.sourceId,
                  session_id: detail.sessionId,
                  statements: detail.statements
                });
              }
              syncMutationTransaction(detail, result, true); emit('kairo:database-script-result', { ok: true, result: result, request: detail }); toast('脚本执行完成', 'ok');
            } catch (error) {
              syncMutationTransaction(detail, error && error.data, false);
              emit('kairo:database-script-result', { ok: false, error: error, request: detail });
              if (error && Number(error.status) === 400 && !detail.sessionId) toast('脚本执行需要当前页签 session_id，请先选择可执行查询页签', 'warn');
              else if (error && Number(error.status) === 404) toast('脚本执行接口尚未部署（可先使用单条执行）', 'info');
              else toast('脚本执行失败：' + (error.message || error), 'err');
            }
          }
          button.disabled = false; button.textContent = '执行脚本';
        } },
        { text: '关闭', className: 'btn', close: true }
      ]
    });
  }

  function buildDeepLink(options) {
    options = options || {};
    const base = location.href.split('#')[0];
    const params = new URLSearchParams();
    const source = options.sourceId || sourceId(); if (source) params.set('source', source);
    const sql = options.sql == null ? sqlText() : String(options.sql); if (sql) params.set('sql', sql);
    // 注意：深链接**不**写入 autorun 标记——tests/database-review-regression.js 明确守住
    // "链接不得自带自动执行语义"。（options.autoRun 因此只是调用方的意图参数，不落地到 URL。）
    if (params.toString().length > 16000) {
      params.delete('sql');
      toast('SQL 过长，链接仅包含数据源；请通过 SQL 文件传递查询。', 'warn');
    }
    return base + '#/database' + (Array.from(params).length ? '?' + params.toString() : '');
  }
  function openIsolatedWindow(url) {
    // Browsers are allowed to return null for a successful window.open when
    // "noopener" is supplied in the features string. Open synchronously from
    // the user gesture, then sever opener immediately so null still means a
    // genuine popup-blocker decision instead of a false warning.
    const child = window.open(url, '_blank');
    if (child) {
      try { child.opener = null; } catch (_) {}
    }
    return child;
  }
  function openNewWindow() {
    const url = buildDeepLink({ autoRun: false });
    const child = openIsolatedWindow(url);
    if (!child) { toast('浏览器阻止了新窗口，请允许本站弹出窗口', 'warn'); return; }
    toast('已在新窗口打开数据库工作台', 'ok');
  }
  function copyDeepLink() {
    const url = buildDeepLink({ autoRun: true });
    const copy = K.core && K.core.copyToClipboard;
    const done = copy ? copy(url) : Promise.reject(new Error('剪贴板不可用'));
    Promise.resolve(done).then(function () { toast('查询深链接已复制，可放入收藏夹', 'ok'); }).catch(function () { window.prompt('复制下面的数据库工作台链接：', url); });
  }
  function applyDeepLink() {
    const current = String(location.hash || '');
    // 优先用当前 hash（应用内 hashchange 跳转），被路由规范化后再回退到解析期快照。
    const hash = /[?&]sql=/.test(current) ? current : initialDeepHash;
    const marker = '#/database';
    if (hash.indexOf(marker) !== 0 || state.deepLinkApplied === hash) return;
    const query = hash.slice(marker.length).replace(/^\?/, '');
    if (!query) return;
    let params; try { params = new URLSearchParams(query); } catch (_) { return; }
    const sql = params.get('sql');
    const wantedSource = params.get('source');
    if (wantedSource) {
      const select = id('db-source');
      if (select && select.value !== wantedSource && Array.from(select.options).some(function (option) { return option.value === wantedSource; })) {
        select.value = wantedSource;
        dispatchEditorInput(select);
        select.dispatchEvent(new Event('change', { bubbles: true }));
      }
    }
    if (sql == null) { state.deepLinkApplied = hash; return; }
    state.deepLinkApplied = hash;
    // Source changes rebuild the editor asynchronously. Re-check for a short
    // bounded window so a deep link works with both cached and remote sources.
    let tries = 0;
    const apply = function () {
      const e = editor();
      if (!e && tries++ < 20) { setTimeout(apply, 100); return; }
      if (!e) return;
      setEditorValue(sql, false);
      if (params.get('autorun') === '1') toast('链接中的 SQL 已载入，请检查后手动执行。', 'warn');
      // 会话恢复（/api/database/sessions/restore）可能在深链接之后落地并覆盖编辑器，
      // 这里补一次保险：只在编辑器被清空时重新写入链接里的 SQL。
      setTimeout(function () {
        const cur = editor();
        if (!cur) return;
        if (String(cur.value || '').indexOf(String(sql).slice(0, 40)) === 0) return;
        if (!String(cur.value || '').trim()) setEditorValue(sql, false);
      }, 1200);
    };
    apply();
  }

  function environmentLabel(value) {
    const key = String(value || '').toLowerCase();
    return key === 'production' ? '生产' : key === 'staging' ? '测试' : '开发';
  }
  function sourceAdvancedHTML(item) {
    item = item || {};
    const tunnel = item.ssh_tunnel || {};
    return '<fieldset class="db-pro-source-advanced"><legend>环境与安全连接</legend>' +
      '<div class="db-form-grid db-pro-source-grid">' +
      '<label class="db-field"><span>环境</span><select id="dbf-environment" class="editor-input"><option value="development">开发</option><option value="staging">测试</option><option value="production">生产</option></select></label>' +
      '</div>' +
      '<div class="db-pro-tls-grid">' +
      '<label class="db-field"><span>TLS Server Name</span><input id="dbf-tls-server-name" class="editor-input" value="' + esc(item.tls_server_name || '') + '" placeholder="证书中的主机名"></label>' +
      '<label class="db-field"><span>TLS CA 文件</span><input id="dbf-tls-ca-file" class="editor-input" value="' + esc(item.tls_ca_file || '') + '" placeholder="服务器上的 PEM 路径"></label>' +
      '<label class="db-field"><span>TLS 客户端证书</span><input id="dbf-tls-client-cert" class="editor-input" value="' + esc(item.tls_client_cert_file || '') + '" placeholder="mTLS cert PEM 路径"></label>' +
      '<label class="db-field"><span>TLS 客户端私钥</span><input id="dbf-tls-client-key" class="editor-input" value="' + esc(item.tls_client_key_file || '') + '" placeholder="mTLS key PEM 路径"></label>' +
      '</div>' +
      '<div class="db-pro-source-subhead">SSH 隧道（跳板机）</div>' +
      '<div class="db-ssh-toggle-row">' +
      '<label class="db-field db-pro-inline-check db-field-span"><span><input id="dbf-ssh-enabled" type="checkbox"> 启用 SSH 隧道</span><small>数据库密码与 SSH 凭据分开保存</small></label>' +
      '</div>' +
      '<div class="db-form-grid db-pro-source-grid db-ssh-fields" id="dbf-ssh-fields">' +
      '<label class="db-field"><span>SSH 主机</span><input id="dbf-ssh-host" class="editor-input" value="' + esc(tunnel.host || '') + '"></label>' +
      '<label class="db-field"><span>SSH 端口</span><input id="dbf-ssh-port" class="editor-input" type="number" min="1" max="65535" value="' + esc(tunnel.port || 22) + '"></label>' +
      '<label class="db-field"><span>SSH 用户</span><input id="dbf-ssh-user" class="editor-input" value="' + esc(tunnel.username || '') + '"></label>' +
      '<label class="db-field"><span>' + (item.has_ssh_password ? 'SSH 密码（留空不改）' : 'SSH 密码') + '</span><input id="dbf-ssh-password" class="editor-input" type="password" autocomplete="new-password" value="" placeholder="' + (item.has_ssh_password ? '已保存，留空不改' : '必填') + '"></label>' +
      '<label class="db-field"><span>远端数据库主机</span><input id="dbf-ssh-remote-host" class="editor-input" value="' + esc(tunnel.remote_host || '') + '"></label>' +
      '<label class="db-field"><span>远端数据库端口</span><input id="dbf-ssh-remote-port" class="editor-input" type="number" min="1" max="65535" value="' + esc(tunnel.remote_port || '') + '"></label>' +
      '<label class="db-field"><span>Host Key SHA256</span><input id="dbf-ssh-host-key" class="editor-input" value="' + esc(tunnel.host_key_sha256 || '') + '" placeholder="推荐填写，禁止未知主机"></label>' +
      '<label class="db-field"><span>SSH Profile</span><select id="dbf-ssh-profile" class="editor-input"><option value="auto">auto</option><option value="modern">modern</option><option value="compat">compat</option><option value="no-ecdh">no-ecdh</option><option value="legacy">legacy</option></select></label>' +
      '<label class="db-field db-pro-inline-check"><span><input id="dbf-ssh-insecure" type="checkbox"> 允许未知 Host Key</span><small>仅排障时临时开启</small></label>' +
      '</div></fieldset>';
  }
  function syncSourceAdvanced(item) {
    const env = id('dbf-environment');
    if (!env) return;
    item = item || {};
    env.value = item.environment || 'production';
    const readOnly = id('dbf-read-only'); if (readOnly) readOnly.checked = item.id ? !!(item.read_only || item.readOnly) : true;
    const ddl = id('dbf-allow-ddl'); if (ddl) ddl.checked = !!(item.allow_ddl || item.allowDDL);
    if (readOnly && ddl) {
      ddl.onchange = function () { if (this.checked && readOnly) readOnly.checked = false; };
      readOnly.onchange = function () { if (this.checked && ddl) ddl.checked = false; };
    }
    const tunnel = item.ssh_tunnel || {};
    const set = function (name, value) { const input = id(name); if (input && value != null) input.value = value; };
    const check = function (name, value) { const input = id(name); if (input) input.checked = !!value; };
    check('dbf-ssh-enabled', tunnel.enabled); check('dbf-ssh-insecure', tunnel.allow_insecure_host_key);
    set('dbf-ssh-profile', tunnel.ssh_profile || 'auto');
  }
  function ensureSourceManager(root) {
    const manager = (root && root.querySelector && root.querySelector('#db-manager')) || id('db-manager');
    if (!manager) return;
    const panel = manager.querySelector('.db-manager'), marker = manager.querySelector('[data-db-pro-source-advanced]');
    if (!panel || marker) return;
    const target = panel.querySelector('.db-form-actions') || panel.lastElementChild;
    if (!target) return;
    const select = id('db-edit-existing'), item = select && state.sourceCatalog[select.value] || {};
    const holder = document.createElement('div'); holder.dataset.dbProSourceAdvanced = '1'; holder.innerHTML = sourceAdvancedHTML(item);
    target.parentNode.insertBefore(holder, target);
    syncSourceAdvanced(item);
    const enabled = id('dbf-ssh-enabled'), sshFields = ['dbf-ssh-host', 'dbf-ssh-port', 'dbf-ssh-user', 'dbf-ssh-password', 'dbf-ssh-remote-host', 'dbf-ssh-remote-port', 'dbf-ssh-host-key', 'dbf-ssh-profile', 'dbf-ssh-insecure'];
    const toggle = function () {
      const on = !!(enabled && enabled.checked);
      sshFields.forEach(function (name) { const input = id(name); if (input) input.disabled = !on; });
      const wrap = id('dbf-ssh-fields');
      if (wrap) wrap.classList.toggle('is-disabled', !on);
    };
    if (enabled) enabled.addEventListener('change', toggle); toggle();
  }
  function updateSourceStatus() {
    const select = id('db-source'), option = select && select.selectedOptions && select.selectedOptions[0], item = option && state.sourceCatalog[option.value];
    if (!select || !item) return;
    const env = String(item.environment || 'development').toLowerCase();
    const envText = environmentLabel(env), readonly = item.read_only || item.readOnly ? ' · 只读' : item.allow_ddl ? ' · 可写/DDL' : ' · 可写';
    if (option) {
      const nextOpt = (item.name || option.textContent).replace(/\s+·\s+(开发|测试|生产)(?:\s+·\s+(?:只读|可写|可写\/DDL))?$/, '') + ' · ' + envText + readonly;
      if (option.textContent !== nextOpt) option.textContent = nextOpt;
      if (option.dataset.environment !== env) option.dataset.environment = env;
      const ro = item.read_only || item.readOnly ? '1' : '0';
      if (option.dataset.readOnly !== ro) option.dataset.readOnly = ro;
      const ddl = item.allow_ddl ? '1' : '0';
      if (option.dataset.allowDdl !== ddl) option.dataset.allowDdl = ddl;
    }
    const badge = id('db-source-badge');
    if (badge) {
      const kind = String(item.kind || '').toLowerCase();
      const nextBadge = (kind === 'oracle' ? 'Oracle' : kind === 'mysql' ? 'MySQL' : kind === 'redis' ? 'Redis' : kind) + ' · ' + envText + readonly;
      if (badge.textContent !== nextBadge) badge.textContent = nextBadge;
      if (badge.dataset.environment !== env) badge.dataset.environment = env;
      const ro = item.read_only || item.readOnly ? '1' : '0';
      if (badge.dataset.readOnly !== ro) badge.dataset.readOnly = ro;
    }
  }
  function enhanceSourceSelect() {
    const select = id('db-source');
    if (!select) return;
    Array.from(select.options || []).forEach(function (option) {
      const item = state.sourceCatalog[option.value];
      if (!item) return;
      option.dataset.environment = String(item.environment || 'development').toLowerCase();
      option.dataset.readOnly = item.read_only || item.readOnly ? '1' : '0';
      option.dataset.allowDdl = item.allow_ddl ? '1' : '0';
    });
    if (select.dataset.dbProSourceBound !== '1') {
      select.dataset.dbProSourceBound = '1';
      select.addEventListener('change', updateSourceStatus);
      // Parameter values belong to a source/session. Never carry a bind from
      // one environment into another after the base page switches sources.
      select.addEventListener('change', function () { state.bindings = Object.create(null); state.fieldCache = Object.create(null); });
    }
    updateSourceStatus();
  }
  function loadSourceCatalog() {
    if (state.sourceCatalogLoaded) { enhanceSourceSelect(); ensureSourceManager(document); return; }
    state.sourceCatalogLoaded = true;
    api('GET', '/api/database/sources').then(function (data) {
      (data.sources || data.items || []).forEach(function (item) { if (item && item.id) state.sourceCatalog[item.id] = item; });
      enhanceSourceSelect(); ensureSourceManager(document);
    }).catch(function () {
      // The base page still remains usable when an old server omits source
      // metadata; the advanced form simply stays hidden until it is available.
      state.sourceCatalogLoaded = false;
    });
  }
  function refreshSourceCatalog() {
    // database.js owns the source CRUD form.  Clear the feature-side copy
    // after a save/delete so environment/read-only badges and the advanced
    // editor never keep rendering a removed or stale source.
    state.sourceCatalogLoaded = false;
    state.sourceCatalog = Object.create(null);
    loadSourceCatalog();
  }

  // 工具按钮统一构造：传 iconName 时只显示彩色图标（中文进 title 悬浮提示），
  // 未传图标名时退回中文按钮（例如「更多」面板内的按钮按要求保留中文）。
  function createQuickButton(idValue, label, title, handler, iconName) {
    const button = document.createElement('button');
    button.type = 'button';
    button.id = idValue;
    const iconFn = K.database && K.database.actionIcon;
    const useIcon = !!iconName && typeof iconFn === 'function';
    button.className = 'btn btn-xs db-pro-button' + (useIcon ? ' db-ic-btn' : '');
    if (useIcon) button.innerHTML = iconFn(iconName) + '<span>' + label + '</span>';
    else button.textContent = label;
    button.title = title || label;
    button.setAttribute('aria-label', title || label);
    button.addEventListener('click', handler);
    return button;
  }
  function ensureToolbar(root) {
    const bar = root.querySelector('.db-editor-bar');
    if (!bar || bar.dataset.dbFeaturesBound === '1') return;
    bar.dataset.dbFeaturesBound = '1';
    // 按用户要求重排：查找/打开收进“更多”面板，保存放在第一行回滚按钮右侧。
    // 注意“更多”面板在 database.js 的模板里就有，这里只往里追加按钮（保持既有 id 与处理器不变）。
    const panel = root.querySelector('.db-toolbar-more-panel');
    const trans = root.querySelector('.db-bar-trans-group') || bar;
    // 第一行只保留图标：保存（其余脚本文件操作都在「更多」里，保持中文按钮）。
    const saveButton = createQuickButton('db-pro-save-button', '保存', '保存 SQL（Ctrl+S）', function () { saveSQL(false); }, 'save');
    saveButton.className = 'btn db-ic-btn db-pro-button';
    trans.appendChild(saveButton);
    if (panel) {
      const quickSection = document.createElement('div');
      quickSection.className = 'db-more-section db-pro-quick-section';
      quickSection.innerHTML = '<div class="db-more-title">脚本文件</div><div class="db-more-row"></div>';
      const quickRow = quickSection.querySelector('.db-more-row');
      quickRow.appendChild(createQuickButton('db-pro-find-button', '查找', '查找 SQL（Ctrl+F）', function () { openFindReplace(false); }));
      quickRow.appendChild(createQuickButton('db-pro-open-button', '打开', '打开 SQL 文件（Ctrl+O）', openSQLInput));
      panel.appendChild(quickSection);
    }
    if (panel) {
      const section = document.createElement('div'); section.className = 'db-more-section db-pro-tools-section';
      section.innerHTML = '<div class="db-more-title">脚本、参数与工作台</div><div class="db-pro-action-grid"></div>';
      const actions = section.querySelector('.db-pro-action-grid');
      actions.appendChild(createQuickButton('db-pro-script', '运行脚本', '拆分并执行整个 SQL 脚本', runScript));
      actions.appendChild(createQuickButton('db-pro-replace', '查找替换', '查找并替换 SQL（Ctrl+H）', function () { openFindReplace(true); }));
      actions.appendChild(createQuickButton('db-pro-params', '绑定参数', '填写 :name / {{name}} 绑定参数', openParameterDialog));
      actions.appendChild(createQuickButton('db-pro-history', '查询历史', '打开可搜索的结构化查询历史', openHistory));
      actions.appendChild(createQuickButton('db-pro-save-as', '另存为', '选择文件名保存 SQL', function () { saveSQL(true); }));
      actions.appendChild(createQuickButton('db-pro-open-new', '新窗口', '在新窗口打开当前数据库查询', openNewWindow));
      actions.appendChild(createQuickButton('db-pro-copy-link', '复制链接', '复制可放入收藏夹的查询深链接', copyDeepLink));
      actions.appendChild(createQuickButton('db-pro-import', '导入数据', '打开 CSV/TSV 数据导入向导', openImportWizard));
      panel.appendChild(section);
    }
    ensureEditor(root);
    ensureGridToolbar(root);
    // 注入完成后把 title 同步为"按钮正上方"的悬浮中文提示
    if (K.database && typeof K.database.syncIconTips === 'function') K.database.syncIconTips(root);
    watchRunState();
    applyDeepLink();
  }

  function ensureEditor(root) {
    const e = editor();
    if (!e) return;
    if (state.editorInput === e) return;
    if (state.disposeEditor) state.disposeEditor();
    state.bindings = Object.create(null);
    state.editor = e; state.editorInput = e;
    const handlers = {
      input: function () { pruneBindings(); },
      scroll: function () { if (state.ac) positionCompletion(e); },
      keyup: function () { scheduleHighlight(); maybeContextCompletion(false); },
      click: function () { scheduleHighlight(); maybeContextCompletion(false); }
    };
    Object.keys(handlers).forEach(function (name) { e.addEventListener(name, handlers[name], { passive: true }); });
    state.disposeEditor = function () {
      Object.keys(handlers).forEach(function (name) { e.removeEventListener(name, handlers[name]); });
      state.editor = null; state.editorInput = null; state.disposeEditor = null;
    };
    scheduleHighlight();
  }
  function scheduleHighlight() {
    if (state.raf) return;
    state.raf = schedule(function () {
      state.raf = 0;
      // database.js owns the single syntax overlay and its input/scroll
      // listeners. The feature layer only asks that owner to synchronize;
      // it never writes the overlay DOM itself.
      const e = editor();
      if (e && typeof e._syncHighlight === 'function') e._syncHighlight();
    });
  }

  function knownObjects() {
    const sets = identifierSets(), result = [];
    sets.objects.forEach(function (name) { result.push({ label: name, kind: 'object', insert: name }); });
    sets.fields.forEach(function (name) { result.push({ label: name, kind: 'field', insert: name }); });
    return result;
  }
  function findTableForQualifier(text, qualifier) {
    const regex = /\b(?:from|join|update|into|delete\s+from)\s+((?:[A-Za-z_$#][\w$#]*\.)?[A-Za-z_$#][\w$#]*)(?:\s+(?:as\s+)?([A-Za-z_$#][\w$#]*))?/ig;
    let match;
    while ((match = regex.exec(text))) {
      const table = match[1], alias = match[2];
      if (!qualifier || alias && alias.toLowerCase() === qualifier.toLowerCase() || table.split('.').pop().toLowerCase() === qualifier.toLowerCase()) return table;
    }
    return qualifier || '';
  }
  async function loadFields(table) {
    if (!table) return [];
    const source = sourceId(), schemaInput = id('db-schema');
    let schema = schemaInput && schemaInput.value ? schemaInput.value.trim() : '';
    if (schema === '加载中…' || schema === '加载失败') schema = '';
    const key = source + '|' + schema + '|' + table;
    if (state.fieldCache[key]) return state.fieldCache[key];
    const parts = table.replace(/["`]/g, '').split('.');
    const object = parts.pop(), effectiveSchema = parts.join('.') || schema;
    if (!source) return [];
    try {
      const result = await api('GET', '/api/database/metadata/fields?source_id=' + encodeURIComponent(source) + '&schema=' + encodeURIComponent(effectiveSchema) + '&object=' + encodeURIComponent(object));
      const fields = (result.fields || result.items || []).map(function (field) { return typeof field === 'string' ? field : field.name; }).filter(Boolean);
      state.fieldCache[key] = fields; return fields;
    } catch (_) { return []; }
  }
  function showCompletion(items, start, end) {
    const e = editor(); if (!e || !items.length) { hideCompletion(); return; }
    let box = id('db-pro-ac');
    if (!box) { box = document.createElement('div'); box.id = 'db-pro-ac'; box.className = 'db-pro-ac'; box.setAttribute('role', 'listbox'); box.setAttribute('aria-label', '数据库字段联想'); (e.parentElement || document.body).appendChild(box); }
    state.ac = box; state.acItems = items.slice(0, 20); state.acIndex = 0; state.acStart = start; state.acEnd = end; box.hidden = false;
    box.innerHTML = state.acItems.map(function (item, index) { return '<button type="button" class="db-pro-ac-item' + (index === 0 ? ' active' : '') + '" data-index="' + index + '" role="option" aria-selected="' + (index === 0 ? 'true' : 'false') + '"><span>' + esc(item.label) + '</span><small>' + esc(item.kind === 'field' ? '字段' : item.kind === 'object' ? '对象' : item.kind === 'function' ? '函数' : '关键字') + '</small></button>'; }).join('');
    box.querySelectorAll('[data-index]').forEach(function (button) { button.addEventListener('mousedown', function (event) { event.preventDefault(); state.acIndex = Number(button.dataset.index); acceptCompletion(); }); });
    positionCompletion(e);
  }
  function positionCompletion(e) {
    const box = state.ac; if (!box || box.hidden) return;
    const style = window.getComputedStyle(e), lineH = parseFloat(style.lineHeight) || 21, padT = parseFloat(style.paddingTop) || 13, padL = parseFloat(style.paddingLeft) || 15;
    const before = e.value.slice(0, state.acStart).split('\n'), row = before.length - 1, colText = before[before.length - 1];
    const canvas = positionCompletion.canvas || (positionCompletion.canvas = document.createElement('canvas')), ctx = canvas.getContext('2d'); ctx.font = style.font;
    box.style.left = Math.max(4, Math.min(e.clientWidth - 260, padL + ctx.measureText(colText).width - e.scrollLeft)) + 'px';
    box.style.top = Math.max(4, padT + (row + 1) * lineH - e.scrollTop) + 'px';
  }
  function hideCompletion() { if (state.ac) { state.ac.hidden = true; state.ac.innerHTML = ''; } state.ac = null; state.acItems = []; }
  function acceptCompletion() {
    const e = editor(), item = state.acItems[state.acIndex]; if (!e || !item) return hideCompletion();
    e.setRangeText(item.insert || item.label, state.acStart, state.acEnd, 'end'); dispatchEditorInput(e); hideCompletion(); scheduleHighlight();
  }
  async function maybeContextCompletion(force) {
    // database.js owns completion whenever its editor is mounted.
    if (id('db-sql-ac')) { hideCompletion(); return; }
    const e = editor(); if (!e) return;
    const prefix = completionPrefix(e.value, e.selectionStart); if (!prefix) { hideCompletion(); return; }
    if (!force && !prefix.qualifier) return;
    const request = ++state.acRequest;
    let names;
    if (prefix.qualifier) {
      names = await loadFields(findTableForQualifier(e.value, prefix.qualifier));
      if (!names.length) names = Array.from(identifierSets().fields);
    } else names = knownObjects().map(function (item) { return item.label; });
    if (request !== state.acRequest || editor() !== e) return;
    const needle = prefix.prefix.toLowerCase();
    const items = names.filter(function (name) { return String(name).toLowerCase().indexOf(needle) === 0; }).map(function (name) { return { label: name, insert: name, kind: prefix.qualifier ? 'field' : 'object' }; });
    if (items.length) showCompletion(items, prefix.start, prefix.end); else hideCompletion();
  }

  function gridContext() {
    return K.database && K.database.getGridContext ? K.database.getGridContext() : null;
  }
  function getGridInfo() {
    const context = gridContext();
    if (!context) return null;
    return { columns: context.columns, selected: context.values.length > 0, rowIndex: context.rowIndex, values: context.values, context: context };
  }
  function inferType(column, value) {
    const name = String(column && column.name || '').toLowerCase(), type = String(column && column.type || '').toLowerCase();
    if (TYPE_DATE.test(type) || /(^|_)(date|time|at|on)$/.test(name)) return 'datetime-local';
    if (TYPE_BOOL.test(type) || /(^|_)(is|has|enabled|active)$/.test(name)) return 'checkbox';
    if (TYPE_NUMBER.test(type) || /(^|_)(id|count|num|amount|price|total|sort|order)$/.test(name)) return 'number';
    return 'text';
  }
  function gridRevision(info) {
    const value = JSON.stringify({ sourceId: sourceId(), rowIndex: info.rowIndex, values: info.values, columns: info.columns.map(function (c) { return c.name; }) });
    let hash = 2166136261;
    for (let i = 0; i < value.length; i++) { hash ^= value.charCodeAt(i); hash = Math.imul(hash, 16777619); }
    return ('00000000' + (hash >>> 0).toString(16)).slice(-8);
  }
  // Grid writes are allowed only for an unambiguous single-table projection.
  // Resolve from executed SQL, never from subsequently edited editor text.
  function resolveGridTarget(text, defaultSchema, sourceKind) {
    if (!defaultSchema || defaultSchema === '加载中…' || defaultSchema === '加载失败' || defaultSchema.indexOf('加载中') !== -1) defaultSchema = '';
    const tokens = tokenizeSQL(text).filter(function (t) { return t.type !== 'space' && t.type !== 'comment'; });
    const word = function (i) { return tokens[i] ? tokens[i].value.toUpperCase() : ''; };
    const ident = function (t) { return t && (t.type === 'ident' || t.type === 'quoted-ident'); };
    const name = function (t) { return t.type === 'quoted-ident' ? t.value.slice(1, -1).replace(/""/g, '"').replace(/``/g, '`') : sourceKind === 'oracle' ? t.value.toUpperCase() : t.value; };
    if (word(0) !== 'SELECT') return null;
    if (tokens.some(function (t, i) { return t.type === 'keyword' && (/^(JOIN|UNION|INTERSECT|MINUS|EXCEPT|GROUP|DISTINCT|WITH)$/.test(t.value.toUpperCase()) || i > 0 && t.value.toUpperCase() === 'SELECT'); })) return null;
    const from = tokens.findIndex(function (t) { return t.type === 'keyword' && t.value.toUpperCase() === 'FROM'; });
    if (from < 2) return null;
    let i = 1;
    while (i < from) {
      if (word(i) === '*') i++;
      else {
        if (!ident(tokens[i++])) return null;
        if (word(i) === '.') { i++; if (word(i) !== '*' && !ident(tokens[i])) return null; i++; }
      }
      if (i === from) break;
      if (word(i++) !== ',' || i === from) return null;
    }
    i = from + 1;
    if (!ident(tokens[i])) return null;
    let schema = defaultSchema || '', table = name(tokens[i++]);
    if (word(i) === '.') { i++; if (!ident(tokens[i])) return null; schema = table; table = name(tokens[i++]); }
    if (!schema || schema === '加载中…' || schema === '加载失败' || schema.indexOf('加载中') !== -1) schema = '';
    if (word(i) === 'AS') i++;
    if (ident(tokens[i])) i++;
    if (i < tokens.length && !/^(WHERE|ORDER|FETCH|LIMIT|OFFSET|FOR|;)$/.test(word(i))) return null;
    return { schema: schema, table: table };
  }
  function gridTarget() {
    const context = gridContext();
    return context ? { schema: context.schema, table: context.table } : { schema: '', table: '' };
  }
  function gridContextKey(context) {
    return context ? JSON.stringify([context.sourceId, context.sessionId, context.schema, context.table, context.resultId || context.sql]) : '';
  }
  function pendingGridMutations(context) {
    const key = gridContextKey(context || gridContext());
    return key ? state.gridMutations.filter(function (item) { return item.contextKey === key; }) : [];
  }
  function clearGridMutations(items) {
    const removed = new Set(items || pendingGridMutations());
    state.gridMutations = state.gridMutations.filter(function (item) { return !removed.has(item); });
    updateGridPendingBadge();
  }
  function objectValues(columns, values) {
    const result = {};
    (columns || []).forEach(function (column, index) {
      const name = typeof column === 'string' ? column : column && column.name;
      if (name) result[name] = values && values[index] !== undefined ? values[index] : null;
    });
    return result;
  }
  async function gridPrimaryKeys(target) {
    const source = target && target.sourceId || sourceId();
    if (!target || !target.table || !source) return [];
    let schema = target.schema || '';
    if (schema === '加载中…' || schema === '加载失败') schema = '';
    const key = source + '|' + schema + '|' + target.table + '|meta';
    if (state.fieldCache[key]) return state.fieldCache[key];
    try {
      const data = await api('GET', '/api/database/metadata/fields?source_id=' + encodeURIComponent(source) + '&schema=' + encodeURIComponent(schema) + '&object=' + encodeURIComponent(target.table));
      const fields = (data.fields || data.items || []).map(function (field) { return typeof field === 'string' ? { name: field } : field; });
      const keys = fields.filter(function (field) { return field.primary_key || field.primaryKey; }).map(function (field) { return field.name; }).filter(Boolean);
      state.fieldCache[key] = keys;
      return keys;
    } catch (_) { return []; }
  }
  // 把"待提交队列"（新增行 / 删除行）写进当前页签事务。
  //
  // 返回值：true=已成功写入（队列已清空），false=未写入（内部已 toast 原因）。
  // opts.silent：由「提交」按钮合并调用时不再单独提示/确认，避免两次弹窗与重复 toast。
  async function applyGridMutations(commit, opts) {
    opts = opts || {};
    const context = gridContext(), queued = pendingGridMutations(context);
    if (!queued.length) { if (!opts.silent) toast('没有待应用的网格变更', 'info'); return false; }
    if (!context || !context.editable || context.applying) { toast('请先开启网格编辑并等待当前操作完成', 'warn'); return false; }
    if (sourceIsReadOnly()) { toast('当前数据源为只读，不能应用网格变更', 'warn'); return false; }
    const target = context, session = context;
    if (!target.table) { toast('无法从当前 SQL 推断目标表，请使用 FROM/UPDATE/INSERT INTO 语句', 'warn'); return false; }
    if (!session.sessionId) { toast('网格变更需要当前页签 session_id，请先选择可执行查询页签', 'warn'); return false; }
    if (state.gridApplying) return false;
    state.gridApplying = true;
    try {
    const primaryKey = await gridPrimaryKeys(target);
    const mutations = queued.map(function (item) {
      const values = objectValues(item.columns, item.values);
      const original = objectValues(item.columns, item.expectedValues);
      (item.columns || []).forEach(function (column) {
        const name = typeof column === 'string' ? column : column.name;
        if (original[name] != null && (typeof original[name] === 'object' || /CLOB|BLOB|LONG|XML|JSON|GEOMETRY/i.test(column.database_type || column.database || column.type || ''))) delete original[name];
      });
      return {
        action: item.kind,
        values: item.kind === 'insert' ? values : {},
        original: item.kind === 'delete' ? original : {},
        key: item.kind === 'delete' ? original : {},
        primary_key: primaryKey,
        rowid: item.rowid || '',
        use_rowid: !!item.useRowID,
        confirm: false
      };
    });
    if (mutations.some(function (item) { return item.action === 'delete' && !item.primary_key.length && !item.use_rowid && !(context.editPlan && context.editPlan.identity_policy === 'unique'); })) {
      toast('目标表没有检测到有效主键、唯一键或 Oracle ROWID，无法删除', 'warn'); return false;
    }
    const production = context.production;
    const confirm = production && !opts.silent ? window.confirm('当前数据源标记为生产环境，确认应用网格变更？') : !!opts.confirm;
    if (production && !confirm && !opts.silent) return false;
    let targetSchema = target.schema || '';
    if (targetSchema === '加载中…' || targetSchema === '加载失败') targetSchema = '';
    const detail = { sourceId: context.sourceId, schema: targetSchema, table: target.table, sessionId: session.sessionId, mutations: mutations, commit: !!commit, confirm: !!confirm, dialect: dialect() };
    emit('kairo:database-grid-commit', detail);
    try {
      const adapter = state.adapters.grid;
      const result = adapter ? await adapter(detail) : await apiFallback('POST', ['/api/database/grid', '/api/database/grid/mutate'], {
        result_id: context.resultId || '',
        source_id: detail.sourceId, schema: detail.schema, table: detail.table, session_id: detail.sessionId,
        mutations: detail.mutations, commit: detail.commit, confirm: detail.confirm
      });
      clearGridMutations(queued);
      syncMutationTransaction(detail, result, true);
      emit('kairo:database-grid-applied', { request: detail, result: result });
      if (!opts.silent) toast('网格变更已应用' + (commit ? '并提交' : '，仍在当前事务中'), 'ok');
      return true;
    } catch (error) {
      syncMutationTransaction(detail, error && error.data, false);
      emit('kairo:database-grid-applied', { request: detail, result: null, error: error });
      if (error && Number(error.status) === 404) toast('网格写入接口尚未部署，请先使用 SQL 修改', 'info');
      else toast('网格变更失败：' + (error.message || error), 'err');
      return false;
    }
    } finally { state.gridApplying = false; }
  }
  function queueGridMutation(mutation, context) {
    mutation.contextKey = gridContextKey(context || gridContext());
    if (!mutation.contextKey) return;
    state.gridMutations.push(mutation);
    const detail = { sourceId: sourceId(), sourceName: sourceName(), mutations: state.gridMutations.slice(), dialect: dialect() };
    emit('kairo:database-grid-change', detail);
    updateGridPendingBadge();
  }
  function updateGridPendingBadge() {
    const badge = id('db-pro-grid-pending'); if (!badge) return;
    const count = pendingGridMutations().length;
    badge.textContent = count ? '待应用 ' + count : '';
    badge.hidden = !count;
  }
  function openGridRowModal(mode) {
    const info = getGridInfo(); if (!info) { toast('请先执行返回结果的查询', 'warn'); return; }
    if (!info.context.editable) { toast('请先开启网格编辑', 'warn'); return; }
    if (mode === 'delete' && !info.selected) { toast('请先选中要删除的行', 'warn'); return; }
    const isInsert = mode === 'insert';
    const values = isInsert ? info.columns.map(function () { return ''; }) : info.values;
    const body = document.createElement('div');
    body.innerHTML = '<p class="db-pro-modal-note">' + (isInsert ? '新增行会作为待提交变更保存。' : '删除使用主键/唯一键或整行旧值匹配；提交时后端必须校验 rows_affected=1。') + '</p><div class="db-pro-row-grid">' + info.columns.map(function (column, index) { const type = inferType(column, values[index]); const input = type === 'checkbox' ? '<input type="checkbox" class="db-pro-row-value" data-col="' + index + '">' : '<input class="editor-input db-pro-row-value" data-col="' + index + '" type="' + type + '" value="' + esc(values[index] || '') + '">'; return '<label><span>' + esc(column.name) + '</span>' + input + '</label>'; }).join('') + '</div><div class="db-pro-conflict-note">并发保护：revision ' + esc(gridRevision(info)) + '；后端接入时请同时发送 expected_values。</div>';
    createModal({
      title: isInsert ? '新增数据行' : '删除数据行', body: body,
      actions: [
        { text: isInsert ? '加入待提交' : '加入删除队列', className: isInsert ? 'btn btn-primary' : 'btn btn-danger', close: false, onClick: function () {
          const next = Array.from(body.querySelectorAll('.db-pro-row-value')).map(function (input) { return input.type === 'checkbox' ? input.checked : input.value; });
          const mutationItem = { kind: isInsert ? 'insert' : 'delete', rowIndex: info.rowIndex, values: next, expectedValues: isInsert ? null : info.values, columns: info.columns.map(function (c) { return c.name; }), revision: gridRevision(info) };
          if (!isInsert && info.context && info.context.editPlan && info.context.editPlan.identity_policy === 'oracle_rowid') {
            const plan = info.context.editPlan;
            let rid = '';
            if (plan.hidden_rowid_index != null && plan.hidden_rowid_index >= 0 && info.values[plan.hidden_rowid_index] != null) {
              rid = String(info.values[plan.hidden_rowid_index]);
            } else if (info.values.length > info.columns.length && info.values[info.columns.length] != null) {
              rid = String(info.values[info.columns.length]);
            }
            if (rid) {
              mutationItem.rowid = rid;
              mutationItem.use_rowid = true;
              mutationItem.useRowID = true;
            }
          }
          queueGridMutation(mutationItem, info.context);
          closeModal(); toast(isInsert ? '新增行已加入待提交队列' : '删除行已加入待提交队列', 'ok');
        } },
        { text: '关闭', className: 'btn', close: true }
      ]
    });
  }
  function ensureGridToolbar(root) {
    const actions = root.querySelector('.db-result-actions'); if (!actions || actions.dataset.dbGridFeatures === '1') return;
    actions.dataset.dbGridFeatures = '1';
    const group = document.createElement('span'); group.className = 'db-pro-grid-actions';
    // 结果网格上方的按钮同样只用图标（中文进悬浮提示），避免占掉列宽。
    // 「应用变更」已与编辑器工具条的「提交」合并（提交 = 先写入待提交队列，再提交事务），
    // 这里只保留 新增行 / 删除行 / 清空变更，避免两个按钮做同一件事。
    group.appendChild(createQuickButton('db-pro-grid-add', '新增行', '新增行', function () { openGridRowModal('insert'); }, 'add'));
    group.appendChild(createQuickButton('db-pro-grid-delete', '删除行', '删除行', function () { openGridRowModal('delete'); }, 'trash'));
    group.appendChild(createQuickButton('db-pro-grid-clear', '清空变更', '清空变更', function () { if (!pendingGridMutations().length || window.confirm('清空尚未提交的网格变更？')) clearGridMutations(); }, 'eraser'));
    const badge = document.createElement('span'); badge.id = 'db-pro-grid-pending'; badge.className = 'db-pro-grid-pending'; badge.hidden = true; badge.setAttribute('role', 'status'); group.appendChild(badge);
    actions.appendChild(group);
    updateGridPendingBadge();
  }

  function parseCSV(text, delimiter) {
    const input = String(text == null ? '' : text).replace(/^\uFEFF/, ''), sep = delimiter || (input.indexOf('\t') >= 0 && input.indexOf(',') < 0 ? '\t' : ',');
    const rows = [], row = []; let field = '', quoted = false;
    for (let i = 0; i < input.length; i++) {
      const c = input[i], n = input[i + 1];
      if (quoted) {
        if (c === '"' && n === '"') { field += '"'; i++; }
        else if (c === '"') quoted = false;
        else field += c;
      } else if (c === '"' && field === '') quoted = true;
      else if (c === sep) { row.push(field); field = ''; }
      else if (c === '\n' || c === '\r') {
        if (c === '\r' && n === '\n') i++;
        row.push(field); field = '';
        if (row.some(function (value) { return value !== ''; })) rows.push(row.slice());
        row.length = 0;
      } else field += c;
    }
    row.push(field); if (row.some(function (value) { return value !== ''; })) rows.push(row);
    return rows;
  }
  function openImportWizard() {
    const body = document.createElement('div');
    const guessed = gridTarget();
    body.innerHTML = '<div class="db-pro-import-target"><label>Schema<input id="db-pro-import-schema" class="editor-input" value="' + esc(guessed.schema || '') + '"></label><label>目标表<input id="db-pro-import-table" class="editor-input" value="' + esc(guessed.table || '') + '" placeholder="例如 ORDERS" required></label></div><label class="db-pro-file-picker">选择 CSV/XLSX 文件<input id="db-pro-import-file" type="file" accept=".csv,.tsv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"></label><div id="db-pro-import-preview" class="db-pro-import-preview"><span class="hint">选择文件后先调用后端预览，确认字段映射再导入。</span></div><div id="db-pro-import-mapping" class="db-pro-import-mapping"></div><div class="db-pro-conflict-note">仅执行 INSERT。导入请求带 session_id，默认加入当前页签事务；后端会返回逐行错误与 rows_affected。</div>';
    let parsedRows = [], fileName = '', format = '', encoded = '', previewData = null, previewTarget = null, previewVersion = 0;
    const modal = createModal({
      title: '数据导入向导', body: body,
      actions: [
        { text: '提交导入', className: 'btn btn-primary', close: false, onClick: async function (button) {
          if (!previewData) { toast('请先选择文件并完成后端预览', 'warn'); return; }
          const table = body.querySelector('#db-pro-import-table').value.trim(), schema = body.querySelector('#db-pro-import-schema').value.trim(), session = activeSession();
          if (!table) { toast('请填写目标表', 'warn'); return; }
          if (!session.sessionId) { toast('导入需要当前页签 session_id，请先打开可执行查询页签', 'warn'); return; }
          const mappings = Array.from(body.querySelectorAll('[data-import-map]')).map(function (row) { const target = row.querySelector('[data-map-target]'), type = row.querySelector('[data-map-type]'), nullable = row.querySelector('[data-map-nullable]'); return { source_index: Number(row.dataset.sourceIndex), source_name: row.dataset.sourceName || '', target: target && target.value || '', type: type && type.value || 'string', nullable: !!(nullable && nullable.checked) }; }).filter(function (item) { return item.target; });
          if (!previewTarget || previewTarget.sourceId !== sourceId() || previewTarget.schema !== schema || previewTarget.table !== table) { toast('目标已改变，请重新选择文件并预览', 'warn'); return; }
          const rows = parsedRows.length ? parsedRows.slice(1) : [];
          if (!previewData.total_rows || !mappings.length) { toast('没有可导入的数据或目标列映射', 'warn'); return; }
          if ((previewData.total_rows || rows.length) > MAX_IMPORT_ROWS) { toast('导入行数超过最大限制（' + MAX_IMPORT_ROWS + ' 行）', 'warn'); return; }
          const production = sourceIsProduction(), confirm = production ? window.confirm('当前数据源标记为生产环境，确认导入数据？') : false;
          if (production && !confirm) return;
          button.disabled = true; button.textContent = '提交中…';
          const detail = { sourceId: sourceId(), sourceName: sourceName(), fileName: fileName, format: format, schema: schema, table: table, mappings: mappings, rows: rows, dataBase64: encoded, totalRows: previewData.total_rows, sessionId: session.sessionId, commit: false, confirm: !!confirm, dialect: dialect() };
          emit('kairo:database-import', detail);
          try {
            let result;
            if (state.adapters.import) result = await state.adapters.import(Object.assign({ phase: 'apply' }, detail));
            else result = await api('POST', '/api/database/import/apply', { source_id: detail.sourceId, schema: detail.schema, table: detail.table, mappings: detail.mappings, format: detail.format, data_base64: detail.dataBase64, session_id: detail.sessionId, commit: detail.commit, confirm: detail.confirm });
            syncMutationTransaction(detail, result, true);
            renderImportResults(body, result && (result.result || result));
            toast('导入完成，共处理 ' + ((result.result || result).processed || 0) + ' 行', 'ok');
            previewData = null; // A successful import cannot be resubmitted accidentally.
          } catch (error) {
            syncMutationTransaction(detail, error && error.data, false);
            renderImportResults(body, error && error.data && (error.data.result || error.data));
            if (error && Number(error.status) === 409) toast('导入存在冲突，请查看逐行错误', 'warn');
            else toast('导入失败：' + (error.message || error), 'err');
          } finally { button.disabled = false; button.textContent = '提交导入'; }
        } },
        { text: '关闭', className: 'btn', close: true }
      ]
    });
    const file = body.querySelector('#db-pro-import-file'), preview = body.querySelector('#db-pro-import-preview'), mappingHost = body.querySelector('#db-pro-import-mapping');
    function renderLocalPreview(rows) {
      const shown = rows.slice(0, 21), headers = rows[0] || [];
      preview.innerHTML = shown.length ? '<div class="db-pro-import-summary">本地预览 · ' + esc(fileName) + ' · ' + Math.max(0, rows.length - 1) + ' 行 · ' + headers.length + ' 列</div><div class="db-pro-import-table-wrap"><table class="db-pro-import-table"><thead><tr>' + headers.map(function (value) { return '<th>' + esc(value) + '</th>'; }).join('') + '</tr></thead><tbody>' + shown.slice(1).map(function (row) { return '<tr>' + headers.map(function (_, index) { return '<td>' + esc(row[index] || '') + '</td>'; }).join('') + '</tr>'; }).join('') + '</tbody></table></div>' : '<span class="text-err">文件没有可导入内容</span>';
    }
    function renderMapping(data) {
      const table = data && data.table || {}, headers = table.headers || [], fields = data && data.fields || [], suggested = data && data.suggested_mapping || [];
      const options = '<option value="">跳过此列</option>' + fields.map(function (field) { const name = typeof field === 'string' ? field : field.name; return '<option value="' + esc(name || '') + '">' + esc(name || '') + '</option>'; }).join('');
      mappingHost.innerHTML = headers.map(function (header, index) { const item = suggested[index] || {}, target = item.target || ''; return '<div class="db-pro-import-map-row" data-import-map data-source-index="' + index + '" data-source-name="' + esc(header) + '"><strong>' + esc(header) + '</strong><select class="editor-input" data-map-target aria-label="' + esc(header) + ' 目标列">' + options + '</select><select class="editor-input" data-map-type aria-label="' + esc(header) + ' 类型"><option value="string">string</option><option value="int64">int64</option><option value="decimal">decimal</option><option value="float">float</option><option value="bool">bool</option><option value="date">date</option><option value="datetime">datetime</option><option value="json">json</option><option value="bytes">bytes</option></select><label class="db-pro-check"><input type="checkbox" data-map-nullable> 空值为 NULL</label></div>'; }).join('');
      mappingHost.querySelectorAll('[data-import-map]').forEach(function (row, index) { const item = suggested[index] || {}, target = row.querySelector('[data-map-target]'), type = row.querySelector('[data-map-type]'), nullable = row.querySelector('[data-map-nullable]'); if (target) target.value = item.target || ''; if (type) type.value = item.type || 'string'; if (nullable) nullable.checked = !!item.nullable; });
    }
    function readAsBase64(picked, version) {
      const reader = new FileReader(); reader.onload = function () { if (version !== previewVersion) return; const data = String(reader.result || ''), comma = data.indexOf(','); encoded = comma >= 0 ? data.slice(comma + 1) : data; requestPreview(version); }; reader.onerror = function () { toast('读取文件失败', 'err'); }; reader.readAsDataURL(picked);
    }
    function encodeTextBase64(value) {
      const text = String(value || '');
      if (window.TextEncoder && window.btoa) { const bytes = new TextEncoder().encode(text); let binary = ''; for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000)); return btoa(binary); }
      return window.btoa(unescape(encodeURIComponent(text)));
    }
    function rowsAsCSV(rows) {
      return rows.map(function (row) { return row.map(function (value) { const text = String(value == null ? '' : value); return /[",\r\n]/.test(text) ? '"' + text.replace(/"/g, '""') + '"' : text; }).join(','); }).join('\r\n');
    }
    function requestPreview(version) {
      const schema = body.querySelector('#db-pro-import-schema').value.trim(), table = body.querySelector('#db-pro-import-table').value.trim();
      if (!sourceId() || !table) { toast('请先选择数据源并填写目标表', 'warn'); return; }
      if (version !== previewVersion) return;
      const target = { sourceId: sourceId(), schema: schema, table: table };
      preview.innerHTML = '<span class="hint">正在读取目标表字段并生成映射…</span>';
      api('POST', '/api/database/import/preview', { source_id: sourceId(), schema: schema, table: table, format: format || 'csv', data_base64: encoded }).then(function (data) {
        if (version !== previewVersion) return;
        previewTarget = target;
        previewData = data.preview || data;
        renderMapping(previewData);
        if (previewData.total_rows > MAX_IMPORT_ROWS) {
          toast('文件包含 ' + previewData.total_rows + ' 行，超过最大导入行数限制（' + MAX_IMPORT_ROWS + ' 行）', 'warn');
        }
        if (!parsedRows.length && previewData.table && previewData.table.rows) {
          preview.innerHTML = '<div class="db-pro-import-summary">后端预览 · ' + esc(fileName) + ' · 共 ' + esc(previewData.total_rows || previewData.table.rows.length) + ' 行</div>';
        }
      }).catch(function (error) {
        if (version !== previewVersion) return;
        previewData = null;
        mappingHost.innerHTML = '';
        if (parsedRows.length) renderLocalPreview(parsedRows);
        toast('导入预览失败：' + (error.message || error), 'err');
      });
    }
    file.addEventListener('change', function () {
      const version = ++previewVersion; previewData = null; previewTarget = null; encoded = ''; parsedRows = []; mappingHost.innerHTML = '';
      const picked = file.files && file.files[0]; if (!picked) return; fileName = picked.name;
      if (picked.size > 8 * 1024 * 1024) { toast('文件不能超过 8 MiB', 'warn'); return; }
      format = /\.xlsx$/i.test(fileName) ? 'xlsx' : 'csv';
      if (format === 'csv') {
        const reader = new FileReader();
        reader.onload = function () {
          if (version !== previewVersion) return;
          const isTSV = /\.tsv$/i.test(fileName);
          parsedRows = parseCSV(reader.result || '', isTSV ? '\t' : undefined);
          const dataRowCount = Math.max(0, parsedRows.length - 1);
          if (dataRowCount > MAX_IMPORT_ROWS) {
            file.value = '';
            parsedRows = [];
            preview.innerHTML = '<span class="text-err">文件包含 ' + dataRowCount + ' 行，超过最大导入行数限制（' + MAX_IMPORT_ROWS + ' 行）</span>';
            toast('导入行数不能超过 ' + MAX_IMPORT_ROWS + ' 行', 'warn');
            return;
          }
          renderLocalPreview(parsedRows);
          if (isTSV) { encoded = encodeTextBase64(rowsAsCSV(parsedRows)); requestPreview(version); }
          else readAsBase64(picked, version);
        };
        reader.readAsText(picked);
      }
      else { parsedRows = []; preview.innerHTML = '<span class="hint">XLSX 将由后端解析…</span>'; readAsBase64(picked, version); }
    });
  }

  function renderImportResults(body, result) {
    const rows = result && (result.rows || result.results);
    if (!Array.isArray(rows)) return;
    let host = body.querySelector('#db-pro-import-results');
    if (!host) { host = document.createElement('div'); host.id = 'db-pro-import-results'; host.className = 'db-pro-import-results'; body.appendChild(host); }
    host.innerHTML = '<div class="db-pro-import-summary">影响行数：' + esc(result.rows_affected == null ? '-' : result.rows_affected) + ' · 成功处理：' + esc(result.processed == null ? rows.length : result.processed) + '</div><table class="db-pro-import-table"><thead><tr><th>行</th><th>状态</th><th>错误</th></tr></thead><tbody>' + rows.map(function (row) { return '<tr><td>' + esc(row.row == null ? row.index : row.row) + '</td><td>' + esc(row.status || '') + '</td><td>' + esc(row.error || '') + '</td></tr>'; }).join('') + '</tbody></table>';
  }

  // 历史重放的来源解析：原始 sourceId 必须保留；来源未知或已删除时要求用户
  // 显式选择，绝不生成带错误 sourceId 的执行链接（新窗口深链接依赖它）。
  function historySourceChoices() {
    const select = id('db-source');
    const list = [];
    if (select && select.options) {
      Array.prototype.forEach.call(select.options, function (option) {
        if (!option || !option.value) return;
        list.push({ id: String(option.value), name: String(option.textContent || '').trim() || String(option.value) });
      });
    }
    if (!list.length) {
      Object.keys(state.sourceCatalog || {}).forEach(function (key) {
        const item = state.sourceCatalog[key];
        list.push({ id: String(key), name: (item && item.name) || String(key) });
      });
    }
    return list;
  }
  function askHistorySource() {
    const choices = historySourceChoices();
    if (!choices.length) {
      toast('没有可用的数据源，无法为这条历史生成执行链接', 'warn');
      return Promise.resolve('');
    }
    const label = '这条历史记录的原数据源未知或已删除，请明确选择要执行的数据源：\n'
      + choices.map(function (item, index) { return (index + 1) + ') ' + item.id + ' — ' + item.name; }).join('\n');
    const parse = function (answer) {
      const text = String(answer == null ? '' : answer).trim();
      if (!text) return '';                       // 用户取消：不生成任何执行链接
      const byIndex = choices[Number(text) - 1];
      const picked = byIndex || choices.filter(function (item) { return item.id === text; })[0];
      if (!picked) { toast('未识别选择的数据源，已取消生成链接', 'warn'); return ''; }
      return picked.id;
    };
    if (K.overlays && typeof K.overlays.prompt === 'function') {
      return Promise.resolve(K.overlays.prompt({ title: '选择历史查询的数据源', label: label, defaultValue: '' }))
        .then(parse, function () { return ''; });
    }
    return Promise.resolve(parse(window.prompt(label, '')));
  }
  function replayHistoryEntry(entry) {
    if (!entry || !entry.sql) return Promise.resolve('');
    const known = historySourceChoices().map(function (item) { return item.id; });
    const original = String(entry.sourceId || '');
    const copyLink = function (sourceIdValue) {
      const url = buildDeepLink({ sourceId: sourceIdValue, sql: entry.sql, autoRun: true });
      const copy = K.core && K.core.copyToClipboard;
      Promise.resolve(copy ? copy(url) : null).then(function () { toast('历史查询深链接已复制', 'ok'); });
      return url;
    };
    if (original && known.indexOf(original) >= 0) return Promise.resolve(copyLink(original));
    return askHistorySource().then(function (picked) { return picked ? copyLink(picked) : ''; });
  }

  function openHistory() {
    const scopeKey = historyKeyFor();
    loadHistory(scopeKey);
    const body = document.createElement('div');
    body.innerHTML = '<div class="db-pro-history-toolbar"><input id="db-pro-history-search" class="editor-input" type="search" placeholder="搜索 SQL、数据源、状态…" autofocus><select id="db-pro-history-scope" class="editor-input" title="历史范围"><option value="' + esc(scopeKey) + '">当前数据源</option><option value="' + LEGACY_HISTORY_BUCKET + '">' + esc(LEGACY_HISTORY_LABEL) + '</option></select><select id="db-pro-history-status" class="editor-input"><option value="">全部状态</option><option value="success">成功</option><option value="error">失败</option><option value="running">执行中</option><option value="interrupted">已中断</option><option value="canceled">已取消</option><option value="unknown">未知</option></select></div><div id="db-pro-history-list" class="db-pro-history-list"></div>';
    const modal = createModal({
      title: '查询历史', body: body,
      actions: [
        { text: '清空当前范围', className: 'btn btn-danger', close: false, onClick: function () { if (!state.history.length || window.confirm('清空当前范围的查询历史？')) { clearHistory(currentScope()); draw(); toast('查询历史已清空', 'ok'); } } },
        { text: '关闭', className: 'btn', close: true }
      ]
    });
    const search = body.querySelector('#db-pro-history-search'), status = body.querySelector('#db-pro-history-status'), scope = body.querySelector('#db-pro-history-scope'), list = body.querySelector('#db-pro-history-list');
    function currentScope() { return scope && scope.value ? scope.value : scopeKey; }
    function draw() {
      // 单一仓库：切换范围只重新读取同一份结构化历史，不做任何旧字符串合并。
      loadHistory(currentScope());
      const needle = search.value.trim().toLowerCase(), wanted = status.value;
      const items = state.history.filter(function (entry) { const hay = [entry.sql, entry.sourceName, entry.status, entry.error].join(' ').toLowerCase(); return (!needle || hay.indexOf(needle) >= 0) && (!wanted || entry.status === wanted); });
      list.innerHTML = items.slice(0, 100).map(function (entry, index) { const date = entry.startedAt ? new Date(entry.startedAt).toLocaleString() : '-'; return '<article class="db-pro-history-card"><header><strong>' + esc(date) + '</strong><span>' + esc(entry.sourceName || entry.sourceId || '未绑定') + '</span><span class="db-pro-history-status ' + esc(entry.status || 'unknown') + '">' + esc(entry.status === 'success' ? '成功' : entry.status === 'error' ? '失败' : entry.status === 'running' ? '执行中' : entry.status === 'canceled' ? '已取消' : entry.status === 'interrupted' ? '已中断' : '未知') + '</span><span>' + esc(entry.elapsedMs == null ? '-' : entry.elapsedMs + ' ms') + '</span></header><pre>' + esc(entry.sql) + '</pre><footer><button type="button" class="btn btn-xs" data-history-insert="' + index + '">插入</button><button type="button" class="btn btn-xs" data-history-link="' + index + '">新窗口链接</button><button type="button" class="btn btn-xs btn-danger" data-history-delete="' + index + '">删除</button></footer></article>'; }).join('') || '<div class="db-pro-history-empty">没有匹配的查询历史</div>';
      list.querySelectorAll('[data-history-insert]').forEach(function (button) { button.onclick = function () { const item = items[Number(button.dataset.historyInsert)]; if (item) { setEditorValue(item.sql, true); closeModal(); } }; });
      list.querySelectorAll('[data-history-link]').forEach(function (button) { button.onclick = function () { const item = items[Number(button.dataset.historyLink)]; if (!item) return; Promise.resolve(replayHistoryEntry(item)).catch(function (error) { toast('生成历史链接失败：' + (error && error.message ? error.message : error), 'err'); }); }; });
      list.querySelectorAll('[data-history-delete]').forEach(function (button) { button.onclick = function () { const item = items[Number(button.dataset.historyDelete)]; if (!item) return; deleteHistoryEntry(item.id, currentScope()); draw(); }; });
    }
    search.addEventListener('input', draw);
    status.addEventListener('change', draw);
    if (scope) { scope.addEventListener('change', function () { loadHistory(currentScope()); draw(); }); scope.value = scopeKey; }
    draw();
  }

  function annotateErrorMessage(root) {
    const box = root.querySelector('#db-result-message'); if (!box || box.dataset.dbProErrorBound === '1' || box.hidden) return;
    const location = parseErrorLocation(box.textContent); if (!location) return;
    box.dataset.dbProErrorBound = '1';
    const button = document.createElement('button'); button.type = 'button'; button.className = 'btn btn-xs db-pro-jump-error'; button.textContent = '跳转到第 ' + (location.line || '?') + ' 行' + (location.column ? '第 ' + location.column + ' 列' : ''); button.title = '将编辑器光标定位到数据库错误位置';
    button.onclick = function () { if (!jumpToLocation(editor(), location)) toast('当前编辑器不可用', 'warn'); };
    box.appendChild(button);
  }
  function enhanceBookmarks() {
    document.querySelectorAll('.db-bookmark-card').forEach(function (card) {
      if (card.dataset.dbProLinkBound === '1') return;
      const pre = card.querySelector('pre'), name = card.querySelector('strong'); if (!pre) return;
      card.dataset.dbProLinkBound = '1';
      const button = createQuickButton('', '新窗口', '在新窗口打开该收藏查询', function () {
        const bookmarkSourceId = card.dataset.sourceId || card.getAttribute('data-source-id') || '';
        const child = openIsolatedWindow(buildDeepLink({ sourceId: bookmarkSourceId || sourceId(), sql: pre.textContent || '', autoRun: true }));
        if (!child) toast('浏览器阻止了新窗口', 'warn');
      });
      (card.querySelector('.db-bookmark-card-actions') || card).appendChild(button);
      if (name) name.title = '点击新窗口打开收藏：' + name.textContent;
    });
  }
  // ---- 数据库快捷键统一分发器（DBUI-05） ----
  // database.js 与本模块不再各自抢占 capture 监听，而是把命令注册到同一个分发器：
  // 修饰键精确匹配（Ctrl+F 不再吞掉 Ctrl+Shift+F）、统一平台 Ctrl/Cmd、
  // 忽略 IME 组合输入、按焦点作用域（编辑器/工作区/弹窗）过滤，并且一个命令执行后
  // 同一事件不再执行后续命令。已处理事件（defaultPrevented）一律不再重复处理。
  const COMMAND_REGISTRY_VERSION = 1;
  function isMacPlatform() {
    let probe = '';
    try { probe = String((navigator && navigator.platform) || '') + ' ' + String((navigator && navigator.userAgent) || ''); } catch (_) { probe = ''; }
    return /mac|iphone|ipad|darwin/i.test(probe);
  }
  function parseShortcutSpec(spec) {
    const combo = { ctrl: false, alt: false, shift: false, meta: false, key: '' };
    String(spec || '').toLowerCase().replace(/\s+/g, '').split('+').filter(Boolean).forEach(function (part) {
      if (part === 'ctrl' || part === 'control') combo.ctrl = true;
      else if (part === 'alt' || part === 'option') combo.alt = true;
      else if (part === 'shift') combo.shift = true;
      else if (part === 'meta' || part === 'cmd' || part === 'command') combo.meta = true;
      else combo.key = part;
    });
    return combo;
  }
  function shortcutMatches(event, spec) {
    if (!event || !spec) return false;
    const combo = parseShortcutSpec(spec);
    if (!combo.key) return false;
    const actual = String(event.key || '').toLowerCase(), code = String(event.code || '').toLowerCase();
    const keyOk = actual === combo.key || code === combo.key
      || (combo.key === 'space' && (actual === ' ' || code === 'space'))
      || (combo.key === 'esc' && actual === 'escape')
      || (combo.key === 'enter' && code === 'numpadenter');
    if (!keyOk) return false;
    const altOk = !!event.altKey === combo.alt;
    const shiftOk = !!event.shiftKey === combo.shift;
    // macOS 兼容：写法为 Ctrl 的组合在 Mac 上接受 Cmd（meta）或 Ctrl。
    if (combo.ctrl && !combo.meta && isMacPlatform()) return !!(event.ctrlKey || event.metaKey) && altOk && shiftOk;
    return !!event.ctrlKey === combo.ctrl && altOk && shiftOk && !!event.metaKey === combo.meta;
  }
  function hasOpenOverlay() {
    try { return !!(document.body && document.body.classList && document.body.classList.contains('has-open-overlay')); } catch (_) { return false; }
  }
  function commandScope(event) {
    const target = event && event.target;
    if (state.modal || hasOpenOverlay()) return 'modal';
    if (target && target.id && String(target.id).indexOf('db-shortcut-') === 0) return 'settings';
    const e = editor();
    if (e && target === e) return 'editor';
    if (!target || target === document.body || target === document.documentElement) return 'workspace';
    const tag = String(target.tagName || '').toUpperCase();
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable) return 'outside';
    if (typeof target.closest === 'function' && target.closest('#db-workspace, .db-sql-layout, #view')) return 'workspace';
    return 'outside';
  }
  function createCommandRegistry() {
    const registry = {
      version: COMMAND_REGISTRY_VERSION,
      commands: []
    };
    registry.register = function (command) {
      if (!command || !command.name || typeof command.run !== 'function') return null;
      if (!command.spec && typeof command.specFn !== 'function') return null;
      const entry = {
        name: String(command.name),
        spec: command.spec || '',
        specFn: typeof command.spec === 'function' ? command.spec : null,
        scope: Array.isArray(command.scope) ? command.scope.slice() : ['editor', 'workspace'],
        priority: Number(command.priority) || 0,
        when: typeof command.when === 'function' ? command.when : null,
        run: command.run,
        stopPropagation: command.stopPropagation === true,
        owner: command.owner || ''
      };
      registry.commands = registry.commands.filter(function (item) { return item.name !== entry.name; });
      registry.commands.push(entry);
      registry.commands.sort(function (a, b) { return (b.priority - a.priority) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0); });
      return entry;
    };
    registry.unregister = function (name) {
      registry.commands = registry.commands.filter(function (item) { return item.name !== name; });
    };
    registry.names = function () { return registry.commands.map(function (item) { return item.name; }); };
    registry.list = function () { return registry.commands.slice(); };
    registry.dispatch = function (event) { return dispatchCommand(registry, event); };
    return registry;
  }
  function commandShortcut(entry) { return entry.specFn ? entry.specFn() : entry.spec; }
  function dispatchCommand(registry, event) {
    if (!event || !registry) return '';
    if (String(location.hash || '').indexOf('#/database') !== 0) return '';
    // IME 组合输入期间任何数据库命令都不得触发。
    if (event.isComposing || event.keyCode === 229) return '';
    // 其他模块（或本模块的兜底监听）已经处理过的事件不再重复处理。
    if (event.defaultPrevented) return '';
    const scope = commandScope(event);
    const context = { scope: scope, event: event, editor: editor(), registry: registry };
    for (let i = 0; i < registry.commands.length; i++) {
      const entry = registry.commands[i];
      if (entry.scope.indexOf(scope) < 0) continue;
      if (!shortcutMatches(event, commandShortcut(entry))) continue;
      if (entry.when && !entry.when(event, context)) continue;
      event.preventDefault();
      if (entry.stopPropagation && event.stopImmediatePropagation) event.stopImmediatePropagation();
      context.command = entry.name;
      entry.run(event, context);
      return entry.name;
    }
    return '';
  }
  function commandRegistry() {
    if (K.databaseCommands && typeof K.databaseCommands.register === 'function' && K.databaseCommands.version === COMMAND_REGISTRY_VERSION) return K.databaseCommands;
    const registry = createCommandRegistry();
    K.databaseCommands = registry;
    // database.js 可能先于本模块加载并挂过兜底 capture 监听；注册表就绪后通知它
    // 把命令迁移进来并撤掉兜底监听，最终只保留一个 capture 分发。
    if (typeof window.dispatchEvent === 'function') emit('kairo:database-commands-ready', { version: COMMAND_REGISTRY_VERSION });
    return registry;
  }
  const commands = commandRegistry();
  function completionOpen() { return !!(state.ac && state.ac.hidden !== true && state.acItems && state.acItems.length); }
  function moveCompletion(step) {
    if (!state.ac || !state.acItems || !state.acItems.length) return;
    state.acIndex = (state.acIndex + step + state.acItems.length) % state.acItems.length;
    state.ac.querySelectorAll('.db-pro-ac-item').forEach(function (item, index) {
      item.classList.toggle('active', index === state.acIndex);
      item.setAttribute('aria-selected', index === state.acIndex ? 'true' : 'false');
    });
  }
  function registerFeatureCommands() {
    const editorOnly = ['editor'];
    commands.register({ name: 'db.find', owner: 'features', spec: 'Ctrl+F', scope: editorOnly, priority: 60, stopPropagation: true, run: function () { openFindReplace(false); } });
    commands.register({ name: 'db.replace', owner: 'features', spec: 'Ctrl+H', scope: editorOnly, priority: 60, stopPropagation: true, run: function () { openFindReplace(true); } });
    commands.register({ name: 'db.save', owner: 'features', spec: 'Ctrl+S', scope: editorOnly, priority: 50, stopPropagation: true, run: function () { saveSQL(false); } });
    commands.register({ name: 'db.open', owner: 'features', spec: 'Ctrl+O', scope: editorOnly, priority: 50, stopPropagation: true, run: function () { openSQLInput(); } });
    commands.register({ name: 'db.complete', owner: 'features', spec: 'Ctrl+Space', scope: editorOnly, priority: 50, stopPropagation: true, when: function () { return !id('db-sql-ac'); }, run: function () { maybeContextCompletion(true); } });
    commands.register({ name: 'db.completion.close', owner: 'features', spec: 'Escape', scope: editorOnly, priority: 95, stopPropagation: true, when: function () { return !!state.ac; }, run: function () { hideCompletion(); } });
    commands.register({ name: 'db.completion.next', owner: 'features', spec: 'ArrowDown', scope: editorOnly, priority: 90, stopPropagation: true, when: completionOpen, run: function () { moveCompletion(1); } });
    commands.register({ name: 'db.completion.previous', owner: 'features', spec: 'ArrowUp', scope: editorOnly, priority: 90, stopPropagation: true, when: completionOpen, run: function () { moveCompletion(-1); } });
    commands.register({ name: 'db.completion.accept', owner: 'features', spec: 'Enter', scope: editorOnly, priority: 90, stopPropagation: true, when: completionOpen, run: function () { acceptCompletion(); } });
    commands.register({ name: 'db.completion.accept-tab', owner: 'features', spec: 'Tab', scope: editorOnly, priority: 90, stopPropagation: true, when: completionOpen, run: function () { acceptCompletion(); } });
  }
  function bindGlobalKeys() {
    if (bindGlobalKeys.bound) return; bindGlobalKeys.bound = true;
    registerFeatureCommands();
    document.addEventListener('click', function (event) {
      if (String(location.hash || '').indexOf('#/database') !== 0) return;
      const target = event.target && event.target.closest ? event.target.closest('#db-run') : null;
      if (target) beginRunCapture(target);
    }, true);
    // 唯一的数据库键盘捕获监听：所有命令都经分发器精确匹配后执行。
    document.addEventListener('keydown', function (event) { dispatchCommand(commands, event); }, true);
  }

  function install(root) {
    if (!root || !root.querySelector) return;
    // Source management is rendered beside #db-workspace and can exist even
    // on the empty-state page, so mount/sync it before requiring an editor.
    loadSourceCatalog(); ensureSourceManager(document);
    const editorNode = root.querySelector('#db-sql'), layout = root.querySelector('.db-sql-layout');
    if (!editorNode && !layout) return;
    state.root = root; ensureToolbar(root); annotateErrorMessage(root); enhanceBookmarks();
    ensureSourceManager(root);
    const viewer = root.querySelector('#db-object-viewer'); if (viewer && viewer.hidden === false) emit('kairo:database-object-viewer', { viewer: viewer });
  }
  function start() {
    if (state.observed) return; state.observed = true; bindGlobalKeys();
    const nav = typeof navigator !== 'undefined' ? navigator : null, connection = nav && nav.connection;
    const lowSpec = (nav && nav.hardwareConcurrency && nav.hardwareConcurrency <= 4) || (nav && nav.deviceMemory && nav.deviceMemory <= 4) || (connection && connection.saveData);
    if (lowSpec && document.documentElement) document.documentElement.classList.add('db-low-spec');
    state.view = document.getElementById('view') || document.body;
    let updating = false;
    const safeInstall = function (target) {
      if (updating) return;
      updating = true;
      try {
        observer.disconnect();
        install(target || document.getElementById('db-workspace') || state.view);
      } finally {
        observer.takeRecords();
        if (String(location.hash || '').indexOf('#/database') === 0) {
          observer.observe(state.view, { subtree: true, childList: true, attributes: true, attributeFilter: ['hidden', 'class'] });
        }
        updating = false;
      }
    };
    const observer = new MutationObserver(function () {
      if (String(location.hash || '').indexOf('#/database') !== 0) return;
      if (updating) return;
      // One animation-frame for a complete route render avoids a cascade of
      // layout reads while database.js replaces its workspace DOM.
      if (start.scheduled) return;
      start.scheduled = true;
      schedule(function () {
        start.scheduled = false;
        if (String(location.hash || '').indexOf('#/database') !== 0) return;
        safeInstall();
      });
    });
    const syncRoute = function () {
      observer.disconnect();
      if (String(location.hash || '').indexOf('#/database') === 0) {
        safeInstall();
      } else {
        if (state.disposeEditor) state.disposeEditor();
        hideCompletion();
        state.root = null;
      }
    };
    window.addEventListener('hashchange', syncRoute);
    syncRoute();
  }

  F.tokenizeSQL = tokenizeSQL;
  F.highlightSQL = highlightSQL;
  F.formatSQL = formatSQL;
  F.splitStatements = splitStatements;
  F.findParameters = findParameters;
  F.scanParameters = scanParameters;
  F.getBoundParameters = getBoundParametersForExecution;
  // 绑定缓存接口：页面层与测试可以读取/写入当前页签的绑定值。
  // 扫描未完成时不会破坏性裁剪，只有确认完成的扫描结果才能删除参数（DBUI-04）。
  F.setBindings = function (values) { state.bindings = Object.assign(Object.create(null), values || {}); };
  F.getBindings = function () { return Object.assign({}, state.bindings || {}); };
  F.pruneBindingsAgainst = pruneBindingsAgainst;
  F.parseErrorLocation = parseErrorLocation;
  F.locationOffset = locationOffset;
  F.jumpToLocation = jumpToLocation;
  F.parseCSV = parseCSV;
  F.buildDeepLink = buildDeepLink;
  F.openFindReplace = openFindReplace;
  F.openParameterDialog = openParameterDialog;
  F.runScript = runScript;
  F.openHistory = openHistory;
  F.addHistory = addHistory;
  // 查询历史单一仓库接口（DBUI-06）：页面层下拉、结构化弹窗与测试都走这里。
  F.loadHistory = loadHistory;
  F.listHistory = listStoredHistory;
  F.deleteHistory = deleteHistoryEntry;
  F.clearHistory = clearHistory;
  F.replayHistoryEntry = replayHistoryEntry;
  F.historyStore = historyStore;
  F.LEGACY_HISTORY_BUCKET = LEGACY_HISTORY_BUCKET;
  F.LEGACY_HISTORY_LABEL = LEGACY_HISTORY_LABEL;
  // 统一快捷键分发器（DBUI-05）：database.js 通过它注册命令，不再各自抢占 capture。
  F.commands = commands;
  F.dispatchCommand = function (event) { return dispatchCommand(commands, event); };
  F.registerCommand = function (command) { return commands.register(command); };
  F.shortcutMatches = shortcutMatches;
  F.recordQueryStart = recordQueryStart;
  F.recordQueryResult = recordQueryResult;
  F.openImportWizard = openImportWizard;
  F.saveSQL = saveSQL;
  F.openSQL = openSQLInput;
  F.refreshSourceCatalog = refreshSourceCatalog;
  // The base page owns the source CRUD form, while this module owns the
  // advanced environment/TLS/SSH metadata cache. Keep the two views in sync
  // after a save/delete so a later edit cannot resurrect stale security flags.
  F.syncSource = function (item) {
    if (!item || !item.id) return;
    state.sourceCatalog[item.id] = item;
    state.sourceCatalogLoaded = true;
    enhanceSourceSelect();
  };
  F.removeSource = function (sourceIdValue) {
    if (!sourceIdValue) return;
    delete state.sourceCatalog[String(sourceIdValue)];
    enhanceSourceSelect();
  };
  // Kept public for the optional object designer module.  The implementation
  // remains here so every database feature shares one keyboard/focus-safe
  // modal and there is no second visual shell to maintain.
  F.createModal = createModal;
  F.closeModal = closeModal;
  F.setEditorValue = setEditorValue;
  F.literal = literal;
  F.setAdapter = function (name, adapter) { if (Object.prototype.hasOwnProperty.call(state.adapters, name)) state.adapters[name] = typeof adapter === 'function' ? adapter : null; };
  F.getAdapter = function (name) { return state.adapters[name] || null; };
  F.getPendingGridMutations = function () { return pendingGridMutations().slice(); };
  F.pendingGridMutations = F.getPendingGridMutations;
  F.clearPendingGridMutations = clearGridMutations;
  F.clearGridMutations = clearGridMutations;
  F.resolveGridTarget = resolveGridTarget;
  F.start = start;

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start); else start();
})();
