package engine

// subagent.go — harness 第4层:Subagents(子代理,上下文防火墙)。
//
// 主 agent 可通过 Task 工具 fan-out 子代理。子代理在隔离上下文窗口跑自己的 ReAct loop,
// 完成后只返回摘要给主 agent——不污染主对话的 context(防 context rot)。
//
// 研究依据:
//   - Claude Code .claude/projects/.../subagents/:独立 jsonl(isSidechain:true)+ meta.json(agentType)
//   - learn-harness-engineering multi-agent-pattern:
//     * Coordinator 必须综合(synthesize),不能只转发理解
//     * 三种委派:Coordinator(零上下文继承)/Fork(全继承,单层)/Swarm(共享任务列表)
//     * Fork 子进程禁止再 fork(递归 guard,防 context 指数膨胀)
//   - harness-engineering concepts/06:Sub-Agents = 上下文防火墙
//
// 实现:Coordinator 模式 + Task 工具
//   - Task(subagent_type="Explore", prompt="...") → 子代理在隔离上下文跑 ReAct,返回摘要
//   - 内置类型:Explore(只读搜索)/Plan(规划)/General(通用)
//   - 子代理禁止调 Task 工具(单层 guard,防递归)

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-eval-platform/internal/store"
)

// SubagentType 内置子代理类型(仿 Claude Code)。
type SubagentType string

const (
	SubagentExplore SubagentType = "Explore" // 只读搜索(Read/Grep/Glob,不能写)
	SubagentPlan    SubagentType = "Plan"    // 规划(只读,产出结构化计划)
	SubagentGeneral SubagentType = "General" // 通用(继承全部工具,但不能 Task)
)

// subagentSystemPrompt 子代理的 system prompt(按类型不同)。
var subagentSystemPrompts = map[SubagentType]string{
	SubagentExplore: `你是一个只读搜索子代理(Explore)。你的任务是快速搜集信息并返回摘要。

规则:
- 只能使用只读工具(FileRead/WebSearch),禁止写操作(FileWrite/BashTool 的写命令)
- 快速扫描,不要深入实现
- 完成后返回结构化摘要(不要返回完整文件内容,只返回关键发现)

输出格式:
## 搜索结果摘要
- 发现 1: ...
- 发现 2: ...
## 关键文件
- path/to/file: 简述
## 建议
- 基于发现,建议...`,

	SubagentPlan: `你是一个规划子代理(Plan)。你的任务是分析需求并产出结构化执行计划。

规则:
- 只读(不能修改文件)
- 把任务拆解为可执行的步骤
- 标注每步的工具和预期产出

输出格式:
## 计划: <标题>
### 步骤 1: ...
  - 工具: FileRead
  - 预期: 找到配置文件
### 步骤 2: ...
  - 工具: BashTool
  - 预期: 运行测试
### 验证标准
- ...`,

	SubagentGeneral: `你是一个通用子代理(General)。你在隔离上下文里执行委派给你的子任务。

规则:
- 只处理委派给你的具体任务,不要扩展范围
- 完成后返回执行摘要(做了什么/结果/遇到的问题)
- 你不能再用 Task 工具委派子代理(单层限制)`,
}

// D35 自定义子代理定义(从 agent_hooks/agents 表加载,仿 Claude Code .md frontmatter)。
type CustomSubagentDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`        // D36 工具作用域(限制可用工具)
	Model       string   `json:"model"`        // 可指定模型(空=继承)
	MaxTurns    int      `json:"max_turns"`
	Instructions string   `json:"instructions"` // system prompt
}

// D35 从 agent 定义加载自定义子代理(从 agents 表查 adapter=custom 的记录)。
func LoadCustomSubagents() map[string]CustomSubagentDef {
	defs := make(map[string]CustomSubagentDef)
	// 内置三个类型始终可用
	defs["Explore"] = CustomSubagentDef{
		Name: "Explore", Description: "只读搜索,快速 fan-out",
		Tools: []string{"FileRead", "Glob", "Grep", "WebSearch"},
		MaxTurns: 4, Instructions: subagentSystemPrompts[SubagentExplore],
	}
	defs["Plan"] = CustomSubagentDef{
		Name: "Plan", Description: "规划,产出结构化计划",
		Tools: []string{"FileRead", "Glob", "Grep"},
		MaxTurns: 4, Instructions: subagentSystemPrompts[SubagentPlan],
	}
	defs["General"] = CustomSubagentDef{
		Name: "General", Description: "通用执行",
		Tools: []string{}, // 空=继承全部
		MaxTurns: 6, Instructions: subagentSystemPrompts[SubagentGeneral],
	}
	return defs
}

// D37 递归深度限制(可配置,默认单层)。
var maxSubagentDepth = 1 // 默认单层(子代理不能再 spawn 子代理)

// SetMaxSubagentDepth 设置递归深度限制。
func SetMaxSubagentDepth(depth int) {
	if depth >= 1 {
		maxSubagentDepth = depth
	}
}

// D36 构建子代理的工具集(按 CustomSubagentDef.Tools 过滤)。
func filterSubagentTools(allTools []ToolDef, allowed []string) []ToolDef {
	if len(allowed) == 0 {
		return allTools // 空=继承全部
	}
	allowedSet := make(map[string]bool)
	for _, t := range allowed {
		allowedSet[t] = true
	}
	var filtered []ToolDef
	for _, t := range allTools {
		if allowedSet[t.Function.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// 4.3.2 removeTools 从工具集中移除指定工具(用于 Fork 递归 guard)。
func removeTools(allTools []ToolDef, excluded []string) []ToolDef {
	excludeSet := make(map[string]bool)
	for _, name := range excluded {
		excludeSet[name] = true
	}
	var result []ToolDef
	for _, t := range allTools {
		if !excludeSet[t.Function.Name] {
			result = append(result, t)
		}
	}
	return result
}

// RunSubagent 在隔离上下文跑子代理的 ReAct loop,返回摘要。
//
// 关键设计(上下文防火墙):
//   - 子代理用自己的 system prompt(不继承主 agent 的 system prompt)
//   - 子代理看不到主 agent 的对话历史,只看到委派的 prompt
//   - 子代理完成后只返回最终文本摘要给主 agent
//   - 子代理的工具集受限(Explore/Plan 只读,General 无 Task)
//
// model: 子代理用的模型(留空用主 agent 同款)。4.4.3 当 CustomSubagentDef 指定 model 时覆盖。
// maxTurns: 子代理最大轮数(默认 4,比主 agent 短)
// tools: 4.4.2 允许的工具名列表(空=继承全部)。通过 filterSubagentTools 过滤。
func RunSubagent(subType SubagentType, prompt, model string, maxTurns int, taskID string, tools []string) string {
	if maxTurns <= 0 {
		maxTurns = 4 // 子代理轮数比主 agent 短
	}

	// 4.4.3 从自定义子代理定义中查找模型覆盖
	effectiveModel := model
	defs := LoadCustomSubagents()
	if def, ok := defs[string(subType)]; ok {
		if def.Model != "" && model == "" {
			effectiveModel = def.Model // 优先用自定义定义的模型
		}
		if len(tools) == 0 && len(def.Tools) > 0 {
			tools = def.Tools // 自定义定义的工具集
		}
	}

	// 构造子代理的 Agent 配置(独立 system prompt,受限工具)
	systemPrompt := subagentSystemPrompts[subType]
	if systemPrompt == "" {
		systemPrompt = subagentSystemPrompts[SubagentGeneral]
	}

	// 用一个临时 AgentRow 跑 ReAct loop
	subAgent := store.AgentRow{
		ID:              "subagent-" + string(subType),
		Name:            string(subType),
		Model:           effectiveModel,
		SystemPrompt:    systemPrompt,
		MaxTurns:        maxTurns,
		PermissionLevel: "read-only", // 子代理默认只读(安全)
	}

	// 4.4.2 过滤子代理工具集
	// 通过 extraTools 传入过滤后的工具集(空=继承全部内置工具)
	var extraTools []ToolDef
	if len(tools) > 0 {
		extraTools = filterSubagentTools(BuiltinTools(), tools)
	}

	// 子代理的工具集:只读工具(Explore/Plan),General 加 BashTool 但无 Task
	result := RunAgentLoop(subAgent, prompt, taskID, extraTools)
	if result.Text == "" {
		return fmt.Sprintf("[子代理 %s 无输出]", subType)
	}
	return result.Text
}

// TaskToolDef 返回 Task 工具声明(主 agent 用它委派子代理)。
func TaskToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
		Name:        "Task",
		Description: "委派一个子代理在隔离上下文执行子任务。子代理有自己的上下文窗口,完成后返回摘要(防主上下文膨胀)。类型:Explore(只读搜索)/Plan(规划)/General(通用执行)/Fork(全上下文继承)。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"subagent_type": {"type": "string", "enum": ["Explore", "Plan", "General", "Fork"], "description": "子代理类型"},
				"prompt": {"type": "string", "description": "委派给子代理的任务描述(自包含,子代理看不到主对话历史,Fork 模式除外)"}
				},
				"required": ["subagent_type", "prompt"]
			}`),
		},
	}
}

// ExecuteTask 执行 Task 工具:启动子代理（4.3 支持 Fork 模式）。
//
// 主 agent 调 Task(subagent_type="Explore", prompt="...") → 启动子代理。
// subagent_type="Fork" → 全上下文继承（复制主 agent 的对话历史）。
//
// modelRef: 主 agent 的模型(子代理复用)
// taskID: 状态推送用
// depth: 4.5.1 当前递归深度(0=顶层,每 spawn 子代理 +1)
func ExecuteTask(args json.RawMessage, modelRef, taskID string, depth int) ToolResult {
	// 4.5.1 递归深度限制检查
	if depth > maxSubagentDepth {
		return ToolResult{Name: "Task", Output: fmt.Sprintf(
			"[子代理深度超限(depth=%d, max=%d)。禁止递归 spawn 子代理。]",
			depth, maxSubagentDepth), IsError: true}
	}

	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return ToolResult{Name: "Task", Output: "参数解析失败", IsError: true}
	}
	subType, _ := argsMap["subagent_type"].(string)
	prompt, _ := argsMap["prompt"].(string)
	if prompt == "" {
		return ToolResult{Name: "Task", Output: "缺少 prompt 参数", IsError: true}
	}

	// 4.3 Fork 模式:从 args 获取主 agent 的 messages 快照
	var forkMessages []loopMessage
	if msgsRaw, ok := argsMap["_fork_messages"]; ok {
		if msgsJSON, _ := json.Marshal(msgsRaw); msgsJSON != nil {
			json.Unmarshal(msgsJSON, &forkMessages)
		}
	}

	SetAgentStatus("main-agent", StatusCallingTool, "委派子代理 "+subType, taskID)

	// 4.1 独立 breaker:子代理有自己的 token/cost 限制
	subBreaker := NewBreakerState(BreakerConfig{
		Enabled: true, MaxTokens: 20000, MaxTurns: 10, MaxCostUSD: 1.0, CostPer1K: 0.002,
	})

	// #31 创建持久化 sidechain 记录(支持后续 resume)
	rec := GetSubagentManager().SpawnSidechain(
		subType, "Task: "+truncate(prompt, 60), prompt, "call-task", taskID)

	start := time.Now()
	var summary string

	// 4.5 子代理总超时（默认 120s）
	subTimeout := 120 * time.Second
	resultCh := make(chan string, 1)
	go func() {
		if subType == "Fork" && len(forkMessages) > 0 {
			// 4.3 Fork:全上下文继承
			summary = RunForkSubagent(prompt, modelRef, forkMessages, subBreaker, taskID)
		} else {
			// Coordinator:零上下文继承
			summary = RunSubagent(SubagentType(subType), prompt, modelRef, 4, taskID, nil)
		}
		resultCh <- summary
	}()

	select {
	case summary = <-resultCh:
	case <-time.After(subTimeout):
		summary = fmt.Sprintf("[子代理 %s 超时(%v)]", subType, subTimeout)
		NewSlog("subagent").Warn("超时()", "subType", subType, "subTimeout", subTimeout)
	}

	elapsed := time.Since(start)

	// #31 标记完成 + 存 transcript
	GetSubagentManager().CompleteSidechain(rec.AgentID, summary, ToolStats{},
		int(elapsed.Milliseconds()), CountTokens(summary))

	// #31 附 resume hint + 子代理统计
	resumeHint := GetSubagentManager().GetResumeHint(rec.AgentID, CountTokens(summary), int(elapsed.Milliseconds()))
	subStats := fmt.Sprintf("\n[duration: %dms | tokens: %d | cost: $%.4f]",
		elapsed.Milliseconds(), subBreaker.TotalTokens, subBreaker.EstCostUSD)

	return ToolResult{Name: "Task", Output: summary + "\n\n" + resumeHint + subStats}
}

// 4.2.2 ExecuteTaskAsync 异步执行 Task 工具,立即返回 sidechain_id。
// 调用方稍后可通过 GetSidechainResult(id) 轮询结果。
// 4.2.3 notifyCallback:子代理完成时的回调函数(可为 nil)。
//
// 返回值:(sidechain_id, 初始 ToolResult)
func ExecuteTaskAsync(args json.RawMessage, modelRef, taskID string, depth int,
	notifyCallback func(result string)) (string, ToolResult) {

	// 4.5.1 递归深度限制检查
	if depth > maxSubagentDepth {
		return "", ToolResult{Name: "Task", Output: fmt.Sprintf(
			"[子代理深度超限(depth=%d, max=%d)]", depth, maxSubagentDepth), IsError: true}
	}

	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return "", ToolResult{Name: "Task", Output: "参数解析失败", IsError: true}
	}
	subType, _ := argsMap["subagent_type"].(string)
	prompt, _ := argsMap["prompt"].(string)
	if prompt == "" {
		return "", ToolResult{Name: "Task", Output: "缺少 prompt 参数", IsError: true}
	}

	// 创建 sidechain 记录
	rec := GetSubagentManager().SpawnSidechain(
		subType, "Task(async): "+truncate(prompt, 60), prompt, "call-task", taskID)

	// 4.2 Fork 消息提取
	var forkMessages []loopMessage
	if msgsRaw, ok := argsMap["_fork_messages"]; ok {
		if msgsJSON, _ := json.Marshal(msgsRaw); msgsJSON != nil {
			json.Unmarshal(msgsJSON, &forkMessages)
		}
	}

	// 4.2.2 异步启动子代理(goroutine),立即返回 sidechain_id
	go func() {
		subBreaker := NewBreakerState(BreakerConfig{
			Enabled: true, MaxTokens: 20000, MaxTurns: 10, MaxCostUSD: 1.0, CostPer1K: 0.002,
		})

		start := time.Now()
		var summary string
		if subType == "Fork" && len(forkMessages) > 0 {
			summary = RunForkSubagent(prompt, modelRef, forkMessages, subBreaker, taskID)
		} else {
			summary = RunSubagent(SubagentType(subType), prompt, modelRef, 4, taskID, nil)
		}
		elapsed := time.Since(start)

		// 标记完成
		GetSubagentManager().CompleteSidechain(rec.AgentID, summary, ToolStats{},
			int(elapsed.Milliseconds()), CountTokens(summary))

		// 4.2.3 完成时调用回调
		if notifyCallback != nil {
			notifyCallback(summary)
		}
	}()

	return rec.AgentID, ToolResult{
		Name:   "Task",
		Output: fmt.Sprintf("[子代理 %s 已异步启动,agentId: %s。用 GetSidechainResult(%s) 轮询结果。]", subType, rec.AgentID, rec.AgentID),
	}
}

// ── 4.3 Fork 模式 ──

// 4.3.1 serializeParentMessages 将父 agent 的对话历史序列化为可读字符串。
// Fork 子代理通过继承这些上下文来延续主 agent 的工作。
func serializeParentMessages(msgs []loopMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role == "system" {
			continue // 跳过 system message(Fork 子代理有自己的)
		}
		content := m.Content
		if len(content) > 2000 {
			content = content[:2000] + "...(截断)"
		}
		// 标注工具调用
		if len(m.ToolCalls) > 0 {
			var toolNames []string
			for _, tc := range m.ToolCalls {
				toolNames = append(toolNames, tc.Name)
			}
			sb.WriteString(fmt.Sprintf("[%s]: (调用工具 %s)\n", m.Role, strings.Join(toolNames, ", ")))
		} else if content != "" {
			sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
		}
	}
	return sb.String()
}

// RunForkSubagent Fork 模式子代理(全上下文继承,单层)。
// 子代理继承主 agent 的全部对话历史,在隔离的 ReAct loop 里继续执行。
// 4.3.1 将 parentMessages 编码到 prompt 中实现上下文继承。
// 4.3.2 程序化移除 Task 工具(防递归,不仅靠 prompt 指令)。
func RunForkSubagent(prompt string, modelRef string, parentMessages []loopMessage,
	breaker *BreakerState, taskID string) string {

	// 构建子 agent(继承主 agent 对话 + 添加新任务)
	subAgent := store.AgentRow{
		ID:              "subagent-Fork",
		Name:            "Fork",
		Model:           modelRef,
		SystemPrompt:    "你是 Fork 子代理。你继承了主 agent 的上下文,请在此基础上完成委派的任务。你不能再用 Task 工具创建子代理。",
		MaxTurns:        6,
		PermissionLevel: "workspace-write", // Fork 继承主 agent 权限
	}

	// 4.3.1 将父上下文消息传入 Fork 子代理(全上下文继承)
	// 编码父对话历史到 prompt 中,使子代理能看到之前发生的事情
	fullPrompt := prompt
	if len(parentMessages) > 0 {
		parentCtx := serializeParentMessages(parentMessages)
		fullPrompt = fmt.Sprintf("【继承的父上下文】\n%s\n---\n【当前任务】\n%s", parentCtx, prompt)
	}

	// 4.3.2 程序化移除 Task 工具(递归 guard,防 context 指数膨胀)
	// 不仅依赖 prompt 指令,直接从工具集中移除 Task
	allTools := BuiltinTools()
	forkTools := removeTools(allTools, []string{"Task"})

	result := RunAgentLoop(subAgent, fullPrompt, taskID, forkTools)
	if result.Text == "" {
		return "[Fork 子代理无输出]"
	}
	return result.Text
}

// ── 4.2 SendMessage 工具 ──

// SendMessageToolDef 返回 SendMessage 工具声明（主 agent 用它续聊子代理）。
func SendMessageToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "SendMessage",
			Description: "向已存在的子代理发送续聊消息。子代理会在之前的上下文基础上继续工作。用 to 指定目标子代理的 agentId(来自 Task 工具返回的 resume hint)。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"to": {"type": "string", "description": "目标子代理的 agentId(从上次 Task 返回的 resume hint 获取)"},
					"message": {"type": "string", "description": "发送给子代理的消息"}
				},
				"required": ["to", "message"]
			}`),
		},
	}
}

// ExecuteSendMessage 执行 SendMessage 工具(4.2 stateful resume)。
func ExecuteSendMessage(args json.RawMessage, modelRef, taskID string) ToolResult {
	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return ToolResult{Name: "SendMessage", Output: "参数解析失败", IsError: true}
	}
	to, _ := argsMap["to"].(string)
	message, _ := argsMap["message"].(string)
	if to == "" || message == "" {
		return ToolResult{Name: "SendMessage", Output: "缺少 to 或 message 参数", IsError: true}
	}

	// 检查目标是否可续聊
	if !GetSubagentManager().IsAlive(to) {
		return ToolResult{Name: "SendMessage", Output: fmt.Sprintf(
			"子代理 %s 不存在或已结束。请检查 agentId 是否正确。", to), IsError: true}
	}

	// 发送消息到子代理
	rec, err := GetSubagentManager().SendMessage(to, message)
	if err != nil {
		return ToolResult{Name: "SendMessage", Output: err.Error(), IsError: true}
	}

	// 重新跑子代理(用已有的 transcript)
	summary := RunSubagent(SubagentType(rec.AgentType), message, modelRef, 4, taskID, nil)

	return ToolResult{Name: "SendMessage",
		Output: fmt.Sprintf("[子代理 %s 回复]\n%s", to, summary)}
}

// ── 4.4 从 DB 加载自定义子代理 ──

// LoadCustomSubagentsFromDB 从 agents 表加载 adapter=custom 的自定义子代理定义。
func LoadCustomSubagentsFromDB() map[string]CustomSubagentDef {
	defs := LoadCustomSubagents() // 先加载内置类型

	if store.DB == nil {
		return defs
	}

	rows, err := store.DB.Query(`SELECT id, name, COALESCE(description,''), COALESCE(tools,'[]'),
		COALESCE(model,''), COALESCE(max_turns,4), COALESCE(system_prompt,'')
		FROM agents WHERE adapter = 'custom'`)
	if err != nil {
		return defs
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, desc, toolsJSON, model string
		var maxTurns int
		var sysPrompt string
		rows.Scan(&id, &name, &desc, &toolsJSON, &model, &maxTurns, &sysPrompt)

		var tools []string
		json.Unmarshal([]byte(toolsJSON), &tools)

		defs[id] = CustomSubagentDef{
			Name:         name,
			Description:  desc,
			Tools:        tools,
			Model:        model,
			MaxTurns:     maxTurns,
			Instructions: sysPrompt,
		}
	}
	return defs
}
