package engine

// stats_cache.go — 8 维度模型用量统计 + 双路 token 追踪(good-skills #40 + #52)。
//
// 补齐专家评审发现的两项差距:
//   #52 双路 token:condenser(上下文压缩器)的 LLM 调用 token 被丢弃,不独立计费
//   #40 stats-cache:仅 2 维 token(prompt/completion),缺 cacheRead/cacheWrite/cost 等 6 维
//
// 研究依据:
//   - Claude Code stats-cache.json:8 维度(cacheRead 单列证明 caching 是一等公民)
//   - OpenHands conversation-state-event.ts:agent + condenser 双路 metrics 分列

import (
	"fmt"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// TokenUsage 四维 token 用量(扩展自原有的 TokenIn/TokenOut 两维)。
type TokenUsage struct {
	PromptTokens        int `json:"prompt_tokens"`          // input
	CompletionTokens    int `json:"completion_tokens"`       // output
	CacheReadTokens     int `json:"cache_read_tokens"`       // prompt cache 读取(一等公民)
	CacheCreationTokens int `json:"cache_creation_tokens"`   // prompt cache 写入
	ReasoningTokens     int `json:"reasoning_tokens"`        // 推理 token(o1/o3 专有)
}

// Add 累加另一份 usage(返回新对象,不可变)。
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	return TokenUsage{
		PromptTokens:        u.PromptTokens + other.PromptTokens,
		CompletionTokens:    u.CompletionTokens + other.CompletionTokens,
		CacheReadTokens:     u.CacheReadTokens + other.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
		ReasoningTokens:     u.ReasoningTokens + other.ReasoningTokens,
	}
}

// Total 总 token(四维之和)。
func (u TokenUsage) Total() int {
	return u.PromptTokens + u.CompletionTokens + u.CacheReadTokens +
		u.CacheCreationTokens + u.ReasoningTokens
}

// CacheHitRate cache 命中率 = cacheRead / (cacheRead + prompt)。
func (u TokenUsage) CacheHitRate() float64 {
	totalRead := u.CacheReadTokens + u.PromptTokens
	if totalRead == 0 {
		return 0
	}
	return float64(u.CacheReadTokens) / float64(totalRead)
}

// ── 双路 Metrics(agent + condenser 各一套)──

// LLMMetrics 单路 LLM 累计指标(agent 或 condenser)。
type LLMMetrics struct {
	Usage          TokenUsage   `json:"usage"`            // 累计四维 token
	CostUSD        float64      `json:"cost_usd"`          // 累计成本(美元)
	Turns          int          `json:"turns"`             // 轮数
	Latencies      []float64    `json:"latencies"`         // 每轮延迟(ms)
}

// DualTokenTracker 双路 token 追踪器(agent + condenser 分列)。
//
// #52 核心修复:condenser 压缩对话用的 LLM 调用 token 不再被丢弃,
// 独立计入 condenser_metrics,支持精确成本归因。
type DualTokenTracker struct {
	mu              sync.Mutex
	AgentMetrics    LLMMetrics `json:"agent_metrics"`
	CondenserMetrics LLMMetrics `json:"condenser_metrics"`
}

// globalDualTracker 全局双路追踪器(单任务内)。
var globalDualTracker = &DualTokenTracker{}

// GetDualTokenTracker 获取全局追踪器。
func GetDualTokenTracker() *DualTokenTracker {
	return globalDualTracker
}

// ResetDualTokenTracker 重置(每个评测任务开始时调)。
func ResetDualTokenTracker() {
	globalDualTracker = &DualTokenTracker{}
}

// RecordAgentTurn 记录主 agent 的一轮 LLM 调用。
func (t *DualTokenTracker) RecordAgentTurn(usage TokenUsage, cost float64, latencyMs float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.AgentMetrics.Usage = t.AgentMetrics.Usage.Add(usage)
	t.AgentMetrics.CostUSD += cost
	t.AgentMetrics.Turns++
	if latencyMs > 0 {
		t.AgentMetrics.Latencies = append(t.AgentMetrics.Latencies, latencyMs)
	}
	// 同步到统计缓存
	updateStatsCache("agent", usage, cost)
}

// RecordCondenserTurn 记录 condenser(上下文压缩器)的一轮 LLM 调用。
//
// #52 核心修复:之前 compaction.go 的 summarizeChunk 调 LLM 后丢弃 token,
// 现在通过此方法独立计入 condenser_metrics。
func (t *DualTokenTracker) RecordCondenserTurn(usage TokenUsage, cost float64, latencyMs float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CondenserMetrics.Usage = t.CondenserMetrics.Usage.Add(usage)
	t.CondenserMetrics.CostUSD += cost
	t.CondenserMetrics.Turns++
	if latencyMs > 0 {
		t.CondenserMetrics.Latencies = append(t.CondenserMetrics.Latencies, latencyMs)
	}
	// 同步到统计缓存
	updateStatsCache("condenser", usage, cost)
}

// TotalCost 总成本(agent + condenser)。
func (t *DualTokenTracker) TotalCost() float64 {
	return t.AgentMetrics.CostUSD + t.CondenserMetrics.CostUSD
}

// TotalTokens 总 token(agent + condenser)。
func (t *DualTokenTracker) TotalTokens() int {
	return t.AgentMetrics.Usage.Total() + t.CondenserMetrics.Usage.Total()
}

// Summary 双路统计摘要。
func (t *DualTokenTracker) Summary() map[string]any {
	avgAgentLatency := 0.0
	if len(t.AgentMetrics.Latencies) > 0 {
		sum := 0.0
		for _, l := range t.AgentMetrics.Latencies {
			sum += l
		}
		avgAgentLatency = sum / float64(len(t.AgentMetrics.Latencies))
	}
	return map[string]any{
		"agent": map[string]any{
			"turns":          t.AgentMetrics.Turns,
			"prompt":         t.AgentMetrics.Usage.PromptTokens,
			"completion":     t.AgentMetrics.Usage.CompletionTokens,
			"cache_read":     t.AgentMetrics.Usage.CacheReadTokens,
			"cache_write":    t.AgentMetrics.Usage.CacheCreationTokens,
			"reasoning":      t.AgentMetrics.Usage.ReasoningTokens,
			"cache_hit_rate": fmt.Sprintf("%.1f%%", t.AgentMetrics.Usage.CacheHitRate()*100),
			"cost_usd":       t.AgentMetrics.CostUSD,
			"avg_latency_ms": avgAgentLatency,
		},
		"condenser": map[string]any{
			"turns":      t.CondenserMetrics.Turns,
			"prompt":     t.CondenserMetrics.Usage.PromptTokens,
			"completion": t.CondenserMetrics.Usage.CompletionTokens,
			"cost_usd":   t.CondenserMetrics.CostUSD,
		},
		"total_cost":   t.TotalCost(),
		"total_tokens": t.TotalTokens(),
	}
}

// ── #40 stats-cache 8 维度 + 扩展指标 ──

// StatsCache 全局统计缓存(增量计算,持久化到 stats 表)。
type StatsCache struct {
	mu               sync.Mutex
	ModelUsage       map[string]*TokenUsage // 按模型分桶
	ModelCost        map[string]float64     // 按模型累计成本
	TotalSessions    int
	TotalMessages    int
	DailyActivity    map[string]*DailyActivity // date → 指标
	HourCounts       map[int]int               // 0-23 小时分布
}

// DailyActivity 每日活动指标。
type DailyActivity struct {
	Date          string `json:"date"`
	MessageCount  int    `json:"message_count"`
	SessionCount  int    `json:"session_count"`
	ToolCallCount int    `json:"tool_call_count"`
}

// globalStatsCache 全局统计缓存。
var globalStatsCache = initStatsCache()

func initStatsCache() *StatsCache {
	return &StatsCache{
		ModelUsage:    make(map[string]*TokenUsage),
		ModelCost:     make(map[string]float64),
		DailyActivity: make(map[string]*DailyActivity),
		HourCounts:    make(map[int]int),
	}
}

// updateStatsCache 更新统计缓存(由 RecordAgentTurn/RecordCondenserTurn 调用)。
func updateStatsCache(model string, usage TokenUsage, cost float64) {
	sc := globalStatsCache
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.ModelUsage[model] == nil {
		sc.ModelUsage[model] = &TokenUsage{}
	}
	sc.ModelUsage[model] = new(TokenUsage)
	// 累加(从 DB 重建会覆盖,这里只做增量)
	u := sc.ModelUsage[model]
	u.PromptTokens += usage.PromptTokens
	u.CompletionTokens += usage.CompletionTokens
	u.CacheReadTokens += usage.CacheReadTokens
	u.CacheCreationTokens += usage.CacheCreationTokens
	u.ReasoningTokens += usage.ReasoningTokens

	sc.ModelCost[model] += cost
	sc.TotalMessages++

	now := time.Now().UTC()
	dateStr := now.Format("2006-01-02")
	if sc.DailyActivity[dateStr] == nil {
		sc.DailyActivity[dateStr] = &DailyActivity{Date: dateStr}
	}
	sc.DailyActivity[dateStr].MessageCount++
	sc.HourCounts[now.Hour()]++
}

// RecordToolCall 记录一次工具调用。
func RecordToolCall() {
	sc := globalStatsCache
	sc.mu.Lock()
	defer sc.mu.Unlock()
	now := time.Now().UTC()
	dateStr := now.Format("2006-01-02")
	if sc.DailyActivity[dateStr] == nil {
		sc.DailyActivity[dateStr] = &DailyActivity{Date: dateStr}
	}
	sc.DailyActivity[dateStr].ToolCallCount++
}

// RecordSession 记录一次会话开始。
func RecordSession() {
	sc := globalStatsCache
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.TotalSessions++
	now := time.Now().UTC()
	dateStr := now.Format("2006-01-02")
	if sc.DailyActivity[dateStr] == nil {
		sc.DailyActivity[dateStr] = &DailyActivity{Date: dateStr}
	}
	sc.DailyActivity[dateStr].SessionCount++
}

// GetStatsSummary 获取统计摘要(供 API /stats 返回)。
func GetStatsSummary() map[string]any {
	sc := globalStatsCache
	sc.mu.Lock()
	defer sc.mu.Unlock()

	modelUsage := make(map[string]any)
	for model, u := range sc.ModelUsage {
		modelUsage[model] = map[string]any{
			"prompt":      u.PromptTokens,
			"completion":  u.CompletionTokens,
			"cache_read":  u.CacheReadTokens,
			"cache_write": u.CacheCreationTokens,
			"reasoning":   u.ReasoningTokens,
			"total":       u.Total(),
			"cost_usd":    sc.ModelCost[model],
			"cache_hit":   fmt.Sprintf("%.1f%%", u.CacheHitRate()*100),
		}
	}

	return map[string]any{
		"total_sessions": sc.TotalSessions,
		"total_messages": sc.TotalMessages,
		"model_usage":    modelUsage,
		"daily_activity": sc.DailyActivity,
		"hour_counts":    sc.HourCounts,
	}
}

// PersistStats 持久化统计到 DB(可选,当前简化版只在内存)。
func PersistStats(taskID string) {
	// 当前简化版:统计缓存在内存,不持久化。
	// 后续可扩展为写入 stats 表。
	_ = store.DB // 确保引用了 store 包
	_ = taskID
}
