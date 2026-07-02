package engine

// tool_search.go — Tool Search 延迟加载(good-skills #24 补齐)。
//
// 补齐专家评审:#24 skill 延迟加载已做,工具本身全量加载。
// 新增:工具分 always-loaded + deferred + tool_search 元工具按需检索。

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolMeta 工具元数据(含 defer_loading 标记)。
type ToolMeta struct {
	Name         string
	Description  string
	Parameters   json.RawMessage
	DeferLoading bool   // true = deferred(不默认加载 schema)
	Source       string // builtin | mcp:server | plugin
}

// ToolSearchRegistry 工具注册表(always-loaded + deferred 分离)。
type ToolSearchRegistry struct {
	AlwaysLoaded map[string]ToolMeta
	Deferred     map[string]ToolMeta
}

// NewToolSearchRegistry 创建注册表。
func NewToolSearchRegistry() *ToolSearchRegistry {
	return &ToolSearchRegistry{
		AlwaysLoaded: make(map[string]ToolMeta),
		Deferred:     make(map[string]ToolMeta),
	}
}

// Register 注册工具(always-loaded 或 deferred)。
func (r *ToolSearchRegistry) Register(meta ToolMeta) {
	if meta.DeferLoading {
		r.Deferred[meta.Name] = meta
	} else {
		r.AlwaysLoaded[meta.Name] = meta
	}
}

// GetToolsForRequest 获取注入 LLM 的 tools 数组(always-loaded 全部 + tool_search 工具)。
func (r *ToolSearchRegistry) GetToolsForRequest() []ToolDef {
	tools := make([]ToolDef, 0, len(r.AlwaysLoaded)+1)
	for _, meta := range r.AlwaysLoaded {
		tools = append(tools, ToolDef{
			Type: "function",
			Function: ToolFunction{
				Name: meta.Name, Description: meta.Description, Parameters: meta.Parameters,
			},
		})
	}
	// 加 tool_search 元工具(若有 deferred 工具)
	if len(r.Deferred) > 0 {
		tools = append(tools, r.toolSearchDef())
	}
	return tools
}

// Search 搜索 deferred 工具(按 description 模糊匹配)。
func (r *ToolSearchRegistry) Search(query string, limit int) []ToolMeta {
	if limit <= 0 {
		limit = 10
	}
	queryLower := strings.ToLower(query)
	keywords := strings.Fields(queryLower)
	type scored struct {
		meta  ToolMeta
		score int
	}
	var results []scored
	for _, meta := range r.Deferred {
		text := strings.ToLower(meta.Name + " " + meta.Description)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				score++
			}
		}
		if score > 0 {
			results = append(results, scored{meta, score})
		}
	}
	// 按分数排序
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	metas := make([]ToolMeta, len(results))
	for i, r := range results {
		metas[i] = r.meta
	}
	return metas
}

// LoadFullSchema 加载某个 deferred 工具的完整 schema(命中后调)。
func (r *ToolSearchRegistry) LoadFullSchema(name string) (ToolDef, bool) {
	meta, ok := r.Deferred[name]
	if !ok {
		return ToolDef{}, false
	}
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name: meta.Name, Description: meta.Description, Parameters: meta.Parameters,
		},
	}, true
}

// toolSearchDef 生成 tool_search 工具声明。
func (r *ToolSearchRegistry) toolSearchDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "tool_search",
			Description: fmt.Sprintf("搜索可用工具(%d 个可按需加载)。当你需要某个功能但工具列表里没有时,先搜索。", len(r.Deferred)),
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "搜索关键词"}
				},
				"required": ["query"]
			}`),
		},
	}
}

// ExecuteToolSearch 执行 tool_search 工具。
func (r *ToolSearchRegistry) ExecuteToolSearch(args json.RawMessage) ToolResult {
	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return ToolResult{Name: "tool_search", Output: "参数解析失败", IsError: true}
	}
	query, _ := argsMap["query"].(string)
	if query == "" {
		return ToolResult{Name: "tool_search", Output: "缺少 query 参数", IsError: true}
	}
	results := r.Search(query, 10)
	if len(results) == 0 {
		return ToolResult{Name: "tool_search", Output: "无匹配工具"}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 个匹配工具:\n", len(results)))
	for _, meta := range results {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", meta.Name, meta.Description))
	}
	sb.WriteString("\n(选中后下次请求会加载其完整参数 schema)")
	return ToolResult{Name: "tool_search", Output: sb.String()}
}

// BuildToolRegistry 从内置工具 + MCP 工具构建注册表。
func BuildToolRegistry(mcpTools []ToolDef) *ToolSearchRegistry {
	reg := NewToolSearchRegistry()
	// 内置工具 = always-loaded
	for _, t := range BuiltinTools() {
		reg.Register(ToolMeta{
			Name: t.Function.Name, Description: t.Function.Description,
			Parameters: t.Function.Parameters, DeferLoading: false, Source: "builtin",
		})
	}
	// MCP 工具 = deferred(数量多,按需加载)
	for _, t := range mcpTools {
		reg.Register(ToolMeta{
			Name: t.Function.Name, Description: t.Function.Description,
			Parameters: t.Function.Parameters, DeferLoading: true, Source: "mcp",
		})
	}
	return reg
}
