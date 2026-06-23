/* ===== web/pages/home.js =====
 * 首页：8 张工具卡片
 *
 * 行为：从 app.js 行 191-220 抽出。纯渲染，无副作用。
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};

  function renderHome(view) {
    view.appendChild(OTB.core.el('div', { class: 'mb-3' }, [
      OTB.core.el('div', { class: 'section-title', text: '内网运维工具箱' }),
      OTB.core.el('div', { class: 'section-sub', text: 'WebSphere 日志检索、报文格式化、常用运维辅助工具。' })
    ]));

    const tools = [
      { id: 'websphere', name: 'WebSphere 日志助手', desc: '多服务器日志并行搜索、上下文查看、日志下载', icon: '📜', tag: 'ready', tagText: '已就绪' },
      { id: 'files', name: '文件下载', desc: '按 SSH 账号权限浏览任意目录，像 FTP 一样层层进入并下载', icon: '📁', tag: 'ready', tagText: 'v0.3 新增' },
      { id: 'formatter', name: '报文格式化', desc: 'JSON / XML 格式化、压缩、校验', icon: '⌗', tag: 'ready', tagText: '已就绪' },
      { id: 'commands', name: '常用命令', desc: 'grep / find / tail / WebSphere 排查模板', icon: '$_', tag: 'placeholder', tagText: '规划中' },
      { id: 'diagnostics', name: '环境自检', desc: '一键体检：本机 / 网络 / 配置 / 工具 / 每台 server 连通性', icon: '🩺', tag: 'ready', tagText: 'v0.4 新增' },
      { id: 'config', name: '系统配置', desc: '在线编辑业务系统/服务器/日志目录,改完点保存即生效', icon: '⚙', tag: 'ready', tagText: '可视化' },
      { id: 'downloads', name: '下载历史', desc: '浏览/删除/重新下载 downloads/ 里所有已下载文件', icon: '⤓', tag: 'ready', tagText: '已就绪' },
      { id: 'history', name: '操作历史', desc: '本地审计日志的最近记录 + CSV 导出', icon: '⏱', tag: 'ready', tagText: '已就绪' }
    ];
    const grid = OTB.core.el('div', { class: 'grid-4' });
    tools.forEach(t => {
      const card = OTB.core.el('div', { class: 'tool-card', onclick: () => { location.hash = '#/' + t.id; } }, [
        OTB.core.el('div', { class: 'row between' }, [
          OTB.core.el('div', { class: 'icon', text: t.icon }),
          OTB.core.el('span', { class: 'tag ' + t.tag, text: t.tagText })
        ]),
        OTB.core.el('div', { class: 'name', text: t.name }),
        OTB.core.el('div', { class: 'desc', text: t.desc })
      ]);
      grid.appendChild(card);
    });
    view.appendChild(grid);
  }

  OTB.pages.home = renderHome;
  OTB.state.routes.home = renderHome;
  OTB.state.routeNames.home = '首页';
})();