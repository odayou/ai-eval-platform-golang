package engine

// agent_status.go — Agent 运行状态机(借鉴 claw-code worker_boot 状态机,见 good-skills/12)。
//
// claw-code 的 WorkerStatus 状态机:Spawning → TrustRequired → ReadyForPrompt → Running → Finished
// 本项目简化为面向"状态显示"的模型(前端像 Codex 那样实时看到 agent 在干什么):
//
//   spawning      — Agent 启动中(查端点/初始化)
//   thinking      — LLM 推理中(调 /chat/completions)
//   calling_tool  — 调用工具中(BashTool/FileEdit/...)
//   reading_file  — 读文件中
//   searching     — 联网搜索中(Brave/Tavily)
//   done          — 完成
//   error         — 出错
//
// 状态写两处:agent_runs 表(持久,供 /api/agents/:id/status 查询)+ WebSocket(实时推送)。

import (
	"fmt"

	"ai-eval-platform/internal/store"
	"ai-eval-platform/internal/ws"

	"github.com/google/uuid"
)

// AgentStatus Agent 运行状态。
type AgentStatus string

const (
	StatusSpawning    AgentStatus = "spawning"
	StatusThinking    AgentStatus = "thinking"
	StatusCallingTool AgentStatus = "calling_tool"
	StatusReadingFile AgentStatus = "reading_file"
	StatusSearching   AgentStatus = "searching"
	StatusDone        AgentStatus = "done"
	StatusError       AgentStatus = "error"
)

// statusEmoji 状态对应的 emoji(前端显示用)。
var statusEmoji = map[AgentStatus]string{
	StatusSpawning:    "🌱",
	StatusThinking:    "🤔",
	StatusCallingTool: "🔧",
	StatusReadingFile: "📖",
	StatusSearching:   "🔍",
	StatusDone:        "✅",
	StatusError:       "❌",
}

// StatusEmoji 返回状态 emoji。
func StatusEmoji(s string) string {
	return statusEmoji[AgentStatus(s)]
}

// SetAgentStatus 设置 Agent 当前运行状态。
//
// 写两处:
//  1. agent_runs 表(upsert,供 /api/agents/:id/status 查询)
//  2. WebSocket(实时推送给前端,taskID 作为频道)
//
// taskID 为空时不推 WebSocket(如创造 Agent 时还没有 task)。
func SetAgentStatus(agentID string, status AgentStatus, detail, taskID string) {
	// 1. 写库
	if store.DB != nil {
		store.SetAgentRun(uuid.New().String(), agentID, string(status), detail)
	}

	// 2. 推 WebSocket(复用 ws.GlobalHub,按 taskID 频道推送)
	if taskID != "" && ws.GlobalHub != nil {
		ws.GlobalHub.Publish(taskID, ws.EvalEvent{
			Type: "agent_status",
			Data: []byte(fmt.Sprintf(`{"agent_id":%q,"status":%q,"detail":%q,"emoji":%q}`,
				agentID, status, detail, statusEmoji[status])),
		})
	}

	// 3. statusline 接入(skill #38):如果配置了 statusLine 命令,推送状态栏文本
	// FormatStatusLine 是纯 Go 内置格式,无外部命令依赖
	_ = FormatStatusLine("", 0, 0, taskID) // 预留:未来可接入自定义命令
}
