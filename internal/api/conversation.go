package api

// conversation.go — 评测会话查询接口(供运行 Tab 展示 Agent 对话轨迹)。
//
// conversations 表由引擎 ② 写入,记录被测 Agent 每轮的 user/assistant 消息。
// 前端"运行"Tab 通过本接口回放评测过程(后续 ⑤ 接 WebSocket 做实时推送)。

import (
	"net/http"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// Conversation 对应 conversations 表,展示一轮评测对话。
type Conversation struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Turn      int    `json:"turn"`
	Messages  any    `json:"messages"`    // 已解析的 JSON 数组 [{role,content}]
	TokenIn   int    `json:"token_in"`
	TokenOut  int    `json:"token_out"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// listConversations GET /api/tasks/:id/conversations
// 返回该任务下的所有评测轮次(按 turn 升序)。
func listConversations(c *gin.Context) {
	taskID := c.Param("id")
	rows, err := store.DB.Query(`SELECT id, task_id, turn, messages, token_in, token_out, status, created_at
		FROM conversations WHERE task_id = ? ORDER BY turn ASC`, taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []Conversation{}
	for rows.Next() {
		var conv Conversation
		var msgsJSON string
		if err := rows.Scan(&conv.ID, &conv.TaskID, &conv.Turn, &msgsJSON,
			&conv.TokenIn, &conv.TokenOut, &conv.Status, &conv.CreatedAt); err != nil {
			continue
		}
		// 把 messages JSON 字符串解析为数组传给前端(避免前端二次 parse)
		conv.Messages = parseMessagesArray(msgsJSON)
		items = append(items, conv)
	}
	c.JSON(http.StatusOK, items)
}

// listTaskAttributions GET /api/tasks/:id/attributions
// 返回该任务的异常归因报告(RESTful 别名,等同 /api/attributions?task_id=xxx)。
func listTaskAttributions(c *gin.Context) {
	taskID := c.Param("id")
	rows, err := store.DB.Query(`SELECT id, task_id, root_cause, analysis, COALESCE(suggested_fix,''),
		applied, created_at FROM attributions WHERE task_id = ? ORDER BY created_at DESC`, taskID)
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
	if items == nil {
		items = []Attribution{}
	}
	c.JSON(http.StatusOK, items)
}
