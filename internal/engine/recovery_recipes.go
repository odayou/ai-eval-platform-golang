package engine

// recovery_recipes.go — 组件失败自动恢复配方(skill #13)。
//
// 7 类常见失败场景 + recipe 配方表 + "一次重试后升级"策略 + 三态结果。
// 设计参考 claw-code src/recovery.py。
//
// 当 LLM 调用/工具执行/沙箱编译失败时,不再直接报错,
// 而是查 recipe 表尝试自动恢复。

import (
	"strings"
	"time"
)

// RecoveryResult 三态恢复结果。
type RecoveryResult struct {
	Status   RecoveryStatus // Recovered / Partial / Escalation
	Action   string         // 执行的恢复动作描述
	Retries  int            // 重试次数
	Error    string         // 原始错误(Escalation 时有值)
}

// RecoveryStatus 三态。
type RecoveryStatus string

const (
	StatusRecovered  RecoveryStatus = "recovered"   // 完全恢复
	StatusPartial    RecoveryStatus = "partial"     // 部分恢复,可继续
	StatusEscalation RecoveryStatus = "escalation"  // 无法恢复,升级报错
)

// RecoveryRecipe 恢复配方。
type RecoveryRecipe struct {
	MatchPattern  string                                 // 错误匹配模式(子串匹配)
	Category      string                                 // 失败分类
	Description   string                                 // 描述
	Recover       func(errMsg string) (bool, string)     // 恢复函数:返回(是否恢复, 动作描述)
}

// 内置恢复配方表。
var recoveryRecipes = []RecoveryRecipe{
	{
		MatchPattern: "rate limit", Category: "rate_limit",
		Description: "API 限流,等待后重试",
		Recover: func(errMsg string) (bool, string) {
			time.Sleep(2 * time.Second)
			return true, "等待 2 秒后重试(限流恢复)"
		},
	},
	{
		MatchPattern: "timeout", Category: "timeout",
		Description: "请求超时,重试一次",
		Recover: func(errMsg string) (bool, string) {
			return true, "超时重试(增加超时时间)"
		},
	},
	{
		MatchPattern: "connection refused", Category: "connection",
		Description: "连接被拒绝,等待后重试",
		Recover: func(errMsg string) (bool, string) {
			time.Sleep(1 * time.Second)
			return true, "等待 1 秒后重试(连接恢复)"
		},
	},
	{
		MatchPattern: "context deadline exceeded", Category: "timeout",
		Description: "上下文超时",
		Recover: func(errMsg string) (bool, string) {
			return true, "上下文超时重试"
		},
	},
	{
		MatchPattern: "EOF", Category: "connection",
		Description: "连接意外断开",
		Recover: func(errMsg string) (bool, string) {
			time.Sleep(500 * time.Millisecond)
			return true, "重连重试(EOF 恢复)"
		},
	},
	{
		MatchPattern: "unexpected end of JSON", Category: "parse",
		Description: "JSON 解析失败,可能空响应",
		Recover: func(errMsg string) (bool, string) {
			return false, "JSON 解析失败(非瞬时,不重试)"
		},
	},
	{
		MatchPattern: "401", Category: "auth",
		Description: "认证失败",
		Recover: func(errMsg string) (bool, string) {
			return false, "认证失败(需检查 API Key,不自动重试)"
		},
	},
}

// AttemptRecovery 尝试根据错误信息自动恢复。
// retryFn 是恢复后执行的重试函数(如重新调 LLM)。
// 最多重试 1 次(一次重试后升级)。
func AttemptRecovery(errMsg string, retryFn func() error) RecoveryResult {
	errLower := strings.ToLower(errMsg)

	// 查配方表
	for _, recipe := range recoveryRecipes {
		if strings.Contains(errLower, recipe.MatchPattern) {
			recovered, action := recipe.Recover(errMsg)
			if !recovered {
				return RecoveryResult{
					Status: StatusEscalation,
					Action: action,
					Error:  errMsg,
				}
			}

			// 重试一次
			if retryFn != nil {
				if err := retryFn(); err == nil {
					return RecoveryResult{
						Status: StatusRecovered,
						Action: action,
						Retries: 1,
					}
				}
				// 重试仍失败 → 升级
				return RecoveryResult{
					Status: StatusEscalation,
					Action: action + "(重试后仍失败)",
					Retries: 1,
					Error:   errMsg,
				}
			}

			// 无重试函数,返回 partial(需要调用方自己重试)
			return RecoveryResult{
				Status: StatusPartial,
				Action: action,
			}
		}
	}

	// 无匹配配方 → 直接升级
	return RecoveryResult{
		Status: StatusEscalation,
		Error:  errMsg,
	}
}

// LogRecovery 记录恢复结果到日志。
func LogRecovery(taskID string, result RecoveryResult) {
	NewSlog("recovery").Info("status= retries= action=", "taskID", taskID, "Status", result.Status, "Retries", result.Retries)
}
