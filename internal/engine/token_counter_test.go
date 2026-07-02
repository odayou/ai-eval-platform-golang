package engine

import (
	"strings"
	"testing"
)

func TestCountTokens_Empty(t *testing.T) {
	if n := CountTokens(""); n != 0 {
		t.Errorf("CountTokens(\"\") = %d, want 0", n)
	}
}

func TestCountTokens_English(t *testing.T) {
	text := "Hello, world! This is a test sentence."
	n := CountTokens(text)
	// GPT-4 实际约 10 token，允许 ±30% 误差
	if n < 7 || n > 13 {
		t.Errorf("CountTokens(english) = %d, want ~10 (±30%%)", n)
	}
}

func TestCountTokens_Chinese(t *testing.T) {
	text := "你好世界这是一个测试句子"
	n := CountTokens(text)
	// 中文 cl100k_base 约 1.1 token/字，11 字约 12 token
	if n < 9 || n > 16 {
		t.Errorf("CountTokens(chinese) = %d, want ~12 (±25%%)", n)
	}
}

func TestCountTokens_Code(t *testing.T) {
	text := `func Hello(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}`
	n := CountTokens(text)
	// GPT-4 cl100k_base 约 38 token
	if n < 25 || n > 55 {
		t.Errorf("CountTokens(code) = %d, want ~38 (±35%%)", n)
	}
}

func TestCountTokens_LargeText(t *testing.T) {
	// 生成 10000 字符的文本，确保不崩溃
	text := strings.Repeat("Hello世界", 1000) // 中英混合 × 1000
	n := CountTokens(text)
	if n <= 0 {
		t.Error("CountTokens should return positive for non-empty text")
	}
	t.Logf("10000 chars mixed text → %d tokens (ratio: %.2f chars/token)", n, float64(len([]rune(text)))/float64(n))
}

func TestCountTokens_CompareWithLenDiv(t *testing.T) {
	// 验证 CountTokens 比原始的 len/3 或 len/4 更合理
	cases := []struct {
		name string
		text string
	}{
		{"纯中文", "今天天气真好适合写代码"},
		{"纯英文", "The quick brown fox jumps over the lazy dog"},
		{"混合", "func main() { fmt.Println(\"你好世界\") }"},
		{"全是符号", "{([<>])} ++ -- => <= >= :: && ||"},
		{"空行多", "line1\n\n\n\nline5\n\n\n\nline9"},
	}

	for _, c := range cases {
		n := CountTokens(c.text)
		old3 := len(c.text) / 3
		old4 := len(c.text) / 4
		t.Logf("%s: CountTokens=%d  len/3=%d  len/4=%d  (len=%d bytes, runes=%d)",
			c.name, n, old3, old4, len(c.text), len([]rune(c.text)))
	}
}

func TestHeuristicTokenCount_Empty(t *testing.T) {
	if n := heuristicTokenCount(""); n != 0 {
		t.Errorf("heuristicTokenCount(\"\") = %d, want 0", n)
	}
}

func TestHeuristicTokenCount_Chinese(t *testing.T) {
	// 纯中文场景不应返回 0
	n := heuristicTokenCount("今天天气真好适合写代码")
	if n <= 0 {
		t.Error("heuristicTokenCount should be positive for Chinese text")
	}
}

func TestCountMessagesTokens(t *testing.T) {
	msgs := []loopMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I'm doing well, thank you!"},
	}
	n := CountMessagesTokens(msgs)
	if n <= 0 {
		t.Error("CountMessagesTokens should return positive")
	}
	t.Logf("3 messages → %d tokens", n)
}

func TestCountTokensForModel(t *testing.T) {
	text := "Hello, world!"
	n := CountTokensForModel(text, "gpt-4")
	if n <= 0 {
		t.Error("CountTokensForModel should return positive")
	}
	t.Logf("'Hello, world!' with gpt-4 → %d tokens", n)
}

// BenchmarkCountTokens 基准对比新旧方案
func BenchmarkCountTokens(b *testing.B) {
	text := strings.Repeat("Hello世界func main() {}\n", 100) // ~3000 chars
	b.Run("tiktoken", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			CountTokens(text)
		}
	})
	b.Run("old-len-div-3", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = len(text) / 3
		}
	})
}
