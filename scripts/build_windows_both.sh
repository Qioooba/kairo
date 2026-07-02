#!/usr/bin/env bash
# build_windows_both.sh
#
# 一次跑完，编出 Win10/11 + Win7 两份 Kairo.exe。
#
# 为什么是两份：Go 1.21+ 不再支持 Win7/8/Server 2008/2012（PE loader / syscall
# 不再适配 NT 6.1），用新版 Go 编出来的二进制在 Win7 上根本加载不了。业务代码
# 完全相同，差别只在 Go 工具链版本，所以"一份代码、两套产物"是唯一正解。
#
# 本脚本是薄编排层：不复制构建逻辑，直接调用两个已有脚本，避免逻辑漂移。
#   - Win10/11 → scripts/build_windows_amd64.sh        （本机 Go）
#   - Win7      → scripts/build_windows_amd64_win7_go120.sh （GO120_HOME 指向的 Go 1.20.x）
#
# 用法：
#   1)（可选）准备 Go 1.20.x 用于 Win7 兼容构建：
#        curl -L -o /tmp/go1.20.14.darwin-amd64.tar.gz \
#          https://go.dev/dl/go1.20.14.darwin-amd64.tar.gz
#        mkdir -p ~/sdk && tar -C ~/sdk -xzf /tmp/go1.20.14.darwin-amd64.tar.gz
#        mv ~/sdk/go ~/sdk/go120
#        export GO120_HOME=~/sdk/go120
#   2) 跑：
#        ./scripts/build_windows_both.sh [版本号]
#
# 产物：
#   dist/kairo-<ver>/Kairo_win10.exe    ← Win10 / Win11 / Server 2016+
#   dist/kairo-<ver>-win7/Kairo_win7.exe ← Win7 / Win8 / Server 2008 / 2012
#
# 没配 GO120_HOME 时自动跳过 Win7，只编 Win10/11。

set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:-v0.1.0}"

if ! command -v go >/dev/null 2>&1; then
  echo "错误：未找到 go 命令，请先安装 Go 并加入 PATH" >&2
  exit 1
fi

GO_VERSION="$(go version | awk '{print $3}')"
echo "============================================="
echo " Kairo 双版本构建"
echo "  版本号:   ${VER}"
echo "  本机 Go:  ${GO_VERSION}  → Win10/11"
[[ -n "${GO120_HOME:-}" && -x "${GO120_HOME}/bin/go" ]] \
  && echo "  Win7 Go:  $("${GO120_HOME}/bin/go" version | awk '{print $3}')  → Win7/8"
echo "============================================="

# -------------------------------------------------------------------
# 1) Win10/11 —— 始终编译（用本机 Go）
# -------------------------------------------------------------------
echo
echo ">>> [1/2] 编译 Win10/11 版本"
./scripts/build_windows_amd64.sh "${VER}"

# -------------------------------------------------------------------
# 2) Win7 —— 需要 GO120_HOME，没配就跳过
# -------------------------------------------------------------------
echo
if [[ -z "${GO120_HOME:-}" || ! -x "${GO120_HOME}/bin/go" ]]; then
  echo ">>> [2/2] 跳过 Win7（未配置 GO120_HOME）"
  echo "         如需 Win7 兼容版本："
  echo "           1) 下载 Go 1.20.x 解压到独立目录"
  echo "           2) export GO120_HOME=~/sdk/go120"
  echo "           3) 重跑 ./scripts/build_windows_both.sh ${VER}"
else
  echo ">>> [2/2] 编译 Win7 版本"
  ./scripts/build_windows_amd64_win7_go120.sh "${VER}"
fi

# -------------------------------------------------------------------
# 收尾
# -------------------------------------------------------------------
echo
echo "============================================="
echo " 构建完成"
echo "============================================="
echo "  Win10/11 → dist/kairo-${VER}/Kairo_win10.exe"
if [[ -f "dist/kairo-${VER}-win7/Kairo_win7.exe" ]]; then
  echo "  Win7     → dist/kairo-${VER}-win7/Kairo_win7.exe"
  echo
  echo ">> 打包：./scripts/package_windows_both.sh ${VER}"
else
  echo "  Win7     → (未生成)"
fi
