package api

// expansion_pipeline.go — 方向1: Agent CI/CD Pipeline API handler。

import (
	"encoding/json"
	"net/http"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func listPipelines(c *gin.Context) {
	rows, err := store.DB.Query(`SELECT id, name, COALESCE(agent_id,''), COALESCE(trigger_type,'manual'),
		COALESCE(scene,'agent'), COALESCE(rule_set_id,''), COALESCE(enabled,1) FROM pipelines ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type Pipeline struct {
		ID, Name, AgentID, TriggerType, Scene, RuleSetID string
		Enabled                                          int
	}
	var items []Pipeline
	for rows.Next() {
		var p Pipeline
		rows.Scan(&p.ID, &p.Name, &p.AgentID, &p.TriggerType, &p.Scene, &p.RuleSetID, &p.Enabled)
		items = append(items, p)
	}
	c.JSON(http.StatusOK, items)
}

func createPipeline(c *gin.Context) {
	var req struct {
		Name        string  `json:"name" binding:"required"`
		AgentID     string  `json:"agent_id"`
		Scene       string  `json:"scene"`
		RuleSetID   string  `json:"rule_set_id"`
		TriggerType string  `json:"trigger_type"`
		MinScore    float64 `json:"min_score"`
		MaxCost     float64 `json:"max_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scene == "" { req.Scene = "agent" }
	if req.TriggerType == "" { req.TriggerType = "manual" }
	gateConfig, _ := json.Marshal(map[string]float64{"min_score": req.MinScore, "max_cost": req.MaxCost})
	id := "pipe-" + uuid.New().String()[:8]
	store.DB.Exec(`INSERT INTO pipelines (id, name, agent_id, trigger_type, trigger_config, gate_config, scene, rule_set_id, enabled)
		VALUES (?, ?, ?, ?, '{}', ?, ?, ?, 1)`, id, req.Name, req.AgentID, req.TriggerType, string(gateConfig), req.Scene, req.RuleSetID)
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name})
}

func triggerPipeline(c *gin.Context) {
	id := c.Param("id")
	var p struct{ AgentID, Scene, GateConfig string }
	store.DB.QueryRow(`SELECT COALESCE(agent_id,''), COALESCE(scene,'agent'), COALESCE(gate_config,'{}') FROM pipelines WHERE id=?`, id).
		Scan(&p.AgentID, &p.Scene, &p.GateConfig)
	var gate struct {
		MinScore float64 `json:"min_score"`
		MaxCost  float64 `json:"max_cost"`
	}
	json.Unmarshal([]byte(p.GateConfig), &gate)
	go func() {
		config := engine.PipelineConfig{
			AgentID: p.AgentID, Scene: p.Scene,
			MinScore: gate.MinScore, MaxCost: gate.MaxCost,
		}
		engine.RunPipeline(config, "manual")
	}()
	c.JSON(http.StatusOK, gin.H{"triggered": id, "message": "管线已触发(异步执行)"})
}

func listPipelineRuns(c *gin.Context) {
	id := c.Param("id")
	rows, err := store.DB.Query(`SELECT id, status, score_correctness, score_security, score_performance,
		cost_usd, overall_passed, started_at, finished_at FROM pipeline_runs WHERE pipeline_id=? ORDER BY started_at DESC LIMIT 20`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type Run struct {
		ID, Status, StartedAt, FinishedAt string
		ScoreC, ScoreS, ScoreP, Cost      float64
		Passed                            int
	}
	var items []Run
	for rows.Next() {
		var r Run
		rows.Scan(&r.ID, &r.Status, &r.ScoreC, &r.ScoreS, &r.ScoreP, &r.Cost, &r.Passed, &r.StartedAt, &r.FinishedAt)
		items = append(items, r)
	}
	c.JSON(http.StatusOK, items)
}
