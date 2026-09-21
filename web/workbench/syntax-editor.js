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
    java: /\b(abstract|assert|boolean|break|byte|case|catch|char|class|const|continue|default|do|double|else|enum|extends|final|finally|float|for|goto|if|implements|import|instanceof|int|interface|long|native|new|package|private|protected|public|return|short|static|strictfp|super|switch|synchronized|this|throw|throws|transient|try|void|volatile|while|true|false|null|var|record|yield|sealed|permits|non-sealed|String|Integer|Long|Boolean|Double|Float|Byte|Short|Character|Object|List|Map|Set)\b/g,
    xml: /(&lt;\/?[A-Za-z_][^&]*?&gt;)/g,
    json: /("(?:\\.|[^"\\])*"\s*:|\b(?:true|false|null)\b|-?\b\d+(?:\.\d+)?\b)/g
  };

  function highlightJava(source) {
    const parts = source.split(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\/\/[^\n]*|\/\*[\s\S]*?\*\/)/g);
    return parts.map(function (part) {
      if (!part) return '';
      if (part.startsWith('//') || part.startsWith('/*')) {
        return '<span class="syn-comment">' + escape(part) + '</span>';
      }
      if (part.startsWith('"') || part.startsWith("'")) {
        return '<span class="syn-string">' + escape(part) + '</span>';
      }
      let s = escape(part);
      s = s.replace(keywords.java, '<span class="syn-keyword">$1</span>');
      s = s.replace(/(@[A-Za-z0-9_]+)/g, '<span class="syn-attr">$1</span>');
      return s;
    }).join('');
  }

  function highlightJsp(source) {
    const parts = source.split(/(<%--[\s\S]*?--%>|<!--[\s\S]*?-->|<%@[\s\S]*?%>|<%=[\s\S]*?%>|<%![\s\S]*?%>|<%[\s\S]*?%>|\$\{[^}\n]*?\}|<\/?[A-Za-z0-9_:-]+(?:\s+[^>]*)?\/?>)/g);
    return parts.map(function (part) {
      if (!part) return '';
      if (part.startsWith('<%--') || part.startsWith('<!--')) {
        return '<span class="syn-comment">' + escape(part) + '</span>';
      }
      if (part.startsWith('<%@')) {
        return '<span class="syn-tag">' + escape(part) + '</span>';
      }
      if (part.startsWith('<%=') || part.startsWith('<%!') || part.startsWith('<%')) {
        const isExpr = part.startsWith('<%=');
        const isDecl = part.startsWith('<%!');
        const openTag = isExpr ? '&lt;%=' : (isDecl ? '&lt;%!' : '&lt;%');
        const code = isExpr || isDecl ? part.slice(3, -2) : part.slice(2, -2);
        return '<span class="syn-tag">' + openTag + '</span>' + highlightJava(code) + '<span class="syn-tag">%&gt;</span>';
      }
      if (part.startsWith('${')) {
        return '<span class="syn-attr">' + escape(part) + '</span>';
      }
      if (part.startsWith('<') && part.endsWith('>')) {
        return '<span class="syn-tag">' + escape(part) + '</span>';
      }
      return escape(part);
    }).join('');
  }

  function highlight(source, language) {
    const raw = String(source == null ? '' : source);
    const value = escape(raw);
    language = String(language || 'text').toLowerCase();
    if (raw.length > 220000 || language === 'text' || language === 'plain') return value;
    if (language === 'java') return highlightJava(raw);
    if (language === 'jsp') return highlightJsp(raw);
    if (language === 'xml') return value.replace(keywords.xml, '<span class="syn-tag">$1</span>');
    const re = keywords[language] || keywords.sql;
    // Tokenize first so quotes/comments are not colored as keywords.
    const parts = raw.split(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\/\/[^\n]*|--[^\n]*|\/\*[\s\S]*?\*\/)/g);
    return parts.map(function (part, i) {
      if (!part) return '';
      if (part.startsWith('//') || part.startsWith('--') || part.startsWith('/*')) return '<span class="syn-comment">' + escape(part) + '</span>';
      if (part.startsWith('"') || part.startsWith("'")) return '<span class="syn-string">' + escape(part) + '</span>';
      return escape(part).replace(re, '<span class="syn-keyword">$1</span>');
    }).join('');
  }
  function languageFromPath(path, fallback) {
    const ext = String(path || '').split('.').pop().toLowerCase();
    return ({ java: 'java', jsp: 'jsp', jspx: 'jsp', tag: 'jsp', tagx: 'jsp', sql: 'sql', xml: 'xml', wsdl: 'xml', json: 'json', log: 'text', txt: 'text' })[ext] || fallback || 'text';
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
