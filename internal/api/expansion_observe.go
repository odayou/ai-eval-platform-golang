package api

// expansion_observe.go — 方向4: Agent Observability API handler。

import (
	"fmt"
	"net/http"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func reportObservabilityEvent(c *gin.Context) {
	var req struct {
		AgentID          string  `json:"agent_id" binding:"required"`
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		CacheReadTokens  int     `json:"cache_read_tokens"`
		CostUSD          float64 `json:"cost_usd"`
		LatencyMs        float64 `json:"latency_ms"`
		ErrorCount       int     `json:"error_count"`
		EventType        string  `json:"event_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.EventType == "" { req.EventType = "message" }
	id := uuid.New().String()
	store.DB.Exec(`INSERT INTO agent_metrics (id, agent_id, prompt_tokens, completion_tokens, cache_read_tokens,
		cost_usd, latency_ms, error_count, event_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, req.AgentID, req.PromptTokens, req.CompletionTokens, req.CacheReadTokens,
		req.CostUSD, req.LatencyMs, req.ErrorCount, req.EventType)
	checkAlertRules(req.AgentID, req.CostUSD, req.LatencyMs, req.ErrorCount)
	c.JSON(http.StatusOK, gin.H{"recorded": id})
}

func getObservabilityDashboard(c *gin.Context) {
	agentID := c.Query("agent_id")
	hours := c.DefaultQuery("hours", "24")
	var stats struct {
		TotalTokens int
		TotalCost   float64
		AvgLatency  float64
		TotalErrors int
		TotalEvents int
	}
	query := `SELECT COALESCE(SUM(prompt_tokens+completion_tokens),0), COALESCE(SUM(cost_usd),0),
		COALESCE(AVG(latency_ms),0), COALESCE(SUM(error_count),0), COUNT(*)
		FROM agent_metrics WHERE timestamp > datetime('now', '-' || ? || ' hours')`
	args := []any{hours}
	if agentID != "" {
		query += " AND agent_id=?"
		args = append(args, agentID)
	}
	store.DB.QueryRow(query, args...).Scan(&stats.TotalTokens, &stats.TotalCost, &stats.AvgLatency, &stats.TotalErrors, &stats.TotalEvents)
	rows, _ := store.DB.Query(`SELECT id, rule_id, metric_value, threshold, message, triggered_at
		FROM alert_events ORDER BY triggered_at DESC LIMIT 10`)
	type Alert struct {
		ID, RuleID, Message, TriggeredAt string
		Value, Threshold                 float64
	}
	var alerts []Alert
	for rows != nil && rows.Next() {
		var a Alert
		rows.Scan(&a.ID, &a.RuleID, &a.Value, &a.Threshold, &a.Message, &a.TriggeredAt)
		alerts = append(alerts, a)
	}
	if rows != nil { rows.Close() }
	c.JSON(http.StatusOK, gin.H{"stats": stats, "alerts": alerts})
}

func checkAlertRules(agentID string, cost, latency float64, errors int) {
	rows, _ := store.DB.Query(`SELECT id, metric, condition, threshold FROM alert_rules WHERE enabled=1 AND (agent_id=? OR agent_id IS NULL OR agent_id='')`, agentID)
	if rows == nil { return }
	defer rows.Close()
	for rows.Next() {
		var rule struct{ ID, Metric, Condition string; Threshold float64 }
		rows.Scan(&rule.ID, &rule.Metric, &rule.Condition, &rule.Threshold)
		triggered := false
		value := 0.0
		switch rule.Metric {
		case "cost_usd": value = cost
		case "latency_ms": value = latency
		case "error_count": value = float64(errors)
		}
		switch rule.Condition {
		case "gt": triggered = value > rule.Threshold
		case "lt": triggered = value < rule.Threshold
		case "eq": triggered = value == rule.Threshold
		}
		if triggered {
			msg := fmt.Sprintf("[%s] %s=%.2f %s %.2f", agentID, rule.Metric, value, rule.Condition, rule.Threshold)
			store.DB.Exec(`INSERT INTO alert_events (id, rule_id, agent_id, metric_value, threshold, message)
				VALUES (?, ?, ?, ?, ?, ?)`, uuid.New().String(), rule.ID, agentID, value, rule.Threshold, msg)
			store.DB.Exec(`UPDATE alert_rules SET last_triggered=? WHERE id=?`, time.Now(), rule.ID)
		}
	}
}

func createAlertRule(c *gin.Context) {
	var req struct {
		AgentID       string  `json:"agent_id"`
		Metric        string  `json:"metric" binding:"required"`
		Condition     string  `json:"condition" binding:"required"`
		Threshold     float64 `json:"threshold" binding:"required"`
		WindowMinutes int     `json:"window_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.WindowMinutes == 0 { req.WindowMinutes = 5 }
	id := "alert-" + uuid.New().String()[:8]
	store.DB.Exec(`INSERT INTO alert_rules (id, agent_id, metric, condition, threshold, window_minutes, enabled)
		VALUES (?, ?, ?, ?, ?, ?, 1)`, id, nullStr(req.AgentID), req.Metric, req.Condition, req.Threshold, req.WindowMinutes)
	c.JSON(http.StatusCreated, gin.H{"id": id})
}
