package store

// schema_eval.go 定义评测域表:题库、Bad Case、异常归因。
// 这些表支撑 ②引擎核心 与 ④差异化壁垒(归因闭环、RLHF 数据准备)。

// migrateEval 创建评测域表。
func migrateEval() {
	execAll("eval", []string{
		// cron_jobs — 定时任务(MRD 10.4 / 5.2)
		`CREATE TABLE IF NOT EXISTS cron_jobs (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			data        TEXT NOT NULL,                  -- JSON: 完整配置
			enabled     INTEGER DEFAULT 1,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// questions — 评测题库(MRD 11.3 评测题库页)
		// 一道题 = 给被测 Agent 的一个 prompt + 可选的期望输出(用于 metric 打分)
		// source 来源:manual(手动) / ai(AI 圆桌出题) / baseline(基线库抽样) / dynamic(动态扩题)
		// difficulty: entry(入门) / mid(中级) / high(高级) / adversarial(对抗)
		// tags 是 JSON 数组,按场景标签筛选(如 Kitex / 高并发锁 / Redis 穿透)
		`CREATE TABLE IF NOT EXISTS questions (
			id          TEXT PRIMARY KEY,
			scene       TEXT NOT NULL DEFAULT 'agent',  -- agent | model | competitor | prd | product
			title       TEXT NOT NULL,                  -- 题目摘要(列表展示用)
			prompt      TEXT NOT NULL,                  -- 喂给被测 Agent 的完整输入
			expected    TEXT,                           -- 期望输出(metric 打分参照,可空)
			difficulty  TEXT NOT NULL DEFAULT 'mid',    -- entry | mid | high | adversarial
			source      TEXT NOT NULL DEFAULT 'manual', -- manual | ai | baseline | dynamic
			tags        TEXT,                           -- JSON array
			rule_set_id TEXT,                           -- 该题默认关联的规则集(可空)
			source_repo TEXT,                           -- V2 7.1 来源仓库名
			source_url  TEXT,                           -- V2 7.1 来源文件 URL
			desensitized INTEGER DEFAULT 0,             -- V2 7.1 已脱敏标记
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// bad_cases — Bad Case 负样本库(MRD 7.3 / 4.6)
		// 记录评测中得分低于阈值的(题, 回答)对,作为 RLHF 数据与规则反哺的原料
		// feedback_type: thumbs_down(人工踩) / auto_low_score(引擎自动标记)
		// vector 为该 case 的 embedding 向量(RAG 检索用,④.4 填充;现阶段为空)
		// status: pending(待处理) / precipitated(已沉淀进规则) / ignored(已忽略)
		`CREATE TABLE IF NOT EXISTS bad_cases (
			id          TEXT PRIMARY KEY,
			task_id     TEXT NOT NULL,                  -- 来源评测任务
			question_id TEXT NOT NULL,                  -- 哪道题答砸了
			agent_id    TEXT,                           -- 哪个 Agent 答的
			answer      TEXT NOT NULL,                  -- 被测 Agent 的实际回答
			score       REAL,                           -- 该题得分
			feedback_type TEXT NOT NULL DEFAULT 'auto_low_score', -- auto_low_score | thumbs_down
			note        TEXT,                           -- 人工批注(踩/赞时填写)
			vector      TEXT,                           -- embedding 向量(RAG 用,Base64 或 JSON)
			status      TEXT NOT NULL DEFAULT 'pending', -- pending | precipitated | ignored
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bad_cases_agent ON bad_cases(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bad_cases_question ON bad_cases(question_id)`,

		// attributions — 异常归因记录(MRD 5.5 Auto-Dream Closure / ④.3)
		// 后台异步归因引擎分析失败原因,反哺 System Prompt
		// root_cause 分类:prompt_compliance(指令遵循度下降) / context_loss(上下文压缩丢信息) /
		//                  tool_failure(工具调用失败) / model_limit(模型能力上限)
		// suggested_fix 是引擎生成的修正建议(如新 System Prompt 片段)
		`CREATE TABLE IF NOT EXISTS attributions (
			id             TEXT PRIMARY KEY,
			task_id        TEXT NOT NULL,               -- 关联的失败任务
			root_cause     TEXT NOT NULL,               -- prompt_compliance | context_loss | tool_failure | model_limit
			analysis       TEXT NOT NULL,               -- 归因分析正文
			suggested_fix  TEXT,                        -- 自动修正建议(如新 Prompt 片段)
			applied        INTEGER DEFAULT 0,           -- 该修正是否已被采纳应用
			created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// task_feedback — 任务级人工反馈(MRD 7.3 / 4.3 Human-in-the-Loop)
		`CREATE TABLE IF NOT EXISTS task_feedback (
			id          TEXT PRIMARY KEY,
			task_id     TEXT NOT NULL,
			action      TEXT NOT NULL,                  -- thumbs_up | thumbs_down
			note        TEXT,                           -- 批注
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// rule_suggestions — 规则更新建议(人工批注触发)
		`CREATE TABLE IF NOT EXISTS rule_suggestions (
			id          TEXT PRIMARY KEY,
			bad_case_id TEXT NOT NULL,
			suggestion  TEXT NOT NULL,                  -- 建议内容
			status      TEXT NOT NULL DEFAULT 'pending', -- pending | applied | rejected
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// rag_embeddings — 规则向量索引(MRD 5.7 / 4.4 RAG 检索)
		// 每条规则的 embedding 向量,用于语义检索最相关规则
		// vector 是 JSON 数组(float64),维度由引擎决定(默认 128)
		`CREATE TABLE IF NOT EXISTS rag_embeddings (
			rule_id     TEXT PRIMARY KEY,
			dimension   TEXT NOT NULL,
			vector      TEXT NOT NULL,                  -- JSON: [0.1, 0.2, ...]
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 为题库建索引,加速按场景/难度筛选(评测题库页左侧筛选用)
		`CREATE INDEX IF NOT EXISTS idx_questions_scene ON questions(scene)`,
		`CREATE INDEX IF NOT EXISTS idx_questions_difficulty ON questions(difficulty)`,
		`CREATE INDEX IF NOT EXISTS idx_questions_source ON questions(source)`,

		// Bad Case 按状态/任务检索(数据看板"最近 Bad Case"列表用)
		`CREATE INDEX IF NOT EXISTS idx_bad_cases_status ON bad_cases(status)`,
		`CREATE INDEX IF NOT EXISTS idx_bad_cases_task ON bad_cases(task_id)`,
		// question_sets — 测试卷(基线测试集,V2 7.1)
		// 按标签/难度筛选一组题,打包成可复用的测试卷。
		`CREATE TABLE IF NOT EXISTS question_sets (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT,
			scene       TEXT DEFAULT 'agent',
			question_ids TEXT,                          -- JSON array of question IDs
			tags        TEXT,                           -- JSON array
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	})

	// 增量迁移:老库补列(V2 7.1 基线字段)
	migrateAddColumn("questions", "source_repo", "TEXT")
	migrateAddColumn("questions", "source_url", "TEXT")
	migrateAddColumn("questions", "desensitized", "INTEGER DEFAULT 0")
}
