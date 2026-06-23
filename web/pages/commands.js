/* ===== web/pages/commands.js =====
 * 常用命令 — 占位页（"功能建设中"）
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el } = OTB.core;

  function renderCommands(view) {
    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '常用命令' }),
      el('div', { class: 'card-desc', text: 'grep / find / tail / WebSphere 排查模板' }),
      el('div', { class: 'text-dim', text: '本模块属于第二阶段规划。第一阶段重点是首页导航、配置读取、SSH 测试连接、列出日志文件、JSON/XML 格式化。' })
    ]));
  }

  OTB.pages.commands = renderCommands;
  OTB.state.routes.commands = renderCommands;
  OTB.state.routeNames.commands = '常用命令';
  OTB.state.routeSubs.commands = '功能建设中 — grep / find / tail / WebSphere 排查模板';
})();