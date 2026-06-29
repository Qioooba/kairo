/*
 * web/theme.js — 暗 / 亮主题切换（v0.5 接入 #19）
 *
 * 设计要点：
 *  - 暴露 window.DTB.theme = { get, set, toggle, init }
 *  - 持久化：localStorage.dtb_theme（默认 'dark'）
 *  - 应用：document.documentElement.setAttribute('data-theme', name)
 *  - 在 index.html 的 <head> 里有一段 inline script 提前读 localStorage
 *    设置 data-theme，避免刷新时整页先闪一下暗色再切到亮色（FOUC）
 *  - 切换后 dispatch 自定义事件 'dtb:themechange'，按钮可监听并改 emoji
 *    （这样 theme.js 本身不耦合顶栏 DOM 结构，扩展友好）
 */
(function () {
  'use strict';
  const KEY = 'dtb_theme';
  // v0.5-G #19：4 种主题（深色 / 浅色 / 护眼绿 / 高对比）
  const ALLOWED = ['dark', 'light', 'green', 'hc'];

  function get() {
    try {
      const v = localStorage.getItem(KEY);
      if (ALLOWED.indexOf(v) !== -1) return v;
    } catch (_) {}
    return 'dark';
  }

  // 切换循环：dark → light → green → hc → dark
  const NEXT = { dark: 'light', light: 'green', green: 'hc', hc: 'dark' };
  // 按钮 emoji / tooltip
  const EMOJI = { dark: '🌙', light: '☀️', green: '🌿', hc: '🔆' };
  const TIP = {
    dark: '切换到浅色主题',
    light: '切换到护眼绿主题',
    green: '切换到高对比主题',
    hc: '切换到深色主题'
  };

  function set(name) {
    if (ALLOWED.indexOf(name) === -1) name = 'dark';
    try { localStorage.setItem(KEY, name); } catch (_) {}
    document.documentElement.setAttribute('data-theme', name);
    try {
      window.dispatchEvent(new CustomEvent('dtb:themechange', { detail: { theme: name } }));
    } catch (_) {}
    const btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.setAttribute('data-theme', name);
      btn.textContent = EMOJI[name] || EMOJI.dark;
      btn.title = TIP[name] || TIP.dark;
    }
    return name;
  }

  function toggle() {
    return set(NEXT[get()] || 'dark');
  }

  // init：设置初始主题 + 按钮 emoji
  function init() {
    const name = get();
    document.documentElement.setAttribute('data-theme', name);
    const btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.setAttribute('data-theme', name);
      btn.textContent = EMOJI[name] || EMOJI.dark;
      btn.title = TIP[name] || TIP.dark;
      if (!btn.onclick) {
        btn.addEventListener('click', () => DTB.theme.toggle());
      }
    }
    return name;
  }

  // 列出所有可用主题（供前端下拉框用）
  function list() {
    return ALLOWED.slice();
  }

  window.DTB = window.DTB || {};
  window.DTB.theme = { get, set, toggle, init, list };
})();