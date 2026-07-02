package store

// models_crud.go — models 表的完整 CRUD(TD-3 收敛 api/engine 层散落的裸 SQL)。
//
// 与 agents_crud.go / tasks_crud.go 同模式。
//
// 注意(api_key 加密):models 表的 api_key 字段是加密存储的(由 engine.EncryptSecret
// 在 api 层加密后写入)。本文件的 InsertModel/UpdateModel 接收的 APIKey 参数应为
// **已加密**的值——加密逻辑留在 api 层,store 层不依赖 engine 包,保持分层纯净。

import (
	"database/sql"
	"fmt"
)

// ModelRow 对应 models 表一行。
// 注意:APIKey 是加密后的值(store 层不解密)。
type ModelRow struct {
	ID        string
	Name      string
	BaseURL   string
	APIKey    string // 加密后的值
	APIFormat string
	Available bool
	CreatedAt string
	UpdatedAt string
}

// ListModels 返回所有模型(含 api_key,供 engine 健康检查/路由用)。按创建时间倒序。
// ⚠️ 返回的 APIKey 是加密值,api 层对外暴露时不应返回此字段。
func ListModels() ([]ModelRow, error) {
	rows, err := DB.Query(`SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,''),
		COALESCE(available,0), created_at, updated_at FROM models ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelRow
	for rows.Next() {
		m, err := scanModelRow(rows)
		if err != nil {
			continue
		}
		out = append(out, *m)
	}
	return out, nil
}

// ListModelsPublic 返回模型列表(不含 api_key,供 api 层 List/Get 暴露给前端用)。
func ListModelsPublic() ([]ModelRow, error) {
	rows, err := DB.Query(`SELECT id, name, base_url, '', COALESCE(api_format,''),
		COALESCE(available,0), created_at, updated_at FROM models ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelRow
	for rows.Next() {
		m, err := scanModelRow(rows)
		if err != nil {
			continue
		}
		out = append(out, *m)
	}
	return out, nil
}

// GetModelPublic 按 id 读模型(不含 api_key,供 api 层用)。
func GetModelPublic(id string) (*ModelRow, error) {
	var m ModelRow
	var avail int
	err := DB.QueryRow(`SELECT id, name, base_url, '', COALESCE(api_format,''),
		COALESCE(available,0), created_at, updated_at FROM models WHERE id = ?`, id).
		Scan(&m.ID, &m.Name, &m.BaseURL, &m.APIKey, &m.APIFormat, &avail, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	m.Available = avail == 1
	return &m, nil
}

// scanModelRow 从 rows(当前行)扫描 ModelRow。
func scanModelRow(rows *sql.Rows) (*ModelRow, error) {
	var m ModelRow
	var avail int
	err := rows.Scan(&m.ID, &m.Name, &m.BaseURL, &m.APIKey, &m.APIFormat, &avail, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	m.Available = avail == 1
	return &m, nil
}

// ModelCreateParams 创建模型参数。APIKey 应为**已加密**的值。
type ModelCreateParams struct {
	ID        string
	Name      string
	BaseURL   string
	APIKey    string // 已加密
	APIFormat string
	CreatedAt string
	UpdatedAt string
}

// InsertModel 创建一个模型(available 默认 0)。APIKey 应为已加密值。
func InsertModel(p ModelCreateParams) error {
	_, err := DB.Exec(`INSERT INTO models (id, name, base_url, api_key, api_format, available, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
		p.ID, p.Name, p.BaseURL, p.APIKey, p.APIFormat, p.CreatedAt, p.UpdatedAt)
	return err
}

// ModelUpdateParams 更新模型参数。APIKey 应为已加密值。
type ModelUpdateParams struct {
	Name      string
	BaseURL   string
	APIKey    string // 已加密
	APIFormat string
	UpdatedAt string
}

// UpdateModel 更新模型基础字段。返回受影响行数(0=不存在)。
func UpdateModel(id string, p ModelUpdateParams) (int64, error) {
	res, err := DB.Exec(`UPDATE models SET name=?, base_url=?, api_key=?, api_format=?, updated_at=? WHERE id=?`,
		p.Name, p.BaseURL, p.APIKey, p.APIFormat, p.UpdatedAt, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteModel 删除模型。返回受影响行数(0=不存在)。
func DeleteModel(id string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM models WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountModels 返回 models 表总行数。
func CountModels() (int, error) {
	var n int
	err := DB.QueryRow("SELECT COUNT(*) FROM models").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count models: %w", err)
	}
	return n, nil
}
