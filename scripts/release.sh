#!/usr/bin/env bash
# Kairo 主线发布：Go 1.24+，目标 Windows 10/11 amd64。
# Win7 / Go 1.20 版本在 legacy 分支独立维护，不共享主线依赖图。

set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -n "${1:-}" ]]; then
  VER="${1}"
elif [[ -f VERSION ]]; then
  VER="$(tr -d '[:space:]' < VERSION)"
else
  echo "错误：未指定版本号，且未找到 VERSION 文件" >&2
  echo "用法: $0 <版本号>" >&2
  exit 1
fi

echo "============================================="
echo " Kairo 主线发布  版本=${VER}"
echo " 目标：Windows 10/11 amd64 · Go 1.24+"
echo "============================================="

echo ">>> [1/4] 检查 VERSION 与派生文件一致"
if command -v node >/dev/null 2>&1; then
  node scripts/check-version.js
else
  echo ">> 跳过：未找到 node，无法跑 scripts/check-version.js"
fi

echo ">>> [2/4] 运行项目质量门"
# 不使用 ./...：开发目录可能带有被 .gitignore 排除的 sdk/，Go 仍会递归进去。
# 显式覆盖所有产品包，确保新增模块不会因为发布脚本白名单过时而漏测。
go test -mod=vendor -count=1 . ./cmd/... ./internal/...
go vet -mod=vendor . ./cmd/... ./internal/...
if command -v node >/dev/null 2>&1; then
	node --check web/api.js
	node --check web/app.js
	node --check web/pages/compare.js
	node --check web/pages/database.js
	node --check web/pages/waspack.js
	node --check web/pages/wscodegen.js
	node web/pages/webservice.test.js
fi

echo ">>> [3/4] 编译主线 Windows 版本"
./scripts/build_windows_amd64.sh "${VER}"

EXE="dist/kairo-${VER}/Kairo_win10.exe"
if [[ ! -f "${EXE}" ]]; then
  echo "错误：构建产物缺失 ${EXE}" >&2
  exit 1
fi

echo ">>> [4/4] 打包主线 Windows 版本"
./scripts/package_windows.sh "${VER}"

ZIP="dist/kairo-${VER}-windows.zip"
if [[ ! -f "${ZIP}" ]]; then
  echo "错误：发布包缺失 ${ZIP}" >&2
  exit 1
fi

echo
echo ">> 发布完成：${ZIP}"
