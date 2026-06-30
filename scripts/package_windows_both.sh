#!/usr/bin/env bash
# package_windows_both.sh
#
# 把已构建好的 Win10/11 + Win7 两个产物目录分别打成 zip，方便分发。
# 两个目录任一缺失就跳过对应的 zip，不报错（比如只编了 Win10/11 时也能跑）。
#
# 用法：
#   ./scripts/package_windows_both.sh <版本号>
# 例如：
#   ./scripts/package_windows_both.sh v0.1.0
#
# 产物：
#   dist/kairo-<ver>-windows.zip       ← Win10/11 包
#   dist/kairo-<ver>-win7-windows.zip  ← Win7 包

set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:?用法: $0 <版本号>，例如: $0 v0.1.0}"

WIN10_DIR="dist/kairo-${VER}"
WIN7_DIR="dist/kairo-${VER}-win7"
WIN10_ZIP="dist/kairo-${VER}-windows.zip"
WIN7_ZIP="dist/kairo-${VER}-win7-windows.zip"

if ! command -v zip >/dev/null 2>&1; then
  echo "错误：未找到 zip 命令（macOS 可用 brew install zip）" >&2
  exit 1
fi

pack_one() {
  local src_dir="$1"
  local zip_path="$2"
  local label="$3"

  if [[ ! -d "${src_dir}" ]]; then
    echo ">> 跳过 ${label}（${src_dir} 不存在，先跑 ./scripts/build_windows_both.sh ${VER}）"
    return 0
  fi

  # 清理不需要打进 zip 的运行时文件
  rm -f "${src_dir}/audit.log"

  if [[ -f "${zip_path}" ]]; then
    echo ">> 覆盖旧包：${zip_path}"
    rm -f "${zip_path}"
  fi

  (cd dist && zip -rq "$(basename "${zip_path}")" "$(basename "${src_dir}")")

  echo ">> ${label}：${zip_path}"
  ls -lh "${zip_path}"
  echo "   zip 内容："
  unzip -l "${zip_path}" | tail -n +2 | head -20
  echo
}

echo "============================================="
echo " Kairo 双版本打包  版本=${VER}"
echo "============================================="
echo

pack_one "${WIN10_DIR}" "${WIN10_ZIP}" "Win10/11"
pack_one "${WIN7_DIR}"  "${WIN7_ZIP}"  "Win7"

echo "============================================="
echo " 打包完成"
echo "============================================="
[[ -f "${WIN10_ZIP}" ]] && echo "  Win10/11 → ${WIN10_ZIP}"
[[ -f "${WIN7_ZIP}" ]]  && echo "  Win7     → ${WIN7_ZIP}"
