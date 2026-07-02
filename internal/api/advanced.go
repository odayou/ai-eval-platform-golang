package api

// advanced.go — 高级评测功能 API(混沌/克隆/归因)。
//
// 这些端点对应 MRD 的差异化壁垒(④),是平台与竞品拉开身位的核心能力。

import (
	"net/http"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// ── 混沌工程 ──

// runChaosTest POST /api/tasks/:id/chaos
// 对一个已完成的任务重新跑混沌注入测试。
func runChaosTest(c *gin.Context) {
	taskID := c.Param("id")
	task, err := store.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// 读 Agent
	var agent store.AgentRow
	if task.AgentID != "" {
		agentPtr, err := store.GetAgent(task.AgentID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "agent not found"})
			return
		}
		agent = *agentPtr
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task has no agent"})
		return
	}

	// 读题目(取前 3 道做混沌测试)
	questions, _ := store.GetQuestionsByScene(task.Scene)
	if len(questions) > 3 {
		questions = questions[:3]
	}

	config := engine.DefaultChaosConfig()
	var allResults []engine.ChaosResult
	for _, q := range questions {
		results := engine.RunChaos(agent, q.Prompt, config, taskID)
		allResults = append(allResults, results...)
	}

	c.JSON(http.StatusOK, gin.H{
		"task_id":    taskID,
		"chaos_count": len(allResults),
		"results":    allResults,
	})
}

// ── 克隆对比 ──

// runCloneTest POST /api/clone-test
// Body: {"source_agent_id":"a-codegen", "target_models":["deepseek-chat","glm-4-flash"], "rule_set_id":"rs-fullstack"}
func runCloneTest(c *gin.Context) {
	var req engine.CloneConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.TargetModels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target_models is required"})
		return
	}

	result, err := engine.RunCloneTest(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// ── 异常归因 ──

// runAttribution POST /api/bad-cases/:id/attribute
// 对单个 Bad Case 执行归因分析。
func runAttribution(c *gin.Context) {
	badCaseID := c.Param("id")
	attribution, err := engine.RunAttribution(badCaseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, attribution)
}

	// runBatchAttribution POST /api/attributions/batch
	// Body: {"task_id":"xxx"} (可选,不传则处理所有 pending)
	func runBatchAttribution(c *gin.Context) {
		var req struct {
			TaskID string `json:"task_id"`
		}
		c.ShouldBindJSON(&req)

		count := engine.RunBatchAttribution(req.TaskID)
		c.JSON(http.StatusOK, gin.H{"processed": count})
	}

	// ── Prompt 智能平移适配器(MRD 8.5)──

	// adaptPrompt POST /api/prompt/adapt
	// Body: {"source_prompt":"...", "source_model":"deepseek-chat", "target_model":"gpt-4o"}
	// 返回适配建议 + 适配后 Prompt 预览。
	func adaptPrompt(c *gin.Context) {
		var req engine.AdaptationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.SourcePrompt == "" || req.SourceModel == "" || req.TargetModel == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source_prompt, source_model, target_model 都是必填"})
			return
		}

		result, err := engine.AdaptPromptWithLLM(req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, result)
	}

	// listModelProfiles GET /api/prompt/profiles
	// 获取所有已知模型的 Prompt 偏好配置。
	func listModelProfiles(c *gin.Context) {
		profiles := engine.GetAllModelProfiles()
		c.JSON(http.StatusOK, profiles)
	}
