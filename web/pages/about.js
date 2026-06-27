/* ===== web/pages/about.js =====
 * 关于页面 / 版本历史
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el } = OTB.core;
  const { api } = OTB.api || {};

  // 兜底版本号；后端 /api/config 读取失败时使用。
  const VERSION = 'v0.8';

  // FE-006：异步从后端 /api/config 读取真实版本号（构建时 ldflags 注入），
  // 失败回退到硬编码 VERSION。返回 null 表示取不到。
  async function fetchVersion() {
    try {
      if (typeof api !== 'function') return null;
      const cfg = await api('GET', '/api/config');
      if (cfg && cfg.version) return cfg.version;
    } catch (e) { /* ignore，回退硬编码 */ }
    return null;
  }

  const changelog = [
    {
      version: 'v0.8',
      date: '2026-06-26',
      features: [
        '下载管理：完善下载历史、外部打开器配置、下载目录选择',
        '工具集新增：代码比对（文件/文件夹对比）、JSONPath查询',
        '全功能完善：主题优化、响应式布局、交互细节打磨',
        '实时跟踪优化：新窗口独立tail，不影响主页面操作'
      ]
    },
    {
      version: 'v0.7',
      date: '2026-06-20',
      features: [
        '日志助手重构：可折叠目标区、多对多目录勾选、搜索关键词历史',
        'HTTP测试增强：请求历史、用例管理、请求/响应格式化',
        '代码比对功能：支持文件和文件夹差异对比（基于diff2html）',
        '时间戳/Cron解析等小工具上线'
      ]
    },
    {
      version: 'v0.6',
      date: '2026-06-10',
      features: [
        'FTP文件下载：支持多选批量下载、打包zip、进度显示',
        '外部打开器：配置本地编辑器快速打开下载文件',
        '凭据存储：OS钥匙串集成（Windows Credential Manager / macOS Keychain）',
        'Tab切换重构：文件/搜索/实时跟踪三大功能区'
      ]
    },
    {
      version: 'v0.5',
      date: '2026-05-28',
      features: [
        '主题切换：深色/浅色/护眼绿/高对比四套主题',
        'WebSocket实时tail：SSE流式输出、关键字高亮',
        '多服务器并行操作：同时勾选多台服务器搜索/下载',
        '搜索结果分组展示：按服务器/目录分组，上下文预览'
      ]
    },
    {
      version: 'v0.4',
      date: '2026-05-15',
      features: [
        '配置管理：可视化编辑业务系统/服务器/日志目录',
        '服务器管理：增删改查、连接测试、状态显示',
        '系统配置：端口、下载目录、搜索参数等全局设置',
        '配置导入导出：yaml格式备份与恢复'
      ]
    },
    {
      version: 'v0.3',
      date: '2026-05-01',
      features: [
        '基础文件下载：SSH连接远程服务器、浏览日志目录',
        'SSH连接：密码认证、多服务器切换',
        '日志列表：显示文件大小、修改时间、操作按钮'
      ]
    },
    {
      version: 'v0.2',
      date: '2026-04-20',
      features: [
        '报文格式化：JSON/XML/YAML/URL-encoded格式化与压缩',
        'JSON/XML工具：语法校验、压缩、格式转换',
        '纯前端处理：敏感数据不上传服务器'
      ]
    },
    {
      version: 'v0.1',
      date: '2026-04-01',
      features: [
        '初始版本：Go后端+原生JS前端架构',
        '单二进制部署，无外部依赖',
        '基础HTTP服务与SSH客户端能力'
      ]
    }
  ];

  function renderAbout(view) {
    // FE-006：版本徽章先用硬编码 VERSION 渲染（避免空缺），异步拿到后端版本后回填。
    const versionBadge = el('span', {
      class: 'tier-badge',
      style: 'display:inline-block; font-size:14px; padding:4px 16px; border-radius:20px; background:var(--primary); color:#fff;',
      text: VERSION
    });
    fetchVersion().then(v => { if (v) versionBadge.textContent = v; });

    const headerCard = el('div', { class: 'card', style: 'text-align:center; padding:32px 24px;' }, [
      el('div', { style: 'font-size:48px; margin-bottom:12px;' }, [document.createTextNode('⚙')]),
      el('h1', { style: 'margin:0 0 8px 0; font-size:28px;', text: '内网运维工具箱' }),
      el('div', { class: 'text-dim', style: 'font-size:15px; margin-bottom:16px;', text: 'OpsToolbox · 内网运维效率平台' }),
      versionBadge
    ]);

    const historyTitle = el('h3', { style: 'margin:24px 0 12px 0;', text: '版本历史' });

    const historyWrap = el('div');
    changelog.forEach((v, idx) => {
      const isLatest = idx === 0;
      const card = el('div', { class: 'card', style: 'margin-bottom:12px; border-left:4px solid ' + (isLatest ? 'var(--primary)' : 'var(--line)') + ';' });
      const head = el('div', { style: 'display:flex; justify-content:space-between; align-items:center; margin-bottom:8px;' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px;' }, [
          el('span', {
            style: 'font-size:18px; font-weight:600; color:' + (isLatest ? 'var(--primary)' : 'var(--text)') + ';',
            text: v.version
          }),
          isLatest ? el('span', {
            class: 'tier-badge',
            style: 'background:var(--primary); color:#fff; font-size:11px;',
            text: '最新'
          }) : null
        ]),
        el('span', { class: 'text-dim', style: 'font-size:13px;', text: v.date })
      ]);
      const ul = el('ul', { style: 'margin:0; padding-left:20px; color:var(--text);' });
      v.features.forEach(f => {
        ul.appendChild(el('li', { style: 'margin-bottom:4px; line-height:1.6;', text: f }));
      });
      card.appendChild(head);
      card.appendChild(ul);
      historyWrap.appendChild(card);
    });

    const footer = el('div', { class: 'text-dim', style: 'text-align:center; margin-top:24px; padding:16px; font-size:13px; border-top:1px solid var(--line);' }, [
      el('div', { text: '© 2026 OpsToolbox' }),
      el('div', { style: 'margin-top:4px;', text: '技术栈：Go + 原生JavaScript' })
    ]);

    view.appendChild(headerCard);
    view.appendChild(historyTitle);
    view.appendChild(historyWrap);
    view.appendChild(footer);
  }

  OTB.pages.about = renderAbout;
  OTB.state.routes.about = renderAbout;
  OTB.state.routeNames.about = '关于';
})();
