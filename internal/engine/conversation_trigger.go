package engine

// conversation_trigger.go — 会话触发源(skill #51)。
// 11 种 trigger 枚举 + 从 tags 推断 trigger。

import "strings"

// ConversationTrigger 会话触发源。
type ConversationTrigger string

const (
	TriggerResolver   ConversationTrigger = "resolver"    // GitHub/GitLab issue resolver
	TriggerGUI        ConversationTrigger = "gui"         // Web GUI
	TriggerSlack      ConversationTrigger = "slack"       // Slack 集成
	TriggerJira       ConversationTrigger = "jira"        // Jira 集成
	TriggerLinear     ConversationTrigger = "linear"      // Linear 集成
	TriggerCLI        ConversationTrigger = "cli"         // 命令行
	TriggerAPI        ConversationTrigger = "api"         // REST API
	TriggerSDK        ConversationTrigger = "sdk"         // SDK 接入
	TriggerCron       ConversationTrigger = "cron"        // 定时任务
	TriggerWebhook    ConversationTrigger = "webhook"     // Webhook
	TriggerManual     ConversationTrigger = "manual"      // 手动触发
)

// InferTrigger 从 tags 推断 trigger 类型。
func InferTrigger(tags []string) ConversationTrigger {
	tagSet := make(map[string]bool)
	for _, t := range tags {
		tagSet[strings.ToLower(t)] = true
	}

	if tagSet["github"] || tagSet["gitlab"] || tagSet["pr"] {
		return TriggerResolver
	}
	if tagSet["slack"] {
		return TriggerSlack
	}
	if tagSet["jira"] {
		return TriggerJira
	}
	if tagSet["linear"] {
		return TriggerLinear
	}
	if tagSet["web"] || tagSet["dashboard"] {
		return TriggerGUI
	}
	if tagSet["cron"] || tagSet["scheduled"] {
		return TriggerCron
	}
	if tagSet["webhook"] {
		return TriggerWebhook
	}
	if tagSet["api"] {
		return TriggerAPI
	}
	if tagSet["sdk"] {
		return TriggerSDK
	}
	if tagSet["cli"] || tagSet["terminal"] {
		return TriggerCLI
	}
	return TriggerManual
}

// TriggerLabel 返回 trigger 的中文标签。
func TriggerLabel(t ConversationTrigger) string {
	labels := map[ConversationTrigger]string{
		TriggerResolver: "GitHub/GitLab Resolver",
		TriggerGUI:      "Web 界面",
		TriggerSlack:    "Slack 集成",
		TriggerJira:     "Jira 集成",
		TriggerLinear:   "Linear 集成",
		TriggerCLI:      "命令行",
		TriggerAPI:      "REST API",
		TriggerSDK:      "SDK 接入",
		TriggerCron:     "定时任务",
		TriggerWebhook:  "Webhook",
		TriggerManual:   "手动触发",
	}
	if label, ok := labels[t]; ok {
		return label
	}
	return string(t)
}
