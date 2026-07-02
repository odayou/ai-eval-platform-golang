package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ai-eval-platform/internal/engine"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NewRouter creates and configures the Gin engine with all routes.
// RouterOptions 是 NewRouter 的可选项。零值字段会回退到安全的默认值。
type RouterOptions struct {
	// CORSOrigins 允许的前端来源。nil/空 → 用本地开发默认值。
	CORSOrigins []string

	// RateLimit 每秒每 IP 最大请求数;0 表示关闭限流(仅内网/调试建议)。
	// 默认 10(正常用户够用,脚本灌队列会被拦)。
	RateLimit float64

	// RateBurst 限流突发容量上限。默认 20。
	RateBurst float64
}

// NewRouter creates and configures the Gin engine with all routes.
// 可选传入 RouterOptions 覆盖默认 CORS 来源等;不传则用本地开发默认值(向后兼容)。
func NewRouter(opts ...RouterOptions) *gin.Engine {
	opt := RouterOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}
	origins := opt.CORSOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	}

	r := gin.Default()

	// Request ID middleware
	r.Use(requestIDMiddleware())

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", "X-Auth-Token"},
		AllowCredentials: true,
	}))

	// 限流:RateLimit>0 时启用 per-IP 令牌桶,防恶意高频请求灌满队列/白嫖 LLM。
	if opt.RateLimit > 0 {
		burst := opt.RateBurst
		if burst <= 0 {
			burst = 20
		}
		r.Use(rateLimitMiddleware(opt.RateLimit, burst))
	}

	// Auth 中间件:可选 Token 鉴权(写操作保护)。
	// 设置环境变量 EVAL_AUTH_TOKEN 后,POST/PUT/DELETE 请求需带 Authorization: Bearer <token> 或 X-Auth-Token。
	// 未设置环境变量时维持本地放行(向后兼容,不破坏现有使用)。
	r.Use(authMiddleware())

		// Health check
		r.GET("/api/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"status":  "ok",
				"service": "ai-eval-platform",
				"version": "v0.2.0",
				"sdk":     "v1.0",
				"sdk_endpoints": []string{
					"POST /api/agents — 注册 Agent(SDK)",
					"POST /api/tasks — 提交评测任务(SDK)",
					"GET  /api/tasks/:id — 查询评测结果(SDK)",
					"GET  /api/sdk/docs — SDK 文档",
				},
			})
		})

	// ── Resource management (七库) ──
	api := r.Group("/api")
	{
		// Agents
		api.GET("/agents", listAgents)
		api.POST("/agents", createAgent)
		api.GET("/agents/:id", getAgent)
		api.PUT("/agents/:id", updateAgent)
		api.PUT("/agents/:id/online", toggleAgentOnline)
		api.DELETE("/agents/:id", deleteAgent)
		// AI 创造 Agent(快速/详细模式 + 实例化 + 状态)
		api.POST("/agents/ai-create-quick", aiCreateAgentQuick)
		api.POST("/agents/ai-create-detail", aiCreateAgentDetail)
		api.POST("/agent-definitions/:id/instantiate", instantiateAgent)
		api.GET("/agents/:id/status", getAgentStatus)
		api.POST("/websearch/test", testWebSearch)
		// Agent Hooks(harness 第5层 生命周期钩子)
		api.GET("/agents/:id/hooks", listAgentHooks)
		api.POST("/agents/:id/hooks", createAgentHook)
		api.DELETE("/agents/:id/hooks/:hid", deleteAgentHook)
		api.POST("/agents/:id/debug", debugAgent)

		// ════════ 六大拓展方向(2026-06-23) ════════
		// 方向1: Agent CI/CD Pipeline
		api.GET("/pipelines", listPipelines)
		api.POST("/pipelines", createPipeline)
		api.POST("/pipelines/:id/trigger", triggerPipeline)
		api.GET("/pipelines/:id/runs", listPipelineRuns)

		// 方向2: Agent Arena
		api.GET("/arena/leaderboard", listArenaLeaderboard)
		api.POST("/arena/submit", submitToArena)

		// 方向3: Red Team
		api.POST("/redteam/scan", startRedTeamScan)
		api.GET("/redteam/scans/:id", getRedTeamScan)
		api.GET("/redteam/scans/:id/findings", listRedTeamFindings)

		// 方向4: Observability
		api.POST("/observe/event", reportObservabilityEvent)
		api.GET("/observe/dashboard", getObservabilityDashboard)
		api.POST("/observe/alerts", createAlertRule)

		// 方向5: Fine-tuning
		api.POST("/finetune/export", exportTrainingDataset)
		api.GET("/finetune/datasets", listTrainingDatasets)

		// 方向6: Orchestration
		api.GET("/workflows", listWorkflows)
		api.POST("/workflows", createWorkflow)
		api.POST("/workflows/:id/run", runWorkflow)
		api.GET("/workflows/runs/:id", getWorkflowStatus)

		// #40 stats-cache 8 维度统计 API
		api.GET("/stats/models", func(c *gin.Context) {
			c.JSON(http.StatusOK, engine.GetStatsSummary())
		})
		api.GET("/stats/dual-token", func(c *gin.Context) {
			c.JSON(http.StatusOK, engine.GetDualTokenTracker().Summary())
		})

		// Expert Groups(可配置专家组)
		api.GET("/expert-groups", listExpertGroups)
		api.POST("/expert-groups", createExpertGroup)
		api.POST("/expert-groups/ai-generate", aiGenerateExpertGroup)
		api.GET("/expert-groups/:id", getExpertGroup)
		api.PUT("/expert-groups/:id", updateExpertGroup)
		api.DELETE("/expert-groups/:id", deleteExpertGroup)

		// Models
		api.GET("/models", listModels)
		api.POST("/models", createModel)
		api.GET("/models/:id", getModel)
		api.PUT("/models/:id", updateModel)
		api.DELETE("/models/:id", deleteModel)
		api.POST("/models/:id/debug", debugModel)

		// Tasks
		api.GET("/tasks", listTasks)
		api.POST("/tasks", createTask)
		api.GET("/tasks/:id", getTask)
		api.DELETE("/tasks/:id", deleteTask)
		api.POST("/tasks/:id/rerun", rerunTask)
		api.GET("/tasks/:id/export", exportTaskReport)
		api.GET("/tasks/:id/conversations", listConversations)
		api.GET("/tasks/:id/attributions", listTaskAttributions)
		api.GET("/tasks/:id/audit", listTaskAudit)

		// Rules
		api.GET("/rules", listRules)
		api.POST("/rules", createRule)
		api.PUT("/rules/:id", updateRule)
		api.DELETE("/rules/:id", deleteRule)

		// Rule Sets
		api.GET("/rule-sets", listRuleSets)
		api.POST("/rule-sets", createRuleSet)
		api.PUT("/rule-sets/:id", updateRuleSet)
		api.DELETE("/rule-sets/:id", deleteRuleSet)

		// Tools
		api.GET("/tools", listTools)
		api.POST("/tools", createTool)
		api.DELETE("/tools/:id", deleteTool)

		// MCP Servers
		api.GET("/mcp-servers", listMCPServers)
		api.POST("/mcp-servers", createMCPServer)
		api.DELETE("/mcp-servers/:id", deleteMCPServer)

		// Skills
		api.GET("/skills", listSkills)
		api.POST("/skills", createSkill)
		api.DELETE("/skills/:id", deleteSkill)

		// Model Config
		api.GET("/model-config", listModelConfig)
		api.PUT("/model-config", upsertModelConfig)

		// Prompt Templates
		api.GET("/prompt-templates", listPromptTemplates)
		api.POST("/prompt-templates", createPromptTemplate)
		api.PUT("/prompt-templates/:id", updatePromptTemplate)
		api.DELETE("/prompt-templates/:id", deletePromptTemplate)
		api.GET("/prompt-templates/:id/versions", listPromptTemplateVersions)
		api.POST("/prompt-templates/:id/rollback", rollbackPromptTemplate)

		// Questions(题库)— ②引擎的输入源
		api.GET("/questions", listQuestions)
		api.POST("/questions", createQuestion)
		api.GET("/questions/:id", getQuestion)
		api.PUT("/questions/:id", updateQuestion)
		api.DELETE("/questions/:id", deleteQuestion)
		api.POST("/questions/generate", generateQuestions) // 圆桌出题
		api.POST("/questions/:id/expand", expandQuestion)   // 动态扩题
		api.POST("/questions/import-baseline", importBaseline) // GitLab 导入基线题(V2 7.1)

		// Question Sets(测试卷,V2 7.1)
		api.GET("/question-sets", listQuestionSets)
		api.POST("/question-sets", createQuestionSet)
		api.DELETE("/question-sets/:id", deleteQuestionSet)
		api.GET("/question-sets/:id/questions", getQuestionSetQuestions)

		// Bad Cases(负样本库)— 只读,写入归引擎
		api.GET("/bad-cases", listBadCases)

		// Audit(白盒审计日志)— 完全可观测的调用链回放
		api.GET("/audit/sessions", listAuditSessions)

		// Attributions(异常归因)— 只读,写入归归因引擎
		api.GET("/attributions", listAttributions)

			// Stats(看板聚合统计)
			api.GET("/stats/dashboard", dashboardStats)
			api.GET("/stats/score-trend", scoreTrend)
			api.GET("/stats/leaderboard", leaderboard)

		// Settings(带脱敏 Key 的模型列表)
		api.GET("/settings/models", listModelsForSettings)
		api.POST("/settings/models/:id/probe", probeModel) // 连通性检测
		// 考官模型库(MRD 模型职责分离)
		api.GET("/settings/judge-models", listJudgeModels)
		api.PUT("/settings/judge-models", upsertJudgeModel)
		api.DELETE("/settings/judge-models/:role", deleteJudgeModel)
		api.POST("/settings/judge-models/:role/probe", probeJudgeModel)

		// ── 高级评测功能(④ 差异化壁垒) ──
		api.POST("/tasks/:id/chaos", runChaosTest)          // 混沌工程
		api.POST("/clone-test", runCloneTest)               // 克隆对比
		api.POST("/bad-cases/:id/attribute", runAttribution) // 单个归因
		api.POST("/attributions/batch", runBatchAttribution) // 批量归因

		// ── 人工协同(4.3 Human-in-the-Loop) ──
		api.POST("/feedback/bad-cases/:id", feedbackBadCase) // 踩/赞+批注
		api.POST("/feedback/tasks/:id", feedbackTask)         // 任务级反馈
		api.GET("/feedback/suggestions", listSuggestions)     // 规则更新建议
		api.POST("/feedback/suggestions/:id/apply", applySuggestion) // 采纳建议
		api.GET("/feedback/rlhf-stats", rlhfStats)                     // RLHF 数据统计(⑧.4)
		api.GET("/feedback/rlhf-export", exportRLHFData)               // 导出 RLHF 训练数据

		// ── 规则导入导出(4.4) ──
		api.GET("/rule-sets/:id/export", exportRuleSet) // 导出规则集
		api.POST("/rule-sets/import", importRuleSet)     // 导入规则集
		api.GET("/rules/export", exportRules)            // 导出全部规则
		api.POST("/rules/import", importRules)           // 导入规则
		api.GET("/rules/graph", rulesGraph)             // 规则知识图谱
		// 规则关系边(MRD §7.3 知识图谱)
		api.GET("/rules/relations", listRuleRelations)
		api.POST("/rules/relations", createRuleRelation)
		api.DELETE("/rules/relations/:id", deleteRuleRelation)

		// ── 定时任务(⑤ cron)──
		api.GET("/cron-jobs", listCronJobs)
		api.POST("/cron-jobs", createCronJob)
		api.DELETE("/cron-jobs/:id", deleteCronJob)
		api.PUT("/cron-jobs/:id/toggle", toggleCronJob)
		api.GET("/scheduler/stats", schedulerStats)

		// ── 自定义场景(⑥.2 自建场景Tab)──
		api.GET("/scenes", listScenes)
		api.POST("/scenes", createScene)
		api.PUT("/scenes/:id", updateScene)
		api.DELETE("/scenes/:id", deleteScene)

		// ── 终端轨道(⑥ 协议层)──
		api.POST("/terminal/discover", discoverTerminal) // 自动识别项目配置
		api.POST("/terminal/test", testTerminal)          // 执行一次终端评测

		// ── Prompt 智能平移适配器(⑧.5)──
		api.POST("/prompt/adapt", adaptPrompt)
		api.GET("/prompt/profiles", listModelProfiles)

			// ── 回归对比(⑨ 版本演进 Diff)──
			api.POST("/regression/compare", compareTasks)
			api.POST("/regression/agent-compare", compareAgents)

			// ── SDK 端点 ──
			api.GET("/sdk/docs", sdkDocs)
			api.GET("/sdk/profiles", listModelProfiles) // 复用 Prompt 适配器的模型配置

		// ── MITM 代理(⑥ 协议逆向轨道)──
		api.POST("/proxy/start", startProxy)
		api.POST("/proxy/stop", stopProxy)
		api.GET("/proxy/status", proxyStatus)
		api.GET("/proxy/sessions", proxySessions)
		api.GET("/proxy/sessions/:id", proxySessionDetail)
		api.POST("/proxy/sessions/:id/convert", convertSession)
		api.POST("/proxy/sessions/:id/replay", replaySession)
		// HTTPS MITM 代理
		api.POST("/proxy/tls/start", startTLSProxy)
		api.POST("/proxy/tls/stop", stopTLSProxy)
		api.GET("/proxy/tls/status", tlsProxyStatus)
		api.GET("/proxy/tls/sessions", tlsProxySessions)
		api.POST("/proxy/tls/generate-ca", tlsGenerateCA)
		api.GET("/proxy/tls/ca-status", tlsCAStatus)
		// 协议逆向:环境变量重定向(对标 claude-code-router)
		api.POST("/proxy/redirect-script", generateRedirectScript)
	}

	// WebSocket 端点(不在 /api 组,需走 ws:// 协议)
	r.GET("/api/tasks/:id/ws", handleTaskWS)

	// ── 前端静态文件服务 ──
	staticDir := findStaticDir()
	if staticDir != "" {
		r.Static("/assets", filepath.Join(staticDir, "assets"))
		r.StaticFile("/favicon.svg", filepath.Join(staticDir, "favicon.svg"))
		r.StaticFile("/icons.svg", filepath.Join(staticDir, "icons.svg"))
		r.NoRoute(func(c *gin.Context) {
			if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
				c.JSON(http.StatusNotFound, gin.H{"error": "endpoint not found"})
				return
			}
			c.File(filepath.Join(staticDir, "index.html"))
		})
	}

	return r
}

// findStaticDir 查找前端构建产物目录。
func findStaticDir() string {
	candidates := []string{"web/dist", "./web/dist", "../web/dist", "./dist"}
	for _, dir := range candidates {
		index := filepath.Join(dir, "index.html")
		if _, err := os.Stat(index); err == nil {
			return dir
		}
	}
	return ""
}

// requestIDMiddleware 为每个请求分配唯一追踪 ID。
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateShortID()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// authMiddleware 可选 Token 鉴权(写操作保护)。
//
// 部署模型:
//   - 本地开发(默认):不设 EVAL_AUTH_TOKEN → 所有请求放行,维持原有体验
//   - 对外暴露:设 EVAL_AUTH_TOKEN=xxx → GET 放行只读,POST/PUT/DELETE 需带 Token
//     Token 通过 Authorization: Bearer <token> 或 X-Auth-Token: <token> 传入
//
// 设计动机:补齐"所有写操作裸奔"的安全口子,但不强制本地用户配 Token。
func authMiddleware() gin.HandlerFunc {
	authToken := os.Getenv("EVAL_AUTH_TOKEN")
	return func(c *gin.Context) {
		// 未配置 Token → 全部放行(本地默认)
		if authToken == "" {
			c.Next()
			return
		}
		// 只读方法(GET/OPTIONS)放行,只保护写操作
		method := c.Request.Method
		if method == "GET" || method == "OPTIONS" {
			c.Next()
			return
		}
		// 写操作:校验 Token
		provided := c.GetHeader("X-Auth-Token")
		if provided == "" {
			if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				provided = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if provided != authToken {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "未授权:请提供有效的 Token(EVAL_AUTH_TOKEN)",
			})
			return
		}
		c.Next()
	}
}

// generateShortID 生成简短的请求追踪 ID(12 字符)。
// 用 uuid 前 12 位,保证每次请求唯一(修复原确定性异或导致所有请求 ID 相同的 bug)。
func generateShortID() string {
	return uuid.New().String()[:12]
}