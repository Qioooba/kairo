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
# 注意：本脚本默认用本地 Go 工具链（不下载更新版本）。

set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -n "${1:-}" ]]; then
  VER="${1}"
elif [[ -f VERSION ]]; then
  VER="$(tr -d '[:space:]' < VERSION)"
else
  echo "错误：未指定版本号，且未找到 VERSION 文件" >&2
  echo "用法: $0 [版本号]  （或在仓库根目录维护 VERSION）" >&2
  exit 1
fi
OUT_DIR="dist/kairo-${VER}"
mkdir -p "${OUT_DIR}"

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
export CGO_ENABLED=0
export GOTOOLCHAIN=local

# 优先用 vendor 模式编译：保证产物的依赖版本与仓库一致，
# 避免"开发机 go.sum 跟生产机 GOMODCACHE 不一致"导致的构建漂移。
# 如果 vendor/ 目录缺失（例如刚 clone 完没跑过 go mod vendor），
# 回退到默认 module 模式，并打印一次性提示。
GO_MOD_FLAGS=()
if [[ -d vendor && -f vendor/modules.txt ]]; then
  GO_MOD_FLAGS=(-mod=vendor)
else
  echo ">> 提示：vendor/ 目录缺失，回退到 module 模式（建议先跑 go mod vendor）"
fi

go build "${GO_MOD_FLAGS[@]}" -trimpath -ldflags "${LDFLAGS}" -o "${OUT_DIR}/Kairo_win10.exe" .

# 复制运行所需文件
# config.yaml 是首选，但发布包里通常只有 config.yaml.production.example（占位 / 模板）。
# 优先用本地 config.yaml，没有就回退到 example，再没有就报错退出。
#
# 注意：无论走哪条路径，都必须剥离开发者白名单行（kairo: ...），
# 这是开发者自用旁路，不应进入生产分发包。详见 docs/KAIRO-LICENSE.md。
# 用 ^\s*kairo: 匹配所有非注释的 kairo 行（不管值的格式：引号、点号、空值等），
# 注释行（# kairo: ...）以 # 开头不会被误伤。
if [[ -f config.yaml ]]; then
  grep -v -E "^\s*kairo:" config.yaml \
    | awk 'BEGIN{skip=0} /^internal_endpoints:[[:space:]]*$/ {skip=1; next} skip==1 && /^[a-zA-Z_]/{skip=0} skip==0 {print}' \
    > "${OUT_DIR}/config.yaml"
elif [[ -f config.yaml.production.example ]]; then
  echo ">> 警告：未找到 config.yaml，使用 config.yaml.production.example 复制为 config.yaml"
  grep -v -E "^\s*kairo:" config.yaml.production.example \
    | awk 'BEGIN{skip=0} /^internal_endpoints:[[:space:]]*$/ {skip=1; next} skip==1 && /^[a-zA-Z_]/{skip=0} skip==0 {print}' \
    > "${OUT_DIR}/config.yaml"
else
  echo "错误：找不到 config.yaml 或 config.yaml.production.example" >&2
  exit 1
fi
# README.md 不再打进产物目录：同事解压后看 README 没什么用，体积也大（68KB）。
# 文档统一走 docs/ 目录或仓库本身，需要时看 GitHub / GitLab 即可。
# 不再需要 start.bat：-H windowsgui 让双击 exe 无控制台窗口，
# 系统托盘提供"打开浏览器"和"退出"菜单。
# downloads/ logs/ data/ 由 exe 启动时自动创建，无需预置。

echo
echo ">> 已生成："
ls -lh "${OUT_DIR}/"
echo
echo ">> 产物目录：${OUT_DIR}/"
echo ">> 建议：把这个目录打包成 zip 发给同事（不要把 dist/ 整目录打进去）"
echo ">> 用法：双击 Kairo_win10.exe，托盘图标常驻右下角，右键退出"
