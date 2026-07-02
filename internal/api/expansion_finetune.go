package api

// expansion_finetune.go — 方向5: Fine-tuning Pipeline API handler。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func exportTrainingDataset(c *gin.Context) {
	var req struct {
		TaskID string `json:"task_id"`
		Format string `json:"format"`
		Scene  string `json:"scene"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Format == "" { req.Format = "both" }
	badCases := getBadCases(req.TaskID, req.Scene)
	sftPairs := generateSFTPairs(badCases)
	dpoPairs := generateDPOPairs(badCases)
	sftJSON, _ := json.Marshal(sftPairs)
	dpoJSON, _ := json.Marshal(dpoPairs)
	id := "ds-" + uuid.New().String()[:8]
	name := fmt.Sprintf("训练集(%s) %s", req.Format, time.Now().Format("01-02"))
	store.DB.Exec(`INSERT INTO training_datasets (id, name, task_id, scene, format, sft_pairs, dpo_pairs, bad_case_count, total_pairs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, name, req.TaskID, req.Scene, req.Format,
		string(sftJSON), string(dpoJSON), len(badCases), len(sftPairs)+len(dpoPairs))
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": name, "sft_pairs": len(sftPairs), "dpo_pairs": len(dpoPairs)})
}

func getBadCases(taskID, scene string) []map[string]any {
	var cases []map[string]any
	// 数据源:bad_cases 表(低分负样本)关联 questions 表(取 prompt + expected)
	// bad_cases 有: question_id / agent_id / answer / score
	query := `SELECT COALESCE(q.prompt,''), COALESCE(q.expected,''), COALESCE(bc.answer,''), bc.score
		FROM bad_cases bc
		LEFT JOIN questions q ON bc.question_id = q.id`
	args := []any{}
	where := []string{}
	if taskID != "" {
		where = append(where, "bc.task_id = ?")
		args = append(args, taskID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY bc.score ASC LIMIT 100"
	rows, _ := store.DB.Query(query, args...)
	if rows == nil {
		return cases
	}
	defer rows.Close()
	for rows.Next() {
		var prompt, response, expected string
		var score float64
		if err := rows.Scan(&prompt, &expected, &response, &score); err != nil {
			continue
		}
		cases = append(cases, map[string]any{
			"prompt": prompt, "expected": expected, "response": response, "score": score,
		})
	}
	return cases
}

func generateSFTPairs(badCases []map[string]any) []map[string]string {
	var pairs []map[string]string
	for _, bc := range badCases {
		prompt, _ := bc["prompt"].(string)
		expected, _ := bc["expected"].(string)
		if expected != "" {
			pairs = append(pairs, map[string]string{"prompt": prompt, "completion": expected})
		}
	}
	return pairs
}

func generateDPOPairs(badCases []map[string]any) []map[string]string {
	var pairs []map[string]string
	for _, bc := range badCases {
		prompt, _ := bc["prompt"].(string)
		expected, _ := bc["expected"].(string)
		response, _ := bc["response"].(string)
		if expected != "" && response != "" {
			pairs = append(pairs, map[string]string{"prompt": prompt, "chosen": expected, "rejected": response})
		}
	}
	return pairs
}

func listTrainingDatasets(c *gin.Context) {
	rows, _ := store.DB.Query(`SELECT id, name, format, bad_case_count, total_pairs, status, created_at FROM training_datasets ORDER BY created_at DESC`)
	type DS struct {
		ID, Name, Format, Status, CreatedAt string
		BadCases, TotalPairs                int
	}
	var items []DS
	for rows.Next() {
		var d DS
		rows.Scan(&d.ID, &d.Name, &d.Format, &d.BadCases, &d.TotalPairs, &d.Status, &d.CreatedAt)
		items = append(items, d)
	}
	rows.Close()
	c.JSON(http.StatusOK, items)
}
