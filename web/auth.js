/* ===== web/auth.js =====
 * 访问令牌认证 UI：
 *   - 页面加载时检查 /api/auth/status，判断是否需要登录
 *   - 需要登录时弹出全屏遮罩要求输入 Token
 *   - 登录成功后在顶栏显示当前用户名和登出按钮
 *   - API 请求 401 时自动弹出登录遮罩
 *
 * 暴露：window.Kairo.auth
 */
(function () {
  'use strict';

  if (!window.Kairo) window.Kairo = {};
  if (window.Kairo.auth) return;
  const auth = window.Kairo.auth = {};

  const ICONS = {
    smShield:  'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z',
    smUser:    'M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2M12 11a4 4 0 100-8 4 4 0 000 8z',
  };
  function svgIcon(name, size) {
    const d = ICONS[name];
    if (!d) return '';
    const s = size || 24;
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="' + d + '"/></svg>';
  }

  // v0.9 rebrand: 一次性迁移 sessionStorage 用户名键（dtb_auth_user / otb_auth_user → kairo_auth_user）
  try {
    if (!sessionStorage.getItem('kairo_auth_user')) {
      var oldUser = sessionStorage.getItem('dtb_auth_user') || sessionStorage.getItem('otb_auth_user');
      if (oldUser) sessionStorage.setItem('kairo_auth_user', oldUser);
    }
  } catch (_) { /* ignore */ }

  let _overlay = null;
  let _userLabel = null;
  let _logoutBtn = null;
  let _loginPending = false;
  let _loginWaiters = [];

  function buildOverlay() {
    if (_overlay) return _overlay;
    _overlay = document.createElement('div');
    _overlay.className = 'auth-overlay';
    _overlay.innerHTML =
      '<div class="auth-card">' +
        '<div class="auth-icon">' + svgIcon('smShield', 48) + '</div>' +
        '<h2 class="auth-title">需要访问令牌</h2>' +
        '<p class="auth-subtitle">请向管理员索取您的访问令牌，输入后即可使用工具箱。<br/>令牌只需输入一次，浏览器会自动记住。</p>' +
        '<div class="auth-error" id="auth-err" style="display:none;"></div>' +
        '<div class="auth-input-group">' +
          '<input type="password" class="auth-input" id="auth-input" placeholder="粘贴访问令牌…" autocomplete="off" spellcheck="false" />' +
          '<button class="btn btn-primary auth-submit" id="auth-submit">登录</button>' +
        '</div>' +
        '<p class="auth-hint">令牌泄露可随时让管理员吊销。</p>' +
      '</div>';
    document.body.appendChild(_overlay);

    const input = _overlay.querySelector('#auth-input');
    const submit = _overlay.querySelector('#auth-submit');
    const errBox = _overlay.querySelector('#auth-err');

    function showErr(msg) {
      errBox.textContent = msg;
      errBox.style.display = 'block';
    }
    function hideErr() {
      errBox.style.display = 'none';
    }

    async function doLogin() {
      const token = (input.value || '').trim();
      if (!token) {
        showErr('请输入访问令牌');
        input.focus();
        return;
      }
      submit.disabled = true;
      submit.textContent = '验证中…';
      hideErr();
      try {
        const resp = await fetch('/api/auth/status', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'same-origin',
          body: JSON.stringify({ token: token }),
        });
        let data = null;
        try { data = await resp.json(); } catch (e) {}
        if (!resp.ok) {
          showErr((data && data.error) || ('验证失败（HTTP ' + resp.status + '）'));
          input.select();
          return;
        }
        input.value = '';
        hideOverlay();
        resolveLoginWaiters(data && data.user);
        showUser(data && data.user, data && data.role);
      } catch (e) {
        showErr('网络错误：' + e.message);
      } finally {
        submit.disabled = false;
        submit.textContent = '登录';
      }
    }

    submit.addEventListener('click', doLogin);
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') doLogin();
    });

    auth._input = input;
    return _overlay;
  }

  function showOverlay() {
    buildOverlay();
    _overlay.style.display = 'flex';
    setTimeout(function () {
      if (auth._input) auth._input.focus();
    }, 100);
  }

  function hideOverlay() {
    if (_overlay) _overlay.style.display = 'none';
  }

  function buildUserArea() {
    if (_userLabel) return;
    // v1.x 顶部 Tab 栏：改挂到右上角工具区 #header-tools，与便笺 / 主题按钮同一行前面
    const right = document.querySelector('#header-tools') || document.querySelector('#sidebar-tools') || document.querySelector('.topbar-right');
    if (!right) return;
    const area = document.createElement('div');
    area.className = 'auth-user-area';
    _userLabel = document.createElement('span');
    _userLabel.className = 'auth-user-label';
    _logoutBtn = document.createElement('button');
    _logoutBtn.className = 'auth-logout-btn';
    _logoutBtn.textContent = '登出';
    _logoutBtn.addEventListener('click', doLogout);
    area.appendChild(_userLabel);
    area.appendChild(_logoutBtn);
    right.insertBefore(area, right.firstChild);
  }

  function showUser(name, role) {
    buildUserArea();
    if (_userLabel) _userLabel.innerHTML = name ? ('<span style="display:inline-flex;align-items:center;gap:6px;">' + svgIcon('smUser', 16) + ' ' + name + '</span>') : '';
    if (_logoutBtn) _logoutBtn.style.display = name ? '' : 'none';
    try { sessionStorage.setItem('kairo_auth_user', name || ''); } catch (e) {}
    try { sessionStorage.setItem('kairo_auth_role', role || ''); } catch (e) {}
  }

  function hideUser() {
    if (_userLabel) _userLabel.textContent = '';
    if (_logoutBtn) _logoutBtn.style.display = 'none';
    try { sessionStorage.removeItem('kairo_auth_user'); sessionStorage.removeItem('kairo_auth_role'); } catch (e) {}
  }

  async function doLogout() {
    try {
      await fetch('/api/auth/logout', {
        method: 'POST',
        credentials: 'same-origin',
      });
    } catch (e) { /* ignore */ }
    hideUser();
    requireLogin();
  }

  function resolveLoginWaiters(user) {
    _loginPending = false;
    const waiters = _loginWaiters.slice();
    _loginWaiters = [];
    waiters.forEach(function (w) { w(user); });
  }

  function waitForLogin() {
    return new Promise(function (resolve) {
      _loginWaiters.push(resolve);
    });
  }

  auth.requireLogin = function () {
    if (_loginPending) return waitForLogin();
    _loginPending = true;
    showOverlay();
    return waitForLogin();
  };

  auth.checkAuth = async function () {
    try {
      const resp = await fetch('/api/auth/status', {
        method: 'GET',
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json' },
      });
      if (!resp.ok) return false;
      const data = await resp.json();
      if (data.auth_required) {
        try { sessionStorage.removeItem('kairo_auth_user'); sessionStorage.removeItem('kairo_auth_role'); } catch (e) {}
        await auth.requireLogin();
        return true;
      } else {
        if (data.user) showUser(data.user, data.role); else hideUser();
        return true;
      }
    } catch (e) {
      return false;
    }
  };

  auth.logout = doLogout;
  auth.getUser = function () {
    try { return sessionStorage.getItem('kairo_auth_user') || ''; } catch (e) { return ''; }
  };
  auth.getRole = function () {
    try { return sessionStorage.getItem('kairo_auth_role') || ''; } catch (e) { return ''; }
  };

  function init() {
    let cached = '';
    let cachedRole = '';
    try { cached = sessionStorage.getItem('kairo_auth_user') || ''; } catch (e) {}
    try { cachedRole = sessionStorage.getItem('kairo_auth_role') || ''; } catch (e) {}
    if (cached) showUser(cached, cachedRole);
    auth.checkAuth();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
