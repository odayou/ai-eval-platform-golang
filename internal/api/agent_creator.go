package api

// agent_creator.go — AI 创造 Agent 功能。
//
// 两种模式:
//   - 快速模式(POST /agents/ai-create-quick):调 LLM 生成草稿(基础配置+工具推荐+system prompt),
//     返回前端预览,确认后直接存 agents 表。
//   - 详细模式(POST /agents/ai-create-detail):调 LLM 生成完整 manifest(含示例对话+验收用例),
//     存 agent_definitions,前端预览确认后实例化到 agents 表。
//
// AI 用模型:用户从大模型广场(models 表)选,通过 model_ref 指定。
// 联网搜索:用户可选 Brave/Tavily,生成 Agent 时带上 search_provider 配置。

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── 快速模式 ──

// aiCreateAgentQuick POST /api/agents/ai-create-quick
//
// 请求: {purpose: "帮我写代码的Agent", model_ref: "glm-4", search_provider: "none", search_api_key: ""}
// 响应: 草稿 JSON(不落库,前端预览编辑后调 POST /agents 保存)
func aiCreateAgentQuick(c *gin.Context) {
	var req struct {
		Purpose        string `json:"purpose" binding:"required"`
		ModelRef       string `json:"model_ref" binding:"required"` // 大模型广场 models.id 或 name
		SearchProvider string `json:"search_provider"`
		SearchAPIKey   string `json:"search_api_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.SearchProvider == "" {
		req.SearchProvider = "none"
	}

	// 用用户选的模型查端点(models 表)
	baseURL, apiKey, realName, apiFormat := engine.LookupModelEndpointForCreate(req.ModelRef)
	if baseURL == "" || apiKey == "" {
		c.JSON(http.StatusOK, quickTemplateDraft(req.Purpose, req.ModelRef, req.SearchProvider))
		return
	}

	// 读工具库(供 LLM 推荐)
	tools, _ := store.DB.Query("SELECT id, name, COALESCE(description,'') FROM tools")
	var toolList []map[string]string
	for tools != nil && tools.Next() {
		var id, name, desc string
		tools.Scan(&id, &name, &desc)
		toolList = append(toolList, map[string]string{"id": id, "name": name, "desc": desc})
	}
	if tools != nil {
		tools.Close()
	}

	toolJSON, _ := json.Marshal(toolList)
	prompt := fmt.Sprintf(`你是 Agent 架构师。用户想创建一个"%s"的 AI Agent,绑定模型 %s。

可用工具列表: %s

请设计这个 Agent。返回 JSON(不要其他文字):
{
  "name": "<简短英文名,如 CodeGenExpert>",
  "display_name": "<中文名,如 代码生成专家>",
  "adapter": "api",
  "description": "<一句话说明这个 Agent 做什么>",
  "tools": ["<从可用工具列表里推荐的 tool_id>"],
  "system_prompt": "<该 Agent 的角色定位+行为准则+输出格式,200字以内>",
  "permission_level": "workspace-write",
  "search_provider": "%s"
}`, req.Purpose, realName, string(toolJSON), req.SearchProvider)

	text, err := engine.CallJudgeRaw(baseURL, apiKey, apiFormat, realName, prompt)
	if err != nil {
		log.Printf("[agent-creator] 快速模式 AI 调用失败(%v),返回模板草稿", err)
		c.JSON(http.StatusOK, quickTemplateDraft(req.Purpose, req.ModelRef, req.SearchProvider))
		return
	}

	draft := extractJSONFromLLM(text)
	if draft == nil {
		c.JSON(http.StatusOK, quickTemplateDraft(req.Purpose, req.ModelRef, req.SearchProvider))
		return
	}
	draft["ai_generated"] = true
	draft["model"] = req.ModelRef
	draft["search_provider"] = req.SearchProvider
	c.JSON(http.StatusOK, draft)
}

// quickTemplateDraft 无 Key 时的模板化草稿。
func quickTemplateDraft(purpose, modelRef, searchProvider string) gin.H {
	return gin.H{
		"name":             "CustomAgent",
		"display_name":     purpose,
		"adapter":          "api",
		"description":      "为「" + purpose + "」设计的 Agent(模板草稿,请编辑后保存)",
		"tools":            []string{},
		"system_prompt":    "你是一个「" + purpose + "」的 AI 助手。请根据用户的需求提供专业、准确的帮助。",
		"permission_level": "workspace-write",
		"search_provider":  searchProvider,
		"model":            modelRef,
		"ai_generated":     false,
		"note":             "未配置模型 Key,返回模板草稿。配置 Key 后可生成更精准的 Agent。",
	}
}

// ── 详细模式 ──

// aiCreateAgentDetail POST /api/agents/ai-create-detail
//
// 请求: {purpose, model_ref, search_provider, search_api_key}
// 响应: 完整 manifest(存 agent_definitions,返回给前端预览)
func aiCreateAgentDetail(c *gin.Context) {
	var req struct {
		Purpose        string `json:"purpose" binding:"required"`
		ModelRef       string `json:"model_ref" binding:"required"`
		SearchProvider string `json:"search_provider"`
		SearchAPIKey   string `json:"search_api_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.SearchProvider == "" {
		req.SearchProvider = "none"
	}

	baseURL, apiKey, realName, apiFormat := engine.LookupModelEndpointForCreate(req.ModelRef)
	if baseURL == "" || apiKey == "" {
		def := detailTemplateDraft(req.Purpose, req.ModelRef, req.SearchProvider)
		id := "ad-" + uuid.New().String()[:8]
		manifestJSON, _ := json.Marshal(def)
		store.InsertAgentDefinition(id, def["name"].(string), def["description"].(string), string(manifestJSON))
		def["definition_id"] = id
		c.JSON(http.StatusOK, def)
		return
	}

	// 读工具/MCP/Skill 库供 LLM 参考
	tools := readResourceNames("SELECT id, name FROM tools")
	mcps := readResourceNames("SELECT id, name FROM mcp_servers")
	skills := readResourceNames("SELECT id, name FROM skills")

	prompt := fmt.Sprintf(`你是 Agent 架构师。为「%s」设计一个完整的 Agent 定义文件。

可用资源:
- 工具(Tools): %s
- MCP服务: %s
- 技能(Skills): %s
- 绑定模型: %s
- 联网搜索: %s

生成完整 JSON manifest(不要其他文字):
{
  "name": "<简短英文名>",
  "display_name": "<中文名>",
  "description": "<详细描述这个 Agent 的职责和能力边界>",
  "adapter": "api",
  "model": "%s",
  "tools": ["<推荐的 tool_id>"],
  "mcps": ["<推荐的 mcp_id>"],
  "skills": ["<推荐的 skill_id>"],
  "system_prompt": "<完整的 system prompt:角色定位(你是谁)+ 核心能力(你能做什么)+ 行为准则(你怎么做)+ 输出格式(你输出什么)+ 限制(你不做什么),500字以内>",
  "example_conversations": [
    {"user": "<典型用户问题1>", "agent": "<Agent 应该这样回答1>"},
    {"user": "<典型用户问题2>", "agent": "<Agent 应该这样回答2>"},
    {"user": "<边界情况或刁钻问题>", "agent": "<Agent 的正确处理方式>"}
  ],
  "search_provider": "%s",
  "permission_level": "workspace-write"
}`, req.Purpose, tools, mcps, skills, realName, req.SearchProvider, req.ModelRef, req.SearchProvider)

	text, err := engine.CallJudgeRaw(baseURL, apiKey, apiFormat, realName, prompt)
	if err != nil {
		log.Printf("[agent-creator] 详细模式 AI 调用失败(%v),返回模板草稿", err)
		def := detailTemplateDraft(req.Purpose, req.ModelRef, req.SearchProvider)
		id := "ad-" + uuid.New().String()[:8]
		manifestJSON, _ := json.Marshal(def)
		store.InsertAgentDefinition(id, def["name"].(string), def["description"].(string), string(manifestJSON))
		def["definition_id"] = id
		c.JSON(http.StatusOK, def)
		return
	}

	manifest := extractJSONFromLLM(text)
	if manifest == nil {
		def := detailTemplateDraft(req.Purpose, req.ModelRef, req.SearchProvider)
		id := "ad-" + uuid.New().String()[:8]
		manifestJSON, _ := json.Marshal(def)
		store.InsertAgentDefinition(id, def["name"].(string), def["description"].(string), string(manifestJSON))
		def["definition_id"] = id
		c.JSON(http.StatusOK, def)
		return
	}

	manifest["ai_generated"] = true
	// 存 agent_definitions
	id := "ad-" + uuid.New().String()[:8]
	manifestJSON, _ := json.Marshal(manifest)
	desc, _ := manifest["description"].(string)
	name, _ := manifest["name"].(string)
	store.InsertAgentDefinition(id, name, desc, string(manifestJSON))
	manifest["definition_id"] = id
	c.JSON(http.StatusOK, manifest)
}

// detailTemplateDraft 无 Key 时的详细模式模板草稿。
func detailTemplateDraft(purpose, modelRef, searchProvider string) gin.H {
	return gin.H{
		"name":         "CustomAgent",
		"display_name": purpose,
		"description":  "为「" + purpose + "」设计的完整 Agent 定义(模板草稿,请编辑后保存)",
		"adapter":      "api",
		"model":        modelRef,
		"tools":        []string{},
		"mcps":         []string{},
		"skills":       []string{},
		"system_prompt": fmt.Sprintf(`你是「%s」专家。

核心能力:
- 准确理解用户需求
- 提供专业、可执行的建议
- 遵循最佳实践

行为准则:
- 先理解再回答,必要时澄清
- 给出具体方案而非泛泛而谈
- 主动指出潜在风险

输出格式:
- 结构化(分点/步骤)
- 包含代码示例(如适用)
- 简洁明了

限制:
- 不确定时明确说明
- 不编造信息`, purpose),
		"example_conversations": []gin.H{
			{"user": "你好", "agent": "你好!我是" + purpose + "专家,有什么可以帮你的?"},
			{"user": "请帮我处理一个具体问题", "agent": "好的,请描述具体场景和需求,我会给出针对性方案。"},
		},
		"search_provider":  searchProvider,
		"permission_level": "workspace-write",
		"ai_generated":     false,
		"note":             "未配置模型 Key,返回模板草稿。",
	}
}

// ── 实例化(从 agent_definitions 创建 agents 表记录) ──

// instantiateAgent POST /api/agent-definitions/:id/instantiate
// 前端预览确认后调用,把定义文件实例化到 agents 表。
func instantiateAgent(c *gin.Context) {
	defID := c.Param("id")
	def, err := store.GetAgentDefinition(defID)
	if err != nil || def == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "定义文件不存在"})
		return
	}

	// 解析 manifest
	var m struct {
		Name                 string `json:"name"`
		Adapter              string `json:"adapter"`
		Model                string `json:"model"`
		Tools                any    `json:"tools"`
		SystemPrompt         string `json:"system_prompt"`
		SearchProvider       string `json:"search_provider"`
		PermissionLevel      string `json:"permission_level"`
		ExampleConversations any    `json:"example_conversations"`
	}
	if err := json.Unmarshal([]byte(def.Manifest), &m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "manifest 解析失败: " + err.Error()})
		return
	}
	if m.Adapter == "" {
		m.Adapter = "api"
	}
	if m.SearchProvider == "" {
		m.SearchProvider = "none"
	}
	if m.PermissionLevel == "" {
		m.PermissionLevel = "workspace-write"
	}

	// tools / example_conversations 序列化为 JSON 字符串
	toolsJSON, _ := json.Marshal(m.Tools)
	exampleJSON, _ := json.Marshal(m.ExampleConversations)

	agentID := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	err = store.InsertAgent(store.AgentCreateParams{
		ID: agentID, Name: m.Name, Adapter: m.Adapter, Model: m.Model,
		Tools: string(toolsJSON), PermissionLevel: m.PermissionLevel,
		SystemPrompt: m.SystemPrompt, SearchProvider: m.SearchProvider,
		ExampleConversations: string(exampleJSON), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 回填 instantiated_agent_id
	store.MarkAgentDefinitionInstantiated(defID, agentID)

	c.JSON(http.StatusCreated, gin.H{"agent_id": agentID, "name": m.Name, "instantiated": true})
}

// ── 状态查询 ──

// getAgentStatus GET /api/agents/:id/status
// 查询 Agent 当前运行状态(前端状态显示用)。
func getAgentStatus(c *gin.Context) {
	agentID := c.Param("id")
	run, err := store.GetAgentRun(agentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if run == nil {
		c.JSON(http.StatusOK, gin.H{"status": "idle", "detail": "未运行", "emoji": "💤"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": run.Status, "detail": run.Detail,
		"emoji":     engine.StatusEmoji(run.Status),
		"updated_at": run.UpdatedAt,
	})
}

// ── 联网搜索测试 ──

// testWebSearch POST /api/websearch/test
func testWebSearch(c *gin.Context) {
	var req struct {
		Provider string `json:"provider" binding:"required"`
		APIKey   string `json:"api_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ok, detail := engine.TestWebSearch(req.Provider, req.APIKey)
	c.JSON(http.StatusOK, gin.H{"ok": ok, "detail": detail})
}

// ── 辅助 ──

// readResourceNames 读资源 id+name 列表(供 LLM 参考用)。
func readResourceNames(query string) string {
	rows, err := store.DB.Query(query)
	if err != nil {
		return "[]"
	}
	defer rows.Close()
	var items []map[string]string
	for rows.Next() {
		var id, name string
		rows.Scan(&id, &name)
		items = append(items, map[string]string{"id": id, "name": name})
	}
	_ = items // 避免 unused 警告(实际被 marshal)
	b, _ := json.Marshal(items)
	return string(b)
}
