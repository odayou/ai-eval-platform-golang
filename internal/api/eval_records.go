package api

// eval_records.go — 评测产出的只读查询(Bad Case + 异常归因)。
//
// 这些记录由 ②引擎 和 ④.3 归因引擎 写入,①阶段先提供查询接口供前端展示。
// 不在此处提供写入端点 —— 写入逻辑归引擎统一控制,避免脏数据。

import (
	"encoding/json"
	"net/http"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// BadCase 对应 bad_cases 表,展示低分/被踩的回答样本。
type BadCase struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"task_id"`
	QuestionID   string  `json:"question_id"`
	AgentID      string  `json:"agent_id"`
	Answer       string  `json:"answer"`
	Score        float64 `json:"score"`
	FeedbackType string  `json:"feedback_type"` // auto_low_score | thumbs_down
	Note         string  `json:"note"`
	Status       string  `json:"status"` // pending | precipitated | ignored
	CreatedAt    string  `json:"created_at"`
}

// Attribution 对应 attributions 表,记录失败任务的根因分析。
type Attribution struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	RootCause    string `json:"root_cause"` // prompt_compliance | context_loss | tool_failure | model_limit
	Analysis     string `json:"analysis"`
	SuggestedFix string `json:"suggested_fix"`
	Applied      bool   `json:"applied"`
	CreatedAt    string `json:"created_at"`
}

// listBadCases GET /api/bad-cases
// 支持 status 筛选(pending/precipitated/ignored)与 limit(默认 50)。
func listBadCases(c *gin.Context) {
	q := `SELECT id, task_id, question_id, COALESCE(agent_id,''), answer, COALESCE(score,0),
	      feedback_type, COALESCE(note,''), status, created_at FROM bad_cases`
	var args []any
	if s := c.Query("status"); s != "" {
		q += " WHERE status = ?"
		args = append(args, s)
	}
	q += " ORDER BY created_at DESC"
	if limit := c.Query("limit"); limit != "" {
		q += " LIMIT ?"
		args = append(args, limit)
	} else {
		q += " LIMIT 50"
	}

	rows, err := store.DB.Query(q, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []BadCase{}
	for rows.Next() {
		var b BadCase
		if err := rows.Scan(&b.ID, &b.TaskID, &b.QuestionID, &b.AgentID, &b.Answer,
			&b.Score, &b.FeedbackType, &b.Note, &b.Status, &b.CreatedAt); err != nil {
			continue
		}
		items = append(items, b)
	}
	c.JSON(http.StatusOK, items)
}

// listAttributions GET /api/attributions
// 按 task_id 过滤,展示该任务的归因报告。
func listAttributions(c *gin.Context) {
	q := `SELECT id, task_id, root_cause, analysis, COALESCE(suggested_fix,''),
	      applied, created_at FROM attributions`
	var args []any
	if tid := c.Query("task_id"); tid != "" {
		q += " WHERE task_id = ?"
		args = append(args, tid)
	}
	q += " ORDER BY created_at DESC"

	rows, err := store.DB.Query(q, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []Attribution{}
	for rows.Next() {
		var a Attribution
		var applied int
		if err := rows.Scan(&a.ID, &a.TaskID, &a.RootCause, &a.Analysis,
			&a.SuggestedFix, &applied, &a.CreatedAt); err != nil {
			continue
		}
		a.Applied = applied == 1
		items = append(items, a)
	}
	c.JSON(http.StatusOK, items)
}

// parseMessagesArray 把 conversations.messages 的 JSON 字符串解析为数组。
// 解析失败时返回原始字符串(前端容错展示)。
func parseMessagesArray(msgsJSON string) any {
	var arr []map[string]string
	if err := json.Unmarshal([]byte(msgsJSON), &arr); err != nil {
		return msgsJSON
	}
	return arr
}
