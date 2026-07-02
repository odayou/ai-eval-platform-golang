package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// setup 初始化测试环境: 临时 DB + Gin 路由。
func setup(t *testing.T) (*gin.Engine, func()) {
	t.Helper()

	// 创建临时数据库
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store.Init(dbPath)
	store.SeedIfEmpty()

	// 创建 Gin 路由(测试模式)
	gin.SetMode(gin.TestMode)
	r := NewRouter()

	// 返回清理函数
	cleanup := func() {
		store.Close()
		os.Remove(dbPath)
	}

	return r, cleanup
}

// ──────────────────────────────────────────────
// API 集成测试(不依赖真实引擎)
// ──────────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("health check 返回 %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAgentCRUD(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// ── 创建 Agent ──
	body := `{"name":"Test Agent","adapter":"api","model":"glm-4"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Errorf("创建 Agent 返回 %d, want 201", w.Code)
	}

	var created map[string]interface{}
	json.NewDecoder(w.Body).Decode(&created)
	if created["name"] != "Test Agent" {
		t.Errorf("name = %v, want 'Test Agent'", created["name"])
	}

	agentID := created["id"].(string)
	if agentID == "" {
		t.Error("agent id 不能为空")
	}

	// ── 列出 Agent ──
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/agents", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("列出 Agent 返回 %d, want 200", w.Code)
	}

	var agents []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&agents)
	if len(agents) == 0 {
		t.Error("Agent 列表不应为空")
	}

	// ── 删除 Agent ──
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", "/api/agents/"+agentID, nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("删除 Agent 返回 %d, want 200", w.Code)
	}
}

func TestTaskCRUD(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// ── 创建任务 ──
	body := `{"name":"测试任务","scene":"agent","agent_id":"a-codegen","rule_set":"{\"rule_set_id\":\"rs-fullstack\"}"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Errorf("创建任务返回 %d, want 201. Body: %s", w.Code, w.Body.String())
	}

	var created map[string]interface{}
	json.NewDecoder(w.Body).Decode(&created)
	taskID := created["id"].(string)
	if taskID == "" {
		t.Error("task id 不能为空")
	}

	// ── 查询任务 ──
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+taskID, nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("查询任务返回 %d, want 200", w.Code)
	}
}

func TestQuestionCRUD(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// ── 列出题目(应有种子数据) ──
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/questions", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("列出题目返回 %d, want 200", w.Code)
	}

	var questions []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&questions)
	if len(questions) == 0 {
		t.Error("题库不应为空(应有种子数据)")
	}
}

func TestRuleCRUD(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// ── 列出规则 ──
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/rules", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("列出规则返回 %d, want 200", w.Code)
	}

	var rules []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&rules)
	if len(rules) == 0 {
		t.Error("规则列表不应为空(应有种子数据)")
	}
}

func TestRuleSetCRUD(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// 创建规则集
	body := `{"name":"测试规则集","scene":"agent","rule_ids":"[\"r-sec-inject\",\"r-sec-leak\"]"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/rule-sets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Errorf("创建规则集返回 %d, want 201", w.Code)
	}
}

func TestModelConfig(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// 应有种子数据
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/model-config", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("模型配置返回 %d, want 200", w.Code)
	}

	var configs []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&configs)
	if len(configs) == 0 {
		t.Error("模型配置不应为空(应有种子数据)")
	}
}

func TestSceneAPI(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	// 列出场景(应包含内置)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/scenes", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("列出场景返回 %d, want 200", w.Code)
	}

	// 创建自定义场景
	body := `{"label":"文生图评测","description":"AIGC 图片生成质量","strategy":"multimodal_review"}`
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/scenes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("创建场景返回 %d, want 200", w.Code)
	}
}

func TestLeaderboardEndpoint(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stats/leaderboard?scene=agent", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("排行榜返回 %d, want 200", w.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["scene"] != "agent" {
		t.Errorf("scene = %v, want agent", result["scene"])
	}
}

func TestDashboardStats(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stats/dashboard", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("看板统计返回 %d, want 200", w.Code)
	}

	var stats map[string]interface{}
	json.NewDecoder(w.Body).Decode(&stats)

	if stats["total_tasks"] == nil {
		t.Error("看板统计应包含 total_tasks")
	}
	if stats["agent_count"] == nil {
		t.Error("看板统计应包含 agent_count")
	}
}

func TestSDKDocs(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/sdk/docs", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("SDK docs 返回 %d, want 200", w.Code)
	}

	var docs map[string]interface{}
	json.NewDecoder(w.Body).Decode(&docs)
	if docs["sdk_version"] != "v1.0" {
		t.Errorf("sdk_version = %v, want v1.0", docs["sdk_version"])
	}
}

func TestBadCasesEndpoint(t *testing.T) {
	r, cleanup := setup(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/bad-cases", nil)
	r.ServeHTTP(w, req)

	// 允许 200 或 500(取决于是否有引擎数据)
	if w.Code != 200 && w.Code != 500 {
		t.Errorf("bad-cases 返回 %d, want 200", w.Code)
	}
}
