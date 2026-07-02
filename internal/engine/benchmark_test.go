package engine

import (
	"testing"

	"ai-eval-platform/internal/store"
)

// BenchmarkClassifyIntent 评测意图分类性能。
func BenchmarkClassifyIntent(b *testing.B) {
	inputs := []string{
		"测一下这个 Agent 的编码能力",
		"比较 GPT-4o 和 DeepSeek 的竞品分析",
		"大模型 API 的指令遵循能力评测",
		"这个 PRD 需求文档写得怎么样",
		"这个 AI 产品的用户体验评测",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			ClassifyIntent(input, "")
		}
	}
}

// BenchmarkMockJudge 评测 mock Judge 性能。
func BenchmarkMockJudge(b *testing.B) {
	rule := store.RuleRow{
		ID:        "bench-rule",
		Name:      "性能检查",
		Dimension: "performance",
		Scoring:   "llm",
		Weight:    1.0,
		Content:   "判断代码是否存在性能问题",
	}
	answer := `func Process(data []int) int {
		sum := 0
		for _, v := range data {
			sum += v
		}
		return sum
	}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Judge(rule, "测试 prompt", answer)
	}
}

// BenchmarkRunMetric 评测 Metric 规则性能。
func BenchmarkRunMetric(b *testing.B) {
	rule := store.RuleRow{
		ID:        "bench-metric",
		Name:      "SQL注入检查",
		Dimension: "security",
		Scoring:   "metric",
		Content:   "检查 SQL 注入",
	}
	answers := []string{
		`func GetUser(db *sql.DB, name string) (*User, error) {
			return &User{}, db.QueryRow("SELECT * FROM users WHERE name = ?", name).Scan(...)
		}`,
		`db.Query(fmt.Sprintf("SELECT * FROM users WHERE name='%s'", name))`,
		`fmt.Println("hello world")`,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, ans := range answers {
			RunMetric(rule, ans)
		}
	}
}

// BenchmarkDetectLanguage 评测语言检测性能。
func BenchmarkDetectLanguage(b *testing.B) {
	samples := []string{
		`package main\nfunc main() { println("hello") }`,
		`def hello():\n    print("world")`,
		`const x = 1\nconsole.log(x)`,
		`<html><body>hello</body></html>`,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range samples {
			detectLanguage(s)
		}
	}
}

// BenchmarkRoundtableWeighted 评测圆桌加权汇总性能。
func BenchmarkRoundtableWeighted(b *testing.B) {
	opinions := []ExpertOpinion{
		{Role: "expert_security", Dimension: "security", Score: 85, Reason: "安全"},
		{Role: "expert_perf", Dimension: "performance", Score: 72, Reason: "性能"},
		{Role: "expert_arch", Dimension: "architecture", Score: 90, Reason: "架构"},
		{Role: "expert_product", Dimension: "product", Score: 78, Reason: "产品"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		weightedOverall(opinions)
		leaderSummarize(opinions)
	}
}

// BenchmarkReportBuild 评测报告聚合性能。
func BenchmarkReportBuild(b *testing.B) {
	results := make([]QuestionResult, 20)
	for i := range results {
		results[i] = QuestionResult{
			QuestionID: "q-bench",
			Title:      "测试题",
			Score:      float64(50 + i*2),
			IsBadCase:  i%5 == 0,
			RuleScores: []RuleScore{
				{Dimension: "security", Score: 85, Weight: 1.0},
				{Dimension: "performance", Score: 72, Weight: 1.0},
				{Dimension: "architecture", Score: 90, Weight: 1.0},
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildReport(results, true)
	}
}

// BenchmarkBreakerCheck 评测断路器检查性能。
func BenchmarkBreakerCheck(b *testing.B) {
	config := DefaultBreakerConfig()
	bs := NewBreakerState(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bs.Check(500, 1000)
	}
}

// BenchmarkGenerateEmbedding 评测 RAG 向量生成性能。
func BenchmarkGenerateEmbedding(b *testing.B) {
	texts := []string{
		"检查 SQL 注入漏洞",
		"判断是否存在并发死锁风险",
		"架构分层是否合理",
		"这是一个很长的测试文本,包含中文和 English 混合内容,用于测试 embedding 生成性能",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, t := range texts {
			generateEmbedding(t)
		}
	}
}

// BenchmarkRAGSearch 评测 RAG 检索性能。
func BenchmarkRAGSearch(b *testing.B) {
	// 构建一个小型索引
	rules := []store.RuleRow{
		{ID: "r1", Name: "SQL注入检查", Dimension: "security", Content: "检查 SQL 注入和拼接"},
		{ID: "r2", Name: "密钥泄露检查", Dimension: "security", Content: "检查 API Key 泄露"},
		{ID: "r3", Name: "死锁检查", Dimension: "performance", Content: "检查并发死锁"},
		{ID: "r4", Name: "内存泄漏检查", Dimension: "performance", Content: "检查 goroutine 泄漏"},
		{ID: "r5", Name: "分层检查", Dimension: "architecture", Content: "检查分层合理性"},
		{ID: "r6", Name: "耦合度检查", Dimension: "architecture", Content: "检查模块耦合度"},
		{ID: "r7", Name: "完整性检查", Dimension: "product", Content: "检查需求完整性"},
		{ID: "r8", Name: "可编译检查", Dimension: "correctness", Content: "检查代码可编译性"},
	}

	idx := &RAGIndex{
		embeddings: make([]RuleEmbedding, len(rules)),
		dimension:  128,
	}
	for i, r := range rules {
		idx.embeddings[i] = RuleEmbedding{
			RuleID: r.ID, RuleName: r.Name,
			Dimension: r.Dimension, Content: r.Content,
			Vector: generateEmbedding(r.Content),
		}
	}

	queries := []string{"SQL 安全防护", "高并发性能优化", "代码架构设计"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, q := range queries {
			idx.Search(q, 3, 0.3)
		}
	}
}
