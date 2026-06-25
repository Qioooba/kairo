/* ===== web/pages/home.js =====
 * 首页：主要功能卡片 + 小工具卡片
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};

  function makeCard(t, small) {
    const cls = 'tool-card' + (small ? ' tool-card-small' : '');
    return OTB.core.el('div', { class: cls, onclick: () => { location.hash = '#/' + t.id; } }, [
      OTB.core.el('div', { class: 'row between' }, [
        OTB.core.el('div', { class: 'icon', text: t.icon }),
        t.tagText ? OTB.core.el('span', { class: 'tag ' + (t.tag || 'ready'), text: t.tagText }) : null
      ]),
      OTB.core.el('div', { class: 'name', text: t.name }),
      OTB.core.el('div', { class: 'desc', text: t.desc })
    ]);
  }

  function renderHome(view) {
    view.appendChild(OTB.core.el('div', { class: 'mb-3' }, [
      OTB.core.el('div', { class: 'section-title', text: '内网运维工具箱' }),
      OTB.core.el('div', { class: 'section-sub', text: 'WebSphere 日志检索、报文格式化、常用运维辅助工具。' })
    ]));

    const mainTools = [
      { id: 'websphere', name: 'WebSphere 日志助手', desc: '多服务器日志并行搜索、上下文查看、实时 Tail、日志下载', icon: '📜', tag: 'ready', tagText: '已就绪' },
      { id: 'files', name: '文件下载', desc: '按 SSH 账号权限浏览任意目录，像 FTP 一样层层进入并下载', icon: '📁', tag: 'ready', tagText: 'v0.3' },
      { id: 'formatter', name: '报文格式化', desc: 'JSON / XML / YAML / URL-form 格式化、压缩、校验、互转', icon: '⌗', tag: 'ready', tagText: '已就绪' },
      { id: 'http', name: 'HTTP 接口测试', desc: 'Postman 风格接口调试，Headers/Body/用例管理/响应高亮', icon: '🌐', tag: 'ready', tagText: 'v0.7' },
      { id: 'commands', name: '常用命令速查', desc: 'Linux / Git / Docker / Oracle / MySQL / Redis / Nginx 等 · 实时搜索 + 一键复制', icon: '$_', tag: 'ready', tagText: 'v0.7' },
      { id: 'diagnostics', name: '环境自检', desc: '一键体检：本机 / 网络 / 配置 / 工具 / 每台 server 连通性', icon: '🩺', tag: 'ready', tagText: 'v0.4' },
      { id: 'downloads', name: '下载历史', desc: '浏览 / 删除 / 重新下载 / 外部程序打开已下载文件', icon: '⤓', tag: 'ready', tagText: '已就绪' },
      { id: 'history', name: '操作历史', desc: '本地审计日志的最近记录 + CSV / JSON 过滤导出', icon: '⏱', tag: 'ready', tagText: '已就绪' }
    ];

    const grid = OTB.core.el('div', { class: 'grid-4' });
    mainTools.forEach(t => grid.appendChild(makeCard(t, false)));
    view.appendChild(grid);

    const miniTools = [
      { id: 'config', name: '系统配置', desc: '在线编辑业务系统 / 服务器 / 日志目录配置', icon: '⚙' },
      { id: 'timestamp', name: '时间戳转换', desc: '时间戳 ↔ 日期互转、时区计算', icon: '🕐' },
      { id: 'cron', name: 'Cron 解析', desc: 'Cron 表达式解析、下次执行时间预览', icon: '⏲' },
      { id: 'jsonpath', name: 'JSONPath 查询', desc: '在线 JSONPath 表达式求值', icon: '🔍' },
      { id: 'compare', name: '文本比对', desc: '两段文本的行级 diff 差异对比', icon: '🔀' },
    ];

    view.appendChild(OTB.core.el('div', { class: 'section-title mt-4', style: 'font-size:14px;color:var(--text-dim);' }, '更多工具'));
    const miniGrid = OTB.core.el('div', { class: 'grid-5' });
    miniTools.forEach(t => miniGrid.appendChild(makeCard(t, true)));
    view.appendChild(miniGrid);
  }

  OTB.pages.home = renderHome;
  OTB.state.routes.home = renderHome;
  OTB.state.routeNames.home = '首页';
})();
