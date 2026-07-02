package engine

// goal_manager.go — 目标/预算管理(good-skills #26 补齐)。
//
// 补齐专家评审:#26 有 token/费用断路器,缺 objective 目标对象。
// 新增:GoalManager(线程级 objective + token/time 预算 + untrusted 标签防注入)。

import (
	"fmt"
	"strings"
	"time"
)

// Goal 目标对象。
type Goal struct {
	Objective     string `json:"objective"`
	Trusted       bool   `json:"trusted"`       // true=用户确认, false=agent 编辑(不可信)
	TokenBudget   int    `json:"token_budget"`   // token 预算
	TimeBudgetS   int    `json:"time_budget_s"`  // 时间预算(秒)
	TokensUsed    int    `json:"tokens_used"`    // 已用 token
	StartedAt     time.Time `json:"started_at"`
}

// GoalManager 目标/预算管理器。
type GoalManager struct {
	Goal *Goal
}

// NewGoalManager 创建管理器。
func NewGoalManager(objective string, tokenBudget, timeBudgetS int) *GoalManager {
	return &GoalManager{
		Goal: &Goal{
			Objective:   objective,
			Trusted:     true, // 初始目标来自用户=可信
			TokenBudget: tokenBudget,
			TimeBudgetS: timeBudgetS,
			StartedAt:   time.Now(),
		},
	}
}

// UpdateObjective 更新目标(默认不可信=来自 agent)。
func (g *GoalManager) UpdateObjective(newObjective string) {
	g.Goal.Objective = newObjective
	g.Goal.Trusted = false // agent 编辑的目标不可信
}

// ConsumeTokens 消耗 token。
func (g *GoalManager) ConsumeTokens(n int) {
	g.Goal.TokensUsed += n
}

// IsBudgetExceeded 预算是否超限。
func (g *GoalManager) IsBudgetExceeded() bool {
	if g.Goal.TokenBudget > 0 && g.Goal.TokensUsed >= g.Goal.TokenBudget {
		return true
	}
	if g.Goal.TimeBudgetS > 0 && time.Since(g.Goal.StartedAt) > time.Duration(g.Goal.TimeBudgetS)*time.Second {
		return true
	}
	return false
}

// RemainingPct 剩余预算百分比。
func (g *GoalManager) RemainingPct() float64 {
	tokPct := 1.0
	if g.Goal.TokenBudget > 0 {
		tokPct = 1.0 - float64(g.Goal.TokensUsed)/float64(g.Goal.TokenBudget)
	}
	timePct := 1.0
	if g.Goal.TimeBudgetS > 0 {
		elapsed := time.Since(g.Goal.StartedAt).Seconds()
		timePct = 1.0 - elapsed/float64(g.Goal.TimeBudgetS)
	}
	if tokPct < timePct {
		return tokPct
	}
	return timePct
}

// RenderGoalPrompt 渲染目标提醒(注入 agent prompt)。
func (g *GoalManager) RenderGoalPrompt() string {
	if g.Goal == nil {
		return ""
	}
	var sb strings.Builder
	if !g.Goal.Trusted {
		sb.WriteString("<untrusted_objective>\n")
		sb.WriteString("当前目标(用户尚未确认): ")
		sb.WriteString(g.Goal.Objective)
		sb.WriteString("\n</untrusted_objective>")
	} else {
		sb.WriteString("<objective>")
		sb.WriteString(g.Goal.Objective)
		sb.WriteString("</objective>")
	}
	pct := g.RemainingPct()
	if pct < 0.2 {
		sb.WriteString(fmt.Sprintf("\n⚠️ 预算仅剩 %.0f%%,请尽快完成当前任务并收尾(wrap up this turn)。", pct*100))
	}
	return sb.String()
}

// RenderContinuationPrompt 上下文压缩后注入(继续目标)。
func (g *GoalManager) RenderContinuationPrompt() string {
	if g.Goal == nil {
		return ""
	}
	return fmt.Sprintf("<continuation>\n之前的对话被压缩了。你的目标是: %s\n已用 token: %d/%d\n请继续完成目标,不要从头开始。\n</continuation>",
		g.Goal.Objective, g.Goal.TokensUsed, g.Goal.TokenBudget)
}
