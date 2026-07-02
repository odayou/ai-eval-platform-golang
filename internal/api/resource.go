package api

import (
	"database/sql"
	"net/http"
	"time"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Agents ──

type Agent struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Adapter         string `json:"adapter"`
	Model           string `json:"model"`
	Tools           string `json:"tools"`
	Online          bool   `json:"online"`
	PermissionLevel string `json:"permission_level"` // MRD §5.6: read-only | workspace-write | full
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func listAgents(c *gin.Context) {
	summaries, err := store.ListAgents()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// AgentSummary → API 的 Agent 视图
	agents := make([]Agent, 0, len(summaries))
	for _, s := range summaries {
		agents = append(agents, Agent{
			ID: s.ID, Name: s.Name, Adapter: s.Adapter, Model: s.Model,
			Tools: s.Tools, Online: s.Online, PermissionLevel: s.PermissionLevel,
			CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, agents)
}

func createAgent(c *gin.Context) {
	var req struct {
		Name                string `json:"name" binding:"required"`
		Adapter             string `json:"adapter"`
		Model               string `json:"model"`
		Tools               string `json:"tools"`
		PermissionLevel     string `json:"permission_level"`
		SystemPrompt        string `json:"system_prompt"`
		SearchProvider      string `json:"search_provider"`
		SearchAPIKey        string `json:"search_api_key"`
		ExampleConversations string `json:"example_conversations"`
		AgentsMD            string `json:"agents_md"`
		SkillIDs            string `json:"skill_ids"`
		MaxTurns            int    `json:"max_turns"`
		VerifyCommand       string `json:"verify_command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Adapter == "" {
		req.Adapter = "api"
	}
	if req.PermissionLevel == "" {
		req.PermissionLevel = "workspace-write"
	}
	if req.SearchProvider == "" {
		req.SearchProvider = "none"
	}
	if req.MaxTurns == 0 {
		req.MaxTurns = 8
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	err := store.InsertAgent(store.AgentCreateParams{
		ID: id, Name: req.Name, Adapter: req.Adapter, Model: req.Model, Tools: req.Tools,
		PermissionLevel: req.PermissionLevel, SystemPrompt: req.SystemPrompt,
		SearchProvider: req.SearchProvider, SearchAPIKey: req.SearchAPIKey,
		ExampleConversations: req.ExampleConversations, AgentsMD: req.AgentsMD,
		SkillIDs: req.SkillIDs, MaxTurns: req.MaxTurns, VerifyCommand: req.VerifyCommand,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, Agent{ID: id, Name: req.Name, Adapter: req.Adapter, Model: req.Model, Tools: req.Tools, PermissionLevel: req.PermissionLevel, CreatedAt: now, UpdatedAt: now})
}

func getAgent(c *gin.Context) {
	id := c.Param("id")
	s, err := store.GetAgentSummary(id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, Agent{
		ID: s.ID, Name: s.Name, Adapter: s.Adapter, Model: s.Model,
		Tools: s.Tools, Online: s.Online, PermissionLevel: s.PermissionLevel,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	})
}

func updateAgent(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name            string `json:"name"`
		Adapter         string `json:"adapter"`
		Model           string `json:"model"`
		Tools           string `json:"tools"`
		PermissionLevel string `json:"permission_level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// permission_level 为空时保持原值(不覆盖),避免 PATCH 语义丢失默认值
	if req.PermissionLevel == "" {
		req.PermissionLevel = "workspace-write"
	}
	now := time.Now().Format(time.RFC3339)
	n, err := store.UpdateAgent(id, store.AgentUpdateParams{
		Name: req.Name, Adapter: req.Adapter, Model: req.Model,
		Tools: req.Tools, PermissionLevel: req.PermissionLevel, UpdatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "updated_at": now})
}

// toggleAgentOnline PUT /api/agents/:id/online
// 切换 Agent 上下线状态。上线=online=1(可被评测任务选用),下线=online=0。
func toggleAgentOnline(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Online bool `json:"online"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := store.UpdateAgentOnline(id, req.Online)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if res == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "online": req.Online})
}

func deleteAgent(c *gin.Context) {
	id := c.Param("id")
	n, err := store.DeleteAgent(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// ── Models ──

type ModelEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIFormat string `json:"api_format"`
	Available bool   `json:"available"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func listModels(c *gin.Context) {
	models, err := store.ListModelsPublic()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]ModelEntry, 0, len(models))
	for _, m := range models {
		out = append(out, modelRowToEntry(m))
	}
	c.JSON(http.StatusOK, out)
}

func createModel(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		BaseURL   string `json:"base_url" binding:"required"`
		APIKey    string `json:"api_key"`
		APIFormat string `json:"api_format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	err := store.InsertModel(store.ModelCreateParams{
		ID: id, Name: req.Name, BaseURL: req.BaseURL,
		APIKey: engine.EncryptSecret(req.APIKey), APIFormat: req.APIFormat,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, ModelEntry{ID: id, Name: req.Name, BaseURL: req.BaseURL, APIFormat: req.APIFormat, CreatedAt: now, UpdatedAt: now})
}

func getModel(c *gin.Context) {
	id := c.Param("id")
	m, err := store.GetModelPublic(id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, modelRowToEntry(*m))
}

func updateModel(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name      string `json:"name"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		APIFormat string `json:"api_format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().Format(time.RFC3339)
	n, err := store.UpdateModel(id, store.ModelUpdateParams{
		Name: req.Name, BaseURL: req.BaseURL,
		APIKey: engine.EncryptSecret(req.APIKey), APIFormat: req.APIFormat, UpdatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "updated_at": now})
}

func deleteModel(c *gin.Context) {
	id := c.Param("id")
	n, err := store.DeleteModel(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// modelRowToEntry 把 store.ModelRow 转成 API 层的 ModelEntry(不暴露 api_key)。
func modelRowToEntry(m store.ModelRow) ModelEntry {
	return ModelEntry{
		ID: m.ID, Name: m.Name, BaseURL: m.BaseURL, APIFormat: m.APIFormat,
		Available: m.Available, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// debugModel POST /api/models/:id/debug
// 直接调该模型端点,返回 LLM 的真实回复(供大模型库调试用)。
// 支持 OpenAI/Anthropic/Gemini 全协议。
func debugModel(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 查模型端点(含 apiFormat)
	baseURL, apiKey, realName, apiFormat := engine.LookupModelEndpointForCreate(id)
	if baseURL == "" || apiKey == "" {
		// 如果按 ID 没找到,试 name 匹配(页面列表展示 name)
		baseURL, apiKey, realName, apiFormat = engine.LookupModelEndpointForCreate("non-existent-sentinel")
		// 用 name 查
		var n, u, k, af sql.NullString
		err := store.DB.QueryRow("SELECT name, base_url, COALESCE(api_key,''), COALESCE(api_format,'') FROM models WHERE name = ?", id).Scan(&n, &u, &k, &af)
		if err != nil || u.String == "" || k.String == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "模型未找到或未配置 API Key"})
			return
		}
		baseURL = u.String
		apiKey = engine.DecryptSecret(k.String)
		realName = n.String
		apiFormat = af.String
	}

	engine.BindEvalContext("debug:model:" + id)
	engine.SetAuditContext("debug:model:"+id, 0)
	defer func() {
		engine.ClearAuditContext()
		engine.UnbindEvalContext()
	}()

	text, err := engine.CallJudgeRaw(baseURL, apiKey, apiFormat, realName, req.Message)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error(), "ok": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"reply":  text,
		"model":  realName,
		"format": apiFormat,
	})
}
// 真实调用该 Agent 配置的模型端点,返回 LLM 的真实回复。
// 支持 OpenAI/Anthropic/Gemini 全协议(经 engine.CallJudgeRaw → provider 适配层)。
func debugAgent(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Message string `json:"message" binding:"required"`
		History []struct {
			Role string `json:"role"` // user | assistant
			Text string `json:"text"`
		} `json:"history"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	agent, err := store.GetAgent(id)
	if err != nil || agent == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	if agent.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该 Agent 未配置模型,请先在资源管理添加模型"})
		return
	}

	// 查模型端点(含 api_format)
	baseURL, apiKey, realName, apiFormat := engine.LookupModelEndpointForCreate(agent.Model)
	if baseURL == "" || apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模型 " + agent.Model + " 未找到或未配置 API Key"})
		return
	}

	// 构造多轮 messages(system prompt + 历史 + 当前消息)
	var messages []engine.AgentMessage
	for _, h := range req.History {
		if h.Role == "user" || h.Role == "assistant" {
			messages = append(messages, engine.AgentMessage{Role: h.Role, Content: h.Text})
		}
	}
	messages = append(messages, engine.AgentMessage{Role: "user", Content: req.Message})

	// 真实调用 LLM(多轮上下文,OpenAI 走 messages 数组,Anthropic/Gemini 拍平兜底)
	text, err := engine.CallAgentMessages(baseURL, apiKey, apiFormat, realName, agent.SystemPrompt, messages)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error(), "ok": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"reply":  text,
		"model":  realName,
		"format": apiFormat,
	})
}
