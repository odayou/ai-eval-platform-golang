// Package ws 提供评测进度的 WebSocket 实时推送(MRD v2 10.2 虚拟化终端日志矩阵)。
//
// 架构:
//   - Hub 管理 taskID → 订阅者列表的映射
//   - engine.Run 逐题发布 EvalEvent 到 Hub.Publish
//   - WebSocket handler 连接时 Subscribe,断开时 Unsubscribe
//   - 每个订阅者有自己的 channel(Hub 负责广播到所有 channel)
//
// 事件类型:
//   question_start  — 开始评测第 N 题
//   agent_response  — Agent 返回了回答
//   question_done  — 第 N 题评测完成(含分数)
//   task_done      — 整个任务评测完成(含报告摘要)
//   task_error     — 评测出错
package ws

import (
	"encoding/json"
	"log"
	"sync"

	"ai-eval-platform/internal/store"
)

// EvalEvent 是一次评测进度事件。
type EvalEvent struct {
	Type string          `json:"type"` // question_start | agent_response | question_done | task_done | task_error
	Data json.RawMessage `json:"data"` // 事件数据(各类型结构不同)
}

// QuestionStartData 开始评测第 N 题。
type QuestionStartData struct {
	TaskID string `json:"task_id"`
	Turn   int    `json:"turn"`   // 第几题(1-based)
	Total  int    `json:"total"`  // 总题数
	Title  string `json:"title"`  // 题目摘要
}

// AgentResponseData Agent 回答完成。
type AgentResponseData struct {
	TaskID        string `json:"task_id"`
	Turn          int    `json:"turn"`
	AnswerPreview string `json:"answer_preview"` // 回答前200字
	TokenIn       int    `json:"token_in"`
	TokenOut      int    `json:"token_out"`
}

// QuestionDoneData 单题评测完成。
type QuestionDoneData struct {
	TaskID    string  `json:"task_id"`
	Turn      int     `json:"turn"`
	Score     float64 `json:"score"`      // 该题加权总分
	IsBadCase bool    `json:"is_bad_case"` // 是否 Bad Case
}

// TaskDoneData 任务评测完成。
type TaskDoneData struct {
	TaskID     string             `json:"task_id"`
	Overall    float64            `json:"overall"`
	Dimensions map[string]float64 `json:"dimensions"`
	BadCases   int                `json:"bad_cases"`
	MockMode   bool               `json:"mock_mode"`
}

// BreakerStatusData 断路器状态推送(7.5.2)。
// 每题评测后实时推送,前端可展示 token 消耗进度条和费用预算。
type BreakerStatusData struct {
	TaskID      string  `json:"task_id"`
	Turn        int     `json:"turn"`         // 当前题号
	TotalTokens int     `json:"total_tokens"` // 累计 token 消耗
	TotalTurns  int     `json:"total_turns"`  // 累计轮数
	EstCostUSD  float64 `json:"est_cost_usd"` // 估算费用(美元)
	Tripped     bool    `json:"tripped"`      // 是否已熔断
}

// listener 是单个订阅者(一个 WebSocket 连接)。
type listener struct {
	taskID string
	ch     chan EvalEvent
}

// Hub 是事件广播中心(进程单例)。
type Hub struct {
	mu        sync.RWMutex
	listeners map[string][]*listener // taskID → 订阅者列表
}

// GlobalHub 是进程级单例。
var GlobalHub = &Hub{
	listeners: make(map[string][]*listener),
}

// Subscribe 注册一个订阅者,返回接收事件的 channel。
// taskID 为空时订阅所有任务(看板用),不为空时只收该任务的事件。
func (h *Hub) Subscribe(taskID string) chan EvalEvent {
	ch := make(chan EvalEvent, 64) // 缓冲 64 个事件,防慢客户端阻塞
	l := &listener{taskID: taskID, ch: ch}

	h.mu.Lock()
	h.listeners[taskID] = append(h.listeners[taskID], l)
	h.mu.Unlock()

	log.Printf("[ws] subscribe task=%s total_listeners=%d", taskID, len(h.listeners[taskID]))
	return ch
}

// Unsubscribe 取消订阅,关闭 channel。
func (h *Hub) Unsubscribe(taskID string, ch chan EvalEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	list := h.listeners[taskID]
	for i, l := range list {
		if l.ch == ch {
			h.listeners[taskID] = append(list[:i], list[i+1:]...)
			close(ch)
			break
		}
	}
	// 清理空列表
	if len(h.listeners[taskID]) == 0 {
		delete(h.listeners, taskID)
	}
}

// Publish 向某任务的所有订阅者广播事件。
// 同时向 taskID="" 的全局订阅者广播(用于看板实时更新)。
func (h *Hub) Publish(taskID string, ev EvalEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// 广播给该任务的订阅者
	for _, l := range h.listeners[taskID] {
		select {
		case l.ch <- ev:
		default:
			// 客户端太慢,丢弃事件(反压)
		}
	}

	// 广播给全局订阅者(taskID="")
	for _, l := range h.listeners[""] {
		select {
		case l.ch <- ev:
		default:
		}
	}
}

// Emit 便捷方法:构造 EvalEvent 并发布。
// 审计埋点 D:事件流镜像落库(完全白盒,记录所有运行时事件)。
func Emit(taskID, eventType string, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Printf("[ws] marshal error for %s: %v", eventType, err)
		return
	}
	GlobalHub.Publish(taskID, EvalEvent{
		Type: eventType,
		Data: json.RawMessage(b),
	})
	// 审计埋点 D:镜像落库(失败仅 log,不影响推送)
	if taskID != "" && store.DB != nil {
		store.InsertAuditLog(store.AuditLogRow{
			TaskID:    taskID,
			Kind:      "event",
			Caller:    "event_" + eventType,
			InputText: string(b),
		})
	}
}
