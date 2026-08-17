/* ===== tools/petgen/shapes.js =====
 * 像素绘制库：Grid 光栅化原语 + 零依赖 PNG 编码（Node 内置 zlib）。
 * 所有坐标为整数像素；颜色为 #RRGGBB / #RRGGBBAA。
 */

'use strict';

// ---------- 颜色 ----------

function parseColor(hex) {
  var s = String(hex || '#000000').trim();
  // transparent → 全透明
  if (s === 'transparent') return [0, 0, 0, 0];
  // rgba(r,g,b,a)（a 为 0~1 浮点）
  var m = s.match(/^rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+)\s*)?\)$/);
  if (m) {
    var af = m[4] === undefined ? 1 : parseFloat(m[4]);
    return [Math.round(+m[1]), Math.round(+m[2]), Math.round(+m[3]), Math.round(Math.max(0, Math.min(1, af)) * 255)];
  }
  var h = s.replace('#', '');
  if (h.length === 3) {
    h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2];
  }
  var r = parseInt(h.slice(0, 2), 16);
  var g = parseInt(h.slice(2, 4), 16);
  var b = parseInt(h.slice(4, 6), 16);
  var a = h.length >= 8 ? parseInt(h.slice(6, 8), 16) : 255;
  if (isNaN(r) || isNaN(g) || isNaN(b)) return [0, 0, 0, 0];
  return [r, g, b, a];
}

// ---------- Grid ----------

// Grid W×H 像素网格，data 为 RGBA 行主序。
function Grid(w, h) {
  this.w = w;
  this.h = h;
  this.data = new Uint8Array(w * h * 4);
}

Grid.prototype.set = function (x, y, c) {
  x = Math.round(x); y = Math.round(y);
  if (x < 0 || y < 0 || x >= this.w || y >= this.h) return;
  var i = (y * this.w + x) * 4;
  var p = parseColor(c);
  // alpha=0 视为擦除（transparent 挖孔语义）
  if (p[3] === 0) {
    this.data[i] = 0; this.data[i + 1] = 0;
    this.data[i + 2] = 0; this.data[i + 3] = 0;
    return;
  }
  // 半透明色做 alpha 混合（p[3]<255 时）
  if (p[3] < 255 && this.data[i + 3] > 0) {
    var da = this.data[i + 3] / 255;
    var sa = p[3] / 255;
    var oa = sa + da * (1 - sa);
    if (oa > 0) {
      this.data[i] = Math.round((p[0] * sa + this.data[i] * da * (1 - sa)) / oa);
      this.data[i + 1] = Math.round((p[1] * sa + this.data[i + 1] * da * (1 - sa)) / oa);
      this.data[i + 2] = Math.round((p[2] * sa + this.data[i + 2] * da * (1 - sa)) / oa);
      this.data[i + 3] = Math.round(oa * 255);
      return;
    }
  }
  this.data[i] = p[0]; this.data[i + 1] = p[1];
  this.data[i + 2] = p[2]; this.data[i + 3] = p[3];
};

Grid.prototype.get = function (x, y) {
  x = Math.round(x); y = Math.round(y);
  if (x < 0 || y < 0 || x >= this.w || y >= this.h) return null;
  var i = (y * this.w + x) * 4;
  return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
};

Grid.prototype.alphaAt = function (x, y) {
  var p = this.get(x, y);
  return p ? p[3] : 0;
};

// rect 填充矩形
Grid.prototype.rect = function (x, y, w, h, c) {
  for (var j = 0; j < h; j++) {
    for (var i = 0; i < w; i++) this.set(x + i, y + j, c);
  }
};

// ellipse 实心椭圆（中点圆算法的椭圆推广）
Grid.prototype.ellipse = function (cx, cy, rx, ry, c) {
  for (var y = Math.floor(cy - ry); y <= Math.ceil(cy + ry); y++) {
    for (var x = Math.floor(cx - rx); x <= Math.ceil(cx + rx); x++) {
      var dx = (x - cx) / rx;
      var dy = (y - cy) / ry;
      if (dx * dx + dy * dy <= 1.05) this.set(x, y, c);
    }
  }
};

// tri 实心三角形（扫描线填充，边上点全部包含）
Grid.prototype.tri = function (x1, y1, x2, y2, x3, y3, c) {
  var minY = Math.floor(Math.min(y1, y2, y3));
  var maxY = Math.ceil(Math.max(y1, y2, y3));
  var minX = Math.floor(Math.min(x1, x2, x3));
  var maxX = Math.ceil(Math.max(x1, x2, x3));
  function edge(ax, ay, bx, by, px, py) {
    return (bx - ax) * (py - ay) - (by - ay) * (px - ax);
  }
  for (var y = minY; y <= maxY; y++) {
    for (var x = minX; x <= maxX; x++) {
      var d1 = edge(x1, y1, x2, y2, x + 0.5, y + 0.5);
      var d2 = edge(x2, y2, x3, y3, x + 0.5, y + 0.5);
      var d3 = edge(x3, y3, x1, y1, x + 0.5, y + 0.5);
      var neg = (d1 < 0) || (d2 < 0) || (d3 < 0);
      var pos = (d1 > 0) || (d2 > 0) || (d3 > 0);
      if (!(neg && pos)) this.set(x, y, c);
    }
  }
};

// line Bresenham 直线（宽 1px）
Grid.prototype.line = function (x1, y1, x2, y2, c) {
  x1 = Math.round(x1); y1 = Math.round(y1);
  x2 = Math.round(x2); y2 = Math.round(y2);
  var dx = Math.abs(x2 - x1), dy = Math.abs(y2 - y1);
  var sx = x1 < x2 ? 1 : -1, sy = y1 < y2 ? 1 : -1;
  var err = dx - dy;
  for (;;) {
    this.set(x1, y1, c);
    if (x1 === x2 && y1 === y2) break;
    var e2 = 2 * err;
    if (e2 > -dy) { err -= dy; x1 += sx; }
    if (e2 < dx) { err += dx; y1 += sy; }
  }
};

// poly 实心多边形（顶点数组 + 颜色）
Grid.prototype.poly = function (pts, c) {
  if (pts.length < 3) return;
  for (var i = 1; i < pts.length - 1; i++) {
    this.tri(pts[0][0], pts[0][1], pts[i][0], pts[i][1], pts[i + 1][0], pts[i + 1][1], c);
  }
};

// outline 描边：给所有 alpha>0 的像素外扩一圈颜色
Grid.prototype.outline = function (c) {
  var edges = [];
  for (var y = 0; y < this.h; y++) {
    for (var x = 0; x < this.w; x++) {
      if (this.alphaAt(x, y) === 0) continue;
      if (this.alphaAt(x - 1, y) === 0 || this.alphaAt(x + 1, y) === 0 ||
          this.alphaAt(x, y - 1) === 0 || this.alphaAt(x, y + 1) === 0) {
        edges.push([x, y]);
      }
    }
  }
  for (var i = 0; i < edges.length; i++) this.set(edges[i][0], edges[i][1], c);
};

// replaceColor 把 from 颜色像素替换为 to（用于阴影调色技巧）
Grid.prototype.replaceColor = function (from, to) {
  var f = parseColor(from);
  for (var i = 0; i < this.data.length; i += 4) {
    if (this.data[i] === f[0] && this.data[i + 1] === f[1] &&
        this.data[i + 2] === f[2] && this.data[i + 3] === f[3]) {
      var t = parseColor(to);
      this.data[i] = t[0]; this.data[i + 1] = t[1];
      this.data[i + 2] = t[2]; this.data[i + 3] = t[3];
    }
  }
};

// ---------- PNG 编码（零依赖） ----------

var CRC_TABLE = (function () {
  var t = new Uint32Array(256);
  for (var n = 0; n < 256; n++) {
    var c = n;
    for (var k = 0; k < 8; k++) c = (c & 1) ? (0xedb88320 ^ (c >>> 1)) : (c >>> 1);
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(buf) {
  var c = 0xffffffff;
  for (var i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  var out = Buffer.alloc(8 + data.length + 4);
  out.writeUInt32BE(data.length, 0);
  out.write(type, 4, 'ascii');
  data.copy(out, 8);
  var crcInput = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  out.writeUInt32BE(crc32(crcInput), 8 + data.length);
  return out;
}

// encodePNG 把 Grid 编码为 PNG Buffer（RGBA，filter 0）
function encodePNG(g) {
  var zlib = require('zlib');
  var raw = Buffer.alloc(g.h * (1 + g.w * 4));
  var o = 0;
  for (var y = 0; y < g.h; y++) {
    raw[o++] = 0; // filter: none
    for (var x = 0; x < g.w; x++) {
      var p = g.get(x, y);
      raw[o++] = p[0]; raw[o++] = p[1]; raw[o++] = p[2]; raw[o++] = p[3];
    }
  }
  var ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(g.w, 0);
  ihdr.writeUInt32BE(g.h, 4);
  ihdr[8] = 8;  // bit depth
  ihdr[9] = 6;  // color type: RGBA
  ihdr[10] = 0; ihdr[11] = 0; ihdr[12] = 0;
  var sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  return Buffer.concat([
    sig,
    chunk('IHDR', ihdr),
    chunk('IDAT', zlib.deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0))
  ]);
}

module.exports = { Grid, encodePNG, parseColor };
