/*
 * Kairo 页面内搜索高亮（公共模块）
 *
 * createSearchHighlighter(opts) → controller
 *   opts:
 *     container: HTMLElement    — 要搜索/高亮的容器元素（必填）
 *     input: HTMLInputElement   — 搜索输入框（必填）
 *     countEl: HTMLElement      — 显示 "N / M" 计数的元素（可选）
 *     prevBtn: HTMLElement      — 上一个按钮（可选）
 *     nextBtn: HTMLElement      — 下一个按钮（可选）
 *     clearBtn: HTMLElement     — 清除按钮（可选）
 *     skipEmptyText: bool       — true 时跳过纯空白文本节点（tail 场景可优化性能），默认 false
 *     onAfterHighlight: fn(marks) — 每次重新高亮后回调（可选）
 *
 *   controller 方法：
 *     .search(term)             — 程序化触发搜索
 *     .clear()                  — 清除高亮
 *     .refresh()                — 在容器 DOM 变化后重新对新内容应用当前搜索词
 *     .destroy()                — 卸载事件监听
 *     .term                     — 当前搜索词（字符串，只读）
 *     .matchCount               — 当前匹配数（数字）
 *     .activeIndex              — 当前激活匹配索引（-1 表示无）
 *
 * 使用要求：
 *   - 页面需加载 search-hl.css（或自行提供 mark.search-hl 样式）
 *   - 容器内已有的 <mark class="search-hl"> 会被清除后重建；其他元素不受影响
 */
(function (global) {
  'use strict';

  function escapeRegExp(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  function createSearchHighlighter(opts) {
    var container = opts.container;
    var input = opts.input;
    var countEl = opts.countEl || null;
    var prevBtn = opts.prevBtn || null;
    var nextBtn = opts.nextBtn || null;
    var clearBtn = opts.clearBtn || null;
    var skipEmptyText = !!opts.skipEmptyText;
    var onAfterHighlight = typeof opts.onAfterHighlight === 'function' ? opts.onAfterHighlight : null;

    if (!container) throw new Error('createSearchHighlighter: container is required');
    if (!input) throw new Error('createSearchHighlighter: input is required');

    var doc = container.ownerDocument || document;
    var searchTerm = '';
    var searchMatches = [];
    var searchActiveIdx = -1;
    var searchDebounce = null;
    var destroyed = false;
    var ctrlKeyHandler = null;

    function clearHighlights() {
      var marks = container.querySelectorAll('mark.search-hl');
      for (var i = marks.length - 1; i >= 0; i--) {
        var m = marks[i];
        var p = m.parentNode;
        while (m.firstChild) p.insertBefore(m.firstChild, m);
        p.removeChild(m);
        p.normalize();
      }
      searchMatches = [];
      searchActiveIdx = -1;
      if (countEl) countEl.textContent = '';
    }

    function highlightTextNode(textNode, regex) {
      var text = textNode.nodeValue;
      if (!text) return [];
      var ms = [];
      var mm;
      regex.lastIndex = 0;
      while ((mm = regex.exec(text)) !== null) {
        if (mm[0].length === 0) { regex.lastIndex++; continue; }
        ms.push({ start: mm.index, length: mm[0].length });
      }
      if (!ms.length) return [];
      var frag = doc.createDocumentFragment();
      var pos = 0;
      var created = [];
      for (var mi = 0; mi < ms.length; mi++) {
        var match = ms[mi];
        if (match.start > pos) {
          frag.appendChild(doc.createTextNode(text.slice(pos, match.start)));
        }
        var mk = doc.createElement('mark');
        mk.className = 'search-hl';
        mk.textContent = text.slice(match.start, match.start + match.length);
        frag.appendChild(mk);
        created.push(mk);
        pos = match.start + match.length;
      }
      if (pos < text.length) {
        frag.appendChild(doc.createTextNode(text.slice(pos)));
      }
      textNode.parentNode.replaceChild(frag, textNode);
      return created;
    }

    function applyHighlight(term) {
      clearHighlights();
      if (!term) {
        if (onAfterHighlight) onAfterHighlight([]);
        return;
      }
      var regex = new RegExp(escapeRegExp(term), 'gi');
      var walker = doc.createTreeWalker(container, NodeFilter.SHOW_TEXT, {
        acceptNode: function (node) {
          if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
          if (skipEmptyText && !node.nodeValue.trim()) return NodeFilter.FILTER_REJECT;
          var p = node.parentNode;
          while (p && p !== container) {
            if (p.classList && p.classList.contains('search-hl')) return NodeFilter.FILTER_REJECT;
            p = p.parentNode;
          }
          return NodeFilter.FILTER_ACCEPT;
        }
      });
      var tnodes = [];
      var n;
      while ((n = walker.nextNode())) tnodes.push(n);
      var marks = [];
      for (var ti = 0; ti < tnodes.length; ti++) {
        var c = highlightTextNode(tnodes[ti], regex);
        for (var ci = 0; ci < c.length; ci++) marks.push(c[ci]);
      }
      searchMatches = marks;
      searchActiveIdx = marks.length > 0 ? 0 : -1;
      updateActiveMark();
      updateCount();
      scrollToActive();
      if (onAfterHighlight) onAfterHighlight(marks);
    }

    function updateActiveMark() {
      for (var i = 0; i < searchMatches.length; i++) {
        if (i === searchActiveIdx) {
          searchMatches[i].classList.add('search-hl-active');
        } else {
          searchMatches[i].classList.remove('search-hl-active');
        }
      }
    }

    function updateCount() {
      if (!countEl) return;
      if (!searchTerm) { countEl.textContent = ''; return; }
      if (searchMatches.length === 0) {
        countEl.textContent = '无匹配';
      } else {
        countEl.textContent = (searchActiveIdx + 1) + ' / ' + searchMatches.length;
      }
    }

    function scrollToActive() {
      if (searchActiveIdx < 0 || !searchMatches[searchActiveIdx]) return;
      searchMatches[searchActiveIdx].scrollIntoView({ block: 'center', behavior: 'smooth' });
    }

    function goNext() {
      if (searchMatches.length === 0) return;
      searchActiveIdx = (searchActiveIdx + 1) % searchMatches.length;
      updateActiveMark();
      updateCount();
      scrollToActive();
    }

    function goPrev() {
      if (searchMatches.length === 0) return;
      searchActiveIdx = (searchActiveIdx - 1 + searchMatches.length) % searchMatches.length;
      updateActiveMark();
      updateCount();
      scrollToActive();
    }

    function onInput() {
      clearTimeout(searchDebounce);
      searchDebounce = setTimeout(function () {
        searchTerm = input.value;
        applyHighlight(searchTerm);
      }, 200);
    }

    function onKeydown(ev) {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        if (ev.shiftKey) goPrev(); else goNext();
      } else if (ev.key === 'Escape') {
        ev.preventDefault();
        input.value = '';
        searchTerm = '';
        clearHighlights();
        input.blur();
      }
    }

    function onClearClick() {
      input.value = '';
      searchTerm = '';
      clearHighlights();
      input.focus();
    }

    function onDocKeydown(ev) {
      if ((ev.ctrlKey || ev.metaKey) && ev.key === 'f') {
        ev.preventDefault();
        input.focus();
        input.select();
      }
    }

    input.addEventListener('input', onInput);
    input.addEventListener('keydown', onKeydown);
    if (prevBtn) prevBtn.addEventListener('click', goPrev);
    if (nextBtn) nextBtn.addEventListener('click', goNext);
    if (clearBtn) clearBtn.addEventListener('click', onClearClick);

    ctrlKeyHandler = onDocKeydown;
    doc.addEventListener('keydown', ctrlKeyHandler);

    return {
      get term() { return searchTerm; },
      get matchCount() { return searchMatches.length; },
      get activeIndex() { return searchActiveIdx; },
      search: function (term) {
        input.value = term;
        searchTerm = term;
        applyHighlight(term);
      },
      clear: function () {
        input.value = '';
        searchTerm = '';
        clearHighlights();
      },
      refresh: function () {
        if (searchTerm) applyHighlight(searchTerm);
      },
      goNext: goNext,
      goPrev: goPrev,
      destroy: function () {
        if (destroyed) return;
        destroyed = true;
        clearTimeout(searchDebounce);
        input.removeEventListener('input', onInput);
        input.removeEventListener('keydown', onKeydown);
        if (prevBtn) prevBtn.removeEventListener('click', goPrev);
        if (nextBtn) nextBtn.removeEventListener('click', goNext);
        if (clearBtn) clearBtn.removeEventListener('click', onClearClick);
        if (ctrlKeyHandler) doc.removeEventListener('keydown', ctrlKeyHandler);
        clearHighlights();
      }
    };
  }

  global.Kairo = global.Kairo || {};
  global.Kairo.createSearchHighlighter = createSearchHighlighter;
})(typeof window !== 'undefined' ? window : this);
