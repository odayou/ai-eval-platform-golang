package engine

// clone.go — 智能体多基座克隆与矩阵式 A/B 测试引擎(MRD 第八章 / 4.1)。
//
// 核心价值(MRD 原话):
//   "用户导入一个 Agent 后,不仅能测它'好不好',还能克隆它、换底层大模型、并联压测,
//    最终告诉开发者'用哪个模型性价比最高'。"
//   让平台从"评测工具"升维成"AI 架构省钱与优化顾问"。
//
// 功能:
//   1. 一键克隆 Agent(复制全部配置,只换模型)
//   2. 并联对比:同一题集,原体 + 克隆体并联跑
//   3. MSI 敏感度指数:Agent 代码对模型切换的鲁棒性
//   4. 性价比帕累托边界:性能 VS 成本二维散点
//
// 数据模型:
//   - 原体:agents 表原始记录
//   - 克隆体:agents 表新记录(model 字段换成目标模型)
//   - 对比结果:clone_results 表(或 JSON 存 tasks.result)

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// CloneConfig 克隆对比的配置。
type CloneConfig struct {
	SourceAgentID string   `json:"source_agent_id"` // 原体 Agent ID
	TargetModels  []string `json:"target_models"`   // 目标模型列表(如 ["glm-4", "deepseek-chat", "gpt-4o"])
	QuestionCount int      `json:"question_count"`  // 对比题数(0=全部)
	RuleSetID     string   `json:"rule_set_id"`     // 规则集 ID
}

// CloneResult 是一次克隆对比的结果。
type CloneResult struct {
	SourceAgent  AgentBenchmark   `json:"source_agent"`  // 原体评测结果
	Clones       []AgentBenchmark `json:"clones"`        // 克隆体评测结果
	MSI          float64          `json:"msi"`           // 模型基座切换敏感度指数
	ROIAnalysis  []ROIPair        `json:"roi_analysis"`  // 性价比分析
	BestValue    string           `json:"best_value"`    // 最佳性价比模型
	Recommendation string         `json:"recommendation"` // 运营建议
}

// AgentBenchmark 是单个 Agent 的评测基准。
type AgentBenchmark struct {
	AgentID    string  `json:"agent_id"`
	AgentName  string  `json:"agent_name"`
	Model      string  `json:"model"`
	Overall    float64 `json:"overall"`
	Dimensions map[string]float64 `json:"dimensions"`
	TokenTotal int     `json:"token_total"`
	EstCost    float64 `json:"est_cost"`
	PassRate   float64 `json:"pass_rate"`
	Samples    []SampleAnswer `json:"samples"` // 前 3 题的问答样本(供 Diff 展示)
}

// SampleAnswer 是一道题的问答样本(供前端 Diff 对比)。
type SampleAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// ROIPair 是一对性价比对比。
type ROIPair struct {
	ModelA    string  `json:"model_a"`
	ModelB    string  `json:"model_b"`
	ScoreA    float64 `json:"score_a"`
	ScoreB    float64 `json:"score_b"`
	CostA     float64 `json:"cost_a"`
	CostB     float64 `json:"cost_b"`
	Winner    string  `json:"winner"`     // 性能胜出
	BestValue string  `json:"best_value"` // 性价比胜出
}

// RunCloneTest 执行一次克隆对比测试。
//
// 流程:
//  1. 读原体 Agent
//  2. 为每个目标模型创建克隆体(写入 agents 表)
//  3. 对每个 Agent(原体+克隆体)跑一次完整评测
//  4. 汇总对比:MSI + 性价比分析 + 运营建议
func RunCloneTest(config CloneConfig) (*CloneResult, error) {
	// 1. 读原体
	sourceAgent, err := store.GetAgent(config.SourceAgentID)
	if err != nil {
		return nil, fmt.Errorf("source agent not found: %w", err)
	}

	// 2. 读题目
	questions, err := store.GetQuestionsByScene("agent")
	if err != nil || len(questions) == 0 {
		return nil, fmt.Errorf("no questions available")
	}
	if config.QuestionCount > 0 && config.QuestionCount < len(questions) {
		questions = questions[:config.QuestionCount]
	}

	// 3. 读规则
	rules, err := loadRules(config.RuleSetID)
	if err != nil {
		return nil, err
	}

	// 4. 评测原体
	NewSlog("clone").Info("评测原体: (model=)", "Name", sourceAgent.Name, "Model", sourceAgent.Model)
	sourceResult := benchmarkAgent(*sourceAgent, questions, rules)

	// 5. 为每个目标模型创建克隆体并评测
	var clones []AgentBenchmark
	for _, targetModel := range config.TargetModels {
		if targetModel == sourceAgent.Model {
			continue // 跳过与原体相同的模型
		}

		// 创建克隆体(写入 agents 表)
		cloneID := uuid.New().String()
		cloneName := fmt.Sprintf("%s (Clone-%s)", sourceAgent.Name, targetModel)
		now := time.Now().Format(time.RFC3339)
		store.InsertAgent(store.AgentCreateParams{
			ID: cloneID, Name: cloneName, Adapter: sourceAgent.Adapter,
			Model: targetModel, Tools: sourceAgent.Tools,
			PermissionLevel: sourceAgent.PermissionLevel,
			CreatedAt: now, UpdatedAt: now,
		})
		// 克隆体直接上线(online=1)
		store.UpdateAgentOnline(cloneID, true)

		cloneAgent := store.AgentRow{
			ID:      cloneID,
			Name:    cloneName,
			Adapter: sourceAgent.Adapter,
			Model:   targetModel,
			Tools:   sourceAgent.Tools,
			Online:  true,
		}

		NewSlog("clone").Info("评测克隆体: (model=)", "cloneName", cloneName, "targetModel", targetModel)
		cloneResult := benchmarkAgent(cloneAgent, questions, rules)
		clones = append(clones, cloneResult)
	}

	// 6. 汇总分析
	result := &CloneResult{
		SourceAgent: sourceResult,
		Clones:      clones,
	}
	result.MSI = calculateMSI(sourceResult, clones)
	result.ROIAnalysis = analyzeROI(sourceResult, clones)
	result.BestValue = findBestValue(sourceResult, clones)
	result.Recommendation = generateRecommendation(sourceResult, clones, result.MSI)

	return result, nil
}

// benchmarkAgent 对单个 Agent 跑完整评测并返回基准分。
func benchmarkAgent(agent store.AgentRow, questions []store.QuestionRow, rules []store.RuleRow) AgentBenchmark {
	var totalScore float64
	var totalTokens int
	var passCount int
	dimScores := map[string][]float64{}
	var samples []SampleAnswer

	for i, q := range questions {
		cr := ChatAgent(agent, q.Prompt)
		_, score := ScoreQuestion(rules, q.Prompt, cr.Text)
		totalScore += score
		totalTokens += cr.TokenIn + cr.TokenOut
		if score >= 60 {
			passCount++
		}

		// 收集维度分(从圆桌结果)
		rt := RunRoundtable("", rules, q.Prompt, cr.Text, AgentProfile{})
		for _, op := range rt.Opinions {
			dimScores[op.Dimension] = append(dimScores[op.Dimension], op.Score)
		}

		// 保存前 3 题的问答样本(供前端 Diff 展示)
		if i < 3 {
			samples = append(samples, SampleAnswer{
				Question: q.Title,
				Answer:   cr.Text,
			})
		}
	}

	// 聚合维度分
	dimensions := map[string]float64{}
	for dim, scores := range dimScores {
		dimensions[dim] = round1(average(scores))
	}

	overall := 0.0
	if len(questions) > 0 {
		overall = round1(totalScore / float64(len(questions)))
	}

	return AgentBenchmark{
		AgentID:    agent.ID,
		AgentName:  agent.Name,
		Model:      agent.Model,
		Overall:    overall,
		Dimensions: dimensions,
		TokenTotal: totalTokens,
		EstCost:    float64(totalTokens) / 1000.0 * 0.002, // GLM-4-flash 价格
		PassRate:   round1(float64(passCount) / float64(len(questions)) * 100),
	}
}

// calculateMSI 计算模型基座切换敏感度指数。
// MSI = 克隆体得分标准差 / 原体得分。越低说明 Agent 架构越健壮。
func calculateMSI(source AgentBenchmark, clones []AgentBenchmark) float64 {
	if len(clones) == 0 {
		return 0
	}

	scores := make([]float64, len(clones))
	for i, c := range clones {
		scores[i] = c.Overall
	}

	// 计算标准差
	var sum float64
	for _, s := range scores {
		sum += s
	}
	mean := sum / float64(len(scores))

	var variance float64
	for _, s := range scores {
		variance += (s - mean) * (s - mean)
	}
	variance /= float64(len(scores))
	stddev := math.Sqrt(variance)

	// MSI = 标准差 / 原体得分(归一化)
	if source.Overall > 0 {
		return round1(stddev / source.Overall * 100)
	}
	return 0
}

// analyzeROI 分析性价比。
func analyzeROI(source AgentBenchmark, clones []AgentBenchmark) []ROIPair {
	var pairs []ROIPair
	all := append([]AgentBenchmark{source}, clones...)

	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			winner := a.Model
			if b.Overall > a.Overall {
				winner = b.Model
			}
			bestValue := a.Model
			if b.EstCost > 0 && a.EstCost > 0 {
				roiA := a.Overall / a.EstCost
				roiB := b.Overall / b.EstCost
				if roiB > roiA {
					bestValue = b.Model
				}
			}
			pairs = append(pairs, ROIPair{
				ModelA: a.Model, ModelB: b.Model,
				ScoreA: a.Overall, ScoreB: b.Overall,
				CostA: a.EstCost, CostB: b.EstCost,
				Winner: winner, BestValue: bestValue,
			})
		}
	}
	return pairs
}

// findBestValue 找最佳性价比模型。
func findBestValue(source AgentBenchmark, clones []AgentBenchmark) string {
	best := source
	bestROI := source.Overall / math.Max(source.EstCost, 0.001)

	for _, c := range clones {
		roi := c.Overall / math.Max(c.EstCost, 0.001)
		if roi > bestROI {
			best = c
			bestROI = roi
		}
	}
	return best.Model
}

// generateRecommendation 生成运营建议。
func generateRecommendation(source AgentBenchmark, clones []AgentBenchmark, msi float64) string {
	bestValue := findBestValue(source, clones)

	if msi < 10 {
		return fmt.Sprintf("您的 Agent 架构非常健壮(MSI=%.1f,极低),在不同模型下表现稳定。建议切换到 %s 以获得最佳性价比。", msi, bestValue)
	}
	if msi < 30 {
		return fmt.Sprintf("您的 Agent 对模型切换有一定敏感度(MSI=%.1f),建议在切换前做充分测试。推荐 %s 作为性价比之选。", msi, bestValue)
	}
	return fmt.Sprintf("您的 Agent 对模型高度敏感(MSI=%.1f),不同模型下表现差异大。建议先优化 Agent 架构(减少对特定模型的依赖),再考虑切换。当前推荐 %s。", msi, bestValue)
}

// CloneResultJSON 把克隆对比结果序列化为 JSON。
func CloneResultJSON(result *CloneResult) string {
	b, _ := json.Marshal(result)
	return string(b)
}
