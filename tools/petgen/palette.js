/* ===== tools/petgen/palette.js =====
 * 调色板与配色工具：颜色加深/提亮（像素画的阴影与高光）。
 */

'use strict';

function clamp255(v) { return Math.max(0, Math.min(255, Math.round(v))); }

// shade 颜色加深（f=0.2 → 降 20%）
function shade(hex, f) {
  var h = hex.replace('#', '');
  var r = parseInt(h.slice(0, 2), 16);
  var g = parseInt(h.slice(2, 4), 16);
  var b = parseInt(h.slice(4, 6), 16);
  r = clamp255(r * (1 - f)); g = clamp255(g * (1 - f)); b = clamp255(b * (1 - f));
  return '#' + [r, g, b].map(function (v) { return v.toString(16).padStart(2, '0'); }).join('');
}

// tint 颜色提亮（f=0.2 → 升 20% 向白靠拢）
function tint(hex, f) {
  var h = hex.replace('#', '');
  var r = parseInt(h.slice(0, 2), 16);
  var g = parseInt(h.slice(2, 4), 16);
  var b = parseInt(h.slice(4, 6), 16);
  r = clamp255(r + (255 - r) * f); g = clamp255(g + (255 - g) * f); b = clamp255(b + (255 - b) * f);
  return '#' + [r, g, b].map(function (v) { return v.toString(16).padStart(2, '0'); }).join('');
}

// 通用描边色（按体色自动加深）
function outlineFor(body) { return shade(body, 0.55); }

module.exports = { shade, tint, outlineFor };
