package store

// schema_agent.go 定义 Agent 运行时表,架构理念完整借鉴 claw-code(Rust 版 Claude Code 重写)。
//
// claw-code 的核心抽象(我们移植到评测平台):
//
//   ConversationRuntime  → conversations 表
//     一次评测会话的运行时容器。被测 Agent 的每一轮对话、工具调用都在此上下文里发生。
//     claw-code 用 conversation.rs 管理这个状态机,我们用它来追溯被测 Agent 的完整行为。
//
//   PermissionEnforcer    → permissions 表
//     权限门控。被测 Agent 想调用某个 tool(尤其危险操作如 shell_exec、file_write)时,
//     必须先经过权限检查。claw-code 的 permission_enforcer.rs + permissions.rs 即此机制。
//     评测平台借它来:① 记录 Agent 的危险行为 ② Chaos 注入时的拦截点 ③ 安全维度评分依据。
//
//   ContextCompaction     → compactions 表
//     上下文压缩。claw-code 的 compact.rs 在长对话时做滑动窗口 + 摘要递进。
//     评测平台借它来:① 评长上下文 Agent ② Auto-Dream 归因时判断是否信息丢失 ③ 5.2 上下文重构层。
//
//   Session + SecretRedaction → sessions 表
//     会话持久化 + 密钥脱敏。claw-code 的 session.rs 在落盘前剥离 API Key。
//     评测平台同样:轨迹回放、思考链路可视化时,绝不能泄露 Key。
//
//   ToolCall trace        → tool_calls 表
//     工具调用链路记录。每次 Agent 调 tool,落一条记录,支撑轨迹拓扑图(⑤ Monaco/拓扑组件)。
//
// 这些表在 ②引擎核心 与 ③评测增强 阶段被真正写入,①阶段先把结构建好。

// migrateAgent 创建 claw-code 架构映射表。
func migrateAgent() {
	execAll("agent", []string{
		// conversations — 评测会话运行时(借鉴 claw-code conversation.rs)
		// 一次评测(task)可能产生多轮对话,每轮是一个 conversation。
		// messages 是完整消息序列的 JSON(user/assistant/tool 多角色)。
		// status: active(进行中) / finished(正常结束) / aborted(被熔断或手动中止)
		`CREATE TABLE IF NOT EXISTS conversations (
			id          TEXT PRIMARY KEY,
			task_id     TEXT NOT NULL,                  -- 所属评测任务
			turn        INTEGER NOT NULL,               -- 第几轮(从 1 开始)
			messages    TEXT NOT NULL,                  -- JSON: [{role, content, tool_calls?}, ...]
			status      TEXT NOT NULL DEFAULT 'active', -- active | finished | aborted
			token_in    INTEGER DEFAULT 0,             -- 输入 token 数(费用估算用)
			token_out   INTEGER DEFAULT 0,             -- 输出 token 数
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// permissions — 权限门控记录(借鉴 claw-code permission_enforcer.rs / permissions.py)
		// 每次被测 Agent 请求调用危险 tool,记一条决策。
		// tool: 工具名(如 shell_exec / file_write / http_request)
		// level: 触发本次决策时 Agent 的权限级别 read-only | workspace-write | full
		// decision: allowed(放行) / denied(拒绝) / asked(需人工确认) / injected(混沌注入记录)
		// reason: 决策依据(如"命中 chaos 注入规则" / "沙箱白名单内" / "危险命令 rm -rf / 被拦截")
		// chaos_injected: 该次调用是否被注入了混沌(④.2),1=是
		`CREATE TABLE IF NOT EXISTS permissions (
			id             TEXT PRIMARY KEY,
			task_id        TEXT NOT NULL,
			conversation_id TEXT,
			tool           TEXT NOT NULL,               -- 工具名
			args           TEXT,                        -- JSON: 调用参数(已脱敏)
			level          TEXT,                        -- Agent 权限级别: read-only | workspace-write | full
			decision       TEXT NOT NULL DEFAULT 'allowed', -- allowed | denied | asked | injected
			reason         TEXT,
			chaos_injected INTEGER DEFAULT 0,           -- 是否被混沌注入(0/1)
			created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// compactions — 上下文压缩摘要(借鉴 claw-code compact.rs)
		// 当对话超过 token 阈值,触发压缩:旧消息 → 摘要 → 摘要 + 近期消息
		// original_tokens / compacted_tokens 记录压缩比,供 5.2 上下文重构层与归因分析使用
		`CREATE TABLE IF NOT EXISTS compactions (
			id               TEXT PRIMARY KEY,
			conversation_id  TEXT NOT NULL,
			original_tokens  INTEGER NOT NULL,          -- 压缩前 token 数
			compacted_tokens INTEGER NOT NULL,          -- 压缩后 token 数
			summary          TEXT NOT NULL,             -- 压缩产出的摘要
			created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// sessions — 会话快照 + 密钥脱敏(借鉴 claw-code session.rs)
		// 用于断点恢复、轨迹回放。snapshot 是脱敏后的完整会话状态 JSON。
		// redaction_count: 本次落盘剥离了多少处密钥(可观测脱敏是否生效)
		`CREATE TABLE IF NOT EXISTS sessions (
			id              TEXT PRIMARY KEY,
			task_id         TEXT NOT NULL,
			conversation_id TEXT,
			snapshot        TEXT NOT NULL,              -- JSON: 脱敏后的会话快照
			redaction_count INTEGER DEFAULT 0,          -- 剥离的密钥条数
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// tool_calls — 工具调用链路(支撑轨迹拓扑图 ⑤)
		// 每次被测 Agent 调 tool,落一条,记录入参/出参/耗时/是否成功。
		// 这些记录串联起来就是 Agent 的"思考轨迹",前端用 G6/Vue Flow 画 DAG。
		`CREATE TABLE IF NOT EXISTS tool_calls (
			id              TEXT PRIMARY KEY,
			task_id         TEXT NOT NULL,
			conversation_id TEXT,
			tool            TEXT NOT NULL,              -- 工具名
			input           TEXT,                       -- JSON: 入参(已脱敏)
			output          TEXT,                       -- JSON: 出参(已脱敏)
			duration_ms     INTEGER,                    -- 耗时毫秒(性能专家维度用)
			success         INTEGER DEFAULT 1,          -- 0=失败 1=成功
			error           TEXT,                       -- 失败原因
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 索引:按任务/会话检索各运行时记录
		`CREATE INDEX IF NOT EXISTS idx_conversations_task ON conversations(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_permissions_task ON permissions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_task ON tool_calls(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_compactions_conv ON compactions(conversation_id)`,

		// audit_logs — 白盒调用审计日志(完全可观测)
		// 统一记录三类事件(kind 区分):llm 调用 / 工具调用 / 运行时事件流。
		// 一次评测任务产生 N 条记录,按 seq 排序回放完整调用链。
		// 设计动机:让用户精确定位"评测结果哪里有问题"——可展开看任意一次 LLM 调用的完整输入/输出/思考。
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id            TEXT PRIMARY KEY,
			task_id       TEXT NOT NULL,
			turn          INTEGER DEFAULT 0,
			kind          TEXT NOT NULL,                  -- llm | tool | event
			seq           INTEGER DEFAULT 0,              -- 任务内自增序号(按时间排序)
			caller        TEXT,                            -- 调用者角色:agent/judge/expert_security/question_gen/router/compaction/guardian/probe/hook/terminal/testgen/mcp...
			provider      TEXT,                            -- llm 专用:openai/anthropic/gemini
			model         TEXT,                            -- llm 专用:模型名
			base_url      TEXT,                            -- llm 专用:端点
			api_format    TEXT,                            -- llm 专用:协议格式
			system_prompt TEXT,                            -- llm 专用:system 消息(脱敏后)
			input_text    TEXT,                            -- llm:用户prompt / tool:入参JSON / event:事件data JSON
			output_text   TEXT,                            -- llm:回复正文 / tool:出参 / event:空
			reasoning     TEXT,                            -- llm 专用:reasoning_content 原文(模型思考链)
			tool_name     TEXT,                            -- tool 专用:BashTool/FileRead/...
			token_in      INTEGER DEFAULT 0,
			token_out     INTEGER DEFAULT 0,
			latency_ms    INTEGER DEFAULT 0,
			http_status   INTEGER DEFAULT 0,
			attempt       INTEGER DEFAULT 0,
			is_mock       INTEGER DEFAULT 0,
			is_error      INTEGER DEFAULT 0,
			error_text    TEXT,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_task ON audit_logs(task_id, seq)`,
	})

	// 增量迁移:为老库补列(ALTER TABLE 不支持 IF NOT EXISTS,用 ignore 错误实现幂等)。
	// permissions.level:权限门控三级级别(MRD §5.6 / claw-code permissions.py)
	migrateAddColumn("permissions", "level", "TEXT")
	// agents.permission_level:每个被测 Agent 的默认权限级别(默认 workspace-write)
	migrateAddColumn("agents", "permission_level", "TEXT NOT NULL DEFAULT 'workspace-write'")
}
