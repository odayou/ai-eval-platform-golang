package engine

// metric.go — metric 类规则的内置校验库。
//
// metric 规则的 content 字段约定为校验器名称(如 "sql_injection_check")。
// 此处维护名称 → 校验函数的映射,每个校验函数返回 0-100 分。
//
// 当前内置校验器(覆盖种子数据里的 metric 规则):
//   - sql_injection_check / injection_check : SQL/命令注入检查
//   - secret_leak_check                    : 密钥泄露检查
//   - compilable_check / build_check       : 可编译性(语法基本检查)
//   - test_coverage_check                  : 测试覆盖(是否含测试代码)
//
// 未匹配的 metric 规则名走通用关键词匹配(fallback)。

import (
	"regexp"
	"strings"

	"ai-eval-platform/internal/store"
)

// MetricResult 是 metric 校验的结果。
type MetricResult struct {
	Score  float64
	Reason string
}

// RunMetric 按规则 content(校验器名)对回答执行 metric 校验。
func RunMetric(rule store.RuleRow, answer string) MetricResult {
	name := strings.ToLower(strings.TrimSpace(rule.Content))
	low := strings.ToLower(answer)

	switch {
	case strings.Contains(name, "sql") || strings.Contains(name, "injection") || strings.Contains(name, "注入"):
		return checkNoInjection(low)
	case strings.Contains(name, "secret") || strings.Contains(name, "leak") || strings.Contains(name, "密钥") || strings.Contains(name, "泄露"):
		return checkNoSecretLeak(low)
	case strings.Contains(name, "compil") || strings.Contains(name, "build") || strings.Contains(name, "编译"):
		return checkCompilable(answer)
	case strings.Contains(name, "test") || strings.Contains(name, "测试") || strings.Contains(name, "覆盖"):
		return checkTestCoverage(answer)
	default:
		return metricKeywordFallback(rule, low)
	}
}

// checkNoInjection 检查是否含字符串拼接 SQL(扣分项)。
func checkNoInjection(lowAns string) MetricResult {
	// 危险模式:fmt.Sprintf 包含 SQL 关键字 + 引号
	danger := regexp.MustCompile(`sprintf\s*\([^)]*(select|insert|update|delete|where)[^)]*%[svd]`)
	if danger.MatchString(lowAns) {
		return MetricResult{Score: 30, Reason: "检测到字符串拼接 SQL,存在注入风险"}
	}
		// 安全模式:用了参数化查询(?占位)
		if strings.Contains(lowAns, "?") && (strings.Contains(lowAns, "query") || strings.Contains(lowAns, "where ?") || strings.Contains(lowAns, "= ?")) {
		return MetricResult{Score: 95, Reason: "使用参数化查询(?占位),无注入风险"}
	}
	return MetricResult{Score: 80, Reason: "未检测到明显 SQL 拼接"}
}

// checkNoSecretLeak 检查是否泄露密钥(硬编码 key/token/password)。
func checkNoSecretLeak(lowAns string) MetricResult {
	// 危险:疑似硬编码密钥(sk-xxx 开头,或 api_key="xxx")
	hardcoded := regexp.MustCompile(`(sk-[a-z0-9]{10,}|api[_-]?key\s*[=:]\s*["'][^"']{8,}|password\s*[=:]\s*["'][^"']+)`)
	if hardcoded.MatchString(lowAns) {
		return MetricResult{Score: 20, Reason: "检测到硬编码密钥/密码,存在泄露风险"}
	}
	return MetricResult{Score: 95, Reason: "未检测到硬编码密钥"}
}

// checkCompilable 基础语法检查(括号配平、无残留语法错误特征)。
func checkCompilable(answer string) MetricResult {
	openParens := strings.Count(answer, "(")
	closeParens := strings.Count(answer, ")")
	openBraces := strings.Count(answer, "{")
	closeBraces := strings.Count(answer, "}")

	// 括号严重不配平 → 扣分
	if abs(openParens-closeParens) > 3 || abs(openBraces-closeBraces) > 3 {
		return MetricResult{Score: 35, Reason: "括号严重不配平,可能无法编译"}
	}
	// 有代码结构(func/func/return 等)
	if regexp.MustCompile(`(?m)^\s*(func|def|return|if|for)\b`).MatchString(answer) {
		return MetricResult{Score: 85, Reason: "代码结构完整,括号基本配平"}
	}
	return MetricResult{Score: 70, Reason: "无明显语法错误特征"}
}

// checkTestCoverage 检查是否含测试代码(Test 函数 / assert)。
func checkTestCoverage(answer string) MetricResult {
	hasTestFunc := regexp.MustCompile(`(?i)(func\s+Test\w+|def\s+test_|describe\s*\(|it\s*\()`).MatchString(answer)
	hasAssert := strings.Contains(answer, "assert") || strings.Contains(answer, "expect") ||
		strings.Contains(answer, "t.Error") || strings.Contains(answer, "if got !=")
	if hasTestFunc && hasAssert {
		return MetricResult{Score: 90, Reason: "含完整测试函数与断言"}
	}
	if hasTestFunc || hasAssert {
		return MetricResult{Score: 70, Reason: "含部分测试代码,但断言不完整"}
	}
	return MetricResult{Score: 40, Reason: "未提供测试代码"}
}

// metricKeywordFallback 通用关键词匹配:规则名在回答里出现 → 高分。
func metricKeywordFallback(rule store.RuleRow, lowAns string) MetricResult {
	keywords := strings.FieldsFunc(strings.ToLower(rule.Name+" "+rule.Content), func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == '/'
	})
	hits := 0
	for _, kw := range keywords {
		if len(kw) >= 3 && strings.Contains(lowAns, kw) {
			hits++
		}
	}
	if hits >= 2 {
		return MetricResult{Score: 82, Reason: "回答命中多个规则关键词"}
	}
	if hits == 1 {
		return MetricResult{Score: 70, Reason: "回答命中 1 个规则关键词"}
	}
	return MetricResult{Score: 60, Reason: "通用匹配:无关键词命中,给基础分"}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
