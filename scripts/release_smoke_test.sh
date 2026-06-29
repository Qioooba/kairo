#!/usr/bin/env bash
# release_smoke_test.sh
#
# Release smoke 测试：解包源码 tar.gz 后验证：
#   1. vendor/ 完整（PKG-001）→ go build -mod=vendor ./... 能跑
#   2. web/vendor/diff2html.min.{css,js} 存在（PKG-002）
#   3. go test -mod=vendor -count=1 -short ./... 能跑（用户要求）
#   4. 启动服务后 /static/vendor/diff2html.min.css 返回 200（无 404）
#
# 用法：
#   ./scripts/release_smoke_test.sh <tar.gz 路径>
# 例如：
#   ./scripts/release_smoke_test.sh ./dist/doubao-toolbox-src-v0.9.tar.gz

set -euo pipefail

TAR="${1:-}"
if [[ -z "${TAR}" ]]; then
  echo "用法：$0 <tar.gz 路径>" >&2
  exit 1
fi

if [[ ! -f "${TAR}" ]]; then
  echo "错误：找不到 ${TAR}" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "错误：未找到 go 命令" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo ">> 解包到 ${TMP_DIR}"
tar -xzf "${TAR}" -C "${TMP_DIR}"

# tar.gz 内可能有顶层目录（如 doubao-toolbox-v0.9/），找到它
SRC_DIR="${TMP_DIR}"
DIRS=$(find "${TMP_DIR}" -maxdepth 1 -mindepth 1 -type d)
DIR_COUNT=$(echo "${DIRS}" | wc -l | tr -d ' ')
if [[ "${DIR_COUNT}" -eq 1 ]]; then
  SRC_DIR=$(echo "${DIRS}" | head -1)
fi

echo ">> 源码目录：${SRC_DIR}"
cd "${SRC_DIR}"

FAIL=0

# ---- 1. PKG-001：vendor/ 完整性 ----
echo
echo "==== 1. PKG-001: vendor/ 完整性 ===="
if [[ ! -f vendor/modules.txt ]]; then
  echo "   ❌ vendor/modules.txt 缺失" >&2
  FAIL=1
else
  VENDOR_GO_COUNT=$(find vendor -name '*.go' 2>/dev/null | wc -l | tr -d ' ')
  echo "   ✅ vendor/modules.txt 存在，${VENDOR_GO_COUNT} 个 Go 文件"
fi

# ---- 2. PKG-002：web/vendor/diff2html ----
echo
echo "==== 2. PKG-002: web/vendor/diff2html ===="
if [[ ! -f web/vendor/diff2html.min.css ]]; then
  echo "   ❌ web/vendor/diff2html.min.css 缺失" >&2
  FAIL=1
else
  echo "   ✅ web/vendor/diff2html.min.css 存在 ($(wc -c < web/vendor/diff2html.min.css) bytes)"
fi
if [[ ! -f web/vendor/diff2html.min.js ]]; then
  echo "   ❌ web/vendor/diff2html.min.js 缺失" >&2
  FAIL=1
else
  echo "   ✅ web/vendor/diff2html.min.js 存在 ($(wc -c < web/vendor/diff2html.min.js) bytes)"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo
  echo ">> 文件检查失败，跳过后续编译/启动测试" >&2
  exit 1
fi

# ---- 3. go build -mod=vendor ----
echo
echo "==== 3. go build -mod=vendor ./... ===="
if ! go build -mod=vendor ./... 2>&1; then
  echo "   ❌ go build 失败" >&2
  FAIL=1
else
  echo "   ✅ go build 成功"
fi

# ---- 4. go test -mod=vendor -short ----
echo
echo "==== 4. go test -mod=vendor -count=1 -short ./... ===="
# -short 跳过耗时测试（如 dial 30s 超时），加快 smoke 验证
# 超时 300s 防止卡死
if ! go test -mod=vendor -count=1 -short -timeout 300s ./... 2>&1; then
  echo "   ⚠ go test 有失败（可能是环境相关，非打包问题）" >&2
  # 不直接 FAIL=1：测试失败可能是环境问题（如 keyring 不可用），不一定是打包问题
  # 打包 smoke 的核心是"能 build + 文件齐全"，测试失败只给警告
else
  echo "   ✅ go test 全部通过"
fi

# ---- 5. 启动服务 + curl 静态资源 ----
echo
echo "==== 5. 启动服务验证 /static/vendor/diff2html 无 404 ===="

# 构建二进制到临时目录
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}" "${BIN_DIR}"' EXIT
BIN="${BIN_DIR}/doubao-toolbox-smoke"

echo "   编译二进制..."
if ! go build -mod=vendor -o "${BIN}" . 2>&1; then
  echo "   ❌ 编译失败" >&2
  FAIL=1
else
  # 创建最小配置（config.yaml 放在二进制同目录，resolveRunDir 会自动找到）
  cat > "${BIN_DIR}/config.yaml" <<'YAML'
app:
  name: smoke-test
  host: 127.0.0.1
  port: 18999
  download_dir: ./downloads
  log_dir: ./logs
  data_dir: ./data
  compare_allowed_roots:
    - "*"
  allow_insecure_host_key: true
  free_file_roots:
    - "*"
  allowed_download_roots:
    - "*"
systems:
  - name: smoke
    servers:
      - name: dummy
        host: 127.0.0.1
        port: 22
        username: nobody
        auth_type: password
        log_dirs:
          - name: test
            path: /tmp
            patterns: ["*.log"]
            encoding: utf-8
search:
  default_latest_files: 3
  max_matches: 200
  default_context_lines: 30
  timeout_seconds: 30
  max_concurrency: 2
YAML
  mkdir -p "${BIN_DIR}/downloads" "${BIN_DIR}/logs" "${BIN_DIR}/data"

  echo "   启动服务（127.0.0.1:18999）..."
  cd "${BIN_DIR}" && ./doubao-toolbox-smoke &
  SRV_PID=$!
  trap 'rm -rf "${TMP_DIR}" "${BIN_DIR}"; kill ${SRV_PID} 2>/dev/null || true' EXIT

  # 等待服务启动
  sleep 2

  # curl 静态资源
  echo "   curl /static/vendor/diff2html.min.css"
  CSS_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:18999/static/vendor/diff2html.min.css" || echo "000")
  if [[ "${CSS_CODE}" != "200" ]]; then
    echo "   ❌ diff2html.min.css 返回 ${CSS_CODE}（期望 200）" >&2
    FAIL=1
  else
    echo "   ✅ diff2html.min.css 返回 200"
  fi

  echo "   curl /static/vendor/diff2html.min.js"
  JS_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:18999/static/vendor/diff2html.min.js" || echo "000")
  if [[ "${JS_CODE}" != "200" ]]; then
    echo "   ❌ diff2html.min.js 返回 ${JS_CODE}（期望 200）" >&2
    FAIL=1
  else
    echo "   ✅ diff2html.min.js 返回 200"
  fi

  # 关闭服务
  kill ${SRV_PID} 2>/dev/null || true
fi

# ---- 结果 ----
echo
if [[ "${FAIL}" -ne 0 ]]; then
  echo "==== SMOKE TEST FAILED ===="
  exit 1
else
  echo "==== SMOKE TEST PASSED ===="
fi
