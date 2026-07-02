package engine

// todo.go — TodoWrite 结构化任务列表(good-skills #35 实现)。
//
// 补齐专家评审:#35 未实现。agent 无结构化任务列表。
// TodoWrite 让 agent 管理任务(JSON 数组 content/status/activeForm),
// 最多一个 in_progress + 原子写(tmp-rename)。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TodoStatus 任务状态。
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// TodoItem 一条任务。
type TodoItem struct {
	Content    string     `json:"content"`
	Status     TodoStatus `json:"status"`
	ActiveForm string     `json:"activeForm"`
}

// TodoStore 任务存储(按 session+agent 隔离)。
type TodoStore struct {
	mu    sync.Mutex
	path  string
	items []TodoItem
}

// todoStores 全局 todo 存储(按 sessionID-agentID 索引)。
var (
	todoStoresMu sync.Mutex
	todoStores   = make(map[string]*TodoStore)
)

// getTodoStore 获取(或创建)todo 存储。
func getTodoStore(sessionID, agentID string) *TodoStore {
	key := sessionID + "-" + agentID
	todoStoresMu.Lock()
	defer todoStoresMu.Unlock()
	if store, ok := todoStores[key]; ok {
		return store
	}
	store := &TodoStore{
		path: filepath.Join(os.TempDir(), "ai-eval-todos",
			fmt.Sprintf("%s-agent-%s.json", sessionID, agentID)),
	}
	store.load()
	todoStores[key] = store
	return store
}

// load 从磁盘加载。
func (s *TodoStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.items)
}

// save 原子写(tmp-rename)。
func (s *TodoStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(s.items)
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Update 更新整个 todo 列表(每次调传完整列表)。
// 约束:最多一个 in_progress。
func (s *TodoStore) Update(items []TodoItem) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errors []string
	// 约束检查:最多一个 in_progress
	inProgress := 0
	for _, t := range items {
		if t.Status == TodoInProgress {
			inProgress++
		}
		// 自动生成 activeForm
		if t.ActiveForm == "" && t.Status == TodoInProgress {
			t.ActiveForm = "正在" + t.Content
		}
	}
	if inProgress > 1 {
		errors = append(errors, fmt.Sprintf("最多一个 in_progress(当前 %d 个)", inProgress))
	}

	s.items = items
	if err := s.save(); err != nil {
		errors = append(errors, "持久化失败: "+err.Error())
	}
	return errors
}

// Next 推进:完成当前 in_progress,启动下一个 pending。
func (s *TodoStore) Next() *TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.items {
		if t.Status == TodoInProgress {
			s.items[i].Status = TodoCompleted
			break
		}
	}
	for i, t := range s.items {
		if t.Status == TodoPending {
			s.items[i].Status = TodoInProgress
			s.items[i].ActiveForm = "正在" + t.Content
			s.save()
			return &s.items[i]
		}
	}
	s.save()
	return nil
}

// Render 渲染为可读文本。
func (s *TodoStore) Render() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return "(无任务)"
	}
	var sb strings.Builder
	for _, t := range s.items {
		switch t.Status {
		case TodoInProgress:
			sb.WriteString("🔄 ")
		case TodoCompleted:
			sb.WriteString("✅ ")
		default:
			sb.WriteString("⏳ ")
		}
		sb.WriteString(t.Content)
		if t.ActiveForm != "" && t.Status == TodoInProgress {
			sb.WriteString(" (")
			sb.WriteString(t.ActiveForm)
			sb.WriteString(")")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// TodoWriteToolDef TodoWrite 工具声明。
func TodoWriteToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "TodoWrite",
			Description: "管理结构化任务列表。每次调传完整列表(replaces previous)。约束:最多一个 in_progress。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"todos": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"content": {"type": "string", "description": "任务描述"},
								"status": {"type": "string", "enum": ["pending", "in_progress", "completed"]},
								"activeForm": {"type": "string", "description": "进行时态短语(UI 显示)"}
							},
							"required": ["content", "status"]
						}
					}
				},
				"required": ["todos"]
			}`),
		},
	}
}

// ExecuteTodoWrite 执行 TodoWrite 工具。
func ExecuteTodoWrite(args json.RawMessage, sessionID, agentID string) ToolResult {
	var argsMap struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return ToolResult{Name: "TodoWrite", Output: "参数解析失败", IsError: true}
	}
	store := getTodoStore(sessionID, agentID)
	items := make([]TodoItem, len(argsMap.Todos))
	for i, t := range argsMap.Todos {
		items[i] = TodoItem{Content: t.Content, Status: TodoStatus(t.Status), ActiveForm: t.ActiveForm}
	}
	errors := store.Update(items)
	if len(errors) > 0 {
		return ToolResult{Name: "TodoWrite", Output: strings.Join(errors, "; "), IsError: true}
	}
	return ToolResult{Name: "TodoWrite", Output: "任务列表已更新:\n" + store.Render()}
}
