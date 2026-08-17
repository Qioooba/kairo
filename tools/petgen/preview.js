/* ===== tools/petgen/preview.js =====
 * 生成拼图预览：50 款皮肤的 frame0（放大 4×，10 列 × 5 行）→ preview.png
 * 用于人工目检生成质量。
 */
'use strict';

var fs = require('fs');
var path = require('path');
var shapes = require('./shapes');
var species = require('./species');
var SKINS = require('./skins').SKINS;

var CELL = 32, SCALE = 4, COLS = 10;

function main() {
  var rows = Math.ceil(SKINS.length / COLS);
  var W = COLS * CELL * SCALE, H = rows * CELL * SCALE;
  var g = new shapes.Grid(W, H);
  // 棋盘底色（透明区可见）
  for (var y = 0; y < H; y++) {
    for (var x = 0; x < W; x++) {
      var ck = ((x >> 4) + (y >> 4)) % 2 === 0;
      g.set(x, y, ck ? '#d8d8d8' : '#c4c4c4');
    }
  }
  SKINS.forEach(function (s, i) {
    var fg = new shapes.Grid(CELL, CELL);
    species.drawSpecies(fg, s.species, 0, s.pal, s.opts);
    var ox = (i % COLS) * CELL * SCALE, oy = Math.floor(i / COLS) * CELL * SCALE;
    for (var yy = 0; yy < CELL; yy++) {
      for (var xx = 0; xx < CELL; xx++) {
        var p = fg.get(xx, yy);
        if (!p || p[3] === 0) continue;
        for (var sy = 0; sy < SCALE; sy++) {
          for (var sx = 0; sx < SCALE; sx++) {
            g.set(ox + xx * SCALE + sx, oy + yy * SCALE + sy,
              'rgba(' + p[0] + ',' + p[1] + ',' + p[2] + ',' + (p[3] / 255) + ')');
          }
        }
      }
    }
  });
  fs.writeFileSync(path.join(__dirname, 'preview.png'), shapes.encodePNG(g));
  console.log('预览图 → tools/petgen/preview.png (' + W + '×' + H + ')');
}
main();
