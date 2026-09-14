#!/usr/bin/env bash
# build_windows_amd64.sh
#
# 用本机 Go 编译 Windows 10/11 用的 Kairo_win10.exe。
# 纯交叉编译，不依赖 Windows。
#
# 用法：
#   ./scripts/build_windows_amd64.sh [版本号]
#
# 产物：
#   ./dist/kairo-<ver>/Kairo_win10.exe
#
# 默认 GOTOOLCHAIN=auto，必要时自动下载匹配工具链；离线构建请预装所需 Go 并设置 GOTOOLCHAIN=local。

set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -n "${1:-}" ]]; then
  VER="${1}"
elif [[ -f VERSION ]]; then
  VER="$(tr -d '[:space:]' < VERSION)"
  if git rev-parse --short HEAD >/dev/null 2>&1; then
    COMMIT="$(git rev-parse --short HEAD)"
    if git log -n 5 --pretty=%B 2>/dev/null | grep -q "v0.19-dev"; then
      VER="v0.19-dev-${COMMIT}"
    fi
  fi
else
  echo "错误：未指定版本号，且未找到 VERSION 文件" >&2
  echo "用法: $0 [版本号]  （或在仓库根目录维护 VERSION）" >&2
  exit 1
fi
OUT_DIR="dist/kairo-${VER}"
mkdir -p "${OUT_DIR}"
# 旧构建目录可能残留历史版本打包的 config.yaml；明确清掉，避免新 ZIP 覆盖用户配置。
rm -f "${OUT_DIR}/config.yaml"

BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
LDFLAGS="-s -w -H windowsgui -X 'kairo/internal/httpserver.Version=${VER}' -X 'kairo/internal/httpserver.BuildTime=${BUILD_TIME}'"

if ! command -v go >/dev/null 2>&1; then
  echo "错误：未找到 go 命令，请先安装 Go 1.24+ 并加入 PATH" >&2
  exit 1
fi

GO_VERSION="$(go version | awk '{print $3}')"
GO_MAJOR="$(echo "${GO_VERSION}" | sed -E 's/^go([0-9]+)\..*/\1/')"
GO_MINOR="$(echo "${GO_VERSION}" | sed -E 's/^go[0-9]+\.([0-9]+).*/\1/')"
if [[ "${GO_MAJOR}" -lt 1 || ( "${GO_MAJOR}" -eq 1 && "${GO_MINOR}" -lt 24 ) ]]; then
  echo "错误：主线要求 Go 1.24+，当前为 ${GO_VERSION}" >&2
  echo "Win7/Go 1.20 版本已移至 legacy 分支，不再参与主线构建。" >&2
  exit 1
fi
echo ">> Go 版本: ${GO_VERSION}"
echo ">> 目标:    Windows 10/11 amd64"

export GOOS=windows
export GOARCH=amd64
# 企业版 Windows 默认 godror/OCI（需本机 gcc + Oracle Client 运行时）
# 构建机需安装 MinGW-w64（winget: BrechtSanders.WinLibs.POSIX.UCRT）
# 运行时仍为单 exe，但依赖本机 oci.dll（PL/SQL Developer 同款 Oracle Client）
# A release must never silently change its driver capabilities.
export CGO_ENABLED="${CGO_ENABLED:-1}"
if [[ "${CGO_ENABLED}" == "1" ]]; then
  export CC="${CC:-gcc}"
  if ! command -v "${CC}" >/dev/null 2>&1; then
    echo "错误：OCI 企业版需要 Windows C 编译器；设置 CC，或显式设置 CGO_ENABLED=0 构建便携版" >&2
    exit 1
  fi
fi
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

# 优先用 vendor 模式编译：保证产物的依赖版本与仓库一致，
# 避免"开发机 go.sum 跟生产机 GOMODCACHE 不一致"导致的构建漂移。
# 如果 vendor/ 目录缺失（例如刚 clone 完没跑过 go mod vendor），
# 回退到默认 module 模式，并打印一次性提示。
# 注意：vendor 需以 CGO_ENABLED=1 生成才能包含 godror；否则按需回落
GO_MOD_FLAGS=()
if [[ -d vendor && -f vendor/modules.txt ]]; then
  if grep -q "godror" vendor/modules.txt 2>/dev/null; then
    GO_MOD_FLAGS=(-mod=vendor)
  else
    if [[ "${CGO_ENABLED}" == "1" ]]; then
      echo ">> vendor 缺少 godror，自动执行 go mod vendor（CGO_ENABLED=1）..."
      go mod vendor || true
      if grep -q "godror" vendor/modules.txt 2>/dev/null; then
        GO_MOD_FLAGS=(-mod=vendor)
      fi
    fi
    if [[ "${#GO_MOD_FLAGS[@]}" -eq 0 ]]; then
      echo ">> 警告：vendor 缺少 godror，回落到 module 模式"
    fi
  fi
else
  echo ">> 提示：vendor/ 目录缺失，回退到 module 模式（建议先跑 go mod vendor）"
fi

if [[ "${CGO_ENABLED}" == "1" ]]; then
  echo ">> 企业版构建：CGO_ENABLED=1，产物将动态链接 Oracle Client（oci.dll），运行时需本机已装 64 位 Oracle Client（与 PL/SQL Developer 同源，但需位数一致）"
else
  echo ">> 便携版构建：CGO_ENABLED=0（纯 Go，未启用 OCI）"
fi

# 嵌入 Windows EXE 图标（若存在 rsrc 工具）
if [[ -f "internal/tray/icon.ico" ]]; then
  RSRC_BIN=""
  if command -v rsrc >/dev/null 2>&1; then
    RSRC_BIN="rsrc"
  elif [[ -x "$(go env GOPATH 2>/dev/null)/bin/rsrc.exe" ]]; then
    RSRC_BIN="$(go env GOPATH)/bin/rsrc.exe"
  elif [[ -x "${HOME}/go/bin/rsrc" ]]; then
    RSRC_BIN="${HOME}/go/bin/rsrc"
  fi
  if [[ -n "${RSRC_BIN}" ]]; then
    echo ">> 正在嵌入 Windows 图标资源（rsrc_windows_amd64.syso）..."
    "${RSRC_BIN}" -ico "internal/tray/icon.ico" -arch amd64 -o "rsrc_windows_amd64.syso"
  fi
fi

go build "${GO_MOD_FLAGS[@]}" -trimpath -ldflags "${LDFLAGS}" -o "${OUT_DIR}/Kairo_win10.exe" .

# config.yaml 已作为首次启动模板嵌入 EXE。运行时副本位于用户配置目录，
# 因此发布目录只需要二进制，解压新版不会覆盖用户配置。
# README.md 不再打进产物目录：同事解压后看 README 没什么用，体积也大（68KB）。
# 文档统一走 docs/ 目录或仓库本身，需要时看 GitHub / GitLab 即可。
# 不再需要 start.bat：-H windowsgui 让双击 exe 无控制台窗口，
# 系统托盘提供"打开浏览器"和"退出"菜单。
# config.yaml、downloads/、logs/、data/ 均由 exe 在用户配置目录按需创建。

echo
echo ">> 已生成："
ls -lh "${OUT_DIR}/"
echo
echo ">> 产物目录：${OUT_DIR}/"
echo ">> 建议：把这个目录打包成 zip 发给同事（不要把 dist/ 整目录打进去）"
echo ">> 用法：双击 Kairo_win10.exe，托盘图标常驻右下角，右键退出"
