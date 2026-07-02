package api

// baseline.go — 企业定制基线测试集管理 API(MRD v2 7.1)。
//
// 端点:
//   POST   /api/questions/import-baseline   — 模拟从 GitLab 导入基线题
//   GET    /api/question-sets              — 列出测试卷
//   POST   /api/question-sets              — 创建测试卷
//   DELETE /api/question-sets/:id          — 删除测试卷
//   GET    /api/question-sets/:id/questions — 获取测试卷所含题目的详情列表

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// importBaseline POST /api/questions/import-baseline
// 模拟从 GitLab 内部代码库抽样导入基线题。
// Body: {"repo": "tiktok-core", "count": 3, "scene": "agent"}
func importBaseline(c *gin.Context) {
	var req struct {
		Repo  string `json:"repo"`  // 仓库名(如 tiktok-core)
		Count int    `json:"count"` // 导入几道
		Scene string `json:"scene"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if req.Scene == "" {
		req.Scene = "agent"
	}
	if req.Repo == "" {
		req.Repo = "internal-repo"
	}
	if req.Count <= 0 {
		req.Count = 3
	}

	// 基线题库模板(模拟从 GitLab 抽样的代码片段)
	templates := []struct {
		title, prompt, expected, difficulty string
		tags                                []string
	}{
		{"[基线] 分布式锁实现", "实现一个基于 Redis 的分布式锁,要求支持自动续期和可重入。",
			"应使用 SETNX + Lua 脚本保证原子性", "high", []string{"Redis", "分布式锁", "Kitex"}},
		{"[基线] 服务熔断器", "实现一个服务熔断器,支持半开状态探测和自动恢复。",
			"熔断器应有 closed/open/half-open 三态", "high", []string{"熔断", "RPC", "Kitex"}},
		{"[基线] 消息队列消费者幂等", "设计一个消息队列消费者的幂等方案,防止重复消费。",
			"应使用消息 ID + Redis 去重", "mid", []string{"消息队列", "幂等", "Redis"}},
		{"[基线] Service Mesh 超时传播", "在 Service Mesh 中实现请求超时的跨服务传播。",
			"应在 HTTP Header 中传递 deadline", "high", []string{"Service Mesh", "超时", "RPC"}},
		{"[基线] 配置热更新", "实现一个支持配置热更新的配置中心客户端。",
			"应监听 etcd/consul 变更事件并原子替换", "mid", []string{"配置中心", "热更新"}},
	}

	imported := 0
	for i := 0; i < req.Count && i < len(templates); i++ {
		t := templates[i]
		id := uuid.New().String()
		tagsJSON, _ := json.Marshal(t.tags)
		url := fmt.Sprintf("https://gitlab.internal/%s/-/blob/main/%s.go#L%d", req.Repo, t.tags[0], 42*i+1)
		now := time.Now().Format(time.RFC3339)
		_, err := store.DB.Exec(`INSERT INTO questions (id, scene, title, prompt, expected, difficulty, source, tags, source_repo, source_url, desensitized, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 'baseline', ?, ?, ?, 1, ?)`,
			id, req.Scene, t.title, t.prompt, t.expected, t.difficulty, string(tagsJSON), req.Repo, url, now)
		if err == nil {
			imported++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"imported": imported,
		"repo":     req.Repo,
		"message":  fmt.Sprintf("已从 %s 导入 %d 道基线题(已自动脱敏)", req.Repo, imported),
	})
}

// ── Question Sets(测试卷)CRUD ──

type questionSet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scene       string `json:"scene"`
	QuestionIDs string `json:"question_ids"` // JSON array
	Tags        string `json:"tags"`
	CreatedAt   string `json:"created_at"`
}

func listQuestionSets(c *gin.Context) {
	rows, err := store.DB.Query(`SELECT id, name, COALESCE(description,''), scene, COALESCE(question_ids,''), COALESCE(tags,''), created_at
		FROM question_sets ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []questionSet
	for rows.Next() {
		var qs questionSet
		rows.Scan(&qs.ID, &qs.Name, &qs.Description, &qs.Scene, &qs.QuestionIDs, &qs.Tags, &qs.CreatedAt)
		items = append(items, qs)
	}
	if items == nil {
		items = []questionSet{}
	}
	c.JSON(http.StatusOK, items)
}

func createQuestionSet(c *gin.Context) {
	var req struct {
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Scene       string   `json:"scene"`
		QuestionIDs []string `json:"question_ids"`
		Tags        []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	qids, _ := json.Marshal(req.QuestionIDs)
	tags, _ := json.Marshal(req.Tags)
	store.DB.Exec(`INSERT INTO question_sets (id, name, description, scene, question_ids, tags, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		id, req.Name, req.Description, req.Scene, string(qids), string(tags))
	c.JSON(http.StatusCreated, questionSet{ID: id, Name: req.Name, Description: req.Description, Scene: req.Scene, QuestionIDs: string(qids), Tags: string(tags)})
}

func deleteQuestionSet(c *gin.Context) {
	id := c.Param("id")
	store.DB.Exec("DELETE FROM question_sets WHERE id = ?", id)
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// getQuestionSetQuestions GET /api/question-sets/:id/questions
// 返回该测试卷中包含的题目的完整详情列表
func getQuestionSetQuestions(c *gin.Context) {
	id := c.Param("id")
	var qidsJSON string
	err := store.DB.QueryRow("SELECT COALESCE(question_ids,'') FROM question_sets WHERE id = ?", id).Scan(&qidsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "测试卷不存在"})
		return
	}
	var qids []string
	json.Unmarshal([]byte(qidsJSON), &qids)
	if len(qids) == 0 {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	// 用原生查询拉题目详情
	placeholders := ""
	args := make([]interface{}, len(qids))
	for i, qid := range qids {
		if i > 0 { placeholders += "," }
		placeholders += "?"
		args[i] = qid
	}
	query := fmt.Sprintf(`SELECT id, scene, title, prompt, COALESCE(expected,''), difficulty, source, COALESCE(tags,''), COALESCE(source_repo,''), COALESCE(source_url,''), COALESCE(desensitized,0), created_at FROM questions WHERE id IN (%s)`, placeholders)
	rows, err := store.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type qDetail struct {
		ID, Scene, Title, Prompt, Expected, Difficulty, Source, Tags, SourceRepo, SourceURL, CreatedAt string
		Desensitized int
	}
	var items []qDetail
	for rows.Next() {
		var q qDetail
		rows.Scan(&q.ID, &q.Scene, &q.Title, &q.Prompt, &q.Expected, &q.Difficulty, &q.Source, &q.Tags, &q.SourceRepo, &q.SourceURL, &q.Desensitized, &q.CreatedAt)
		items = append(items, q)
	}
	if items == nil { items = []qDetail{} }
	c.JSON(http.StatusOK, items)
}
