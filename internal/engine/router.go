package engine

// router.go — 主 Agent 意图识别路由(MRD 5.0 / 2.0)。
//
// 核心决策(MRD 原话):
//   "不做五个独立的场景 Agent,而是一个主 Agent 统管所有场景。
//    其第一步是意图识别——主 Agent 收到被测对象和评测需求后,
//    先判断这属于哪个场景(Agent 测评 / 大模型测评 / 竞品报告 / PRD / AI 产品),
//    再路由到对应的规则集和执行策略。"
//
// 为什么不做五个独立 Agent:
//   - 五份重复基建(喂题→执行→收结果→打分→归因管线完全一样)
//   - 数据隔离导致跨场景对比做不了
//   - 加新场景还得造一整个 Agent
//   - 单 Agent 模式下,加场景只是加个 Tab + 挂套规则
//
// 设计:
//   - IntentClassifier:分析输入内容,输出场景分类 + 置信度
//   - SceneRouter:根据场景选择规则集 + 执行策略
//   - 执行策略:Agent→沙箱+圆桌, Model→API直调, 其他→文本评审
//
// 意图分类方法:
//   - 关键词启发式(快,免费,准确率 ~80%)
//   - LLM 分类(慢,要钱,准确率 ~95%)
//   - 混合:先关键词,低置信度时 fallback 到 LLM

import (
	"encoding/json"
	"fmt"
	"strings"

	"ai-eval-platform/internal/store"
)

// Scene 是评测场景类型。
type Scene string

const (
	SceneAgent      Scene = "agent"       // Agent 评测
	SceneModel      Scene = "model"       // 大模型评测
	SceneCompetitor Scene = "competitor"   // 竞品报告评测
	ScenePRD        Scene = "prd"         // PRD 评测
	SceneProduct    Scene = "product"     // AI 产品评测
)

// Intent 是意图识别的结果。
type Intent struct {
	Scene      Scene   `json:"scene"`       // 识别出的场景
	Confidence float64 `json:"confidence"`  // 置信度 0-1
	Reason     string  `json:"reason"`      // 识别依据
	Strategy   string  `json:"strategy"`    // 推荐的执行策略
}

// SceneConfig 是每个场景的配置。
type SceneConfig struct {
	Scene       Scene  `json:"scene"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Strategy    string `json:"strategy"`    // sandbox | api_direct | text_review | multimodal
	RuleSetID   string `json:"rule_set_id"` // 默认规则集
	Keywords    []string `json:"keywords"`  // 场景关键词
}

// sceneConfigs 定义了 5 个预置场景的配置。
var sceneConfigs = []SceneConfig{
	{
		Scene:       SceneAgent,
		Label:       "Agent 评测",
		Description: "测各类智能体(编码 Agent、通用 Agent、团队自研 Agent)",
		Strategy:    "sandbox_roundtable", // 沙箱编译 + 圆桌合议
		RuleSetID:   "rs-fullstack",
		Keywords:    []string{"agent", "智能体", "coding", "编码", "代码生成", "tool", "工具调用", "multi-turn", "多轮"},
	},
	{
		Scene:       SceneModel,
		Label:       "大模型评测",
		Description: "测裸模型能力(GLM / DeepSeek / Claude / GPT 等 API)",
		Strategy:    "api_direct", // 直接调 API,不走沙箱
		RuleSetID:   "rs-security",
		Keywords:    []string{"模型", "model", "api", "prompt", "指令遵循", "幻觉", "hallucination", "长文本", "知识"},
	},
	{
		Scene:       SceneCompetitor,
		Label:       "竞品报告评测",
		Description: "测/对比竞品分析报告(Cursor vs Trae、各家 Agent 横评)",
		Strategy:    "text_review", // 纯文本评审,圆桌打分
		RuleSetID:   "rs-arch",
		Keywords:    []string{"竞品", "对比", "competitor", "vs", "横评", "比较", "benchmark"},
	},
	{
		Scene:       ScenePRD,
		Label:       "PRD 评测",
		Description: "测 AI 参与生成的产品文档(需求文档、功能规格)",
		Strategy:    "text_review",
		RuleSetID:   "rs-arch",
		Keywords:    []string{"prd", "需求", "product", "requirement", "功能规格", "文档", "document"},
	},
	{
		Scene:       SceneProduct,
		Label:       "AI 产品评测",
		Description: "测完整的 AI 产品/工具(Trae / Cursor / zCode / 文生图产品)",
		Strategy:    "multimodal_review", // 多模态评审
		RuleSetID:   "rs-fullstack",
		Keywords:    []string{"产品", "product", "tool", "工具", "app", "应用", "功能", "体验"},
	},
}

// ClassifyIntent 对输入内容进行意图分类。
//
// 三种策略:
//  1. 显式指定:用户直接传 scene 字段 → 直接用
//  2. 关键词匹配:分析输入文本的关键词 → 快速分类
//  3. LLM 分类:低置信度时调 LLM → 精确分类(需 Key)
func ClassifyIntent(input string, explicitScene string) Intent {
	// 1. 显式指定
	if explicitScene != "" {
		for _, cfg := range sceneConfigs {
			if string(cfg.Scene) == explicitScene {
				return Intent{
					Scene:      cfg.Scene,
					Confidence: 1.0,
					Reason:     "用户显式指定场景",
					Strategy:   cfg.Strategy,
				}
			}
		}
	}

	// 2. 关键词匹配
	intent := classifyByKeywords(input)
	if intent.Confidence >= 0.6 {
		return intent
	}

	// 3. LLM 分类(fallback)
	llmIntent := classifyByLLM(input)
	if llmIntent.Confidence > intent.Confidence {
		return llmIntent
	}

	// 默认:Agent 评测(最通用的场景)
	return Intent{
		Scene:      SceneAgent,
		Confidence: 0.5,
		Reason:     "无法明确分类,默认 Agent 评测",
		Strategy:   "sandbox_roundtable",
	}
}

// classifyByKeywords 基于关键词的快速分类。
func classifyByKeywords(input string) Intent {
	low := strings.ToLower(input)
	scores := map[Scene]float64{}
	reasons := map[Scene][]string{}

	for _, cfg := range sceneConfigs {
		hitCount := 0
		for _, kw := range cfg.Keywords {
			if strings.Contains(low, kw) {
				hitCount++
				reasons[cfg.Scene] = append(reasons[cfg.Scene], kw)
			}
		}
		if len(cfg.Keywords) > 0 {
			scores[cfg.Scene] = float64(hitCount) / float64(len(cfg.Keywords))
		}
	}

	// 找最高分
	var bestScene Scene
	var bestScore float64
	for scene, score := range scores {
		if score > bestScore {
			bestScore = score
			bestScene = scene
		}
	}

	if bestScore == 0 {
		return Intent{Confidence: 0}
	}

	// 找对应的配置
	for _, cfg := range sceneConfigs {
		if cfg.Scene == bestScene {
			return Intent{
				Scene:      bestScene,
				Confidence: bestScore,
				Reason:     fmt.Sprintf("关键词匹配: %s", strings.Join(reasons[bestScene], ", ")),
				Strategy:   cfg.Strategy,
			}
		}
	}

	return Intent{Confidence: 0}
}

// classifyByLLM 调 LLM 进行意图分类。
func classifyByLLM(input string) Intent {
	prompt := fmt.Sprintf(`你是一个评测平台的意图分类器。请分析以下输入内容,判断它属于哪个评测场景。

【输入内容】
%s

【可选场景】
1. agent — Agent 评测(测智能体、编码能力、工具调用)
2. model — 大模型评测(测裸模型 API、指令遵循、幻率)
3. competitor — 竞品报告评测(对比分析、横评报告)
4. prd — PRD 评测(产品需求文档、功能规格)
5. product — AI 产品评测(完整产品、工具、体验)

请仅输出 JSON:
{"scene": "agent", "confidence": 0.9, "reason": "一句话理由"}`, truncateForClassify(input, 500))

	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("main")

	if baseURL == "" || apiKey == "" {
		return Intent{Confidence: 0} // 无 Key,跳过 LLM 分类
	}

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, prompt)
	if err != nil {
		NewSlog("router").Error("LLM 意图分类失败", "err", err)
		return Intent{Confidence: 0}
	}

	return parseIntentJSON(result.Text)
}

// parseIntentJSON 解析 LLM 返回的意图分类 JSON。
func parseIntentJSON(text string) Intent {
	// 简单解析
	var parsed struct {
		Scene      string  `json:"scene"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}

	// 尝试直接解析
	if err := jsonUnmarshal([]byte(text), &parsed); err == nil && parsed.Scene != "" {
		return Intent{
			Scene:      Scene(parsed.Scene),
			Confidence: parsed.Confidence,
			Reason:     "LLM 分类: " + parsed.Reason,
			Strategy:   getStrategyForScene(Scene(parsed.Scene)),
		}
	}

	return Intent{Confidence: 0}
}

// getStrategyForScene 获取场景对应的执行策略。
func getStrategyForScene(scene Scene) string {
	for _, cfg := range sceneConfigs {
		if cfg.Scene == scene {
			return cfg.Strategy
		}
	}
	return "sandbox_roundtable"
}

// GetSceneConfig 获取场景配置。
func GetSceneConfig(scene Scene) *SceneConfig {
	for _, cfg := range sceneConfigs {
		if cfg.Scene == scene {
			return &cfg
		}
	}
	return nil
}

// GetAllSceneConfigs 获取所有场景配置。
func GetAllSceneConfigs() []SceneConfig {
	return sceneConfigs
}

// RouteForTask 为一个任务选择最优的规则集和执行策略。
func RouteForTask(task store.TaskRow) (ruleSetID string, strategy string, err error) {
	// 1. 意图识别
	intent := ClassifyIntent(task.Name+" "+task.Scene, task.Scene)

	// 2. 选择规则集(优先用任务指定的,否则用场景默认的)
	ruleSetID = task.RuleSet
	if ruleSetID == "" {
		cfg := GetSceneConfig(intent.Scene)
		if cfg != nil {
			ruleSetID = cfg.RuleSetID
		}
	}

	// 3. 选择执行策略
	strategy = intent.Strategy
	if strategy == "" {
		strategy = "sandbox_roundtable"
	}

	NewSlog("router").Info("任务 路由: scene=(%), strategy=, rule_set=", "ID", task.ID, "Scene", intent.Scene, "Confidence100", intent.Confidence*100, "strategy", strategy, "ruleSetID", ruleSetID)
	return
}

// truncateForClassify 截断文本(分类用,不需要太长)。
func truncateForClassify(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// jsonUnmarshal 是 encoding/json.Unmarshal 的薄包装。
func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
