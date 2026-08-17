/* ===== web/icons.js =====
 * 彩色实心 SVG 图标库（基于对比页 v2 的"B · 彩色实心"风格）
 *
 * 设计动机：
 *   - v1 用 lucide stroke-only（抽象、太"系统 UI 感"），用户反馈"不好看"
 *   - v2 对比页挑了 B：每个工具一种主色实心填充
 *     → 视觉重量最重、最"产品化"、辨识度最高
 *
 * 配色原则：
 *   - 13 个工具每个一种主色（蓝/绿/橙/青/紫/粉/玫红 等）
 *   - 用 fill="#xxx" 直接写死颜色（不走 currentColor，因为是彩色不是单色）
 *   - 主题按钮 5 个图标也保持彩色（视觉统一）
 *
 * 暴露：
 *   - Kairo.icons.svg(name, size)        → DOM SVG 元素
 *   - Kairo.icons.innerHTML(name, size)  → SVG 字符串
 *   - Kairo.icons.has(name)
 *
 * 加载顺序：core.js 之后、theme.js / home.js 之前。
 */

(function () {
  'use strict';
  if (!window.Kairo) window.Kairo = {};
  if (!window.Kairo.icons) window.Kairo.icons = {};
  const icons = window.Kairo.icons;

  const VB = '0 0 24 24';
  const NS = 'http://www.w3.org/2000/svg';

  // 13 个工具卡片图标（彩色实心版）
  const TOOLS = {
    // 日志助手：蓝色文件 + 白色文字线
    'file-text': `<path d="M14 3v4a1 1 0 0 0 1 1h4v9a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" fill="#3b82f6"/><path d="M14 3v4a1 1 0 0 0 1 1h4l-5-5z" fill="#1e40af"/><rect x="8" y="11" width="8" height="1.5" rx="0.5" fill="#ffffff"/><rect x="8" y="14" width="8" height="1.5" rx="0.5" fill="#ffffff"/><circle cx="9.5" cy="8" r="0.8" fill="#ffffff"/>`,

    // 文件下载：绿色文件 + 白色下箭头
    'file-down': `<path d="M14 3v4a1 1 0 0 0 1 1h4v9a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" fill="#10b981"/><path d="M14 3v4a1 1 0 0 0 1 1h4l-5-5z" fill="#047857"/><path d="M12 15.5v-5" stroke="#ffffff" stroke-width="2" stroke-linecap="round"/><polyline points="9.5 12.5 12 15 14.5 12.5" fill="none" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`,

    // 报文格式化：橙色粗描边大括号 + 中心红点（不用手算 fill 路径，避免钩子瑕疵）
    'braces': `<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1" fill="none" stroke="#f59e0b" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1" fill="none" stroke="#f59e0b" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/><circle cx="12" cy="12" r="1.5" fill="#dc2626"/>`,

    // HTTP：青色地球 + 经纬线 + 黄色光点
    'globe': `<circle cx="12" cy="12" r="10" fill="#06b6d4"/><path d="M12 2a14.5 14.5 0 0 1 0 20 14.5 14.5 0 0 1 0-20" fill="none" stroke="#ffffff" stroke-width="1.4" opacity="0.85"/><line x1="2" y1="12" x2="22" y2="12" stroke="#ffffff" stroke-width="1.4" opacity="0.85"/><circle cx="17" cy="8" r="1.5" fill="#fbbf24"/>`,

    // 代码比对：紫色两个圆 + 连线
    'git-compare': `<circle cx="6" cy="6" r="3" fill="#7c3aed"/><circle cx="18" cy="18" r="3" fill="#a855f7"/><path d="M9 6h4a3 3 0 0 1 3 3v6" stroke="#7c3aed" stroke-width="2.5" stroke-linecap="round" fill="none"/><path d="M15 18h-4a3 3 0 0 1-3-3V9" stroke="#a855f7" stroke-width="2.5" stroke-linecap="round" fill="none"/>`,

    // 常用命令：深灰终端窗口 + 绿/黄/红三色圆点 + 绿色 prompt
    'square-terminal': `<rect x="3" y="3" width="18" height="18" rx="2" fill="#1f2937"/><rect x="3" y="3" width="18" height="18" rx="2" fill="none" stroke="#374151" stroke-width="0.5"/><circle cx="6" cy="6.5" r="0.8" fill="#ef4444"/><circle cx="8.5" cy="6.5" r="0.8" fill="#eab308"/><circle cx="11" cy="6.5" r="0.8" fill="#10b981"/><polyline points="8 11 11 13 8 15" fill="none" stroke="#10b981" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="13" y1="15" x2="17" y2="15" stroke="#10b981" stroke-width="2" stroke-linecap="round"/>`,

    // 环境自检：绿色盾牌 + 白色对勾
    'shield-check': `<path d="M12 2.5 5.5 5v6.5c0 5 3.5 8.5 6.5 9.5 3-1 6.5-4.5 6.5-9.5V5z" fill="#10b981"/><path d="M9 11.5 11 13.5 14.5 10" fill="none" stroke="#ffffff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>`,

    // 下载历史：紫色文件夹 + 白色时钟角章
    //（之前 fill 回旋箭头会出现钩子瑕疵，改成 folder-clock 形态更干净）
    'history': `<path d="M2 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2z" fill="#8b5cf6"/><path d="M2 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v1H2z" fill="#7c3aed"/><circle cx="17" cy="16" r="4.2" fill="#ffffff"/><circle cx="17" cy="16" r="3.6" fill="none" stroke="#8b5cf6" stroke-width="0.5" opacity="0.4"/><line x1="17" y1="16" x2="17" y2="13.5" stroke="#8b5cf6" stroke-width="1.5" stroke-linecap="round"/><line x1="17" y1="16" x2="19" y2="17.5" stroke="#8b5cf6" stroke-width="1.5" stroke-linecap="round"/><circle cx="17" cy="16" r="0.9" fill="#8b5cf6"/>`,

    // 系统配置：橙色齿轮 + 白色中心
    'settings': `<path d="M14.7 4.1a2.5 2.5 0 0 1-5 0 2.5 2.5 0 0 0-3.5 2 2.5 2.5 0 0 1-2.5 4.3 2.5 2.5 0 0 0 0 4.1 2.5 2.5 0 0 1 2.5 4.3 2.5 2.5 0 0 0 3.5 2 2.5 2.5 0 0 1 5 0 2.5 2.5 0 0 0 3.5-2 2.5 2.5 0 0 1 2.5-4.3 2.5 2.5 0 0 0 0-4.1 2.5 2.5 0 0 1-2.5-4.3 2.5 2.5 0 0 0-3.5-2z" fill="#f97316"/><circle cx="12" cy="12" r="3.5" fill="#ffffff"/>`,

    // 时间戳：玫红时钟 + 白色指针
    'clock': `<circle cx="12" cy="12" r="10" fill="#f43f5e"/><circle cx="12" cy="12" r="8" fill="none" stroke="#ffffff" stroke-width="0.5" opacity="0.4"/><polyline points="12 6 12 12 15.5 14" fill="none" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="12" cy="12" r="1" fill="#ffffff"/>`,

    // 定时任务：橙色秒表 + 白色指针 + 顶部按钮（与 clock 区分：多表冠 + 刻度点）
    'timer': `<circle cx="12" cy="13.5" r="8" fill="#f97316"/><circle cx="12" cy="13.5" r="6.6" fill="none" stroke="#ffffff" stroke-width="0.6" opacity="0.45"/><rect x="10.4" y="1.2" width="3.2" height="2.4" rx="0.9" fill="#c2410c"/><rect x="8.8" y="3.2" width="6.4" height="1.7" rx="0.85" fill="#c2410c"/><polyline points="12 9.8 12 13.5 15.2 15.3" fill="none" stroke="#ffffff" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/><circle cx="12" cy="13.5" r="1" fill="#ffffff"/><circle cx="12" cy="7.6" r="0.55" fill="#ffffff" opacity="0.85"/><circle cx="17.4" cy="13.5" r="0.55" fill="#ffffff" opacity="0.85"/><circle cx="12" cy="19.4" r="0.55" fill="#ffffff" opacity="0.85"/><circle cx="6.6" cy="13.5" r="0.55" fill="#ffffff" opacity="0.85"/>`,

    // Cron 解析：靛蓝日历 + 绿色时钟角章
    'calendar-clock': `<rect x="3" y="5" width="14" height="11" rx="1.5" fill="#6366f1"/><rect x="3" y="5" width="14" height="2.5" rx="1" fill="#4338ca"/><line x1="8" y1="3" x2="8" y2="6" stroke="#4338ca" stroke-width="1.5" stroke-linecap="round"/><line x1="13" y1="3" x2="13" y2="6" stroke="#4338ca" stroke-width="1.5" stroke-linecap="round"/><line x1="3" y1="9" x2="17" y2="9" stroke="#4338ca" stroke-width="0.8"/><circle cx="6" cy="12" r="0.8" fill="#ffffff"/><circle cx="9" cy="12" r="0.8" fill="#ffffff"/><circle cx="12" cy="12" r="0.8" fill="#ffffff"/><circle cx="16" cy="16" r="6" fill="#10b981"/><polyline points="16 13 16 16 18.5 17.5" fill="none" stroke="#ffffff" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>`,

    // JSONPath：粉色方块+连线
    'workflow': `<rect x="3" y="3" width="9" height="9" rx="1.5" fill="#ec4899"/><rect x="12" y="12" width="9" height="9" rx="1.5" fill="#f472b6"/><path d="M12 6h2a3 3 0 0 1 3 3v3" stroke="#ec4899" stroke-width="2" stroke-linecap="round" fill="none"/><circle cx="5" cy="5" r="1.5" fill="#ffffff" opacity="0.7"/><circle cx="14" cy="14" r="1.5" fill="#ffffff" opacity="0.7"/>`,

    // WebService 调试：靛蓝 SOAP 信封 + 橙色齿轮角章
    'soap-envelope': `<path d="M3 6l9 5 9-5-9-5z" fill="#6366f1"/><path d="M3 6v12l9 5V11z" fill="#4f46e5"/><path d="M21 6v12l-9 5V11z" fill="#818cf8"/><path d="M7 9l3 1.5L7 12z" fill="#ffffff" opacity="0.8"/><circle cx="18" cy="18" r="5" fill="#f59e0b"/><path d="M18 16v2l1.5 1" fill="none" stroke="#ffffff" stroke-width="1.4" stroke-linecap="round"/>`,

    // 关于：中性灰圆 + 白色 i（之前 #475569 在 dark 主题下太沉，改亮一档 #64748b）
    'info': `<circle cx="12" cy="12" r="10" fill="#64748b"/><circle cx="12" cy="12" r="9" fill="none" stroke="#ffffff" stroke-width="0.7" opacity="0.45"/><line x1="12" y1="16.5" x2="12" y2="11" stroke="#ffffff" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="8" r="1.3" fill="#ffffff"/>`,

    // SSH 终端（v0.10 起）：蓝色终端 + 白色 prompt + 光标
    // 区别于 square-terminal（深灰 + 红黄绿圆点）：这里是「一个具体 shell 会话」
    'terminal': `<rect x="3" y="3" width="18" height="18" rx="2" fill="#0ea5e9"/><rect x="3" y="3" width="18" height="18" rx="2" fill="none" stroke="#0284c7" stroke-width="0.5"/><polyline points="7 10 10 12 7 14" fill="none" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="12" y1="15" x2="17" y2="15" stroke="#ffffff" stroke-width="2" stroke-linecap="round"/><rect x="17" y="15" width="0.8" height="2" fill="#ffffff"/>`,

    // 便笺提醒（v0.13 起）：黄色钟铃 + 橙色摆锤
    // 区别于 clock（玫红实心圆 + 白色指针）：这里是「独立的钟 + 提醒感」
    'bell': `<path d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15z" fill="#fbbf24"/><path d="M6 16V11a6 6 0 0 1 12 0v5" fill="none" stroke="#b45309" stroke-width="0.5" opacity="0.6"/><path d="M10.5 19a1.5 1.5 0 0 0 3 0" stroke="#f59e0b" stroke-width="1.5" fill="none"/><line x1="12" y1="6.5" x2="12" y2="5" stroke="#f59e0b" stroke-width="1.5" stroke-linecap="round"/><circle cx="12" cy="4" r="0.9" fill="#f59e0b"/>`,

    // 首页（home）：橙色屋顶 + 烟囱
    'home': `<path d="M3 11.5 12 4l9 7.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z" fill="#f97316"/><path d="M3 11.5 12 4l9 7.5" fill="none" stroke="#ea580c" stroke-width="0.6"/><rect x="14" y="5" width="2" height="2.5" fill="#ea580c"/>`,

    // 搜索（lucide 风格放大镜）：青绿描边
    'search': `<circle cx="11" cy="11" r="6.5" fill="none" stroke="#06b6d4" stroke-width="2"/><line x1="15.5" y1="15.5" x2="20" y2="20" stroke="#06b6d4" stroke-width="2.2" stroke-linecap="round"/><circle cx="11" cy="11" r="2.5" fill="#06b6d4" opacity="0.25"/>`,

    // 鼠标点击（lucide mouse-pointer-click 风格）：青蓝箭头 + 三道点击波纹
    // 用途：替代 👆 之类的"指一下/点一下"emoji（Win 7 无字体支持）
    'mouse-pointer-click': `<path d="M9 4l3 13 2.5-6.5L21 8z" fill="#06b6d4" stroke="#0891b2" stroke-width="0.6" stroke-linejoin="round"/><path d="M3 4l1.5 1.5M3 9l2 0M4.5 13.5l-1.5 1.5" stroke="#06b6d4" stroke-width="1.8" stroke-linecap="round" fill="none"/>`,

    // 向下箭头（lucide arrow-down 风格）：青蓝描边
    // 用途：替代 👇 之类的"往下选"emoji（Win 7 无字体支持）
    'arrow-down': `<path d="M12 5v14M5 12l7 7 7-7" fill="none" stroke="#06b6d4" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>`,

    // 沙漏加载（lucide loader 风格 + hourglass 混合）：青蓝旋转弧
    // 用途：替代 ⏳ loading 提示（Win 7 无字体支持；静态弧形不需要动画也清晰）
    'hourglass': `<path d="M5 4h14M5 20h14" stroke="#06b6d4" stroke-width="2" stroke-linecap="round"/><path d="M7 4c0 5 2.5 6 5 8 2.5-2 5-3 5-8M7 20c0-5 2.5-6 5-8 2.5 2 5 3 5 8" fill="none" stroke="#06b6d4" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M9.5 4h5M9.5 20h5" stroke="#06b6d4" stroke-width="1.2" stroke-linecap="round"/><circle cx="9" cy="11" r="0.9" fill="#fbbf24"><animate attributeName="cy" values="11;13;11" dur="1.4s" repeatCount="indefinite"/></circle>`,

    // 哭脸（替代 😢）：红脸蛋 + 蓝眼泪（Win 7 跨平台一致）
    'sad-face': `<circle cx="12" cy="12" r="9" fill="#fecaca" stroke="#dc2626" stroke-width="1.4"/><circle cx="8.5" cy="10" r="0.9" fill="#1f2937"/><circle cx="15.5" cy="10" r="0.9" fill="#1f2937"/><path d="M8 16 Q12 13.5 16 16" fill="none" stroke="#dc2626" stroke-width="1.6" stroke-linecap="round"/><path d="M9 8 Q8.2 9.4 8.6 11" fill="none" stroke="#3b82f6" stroke-width="1.4" stroke-linecap="round"/><path d="M15 8 Q15.8 9.4 15.4 11" fill="none" stroke="#3b82f6" stroke-width="1.4" stroke-linecap="round"/>`,

    // 思考脸（替代 🤔）：黄脸 + 皱眉 + 托腮手
    'thinking-face': `<circle cx="12" cy="12" r="9" fill="#fde68a" stroke="#d97706" stroke-width="1.4"/><circle cx="8.5" cy="11" r="0.9" fill="#1f2937"/><circle cx="15.5" cy="11" r="0.9" fill="#1f2937"/><path d="M9 16 Q12 15 15 16" fill="none" stroke="#92400e" stroke-width="1.4" stroke-linecap="round"/><path d="M9 6.5 L8 5.5 M11 5.5 L10 4.5" stroke="#92400e" stroke-width="1.4" stroke-linecap="round"/><path d="M17 18 Q19 18 19 16 Q19 14 17 14 L16 14 L16 18 Z" fill="#fde68a" stroke="#d97706" stroke-width="1.2" stroke-linejoin="round"/>`,

    // 状态点（dot 风格）：彩色实心圆点。color 字段允许覆盖默认 #10b981
    // 用途：替代 🟢 / 🟡 / 🔴 状态 emoji（Win 7 无字体支持，且圆点配色不可控）
    'status-dot': `<circle cx="12" cy="12" r="6" fill="#10b981"/><circle cx="12" cy="12" r="6" fill="none" stroke="#ffffff" stroke-width="0.6" opacity="0.5"/>`,

    // 赞助作者：琥珀色咖啡杯 + 热气
    'coffee': `<path d="M5 9h11v5a3.5 3.5 0 0 1-3.5 3.5h-4A3.5 3.5 0 0 1 5 14z" fill="#b45309"/><path d="M5 9h11v1H5z" fill="#92400e"/><path d="M16 10.5h2a2 2 0 0 1 0 3h-2" fill="none" stroke="#b45309" stroke-width="2"/><path d="M8 4.5c0 1.2-1 1.8-1 3M11.5 3.5c0 1.2-1 1.8-1 3M15 4.5c0 1.2-1 1.8-1 3" fill="none" stroke="#f59e0b" stroke-width="1.6" stroke-linecap="round"/><rect x="3" y="19" width="15" height="1.8" rx="0.8" fill="#92400e"/>`,
  };

  // 主题按钮 5 个图标（彩色实心版，与工具卡片风格统一）
  const THEMES = {
    // dark → 月亮（继承 currentColor，避免 #1e293b 在 dark topbar 上对比度仅 1.3:1）
    moon: `<path d="M20.985 12.486a9 9 0 1 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401z" fill="currentColor"/><path d="M20.985 12.486a9 9 0 0 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401z" fill="currentColor" opacity="0.35"/>`,
    // light → 太阳（黄色）
    sun: `<circle cx="12" cy="12" r="4" fill="#fbbf24"/><g stroke="#f59e0b" stroke-width="2" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="5"/><line x1="12" y1="19" x2="12" y2="22"/><line x1="4.93" y1="4.93" x2="7.05" y2="7.05"/><line x1="16.95" y1="16.95" x2="19.07" y2="19.07"/><line x1="2" y1="12" x2="5" y2="12"/><line x1="19" y1="12" x2="22" y2="12"/><line x1="4.93" y1="19.07" x2="7.05" y2="16.95"/><line x1="16.95" y1="7.05" x2="19.07" y2="4.93"/></g>`,
    // green → 叶子（翠绿）
    leaf: `<path d="M11 20A7 7 0 0 1 9.8 6.1C15.5 5 17 4.48 19 2c1 2 2 4.18 2 8 0 5.5-4.78 10-10 10Z" fill="#22c55e"/><path d="M2 21c0-3 1.85-5.36 5.08-6C9.5 14.52 12 13 13 12" stroke="#15803d" stroke-width="2" stroke-linecap="round" fill="none"/>`,
    // hc → 高对比（黑黄半圆）
    contrast: `<circle cx="12" cy="12" r="10" fill="#000000"/><path d="M12 2a10 10 0 0 1 0 20z" fill="#fbbf24"/>`,
    // xianxia → 剑（古铜紫）
    sword: `<path d="M9.5 17.5 21 6V3h-3L6.5 14.5z" fill="#a855f7"/><path d="m11 19-6-6" stroke="#7c3aed" stroke-width="2.5" stroke-linecap="round" fill="none"/><path d="m5 21-2-2" stroke="#7c3aed" stroke-width="2.5" stroke-linecap="round"/><path d="m8 16-4 4" stroke="#7c3aed" stroke-width="2.5" stroke-linecap="round"/>`,
  };

  // README 章节图标（13 个，用 section- 前缀避免和工具卡片 key 冲突）
  // 用于 README.md / 文档章节标题，SVG img 引用 `docs/section-icons/*.svg`
  const SECTIONS = {
    'section-overview':    `<path d="M14 3v4a1 1 0 0 0 1 1h4v9a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" fill="#3b82f6"/><path d="M14 3v4a1 1 0 0 0 1 1h4l-5-5z" fill="#1e40af"/><rect x="8" y="11" width="8" height="1.5" rx="0.5" fill="#ffffff"/><rect x="8" y="14" width="8" height="1.5" rx="0.5" fill="#ffffff"/><rect x="8" y="17" width="5" height="1.5" rx="0.5" fill="#ffffff" opacity="0.7"/>`,
    'section-philosophy':  `<circle cx="12" cy="12" r="4" fill="#fbbf24"/><circle cx="12" cy="12" r="3" fill="#ffffff"/><circle cx="12" cy="12" r="1.6" fill="#f59e0b"/><g stroke="#f59e0b" stroke-width="2.2" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="5"/><line x1="12" y1="19" x2="12" y2="22"/><line x1="4.93" y1="4.93" x2="7.05" y2="7.05"/><line x1="16.95" y1="16.95" x2="19.07" y2="19.07"/><line x1="2" y1="12" x2="5" y2="12"/><line x1="19" y1="12" x2="22" y2="12"/><line x1="4.93" y1="19.07" x2="7.05" y2="16.95"/><line x1="16.95" y1="7.05" x2="19.07" y2="4.93"/></g>`,
    'section-architecture':`<path d="M12 2 2 7l10 5 10-5z" fill="#6366f1"/><path d="M2 12l10 5 10-5" fill="none" stroke="#4338ca" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M2 17l10 5 10-5-10-5z" fill="#818cf8"/>`,
    'section-stack':       `<rect x="3" y="3" width="18" height="18" rx="3" fill="#1f2937"/><polyline points="16 8 20 12 16 16" fill="none" stroke="#22d3ee" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/><polyline points="8 8 4 12 8 16" fill="none" stroke="#22d3ee" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/><line x1="14" y1="4" x2="9" y2="20" stroke="#22d3ee" stroke-width="2.4" stroke-linecap="round"/>`,
    'section-security':    `<path d="M12 2.5 5.5 5v6.5c0 5 3.5 8.5 6.5 9.5 3-1 6.5-4.5 6.5-9.5V5z" fill="#10b981"/><path d="M9 11.5 11 13.5 14.5 10" fill="none" stroke="#ffffff" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>`,
    'section-ssh':         `<rect x="3" y="3" width="18" height="18" rx="2" fill="#1f2937"/><circle cx="6" cy="6.5" r="0.8" fill="#ef4444"/><circle cx="8.5" cy="6.5" r="0.8" fill="#eab308"/><circle cx="11" cy="6.5" r="0.8" fill="#10b981"/><polyline points="8 11 11 13 8 15" fill="none" stroke="#10b981" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="13" y1="15" x2="17" y2="15" stroke="#10b981" stroke-width="2" stroke-linecap="round"/>`,
    'section-quality':     `<circle cx="12" cy="12" r="10" fill="#10b981"/><polyline points="9 11 12 14 21 5" fill="none" stroke="#ffffff" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round"/>`,
    'section-modules':     `<rect x="3" y="3" width="8" height="8" rx="1.5" fill="#06b6d4"/><rect x="13" y="3" width="8" height="8" rx="1.5" fill="#0891b2"/><rect x="13" y="13" width="8" height="8" rx="1.5" fill="#22d3ee"/><rect x="3" y="13" width="8" height="8" rx="1.5" fill="#67e8f9"/><circle cx="7" cy="7" r="1.5" fill="#ffffff" opacity="0.7"/><circle cx="17" cy="17" r="1.5" fill="#ffffff" opacity="0.7"/>`,
    'section-compare':     `<circle cx="6" cy="6" r="3" fill="#7c3aed"/><circle cx="18" cy="18" r="3" fill="#a855f7"/><path d="M9 6h4a3 3 0 0 1 3 3v6" stroke="#7c3aed" stroke-width="2.5" stroke-linecap="round" fill="none"/><path d="M15 18h-4a3 3 0 0 1-3-3V9" stroke="#a855f7" stroke-width="2.5" stroke-linecap="round" fill="none"/>`,
    'section-incident':    `<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" fill="#f97316"/>`,
    'section-changelog':   `<path d="M2 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2z" fill="#8b5cf6"/><path d="M2 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v1H2z" fill="#7c3aed"/><circle cx="17" cy="16" r="4.2" fill="#ffffff"/><line x1="17" y1="16" x2="17" y2="13.5" stroke="#8b5cf6" stroke-width="1.6" stroke-linecap="round"/><line x1="17" y1="16" x2="19" y2="17.5" stroke="#8b5cf6" stroke-width="1.6" stroke-linecap="round"/><circle cx="17" cy="16" r="1" fill="#8b5cf6"/>`,
    'section-faq':         `<circle cx="12" cy="12" r="10" fill="#ec4899"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" fill="none" stroke="#ffffff" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/><circle cx="12" cy="17" r="1.4" fill="#ffffff"/>`,
    'section-roadmap':     `<circle cx="6" cy="19" r="3" fill="#10b981"/><circle cx="15" cy="5" r="3" fill="#22d3ee"/><path d="M9 19h8.5a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7H15" stroke="#3b82f6" stroke-width="2.6" fill="none" stroke-linecap="round"/>`,
    'section-sparkle':    `<path d="M12 2 L13.5 9 L20 10.5 L13.5 12 L12 19 L10.5 12 L4 10.5 L10.5 9 Z" fill="#fbbf24"/><path d="M12 4.5 L12.8 8 L15.5 8.7 L12.8 9.4 L12 13 L11.2 9.4 L8.5 8.7 L11.2 8 Z" fill="#fef3c7"/><path d="M19 4 L20 7 L23 8 L20 9 L19 12 L18 9 L15 8 L18 7 Z" fill="#fbbf24" opacity="0.7"/><path d="M5 16 L5.5 17.5 L7 18 L5.5 18.5 L5 20 L4.5 18.5 L3 18 L4.5 17.5 Z" fill="#fbbf24" opacity="0.7"/>`,
    'section-rocket':     `<path d="M12 2 C9 5 7 8 7 12 v3 h10 v-3 c0-4 -2-7 -5-10 z" fill="#ef4444"/><path d="M12 2 C9 5 7 8 7 12 v3 h10 v-3 c0-4 -2-7 -5-10 z" fill="none" stroke="#b91c1c" stroke-width="0.8"/><circle cx="12" cy="10" r="1.8" fill="#dbeafe"/><circle cx="12" cy="10" r="0.9" fill="#3b82f6"/><path d="M9 15 L10 19 L11 16 L12 20 L13 16 L14 19 L15 15 Z" fill="#f59e0b"/><path d="M7 12 v3 l-2 1 v-3 z M17 12 v3 l2 1 v-3 z" fill="#dc2626"/>`,
    'section-radio':      `<path d="M5 12 a7 7 0 0 1 14 0" fill="none" stroke="#06b6d4" stroke-width="2.4" stroke-linecap="round"/><path d="M2 12 a10 10 0 0 1 20 0" fill="none" stroke="#06b6d4" stroke-width="2.4" stroke-linecap="round" opacity="0.55"/><circle cx="12" cy="14" r="1.6" fill="#06b6d4"/><path d="M12 14 L12 18 a2 2 0 0 0 2 2 h2" fill="none" stroke="#0891b2" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>`,
    'section-package':    `<path d="M3 7 L12 3 L21 7 L21 17 L12 21 L3 17 Z" fill="#92400e"/><path d="M3 7 L12 11 L21 7" fill="none" stroke="#fbbf24" stroke-width="1.6" stroke-linejoin="round"/><path d="M12 11 L12 21" stroke="#fbbf24" stroke-width="1.6"/><path d="M7.5 5 L16.5 9" stroke="#fbbf24" stroke-width="1.6" stroke-linecap="round"/><circle cx="12" cy="9" r="0.9" fill="#fef3c7"/>`,
    'section-foldertree': `<path d="M2 4 a1 1 0 0 1 1 -1 h4 l2 2 h6 a1 1 0 0 1 1 1 v3 H2 z" fill="#92400e"/><path d="M2 4 a1 1 0 0 1 1 -1 h4 l2 2 h6 a1 1 0 0 1 1 1 v8 a1 1 0 0 1 -1 1 H3 a1 1 0 0 1 -1 -1 z" fill="#d97706"/><rect x="6" y="11" width="5" height="3.5" rx="0.7" fill="#f59e0b"/><rect x="14" y="11" width="5" height="3.5" rx="0.7" fill="#f59e0b"/><rect x="10" y="16" width="5" height="3.5" rx="0.7" fill="#fbbf24"/><path d="M8.5 11 v1 h3 v1.5" fill="none" stroke="#92400e" stroke-width="0.9"/><path d="M16.5 11 v1 h-3 v1.5" fill="none" stroke="#92400e" stroke-width="0.9"/>`,
  };

  const PATHS = Object.assign({}, TOOLS, THEMES, SECTIONS);

  function svg(name, size) {
    const inner = PATHS[name];
    if (!inner) return null;
    const w = String(size || 24);
    const el = document.createElementNS(NS, 'svg');
    el.setAttribute('xmlns', NS);
    el.setAttribute('width', w);
    el.setAttribute('height', w);
    el.setAttribute('viewBox', VB);
    el.setAttribute('aria-hidden', 'true');
    el.innerHTML = inner;
    return el;
  }

  function toHTML(name, size) {
    const inner = PATHS[name];
    if (!inner) return '';
    const w = String(size || 24);
    return '<svg xmlns="' + NS + '" width="' + w + '" height="' + w + '" viewBox="' + VB + '" aria-hidden="true">' + inner + '</svg>';
  }

  icons.svg = svg;
  icons.innerHTML = toHTML;
  icons.has = function (name) { return !!PATHS[name]; };
  icons.PATHS = PATHS;

  // ---------- 外部打开器图标统一渲染 ----------
  //
  // 设计动机：v0.14 前各页面（config/downloads/files/ssh）各自 inline 一份
  // "emoji 优先 / 否则按名推断颜色 + 通用 SVG 占位" 的逻辑，且都没有真实 exe
  // 图标。v0.14 新增 Windows exe 图标提取后，把渲染逻辑统一收口到这里，
  // 4 个页面共用同一份。
  //
  // 渲染优先级：
  //   1. op.icon 是 emoji → 直接返回 emoji span（用户在配置页手填的 emoji）
  //   2. 后端已缓存 exe 图标 → <img src="/api/local/opener-icon?name=...&t=hash">
  //      onerror 时自动换成 SVG fallback（防止缓存未命中 / 非平台支持时显示破图）
  //   3. 兜底：按 path/name 关键词推断颜色 + 通用 file-text SVG 占位
  //
  // 入参：
  //   op: { name, path, icon }（ExternalOpener 结构）
  //   size: 图标像素尺寸，默认 14
  //
  // 返回 HTML 字符串，调用方用 unsafeHtml 注入到 button/span 里。
  function inferOpenerColor(path, name) {
    const s = ((path || '') + ' ' + (name || '')).toLowerCase();
    if (s.includes('code') || s.includes('vscode') || s.includes('vs ')) return '#3b82f6';
    if (s.includes('notepad') || s.includes('npp')) return '#f59e0b';
    if (s.includes('idea') || s.includes('intellij')) return '#f97316';
    if (s.includes('vim') || s.includes('nvim')) return '#374151';
    if (s.includes('sublime')) return '#8b5cf6';
    if (s.includes('terminal') || s.includes('cmd') || s.includes('powershell') || s.includes('iterm')) return '#10b981';
    if (s.includes('excel') || s.includes('xlsx')) return '#22c55e';
    if (s.includes('word') || s.includes('docx')) return '#3b82f6';
    return '#64748b';
  }

  // 通用文件 SVG 占位（单色 stroke，可指定颜色）。
  // 跟 icons.js 整体"彩色实心"风格不同——这里用 stroke 是因为占位图本身就是
  // 临时替代品，视觉重量轻一点更好，让真实 exe 图标（彩色 PNG）来当主角。
  function openerFallbackSVG(path, name, size) {
    const s = size || 14;
    const color = inferOpenerColor(path, name);
    // 用 file-text 路径（与 Kairo.icons.PATHS['file-text'] 风格一致但用 stroke）
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="' + color + '" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
      '<path d="M14 3v4a1 1 0 0 0 1 1h4v9a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z"/>' +
      '<path d="M14 3v4a1 1 0 0 0 1 1h4l-5-5z"/>' +
      '<line x1="9" y1="13" x2="15" y2="13"/>' +
      '<line x1="9" y1="17" x2="13" y2="17"/>' +
      '</svg>';
  }

  // 简单 hash（djb2）→ 36 进制，用作 URL 的 cacheBust 参数。
  // 同一 path 始终产生同一 hash；path 改了 hash 变，浏览器重新拉图标。
  function pathHash(s) {
    let h = 5381;
    for (let i = 0; i < s.length; i++) {
      h = ((h << 5) + h + s.charCodeAt(i)) >>> 0;
    }
    return h.toString(36);
  }

  function openerIconHTML(op, size) {
    if (!op) return '';
    const s = size || 14;
    const ic = (typeof op.icon === 'string') ? op.icon.trim() : '';
    if (ic) {
      // emoji 检测：覆盖常见 emoji 区段（与原各页面逻辑一致）
      const isEmoji = /[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u.test(ic);
      if (isEmoji) {
        return '<span style="font-size:' + s + 'px; line-height:1; display:inline-flex; align-items:center;">' + escapeHTML(ic) + '</span>';
      }
    }
    // 没有真实 exe 图标缓存时（非 Windows 平台 / 提取失败）走 fallback；
    // <img> 的 onerror 会替换 outerHTML 为 SVG，避免破图。
    // name 必须 encodeURIComponent 防 URL 注入；path hash 用于 cacheBust。
    const name = op.name || '';
    if (!name) {
      return openerFallbackSVG(op.path || '', op.name || '', s);
    }
    const url = '/api/local/opener-icon?name=' + encodeURIComponent(name) + '&t=' + pathHash(op.path || name);
    const fallbackSVG = openerFallbackSVG(op.path || '', op.name || '', s)
      // onerror 里塞的是 JS 字符串，SVG 里的双引号要转成 &quot; 才不会破坏 HTML 属性
      .replace(/"/g, '&quot;');
    return '<img src="' + url + '" alt="" width="' + s + '" height="' + s + '" ' +
      'style="width:' + s + 'px; height:' + s + 'px; vertical-align:middle; object-fit:contain;" ' +
      'onerror="this.outerHTML=\'' + fallbackSVG + '\'" ' +
      'loading="lazy" decoding="async">';
  }
  icons.openerIconHTML = openerIconHTML;

  // escapeHTML 跟 core.js 的实现保持一致（这里 inline 一份避免循环依赖）。
  // 注意：修改时务必与 core.js 的 escapeHtml 保持同步（含 null 守卫）。
  function escapeHTML(s) {
    if (s == null) return '';
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }
})();