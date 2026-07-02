package engine

// breaker.go — 钱包保护断路器(MRD 7.5 / 4.8)。
//
// 问题(MRD 原话):
//   团队自己写的本地 Agent 处于开发期,稳定性极差,可能陷入"无限内耗"的死循环
//   (疯狂调用同一个 Tool,或在 Prompt 幻觉里疯狂发送请求)。
//   既然花的是用户自己的 API 额度,平台不管的话,用户跑一次可能收到几百美金的账单。
//
// 解决:
//   - 单次评测最大 Token 消耗上限
//   - 最大 Tool Calling 轮数上限
//   - 超限自动熔断(强制中止) + 提示
//   - 提测前 Token 预估 + 费用估算
//
// 实现:在 engine.Run 的循环中检查 breaker,超限立即中止。

import (
	"fmt"
)

// BreakerConfig 断路器配置。
type BreakerConfig struct {
	Enabled      bool    `json:"enabled"`       // 是否启用断路器
	MaxTokens    int     `json:"max_tokens"`    // 单次评测最大 token 消耗(0=不限)
	MaxTurns     int     `json:"max_turns"`     // 最大 Tool Calling 轮数(0=不限)
	MaxCostUSD   float64 `json:"max_cost_usd"`  // 最大费用美元(0=不限)
	CostPer1K    float64 `json:"cost_per_1k"`   // 每 1K token 费用(用于估算)
}

// BreakerState 断路器运行时状态。
type BreakerState struct {
	Config       BreakerConfig
	TotalTokens  int     `json:"total_tokens"`  // 已消耗 token
	TotalTurns   int     `json:"total_turns"`   // 已执行轮数
	EstCostUSD   float64 `json:"est_cost_usd"`  // 估算费用
	Tripped      bool    `json:"tripped"`       // 是否已熔断
	TripReason   string  `json:"trip_reason"`   // 熔断原因
}

// DefaultBreakerConfig 返回默认的断路器配置。
func DefaultBreakerConfig() BreakerConfig {
	return BreakerConfig{
		Enabled:    true,
		MaxTokens:  100000,  // 10 万 token
		MaxTurns:   30,      // 30 轮
		MaxCostUSD: 5.0,     // 5 美元
		CostPer1K:  0.002,   // GLM-4-flash 价格
	}
}

// NewBreakerState 创建一个新的断路器状态。
func NewBreakerState(config BreakerConfig) *BreakerState {
	return &BreakerState{
		Config: config,
	}
}

// Check 检查是否应该熔断。每次 Agent 调用后调用。
func (bs *BreakerState) Check(tokenIn, tokenOut int) error {
	if !bs.Config.Enabled {
		return nil
	}

	bs.TotalTokens += tokenIn + tokenOut
	bs.TotalTurns++
	bs.EstCostUSD = float64(bs.TotalTokens) / 1000.0 * bs.Config.CostPer1K

	// 检查 token 上限
	if bs.Config.MaxTokens > 0 && bs.TotalTokens > bs.Config.MaxTokens {
		bs.Tripped = true
		bs.TripReason = fmt.Sprintf("Token 消耗超限: %d > %d", bs.TotalTokens, bs.Config.MaxTokens)
		return fmt.Errorf("breaker tripped: %s", bs.TripReason)
	}

	// 检查轮数上限
	if bs.Config.MaxTurns > 0 && bs.TotalTurns > bs.Config.MaxTurns {
		bs.Tripped = true
		bs.TripReason = fmt.Sprintf("Tool Calling 轮数超限: %d > %d", bs.TotalTurns, bs.Config.MaxTurns)
		return fmt.Errorf("breaker tripped: %s", bs.TripReason)
	}

	// 检查费用上限
	if bs.Config.MaxCostUSD > 0 && bs.EstCostUSD > bs.Config.MaxCostUSD {
		bs.Tripped = true
		bs.TripReason = fmt.Sprintf("费用超限: $%.2f > $%.2f", bs.EstCostUSD, bs.Config.MaxCostUSD)
		return fmt.Errorf("breaker tripped: %s", bs.TripReason)
	}

	return nil
}

// EstimateTokens 预估一次评测的 token 消耗。
// 基于题目数 × 每题平均 token。
func EstimateTokens(questionCount, rulesPerQuestion int) int {
	// 每题:prompt(~200) + agent回答(~500) + 每条规则打分(~300) + 圆桌(~400)
	perQuestion := 200 + 500 + rulesPerQuestion*300 + 400
	return questionCount * perQuestion
}

// EstimateCost 预估一次评测的费用。
func EstimateCost(questionCount, rulesPerQuestion int, config BreakerConfig) float64 {
	tokens := EstimateTokens(questionCount, rulesPerQuestion)
	return float64(tokens) / 1000.0 * config.CostPer1K
}

// FormatBreakerStatus 格式化断路器状态(供前端展示)。
func FormatBreakerStatus(bs *BreakerState) string {
	if bs.Tripped {
		return fmt.Sprintf("🔴 已熔断: %s (Token:%d, 轮数:%d, 费用:$%.2f)",
			bs.TripReason, bs.TotalTokens, bs.TotalTurns, bs.EstCostUSD)
	}
	return fmt.Sprintf("🟢 正常 (Token:%d, 轮数:%d, 费用:$%.2f)",
		bs.TotalTokens, bs.TotalTurns, bs.EstCostUSD)
}

// LogBreakerStatus 记录断路器状态到日志。
func LogBreakerStatus(bs *BreakerState, taskID string) {
	status := FormatBreakerStatus(bs)
	NewSlog("breaker").With("task_id", taskID).Info("断路器状态", "status", status)
}
