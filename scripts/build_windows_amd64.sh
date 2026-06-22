#!/usr/bin/env bash
# build_windows_amd64.sh
#
# 用本机 Go 编译 Windows 10/11 用的 OpsToolbox.exe。
# 纯交叉编译，不依赖 Windows。
#
# 用法：
#   ./scripts/build_windows_amd64.sh [版本号]
#
# 产物：
#   ./dist/ops-toolbox-<ver>/OpsToolbox.exe
#
# 注意：本脚本默认用本地 Go 工具链（不下载更新版本）。

set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:-v0.1.0}"
OUT_DIR="dist/ops-toolbox-${VER}"
mkdir -p "${OUT_DIR}"

if ! command -v go >/dev/null 2>&1; then
  echo "错误：未找到 go 命令，请先安装 Go 1.20+ 并加入 PATH" >&2
  exit 1
fi

GO_VERSION="$(go version | awk '{print $3}')"
echo ">> Go 版本: ${GO_VERSION}"
echo ">> 目标:    Windows 10/11 amd64"

export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0
export GOTOOLCHAIN=local

go build -trimpath -ldflags "-s -w" -o "${OUT_DIR}/OpsToolbox.exe" .

# 复制运行所需文件
# config.yaml 是首选，但发布包里通常只有 config.yaml.production.example（占位 / 模板）。
# 优先用本地 config.yaml，没有就回退到 example，再没有就报错退出。
if [[ -f config.yaml ]]; then
  cp config.yaml "${OUT_DIR}/config.yaml"
elif [[ -f config.yaml.production.example ]]; then
  echo ">> 警告：未找到 config.yaml，使用 config.yaml.production.example 复制为 config.yaml"
  cp config.yaml.production.example "${OUT_DIR}/config.yaml"
else
  echo "错误：找不到 config.yaml 或 config.yaml.production.example" >&2
  exit 1
fi
cp README.md    "${OUT_DIR}/"
# 顺手复制启动脚本（README 里说可以用 start.bat）
if [[ -f scripts/start.bat ]]; then
  cp scripts/start.bat "${OUT_DIR}/start.bat"
fi
mkdir -p "${OUT_DIR}/downloads" "${OUT_DIR}/logs" "${OUT_DIR}/data"

echo
echo ">> 已生成："
ls -lh "${OUT_DIR}/"
echo
echo ">> 产物目录：${OUT_DIR}/"
echo ">> 建议：把这个目录打包成 zip 发给同事（不要把 dist/ 整目录打进去）"
