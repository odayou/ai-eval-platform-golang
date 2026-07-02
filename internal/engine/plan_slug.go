package engine

// plan_slug.go — Plan Mode 产物持久化(skill #41)。
//
// plan mode(只读模式)已在 permission_mode.go 实现(ModePlan)。
// 本模块补齐 plan 产物的持久化:
//   - 三词随机 slug 生成(如 "amber-otter-leap")
//   - plans/ 目录管理
//   - Plan 结构化模板(Context/产出/步骤/验证)
//
// 用途:Agent 在 plan mode 下产出的计划可持久化、可追溯、可后续执行。

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PlanFile plan 产物的结构化模板。
type PlanFile struct {
	Slug        string   `json:"slug"`         // 三词随机标识(如 "amber-otter-leap")
	Title       string   `json:"title"`        // 计划标题
	CreatedAt   string   `json:"created_at"`   // 创建时间
	Context     string   `json:"context"`      // 背景与目标
	Deliverables []string `json:"deliverables"` // 产出物清单
	Steps       []PlanStep `json:"steps"`       // 步骤
	Verification string   `json:"verification"` // 验证标准
	AgentID     string   `json:"agent_id"`     // 产出该计划的 Agent
}

// PlanStep 单个步骤。
type PlanStep struct {
	Description string `json:"description"`
	Status      string `json:"status"` // pending | done | skipped
}

// slugWordPool 三词 slug 的词汇池(形容词+名词+动词)。
var slugAdjectives = []string{
	"amber", "azure", "coral", "crimson", "emerald", "golden", "indigo",
	"ivory", "jade", "lavender", "magenta", "onyx", "pearl", "quartz",
	"ruby", "sapphire", "silver", "teal", "violet", "cobalt",
}
var slugNouns = []string{
	"otter", "falcon", "harbor", "meadow", "summit", "river", "forest",
	"canyon", "glacier", "orchid", "phoenix", "raven", "willow", "zephyr",
	"badger", "compass", "lantern", "anchor", "beacon", "cedar",
}
var slugVerbs = []string{
	"leap", "soar", "drift", "spark", "forge", "bloom", "gleam",
	"ascend", "cascade", "converge", "diverge", "embrace", "ignite",
	"navigate", "pioneer", "resolve", "transcend", "unfold", "venture", "weave",
}

// GeneratePlanSlug 生成三词随机 slug(如 "amber-otter-leap")。
func GeneratePlanSlug() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%s-%s-%s",
		slugAdjectives[r.Intn(len(slugAdjectives))],
		slugNouns[r.Intn(len(slugNouns))],
		slugVerbs[r.Intn(len(slugVerbs))],
	)
}

// PlansDir 返回 plans/ 目录路径(workspace 相对)。
func PlansDir(workspace string) string {
	return filepath.Join(workspace, "plans")
}

// SavePlan 将 plan 写入 plans/<slug>.md(Markdown 格式)。
// 返回文件路径。
func SavePlan(plan PlanFile, workspace string) (string, error) {
	dir := PlansDir(workspace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建 plans 目录失败: %w", err)
	}

	if plan.Slug == "" {
		plan.Slug = GeneratePlanSlug()
	}

	filename := filepath.Join(dir, plan.Slug+".md")
	content := renderPlanMarkdown(plan)

	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入 plan 文件失败: %w", err)
	}

	return filename, nil
}

// renderPlanMarkdown 将 PlanFile 渲染为 Markdown。
func renderPlanMarkdown(p PlanFile) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Plan: %s\n\n", p.Title))
	sb.WriteString(fmt.Sprintf("> **slug**: `%s` | **created**: %s | **agent**: %s\n\n",
		p.Slug, p.CreatedAt, p.AgentID))

	sb.WriteString("## Context\n\n")
	sb.WriteString(p.Context + "\n\n")

	sb.WriteString("## Deliverables\n\n")
	for _, d := range p.Deliverables {
		sb.WriteString(fmt.Sprintf("- [ ] %s\n", d))
	}
	sb.WriteString("\n")

	sb.WriteString("## Steps\n\n")
	for i, s := range p.Steps {
		checkbox := "[ ]"
		if s.Status == "done" {
			checkbox = "[x]"
		} else if s.Status == "skipped" {
			checkbox = "[-]"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, checkbox, s.Description))
	}
	sb.WriteString("\n")

	sb.WriteString("## Verification\n\n")
	sb.WriteString(p.Verification + "\n")

	return sb.String()
}

// ListPlans 列出 workspace 下 plans/ 目录的所有 plan 文件。
func ListPlans(workspace string) ([]string, error) {
	dir := PlansDir(workspace)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var plans []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			plans = append(plans, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return plans, nil
}
