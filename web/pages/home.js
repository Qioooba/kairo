/* ===== web/pages/home.js =====
 * 首页：主要功能卡片 + 小工具卡片
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};

  function makeCard(t, small) {
    const cls = 'tool-card' + (small ? ' tool-card-small' : '');
    // SVG 实际显示尺寸由 CSS .tool-card .icon svg 控制（28px / 24px），
    // icons.svg() 第二个参数这里只传占位，CSS 会覆盖。
    const iconNode = Kairo.icons.svg(t.icon, 24);
    return Kairo.core.el('div', {
      class: cls,
      tabindex: '0',
      role: 'button',
      'aria-label': t.name,
      onclick: () => { location.hash = '#/' + t.id; },
      onkeydown: (ev) => {
        if (ev.key === 'Enter' || ev.key === ' ') {
          ev.preventDefault();
          location.hash = '#/' + t.id;
        }
      }
    }, [
      Kairo.core.el('div', { class: 'row between' }, [
        // t.icon 现在是 lucide 图标 key（不是 emoji 字符）
        Kairo.core.el('div', { class: 'icon' }, iconNode),
        t.tagText ? Kairo.core.el('span', { class: 'tag ' + (t.tag || 'ready'), text: t.tagText }) : null
      ]),
      Kairo.core.el('div', { class: 'name', text: t.name }),
      Kairo.core.el('div', { class: 'desc', text: t.desc })
    ]);
  }

  function renderHome(view) {
    view.appendChild(Kairo.core.el('div', { class: 'mb-3' }, [
      Kairo.core.el('div', { class: 'section-title brand-text-title', text: 'Kairo · 天命契机' }),
      Kairo.core.el('div', { class: 'section-sub', text: '日志检索、报文格式化、常用运维辅助工具。' })
    ]));

    // icon 字段：lucide 图标 key（见 web/icons.js）。
    // 之前用 ASCII 字符 [L] {} 等是因为 Win 7 没有现代 emoji 字体，
    // 现在改内联 SVG 跨平台一致。
    const mainTools = [
      { id: 'websphere', name: '日志助手', desc: '多服务器日志并行搜索、上下文查看、实时 Tail、日志下载', icon: 'file-text', tag: 'ready', tagText: '已就绪' },
      { id: 'files', name: '文件下载', desc: '按 SSH 账号权限浏览任意目录，像 FTP 一样层层进入并下载', icon: 'file-down', tag: 'ready', tagText: 'v0.3' },
      { id: 'ssh', name: 'SSH 终端', desc: '浏览器里直接开交互式 shell，多 tab、复用老 SSH 兼容配置', icon: 'terminal', tag: 'ready', tagText: 'v0.10' },
      { id: 'http', name: 'HTTP 接口测试', desc: 'Postman 风格接口调试，Headers/Body/用例管理/响应高亮', icon: 'globe', tag: 'ready', tagText: 'v0.7' },
      { id: 'webservice', name: 'WebService 调试', desc: 'SOAP/WSDL 调试中心：导入 WSDL、生成报文、发送请求、Mock 服务端', icon: 'soap-envelope', tag: 'ready', tagText: 'v0.12' },
      { id: 'formatter', name: '报文格式化', desc: 'JSON / XML / YAML / URL-form 格式化、压缩、校验、互转', icon: 'braces', tag: 'ready', tagText: '已就绪' },
      { id: 'diagnostics', name: '环境自检', desc: '一键体检：本机 / 网络 / 配置 / 工具 / 每台 server 连通性', icon: 'shield-check', tag: 'ready', tagText: 'v0.4' },
      { id: 'config', name: '系统配置', desc: '在线编辑业务系统 / 服务器 / 日志目录 / 全局设置', icon: 'settings', tag: 'ready', tagText: 'v0.4' },
      { id: 'downloads', name: '下载历史', desc: '浏览 / 删除 / 重新下载 / 外部程序打开已下载文件', icon: 'history', tag: 'ready', tagText: '已就绪' }
    ];

    const grid = Kairo.core.el('div', { class: 'grid-4' });
    mainTools.forEach(t => grid.appendChild(makeCard(t, false)));
    view.appendChild(grid);

    const miniTools = [
      { id: 'timestamp', name: '时间戳转换', desc: '时间戳 ↔ 日期互转、时区计算', icon: 'clock' },
      { id: 'cron', name: 'Cron 解析', desc: 'Cron 表达式解析、下次执行时间预览', icon: 'calendar-clock' },
      { id: 'jsonpath', name: 'JSONPath 查询', desc: '在线 JSONPath 表达式求值', icon: 'workflow' },
      { id: 'compare', name: '代码比对', desc: '文本 / 文件 / 文件夹级 diff 差异对比，支持多种视图模式', icon: 'git-compare' },
      { id: 'commands', name: '常用命令速查', desc: 'Linux / Git / Docker / Oracle / MySQL / Redis / Nginx 等 · 实时搜索 + 一键复制', icon: 'square-terminal' },
      { id: 'reminders', name: '便笺提醒', desc: '一次性 / 周期 / Cron 表达式定时，本地落盘不依赖外网', icon: 'bell' },
      { id: 'about', name: '关于', desc: '版本信息、技术架构、数据统计', icon: 'info' },
    ];

    view.appendChild(Kairo.core.el('div', { class: 'section-title mt-4', style: 'font-size:14px;color:var(--text-dim);' }, '更多工具'));
    const miniGrid = Kairo.core.el('div', { class: 'grid-5' });
    miniTools.forEach(t => miniGrid.appendChild(makeCard(t, true)));
    view.appendChild(miniGrid);
  }

  Kairo.pages.home = renderHome;
  Kairo.state.routes.home = renderHome;
  Kairo.state.routeNames.home = '首页';
})();
