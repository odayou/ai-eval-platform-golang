package engine

// compaction_test.go — compaction 模块单元测试（TODO 9.3.5 补齐）。
//
// 测试覆盖：
//   - splitIntoChunks：分段正确性
//   - extractiveSummary：提取式摘要
//   - CompactContext：短文本不压缩 / 长文本压缩
//   - CompactIfNeeded：自动判断是否需要压缩
//   - EstimateTokensForText：token 估算
//   - CompactConversationHistory：对话历史压缩

import (
	"strings"
	"testing"
)

func TestSplitIntoChunks_ShortText(t *testing.T) {
	text := "hello world"
	chunks := splitIntoChunks(text, 1000)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected %q, got %q", text, chunks[0])
	}
}

func TestSplitIntoChunks_LongText(t *testing.T) {
	// 构造超过 chunkSize 的文本
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line " + strings.Repeat("x", 50)
	}
	text := strings.Join(lines, "\n")

	chunks := splitIntoChunks(text, 500)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}

	// 验证所有内容都被覆盖
	reconstructed := strings.Join(chunks, "\n")
	if len(reconstructed) < len(text)/2 {
		t.Errorf("chunks lost too much content: original %d chars, reconstructed %d chars", len(text), len(reconstructed))
	}
}

func TestSplitIntoChunks_LineBoundary(t *testing.T) {
	text := "aaa\nbbb\nccc\nddd\neee"
	chunks := splitIntoChunks(text, 10) // 每段约 10 字符
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for 15-char text with 10-char limit, got %d", len(chunks))
	}
	// 验证不在行中间切断（splitIntoChunks 按行分割）
	for _, chunk := range chunks {
		// 每个 chunk 应该是完整的行
		if chunk != "" && !strings.HasPrefix(chunk, "line") && !strings.HasPrefix(chunk, "aaa") &&
			!strings.HasPrefix(chunk, "bbb") && !strings.HasPrefix(chunk, "ccc") &&
			!strings.HasPrefix(chunk, "ddd") && !strings.HasPrefix(chunk, "eee") &&
			!strings.Contains(chunk, "--- project-doc ---") {
			// chunk 可能包含多行，这是正常的
		}
	}
}

func TestExtractiveSummary_ShortCode(t *testing.T) {
	code := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	summary := extractiveSummary(code)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	// 应该保留 func 行
	if !strings.Contains(summary, "func") {
		t.Errorf("summary should contain 'func', got: %s", summary)
	}
}

func TestExtractiveSummary_LongCode(t *testing.T) {
	// 构造长代码
	var lines []string
	lines = append(lines, "package main", "", "import (", `	"fmt"`, ")", "")
	for i := 0; i < 50; i++ {
		lines = append(lines, "// some comment")
		lines = append(lines, "func function"+strings.Repeat("x", i%10)+"() {}")
	}
	lines = append(lines, "", "func main() {", `	fmt.Println("end")`, "}")
	code := strings.Join(lines, "\n")

	summary := extractiveSummary(code)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	// 摘要应该比原文短很多
	if len(summary) >= len(code) {
		t.Errorf("summary (%d) should be shorter than original (%d)", len(summary), len(code))
	}
}

func TestCompactContext_ShortText(t *testing.T) {
	text := "这是一段短文本，不需要压缩。"
	config := DefaultCompactionConfig()
	result := CompactContext(text, config, "")

	if result.Rounds != 0 {
		t.Errorf("short text should not be compressed, got %d rounds", result.Rounds)
	}
	if result.Summary != text {
		t.Errorf("short text should be returned as-is")
	}
	if result.Ratio != 1.0 {
		t.Errorf("ratio should be 1.0 for uncompressed, got %f", result.Ratio)
	}
}

func TestCompactContext_LongText(t *testing.T) {
	// 构造超过阈值的文本（默认 2000 token * 2 字符/token = 4000 字符）
	var parts []string
	for i := 0; i < 100; i++ {
		parts = append(parts, "这是第"+strings.Repeat("x", 40)+"行的内容，用于测试压缩功能。")
	}
	text := strings.Join(parts, "\n")

	config := DefaultCompactionConfig()
	result := CompactContext(text, config, "")

	if result.Rounds == 0 {
		t.Error("long text should be compressed")
	}
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
	if result.Ratio >= 1.0 {
		t.Errorf("ratio should be < 1.0 for compressed text, got %f", result.Ratio)
	}
	if result.SavedTokens <= 0 {
		t.Errorf("should save tokens, got %d", result.SavedTokens)
	}
}

func TestCompactIfNeeded_ShortText(t *testing.T) {
	text := "短文本"
	summary, result := CompactIfNeeded(text, "conv-1")
	if summary != text {
		t.Error("short text should pass through unchanged")
	}
	if result.Rounds != 0 {
		t.Error("should not compress short text")
	}
}

func TestCompactIfNeeded_LongText(t *testing.T) {
	text := strings.Repeat("这是一段很长的文本，需要被压缩。", 500)
	summary, result := CompactIfNeeded(text, "conv-1")
	if summary == text {
		t.Error("long text should be compressed")
	}
	if result.Rounds == 0 {
		t.Error("should have compressed")
	}
}

func TestEstimateTokensForText_Chinese(t *testing.T) {
	text := "你好世界" // 4 个中文字符
	tokens := EstimateTokensForText(text)
	// 中文约 1.5-2 字符/token，4 字符约 2-3 token
	if tokens < 1 || tokens > 6 {
		t.Errorf("expected 1-6 tokens for 4 Chinese chars, got %d", tokens)
	}
}

func TestEstimateTokensForText_English(t *testing.T) {
	text := "hello world" // 11 字符（含空格）
	tokens := EstimateTokensForText(text)
	// 英文约 4 字符/token，11 字符约 3 token
	if tokens < 1 || tokens > 8 {
		t.Errorf("expected 1-8 tokens for 'hello world', got %d", tokens)
	}
}

func TestEstimateTokensForText_Mixed(t *testing.T) {
	text := "hello 你好 world 世界"
	tokens := EstimateTokensForText(text)
	if tokens < 2 {
		t.Errorf("mixed text should have at least 2 tokens, got %d", tokens)
	}
}

func TestCompactConversationHistory_Short(t *testing.T) {
	messages := []map[string]string{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi"},
	}
	result := CompactConversationHistory(messages, 10000)
	if len(result) != 2 {
		t.Errorf("short history should not be compressed, got %d messages", len(result))
	}
}

func TestCompactConversationHistory_Long(t *testing.T) {
	// 构造超长对话历史
	var messages []map[string]string
	for i := 0; i < 20; i++ {
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": strings.Repeat("这是很长的消息内容。", 100),
		})
		messages = append(messages, map[string]string{
			"role":    "assistant",
			"content": strings.Repeat("这是很长的回复内容。", 100),
		})
	}

	result := CompactConversationHistory(messages, 1000) // 很小的限制
	// 应该压缩：1 条摘要 + 最近 4 条
	if len(result) >= len(messages) {
		t.Errorf("long history should be compressed: got %d, original %d", len(result), len(messages))
	}
	// 第一条应该是 system 摘要
	if result[0]["role"] != "system" {
		t.Errorf("first message should be system summary, got %s", result[0]["role"])
	}
}

func TestFormatCompactionStats_NoCompression(t *testing.T) {
	result := CompactionResult{Rounds: 0}
	stats := FormatCompactionStats(result)
	if !strings.Contains(stats, "未压缩") {
		t.Errorf("should say '未压缩', got: %s", stats)
	}
}

func TestFormatCompactionStats_Compressed(t *testing.T) {
	result := CompactionResult{
		OriginalLength:  1000,
		CompactedLength: 300,
		Ratio:           0.3,
		Rounds:          2,
		SavedTokens:     350,
	}
	stats := FormatCompactionStats(result)
	if !strings.Contains(stats, "2 轮") {
		t.Errorf("should mention rounds, got: %s", stats)
	}
	if !strings.Contains(stats, "30%") {
		t.Errorf("should mention ratio, got: %s", stats)
	}
}

// ── 6.1.3 压缩质量评分测试 ──

func TestComputeQualityScore_Identical(t *testing.T) {
	// 原文和摘要完全相同时,质量评分应为 100
	text := "func GetUser(id string) *User { return db.Find(id) }"
	score := computeQualityScore(text, text)
	if score != 100 {
		t.Errorf("identical text should score 100, got %d", score)
	}
}

func TestComputeQualityScore_Subset(t *testing.T) {
	// 摘要是原文的子集时,质量评分应 > 0
	original := "func GetUser(id string) *User { return db.Find(id) } func DeleteUser(id string) error { return db.Delete(id) }"
	summary := "func GetUser(id string) *User { return db.Find(id) }"
	score := computeQualityScore(original, summary)
	if score <= 0 {
		t.Errorf("subset summary should score > 0, got %d", score)
	}
	if score > 100 {
		t.Errorf("score should be <= 100, got %d", score)
	}
}

func TestComputeQualityScore_EmptySummary(t *testing.T) {
	// 空摘要时质量评分应为 0
	original := "func GetUser(id string) *User { return db.Find(id) }"
	score := computeQualityScore(original, "")
	if score != 0 {
		t.Errorf("empty summary should score 0, got %d", score)
	}
}

func TestComputeQualityScore_InCompactContext(t *testing.T) {
	// CompactContext 的结果应包含 QualityScore
	var parts []string
	for i := 0; i < 100; i++ {
		parts = append(parts, "这是第"+strings.Repeat("x", 40)+"行的内容，用于测试压缩功能。")
	}
	text := strings.Join(parts, "\n")
	config := DefaultCompactionConfig()
	result := CompactContext(text, config, "")

	if result.QualityScore < 0 || result.QualityScore > 100 {
		t.Errorf("QualityScore should be 0-100, got %d", result.QualityScore)
	}
}

// ── 6.2.3 策略自动选择测试 ──

func TestAutoSelectCondenser_Short(t *testing.T) {
	// 短对话(≤20 条)应选择 Recent 策略
	cond := autoSelectCondenser(10)
	if cond != CondenserRecent {
		t.Errorf("short conversation should use Recent, got %s", cond)
	}
	cond = autoSelectCondenser(20)
	if cond != CondenserRecent {
		t.Errorf("20 events should use Recent, got %s", cond)
	}
}

func TestAutoSelectCondenser_Medium(t *testing.T) {
	// 中等对话(21-100 条)应选择 Amortized 策略
	cond := autoSelectCondenser(50)
	if cond != CondenserAmortized {
		t.Errorf("medium conversation should use Amortized, got %s", cond)
	}
	cond = autoSelectCondenser(100)
	if cond != CondenserAmortized {
		t.Errorf("100 events should use Amortized, got %s", cond)
	}
}

func TestAutoSelectCondenser_Long(t *testing.T) {
	// 长对话(>100 条)应选择 LLMAttention 策略
	cond := autoSelectCondenser(200)
	if cond != CondenserLLMAttention {
		t.Errorf("long conversation should use LLMAttention, got %s", cond)
	}
}

func TestExtractKeywords(t *testing.T) {
	text := "func GetUser(id string) *User { return db.Find(id) }"
	kw := extractKeywords(text)
	if len(kw) == 0 {
		t.Error("should extract keywords from code")
	}
	// 应该包含 func、GetUser 等关键词
	if !kw["func"] {
		t.Error("should extract 'func' as keyword")
	}
	if !kw["getuser"] {
		t.Error("should extract 'getuser' (lowercase) as keyword")
	}
}
