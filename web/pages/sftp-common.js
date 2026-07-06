/* ===== web/pages/sftp-common.js =====
 * SFTP 共享模块（v0.11+）：files 页和 ssh tab 内嵌 SFTP 面板共用。
 *
 * 提供：
 *   - SftpCommon.subscribeDownload(id, route, callbacks) → EventSource
 *       订阅 /api/ssh/sftp/download/{id}/events 或 /api/files/download/{id}/events
 *       自动处理 done 事件 + onerror 收尾，避免 EventSource 自动重连刷 404。
 *   - SftpCommon.formatBytes(n) → "1.23 MB"
 *   - SftpCommon.formatMTime(rfc3339) → "2026-07-04 09:30"
 *   - SftpCommon.escapeHtml(s)
 *   - SftpCommon.openPreviewWindow(previewUrl)  在新 tab 打开预览页面
 *   - SftpCommon.apiDownloadSshSftp(payload) → 启动 SSH tab 内嵌 SFTP 下载，返回 {id, target_dir}
 *
 * 设计要点：
 *   - 不依赖 Kairo.core 的 toast，由调用方在 callbacks 里自行决定 UI 反馈；
 *   - SSE done 一旦见到立刻 close，防止自动重连（参考 files.js P1-BUG-4 修复）；
 *   - 兼容 EventSource 不存在的浏览器（调用方应在调用前自查）。
 */
(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};

  // 路由前缀：files 页走 /api/files/download，ssh tab 走 /api/ssh/sftp/download
  const ROUTE_FILES = '/api/files/download/';
  const ROUTE_SSH_SFTP = '/api/ssh/sftp/download/';

  // subscribeDownload 订阅下载进度 SSE。
  //   id: 下载任务 id
  //   route: ROUTE_FILES | ROUTE_SSH_SFTP
  //   callbacks: {
  //     onMessage(o),      // 每个 SSE message（含 done）
  //     onDone(o),         // done 事件（o.ok / o.error / o.downloads / o.folder）
  //     onError(reason),   // SSE 异常收尾（reason='done'|'error'）
  //   }
  // 返回 EventSource 实例（调用方持有引用，便于 close）。
  function subscribeDownload(id, route, callbacks) {
    if (!window.EventSource) {
      callbacks.onError('no_eventsource');
      return null;
    }
    const url = route + id + '/events';
    const es = new EventSource(url);
    let gotDone = false;

    const onDoneSeen = function (reason) {
      if (gotDone) return;
      gotDone = true;
      try { es.onerror = null; } catch (e) { /* ignore */ }
      try { es.onmessage = null; } catch (e) { /* ignore */ }
      try { es.close(); } catch (e) { /* ignore */ }
      if (callbacks.onError) callbacks.onError(reason);
    };

    es.onmessage = function (ev) {
      let o;
      try { o = JSON.parse(ev.data); } catch (e) { return; }
      if (o && o.kind === 'done') {
        if (callbacks.onDone) callbacks.onDone(o);
        if (callbacks.onMessage) callbacks.onMessage(o);
        onDoneSeen('done');
        return;
      }
      if (callbacks.onMessage) callbacks.onMessage(o);
    };
    es.addEventListener('done', function () { onDoneSeen('done'); });
    es.onerror = function () {
      if (gotDone) {
        try { es.close(); } catch (e) { /* ignore */ }
        return;
      }
      // SSE 异常：2s 后若仍未 gotDone，强制收尾（防止 EventSource 重连刷 404）
      setTimeout(function () {
        if (!gotDone) {
          onDoneSeen('error');
        }
      }, 2000);
    };
    return es;
  }

  // formatBytes 把字节数格式化成人类可读字符串。
  //   < 1024 → "123 B"
  //   < 1024*1024 → "1.23 KB"
  //   < 1024*1024*1024 → "1.23 MB"
  //   else → "1.23 GB"
  function formatBytes(n) {
    if (n == null || isNaN(n)) return '-';
    n = Number(n);
    if (n < 0) return '-';
    if (n < 1024) return n + ' B';
    const units = ['KB', 'MB', 'GB', 'TB'];
    let i = -1;
    let v = n;
    do {
      v /= 1024;
      i++;
    } while (v >= 1024 && i < units.length - 1);
    return v.toFixed(2) + ' ' + units[i];
  }

  // formatMTime 把 RFC3339 时间格式化成 "YYYY-MM-DD HH:mm"（本地时区）。
  function formatMTime(rfc3339) {
    if (!rfc3339) return '-';
    try {
      const d = new Date(rfc3339);
      if (isNaN(d.getTime())) return '-';
      const pad = function (n) { return n < 10 ? '0' + n : '' + n; };
      return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
        ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
    } catch (e) {
      return '-';
    }
  }

  // escapeHtml 简易 HTML 转义（防止文件名含 < > & " '）。
  function escapeHtml(s) {
    if (s == null) return '';
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // isTextFileByExt 根据扩展名判断是否文本文件（用于「双击预览」）。
  // 策略：白名单（常见代码/配置/脚本/标记语言）→ 黑名单（已知二进制）→ 默认按文本。
  // 白名单解决扩展名撞名问题（如 .ts 既可是 TypeScript 源码也可是 MPEG-TS 视频，
  // 运维场景下代码/配置/日志更常见，白名单优先判文本）；
  // 黑名单拦截图片/音视频/压缩包/可执行等确定是二进制的格式；
  // 其余无扩展名/未知扩展名默认按文本，服务端 NUL 字节检测兜底二进制误判。
  function isTextFileByExt(name) {
    if (!name) return false;
    const lower = name.toLowerCase();
    // 白名单：常见代码/脚本/配置/标记语言/数据交换格式（确保按文本预览）
    const textExts = new Set([
      // 编程语言
      '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx', '.py', '.rb', '.pl', '.pm',
      '.php', '.java', '.kt', '.kts', '.scala', '.groovy', '.gradle',
      '.c', '.h', '.cpp', '.cc', '.cxx', '.hpp', '.hh', '.hxx', '.m', '.mm',
      '.go', '.rs', '.swift', '.cs', '.fs', '.fsx', '.vb', '.r',
      '.lua', '.tcl', '.awk', '.sed', '.sh', '.bash', '.zsh', '.fish',
      '.bat', '.cmd', '.ps1', '.psm1', '.vbs',
      '.dart', '.el', '.lisp', '.clj', '.cljs', '.hs', '.erl', '.ex', '.exs',
      '.ml', '.mli', '.nim', '.zig', '.cr',
      // Web/标记/样式
      '.html', '.htm', '.xhtml', '.xml', '.xsl', '.xslt', '.svg', '.vue', '.svelte',
      '.css', '.scss', '.sass', '.less', '.styl',
      '.jsp', '.jspx', '.asp', '.aspx', '.cshtml', '.vbhtml',
      // 数据/配置
      '.json', '.json5', '.jsonc', '.yaml', '.yml', '.toml', '.ini', '.cfg', '.conf',
      '.properties', '.prop', '.env', '.envrc', '.editorconfig',
      '.plist', '.csv', '.tsv',
      // 标记/文档（纯文本类）
      '.md', '.markdown', '.rst', '.txt', '.text', '.org', '.adoc', '.asciidoc',
      '.tex', '.latex', '.ltx',
      // 模板
      '.tmpl', '.tpl', '.template', '.mustache', '.handlebars', '.hbs', '.pug', '.jade',
      '.ejs', '.jinja', '.jinja2', '.j2',
      // 日志/特殊
      '.log', '.out', '.err', '.trace',
      '.sql', '.ddl', '.dml',
      '.patch', '.diff', '.rej',
      '.nfo', '.diz',
      // 点文件配置（无其他扩展名，文件名本身以 . 开头）
      '.gitignore', '.gitattributes', '.gitmodules', '.npmignore', '.dockerignore',
      '.eslintrc', '.prettierrc', '.babelrc', '.bashrc', '.bash_profile', '.bash_login',
      '.profile', '.zshrc', '.zshenv', '.zprofile', '.zlogin', '.zlogout',
      '.vimrc', '.exrc', '.gitconfig', '.npmrc', '.yarnrc',
    ]);
    // 无扩展名但命中文件名白名单（全小写比较）
    const textBasenames = new Set([
      'makefile', 'dockerfile', 'rakefile', 'gemfile', 'vagrantfile',
      'readme', 'changelog', 'changes', 'news', 'copying', 'license', 'licence',
      'authors', 'contributors', 'todo', 'notes', 'notice',
    ]);
    // 已知二进制扩展名（压缩包/图片/音视频/可执行/字体/Office文档等）→ 不预览
    const binaryExts = [
      // 压缩/归档
      '.zip', '.tar', '.gz', '.tgz', '.bz2', '.xz', '.7z', '.rar', '.jar', '.war',
      '.ear', '.deb', '.rpm', '.dmg', '.iso', '.pkg',
      // 图片
      '.png', '.jpg', '.jpeg', '.gif', '.bmp', '.ico', '.webp', '.tiff', '.tif',
      '.psd', '.ai', '.eps',
      // 音视频
      '.mp3', '.mp4', '.wav', '.flac', '.ogg', '.aac', '.wma', '.avi', '.mkv',
      '.mov', '.wmv', '.flv', '.webm', '.m2ts', '.mts',
      // 可执行/库
      '.exe', '.dll', '.so', '.dylib', '.o', '.a', '.lib', '.obj', '.bin', '.class',
      '.pyc', '.pyo', '.elc', '.ko',
      // 字体
      '.ttf', '.otf', '.woff', '.woff2', '.eot',
      // Office/PDF
      '.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx', '.odt', '.ods', '.odp',
      // 数据库
      '.db', '.sqlite', '.sqlite3', '.mdb',
      // 其他二进制
      '.swf', '.pdb', '.wasm', '.apk', '.ipa', '.keystore', '.jks',
    ];
    // 白名单优先（解决 .ts 等扩展名歧义）
    const dot = lower.lastIndexOf('.');
    const basename = dot >= 0 ? lower.substring(0, dot) : lower;
    const ext = dot >= 0 ? lower.substring(dot) : '';
    if (textExts.has(ext) || textBasenames.has(lower) || textBasenames.has(basename)) return true;
    for (let i = 0; i < binaryExts.length; i++) {
      if (lower.endsWith(binaryExts[i])) return false;
    }
    // 无扩展名/未知扩展名默认按文本处理（服务端 NUL 字节检测兜底）
    return true;
  }

  // openPreviewWindow 在新 tab 打开预览页面。
  // previewUrl 形如 "/preview.html?system=X&server=Y&path=/var/log/app.log&encoding=utf-8&source=ssh-sftp"
  // 不带 size 参数 → 浏览器开新 tab 而非 popup 窗口（项目约定）
  function openPreviewWindow(previewUrl) {
    window.open(previewUrl, '_blank');
  }

  // apiDownloadSshSftp 启动 SSH tab 内嵌 SFTP 下载。
  // payload: { system, server, paths: [...], zip, target_dir? }
  // 返回 Promise<{id, target_dir}>
  function apiDownloadSshSftp(payload) {
    const K = window.Kairo;
    const api = K.api && K.api.api;
    if (!api) return Promise.reject(new Error('Kairo.api.api 未初始化'));
    return api('POST', '/api/ssh/sftp/download', payload);
  }

  // apiDownloadFiles 启动 files 页下载（保留接口对称）。
  function apiDownloadFiles(payload) {
    const K = window.Kairo;
    const api = K.api && K.api.api;
    if (!api) return Promise.reject(new Error('Kairo.api.api 未初始化'));
    return api('POST', '/api/files/download', payload);
  }

  Kairo.SftpCommon = {
    subscribeDownload: subscribeDownload,
    formatBytes: formatBytes,
    formatMTime: formatMTime,
    escapeHtml: escapeHtml,
    isTextFileByExt: isTextFileByExt,
    openPreviewWindow: openPreviewWindow,
    apiDownloadSshSftp: apiDownloadSshSftp,
    apiDownloadFiles: apiDownloadFiles,
    ROUTE_FILES: ROUTE_FILES,
    ROUTE_SSH_SFTP: ROUTE_SSH_SFTP,
  };
})();
