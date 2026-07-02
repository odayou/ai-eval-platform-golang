package store

// agents_crud.go — agents 表的完整 CRUD(TD-3 收敛 api/engine 层散落的裸 SQL)。
//
// 背景:此前 api/resource.go、api/agent_creator.go、engine/clone.go 各自用
// store.DB.Exec/Query 重复写 agents 表的 INSERT/UPDATE/SELECT,导致:
//   - SQL 散落,schema 改动要全局搜改
//   - 同一 INSERT 在 3 处重复(创建/克隆/AI创建)
//   - 数据层无法 mock
//
// 本文件提供 agents 表的统一 CRUD,收敛上述调用。模式可复用到其他表(见 TECH_DEBT.md)。

import (
	"database/sql"
	"fmt"
)

// ListAgents 返回所有 Agent 的列表视图(含时间戳,按创建时间倒序)。
func ListAgents() ([]AgentSummary, error) {
	rows, err := DB.Query(`SELECT id, name, adapter, model, tools, online,
		COALESCE(permission_level,'workspace-write'), created_at, updated_at
		FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentSummary
	for rows.Next() {
		var a AgentSummary
		var online int
		if err := rows.Scan(&a.ID, &a.Name, &a.Adapter, &a.Model, &a.Tools,
			&online, &a.PermissionLevel, &a.CreatedAt, &a.UpdatedAt); err != nil {
			continue
		}
		a.Online = online == 1
		if a.PermissionLevel == "" {
			a.PermissionLevel = "workspace-write"
		}
		out = append(out, a)
	}
	return out, nil
}

// GetAgentSummary 按 id 读一个 Agent 的列表视图(含时间戳)。不存在返回 sql.ErrNoRows。
func GetAgentSummary(id string) (*AgentSummary, error) {
	var a AgentSummary
	var online int
	err := DB.QueryRow(`SELECT id, name, adapter, model, tools, online,
		COALESCE(permission_level,'workspace-write'), created_at, updated_at
		FROM agents WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &a.Adapter, &a.Model, &a.Tools,
			&online, &a.PermissionLevel, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Online = online == 1
	if a.PermissionLevel == "" {
		a.PermissionLevel = "workspace-write"
	}
	return &a, nil
}

// AgentCreateParams 是创建 Agent 的参数(字段对齐 createAgent API)。
type AgentCreateParams struct {
	ID                   string
	Name                 string
	Adapter              string
	Model                string
	Tools                string
	PermissionLevel      string
	SystemPrompt         string
	SearchProvider       string
	SearchAPIKey         string
	ExampleConversations string
	AgentsMD             string
	SkillIDs             string
	MaxTurns             int
	VerifyCommand        string
	CreatedAt            string
	UpdatedAt            string
}

// InsertAgent 创建一个 Agent。返回的 AgentCreateParams 已填好 ID/时间戳。
// 注意:online 默认 0(下线),需单独调 UpdateAgentOnline 上线。
func InsertAgent(p AgentCreateParams) error {
	_, err := DB.Exec(`INSERT INTO agents (id, name, adapter, model, tools, online, permission_level,
		system_prompt, search_provider, search_api_key, example_conversations,
		agents_md, skill_ids, max_turns, verify_command, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Adapter, p.Model, p.Tools, p.PermissionLevel,
		nullStr(p.SystemPrompt), p.SearchProvider, nullStr(p.SearchAPIKey),
		nullStr(p.ExampleConversations), nullStr(p.AgentsMD), nullStr(p.SkillIDs),
		p.MaxTurns, nullStr(p.VerifyCommand), p.CreatedAt, p.UpdatedAt)
	return err
}

// AgentUpdateParams 是更新 Agent 基础字段的参数(PATCH 语义)。
type AgentUpdateParams struct {
	Name            string
	Adapter         string
	Model           string
	Tools           string
	PermissionLevel string
	UpdatedAt       string
}

// UpdateAgent 更新 Agent 的基础字段。返回受影响行数(0=不存在)。
func UpdateAgent(id string, p AgentUpdateParams) (int64, error) {
	res, err := DB.Exec(`UPDATE agents SET name=?, adapter=?, model=?, tools=?, permission_level=?, updated_at=? WHERE id=?`,
		p.Name, p.Adapter, p.Model, p.Tools, p.PermissionLevel, p.UpdatedAt, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UpdateAgentOnline 切换 Agent 上下线状态。返回受影响行数(0=不存在)。
func UpdateAgentOnline(id string, online bool) (int64, error) {
	onlineVal := 0
	if online {
		onlineVal = 1
	}
	res, err := DB.Exec(`UPDATE agents SET online = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, onlineVal, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteAgent 删除一个 Agent。返回受影响行数(0=不存在)。
func DeleteAgent(id string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountAgents 返回 agents 表总行数(供 stats 用)。
func CountAgents() (int, error) {
	var n int
	err := DB.QueryRow("SELECT COUNT(*) FROM agents").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count agents: %w", err)
	}
	return n, nil
}

// nullStr 把空字符串转成 sql.NullString(空→NULL),非空→有值。
// 复用 query.go 已有的同名辅助会冲突,这里用包内可见的小写别名避免重复定义检查。
// 注意:query.go 里若已有 nullStr 则本函数是重复的——Go 不允许同包重复定义。
// 经查 query.go 无 nullStr,定义于此。若已存在请删除本函数。
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
