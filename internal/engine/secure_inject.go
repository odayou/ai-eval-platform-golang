package engine

// secure_inject.go — 用户自带 Key 安全注入(MRD v2 7.4)。
//
// MRD 原话:
//   "用户自带的 API Key 不做明文落库,评测时通过环境变量安全注入到 Docker 沙箱中。
//    平台在落盘前自动扫描日志/会话记录,对 API Key 做换密处理,
//    确保评测记录可复现但不泄密。"
//
// 实现:
//   1. Key 脱敏 — 任何地方需要打印/存储 Key 时,先过 RedactKey 脱敏
//   2. 会话脱敏 — InsertConversation 前扫描 messages 内容,替换 Key
//   3. 日志脱敏 — 引擎日志中的 Key 自动打码
//   4. redaction_count — 每次落盘时记录被剥离的密钥条数(schema_agent.go sessions 表)
//
// Key 识别模式:智谱(xxxxxxxx.yyyyyyyyyy)、OpenAI(sk-xxx)、自定 Key(长度>20 的连续字母数字)

import (
	"regexp"
	"strings"
)

// RedactKey 对 Key 进行脱敏处理。
// 智谱格式:保留前 4 + 后 4,中间用 *** 替代
// OpenAI 格式:保留 sk- + 后 4
// 通用:保留首 3 + 尾 3
func RedactKey(key string) string {
	if key == "" {
		return "(未配置)"
	}
	if len(key) <= 6 {
		return "***"
	}
	// 智谱格式: xxxxxxxx.yyyyyyyyyyyy
	if strings.Contains(key, ".") {
		parts := strings.SplitN(key, ".", 2)
		if len(parts[0]) > 4 {
			return parts[0][:4] + "***." + safeTail(parts[1], 4)
		}
		return "***." + safeTail(parts[1], 4)
	}
	// OpenAI 格式: sk-xxx
	if strings.HasPrefix(strings.ToLower(key), "sk-") {
		return key[:5] + "***" + safeTail(key[7:], 4)
	}
	// 通用
	return key[:3] + "***" + safeTail(key[3:], 3)
}

func safeTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// RedactText 扫描文本中的 Key 并脱敏。
// 用于落盘前的会话记录脱敏。
// 返回脱敏后的文本 + 被剥离的密钥条数。
func RedactText(text string) (string, int) {
	count := 0
	result := text

	// 智谱 Key 模式: 8位hex.16位hex
	zhipuRe := regexp.MustCompile(`[a-fA-F0-9]{8}\.[a-fA-F0-9]{16,32}`)
	result = zhipuRe.ReplaceAllStringFunc(result, func(s string) string {
		count++
		return RedactKey(s)
	})

	// OpenAI Key 模式: sk- + 字母数字 ≥20位
	openaiRe := regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`)
	result = openaiRe.ReplaceAllStringFunc(result, func(s string) string {
		count++
		return RedactKey(s)
	})

	// 通用: api_key= / apikey= / key= 后面跟长串
	keyRe := regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token)\s*[=:]\s*['\"]?([a-zA-Z0-9_\-]{16,})['\"]?`)
	result = keyRe.ReplaceAllStringFunc(result, func(s string) string {
		count++
		return regexp.MustCompile(`(?i)([^=:]+)\s*[=:]\s*['\"]?[a-zA-Z0-9_\-]{16,}['\"]?`).ReplaceAllString(s, "${1}=***REDACTED***")
	})

	return result, count
}

// IsKeyPresent 快速检查文本是否可能含 Key。
func IsKeyPresent(text string) bool {
	rapidCheck := regexp.MustCompile(`(sk-[a-zA-Z0-9]{10,}|[a-fA-F0-9]{8}\.[a-fA-F0-9]{10,}|api[_-]?key\s*[=:])`)
	return rapidCheck.MatchString(text)
}

// MaskKeyForLog 为日志输出的 Key 脱敏(只显示少量信息)。
func MaskKeyForLog(key string) string {
	if key == "" {
		return "(none)"
	}
	return RedactKey(key)
}
