#!/usr/bin/env bash
# 兼容入口：主线只打 Win10/11 单版本包。

set -euo pipefail

cd "$(dirname "$0")/.."
echo ">> package_windows_both.sh 已废弃，改为生成主线 Windows 单版本包。"
exec ./scripts/package_windows.sh "${1:-}"
