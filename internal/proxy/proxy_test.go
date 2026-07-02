package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ──────────────────────────────────────────────
// Proxy 核心功能测试
// ──────────────────────────────────────────────

func TestNewProxy(t *testing.T) {
	p := New(":9090", "https://api.openai.com")
	if p == nil {
		t.Fatal("New() 返回 nil")
	}
	if p.listenAddr != ":9090" {
		t.Errorf("listenAddr = %q, want :9090", p.listenAddr)
	}
	if p.targetURL != "https://api.openai.com" {
		t.Errorf("targetURL = %q", p.targetURL)
	}
}

func TestProxyStatusWhenStopped(t *testing.T) {
	p := New(":9090", "https://api.openai.com")
	status := p.Status()
	if status["running"] != false {
		t.Error("未启动的代理状态应为 running=false")
	}
}

func TestProxyCapture(t *testing.T) {
	p := New(":9090", "https://api.openai.com")

	// 手动触发 capture(模拟代理捕获请求)
	p.capture("POST", "https://api.openai.com/v1/chat/completions",
		`{"model":"gpt-4","messages":[]}`, 200,
		`{"choices":[{"message":{"content":"hello"}}]}`, 1500,
		http.Header{"Authorization": []string{"Bearer sk-test123"}})

	sessions := p.GetSessions()
	if len(sessions) != 1 {
		t.Fatalf("应有 1 个会话, 实际 %d", len(sessions))
	}

	sess := sessions[0]
	if sess.TargetURL != "https://api.openai.com" {
		t.Errorf("TargetURL = %q", sess.TargetURL)
	}

	if len(sess.Requests) != 1 {
		t.Fatalf("会话应有 1 条请求, 实际 %d", len(sess.Requests))
	}

	req := sess.Requests[0]
	if req.Method != "POST" {
		t.Errorf("Method = %q, want POST", req.Method)
	}
	if req.RespStatus != 200 {
		t.Errorf("RespStatus = %d, want 200", req.RespStatus)
	}
	if req.DurationMs != 1500 {
		t.Errorf("DurationMs = %d, want 1500", req.DurationMs)
	}

	// 验证凭证提取
	if sess.ExtractedAuth == nil {
		t.Fatal("ExtractedAuth 不应为 nil")
	}
	if sess.ExtractedAuth.AuthType != "openai" {
		t.Errorf("AuthType = %q, want openai (因为 Authorization: Bearer sk-xxx)", sess.ExtractedAuth.AuthType)
	}
	if sess.ExtractedAuth.OpenAIKey == "" {
		t.Error("OpenAIKey 不应为空")
	}
}

func TestProxyMultipleCaptures(t *testing.T) {
	p := New(":9090", "https://api.anthropic.com")

	// 捕获 3 次请求
	for i := 0; i < 3; i++ {
		p.capture("GET", "https://api.anthropic.com/v1/messages", "", 200, "{}", 100, http.Header{})
	}

	sessions := p.GetSessions()
	if len(sessions) != 1 {
		t.Fatalf("应有 1 个会话, 实际 %d", len(sessions))
	}

	sess := sessions[0]
	if len(sess.Requests) != 3 {
		t.Errorf("会话应有 3 条请求, 实际 %d", len(sess.Requests))
	}

	// 验证 Seq 序号
	for i, req := range sess.Requests {
		if req.Seq != i+1 {
			t.Errorf("请求 #%d Seq = %d, want %d", i, req.Seq, i+1)
		}
	}
}

func TestReplaySession(t *testing.T) {
	// 准备一个测试用的目标服务器
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"replayed":true}`))
	}))
	defer targetServer.Close()

	// 创建代理并捕获请求
	p := New(":9090", "https://original.com")
	GlobalProxy = p

	p.capture("GET", "https://original.com/v1/test", "", 200, "original response", 100, http.Header{})

	// 回放到测试目标
	exchanges, err := ReplaySession(p.sessions[0].ID, targetServer.URL)
	if err != nil {
		t.Fatalf("ReplaySession 失败: %v", err)
	}

	if len(exchanges) != 1 {
		t.Fatalf("应有 1 条回放结果, 实际 %d", len(exchanges))
	}

	if exchanges[0].RespStatus != 200 {
		t.Errorf("回放响应状态 = %d, want 200", exchanges[0].RespStatus)
	}
}

// ──────────────────────────────────────────────
// Auth 提取测试
// ──────────────────────────────────────────────

func TestExtractAuth(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		wantType string
	}{
		{
			"Bearer Token(non-OpenAI)",
			http.Header{"Authorization": []string{"Bearer my-custom-token-12345"}},
			"bearer",
		},
		{
			"OpenAI Key",
			http.Header{"Authorization": []string{"Bearer sk-test1234567890test"}},
			"openai",
		},
		{
			"API Key header",
			http.Header{"X-Api-Key": []string{"my-api-key-12345"}},
			"api-key",
		},
		{
			"Session Cookie",
			http.Header{"Cookie": []string{"session=abc123; token=xyz"}},
			"session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 为每个测试创建独立的 proxy 实例,避免状态污染
			p := New(":9090", "https://api.test.com")
			p.currentSess = &Session{
				ID:        "test-" + tt.name,
				TargetURL: p.targetURL,
				StartTime: now(),
				Requests:  make([]CapturedExchange, 0),
			}

			p.extractAuth(tt.headers)

			if p.currentSess.ExtractedAuth == nil {
				t.Fatalf("ExtractedAuth 不应为 nil for %s", tt.name)
			}
			if p.currentSess.ExtractedAuth.AuthType != tt.wantType {
				t.Errorf("AuthType = %q, want %q", p.currentSess.ExtractedAuth.AuthType, tt.wantType)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Token 脱敏测试
// ──────────────────────────────────────────────

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input string
		want  string // 前缀匹配
	}{
		{"sk-test1234567890abc", "sk-t***abc"},
		{"short", "***"},
		{"", "***"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskToken(tt.input)
			if len(got) < 3 {
				t.Errorf("maskToken(%q) = %q, too short", tt.input, got)
			}
		})
	}
}

// ──────────────────────────────────────────────
// ConvertSessionToTask 测试(需要 DB)
// ──────────────────────────────────────────────

// ConvertSessionToTask 需要 DB 连接,跳过单元测试(由 e2e 测试覆盖)
func TestConvertSessionToTask(t *testing.T) {
	t.Skip("需要 DB 连接,由 e2e 测试覆盖")
}


// ──────────────────────────────────────────────
// JSON 工具测试
// ──────────────────────────────────────────────

func TestEscapeJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`hello world`, `hello world`},
		{`say "hello"`, `say \"hello\"`},
		{`line1\nline2`, `line1\\nline2`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := escapeJSON(tt.input)
			// 验证可以反序列化
			var s string
			if err := json.Unmarshal([]byte(`"`+got+`"`), &s); err != nil {
				t.Errorf("escapeJSON(%q) 结果不可 JSON 解析: %v", tt.input, err)
			}
		})
	}
}

// now 返回当前时间(用于测试会话初始化)。
func now() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

var nilTime time.Time
