/*
 * web/theme.js — 暗 / 亮主题切换（v0.5 接入 #19）
 *
 * 设计要点：
 *  - 暴露 window.OTB.theme = { get, set, toggle, init }
 *  - 持久化：localStorage.otb_theme（默认 'dark'）
 *  - 应用：document.documentElement.setAttribute('data-theme', name)
 *  - 在 index.html 的 <head> 里有一段 inline script 提前读 localStorage
 *    设置 data-theme，避免刷新时整页先闪一下暗色再切到亮色（FOUC）
 *  - 切换后 dispatch 自定义事件 'otb:themechange'，按钮可监听并改 emoji
 *    （这样 theme.js 本身不耦合顶栏 DOM 结构，扩展友好）
 */
(function () {
  'use strict';
  const KEY = 'otb_theme';
  const ALLOWED = ['dark', 'light'];

  function get() {
    try {
      const v = localStorage.getItem(KEY);
      if (ALLOWED.indexOf(v) !== -1) return v;
    } catch (_) {}
    return 'dark';
  }

  function set(name) {
    if (ALLOWED.indexOf(name) === -1) name = 'dark';
    try { localStorage.setItem(KEY, name); } catch (_) {}
    document.documentElement.setAttribute('data-theme', name);
    // 通知按钮 / 其他监听者
    try {
      window.dispatchEvent(new CustomEvent('otb:themechange', { detail: { theme: name } }));
    } catch (_) {}
    // 兜底：如果没有按钮监听，直接更新 #theme-toggle 的 emoji
    const btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.setAttribute('data-theme', name);
      btn.textContent = name === 'dark' ? '🌙' : '☀️';
      btn.title = name === 'dark' ? '切换到亮色主题' : '切换到暗色主题';
    }
    return name;
  }

  function toggle() {
    return set(get() === 'dark' ? 'light' : 'dark');
  }

  // 初始化：用于 core.js 之后调用，保证按钮 emoji 与初始主题一致
  function init() {
    const name = get();
    document.documentElement.setAttribute('data-theme', name);
    const btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.setAttribute('data-theme', name);
      btn.textContent = name === 'dark' ? '🌙' : '☀️';
      btn.title = name === 'dark' ? '切换到亮色主题' : '切换到暗色主题';
      // 点击切换（如果 HTML 里 onclick 没绑，这里兜底）
      if (!btn.onclick) {
        btn.addEventListener('click', () => OTB.theme.toggle());
      }
    }
    return name;
  }

  window.OTB = window.OTB || {};
  window.OTB.theme = { get, set, toggle, init };
})();