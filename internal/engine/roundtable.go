package engine

// roundtable.go — 圆桌合议引擎(MRD 5.4 规则库与圆桌合议层)。
//
// 核心设计(对应 MRD):
//   - 一个主导者 Agent 综览全局,多个领域专家 Agent 各自从专业角度独立打分
//   - 专家并发执行(sync.WaitGroup),各自只读自己维度的规则
//   - 主导者收集所有专家意见,交叉比对,加权汇总出结构化报告
//
// 与单判官(Judge)的区别:
//   - 单判官:一条规则调一次 LLM,所有规则串行,无交叉验证
//   - 圆桌:每个专家对自己维度的所有规则综合评判(一个 prompt 评多条规则),
//     专家间并发,主导者做最终裁决——更接近真实 Code Review 流程
//
// 专家角色(对应 model_config 表的 role):
//   expert_security  — 安全专家:注入、泄密、越权
//   expert_perf      — 性能专家:死锁、内存、并发
//   expert_arch      — 架构专家:分层、解耦、扩展
//   expert_product   — 产品专家:需求完整性、可落实性
//
// 扩展性:专家列表从 judge_models 表动态加载(见 loadExperts),
//         用户在考官模型库新增 role=expert_*, dimension=xxx 的专家即自动生效。

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// ExpertOpinion 是一个专家对单题的评审意见。
type ExpertOpinion struct {
	Role      string  `json:"role"`      // expert_security / expert_perf / ... 或自定义
	Dimension string  `json:"dimension"` // security / performance / ...
	Score     float64 `json:"score"`     // 0-100
	Reason    string  `json:"reason"`    // 评审依据
	IsMock    bool    `json:"is_mock"`   // 是否 mock
	Weight    float64 `json:"weight"`    // 该专家的打分权重(加权汇总用)
}

// RoundtableResult 是圆桌合议对单题的最终结果。
type RoundtableResult struct {
	Opinions   []ExpertOpinion `json:"opinions"`   // 各专家独立意见
	LeaderNote string          `json:"leader_note"` // 主导者综合评语
	FinalScore float64         `json:"final_score"` // 加权最终分
	IsMock     bool            `json:"is_mock"`     // 是否全程 mock
}

// expertDef 定义一个专家角色。
type expertDef struct {
	Role             string  // model_config 里的 role 或自定义 role
	Dimension        string  // 对应的评测维度
	Label            string  // 中文名(日志用)
	MemberType       string  // agent | model | role(专家组来源,空表示兜底专家)
	MemberRef        string  // agent→agents.id; model/role→role 字符串
	Weight           float64 // 打分权重
	PromptText       string  // 成员内嵌提示词(优先级低于 PromptTemplateID)
	PromptTemplateID string  // 引用 prompt_templates.id(优先级高于 PromptText)
}

// builtinExperts 是预置兜底:judge_models 表无专家数据时使用。
// 正常情况下专家从数据库动态加载(loadExperts / loadExpertsFromGroup)。
var builtinExperts = []expertDef{
	{Role: "expert_security", Dimension: "security", Label: "安全专家", Weight: 1.0},
	{Role: "expert_perf", Dimension: "performance", Label: "性能专家", Weight: 1.0},
	{Role: "expert_arch", Dimension: "architecture", Label: "架构专家", Weight: 1.0},
	{Role: "expert_product", Dimension: "product", Label: "产品专家", Weight: 1.0},
}

// expertLabels 专家 role → 中文名(用于日志和 prompt)。
// 预置专家有固定中文名,自定义专家用 role 推导(去掉 expert_ 前缀)。
func expertLabel(role string) string {
	labels := map[string]string{
		"expert_security": "安全专家",
		"expert_perf":     "性能专家",
		"expert_arch":     "架构专家",
		"expert_product":  "产品专家",
	}
	if l, ok := labels[role]; ok {
		return l
	}
	// 自定义专家:expert_reliability → "可靠性专家"(简单处理,英文转可读名)
	if strings.HasPrefix(role, "expert_") {
		return strings.TrimPrefix(role, "expert_") + " 专家"
	}
	return role + " 专家"
}

// loadExperts 从 judge_models 表动态加载专家列表。
// 只取 role 以 expert_ 开头且配了 dimension 的记录。
// 数据库无数据(如降级/老库)时回退到预置 builtinExperts,保证引擎不空转。
func loadExperts() []expertDef {
	jms, err := store.GetExperts()
	if err != nil || len(jms) == 0 {
		if err != nil {
			NewSlog("roundtable").Error("加载专家失败(),回退预置 4 专家", "err", err)
		}
		return builtinExperts
	}
	experts := make([]expertDef, 0, len(jms))
	for _, jm := range jms {
		experts = append(experts, expertDef{
			Role:      jm.Role,
			Dimension: jm.Dimension,
			Label:     expertLabel(jm.Role),
			Weight:    1.0,
		})
	}
	return experts
}

// loadExpertsFromGroup 从 expert_groups 表加载专家组(可配置圆桌成员)。
//
// 与 loadExperts() 的区别:
//   - loadExperts 从 judge_models 表加载(只有 model/role 型,无 agent 型,无内嵌提示词)
//   - loadExpertsFromGroup 从 expert_groups 表加载(支持 agent/model/role 三种成员类型,
//     支持成员内嵌 prompt_text 和引用 prompt_template_id,支持 weight 权重)
//
// groupID 为空时用默认专家组 "eg-default"(向后兼容)。
// 专家组不存在或无成员时回退到 loadExperts()(judge_models 兜底)。
func loadExpertsFromGroup(groupID string) []expertDef {
	if groupID == "" {
		groupID = "eg-default"
	}
	_, members, err := store.GetExpertGroup(groupID)
	if err != nil || len(members) == 0 {
		// 专家组不存在或空,回退到 judge_models 加载
		if err != nil {
			NewSlog("roundtable").Error("加载专家组 失败(),回退 judge_models", "groupID", groupID, "err", err)
		}
		return loadExperts()
	}
	experts := make([]expertDef, 0, len(members))
	for _, m := range members {
		label := m.RoleLabel
		if label == "" {
			label = expertLabel(m.MemberRef)
		}
		weight := m.Weight
		if weight == 0 {
			weight = 1.0
		}
		experts = append(experts, expertDef{
			Role:             m.MemberRef, // agent→agents.id; model/role→role 字符串
			Dimension:        m.Dimension,
			Label:            label,
			MemberType:       m.MemberType,
			MemberRef:        m.MemberRef,
			Weight:           weight,
			PromptText:       m.PromptText,
			PromptTemplateID: m.PromptTemplateID,
		})
	}
	NewSlog("roundtable").Info("从专家组 加载 个成员", "groupID", groupID, "experts_count", len(experts))
	return experts
}

// RunRoundtable 对单题执行圆桌合议。
//
// 流程:
//  1. 按维度分组规则(每个专家只看自己维度的规则)
//  2. 并发启动各专家 goroutine,每个专家:
//     a. 收集自己维度的所有规则 content 拼成 rubric(或用成员内嵌/引用的提示词)
//     b. 按 member_type 解析端点(agent→models 表,model/role→judge_models 表)
//     c. 调 LLM 判官(或 mock)对该维度综合打分
//     d. 产出 ExpertOpinion(携带 weight)
//  3. 主导者收集所有意见,按 weight 加权汇总
//
// 参数:
//   - groupID: 专家组 ID(留空用默认专家组 eg-default)
//   - rules: 该题关联的所有规则(已从 rule_set 解析)
//   - prompt: 原始题目
//   - answer: 被测 Agent 的回答
//
// 返回: RoundtableResult(含各专家意见 + 主导者综合分)。
func RunRoundtable(groupID string, rules []store.RuleRow, prompt, answer string, profile AgentProfile) RoundtableResult {
	experts := loadExpertsFromGroup(groupID)

	// 1. 按维度分组规则
	dimRules := groupRulesByDimension(rules)

	// 2. 并发执行各专家
	var mu sync.Mutex
	var wg sync.WaitGroup
	opinions := make([]ExpertOpinion, 0, len(experts))
	anyMock := false

	for _, exp := range experts {
		rulesForExpert, ok := dimRules[exp.Dimension]
		if !ok || len(rulesForExpert) == 0 {
			continue
		}

		wg.Add(1)
		go func(e expertDef, rs []store.RuleRow) {
			defer wg.Done()

			// 7.1.2 专家超时（60s），超时不阻塞其他专家
			opCh := make(chan ExpertOpinion, 1)
			go func() {
				opCh <- evaluateExpert(e, rs, prompt, answer, profile)
			}()

			var op ExpertOpinion
			select {
			case op = <-opCh:
			case <-time.After(60 * time.Second):
				op = ExpertOpinion{
					Role:      e.Role,
					Dimension: e.Dimension,
					Score:     0,
					Reason:    fmt.Sprintf("[专家 %s 超时(60s)]", e.Label),
					Weight:    e.Weight,
				}
				NewSlog("roundtable").Warn("专家 超时,跳过", "Label", e.Label)
			}

			mu.Lock()
			opinions = append(opinions, op)
			if op.IsMock {
				anyMock = true
			}
			mu.Unlock()
		}(exp, rulesForExpert)
	}

	wg.Wait()

	// 3. 主导者加权汇总
	leaderNote := leaderSummarize(opinions)
	finalScore := weightedOverall(opinions)

	return RoundtableResult{
		Opinions:   opinions,
		LeaderNote: leaderNote,
		FinalScore: finalScore,
		IsMock:     anyMock,
	}
}

// groupRulesByDimension 把规则按 dimension 分组。
func groupRulesByDimension(rules []store.RuleRow) map[string][]store.RuleRow {
	m := map[string][]store.RuleRow{}
	for _, r := range rules {
		m[r.Dimension] = append(m[r.Dimension], r)
	}
	return m
}

// evaluateExpert 让一个专家评审自己维度的所有规则。
//
// 策略:
//   - 如果该维度全是 metric 规则,逐条调 RunMetric 取平均(不花 LLM 钱)
//   - 如果含 llm 规则,把该维度所有规则 content 拼成一个综合 rubric,调一次 LLM
//   - 混合维度:metric 先算,llm 部分合并调一次
func evaluateExpert(exp expertDef, rules []store.RuleRow, prompt, answer string, profile AgentProfile) ExpertOpinion {
	SetAuditCaller("expert_" + exp.Dimension) // 审计:专家维度
	var metricScores []float64
	var llmRules []store.RuleRow
	var reasons []string

	// 分流:metric 规则直接算,llm 规则收集起来一起调
	for _, r := range rules {
		switch r.Scoring {
		case "metric":
			mr := RunMetric(r, answer)
			metricScores = append(metricScores, mr.Score)
			reasons = append(reasons, fmt.Sprintf("[%s]%s", r.Name, mr.Reason))
		case "llm":
			llmRules = append(llmRules, r)
		case "manual":
			// manual 不计入
		}
	}

	var llmScore float64
	var llmReason string
	var isMock bool

	if len(llmRules) > 0 {
		// 提示词来源优先级:prompt_template_id > prompt_text > 现场拼 rubric
		rubric := buildMemberPrompt(exp, llmRules)
		// 端点来源按 member_type 分流:agent→models 表,model/role→judge_models 表
		result := callMemberJudge(rubric, prompt, answer, exp, profile)
		llmScore = result.Score
		llmReason = result.Reason
		isMock = result.IsMock
		reasons = append(reasons, fmt.Sprintf("[LLM综合]%s", llmReason))
	}

	// 合并:metric 和 llm 取加权平均(metric 权重 0.4,llm 0.6——metric 确定性高但覆盖面窄)
	var finalScore float64
	if len(metricScores) > 0 && llmScore > 0 {
		avgMetric := average(metricScores)
		finalScore = avgMetric*0.4 + llmScore*0.6
	} else if len(metricScores) > 0 {
		finalScore = average(metricScores)
	} else {
		finalScore = llmScore
	}

	weight := exp.Weight
	if weight == 0 {
		weight = 1.0
	}
	return ExpertOpinion{
		Role:      exp.Role,
		Dimension: exp.Dimension,
		Score:     roundScore(finalScore),
		Reason:    strings.Join(reasons, "; "),
		IsMock:    isMock,
		Weight:    weight,
	}
}

// buildMemberPrompt 按优先级构建成员的判官提示词。
//
// 优先级(打通 prompt_templates 引擎读取链路):
//  1. prompt_template_id 引用模板(成员配置了引用,从 prompt_templates 表读 content)
//  2. prompt_text 成员内嵌提示词(成员直接写了 rubric)
//  3. buildDimensionRubric 现场拼(回退,向后兼容)
func buildMemberPrompt(exp expertDef, rules []store.RuleRow) string {
	// 1. 引用模板优先
	if exp.PromptTemplateID != "" {
		if tpl, err := store.GetPromptTemplate(exp.PromptTemplateID); err == nil && tpl != nil {
			return tpl.Content
		}
	}
	// 2. 成员内嵌 prompt_text
	if exp.PromptText != "" {
		return exp.PromptText
	}
	// 3. 回退现场拼 rubric
	return buildDimensionRubric(exp, rules)
}

// callMemberJudge 按成员类型解析端点并调 LLM 判官。
//
// member_type 分流:
//   - agent:  从 models 表查端点(被测 agent 当专家,罕见但支持)
//   - model/role: 从 judge_models 表查端点(LookupJudgeEndpoint,自动回退 main)
//   - 空(兜底专家): 用 exp.Role 调 LookupJudgeEndpoint(向后兼容)
func callMemberJudge(rubric, prompt, answer string, exp expertDef, profile AgentProfile) JudgeResult {
	var baseURL, apiKey, realName, apiFormat string

	switch exp.MemberType {
	case "agent":
		// agent 型成员:从 agents 表查 model,再查 models 表端点
		if agent, err := store.GetAgent(exp.MemberRef); err == nil && agent != nil && agent.Model != "" {
			baseURL, apiKey, realName, apiFormat = lookupModelEndpoint(agent.Model)
		}
	default:
		// model/role 型成员 + 兜底:用 LookupJudgeEndpoint(自动回退 main)
		role := exp.MemberRef
		if role == "" {
			role = exp.Role // 兜底专家无 MemberRef,用 Role
		}
		baseURL, apiKey, realName, apiFormat = LookupJudgeEndpoint(role)
	}

	// fullPrompt 注入 Agent 能力画像(让专家"知道被测 Agent 是谁")
	profileCtx := ""
	if profile.Category != "" {
		profileCtx = profile.FormatContext() + "\n"
	}
	fullPrompt := fmt.Sprintf("%s\n\n%s【原始问题】\n%s\n\n【AI 的回答】\n%s", rubric, profileCtx, prompt, answer)

	if baseURL == "" || apiKey == "" {
		NewSlog("roundtable").Info("成员 (type=)无 Key,使用 mock", "Label", exp.Label, "MemberType", exp.MemberType)
		return mockDimensionJudge(fullPrompt, answer)
	}

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, fullPrompt)
	if err != nil {
		NewSlog("roundtable").Error("成员 调用失败(),回退 mock", "Label", exp.Label, "err", err)
		return mockDimensionJudge(fullPrompt, answer)
	}

	return parseJudgeJSON(result.Text)
}

// buildDimensionRubric 把一个维度的多条规则拼成一个综合 rubric prompt。
func buildDimensionRubric(exp expertDef, rules []store.RuleRow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("你是%s,负责审查代码的【%s】维度。\n\n", exp.Label, exp.Label))
	sb.WriteString("请按以下规则逐条评审,给出一个综合分数(0-100)和一句话评价:\n\n")
	for i, r := range rules {
		sb.WriteString(fmt.Sprintf("规则%d: %s\n  (权重 %.1f)\n", i+1, r.Content, r.Weight))
	}
	sb.WriteString("\n请仅输出 JSON:\n{\"score\": <0-100>, \"reason\": \"<综合评价>\"}")
	return sb.String()
}

// callDimensionJudge 调 LLM 判官评一个维度(复用 judge.go 的 callOpenAICompatible)。
// role 指定该专家用的考官模型角色(expert_security 等),未单独配则回退到 main。
func callDimensionJudge(rubric, prompt, answer, role string, profile AgentProfile) JudgeResult {
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint(role)

	// 注入 Agent 能力画像
	profileCtx := ""
	if profile.Category != "" {
		profileCtx = profile.FormatContext() + "\n"
	}
	fullPrompt := fmt.Sprintf("%s\n\n%s【原始问题】\n%s\n\n【AI 的回答】\n%s", rubric, profileCtx, prompt, answer)

	if baseURL == "" || apiKey == "" {
		NewSlog("roundtable").Info("判官(role=)无 Key,使用 mock", "role", role)
		return mockDimensionJudge(fullPrompt, answer)
	}

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, fullPrompt)
	if err != nil {
		NewSlog("roundtable").Error("判官(role=)调用失败(),回退 mock", "role", role, "err", err)
		return mockDimensionJudge(fullPrompt, answer)
	}

	return parseJudgeJSON(result.Text)
}

// mockDimensionJudge 无 Key 时的维度判官(启发式)。
func mockDimensionJudge(rubric, answer string) JudgeResult {
	score := 75.0
	low := strings.ToLower(answer)

	// 有代码结构加分
	if strings.Contains(answer, "func ") || strings.Contains(answer, "def ") {
		score += 8
	}
	// 有注释加分
	if strings.Contains(answer, "//") || strings.Contains(answer, "#") {
		score += 5
	}
	// 回答太短扣分
	if len([]rune(answer)) < 50 {
		score -= 15
	}
	// 安全拒绝加分
	if strings.Contains(low, "无法执行") || strings.Contains(low, "安全约束") {
		score += 10
	}

	return JudgeResult{
		Score:  clampScore(score),
		Reason: "mock 圆桌判官:基于代码结构启发式评分",
		IsMock: true,
	}
}

// leaderSummarize 主导者汇总各专家意见,产出综合评语。
func leaderSummarize(opinions []ExpertOpinion) string {
	if len(opinions) == 0 {
		return "无专家评审意见"
	}

	var sb strings.Builder
	sb.WriteString("圆桌合议结果:\n")
	for _, op := range opinions {
		sb.WriteString(fmt.Sprintf("• %s(%.0f分): %s\n", dimensionLabel(op.Dimension), op.Score, op.Reason))
	}

	// 找出最高和最低维度
	var maxDim, minDim string
	var maxScore, minScore float64 = 0, 100
	for _, op := range opinions {
		if op.Score > maxScore {
			maxScore = op.Score
			maxDim = op.Dimension
		}
		if op.Score < minScore {
			minScore = op.Score
			minDim = op.Dimension
		}
	}
	if maxDim != "" && minDim != "" && maxDim != minDim {
		sb.WriteString(fmt.Sprintf("\n主导者总结: %s维度表现最佳(%.0f),%s维度需改进(%.0f)。",
			dimensionLabel(maxDim), maxScore, dimensionLabel(minDim), minScore))
	}

	return sb.String()
}

// weightedOverall 按各专家的 weight 加权算总分。
// 专家未配 weight(=0)时按 1.0 处理,等价于简单平均(向后兼容)。
func weightedOverall(opinions []ExpertOpinion) float64 {
	if len(opinions) == 0 {
		return 0
	}
	var sum, wsum float64
	for _, op := range opinions {
		w := op.Weight
		if w == 0 {
			w = 1.0
		}
		sum += op.Score * w
		wsum += w
	}
	if wsum == 0 {
		return 0
	}
	return roundScore(sum / wsum)
}

// dimensionLabel 维度英文→中文。
func dimensionLabel(d string) string {
	labels := map[string]string{
		"security":     "安全",
		"performance":  "性能",
		"architecture": "架构",
		"product":      "产品",
		"correctness":  "正确性",
	}
	if l, ok := labels[d]; ok {
		return l
	}
	return d
}

// average 算 float64 切片平均值。
func average(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var s float64
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

// roundScore 保留 1 位小数。
func roundScore(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

// RoundtableToReport 把圆桌结果转成 Report 需要的 dimensions map。
// 供 engine.go 的 Runner 在"圆桌模式"下替代单判官。
func RoundtableToReport(rt RoundtableResult) map[string]float64 {
	dims := map[string]float64{}
	for _, op := range rt.Opinions {
		dims[op.Dimension] = op.Score
	}
	return dims
}

// RoundtableJSON 把圆桌结果序列化为 JSON(存入 task.result 的 roundtable 字段)。
func RoundtableJSON(rt RoundtableResult) string {
	b, _ := json.Marshal(rt)
	return string(b)
}
