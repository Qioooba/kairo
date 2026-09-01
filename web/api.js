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
  async function triggerDownload(url, overrideFilename) {
    try {
      const r = await fetch(url, { credentials: 'same-origin' });
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
      if (!r.ok) throw new Error('HTTP ' + r.status);
      const blob = await r.blob();
      const a = document.createElement('a');
      const objUrl = URL.createObjectURL(blob);
      a.href = objUrl;
      a.download = overrideFilename || '';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(objUrl);
      return true;
    } catch (e) {
      if (!e.authRequired) core.toast('下载失败：' + e.message, 'err');
      throw e;
    }
  }
  apiNs.triggerDownload = triggerDownload;
})();
