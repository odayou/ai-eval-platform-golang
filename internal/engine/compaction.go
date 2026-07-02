package engine

// compaction.go — 高可用上下文重构层(MRD 5.2 / 2.2)。
//
// 核心问题(MRD 原话):
//   "研发长文本上下文导致 Token 成本飙升与模型指令漂移。"
//
// 解决方案(借鉴 claw-code 的 Context Compaction 机制):
//   - 自动索引目标微服务的 RPC 接口定义、网络拓扑及历史高频 Bug 日志
//   - 采用"滑动窗口 + 摘要递进"算法,将几万行的代码背景压缩在千字以内
//   - 生成"高可用审查专属提示词",以最低成本换取最高代码可读性
//
// 工作流程:
//   1. 输入:一段长文本(代码/对话/文档)
//   2. 检测:是否超过 token 阈值
//   3. 压缩:滑动窗口切分 → 每段摘要 → 合并 → 再摘要
//   4. 输出:压缩后的文本 + 压缩比
//
// 压缩策略:
//   - 短文本(< 阈值):不压缩,直接返回
//   - 中等文本(1-3x 阈值):单轮摘要
//   - 超长文本(> 3x 阈值):多轮递进压缩(先分段摘要,再合并摘要)
//
// 存储:
//   - 压缩记录写入 compactions 表(借鉴 claw-code compact.rs)
//   - 支持压缩比统计(供归因分析判断是否信息丢失)

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// CompactionResult 是一次上下文压缩的结果。
type CompactionResult struct {
	OriginalLength  int     `json:"original_length"`  // 原始文本长度(字符)
	CompactedLength int     `json:"compacted_length"` // 压缩后长度
	Ratio           float64 `json:"ratio"`            // 压缩比(越小压得越狠)
	Summary         string  `json:"summary"`          // 压缩后的文本
	Rounds          int     `json:"rounds"`           // 压缩轮数
	SavedTokens     int     `json:"saved_tokens"`     // 节省的 token 数
	QualityScore    int     `json:"quality_score"`    // 6.1.3 压缩质量评分(0-100,基于关键词重叠率)
}

// CompactionConfig 压缩配置。
type CompactionConfig struct {
	MaxTokens      int `json:"max_tokens"`       // 目标 token 数(超过则压缩)
	CharsPerToken  int `json:"chars_per_token"`  // 字符/token 比率(中文约 1.5)
	MaxChunkTokens int `json:"max_chunk_tokens"` // 每段最大 token 数
	MaxRounds      int `json:"max_rounds"`       // 最大压缩轮数
}

// DefaultCompactionConfig 返回默认配置。
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		MaxTokens:      2000,  // 超过 2000 token 就压缩
		CharsPerToken:  2,     // 中文约 2 字符/token
		MaxChunkTokens: 500,   // 每段 500 token
		MaxRounds:      3,     // 最多 3 轮压缩
	}
}

// CompactContext 对长文本执行上下文压缩。
//
// 参数:
//   - text: 原始长文本
//   - config: 压缩配置
//   - conversationID: 关联的会话 ID(用于持久化)
//
// 返回:压缩结果(含压缩后文本 + 压缩比)。
func CompactContext(text string, config CompactionConfig, conversationID string) CompactionResult {
	originalLen := len([]rune(text))
	maxChars := config.MaxTokens * config.CharsPerToken

	// 短文本不压缩
	if originalLen <= maxChars {
		return CompactionResult{
			OriginalLength:  originalLen,
			CompactedLength: originalLen,
			Ratio:           1.0,
			Summary:         text,
			Rounds:          0,
			SavedTokens:     0,
		}
	}

	NewSlog("compaction").Info("开始压缩: 原始 字符(约 token), 目标 token", "originalLen", originalLen, "CharsPerToken", originalLen/config.CharsPerToken, "MaxTokens", config.MaxTokens)

	// 多轮递进压缩
	current := text
	rounds := 0
	for rounds < config.MaxRounds {
		currentLen := len([]rune(current))
		if currentLen <= maxChars {
			break
		}

		rounds++
		current = compactRound(current, config)
		NewSlog("compaction").Info("第 轮压缩: → 字符", "rounds", rounds, "currentLen", currentLen, "lenrunecurrent", len([]rune(current)))
	}

	compactedLen := len([]rune(current))
	ratio := float64(compactedLen) / float64(originalLen)
	savedTokens := (originalLen - compactedLen) / config.CharsPerToken

	result := CompactionResult{
		OriginalLength:  originalLen,
		CompactedLength: compactedLen,
		Ratio:           ratio,
		Summary:         current,
		Rounds:          rounds,
		SavedTokens:     savedTokens,
		QualityScore:    computeQualityScore(text, current),
	}

	// 持久化压缩记录
	if conversationID != "" {
		saveCompaction(conversationID, originalLen/config.CharsPerToken, compactedLen/config.CharsPerToken, current)
	}

	NewSlog("compaction").Info("压缩完成: → 字符(压缩比 %), 节省 token, 轮", "originalLen", originalLen, "compactedLen", compactedLen, "ratio100", ratio*100, "savedTokens", savedTokens, "rounds", rounds)

	return result
}

// compactRound 执行一轮压缩。
func compactRound(text string, config CompactionConfig) string {
	chunkChars := config.MaxChunkTokens * config.CharsPerToken

	// 1. 分段
	chunks := splitIntoChunks(text, chunkChars)

	// 2. 每段摘要
	summaries := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		summary := summarizeChunk(chunk, i+1, len(chunks))
		summaries = append(summaries, summary)
	}

	// 3. 合并
	merged := strings.Join(summaries, "\n\n")

	// 如果合并后仍然太长,递归压缩
	if len([]rune(merged)) > config.MaxTokens*config.CharsPerToken {
		return summarizeChunk(merged, 1, 1) // 整体再摘要一次
	}

	return merged
}

// splitIntoChunks 把长文本按固定大小切分(尽量在行边界切)。
func splitIntoChunks(text string, chunkSize int) []string {
	runes := []rune(text)
	if len(runes) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	lines := strings.Split(text, "\n")
	current := ""
	currentLen := 0

	for _, line := range lines {
		lineLen := len([]rune(line))
		if currentLen+lineLen+1 > chunkSize && current != "" {
			chunks = append(chunks, current)
			current = ""
			currentLen = 0
		}
		if current != "" {
			current += "\n"
			currentLen++
		}
		current += line
		currentLen += lineLen
	}

	if current != "" {
		chunks = append(chunks, current)
	}

	return chunks
}

// summarizeChunk 对一段文本生成摘要。
//
// 策略:
//   - 有 Key 时:调 LLM 生成摘要
//   - 无 Key 时:提取式摘要(取首尾 + 关键句)
func summarizeChunk(text string, chunkIndex, totalChunks int) string {
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("main")

	if baseURL != "" && apiKey != "" {
		// LLM 摘要
		prompt := fmt.Sprintf(`请对以下文本生成简洁摘要,保留关键信息(代码结构、函数签名、核心逻辑、错误处理),去除注释和冗余。

【文本(第 %d/%d 段)】
%s

请输出摘要(不超过 200 字):`, chunkIndex, totalChunks, truncateForClassify(text, 2000))

		result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, prompt)
		if err == nil && len(result.Text) > 20 {
			// #52 修复:condenser 的 token 不再丢弃,计入 condenser_metrics
			GetDualTokenTracker().RecordCondenserTurn(TokenUsage{
				PromptTokens:     result.TokenIn,
				CompletionTokens: result.TokenOut,
			}, 0, 0)
			return result.Text
		}
	}

	// Fallback:提取式摘要
	return extractiveSummary(text)
}

// extractiveSummary 提取式摘要(无 Key 时的 fallback)。
//
// 策略:
//   - 保留前 N 行(通常是 import/定义)
//   - 保留包含 func/def/class/struct 的行(结构骨架)
//   - 保留最后 N 行(通常是 return/输出)
//   - 去除纯注释和空行
func extractiveSummary(text string) string {
	lines := strings.Split(text, "\n")
	var important []string
	structural := 0

	// 保留前 5 行
	headCount := 5
	if headCount > len(lines) {
		headCount = len(lines)
	}
	important = append(important, lines[:headCount]...)
	important = append(important, "...")

	// 保留结构性行
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// 函数/类/结构体定义
		if strings.Contains(trimmed, "func ") || strings.Contains(trimmed, "def ") ||
			strings.Contains(trimmed, "class ") || strings.Contains(trimmed, "struct ") ||
			strings.Contains(trimmed, "type ") || strings.Contains(trimmed, "interface ") {
			important = append(important, line)
			structural++
			if structural > 15 {
				break
			}
		}
	}

	// 保留最后 3 行
	tailCount := 3
	if tailCount > len(lines) {
		tailCount = len(lines)
	}
	important = append(important, "...")
	important = append(important, lines[len(lines)-tailCount:]...)

	return strings.Join(important, "\n")
}

// saveCompaction 持久化压缩记录到 compactions 表。
func saveCompaction(conversationID string, originalTokens, compactedTokens int, summary string) {
	if store.DB == nil {
		return // 允许测试中没有数据库
	}
	store.DB.Exec(`INSERT INTO compactions (id, conversation_id, original_tokens, compacted_tokens, summary, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		uuid.New().String(), conversationID, originalTokens, compactedTokens, summary)
}

// CompactIfNeeded 检查文本是否需要压缩,需要则压缩。
func CompactIfNeeded(text string, conversationID string) (string, CompactionResult) {
	config := DefaultCompactionConfig()
	originalTokens := len([]rune(text)) / config.CharsPerToken

	if originalTokens <= config.MaxTokens {
		return text, CompactionResult{
			OriginalLength:  len([]rune(text)),
			CompactedLength: len([]rune(text)),
			Ratio:           1.0,
			Summary:         text,
			Rounds:          0,
		}
	}

	result := CompactContext(text, config, conversationID)
	return result.Summary, result
}

// EstimateTokensForText 估算文本的 token 数。
func EstimateTokensForText(text string) int {
	return CountTokens(text)
}

// CompactConversationHistory 压缩对话历史(保留最近 N 轮,旧的摘要)。
func CompactConversationHistory(messages []map[string]string, maxTokens int) []map[string]string {
	if len(messages) <= 4 {
		return messages // 太短不压缩
	}

	// 估算总 token
	totalTokens := 0
	for _, m := range messages {
		totalTokens += EstimateTokensForText(m["content"])
	}

	if totalTokens <= maxTokens {
		return messages // 不超限
	}

	// 保留最近 4 条消息
	recent := messages[len(messages)-4:]
	old := messages[:len(messages)-4]

	// 对旧消息生成摘要
	oldText := ""
	for _, m := range old {
		oldText += fmt.Sprintf("[%s] %s\n", m["role"], m["content"])
	}

	summary, _ := CompactIfNeeded(oldText, "")

	// 构造压缩后的消息列表
	compacted := []map[string]string{
		{"role": "system", "content": fmt.Sprintf("【历史对话摘要】\n%s", summary)},
	}
	compacted = append(compacted, recent...)

	NewSlog("compaction").Info("对话历史压缩: 条 → 1 条摘要 + 4 条最近", "messages_count", len(messages))
	return compacted
}

// FormatCompactionStats 格式化压缩统计信息。
func FormatCompactionStats(result CompactionResult) string {
	if result.Rounds == 0 {
		return "未压缩(文本在限制内)"
	}
	return fmt.Sprintf("压缩 %d 轮: %d → %d 字符(压缩比 %.0f%%), 节省约 %d token",
		result.Rounds, result.OriginalLength, result.CompactedLength, result.Ratio*100, result.SavedTokens)
}

// ── 6 种 Condenser 策略(good-skills #46 补齐) ──
//
// 移植自 OpenHands config.template.toml condenser 配置。
// 6 种策略覆盖不同场景:noop/masking/recent/llm/amortized/llm_attention。
// 统一 Condense 接口,可配置切换。

// CondenserType 上下文压缩策略类型。
type CondenserType string

const (
	CondenserNoop               CondenserType = "noop"                // 不压缩
	CondenserObservationMasking CondenserType = "observation_masking" // 屏蔽旧 observation
	CondenserRecent             CondenserType = "recent"              // 只保留最近 N 条
	CondenserLLM                CondenserType = "llm"                 // LLM 摘要
	CondenserAmortized          CondenserType = "amortized"           // 智能遗忘(action 优先)
	CondenserLLMAttention       CondenserType = "llm_attention"       // LLM 优先级评分
)

// CondenserConfig 6 种策略的配置。
type CondenserConfig struct {
	Type            CondenserType `json:"type"`
	KeepFirst       int           `json:"keep_first"`        // 保留首条(用户原始意图)
	MaxEvents       int           `json:"max_events"`        // 窗口大小
	AttentionWindow int           `json:"attention_window"`  // observation_masking 的窗口
	LLMRole         string        `json:"llm_role"`          // 独立 condenser LLM 角色
}

// DefaultCondenserConfig 默认配置(LLM 摘要)。
func DefaultCondenserConfig() CondenserConfig {
	return CondenserConfig{
		Type:      CondenserLLM,
		KeepFirst: 1,
		MaxEvents: 100,
		AttentionWindow: 50,
		LLMRole:   "main",
	}
}

// CondenseEvent 是压缩操作的输入单元(对话历史的一条)。
type CondenseEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`    // message | action | observation | tool_result
	Role    string `json:"role"`    // user | assistant | tool
	Content string `json:"content"`
}

// CondensationOutcome 压缩结果。
type CondensationOutcome struct {
	KeptEvents        []CondenseEvent `json:"kept_events"`
	ForgottenEventIDs []string        `json:"forgotten_event_ids"`
	Summary           string          `json:"summary"`
}

// Condense 按配置策略压缩事件列表。
func Condense(events []CondenseEvent, cfg CondenserConfig) CondensationOutcome {
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 100
	}
	if cfg.KeepFirst <= 0 {
		cfg.KeepFirst = 1
	}
	if len(events) <= cfg.MaxEvents {
		return CondensationOutcome{KeptEvents: events}
	}

	switch cfg.Type {
	case CondenserNoop:
		return CondensationOutcome{KeptEvents: events}
	case CondenserObservationMasking:
		return condenseMasking(events, cfg)
	case CondenserRecent:
		return condenseRecent(events, cfg)
	case CondenserAmortized:
		return condenseAmortized(events, cfg)
	case CondenserLLMAttention:
		return condenseLLMAttention(events, cfg)
	case CondenserLLM:
		fallthrough
	default:
		return condenseLLM(events, cfg)
	}
}

// ── 6.2.3 策略自动选择 ──

// autoSelectCondenser 根据对话长度自动选择最佳压缩策略。
// 策略:
//   - 短对话(≤20 条):Recent 策略(保留最近,开销最低)
//   - 中等对话(21-100 条):Importance 策略(Amortized,action 优先保留)
//   - 长对话(>100 条):LLMAttention 策略(LLM 评分,信息保留最好)
func autoSelectCondenser(eventCount int) CondenserType {
	switch {
	case eventCount <= 20:
		return CondenserRecent
	case eventCount <= 100:
		return CondenserAmortized
	default:
		return CondenserLLMAttention
	}
}

// condenseMasking 保留结构但屏蔽旧 observation(attention_window 外的)。
func condenseMasking(events []CondenseEvent, cfg CondenserConfig) CondensationOutcome {
	window := cfg.AttentionWindow
	if window <= 0 {
		window = 50
	}
	kept := make([]CondenseEvent, len(events))
	copy(kept, events)
	var forgotten []string
	cutoff := len(events) - window
	for i, e := range kept {
		if e.Type == "observation" && i < cutoff {
			kept[i].Content = "[masked]"
			forgotten = append(forgotten, e.ID)
		}
	}
	return CondensationOutcome{KeptEvents: kept, ForgottenEventIDs: forgotten}
}

// condenseRecent 只保留首条 + 最近 N 条。
func condenseRecent(events []CondenseEvent, cfg CondenserConfig) CondensationOutcome {
	first := events[:cfg.KeepFirst]
	recentStart := len(events) - cfg.MaxEvents + cfg.KeepFirst
	if recentStart < cfg.KeepFirst {
		recentStart = cfg.KeepFirst
	}
	kept := append(first, events[recentStart:]...)
	var forgotten []string
	keptIDs := make(map[string]bool)
	for _, e := range kept {
		keptIDs[e.ID] = true
	}
	for _, e := range events {
		if !keptIDs[e.ID] {
			forgotten = append(forgotten, e.ID)
		}
	}
	return CondensationOutcome{KeptEvents: kept, ForgottenEventIDs: forgotten}
}

// condenseAmortized 智能遗忘:action 优先保留,observation 按时间衰减。
func condenseAmortized(events []CondenseEvent, cfg CondenserConfig) CondensationOutcome {
	half := cfg.MaxEvents / 2
	if half < 1 {
		half = 1
	}
	first := events[:cfg.KeepFirst]
	rest := events[cfg.KeepFirst:]
	var actions, observations []CondenseEvent
	for _, e := range rest {
		if e.Type == "action" || e.Type == "message" {
			actions = append(actions, e)
		} else {
			observations = append(observations, e)
		}
	}
	// 各取最近的 half 条
	if len(actions) > half {
		actions = actions[len(actions)-half:]
	}
	if len(observations) > half {
		observations = observations[len(observations)-half:]
	}
	// 按 ID 在原 events 里的顺序合并
	keepSet := make(map[string]bool)
	for _, e := range first {
		keepSet[e.ID] = true
	}
	for _, e := range actions {
		keepSet[e.ID] = true
	}
	for _, e := range observations {
		keepSet[e.ID] = true
	}
	var kept []CondenseEvent
	var forgotten []string
	for _, e := range events {
		if keepSet[e.ID] {
			kept = append(kept, e)
		} else {
			forgotten = append(forgotten, e.ID)
		}
	}
	return CondensationOutcome{KeptEvents: kept, ForgottenEventIDs: forgotten}
}

// condenseLLMAttention LLM 优先级评分。
// 6.2.1:当 LLM 可用时,调用 LLM 为每条事件评分;失败时回退到启发式评分。
func condenseLLMAttention(events []CondenseEvent, cfg CondenserConfig) CondensationOutcome {
	// 6.2.1:尝试用 LLM 评分
	if llmScores, ok := scoreEventsByLLM(events, cfg); ok {
		return selectByScores(events, llmScores, cfg)
	}

	// 回退:启发式评分(message/action 高分,observation 低分;首条和最新加分)
	scores := make([]int, len(events))
	for i, e := range events {
		score := 0
		if e.Type == "message" {
			score += 3
		}
		if e.Type == "action" {
			score += 2
		}
		if e.Type == "observation" {
			score += 1
		}
		if i < cfg.KeepFirst {
			score += 10 // 首条保底高分
		}
		if i >= len(events)-5 {
			score += 5 // 最近 5 条加分
		}
		scores[i] = score
	}
	// 按分数取 top MaxEvents
	type idxScore struct{ idx, score int }
	ranked := make([]idxScore, len(events))
	for i := range events {
		ranked[i] = idxScore{i, scores[i]}
	}
	// 简单选择排序取 top N
	keepCount := cfg.MaxEvents
	if keepCount > len(events) {
		keepCount = len(events)
	}
	for i := 0; i < keepCount; i++ {
		maxJ := i
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[maxJ].score {
				maxJ = j
			}
		}
		ranked[i], ranked[maxJ] = ranked[maxJ], ranked[i]
	}
	keepSet := make(map[int]bool)
	for i := 0; i < keepCount; i++ {
		keepSet[ranked[i].idx] = true
	}
	var kept []CondenseEvent
	var forgotten []string
	for i, e := range events {
		if keepSet[i] {
			kept = append(kept, e)
		} else {
			forgotten = append(forgotten, e.ID)
		}
	}
	return CondensationOutcome{KeptEvents: kept, ForgottenEventIDs: forgotten}
}

// ── 6.2.1 LLM 事件评分 ──

// scoreEventsByLLM 调用 LLM 为每条事件评分(重要性 1-10)。
// 返回 (scores, true) 表示成功;失败时返回 (nil, false) 供调用方回退到启发式。
func scoreEventsByLLM(events []CondenseEvent, cfg CondenserConfig) ([]int, bool) {
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint(cfg.LLMRole)
	if baseURL == "" || apiKey == "" {
		return nil, false
	}

	// 构造事件摘要列表(截断避免超 token)
	var sb strings.Builder
	sb.WriteString("请对以下对话事件按重要性评分(1-10分,10=最关键)。\n")
	sb.WriteString("只输出每行一个数字,每行对应一个事件的分数,不要输出其他内容。\n\n")
	for i, e := range events {
		sb.WriteString(fmt.Sprintf("%d. [%s/%s] %s\n", i+1, e.Type, e.Role, truncate(e.Content, 150)))
	}
	sb.WriteString("\n请输出每行一个分数(共 ")
	sb.WriteString(fmt.Sprintf("%d 行):", len(events)))

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, sb.String())
	if err != nil {
		NewSlog("compaction").Error("LLM 评分失败,回退启发式", "err", err)
		return nil, false
	}

	// 解析 LLM 返回的分数(每行一个数字)
	lines := strings.Split(strings.TrimSpace(result.Text), "\n")
	scores := make([]int, len(events))
	parsed := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 跳过非数字行(如 "1. 8" → 提取 "8")
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		scoreStr := parts[len(parts)-1] // 取最后一个字段作为分数
		var score int
		if _, err := fmt.Sscanf(scoreStr, "%d", &score); err == nil && score >= 1 && score <= 10 {
			if parsed < len(scores) {
				scores[parsed] = score
				parsed++
			}
		}
	}

	if parsed < len(events)/2 {
		// 解析成功的不到一半,视为失败
		NewSlog("compaction").Warn("LLM 评分解析不完整(/),回退启发式", "parsed", parsed, "events_count", len(events))
		return nil, false
	}

	// 未解析到的事件给默认分 1
	for i := parsed; i < len(scores); i++ {
		scores[i] = 1
	}

	// 记录 condenser token
	GetDualTokenTracker().RecordCondenserTurn(TokenUsage{
		PromptTokens:     result.TokenIn,
		CompletionTokens: result.TokenOut,
	}, 0, 0)

	return scores, true
}

// selectByScores 根据分数选择保留的事件(top MaxEvents)。
func selectByScores(events []CondenseEvent, scores []int, cfg CondenserConfig) CondensationOutcome {
	type idxScore struct{ idx, score int }
	ranked := make([]idxScore, len(events))
	for i := range events {
		ranked[i] = idxScore{i, scores[i]}
	}
	// 选择排序取 top N
	keepCount := cfg.MaxEvents
	if keepCount > len(events) {
		keepCount = len(events)
	}
	for i := 0; i < keepCount; i++ {
		maxJ := i
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[maxJ].score {
				maxJ = j
			}
		}
		ranked[i], ranked[maxJ] = ranked[maxJ], ranked[i]
	}
	keepSet := make(map[int]bool)
	for i := 0; i < keepCount; i++ {
		keepSet[ranked[i].idx] = true
	}
	// 确保首条事件保留
	for i := 0; i < cfg.KeepFirst && i < len(events); i++ {
		keepSet[i] = true
	}
	var kept []CondenseEvent
	var forgotten []string
	for i, e := range events {
		if keepSet[i] {
			kept = append(kept, e)
		} else {
			forgotten = append(forgotten, e.ID)
		}
	}
	return CondensationOutcome{KeptEvents: kept, ForgottenEventIDs: forgotten}
}

// ── 6.1.2 摘要缓存 ──

type compactCacheEntry struct {
	summary string
	time    time.Time
}

// condenseLLM 用 LLM 摘要旧事件(复用已有的 summarizeChunk)。
func condenseLLM(events []CondenseEvent, cfg CondenserConfig) CondensationOutcome {
	first := events[:cfg.KeepFirst]
	recentStart := len(events) - cfg.MaxEvents + cfg.KeepFirst
	if recentStart < cfg.KeepFirst {
		recentStart = cfg.KeepFirst
	}
	toSummarize := events[cfg.KeepFirst:recentStart]
	recent := events[recentStart:]

	var sb strings.Builder
	for _, e := range toSummarize {
		sb.WriteString(fmt.Sprintf("[%s/%s] %s\n", e.Type, e.Role, truncate(e.Content, 200)))
	}
	summary := summarizeChunk(sb.String(), 0, 1)

	var forgotten []string
	for _, e := range toSummarize {
		forgotten = append(forgotten, e.ID)
	}
	summaryEvent := CondenseEvent{
		ID:      "condensation-summary",
		Type:    "condensation",
		Role:    "system",
		Content: summary,
	}
	kept := append(first, summaryEvent)
	kept = append(kept, recent...)
	return CondensationOutcome{KeptEvents: kept, ForgottenEventIDs: forgotten, Summary: summary}
}

var (
	compactionCache   = make(map[string]compactCacheEntry)
	compactionCacheMu sync.RWMutex
)

// getCachedSummary 检查文本是否有缓存的摘要（6.1.2 hash-based）。
func getCachedSummary(text string) (string, bool) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))[:16]
	compactionCacheMu.RLock()
	entry, ok := compactionCache[hash]
	compactionCacheMu.RUnlock()
	if ok && time.Since(entry.time) < 30*time.Minute {
		return entry.summary, true
	}
	return "", false
}

// setCachedSummary 存入缓存。
func setCachedSummary(text, summary string) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))[:16]
	compactionCacheMu.Lock()
	compactionCache[hash] = compactCacheEntry{summary: summary, time: time.Now()}
	compactionCacheMu.Unlock()
}

// summarizeChunkCached 带缓存的 summarizeChunk（6.1.2）。
func summarizeChunkCached(text string, chunkIndex, totalChunks int) string {
	if cached, ok := getCachedSummary(text); ok {
		return cached
	}
	result := summarizeChunk(text, chunkIndex, totalChunks)
	setCachedSummary(text, result)
	return result
}

// ── 6.4 结构化压缩摘要 ──

// formatStructuredSummary 将压缩结果格式化为结构化摘要。
// 代码场景：文件列表 / 关键函数签名 / 变更点
// 对话场景：用户意图 / 已完成步骤 / 当前阻塞 / 下一步
func formatStructuredSummary(outcome CondensationOutcome, scene string) string {
	var sb strings.Builder
	if scene == "code" {
		sb.WriteString("## 压缩摘要（代码场景）\n")
	} else {
		sb.WriteString("## 压缩摘要（对话场景）\n")
	}

	// 保留的关键事件数
	sb.WriteString(fmt.Sprintf("- 保留事件: %d / 丢弃: %d\n", len(outcome.KeptEvents), len(outcome.ForgottenEventIDs)))

	// 摘要内容
	if outcome.Summary != "" {
		sb.WriteString("\n### 压缩后摘要\n")
		sb.WriteString(outcome.Summary)
	}

	// 关键文件/函数列表（从 kept events 提取）
	if len(outcome.KeptEvents) > 0 {
		sb.WriteString("\n\n### 关键事件（保留）\n")
		for i, e := range outcome.KeptEvents {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... 还有 %d 条\n", len(outcome.KeptEvents)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", e.Type, e.Role, truncate(e.Content, 100)))
		}
	}

	return sb.String()
}

// ── 6.3 解耦: CompactAgentMessages 供 agent_loop 调用 ──
// 2.3.2: condenserType 参数化(不再硬编码 CondenserRecent),支持按 agent 配置选择压缩策略。
func CompactAgentMessages(messages []loopMessage, taskID string, tokenBudget int, condenserType CondenserType) []loopMessage {
	var events []CondenseEvent
	for i, msg := range messages {
		events = append(events, CondenseEvent{
			ID: fmt.Sprintf("msg-%d", i), Type: msg.Role,
			Role: msg.Role, Content: truncateForCompaction(msg.Content, 500),
		})
	}
	compacted := Condense(events, CondenserConfig{
		Type: condenserType, KeepFirst: 2, MaxEvents: 20,
	})
	result := make([]loopMessage, 0, len(compacted.KeptEvents))
	for _, e := range compacted.KeptEvents {
		result = append(result, loopMessage{Role: e.Role, Content: e.Content})
	}
	if taskID != "" {
		if task, gerr := store.GetTask(taskID); gerr == nil && task != nil {
			gm := NewGoalManager("完成评测任务: "+task.Name, tokenBudget, 600)
			result = append(result, loopMessage{
				Role: "system", Content: gm.RenderContinuationPrompt(),
			})
		}
	}
	return result
}

func truncateForCompaction(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "..."
}

// ── 6.1.3 压缩质量评估 ──

// computeQualityScore 计算压缩质量评分(0-100)。
// 策略:提取原始文本和摘要中的关键词,计算关键词重叠率。
// 重叠率越高说明摘要保留了越多关键信息,质量越好。
func computeQualityScore(original, summary string) int {
	originalKeywords := extractKeywords(original)
	summaryKeywords := extractKeywords(summary)

	if len(originalKeywords) == 0 {
		return 100 // 原始文本无关键词,压缩无损
	}

	// 计算关键词重叠数
	overlap := 0
	for kw := range summaryKeywords {
		if originalKeywords[kw] {
			overlap++
		}
	}

	// 评分 = 重叠数 / 原始关键词数 * 100
	score := int(float64(overlap) / float64(len(originalKeywords)) * 100)
	if score > 100 {
		score = 100
	}
	return score
}

// extractKeywords 从文本中提取关键词(简单分词:按空白和标点切分,过滤短词和停用词)。
func extractKeywords(text string) map[string]bool {
	stopWords := map[string]bool{
		"的": true, "了": true, "是": true, "在": true, "和": true,
		"与": true, "或": true, "不": true, "有": true, "为": true,
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "shall": true, "can": true, "to": true,
		"of": true, "in": true, "for": true, "on": true, "with": true,
		"at": true, "by": true, "from": true, "as": true, "into": true,
		"this": true, "that": true, "it": true, "its": true, "if": true,
		"and": true, "or": true, "but": true, "not": true, "no": true,
		"//": true, "/*": true, "*/": true, "**": true, "##": true,
	}

	keywords := make(map[string]bool)
	// 按空白和标点切分
	words := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '.' ||
			r == ';' || r == ':' || r == '(' || r == ')' || r == '{' || r == '}' ||
			r == '[' || r == ']' || r == '<' || r == '>' || r == '"' || r == '\'' ||
			r == '/' || r == '\\' || r == '|' || r == '!' || r == '?' ||
			r == '=' || r == '+' || r == '-' || r == '*' || r == '&'
	})

	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if len([]rune(w)) >= 2 && !stopWords[w] {
			keywords[w] = true
		}
	}
	return keywords
}
