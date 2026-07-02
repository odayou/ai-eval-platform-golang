package engine

// prompt_adapter.go — Prompt 智能平移适配器(MRD 8.5)。
//
// MRD 原话:
//   "不同的模型对 Prompt 的脾气不一样。在本地针对 Claude 疯狂优化的 System Prompt,
//    一键切换成 GPT-4o 后,可能因为模型习惯不同(如对 XML 标签的敏感度)导致不适应。
//    当用户复制 Agent 并切换模型时,主评测 Agent 读取目标模型的偏好,
//    自动建议用户是否将克隆体内的提示词进行结构化平移适配,确保 A/B 测试的绝对公平。"
//
// 设计:
//   1. ModelProfile:记录已知模型的 Prompt 偏好(格式/结构/关键词)
//   2. SourceAnalyzer:分析源 Prompt 的风格特征
//   3. Adapter:根据源→目标的偏好差异,生成具体的适配建议
//
// 模型偏好数据库(基于公开文档+社区最佳实践):
//   - DeepSeek: 喜欢思维链(CoT)、明确步骤、中文友好、不喜欢 XML
//   - GPT-4o:   喜欢结构化(Markdown)、中等长度、喜欢 JSON 输出约束
//   - Claude:   喜欢 XML 标签(<thinking>/<answer>)、长上下文、分节清晰
//   - GLM-4:    喜欢简洁直接、中文代码混合、不喜欢过度结构化
//   - Gemini:   喜欢角色扮演、few-shot 示例、视觉提示
//   - Qwen:     喜欢中文语境、代码注释、分点说明

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ── 模型偏好配置 ──

// ModelProfile 记录一个模型的 Prompt 编写偏好。
type ModelProfile struct {
	ModelID       string   `json:"model_id"`        // 模型标识(如 glm-4, deepseek-chat)
	Vendor        string   `json:"vendor"`           // 厂商
	DisplayName   string   `json:"display_name"`     // 显示名
	Style         string   `json:"style"`            // 偏好风格: structured | cot | minimal | xml | roleplay
	Delimiters    []string `json:"delimiters"`       // 偏好的标签/分隔符(如 ["<thinking>","</thinking>"])
	AvoidPatterns []string `json:"avoid_patterns"`   // 应避免的模式
	BestPractices []string `json:"best_practices"`   // 最佳实践提示
	TokenPerChar  float64  `json:"token_per_char"`   // 中文 token/字符比率(用于成本估算)
	MaxContext    int      `json:"max_context"`      // 最大上下文 token
}

// modelProfiles 是已知模型的 Prompt 偏好数据库。
var modelProfiles = []ModelProfile{
	{
		ModelID: "deepseek-chat", Vendor: "DeepSeek", DisplayName: "DeepSeek-V3",
		Style: "cot",
		Delimiters:    []string{"【", "】", "步骤", "---"},
		AvoidPatterns: []string{"<xml>", "<tag>", "```system", "<|system|>"},
		BestPractices: []string{
			"用【】标记关键信息(而非 XML 标签)",
			"要求模型先思考再回答(显式要求 <｜end▁of▁thinking｜> 前输出推理)",
			"中文指令更有效,可中英混合",
			"每步指令独立成段,用 --- 分隔",
		},
		TokenPerChar: 2.0,
		MaxContext:   128000,
	},
	{
		ModelID: "glm-4-flash", Vendor: "Zhipu", DisplayName: "GLM-4-Flash",
		Style: "minimal",
		Delimiters:    []string{"##", "**", "`"},
		AvoidPatterns: []string{"<thinking>", "<answer>", "```xml"},
		BestPractices: []string{
			"指令简洁直接,不过度结构化",
			"代码和中文可混合,无需特殊分隔",
			"用 Markdown 标题分节即可",
			"避免冗长的角色设定,直接给任务",
		},
		TokenPerChar: 1.8,
		MaxContext:   128000,
	},
	{
		ModelID: "claude-", Vendor: "Anthropic", DisplayName: "Claude",
		Style: "xml",
		Delimiters:    []string{"<thinking>", "</thinking>", "<answer>", "</answer>", "<instruction>", "</instruction>"},
		AvoidPatterns: []string{"【", "】", "步骤一/二/三"},
		BestPractices: []string{
			"用 XML 标签包裹不同语义块(<thinking>/<answer>/<constraint>)",
			"长篇指令分 Section,用 <section> 包裹",
			"约束条件放入 <constraint> 标签",
			"角色设定用 <role> 标签",
		},
		TokenPerChar: 2.2,
		MaxContext:   200000,
	},
	{
		ModelID: "gpt-4o", Vendor: "OpenAI", DisplayName: "GPT-4o",
		Style: "structured",
		Delimiters:    []string{"###", "```json", "**"},
		AvoidPatterns: []string{"<thinking>", "【", "】", "<|"},
		BestPractices: []string{
			"用 Markdown 标题层级组织指令",
			"输出格式约束用 JSON Schema 描述",
			"用 **粗体** 强调关键要求",
			"使用 ### 分节,每节单一职责",
		},
		TokenPerChar: 2.2,
		MaxContext:   128000,
	},
	{
		ModelID: "gpt-4", Vendor: "OpenAI", DisplayName: "GPT-4",
		Style: "structured",
		Delimiters:    []string{"###", "```json"},
		AvoidPatterns: []string{"<thinking>", "【", "】"},
		BestPractices: []string{
			"详细但有层次地组织指令",
			"用 JSON 约束输出格式",
			"分步骤说明复杂任务",
		},
		TokenPerChar: 2.2,
		MaxContext:   128000,
	},
	{
		ModelID: "gemini", Vendor: "Google", DisplayName: "Gemini",
		Style: "roleplay",
		Delimiters:    []string{"**", "*", "##"},
		AvoidPatterns: []string{"<thinking>", "<|system|>"},
		BestPractices: []string{
			"用角色扮演设定上下文(You are an expert...)",
			"提供 few-shot 示例(至少 2 个)",
			"视觉相关任务放入 [image] 占位符",
			"多层嵌套指令用 **粗体** 强调层级",
		},
		TokenPerChar: 2.5,
		MaxContext:   1048576,
	},
	{
		ModelID: "qwen", Vendor: "Alibaba", DisplayName: "Qwen",
		Style: "minimal",
		Delimiters:    []string{"##", "1.", "2.", "```"},
		AvoidPatterns: []string{"<thinking>", "<|im_start|>"},
		BestPractices: []string{
			"中文语境友好,可直接用中文指令",
			"代码要求写注释(中文注释有效)",
			"用数字分点说明,比 XML 更有效",
			"角色设定简洁,不用过度包装",
		},
		TokenPerChar: 1.7,
		MaxContext:   131072,
	},
}

// ── 适配请求/响应 ──

// AdaptationRequest 是一次 Prompt 适配请求。
type AdaptationRequest struct {
	SourcePrompt string `json:"source_prompt"` // 原始 Prompt(针对源模型优化过)
	SourceModel  string `json:"source_model"`  // 源模型标识(如 "deepseek-chat")
	TargetModel  string `json:"target_model"`  // 目标模型标识(如 "gpt-4o")
	PromptName   string `json:"prompt_name"`   // 可选的 Prompt 名称(日志用)
}

// AdaptationResult 是一次适配建议的结果。
type AdaptationResult struct {
	SourceProfile    ModelProfile       `json:"source_profile"`    // 源模型偏好
	TargetProfile    ModelProfile       `json:"target_profile"`    // 目标模型偏好
	StyleConflict    string             `json:"style_conflict"`    // 风格冲突描述
	Suggestions      []AdaptationSuggestion `json:"suggestions"`   // 适配建议列表
	AdaptedPrompt    string             `json:"adapted_prompt"`    // 自动适配后的 Prompt(预览)
	Confidence       float64            `json:"confidence"`        // 适配置信度
}

// AdaptationSuggestion 是一条具体的适配建议。
type AdaptationSuggestion struct {
	Type        string `json:"type"`         // "replace" | "add" | "remove" | "restructure"
	Severity    string `json:"severity"`     // "must" | "should" | "may"
	Description string `json:"description"`  // 建议描述
	Before      string `json:"before"`       // 原文片段(如适用)
	After       string `json:"after"`        // 建议替换(如适用)
	Reason      string `json:"reason"`       // 适配原因
}

// ── 适配引擎 ──

// AdaptPrompt 分析源 Prompt 并生成适配到目标模型的建议。
//
// 流程:
//   1. 查找源模型和目标模型的偏好配置
//   2. 分析源 Prompt 的风格特征
//   3. 比较两模型的风格差异,生成适配建议
//   4. 可选:调用 LLM 做最终的精细适配
func AdaptPrompt(req AdaptationRequest) (*AdaptationResult, error) {
	srcProfile := lookupModelProfile(req.SourceModel)
	tgtProfile := lookupModelProfile(req.TargetModel)

	if srcProfile == nil || tgtProfile == nil {
		return nil, fmt.Errorf("未知模型: source=%s target=%s (已知: deepseek-chat, glm-4-flash, gpt-4o, claude-, gemini, qwen)", req.SourceModel, req.TargetModel)
	}

	if srcProfile.Style == tgtProfile.Style {
		// 风格相同,无需适配
		return &AdaptationResult{
			SourceProfile: *srcProfile,
			TargetProfile: *tgtProfile,
			StyleConflict: fmt.Sprintf("两模型风格相同(%s),无需适配", srcProfile.Style),
			Suggestions:   nil,
			AdaptedPrompt: req.SourcePrompt,
			Confidence:    1.0,
		}, nil
	}

	// 分析源 Prompt 特征
	features := analyzePromptFeatures(req.SourcePrompt, srcProfile)

	// 生成适配建议
	suggestions := generateSuggestions(req.SourcePrompt, *srcProfile, *tgtProfile, features)

	// 生成适配预览
	adapted := applySuggestions(req.SourcePrompt, suggestions)

	// 评估置信度
	confidence := estimateConfidence(suggestions)

	styleConflict := fmt.Sprintf("源模型(%s)偏好「%s」风格,目标模型(%s)偏好「%s」风格,存在适配需求",
		srcProfile.DisplayName, styleLabel(srcProfile.Style),
		tgtProfile.DisplayName, styleLabel(tgtProfile.Style))

	result := &AdaptationResult{
		SourceProfile: *srcProfile,
		TargetProfile: *tgtProfile,
		StyleConflict: styleConflict,
		Suggestions:   suggestions,
		AdaptedPrompt: adapted,
		Confidence:    confidence,
	}

	NewSlog("prompt_adapter").Info("(→): 条建议, 置信度", "PromptName", req.PromptName, "DisplayName", srcProfile.DisplayName, "DisplayName", tgtProfile.DisplayName, "suggestions_count", len(suggestions), "confidence", confidence)

	return result, nil
}

// AdaptPromptWithLLM 调用 LLM 做最终的精细适配(回退到启发式)。
func AdaptPromptWithLLM(req AdaptationRequest) (*AdaptationResult, error) {
	// 先用启发式生成初步建议
	result, err := AdaptPrompt(req)
	if err != nil {
		return nil, err
	}
	if len(result.Suggestions) == 0 {
		return result, nil
	}

	// 尝试用 LLM 精调(考官模型)
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("main")
	if baseURL == "" || apiKey == "" {
		return result, nil // 无 Key,返回启发式结果
	}

	prompt := fmt.Sprintf(`你是 Prompt 工程专家。以下是 Prompt 适配任务:

【源模型偏好】%s 风格: %s
【目标模型偏好】%s 风格: %s

【已有建议】
%s

【原始 Prompt】
%s

请基于以上建议,输出适配后的完整 Prompt。只输出适配后的 Prompt 文本,不要额外解释。`,
		result.SourceProfile.DisplayName, styleLabel(result.SourceProfile.Style),
		result.TargetProfile.DisplayName, styleLabel(result.TargetProfile.Style),
		formatSuggestionsForLLM(result.Suggestions),
		req.SourcePrompt)

	resp, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, prompt)
	if err == nil && len(resp.Text) > 20 {
		result.AdaptedPrompt = resp.Text
		result.Confidence = 0.95
		NewSlog("prompt_adapter").Info("LLM 精调完成: 字符 → 字符", "SourcePrompt", len([]rune(req.SourcePrompt)), "Text", len([]rune(resp.Text)))
	}

	return result, nil
}

// ── 内部函数 ──

// lookupModelProfile 按模型 ID 前缀匹配查找配置。
func lookupModelProfile(modelID string) *ModelProfile {
	low := strings.ToLower(modelID)
	// 精确匹配优先
	for i := range modelProfiles {
		if strings.EqualFold(modelProfiles[i].ModelID, low) {
			return &modelProfiles[i]
		}
	}
	// 前缀匹配
	for i := range modelProfiles {
		if strings.HasPrefix(low, modelProfiles[i].ModelID) {
			return &modelProfiles[i]
		}
	}
	// 默认返回 GPT-4o 配置(最通用)
	for i := range modelProfiles {
		if modelProfiles[i].ModelID == "gpt-4o" {
			return &modelProfiles[i]
		}
	}
	return nil
}

// PromptFeatures 是源 Prompt 的风格特征。
type PromptFeatures struct {
	HasXML       bool     // 是否含 XML 标签
	HasBrackets  bool     // 是否含【】标记
	HasMarkdown  bool     // 是否含 Markdown 标题
	HasCoT       bool     // 是否含思维链要求
	HasJSON      bool     // 是否含 JSON 约束
	HasRoleplay  bool     // 是否含角色设定
	HasFewshot   bool     // 是否含 few-shot 示例
	Length       int      // 长度(字符)
}

// analyzePromptFeatures 分析源 Prompt 的风格特征。
func analyzePromptFeatures(prompt string, src *ModelProfile) PromptFeatures {
	low := strings.ToLower(prompt)
	return PromptFeatures{
		HasXML:      strings.Contains(low, "<thinking>") || strings.Contains(low, "<answer>") || strings.Contains(low, "<role>") || strings.Contains(low, "<instruction>"),
		HasBrackets: strings.Contains(low, "【") || strings.Contains(low, "】"),
		HasMarkdown: strings.Contains(low, "### ") || strings.Contains(low, "## ") || strings.Contains(low, "**"),
		HasCoT:      strings.Contains(low, "思考") || strings.Contains(low, "推理") || strings.Contains(low, "步骤") || strings.Contains(low, "step"),
		HasJSON:     strings.Contains(low, "json") || strings.Contains(low, "{") || strings.Contains(low, "schema"),
		HasRoleplay: strings.Contains(low, "你是") || strings.Contains(low, "you are") || strings.Contains(low, "role"),
		HasFewshot:  strings.Contains(low, "示例") || strings.Contains(low, "example") || strings.Contains(low, "for example"),
		Length:      len([]rune(prompt)),
	}
}

// generateSuggestions 根据风格差异生成适配建议。
func generateSuggestions(prompt string, src, tgt ModelProfile, feats PromptFeatures) []AdaptationSuggestion {
	var suggestions []AdaptationSuggestion

	// 规则 1: XML → 非 XML 风格
	if feats.HasXML && tgt.Style != "xml" {
		suggestions = append(suggestions, AdaptationSuggestion{
			Type: "replace", Severity: "must",
			Description: fmt.Sprintf("移除 XML 标签,改用 %s 风格的分隔符", styleLabel(tgt.Style)),
			Before:  extractXMLSnippet(prompt, 80),
			After:   convertXMLToStyle(prompt, tgt),
			Reason:   fmt.Sprintf("%s 对 XML 标签敏感度低,偏好 %s 格式", tgt.DisplayName, tgt.Delimiters[0]),
		})
	}

	// 规则 2: 【】→ 目标格式
	if feats.HasBrackets && !containsAny(tgt.Delimiters, "【") {
		suggestions = append(suggestions, AdaptationSuggestion{
			Type: "replace", Severity: "should",
			Description: fmt.Sprintf("将【】标记替换为 %s 偏好的分隔方式", tgt.DisplayName),
			Before:  extractBracketSnippet(prompt, 60),
			After:   "见适配预览",
			Reason:   fmt.Sprintf("%s 对中文方括号理解不稳定,建议用 %s 替代", tgt.DisplayName, strings.Join(tgt.Delimiters[:min(2, len(tgt.Delimiters))], " 或 ")),
		})
	}

	// 规则 3: CoT 风格差异
	if feats.HasCoT && tgt.Style == "minimal" {
		suggestions = append(suggestions, AdaptationSuggestion{
			Type: "remove", Severity: "may",
			Description: fmt.Sprintf("简化思维链要求, %s 偏好简洁指令", tgt.DisplayName),
			Before:  "请先思考...然后回答...",
			After:   "直接给出答案",
			Reason:   fmt.Sprintf("%s 在简洁模式下不擅长长链推理,显式要求可能降低输出质量", tgt.DisplayName),
		})
	}

	// 规则 4: 非 XML → XML
	if !feats.HasXML && tgt.Style == "xml" && feats.Length > 100 {
		suggestions = append(suggestions, AdaptationSuggestion{
			Type: "add", Severity: "should",
			Description: "用 XML 标签包裹不同语义块(Claude 偏好)",
			Before:  prompt[:min(80, len(prompt))] + "...",
			After:   wrapInXML(prompt),
			Reason:   "Claude 对 XML 标签里的内容理解更精准,用 <instruction>/<constraint>/<output_format> 分块能提升遵循度",
		})
	}

	// 规则 5: 角色扮演差异
	if feats.HasRoleplay && tgt.Style == "minimal" {
		suggestions = append(suggestions, AdaptationSuggestion{
			Type: "restructure", Severity: "may",
			Description: "简化角色设定,直接给任务指令",
			Before:  extractRoleSnippet(prompt, 80),
			After:   "直接说明任务和约束即可",
			Reason:   fmt.Sprintf("%s 对冗长角色设定敏感度低,简洁指令效果更好", tgt.DisplayName),
		})
	}

	// 规则 6: avoider 检查(目标模型应避免的模式)
	for _, avoid := range tgt.AvoidPatterns {
		if strings.Contains(strings.ToLower(prompt), strings.ToLower(avoid)) {
			suggestions = append(suggestions, AdaptationSuggestion{
				Type: "remove", Severity: "should",
				Description: fmt.Sprintf("移除「%s」相关模式(%s 不兼容)", avoid, tgt.DisplayName),
				Before:  extractAround(prompt, avoid, 40),
				After:   "(移除该模式)",
				Reason:   fmt.Sprintf("%s 的已知最佳实践: 避免使用 %s", tgt.DisplayName, avoid),
			})
		}
	}

	// 规则 7: 添加目标最佳实践
	if len(tgt.BestPractices) > 0 && len(suggestions) == 0 {
		suggestions = append(suggestions, AdaptationSuggestion{
			Type: "add", Severity: "may",
			Description: fmt.Sprintf("添加 %s 的最佳实践提示", tgt.DisplayName),
			Before:  "(Prompt 开头)",
			After:   "建议在 Prompt 开头加入: " + strings.Join(tgt.BestPractices[:min(2, len(tgt.BestPractices))], "; "),
			Reason:   "基于公开文档和社区最佳实践",
		})
	}

	return suggestions
}

// applySuggestions 将适应建议应用到 Prompt。
func applySuggestions(prompt string, suggestions []AdaptationSuggestion) string {
	result := prompt

	for _, s := range suggestions {
		switch s.Type {
		case "replace":
			// 简单替换(启发式)
			result = applyReplace(result, s)
		case "add":
			if s.Severity == "should" || s.Severity == "must" {
				result = s.After + "\n\n" + result
			}
		case "remove":
			result = applyRemove(result, s)
		case "restructure":
			result = applyRestructure(result, s)
		}
	}

	return result
}

func applyReplace(prompt string, s AdaptationSuggestion) string {
	// 简单:替换 XML 标签风格
	if s.Before != "" && strings.Contains(prompt, s.Before) {
		result := strings.ReplaceAll(prompt, s.Before, s.After)
		return result
	}
	// 通用:替换 XML 标签为 Markdown
	result := strings.ReplaceAll(prompt, "<thinking>", "**思考:**\n")
	result = strings.ReplaceAll(result, "</thinking>", "")
	result = strings.ReplaceAll(result, "<answer>", "**回答:**\n")
	result = strings.ReplaceAll(result, "</answer>", "")
	result = strings.ReplaceAll(result, "<instruction>", "## 指令\n")
	result = strings.ReplaceAll(result, "</instruction>", "")
	result = strings.ReplaceAll(result, "<constraint>", "**约束:** ")
	result = strings.ReplaceAll(result, "</constraint>", "")
	return result
}

func applyRemove(prompt string, s AdaptationSuggestion) string {
	if s.Before != "" && strings.Contains(prompt, s.Before) {
		return strings.ReplaceAll(prompt, s.Before, s.After)
	}
	return prompt
}

func applyRestructure(prompt string, s AdaptationSuggestion) string {
	// 简化:移除长角色设定,保留任务部分
	lines := strings.Split(prompt, "\n")
	var cleaned []string
	inRole := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "你是") || strings.HasPrefix(trimmed, "You are") {
			inRole = true
			continue
		}
		if inRole && (strings.HasPrefix(trimmed, "任务") || strings.HasPrefix(trimmed, "##") || strings.HasPrefix(trimmed, "请")) {
			inRole = false
		}
		if !inRole {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func estimateConfidence(suggestions []AdaptationSuggestion) float64 {
	if len(suggestions) == 0 {
		return 1.0
	}
	// must 多的降低置信度
	mustCount := 0
	for _, s := range suggestions {
		if s.Severity == "must" {
			mustCount++
		}
	}
	confidence := 0.9 - float64(mustCount)*0.15
	if confidence < 0.3 {
		confidence = 0.3
	}
	return confidence
}

// ── 工具函数 ──

func styleLabel(style string) string {
	labels := map[string]string{
		"cot":        "思维链(CoT)",
		"minimal":    "极简",
		"xml":        "XML 标签",
		"structured": "结构化(Markdown)",
		"roleplay":   "角色扮演",
	}
	if l, ok := labels[style]; ok {
		return l
	}
	return style
}

func extractXMLSnippet(prompt string, n int) string {
	tags := []string{"<thinking>", "<answer>", "<role>", "<instruction>"}
	for _, tag := range tags {
		idx := strings.Index(strings.ToLower(prompt), tag)
		if idx >= 0 {
			end := idx + n
			if end > len(prompt) {
				end = len(prompt)
			}
			return prompt[idx:end]
		}
	}
	return prompt[:min(n, len(prompt))]
}

func extractBracketSnippet(prompt string, n int) string {
	idx := strings.Index(prompt, "【")
	if idx < 0 {
		return prompt[:min(n, len(prompt))]
	}
	end := idx + n
	if end > len(prompt) {
		end = len(prompt)
	}
	return prompt[idx:end]
}

func extractRoleSnippet(prompt string, n int) string {
	for _, kw := range []string{"你是", "You are", "你的角色"} {
		idx := strings.Index(prompt, kw)
		if idx >= 0 {
			end := idx + n
			if end > len(prompt) {
				end = len(prompt)
			}
			return prompt[idx:end]
		}
	}
	return prompt[:min(n, len(prompt))]
}

func extractAround(text, pattern string, n int) string {
	idx := strings.Index(strings.ToLower(text), strings.ToLower(pattern))
	if idx < 0 {
		return ""
	}
	start := idx - n/2
	if start < 0 {
		start = 0
	}
	end := idx + len(pattern) + n/2
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

func convertXMLToStyle(prompt string, tgt ModelProfile) string {
	return applyReplace(prompt, AdaptationSuggestion{Before: "<thinking>", After: "**思考:**"})
}

func wrapInXML(prompt string) string {
	// 简单的 XML 包裹
	lines := strings.Split(prompt, "\n")
	var sections []string
	var current []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "##") || strings.HasPrefix(trimmed, "###") {
			if len(current) > 0 {
				sections = append(sections, strings.Join(current, "\n"))
			}
			current = []string{line}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		sections = append(sections, strings.Join(current, "\n"))
	}

	var wrapped []string
	wrapped = append(wrapped, "<instruction>")
	for _, s := range sections {
		wrapped = append(wrapped, "  "+s)
	}
	wrapped = append(wrapped, "</instruction>")
	return strings.Join(wrapped, "\n")
}

func formatSuggestionsForLLM(suggestions []AdaptationSuggestion) string {
	var lines []string
	for i, s := range suggestions {
		lines = append(lines, fmt.Sprintf("%d. [%s] %s: %s → %s (原因: %s)",
			i+1, s.Severity, s.Description, s.Before, s.After, s.Reason))
	}
	return strings.Join(lines, "\n")
}

func containsAny(slice []string, target string) bool {
	for _, s := range slice {
		if strings.Contains(s, target) {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetAllModelProfiles 获取所有已知模型的偏好配置(供前端下拉框)。
func GetAllModelProfiles() []ModelProfile {
	return modelProfiles
}

// AdaptPromptJSON 序列化适配结果。
func AdaptPromptJSON(r *AdaptationResult) string {
	b, _ := json.Marshal(r)
	return string(b)
}
