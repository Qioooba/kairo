/* ===== web/pages/diagnostics.js =====
 * 环境自检 — 调用 /api/diagnostics
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  const { el, kvTable, toast } = Kairo.core;
  const { api } = Kairo.api;

  const ICONS = {
    smCheck:   'M20 6L9 17l-5-5',
    smX:       'M18 6L6 18M6 6l12 12',
    smWarn:    'M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0zM12 9v4M12 17h.01',
  };
  function svgIcon(name, size) {
    const d = ICONS[name];
    if (!d) return '';
    const s = size || 16;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

  function renderDiagnostics(view) {
    const summaryEl = el('div', { class: 'text-dim mt-2', text: '加载中…' });
    const btnRefresh = el('button', { class: 'btn btn-primary', text: '重新自检', onclick: load });
    const btnSkipServers = el('button', { class: 'btn', text: '只看本机（跳过 server 检查）', onclick: () => load(true) });
    const issuesCard = el('div');
    const sectionsWrap = el('div');
    const toolsWrap = el('div');
    const serversWrap = el('div');

    view.appendChild(el('div', { class: 'card' }, [
      el('h3', { text: '环境自检' }),
      el('div', { class: 'card-desc', text: '本机环境、磁盘可写性、监听地址、凭据后端、本机命令工具、配置里每台 server 的 DNS + TCP 连通性。' }),
      el('div', { class: 'btn-row mt-2' }, [btnRefresh, btnSkipServers]),
      summaryEl
    ]));
    view.appendChild(issuesCard);
    view.appendChild(sectionsWrap);
    view.appendChild(toolsWrap);
    view.appendChild(serversWrap);

    async function load(skipServers) {
      summaryEl.textContent = '加载中…';
      issuesCard.innerHTML = '';
      sectionsWrap.innerHTML = '';
      toolsWrap.innerHTML = '';
      serversWrap.innerHTML = '';
      const qs = new URLSearchParams();
      if (skipServers) qs.set('check_servers', 'false');
      try {
        const r = await api('GET', '/api/diagnostics' + (qs.toString() ? '?' + qs : ''));
        render(r);
      } catch (e) {
        summaryEl.textContent = '加载失败：' + e.message;
        summaryEl.className = 'text-err mt-2';
      }
    }

    function render(r) {
      const okN = (r.servers || []).filter(s => s.dns && s.dns.ok && s.tcp && s.tcp.ok).length;
      const totalN = (r.servers || []).length;
      summaryEl.textContent = '生成于 ' + r.generated_at + ' · ' + okN + '/' + totalN + ' 台 server 网络可达';
      summaryEl.className = 'text-dim mt-2';

      // 顶部 Issues 红条（如果有）
      if (r.issues && r.issues.length) {
        issuesCard.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '需要关注 (' + r.issues.length + ')' }),
          el('div', {}, r.issues.map(t => {
            let icon = 'smWarn';
            let text = t;
            if (t.startsWith('⚠ ')) {
              icon = 'smWarn';
              text = t.slice(2);
            } else if (t.startsWith('✗ ')) {
              icon = 'smX';
              text = t.slice(2);
            }
            return el('div', { class: 'text-warn', style: 'display:flex; align-items:flex-start; gap:6px; margin-bottom:4px;', unsafeHtml: '<span style="flex-shrink:0; margin-top:2px;">' + svgIcon(icon, 14) + '</span>' + text });
          }))
        ]));
      } else {
        issuesCard.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '需要关注' }),
          el('div', { class: 'text-dim', style: 'display:inline-flex; align-items:center; gap:6px;', unsafeHtml: svgIcon('smCheck', 16) + ' 一切正常' })
        ]));
      }

      sectionsWrap.appendChild(groupedKvCard('应用', [
        ['名称', r.app.name],
        ['监听', r.app.listen_addr + ' (' + r.app.host + ':' + r.app.port + ')'],
        ['凭据后端', r.app.credential_store],
        ['文件浏览器', r.app.free_file_browser ? '开' : '关'],
        ['文件浏览器白名单', (r.app.free_file_roots || []).join(', ') || '（未设，按账号权限放行）'],
        ['SSH 兼容 profile', r.app.ssh_compat_profile || 'auto'],
        ['SSH 调试日志', r.app.ssh_debug ? '开' : '关'],
        ['SSH 流量镜像', r.app.ssh_traffic_dump ? '开' : '关'],
        ['SSH 日志上限', r.app.ssh_log_max_mb + ' MB × ' + r.app.ssh_log_keep + ' 份'],
        ['下载目录', r.app.download_dir],
        ['日志目录', r.app.log_dir],
        ['数据目录', r.app.data_dir],
        ['配置路径', r.app.config_path]
      ]));
      sectionsWrap.appendChild(groupedKvCard('运行时', [
        ['Go 版本', r.build.go_version],
        ['GOOS/GOARCH', r.build.goos + '/' + r.build.goarch],
        ['x/crypto/ssh', r.build.crypto_ssh_version],
        ['数据目录可写', yesNo(r.runtime.data_dir_writable)],
        ['下载目录可写', yesNo(r.runtime.download_dir_writable)],
        ['日志目录可写', yesNo(r.runtime.log_dir_writable)],
        ['工作目录', r.runtime.working_dir],
        ['PID', String(r.runtime.pid)],
        ['Goroutine 数', String(r.runtime.num_goroutine)]
      ]));
      if (r.database) {
        sectionsWrap.appendChild(groupedKvCard('数据库与驱动兼容性', [
          ['当前 Oracle 驱动', r.database.backend || 'go-ora'],
          ['OCI 原生加速', r.database.native_oci_available ? yesNo(true) : { html: '<span style="display:inline-flex;align-items:center;gap:4px;color:var(--text-dim);">未启用（纯 Go 驱动）</span>' }],
          ['程序架构', r.database.app_bitness || r.build.goarch || 'amd64'],
          ['客户端架构', r.database.client_bitness || '未检测到'],
          ['PL/SQL Developer', r.database.plsql_detected ? (r.database.plsql_bitness + ' · ' + (r.database.plsql_detail || '已检测到')) : '未检测到'],
          ['架构兼容状态', r.database.bitness_mismatch
            ? { html: '<span style="display:inline-flex;align-items:center;gap:4px;color:#f59e0b;">' + svgIcon('smWarn', 14) + ' 32/64 位不匹配（已自动降级为 go-ora 保证连接）</span>' }
            : yesNo(true)
          ],
          ['驱动与环境说明', r.database.detail || '正常']
        ]));
      }

      toolsWrap.appendChild(el('div', { class: 'card' }, [
        el('h3', { text: '本机命令工具' }),
        toolsTable(r.tools),
        // Note 字段：当前平台探测这些 Linux 命令工具的意义说明。
        // Windows 上 Note 解释为什么 find/grep/sed/tail/unzip 没找到是正常的。
        r.tools && r.tools.note ? el('div', { class: 'text-dim mt-2', style: 'font-size: 12.5px;', text: r.tools.note }) : null
      ]));

      if (r.servers && r.servers.length) {
        serversWrap.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '配置 server 连通性 (' + r.servers.length + ' 台)' }),
          serversTable(r.servers)
        ]));
      } else {
        serversWrap.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '配置 server 连通性' }),
          el('div', { class: 'text-dim', text: '（未勾选 check_servers，或配置里没有 server）' })
        ]));
      }
    }

    function yesNo(b) {
      return {
        html: b
          ? '<span style="display:inline-flex;align-items:center;gap:4px;color:#22c55e;">' + svgIcon('smCheck', 14) + ' 是</span>'
          : '<span style="display:inline-flex;align-items:center;gap:4px;color:#ef4444;">' + svgIcon('smX', 14) + ' 否</span>'
      };
    }
    function groupedKvCard(title, rows) {
      return el('div', { class: 'card' }, [
        el('h3', { text: title }),
        kvTable(rows)
      ]);
    }
    function toolsTable(t) {
      const tbl = el('table', { class: 'table' });
      const tb = el('tbody');
      const rows = [
        ['find', t.find],
        ['grep', t.grep],
        ['sed', t.sed],
        ['tail', t.tail],
        ['unzip', t.unzip],
        ['ssh', t.ssh],
        ['find 支持 -printf', t.find_supports_printf
          ? { html: '<span style="display:inline-flex;align-items:center;gap:4px;color:#22c55e;">' + svgIcon('smCheck', 14) + '</span>' }
          : { html: '<span style="display:inline-flex;align-items:center;gap:4px;color:#ef4444;">' + svgIcon('smX', 14) + '（AIX / 老 BSD）</span>' }
        ]
      ];
      rows.forEach(r => {
        const tc = r[1];
        if (typeof tc === 'object' && tc !== null && tc.nodeType === undefined && !tc.html) {
          tb.appendChild(el('tr', null, [
            el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
            el('td', tc.found
              ? { text: tc.path + (tc.version ? ' · ' + tc.version : '') }
              : { unsafeHtml: '<span style="display:inline-flex;align-items:center;gap:4px;color:#ef4444;">' + svgIcon('smX', 14) + ' 未找到</span>' }
            )
          ]));
        } else if (typeof tc === 'object' && tc.html) {
          tb.appendChild(el('tr', null, [
            el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
            el('td', { unsafeHtml: tc.html })
          ]));
        } else {
          tb.appendChild(el('tr', null, [
            el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
            el('td', { text: String(r[1]) })
          ]));
        }
      });
      tbl.appendChild(tb);
      return tbl;
    }
    function serversTable(arr) {
      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { text: '系统 / 服务器' }),
        el('th', { text: '目标' }),
        el('th', { text: 'DNS' }),
        el('th', { text: 'TCP' }),
        el('th', { text: '连接方案' }),
        el('th', { text: '耗时' }),
        el('th', { text: '详情' })
      ]));
      tbl.appendChild(thead);
      const tb = el('tbody');
      arr.forEach(s => {
        const dnsOk = s.dns && s.dns.ok;
        const tcpOk = s.tcp && s.tcp.ok;
        const dnsHtml = '<span style="display:inline-flex;align-items:center;gap:4px;color:' + (dnsOk ? '#22c55e' : '#ef4444') + ';">' + svgIcon(dnsOk ? 'smCheck' : 'smX', 14) + '</span>';
        const tcpHtml = '<span style="display:inline-flex;align-items:center;gap:4px;color:' + (tcpOk ? '#22c55e' : '#ef4444') + ';">' + svgIcon(tcpOk ? 'smCheck' : 'smX', 14) + '</span>';
        const detail = [];
        if (s.dns && s.dns.message) detail.push('DNS: ' + s.dns.message);
        if (s.tcp && s.tcp.message) detail.push('TCP: ' + s.tcp.message);
        if (s.error) detail.push(s.error);
        tb.appendChild(el('tr', null, [
          el('td', { text: s.system + ' · ' + s.server }),
          el('td', { class: 'mono', text: s.host + ':' + s.port }),
          el('td', { unsafeHtml: dnsHtml }),
          el('td', { unsafeHtml: tcpHtml }),
          el('td', { class: 'mono muted', text: s.profile || '自动探测' }),
          el('td', { class: 'num muted', text: s.elapsed_ms + ' ms' }),
          el('td', { class: 'muted small', text: detail.join(' · ') || '-' })
        ]));
      });
      tbl.appendChild(tb);
      return tbl;
    }

    load(false);
  }

  Kairo.pages.diagnostics = renderDiagnostics;
  Kairo.state.routes.diagnostics = renderDiagnostics;
  Kairo.state.routeNames.diagnostics = '环境自检';
  Kairo.state.routeSubs.diagnostics = '本机 / 网络 / 配置 / 工具 / 每台 server 连通性快速体检';
})();