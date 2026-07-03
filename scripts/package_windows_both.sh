#!/usr/bin/env bash
# package_windows_both.sh
#
# 把已构建好的 Win10/11 + Win7 两个产物目录打成 zip，方便分发。
#
# 产物策略：
#   - 双版本都在场（推荐 / release 默认场景）→ 打一个合并包：
#       dist/Kairo-<ver>-windows-both.zip
#       内部含 kairo-<ver>/（Win10/11）和 kairo-<ver>-win7/（Win7）两个目录
#   - 只编了其中一个（快速测试场景，比如没配 GO120_HOME）→ 打对应单包：
#       dist/kairo-<ver>-windows.zip        ← Win10/11 单包
#       dist/kairo-<ver>-win7-windows.zip   ← Win7 单包
#
# 用法：
#   ./scripts/package_windows_both.sh <版本号>
# 例如：
#   ./scripts/package_windows_both.sh v0.1.0
#

set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -n "${1:-}" ]]; then
  VER="${1}"
elif [[ -f VERSION ]]; then
  VER="$(tr -d '[:space:]' < VERSION)"
else
  echo "错误：未指定版本号，且未找到 VERSION 文件" >&2
  echo "用法: $0 [版本号]  （或根目录放 VERSION 文件）" >&2
  echo "例如: $0 v0.1.0" >&2
  exit 1
fi

WIN10_DIR="dist/kairo-${VER}"
WIN7_DIR="dist/kairo-${VER}-win7"
BOTH_ZIP="dist/Kairo-${VER}-windows-both.zip"
WIN10_ZIP="dist/kairo-${VER}-windows.zip"
WIN7_ZIP="dist/kairo-${VER}-win7-windows.zip"

if ! command -v zip >/dev/null 2>&1; then
  echo "错误：未找到 zip 命令（macOS 可用 brew install zip）" >&2
  exit 1
fi

# 清理不需要打进 zip 的运行时文件
[[ -d "${WIN10_DIR}" ]] && rm -f "${WIN10_DIR}/audit.log"
[[ -d "${WIN7_DIR}" ]]  && rm -f "${WIN7_DIR}/audit.log"

# -------------------------------------------------------------------
# 1) 双版本都在场：打合并包（默认 release 场景）
# -------------------------------------------------------------------
if [[ -d "${WIN10_DIR}" && -d "${WIN7_DIR}" ]]; then
  [[ -f "${BOTH_ZIP}" ]] && rm -f "${BOTH_ZIP}"

  (cd dist && zip -rq "$(basename "${BOTH_ZIP}")" \
      "$(basename "${WIN10_DIR}")" \
      "$(basename "${WIN7_DIR}")")

  echo "============================================="
  echo " Kairo 双版本打包  版本=${VER}"
  echo "============================================="
  echo
  echo ">> Win10/11 + Win7 合并包：${BOTH_ZIP}"
  ls -lh "${BOTH_ZIP}"
  echo "   zip 内容："
  unzip -l "${BOTH_ZIP}" | tail -n +2 | head -30
  echo
  echo "============================================="
  echo " 打包完成（合并包）"
  echo "============================================="
  echo "  Win10/11 + Win7 → ${BOTH_ZIP}"
  echo
  echo ">> 用法：解压后根据系统选择对应子目录里的 exe（双击 Kairo_win10.exe 或 Kairo_win7.exe）"
  exit 0
fi

# -------------------------------------------------------------------
# 2) 单版本回退：只编了 Win10/11 或只编了 Win7 的场景（快速测试用）
# -------------------------------------------------------------------
echo "============================================="
echo " Kairo 单版本打包  版本=${VER}"
echo "============================================="
echo
echo ">> 双版本不全，只打单包（要走 release 流程请确保 Win7 也编了）"
echo

packed_any=0

if [[ -d "${WIN10_DIR}" ]]; then
  [[ -f "${WIN10_ZIP}" ]] && rm -f "${WIN10_ZIP}"
  (cd dist && zip -rq "$(basename "${WIN10_ZIP}")" "$(basename "${WIN10_DIR}")")
  echo ">> Win10/11：${WIN10_ZIP}"
  ls -lh "${WIN10_ZIP}"
  echo "   zip 内容："
  unzip -l "${WIN10_ZIP}" | tail -n +2 | head -20
  echo
  packed_any=1
fi

if [[ -d "${WIN7_DIR}" ]]; then
  [[ -f "${WIN7_ZIP}" ]] && rm -f "${WIN7_ZIP}"
  (cd dist && zip -rq "$(basename "${WIN7_ZIP}")" "$(basename "${WIN7_DIR}")")
  echo ">> Win7：${WIN7_ZIP}"
  ls -lh "${WIN7_ZIP}"
  echo "   zip 内容："
  unzip -l "${WIN7_ZIP}" | tail -n +2 | head -20
  echo
  packed_any=1
fi

if [[ ${packed_any} -eq 0 ]]; then
  echo "错误：找不到 ${WIN10_DIR} 或 ${WIN7_DIR}" >&2
  echo "请先跑 ./scripts/build_windows_both.sh ${VER}" >&2
  exit 1
fi

echo "============================================="
echo " 打包完成（单包模式）"
echo "============================================="
[[ -f "${WIN10_ZIP}" ]] && echo "  Win10/11 → ${WIN10_ZIP}"
[[ -f "${WIN7_ZIP}" ]]  && echo "  Win7     → ${WIN7_ZIP}"
