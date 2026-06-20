#!/usr/bin/env bash
# build_windows_amd64_win7_go120.sh
#
# 用 Go 1.20.x 编译 Windows 7 兼容版。
#
# 为什么需要单独环境：
#   Go 1.21+ 不再支持 Windows 7/8/Server 2008/2012（要求 Win10/Server 2016+）。
#   所以 Win7 兼容版必须用 Go 1.20.x（最后一个支持 Win7 的版本）单独编译。
#   本机 macOS 上 brew install go 通常装的是最新版，不能直接编 Win7。
#
# 用法：
#   1) 下载 Go 1.20.x 到一个独立目录（不要 brew install）：
#        curl -L -o /tmp/go1.20.14.linux-amd64.tar.gz \
#          https://go.dev/dl/go1.20.14.linux-amd64.tar.gz
#        或在 macOS / Linux 上：
#        curl -L -o /tmp/go1.20.14.darwin-amd64.tar.gz \
#          https://go.dev/dl/go1.20.14.darwin-amd64.tar.gz
#   2) 把它解压到某个目录，例如 ~/sdk/go120
#   3) 把 GO120_HOME 环境变量指向这个目录（包含 bin/go 的父目录）
#   4) 跑这个脚本：
#        GO120_HOME=~/sdk/go120 ./scripts/build_windows_amd64_win7_go120.sh v0.1.0
#
# 产物：
#   ./dist/ops-toolbox-v0.1.0-win7/OpsToolbox_win7.exe
#
# 验证 Win7 兼容：
#   把产物 + config.yaml + downloads/ + logs/ + data/ 拷到一台 Win7 机器上，
#   用 cmd 启动 OpsToolbox_win7.exe 验证。

set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:-v0.1.0}"
OUT_DIR="dist/ops-toolbox-${VER}-win7"
mkdir -p "${OUT_DIR}"

if [[ -z "${GO120_HOME:-}" ]]; then
  echo "错误：未设置 GO120_HOME" >&2
  echo "请先把 Go 1.20.x 解压到某目录，然后：" >&2
  echo "  export GO120_HOME=~/sdk/go120" >&2
  echo "  $0 ${VER}" >&2
  exit 1
fi

GO_BIN="${GO120_HOME}/bin/go"
if [[ ! -x "${GO_BIN}" ]]; then
  echo "错误：找不到 ${GO_BIN}" >&2
  exit 1
fi

GO120_VERSION="$("${GO_BIN}" version | awk '{print $3}')"
echo ">> Go 版本: ${GO120_VERSION}"
MAJOR_MINOR="$(echo "${GO120_VERSION}" | sed -E 's/^go([0-9]+)\.([0-9]+).*/\1.\2/')"
if [[ "${MAJOR_MINOR}" != "1.20" ]]; then
  echo "警告：检测到 ${GO120_VERSION}，不是 1.20.x" >&2
  echo "      建议使用 Go 1.20 系列的最新版本（最后一个支持 Win7 的版本）" >&2
  read -p "继续？[y/N] " ans
  [[ "${ans}" == "y" || "${ans}" == "Y" ]] || exit 1
fi

export PATH="${GO120_HOME}/bin:${PATH}"
export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0
export GOTOOLCHAIN=local

"${GO_BIN}" build -trimpath -ldflags "-s -w" -o "${OUT_DIR}/OpsToolbox_win7.exe" .

cp config.yaml  "${OUT_DIR}/"
cp README.md    "${OUT_DIR}/"
mkdir -p "${OUT_DIR}/downloads" "${OUT_DIR}/logs" "${OUT_DIR}/data"

echo
echo ">> 已生成："
ls -lh "${OUT_DIR}/"
echo
echo ">> 提示：把这个目录拷到 Win7 机器上，从 cmd 启动 OpsToolbox_win7.exe 验证"
