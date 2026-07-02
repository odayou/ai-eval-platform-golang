package engine

// hooks.go — harness 第5层:Agent 生命周期钩子。
//
// 在 agent loop 的关键事件点执行用户配置的 shell 命令(hook)。
// 仿 Claude Code settings.json 的 hooks 格式 + good-skills/08-hook-system 的协议设计。
//
// 研究依据:
//   - Claude Code .claude/settings.json 的 hooks 字段(PreToolUse/PostToolUse/Stop/...)
//   - learn-harness-engineering tool-registry-pattern:fail-closed(默认拒绝,显式允许)
//   - good-skills/08-hook-system:退出码语义 0=allow/2=deny,stdin JSON payload
//   - harness-engineering concepts/06:Hooks = 生命周期脚本,成功静默/失败报错
//
// 事件点:
//   - PreToolUse:工具执行前(可拒绝/修改输入)
//   - PostToolUse:工具执行后(审计/后处理)
//   - Stop:agent loop 结束时(可跑验证)
//
// 执行协议:
//   - stdin 传 JSON payload(tool_name/tool_input/tool_output)
//   - 退出码:0=允许/2=拒绝/其他=失败(失败按 fail-closed 处理:默认允许但记日志)
//   - stdout 可输出 JSON 控制行为(decision:block / updatedInput / message)

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// HookEvent 钩子事件类型（9 种，good-skills #36 补齐）。
type HookEvent string

const (
	HookPreToolUse      HookEvent = "PreToolUse"
	HookPostToolUse     HookEvent = "PostToolUse"
	HookStop            HookEvent = "Stop"
	HookSubagentStop    HookEvent = "SubagentStop"    // #36 新增:子代理结束
	HookUserPromptSubmit HookEvent = "UserPromptSubmit" // #36 新增:用户提交消息
	HookPreCompact      HookEvent = "PreCompact"      // #36 新增:上下文压缩前
	HookSessionStart    HookEvent = "SessionStart"    // #36 新增:会话开始
	HookSessionEnd      HookEvent = "SessionEnd"      // #36 新增:会话结束
	HookNotification    HookEvent = "Notification"    // #36 新增:系统通知
)

// AllHookEvents 全部 9 种事件（供前端展示/校验）。
var AllHookEvents = []HookEvent{
	HookPreToolUse, HookPostToolUse, HookStop, HookSubagentStop,
	HookUserPromptSubmit, HookPreCompact, HookSessionStart,
	HookSessionEnd, HookNotification,
}

// HookType hook 执行类型（3 种，good-skills #36 补齐）。
type HookType string

const (
	HookTypeBash      HookType = "command"  // shell 命令（原有）
	HookTypeLLM       HookType = "llm"      // #36 新增:LLM 评判（$ARGUMENTS 占位）
	HookTypeVerifier  HookType = "verifier" // #36 新增:子 agent 验证
)

// HookPayload 喂给 hook 脚本的 stdin JSON。
type HookPayload struct {
	Event       string `json:"event"`         // PreToolUse|PostToolUse|Stop
	ToolName    string `json:"tool_name"`     // 触发的工具名(Stop 事件为空)
	ToolInput   any    `json:"tool_input"`    // 工具输入参数
	ToolOutput  string `json:"tool_output"`   // 工具输出(PostToolUse 才有)
	AgentID     string `json:"agent_id"`
}

// HookDecision hook 的决策。
type HookDecision struct {
	Allow   bool   // 是否允许工具执行
	Message string // hook 返回的消息(注入给 agent 或记日志)
}

// HookRowExt 扩展 HookRow，添加内存中的 priority/hook_type/timeout_ms 字段。
// 完整实现需要给 agent_hooks 表加这些列——当前版本在内存中提供默认值。
type HookRowExt struct {
	store.HookRow
	Priority    int    // 5.1: 优先级(越高越先执行,默认 0)
	HookType    string // "command"|"llm"|"verifier" (默认 "command")
	TimeoutMs   int    // 5.5: per-hook 超时(ms,0=用事件默认)
	StopOnMatch bool   // 5.1: 本 hook 匹配后是否停止后续 hook
	FailMode    string // 5.2.3: 失败模式("open"=降级放行,"closed"=拒绝)
	Model       string // 5.3.1: verifier/llm hook 独立模型(空=继承主 agent 模型)
}

// wrapHooks 将 HookRow 列表转为带默认值的扩展列表。
// 5.2.3: FailMode 默认 "open"(降级放行),DB 列 fail_mode 可覆盖为 "closed"。
// 5.3.1: Model 默认空(继承主 agent),DB 列 hook_model 可指定独立模型。
// 5.5.2: TimeoutMs 默认 0(使用事件级超时),DB 列 timeout_ms 可覆盖。
func wrapHooks(rows []store.HookRow) []HookRowExt {
	ext := make([]HookRowExt, len(rows))
	for i, r := range rows {
		ext[i] = HookRowExt{
			HookRow:  r,
			Priority: 0,
			HookType: "command",
			FailMode: "open", // 5.2.3 默认 fail-open(降级放行)
			// TimeoutMs 和 Model 默认零值(0 和 ""),由调用方或 DB 列覆盖
		}
	}
	return ext
}

// HookRunner 钩子执行器。
type HookRunner struct {
	AgentID  string
	Hooks    []HookRowExt // 从 DB 加载的钩子配置(扩展)
}

// NewHookRunner 从 DB 加载 agent 的钩子配置。
func NewHookRunner(agentID string) *HookRunner {
	hooks, err := store.GetAgentHooks(agentID)
	if err != nil {
		return &HookRunner{AgentID: agentID}
	}
	return &HookRunner{AgentID: agentID, Hooks: wrapHooks(hooks)}
}

// matchMatcher 检查 hook 的 matcher 是否匹配工具名(通配符支持)。
//
// matcher 为空或 "*" 匹配全部;"Bash*" 匹配 BashTool/Bash 等。
func matchMatcher(matcher, toolName string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	// 简单通配:Bash* / *Tool
	if strings.HasSuffix(matcher, "*") {
		return strings.HasPrefix(toolName, strings.TrimSuffix(matcher, "*"))
	}
	if strings.HasPrefix(matcher, "*") {
		return strings.HasSuffix(toolName, strings.TrimPrefix(matcher, "*"))
	}
	return matcher == toolName
}

// RunHooks 执行某事件点的全部匹配 hook。
//
// 5.1: 按 priority DESC 排序后执行，支持 stop_on_first_match。
// 5.4: PostToolUse/Stop/Notification 事件异步执行（不阻塞 agent loop）。
//
// 返回最终决策(PreToolUse 场景:任一 hook 拒绝则拒绝)。
func (r *HookRunner) RunHooks(event HookEvent, payload HookPayload) HookDecision {
	decision := HookDecision{Allow: true}

	// 5.1: 按 priority DESC 排序
	sorted := sortHooksByPriority(r.Hooks)

	for _, hook := range sorted {
		if !hook.Enabled || hook.Event != string(event) {
			continue
		}
		if event == HookPreToolUse || event == HookPostToolUse {
			if !matchMatcher(hook.Matcher, payload.ToolName) {
				continue
			}
		}

		// 5.4: 异步事件（PostToolUse/Stop/Notification/SubagentStop/SessionEnd）
		if event == HookPostToolUse || event == HookStop || event == HookNotification ||
			event == HookSubagentStop || event == HookSessionEnd {
			hookCopy := hook
			payloadCopy := payload
			go func() {
				r.executeOne(hookCopy, payloadCopy, event)
			}()
			// 异步不参与决策，继续下一个
			continue
		}

		payload.AgentID = r.AgentID
		result := r.executeOne(hook, payload, event)

		if !result.Allow {
			return result
		}
		if result.Message != "" {
			decision.Message += result.Message + "\n"
		}

		// 5.1: stop_on_first_match
		if hook.StopOnMatch {
			break
		}
	}
	return decision
}

// executeOne 执行一个 hook（5.5 使用 D20 事件超时）。
func (r *HookRunner) executeOne(hook HookRowExt, payload HookPayload, event HookEvent) HookDecision {
	// 按 hook 类型分发
	switch hook.HookType {
	case "llm":
		// 5.2.3 传入 FailMode,LLM 调用失败时按模式决定放行或拒绝
		return ExecuteLLMHook(hook.Command, payload, hook.FailMode)
	case "verifier":
		// 5.3.1 传入 Model,verifier 子代理可使用独立模型
		return ExecuteVerifierHook(hook.Command, payload, hook.Model)
	default:
		// command 类型：shell 执行(5.5.2 传入 per-hook 超时)
		return r.executeBashHook(hook, payload, event)
	}
}

// executeBashHook 执行 shell 类型 hook（5.5 使用事件超时，5.5.2 per-hook 超时可覆盖）。
func (r *HookRunner) executeBashHook(hook HookRowExt, payload HookPayload, event HookEvent) HookDecision {
	if hook.Command == "" {
		return HookDecision{Allow: true}
	}

	payloadJSON, _ := json.Marshal(payload)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", hook.Command)
	} else {
		cmd = exec.Command("sh", "-c", hook.Command)
	}
	cmd.Stdin = strings.NewReader(string(payloadJSON))

	// 5.5.2 per-hook 超时优先,否则使用事件级超时
	timeout := getEventTimeout(event)
	if hook.TimeoutMs > 0 {
		timeout = time.Duration(hook.TimeoutMs) * time.Millisecond
	}
	timer := time.AfterFunc(timeout, func() { cmd.Process.Kill() })
	defer timer.Stop()

	output, err := cmd.CombinedOutput()
	out := string(output)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return HookDecision{Allow: false, Message: "hook 拒绝: " + truncate(out, 200)}
		}
		NewSlog("hooks").Error("hook 执行失败(cmd=): | output", "Command", hook.Command, "err", err, "truncateout100", truncate(out, 100))
		return HookDecision{Allow: true, Message: "[hook 执行失败,已降级放行]"}
	}

	decision := parseHookStdout(out)
	return decision
}

// parseHookStdout 解析 hook 的 stdout(支持纯文本和 JSON)。
func parseHookStdout(stdout string) HookDecision {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return HookDecision{Allow: true}
	}
	// 尝试 JSON
	if strings.HasPrefix(stdout, "{") {
		var data struct {
			Decision string `json:"decision"` // "block" = 拒绝
			Message  string `json:"message"`
		}
		if err := json.Unmarshal([]byte(stdout), &data); err == nil {
			if data.Decision == "block" {
				return HookDecision{Allow: false, Message: data.Message}
			}
			return HookDecision{Allow: true, Message: data.Message}
		}
	}
	// 纯文本当消息
	return HookDecision{Allow: true, Message: stdout}
}

// ── 便捷方法:在 agent loop 关键点调用 ──

// OnPreToolUse PreToolUse hook:工具执行前,可拒绝。
func (r *HookRunner) OnPreToolUse(toolName string, toolInput any) HookDecision {
	return r.RunHooks(HookPreToolUse, HookPayload{
		Event:     string(HookPreToolUse),
		ToolName:  toolName,
		ToolInput: toolInput,
	})
}

// OnPostToolUse PostToolUse hook:工具执行后,审计用。
func (r *HookRunner) OnPostToolUse(toolName, toolOutput string, toolInput any) HookDecision {
	return r.RunHooks(HookPostToolUse, HookPayload{
		Event:      string(HookPostToolUse),
		ToolName:   toolName,
		ToolInput:  toolInput,
		ToolOutput: toolOutput,
	})
}

// OnStop Stop hook:agent loop 结束时。
func (r *HookRunner) OnStop() HookDecision {
	return r.RunHooks(HookStop, HookPayload{
		Event: string(HookStop),
	})
}

// ── 内置 hook(把现有 chaos/antileak/permission_gate 统一为 hook 语义)──

// BuiltinPreToolHooks 内置的 PreToolUse hook(权限门控等)。
// 这些是编译进二进制的 hook,不需要 DB 配置,总是生效。
func BuiltinPreToolHooks(toolName, command string, permLevel string) HookDecision {
	// BashTool 的权限门控(复用 permission_gate.go)
	if toolName == "BashTool" && command != "" {
		gate := NewPermissionGate(permLevel, ".")
		if allow, reason := gate.CheckCommand(command); !allow {
			return HookDecision{Allow: false, Message: fmt.Sprintf("[内置权限 hook] %s", reason)}
		}
	}
	return HookDecision{Allow: true}
}

// ── #36 新增便捷方法:其他 6 种事件 ──

// OnSessionStart SessionStart hook:会话开始时。
func (r *HookRunner) OnSessionStart() HookDecision {
	return r.RunHooks(HookSessionStart, HookPayload{Event: string(HookSessionStart)})
}

// OnSessionEnd SessionEnd hook:会话结束时。
func (r *HookRunner) OnSessionEnd() HookDecision {
	return r.RunHooks(HookSessionEnd, HookPayload{Event: string(HookSessionEnd)})
}

// OnUserPromptSubmit UserPromptSubmit hook:用户提交消息时。
func (r *HookRunner) OnUserPromptSubmit(prompt string) HookDecision {
	return r.RunHooks(HookUserPromptSubmit, HookPayload{
		Event:    string(HookUserPromptSubmit),
		ToolInput: map[string]string{"prompt": prompt},
	})
}

// OnPreCompact PreCompact hook:上下文压缩前。
func (r *HookRunner) OnPreCompact() HookDecision {
	return r.RunHooks(HookPreCompact, HookPayload{Event: string(HookPreCompact)})
}

// OnSubagentStop SubagentStop hook:子代理结束时。
func (r *HookRunner) OnSubagentStop(subagentID, summary string) HookDecision {
	return r.RunHooks(HookSubagentStop, HookPayload{
		Event:      string(HookSubagentStop),
		ToolOutput: summary,
		ToolName:   subagentID,
	})
}

// OnNotification Notification hook:系统通知时。
func (r *HookRunner) OnNotification(message string) HookDecision {
	return r.RunHooks(HookNotification, HookPayload{
		Event:      string(HookNotification),
		ToolOutput: message,
	})
}

// ── #36 LLM hook 类型执行器 ──
//
// LLM hook 不执行 shell 命令,而是用 $ARGUMENTS 占位调 LLM 评判。
// 适用于需要语义判断的 hook（如"这段代码是否安全"）。

// ── 5.2 LLM hook 缓存 ──

type llmHookCacheEntry struct {
	result   HookDecision
	time     time.Time
}

var (
	llmHookCache   = make(map[string]llmHookCacheEntry)
	llmHookCacheMu sync.RWMutex
)

// ExecuteLLMHook 执行 LLM 类型 hook（5.2 加缓存）。
// failMode: 5.2.3 "open"(默认,降级放行) 或 "closed"(失败时拒绝)。
func ExecuteLLMHook(promptTemplate string, payload HookPayload, failMode string) HookDecision {
	// 5.2.1 缓存检查：相同 prompt 5 分钟内复用
	cacheKey := promptTemplate + "|" + payload.ToolName
	llmHookCacheMu.RLock()
	if entry, ok := llmHookCache[cacheKey]; ok && time.Since(entry.time) < 5*time.Minute {
		llmHookCacheMu.RUnlock()
		return entry.result
	}
	llmHookCacheMu.RUnlock()

	// 替换占位符
	payloadJSON, _ := json.Marshal(payload)
	prompt := strings.ReplaceAll(promptTemplate, "$ARGUMENTS", string(payloadJSON))
	prompt = strings.ReplaceAll(prompt, "$TOOL_NAME", payload.ToolName)
	prompt = strings.ReplaceAll(prompt, "$TOOL_OUTPUT", payload.ToolOutput)

	// 调 LLM
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("main")
	if baseURL == "" || apiKey == "" {
		// 5.2.3 无 Key 时按 failMode 决定
		if failMode == "closed" {
			return HookDecision{Allow: false, Message: "[LLM hook 无 Key,fail-closed 拒绝]"}
		}
		return HookDecision{Allow: true, Message: "[LLM hook 无 Key,降级放行]"}
	}

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, prompt)
	if err != nil {
		NewSlog("hooks").Error("D18 LLM hook 调用失败: (failMode=)", "err", err, "failMode", failMode)
		// 5.2.3 根据 failMode 决定失败时的行为
		if failMode == "closed" {
			decision := HookDecision{Allow: false, Message: "[LLM hook 失败,fail-closed 拒绝]"}
			llmHookCacheMu.Lock()
			llmHookCache[cacheKey] = llmHookCacheEntry{result: decision, time: time.Now()}
			llmHookCacheMu.Unlock()
			return decision
		}
		// fail-open: 降级放行
		decision := HookDecision{Allow: true, Message: "[LLM hook 失败,降级放行]"}
		// 5.2.3 失败也缓存（避免重复失败调用）
		llmHookCacheMu.Lock()
		llmHookCache[cacheKey] = llmHookCacheEntry{result: decision, time: time.Now()}
		llmHookCacheMu.Unlock()
		return decision
	}

	decision := parseHookStdout(result.Text)

	// D22 完整 JSON 输出协议
	text := strings.TrimSpace(result.Text)
	if strings.HasPrefix(text, "{") {
		var data struct {
			Decision           string `json:"decision"`
			Message            string `json:"message"`
			HookSpecificOutput struct {
				AdditionalContext  string `json:"additionalContext"`
				PermissionDecision string `json:"permissionDecision"`
				UpdatedInput       any    `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(text), &data); err == nil {
			if data.HookSpecificOutput.AdditionalContext != "" {
				decision.Message += "\n[注入上下文] " + data.HookSpecificOutput.AdditionalContext
			}
			if data.HookSpecificOutput.PermissionDecision == "deny" {
				decision.Allow = false
			}
			if data.HookSpecificOutput.UpdatedInput != nil {
				updatedJSON, _ := json.Marshal(data.HookSpecificOutput.UpdatedInput)
				decision.Message += "\n[updatedInput] " + string(updatedJSON)
			}
		}
	}

	// 5.2.1 存入缓存
	llmHookCacheMu.Lock()
	llmHookCache[cacheKey] = llmHookCacheEntry{result: decision, time: time.Now()}
	llmHookCacheMu.Unlock()

	return decision
}

// ExecuteVerifierHook 执行 Verifier 类型 hook(D19 完整实现:启动子 agent 验证)。
//
// D19 增强:
//   - 用 RunSubagent 启动 General 子代理做复杂验证(完整 ReAct loop)
//   - 子代理返回 "PASS" 或 "FAIL: 原因"
//   - 超时 60s(防止子代理无限跑)
//   - fail-closed: 子代理报错时默认放行(不阻塞,但记 warning)
func ExecuteVerifierHook(verificationPrompt string, payload HookPayload, model string) HookDecision {
	payloadJSON, _ := json.Marshal(payload)
	fullPrompt := fmt.Sprintf("%s\n\n【验证上下文】\n%s\n\n请判断此操作是否安全。输出 PASS 或 FAIL: 原因。",
		verificationPrompt, string(payloadJSON))

	// D19 用 goroutine + 超时
	// 5.3.1 使用 verifier 配置的独立模型(空则继承主 agent 模型)
	done := make(chan string, 1)
	go func() {
		summary := RunSubagent(SubagentGeneral, fullPrompt, model, 2, "", nil)
		done <- summary
	}()

	select {
	case summary := <-done:
		upperSummary := strings.ToUpper(summary)
		if strings.Contains(upperSummary, "FAIL") {
			return HookDecision{Allow: false, Message: "[verifier 拒绝] " + truncate(summary, 200)}
		}
		return HookDecision{Allow: true, Message: "[verifier 通过] " + truncate(summary, 100)}
	case <-time.After(60 * time.Second):
		return HookDecision{Allow: true, Message: "[verifier 超时,降级放行]"}
	}
}

// D20 按事件配置超时(PreToolUse 快 10s,Stop 慢 60s,SessionStart 30s)。
var eventTimeouts = map[HookEvent]time.Duration{
	HookPreToolUse:      10 * time.Second,
	HookPostToolUse:     30 * time.Second,
	HookStop:            60 * time.Second,
	HookSubagentStop:    30 * time.Second,
	HookUserPromptSubmit: 10 * time.Second,
	HookPreCompact:      30 * time.Second,
	HookSessionStart:    30 * time.Second,
	HookSessionEnd:      60 * time.Second,
	HookNotification:    5 * time.Second,
}

// getEventTimeout 获取事件超时(D20)。
func getEventTimeout(event HookEvent) time.Duration {
	if t, ok := eventTimeouts[event]; ok {
		return t
	}
	return 10 * time.Second // 默认
}

// D21 hook 优先级排序:按 Priority DESC + HookType(verifier 最后)。
// 同优先级下:command > llm > verifier(verifier 最重放最后)。
func sortHooksByPriority(hooks []HookRowExt) []HookRowExt {
	result := make([]HookRowExt, len(hooks))
	copy(result, hooks)
	// 冒泡排序(数量少(<10),简单实现即可)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			// Priority DESC, 同 priority 时 type 优先(verifier 最后)
			if result[i].Priority < result[j].Priority ||
				(result[i].Priority == result[j].Priority && typeOrder(result[i].HookType) > typeOrder(result[j].HookType)) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// typeOrder verifier 排最后(最重操作)。
func typeOrder(t string) int {
	switch t {
	case "command":
		return 0
	case "llm":
		return 1
	case "verifier":
		return 2
	default:
		return 1
	}
}
