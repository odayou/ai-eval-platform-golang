package api

import (
	"database/sql"
	"net/http"
	"time"

	"ai-eval-platform/internal/scheduler"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Task struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Scene         string `json:"scene"`
	AgentID       string `json:"agent_id"`
	RuleSet       string `json:"rule_set"`
	Status        string `json:"status"`
	Result        string `json:"result"`
	ChaosEnabled  bool   `json:"chaos_enabled"`
	ExpertGroupID string `json:"expert_group_id"` // 专家组 ID(留空用默认)
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func listTasks(c *gin.Context) {
	summaries, err := store.ListTasks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tasks := make([]Task, 0, len(summaries))
	for _, s := range summaries {
		tasks = append(tasks, taskSummaryToAPI(s))
	}
	c.JSON(http.StatusOK, tasks)
}

func createTask(c *gin.Context) {
	var req struct {
		Name          string `json:"name" binding:"required"`
		Scene         string `json:"scene"`
		AgentID       string `json:"agent_id"`
		RuleSet       string `json:"rule_set"`
		ChaosEnabled  bool   `json:"chaos_enabled"`
		ExpertGroupID string `json:"expert_group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scene == "" {
		req.Scene = "agent"
	}
	// scene 存在性校验(MRD 批次④:强绑定)
	// 必须是内置 5 个分类之一,或在 scenes 表里存在,否则拒绝(避免脏数据)。
	if !isValidScene(req.Scene) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的测评分类(scene): " + req.Scene + ",请先在场景管理创建"})
		return
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	err := store.InsertTask(store.TaskCreateParams{
		ID: id, Name: req.Name, Scene: req.Scene,
		AgentID: req.AgentID, RuleSet: req.RuleSet,
		ChaosEnabled: req.ChaosEnabled, ExpertGroupID: req.ExpertGroupID,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, Task{ID: id, Name: req.Name, Scene: req.Scene, AgentID: req.AgentID, RuleSet: req.RuleSet, ChaosEnabled: req.ChaosEnabled, ExpertGroupID: req.ExpertGroupID, Status: "pending", CreatedAt: now, UpdatedAt: now})

	// 入队调度器(⑤ worker pool),高优先级(用户手动触发)。
	// 调度器异步调 engine.Run,不阻塞 HTTP 响应。
	// 好处:并发受 MaxWorkers 限制、有超时熔断和失败重试。
	if err := scheduler.SubmitGlobalHigh(id); err != nil {
		// 队列满,降级为直接执行(保底不丢任务)
		scheduler.RunDirect(id)
	}
}

func getTask(c *gin.Context) {
	id := c.Param("id")
	s, err := store.GetTaskSummary(id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, taskSummaryToAPI(*s))
}

func deleteTask(c *gin.Context) {
	id := c.Param("id")
	n, err := store.DeleteTask(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// rerunTask POST /api/tasks/:id/rerun
// 复制原任务的配置(Agent/规则集/场景/混沌/专家组),创建一个新的 pending 任务并立即入队。
// 用途:改了 Agent 或规则后,一键复测验证,免去手动重建。
func rerunTask(c *gin.Context) {
	srcID := c.Param("id")

	// 读原任务配置(用 GetTaskSummary,字段齐全含 chaos/expert_group)
	src, err := store.GetTaskSummary(srcID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "原任务不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 复制配置创建新任务(名字加"重跑"后缀)
	newID := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	newName := src.Name + " (重跑)"
	if err := store.InsertTask(store.TaskCreateParams{
		ID: newID, Name: newName, Scene: src.Scene,
		AgentID: src.AgentID, RuleSet: src.RuleSet,
		ChaosEnabled: src.ChaosEnabled, ExpertGroupID: src.ExpertGroupID,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 高优先级入队(用户手动触发的复测)
	if err := scheduler.SubmitGlobalHigh(newID); err != nil {
		scheduler.RunDirect(newID)
	}

	c.JSON(http.StatusCreated, Task{
		ID: newID, Name: newName, Scene: src.Scene,
		AgentID: src.AgentID, RuleSet: src.RuleSet,
		ChaosEnabled: src.ChaosEnabled, ExpertGroupID: src.ExpertGroupID,
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	})
}

// taskSummaryToAPI 把 store.TaskSummary 转成 API 层的 Task 视图。
func taskSummaryToAPI(s store.TaskSummary) Task {
	return Task{
		ID: s.ID, Name: s.Name, Scene: s.Scene, AgentID: s.AgentID,
		RuleSet: s.RuleSet, Status: s.Status, Result: s.Result,
		ChaosEnabled: s.ChaosEnabled, ExpertGroupID: s.ExpertGroupID,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

// ── Rules ──

type Rule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Dimension string  `json:"dimension"`
	Scoring   string  `json:"scoring"`
	Weight    float64 `json:"weight"`
	Content   string  `json:"content"`
	Tags      string  `json:"tags"`
	CreatedAt string  `json:"created_at"`
}

func listRules(c *gin.Context) {
	rules, err := store.ListRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		out = append(out, Rule{
			ID: r.ID, Name: r.Name, Dimension: r.Dimension, Scoring: r.Scoring,
			Weight: r.Weight, Content: r.Content, Tags: r.Tags, CreatedAt: r.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

func createRule(c *gin.Context) {
	var req struct {
		Name      string  `json:"name" binding:"required"`
		Dimension string  `json:"dimension" binding:"required"`
		Scoring   string  `json:"scoring"`
		Weight    float64 `json:"weight"`
		Content   string  `json:"content" binding:"required"`
		Tags      string  `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scoring == "" {
		req.Scoring = "llm"
	}
	if req.Weight == 0 {
		req.Weight = 1.0
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	err := store.InsertRule(store.RuleCreateParams{
		ID: id, Name: req.Name, Dimension: req.Dimension, Scoring: req.Scoring,
		Weight: req.Weight, Content: req.Content, Tags: req.Tags, CreatedAt: now,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, Rule{ID: id, Name: req.Name, Dimension: req.Dimension, Scoring: req.Scoring, Weight: req.Weight, Content: req.Content, Tags: req.Tags, CreatedAt: now})
}

func updateRule(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name      string  `json:"name"`
		Dimension string  `json:"dimension"`
		Scoring   string  `json:"scoring"`
		Weight    float64 `json:"weight"`
		Content   string  `json:"content"`
		Tags      string  `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n, err := store.UpdateRule(id, store.RuleUpdateParams{
		Name: req.Name, Dimension: req.Dimension, Scoring: req.Scoring,
		Weight: req.Weight, Content: req.Content, Tags: req.Tags,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id})
}

func deleteRule(c *gin.Context) {
	id := c.Param("id")
	n, err := store.DeleteRule(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// ── Rule Sets ──

type RuleSet struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Scene     string `json:"scene"`
	RuleIDs   string `json:"rule_ids"`
	CreatedAt string `json:"created_at"`
}

func listRuleSets(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, name, scene, rule_ids, created_at FROM rule_sets ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var sets []RuleSet
	for rows.Next() {
		var s RuleSet
		var scene, ruleIDs sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &scene, &ruleIDs, &s.CreatedAt); err != nil {
			continue
		}
		s.Scene = scene.String
		s.RuleIDs = ruleIDs.String
		sets = append(sets, s)
	}
	if sets == nil {
		sets = []RuleSet{}
	}
	c.JSON(http.StatusOK, sets)
}

func createRuleSet(c *gin.Context) {
	var req struct {
		Name    string `json:"name" binding:"required"`
		Scene   string `json:"scene"`
		RuleIDs string `json:"rule_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// scene 非空时校验合法性(空表示通用规则集,允许)
	if req.Scene != "" && !isValidScene(req.Scene) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的测评分类(scene): " + req.Scene})
		return
	}
	id := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	_, err := store.DB.Exec("INSERT INTO rule_sets (id, name, scene, rule_ids, created_at) VALUES (?, ?, ?, ?, ?)",
		id, req.Name, nullStr(req.Scene), nullStr(req.RuleIDs), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, RuleSet{ID: id, Name: req.Name, Scene: req.Scene, RuleIDs: req.RuleIDs, CreatedAt: now})
}

func updateRuleSet(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name    string `json:"name"`
		Scene   string `json:"scene"`
		RuleIDs string `json:"rule_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := store.DB.Exec("UPDATE rule_sets SET name=?, scene=?, rule_ids=? WHERE id=?",
		req.Name, nullStr(req.Scene), nullStr(req.RuleIDs), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule set not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id})
}

func deleteRuleSet(c *gin.Context) {
	id := c.Param("id")
	res, err := store.DB.Exec("DELETE FROM rule_sets WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule set not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// nullStr wraps a string as sql.NullString for nullable DB columns.
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// isValidScene 校验 scene 是否合法(MRD 批次④:强绑定)。
// 合法 = 5 个内置分类之一,或在 scenes 表里存在。
func isValidScene(scene string) bool {
	// 内置 5 个直接放行
	for _, s := range defaultScenes {
		if s["id"] == scene {
			return true
		}
	}
	// 查 scenes 表
	var n int
	store.DB.QueryRow("SELECT COUNT(*) FROM scenes WHERE id = ?", scene).Scan(&n)
	return n > 0
}
