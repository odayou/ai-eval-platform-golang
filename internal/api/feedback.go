package api

// feedback.go — 人工协同与系统自进化闭环(MRD 7.3 / 4.3)。
//
// 四步闭环:
//   1. 人工纠偏 — 踩/赞 + 批注(针对圆桌结果)
//   2. Bad Case 负样本库沉淀 — 被踩 Case 向量化入库
//   3. 规则反哺 — 人类批注触发规则更新建议
//   4. 数据积累 — 为后续 RLHF 做数据储备
//
// API:
//   POST /api/feedback/bad-cases/:id  — 对 Bad Case 踩/赞+批注
//   POST /api/feedback/tasks/:id      — 对任务整体踩/赞
//   GET  /api/feedback/suggestions     — 获取规则更新建议
//   POST /api/feedback/suggestions/:id/apply — 采纳建议→写入规则库

import (
	"net/http"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// feedbackBadCase POST /api/feedback/bad-cases/:id
// Body: {"action": "thumbs_down", "note": "这段代码圆桌说没问题,但实际上违反了团队内部的设计模式"}
func feedbackBadCase(c *gin.Context) {
	badCaseID := c.Param("id")
	var req struct {
		Action string `json:"action" binding:"required"` // thumbs_up | thumbs_down
		Note   string `json:"note"`                      // 批注
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Action != "thumbs_up" && req.Action != "thumbs_down" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be thumbs_up or thumbs_down"})
		return
	}

	// 更新 Bad Case 的 feedback_type 和 note
	res, err := store.DB.Exec(`UPDATE bad_cases SET feedback_type = ?, note = ?, status = 'reviewed' WHERE id = ?`,
		req.Action, req.Note, badCaseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "bad case not found"})
		return
	}

	// 如果是踩,自动生成规则更新建议
	if req.Action == "thumbs_down" && req.Note != "" {
		createRuleSuggestion(badCaseID, req.Note)
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "action": req.Action})
}

// feedbackTask POST /api/feedback/tasks/:id
// Body: {"action": "thumbs_up", "note": "评测结果准确"}
func feedbackTask(c *gin.Context) {
	taskID := c.Param("id")
	var req struct {
		Action string `json:"action" binding:"required"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 存入 task_feedback 表(或直接更新 tasks 的备注)
	store.DB.Exec(`INSERT INTO task_feedback (id, task_id, action, note, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		uuid.New().String(), taskID, req.Action, req.Note)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// listSuggestions GET /api/feedback/suggestions
// 获取待处理的规则更新建议。
func listSuggestions(c *gin.Context) {
	rows, err := store.DB.Query(`SELECT id, bad_case_id, suggestion, status, created_at
		FROM rule_suggestions WHERE status = 'pending' ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var id, badCaseID, suggestion, status, createdAt string
		rows.Scan(&id, &badCaseID, &suggestion, &status, &createdAt)
		items = append(items, map[string]interface{}{
			"id": id, "bad_case_id": badCaseID, "suggestion": suggestion,
			"status": status, "created_at": createdAt,
		})
	}
	if items == nil {
		items = []map[string]interface{}{}
	}
	c.JSON(http.StatusOK, items)
}

// applySuggestion POST /api/feedback/suggestions/:id/apply
// 采纳规则建议→写入规则库。
func applySuggestion(c *gin.Context) {
	suggestionID := c.Param("id")

	// 读建议
	var suggestion, badCaseID string
	err := store.DB.QueryRow(`SELECT suggestion, bad_case_id FROM rule_suggestions WHERE id = ?`,
		suggestionID).Scan(&suggestion, &badCaseID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "suggestion not found"})
		return
	}

	// 从 Bad Case 中提取信息
	var questionID, answer string
	store.DB.QueryRow(`SELECT question_id, answer FROM bad_cases WHERE id = ?`,
		badCaseID).Scan(&questionID, &answer)

	// 创建新规则
	ruleID := uuid.New().String()
	store.InsertRule(store.RuleCreateParams{
		ID: ruleID, Name: "人工反馈规则-" + ruleID[:8], Dimension: "correctness",
		Scoring: "llm", Weight: 1.0, Content: suggestion,
		Tags: `["human_feedback"]`, CreatedAt: time.Now().Format(time.RFC3339),
	})

	// 标记建议已采纳
	store.DB.Exec(`UPDATE rule_suggestions SET status = 'applied' WHERE id = ?`, suggestionID)

	c.JSON(http.StatusOK, gin.H{"rule_id": ruleID, "status": "applied"})
}

	// createRuleSuggestion 从人工批注中生成规则更新建议。
	func createRuleSuggestion(badCaseID, note string) {
		suggestion := "基于人工反馈: " + note
		store.DB.Exec(`INSERT INTO rule_suggestions (id, bad_case_id, suggestion, status, created_at)
			VALUES (?, ?, ?, 'pending', CURRENT_TIMESTAMP)`,
			uuid.New().String(), badCaseID, suggestion)
	}

	// rlhfStats GET /api/feedback/rlhf-stats
	// 返回 RLHF 数据积累统计(MRD 7.3 第四步)。
	// 用于展示"数据准备"进度:踩赞数、可导出样本数、标注覆盖率。
	func rlhfStats(c *gin.Context) {
		var totalBadCases, reviewedCases, thumbsDown, thumbsUp int
		store.DB.QueryRow("SELECT COUNT(*) FROM bad_cases").Scan(&totalBadCases)
		store.DB.QueryRow("SELECT COUNT(*) FROM bad_cases WHERE status = 'reviewed' OR status = 'analyzed'").Scan(&reviewedCases)
		store.DB.QueryRow("SELECT COUNT(*) FROM bad_cases WHERE feedback_type = 'thumbs_down'").Scan(&thumbsDown)
		store.DB.QueryRow("SELECT COUNT(*) FROM bad_cases WHERE feedback_type = 'thumbs_up'").Scan(&thumbsUp)

		var totalSuggestions, appliedSuggestions int
		store.DB.QueryRow("SELECT COUNT(*) FROM rule_suggestions").Scan(&totalSuggestions)
		store.DB.QueryRow("SELECT COUNT(*) FROM rule_suggestions WHERE status = 'applied'").Scan(&appliedSuggestions)

		coverage := 0.0
		if totalBadCases > 0 {
			coverage = float64(reviewedCases) / float64(totalBadCases) * 100
		}

		c.JSON(http.StatusOK, gin.H{
			"total_bad_cases":       totalBadCases,
			"reviewed_cases":        reviewedCases,
			"thumbs_down":           thumbsDown,
			"thumbs_up":             thumbsUp,
			"review_coverage_pct":   round1(coverage),
			"total_suggestions":     totalSuggestions,
			"applied_suggestions":   appliedSuggestions,
			"ready_for_rlhf":        reviewedCases >= 50, // 50 个标注即可开始 RLHF
			"estimated_data_size":   reviewedCases * 2,   // 每条 ~2KB(问题+回答+标注)
		})
	}

	// exportRLHFData GET /api/feedback/rlhf-export
	// 导出 RLHF 训练数据为 JSONL 格式(每行一个标注样本)。
	// 格式: {"prompt": "...", "answer": "...", "score": 0.0, "feedback": "thumbs_down", "note": "..."}
	// 可直接用于模型微调(如 OpenAI Fine-tuning API、DPO 训练)。
	func exportRLHFData(c *gin.Context) {
		rows, err := store.DB.Query(`SELECT bc.id, q.prompt, bc.answer, bc.score, bc.feedback_type, bc.note
			FROM bad_cases bc
			LEFT JOIN questions q ON bc.question_id = q.id
			WHERE bc.status IN ('reviewed', 'analyzed')
			ORDER BY bc.created_at DESC`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()

		var samples []map[string]interface{}
		for rows.Next() {
			var id, answer, feedback, note string
			var prompt *string
			var score float64
			rows.Scan(&id, &prompt, &answer, &score, &feedback, &note)

			promptStr := ""
			if prompt != nil {
				promptStr = *prompt
			}

			// 构建 RLHF 标注样本
			chosen := answer
			rejected := ""
			if feedback == "thumbs_down" {
				rejected = answer
				chosen = "(人工标注: 该回答不符合预期, 期望质量更高的回答)"
			}

			samples = append(samples, map[string]interface{}{
				"id":       id,
				"prompt":   promptStr,
				"chosen":   chosen,
				"rejected": rejected,
				"score":    score,
				"feedback": feedback,
				"note":     note,
			})
		}

		if samples == nil {
			samples = []map[string]interface{}{}
		}

		c.JSON(http.StatusOK, gin.H{
			"format":   "rlhf-preference-pairs",
			"count":    len(samples),
			"samples":  samples,
			"usage":    "可用于 DPO(直接偏好优化)训练 或 Reward Model 训练",
			"exported_at": gin.H{"$time": "now"},
		})
	}

	// round1 保留 1 位小数(复用 engine 的,这里内联)。
	func round1(f float64) float64 {
		return float64(int(f*10+0.5)) / 10
	}
