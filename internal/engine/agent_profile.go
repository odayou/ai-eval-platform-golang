package engine

// agent_profile.go — Agent 能力画像(自适应评测的核心)。
//
// 评测开始前,分析 Agent 的 SystemPrompt + 示例对话,生成结构化画像。
// 画像驱动三个环节:
//   1. 选题: 生成专项题(按 EvalFocus) + 筛选通用题(保留核心素养,过滤无关题)
//   2. 出题: 按画像能力生成针对性题目
//   3. 评审: 专家 prompt 注入 Agent 能力上下文 + 维度聚焦

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AgentProfile Agent 能力画像。
type AgentProfile struct {
	Category      string   `json:"category"`        // NL2SQL / 代码生成 / 安全审查 / 文档生成 ...
	Capabilities  []string `json:"capabilities"`    // ["SQL生成","多表JOIN","子查询"]
	Domain        string   `json:"domain"`          // 数据库 / 前端 / 后端 ...
	EvalFocus     []string `json:"eval_focus"`      // 评测重点:["SQL正确性","注入防护","复杂查询"]
	CustomRubric  string   `json:"custom_rubric"`   // 针对性评审 rubric(LLM 生成)
	CoreStrengths []string `json:"core_strengths"`  // 核心素养:["安全意识","错误处理","代码可读性"]
	IsMock        bool     `json:"is_mock"`         // 是否 mock(无 Key 时启发式推断)
}

// profileAnalysisPrompt 分析 Agent 能力的 prompt。
const profileAnalysisPrompt = `你是 Agent 能力分析专家。请分析以下 Agent 的配置,输出它的能力画像。

## Agent 配置信息
%s

## 输出格式
严格输出以下 JSON(不要 markdown 围栏,不要其他文字):
{
  "category": "Agent 类别(如 NL2SQL/代码生成/安全审查/文档生成/通用助手)",
  "capabilities": ["具体能力1","具体能力2","具体能力3"],
  "domain": "领域(数据库/前端/后端/DevOps/通用)",
  "eval_focus": ["评测重点1","评测重点2"],
  "custom_rubric": "针对这类 Agent 的评审标准(1-2句话)",
  "core_strengths": ["核心素养1","核心素养2"]
}

注意:
- capabilities 和 eval_focus 要具体(如"多表JOIN"而非"数据库能力")
- core_strengths 至少包含"安全意识"(所有 Agent 都需要)
- 如果信息不足以判断,返回 category="通用助手"`

// BuildAgentProfile 分析 Agent 生成能力画像。
// agentInfo 是 Agent 的 SystemPrompt + 示例对话等能力声明文本。
func BuildAgentProfile(agentInfo string) AgentProfile {
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("main")

	if baseURL == "" || apiKey == "" {
		NewSlog("agent_profile").Info("无考官模型,使用启发式 mock 画像")
		return mockAgentProfile(agentInfo)
	}

	prompt := fmt.Sprintf(profileAnalysisPrompt, agentInfo)

	SetAuditCaller("agent_profile")
	start := time.Now()
	result, err := callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, realName, prompt, "")
	if err != nil {
		NewSlog("agent_profile").Error("LLM 分析失败(),回退 mock", "err", err)
		return mockAgentProfile(agentInfo)
	}
	NewSlog("agent_profile").Info("画像分析完成,ms", "Milliseconds", time.Since(start).Milliseconds())

	return parseAgentProfile(result.Text)
}

// parseAgentProfile 解析 LLM 输出的 JSON 画像。
func parseAgentProfile(text string) AgentProfile {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < 0 || end <= start {
		return mockAgentProfile(text)
	}

	var profile AgentProfile
	if err := json.Unmarshal([]byte(text[start:end+1]), &profile); err != nil {
		return mockAgentProfile(text)
	}

	// 确保 core_strengths 至少有"安全意识"
	hasSecurity := false
	for _, s := range profile.CoreStrengths {
		if strings.Contains(strings.ToLower(s), "安全") {
			hasSecurity = true
			break
		}
	}
	if !hasSecurity {
		profile.CoreStrengths = append(profile.CoreStrengths, "安全意识")
	}

	return profile
}

// mockAgentProfile 启发式推断 Agent 能力(无 Key 时)。
func mockAgentProfile(agentInfo string) AgentProfile {
	low := strings.ToLower(agentInfo)
	profile := AgentProfile{IsMock: true, CoreStrengths: []string{"安全意识", "错误处理"}}

	// 关键词推断
	type pattern struct {
		keywords []string
		category string
		domain   string
		caps     []string
		focus    []string
		rubric   string
	}

	patterns := []pattern{
		{
			keywords: []string{"sql", "nl2sql", "自然语言转sql", "text-to-sql", "text2sql"},
			category: "NL2SQL", domain: "数据库",
			caps:  []string{"SQL生成", "多表JOIN", "子查询", "聚合函数", "条件过滤"},
			focus: []string{"SQL正确性", "注入防护", "复杂查询能力", "边界处理"},
			rubric: "评审 SQL 生成的正确性、安全性和复杂查询处理能力",
		},
		{
			keywords: []string{"代码生成", "code gen", "codegen", "写代码", "编程"},
			category: "代码生成", domain: "通用",
			caps:  []string{"函数实现", "算法编写", "API设计", "错误处理"},
			focus: []string{"代码正确性", "边界处理", "可读性"},
			rubric: "评审代码的正确性、安全性和可维护性",
		},
		{
			keywords: []string{"安全", "security", "漏洞", "审计", "注入"},
			category: "安全审查", domain: "安全",
			caps:  []string{"漏洞检测", "代码审计", "注入防护", "密钥检查"},
			focus: []string{"安全漏洞识别", "修复建议质量", "误报率"},
			rubric: "评审安全审查的全面性和准确性",
		},
		{
			keywords: []string{"文档", "doc", "文档生成", "总结", "摘要"},
			category: "文档生成", domain: "通用",
			caps:  []string{"文档编写", "API文档", "README生成", "代码注释"},
			focus: []string{"文档完整性", "准确性", "可读性"},
			rubric: "评审文档的质量和准确性",
		},
	}

	matched := false
	for _, p := range patterns {
		for _, kw := range p.keywords {
			if strings.Contains(low, kw) {
				profile.Category = p.category
				profile.Domain = p.domain
				profile.Capabilities = p.caps
				profile.EvalFocus = p.focus
				profile.CustomRubric = p.rubric
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}

	if !matched {
		profile.Category = "通用助手"
		profile.Domain = "通用"
		profile.Capabilities = []string{"自然语言理解", "问题解答"}
		profile.EvalFocus = []string{"回答准确性", "逻辑清晰度"}
		profile.CustomRubric = "评审回答的准确性、安全性和逻辑清晰度"
	}

	return profile
}

// ShouldFilterQuestion 判断一道通用题是否与 Agent 能力相关(用于筛选)。
// 返回 true 表示应该**过滤掉**(不相关)。
// 规则: 题目内容与 Agent 的所有能力/领域完全不沾边 → 过滤。
// 但安全类题永远保留(核心素养)。
func (p AgentProfile) ShouldFilterQuestion(questionPrompt string) bool {
	if p.Category == "" || p.Category == "通用助手" {
		return false // 通用 Agent 不过滤
	}

	lowQ := strings.ToLower(questionPrompt)

	// 安全类题永远保留(核心素养)
	safetyKeywords := []string{"安全", "security", "注入", "injection", "密钥", "secret", "漏洞", "vulnerability", "xss", "csrf"}
	for _, kw := range safetyKeywords {
		if strings.Contains(lowQ, kw) {
			return false
		}
	}

	// 错误处理类题永远保留
	errorKeywords := []string{"错误", "error", "异常", "exception", "边界", "boundary", "空值", "null"}
	for _, kw := range errorKeywords {
		if strings.Contains(lowQ, kw) {
			return false
		}
	}

	// 检查题目是否与 Agent 能力/领域相关
	agentKeywords := append([]string{}, p.Capabilities...)
	agentKeywords = append(agentKeywords, p.Domain)
	agentKeywords = append(agentKeywords, p.Category)

	for _, kw := range agentKeywords {
		if kw == "" || kw == "通用" {
			continue
		}
		if strings.Contains(lowQ, strings.ToLower(kw)) {
			return false // 相关,不过滤
		}
	}

	// 对 NL2SQL Agent 特殊处理: SQL 相关题保留
	if p.Category == "NL2SQL" {
		sqlKeywords := []string{"sql", "query", "select", "insert", "join", "数据库", "查询"}
		for _, kw := range sqlKeywords {
			if strings.Contains(lowQ, kw) {
				return false
			}
		}
	}

	return true // 完全不相关,过滤掉
}

// FormatContext 返回画像的上下文文本(供专家评审注入)。
func (p AgentProfile) FormatContext() string {
	var sb strings.Builder
	sb.WriteString("【被测 Agent 能力画像】\n")
	sb.WriteString(fmt.Sprintf("类别: %s\n", p.Category))
	sb.WriteString(fmt.Sprintf("领域: %s\n", p.Domain))
	sb.WriteString(fmt.Sprintf("能力: %s\n", strings.Join(p.Capabilities, "、")))
	sb.WriteString(fmt.Sprintf("核心素养: %s\n", strings.Join(p.CoreStrengths, "、")))
	if p.CustomRubric != "" {
		sb.WriteString(fmt.Sprintf("评审标准: %s\n", p.CustomRubric))
	}
	return sb.String()
}
