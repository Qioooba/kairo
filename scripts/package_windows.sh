#!/usr/bin/env bash
# package_windows.sh
#
# 把已经构建好的 dist/kairo-<ver>/ 目录打包成 zip，
# 方便发给同事（同事只需解压后双击 Kairo.exe）。
#
# 用法：
#   ./scripts/package_windows.sh <ver>
# 例如：
#   ./scripts/package_windows.sh v0.1.0
#
# 产物：
#   ./dist/kairo-<ver>-windows.zip

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

SRC="dist/kairo-${VER}"
DST="dist/kairo-${VER}-windows.zip"

if [[ ! -d "${SRC}" ]]; then
  echo "错误：找不到 ${SRC}" >&2
  echo "请先跑 ./scripts/build_windows_amd64.sh ${VER}" >&2
  exit 1
fi

if ! command -v zip >/dev/null 2>&1; then
  echo "错误：未找到 zip 命令（macOS 可用 brew install zip）" >&2
  exit 1
fi

# 清理不需要打进 zip 的内容
rm -f "${SRC}/audit.log"
rm -f "${SRC}/config.yaml"

# zip 默认会更新已有归档，已从源目录删除的历史条目仍会残留；每次发版必须全新生成。
rm -f "${DST}"

(cd "$(dirname "${SRC}")" && zip -r "$(basename "${DST}")" "$(basename "${SRC}")")

if unzip -Z1 "${DST}" | grep -Eq '(^|/)config\.yaml$'; then
  echo "错误：发布包不应包含会覆盖用户配置的 config.yaml" >&2
  exit 1
fi

echo
echo ">> 已生成：${DST}"
ls -lh "${DST}"
echo
echo ">> zip 内包含："
unzip -l "${DST}" | tail -20
