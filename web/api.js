/* ===== web/api.js =====
 * API 封装：fetch + status 联动 + 一些常用的 fetch 包装
 *
 * 暴露：window.Kairo.api.{api, postJSON, getJSON, triggerDownload}
 */

(function () {
  'use strict';

  if (!window.Kairo) window.Kairo = {};
  if (!window.Kairo.api) window.Kairo.api = {};
  const apiNs = window.Kairo.api;
  const core = window.Kairo.core;

  async function api(method, path, body) {
    core.setStatus('busy');
    try {
      const opts = { method, headers: {}, credentials: 'same-origin' };
      if (body !== undefined) {
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(body);
      }
      const resp = await fetch(path, opts);
      let data = null;
      try { data = await resp.json(); } catch (e) { /* ignore */ }
      if (resp.status === 401 && data && data.auth_required) {
        core.setStatus('idle');
        if (window.Kairo.auth && window.Kairo.auth.requireLogin) {
          window.Kairo.auth.requireLogin();
        }
        const err = new Error(data.error || '需要登录');
        err.authRequired = true;
        throw err;
      }
      if (!resp.ok) {
        const msg = (data && data.error) ? data.error : ('HTTP ' + resp.status);
        const err = new Error(msg);
        // P1-BUG-10 修复：把后端结构化 body（category / reason / suggestion）挂到 err 上，
        // 调用方按需读取做更友好的 toast/展示；没有时保持 undefined
        if (data && typeof data === 'object') {
          err.data = data;
          if (data.current) err.current = data.current;
          if (data.category) err.category = data.category;
          if (data.reason) err.reason = data.reason;
          if (data.suggestion) err.suggestion = data.suggestion;
        }
        err.status = resp.status;
        throw err;
      }
      core.setStatus('ok', '完成');
      setTimeout(() => core.setStatus('idle'), 800);
      return data;
    } catch (err) {
      if (!err.authRequired) {
        core.setStatus('err', '失败');
        setTimeout(() => core.setStatus('idle'), 1500);
      }
      throw err;
    }
  }
  apiNs.api = api;

  function postJSON(path, body) { return api('POST', path, body); }
  function getJSON(path) { return api('GET', path); }
  function putJSON(path, body) { return api('PUT', path, body); }
  function deleteJSON(path) { return api('DELETE', path); }
  apiNs.postJSON = postJSON;
  apiNs.getJSON = getJSON;
  apiNs.putJSON = putJSON;
  apiNs.deleteJSON = deleteJSON;

  async function getPreference(namespace) {
    const data = await api('GET', '/api/preferences/' + encodeURIComponent(namespace));
    return data || { exists: false, value: {} };
  }
  function putPreference(namespace, value) {
    return api('PUT', '/api/preferences/' + encodeURIComponent(namespace), value);
  }
  function deletePreference(namespace) {
    return api('DELETE', '/api/preferences/' + encodeURIComponent(namespace));
  }
  const pendingPreferences = {};
  function preferenceSaver(namespace, wait) {
    let timer = 0;
    let latest = null;
    let revision = 0;
    return function save(value) {
      latest = value;
      const mine = ++revision;
      pendingPreferences[namespace] = { revision: mine, value: value };
      clearTimeout(timer);
      timer = setTimeout(function () {
        const snapshot = latest;
        putPreference(namespace, snapshot).then(function () {
          if (pendingPreferences[namespace] && pendingPreferences[namespace].revision === mine) delete pendingPreferences[namespace];
        }).catch(function (err) {
          if (mine === revision && window.console) console.warn('保存 ' + namespace + ' 偏好失败:', err);
        });
      }, wait == null ? 500 : wait);
    };
  }
  window.addEventListener('pagehide', function () {
    Object.keys(pendingPreferences).forEach(function (namespace) {
      try {
        fetch('/api/preferences/' + encodeURIComponent(namespace), {
          method: 'PUT', credentials: 'same-origin', keepalive: true,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(pendingPreferences[namespace].value)
        });
      } catch (_) { /* best effort during page shutdown */ }
    });
  });
  apiNs.getPreference = getPreference;
  apiNs.putPreference = putPreference;
  apiNs.deletePreference = deletePreference;
  apiNs.preferenceSaver = preferenceSaver;

  // triggerDownload 通过 fetch 拿 blob 并触发浏览器下载（不被弹窗拦截）。
  // 默认 filename 走 Content-Disposition；可显式指定 override。
  // opts: { method, body, headers }，body 为对象时按 JSON POST。
  async function triggerDownload(url, overrideFilename, opts) {
    try {
      opts = opts || {};
      const fetchOpts = { credentials: 'same-origin', method: opts.method || 'GET', headers: opts.headers || {} };
      if (opts.body !== undefined) {
        fetchOpts.headers['Content-Type'] = fetchOpts.headers['Content-Type'] || 'application/json';
        fetchOpts.body = typeof opts.body === 'string' ? opts.body : JSON.stringify(opts.body);
      }
      const r = await fetch(url, fetchOpts);
      if (r.status === 401) {
        let data = null;
        try { data = await r.json(); } catch (e) {}
        if (data && data.auth_required && window.Kairo.auth && window.Kairo.auth.requireLogin) {
          window.Kairo.auth.requireLogin();
          const err = new Error(data.error || '需要登录');
          err.authRequired = true;
          throw err;
        }
      }
      if (!r.ok) {
        let msg = 'HTTP ' + r.status;
        const ct = r.headers.get('Content-Type') || '';
        if (ct.indexOf('json') >= 0) {
          try {
            const data = await r.json();
            if (data && data.error) msg = data.error;
          } catch (e) { /* keep HTTP status */ }
        }
        throw new Error(msg);
      }
      const blob = await r.blob();
      const a = document.createElement('a');
      const objUrl = URL.createObjectURL(blob);
      a.href = objUrl;
      a.download = overrideFilename || filenameFromDisposition(r.headers.get('Content-Disposition')) || 'download.bin';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(objUrl);
      return {
        filename: a.download,
        fileCount: parseInt(r.headers.get('X-Kairo-Files') || '0', 10) || 0
      };
    } catch (e) {
      if (!e.authRequired) core.toast('下载失败：' + e.message, 'err');
      throw e;
    }
  }
  function filenameFromDisposition(header) {
    if (!header) return '';
    const star = /filename\*=UTF-8''([^;]+)/i.exec(header);
    if (star) {
      try { return decodeURIComponent(star[1].replace(/["']/g, '')); } catch (e) { return star[1]; }
    }
    const plain = /filename="([^"]+)"/i.exec(header) || /filename=([^;]+)/i.exec(header);
    return plain ? plain[1].trim() : '';
  }
	apiNs.triggerDownload = triggerDownload;

  async function chooseLocalPath(opts) {
    opts = opts || {};
    const data = await api('POST', opts.directory ? '/api/choose-dir' : '/api/choose-file', {
      initial: opts.initial || ''
    });
    return data && data.path ? String(data.path) : '';
  }
  apiNs.chooseLocalPath = chooseLocalPath;

  // quoteLocalPath / applyCommandPath：给命令框插入脚本路径时保留已有参数。
  function quoteLocalPath(path) {
    path = String(path || '');
    if (!path) return '';
    if (/[\s&<>|^()"]/.test(path)) return '"' + path.replace(/"/g, '\\"') + '"';
    return path;
  }
  function applyCommandPath(current, picked) {
    const quoted = quoteLocalPath(picked);
    if (!quoted) return String(current || '');
    const s = String(current || '');
    const lead = (s.match(/^\s*/) || [''])[0];
    const body = s.slice(lead.length);
    if (!body) return quoted;
    const fileFlag = /(^|[\s])(-File)(\s+)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\S+)/i;
    if (fileFlag.test(body)) {
      return lead + body.replace(fileFlag, function (_, pre, flag, sp) {
        return pre + flag + sp + quoted;
      });
    }
    const q = body.charAt(0);
    if (q === '"' || q === "'") {
      const end = body.indexOf(q, 1);
      if (end > 0) return lead + quoted + body.slice(end + 1);
      return lead + quoted;
    }
    if (/^([A-Za-z]:[\\/]|\\\\|\/)/.test(body)) {
      const token = body.match(/^\S+/)[0];
      return lead + quoted + body.slice(token.length);
    }
    return s.replace(/\s+$/, '') + ' ' + quoted;
  }
  apiNs.quoteLocalPath = quoteLocalPath;
  apiNs.applyCommandPath = applyCommandPath;

  const FOLDER_ICON = '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 5a2 2 0 012-2h5l2 2h9a2 2 0 012 2v11a2 2 0 01-2 2H4a2 2 0 01-2-2V5z"/></svg>';
  let nativePickerOpen = false;

  function setPathBrowseBusy(busy) {
    const nodes = document.querySelectorAll('button.path-field-browse');
    for (let i = 0; i < nodes.length; i++) nodes[i].disabled = !!busy;
  }

  function paintBrowseButton(btn, opts, busy) {
    const label = opts.label || '浏览';
    if (busy) {
      btn.innerHTML = opts.iconOnly ? FOLDER_ICON : (FOLDER_ICON + '<span>选择中…</span>');
      return;
    }
    btn.innerHTML = opts.iconOnly ? FOLDER_ICON : (FOLDER_ICON + '<span>' + core.escapeHtml(label) + '</span>');
  }

  function resolveBrowseInput(opts) {
    if (typeof opts.input === 'function') return opts.input();
    return opts.input || null;
  }

  function resolveBrowseInitial(opts, input) {
    if (typeof opts.initial === 'function') return String(opts.initial() || '');
    if (opts.initial) return String(opts.initial);
    return input && input.value ? String(input.value) : '';
  }

  function writePickedPath(input, path, apply) {
    if (!input || path == null) return;
    let next = path;
    if (apply === 'command' || apply === 'insert-command') next = applyCommandPath(input.value, path);
    else if (typeof apply === 'function') next = apply(input.value, path);
    input.value = next;
    try {
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.dispatchEvent(new Event('change', { bubbles: true }));
    } catch (_) { /* old browsers without Event constructor still have the value */ }
  }

  function browseButton(opts) {
    opts = opts || {};
    const directory = !!opts.directory;
    const title = opts.title || (directory ? '选择文件夹' : '选择文件');
    const cls = opts.className || (opts.compact ? 'btn btn-sm path-field-browse' : 'btn path-field-browse');
    const classes = [cls];
    if (cls.indexOf('path-field-browse') < 0) classes.push('path-field-browse');
    if (opts.iconOnly && cls.indexOf('path-field-browse-icon') < 0) classes.push('path-field-browse-icon');
    const btn = core.el('button', {
      type: 'button',
      class: classes.join(' '),
      title: title,
      'aria-label': title
    });
    paintBrowseButton(btn, opts, false);
    btn.addEventListener('click', async function () {
      if (nativePickerOpen || btn.disabled) return;
      nativePickerOpen = true;
      setPathBrowseBusy(true);
      paintBrowseButton(btn, opts, true);
      btn.setAttribute('aria-busy', 'true');
      try {
        const input = resolveBrowseInput(opts);
        const path = await chooseLocalPath({
          directory: directory,
          initial: resolveBrowseInitial(opts, input)
        });
        if (path) {
          writePickedPath(input, path, opts.apply);
          if (opts.onPick) await opts.onPick(path, input);
        }
      } catch (e) {
        core.toast(e.message || String(e), 'err');
      } finally {
        nativePickerOpen = false;
        btn.removeAttribute('aria-busy');
        paintBrowseButton(btn, opts, false);
        setPathBrowseBusy(false);
      }
    });
    return btn;
  }

  function pathRow(input, opts) {
    opts = Object.assign({}, opts || {}, { input: input });
    const rowClass = opts.rowClass || 'path-field';
    return core.el('div', { class: rowClass }, [input, browseButton(opts)]);
  }

  apiNs.browseButton = browseButton;
  apiNs.pathRow = pathRow;
})();
