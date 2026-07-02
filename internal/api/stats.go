package api

// stats.go — 数据看板的聚合统计接口。
//
// 看板页(11.5)需要平台级指标:总任务数、各状态分布、平均分、Bad Case 总量等。
// ①阶段提供基础计数;Pass@1 趋势、帕累托散点等需 ②引擎产出数据后补全。

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// dashboardStats GET /api/stats/dashboard
// 返回看板首页所需的聚合指标。
func dashboardStats(c *gin.Context) {
	stats := gin.H{}

	// 总任务数
	totalTasks, _ := store.CountTasks()
	stats["total_tasks"] = totalTasks

	// 各状态任务数(pending/running/done/failed/blown)
	statusStats, _ := store.TaskStatusStats()
	statusDist := gin.H{}
	for _, s := range statusStats {
		statusDist[s.Status] = s.Count
	}
	stats["status_distribution"] = statusDist

	// Bad Case 总量(仅 pending,即未处理的)
	var badCaseCount int
	store.DB.QueryRow("SELECT COUNT(*) FROM bad_cases WHERE status='pending'").Scan(&badCaseCount)
	stats["bad_case_total"] = badCaseCount

	// Agent 数、模型数、题库数、规则数(资源概览)
	agentCount, _ := store.CountAgents()
	modelCount, _ := store.CountModels()
	questionCount := store.CountQuestions()
	ruleCount, _ := store.CountRules()
	stats["agent_count"] = agentCount
	stats["model_count"] = modelCount
	stats["question_count"] = questionCount
	stats["rule_count"] = ruleCount

	// 平均综合分(done 任务的 result.overall 平均)
	var avgScore sql.NullFloat64
	store.DB.QueryRow(`SELECT AVG(CAST(json_extract(result, '$.overall') AS REAL))
		FROM tasks WHERE status='done' AND result IS NOT NULL`).Scan(&avgScore)
	if avgScore.Valid {
		stats["avg_score"] = math.Round(avgScore.Float64*10) / 10
	} else {
		stats["avg_score"] = 0
	}

	c.JSON(http.StatusOK, stats)
}

// scoreTrend GET /api/stats/score-trend
// 返回最近 N 个已完成任务的综合分趋势(供看板折线图)。
func scoreTrend(c *gin.Context) {
	limit := 20
	rows, err := store.DB.Query(`SELECT id, name, CAST(json_extract(result, '$.overall') AS REAL),
		created_at FROM tasks WHERE status='done' AND result IS NOT NULL
		ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var items []gin.H
	for rows.Next() {
		var id, name, created string
		var score float64
		if err := rows.Scan(&id, &name, &score, &created); err != nil {
			continue
		}
		items = append(items, gin.H{
			"id": id, "name": name, "score": math.Round(score*10) / 10,
			"created_at": created,
		})
	}
	// 反转让最早在前(折线图从左到右时间递增)
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	if items == nil {
			items = []gin.H{}
		}
		c.JSON(http.StatusOK, items)
	}

// leaderboard GET /api/stats/leaderboard
// 返回多 Agent 多模型综合分排名(排行榜)。
func leaderboard(c *gin.Context) {
	scene := c.DefaultQuery("scene", "agent")
	limit := 20

	rows, err := store.DB.Query(`SELECT t.id, t.name, t.agent_id, t.result, t.created_at,
		COALESCE(a.name, ''), COALESCE(a.model, '')
		FROM tasks t
		LEFT JOIN agents a ON t.agent_id = a.id
		WHERE t.status = 'done' AND t.result IS NOT NULL AND t.scene = ?
		ORDER BY t.created_at DESC`, scene)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type taskInfo struct {
		id, name, agentID, result, createdAt, agentName, model string
	}
	latestMap := map[string]taskInfo{}
	for rows.Next() {
		var v taskInfo
		rows.Scan(&v.id, &v.name, &v.agentID, &v.result, &v.createdAt, &v.agentName, &v.model)
		if v.agentID == "" { continue }
		if _, exists := latestMap[v.agentID]; !exists {
			latestMap[v.agentID] = v
		}
	}

	type entry struct {
		Rank        int                `json:"rank"`
		AgentID     string             `json:"agent_id"`
		AgentName   string             `json:"agent_name"`
		Model       string             `json:"model"`
		Overall     float64            `json:"overall"`
		Dimensions  map[string]float64 `json:"dimensions"`
		BadCases    int                `json:"bad_cases"`
		TotalQ      int                `json:"total_questions"`
		TaskName    string             `json:"task_name"`
		CompletedAt string             `json:"completed_at"`
	}

	var entries []entry
	for _, v := range latestMap {
		var resultMap map[string]interface{}
		if err := json.Unmarshal([]byte(v.result), &resultMap); err != nil { continue }

		overall, _ := resultMap["overall"].(float64)
		badCases, _ := resultMap["bad_cases"].(float64)
		totalQ, _ := resultMap["total_questions"].(float64)

		dims := map[string]float64{}
		if dimsRaw, ok := resultMap["dimensions"].(map[string]interface{}); ok {
			for k, val := range dimsRaw {
				if v, ok := val.(float64); ok { dims[k] = v }
			}
		}

		entries = append(entries, entry{
			AgentID: v.agentID, AgentName: v.agentName, Model: v.model,
			Overall: math.Round(overall*10) / 10, Dimensions: dims,
			BadCases: int(badCases), TotalQ: int(totalQ),
			TaskName: v.name, CompletedAt: v.createdAt,
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Overall > entries[j].Overall })
	for i := range entries { entries[i].Rank = i + 1 }
	if len(entries) > limit { entries = entries[:limit] }
	if entries == nil { entries = []entry{} }

	c.JSON(http.StatusOK, gin.H{"scene": scene, "total": len(entries), "entries": entries})
}
