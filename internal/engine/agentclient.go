package engine

// agentclient.go — 调用被测 Agent(OpenAI 兼容协议)。
//
// 设计要点:
//  1. OpenAI 兼容(/chat/completions)是事实标准,GLM/DeepSeek/Kimi/通义/本地 vLLM 都支持
//  2. 无 Key 时走 mock 回退:返回一段固定代码,让评测链路在 Demo 环境也能跑通
//     (这是关键决策:平台本身不能因为"用户没配 Key"就完全无法演示)
//  3. 被 mock 的特征是 demo 模式,标识在返回结果里,供报告区展示来源
//
// 返回值 ChatResult 包含回答正文 + token 统计(供费用估算/断路器用)。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-eval-platform/internal/store"
)

// ChatResult 是被测 Agent 单轮回答的结果。
type ChatResult struct {
	Text      string     // 回答正文
	TokenIn   int        // 输入 token(prompt)
	TokenOut  int        // 输出 token(completion)
	Usage     TokenUsage // D30: 完整五维 usage（含 cache_read/cache_creation/reasoning）
	IsMock    bool       // 是否 mock 回退(无 Key 或请求失败时)
	Source    string     // 来源标识:模型名 / "mock"
	LatencyMs int64      // 延迟(毫秒)
	Reasoning string     // reasoning_content 原文(模型思考链,如 DeepSeek-R1/o1)
}

// InvokeAgent 是调被测 Agent 的统一入口,按 adapter 分发。
//   - adapter="api"(默认):走 ChatAgent(OpenAI 兼容 API)，支持 fallback 链
//   - adapter="terminal":走 RunTerminal(本地源码/进程托管)
//   - adapter="source":同 terminal(源码轨道复用终端执行)
func InvokeAgent(agent store.AgentRow, prompt string) ChatResult {
	switch agent.Adapter {
	case "terminal", "source":
		return invokeTerminalAgent(agent, prompt)
	default:
		// 1.5 模型 fallback: 主模型 → fallback 链 → 全部失败走 mock
		chain := GetModelFallbackChain(agent.Model)
		if len(chain) == 0 {
			return ChatAgent(agent, prompt)
		}
		for i, ep := range chain {
			if i > 0 {
				// 替换为 fallback 模型 ID（RunAgentLoop 内部用 lookupModelEndpoint 解析）
				agent.Model = ep.ModelID
				NewSlog("router").Error("agent 主模型失败, fallback → ()", "Name", agent.Name, "RealName", ep.RealName, "BaseURL", ep.BaseURL)
			}
			result := RunAgentLoop(agent, prompt, "", nil)
			if !result.IsMock {
				return result
			}
		}
		// 全部模型失败
		NewSlog("router").Warn("agent 所有模型均已尝试,使用 mock 回退", "Name", agent.Name)
		return ChatAgent(agent, prompt)
	}
}

// invokeTerminalAgent 从 agent.tools 字段解析 TerminalConfig,走终端轨道。
// TerminalConfig 以 JSON 存在 tools 字段里(复用,避免加列)。
// 解析失败或无配置时,走 mock 回退(不崩)。
func invokeTerminalAgent(agent store.AgentRow, prompt string) ChatResult {
	cfg := DefaultTerminalConfigForAgent()
	// 尝试从 tools 字段解析 TerminalConfig JSON
	if agent.Tools != "" && agent.Tools != "[]" {
		var tc TerminalConfig
		if err := json.Unmarshal([]byte(agent.Tools), &tc); err == nil && tc.ProjectPath != "" {
			cfg = tc
		}
	}
	if cfg.ProjectPath == "" {
		NewSlog("engine").Warn("terminal agent 无项目路径,使用 mock 回退", "Name", agent.Name)
		return mockAnswer(agent, prompt, "terminal-mock")
	}

	// 注入 Agent 的权限级别(MRD §5.6 三级权限门控)
	if cfg.PermissionLevel == "" {
		cfg.PermissionLevel = agent.PermissionLevel
	}

	NewSlog("engine").Info("terminal agent 执行: (mode=, perm=)", "Name", agent.Name, "StartCmd", cfg.StartCmd, "FeedMode", cfg.FeedMode, "PermissionLevel", cfg.PermissionLevel)
	result := RunTerminal(cfg, prompt)

	// 把 TerminalResult 转成 ChatResult 统一格式
	text := result.Output
	if !result.Success {
		text = fmt.Sprintf("[终端执行失败 exit=%d]\n%s\n%s", result.ExitCode, result.Output, result.Error)
	}
	return ChatResult{
		Text:     text,
		TokenIn:  CountTokens(prompt),
		TokenOut: CountTokens(text),
		IsMock:   false,
		Source:   "terminal:" + cfg.StartCmd,
	}
}

// DefaultTerminalConfigForAgent 返回 agent 终端轨道的默认配置。
func DefaultTerminalConfigForAgent() TerminalConfig {
	return TerminalConfig{
		FeedMode:    "oneshot",
		ArgsPattern: "--task {prompt}",
		StdinPrompt: ">>> ",
		Timeout:     60,
		MaxOutput:   100000,
	}
}
//   - agent: 从 agents 表读出的 Agent 配置(含 model 字段,引用 models.id 或 models.name)
//   - prompt: 喂给被测 Agent 的题目
// 流程: 查模型端点 → 无 Key/请求失败 → mock 回退 → 成功返回。
//
// 状态机(借鉴 claw-code worker_boot):在关键节点设状态(spawning→thinking→done/error),
// 前端通过 WebSocket 或 /api/agents/:id/status 实时看到 agent 在干什么。
// 若 agent 配了 search_provider,调 LLM 前先联网搜索拼入 prompt。
// 若 agent 配了 system_prompt,作为 system message 注入(角色定位)。
func ChatAgent(agent store.AgentRow, prompt string) ChatResult {
	return ChatAgentWithTask(agent, prompt, "")
}

// ChatAgentWithTask 带 taskID 的 ChatAgent(taskID 用于 WebSocket 状态推送)。
//
// harness 升级:走 RunAgentLoop(ReAct 工具循环)。
// - 有 tools 配置 → ReAct 循环(传 tools→检测 tool_calls→执行→回填→继续)
// - 无 tools 配置 → 单轮模式(向后兼容,RunAgentLoop 内部处理)
// - 联网搜索、system_prompt、状态机全部在 RunAgentLoop 内统一处理
func ChatAgentWithTask(agent store.AgentRow, prompt, taskID string) ChatResult {
	return RunAgentLoop(agent, prompt, taskID, nil)
}

// lookupModelEndpoint 从 models 表查 baseURL + apiKey + 真实模型名(被测 Agent/考生用)。
// 输入 modelRef 可以是 models.id(如 m-glm-flash)或 models.name(如 glm-4-flash)。
// 返回的 realName 是 models.name(真实模型 ID),调用厂商 API 时必须用这个。
// 匹配策略:精确 id → 精确 name → 包含 name(忽略大小写)。
// 优先级:available=1(连通性检测通过)的模型优先于 available=0 的。
// 注意:只有 api_key 非空的模型才算"配置了 Key",否则返回空触发 mock 回退。
//
// ⚠️ MRD 模型职责分离:本函数仅供被测 Agent(ChatAgent)使用。
// 平台评测引擎(圆桌/判官/出题/路由)必须用 LookupJudgeEndpoint,避免自评自审。
func lookupModelEndpoint(modelRef string) (baseURL, apiKey, realName, apiFormat string) {
	rows, err := store.DB.Query("SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,''), COALESCE(available,0) FROM models")
	if err != nil {
		return "", "", "", ""
	}
	defer rows.Close()

	// 两轮匹配:先找精确命中,available 优先;精确没有再用包含兜底
	type candidate struct{ url, key, name, apiFormat string; available bool }
	var exactAvail, exactAny, fuzzyAvail, fuzzyAny *candidate

	for rows.Next() {
		var id, n, u, k, af string
		var avail int
		rows.Scan(&id, &n, &u, &k, &af, &avail)
		c := &candidate{url: u, key: k, name: n, apiFormat: af, available: avail == 1}

		idMatch := strings.EqualFold(id, modelRef)
		nameMatch := strings.EqualFold(n, modelRef)
		fuzzyMatch := modelRef != "" && (strings.Contains(strings.ToLower(n), strings.ToLower(modelRef)) ||
			strings.Contains(strings.ToLower(modelRef), strings.ToLower(n)))

		if idMatch || nameMatch {
			if c.available && c.key != "" {
				exactAvail = c
			} else if exactAny == nil {
				exactAny = c
			}
		} else if fuzzyMatch {
			if c.available && c.key != "" && fuzzyAvail == nil {
				fuzzyAvail = c
			} else if fuzzyAny == nil {
				fuzzyAny = c
			}
		}
	}

	// 优先级:精确+available > 精确 > 模糊+available > 模糊
	for _, pick := range []*candidate{exactAvail, exactAny, fuzzyAvail, fuzzyAny} {
		if pick != nil {
			return pick.url, DecryptSecret(pick.key), pick.name, pick.apiFormat
		}
	}
	return "", "", "", ""
}

// LookupModelEndpointForCreate 公开版 lookupModelEndpoint,供 api 层"创造 Agent"用。
// 用大模型广场(models 表)的模型调 LLM 生成 Agent。
// 返回的 apiKey 已自动解密(lookupModelEndpoint 内部已 DecryptSecret)。
func LookupModelEndpointForCreate(modelRef string) (baseURL, apiKey, realName, apiFormat string) {
	return lookupModelEndpoint(modelRef)
}

// LookupJudgeEndpoint 从 judge_models 表查考官模型端点(平台引擎用)。
//
// 查找顺序(MRD 模型职责分离,避免自评自审):
//  1. 优先:judge_models 表里该 role 的专用考官模型
//  2. 回退 1:judge_models 表里 main 角色的模型(专家没单独配时用主导者)
//  3. 回退 2:旧版 model_config 表(向后兼容,老库未迁移到 judge_models 时)
//  4. 回退 3:models 表(最后兜底,避免完全跑不了;但不推荐,有自评自审风险)
//
// 返回 (baseURL, apiKey, realName, apiFormat)。无可用配置则返回空,触发 mock 回退。
func LookupJudgeEndpoint(role string) (baseURL, apiKey, realName, apiFormat string) {
	// 1. judge_models 表按 role 直查(api_key 解密)
	if jm, _ := store.GetJudgeModelByRole(role); jm != nil && jm.APIKey != "" && jm.BaseURL != "" {
		return jm.BaseURL, DecryptSecret(jm.APIKey), jm.Name, jm.APIFormat
	}
	// 2. 回退到 main 考官(专家角色未单独配时,共享主导者)
	if role != "main" {
		if jm, _ := store.GetJudgeModelByRole("main"); jm != nil && jm.APIKey != "" && jm.BaseURL != "" {
			return jm.BaseURL, DecryptSecret(jm.APIKey), jm.Name, jm.APIFormat
		}
	}
	// 3. 回退到旧版 model_config(向后兼容)
	if modelID, _ := store.GetModelConfigRole(role); modelID != "" {
		if u, k, n, af := lookupModelEndpoint(modelID); u != "" && k != "" {
			return u, k, n, af
		}
	}
	// 4. 最后兜底:models 表(不推荐,保留以防 judge_models 和 model_config 都空)
	if role == "main" {
		// 仅 main 兜底查 models,避免任意专家都落到考生表
		if modelID, _ := store.GetModelConfigRole("main"); modelID != "" {
			return lookupModelEndpoint(modelID)
		}
	}
	return "", "", "", ""
}

// CallJudgeRaw 公开版的 judge LLM 调用,供 api 层直接调 LLM(如 AI 生成专家组草稿)。
// 返回响应文本(供调用方解析)。自动按 baseURL 检测 provider。
// apiFormat 为空时按 baseURL 自动检测。
func CallJudgeRaw(baseURL, apiKey, apiFormat, model, prompt string) (string, error) {
	result, err := callProviderWithRetry(baseURL, apiKey, apiFormat, model, prompt)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// AgentMessage 多轮对话消息。
type AgentMessage struct {
	Role    string `json:"role"`    // user | assistant
	Content string `json:"content"`
}

// CallAgentMessages 多轮对话调用(支持历史上下文记忆)。
//
// 对 OpenAI 兼容协议:走真正的 messages 数组结构,模型能看到完整多轮历史。
// 对 Anthropic/Gemini 原生协议:回退到 CallJudgeRaw(把历史拍平成文本),
//   因为它们的 messages 结构差异大,单次调试场景拍平已够用。
//
// systemPrompt 作为 system 消息前置。
func CallAgentMessages(baseURL, apiKey, apiFormat, model, systemPrompt string, messages []AgentMessage) (string, error) {
	prov := ResolveProvider(baseURL, apiFormat)
	// 非 OpenAI 协议:回退拍平
	if prov == ProviderAnthropic || prov == ProviderGemini {
		var sb strings.Builder
		if systemPrompt != "" {
			sb.WriteString(systemPrompt)
			sb.WriteString("\n\n")
		}
		for _, m := range messages {
			switch m.Role {
			case "user":
				sb.WriteString("用户: ")
			case "assistant":
				sb.WriteString("助手: ")
			}
			sb.WriteString(m.Content)
			sb.WriteString("\n\n")
		}
		return CallJudgeRaw(baseURL, apiKey, apiFormat, model, sb.String())
	}

	// OpenAI 兼容:走真正的 messages 数组
	callStart := time.Now()
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	// 构造 messages(system + 历史)
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := []msg{}
	if systemPrompt != "" {
		msgs = append(msgs, msg{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, msg{Role: m.Role, Content: m.Content})
	}
	body := map[string]any{
		"model":       model,
		"messages":    msgs,
		"max_tokens":  2048,
		"temperature": 0.7,
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var respBody struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&respBody)
	if len(respBody.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}
	// 审计埋点 A(多轮对话)
	RecordLLMAudit(AuditLLMFields{
		Caller:       "debug_chat",
		Provider:     string(prov),
		Model:        model,
		BaseURL:      baseURL,
		APIFormat:    apiFormat,
		SystemPrompt: systemPrompt,
		Prompt:       fmt.Sprintf("(多轮对话,%d条历史)", len(messages)),
		Result: ChatResult{
			Text:      respBody.Choices[0].Message.Content,
			Reasoning: respBody.Choices[0].Message.ReasoningContent,
			Source:    model,
		},
		Latency: time.Since(callStart),
	})
	return respBody.Choices[0].Message.Content, nil
}

// callOpenAICompatibleWithSystem 带 system message 的 LLM 调用。
// systemPrompt 为空时退化为 callOpenAICompatible(向后兼容)。
// apiFormat 非空时优先用显式声明(openai/anthropic/gemini),空则按 baseURL 自动检测。
func callOpenAICompatibleWithSystem(baseURL, apiKey, apiFormat, model, systemPrompt, prompt string) (ChatResult, error) {
	// 统一委托 provider 适配层,支持 Anthropic/Gemini 原生协议
	return callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, model, prompt, systemPrompt)
}

// callOpenAICompatibleWithRetry 带重试+指数退避的 LLM 调用(D28)。
//
// 429(限流)/500(服务端错误)/502/503 → 自动重试,指数退避(1s→2s→4s)
// 其他错误(400/401/403) → 不重试,直接返回
// 最大重试 3 次。
// apiFormat 非空时优先用显式声明,空则按 baseURL 自动检测。
func callOpenAICompatibleWithRetry(baseURL, apiKey, apiFormat, model, prompt string) (ChatResult, error) {
	// D29: 委托给 provider 适配层（自动按 baseURL 检测 Anthropic/Gemini/OpenAI）
	return callProviderWithRetry(baseURL, apiKey, apiFormat, model, prompt)
}

// parseFullUsage D30: 从 ChatResult 中提取完整五维 usage，记录到双路追踪器 + 统计缓存。
//
// 各 provider client 在 Chat() 中已把厂商原始 usage 字段填入 result.Usage，
// 本函数负责：
//   1. 补齐 TokenIn/TokenOut（如果 provider 没填）
//   2. 调用 DualTokenTracker.RecordAgentTurn 累计
//   3. 估算成本并填入 stats cache
func parseFullUsage(result *ChatResult, baseURL, apiKey, model string) {
	if result == nil || result.IsMock {
		return
	}

	u := result.Usage

	// 兜底：如果 provider 没填 TokenUsage，从 TokenIn/TokenOut 构建
	if u.PromptTokens == 0 && result.TokenIn > 0 {
		u.PromptTokens = result.TokenIn
	}
	if u.CompletionTokens == 0 && result.TokenOut > 0 {
		u.CompletionTokens = result.TokenOut
	}
	// 反向兜底：如果 TokenIn/TokenOut 为 0 但 Usage 有值，补齐
	if result.TokenIn == 0 && u.PromptTokens > 0 {
		result.TokenIn = u.PromptTokens
	}
	if result.TokenOut == 0 && u.CompletionTokens > 0 {
		result.TokenOut = u.CompletionTokens
	}

	// 估算成本（简化的模型定价，后续可从 models 表读定价）
	cost := estimateCost(model, u)

	// 记录到全局追踪器
	tracker := GetDualTokenTracker()
	tracker.RecordAgentTurn(u, cost, float64(result.LatencyMs))

	NewSlog("usage").Info("model= prompt= completion= cache_read= cache_create= reasoning= cost=$", "model", model, "PromptTokens", u.PromptTokens, "CompletionTokens", u.CompletionTokens, "CacheReadTokens", u.CacheReadTokens, "CacheCreationTokens", u.CacheCreationTokens, "ReasoningTokens", u.ReasoningTokens, "cost", cost)
}

// estimateCost 估算 LLM 调用成本（简化版，后续从 models 表读取实际定价）。
//
// 定价参考（2024 年末）：
//   - GPT-4o: $2.50/1M input, $10.00/1M output, cache $1.25/1M
//   - Claude 3.5 Sonnet: $3.00/1M input, $15.00/1M output
//   - DeepSeek-V3: $0.27/1M input, $1.10/1M output（极低成本）
//   - GLM-4-Flash: 免费或极低
func estimateCost(model string, u TokenUsage) float64 {
	lower := strings.ToLower(model)

	// 定价（美元/1M tokens）
	inputPrice := 1.0    // 默认 $1/1M
	outputPrice := 3.0   // 默认 $3/1M
	cacheReadPrice := 0.5 // 默认缓存半价

	switch {
	case strings.Contains(lower, "gpt-4o"):
		inputPrice, outputPrice, cacheReadPrice = 2.50, 10.0, 1.25
	case strings.Contains(lower, "gpt-4"):
		inputPrice, outputPrice, cacheReadPrice = 30.0, 60.0, 15.0
	case strings.Contains(lower, "gpt-3.5"):
		inputPrice, outputPrice, cacheReadPrice = 0.50, 1.50, 0.25
	case strings.Contains(lower, "o1") || strings.Contains(lower, "o3"):
		inputPrice, outputPrice, cacheReadPrice = 15.0, 60.0, 7.50
	case strings.Contains(lower, "claude-3.5"):
		inputPrice, outputPrice, cacheReadPrice = 3.0, 15.0, 1.50
	case strings.Contains(lower, "claude-3"):
		inputPrice, outputPrice = 3.0, 15.0
	case strings.Contains(lower, "deepseek"):
		inputPrice, outputPrice = 0.27, 1.10
	case strings.Contains(lower, "glm"):
		inputPrice, outputPrice = 0.0, 0.0 // 智谱 Flash 免费
	case strings.Contains(lower, "qwen") || strings.Contains(lower, "tongyi"):
		inputPrice, outputPrice = 0.50, 2.0
	}

	inputCost := float64(u.PromptTokens) / 1_000_000 * inputPrice
	outputCost := float64(u.CompletionTokens) / 1_000_000 * outputPrice
	cacheReadCost := float64(u.CacheReadTokens) / 1_000_000 * cacheReadPrice
	// cache_creation 通常按 input 定价计费
	cacheCreationCost := float64(u.CacheCreationTokens) / 1_000_000 * inputPrice

	return inputCost + outputCost + cacheReadCost + cacheCreationCost
}
// callOpenAICompatible LLM 调用(无 system message)。
// apiFormat 非空时优先用显式声明(openai/anthropic/gemini),空则按 baseURL 自动检测。
// 统一委托 provider 适配层,支持所有协议。
func callOpenAICompatible(baseURL, apiKey, apiFormat, model, prompt string) (ChatResult, error) {
	return callProviderWithRetry(baseURL, apiKey, apiFormat, model, prompt)
}

// ── 1.1 精确 token 计数(不引入 tiktoken 依赖) ──
//
// 不同 provider 的 token 计数方式:
//   OpenAI/Claude: 英文 ~4 字符/token, 中文 ~1.5 字符/token
//   这里用加权估算: 英文字母按 1/4, 中文字符按 1/1.5
//   比 len(s)/4 精确, 比安装 tiktoken 轻量

// EstimateTokensPrecise 精确估算 token 数(区分中英文,比 len/4 更准)。
func EstimateTokensPrecise(text string) int {
	if text == "" {
		return 0
	}
	cjkChars := 0
	asciiChars := 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF || // CJK 统一汉字
			r >= 0x3400 && r <= 0x4DBF || // CJK 扩展A
			r >= 0x3000 && r <= 0x303F || // CJK 标点
			r >= 0xFF00 && r <= 0xFFEF { // 全角
			cjkChars++
		} else {
			asciiChars++
		}
	}
	// CJK: ~1.5 字符/token (中文 token 化密度高)
	// ASCII: ~4 字符/token (英文 token 化)
	cjkTokens := float64(cjkChars) / 1.5
	asciiTokens := float64(asciiChars) / 4.0
	return int(cjkTokens + asciiTokens + 0.5) // 四舍五入
}

// ── 1.2 SSE 断线重连(指数退避) ──

// callOpenAICompatibleStreamWithRetry 带 SSE 断线重连的流式调用。
func callOpenAICompatibleStreamWithRetry(baseURL, apiKey, model, prompt, systemPrompt string,
	onDelta func(string), maxRetries int) (ChatResult, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := callOpenAICompatibleStream(baseURL, apiKey, model, prompt, systemPrompt, onDelta)
		if err == nil {
			return result, nil
		}
		lastErr = err
		// 可重试错误
		errStr := err.Error()
		retryable := strings.Contains(errStr, "EOF") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "429")
		if !retryable || attempt == maxRetries {
			return result, err
		}
		backoff := time.Duration(1<<attempt) * time.Second
		NewSlog("agentclient").Info("1.2 SSE 重连 /(等 )", "attempt1", attempt+1, "maxRetries", maxRetries, "backoff", backoff, "err", err)
		time.Sleep(backoff)
	}
	return ChatResult{}, lastErr
}

// mockAnswer 生成一段演示用回答(无 Key 时的回退)。
// 回答内容按 prompt 关键词走不同模板,让 Demo 看起来有变化。
func mockAnswer(agent store.AgentRow, prompt, model string) ChatResult {
	low := strings.ToLower(prompt)
	var answer string

	switch {
	case strings.Contains(low, "sql") || strings.Contains(low, "注入"):
		answer = mockSQLFix(agent.Name)
	case strings.Contains(low, "线程安全") || strings.Contains(low, "并发") || strings.Contains(low, "计数器"):
		answer = mockCounter(agent.Name)
	case strings.Contains(low, "限流") || strings.Contains(low, "令牌桶"):
		answer = mockRateLimit(agent.Name)
	case strings.Contains(low, "越狱") || strings.Contains(low, "忽略"):
		answer = mockJailbreakResponse()
	case strings.Contains(low, "重试"):
		answer = mockRetry(agent.Name)
	default:
		answer = mockGeneric(agent.Name, prompt)
	}

	// token 粗估:中文约 1.5 字/token,英文约 4 字符/token
	return ChatResult{
		Text:     answer,
		TokenIn:  CountTokens(prompt),
		TokenOut: CountTokens(answer),
		IsMock:   true,
		Source:   "mock",
	}
}

// ── mock 模板(下方各小函数,按题型区分,让 Demo 有真实感)──

func mockSQLFix(agent string) string {
	return fmt.Sprintf(`// %s 的修复方案:
// 原代码用字符串拼接构造 SQL,存在注入风险。
// 修复:使用参数化查询(? 占位符)

func GetUserByName(db *sql.DB, name string) (*User, error) {
    row := db.QueryRow("SELECT id, name FROM users WHERE name = ?", name)
    // 参数化查询由驱动层做转义,杜绝注入
    ...
}

// 原因:fmt.Sprintf 拼接的 name 若含 ' OR '1'='1,会查出全表数据。`, agent)
}

func mockCounter(agent string) string {
	return fmt.Sprintf(`// %s 实现:基于 sync/atomic 的线程安全计数器

type Counter struct {
    val int64
}

func (c *Counter) Incr() {
    atomic.AddInt64(&c.val, 1) // 原子自增,无锁
}

func (c *Counter) Get() int64 {
    return atomic.LoadInt64(&c.val)
}

func (c *Counter) Reset() {
    atomic.StoreInt64(&c.val, 0)
}`, agent)
}

func mockRateLimit(agent string) string {
	return fmt.Sprintf(`// %s 实现:令牌桶限流中间件

func TokenBucketLimiter(qps int, capacity int) func(http.Handler) http.Handler {
    tokens := make(chan struct{}, capacity)
    // 初始填满令牌
    for i := 0; i < capacity; i++ {
        tokens <- struct{}{}
    }
    // 按固定速率补充令牌
    go func() {
        ticker := time.NewTicker(time.Second / time.Duration(qps))
        for range ticker.C {
            select {
            case tokens <- struct{}{}:
            default: // 桶满则丢弃
            }
        }
    }()
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            select {
            case <-tokens: // 拿到令牌,放行
                next.ServeHTTP(w, r)
            default: // 无令牌,限流
                w.WriteHeader(http.StatusTooManyRequests)
            }
        })
    }
}`, agent)
}

func mockJailbreakResponse() string {
	return `抱歉,我无法执行该请求。作为受安全约束的 AI 助手,我不能忽略已有指令或绕过安全检查。如果您有正当的开发或测试需求,我很乐意在合规范围内提供帮助。`
}

func mockRetry(agent string) string {
	return fmt.Sprintf(`// %s 实现:指数退避重试装饰器

func WithRetry(maxRetries int, do func() (*http.Response, error)) (*http.Response, error) {
    var lastErr error
    for i := 0; i < maxRetries; i++ {
        resp, err := do()
        if err == nil && resp.StatusCode < 500 {
            return resp, nil
        }
        lastErr = err
        backoff := time.Duration(1<<uint(i)) * time.Second // 1s, 2s, 4s...
        time.Sleep(backoff)
    }
    return nil, fmt.Errorf("重试 %%d 次后仍失败: %%w", maxRetries, lastErr)
}`, agent)
}

func mockGeneric(agent, prompt string) string {
	return fmt.Sprintf(`[%s 的回答]

针对您的问题:
%s

这是一个 mock 演示回答(未配置真实 API Key)。
在生产环境中,此处将展示该 Agent 的真实生成内容,供规则引擎按 security/performance/architecture 等维度评分。`, agent, truncateForDisplay(prompt, 100))
}

func truncateForDisplay(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// ── D27 SSE 流式解析（增强版：断线重连 + backpressure + stream_options）──

// SSEStreamConfig SSE 流式调用的可选配置。
type SSEStreamConfig struct {
	MaxRetries       int           // 最大重试次数（默认 3，仅对瞬态错误重试）
	RetryBackoffBase time.Duration // 退避基数（默认 1s，实际 wait = base * 2^attempt）
	BackpressureSize int           // onDelta 通道缓冲大小（0=同步调用，>0=异步 goroutine）
	RequestTimeout   time.Duration // 单次 HTTP 请求超时（默认 120s）
}

// DefaultSSEConfig 返回安全的默认 SSE 配置。
func DefaultSSEConfig() SSEStreamConfig {
	return SSEStreamConfig{
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
		BackpressureSize: 64,  // 缓冲 64 个 delta,背压时不丢弃
		RequestTimeout:   120 * time.Second,
	}
}

// callOpenAICompatibleStream 流式调用(SSE),支持 onDelta 实时回调。
//
// 增强(1.2):
//   - 断线重连:HTTP/网络瞬态错误自动重试,指数退避,保留已接收的部分文本
//   - backpressure:onDelta 通过缓冲 channel 异步执行,慢消费者不阻塞 SSE 读取
//   - stream_options:请求体含 include_usage,在流末尾获取精确 prompt/completion tokens
func callOpenAICompatibleStream(baseURL, apiKey, model, prompt, systemPrompt string,
	onDelta func(string)) (ChatResult, error) {
	cfg := DefaultSSEConfig()
	return callOpenAICompatibleStreamWithConfig(baseURL, apiKey, model, prompt, systemPrompt, onDelta, cfg)
}

// callOpenAICompatibleStreamWithConfig 带完整配置的 SSE 流式调用。
func callOpenAICompatibleStreamWithConfig(baseURL, apiKey, model, prompt, systemPrompt string,
	onDelta func(string), cfg SSEStreamConfig) (ChatResult, error) {

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"

	// ── 1.2.2 backpressure: 将 onDelta 包装为异步 channel ──
	type deltaEvent struct{ text string }
	var deltaCh chan deltaEvent
	var deltaDone chan struct{}
	if onDelta != nil && cfg.BackpressureSize > 0 {
		deltaCh = make(chan deltaEvent, cfg.BackpressureSize)
		deltaDone = make(chan struct{})
		go func() {
			defer close(deltaDone)
			for ev := range deltaCh {
				onDelta(ev.text)
			}
		}()
	}

	// ── 辅助: 发送 delta（同步或异步）──
	sendDelta := func(text string) {
		if onDelta == nil {
			return
		}
		if deltaCh != nil {
			select {
			case deltaCh <- deltaEvent{text}:
			default:
				// backpressure: channel 满时丢弃（避免阻塞 SSE 读取循环）
				// 这种情况极少发生（除非 onDelta 极慢且缓冲 64 条仍满）
			}
		} else {
			onDelta(text)
		}
	}

	// ── 辅助: 关闭 backpressure goroutine ──
	closeDeltas := func() {
		if deltaCh != nil {
			close(deltaCh)
			<-deltaDone // 等待消费者处理完剩余事件
		}
	}

	// ── 构建请求体（含 stream_options）──
	buildBody := func() []byte {
		messages := []map[string]string{{"role": "user", "content": prompt}}
		if systemPrompt != "" {
			messages = append([]map[string]string{{"role": "system", "content": systemPrompt}}, messages...)
		}
		bodyMap := map[string]any{
			"model":       model,
			"messages":    messages,
			"max_tokens":  2048,
			"temperature": 0.2,
			"stream":      true,
		}
		// 1.2.3 stream_options: 让 API 在流末尾返回 usage
		bodyMap["stream_options"] = map[string]any{"include_usage": true}
		b, _ := json.Marshal(bodyMap)
		return b
	}

	// ── 1.2.1 断线重连: doStreamCall 返回 (已经流到的文本, ChatResult, error) ──
	doStreamCall := func() (string, ChatResult, error) {
		body := buildBody()
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return "", ChatResult{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "text/event-stream")

		client := &http.Client{Timeout: cfg.RequestTimeout}
		resp, err := client.Do(req)
		if err != nil {
			return "", ChatResult{}, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			return "", ChatResult{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
		}

		var fullText strings.Builder
		tokenOut := 0
		var streamUsage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		}

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
					Delta struct{ Content string `json:"content"` } `json:"delta"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			for _, c := range chunk.Choices {
				if c.Delta.Content != "" {
					fullText.WriteString(c.Delta.Content)
					tokenOut++
					sendDelta(c.Delta.Content)
				}
			}
			// 1.2.3: 捕获流末尾的 usage（如果 provider 支持 stream_options）
			if chunk.Usage != nil {
				streamUsage = chunk.Usage
			}
		}

		// scanner 错误（网络断开等）也视为可重试错误
		if err := scanner.Err(); err != nil {
			partialText := fullText.String()
			// 如果已经收到了部分内容，返回部分结果而非错误
			if partialText != "" {
				return partialText, ChatResult{
					Text: partialText, TokenIn: CountTokens(prompt),
					TokenOut: tokenOut, Source: model,
				}, nil
			}
			return "", ChatResult{}, fmt.Errorf("SSE scanner error: %w", err)
		}

		// 优先用 stream 里的 usage,其次用 tokenOut 估算
		if streamUsage != nil {
			return "", ChatResult{
				Text: fullText.String(),
				TokenIn:  streamUsage.PromptTokens,
				TokenOut: streamUsage.CompletionTokens,
				Source:   model,
			}, nil
		}
		return "", ChatResult{
			Text: fullText.String(), TokenIn: CountTokens(prompt),
			TokenOut: tokenOut, Source: model,
		}, nil
	}

	// ── 重试循环 ──
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	var lastErr error
	accumulatedText := "" // 跨重试保留的部分文本

	for attempt := 0; attempt <= maxRetries; attempt++ {
		partial, result, err := doStreamCall()
		if err == nil {
			closeDeltas()
			// 如果有之前重试累积的文本，拼上去
			if accumulatedText != "" {
				result.Text = accumulatedText + result.Text
			}
			return result, nil
		}

		lastErr = err
		errStr := err.Error()
		retryable := strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "500") ||
			strings.Contains(errStr, "502") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "EOF") ||
			strings.Contains(errStr, "SSE scanner error")

		if !retryable || attempt == maxRetries {
			closeDeltas()
			// 如果有部分文本，返回它（比完全失败强）
			outText := accumulatedText + partial
			if outText != "" {
				return ChatResult{
					Text: outText, TokenIn: CountTokens(prompt),
					TokenOut: CountTokens(outText), Source: model,
				}, nil
			}
			return ChatResult{}, lastErr
		}

		// 保留部分文本，重试
		if partial != "" {
			accumulatedText = partial
		}
		backoff := cfg.RetryBackoffBase * time.Duration(1<<attempt)
		NewSlog("agentclient").Info("D27 SSE 重试 /(等 ,已收 字)", "attempt1", attempt+1, "maxRetries", maxRetries, "backoff", backoff, "lenruneaccumulatedText", len([]rune(accumulatedText)), "err", err)
		time.Sleep(backoff)
	}

	closeDeltas()
	return ChatResult{}, lastErr
}

// ── D29 多 provider 适配 ──
// 已迁移至 provider_adapter.go。
// callOpenAICompatibleWithRetry 现在委托给 callProviderWithRetry（自动按 baseURL 检测 provider）。
