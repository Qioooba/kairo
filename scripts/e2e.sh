#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEST_RESULTS_DIR="${PROJECT_ROOT}/test-results"

BASE_URL="${BASE_URL:-http://127.0.0.1:18092}"
WITH_MOCK=false
HEADED=false

MOCK_SSH_PORT=2225
MOCK_SSH_USER=test
MOCK_SSH_PASS=test

MAIN_PID=""
MOCK_SSH_PID=""
TEST_EXIT_CODE=0

function print_help() {
  echo "用法: $0 [选项]"
  echo ""
  echo "选项："
  echo "  --with-mock       自动启动 mock SSH 和主服务"
  echo "  --headed          有头模式运行（显示浏览器）"
  echo "  --base-url <url>  指定服务地址（默认: ${BASE_URL}）"
  echo "  --help            显示帮助"
  echo ""
  echo "环境变量："
  echo "  BASE_URL          服务地址"
  echo "  HEADLESS=false    等价于 --headed"
}

function parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --with-mock)
        WITH_MOCK=true
        shift
        ;;
      --headed)
        HEADED=true
        shift
        ;;
      --base-url)
        BASE_URL="$2"
        shift 2
        ;;
      --help|-h)
        print_help
        exit 0
        ;;
      *)
        echo "未知选项: $1" >&2
        print_help
        exit 1
        ;;
    esac
  done
}

function check_node() {
  if ! command -v node >/dev/null 2>&1; then
    echo "错误：未找到 node 命令，请先安装 Node.js" >&2
    exit 1
  fi
  echo "✅ Node.js: $(node --version)"
}

function prepare_fixtures() {
  echo "==> 准备 E2E fixtures..."
  if [[ -f "${PROJECT_ROOT}/scripts/e2e-prepare-fixtures.js" ]]; then
    node "${PROJECT_ROOT}/scripts/e2e-prepare-fixtures.js"
    echo "✅ Fixtures 准备完成"
  else
    echo "⚠️  未找到 e2e-prepare-fixtures.js，跳过 fixture 准备" >&2
  fi
}

function check_service() {
  local url="$1"
  echo "==> 检查服务是否可达: ${url}"
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" "${url}" --max-time 5 2>/dev/null || echo "000")
  if [[ "${code}" == "000" ]]; then
    echo "❌ 服务不可达: ${url}" >&2
    return 1
  fi
  echo "✅ 服务可达 (HTTP ${code})"
  return 0
}

function start_mock_ssh() {
  echo "==> 启动 mock SSH 服务..."
  if [[ -f "${PROJECT_ROOT}/scripts/mock_sshd.py" ]]; then
    MOCK_SSHD_PASSWORD="${MOCK_SSH_PASS}" MOCK_SSHD_PORT="${MOCK_SSH_PORT}" python3 "${PROJECT_ROOT}/scripts/mock_sshd.py" &
    MOCK_SSH_PID=$!
    echo "    Mock SSH PID: ${MOCK_SSH_PID} (port: ${MOCK_SSH_PORT}, password: ${MOCK_SSH_PASS})"
    sleep 1
  else
    echo "⚠️  未找到 mock_sshd.py，跳过 mock SSH 启动" >&2
  fi
}

function start_main_service() {
  echo "==> 启动主服务..."
  if command -v go >/dev/null 2>&1 && [[ -f "${PROJECT_ROOT}/main.go" ]]; then
    cd "${PROJECT_ROOT}"
    go run . &
    MAIN_PID=$!
    echo "    主服务 PID: ${MAIN_PID}"
    echo "    等待服务启动..."
    for i in {1..30}; do
      if check_service "${BASE_URL}" >/dev/null 2>&1; then
        echo "✅ 主服务已启动"
        return 0
      fi
      sleep 1
    done
    echo "❌ 主服务启动超时" >&2
    return 1
  else
    echo "⚠️  未找到 go 或 main.go，跳过主服务启动" >&2
  fi
}

function create_results_dir() {
  echo "==> 创建结果目录..."
  mkdir -p "${TEST_RESULTS_DIR}/screenshots"
  mkdir -p "${TEST_RESULTS_DIR}/network"
  mkdir -p "${TEST_RESULTS_DIR}/console"
  mkdir -p "${TEST_RESULTS_DIR}/json-report"
  echo "✅ 结果目录: ${TEST_RESULTS_DIR}"
}

function run_tests() {
  echo "==> 运行 E2E 测试..."
  cd "${PROJECT_ROOT}"

  local extra_args=()
  if [[ "${HEADED}" == true ]]; then
    extra_args+=("--headed")
  fi

  set +e
  BASE_URL="${BASE_URL}" node tests/e2e/index.js "${extra_args[@]}"
  TEST_EXIT_CODE=$?
  set -e

  echo ""
  echo "==> 测试退出码: ${TEST_EXIT_CODE}"
}

function print_report_summary() {
  local report_path="${TEST_RESULTS_DIR}/report.md"
  if [[ -f "${report_path}" ]]; then
    echo ""
    echo "========================================"
    echo "  报告已生成: ${report_path}"
    echo "========================================"
  fi
}

function cleanup() {
  echo ""
  echo "==> 清理..."
  if [[ -n "${MAIN_PID}" ]]; then
    echo "    停止主服务 (PID: ${MAIN_PID})"
    kill "${MAIN_PID}" 2>/dev/null || true
    wait "${MAIN_PID}" 2>/dev/null || true
  fi
  if [[ -n "${MOCK_SSH_PID}" ]]; then
    echo "    停止 mock SSH (PID: ${MOCK_SSH_PID})"
    kill "${MOCK_SSH_PID}" 2>/dev/null || true
    wait "${MOCK_SSH_PID}" 2>/dev/null || true
  fi
}

function main() {
  parse_args "$@"

  echo "========================================"
  echo "  Doubao Toolbox E2E 测试"
  echo "========================================"
  echo ""

  check_node
  prepare_fixtures

  if [[ "${WITH_MOCK}" == true ]]; then
    trap cleanup EXIT
    start_mock_ssh
    start_main_service
  else
    if ! check_service "${BASE_URL}"; then
      echo ""
      echo "提示：请先启动服务，或使用 --with-mock 参数自动启动 mock 环境" >&2
      echo "  用法: $0 --with-mock" >&2
      exit 1
    fi
  fi

  echo ""
  create_results_dir

  echo ""
  run_tests

  print_report_summary

  echo ""
  if [[ "${TEST_EXIT_CODE}" -eq 0 ]]; then
    echo "🎉 所有测试通过！"
  else
    echo "⚠️  测试有失败，请查看报告: ${TEST_RESULTS_DIR}/report.md"
  fi

  exit "${TEST_EXIT_CODE}"
}

main "$@"
