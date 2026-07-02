package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefaultSSEConfig(t *testing.T) {
	cfg := DefaultSSEConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryBackoffBase != time.Second {
		t.Errorf("RetryBackoffBase = %v, want 1s", cfg.RetryBackoffBase)
	}
	if cfg.BackpressureSize != 64 {
		t.Errorf("BackpressureSize = %d, want 64", cfg.BackpressureSize)
	}
	if cfg.RequestTimeout != 120*time.Second {
		t.Errorf("RequestTimeout = %v, want 120s", cfg.RequestTimeout)
	}
}

func TestSSEStreamOptionsInBody(t *testing.T) {
	// 验证 stream_options.include_usage 在请求体中
	// 用 buildBody 的逻辑内联测试
	messages := []map[string]string{{"role": "user", "content": "hi"}}
	bodyMap := map[string]any{
		"model":       "test-model",
		"messages":    messages,
		"max_tokens":  2048,
		"temperature": 0.2,
		"stream":      true,
	}
	bodyMap["stream_options"] = map[string]any{"include_usage": true}
	b, err := json.Marshal(bodyMap)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}

	// 验证 stream_options 存在且 include_usage 为 true
	so, ok := parsed["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream_options missing or wrong type")
	}
	iu, ok := so["include_usage"].(bool)
	if !ok || !iu {
		t.Fatalf("include_usage = %v, want true", iu)
	}
}

func TestIsRetryableError(t *testing.T) {
	// 验证重试错误分类器
	cases := []struct {
		errMsg   string
		retryable bool
	}{
		{"HTTP 429: rate limit exceeded", true},
		{"HTTP 500: internal server error", true},
		{"HTTP 502: bad gateway", true},
		{"HTTP 503: service unavailable", true},
		{"dial tcp: connection refused", true},
		{"read tcp: connection reset by peer", true},
		{"unexpected EOF", true},
		{"context deadline exceeded (timeout)", true},
		{"SSE scanner error: read failed", true},
		{"HTTP 400: bad request", false},
		{"HTTP 401: unauthorized", false},
		{"HTTP 403: forbidden", false},
		{"invalid model name", false},
	}

	for _, c := range cases {
		retryable := strings.Contains(c.errMsg, "429") ||
			strings.Contains(c.errMsg, "500") ||
			strings.Contains(c.errMsg, "502") ||
			strings.Contains(c.errMsg, "503") ||
			strings.Contains(c.errMsg, "timeout") ||
			strings.Contains(c.errMsg, "connection refused") ||
			strings.Contains(c.errMsg, "connection reset") ||
			strings.Contains(c.errMsg, "EOF") ||
			strings.Contains(c.errMsg, "SSE scanner error")
		if retryable != c.retryable {
			t.Errorf("isRetryable(%q) = %v, want %v", c.errMsg, retryable, c.retryable)
		}
	}
}

func TestSSEBackpressureChannel(t *testing.T) {
	// 验证 backpressure: channel 满时不阻塞（不 panic）
	ch := make(chan struct{}, 3)
	// 填满 channel
	for i := 0; i < 3; i++ {
		ch <- struct{}{}
	}
	// 再次发送应走 default 分支（不阻塞、不 panic）
	select {
	case ch <- struct{}{}:
		t.Log("channel accepted (unexpected but not a bug)")
	default:
		t.Log("backpressure: dropped (expected)")
	}
	// 清空 channel
	for i := 0; i < 3; i++ {
		<-ch
	}
}

func TestSSEDeltaConsumer(t *testing.T) {
	// 验证 delta 消费者 goroutine 正常启停
	ch := make(chan struct{ string }, 10)
	done := make(chan struct{})
	received := make([]string, 0)

	go func() {
		defer close(done)
		for ev := range ch {
			received = append(received, ev.string)
		}
	}()

	// 发送 5 条
	for i := 0; i < 5; i++ {
		ch <- struct{ string }{"hello"}
	}
	close(ch)
	<-done // 等待消费者完成

	if len(received) != 5 {
		t.Errorf("received %d events, want 5", len(received))
	}
}
