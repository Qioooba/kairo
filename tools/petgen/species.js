/* ===== tools/petgen/species.js =====
 * 物种模板：Q 版可爱风形体的程序化像素绘制（2026-08-17 全面可爱化重画）。
 *
 * 可爱化三原则：
 *   1. 大眼：3×3 圆角大眼 + 左上白高光 + 右下微光（豆豆眼 → 果冻眼）；
 *   2. 圆胖：身体接近正圆/蛋形，重心下移（头大身小 chibi 比例）；
 *   3. 柔软：粗短四肢、大腮红、浅描边（阴影最后画，不参与描边）。
 *
 * 帧约定（f = 0..3，idle 循环）：
 *   f0 基础 / f1 squash（压扁 1px）/ f2 基础+眨眼 / f3 stretch（拉长 1px）
 *
 * 调色板 p 字段：body/belly/ear/earIn/eye/blush/mouth/pattern/acc/acc2/outline
 * 选项 o 字段：pattern('stripes'|'spots'|'gradient'|'none') / acc(配饰) /
 *   variant('milk'奶龙 | 'duck'鸭 | slime/ghost/... 幻想子形态) /
 *   chub(更胖) / sleepy(下垂眼) / dead(咸鱼脸)
 */

'use strict';

var P = require('./palette');

// ---------- 公共小件 ----------

// eyeOpen 大圆眼：3×3 主体 + 底部圆角 + 左上高光 + 右下微光
function eyeOpen(g, x, y, c) {
  g.rect(x - 1, y - 1, 3, 3, c);
  g.set(x, y + 2, c);
  g.set(x - 1, y - 1, '#ffffff');
  g.set(x + 1, y + 1, '#ffffff99');
}

// eyeBlink 眨眼横线
function eyeBlink(g, x, y, c) {
  g.rect(x - 1, y, 3, 1, c);
}

// eyes 一对大眼（blink = f2 眨眼帧）
function eyes(g, x1, x2, y, blink, c) {
  if (blink) { eyeBlink(g, x1, y, c); eyeBlink(g, x2, y, c); return; }
  eyeOpen(g, x1, y, c); eyeOpen(g, x2, y, c);
}

// eyesChill 眯眯眼 ∪∪（水豚式松弛）
function eyesChill(g, x1, x2, y, c) {
  [x1, x2].forEach(function (x) {
    g.set(x - 1, y + 1, c);
    g.set(x, y, c);
    g.set(x + 1, y + 1, c);
  });
}

// blush 腮红一对（2×2 圆角）
function blush(g, x1, x2, y, c) {
  g.rect(x1, y, 2, 2, c);
  g.set(x1, y + 2, c + '66');
  g.rect(x2, y, 2, 2, c);
  g.set(x2, y + 2, c + '66');
}

// shadow 底部阴影椭圆（最后画，不参与描边）
function shadow(g, cy, rx) {
  g.ellipse(16, cy, rx, 1.6, '#00000022');
}

// catMouth 猫嘴 ω + 鼻
function catMouth(g, cx, y, noseC, mouthC) {
  g.set(cx, y, noseC);
  g.line(cx - 2, y + 1, cx, y + 2, mouthC);
  g.line(cx, y + 2, cx + 2, y + 1, mouthC);
}

// pattern 花纹（条纹=头顶竖线 / 斑点=圆点 / 渐层=下身浅带）
function applyPattern(g, o, p, headY) {
  if (!o.pattern || o.pattern === 'none') return;
  if (o.pattern === 'stripes') {
    for (var i = -1; i <= 1; i++) {
      g.rect(16 + i * 4 - 1, headY, 2, 4, p.pattern);
    }
  } else if (o.pattern === 'spots') {
    g.ellipse(11, 13, 2, 2, p.pattern);
    g.ellipse(21, 15, 2.4, 2, p.pattern);
    g.ellipse(16, 25, 2, 1.6, p.pattern);
  } else if (o.pattern === 'gradient') {
    g.ellipse(16, 24, 7, 4, p.pattern);
  }
}

// acc 配饰：bow/scarf/bell/flower/moon/crown/orange/towel/butter
function applyAcc(g, kind, p, topY, neckY) {
  switch (kind) {
    case 'bow': // 头顶蝴蝶结
      g.tri(8, topY + 2, 5, topY - 1, 8, topY + 5, p.acc);
      g.tri(8, topY + 2, 11, topY - 1, 8, topY + 5, p.acc);
      g.set(8, topY + 2, p.acc2 || '#ffffff');
      break;
    case 'scarf': // 脖子围巾
      g.rect(10, neckY, 12, 2, p.acc);
      g.rect(20, neckY + 2, 3, 4, p.acc);
      break;
    case 'bell': // 铃铛
      g.ellipse(16, neckY + 1, 2, 2, p.acc);
      g.set(16, neckY + 1, p.acc2 || '#333333');
      break;
    case 'flower': // 头侧小花
      g.ellipse(9, topY + 1, 1.4, 1.4, p.acc);
      g.ellipse(11, topY + 1, 1.4, 1.4, p.acc);
      g.ellipse(10, topY - 1, 1.4, 1.4, p.acc);
      g.ellipse(10, topY + 3, 1.4, 1.4, p.acc);
      g.set(10, topY + 1, '#ffd700');
      break;
    case 'moon': // 头顶月牙
      g.ellipse(9, topY, 3, 3, '#f7d774');
      g.ellipse(10.6, topY - 0.6, 2.6, 2.6, 'transparent');
      break;
    case 'crown': // 小皇冠
      g.poly([[12, topY + 3], [12, topY - 1], [14, topY + 1], [16, topY - 2], [18, topY + 1], [20, topY - 1], [20, topY + 3]], p.acc);
      break;
    case 'orange': // 头顶橘子（水豚名梗）
      g.ellipse(16, topY, 3, 2.6, '#f5a34a');
      g.ellipse(16, topY, 2, 1.6, '#f8bc69');
      g.line(16, topY - 3, 16, topY - 4, '#5a8a3a');
      g.tri(16, topY - 4, 19, topY - 5, 16, topY - 3, '#6aa84a');
      break;
    case 'towel': // 头顶温泉毛巾 + 热气
      g.rect(10, topY, 12, 3, '#ffffff');
      g.rect(10, topY + 2, 12, 1, '#e8e8f0');
      g.set(13, topY + 1, '#e05a6a'); // 小红点
      g.set(18, topY + 1, '#e05a6a');
      g.line(12, topY - 3, 12, topY - 5, '#ffffffaa'); // 蒸汽
      g.line(20, topY - 3, 20, topY - 5, '#ffffffaa');
      break;
    case 'butter': // 头顶黄油吐司（黄油小猫梗）
      g.rect(10, topY - 1, 12, 4, '#e8b04a');       // 吐司
      g.rect(11, topY - 2, 10, 2, '#f8d8a0');       // 吐司内芯
      g.rect(13, topY - 4, 6, 3, '#ffd23e');        // 黄油块
      g.rect(13, topY - 4, 6, 1, '#ffe88a');        // 黄油高光
      break;
    default:
      break;
  }
}

// ---------- 形变辅助 ----------

// def 根据 f 计算形变量：{dy, drx, dry, ear}（squash 下沉、stretch 上拔）
function deform(f) {
  if (f === 1) return { drx: 1, dry: -1, dy: 1, ear: 1 };  // squash
  if (f === 3) return { drx: -1, dry: 1, dy: -1, ear: -1 }; // stretch
  return { drx: 0, dry: 0, dy: 0, ear: 0 };
}

// chubX chub 选项加胖宽度
function chubX(o) { return o.chub ? 1.5 : 0; }

// ---------- 物种：猫（噜噜式圆脸） ----------

function drawCat(g, f, p, o) {
  var d = deform(f);
  var cy = 19 + d.dy;
  // 尾巴（翘起小卷）
  g.line(25, cy + 4, 29, cy - 1, p.body);
  g.set(29, cy - 2, p.body); g.set(29, cy - 3, p.body);
  // 耳朵（大圆角三角）
  var ey = 11 + d.ear;
  g.tri(10, ey, 6, ey - 8, 14, ey - 2, p.ear);
  g.tri(22, ey, 26, ey - 8, 18, ey - 2, p.ear);
  g.tri(10, ey - 1, 8, ey - 5, 12, ey - 2, p.earIn);
  g.tri(22, ey - 1, 24, ey - 5, 20, ey - 2, p.earIn);
  // 身体（超圆胖）
  g.ellipse(16, cy, 10 + chubX(o) + d.drx, 9.5 + d.dry, p.body);
  g.ellipse(16, cy + 4, 6.5, 4.5, p.belly);
  applyPattern(g, o, p, 9 + d.ear);
  // 脸：大眼 + ω 嘴 + 大腮红
  eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  catMouth(g, 16, cy, p.blush, p.mouth);
  blush(g, 8, 22, cy - 1, p.blush);
  // 短胡须
  g.line(4, cy - 2, 7, cy - 1, '#ffffff99');
  g.line(4, cy + 1, 7, cy + 1, '#ffffff99');
  g.line(25, cy - 1, 28, cy - 2, '#ffffff99');
  g.line(25, cy + 1, 28, cy + 1, '#ffffff99');
  applyAcc(g, o.acc, p, 4, cy + 8);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：犬（垂耳 Q 版） ----------

function drawDog(g, f, p, o) {
  var d = deform(f);
  var cy = 19 + d.dy;
  // 短尾
  g.tri(25, cy + 3, 29, cy - 1, 24, cy + 1, p.body);
  // 垂耳（两侧长椭圆）
  var ay = 12 + d.ear;
  g.ellipse(8, ay + 3, 2.5, 5.5, p.ear);
  g.ellipse(24, ay + 3, 2.5, 5.5, p.ear);
  // 身体（圆胖）
  g.ellipse(16, cy, 10 + chubX(o) + d.drx, 9.5 + d.dry, p.body);
  g.ellipse(16, cy + 4, 6.5, 4.5, p.belly);
  applyPattern(g, o, p, 10 + d.ear);
  // 脸：大眼 + 大鼻头 + 吐舌
  eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  g.ellipse(16, cy, 2.4, 1.8, p.mouth);
  g.line(14, cy + 1, 16, cy + 2, '#00000088');
  g.line(16, cy + 2, 18, cy + 1, '#00000088');
  if (o.tongue !== false) g.rect(15, cy + 3, 2, 3, '#e8738c');
  blush(g, 8, 22, cy - 1, p.blush);
  applyAcc(g, o.acc, p, 5, cy + 8);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：兔 ----------

function drawRabbit(g, f, p, o) {
  var d = deform(f);
  var cy = 19 + d.dy;
  // 圆尾球
  g.ellipse(7, cy + 5, 2, 2, p.belly);
  // 长耳（圆头竖条 + 内耳）
  var ey = d.ear;
  g.rect(10, 2 + ey, 4, 11, p.ear);
  g.rect(18, 2 + ey, 4, 11, p.ear);
  g.ellipse(12, 5 + ey, 2, 2, p.ear);
  g.ellipse(20, 5 + ey, 2, 2, p.ear);
  g.rect(11, 4 + ey, 2, 8, p.earIn);
  g.rect(19, 4 + ey, 2, 8, p.earIn);
  // 身体（圆胖）
  g.ellipse(16, cy, 9 + chubX(o) + d.drx, 9 + d.dry, p.body);
  g.ellipse(16, cy + 4, 6, 4.5, p.belly);
  applyPattern(g, o, p, 10 + d.ear);
  // 脸：大眼 + 三瓣嘴
  eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  g.set(16, cy, p.blush); // 鼻
  g.line(14, cy + 1, 16, cy + 2, p.mouth);
  g.line(16, cy + 2, 18, cy + 1, p.mouth);
  g.set(16, cy + 2, p.mouth);
  blush(g, 8, 22, cy - 1, p.blush);
  applyAcc(g, o.acc, p, 2, cy + 7);
  g.outline(p.outline);
  shadow(g, 30, 8);
}

// ---------- 物种：奶龙（超圆蛋 + 奶角 + 无辜脸） ----------

function drawMilkDragon(g, f, p, o) {
  var d = deform(f);
  var cy = 18 + d.dy;
  // 奶角（头顶两只小圆角）
  g.ellipse(11, 8 + d.ear, 1.6, 2, p.earIn);
  g.ellipse(21, 8 + d.ear, 1.6, 2, p.earIn);
  g.set(11, 6 + d.ear, p.earIn);
  g.set(21, 6 + d.ear, p.earIn);
  // 身体：竖圆蛋
  g.ellipse(16, cy, 9.5 + d.drx, 10.5 + d.dry, p.body);
  // 奶油肚
  g.ellipse(16, cy + 4, 6, 5.5, p.belly);
  // 小短手
  g.ellipse(6, cy + 2, 2, 1.6, p.body);
  g.ellipse(26, cy + 2, 2, 1.6, p.body);
  // 小短脚
  g.ellipse(12, cy + 9, 2.2, 1.4, p.body);
  g.ellipse(20, cy + 9, 2.2, 1.4, p.body);
  // 无辜脸：大眼 + 微笑 + 大腮红
  eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  g.line(14, cy + 1, 16, cy + 2, p.mouth);
  g.line(16, cy + 2, 18, cy + 1, p.mouth);
  blush(g, 8, 22, cy - 1, p.blush);
  applyAcc(g, o.acc, p, 4, cy + 8);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：龙（经典 Q 版小飞龙） ----------

function drawDragon(g, f, p, o) {
  if (o.variant === 'milk') return drawMilkDragon(g, f, p, o);
  var d = deform(f);
  var cy = 19 + d.dy;
  // 卷卷尾
  g.line(24, cy + 4, 28, cy, p.body);
  g.ellipse(28, cy - 1, 1.6, 1.6, p.ear);
  // 小圆翅（Q 版化）
  var wy = cy - 3 + d.ear;
  g.ellipse(6, wy, 3, 4, p.acc);
  g.ellipse(26, wy, 3, 4, p.acc);
  // 小角
  var hy = 10 + d.ear;
  g.tri(12, hy, 10, hy - 5, 14, hy - 1, p.earIn);
  g.tri(20, hy, 22, hy - 5, 18, hy - 1, p.earIn);
  // 背鳍（头顶 2 个小三角）
  g.tri(14, hy - 2, 16, hy - 5, 16, hy - 2, p.acc);
  g.tri(16, hy - 2, 18, hy - 5, 18, hy - 2, p.acc);
  // 身体（圆胖）
  g.ellipse(16, cy, 9.5 + d.drx, 9 + d.dry, p.body);
  g.ellipse(16, cy + 4, 6, 4.5, p.belly);
  g.rect(12, cy + 2, 8, 1, P.shade(p.belly, 0.12));
  g.rect(12, cy + 4, 8, 1, P.shade(p.belly, 0.12));
  // 脸
  eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  catMouth(g, 16, cy + 1, p.blush, p.mouth);
  blush(g, 8, 22, cy - 1, p.blush);
  applyAcc(g, o.acc, p, 5, cy + 7);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：鸭（小黄鸭 / 摆烂鸭） ----------

function drawDuck(g, f, p, o) {
  var d = deform(f);
  var cy = 18 + d.dy;
  // 呆毛
  g.line(15, cy - 9 + d.ear, 13, cy - 12 + d.ear, p.body);
  g.line(16, cy - 9 + d.ear, 16, cy - 13 + d.ear, p.body);
  // 身体（圆）
  g.ellipse(16, cy, 9.5 + d.drx, 9 + d.dry, p.body);
  g.ellipse(16, cy + 4, 6, 4.5, p.belly);
  // 小翅膀
  g.ellipse(7, cy + 1, 2, 3.5, p.ear);
  g.ellipse(25, cy + 1, 2, 3.5, p.ear);
  // 扁嘴（上宽下窄双层）
  g.rect(12, cy - 1, 8, 2, p.mouth);
  g.rect(13, cy + 1, 6, 1, P.shade(p.mouth, -0.18));
  // 小脚
  g.line(13, cy + 9, 13, cy + 11, p.mouth);
  g.line(19, cy + 9, 19, cy + 11, p.mouth);
  // 眼（sleepy = 摆烂下垂眼皮）
  if (o.sleepy) {
    eyeOpen(g, 12, cy - 4, p.eye);
    eyeOpen(g, 20, cy - 4, p.eye);
    g.rect(11, cy - 5, 3, 2, p.body);
    g.rect(19, cy - 5, 3, 2, p.body);
    g.line(10, cy - 3, 13, cy - 3, '#00000055');
    g.line(19, cy - 3, 22, cy - 3, '#00000055');
  } else {
    eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  }
  blush(g, 8, 22, cy - 1, p.blush);
  applyAcc(g, o.acc, p, 5, cy + 8);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：鸟 ----------

function drawBird(g, f, p, o) {
  if (o.variant === 'duck') return drawDuck(g, f, p, o);
  var d = deform(f);
  var cy = 18 + d.dy;
  // 翅膀（贴身椭圆）
  var wy = cy - 1 + d.ear;
  g.ellipse(7, wy + 1, 2, 4, p.ear);
  g.ellipse(25, wy + 1, 2, 4, p.ear);
  // 身体（大圆）
  g.ellipse(16, cy, 10 + d.drx, 9 + d.dry, p.body);
  g.ellipse(16, cy + 3, 6.5, 4.5, p.belly);
  // 头顶呆毛
  g.line(15, cy - 9 + d.ear, 14, cy - 12 + d.ear, p.body);
  g.line(15, cy - 9 + d.ear, 17, cy - 12 + d.ear, p.body);
  // 喙
  g.tri(14, cy - 1, 18, cy - 1, 16, cy + 2, p.mouth);
  // 脚
  g.line(13, cy + 9, 13, cy + 11, p.mouth);
  g.line(19, cy + 9, 19, cy + 11, p.mouth);
  // 脸
  eyes(g, 12, 20, cy - 4, f === 2, p.eye);
  blush(g, 8, 22, cy, p.blush);
  applyPattern(g, o, p, 10 + d.ear);
  applyAcc(g, o.acc, p, 5, cy + 8);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：圆滚（仓鼠/汤圆系） ----------

function drawRound(g, f, p, o) {
  var d = deform(f);
  var cy = 19 + d.dy;
  // 小圆耳
  var ey = 10 + d.ear;
  g.ellipse(10, ey, 2.2, 2.2, p.ear);
  g.ellipse(22, ey, 2.2, 2.2, p.ear);
  g.ellipse(10, ey, 1, 1, p.earIn);
  g.ellipse(22, ey, 1, 1, p.earIn);
  // 超圆身体
  g.ellipse(16, cy, 10.5 + chubX(o) + d.drx, 9.5 + d.dry, p.body);
  g.ellipse(16, cy + 4, 6.5, 4.5, p.belly);
  // 短手
  g.ellipse(6, cy + 1, 2, 1.4, p.body);
  g.ellipse(26, cy + 1, 2, 1.4, p.body);
  applyPattern(g, o, p, 10 + d.ear);
  // 脸（大眼 + 大腮红）
  eyes(g, 12, 20, cy - 2, f === 2, p.eye);
  catMouth(g, 16, cy + 1, p.blush, p.mouth);
  blush(g, 7, 23, cy + 1, p.blush);
  applyAcc(g, o.acc, p, 5, cy + 8);
  g.outline(p.outline);
  shadow(g, 30, 9);
}

// ---------- 物种：水豚（卡皮巴拉，松弛感之王） ----------

function drawCapybara(g, f, p, o) {
  var d = deform(f);
  var cy = 19 + d.dy;
  // 耳朵：头顶两侧小圆
  g.ellipse(9, 11 + d.ear, 2, 2, p.ear);
  g.ellipse(23, 11 + d.ear, 2, 2, p.ear);
  // 身体：横向长圆（面包形）
  g.ellipse(16, cy, 11 + d.drx, 8.5 + d.dry, p.body);
  // 大鼻吻（中下浅色椭圆）
  g.ellipse(16, cy + 2, 6.5, 5, p.belly);
  // 鼻孔
  g.ellipse(13, cy + 1, 1.2, 1.6, '#00000066');
  g.ellipse(19, cy + 1, 1.2, 1.6, '#00000066');
  // 眯眯眼（chill ∪∪）
  eyesChill(g, 9, 23, cy - 4, p.eye);
  // 小手扒边
  g.ellipse(5, cy + 3, 2, 1.5, p.body);
  g.ellipse(27, cy + 3, 2, 1.5, p.body);
  // 腮红
  blush(g, 7, 23, cy, p.blush);
  applyAcc(g, o.acc, p, 7, cy + 7);
  g.outline(p.outline);
  shadow(g, 30, 10);
}

// ---------- 物种：鱼（咸鱼 / 锦鲤） ----------

function drawFish(g, f, p, o) {
  var d = deform(f);
  var cy = 17 + d.dy;
  // 尾鳍（左扇形）
  g.poly([[6, cy], [1, cy - 5], [2, cy], [1, cy + 5]], p.acc);
  // 背鳍
  g.tri(12, cy - 6, 16, cy - 10, 20, cy - 6, p.acc);
  // 腹鳍
  g.tri(13, cy + 5, 15, cy + 9, 17, cy + 5, p.acc);
  // 身体（横椭圆鱼雷）
  g.ellipse(16, cy, 10 + d.drx, 7 + d.dry, p.body);
  g.ellipse(16, cy + 2, 7, 3.5, p.belly);
  // 锦鲤斑
  if (o.pattern === 'spots') {
    g.ellipse(13, cy - 2, 3, 2.4, p.pattern);
    g.ellipse(19, cy + 1, 2.4, 2, p.pattern);
    g.ellipse(16, cy - 4, 2, 1.6, p.pattern);
  }
  // 脸：dead = 咸鱼死鱼眼 + 摆烂平嘴
  if (o.dead) {
    g.rect(19, cy - 2, 2, 2, p.eye);
    g.line(13, cy + 3, 17, cy + 3, p.mouth);
  } else {
    eyeOpen(g, 20, cy - 2, p.eye);
    g.line(23, cy + 1, 25, cy + 2, p.mouth);
    g.line(25, cy + 2, 23, cy + 3, p.mouth);
  }
  applyAcc(g, o.acc, p, 6, cy + 6);
  g.outline(p.outline);
  shadow(g, 30, 10);
}

// ---------- 物种：幻想系（按 variant 分发） ----------

function drawFantasy(g, f, p, o) {
  var v = o.variant || 'slime';
  var d = deform(f);
  var blink = f === 2;
  switch (v) {
    case 'slime': { // 史莱姆：水滴形 + 高光
      var cy = 19 + d.dy;
      g.ellipse(16, cy, 10 + d.drx, 8 + d.dry, p.body);
      g.ellipse(16, cy - 6 + d.ear, 4, 3, p.body);
      g.ellipse(11, cy - 3, 2, 3, '#ffffff55');
      eyes(g, 12, 20, cy - 2, blink, p.eye);
      g.line(15, cy + 2, 16, cy + 3, p.mouth);
      g.line(16, cy + 3, 17, cy + 2, p.mouth);
      g.outline(p.outline);
      shadow(g, 30, 9);
      break;
    }
    case 'ghost': { // 幽灵：圆头 + 波浪底
      var cy2 = 17 + d.dy;
      g.ellipse(16, cy2, 9.5, 8, p.body);
      g.rect(7, cy2, 18, 7, p.body);
      g.ellipse(10, cy2 + 7, 2.6, 2, p.body);
      g.ellipse(16, cy2 + 8, 2.6, 2, p.body);
      g.ellipse(22, cy2 + 7, 2.6, 2, p.body);
      eyes(g, 12, 20, cy2 - 1, blink, p.eye);
      g.ellipse(16, cy2 + 3, 2, 2, p.mouth);
      blush(g, 8, 22, cy2 + 1, p.blush + '88');
      g.outline(p.outline);
      shadow(g, 30, 8);
      break;
    }
    case 'star': { // 星辰：五角星
      var cy3 = 17 + d.dy;
      var pts = [];
      for (var i = 0; i < 10; i++) {
        var ang = -Math.PI / 2 + i * Math.PI / 5;
        var r = (i % 2 === 0) ? 12 : 5;
        pts.push([16 + Math.cos(ang) * r, cy3 + Math.sin(ang) * r]);
      }
      g.poly(pts, p.body);
      eyes(g, 13, 19, cy3 - 2, blink, p.eye);
      g.line(15, cy3 + 2, 16, cy3 + 3, p.mouth);
      g.line(16, cy3 + 3, 17, cy3 + 2, p.mouth);
      blush(g, 10, 21, cy3 + 1, p.blush + '66');
      g.outline(p.outline);
      shadow(g, 30, 7);
      break;
    }
    case 'cloud': { // 云朵：三圆合并
      var cy4 = 17 + d.dy;
      g.ellipse(10, cy4 + 2, 5.5, 4, p.body);
      g.ellipse(16, cy4 - 1, 6.5, 5, p.body);
      g.ellipse(22, cy4 + 2, 5.5, 4, p.body);
      g.rect(10, cy4 + 2, 12, 4, p.body);
      eyes(g, 12, 20, cy4, blink, p.eye);
      g.line(15, cy4 + 3, 16, cy4 + 4, p.mouth);
      g.line(16, cy4 + 4, 17, cy4 + 3, p.mouth);
      blush(g, 8, 22, cy4 + 2, p.blush + '66');
      g.outline(p.outline);
      shadow(g, 30, 8);
      break;
    }
    case 'lava': { // 岩浆：圆身 + 裂纹
      var cy5 = 18 + d.dy;
      g.ellipse(16, cy5, 10.5 + d.drx, 9.5 + d.dry, p.body);
      g.line(10, cy5 - 2, 13, cy5 + 1, p.pattern);
      g.line(13, cy5 + 1, 12, cy5 + 4, p.pattern);
      g.line(20, cy5 - 4, 22, cy5 - 1, p.pattern);
      g.line(22, cy5 - 1, 25, cy5 + 1, p.pattern);
      eyes(g, 12, 20, cy5 - 2, blink, p.eye);
      g.line(14, cy5 + 3, 18, cy5 + 3, p.mouth);
      g.outline(p.outline);
      shadow(g, 30, 9);
      break;
    }
    case 'ice': { // 寒冰：六边形棱晶
      var cy6 = 17 + d.dy;
      g.poly([[16, cy6 - 11], [25, cy6 - 5], [25, cy6 + 5], [16, cy6 + 10], [7, cy6 + 5], [7, cy6 - 5]], p.body);
      g.poly([[16, cy6 - 7], [21, cy6 - 3], [16, cy6 + 1], [11, cy6 - 3]], p.belly);
      eyes(g, 13, 19, cy6 - 1, blink, p.eye);
      g.line(15, cy6 + 3, 16, cy6 + 4, p.mouth);
      g.line(16, cy6 + 4, 17, cy6 + 3, p.mouth);
      g.outline(p.outline);
      shadow(g, 30, 7);
      break;
    }
    case 'robo': { // 机械：方身 + 天线 + LED 眼
      var cy7 = 18 + d.dy;
      g.line(16, 9 + d.ear, 16, 6 + d.ear, p.acc);
      g.ellipse(16, 5 + d.ear, 1.4, 1.4, p.acc2 || '#ff5252');
      g.rect(7, 10 + d.ear, 18, 16, p.body);
      g.rect(9, 12 + d.ear, 14, 12, p.belly);
      if (blink) {
        g.rect(10, cy7 - 2, 4, 1, p.eye);
        g.rect(18, cy7 - 2, 4, 1, p.eye);
      } else {
        g.rect(10, cy7 - 3, 4, 3, p.eye);
        g.rect(18, cy7 - 3, 4, 3, p.eye);
        g.set(11, cy7 - 3, '#ffffffaa');
        g.set(19, cy7 - 3, '#ffffffaa');
      }
      g.rect(13, cy7 + 2, 6, 1, p.mouth);
      g.rect(14, cy7 + 4, 4, 1, p.mouth);
      g.rect(5, 16, 2, 4, p.acc);
      g.rect(25, 16, 2, 4, p.acc);
      g.outline(p.outline);
      shadow(g, 30, 9);
      break;
    }
    default:
      drawRound(g, f, p, o);
  }
}

// ---------- 分发 ----------

var SPECIES = {
  cat: drawCat,
  dog: drawDog,
  rabbit: drawRabbit,
  dragon: drawDragon,
  bird: drawBird,
  round: drawRound,
  capybara: drawCapybara,
  fish: drawFish,
  fantasy: drawFantasy
};

function drawSpecies(g, species, frame, pal, opts) {
  var fn = SPECIES[species] || drawRound;
  fn(g, frame, pal, opts || {});
}

module.exports = { drawSpecies, SPECIES };
