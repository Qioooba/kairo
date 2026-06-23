/* ===== web/api.js =====
 * API 封装：fetch + status 联动 + 一些常用的 fetch 包装
 *
 * 暴露：window.OTB.api.{api, postJSON, getJSON, triggerDownload}
 */

(function () {
  'use strict';

  if (!window.OTB) window.OTB = {};
  if (!window.OTB.api) window.OTB.api = {};
  const apiNs = window.OTB.api;
  const core = window.OTB.core;

  async function api(method, path, body) {
    core.setStatus('busy');
    try {
      const opts = { method, headers: {} };
      if (body !== undefined) {
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(body);
      }
      const resp = await fetch(path, opts);
      let data = null;
      try { data = await resp.json(); } catch (e) { /* ignore */ }
      if (!resp.ok) {
        const msg = (data && data.error) ? data.error : ('HTTP ' + resp.status);
        throw new Error(msg);
      }
      core.setStatus('ok', '完成');
      setTimeout(() => core.setStatus('idle'), 800);
      return data;
    } catch (err) {
      core.setStatus('err', '失败');
      setTimeout(() => core.setStatus('idle'), 1500);
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

  // triggerDownload 通过 fetch 拿 blob 并触发浏览器下载（不被弹窗拦截）。
  // 默认 filename 走 Content-Disposition；可显式指定 override。
  async function triggerDownload(url, overrideFilename) {
    try {
      const r = await fetch(url);
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
      core.toast('下载失败：' + e.message, 'err');
      throw e;
    }
  }
  apiNs.triggerDownload = triggerDownload;
})();