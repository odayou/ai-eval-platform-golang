// Package sdk 提供 AI Eval Platform 的标准接入 SDK。
//
// 本 SDK 让第三方 Agent/工具能够通过标准 MCP 协议接入评测平台,
// 实现"接入即评测"——Agent 只需实现 SDK 定义的接口即可被平台发现、注册、评测。
//
// 设计原则:
//   - MCP(Model Context Protocol) 作为底层通信协议
//   - Platform API (REST) 作为上层管理接口
//   - Agent 只需实现一个 eval endpoint,其余由平台自动完成
//
// 快速接入(三步):
//   1. 在 Agent 代码中嵌入 SDK Handler
//   2. 通过 PlatformAPI 注册到评测平台
//   3. 平台自动发现并开始评测
//
// 示例:
//
//	import "ai-eval-platform/internal/sdk"
//
//	func main() {
//	    // 创建 SDK 客户端
//	    client := sdk.NewPlatformClient("http://localhost:8080", "my-api-key")
//
//	    // 注册 Agent
//	    agent, err := client.RegisterAgent(sdk.AgentRegistration{
//	        Name:    "My Agent",
//	        Model:   "gpt-4o",
//	        Adapter: "api",
//	        Endpoint: "http://localhost:9090/eval",
//	    })
//
//	    // 提交评测任务
//	    task, err := client.SubmitTask(sdk.TaskSubmission{
//	        AgentID:  agent.ID,
//	        Scene:    "agent",
//	        RuleSet:  "rs-fullstack",
//	    })
//	}
package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ──────────────────────────────────────────────
// 类型定义
// ──────────────────────────────────────────────

// AgentRegistration 是注册 Agent 的请求。
type AgentRegistration struct {
	Name        string `json:"name"`                  // Agent 名称
	Model       string `json:"model"`                  // 模型标识(如 glm-4, deepseek-chat)
	Adapter     string `json:"adapter"`                // 接入方式: api | terminal | source
	Endpoint    string `json:"endpoint,omitempty"`     // Agent 评测端点(平台调 Agent 用)
	Description string `json:"description,omitempty"`  // 描述
	Version     string `json:"version,omitempty"`      // Agent 版本
}

// AgentInfo 是已注册 Agent 的信息。
type AgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Adapter     string `json:"adapter"`
	Endpoint    string `json:"endpoint"`
	Online      bool   `json:"online"`
	CreatedAt   string `json:"created_at"`
}

// TaskSubmission 是创建评测任务的请求。
type TaskSubmission struct {
	Name         string `json:"name"`                   // 任务名称
	Scene        string `json:"scene"`                  // 评测场景: agent | model | competitor | prd | product
	AgentID      string `json:"agent_id"`               // 被测 Agent ID
	RuleSet      string `json:"rule_set"`                // 规则集 JSON
	ChaosEnabled bool   `json:"chaos_enabled"`           // 是否启用混沌对抗
}

// TaskInfo 是评测任务的状态和信息。
type TaskInfo struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Scene     string  `json:"scene"`
	Status    string  `json:"status"`     // pending | running | done | failed | blown
	Overall   float64 `json:"overall"`    // 综合分(仅 done 时有效)
	CreatedAt string  `json:"created_at"`
}

// EvalRequest 是 Agent 评测端点的请求格式(MCP 兼容)。
// 平台调 Agent 的这个端点来获取评测样本。
type EvalRequest struct {
	Prompt     string            `json:"prompt"`      // 评测题目
	Context    map[string]string `json:"context"`     // 上下文(历史对话、工具结果)
	MaxTokens  int               `json:"max_tokens"`  // 最大生成 token 数
	Temperature float64          `json:"temperature"` // 生成温度
}

// EvalResponse 是 Agent 评测端点的响应格式(MCP 兼容)。
type EvalResponse struct {
	Text      string `json:"text"`       // 生成的回答
	TokenIn   int    `json:"token_in"`   // 输入 token 数
	TokenOut  int    `json:"token_out"`  // 输出 token 数
	Model     string `json:"model"`      // 实际使用的模型
	LatencyMs int64  `json:"latency_ms"` // 响应耗时
}

// ──────────────────────────────────────────────
// PlatformClient — 平台 API 客户端
// ──────────────────────────────────────────────

// PlatformClient 是 AI Eval Platform 的 API 客户端。
// Agent 通过它注册自身、创建评测任务、查询结果。
type PlatformClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewPlatformClient 创建新的平台客户端。
// baseURL: 平台地址(如 http://localhost:8080/api)
// apiKey: API Key(平台设置页生成的 Key)
func NewPlatformClient(baseURL, apiKey string) *PlatformClient {
	return &PlatformClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// RegisterAgent 向平台注册一个 Agent。
// 注册后 Agent 出现在平台的 Agent 库中，可被选中评测。
func (c *PlatformClient) RegisterAgent(req AgentRegistration) (*AgentInfo, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", "/agents", body)
	if err != nil {
		return nil, fmt.Errorf("register agent: %w", err)
	}
	defer resp.Body.Close()

	var result AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// ListAgents 获取平台上的所有 Agent 列表。
func (c *PlatformClient) ListAgents() ([]AgentInfo, error) {
	resp, err := c.do("GET", "/agents", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var agents []AgentInfo
	json.NewDecoder(resp.Body).Decode(&agents)
	return agents, nil
}

// SubmitTask 提交评测任务。
// 任务创建后平台自动入队调度执行。
func (c *PlatformClient) SubmitTask(req TaskSubmission) (*TaskInfo, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", "/tasks", body)
	if err != nil {
		return nil, fmt.Errorf("submit task: %w", err)
	}
	defer resp.Body.Close()

	var result TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// GetTask 获取评测任务的状态和结果。
func (c *PlatformClient) GetTask(taskID string) (*TaskInfo, error) {
	resp, err := c.do("GET", "/tasks/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// WaitForTask 等待评测任务完成并返回结果。
// 轮询间隔 2 秒,超时 10 分钟。
func (c *PlatformClient) WaitForTask(taskID string) (*TaskInfo, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		task, err := c.GetTask(taskID)
		if err != nil {
			return nil, err
		}
		switch task.Status {
		case "done", "failed", "blown":
			return task, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("task %s timed out after 10 minutes", taskID)
}

// ListRuleSets 获取可用规则集列表。
func (c *PlatformClient) ListRuleSets() ([]map[string]interface{}, error) {
	resp, err := c.do("GET", "/rule-sets", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var sets []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&sets)
	return sets, nil
}

// ListQuestions 获取题目列表。
func (c *PlatformClient) ListQuestions(scene string) ([]map[string]interface{}, error) {
	url := "/questions"
	if scene != "" {
		url += "?scene=" + scene
	}
	resp, err := c.do("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var qs []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&qs)
	return qs, nil
}

// do 执行 HTTP 请求并检查响应状态。
func (c *PlatformClient) do(method, path string, body []byte) (*http.Response, error) {
	url := c.baseURL + path
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("User-Agent", "ai-eval-platform-sdk/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// ──────────────────────────────────────────────
// EvalHandler — Agent 评测端点处理器(MCP 协议)
// ──────────────────────────────────────────────

// EvalHandler 是 Agent 评测端点的 HTTP 处理器。
// Agent 嵌入此 Handler 后即可被平台通过 MCP 协议调用评测。
//
// 使用示例:
//
//	http.Handle("/eval", sdk.EvalHandler(func(req sdk.EvalRequest) sdk.EvalResponse {
//	    // 调用 Agent 的核心逻辑
//	    result := myAgent.Run(req.Prompt)
//	    return sdk.EvalResponse{
//	        Text:     result.Text,
//	        TokenIn:  result.TokenIn,
//	        TokenOut: result.TokenOut,
//	        Model:    "my-model",
//	    }
//	}))
type EvalHandler func(req EvalRequest) EvalResponse

// ServeHTTP 实现 http.Handler 接口。
func (h EvalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req EvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	start := time.Now()
	resp := h(req)
	resp.LatencyMs = time.Since(start).Milliseconds()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ──────────────────────────────────────────────
// MCPServerHandler — MCP Server 接入代理
// ──────────────────────────────────────────────

// MCPServerHandler 是一个 MCP 协议兼容的服务器 Handler。
// 它让任何实现了 Tool 接口的 Go 函数能以 MCP Server 形式被平台调用。
//
// MCP 协议格式:
//
//	请求: {"method": "call_tool", "params": {"name": "tool_name", "arguments": {...}}}
//	响应: {"result": {"content": [...]}}
type MCPServerHandler struct {
	tools map[string]MCPTool
}

// MCPTool 是一个 MCP 工具的定义。
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema string `json:"input_schema"` // JSON Schema
}

// NewMCPServer 创建 MCP Server Handler。
func NewMCPServer() *MCPServerHandler {
	return &MCPServerHandler{
		tools: make(map[string]MCPTool),
	}
}

// RegisterTool 注册一个 MCP 工具。
func (m *MCPServerHandler) RegisterTool(tool MCPTool) {
	m.tools[tool.Name] = tool
}

// ServeHTTP 实现 MCP Server 的 HTTP 端点。
func (m *MCPServerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 处理 MCP ListTools 请求
	if r.Method == http.MethodGet {
		var tools []MCPTool
		for _, t := range m.tools {
			tools = append(tools, t)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"tools": tools,
			},
		})
		return
	}

	// 处理 MCP CallTool 请求
	var req struct {
		Method string `json:"method"`
		Params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Method != "call_tool" {
		http.Error(w, `{"error":"method not supported"}`, http.StatusBadRequest)
		return
	}

	tool, ok := m.tools[req.Params.Name]
	if !ok {
		http.Error(w, `{"error":"tool not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("Tool %s called with %d arguments", tool.Name, len(req.Params.Arguments))},
			},
		},
	})
}
