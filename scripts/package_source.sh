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
#   ./dist/kairo-src-<ver>.tar.gz
#
# 打包后建议跑 smoke 测试：
#   ./scripts/release_smoke_test.sh ./dist/kairo-src-<ver>.tar.gz

set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -n "${1:-}" ]]; then
  VER="${1}"
elif [[ -f VERSION ]]; then
  VER="$(tr -d '[:space:]' < VERSION)"
else
  echo "错误：未指定版本号，且未找到 VERSION 文件" >&2
  echo "用法: $0 [版本号]  （或根目录放 VERSION 文件）" >&2
  echo "例如: $0 v0.9" >&2
  exit 1
fi
OUT="dist/kairo-src-${VER}.tar.gz"

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
  git archive --format=tar.gz --prefix="kairo-${VER}/" HEAD -o "${OUT}"
else
  echo ">> 不在 git 仓库内，手动 tar 打包（排除 mock/测试文件）"
  tar -czf "${OUT}" \
    --exclude='./dist' \
    --exclude='./.git' \
    --exclude='./logs/audit.log' \
    --exclude='./data' \
    --exclude='./downloads' \
    --exclude='./node_modules' \
    --exclude='./*.exe' \
    --exclude='./scripts/fake-files' \
    --exclude='./scripts/fake-websphere' \
    --exclude='./scripts/mock_sshd.py' \
    --exclude='./scripts/mock_shell_sshd.py' \
    --exclude='./scripts/e2e.sh' \
    --exclude='./scripts/e2e-prepare-fixtures.js' \
    --exclude='./scripts/acceptance_run.py' \
    --exclude='./scripts/release_smoke_test.sh' \
    --exclude='./cmd/mock-license-server' \
    --exclude='./playwright-*.js' \
    --exclude='./docs/qa/*.js' \
    --exclude='./docs/qa/*.json' \
    --transform "s,^\./,kairo-${VER}/," \
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
echo ">> 验证 mock/测试文件已被排除："

check_not_in_tar() {
  local pattern="$1"
  local label="$2"
  local count
  count=$(tar -tzf "${OUT}" | grep -c "${pattern}" || true)
  if [[ "${count}" -ne 0 ]]; then
    echo "   ❌ ${label}：发现 ${count} 个文件，应当被排除（pattern: ${pattern}）" >&2
    exit 1
  fi
  echo "   ✅ ${label}：已排除"
}

check_not_in_tar "scripts/fake-files/"    "mock 数据（scripts/fake-files/）"
check_not_in_tar "scripts/fake-websphere/" "mock WebSphere 日志（scripts/fake-websphere/）"
check_not_in_tar "mock_sshd\.py"           "mock SSH 服务"
check_not_in_tar "cmd/mock-license-server" "mock License 服务"
check_not_in_tar "playwright-.*\.js"       "Playwright E2E 脚本"

echo
echo ">> 打包完成：${OUT}"
ls -lh "${OUT}"
echo
echo ">> 建议跑 smoke 测试："
echo "   ./scripts/release_smoke_test.sh ${OUT}"
