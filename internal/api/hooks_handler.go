package api

// hooks_handler.go — Agent Hooks 的 API handler(harness 第5层)。

import (
	"net/http"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// listAgentHooks GET /api/agents/:id/hooks
func listAgentHooks(c *gin.Context) {
	agentID := c.Param("id")
	hooks, err := store.GetAgentHooks(agentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if hooks == nil {
		hooks = []store.HookRow{}
	}
	c.JSON(http.StatusOK, hooks)
}

// createAgentHook POST /api/agents/:id/hooks
// Body: {event, matcher, command}
func createAgentHook(c *gin.Context) {
	agentID := c.Param("id")
	var req struct {
		Event   string `json:"event" binding:"required"`
		Matcher string `json:"matcher"`
		Command string `json:"command" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Matcher == "" {
		req.Matcher = "*"
	}
	id := "hook-" + uuid.New().String()[:8]
	if err := store.InsertAgentHook(id, agentID, req.Event, req.Matcher, req.Command); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "event": req.Event, "matcher": req.Matcher, "command": req.Command})
}

// deleteAgentHook DELETE /api/agents/:id/hooks/:hid
func deleteAgentHook(c *gin.Context) {
	hid := c.Param("hid")
	_, err := store.DB.Exec(`DELETE FROM agent_hooks WHERE id = ?`, hid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": hid})
}
