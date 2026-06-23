/* ===== web/pages/diagnostics.js =====
 * 环境自检 — 调用 /api/diagnostics
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, kvTable, toast } = OTB.core;
  const { api } = OTB.api;

  function renderDiagnostics(view) {
    const summaryEl = el('div', { class: 'text-dim', text: '加载中…' });
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
          el('div', {}, r.issues.map(t => el('div', { class: 'text-warn', text: t })))
        ]));
      } else {
        issuesCard.appendChild(el('div', { class: 'card' }, [
          el('h3', { text: '需要关注' }),
          el('div', { class: 'text-dim', text: '一切正常 ✓' })
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

      toolsWrap.appendChild(el('div', { class: 'card' }, [
        el('h3', { text: '本机命令工具' }),
        toolsTable(r.tools)
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
      return b ? '✓ 是' : '✗ 否';
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
        ['find 支持 -printf', t.find_supports_printf ? '✓' : '✗（AIX / 老 BSD）']
      ];
      rows.forEach(r => {
        const tc = r[1];
        if (typeof tc === 'object' && tc !== null) {
          tb.appendChild(el('tr', null, [
            el('td', { style: 'width: 220px; color: var(--text-dim)', text: r[0] }),
            el('td', { text: tc.found ? (tc.path + (tc.version ? ' · ' + tc.version : '')) : '✗ 未找到' })
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
        el('th', { text: '耗时' }),
        el('th', { text: '详情' })
      ]));
      tbl.appendChild(thead);
      const tb = el('tbody');
      arr.forEach(s => {
        const dnsDot = s.dns && s.dns.ok ? '✓' : '✗';
        const tcpDot = s.tcp && s.tcp.ok ? '✓' : '✗';
        const detail = [];
        if (s.dns && s.dns.message) detail.push('DNS: ' + s.dns.message);
        if (s.tcp && s.tcp.message) detail.push('TCP: ' + s.tcp.message);
        if (s.error) detail.push(s.error);
        tb.appendChild(el('tr', null, [
          el('td', { text: s.system + ' · ' + s.server }),
          el('td', { class: 'mono', text: s.host + ':' + s.port }),
          el('td', { text: dnsDot }),
          el('td', { text: tcpDot }),
          el('td', { class: 'num muted', text: s.elapsed_ms + ' ms' }),
          el('td', { class: 'muted small', text: detail.join(' · ') || '-' })
        ]));
      });
      tbl.appendChild(tb);
      return tbl;
    }

    load(false);
  }

  OTB.pages.diagnostics = renderDiagnostics;
  OTB.state.routes.diagnostics = renderDiagnostics;
  OTB.state.routeNames.diagnostics = '环境自检';
  OTB.state.routeSubs.diagnostics = '本机 / 网络 / 配置 / 工具 / 每台 server 连通性快速体检';
})();