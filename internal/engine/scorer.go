package engine

// scorer.go — 三轨打分发分发 + 单题维度聚合。
//
// 每条规则按 scoring 字段分发到不同轨道:
//   - metric: 调 RunMetric(正则/关键词,免费确定)
//   - llm:    调 Judge(LLM-as-Judge,成本高但能评语义)
//   - manual: 标记为待人工评分(本次给 0,不计入聚合;阶段①不实现 UI)
//
// 单题得分 = 加权平均(每条规则 Score × Weight 之和 / Weight 之和)。

import (
	"ai-eval-platform/internal/store"
)

// RuleScore 是一条规则对单题的打分结果。
type RuleScore struct {
	RuleID    string  `json:"rule_id"`
	RuleName  string  `json:"rule_name"`
	Dimension string  `json:"dimension"`
	Scoring   string  `json:"scoring"` // metric | llm | manual
	Weight    float64 `json:"weight"`
	Score     float64 `json:"score"`     // 0-100,manual 时为 0
	Reason    string  `json:"reason"`
	IsMock    bool    `json:"is_mock"`
}

// ScoreQuestion 对单题跑全部规则,返回每条规则的得分 + 该题加权总分。
func ScoreQuestion(rules []store.RuleRow, prompt, answer string) ([]RuleScore, float64) {
	scores := make([]RuleScore, 0, len(rules))
	var totalWeight, weightedSum float64

	for _, r := range rules {
		rs := RuleScore{
			RuleID:    r.ID,
			RuleName:  r.Name,
			Dimension: r.Dimension,
			Scoring:   r.Scoring,
			Weight:    r.Weight,
		}

		switch r.Scoring {
		case "metric":
			mr := RunMetric(r, answer)
			rs.Score = mr.Score
			rs.Reason = mr.Reason
		case "llm":
			jr := Judge(r, prompt, answer)
			rs.Score = jr.Score
			rs.Reason = jr.Reason
			rs.IsMock = jr.IsMock
		case "manual":
			// 阶段①不实现人工评分 UI,记 0 并标注
			rs.Score = 0
			rs.Reason = "待人工评分(本次未计入)"
		default:
			rs.Score = 50
			rs.Reason = "未知评分方式,给中性分"
		}

		scores = append(scores, rs)
		// manual 不计入加权聚合
		if r.Scoring != "manual" {
			weightedSum += rs.Score * r.Weight
			totalWeight += r.Weight
		}
	}

	// 加权总分
	var total float64
	if totalWeight > 0 {
		total = weightedSum / totalWeight
	} else {
		total = 50 // 无可计分规则,给中性
	}
	return scores, total
}
