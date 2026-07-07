/*
 * web/theme.js — 主题切换（v0.5 接入 #19，v0.9.0 新增武侠主题）
 *
 * 设计要点：
 *  - 暴露 window.Kairo.theme = { get, set, toggle, init }
 *  - 持久化：localStorage.kairo_theme（默认 'dark'）
 *  - 应用：document.documentElement.setAttribute('data-theme', name)
 *  - 在 index.html 的 <head> 里有一段 inline script 提前读 localStorage
 *    设置 data-theme，避免刷新时整页先闪一下暗色再切到亮色（FOUC）
 *  - 切换后 dispatch 自定义事件 'kairo:themechange'，按钮可监听并改 emoji
 *    （这样 theme.js 本身不耦合顶栏 DOM 结构，扩展友好）
 */
(function () {
  'use strict';
  const KEY = 'kairo_theme';
  // 5 种主题：深色 / 浅色 / 护眼绿 / 高对比 / 武侠古风
  const ALLOWED = ['dark', 'light', 'green', 'hc', 'xianxia'];

  function get() {
    try {
      const v = localStorage.getItem(KEY);
      if (ALLOWED.indexOf(v) !== -1) return v;
    } catch (_) {}
    return 'dark';
  }

  // 切换循环：dark → light → green → hc → xianxia → dark
  const NEXT = { dark: 'light', light: 'green', green: 'hc', hc: 'xianxia', xianxia: 'dark' };
  // 按钮图标（lucide key，见 web/icons.js）+ tooltip
  // 之前是 emoji，在 Win 7 上字体缺失会显示豆腐块，改内联 SVG 跨平台一致
  const THEME_ICON = { dark: 'moon', light: 'sun', green: 'leaf', hc: 'contrast', xianxia: 'sword' };
  // 按钮文字 label（中文系统字体，Win 7 / 老 Chrome 也稳）
  // v0.13 UI：单图标按钮很多人不知道是"切换主题"，加文字一眼可读
  const LABEL = { dark: '深色', light: '浅色', green: '护眼绿', hc: '高对比', xianxia: '武侠' };
  const TIP = {
    dark: '当前：深色主题。点击切换到浅色主题',
    light: '当前：浅色主题。点击切换到护眼绿主题',
    green: '当前：护眼绿主题。点击切换到高对比主题',
    hc: '当前：高对比主题。点击切换到武侠古风主题',
    xianxia: '当前：武侠古风主题。点击切换到深色主题'
  };

  // 按钮里的 SVG 尺寸（px）
  const ICON_SIZE = 16;

  function paintBtn(btn, name) {
    if (!btn) return;
    btn.setAttribute('data-theme', name);
    btn.setAttribute('aria-label', '切换主题，当前：' + (LABEL[name] || LABEL.dark));
    // icons.js 在 core.js 之后加载；理论上一定可用，但防御一下
    const ic = (window.Kairo && Kairo.icons && Kairo.icons.innerHTML)
      ? Kairo.icons.innerHTML(THEME_ICON[name] || THEME_ICON.dark, ICON_SIZE)
      : '';
    const label = LABEL[name] || LABEL.dark;
    btn.innerHTML =
      '<span class="theme-toggle-icon">' + ic + '</span>' +
      '<span class="theme-toggle-label">' + label + '</span>';
    btn.title = TIP[name] || TIP.dark;
  }

  function set(name) {
    if (ALLOWED.indexOf(name) === -1) name = 'dark';
    try { localStorage.setItem(KEY, name); } catch (_) {}
    document.documentElement.setAttribute('data-theme', name);
    try {
      window.dispatchEvent(new CustomEvent('kairo:themechange', { detail: { theme: name } }));
    } catch (_) {}
    paintBtn(document.getElementById('theme-toggle'), name);
    return name;
  }

  function toggle() {
    return set(NEXT[get()] || 'dark');
  }

  // init：设置初始主题 + 按钮 SVG
  function init() {
    const name = get();
    document.documentElement.setAttribute('data-theme', name);
    const btn = document.getElementById('theme-toggle');
    if (btn) {
      paintBtn(btn, name);
      if (!btn.onclick) {
        btn.addEventListener('click', () => Kairo.theme.toggle());
      }
    }
    return name;
  }

  // 列出所有可用主题（供前端下拉框用）
  function list() {
    return ALLOWED.slice();
  }

  window.Kairo = window.Kairo || {};
  window.Kairo.theme = { get, set, toggle, init, list };
})();