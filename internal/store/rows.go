package store

// rows.go — 跨包共享的数据行结构。
//
// api 包的 handler 各自定义了局部 struct(如 api.Agent),但 engine 层
// 需要从 store 直接读取数据,不能依赖 api 包(避免循环依赖)。
// 因此在此定义中立的行结构,供 engine/config 等其他包使用。
//
// 命名约定: XxxRow,字段对应数据库列(下划线转驼峰)。

// AgentRow 对应 agents 表一行。
type AgentRow struct {
	ID                   string
	Name                 string
	Adapter              string // api | source | terminal
	Model                string
	Tools                string // JSON array
	Online               bool
	PermissionLevel      string // read-only | workspace-write | full
	SystemPrompt         string // 专属 system prompt(创造 Agent 生成)
	SearchProvider       string // none | brave | tavily(联网搜索)
	SearchAPIKey         string // 联网搜索 API Key
	ExampleConversations string // JSON: 示例对话/验收用例
	AgentsMD             string // 第1层:专属 AGENTS.md(项目约定)
	SkillIDs             string // 第3层:引用的 skill_id JSON 数组
	MaxTurns             int    // ReAct 最大轮数(默认 8)
	VerifyCommand        string // 第6层:自我验证命令(如 go test ./...)
}

// AgentSummary 是 agents 表的列表视图(含时间戳,供 API 层列表/详情用)。
// 比 AgentRow 轻量:只含管理界面需要的字段。
type AgentSummary struct {
	ID              string
	Name            string
	Adapter         string
	Model           string
	Tools           string
	Online          bool
	PermissionLevel string
	CreatedAt       string
	UpdatedAt       string
}

// AgentDefinitionRow 对应 agent_definitions 表一行(声明式 Agent 定义文件)。
type AgentDefinitionRow struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Manifest             string `json:"manifest"`               // JSON: 完整 agent 定义
	InstantiatedAgentID  string `json:"instantiated_agent_id"`  // 实例化后的 agents.id
}

// AgentRunRow 对应 agent_runs 表一行(运行状态追踪)。
type AgentRunRow struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Status    string `json:"status"`  // spawning|thinking|calling_tool|reading_file|done|error
	Detail    string `json:"detail"`
	StartedAt string `json:"started_at"`
	UpdatedAt string `json:"updated_at"`
}

// HookRow 对应 agent_hooks 表一行(harness 第5层 生命周期钩子)。
type HookRow struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Event     string `json:"event"`   // PreToolUse|PostToolUse|Stop|UserPromptSubmit|SessionStart|SessionEnd
	Matcher   string `json:"matcher"` // 工具名匹配(* / Bash* / 精确名)
	Command   string `json:"command"` // shell 命令
	Enabled   bool   `json:"enabled"`
}

// QuestionRow 对应 questions 表一行。
type QuestionRow struct {
	ID           string
	Scene        string
	Title        string
	Prompt       string
	Expected     string
	Difficulty   string
	Source       string
	Tags         string
	RuleSetID    string
	SourceRepo   string // V2 7.1
	SourceURL    string // V2 7.1
	Desensitized bool   // V2 7.1
	CreatedAt    string
}

// RuleRow 对应 rules 表一行。
type RuleRow struct {
	ID        string
	Name      string
	Dimension string
	Scoring   string  // llm | metric | manual
	Weight    float64
	Content   string
	Tags      string
	CreatedAt string
}

// RuleSetRow 对应 rule_sets 表一行。
type RuleSetRow struct {
	ID      string
	Name    string
	Scene   string
	RuleIDs string // JSON array
}

// TaskRow 对应 tasks 表一行(engine 读取用)。
type TaskRow struct {
	ID             string
	Name           string
	Scene          string
	AgentID        string
	RuleSet        string // JSON 字符串
	Status         string
	Result         string // JSON 报告
	ChaosEnabled   bool   // 是否开启混沌对抗模式
	ExpertGroupID  string // 专家组 ID(留空用默认专家组 eg-default)
}

// TaskSummary 是 tasks 表的列表视图(含时间戳,供 API 层列表/详情用)。
type TaskSummary struct {
	ID            string
	Name          string
	Scene         string
	AgentID       string
	RuleSet       string
	Status        string
	Result        string
	ChaosEnabled  bool
	ExpertGroupID string
	CreatedAt     string
	UpdatedAt     string
}

// ExpertGroupRow 对应 expert_groups 表一行。
type ExpertGroupRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"` // active | draft
}

// ExpertGroupMemberRow 对应 expert_group_members 表一行。
type ExpertGroupMemberRow struct {
	ID                string  `json:"id"`
	GroupID           string  `json:"group_id"`
	Ordinal           int     `json:"ordinal"`
	MemberType        string  `json:"member_type"` // agent | model | role
	MemberRef         string  `json:"member_ref"`
	Dimension         string  `json:"dimension"`
	RoleLabel         string  `json:"role_label"`
	Weight            float64 `json:"weight"`
	PromptText        string  `json:"prompt_text"`
	PromptTemplateID  string  `json:"prompt_template_id"`
}
