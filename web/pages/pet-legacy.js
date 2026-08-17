/* ===== web/pages/pet-legacy.js =====
 * IE/旧内核兼容入口：显示宠物核心信息，并做最小 poll/交互。
 */

(function () {
  'use strict';

  var Kairo = window.Kairo = window.Kairo || {};
  var pollTimer = null;
  var state = null;
  var toastTimer = null;
  var petArtPx = 152;
  var unlockHintShown = false; // 未开启提示去重（避免 1.2s 刷屏）

  function api(path, method, payload, onSuccess, onError) {
    method = (method || 'GET').toUpperCase();
    onSuccess = onSuccess || function () {};
    onError = onError || function () {};

    var xhr;
    try {
      xhr = new XMLHttpRequest();
      xhr.open(method, '/api' + path, true);
      if (method === 'POST') {
        xhr.setRequestHeader('Content-Type', 'application/json;charset=utf-8');
      }
    } catch (e) {
      onError(e);
      return;
    }

    xhr.onreadystatechange = function () {
      if (xhr.readyState !== 4) {
        return;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        if (!xhr.responseText) {
          onSuccess({});
          return;
        }
        try {
          onSuccess(JSON.parse(xhr.responseText));
        } catch (e) {
          onError(e);
        }
        return;
      }

      var msg = '请求失败';
      try {
        var data = JSON.parse(xhr.responseText || '{}');
        if (data && data.error) {
          msg = data.error;
        }
      } catch (e) {
        /* ignore */
      }
      onError(new Error(msg));
    };

    if (method === 'POST' && payload) {
      xhr.send(JSON.stringify(payload));
      return;
    }
    xhr.send();
  }

  function toast(msg) {
    if (!window.console || !console.log) {
      return;
    }
    try {
      console.log('[pet-legacy]', msg);
    } catch (e) {
      /* ignore */
    }
  }

  function setText(el, text) {
    if (!el) {
      return;
    }
    el.textContent = text || '';
  }

  function buildRoot() {
    var root = document.getElementById('pet-root');
    var existing = document.getElementById('pet-card');
    if (!root) {
      return null;
    }
    if (existing) {
      existing.innerHTML = '';
      return existing;
    }

    var card = document.createElement('div');
    card.id = 'pet-card';
    card.style.cssText = 'position:relative;border:1px solid rgba(148,163,184,.35);background:rgba(30,41,59,.9);border-radius:14px;'
      + 'box-shadow:0 10px 30px rgba(0,0,0,.35);width:100%;height:100%;display:flex;'
      + 'flex-direction:column;justify-content:center;align-items:center;box-sizing:border-box;gap:10px;'
      + 'padding-top:12px;padding-bottom:12px;';
    root.appendChild(card);
    return card;
  }

  function showToast(msg) {
    var toastEl = document.getElementById('toast');
    if (!toastEl) {
      return;
    }
    setText(toastEl, msg || '');
    if (toastTimer) {
      clearTimeout(toastTimer);
      toastTimer = null;
    }
    toastTimer = setTimeout(function () {
      if (!toastEl) {
        return;
      }
      toastEl.textContent = '';
    }, 2600);
    toast(msg || '');
  }

  // -------- 加经验漂浮提示（冒险岛风，IE/旧内核兼容） --------
  // 用 Date.now + setInterval 驱动（IE10+ rAF 缺失时也可用），样式内联。
  var expFloats = [];
  var expFloatTimer = null;
  var lastTotal = 0; // 上次轮询总经验，用于差值提示
  var EXP_FLOAT_MAX = 5;

  function killExpFloat(f) {
    if (!f || !f.el) {
      return;
    }
    try {
      if (f.el.parentNode) {
        f.el.parentNode.removeChild(f.el);
      }
    } catch (e) {
      /* ignore */
    }
    f.el = null;
  }

  function expFloatStyle(seq) {
    var top = 8 - seq * 20;
    return 'position:absolute;left:0;right:0;top:' + top + 'px;text-align:center;'
      + 'pointer-events:none;z-index:6;font-family:"Microsoft YaHei","PingFang SC",Arial,sans-serif;'
      + 'white-space:nowrap;';
  }

  function spawnExpFloat(n) {
    n = Math.floor(Number(n || 0));
    if (n <= 0) {
      return;
    }
    var card = document.getElementById('pet-card');
    if (!card) {
      return;
    }
    while (expFloats.length >= EXP_FLOAT_MAX) {
      killExpFloat(expFloats[0]);
      expFloats.shift();
    }
    var seq = expFloats.length % 3;
    var elFloat = document.createElement('div');
    elFloat.className = 'kairo-exp-float'; // 类名供 e2e/调试选择器使用
    elFloat.style.cssText = expFloatStyle(seq);
    elFloat.innerHTML = '<span style="display:inline-block;margin-right:3px;font-size:13px;line-height:1;'
      + 'color:#fff6d8;text-shadow:0 1px 0 #c06a12,0 0 8px rgba(255,210,80,.95);">&#9733;</span>'
      + '<span style="font-size:22px;font-weight:800;line-height:1;color:#ffe27a;'
      + 'text-shadow:0 2px 0 #7a3c08,-2px 2px 0 #7a3c08,2px 2px 0 #7a3c08,-2px -1px 0 #7a3c08,'
      + '2px -1px 0 #7a3c08,0 -1px 0 #7a3c08,0 0 12px rgba(255,180,60,.9);">+' + n + '</span>';
    card.appendChild(elFloat);

    var f = {
      el: elFloat,
      t0: Date.now(),
      dur: 1500,
      ox: (Math.random() * 2 - 1) * 14,
      rot: (Math.random() * 2 - 1) * 7,
      seq: seq
    };
    expFloats.push(f);
    if (!expFloatTimer) {
      expFloatTimer = setInterval(expFloatTick, 33);
    }
  }

  function expFloatTick() {
    var now = Date.now();
    var alive = [];
    var any = false;
    for (var i = 0; i < expFloats.length; i++) {
      var f = expFloats[i];
      if (!f.el) {
        continue;
      }
      var p = (now - f.t0) / f.dur;
      if (p >= 1) {
        killExpFloat(f);
        continue;
      }
      var scale, dy, op, x, rot;
      if (p < 0.18) {
        // 弹性弹出（easeOutBack）
        var k = p / 0.18;
        var kk = k - 1;
        var b = 1.70158;
        scale = 0.2 + 0.8 * (kk * kk * ((b + 1) * kk + b) + 1);
        dy = -4 * k;
        op = k;
        x = f.ox * k;
        rot = f.rot * (1 - k);
      } else {
        // 上浮 + 横向摆动 + 尾部淡出
        var q = (p - 0.18) / 0.82;
        scale = 1 - 0.05 * q;
        dy = -4 - q * 46;
        x = f.ox * (1 - q) + Math.sin(q * Math.PI * 3) * 5 * (1 - q);
        rot = -f.rot * q;
        op = q < 0.72 ? 1 : 1 - (q - 0.72) / 0.28;
      }
      f.el.style.opacity = String(op);
      var tf = 'translate(' + x.toFixed(2) + 'px,' + dy.toFixed(2) + 'px) scale('
        + scale.toFixed(3) + ') rotate(' + rot.toFixed(2) + 'deg)';
      f.el.style.transform = tf;
      f.el.style.msTransform = tf;
      f.el.style.webkitTransform = tf;
      alive.push(f);
      any = true;
    }
    expFloats = alive;
    if (!any && expFloatTimer) {
      clearInterval(expFloatTimer);
      expFloatTimer = null;
    }
  }

  function renderStaticUI() {
    var card = document.getElementById('pet-card');
    if (!card) {
      return;
    }
    if (!document.getElementById('tips')) {
      var badge = document.createElement('div');
      badge.id = 'tips';
      badge.style.cssText = 'position:absolute;top:8px;left:8px;'
        + 'font-size:11px;padding:6px 10px;border-radius:999px;'
        + 'background:rgba(30,41,59,.75);color:#e2e8f0;border:1px solid rgba(148,163,184,.35);'
        + 'max-width:220px;line-height:1.4;white-space:normal;';
      badge.textContent = '桌面兼容模式 · 仅保留核心功能';
      card.appendChild(badge);
    }

    if (!document.getElementById('pet-art')) {
      var art = document.createElement('div');
      art.id = 'pet-art';
      art.style.width = petArtPx + 'px';
      art.style.height = petArtPx + 'px';
      art.style.borderRadius = '14px';
      art.style.background = '#0f172a no-repeat center/' + petArtPx + 'px ' + petArtPx + 'px';
      art.style.imageRendering = 'pixelated';
      art.style.border = '1px solid rgba(148,163,184,.35)';
      card.appendChild(art);
    }

    if (!document.getElementById('line1')) {
      var line1 = document.createElement('div');
      line1.id = 'line1';
      line1.style.cssText = 'font-size:13px;font-weight:600;text-align:center;';
      card.appendChild(line1);
    }

    if (!document.getElementById('line2')) {
      var line2 = document.createElement('div');
      line2.id = 'line2';
      line2.style.cssText = 'font-size:11px;color:#cbd5e1;margin-top:-4px;text-align:center;';
      card.appendChild(line2);
    }

    if (!document.getElementById('toast')) {
      var toastEl = document.createElement('div');
      toastEl.id = 'toast';
      toastEl.style.cssText = 'font-size:12px;color:#e2e8f0;min-height:30px;text-align:center;line-height:1.4;'
        + 'margin-top:2px;max-width:220px;';
      card.appendChild(toastEl);
    }

    if (!document.getElementById('stats')) {
      var row = document.createElement('div');
      row.id = 'stats';
      row.style.cssText = 'font-size:11px;color:#cbd5e1;line-height:1.6;text-align:center;';
      card.appendChild(row);
    }

    if (!document.getElementById('name-input')) {
      var nameInput = document.createElement('input');
      nameInput.type = 'text';
      nameInput.id = 'name-input';
      nameInput.maxLength = 16;
      nameInput.placeholder = '给宠物起个名';
      nameInput.style.cssText = 'width:120px;box-sizing:border-box;border-radius:8px;border:1px solid rgba(148,163,184,.5);'
        + 'background:rgba(15,23,42,.85);color:#e2e8f0;padding:4px 8px;font-size:11px;'
        + 'margin-top:2px;display:none;';
      card.appendChild(nameInput);
    }

    if (!document.getElementById('rename-btn')) {
      var btnRow = document.createElement('div');
      btnRow.style.cssText = 'display:flex;gap:6px;align-items:center;justify-content:center;margin-top:2px;';

      var renameBtn = document.createElement('button');
      renameBtn.id = 'rename-btn';
      renameBtn.className = 'btn';
      renameBtn.style.cssText = 'border:1px solid rgba(148,163,184,.5);background:rgba(59,130,246,.16);'
        + 'color:#dbeafe;border-radius:8px;font-size:11px;padding:4px 8px;cursor:pointer;'
        + 'outline:none;';
      renameBtn.textContent = '改名';
      btnRow.appendChild(renameBtn);
      card.appendChild(btnRow);

      renameBtn.onclick = function () {
        var input = document.getElementById('name-input');
        if (!input) {
          return;
        }
        if (input.style.display === 'none' || input.style.display === '') {
          input.value = (state && state.name) ? state.name : '';
          input.style.display = 'inline-block';
          renameBtn.textContent = '提交';
          return;
        }

        var newName = (input.value || '').replace(/^\s+|\s+$/g, '');
        if (!newName) {
          showToast('名字不能为空');
          return;
        }
        renameBtn.disabled = true;
        api('/pet/name', 'POST', { name: newName }, function (resp) {
          renameBtn.disabled = false;
          if (resp && resp.name !== undefined) {
            state = resp;
            applyState();
            showToast('改名成功');
          } else {
            showToast('改名失败');
            input.style.display = 'inline-block'; // 失败保留输入框供重试
            return;
          }
          input.style.display = 'none';
          renameBtn.textContent = '改名';
        }, function (e) {
          renameBtn.disabled = false;
          input.style.display = 'inline-block'; // 失败保留输入框供重试
          showToast(e && e.message ? e.message : '改名失败');
        });
      };
    }
  }

  function applyState() {
    if (!state || !document.getElementById('pet-card')) {
      return;
    }

    renderStaticUI();

    var art = document.getElementById('pet-art');
    var line1 = document.getElementById('line1');
    var line2 = document.getElementById('line2');
    var row = document.getElementById('stats');

    var skinId = (state && state.skin) || 'orange-cat';
    var safeName = (state && state.name) ? state.name : '小K';
    var levelText = (state && state.level !== undefined) ? state.level : '0';
    var stageText = (state && state.stage) ? state.stage : '未知';

    if (art) {
      art.style.backgroundImage = 'url(/static/img/pet/skins/' + encodeURIComponent(String(skinId)) + '.png)';
      art.style.backgroundSize = 'auto ' + petArtPx + 'px';
      // 精灵图 4 帧横排（128×32）：必须左对齐显示第 0 帧，居中会落在帧接缝上（显示"两个半帧"）
      art.style.backgroundPosition = '0 center';
    }

    setText(line1, safeName + '  Lv ' + levelText);
    setText(line2, '阶段：' + stageText + ' · 今日经验 ' + (state && state.today_earned ? state.today_earned : 0) + ' · 总经验 ' + (state && state.total_earned ? state.total_earned : 0));
    if (row) {
      row.textContent = '体力/上限：' + (state && state.exp !== undefined ? state.exp : 0) + ' / ' + (state && state.next_exp ? state.next_exp : 0);
    }
  }

  function poll() {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
    api('/pet/state', 'GET', null, function (resp) {
      if (!resp || !resp.enabled) {
        // 未开启：退避到 10s 而非 1.2s 高频打接口，且提示只弹一次
        if (!unlockHintShown) {
          unlockHintShown = true;
          showToast('宠物功能未开启');
        }
        pollTimer = setTimeout(poll, 10000);
        return;
      }
      var nowTotal = Number(resp.total_earned || 0);
      if (nowTotal > lastTotal) {
        spawnExpFloat(nowTotal - lastTotal);
      }
      lastTotal = nowTotal;
      state = resp;
      applyState();
      pollTimer = setTimeout(poll, 7000);
    }, function () {
      pollTimer = setTimeout(poll, 12000);
    });
  }

  function initLegacy() {
    buildRoot();
    renderStaticUI();
    showToast('正在启动宠物兼容模式…');
    poll();
  }

  Kairo.pet = Kairo.pet || {};
  Kairo.pet.initLegacy = initLegacy;
})();
