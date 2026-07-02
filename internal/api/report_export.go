package api

// report_export.go — 评测报告导出(Markdown)。
//
// 从任务的 result JSON 生成结构化 Markdown 报告,供汇报/归档/分享场景。
// GET /api/tasks/:id/export?format=md

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
)

// exportTaskReport GET /api/tasks/:id/export?format=md
func exportTaskReport(c *gin.Context) {
	id := c.Param("id")
	// format 参数当前固定 md(预留扩展);从 query 读但不强制校验,默认生成 Markdown
	_ = c.DefaultQuery("format", "md")

	// 读任务(用 store.GetTask,字段齐全:name/scene/agentID/ruleSet/status/result)
	t, err := store.GetTask(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	name := t.Name
	scene := t.Scene
	agentID := t.AgentID
	ruleSet := t.RuleSet
	status := t.Status
	resultJSON := t.Result

	// 读 Agent 名
	agentName := agentID
	if agentID != "" {
		if a, _ := store.GetAgent(agentID); a != nil {
			agentName = a.Name
		}
	}

	// 生成 Markdown
	md := buildReportMarkdown(name, scene, agentName, ruleSet, status, resultJSON, id)

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%s.md"`, sanitizeFilename(name)))
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
}

// buildReportMarkdown 从 result JSON 生成 Markdown 报告。
func buildReportMarkdown(name, scene, agent, ruleSet, status, resultJSON, taskID string) string {
	var sb strings.Builder

	// 标题
	sb.WriteString(fmt.Sprintf("# %s — 评测报告\n\n", name))
	sb.WriteString(fmt.Sprintf("> 生成时间: %s | 任务ID: `%s`\n\n", time.Now().Format("2006-01-02 15:04:05"), taskID))

	// 基础信息
	sb.WriteString("## 基本信息\n\n")
	sb.WriteString(fmt.Sprintf("| 项目 | 值 |\n|---|---|\n"))
	sb.WriteString(fmt.Sprintf("| 被测 Agent | %s |\n", agent))
	sb.WriteString(fmt.Sprintf("| 评测场景 | %s |\n", scene))
	sb.WriteString(fmt.Sprintf("| 规则集 | %s |\n", ruleSet))
	sb.WriteString(fmt.Sprintf("| 状态 | %s |\n\n", status))

	// 解析 result JSON
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil || result == nil {
		sb.WriteString("> 该任务暂无评测结果数据(可能尚未完成)。\n")
		return sb.String()
	}

	// 综合分
	overall, _ := result["overall"].(float64)
	mockMode, _ := result["mock_mode"].(bool)
	sb.WriteString("## 综合评分\n\n")
	sb.WriteString(fmt.Sprintf("**综合分: %.1f / 100**", overall))
	if mockMode {
		sb.WriteString(" ⚠️ (mock 模式,非真实评测)")
	}
	sb.WriteString("\n\n")

	// 维度得分
	if dims, ok := result["dimensions"].(map[string]any); ok && len(dims) > 0 {
		sb.WriteString("## 维度得分\n\n")
		sb.WriteString("| 维度 | 分数 | 说明 |\n|---|---|---|\n")
		for dim, v := range dims {
			score := 0.0
			reason := ""
			if m, ok := v.(map[string]any); ok {
				if s, ok := m["score"].(float64); ok {
					score = s
				}
				if r, ok := m["reason"].(string); ok {
					reason = r
				}
			} else if s, ok := v.(float64); ok {
				score = s
			}
			sb.WriteString(fmt.Sprintf("| %s | %.1f | %s |\n", dim, score, truncateStr(reason, 100)))
		}
		sb.WriteString("\n")
	}

	// 题目明细(字段名是 per_question,不是 questions)
	if questions, ok := result["per_question"].([]any); ok && len(questions) > 0 {
		sb.WriteString(fmt.Sprintf("## 题目明细 (%d 题)\n\n", len(questions)))
		for i, q := range questions {
			qm, ok := q.(map[string]any)
			if !ok {
				continue
			}
			score, _ := qm["score"].(float64)
			isBad, _ := qm["is_bad_case"].(bool)
			title, _ := qm["title"].(string)
			if title == "" {
				title = fmt.Sprintf("第 %d 题", i+1)
			}
			tag := ""
			if isBad {
				tag = " ❌ Bad Case"
			}
			sb.WriteString(fmt.Sprintf("### %d. %s — %.1f%s\n\n", i+1, title, score, tag))

			if ans, ok := qm["answer"].(string); ok && ans != "" {
				sb.WriteString("**回答:**\n\n```\n")
				sb.WriteString(truncateStr(ans, 500))
				sb.WriteString("\n```\n\n")
			}

			// 规则打分明细(字段名是 rule_scores,不是 rules)
			if rules, ok := qm["rule_scores"].([]any); ok {
				for _, r := range rules {
					rm, ok := r.(map[string]any)
					if !ok {
						continue
					}
					rscore, _ := rm["score"].(float64)
					rname, _ := rm["dimension"].(string)
					rreason, _ := rm["reason"].(string)
					sb.WriteString(fmt.Sprintf("- **%s**: %.1f — %s\n", rname, rscore, truncateStr(rreason, 150)))
				}
				sb.WriteString("\n")
			}
		}
	}

	// 沙箱汇总
	if sandbox, ok := result["sandbox"].(map[string]any); ok && len(sandbox) > 0 {
		sb.WriteString("## 沙箱执行\n\n")
		sb.WriteString(fmt.Sprintf("| 指标 | 值 |\n|---|---|\n"))
		for k, v := range sandbox {
			sb.WriteString(fmt.Sprintf("| %s | %v |\n", k, v))
		}
		sb.WriteString("\n")
	}

	// 断路器
	if breaker, ok := result["breaker"].(map[string]any); ok {
		sb.WriteString("## 断路器\n\n")
		sb.WriteString(fmt.Sprintf("| 指标 | 值 |\n|---|---|\n"))
		for k, v := range breaker {
			sb.WriteString(fmt.Sprintf("| %s | %v |\n", k, v))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n*由 AI Eval Platform 自动生成*\n")
	return sb.String()
}

// truncateStr 截断字符串。
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// sanitizeFilename 清理文件名中的非法字符。
func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, ":", "")
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}
