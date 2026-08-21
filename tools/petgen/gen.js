/* ===== tools/petgen/gen.js =====
 * 生成器入口：node tools/petgen/gen.js
 * 输出：
 *   web/img/pet/skins/{id}.png   每款一张精灵图（4 帧横排 256×64，逻辑帧 32 放大 2×）
 *   web/img/pet/skins/skins.json 皮肤清单（id/名称/分类/解锁等级/帧数/fps）
 */

'use strict';

var fs = require('fs');
var path = require('path');
var shapes = require('./shapes');
var species = require('./species');
var SKINS = require('./skins').SKINS;

var FRAME = 32;    // 逻辑帧尺寸 32×32（species.js 全部坐标按 32 设计）
var SCALE = 2;     // 输出放大倍数：每逻辑像素 → SCALE×SCALE，像素更高更细腻
var OUT_FRAME = FRAME * SCALE; // 64
var FRAMES = 4;    // 每款 4 帧
var OUT_DIR = path.join(__dirname, '..', '..', 'web', 'img', 'pet', 'skins');

// blitScaled 把 src 逻辑像素放大 SCALE 倍后复制到 dst 的 (ox, oy)。
function blitScaled(dst, src, ox, oy, scale) {
  for (var y = 0; y < src.h; y++) {
    for (var x = 0; x < src.w; x++) {
      var p = src.get(x, y);
      if (!p || p[3] === 0) continue;
      for (var sy = 0; sy < scale; sy++) {
        for (var sx = 0; sx < scale; sx++) {
          dst.set(ox + x * scale + sx, oy + y * scale + sy,
            'rgba(' + p[0] + ',' + p[1] + ',' + p[2] + ',' + (p[3] / 255) + ')');
        }
      }
    }
  }
}

function main() {
  fs.mkdirSync(OUT_DIR, { recursive: true });

  var manifest = [];
  SKINS.forEach(function (s) {
    // 一张精灵图 = FRAMES 帧横排（每帧 OUT_FRAME×OUT_FRAME）
    var sheet = new shapes.Grid(OUT_FRAME * FRAMES, OUT_FRAME);
    for (var f = 0; f < FRAMES; f++) {
      var g = new shapes.Grid(FRAME, FRAME);
      species.drawSpecies(g, s.species, f, s.pal, s.opts);
      blitScaled(sheet, g, f * OUT_FRAME, 0, SCALE);
    }
    var png = shapes.encodePNG(sheet);
    var file = s.id + '.png';
    fs.writeFileSync(path.join(OUT_DIR, file), png);
    manifest.push({
      id: s.id,
      name: s.name,
      category: s.category,
      unlock: s.unlock,
      frames: FRAMES,
      fps: 6
    });
    console.log('✓ ' + file + ' (' + png.length + ' bytes) ' + s.name);
  });

  var json = {
    version: 2,
    frameSize: OUT_FRAME,
    frames: FRAMES,
    fps: 6,
    skins: manifest
  };
  fs.writeFileSync(
    path.join(OUT_DIR, 'skins.json'),
    JSON.stringify(json, null, 2) + '\n'
  );
  console.log('');
  console.log('共 ' + manifest.length + ' 款皮肤 → ' + OUT_DIR);
  console.log('清单 → skins.json');
}

main();
