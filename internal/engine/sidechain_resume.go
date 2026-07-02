package engine

// sidechain_resume.go — Sidechain stateful resume(good-skills #31 补齐)。
//
// 补齐专家评审:#31 子代理一次性,不能续聊。
// 新增:agentId + 独立 transcript + SendMessage 续聊。
// 与 subagent.go 的 RunSubagent 互补(后者是一次性,本文件加持久化 + resume)。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SubagentRecord 子代理执行记录(支持 resume)。
type SubagentRecord struct {
	AgentID          string            `json:"agent_id"`
	AgentType        string            `json:"agent_type"`
	Description      string            `json:"description"`
	ToolUseID        string            `json:"tool_use_id"`  // 父调用 ID(双向链接)
	PromptID         string            `json:"prompt_id"`    // 同批 fan-out 共享
	ParentSessionID  string            `json:"parent_session_id"`
	IsAlive          bool              `json:"is_alive"`
	Result           string            `json:"result"`       // 4.2.2 子代理执行结果(完成后填充)
	Transcript       []map[string]any  `json:"transcript"`   // sidechain 消息
	CreatedAt        time.Time         `json:"created_at"`
}

// ToolStats 子代理工具调用统计。
type ToolStats struct {
	ReadCount     int `json:"readCount"`
	SearchCount   int `json:"searchCount"`
	BashCount     int `json:"bashCount"`
	EditFileCount int `json:"editFileCount"`
	LinesAdded    int `json:"linesAdded"`
	LinesRemoved  int `json:"linesRemoved"`
	OtherCount    int `json:"otherToolCount"`
}

// SubagentManager 子代理管理器(支持 stateful resume)。
type SubagentManager struct {
	mu         sync.Mutex
	subagents  map[string]*SubagentRecord // agentId → record
	storageDir string
}

// globalSubagentManager 全局子代理管理器。
var globalSubagentManager = NewSubagentManager()

// NewSubagentManager 创建管理器。
func NewSubagentManager() *SubagentManager {
	dir := filepath.Join(os.TempDir(), "ai-eval-subagents")
	os.MkdirAll(dir, 0755)
	return &SubagentManager{
		subagents:  make(map[string]*SubagentRecord),
		storageDir: dir,
	}
}

// GetSubagentManager 获取全局管理器。
func GetSubagentManager() *SubagentManager {
	return globalSubagentManager
}

// SpawnSidechain spawn 一个子代理(创建 sidechain transcript)。
func (m *SubagentManager) SpawnSidechain(agentType, description, prompt, toolUseID, parentSessionID string) *SubagentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()

	agentID := uuid.New().String()[:16]
	promptID := uuid.New().String()[:8]
	rec := &SubagentRecord{
		AgentID:         agentID,
		AgentType:       agentType,
		Description:     description,
		ToolUseID:       toolUseID,
		PromptID:        promptID,
		ParentSessionID: parentSessionID,
		IsAlive:         true,
		Transcript: []map[string]any{
			{
				"isSidechain": true,
				"agentId":     agentID,
				"promptId":    promptID,
				"type":        "user",
				"content":     prompt,
				"sessionId":   parentSessionID,
			},
		},
		CreatedAt: time.Now(),
	}
	m.subagents[agentID] = rec
	m.persist(rec)
	// 4.1.2 写入 JSONL 持久化记录(data/sidechains/{id}.jsonl)
	m.persistJSONL(rec)
	return rec
}

// SendMessage stateful resume:往已存在的子代理发消息续聊。
func (m *SubagentManager) SendMessage(targetAgentID, message string) (*SubagentRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.subagents[targetAgentID]
	if !ok {
		return nil, fmt.Errorf("子代理 %s 不存在", targetAgentID)
	}
	if !rec.IsAlive {
		return nil, fmt.Errorf("子代理 %s 已结束,不可续聊", targetAgentID)
	}
	rec.Transcript = append(rec.Transcript, map[string]any{
		"isSidechain": true,
		"agentId":     targetAgentID,
		"promptId":    rec.PromptID,
		"type":        "user",
		"content":     message,
	})
	m.persist(rec)
	return rec, nil
}

// CompleteSidechain 标记子代理完成。
func (m *SubagentManager) CompleteSidechain(agentID string, result string, stats ToolStats, durationMs int, totalTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.subagents[agentID]
	if !ok {
		return
	}
	rec.IsAlive = false
	rec.Result = result // 4.2.2 保存结果供 GetSidechainResult 轮询
	rec.Transcript = append(rec.Transcript, map[string]any{
		"isSidechain": true,
		"agentId":     agentID,
		"type":        "result",
		"content":     result,
		"toolStats":   stats,
		"duration_ms": durationMs,
		"total_tokens": totalTokens,
	})
	m.persist(rec)
	// 4.1.2 完成时追加一条 JSONL 记录
	m.persistJSONL(rec)
}

// GetPromptGroup 获取同一批 fan-out 的子代理。
func (m *SubagentManager) GetPromptGroup(promptID string) []*SubagentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*SubagentRecord
	for _, rec := range m.subagents {
		if rec.PromptID == promptID {
			result = append(result, rec)
		}
	}
	return result
}

// GetResumeHint 获取 resume 提示(tool_result 末尾附)。
func (m *SubagentManager) GetResumeHint(agentID string, totalTokens, durationMs int) string {
	return fmt.Sprintf("agentId: %s (use SendMessage with to: '%s' to continue this agent)\nsubagent_tokens: %d\nduration_ms: %d",
		agentID, agentID, totalTokens, durationMs)
}

// persist 持久化到磁盘。
func (m *SubagentManager) persist(rec *SubagentRecord) {
	path := filepath.Join(m.storageDir, rec.ParentSessionID, fmt.Sprintf("agent-%s.json", rec.AgentID))
	os.MkdirAll(filepath.Dir(path), 0755)
	data, _ := json.MarshalIndent(rec, "", "  ")
	os.WriteFile(path, data, 0644)
}

// IsAlive 检查子代理是否可续聊。
func (m *SubagentManager) IsAlive(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.subagents[agentID]
	return ok && rec.IsAlive
}

// 4.1.2 persistJSONL 将 sidechain 记录追加到 JSONL 文件(data/sidechains/{id}.jsonl)。
// 每次状态变更(创建/完成)都会追加一行 JSON,便于离线分析子代理执行历史。
func (m *SubagentManager) persistJSONL(rec *SubagentRecord) {
	dir := filepath.Join("data", "sidechains")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, fmt.Sprintf("%s.jsonl", rec.AgentID))

	line, err := json.Marshal(map[string]any{
		"agent_id":    rec.AgentID,
		"agent_type":  rec.AgentType,
		"description": rec.Description,
		"is_alive":    rec.IsAlive,
		"result":      rec.Result,
		"timestamp":   time.Now().Format(time.RFC3339),
	})
	if err != nil {
		NewSlog("sidechain").Error("JSONL 序列化失败", "err", err)
		return
	}
	line = append(line, '\n')

	// 追加写入(不覆盖历史记录)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		NewSlog("sidechain").Error("JSONL 写入失败(path=)", "path", path, "err", err)
		return
	}
	defer f.Close()
	f.Write(line)
}

// 4.2.2 GetSidechainResult 获取子代理执行结果(用于异步轮询)。
// 返回 (结果, 是否已完成)。未完成时返回 ("", false)。
func (m *SubagentManager) GetSidechainResult(agentID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.subagents[agentID]
	if !ok {
		return "", false
	}
	if rec.IsAlive {
		return "", false // 还在执行中
	}
	return rec.Result, true
}
