package api

// expert_group.go — 专家组 CRUD + AI 生成草稿。
//
// 专家组 = 一组可配置的圆桌成员,成员类型可以是:
//   - agent: 引用 agents.id(被测 agent 当专家)
//   - model: 引用 judge_models.role(考官模型)
//   - role:  自定义 role 字符串(走 LookupJudgeEndpoint 回退链)
// 每个成员可配 weight(权重)、dimension(维度)、prompt_text(内嵌提示词)、prompt_template_id(引用模板)。

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// memberInput 前端提交的成员结构(创建/更新专家组时)。
type memberInput struct {
	Ordinal          int     `json:"ordinal"`
	MemberType       string  `json:"member_type"` // agent | model | role
	MemberRef        string  `json:"member_ref"`
	Dimension        string  `json:"dimension"`
	RoleLabel        string  `json:"role_label"`
	Weight           float64 `json:"weight"`
	PromptText       string  `json:"prompt_text"`
	PromptTemplateID string  `json:"prompt_template_id"`
}

// expertGroupInput 创建/更新专家组的请求体。
type expertGroupInput struct {
	Name        string        `json:"name" binding:"required"`
	Description string        `json:"description"`
	Status      string        `json:"status"` // 留空默认 active
	Members     []memberInput `json:"members"`
}

// listExpertGroups GET /api/expert-groups
// 列出全部专家组(带成员数)。
func listExpertGroups(c *gin.Context) {
	groups, err := store.GetAllExpertGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type groupWithCount struct {
		store.ExpertGroupRow
		MemberCount int `json:"member_count"`
	}
	result := make([]groupWithCount, 0, len(groups))
	for _, g := range groups {
		members, _ := store.GetExpertGroupMembers(g.ID)
		result = append(result, groupWithCount{ExpertGroupRow: g, MemberCount: len(members)})
	}
	c.JSON(http.StatusOK, result)
}

// getExpertGroup GET /api/expert-groups/:id
// 详情(主表 + 全部成员)。
func getExpertGroup(c *gin.Context) {
	id := c.Param("id")
	g, members, err := store.GetExpertGroup(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if g == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "专家组不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": g, "members": members})
}

// createExpertGroup POST /api/expert-groups
// 新建专家组(含成员)。
func createExpertGroup(c *gin.Context) {
	var req expertGroupInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	id := "eg-" + uuid.New().String()[:8]
	if err := store.InsertExpertGroup(id, req.Name, req.Description, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := saveMembers(id, req.Members); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name, "member_count": len(req.Members)})
}

// updateExpertGroup PUT /api/expert-groups/:id
// 更新专家组(主表 + 重建成员)。
func updateExpertGroup(c *gin.Context) {
	id := c.Param("id")
	var req expertGroupInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if err := store.UpdateExpertGroup(id, req.Name, req.Description, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 重建成员:先删后插
	if err := store.DeleteExpertGroupMembers(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := saveMembers(id, req.Members); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "updated": true, "member_count": len(req.Members)})
}

// deleteExpertGroup DELETE /api/expert-groups/:id
// 删除专家组(级联删成员)。默认专家组 eg-default 不允许删。
func deleteExpertGroup(c *gin.Context) {
	id := c.Param("id")
	if id == "eg-default" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "默认专家组不可删除"})
		return
	}
	if err := store.DeleteExpertGroup(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// saveMembers 批量写入成员(内部辅助)。
func saveMembers(groupID string, members []memberInput) error {
	for i, m := range members {
		if m.MemberType == "" {
			m.MemberType = "role"
		}
		if m.Weight == 0 {
			m.Weight = 1.0
		}
		ordinal := m.Ordinal
		if ordinal == 0 {
			ordinal = i
		}
		memberID := "egm-" + uuid.New().String()[:8]
		if err := store.InsertExpertGroupMember(
			memberID, groupID, ordinal, m.MemberType, m.MemberRef,
			m.Dimension, m.RoleLabel, m.Weight, m.PromptText, m.PromptTemplateID); err != nil {
			return err
		}
	}
	return nil
}

// aiGenerateExpertGroup POST /api/expert-groups/ai-generate
// 用 LLM 生成专家组草稿(不落库,返回给前端预览编辑)。
//
// 请求: {scenario: "数据库SQL评测", count: 4}
// 响应: {name, description, members: [...]}  草稿结构
//
// 用户在前端编辑确认后,调 createExpertGroup 保存。
func aiGenerateExpertGroup(c *gin.Context) {
	var req struct {
		Scenario string `json:"scenario" binding:"required"`
		Count    int    `json:"count"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Count <= 0 || req.Count > 10 {
		req.Count = 4
	}

	// 用 question_gen 考官模型生成
	baseURL, apiKey, realName, apiFormat := engine.LookupJudgeEndpoint("question_gen")
	if baseURL == "" || apiKey == "" {
		// 无 Key,返回模板化草稿(让用户有东西可编辑)
		c.JSON(http.StatusOK, generateTemplateDraft(req.Scenario, req.Count))
		return
	}

	prompt := fmt.Sprintf(`你是 AI 评测架构师。请为评测场景"%s"设计一个专家组(%d 个成员)。

要求:
1. 每个成员负责一个评测维度(如 security/performance/architecture/product/correctness/reliability 等)
2. 成员类型统一用 "role"(用户后续可在前端改成 agent 或 model)
3. member_ref 用 expert_<维度英文> 格式
4. 为每个成员写一段专门的评审提示词(prompt_text),说明该维度的评审重点

只返回 JSON,不要其他文字:
{
  "name": "<专家组名称>",
  "description": "<一句话描述>",
  "members": [
    {"member_type":"role","member_ref":"expert_<dim>","dimension":"<dim>","role_label":"<中文名>","weight":1.0,"prompt_text":"<评审提示词>"}
  ]
}`, req.Scenario, req.Count)

	result, err := engine.CallJudgeRaw(baseURL, apiKey, apiFormat, realName, prompt)
	if err != nil {
		log.Printf("[expert-group] AI 生成失败(%v),返回模板草稿", err)
		c.JSON(http.StatusOK, generateTemplateDraft(req.Scenario, req.Count))
		return
	}

	// 从 LLM 响应里抽 JSON
	draft := extractJSONFromLLM(result)
	if draft == nil {
		// JSON 解析失败,返回模板草稿
		c.JSON(http.StatusOK, generateTemplateDraft(req.Scenario, req.Count))
		return
	}
	draft["ai_generated"] = true
	c.JSON(http.StatusOK, draft)
}

// generateTemplateDraft 无 LLM Key 时的模板化草稿(让用户有东西可编辑)。
func generateTemplateDraft(scenario string, count int) gin.H {
	dims := []struct{ dim, label, prompt string }{
		{"security", "安全专家", "审查注入、越权、敏感信息泄露等安全问题。检查输入验证、权限控制、密钥管理。"},
		{"performance", "性能专家", "审查性能瓶颈、死锁、内存泄漏、N+1 查询。检查并发安全性和资源利用率。"},
		{"architecture", "架构专家", "审查分层合理性、耦合度、扩展性。检查设计模式使用和模块边界。"},
		{"correctness", "正确性专家", "审查逻辑正确性、边界条件、错误处理。检查是否符合题目要求。"},
	}
	members := []gin.H{}
	for i := 0; i < count && i < len(dims); i++ {
		d := dims[i]
		members = append(members, gin.H{
			"member_type": "role", "member_ref": "expert_" + d.dim,
			"dimension": d.dim, "role_label": d.label, "weight": 1.0,
			"prompt_text": d.prompt,
		})
	}
	return gin.H{
		"name":        scenario + " 评测专家组",
		"description": "为 " + scenario + " 场景设计的评测专家组(模板草稿,请编辑后保存)",
		"members":     members,
		"ai_generated": false,
		"note":        "未配置出题模型 Key,返回模板草稿。配置 Key 后可生成更精准的专家组。",
	}
}

// extractJSONFromLLM 从 LLM 响应文本里抽 JSON 对象。
// LLM 常在 JSON 前后加 markdown 围栏或解释文字,这里容错处理。
func extractJSONFromLLM(text string) gin.H {
	text = strings.TrimSpace(text)
	// 去 markdown 代码围栏
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	// 找第一个 { 和最后一个 }
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	jsonStr := text[start : end+1]
	var result gin.H
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil
	}
	return result
}
