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

func apiSetup(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store.Init(dbPath)
	store.SeedIfEmpty()
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	cleanup := func() { store.Close(); os.Remove(dbPath) }
	return r, cleanup
}

func apiGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", path, nil)
	r.ServeHTTP(w, req)
	return w
}

func apiPost(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func apiPut(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func apiDel(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", path, nil)
	r.ServeHTTP(w, req)
	return w
}

func apiDecode(t *testing.T, w *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode failed (status=%d, body=%s): %v", w.Code, w.Body.String(), err)
	}
}

// ── CRUD Lifecycle Tests ──

func TestIntAgentFullCRUD(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiGet(r, "/api/agents")
	apiDecode(t, w, &[]map[string]interface{}{})
	if w.Code != 200 {
		t.Fatalf("list agents returned %d", w.Code)
	}

	w = apiPost(r, "/api/agents", `{"name":"IntTest Agent","adapter":"api","model":"glm-4"}`)
	if w.Code != 201 {
		t.Fatalf("create agent returned %d: %s", w.Code, w.Body.String())
	}
	var created map[string]interface{}
	apiDecode(t, w, &created)
	agentID := created["id"].(string)

	w = apiGet(r, "/api/agents/"+agentID)
	if w.Code != 200 {
		t.Fatalf("get agent returned %d", w.Code)
	}

	w = apiPut(r, "/api/agents/"+agentID, `{"name":"Updated IntTest Agent"}`)
	if w.Code != 200 {
		t.Fatalf("update agent returned %d", w.Code)
	}

	w = apiDel(r, "/api/agents/"+agentID)
	if w.Code != 200 {
		t.Fatalf("delete agent returned %d", w.Code)
	}

	w = apiGet(r, "/api/agents/"+agentID)
	if w.Code != 404 {
		t.Fatalf("get deleted agent returned %d, want 404", w.Code)
	}
}

func TestIntModelFullCRUD(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/models", `{"name":"int-test-model","base_url":"https://api.test.com/v1","api_key":"sk-test"}`)
	if w.Code != 201 {
		t.Fatalf("create model returned %d: %s", w.Code, w.Body.String())
	}
	var created map[string]interface{}
	apiDecode(t, w, &created)
	modelID := created["id"].(string)

	w = apiGet(r, "/api/models/"+modelID)
	if w.Code != 200 {
		t.Fatalf("get model returned %d", w.Code)
	}

	w = apiDel(r, "/api/models/"+modelID)
	if w.Code != 200 {
		t.Fatalf("delete model returned %d", w.Code)
	}
}

func TestIntTaskFullCRUD(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/tasks", `{"name":"IntTest Task","scene":"agent","agent_id":"a-codegen","rule_set":"{\"rule_set_id\":\"rs-fullstack\"}"}`)
	if w.Code != 201 {
		t.Fatalf("create task returned %d: %s", w.Code, w.Body.String())
	}
	var created map[string]interface{}
	apiDecode(t, w, &created)
	taskID := created["id"].(string)

	w = apiGet(r, "/api/tasks/"+taskID)
	if w.Code != 200 {
		t.Fatalf("get task returned %d", w.Code)
	}

	w = apiDel(r, "/api/tasks/"+taskID)
	if w.Code != 200 {
		t.Fatalf("delete task returned %d", w.Code)
	}
}

func TestIntRuleFullCRUD(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/rules", `{"name":"IntTest Rule","dimension":"security","scoring":"metric","weight":1.5,"content":"Integration test rule"}`)
	if w.Code != 201 {
		t.Fatalf("create rule returned %d: %s", w.Code, w.Body.String())
	}
	var created map[string]interface{}
	apiDecode(t, w, &created)
	ruleID := created["id"].(string)

	w = apiPut(r, "/api/rules/"+ruleID, `{"name":"Updated Rule","weight":2.0}`)
	if w.Code != 200 {
		t.Fatalf("update rule returned %d", w.Code)
	}

	w = apiDel(r, "/api/rules/"+ruleID)
	if w.Code != 200 {
		t.Fatalf("delete rule returned %d", w.Code)
	}
}

func TestIntPromptTemplateLifecycle(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/prompt-templates", `{"name":"int-test-tmpl","content":"You are a test assistant.","model_hint":"glm-4"}`)
	if w.Code != 201 {
		t.Fatalf("create template returned %d: %s", w.Code, w.Body.String())
	}
	var created map[string]interface{}
	apiDecode(t, w, &created)
	tmplID := created["id"].(string)

	w = apiPut(r, "/api/prompt-templates/"+tmplID, `{"content":"Updated content for v2"}`)
	if w.Code != 200 {
		t.Fatalf("update template returned %d", w.Code)
	}

	w = apiGet(r, "/api/prompt-templates/"+tmplID+"/versions")
	if w.Code != 200 {
		t.Fatalf("list versions returned %d", w.Code)
	}
	var versions []map[string]interface{}
	apiDecode(t, w, &versions)
	if len(versions) < 1 {
		t.Error("expected at least 1 version")
	}
}

// ── Advanced API Tests ──

func TestIntGenerateQuestions(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/questions/generate", `{"scene":"agent","rule_set_id":"rs-fullstack","count_per_dim":1}`)
	if w.Code != 201 && w.Code != 500 {
		t.Errorf("generate questions returned %d, want 201 or 500", w.Code)
	}
}

func TestIntImportBaseline(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/questions/import-baseline", `{"repo":"tiktok-core","count":2,"scene":"agent"}`)
	if w.Code != 200 {
		t.Fatalf("import baseline returned %d: %s", w.Code, w.Body.String())
	}
	var result map[string]interface{}
	apiDecode(t, w, &result)
	if result["imported"] != float64(2) {
		t.Errorf("imported = %v, want 2", result["imported"])
	}
}

func TestIntCloneTest(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/clone-test", `{"source_agent_id":"a-codegen","target_models":["glm-4-flash"],"rule_set_id":"rs-fullstack","question_count":1}`)
	if w.Code != 200 && w.Code != 500 {
		t.Errorf("clone test returned %d, want 200 or 500", w.Code)
	}
}

func TestIntPromptAdapter(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiPost(r, "/api/prompt/adapt", `{"source_prompt":"你是安全专家。","source_model":"deepseek-chat","target_model":"glm-4-flash"}`)
	if w.Code != 200 {
		t.Fatalf("prompt adapt returned %d: %s", w.Code, w.Body.String())
	}
	var result map[string]interface{}
	apiDecode(t, w, &result)
	if result["source_profile"] == nil {
		t.Error("expected source_profile in response")
	}
	if result["adapted_prompt"] == nil {
		t.Error("expected adapted_prompt in response")
	}
}

func TestIntSDKDocs(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiGet(r, "/api/sdk/docs")
	if w.Code != 200 {
		t.Fatalf("sdk docs returned %d", w.Code)
	}
	var docs map[string]interface{}
	apiDecode(t, w, &docs)
	if docs["sdk_version"] != "v1.0" {
		t.Errorf("sdk_version = %v, want v1.0", docs["sdk_version"])
	}
}

func TestIntDashboardStats(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiGet(r, "/api/stats/dashboard")
	if w.Code != 200 {
		t.Fatalf("dashboard stats returned %d", w.Code)
	}
	var stats map[string]interface{}
	apiDecode(t, w, &stats)
	if stats["total_tasks"] == nil {
		t.Error("expected total_tasks")
	}
	if stats["agent_count"] == nil {
		t.Error("expected agent_count")
	}
}

func TestIntLeaderboard(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiGet(r, "/api/stats/leaderboard?scene=agent")
	if w.Code != 200 {
		t.Fatalf("leaderboard returned %d", w.Code)
	}
}

func TestIntNotFoundErrors(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	paths := []string{
		"/api/agents/nonexistent",
		"/api/models/nonexistent",
		"/api/tasks/nonexistent",
		"/api/questions/nonexistent",
		"/api/rules/nonexistent",
	}
	for _, path := range paths {
		w := apiGet(r, path)
		if w.Code != 404 {
			t.Errorf("GET %s returned %d, want 404", path, w.Code)
		}
	}
}

func TestIntBadRequestErrors(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/api/agents", `{}`},
		{"POST", "/api/tasks", `{}`},
		{"POST", "/api/rules", `{}`},
		{"POST", "/api/questions", `{}`},
		{"POST", "/api/models", `{}`},
	}
	for _, tc := range tests {
		var w *httptest.ResponseRecorder
		switch tc.method {
		case "POST":
			w = apiPost(r, tc.path, tc.body)
		case "PUT":
			w = apiPut(r, tc.path, tc.body)
		}
		if w.Code != 400 {
			t.Errorf("%s %s returned %d, want 400 (body: %s)", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestIntSeedDataIntegrity(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	var agents []map[string]interface{}
	apiDecode(t, apiGet(r, "/api/agents"), &agents)
	if len(agents) < 3 {
		t.Errorf("expected >=3 agents, got %d", len(agents))
	}

	var rules []map[string]interface{}
	apiDecode(t, apiGet(r, "/api/rules"), &rules)
	dims := map[string]bool{}
	for _, r := range rules {
		dims[r["dimension"].(string)] = true
	}
	for _, d := range []string{"security", "performance", "architecture", "product", "correctness"} {
		if !dims[d] {
			t.Errorf("missing dimension: %s", d)
		}
	}

	var qs []map[string]interface{}
	apiDecode(t, apiGet(r, "/api/questions"), &qs)
	if len(qs) < 10 {
		t.Errorf("expected >=10 questions, got %d", len(qs))
	}
}

func TestIntRuleExportImport(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	w := apiGet(r, "/api/rule-sets/rs-fullstack/export")
	if w.Code != 200 {
		t.Fatalf("export rule set returned %d", w.Code)
	}

	w = apiPost(r, "/api/rules/import", `{"version":"1.0","rules":[{"id":"imp-1","name":"Imported","dimension":"security","scoring":"llm","weight":1.0,"content":"test"}]}`)
	if w.Code != 200 {
		t.Fatalf("import rules returned %d: %s", w.Code, w.Body.String())
	}
}

func TestIntConcurrentReads(t *testing.T) {
	r, cleanup := apiSetup(t)
	defer cleanup()

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			endpoints := []string{"/api/agents", "/api/models", "/api/tasks", "/api/rules", "/api/questions", "/api/scenes", "/api/health"}
			for _, ep := range endpoints {
				w := apiGet(r, ep)
				if w.Code != 200 {
					t.Errorf("concurrent %s returned %d", ep, w.Code)
				}
			}
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
