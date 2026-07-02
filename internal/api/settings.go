package api

// settings.go — 设置页专用接口。
//
// 单独文件:Settings 页需要查看/管理 API Key,但普通 /api/models 出于安全不返回 key。
// 这里提供带脱敏 key 的视图端点,Key 前后各显示几位,中间打码。

import (
	"log"
	"net/http"
	"strings"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// ModelWithKey 用于设置页,带脱敏 key(仅显示首尾,中间打码)。
type ModelWithKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIFormat string `json:"api_format"`
	HasKey    bool   `json:"has_key"`     // 是否已配置 Key
	MaskedKey string `json:"masked_key"`  // 脱敏后的 Key(如 sk-***7890)
}

// listModelsForSettings GET /api/settings/models
// 返回模型列表,Key 做脱敏处理(maskKey)。
func listModelsForSettings(c *gin.Context) {
	rows, err := store.DB.Query("SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,'') FROM models ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []ModelWithKey{}
	for rows.Next() {
		var m ModelWithKey
		var key string
		if err := rows.Scan(&m.ID, &m.Name, &m.BaseURL, &key, &m.APIFormat); err != nil {
			continue
		}
		m.HasKey = key != ""
		m.MaskedKey = maskKey(key)
		items = append(items, m)
	}
	c.JSON(http.StatusOK, items)
}

// maskKey 把 Key 脱敏:长度<=8 全部打码;否则保留首 4 + 尾 4,中间 ***。
// 例:sk-abcdef1234567890 → sk-a***7890
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "********"
	}
	return key[:4] + "***" + key[len(key)-4:]
}

// probeModel POST /api/settings/models/:id/probe
// 真正调一次模型 API 检测连通性,成功则标记 available=1。
func probeModel(c *gin.Context) {
	modelRef := c.Param("id")
	result := engine.ProbeModel(modelRef)
	c.JSON(http.StatusOK, result)
}

// ── 考官模型库(MRD 模型职责分离) ──

// JudgeModelWithKey 前端视图,带脱敏 key + 角色中文标签。
type JudgeModelWithKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Role      string `json:"role"`
	RoleLabel string `json:"role_label"`
	Dimension string `json:"dimension"`
	APIFormat string `json:"api_format"`
	HasKey    bool   `json:"has_key"`
	MaskedKey string `json:"masked_key"`
	Available bool   `json:"available"`
}

// judgeRoleLabel 考官角色中文标签。预置角色查表,自定义专家按 role 推导。
func judgeRoleLabel(role string) string {
	labels := map[string]string{
		"main":            "主导者 Agent",
		"expert_security": "安全专家",
		"expert_perf":     "性能专家",
		"expert_arch":     "架构专家",
		"expert_product":  "产品专家",
		"question_gen":    "出题 Agent",
	}
	if l, ok := labels[role]; ok {
		return l
	}
	// 自定义专家:expert_reliability → "可靠性专家"
	if strings.HasPrefix(role, "expert_") {
		return strings.TrimPrefix(role, "expert_") + " 专家"
	}
	return role
}

// listJudgeModels GET /api/settings/judge-models
// 列出考官模型库(带脱敏 key)。
func listJudgeModels(c *gin.Context) {
	jms, err := store.GetAllJudgeModels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := []JudgeModelWithKey{}
	for _, jm := range jms {
		items = append(items, JudgeModelWithKey{
			ID:        jm.ID,
			Name:      jm.Name,
			BaseURL:   jm.BaseURL,
			Role:      jm.Role,
			RoleLabel: judgeRoleLabel(jm.Role),
			Dimension: jm.Dimension,
			APIFormat: jm.APIFormat,
			HasKey:    jm.APIKey != "",
			MaskedKey: maskKey(jm.APIKey),
			Available: jm.Available,
		})
	}
	c.JSON(http.StatusOK, items)
}

// upsertJudgeModel PUT /api/settings/judge-models
// 写入/更新一个考官模型(按 role Upsert)。
// Body: { role, name, base_url, api_key, dimension?, role_label? }
//   - dimension: 专家负责的维度(security/...),expert_* 角色必填,其余留空
//   - role_label: 中文标签(如"可靠性专家"),自定义专家用,缺省则按 role 推导
func upsertJudgeModel(c *gin.Context) {
	var req struct {
		Role      string `json:"role" binding:"required"`
		Name      string `json:"name" binding:"required"`
		BaseURL   string `json:"base_url" binding:"required"`
		APIKey    string `json:"api_key"`
		APIFormat string `json:"api_format"`
		Dimension string `json:"dimension"`
		RoleLabel string `json:"role_label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := "jm-" + req.Role
	if err := store.UpsertJudgeModel(id, req.Name, req.BaseURL, engine.EncryptSecret(req.APIKey), req.APIFormat, req.Role, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// dimension 单独更新(UpsertJudgeModel 未覆盖该列)
	if req.Dimension != "" || strings.HasPrefix(req.Role, "expert_") {
		if err := store.UpdateJudgeModelDimension(req.Dimension, req.Role); err != nil {
			log.Printf("[settings] 更新 dimension 失败 role=%s: %v", req.Role, err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "role": req.Role, "dimension": req.Dimension, "api_format": req.APIFormat, "saved": true})
}

// deleteJudgeModel DELETE /api/settings/judge-models/:role
// 删除一个考官模型(自定义专家用)。预置角色(main/question_gen)禁止删。
func deleteJudgeModel(c *gin.Context) {
	role := c.Param("role")
	// 保护预置角色:main 和 question_gen 是引擎必需,不可删
	if role == "main" || role == "question_gen" || !strings.HasPrefix(role, "expert_") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "预置角色不可删除"})
		return
	}
	n, err := store.DeleteJudgeModelByRole(role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"role": role, "deleted": n > 0})
}

// probeJudgeModel POST /api/settings/judge-models/:role/probe
// 调一次考官模型 API 检测连通性,成功标记 available=1。
func probeJudgeModel(c *gin.Context) {
	role := c.Param("role")
	jm, err := store.GetJudgeModelByRole(role)
	if err != nil || jm == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "考官模型未配置: " + role})
		return
	}
	if jm.APIKey == "" || jm.BaseURL == "" {
		c.JSON(http.StatusOK, gin.H{"role": role, "available": false, "detail": "未配置 Key 或 Base URL"})
		return
	}
	// 用显式端点检测(考官模型不在考生 models 表;api_key 需解密)
	result := engine.ProbeModelEndpoint(jm.BaseURL, engine.DecryptSecret(jm.APIKey), jm.APIFormat, jm.Name)
	if result.OK {
		store.UpsertJudgeModel(jm.ID, jm.Name, jm.BaseURL, jm.APIKey, jm.APIFormat, jm.Role, true)
	}
	c.JSON(http.StatusOK, gin.H{"role": role, "available": result.OK, "detail": result.Message})
}
