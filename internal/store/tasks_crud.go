package store

// tasks_crud.go — tasks 表的完整 CRUD(TD-3 收敛 api 层散落的裸 SQL)。
//
// 与 agents_crud.go 同模式:补齐 List/Insert/Delete/Count/Stats,消除 api/task.go、
// report_export.go、stats.go 里重复的 tasks 表裸 SQL。
//
// 已有的 GetTask/UpdateTaskStatus/UpdateTaskResult(query.go)继续保留,
// 本文件只补缺失的部分。

import (
	"database/sql"
	"fmt"
)

// ListTasks 返回所有任务的列表视图(含时间戳,按创建时间倒序)。
func ListTasks() ([]TaskSummary, error) {
	rows, err := DB.Query(`SELECT id, name, scene, agent_id, rule_set, status, result,
		COALESCE(chaos_enabled,0), COALESCE(expert_group_id,''), created_at, updated_at
		FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskSummary
	for rows.Next() {
		t, err := scanTaskSummary(rows)
		if err != nil {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}

// GetTaskSummary 按 id 读一个任务的列表视图(含时间戳)。不存在返回 sql.ErrNoRows。
func GetTaskSummary(id string) (*TaskSummary, error) {
	var t TaskSummary
	var agentID, ruleSet, result, expertGroupID sql.NullString
	var chaosEnabled int
	err := DB.QueryRow(`SELECT id, name, scene, agent_id, rule_set, status, result,
		COALESCE(chaos_enabled,0), COALESCE(expert_group_id,''), created_at, updated_at
		FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Scene, &agentID, &ruleSet, &t.Status, &result,
			&chaosEnabled, &expertGroupID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.AgentID = agentID.String
	t.RuleSet = ruleSet.String
	t.Result = result.String
	t.ChaosEnabled = chaosEnabled == 1
	t.ExpertGroupID = expertGroupID.String
	return &t, nil
}

// scanTaskSummary 从 rows(当前行)扫描出 TaskSummary。供 ListTasks 复用。
func scanTaskSummary(rows *sql.Rows) (*TaskSummary, error) {
	var t TaskSummary
	var agentID, ruleSet, result, expertGroupID sql.NullString
	var chaosEnabled int
	err := rows.Scan(&t.ID, &t.Name, &t.Scene, &agentID, &ruleSet, &t.Status, &result,
		&chaosEnabled, &expertGroupID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.AgentID = agentID.String
	t.RuleSet = ruleSet.String
	t.Result = result.String
	t.ChaosEnabled = chaosEnabled == 1
	t.ExpertGroupID = expertGroupID.String
	return &t, nil
}

// TaskCreateParams 是创建任务的参数。Status 固定为 "pending"(创建时刚入队)。
type TaskCreateParams struct {
	ID            string
	Name          string
	Scene         string
	AgentID       string // 允许空
	RuleSet       string // 允许空
	ChaosEnabled  bool
	ExpertGroupID string // 允许空
	CreatedAt     string
	UpdatedAt     string
}

// InsertTask 创建一个任务(status='pending')。统一 createTask/rerunTask 的重复 INSERT。
func InsertTask(p TaskCreateParams) error {
	chaosVal := 0
	if p.ChaosEnabled {
		chaosVal = 1
	}
	_, err := DB.Exec(`INSERT INTO tasks (id, name, scene, agent_id, rule_set, status, chaos_enabled, expert_group_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
		p.ID, p.Name, p.Scene, nullStr(p.AgentID), nullStr(p.RuleSet),
		chaosVal, nullStr(p.ExpertGroupID), p.CreatedAt, p.UpdatedAt)
	return err
}

// DeleteTask 删除一个任务。返回受影响行数(0=不存在)。
func DeleteTask(id string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountTasks 返回 tasks 表总行数。
func CountTasks() (int, error) {
	var n int
	err := DB.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return n, nil
}

// TaskStatusStat 是按 status 分组的任务计数(供仪表盘用)。
type TaskStatusStat struct {
	Status string
	Count  int
}

// TaskStatusStats 按 status 分组统计任务数。
func TaskStatusStats() ([]TaskStatusStat, error) {
	rows, err := DB.Query("SELECT status, COUNT(*) FROM tasks GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskStatusStat
	for rows.Next() {
		var s TaskStatusStat
		if err := rows.Scan(&s.Status, &s.Count); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
