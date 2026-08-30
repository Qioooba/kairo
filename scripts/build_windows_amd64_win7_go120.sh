#!/usr/bin/env bash
# Win7 构建已从主线移除。保留此入口，让旧 CI 明确失败并给出迁移方向，
# 避免拿 Go 1.20 强行编译 go 1.24 主线后得到不完整或不可复现的产物。

set -euo pipefail

echo "错误：主线不再支持 Win7 / Go 1.20 构建。" >&2
echo "请切换到项目的 Win7 legacy 分支维护和发布旧平台版本。" >&2
exit 2
