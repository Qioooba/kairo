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

    // v0.5：每个勾选服务器下展开它的日志目录（二级勾选），用于 #10 多对多。
    const srvDirsWrap = el('div', { class: 'srv-dirs' });
    const srvDirsHint = el('div', { class: 'text-dim', text: '勾选服务器后会展开它的日志目录，可多选。' });
    srvDirsWrap.appendChild(srvDirsHint);
    const srvDirsToolbar = el('div', { class: 'srv-pick-toolbar' }, [
      document.createTextNode('目标日志目录（多对多勾选）:'),
      el('button', { class: 'btn btn-sm', text: '全选', onclick: () => toggleAllDirs(true) }),
      el('button', { class: 'btn btn-sm', text: '全不选', onclick: () => toggleAllDirs(false) })
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
      renderSrvDirs();
      persistSelection();
      refreshCredStatus();
    }
    function toggleOnline() {
      srvPickWrap.querySelectorAll('input[type="checkbox"][data-srv]').forEach(cb => {
        cb.checked = srvStatus[cb.getAttribute('data-srv')] && srvStatus[cb.getAttribute('data-srv')].state === 'ok';
      });
      renderSrvDirs();
      persistSelection();
      refreshCredStatus();
    }
    // 当前勾选的 (server, dir) targets 列表；用于多对多搜索/列文件。
    // 优先取二级勾选；二级都没勾时退回到 dirSel（一级单选）作为所有勾选服务器的目录。
    function getSelectedTargets() {
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      if (!sys) return [];
      const out = [];
      const explicitDirs = srvDirsWrap.querySelectorAll('input[type="checkbox"][data-srv][data-dir]:checked');
      if (explicitDirs.length > 0) {
        // 多对多：每条 (server, dir) 一项
        explicitDirs.forEach(cb => {
          out.push({ server: cb.getAttribute('data-srv'), dir: cb.getAttribute('data-dir') });
        });
        return out;
      }
      // 退回模式：每个勾选服务器用 dirSel 的目录
      const fallbackDir = dirSel.value;
      if (!fallbackDir) return [];
      getCheckedServers().forEach(srvName => {
        out.push({ server: srvName, dir: fallbackDir });
      });
      return out;
    }
    function toggleAllDirs(on) {
      srvDirsWrap.querySelectorAll('input[type="checkbox"][data-srv][data-dir]').forEach(cb => { cb.checked = on; });
      persistSelection();
    }
    // 把"系统 + 勾选服务器 + 目录（含二级）"存 localStorage，下次打开自动恢复
    function persistSelection() {
      if (!sysSel.value) return;
      const checked = getCheckedServers();
      const explicitDirs = srvDirsWrap.querySelectorAll('input[type="checkbox"][data-srv][data-dir]:checked');
      const dirs = [];
      explicitDirs.forEach(cb => dirs.push({ srv: cb.getAttribute('data-srv'), dir: cb.getAttribute('data-dir') }));
      OTB.core.lastSet('websphere', 'sel', {
        system: sysSel.value,
        servers: checked,
        dir: dirSel.value,
        dirs: dirs,
        username: userInp.value
      });
    }
    function renderSrvPick() {
      const prevChecked = getCheckedServers();
      // 第一次加载：尝试用上次记忆的服务器列表
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      const lastForSys = (lastSel && lastSel.system === sysSel.value && Array.isArray(lastSel.servers)) ? lastSel.servers : null;
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
        // v0.5 #11：默认勾选目标服务器
        //   - 有上次选择 → 恢复
        //   - 无上次 → 默认勾全部（用户开箱即用；取消勾也行）
        const wasChecked = prevChecked.indexOf(s.name) !== -1
          || (lastForSys && lastForSys.indexOf(s.name) !== -1)
          || (prevChecked.length === 0 && !lastForSys); // ← 无上次记忆时全选
        if (wasChecked) cb.checked = true;
        cb.addEventListener('change', () => { renderSrvDirs(); persistSelection(); refreshCredStatus(); });
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
    // renderSrvDirs v0.5：每个勾选服务器展开一个目录勾选区
    function renderSrvDirs() {
      srvDirsWrap.innerHTML = '';
      const sysName = sysSel.value;
      const sys = cfg && cfg.systems.find(s => s.name === sysName);
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      const lastDirs = (lastSel && lastSel.system === sysName && Array.isArray(lastSel.dirs)) ? lastSel.dirs : [];
      const checkedSrvs = getCheckedServers();
      if (!checkedSrvs.length) {
        srvDirsWrap.appendChild(el('div', { class: 'text-dim', text: '先在「目标服务器」里勾选至少一台。' }));
        return;
      }
      if (!sys || !sys.servers) return;
      checkedSrvs.forEach(srvName => {
        const srv = sys.servers.find(s => s.name === srvName);
        if (!srv) return;
        const block = el('div', { class: 'srv-dirs-block' });
        const head = el('div', { class: 'srv-dirs-head' }, [
          el('span', { class: 'name', text: srv.name }),
          el('span', { class: 'text-dim', text: ' · ' + (srv.log_dirs || []).length + ' 个目录' })
        ]);
        block.appendChild(head);
        const list = el('div', { class: 'srv-dirs-list' });
        (srv.log_dirs || []).forEach(d => {
          const cb = el('input', { type: 'checkbox', 'data-srv': srv.name, 'data-dir': d.path, value: d.path });
          // v0.5 #11：默认勾选日志目录
          //   - 有上次选择 → 恢复
          //   - 无上次 → 父级 server 勾选了 → 默认全勾这个 server 的目录
          //   - 无上次 + dirSel 命中 → 兜底勾上
          const dirHit = lastDirs.find(x => x.srv === srv.name && x.dir === d.path);
          const fallbackHit = (lastDirs.length === 0 && dirSel.value === d.path);
          const parentChecked = (lastDirs.length === 0 && wasChecked); // 同上：父级默认勾则目录默认全勾
          if (dirHit || fallbackHit || parentChecked) cb.checked = true;
          cb.addEventListener('change', persistSelection);
          const enc = (d.encoding || 'utf-8').toLowerCase();
          const item = el('label', { class: 'srv-dirs-item' }, [
            cb,
            el('span', { class: 'name', text: d.name || d.path }),
            el('span', { class: 'path', text: d.path }),
            el('span', { class: 'enc', text: enc })
          ]);
          list.appendChild(item);
        });
        if (!(srv.log_dirs || []).length) {
          list.appendChild(el('div', { class: 'text-dim', text: '（该服务器未配置日志目录）' }));
        }
        block.appendChild(list);
        srvDirsWrap.appendChild(block);
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
    sysSel.addEventListener('change', () => { renderSrvPick(); refreshDirs(); refreshCredStatus(); persistSelection(); });
    dirSel.addEventListener('change', persistSelection);
    userInp.addEventListener('input', () => { clearTimeout(userInp._t); userInp._t = setTimeout(persistSelection, 500); });
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
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      // 并行拉每台 (server, dir) 的文件列表，结果分组合并
      listState.groups = [];
      listState.files = [];
      listState.serverName = targets.length === 1 ? (targets[0].server + ' · ' + (targets[0].dir.split('/').pop() || targets[0].dir)) : (targets.length + ' 组');
      renderFileTable();
      fileTableWrap.style.display = '';
      const t0 = Date.now();
      // 简单并发（不抢搜索的并发槽）
      const conc = Math.min(8, targets.length);
      const sem = new Array(conc).fill(Promise.resolve());
      const promises = targets.map((tgt) => {
        return new Promise((resolve) => {
          const slot = sem.shift();
          sem.push(slot.then(() => {
            return api('POST', '/api/logs/list', {
              system: sysSel.value, server: tgt.server, dir: tgt.dir,
              username: userInp.value, password: passInp.value
            }).then(r => {
              const grp = { server: tgt.server, dir: tgt.dir, files: r.files || [], error: null };
              listState.groups.push(grp);
              resolve();
            }).catch(e => {
              listState.groups.push({ server: tgt.server, dir: tgt.dir, files: [], error: e.message });
              resolve();
            });
          }));
        });
      });
      await Promise.all(promises);
      renderFileTable();
      const dt = Date.now() - t0;
      const okN = listState.groups.filter(g => !g.error).length;
      const totalFiles = listState.groups.reduce((a, g) => a + g.files.length, 0);
      toast((okN === listState.groups.length ? '列出完成：' : '部分失败：') + totalFiles + ' 个文件 / ' + okN + '/' + listState.groups.length + ' 组 · ' + dt + 'ms', okN === listState.groups.length ? 'ok' : 'warn');
      // 保存第一组成功连接的密码
      const firstOk = listState.groups.find(g => !g.error);
      if (firstOk) await maybeSaveCred(firstOk.server);
    }

    function renderFileTable() {
      fileTableWrap.innerHTML = '';
      const groups = listState.groups || [];
      const totalFiles = groups.reduce((a, g) => a + g.files.length, 0);
      fileTableWrap.appendChild(el('h3', { text: '文件列表 · ' + (listState.serverName || '') + (groups.length ? '（' + totalFiles + ' 个文件 / ' + groups.length + ' 组）' : '') }));
      if (!groups.length && !listState.files.length) {
        fileTableWrap.appendChild(el('div', { class: 'text-dim', text: '暂无文件，先点击「列出文件」。' }));
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

      // 多组展示：每组一个 server-group（沿用搜索结果那套样式），组内是表格
      groups.forEach(g => {
        const grp = el('div', { class: 'server-group ' + (g.error ? 'fail' : 'ok') });
        grp.appendChild(el('div', { class: 'server-group-head' }, [
          el('span', { class: 'dot dot-' + (g.error ? 'err' : 'ok') }),
          el('span', { class: 'name', text: g.server }),
          el('span', { class: 'text-dim', text: ' · ' + g.dir }),
          el('span', { class: 'meta', text: g.error ? ('失败：' + g.error) : (g.files.length + ' 个文件') })
        ]));
        if (g.error) {
          grp.appendChild(el('div', { class: 'err-msg', text: g.error }));
          fileTableWrap.appendChild(grp);
          return;
        }
        const tbl = el('table', { class: 'table' });
        tbl.appendChild(el('thead', null, el('tr', null, [
          el('th', { class: 'col-check' }),
          el('th', { text: '文件名' }),
          el('th', { text: '大小' }),
          el('th', { text: '修改时间' }),
          el('th', { text: '路径' }),
          el('th', { class: 'col-status', text: '状态' })
        ])));
        const tbody = el('tbody');
        // 把 listState.files 也并入（旧调用兼容）
        const allFiles = g.files;
        allFiles.forEach(f => {
          const row = el('tr', { 'data-file': f.name, 'data-srv': g.server });
          const cb = el('input', { type: 'checkbox', 'data-file': f.name, 'data-srv': g.server, onchange: refreshSummary });
          const statusCell = el('td', { class: 'col-status', 'data-status': f.name });
          // v0.5 #14：每行加 Tail / 新窗口 Tail 按钮（不用手输文件名）
          const tailBtn = el('button', {
            class: 'btn btn-sm',
            text: '📺 内嵌 Tail',
            title: '在下方 tail 区域跟踪此文件',
            onclick: () => startTailForFile(g.server, g.dir, f.name)
          });
          const tailNewWinBtn = el('button', {
            class: 'btn btn-sm',
            text: '↗ 新窗口 Tail',
            title: '在新窗口中跟踪此文件（避免本页卡死）',
            onclick: () => openTailForFileInNewTab(g.server, g.dir, f.name)
          });
          row.appendChild(el('td', { class: 'col-check' }, [cb]));
          row.appendChild(el('td', null, f.name));
          row.appendChild(el('td', { class: 'num', text: formatBytes(f.size) }));
          row.appendChild(el('td', { class: 'muted', text: formatTime(f.mod_time) }));
          row.appendChild(el('td', { class: 'muted', text: f.full_path }));
          row.appendChild(el('td', null, [tailBtn, ' ', tailNewWinBtn]));
          row.appendChild(statusCell);
          tbody.appendChild(row);
        });
        tbl.appendChild(tbody);
        grp.appendChild(tbl);
        fileTableWrap.appendChild(grp);
      });

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
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      const latest = Number(dlNSel.value) || 1;
      const wantZip = dlZipChk.checked;
      let zip = wantZip;
      if (zip && latest < 2) {
        toast('zip 打包需要 ≥ 2 个文件，已仅返回原始文件', 'warn');
        zip = false;
      }
      const allResults = [];
      // 简单并发：每对 (server, dir) 一个请求，concurrency 上限 6
      const conc = 6;
      const queue = targets.slice();
      const runners = Array.from({ length: conc }, async () => {
        while (queue.length) {
          const tgt = queue.shift();
          if (!tgt) break;
          try {
            const r = await api('POST', '/api/logs/download-latest', {
              system: sysSel.value, server: tgt.server, dir: tgt.dir,
              username: userInp.value, password: passInp.value,
              latest: latest, zip: zip
            });
            allResults.push({ server: tgt.server, dir: tgt.dir, downloads: r.downloads || [], folder: r.folder });
          } catch (e) {
            allResults.push({ server: tgt.server, dir: tgt.dir, error: e.message });
          }
        }
      });
      await Promise.all(runners);
      renderDownloadResults(allResults);
      const okN = allResults.filter(x => !x.error).length;
      toast((okN === targets.length ? '下载完成：' : '部分失败：') + okN + '/' + targets.length, okN === targets.length ? 'ok' : 'warn');
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
              // v0.5-F P1-12：在文件管理器中显示 + 复制绝对路径
              if (d.abs_path) {
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📂 打开', style: 'margin-left:8px;',
                  title: '在 Finder/Explorer 中显示（' + d.abs_path + '）',
                  onclick: () => revealLocal(d.abs_path)
                }));
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📋', style: 'margin-left:4px;',
                  title: '复制绝对路径：' + d.abs_path,
                  onclick: () => copyToClipboard(d.abs_path)
                }));
              }
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
              if (d.abs_path) {
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📂 打开', style: 'margin-left:8px;',
                  title: '在 Finder/Explorer 中显示（' + d.abs_path + '）',
                  onclick: () => revealLocal(d.abs_path)
                }));
                row.appendChild(el('button', {
                  class: 'btn btn-sm', text: '📋', style: 'margin-left:4px;',
                  title: '复制绝对路径：' + d.abs_path,
                  onclick: () => copyToClipboard(d.abs_path)
                }));
              }
              block.appendChild(row);
            });
            inner.appendChild(block);
          }
          grp.appendChild(inner);
        }
        wrap.appendChild(grp);
      });
      // v0.5-F P1-12：底部明确显示保存目录 + 一键打开 + 复制
      const folder = (allResults.find(r => r.folder) || {}).folder || '-';
      const folderRow = el('div', {
        class: 'text-dim mt-2',
        style: 'display:flex; gap:8px; align-items:center; flex-wrap:wrap;'
      }, [
        el('span', null, [document.createTextNode('本地保存目录：' + folder)])
      ]);
      if (folder && folder !== '-') {
        folderRow.appendChild(el('button', {
          class: 'btn btn-sm', text: '📁 打开目录',
          title: '在 Finder/Explorer 中打开目录',
          onclick: () => openLocalFolder(folder)
        }));
        folderRow.appendChild(el('button', {
          class: 'btn btn-sm', text: '📋 复制路径',
          title: '复制目录绝对路径到剪贴板',
          onclick: () => copyToClipboard(folder)
        }));
      }
      wrap.appendChild(folderRow);
      fileTableWrap.parentNode.insertBefore(wrap, fileTableWrap.nextSibling);
    }

    // v0.5-F P1-12：调后端 reveal / open folder 接口
    async function revealLocal(absPath) {
      try {
        await api('POST', '/api/local/reveal-file', { path: absPath });
      } catch (e) {
        toast('打开失败：' + e.message, 'err');
      }
    }
    async function openLocalFolder(absDir) {
      try {
        await api('POST', '/api/local/open-folder', { path: absDir });
      } catch (e) {
        toast('打开目录失败：' + e.message, 'err');
      }
    }
    // 复制文本到剪贴板（fallback：旧浏览器走 prompt）
    function copyToClipboard(text) {
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(
          () => toast('已复制：' + text, 'ok'),
          () => fallbackCopy(text)
        );
      } else {
        fallbackCopy(text);
      }
    }
    function fallbackCopy(text) {
      try {
        const ta = document.createElement('textarea');
        ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        toast('已复制：' + text, 'ok');
      } catch (e) {
        toast('复制失败：' + e.message, 'err');
      }
    }

    async function doSearch() {
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      if (!queryInp.value.trim()) { toast('搜索表达式不能为空', 'warn'); return; }
      hitTableWrap.style.display = '';
      hitTableWrap.innerHTML = '';
      hitTableWrap.appendChild(el('h3', { text: '并行搜索中…' }));
      const conc = Number(concSel.value) || 8;
      const timeRange = buildTimeRange();
      // v0.5 #8：文件名参数 — 单文件 / 多文件 / glob 模糊匹配
      const filePatternsRaw = (filePatternInp.value || '').trim();
      const filePatterns = filePatternsRaw
        ? filePatternsRaw.split(/[,\s\n]+/).map(s => s.trim()).filter(Boolean)
        : null;
      try {
        const qs = new URLSearchParams();
        if (timeRange.since) qs.set('since', timeRange.since);
        if (timeRange.until) qs.set('until', timeRange.until);
        const body = {
          system: sysSel.value,
          targets: targets,
          query: queryInp.value,
          files: Number(filesNSel.value),
          max_concurrency: conc,
          username: userInp.value,
          password: passInp.value
        };
        if (filePatterns) body.file_patterns = filePatterns;
        const url = '/api/logs/search/multi' + (qs.toString() ? '?' + qs : '');
        const r = await api('POST', url, body);
        renderMultiResults(r);
        let toastMsg = '命中 ' + r.total_hits + ' 条，' + r.ok_count + '/' + targets.length + ' 组成功';
        if (timeRange.since || timeRange.until) toastMsg += '（时间范围已应用）';
        if (filePatterns) toastMsg += '（文件名过滤：' + filePatterns.join(', ') + '）';
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
      el('div', { class: 'card-desc', text: '先选业务系统 → 勾选目标服务器（可全选/全不选/只选可用的）→ 在下面展开的日志目录里多选要操作的目录 → 输入凭据 → 测试 / 列出 / 搜索 / 下载。' }),
      el('div', { class: 'grid-2' }, [
        el('div', null, [el('label', { text: '业务系统' }), sysSel]),
        el('div', null, [el('label', { text: '默认目录（多目录勾选未选时回退到此）' }), dirSel])
      ]),
      el('div', { class: 'mt-2' }, [srvPickToolbar, srvPickWrap]),
      el('div', { class: 'mt-2' }, [srvDirsToolbar, srvDirsWrap]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: 'SSH 用户名' }), userInp]),
        el('div', null, [el('label', { text: 'SSH 密码' }), passInp])
      ]),
      el('div', { class: 'mt-1' }, [rememberLbl, credStatusRow]),
      el('div', { class: 'btn-row mt-3' }, [btnTest, btnList]),
      el('div', { class: 'mt-3' }, [
        el('div', { class: 'text-dim mb-1', text: '下载最新日志（每台服务器每个勾选目录分别下，可选 zip）' }),
        el('div', { class: 'btn-row' }, [
          dlNSel, dlZipLabel, btnDownload
        ])
      ])
    ]);

    // v0.5 #8：文件名参数 — 单文件 / 多文件 / glob 模糊匹配（空格或逗号分隔）
    // P1-08 改进：明确语义 — 填了 glob 后就只用 glob 匹配，N 仍控制"取最新 N 个匹配上的"
    const filePatternInp = el('input', { type: 'text', id: 'ws-file-pattern', placeholder: '可选 glob（逗号/空格分隔）：例 SystemOut*.log 或 *.log,*.txt' });

    const searchCard = el('div', { class: 'card' }, [
      el('h3', { text: '多服务器并行搜索' }),
      el('div', { class: 'card-desc', unsafeHtml: '语法：<span class="code-inline">A &amp;&amp; B</span>（同包含）、<span class="code-inline">A || B</span>（任一）、<span class="code-inline">!X</span>（排除）。结果按服务器 / 目录分组。' }),
      el('div', { class: 'grid-3' }, [
        el('div', { style: 'grid-column: span 2' }, [el('label', { text: '搜索表达式' }), queryInp]),
        el('div', null, [el('label', { text: '并发' }), concSel])
      ]),
      el('div', { class: 'grid-2 mt-2' }, [
        el('div', null, [el('label', { text: '最近文件数（每台服务器每个目录）' }), filesNSel]),
        el('div', null, [
          el('label', { text: '文件名 glob（留空用配置 patterns；填了只搜匹配的文件）' }),
          filePatternInp,
          el('div', { class: 'text-dim', style: 'font-size:11.5px; margin-top:2px;', text: '💡 填 glob 后，N 仍限制"取匹配文件中的最新 N 个"（不是只搜 1 个）' })
        ])
      ]),
      el('div', { class: 'grid-3 mt-2' }, [
        el('div', null, [el('label', { text: '时间范围' }), timeSel]),
        el('div', { style: 'display:flex; gap:8px; align-items:flex-end;' }, [timeFromInp, timeToInp]),
        el('div', null, [el('div', { class: 'text-dim', style: 'padding-top:18px;', text: '搜索作用于上方勾选的「服务器 × 目录」多对多 targets' })])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnSearch])
    ]);

    // ---- 实时 tail ----
    const tailFileInp = el('input', { type: 'text', id: 'ws-tail-file', placeholder: '文件名（例：SystemOut.log）', value: 'SystemOut.log' });
    const tailLinesInp = el('input', { type: 'number', id: 'ws-tail-lines', placeholder: '起始行数', value: '100' });
    const tailOut = el('pre', { id: 'ws-tail-out', class: 'tail-out' });
    const btnTailStart = el('button', { class: 'btn btn-primary', text: '开始跟踪', onclick: doTailStart });
    const btnTailStop = el('button', { class: 'btn', text: '停止', onclick: doTailStop, disabled: true });
    const btnTailNewTab = el('button', { class: 'btn', text: '↗ 新窗口打开', onclick: openTailInNewTab, title: '在新窗口中跟踪，避免本页卡死' });
    let tailEvtSrc = null;
    let tailId = null;

    async function doTailStart() {
      const file = (tailFileInp.value || '').trim();
      if (!file) { toast('请输入文件名', 'warn'); return; }
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      const serverName = targets[0].server;
      const dirPath = targets[0].dir;
      const lines = Math.max(0, Math.min(1000, Number(tailLinesInp.value) || 0));
      tailOut.textContent = '';
      pendingTailLines = [];
      tailTotalLines = 0;
      tailFlushScheduled = false;
      setStatus('busy', '跟踪中…');
      try {
        const r = await api('POST', '/api/logs/tail/start', {
          system: sysSel.value, server: serverName, dir: dirPath,
          username: userInp.value, password: passInp.value,
          file: file, lines: lines,
        });
        tailId = r.id;
        btnTailStart.disabled = true;
        btnTailStop.disabled = false;
        appendTailLine({ kind: 'info', msg: '已开启 tail · 服务器=' + serverName + ' · 目录=' + dirPath + ' · id=' + tailId });
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

    function openTailInNewTab() {
      const file = (tailFileInp.value || '').trim();
      if (!file) { toast('请输入文件名', 'warn'); return; }
      const targets = getSelectedTargets();
      if (!targets.length) { toast('请先勾选服务器 + 目录（多对多）', 'warn'); return; }
      openTailForFileInNewTab(targets[0].server, targets[0].dir, file);
    }

    // v0.5 #14：点文件列表里的文件 → 直接开新窗口 tail（无需手输文件名）
    function openTailForFileInNewTab(serverName, dirPath, fileName) {
      const params = new URLSearchParams({
        system: sysSel.value,
        server: serverName,
        dir: dirPath,
        file: fileName,
        lines: String(Math.max(0, Math.min(1000, Number(tailLinesInp.value) || 0)))
      });
      window.open('/static/tail.html?' + params.toString(), '_blank');
    }

    // v0.5 #14：点文件列表里的文件 → 在本页 tail 区域跟踪
    // 把 server/dir/file 推到 tail 输入区，调 doTailStart
    async function startTailForFile(serverName, dirPath, fileName) {
      // 同步选中状态：把"目标选择区"里这台 server+dir 的 checkbox 勾上
      // （保证 doTailStart 里 getSelectedTargets 能拿到对应目标）
      try {
        // 简化做法：直接拼请求 payload，不依赖全局 getSelectedTargets
        tailFileInp.value = fileName;
        // 直接构造 targets（[server, dir] 一项），绕过 UI 选择
        const targets = getSelectedTargets();
        const wantDir = dirPath;
        const wantSrv = serverName;
        if (!targets.find(t => t.server === wantSrv && t.dir === wantDir)) {
          toast('请先在「目标选择」里勾选 ' + wantSrv + ' / ' + wantDir, 'warn');
          return;
        }
        await doTailStart();
      } catch (e) {
        toast('启动 tail 失败：' + e.message, 'err');
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
      // v0.5 修复：实时 tail 改用 buffer + requestAnimationFrame 批量 append。
      // 原版每行都 textContent += + 全文 split/slice/join，日志一快直接卡死浏览器。
      // 与独立 tab tail.js 同样的优化策略：缓冲 N 行，rAF 一次性 appendChild TextNode。
      if (o.kind === 'line') {
        pendingTailLines.push(o.line);
      } else if (o.kind === 'info') {
        pendingTailLines.push('⟦info⟧ ' + o.msg);
      } else if (o.kind === 'error') {
        pendingTailLines.push('⟦error⟧ ' + o.msg);
      } else if (o.kind === 'done') {
        pendingTailLines.push('⟦done⟧ ' + o.msg);
      } else {
        pendingTailLines.push(String(o));
      }
      scheduleFlushTail();
    }

    // ----- tail 缓冲 + rAF 批量刷新（v0.5 修复卡死） -----
    const MAX_TAIL_LINES = 5000;
    let pendingTailLines = [];
    let tailFlushScheduled = false;
    let tailTotalLines = 0;

    function scheduleFlushTail() {
      if (tailFlushScheduled) return;
      tailFlushScheduled = true;
      // 优先 rAF（16ms 一帧），没有 rAF 的环境用 setTimeout 50ms 兜底
      const raf = typeof requestAnimationFrame === 'function' ? requestAnimationFrame : (fn) => setTimeout(fn, 50);
      raf(flushTailBuffer);
    }
    function flushTailBuffer() {
      tailFlushScheduled = false;
      if (!pendingTailLines.length) return;
      const chunk = pendingTailLines.join('\n') + '\n';
      pendingTailLines = [];
      // 用 appendChild TextNode 而非 textContent +=：避免整段重排
      tailOut.appendChild(document.createTextNode(chunk));
      tailTotalLines += chunk.split('\n').length - 1;
      // 限速：超过 5000 行截断
      const lineCount = tailTotalLines;
      if (lineCount > MAX_TAIL_LINES) {
        // 直接截断：拿整段 textContent 切后 5000 行重新赋值（罕见操作，可接受）
        const arr = tailOut.textContent.split('\n');
        tailOut.textContent = arr.slice(arr.length - MAX_TAIL_LINES).join('\n');
        tailTotalLines = MAX_TAIL_LINES;
      }
      tailOut.scrollTop = tailOut.scrollHeight;
    }

    const tailCard = el('div', { class: 'card' }, [
      el('h3', { text: '实时 tail（单文件，SSE 流式）' }),
      el('div', { class: 'card-desc', text: '跟踪远程文件新增行（用 tail -F）。使用主表单的「目标服务器」「日志目录」选择 —— 取第一台勾选服务器。日志量大卡顿时点「↗ 新窗口打开」切到独立窗口。' }),
      el('div', { class: 'grid-2' }, [
        el('div', null, [el('label', { text: '文件名（相对日志目录）' }), tailFileInp]),
        el('div', null, [el('label', { text: '起始行数（0=只追新增）' }), tailLinesInp])
      ]),
      el('div', { class: 'btn-row mt-2' }, [btnTailStart, btnTailStop, btnTailNewTab]),
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
      // 恢复上次选择
      const lastSel = OTB.core.lastGet('websphere', 'sel');
      if (lastSel && lastSel.system && info.systems.find(s => s.name === lastSel.system)) {
        sysSel.value = lastSel.system;
        if (lastSel.username) userInp.value = lastSel.username;
      }
      renderSrvPick();
      refreshDirs();
      if (lastSel && lastSel.dir) dirSel.value = lastSel.dir;
      refreshCredStatus();
      // 默认勾上"记住密码"（keyring 模式下；file/disabled 时由 refreshCredStatus 强制取消）
      rememberChk.checked = true;
      rememberChk.disabled = false;
      refreshCredStatus();
    }).catch(e => toast('配置加载失败：' + e.message, 'err'));
  }

  OTB.pages.websphere = renderWebsphere;
  OTB.state.routes.websphere = renderWebsphere;
  OTB.state.routeNames.websphere = 'WebSphere 日志助手';
})();