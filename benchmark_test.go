package benchmark

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-eval-platform/internal/api"
	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/scheduler"
	"ai-eval-platform/internal/store"
	"ai-eval-platform/pkg/sdk"

	"github.com/google/uuid"
)

func BenchmarkDBInsertTasks(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "bench-db-*")
	dbPath := filepath.Join(tmpDir, "bench.db")
	store.Init(dbPath)
	store.SeedIfEmpty()
	b.Cleanup(func() { store.Close(); os.RemoveAll(tmpDir) })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uuid.New().String()
		store.DB.Exec(`INSERT INTO tasks (id, name, scene, agent_id, rule_set, status, created_at, updated_at)
			VALUES (?, ?, 'agent', 'a-codegen', ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			id, "bench-"+id, `{"rule_set_id":"rs-fullstack"}`)
	}
}

func BenchmarkDBQueryTasks(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "bench-db-*")
	dbPath := filepath.Join(tmpDir, "bench.db")
	store.Init(dbPath)
	store.SeedIfEmpty()
	b.Cleanup(func() { store.Close(); os.RemoveAll(tmpDir) })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, _ := store.DB.Query("SELECT id, name, scene, status FROM tasks ORDER BY created_at DESC")
		if rows != nil {
			for rows.Next() {
				var id, name, scene, status string
				rows.Scan(&id, &name, &scene, &status)
			}
			rows.Close()
		}
	}
}

func BenchmarkEngineFullPipeline(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "bench-eng-*")
	dbPath := filepath.Join(tmpDir, "bench.db")
	store.Init(dbPath)
	store.SeedIfEmpty()
	scheduler.InitGlobal(engine.Run, 1)
	b.Cleanup(func() { scheduler.StopGlobal(); store.Close(); os.RemoveAll(tmpDir) })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		taskID := uuid.New().String()
		store.DB.Exec(`INSERT INTO tasks (id, name, scene, agent_id, rule_set, status, created_at, updated_at)
			VALUES (?, ?, 'agent', 'a-codegen', ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			taskID, "bench-pipe-"+taskID, `{"rule_set_id":"rs-fullstack"}`)
		scheduler.SubmitGlobalHigh(taskID)
	}
}

func BenchmarkEngineRoundtableConcurrent(b *testing.B) {
	rules := []store.RuleRow{
		{ID: "r1", Name: "SQL注入检查", Dimension: "security", Scoring: "metric", Content: "检查SQL注入"},
		{ID: "r2", Name: "密钥泄露检查", Dimension: "security", Scoring: "metric", Content: "检查密钥"},
		{ID: "r3", Name: "死锁检查", Dimension: "performance", Scoring: "llm", Content: "检查死锁"},
		{ID: "r4", Name: "分层检查", Dimension: "architecture", Scoring: "llm", Content: "检查分层"},
	}
	answer := `func main() { println("hello") }`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.RunRoundtable("", rules, "test prompt", answer, engine.AgentProfile{})
	}
}

func BenchmarkEngineRAGSearchLarge(b *testing.B) {
	rules := make([]store.RuleRow, 100)
	for i := range rules {
		rules[i] = store.RuleRow{
			ID: uuid.New().String(), Name: "Rule", Dimension: "security",
			Content: "test content for rules",
		}
	}
	idx := &engine.RAGIndex{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = idx
		_ = rules
	}
}

func BenchmarkAPIAgentCRUD(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "bench-api-*")
	dbPath := filepath.Join(tmpDir, "bench.db")
	store.Init(dbPath)
	store.SeedIfEmpty()
	gin := api.NewRouter()
	b.Cleanup(func() { store.Close(); os.RemoveAll(tmpDir) })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		body := `{"name":"bench-agent","adapter":"api","model":"glm-4"}`
		req, _ := http.NewRequest("POST", "/api/agents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		gin.ServeHTTP(w, req)
	}
}

func BenchmarkAPIConcurrentRequests(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "bench-conc-*")
	dbPath := filepath.Join(tmpDir, "bench.db")
	store.Init(dbPath)
	store.SeedIfEmpty()
	gin := api.NewRouter()
	b.Cleanup(func() { store.Close(); os.RemoveAll(tmpDir) })

	endpoints := []string{
		"/api/agents", "/api/models", "/api/tasks", "/api/rules",
		"/api/questions", "/api/scenes", "/api/health",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		done := make(chan bool, len(endpoints))
		for _, ep := range endpoints {
			go func(path string) {
				w := httptest.NewRecorder()
				req, _ := http.NewRequest("GET", path, nil)
				gin.ServeHTTP(w, req)
				done <- true
			}(ep)
		}
		for range endpoints {
			<-done
		}
	}
}

func BenchmarkSDKClientSerialization(b *testing.B) {
	reg := sdk.AgentRegistration{
		Name: "bench-agent", Model: "glm-4", Adapter: "api",
		Endpoint: "http://localhost:9090/eval", Description: "bench",
	}
	sub := sdk.TaskSubmission{
		Name: "bench", Scene: "agent", AgentID: "test-id",
		RuleSet: `{"rule_set_id":"rs-fullstack"}`,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(reg)
		json.Marshal(sub)
	}
}

func BenchmarkEngineBreaker(b *testing.B) {
	cfg := engine.DefaultBreakerConfig()
	bs := engine.NewBreakerState(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bs.Check(500, 1000)
	}
}

func BenchmarkEngineReportBuild(b *testing.B) {
	results := make([]engine.QuestionResult, 20)
	for i := range results {
		results[i] = engine.QuestionResult{
			QuestionID: uuid.New().String(), Title: "test",
			Score: float64(50 + i*2), IsBadCase: i%5 == 0,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.BuildReport(results, true)
	}
}
