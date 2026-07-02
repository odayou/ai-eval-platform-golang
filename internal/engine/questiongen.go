package engine

// questiongen.go — 出题子 Agent + 动态扩题引擎(MRD 6.2 / 3.4)。
//
// 两种出题模式:
//   1. 圆桌出题:多专家讨论后出题(覆盖每个评测维度)
//   2. 动态扩题:Agent 在某题失败后,当场生成同类变体题加试
//
// 出题流程(圆桌模式):
//   - 读规则集 → 按维度分组 → 每个专家生成该维度的题目
//   - 合并去重 → 写入 questions 表
//
// 动态扩题(弱点捕捉):
//   - 某题得分 < 阈值 → 读该题的 prompt + 失败原因
//   - 调 LLM 生成 N 道同维度/同难度的变体题
//   - 写入 questions 表,标记 source=dynamic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// GeneratedQuestion 是出题 Agent 产出的一道题。
type GeneratedQuestion struct {
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Expected   string `json:"expected"`
	Dimension  string `json:"dimension"`
	Difficulty string `json:"difficulty"`
	Source     string `json:"source"` // ai / dynamic / adaptive
}

// GenerateQuestions 圆桌出题:让各专家按自己维度出题。
//
// 参数:
//   - scene: 评测场景(agent/model/...)
//   - ruleSetID: 规则集 ID(决定哪些维度需要出题)
//   - countPerDim: 每个维度出几道题
//
// 返回:生成的题目列表(已写入 questions 表)。
func GenerateQuestions(scene, ruleSetID string, countPerDim int) ([]GeneratedQuestion, error) {
	// 读规则集
	rs, err := store.GetRuleSet(ruleSetID)
	if err != nil {
		return nil, fmt.Errorf("rule_set %s not found: %w", ruleSetID, err)
	}
	ruleIDs := store.ParseRuleIDs(rs.RuleIDs)
	rules, err := store.GetRulesByIDs(ruleIDs)
	if err != nil {
		return nil, err
	}

	// 按维度分组
	dimRules := groupRulesByDimension(rules)
	var allQuestions []GeneratedQuestion

	for dim, rs := range dimRules {
		expertLabel := dimensionLabel(dim)
		questions, err := generateForDimension(scene, expertLabel, dim, rs, countPerDim, ruleSetID)
		if err != nil {
			NewSlog("questiongen").Error("维度 出题失败", "dim", dim, "err", err)
			continue
		}
		allQuestions = append(allQuestions, questions...)
	}

	NewSlog("questiongen").Info("圆桌出题完成: 道(场景=, 规则集=)", "allQuestions_count", len(allQuestions), "scene", scene, "ruleSetID", ruleSetID)
	return allQuestions, nil
}

// generateForDimension 让一个专家为某个维度出题。
func generateForDimension(scene, expertLabel, dimension string, rules []store.RuleRow, count int, ruleSetID string) ([]GeneratedQuestion, error) {
	// 构建出题 prompt
	rubric := buildQuestionPrompt(expertLabel, dimension, rules, count)

	// 调 LLM 出题(或 mock)
	questions, err := callQuestionGen(rubric, scene, dimension, count, ruleSetID)
	if err != nil {
		return nil, err
	}

	// 写入数据库
	for _, q := range questions {
		id := uuid.New().String()
		store.InsertQuestion(store.QuestionCreateParams{
			ID: id, Scene: scene, Title: q.Title, Prompt: q.Prompt,
			Expected: q.Expected, Difficulty: q.Difficulty, Source: "ai",
			Tags: "[]", RuleSetID: ruleSetID, CreatedAt: time.Now().Format(time.RFC3339),
		})
		q.Dimension = dimension
	}

	return questions, nil
}

// buildQuestionPrompt 构建出题 agent 的 prompt。
func buildQuestionPrompt(expertLabel, dimension string, rules []store.RuleRow, count int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("你是%s,负责为【%s】维度出评测题。\n\n", expertLabel, expertLabel))
	sb.WriteString("请根据以下规则,生成评测题目:\n\n")
	for i, r := range rules {
		sb.WriteString(fmt.Sprintf("规则%d: %s\n", i+1, r.Content))
	}
	sb.WriteString(fmt.Sprintf("\n请生成 %d 道题,每道题包含:\n", count))
	sb.WriteString("- title: 简短标题\n")
	sb.WriteString("- prompt: 喂给 AI 的完整问题\n")
	sb.WriteString("- expected: 期望回答的关键点(可空)\n")
	sb.WriteString("- difficulty: entry/mid/high/adversarial\n\n")
	sb.WriteString(`请仅输出 JSON 数组:
[{"title":"...","prompt":"...","expected":"...","difficulty":"mid"}]`)
	return sb.String()
}

// callQuestionGen 调 LLM 出题(或 mock)。
func callQuestionGen(prompt, scene, dimension string, count int, ruleSetID string) ([]GeneratedQuestion, error) {
	// 考官模型:优先 question_gen 角色,LookupJudgeEndpoint 内部自动回退到 main
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("question_gen")

	if baseURL == "" || apiKey == "" {
		NewSlog("questiongen").Info("出题模型无 Key,使用 mock 出题")
		return mockGenerateQuestions(dimension, count), nil
	}

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, prompt)
	if err != nil {
		NewSlog("questiongen").Error("出题调用失败(),回退 mock", "err", err)
		return mockGenerateQuestions(dimension, count), nil
	}

	return parseGeneratedQuestions(result.Text, count)
}

// parseGeneratedQuestions 解析 LLM 返回的题目 JSON。
func parseGeneratedQuestions(text string, maxCount int) ([]GeneratedQuestion, error) {
	// 尝试直接解析
	var qs []GeneratedQuestion
	if err := json.Unmarshal([]byte(text), &qs); err == nil && len(qs) > 0 {
		if len(qs) > maxCount {
			qs = qs[:maxCount]
		}
		return qs, nil
	}

	// 兜底:从文本中提取 JSON 数组
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start >= 0 && end > start {
		jsonStr := text[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &qs); err == nil && len(qs) > 0 {
			if len(qs) > maxCount {
				qs = qs[:maxCount]
			}
			return qs, nil
		}
	}

	return nil, fmt.Errorf("failed to parse generated questions from: %s", text[:min(200, len(text))])
}

// mockGenerateQuestions 无 Key 时的 mock 出题。
func mockGenerateQuestions(dimension string, count int) []GeneratedQuestion {
	templates := map[string][]GeneratedQuestion{
		"security": {
			{Title: "XSS 防御实现", Prompt: "实现一个 HTML 转义函数,防止 XSS 攻击。", Expected: "应对 <, >, &, \", ' 进行转义", Difficulty: "mid"},
			{Title: "JWT Token 验证", Prompt: "实现 JWT Token 的验证中间件,处理过期和篡改。", Expected: "应验证签名和过期时间", Difficulty: "high"},
			{Title: "CSRF Token 生成", Prompt: "为 Web 表单生成 CSRF Token 并实现验证逻辑。", Expected: "Token 应与 session 绑定", Difficulty: "mid"},
		},
		"performance": {
			{Title: "连接池实现", Prompt: "实现一个数据库连接池,支持最大连接数和超时。", Expected: "应使用 channel 或 sync.Pool", Difficulty: "high"},
			{Title: "缓存淘汰策略", Prompt: "实现 LRU 缓存,支持 O(1) 的 Get/Put 操作。", Expected: "应使用双向链表 + 哈希表", Difficulty: "mid"},
			{Title: "批量写入优化", Prompt: "将逐条 INSERT 改为批量写入,提升 10 倍性能。", Expected: "应使用事务 + 多值 INSERT", Difficulty: "entry"},
		},
		"architecture": {
			{Title: "依赖注入容器", Prompt: "实现一个简单的依赖注入容器,支持单例和工厂模式。", Expected: "应使用 reflect 或 register/get 模式", Difficulty: "high"},
			{Title: "事件总线设计", Prompt: "设计一个进程内事件总线,支持同步/异步发布和订阅。", Expected: "应解耦生产者和消费者", Difficulty: "mid"},
			{Title: "配置中心抽象", Prompt: "设计一个配置中心接口,支持本地文件和远程配置源。", Expected: "应使用策略模式", Difficulty: "mid"},
		},
		"product": {
			{Title: "分页查询接口", Prompt: "设计一个通用分页查询接口,支持游标分页和偏移分页。", Expected: "应处理边界情况", Difficulty: "entry"},
			{Title: "错误码体系", Prompt: "设计一套错误码体系,支持国际化和错误链。", Expected: "应包含业务码和 HTTP 状态码映射", Difficulty: "mid"},
			{Title: "API 版本管理", Prompt: "设计 RESTful API 的版本管理方案,支持平滑升级。", Expected: "应考虑向后兼容", Difficulty: "mid"},
		},
	}

	qs, ok := templates[dimension]
	if !ok || len(qs) == 0 {
		qs = templates["architecture"] // 兜底
	}
	if count > len(qs) {
		count = len(qs)
	}
	return qs[:count]
}

// ExpandQuestions 动态扩题:Agent 在某题失败后,生成同类变体题。
//
// 参数:
//   - questionID: 失败的原题 ID
//   - count: 生成几道变体
//
// 流程:读原题 → 读失败原因 → 调 LLM 生成变体 → 写入 questions 表。
func ExpandQuestions(questionID string, count int) ([]GeneratedQuestion, error) {
	// 读原题
	var q store.QuestionRow
	err := store.DB.QueryRow(`SELECT id, scene, title, prompt, COALESCE(expected,''), difficulty, source,
		COALESCE(rule_set_id,'') FROM questions WHERE id = ?`, questionID).
		Scan(&q.ID, &q.Scene, &q.Title, &q.Prompt, &q.Expected, &q.Difficulty, &q.Source, &q.RuleSetID)
	if err != nil {
		return nil, fmt.Errorf("question %s not found: %w", questionID, err)
	}

	// 构建扩题 prompt
	prompt := fmt.Sprintf(`你是出题专家。以下是一道评测题,请生成 %d 道同类型、同难度的变体题。

【原题标题】%s
【原题内容】%s
【难度】%s

要求:
1. 变体题应考察相同的知识点/能力,但场景不同
2. 难度与原题一致
3. 每道题包含 title、prompt、expected、difficulty

请仅输出 JSON 数组:
[{"title":"...","prompt":"...","expected":"...","difficulty":"%s"}]`,
		count, q.Title, q.Prompt, q.Difficulty, q.Difficulty)

	// 调 LLM(或 mock)
	questions, err := callQuestionGen(prompt, q.Scene, "mixed", count, q.RuleSetID)
	if err != nil {
		return nil, err
	}

	// 写入数据库(标记 source=dynamic)
	for _, gq := range questions {
		id := uuid.New().String()
		store.InsertQuestion(store.QuestionCreateParams{
			ID: id, Scene: q.Scene, Title: gq.Title, Prompt: gq.Prompt,
			Expected: gq.Expected, Difficulty: gq.Difficulty, Source: "dynamic",
			Tags: "[]", RuleSetID: q.RuleSetID, CreatedAt: time.Now().Format(time.RFC3339),
		})
	}

	NewSlog("questiongen").Info("动态扩题完成:原题=, 新增= 道", "questionID", questionID, "questions_count", len(questions))
	return questions, nil
}

// ──────────────────────────────────────────────────────────────────────────
// 自适应出题(基于 Agent 能力画像)
// ──────────────────────────────────────────────────────────────────────────

// adaptiveQuestionPrompt 自适应出题 prompt 模板。
const adaptiveQuestionPrompt = `你是评测题目设计专家。请为以下类型的 Agent 设计针对性的测试题。

## Agent 能力画像
- 类别: %s
- 领域: %s
- 能力: %s
- 评测重点: %s
- 评审标准: %s

## 出题要求
请设计 %d 道针对该 Agent 核心能力的测试题。每道题应该是该 Agent 在实际使用中会遇到的典型场景。
题目要具体、可执行,能真实考察 Agent 的专业能力。

## 输出格式
严格输出 JSON 数组(不要 markdown 围栏):
[
  {"title":"题目标题","prompt":"给 Agent 的完整指令","expected":"期望的正确输出或行为"}
]`

// GenerateAdaptiveQuestions 根据 Agent 能力画像生成针对性专项题。
// count 是生成数量(建议 3-5 道)。
func GenerateAdaptiveQuestions(profile AgentProfile, count int) []GeneratedQuestion {
	if count <= 0 {
		count = 3
	}

	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("question_gen")
	if baseURL == "" || apiKey == "" {
		NewSlog("adaptive_q").Warn("无出题模型,跳过动态出题")
		return nil
	}

	prompt := fmt.Sprintf(adaptiveQuestionPrompt,
		profile.Category, profile.Domain,
		strings.Join(profile.Capabilities, "、"),
		strings.Join(profile.EvalFocus, "、"),
		profile.CustomRubric,
		count,
	)

	SetAuditCaller("adaptive_question_gen")
	result, err := callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, realName, prompt, "")
	if err != nil {
		NewSlog("adaptive_q").Error("LLM 调用失败(),跳过", "err", err)
		return nil
	}

	questions := parseAdaptiveQuestions(result.Text)
	for i := range questions {
		questions[i].Source = "adaptive"
	}
	NewSlog("adaptive_q").Info("生成 道专项题(类别: )", "questions_count", len(questions), "Category", profile.Category)
	return questions
}

// parseAdaptiveQuestions 解析 LLM 输出的 JSON 题目数组。
func parseAdaptiveQuestions(text string) []GeneratedQuestion {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")

	// 提取 JSON 数组
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end <= start {
		return nil
	}

	var questions []GeneratedQuestion
	if err := json.Unmarshal([]byte(text[start:end+1]), &questions); err != nil {
		// 容错:尝试单对象
		var single GeneratedQuestion
		if err2 := json.Unmarshal([]byte(text[start:end+1]), &single); err2 == nil {
			questions = []GeneratedQuestion{single}
		}
	}
	return questions
}

// 兜底:确保 strings 包被引用(adaptive 出题用到)
var _ = strings.Contains
