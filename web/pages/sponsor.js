/* ===== web/pages/sponsor.js =====
 * 投喂作者页面 · 咖啡续命小站
 *
 * 风格：同事间玩笑感，搞笑不乞讨，像点外卖一样轻松
 */
(function () {
  'use strict';
  var Kairo = window.Kairo = window.Kairo || {};
  Kairo.pages = Kairo.pages || {};
  var el = Kairo.core.el;
  // v0.14 修复: api 客户端从 Kairo.api 命名空间拿, 跟 web/api.js 一致
  // (之前直接调 api() 是 ReferenceError, 排行榜整条流挂)
  var api = (Kairo.api && Kairo.api.api) || function () {
    return Promise.reject(new Error('Kairo.api.api 未初始化'));
  };

  // ---- 品牌 logo + 配色 ----
  // logo 字段指向 web/img/sponsor/ 下的图片（go:embed 嵌进二进制）
  // 库迪：用 matrix 生成的 PNG（@ 风格 + Cotti Coffee + 库迪咖啡）
  // 瑞幸：原版 SVG（白鹿 + luckin coffee）
  // 星巴克：原版彩色 SVG
  // 随缘：用 matrix 生成的奶茶杯 PNG
  // 颜色：用于文字高亮和选中态指示
  var BRANDS = [
    {
      id: 'cotti',
      name: '库迪',
      logo: '/static/img/sponsor/cotti.svg',
      color: '#C8293C',
      tagline: '9.9 续命首选'
    },
    {
      id: 'luckin',
      name: '瑞幸',
      logo: '/static/img/sponsor/luckin.svg',
      // 瑞幸原版是白鹿：浅色主题下用 filter 染成深色（保留原图，用 CSS 适配主题）
      color: '#4A90E2',
      tagline: '小蓝杯 懂的都懂'
    },
    {
      id: 'milktea',
      name: '随缘',
      logo: '/static/img/sponsor/random.png',
      color: '#9B5BD9',
      tagline: '给啥喝啥 不挑食'
    }
  ];

  // ---- 搞笑随机文案（同事玩笑感，不乞讨）----
  // 每次进页随机选一条
  var JOKES = [
    // ===== 原有 10 条 =====
    '工具帮你准点下班了？请杯咖啡不过分吧 ☕',
    '喝了你的咖啡，我改bug速度+50%（大概',
    '不请也没事，下次提需求我还是会接的（看心情',
    '你投食 我写码 合作愉快 🤝',
    'Kairo 是用爱发电的 但爱也需要咖啡因',
    '作者咖啡杯已空 正在靠意志力维持生命体征',
    '请我喝咖啡的人 提需求优先级自动+1（开玩笑的',
    '不扫码也没关系 工具照常用 我们还是好同事 😎',
    '一杯咖啡=少写一个bug 这笔交易很划算',
    '作者不挑 库迪瑞幸都行 速溶也能凑合',
    // ===== 新加 8 条 =====
    '写代码 1 小时，喝咖啡 5 分钟，这比例很合理',
    '没喝咖啡的代码 = 上线就崩的代码',
    '续的不是命，是 30 岁以后的颈椎和头发',
    '你的咖啡能帮我更快定位内存泄漏 (大概)',
    '投喂一时爽 一直投喂一直爽',
    '改不动 bug？来杯咖啡我试试',
    '这工具纯靠咖啡驱动，关掉咖啡就关掉工具',
    '工具好用？那是因为我喝了你请的咖啡',
  ];

  // ---- 底部签名（10 条随机选一条）----
  var SIGNS = [
    'Kairo · 天命契机 · 写代码不喝咖啡会死星人出品',
    'Kairo · 天命契机 · Powered by 咖啡因',
    'Kairo · 天命契机 · 此项目 99% 是咖啡做的',
    'Kairo · 天命契机 · 没咖啡就写不出代码 (别问)',
    'Kairo · 天命契机 · 一个靠咖啡续命的开源工具箱',
    'Kairo · 天命契机 · 工位摆烂中 谢谢投喂',
    'Kairo · 天命契机 · Bug 不修了 喝咖啡要紧',
    'Kairo · 天命契机 · 如果好用请请我喝一杯',
    'Kairo · 天命契机 · 持续维护中 (主要靠咖啡因)',
    'Kairo · 天命契机 · 付费功能: 你请的那杯',
  ];

  // ---- 天命武林榜：50 个昵称写死, 按 rank 1:1 绑定 ----
  // 渲染时: 排名 i 取 NICKNAMES[i-1] 拼上后端返回的真实姓名
  // 后端只返真实姓名 + 杯数, 昵称永远不变 (用户原话: "前 50 名的昵称是前端写死的")
  var NICKNAMES = [
    // ===== 神级·武林神话（#1）=====
    '武林神话',
    // ===== 绝世·至高级（#2-#3）=====
    '一代武神', '剑道至尊',
    // ===== 宗师级（#4-#7）=====
    '武林泰斗', '刀道至尊', '太极宗师', '玄铁剑主',
    // ===== 顶级·开宗立派（#8-#12）=====
    '玄门正宗', '逍遥派主', '独孤传人', '拳道宗师', '飞刀传人',
    // ===== 掌门级（#13-#17）=====
    '剑魔', '刀王', '拳圣', '枪神', '丐帮帮主',
    // ===== 名宿级·江湖名流（#18-#25）=====
    '飞天大侠', '追风剑客', '屠龙刀客', '倚天剑客', '降魔罗汉',
    '凌风剑客', '飞刀大侠', '神雕大侠',
    // ===== 强手级（#26-#30）=====
    '一剑封喉', '断水流', '醉剑仙', '独孤剑', '无影剑',
    // ===== 好手级（#31-#40）=====
    '绝情刀客', '听风剑客', '摘星剑', '寒冰剑', '烈焰刀',
    '追风刀', '闪电刀', '望月刀客', '长虹剑', '残月刀',
    // ===== 散人级（#41-#50）=====
    '寒霜剑', '落英神剑', '踏雪无痕', '踏月留香', '醉卧红尘',
    '雪飘人间', '摘叶飞花', '一苇渡江', '深藏身与名', '千里不留行'
  ];

  function brandById(id) {
    for (var i = 0; i < BRANDS.length; i++) {
      if (BRANDS[i].id === id) return BRANDS[i];
    }
    return BRANDS[2];
  }

  // ---- v0.14 起: 不再前端组装 drinks 数组, 用后端 entry.total / entry.cotti / etc. 字段 ----
  // 删除 cn / formatDrinks / totalCups 死代码 (mock 阶段用过, 改 API 后用不上)

  // v0.15 改造: 同步骨架 + 异步排行榜
  // 旧版: 整个 renderSponsor 是 async, 排行榜 await 卡住会让整个 view 都是空的,
  //       一旦 Java 端慢/挂, 用户感觉"页面崩了"
  // 新版: 静态内容(banner/品牌/QR/footer)立即 append,
  //       排行榜只在自己容器里显示 loading, 失败/空都只影响这个容器, 不影响其他模块
  function renderSponsor(view) {
    var selectedBrandId = null;

    var jokeEl = el('div', {
      style: 'text-align:center; font-size:15px; font-weight:600; color:var(--text-dim); ' +
             'padding:8px 0 16px; min-height:28px; transition:all .3s; ' +
             'display:inline-flex; align-items:center; justify-content:center; gap:6px; width:100%;'
    }, [
      // 左右两个下指箭头（替代 👇👇 emoji：Win 7 跨平台一致）
      (window.Kairo && Kairo.icons && Kairo.icons.svg)
        ? Kairo.icons.svg('arrow-down', 14)
        : document.createTextNode('↓'),
      el('span', { text: '选一杯？' }),
      (window.Kairo && Kairo.icons && Kairo.icons.svg)
        ? Kairo.icons.svg('arrow-down', 14)
        : document.createTextNode('↓'),
    ]);

    // ============ 1. 店铺招牌横幅 ============
    var banner = el('div', {
      style: 'position:relative; padding:24px 28px 20px; border-radius:20px; ' +
             'background:linear-gradient(135deg, #FEF3C7 0%, #FDE68A 50%, #FBBF24 100%); ' +
             'margin-bottom:24px; box-shadow:0 6px 20px rgba(251,191,36,0.25); ' +
             'text-align:center;'
    }, [
      el('div', {
        style: 'position:absolute; top:-10px; right:20px; background:#DC2626; color:#fff; ' +
               'padding:4px 14px; border-radius:20px; font-size:12px; font-weight:800; ' +
               'transform:rotate(5deg); box-shadow:0 2px 8px rgba(220,38,38,0.3); ' +
               'animation:sponsor-pulse 2s ease-in-out infinite; ' +
               'display:inline-flex; align-items:center; gap:5px;'
      }, [
        // 营业中绿点（替代 🟢 emoji：Win 7 无字体支持，用 SVG 圆点跨平台一致）
        (window.Kairo && Kairo.icons && Kairo.icons.svg)
          ? Kairo.icons.svg('status-dot', 12)
          : document.createTextNode('●'),
        el('span', { text: '营业中' })
      ]),
      el('div', {
        style: 'font-size:36px; font-weight:900; color:#78350F; text-shadow:1px 1px 0 #fff3;',
        text: '☕ Kairo 咖啡续命小站 ☕'
      }),
      el('div', {
        style: 'margin-top:6px; font-size:14px; font-weight:700; color:#92400E;',
        text: '——— 为Kairo 充值 token ———'
      }),
    ]);

    // ============ 2. 品牌选择区 ============
    // 直接在页面背景上展示 logo + 文字，无卡片框、无价格标签、无 emoji。
    // 用 flex 而非 grid：flex 从 Chrome 29 就稳定支持，grid 要 57+，
    // 适配 Win 7 残留老 Chrome 时 grid 会破版。
    var cardsWrap = el('div', {
      class: 'sponsor-row',
      style: 'display:flex; flex-wrap:wrap; justify-content:center; align-items:flex-start; ' +
             'gap:20px; padding:8px 0; max-width:680px; margin:0 auto;'
    });

    function selectBrand(brand) {
      selectedBrandId = brand.id;
      var cards = cardsWrap.querySelectorAll('.sponsor-item');
      for (var i = 0; i < cards.length; i++) {
        var bid = cards[i].getAttribute('data-brand');
        var b = brandById(bid);
        var isSelected = bid === brand.id;
        // 选中态：放大 1.08 + 底部彩色实心长条
        // 不透明度不变（用户反馈"颜色浅"——不能靠 opacity 0.65 区分选中状态）
        cards[i].style.transform = isSelected ? 'scale(1.08)' : 'scale(1)';
        var indicator = cards[i].querySelector('.sponsor-indicator');
        if (indicator) {
          indicator.style.background = isSelected ? b.color : 'transparent';
          indicator.style.borderColor = isSelected ? b.color : 'rgba(255,255,255,0.18)';
          indicator.style.borderWidth = isSelected ? '0' : '1px';
          indicator.style.width = isSelected ? '50px' : '30px';
        }
      }
      jokeEl.textContent = '已选：' + brand.name + ' · ' + brand.tagline;
      jokeEl.style.color = brand.color;
    }

    BRANDS.forEach(function (brand) {
      // 无 border / 无 background / 无 box-shadow / 无 border-radius：
      // logo 直接铺在页面背景上，下方配品牌名 + tagline
      var card = el('div', {
        class: 'sponsor-item',
        'data-brand': brand.id,
        tabindex: '0',
        role: 'button',
        style: 'cursor:pointer; padding:12px 8px 6px; min-width:130px; ' +
               'text-align:center; transition:transform .2s ease; ' +
               'flex:0 0 auto;',
        onkeydown: function (e) {
          // Enter / Space 触发选中（键盘可访问性 —— M-5）
          if (e.key === 'Enter' || e.key === ' ' || e.key === 'Spacebar') {
            e.preventDefault();
            selectBrand(brand);
          }
        }
      }, [
        // logo 区域：高度 96px 居中展示。所有图片高度统一，宽度按比例自适应
        el('div', {
          style: 'height:96px; display:flex; align-items:center; justify-content:center; ' +
                 'margin-bottom:14px;'
        }, [
          el('img', {
            src: brand.logo,
            alt: brand.name,
            // 瑞幸原版白鹿在浅色主题下需要染成深色（在 web/style.css 里加 :root[data-theme="light"] 选择器）
            style: 'max-height:96px; max-width:140px; width:auto; height:auto;',
            onerror: function () { this.style.display = 'none'; var span = document.createElement('span'); span.textContent = this.alt || ''; span.style.cssText = 'font-size:14px;color:#999;'; this.parentNode.appendChild(span); }
          })
        ]),
        // 品牌名
        el('div', {
          style: 'font-size:18px; font-weight:800; color:var(--text); margin-bottom:4px;',
          text: brand.name
        }),
        // tagline
        el('div', {
          style: 'font-size:12px; color:var(--text-dim); font-weight:600; margin-bottom:10px;',
          text: brand.tagline
        }),
        // 选中态指示条（细长色块，未选中时灰透明，选中时变品牌色 + 加长）
        el('div', {
          class: 'sponsor-indicator',
          style: 'height:3px; width:20px; margin:0 auto; border-radius:2px; ' +
                 'background:rgba(255,255,255,0.1); transition:all .2s ease;'
        })
      ]);

      card.addEventListener('click', function () { selectBrand(brand); });
      // hover 仅做轻微缩放，不再用 opacity 变浅（用户反馈"颜色浅"）
      card.addEventListener('mouseenter', function () {
        if (selectedBrandId !== brand.id) {
          card.style.transform = 'scale(1.04)';
        }
      });
      card.addEventListener('mouseleave', function () {
        if (selectedBrandId !== brand.id) {
          card.style.transform = 'scale(1)';
        }
      });

      cardsWrap.appendChild(card);
    });

    // ============ 3. 投喂方式区 ============
    // mock QR 占位：看起来像二维码但其实是装饰 —— 真实二维码后续可替换 src
    // 设计：标准 QR 的 3 个角 finder pattern（3x3 边框 + 中心 1x1 实心）+ 数据区
    var qrMock = (function () {
      var S = 16;  // 每格 16px
      var N = 13;  // 13x13 网格
      // 3 个 finder：3x3 边框 + 中心 1x1 实心
      function finder(cx, cy) {
        var s = '';
        // 3x3 边框
        for (var dx = 0; dx < 3; dx++) {
          for (var dy = 0; dy < 3; dy++) {
            if (dx === 0 || dx === 2 || dy === 0 || dy === 2) {
              s += '<rect x="' + (cx + dx) * S + '" y="' + (cy + dy) * S + '" width="' + S + '" height="' + S + '" fill="#1a1a1a"/>';
            }
          }
        }
        // 中心 1x1 实心
        s += '<rect x="' + (cx + 1) * S + '" y="' + (cy + 1) * S + '" width="' + S + '" height="' + S + '" fill="#1a1a1a"/>';
        return s;
      }
      var rects = '';
      rects += finder(0, 0);                  // 左上
      rects += finder(N - 3, 0);              // 右上
      rects += finder(0, N - 3);              // 左下
      // 数据区（避开 3 个 finder）
      // 用 deterministic 伪随机数据
      var seed = 7;
      function rnd() {
        seed = (seed * 9301 + 49297) % 233280;
        return seed / 233280;
      }
      for (var y = 0; y < N; y++) {
        for (var x = 0; x < N; x++) {
          // 跳过 finder 区域
          if ((x < 3 && y < 3) || (x >= N - 3 && y < 3) || (x < 3 && y >= N - 3)) continue;
          // 跳过 finder 之间的"分隔条"（标准 QR 这里是空白）
          if (x === 3 && (y < 3 || y >= N - 3)) continue;
          if (y === 3 && (x < 3 || x >= N - 3)) continue;
          if (rnd() > 0.5) {
            rects += '<rect x="' + x * S + '" y="' + y * S + '" width="' + S + '" height="' + S + '" fill="#1a1a1a"/>';
          }
        }
      }
      var size = N * S;
      return '<svg viewBox="0 0 ' + size + ' ' + size + '" width="120" height="120" xmlns="http://www.w3.org/2000/svg">' +
             '<rect width="' + size + '" height="' + size + '" fill="#fff" rx="6"/>' +
             rects +
             '<rect x="0" y="0" width="' + size + '" height="' + size + '" fill="none" stroke="#1a1a1a" stroke-width="1" rx="6"/>' +
             '</svg>';
    })();

    // 默认显示：mock QR + 扫码提示
    // 点击后切换到"玩笑话"对话气泡
    var qrRevealed = false;
    var qrBox = el('div', {
      style: 'width:120px; height:120px; border-radius:10px; background:#fff; ' +
             'box-shadow:0 2px 8px rgba(0,0,0,0.10); ' +
             'position:relative; cursor:pointer; overflow:hidden; ' +
             'display:flex; align-items:center; justify-content:center; ' +
             'border:2px solid rgba(0,0,0,0.08); flex-shrink:0;'
    });
    qrBox.innerHTML = qrMock;

    // 彩蛋提示（点击 QR 时展开，紧贴在卡片下方）
    // 注：text 里不用 emoji —— 全部走 inline SVG，Win 7 跨平台一致
    var jokeCard = el('div', {
      style: 'display:none; margin-top:10px; padding:8px 12px; border-radius:10px; ' +
             'background:linear-gradient(135deg, #FEF3C7 0%, #FDE68A 100%); ' +
             'color:#78350F; font-size:12px; line-height:1.5; font-weight:600; text-align:center; ' +
             'align-items:center; justify-content:center; gap:6px;'
    }, [
      // 笑（替代 😏 emoji — 彩蛋文案里的「得意的笑」表情，Win 7 跨平台一致）
      // 复用 coffee 图标的杯口弧线 + 嘴部上扬曲线凑一个简笔笑脸，避免新增 icon
      (function () {
        var s = '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24">' +
                '<circle cx="12" cy="12" r="9" fill="none" stroke="#78350F" stroke-width="2"/>' +
                '<circle cx="9" cy="10" r="0.9" fill="#78350F"/>' +
                '<circle cx="15" cy="10" r="0.9" fill="#78350F"/>' +
                '<path d="M8 14 Q12 17 16 14" fill="none" stroke="#78350F" stroke-width="1.6" stroke-linecap="round"/></svg>';
        var wrap = document.createElement('span');
        wrap.style.display = 'inline-flex';
        wrap.style.alignItems = 'center';
        wrap.innerHTML = s;
        return wrap;
      })(),
      el('span', { text: '这不是付款码，被骗了吧？哈哈，直接点了，送过来放我桌上' }),
      // 咖啡杯（替代 ☕ emoji：Win 7 无字体支持，用 coffee SVG 跨平台一致）
      (window.Kairo && Kairo.icons && Kairo.icons.svg)
        ? Kairo.icons.svg('coffee', 14)
        : document.createTextNode('☕')
    ]);

    function toggleJoke() {
      qrRevealed = !qrRevealed;
      jokeCard.style.display = qrRevealed ? 'flex' : 'none';
    }

    // 横排卡片：QR 在左，文字在右
    // 点击事件只挂 qrCard 一次 —— 之前 qrBox 和 qrCard 都有 handler 会触发两次 toggle，净结果不变
    var qrCard = el('div', {
      style: 'margin-top:24px; padding:14px; border-radius:14px; ' +
             'background:linear-gradient(135deg, var(--bg-1) 0%, var(--bg-2) 100%); ' +
             'border:3px dashed #F59E0B66; max-width:380px; margin-left:auto; margin-right:auto; ' +
             'cursor:pointer; transition:transform .15s;',
      onclick: toggleJoke,
      onmouseenter: function () { this.style.transform = 'translateY(-1px)'; },
      onmouseleave: function () { this.style.transform = 'translateY(0)'; }
    }, [
      el('div', { style: 'display:flex; align-items:center; gap:14px;' }, [
        qrBox,
        el('div', { style: 'flex:1; text-align:left; min-width:0;' }, [
          el('div', {
            style: 'display:inline-flex; align-items:center; gap:5px; background:#F59E0B; color:#fff; padding:3px 12px; border-radius:12px; ' +
                   'font-size:12px; font-weight:800; margin-bottom:6px; box-shadow:0 2px 6px rgba(245,158,11,0.3);'
          }, [
            // 投喂方式牌（替代 🪑 emoji：Win 7 无字体支持，用 coffee SVG 跨平台一致）
            (window.Kairo && Kairo.icons && Kairo.icons.svg)
              ? Kairo.icons.svg('coffee', 14)
              : document.createTextNode('☕'),
            el('span', { text: '投喂方式' })
          ]),
          el('div', {
            style: 'font-size:13px; color:var(--text); font-weight:700; margin-bottom:3px;',
            text: '扫一扫 请我喝一杯'
          }),
          el('div', {
            style: 'font-size:11px; color:var(--text-dim); font-weight:600; ' +
                   'display:inline-flex; align-items:center; gap:4px;'
          }, [
            // 点一下提示（替代 👆 emoji：Win 7 无字体支持，用 mouse-pointer-click SVG）
            (window.Kairo && Kairo.icons && Kairo.icons.svg)
              ? Kairo.icons.svg('mouse-pointer-click', 12)
              : document.createTextNode('☞'),
            el('span', { text: '点一下试试彩蛋' })
          ])
        ])
      ]),
      jokeCard
    ]);

    // ============ 4. 天命武林榜 (v0.15: 静态骨架立即 append, 数据异步加载) ============
    //
    // 设计: honorList 容器里先放 loading 占位, 函数末尾先 append 所有静态部分
    //       (banner/品牌/QR/footer) 到 view, 然后调 loadHonorList() 异步拉数据
    //       拉数据失败/为空都只影响 honorList 内部, 其他模块不受影响
    //
    // 后端字段 (entries[]):
    //   {rank: 1-based, real_name, cotti, lucky, milktea, total, date}
    var honorList = el('div', { style: 'margin-top:8px;' });

    // loading 占位 (克隆用, 原始节点会先 append 一次, 之后清空时再 clone 出来)
    var loadingBox = el('div', {
      style: 'text-align:center; padding:24px 16px;'
    }, [
      // 沙漏图标（替代 ⏳ emoji：Win 7 无字体支持，hourglass 自带一颗落沙动画）
      el('div', { style: 'margin-bottom:8px; display:flex; justify-content:center;' },
        [(window.Kairo && Kairo.icons && Kairo.icons.svg)
          ? Kairo.icons.svg('hourglass', 28)
          : el('div', { style: 'font-size:28px;', text: '⏳' })]),
      el('div', { style: 'font-size:13px; color:var(--text-dim);', text: '正在从天命服务器拉取榜单…' })
    ]);
    honorList.appendChild(loadingBox);

    var honorCard = el('div', { style: 'margin-top:28px;' }, [
      el('div', {
        style: 'text-align:center; margin-bottom:14px;',
      }, [
        el('span', { style: 'font-size:22px;', text: '⚔️' }),
        el('span', {
          style: 'font-size:18px; font-weight:900; color:var(--text); margin:0 8px;',
          text: '天命武林榜'
        }),
        el('span', { style: 'font-size:22px;', text: '🏆' }),
      ]),
      el('div', {
        style: 'text-align:center; font-size:12px; color:var(--text-dim); margin-bottom:12px; font-weight:600;',
        text: '武功越高 排名越前'
      }),
      honorList,
    ]);

    // ============ 5. 底部随机段子 + 随机签名 ============
    var randomJoke = JOKES[Math.floor(Math.random() * JOKES.length)];
    var randomSign = SIGNS[Math.floor(Math.random() * SIGNS.length)];
    var footer = el('div', {
      style: 'text-align:center; padding:24px 0 8px; font-size:14px; color:var(--text-dim); line-height:1.7;'
    }, [
      el('div', {
        style: 'font-weight:700; font-style:italic; padding:10px 16px; border-radius:12px; ' +
               'background:var(--bg-2); display:inline-block;',
        text: '💬 ' + randomJoke
      }),
      el('div', {
        style: 'margin-top:12px; font-size:11px; color:var(--text-mute);',
        text: randomSign
      }),
    ]);

    // ============ 组装页面 (静态部分立即 append, 排行榜异步加载) ============
    // 顺序: 招牌 → 品牌 → 提示 → QR → 武林榜 → 段子
    // 武林榜此时内部是 loading 占位, loadHonorList() 完成后会原地替换
    view.appendChild(banner);
    view.appendChild(cardsWrap);
    view.appendChild(jokeEl);
    view.appendChild(qrCard);
    view.appendChild(honorCard);
    view.appendChild(footer);

    // ---- 异步加载排行榜 (失败/空都只影响 honorList 内部) ----
    //
    // v0.15.1 修复: 不要把 loadError 反显到页面上
    //   - 之前直接 '错误: ' + loadError, 会把 endpointclient 主备地址 + Java 端响应
    //     (66.0.34.199:9080/credit/httpInterface?... HTTP 502) 全贴在页面上
    //   - 同事看到了就知道内网地址 + serviceID, 等于白送
    //   - 真实错误细节走 console.warn 给开发者排查, 后端 audit log 也有 (handlers_sponsor.go
    //     写 audit "sponsor.leaderboard.error" + err.Error()), 页面只显示一句搞笑话
    function loadHonorList() {
      // 先重置回 loading 占位 (cloneNode 是因为 loadingBox 已经被 append 过,
      // 直接重用可能被 detach, clone 出一个干净的)
      honorList.innerHTML = '';
      honorList.appendChild(loadingBox.cloneNode(true));

      api('GET', '/api/sponsor/leaderboard').then(function (resp) {
        var entries = null;
        var loadError = null;
        if (resp && resp.ok && Array.isArray(resp.entries)) {
          entries = resp.entries;
        } else {
          loadError = (resp && resp.error) ? resp.error : '响应格式不对';
          // 真实错误只走 console, UI 不显示
          // eslint-disable-next-line no-console
          console.warn('[sponsor] leaderboard load failed:', loadError);
        }
        renderHonorList(entries, loadError);
      }).catch(function (e) {
        // 网络错 / 5xx, 同样 console.warn 不反显
        // eslint-disable-next-line no-console
        console.warn('[sponsor] leaderboard request error:', e && e.message);
        renderHonorList(null, e && e.message ? e.message : '网络错误');
      });
    }

    // 排行榜拉取失败时的搞笑提示语 (v0.15.1 加, 替代之前的 "错误: <原样后端报错>")
    // 5 条随机选一条, 跟整体"咖啡续命 + 武林"风格对齐, 不暴露任何内部地址
    var HONOR_ERROR_QUIPS = [
      '☕ 天命服务器出门买咖啡了,稍等片刻',
      '🥲 江湖榜今日休刊,榜主下山云游去了',
      '🛠️ 武林盟主闭关修榜中,先来杯咖啡提提神',
      '🌙 月黑风高,榜主早已归隐山林',
      '🤔 天机不可泄露,榜单暂时算不出来'
    ];

    function renderHonorList(entries, loadError) {
      honorList.innerHTML = '';
      if (loadError) {
        // 失败: 搞笑话 + 重试按钮 (真实错误只走 console, 不反显)
        // 不再显示 '错误: <后端原始报错>' 那一行 —— 避免把内部地址 / serviceID 暴露给前端用户
        var quip = HONOR_ERROR_QUIPS[Math.floor(Math.random() * HONOR_ERROR_QUIPS.length)];
        honorList.appendChild(el('div', {
          style: 'text-align:center; padding:32px 16px;'
        }, [
          // 哭脸（替代 😢 emoji：Win 7 跨平台一致）
          el('div', { style: 'margin-bottom:8px; display:flex; justify-content:center;' },
            [(window.Kairo && Kairo.icons && Kairo.icons.svg)
              ? Kairo.icons.svg('sad-face', 32)
              : el('div', { style: 'font-size:32px;', text: '😢' })]),
          el('div', {
            style: 'font-size:14px; font-weight:700; color:var(--text-err,#dc2626); margin-bottom:14px; line-height:1.5;',
            text: quip
          }),
          el('button', {
            class: 'btn',
            style: 'padding:6px 18px;',
            text: '再试一次',
            onclick: loadHonorList
          })
        ]));
      } else if (!entries || entries.length === 0) {
        // 没人赞助
        honorList.appendChild(el('div', {
          style: 'text-align:center; padding:32px 16px;',
        }, [
          // 思考脸（替代 🤔 emoji：Win 7 跨平台一致）
          el('div', { style: 'margin-bottom:8px; display:flex; justify-content:center;' },
            [(window.Kairo && Kairo.icons && Kairo.icons.svg)
              ? Kairo.icons.svg('thinking-face', 48)
              : el('div', { style: 'font-size:48px;', text: '🤔' })]),
          el('div', { style: 'font-size:15px; font-weight:700; color:var(--text);', text: '还没人请过咖啡呢' }),
          el('div', { style: 'font-size:12px; color:var(--text-dim); margin-top:4px;', text: '第一个请的人 名字永远在这里（直到我删代码）' }),
        ]));
      } else {
        // 成功: 渲染列表 (后端已经按 rank 排好, 直接用)
        // 排名节点: 1-3 用金/银/铜 SVG 奖杯, 4+ 用两位补零数字 (04, 05, ...)
        function makeRankNode(idx) {
          if (idx === 0) {
            return el('img', { src: '/static/img/sponsor/trophy-gold.svg',   alt: '🥇', style: 'width:32px; height:32px; flex-shrink:0;' });
          } else if (idx === 1) {
            return el('img', { src: '/static/img/sponsor/trophy-silver.svg', alt: '🥈', style: 'width:32px; height:32px; flex-shrink:0;' });
          } else if (idx === 2) {
            return el('img', { src: '/static/img/sponsor/trophy-bronze.svg', alt: '🥉', style: 'width:32px; height:32px; flex-shrink:0;' });
          }
          var rankStr = String(idx + 1);
          rankStr = rankStr.length < 2 ? '0' + rankStr : rankStr;
          return el('span', {
            style: 'width:32px; text-align:center; font-size:14px; font-weight:800; color:var(--text-mute); ' +
                   'font-family:ui-monospace,monospace; flex-shrink:0; letter-spacing:0.5px;',
            text: rankStr
          });
        }

        entries.forEach(function (e, i) {
          var rankNode = makeRankNode(i);
          // 拼 name: 前 50 名拿 NICKNAMES[rank-1], 50+ 只显示真实姓名
          var nick = NICKNAMES[(e.rank || 1) - 1];
          var displayName = nick ? (nick + '·' + e.real_name) : e.real_name;

          if (i < 3) {
            // ===== Top 3: 1 行带金色左边框 (奖杯 + 名称 + 总杯数 + 日期) =====
            honorList.appendChild(el('div', {
              style: 'display:flex; align-items:center; gap:12px; padding:10px 14px; border-radius:8px; ' +
                     'margin-bottom:6px; background:var(--bg-1); border-left:3px solid #F59E0B;'
            }, [
              rankNode,
              el('span', {
                style: 'flex:1; min-width:0; font-size:15px; font-weight:800; color:var(--text); ' +
                       'white-space:nowrap; overflow:hidden; text-overflow:ellipsis;',
                text: displayName
              }),
              el('span', {
                style: 'font-size:12px; font-weight:700; color:var(--warn); ' +
                       'padding:2px 10px; border-radius:8px; background:rgba(245,158,11,0.12); flex-shrink:0;',
                text: e.total + ' 杯'
              }),
              el('span', {
                style: 'font-size:12px; color:var(--text-mute); font-family:ui-monospace,monospace; flex-shrink:0;',
                text: e.date || ''
              }),
            ]));
          } else {
            // ===== #4 ~ #50: 1 行紧凑版 (序号 + 名称 + 总杯数 + 日期) =====
            honorList.appendChild(el('div', {
              style: 'display:flex; align-items:center; gap:12px; padding:6px 14px; border-radius:6px; ' +
                     'margin-bottom:2px;'
            }, [
              rankNode,
              el('span', {
                style: 'flex:1; min-width:0; font-size:14px; font-weight:700; color:var(--text); ' +
                       'white-space:nowrap; overflow:hidden; text-overflow:ellipsis;',
                text: displayName
              }),
              el('span', {
                style: 'font-size:12px; font-weight:700; color:var(--warn); ' +
                       'padding:1px 8px; border-radius:6px; background:rgba(245,158,11,0.10); flex-shrink:0;',
                text: e.total + ' 杯'
              }),
              el('span', {
                style: 'font-size:11px; color:var(--text-mute); font-family:ui-monospace,monospace; flex-shrink:0;',
                text: e.date || ''
              }),
            ]));
          }
        });
      }
    }

    // 触发异步加载
    loadHonorList();
  }

  Kairo.pages.sponsor = renderSponsor;
  Kairo.state.routes.sponsor = renderSponsor;
  Kairo.state.routeNames.sponsor = '投喂作者';
})();
