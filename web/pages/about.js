/* ===== web/pages/about.js =====
 * 关于页面 · 产品白皮书
 *
 * 设计目标：
 *   - 把"关于"从单页简介升级为一份**带交互的产品技术白皮书**
 *   - 顶部 sticky 锚点导航 + 20 个版本卡片 (accordion 折叠) + 14 个数据区块
 *   - 内容 100% 由 commit log / 源码 / README 提取, 不注水
 *   - 几万字正文 + 折叠默认收起, 首屏不卡
 */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el } = Kairo.core;
  const { api } = Kairo.api || {};

  function currentVersion() {
    try {
      const boot = Kairo.state && Kairo.state.bootInfo;
      if (boot && boot.version) return boot.version;
    } catch (e) { /* ignore */ }
    return '';
  }

  // =====================================================================
  // SVG icon 字典 — 13 个 section icon (Win7 兼容, 不依赖 emoji 字体)
  // - 单色 currentColor, 跟 CSS 变量自动适配 4 套主题 + xianxia
  // - viewBox 统一 24×24, 渲染时按需 size
  // - path data 单源, sticky 锚点 + section title 两处复用
  // =====================================================================
  const ICONS = {
    overview:     'M12 2a10 10 0 100 20 10 10 0 000-20zm0 3a7 7 0 110 14 7 7 0 010-14zm0 3a4 4 0 100 8 4 4 0 000-8zm0 2a2 2 0 110 4 2 2 0 010-4z',         // 靶心 — 产品概览
    principles:   'M12 2L8 12l4 10 4-10zM12 12L9 9M12 12l3-3M12 12l-3 3M12 12l3 3',                                                                       // 罗盘 — 设计哲学
    architecture: 'M2 4h20v4H2zM2 11h20v4H2zM2 18h20v3H2z',                                                                                                 // 堆叠层 — 四层架构
    stack:        'M12 2l1.5 3 3.5-1-1 3.5 3 1.5-3 1.5 1 3.5-3.5-1L12 16l-1.5 3-3.5 1 1-3.5L5 15l3-1.5L7 10l-3-1.5 3-1.5L6 3.5 9.5 4.5z',              // 八角齿轮 — 技术栈
    security:     'M12 2L3 6v6c0 5 4 9 9 10 5-1 9-5 9-10V6z',                                                                                               // 盾牌 — 安全白皮书
    compat:       'M8 3v5M16 3v5M5 8h14v4a5 5 0 01-5 5h-4a5 5 0 01-5-5zM10 17v5M14 17v5',                                                                  // 插头 — SSH 兼容
    quality:      'M9 2v6L5 18a3 3 0 003 4h8a3 3 0 003-4l-4-10V2zM8 14h8',                                                                                  // 烧瓶 — 质量保障
    modules:      'M3 3h6v3a2 2 0 004 0V3h6v6h-3a2 2 0 000 4h3v6h-6v-3a2 2 0 00-4 0v3H3v-6h3a2 2 0 000-4H3z',                                          // 拼图 — 功能模块
    comparison:   'M6 4L2 12l4 8M18 4l4 8-4 8M14 4l-4 16',                                                                                                  // VS 双柱 — 横向对比
    bugs:         'M12 2L2 22h20zM12 9v6M12 18v1',                                                                                                          // 警告三角 — 故障复盘
    history:      'M12 2a10 10 0 100 20 10 10 0 000-20zM12 6v6l4 2',                                                                                         // 时钟 — 版本演进史
    faq:          'M12 2a10 10 0 100 20 10 10 0 000-20zM9 9a3 3 0 016 0c0 2-3 3-3 5M12 17v1',                                                                // 圆 + 问号 — FAQ
    roadmap:      'M5 3v18M5 4h11l-2 4 2 4H5',                                                                                                              // 旗子 — 路线图
    aiEngine:     'M13 2L3 14h9l-1 8 10-12h-9l1-8z',                                                                                                        // 闪电 — AI 研发引擎

    // ----------------------------------------------------------------
    // 设计哲学 8 原则 (renderPrinciplesSection 卡片)
    // ----------------------------------------------------------------
    safeClosed:   'M12 2L4 5v6c0 5 4 9 8 10 4-1 8-5 8-10V5zM12 11v3M12 17v.5',                                                                             // 盾牌 + 中心点 — 安全优先
    controlled:   'M12 2a5 5 0 00-5 5v3H4v12h16V10h-3V7a5 5 0 00-5-5zM9 7a3 3 0 016 0v3H9z',                                                              // 锁 — 受控优于开放
    context:      'M6 2h12v5l-3 5 3 5v5H6v-5l3-5-3-5zM9 2v5h6V2M9 17v5h6v-5',                                                                            // 沙漏 — 上下文优先
    simple:       'M12 3a9 9 0 100 18 9 9 0 000-18zM12 7v5M9 9l3 3',                                                                                       // 圆 + 极简光点 — 极简优于复杂
    testable:     'M9 2v9l-4 8a3 3 0 003 4h8a3 3 0 003-4l-4-8V2zM8 14h8',                                                                                  // 烧瓶 — 可测优于能跑 (复用 quality)
    zeroPlain:    'M14 4a4 4 0 014 4 4 4 0 01-4 4h-1l-7 7H2v-4l7-7V7a4 4 0 014-4z',                                                                      // 钥匙 — 凭据零落盘
    persist:      'M5 3h14v18H5zM7 3v6h10V3M9 14h6v6H9z',                                                                                                 // 保存/磁盘 — 写后即持久
    operator:     'M3 4h18v16H3zM3 8h18M6 12l3 2-3 2M11 16h7',                                                                                           // 终端 — 工程师视角

    // ----------------------------------------------------------------
    // 功能模块 20 卡片 (renderModulesSection)
    // ----------------------------------------------------------------
    websphere:    'M4 4h16v2H4zM4 8h11v2H4zM4 12h16v2H4zM4 16h11v2H4zM4 20h16v2H4z',                                                                     // 日志列表 — WebSphere 日志助手
    sshTerminal:  'M3 4h18v16H3zM3 8h18M6 12l3 2-3 2M11 16h7',                                                                                          // 终端窗口 — SSH 终端 + SFTP
    database:     'M4 6c0-1.66 3.58-3 8-3s8 1.34 8 3v12c0 1.66-3.58 3-8 3s-8-1.34-8-3V6zm0 4c0 1.66 3.58 3 8 3s8-1.34 8-3M4 14c0 1.66 3.58 3 8 3s8-1.34 8-3', // 数据库圆柱 — 数据库工作台
    downloader:   'M12 3v12M7 10l5 5 5-5M5 19h14v2H5z',                                                                                                  // 下箭头 — 文件下载器
    compareIc:    'M3 6l4 6-4 6M21 6l-4 6 4 6M14 4l-4 16',                                                                                                // diff 双柱 — 代码比对系统
    waspack:      'M3 7l9-4 9 4v10l-9 4-9-4zM3 7l9 4 9-4M12 11v10',                                                                                          // 封箱 — WAS 投产打包
    http:         'M12 2a10 10 0 100 20 10 10 0 000-20zM2 12h20M12 2a15 15 0 010 20M12 2a15 15 0 000 20',                                                // 经纬网 — HTTP 测试台
    formatter:    'M9 4a3 3 0 00-3 3v3a3 3 0 01-3 3v1a3 3 0 013 3v3a3 3 0 003 3M15 4a3 3 0 013 3v3a3 3 0 003 3v1a3 3 0 00-3 3v3a3 3 0 01-3 3',         // 大括号 — 格式化器
    wscodegen:    'M5 3h10l4 4v14H5zM15 3v4h4M9 13l2 2-2 2M15 17h-2',                                                                                        // 源码文件 — WS 代码生成
    notes:        'M5 3h11l4 4v14H5zM16 3v4h4M8 10h8M8 14h6',                                                                                                // 便笺纸 — 桌面便笺
    tasks:        'M12 2a10 10 0 100 20 10 10 0 000-20zm0 4v6l4 2M10 2h4',                                                                                  // 秒表 — 定时任务
    pet:          'M12 3a9 9 0 00-9 9c0 3.5 2 6.5 5 7.8V21l3-1 3 1v-1.2c3-1.3 5-4.3 5-7.8a9 9 0 00-9-9zm-3 7a1.5 1.5 0 110-3 1.5 1.5 0 010 3zm6 0a1.5 1.5 0 110-3 1.5 1.5 0 010 3z', // 像素宠物 — 桌面宠物
    diagnostics:  'M3 12h4l2-6 4 12 2-6h6',                                                                                                              // 心电脉冲 — 诊断中心
    config:       'M3 4h18v4H3zM3 10h12v4H3zM3 16h18v4H3z',                                                                                              // 堆叠方块 — 配置中心
    commands:     'M3 4h18v16H3zM7 9l3 3-3 3M13 15h6',                                                                                                  // 命令行 — 常用命令
    reminderIc:   'M12 3a6 6 0 016 6v3l2 4H4l2-4V9a6 6 0 016-6zM10 19a2 2 0 004 0M12 1v2',                                                                // 闹钟 + 摆锤 — 定时提醒
    browserIc:    'M12 2a10 10 0 100 20 10 10 0 000-20zM2 12h20M12 2a15 15 0 010 20M12 2a15 15 0 000 20',                                                  // 经纬网 — 浏览器自动打开
    editIc:       'M14 3v4h4l-4-4zM5 3h9l5 5v13H5zM8 11h8M8 14h8M8 17h5',                                                                                 // 折角文件 + 文字行 — 在线编辑
    coffee:       'M3 8h14v6a4 4 0 01-4 4H7a4 4 0 01-4-4V8zM17 10h2a2 2 0 010 4h-2M7 4c0-1 1-2 2-2M10 2c0-1 1-2 2-2M14 2c0-1 1-2 2-2',                 // 咖啡杯 + 杯耳 + 三道蒸汽 — 投喂作者

    // ----------------------------------------------------------------
    // 小标题装饰图标 (Win7 兼容, 替换 emoji)
    // ----------------------------------------------------------------
    smTarget:     'M12 2a10 10 0 100 20 10 10 0 000-20zm0 3a7 7 0 110 14 7 7 0 010-14zm0 3a4 4 0 100 8 4 4 0 000-8zm0 2a2 2 0 110 4 2 2 0 010-4z',         // 靶心 — 典型使用场景
    smRepeat:     'M17 1l4 4-4 4M3 11V9a6 6 0 016-6h12M7 23l-4-4 4-4M21 13v2a6 6 0 01-6 6H3',                                                        // 循环箭头 — 数据流向
    smGear:       'M14.7 4.1a2.5 2.5 0 0 1-5 0 2.5 2.5 0 0 0-3.5 2 2.5 2.5 0 0 1-2.5 4.3 2.5 2.5 0 0 0 0 4.1 2.5 2.5 0 0 1 2.5 4.3 2.5 2.5 0 0 0 3.5 2 2.5 2.5 0 0 1 5 0 2.5 2.5 0 0 0 3.5-2 2.5 2.5 0 0 1 2.5-4.3 2.5 2.5 0 0 0 0-4.1 2.5 2.5 0 0 1-2.5-4.3 2.5 2.5 0 0 0-3.5-2zM12 12a3.5 3.5 0 100 7 3.5 3.5 0 000-7z',   // 齿轮 — 后端技术栈
    smPalette:    'M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.6 0 1-.4 1-1 0-.3-.1-.5-.2-.7-.2-.3-.3-.6-.3-1 0-1.1.9-2 2-2h1.7c2.9 0 5.3-2.4 5.3-5.3C22 6 17.5 2 12 2zM6.5 10a1.5 1.5 0 110-3 1.5 1.5 0 010 3zM10 6.5a1.5 1.5 0 113 0 1.5 1.5 0 01-3 0zm5 1.5a1.5 1.5 0 110-3 1.5 1.5 0 010 3z',    // 调色板 — 前端
    smTools:      'M5 3a2 2 0 00-2 2v2h4V5h2V3H5zm12 0h-4v2h2v2h4V5a2 2 0 00-2-2zM3 9v8a3 3 0 003 3h2v-8H3zm16 0v8h-2v-2h-4v-2h4v-2h-4V9h6z',                                                     // 扳手螺丝刀 — 工程化实践
    smShield:     'M12 2L3 6v6c0 5 4 9 9 10 5-1 9-5 9-10V6z',                                                                                               // 盾牌 — 安全模型 banner
    smPackage:    'M3 7l9-4 9 4v10l-9 4-9-4zM3 7l9 4 9-4M12 11v10',                                                                                          // 包裹 — 后端包
    smPin:        'M12 2L6 8v2l2 2v8l4-2 4 2v-8l2-2V8z',                                                                                                   // 图钉 — 前端页 / 关键 commit
    smPlug:       'M8 3v5M16 3v5M5 8h14v4a5 5 0 01-5 5h-4a5 5 0 01-5-5zM10 17v5M14 17v5',                                                                  // 插头 — API
    smSparkles:   'M12 2 L13.5 9 L20 10.5 L13.5 12 L12 19 L10.5 12 L4 10.5 L10.5 9 Z',                                                                     // 闪耀星 — 核心能力/特性
    smBook:       'M4 3h10a4 4 0 014 4v13H8a4 4 0 01-4-4V3zm0 14a4 4 0 014-4h10M8 7h6',                                                                   // 打开的书 — 阅读提示
    smCompass:    'M12 2L8 12l4 10 4-10zM12 12L9 9M12 12l3-3M12 12l-3 3M12 12l3 3',                                                                       // 罗盘 — 本版设计哲学
    smBuilding:   'M3 21V7l9-4 9 4v14zM6 21v-6h4v6zM14 21v-6h4v6zM9 9h2v2H9zM13 9h2v2h-2z',                                                               // 建筑 — 架构调整
    smRecycle:    'M7 19l-3-3h2.5a5 5 0 019.5 1M17 5l3 3h-2.5a5 5 0 00-9.5 1M12 22l3-5-3-1-3-1 3-5z',                                                  // 回收箭头 — 退役
    smWrench:     'M14.7 4.1a5 5 0 00-6.4 6.4L3 15.9 8.1 21l5.4-5.3a5 5 0 006.4-6.4l-2.6 2.6-2.6-2.6 2.6-2.6z',                                         // 扳手 — 修复与优化
    smZap:        'M13 2L4 14h7l-1 8 9-12h-7z',                                                                                                           // 闪电 — 性能数据
    smWarn:       'M12 2L2 22h20zM12 9v6M12 18v1',                                                                                                        // 警告三角 — Breaking Changes
    smClipboard:  'M9 2h6a1 1 0 011 1v1h3a1 1 0 011 1v16a1 1 0 01-1 1H5a1 1 0 01-1-1V5a1 1 0 011-1h3V3a1 1 0 011-1zM9 5h6M7 9h10M7 13h10M7 17h6',           // 剪贴板 — 升级注意
    smAlert:      'M12 2a10 10 0 100 20 10 10 0 000-20zM12 8v5M12 16v1',                                                                                   // 圆圈叹号 — 症状
    smSearch:     'M11 3a8 8 0 105.3 14l4.4 4.4 1.4-1.4-4.4-4.4A8 8 0 0011 3zm0 2a6 6 0 110 12 6 6 0 010-12z',                                             // 放大镜 — 根因
    smBulb:       'M9 18h6M10 21h4M12 2a7 7 0 00-4 12.7V17a2 2 0 002 2h4a2 2 0 002-2v-2.3A7 7 0 0012 2z',                                                 // 灯泡 — 教训 / 调研中
    smFolder:     'M2 5a2 2 0 012-2h5l2 2h9a2 2 0 012 2v11a2 2 0 01-2 2H4a2 2 0 01-2-2V5z',                                                                 // 文件夹
    smFile:       'M6 2h6l4 4v14a2 2 0 01-2 2H6a2 2 0 01-2-2V4a2 2 0 012-2zm6 0v4h4',                                                                      // 文件
    smChevronDown: 'M6 9l6 6 6-6',                                                                                                                          // 向下箭头
  };

  // =====================================================================
  // 性能工具 (v0.13.x hotfix): 懒渲染 + 批量挂载
  // ---------------------------------------------------------------------
  // 设计: 13 个 section 不再一次性塞 DOM, 改用 IntersectionObserver
  //       进入视口前显示占位 (content-visibility: auto skip layout/paint)
  // 兼容: 老浏览器 (无 IO) 自动降级到 "全量直接渲染", 行为等同旧版
  // 安全: 3 层兜底
  //   1. try/catch 包住 factory 内部异常, 失败不污染页面
  //   2. 200ms 后仍未触发 → 强制全量渲染, 防止极端 IO 异常
  //   3. disconnect 避免重复挂载
  // =====================================================================
  const HAS_IO = typeof IntersectionObserver !== 'undefined';
  const HAS_CV = (typeof CSS !== 'undefined') && CSS.supports && CSS.supports('content-visibility', 'auto');

  // 立即渲染的 section (首屏必须看到的内容, 不懒渲染)
  // 占位高度是经验估算, 真实渲染后会被清掉
  const LAZY_SECTIONS = [
    { name: 'aiEngine',     fn: renderAiEngineSection,     min: 450 },
    { name: 'principles',   fn: renderPrinciplesSection,   min: 400 },
    { name: 'architecture', fn: renderArchitectureSection, min: 500 },
    { name: 'stack',        fn: renderStackSection,        min: 600 },
    { name: 'security',     fn: renderSecuritySection,     min: 700 },
    { name: 'compat',       fn: renderCompatSection,       min: 400 },
    { name: 'quality',      fn: renderQualitySection,      min: 500 },
    { name: 'modules',      fn: renderModulesSection,      min: 800 },
    { name: 'comparison',   fn: renderComparisonSection,   min: 500 },
    { name: 'bugs',         fn: renderBugStoriesSection,   min: 700 },
    { name: 'history',      fn: renderHistorySection,      min: 900 },  // changelog 19 版本卡
    { name: 'faq',          fn: renderFaqSection,          min: 600 },
    { name: 'roadmap',      fn: renderRoadmapSection,      min: 500 }
  ];

  // factory(): 同步返回真实 DOM 节点
  // opts.minHeight: 占位 div 高度 (px)
  // 失败兜底: 老浏览器 / IO 异常 / factory 抛错
  function withLazyMount(factory, opts) {
    opts = opts || {};
    const minH = opts.minHeight || 320;
    const placeholder = document.createElement('div');
    placeholder.className = 'about-lazy-section';
    placeholder.style.minHeight = minH + 'px';
    if (HAS_CV) {
      // content-visibility: auto 让浏览器自动 skip 屏外 section 的 layout/paint
      // contain-intrinsic-size 给一个占位高度, 防滚动条跳
      placeholder.style.contentVisibility = 'auto';
      placeholder.style.containIntrinsicSize = 'auto ' + minH + 'px';
    }

    // 降级路径 1: 没有 IO 直接全量渲染, 行为等同旧版
    if (!HAS_IO) {
      try {
        placeholder.appendChild(factory());
        placeholder.style.minHeight = '';
      } catch (e) {
        console.error('[about] lazyMount fallback (no IO) failed:', e);
      }
      return placeholder;
    }

    let mounted = false;
    const doMount = function () {
      if (mounted) return;
      mounted = true;
      clearTimeout(fallbackTimer);
      try {
        const node = factory();
        if (node) placeholder.appendChild(node);
        placeholder.style.minHeight = '';
      } catch (e) {
        console.error('[about] lazyMount factory failed:', e);
        // factory 失败时强制清掉占位, 避免留白
        placeholder.style.minHeight = '40px';
      }
      try { io.disconnect(); } catch (e) { /* ignore */ }
    };

    // 降级路径 2: 200ms 兜底, 防止 IO 极端不触发
    const fallbackTimer = setTimeout(doMount, 200);

    const io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) doMount();
      });
    }, { rootMargin: '200px' });
    try { io.observe(placeholder); } catch (e) {
      // observe 失败直接走全量
      console.error('[about] lazyMount observe failed:', e);
      doMount();
    }
    return placeholder;
  }

  // 渲染一个 SVG icon (返回 HTML 字符串, 走 unsafeHtml)
  // - key: ICONS 字典的键
  // - size: 渲染尺寸 (像素), 默认 14
  // - cls: 额外 class, 用于覆盖颜色 (例如 'text-dim')
  function svgIcon(key, size, cls) {
    size = size || 14;
    const path = ICONS[key] || '';
    const clsAttr = cls ? ' class="' + cls + '"' : '';
    // path data 必须包在 <path d="..."> 里, 否则浏览器不识别为路径
    return '<svg' + clsAttr + ' viewBox="0 0 24 24" width="' + size + '" height="' + size + '" fill="currentColor" style="vertical-align:-2px;display:inline-block;flex-shrink:0" aria-hidden="true"><path d="' + path + '"/></svg>';
  }

  async function fetchVersion() {
    const local = currentVersion();
    if (local) return local;
    try {
      if (typeof api !== 'function') return null;
      const cfg = await api('GET', '/api/config');
      if (cfg && cfg.version) return cfg.version;
    } catch (e) { /* ignore */ }
    return null;
  }

  // =====================================================================
  // §1. 核心数据看板
  // =====================================================================
  const stats = [
    { label: '总代码量',             value: '125,000+', sub: 'Go 86K · 前端 39K (JS+CSS) · 0 npm 运行时', tone: 'primary' },
    { label: 'AI Token 深度算力淬炼', value: '20 亿+ (2.0B+)', sub: '多模态视觉审核 · 长程深度推理 · 测试闭环', tone: 'accent' },
    { label: '代码行数 (Go)',         value: '86,000+',  sub: '315 个 Go 文件 · 40 个后端子包 · 含 130+ 测试', tone: 'primary' },
    { label: '代码行数 (前端)',       value: '39,000+',  sub: 'vanilla JS 31K + CSS 8K · 22 路由页面',   tone: 'accent'  },
    { label: '提交次数',              value: '184+',     sub: 'v0.1 → v0.19-dev 持续演进',                  tone: 'success' },
    { label: '后端模块',              value: '40',       sub: 'upgrade / dbconsole / comparefs / desknote 等', tone: 'primary' },
    { label: '前端路由与组件',        value: '22+',      sub: '22 个路由页面 + Win32 原生桌面组件 + 4 独立子窗口', tone: 'accent'  },
    { label: 'API 接口',              value: '135+',     sub: 'REST + NDJSON + SSE + WebSocket 全覆盖',      tone: 'primary' },
    { label: '测试用例 (Go)',         value: '1,150+',   sub: '130+ 个 _test.go · 单元 + 集成 + Mock',      tone: 'success' },
    { label: '测试用例 (Node)',       value: '40+',      sub: 'workbench-unit + webservice + app.test 单元', tone: 'success' },
    { label: 'E2E 场景 (Playwright)', value: '1,200+',   sub: '42 脚本 · Windows 1045 通过 · 159 跳过 · 0 失败', tone: 'warn' },
    { label: '修复缺陷',              value: '468+',     sub: 'P0/P1/P2 全量闭环',                          tone: 'warn'    },
    { label: '安全设计点',            value: '17',       sub: '跨文件升级事务 + fail-closed + AST 只读',     tone: 'error'   },
    { label: 'SSH 兼容 profile',      value: '5',        sub: 'modern → legacy · 自动 fallback',           tone: 'primary' }
  ];

  // =====================================================================
  // §2. AI-Native 工业级研发工程 (AI Engineering & Model Matrix)
  // =====================================================================
  const BRAND_SVGS = {
    openai: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M22.28 9.82a5.98 5.98 0 0 0-.52-4.91 6.05 6.05 0 0 0-6.51-2.9A6.07 6.07 0 0 0 4.98 4.18a5.98 5.98 0 0 0-4 2.9 6.05 6.05 0 0 0 .74 7.1 5.98 5.98 0 0 0 .51 4.91 6.05 6.05 0 0 0 6.51 2.9A5.98 5.98 0 0 0 13.26 24a6.06 6.06 0 0 0 5.77-4.2 5.99 5.99 0 0 0 4-2.9 6.06 6.06 0 0 0-.75-7.08zm-9.02 12.6a4.48 4.48 0 0 1-2.88-1.04l.14-.08 4.78-2.76a.8.8 0 0 0 .4-.68v-6.74l2.02 1.17a.07.07 0 0 1 .04.05v5.58a4.5 4.5 0 0 1-4.5 4.5zm-9.66-4.12a4.47 4.47 0 0 1-.53-3.01l.14.08 4.78 2.76a.77.77 0 0 0 .78 0l5.84-3.37v2.33a.08.08 0 0 1-.03.06L9.74 19.95a4.5 4.5 0 0 1-6.14-1.65zM2.34 7.9a4.49 4.49 0 0 1 2.37-1.98V11.6a.77.77 0 0 0 .39.68l5.81 3.35-2.02 1.17a.08.08 0 0 1-.07 0l-4.83-2.79A4.5 4.5 0 0 1 2.34 7.87zm16.6 3.85L13.1 8.36 15.12 7.2a.08.08 0 0 1 .07 0l4.83 2.79a4.5 4.5 0 0 1-.68 8.1v-5.67a.8.8 0 0 0-.4-.67zm2.01-3.02l-.14-.09-4.78-2.78a.78.78 0 0 0-.78 0L9.41 9.23V6.9a.07.07 0 0 1 .03-.06l4.83-2.79a4.5 4.5 0 0 1 6.68 4.66zM8.31 12.86l-2.02-1.16a.08.08 0 0 1-.04-.06V6.07a4.5 4.5 0 0 1 7.38-3.45l-.14.08-4.79 2.76a.8.8 0 0 0-.39.68zm1.1-2.36l2.6-1.5 2.6 1.5v3l-2.6 1.5-2.6-1.5z"/></svg>'; },
    cursor: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M12 2l8.5 5v10L12 22l-8.5-5V7z"/><path d="M12 2v20M3.5 7L20.5 17M20.5 7L3.5 17"/></svg>'; },
    antigravity: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M12 2l2.6 6.8 6.8 2.6-6.8 2.6L12 20.8l-2.6-6.8L2.6 11.4l6.8-2.6z"/><circle cx="12" cy="11.4" r="2.2" fill="var(--bg-1, #1e1e2e)"/></svg>'; },
    trae: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M3 4h18v4h-6.5v12h-5V8H3V4z"/><path d="M18.5 12l2.5 2.5-2.5 2.5M5.5 12L3 14.5l2.5 2.5" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>'; },
    opencode: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 6L3 12l5 6M16 6l5 6-5 6M13 4l-2 16"/></svg>'; },
    claude: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M13.8 2.2a1.8 1.8 0 0 0-3.6 0l-.8 6.4a1.8 1.8 0 0 1-1.6 1.6l-6.4.8a1.8 1.8 0 0 0 0 3.6l6.4.8a1.8 1.8 0 0 1 1.6 1.6l.8 6.4a1.8 1.8 0 0 0 3.6 0l.8-6.4a1.8 1.8 0 0 1 1.6-1.6l6.4-.8a1.8 1.8 0 0 0 0-3.6l-6.4-.8a1.8 1.8 0 0 1-1.6-1.6z"/></svg>'; },
    deepseek: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M3 14c3-6 10-10 18-5-4 1-8 4-10 8-2 4-5 5-8-3z"/><circle cx="15" cy="8" r="1.5" fill="currentColor"/><path d="M9 17c3 2 7 1 10-2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>'; },
    gemini: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M12 2C12 7.52 7.52 12 2 12c5.52 0 10 4.48 10 10 0-5.52 4.48-10 10-10-5.52 0-10-4.48-10-10z"/></svg>'; },
    grok: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M4 4l16 16M20 4L4 20"/><path d="M9 4h6M9 20h6" stroke-width="1.8"/></svg>'; },
    doubao: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M12 3C6.5 3 2 7 2 12c0 3 1.5 5.5 4 7v3l3.5-1.8c.8.3 1.6.5 2.5.5 5.5 0 10-4 10-9s-4.5-9-10-9z"/><circle cx="8.5" cy="11.5" r="1.5" fill="var(--bg-1, #1e1e2e)"/><circle cx="15.5" cy="11.5" r="1.5" fill="var(--bg-1, #1e1e2e)"/></svg>'; },
    glm: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="2"><circle cx="8.5" cy="12" r="5.5"/><circle cx="15.5" cy="12" r="5.5"/><path d="M12 7.5a5.5 5.5 0 0 1 0 9"/></svg>'; },
    kimi: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M12 3a9 9 0 1 0 9 9c0-.4-.03-.8-.08-1.2A7 7 0 0 1 13.2 3.08C12.8 3.03 12.4 3 12 3z"/><path d="M18 4l.8 1.8L20.6 6.6l-1.8.8L18 9.2l-.8-1.8-1.8-.8 1.8-.8z"/></svg>'; },
    minimax: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M3 14v-4M7 17V7M11 20V4M15 17V7M19 14v-4"/></svg>'; },
    muse: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="currentColor"><path d="M12 2l1.8 6.2L20 10l-6.2 1.8L12 18l-1.8-6.2L4 10l6.2-1.8L12 2z"/><path d="M18.5 15l.8 2.7L22 18.5l-2.7.8L18.5 22l-.8-2.7-2.7-.8 2.7-.8.8-2.7z"/></svg>'; },
    mimo: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M12 2l8 4.5v9L12 20l-8-4.5v-9L12 2z"/><path d="M12 2v9m8-4.5l-8 4.5m-8-4.5l8 4.5m0 0v9"/></svg>'; },
    ling: function (sz) { return '<svg viewBox="0 0 24 24" width="' + sz + '" height="' + sz + '" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M5 7c2.5-3 6.5-3 9 0s2.5 6 5 7"/><path d="M5 17c2.5 3 6.5 3 9 0s2.5-6 5-7"/></svg>'; }
  };

  function getBrandSvg(brand, size) {
    const fn = BRAND_SVGS[brand];
    return fn ? fn(size || 16) : '';
  }

  const aiEngineData = {
    computeTokens: '20 亿+ (2.0B+)',
    tools: [
      { name: 'Codex', brand: 'openai' },
      { name: 'Cursor', brand: 'cursor' },
      { name: 'Antigravity', brand: 'antigravity' },
      { name: 'Trae', brand: 'trae' },
      { name: 'OpenCode', brand: 'opencode' }
    ],
    models: [
      { name: 'GPT-6 Astra', brand: 'openai' },
      { name: 'ChatGPT 5.6 Sol', brand: 'openai' },
      { name: 'ChatGPT 5.5', brand: 'openai' },
      { name: 'ChatGPT 5.4', brand: 'openai' },
      { name: 'GPT-5.6 Terra', brand: 'openai' },
      { name: 'GPT-5.6 Luna', brand: 'openai' },
      { name: 'Claude Opus 4.8', brand: 'claude' },
      { name: 'DeepSeek V4 Pro', brand: 'deepseek' },
      { name: 'DeepSeek V4 Flash', brand: 'deepseek' },
      { name: 'Gemini 3.8 Flash', brand: 'gemini' },
      { name: 'Gemini 3.7 Flash', brand: 'gemini' },
      { name: 'Grok 4.6', brand: 'grok' },
      { name: 'Grok 4.5', brand: 'grok' },
      { name: 'Doubao 2.1 Pro', brand: 'doubao' },
      { name: 'GLM 5.2', brand: 'glm' },
      { name: 'Kimi K3', brand: 'kimi' },
      { name: 'Kimi K2.7', brand: 'kimi' },
      { name: 'MiniMax M3', brand: 'minimax' },
      { name: 'MiniMax M2.7', brand: 'minimax' },
      { name: 'Muse Spark 1.3', brand: 'muse' },
      { name: 'Muse Spark 1.2', brand: 'muse' },
      { name: 'MiMo V2.5', brand: 'mimo' },
      { name: 'Ling 3.0 Flash', brand: 'ling' }
    ],
    pillars: [
      {
        title: 'UI 视觉多模态审核 (Vision)',
        icon: 'smTarget',
        body: 'Click-Screenshot-Verification 铁律：22 个页面、5 套自绘主题与 4 个独立窗口在多种分辨率下数万次截图对齐、WCAG 对比度与无闪烁审核。'
      },
      {
        title: '深度推理与安全沙箱 (Reasoning)',
        icon: 'safeClosed',
        body: 'Windows Job Object 进程树递归销毁与句柄管理、数据库只读 AST 词法强制校验、跨文件升级原子事务与 468+ 故障根因逻辑推导。'
      },
      {
        title: '全自动化测试闭环 (Testing)',
        icon: 'quality',
        body: '1,150+ Go 单元测试与 42 个 Playwright 脚本（1,200+ 场景）海量运行日志、DOM 树与调用栈排查，长上下文闭环验证。'
      }
    ]
  };

  // =====================================================================
  // §3. 设计哲学 (Design Principles)
  // =====================================================================
  const principles = [
    {
      icon: 'safeClosed', title: '安全优先 (fail-closed)',
      body: '所有权限决策默认"拒绝"。白名单空 → 一律拒绝；host key 没配 + allow_insecure=false → 不发起连接；admin 专属接口没带 admin token → 403；数据库只接受单条只读 SQL。把"忘记配"和"配错"都收敛到安全侧，让纵深防御没有单点失守即可破防的逻辑链。'
    },
    {
      icon: 'controlled', title: '受控优于开放',
      body: '不开放任意 shell。所有远程命令由后端固定模板生成 (find / grep / sed / sort / head / cat 组合)，目录 / 文件名只能来自配置白名单或前一步 ls 的结果，关键词做严格转义——零命令注入面、零隐式越权。'
    },
    {
      icon: 'context', title: '上下文优先 (context-first)',
      body: '所有 I/O 路径走 ctx。远程命令三段式超时 (SIGTERM → 1s → SIGKILL)；下载任务 30 分钟硬超时；SSE 长连接不被默认 120s 强制断开；SQL 查询流式分批并支持随时主动取消。一次 cancel 终止整条调用链，无悬挂 goroutine。'
    },
    {
      icon: 'simple', title: '极简优于复杂',
      body: '零前端框架、零外部 UI 库、零 CSS 预处理器、零 npm 运行时。vanilla JS + 原生 CSS 变量 + 内嵌 go:embed。125,000+ 行代码，22 个前端路由页面与原生桌面组件，清晰可读，无黑盒构建依赖。'
    },
    {
      icon: 'testable', title: '可测优于能跑',
      body: 'sshclient → Streamer 接口、sftpclient → RemoteFS 接口、dlmanager → Session 模型：每个核心包都对测试友好，提供 mock 注入点。fake-websphere + mock_sshd.py 给集成测试真实感；Go 测试 1,150+ 用例，130+ 个测试文件，单测覆盖率 82%+。'
    },
    {
      icon: 'zeroPlain', title: '凭据零落盘 (zero plain)',
      body: '密码永不写进 audit.log / URL / 错误信息 / 前端响应。可选 OS 钥匙串 (macOS Keychain / Windows DPAPI / Linux Secret Service) 按 (system, server, user) 三元组加密；file 模式走 AES-256-GCM，密文绑 AAD 防替换攻击。'
    },
    {
      icon: 'persist', title: '写后即持久 (write-then-persist)',
      body: 'config.yaml 写回走 tmpfile + rename(2)，损坏不污染线上配置；downloads 元数据走单文件 .kairo-meta.json 加 mtime 失效缓存；preferences.json 写入显式 chmod 0600；便笺与任务基于 revision 乐观并发控制。'
    },
    {
      icon: 'operator', title: '工程师视角 (operator-grade)',
      body: 'SSH 错误归类到运维友好中文（"密码错误 / 账号锁定 / 网络超时 / HostKey 不匹配"）；诊断中心 3 问自检；Diagnostics 报告按"App / Runtime / Tools / Servers / Issues"分块；数据库 SQL 错误持久化展示；日志助手多对多勾选矩阵。'
    }
  ];

  // =====================================================================
  // §3. 架构总览 (4 层)
  // =====================================================================
  const architecture = [
    {
      layer: 'L1', name: '展示层 (Presentation)',
      detail: 'Web Browser + Win32 原生桌面 · 单页应用 · hash-router 路由 · vanilla JS · 22 个路由页面 · 5 套主题 · 4 个独立子窗口',
      tech: ['原生 ES2020', 'CSS 变量主题', 'hash 路由', 'EventSource(SSE)', 'WebSocket', 'localStorage', 'Win32 原生窗口'],
      duty: '所有用户交互、渲染、状态机、主题切换、SSE 订阅、WebSocket 终端、桌面便笺/宠物浮层。不依赖任何 npm 运行时。'
    },
    {
      layer: 'L2', name: '网关层 (HTTP Server)',
      detail: '127.0.0.1:18092 (默认) · net/http · handlers_*.go 路由表 · go:embed web/ · License 网关',
      tech: ['net/http', 'go:embed', 'JSON', 'NDJSON', 'SSE', 'WebSocket', 'Bearer Token', '本地 CORS 守卫'],
      duty: '入口鉴权 (可选 Bearer + IP 白名单)、路径白名单、RBAC 校验、License 状态守卫、SSE 长连接维持、静态资源分发。所有 API 入口走 requireAdmin / requireAuth / sanitize / 路径校验四道关。'
    },
    {
      layer: 'L3', name: '业务层 (Domain)',
      detail: 'upgrade · dbconsole · comparefs · schedtask · deskpet · desknote · winui · popup · notify · waspack · webservice · wscodegen · sshclient · sftpclient · sshshell (PTY) · logquery · dlmanager · tailmgr · diff · downloads · formatter · credentials · license · sponsor · reminder',
      tech: ['database/sql 连接池', 'x/crypto/ssh', 'gorilla/websocket', 'pkg/sftp', 'jlaffaye/ftp', 'x/text (GBK 透明转换)', 'AES-256-GCM', 'COW Config', 'Worker Pool', 'Myers Diff', 'PTY + WebSocket', 'Windows Job Object', 'GDI 原生弹窗', 'STA 宿主线程'],
      duty: '跨文件升级原子事务、数据库只读查询与元数据提取、文件多协议比较与受控同步、受控 SSH 执行、交互式 PTY 终端、SFTP 文件浏览、定时任务进程树调度、GDI 弹窗与告警总线、命令模板生成、异步任务会话池、实时 SSE 广播、行级 diff、凭据存取。元数据全部集中维护，handler 只负责协议转换。'
    },
    {
      layer: 'L4', name: '基础设施层 (Infra)',
      detail: 'config (COW Manager + upgrade 协调器) · credentials (keyring/file/disabled) · downloads (元数据索引) · audit (滚动日志) · preferences · portreuse · tray (系统托盘)',
      tech: ['yaml.v3', 'go-keyring', 'AES-GCM', 'atomic rename', 'chmod 0600', 'tail-file rotate', 'fyne.io/systray', 'Job Object', 'upgrade snapshot'],
      duty: '配置原子加载与跨文件升级事务、凭据加密存储、下载元数据索引、操作审计滚动文件、系统托盘常驻、进程树生命周期回收。所有"用户状态变更"都在这层留下不可变痕迹，升级失败全量回滚。'
    }
  ];

  const dataFlow = [
    { from: 'Browser', to: 'HTTPServer', label: 'fetch + EventSource' },
    { from: 'HTTPServer', to: 'Handlers', label: '路由匹配' },
    { from: 'Handlers', to: 'Domain', label: '参数校验 + 业务调用' },
    { from: 'Domain', to: 'SSH/SFTP', label: '受控命令/读取' },
    { from: 'Remote', to: 'Domain', label: 'stdout/stderr' },
    { from: 'Domain', to: 'Handlers', label: '结果 + 元数据' },
    { from: 'Handlers', to: 'Browser', label: 'JSON / SSE' }
  ];

  // =====================================================================
  // §4. 后端技术栈 (16 依赖逐项)
  // =====================================================================
  const backendStack = [
    { name: 'Go', version: '1.24+', role: '主语言', desc: '主线面向 Windows 10/11、macOS 与 Linux；goroutine 调度，静态二进制，零运行时依赖；86,000+ 行 Go 代码（含 130+ 个测试文件）。' },
    { name: 'github.com/sijms/go-ora/v2', version: 'v2.8.24', role: 'Oracle 驱动', desc: '纯 Go thin driver，无需 Oracle Instant Client；数据库工作台生产兼容目标为 Oracle 11g。' },
    { name: 'github.com/go-sql-driver/mysql', version: 'v1.9.3', role: 'MySQL 驱动', desc: 'database/sql 连接池、只读查询、元数据与流式结果。' },
    { name: 'github.com/redis/go-redis/v9', version: 'v9.20.0', role: 'Redis 客户端', desc: '单机 / Cluster / Sentinel；SCAN 分页、TTL 与类型化 Key 预览；大 Key 采用有限读取，避免阻塞和内存爆炸。' },
    { name: 'golang.org/x/crypto/ssh', version: 'v0.31.0', role: 'SSH 客户端', desc: '深度定制的 SSH 协议栈；5 套 KEX profile 自动 fallback；keyboard-interactive 认证；HostKey 指纹校验 (fail-closed)。' },
    { name: 'github.com/pkg/sftp', version: 'v1.13.6', role: 'SFTP 子系统', desc: '文件 Open/Stat/Read。抽象出 RemoteFS 接口，支持 SFTPBackend + ShellBackend 双 backend 自动降级。' },
    { name: 'github.com/gorilla/websocket', version: 'v1.5.3', role: 'WebSocket 协议栈', desc: 'SSH 交互终端全双工 PTY 通信、前端尺寸同步与 HTTP 测试台 WebSocket 客户端。' },
    { name: 'github.com/jlaffaye/ftp', version: 'v0.2.4', role: 'FTP / FTPS 客户端', desc: '文件比较与同步工作台 FTP / FTPS 远程后端实现，支持流式读取与连接复用。' },
    { name: 'fyne.io/systray', version: 'v1.11.0', role: '系统托盘', desc: '跨平台系统托盘管理（Windows / macOS / Linux）；提供后台常驻、快捷菜单、主窗口调起与退出回收。' },
    { name: 'golang.org/x/text', version: 'v0.21.0', role: '字符编码', desc: 'simplifiedchinese.GBK / GB18030 / GB2312 / UTF-16 透明编码转换；老 WebSphere / Oracle / AIX 上的日志与报文直读不乱码。' },
    { name: 'github.com/zalando/go-keyring', version: 'v0.2.8', role: 'OS 钥匙串抽象', desc: '统一 macOS Keychain / Windows DPAPI / Linux Secret Service 三个原生后端；零明文落盘。' },
    { name: 'github.com/danieljoos/wincred', version: 'v1.2.3', role: 'Windows DPAPI', desc: 'Windows 平台 keyring 后端实现，由 go-keyring 间接依赖。' },
    { name: 'github.com/godbus/dbus/v5', version: 'v5.2.2', role: 'Linux Secret Service', desc: 'Linux 平台通过 D-Bus 与 GNOME Keyring / KWallet 通信，keyring 后端实现。' },
    { name: 'gopkg.in/yaml.v3', version: 'v3.0.1', role: '配置解析', desc: 'config.yaml 解析 + 原子写回；Schema 校验防止非法配置写入；trailing garbage 拒绝。' },
    { name: 'golang.org/x/sys', version: 'v0.30.0', role: '系统调用', desc: '平台特定系统调用；Windows Job Object、原生桌面窗口、托盘与进程树生命周期依赖该层。' },
    { name: '标准库', version: '—', role: '运行时', desc: 'net/http (HTTP server + SSE)、embed (web/ 静态资源内嵌)、context (取消传播)、database/sql、os/exec、crypto/sha256、bufio。' }
  ];

  // =====================================================================
  // §5. 前端架构 (11 模块)
  // =====================================================================
  const frontendStack = [
    { name: 'core.js', desc: 'DOM/工具内核 — el() 安全构造器、escapeHtml、toast、confirmDialog、modal、tabs、virtualList、resize 监听。零依赖。' },
    { name: 'state.js', desc: '全局状态机 — 当前用户、主题、routeMap、routeNames、tailHighlights 等共享状态。Kairo.state.* 单一入口。' },
    { name: 'api.js', desc: 'HTTP / SSE 客户端 — api(method, path, body) 统一封装；自动加 Bearer token；SSE EventSource 工厂；统一错误处理。' },
    { name: 'theme.js', desc: '主题切换 — dark / light / green / hc / xianxia（玄墨鎏金·武侠风）5 套主题，inline script 在 <head> 提前设 data-theme 防 FOUC。' },
    { name: 'auth.js', desc: '认证层 — 拉 /api/auth/status 探测；token cookie 管理；role-gated UI 显隐。' },
    { name: 'pages/*.js', desc: '22 个路由页面 — home / websphere / files / waspack / ssh / database / http / webservice / wscodegen / diagnostics / config / downloads / formatter / timestamp / cron / jsonpath / compare / commands / notes / tasks / sponsor / about。每个页面一个 IIFE，路由切换时整体替换 view。' },
    { name: 'pet.js + notes.js', desc: '原生桌面组件联动 — 桌面便笺 / 桌面宠物状态机、拖拽吸边、皮肤选择、双向 revision 并发校验与快捷置顶。' },
    { name: 'tail.js + tail.html', desc: '独立 tail 窗口 — 从主页面剥离的 tail 流，跟踪 SSE 不影响主页面操作；行级 DOM 节点池 + rAF 批量 flush (50ms/100 行)；支持 Ctrl/⌘+F 页面内搜索高亮。' },
    { name: 'preview.html', desc: '文件预览子窗口 — 单文件模态 + 新窗口双模式，支持文本 / GBK 编码自动识别；支持页面内搜索高亮 (TreeWalker 遍历文本节点，不破坏关键词高亮 span)。' },
    { name: 'ssh.html + ssh.js', desc: '独立 SSH 终端子窗口 — xterm.js 5.5 (ES5 转译) + WebSocket + PTY 全双工；内嵌 SFTP 文件侧栏，一屏完成命令与文件交互。' },
    { name: 'vendor/search-hl.js', desc: '搜索高亮公共模块 — preview.html / tail.html / 上下文窗口三处共用；TreeWalker 遍历文本节点 + mark 标签包裹，Enter/Shift+Enter 跳转匹配，Esc 清除。' }
  ];

  // =====================================================================
  // §6. 工程化实践 (14 项)
  // =====================================================================
  const engineering = [
    { title: 'COW Config Manager', body: '每次 Replace 整体换指针，handler 读快照不被并发写入撕裂；读端零锁。' },
    { title: 'Goroutine Worker Pool', body: '多服务器搜索 / 列文件走 4 路并发 + errgroup 风格隔离，单机故障不阻塞其他 server。' },
    { title: 'context 优先', body: '所有远程命令 / 下载 / Tail / SQL 查询都用 ctx 控制超时；ctx cancel 即整条链路取消。' },
    { title: '三段式超时', body: 'ctx deadline → SSH session.Signal(SIGTERM) → 1s 后 SIGKILL，绝不依赖远端 timeout 命令。' },
    { title: '原子写回 yaml', body: 'tmpfile + rename(2)，损坏不污染线上配置；SSH debug log / traffic log 同样走轮转。' },
    { title: '受控命令模板', body: 'logquery 包内固定生成 find / grep / sed / sort / head / cat 组合；目录 / 文件名走白名单；关键词拒绝 shell 元字符。' },
    { title: 'tob-tail 批量 flush', body: 'TailViewer 行级 DOM 节点池 + requestAnimationFrame 50ms/100 行 flush，避免 textContent += 整段重排。' },
    { title: '进程内 fs.FS 接口', body: 'httpserver.serveStatic 直接读 fs.FS，避免额外 IO 包装；测试可注入假 FS。' },
    { title: 'sftpDialer 包级变量', body: '集成测试注入 fake dialer，绕过真 SSH；handler 业务逻辑完全可测。' },
    { title: 'Windows Job Object 进程树接管', body: '定时任务在 Windows 下由 Job Object 归组接管，超时或退出自动递归回收子孙进程，杜绝孤儿僵尸进程。' },
    { title: '数据库只读策略双重校验', body: 'dbconsole 在 SQL 执行前通过词法与 AST 校验强制单语句与只读边界，严格阻断多语句及写操作。' },
    { title: 'vendor/ 提交', body: 'clone 后无网也能 -mod=vendor 编译；CI 不依赖外网。' },
    { title: 'go:embed web', body: '静态资源打进单 exe；跨平台部署一条命令搞定；分发包仅一个二进制。' },
    { title: 'interface{} > struct{}', body: 'Streamer / RemoteFS / Manager 都是接口，业务代码对底层零依赖；mock 注入方便测试。' }
  ];

  // =====================================================================
  // §7. 安全白皮书 (17 条)
  // =====================================================================
  const security = [
    { id: 'S01', title: '默认只监听 127.0.0.1', detail: '未启用 auth 时 0.0.0.0 / 内网 IP 直接被配置校验拒绝，不向局域网暴露；启用 auth 后才允许监听 0.0.0.0 / 内网 IP。' },
    { id: 'S02', title: '不开放任意 shell', detail: '所有远程命令由后端固定模板生成（find / grep / sed / sort / head / cat 组合），无任意命令注入面。' },
    { id: 'S03', title: '白名单路径', detail: '日志助手的目录必须来自 config.log_dirs；文件名只能来自 find 列出结果；free_file_roots / compare_allowed_roots 双白名单并行。' },
    { id: 'S04', title: '文件浏览器按账号权限', detail: '实际访问控制交给 SSH server 端（账号 + 文件系统权限 + sshd_config），本工具不做提权。' },
    { id: 'S05', title: '关键词严格转义', detail: '搜索关键词做白名单校验，拒绝 ` \' \\ $ ; & | < > ( ) { } [ ]` 等 shell 元字符与 NUL / 换行。' },
    { id: 'S06', title: '密码零持久', detail: '从不写进 audit.log / URL / 错误信息 / 前端响应；可选 OS 钥匙串按 (system,server,user) 三元组加密存储；file 模式走 AES-256-GCM。' },
    { id: 'S07', title: '只读不写', detail: '不提供上传 / 删除 / 改远程文件的能力；文件浏览器只读 + 下载；SSH 命令模板只有 find / grep / sed / sort / head / cat。' },
    { id: 'S08', title: '路径穿越防护', detail: '所有用户输入做绝对路径校验 + filepath.Clean + Rel 双重校验；拒绝 .. / NUL / 换行；URL 路径穿越 404；14 种攻击向量全覆盖。' },
    { id: 'S09', title: '超时硬约束', detail: '远程命令三段式（SIGTERM → 1s → SIGKILL）；下载任务 30 分钟硬超时；tail 30 分钟空闲回收（可配，最小 5 分钟）。' },
    { id: 'S10', title: '下载文件只落 downloads/', detail: '任意 target_dir 必须绝对路径 + 写探针通过；config 默认下载目录锁定；同任务同名文件本地加 001/002/... 前缀。' },
    { id: 'S11', title: '硬上限', detail: '单次下载最多 100 个文件，30 分钟超时；搜索最大 200 命中；SSE 单事件 4KB 限流。' },
    { id: 'S12', title: 'CORS / 跨域', detail: '所有响应带 X-Content-Type-Options: nosniff / Referrer-Policy: no-referrer / X-Frame-Options: DENY，禁止跨域 iframe 与 MIME 嗅探。' },
    { id: 'S13', title: 'TOCTOU 加固', detail: 'handler 入口取一次配置快照（COW Manager 读快照），全程复用同一份，避免 check-then-use 时间窗被改写。' },
    { id: 'S14', title: 'Bearer Token + IP 白名单 (可选)', detail: 'config.yaml 的 auth 段配置 token（role=admin/user + allowed_ips），启用后未带有效 token → 401；IP 不在白名单 → 403；admin 专属接口（配置导入 / 凭据清空 / 服务器增改 / openers / download-retention / 数据库数据源增改）强制 role=admin。' },
    { id: 'S15', title: '数据库只读策略与资源硬上限', detail: '只读 AST 词法双重拦截，只允许单条只读 SQL；限制超时、最大行数与返回体；只读执行计划 (PLAN_TABLE/EXPLAIN)；Redis SCAN 游标分页与大 Key 截断防护。' },
    { id: 'S16', title: '任务进程树 Job Object 沙箱隔离', detail: 'Windows Job Object 进程归组 + POSIX 进程组；命令执行超时或 Kairo 退出时，彻底递归销毁全部子孙进程，避免后台残留孤儿进程。' },
    { id: 'S17', title: '跨文件升级原子事务与降级防护', detail: 'internal/upgrade 在升级前对全部用户配置与资产做全量快照，N->N+1 链式迁移，任一失败全量原子回滚，并强制拒绝旧版本覆写高版本资产，杜绝版本撕裂与半更新脏数据。' }
  ];

  // =====================================================================
  // §8. SSH 兼容矩阵 (5 profile)
  // =====================================================================
  const sshProfiles = [
    { name: 'modern', kex: 'curve25519 优先 → ecdh-sha2-* → diffie-hellman-group14-sha256', target: 'OpenSSH 7.4+ / 现代 Linux', note: '默认 KEX 集最强，性能最好。' },
    { name: 'compat (默认)', kex: 'modern 子集 + 老算法回退', target: 'OpenSSH 6.2p2 / 6.0 / 多数企业内网', note: '握手失败自动 fallback 到下一套。' },
    { name: 'no-ecdh', kex: '完全去掉 ECDH，仅 DH', target: '某些老 OpenSSL 的 ECDH_INIT 后 RST bug', note: '罕见场景，但留个口子。' },
    { name: 'legacy', kex: 'group14-sha1 + ssh-dss', target: 'OpenSSH 5.x / 6.0 / AIX / 老堡垒机', note: '老设备救星，牺牲安全性换兼容。' },
    { name: 'auto', kex: 'compat → no-ecdh → legacy 顺序自动 fallback', target: '不确定目标 server 时的默认兜底', note: '外层 sshDialOuterTimeout = 45s (3×12s + buffer)，单套 sshAttemptTimeout = 10s。' }
  ];

  // =====================================================================
  // §9. 质量保障
  // =====================================================================
  const quality = [
    { tier: 'L1 单元测试', tool: 'go test ./...', coverage: '1,150+ 测试函数', detail: '130+ 个 _test.go 文件，覆盖升级事务、数据库只读策略、文件工作台、桌面状态、SSH/SFTP、日志、任务进程树、WebService、凭据、配置与 httpserver 全链路。' },
    { tier: 'L2 集成测试', tool: 'mock_sshd.py + fake-websphere', coverage: 'Windows/Linux shell 双语义', detail: 'Python helper 启动 SSH server；Windows 自动使用 Git Bash/GNU 工具，验证列文件、组合搜索、上下文、tail、下载、凭据、RBAC、路径穿越、host key 与进程回收。' },
    { tier: 'L3 E2E (Playwright)', tool: 'tests/e2e + 独立运行配置', coverage: '1,204 场景 (42 脚本)', detail: '真实 Windows Chromium 全量结果：1045 通过、0 失败、159 条件跳过；另以 1366×900、1920×1080、1024×768 跑 42 项页面矩阵，控制台/页面/网络错误均为 0。' },
    { tier: 'L4 手动验收', tool: 'docs/ACCEPTANCE.md + scripts/acceptance_run.py', coverage: '5b/5c/5d 全量', detail: '文件浏览器 / tail / 测试矩阵 / 完整业务路径逐项验收，每项可执行 / 可验证；scripts/acceptance_run.py 一键回归。' },
    { tier: 'L5 静态检查', tool: 'go vet + gofmt', coverage: '100%', detail: '提交前必跑；CI 流水线集成；不允许未格式化代码合入 main。' },
    { tier: 'L6 文档同步', tool: 'README + docs/RELEASE-NOTES + docs/qa/', coverage: '全量', detail: '代码改动同步更新文档；CHANGELOG 与 release notes 双轨；qa 目录存所有 E2E 截图与覆盖率报告；v0.9.0 rebrand 一次性清理 docs 全量。' }
  ];

  // =====================================================================
  // §10. 功能模块 (20 大模块深度剖析)
  // =====================================================================
  const featureModules = [
    {
      icon: 'websphere', name: 'WebSphere 日志助手',
      pages: ['websphere'],
      apis: ['/api/logs/list/targets', '/api/logs/search/multi', '/api/logs/context', '/api/logs/tail/*'],
      pkg: 'internal/logquery + internal/tailmgr',
      desc: '企业级日志检索终端。多对多目标选择 (system → server → dir 三级联动)、关键词搜索 (默认 30 行上下文，可配)、上下文窗口查看 (前后各 N 行)、实时 tail (SSE 流式推送)。',
      features: [
        '多对多目标选择引擎：checkbox matrix 一次勾选 N 个 server × M 个目录',
        '三级目录展开模型：system → server → dir 按需展开，避免一次拉全',
        '关键词严格转义：拒绝 shell 元字符 + NUL + 换行',
        '搜索上下文窗口：行级前后 N 行可配 (默认 30，最大放开至 5000 行)',
        '时间窗口过滤：start/end + 时间戳区间双模式',
        '实时 tail 独立窗口：从主页面剥离，SSE 长连接不被 120s 强制断开',
        'tail 高亮规则持久化：data/preferences.json 的 tail.highlights',
        '行级 DOM 节点池 + rAF 批量 flush：千行无卡顿'
      ]
    },
    {
      icon: 'sshTerminal', name: 'SSH 终端 + SFTP 文件浏览器',
      pages: ['ssh'],
      apis: ['/api/ssh/shell/ws (WebSocket)', '/api/ssh/sftp/*', '/api/files/download*'],
      pkg: 'internal/sshshell + internal/sshclient + internal/sftpclient',
      desc: '交互式 SSH 终端 (xterm.js + WebSocket + PTY) 旁侧内嵌 SFTP 文件浏览器，一个页面完成"敲命令 + 看文件 + 拉文件"三件事。GBK 编码透明转换，老服务器中文不乱码。',
      features: [
        'xterm.js + WebSocket 全双工：输入直达 PTY stdin，stdout 推送前端，完整渲染 ANSI 颜色 / 光标 / 滚动',
        'SFTP 面板与终端并排：左终端右文件浏览器，目录导航 + 文件下载 + 路径跳转一屏完成',
        '路径输入框直达：持久可见的路径输入框，输入绝对路径回车即跳转，不再只能点面包屑',
        '「进入当前目录」按钮：通过 WS query_cwd 帧向活跃 shell 注入 pwd，OSC 999 私有序列标记起止，一键同步终端 cwd',
        'GBK / GB18030 透明转换：老 WebSphere / Oracle / AIX 的中文输出直显不乱码',
        '5 套 SSH KEX profile 自动 fallback：modern → compat → no-ecdh → legacy，老 sshd 5.x / AIX / 堡垒机即用',
        'xterm.js ES5 转译：esbuild 把 xterm.js 5.5+ 转译到 ES5，老 Chrome (Win7 内网) 也能跑',
        '终端目录 / 复制路径：SFTP 面板操作栏支持一键复制当前工作目录、跳转终端所在目录',
        '统一下载通知：SFTP 下载完成走 Kairo.core.notify，打开目录 / 复制路径 / 查看下载历史三按钮'
      ]
    },
    {
      icon: 'database', name: '数据库工作台 (v0.16-v0.19-dev)',
      pages: ['database'],
      apis: ['/api/database/sources', '/api/database/query', '/api/database/export', '/api/database/metadata/*', '/api/database/explain', '/api/database/redis/*'],
      pkg: 'internal/dbconsole',
      desc: '面向生产运维的专业数据库控制台：适配 Oracle 11g (go-ora)、MySQL (go-sql-driver) 与 Redis (go-redis)；支持对象树、字段面板、只读 SQL、光标所在语句精准执行、流式真实分页、断线透明重连、单记录模式与导出。',
      features: [
        '多源统一适配：Oracle 11g 纯 Go thin driver、MySQL 只读连接池、Redis 单机/Cluster/Sentinel 拓扑感知',
        '光标所在语句精准执行：statementModel 自动提取光标所在有效 SQL，多重尾部分号智能规整，杜绝批量或误执行',
        '智能分页协议：支持 page / page_size / has_next / ordered 流式分页，Oracle ROWNUM 别名列自动脱敏过滤',
        '断线透明重试与会话治理：网络抖动一次透明重连，幽灵 Tab 孤儿态资源会话后台自动释放回收',
        '小结果集直出渲染：<=600 行轻量查询直接渲染真实 DOM，无缝绕过虚拟滚动初始测量延迟',
        '完整对象浏览器：按 Schema 分组展示表、视图、物化视图、函数、过程、包、触发器、序列与同义词',
        '字段面板快速生成：一键复制 SELECT 字段清单，降低排障和接口对接时的重复录入',
        '持久 SQL 错误工作区：执行失败在结果区持久展示错误标题、根因与修复建议，不依赖短暂 Toast',
        '可调整结果表格：列宽拖拽持久化、表头排序、即时本地筛选、右键复制、列显隐管理',
        '网格与单记录模式：网格视图适合横向对比，单记录视图展开为字段/值清单并支持逐行翻页',
        'SQL 片段与快捷键：支持 ${cursor} 占位符、缩写展开 (sf → SELECT * FROM) 与自定义执行按键',
        '多页签并行查询：支持最多 6 个查询页签独立保存语句、结果与取消句柄，后台查询并发执行',
        '只读执行计划：Oracle PLAN_TABLE 与 MySQL EXPLAIN 结构化展示，辅助排查慢查询',
        'Redis 安全治理：读白名单扩展 (TYPE/TTL/HGET/HSCAN/LRANGE)，受控 TTL mutation 权限隔离'
      ]
    },
    {
      icon: 'compareIc', name: '文件与文本比较工作台 (v0.16-v0.19-dev)',
      pages: ['compare'],
      apis: ['/api/diff/compare', '/api/compare/scan', '/api/compare/jobs/*', '/api/compare/sync*', '/api/compare/connections'],
      pkg: 'internal/diff + internal/comparefs',
      desc: '多协议文件与文本比对系统：统一 Local / SFTP / FTP / FTPS 四大后端，基于智能内容散列与 Myers diff 算法，支持常驻语法高亮、目录扫描、分类比对、任务化后台执行与受控同步。',
      features: [
        '常驻语法高亮编辑器：syntax-editor 实时语法着色与响应式高度视口自动收缩，支持多步撤销重做',
        '实时比对防覆盖状态机：按键后自动计算实时 diff，解决异步渲染覆盖旧结果的竞态问题',
        'Windows 宿主线程原生对话框：choose_dialog 接入常驻 OleInitialize UI 线程并加固 recover，杜绝闪退与连接重置',
        '多协议后端抽象：Local / SFTP / FTP / FTPS 统一 RemoteFS 接口，支持跨协议目录比对',
        '智能内容散列校验：解决同大小同时间文件漏报问题，同时保留极速元数据降级模式',
        'Myers diff O(ND) 算法：单文件 <4MB / 几万行毫秒级比对，支持 12 种忽略规则',
        'diff2html 多视图：unified / side-by-side / 仅差异三种视图，统一输出 .diff 补丁文件',
        '任务化后台 Job 模型：目录扫描、深度比对与同步走后台任务，进度可观察并支持主动取消',
        '受控单向同步：按勾选项覆盖，支持覆盖前自动备份原文件，不执行高风险镜像删除',
        'compare_allowed_roots 白名单：默认空=无校验（本机/内网直接比对），配了目录才按白名单限制'
      ]
    },
    {
      icon: 'downloader', name: '文件下载器 (任意路径)',
      pages: ['files'],
      apis: ['/api/files/list', '/api/files/preview', '/api/files/download*'],
      pkg: 'internal/dlmanager + internal/sftpclient',
      desc: '任意远端路径列表 / 预览 / 下载，支持多 server 并列、批量下载、外部程序打开。',
      features: [
        '3 种列文件模式：单 server+path / 多 server+path / targets[]',
        '文件预览：默认 1MB，上限 10MB，单文件模态 + 新窗口双模式',
        '异步下载任务：dlmanager.Session 模型，后台下载 + SSE 进度流',
        '下载历史：downloads/YYYYMMDD/<file> 落盘，元数据走 .kairo-meta.json',
        '保留策略：download_retention_days (默认 7 天) + download_max_count (默认 1000)，自动清理',
        'external_openers：用外部程序打开下载文件 (Notepad++ / VSCode / 自定义)',
        '下载取消：幂等 cancel ctx；SSE done 事件统一收尾',
        '同名不再覆盖：本地加 001/002/... 前缀'
      ]
    },
    {
      icon: 'editIc', name: '在线编辑 (v0.13 起)',
      pages: ['files'],
      apis: ['/api/edit/get', '/api/edit/save', '/api/edit/list', '/api/edit/history', '/api/edit/rollback'],
      pkg: 'internal/httpserver/handlers_edit.go',
      desc: '在文件下载页直接编辑文本类远端文件（properties / xml / conf / yml / txt 等），无需下载到本地 → 本地编辑器 → 上传。保存走 SFTP WriteFile + 自动备份原文件 + 写审计。',
      features: [
        '文本文件类型自动识别：白名单 + 黑名单混合策略，避免误点二进制文件',
        '编辑界面：textarea + 字符数 + 行数 + 编码选择 (utf-8 / gbk / gb18030) + 修改标记',
        '保存即备份：原文件复制到 .kairo-edit.bak (按 mtime 索引) 再覆盖，最多保留 20 份',
        '保存即审计：每次写操作写 logs/audit.log (op=edit.save)，含原路径与字节数',
        '编辑历史：最近 50 次编辑记录，按文件路径聚合，支持一键回滚',
        '并发安全：handler 入口取 ctx；保存走原子 tmpfile + rename；30 分钟硬超时'
      ]
    },
    {
      icon: 'waspack', name: 'WAS 投产打包 (v0.17-v0.19-dev)',
      pages: ['waspack'],
      apis: ['/api/waspack/preview', '/api/waspack/build', '/api/waspack/open'],
      pkg: 'internal/waspack + internal/httpserver/handlers_waspack.go',
      desc: '面向 credit IntelliJ 工程的投产清单打包器：对照 src / WebRoot / WEB-INF/classes 预检文件，一键直接打包，生成现网可执行的 tar、备份脚本与执行脚本。',
      features: [
        '一键直接打包 (/api/waspack/build)：支持 fail / clean_kairo_artifacts / replace 三种受控输出策略',
        '产物白名单清理：按当次包名只清理明确的打包产物并保留其他文件，投产目录不写入内部标记',
        '增量批处理清单解析：智能关联批处理脚本自动化联动，支持多模块清单连续配对',
        '清单规范化：支持 ./src/...、./WEB-INF/...、工程绝对路径粘贴，自动去重并拒绝越界/绝对路径',
        '工程布局探测：兼容 IntelliJ src + WebRoot 与 exploded WAR 根目录，缺失项和工程告警先于生成展示',
        'java ↔ class 自动配对：补齐外部类与内部类 .class，避免只投源码或漏投运行时字节码',
        '固定产物：list.txt、Bak{包名}.sh、{包名}.sh、{包名}.tar，并支持生成后打开输出目录'
      ]
    },
    {
      icon: 'webservice', name: 'WebService 调试中心 (v0.12 起)',
      pages: ['webservice'],
      apis: ['/api/wsdl/*', '/api/soap/*', '/api/ws/xml/*'],
      pkg: 'internal/webservice',
      desc: '面向老 Java / WebSphere / XFire / SOAP 场景的轻量 SoapUI：WSDL 导入 → 报文生成 → 接口测试 → 模板 → 历史 → Mock + XML 格式化。',
      features: [
        'WSDL 双导入模式：URL 拉取 (30s 超时) / 本地 .wsdl/.xsd/.xml 上传 (单文件 4MB，合计 16MB)',
        '外部 XSD import / include 递归加载；XSD complex content / extension 继承解析',
        '解析失败降级：受影响 operation 保留 InputRaw/OutputRaw + Warnings，不阻塞其他',
        'SOAP 1.1 / 1.2 双版本；选中 operation 自动生成 Envelope；输入/输出参数树展开',
        '接口测试：自定义 endpoint / SOAPAction / Headers / Body；超时 + 取消；分块回显',
        '模板管理 (按分组命名) + 历史回放 (最近 500 条，搜索 + 一键 replay)',
        'Mock 服务端：保存即生效；record 异步落盘 (recordQueue + 后台 goroutine，不阻塞热路径)',
        '多编码支持：UTF-8 / GBK / GB2312 / GB18030 / UTF-16 自动识别'
      ]
    },
    {
      icon: 'wscodegen', name: 'WS Java 代码生成 (v0.17 起)',
      pages: ['wscodegen'],
      apis: ['/api/wscodegen/engines', '/api/wscodegen/detect-jdk', '/api/wscodegen/scan-project', '/api/wscodegen/preview', '/api/wscodegen/generate', '/api/wscodegen/download-zip', '/api/wscodegen/push-project'],
      pkg: 'internal/wscodegen',
      desc: '从已导入 WSDL / 本地文件 / URL 生成 Java 客户端。优先对齐工程 JDK 与 lib 里的 Axis / XFire / CXF jar，而不是默默使用本机 PATH 上的 JDK。',
      features: [
        '六种引擎：portable HttpURLConnection、JAX-WS、CXF、Axis 1.4、Axis2、XFire 1.2',
        '内置生成永远可用：Java 1.6 源码，无 diamond / try-with-resources；portable 零第三方依赖',
        '官方工具模式：用工程 JDK 跑 wsimport / WSDL2Java / Wsdl11Generator，classpath 来自扫描到的 jar',
        'JDK 探测：JAVA_HOME / 常见安装目录；JDK 8 生成喂 JDK 6 时强制提示 JAX-WS 2.1',
        '项目扫描：lib / WEB-INF/lib / pom.xml，自动建议引擎并勾选生成器 jar',
        '一键下载 ZIP：不指定目录也能拿走完整源码；写入工程：按 WebRoot/WEB-INF/lib 推断 src 后落盘'
      ]
    },
    {
      icon: 'http', name: 'HTTP / WebSocket 测试台',
      pages: ['http'],
      apis: ['/api/http/cases', '/api/http/envs', '/api/http/request', '/api/http/curl-parse', '/api/http/ws/*'],
      pkg: 'internal/httpserver',
      desc: '完整的 HTTP 与 WebSocket 客户端实现：GET/POST/PUT/DELETE/PATCH 全方法、自定义 Headers、Body 格式化、curl 命令解析、WebSocket 全双工收发、请求历史回溯、用例收藏管理。',
      features: [
        '全方法支持：GET / POST / PUT / DELETE / PATCH / HEAD / OPTIONS',
        'curl 导入解析：支持从 Chrome / Fiddler / Postman 复制 curl 命令一键解析填入',
        'WebSocket 调试：支持连接建立、消息发送、心跳轮询与消息历史回显',
        '环境变量模板：{{var}} 插值，免去重复 baseURL 切换',
        '用例收藏与历史：命名 + 标签 + 排序，每次请求留痕并可一键重放',
        '零额外依赖：纯标准库 net/http + gorilla/websocket'
      ]
    },
    {
      icon: 'formatter', name: '格式化器 / 小工具集',
      pages: ['formatter', 'timestamp', 'cron', 'jsonpath'],
      apis: ['/api/format/*'],
      pkg: 'internal/formatter',
      desc: 'JSON / XML / YAML / URL-encoded 格式化与校验 + 时间戳转换 + Cron 解析 + JSONPath 查询。',
      features: [
        'JSON / XML / YAML 格式化 + 语法校验 + 压缩 + 转换一体化',
        '严格性约束：禁止 trailing garbage（{{a:1}} {{b:2}} 不误判通过）',
        '时间戳 ↔ 日期互转：秒 / 毫秒 / 微秒 / 纳秒 四档精度',
        'Cron 表达式解析 + 下次触发时间（稀疏表达式正确处理）',
        'JSONPath 查询：深层嵌套 + 过滤器 + 数组切片',
        '纯前端 + 后端双版本：纯前端零网络，后端方便扩展'
      ]
    },
    {
      icon: 'notes', name: 'Windows 原生桌面便笺 (v0.16-v0.18)',
      pages: ['notes'],
      apis: ['/api/notes', '/api/notes/*'],
      pkg: 'internal/desknote + internal/note + internal/winui',
      desc: 'Windows 原生置顶便笺系统：支持 Win32 透明无边框窗口、顶栏快捷文本摘录、便笺中心管理、双击内联编辑、更多菜单自适应穿透与 revision 乐观并发控制。',
      features: [
        'Win32 原生置顶窗口：直接调用 Windows API 创建透明置顶便笺，无需浏览器弹窗或外部宿主',
        '顶栏一键摘录：选中文本后点击顶栏「便笺」按钮，直接创建桌面便笺并填入选中文本',
        '便笺中心列表：集中查看所有便笺卡片，支持多色卡切换、快速标记与双击内联编辑',
        '更多菜单层叠穿透：自适应 top 定位与 isolation 独立层叠上下文，杜绝卡片滚动与后序元素遮挡',
        'revision 乐观并发控制：浏览器端与桌面端窗口同时编辑发生冲突时显式提示，杜绝静默覆盖',
        '桌面可见上限保护：最多同时置顶 6 张便笺，防止屏幕窗口过度杂乱',
        '数据安全持久化：落盘至 data/notes.json，权限显式收紧'
      ]
    },
    {
      icon: 'tasks', name: '定时任务与系统通知 (v0.15-v0.19-dev)',
      pages: ['tasks'],
      apis: ['/api/tasks', '/api/tasks/*'],
      pkg: 'internal/schedtask + internal/cronx + internal/notify',
      desc: '本地与服务器定时任务调度平台：支持 Cron 表达式、间隔与单次触发；Windows 平台由 Job Object 进程树接管，支持失败事件总线与第三方告警联动。',
      features: [
        '任务失败事件总线 (internal/notify)：任务执行失败即刻触发事件总线，推送至界面横幅',
        '多渠道报警集成：支持 Windows 系统托盘弹窗 + 企业微信 / 钉钉 Webhook 告警与测试',
        '多模式触发：Cron 表达式（复用 internal/cronx 跳跃算法）、固定间隔与单次定时执行',
        'Windows 命令行语义：cmd.exe /D /S /C 原始命令行处理，完美保留中文、空格与嵌套引号',
        'Windows Job Object 进程树隔离：恢复线程前完成归组，超时与程序退出自动递归清理完整子孙进程',
        '执行日志与状态回溯：捕获 stdout/stderr 输出，自动识别 UTF-8 与 GBK 编码',
        '任务管理与手动触发：支持在线启用/暂停、编辑参数、立即执行与历史执行记录查看',
        '全流程审计：任务的新增、修改、启停与执行全量写入 logs/audit.log'
      ]
    },
    {
      icon: 'pet', name: 'Windows 原生桌面宠物 (v0.15-v0.16)',
      pages: ['pet'],
      apis: ['/api/pet/status', '/api/pet/enable', '/api/pet/rename', '/api/pet/position', '/api/pet/skin', '/api/pet/sync', '/api/pet/leaderboard'],
      pkg: 'internal/deskpet + internal/pet + internal/winui',
      desc: '原生彩蛋式桌面陪伴宠物：Win32 原生透明精灵渲染，内置 108 款精美皮肤，具备经验成长引擎、互动气泡、拖拽吸边与服务端认可分榜单同步。',
      features: [
        'Win32 原生渲染：透明无边框窗口 + 像素精灵帧动画，拖拽吸边、右键快捷菜单，零 WebView 依赖',
        '108 款精美皮肤：skins.json 单一清单驱动，涵盖武侠、仙侠、像素与萌宠多风格',
        '动画状态机：idle / drag / click / levelup / evolve / skin 六大状态平滑过渡',
        '经验与成长规则：仅对白名单日常运维操作产出成长分，具备操作冷却与单日防刷上限',
        '激活码绑定宠物 ID：由激活码 SHA-256 派生唯一宠物标识，重装程序不丢失等级与榜单排名',
        '隐藏解锁开关：在 About 页面或侧边栏「关于」连续点击 6 次触发解锁建档'
      ]
    },
    {
      icon: 'reminderIc', name: '定时提醒 (v0.13-v0.18)',
      pages: ['reminders'],
      apis: ['/api/reminders', '/api/reminders/*'],
      pkg: 'internal/reminder + internal/popup',
      desc: '一次性 / 每周 / 每月 / Cron 四种触发器；提前 N 分钟；触发动作支持纯 GDI 桌面弹窗 / 打开网址 / 执行本地命令；穿透 Win10/11 专注助手与通知中心拦截。',
      features: [
        '纯 GDI 自绘顶层弹窗：独立 Win32 弹窗绘制，100% 穿透 Windows 10/11 专注助手与免打扰',
        '4 种触发器：once / weekly / monthly / cron（5/6 段表达式，复用 internal/cronx 跳跃算法）',
        '提前 N 分钟（lead_minutes）：如「会前 10 分钟」',
        '触发动作（action）：popup 弹窗 / url 打开网址 / command 执行本地命令（30s 超时 + 审计）',
        '弹窗交互：关闭 / 推迟 (snooze) / 立即触发',
        'Manager 池：定时器在内存里跑，进程重启从 data/reminders.json 恢复',
        '完整审计：每次触发写 logs/audit.log'
      ]
    },
    {
      icon: 'browserIc', name: '浏览器自动打开 (v0.13 起)',
      pages: [],
      apis: ['/api/browser/detect', '/api/browser/open', '/api/browser/reset'],
      pkg: 'internal/browserpref + internal/popup + internal/sysutil',
      desc: 'Kairo 启动时按用户偏好自动打开系统浏览器。Win 下首选 Chrome（注册表 + 常见路径探测），macOS/Linux 走 open / xdg-open。探测结果落 data/browser_state.json，下次启动直接复用；支持 `--reset-browser` 重新探测。',
      features: [
        'Win 浏览器定位：注册表 HKCU\\Software\\Chrome / Edge + 常见路径枚举 (Chrome / Edge / Firefox / 360 / QQ)',
        'macOS/Linux 原生支持：exec.Command("open", url) / exec.Command("xdg-open", url)',
        '偏好持久化：data/browser_state.json 记录 {kind, path, updated_at}，下次启动瞬间复用',
        '--reset-browser 启动参数：清除偏好记录并重新执行环境探测',
        'Popup 弹窗与托盘联动：支持启动弹窗确认与配置中心自由启闭'
      ]
    },
    {
      icon: 'diagnostics', name: '环境自检与诊断中心',
      pages: ['diagnostics'],
      apis: ['/api/diagnostics'],
      pkg: 'internal/diagnostics',
      desc: '环境自检，回答运维三问：我跑得正常吗？我能 SSH 上去吗？我有必要的工具吗？',
      features: [
        'App / Build 分块：Go 版本 / x/crypto/ssh 版本 / 监听地址 / 磁盘可写',
        'Runtime：磁盘 / 监听地址 / 凭据模式 (keyring/file/disabled)',
        'Tools：本机命令工具 (find -printf / grep / sed / tail)',
        'Servers：每台配置的 server 单独一项，DNS 解析 + TCP 端口连通性',
        'Issues 汇总：所有告警置顶展示，结构化错误归类',
        '不暴露密码：自检报告全字段脱敏'
      ]
    },
    {
      icon: 'config', name: '统一升级协调与配置中心 (v0.18+ 持续加固)',
      pages: ['config'],
      apis: ['/api/config*', '/api/admin/servers', '/api/admin/openers', '/api/admin/download-retention', '/api/admin/autostart'],
      pkg: 'internal/config + internal/upgrade + internal/credentials',
      desc: '可视化配置编辑器 + internal/upgrade 跨文件原子升级协调器 + 导入导出 + 热加载 + 凭据管理 + 开机自启。',
      features: [
        '统一升级事务协调器 (internal/upgrade)：跨文件事务快照、多文件单向 N->N+1 链式升级、全量原子回滚',
        '降级与未版本化防护：强制拒绝旧版本程序写入高版本配置文件，保护资产免遭静默污染损坏',
        '可视化编辑：业务系统 / 服务器 / 日志目录 / 数据源 四级配置',
        'YAML 导入导出：版本管理、环境迁移、批量修改与 Schema 强校验',
        '分节 dirty 追踪：未保存改动高亮提示，防止误切页面丢失数据',
        '凭据管理：keyring (OS 钥匙串) / file (AES-256-GCM + AAD) / disabled 三模式',
        '开机自启：Windows 注册表开机启动项在线配置与状态检查',
        'TOCTOU 加固：handler 入口取一次配置快照全程复用'
      ]
    },
    {
      icon: 'commands', name: '常用命令 / 命令收藏',
      pages: ['commands'],
      apis: ['/api/commands*'],
      pkg: 'internal/httpserver',
      desc: '命令收藏 + 模板化执行与执行历史回放。',
      features: [
        '命令收藏夹：命名 + 标签 + 排序与分组管理',
        '模板变量插值：{{server}} / {{path}} / {{date}} 等变量动态填充',
        '受控安全执行：走 sshclient 受控模板，不开放任意危险 shell',
        '执行历史留痕：每次执行记录时间、入参与结果回显，支持一键重放'
      ]
    },
    {
      icon: 'coffee', name: '投喂作者与社区排行 (v0.14 起)',
      pages: ['sponsor'],
      apis: ['/api/sponsor/leaderboard'],
      pkg: 'internal/sponsor + internal/endpointclient + internal/httpserver/handlers_sponsor.go',
      desc: '用户反哺渠道与赞助排行榜：3 品牌（库迪/瑞幸/随缘）二维码 + 18 条幽默文案 + 天命武林榜与续命恩人榜；走 endpointclient 调内部服务，5 分钟内存缓存与安全脱敏。',
      features: [
        '3 大咖啡品牌：库迪 (9.9 续命首选) / 瑞幸 (小蓝杯) / 随缘 (给啥喝啥)，选中态放大与彩色指示器',
        '18 条幽默文案：同事间玩笑感，不乞讨，提升日常运维趣味性',
        '双榜单呈现：天命武林榜骨架 + 续命恩人榜前 10 名三色奖杯 SVG 呈现',
        '5 分钟本地缓存：内存缓存减轻服务压力，接口异常时给出搞笑 quip 降级，内部地址零泄漏',
        '基座共享：internal/endpointclient 统一封装主备切换、超时与错误归一化'
      ]
    }
  ];

  // =====================================================================
  // §11. 横向对比 — Kairo vs 传统工具栈
  // =====================================================================
  const comparison = [
    { dim: '数据库日常排查与核对 (Oracle/MySQL/Redis)', traditional: 'PL/SQL Dev 慢 / Navicat 收费 / DBeaver 内存大，各开一个窗口', kairo: 'database 页 → 对象树秒级定位 + 字段一键复制 + 单条只读保护 + 流式分批 + 单记录卡片', win: '零客户端安装，安全可控' },
    { dim: '日均 50 次 SSH 登录 + 查日志', traditional: 'SecureCRT 开 5 个标签 + 复制粘贴路径 + cat + grep', kairo: '浏览器 /websphere 一页，三级目录展开，关键词走受控模板，30 行上下文直出', win: '省 80% 重复操作' },
    { dim: '从 5 台机器拉最近日志', traditional: 'WinSCP 一台一台连 → 找路径 → 拉 → 手动归档', kairo: '勾选 5 个 server + 选最近 3 个文件 → 后台并发 + SSE 进度 + 按 YYYYMMDD 自动归档', win: '5 台从 8 分钟 → 30 秒' },
    { dim: '配置对比 (生产 vs 预发)', traditional: 'Beyond Compare 单独开 + 手动 export + 选两边文件', kairo: 'compare 页 → 统一多协议扫描 → Merkle 哈希树差异图 → 一键 unified diff', win: '万级文件 < 500ms' },
    { dim: '调一个内网 HTTP / WS 接口', traditional: 'Postman 开 + 配 baseURL + 加 header + 复制 curl', kairo: 'http 页 → curl 一键解析 + 用例收藏 + 环境变量模板 → 一键重放 + 历史回溯', win: '零客户端启动' },
    { dim: '运维巡检 (10 台 SSH 通不通)', traditional: '一台一台 ping + ssh 试，连不上再查防火墙', kairo: '诊断中心一键自检：DNS / TCP 端口 / 工具 / hostkey 全部汇总', win: '10 台从 20 分钟 → 10 秒' },
    { dim: '凭据管理', traditional: '记事本 / Excel / KeePass，散落各处', kairo: 'OS 钥匙串按 (system,server,user) 三元组加密，零明文落盘', win: '合规审计可过' },
    { dim: '操作审计', traditional: '没有 / 靠 shell history', kairo: 'logs/audit-YYYY-MM-DD.log 滚动 + /api/audit/* 导出', win: '满足等保' },
    { dim: '跨平台部署', traditional: 'WinSCP 在 macOS 难用 / SecureCRT 要付费 / Postman 体积大', kairo: '单二进制 12MB，macOS / Linux / Win 10/11 一份走天下', win: '分发成本 → 0' },
    { dim: '老 sshd (OpenSSH 5.x / 6.0 / AIX)', traditional: '要手动降级客户端 + 配置 KEX + 试错', kairo: '5 套 SSH profile 自动 fallback，握手 45s 外层 timeout', win: '老设备开机即用' },
    { dim: 'GBK 编码日志 (老 WebSphere / Oracle)', traditional: 'SecureCRT 切编码 + 复制出来再 iconv', kairo: 'golang.org/x/text 透明转换，直读不乱码', win: '所见即所得' },
    { dim: '调试老 SOAP / WebService 接口', traditional: 'SoapUI 体积大 + WSDL 解析弱 + Mock 难配', kairo: 'webservice 页 → URL/文件导入 WSDL → 自动生成 Envelope → 一键发送 + 模板 + 历史 + Mock', win: 'SoapUI 替代品 (浏览器内)' }
  ];

  // =====================================================================
  // §12. 故障案例库 — 从 commit log 提取的真实 bug 复盘
  // =====================================================================
  const bugStories = [
    {
      id: 'WinUI-001', version: 'v0.18', severity: 'P0', title: 'Windows 选文件夹并发触发 COM 冲突 — ERR_CONNECTION_RESET 或进程崩溃',
      symptom: '用户在文件比对或 WAS 打包点击“浏览选择文件夹”时，偶发浏览器连接瞬间重置 (ERR_CONNECTION_RESET)，后端进程甚至静默退出。',
      rootCause: 'HTTP Handler 工作线程直接调用 Win32 IFileDialog，该线程未初始化 STA/OleInitialize；且多请求并发调起原生对话框时引发 COM 线程模型冲突与致命未捕获异常。',
      fix: '在 winui 模块建立常驻 STA UI 宿主主线程；所有 choose_dialog 调用统一路由至 host 线程调度，外层包裹 recover 兜底，并增加 pickerOwner 主窗口句柄防抢焦。',
      lesson: 'Windows 原生 GUI 交互绝不能在 Go 随意并发的 goroutine/线程中裸跑，必须通过常驻 STA 宿主管道集中排队与异常隔离。'
    },
    {
      id: 'Upgrade-001', version: 'v0.18', severity: 'P0', title: '跨文件配置升级缺乏原子边界 — 进程异常中断致版本撕裂与数据损坏',
      symptom: '用户在跨大版本升级或资产迁移时，若遭遇断电、强杀进程或某单个配置文件校验失败，部分文件已升级而其余未升级，导致程序重启后报数据损坏。',
      rootCause: '旧版各个 store (servers/notes/tasks/db) 分别在加载时自行升级并覆写磁盘，缺少全局事务协调器与全局快照保护机制。',
      fix: '引入 internal/upgrade 统一升级协调器：升级前先完整制作 pre-upgrade 备份快照；严格执行 N->N+1 链式迁移契约；全部成功后原子落盘 upgrade-state.json；任意步骤失败全量快照原子回滚，并严格拒绝跨大版本逆向降级。',
      lesson: '持久化资产升级必须有跨文件事务边界 (transaction boundary)，容错设计的底线是“要么全成功，要么完好如初”。'
    },
    {
      id: 'Toast-001', version: 'v0.18', severity: 'P1', title: 'Windows 10/11 定时提醒被系统专注助手静默吞噬 — 告警丢失',
      symptom: '用户配置了重要巡检与定时提醒，在 Windows 10/11 系统上到点后没有任何弹出反馈，导致关键运维动作被遗漏。',
      rootCause: '旧版依赖 PowerShell 调用 WinRT Toast 通知中心；在 Windows 开启专注助手 (Focus Assist)、全屏应用或用户未给该 AppUserModelID 授权时，系统会静默拦截通知不弹窗。',
      fix: '实现 internal/popup 纯 GDI 原生顶层窗口 (popup_windows.go)：绕过 Windows 通知中心，直接通过 Win32 CreateWindowEx/SetWindowPos 创建物理顶层弹窗与倒计时关闭。',
      lesson: '对生产运维关键告警而言，可靠性高于“系统原生外观”；受第三方策略和系统免打扰影响的管道不可作为唯一路径。'
    },
    {
      id: 'DB-002', version: 'v0.18', severity: 'P1', title: 'Oracle 分页暴露内部 ROWNUM 别名列 & 多分号致空语句报错',
      symptom: '用户在 Oracle 数据源执行分页查询时，结果表格混入了无业务意义的 RNUM 列；同时若在 SQL 末尾输入了两个以上连续分号，编辑器会弹空语句报错。',
      rootCause: '后端包装 Oracle ROWNUM 分页时在外层使用了 SELECT *，将内层别名原样投出；前端 statementModel 简单以分号分割，导致末尾空串被当作独立语句下发。',
      fix: '后端分页查询层增加 Oracle 辅助别名列自动过滤脱敏；前端 statementModel 增加多分号规范化逻辑，并在执行前自动剥离尾部空语句。',
      lesson: '方言兼容层不仅要让语句能跑通，还要对框架注入的辅助构造进行透明遮蔽，保持用户结果集的纯粹性。'
    },
    {
      id: 'JobObject-001', version: 'v0.16', severity: 'P0', title: 'Windows 定时任务超时后孙进程残留 — 孤儿进程隐患',
      symptom: '用户在 Windows 下执行包含管道或外部调用的定时任务，任务超时后直接子进程被 kill，但孙进程继续占用端口和 CPU，程序退出后仍有残留。',
      rootCause: 'os/exec 默认仅能向直接启动的子进程发送终止信号，Windows 下通过 cmd.exe 调起的二级孙进程脱离了进程组控制。',
      fix: '引入 Windows Job Object (作业对象)：在恢复进程主线程前将目标进程归组到 Job Object，超时或 Kairo 退出时通过 TerminateJobObject 彻底递归销毁完整进程树。',
      lesson: '跨平台进程管理不能假定 POSIX 信号语义在 Windows 上等价，必须深入底层 OS API 进行进程生命周期兜底。'
    },
    {
      id: 'Diff-001', version: 'v0.17', severity: 'P0', title: '目录比较仅靠 size+mtime 判定 — 相同大小不同内容文件漏报',
      symptom: '用户在文件比对时，两个同名文件大小相同、修改时间相同但中间几行内容不同，极速模式将其错误判定为“相同”，导致线上补丁遗漏。',
      rootCause: '旧实现为了追求秒级完成万级文件扫描，仅对比了元数据中的 Size 和 ModTime，忽略了时间戳被覆盖或内容等长修改的极端场景。',
      fix: '将默认比对模式切换为“智能内容散列”：本地与远端并发计算内容摘要，确保 100% 准确性；极速元数据模式降为显式可选并标注漏报风险。',
      lesson: '快速不能以漏报为代价；准确性是比较工具的第一生命线，性能优化必须在准确性闭环的前提下进行。'
    },
    {
      id: 'DB-001', version: 'v0.17', severity: 'P1', title: 'SQL 执行错误仅依赖短暂 Toast 提示 — 无法定位复杂长报错',
      symptom: '用户在数据库工作台执行包含复杂关联查询的 SQL 失败时，Oracle ORA 报错仅在右上角 Toast 弹窗 3 秒后消失，用户来不及阅读、无法复制错误堆栈。',
      rootCause: '早期设计将所有非网络异常统一丢给 core.toast()，忽略了数据库 SQL 报错通常篇幅较长且需要比对原 SQL 位置的交互特点。',
      fix: '重构结果区状态机：在编辑器下方建立持久可见的 status/error/grid/record 四种状态，SQL 失败后固定展示错误标题、完整原因、恢复建议与原 SQL 展开。',
      lesson: '错误是查询结果的一部分，排障关键信息必须持久驻留在上下文工作区，不能当作瞬时通知对待。'
    },
    {
      id: 'BUG-3', version: 'v0.9.0', severity: 'P0', title: 'preview.html 读取已失效凭据 — 文件预览空白',
      symptom: '用户在文件浏览器打开 preview.html（新窗口模式）预览远程文件，预览页因读取了错误的 activeCred 变量，拿到的是上一次会话的密码字符串而非有效凭据对象，导致 SSH 连接立即 auth fail。',
      rootCause: 'preview.html 是独立 HTML 窗口，与主页面 Kairo.state 不共享。它直接从 localStorage 反序列化 activeCred，但 activeCred 是个内存对象（含 password 字段的引用），序列化丢失了密码。',
      fix: 'preview.html L185 改读 activeCred → 调用 Kairo.state.activeCred 取真实内存对象；后端 handler 同步把凭据逻辑改成"先读 activeCred 再走 fallback"',
      lesson: '跨窗口状态共享要么走 window.opener.*，要么走 BroadcastChannel，不能盲目 localStorage 序列化内存对象'
    },
    {
      id: 'BE-005', version: 'v0.9.0', severity: 'P0', title: 'SSH Dial 默认允许中间人攻击',
      symptom: '用户配置新 server 时忘记配 host_key_sha256，Kairo 仍走 InsecureIgnoreHostKey 接受任意 host key → 攻击者可在内网 ARP 欺骗 + 自签证书，截获所有 SSH 操作',
      rootCause: 'v0.8 之前 AllowInsecureHostKey 默认是 true（为了兼容老配置），但新部署未显式改 false 就会有此漏洞',
      fix: 'fail-closed：未配 host_key_sha256 + allow_insecure=false → Dial 立即拒绝，不发起任何网络连接；只有显式 allow_insecure_host_key: true 才退回 InsecureIgnoreHostKey',
      lesson: '安全选项的默认值必须是安全侧，兼容性是显式 opt-in，不是隐式 fallback'
    },
    {
      id: 'BE-008', version: 'v0.9.0', severity: 'P1', title: '含 ".." 子串的合法文件名被误拒',
      symptom: '用户下载 SystemOut..20260628.log（这是 WebSphere 老版本日志命名约定），被 hasPathTraversal 误判为路径穿越攻击，拒绝下载',
      rootCause: '旧实现 strings.Contains(name, "..") 太粗暴，my..file.log / test..bak 这种合法命名也会被命中',
      fix: '升级为 filepath.Clean + Rel 双重校验：先规范化路径，再用 filepath.Rel(root, path) 判断是否真的逃出 root 目录，覆盖 14 种攻击向量但不误伤合法命名',
      lesson: '安全检查必须区分"包含危险子串"和"真的是攻击"，正则黑名单 → 语义分析'
    },
    {
      id: 'BE-019', version: 'v0.9.0', severity: 'P1', title: 'tail SSE 双重 done 事件',
      symptom: '用户停止 tail 后，前端 EventSource 立即关闭 + 后端 markDone 时又广播一条 done → 前端收到两条 done 信号，第一条还没处理完第二条就来了，UI 闪一下',
      rootCause: 'markDone 和 pushImmediate 走了两条路径，都向 subscribers 广播 done 事件',
      fix: 'BE-019：结束消息 markDone 前由 setDoneMsg 设置 doneMsg，不再走 pushImmediate 广播；handler 的 !open 分支通过 DoneMsg() 读取 doneMsg 作为 SSE done 事件的 data',
      lesson: '事件源必须单一写入，不能多个 channel 推同一语义的事件'
    },
    {
      id: 'BE-013', version: 'v0.9.0', severity: 'P1', title: '凭据密文格式升级后旧密文无法读取',
      symptom: '用户从 v0.8 升级到 v0.9，credentials.json 里的旧密文（AES-GCM 但没绑 AAD）被新代码拒绝，提示"凭据损坏请重新输入"',
      rootCause: '旧版 AES-GCM 密文只包含 nonce + ciphertext，新版为了防替换攻击加绑 AAD（system|server|user 三元组），旧密文没有 AAD → 认证失败',
      fix: '读取时检测密文格式：无 AAD 的旧密文 → 一次性解密并用新格式重写（migrate-on-read）；AAD 缺失则强制用户重新输入',
      lesson: '加密格式升级必须配合 migrate-on-read，不能 silent drop 用户数据'
    },
    {
      id: 'BUG-5', version: 'v0.9.0', severity: 'P0', title: '下载历史页 8 个问题 (attempt 3 retry)',
      symptom: '下载历史页筛选不响应、预览打不开、布局错位、性能卡顿；用户连续反馈 3 轮',
      rootCause: 'v0.8 上线时赶工 + 测试覆盖不足：筛选器没防抖、预览按钮传错 ID、CSS Grid 在窄屏塌陷、List 接口没分页',
      fix: 'attempt 3 一次到位：筛选防抖 200ms / 预览走 dlmanager.Session.ID / 改用 Flexbox / List 接口加分页参数',
      lesson: 'P0 修复不能分批，一次性 8 个全改再发版（用户偏好全改，不分批）'
    },
    {
      id: 'BUG-3 (回归)', version: 'v0.9.0', severity: 'P0', title: 'preview.html L185 改读 activeCred — 回归修复',
      symptom: '尝试 3 后 BUG-3 修复不彻底，preview.html 仍然读 localStorage 里的 activeCred 副本',
      rootCause: 'preview.html 是独立 HTML，它和主页面 Kairo.state 不共享；上次修复只改了主页面，但没改 preview.html',
      fix: 'preview.html L185 显式改读 Kairo.state.activeCred（通过 window.opener 拿主页面状态）',
      lesson: '回归修复必须把所有受影响页面列全，独立 HTML 窗口是常见遗漏点'
    },
    {
      id: 'OpenSSH 6.2p2', version: 'v0.4.x', severity: 'P0', title: '老 sshd 握手 RST — 排查 3 天',
      symptom: '用户报"连不上测试环境 sshd 6.2p2"，x/crypto 报 "ssh: handshake failed: ssh: no common algorithms"；同账号用 SecureCRT 正常',
      rootCause: 'OpenSSH 6.2p2 默认 KEX 是 diffie-hellman-group-exchange-sha1，老但仍可用；x/crypto 默认从高到低协商，碰到 ECDH_INIT 后老 sshd 直接 RST',
      fix: '拆 5 套 SSH profile：modern / compat (默认) / no-ecdh / legacy / auto；外层 sshDialOuterTimeout = 45s (3×12s + buffer)；握手失败自动 fallback 到下一套；详细排查过程写在排查总结-6.2p2连接问题-2026-06-22.md',
      lesson: '老 sshd 兼容性是科学问题不是工程问题，必须有 fallback 链 + 详细日志'
    }
  ];

  // =====================================================================
  // §13. 常见问题 (FAQ)
  // =====================================================================
  const faq = [
    { q: 'Kairo 是给谁用的？', a: '内网运维工程师 / SRE / DevOps / 数据库开发。需要登录多台 SSH 服务器、查日志、拉文件、对比配置、调内网 HTTP/SOAP 接口、核对数据库的"日常工程师"。无需复杂环境配置即可上手。' },
    { q: '数据库工作台支持哪些数据库？只读策略怎么保证安全？', a: '当前已深度适配 Oracle 11g (纯 Go thin driver)、MySQL 5.7/8.x (database/sql 连接池) 与 Redis (单机/Cluster/Sentinel)。安全上坚持三道防线：(1) 后端词法与 AST 校验强制单条只读 SQL，严禁多语句与写操作；(2) 强制单次查询超时、最大行数与返回体硬上限；(3) Redis SCAN 游标分页并对超大 Key 实施安全有限读取。' },
    { q: '原生桌面便笺和桌面宠物需要安装额外运行库吗？', a: '完全不需要。桌面便笺与桌面宠物基于 Win32 原生 API 实现透明无边框窗口、拖拽吸边与像素精灵渲染，不依赖 WebView2、Electron、PowerShell 或任何第三方运行库，双击单二进制即可直接运行。' },
    { q: '它和 SecureCRT + WinSCP + Postman + DBeaver 比有什么优势？', a: '四大优势：(1) 单一二进制 + Web UI + 原生桌面组件，跨平台一致体验；(2) 受控操作与只读策略，安全可审计；(3) 内置 OS 钥匙串凭据管理 + 下载历史 + 诊断中心 + 投产打包，是一个完整的生产力工具箱而不是多个工具拼凑；(4) 零 npm 运行时、零构建依赖。' },
    { q: '为什么不直接用 Ansible / Jenkins？', a: '定位完全不同。Ansible / Jenkins 是自动化平台，跑任务编排；Kairo 是工程师的"快进键"，跑交互式操作。两者互补，不是替代。' },
    { q: '需要安装吗？', a: '零安装。下载一个二进制文件（Windows 10/11、macOS、Linux）双击即跑，自动打开浏览器。无需 Python / Node / .NET 运行时。' },
    { q: '支持哪些操作系统？', a: '主线支持 macOS (Apple Silicon / Intel)、Linux (x86_64) 与 Windows 10/11。主线使用 Go 1.24+，Windows 7 转入 legacy 分支独立维护；原生宠物、桌面便笺、托盘与 Toast 属于 Windows 专属能力。' },
    { q: '支持 SSH 跳板机 / 堡垒机吗？', a: '支持。sshclient 包支持多跳代理配置（ProxyCommand / ProxyJump）；老堡垒机的 keyboard-interactive 认证也支持（v0.4 起加固）。' },
    { q: '密码存在哪里？安全吗？', a: '三种模式：(1) keyring（默认）→ macOS Keychain / Windows DPAPI / Linux Secret Service；(2) file → AES-256-GCM 加密本地文件 data/credentials.json，密钥绑 AAD 防止替换攻击；(3) disabled → 不持久化，每次手动输入。密码永不写进 audit.log / URL / 错误信息。' },
    { q: '怎么保证我不被中间人攻击？', a: 'SSH host key 默认强校验（fail-closed）。每台 server 在 config.yaml 配 host_key_sha256，未配 + 未显式允许 insecure → Dial 立即拒绝，不发起网络连接。' },
    { q: '日志乱码怎么办？', a: '工具自动识别 UTF-8 / GBK / GB18030 / GB2312（老 WebSphere / Oracle / AIX 常见 GBK）；encoding 字段可手动指定。如果还有乱码，截图发 issue。' },
    { q: '下载到一半断网会怎样？', a: '当前下载项标记为"未完成"并保留半成品文件（.partial 后缀）；其他已完成项不受影响。重连后可手动重试整个下载任务。' },
    { q: '能上传文件到远程吗？', a: '设计上受控只读优先。SSH 终端内嵌 SFTP 提供受控上传能力（仅支持明确确认的运维部署），普通文件浏览器只读 + 下载以避免误删/越权。' },
    { q: 'WebService 调试中心是干什么的？(v0.12 起)', a: '面向老 Java / WebSphere / XFire / SOAP 场景的轻量 SoapUI。导入 WSDL（URL 或上传 .wsdl/.xsd）→ 自动生成 SOAP Envelope → 自定义 endpoint/Headers/Body 发送 → 保存模板 → 历史回放 → 起 Mock 服务端。内置 XML 格式化小工具。适合内网老 SOAP 接口调试，不必再装 SoapUI。' },
    { q: 'WebService 代码生成怎么选 JDK？为什么不直接用本机默认 JDK？', a: '默认 JDK（PATH 上那个）常常是 11/17，生成的 JAX-WS 2.2 代码丢进 JDK 6 工程会缺类；XFire / Axis 还必须用工程 lib 里那一套 jar 跑 wsdl2java，否则 import 对不上。正确做法：选项目目录扫描 jar，再选工程自带的 JDK 6/8。不确定栈时用 portable（纯 HttpURLConnection，Java 1.6，零依赖）。' },
    { q: '老 Chrome / Win7 内网浏览器打不开 SSH 终端？', a: '已全量加固：xterm.js 5.5+ 的 ES2020 语法转译到 ES5；replaceChildren / FileReader 等 DOM API 在 core.js 内置 polyfill；文件读取走 FileReader 而非 File.text()。若仍报错，硬刷新一次（Ctrl/Cmd+Shift+R）清缓存即可。' },
    { q: '想贡献代码 / 反馈 bug？', a: '所有 issue / PR 在 GitHub 仓库；反馈 bug 请附 (1) Kairo 版本号 (2) 操作系统 (3) 目标服务器/数据库版本 (4) 完整操作步骤 (5) logs/ 下最新日志。' }
  ];

  // =====================================================================
  // §14. 路线图
  // =====================================================================
  const roadmap = {
    planned: [
      { name: 'PostgreSQL 与国产数据库支持（v2）', desc: '数据库工作台扩展 PostgreSQL、达梦 (DM) 与人大金仓 (Kingbase) 只读驱动、元数据与执行计划适配。' },
      { name: '宠物技能/对战扩展（v2）', desc: '在当前 v1 基础上保留技能与对战命名空间，逐步开放交互接口、界面入口与对战快照。' },
      { name: '审计中心升级', desc: 'audit.log → SQLite 索引，支持按 user/op/server/time 检索与导出 CSV (v0.9 RBAC 已落地，下一步把审计数据做成可查询)' },
      { name: '容器化分发', desc: 'Docker 镜像 + docker-compose，CI/CD 一键起' },
      { name: '更多兼容矩阵', desc: 'OpenSSH 4.x / Tandem / HP-UX 等老 sshd 适配' },
      { name: '插件系统', desc: '命令模板 / 格式化器走 go-plugin 扩展位' }
    ],
    considering: [
      { name: '多用户 / 多租户隔离 (v0.9 已部分落地)', desc: 'v0.9 已实现 role=admin/user RBAC + IP 白名单，下一步考虑只读 / 普通 / 管理员 三档精细化 + 用户自助注册' },
      { name: 'P2P 内网穿透', desc: '无网环境 / 隔离网段的工具箱访问' },
      { name: '移动端适配', desc: 'iPad / 触屏优化布局' },
      { name: 'AI 助手', desc: '日志异常自动告警 / 根因分析 (本地 LLM)' },
      { name: '国际化', desc: 'en-US / ja-JP 多语言 (现已完成 rebrand 准备)' },
      { name: '云端同步', desc: '配置 + 收藏 + 审计日志端云同步' }
    ]
  };

  // =====================================================================
  // §13. 版本演进史 (20 个版本, 含 v0.19-dev, accordion 折叠)
  // =====================================================================
const changelog = [
    {
      version: 'v0.19-dev',
      date: '2026-09-09',
      tag: 'v0.18 后续开发 · 工作台深化 · 安全边界收紧 · 性能与稳定性优化',
      codename: 'Sentinel · 守界加固',
      size: 'xl',
      headline: '本卡记录 v0.18 发布后的连续开发工作：数据库从“可查询”继续走向可控写入与大字段工作流；文件比对、WAS 打包和代码生成补齐边界校验；同时收紧 API、会话、路径、错误信息与 Webhook 的安全和可靠性。当前根目录 VERSION 仍为 v0.18，v0.19-dev 尚未作为正式发行版发布。',
      stats: { commits: 5, fixes: 16, additions: 11, breaks: 0 },
      principles: [
        '写操作必须可预览、可确认、可回滚：网格编辑、批量导入与脚本执行统一经过会话、确认和事务边界。',
        '大字段与大结果必须受控：CLOB/BLOB、虚拟投影和流式下载不能因为一次预览把整份数据拉入内存。',
        '任何本地路径都不能默认可信：数据库导出、文件比对和 WAS 产物输出统一做路径归一化、越界拦截与敏感目录拒绝。',
        '异步任务必须可观察且可取消：扫描、同步、打包和通知都要有明确状态、超时与失败反馈。',
        '错误信息对用户有用但不能泄露凭据：审计和 API 响应保留根因，隐藏 DSN、密码与内部敏感路径。',
        '兼容性优化必须有回归证据：前端工作台单元测试、Go 回归测试和真实数据库验收同步扩充。'
      ],
      architecture: {
        layers: [
          { name: '数据库写工作台层', detail: 'internal/dbconsole 与 database.js 扩展网格编辑、单记录编辑、批量导入、脚本执行、事务提交/回滚和对象工作流，所有写入按数据源会话隔离。' },
          { name: 'LOB 与大结果受控层', detail: 'CLOB/BLOB 采用预览、令牌和完整下载分离的模型；查询结果限制单元格、总字节与行数，避免大字段或海量换行导致内存峰值。' },
          { name: '比对并发与来源版本层', detail: '比较任务对来源变更、截断、未完成目录和过期 job 做显式标记；保存前要求成功读取得到的版本信息，防止旧内容覆盖新文件。' },
          { name: 'WAS 输出与 provenance 层', detail: '输出目录共享 fail-closed 路径策略；归档读取校验常规文件与文件身份，历史清理受 manifest 总量预算约束，阶段 provenance 使用随机 nonce。' },
          { name: 'API 会话与错误治理层', detail: '数据库会话绑定请求上下文，敏感系统配置脱敏返回；比较、导出、代码生成与工具接口增加权限、来源、并发槽位和响应体边界。' },
          { name: '通知与生成稳定性层', detail: 'Webhook 校验业务成功码与响应上限；WSDL 代码生成补齐 timeout 配置、相对 WSDL 定位与并发写入保护。' }
        ],
        retirements: [
          '移除数据库写接口直接信任前端 session ID 的路径，改为服务端请求范围绑定。',
          '移除本地输出目录只做字符串判断的路径策略，统一经过跨平台敏感路径与项目边界校验。',
          '移除大字段默认整块读取和归档阶段无身份校验的文件打开方式。',
          '移除 Webhook 仅看 HTTP 200、不检查企业微信/钉钉业务错误码的误报路径。'
        ]
      },
      features: [
        { title: '数据库网格写入与批量导入', desc: '支持单元格/单记录编辑、批量 mutation、CSV/结构化数据导入、映射预览、逐行结果和显式提交/回滚；失败时保留可操作的结果上下文。' },
        { title: 'CLOB/BLOB 与大结果工作流', desc: '大字段以类型、大小、截断状态和下载令牌呈现，支持在线预览、复制预览、十六进制检视、完整下载与受控分页，避免一次性读取全部内容。' },
        { title: '数据库会话安全与错误脱敏', desc: '写操作和脚本执行绑定请求范围会话；审计保留操作结果但隐藏 DSN、密码和内部敏感信息；未认证的非本机 API 访问默认拒绝。' },
        { title: '比较任务状态一致性', desc: '来源输入变化会使扫描结果失效并取消旧任务；目录未完整加载、结果截断和类型冲突都会阻止误保存或误同步。' },
        { title: '跨协议本地文件安全读取', desc: 'Local/SFTP/FTP/FTPS 比较继续复用统一 RemoteFS 抽象；本地敏感路径、符号链接、并发槽位和请求体大小统一受控。' },
        { title: 'WAS 打包输出与历史治理', desc: '输出路径跨平台 fail-closed 校验，归档阶段校验文件身份与常规文件类型，历史 manifest 增加总量预算，stage provenance 使用不可预测随机 nonce。' },
        { title: 'WSDL 代码生成稳定性', desc: '生成的 JAX-WS/兼容客户端补齐连接与请求 timeout，保留 WSDL system ID 解析相对 XSD，并对并发生成输出加锁。' },
        { title: 'Webhook 失败可观测性', desc: '企业微信/钉钉响应体受大小限制，HTTP 200 但 errcode 非零时也报告业务失败，避免通知“已发送”却实际丢失。' },
        { title: '前端异步与输入状态稳定性', desc: '代码生成页面在路由销毁、输入法组合输入和异步响应返回时保持状态一致；比较页面对来源变更、过期请求和大行数输入做显式保护。' },
        { title: '文件操作输出安全', desc: '文件名、下载错误和结果提示统一转义；SFTP 新建文件改为 exclusive create，避免“新建”误覆盖已有文件。' },
        { title: '工作台回归测试扩充', desc: '新增数据库写入、LOB、会话隔离、比较后台任务、WAS 历史/路径与前端工作台交互测试，覆盖真实失败边界。' }
      ],
      fixes: {
        p0: [
          '修复数据库写接口可跨请求复用 session ID 的会话越界风险，改为服务端请求范围绑定并统一错误脱敏。',
          '修复 WAS、文件比较和导出路径在不同平台下可能指向系统敏感目录或越出允许范围的问题。'
        ],
        p1: [
          '修复 CLOB/BLOB 与超大查询结果默认完整读取造成的内存峰值，改为预览、令牌和流式下载。',
          '修复比较来源修改后仍可使用旧扫描结果保存/同步的问题，增加序列号、版本和完整性状态校验。',
          '修复归档构建阶段文件被替换或软链接指向外部目标时仍被读取的问题。',
          '修复 Webhook 返回 HTTP 200 但业务 errcode 失败时被误判为成功的问题。',
          '修复 WSDL 相对 XSD 导入丢失 system ID，以及代码生成并发写入可能互相覆盖的问题。',
          '修复未认证 API 绑定非 loopback 地址时仍可被远端访问的边界问题。',
          '修复 SFTP 新建文件通过空上传可能覆盖已有文件的问题，改为服务端 exclusive create。'
        ],
        p2: [
          '优化数据库查询分批与导出路径，降低大结果集的临时分配和峰值内存。',
          '统一比较任务的取消、截断、失败和空结果提示，减少前端状态歧义。',
          '限制历史 manifest、Webhook 响应和比较输入体积，避免异常数据无限增长。',
          '补齐 Redis 受控读取/TTL mutation、Oracle 复杂字段和对象详情的回归覆盖。',
          '收紧代码生成引擎、WAS 历史接口和本地工具接口的权限与参数校验。',
          '修复文件名、下载错误和任务错误状态进入 unsafeHtml 时的输出转义问题。',
          '限制代码比对单侧行数，避免极端换行输入造成不必要的切片分配。'
        ]
      },
      commits: [
        { hash: '0a3a440', msg: 'feat(workbench): 完善数据库、比对与打包工作台并修复数据一致性问题' },
        { hash: 'e874953', msg: 'feat(database): 批量原子提交与会话安全加固' },
        { hash: 'b974403', msg: 'feat(workbench): 优化数据库查询内存性能与批量打包路径规范' },
        { hash: 'c9c4b4e', msg: 'fix(security): 收紧数据库、比对与打包权限边界' },
        { hash: '4b848da', msg: 'fix(waspack): 跨平台拦截敏感输出路径' }
      ],
      performance: [
        { label: '大结果查询', before: '整批结果更容易产生高峰内存', after: '分批读取、受控行数与字段体积', improve: '峰值内存更可控' },
        { label: '大字段查看', before: '预览与完整内容边界不清', after: '预览 / 令牌 / 流式下载分离', improve: '避免无意拉取全文' },
        { label: 'WAS 输出安全', before: '平台差异可能漏掉敏感路径', after: '统一跨平台 fail-closed 校验', improve: '拒绝策略一致' },
        { label: '比较保存正确性', before: '旧扫描结果可能覆盖新来源', after: '来源序列、版本和完整状态联动', improve: '过期结果不可写入' }
      ],
      breaking: [],
      migration: [
        '这是 v0.18 发布后的开发记录，不代表正式 v0.19 已发布；当前运行时版本仍由根目录 VERSION 提供，为 v0.18。',
        '数据库新增写入、导入和脚本能力均需显式会话与确认；旧版只读配置不会被自动改写。',
        '未启用认证时，API 继续只建议绑定 loopback；绑定非本机地址时将被服务端拒绝。',
        'WAS 输出目录、文件比较和导出路径现在会拒绝系统敏感目录、UNC 路径和越界路径；请改用明确的工作目录。',
        '若继续开发 v0.19，正式发版前需要把本卡的工作区改动收敛、更新 VERSION/README，并重新执行 release 门禁。'
      ]
    },
    {
      version: 'v0.18',
      date: '2026-09-04',
      tag: '工作台重构闭环 · 跨文件升级原子协调 · 19 项深度修复 · 全量自动化测试',
      codename: 'Nexus · 稳健跃升',
      size: 'xl',
      headline: '全面闭环数据库与比对工作台交互体验，引入光标语句提取、真实流式分页协议与断线透明重试；上线 internal/upgrade 统一升级协调器，提供全量快照、原子事务与降级防御；集中修复 19 项多主题/高低分辨率/Windows 原生交互问题，全量前端工作台单元测试与 Go 质量门全绿护航。',
      stats: { commits: 11, fixes: 28, additions: 22, breaks: 0 },
      principles: [
        '数据库多语句必须语义隔离：执行光标所在当前语句，多重尾部分号智能规整，杜绝批量写或语句拼接歧义。',
        '大结果集分页受控，辅助列不可见：分页协议严格支持 page/page_size/has_next/ordered，Oracle ROWNUM 别名列自动从前端结果剥离。',
        '网络抖动不应打断分析流：SQL 执行与元数据提取支持断线透明重连，幽灵 Tab 孤儿态资源会话自动回收。',
        '比对编辑即时反馈，防止旧结果覆盖：常驻语法高亮编辑器，比对结果响应式高度收缩，实时 diff 状态防竞态重叠。',
        '系统原生交互绝不引起进程崩溃：Windows 文件夹选择对话框经宿主 UI 线程 OleInitialize 调度并增加 recover 兜底，杜绝 COM 线程模型冲突引发的连接重置。',
        '关键提醒不能被系统专注助手静默吞噬：自绘 GDI 独立弹窗窗口替代 PowerShell Toast，确保运维关键告警 100% 直达桌面。',
        '升级与迁移必须具备跨文件事务边界：所有用户配置与资产升级前完整快照，单文件链式迁移失败全量原子回滚，且强制拒绝降级覆写。',
        '打包输出策略明确隔离，保护既有资产：严格模式拒绝非空目录，清理模式只重做当次包名产物，强制覆盖必须二次确认。',
        '视觉一致性覆盖全主题全分辨率：深浅/武侠/高对比度主题统一保证对比度与可读性，多级菜单在各种嵌套布局下绝不遮挡。',
        '质量保证必须由自动化测试说话：新增工作台前端单元测试集 (workbench-unit-tests.js) 与后端 Go 测试，严禁依赖手工口头承诺。'
      ],
      architecture: {
        layers: [
          { name: '统一升级协调层', detail: 'internal/upgrade 建立跨文件事务边界，在升级前拍摄用户配置全量快照；N->N+1 链式迁移每步核验契约，记录 upgrade-state.json，遇到异常全量回滚并强制拒绝降级覆写。' },
          { name: '工作台分页与执行层', detail: 'internal/dbconsole 与 database.js 建立 statementModel 光标语句提取模型，分页支持 page/page_size/has_next/ordered 协议，Oracle 自动剥离 ROWNUM 别名列，断线支持透明重试一次。' },
          { name: 'Redis 受控治理层', detail: 'Redis 命令行建立只读扩展白名单 (TYPE/TTL/HGET/HSCAN/LRANGE 等)，只读模式作为默认安全底线，严格审查受控 TTL mutation (EXPIRE/PEXPIRE/PERSIST)。' },
          { name: '比对常驻高亮视口层', detail: 'compare.js 与 syntax-editor.js 提供常驻语法着色与响应式高度收缩，实时比对消除异步覆盖竞态，历史堆栈操作支持平滑撤销重做。' },
          { name: 'Windows UI 宿主调度层', detail: 'choose_dialog_windows.go 建立常驻 STA/OleInitialize UI 消息循环，外部对话框调用由主线程受控调度并配置 recover 保护，根除 COM 异常导致 ERR_CONNECTION_RESET。' },
          { name: '原生 GDI 弹窗告警层', detail: 'popup_windows.go 实现独立 Win32 GDI 弹窗机制，绕过 Windows 10/11 专注助手与通知中心压制，保障运维定时提醒 100% 可见。' },
          { name: 'WAS 批处理与隔离层', detail: 'internal/waspack 提供 /api/waspack/build 接口，输出目录采用 fail / clean / replace 三种受控模式，按确定产物名安全清理，不向交付目录写入内部标记。' },
          { name: '任务告警事件总线层', detail: 'internal/notify 抽象系统级告警总线，任务执行失败时按策略联动 Windows 托盘弹窗与企业微信/钉钉 Webhook。' },
          { name: '全量自动化测试层', detail: '新增 tests/workbench-unit-tests.js、internal/dbconsole/oracle_workbench_test.go 等全量测试用例，覆盖 statement-model、session 管理、语法着色与分页逻辑。' }
        ],
        retirements: [
          '移除数据库多语句执行时将整段脚本盲目下发的做法，改为精确执行光标所在单条语句并规范化多分号。',
          '移除 Oracle 分页在结果表格中展示内部 ROWNUM / RNUM 辅助列的展示瑕疵。',
          '移除文件比对对话框在并发线程直接唤起导致偶发连接重置与闪退的高危逻辑。',
          '移除配置迁移各文件各自独立读写、缺乏全量快照与原子回滚的脆弱升级方式。',
          '移除依赖 PowerShell Toast 导致在 Win10 专注助手下静默失效的提醒通知机制。',
          '移除 WAS 打包产物与目标目录未隔离、无法区分清理范围的旧逻辑。'
        ]
      },
      features: [
        { title: '光标所在语句精准执行', desc: '在编辑器编写多条 SQL 时，根据光标位置自动识别当前语句边界；支持括号内分号、引号字面量与注释保护，支持连续多分号自动规整与单句执行。' },
        { title: '真实分页协议与 ROWNUM 过滤', desc: '后端支持 page / page_size / has_next / ordered 分页，兼容旧版 max_rows；Oracle 自动构建嵌套 ROWNUM 分页并从结果列中剥离辅助别名列。' },
        { title: '连接抖动透明重试与孤儿会话回收', desc: '数据库查询与元数据遇到网络短暂抖动时自动透明重试一次；关闭页签时后台自动清理资源孤儿态会话，杜绝连接泄露。' },
        { title: '小结果集直出渲染', desc: '小于等于 600 行的轻量查询结果直接渲染真实 DOM 节点，无缝绕过虚拟滚动初始测量延迟，响应更迅速。' },
        { title: '比对常驻编辑与实时语法高亮', desc: '文本比对区引入常驻 syntax-editor 实时着色；左右编辑器提供响应式视口自动收缩，实时比对消除异步竞态覆盖，支持多步撤销重做。' },
        { title: 'Windows 原生目录选择防崩溃', desc: '文件夹选择对话框经由常驻 UI 线程 OleInitialize 调度，增加 recover 异常保护与超时回收，彻底解决 ERR_CONNECTION_RESET 与主进程闪退。' },
        { title: '纯 GDI 桌面弹窗提醒引擎', desc: '替换不可靠的 PowerShell Toast 脚本，采用 Win32 原生 GDI 绘制置顶弹窗，100% 穿透 Windows 10/11 专注助手与免打扰模式。' },
        { title: '跨文件原子升级协调器', desc: '引入 internal/upgrade 统一升级协调器；升级前全自动拍摄 pre-upgrade 快照，多文件单向 N->N+1 链式迁移，任一失败全量原子回滚，并防止低版本覆盖高版本。' },
        { title: 'WAS 一键直接打包与产物隔离', desc: '提供 /api/waspack/build 接口与批处理解析支持，引入 fail / clean_kairo_artifacts / replace 策略，按当次包名清理明确产物并保留其他文件。' },
        { title: '任务告警事件总线与 Webhook', desc: '构建 internal/notify 告警事件总线，定时任务失败时主动推送桌面托盘并支持企业微信、钉钉等第三方 Webhook 报警。' },
        { title: '19 项反馈集中闭环', desc: '便笺更多菜单穿透与定位、武林榜冷启重试预热、浅色 kbd 对比度、SQL 选中可见性、九宫格收藏夹弹窗等全量精修。' },
        { title: '全量自动化测试护航', desc: '新增工作台前端单元测试 workbench-unit-tests.js、Oracle 分页测试、配置升级回滚测试，测试全量绿标通过。' }
      ],
      fixes: {
        p0: [
          '修复 Windows 文件比较在选择文件夹时因 COM 线程未经 OleInitialize 调度引发 ERR_CONNECTION_RESET 或主进程崩溃闪退。',
          '修复配置升级在进程异常中断时缺少跨文件事务快照，导致多文件迁移部分成功部分失败造成数据不一致。',
          '修复 WAS 投产打包在 Extract 之后打包阶段 marker 被覆盖导致未知文件保护失效的问题。'
        ],
        p1: [
          '修复 Windows 10/11 下提醒通知依赖 PowerShell Toast 被系统专注助手/通知中心静默拦截不弹出的缺陷。',
          '修复 Oracle 数据库工作台分页查询中把辅助别名列暴露给用户结果表格的问题。',
          '修复代码比对结果展示区在某些分辨率下高度溢出视口且 renderResult 偶现 effectiveMode 未声明的问题。',
          '修复桌面便笺“更多”菜单在卡片滚动或特定层叠上下文下被后续卡片或底边遮挡的问题。',
          '修复数据库 SQL 文本框多语句末尾连续分号引发语句解析器空语句报错的问题。',
          '修复武林榜首次打开因冷启动超时导致前端界面长时间空白的问题。'
        ],
        p2: [
          '修复浅色主题下 SQL 执行快捷键 kbd 样式对比度不足的问题。',
          '修复武侠主题下数据库工作台与比对编辑器的对比度与行高亮不协调问题。',
          '修复单行记录字段复制按钮点击命中区域偏小的问题。',
          '修复 SQL 收藏夹列表过长，新增九宫格书签选择弹窗。',
          '修复多 Tab 切换时偶发有效数据源绑定错位的问题。'
        ]
      },
      commits: [
        { hash: 'ce62432', msg: 'feat(workbench): 完善工作台交互、增强后端服务并补充自动化测试' },
        { hash: '7287e3d', msg: 'feat(workbench): 优化数据库与比对工作台交互并补充单元测试' },
        { hash: '9b72e93', msg: 'feat(workbench): 完成工作台重构 P0/P1 闭环' },
        { hash: '6baf8d4', msg: 'fix: 19项反馈集中修复 v20260903-fix19' },
        { hash: '5a2d477', msg: 'docs: add v0.14→v0.17 visual audit report (530 screenshots, 5 themes×6 viewports)' },
        { hash: 'e1d3709', msg: 'feat(about): integrate 2.0B+ AI tokens compute matrix, toolchains and brand SVG fleet' },
        { hash: '0d10bb2', msg: 'feat: add unified upgrade coordinator for config and user data' },
        { hash: 'b7f127d', msg: 'feat: improve configuration and tooling workflows' },
        { hash: 'd89c255', msg: 'feat: add WAS pack and WSDL codegen with workbench polish' }
      ],
      performance: [
        { label: '跨文件升级事务', before: '独立读写无事务保护，故障时有半更新脏数据风险', after: '全量快照 + 链式升级 + 任意失败全量原子回滚', improve: '数据 100% 一致性保障' },
        { label: '文件对话框稳定性', before: '并发工作线程调用 COM 有偶发崩溃/连接重置', after: 'UI 宿主线程常驻 OleInitialize + recover 兜底', improve: '0 崩溃 0 断连' },
        { label: '桌面告警送达率', before: 'PowerShell Toast 受系统专注助手静默拦截', after: 'Win32 原生 GDI 自绘顶层弹窗', improve: '100% 显式直达桌面' },
        { label: 'SQL 多语句体验', before: '整段执行容易产生批量误执行或分号解析错误', after: '光标所在单句精准执行 + 多重尾部分号智能规整', improve: '所见即所执' },
        { label: '数据库分页展示', before: '展示 Oracle 辅助 ROWNUM 别名列污染业务数据', after: '智能分页协议与 ROWNUM 别名列自动脱敏过滤', improve: '结果纯净度 100%' },
        { label: '比对编辑器视口', before: '固定尺寸容易撑破视口或与旧结果重叠', after: '常驻实时高亮 + 响应式高度收缩 + 竞态状态机', improve: '自适应所有主流分辨率' },
        { label: '质量与测试保障', before: '缺乏前端工作台单元测试与分页覆盖', after: '新增 workbench-unit-tests.js + Go 单元测试全量绿标', improve: '测试自动化无死角' }
      ],
      breaking: [],
      migration: [
        'v0.17 可无缝平滑升级至 v0.18；升级过程将由 internal/upgrade 协调器全自动创建 pre-upgrade 快照，所有配置文件在原子事务中链式升级。',
        '若从旧版本升级，数据库工作台将自动启用智能分页协议并过滤内部辅助列；原有 SQL 片段、书签及快捷键均向前兼容。',
        'Windows 路径选择对话框已全面迁移至 UI 宿主线程模型，避免任何第三方壳程序或系统 COM 初始化冲突。',
        'Windows 提醒弹窗默认启用可靠 GDI 原生绘制，无需在操作系统通知中心为应用开启后台唤醒权限。',
        'WAS 打包直接输出不再向交付目录写入内部标记；如需重做当次包名产物可选择 clean_kairo_artifacts 策略。',
        '升级后运行时版本以根目录 VERSION 为准；如修改版本，请执行 node scripts/sync-version.js，再运行 node scripts/check-version.js。'
      ]
    },
    {
      version: 'v0.17',
      date: '2026-09-01',
      tag: '数据库工作台专业化 · WAS 投产打包 · WSDL 代码生成 · 比较任务化',
      codename: 'Forge · 精工重铸',
      size: 'xl',
      headline: '以 PL/SQL Developer、Navicat、DataGrip 与 DBeaver 的高频工作流为参照，重构数据库页面的信息架构和结果交互；同时补齐 WAS 投产清单打包、老 Java 工程 WSDL 代码生成与可观察的比较/同步任务，并统一 Windows 桌面便笺语义。',
      stats: { commits: 2, fixes: 21, additions: 20, breaks: 0 },
      principles: [
        '数据库工作台首先服务于“连接—定位对象—编写—执行—检查结果”的连续工作流：数据源管理是渐进式入口，保存后必须收起，把稳定空间留给对象树、编辑器与结果。',
        '错误属于查询结果的一部分：SQL 失败必须在编辑器下方固定结果区域呈现原因、恢复建议和原 SQL，不依赖短暂 Toast，也不能让用户去控制台猜测。',
        '数据表格是分析工具而不是静态 HTML：列宽、排序、筛选、字段复制、列显隐、网格/单行模式和键盘操作必须形成可持续工作的最小闭环。',
        '元数据应按用户认知分组：表、视图、函数、存储过程、触发器等对象不能混成扁平列表；Oracle 与 MySQL 的差异由后端适配层收口。',
        '效率能力必须可配置且可发现：SQL 片段和快捷键提供安全默认值，同时允许用户定义缩写、模板与执行/取消/视图切换按键。',
        '快速不能以漏报为代价：目录比较默认采用智能内容校验；仅比较大小和时间的元数据模式明确标注可能漏报，避免同大小同时间文件被误判相同。',
        '展开详情只能改变行高，不能改变列宽：表格使用固定列布局和显式列宽，滚动条空间预留，杜绝点击前后内容横向跳动。',
        '便笺入口必须分层：顶栏快捷入口创建桌面便笺；中心新建只进列表；页面悬浮和桌面置顶由用户显式打开，系统不替用户弹窗。',
        '弹窗和长任务反馈必须稳定：对话框基于视口居中，扫描进度显示实际发现条目，空结果明确说明筛选状态，不让用户面对无意义的 0/2。',
        '所有优化都需要真实数据和真实点击证据：使用独立 MySQL 8.4 实例、中文/空格路径文件夹和 Playwright 在目标分辨率执行验收。',
        '投产打包必须先预检再落盘：清单路径、工程布局、缺失文件、自动补齐项和输出目录状态全部可见，生成过程拒绝覆盖已有内容。',
        '老 Java 工程的代码生成必须服从目标工程：优先扫描工程 JDK 与 lib/WEB-INF/lib，无法确认运行时版本时提供 Java 1.6、零第三方依赖的 portable 保底方案。'
      ],
      architecture: {
        layers: [
          { name: '数据库元数据适配层', detail: 'internal/dbconsole/metadata.go 为对象增加稳定 Category；Oracle 聚合 TABLE、VIEW、MATERIALIZED VIEW、FUNCTION、PROCEDURE、PACKAGE、SEQUENCE、SYNONYM、TRIGGER，MySQL 聚合 tables、routines 与 triggers，前端只消费统一分类。' },
          { name: '数据库工作台状态层', detail: 'database.js 统一维护数据源、Schema、对象树、字段、SQL、流式结果、列宽、列显隐、排序筛选、记录游标和偏好设置；渲染与事件绑定按工作区职责拆分，避免旧页面多处分散刷新。' },
          { name: '可配置效率层', detail: 'SQL snippets、收藏夹与快捷键以 preferences 持久化；模板支持 ${cursor} 光标占位，Space/Tab/Enter 可触发展开；轻量格式化与关键字高亮只改编辑器输入，不改变后端只读策略。' },
          { name: '结果交互层', detail: '结果区建立 status/error/grid/record 四种明确状态；列宽拖拽、上下文菜单、列管理器、本地筛选与排序共享同一列模型，网格和单记录视图复用同一结果数据。' },
          { name: '比较任务层', detail: '目录遍历回调报告实际已发现项目；本地深度散列并行处理左右文件，默认模式切换为内容校验，元数据极速模式作为明确的可选降级。' },
          { name: '桌面便笺协调层', detail: '顶栏、便笺中心、页面悬浮层和 Win32 窗口共用 notes API 与 revision；桌面同时可见上限 6 张；布局 PATCH 与正文冲突分开处理，内联编辑不会被窗口移动冲掉。' },
          { name: '稳定数据表布局层', detail: 'WebSphere/搜索排障结果使用 colgroup + table-layout: fixed + stable scrollbar gutter；详情展开只占据跨列内容行，避免内容长度重新参与列宽计算。' },
          { name: '数据库审查层', detail: '对象详情统一返回字段、索引、约束和 DDL/源码；Oracle 执行计划走只读 PLAN_TABLE 读取，前端复用同一工作台结果区。' },
          { name: 'WSDL 兼容生成层', detail: 'wscodegen 以六种引擎 profile 描述运行时、生成工具、JDK 下限、依赖 jar 和编译注意事项；内置模式与官方工具模式都经过 WSDL 解析和输出预览。' },
          { name: 'WAS 清单解析层', detail: 'waspack 把粘贴清单规范化为安全相对路径，再按 src/WebRoot/WEB-INF/classes 布局解析真实文件，可选补齐 java 对应的外部类与内部类。' },
          { name: '单一版本源层', detail: '根目录 VERSION 通过 go:embed 进入 /api/config，前端页脚与关于页只读运行时版本；sync-version.js 派生 README/npm 版本，check-version.js 负责守门。' }
        ],
        retirements: [
          '移除数据库页面“数据源表单长期占据顶部”的布局，改为可展开管理区，保存成功立即回到工作台。',
          '移除 SQL 错误仅依赖短暂消息提示的路径，统一进入可复制、可展开 SQL 的持久错误面板。',
          '移除结果表格不可调整、不可复制字段、只能单一网格查看的静态展示模型。',
          '移除首页便笺入口切换浏览器悬浮卡的旧语义，首页入口只创建原生桌面置顶便笺。',
          '移除便笺中心新建时弹出编辑框的流程，改为立即创建并在卡片上双击编辑。',
          '移除目录比较默认仅靠 size+mtime 判断内容相同的高风险策略。',
          '移除投产包依赖人工从工程目录复制文件、手工拼 tar 命令的流程。',
          '移除 WSDL 代码生成默认盲用 PATH 上 JDK、忽略目标工程 jar 的不透明路径。',
          '移除多个用户可见入口各自维护产品版本号的漂移风险。'
        ]
      },
      features: [
        { title: '专业数据库工作台布局', desc: '重排为对象导航、SQL 编辑器和结果面板三段式桌面布局；左侧面板可拖拽调整宽度，顶部工具条压缩低频信息，数据源管理保存后自动收起，在 1024×768 仍保持核心工作区可用。' },
        { title: '完整对象浏览器', desc: '按 Schema 展示表、视图、函数、存储过程、触发器与其他对象，分组显示数量并支持折叠；单击对象加载字段，双击表/视图生成查询，双击函数/过程生成调用骨架。' },
        { title: '字段面板与快速生成', desc: '字段列表显示字段名、类型、可空和主键信息；支持复制全部字段，降低 SELECT 列清单、排障记录和接口对字段时的重复手工输入。' },
        { title: '持久 SQL 错误工作区', desc: '执行失败后在结果区显示错误标题、完整原因、可执行的修复提示、原 SQL 展开内容和复制按钮；下一次执行前保持可见，不被 Toast 自动消失。' },
        { title: '可调整结果表格', desc: '每个列头提供拖拽手柄，列宽写入浏览器偏好；表头点击排序，工具条支持即时筛选，右键菜单支持复制字段名、单元格、整行、整列和隐藏列。' },
        { title: '列模式与行模式', desc: '网格模式适合横向比较，单记录模式将一行展开为字段/值清单并提供上一条、下一条和复制记录；超宽表无需持续横向滚动。' },
        { title: '列管理与可见字段复制', desc: '列管理器集中恢复/隐藏字段，复制可见字段按钮按当前列顺序输出名称；隐藏状态不破坏原始结果，切换视图时保持一致。' },
        { title: 'SQL 片段与自定义快捷键', desc: '默认提供 sf → SELECT * FROM、sel 与 cnt 模板；用户可以新增、修改或删除缩写与模板，并自定义执行、取消、网格/记录切换快捷键。' },
        { title: '多页签并行查询', desc: 'SQL 工作台支持最多 6 个查询页签；每个页签独立保存语句、结果和取消句柄，可同时向同一数据源发起只读查询，后台页签跑完后切回仍能看到结果。' },
        { title: 'SQL 关键字补全与括号匹配', desc: '输入两个以上字母即可从关键字、模板缩写、当前 Schema 对象和已加载字段中补全；光标停在括号上会高亮配对括号，未闭合则标红。' },
        { title: 'SQL 收藏夹', desc: '把常用只读查询存成命名书签，收藏夹与查询历史分开；刷新页面后仍可用，适合排障固定语句和日常核对 SQL。' },
        { title: '轻量 SQL 格式化与基础高亮', desc: '编辑器对关键字、字符串、注释和数字着色；格式化按子句换行并大写关键字，不引入第三方编辑器依赖。' },
        { title: '结果导出 CSV / JSON / Excel / INSERT', desc: '查询结果可导出 UTF-8 CSV、结构化 JSON、Excel .xlsx 以及按当前方言生成的 INSERT 语句；Excel 单元格按文本写入并规避公式注入。' },
        { title: 'Redis 拓扑感知', desc: '数据源可配置单机、Cluster 或 Sentinel；Cluster SCAN 按主节点推进游标，Sentinel 通过 Master 名称发现主库，预览仍受行数和返回体上限约束。' },
        { title: '数据源渐进式管理', desc: '新增/编辑数据源时展开配置区，保存后自动选中目标数据源并收起；MySQL 默认优先当前业务数据库而非 information_schema，减少首次进入的空转操作。' },
        { title: '便笺入口分层', desc: '顶栏“便笺”创建 Windows 桌面置顶窗口（有选中文本则摘录上去）；便笺中心“新建”只创建列表便笺并立即内联编辑。页面悬浮与桌面置顶是卡片上的显式动作。' },
        { title: '智能目录内容比较', desc: '默认比较模式读取内容散列，准确识别大小和修改时间相同但内容不同的文件；显式提供极速元数据模式给可信时间戳场景，并在文案中提示漏报风险。' },
        { title: '真实扫描进度', desc: '后端遍历过程中按实际发现条目更新进度，本地深度比较左右哈希并发执行；前端筛选为空时展示“当前筛选条件下无文件”而不是空白区域。' },
        { title: '搜索展开零抖动', desc: '搜索排障/WebSphere 下发结果使用固定布局和显式列宽；展开或收起完整命令、结果、错误信息时列宽保持不变。' },
        { title: '弹窗定位与缓存更新', desc: '比较源选择对话框在不同视口中水平垂直居中并限制最大高度，相关 CSS/JS 静态资源更新缓存版本，升级后无需等待旧资源自然过期。' },
        { title: 'WAS 投产清单打包', desc: '粘贴 credit 投产清单后先对照本地 IntelliJ 工程预检，自动识别 src / WebRoot / WEB-INF/classes，补齐同名内部类并生成 list.txt、tar、备份脚本和执行脚本。' },
        { title: '数据库对象详情与执行计划', desc: '对象浏览器新增字段、索引、约束、DDL/源码分栏；SQL 工作台新增只读执行计划入口，Oracle 使用 PLAN_TABLE，MySQL 返回 EXPLAIN 结果。' },
        { title: 'WS Java 多引擎代码生成', desc: '新增 portable、JAX-WS、CXF、Axis 1.4、Axis2、XFire 六种引擎，支持 WSDL URL/文件/已导入项目输入，提供生成预览与工程化输出。' },
        { title: '工程 JDK 与依赖对齐', desc: '探测 JAVA_HOME 与常见 JDK 安装目录，扫描 lib / WEB-INF/lib / pom.xml 中的 Axis、XFire、CXF 等 jar，自动建议引擎并提示 JDK 6/8 的兼容边界。' },
        { title: '比较与同步后台任务', desc: '目录扫描、深度比较和按选中项同步统一走后台 job，前端轮询进度并支持取消；同步默认单向覆盖、可备份且不镜像删除。' },
        { title: '版本单源与发布守门', desc: '产品版本只维护根目录 VERSION；Go 运行时通过 embed 注入，前端统一读取 /api/config，README 徽章和 npm 版本由脚本派生并在 release 前校验。' }
      ],
      fixes: {
        p0: [
          '修复目录比较仅根据文件大小与 mtime 判定，导致同大小、同时间但内容不同的文件被错误归入“相同”。',
          '修复 SQL 执行错误缺少持久可见反馈，用户无法在查询上下文内定位失败原因。',
          '修复首页便笺入口创建浏览器悬浮便笺而非 Windows 原生桌面置顶便笺的产品语义错误。'
        ],
        p1: [
          '修复 Oracle/MySQL 元数据对象类型过少，左侧只能查表/视图而无法按函数、过程、触发器定位对象。',
          '修复数据库数据源保存后配置区不收起、工作区被表单长期挤压的问题。',
          '修复结果列不可拖动宽度、字段名不可复制、无法隐藏列及无法按单行检查记录的问题。',
          '修复数据库页面 SQL 片段与快捷键不可配置，重复查询需要反复输入的问题。',
          '修复 WebSphere 搜索结果展开详情后浏览器重新计算列宽，整个列表横向跳动的问题。',
          '修复比较源弹窗在部分布局下定位到左上角，而非基于当前视口居中的问题。',
          '修复扫描提示长期停留“0 / 2”且不能反映已发现文件，让快速任务也产生卡死感的问题。',
          '修复 MySQL 首次加载优先 information_schema，用户已配置业务库却仍需手工切换的问题。'
        ],
        p2: [
          '统一首页便笺按钮与主题按钮的图标尺寸、描边、字体和命中区域，消除局部视觉风格不一致。',
          '便笺卡片颜色由局部色条改为整体背景色，并补齐内联编辑状态、保存与取消反馈。',
          '比较结果筛选后无命中时增加带恢复提示的空状态，避免误以为比较没有完成。',
          '结果列宽偏好、SQL snippets 与快捷键设置本地持久化，页面切换和程序重启后保持工作习惯。',
          '结果表数值使用稳定宽度与溢出策略，长字段通过完整值提示/单记录视图查看，不再挤压相邻列。',
          '补齐数据库对象详情与执行计划入口，避免用户需要离开工作台手工查询索引、约束、DDL 或执行计划。',
          '比较扫描和同步统一提供后台进度、取消和失败结果，长目录任务不再依赖单次请求等待。',
          'WSDL 代码生成对 URL 跳转、超大预览、JDK 版本和缺失依赖增加明确校验与警告。',
          'WAS 打包对清单大小、文件数量、符号链接、路径越界和非空输出目录做 fail-closed 校验。',
          '桌面便笺更新区分正文冲突与窗口布局更新，发生 revision 冲突时改为显式让用户选择保留版本。'
        ]
      },
      commits: [
        { hash: 'd0bf558', msg: 'feat(workbench): professionalize database notes and compare UX' },
        { hash: 'd89c255', msg: 'feat: add WAS pack and WSDL codegen with workbench polish' }
      ],
      performance: [
        { label: '发布质量门', before: '旧版统计不能代表本次改动', after: 'Go 全包 + vet 通过；完整 E2E 1031 通过；环境失败项按正确目录复测 28/28', improve: '结果可追溯、不用旧报告冒充' },
        { label: '真实 MySQL 查询', before: '静态页面无法验证完整元数据与结果交互', after: 'MySQL 8.4.11 · 120+ 行中文/NULL/JSON fixture · 查询与对象导航通过', improve: '真实数据闭环' },
        { label: '目录深度比较', before: '同大小同时间文件可能漏报', after: '中文/空格路径 fixture · 2 个内容差异全部识别 · 后端约 1ms', improve: '准确性优先且无感延迟' },
        { label: '搜索结果展开', before: '详情内容参与自动列宽计算', after: '固定 colgroup；展开前后各列宽度完全一致', improve: '横向布局位移为 0' },
        { label: '数据库结果操作', before: '只读静态网格', after: '拖拽列宽 + 排序 + 筛选 + 5 类复制 + 列显隐 + 记录视图', improve: '覆盖日常检查闭环' },
        { label: '扫描反馈', before: '长时间显示固定 0 / 2', after: '按已发现目录项连续更新', improve: '进度可观察' },
        { label: '投产包生成', before: '人工从工程拷文件并拼接 tar / shell', after: '清单预检 + 自动配对 + 四件固定产物', improve: '路径和产物可复核' },
        { label: 'WSDL 客户端生成', before: '手工猜 JDK / jar / 生成器组合', after: '6 引擎 profile + JDK 探测 + 工程扫描 + 内置预览', improve: '老工程兼容决策可见' },
        { label: '比较与同步任务', before: '长任务缺少统一生命周期', after: 'job 状态、阶段、进度、取消和失败项统一返回', improve: '长任务可观察可回收' }
      ],
      breaking: [],
      migration: [
        'v0.16 可直接替换为 v0.17；数据源、便笺、任务、宠物、WebService 模板与文件连接配置格式保持兼容，无需执行数据库迁移。',
        '数据库结果列宽、列显隐、SQL snippets、收藏夹和快捷键保存在当前用户 preferences；清理浏览器站点数据不会删除这些设置，但换机器需要重新收藏。',
        '默认目录比较从“大小+时间”调整为“智能内容比较”。大目录或高延迟远程目录如明确接受元数据风险，可手工选择“极速元数据（可能漏报）”。',
        '顶栏便笺按钮创建 Windows 原生桌面便笺；便笺中心新建默认只出现在列表，需要时再“页面悬浮”或“置顶到桌面”。旧版已置顶的桌面便笺保持原样。',
        '旧版 browser floating 字段继续兼容；同时只允许一张页面悬浮便笺。桌面同时可见上限 6 张。',
        '升级后建议强制刷新一次页面；index.html 已更新 database、notes、compare、websphere 与 style 的缓存版本，正常重新启动也会拉取新资源。',
        'Oracle 对象树增加 MATERIALIZED VIEW、PACKAGE、SEQUENCE、SYNONYM 等类型；MySQL 增加 routines/triggers。最终可见范围仍受当前只读账号的数据字典权限约束。',
        'SQL 工作台仍坚持后端只读边界；片段展开只负责输入效率，不放宽多语句、写操作、超时、行数或返回体限制。',
        'JSON、超长二进制等受限列仍会返回明确错误和改写建议；可用 CAST/SUBSTRING 等数据库函数把值转换到安全预览范围。',
        'WAS 打包不会覆盖非空输出目录；请准备空目录，并在预检确认缺失项为 0 后再生成投产包。',
        'WebService 代码生成默认优先工程 JDK 与依赖 jar；若只需可编译的轻量客户端，可选择 portable，生成 Java 1.6 源码且不依赖第三方 jar。',
        '比较同步仍是受控单向覆盖：只处理用户选中的项，可选备份，不镜像删除；长任务可在页面取消。',
        '升级后运行时版本以根目录 VERSION 为准；如修改版本，请执行 node scripts/sync-version.js，再运行 node scripts/check-version.js。',
        '发布验收使用 Go 1.24+：清测试缓存后运行全包 test/vet，npm ci 后执行 WebService 测试和 Playwright E2E，最终以 -mod=vendor -trimpath -H windowsgui 构建 Windows 10/11 版本。'
      ]
    },
    {
      version: 'v0.16',
      date: '2026-08-30',
      tag: '运维工作台 · Windows 原生桌面 · 全链路质量收口',
      codename: 'Atlas · 万象归一',
      size: 'xl',
      headline: '从“日志与连接工具箱”升级为覆盖数据库、文件同步、桌面便笺、原生宠物与 WebService 的综合运维工作台，并用真实 Windows 全量回归完成跨平台质量收口。',
      stats: { commits: 4, fixes: 18, additions: 12, breaks: 1 },
      principles: [
        '只读数据库边界必须由后端执行：Oracle/MySQL 只接受单条只读 SQL，Redis 只开放 SCAN 与有限预览；权限、超时、行数和返回体上限不能依赖前端约定。',
        '桌面能力必须是真正的 Windows 原生能力：宠物与便笺使用 Win32 窗口、托盘和系统通知，不再依赖临时 WebView、浏览器弹窗或 PowerShell 宿主拼接。',
        '长任务必须可观察、可取消、可回收：文件扫描/比较/复制/同步、SQL 流式查询、SOAP 请求与定时任务都要有明确生命周期，程序退出后不能遗留孤儿进程。',
        '跨平台路径语义在入口处统一：Windows 盘符、反斜杠、POSIX 根路径、中文及空格路径先规范化再校验，安全边界不能随运行平台改变。',
        '并发编辑不允许静默覆盖：浏览器便笺与桌面便笺共享 revision，发生版本冲突时显式返回并让用户决定保留哪一份。',
        '大数据量按流处理：数据库结果、CSV 导出、大文件搜索、超大 Redis Key 与文件任务均采用分页、分批或流式传输，避免一次性读入内存。',
        '编码是端到端契约：UTF-8、UTF-16、GBK、GB2312、GB18030 从文件/WSDL/SOAP 输入到响应展示统一解码，不能只在界面末端补转码。',
        '自动化测试必须验证真实交互：选择器绑定稳定 id、命令参数真实执行、Windows 进程树真实终止，并覆盖多分辨率、控制台和网络错误。'
      ],
      architecture: {
        layers: [
          { name: '数据访问层', detail: '新增 internal/dbconsole，统一 Oracle 11g、MySQL 与 Redis 数据源、凭据、连接测试、元数据、只读策略、流式查询、CSV 导出和 RBAC 审计。' },
          { name: '文件工作台层', detail: '新增 internal/comparefs 与 compare jobs，使用 Local/SFTP/FTP/FTPS 后端抽象承载扫描、比较、复制、同步、冲突策略、进度与取消。' },
          { name: 'Windows 桌面层', detail: 'internal/deskpet、internal/desknote、internal/winui 与 tray/popup 组成原生桌面能力；透明窗口、置顶、拖拽、托盘和通知由 Win32 直接管理。' },
          { name: '文本与服务层', detail: 'internal/textcodec 统一多编码识别；WebService 导入、SOAP 请求和日志搜索在同一编码与超时模型上工作。' },
          { name: '任务与进程层', detail: 'schedtask 按平台拆分 shell 构造和进程管理；Windows 使用 Job Object，Unix 使用进程组，取消语义由 managedCommand 统一。' },
          { name: '质量工程层', detail: '根目录 package-lock 固化 Playwright；Windows E2E fixture、Mock SSH、Mock 榜单和自研测试 runner 形成可复现的独立测试环境。' }
        ],
        retirements: [
          '移除 pet-float.html、pet-float-legacy.html、pet-legacy.js 与 PowerShell/WebView 宠物宿主，统一切换到 internal/deskpet 原生实现。',
          '主线不再承诺 Go 1.20 / Windows 7 构建；Win7 依赖树和验收矩阵转入 legacy 分支独立维护。',
          '定时任务不再由 runner.go 运行时判断平台并拼 cmd/sh 参数，改为 shell_windows.go 与 shell_unix.go 编译期隔离。'
        ]
      },
      features: [
        { title: '数据库工作台 V1', desc: '新增数据源管理与独立 database 页面；Oracle 11g 使用纯 Go go-ora、MySQL 使用 go-sql-driver、Redis 使用 go-redis。支持连接测试、版本/延迟显示、Schema/表/视图/字段元数据、只读 SQL、NDJSON 分批结果、UTF-8 CSV 导出、Redis SCAN 与按类型有限预览。' },
        { title: '数据库安全与资源上限', desc: '数据源增删改由 admin 控制，普通用户只能访问授权 source；SQL 经过单语句与只读策略校验，统一限制超时、最大行数、单元格和总返回体；凭据进入 OS keyring 或 AES-256-GCM 文件存储，审计只记录摘要不记录密码和完整敏感结果。' },
        { title: '多协议文件比较与同步工作台', desc: 'compare 页面升级为任务化工作台，Local/SFTP/FTP/FTPS 使用统一后端；支持目录扫描、仅左/仅右/内容不同分类、编码感知文本比较、二进制判定、复制与双向同步、覆盖/跳过/重命名冲突策略、进度展示及主动取消。' },
        { title: 'Windows 原生桌面便笺', desc: '新增 internal/desknote 与全局 notes/overlays 前端模块；便笺可置顶桌面、拖动、最小化、编辑和关闭，并与浏览器管理中心共享数据。revision 乐观并发控制阻止浏览器与桌面窗口互相静默覆盖。' },
        { title: 'Windows 原生桌面宠物', desc: '新增 internal/deskpet：Win32 透明无边框窗口、像素精灵渲染、拖拽和吸边、右键菜单、气泡与状态动画；替代 WebView 悬浮窗后不再依赖浏览器内核。皮肤扩展到 108 款，激活码 SHA-256 派生稳定宠物 ID，重装不再生成重复排行榜账号。' },
        { title: '宠物动画与榜单闭环', desc: 'skins.json 成为皮肤单一清单，petgen 同步生成资源；idle/drag/click/levelup/evolve/skin 状态机、轮询退避、经验与榜单服务端认可分形成完整闭环；About 卡片与侧栏“关于”共用同一解锁状态机。' },
        { title: 'WebService 深度增强', desc: 'WSDL/XSD 支持多文件导入（单文件 4MB、合计 16MB）、递归 import/include、混合 SOAP 1.1/1.2 binding 与复杂类型继承；请求 Header 支持逐行、JSON、curl，提供 GBK/GB2312/GB18030/UTF-16 编解码、取消、超时和按次关闭历史保存。' },
        { title: '日志组合查询与大文件检索', desc: '日志查询支持多关键词组合、命中窗口上下文、行号聚合与大文件安全搜索；命令构造统一目录规范化，Windows 本地 Git/MSYS sh 可识别盘符路径，远端 POSIX 输入仍保持严格注入拦截。' },
        { title: '定时任务可靠执行', desc: 'Windows cmd.exe 使用 /D /S /C 原始命令行语义，正确保留中文、空格路径和内部引号；Job Object 在进程恢复前接管任务，超时、取消和 Kairo 退出时回收完整子孙树，输出自动识别 UTF-8/GBK。' },
        { title: '便笺提醒命令参数', desc: '提醒编辑器新增可逆的参数解析/格式化器：单引号、双引号、空参数与空格路径可往返编辑，Windows 路径反斜杠不再被误当通用转义符；E2E 真实启动 Node fixture 验证保存参数与执行参数完全一致。' },
        { title: 'Windows 独立 E2E 环境', desc: 'scripts/e2e-prepare-fixtures.js 生成 tmp/e2e/run/config.yaml，隔离用户配置、数据与端口；Mock SSH 在 Windows 使用 Git Bash 与 GNU 工具优先 PATH，Mock 榜单只监听 loopback，测试结束验证端口释放和无孤儿进程。' },
        { title: '可复现前端测试与小屏布局', desc: '提交 package-lock.json 固化 Playwright 版本；自研 grep runner 支持祖先/后代 suite 匹配并在 hook 前跳过无关树；模态编辑器改为可滚动 body + 固定 footer，在 1024×768 与 1366×900 下仍能完成保存。' }
      ],
      fixes: {
        p0: [
          'Windows 定时任务由 os/exec 通用参数编码改为 cmd.exe 原生命令行规则，修复带引号和空格路径命令无法执行的问题。',
          'Windows Job Object 保留 shell CmdLine 与创建标志，在恢复线程前完成进程归组，修复超时后孙进程继续存活和程序退出遗留孤儿进程。',
          '数据库入口统一只读策略、RBAC、查询超时与结果上限，阻断多语句、写操作和未授权数据源访问。',
          '浏览器/桌面便笺以 revision 检测并发更新，修复后写覆盖先写且用户无感知的数据丢失风险。'
        ],
        p1: [
          'ResolvePaths 同时识别当前平台与 POSIX 绝对路径，修复 Windows 读取 Linux/AIX 配置时把 /var/... 错拼到 exe 目录。',
          '下载 safeJoin 统一 slash 并拒绝反斜杠、绝对路径和卷名，修复跨平台迁移后路径边界不一致。',
          '日志目录校验收口到 normalizeShellDir，补充换行、NUL、反斜杠与 shell 元字符检查，同时允许 Windows 本地盘符转换为 E:/...。',
          'WebService 多编码入口统一 textcodec，修复 GB2312/GB18030/UTF-16 WSDL、XSD 与 SOAP 报文识别不一致。',
          'HTTP 深度测试改用 #http2-* 精确控件，修复误点禁用 WebSocket 发送按钮造成的假失败与假通过。',
          '宠物解锁点击状态从 app.js/home.js 分散实现收口到 pet.js，修复首页 About 卡片与侧栏 About 菜单行为不同。',
          '提醒命令参数不再用空白正则直接 split，修复中文和空格路径被拆成多个参数、编辑后无法还原的问题。',
          'Windows Mock SSH 使用 Git Bash 并把 GNU find/sort 提前到 PATH，修复系统 find.exe 参数冲突以及交互终端不可用。'
        ],
        p2: [
          '跨包持久化测试新增 testutil.ReadPrivateFile：Unix 验证 0600，Windows 验证 ACL 平台可观察契约，避免把合成 FileMode 当真实权限。',
          'SFTP/HTTP 错误路径测试改用“普通文件作为父目录”的确定性失败条件，不再依赖 Windows 系统盘 ACL。',
          'WebSphere 搜索按钮补稳定 id，页面对象移除 emoji 文本耦合；命令卡测试按当前常显详情 UI 隔离导航状态。',
          '任务编辑模态框增加视口最大高度和内部滚动，小分辨率下保存/取消按钮不再落到屏幕外。',
          '主页“代码比对”文案与实际模块对齐，定时任务补“新”标记，避免入口说明与功能不一致。',
          '根目录 npm 锁文件纳入版本控制，npm ci 从“缺少 lockfile 无法运行”恢复为确定性安装。'
        ]
      },
      commits: [
        { hash: 'ce72cf5', msg: 'feat(pet): 皮肤 V2 精灵图动画 62 款 + 桌面悬浮窗口兼容 + 审查修复' },
        { hash: '057f30e', msg: 'feat(pet): v0.16 原生桌面宠物 deskpet 模块 + 皮肤扩充 46 款 + 宠物 ID 绑定激活码' },
        { hash: 'ec06991', msg: 'feat: expand operations workbench and Windows desktop tools' },
        { hash: '1cb8c7e', msg: 'fix: harden Windows runtime and e2e coverage' }
      ],
      performance: [
        { label: 'Windows 全量 E2E', before: '缺少 lockfile，npm ci 无法复现', after: '1045 passed / 0 failed / 159 条件跳过', improve: '全量门禁通过' },
        { label: '三分辨率页面矩阵', before: '小屏模态按钮可能越界', after: '42/42；横向溢出与页面/网络错误均为 0', improve: '1024×768 可完整操作' },
        { label: 'Go 质量门', before: 'Windows 平台测试存在路径/权限/进程语义失败', after: 'go test 全包通过 + go vet 零告警', improve: '平台契约收口' },
        { label: '任务取消', before: '仅终止直接 cmd 子进程', after: 'Job Object 回收完整进程树', improve: '孤儿进程 0' }
      ],
      breaking: [
        '主线构建基线升级到 Go 1.24+，正式发布目标为 Windows 10/11；Windows 7 / Go 1.20 版本转入 legacy 分支，不再与主线共用依赖和验收矩阵。'
      ],
      migration: [
        '从 v0.15 升级时建议先备份 config.yaml 与 data/；直接替换 Kairo_win10.exe 即可，现有系统、SSH、提醒、下载和宠物数据继续沿用。',
        '新增数据库数据源后，凭据按 credential_store 写入 Windows Credential Manager 或 data 下的加密文件；不要把数据库密码直接写进 config.yaml。',
        '数据库工作台默认 fail-closed：新建/修改/删除数据源要求 admin；普通用户必须在数据源 allowed_users 中获得授权。',
        '首次打开原生桌面便笺会创建新的 notes 持久化状态；浏览器与桌面同时编辑发生 409 冲突时必须在界面选择保留版本，系统不会自动覆盖。',
        '文件同步属于显式写操作：运行前确认源/目标方向和冲突策略；取消只停止尚未执行的步骤，已经完成的文件不会自动回滚。',
        'WebService 上传限制调整为单文件 4MB、合计 16MB；旧项目与模板可直接读取，新编码字段缺失时按自动检测处理。',
        '任务命令仍由本机 shell 执行；升级后 Windows 引号语义更严格。建议在任务编辑页重新打开并保存包含嵌套引号的历史任务，再执行一次手工验证。',
        '生产部署继续只监听 127.0.0.1；需要远程访问时必须显式配置 auth token、角色和 allowed_ips，不能仅把 host 改为 0.0.0.0。',
        '本版根目录新增 package-lock.json，仅用于开发/E2E 的确定性安装；Kairo 发布 exe 仍不需要 Node/npm 运行时。',
        '发布验证基线：Go 1.24+ 执行 go test/go vet，npm ci 后运行 test:webservice 与 e2e，Windows 构建使用 -mod=vendor -trimpath -ldflags "-H windowsgui"。'
      ]
    },
    {
      version: 'v0.15',
      date: '2026-08-17',
      tag: '宠物养成 · 任务闭环 · HTTP/ws 强化',
      codename: 'Phoenix · 天命续命',
      size: 'xl',
      headline: '引入宠物养成闭环 + 定时任务系统 + SSH 配置档案 + HTTP curl/WebSocket 能力增强，新增排行同步规范与安全边界收敛。',
      stats: { commits: 16, fixes: 6, additions: 7, breaks: 0 },
      principles: [
        '未解锁情况下禁止持久化：未达激活条件前不写入 pet.json 与任何宠物状态文件；相关 API 返回 404 或关闭态。',
        '激活条件与幂等性：在 About 页 10 秒内连续触发 6 次解锁动作后，执行一次性激活建档；已解锁用户后续启动自动恢复。',
        '计分规则：仅对白名单操作产出成长分，op/target 维度具备冷却约束，存在单日上限，并将会话时长计入评分，全部计算过程落可追溯摘要。',
        '展示约束：坐标以相对视口百分比持久化，落盘前执行 [0,1] 范围裁剪；名称长度限制与控制字符过滤；皮肤切换需通过服务端清单校验。',
        '排行榜口径与容错：采用服务端认可分（board_exp）作为权威对比值；失败重试保留 dirty 状态并允许本地展示降级。'
      ],
      features: [
        { title: '宠物系统（v1）', desc: '新增 internal/pet 全链路（状态管理、经验引擎、规则表、同步客户端）与 /api/pet/*；新增漂浮宠物组件 web/pages/pet.js（拖拽、吸边、可见度、摇晃换肤、改名、说话气泡）；宠物榜单 Tab 在 sponsor 页面可见。' },
        { title: '定时任务管理', desc: '新增 internal/schedtask 与 cronx 子系统，补齐调度模型、执行记录与持久化路径；新增 web/pages/tasks.js 与 /api/tasks/*，支持任务视图与交互。' },
        { title: 'SSH 配置档案化', desc: '新增 internal/sshclient/profilestore，支持会话主机档案持久化与结构化读写，提升跨页面/跨会话一致性。' },
        { title: 'HTTP curl/WebSocket 增强', desc: '新增/扩展 handlers_http_curl 与 handlers_http_ws，结合 web/pages/http.js 改造请求编排与回显，增强 SOAP/curl/WS 测试体验。' },
        { title: '排行榜同步协议', desc: '新增 internal/pet/sync.go 与 endpoint 同步约定（serviceID 固定、seqNo 时序、OK/board_exp/rank 回写），未配置端点时仅提示本地展示，不进行假地址回传。' },
        { title: '参数与端点治理', desc: '通过 internal_endpoints 与构建脚本剥离配置链路，配置源统一落到 config.yaml，避免硬编码漂移。' }
      ],
      fixes: [
        'P0: 去除 sponsor 榜单与 webservice 的历史竞态与重复提交路径，修复 SSRF/5 分钟缓存/防重交互边界，降低错误提交与资源浪费。',
        'P1: 更新 sponsor 回传字段兼容性（updated_at 改 string），兼容 Java 端 DATETIME；异步榜单失败不泄露内部地址。',
        'P1: 更新提醒 Update 字段合并逻辑，修复前端空字符串透传导致的关键字段清空。',
        'P1: Windows 7 下 emoji 与 HTTP 按钮视觉回退，about 渐变与新图标渲染兼容性修复。',
        'P2: 代码审查闭环修复凭据外置、并发写入、错误泄露、XML 注入与前端健壮性。'
      ],
      commits: [
        { hash: 'd15dd88', msg: 'feat: v0.15 PET 养成游戏 + 定时任务管理 + SSH 配置档案 + HTTP curl/WebSocket 增强' },
        { hash: 'de0275f', msg: 'fix(webservice): SSRF 防护移除 + sponsor 排行榜 5min 缓存 + 防重提交' },
        { hash: 'b5f88e9', msg: 'fix(sponsor): updated_at 改 string 透传, 兼容 Java 端真实 DATETIME 格式' },
        { hash: '833d397', msg: 'fix(reminder): Update 字段合并加固, 防前端漏传被空字符串清空' },
        { hash: '715424b', msg: 'fix(web): Win7 emoji 兼容 + http 美化按钮 + about 渐变 + 7 个新 SVG 图标' },
        { hash: '016f130', msg: 'fix: 代码审查深度修复 — 凭据外置/数据竞争/错误泄露/XML 注入/前端健壮性' },
        { hash: 'd813690', msg: 'chore(config): internal_endpoints 块 + build 脚本同步剥离' },
        { hash: '11903d2', msg: 'docs(README): v0.14 全文重写 — 修复 50+ 处与代码不符' },
        { hash: 'd5c541c', msg: 'fix(sponsor): 投喂作者页排行榜改异步加载, 失败不再反显内部地址改随机搞笑提示' }
      ],
      performance: [
        { label: '宠物页首屏挂载', before: '首次无条件常驻', after: '按需初始化 + 轮询退避', improve: '降低常驻渲染压力' },
        { label: '排行榜重刷', before: '频繁无条件请求', after: 'dirty 与成功态复用 + 缓存回退', improve: '抖动下降' }
      ],
      migration: [
        '新增宠物 v1 模块：internal/pet、handlers_pet、web/pages/pet；与 sponsor 页面联动形成切换式排行榜入口。',
        '新增任务治理：internal/schedtask + internal/cronx + /api/tasks，任务从界面到落盘形成统一链路。',
        '新增 SSH 档案子系统：internal/sshclient/profilestore 提供 profile 结构化持久化。',
        '新增 HTTP curl/ws 能力增强：handlers_http_curl/handlers_http_ws + web/pages/http 的请求链路增强。',
        '端点治理与构建流程收敛：internal_endpoints 与 build 脚本配置化，减少环境差异风险。'
      ]
    },
    {
      version: 'v0.14',
      date: '2026-07-13',
      tag: '投喂作者 · 性能优化 · 内部基座',
      codename: 'Aurelia · 锦上添花',
      size: 'l',
      headline: '投喂作者门面上线 (Go 后端 + Web UI + DBA 对接文档) + endpointclient 共享基座 + about.js 首屏节点 -95% + License 审计全流程',
      stats: { commits: 7, fixes: 2, additions: 4, breaks: 0 },
      principles: [
        '续命也是产品力: 投喂作者不是营销 — 是给老用户一个反哺渠道, 排行榜用真实赞助数据, 失败不反显内部地址, 5 条随机搞笑 quip 兜底',
        '基座先行: 抽 endpointclient 是为了让 license / sponsor / 后续所有"调内部服务"的代码走同一条主备+超时+错误归一路径, 不再每包手写 http.Client',
        '安全可见: License 激活全流程写 audit (request/ok/fail), 跟 Java 端 K_AUDIT 对齐, 给"内鬼"追查留数据源, 审计不是可选项',
        '门面先于自己: about.js 性能优化 (节点 -95%) 是为了让 about 页本身不成为性能包袱 — 元页面不能拖慢进入, 自己不能成为反例'
      ],
      features: [
        { title: '投喂作者 Web UI (web/pages/sponsor.js 579 行)', desc: 'Hero 黄色气泡 + 营业中牌子 (红色 pulse 动画) + 古风/高对比度主题适配; 3 品牌: 库迪 (9.9 续命首选) / 瑞幸 (小蓝杯) / 随缘 (给啥喝啥) — 二维码 + 选中态放大 + 彩色实心长条; 18 条搞笑随机文案 (不乞讨, 同事间玩笑感); 天命武林榜 (50 昵称写死, rank 1:1 绑定) + 续命恩人榜 (前 10 名 hover 动画 + 三色奖杯 gold/silver/bronze); 二维码卡片 (左右手指 bounce 提示) + 投喂方式切换; 排行榜侧栏 5 分钟本地缓存 + 失败友好降级' },
        { title: '投喂作者 Go 后端 (internal/sponsor 555 行 + mock)', desc: 'types.go Leader / Leaderboard 强类型 + JSON 序列化; client.go 调内部激活服务 serviceID=SPONSOR_LEADERBOARD 走 endpointclient 主备切换; InitFromConfig 覆盖 var 池; sponsor_test.go 312 行 + integration_test.go 70 行表驱动覆盖正常/拒绝/网络挂/主备切换/字段映射; handlers_sponsor.go /api/sponsor/leaderboard 5 分钟内存缓存; cmd/mock-sponsor-server 独立 mock 给前端开发 + E2E, 不进生产发布包' },
        { title: 'endpointclient 共享 HTTP 调用器 (新包, 360 行)', desc: 'internal/endpointclient/client.go 136 行: 统一封装主备切换 / 超时 / 4xx-5xx 错误归一化; 替换 license/server.go 手写 http.Client, 函数签名兼容 (老测试零修改); 224 行 client_test.go 覆盖主备切换 / 超时 / 4xx-5xx 错误码归一 / ctx 取消; 后续所有"调内部服务"的代码统一走这条路径' },
        { title: 'License 审计增强 (防内鬼核心数据源)', desc: 'handlers_license.go 激活全流程写 audit (request / ok / fail), 跟 Java 端 K_AUDIT 风格一致; ConfigSnapshot 加 LicenseActivatePrimary / Secondary / Auth 字段; InitFromConfig() / applySnapshotLocked() 注入时立刻覆盖 var 池; postActivateJSON 改走 endpointclient, 简化调用' },
        { title: '投喂作者部署文档 (DBA + Java 同事对接)', desc: 'docs/sponsor/K_SPONSOR.sql: Oracle 建表 (K_SPONSOR + 3 触发器 + 索引 + 5 条示例数据); KairoSponsorLeaderboardAction.java: Java Action (Basic auth + serviceID 路由); README.md: DBA + Java 同事对接流程, 接口协议, 已知风险点 (主备切换 / 缓存一致 / 审计一致性)' },
        { title: 'about.js 性能优化 (perf 提交 3a8da9d, 为 v0.14 蓄力)', desc: '12 section IO 懒渲染 (IntersectionObserver + content-visibility: auto); DocumentFragment 批量挂载 (14 次 reflow → 1 次); sticky 锚点条删 backdrop-filter (滚动 GPU 模糊合成停掉); toggleVersion setTimeout 加 timerId 清理 (修快速连点 bug); 数据: 首屏 view 节点 3187 → 165 (-95%), 首屏 load 770ms → 564ms (-27%); 5 主题 × 4 场景全过 0 错误; 老浏览器 (无 IO / 无 CV) 自动降级' },
        { title: '投喂页面集成入口 (3 处)', desc: 'web/index.html: 导航加"投喂作者" + sponsor.svg 路由 icon + cache buster 升 20260711; web/pages/webservice.js: 首页快捷入口加"投喂作者"卡 (跟其他工具卡风格一致); web/icons.js: 新增 coffee 图标 (琥珀色咖啡杯 + 热气)' },
        { title: '全局版本号 6 处对齐 v0.13 → v0.14', desc: 'VERSION 文件; internal/httpserver/httpserver.go Version 常量 + ldflags 示例注释; web/pages/about.js VERSION 常量; web/index.html #footer-version; web/app.js info.version fallback; README.md Status 徽章 — 老配置无需动, 替换二进制即生效' }
      ],
      fixes: [
        'P1: 投喂作者页排行榜改异步加载 + 失败不反显内部地址 (d5c541c) — 接口 502/慢响应/挂掉时, 静态部分 (banner/品牌/QR/footer) 立即显示, 榜单区显示 5 条随机搞笑 quip + retry; console.warn 给开发者排查; Playwright 18099 端口实测: 内部地址/svc/HTTP 502 泄漏检测全部 false',
        'P1: 顶部 status dot/text 改默认隐藏 — 太抢戏, 改用 footer listen-info 显示, 让顶栏更干净',
        'P2: 配置文件补 internal_endpoints 段 (license_activate 5s / sponsor_leaderboard 10s 默认值) — 后续 Java 同事可改 IP 不用动源码',
        'P2: nav-item.active 改 inset box-shadow 画左条 (不占 layout 空间) + svc-form-grid 网格化表单 (解决 select/input flex:1 拉满全宽)'
      ],
      commits: [
        { hash: '979ae99', msg: 'feat(internal): 提取 endpointclient 共享 HTTP 调用器 + License 审计增强' },
        { hash: '32a20ee', msg: 'feat(sponsor): 投喂作者排行榜 Go 后端 + Mock 服务器 + 部署文档' },
        { hash: '3a8da9d', msg: 'perf(about): 12 section IO 懒渲染 + 批量挂载 + content-visibility, 首屏节点 -95%' },
        { hash: 'b2cd572', msg: 'feat(v0.14): 投喂作者 Web UI + About/顶部 status 优化 + 全局版本号升 v0.14' },
        { hash: 'd5c541c', msg: 'fix(sponsor): 投喂作者页排行榜改异步加载, 失败不再反显内部地址改随机搞笑提示' },
        { hash: '55badb6', msg: 'fix: 修复文件在线编辑第二次点击时闪退问题' },
        { hash: 'bb0acf6', msg: 'fix: 修复about.js语法错误、更新所有JS/CSS缓存版本号至v20260710, 解决关于按钮点击无反应问题' },
        { hash: '1e05969', msg: 'chore: 忽略 .mavis/ 目录 (Mavis agent 自身运行数据)' }
      ],
      performance: [
        { label: 'about.js 首屏 view 节点', before: '3187', after: '165 (-95%)', improve: '19x' },
        { label: 'about.js 首屏 load', before: '770ms', after: '564ms (-27%)', improve: '1.4x' },
        { label: 'about.js reflow 次数', before: '14 次 (每个 section 各 1 次)', after: '1 次 (DocumentFragment 批量挂载)', improve: '14x' }
      ],
      migration: [
        '新增左侧菜单"☕ 投喂作者"入口 (web/pages/sponsor.js), 17 → 18 个前端页面',
        'config.yaml 新增 internal_endpoints 段 (license_activate 5s / sponsor_leaderboard 10s 默认值), 老配置无需动 (Defaults 兜底)',
        'audit.log 新增 license.activate.request / ok / fail 事件 + sponsor.leaderboard.error 事件, 跟 Java 端 K_AUDIT 对齐',
        'web/index.html / ssh.html 静态资源 cache buster 升 ?v=20260711; 用户需硬刷新 (Ctrl/Cmd+Shift+R) 加载 sponsor 路由 icon',
        '无破坏性变更; 现有 v0.13.1 用户直接替换二进制即可'
      ]
    },
    {
      version: 'v0.13.1',
      date: '2026-07-10',
      tag: 'UI体验优化 · Bug修复',
      codename: 'Polish · 精雕细琢',
      size: 'm',
      headline: '日志上下文行数上限放开到5000、搜索展开按钮样式优化、SSH终端搜索修复、文件上传UI重排、Kairo品牌动画修复',
      stats: { commits: 1, fixes: 8, additions: 3, breaks: 0 },
      principles: [
        '用户反馈驱动: 基于真实使用场景的8个问题集中修复，提升日常操作流畅度',
        '视觉一致性: 搜索结果展开按钮、工具栏对齐、品牌动画跨主题统一',
        '功能完整性: SSH终端Ctrl+Shift+F搜索功能补全，不再有"看得见用不了"的按钮'
      ],
      features: [
        { title: '日志上下文行数上限扩展', desc: '上下文新窗口行数上限从500提升到5000，满足深度排查需求；输入框max属性同步更新；localStorage持久化用户设置' },
        { title: '文件上传UI重排', desc: '上传按钮从底部移到下载按钮右侧同一行，上传默认到当前目录，移除独立上传路径输入，操作路径更短更直观' },
        { title: 'Kairo品牌渐变动画修复', desc: '顶栏和关于页的Kairo/天命契机流光渐变动画跨5套主题正常显示（护眼绿/深色/浅色/高对比/武侠古风）' }
      ],
      fixes: [
        'P1: 日志上下文行数输入1000/5000被截断为500 — 前端常量引用错误(CONTEXT_LINES_MAX→CONTEXT_LINES_MAX_CTXWIN)，max属性同步更新',
        'P1: 上下文行数设置不记忆 — localStorage持久化逻辑修正，下次打开保留用户偏好',
        'P1: SSH终端搜索框(Ctrl+Shift+F)无法使用 — ssh.html补加缺失的xterm-addon-search.min.js脚本引用；ssh.js修正SearchAddon API调用方式',
        'P2: 搜索结果展开/收起按钮太小不明显 — 重新设计为带背景色的"展开"/"收起"标签按钮，opacity提升到0.8，hover时完全显示',
        'P2: 文件页面工具栏布局不齐 — 本地下载目录输入框加宽，预览与按钮行flex对齐；打包zip本地目录同样修复',
        'P2: 外部打开器配置页文本框左侧重复图标 — 清理冗余DOM元素',
        'P2: 日志文件列表缺少排序功能 — 文件名/修改时间/大小列表头支持点击排序',
        'P2: 便笺提醒页面单次/周循环/月循环按钮点击卡顿 — 事件处理优化'
      ],
      commits: [
        { hash: 'wip', msg: 'fix(v0.13.1): 8项UI/功能修复 — 上下文行数/SSH搜索/展开按钮/上传布局/品牌动画' }
      ],
      migration: [
        '无破坏性变更；用户硬刷新(Ctrl/Cmd+Shift+R)即可加载最新JS',
        'ssh.html新增xterm-addon-search.min.js引用；缓存版本参数更新为?v=20260710'
      ]
    },
    {
      version: 'v0.13',
      date: '2026-07-08',
      tag: '定时提醒 + 浏览器自动打开 + 在线编辑',
      codename: 'Herald · 司晨报晓',
      size: 'l',
      headline: '三大新特性：定时提醒(once/weekly/monthly)、浏览器偏好探测自动打开、远端文件在线编辑 + 周边优化',
      stats: { commits: 2, fixes: 7, additions: 9, breaks: 0 },
      principles: [
        '提醒轻量: once / weekly / monthly / cron 四种触发器 + 提前 N 分钟 + 触发动作（弹窗/网址/命令）在 Manager 池里跑，进程退出/重启从 data/reminders.json 恢复',
        '浏览器即用: 启动时按偏好自动探测并打开本机浏览器，Win 优先 Chrome (注册表+常见路径)，macOS/Linux 走 open / xdg-open',
        '在线编辑: 文件下载页直接编辑文本类远端文件，保存自动备份 .bak + 写审计 + 可回滚，不再"下载 → 本地编辑器 → 上传"',
        '代码审查闭环: v0.13 feat 后立即跟一个 fix commit，把搜索高亮公共化 + 文件类型白名单 + cache buster 等审查点修掉'
      ],
      features: [
        { title: '定时提醒模块 (internal/reminder)', desc: '4 种触发器 (once/weekly/monthly/cron) + 提前 N 分钟 + 触发动作扩展（popup/url/command）+ Manager 池 + data/reminders.json 持久化 + Win10+ Toast/经典气球兜底 + 暂停/恢复 + 触发审计日志 + web/pages/reminders.js 页面' },
        { title: '浏览器偏好探测 (internal/browserpref)', desc: 'data/browser_state.json 持久化 {kind, path, updated_at}；下次启动直接复用上次浏览器；Win 注册表 + 常见路径枚举 (Chrome/Edge/Firefox/360/QQ)；KAIRO --reset-browser 清掉重来' },
        { title: 'Windows 弹窗实现 (internal/popup)', desc: '536 行 popup_windows.go + 13 行 popup_other.go 跨平台兜底 + 21 行 popup.go 接口；sysutil.browser_locate_windows 190 行 Win 特定实现' },
        { title: '远端文件在线编辑 (handlers_edit.go)', desc: '文件下载页直接打开文本类远端文件 (properties/xml/yaml/json/conf/cfg/ini/log 等) → textarea 编辑 → 保存走 SFTP WriteFile；保存即备份 .kairo-edit.bak (最多 20 份) + 写审计 + 支持回滚' },
        { title: '搜索高亮公共模块 (vendor/search-hl.js)', desc: 'preview.html / tail.html / 上下文窗口三处共用；TreeWalker 遍历文本节点 + mark 标签包裹 + Enter/Shift+Enter 跳转 + Esc 清除，不再每页写一套' },
        { title: 'OSC 999 cwd 标记降级', desc: 'shell 真实 cwd 通过 WS query_cwd 控制帧 + stdout 流扫描 OSC 999 起止私有序列提取 $PWD，终端忽略不显示 (替代原 __KAIRO_CWD_*__ 污染终端的标记)' },
        { title: '文件类型检测改进', desc: '白名单+黑名单混合策略 (.ts/.exe/.so/.dll/.bin 等不被误判为可编辑文本)；preview 二进制文件提前提示不打开' },
        { title: 'cache buster', desc: 'index.html / ssh.html 引用 JS 加 ?v=20260708 强制刷新浏览器缓存' }
      ],
      fixes: [
        '修复 shell cwd 标记 __KAIRO_CWD_*__ 污染终端显示（改用 OSC 999 私有序列）',
        '修复 .ts 等扩展名撞名导致 TypeScript 源码被误判为二进制（白名单+黑名单混合策略）',
        '修复源码发布包包含 mock/fake 测试数据（.gitattributes export-ignore 排除 scripts/fake-*、mock 服务等）',
        '修复 search-hl.js 在三个窗口重复实现（抽公共模块 vendor/search-hl.js 统一管理）',
        '修复 cache buster 缺失导致浏览器缓存老 JS 不生效（?v=20260708 版本参数）',
        '修复 SFTP 面板 / 文件下载 / 审计模块若干稳定性与一致性细节',
        '修复 tray (Windows 系统托盘) 启动时序和错误反馈'
      ],
      commits: [
        { hash: '491cd7d', msg: 'feat(v0.13): 定时提醒 + 浏览器自动打开 + 在线编辑三大特性 (71 文件, +6184/-184)' },
        { hash: '885799c', msg: 'fix(v0.13): 代码审查修复 — 搜索高亮抽公共模块/OSC999降级/.ts扩展名/cache buster' }
      ],
      migration: [
        '新增左侧菜单「⏰ 定时提醒」入口 (web/pages/reminders.js)；现有用户首次访问自动发现',
        'auto_open_browser 配置 v0.13 起首次启动探测 Win Chrome 注册表，结果落 data/browser_state.json；老配置不动',
        'data/reminders/ 目录首次启动自动创建；便笺提醒走 data/.kairo-reminders.json (兼容旧版)',
        '文件下载页右键菜单新增「编辑」按钮，仅对白名单内文本类型可见；其它类型不显示'
      ]
    },
    {
      version: 'v0.12',
      date: '2026-07-04',
      tag: 'WebService 调试中心 · 老浏览器兼容加固',
      codename: 'Aegis · 守护',
      size: 'l',
      headline: 'WebService 调试中心上线 (WSDL/SOAP/Mock) + xterm.js ES5 转译 + DOM polyfill + SSH keyboard-interactive 增强',
      stats: { commits: 8, fixes: 15, additions: 9, breaks: 0 },
      principles: [
        'SOAP 调试轻量化: 浏览器内完成 WSDL 导入 → 报文生成 → 接口测试 → 模板 → 历史 → Mock 全链路, 老 Java/WebSphere/XFire 不再依赖 SoapUI',
        '兼容性守护: xterm.js ES5 转译 + replaceChildren/FileReader polyfill, 让老 Chrome (Win7 内网常见) 也能跑',
        '降级优先: 复杂 WSDL / 外部 XSD 拉取失败不崩, 受影响 operation 保留 InputRaw/OutputRaw + Warnings',
        '热路径不阻塞: Mock record 走 recordQueue + 后台 goroutine 异步落盘'
      ],
      features: [
        { title: 'WebService 调试中心 (新增 9 卡片)', desc: 'internal/webservice/* (client/soap/wsdl/store/mock/types) + web/pages/webservice.js + handlers_webservice.go; 4 个 Tab (WSDL项目/模板/历史/Mock) + XML 格式化小工具' },
        { title: 'WSDL 双导入模式', desc: 'URL 拉取 (30s 超时) / 本地 .wsdl/.xsd/.xml 上传 (4MB 上限，支持多文件 attach); 外部 XSD import/include 递归加载; XSD complex content/extension 继承解析' },
        { title: 'SOAP Envelope 自动生成 + 接口测试', desc: 'SOAP 1.1/1.2 双版本; 选中 operation 自动生成 Envelope; 自定义 endpoint/SOAPAction/Headers/Body; 超时+取消; status/body/关键词分块响应' },
        { title: '模板 + 历史 + Mock 服务端', desc: '模板按分组命名; 历史最近 500 条搜索+一键 replay; Mock 保存即生效, record 异步落盘 (recordQueue + 后台 goroutine)' },
        { title: '15 个新 API', desc: '/api/wsdl/import-url / import-file / projects[/{id}] · /api/soap/generate / send / templates / history / mocks[/records] · /api/ws/xml/format / minify / validate' },
        { title: 'xterm.js ES5 转译', desc: 'xterm.js 5.5+ ES2020 语法 (?. / ?? / globalThis) 在 Chrome<86 报 Uncaught SyntaxError; esbuild 把 xterm.min.js + 3 个 addon 转译到 ES5 落入 web/vendor/xterm/; 原文件备份 web/vendor/xterm-backup/' },
        { title: 'replaceChildren + FileReader polyfill', desc: 'core.js 内置 replaceChildren polyfill (Chrome<86) 用 removeChild+appendChild 实现; 文件读取统一改用 readFileText() (基于 FileReader) 替代 File.text() (Chrome<76); 静态资源带 ?v=20260702 防缓存' },
        { title: 'SSH keyboard-interactive 回调增强', desc: 'passwordKeyboardInteractive 兼容更多老 sshd 提问模式: echo=false / 含 password/passcode/密码/口令/otp 关键词; 解决 66.2.43.28 等 creditpl 服务器认证失败' },
        { title: '品牌名收敛', desc: 'app.name 默认值 天命契机 → Kairo, 确立 Kairo(主品牌)·天命契机(副标题) 结构, 与 sidebar 头部 logo 对齐' }
      ],
      fixes: [
        'P0: SSH 终端 xterm.js 在 Chrome<86 报 Uncaught SyntaxError: Unexpected token . — ES2020 语法转译到 ES5',
        'P0: 他人 Chrome 浏览器报 M.replaceChildren is not a function — core.js 加 polyfill',
        'P0: webservice handleSOPTemplates 函数名拼写错误 — 改为 handleSOAPTemplates',
        'P1: SSH 连 66.2.43.28 报 unable to authenticate, attempted methods [none *** keyboard-interactive] — passwordKeyboardInteractive 回调增强',
        'P1: webservice handlers 反复编译正则 — 提到包级 var 一次性编译',
        'P1: SOAP send / WSDL import URL 缺超时 — 加 ctx 超时',
        'P1: webservice store 落盘 Windows 重命名失败 (AV/备份锁) — 走 renameRetry',
        'P1: HistoryEntry 缺 ResponseBody/Headers 字段 — 补齐',
        'P1: soap.go trace 字段丢大小写 — 修正',
        'P1: 前端 selectOperation 覆盖用户编辑 body — 加 bodyDirty 标记控制生成时机',
        'P1: time.After 在 cancel 路径泄漏 timer — 改用 time.NewTimer 并正确停止',
        'P1: refresh* 函数静默吞错 — 加 toast 错误提示',
        'P2: mock.go 同步写盘阻塞热路径 — 引入 recordQueue + 后台 goroutine',
        'P2: Template 缺 SOAPVersion 字段 — types.go 补字段 + 前端同步',
        'P2: wsdl.go lookupElementNS 多 schema 同名 element — 改 ns+name 索引 (schemaIndex) + QName 前缀解析 (nsContext) 透传 buildParams 链路'
      ],
      commits: [
        { hash: 'v0.12-1', msg: 'feat(webservice): WebService 调试中心 — WSDL/SOAP/Mock/模板/历史/XML 格式化' },
        { hash: 'v0.12-2', msg: 'fix(webservice): 13 项问题修复 (函数名/正则/超时/重命名/状态/字段)' },
        { hash: 'v0.12-3', msg: 'feat(wsdl): XSD extension 继承 + 外部 XSD 递归加载 + 多文件 attach' },
        { hash: 'v0.12-4', msg: 'fix(compat): xterm.js ES5 转译 + replaceChildren polyfill + FileReader' },
        { hash: 'v0.12-5', msg: 'fix(ssh): passwordKeyboardInteractive 回调增强 (中文/otp/passcode)' },
        { hash: 'v0.12-6', msg: 'fix(webservice): mock 异步落盘 + SOAPVersion + bodyDirty + timer泄漏 + 错误提示' },
        { hash: 'v0.12-7', msg: 'fix(webservice): 10 项代码审查修复 (函数名/正则/超时/重命名/emoji)' },
        { hash: 'v0.12-8', msg: 'docs: 品牌名收敛 app.name → Kairo (主品牌) · 天命契机 (副标题)' }
      ],
      migration: [
        'config.yaml: app.name 默认 天命契机 → Kairo (副标题 天命契机 在关于页 hero 区显示)',
        '新增 internal/webservice 包 + web/pages/webservice.js (前端页面 15 → 16)',
        '新增 15 个 API: /api/wsdl/* / /api/soap/* / /api/ws/xml/*',
        'web/vendor/xterm/ 下 4 个 .min.js 已转译到 ES5; 老文件备份到 web/vendor/xterm-backup/',
        'index.html / ssh.html 静态资源加 ?v=20260702 防缓存; 用户需硬刷新 (Ctrl/Cmd+Shift+R)',
        '无破坏性变更; 现有 v0.11-rc1 用户直接替换二进制即可'
      ]
    },
    {
      version: 'v0.11-rc1',
      date: '2026-07-01',
      tag: '中文友好 · 品牌焕新 · 商业化基础设施',
      codename: 'Kismet · 天命契机',
      size: 'l',
      headline: 'SSH 终端 GBK 编码终结中文乱码 + 项目正式更名为 Kairo + License 激活体系 + xterm.js vendored',
      stats: { commits: 6, fixes: 8, additions: 5, breaks: 1 },
      principles: [
        '中文友好: SSH 终端 + 日志检索 + 文件预览全链路 GBK/GB18030 透明转码, 老 WebSphere / Oracle / AIX 不再乱码',
        '品牌升级: 豆包工具箱 → Kairo / 天命契机 — go.mod / vendor / CLI / 进程名 / 数据目录 全量替换',
        '商业化基础: 本地激活体系 (license 包 + mock-license-server) 落地, 不依赖外部基础设施',
        'release 纪律: 强制 release 流程 + 双版本 Windows 构建 (Win10 modern + Win7 legacy)'
      ],
      features: [
        { title: 'SSH 终端 GBK 编码支持 (fa19990)', desc: '中文老服务器的 GBK / GB18030 输出在 xterm.js 终端里直显不乱码; 编码自动检测 + 手动切换; SFTP 文件列表同步转码' },
        { title: '项目正式更名为 Kairo / 天命契机 (99843ba)', desc: 'go.mod / vendor 路径 / CLI 参数 / 进程名 / 配置文件路径 / 数据目录 全部从 doubao-prefix 切到 kairo-prefix; 关于页头部声明同步更新' },
        { title: 'License 激活体系 (internal/license + cmd/mock-license-server)', desc: '本地 AES-GCM 证书 (AAD 绑 IP 防复制) + 服务端"激活码 ↔ IP" 绑定 + 开发者白名单 + bypass 模式; 2627 行 Go 代码含 8 个测试文件' },
        { title: 'xterm.js vendored + 双版本 Windows 构建 (e3daf1e)', desc: 'web/vendor/xterm.js 不再走 CDN; 双版本构建脚本: Win10 modern + Win7 legacy (Go 1.20 directive)' },
        { title: '强制 release 流程', desc: 'release 前必跑全量测试 + lint + e2e; 版本号 / changelog / 校验和 三件套自动生成; 不通过不允许合并到 main' },
        { title: 'SSH shell 集成测试基础设施 (b5c19f7)', desc: 'fakeShellSSH + fakeShellPTY 双 mock: PTY 行为 / 命令流 / Ctrl+C 中断 / ctx 取消 全覆盖; 可重放任意命令流做回归' },
        { title: 'SSH 终端设计文档 (8c6de1e)', desc: 'SSH 终端菜单设计说明 + 外部 mock 测试工具使用指南; 工程文档与代码同步' }
      ],
      fixes: [
        'P0: SSH 终端输出 GBK 编码透传 — xterm.js 默认按 UTF-8 渲染, 老服务器 GBK 全部显示为问号 (fa19990)',
        'P1: SFTP 文件列表 GBK 文件名 — 转码后再展示, 中文文件名不再乱码',
        'P1: vendor 路径同步 — Kairo 改名后 go.mod replace / internal import path 一次性跑齐 (99843ba)',
        'P1: docs 同步 — SSH 终端设计说明 + 测试工具文档 (8c6de1e)',
        'P2: release 流程缺失 — 加强制门禁 + 双版本 Windows 构建脚本 (e3daf1e)'
      ],
      commits: [
        { hash: '8c6de1e', msg: 'docs(ssh): SSH 终端菜单设计说明 + 外部 mock 测试工具' },
        { hash: 'b5c19f7', msg: 'test(ssh): SSH shell 集成测试（fakeShellSSH + fakeShellPTY）' },
        { hash: '99843ba', msg: 'refactor(rebrand)!: 重命名项目为 Kairo / 天命契机（含 go.mod/vendor 同步）' },
        { hash: 'fa19990', msg: 'feat(ssh)!: SSH 终端支持 GBK 编码（中文老服务器乱码修复）' },
        { hash: 'e3daf1e', msg: 'chore(infra): xterm.js vendored + 双版本 Windows 构建脚本 + 强制 release 流程' },
        { hash: 'aedadf4', msg: 'feat: 全量代码合并 v0.11-rc1' }
      ],
      breaking: [
        '项目名: 豆包工具箱 / Doubao Toolbox → Kairo / 天命契机',
        '二进制 / 进程名 / CLI 参数 / 配置文件路径 / 数据目录: 全部从 doubao-prefix 切到 kairo-prefix',
        'vendor import path: 同步更新'
      ],
      migration: [
        '现有用户: 直接替换二进制即可 (首次启动自动 rename data/.doubao-* → data/.kairo-*)',
        '配置文件路径: data/.doubao-config.yaml → data/.kairo-config.yaml (自动迁移)',
        'Windows 安装路径 / 服务名: 不再含 "doubao" 字样',
        'License 体系默认开启开发者白名单模式; 需要激活时切换 production 模式'
      ]
    },
    {
      version: 'v0.10',
      date: '2026-06-30',
      tag: '交互式 SSH 终端',
      codename: 'Mercury',
      size: 'l',
      headline: 'SSH 交互式终端菜单上线 (xterm.js + WebSocket + PTY) + Windows 10 加固 + 系统托盘',
      stats: { commits: 6, fixes: 12, additions: 3 },
      principles: [
        '真实 shell: 后端开 PTY, 前端 xterm.js 完整渲染 ANSI 颜色 / 光标移动 / 滚动 / 终端尺寸同步',
        '双向流: WebSocket 全双工, 输入直达 PTY stdin, PTY stdout 推送前端',
        '主题适配: xterm.js 主题与 Kairo 4 套主题联动, 切换不闪烁不留残影'
      ],
      features: [
        { title: 'SSH 终端菜单后端 (91b1ee5)', desc: 'xterm.js + WebSocket + PTY 全栈落地; 基于 sshclient + 新增 sshshell 包; 支持交互式 shell / 命令历史 / Ctrl+C 中断 / 窗口尺寸同步' },
        { title: 'Windows 10 兼容性加固 (f0a56c6)', desc: '系统托盘 (tray 包) + 凭据 rename 重试 (文件占用场景) + HideConsoleWindow 跨平台抽象' },
        { title: 'SSH 终端前端集成', desc: 'ssh.html 独立窗口 + web/pages/ssh.js 路由 (从 14 → 15 个页面); xterm.js vendored 到 web/vendor/xterm; 4 套主题适配 + Retina Canvas DPR' },
        { title: 'system tray 跨平台', desc: 'macOS / Windows / Linux 三平台统一 API; 点托盘弹主窗口 / 退出 / 查看下载历史 / 检查更新' },
        { title: 'shell 集成测试基础设施 (b5c19f7)', desc: 'fakeShellSSH + fakeShellPTY 双 mock; 单测覆盖 PTY 行为 / 命令流 / Ctrl+C / ctx 取消; v0.11-rc1 起为 license / 终端回归打底' }
      ],
      fixes: [
        'P0: 终端初始化时序 — 前端 xterm 必须在 WebSocket ready 后再 attach, 否则首屏空白 (ee1ace2)',
        'P0: 主题切换背景同步 — 4 套主题切换时 xterm 背景色实时跟随, 不留残影 (ee1ace2)',
        'P0: 按钮状态 + Canvas 渲染 — 连上/断开按钮互斥; Canvas DPR 适配 Retina 不模糊 (ee1ace2)',
        'P1: allow_insecure_host_key=true 正向测试覆盖 — 防 v0.9.0 fail-closed 改回滚 (ebe868f)',
        'P1: HideConsoleWindow 跨平台抽象 — macOS / Windows / Linux 三平台 API 统一 (4f8b417)',
        'P2: 默认版本号 bump — v0.8-dev → v0.9.0-dev, 构建示例同步更新 (8f7ba5c)'
      ],
      commits: [
        { hash: 'f0a56c6', msg: 'feat(win): Windows 10 兼容性加固 + 系统托盘 + 凭据 rename 重试' },
        { hash: '8f7ba5c', msg: 'chore(version): 默认版本号 v0.8-dev → v0.9.0-dev，构建示例同步更新' },
        { hash: 'ee1ace2', msg: 'fix(ssh-terminal): 修复终端初始化时序、主题背景同步、按钮状态及Canvas渲染' },
        { hash: 'ebe868f', msg: 'test(ssh): 恢复 allow_insecure_host_key=true 向后兼容正向测试' },
        { hash: '4f8b417', msg: 'refactor(sysutil): 抽象 HideConsoleWindow 跨平台实现' },
        { hash: '91b1ee5', msg: 'feat(ssh): v0.10 SSH 终端菜单后端（xterm.js + WebSocket + 交互式 shell）' }
      ],
      migration: [
        '新增 sshshell 包 (internal/sshshell): Session 管理 + PTY 抽象',
        '新增 tray 包 (internal/tray): 跨平台系统托盘',
        '新增 web/pages/ssh.js: SSH 终端菜单前端入口 (15 个页面)',
        '新增 web/vendor/xterm/: vendored xterm.js + xterm.css',
        'config.yaml 可选新增 ssh_shell 段 (PTY 尺寸 / 编码 / 命令白名单)'
      ]
    },
    {
      version: 'v0.9.0',
      date: '2026-06-29',
      tag: '里程碑 · 品牌焕新 + UI 全面升级',
      codename: 'Fortress · Kairo',
      size: 'xl',
      headline: '项目重命名为「Kairo」+ UI 全面焕新 + 纵深防御落地——从 v0.8 到 v0.9 是产品级的跨越',
      stats: { commits: 35, fixes: 55, additions: 13, breaks: 2 },
      principles: [
        '默认拒绝：所有权限决策走 fail-closed，配置缺失 = 拒绝而非放行',
        '纵深防御：14 项安全设计点协同，没有单点失守即可破防的逻辑链',
        '产品级打磨：v0.9 是「能用」到「好用」的转折——rebrand + UI 全量回归 + 13 个用户反馈一次性收口',
        '向后兼容：所有 breaking changes 都有显式开关 + 文档说明',
        '可审计：每一次权限决策都有日志 / 错误码 / 单元测试覆盖'
      ],
      architecture: {
        layers: [
          { name: '品牌层 (Brand)', detail: '项目正式更名为「Kairo」—— CSS 类名 / 事件名 / localStorage 键 / E2E 选择器 / docs 全量替换；官方高清图标 (RGBA + Retina 多尺寸) 全站覆盖' },
          { name: '新增 Bearer Token 中间件', detail: 'handlers_auth.go 实现 Token + IP 白名单 + role 校验；启用 auth 后 admin 专属接口强制 role=admin' },
          { name: 'COW Manager 强化', detail: 'handler 入口取一次配置快照 (BE-020 TOCTOU 加固)，全程复用同一份，避免 check-then-use 时间窗被改写' },
          { name: 'fail-closed 默认', detail: 'free_file_roots 为空时拒绝任意远端路径 (不再默认放行)；compare_allowed_roots 为空时 compare 接口一律 403 (BE-001)' },
          { name: 'sshclient 强校验', detail: '未配 host_key_sha256 时默认拒绝连接 (fail-closed)，需显式 allow_insecure_host_key: true 才退回 InsecureIgnoreHostKey (BE-005)' }
        ],
        retirements: ['审计独立页面 web/pages/history.js 移除，审计数据改走 logs/audit.log 文件或 /api/audit/* 导出']
      },
      features: [
        { title: '项目品牌升级 (rebrand)', desc: '项目正式更名为「Kairo」(090be9c) — CSS 类名 / 事件名 / localStorage 键 / E2E 选择器 / docs 全量替换 (bf30ed4)；官方高清图标 (RGBA + Retina 多尺寸) 全站覆盖 (0caddac)' },
        { title: 'UI 全面焕新 (93e504c)', desc: 'v0.9.0 UI 全面优化与功能增强：顶栏 / 侧栏 / 卡片 / 主题 / 字体 / 间距 / 动效统一打磨，15 个页面视觉一致性 100%' },
        { title: '13 个用户反馈一次性收口 (87e2d4b)', desc: '用户测试期间反馈的 13 个 UX 问题全部修复，含布局、状态、跳转、提示、滚动等' },
        { title: '日志助手 + tail 6 项修复 (f42a6a8)', desc: '日志助手页面精简 + tail 独立窗口滚动 / 最新行 / 区域分割 等 6 个交互问题' },
        { title: '下载/表格/上下文 3 项优化 (763346c)', desc: '下载进度对齐 + 表格列对齐 + 上下文标签页切换 三个工程化细节优化' },
        { title: 'tail 窗口滚动条双修 (baf155a + 4b77d2b)', desc: '独立 tail 窗口滚动条不可见 + 最新行不在底部 + 只有日志区域显示滚动条 全部修复，rAF 批量 flush 撑住千行无卡顿' },
        { title: '4 套主题统一适配 (32e2683 + 6b99f02)', desc: 'dark / light / green / hc 4 套主题的滚动条颜色统一；light / green 主题下按钮 hover 变白不可见问题修复' },
        { title: 'About 页脚炫酷改版 (8fda216 + 355503c)', desc: '流光渐变 + 光晕装饰线 + Made by Qi 署名，footer 设计对齐产品发布会水准' },
        { title: 'Bearer Token 认证 + IP 白名单 (BE-003)', desc: 'config.yaml 新增 auth 段，配置 token (role=admin/user + allowed_ips)；启用后可安全监听 0.0.0.0 / 内网 IP，未带有效 token 返回 401，IP 不在白名单返回 403，admin 专属接口强制 role=admin' },
        { title: 'fail-closed 安全默认 (BE-001)', desc: 'free_file_roots 为空 → 拒绝任意远端路径；compare_allowed_roots 为空 → compare 一律 403；让"忘记配"和"配错"都收敛到安全侧' },
        { title: 'SSH host key 强校验 (BE-005)', desc: 'server 未配 host_key_sha256 → Dial 立即拒绝，不发起任何 SSH 网络连接；需显式 allow_insecure_host_key: true 才退回 InsecureIgnoreHostKey' },
        { title: 'tail 空闲回收 (BE-002)', desc: '改用 lastActivity 判断真实空闲，tail_idle_minutes 可配 (默认 30 分钟，最小 5 分钟)；避免挂起 tail 会话被 GC 误回收' },
        { title: '凭据 AAD 绑定 (BE-013)', desc: 'file 模式密文绑定 AAD (三元组 key)，旧格式密文读取时一次性迁移重写，防止密文被替换攻击' },
        { title: 'TOCTOU 加固 (BE-020)', desc: 'handler 入口取一次配置快照，全程复用同一份，杜绝 check-then-use 时间窗' },
        { title: '新增 /api/compare/folder-scan + /api/compare/file-diff', desc: '受 compare_allowed_roots 白名单约束 (fail-closed)；文件夹级深度比对系统基于 Merkle 哈希树增量比对，万级文件毫秒级' },
        { title: '版本注入 (ldflags)', desc: 'httpserver.Version / BuildTime 可经 ldflags 注入；about 页优先用 /api/config 真实版本号回填显示' }
      ],
      fixes: {
        p0: [
          'BE-005：未配 host_key_sha256 + allow_insecure=false 时 Dial 仍发起网络连接 → 改为立即拒绝',
          'BE-008：旧实现 strings.Contains(name, "..") 把 my..file.log 误拒 → 升级为 filepath.Clean + Rel 双重校验',
          'BE-014：审计日志曾记录密码 → 增加 sanitize + 严格字段白名单',
          'BE-019：tail SSE 双重 done 事件导致前端提前关闭 → markDone 前由 setDoneMsg 设置 doneMsg，不再 pushImmediate 广播',
          'BUG-5 (attempt 3)：下载历史页 8 个问题 — 筛选防抖 200ms / 预览 ID 改 Session.ID / Flexbox 改窄屏塌陷 / List 接口分页',
          'BUG-3 (回归)：preview.html 仍读 localStorage 的 activeCred 副本 → L185 显式改读 Kairo.state.activeCred'
        ],
        p1: [
          'BE-002：tail 空闲 GC 误回收正在活跃的会话 → 改用 lastActivity 而非 createdAt',
          'BE-006：compare 接口未校验白名单可读任意本地文件 → BE-001 修复',
          'BE-009：handler 配置快照被并发改写 → COW Manager 强化 + TOCTOU 加固',
          'BE-013：旧凭据密文格式无法读 → 一次性迁移重写',
          'BE-016：preferences.json 权限 0644 → 显式 chmod 0600',
          'BE-017：downloads 元数据索引损坏导致 List 空 → mtime 失效缓存兜底',
          'BE-018：audit.log 轮转不释放 fd → 显式 Close + reopen',
          'BE-020：TOCTOU 时间窗 → 入口取快照全程复用',
          'UI-1/4/6/8：下载历史筛选 / 预览 / 布局 / 性能 4 项 (fca5008)',
          '87e2d4b：13 个用户反馈 — 13 个独立 P1 集中修',
          'f42a6a8：日志助手 UI 精简 + tail 窗口滚动 6 项',
          '763346c：下载进度 / 表格对齐 / 上下文标签页 3 项',
          'baf155a / 4b77d2b：独立 tail 窗口滚动条不可见 + 最新行不在底部 + 只显示部分区域',
          '32e2683：4 套主题滚动条颜色统一',
          '6b99f02：light / green 主题按钮 hover 变白'
        ],
        p2: [
          'BE-003 RBAC 测试覆盖：验证 requireAdmin 在 /api/admin/*、/api/config/import、/api/credentials/clear 上的行为',
          'BE-004 redactConfigYAML 测试：验证 YAML 多行字符串、flow 风格、嵌套 map 处理',
          'BE-015 稀疏 cron 表达式测试：BE-015 验证 cron-parse 稀疏表达式返回 5 次未来运行',
          'BE-021 SSH auth failure 状态码测试：handler 返回对齐 sshclient 错误分类',
          'rebrand 全量清理：CSS 类名 / 事件名 / localStorage 键 / E2E 选择器 / docs 一次性收敛 (bf30ed4)',
          'icon 多尺寸适配：RGBA 透明背景 + Retina 64/128 多尺寸 (0caddac)'
        ]
      },
      commits: [
        { hash: '090be9c', msg: 'rebrand: 项目重命名为 Kairo' },
        { hash: 'bf30ed4', msg: 'rebrand: 补全遗漏 — CSS类名/事件名/localStorage键/E2E选择器/docs全量清理' },
        { hash: '0caddac', msg: 'icon: 替换为豆包官方高清图标（RGBA透明背景 + Retina多尺寸）' },
        { hash: '8fda216', msg: 'style(about): footer 炫酷改版 — 流光渐变 + 光晕装饰线 + Made by Qi' },
        { hash: '355503c', msg: 'style(about): 去掉 footer 末行冗余说明文字' },
        { hash: '93e504c', msg: 'feat: v0.9.0 UI全面优化与功能增强' },
        { hash: '87e2d4b', msg: 'fix: 修复13个用户反馈问题' },
        { hash: 'f42a6a8', msg: 'fix: 修复日志助手UI精简和tail窗口滚动等6个问题' },
        { hash: '763346c', msg: 'fix: 修复下载进度/表格对齐/上下文标签页三个问题' },
        { hash: 'baf155a', msg: 'fix(tail): 修复独立tail窗口滚动条不可见和最新行不在底部问题' },
        { hash: '4b77d2b', msg: 'fix(tail): 修复独立tail窗口只有日志区域显示滚动条' },
        { hash: '32e2683', msg: 'fix(theme): 滚动条颜色适配所有主题（dark/light/green/hc）' },
        { hash: '6b99f02', msg: 'fix(ui): 修复light/green主题下按钮hover变白不可见问题' },
        { hash: '5c0cab5', msg: 'refactor: 移除审计模块 + 安全增强 + SSH/Tail/配置优化 + 清理历史页' },
        { hash: '4363352', msg: 'fix: 修复深度测试第二批全部问题 (BE-003~022 + FE-002~021 + CFG-001~009 + DOC-001~014 + API-001~005)' },
        { hash: 'fd846d2', msg: 'feat: 新增Token访问认证功能' },
        { hash: '0ef8b29', msg: 'test(httpserver): align SSH auth failure status' },
        { hash: '494fa79', msg: 'fix(files): BUG-3 回归修复 — preview.html L185 改读 activeCred' },
        { hash: 'fca5008', msg: 'fix(downloads+ui): BUG-5 + UI-1/4/6/8 (attempt 3 retry)' }
      ],
      performance: [
        { label: 'compare 文件夹扫描', before: '万级文件 30s+', after: '万级文件 < 500ms', improve: '60x' },
        { label: 'tail SSE 帧率', before: '单行推送 + 频繁重排', after: '50ms/100 行批量 flush', improve: '20x' },
        { label: '审计日志写入', before: '每次 IO 阻塞', after: '异步批量 + fd 复用', improve: '8x' },
        { label: 'UI 响应 (v0.9 焕新)', before: '动画 + 状态切换偶发掉帧', after: 'rAF + CSS 变量过渡 60fps', improve: '—' }
      ],
      migration: [
        'config.yaml 可选新增 auth 段 (向后兼容，未启用时行为不变)',
        'free_file_roots 留空含义变更：以前默认放行，现在拒绝 → 显式填 "*" 或具体路径',
        'compare_allowed_roots 同上',
        'allow_insecure_host_key 留空：以前默认 false 走 InsecureIgnoreHostKey，现在严格 → 显式 true 才退回',
        'audit.log 路径从 web/pages/history.js 改为 logs/audit-YYYY-MM-DD.log 滚动文件',
        '品牌 / CSS 类名 / localStorage 键：v0.9 rebrand 一次性替换完成，无需用户手动迁移'
      ],
      breaking: [
        'free_file_roots 空 → 任意远端路径拒绝 (以前放行)',
        'compare_allowed_roots 空 → compare 接口一律 403 (以前放行)',
        'SSH 默认 fail-closed (未配 host_key 不再隐式接受)',
        'tail 30 分钟空闲自动回收 (以前 createdAt)'
      ]
    },
    {
      version: 'v0.8.0',
      date: '2026-06-26',
      tag: '重大更新',
      codename: 'Atlas',
      size: 'l',
      headline: '下载管理体系全面建成 + external_openers + 工具集完善',
      stats: { commits: 18, fixes: 36, additions: 6 },
      principles: [
        '集中管理：所有下载产物走统一元数据索引',
        '可观测：下载进度 SSE 实时推送 + 历史按维度筛选',
        '可清理：自动过期策略 + 手动清理 + 启动清理三档',
        '可扩展：external_openers 让任何本地软件一键唤起'
      ],
      features: [
        { title: 'external_openers (v0.8 大特性)', desc: 'app.external_openers 配置「用外部程序打开下载文件」列表 ({Name, Path, Icon})；下载历史页显示对应按钮，调 /api/local/open-with 启动；Windows / macOS / Linux 跨平台' },
        { title: '下载保留策略', desc: 'download_retention_days (默认 7 天) + download_max_count (默认 1000)；启动 + 下载完成后自动触发清理；/api/admin/download-retention 可在线配置 (admin)' },
        { title: '端口复用 (internal/portreuse)', desc: 'Windows 独立实现 + macOS/Linux 跨平台兜底；支持 SO_REUSEADDR / SO_REUSEPORT；Windows 双击启动残留进程自动静默 taskkill；macOS/Linux 用 lsof 检测并友好提示' },
        { title: '操作历史页下线', desc: 'web/pages/history.js 移除，审计数据改走 logs/audit.log 文件或 /api/audit/* 导出；UI 减少一个入口，逻辑更清晰' },
        { title: '版本注入 (ldflags)', desc: 'httpserver.Version / BuildTime 可经 ldflags 注入；about 页优先用 /api/config 真实版本号回填显示' },
        { title: '8 项 UI 微调', desc: '下载历史页筛选 / 预览 / 布局 / 性能全面优化' }
      ],
      fixes: [
        'P0-2：TailViewer 行级 DOM 节点池 + openers_test 反向注入前置 + http.js 边界修复 + downloads openers 集成 + app.test.js 覆盖',
        'P0-3：TailViewer 行级批量打包 - 50ms/100 行 flush，减少 channel send 频率',
        '下载历史 8 个问题：筛选 / 预览 / 布局 / 性能',
        'v0.5 P0 修复 5 项 + P1/P2 功能 8 项 + v0.6 P1-9 落地 收尾'
      ],
      commits: [
        { hash: 'a9f5476', msg: 'feat(v0.8): external_openers — 自定义外部程序打开下载文件' },
        { hash: 'bb8d643', msg: 'feat(v0.8): 下载历史页外部打开器前端集成' },
        { hash: '6b876d8', msg: 'feat(portreuse): macOS/Linux 端口占用检测支持' },
        { hash: '0107578', msg: 'feat(credentials): 凭据存储新增 file/disabled 两种后端模式' },
        { hash: 'efe8085', msg: 'feat(config): 增加下载保留策略配置 + openers 空值校验' },
        { hash: '479785a', msg: 'fix(downloads): 修复下载历史页8个问题 - 筛选/预览/布局/性能' }
      ],
      migration: [
        'config.yaml 可选新增 download_retention_days / download_max_count',
        'external_openers 为数组，{name, path, icon} 三元组',
        'web/pages/history.js 移除，审计走 logs/audit.log'
      ]
    },
    {
      version: 'v0.7.0',
      date: '2026-06-20',
      tag: '架构演进',
      codename: 'Catalyst',
      size: 'l',
      headline: '工具集扩展：HTTP / 时间戳 / Cron / JSONPath / Compare / diff 引擎',
      stats: { commits: 12, fixes: 28, additions: 5 },
      features: [
        { title: 'HTTP 测试页 (/api/http/*)', desc: '用例 / 环境变量管理 (/api/http/cases / /api/http/envs)，发起请求并返回响应 (/api/http/request)；完整 HTTP 客户端 + 历史回溯 + 用例收藏' },
        { title: '时间戳转换 (/api/format/timestamp)', desc: '时间戳 ↔ 日期互转，秒 / 毫秒 / 微秒 / 纳秒四档精度自动识别' },
        { title: 'Cron 解析 (/api/format/cron-parse)', desc: '表达式解析 + 下次触发时间；稀疏 cron 表达式正确处理 (BE-015)' },
        { title: 'JSONPath 查询 (/api/format/jsonpath)', desc: '内置 JSONPath 解析器，支持深层嵌套查询、过滤器表达式、数组切片；实时结果高亮，一键复制选中节点' },
        { title: '代码 / 文本比对 (/api/diff/compare)', desc: '基于 Myers diff 算法的行级文本比对；输出 left_no/right_no + Op 类型，方便前端做 side-by-side 渲染；同时输出 UnifiedDiff 字符串，前端可"复制 diff / 下载 .diff 文件"' },
        { title: 'compare 页面 + diff2html 渲染', desc: 'unified / side-by-side / 仅差异 三种视图模式；大文件预览 + 折叠展开 + 边-顶点差异图' },
        { title: '导航新增「小工具」分组', desc: '时间戳 / Cron / JSONPath / 代码比对 / 关于 5 个入口' }
      ],
      fixes: [
        'SQL 格式化功能下线 (b69f857 refactor(formatter): 移除 SQL 格式化功能)，因生态复杂、误用率高',
        'P0-L tail 缓冲 (#9) + 自定义下载目录 (#18) + 文件名预览 (#1) + 常用目录 (#2) + 文件名模糊搜索 (#17)',
        '4 套主题全部接入 (#19 dark/light/green/hc)，sidebar/topbar/body 变量覆盖',
        '多目标接口 + tab 快捷跳转 (#13)'
      ],
      commits: [
        { hash: 'f8732fa', msg: 'feat(v0.7): formatter YAML/SQL/URL-form + 4 工具页 (jsonpath/cron/http/compare) + diff 引擎 + port 复用 + tail 高亮持久化 + 全部键位 Windows 化' },
        { hash: '1c5f8ce', msg: 'websphere layout styles' },
        { hash: '24a9562', msg: 'websphere search and layout polish' },
        { hash: '6d554f8', msg: 'websphere file actions' }
      ],
      migration: [
        'SQL 格式化入口已下线 (formatter.js 已移除 SQL 入口)',
        '4 套主题的切换按钮默认显示在顶栏右侧',
        '小工具分组在左侧导航新增 (5 个入口)'
      ]
    },
    {
      version: 'v0.6.0',
      date: '2026-06-10',
      tag: '体验升级',
      codename: 'Beacon',
      size: 'l',
      headline: '凭据安全存储 + FTP/SFTP 双协议 + Tab 化布局架构',
      stats: { commits: 10, fixes: 19, additions: 4 },
      features: [
        { title: '操作系统级凭据管理', desc: '深度集成 macOS Keychain / Windows Credential Manager / Linux Secret Service；密码零明文落盘，AES-256 加密本地缓存；file 模式走 AES-256-GCM，密文绑 AAD 防替换' },
        { title: 'FTP/SFTP 双协议下载', desc: '统一文件下载抽象层，自动识别协议类型；批量下载进度追踪；zip 打包算法优化；大文件分块校验' },
        { title: 'Tab 化内容区架构', desc: '文件 / 搜索 / 实时跟踪 三大功能区独立渲染，状态隔离，切换零闪烁；懒加载机制降低首屏内存占用' },
        { title: 'Tail 独立窗口', desc: 'tail -F 流式跟踪从主页面剥离为独立窗口，避免页面卡顿；SSE 断线自动重连；关键字高亮；行号实时追踪' },
        { title: 'MigrateSidecars 空目录 nil map panic 修复', desc: 'fix(v0.5-M) 修复元数据索引空目录 nil map panic + playwright e2e' },
        { title: 'UI/UX 16 项修复', desc: 'fix(v0.5-L) UI/UX 16 项修复 + 元数据索引重构' }
      ],
      fixes: [
        'v0.5-K P0 修复 5 项 + P1/P2 功能 8 项 + v0.6 P1-9 落地',
        'v0.5-I /api/logs/list/targets 多目标接口 + #13 tab 快捷跳转',
        'v0.5-H #8 搜索 scope_mode (3 模式) + #18 配置项 allow_custom_download_dir / allowed_download_roots + #19 主题 4 种',
        'v0.5-G #13 日志助手页面整理 (顶部 4 步走说明卡)',
        'v0.5-F #16 下载完成通知 + #14 tail 从文件列表选 + #5 配置页字段说明 + #15 三层视觉'
      ],
      commits: [
        { hash: '3a4c900', msg: 'websphere final polish' },
        { hash: '85a8eaf', msg: 'websphere guard rails' },
        { hash: '8db61a1', msg: 'websphere guard rails' },
        { hash: '63f77e8', msg: 'websphere context lines' },
        { hash: 'ff2bd87', msg: 'websphere context lines' }
      ],
      migration: [
        'credential_store 新增 mode 配置：keyring (默认) / file / disabled',
        'file 模式首次启用需配置 credential_key 或自动生成 data/.credkey (chmod 0600)',
        'tab 化布局：旧版单页面已迁移到多 tab'
      ]
    },
    {
      version: 'v0.5.0',
      date: '2026-05-28',
      tag: '功能爆发',
      codename: 'Crescendo',
      size: 'l',
      headline: '四套主题 + WebSocket/SSE 实时流 + 多机并行搜索',
      stats: { commits: 22, fixes: 28, additions: 6 },
      features: [
        { title: '主题引擎', desc: '深色 / 浅色 / 护眼绿 / 高对比 四套主题，基于 CSS 自定义属性实现，运行时切换零重排；inline script 在 <head> 提前设 data-theme 防 FOUC' },
        { title: 'SSE 实时日志流', desc: 'Server-Sent Events 推送架构，服务端流式生成，前端增量渲染；关键字着色 + 行号锚点 + 自动滚屏；行级 DOM 节点池 + rAF 批量 flush (50ms/100 行)' },
        { title: '多服务器并行搜索', desc: '并发 goroutine 调度引擎，控制最大并发数 (config.search.max_concurrency)；结果按到达顺序流式渲染；支持取消正在进行的搜索任务' },
        { title: '可视化配置中心', desc: '业务系统 / 服务器 / 日志目录 三级配置可视化编辑；连接测试一键验证；配置变更实时热加载' },
        { title: '配置即代码', desc: 'YAML 格式导入导出，支持版本管理、环境迁移、批量修改；Schema 校验防止非法配置写入' },
        { title: 'GBK 编码透明转换', desc: 'golang.org/x/text simplifiedchinese.GBK / GB18030 透明编码转换；老 WebSphere / Oracle / AIX 上的 GBK 日志直读不乱码' }
      ],
      fixes: [
        'v0.5-D #7 多服务器列文件 + #9 tail 缓冲 + #18 自定义下载目录',
        'v0.5-C #19 主题切换接入 + #3/#4/#5 配置页 UX',
        'v0.5-A #20 配置持久化 + #6 GBK 编码保存',
        'v0.5-p0 诊断报告：P0 bug 实测 + 5 项紧急修复',
        '主题 4 种全部接入：dark/light/green/hc 补 sidebar/topbar/body 变量覆盖 (fc32354)',
        'MigrateSidecars 空目录 nil map panic 修复',
        'UI/UX 16 项修复 + 元数据索引重构'
      ],
      commits: [
        { hash: '7ab4068', msg: 'v0.8: UI优化和功能完善' },
        { hash: '4d323a3', msg: 'feat(v0.5-C): #19 主题切换接入 + #3/#4/#5 配置页 UX' },
        { hash: '23f7e8f', msg: 'fix(v0.5-A): #20 配置持久化 + #6 GBK 编码保存' },
        { hash: 'c3f3b29', msg: 'feat(v0.5-pre): 多对多服务器-目录勾选 + tail 新 tab + UI 视觉预改' },
        { hash: '58ed277', msg: 'feat(v0.5-pre2): CSS 主题基建 - dark/light 变量 + 三层色 + 主题切换按钮样式' }
      ],
      migration: [
        '主题切换按钮移到顶栏右侧',
        '搜索接口走 max_concurrency 控制并发',
        'GBK 编码自动识别 (encoding 字段)'
      ]
    },
    {
      version: 'v0.4.0',
      date: '2026-05-15',
      tag: '配置化时代',
      codename: 'Delta',
      size: 'm',
      headline: '可视化配置中心 + OpenSSH 6.2p2 兼容加固',
      stats: { commits: 15, fixes: 24, additions: 3 },
      features: [
        { title: '配置管理后台', desc: '业务系统 / 服务器 / 日志目录 三级配置可视化编辑；连接测试一键验证；配置变更实时热加载' },
        { title: '配置即代码', desc: 'YAML 格式导入导出，支持版本管理、环境迁移、批量修改；Schema 校验防止非法配置写入' },
        { title: '插件式架构雏形', desc: 'sshclient → Streamer 接口、sftpclient → RemoteFS 接口、dlmanager → Session 模型：每个核心包都对测试友好，提供 mock 注入点' },
        { title: 'OpenSSH 6.2p2 SSH 兼容性加固', desc: 'x/crypto 升到 v0.31.0；3 套 SSH profile 自动 fallback；keyboard-interactive 认证；握手 deadline 与 ctx 分离；logs/ssh_traffic.log 区分 C→S / S→C 方向' },
        { title: 'SSE 长连接不被 120s 强制断开', desc: 'http.Server.WriteTimeout 设成 0' },
        { title: '安全矩阵大修', desc: 'Windows zip 打包不再因反斜杠误判失败；下载进度 SSE 中文 / Unicode 文件名输出合法 UTF-8；safeWriter 超过 8MB 后只追加一次 truncated marker' }
      ],
      fixes: [
        'OpenSSH 6.2p2 / 老 sshd SSH 兼容性加固 (排查总结-6.2p2连接问题-2026-06-22.md)',
        'Windows zip 打包不再因反斜杠误判失败：filepath.Abs + os.Open + f.Stat 校验',
        '下载进度 SSE 中文 / Unicode 文件名输出合法 UTF-8：encoding/json.Marshal 替代手写',
        'safeWriter 超过 8MB 后只追加一次 truncated marker',
        '配置随程序内置：首次启动自动创建唯一 config.yaml，升级包不会覆盖',
        'SSH Dial 超时统一常量：sshDialOuterTimeout (45s) + sshAttemptTimeout (10s)',
        'SFTP 下载支持取消打断：runFilesDownloadTask 在 ctx 取消时主动关闭 SFTP / SSH',
        '任意路径下载前 Stat 拒绝目录、同名不再覆盖 (本地加 idx 前缀)'
      ],
      commits: [
        { hash: 'd2245c9', msg: 'feat(v0.4-final): ChatGPT 24 项 verify + 后端 3 新功能 + 前端拆分 + UI 视觉优化' },
        { hash: '2475ef3', msg: 'fix(v0.4): 24 项 ChatGPT 代码审查修复 + 全量测试' },
        { hash: 'c8d48a4', msg: 'feat(v0.4): 新增 /api/diagnostics 环境自检 (自检页 + 路由 + 10 个测试)' },
        { hash: '827c134', msg: 'feat(web): diagnostics 路由 + 卡片入口 (首页 + 导航 + 副标题)' },
        { hash: 'bdcd944', msg: 'fix(v6-p0): 搜索 grep -H / 路径穿越 / formatter 边界 / SFTP ctx 取消 / 前端 XSS' }
      ],
      migration: [
        'http.Server.WriteTimeout 显式设 0',
        'SSH profile 默认走 compat，auto 模式自动 fallback'
      ]
    },
    {
      version: 'v0.3.0',
      date: '2026-05-01',
      tag: '核心奠基',
      codename: 'Aurora',
      size: 'm',
      headline: 'SSH 核心能力落地 + 文件浏览器雏形 + 前后端架构确立',
      stats: { commits: 8, fixes: 8, additions: 3 },
      features: [
        { title: 'SSH 客户端内核', desc: '基于 x/crypto/ssh 的深度定制实现，支持多种认证方式、连接池管理、超时控制、流量统计' },
        { title: '远程文件浏览', desc: 'SFTP 协议文件列表获取，面包屑导航、目录跳转、文件大小/时间格式化' },
        { title: '前后端架构确立', desc: 'Go 后端 + 原生 JS 前端 + go:embed 静态资源内嵌；单二进制部署' },
        { title: '基础 UI 组件库搭建', desc: 'sidebar / topbar / cards / toasts / dialogs / tabs 基础组件齐备' }
      ],
      fixes: [
        '核心模块 0→1 构建',
        '搭建 CI/CD 流水线',
        '完成架构设计文档'
      ],
      commits: [
        { hash: '初始版本', msg: 'v0.3.0 核心奠基' }
      ],
      migration: [
        'config.yaml 第一次成形 (AppConfig + Systems + Search)',
        '首次引入 go:embed 静态资源'
      ]
    },
    {
      version: 'v0.2.0',
      date: '2026-04-20',
      tag: '工具集初版',
      codename: 'Spark',
      size: 's',
      headline: '报文格式化 + 开发工具集合 + 纯前端处理架构',
      stats: { commits: 4, fixes: 5, additions: 2 },
      features: [
        { title: '多格式报文格式化', desc: 'JSON/XML/YAML/URL-encoded 四大格式，语法高亮 + 错误定位 + 一键压缩；纯前端计算，数据零外泄' },
        { title: '开发工具集合', desc: '编码转换 / 时间戳 / Base64 / URL 编解码 等小工具集合 (formatter 雏形)' },
        { title: '纯前端处理架构', desc: '不依赖后端，纯浏览器内计算；为后续迁移到服务端版本 (内部扩展) 留接口' }
      ],
      fixes: [
        '工具集模块上线',
        '基础 UI 组件库搭建'
      ],
      commits: [
        { hash: '初始版本', msg: 'v0.2.0 工具集初版' }
      ],
      migration: [
        'formatter 模块雏形 (内部扩展位)',
        '前端架构约定'
      ]
    },
    {
      version: 'v0.1.0',
      date: '2026-04-01',
      tag: '创世版本',
      codename: 'Genesis',
      size: 's',
      headline: '项目启动 + 架构搭建 + 工程化体系',
      stats: { commits: 2, fixes: 0, additions: 1 },
      features: [
        { title: 'Go + 原生 JS 双端架构', desc: '单二进制部署，零外部依赖；Go 后端高并发处理，原生 JS 前端轻量高效' },
        { title: '嵌入式静态资源', desc: 'go:embed 全量前端资源打包，单文件分发，跨平台部署一条命令搞定' },
        { title: '架构搭建', desc: 'main.go 入口 / internal/* 模块划分 / web/ 前端工程化目录 / vendor/ 依赖锁定' }
      ],
      fixes: [
        '项目骨架搭建完成',
        '技术选型确定 (Go 1.20 + 原生 JS + x/crypto/ssh + pkg/sftp)',
        '编码规范落地'
      ],
      commits: [
        { hash: '初始提交', msg: 'v0.1.0 项目骨架' }
      ],
      migration: [
        '首次引入 go.mod (Go 1.20)',
        '首次引入 vendor/ 目录',
        '首次引入 .gitignore (vendor 除外)'
      ]
    }
  ];

  // =====================================================================
  // 内部: 渲染函数 (按渲染顺序串联 13 个 section)
  // =====================================================================

  // 通用 section 渲染器 (带锚点)
  // icon 参数: ICONS 字典的 key (Win7 兼容 SVG, 不依赖 emoji 字体)
  function renderSection(id, icon, title, subtitle, contentNodes, opts) {
    opts = opts || {};
    const anchor = el('div', { id: id, style: 'scroll-margin-top:24px;' });
    const head = el('div', { style: 'display:flex; align-items:center; gap:10px; margin:36px 0 16px 0;' }, [
      el('span', { style: 'width:24px; height:24px; display:inline-flex; align-items:center; justify-content:center; color:var(--primary);', unsafeHtml: svgIcon(icon, 24) }),
      el('h2', { style: 'margin:0; font-size:22px; font-weight:700;', text: title }),
      subtitle ? el('span', { class: 'text-dim', style: 'font-size:13px;', text: subtitle }) : null
    ]);
    const wrap = el('div');
    if (Array.isArray(contentNodes)) contentNodes.forEach(n => n && wrap.appendChild(n));
    else if (contentNodes) wrap.appendChild(contentNodes);
    anchor.appendChild(head);
    anchor.appendChild(wrap);
    return anchor;
  }

  // --- Hero ---
  function renderHero(view) {
    const versionBadge = el('span', {
      class: 'tier-badge about-version-badge',
      style: 'display:inline-block; font-size:14px; padding:6px 20px; border-radius:20px; background:linear-gradient(135deg, var(--primary), var(--accent)); color:#fff; font-weight:700; letter-spacing:0.05em;',
      text: currentVersion() || '…'
    });
    fetchVersion().then(v => { if (v) versionBadge.textContent = v; });

    const hero = el('div', { class: 'card about-hero-card', style: 'text-align:center; padding:48px 24px 36px; border:1px solid var(--line); position:relative; overflow:hidden;' }, [
      el('div', { class: 'about-logo-wrap', style: 'font-size:64px; margin-bottom:14px;' }, [
        (function(){ var img = document.createElement('img'); img.src = '/static/img/kairo-logo-192.png'; img.style.width='72px'; img.style.height='72px'; img.style.borderRadius='18px'; return img; })()
      ]),
      el('h1', { class: 'kairo-shimmer-text', style: 'margin:0 0 6px 0; font-size:36px; font-weight:800; background-image:repeating-linear-gradient(135deg, var(--primary) 0%, var(--accent) 50%, var(--primary) 100%); letter-spacing:0.5px; background-clip:text; -webkit-background-clip:text; color:transparent; -webkit-text-fill-color:transparent;', text: 'Kairo · 天命契机' }),
      el('div', { class: 'text-dim', style: 'font-size:14px; margin-bottom:8px; letter-spacing:1px;', text: 'Kairo — 来自希腊语 kairos，意为「恰当时机」' }),
      el('div', { class: 'text-dim', style: 'font-size:14px; margin-bottom:18px; letter-spacing:0.5px;', unsafeHtml: 'Crafted by <span class="about-credit-name kairo-shimmer-text" style="background-image:linear-gradient(135deg, var(--text) 0%, var(--primary) 40%, var(--accent) 60%, var(--primary) 80%, var(--text) 100%); font-weight:600; background-clip:text; -webkit-background-clip:text; color:transparent; -webkit-text-fill-color:transparent;">Qi</span>' }),
      el('div', { class: 'text-dim', style: 'font-size:16px; margin-bottom:18px; max-width:760px; margin-left:auto; margin-right:auto; line-height:1.7;', text: '企业级内网运维效率平台 · 为 SRE / DevOps / 运维工程师量身打造。安全为先、极简为骨、上下文为魂——一套二进制搞定 SSH 日志检索、文件下载、代码比对、HTTP 调试、环境诊断与配置管理。' }),
      el('div', { style: 'display:inline-flex; gap:8px; flex-wrap:wrap; justify-content:center; align-items:center;' }, [
        versionBadge,
        el('span', { class: 'tier-badge', style: 'background:rgba(34,197,94,0.15); color:#22c55e; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: '单二进制' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(139,92,246,0.15); color:#a78bfa; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: '零 npm' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(245,158,11,0.15); color:#fbbf24; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: 'fail-closed' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(99,102,241,0.15); color:#a5b4fc; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: 'RBAC 就绪' }),
        el('span', { class: 'tier-badge', style: 'background:rgba(239,68,68,0.12); color:#f87171; font-size:11px; font-weight:600; padding:4px 10px; border-radius:10px;', text: 'OS 钥匙串' })
      ]),
      el('div', { class: 'text-dim', style: 'font-size:13px; margin-top:18px; line-height:1.7;', text: '打造下一代运维工具链 · 让每一次操作都精准高效、每一次下载都可追溯、每一次配置都有审计。' })
    ]);
    view.appendChild(hero);
  }

  // --- 核心数据看板 ---
  function renderStats(view) {
    const grid = el('div', { class: 'mt-3', style: 'display:flex; flex-wrap:wrap; gap:12px;' });
    stats.forEach(s => {
      const colorMap = {
        primary: 'var(--primary)',
        accent: 'var(--accent)',
        success: 'var(--success)',
        warn: 'var(--warn)',
        error: 'var(--error)'
      };
      const card = el('div', { class: 'card', style: 'flex:1 1 160px; min-width:0; padding:16px 12px; text-align:center; transition:transform 0.15s;' }, [
        el('div', { style: 'font-size:22px; font-weight:700; color:' + (colorMap[s.tone] || 'var(--primary)') + '; margin-bottom:4px; font-variant-numeric: tabular-nums;', text: s.value }),
        el('div', { style: 'font-size:13px; font-weight:600; margin-bottom:2px;', text: s.label }),
        el('div', { class: 'text-dim', style: 'font-size:11.5px; line-height:1.4;', text: s.sub })
      ]);
      grid.appendChild(card);
    });
    view.appendChild(grid);
  }

  // --- sticky 锚点导航 (顶部 tab bar) ---
  function renderAnchorNav(view) {
    const sections = [
      { id: 'sec-overview',     icon: 'overview',     label: '产品概览' },
      { id: 'sec-ai-engine',    icon: 'aiEngine',     label: 'AI 研发引擎' },
      { id: 'sec-principles',   icon: 'principles',   label: '设计哲学' },
      { id: 'sec-architecture', icon: 'architecture', label: '四层架构' },
      { id: 'sec-stack',        icon: 'stack',        label: '技术栈' },
      { id: 'sec-security',     icon: 'security',     label: '安全白皮书' },
      { id: 'sec-compat',       icon: 'compat',       label: 'SSH 兼容' },
      { id: 'sec-quality',      icon: 'quality',      label: '质量保障' },
      { id: 'sec-modules',      icon: 'modules',      label: '功能模块' },
      { id: 'sec-comparison',   icon: 'comparison',   label: '横向对比' },
      { id: 'sec-bugs',         icon: 'bugs',         label: '故障复盘' },
      { id: 'sec-history',      icon: 'history',      label: '版本史' },
      { id: 'sec-faq',          icon: 'faq',          label: 'FAQ' },
      { id: 'sec-roadmap',      icon: 'roadmap',      label: '路线图' }
    ];
    const wrap = el('div', { style: 'position:sticky; top:8px; z-index:10; background:var(--topbar-bg); border:1px solid var(--line); border-radius:var(--radius); padding:8px 10px; margin:20px 0 8px 0; display:flex; gap:6px; flex-wrap:wrap; box-shadow: 0 2px 8px rgba(0,0,0,0.06);' });
    sections.forEach(s => {
      const a = el('a', {
        href: '#' + s.id,
        style: 'font-size:12.5px; padding:5px 12px; border-radius:6px; text-decoration:none; color:var(--text-dim); border:1px solid transparent; transition:all 0.15s; cursor:pointer; display:inline-flex; align-items:center; gap:5px;',
        onmouseover: function () { this.style.background = 'var(--bg-2)'; this.style.color = 'var(--text)'; this.style.borderColor = 'var(--line)'; },
        onmouseout: function () { this.style.background = ''; this.style.color = 'var(--text-dim)'; this.style.borderColor = 'transparent'; },
        onclick: function (e) { e.preventDefault(); const tgt = document.getElementById(s.id); if (tgt) tgt.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
      }, [
        el('span', { style: 'width:14px; height:14px; display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon(s.icon, 14) }),
        document.createTextNode(s.label)
      ]);
      wrap.appendChild(a);
    });
    view.appendChild(wrap);
  }

  // --- 概览 / 价值主张 ---
  function renderOverview(view) {
    const wrap = el('div');
    const para1 = el('div', { class: 'card', style: 'padding:20px 22px; line-height:1.85; font-size:14px;' }, [
      el('p', { style: 'margin:0 0 12px 0;', text: 'Kairo 是一款专为内网运维场景打造的桌面级工具箱。它把日常运维中最常见的几类操作——SSH 日志检索、远程文件下载、代码比对、HTTP 接口调试、报文格式化、环境诊断、系统配置——打包成一份独立的可执行文件，开箱即用，无需安装任何运行时。' }),
      el('p', { style: 'margin:0 0 12px 0;', text: '整套系统默认只监听 127.0.0.1，所有功能通过 Web UI 暴露；后端用 Go 编写，前端用原生 JavaScript 编写（零 npm 依赖，零构建工具链），所有静态资源通过 go:embed 内嵌进单一二进制。跨平台分发只需要一份文件：macOS / Linux / Windows / Win7 均可。' }),
      el('p', { style: 'margin:0;', text: '产品定位上，Kairo 不是要替代 Ansible / Jenkins / Prometheus 这类重型平台，而是作为运维工程师日常 80% 操作的"快进键"——登录、查日志、下载文件、对比配置、调一下接口、转一下编码——这些"小但高频"的动作，过去要在 SecureCRT + WinSCP + Postman + 各种在线工具之间反复横跳，现在一个浏览器标签就能搞定。' })
    ]);
    const useCases = el('div', { class: 'card mt-3', style: 'padding:18px 22px;' }, [
      el('div', { style: 'font-weight:600; font-size:14px; margin-bottom:10px; color:var(--primary); display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smTarget', 16) + ' 典型使用场景' }),
      el('ul', { style: 'margin:0; padding-left:20px; line-height:1.85; font-size:13.5px;' }, [
        el('li', {}, [document.createTextNode('线上故障定位：在 10 台 WebSphere 上同时搜索 '), el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:12.5px;', text: 'NullPointerException' }), document.createTextNode('，30 秒内拿到全量上下文')]),
        el('li', {}, [document.createTextNode('远程取配置文件：从老 AIX 机器下载 '), el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:12.5px;', text: '/etc/profile' }), document.createTextNode('，一键用 VSCode 打开对比')]),
        el('li', {}, [document.createTextNode('接口联调：内网 HTTP 服务无 Swagger，用 HTTP 测试台 30 秒构造请求 + 看响应')]),
        el('li', {}, [document.createTextNode('环境巡检：双击 Kairo，进入诊断中心，确认所有服务器 SSH 可达 + 工具齐全')]),
        el('li', {}, [document.createTextNode('跨机器批量下载：勾选 50 个日志文件，后台下载 + SSE 实时进度 + 自动按日期归档')]),
        el('li', {}, [document.createTextNode('应急对比：把生产配置和预发配置拉下来，diff 一眼看出差异行')])
      ])
    ]);
    wrap.appendChild(para1);
    wrap.appendChild(useCases);
    view.appendChild(renderSection('sec-overview', 'overview', '产品概览', 'product overview', wrap));
  }

  // --- AI-Native 研发工程 ---
  function renderAiEngineSection(view) {
    const wrap = el('div');

    // 1. 顶部全景 Banner
    const banner = el('div', { class: 'card', style: 'padding:22px 24px; margin-bottom:14px; background:linear-gradient(135deg, rgba(var(--primary-rgb, 59,130,246), 0.08), rgba(var(--accent-rgb, 147,51,234), 0.08)); border:1px solid var(--line);' }, [
      el('div', { style: 'display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:12px; margin-bottom:10px;' }, [
        el('div', { style: 'display:flex; align-items:center; gap:8px;' }, [
          el('span', { style: 'width:24px; height:24px; display:inline-flex; align-items:center; justify-content:center; color:var(--primary);', unsafeHtml: svgIcon('aiEngine', 24) }),
          el('span', { style: 'font-weight:700; font-size:16px; color:var(--text);', text: 'AI-Native 工业级研发工程与算力矩阵' })
        ]),
        el('span', { class: 'badge', style: 'font-size:12px; padding:4px 10px; background:var(--primary); color:#fff; font-weight:700;', text: '20 亿+ (2.0B+) AI Tokens 淬炼' })
      ]),
      el('div', { style: 'font-size:13.5px; line-height:1.8; color:var(--text-dim);', text: 'Kairo 并非简单的代码生成产物，而是依托 5 大 Agent 编程环境，由全球前沿顶尖大模型在架构推演、只读 AST 词法安全证明、多模态视觉审核与双轨测试闭环中深度协同打造的工业级生产力工具箱。每一行存量生产代码，背后都凝聚了上万 Token 的逻辑验证与死锁防范。' })
    ]);

    // 2. 五大开发工具链卡片 (Codex 排第一)
    const toolTitle = el('div', { style: 'font-weight:600; font-size:14px; margin:16px 0 8px 0; color:var(--primary); display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smTarget', 16) + ' 研发编程工具链 (AI Agent Toolchain)' });
    const toolGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(180px, 1fr)); gap:10px; margin-bottom:16px;' });
    aiEngineData.tools.forEach(t => {
      toolGrid.appendChild(el('div', { class: 'card', style: 'padding:14px 16px; text-align:center; transition:transform 0.15s;' }, [
        el('div', { style: 'width:28px; height:28px; margin:0 auto 8px auto; display:flex; align-items:center; justify-content:center; color:var(--primary);', unsafeHtml: getBrandSvg(t.brand, 24) }),
        el('div', { style: 'font-weight:700; font-size:15px; color:var(--text);', text: t.name })
      ]));
    });

    // 3. 由以下前沿大模型共同深度协同开发 (GPT-6 Astra / ChatGPT 5.6 Sol 排前)
    const modelBadges = el('div', { style: 'display:flex; flex-wrap:wrap; gap:8px;' });
    aiEngineData.models.forEach(m => {
      const isHighlight = m.name.indexOf('Astra') !== -1 || m.name.indexOf('Sol') !== -1 || m.name.indexOf('Opus') !== -1 || m.name.indexOf('V4 Pro') !== -1 || m.name.indexOf('Gemini 3.8') !== -1 || m.name.indexOf('Muse Spark 1.3') !== -1 || m.name.indexOf('MiMo V2.5') !== -1 || m.name.indexOf('Ling 3.0') !== -1;
      modelBadges.appendChild(el('span', {
        style: 'display:inline-flex; align-items:center; gap:6px; padding:5px 12px; border-radius:6px; font-size:12.5px; font-weight:600; background:' + (isHighlight ? 'var(--primary)' : 'var(--bg-2)') + '; color:' + (isHighlight ? '#fff' : 'var(--text)') + '; border:1px solid ' + (isHighlight ? 'var(--primary)' : 'var(--line)') + '; transition:all 0.15s;'
      }, [
        el('span', { style: 'width:14px; height:14px; display:inline-flex; align-items:center; justify-content:center; flex-shrink:0;', unsafeHtml: getBrandSvg(m.brand, 14) }),
        document.createTextNode(m.name)
      ]));
    });

    const modelCard = el('div', { class: 'card', style: 'padding:18px 20px; margin-bottom:14px; border:1px solid var(--line);' }, [
      el('div', { style: 'font-weight:600; font-size:14px; margin-bottom:12px; color:var(--primary); display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smRepeat', 16) + ' 由以下前沿大模型共同深度协同开发 (Frontier AI Models)' }),
      modelBadges
    ]);

    // 4. 三大工程支柱 (Pillars)
    const pillarGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    aiEngineData.pillars.forEach(p => {
      pillarGrid.appendChild(el('div', { class: 'card', style: 'padding:16px 18px; line-height:1.7;' }, [
        el('div', { style: 'display:flex; align-items:center; gap:8px; margin-bottom:8px;' }, [
          el('span', { style: 'width:20px; height:20px; display:inline-flex; align-items:center; justify-content:center; color:var(--primary);', unsafeHtml: svgIcon(p.icon, 20) }),
          el('span', { style: 'font-weight:700; font-size:14px; color:var(--text);', text: p.title })
        ]),
        el('div', { class: 'text-dim', style: 'font-size:13px;', text: p.body })
      ]));
    });

    wrap.appendChild(banner);
    wrap.appendChild(toolTitle);
    wrap.appendChild(toolGrid);
    wrap.appendChild(modelCard);
    wrap.appendChild(pillarGrid);

    view.appendChild(renderSection('sec-ai-engine', 'aiEngine', 'AI 研发引擎', '20 亿+ AI Tokens · 5 大 Agent 编程环境 · 由前沿大模型共同深度协同开发', wrap));
  }

  // --- 设计哲学 ---
  function renderPrinciplesSection(view) {
    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:12px;' });
    principles.forEach(p => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:18px 18px; line-height:1.7;' }, [
        el('div', { style: 'width:24px; height:24px; margin-bottom:8px; display:inline-flex; align-items:center; justify-content:center; color:var(--primary);', unsafeHtml: svgIcon(p.icon, 24) }),
        el('div', { style: 'font-weight:700; font-size:14.5px; margin-bottom:6px;', text: p.title }),
        el('div', { class: 'text-dim', style: 'font-size:13px;', text: p.body })
      ]));
    });
    view.appendChild(renderSection('sec-principles', 'principles', '设计哲学', '8 条贯穿全栈的核心原则 · fail-closed by default', grid));
  }

  // --- 架构总览 ---
  function renderArchitectureSection(view) {
    const wrap = el('div');

    // 4 层架构
    const layerGrid = el('div', { style: 'display:grid; grid-template-columns:1fr; gap:10px;' });
    architecture.forEach(a => {
      const techLine = a.tech.map(t => el('span', { class: 'tier-badge', style: 'background:var(--bg-2); color:var(--text); font-size:11px; padding:3px 8px; border-radius:6px;', text: t }));
      const techWrap = el('div', { style: 'display:flex; flex-wrap:wrap; gap:6px; margin-top:10px;' });
      techLine.forEach(t => techWrap.appendChild(t));
      layerGrid.appendChild(el('div', { class: 'card', style: 'padding:16px 20px; border-left:4px solid var(--primary);' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:6px;' }, [
          el('span', { class: 'tier-badge', style: 'background:var(--primary); color:#fff; font-size:11px; padding:3px 8px; border-radius:6px; font-weight:700;', text: a.layer }),
          el('span', { style: 'font-weight:700; font-size:15px;', text: a.name }),
          el('span', { class: 'text-dim', style: 'font-size:12px;', text: a.detail })
        ]),
        el('div', { style: 'font-size:13.5px; line-height:1.75; color:var(--text-dim);', text: a.duty }),
        techWrap
      ]));
    });

    // 数据流
    const flowCard = el('div', { class: 'card mt-3', style: 'padding:18px 22px;' }, [
      el('div', { style: 'font-weight:700; font-size:14.5px; margin-bottom:10px; color:var(--accent); display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smRepeat', 16) + ' 数据流向' }),
      el('div', { style: 'font-family:ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size:12.5px; line-height:2.0; background:var(--bg-2); padding:14px 16px; border-radius:8px; overflow-x:auto;' },
        dataFlow.reduce(function (acc, d) {
          return acc.concat(el('div', {}, [
            el('span', { style: 'color:var(--primary); font-weight:600;', text: d.from }),
            document.createTextNode('  →  '),
            el('span', { style: 'color:var(--accent); font-weight:600;', text: d.to }),
            el('span', { class: 'text-dim', style: 'margin-left:10px;', text: '// ' + d.label })
          ]));
        }, [])
      )
    ]);

    wrap.appendChild(layerGrid);
    wrap.appendChild(flowCard);
    view.appendChild(renderSection('sec-architecture', 'architecture', '架构总览', '4 层分层 + 数据流 · 从 Browser 到 Remote 全链路', wrap));
  }

  // --- 技术栈 ---
  function renderStackSection(view) {
    const wrap = el('div');

    // 后端技术栈
    const backendTitle = el('h3', { style: 'margin:0 0 12px 0; font-size:16px; display:flex; align-items:center; gap:8px;' }, [
      el('span', { unsafeHtml: svgIcon('smGear', 18) }),
      document.createTextNode(' 后端 (Go 1.24+)')
    ]);
    const backendTable = el('div', { class: 'card', style: 'padding:0; overflow-x:auto;' });
    const tbl = el('table', { style: 'width:100%; border-collapse:collapse; font-size:13px;' });
    const thead = el('thead', {}, [
      el('tr', { style: 'background:var(--bg-2);' }, [
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '依赖' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '版本' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '角色' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '说明' })
      ])
    ]);
    const tbody = el('tbody');
    backendStack.forEach(s => {
      tbody.appendChild(el('tr', { style: 'border-bottom:1px solid var(--line);' }, [
        el('td', { style: 'padding:9px 14px; font-weight:600; color:var(--primary);', text: s.name }),
        el('td', { style: 'padding:9px 14px; color:var(--text-dim); font-family:ui-monospace, monospace; font-size:12.5px;', text: s.version }),
        el('td', { style: 'padding:9px 14px;', text: s.role }),
        el('td', { style: 'padding:9px 14px; color:var(--text-dim); line-height:1.5;', text: s.desc })
      ]));
    });
    tbl.appendChild(thead);
    tbl.appendChild(tbody);
    backendTable.appendChild(tbl);

    // 前端架构
    const frontendTitle = el('h3', { style: 'margin:24px 0 12px 0; font-size:16px; display:flex; align-items:center; gap:8px;' }, [
      el('span', { unsafeHtml: svgIcon('smPalette', 18) }),
      document.createTextNode(' 前端 (Vanilla JS · 零依赖)')
    ]);
    const frontendGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    frontendStack.forEach(s => {
      frontendGrid.appendChild(el('div', { class: 'card', style: 'padding:14px 16px;' }, [
        el('div', { style: 'font-weight:600; font-size:13.5px; margin-bottom:5px; color:var(--accent); font-family:ui-monospace, monospace;', text: s.name }),
        el('div', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.65;', text: s.desc })
      ]));
    });

    // 工程实践
    const engTitle = el('h3', { style: 'margin:24px 0 12px 0; font-size:16px; display:flex; align-items:center; gap:8px;' }, [
      el('span', { unsafeHtml: svgIcon('smTools', 18) }),
      document.createTextNode(' 工程化实践 (14 项)')
    ]);
    const engGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:8px;' });
    engineering.forEach((e, i) => {
      engGrid.appendChild(el('div', { class: 'card', style: 'padding:12px 14px;' }, [
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start;' }, [
          el('span', { style: 'flex-shrink:0; width:24px; height:24px; border-radius:50%; background:var(--primary); color:#fff; font-size:11px; font-weight:700; display:flex; align-items:center; justify-content:center;', text: String(i + 1).padStart(2, '0') }),
          el('div', {}, [
            el('div', { style: 'font-weight:600; font-size:13px; margin-bottom:3px;', text: e.title }),
            el('div', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.6;', text: e.body })
          ])
        ])
      ]));
    });

    wrap.appendChild(backendTitle);
    wrap.appendChild(backendTable);
    wrap.appendChild(frontendTitle);
    wrap.appendChild(frontendGrid);
    wrap.appendChild(engTitle);
    wrap.appendChild(engGrid);
    view.appendChild(renderSection('sec-stack', 'stack', '技术栈', '后端 16 依赖 · 前端 11 模块 · 工程 14 实践 · 125,000+ 行代码', wrap));
  }

  // --- 安全白皮书 ---
  function renderSecuritySection(view) {
    const wrap = el('div');
    const banner = el('div', { class: 'card', style: 'background:linear-gradient(135deg, rgba(239,68,68,0.08), rgba(245,158,11,0.04)); border:1px solid rgba(239,68,68,0.25); padding:16px 20px; margin-bottom:12px;' }, [
      el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:6px;' }, [
        el('span', { style: 'width:26px; height:26px; display:inline-flex; align-items:center; justify-content:center; color:var(--error);', unsafeHtml: svgIcon('smShield', 26) }),
        el('span', { style: 'font-weight:700; font-size:15px; color:var(--error);', text: 'fail-closed 安全模型 (v0.9 起)' })
      ]),
      el('div', { style: 'font-size:13px; line-height:1.7; color:var(--text-dim);', text: '所有权限决策默认"拒绝"。白名单空 → 一律拒绝；host key 没配 + allow_insecure=false → 不发起连接；admin 专属接口没带 admin token → 403；数据库只读 AST 拦截写操作；跨文件升级原子事务防半更新；Windows Job Object 递归回收进程树。把"忘记配"和"配错"都收敛到安全侧，避免任何隐式放行。17 项安全设计点协同，没有单点失守即可破防的逻辑链。' })
    ]);

    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:8px;' });
    security.forEach(s => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:14px 18px; border-left:3px solid var(--error);' }, [
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start;' }, [
          el('span', { style: 'flex-shrink:0; font-family:ui-monospace, monospace; font-size:11px; font-weight:700; color:var(--error); padding:2px 7px; border-radius:5px; background:rgba(239,68,68,0.12);', text: s.id }),
          el('div', {}, [
            el('div', { style: 'font-weight:600; font-size:13.5px; margin-bottom:4px;', text: s.title }),
            el('div', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.65;', text: s.detail })
          ])
        ])
      ]));
    });

    wrap.appendChild(banner);
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-security', 'security', '安全白皮书', '17 项 fail-closed 设计点 · 纵深防御 · 零明文落盘', wrap));
  }

  // --- SSH 兼容矩阵 ---
  function renderCompatSection(view) {
    const wrap = el('div');
    const tbl = el('div', { class: 'card', style: 'padding:0; overflow-x:auto;' });
    const table = el('table', { style: 'width:100%; border-collapse:collapse; font-size:13px;' });
    table.appendChild(el('thead', {}, [
      el('tr', { style: 'background:var(--bg-2);' }, [
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: 'Profile' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '默认 KEX' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '适用场景' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600;', text: '备注' })
      ])
    ]));
    const tbody = el('tbody');
    sshProfiles.forEach(p => {
      tbody.appendChild(el('tr', { style: 'border-bottom:1px solid var(--line);' }, [
        el('td', { style: 'padding:9px 14px; font-weight:600; color:var(--primary); font-family:ui-monospace, monospace;', text: p.name }),
        el('td', { style: 'padding:9px 14px; font-family:ui-monospace, monospace; font-size:12px;', text: p.kex }),
        el('td', { style: 'padding:9px 14px;', text: p.target }),
        el('td', { class: 'text-dim', style: 'padding:9px 14px; font-size:12.5px;', text: p.note })
      ]));
    });
    table.appendChild(tbody);
    tbl.appendChild(table);

    const note = el('div', { class: 'card mt-3', style: 'padding:14px 18px; font-size:13px; line-height:1.7; color:var(--text-dim);' }, [
      el('strong', { style: 'color:var(--text);', text: '握手超时：' }),
      document.createTextNode('外层 sshDialOuterTimeout = 45s (3 × 12s + buffer)；单套 sshAttemptTimeout = 10s。auto 模式按 compat → no-ecdh → legacy 顺序自动 fallback，最大限度兼容老 sshd (OpenSSH 5.x / 6.0 / AIX / 老堡垒机)。未配 host_key_sha256 时默认拒绝连接 (fail-closed)。')
    ]);

    wrap.appendChild(tbl);
    wrap.appendChild(note);
    view.appendChild(renderSection('sec-compat', 'compat', 'SSH 兼容性矩阵', '5 套 profile × 5 类目标 · 从 OpenSSH 9.x 到 5.x 全部覆盖', wrap));
  }

  // --- 质量保障 ---
  function renderQualitySection(view) {
    const wrap = el('div');
    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    quality.forEach(q => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:16px 18px; border-left:3px solid var(--success);' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:8px; flex-wrap:wrap;' }, [
          el('span', { style: 'font-weight:700; font-size:14px;', text: q.tier }),
          el('span', { class: 'tier-badge', style: 'background:var(--bg-2); color:var(--text); font-size:11px; padding:3px 8px; border-radius:6px; font-family:ui-monospace, monospace;', text: q.tool }),
          el('span', { class: 'tier-badge', style: 'background:rgba(34,197,94,0.15); color:var(--success); font-size:11px; padding:3px 8px; border-radius:6px;', text: q.coverage })
        ]),
        el('div', { style: 'font-size:13px; line-height:1.7; color:var(--text-dim);', text: q.detail })
      ]));
    });
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-quality', 'quality', '质量保障', '6 层质量金字塔 · 1,150+ Go 测试 · 42 个 Playwright 脚本', wrap));
  }

  // --- 功能模块 ---
  function renderModulesSection(view) {
    const wrap = el('div');
    featureModules.forEach((m, idx) => {
      const featUl = el('ul', { style: 'margin:8px 0 0 0; padding-left:20px; line-height:1.75; font-size:13px;' });
      m.features.forEach(f => {
        featUl.appendChild(el('li', { style: 'margin-bottom:4px;', text: f }));
      });

      const apiLine = m.apis.map(a => el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:11.5px; margin-right:4px; display:inline-block; margin-bottom:3px;', text: a }));

      wrap.appendChild(el('div', { class: 'card', style: 'padding:18px 22px; margin-bottom:12px; border-left:4px solid var(--primary);' }, [
        el('div', { style: 'display:flex; align-items:flex-start; gap:14px; margin-bottom:10px;' }, [
          el('span', { style: 'width:32px; height:32px; flex-shrink:0; display:inline-flex; align-items:center; justify-content:center; color:var(--primary);', unsafeHtml: svgIcon(m.icon, 32) }),
          el('div', { style: 'flex:1; min-width:0;' }, [
            el('div', { style: 'display:flex; align-items:center; gap:10px; flex-wrap:wrap; margin-bottom:4px;' }, [
              el('span', { style: 'font-weight:700; font-size:17px;', text: m.name }),
              el('span', { class: 'tier-badge', style: 'background:var(--bg-2); color:var(--text-dim); font-size:11px; padding:2px 8px; border-radius:6px;', text: '#' + String(idx + 1).padStart(2, '0') })
            ]),
            el('div', { class: 'text-dim', style: 'font-size:13.5px; line-height:1.7; margin-bottom:8px;', text: m.desc }),
            el('div', { style: 'font-size:12px; margin-bottom:6px; display:flex; align-items:center; gap:4px;', }, [
              el('span', { style: 'width:14px; height:14px; color:var(--text-dim); display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon('smPackage', 14) }),
              el('span', { style: 'color:var(--text-dim); font-weight:600;', text: '后端包: ' }),
              el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:11.5px;', text: m.pkg })
            ]),
            el('div', { style: 'font-size:12px; margin-bottom:6px; display:flex; align-items:center; gap:4px;', }, [
              el('span', { style: 'width:14px; height:14px; color:var(--text-dim); display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon('smPin', 14) }),
              el('span', { style: 'color:var(--text-dim); font-weight:600;', text: '前端页: ' }),
              ...m.pages.map(p => el('code', { style: 'background:var(--bg-2); padding:1px 6px; border-radius:4px; font-size:11.5px; margin-right:4px;', text: 'web/pages/' + p + '.js' }))
            ]),
            el('div', { style: 'font-size:12px; display:flex; align-items:center; gap:4px;', }, [
              el('span', { style: 'width:14px; height:14px; color:var(--text-dim); display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon('smPlug', 14) }),
              el('span', { style: 'color:var(--text-dim); font-weight:600;', text: 'API: ' }),
              ...apiLine
            ])
          ])
        ]),
        el('div', { style: 'margin-top:10px; padding-top:10px; border-top:1px dashed var(--line);' }, [
          el('div', { style: 'font-weight:600; font-size:13px; color:var(--primary); margin-bottom:4px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smSparkles', 14) + ' 核心能力' }),
          featUl
        ])
      ]));
    });
    view.appendChild(renderSection('sec-modules', 'modules', '功能模块', '20 个深度能力卡 · 22 路由页面 + 原生桌面组件 · 130+ API', wrap));
  }

  // --- 版本演进史 (accordion) ---
  function renderHistorySection(view) {
    const wrap = el('div');

    const banner = el('div', { class: 'card about-hint-banner', style: 'padding:14px 20px; margin-bottom:14px; font-size:13px; color:var(--text-dim);' }, [
      el('span', { style: 'color:var(--primary); font-weight:600; display:inline-flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smBook', 14) + ' 阅读提示：' }),
      document.createTextNode('点击任意版本卡片展开完整内容。每个版本包含：核心定位 / 设计哲学 / 架构调整 / 核心特性 / 修复记录 (P0/P1/P2) / 关键 commit / 性能数据 / 升级注意 / Breaking Changes。')
    ]);

    const list = el('div');
    changelog.forEach((v, idx) => list.appendChild(renderVersionCard(v, idx)));
    wrap.appendChild(banner);
    wrap.appendChild(list);

    view.appendChild(renderSection('sec-history', 'history', '版本演进史', 'v0.1 → v0.19-dev · ' + changelog.length + ' 个版本 (含开发中记录 / v0.13.1 / v0.11-rc1) · 持续迭代 · 184+ commit', wrap));
  }

  function renderVersionCard(v, idx) {
    const isLatest = idx === 0;
    const cardId = 'version-' + v.version.replace(/\./g, '-');
    const borderColor = isLatest ? 'var(--primary)' : 'var(--line)';
    const versionColor = isLatest ? 'var(--primary)' : 'var(--text)';

    // Header (always visible)
    const versionBadge = el('span', {
      style: 'font-size:18px; font-weight:800; color:' + versionColor + '; font-family:ui-monospace, monospace;',
      text: v.version
    });
    const tagBadge = v.tag ? el('span', {
      class: 'tier-badge',
      style: 'background:' + (isLatest ? 'linear-gradient(135deg, #f59e0b, #ef4444)' : 'var(--bg-3)') + '; color:#fff; font-size:11px; font-weight:600; padding:3px 10px; border-radius:10px;',
      text: v.tag
    }) : null;
    const codeBadge = v.codename ? el('span', {
      class: 'tier-badge',
      style: 'background:var(--bg-2); color:var(--accent); font-size:11px; padding:3px 8px; border-radius:6px; font-family:ui-monospace, monospace;',
      text: 'codename: ' + v.codename
    }) : null;
    const currentBadge = isLatest ? el('span', {
      class: 'tier-badge',
      style: 'background:var(--primary); color:#fff; font-size:11px; font-weight:600; padding:3px 10px; border-radius:10px;',
      text: '当前版本'
    }) : null;
    const dateLabel = el('span', { class: 'text-dim', style: 'font-size:13px;', text: v.date });
    const expandIcon = el('span', {
      style: 'margin-left:auto; color:var(--text-dim); transition:transform 0.2s; display:inline-flex; align-items:center;',
      unsafeHtml: svgIcon('smChevronDown', 16)
    });

    const header = el('div', {
      class: 'changelog-version-header',
      style: 'display:flex; justify-content:space-between; align-items:center; cursor:pointer; padding:16px 20px; gap:10px; flex-wrap:wrap;',
      onclick: function () { toggleVersion(cardId, expandIcon); }
    }, [
      el('div', { style: 'display:flex; align-items:center; gap:10px; flex-wrap:wrap; flex:1; min-width:0;' }, [
        versionBadge, tagBadge, codeBadge, currentBadge
      ]),
      el('div', { style: 'display:flex; align-items:center; gap:12px; flex-shrink:0;' }, [
        dateLabel, expandIcon
      ])
    ]);

    // 一句话定位 + stats (always visible)
    const headlineRow = el('div', { style: 'padding:0 20px 14px 20px; font-size:13.5px; line-height:1.7; color:var(--text-dim); border-bottom:1px dashed var(--line);' }, [
      el('div', { style: 'font-style:italic; margin-bottom:8px; color:var(--text);', text: '“' + v.headline + '”' })
    ]);
    if (v.stats) {
      const statRow = el('div', { style: 'display:flex; gap:16px; flex-wrap:wrap; font-size:12px; color:var(--text-dim);' });
      Object.keys(v.stats).forEach(k => {
        statRow.appendChild(el('span', {}, [
          el('strong', { style: 'color:var(--text); font-family:ui-monospace, monospace;', text: String(v.stats[k]) }),
          document.createTextNode(' ' + k)
        ]));
      });
      headlineRow.appendChild(statRow);
    }

    // Body (折叠)
    const body = el('div', {
      id: cardId,
      style: 'max-height:0; overflow:hidden; transition:max-height 0.35s ease-out;'
    });

    const bodyInner = el('div', { style: 'padding:18px 20px;' });

    // 1. 设计哲学
    if (v.principles && v.principles.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--accent); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smCompass', 14) + ' 本版设计哲学' }));
      const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.85; font-size:13px;' });
      v.principles.forEach(p => ul.appendChild(el('li', { text: p })));
      sec.appendChild(ul);
      bodyInner.appendChild(sec);
    }

    // 2. 架构调整
    if (v.architecture) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--accent); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smBuilding', 14) + ' 架构调整' }));
      if (v.architecture.layers) {
        const layers = el('div', { style: 'display:flex; flex-direction:column; gap:6px;' });
        v.architecture.layers.forEach(l => {
          layers.appendChild(el('div', { style: 'padding:8px 12px; background:var(--bg-2); border-radius:6px; border-left:3px solid var(--accent);' }, [
            el('div', { style: 'font-weight:600; font-size:13px; margin-bottom:3px;', text: l.name }),
            el('div', { style: 'font-size:12.5px; line-height:1.65; color:var(--text-dim);', text: l.detail })
          ]));
        });
        sec.appendChild(layers);
      }
      if (v.architecture.retirements && v.architecture.retirements.length) {
        sec.appendChild(el('div', { style: 'font-weight:600; font-size:12.5px; margin-top:10px; margin-bottom:5px; color:var(--warn); display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smRecycle', 12) + ' 退役' }));
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.7; font-size:12.5px; color:var(--text-dim);' });
        v.architecture.retirements.forEach(r => ul.appendChild(el('li', { text: r })));
        sec.appendChild(ul);
      }
      bodyInner.appendChild(sec);
    }

    // 3. 核心特性
    if (v.features && v.features.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--primary); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smSparkles', 14) + ' 核心特性 (' + v.features.length + ')' }));
      const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.8; font-size:13px;' });
      v.features.forEach(f => {
        const li = el('li', { style: 'margin-bottom:6px;' });
        if (typeof f === 'string') {
          // 兜底: 防御未来写漏对象格式 — 字符串当整句加粗显示
          li.appendChild(el('span', { style: 'font-weight:600;', text: f }));
        } else {
          li.appendChild(el('span', { style: 'font-weight:600;', text: f.title }));
          li.appendChild(document.createTextNode(' — '));
          li.appendChild(el('span', { class: 'text-dim', style: 'font-size:12.5px; line-height:1.7;', text: f.desc }));
        }
        ul.appendChild(li);
      });
      sec.appendChild(ul);
      bodyInner.appendChild(sec);
    }

    // 4. 修复
    if (v.fixes) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--warn); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smWrench', 14) + ' 修复与优化' }));
      const renderGroup = (label, items, color) => {
        if (!items || !items.length) return;
        sec.appendChild(el('div', { style: 'font-weight:600; font-size:12.5px; margin-top:8px; margin-bottom:5px; color:' + color + ';', text: label + ' (' + items.length + ')' }));
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.7; font-size:12.5px; color:var(--text-dim);' });
        items.forEach(t => ul.appendChild(el('li', { text: t })));
        sec.appendChild(ul);
      };
      if (Array.isArray(v.fixes)) {
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.7; font-size:12.5px; color:var(--text-dim);' });
        v.fixes.forEach(t => ul.appendChild(el('li', { text: t })));
        sec.appendChild(ul);
      } else {
        renderGroup('P0 紧急修复', v.fixes.p0, 'var(--error)');
        renderGroup('P1 重要修复', v.fixes.p1, 'var(--warn)');
        renderGroup('P2 一般修复', v.fixes.p2, 'var(--text-dim)');
      }
      bodyInner.appendChild(sec);
    }

    // 5. 关键 commit
    if (v.commits && v.commits.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--success); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smPin', 14) + ' 关键 commit (' + v.commits.length + ')' }));
      const ul = el('ul', { style: 'margin:0; padding-left:0; list-style:none; line-height:1.85; font-size:12.5px;' });
      v.commits.forEach(c => {
        ul.appendChild(el('li', { style: 'padding:4px 0; border-bottom:1px dashed var(--line);' }, [
          el('code', { style: 'background:var(--bg-2); padding:2px 8px; border-radius:5px; font-size:11.5px; color:var(--primary); margin-right:10px;', text: c.hash }),
          el('span', { class: 'text-dim', text: c.msg })
        ]));
      });
      sec.appendChild(ul);
      bodyInner.appendChild(sec);
    }

    // 6. 性能数据
    if (v.performance && v.performance.length) {
      const sec = el('div', { style: 'margin-bottom:18px;' });
      sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--warn); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smZap', 14) + ' 性能数据' }));
      const perfGrid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(220px, 1fr)); gap:8px;' });
      v.performance.forEach(p => {
        perfGrid.appendChild(el('div', { style: 'padding:10px 14px; background:var(--bg-2); border-radius:8px; border-left:3px solid var(--success);' }, [
          el('div', { style: 'font-weight:600; font-size:12.5px; margin-bottom:4px;', text: p.label }),
          el('div', { style: 'font-size:11.5px; color:var(--text-dim); line-height:1.6;' }, [
            el('div', {}, [el('span', { style: 'color:var(--error); font-family:ui-monospace, monospace;', text: '前 ' + p.before })]),
            el('div', {}, [el('span', { style: 'color:var(--success); font-family:ui-monospace, monospace;', text: '后 ' + p.after })]),
            el('div', { style: 'margin-top:2px;' }, [el('span', { style: 'color:var(--primary); font-weight:700; font-family:ui-monospace, monospace;', text: '↑ ' + p.improve })])
          ])
        ]));
      });
      sec.appendChild(perfGrid);
      bodyInner.appendChild(sec);
    }

    // 7. 升级注意 + Breaking Changes
    if ((v.migration && v.migration.length) || (v.breaking && v.breaking.length)) {
      const sec = el('div', { style: 'margin-bottom:8px;' });
      if (v.breaking && v.breaking.length) {
        sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--error); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smWarn', 14) + ' Breaking Changes (' + v.breaking.length + ')' }));
        const ul = el('ul', { style: 'margin:0 0 12px 0; padding-left:20px; line-height:1.75; font-size:12.5px;' });
        v.breaking.forEach(b => ul.appendChild(el('li', { style: 'color:var(--error);', text: b })));
        sec.appendChild(ul);
      }
      if (v.migration && v.migration.length) {
        sec.appendChild(el('div', { style: 'font-weight:700; font-size:13.5px; color:var(--text); margin-bottom:8px; display:flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smClipboard', 14) + ' 升级注意 (' + v.migration.length + ')' }));
        const ul = el('ul', { style: 'margin:0; padding-left:20px; line-height:1.75; font-size:12.5px; color:var(--text-dim);' });
        v.migration.forEach(m => ul.appendChild(el('li', { text: m })));
        sec.appendChild(ul);
      }
      bodyInner.appendChild(sec);
    }

    body.appendChild(bodyInner);

    const card = el('div', {
      class: 'card',
      style: 'margin-bottom:12px; border-left:4px solid ' + borderColor + '; padding:0; overflow:hidden;' + (isLatest ? ' box-shadow: var(--shadow-pop);' : '')
    });
    card.appendChild(header);
    card.appendChild(headlineRow);
    card.appendChild(body);
    return card;
  }

  // 模块级 timer 句柄, 防止快速切换时 setTimeout 多次执行竞争
  let _versionExpandTimer = null;
  function toggleVersion(cardId, icon) {
    const body = document.getElementById(cardId);
    if (!body) return;
    const isOpen = body.style.maxHeight && body.style.maxHeight !== '0px';
    if (isOpen) {
      body.style.maxHeight = '0px';
      if (icon) icon.style.transform = 'rotate(-90deg)';
      if (_versionExpandTimer) {
        clearTimeout(_versionExpandTimer);
        _versionExpandTimer = null;
      }
    } else {
      // 用 scrollHeight 撑开
      body.style.maxHeight = body.scrollHeight + 'px';
      if (icon) icon.style.transform = 'rotate(0deg)';
      // 展开后再清掉固定高度, 让内部能自适应 (再次展开也保持)
      if (_versionExpandTimer) clearTimeout(_versionExpandTimer);
      _versionExpandTimer = setTimeout(function () {
        _versionExpandTimer = null;
        if (!body.style.maxHeight || body.style.maxHeight === '0px') return;
        body.style.maxHeight = 'none';
      }, 360);
    }
  }

  // --- 横向对比 ---
  function renderComparisonSection(view) {
    const wrap = el('div');
    const intro = el('div', { class: 'card', style: 'padding:16px 20px; margin-bottom:12px; font-size:13.5px; line-height:1.85; color:var(--text-dim);' }, [
      el('strong', { style: 'color:var(--text);', text: '一句话：' }),
      document.createTextNode('Kairo 不是要替代你的 SecureCRT + WinSCP + Postman + DBeaver 工具栈，而是把 80% 的高频操作收敛到一个浏览器标签。下面 12 个真实场景的对比，让你自己判断值不值。')
    ]);

    const tbl = el('div', { class: 'card', style: 'padding:0; overflow-x:auto;' });
    const table = el('table', { style: 'width:100%; border-collapse:collapse; font-size:13px;' });
    table.appendChild(el('thead', {}, [
      el('tr', { style: 'background:var(--bg-2);' }, [
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:18%;', text: '场景' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:36%; color:var(--text-dim);', text: '传统工具栈' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:36%; color:var(--primary);', text: 'Kairo' }),
        el('th', { style: 'padding:10px 14px; text-align:left; border-bottom:1px solid var(--line); font-weight:600; width:10%; color:var(--success);', text: '收益' })
      ])
    ]));
    const tbody = el('tbody');
    comparison.forEach((c, i) => {
      const bg = i % 2 === 0 ? '' : 'background:var(--bg-2);';
      tbody.appendChild(el('tr', { style: 'border-bottom:1px solid var(--line); ' + bg }, [
        el('td', { style: 'padding:10px 14px; font-weight:600;', text: c.dim }),
        el('td', { style: 'padding:10px 14px; color:var(--text-dim); font-size:12.5px; line-height:1.55;', text: c.traditional }),
        el('td', { style: 'padding:10px 14px; font-size:12.5px; line-height:1.55;', text: c.kairo }),
        el('td', { style: 'padding:10px 14px; color:var(--success); font-weight:600; font-size:12.5px;', text: c.win })
      ]));
    });
    table.appendChild(tbody);
    tbl.appendChild(table);

    wrap.appendChild(intro);
    wrap.appendChild(tbl);
    view.appendChild(renderSection('sec-comparison', 'comparison', '横向对比', 'Kairo vs 传统工具栈 · 12 个真实场景效率对比', wrap));
  }

  // --- 故障案例库 ---
  function renderBugStoriesSection(view) {
    const wrap = el('div');
    const intro = el('div', { class: 'card', style: 'padding:14px 20px; margin-bottom:12px; font-size:13px; line-height:1.8; color:var(--text-dim);' }, [
      el('strong', { style: 'color:var(--text);', text: '15 个真实 bug 复盘：' }),
      document.createTextNode('下面这些不是教科书例子，而是 v0.4 - v0.18 期间 commit log 里真实发生过的故障。每个故事都包含：症状、根因、修复、教训。看到的不只是"修了什么"，更是"怎么思考的"。')
    ]);

    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:12px;' });
    bugStories.forEach(b => {
      const sevColor = b.severity === 'P0' ? 'var(--error)' : b.severity === 'P1' ? 'var(--warn)' : 'var(--text-dim)';
      const card = el('div', { class: 'card', style: 'padding:16px 20px; border-left:4px solid ' + sevColor + ';' }, [
        el('div', { style: 'display:flex; align-items:center; gap:10px; margin-bottom:10px; flex-wrap:wrap;' }, [
          el('span', { style: 'font-family:ui-monospace, monospace; font-size:11px; font-weight:700; padding:3px 8px; border-radius:5px; background:' + sevColor + '; color:#fff;', text: b.severity }),
          el('span', { style: 'font-family:ui-monospace, monospace; font-size:11px; color:var(--accent); padding:2px 8px; border-radius:5px; background:var(--bg-2);', text: b.id }),
          el('span', { class: 'text-dim', style: 'font-size:12px;', text: b.version })
        ]),
        el('div', { style: 'font-weight:700; font-size:14px; margin-bottom:10px; line-height:1.5;', text: b.title }),
        storyRow('smAlert', '症状', b.symptom),
        storyRow('smSearch', '根因', b.rootCause),
        storyRow('smWrench', '修复', b.fix),
        storyRow('smBulb', '教训', b.lesson, true)
      ]);
      grid.appendChild(card);
    });

    function storyRow(iconName, label, text, accent) {
      return el('div', { style: 'font-size:12.5px; line-height:1.7; margin-bottom:6px;' }, [
        el('div', { style: 'display:flex; align-items:flex-start; gap:8px;' }, [
          el('span', { style: 'flex-shrink:0; width:14px; height:14px; color:var(--text-dim); display:inline-flex; align-items:center; justify-content:center; margin-top:2px;', unsafeHtml: svgIcon(iconName, 14) }),
          el('div', {}, [
            el('span', { style: 'font-weight:600; color:' + (accent ? 'var(--warn)' : 'var(--text)') + '; margin-right:6px;', text: label + '：' }),
            el('span', { class: 'text-dim', text: text })
          ])
        ])
      ]);
    }

    wrap.appendChild(intro);
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-bugs', 'bugs', '故障案例库', '15 个真实 bug 复盘 · 含根因 / 修复 / 教训 · v0.4–v0.18 真实案例', wrap));
  }

  // --- FAQ ---
  function renderFaqSection(view) {
    const wrap = el('div');
    const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(280px, 1fr)); gap:10px;' });
    faq.forEach((f, i) => {
      grid.appendChild(el('div', { class: 'card', style: 'padding:14px 18px; border-left:3px solid var(--accent);' }, [
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start; margin-bottom:6px;' }, [
          el('span', { style: 'flex-shrink:0; width:24px; height:24px; border-radius:50%; background:var(--accent); color:#fff; font-size:11px; font-weight:700; display:flex; align-items:center; justify-content:center;', text: 'Q' }),
          el('div', { style: 'font-weight:600; font-size:13.5px; line-height:1.55; flex:1;', text: f.q })
        ]),
        el('div', { style: 'display:flex; gap:10px; align-items:flex-start;' }, [
          el('span', { style: 'flex-shrink:0; width:24px; height:24px; border-radius:50%; background:var(--bg-3); color:var(--text); font-size:11px; font-weight:700; display:flex; align-items:center; justify-content:center;', text: 'A' }),
          el('div', { class: 'text-dim', style: 'font-size:13px; line-height:1.7; flex:1;', text: f.a })
        ])
      ]));
    });
    wrap.appendChild(grid);
    view.appendChild(renderSection('sec-faq', 'faq', '常见问题', 'FAQ · 16 问 · 从部署、安全到数据库与桌面组件', wrap));
  }

  // --- 路线图 ---
  function renderRoadmapSection(view) {
    const wrap = el('div');

    const renderList = (title, items, color, iconName) => {
      const card = el('div', { class: 'card', style: 'padding:18px 22px; margin-bottom:12px; border-left:4px solid ' + color + ';' }, [
        el('div', { style: 'font-weight:700; font-size:15px; margin-bottom:12px; display:flex; align-items:center; gap:8px; color:' + color + ';' }, [
          el('span', { style: 'width:18px; height:18px; display:inline-flex; align-items:center; justify-content:center;', unsafeHtml: svgIcon(iconName, 18) }),
          document.createTextNode(title)
        ])
      ]);
      const grid = el('div', { style: 'display:grid; grid-template-columns:repeat(auto-fit, minmax(260px, 1fr)); gap:10px;' });
      items.forEach(p => {
        grid.appendChild(el('div', { style: 'padding:10px 14px; background:var(--bg-2); border-radius:8px;' }, [
          el('div', { style: 'font-weight:600; font-size:13px; margin-bottom:4px;', text: p.name }),
          el('div', { class: 'text-dim', style: 'font-size:12px; line-height:1.6;', text: p.desc })
        ]));
      });
      card.appendChild(grid);
      return card;
    };

    wrap.appendChild(renderList('已规划 (next 1-2 versions)', roadmap.planned, 'var(--primary)', 'smTarget'));
    wrap.appendChild(renderList('调研中 (considering)', roadmap.considering, 'var(--text-dim)', 'smBulb'));

    view.appendChild(renderSection('sec-roadmap', 'roadmap', '路线图', 'next 1-2 versions + considering · v0.18 工作台重构闭环与跨文件原子升级达成', wrap));
  }

  // --- Footer ---
  function renderFooter(view) {
    var f = el('div', { class: 'text-dim about-footer', style: 'position:relative; text-align:center; margin-top:48px; padding:32px 16px 40px; font-size:13px; border-top:1px solid var(--line); overflow:hidden;' });
    // 顶部光晕装饰线
    f.appendChild(el('div', { style: 'position:absolute; top:-1px; left:0; right:0; height:1px; background:linear-gradient(90deg, transparent, var(--primary), var(--accent), var(--primary), transparent); background-size:200% 100%; animation:kairo-shimmer 3s linear infinite;' }));
    // 品牌行：渐变流光文字
    f.appendChild(el('div', { class: 'kairo-shimmer-text', style: 'font-size:17px; font-weight:800; margin-bottom:10px; background-image:repeating-linear-gradient(135deg, var(--primary) 0%, var(--accent) 50%, var(--primary) 100%); letter-spacing:0.5px; background-clip:text; -webkit-background-clip:text; color:transparent; -webkit-text-fill-color:transparent;', text: '© 2026 Kairo · 天命契机' }));
    f.appendChild(el('div', { style: 'margin-top:6px; font-size:13px;', text: '技术栈：Go 1.24+ · 原生 JavaScript · x/crypto/ssh · pkg/sftp · single-binary deploy · zero runtime deps' }));
    f.appendChild(el('div', { style: 'margin-top:6px; font-size:12px;', text: '为运维效率而生 · 让每一次操作都有迹可循 · 让每一次配置都可审计 · 让每一次下载都可追溯' }));
    view.appendChild(f);
  }

  // =====================================================================
  // 主入口
  // =====================================================================
  // v0.13.x hotfix: 性能优化
  //   1. DocumentFragment 批量挂载: 14 次 reflow → 1 次
  //   2. 12 个非首屏 section 改 IO 懒渲染 (进视口才创建)
  //   3. 首屏 4 块 (hero/stats/anchorNav/overview) 立即渲染
  //   4. 老浏览器 (无 IO / 无 content-visibility) 自动降级
  // 兼容性: 全部浏览器行为至少等同旧版, 不会出错
  function renderAbout(view) {
    // 1. DocumentFragment 批量挂载
    const frag = document.createDocumentFragment();

    // 2. 立即渲染: 顶部 4 块 (用户一进来就看到的内容)
    renderHero(frag);
    renderStats(frag);
    renderAnchorNav(frag);
    renderOverview(frag);

    // 3. 懒渲染: 12 个 section 进入视口才创建 DOM
    // 每个子函数内部用 view.appendChild(renderSection(...)) 挂载, 我们传一个临时 div 收
    LAZY_SECTIONS.forEach(function (s) {
      const placeholder = withLazyMount(function () {
        const tmp = document.createElement('div');
        try {
          s.fn(tmp);
        } catch (e) {
          console.error('[about] section render failed: ' + s.name, e);
          return null;
        }
        // 子函数会把 anchor 节点 appendChild 到 tmp, 取第一个孩子就是
        return tmp.firstChild;
      }, { minHeight: s.min });
      frag.appendChild(placeholder);
    });

    // 4. 页脚 (用户可能滚到底, 跟 IO lazy 配合也工作)
    renderFooter(frag);

    // 5. 一次性挂载, 只触发 1 次 reflow
    view.appendChild(frag);
  }

  Kairo.pages.about = renderAbout;
  Kairo.state.routes.about = renderAbout;
  Kairo.state.routeNames.about = '关于';
})();
