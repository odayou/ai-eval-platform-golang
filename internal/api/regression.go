package api

// regression.go — 回归对比 API(MRD 9.4)。
//
// 提供两种对比模式:
//   1. 基于已有 task 对比(两个已完成的任务跑回归 diff)
//   2. Agent 级对比(基线 Agent vs 目标模型,自动创建任务跑对比)

import (
	"net/http"

	"ai-eval-platform/internal/engine"

	"github.com/gin-gonic/gin"
)

// compareTasks POST /api/regression/compare
// Body: {"base_task_id":"xxx", "target_task_id":"yyy"}
// 两个已完成的任务跑回归 diff,即时返回(无需执行新任务)。
func compareTasks(c *gin.Context) {
	var req engine.RegressionConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.BaseTaskID == "" || req.TargetTaskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_task_id 和 target_task_id 都是必填"})
		return
	}

	result, err := engine.RunRegression(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// compareAgents POST /api/regression/agent-compare
// Body: {"base_agent_id":"xxx", "target_models":["glm-4","deepseek-chat"], "rule_set_id":"rs-fullstack", "question_count":5}
// 基线 Agent vs 目标模型,自动创建任务跑对比。
func compareAgents(c *gin.Context) {
	var req engine.AgentRegressionConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.BaseAgentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_agent_id 是必填"})
		return
	}
	if len(req.TargetModels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target_models 至少需要一个"})
		return
	}
	if req.Scene == "" {
		req.Scene = "agent"
	}

	results, err := engine.RunAgentRegression(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"base_agent": req.BaseAgentID,
		"comparisons": len(results),
		"results":     results,
	})
}
