package api

// ruleio.go — 规则导入导出(MRD 6.1 / 4.4)。
//
// 功能:
//   - 导出规则集为 JSON(供分享给同事/社区)
//   - 从 JSON 导入规则集(支持新增 + 覆盖)
//   - 导出全部规则为 YAML(备份/迁移)
//   - 从 YAML 导入规则
//
// API:
//   GET  /api/rule-sets/:id/export   — 导出规则集 JSON
//   POST /api/rule-sets/import       — 导入规则集 JSON
//   GET  /api/rules/export           — 导出全部规则 JSON
//   POST /api/rules/import           — 导入规则 JSON

import (
	"encoding/json"
	"math"
	"net/http"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// exportRuleSet GET /api/rule-sets/:id/export
// 导出规则集(含规则详情)为 JSON。
func exportRuleSet(c *gin.Context) {
	id := c.Param("id")

	// 读规则集
	rs, err := store.GetRuleSet(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule set not found"})
		return
	}

	// 读规则详情
	ruleIDs := store.ParseRuleIDs(rs.RuleIDs)
	rules, _ := store.GetRulesByIDs(ruleIDs)

	// 构造导出结构
	export := map[string]interface{}{
		"version":    "1.0",
		"rule_set": map[string]interface{}{
			"id":       rs.ID,
			"name":     rs.Name,
			"scene":    rs.Scene,
		},
		"rules": rules,
	}

	c.Header("Content-Disposition", "attachment; filename=rule-set-"+id+".json")
	c.JSON(http.StatusOK, export)
}

// importRuleSet POST /api/rule-sets/import
// Body: JSON 格式的规则集(含规则详情)。
func importRuleSet(c *gin.Context) {
	var req struct {
		Version string `json:"version"`
		RuleSet struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Scene string `json:"scene"`
		} `json:"rule_set"`
		Rules []struct {
			ID        string  `json:"id"`
			Name      string  `json:"name"`
			Dimension string  `json:"dimension"`
			Scoring   string  `json:"scoring"`
			Weight    float64 `json:"weight"`
			Content   string  `json:"content"`
			Tags      string  `json:"tags"`
		} `json:"rules"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 导入规则(如果 ID 冲突则跳过)
	importedRules := 0
	now := time.Now().Format(time.RFC3339)
	for _, r := range req.Rules {
		if r.ID == "" {
			r.ID = uuid.New().String()
		}
		err := store.InsertRuleIgnore(store.RuleCreateParams{
			ID: r.ID, Name: r.Name, Dimension: r.Dimension, Scoring: r.Scoring,
			Weight: r.Weight, Content: r.Content, Tags: r.Tags, CreatedAt: now,
		})
		if err == nil {
			importedRules++
		}
	}

	// 导入规则集
	rsID := req.RuleSet.ID
	if rsID == "" {
		rsID = uuid.New().String()
	}
	ruleIDs, _ := json.Marshal(getRuleIDsFromImport(req.Rules))
	store.DB.Exec(`INSERT OR IGNORE INTO rule_sets (id, name, scene, rule_ids, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		rsID, req.RuleSet.Name, req.RuleSet.Scene, string(ruleIDs))

	c.JSON(http.StatusOK, gin.H{
		"rule_set_id":    rsID,
		"imported_rules": importedRules,
	})
}

// exportRules GET /api/rules/export
// 导出全部规则为 JSON。
func exportRules(c *gin.Context) {
	rules, err := store.GetAllRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	relations, _ := store.GetAllRuleRelations()

	export := map[string]interface{}{
		"version":   "1.0",
		"count":     len(rules),
		"rules":     rules,
		"relations": relations, // MRD §7.3 知识图谱关系边一并导出
	}

	c.Header("Content-Disposition", "attachment; filename=rules-export.json")
	c.JSON(http.StatusOK, export)
}

// importRules POST /api/rules/import
// Body: JSON 格式的规则列表(可选含 relations 关系边)。
func importRules(c *gin.Context) {
	var req struct {
		Version string `json:"version"`
		Rules   []struct {
			ID        string  `json:"id"`
			Name      string  `json:"name"`
			Dimension string  `json:"dimension"`
			Scoring   string  `json:"scoring"`
			Weight    float64 `json:"weight"`
			Content   string  `json:"content"`
			Tags      string  `json:"tags"`
		} `json:"rules"`
		Relations []struct {
			SourceRuleID string  `json:"source_rule_id"`
			TargetRuleID string  `json:"target_rule_id"`
			RelationType string  `json:"relation_type"`
			Weight       float64 `json:"weight"`
		} `json:"relations"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	imported := 0
	now := time.Now().Format(time.RFC3339)
	for _, r := range req.Rules {
		if r.ID == "" {
			r.ID = uuid.New().String()
		}
		err := store.InsertRuleIgnore(store.RuleCreateParams{
			ID: r.ID, Name: r.Name, Dimension: r.Dimension, Scoring: r.Scoring,
			Weight: r.Weight, Content: r.Content, Tags: r.Tags, CreatedAt: now,
		})
		if err == nil {
			imported++
		}
	}

	// 导入关系边(MRD §7.3)
	relImported := 0
	for _, rel := range req.Relations {
		if rel.SourceRuleID == "" || rel.TargetRuleID == "" {
			continue
		}
		if rel.RelationType == "" {
			rel.RelationType = "related_to"
		}
		if rel.Weight == 0 {
			rel.Weight = 1.0
		}
		if err := store.InsertRuleRelation(uuid.New().String(), rel.SourceRuleID, rel.TargetRuleID, rel.RelationType, rel.Weight); err == nil {
			relImported++
		}
	}

	c.JSON(http.StatusOK, gin.H{"imported": imported, "relations_imported": relImported})
}

// getRuleIDsFromImport 从导入的规则列表中提取 ID。
func getRuleIDsFromImport(rules []struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Dimension string  `json:"dimension"`
	Scoring   string  `json:"scoring"`
	Weight    float64 `json:"weight"`
	Content   string  `json:"content"`
	Tags      string  `json:"tags"`
}) []string {
	var ids []string
	for _, r := range rules {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
		return ids
	}

	// rulesGraph GET /api/rules/graph
	// 返回规则知识图谱数据(节点+边),供前端 Canvas/G6 渲染。
	// 节点 = 每条规则,边 = 同 dimension / 同 scene 关系。
	func rulesGraph(c *gin.Context) {
		rules, err := store.GetAllRules()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 按 dimension 聚类,分配位置(圆环布局)
		dimGroups := map[string][]store.RuleRow{}
		for _, r := range rules {
			dimGroups[r.Dimension] = append(dimGroups[r.Dimension], r)
		}

		type GraphNode struct {
			ID        string  `json:"id"`
			Name      string  `json:"name"`
			Dimension string  `json:"dimension"`
			Group     string  `json:"group"`   // 聚类维度
			X         float64 `json:"x"`
			Y         float64 `json:"y"`
			Color     string  `json:"color"`
			Score     float64 `json:"score"`    // 权重(控制大小)
		}
		type GraphEdge struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Label  string `json:"label"`  // 关系类型
			Type   string `json:"type"`   // same_dim | related | derived
		}

		// 维度颜色表
		dimColors := map[string]string{
			"security":     "#f56c6c",
			"performance":  "#e6a23c",
			"architecture": "#409eff",
			"product":      "#67c23a",
			"correctness":  "#9c27b0",
		}

	// 按 dimension 环形分布节点
	dimNames := make([]string, 0, len(dimGroups))
	for d := range dimGroups {
		dimNames = append(dimNames, d)
	}

	nodes := []GraphNode{}
	edges := []GraphEdge{}
	centerX, centerY := 400.0, 300.0
	ringRadius := 200.0

	// 规则 ID 集合(用于过滤指向不存在规则的关系边)
	ruleExists := map[string]bool{}
	for di, dim := range dimNames {
		group := dimGroups[dim]
		angle := float64(di) * 2 * 3.14159 / float64(len(dimNames))
		dimCX := centerX + ringRadius*cos(angle)
		dimCY := centerY + ringRadius*sin(angle)

		for j, rule := range group {
			ruleExists[rule.ID] = true
			// 子节点在维度圆心周围散开
			nodeAngle := float64(j) * 2 * 3.14159 / float64(len(group))
			nodeR := 50.0 + float64(len(group))*2.0
			nx := dimCX + nodeR*cos(nodeAngle)
			ny := dimCY + nodeR*sin(nodeAngle)

			nodes = append(nodes, GraphNode{
				ID:        rule.ID,
				Name:      rule.Name,
				Dimension: rule.Dimension,
				Group:     dim,
				X:         nx,
				Y:         ny,
				Color:     dimColors[dim],
				Score:     rule.Weight,
			})
		}
	}

	// 边的来源(MRD §7.3 知识图谱关系边):
	//   1. 优先:从 rule_relations 表读真实关系边
	//   2. 回退:关系表为空时,才用"同维度聚类"启发式边(保证空库也有图)
	relations, _ := store.GetAllRuleRelations()
	relationLabel := map[string]string{
		"related_to":         "关联",
		"belong_to_category": "同类",
		"derived_from":       "派生",
	}
	for _, rel := range relations {
		if !ruleExists[rel.SourceRuleID] || !ruleExists[rel.TargetRuleID] {
			continue // 过滤悬空边(指向已删除规则的边)
		}
		label := relationLabel[rel.RelationType]
		if label == "" {
			label = "关联"
		}
		edges = append(edges, GraphEdge{
			Source: rel.SourceRuleID,
			Target: rel.TargetRuleID,
			Label:  label,
			Type:   "related",
		})
	}

	// 无真实关系边时,回退到同维度聚类边(让空库也能展示图谱)
	if len(edges) == 0 {
		for _, dim := range dimNames {
			group := dimGroups[dim]
			for i := 0; i < len(group); i++ {
				for j := i + 1; j < len(group); j++ {
					edges = append(edges, GraphEdge{
						Source: group[i].ID,
						Target: group[j].ID,
						Label:  "同维度",
						Type:   "same_dim",
					})
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes": nodes,
		"edges": edges,
		"dims":  dimNames,
		"total": len(nodes),
	})
}

		// cos / sin 辅助
		func cos(x float64) float64 { return math.Cos(x) }
		func sin(x float64) float64 { return math.Sin(x) }

// ── 规则关系边 CRUD(MRD §7.3 知识图谱) ──

// listRuleRelations GET /api/rules/relations
// 列出全部规则关系边。
func listRuleRelations(c *gin.Context) {
	relations, err := store.GetAllRuleRelations()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if relations == nil {
		relations = []store.RuleRelation{}
	}
	c.JSON(http.StatusOK, relations)
}

// createRuleRelation POST /api/rules/relations
// 新建一条规则关系边。
// Body: { source_rule_id, target_rule_id, relation_type, weight }
func createRuleRelation(c *gin.Context) {
	var req struct {
		SourceRuleID string  `json:"source_rule_id" binding:"required"`
		TargetRuleID string  `json:"target_rule_id" binding:"required"`
		RelationType string  `json:"relation_type"`
		Weight       float64 `json:"weight"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RelationType == "" {
		req.RelationType = "related_to"
	}
	if req.Weight == 0 {
		req.Weight = 1.0
	}
	id := uuid.New().String()
	if err := store.InsertRuleRelation(id, req.SourceRuleID, req.TargetRuleID, req.RelationType, req.Weight); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id": id, "source_rule_id": req.SourceRuleID, "target_rule_id": req.TargetRuleID,
		"relation_type": req.RelationType, "weight": req.Weight,
	})
}

// deleteRuleRelation DELETE /api/rules/relations/:id
func deleteRuleRelation(c *gin.Context) {
	id := c.Param("id")
	if err := store.DeleteRuleRelation(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}
