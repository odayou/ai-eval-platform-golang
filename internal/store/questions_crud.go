package store

// questions_crud.go — questions 表的 CRUD(TD-3 收敛 api/engine 层散落的裸 SQL)。
//
// 与 agents/tasks/models 同模式。已有的 GetQuestionsByScene/GetQuestionsBySetID/
// CountQuestions(query.go)继续保留,本文件补 Insert/Get/Update/Delete。
//
// 注意:listQuestions 是动态多条件查询(WHERE 拼装),保留在 api 层(收敛价值低)。

import (
	"database/sql"
)

// GetQuestion 按 id 读一个题目(全字段)。不存在返回 sql.ErrNoRows。
func GetQuestion(id string) (*QuestionRow, error) {
	var q QuestionRow
	var tags, expected, ruleSetID, sourceRepo, sourceURL sql.NullString
	var desensitized int
	err := DB.QueryRow(`SELECT id, scene, title, prompt, COALESCE(expected,''), difficulty, source,
		COALESCE(tags,''), COALESCE(rule_set_id,''), COALESCE(source_repo,''), COALESCE(source_url,''),
		COALESCE(desensitized,0), created_at FROM questions WHERE id = ?`, id).
		Scan(&q.ID, &q.Scene, &q.Title, &q.Prompt, &expected, &q.Difficulty, &q.Source,
			&tags, &ruleSetID, &sourceRepo, &sourceURL, &desensitized, &q.CreatedAt)
	if err != nil {
		return nil, err
	}
	q.Expected = expected.String
	q.Tags = tags.String
	q.RuleSetID = ruleSetID.String
	q.SourceRepo = sourceRepo.String
	q.SourceURL = sourceURL.String
	q.Desensitized = desensitized == 1
	return &q, nil
}

// QuestionCreateParams 创建题目参数。
type QuestionCreateParams struct {
	ID         string
	Scene      string
	Title      string
	Prompt     string
	Expected   string // 允许空
	Difficulty string
	Source     string
	Tags       string // 允许空
	RuleSetID  string // 允许空
	CreatedAt  string
}

// InsertQuestion 创建一个题目。统一 question.go/baseline.go/questiongen.go 的重复 INSERT。
func InsertQuestion(p QuestionCreateParams) error {
	_, err := DB.Exec(`INSERT INTO questions
		(id, scene, title, prompt, expected, difficulty, source, tags, rule_set_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Scene, p.Title, p.Prompt, nullStr(p.Expected),
		p.Difficulty, p.Source, nullStr(p.Tags), nullStr(p.RuleSetID), p.CreatedAt)
	return err
}

// QuestionUpdateParams 更新题目参数(PATCH 语义,全字段覆盖)。
type QuestionUpdateParams struct {
	Scene      string
	Title      string
	Prompt     string
	Expected   string
	Difficulty string
	Source     string
	Tags       string
	RuleSetID  string
}

// UpdateQuestion 更新题目。返回受影响行数(0=不存在)。
func UpdateQuestion(id string, p QuestionUpdateParams) (int64, error) {
	res, err := DB.Exec(`UPDATE questions SET scene=?, title=?, prompt=?, expected=?,
		difficulty=?, source=?, tags=?, rule_set_id=? WHERE id=?`,
		p.Scene, p.Title, p.Prompt, nullStr(p.Expected), p.Difficulty,
		p.Source, nullStr(p.Tags), nullStr(p.RuleSetID), id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteQuestion 删除题目。返回受影响行数(0=不存在)。
func DeleteQuestion(id string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM questions WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
