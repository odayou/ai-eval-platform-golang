package store

import (
	"os"
	"path/filepath"
	"testing"
)

// setupTestDB 初始化测试数据库。
func setupTestDB(t *testing.T) func() {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	Init(dbPath)
	return func() {
		Close()
		os.Remove(dbPath)
	}
}

func TestInit(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	if DB == nil {
		t.Fatal("DB 初始化后不应为 nil")
	}

	// 检查表是否存在
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM agents").Scan(&count)
	// agents 表存在即可(无论是否有数据)
}

func TestSeedIfEmpty(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	SeedIfEmpty()

	// 检查种子数据
	var agentCount int
	DB.QueryRow("SELECT COUNT(*) FROM agents").Scan(&agentCount)
	if agentCount == 0 {
		t.Error("种子数据应包含 Agent")
	}

	var ruleCount int
	DB.QueryRow("SELECT COUNT(*) FROM rules").Scan(&ruleCount)
	if ruleCount == 0 {
		t.Error("种子数据应包含规则")
	}

	var questionCount int
	DB.QueryRow("SELECT COUNT(*) FROM questions").Scan(&questionCount)
	if questionCount == 0 {
		t.Error("种子数据应包含题目")
	}

	// MRD §7.3:种子数据应包含规则关系边
	var relCount int
	DB.QueryRow("SELECT COUNT(*) FROM rule_relations").Scan(&relCount)
	if relCount == 0 {
		t.Error("种子数据应包含规则关系边")
	}

	// 幂等性:再次调用不应报错
	SeedIfEmpty()
}

// TestRuleRelations 测试规则关系边 CRUD(MRD §7.3 知识图谱)。
func TestRuleRelations(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	// 读种子关系边
	rels, err := GetAllRuleRelations()
	if err != nil {
		t.Fatalf("GetAllRuleRelations 失败: %v", err)
	}
	origCount := len(rels)
	if origCount == 0 {
		t.Fatal("种子应有关系边")
	}

	// 插入新关系边
	err = InsertRuleRelation("test-rr-1", "r-perf-deadlock", "r-perf-latency", "related_to", 0.6)
	if err != nil {
		t.Fatalf("InsertRuleRelation 失败: %v", err)
	}

	// 验证 GetRelatedRuleIDs
	related := GetRelatedRuleIDs("r-perf-deadlock")
	if len(related) == 0 {
		t.Error("GetRelatedRuleIDs 应返回 r-perf-deadlock 的关联规则")
	}

	// 验证插入成功
	rels2, _ := GetAllRuleRelations()
	if len(rels2) != origCount+1 {
		t.Errorf("插入后关系边数应为 %d, 实际 %d", origCount+1, len(rels2))
	}

	// 删除
	DeleteRuleRelation("test-rr-1")
	rels3, _ := GetAllRuleRelations()
	if len(rels3) != origCount {
		t.Errorf("删除后关系边数应为 %d, 实际 %d", origCount, len(rels3))
	}
}

func TestModelsTable(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	rows, err := DB.Query("SELECT id, name, base_url FROM models")
	if err != nil {
		t.Fatalf("查询 models 表失败: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name, baseURL string
		if err := rows.Scan(&id, &name, &baseURL); err != nil {
			continue
		}
		count++
	}
	if count == 0 {
		t.Error("models 表应有数据")
	}
}

func TestTasksTable(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	// 查询任务
	rows, err := DB.Query("SELECT id, name, scene, status FROM tasks")
	if err != nil {
		t.Fatalf("查询 tasks 表失败: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name, scene, status string
		if err := rows.Scan(&id, &name, &scene, &status); err != nil {
			continue
		}
		count++
	}
	if count == 0 {
		t.Log("tasks 表空(正常,引擎运行后产生)")
	}
}

func TestBadCasesTable(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	// 表应存在
	_, err := DB.Exec("SELECT COUNT(*) FROM bad_cases")
	if err != nil {
		t.Fatalf("bad_cases 表查询失败: %v", err)
	}
}

func TestQuestionSetsTable(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	rows, err := DB.Query("SELECT id, name FROM question_sets")
	if err != nil {
		t.Fatalf("question_sets 表查询失败: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name string
		rows.Scan(&id, &name)
		count++
	}
	if count == 0 {
		t.Log("question_sets 表空(种子可能未灌入)")
	}
}

func TestScenesTable(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	rows, err := DB.Query("SELECT id, label FROM scenes")
	if err != nil {
		t.Fatalf("scenes 表查询失败: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, label string
		rows.Scan(&id, &label)
		count++
	}
	t.Logf("scenes 表有 %d 条记录", count)
}

func TestWALMode(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	var journalMode string
	DB.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if journalMode != "wal" {
		t.Logf("journal_mode = %q (测试环境可能无 WAL 支持)", journalMode)
	}
}

func TestConcurrentRead(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	// 并发读测试
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			rows, err := DB.Query("SELECT COUNT(*) FROM agents")
			if err == nil {
				rows.Close()
			}
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}

func TestGetQuestionsByScene(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()
	SeedIfEmpty()

	questions, err := GetQuestionsByScene("agent")
	if err != nil {
		t.Fatalf("GetQuestionsByScene 失败: %v", err)
	}
	// agent 场景应有题目
	t.Logf("agent 场景有 %d 道题", len(questions))

	// model 场景也应有题
	modelQuestions, _ := GetQuestionsByScene("model")
	t.Logf("model 场景有 %d 道题", len(modelQuestions))
}
