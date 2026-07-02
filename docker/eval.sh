#!/bin/sh
# eval.sh — Docker 沙箱统一执行脚本(MRD 5.3)。
#
# 用法: /eval.sh <code_file> <language> [timeout_sec]
#
# 输入:
#   - 代码文件已通过 Docker volume 挂载到 /code/<code_file>
#   - language: go | python | javascript
#   - timeout_sec: 超时秒数(默认 30)
#
# 输出(JSON 到 stdout):
#   {"success": true/false, "compile_time": 123, "language": "go",
#    "compile_error": "", "test_passed": 3, "test_failed": 1, "test_total": 4,
#    "lint_errors": 2, "score": 85, "is_mock": false}
#
# 设计:
#   - 每种语言独立处理
#   - 编译 + 测试 + Lint 三个阶段
#   - 超时由 Docker 的 --stop-timeout 处理(本脚本内也有 timeout 保护)
#   - 输出统一 JSON,供 parseDockerOutput 解析

set -e

FILE="$1"
LANG="$2"
TIMEOUT="${3:-30}"

# 切换到代码挂载目录
cd /code 2>/dev/null || cd /tmp

# 输出 JSON 的辅助函数
output_json() {
  # 对字符串中的双引号做转义
  compile_err=$(echo "$3" | sed 's/"/\\"/g' | head -20)
  cat <<JSON
{"success":$1,"compile_time":$2,"language":"$LANG",
 "compile_error":"$compile_err",
 "test_passed":${4:-0},"test_failed":${5:-0},"test_total":${6:-0},
 "lint_errors":${7:-0},"score":${8:-0},"is_mock":false}
JSON
}

output_success() {
  compile_time="$1"
  test_passed="${2:-0}"
  test_failed="${3:-0}"
  lint_errors="${4:-0}"
  test_total=$((test_passed + test_failed))
  score=70
  if [ "$test_total" -gt 0 ]; then
    score=$((50 + (test_passed * 50) / test_total))
  fi
  [ "$score" -gt 100 ] && score=100
  output_json true "$compile_time" "" "$test_passed" "$test_failed" "$test_total" "$lint_errors" "$score"
}

output_fail() {
  compile_time="$1"
  err="$2"
  output_json false "$compile_time" "$err" 0 0 0 0 20
}

# ── 语言编译/检查 ──

case "$LANG" in
  go)
    # 编译
    START=$(date +%s%3N 2>/dev/null || echo 0)
    COMPILE_OUT=$(cd /code && go build -o /tmp/sandbox_out "./$FILE" 2>&1) || {
      END=$(date +%s%3N 2>/dev/null || echo "$START")
      ELAPSED=$((END - START))
      output_fail "$ELAPSED" "$COMPILE_OUT"
      exit 0
    }
    END=$(date +%s%3N 2>/dev/null || echo "$START")
    COMPILE_TIME=$((END - START))

    # 测试(查找 _test.go 文件)
    TEST_PASSED=0
    TEST_FAILED=0
    if ls /code/*_test.go >/dev/null 2>&1; then
      TEST_OUT=$(cd /code && go test -v -count=1 -timeout="${TIMEOUT}s" ./... 2>&1) || true
      TEST_PASSED=$(echo "$TEST_OUT" | grep -c '^--- PASS:' || echo 0)
      TEST_FAILED=$(echo "$TEST_OUT" | grep -c '^--- FAIL:' || echo 0)
    fi

    # Lint(go vet)
    LINT_OUT=$(cd /code && go vet "./$FILE" 2>&1) || true
    LINT_COUNT=$(echo "$LINT_OUT" | grep -c '^' || echo 0)
    [ "$LINT_OUT" = "" ] && LINT_COUNT=0

    output_success "$COMPILE_TIME" "$TEST_PASSED" "$TEST_FAILED" "$LINT_COUNT"
    ;;

  python)
    # 语法检查(py_compile)
    START=$(date +%s%3N 2>/dev/null || echo 0)
    COMPILE_OUT=$(python -m py_compile "/code/$FILE" 2>&1) || {
      END=$(date +%s%3N 2>/dev/null || echo "$START")
      ELAPSED=$((END - START))
      output_fail "$ELAPSED" "$COMPILE_OUT"
      exit 0
    }
    END=$(date +%s%3N 2>/dev/null || echo "$START")
    COMPILE_TIME=$((END - START))

    # 测试(如果存在 test_*.py)
    TEST_PASSED=0
    TEST_FAILED=0
    if ls /code/test_*.py >/dev/null 2>&1 || ls /code/*_test.py >/dev/null 2>&1; then
      TEST_OUT=$(cd /code && python -m pytest --tb=short -q 2>&1) || true
      TEST_PASSED=$(echo "$TEST_OUT" | grep -oE '[0-9]+ passed' | grep -oE '[0-9]+' || echo 0)
      TEST_FAILED=$(echo "$TEST_OUT" | grep -oE '[0-9]+ failed' | grep -oE '[0-9]+' || echo 0)
    fi

    # Lint(pylint --errors-only, 如果安装了)
    LINT_COUNT=0
    if command -v pylint >/dev/null 2>&1; then
      LINT_OUT=$(pylint --errors-only "/code/$FILE" 2>&1) || true
      LINT_COUNT=$(echo "$LINT_OUT" | grep -c '^E:' || echo 0)
    fi

    output_success "$COMPILE_TIME" "$TEST_PASSED" "$TEST_FAILED" "$LINT_COUNT"
    ;;

  javascript)
    # 语法检查(node --check)
    START=$(date +%s%3N 2>/dev/null || echo 0)
    COMPILE_OUT=$(node --check "/code/$FILE" 2>&1) || {
      END=$(date +%s%3N 2>/dev/null || echo "$START")
      ELAPSED=$((END - START))
      output_fail "$ELAPSED" "$COMPILE_OUT"
      exit 0
    }
    END=$(date +%s%3N 2>/dev/null || echo "$START")
    COMPILE_TIME=$((END - START))

    # 测试(如果存在 *.test.js)
    TEST_PASSED=0
    TEST_FAILED=0
    if ls /code/*.test.js >/dev/null 2>&1; then
      # 尝试用 jest 或 node 内置 test runner
      if command -v jest >/dev/null 2>&1; then
        TEST_OUT=$(cd /code && jest --passWithNoTests 2>&1) || true
      elif command -v npx >/dev/null 2>&1; then
        TEST_OUT=$(cd /code && npx jest --passWithNoTests 2>&1) || true
      fi
      TEST_PASSED=$(echo "$TEST_OUT" | grep -oE '[0-9]+ passed' | grep -oE '[0-9]+' || echo 0)
      TEST_FAILED=$(echo "$TEST_OUT" | grep -oE '[0-9]+ failed' | grep -oE '[0-9]+' || echo 0)
    fi

    # Lint(eslint, 如果安装了)
    LINT_COUNT=0
    if command -v eslint >/dev/null 2>&1; then
      LINT_OUT=$(eslint "/code/$FILE" 2>&1) || true
      LINT_COUNT=$(echo "$LINT_OUT" | grep -oE '[0-9]+ error' | grep -oE '[0-9]+' || echo 0)
    fi

    output_success "$COMPILE_TIME" "$TEST_PASSED" "$TEST_FAILED" "$LINT_COUNT"
    ;;

  *)
    echo '{"success":false,"compile_time":0,"language":"'"$LANG"'","compile_error":"不支持的语言: '"$LANG"'","score":0,"is_mock":true}'
    exit 1
    ;;
esac
