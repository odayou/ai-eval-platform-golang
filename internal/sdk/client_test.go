package sdk

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ──────────────────────────────────────────────
// SDK Client 测试
// ──────────────────────────────────────────────

// mockPlatformServer 模拟平台 API 的 HTTP 服务器。
func mockPlatformServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// POST /api/agents → 注册 Agent
	mux.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "test-agent-id",
				"name": "Test Agent",
				"model": "glm-4",
				"online": true,
			})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "a-1", "name": "Agent 1", "model": "glm-4"},
			})
		}
	})

	// POST /api/tasks → 创建任务
	mux.HandleFunc("/tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "test-task-id",
				"name":   "Test Task",
				"status": "pending",
			})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "t-1", "name": "Task 1", "status": "done"},
			})
		}
	})

	// GET /api/tasks/:id → 任务详情
	mux.HandleFunc("/tasks/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "test-task-id",
			"name":    "Test Task",
			"status":  "done",
			"overall": 85.5,
		})
	})

	// GET /api/rule-sets
	mux.HandleFunc("/rule-sets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "rs-1", "name": "全栈评测集"},
		})
	})

	// GET/POST /api/questions
	mux.HandleFunc("/questions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "q-1", "title": "实现线程安全计数器", "difficulty": "mid"},
		})
	})

	return httptest.NewServer(mux)
}

func TestNewPlatformClient(t *testing.T) {
	client := NewPlatformClient("http://localhost:8080/api", "test-key")
	if client == nil {
		t.Fatal("NewPlatformClient 返回 nil")
	}
	if client.baseURL != "http://localhost:8080/api" {
		t.Errorf("baseURL = %q", client.baseURL)
	}
}

func TestRegisterAgent(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	agent, err := client.RegisterAgent(AgentRegistration{
		Name:    "Test Agent",
		Model:   "glm-4",
		Adapter: "api",
	})

	if err != nil {
		t.Fatalf("RegisterAgent 失败: %v", err)
	}
	if agent.ID != "test-agent-id" {
		t.Errorf("Agent.ID = %q, want test-agent-id", agent.ID)
	}
	if !agent.Online {
		t.Error("Agent.Online 应为 true")
	}
}

func TestListAgents(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	agents, err := client.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents 失败: %v", err)
	}
	if len(agents) == 0 {
		t.Error("Agent 列表不应为空")
	}
}

func TestSubmitTask(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	task, err := client.SubmitTask(TaskSubmission{
		Name:    "测试任务",
		Scene:   "agent",
		AgentID: "test-agent-id",
	})

	if err != nil {
		t.Fatalf("SubmitTask 失败: %v", err)
	}
	if task.ID != "test-task-id" {
		t.Errorf("Task.ID = %q, want test-task-id", task.ID)
	}
	if task.Status != "pending" {
		t.Errorf("Task.Status = %q, want pending", task.Status)
	}
}

func TestGetTask(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	task, err := client.GetTask("test-task-id")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("Task.Status = %q, want done", task.Status)
	}
}

func TestWaitForTask(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	task, err := client.WaitForTask("test-task-id")
	if err != nil {
		t.Fatalf("WaitForTask 失败: %v", err)
	}
	if task.Status != "done" {
		t.Errorf("Task.Status = %q, want done", task.Status)
	}
}

func TestListRuleSets(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	sets, err := client.ListRuleSets()
	if err != nil {
		t.Fatalf("ListRuleSets 失败: %v", err)
	}
	if len(sets) == 0 {
		t.Error("规则集列表不应为空")
	}
}

func TestListQuestions(t *testing.T) {
	server := mockPlatformServer(t)
	defer server.Close()

	client := NewPlatformClient(server.URL, "")
	qs, err := client.ListQuestions("agent")
	if err != nil {
		t.Fatalf("ListQuestions 失败: %v", err)
	}
	if len(qs) == 0 {
		t.Error("题目列表不应为空")
	}
}

// ──────────────────────────────────────────────
// EvalHandler 测试
// ──────────────────────────────────────────────

func TestEvalHandler(t *testing.T) {
	handler := EvalHandler(func(req EvalRequest) EvalResponse {
		return EvalResponse{
			Text:     "test response",
			TokenIn:  10,
			TokenOut: 5,
			Model:    "test-model",
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// 发送评测请求
	resp, err := http.Post(server.URL+"/eval", "application/json",
		bytes.NewReader(mustMarshal(t, EvalRequest{Prompt: "test prompt"})))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	var result EvalResponse
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Text != "test response" {
		t.Errorf("Text = %q, want 'test response'", result.Text)
	}
	if result.TokenIn != 10 {
		t.Errorf("TokenIn = %d, want 10", result.TokenIn)
	}
	if result.LatencyMs < 0 {
		t.Error("LatencyMs 应为非负")
	}
}

func TestEvalHandlerInvalidMethod(t *testing.T) {
	handler := EvalHandler(func(req EvalRequest) EvalResponse {
		return EvalResponse{Text: "ok"}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/eval")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if resp.StatusCode != 405 {
		t.Errorf("GET 应返回 405, 实际 %d", resp.StatusCode)
	}
}

// ──────────────────────────────────────────────
// MCPServerHandler 测试
// ──────────────────────────────────────────────

func TestMCPServer(t *testing.T) {
	server := NewMCPServer()
	server.RegisterTool(MCPTool{
		Name:        "test_tool",
		Description: "测试工具",
		InputSchema: `{"type":"object"}`,
	})

	// 测试 ListTools
	mux := http.NewServeMux()
	mux.Handle("/mcp", server)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET /mcp 失败: %v", err)
	}
	defer resp.Body.Close()

	var listResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&listResp)

	result := listResp["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})
	if len(tools) != 1 {
		t.Errorf("应有 1 个工具, 实际 %d", len(tools))
	}
}

// ──────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal 失败: %v", err)
	}
	return b
}
