#!/bin/bash
# AI Eval Platform — 快速演示脚本
# 用法: bash scripts/demo.sh
# 前置条件: 服务器已在 :8080 运行

BASE="http://localhost:8080/api"
PASS=0
FAIL=0

check() {
    local desc="$1"
    local method="$2"
    local url="$3"
    local data="$4"
    local expect="$5"
    
    if [ "$method" = "GET" ]; then
        resp=$(curl -s "$url" 2>/dev/null)
    else
        resp=$(curl -s -X "$method" "$url" -H "Content-Type: application/json" -d "$data" 2>/dev/null)
    fi
    
    if echo "$resp" | grep -q "$expect"; then
        echo "  ✅ $desc"
        PASS=$((PASS+1))
    else
        echo "  ❌ $desc"
        echo "     期望包含: $expect"
        echo "     实际: ${resp:0:100}"
        FAIL=$((FAIL+1))
    fi
}

echo "══════════════════════════════════════"
echo "  AI Eval Platform — 功能演示"
echo "══════════════════════════════════════"
echo ""
echo "  服务器: $BASE"
echo ""

# ── 1. 健康检查 ──
echo "【1/8】基础设施"
check "健康检查" "GET" "$BASE/health" "" "ok"

# ── 2. 资源管理 ──
echo "【2/8】资源管理"
check "列出 Agents" "GET" "$BASE/agents" "" "CodeGen"
check "列出 模型" "GET" "$BASE/models" "" "glm-4"
check "列出 规则" "GET" "$BASE/rules" "" "security"
check "列出 场景" "GET" "$BASE/scenes" "" "Agent评测"

# ── 3. 题库 ──
echo "【3/8】题库管理"
check "列出 题目" "GET" "$BASE/questions" "" "计数器"
check "创建 题目" "POST" "$BASE/questions" \
    '{"title":"Go 错误处理","prompt":"解释 Go 的 error handling 最佳实践","difficulty":"mid","scene":"agent"}' \
    "Go 错误处理"

# ── 4. 规则 ──
echo "【4/8】规则管理"
check "规则图谱" "GET" "$BASE/rules/graph" "" "nodes"
check "导出规则" "GET" "$BASE/rule-sets/rs-fullstack/export" "" "rule_set"

# ── 5. 评测任务 ──
echo "【5/8】评测任务"
TASK_RESP=$(curl -s -X POST "$BASE/tasks" \
    -H "Content-Type: application/json" \
    -d '{"name":"演示-代码评测","agent_id":"a-codegen","scene":"agent","rule_set":"{\"rule_set_id\":\"rs-fullstack\"}"}')
TASK_ID=$(echo "$TASK_RESP" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

if [ -n "$TASK_ID" ]; then
    echo "  ✅ 创建评测任务: ${TASK_ID:0:8}"
    PASS=$((PASS+1))
    
    # 等待引擎执行
    echo "    等待引擎执行..."
    for i in $(seq 1 20); do
        TASK=$(curl -s "$BASE/tasks/$TASK_ID")
        STATUS=$(echo "$TASK" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
        if [ "$STATUS" = "done" ]; then
            echo "  ✅ 任务完成: status=done"
            PASS=$((PASS+1))
            
            RESULT=$(echo "$TASK" | grep -o '"overall":[0-9.]*')
            echo "     综合分: $RESULT"
            break
        fi
        sleep 1
    done
else
    echo "  ❌ 创建任务失败"
    FAIL=$((FAIL+2))
fi

# ── 6. 排行榜 ──
echo "【6/8】排行榜"
check "排行榜数据" "GET" "$BASE/stats/leaderboard" "" "entries"
check "看板统计" "GET" "$BASE/stats/dashboard" "" "total_tasks"

# ── 7. SDK ──
echo "【7/8】SDK"
check "SDK 文档" "GET" "$BASE/sdk/docs" "" "sdk_version"

# ── 8. 高级功能 ──
echo "【8/8】高级功能"
check "Bad Cases" "GET" "$BASE/bad-cases" "" ""
check "归因记录" "GET" "$BASE/attributions" "" ""
check "规则建议" "GET" "$BASE/feedback/suggestions" "" ""

# ── 结果 ──
echo ""
echo "══════════════════════════════════════"
echo "  演示结果"
echo "══════════════════════════════════════"
echo "  通过: $PASS"
echo "  失败: $FAIL"
echo "  总览: $(curl -s "$BASE/stats/dashboard" | grep -o '"total_tasks":[0-9]*')"
echo "══════════════════════════════════════"
