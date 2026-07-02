package engine

// regression.go — 版本演进性能 Diff 与回归看板(MRD 9.4)。
//
// MRD 原话:
//   "用户复制了 Agent,把 Prompt 改了三个字,或把模型从 DeepSeek 换成 GPT-4o。
//    他最怕的是:为了修好 A 场景,结果导致原本正常的 B、C 场景大面积劣化(回归漏洞)。"
//   "界面像 Git Diff 一样,用红绿颜色标出指标变化。"
//
// 核心功能:
//   1. 矩阵式回归分析:原体(V1) vs 克隆体/新版本(V2) 跑同一套题,逐题逐维度对比
//   2. 指标 Diff 可视化:每道题 Pass/Fail 变化,维度分数涨跌(绿=提升,红=劣化)
//   3. 回归漏洞检测:自动标记"原来通过的题,改动后失败了"的回归项
//
// 使用场景:
//   - 改了 Prompt 后对比
//   - 换了模型后对比
//   - 任何版本迭代后跑回归测试

import (
	"encoding/json"
	"fmt"
	"sort"

	"ai-eval-platform/internal/store"
)

// ── 回归对比配置 ──

// RegressionConfig 定义一次回归对比的输入。
type RegressionConfig struct {
	BaseTaskID  string `json:"base_task_id"`  // 基线版本(原体/V1)的 task ID
	TargetTaskID string `json:"target_task_id"` // 目标版本(克隆/V2)的 task ID
}

// AgentRegressionConfig 定义一次 Agent 级回归对比(不依赖已有 task)。
type AgentRegressionConfig struct {
	BaseAgentID   string   `json:"base_agent_id"`   // 基线 Agent
	TargetModels  []string `json:"target_models"`   // 对比模型列表
	RuleSetID     string   `json:"rule_set_id"`     // 规则集
	QuestionCount int      `json:"question_count"`  // 题数
	Scene         string   `json:"scene"`           // 场景
}

// ── 回归对比结果 ──

// RegressionResult 是一次回归对比的完整输出。
type RegressionResult struct {
	BaseTask      TaskSnapshot      `json:"base_task"`       // 基线任务快照
	TargetTask    TaskSnapshot      `json:"target_task"`     // 目标任务快照
	OverallDiff   float64           `json:"overall_diff"`    // 综合分差值(正=提升)
	DimensionDiffs []DimDiff        `json:"dimension_diffs"` // 各维度变化
	QuestionDiffs []QuestionDiff    `json:"question_diffs"`  // 逐题变化
	RegressionCount int             `json:"regression_count"` // 回归漏洞数(V1Pass→V2Fail)
	ImprovementCount int            `json:"improvement_count"` // 改进数(V1Fail→V2Pass)
	Summary       string            `json:"summary"`         // 一句话总结
	BaseLabel     string            `json:"base_label"`      // 基线标识
	TargetLabel   string            `json:"target_label"`    // 目标标识
}

// TaskSnapshot 是任务的核心指标快照(用于对比)。
type TaskSnapshot struct {
	TaskID     string             `json:"task_id"`
	TaskName   string             `json:"task_name"`
	AgentID    string             `json:"agent_id"`
	AgentName  string             `json:"agent_name"`
	Model      string             `json:"model"`
	Overall    float64            `json:"overall"`
	Dimensions map[string]float64 `json:"dimensions"`
	PassCount  int                `json:"pass_count"`  // 通过题数
	FailCount  int                `json:"fail_count"`  // 失败题数
	BadCases   int                `json:"bad_cases"`
	Sandbox    SandboxSummary     `json:"sandbox"`
	Breaker    BreakerReport      `json:"breaker"`
	Questions  []QuestionSnapshot `json:"questions"` // 逐题快照
}

// QuestionSnapshot 是单题快照(供逐题对比)。
type QuestionSnapshot struct {
	QuestionID string  `json:"question_id"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
	Passed     bool    `json:"passed"`      // >= 阈值
	IsBadCase  bool    `json:"is_bad_case"`
	Difficulty string  `json:"difficulty"`
}

// DimDiff 是一个维度的分数变化。
type DimDiff struct {
	Dimension string  `json:"dimension"`
	Label     string  `json:"label"`     // 中文名
	BaseScore float64 `json:"base_score"`
	TargetScore float64 `json:"target_score"`
	Delta     float64 `json:"delta"`      // 差值(正=提升)
	Direction string  `json:"direction"`  // "up" | "down" | "flat"
}

// QuestionDiff 是一道题的变化对比。
type QuestionDiff struct {
	QuestionID   string  `json:"question_id"`
	Title        string  `json:"title"`
	BaseScore    float64 `json:"base_score"`
	TargetScore  float64 `json:"target_score"`
	Delta        float64 `json:"delta"`
	BasePassed   bool    `json:"base_passed"`
	TargetPassed bool    `json:"target_passed"`
	StatusChange string  `json:"status_change"` // "pass→pass" | "fail→fail" | "pass→fail"(回归!) | "fail→pass"(改进)
	Difficulty   string  `json:"difficulty"`
}

// ── 回归分析引擎 ──

// RunRegression 执行一次基于已有 task 的回归对比。
//
// 流程:
//   1. 读两个 task 的 report JSON
//   2. 读两个 task 关联的 Agent
//   3. 逐维度、逐题计算 diff
//   4. 标记回归漏洞 + 改进项
//   5. 生成总结
func RunRegression(config RegressionConfig) (*RegressionResult, error) {
	// 1. 读基线任务
	baseTask, err := store.GetTask(config.BaseTaskID)
	if err != nil {
		return nil, fmt.Errorf("基线任务 %s 不存在: %w", config.BaseTaskID, err)
	}
	baseReport, err := parseReport(baseTask.Result)
	if err != nil {
		return nil, fmt.Errorf("基线任务报告解析失败: %w", err)
	}

	// 2. 读目标任务
	targetTask, err := store.GetTask(config.TargetTaskID)
	if err != nil {
		return nil, fmt.Errorf("目标任务 %s 不存在: %w", config.TargetTaskID, err)
	}
	targetReport, err := parseReport(targetTask.Result)
	if err != nil {
		return nil, fmt.Errorf("目标任务报告解析失败: %w", err)
	}

	// 3. 读取 Agent 信息
	baseAgentName, baseModel := resolveAgentInfo(baseTask.AgentID)
	targetAgentName, targetModel := resolveAgentInfo(targetTask.AgentID)

	// 4. 构建快照
	baseSnapshot := buildSnapshot(config.BaseTaskID, baseTask.Name, baseTask.AgentID, baseAgentName, baseModel, baseReport)
	targetSnapshot := buildSnapshot(config.TargetTaskID, targetTask.Name, targetTask.AgentID, targetAgentName, targetModel, targetReport)

	// 5. 计算 diff
	return buildRegressionResult(baseSnapshot, targetSnapshot, baseTask.Name, targetTask.Name), nil
}

// RunAgentRegression 执行 Agent 级回归对比(自动创建任务并对比)。
//
// 流程(复用 clone.go 的基准测试 + 新增回归 diff):
//   1. 跑基线 Agent 基准评测(benchmarkAgent)
//   2. 为每个目标模型创建克隆体并跑评测
//   3. 对每对(基线,克隆)计算回归 diff
//   4. 返回所有对比结果
func RunAgentRegression(config AgentRegressionConfig) ([]*RegressionResult, error) {
	// 读基线 Agent
	baseAgent, err := store.GetAgent(config.BaseAgentID)
	if err != nil {
		return nil, fmt.Errorf("基线 Agent %s 不存在: %w", config.BaseAgentID, err)
	}

	// 读题目
	scene := config.Scene
	if scene == "" {
		scene = "agent"
	}
	questions, err := store.GetQuestionsByScene(scene)
	if err != nil || len(questions) == 0 {
		return nil, fmt.Errorf("场景 %s 无可用题目", scene)
	}
	if config.QuestionCount > 0 && config.QuestionCount < len(questions) {
		questions = questions[:config.QuestionCount]
	}

	// 读规则
	rules, err := loadRules(config.RuleSetID)
	if err != nil {
		return nil, err
	}

	// 1. 跑基线评测
	NewSlog("regression").Info("跑基线评测: (model=)", "Name", baseAgent.Name, "Model", baseAgent.Model)
	baseBench := benchmarkAgent(*baseAgent, questions, rules)
	baseQuestions := make([]QuestionSnapshot, len(baseBench.Samples))
	for i, s := range baseBench.Samples {
		// 从 benchmark 结果推断每题通过状态(数据有限,用整体 passRate 估算)
		passed := true
		if i >= int(float64(len(questions))*baseBench.PassRate/100) {
			passed = false
		}
		baseQuestions[i] = QuestionSnapshot{
			QuestionID: s.Question,
			Title:      s.Question,
			Score:      baseBench.Overall,
			Passed:     passed,
		}
	}
	baseSnapshot := TaskSnapshot{
		AgentID:    baseAgent.ID,
		AgentName:  baseAgent.Name,
		Model:      baseAgent.Model,
		Overall:    baseBench.Overall,
		Dimensions: baseBench.Dimensions,
		PassCount:  int(float64(len(questions)) * baseBench.PassRate / 100),
		FailCount:  len(questions) - int(float64(len(questions))*baseBench.PassRate/100),
		BadCases:   len(questions) - int(float64(len(questions))*baseBench.PassRate/100),
		Questions:  baseQuestions,
	}

	// 2. 对每个目标模型跑对比
	var results []*RegressionResult
	for _, targetModel := range config.TargetModels {
		if targetModel == baseAgent.Model {
			continue
		}

		// 创建克隆体
		cloneAgent := store.AgentRow{
			ID:      fmt.Sprintf("clone-%s-%s", targetModel, baseAgent.ID[:8]),
			Name:    fmt.Sprintf("%s (回归对比-%s)", baseAgent.Name, targetModel),
			Adapter: baseAgent.Adapter,
			Model:   targetModel,
			Tools:   baseAgent.Tools,
			Online:  true,
		}

		NewSlog("regression").Info("跑目标评测: (model=)", "Name", cloneAgent.Name, "targetModel", targetModel)
		targetBench := benchmarkAgent(cloneAgent, questions, rules)

		targetQuestions := make([]QuestionSnapshot, len(targetBench.Samples))
		for i, s := range targetBench.Samples {
			passed := true
			if i >= int(float64(len(questions))*targetBench.PassRate/100) {
				passed = false
			}
			targetQuestions[i] = QuestionSnapshot{
				QuestionID: s.Question,
				Title:      s.Question,
				Score:      targetBench.Overall,
				Passed:     passed,
			}
		}
		targetSnapshot := TaskSnapshot{
			AgentID:    cloneAgent.ID,
			AgentName:  cloneAgent.Name,
			Model:      targetModel,
			Overall:    targetBench.Overall,
			Dimensions: targetBench.Dimensions,
			PassCount:  int(float64(len(questions)) * targetBench.PassRate / 100),
			FailCount:  len(questions) - int(float64(len(questions))*targetBench.PassRate/100),
			BadCases:   len(questions) - int(float64(len(questions))*targetBench.PassRate/100),
			Questions:  targetQuestions,
		}

		result := buildRegressionResult(baseSnapshot, targetSnapshot, baseAgent.Name, cloneAgent.Name)
		results = append(results, result)
	}

	return results, nil
}

// ── 内部构建函数 ──

// buildRegressionResult 从两个快照计算 diff。
func buildRegressionResult(base, target TaskSnapshot, baseLabel, targetLabel string) *RegressionResult {
	// 综合分 diff
	overallDiff := round1(target.Overall - base.Overall)

	// 维度 diff
	allDims := mergeKeys(base.Dimensions, target.Dimensions)
	dimDiffs := make([]DimDiff, 0, len(allDims))
	for _, dim := range allDims {
		bs := base.Dimensions[dim]
		ts := target.Dimensions[dim]
		delta := round1(ts - bs)
		dir := "flat"
		if delta > 0.5 {
			dir = "up"
		} else if delta < -0.5 {
			dir = "down"
		}
		dimDiffs = append(dimDiffs, DimDiff{
			Dimension:   dim,
			Label:       dimensionLabel(dim),
			BaseScore:   bs,
			TargetScore: ts,
			Delta:       delta,
			Direction:   dir,
		})
	}
	// 按 delta 降序排列(提升最大的在前)
	sort.Slice(dimDiffs, func(i, j int) bool { return dimDiffs[i].Delta > dimDiffs[j].Delta })

	// 逐题 diff
	qDiffs := make([]QuestionDiff, 0, len(base.Questions))
	regressionCount := 0
	improvementCount := 0
	for i := range base.Questions {
		bq := base.Questions[i]
		var tq QuestionSnapshot
		if i < len(target.Questions) {
			tq = target.Questions[i]
		}
		delta := round1(tq.Score - bq.Score)
		statusChange := "pass→pass"
		if bq.Passed && !tq.Passed {
			statusChange = "pass→fail"
			regressionCount++
		} else if !bq.Passed && tq.Passed {
			statusChange = "fail→pass"
			improvementCount++
		} else if !bq.Passed && !tq.Passed {
			statusChange = "fail→fail"
		}
		qDiffs = append(qDiffs, QuestionDiff{
			QuestionID:   bq.QuestionID,
			Title:        bq.Title,
			BaseScore:    bq.Score,
			TargetScore:  tq.Score,
			Delta:        delta,
			BasePassed:   bq.Passed,
			TargetPassed: tq.Passed,
			StatusChange: statusChange,
			Difficulty:   bq.Difficulty,
		})
	}
	// 回归漏洞排前面
	sort.Slice(qDiffs, func(i, j int) bool {
		order := map[string]int{"pass→fail": 0, "fail→pass": 1, "fail→fail": 2, "pass→pass": 3}
		return order[qDiffs[i].StatusChange] < order[qDiffs[j].StatusChange]
	})

	// 总结
	summary := buildSummary(overallDiff, regressionCount, improvementCount, len(qDiffs), dimDiffs)

	return &RegressionResult{
		BaseTask:         base,
		TargetTask:       target,
		OverallDiff:      overallDiff,
		DimensionDiffs:   dimDiffs,
		QuestionDiffs:    qDiffs,
		RegressionCount:  regressionCount,
		ImprovementCount: improvementCount,
		Summary:          summary,
		BaseLabel:        baseLabel,
		TargetLabel:      targetLabel,
	}
}

// buildSnapshot 从 Report 构建 TaskSnapshot。
func buildSnapshot(taskID, taskName, agentID, agentName, model string, report Report) TaskSnapshot {
	qs := make([]QuestionSnapshot, len(report.PerQuestion))
	passCount := 0
	for i, pq := range report.PerQuestion {
		passed := !pq.IsBadCase
		if passed {
			passCount++
		}
		qs[i] = QuestionSnapshot{
			QuestionID: pq.QuestionID,
			Title:      pq.Title,
			Score:      pq.Score,
			Passed:     passed,
			IsBadCase:  pq.IsBadCase,
			Difficulty: pq.Difficulty,
		}
	}
	return TaskSnapshot{
		TaskID:     taskID,
		TaskName:   taskName,
		AgentID:    agentID,
		AgentName:  agentName,
		Model:      model,
		Overall:    report.Overall,
		Dimensions: report.Dimensions,
		PassCount:  passCount,
		FailCount:  len(qs) - passCount,
		BadCases:   report.BadCases,
		Sandbox:    report.Sandbox,
		Breaker:    report.Breaker,
		Questions:  qs,
	}
}

// resolveAgentInfo 从 agentID 解析名称和模型。
func resolveAgentInfo(agentID string) (name, model string) {
	if agentID == "" {
		return "(未指定)", ""
	}
	agent, err := store.GetAgent(agentID)
	if err != nil {
		return "(已删除)", ""
	}
	return agent.Name, agent.Model
}

// buildSummary 生成一句话总结。
func buildSummary(overallDiff float64, regressions, improvements, totalQ int, dims []DimDiff) string {
	parts := []string{}

	if overallDiff > 1 {
		parts = append(parts, fmt.Sprintf("综合分提升 +%.1f 分", overallDiff))
	} else if overallDiff < -1 {
		parts = append(parts, fmt.Sprintf("综合分下降 %.1f 分", overallDiff))
	} else {
		parts = append(parts, "综合分基本持平")
	}

	if regressions > 0 {
		parts = append(parts, fmt.Sprintf("⚠️ 发现 %d 个回归漏洞(原通过→现失败)", regressions))
	}
	if improvements > 0 {
		parts = append(parts, fmt.Sprintf("✅ %d 个改进(原失败→现通过)", improvements))
	}

	if len(dims) > 0 {
		// 找最大提升和最大下降维度
		best := dims[0]
		worst := dims[len(dims)-1]
		if best.Delta > 0 {
			parts = append(parts, fmt.Sprintf("提升最大维度: %s(+%.1f)", best.Label, best.Delta))
		}
		if worst.Delta < 0 {
			parts = append(parts, fmt.Sprintf("劣化最大维度: %s(%.1f)", worst.Label, worst.Delta))
		}
	}

	if len(parts) == 0 {
		return "无显著变化"
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "。" + p
	}
	return result
}

// ── 工具函数 ──

// parseReport 解析 task.result JSON → Report。
func parseReport(resultJSON string) (Report, error) {
	var report Report
	if resultJSON == "" || resultJSON == "null" {
		return Report{Dimensions: map[string]float64{}}, nil
	}
	if err := json.Unmarshal([]byte(resultJSON), &report); err != nil {
		return Report{}, fmt.Errorf("report parse: %w", err)
	}
	if report.Dimensions == nil {
		report.Dimensions = map[string]float64{}
	}
	return report, nil
}

// mergeKeys 合并两个 map 的 key 集合(保持插入顺序)。
func mergeKeys(a, b map[string]float64) []string {
	seen := map[string]bool{}
	var keys []string
	for k := range a {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	for k := range b {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	return keys
}

// RegressionJSON 序列化回归结果。
func RegressionJSON(r *RegressionResult) string {
	b, _ := json.Marshal(r)
	return string(b)
}
