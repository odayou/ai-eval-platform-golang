package store

// schema_core.go 定义"七库"核心表结构。
//
// 七库是平台的地基(MRD 第四章),所有被管理的资源都在这里:
//   ① agents      — Agent 库(被测主体)
//   ② models      — 大模型库(LLM 端点 + Key)
//   ③ rules       — 规则库(评测维度 + 评分方式 + 权重)
//   ④ rule_sets   — 规则集(规则的打包,一次评测挂一个集)
//   ⑤ tasks       — 评测任务(一次评测 = 一条记录)
//   ⑥ tools       — Tools 库(Agent 可调用的原子能力)
//   ⑦ mcp_servers — MCP Server 库(外部能力网关)
//   ⑧ skills      — Skills 库(Tool 的组合编排)
//   ⑨ model_config— 模型配置(角色→模型映射,圆桌/出题/视觉/RAG 各用哪个模型)
//   ⑩ prompt_templates — Prompt 模板库(版本化)

// migrateCore 创建七库核心表。所有表用 CREATE TABLE IF NOT EXISTS,可重复执行。
func migrateCore() {
	execAll("core", []string{
		// ① agents — Agent 库
		// adapter 表示接入方式:api(直接 HTTP)/ source(本地源码)/ terminal(终端托管)
		// model 字段是该 Agent 默认使用的模型名(可被任务级配置覆盖)
		// tools 是 JSON 数组,记录该 Agent 挂载了哪些 tool_id
		`CREATE TABLE IF NOT EXISTS agents (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			adapter     TEXT NOT NULL DEFAULT 'api',  -- api | source | terminal
			model       TEXT,
			tools       TEXT,                          -- JSON array of tool_id
			online      INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ② models — 大模型库
		// api_key 明文存储(Demo 阶段);生产环境应加密,见 ROADMAP ④.4 安全注入
		// available 标记该模型当前是否可用(连通性检测后更新)
		`CREATE TABLE IF NOT EXISTS models (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			base_url    TEXT NOT NULL,                 -- OpenAI 兼容端点,如 https://open.bigmodel.cn/api/paas/v4
			api_key     TEXT,                           -- 平台使用的 Key(被测 Agent 自带 Key 另存 agents 表)
			api_format  TEXT DEFAULT '',                -- API 协议格式:openai|anthropic|gemini|''(空=自动检测)
			available   INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ③ tasks — 评测任务
		// scene 是评测场景:agent/model/competitor/prd/product(对应顶部 5 个 Tab)
		// status 状态机:pending→running→done/failed/blown(熔断)
		// result 存评测报告 JSON(雷达图数据 + Bad Case 列表),由 ②引擎写入
		// chaos_enabled 是否开启混沌工程对抗模式(④.2):弱网/脏数据/越狱/资源耗尽注入
		`CREATE TABLE IF NOT EXISTS tasks (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			scene       TEXT NOT NULL DEFAULT 'agent',  -- agent | model | competitor | prd | product
			agent_id    TEXT,
			rule_set    TEXT,                           -- JSON: 规则集快照
			status      TEXT NOT NULL DEFAULT 'pending', -- pending | running | done | failed | blown
			result      TEXT,                           -- JSON 报告(引擎产出)
			chaos_enabled INTEGER DEFAULT 0,            -- 0=关闭 1=开启混沌对抗模式
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// tasks 表索引:看板/排行榜/列表的热点查询覆盖
		`CREATE INDEX IF NOT EXISTS idx_tasks_status_scene ON tasks(status, scene)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_agent ON tasks(agent_id)`,

		// ④ rules — 规则库(单条规则)
		// scoring 决定打分方式:metric(正则/指标) | llm(LLM-as-Judge) | manual(人工)
		// weight 是该规则在维度聚合时的权重
		// content 是规则正文(metric=校验逻辑, llm=rubric 提示词)
		`CREATE TABLE IF NOT EXISTS rules (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			dimension   TEXT NOT NULL,                 -- 所属维度:security | performance | architecture | product | correctness
			scoring     TEXT NOT NULL DEFAULT 'llm',   -- llm | metric | manual
			weight      REAL DEFAULT 1.0,
			content     TEXT NOT NULL,                  -- 规则正文 / 校验逻辑 / rubric
			tags        TEXT,                           -- JSON array
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ⑤ rule_sets — 规则集(规则的打包)
		// rule_ids 是 JSON 数组,引用 rules.id
		// 一个任务挂一个规则集,评测时动态读取(规则不硬编码进 Prompt)
		`CREATE TABLE IF NOT EXISTS rule_sets (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			scene       TEXT,
			rule_ids    TEXT,                           -- JSON array of rule IDs
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ⑥ tools — Tools 库(Agent 可调用的原子能力)
		// params_schema 是 JSON Schema,描述该工具的入参结构
		// builtin 标记是否平台内置(内置工具不可删)
		`CREATE TABLE IF NOT EXISTS tools (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT,
			params_schema TEXT,                         -- JSON Schema
			tags        TEXT,                           -- JSON array
			builtin     INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ⑦ mcp_servers — MCP Server 库(外部能力网关)
		// protocol: stdio / sse / streamable(MCP 三种传输)
		// status: online / offline(连通性探测后更新)
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			protocol    TEXT NOT NULL DEFAULT 'stdio',  -- stdio | sse | streamable
			endpoint    TEXT,
			status      TEXT NOT NULL DEFAULT 'offline',
			capabilities TEXT,                          -- JSON array
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ⑧ skills — Skills 库(Tool 的组合编排)
		// tool_ids 是 JSON 数组,记录该 Skill 编排了哪些 tool_id
		`CREATE TABLE IF NOT EXISTS skills (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT,
			content     TEXT,                           -- skill 完整文本
			tool_ids    TEXT,                           -- JSON array of tool IDs
			tags        TEXT,                           -- JSON array
			builtin     INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ⑨ model_config — 模型配置(角色→模型映射)
		// 这是圆桌合议引擎(③.2)的数据基础:
		//   main            主导者 Agent
		//   expert_security 安全专家 / expert_perf 性能专家 / expert_arch 架构专家 / expert_product 产品专家
		//   question_gen    出题 Agent / vision 视觉解析 / rag RAG 检索
		// role 唯一,每个角色只能绑一个模型(Upsert)
		`CREATE TABLE IF NOT EXISTS model_config (
			id          TEXT PRIMARY KEY,
			role        TEXT NOT NULL UNIQUE,           -- main | expert_security | expert_perf | expert_arch | expert_product | question_gen | vision | rag
			model_id    TEXT NOT NULL,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// ⑩ prompt_templates — Prompt 模板库
		// version 支持版本回滚;model_hint 标记该模板针对哪个模型优化
		`CREATE TABLE IF NOT EXISTS prompt_templates (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			content     TEXT NOT NULL,
			version     INTEGER DEFAULT 1,
			model_hint  TEXT,                           -- 该模板针对哪个模型优化
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	})

		// 增量迁移:为已有库补列(ALTER TABLE 不支持 IF NOT EXISTS,用 ignore 错误实现幂等)。
		// 新加的 chaos_enabled 列:老库升级时补上。
		migrateAddColumn("tasks", "chaos_enabled", "INTEGER DEFAULT 0")
		// api_format 列:支持显式声明 API 协议格式(openai/anthropic/gemini),空=自动检测
		migrateAddColumn("models", "api_format", "TEXT DEFAULT ''")

		// ⑪ scenes — 自定义评测场景(MRD 6.2)
		execAll("scenes", []string{
			`CREATE TABLE IF NOT EXISTS scenes (
				id          TEXT PRIMARY KEY,
				label       TEXT NOT NULL,
				description TEXT,
				strategy    TEXT DEFAULT 'text_review',
				rule_set_id TEXT,                       -- 该分类的默认规则集
				default_question_set_id TEXT,            -- 该分类的默认题集(MRD 批次④:分类套件)
				builtin     INTEGER DEFAULT 0,
				created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		})
		// 增量迁移:为老库 scenes 补 default_question_set_id 列
		migrateAddColumn("scenes", "default_question_set_id", "TEXT")

		// ⑫ prompt_template_versions — Prompt 模板版本历史(MRD 4.7)
		execAll("pt_versions", []string{
			`CREATE TABLE IF NOT EXISTS prompt_template_versions (
				id          TEXT PRIMARY KEY,
				template_id TEXT NOT NULL,
				version     INTEGER NOT NULL,
				name        TEXT,
				content     TEXT,
				model_hint  TEXT,
				created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		})

		// ⑬ rule_relations — 规则关系边(MRD §7.3 知识图谱规则库)
		// 规则之间显式的关系边,让 RAG 检索能顺着边拉出关联规则。
		// relation_type:
		//   related_to         — 同主题关联(如"高并发死锁"和"RPC 超时"都属于稳定性)
		//   belong_to_category — 归属同一类(规则→规则分类)
		//   derived_from       — 派生关系(后一条由前一条细化而来)
		// weight: 关联强度 0-1,检索时加权用
		execAll("rule_relations", []string{
			`CREATE TABLE IF NOT EXISTS rule_relations (
				id              TEXT PRIMARY KEY,
				source_rule_id  TEXT NOT NULL,
				target_rule_id  TEXT NOT NULL,
				relation_type   TEXT NOT NULL DEFAULT 'related_to',
				weight          REAL DEFAULT 1.0,
				created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_rule_relations_source ON rule_relations(source_rule_id)`,
			`CREATE INDEX IF NOT EXISTS idx_rule_relations_target ON rule_relations(target_rule_id)`,
		})

		// ⑭ judge_models — 考官模型库(MRD 模型职责分离)
		// 与 models 表(被测对象/考生)分离,避免自评自审风险。
		// role 唯一:main(主导者) | expert_security | expert_perf | expert_arch | expert_product | question_gen
		// 平台评测引擎(圆桌/判官/出题/路由)只读这张表,绝不读 models 表。
		execAll("judge_models", []string{
			`CREATE TABLE IF NOT EXISTS judge_models (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL,                  -- 真实模型 ID(如 glm-4-flash)
				base_url    TEXT NOT NULL,                  -- OpenAI 兼容端点
				api_key     TEXT,                            -- 考官专用 Key(可与考生 Key 不同)
				api_format  TEXT DEFAULT '',                -- API 协议格式:openai|anthropic|gemini|''(空=自动检测)
				role        TEXT NOT NULL UNIQUE,           -- main | expert_security | expert_perf | expert_arch | expert_product | <自定义 expert_*>
				dimension   TEXT DEFAULT '',                -- 专家负责的评测维度(security/performance/architecture/product/...),非专家留空
				available   INTEGER DEFAULT 0,
				created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		})
		// 增量迁移:为老库 judge_models 补 dimension 列(升级后支持自定义专家)
		migrateAddColumn("judge_models", "dimension", "TEXT DEFAULT ''")
		// 增量迁移:为老库 judge_models 补 api_format 列(支持显式声明协议)
		migrateAddColumn("judge_models", "api_format", "TEXT DEFAULT ''")
		// 回填:预置专家的 dimension(schema 迁移每次启动都跑,新老库都覆盖)。
		// 解决"旧库升级后 judge_models 永远为空/无 dimension"的问题。
		judgeDims := map[string]string{
			"expert_security": "security",
			"expert_perf":     "performance",
			"expert_arch":     "architecture",
			"expert_product":  "product",
		}
		for role, dim := range judgeDims {
			DB.Exec(`UPDATE judge_models SET dimension = ? WHERE role = ? AND (dimension IS NULL OR dimension = '')`,
				dim, role)
		}

		// ⑮ expert_groups — 专家组(可配置的圆桌成员组合)
		// 一个专家组 = 一组评测成员,成员可来自 agent / 模型+提示词 / 自定义 role。
		// 任务挂 expert_group_id 决定用哪个专家组;留空用默认专家组(eg-default)。
		execAll("expert_groups", []string{
			`CREATE TABLE IF NOT EXISTS expert_groups (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL,
				description TEXT,
				status      TEXT DEFAULT 'active',          -- active | draft(AI 生成未确认为 draft)
				created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS expert_group_members (
				id                  TEXT PRIMARY KEY,
				group_id            TEXT NOT NULL,
				ordinal             INTEGER DEFAULT 0,      -- 成员顺序
				member_type         TEXT NOT NULL,          -- agent | model | role
				member_ref          TEXT,                   -- agent→agents.id; model→judge_models.role; role→自定义字符串
				dimension           TEXT,                   -- 该成员负责的评测维度(security/performance/...)
				role_label          TEXT,                   -- 显示名(如"安全专家")
				weight              REAL DEFAULT 1.0,       -- 打分权重
				prompt_text         TEXT,                   -- 内嵌提示词(成员专属 rubric)
				prompt_template_id  TEXT,                   -- 引用 prompt_templates.id(优先于 prompt_text)
				created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_egm_group ON expert_group_members(group_id)`,
		})
		// 增量迁移:tasks 表加 expert_group_id 列(可选,留空用默认专家组)
		migrateAddColumn("tasks", "expert_group_id", "TEXT")

		// ⑯ agent_definitions — 声明式 Agent 定义文件(创造 Agent 详细模式)
		// manifest 是完整 JSON:含 model/adapter/tools/mcp/skills/system_prompt/示例对话/联网配置
		// 实例化后回填 instantiated_agent_id(指向 agents 表)
		execAll("agent_definitions", []string{
			`CREATE TABLE IF NOT EXISTS agent_definitions (
				id                      TEXT PRIMARY KEY,
				name                    TEXT NOT NULL,
				description             TEXT,
				manifest                TEXT NOT NULL,
				instantiated_agent_id   TEXT,
				created_at              DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		})

		// ⑰ agent_runs — Agent 运行状态追踪(状态显示用,借鉴 claw-code worker_boot 状态机)
		execAll("agent_runs", []string{
			`CREATE TABLE IF NOT EXISTS agent_runs (
				id          TEXT PRIMARY KEY,
				agent_id    TEXT NOT NULL,
				status      TEXT NOT NULL,    -- spawning|thinking|calling_tool|reading_file|done|error
				detail      TEXT,
				started_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_agent_runs_agent ON agent_runs(agent_id)`,
		})

		// 增量迁移:agents 表加列(创造 Agent 功能扩展)
		migrateAddColumn("agents", "system_prompt", "TEXT")            // 专属 system prompt
		migrateAddColumn("agents", "search_provider", "TEXT")          // none|brave|tavily
		migrateAddColumn("agents", "search_api_key", "TEXT")           // 联网搜索 API Key
		migrateAddColumn("agents", "example_conversations", "TEXT")    // JSON: 示例对话/验收用例

		// ⑱ agent_hooks — Agent 生命周期钩子(harness 第5层)
		// 仿 Claude Code settings.json 的 hooks 格式:event + matcher + command
		execAll("agent_hooks", []string{
			`CREATE TABLE IF NOT EXISTS agent_hooks (
				id          TEXT PRIMARY KEY,
				agent_id    TEXT NOT NULL,
				event       TEXT NOT NULL,    -- PreToolUse|PostToolUse|Stop|UserPromptSubmit|SessionStart|SessionEnd
				matcher     TEXT,             -- 工具名匹配(* 全匹配,Bash* 通配)
				command     TEXT NOT NULL,    -- shell 命令(stdin 收 JSON,退出码 0=允许/2=拒绝)
				enabled     INTEGER DEFAULT 1,
				created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_agent_hooks_agent ON agent_hooks(agent_id)`,
		})

		// 增量迁移:agents 表加 harness 列(六层能力配置)
		migrateAddColumn("agents", "agents_md", "TEXT")                // 第1层:专属 AGENTS.md
		migrateAddColumn("agents", "skill_ids", "TEXT")                // 第3层:引用的 skill_id JSON 数组
		migrateAddColumn("agents", "max_turns", "INTEGER DEFAULT 8")   // ReAct 最大轮数
		migrateAddColumn("agents", "verify_command", "TEXT")           // 第6层:自我验证命令

		// ════════════════════════════════════════════════════════════
		// 六大拓展方向(2026-06-23)
		// ════════════════════════════════════════════════════════════

		// ⑲ pipelines — Agent CI/CD 管线(方向1)
		execAll("pipelines", []string{
			`CREATE TABLE IF NOT EXISTS pipelines (
				id              TEXT PRIMARY KEY,
				name            TEXT NOT NULL,
				agent_id        TEXT,
				trigger_type    TEXT DEFAULT 'manual',    -- manual | git_push | cron | api
				trigger_config  TEXT,                     -- JSON: webhook_url/cron_expr/scene
				gate_config     TEXT,                     -- JSON: min_score/max_cost/regression_threshold
				scene           TEXT DEFAULT 'agent',
				rule_set_id     TEXT,
				enabled         INTEGER DEFAULT 1,
				created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS pipeline_runs (
				id              TEXT PRIMARY KEY,
				pipeline_id     TEXT NOT NULL,
				status          TEXT DEFAULT 'pending',   -- pending|running|passed|failed|aborted
				trigger_type    TEXT,
				score_correctness REAL,
				score_security    REAL,
				score_performance REAL,
				cost_usd         REAL,
				overall_passed   INTEGER DEFAULT 0,
				report           TEXT,                    -- JSON 报告
				started_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
				finished_at      DATETIME
			)`,
			`CREATE INDEX IF NOT EXISTS idx_pipeline_runs_pipeline ON pipeline_runs(pipeline_id)`,
		})

		// ⑳ arena — Agent 对战排行榜(方向2)
		execAll("arena", []string{
			`CREATE TABLE IF NOT EXISTS arena_submissions (
				id              TEXT PRIMARY KEY,
				agent_id        TEXT NOT NULL,
				agent_name      TEXT,
				developer       TEXT,
				scene           TEXT DEFAULT 'agent',
				elo_rating      INTEGER DEFAULT 1200,
				battles_won     INTEGER DEFAULT 0,
				battles_lost    INTEGER DEFAULT 0,
				battles_draw    INTEGER DEFAULT 0,
				avg_correctness REAL,
				avg_security    REAL,
				avg_performance REAL,
				total_cost      REAL,
				status          TEXT DEFAULT 'evaluating', -- evaluating|ranked|retired
				submitted_at    DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS arena_battles (
				id              TEXT PRIMARY KEY,
				submission_a    TEXT NOT NULL,
				submission_b    TEXT NOT NULL,
				question_id     TEXT,
				winner          TEXT,                     -- a | b | draw
				score_a         REAL,
				score_b         REAL,
				elo_delta_a     INTEGER,
				elo_delta_b     INTEGER,
				transcript      TEXT,                     -- JSON 对战回放
				battled_at      DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_arena_submissions_scene ON arena_submissions(scene)`,
		})

		// ㉑ redteam — 安全红队测试(方向3)
		execAll("redteam", []string{
			`CREATE TABLE IF NOT EXISTS redteam_scans (
				id              TEXT PRIMARY KEY,
				agent_id        TEXT NOT NULL,
				scan_config     TEXT,                     -- JSON: 哪些类测试启用
				status          TEXT DEFAULT 'pending',   -- pending|scanning|completed|failed
				cvss_score      REAL,                     -- 综合风险评分 0-10
				findings        TEXT,                     -- JSON: 发现项列表
				report          TEXT,                     -- JSON: 完整报告(markdown)
				started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
				completed_at    DATETIME
			)`,
			`CREATE TABLE IF NOT EXISTS redteam_findings (
				id              TEXT PRIMARY KEY,
				scan_id         TEXT NOT NULL,
				category        TEXT,                     -- injection|leakage|jailbreak|privilege|poison|side_channel
				severity        TEXT,                     -- critical|high|medium|low|info
				title           TEXT,
				description     TEXT,
				evidence        TEXT,                     -- 攻击 payload + agent 响应
				remediation     TEXT,
				cvss_vector     TEXT,
				found_at        DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_redteam_findings_scan ON redteam_findings(scan_id)`,
		})

		// ㉒ observability — 生产监控(方向4)
		execAll("observability", []string{
			`CREATE TABLE IF NOT EXISTS agent_metrics (
				id              TEXT PRIMARY KEY,
				agent_id        TEXT NOT NULL,
				timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP,
				prompt_tokens   INTEGER DEFAULT 0,
				completion_tokens INTEGER DEFAULT 0,
				cache_read_tokens INTEGER DEFAULT 0,
				cost_usd        REAL DEFAULT 0,
				latency_ms      REAL DEFAULT 0,
				error_count     INTEGER DEFAULT 0,
				tool_call_count INTEGER DEFAULT 0,
				event_type      TEXT                       -- message|action|observation|error
			)`,
			`CREATE INDEX IF NOT EXISTS idx_agent_metrics_agent_ts ON agent_metrics(agent_id, timestamp)`,
			`CREATE TABLE IF NOT EXISTS alert_rules (
				id              TEXT PRIMARY KEY,
				agent_id        TEXT,
				metric          TEXT,                     -- cost_usd|latency_ms|error_count
				condition       TEXT,                     -- gt|lt|eq
				threshold       REAL,
				window_minutes  INTEGER DEFAULT 5,
				enabled         INTEGER DEFAULT 1,
				last_triggered  DATETIME,
				created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS alert_events (
				id              TEXT PRIMARY KEY,
				rule_id         TEXT NOT NULL,
				agent_id        TEXT,
				metric_value    REAL,
				threshold       REAL,
				message         TEXT,
				triggered_at    DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		})

		// ㉓ finetune — 评测反哺训练(方向5)
		execAll("finetune", []string{
			`CREATE TABLE IF NOT EXISTS training_datasets (
				id              TEXT PRIMARY KEY,
				name            TEXT NOT NULL,
				task_id         TEXT,
				scene           TEXT,
				format          TEXT DEFAULT 'sft',       -- sft|dpo|both
				sft_pairs       TEXT,                     -- JSON: [{prompt, completion}]
				dpo_pairs       TEXT,                     -- JSON: [{prompt, chosen, rejected}]
				bad_case_count  INTEGER DEFAULT 0,
				total_pairs     INTEGER DEFAULT 0,
				status          TEXT DEFAULT 'draft',     -- draft|exported|consumed
				created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		})

		// ㉔ workflows — 多 Agent 编排(方向6)
		execAll("workflows", []string{
			`CREATE TABLE IF NOT EXISTS workflows (
				id              TEXT PRIMARY KEY,
				name            TEXT NOT NULL,
				description     TEXT,
				dag             TEXT NOT NULL,             -- JSON: DAG 定义
				status          TEXT DEFAULT 'draft',      -- draft|active|archived
				created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS workflow_runs (
				id              TEXT PRIMARY KEY,
				workflow_id     TEXT NOT NULL,
				status          TEXT DEFAULT 'pending',    -- pending|running|completed|failed
				input_data      TEXT,                      -- JSON: 输入
				output_data     TEXT,                      -- JSON: 输出
				node_states     TEXT,                      -- JSON: 各节点状态
				started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
				completed_at    DATETIME
			)`,
			`CREATE INDEX IF NOT EXISTS idx_workflow_runs_wf ON workflow_runs(workflow_id)`,
		})
	}

// migrateAddColumn 幂等地给表加列(列已存在则忽略错误)。
// SQLite 的 ALTER TABLE ADD COLUMN 在列已存在时报错,这里吞掉该错误。
func migrateAddColumn(table, column, definition string) {
	_, _ = DB.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
}
