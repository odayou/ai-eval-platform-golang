package engine

// code_review.go — 结构化代码评审 Rubric(skill #27)。
//
// 区别于 judge.go(按评测规则给回答打分),这个模块做的是:
// 让 LLM 按专业代码评审 rubric 做结构化评审,输出 P0-P3 findings JSON。
//
// 设计参考: codex prompts/templates/review/rubric.md
// findings 数组(title/body/confidence/priority/code_location)
// + overall_correctness(correct/incorrect/partially_correct)
// + overall_confidence_score

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ── 数据结构 ──

// ReviewFinding 单条评审发现。
type ReviewFinding struct {
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	Confidence   float64 `json:"confidence_score"`
	Priority     string  `json:"priority"` // P0|P1|P2|P3
	CodeLocation string  `json:"code_location"`
}

// ReviewResult 评审结果。
type ReviewResult struct {
	Findings            []ReviewFinding `json:"findings"`
	OverallCorrectness  string          `json:"overall_correctness"`  // correct|incorrect|partially_correct
	OverallConfidence   float64         `json:"overall_confidence_score"`
	IsMock              bool            `json:"is_mock"`
}

// ── 评审 Rubric Prompt ──

const reviewRubricPrompt = `你是代码审查专家。请按以下 rubric 严格评审代码:

## 评审维度
1. **正确性**: 逻辑是否正确? 边界条件处理了吗? 空值检查?
2. **安全性**: 有注入/越权/数据泄漏风险吗? 密钥硬编码?
3. **可维护性**: 代码清晰吗? 有重复吗? 命名合理吗?
4. **性能**: 有明显性能问题吗? N+1 查询? 不必要的循环?

## 优先级
- P0: 安全漏洞/数据丢失(必须立即修)
- P1: 正确性 bug(会导致错误行为)
- P2: 改进建议(应该修但不紧急)
- P3: 风格/命名(锦上添花)

## 输出格式
严格输出以下 JSON(不要 markdown 围栏,不要其他文字):
{
  "findings": [
    {"title":"问题标题","body":"详细描述+修复建议","confidence_score":0.9,"priority":"P1","code_location":"文件名:行号"}
  ],
  "overall_correctness":"correct|incorrect|partially_correct",
  "overall_confidence_score":0.85
}

注意: 如果代码质量很好没有问题,findings 返回空数组 []。`

// ── 评审执行 ──

// RunCodeReview 对一段代码做结构化代码评审。
// 用考官模型(main 角色)执行,输出 P0-P3 findings JSON。
// language 是代码语言(go/python/javascript/sql),用于在 prompt 里标注。
func RunCodeReview(code, language, context string) ReviewResult {
	// 查考官模型端点
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("main")

	if baseURL == "" || apiKey == "" {
		NewSlog("code_review").Info("无考官模型,使用 mock 评审")
		return mockCodeReview(code, language)
	}

	prompt := fmt.Sprintf("%s\n\n## 待评审代码 (%s)\n\n上下文: %s\n\n```\n%s\n```",
		reviewRubricPrompt, language, context, code)

	SetAuditCaller("code_review")
	start := time.Now()
	result, err := callProviderWithRetryAndSystem(baseURL, apiKey, apiFormat, realName, prompt, "")
	if err != nil {
		NewSlog("code_review").Error("LLM 调用失败(),回退 mock", "err", err)
		return mockCodeReview(code, language)
	}
	NewSlog("code_review").Info("评审完成,ms", "Milliseconds", time.Since(start).Milliseconds())

	return parseReviewOutput(result.Text)
}

// parseReviewOutput 解析 LLM 评审输出为结构化结果。
// 容错:去 markdown 围栏 + 从文本中提取 JSON。
func parseReviewOutput(text string) ReviewResult {
	text = strings.TrimSpace(text)
	// 去 markdown 围栏
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	// 提取第一个完整 JSON 对象
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < 0 || end <= start {
		return ReviewResult{
			Findings:           []ReviewFinding{},
			OverallCorrectness: "partially_correct",
			OverallConfidence:  0.0,
			IsMock:             true,
		}
	}

	jsonStr := text[start : end+1]
	var raw struct {
		Findings []struct {
			Title        string  `json:"title"`
			Body         string  `json:"body"`
			Confidence   float64 `json:"confidence_score"`
			Priority     string  `json:"priority"`
			CodeLocation string  `json:"code_location"`
		} `json:"findings"`
		OverallCorrectness string  `json:"overall_correctness"`
		OverallConfidence  float64 `json:"overall_confidence_score"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return ReviewResult{
			Findings:           []ReviewFinding{},
			OverallCorrectness: "partially_correct",
			OverallConfidence:  0.0,
			IsMock:             true,
		}
	}

	findings := make([]ReviewFinding, 0, len(raw.Findings))
	for _, f := range raw.Findings {
		// 校验优先级,非法值降级为 P2
		priority := f.Priority
		if !regexp.MustCompile(`^P[0-3]$`).MatchString(priority) {
			priority = "P2"
		}
		findings = append(findings, ReviewFinding{
			Title:        f.Title,
			Body:         f.Body,
			Confidence:   f.Confidence,
			Priority:     priority,
			CodeLocation: f.CodeLocation,
		})
	}

	// 按 P0→P3 排序
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Priority < findings[j].Priority
	})

	correctness := raw.OverallCorrectness
	if correctness != "correct" && correctness != "incorrect" && correctness != "partially_correct" {
		correctness = "partially_correct"
	}

	return ReviewResult{
		Findings:           findings,
		OverallCorrectness: correctness,
		OverallConfidence:  raw.OverallConfidence,
	}
}

// Summary 生成评审摘要(供报告展示)。
func (r ReviewResult) Summary() string {
	p0 := 0
	p1 := 0
	for _, f := range r.Findings {
		switch f.Priority {
		case "P0":
			p0++
		case "P1":
			p1++
		}
	}
	other := len(r.Findings) - p0 - p1
	return fmt.Sprintf("%s (置信度 %.0f%%) | %d P0 + %d P1 + %d 其他",
		r.OverallCorrectness, r.OverallConfidence*100, p0, p1, other)
}

// ── Mock 评审(无 Key 时) ──

func mockCodeReview(code, language string) ReviewResult {
	var findings []ReviewFinding
	low := strings.ToLower(code)

	// 启发式检查
	if strings.Contains(low, "sprintf") && (strings.Contains(low, "select") || strings.Contains(low, "insert") || strings.Contains(low, "where")) {
		findings = append(findings, ReviewFinding{
			Title: "SQL 注入风险", Body: "检测到 sprintf 拼接 SQL,应改用参数化查询(?)",
			Confidence: 0.8, Priority: "P0", CodeLocation: "",
		})
	}
	if regexp.MustCompile(`(?i)(api[_-]?key|password|secret|token)\s*[:=]\s*["'][^"']{8,}`).MatchString(code) {
		findings = append(findings, ReviewFinding{
			Title: "硬编码密钥", Body: "检测到疑似硬编码的密钥/密码,应从环境变量读取",
			Confidence: 0.7, Priority: "P0", CodeLocation: "",
		})
	}
	if strings.Contains(low, "panic(") || strings.Contains(low, "os.exit(") {
		findings = append(findings, ReviewFinding{
			Title: "不安全的退出方式", Body: "检测到 panic/os.Exit,应优雅返回错误",
			Confidence: 0.6, Priority: "P1", CodeLocation: "",
		})
	}

	correctness := "correct"
	if len(findings) > 0 {
		correctness = "partially_correct"
		for _, f := range findings {
			if f.Priority == "P0" {
				correctness = "incorrect"
				break
			}
		}
	}

	return ReviewResult{
		Findings:           findings,
		OverallCorrectness: correctness,
		OverallConfidence:  0.5,
		IsMock:             true,
	}
}
