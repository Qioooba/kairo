/* Small safe syntax overlay used by Compare and future editors.  It is not a
 * compiler: unknown constructs remain plain text and all source is escaped. */
(function () {
  'use strict';
  const K = window.Kairo = window.Kairo || {};
  const W = K.workbench = K.workbench || {};
  const escape = function (value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]; });
  };
  const keywords = {
    sql: /\b(SELECT|FROM|WHERE|WITH|JOIN|LEFT|RIGHT|INNER|OUTER|GROUP|ORDER|BY|HAVING|AS|AND|OR|NOT|NULL|IS|IN|EXISTS|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP|CALL|BEGIN|END|CASE|WHEN|THEN|ELSE)\b/gi,
    java: /\b(public|private|protected|class|interface|extends|implements|static|final|void|new|return|if|else|for|while|try|catch|throws|throw|package|import|boolean|int|long|String)\b/g,
    xml: /(&lt;\/?[A-Za-z_][^&]*?&gt;)/g,
    json: /("(?:\\.|[^"\\])*"\s*:|\b(?:true|false|null)\b|-?\b\d+(?:\.\d+)?\b)/g
  };
  function highlight(source, language) {
    const raw = String(source == null ? '' : source);
    const value = escape(raw);
    language = String(language || 'text').toLowerCase();
    if (raw.length > 220000 || language === 'text' || language === 'plain') return value;
    if (language === 'xml') return value.replace(keywords.xml, '<span class="syn-tag">$1</span>');
    const re = keywords[language] || keywords.sql;
    // Tokenize first so quotes/comments are not colored as keywords.
    const parts = raw.split(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\/\/[^\n]*|--[^\n]*|\/\*[\s\S]*?\*\/)/g);
    return parts.map(function (part, i) {
      if (!part) return '';
      if (/^("|'|\/\/|--|\/\*)/.test(part)) return '<span class="syn-string">' + escape(part) + '</span>';
      return escape(part).replace(re, '<span class="syn-keyword">$1</span>');
    }).join('');
  }
  function languageFromPath(path, fallback) {
    const ext = String(path || '').split('.').pop().toLowerCase();
    return ({ java: 'java', sql: 'sql', xml: 'xml', wsdl: 'xml', json: 'json', log: 'text', txt: 'text' })[ext] || fallback || 'text';
  }
  function bind(textarea, code, options) {
    options = options || {};
    let lastValue = null, lastLanguage = null;
    const scroll = function () {
      code.scrollTop = textarea.scrollTop; code.scrollLeft = textarea.scrollLeft;
    };
    const sync = function () {
      const language = options.language || 'text';
      if (lastValue !== textarea.value || lastLanguage !== language) {
        code.innerHTML = highlight(textarea.value, language);
        lastValue = textarea.value; lastLanguage = language;
      }
      scroll();
      if (options.onInput) options.onInput(textarea.value);
    };
    textarea.addEventListener('input', sync);
    textarea.addEventListener('scroll', scroll);
    sync();
    return { dispose: function () { textarea.removeEventListener('input', sync); textarea.removeEventListener('scroll', scroll); }, refresh: sync, setLanguage: function (language) { options.language = language; sync(); } };
  }
  W.syntaxEditor = { escape: escape, highlight: highlight, languageFromPath: languageFromPath, bind: bind };
})();
