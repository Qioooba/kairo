/* ===== web/auth.js =====
 * 访问令牌认证 UI：
 *   - 页面加载时检查 /api/auth/status，判断是否需要登录
 *   - 需要登录时弹出全屏遮罩要求输入 Token
 *   - 登录成功后在顶栏显示当前用户名和登出按钮
 *   - API 请求 401 时自动弹出登录遮罩
 *
 * 暴露：window.DTB.auth
 */
(function () {
  'use strict';

  if (!window.DTB) window.DTB = {};
  if (window.DTB.auth) return;
  const auth = window.DTB.auth = {};

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
        '<div class="auth-icon">🔐</div>' +
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
        showUser(data && data.user);
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
    const right = document.querySelector('.topbar-right');
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

  function showUser(name) {
    buildUserArea();
    if (_userLabel) _userLabel.textContent = name ? ('👤 ' + name) : '';
    if (_logoutBtn) _logoutBtn.style.display = name ? '' : 'none';
    try { sessionStorage.setItem('dtb_auth_user', name || ''); } catch (e) {}
  }

  function hideUser() {
    if (_userLabel) _userLabel.textContent = '';
    if (_logoutBtn) _logoutBtn.style.display = 'none';
    try { sessionStorage.removeItem('dtb_auth_user'); } catch (e) {}
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
        try { sessionStorage.removeItem('dtb_auth_user'); } catch (e) {}
        await auth.requireLogin();
        return true;
      } else {
        if (data.user) showUser(data.user);
        return true;
      }
    } catch (e) {
      return false;
    }
  };

  auth.logout = doLogout;
  auth.getUser = function () {
    try { return sessionStorage.getItem('dtb_auth_user') || ''; } catch (e) { return ''; }
  };

  function init() {
    let cached = '';
    try { cached = sessionStorage.getItem('dtb_auth_user') || ''; } catch (e) {}
    if (cached) showUser(cached);
    auth.checkAuth();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
