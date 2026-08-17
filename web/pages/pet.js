/* ===== web/pages/pet.js =====
 * 宠物彩蛋：右下角漂浮宠物 + 迷你面板 + 说话气泡 + 5 秒轮询
 *
 * 入口：
 *   - app.js 启动后调 Kairo.pet.init()（已开启才挂载）
 *   - 首页「关于」卡片 10 秒内点 6 次 → Kairo.pet.unlock()
 *   - 武林页 tab 切换时调 Kairo.pet.refreshBoardTab() 控制宠物榜单入口显隐
 *
 * 后端字段见 handlers_pet.go，这里只做展示；数字可能以 float64 到达，
 * 展示前统一 Math.floor(Number())。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const el = Kairo.core.el;
  const api = (Kairo.api && Kairo.api.api) || function () {
    return Promise.reject(new Error('Kairo.api.api 未初始化'));
  };

  var cur = null;        // 当前宠物状态（/api/pet/state 返回值）
  var pollTimer = null;  // 5 秒轮询定时器
  var shakeCooldown = 0; // 摇一摇换肤冷却时间戳

  // -------- 组件节点引用 --------
  var widget = null;     // 漂浮宠物容器
  var widgetImg = null;  // 皮肤图片
  var badgeEl = null;    // Lv 徽章
  var bubbleEl = null;   // 说话气泡
  var bubbleTimer = null;
  var panel = null;      // 迷你面板
  var posTimer = null;   // 拖拽落点保存 debounce
  var samples = [];      // 摇晃采样 [{t, dx}]

  var WIDGET_W = 72;
  var WIDGET_H = 96;

  // 阶段中文名
  var STAGE_CN = { egg: '蛋', hatchling: '幼体', grown: '成体', mythic: '神话' };

  // 话语池 v1 占位常量（话语池后续再设计）
  var UNLOCK_QUIPS = [
    '嗨，我是小K，以后一起加油～',
    '主人好！我会陪着你干活的',
    '终于等到你啦！',
    '以后请多指教～'
  ];
  var LEVEL_QUIPS = [
    '升级啦，感觉更精神了！',
    '又变强了一点点，嘿嘿',
    '经验到手，精神抖擞！',
    '离下一个目标更近啦'
  ];
  var EVOLVE_QUIPS = [
    '我进化了！',
    '看看我的新样子！',
    '变得更强了哦'
  ];
  var SKIN_QUIPS = [
    '换新皮肤啦～',
    '新衣服真好看！',
    '今天也是美美的一天'
  ];
  var RENAME_QUIPS = [
    '新名字真好听！',
    '我很喜欢这个名字～',
    '好的，记住了！'
  ];

  function randomPick(arr) {
    return arr[Math.floor(Math.random() * arr.length)];
  }

  function pad2(n) {
    var s = String(n);
    return s.length < 2 ? '0' + s : s;
  }

  function skinSrc(n) {
    var s = Math.floor(Number(n || 0));
    var count = Math.max(2, Math.floor(Number((cur && cur.skin_count) || 2)));
    if (s < 0) s = 0;
    if (s >= count) s = count - 1;
    return '/static/img/pet/skin-' + pad2(s) + '.png';
  }

  function clamp(v, min, max) {
    if (v < min) return min;
    if (v > max) return max;
    return v;
  }

  function fmtNum(n) {
    return Math.floor(Number(n || 0));
  }

  // toast：优先复用 core.toast（#toast 节点），没有时自建临时节点
  function toast(msg, type) {
    if (Kairo.core && Kairo.core.toast) {
      try { Kairo.core.toast(msg, type); return; } catch (e) { /* 走 fallback */ }
    }
    var t = document.createElement('div');
    t.style.cssText = 'position:fixed;top:20%;left:50%;transform:translateX(-50%);' +
      'background:var(--bg-2,#fff);border:1px solid var(--border,rgba(0,0,0,.15));' +
      'border-radius:8px;padding:8px 14px;font-size:13px;z-index:10000;';
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(function () { try { t.remove(); } catch (e) { /* ignore */ } }, 2500);
  }

  // -------- 说话气泡 --------
  function bubble(text, ms) {
    if (!bubbleEl) return;
    bubbleEl.textContent = text;
    bubbleEl.style.display = 'block';
    bubbleEl.style.opacity = '1';
    clearTimeout(bubbleTimer);
    bubbleTimer = setTimeout(function () {
      bubbleEl.style.transition = 'opacity .3s';
      bubbleEl.style.opacity = '0';
      setTimeout(function () {
        bubbleEl.style.display = 'none';
        bubbleEl.style.transition = '';
      }, 320);
    }, ms || 4000);
  }

  // -------- init / poll --------
  function init() {
    api('GET', '/api/pet/state').then(function (resp) {
      if (!resp || !resp.enabled) {
        refreshBoardTab(); // 未开启：纠正任何已渲染页面的宠物榜 tab 显隐
        return;
      }
      cur = resp;
      mount();
      if (pollTimer) clearInterval(pollTimer);
      pollTimer = setInterval(poll, 5000);
      refreshBoardTab(); // 状态就绪后再纠正一次（渲染时序在 init 之前时）
    }).catch(function () { /* 静默 */ });
  }

  function poll() {
    api('GET', '/api/pet/state').then(function (resp) {
      if (!resp || !resp.enabled) {
        unmount();
        return;
      }
      var oldLv = fmtNum(cur && cur.level);
      var oldStage = cur ? cur.stage : null;
      cur = resp;
      var lv = fmtNum(resp.level);
      if (lv > oldLv) {
        toast('升级到 Lv ' + lv);
        bubble(randomPick(LEVEL_QUIPS));
      }
      if (resp.stage && resp.stage !== oldStage) {
        bubble(randomPick(EVOLVE_QUIPS));
        toast('宠物进化了：' + (STAGE_CN[resp.stage] || String(resp.stage)));
      }
      updateWidget();
    }).catch(function () { /* 静默 */ });
  }

  function unmount() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
    if (widget) {
      try { widget.remove(); } catch (e) { /* ignore */ }
      widget = null;
      widgetImg = null;
      badgeEl = null;
      bubbleEl = null;
    }
    closePanel();
  }

  // -------- 漂浮宠物挂载 --------
  function mount() {
    if (widget) {
      if (!widget.parentNode) document.body.appendChild(widget);
      updateWidget();
      return;
    }
    var styleStr = 'position:fixed;z-index:9999;user-select:none;cursor:grab;width:72px;height:96px;';
    var pos = cur.pos;
    if (pos && typeof pos.x === 'number' && typeof pos.y === 'number') {
      var left = clamp(Number(pos.x), 0, 1) * (window.innerWidth - WIDGET_W);
      var top = clamp(Number(pos.y), 0, 1) * (window.innerHeight - WIDGET_H);
      styleStr += 'left:' + left + 'px;top:' + top + 'px;';
    } else {
      styleStr += 'right:24px;bottom:24px;';
    }
    widget = el('div', { class: 'kairo-pet-widget', style: styleStr });

    bubbleEl = el('div', {
      class: 'kairo-pet-bubble',
      style: 'position:absolute;bottom:100%;left:50%;transform:translateX(-50%);max-width:220px;' +
        'background:var(--bg-2,#fff);color:var(--text);border:1px solid var(--border,rgba(0,0,0,.15));' +
        'border-radius:10px;padding:6px 10px;font-size:12px;white-space:nowrap;display:none;'
    });
    widgetImg = el('img', {
      src: skinSrc(cur.skin),
      alt: '宠物',
      style: 'width:64px;height:64px;pointer-events:none;display:block;'
    });
    badgeEl = el('div', {
      class: 'kairo-pet-badge',
      style: 'position:absolute;top:0;right:0;font-size:10px;padding:1px 6px;border-radius:8px;' +
        'background:var(--accent,#4a6fa5);color:#fff;'
    });
    widget.appendChild(bubbleEl);
    widget.appendChild(widgetImg);
    widget.appendChild(badgeEl);

    widget.addEventListener('mousedown', function (e) {
      e.preventDefault();
      startDrag(e.clientX, e.clientY);
    });
    widget.addEventListener('touchstart', function (e) {
      if (e.touches && e.touches.length) {
        e.preventDefault();
        startDrag(e.touches[0].clientX, e.touches[0].clientY);
      }
    }, { passive: false });

    document.addEventListener('mousemove', onDocMove);
    document.addEventListener('mouseup', onDocUp);
    document.addEventListener('touchmove', onDocTouchMove, { passive: false });
    document.addEventListener('touchend', onDocTouchEnd);

    document.body.appendChild(widget);
    updateWidget();
    bubble(randomPick(UNLOCK_QUIPS));
  }

  function updateWidget() {
    if (!widget || !cur) return;
    if (widgetImg) widgetImg.src = skinSrc(cur.skin);
    if (badgeEl) badgeEl.textContent = 'Lv' + fmtNum(cur.level);
  }

  // -------- 拖拽 + 摇晃检测 --------
  var drag = null; // {startX, startY, origLeft, origTop, moved, lastX, lastY}

  function startDrag(clientX, clientY) {
    if (!widget) return;
    var rect = widget.getBoundingClientRect();
    widget.style.right = 'auto';
    widget.style.bottom = 'auto';
    widget.style.left = rect.left + 'px';
    widget.style.top = rect.top + 'px';
    drag = {
      startX: clientX, startY: clientY,
      origLeft: rect.left, origTop: rect.top,
      moved: 0, lastX: clientX, lastY: clientY
    };
    samples = [];
    widget.style.cursor = 'grabbing';
  }

  function moveDrag(clientX, clientY) {
    if (!drag || !widget) return;
    var dx = clientX - drag.startX;
    var dy = clientY - drag.startY;
    drag.moved = Math.max(drag.moved, Math.abs(dx) + Math.abs(dy));
    var maxLeft = window.innerWidth - WIDGET_W;
    var maxTop = window.innerHeight - WIDGET_H;
    widget.style.left = clamp(drag.origLeft + dx, 0, maxLeft) + 'px';
    widget.style.top = clamp(drag.origTop + dy, 0, maxTop) + 'px';
    // 摇晃采样：水平方向大位移才记录
    var stepX = clientX - drag.lastX;
    drag.lastX = clientX;
    drag.lastY = clientY;
    if (Math.abs(stepX) > 8) {
      samples.push({ t: Date.now(), dx: stepX });
      detectShake();
    }
  }

  function detectShake() {
    if (Date.now() - shakeCooldown < 2000) return;
    var now = Date.now();
    var win = [];
    for (var i = 0; i < samples.length; i++) {
      if (now - samples[i].t <= 1500) win.push(samples[i]);
    }
    samples = win;
    var reversals = 0;
    for (var j = 1; j < win.length; j++) {
      if (win[j].dx > 0 && win[j - 1].dx < 0) reversals++;
      else if (win[j].dx < 0 && win[j - 1].dx > 0) reversals++;
    }
    if (reversals >= 4) {
      samples = [];
      shakeCooldown = Date.now();
      doSkinChange();
    }
  }

  function endDrag() {
    if (!drag || !widget) { drag = null; return; }
    var moved = drag.moved;
    drag = null;
    if (widget) widget.style.cursor = 'grab';
    if (moved < 5) {
      openMiniPanel(); // 没拖动 → 视为点击
      return;
    }
    clearTimeout(posTimer);
    posTimer = setTimeout(savePos, 500);
  }

  function savePos() {
    if (!widget || !cur) return;
    var maxLeft = window.innerWidth - WIDGET_W;
    var maxTop = window.innerHeight - WIDGET_H;
    if (maxLeft <= 0 || maxTop <= 0) return;
    var left = parseFloat(widget.style.left);
    var top = parseFloat(widget.style.top);
    if (isNaN(left) || isNaN(top)) return;
    var x = clamp(left / maxLeft, 0, 1);
    var y = clamp(top / maxTop, 0, 1);
    api('POST', '/api/pet/pos', { x: x, y: y }).then(function () {
      if (cur) cur.pos = { x: x, y: y };
    }).catch(function () { /* 静默 */ });
  }

  function onDocMove(e) { if (drag) moveDrag(e.clientX, e.clientY); }
  function onDocUp() { endDrag(); }
  function onDocTouchMove(e) {
    if (drag && e.touches && e.touches.length) {
      e.preventDefault();
      moveDrag(e.touches[0].clientX, e.touches[0].clientY);
    }
  }
  function onDocTouchEnd() { endDrag(); }

  // -------- 摇一摇换肤 --------
  function doSkinChange() {
    if (!cur) return;
    var count = Math.max(2, fmtNum(cur.skin_count || 2));
    var next = (fmtNum(cur.skin) + 1) % count;
    api('POST', '/api/pet/skin', { skin: next }).then(function (resp) {
      if (!resp || !resp.ok) return;
      cur.skin = next;
      if (widgetImg) widgetImg.src = skinSrc(next);
      rotateAnim();
      bubble(randomPick(SKIN_QUIPS));
    }).catch(function () { /* 静默 */ });
  }

  // 换肤成功后的 0.5s 摇摆动画
  function rotateAnim() {
    if (!widgetImg) return;
    widgetImg.style.transition = 'transform .25s';
    widgetImg.style.transform = 'rotate(-30deg)';
    setTimeout(function () { widgetImg.style.transform = 'rotate(30deg)'; }, 250);
    setTimeout(function () {
      widgetImg.style.transform = 'rotate(0deg)';
      setTimeout(function () {
        widgetImg.style.transition = '';
        widgetImg.style.transform = '';
      }, 260);
    }, 500);
  }

  // -------- 迷你面板 --------
  function openMiniPanel() {
    if (!cur) return;
    closePanel();
    var p = el('div', {
      class: 'kairo-pet-panel',
      style: 'position:fixed;right:24px;bottom:130px;width:240px;background:var(--bg-2,#fff);' +
        'border:1px solid var(--border,rgba(0,0,0,.15));border-radius:12px;padding:12px;' +
        'box-shadow:0 8px 24px rgba(0,0,0,.25);z-index:9999;'
    });
    panel = p;

    var closeBtn = el('button', {
      type: 'button',
      text: '×',
      title: '关闭',
      onclick: closePanel,
      style: 'position:absolute;top:6px;right:10px;background:none;border:none;color:var(--text-dim);' +
        'font-size:18px;cursor:pointer;line-height:1;padding:2px;'
    });
    p.appendChild(closeBtn);

    // 一行：名字 + Lv + 中文阶段
    var nameSpan = el('span', {
      style: 'font-weight:700;font-size:15px;color:var(--text);margin-right:6px;',
      text: String(cur.name || '小K')
    });
    var lvPill = el('span', {
      style: 'font-size:10px;padding:1px 6px;border-radius:8px;background:var(--accent,#4a6fa5);' +
        'color:#fff;margin-right:6px;vertical-align:middle;',
      text: 'Lv ' + fmtNum(cur.level)
    });
    var stageSpan = el('span', {
      style: 'font-size:12px;color:var(--text-dim);',
      text: STAGE_CN[cur.stage] || String(cur.stage || '蛋')
    });
    p.appendChild(el('div', { style: 'margin-bottom:8px;padding-right:20px;' }, [nameSpan, lvPill, stageSpan]));

    // 经验条
    var exp = Number(cur.exp || 0);
    var nextExp = Number(cur.next_exp || 0);
    var pct = nextExp > 0 ? Math.min(100, Math.floor(exp / nextExp * 100)) : 0;
    p.appendChild(el('div', {
      style: 'height:8px;border-radius:4px;background:var(--bg-1,#eee);overflow:hidden;margin-bottom:4px;'
    }, [
      el('div', {
        style: 'height:100%;border-radius:4px;background:var(--accent,#4a6fa5);width:' + pct + '%;'
      })
    ]));
    p.appendChild(el('div', {
      style: 'font-size:11px;color:var(--text-dim);margin-bottom:8px;',
      text: '经验 ' + fmtNum(exp) + ' / ' + fmtNum(nextExp)
    }));

    // 今日 / 总经验
    p.appendChild(el('div', {
      style: 'font-size:12px;color:var(--text-dim);margin-bottom:2px;',
      text: '今日经验 ' + fmtNum(cur.today_earned) + ' / ' + fmtNum(cur.daily_cap)
    }));
    p.appendChild(el('div', {
      style: 'font-size:12px;color:var(--text-dim);margin-bottom:8px;',
      text: '总经验 ' + fmtNum(cur.total_earned)
    }));

    // 改名行
    var nameInput = el('input', {
      type: 'text',
      maxlength: '16',
      value: String(cur.name || ''),
      placeholder: '给宠物起个名字',
      style: 'flex:1;min-width:0;padding:4px 8px;font-size:12px;' +
        'border:1px solid var(--border,rgba(0,0,0,.15));border-radius:6px;' +
        'background:var(--bg-1,#fff);color:var(--text);'
    });
    var renameBtn = el('button', {
      class: 'btn btn-sm',
      type: 'button',
      text: '确定',
      style: 'flex-shrink:0;',
      onclick: doRename
    });
    function doRename() {
      var newName = (nameInput.value || '').trim();
      if (!newName) { toast('名字不能为空'); return; }
      renameBtn.disabled = true;
      api('POST', '/api/pet/name', { name: newName }).then(function (resp) {
        renameBtn.disabled = false;
        if (resp && resp.name !== undefined) {
          cur = resp; // 后端返回完整 state
          nameSpan.textContent = String(resp.name || newName);
          updateWidget();
          toast('改名成功');
          bubble(randomPick(RENAME_QUIPS));
        } else {
          toast('改名失败');
        }
      }).catch(function (e) {
        renameBtn.disabled = false;
        toast(e && e.message ? e.message : '名字不合法');
      });
    }
    p.appendChild(el('div', { style: 'display:flex;gap:6px;align-items:center;margin-bottom:8px;' }, [nameInput, renameBtn]));

    // 皮肤行
    p.appendChild(el('div', {
      style: 'font-size:12px;color:var(--text-dim);margin-bottom:8px;',
      text: '皮肤 ' + (fmtNum(cur.skin) + 1) + ' / ' + Math.max(2, fmtNum(cur.skin_count || 2)) +
        '（拖住左右摇晃可换肤）'
    }));

    // 排行榜入口提示
    p.appendChild(el('div', {
      style: 'font-size:11px;color:var(--text-mute);',
      text: '宠物排行榜在「武林」页面'
    }));

    document.body.appendChild(p);
    // 延迟挂 click 监听，避免本次打开点击立刻触发关闭
    setTimeout(function () {
      document.addEventListener('click', onPanelDocClick);
    }, 0);
  }

  function onPanelDocClick(e) {
    if (!panel) return;
    var t = e.target;
    if (panel.contains(t)) return;
    if (widget && widget.contains(t)) return; // 点宠物本体：由拖拽逻辑负责重开面板
    closePanel();
  }

  function closePanel() {
    document.removeEventListener('click', onPanelDocClick);
    if (panel) {
      try { panel.remove(); } catch (e) { /* ignore */ }
      panel = null;
    }
  }

  // -------- 对外 API --------
  function isEnabled() {
    return !!(cur && cur.enabled);
  }

  function getState() {
    return cur;
  }

  function unlock() {
    if (isEnabled()) { toast('宠物已开启'); return; }
    api('POST', '/api/pet/enable', {}).then(function (resp) {
      if (resp && resp.enabled) {
        toast('已开启宠物功能，看看右下角～');
        init(); // init 挂载后会说开场白（UNLOCK_QUIPS）
      } else {
        toast('开启失败：' + ((resp && resp.error) || '响应异常'));
      }
    }).catch(function (e) {
      toast('开启失败：' + ((e && e.message) || '网络错误'));
    });
  }

  function refreshBoardTab() {
    var tabs = document.querySelectorAll('.pet-board-tab');
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].style.display = isEnabled() ? '' : 'none';
    }
  }

  Kairo.pet = {
    init: init,
    state: getState,
    say: bubble,
    bubble: bubble,
    isEnabled: isEnabled,
    unlock: unlock,
    refreshBoardTab: refreshBoardTab
  };
})();
