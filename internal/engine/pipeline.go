package engine

// pipeline.go — Agent CI/CD 管线(拓展方向1)。
//
// 把"手动跑一次评测"升级为"持续集成":
//   触发(git_push/cron/manual) → 跑标准题库 → 三维度评分(正确性/安全/性能)
//   → gate判定(min_score/max_cost/regression_threshold) → 通过/失败报告

import (
	"encoding/json"
	"fmt"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// PipelineConfig 管线配置。
type PipelineConfig struct {
	AgentID     string  `json:"agent_id"`
	Scene       string  `json:"scene"`
	RuleSetID   string  `json:"rule_set_id"`
	MinScore    float64 `json:"min_score"`     // gate: 最低均分(0-100)
	MaxCost     float64 `json:"max_cost"`      // gate: 最大成本(USD)
	MaxRegression float64 `json:"max_regression"` // gate: 允许的最大退化(与基准的分差)
}

// PipelineResult 管线运行结果。
type PipelineResult struct {
	RunID            string  `json:"run_id"`
	Status           string  `json:"status"`           // passed|failed
	ScoreCorrectness float64 `json:"score_correctness"`
	ScoreSecurity    float64 `json:"score_security"`
	ScorePerformance float64 `json:"score_performance"`
	CostUSD          float64 `json:"cost_usd"`
	OverallScore     float64 `json:"overall_score"`
	Regression       float64 `json:"regression"`       // 与基准的分差(正值=退化)
	GateReason       string  `json:"gate_reason"`
	Report           map[string]any `json:"report"`
}

// RunPipeline 执行一次 CI/CD 管线评测。
func RunPipeline(config PipelineConfig, triggerType string) PipelineResult {
	runID := "run-" + uuid.New().String()[:8]
	result := PipelineResult{RunID: runID, Status: "running"}

	// 更新 DB 状态
	store.DB.Exec(`INSERT INTO pipeline_runs (id, pipeline_id, status, trigger_type, started_at)
		VALUES (?, ?, 'running', ?, CURRENT_TIMESTAMP)`, runID, config.AgentID, triggerType)

	NewSlog("pipeline").Info("run 开始: agent= scene=", "runID", runID, "AgentID", config.AgentID, "Scene", config.Scene)

	// 1. 加载 agent + 题库 + 规则
	agent, err := store.GetAgent(config.AgentID)
	if err != nil || agent == nil {
		result.Status = "failed"
		result.GateReason = fmt.Sprintf("agent 加载失败: %v", err)
		result.finalize(config, "failed")
		return result
	}

	questions, _ := store.GetQuestionsByScene(config.Scene)
	if len(questions) == 0 {
		questions, _ = store.GetQuestionsByScene("agent") // 回退默认场景
	}
	if len(questions) == 0 {
		result.Status = "failed"
		result.GateReason = "题库为空"
		result.finalize(config, "failed")
		return result
	}

	// 2. 跑评测(D48 真实评测:调 agent + judge 打分,不再硬编码分数)
	var scores []float64
	var securityScores []float64
	var perfScores []float64
	totalCost := 0.0

	// 加载规则集(D50 从 gate_config JSON 读)
	rules, _ := store.GetAllRules()
	if len(rules) == 0 {
		rules = []store.RuleRow{{ID: "default", Name: "默认", Dimension: "correctness", Scoring: "llm", Weight: 1.0, Content: "回答正确且完整"}}
	}

	for i, q := range questions {
		if i >= 10 { // 限制 10 题加速 CI
			break
		}
		// 调被测 agent(走 agent_loop ReAct 循环)
		cr := ChatAgent(*agent, q.Prompt)

		// D48 真实打分:调 judge(单判官模式,快速)
		judgeResult := Judge(rules[0], q.Prompt, cr.Text)
		scores = append(scores, judgeResult.Score)

		// 安全维度:跑 antileak 扫描
		securityScore := 100.0
		if cr.Text != "" {
			leakageResult := ScanLeakage(q.Prompt, cr.Text)
			securityScore = float64(leakageResult.Score)
			if len(leakageResult.Findings) > 0 {
				securityScore -= float64(len(leakageResult.Findings)) * 10
				if securityScore < 0 {
					securityScore = 0
				}
			}
		}
		securityScores = append(securityScores, securityScore)

		// 性能维度:用响应长度/延迟估算(简化)
		perfScores = append(perfScores, 70.0) // 简化:实际可测响应时间

		totalCost += 0.01
	}

	// 3. 三维度评分
	result.ScoreCorrectness = avg(scores)
	result.ScoreSecurity = avg(securityScores)
	result.ScorePerformance = avg(perfScores)
	result.OverallScore = (result.ScoreCorrectness*0.5 + result.ScoreSecurity*0.3 + result.ScorePerformance*0.2)
	result.CostUSD = totalCost

	// 4. Gate 判定
	result.Status = "passed"
	if result.OverallScore < config.MinScore {
		result.Status = "failed"
		result.GateReason = fmt.Sprintf("均分 %.1f < 最低要求 %.1f", result.OverallScore, config.MinScore)
	}
	if result.CostUSD > config.MaxCost && config.MaxCost > 0 {
		result.Status = "failed"
		result.GateReason += fmt.Sprintf("; 成本 $%.4f > 上限 $%.4f", result.CostUSD, config.MaxCost)
	}

	// 5. 退化检测(与上次基准比)
	result.Regression = checkRegression(config.AgentID, result.OverallScore)
	if result.Regression > config.MaxRegression && config.MaxRegression > 0 {
		result.Status = "failed"
		result.GateReason += fmt.Sprintf("; 退化 %.1f 分(阈值 %.1f)", result.Regression, config.MaxRegression)
	}

	result.Report = map[string]any{
		"questions_run": len(scores),
		"dimensions": map[string]float64{
			"correctness": result.ScoreCorrectness,
			"security":    result.ScoreSecurity,
			"performance": result.ScorePerformance,
		},
		"gate": map[string]any{
			"min_score":      config.MinScore,
			"max_cost":       config.MaxCost,
			"max_regression": config.MaxRegression,
		},
	}

	result.finalize(config, result.Status)
	NewSlog("pipeline").Info("run 完成: status= score= cost=$ regression=", "runID", runID, "Status", result.Status, "OverallScore", result.OverallScore, "CostUSD", result.CostUSD, "Regression", result.Regression)
	return result
}

// finalize 写 DB。
func (r *PipelineResult) finalize(config PipelineConfig, status string) {
	passed := 0
	if status == "passed" {
		passed = 1
	}
	reportJSON, _ := json.Marshal(r.Report)
	store.DB.Exec(`UPDATE pipeline_runs SET status=?, score_correctness=?, score_security=?,
		score_performance=?, cost_usd=?, overall_passed=?, report=?, finished_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		status, r.ScoreCorrectness, r.ScoreSecurity, r.ScorePerformance,
		r.CostUSD, passed, string(reportJSON), r.RunID)
}

// checkRegression 检查与上次基准的退化。
func checkRegression(agentID string, currentScore float64) float64 {
	var lastScore float64
	err := store.DB.QueryRow(
		`SELECT MAX(score_correctness) FROM pipeline_runs WHERE pipeline_id=? AND overall_passed=1 AND status='passed'`,
		agentID).Scan(&lastScore)
	if err != nil || lastScore == 0 {
		return 0 // 无历史基准
	}
	if currentScore < lastScore {
		return lastScore - currentScore // 正值=退化
	}
	return 0
}

func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
