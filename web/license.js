/* ===== web/license.js =====
 * Kairo 工具箱 - 激活弹窗
 *
 * 行为:
 *   - 页面加载后调 GET /api/license/status, licensed=false 则弹全屏遮罩
 *   - 用户输入激活码 → POST /api/license/activate → 成功 reload
 *   - 弹窗复用 index.html 里已有的 .auth-overlay / .auth-card 样式 (与 web/auth.js 风格一致)
 *
 * 与 web/auth.js 的区别: auth.js 处理 Bearer token 登录, license.js 处理激活码输入
 */
(function () {
  'use strict';
  if (!window.Kairo) window.Kairo = {};
  if (window.Kairo.license) return;
  const lic = window.Kairo.license = {};

  const ICONS = {
    smKey:   'M21 2l-2 2m-7.61 7.61a5.5 5.5 0 11-7.778 7.778 5.5 5.5 0 017.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4',
    smCheck: 'M20 6L9 17l-5-5',
  };
  function svgIcon(name, size) {
    const d = ICONS[name];
    if (!d) return '';
    const s = size || 24;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

  let _overlay = null;
  let _submitting = false;

  // 不同 reason 对应不同提示语
  const REASON_TIPS = {
    'no-local-cert':   '请向管理员索取激活码，输入后即可使用本工具箱。',
    'cert-corrupted':  '本地激活记录损坏，请重新输入激活码。',
    'ip-mismatch':     '本机 IP 与首次激活时不一致（可能更换了网络环境），请重新激活。',
    'unknown':         '请输入激活码完成首次激活。'
  };

  function buildOverlay() {
    if (_overlay) return _overlay;
    _overlay = document.createElement('div');
    _overlay.className = 'auth-overlay';
    _overlay.style.display = 'none';
    _overlay.innerHTML =
      '<div class="auth-card">' +
        '<div class="auth-icon">' + svgIcon('smKey', 48) + '</div>' +
        '<h2 class="auth-title" id="lic-title">首次使用需要激活</h2>' +
        '<p class="auth-subtitle" id="lic-subtitle">请向管理员索取激活码，输入后即可使用本工具箱。<br/>激活只需一次，后续启动自动通过。</p>' +
        '<div class="auth-error" id="lic-err" style="display:none;"></div>' +
        '<div class="auth-input-group">' +
          '<input type="text" class="auth-input" id="lic-input" placeholder="粘贴激活码…" autocomplete="off" spellcheck="false" />' +
          '<button class="btn btn-primary auth-submit" id="lic-submit">激活</button>' +
        '</div>' +
        '<p class="auth-hint">本机 IP 将自动识别并绑定到当前设备，激活后不可转移。</p>' +
      '</div>';
    document.body.appendChild(_overlay);

    const input   = _overlay.querySelector('#lic-input');
    const submit  = _overlay.querySelector('#lic-submit');
    const errBox  = _overlay.querySelector('#lic-err');
    const title   = _overlay.querySelector('#lic-title');
    const subtitle= _overlay.querySelector('#lic-subtitle');

    function showErr(msg) {
      errBox.textContent = msg;
      errBox.style.display = 'block';
    }
    function hideErr() {
      errBox.style.display = 'none';
    }
    function setReason(reason) {
      const tip = REASON_TIPS[reason] || REASON_TIPS['unknown'];
      subtitle.innerHTML = tip;
      if (reason === 'ip-mismatch') {
        title.textContent = '需要重新激活';
      } else if (reason === 'cert-corrupted') {
        title.textContent = '本地记录异常';
      } else {
        title.textContent = '首次使用需要激活';
      }
    }

    async function doSubmit() {
      const code = (input.value || '').trim();
      if (!code) {
        showErr('请输入激活码');
        input.focus();
        return;
      }
      if (_submitting) return;
      _submitting = true;
      submit.disabled = true;
      submit.textContent = '激活中…';
      hideErr();
      try {
        const r = await fetch('/api/license/activate', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ code: code })
        });
        let data = {};
        try { data = await r.json(); } catch (e) {}
        if (r.ok && data.ok) {
          submit.innerHTML = svgIcon('smCheck', 16) + ' 已激活';
          submit.style.display = 'inline-flex';
          submit.style.alignItems = 'center';
          submit.style.gap = '6px';
          submit.classList.add('btn-success');
          // 短暂延迟让用户看到成功状态, 然后 reload
          setTimeout(function () { location.reload(); }, 600);
        } else {
          showErr((data && data.error) || ('激活失败（HTTP ' + r.status + '）'));
          input.select();
          submit.disabled = false;
          submit.textContent = '激活';
          _submitting = false;
        }
      } catch (e) {
        showErr('网络错误：' + (e && e.message ? e.message : e));
        submit.disabled = false;
        submit.textContent = '激活';
        _submitting = false;
      }
    }

    submit.addEventListener('click', doSubmit);
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') doSubmit();
    });

    lic._input = input;
    lic._setReason = setReason;
    return _overlay;
  }

  lic.show = function (reason) {
    buildOverlay();
    if (reason && lic._setReason) lic._setReason(reason);
    _overlay.style.display = 'flex';
    setTimeout(function () {
      if (lic._input) lic._input.focus();
    }, 100);
  };

  lic.hide = function () {
    if (_overlay) _overlay.style.display = 'none';
  };

  // check 启动时检查 license 状态, 不通过则弹框
  lic.check = async function () {
    try {
      const r = await fetch('/api/license/status', {
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json' }
      });
      if (!r.ok) {
        // 端点都没起来, 不弹 (可能后端在重启)
        console.warn('license status check failed: HTTP', r.status);
        return;
      }
      const data = await r.json();
      if (!data.licensed) {
        lic.show(data.reason || 'unknown');
      }
    } catch (e) {
      console.warn('license check failed', e);
    }
  };

  function init() {
    // 页面加载完成后检查一次
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', function () { lic.check(); });
    } else {
      lic.check();
    }
  }

  init();
})();