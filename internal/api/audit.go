package api

// audit.go — 白盒调用审计日志的 API handler。
//
// 两个端点:
//   GET /api/tasks/:id/audit   —— 某次评测任务的全部审计记录(可选 ?kind=llm|tool|event 过滤)
//   GET /api/audit/sessions     —— 所有有审计记录的会话列表(独立审计页左侧用)

import (
	"net/http"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// listTaskAudit GET /api/tasks/:id/audit?kind=llm|tool|event
// 返回该任务的全部审计日志,按 seq 升序。
func listTaskAudit(c *gin.Context) {
	taskID := c.Param("id")
	kind := c.Query("kind") // 可选过滤
	logs, err := store.ListAuditLogs(taskID, kind)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logs)
}

// listAuditSessions GET /api/audit/sessions
// 返回所有有审计日志的会话(去重),供独立审计页左侧列表。
func listAuditSessions(c *gin.Context) {
	sessions, err := store.ListAuditSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessions)
}
