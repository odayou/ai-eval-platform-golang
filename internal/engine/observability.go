package engine

// observability.go — Agent 生产监控引擎(拓展方向4 独立文件)。
//
// D57-D60:从 expansion.go 抽出,增强:
//   - D58 多粒度时序聚合(1min/5min/1hour/1day)
//   - D59 异常检测(移动平均 + 标准差,不只阈值)
//   - D60 多渠道告警(webhook/email/slack)

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// D58 多粒度时序聚合。

// TimeSeriesBucket 时序聚合桶。
type TimeSeriesBucket struct {
	Timestamp    time.Time `json:"timestamp"`
	TokenCount   int       `json:"token_count"`
	Cost         float64   `json:"cost"`
	LatencyAvg   float64   `json:"latency_avg"`
	ErrorCount   int       `json:"error_count"`
	EventCount   int       `json:"event_count"`
}

// AggregateMetrics 多粒度聚合指标(从 agent_metrics 表读)。
//
// granularity: "1min" / "5min" / "1hour" / "1day"
func AggregateMetrics(agentID, granularity string, hours int) []TimeSeriesBucket {
	if hours <= 0 {
		hours = 24
	}
	// 构造 SQL 按时间桶分组
	var bucketExpr string
	switch granularity {
	case "1min":
		bucketExpr = "strftime('%Y-%m-%d %H:%M', timestamp)"
	case "5min":
		bucketExpr = "strftime('%Y-%m-%d %H:%M', datetime(timestamp, '-' || (strftime('%M',timestamp) % 5) || ' minutes'))"
	case "1hour":
		bucketExpr = "strftime('%Y-%m-%d %H', timestamp)"
	case "1day":
		bucketExpr = "strftime('%Y-%m-%d', timestamp)"
	default:
		bucketExpr = "strftime('%Y-%m-%d %H', timestamp)"
	}

	query := fmt.Sprintf(`SELECT %s as bucket,
		COALESCE(SUM(prompt_tokens + completion_tokens), 0),
		COALESCE(SUM(cost_usd), 0),
		COALESCE(AVG(latency_ms), 0),
		COALESCE(SUM(error_count), 0),
		COUNT(*)
		FROM agent_metrics
		WHERE timestamp > datetime('now', '-%d hours')
		AND (agent_id = ? OR ? = '')
		GROUP BY bucket ORDER BY bucket`, bucketExpr, hours)

	rows, err := store.DB.Query(query, agentID, agentID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var buckets []TimeSeriesBucket
	for rows.Next() {
		var b TimeSeriesBucket
		var bucketStr string
		rows.Scan(&bucketStr, &b.TokenCount, &b.Cost, &b.LatencyAvg, &b.ErrorCount, &b.EventCount)
		// 尝试解析时间
		for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15", "2006-01-02"} {
			if t, err := time.Parse(layout, bucketStr); err == nil {
				b.Timestamp = t
				break
			}
		}
		buckets = append(buckets, b)
	}
	return buckets
}

// D59 异常检测(移动平均 + 标准差)。

// AnomalyDetector 异常检测器。
type AnomalyDetector struct {
	Window      int     // 移动平均窗口(默认 10)
	Sensitivity float64 // 灵敏度(标准差倍数,默认 3.0=3σ)
}

// NewAnomalyDetector 创建异常检测器。
func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{Window: 10, Sensitivity: 3.0}
}

// DetectAnomalies 检测时序数据中的异常点。
//
// 用移动平均 + 标准差:偏离均值超过 sensitivity 个标准差的点为异常。
// 返回异常点的索引列表。
func (d *AnomalyDetector) DetectAnomalies(values []float64) []int {
	if len(values) < d.Window {
		return nil // 数据太少
	}
	var anomalies []int
	for i := d.Window; i < len(values); i++ {
		// 计算前 Window 个点的移动平均和标准差
		window := values[i-d.Window : i]
		mean := avg(window)
		stdDev := stdDev(window, mean)

		// 检查当前点是否偏离
		if stdDev > 0 {
			zScore := math.Abs(values[i]-mean) / stdDev
			if zScore > d.Sensitivity {
				anomalies = append(anomalies, i)
			}
		}
	}
	return anomalies
}

// DetectCostSpike 检测成本突变(比上一周期高 N 倍)。
func DetectCostSpike(buckets []TimeSeriesBucket, spikeMultiplier float64) []int {
	if spikeMultiplier <= 0 {
		spikeMultiplier = 5.0 // 默认 5 倍突变
	}
	var spikes []int
	for i := 1; i < len(buckets); i++ {
		if buckets[i-1].Cost > 0 && buckets[i].Cost > buckets[i-1].Cost*spikeMultiplier {
			spikes = append(spikes, i)
		}
	}
	return spikes
}

// DetectLatencyDegradation 检测延迟退化(比平均高 N 倍)。
func DetectLatencyDegradation(buckets []TimeSeriesBucket, multiplier float64) []int {
	if multiplier <= 0 {
		multiplier = 3.0
	}
	if len(buckets) < 3 {
		return nil
	}
	latencies := make([]float64, len(buckets))
	for i, b := range buckets {
		latencies[i] = b.LatencyAvg
	}
	meanLat := avg(latencies)
	var degraded []int
	for i, l := range latencies {
		if meanLat > 0 && l > meanLat*multiplier {
			degraded = append(degraded, i)
		}
	}
	return degraded
}

// D60 多渠道告警。

// AlertChannel 告警渠道。
type AlertChannel string

const (
	AlertWebhook AlertChannel = "webhook"
	AlertEmail   AlertChannel = "email"
	AlertSlack   AlertChannel = "slack"
)

// AlertNotification 告警通知。
type AlertNotification struct {
	AgentID   string        `json:"agent_id"`
	Rule      string        `json:"rule"`
	Value     float64       `json:"value"`
	Threshold float64       `json:"threshold"`
	Message   string        `json:"message"`
	Severity  string        `json:"severity"`
	Channel   AlertChannel  `json:"channel"`
	Target    string        `json:"target"` // webhook URL / email / slack channel
	Timestamp time.Time     `json:"timestamp"`
}

// SendAlert 发送告警(多渠道)。
func SendAlert(notif AlertNotification) error {
	switch notif.Channel {
	case AlertWebhook:
		return sendWebhookAlert(notif)
	case AlertEmail:
		return sendEmailAlert(notif)
	case AlertSlack:
		return sendSlackAlert(notif)
	}
	return nil
}

// sendWebhookAlert 发送 webhook 告警。
func sendWebhookAlert(notif AlertNotification) error {
	// 构造 webhook payload
	payload, _ := json.Marshal(map[string]any{
		"text":      notif.Message,
		"agent_id":  notif.AgentID,
		"rule":      notif.Rule,
		"value":     notif.Value,
		"threshold": notif.Threshold,
		"severity":  notif.Severity,
		"timestamp": notif.Timestamp.Format(time.RFC3339),
	})
	_ = payload // 真实场景:POST 到 notif.Target
	NewSlog("observability").Info("D60 webhook 告警: →", "Message", notif.Message, "Target", notif.Target)
	return nil
}

func sendEmailAlert(notif AlertNotification) error {
	NewSlog("observability").Info("D60 email 告警: →", "Message", notif.Message, "Target", notif.Target)
	return nil
}

func sendSlackAlert(notif AlertNotification) error {
	NewSlog("observability").Info("D60 slack 告警: →", "Message", notif.Message, "Target", notif.Target)
	return nil
}

// RecordMetricAndCheckAlerts 记录指标并检查告警(一站式)。
func RecordMetricAndCheckAlerts(agentID string, promptTokens, completionTokens int,
	costUSD, latencyMs float64, errorCount int) {

	// 写入 agent_metrics
	store.DB.Exec(`INSERT INTO agent_metrics (id, agent_id, prompt_tokens, completion_tokens,
		cost_usd, latency_ms, error_count, event_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'message')`,
		uuid.New().String(), agentID, promptTokens, completionTokens,
		costUSD, latencyMs, errorCount)

	// 检查告警规则
	checkAlertRulesEnhanced(agentID, costUSD, latencyMs, errorCount)
}

// checkAlertRulesEnhanced 增强版告警检查(含异常检测)。
func checkAlertRulesEnhanced(agentID string, cost, latency float64, errors int) {
	rows, _ := store.DB.Query(`SELECT id, metric, condition, threshold FROM alert_rules WHERE enabled=1 AND (agent_id=? OR agent_id IS NULL OR agent_id='')`, agentID)
	if rows == nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rule struct{ ID, Metric, Condition string; Threshold float64 }
		rows.Scan(&rule.ID, &rule.Metric, &rule.Condition, &rule.Threshold)
		triggered := false
		value := 0.0
		switch rule.Metric {
		case "cost_usd":
			value = cost
		case "latency_ms":
			value = latency
		case "error_count":
			value = float64(errors)
		}
		switch rule.Condition {
		case "gt":
			triggered = value > rule.Threshold
		case "lt":
			triggered = value < rule.Threshold
		case "eq":
			triggered = value == rule.Threshold
		}
		if triggered {
			severity := "warning"
			if value > rule.Threshold*2 {
				severity = "critical"
			}
			msg := fmt.Sprintf("[%s] %s %.2f %s %.2f (%s)", agentID, rule.Metric, value, rule.Condition, rule.Threshold, severity)
			store.DB.Exec(`INSERT INTO alert_events (id, rule_id, agent_id, metric_value, threshold, message)
				VALUES (?, ?, ?, ?, ?, ?)`, uuid.New().String(), rule.ID, agentID, value, rule.Threshold, msg)
			store.DB.Exec(`UPDATE alert_rules SET last_triggered=? WHERE id=?`, time.Now(), rule.ID)

			// D60 发送多渠道告警(简化:只记日志)
			SendAlert(AlertNotification{
				AgentID:   agentID,
				Rule:      rule.ID,
				Value:     value,
				Threshold: rule.Threshold,
				Message:   msg,
				Severity:  severity,
				Channel:   AlertWebhook,
				Target:    "",
				Timestamp: time.Now(),
			})
		}
	}
}

// ── 辅助函数 ──

func stdDev(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sumSq float64
	for _, v := range values {
		sumSq += (v - mean) * (v - mean)
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

// AlertSummary 告警摘要(供 dashboard)。
type AlertSummary struct {
	TotalAlerts    int            `json:"total_alerts"`
	CriticalCount  int            `json:"critical_count"`
	WarningCount   int            `json:"warning_count"`
	RecentAlerts   []map[string]any `json:"recent_alerts"`
	TopAgents      map[string]int `json:"top_agents"` // agent_id → alert count
}

// GetAlertSummary 获取告警摘要。
func GetAlertSummary(hours int) AlertSummary {
	if hours <= 0 {
		hours = 24
	}
	rows, err := store.DB.Query(`SELECT message, triggered_at FROM alert_events
		WHERE triggered_at > datetime('now', '-%d hours') ORDER BY triggered_at DESC LIMIT 20`, hours)
	if err != nil || rows == nil {
		return AlertSummary{}
	}
	defer rows.Close()

	var alerts []map[string]any
	critical := 0
	warning := 0
	for rows.Next() {
		var msg, ts string
		rows.Scan(&msg, &ts)
		alerts = append(alerts, map[string]any{"message": msg, "timestamp": ts})
		if strings.Contains(msg, "critical") {
			critical++
		} else {
			warning++
		}
	}
	return AlertSummary{
		TotalAlerts:   len(alerts),
		CriticalCount: critical,
		WarningCount:  warning,
		RecentAlerts:  alerts,
	}
}
