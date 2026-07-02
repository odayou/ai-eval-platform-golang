package engine

// provider_adapter.go — D29 多 provider 真正适配层。
//
// 当前状态（1.3 之前）：
//   DetectProvider() 只是字符串匹配，AdaptModelName() 只加了个 "-20241022" 后缀，
//   所有实际 API 调用都经过 callOpenAICompatible（硬编码 OpenAI 协议）。
//
// 目标：
//   1. 统一 ProviderClient 接口（Chat + ChatStream）
//   2. OpenAI / Anthropic / Gemini 各自实现
//   3. callProvider 工厂函数自动检测 provider 并路由
//   4. 所有上层调用（agent loop / judge / compaction 等）无需感知 provider 差异

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ── Provider 枚举 ──

// LLMProvider 标识 LLM 厂商。
type LLMProvider string

const (
	ProviderOpenAI    LLMProvider = "openai"
	ProviderAnthropic LLMProvider = "anthropic"
	ProviderGemini    LLMProvider = "gemini"
	ProviderCustom    LLMProvider = "custom" // 兼容 OpenAI 协议的私有部署
)

// DetectProvider 从 baseURL 检测 LLM provider。
func DetectProvider(baseURL string) LLMProvider {
	lower := strings.ToLower(baseURL)
	if strings.Contains(lower, "anthropic") {
		return ProviderAnthropic
	}
	if strings.Contains(lower, "generativelanguage.googleapis") || strings.Contains(lower, "gemini") {
		return ProviderGemini
	}
	if strings.Contains(lower, "openai.com") {
		return ProviderOpenAI
	}
	return ProviderCustom // 私有部署/GLM/DeepSeek → 走 OpenAI 兼容协议
}

// ResolveProvider 解析 provider:显式 apiFormat 非空时优先使用,否则回退 DetectProvider(baseURL)。
//
// apiFormat 取值:"openai"|"anthropic"|"gemini"|"custom"|""(空=自动检测)。
// 用户在模型管理页选定了 API 格式后,优先按显式声明路由,避免 URL 猜测不准的问题。
func ResolveProvider(baseURL, apiFormat string) LLMProvider {
	switch strings.ToLower(strings.TrimSpace(apiFormat)) {
	case "openai":
		return ProviderOpenAI
	case "anthropic":
		return ProviderAnthropic
	case "gemini":
		return ProviderGemini
	case "custom":
		return ProviderCustom
	default: // 空或未知 → 自动检测
		return DetectProvider(baseURL)
	}
}

// ── ProviderClient 接口 ──

// ProviderClient 统一的 LLM provider 调用接口。
// 新 provider 只需实现此接口，上层代码无需关心厂商差异。
type ProviderClient interface {
	// Chat 单轮对话（非流式）。
	// systemPrompt 为空时退化为纯 user message。
	Chat(prompt, systemPrompt string) (ChatResult, error)
}

// ── 工厂: callProvider ──

// callProvider 自动检测 provider，调用对应的 API，带重试。
//
// 此函数替代原有的 callOpenAICompatible + callOpenAICompatibleWithRetry 组合，
// 上层函数（RunAgentLoop / judge / compaction 等）调用此函数即可自动适配所有 provider。
// apiFormat 非空时优先用显式声明,空则按 baseURL 自动检测。
func callProvider(baseURL, apiKey, apiFormat, model, prompt string) (ChatResult, error) {
	provider := ResolveProvider(baseURL, apiFormat)
	client := newProviderClient(provider, baseURL, apiKey, model)
	return client.Chat(prompt, "")
}

// callProviderWithSystem 带 system prompt 的 provider 调用。
func callProviderWithSystem(baseURL, apiKey, apiFormat, model, prompt, systemPrompt string) (ChatResult, error) {
	provider := ResolveProvider(baseURL, apiFormat)
	client := newProviderClient(provider, baseURL, apiKey, model)
	return client.Chat(prompt, systemPrompt)
}

// callProviderWithRetry 带重试 + 指数退避的 provider 调用（D28）。
//
// 直接替换原有的 callOpenAICompatibleWithRetry。
func callProviderWithRetry(baseURL, apiKey, apiFormat, model, prompt string) (ChatResult, error) {
	return callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, model, prompt, "")
}

// callProviderWithRetryAndSystem 带重试 + system prompt 的 provider 调用。
// 审计埋点 A:所有非流式 LLM 调用最终都经过这里(覆盖 judge/compaction/guardian/probe/router/roundtable 等)。
func callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, model, prompt, systemPrompt string) (ChatResult, error) {
	maxRetries := 3
	var lastErr error
	prov := ResolveProvider(baseURL, apiFormat)
	callStart := time.Now()
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := callProviderWithSystem(baseURL, apiKey, apiFormat, model, prompt, systemPrompt)
		if err == nil {
			// D30: 解析完整 usage
			parseFullUsage(&result, baseURL, apiKey, model)
			result.LatencyMs = time.Since(callStart).Milliseconds()
			// 审计埋点 A:记录成功调用
			RecordLLMAudit(AuditLLMFields{
				Caller:       currentAuditCaller(),
				Provider:     string(prov),
				Model:        model,
				BaseURL:      baseURL,
				APIFormat:    apiFormat,
				SystemPrompt: systemPrompt,
				Prompt:       prompt,
				Result:       result,
				Latency:      time.Since(callStart),
				Attempt:      attempt,
			})
			return result, nil
		}
		lastErr = err
		errStr := err.Error()
		retryable := strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "500") ||
			strings.Contains(errStr, "502") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "connection refused")
		if !retryable || attempt == maxRetries {
			latency := time.Since(callStart)
			// 审计埋点 A:记录失败调用
			RecordLLMAudit(AuditLLMFields{
				Caller:       currentAuditCaller(),
				Provider:     string(prov),
				Model:        model,
				BaseURL:      baseURL,
				APIFormat:    apiFormat,
				SystemPrompt: systemPrompt,
				Prompt:       prompt,
				Result:       result,
				Error:        err,
				Latency:      latency,
				Attempt:      attempt,
			})
			return result, err
		}
		backoff := time.Duration(1<<attempt) * time.Second
			// recovery_recipes 接入:先用恢复配方处理(如限流等待、超时重试)
			recovery := AttemptRecovery(errStr, nil)
			if recovery.Status == StatusEscalation {
				// 不可恢复的错误(如 401 认证),立即返回不重试
				NewSlog("provider").Info("recovery 升级(不重试): |", "Action", recovery.Action, "errStr", errStr)
				latency := time.Since(callStart)
				RecordLLMAudit(AuditLLMFields{
					Caller: currentAuditCaller(), Provider: string(prov), Model: model,
					BaseURL: baseURL, APIFormat: apiFormat, SystemPrompt: systemPrompt, Prompt: prompt,
					Result: result, Error: err, Latency: latency, Attempt: attempt,
				})
				return result, err
			}
			NewSlog("provider").Info("D28 重试 /(,等 ,recovery=)", "attempt1", attempt+1, "maxRetries", maxRetries, "prov", prov, "backoff", backoff, "Status", recovery.Status, "err", err)
			time.Sleep(backoff)
		}
	return ChatResult{}, lastErr
}

// ── 内部工厂 ──

func newProviderClient(provider LLMProvider, baseURL, apiKey, model string) ProviderClient {
	switch provider {
	case ProviderAnthropic:
		return &anthropicClient{baseURL: baseURL, apiKey: apiKey, model: model}
	case ProviderGemini:
		return &geminiClient{baseURL: baseURL, apiKey: apiKey, model: model}
	default:
		// OpenAI 兼容（含 GLM / DeepSeek / Kimi / 通义 / vLLM）
		return &openAICompatClient{baseURL: baseURL, apiKey: apiKey, model: model}
	}
}

// ── OpenAI 兼容 client（现有逻辑封装）──

type openAICompatClient struct {
	baseURL string
	apiKey  string
	model   string
}

func (c *openAICompatClient) Chat(prompt, systemPrompt string) (ChatResult, error) {
	url := strings.TrimRight(c.baseURL, "/") + "/chat/completions"

	messages := []map[string]string{{"role": "user", "content": prompt}}
	if systemPrompt != "" {
		messages = append([]map[string]string{{"role": "system", "content": systemPrompt}}, messages...)
	}

	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"max_tokens":  2048,
		"temperature": 0.2,
	}

	// DeepSeek 思考模式(thinking mode):当模型是 DeepSeek 时,自动启用思考模式。
	// 思考模式通过 thinking: {type: "enabled"} 和 reasoning_effort: "high" 控制。
	// 思考模式下 temperature/top_p 不生效(设置不会报错但被忽略),但出于兼容发送也无害。
	// 仅对非探活(probe)的请求启用,探活请求发"hi"不需要思考。
	if strings.Contains(strings.ToLower(c.baseURL), "deepseek") && !strings.Contains(prompt, "\"hi\"") && prompt != "hi" {
		body["thinking"] = map[string]string{"type": "enabled"}
		body["reasoning_effort"] = "high"
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return ChatResult{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var respBody struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"` // DeepSeek-R1/o1 等模型的思考链
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokenDetails  *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokenDetails *struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return ChatResult{}, err
	}
	if len(respBody.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("empty choices")
	}

	result := ChatResult{
		Text:      respBody.Choices[0].Message.Content,
		Reasoning: respBody.Choices[0].Message.ReasoningContent,
		TokenIn:   respBody.Usage.PromptTokens,
		TokenOut:  respBody.Usage.CompletionTokens,
		IsMock:    false,
		Source:    c.model,
	}
	// D30: 填入完整五维 usage
	result.Usage.PromptTokens = respBody.Usage.PromptTokens
	result.Usage.CompletionTokens = respBody.Usage.CompletionTokens
	if respBody.Usage.PromptTokenDetails != nil {
		result.Usage.CacheReadTokens = respBody.Usage.PromptTokenDetails.CachedTokens
	}
	if respBody.Usage.CompletionTokenDetails != nil {
		result.Usage.ReasoningTokens = respBody.Usage.CompletionTokenDetails.ReasoningTokens
	}
	return result, nil
}

// ── Anthropic client ──

type anthropicClient struct {
	baseURL string
	apiKey  string
	model   string
}

// anthropicRequest Anthropic Messages API 请求体。
type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"` // Anthropic 不支持 system role,用顶层字段
	Messages  []anthropicMessage  `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse Anthropic Messages API 响应体。
type anthropicResponse struct {
	Content []struct {
		Type     string `json:"type"`      // text | thinking | tool_use
		Text     string `json:"text"`      // type=text 时的正文
		Thinking string `json:"thinking"`  // type=thinking 时的思考内容
	} `json:"content"`
	Usage struct {
		InputTokens             int `json:"input_tokens"`
		OutputTokens            int `json:"output_tokens"`
		CacheReadInputTokens    int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *anthropicClient) Chat(prompt, systemPrompt string) (ChatResult, error) {
	// 归一化 baseURL: 用户可能配 https://api.anthropic.com 或带 /v1
	baseURL := strings.TrimRight(c.baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1/messages") {
		if !strings.HasSuffix(baseURL, "/v1") {
			baseURL += "/v1"
		}
		baseURL += "/messages"
	}

	reqBody := anthropicRequest{
		Model:     adaptAnthropicModel(c.model),
		MaxTokens: 2048,
		System:    systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}
	jsonBody, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	// 检查 HTTP 状态码(DeepSeek 等非标 Anthropic 实现可能在 4xx 返回空 body)
	if resp.StatusCode != http.StatusOK {
		if len(raw) == 0 {
			return ChatResult{}, fmt.Errorf("anthropic: HTTP %d (empty body)", resp.StatusCode)
		}
		// 可能包含错误信息
		var errResp struct {
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if json.Unmarshal(raw, &errResp) == nil && errResp.Error != nil {
			return ChatResult{}, fmt.Errorf("anthropic: HTTP %d: %s: %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
		}
		return ChatResult{}, fmt.Errorf("anthropic: HTTP %d (body=%s)", resp.StatusCode, truncateForDisplay(string(raw), 200))
	}

	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return ChatResult{}, fmt.Errorf("anthropic: parse error: %w (body=%s)", err, truncateForDisplay(string(raw), 200))
	}

	if ar.Error != nil {
		return ChatResult{}, fmt.Errorf("anthropic: %s: %s", ar.Error.Type, ar.Error.Message)
	}

	// 合并所有 content block 的 text,并提取 thinking(思考链)
	var text strings.Builder
	var reasoning strings.Builder
	for _, block := range ar.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		}
	}

	// 容错:DeepSeek 走 /anthropic 端点时,某些响应 content block 的 type
	// 可能不是标准 "text"(如返回空 type 或非标格式),导致 text 为空。
	// 此时尝试从所有 block 里提取任何非空文本字段,避免误判为"空响应"。
	if text.Len() == 0 {
		for _, block := range ar.Content {
			// 优先取 Text 字段(即使 type 不匹配),其次尝试 Thinking
			if block.Text != "" {
				text.WriteString(block.Text)
			} else if block.Thinking != "" {
				// 某些模型把正文塞进 thinking 字段
				text.WriteString(block.Thinking)
			}
		}
	}

	if text.Len() == 0 {
		return ChatResult{}, fmt.Errorf("anthropic: empty response content (blocks=%d)", len(ar.Content))
	}

	return ChatResult{
		Text:      text.String(),
		Reasoning: reasoning.String(),
		TokenIn:   ar.Usage.InputTokens,
		TokenOut:  ar.Usage.OutputTokens,
		Usage: TokenUsage{
			PromptTokens:        ar.Usage.InputTokens,
			CompletionTokens:    ar.Usage.OutputTokens,
			CacheReadTokens:     ar.Usage.CacheReadInputTokens,
			CacheCreationTokens: ar.Usage.CacheCreationInputTokens,
		},
		IsMock: false,
		Source: c.model,
	}, nil
}

// adaptAnthropicModel 适配 Anthropic 模型名。
// Claude 3.5 Sonnet 需要带日期后缀 "-20241022"，否则 API 报错。
func adaptAnthropicModel(modelRef string) string {
	lower := strings.ToLower(modelRef)
	// claude-3.5-sonnet / claude-3-5-sonnet → 带日期尾缀
	if strings.Contains(lower, "claude-3.5-sonnet") || strings.Contains(lower, "claude-3-5-sonnet") {
		if !strings.Contains(modelRef, "20") {
			return modelRef + "-20241022"
		}
	}
	// claude-3.5-haiku → 同样需要日期
	if strings.Contains(lower, "claude-3.5-haiku") || strings.Contains(lower, "claude-3-5-haiku") {
		if !strings.Contains(modelRef, "20") {
			return modelRef + "-20241022"
		}
	}
	return modelRef
}

// ── Gemini client ──

type geminiClient struct {
	baseURL string
	apiKey  string
	model   string
}

// geminiRequest Gemini generateContent API 请求体。
type geminiRequest struct {
	SystemInstruction *geminiContent `json:"system_instruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
}

type geminiContent struct {
	Role  string         `json:"role,omitempty"`
	Parts []geminiPart   `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

// geminiResponse Gemini generateContent API 响应体。
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Role  string       `json:"role"`
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *geminiClient) Chat(prompt, systemPrompt string) (ChatResult, error) {
	// 归一化 baseURL 并拼接 model + API key
	baseURL := strings.TrimRight(c.baseURL, "/")
	lower := strings.ToLower(baseURL)

	// 非 Google 系端点退回 OpenAI 兼容（用户可能配了代理）
	// 识别条件：URL 包含 generativelanguage / gemini / google 关键字
	if !strings.Contains(lower, "generativelanguage") &&
		!strings.Contains(lower, "gemini") &&
		!strings.Contains(lower, "google") {
		return (&openAICompatClient{c.baseURL, c.apiKey, c.model}).Chat(prompt, systemPrompt)
	}

	// 构建 URL: {base}/models/{model}:generateContent?key={apiKey}
	apiURL := fmt.Sprintf("%s/models/%s:generateContent", baseURL, c.model)
	parsed, _ := url.Parse(apiURL)
	q := parsed.Query()
	q.Set("key", c.apiKey)
	parsed.RawQuery = q.Encode()

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{{Text: prompt}},
			},
		},
	}
	if systemPrompt != "" {
		reqBody.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}
	jsonBody, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", parsed.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return ChatResult{}, fmt.Errorf("gemini: parse error: %w (body=%s)", err, truncateForDisplay(string(raw), 200))
	}

	if gr.Error != nil {
		return ChatResult{}, fmt.Errorf("gemini: code=%d: %s", gr.Error.Code, gr.Error.Message)
	}

	if len(gr.Candidates) == 0 {
		return ChatResult{}, fmt.Errorf("gemini: empty candidates")
	}

	// 合并所有 parts
	var text strings.Builder
	for _, part := range gr.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}

	result := ChatResult{
		Text:   text.String(),
		IsMock: false,
		Source: c.model,
	}

	// Gemini usage 用不同的字段名
	if gr.UsageMetadata != nil {
		result.TokenIn = gr.UsageMetadata.PromptTokenCount
		result.TokenOut = gr.UsageMetadata.CandidatesTokenCount
		result.Usage.PromptTokens = gr.UsageMetadata.PromptTokenCount
		result.Usage.CompletionTokens = gr.UsageMetadata.CandidatesTokenCount
		// Gemini 目前无独立 cache/reasoning 字段
	} else {
		// fallback: 精确 token 计数
		result.TokenIn = CountTokens(prompt)
		result.TokenOut = CountTokens(result.Text)
		result.Usage.PromptTokens = result.TokenIn
		result.Usage.CompletionTokens = result.TokenOut
	}

	return result, nil
}
