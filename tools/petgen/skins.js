/* ===== tools/petgen/skins.js =====
 * 62 款皮肤定义表：id / 名称 / 物种 / 分类 / 解锁等级 / 调色板 / 选项。
 * 含热门梗宠系列：奶龙系（dragon variant=milk）/ 噜噜（猫）/ 黄油小猫 /
 * 水豚系（capybara）/ 小黄鸭·摆烂鸭（bird variant=duck）/ 咸鱼·锦鲤（fish）。
 * 新增皮肤：在 SKINS 数组加一条 → node gen.js 重新生成即可。
 */

'use strict';

var P = require('./palette');

// base 默认调色板（各皮肤按需覆盖字段）
function base() {
  return {
    body: '#f5a34a', belly: '#fff4e6', ear: '#f5a34a', earIn: '#f7c0b0',
    eye: '#33261a', blush: '#f4938c', mouth: '#8a6a5a',
    pattern: '#d97c1e', acc: '#e05a6a', acc2: '#ffffff',
    outline: '#3a2a1a'
  };
}

// mk 快捷构造：overrides 覆盖默认调色板；outline 默认按 body 深化
function mk(id, name, species, category, unlock, pal, opts) {
  var p = base();
  for (var k in (pal || {})) p[k] = pal[k];
  if (!pal || !pal.outline) p.outline = P.outlineFor(p.body);
  return {
    id: id, name: name, species: species, category: category,
    unlock: unlock, pal: p, opts: opts || {}
  };
}

var SKINS = [
  // ---- 猫系 ----
  mk('orange-cat', '橘座', 'cat', 'cat', 1,
    { pattern: '#d97c1e' }, { pattern: 'stripes' }),
  mk('blue-cat', '蓝猫', 'cat', 'cat', 1,
    { body: '#8fa2b8', ear: '#7a8da4', belly: '#e8edf2', earIn: '#f0b8c0', pattern: '#7a8da4' }),
  mk('cow-cat', '奶牛', 'cat', 'cat', 4,
    { body: '#f5f5f5', ear: '#efefef', belly: '#ffffff', pattern: '#383838', outline: '#2e2e2e' },
    { pattern: 'spots' }),
  mk('calico-cat', '三花', 'cat', 'cat', 4,
    { body: '#f0e0c8', belly: '#fff8ec', pattern: '#e8874a' },
    { pattern: 'spots' }),
  mk('black-cat', '玄猫', 'cat', 'cat', 8,
    { body: '#3a3a46', ear: '#34343e', belly: '#52525e', earIn: '#5a4a52', eye: '#ffd23e', pattern: '#34343e' }),
  mk('fairy-cat', '仙女猫', 'cat', 'cat', 8,
    { body: '#f7d4e0', ear: '#f2c4d6', belly: '#fffafc', earIn: '#f8b8cc', pattern: '#ef9ec4', acc: '#e86a9a' },
    { pattern: 'gradient', acc: 'bow' }),
  mk('point-cat', '重点色', 'cat', 'cat', 8,
    { body: '#ece2d4', ear: '#8a6d52', belly: '#f6f0e6', earIn: '#c8a88a', eye: '#4a90d9', pattern: '#8a6d52', mouth: '#6a5340' }),
  mk('lulu', '噜噜', 'cat', 'cat', 1,
    { body: '#f0993c', ear: '#e0882c', belly: '#fdf0dc', pattern: '#d97c1e' },
    { pattern: 'stripes', chub: true }),
  mk('butter-cat', '黄油小猫', 'cat', 'cat', 8,
    { body: '#f5d76e', ear: '#e8c85e', belly: '#fff6d0', pattern: '#e8c85e' },
    { acc: 'butter' }),
  // ---- 犬系 ----
  mk('shiba', '柴柴', 'dog', 'dog', 1,
    { body: '#e8963e', ear: '#d97c2a', belly: '#fff7ea', earIn: '#f0c8a8', pattern: '#d97c2a' }),
  mk('corgi', '柯基', 'dog', 'dog', 4,
    { body: '#e8963e', ear: '#e08a30', belly: '#fff7ea', earIn: '#f0c8a8', pattern: '#fff7ea' },
    { pattern: 'gradient' }),
  mk('husky', '二哈', 'dog', 'dog', 4,
    { body: '#9aa8b8', ear: '#7a8898', belly: '#f4f6f8', earIn: '#c8d0d8', eye: '#4aa3d8', pattern: '#7a8898' }),
  mk('golden', '金毛', 'dog', 'dog', 8,
    { body: '#e0b356', ear: '#d0a046', belly: '#f8ecd0', earIn: '#f0d8a8', pattern: '#f0cf8a' },
    { pattern: 'gradient' }),
  mk('border', '边牧', 'dog', 'dog', 8,
    { body: '#2e2e38', ear: '#26262e', belly: '#f4f4f8', earIn: '#4a4a58', eye: '#4aa3d8', pattern: '#26262e', outline: '#1a1a22' }),
  mk('teddy', '泰迪', 'dog', 'dog', 8,
    { body: '#9a6b4a', ear: '#8a5c3e', belly: '#c89a70', earIn: '#b8876a', pattern: '#8a5c3e' },
    { pattern: 'spots' }),
  mk('frenchie', '法斗', 'dog', 'dog', 8,
    { body: '#c8c4be', ear: '#8a8680', belly: '#e8e4de', earIn: '#a8a49e', pattern: '#8a8680' }),
  // ---- 兔系 ----
  mk('snow-bunny', '白团', 'rabbit', 'rabbit', 1,
    { body: '#ffffff', ear: '#f4f4f4', belly: '#ffffff', earIn: '#f7c8d0', pattern: '#f4f4f4', outline: '#c8c4c8' }),
  mk('lop-ear', '垂垂', 'rabbit', 'rabbit', 4,
    { body: '#f0c4cf', ear: '#e8b4c0', belly: '#fdf2f5', earIn: '#f8d0d8', pattern: '#e8b4c0' }),
  mk('gray-bunny', '灰灰', 'rabbit', 'rabbit', 4,
    { body: '#b8bec8', ear: '#a8aeb8', belly: '#e8ecf0', earIn: '#d8c0c8', pattern: '#a8aeb8' }),
  mk('sakura-bunny', '樱花兔', 'rabbit', 'rabbit', 8,
    { body: '#fbe4ec', ear: '#f8d4e0', belly: '#fffafc', earIn: '#f8c0d0', pattern: '#f0a8c0', acc: '#f28ab0' },
    { acc: 'flower' }),
  mk('moon-rabbit', '月兔', 'rabbit', 'rabbit', 12,
    { body: '#e8ecf4', ear: '#dce2ec', belly: '#f8fafc', earIn: '#d0c8e0', pattern: '#dce2ec' },
    { acc: 'moon' }),
  mk('coffee-bunny', '咖啡兔', 'rabbit', 'rabbit', 8,
    { body: '#a58262', ear: '#98755a', belly: '#d8c0a8', earIn: '#c8a088', pattern: '#98755a' }),
  mk('mint-bunny', '薄荷兔', 'rabbit', 'rabbit', 8,
    { body: '#b8e6d0', ear: '#a8d8c2', belly: '#e8f8f0', earIn: '#d0f0e0', pattern: '#a8d8c2' }),
  // ---- 龙系（奶龙系列 Lv1 起 + 经典龙 Lv8 起） ----
  mk('nai-long', '奶龙', 'dragon', 'dragon', 1,
    { body: '#ffdf70', ear: '#ffdf70', belly: '#fff8dc', earIn: '#fff1b8', blush: '#ff9eb0', mouth: '#b8863f', outline: '#c9a23f' },
    { variant: 'milk' }),
  mk('berry-nai', '草莓奶龙', 'dragon', 'dragon', 8,
    { body: '#ffb0c8', ear: '#ffb0c8', belly: '#ffe8f0', earIn: '#ffd9e6', blush: '#ff7fa0', mouth: '#c46a86', outline: '#d986a0' },
    { variant: 'milk' }),
  mk('matcha-nai', '抹茶奶龙', 'dragon', 'dragon', 8,
    { body: '#c3dd9a', ear: '#c3dd9a', belly: '#eef6da', earIn: '#e0f0c0', blush: '#e8a8b0', mouth: '#8a9a5a', outline: '#a0b87a' },
    { variant: 'milk' }),
  mk('green-dragon', '青龙', 'dragon', 'dragon', 8,
    { body: '#6db86a', ear: '#5aa85a', belly: '#d8efc8', earIn: '#f0e0b0', eye: '#2e5a2e', acc: '#4a9a52' }),
  mk('red-dragon', '红龙', 'dragon', 'dragon', 12,
    { body: '#e05a4a', ear: '#c84a3c', belly: '#f8d8c8', earIn: '#f0e0b0', acc: '#c03a30' }),
  mk('blue-dragon', '蓝龙', 'dragon', 'dragon', 12,
    { body: '#4a8ad8', ear: '#3a78c4', belly: '#d8ecf8', earIn: '#f0e0b0', acc: '#2f6ab0' }),
  mk('night-wing', '夜翼', 'dragon', 'dragon', 16,
    { body: '#3a3450', ear: '#322e46', belly: '#544c70', earIn: '#8a7ab0', eye: '#8ad8f0', acc: '#5a4a80', outline: '#221e32' }),
  mk('frost-dragon', '冰霜龙', 'dragon', 'dragon', 16,
    { body: '#a8d8f0', ear: '#98c8e4', belly: '#e8f6ff', earIn: '#f0f8ff', eye: '#2a6a9a', acc: '#78b8e0' }),
  mk('flame-dragon', '烈焰龙', 'dragon', 'dragon', 16,
    { body: '#f08030', ear: '#e07020', belly: '#fcd8a0', earIn: '#f8e8b0', pattern: '#f8b060', acc: '#d85020' },
    { pattern: 'gradient' }),
  mk('rainbow-dragon', '彩虹龙', 'dragon', 'dragon', 16,
    { body: '#e878c0', ear: '#d868b0', belly: '#fcd8f0', earIn: '#f8e0b0', acc: '#78c8e8' }),
  // ---- 水豚系（卡皮巴拉，松弛感之王） ----
  mk('capybara', '卡皮巴拉', 'capybara', 'capybara', 1,
    { body: '#a97c50', ear: '#8a6440', belly: '#c9a274', blush: '#d89868', eye: '#3a2a1a', outline: '#6b4a2c' }),
  mk('orange-capy', '顶橘豚', 'capybara', 'capybara', 8,
    { body: '#a97c50', ear: '#8a6440', belly: '#c9a274', blush: '#d89868', eye: '#3a2a1a', outline: '#6b4a2c' },
    { acc: 'orange' }),
  mk('spa-capy', '泡汤豚', 'capybara', 'capybara', 12,
    { body: '#a97c50', ear: '#8a6440', belly: '#c9a274', blush: '#d89868', eye: '#3a2a1a', outline: '#6b4a2c' },
    { acc: 'towel' }),
  // ---- 鱼系 ----
  mk('salted-fish', '咸鱼', 'fish', 'fish', 1,
    { body: '#b0bca8', belly: '#d0d8c4', acc: '#98a68e', eye: '#3a4234', mouth: '#6a7462', blush: '#c8a8a0', outline: '#7a8670' },
    { dead: true }),
  mk('koi', '锦鲤', 'fish', 'fish', 12,
    { body: '#f4f4f4', belly: '#ffffff', pattern: '#e8604a', acc: '#e8604a', eye: '#3a3a3a', mouth: '#c87a60', blush: '#f0a890', outline: '#c8b8a8' },
    { pattern: 'spots' }),
  // ---- 鸟系（Lv4 起） ----
  mk('chick', '团子鸡', 'bird', 'bird', 4,
    { body: '#ffd94a', ear: '#f0c030', belly: '#fce8a8', mouth: '#f09a30', pattern: '#f0c030' }),
  mk('parrot', '鹦鹉', 'bird', 'bird', 4,
    { body: '#e85d4a', ear: '#4aa860', belly: '#f8c832', mouth: '#4a4a52', pattern: '#d84a38' }),
  mk('sleepy-bird', '呆萌鸟', 'bird', 'bird', 8,
    { body: '#a8b8d8', ear: '#8898b8', belly: '#e0e8f4', mouth: '#f0a030', pattern: '#8898b8' }),
  mk('bluebird', '蓝雀', 'bird', 'bird', 8,
    { body: '#5a9ad8', ear: '#4a8ac4', belly: '#ffffff', mouth: '#f0a030', pattern: '#4a8ac4' }),
  mk('skylark', '云雀', 'bird', 'bird', 12,
    { body: '#c8a878', ear: '#b09468', belly: '#f0e8d8', mouth: '#e08840', pattern: '#b09468' }),
  mk('penguin', '企鹅', 'bird', 'bird', 12,
    { body: '#3a4252', ear: '#2a313e', belly: '#ffffff', mouth: '#f0a030', pattern: '#2a313e', outline: '#1e242e' }),
  mk('owl', '猫头鹰', 'bird', 'bird', 12,
    { body: '#a8805a', ear: '#98704a', belly: '#d8c0a0', mouth: '#f0a030', pattern: '#98704a' }),
  mk('yellow-duck', '小黄鸭', 'bird', 'bird', 4,
    { body: '#ffd94a', ear: '#f0c030', belly: '#fce8a0', mouth: '#f08a30', blush: '#f8a868' },
    { variant: 'duck' }),
  mk('lazy-duck', '摆烂鸭', 'bird', 'bird', 12,
    { body: '#d8d4cc', ear: '#c4c0b8', belly: '#eeeae2', mouth: '#e8a050', blush: '#e0a8a0' },
    { variant: 'duck', sleepy: true }),
  // ---- 圆滚系 ----
  mk('hamster', '仓鼠', 'round', 'round', 1,
    { body: '#f0c060', ear: '#e0b050', belly: '#fff8ec', earIn: '#f0c8c0', pattern: '#e0b050' }),
  mk('mochi-ball', '汤圆', 'round', 'round', 1,
    { body: '#fdf6ec', ear: '#f4ece0', belly: '#ffffff', earIn: '#f7c8d0', pattern: '#f4ece0', outline: '#d8cfc4' }),
  mk('bread', '面包', 'round', 'round', 8,
    { body: '#f0d8a8', ear: '#e8c890', belly: '#fdf2dc', earIn: '#f0c8c0', pattern: '#e8b878' },
    { pattern: 'gradient' }),
  mk('dango', '饭团', 'round', 'round', 12,
    { body: '#ffffff', ear: '#3a4a3a', belly: '#ffffff', earIn: '#3a4a3a', pattern: '#f0f0f0', outline: '#2e3a2e' }),
  mk('pudding', '布丁', 'round', 'round', 12,
    { body: '#f8d878', ear: '#f0c860', belly: '#fce8b0', earIn: '#f0c8c0', pattern: '#c8823c' },
    { pattern: 'gradient' }),
  mk('snowball', '雪球', 'round', 'round', 8,
    { body: '#ffffff', ear: '#f4f4f8', belly: '#ffffff', earIn: '#f7c8d0', pattern: '#f4f4f8', acc: '#e05a6a', outline: '#c8ccd8' },
    { acc: 'scarf' }),
  mk('matcha', '麻薯', 'round', 'round', 12,
    { body: '#b8d0a0', ear: '#a8c090', belly: '#e0ecc8', earIn: '#d8e8c0', pattern: '#a8c090' }),
  // ---- 幻想系（Lv16 起，史莱姆默认） ----
  mk('slime', '史莱姆', 'fantasy', 'fantasy', 1,
    { body: '#7fd86a', belly: '#a8e898', eye: '#2e5a26', pattern: '#6ac85a', outline: '#3a7a2e' },
    { variant: 'slime' }),
  mk('ghost', '幽灵', 'fantasy', 'fantasy', 12,
    { body: '#f4f6fa', belly: '#ffffff', eye: '#3a3a4a', mouth: '#7a7a8a', pattern: '#f4f6fa', outline: '#b8c0d0' },
    { variant: 'ghost' }),
  mk('star', '星辰', 'fantasy', 'fantasy', 16,
    { body: '#ffd23e', belly: '#ffe88a', eye: '#8a5a10', mouth: '#c8871e', pattern: '#ffd23e', outline: '#c8930e' },
    { variant: 'star' }),
  mk('cloud', '云朵', 'fantasy', 'fantasy', 12,
    { body: '#f4f8ff', belly: '#ffffff', eye: '#5a6a8a', mouth: '#8a9ab8', pattern: '#f4f8ff', outline: '#c0d0e8' },
    { variant: 'cloud' }),
  mk('lava', '岩浆', 'fantasy', 'fantasy', 16,
    { body: '#d84830', belly: '#f07840', eye: '#ffd23e', mouth: '#3a1208', pattern: '#f8b040', outline: '#7a1e0e' },
    { variant: 'lava' }),
  mk('ice-shard', '寒冰', 'fantasy', 'fantasy', 16,
    { body: '#a8e0f0', belly: '#d8f2fa', eye: '#2a6a8a', mouth: '#5a9ab8', pattern: '#a8e0f0', outline: '#5aa0c0' },
    { variant: 'ice' }),
  mk('robo', '机械', 'fantasy', 'fantasy', 16,
    { body: '#8a94a8', belly: '#c8d0dc', eye: '#4af0a0', mouth: '#3a4252', pattern: '#8a94a8', acc: '#5a6478', acc2: '#ff5252', outline: '#3a4252' },
    { variant: 'robo' }),
  // ---- 神话 ----
  mk('gold-dragon', '金龙', 'dragon', 'dragon', 20,
    { body: '#f0c030', ear: '#e0b020', belly: '#fce8b0', earIn: '#fff0c8', eye: '#8a5a10', acc: '#d8a020', acc2: '#ffd700' },
    { acc: 'crown' })
];

module.exports = { SKINS };
