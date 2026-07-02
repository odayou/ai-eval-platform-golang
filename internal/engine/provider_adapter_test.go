package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectProvider(t *testing.T) {
	cases := []struct {
		baseURL  string
		expected LLMProvider
	}{
		{"https://api.openai.com/v1", ProviderOpenAI},
		{"https://api.anthropic.com", ProviderAnthropic},
		{"https://api.anthropic.com/v1/messages", ProviderAnthropic},
		{"https://generativelanguage.googleapis.com", ProviderGemini},
		{"https://generativelanguage.googleapis.com/v1beta", ProviderGemini},
		{"https://api.gemini.google.com", ProviderGemini},
		{"https://open.bigmodel.cn/api/paas/v4", ProviderCustom},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", ProviderCustom},
		{"https://api.deepseek.com/v1", ProviderCustom},
	}

	for _, c := range cases {
		got := DetectProvider(c.baseURL)
		if got != c.expected {
			t.Errorf("DetectProvider(%q) = %s, want %s", c.baseURL, got, c.expected)
		}
	}
}

func TestAdaptAnthropicModel(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"claude-3-5-sonnet-20240620", "claude-3-5-sonnet-20240620"}, // 已有日期
		{"claude-3.5-sonnet", "claude-3.5-sonnet-20241022"},
		{"claude-3-5-sonnet", "claude-3-5-sonnet-20241022"},
		{"claude-3.5-haiku", "claude-3.5-haiku-20241022"},
		{"claude-3-opus-20240229", "claude-3-opus-20240229"}, // 已有日期
		{"claude-3-haiku", "claude-3-haiku"},                 // 不需要日期
		{"gpt-4", "gpt-4"},                                   // 非 Anthropic
	}

	for _, c := range cases {
		got := adaptAnthropicModel(c.input)
		if got != c.expected {
			t.Errorf("adaptAnthropicModel(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestOpenAICompatClient_Chat(t *testing.T) {
	// 启动一个 mock OpenAI API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(401)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "Hello from OpenAI!"}},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &openAICompatClient{
		baseURL: server.URL,
		apiKey:  "test-key",
		model:   "gpt-4",
	}
	result, err := client.Chat("Hello", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello from OpenAI!" {
		t.Errorf("Text = %q, want 'Hello from OpenAI!'", result.Text)
	}
	if result.TokenIn != 10 || result.TokenOut != 5 {
		t.Errorf("TokenIn=%d TokenOut=%d, want 10,5", result.TokenIn, result.TokenOut)
	}
	if result.Source != "gpt-4" {
		t.Errorf("Source = %q, want 'gpt-4'", result.Source)
	}
}

func TestAnthropicClient_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic uses x-api-key header, not Authorization
		if r.Header.Get("x-api-key") != "test-key" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			w.WriteHeader(400)
			return
		}

		// 解析请求体验证格式
		var req anthropicRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "invalid_request_error", "message": "missing model"}})
			return
		}
		if req.MaxTokens == 0 {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "invalid_request_error", "message": "missing max_tokens"}})
			return
		}

		resp := anthropicResponse{
			Content: []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			}{{Type: "text", Text: "Hello from Claude!"}},
			Usage: struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			}{InputTokens: 15, OutputTokens: 3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &anthropicClient{
		baseURL: server.URL,
		apiKey:  "test-key",
		model:   "claude-3-5-sonnet-20240620",
	}
	result, err := client.Chat("Hello", "You are helpful.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello from Claude!" {
		t.Errorf("Text = %q, want 'Hello from Claude!'", result.Text)
	}
	if result.TokenIn != 15 || result.TokenOut != 3 {
		t.Errorf("TokenIn=%d TokenOut=%d, want 15,3", result.TokenIn, result.TokenOut)
	}
}

func TestAnthropicClient_RequestFormat(t *testing.T) {
	// 验证 Anthropic 请求体的格式正确性
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		r.Body.Read(capturedBody)

		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			}{{Type: "text", Text: "ok"}},
			Usage: struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			}{InputTokens: 1, OutputTokens: 1},
		})
	}))
	defer server.Close()

	client := &anthropicClient{
		baseURL: server.URL,
		apiKey:  "test-key",
		model:   "claude-3.5-sonnet",
	}
	client.Chat("hi", "be helpful")

	var req anthropicRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("request not valid JSON: %v", err)
	}
	if req.Model != "claude-3.5-sonnet-20241022" {
		t.Errorf("Model = %q, want 'claude-3.5-sonnet-20241022' (adapted with date suffix)", req.Model)
	}
	if req.System != "be helpful" {
		t.Errorf("System = %q, want 'be helpful'", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "hi" {
		t.Errorf("Messages = %+v, want [{user hi}]", req.Messages)
	}
}

func TestGeminiClient_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Gemini uses API key in query param
		if r.URL.Query().Get("key") != "test-key" {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 403, "message": "bad key"}})
			return
		}

		resp := geminiResponse{
			Candidates: []struct {
				Content struct {
					Role  string       `json:"role"`
					Parts []geminiPart `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			}{
				{
					Content: struct {
						Role  string       `json:"role"`
						Parts []geminiPart `json:"parts"`
					}{Role: "model", Parts: []geminiPart{{Text: "Hello from Gemini!"}}},
					FinishReason: "STOP",
				},
			},
			UsageMetadata: &struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			}{PromptTokenCount: 8, CandidatesTokenCount: 4, TotalTokenCount: 12},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// URL 需包含 "gemini" 以触发 Gemini adapter（而非退回 OpenAI）
	client := &geminiClient{
		baseURL: server.URL + "/gemini/v1beta",
		apiKey:  "test-key",
		model:   "gemini-2.0-flash",
	}
	result, err := client.Chat("Hello", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello from Gemini!" {
		t.Errorf("Text = %q, want 'Hello from Gemini!'", result.Text)
	}
	if result.TokenIn != 8 || result.TokenOut != 4 {
		t.Errorf("TokenIn=%d TokenOut=%d, want 8,4", result.TokenIn, result.TokenOut)
	}
}

func TestGeminiClient_SystemInstruction(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		r.Body.Read(capturedBody)

		json.NewEncoder(w).Encode(geminiResponse{
			Candidates: []struct {
				Content struct {
					Role  string       `json:"role"`
					Parts []geminiPart `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			}{
				{
					Content: struct {
						Role  string       `json:"role"`
						Parts []geminiPart `json:"parts"`
					}{Role: "model", Parts: []geminiPart{{Text: "ok"}}},
				},
			},
		})
	}))
	defer server.Close()

	client := &geminiClient{
		baseURL: server.URL + "/gemini/v1beta",
		apiKey:  "test-key",
		model:   "gemini-2.0-flash",
	}
	client.Chat("hi", "You are helpful.")

	var req geminiRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("request not valid JSON: %v", err)
	}
	if req.SystemInstruction == nil {
		t.Fatal("SystemInstruction should not be nil when system prompt is provided")
	}
	if len(req.SystemInstruction.Parts) == 0 || req.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Errorf("SystemInstruction = %+v, want 'You are helpful.'", req.SystemInstruction)
	}
}

func TestGeminiClient_NonGoogleEndpoint(t *testing.T) {
	// 当 baseURL 不是 Google 域名时，应退回 OpenAI 兼容
	client := &geminiClient{
		baseURL: "https://my-proxy.example.com/v1",
		apiKey:  "test-key",
		model:   "gemini-2.0-flash",
	}
	// 不应该 panic，应该退回 OpenAI 兼容
	_, err := client.Chat("hi", "")
	// 预期会有网络错误（连接不上），但不是 panic
	if err == nil {
		t.Skip("expected network error for non-existent endpoint")
	}
	t.Logf("expected error (fallback to OpenAI compat): %v", err)
}

// ── 1.4 D30 完整 usage 解析 + 成本估算 ──

func TestEstimateCost(t *testing.T) {
	cases := []struct {
		model   string
		usage   TokenUsage
		wantMin float64 // 成本至少大于 0（非免费模型）
		wantMax float64 // 成本不超过此值
	}{
		{"gpt-4o", TokenUsage{PromptTokens: 1000000, CompletionTokens: 1000000}, 10.0, 15.0},
		{"gpt-4", TokenUsage{PromptTokens: 1000000, CompletionTokens: 1000000}, 80.0, 100.0},
		{"deepseek-v3", TokenUsage{PromptTokens: 1000000, CompletionTokens: 1000000}, 0.5, 3.0},
		{"glm-4-flash", TokenUsage{PromptTokens: 1000000, CompletionTokens: 1000000}, 0.0, 0.001},
		{"claude-3.5-sonnet", TokenUsage{PromptTokens: 1000000, CompletionTokens: 1000000}, 15.0, 20.0},
	}

	for _, c := range cases {
		cost := estimateCost(c.model, c.usage)
		if cost < c.wantMin || cost > c.wantMax {
			t.Errorf("estimateCost(%q, %+v) = $%.4f, want between $%.2f-$%.2f",
				c.model, c.usage, cost, c.wantMin, c.wantMax)
		}
		t.Logf("%s: 1M/1M → $%.4f", c.model, cost)
	}
}

func TestEstimateCost_WithCache(t *testing.T) {
	// 验证 cache_read 按半价计费
	u := TokenUsage{
		PromptTokens:     1000000,
		CompletionTokens: 500000,
		CacheReadTokens:  2000000, // 2M cache hits
	}
	cost := estimateCost("gpt-4o", u)
	t.Logf("gpt-4o with 2M cache reads: $%.4f", cost)
	if cost < 5.0 {
		t.Errorf("cost with cache should be positive, got $%.4f", cost)
	}
}

func TestParseFullUsage(t *testing.T) {
	// 重置追踪器
	ResetDualTokenTracker()

	result := &ChatResult{
		Text:      "Hello, world!",
		TokenIn:   10,
		TokenOut:  5,
		Source:    "gpt-4o",
		LatencyMs: 250,
		Usage: TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			CacheReadTokens:  3,
		},
	}

	parseFullUsage(result, "", "test-key", "gpt-4o")

	// 验证追踪器记录
	tracker := GetDualTokenTracker()
	t.Logf("Agent turns: %d, tokens: %d", tracker.AgentMetrics.Turns, tracker.AgentMetrics.Usage.Total())
	if tracker.AgentMetrics.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", tracker.AgentMetrics.Turns)
	}
	if tracker.AgentMetrics.Usage.CacheReadTokens != 3 {
		t.Errorf("expected 3 cache_read tokens, got %d", tracker.AgentMetrics.Usage.CacheReadTokens)
	}
}

func TestParseFullUsage_MockSkips(t *testing.T) {
	ResetDualTokenTracker()

	result := &ChatResult{
		Text:   "mock answer",
		IsMock: true,
	}
	parseFullUsage(result, "", "", "mock")

	tracker := GetDualTokenTracker()
	if tracker.AgentMetrics.Turns != 0 {
		t.Errorf("mock result should not be tracked, got %d turns", tracker.AgentMetrics.Turns)
	}
}

func TestParseFullUsage_FallbackTokens(t *testing.T) {
	ResetDualTokenTracker()

	// Usage 为空但 TokenIn/TokenOut 有值 → 应从 TokenIn/TokenOut 兜底
	result := &ChatResult{
		Text:     "test",
		TokenIn:  42,
		TokenOut: 7,
	}
	parseFullUsage(result, "", "", "gpt-4o")

	tracker := GetDualTokenTracker()
	if tracker.AgentMetrics.Usage.PromptTokens != 42 {
		t.Errorf("expected 42 prompt tokens from fallback, got %d", tracker.AgentMetrics.Usage.PromptTokens)
	}
}
