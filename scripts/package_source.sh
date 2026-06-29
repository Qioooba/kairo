#!/usr/bin/env bash
# package_source.sh
#
# 打包源码发布包（tar.gz），确保包含 vendor/ 和 web/vendor/diff2html.min.{css,js}。
# 修复 PKG-001（缺 vendor/）和 PKG-002（缺 web/vendor/diff2html）。
#
# 用法：
#   ./scripts/package_source.sh [版本号]
# 例如：
#   ./scripts/package_source.sh v0.9
#
# 产物：
#   ./dist/doubao-toolbox-src-<ver>.tar.gz
#
# 打包后建议跑 smoke 测试：
#   ./scripts/release_smoke_test.sh ./dist/doubao-toolbox-src-<ver>.tar.gz

set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:-v0.9}"
OUT="dist/doubao-toolbox-src-${VER}.tar.gz"

echo ">> 打包源码发布包: ${OUT}"

# ---- 前置检查：vendor/ 和 web/vendor/ 必须存在 ----
# PKG-001：vendor/ 缺失 → 离线环境 go build/go test 失败
if [[ ! -d vendor || ! -f vendor/modules.txt ]]; then
  echo "错误：vendor/ 目录或 vendor/modules.txt 缺失" >&2
  echo "请先执行：go mod vendor" >&2
  exit 1
fi

# PKG-002：web/vendor/diff2html 缺失 → 代码比对页 404
if [[ ! -f web/vendor/diff2html.min.css || ! -f web/vendor/diff2html.min.js ]]; then
  echo "错误：web/vendor/diff2html.min.css 或 .js 缺失" >&2
  echo "代码比对页依赖这两个文件（web/index.html 直接引用）" >&2
  exit 1
fi

mkdir -p dist

# ---- 打包 ----
# 用 git archive 保证只包含版本控制内的文件（排除 dist/、.git/、运行时产物）。
# git archive 会自动包含 vendor/ 和 web/vendor/（它们已在版本控制内）。
# 如果不在 git 仓库内，回退到 tar 手动排除。
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo ">> 使用 git archive 打包（自动排除 dist/、.git/、运行时产物）"
  git archive --format=tar.gz --prefix="doubao-toolbox-${VER}/" HEAD -o "${OUT}"
else
  echo ">> 不在 git 仓库内，手动 tar 打包"
  tar -czf "${OUT}" \
    --exclude='./dist' \
    --exclude='./.git' \
    --exclude='./logs/audit.log' \
    --exclude='./data' \
    --exclude='./downloads' \
    --exclude='./node_modules' \
    --exclude='./*.exe' \
    --transform "s,^\./,doubao-toolbox-${VER}/," \
    .
fi

# ---- 验证 tar.gz 内容 ----
echo
echo ">> 验证关键文件存在于 tar.gz 中："

check_in_tar() {
  local pattern="$1"
  local label="$2"
  local count
  count=$(tar -tzf "${OUT}" | grep -c "${pattern}" || true)
  if [[ "${count}" -eq 0 ]]; then
    echo "   ❌ ${label}：未找到（pattern: ${pattern}）" >&2
    exit 1
  fi
  echo "   ✅ ${label}：${count} 个文件"
}

check_in_tar "vendor/modules\.txt$" "PKG-001 vendor/modules.txt"
check_in_tar "vendor/.*\.go$" "PKG-001 vendor Go 源码"
check_in_tar "web/vendor/diff2html\.min\.css$" "PKG-002 diff2html.min.css"
check_in_tar "web/vendor/diff2html\.min\.js$"  "PKG-002 diff2html.min.js"
check_in_tar "go\.mod$" "go.mod"
check_in_tar "main\.go$" "main.go"

echo
echo ">> 打包完成：${OUT}"
ls -lh "${OUT}"
echo
echo ">> 建议跑 smoke 测试："
echo "   ./scripts/release_smoke_test.sh ${OUT}"
