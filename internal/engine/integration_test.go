//go:build integration

// Package engine 集成测试 — 验证完整评测管线。
//
// 这些测试启动完整的引擎管线（DB + 内存调度器），
// 验证从"创建任务 → 执行 → 报告"的完整流程。
//
// 运行方式:
//
//	go test -v -count=1 -run TestIntegration ./internal/engine/
//
// 注意: 不需要真实 API Key(mock 回退模式自动启用)。
package engine

import (
	"encoding/json"
	"testing"
	"time"

	"ai-eval-platform/internal/scheduler"
	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// createIntegrationTask 创建一条评测任务并返回 ID。
func createIntegrationTask(t *testing.T, name, agentID, ruleSet string) string {
	t.Helper()
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	_, err := store.DB.Exec(`INSERT INTO tasks (id, name, scene, agent_id, rule_set, status, chaos_enabled, created_at, updated_at)
		VALUES (?, ?, 'agent', ?, ?, 'pending', 0, ?, ?)`,
		id, name, agentID, ruleSet, now, now)
	if err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}
	return id
}

// waitForTaskIntegration 轮询等待任务完成。
func waitForTaskIntegration(t *testing.T, taskID string, timeout time.Duration) *store.TaskRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(taskID)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if task.Status == "done" || task.Status == "failed" || task.Status == "blown" {
			return task
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// TestIntegrationFullPipeline 测试引擎完整管线: 创建 → 执行 → 报告。
func TestIntegrationFullPipeline(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// 初始化调度器
	scheduler.InitGlobal(Run)
	defer scheduler.StopGlobal()

	agent, err := store.GetAgent("a-codegen")
	if err != nil {
		t.Fatalf("获取 Agent 失败: %v", err)
	}

	taskID := createIntegrationTask(t, "集成测试-完整管线", agent.ID, `{"rule_set_id":"rs-fullstack"}`)

	if err := scheduler.SubmitGlobalHigh(taskID); err != nil {
		t.Fatalf("提交任务失败: %v", err)
	}

	task := waitForTaskIntegration(t, taskID, 60*time.Second)
	if task == nil {
		t.Fatal("任务超时(60s 仍未完成)")
	}

	if task.Status != "done" {
		t.Fatalf("评测未正常完成: status=%s", task.Status)
	}

	t.Logf("✅ 评测完成: status=%s", task.Status)

	var report map[string]interface{}
	if err := json.Unmarshal([]byte(task.Result), &report); err != nil {
		t.Fatalf("解析报告失败: %v", err)
	}

	overall, _ := report["overall"].(float64)
	dims, _ := report["dimensions"].(map[string]interface{})
	perQ, _ := report["per_question"].([]interface{})

	t.Logf("   综合分: %.1f", overall)
	t.Logf("   维度: %v", dims)
	t.Logf("   题数: %d", len(perQ))
}

// TestIntegrationBadCase 测试引擎 Bad Case 检测。
func TestIntegrationBadCase(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	scheduler.InitGlobal(Run)
	defer scheduler.StopGlobal()

	taskID := createIntegrationTask(t, "集成测试-BadCase", "a-debugger", `{"rule_set_id":"rs-security"}`)

	scheduler.SubmitGlobalHigh(taskID)

	task := waitForTaskIntegration(t, taskID, 60*time.Second)
	if task == nil {
		t.Fatal("任务超时")
	}

	t.Logf("安全评测: status=%s", task.Status)
	if task.Status != "done" {
		return
	}

	var report map[string]interface{}
	json.Unmarshal([]byte(task.Result), &report)
	badCases, _ := report["bad_cases"].(float64)
	t.Logf("   Bad Case 数: %.0f", badCases)
}

// TestIntegrationAttribution 测试归因闭环。
func TestIntegrationAttribution(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// 先创建任务(attribution 依赖任务存在)
	taskID := createIntegrationTask(t, "集成测试-归因", "a-codegen", `{"rule_set_id":"rs-fullstack"}`)

	// 插入一条 Bad Case
	bcID := uuid.New().String()
	store.InsertBadCase(bcID, taskID, "q-2", "a-codegen",
		"不安全的代码: db.Query(fmt.Sprintf(...))", 30.0, "auto_low_score")

	// 执行归因
	attr, err := RunAttribution(bcID)
	if err != nil {
		t.Fatalf("归因执行失败: %v", err)
	}

	t.Logf("✅ 归因完成: root_cause=%s confidence=%.1f suggested_fix=%s",
		attr.RootCause, attr.Confidence, truncateStr(attr.SuggestedFix, 60))

	if attr.RootCause == "" {
		t.Error("归因根因不应为空")
	}
}

// TestIntegrationSandbox 测试沙箱(不依赖编译器)。
func TestIntegrationSandbox(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	result := RunSandbox(`package main
func main() {
	println("hello")
}`)

	t.Logf("沙箱: lang=%s success=%v score=%.0f", result.Language, result.Success, result.Score)
}

// TestIntegrationAllQuestions 测试种子题库完整性。
func TestIntegrationAllQuestions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	questions, err := store.GetQuestionsByScene("agent")
	if err != nil {
		t.Fatalf("获取题目失败: %v", err)
	}

	t.Logf("Agent 场景有 %d 道种子题:", len(questions))
	for _, q := range questions {
		t.Logf("   • [%s] %s (难度: %s)", q.Source, q.Title, q.Difficulty)
	}

	if len(questions) == 0 {
		t.Error("种子题库为空")
	}
}

// TestIntegrationEngineStatus 测试任务状态机。
func TestIntegrationEngineStatus(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	scheduler.InitGlobal(Run)
	defer scheduler.StopGlobal()

	taskID := createIntegrationTask(t, "集成测试-状态", "a-codegen", `{"rule_set_id":"rs-fullstack"}`)

	// 验证 pending 状态
	task, _ := store.GetTask(taskID)
	if task.Status != "pending" {
		t.Errorf("初始状态应为 pending, 实际 %s", task.Status)
	}

	scheduler.SubmitGlobalHigh(taskID)
	time.Sleep(1 * time.Second)

	// 等待完成
	task = waitForTaskIntegration(t, taskID, 60*time.Second)
	if task == nil {
		t.Fatal("任务超时")
	}

	if task.Status != "done" {
		t.Errorf("最终状态应为 done, 实际 %s", task.Status)
	}
}
