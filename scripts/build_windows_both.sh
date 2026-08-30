#!/usr/bin/env bash
# 兼容入口：数据库工作台 v1 起主线升级为 Go 1.24+，不再双构建 Win7。

set -euo pipefail

cd "$(dirname "$0")/.."
echo ">> build_windows_both.sh 已废弃：主线只发布 Win10/11（Go 1.24+）。"
echo ">> Win7 产物请从 legacy 分支构建，不与主线依赖混用。"
exec ./scripts/build_windows_amd64.sh "${1:-}"
