package api

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ══════════════════════════════════════════
// Tools
// ══════════════════════════════════════════

type Tool struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ParamsSchema string `json:"params_schema"`
	Tags         string `json:"tags"`
	Builtin      bool   `json:"builtin"`
	CreatedAt    string `json:"created_at"`
}

func listTools(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, name, description, params_schema, tags, builtin, created_at FROM tools ORDER BY builtin DESC, created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []Tool
	for rows.Next() {
		var t Tool
		var desc, schema, tags sql.NullString
		var builtin int
		if err := rows.Scan(&t.ID, &t.Name, &desc, &schema, &tags, &builtin, &t.CreatedAt); err != nil {
			continue
		}
		t.Description = desc.String
		t.ParamsSchema = schema.String
		t.Tags = tags.String
		t.Builtin = builtin == 1
		items = append(items, t)
	}
	if items == nil {
		items = []Tool{}
	}
	c.JSON(http.StatusOK, items)
}

func createTool(c *gin.Context) {
	var req struct {
		Name         string `json:"name" binding:"required"`
		Description  string `json:"description"`
		ParamsSchema string `json:"params_schema"`
		Tags         string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	_, err := store.DB.Exec("INSERT INTO tools (id, name, description, params_schema, tags, builtin, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)",
		id, req.Name, nullStr(req.Description), nullStr(req.ParamsSchema), nullStr(req.Tags), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, Tool{ID: id, Name: req.Name, Description: req.Description, ParamsSchema: req.ParamsSchema, Tags: req.Tags, CreatedAt: now})
}

func deleteTool(c *gin.Context) {
	id := c.Param("id")
	res, _ := store.DB.Exec("DELETE FROM tools WHERE id = ?", id)
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "tool not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// ══════════════════════════════════════════
// MCP Servers
// ══════════════════════════════════════════

type MCPServer struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Endpoint     string `json:"endpoint"`
	Status       string `json:"status"`
	Capabilities string `json:"capabilities"`
	CreatedAt    string `json:"created_at"`
}

func listMCPServers(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, name, protocol, endpoint, status, capabilities, created_at FROM mcp_servers ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []MCPServer
	for rows.Next() {
		var m MCPServer
		var endpoint, caps sql.NullString
		if err := rows.Scan(&m.ID, &m.Name, &m.Protocol, &endpoint, &m.Status, &caps, &m.CreatedAt); err != nil {
			continue
		}
		m.Endpoint = endpoint.String
		m.Capabilities = caps.String
		items = append(items, m)
	}
	if items == nil {
		items = []MCPServer{}
	}
	c.JSON(http.StatusOK, items)
}

func createMCPServer(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required"`
		Protocol string `json:"protocol"`
		Endpoint string `json:"endpoint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Protocol == "" {
		req.Protocol = "stdio"
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	_, err := store.DB.Exec("INSERT INTO mcp_servers (id, name, protocol, endpoint, status, created_at) VALUES (?, ?, ?, ?, 'offline', ?)",
		id, req.Name, req.Protocol, nullStr(req.Endpoint), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, MCPServer{ID: id, Name: req.Name, Protocol: req.Protocol, Endpoint: req.Endpoint, Status: "offline", CreatedAt: now})
}

func deleteMCPServer(c *gin.Context) {
	id := c.Param("id")
	res, _ := store.DB.Exec("DELETE FROM mcp_servers WHERE id = ?", id)
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "mcp server not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// ══════════════════════════════════════════
// Skills
// ══════════════════════════════════════════

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ToolIDs     string `json:"tool_ids"`
	Tags        string `json:"tags"`
	Builtin     bool   `json:"builtin"`
	CreatedAt   string `json:"created_at"`
}

func listSkills(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, name, description, tool_ids, tags, builtin, created_at FROM skills ORDER BY builtin DESC, created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []Skill
	for rows.Next() {
		var s Skill
		var desc, toolIDs, tags sql.NullString
		var builtin int
		if err := rows.Scan(&s.ID, &s.Name, &desc, &toolIDs, &tags, &builtin, &s.CreatedAt); err != nil {
			continue
		}
		s.Description = desc.String
		s.ToolIDs = toolIDs.String
		s.Tags = tags.String
		s.Builtin = builtin == 1
		items = append(items, s)
	}
	if items == nil {
		items = []Skill{}
	}
	c.JSON(http.StatusOK, items)
}

func createSkill(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		ToolIDs     string `json:"tool_ids"`
		Tags        string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	_, err := store.DB.Exec("INSERT INTO skills (id, name, description, tool_ids, tags, builtin, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)",
		id, req.Name, nullStr(req.Description), nullStr(req.ToolIDs), nullStr(req.Tags), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, Skill{ID: id, Name: req.Name, Description: req.Description, ToolIDs: req.ToolIDs, Tags: req.Tags, CreatedAt: now})
}

func deleteSkill(c *gin.Context) {
	id := c.Param("id")
	res, _ := store.DB.Exec("DELETE FROM skills WHERE id = ?", id)
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// ══════════════════════════════════════════
// Model Config (role → model mapping)
// ══════════════════════════════════════════

type ModelConfigEntry struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	ModelID  string `json:"model_id"`
	UpdatedAt string `json:"updated_at"`
}

func listModelConfig(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, role, model_id, updated_at FROM model_config ORDER BY role")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []ModelConfigEntry
	for rows.Next() {
		var m ModelConfigEntry
		if err := rows.Scan(&m.ID, &m.Role, &m.ModelID, &m.UpdatedAt); err != nil {
			continue
		}
		items = append(items, m)
	}
	if items == nil {
		items = []ModelConfigEntry{}
	}
	c.JSON(http.StatusOK, items)
}

func upsertModelConfig(c *gin.Context) {
	var req struct {
		Role    string `json:"role" binding:"required"`
		ModelID string `json:"model_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().Format(time.RFC3339)
	// Upsert: try update first, then insert
	res, _ := store.DB.Exec("UPDATE model_config SET model_id=?, updated_at=? WHERE role=?", req.ModelID, now, req.Role)
	n, _ := res.RowsAffected()
	if n == 0 {
		id := uuid.New().String()
		store.DB.Exec("INSERT INTO model_config (id, role, model_id, updated_at) VALUES (?, ?, ?, ?)", id, req.Role, req.ModelID, now)
	}
	c.JSON(http.StatusOK, gin.H{"role": req.Role, "model_id": req.ModelID, "updated_at": now})
}

// ══════════════════════════════════════════
// Prompt Templates
// ══════════════════════════════════════════

type PromptTemplate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Version   int    `json:"version"`
	ModelHint string `json:"model_hint"`
	CreatedAt string `json:"created_at"`
}

func listPromptTemplates(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, name, content, version, model_hint, created_at FROM prompt_templates ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []PromptTemplate
	for rows.Next() {
		var p PromptTemplate
		var hint sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.Content, &p.Version, &hint, &p.CreatedAt); err != nil {
			continue
		}
		p.ModelHint = hint.String
		items = append(items, p)
	}
	if items == nil {
		items = []PromptTemplate{}
	}
	c.JSON(http.StatusOK, items)
}

func createPromptTemplate(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		Content   string `json:"content" binding:"required"`
		ModelHint string `json:"model_hint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	_, err := store.DB.Exec("INSERT INTO prompt_templates (id, name, content, version, model_hint, created_at) VALUES (?, ?, ?, 1, ?, ?)",
		id, req.Name, req.Content, nullStr(req.ModelHint), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, PromptTemplate{ID: id, Name: req.Name, Content: req.Content, Version: 1, ModelHint: req.ModelHint, CreatedAt: now})
}

	func deletePromptTemplate(c *gin.Context) {
		id := c.Param("id")
		res, _ := store.DB.Exec("DELETE FROM prompt_templates WHERE id = ?", id)
		n, _ := res.RowsAffected()
		if n == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": id})
	}

	// updatePromptTemplate PUT /api/prompt-templates/:id
	// 更新模板内容,自动递增版本号。旧版本保留在 prompt_template_versions 表中。
	func updatePromptTemplate(c *gin.Context) {
		id := c.Param("id")
		var req struct {
			Name      string `json:"name"`
			Content   string `json:"content" binding:"required"`
			ModelHint string `json:"model_hint"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// 读当前版本(连同 model_hint 一起,归档需要)
		var currentVersion int
		var oldContent, oldName, oldModelHint string
		err := store.DB.QueryRow("SELECT version, content, name, COALESCE(model_hint,'') FROM prompt_templates WHERE id = ?", id).
			Scan(&currentVersion, &oldContent, &oldName, &oldModelHint)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
			return
		}

		// 保存旧版本到版本历史表(参数对齐:7 列 7 个值)
		if _, e := store.DB.Exec(`INSERT INTO prompt_template_versions (id, template_id, version, name, content, model_hint, created_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			uuid.New().String(), id, currentVersion, oldName, oldContent, nullStr(oldModelHint)); e != nil {
			log.Printf("[resource2] 归档旧版本失败 template=%s: %v", id, e)
		}

		// 更新为新版本(version +1)
		newVersion := currentVersion + 1
		name := oldName
		if req.Name != "" {
			name = req.Name
		}
		store.DB.Exec("UPDATE prompt_templates SET name=?, content=?, version=?, model_hint=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
			name, req.Content, newVersion, nullStr(req.ModelHint), id)

		c.JSON(http.StatusOK, gin.H{
			"id":      id,
			"version": newVersion,
			"message": "模板已更新,旧版本已存档",
		})
	}

	// listPromptTemplateVersions GET /api/prompt-templates/:id/versions
	// 获取某个模板的所有历史版本。
	func listPromptTemplateVersions(c *gin.Context) {
		id := c.Param("id")
		rows, err := store.DB.Query(`SELECT id, version, name, content, model_hint, created_at
			FROM prompt_template_versions WHERE template_id = ?
			UNION ALL
			SELECT id, version, name, content, model_hint, created_at
			FROM prompt_templates WHERE id = ?
			ORDER BY version DESC`, id, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()

		var items []PromptTemplate
		for rows.Next() {
			var p PromptTemplate
			var hint sql.NullString
			rows.Scan(&p.ID, &p.Version, &p.Name, &p.Content, &hint, &p.CreatedAt)
			p.ModelHint = hint.String
			items = append(items, p)
		}
		if items == nil {
			items = []PromptTemplate{}
		}
		c.JSON(http.StatusOK, items)
	}

	// rollbackPromptTemplate POST /api/prompt-templates/:id/rollback
	// Body: {"version": 3} 回滚到指定版本。
	func rollbackPromptTemplate(c *gin.Context) {
		id := c.Param("id")
		var req struct {
			Version int `json:"version" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// 查找目标版本的内容
		var content, name, modelHint string
		err := store.DB.QueryRow(`SELECT content, name, model_hint FROM prompt_template_versions
			WHERE template_id = ? AND version = ?`, id, req.Version).
			Scan(&content, &name, &modelHint)
		if err != nil {
			// 也可能当前版本就是目标版本
			err2 := store.DB.QueryRow(`SELECT content, name, model_hint FROM prompt_templates
				WHERE id = ? AND version = ?`, id, req.Version).
				Scan(&content, &name, &modelHint)
			if err2 != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
				return
			}
		}

		// 保存当前版本(归档),然后回滚
		var curVersion int
		var curName, curContent, curModelHint string
		store.DB.QueryRow("SELECT version, name, content, COALESCE(model_hint,'') FROM prompt_templates WHERE id = ?", id).
			Scan(&curVersion, &curName, &curContent, &curModelHint)
		if _, e := store.DB.Exec(`INSERT INTO prompt_template_versions (id, template_id, version, name, content, model_hint, created_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			uuid.New().String(), id, curVersion, curName, curContent, nullStr(curModelHint)); e != nil {
			log.Printf("[resource2] 归档当前版本失败 template=%s: %v", id, e)
		}

		newVersion := req.Version + 100 // 回滚后版本号大幅跳跃,避免与后续正常编辑混淆
		store.DB.Exec("UPDATE prompt_templates SET content=?, name=?, version=?, model_hint=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
			content, name, newVersion, nullStr(modelHint), id)

		c.JSON(http.StatusOK, gin.H{
			"id":             id,
			"rolled_to":      req.Version,
			"new_version":    newVersion,
			"message":        "已回滚到 v" + itoa(req.Version),
		})
	}
