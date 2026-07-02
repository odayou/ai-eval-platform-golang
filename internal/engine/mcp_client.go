package engine

// mcp_client.go — harness 第2层 MCP:轻量 JSON-RPC over HTTP 客户端。
//
// 连接 mcp_servers 表配置的外部 MCP server,把它的工具声明合并进 agent 的工具集。
// 不引入第三方 MCP SDK,自研轻量客户端(标准库 only)。
//
// 协议:MCP over HTTP(POST {endpoint} JSON-RPC 2.0)
//   - tools/list:拿工具声明(name/description/inputSchema)
//   - tools/call:执行工具
//
// 研究依据:
//   - Claude Code .claude/settings.json 的 mcpServers 配置
//   - learn-harness-engineering tool-registry-pattern:fail-closed + per-call concurrency
//   - harness-engineering concepts/06:MCP Servers 信任边界 + 工具数量控制

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// MCPClient MCP server 客户端(一个 server 一个 client 实例)。
type MCPClient struct {
	Endpoint string // MCP server HTTP 端点
	APIKey   string // 可选鉴权(若 MCP server 需要)
	client   *http.Client
}

// NewMCPClient 构造 MCP 客户端。
func NewMCPClient(endpoint, apiKey string) *MCPClient {
	return &MCPClient{
		Endpoint: endpoint,
		APIKey:   apiKey,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// mcpRequest JSON-RPC 2.0 请求。
type mcpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// mcpToolListResult tools/list 响应。
type mcpToolListResult struct {
	Result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// mcpCallResult tools/call 响应。
type mcpCallResult struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// call 发送 JSON-RPC 请求。
func (c *MCPClient) call(method string, params any) (json.RawMessage, error) {
	if c.Endpoint == "" {
		return nil, fmt.Errorf("MCP endpoint 为空")
	}
	reqBody := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", c.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MCP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, string(raw[:min(200, len(raw))]))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// 返回原始 JSON(由调用方解析)
	return raw, nil
}

// ListTools 拉取 MCP server 的工具声明,转为 ToolDef 数组。
func (c *MCPClient) ListTools() ([]ToolDef, error) {
	raw, err := c.call("tools/list", struct{}{})
	if err != nil {
		return nil, err
	}
	var result mcpToolListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("MCP tools/list 解析失败: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", result.Error.Code, result.Error.Message)
	}
	tools := make([]ToolDef, 0, len(result.Result.Tools))
	for _, t := range result.Result.Tools {
		params := t.InputSchema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, ToolDef{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return tools, nil
}

// CallTool 执行 MCP server 的一个工具。
func (c *MCPClient) CallTool(name string, arguments json.RawMessage) (ToolResult, error) {
	// arguments 可能是 JSON 字符串或 object,统一解析
	var argsMap map[string]any
	if err := json.Unmarshal(arguments, &argsMap); err != nil {
		argsMap = map[string]any{"_raw": string(arguments)}
	}
	raw, err := c.call("tools/call", map[string]any{
		"name":      name,
		"arguments": argsMap,
	})
	if err != nil {
		return ToolResult{Name: name, Output: err.Error(), IsError: true}, err
	}
	var result mcpCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolResult{Name: name, Output: "解析失败: " + err.Error(), IsError: true}, err
	}
	if result.Error != nil {
		return ToolResult{Name: name, Output: result.Error.Message, IsError: true}, nil
	}
	// 拼接 content 文本
	var sb bytes.Buffer
	for _, c := range result.Result.Content {
		sb.WriteString(c.Text)
		sb.WriteString("\n")
	}
	output := sb.String()
	// 9.4.3 结果大小限制：MCP tool 输出最多 10000 字符
	if len(output) > 10000 {
		output = output[:10000] + fmt.Sprintf("\n...[MCP 输出截断: 原始 %d 字符]", len(output))
	}
	return ToolResult{
		Name:    name,
		Output:  output,
		IsError: result.Result.IsError,
	}, nil
}

// ── 从 DB 加载 agent 配置的 MCP server 并合并工具 ──

// LoadMCPToolsForAgent 从 mcp_servers 表加载 agent 配置的 MCP server,
// 拉取它们的工具声明,合并成一个 ToolDef 数组。
//
// agent.Tools 字段(JSON 数组)里存的是 tool_id,这里不直接用(那是内置工具引用)。
// MCP server 通过 agent 的 MCP 配置关联(当前简化:加载全部 online 的 MCP server)。
//
// 返回:(合并的工具声明, MCPClient 映射[server_id→client],用于 CallTool 时路由)
func LoadMCPToolsForAgent() ([]ToolDef, map[string]*MCPClient, error) {
	// 查全部 MCP server(简化版:不按 agent 过滤,因为当前 agent.Tools 是 tool_id 不是 mcp_id)
	rows, err := store.DB.Query(`SELECT id, endpoint, COALESCE(api_key,'') FROM mcp_servers WHERE COALESCE(status,'') != 'offline'`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var allTools []ToolDef
	clients := make(map[string]*MCPClient)

	for rows.Next() {
		var id, endpoint, apiKey string
		if err := rows.Scan(&id, &endpoint, &apiKey); err != nil {
			continue
		}
		if endpoint == "" {
			continue
		}
		client := NewMCPClient(endpoint, apiKey)
		tools, err := client.ListTools()
		if err != nil {
			// MCP server 连不上,跳过(降级,不中断)
			continue
		}
		allTools = append(allTools, tools...)
		clients[id] = client
	}
	return allTools, clients, nil
}

// ExecuteMCPTool 在 MCP client 映射里找到能执行该工具的 server,调用它。
// (简化版:遍历所有 client 尝试,第一个成功的返回)
func ExecuteMCPTool(toolName string, arguments json.RawMessage, clients map[string]*MCPClient) ToolResult {
	for _, client := range clients {
		result, err := client.CallTool(toolName, arguments)
		if err != nil {
			continue // 这个 server 不支持或失败,试下一个
		}
		return result
	}
	return ToolResult{Name: toolName, Output: "无 MCP server 可执行此工具", IsError: true}
}

// ── D31 stdio transport:子进程 JSON-RPC over stdin/stdout ──

// StdioMCPClient stdio transport MCP 客户端(MCP 规范 transport 之一)。
type StdioMCPClient struct {
	Command string
	Args    []string
	Env     []string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	mu      sync.Mutex
	nextID  int
}

func NewStdioMCPClient(command string, args []string, env []string) *StdioMCPClient {
	return &StdioMCPClient{Command: command, Args: args, Env: env, nextID: 1}
}

func (c *StdioMCPClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmdParts := strings.Fields(c.Command)
	cmdParts = append(cmdParts, c.Args...)
	if len(cmdParts) == 0 {
		return fmt.Errorf("command 为空")
	}
	c.cmd = exec.Command(cmdParts[0], cmdParts[1:]...)
	if len(c.Env) > 0 {
		c.cmd.Env = append(os.Environ(), c.Env...)
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	c.stdin = stdin
	c.stdout = stdout
	return c.cmd.Start()
}

func (c *StdioMCPClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin != nil { c.stdin.Close() }
	if c.cmd != nil && c.cmd.Process != nil { c.cmd.Process.Kill() }
}

func (c *StdioMCPClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil || c.stdout == nil {
		return nil, fmt.Errorf("未连接")
	}
	id := c.nextID
	c.nextID++
	reqBytes, _ := json.Marshal(mcpRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if _, err := c.stdin.Write(append(reqBytes, '\n')); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		return nil, fmt.Errorf("stdout 读取失败")
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct{ Code int; Message string } `json:"error,omitempty"`
	}
	json.Unmarshal(scanner.Bytes(), &resp)
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func (c *StdioMCPClient) ListTools() ([]ToolDef, error) {
	raw, err := c.call("tools/list", struct{}{})
	if err != nil {
		return nil, err
	}
	var result mcpToolListResult
	json.Unmarshal(raw, &result)
	tools := make([]ToolDef, 0, len(result.Result.Tools))
	for _, t := range result.Result.Tools {
		params := t.InputSchema
		if len(params) == 0 { params = json.RawMessage(`{"type":"object","properties":{}}`) }
		tools = append(tools, ToolDef{Type: "function", Function: ToolFunction{
			Name: t.Name, Description: t.Description, Parameters: params}})
	}
	return tools, nil
}

func (c *StdioMCPClient) CallTool(name string, arguments json.RawMessage) (ToolResult, error) {
	var argsMap map[string]any
	json.Unmarshal(arguments, &argsMap)
	raw, err := c.call("tools/call", map[string]any{"name": name, "arguments": argsMap})
	if err != nil {
		return ToolResult{Name: name, Output: err.Error(), IsError: true}, err
	}
	var result mcpCallResult
	json.Unmarshal(raw, &result)
	var sb bytes.Buffer
	for _, ct := range result.Result.Content { sb.WriteString(ct.Text + "\n") }
	return ToolResult{Name: name, Output: sb.String(), IsError: result.Result.IsError}, nil
}

// ── D33 工具发现缓存 ──

var globalMCPCache = &MCPToolCache{cache: make(map[string][]ToolDef), expiry: make(map[string]time.Time), ttl: 5 * time.Minute}

type MCPToolCache struct {
	mu     sync.RWMutex
	cache  map[string][]ToolDef
	expiry map[string]time.Time
	ttl    time.Duration
}

func (c *MCPToolCache) GetCached(serverID string) []ToolDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if tools, ok := c.cache[serverID]; ok {
		if exp, exists := c.expiry[serverID]; exists && time.Now().Before(exp) {
			return tools
		}
	}
	return nil
}

func (c *MCPToolCache) Set(serverID string, tools []ToolDef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[serverID] = tools
	c.expiry[serverID] = time.Now().Add(c.ttl)
}

func (c *MCPToolCache) Invalidate(serverID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, serverID)
	delete(c.expiry, serverID)
}

// ── D34 连接池+重连 ──

var globalMCPPool = &MCPConnectionPool{clients: make(map[string]*MCPClient), retryMax: 3}

type MCPConnectionPool struct {
	mu       sync.Mutex
	clients  map[string]*MCPClient
	retryMax int
}

// ── 3.6 tool_search 延迟加载 ──

// ToolSearchToolDef 返回 tool_search 工具声明。
// agent 可以通过此工具按需发现已注册的 MCP server 提供的工具。
func ToolSearchToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "tool_search",
			Description: "搜索可用的 MCP 工具。按关键词搜索已注册的 MCP server 提供的工具。若不传关键词则列出所有可用 MCP 工具。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "搜索关键词（如 'database'）,留空列出全部"},
					"server": {"type": "string", "description": "限定特定的 MCP server（可选）"}
				}
			}`),
		},
	}
}

// ExecuteToolSearch 执行 tool_search（从 DB 加载 MCP server 并搜索工具）。
func ExecuteToolSearch(args json.RawMessage) ToolResult {
	var params struct {
		Query  string `json:"query"`
		Server string `json:"server"`
	}
	json.Unmarshal(args, &params)

	if store.DB == nil {
		return ToolResult{Name: "tool_search", Output: "数据库未初始化", IsError: true}
	}

	// 从 DB 查 MCP server(参数化查询,防 SQL 注入 — params.Server 来自 LLM 工具调用)
	query := `SELECT id, endpoint, COALESCE(api_key,''), COALESCE(description,'') FROM mcp_servers`
	var rows *sql.Rows
	var err error
	if params.Server != "" {
		query += ` WHERE id = ?`
		rows, err = store.DB.Query(query, params.Server)
	} else {
		rows, err = store.DB.Query(query)
	}
	if err != nil {
		return ToolResult{Name: "tool_search", Output: "查询失败: " + err.Error(), IsError: true}
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var id, endpoint, apiKey, desc string
		rows.Scan(&id, &endpoint, &apiKey, &desc)

		// 检查缓存
		tools := globalMCPCache.GetCached(id)
		if tools == nil {
			client := NewMCPClient(endpoint, apiKey)
			var err error
			tools, err = client.ListTools()
			if err != nil {
				continue // 跳过连不上的
			}
			globalMCPCache.Set(id, tools)
		}

		// 按关键词过滤
		for _, t := range tools {
			if params.Query != "" {
				if !strings.Contains(strings.ToLower(t.Function.Name), strings.ToLower(params.Query)) &&
					!strings.Contains(strings.ToLower(t.Function.Description), strings.ToLower(params.Query)) {
					continue
				}
			}
			results = append(results, fmt.Sprintf("[%s] %s: %s", id, t.Function.Name, t.Function.Description))
		}
	}

	if len(results) == 0 {
		if params.Query != "" {
			return ToolResult{Name: "tool_search", Output: fmt.Sprintf("未找到匹配 '%s' 的 MCP 工具", params.Query)}
		}
		return ToolResult{Name: "tool_search", Output: "无可用 MCP 工具"}
	}

	output := fmt.Sprintf("找到 %d 个 MCP 工具:\n%s", len(results), strings.Join(results, "\n"))
	return ToolResult{Name: "tool_search", Output: output}
}

// ── D34 连接池 ──

func (p *MCPConnectionPool) GetOrConnect(serverID, endpoint, apiKey string) (*MCPClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if client, ok := p.clients[serverID]; ok {
		return client, nil
	}
	var lastErr error
	for attempt := 0; attempt < p.retryMax; attempt++ {
		client := NewMCPClient(endpoint, apiKey)
		if _, err := client.ListTools(); err == nil {
			p.clients[serverID] = client
			return client, nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	return nil, fmt.Errorf("MCP %s 连接失败(%d 次): %w", serverID, p.retryMax, lastErr)
}

func (p *MCPConnectionPool) Disconnect(serverID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, serverID)
	globalMCPCache.Invalidate(serverID)
}
