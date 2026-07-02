package store

// rules_crud.go — rules 表的 CRUD(TD-3 收敛 api 层散落的裸 SQL)。
//
// 与 agents/tasks/models/questions 同模式。已有的 GetAllRules/GetRulesByIDs/
// GetRuleSet(query.go)继续保留,本文件补 List(含时间戳)/Insert/Update/Delete/Count。
//
// 注意:INSERT 此前在 4 处重复(createRule/feedback/ruleio×2),本文件统一。

import (
	"fmt"
)

// ListRules 返回所有规则(含时间戳,按创建时间倒序)。供 api 层列表用。
func ListRules() ([]RuleRow, error) {
	rows, err := DB.Query(`SELECT id, name, dimension, scoring, weight, content,
		COALESCE(tags,''), created_at FROM rules ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleRow
	for rows.Next() {
		var r RuleRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Dimension, &r.Scoring, &r.Weight,
			&r.Content, &r.Tags, &r.CreatedAt); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// RuleCreateParams 创建规则参数。
type RuleCreateParams struct {
	ID        string
	Name      string
	Dimension string
	Scoring   string
	Weight    float64
	Content   string
	Tags      string // 允许空
	CreatedAt string
}

// InsertRule 创建一个规则。统一 createRule/feedback/ruleio 的重复 INSERT。
func InsertRule(p RuleCreateParams) error {
	_, err := DB.Exec(`INSERT INTO rules (id, name, dimension, scoring, weight, content, tags, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Dimension, p.Scoring, p.Weight, p.Content, nullStr(p.Tags), p.CreatedAt)
	return err
}

// InsertRuleIgnore 创建规则,已存在则忽略(INSERT OR IGNORE)。
// 供 ruleio 批量导入用(重复 id 跳过而非报错)。
func InsertRuleIgnore(p RuleCreateParams) error {
	_, err := DB.Exec(`INSERT OR IGNORE INTO rules (id, name, dimension, scoring, weight, content, tags, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Dimension, p.Scoring, p.Weight, p.Content, nullStr(p.Tags), p.CreatedAt)
	return err
}

// RuleUpdateParams 更新规则参数(PATCH 语义)。
type RuleUpdateParams struct {
	Name      string
	Dimension string
	Scoring   string
	Weight    float64
	Content   string
	Tags      string
}

// UpdateRule 更新规则。返回受影响行数(0=不存在)。
func UpdateRule(id string, p RuleUpdateParams) (int64, error) {
	res, err := DB.Exec(`UPDATE rules SET name=?, dimension=?, scoring=?, weight=?, content=?, tags=? WHERE id=?`,
		p.Name, p.Dimension, p.Scoring, p.Weight, p.Content, nullStr(p.Tags), id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteRule 删除规则。返回受影响行数(0=不存在)。
func DeleteRule(id string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountRules 返回 rules 表总行数。
func CountRules() (int, error) {
	var n int
	err := DB.QueryRow("SELECT COUNT(*) FROM rules").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count rules: %w", err)
	}
	return n, nil
}
