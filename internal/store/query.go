package store

// query.go — 供 engine 层使用的查询辅助函数。
//
// 这些函数封装了常用查询,让 engine 不直接写 SQL,保持职责清晰。
// handler(api 包)仍直接用 store.DB.Query,这两套并存不冲突。

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// GetAgent 按 id 读一个 Agent。不存在返回 error。
func GetAgent(id string) (*AgentRow, error) {
	var a AgentRow
	var online int
	var permLevel, sysPrompt, searchProvider, searchAPIKey, exampleConv, agentsMD, skillIDs, verifyCmd sql.NullString
	var maxTurns sql.NullInt64
	err := DB.QueryRow(`SELECT id, name, COALESCE(adapter,'api'), COALESCE(model,''),
		COALESCE(tools,''), online, COALESCE(permission_level,'workspace-write'),
		COALESCE(system_prompt,''), COALESCE(search_provider,'none'),
		COALESCE(search_api_key,''), COALESCE(example_conversations,''),
		COALESCE(agents_md,''), COALESCE(skill_ids,''),
		COALESCE(max_turns,8), COALESCE(verify_command,'')
		FROM agents WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &a.Adapter, &a.Model, &a.Tools, &online, &permLevel,
			&sysPrompt, &searchProvider, &searchAPIKey, &exampleConv,
			&agentsMD, &skillIDs, &maxTurns, &verifyCmd)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("agent %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	a.Online = online == 1
	a.PermissionLevel = permLevel.String
	if a.PermissionLevel == "" {
		a.PermissionLevel = "workspace-write"
	}
	a.SystemPrompt = sysPrompt.String
	a.SearchProvider = searchProvider.String
	a.SearchAPIKey = searchAPIKey.String
	a.ExampleConversations = exampleConv.String
	a.AgentsMD = agentsMD.String
	a.SkillIDs = skillIDs.String
	a.MaxTurns = int(maxTurns.Int64)
	if a.MaxTurns == 0 {
		a.MaxTurns = 8
	}
	a.VerifyCommand = verifyCmd.String
	return &a, nil
}

// GetQuestionsByScene 按场景读题目列表(scene 为空则返回全部)。
func GetQuestionsByScene(scene string) ([]QuestionRow, error) {
	q := `SELECT id, scene, title, prompt, COALESCE(expected,''), difficulty, source,
	      COALESCE(rule_set_id,''), COALESCE(source_repo,''), COALESCE(source_url,''), COALESCE(desensitized,0) FROM questions`
	var args []any
	if scene != "" {
		q += " WHERE scene = ?"
		args = append(args, scene)
	}
	q += " ORDER BY created_at"

	rows, err := DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []QuestionRow
	for rows.Next() {
		var r QuestionRow
		if err := rows.Scan(&r.ID, &r.Scene, &r.Title, &r.Prompt, &r.Expected,
			&r.Difficulty, &r.Source, &r.RuleSetID, &r.SourceRepo, &r.SourceURL, &r.Desensitized); err != nil {
			continue
		}
		items = append(items, r)
	}
	return items, nil
}

// GetRulesByIDs 按 id 列表批量读规则。
func GetRulesByIDs(ids []string) ([]RuleRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// SQLite 支持 IN (?, ?, ...),动态构造占位符
	placeholders := ""
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}
	q := fmt.Sprintf(`SELECT id, name, dimension, scoring, weight, content, COALESCE(tags,'')
		FROM rules WHERE id IN (%s)`, placeholders)
	rows, err := DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []RuleRow
	for rows.Next() {
		var r RuleRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Dimension, &r.Scoring, &r.Weight, &r.Content, &r.Tags); err != nil {
			continue
		}
		items = append(items, r)
	}
	return items, nil
}

// ParseRuleIDs 把 rule_sets.rule_ids(JSON 数组字符串)解析为 []string。
func ParseRuleIDs(ruleIDsJSON string) []string {
	if ruleIDsJSON == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(ruleIDsJSON), &ids); err != nil {
		return nil
	}
	return ids
}

// GetRuleSet 按 id 读一个规则集。
func GetRuleSet(id string) (*RuleSetRow, error) {
	var rs RuleSetRow
	var scene, ruleIDs sql.NullString
	err := DB.QueryRow(`SELECT id, name, scene, rule_ids FROM rule_sets WHERE id = ?`, id).
		Scan(&rs.ID, &rs.Name, &scene, &ruleIDs)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("rule_set %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	rs.Scene = scene.String
	rs.RuleIDs = ruleIDs.String
	return &rs, nil
}

// GetModelConfigRole 读 model_config 表中某角色绑定的模型 id。
func GetModelConfigRole(role string) (string, error) {
	if DB == nil {
		return "", nil // DB 未初始化,返回空
	}
	var modelID string
	err := DB.QueryRow("SELECT model_id FROM model_config WHERE role = ?", role).Scan(&modelID)
	if err == sql.ErrNoRows {
		return "", nil // 未配置返回空,由调用方决定回退
	}
	return modelID, err
}

// JudgeModelRow 对应 judge_models 表一行(考官模型库,MRD 模型职责分离)。
type JudgeModelRow struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`       // 真实模型 ID(如 glm-4-flash)
	BaseURL   string  `json:"base_url"`
	APIKey    string  `json:"-"`          // 不序列化输出(脱敏由 handler 处理)
	APIFormat string  `json:"api_format"` // API 协议格式:openai|anthropic|gemini|''(空=自动检测)
	Role      string  `json:"role"`       // main | expert_* | question_gen
	Dimension string  `json:"dimension"`  // 专家负责的维度(security/...),非专家留空
	Available bool    `json:"available"`
}

// GetJudgeModelByRole 读 judge_models 表中某角色绑定的考官模型。
// 不存在返回 (nil, nil)(由调用方决定回退到 main 或 mock)。
func GetJudgeModelByRole(role string) (*JudgeModelRow, error) {
	if DB == nil {
		return nil, nil
	}
	var jm JudgeModelRow
	var avail int
	var apiKey, dimension, apiFormat sql.NullString
	err := DB.QueryRow(`SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,''), role, COALESCE(dimension,''), COALESCE(available,0)
		FROM judge_models WHERE role = ?`, role).
		Scan(&jm.ID, &jm.Name, &jm.BaseURL, &apiKey, &apiFormat, &jm.Role, &dimension, &avail)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	jm.APIKey = apiKey.String
	jm.APIFormat = apiFormat.String
	jm.Dimension = dimension.String
	jm.Available = avail == 1
	return &jm, nil
}

// GetAllJudgeModels 读全部考官模型(供前端管理页)。
func GetAllJudgeModels() ([]JudgeModelRow, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,''), role, COALESCE(dimension,''), COALESCE(available,0)
		FROM judge_models ORDER BY role`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []JudgeModelRow
	for rows.Next() {
		var jm JudgeModelRow
		var avail int
		if err := rows.Scan(&jm.ID, &jm.Name, &jm.BaseURL, &jm.APIKey, &jm.APIFormat, &jm.Role, &jm.Dimension, &avail); err != nil {
			continue
		}
		jm.Available = avail == 1
		items = append(items, jm)
	}
	return items, nil
}

// GetExperts 读所有专家角色(role 以 expert_ 开头且配了 dimension)。
// 供圆桌引擎动态加载专家,替代硬编码列表。
func GetExperts() ([]JudgeModelRow, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,''), role, COALESCE(dimension,''), COALESCE(available,0)
		FROM judge_models
		WHERE role LIKE 'expert_%' AND COALESCE(dimension,'') != ''
		ORDER BY role`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []JudgeModelRow
	for rows.Next() {
		var jm JudgeModelRow
		var avail int
		if err := rows.Scan(&jm.ID, &jm.Name, &jm.BaseURL, &jm.APIKey, &jm.APIFormat, &jm.Role, &jm.Dimension, &avail); err != nil {
			continue
		}
		jm.Available = avail == 1
		items = append(items, jm)
	}
	return items, nil
}

// SceneDefaults 是某评测分类的默认规则集 + 默认题集(MRD 批次④:分类套件)。
type SceneDefaults struct {
	ID                   string
	RuleSetID            string
	DefaultQuestionSetID string
}

// GetSceneDefaults 读 scenes 表中某分类的默认规则集 + 默认题集。
// 内置 5 个分类(agent/model/competitor/prd/product)在 DB 里可能没有记录,
// 调用方需对返回的空 RuleSetID/DefaultQuestionSetID 做兜底处理。
func GetSceneDefaults(sceneID string) (*SceneDefaults, error) {
	if DB == nil {
		return nil, nil
	}
	var sd SceneDefaults
	var ruleSetID, qsetID sql.NullString
	err := DB.QueryRow(`SELECT id, COALESCE(rule_set_id,''), COALESCE(default_question_set_id,'')
		FROM scenes WHERE id = ?`, sceneID).
		Scan(&sd.ID, &ruleSetID, &qsetID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sd.RuleSetID = ruleSetID.String
	sd.DefaultQuestionSetID = qsetID.String
	return &sd, nil
}

// GetQuestionsBySetID 按题集 ID 读题目列表(question_sets.question_ids 解析)。
// 供 engine 在分类默认题集场景下取题(替代 GetQuestionsByScene 的 scene 字符串过滤)。
func GetQuestionsBySetID(setID string) ([]QuestionRow, error) {
	if setID == "" {
		return nil, nil
	}
	var qidsJSON string
	err := DB.QueryRow("SELECT COALESCE(question_ids,'') FROM question_sets WHERE id = ?", setID).Scan(&qidsJSON)
	if err != nil || qidsJSON == "" {
		return nil, err
	}
	ids := ParseRuleIDs(qidsJSON)
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := ""
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}
	q := fmt.Sprintf(`SELECT id, scene, title, prompt, COALESCE(expected,''), difficulty, source,
		COALESCE(rule_set_id,''), COALESCE(source_repo,''), COALESCE(source_url,''), COALESCE(desensitized,0)
		FROM questions WHERE id IN (%s)`, placeholders)
	rows, err := DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []QuestionRow
	for rows.Next() {
		var r QuestionRow
		if err := rows.Scan(&r.ID, &r.Scene, &r.Title, &r.Prompt, &r.Expected,
			&r.Difficulty, &r.Source, &r.RuleSetID, &r.SourceRepo, &r.SourceURL, &r.Desensitized); err != nil {
			continue
		}
		items = append(items, r)
	}
	return items, nil
}

// UpsertJudgeModel 按 role 写入/更新一个考官模型(Upsert)。
func UpsertJudgeModel(id, name, baseURL, apiKey, apiFormat, role string, available bool) error {
	avail := 0
	if available {
		avail = 1
	}
	// 先尝试 UPDATE(按 role 唯一键)
	res, err := DB.Exec(`UPDATE judge_models SET name=?, base_url=?, api_key=?, api_format=?, available=?, updated_at=CURRENT_TIMESTAMP
		WHERE role=?`, name, baseURL, apiKey, apiFormat, avail, role)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// 不存在则 INSERT
	_, err = DB.Exec(`INSERT INTO judge_models (id, name, base_url, api_key, api_format, role, available, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, name, baseURL, apiKey, apiFormat, role, avail)
	return err
}

// UpdateJudgeModelDimension 单独更新考官模型的 dimension 列(专家维度)。
// UpsertJudgeModel 不覆盖 dimension,故单独提供(供 settings 页设置专家维度用)。
func UpdateJudgeModelDimension(dimension, role string) error {
	_, err := DB.Exec(`UPDATE judge_models SET dimension = ? WHERE role = ?`, dimension, role)
	return err
}

// DeleteJudgeModelByRole 按 role 删除考官模型(自定义专家用)。
// 返回受影响行数(0=不存在)。注意:调用方应保护预置角色(main/question_gen)不被删。
func DeleteJudgeModelByRole(role string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM judge_models WHERE role = ?`, role)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UpdateTaskStatus 更新任务状态(created_at 不动,更新 updated_at)。
func UpdateTaskStatus(id, status string) error {
	_, err := DB.Exec("UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", status, id)
	return err
}

// UpdateTaskResult 写入评测报告(result JSON)并标记完成。
func UpdateTaskResult(id, resultJSON string) error {
	_, err := DB.Exec("UPDATE tasks SET status = 'done', result = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		resultJSON, id)
	return err
}

// InsertConversation 记录一轮评测对话(借鉴 claw-code conversation 持久化)。
func InsertConversation(id, taskID string, turn int, messagesJSON string, tokenIn, tokenOut int) error {
	_, err := DB.Exec(`INSERT INTO conversations (id, task_id, turn, messages, status, token_in, token_out, created_at)
		VALUES (?, ?, ?, ?, 'finished', ?, ?, CURRENT_TIMESTAMP)`,
		id, taskID, turn, messagesJSON, tokenIn, tokenOut)
	return err
}

// InsertBadCase 记录一个 Bad Case(低分题)。
func InsertBadCase(id, taskID, questionID, agentID, answer string, score float64, feedbackType string) error {
	_, err := DB.Exec(`INSERT INTO bad_cases (id, task_id, question_id, agent_id, answer, score, feedback_type, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP)`,
		id, taskID, questionID, agentID, answer, score, feedbackType)
	return err
}

// GetTask 按 id 读一个任务(engine 入口)。
func GetTask(id string) (*TaskRow, error) {
	var t TaskRow
	var agentID, ruleSet, result, expertGroupID sql.NullString
	var chaosEnabled int
	err := DB.QueryRow(`SELECT id, name, scene, COALESCE(agent_id,''), rule_set, status,
		COALESCE(result,''), COALESCE(chaos_enabled,0), COALESCE(expert_group_id,'')
		FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Scene, &agentID, &ruleSet, &t.Status, &result, &chaosEnabled, &expertGroupID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %s not found", id)
	}
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

// GetAllRules 读全部规则(无规则集时的兜底)。
func GetAllRules() ([]RuleRow, error) {
	rows, err := DB.Query(`SELECT id, name, dimension, scoring, weight, content, COALESCE(tags,'')
		FROM rules ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RuleRow
	for rows.Next() {
		var r RuleRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Dimension, &r.Scoring, &r.Weight, &r.Content, &r.Tags); err != nil {
			continue
		}
		items = append(items, r)
	}
	return items, nil
}

// CountQuestions 返回题库总数(任务无关联题集时用默认题量)。
func CountQuestions() int {
	var n int
	DB.QueryRow("SELECT COUNT(*) FROM questions").Scan(&n)
	return n
}

// RuleRelation 对应 rule_relations 表一行(MRD §7.3 知识图谱关系边)。
type RuleRelation struct {
	ID            string  `json:"id"`
	SourceRuleID  string  `json:"source_rule_id"`
	TargetRuleID  string  `json:"target_rule_id"`
	RelationType  string  `json:"relation_type"`  // related_to | belong_to_category | derived_from
	Weight        float64 `json:"weight"`
}

// GetRelatedRuleIDs 沿关系边 1-hop 拉取与 sourceRuleID 关联的目标规则 ID。
// 用于 RAG 检索:命中一条规则后,顺着关系边把关联规则也带进评测。
func GetRelatedRuleIDs(sourceRuleID string) []string {
	if DB == nil {
		return nil
	}
	rows, err := DB.Query(`SELECT target_rule_id FROM rule_relations WHERE source_rule_id = ?`, sourceRuleID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// GetAllRuleRelations 读全部关系边(供知识图谱可视化 + 导出)。
func GetAllRuleRelations() ([]RuleRelation, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT id, source_rule_id, target_rule_id, relation_type, COALESCE(weight,1.0)
		FROM rule_relations ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RuleRelation
	for rows.Next() {
		var r RuleRelation
		if err := rows.Scan(&r.ID, &r.SourceRuleID, &r.TargetRuleID, &r.RelationType, &r.Weight); err != nil {
			continue
		}
		items = append(items, r)
	}
	return items, nil
}

// InsertRuleRelation 插入一条关系边。重复(source,target,type)则忽略。
func InsertRuleRelation(id, sourceID, targetID, relationType string, weight float64) error {
	_, err := DB.Exec(`INSERT OR IGNORE INTO rule_relations (id, source_rule_id, target_rule_id, relation_type, weight, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		id, sourceID, targetID, relationType, weight)
	return err
}

// DeleteRuleRelation 删除一条关系边。
func DeleteRuleRelation(id string) error {
	_, err := DB.Exec(`DELETE FROM rule_relations WHERE id = ?`, id)
	return err
}

// ── 专家组(Expert Group)查询 ──

// GetExpertGroup 读一个专家组(主表 + 全部成员)。
// groupID 不存在返回 (nil, nil, nil)。
func GetExpertGroup(groupID string) (*ExpertGroupRow, []ExpertGroupMemberRow, error) {
	if DB == nil || groupID == "" {
		return nil, nil, nil
	}
	var g ExpertGroupRow
	var desc sql.NullString
	err := DB.QueryRow(`SELECT id, name, COALESCE(description,''), COALESCE(status,'active')
		FROM expert_groups WHERE id = ?`, groupID).
		Scan(&g.ID, &g.Name, &desc, &g.Status)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	g.Description = desc.String

	members, err := GetExpertGroupMembers(groupID)
	if err != nil {
		return &g, nil, err
	}
	return &g, members, nil
}

// GetExpertGroupMembers 读一个专家组的全部成员(按 ordinal 排序)。
func GetExpertGroupMembers(groupID string) ([]ExpertGroupMemberRow, error) {
	rows, err := DB.Query(`SELECT id, group_id, COALESCE(ordinal,0), member_type, COALESCE(member_ref,''),
		COALESCE(dimension,''), COALESCE(role_label,''), COALESCE(weight,1.0),
		COALESCE(prompt_text,''), COALESCE(prompt_template_id,'')
		FROM expert_group_members WHERE group_id = ? ORDER BY ordinal`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ExpertGroupMemberRow
	for rows.Next() {
		var m ExpertGroupMemberRow
		if err := rows.Scan(&m.ID, &m.GroupID, &m.Ordinal, &m.MemberType, &m.MemberRef,
			&m.Dimension, &m.RoleLabel, &m.Weight, &m.PromptText, &m.PromptTemplateID); err != nil {
			continue
		}
		items = append(items, m)
	}
	return items, nil
}

// GetAllExpertGroups 读全部专家组(不含成员,供列表)。
func GetAllExpertGroups() ([]ExpertGroupRow, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT id, name, COALESCE(description,''), COALESCE(status,'active')
		FROM expert_groups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ExpertGroupRow
	for rows.Next() {
		var g ExpertGroupRow
		var desc sql.NullString
		if err := rows.Scan(&g.ID, &g.Name, &desc, &g.Status); err != nil {
			continue
		}
		g.Description = desc.String
		items = append(items, g)
	}
	return items, nil
}

// InsertExpertGroup 新建专家组主表。
func InsertExpertGroup(id, name, description, status string) error {
	_, err := DB.Exec(`INSERT INTO expert_groups (id, name, description, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, name, description, status)
	return err
}

// UpdateExpertGroup 更新专家组主表(不改成员,成员由重建接口处理)。
func UpdateExpertGroup(id, name, description, status string) error {
	_, err := DB.Exec(`UPDATE expert_groups SET name=?, description=?, status=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, name, description, status, id)
	return err
}

// InsertExpertGroupMember 新建一个成员。
func InsertExpertGroupMember(id, groupID string, ordinal int, memberType, memberRef,
	dimension, roleLabel string, weight float64, promptText, promptTemplateID string) error {
	_, err := DB.Exec(`INSERT INTO expert_group_members
		(id, group_id, ordinal, member_type, member_ref, dimension, role_label, weight, prompt_text, prompt_template_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		id, groupID, ordinal, memberType, memberRef, dimension, roleLabel, weight, promptText, promptTemplateID)
	return err
}

// DeleteExpertGroupMembers 删除一个专家组的全部成员(重建用)。
func DeleteExpertGroupMembers(groupID string) error {
	_, err := DB.Exec(`DELETE FROM expert_group_members WHERE group_id = ?`, groupID)
	return err
}

// DeleteExpertGroup 删除专家组(级联删成员)。
func DeleteExpertGroup(groupID string) error {
	if err := DeleteExpertGroupMembers(groupID); err != nil {
		return err
	}
	_, err := DB.Exec(`DELETE FROM expert_groups WHERE id = ?`, groupID)
	return err
}

// GetPromptTemplate 读一个 Prompt 模板(打通引擎读取链路)。
// 之前 prompt_templates 表引擎只写不读(attribution.go 归因闭环写入),
// 专家组成员引用 prompt_template_id 时用此函数读取。
func GetPromptTemplate(id string) (*struct {
	ID        string
	Name      string
	Content   string
	Version   int
	ModelHint string
}, error) {
	if DB == nil || id == "" {
		return nil, nil
	}
	tpl := &struct {
		ID        string
		Name      string
		Content   string
		Version   int
		ModelHint string
	}{}
	var modelHint sql.NullString
	err := DB.QueryRow(`SELECT id, name, content, COALESCE(version,1), COALESCE(model_hint,'')
		FROM prompt_templates WHERE id = ?`, id).
		Scan(&tpl.ID, &tpl.Name, &tpl.Content, &tpl.Version, &modelHint)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tpl.ModelHint = modelHint.String
	return tpl, nil
}

// ── Agent Definition(声明式定义文件)查询 ──

// InsertAgentDefinition 新建一个 Agent 定义文件。
func InsertAgentDefinition(id, name, description, manifest string) error {
	_, err := DB.Exec(`INSERT INTO agent_definitions (id, name, description, manifest, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`, id, name, description, manifest)
	return err
}

// GetAgentDefinition 读一个 Agent 定义文件。
func GetAgentDefinition(id string) (*AgentDefinitionRow, error) {
	var d AgentDefinitionRow
	var desc, instID sql.NullString
	err := DB.QueryRow(`SELECT id, name, COALESCE(description,''), manifest, COALESCE(instantiated_agent_id,'')
		FROM agent_definitions WHERE id = ?`, id).
		Scan(&d.ID, &d.Name, &desc, &d.Manifest, &instID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Description = desc.String
	d.InstantiatedAgentID = instID.String
	return &d, nil
}

// MarkAgentDefinitionInstantiated 回填实例化后的 agent_id。
func MarkAgentDefinitionInstantiated(defID, agentID string) error {
	_, err := DB.Exec(`UPDATE agent_definitions SET instantiated_agent_id = ? WHERE id = ?`, agentID, defID)
	return err
}

// GetAllAgentDefinitions 读全部 Agent 定义文件。
func GetAllAgentDefinitions() ([]AgentDefinitionRow, error) {
	rows, err := DB.Query(`SELECT id, name, COALESCE(description,''), COALESCE(instantiated_agent_id,'')
		FROM agent_definitions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []AgentDefinitionRow
	for rows.Next() {
		var d AgentDefinitionRow
		var desc, instID sql.NullString
		if err := rows.Scan(&d.ID, &d.Name, &desc, &instID); err != nil {
			continue
		}
		d.Description = desc.String
		d.InstantiatedAgentID = instID.String
		items = append(items, d)
	}
	return items, nil
}

// ── Agent Run(运行状态追踪)查询 ──

// SetAgentRun 设置 Agent 当前运行状态(upsert:同 agent_id 覆盖)。
func SetAgentRun(id, agentID, status, detail string) error {
	// 先尝试 UPDATE
	res, err := DB.Exec(`UPDATE agent_runs SET status=?, detail=?, updated_at=CURRENT_TIMESTAMP
		WHERE agent_id = ?`, status, detail, agentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// 不存在则 INSERT
	_, err = DB.Exec(`INSERT INTO agent_runs (id, agent_id, status, detail, started_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, agentID, status, detail)
	return err
}

// GetAgentRun 读 Agent 当前运行状态。
func GetAgentRun(agentID string) (*AgentRunRow, error) {
	var r AgentRunRow
	var detail sql.NullString
	err := DB.QueryRow(`SELECT id, agent_id, status, COALESCE(detail,''), started_at, updated_at
		FROM agent_runs WHERE agent_id = ? ORDER BY updated_at DESC LIMIT 1`, agentID).
		Scan(&r.ID, &r.AgentID, &r.Status, &detail, &r.StartedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Detail = detail.String
	return &r, nil
}

// ── Agent Hooks(harness 第5层)查询 ──

// GetAgentHooks 读一个 Agent 的全部钩子。
func GetAgentHooks(agentID string) ([]HookRow, error) {
	rows, err := DB.Query(`SELECT id, agent_id, event, COALESCE(matcher,'*'), command, COALESCE(enabled,1)
		FROM agent_hooks WHERE agent_id = ? ORDER BY event`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []HookRow
	for rows.Next() {
		var h HookRow
		var enabled int
		if err := rows.Scan(&h.ID, &h.AgentID, &h.Event, &h.Matcher, &h.Command, &enabled); err != nil {
			continue
		}
		h.Enabled = enabled == 1
		items = append(items, h)
	}
	return items, nil
}

// InsertAgentHook 新建一个钩子。
func InsertAgentHook(id, agentID, event, matcher, command string) error {
	_, err := DB.Exec(`INSERT INTO agent_hooks (id, agent_id, event, matcher, command, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)`, id, agentID, event, matcher, command)
	return err
}

// DeleteAgentHooks 删除一个 Agent 的全部钩子(重建用)。
func DeleteAgentHooks(agentID string) error {
	_, err := DB.Exec(`DELETE FROM agent_hooks WHERE agent_id = ?`, agentID)
	return err
}

// ──────────────────────────────────────────────────────────────────────────
// audit_logs — 白盒调用审计日志
// ──────────────────────────────────────────────────────────────────────────

// AuditLogRow 对应 audit_logs 表一行(白盒审计日志)。
type AuditLogRow struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	Turn         int    `json:"turn"`
	Kind         string `json:"kind"` // llm | tool | event
	Seq          int    `json:"seq"`
	Caller       string `json:"caller"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	BaseURL      string `json:"base_url"`
	APIFormat    string `json:"api_format"`
	SystemPrompt string `json:"system_prompt"`
	InputText    string `json:"input_text"`
	OutputText   string `json:"output_text"`
	Reasoning    string `json:"reasoning"`
	ToolName     string `json:"tool_name"`
	TokenIn      int    `json:"token_in"`
	TokenOut     int    `json:"token_out"`
	LatencyMs    int    `json:"latency_ms"`
	HTTPStatus   int    `json:"http_status"`
	Attempt      int    `json:"attempt"`
	IsMock       bool   `json:"is_mock"`
	IsError      bool   `json:"is_error"`
	ErrorText    string `json:"error_text"`
	CreatedAt    string `json:"created_at"`
}

// InsertAuditLog 写入一条审计日志,seq 自增(任务内)。
// 用事务(BEGIN IMMEDIATE)包裹 MAX(seq)+INSERT,消除并发竞态。
// 失败仅返回 error,调用方应忽略(审计日志不能影响主流程)。
func InsertAuditLog(r AuditLogRow) error {
	if DB == nil {
		return fmt.Errorf("DB nil")
	}
	isMock, isError := 0, 0
	if r.IsMock {
		isMock = 1
	}
	if r.IsError {
		isError = 1
	}

	// 事务包裹:BEGIN IMMEDIATE 获取写锁,保证 MAX(seq)+INSERT 原子
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// seq 自增:查当前任务最大 seq + 1
	var maxSeq sql.NullInt64
	tx.QueryRow(`SELECT MAX(seq) FROM audit_logs WHERE task_id = ?`, r.TaskID).Scan(&maxSeq)
	r.Seq = int(maxSeq.Int64) + 1
	if r.ID == "" {
		r.ID = fmt.Sprintf("al-%s-%d", r.TaskID, r.Seq)
	}

	_, err = tx.Exec(`INSERT INTO audit_logs
		(id, task_id, turn, kind, seq, caller, provider, model, base_url, api_format,
		 system_prompt, input_text, output_text, reasoning, tool_name,
		 token_in, token_out, latency_ms, http_status, attempt, is_mock, is_error, error_text, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,  ?,?,?,?,?, ?,  ?,?,?,?,?,?, ?, CURRENT_TIMESTAMP)`,
		r.ID, r.TaskID, r.Turn, r.Kind, r.Seq, r.Caller, r.Provider, r.Model, r.BaseURL, r.APIFormat,
		r.SystemPrompt, r.InputText, r.OutputText, r.Reasoning, r.ToolName,
		r.TokenIn, r.TokenOut, r.LatencyMs, r.HTTPStatus, r.Attempt, isMock, isError, r.ErrorText)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ListAuditLogs 按任务查审计日志,kind 为空则返回全部。
func ListAuditLogs(taskID, kind string) ([]AuditLogRow, error) {
	if DB == nil {
		return []AuditLogRow{}, nil
	}
	var rows *sql.Rows
	var err error
	if kind != "" {
		rows, err = DB.Query(`SELECT id, task_id, turn, kind, seq, caller, provider, model, base_url, api_format,
			system_prompt, input_text, output_text, reasoning, tool_name,
			token_in, token_out, latency_ms, http_status, attempt, is_mock, is_error, error_text, created_at
			FROM audit_logs WHERE task_id = ? AND kind = ? ORDER BY seq ASC`, taskID, kind)
	} else {
		rows, err = DB.Query(`SELECT id, task_id, turn, kind, seq, caller, provider, model, base_url, api_format,
			system_prompt, input_text, output_text, reasoning, tool_name,
			token_in, token_out, latency_ms, http_status, attempt, is_mock, is_error, error_text, created_at
			FROM audit_logs WHERE task_id = ? ORDER BY seq ASC`, taskID)
	}
	if err != nil {
		return []AuditLogRow{}, err
	}
	defer rows.Close()

	var items []AuditLogRow
	for rows.Next() {
		var r AuditLogRow
		var isMock, isError int
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Turn, &r.Kind, &r.Seq, &r.Caller, &r.Provider, &r.Model, &r.BaseURL, &r.APIFormat,
			&r.SystemPrompt, &r.InputText, &r.OutputText, &r.Reasoning, &r.ToolName,
			&r.TokenIn, &r.TokenOut, &r.LatencyMs, &r.HTTPStatus, &r.Attempt, &isMock, &isError, &r.ErrorText, &r.CreatedAt); err != nil {
			continue
		}
		r.IsMock = isMock == 1
		r.IsError = isError == 1
		items = append(items, r)
	}
	if items == nil {
		items = []AuditLogRow{}
	}
	return items, nil
}

// AuditSession 审计会话摘要(独立审计页左侧列表用)。
type AuditSession struct {
	TaskID    string `json:"task_id"`
	TaskName  string `json:"task_name"`
	Status    string `json:"status"`
	CallCount int    `json:"call_count"`
	LLMCount  int    `json:"llm_count"`
	ToolCount int    `json:"tool_count"`
	CreatedAt string `json:"created_at"`
}

// ListAuditSessions 列出所有有审计日志的会话(去重),按时间倒序。
// ListAuditSessions 列出所有有审计日志的会话(去重),按时间倒序。
// 用 LEFT JOIN tasks 一次查出任务名/状态(消除 N+1),LIMIT 200 防止无限增长。
func ListAuditSessions() ([]AuditSession, error) {
	if DB == nil {
		return []AuditSession{}, nil
	}
	// LEFT JOIN tasks:一次查询拿到任务名/状态,避免循环内逐条查询(N+1)
	rows, err := DB.Query(`SELECT a.task_id,
		COUNT(*) as total,
		SUM(CASE WHEN a.kind='llm' THEN 1 ELSE 0 END) as llm_cnt,
		SUM(CASE WHEN a.kind='tool' THEN 1 ELSE 0 END) as tool_cnt,
		MIN(a.created_at) as first_at,
		COALESCE(t.name,'') as task_name,
		COALESCE(t.status,'') as status
		FROM audit_logs a
		LEFT JOIN tasks t ON a.task_id = t.id
		GROUP BY a.task_id
		ORDER BY first_at DESC
		LIMIT 200`)
	if err != nil {
		return []AuditSession{}, err
	}
	defer rows.Close()

	var items []AuditSession
	for rows.Next() {
		var s AuditSession
		if err := rows.Scan(&s.TaskID, &s.CallCount, &s.LLMCount, &s.ToolCount, &s.CreatedAt, &s.TaskName, &s.Status); err != nil {
			continue
		}
		items = append(items, s)
	}
	if items == nil {
		items = []AuditSession{}
	}
	return items, nil
}
