package engine

// agent_loop.go — harness 第2层核心:ReAct 工具调用循环(agent loop)。
//
// 这是整个 harness 的心脏。把 ChatAgent 从"单轮文本对话"升级为"带工具的 ReAct 循环":
//
//   传 tools 给 LLM → 检测 tool_calls → 执行工具 → 结果回填 → 继续对话
//   直到模型无 tool_calls(任务完成)或达 maxTurns。
//
// 这是 Claude Code / codex / OpenHands 的核心模式。
// 研究依据:
//   - Claude Code 本地架构(.claude/):agent_listing_delta + skill_listing 动态注入 + sidechain
//   - harness-engineering 仓库:Agent = Model + Harness,六杠杆之"编排逻辑(子代理生成/handoff)"
//   - good-skills/05-agent-loop-engine:四旋钮(maxTurns/budget/compact/structuredRetry)
//
// 向后兼容:无 tools 的 agent 走单轮模式(callOpenAICompatibleWithSystem)。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// ContentPart 多模态内容片段(2.5.1: 支持 OpenAI content 数组格式)。
// 当消息包含图片或多模态内容时,ContentParts 替代纯文本 Content。
type ContentPart struct {
	Type     string `json:"type"`                // "text" | "image_url"
	Text     string `json:"text,omitempty"`      // type=text 时的文本内容
	ImageURL *struct {
		URL string `json:"url"` // 图片 URL(base64 或 http)
	} `json:"image_url,omitempty"` // type=image_url 时的图片
}

// loopMessage ReAct 循环里的消息(兼容 OpenAI messages 格式)。
type loopMessage struct {
	Role         string        `json:"role"`                    // system | user | assistant | tool
	Content      string        `json:"content,omitempty"`
	ContentParts []ContentPart `json:"-"`                       // 2.5.1: 多模态内容(自定义序列化)
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`   // assistant 发起的工具调用
	ToolCallID   string        `json:"tool_call_id,omitempty"` // tool 角色回填时关联的 id
	Name         string        `json:"name,omitempty"`         // tool 角色的工具名
}

// MarshalJSON 自定义序列化:当 ContentParts 非空时,content 输出为数组而非字符串。
// 这是 OpenAI multimodal messages 的标准格式(2.5.1)。
func (lm loopMessage) MarshalJSON() ([]byte, error) {
	type raw loopMessage // 避免递归
	if len(lm.ContentParts) > 0 {
		return json.Marshal(struct {
			Content json.RawMessage `json:"content"`
			raw
		}{
			Content: mustMarshalJSON(lm.ContentParts),
			raw:     raw(lm),
		})
	}
	// 纯文本:content 输出为字符串(向后兼容)
	return json.Marshal(struct {
		raw
	}{raw: raw(lm)})
}

// mustMarshalJSON 辅助:序列化失败时返回 null。
func mustMarshalJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// RunAgentLoop 执行 ReAct 工具调用循环。
//
// 参数:
//   - ctx: 上下文（nil → context.Background），外部可通过 cancel 中断循环
//   - agent: Agent 配置(含 SystemPrompt/AgentsMD/MaxTurns/权限/搜索配置)
//   - prompt: 用户/题目
//   - taskID: 状态推送用
//   - extraTools: 额外工具(MCP + skills 暴露的工具)
//
// 若 agent 无 tools 配置(MaxTurns<=0 或无工具),退化为单轮模式(向后兼容)。
func RunAgentLoop(agent store.AgentRow, prompt, taskID string, extraTools []ToolDef) ChatResult {
	// 2.7 context 支持：nil → Background
	return runAgentLoopWithContext(context.Background(), agent, prompt, taskID, extraTools)
}

// RunAgentLoopWithContext 带 context 的 ReAct 循环（2.7：支持中断/取消）。
func RunAgentLoopWithContext(ctx context.Context, agent store.AgentRow, prompt, taskID string, extraTools []ToolDef) ChatResult {
	if ctx == nil {
		ctx = context.Background()
	}
	return runAgentLoopWithContext(ctx, agent, prompt, taskID, extraTools)
}

// runAgentLoopWithContext 内部实现（统一入口）。
func runAgentLoopWithContext(ctx context.Context, agent store.AgentRow, prompt, taskID string, extraTools []ToolDef) ChatResult {
	agentID := agent.ID
	if agentID == "" {
		agentID = agent.Name
	}

	// 查端点
	modelRef := agent.Model
	if modelRef == "" {
		modelRef = "unknown"
	}
	baseURL, apiKey, realName, apiFormat := lookupModelEndpoint(modelRef)
	if baseURL == "" || apiKey == "" {
		NewSlog("agent_loop").Warn("agent 无可用 Key,使用 mock 回退", "taskID", taskID)
		SetAgentStatus(agentID, StatusError, "无可用 API Key", taskID)
		return mockAnswer(agent, prompt, realName)
	}

	// 收集工具(内置 + LoadSkill + Task子代理 + Verify + apply_patch + TodoWrite + 额外 + MCP server)
		tools := append(BuiltinTools(),
			LoadSkillToolDef(), TaskToolDef(), VerifyToolDef(),
			ApplyPatchToolDef(), TodoWriteToolDef(), SendMessageToolDef(), ToolSearchToolDef())
	tools = append(tools, extraTools...)
	// 加载 MCP server 工具(连接失败静默跳过,降级为无 MCP)
	// #24 tool-search:MCP 工具走 deferred(用 tool_search 按需加载)
	mcpTools, mcpClients, _ := LoadMCPToolsForAgent()
	if len(mcpTools) > 0 {
		tools = append(tools, mcpTools...)
	}

	// 无工具 → 单轮模式(向后兼容)
	maxTurns := agent.MaxTurns
	if maxTurns <= 0 || len(tools) == 0 {
		SetAgentStatus(agentID, StatusSpawning, "初始化(单轮模式)", taskID)
		// 联网搜索(若配置)
		enhancedPrompt := prompt
		if agent.SearchProvider != "" && agent.SearchProvider != "none" && agent.SearchAPIKey != "" {
			SetAgentStatus(agentID, StatusSearching, "联网搜索", taskID)
			ctx := SearchAndInject(agent.SearchProvider, agent.SearchAPIKey, prompt)
			if ctx != "" {
				enhancedPrompt = ctx + "\n\n" + prompt
			}
		}
		SetAgentStatus(agentID, StatusThinking, "LLM 推理中", taskID)
		result, err := callOpenAICompatibleWithSystem(baseURL, apiKey, apiFormat, realName, agent.SystemPrompt, enhancedPrompt)
		if err != nil {
			SetAgentStatus(agentID, StatusError, err.Error(), taskID)
			return mockAnswer(agent, prompt, realName)
		}
		SetAgentStatus(agentID, StatusDone, "完成", taskID)
		return result
	}

	// ── ReAct 循环 ──
	if maxTurns == 0 {
		maxTurns = 8
	}

	// #23 AGENTS.md 多层发现:从文件系统逐层收集(cwd→.git root),叠加 agent 专属 agents_md
	discoveredMD := GetAgentsMDForCWD(".") // 从当前目录向上发现
	agentsMDContent := agent.AgentsMD
	if discoveredMD != "" {
		if agentsMDContent != "" {
			agentsMDContent = discoveredMD + "\n\n--- agent-specific ---\n\n" + agentsMDContent
		} else {
			agentsMDContent = discoveredMD
		}
	}
	// 构建初始 messages(system prompt 含 AGENTS.md + skill 清单)
	systemContent := BuildSkillSystemPrompt(agent.SystemPrompt, agentsMDContent, agent.SkillIDs)

	// #26 GoalManager:注入目标提醒(若 taskID 有对应任务)
	goalPrompt := ""
	if taskID != "" {
		if task, gerr := store.GetTask(taskID); gerr == nil && task != nil {
			gm := NewGoalManager("完成评测任务: "+task.Name, 100000, 600)
			goalPrompt = gm.RenderGoalPrompt()
		}
	}
	if goalPrompt != "" {
		systemContent += "\n\n" + goalPrompt
	}

	messages := []loopMessage{
		{Role: "system", Content: systemContent},
		{Role: "user", Content: prompt},
	}

	// 加载 agent 的 hooks 配置(harness 第5层,提前创建供 SessionStart/UserPromptSubmit 用)
	hookRunner := NewHookRunner(agentID)

	// 权限级别 + 工作区(工具执行用,提前定义供 #37 权限模式用)
	permLevel := agent.PermissionLevel
	if permLevel == "" {
		permLevel = "workspace-write"
	}
	workspaceRoot := "."

	// #37 权限模式:从 agent 配置构建 harness 权限(plan 模式→只读,bypass→跳过审批)
	harnessPerm := DefaultHarnessPermission()
	if permLevel == "read-only" {
		harnessPerm.Mode = ModePlan // read-only agent 映射到 plan 模式(只读)
	}

	// #43 事件流:emit 启动事件
	GetEventStream().EmitMessage(taskID, "Agent "+agent.Name+" 启动")
	// #36 SessionStart hook
	hookRunner.OnSessionStart()
	// #36 UserPromptSubmit hook
	hookRunner.OnUserPromptSubmit(prompt)

	// 联网搜索预注入
	if agent.SearchProvider != "" && agent.SearchProvider != "none" && agent.SearchAPIKey != "" {
		SetAgentStatus(agentID, StatusSearching, "联网搜索", taskID)
		ctx := SearchAndInject(agent.SearchProvider, agent.SearchAPIKey, prompt)
		if ctx != "" {
			messages = append(messages, loopMessage{
				Role: "user", Content: "【联网搜索参考】\n" + ctx,
			})
		}
	}

	// 工具集 JSON(给请求体)
	toolsJSON := ToolsToJSON(tools)

	// 循环
	var lastText string
	totalTokenIn, totalTokenOut := 0, 0
	consecutiveErrors := 0 // 2.1: 连续同类型错误计数

	// 2.2: per-turn 预算分配
	tokenBudget := 80000 // 总预算
	turnsRemaining := maxTurns

	for turn := 0; turn < maxTurns; turn++ {
		// 2.7: 检查外部中断
		select {
		case <-ctx.Done():
			NewSlog("agent_loop").Warn("第 轮被取消", "taskID", taskID, "turn1", turn+1)
			SetAgentStatus(agentID, StatusError, "被取消", taskID)
			// 2.7.2: 中断时保存检查点(供后续恢复)
			if len(messages) > 2 {
				SaveAgentCheckpoint(messages, agent.Name, taskID, totalTokenIn, totalTokenOut, turn+1)
			}
			if lastText != "" {
				return ChatResult{Text: lastText, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
			}
			return mockAnswer(agent, prompt, realName)
		default:
		}

		SetAgentStatus(agentID, StatusThinking, fmt.Sprintf("第 %d/%d 轮推理", turn+1, maxTurns), taskID)

	// 调 LLM(带 tools) — taskID 非空时走 SSE 流式推送
	resp, err := callLLMWithToolsStream(baseURL, apiKey, apiFormat, realName, messages, toolsJSON, taskID)
	if err != nil {
			// 2.1 错误分类
			errType := classifyAgentError(err)
			NewSlog("agent_loop").Error("第 轮调用失败()", "taskID", taskID, "turn1", turn+1, "errType", errType)
			SetAgentStatus(agentID, StatusError, fmt.Sprintf("第%d轮失败(%s): %v", turn+1, errType, err), taskID)

			switch errType {
			case errRetryable:
				// 2.8.2: token 超限时先压缩消息历史,再降级纯文本重试
				if strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "length") {
					// 先尝试压缩消息(释放 token 空间)
					compacted := CompactAgentMessages(messages, taskID, tokenBudget, CondenserRecent)
					if len(compacted) < len(messages) {
						NewSlog("agent_loop").Info("2.8.2 token 超限,压缩消息 → 条后重试", "taskID", taskID, "messages_count", len(messages))
						messages = compacted
						continue // 用压缩后的消息重试(不消耗 turn)
					}
					// 压缩无效,降级纯文本
					NewSlog("agent_loop").Info("疑似 token 超限，尝试无工具纯文本重试")
					resp2, err2 := callLLMWithoutTools(baseURL, apiKey, apiFormat, realName, messages)
					if err2 == nil {
						consecutiveErrors = 0
						lastText = resp2.Text
						totalTokenIn += resp2.TokenIn
						totalTokenOut += resp2.TokenOut
						SetAgentStatus(agentID, StatusDone, fmt.Sprintf("完成(降级纯文本,共 %d 轮)", turn+1), taskID)
						return ChatResult{Text: resp2.Text, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
					}
				}
				// 可重试：告诉 agent 上一步失败，不占 turn
				consecutiveErrors++
				if consecutiveErrors >= 3 {
					// 2.1.4: 连续 3 次失败 → 提前退出（避免死循环）
					NewSlog("agent_loop").Error("连续 次失败，提前退出", "taskID", taskID)
					if lastText != "" {
						return ChatResult{Text: lastText, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
					}
					return mockAnswer(agent, prompt, realName)
				}
				// 回填错误消息让 agent 知道失败（不占原本的 assistant message）
				messages = append(messages, loopMessage{
					Role: "tool", Content: fmt.Sprintf("[系统: 上一轮 LLM 调用失败(%v),请重试]", err),
					ToolCallID: fmt.Sprintf("err-%d", turn),
				})
				continue // 不消耗 turn（agent 需要纠正）
			case errFatal:
				if lastText != "" {
					return ChatResult{Text: lastText, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
				}
				return mockAnswer(agent, prompt, realName)
			case errMalformed:
				// 2.1.3: 畸形 JSON → 注入纠正提示,让 LLM 重新生成合法 tool_calls
				NewSlog("agent_loop").Info("第 轮响应 JSON 畸形,注入纠正提示", "taskID", taskID)
				messages = append(messages, loopMessage{
					Role:    "system",
					Content: fmt.Sprintf("[系统: 上一轮响应 JSON 格式错误(%v)。请用合法的 OpenAI tool_calls JSON 格式重试。]", err),
				})
				continue // 不消耗 turn,给 agent 纠正机会
			default:
				// 其他未知错误,退出
				if lastText != "" {
					return ChatResult{Text: lastText, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
				}
				return mockAnswer(agent, prompt, realName)
			}
		}

		// 成功 → 重置连续错误计数
		consecutiveErrors = 0

		totalTokenIn += resp.TokenIn
		totalTokenOut += resp.TokenOut
		turnsRemaining--

		// 2.2.1: token 预算强制执行(超限 → 中断循环)
		if totalTokenIn >= tokenBudget {
			NewSlog("agent_loop").Info("2.2 token 预算耗尽: /, 剩余 轮, 强制中断", "taskID", taskID, "totalTokenIn", totalTokenIn, "tokenBudget", tokenBudget)
			break
		}

		// 2.2.2: per-turn 预算分配(按剩余轮次均分)
		perTurnBudget := tokenBudget / max(turnsRemaining, 1)

		// 2.2.3: 低预算模式 — 剩余预算不足 20% 时警告(提示可能需要 prompt 精简)
		if totalTokenIn > tokenBudget*80/100 && turn < maxTurns-1 {
			NewSlog("agent_loop").With("task_id", taskID).Warn("低预算模式",
				"used_tokens", totalTokenIn, "budget", tokenBudget,
				"used_pct", float64(totalTokenIn)/float64(tokenBudget)*100,
				"per_turn_budget", perTurnBudget)
		}

		// #52 双路统计:计入 agent metrics(含 cache 维度)
		GetDualTokenTracker().RecordAgentTurn(TokenUsage{
			PromptTokens:     resp.TokenIn,
			CompletionTokens: resp.TokenOut,
		}, 0, 0)
		lastText = resp.Text

		// WebSocket 实时流：推送增量文本
		// 只推新文本（和上一轮相比的增量）
		if taskID != "" && resp.Text != "" {
			prevText := ""
			for _, m := range messages {
				if m.Role == "assistant" && m.Content != "" {
					prevText += m.Content + "\n"
				}
			}
			if len(prevText) < len(resp.Text) {
				delta := resp.Text[len(prevText):]
				GetEventStream().EmitMessage(taskID, delta)
			} else if prevText == "" {
				GetEventStream().EmitMessage(taskID, resp.Text)
			}
		}

		// 无工具调用 → 任务完成
		if len(resp.ToolCalls) == 0 {
			// ── 第6层 Back-Pressure 自我验证回路 ──
			// Agent 配了 verify_command 时,在返回前跑验证:
			//   通过 → 正常返回
			//   失败 → 注入验证错误反馈,让 agent 再修,最多重试 2 次
			if agent.VerifyCommand != "" {
				verifyCfg := VerifyConfig{Command: agent.VerifyCommand, RetryLimit: 2, WorkspaceRoot: workspaceRoot}
				vResult, _ := VerifyWithRetry(verifyCfg, messages,
					func(msgs []loopMessage) (string, error) {
						SetAgentStatus(agentID, StatusThinking, "验证失败,修正中", taskID)
						r, e := callLLMInternal(baseURL, apiKey, apiFormat, realName, msgs, toolsJSON, "")
						return r.Text, e
					})
				NewSlog("agent_loop").Info("back-pressure 验证: passed= retries=", "taskID", taskID, "Passed", vResult.Passed)
			}
			// #36 hooks:Stop + SessionEnd
			hookRunner.OnStop()
			hookRunner.OnSessionEnd()
			// #43 事件流:emit 完成
			GetEventStream().EmitStateUpdate(taskID, "execution_status", "finished")
			GetEventStream().EmitMessage(taskID, resp.Text[:min(200, len(resp.Text))])
			SetAgentStatus(agentID, StatusDone, fmt.Sprintf("完成(共 %d 轮)", turn+1), taskID)
			return ChatResult{Text: resp.Text, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
		}

		// 有工具调用 → 执行 + 回填
		// 先把 assistant 的 tool_calls 加入 messages
		messages = append(messages, loopMessage{
			Role:      "assistant",
			Content:   resp.Text,
			ToolCalls: resp.ToolCalls,
		})

		// 2.4: 并行执行工具（无依赖的只读工具并发，写操作串行）
		// 先做 hook + guardian 审批，然后并行/串行执行

		// 预处理：收集所有工具的 hook 决策和权限检查
		type toolExecPlan struct {
			tc          ToolCall
			allow       bool
			rejectMsg   string
			toolInput   any
			toolInputMap map[string]any
		}
		plans := make([]toolExecPlan, 0, len(resp.ToolCalls))

		for _, tc := range resp.ToolCalls {
			var toolInput any
			json.Unmarshal(tc.Arguments, &toolInput)

			plan := toolExecPlan{tc: tc, allow: true, toolInput: toolInput}
			plan.toolInputMap, _ = toolInput.(map[string]any)

			// PreToolUse hook
			preDecision := hookRunner.OnPreToolUse(tc.Name, toolInput)
			if !preDecision.Allow {
				plan.allow = false
				plan.rejectMsg = "[hook 拒绝] " + preDecision.Message
				GetEventStream().EmitHookExecution(taskID, "PreToolUse", preDecision.Message, true, 2)
				NewSlog("agent_loop").Warn("turn 工具 被 hook 拒绝", "taskID", taskID, "turn1", turn+1, "Name", tc.Name)
				plans = append(plans, plan)
				continue
			}

			// Guardian 审批
			if tc.Name == "BashTool" {
				var cmdStr string
				if cmdMap, ok := toolInput.(map[string]any); ok {
					cmdStr, _ = cmdMap["command"].(string)
				}
				if isDangerousCommand(cmdStr) {
					guardian := NewGuardianApprover()
					decision := guardian.Approve(
						map[string]any{"type": "shell", "command": cmdStr},
						truncate(prompt, 500), "medium")
					if decision.Outcome == "deny" {
						plan.allow = false
						plan.rejectMsg = "[Guardian 拒绝] " + decision.Rationale
						NewSlog("agent_loop").Warn("#17 Guardian 拒绝命令: ()", "taskID", taskID, "cmdStr", cmdStr)
					}
				}
			}

			// #37 权限模式
			if harnessPerm.Mode == ModePlan {
				if tc.Name == "FileWrite" || tc.Name == "BashTool" || tc.Name == "apply_patch" {
					plan.allow = false
					plan.rejectMsg = "[plan 模式] 只读权限禁止写操作"
				}
			}

			plans = append(plans, plan)
		}

		// 分离可执行和被拒绝的工具
		var executablePlans []toolExecPlan
		for _, plan := range plans {
			if !plan.allow {
				// 被拒绝：直接回填拒绝消息
				result := ToolResult{Name: plan.tc.Name, Output: plan.rejectMsg, IsError: true}
				resultJSON, _ := json.Marshal(result)
				messages = append(messages, loopMessage{
					Role: "tool", Content: string(resultJSON), ToolCallID: plan.tc.ID, Name: plan.tc.Name,
				})
			} else {
				executablePlans = append(executablePlans, plan)
			}
		}

		// 2.4: 并行执行可执行工具
		if len(executablePlans) > 0 {
			// 提取 tool calls
			execCalls := make([]ToolCall, len(executablePlans))
			for i, plan := range executablePlans {
				execCalls[i] = plan.tc
			}

		SetAgentStatus(agentID, StatusCallingTool, fmt.Sprintf("执行 %d 个工具", len(execCalls)), taskID)

			// 构建执行请求
			execReq := toolExecRequest{
				PermLevel:     permLevel,
				Workspace:     workspaceRoot,
				SearchProv:    agent.SearchProvider,
				SearchKey:     agent.SearchAPIKey,
				AgentID:       agentID,
				TaskID:        taskID,
				VerifyCommand: agent.VerifyCommand, // 第6层:Agent 配置的自我验证命令
				MCPClients:    mcpClients,
			}

			results := executeToolsParallel(execCalls, execReq)

			for i, r := range results {
				tc := execCalls[i]

				// D5 输出截断
				maxToolOutput := 40000
				if len(r.Output) > maxToolOutput {
					r.Output = r.Output[:maxToolOutput] +
						fmt.Sprintf("\n\n...[工具输出截断]")
				}

				// PostToolUse hook
				hookRunner.OnPostToolUse(tc.Name, r.Output, executablePlans[i].toolInput)
				// 事件流
				GetEventStream().EmitObservation(taskID, r.Output, r.IsError)
				RecordToolCall()

				// 回填
				resultJSON, _ := json.Marshal(r)
				messages = append(messages, loopMessage{
					Role: "tool", Content: string(resultJSON), ToolCallID: tc.ID, Name: tc.Name,
				})
				NewSlog("agent_loop").Info("turn 工具", "taskID", taskID, "turn1", turn+1, "Name", tc.Name)
			}
		}

			// D7/6.3 token 预算中途检查:超阈值时压缩消息历史(解耦为 CompactAgentMessages)
			compactionThreshold := tokenBudget * 75 / 100
			if totalTokenIn > compactionThreshold && turn < maxTurns-1 {
				NewSlog("agent_loop").Info("D7 token 超阈值 (%),触发中途压缩", "taskID", taskID, "totalTokenIn", totalTokenIn, "compactionThreshold", compactionThreshold)
				// 2.3.2: 传递 condenser 类型(默认 CondenserRecent,可扩展为 agent 配置)
				messages = CompactAgentMessages(messages, taskID, tokenBudget, CondenserRecent)
				hookRunner.OnPreCompact()
			}
	}

	// 达 maxTurns
	NewSlog("agent_loop").Info("agent 达 maxTurns=,返回最后文本", "taskID", taskID, "Name", agent.Name)
	// #36 hooks:Stop + SessionEnd
	hookRunner.OnStop()
	hookRunner.OnSessionEnd()
	// #43 事件流:emit 完成
	GetEventStream().EmitStateUpdate(taskID, "execution_status", "finished")
	GetEventStream().EmitMessage(taskID, "Agent "+agent.Name+" 完成(达 maxTurns)")
	SetAgentStatus(agentID, StatusDone, fmt.Sprintf("达最大轮数 %d", maxTurns), taskID)
	return ChatResult{Text: lastText, TokenIn: totalTokenIn, TokenOut: totalTokenOut, Source: realName}
}

// llmWithToolsResponse 带 tool_calls 的 LLM 响应。
type llmWithToolsResponse struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls"`
	TokenIn   int        `json:"token_in"`
	TokenOut  int        `json:"token_out"`
}

// callLLMWithToolsStream SSE 流式调用（D27 集成：实时推送 token 到 EventStream + WebSocket）。
// 解析 tool_calls,支持模型主动调用工具。
// apiFormat 非空时优先用显式声明;Anthropic/Gemini 原生协议的 tools+stream 暂走非流式回退。
func callLLMWithToolsStream(baseURL, apiKey, apiFormat, model string, messages []loopMessage, toolsJSON []byte, taskID string) (llmWithToolsResponse, error) {
	// 无 taskID 或非 OpenAI 协议 → 直接走非流式
	if taskID == "" {
		return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, toolsJSON, "")
	}
	// Anthropic/Gemini 原生协议:tools+stream 跨协议适配复杂,回退非流式调用
	prov := ResolveProvider(baseURL, apiFormat)
	if prov == ProviderAnthropic || prov == ProviderGemini {
		return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, toolsJSON, "")
	}

	// 构建带 tools 的请求体
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	body := map[string]any{
		"model":       model,
		"messages":    messages,
		"max_tokens":  2048,
		"temperature": 0.2,
	}
	if len(toolsJSON) > 0 && string(toolsJSON) != "null" {
		body["tools"] = json.RawMessage(toolsJSON)
	}
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, toolsJSON, "")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, toolsJSON, "")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, toolsJSON, "")
	}

	var fullText strings.Builder
	var toolCalls []ToolCall
	tokenIn, tokenOut := 0, 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content    string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// 处理 usage（流末尾）
		if chunk.Usage != nil {
			tokenIn = chunk.Usage.PromptTokens
			tokenOut = chunk.Usage.CompletionTokens
		}

		// 处理 delta
		for _, choice := range chunk.Choices {
			d := choice.Delta
			if d.Content != "" {
				fullText.WriteString(d.Content)
				// 推送到事件流（WebSocket 客户端可收到实时 token）
				if taskID != "" {
					GetEventStream().EmitMessage(taskID, d.Content)
				}
			}
			// 收集 tool_calls（流式返回，每个 delta 可能只含部分字段）
			for _, tc := range d.ToolCalls {
				// 确保 toolCalls 有足够的槽位
				for len(toolCalls) <= tc.Index {
					toolCalls = append(toolCalls, ToolCall{})
				}
				if tc.ID != "" {
					toolCalls[tc.Index].ID = tc.ID
					toolCalls[tc.Index].Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					toolCalls[tc.Index].Arguments = append(toolCalls[tc.Index].Arguments, []byte(tc.Function.Arguments)...)
				}
			}
		}
	}

	// SSE 结束：填充 fallback token 计数
	if tokenIn == 0 {
		tokenIn = CountMessagesTokens(messages)
	}
	if tokenOut == 0 {
		tokenOut = CountTokens(fullText.String())
	}

	return llmWithToolsResponse{
		Text:      fullText.String(),
		ToolCalls: toolCalls,
		TokenIn:   tokenIn,
		TokenOut:  tokenOut,
	}, nil
}

// callLLMWithTools 调 OpenAI 兼容端点(带 tools 字段)。
// 解析 tool_calls,支持模型主动调用工具。
func callLLMWithTools(baseURL, apiKey, apiFormat, model string, messages []loopMessage, toolsJSON []byte) (llmWithToolsResponse, error) {
	return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, toolsJSON, "")
}

// callLLMWithoutTools 无工具纯文本调用（2.8 token 超限时的降级路径）。
func callLLMWithoutTools(baseURL, apiKey, apiFormat, model string, messages []loopMessage) (llmWithToolsResponse, error) {
	return callLLMInternal(baseURL, apiKey, apiFormat, model, messages, nil, "")
}

// callLLMInternal 统一的 LLM 调用实现。
// 2.6.2: responseFormat 参数支持 OpenAI response_format(如 "json_object")。
// apiFormat 非空时优先用显式声明;Anthropic/Gemini 走 provider 适配层非流式调用。
func callLLMInternal(baseURL, apiKey, apiFormat, model string, messages []loopMessage, toolsJSON []byte, responseFormat string) (llmWithToolsResponse, error) {
	llmCallStart := time.Now()
	// Anthropic/Gemini 原生协议:走 provider 适配层(它们不用 /chat/completions)
	prov := ResolveProvider(baseURL, apiFormat)
	if prov == ProviderAnthropic || prov == ProviderGemini {
		// 提取 system prompt + 最后一条 user message
		var systemPrompt, userPrompt string
		for _, m := range messages {
			if m.Role == "system" {
				systemPrompt += m.Content + "\n"
			}
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				userPrompt = messages[i].Content
				break
			}
		}
		result, err := callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, model, userPrompt, strings.TrimSpace(systemPrompt))
		if err != nil {
			return llmWithToolsResponse{}, err
		}
		return llmWithToolsResponse{
			Text:     result.Text,
			TokenIn:  result.TokenIn,
			TokenOut: result.TokenOut,
		}, nil
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"

	// 构造请求体
	body := map[string]any{
		"model":       model,
		"messages":    messages,
		"max_tokens":  2048,
		"temperature": 0.2,
	}
	if len(toolsJSON) > 0 && string(toolsJSON) != "null" {
		body["tools"] = json.RawMessage(toolsJSON)
	}
	// 2.6.1: response_format 支持(json_object 等)
		if responseFormat != "" {
			body["response_format"] = map[string]string{"type": responseFormat}
		}
		// DeepSeek 思考模式:检测到 DeepSeek 且非探活请求时启用
		if strings.Contains(strings.ToLower(baseURL), "deepseek") {
			body["thinking"] = map[string]string{"type": "enabled"}
			body["reasoning_effort"] = "high"
		}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return llmWithToolsResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return llmWithToolsResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return llmWithToolsResponse{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var respBody struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"` // 模型思考链
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return llmWithToolsResponse{}, err
	}
	if len(respBody.Choices) == 0 {
		return llmWithToolsResponse{}, fmt.Errorf("empty choices")
	}

	choice := respBody.Choices[0]
	// 转换 tool_calls
	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	// 审计埋点 B:记录 agent loop 的 LLM 调用(带 tools 的 OpenAI 路径)
	toolNames := ""
	for i, tc := range toolCalls {
		if i > 0 {
			toolNames += ","
		}
		toolNames += tc.Name
	}
	RecordLLMAudit(AuditLLMFields{
		Caller:    "agent_tools" + toolsSuffix(toolNames),
		Provider:  "openai",
		Model:     model,
		BaseURL:   baseURL,
		APIFormat: apiFormat,
		SystemPrompt: extractSystemPrompt(messages),
		Prompt:    extractLastUserPrompt(messages),
		Result: ChatResult{
			Text:      choice.Message.Content,
			Reasoning: choice.Message.ReasoningContent,
			TokenIn:   respBody.Usage.PromptTokens,
			TokenOut:  respBody.Usage.CompletionTokens,
			Source:    model,
		},
		Latency: time.Since(llmCallStart),
	})

	return llmWithToolsResponse{
		Text:      choice.Message.Content,
		ToolCalls: toolCalls,
		TokenIn:   respBody.Usage.PromptTokens,
		TokenOut:  respBody.Usage.CompletionTokens,
	}, nil
}

// isDangerousCommand 判断命令是否需要 Guardian 审批(写操作/网络/系统修改)。
func isDangerousCommand(cmd string) bool {
	dangerous := []string{"rm ", "rm -", "sudo", "chmod", "chown", "mkfs", "dd if",
		"curl ", "wget ", "> /etc/", "> /sys/", "shutdown", "reboot",
		"docker ", "systemctl", "pip install", "npm install -g"}
	for _, d := range dangerous {
		if strings.Contains(cmd, d) {
			return true
		}
	}
	return false
}

func agentLoopMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── 2.1 Structured Retry: 错误分类器 ──

// agentErrorType 标识 agent loop 中的错误类型。
type agentErrorType int

const (
	errUnknown   agentErrorType = iota // 未知错误
	errRetryable                       // 可重试（网络超时/429/5xx）
	errFatal                            // 致命（401/403/模型不存在）
	errMalformed                        // 响应格式错误（JSON 解析失败）
)

func (e agentErrorType) String() string {
	switch e {
	case errRetryable:
		return "retryable"
	case errFatal:
		return "fatal"
	case errMalformed:
		return "malformed"
	default:
		return "unknown"
	}
}

// classifyAgentError 分类 LLM 调用错误。
//
// 2.1.1: 区分可恢复错误和不可恢复错误。
// 可重试: 429/5xx/timeout/connection → 告诉 agent 重试
// 致命:   401/403/400 → 直接放弃
// 畸形:   JSON 解析失败 → 降级为纯文本
func classifyAgentError(err error) agentErrorType {
	if err == nil {
		return errUnknown
	}
	errStr := err.Error()
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") {
		return errFatal
	}
	if strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF") {
		return errRetryable
	}
	if strings.Contains(errStr, "JSON") || strings.Contains(errStr, "parse") ||
		strings.Contains(errStr, "unmarshal") || strings.Contains(errStr, "decode") {
		return errMalformed
	}
	return errUnknown
}

// ── 2.4 并行工具执行 ──

// toolExecRequest 工具执行请求（用于并行调度）。
type toolExecRequest struct {
	ToolCall      ToolCall
	PermLevel     string
	Workspace     string
	SearchProv    string
	SearchKey     string
	AgentID       string
	TaskID        string
	VerifyCommand string                      // 第6层:Agent 配置的自我验证命令(如 go test ./...)
	MCPClients    map[string]*MCPClient       // 用于 MCP 工具路由
}

// executeSingleTool 执行单个工具（从 agent loop 提取，供并行调度）。
// 审计埋点 C:所有工具调用都经过这里,统一记录入参/出参。
func executeSingleTool(req toolExecRequest) ToolResult {
	tc := req.ToolCall
	toolStart := time.Now()
	// 执行工具
	var result ToolResult
	switch {
	case tc.Name == "LoadSkill":
		result = ExecuteLoadSkill(tc.Arguments)
	case tc.Name == "Task":
		result = ExecuteTask(tc.Arguments, "", req.TaskID, 0)
	case tc.Name == "Verify":
		result = ExecuteVerify(req.VerifyCommand, req.Workspace)
	case tc.Name == "apply_patch":
		result = ExecuteApplyPatch(tc.Arguments, req.Workspace)
	case tc.Name == "TodoWrite":
		result = ExecuteTodoWrite(tc.Arguments, req.TaskID, req.AgentID)
	case tc.Name == "SendMessage":
		result = ExecuteSendMessage(tc.Arguments, "", req.TaskID)
	case tc.Name == "tool_search":
		result = ExecuteToolSearch(tc.Arguments)
	case isBuiltinTool(tc.Name):
		result = ExecuteBuiltinTool(tc, req.PermLevel, req.Workspace,
			req.SearchProv, req.SearchKey, req.AgentID, req.TaskID)
	default:
		result = ExecuteMCPTool(tc.Name, tc.Arguments, req.MCPClients)
	}
	// 审计埋点 C:记录工具调用
	argsJSON, _ := json.Marshal(tc.Arguments)
	RecordToolAudit(
		req.TaskID, currentAuditTurnSafe(), "agent",
		tc.Name, string(argsJSON), result.Output,
		result.IsError, "", time.Since(toolStart),
	)
	return result
}

// canRunParallel 判断工具是否可以与其他工具并行执行。
//
// 规则：
//   - 只读工具（FileRead/Glob/Grep/WebSearch）可以并行
//   - 写工具（FileWrite/BashTool/apply_patch）必须串行（避免竞态）
//   - Task（子代理）可并行（每个子代理独立上下文）
//   - LoadSkill/TodoWrite 可并行
func canRunParallel(tc ToolCall) bool {
	parallelizable := map[string]bool{
		"FileRead":  true,
		"Glob":      true,
		"Grep":      true,
		"WebSearch": true,
		"Task":      true,
		"LoadSkill": true,
		"TodoWrite": true,
	}
	return parallelizable[tc.Name]
}

// executeToolsParallel 并行执行无依赖的工具调用（2.4）。
//
// 将 toolCalls 分为两组：可并行的先并发了，串行的按顺序执行。
// 串行工具在并行组完成后执行（可能依赖并行工具的结果）。
func executeToolsParallel(toolCalls []ToolCall, req toolExecRequest) []ToolResult {
	var parallelGroup, serialGroup []ToolCall
	for _, tc := range toolCalls {
		if canRunParallel(tc) {
			parallelGroup = append(parallelGroup, tc)
		} else {
			serialGroup = append(serialGroup, tc)
		}
	}

	results := make([]ToolResult, len(toolCalls))

	// 捕获父 goroutine 的 EvalContext,供并行子 goroutine 继承(工具审计 turn/caller)。
	parentCtx := currentEvalContext()

	// 并行执行 parallelGroup
	if len(parallelGroup) > 0 {
		var wg sync.WaitGroup
		resultsCh := make(chan struct {
			idx int
			r   ToolResult
		}, len(parallelGroup))

		for i, tc := range parallelGroup {
			wg.Add(1)
			go func(idx int, tc ToolCall) {
				defer wg.Done()
				// 继承父上下文:让工具审计能正确记录 turn/caller(并发安全,详见 evalctx.go)
				BindChildEvalContext(parentCtx)
				defer UnbindEvalContext()
				reqCopy := req
				reqCopy.ToolCall = tc

				// 每个工具独立超时
				toolTimeout := 30 * time.Second
				if tc.Name == "Task" {
					toolTimeout = 120 * time.Second
				}
				resultCh := make(chan ToolResult, 1)
				go func() { resultCh <- executeSingleTool(reqCopy) }()

				var r ToolResult
				select {
				case r = <-resultCh:
				case <-time.After(toolTimeout):
					r = ToolResult{
						Name: tc.Name, IsError: true,
						Output: fmt.Sprintf("[工具超时] %s 执行超过 %v", tc.Name, toolTimeout),
					}
				}
				resultsCh <- struct {
					idx int
					r   ToolResult
				}{idx, r}
			}(i, tc)
		}

		go func() {
			wg.Wait()
			close(resultsCh)
		}()

		for item := range resultsCh {
			results[item.idx] = item.r
		}
	}

	// 串行执行 serialGroup（偏移量 = len(parallelGroup)）
	for i, tc := range serialGroup {
		reqCopy := req
		reqCopy.ToolCall = tc

		toolTimeout := 30 * time.Second
		if tc.Name == "Task" {
			toolTimeout = 120 * time.Second
		}
		resultCh := make(chan ToolResult, 1)
		go func() {
			// 继承父上下文(串行执行组同样需要,工具审计才能记录 turn/caller)
			BindChildEvalContext(parentCtx)
			defer UnbindEvalContext()
			resultCh <- executeSingleTool(reqCopy)
		}()

		var r ToolResult
		select {
		case r = <-resultCh:
		case <-time.After(toolTimeout):
			r = ToolResult{
				Name: tc.Name, IsError: true,
				Output: fmt.Sprintf("[工具超时] %s 执行超过 %v", tc.Name, toolTimeout),
			}
		}
		results[len(parallelGroup)+i] = r
	}

	return results
}


// AgentCheckpoint agent loop 检查点快照结构(2.7.2)。
type AgentCheckpoint struct {
	AgentName     string        `json:"agent_name"`
	TaskID        string        `json:"task_id"`
	Turn          int           `json:"turn"`
	TotalTokenIn  int           `json:"total_token_in"`
	TotalTokenOut int           `json:"total_token_out"`
	Messages      []loopMessage `json:"messages"`
	SavedAt       time.Time     `json:"saved_at"`
}

// SaveAgentCheckpoint 保存 agent loop 检查点到文件(2.7.2 中断恢复)。
// 调用时机：ctx.Done() 触发、token 超限熔断、致命错误等中断场景。
func SaveAgentCheckpoint(messages []loopMessage, agentName, taskID string, tokenIn, tokenOut, turn int) {
	checkpoint := AgentCheckpoint{
		AgentName: agentName, TaskID: taskID, Turn: turn,
		TotalTokenIn: tokenIn, TotalTokenOut: tokenOut,
		Messages: messages, SavedAt: time.Now(),
	}
	dir := "data/checkpoints"
	os.MkdirAll(dir, 0755)
	filename := filepath.Join(dir, fmt.Sprintf("%s_%d.json", taskID, turn))
	jsonData, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		NewSlog("checkpoint").Error("序列化失败", "err", err)
		return
	}
	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
		NewSlog("checkpoint").Error("写入失败", "err", err)
		return
	}
	NewSlog("checkpoint").Info("已保存: (turn=, tokens=/)", "filename", filename, "turn", turn, "tokenIn", tokenIn, "tokenOut", tokenOut)
}

// ResumeFromCheckpoint 从检查点恢复 agent loop 状态(2.7.2)。
// 返回保存的检查点数据,文件不存在返回 error。
func ResumeFromCheckpoint(taskID string) (*AgentCheckpoint, error) {
	dir := "data/checkpoints"
	pattern := filepath.Join(dir, taskID+"_*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("no checkpoint found for task %s", taskID)
	}
	latestFile := matches[len(matches)-1]
	data, err := os.ReadFile(latestFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	var checkpoint AgentCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, fmt.Errorf("failed to parse checkpoint: %w", err)
	}
	NewSlog("checkpoint").Info("已恢复: (turn=)", "latestFile", latestFile, "Turn", checkpoint.Turn)
	return &checkpoint, nil
}

// CleanupCheckpoints 清理指定 task 的所有检查点。
func CleanupCheckpoints(taskID string) {
	dir := "data/checkpoints"
	pattern := filepath.Join(dir, taskID+"_*.json")
	matches, _ := filepath.Glob(pattern)
	for _, f := range matches {
		os.Remove(f)
	}
}

// ── 审计埋点 helper(供 callLLMInternal 用)──

// toolsSuffix 工具名列表转 caller 后缀(如 "agent_tools[BashTool,FileRead]")。
func toolsSuffix(toolNames string) string {
	if toolNames == "" {
		return ""
	}
	return "[" + toolNames + "]"
}

// extractSystemPrompt 从 messages 提取所有 system 消息拼接。
func extractSystemPrompt(messages []loopMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		if m.Role == "system" {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// extractLastUserPrompt 从 messages 提取最后一条 user 消息。
func extractLastUserPrompt(messages []loopMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
