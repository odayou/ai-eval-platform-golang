package api

// question.go — 评测题库 CRUD。
//
// 题库是 ②引擎的输入:引擎读题 → 喂给被测 Agent → 规则打分。
// 四种来源(manual/ai/baseline/dynamic)统一在此表管理(MRD 9.3)。
//
// 支持按 scene / difficulty / source / tag 筛选(评测题库页左侧筛选用)。

import (
	"database/sql"
	"net/http"
	"time"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Question 是评测题库的一条记录,对应 questions 表。
type Question struct {
	ID           string `json:"id"`
	Scene        string `json:"scene"`
	Title        string `json:"title"`
	Prompt       string `json:"prompt"`
	Expected     string `json:"expected"`
	Difficulty   string `json:"difficulty"`
	Source       string `json:"source"`
	Tags         string `json:"tags"`
	RuleSetID    string `json:"rule_set_id"`
	SourceRepo   string `json:"source_repo"`   // V2 7.1 来源仓库
	SourceURL    string `json:"source_url"`    // V2 7.1 来源文件 URL
	Desensitized bool   `json:"desensitized"`  // V2 7.1 已脱敏
	CreatedAt    string `json:"created_at"`
}

// difficultyLabel / sourceLabel 供前端友好显示。
var difficultyLabel = map[string]string{
	"entry": "入门", "mid": "中级", "high": "高级", "adversarial": "对抗",
}
var sourceLabel = map[string]string{
	"manual": "手动录入", "ai": "AI 生成", "baseline": "基线库", "dynamic": "动态扩题",
}

// listQuestions GET /api/questions
// 支持 query 参数筛选:scene / difficulty / source。
func listQuestions(c *gin.Context) {
	// 动态拼装 WHERE,避免 SQL 注入(参数走占位符)
		q := `SELECT id, scene, title, prompt, COALESCE(expected,''), difficulty, source,
		      COALESCE(tags,''), COALESCE(rule_set_id,''), COALESCE(source_repo,''), COALESCE(source_url,''), COALESCE(desensitized,0), created_at FROM questions`
		var (
			conds  []string
			args   []any
		)
		if s := c.Query("scene"); s != "" {
			conds = append(conds, "scene = ?")
			args = append(args, s)
		}
		if d := c.Query("difficulty"); d != "" {
			conds = append(conds, "difficulty = ?")
			args = append(args, d)
		}
		if src := c.Query("source"); src != "" {
			conds = append(conds, "source = ?")
			args = append(args, src)
		}
		if repo := c.Query("source_repo"); repo != "" {
			conds = append(conds, "source_repo = ?")
			args = append(args, repo)
		}
		// 关键词搜索(匹配 title 或 prompt,LIKE 模糊)
		if kw := c.Query("keyword"); kw != "" {
			conds = append(conds, "(title LIKE ? OR prompt LIKE ?)")
			args = append(args, "%"+kw+"%", "%"+kw+"%")
		}
	if len(conds) > 0 {
		q += " WHERE " + joinAnd(conds)
	}
	q += " ORDER BY created_at DESC"

	rows, err := store.DB.Query(q, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []Question{}
	for rows.Next() {
		var it Question
		if err := rows.Scan(&it.ID, &it.Scene, &it.Title, &it.Prompt, &it.Expected,
			&it.Difficulty, &it.Source, &it.Tags, &it.RuleSetID, &it.SourceRepo, &it.SourceURL, &it.Desensitized, &it.CreatedAt); err != nil {
			continue
		}
		items = append(items, it)
	}
	c.JSON(http.StatusOK, items)
}

// createQuestion POST /api/questions
func createQuestion(c *gin.Context) {
	var req struct {
		Scene      string `json:"scene"`
		Title      string `json:"title" binding:"required"`
		Prompt     string `json:"prompt" binding:"required"`
		Expected   string `json:"expected"`
		Difficulty string `json:"difficulty"`
		Source     string `json:"source"`
		Tags       string `json:"tags"`
		RuleSetID  string `json:"rule_set_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 兜底默认值,避免空字段
	if req.Scene == "" {
		req.Scene = "agent"
	}
	// scene 存在性校验(MRD 批次④:强绑定)
	if !isValidScene(req.Scene) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的测评分类(scene): " + req.Scene})
		return
	}
	if req.Difficulty == "" {
		req.Difficulty = "mid"
	}
	if req.Source == "" {
		req.Source = "manual"
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	err := store.InsertQuestion(store.QuestionCreateParams{
		ID: id, Scene: req.Scene, Title: req.Title, Prompt: req.Prompt,
		Expected: req.Expected, Difficulty: req.Difficulty, Source: req.Source,
		Tags: req.Tags, RuleSetID: req.RuleSetID, CreatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, Question{
		ID: id, Scene: req.Scene, Title: req.Title, Prompt: req.Prompt, Expected: req.Expected,
		Difficulty: req.Difficulty, Source: req.Source, Tags: req.Tags, RuleSetID: req.RuleSetID,
		CreatedAt: now,
	})
}

// getQuestion GET /api/questions/:id
func getQuestion(c *gin.Context) {
	id := c.Param("id")
	qr, err := store.GetQuestion(id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "question not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, Question{
		ID: qr.ID, Scene: qr.Scene, Title: qr.Title, Prompt: qr.Prompt,
		Expected: qr.Expected, Difficulty: qr.Difficulty, Source: qr.Source,
		Tags: qr.Tags, RuleSetID: qr.RuleSetID, SourceRepo: qr.SourceRepo,
		SourceURL: qr.SourceURL, Desensitized: qr.Desensitized, CreatedAt: qr.CreatedAt,
	})
}

// updateQuestion PUT /api/questions/:id
func updateQuestion(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Scene      string `json:"scene"`
		Title      string `json:"title"`
		Prompt     string `json:"prompt"`
		Expected   string `json:"expected"`
		Difficulty string `json:"difficulty"`
		Source     string `json:"source"`
		Tags       string `json:"tags"`
		RuleSetID  string `json:"rule_set_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n, err := store.UpdateQuestion(id, store.QuestionUpdateParams{
		Scene: req.Scene, Title: req.Title, Prompt: req.Prompt, Expected: req.Expected,
		Difficulty: req.Difficulty, Source: req.Source, Tags: req.Tags, RuleSetID: req.RuleSetID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "question not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// deleteQuestion DELETE /api/questions/:id
func deleteQuestion(c *gin.Context) {
	id := c.Param("id")
	n, err := store.DeleteQuestion(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "question not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// joinAnd 用 " AND " 连接条件片段(小工具,避免引 strings 包)。
func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}

// generateQuestions POST /api/questions/generate — 圆桌出题
// Body: {"scene":"agent", "rule_set_id":"rs-fullstack", "count_per_dim": 2}
func generateQuestions(c *gin.Context) {
	var req struct {
		Scene       string `json:"scene"`
		RuleSetID   string `json:"rule_set_id" binding:"required"`
		CountPerDim int    `json:"count_per_dim"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.CountPerDim <= 0 {
		req.CountPerDim = 2
	}
	if req.Scene == "" {
		req.Scene = "agent"
	}

	questions, err := engine.GenerateQuestions(req.Scene, req.RuleSetID, req.CountPerDim)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"generated": len(questions), "questions": questions})
}

// expandQuestion POST /api/questions/:id/expand — 动态扩题
// Body: {"count": 3}
func expandQuestion(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Count int `json:"count"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Count <= 0 {
		req.Count = 3
	}

	questions, err := engine.ExpandQuestions(id, req.Count)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"expanded": len(questions), "questions": questions})
}
