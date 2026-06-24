# 主题 CSS 变量名速查

v0.7 主题适配时踩坑总结：写测试或新代码时，**别猜变量名**，先 `grep "^\s*--"` 一下本文件。

## ⚠️ 容易踩的坑

| 错误名 | 实际名 | 原因 |
|---|---|---|
| `--bg` | `--bg-0` / `--bg-1` / `--bg-2` / `--bg-3` | 项目**没有** `--bg`，是分层的 0/1/2/3（0 最深，3 最浅） |
| `--btn-bg` | `--bg-3` | `.btn` 默认用 `--bg-3` 作底色，没有专门 `--btn-bg` 变量 |
| `--btn-border` | `--line` | `.btn` 默认用 `--line` 作边框 |

## ✅ 项目有的主题变量（速查）

**所有主题**都有的 11 个核心：
- `--bg-0` `--bg-1` `--bg-2` `--bg-3`（背景 4 级，0 最深）
- `--line` `--line-2`（边框 2 级）
- `--text` `--text-dim` `--text-mute`（文字 3 级）
- `--primary` `--primary-2` `--primary-fade`（主色 3 级）
- `--success` `--warn` `--error` `--info`（语义色）

**tag 系列**（v0.7 补全）:
- `--tag-bg` `--tag-fg`（默认 tag 背景/字）
- `--tag-enc-bg` `--tag-enc-fg`（蓝色 tag，编码/信息）
- `--tag-warn-bg` `--tag-warn-fg`（橙色 tag，警告）
- `--tag-ready-bg` `--tag-ready-fg`（绿色 tag，成功/就绪）
- `--tag-placeholder-bg` `--tag-placeholder-fg`（占位）

**按钮 override**（v0.7 新加，按主题覆盖默认 `.btn`）:
- `--btn-bg-override`（按钮背景）
- `--btn-border-override`（按钮边框）
- `--btn-hover-bg-override`（按钮 hover 背景）

`.btn` 用法：`background: var(--btn-bg-override, var(--bg-3))` — fallback 链，未定义 override 时用默认。

**其它**:
- `--shadow` `--shadow-sm` `--shadow-pop`（阴影）
- `--btn-primary-grad`（主按钮渐变）
- `--btn-danger-grad`（危险按钮渐变）
- `--btn-hover-bg` `--btn-hover-border`（hover 默认）
- `--topbar-bg` `--sidebar-bg` `--body-bg`（布局容器）

## 🎨 4 主题特性（v0.7 修后）

- **dark**：默认，按钮默认 bg-3 + line 边框
- **light**：`--btn-bg-override=#ffffff` `--btn-border-override=#cbd5e1` 让按钮"立"起来；tag 字色加深到 7:1
- **green**：`--btn-bg-override=#f3f6e8` `--btn-border-override=#b8c5a3`；tag 用绿色系
- **hc**：`--btn-bg-override=#0a0a0a` `--btn-border-override=#ffffff`；tag 黑底亮黄字 16:1

## 🔍 怎么查

```bash
# 列所有主题变量定义
grep -nE "^\s*--[a-z]" web/style.css | head -50

# 看某个变量在哪几个主题里被定义
grep -nE "^\s*--tag-fg:" web/style.css
```

## 📌 写新 page 时的建议

1. **不要硬编码颜色**——用变量，让 4 主题自动适配
2. 想要"按钮感" → 用 `.btn` 类，自动吃主题 override
3. 想要"tag 形状" → 用 `.tag` 或 `.tag.tag-warn` 等，自动吃主题配色
4. 新加元素如果用 inline style 设 `color: #xxx`，**永远不要**这么写——必须用 `var(--text)` 之类

## 🐛 v0.7 修掉的真 bug

1. **light .btn 边框淡，按钮"飘"** → 加 `--btn-border-override`
2. **light .tag 紫字 4.0:1** → 加深到 #4c1d95（AAA）
3. **green .tag 紫字跟米绿底冲突** → 换绿色系
4. **hc .tag 紫字配黑底** → 改黄字黑底 16:1
5. **cron.js statusTag 初始 `text: '—'`** → 初始 visibility:hidden，doParse 后才显示
6. **cron.js validTag 创建后从未使用** → 删除遗留代码
