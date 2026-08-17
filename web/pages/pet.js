/* ===== web/pages/pet.js =====
 * 宠物彩蛋 v2：Canvas 精灵动画 + 皮肤网格 + 轮询退避 + 边缘吸附
 *
 * 入口：
 *   - app.js 启动后调 Kairo.pet.init()（已开启才挂载）
 *   - 首页「关于」卡片 10 秒内点 6 次 → Kairo.pet.unlock()
 *   - 武林页 tab 切换时调 Kairo.pet.refreshBoardTab() 控制宠物榜单入口显隐
 *
 * v2 要点（见 docs/PET-SKINS-V2-DESIGN.md）：
 *   - 皮肤 = 精灵图逐帧（/static/img/pet/skins/{id}.png，4 帧横排 128×32）；
 *   - 动画状态机 idle/drag/click/levelup/evolve/skin（requestAnimationFrame 驱动）；
 *   - 修复 v1 四 bug：document 监听泄漏 / resize 不 clamp / 气泡 nowrap 溢出 /
 *     skin_count 与文件脱节 404（v2 改为后端 skins.json 清单下发）；
 *   - 轮询退避（失败指数退避 + 页面隐藏 30s）；
 *   - 边缘吸附（贴边半隐）+ 30s 无操作自动半隐。
 */

(function () {
  'use strict';
  const Kairo = window.Kairo = window.Kairo || {};
  const el = Kairo.core.el;
  const api = (Kairo.api && Kairo.api.api) || function () {
    return Promise.reject(new Error('Kairo.api.api 未初始化'));
  };

  // -------- 常量 --------
  var WIDGET_W = 96;
  var WIDGET_H = 128;
  var SPRITE_DRAW = 88;          // 32×32 像素帧放大到 88×88（image-rendering: pixelated）
  var DEFAULT_SKIN = 'orange-cat';
  var STAGE_CN = { egg: '蛋', hatchling: '幼体', grown: '成体', mythic: '神话' };
  var CATEGORY_CN = {
    cat: '猫系', dog: '犬系', rabbit: '兔系', dragon: '龙系',
    bird: '鸟系', round: '圆滚系', capybara: '水豚系', fish: '鱼系',
    fantasy: '幻想系'
  };
  var POLL_BASE = 5000;          // 基础轮询 5s
  var POLL_HIDDEN = 30000;       // 页面隐藏退避 30s
  var POLL_MAX_BACKOFF = 60000;  // 失败退避上限 60s
  var EDGE_SNAP_PX = 24;         // 距边缘多少 px 内吸附
  var AUTO_DIM_MS = 30000;       // 无操作自动半隐

  // -------- 运行态 --------
  var cur = null;          // 当前宠物状态（/api/pet/state）
  var pollTimer = null;
  var polling = false;     // 在飞轮询请求去重（可见性切换等并发场景）
  var pollFail = 0;        // 连续失败次数（指数退避）
  var shakeCooldown = 0;

  var widget = null;       // 漂浮宠物容器
  var canvasEl = null;     // 精灵画布
  var ctx = null;
  var badgeEl = null;
  var bubbleEl = null;
  var bubbleTimer = null;
  var panel = null;
  var posTimer = null;
  var samples = [];        // 摇晃采样 [{t, dx}]

  var docListenersBound = false; // document/window 监听对称挂载标记（bug#1）
  var rafId = 0;
  var rafRunning = false;
  var reducedMotion = false;

  var edgeSnapped = false; // 贴边半隐
  var autoDimmed = false;  // 30s 无操作半隐
  var idleDimTimer = null;

  // -------- 精灵图缓存 --------
  var spriteCache = {};    // id → {img, frames, frameSize, ok}

  function skinMeta() {
    var m = (cur && cur.skin_meta) || {};
    return {
      frameSize: Math.max(4, fmtNum(m.frameSize) || 32),
      frames: Math.max(1, fmtNum(m.frames) || 4),
      fps: Math.max(1, fmtNum(m.fps) || 6)
    };
  }

  function safeSkinId(id) {
    return /^[a-z0-9][a-z0-9-]*$/i.test(String(id || '')) ? String(id) : DEFAULT_SKIN;
  }

  function skinSrc(id) {
    return '/static/img/pet/skins/' + safeSkinId(id) + '.png';
  }

  function getSprite(id) {
    id = safeSkinId(id);
    var sp = spriteCache[id];
    if (sp) return sp;
    sp = spriteCache[id] = { img: null, frames: skinMeta().frames, frameSize: skinMeta().frameSize, ok: false };
    var img = new Image();
    img.onload = function () {
      sp.img = img;
      // 图片自描述：帧高 = 图高，帧数 = 图宽/图高（与清单元信息互为兜底）
      var h = img.naturalHeight || sp.frameSize;
      var w = img.naturalWidth || h * sp.frames;
      if (h > 0 && w >= h) {
        sp.frameSize = h;
        sp.frames = Math.max(1, Math.round(w / h));
      }
      sp.ok = true;
    };
    img.onerror = function () { sp.ok = false; };
    img.src = skinSrc(id);
    return sp;
  }

  // -------- 皮肤清单辅助 --------
  function skinList() {
    var list = (cur && cur.skins) || [];
    return Array.isArray(list) ? list : [];
  }

  function unlockedIds() {
    return skinList().filter(function (s) { return s && s.unlocked; })
      .map(function (s) { return String(s.id); });
  }

  // -------- 动画状态机 --------
  var anim = {
    state: 'idle',   // idle | drag | click | levelup | evolve | skin
    t0: 0,           // 状态进入时间（performance.now）
    dur: 0,          // 限时状态时长 ms（0 = 常驻）
    tilt: 0          // drag 状态的倾斜角（deg）
  };
  var particles = []; // levelup 光环粒子

  function setAnim(state, dur) {
    anim.state = state;
    anim.t0 = performance.now();
    anim.dur = dur || 0;
    if (state === 'levelup') spawnParticles();
  }

  function spawnParticles() {
    particles.length = 0;
    var cx = WIDGET_W / 2, cy = WIDGET_H - SPRITE_DRAW / 2 - 4;
    for (var i = 0; i < 10; i++) {
      var ang = (Math.PI * 2 * i) / 10 + Math.random() * 0.4;
      var speed = 26 + Math.random() * 22;
      particles.push({
        x: cx, y: cy,
        vx: Math.cos(ang) * speed, vy: Math.sin(ang) * speed - 18,
        life: 1
      });
    }
  }

  function drawSpriteFrame(sp, frame, dx, dy, rot, whiteOut) {
    if (!ctx) return;
    var fs = sp.frameSize || 32;
    var idx = ((frame % sp.frames) + sp.frames) % sp.frames;
    ctx.save();
    ctx.translate(WIDGET_W / 2 + (dx || 0), WIDGET_H - SPRITE_DRAW / 2 - 4 + (dy || 0));
    if (rot) ctx.rotate(rot);
    ctx.imageSmoothingEnabled = false;
    if (sp.img) {
      ctx.drawImage(sp.img, idx * fs, 0, fs, fs, -SPRITE_DRAW / 2, -SPRITE_DRAW / 2, SPRITE_DRAW, SPRITE_DRAW);
    } else {
      // 精灵图未加载/加载失败：灰团兜底，避免白屏破图
      ctx.fillStyle = '#9aa4b0';
      ctx.beginPath();
      ctx.arc(0, 0, SPRITE_DRAW / 2 - 6, 0, Math.PI * 2);
      ctx.fill();
    }
    if (whiteOut) {
      // evolve 闪白：以精灵形状叠白
      ctx.globalCompositeOperation = 'source-atop';
      ctx.fillStyle = 'rgba(255,255,255,.85)';
      ctx.fillRect(-SPRITE_DRAW, -SPRITE_DRAW, SPRITE_DRAW * 2, SPRITE_DRAW * 2);
    }
    ctx.restore();
  }

  function drawLoop(now) {
    rafId = 0;
    if (!widget || !canvasEl) { rafRunning = false; return; }
    var sp = getSprite(cur && cur.skin);
    var meta = skinMeta();
    var fps = sp.ok ? 6 : meta.fps;
    var p = anim.dur > 0 ? clamp((now - anim.t0) / anim.dur, 0, 1) : 0;
    var frame = 0, dy = 0, rot = 0, whiteOut = false;

    if (reducedMotion) {
      frame = 0; // 无障碍：静态帧 0
    } else {
      switch (anim.state) {
        case 'drag':
          frame = Math.floor(((now - anim.t0) / 1000) * fps * 2);
          rot = (anim.tilt * Math.PI) / 180;
          break;
        case 'click': {
          var seq = [1, 3, 1, 3];
          frame = seq[Math.min(seq.length - 1, Math.floor(p * seq.length))];
          break;
        }
        case 'levelup':
          frame = Math.floor(((now - anim.t0) / 1000) * fps * 1.5);
          dy = -Math.abs(Math.sin(p * Math.PI * 2)) * 16; // 跳两下
          break;
        case 'evolve':
          frame = Math.floor(((now - anim.t0) / 1000) * fps);
          whiteOut = Math.floor(p * 6) % 2 === 0; // 闪白 3 次
          dy = -Math.abs(Math.sin(p * Math.PI)) * 6;
          break;
        case 'skin':
          frame = Math.floor(((now - anim.t0) / 1000) * fps);
          rot = p * Math.PI * 2; // 旋转 360°
          break;
        default: // idle
          frame = Math.floor(((now - anim.t0) / 1000) * fps);
      }
    }

    ctx.clearRect(0, 0, WIDGET_W, WIDGET_H);
    drawSpriteFrame(sp, frame, 0, dy, rot, whiteOut);

    // levelup 粒子
    if (anim.state === 'levelup' && !reducedMotion && particles.length) {
      var dt = 1 / 60;
      ctx.save();
      for (var i = 0; i < particles.length; i++) {
        var pt = particles[i];
        pt.x += pt.vx * dt; pt.y += pt.vy * dt; pt.vy += 60 * dt; pt.life -= dt * 1.1;
        if (pt.life <= 0) continue;
        ctx.globalAlpha = clamp(pt.life, 0, 1);
        ctx.fillStyle = '#f5c542';
        ctx.beginPath();
        ctx.arc(pt.x, pt.y, 2.4, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.restore();
    }

    // 限时状态结束 → 回 idle
    if (anim.dur > 0 && now - anim.t0 >= anim.dur) {
      setAnim('idle');
    }
    scheduleFrame();
  }

  function scheduleFrame() {
    if (!rafRunning || rafId) return;
    rafId = requestAnimationFrame(drawLoop);
  }

  function startLoop() {
    rafRunning = true;
    scheduleFrame();
  }

  function stopLoop() {
    rafRunning = false;
    if (rafId) { cancelAnimationFrame(rafId); rafId = 0; }
  }

  // -------- 小工具 --------
  function randomPick(arr) { return arr[Math.floor(Math.random() * arr.length)]; }

  function clamp(v, min, max) {
    if (v < min) return min;
    if (v > max) return max;
    return v;
  }

  function fmtNum(n) { return Math.floor(Number(n || 0)); }

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

  // -------- 说话气泡（bug#3：normal 换行 + max-width 防溢出） --------
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
        if (!bubbleEl) return;
        bubbleEl.style.display = 'none';
        bubbleEl.style.transition = '';
      }, 320);
    }, ms || 4000);
  }

  // -------- 加经验漂浮提示（冒险岛风：弹性弹出 → 上浮摆动 → 淡出） --------
  // 全部用 ES5 写法 + rAF 驱动，兼容旧内核；样式内联，避免依赖 style.css 版本号。
  var expFloats = [];    // 存活漂浮 [{el, t0, dur, ox, rot}]
  var expFloatRaf = 0;
  var expFloatSeq = 0;
  var EXP_FLOAT_MAX = 5; // 同屏上限

  function expFloatStyle(seq) {
    var top = 10 - seq * 20; // 多段叠加时逐层抬高，避免互相遮挡
    return 'position:absolute;left:0;right:0;top:' + top + 'px;text-align:center;' +
      'pointer-events:none;z-index:6;font-family:"Microsoft YaHei","PingFang SC",Arial,sans-serif;' +
      'white-space:nowrap;will-change:transform,opacity;';
  }

  function spawnExpFloat(n) {
    n = fmtNum(n);
    if (!widget || n <= 0) return;
    while (expFloats.length >= EXP_FLOAT_MAX) {
      killExpFloat(expFloats[0]);
      expFloats.shift();
    }
    var seq = expFloatSeq++ % 3;
    var star = el('span', {
      style: 'display:inline-block;margin-right:3px;font-size:13px;line-height:1;' +
        'color:#fff6d8;text-shadow:0 1px 0 #c06a12,0 0 8px rgba(255,210,80,.95);',
      text: '★'
    });
    var num = el('span', {
      style: 'font-size:22px;font-weight:800;line-height:1;color:#ffe27a;' +
        'text-shadow:0 2px 0 #7a3c08,-2px 2px 0 #7a3c08,2px 2px 0 #7a3c08,' +
        '-2px -1px 0 #7a3c08,2px -1px 0 #7a3c08,0 -1px 0 #7a3c08,0 0 12px rgba(255,180,60,.9);',
      text: '+' + n
    });
    var floatEl = el('div', {
      class: 'kairo-exp-float', // 类名供 e2e/调试选择器使用
      style: expFloatStyle(seq)
    }, [star, num]);
    widget.appendChild(floatEl);
    var f = {
      el: floatEl,
      t0: performance.now(),
      dur: reducedMotion ? 700 : 1500,
      ox: (Math.random() * 2 - 1) * 14, // 随机水平错位
      rot: (Math.random() * 2 - 1) * 7  // 随机起始倾斜
    };
    expFloats.push(f);
    if (reducedMotion) {
      // 无障碍：跳过位移动画，短暂展示后直接移除
      setTimeout(function () { killExpFloat(f); }, 700);
    } else if (!expFloatRaf) {
      expFloatRaf = requestAnimationFrame(expFloatTick);
    }
  }

  function killExpFloat(f) {
    if (!f || !f.el) return;
    try { if (f.el.parentNode) f.el.parentNode.removeChild(f.el); } catch (e) { /* ignore */ }
    f.el = null;
  }

  function expFloatTick(now) {
    expFloatRaf = 0;
    var alive = [];
    for (var i = 0; i < expFloats.length; i++) {
      var f = expFloats[i];
      if (!f.el) continue;
      var p = clamp((now - f.t0) / f.dur, 0, 1);
      if (p >= 1) { killExpFloat(f); continue; }
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
      f.el.style.transform = 'translate(' + x.toFixed(2) + 'px,' + dy.toFixed(2) + 'px) ' +
        'scale(' + scale.toFixed(3) + ') rotate(' + rot.toFixed(2) + 'deg)';
      f.el.style.webkitTransform = f.el.style.transform;
      alive.push(f);
    }
    expFloats = alive;
    if (expFloats.length) {
      expFloatRaf = requestAnimationFrame(expFloatTick);
    }
  }

  function clearExpFloats() {
    for (var i = 0; i < expFloats.length; i++) killExpFloat(expFloats[i]);
    expFloats = [];
    if (expFloatRaf) { cancelAnimationFrame(expFloatRaf); expFloatRaf = 0; }
  }

  // -------- init / poll（含退避） --------
  function init() {
    api('GET', '/api/pet/state').then(function (resp) {
      pollFail = 0;
      if (!resp || !resp.enabled) {
        unmount();
        refreshBoardTab();
        return;
      }
      cur = resp;
      mount();
      schedulePoll();
      refreshBoardTab();
    }).catch(function () { /* 静默 */ });
  }

  function schedulePoll() {
    clearTimeout(pollTimer);
    var iv = POLL_BASE;
    if (document.hidden) {
      iv = POLL_HIDDEN; // 页面隐藏固定 30s，失败退避不再叠加覆盖
    } else if (pollFail > 0) {
      iv = Math.min(POLL_BASE * Math.pow(2, pollFail), POLL_MAX_BACKOFF);
    }
    pollTimer = setTimeout(poll, iv);
  }

  function poll() {
    if (polling) return; // 已有在飞请求，跳过本次（避免响应乱序覆盖 cur）
    polling = true;
    api('GET', '/api/pet/state').then(function (resp) {
      pollFail = 0;
      if (!resp || !resp.enabled) {
        unmount();
        refreshBoardTab();
        return;
      }
      var oldLv = fmtNum(cur && cur.level);
      var oldTotalExp = fmtNum(cur && cur.total_earned);
      var oldStage = cur ? cur.stage : null;
      var oldUnlocked = {};
      var oldSkin = cur ? cur.skin : null;
      skinList().forEach(function (s) {
        if (s && s.unlocked) oldUnlocked[String(s.id)] = true;
      });
      cur = resp;
      var lv = fmtNum(resp.level);
      var nowTotalExp = fmtNum(resp.total_earned);

      if (nowTotalExp > oldTotalExp) {
        var gained = nowTotalExp - oldTotalExp;
        spawnExpFloat(gained);
        bubble(randomPick(EXP_QUIPS));
      }

      if (lv > oldLv) {
        toast('升级到 Lv ' + lv);
        bubble(randomPick(LEVEL_QUIPS));
        setAnim('levelup', 1000);
      }
      if (resp.stage && resp.stage !== oldStage) {
        bubble(randomPick(EVOLVE_QUIPS));
        toast('宠物进化了：' + (STAGE_CN[resp.stage] || String(resp.stage)));
        setAnim('evolve', 900);
        setTimeout(openMiniPanel, 950); // 展示新解锁的皮肤网格
      }
      // 新解锁皮肤提示（不含换肤导致的旧值缺失）
      if (oldSkin) {
        var fresh = skinList().filter(function (s) {
          return s && s.unlocked && !oldUnlocked[String(s.id)];
        });
        if (fresh.length) toast('解锁新皮肤 ×' + fresh.length + '，点击宠物查看');
      }
      updateWidget();
      schedulePoll();
    }).catch(function () {
      pollFail++;
      schedulePoll();
    }).then(function () {
      polling = false; // 成功/失败分支都释放在飞标记
    });
  }

  // -------- 漂浮宠物挂载（bug#1：监听对称挂载/卸载） --------
  function bindDocListeners() {
    if (docListenersBound) return;
    docListenersBound = true;
    document.addEventListener('mousemove', onDocMove);
    document.addEventListener('mouseup', onDocUp);
    document.addEventListener('touchmove', onDocTouchMove, { passive: false });
    document.addEventListener('touchend', onDocTouchEnd);
    window.addEventListener('resize', onWinResize);           // bug#2：resize clamp
    document.addEventListener('visibilitychange', onVisibility);
  }

  function unbindDocListeners() {
    if (!docListenersBound) return;
    docListenersBound = false;
    document.removeEventListener('mousemove', onDocMove);
    document.removeEventListener('mouseup', onDocUp);
    document.removeEventListener('touchmove', onDocTouchMove);
    document.removeEventListener('touchend', onDocTouchEnd);
    window.removeEventListener('resize', onWinResize);
    document.removeEventListener('visibilitychange', onVisibility);
  }

  function onVisibility() {
    if (document.hidden) {
      stopLoop();      // 页面隐藏：动画暂停 + 轮询退避
      schedulePoll();
    } else {
      startLoop();
      poll();          // 回前台立即刷新一次
    }
  }

  function mount() {
    if (widget) {
      if (!widget.parentNode) document.body.appendChild(widget);
      updateWidget();
      return;
    }
    reducedMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

    var styleStr = 'position:fixed;z-index:9999;user-select:none;cursor:grab;width:' +
      WIDGET_W + 'px;height:' + WIDGET_H + 'px;transition:opacity .25s;';
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
      style: 'position:absolute;bottom:100%;left:50%;transform:translateX(-50%);' +
        'width:max-content;max-width:260px;' +
        'background:linear-gradient(180deg,rgba(255,255,255,0.98),rgba(246,250,255,0.96));color:#1f2d43;' +
        'border:1px solid rgba(124,140,255,.3);border-radius:12px;padding:7px 12px;font-size:12px;line-height:1.45;' +
        'white-space:normal;word-break:break-all;display:none;'
    });

    var dpr = Math.min(window.devicePixelRatio || 1, 3);
    canvasEl = el('canvas', {
      width: String(WIDGET_W * dpr),
      height: String(WIDGET_H * dpr),
      style: 'width:' + WIDGET_W + 'px;height:' + WIDGET_H + 'px;display:block;pointer-events:none;'
    });
    ctx = canvasEl.getContext('2d');
    if (ctx && dpr !== 1) ctx.scale(dpr, dpr);

    badgeEl = el('div', {
      class: 'kairo-pet-badge',
      style: 'position:absolute;top:0;right:0;font-size:11px;padding:2px 8px;border-radius:10px;' +
        'background:var(--accent,#4a6fa5);color:#fff;pointer-events:none;'
    });
    widget.appendChild(bubbleEl);
    widget.appendChild(canvasEl);
    widget.appendChild(badgeEl);

    widget.addEventListener('mousedown', function (e) {
      e.preventDefault();
      wake();
      startDrag(e.clientX, e.clientY);
    });
    widget.addEventListener('touchstart', function (e) {
      if (e.touches && e.touches.length) {
        e.preventDefault();
        wake();
        startDrag(e.touches[0].clientX, e.touches[0].clientY);
      }
    }, { passive: false });
    widget.addEventListener('mouseenter', wake);

    bindDocListeners();
    document.body.appendChild(widget);

    getSprite(cur && cur.skin); // 预热当前皮肤
    setAnim('idle');
    startLoop();
    updateWidget();
    scheduleAutoDim();
    bubble(randomPick(UNLOCK_QUIPS));
  }

  function unmount() {
    clearTimeout(pollTimer); pollTimer = null;
    clearTimeout(idleDimTimer); idleDimTimer = null;
    clearTimeout(posTimer); posTimer = null;
    clearTimeout(bubbleTimer); bubbleTimer = null; // 残留气泡定时器不再访问已卸载 DOM
    stopLoop();
    unbindDocListeners();
    closePanel();
    clearExpFloats();
    if (widget) {
      try { widget.remove(); } catch (e) { /* ignore */ }
      widget = null; canvasEl = null; ctx = null;
      badgeEl = null; bubbleEl = null;
    }
    edgeSnapped = false; autoDimmed = false;
  }

  function updateWidget() {
    if (!widget || !cur) return;
    if (badgeEl) badgeEl.textContent = 'Lv' + fmtNum(cur.level);
    applyDim();
  }

  // -------- 半隐（贴边 / 30s 无操作） --------
  function applyDim() {
    if (!widget) return;
    widget.style.opacity = (edgeSnapped || autoDimmed) ? '0.55' : '1';
  }

  function scheduleAutoDim() {
    clearTimeout(idleDimTimer);
    idleDimTimer = setTimeout(function () {
      autoDimmed = true;
      applyDim();
    }, AUTO_DIM_MS);
  }

  function wake() {
    autoDimmed = false;
    if (edgeSnapped) edgeSnapped = false; // 点击/hover 弹回
    applyDim();
    scheduleAutoDim();
  }

  // -------- resize clamp（bug#2） --------
  function clampToViewport() {
    if (!widget) return;
    var maxLeft = window.innerWidth - WIDGET_W;
    var maxTop = window.innerHeight - WIDGET_H;
    if (maxLeft <= 0 || maxTop <= 0) return;
    var left = parseFloat(widget.style.left);
    var top = parseFloat(widget.style.top);
    if (isNaN(left) || isNaN(top)) return; // 未拖拽过（right/bottom 定位）无需处理
    widget.style.left = clamp(left, 0, maxLeft) + 'px';
    widget.style.top = clamp(top, 0, maxTop) + 'px';
    if (edgeSnapped) {
      // 吸附态重算贴边方向
      var center = parseFloat(widget.style.left) + WIDGET_W / 2;
      widget.style.left = (center < window.innerWidth / 2 ? 0 : maxLeft) + 'px';
    }
  }

  function onWinResize() {
    clampToViewport();
    schedulePoll(); // 窗口变化时同步校正轮询节奏（隐藏态退避）
  }

  // -------- 拖拽 + 摇晃检测 --------
  var drag = null; // {startX, startY, origLeft, origTop, moved, lastX, lastY, tiltVX}

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
      moved: 0, lastX: clientX, lastY: clientY, tiltVX: 0
    };
    samples = [];
    widget.style.cursor = 'grabbing';
    setAnim('drag');
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
    // 倾斜跟踪（平滑水平速度） + 摇晃采样
    var stepX = clientX - drag.lastX;
    drag.tiltVX = drag.tiltVX * 0.8 + stepX * 0.2;
    anim.tilt = clamp(drag.tiltVX * 1.2, -16, 16);
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
      shakeNextSkin();
    }
  }

  function endDrag() {
    if (!drag || !widget) { drag = null; return; }
    var moved = drag.moved;
    drag = null;
    anim.tilt = 0;
    if (widget) widget.style.cursor = 'grab';
    if (moved < 5) {
      setAnim('click', 500); // 点击：squash & stretch
      openMiniPanel();
      return;
    }
    snapToEdge();
    clearTimeout(posTimer);
    posTimer = setTimeout(savePos, 500);
  }

  // -------- 边缘吸附 --------
  function snapToEdge() {
    if (!widget) return;
    var maxLeft = window.innerWidth - WIDGET_W;
    var left = parseFloat(widget.style.left);
    if (isNaN(left)) return;
    var center = left + WIDGET_W / 2;
    if (center < EDGE_SNAP_PX) {
      widget.style.left = '0px';
      edgeSnapped = true;
    } else if (window.innerWidth - center < EDGE_SNAP_PX) {
      widget.style.left = maxLeft + 'px';
      edgeSnapped = true;
    } else {
      edgeSnapped = false;
    }
    applyDim();
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

  // -------- 换肤 --------
  function applySkin(id, animMs) {
    return api('POST', '/api/pet/skin', { skin: id }).then(function (resp) {
      if (!resp || !resp.ok) throw new Error((resp && resp.error) || '换肤失败');
      cur.skin = id;
      getSprite(id); // 预热
      setAnim('skin', animMs || 650);
      bubble(randomPick(SKIN_QUIPS));
      return id;
    });
  }

  // 摇一摇：仅在已解锁皮肤间循环（v2 清单驱动，无 404）
  function shakeNextSkin() {
    var ids = unlockedIds();
    if (ids.length < 2) {
      bubble('还没有别的皮肤可以换，先去升级吧～');
      return;
    }
    var idx = ids.indexOf(String(cur && cur.skin));
    var next = ids[(idx + 1 + ids.length) % ids.length] || ids[0];
    applySkin(next).catch(function (e) {
      toast(e && e.message ? e.message : '换肤失败');
    });
  }

  // -------- 迷你面板 --------
  function openMiniPanel() {
    if (!cur) return;
    closePanel();
    var p = el('div', {
      class: 'kairo-pet-panel',
      style: 'position:fixed;right:24px;bottom:130px;width:264px;background:var(--bg-2,#fff);' +
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
          if (resp.enabled !== undefined) cur = resp; // 后端返回完整 state
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

    // 皮肤网格（分类分组；标题同时是 e2e 断言锚点）
    p.appendChild(el('div', {
      style: 'font-size:12px;color:var(--text-dim);margin:8px 0 4px;',
      text: '皮肤（点击穿戴，摇晃宠物快速换肤）'
    }));
    p.appendChild(buildSkinGrid());

    p.appendChild(el('div', {
      style: 'font-size:11px;color:var(--text-mute);margin-top:8px;',
      text: '拖住宠物左右摇晃也能换肤 · 排行榜在「武林」页面'
    }));

    document.body.appendChild(p);
    setTimeout(function () {
      document.addEventListener('click', onPanelDocClick);
    }, 0);
  }

  // 皮肤网格：分类分组，锁定款灰显 + 解锁等级角标
  function buildSkinGrid() {
    var wrap = el('div', { style: 'max-height:190px;overflow-y:auto;padding-right:2px;' });
    var list = skinList();
    if (!list.length) {
      wrap.appendChild(el('div', {
        style: 'font-size:12px;color:var(--text-dim);',
        text: '皮肤清单加载中…稍后重开面板'
      }));
      return wrap;
    }

    // 按清单顺序分桶（清单本身按解锁等级排序）
    var groups = {};
    var order = [];
    list.forEach(function (s) {
      if (!s || !s.id) return;
      var cat = String(s.category || 'other');
      if (!groups[cat]) { groups[cat] = []; order.push(cat); }
      groups[cat].push(s);
    });

    var currentId = safeSkinId(cur && cur.skin);
    order.forEach(function (cat) {
      wrap.appendChild(el('div', {
        style: 'font-size:11px;color:var(--text-dim);margin:6px 0 4px;',
        text: CATEGORY_CN[cat] || cat
      }));
      var grid = el('div', { style: 'display:flex;flex-wrap:wrap;gap:6px;' });
      groups[cat].forEach(function (s) {
        grid.appendChild(skinTile(s, currentId));
      });
      wrap.appendChild(grid);
    });
    return wrap;
  }

  function skinTile(s, currentId) {
    var id = String(s.id);
    var unlocked = !!s.unlocked;
    var isCurrent = id === currentId;
    var meta = skinMeta();
    var frames = Math.max(1, fmtNum(s.frames) || meta.frames);

    var tile = el('div', {
      title: unlocked ? (String(s.name || id) + '（点击穿戴）')
        : (String(s.name || id) + ' Lv' + fmtNum(s.unlock) + ' 解锁'),
      style: 'position:relative;width:38px;height:38px;border-radius:8px;cursor:' + (unlocked ? 'pointer' : 'not-allowed') + ';' +
        'border:2px solid ' + (isCurrent ? 'var(--accent,#4a6fa5)' : 'var(--border,rgba(0,0,0,.12))') + ';' +
        'background-color:var(--bg-1,#f3f4f6);overflow:hidden;' +
        (unlocked ? '' : 'filter:grayscale(1);opacity:.45;') +
        (isCurrent ? 'box-shadow:0 0 0 2px rgba(90,140,220,.25);' : '')
    });

    // 帧瓦片：CSS background 定位精灵图第 0 帧（宽 = frames×100%）
    var face = el('div', {
      style: 'position:absolute;inset:4px;background-repeat:no-repeat;' +
        'background-position:0 0;background-size:' + (frames * 100) + '% 100%;' +
        'image-rendering:pixelated;background-image:url(' + skinSrc(id) + ');'
    });
    tile.appendChild(face);

    if (isCurrent) {
      tile.appendChild(el('div', {
        style: 'position:absolute;bottom:0;left:0;right:0;font-size:8px;line-height:1.4;' +
          'text-align:center;background:var(--accent,#4a6fa5);color:#fff;',
        text: '当前'
      }));
    } else if (!unlocked) {
      tile.appendChild(el('div', {
        style: 'position:absolute;bottom:0;left:0;right:0;font-size:8px;line-height:1.4;' +
          'text-align:center;background:rgba(0,0,0,.55);color:#fff;',
        text: 'Lv' + fmtNum(s.unlock)
      }));
    }

    if (unlocked) {
      tile.addEventListener('click', function () {
        if (isCurrent) return;
        applySkin(id).then(function () {
          closePanel();
          openMiniPanel(); // 重开刷新"当前"标记
        }).catch(function (e) {
          toast(e && e.message ? e.message : '换肤失败');
        });
      });
    }
    return tile;
  }

  function onPanelDocClick(e) {
    if (!panel) return;
    var t = e.target;
    if (panel.contains(t)) return;
    if (widget && widget.contains(t)) return;
    closePanel();
  }

  function closePanel() {
    document.removeEventListener('click', onPanelDocClick);
    if (panel) {
      try { panel.remove(); } catch (e) { /* ignore */ }
      panel = null;
    }
  }

  // -------- 话语池 --------
  var UNLOCK_QUIPS = [
    '嗨呀，勇者召唤我啦！',
    '今天也来场冒险吧~',
    '主人大人，欢迎回来，出发！',
    '我会在你身边陪你逐级打怪'
  ];
  var EXP_QUIPS = [
    '经验到手，继续冒险！',
    '又是收获满满的一击~',
    '冒险点数已入账~',
    '咕咕，经验值涨啦！'
  ];
  var LEVEL_QUIPS = [
    '✨ 升级啦，我变更强了！',
    '我更会加班啦（开个玩笑）',
    '技能点又多了，继续干得漂亮！',
    '离下一层BOSS又进了一步'
  ];
  var EVOLVE_QUIPS = [
    '我进化了！看起来更帅了吧~',
    '全新形态到位，闪瞎小怪',
    '换皮肤不只是换样子，运气也会变好'
  ];
  var SKIN_QUIPS = [
    '换新皮肤啦，今天也要闪亮登场！',
    '新装备已穿戴，等价于加BUFF',
    '我的新衣服开局就很强大'
  ];
  var RENAME_QUIPS = [
    '新名字真好听，冒险家也该有称号！',
    '这名字很棒，记住啦～',
    '好的，铭刻在史册里！'
  ];

  // -------- 对外 API --------
  function isEnabled() { return !!(cur && cur.enabled); }
  function getState() { return cur; }

  function unlock() {
    if (isEnabled()) { toast('宠物已开启'); return; }
    api('POST', '/api/pet/enable', {}).then(function (resp) {
      if (resp && resp.enabled) {
        toast('已开启宠物功能，看看右下角～');
        init();
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
