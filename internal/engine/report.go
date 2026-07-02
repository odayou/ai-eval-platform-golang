package engine

// report.go — 评测报告聚合。
//
// 输入:每道题的规则得分(RuleScore)列表 + 题目元信息。
// 输出:结构化报告 JSON,供前端 TaskReport 组件渲染雷达图。
//
// 报告结构:
//   {
//     "overall": 综合分,
//     "dimensions": { security: xx, performance: xx, ... },  ← 雷达图数据
//     "per_question": [ { question_id, title, score, ... } ],
//     "bad_cases": 数量,
//     "mock_mode": 是否全程 mock(无真实 Key)
//   }
//
// Bad Case 判定:单题加权总分 < BAD_CASE_THRESHOLD(默认 60)。
// 维度聚合:同一 dimension 下所有题的该维度规则得分取平均。

import (
	"ai-eval-platform/internal/store"
)

// BAD_CASE_THRESHOLD 单题总分低于此值记为 Bad Case。
const BAD_CASE_THRESHOLD = 60.0

// QuestionResult 是单道题的评测结果。
type QuestionResult struct {
	QuestionID   string           `json:"question_id"`
	Title        string           `json:"title"`
	Score        float64          `json:"score"`        // 该题加权总分
	IsBadCase    bool             `json:"is_bad_case"`  // 是否 Bad Case
	RuleScores   []RuleScore      `json:"rule_scores"`  // 每条规则明细
	Difficulty   string           `json:"difficulty"`
	Answer       string           `json:"answer"`
	Roundtable   *RoundtableResult `json:"roundtable,omitempty"` // 该题的圆桌合议结果
	Sandbox      *SandboxResult    `json:"sandbox,omitempty"`    // 沙箱编译执行结果
	CodeReview   *ReviewResult     `json:"code_review,omitempty"` // 结构化代码评审(P0-P3 findings)
}

// Report 是整个任务的评测报告。
type Report struct {
	Overall     float64                `json:"overall"`     // 综合分(所有题加权总分平均)
	Dimensions  map[string]float64     `json:"dimensions"`  // 各维度平均分(雷达图)
	PerQuestion []QuestionResult       `json:"per_question"`
	BadCases    int                    `json:"bad_cases"`
	MockMode    bool                   `json:"mock_mode"`   // 是否全程 mock
	TotalQuestions int                 `json:"total_questions"`
	Roundtable  []RoundtableResult     `json:"roundtable,omitempty"` // 圆桌合议结果(每题一个)
	Sandbox     SandboxSummary         `json:"sandbox"`     // 沙箱执行汇总
	Breaker     BreakerReport          `json:"breaker"`     // 断路器状态(费用/token 监控)
	Chaos       *ChaosSummary          `json:"chaos,omitempty"` // 混沌工程结果(开启时才有)
	Leakage     *LeakageSummary        `json:"leakage,omitempty"` // 反泄漏扫描结果(V2 9.5)
	DualToken   map[string]any         `json:"dual_token,omitempty"` // #52 双路 token 统计(agent+condenser)
	AgentProfile *AgentProfile         `json:"agent_profile,omitempty"` // Agent 能力画像(自适应评测)
}

// ChaosSummary 是混沌工程对抗模式的汇总(④.2)。
type ChaosSummary struct {
	Enabled  bool          `json:"enabled"`   // 是否开启了混沌
	Results  []ChaosResult `json:"results"`   // 各注入类型的结果
	AvgScore float64       `json:"avg_score"` // 鲁棒性平均分
}

// LeakageSummary 是反泄漏扫描的汇总(V2 9.5)。
type LeakageSummary struct {
	TotalScans  int              `json:"total_scans"`  // 扫描题目数
	FailedScans int              `json:"failed_scans"` // 发现隐患的题数
	AvgScore    int              `json:"avg_score"`    // 平均安全得分
	Findings    []LeakageFinding `json:"findings"`     // 所有隐患列表
}

// BreakerReport 是断路器在报告里的展示结构(钱包保护透明化)。
type BreakerReport struct {
	Tripped     bool    `json:"tripped"`      // 是否熔断
	TripReason  string  `json:"trip_reason"`  // 熔断原因
	TotalTokens int     `json:"total_tokens"` // 总 token 消耗
	TotalTurns  int     `json:"total_turns"`  // 总轮数
	EstCostUSD  float64 `json:"est_cost_usd"` // 估算费用(美元)
}

// SandboxSummary 是所有题的沙箱执行汇总指标。
type SandboxSummary struct {
	CompileOK    int     `json:"compile_ok"`     // 编译成功数
	CompileFail  int     `json:"compile_fail"`   // 编译失败数
	PassRate     float64 `json:"pass_rate"`      // 编译成功率
	AvgCompileMs int64   `json:"avg_compile_ms"` // 平均编译耗时
	TotalTests   int     `json:"total_tests"`    // 总测试数
	TestsPassed  int     `json:"tests_passed"`   // 通过测试数
	LintTotal    int     `json:"lint_total"`     // 总 Lint 错误
}

// BuildReport 聚合所有题的得分,生成报告。
// 当 roundtable 结果存在时,优先用圆桌的维度分(更准确)。
func BuildReport(results []QuestionResult, anyMock bool) Report {
	if len(results) == 0 {
		return Report{Dimensions: map[string]float64{}, MockMode: anyMock}
	}

	// 综合分 = 所有题加权总分平均
	var sum float64
	badCount := 0
	var rtResults []RoundtableResult
	for _, qr := range results {
		sum += qr.Score
		if qr.IsBadCase {
			badCount++
		}
		if qr.Roundtable != nil {
			rtResults = append(rtResults, *qr.Roundtable)
		}
	}
	overall := sum / float64(len(results))

	// 维度聚合:优先用圆桌维度分,回退到规则得分聚合
	dimensions := map[string]float64{}
	if len(rtResults) > 0 {
		// 圆桌模式:每题的各专家维度分取平均
		dimSums := map[string][]float64{}
		for _, rt := range rtResults {
			for _, op := range rt.Opinions {
				dimSums[op.Dimension] = append(dimSums[op.Dimension], op.Score)
			}
		}
		for dim, scores := range dimSums {
			dimensions[dim] = round1(average(scores))
		}
	} else {
		// 单判官模式:从规则得分聚合
		dimScores := map[string][]float64{}
		for _, qr := range results {
			for _, rs := range qr.RuleScores {
				if rs.Scoring == "manual" {
					continue
				}
				dimScores[rs.Dimension] = append(dimScores[rs.Dimension], rs.Score)
			}
		}
		for dim, scores := range dimScores {
			dimensions[dim] = round1(average(scores))
		}
	}

	return Report{
		Overall:        round1(overall),
		Dimensions:     dimensions,
		PerQuestion:    results,
		BadCases:       badCount,
		MockMode:       anyMock,
		TotalQuestions: len(results),
		Roundtable:     rtResults,
		Sandbox:        buildSandboxSummary(results),
		DualToken:      GetDualTokenTracker().Summary(), // #52 双路 token 统计写入报告
	}
}

// IsBadCase 判断单题是否为 Bad Case。
func IsBadCase(score float64) bool {
	return score < BAD_CASE_THRESHOLD
}

// round1 保留 1 位小数。
func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

// buildSandboxSummary 从所有题的沙箱结果中汇总指标。
func buildSandboxSummary(results []QuestionResult) SandboxSummary {
	var ss SandboxSummary
	var totalCompileMs int64
	compileCount := 0

	for _, qr := range results {
		if qr.Sandbox == nil {
			continue
		}
		sb := qr.Sandbox
		if sb.IsMock {
			continue
		}
		if sb.Success {
			ss.CompileOK++
		} else {
			ss.CompileFail++
		}
		if sb.CompileTime > 0 {
			totalCompileMs += sb.CompileTime
			compileCount++
		}
		ss.TotalTests += sb.TestTotal
		ss.TestsPassed += sb.TestPassed
		ss.LintTotal += sb.LintErrors
	}

	total := ss.CompileOK + ss.CompileFail
	if total > 0 {
		ss.PassRate = round1(float64(ss.CompileOK) / float64(total) * 100)
	}
	if compileCount > 0 {
		ss.AvgCompileMs = totalCompileMs / int64(compileCount)
	}
	return ss
}

// makeQuestionResult 把打分结果封装成单题结果(供 Runner 调用)。
func makeQuestionResult(q store.QuestionRow, scores []RuleScore, total float64, answer string, rt *RoundtableResult, sb *SandboxResult) QuestionResult {
	return QuestionResult{
		QuestionID: q.ID,
		Title:      q.Title,
		Score:      round1(total),
		IsBadCase:  IsBadCase(total),
		RuleScores: scores,
		Difficulty: q.Difficulty,
		Answer:     truncateAnswer(answer, 2000), // 报告存完整代码(阈值2000),全文走 conversation 表
		Roundtable: rt,
		Sandbox:    sb,
	}
}

// truncateAnswer 截断回答(避免报告 JSON 过大)。
// 默认阈值 2000(原 500 太短,大部分代码片段都被截断,影响 Bad Case 分析)。
func truncateAnswer(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "...(截断)"
}
