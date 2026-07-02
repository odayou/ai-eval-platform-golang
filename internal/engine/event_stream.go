package engine

// event_stream.go — 事件流架构(good-skills #43 补齐)。
//
// 补齐专家评审:#43 有 WebSocket 推送(5 种 UI 事件),缺 9 种事件类型 + 持久化 + replay。
// 新增:EventStream + EventStorage(Filesystem 后端) + emit/replay + 9 种事件类型。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EventSourceType 事件来源。
type EventSourceType string

const (
	EventSourceAgent      EventSourceType = "agent"
	EventSourceEnvironment EventSourceType = "environment"
	EventSourceUser       EventSourceType = "user"
	EventSourceHook       EventSourceType = "hook"
)

// AgentEvent 通用事件(9 种 type)。
type AgentEvent struct {
	ID        string            `json:"id"`
	Timestamp string            `json:"timestamp"`
	Source    EventSourceType   `json:"source"`
	Type      string            `json:"type"`
	Data      map[string]any    `json:"data"`
}

// 9 种事件类型常量。
const (
	EvtMessage        = "MessageEvent"
	EvtAction         = "ActionEvent"
	EvtObservation    = "ObservationEvent"
	EvtCondensation   = "CondensationEvent"
	EvtStateUpdate    = "ConversationStateUpdateEvent"
	EvtSystemPrompt   = "SystemPromptEvent"
	EvtPause          = "PauseEvent"
	EvtHookExecution  = "HookExecutionEvent"
	EvtACPToolCall    = "ACPToolCallEvent"
)

// AllEventTypes 全部 9 种事件类型。
var AllEventTypes = []string{
	EvtMessage, EvtAction, EvtObservation, EvtCondensation, EvtStateUpdate,
	EvtSystemPrompt, EvtPause, EvtHookExecution, EvtACPToolCall,
}

// EventStorage 事件存储后端(可插拔)。
type EventStorage interface {
	Store(conversationID string, event AgentEvent) error
	LoadAll(conversationID string) ([]AgentEvent, error)
}

// FilesystemEventStorage 文件系统事件存储(每事件一个 JSON 文件)。
type FilesystemEventStorage struct {
	BaseDir string
}

// NewFilesystemEventStorage 创建文件系统存储。
func NewFilesystemEventStorage(baseDir string) *FilesystemEventStorage {
	return &FilesystemEventStorage{BaseDir: baseDir}
}

func (s *FilesystemEventStorage) Store(conversationID string, event AgentEvent) error {
	dir := filepath.Join(s.BaseDir, conversationID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, event.ID+".json")
	data, _ := json.MarshalIndent(event, "", "  ")
	return os.WriteFile(path, data, 0644)
}

func (s *FilesystemEventStorage) LoadAll(conversationID string) ([]AgentEvent, error) {
	dir := filepath.Join(s.BaseDir, conversationID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var events []AgentEvent
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var event AgentEvent
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	// 按时间戳排序
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})
	return events, nil
}

// EventStream 事件流管理器。
type EventStream struct {
	mu      sync.Mutex
	storage EventStorage
}

// NewEventStream 创建事件流。
func NewEventStream(storage EventStorage) *EventStream {
	return &EventStream{storage: storage}
}

// Emit 发射事件(存储 + 返回)。
func (s *EventStream) Emit(conversationID string, eventType string, source EventSourceType, data map[string]any) AgentEvent {
	event := AgentEvent{
		ID:        uuid.New().String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    source,
		Type:      eventType,
		Data:      data,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storage != nil {
		s.storage.Store(conversationID, event)
	}
	return event
}

// EmitMessage 发射 MessageEvent。
func (s *EventStream) EmitMessage(convID, message string) AgentEvent {
	return s.Emit(convID, EvtMessage, EventSourceAgent, map[string]any{"llm_message": message})
}

// EmitAction 发射 ActionEvent(工具调用)。
func (s *EventStream) EmitAction(convID, action string, args map[string]any) AgentEvent {
	return s.Emit(convID, EvtAction, EventSourceAgent, map[string]any{"action": action, "args": args})
}

// EmitObservation 发射 ObservationEvent(工具结果)。
func (s *EventStream) EmitObservation(convID, observation string, isError bool) AgentEvent {
	return s.Emit(convID, EvtObservation, EventSourceEnvironment,
		map[string]any{"observation": observation, "is_error": isError})
}

// EmitStateUpdate 发射 ConversationStateUpdateEvent。
func (s *EventStream) EmitStateUpdate(convID, key string, value any) AgentEvent {
	return s.Emit(convID, EvtStateUpdate, EventSourceEnvironment,
		map[string]any{"key": key, "value": value})
}

// EmitHookExecution 发射 HookExecutionEvent。
func (s *EventStream) EmitHookExecution(convID, event, hookOutput string, blocked bool, exitCode int) AgentEvent {
	return s.Emit(convID, EvtHookExecution, EventSourceHook,
		map[string]any{"event": event, "stdout": hookOutput, "blocked": blocked, "exit_code": exitCode})
}

// Replay 回放事件流。
func (s *EventStream) Replay(conversationID string) ([]AgentEvent, error) {
	if s.storage == nil {
		return nil, fmt.Errorf("no storage backend")
	}
	return s.storage.LoadAll(conversationID)
}

// globalEventStream 全局事件流(默认文件系统存储)。
var globalEventStream = NewEventStream(NewFilesystemEventStorage(
	filepath.Join(os.TempDir(), "ai-eval-events")))

// GetEventStream 获取全局事件流。
func GetEventStream() *EventStream {
	return globalEventStream
}
