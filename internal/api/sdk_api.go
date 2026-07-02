package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// sdkDocs GET /api/sdk/docs
// 返回 SDK 接入文档的索引，供 Agent 开发者快速了解平台 API。
func sdkDocs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"sdk_version": "v1.0",
		"protocol":    "REST + MCP(Model Context Protocol)",
		"description": "AI Eval Platform 标准 Agent 接入 SDK。Agent 实现 SDK 接口后即可被平台发现、注册、评测。",
		"endpoints": []gin.H{
			{
				"method": "POST",
				"path":   "/api/agents",
				"description": "注册 Agent。Agent 首次接入时调用此接口在平台创建 Agent 记录。",
				"request_body": `{"name":"My Agent","model":"glm-4","adapter":"api","endpoint":"http://.../eval"}`,
				"response":     `{"id":"agent-uuid","name":"My Agent","online":true}`,
			},
			{
				"method": "POST",
				"path":   "/api/tasks",
				"description": "创建评测任务。平台自动调度引擎执行完整的评测管线。",
				"request_body": `{"name":"评测任务","scene":"agent","agent_id":"<id>","rule_set":"{\"rule_set_id\":\"rs-fullstack\"}"}`,
				"response":     `{"id":"task-uuid","name":"评测任务","status":"pending"}`,
			},
			{
				"method": "GET",
				"path":   "/api/tasks/:id",
				"description": "查询任务状态和评测报告。status=done 时 result 字段包含完整报告。",
				"response": `{"id":"...","status":"done","result":{"overall":82.5,"dimensions":{...}}}`,
			},
			{
				"method": "GET",
				"path":   "/api/questions",
				"description": "获取可用题库列表，支持 ?scene=agent 参数筛选。",
			},
			{
				"method": "GET",
				"path":   "/api/rule-sets",
				"description": "获取可用规则集列表，含规则数量和适用场景。",
			},
			{
				"method": "POST",
				"path":   "/api/tasks/:id/chaos",
				"description": "对指定任务执行混沌工程对抗测试(需任务已存在且未完成)。",
			},
			{
				"method": "POST",
				"path":   "/api/clone-test",
				"description": "克隆 Agent 并换模型做 A/B 对比测试。",
				"request_body": `{"source_agent_id":"<id>","target_models":["gpt-4o","deepseek-chat"],"question_count":5}`,
			},
		},
		"mcp_protocol": gin.H{
			"description": "Agent 可通过暴露 /eval HTTP 端点接入平台 MCP 评测协议",
			"endpoint":    "POST /eval",
			"request":     `{"prompt":"string","context":{},"max_tokens":2048,"temperature":0.2}`,
			"response":    `{"text":"string","token_in":128,"token_out":256,"model":"glm-4","latency_ms":1500}`,
		},
		"quick_start": `# 三步接入
1. POST /api/agents → 获取 agent_id
2. POST /api/tasks  → 创建评测任务(引用 agent_id)
3. GET /api/tasks/:id → 轮询等待 status=done 获取评测报告`,
		"more_docs": "查看 internal/sdk/README.md 获取完整 SDK 文档和 Go 代码示例",
	})
}
