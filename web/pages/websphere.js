/* ===== web/pages/websphere.js =====
 * WebSphere 日志助手 —— 多服务器并行版
 *
 * 4 块：
 *   1. 顶部表单（系统 / 服务器 / 凭据 / 测试 / 列表 / 下载最新 N）
 *   2. 多服务器并行搜索（含时间窗口）
 *   3. 实时 tail（单文件 SSE 流）
 *   4. 文件列表 / 搜索结果 / 上下文
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, $, toast, setStatus, cssEscape, pctText, formatBytes, formatTime, trimMiddle, looksMojibake, basenameOf } = OTB.core;
  const { api } = OTB.api;

  function renderWebsphere(view) {
    let cfg = null;
    let listState = { files: [], serverName: '', dlId: null, dlEvtSrc: null, fileStates: {} };
    const srvStatus = {};

    const sysSel = el('select', { id: 'ws-sys' });
    const dirSel = el('select', { id: 'ws-dir' });
    const userInp = el('input', { type: 'text', id: 'ws-user', placeholder: 'SSH 用户名（可留空，使用配置默认）' });
    const passInp = el('input', { type: 'password', id: 'ws-pass', placeholder: 'SSH 密码' });
    const queryInp = el('input', { type: 'text', id: 'ws-query', placeholder: '例: Exception && userinfo   或   !DEBUG', value: 'Exception' });
    const filesNSel = el('select', { id: 'ws-files-n' });
    [1, 3, 5, 10].forEach(n => {
      const o = el('option', { value: String(n), text: '最近 ' + n + '个文件' });
      if (n === 3) o.selected = true;
      filesNSel.appendChild(o);
    });
    const concSel = el('select', { id: 'ws-conc' });
    [1, 2, 4, 8, 16].forEach(n => {
      const o = el('option', { value: String(n), text: '并发 ' + n });
      if (n === 8) o.selected = true;
      concSel.appendChild(o);
    });

    // 时间窗口（项 C1：默认 "全部"，可选 10min / 1h / today / 自定义）
    const timeSel = el('select', { id: 'ws-time' });
    [
      ['', '全部时间'],
      ['10m', '最近 10 分钟'],
      ['1h', '最近 1 小时'],
      ['today', '今天'],
      ['custom', '自定义…']
    ].forEach(([v, t]) => timeSel.appendChild(el('option', { value: v, text: t })));
    const timeFromInp = el('input', { type: 'datetime-local', id: 'ws-time-from', style: 'display:none' });
    const timeToInp = el('input', { type: 'datetime-local', id: 'ws-time-to', style: 'display:none' });

    function updateTimeCustomVisibility() {
      const isCustom = timeSel.value === 'custom';
      timeFromInp.style.display = isCustom ? '' : 'none';
      timeToInp.style.display = isCustom ? '' : 'none';
    }
    timeSel.addEventListener('change', updateTimeCustomVisibility);
    updateTimeCustomVisibility();

    // 构造时间范围参数（透传给 /api/logs/search/multi）
    function buildTimeRange() {
      if (!timeSel.value) return {};
      if (timeSel.value === 'custom') {
        const from = timeFromInp.value ? new Date(timeFromInp.value).toISOString() : '';
        const to = timeToInp.value ? new Date(timeToInp.value).toISOString() : '';
        return Object.assign({}, from && { since: from }, to && { until: to });
      }
      const now = Date.now();
      let sinceMs = null;
      if (timeSel.value === '10m') sinceMs = now - 10 * 60 * 1000;
      else if (timeSel.value === '1h') sinceMs = now - 60 * 60 * 1000;
      else if (timeSel.value === 'today') {
        const d = new Date();
        d.setHours(0, 0, 0, 0);
        sinceMs = d.getTime();
      }
      return sinceMs ? { since: new Date(sinceMs).toISOString() } : {};
    }

    const dlNSel = el('select', { id: 'ws-dl-n' });
    [1, 2, 3, 4, 5].forEach(n => {
      const o = el('option', { value: String(n), text: '最近 ' + n + ' 个文件' });
      if (n === 3) o.selected = true;
      dlNSel.appendChild(o);
    });
    const dlZipChk = el('input', { type: 'checkbox', id: 'ws-dl-zip' });
    const dlZipLabel = el('label', { class: 'inline' }, [dlZipChk, document.createTextNode('打包为 zip')]);

    // --- 服务器多选面板 ---
    const srvPickWrap = el('div', { class: 'srv-pick' });
    const srvPickHint = el('div', { class: 'text-dim', text: '先选择系统' });
    srvPickWrap.appendChild(srvPickHint);
    const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllSrv(true) });
    const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => toggleAllSrv(false) });
    const btnPickOnline = el('button', { class: 'btn btn-sm', text: '只选可用的', onclick: () => toggleOnline() });
    const srvPickToolbar = el('div', { class: 'srv-pick-toolbar' }, [
      document.createTextNode('目标服务器:'), btnPickAll, btnPickNone, btnPickOnline
    ]);
    const dlTargetSel = el('select', { style: 'display:none' });

    function getCheckedServers() {
      const out = [];
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        if (cb.checked) out.push(cb.getAttribute('data-srv'));
      });
      return out;
    }
    function toggleAllSrv(on) {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => { cb.checked = on; });
      refreshCredStatus();
    }
    function toggleOnline() {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        cb.checked = srvStatus[cb.getAttribute('data-srv')] && srvStatus[cb.getAttribute('data-srv')].state === 'ok';
      });
      refreshCredStatus();
    }
    function renderSrvPick() {
      const prevChecked = getCheckedServers();
      srvPickWrap.innerHTML = '';
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      if (!sys || !sys.servers || !sys.servers.length) {
        srvPickWrap.appendChild(el('div', { class: 'text-dim', text: '该业务系统下没有服务器。' }));
        return;
      }
      sys.servers.forEach(s => {
        const st = srvStatus[s.name] || { state: 'idle' };
        const dotCls = 'dot dot-' + (st.state === 'idle' ? 'idle' : st.state);
        const cb = el('input', { type: 'checkbox', 'data-srv': s.name, value: s.name });
        if (prevChecked.indexOf(s.name) !== -1 || st.state === 'ok') {
          cb.checked = true;
        }
        cb.addEventListener('change', refreshCredStatus);
        const item = el('label', { class: 'srv-pick-item' }, [
          cb,
          el('span', { class: 'name', text: s.name }),
          el('span', { class: 'host', text: s.host + ':' + s.port }),
          el('span', { class: 'status' }, [
            el('span', { class: dotCls }),
            document.createTextNode(' ' + (st.state === 'ok' ? '已测通' : st.state === 'fail' ? '连接失败' : st.state === 'busy' ? '测试中' : '未测'))
          ])
        ]);
        srvPickWrap.appendChild(item);
      });
    }
    function refreshDirs() {
      dirSel.innerHTML = '';
      dlTargetSel.innerHTML = '';
      const sysName = sysSel.value;
      const srvs = getCheckedServers();
      const srvName = srvs[0] || (cfg && cfg.systems.find(s => s.name === sysName) && cfg.systems.find(s => s.name === sysName).servers[0] && cfg.systems.find(s => s.name === sysName).servers[0].name);
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      const srv = sys && sys.servers.find(s => s.name === srvName);
      (srv ? srv.log_dirs : []).forEach(d => {
        dirSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
        dlTargetSel.appendChild(el('option', { value: d.path, text: (d.name || d.path) + '  ·  ' + d.path }));
      });
    }
    sysSel.addEventListener('change', () => { renderSrvPick(); refreshDirs(); refreshCredStatus(); });
    userInp.addEventListener('change', refreshCredStatus);
    userInp.addEventListener('blur', refreshCredStatus);

    const btnTest = el('button', { class: 'btn', text: '测试连接', onclick: doTest });
    const btnList = el('button', { class: 'btn btn-primary', text: '列出文件', onclick: doList });
    const btnDownload = el('button', { class: 'btn', text: '下载', onclick: doDownload });
    const btnSearch = el('button', { class: 'btn btn-primary', text: '搜索', onclick: doSearch });

    const fileTableWrap = el('div', { class: 'card', style: 'display:none' });
    const hitTableWrap = el('div', { class: 'card', style: 'display:none' });
    const ctxCard = el('div', { class: 'card', style: 'display:none' });

    function credsOne(srvName) {
      return { system: sysSel.value, server: srvName, dir: dirSel.value,
               username: userInp.value, password: passInp.value };
    }
    function credsMulti() {
      return { system: sysSel.value, dir: dirSel.value,
               servers: getCheckedServers(),
               username: userInp.value, password: passInp.value };
    }

    // ---- 凭据保存（OS 钥匙串）----
    const rememberChk = el('input', { type: 'checkbox', id: 'ws-remember' });
    const rememberLbl = el('label', { class: 'inline' }, [rememberChk, document.createTextNode('记住密码（存进系统钥匙串）')]);
    const credStatus = el('div', { class: 'text-dim mt-1', id: 'ws-cred-status', text: '未保存密码' });
    const btnForget = el('button', { class: 'btn btn-sm', text: '忘记', onclick: doForget, style: 'display:none' });
    const credStatusRow = el('div', { class: 'text-dim mt-1', style: 'display:flex; gap:8px; align-items:center;' }, [
      credStatus, btnForget
    ]);

    function currentCredKey() {
      const srvs = getCheckedServers();
      const srv = srvs[0] || (sysSel.value && cfg ? (cfg.systems.find(s => s.name === sysSel.value) || {}).servers?.[0]?.name : '');
      return { system: sysSel.value, server: srv, username: userInp.value };
    }

    async function refreshCredStatus() {
      const k = currentCredKey();
      if (!k.system || !k.server || !k.username) {
        credStatus.textContent = '未保存密码';
        btnForget.style.display = 'none';
        return;
      }
      try {
        const r = await api('GET', '/api/credentials/has?system=' + encodeURIComponent(k.system)
          + '&server=' + encodeURIComponent(k.server)
          + '&username=' + encodeURIComponent(k.username));
        if (!r.ok) return;
        const mode = r.mode || 'keyring';
        const storeDisabled = (mode === 'disabled') || (mode === 'file');
        if (storeDisabled) {
          rememberChk.checked = false;
          rememberChk.disabled = true;
          rememberLbl.style.display = 'none';
          credStatus.textContent = r.reason || ('凭据存储=' + mode);
          credStatus.style.color = '#999';
          btnForget.style.display = 'none';
          return;
        }
        rememberChk.disabled = false;
        rememberLbl.style.display = '';
        if (!r.available) {
          credStatus.textContent = '⚠ 系统钥匙串不可用 — 当前无法「记住密码」';
          credStatus.style.color = '#c00';
          btnForget.style.display = 'none';
        } else if (r.has) {
          credStatus.textContent = '✓ 已为 ' + k.username + '@' + k.server + ' 保存密码（无需再次输入）';
          credStatus.style.color = '';
          btnForget.style.display = '';
        } else {
          credStatus.textContent = '未保存密码';
          credStatus.style.color = '';
          btnForget.style.display = 'none';
        }
      } catch (e) { /* ignore */ }
    }

    async function doForget() {
      const k = currentCredKey();
      if (!k.system || !k.server || !k.username) return;
      try {
        await api('POST', '/api/credentials/clear', { system: k.system, server: k.server, username: k.username });
        toast('已忘记 ' + k.server + ' 上的密码', 'ok');
        await refreshCredStatus();
      } catch (e) {
        toast('清除失败: ' + e.message, 'err');
      }
    }

    async function maybeSaveCred(srvName) {
      if (!rememberChk.checked) return;
      const pw = passInp.value;
      if (!pw) return;
      const k = currentCredKey();
      const target = srvName || k.server;
      if (!k.system || !target || !k.username) return;
      try {
        await api('POST', '/api/credentials/save', {
          system: k.system, server: target, username: k.username, password: pw
        });
        await refreshCredStatus();
      } catch (e) {
        console.warn('save credential failed:', e);
      }
    }

    async function doTest() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要测试的服务器', 'warn'); return; }
      srvs.forEach(n => { srvStatus[n] = { state: 'busy' }; });
      renderSrvPick();
      await Promise.all(srvs.map(async (n) => {
        try {
          await api('POST', '/api/ssh/test', credsOne(n));
          srvStatus[n] = { state: 'ok' };
          await maybeSaveCred(n);
        } catch (e) {
          srvStatus[n] = { state: 'fail', err: e.message };
        } finally {
          renderSrvPick();
        }
      }));
      const okN = srvs.filter(n => srvStatus[n].state === 'ok').length;
      toast(okN + '/' + srvs.length + ' 台连接成功', okN === srvs.length ? 'ok' : 'warn');
      refreshCredStatus();
    }

    async function doList() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选服务器', 'warn'); return; }
      const n = srvs[0];
      try {
        const r = await api('POST', '/api/logs/list', credsOne(n));
        listState.files = r.files || [];
        listState.serverName = n;
        renderFileTable();
        fileTableWrap.style.display = '';
        toast('[' + n + '] 共 ' + listState.files.length + ' 个文件', 'ok');
        await maybeSaveCred(n);
      } catch (e) { toast('列文件失败：' + e.message, 'err'); }
    }

    function renderFileTable() {
      fileTableWrap.innerHTML = '';
      fileTableWrap.appendChild(el('h3', { text: '文件列表 · ' + (listState.serverName || '') + '（按修改时间倒序）' }));
      if (!listState.files.length) {
        fileTableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无文件，先点击"列出文件"。' }));
        return;
      }

      const btnPickAll = el('button', { class: 'btn btn-sm', text: '全选', onclick: () => pickAll(true) });
      const btnPickNone = el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => pickAll(false) });
      const btnPickTop = el('button', { class: 'btn btn-sm', text: '选前 ' + (dlNSel.value) + ' 个', onclick: () => pickTop(Number(dlNSel.value) || 1) });
      const btnDownloadSel = el('button', { class: 'btn btn-primary', text: '下载选中', onclick: doDownloadSelected });
      const btnCancel = el('button', { class: 'btn', text: '停止', onclick: doCancelDownload, disabled: true });
      const summary = el('div', { class: 'text-dim', text: '已选 0 个' });
      const toolbar = el('div', { class: 'file-toolbar' }, [
        btnPickAll, btnPickNone, btnPickTop, btnDownloadSel, btnCancel, summary
      ]);
      fileTableWrap.appendChild(toolbar);

      const tbl = el('table', { class: 'table' });
      const thead = el('thead', null, el('tr', null, [
        el('th', { class: 'col-check' }, [el('input', { type: 'checkbox', id: 'ws-file-checkall', onchange: (e) => pickAll(e.target.checked) })]),
        el('th', { text: '文件名' }),
        el('th', { text: '大小' }),
        el('th', { text: '修改时间' }),
        el('th', { text: '路径' }),
        el('th', { class: 'col-status', text: '状态' })
      ]));
      tbl.appendChild(thead);
      const tbody = el('tbody');
      listState.files.forEach(f => {
        const row = el('tr', { 'data-file': f.name });
        const cb = el('input', { type: 'checkbox', 'data-file': f.name, onchange: refreshSummary });
        const statusCell = el('td', { class: 'col-status', 'data-status': f.name });
        row.appendChild(el('td', { class: 'col-check' }, [cb]));
        row.appendChild(el('td', null, f.name));
        row.appendChild(el('td', { class: 'num', text: formatBytes(f.size) }));
        row.appendChild(el('td', { class: 'muted', text: formatTime(f.mod_time) }));
        row.appendChild(el('td', { class: 'muted', text: f.full_path }));
        row.appendChild(statusCell);
        tbody.appendChild(row);
      });
      tbl.appendChild(tbody);
      fileTableWrap.appendChild(tbl);

      fileTableWrap._toolbar = { summary, btnDownloadSel, btnCancel, btnPickAll, btnPickNone, btnPickTop };
      refreshSummary();
      listState.fileStates = {};
    }

    function pickAll(on) {
      fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]').forEach(cb => { cb.checked = on; });
      refreshSummary();
    }
    function pickTop(n) {
      const cbs = fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]');
      cbs.forEach((cb, i) => { cb.checked = i < n; });
      refreshSummary();
    }
    function getSelectedFiles() {
      const out = [];
      fileTableWrap.querySelectorAll('input[type="checkbox"][data-file]').forEach(cb => {
        if (cb.checked) out.push(cb.getAttribute('data-file'));
      });
      return out;
    }
    function refreshSummary() {
      const tb = fileTableWrap._toolbar;
      if (!tb) return;
      const sel = getSelectedFiles();
      tb.summary.textContent = '已选 ' + sel.length + ' / ' + listState.files.length + ' 个';
      const checkAll = fileTableWrap.querySelector('#ws-file-checkall');
      if (checkAll) {
        checkAll.checked = listState.files.length > 0 && sel.length === listState.files.length;
        checkAll.indeterminate = sel.length > 0 && sel.length < listState.files.length;
      }
    }

    function setRowStatus(name, status, args, rowClass) {
      const cell = fileTableWrap.querySelector('[data-status="' + cssEscape(name) + '"]');
      if (cell) {
        while (cell.firstChild) cell.removeChild(cell.firstChild);
        const node = buildStatusNode(status, args || {});
        if (node) cell.appendChild(node);
      }
      const row = fileTableWrap.querySelector('tr[data-file="' + cssEscape(name) + '"]');
      if (row && rowClass) {
        row.classList.remove('row-done', 'row-fail', 'row-active');
        row.classList.add(rowClass);
      }
    }

    function buildStatusNode(status, args) {
      switch (status) {
        case 'pending':
          return el('span', { class: 'dl-pct', text: '等待…' });
        case 'downloading': {
          const bar = el('div', { class: 'dl-bar' });
          const totalKnown = args.total && args.total > 0;
          const pct = totalKnown ? Math.min(100, ((args.written || 0) / args.total) * 100) : 0;
          const fill = el('div', {
            class: 'dl-bar-fill' + (totalKnown ? '' : ' indeterminate'),
            style: totalKnown ? ('width:' + pct + '%') : ''
          });
          bar.appendChild(fill);
          const txt = el('span', { class: 'dl-pct', text: pctText(args.written || 0, args.total || -1) });
          const wrap = el('span');
          wrap.appendChild(bar);
          wrap.appendChild(document.createTextNode(' '));
          wrap.appendChild(txt);
          return wrap;
        }
        case 'done': {
          const span = el('span', { class: 'dl-pct', style: 'color:#10b981' });
          span.appendChild(document.createTextNode('✓ 完成 · ' + formatBytes(args.bytes || 0)));
          return span;
        }
        case 'fail': {
          const span = el('span', { class: 'dl-pct', style: 'color:#ef4444' });
          span.appendChild(document.createTextNode('✗ ' + (args.error || '失败')));
          return span;
        }
        case 'startfail':
          return el('span', { class: 'dl-pct', style: 'color:#ef4444', text: '启动失败' });
        case 'cancel':
          return el('span', { class: 'dl-pct', style: 'color:#999', text: '已停止' });
        default:
          return el('span', { class: 'dl-pct', text: String(status) });
      }
    }

    async function doDownloadSelected() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选服务器', 'warn'); return; }
      const files = getSelectedFiles();
      if (!files.length) { toast('请先勾选要下载的文件', 'warn'); return; }
      if (listState.dlId) { toast('已有下载任务在进行中', 'warn'); return; }
      const srvName = srvs[0];
      const paths = files.map(name => {
        const ent = listState.files.find(f => f.name === name);
        return (ent && ent.full_path) ? ent.full_path : (dirSel.value + '/' + name);
      });
      const wantZip = dlZipChk.checked;
      const zip = wantZip && files.length >= 2;
      if (wantZip && files.length < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
      }
      listState.fileStates = {};
      files.forEach(name => {
        listState.fileStates[name] = { status: 'pending' };
        setRowStatus(name, 'pending');
      });
      const tb = fileTableWrap._toolbar;
      if (tb) { tb.btnDownloadSel.disabled = true; tb.btnCancel.disabled = false; }
      setStatus('busy', '下载中…');
      let dlId;
      try {
        const r = await api('POST', '/api/files/download', Object.assign({}, credsOne(srvName), {
          paths: paths, zip: zip
        }));
        dlId = r.id;
        listState.dlId = dlId;
        if (!window.EventSource) { toast('浏览器不支持 EventSource', 'err'); return; }
        const es = new EventSource('/api/files/download/' + dlId + '/events');
        listState.dlEvtSrc = es;
        OTB.core.setActiveDL({ id: dlId, evtsrc: es });
        let gotDone = false;
        const onDoneSeen = (reason) => {
          if (gotDone) return;
          gotDone = true;
          closeDownloadStream(reason);
        };
        es.onmessage = (ev) => {
          let o; try { o = JSON.parse(ev.data); } catch (e) { return; }
          if (o && o.kind === 'done') {
            handleDownloadEvent(o, files);
            onDoneSeen('done');
            return;
          }
          handleDownloadEvent(o, files);
        };
        es.addEventListener('done', () => { onDoneSeen('done'); });
        es.onerror = () => {
          setTimeout(() => {
            if (listState.dlId === dlId && listState.dlEvtSrc === es && !gotDone) {
              onDoneSeen('error');
              toast('SSE 连接异常（已强制收尾）', 'err');
            }
          }, 2000);
        };
      } catch (e) {
        toast('启动下载失败：' + e.message, 'err');
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
        if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
        files.forEach(name => {
          setRowStatus(name, 'startfail', null, 'row-fail');
        });
        listState.dlId = null;
        listState.dlEvtSrc = null;
      }
    }

    function handleDownloadEvent(o, files) {
      const key = basenameOf(o.file);
      if (o.kind === 'file_start') {
        listState.fileStates[key] = { status: 'downloading', written: 0, total: o.total || -1 };
        setRowStatus(key, 'downloading', { written: 0, total: o.total || -1 }, 'row-active');
      } else if (o.kind === 'progress') {
        const st = listState.fileStates[key] || {};
        st.status = 'downloading'; st.written = o.written; st.total = o.total;
        listState.fileStates[key] = st;
        setRowStatus(key, 'downloading', { written: o.written, total: o.total }, 'row-active');
      } else if (o.kind === 'file_done') {
        listState.fileStates[key] = { status: 'done', bytes: o.bytes };
        setRowStatus(key, 'done', { bytes: o.bytes }, 'row-done');
      } else if (o.kind === 'done') {
        const tb = fileTableWrap._toolbar;
        if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
        listState.dlId = null;
        if (o.ok) {
          toast('下载完成：' + (o.downloads || []).length + ' 个产物', 'ok');
          renderDownloadResults([{ server: listState.serverName, downloads: o.downloads, folder: o.folder }]);
        } else {
          toast('下载失败：' + (o.error || '未知错误'), 'err');
          files.forEach(name => {
            const st = listState.fileStates[name];
            if (!st || st.status === 'pending' || st.status === 'downloading') {
              setRowStatus(name, 'fail', { error: o.error || '失败' }, 'row-fail');
            }
          });
        }
        setStatus('idle');
        listState.dlEvtSrc = null;
      }
    }

    function closeDownloadStream(reason) {
      if (listState.dlEvtSrc) {
        listState.dlEvtSrc.close();
        listState.dlEvtSrc = null;
      }
      OTB.core.clearActiveDL();
      const tb = fileTableWrap._toolbar;
      if (tb) { tb.btnDownloadSel.disabled = false; tb.btnCancel.disabled = true; }
      if (reason === 'cancel') {
        toast('已停止下载', 'warn');
      }
      setStatus('idle');
    }

    async function doCancelDownload() {
      if (!listState.dlId) return;
      try { await api('POST', '/api/files/download/' + listState.dlId + '/cancel', {}); }
      catch (e) { /* ignore */ }
      closeDownloadStream('cancel');
    }

    async function doDownload() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要下载的服务器（多台会逐个下）', 'warn'); return; }
      const latest = Number(dlNSel.value) || 1;
      const wantZip = dlZipChk.checked;
      let zip = wantZip;
      if (zip && latest < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
        zip = false;
      }
      const allResults = [];
      let firstErr = null;
      for (const srvName of srvs) {
        try {
          const r = await api('POST', '/api/logs/download-latest',
            Object.assign({}, credsOne(srvName), { latest: latest, zip: zip }));
          allResults.push({ server: srvName, downloads: r.downloads || [], folder: r.folder });
        } catch (e) {
          allResults.push({ server: srvName, error: e.message });
          if (!firstErr) firstErr = e.message;
        }
      }
      renderDownloadResults(allResults);
      const okN = allResults.filter(x => !x.error).length;
      toast((okN === srvs.length ? '下载完成：' : '部分失败：') + okN + '/' + srvs.length, okN === srvs.length ? 'ok' : 'warn');
      for (const r of allResults) {
        if (!r.error) await maybeSaveCred(r.server);
      }
      refreshCredStatus();
    }

    function renderDownloadResults(allResults) {
      const existing = $('#ws-dl-results');
      if (existing) existing.remove();
      const wrap = el('div', { id: 'ws-dl-results', class: 'card' }, [
        el('h3', { text: '下载结果' })
      ]);
      allResults.forEach(r => {
        const grp = el('div', { class: 'server-group ' + (r.error ? 'fail' : 'ok') });
        if (r.error) {
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'name', text: r.server }),
            el('span', { class: 'meta', text: '下载失败' })
          ]));
          grp.appendChild(el('div', { class: 'err-msg', text: r.error }));
        } else {
          const files = (r.downloads || []).filter(d => d.kind !== 'zip');
          const zips = (r.downloads || []).filter(d => d.kind === 'zip');
          grp.appendChild(el('div', { class: 'server-group-head' }, [
            el('span', { class: 'name', text: r.server }),
            el('span', { class: 'meta', text: files.length + ' 个文件 + ' + zips.length + ' 个 zip' })
          ]));
          const inner = el('div', { style: 'padding: 8px 12px; font-size: 12.5px;' });
          if (files.length) {
            const block = el('div');
            files.forEach(d => {
              const row = el('div');
              row.appendChild(document.createTextNode('· ' + d.file + ' → '));
              row.appendChild(el('a', {
                href: '/downloads/' + encodeURIComponent(d.local),
                text: d.local,
              }));
              row.appendChild(document.createTextNode('（' + formatBytes(d.bytes) + '）'));
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          if (zips.length) {
            const block = el('div', { class: 'mt-2' });
            zips.forEach(d => {
              const row = el('div');
              row.appendChild(document.createTextNode('· 📦 '));
              row.appendChild(el('a', {
                href: '/downloads/' + encodeURIComponent(d.local),
                text: d.local,
              }));
              row.appendChild(document.createTextNode('（' + formatBytes(d.bytes) + '）'));
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          grp.appendChild(inner);
        }
        wrap.appendChild(grp);
      });
      const folder = (allResults.find(r => r.folder) || {}).folder || '-';
      wrap.appendChild(el('div', { class: 'text-dim mt-2', text: '本地保存目录：' + folder }));
      fileTableWrap.parentNode.insertBefore(wrap, fileTableWrap.nextSibling);
    }

    async function doSearch() {
      const srvs = getCheckedServers();
      if (!srvs.length) { toast('请先勾选要搜索的服务器', 'warn'); return; }
      if (!queryInp.value.trim()) { toast('搜索表达式不能为空', 'warn'); return; }
      hitTableWrap.style.display = '';
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '并行搜索中…' }));
      const conc = Number(concSel.value) || 8;
      const timeRange = buildTimeRange();
      try {
        const body = Object.assign({}, credsMulti(), {
          query: queryInp.value,
          files: Number(filesNSel.value),
          max_concurrency: conc
        }, timeRange);
        const r = await api('POST', '/api/logs/search/multi', body);
        renderMultiResults(r);
        let toastMsg = '命中 ' + r.total_hits + ' 条，' + r.ok_count + '/' + srvs.length + ' 成功';
        if (timeRange.since || timeRange.until) {
          toastMsg += '（时间范围已应用）';
        }
        toast(toastMsg, r.fail_count > 0 ? 'warn' : 'ok');
        for (const srv of (r.servers || [])) {
          if (srv.ok) await maybeSaveCred(srv.server);
        }
        refreshCredStatus();
      } catch (e) {
        hitTableWrap.innerHTML = '';
        hitTableWrap.appendChild(el('div', { class: 'text-err', text: '搜索失败：' + e.message }));
        toast('搜索失败：' + e.message, 'err');
      }
    }

    function renderMultiResults(r) {
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '搜索结果 · ' + r.ok_count + '/' + r.servers.length + ' 成功，共 ' + r.total_hits + ' 条命中' }));
      hitTableWrap.appendChild(el('div', { class: 'text-dim mb-2', text: '并发 ' + r.max_concurrency + '，按服务器分组展示' }));

      if (!r.servers || !r.servers.length) {
        hitTableWrap.appendChild(el('div', { class: 'text-dim', text: '没有结果。' }));
        return;
      }

      r.servers.forEach(srv => {
        const grp = el('div', { class: 'server-group ' + (srv.ok ? 'ok' : 'fail') });
        const headChildren = [
          el('span', { class: 'dot dot-' + (srv.ok ? (srv.hits_count > 0 ? 'ok' : 'idle') : 'err') }),
          el('span', { class: 'name', text: srv.server + (srv.host ? '  ·  ' + srv.host : '') })
        ];
        if (srv.ok && srv.encoding) {
          headChildren.push(el('span', { class: 'tag tag-enc', title: '当前目录编码（影响中文显示）', text: srv.encoding }));
        }
        headChildren.push(el('span', { class: 'meta', text:
          (srv.ok
            ? (srv.hits_count + ' 命中 / ' + (srv.files || []).length + ' 文件 / ' + srv.elapsed_ms + 'ms')
            : '失败 / ' + srv.elapsed_ms + 'ms') }));
        const head = el('div', { class: 'server-group-head' }, headChildren);
        grp.appendChild(head);
        if (!srv.ok) {
          grp.appendChild(el('div', { class: 'err-msg', text: srv.error || '未知错误' }));
        } else if (srv.hits_count === 0) {
          grp.appendChild(el('div', { class: 'text-dim', style: 'padding: 8px 12px;', text: '本台无匹配' }));
        } else {
          const tbl = el('table', { class: 'table' });
          const thead = el('thead', null, el('tr', null, [
            el('th', { text: '文件' }),
            el('th', { text: '行号' }),
            el('th', { text: '内容' }),
            el('th', { text: '操作' })
          ]));
          tbl.appendChild(thead);
          const tbody = el('tbody');
          srv.hits.forEach(h => {
            const content = trimMiddle(h.content, 280);
            const cells = [
              el('td', { class: 'muted', text: h.file }),
              el('td', { class: 'num', text: h.line_no })
            ];
            const contentCell = el('td', { class: 'hit-line', text: content });
            if (looksMojibake(content)) {
              contentCell.appendChild(el('span', { class: 'tag tag-warn', title: '当前目录编码与文件实际编码不一致，中文可能错位。试试切换到「GBK」目录。', text: '⚠ 解码可能有误' }));
            }
            cells.push(contentCell);
            cells.push(el('td', { class: 'actions' }, [
              el('button', { class: 'btn btn-sm', text: '上下文', onclick: () => doContext(h) })
            ]));
            tbody.appendChild(el('tr', null, cells));
          });
          tbl.appendChild(tbody);
          grp.appendChild(tbl);
        }
        hitTableWrap.appendChild(grp);
      });
    }

    async function doContext(hit) {
      const body = Object.assign({}, credsOne(hit.server), {
        file: hit.file, line: hit.line_no,
        before: 30, after: 30
      });
      try {
        const r = await api('POST', '/api/logs/context', body);
        renderContext(r.lines || [], hit);
        ctxCard.style.display = '';
        ctxCard.scrollIntoView({ behavior: 'smooth' });
      } catch (e) { toast('上下文获取失败：' + e.message, 'err'); }
    }

    function renderContext(lines, hit) {
      ctxCard.innerHTML = '';
      ctxCard.appendChild(el('h3', { text: '上下文 · ' + hit.server + ' · ' + hit.file + ':' + hit.line_no }));
      const view = el('div', { class: 'context-view' });
      lines.forEach(l => {
        view.appendChild(el('div', { class: 'row' + (l.hit ? ' hit' : '') }, [
          el('div', { class: 'ln', text: l.line_no }),
          el('div', { class: 'ct', text: l.content })
        ]));
      });
      ctxCard.appendChild(view);
    }

    const formCard = el('div', { class: 'card' }, [
      el('h3', { text: 'WebSphere 日志助手 · 多服务器并行' }),
      el('div', { class: 'card-desc', text: '先选业务系统 → 勾选目标服务器（可全选/全不选/只选可用的）→ 输入凭据 → 测试 / 列出 / 搜索 / 下载。' }),
      el('div', { class: 'grid-2' }, [
        el('div', null, [el('label', { text: '业务系统' }), sysSel]),
        el('div', null, [el('label', { text: '日志目录（取自第一台勾选服务器）' }), dirSel])
      ]),
      el('div', { class: 'mt-2' }, [srvPickToolbar, srvPickWrap]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: 'SSH 用户名' }), userInp]),
        el('div', null, [el('label', { text: 'SSH 密码' }), passInp])
      ]),
      el('div', { class: 'mt-1' }, [rememberLbl, credStatusRow]),
      el('div', { class: 'btn-row mt-3' }, [btnTest, btnList]),
      el('div', { class: 'mt-3' }, [
        el('div', { class: 'text-dim mb-1', text: '下载最新日志（每台服务器分别下，可选 zip）' }),
        el('div', { class: 'btn-row' }, [
          dlNSel, dlZipLabel, btnDownload
        ])
      ])
    ]);

    const searchCard = el('div', { class: 'card' }, [
      el('h3', { text: '多服务器并行搜索' }),
      el('div', { class: 'card-desc', unsafeHtml: '语法：<span class="code-inline">A &amp;&amp; B</span>（同包含）、<span class="code-inline">A || B</span>（任一）、<span class="code-inline">!X</span>（排除）。结果按服务器分组。' }),
      el('div', { class: 'grid-3' }, [
        el('div', { style: 'grid-column: span 2' }, [el('label', { text: '搜索表达式' }), queryInp]),
        el('div', null, [el('label', { text: '并发' }), concSel])
      ]),
      el('div', { class: 'grid-3 mt-2' }, [
        el('div', null, [el('label', { text: '搜索范围（每台）' }), filesNSel]),
        el('div', null, [el('label', { text: '时间范围' }), timeSel]),
        el('div', { style: 'display:flex; gap:8px; align-items:flex-end;' }, [timeFromInp, timeToInp])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnSearch])
    ]);

    // ---- 实时 tail ----
    const tailFileInp = el('input', { type: 'text', id: 'ws-tail-file', placeholder: '文件名（例：SystemOut.log）', value: 'SystemOut.log' });
    const tailLinesInp = el('input', { type: 'number', id: 'ws-tail-lines', placeholder: '起始行数', value: '100' });
    const tailSrvSel = el('select', { id: 'ws-tail-srv' });
    const tailOut = el('pre', { id: 'ws-tail-out', class: 'tail-out' });
    const btnTailStart = el('button', { class: 'btn btn-primary', text: '开始跟踪', onclick: doTailStart });
    const btnTailStop = el('button', { class: 'btn', text: '停止', onclick: doTailStop, disabled: true });
    let tailEvtSrc = null;
    let tailId = null;

    function refreshTailServers() {
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      tailSrvSel.innerHTML = '';
      (sys ? sys.servers : []).forEach(s => {
        tailSrvSel.appendChild(el('option', { value: s.name, text: s.name + '  (' + s.host + ')' }));
      });
    }
    sysSel.addEventListener('change', refreshTailServers);

    async function doTailStart() {
      const file = (tailFileInp.value || '').trim();
      if (!file) { toast('请输入文件名', 'warn'); return; }
      const serverName = tailSrvSel.value;
      if (!serverName) { toast('请选择服务器', 'warn'); return; }
      if (!dirSel.value) { toast('请选择日志目录', 'warn'); return; }
      const lines = Math.max(0, Math.min(1000, Number(tailLinesInp.value) || 0));
      tailOut.textContent = '';
      setStatus('busy', '跟踪中…');
      try {
        const r = await api('POST', '/api/logs/tail/start', Object.assign({}, credsOne(serverName), {
          file: file, lines: lines,
        }));
        tailId = r.id;
        btnTailStart.disabled = true;
        btnTailStop.disabled = false;
        appendTailLine({ kind: 'info', msg: '已开启 tail，id=' + tailId });
        if (window.EventSource) {
          tailEvtSrc = new EventSource('/api/logs/tail/' + tailId + '/events');
          tailEvtSrc.onmessage = (ev) => {
            try { appendTailLine(JSON.parse(ev.data)); } catch (e) { appendTailLine({ kind: 'info', msg: ev.data }); }
          };
          tailEvtSrc.addEventListener('done', () => {
            appendTailLine({ kind: 'info', msg: 'SSE 通道关闭' });
            stopTailUI();
          });
          tailEvtSrc.onerror = () => {
            appendTailLine({ kind: 'error', msg: 'SSE 连接异常' });
          };
        } else {
          appendTailLine({ kind: 'error', msg: '浏览器不支持 EventSource' });
        }
      } catch (e) {
        toast('启动 tail 失败：' + e.message, 'err');
        setStatus('err', '失败');
        setTimeout(() => setStatus('idle'), 1500);
      }
    }

    async function doTailStop() {
      if (!tailId) return;
      try { await api('POST', '/api/logs/tail/' + tailId + '/stop'); }
      catch (e) { /* ignore */ }
      stopTailUI();
    }

    function stopTailUI() {
      if (tailEvtSrc) { tailEvtSrc.close(); tailEvtSrc = null; }
      tailId = null;
      btnTailStart.disabled = false;
      btnTailStop.disabled = true;
      setStatus('idle');
    }

    function appendTailLine(o) {
      if (o.kind === 'line') {
        tailOut.textContent += o.line + '\n';
      } else if (o.kind === 'info') {
        tailOut.textContent += '⟦info⟧ ' + o.msg + '\n';
      } else if (o.kind === 'error') {
        tailOut.textContent += '⟦error⟧ ' + o.msg + '\n';
      } else if (o.kind === 'done') {
        tailOut.textContent += '⟦done⟧ ' + o.msg + '\n';
      }
      tailOut.scrollTop = tailOut.scrollHeight;
      const MAX_LINES = 5000;
      const lines = tailOut.textContent.split('\n');
      if (lines.length > MAX_LINES) {
        tailOut.textContent = lines.slice(lines.length - MAX_LINES).join('\n');
      }
    }

    const tailCard = el('div', { class: 'card' }, [
      el('h3', { text: '实时 tail（单文件，SSE 流式）' }),
      el('div', { class: 'card-desc', text: '跟踪远程文件新增行（用 tail -F），关闭/刷新页面会自动停。' }),
      el('div', { class: 'grid-3' }, [
        el('div', null, [el('label', { text: '服务器' }), tailSrvSel]),
        el('div', null, [el('label', { text: '文件名' }), tailFileInp]),
        el('div', null, [el('label', { text: '起始行数（0=只追新增）' }), tailLinesInp])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnTailStart, btnTailStop]),
      el('div', { class: 'mt-2' }, tailOut)
    ]);

    view.appendChild(formCard);
    view.appendChild(searchCard);
    view.appendChild(tailCard);
    view.appendChild(fileTableWrap);
    view.appendChild(hitTableWrap);
    view.appendChild(ctxCard);

    api('GET', '/api/config').then(info => {
      cfg = info;
      sysSel.innerHTML = '';
      info.systems.forEach(s => sysSel.appendChild(el('option', { value: s.name, text: s.name })));
      renderSrvPick();
      refreshDirs();
      refreshCredStatus();
      refreshTailServers();
    }).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  OTB.pages.websphere = renderWebsphere;
  OTB.state.routes.websphere = renderWebsphere;
  OTB.state.routeNames.websphere = 'WebSphere 日志助手';
})();