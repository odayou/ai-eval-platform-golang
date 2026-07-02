// api/scene.go — 自定义评测场景管理(MRD 6.2 自建场景Tab)。
//
// "左侧导航底部有一个'+ 创建评测场景'的入口。用户点开后可以创建一个
//   全新的评测场景(如'AIGC 文生图评测''实验报告评测''客服话术质量评测')，
//   配置该场景下的被测对象接入方式和默认规则集。
//   创建后这个新场景会出现在左侧导航，与默认5个并列。"
//
// API:
//   GET  /api/scenes        — 列出所有场景(默认5 + 用户自定义)
//   POST /api/scenes        — 创建自定义场景
//   PUT  /api/scenes/:id    — 更新场景
//   DELETE /api/scenes/:id  — 删除场景(仅自定义场景可删)
package api

import (
	"net/http"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// defaultScenes 是 5 个预置场景(不可删除)。
var defaultScenes = []map[string]string{
	{"id": "agent", "label": "Agent 评测", "description": "测各类智能体(编码 Agent、通用 Agent、团队自研 Agent)", "strategy": "sandbox_roundtable", "builtin": "1"},
	{"id": "model", "label": "大模型评测", "description": "测裸模型能力(GLM / DeepSeek / Claude / GPT 等 API)", "strategy": "api_direct", "builtin": "1"},
	{"id": "competitor", "label": "竞品报告评测", "description": "测/对比竞品分析报告(Cursor vs Trae、各家 Agent 横评)", "strategy": "text_review", "builtin": "1"},
	{"id": "prd", "label": "PRD 评测", "description": "测 AI 参与生成的产品文档(需求文档、功能规格)", "strategy": "text_review", "builtin": "1"},
	{"id": "product", "label": "AI 产品评测", "description": "测完整的 AI 产品/工具(Trae / Cursor / zCode / 文生图产品)", "strategy": "multimodal_review", "builtin": "1"},
}

// listScenes GET /api/scenes
//
// 返回全部场景(5 个内置 + 自定义),并去重:DB 中存在的同名内置场景不再追加,
// 避免出现重复 Tab(MRD 批次④ 修复 builtin 双源问题)。
// 每个 scene 携带 rule_set_id / default_question_set_id(供前端联动预选)。
func listScenes(c *gin.Context) {
	// 从 DB 读全部场景(含可能被 seed 或用户创建的)
	rows, err := store.DB.Query(`SELECT id, label, COALESCE(description,''), COALESCE(strategy,'text_review'),
		COALESCE(rule_set_id,''), COALESCE(default_question_set_id,''), builtin FROM scenes ORDER BY builtin DESC, created_at ASC`)
	if err != nil {
		// scenes 表未就绪:只返回默认场景
		out := make([]map[string]string, 0, len(defaultScenes))
		for _, s := range defaultScenes {
			out = append(out, s)
		}
		c.JSON(http.StatusOK, out)
		return
	}
	defer rows.Close()

	// 用 map 去重:id 已存在则不重复加入(内置场景 id 固定)
	seen := make(map[string]bool)
	allScenes := []map[string]string{}

	// 先放内置场景
	for _, s := range defaultScenes {
		allScenes = append(allScenes, s)
		seen[s["id"]] = true
	}
	// 再放 DB 场景(跳过与内置重复的 id)
	for rows.Next() {
		var id, label, description, strategy, ruleSetID, qsetID string
		var builtin int
		rows.Scan(&id, &label, &description, &strategy, &ruleSetID, &qsetID, &builtin)
		if seen[id] {
			continue // 去重:内置已存在
		}
		allScenes = append(allScenes, map[string]string{
			"id":                        id,
			"label":                     label,
			"description":               description,
			"strategy":                  strategy,
			"rule_set_id":               ruleSetID,
			"default_question_set_id":   qsetID,
			"builtin":                   itoa(builtin),
		})
		seen[id] = true
	}

	c.JSON(http.StatusOK, allScenes)
}

// createScene POST /api/scenes
// Body: {"label":"AIGC 文生图评测", "description":"...", "strategy":"text_review",
//        "rule_set_id":"rs-arch", "default_question_set_id":"qs-xxx"}
func createScene(c *gin.Context) {
	var req struct {
		Label                 string `json:"label" binding:"required"`
		Description           string `json:"description"`
		Strategy              string `json:"strategy"`
		RuleSetID             string `json:"rule_set_id"`
		DefaultQuestionSetID  string `json:"default_question_set_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label 不能为空"})
		return
	}
	if req.Strategy == "" {
		req.Strategy = "text_review"
	}

	id := uuid.New().String()
	_, err := store.DB.Exec(`INSERT INTO scenes (id, label, description, strategy, rule_set_id, default_question_set_id, builtin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, req.Label, req.Description, req.Strategy, req.RuleSetID, req.DefaultQuestionSetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":                      id,
		"label":                   req.Label,
		"description":             req.Description,
		"strategy":                req.Strategy,
		"rule_set_id":             req.RuleSetID,
		"default_question_set_id": req.DefaultQuestionSetID,
		"builtin":                 "0",
	})
}

// updateScene PUT /api/scenes/:id
func updateScene(c *gin.Context) {
	id := c.Param("id")

	// 检查是否是内置场景
	var builtin int
	store.DB.QueryRow("SELECT builtin FROM scenes WHERE id = ?", id).Scan(&builtin)
	if builtin == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "内置场景不可修改"})
		return
	}

	var req struct {
		Label                string `json:"label"`
		Description          string `json:"description"`
		Strategy             string `json:"strategy"`
		RuleSetID            string `json:"rule_set_id"`
		DefaultQuestionSetID string `json:"default_question_set_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	store.DB.Exec(`UPDATE scenes SET label=?, description=?, strategy=?, rule_set_id=?, default_question_set_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		req.Label, req.Description, req.Strategy, req.RuleSetID, req.DefaultQuestionSetID, id)

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// deleteScene DELETE /api/scenes/:id
func deleteScene(c *gin.Context) {
	id := c.Param("id")

	// 检查是否是内置场景
	var builtin int
	store.DB.QueryRow("SELECT builtin FROM scenes WHERE id = ?", id).Scan(&builtin)
	if builtin == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "内置场景不可删除"})
		return
	}

	store.DB.Exec("DELETE FROM scenes WHERE id = ?", id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// itoa 简易 int→string。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
