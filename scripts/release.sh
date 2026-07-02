#!/usr/bin/env bash
# release.sh
#
# 正式发布流程：强制编译 Win10 + Win7 双版本并打包，缺一不可。
#
# 与 build_windows_both.sh 的区别：
#   - build_windows_both.sh：允许跳过 Win7（没 GO120_HOME 就只编 Win10）→ 快速测试用
#   - release.sh：强制要求 GO120_HOME，缺失就报错退出 → 正式发布用，防止漏版本
#
# 用法：
#   1) 确保 Go 1.20.x 已准备好（Win7 兼容构建）：
#        export GO120_HOME=~/sdk/go120
#   2) 跑发布脚本：
#        ./scripts/release.sh v0.9.0
#
# 产物：
#   dist/Kairo-<ver>-windows-both.zip  ← Win10/11 + Win7 合并发布包（v0.12-rc10+ 起默认）
#
# 失败条件（会立即退出）：
#   - GO120_HOME 未配置或路径无效
#   - Win10/Win7 编译产物缺失
#   - 打包产物缺失

set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:?用法: $0 <版本号>，例如: $0 v0.9.0}"

echo "============================================="
echo " Kairo 正式发布  版本=${VER}"
echo "============================================="
echo

# -------------------------------------------------------------------
# 1) 前置检查：GO120_HOME 必须有效
# -------------------------------------------------------------------
if [[ -z "${GO120_HOME:-}" ]]; then
  echo "错误：未设置 GO120_HOME" >&2
  echo "正式发布必须同时编译 Win10 + Win7 双版本。" >&2
  echo "请先准备 Go 1.20.x：" >&2
  echo "  curl -L -o /tmp/go1.20.14.darwin-amd64.tar.gz \\"
  echo "    https://go.dev/dl/go1.20.14.darwin-amd64.tar.gz"
  echo "  mkdir -p ~/sdk && tar -C ~/sdk -xzf /tmp/go1.20.14.darwin-amd64.tar.gz"
  echo "  mv ~/sdk/go ~/sdk/go120"
  echo "  export GO120_HOME=~/sdk/go120" >&2
  exit 1
fi

GO120_BIN="${GO120_HOME}/bin/go"
if [[ ! -x "${GO120_BIN}" ]]; then
  echo "错误：GO120_HOME 路径无效，找不到 ${GO120_BIN}" >&2
  exit 1
fi

GO120_VER="$("${GO120_BIN}" version | awk '{print $3}')"
MAJOR_MINOR="$(echo "${GO120_VER}" | sed -E 's/^go([0-9]+)\.([0-9]+).*/\1.\2/')"
if [[ "${MAJOR_MINOR}" != "1.20" ]]; then
  echo "错误：GO120_HOME 的 Go 版本为 ${GO120_VER}，不是 1.20.x" >&2
  echo "Win7 兼容构建必须用 Go 1.20 系列版本。" >&2
  exit 1
fi

echo ">> Win10/11 工具链：$(go version | awk '{print $3}')"
echo ">> Win7     工具链：${GO120_VER}"
echo

# -------------------------------------------------------------------
# 2) 编译双版本（调用 build_windows_both.sh）
# -------------------------------------------------------------------
echo ">>> [1/3] 编译 Win10 + Win7 双版本"
./scripts/build_windows_both.sh "${VER}"

# -------------------------------------------------------------------
# 3) 验证产物齐全
# -------------------------------------------------------------------
WIN10_EXE="dist/kairo-${VER}/Kairo_win10.exe"
WIN7_EXE="dist/kairo-${VER}-win7/Kairo_win7.exe"

echo
echo ">>> [2/3] 验证编译产物"

if [[ ! -f "${WIN10_EXE}" ]]; then
  echo "错误：Win10 编译产物缺失 ${WIN10_EXE}" >&2
  exit 1
fi
echo "   ✅ Win10: ${WIN10_EXE} ($(ls -lh "${WIN10_EXE}" | awk '{print $5}'))"

if [[ ! -f "${WIN7_EXE}" ]]; then
  echo "错误：Win7 编译产物缺失 ${WIN7_EXE}" >&2
  echo "（GO120_HOME 已配置但 Win7 编译失败，请检查上方日志）" >&2
  exit 1
fi
echo "   ✅ Win7:  ${WIN7_EXE} ($(ls -lh "${WIN7_EXE}" | awk '{print $5}'))"

# -------------------------------------------------------------------
# 4) 打包
# -------------------------------------------------------------------
echo
echo ">>> [3/3] 打包发布 zip"
./scripts/package_windows_both.sh "${VER}"

# -------------------------------------------------------------------
# 5) 验证打包产物
# -------------------------------------------------------------------
BOTH_ZIP="dist/Kairo-${VER}-windows-both.zip"

if [[ ! -f "${BOTH_ZIP}" ]]; then
  echo "错误：合并发布包缺失 ${BOTH_ZIP}" >&2
  echo "（package_windows_both.sh 应当默认打合并包；如果是单包模式说明 Win10/Win7 有一个没编）" >&2
  exit 1
fi
echo "   ✅ 合并包: ${BOTH_ZIP} ($(ls -lh "${BOTH_ZIP}" | awk '{print $5}'))"

echo
echo "============================================="
echo " 发布完成"
echo "============================================="
echo "  Win10/11 + Win7 → ${BOTH_ZIP} ($(ls -lh "${BOTH_ZIP}" | awk '{print $5}'))"
echo
echo ">> 下一步：把 ${BOTH_ZIP} 发给用户 / 上传发布渠道"