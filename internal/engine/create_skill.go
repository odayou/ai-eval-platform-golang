package engine

// create_skill.go — 元技能:生成符合约定的新 skill(skill #16)。
//
// Agent 可以通过这个工具自主创建新 skill 目录(SKILL.md + frontmatter)。
// 设计参考 claw-code skills/create-skill/。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SkillSpec 新 skill 的规格。
type SkillSpec struct {
	Name        string   // skill 名称(如 "deploy-helper")
	Description string   // 一句话描述
	Category    string   // 分类(coding/security/devops/analysis/...)
	Steps       []string // 步骤描述(写入 SKILL.md body)
}

// CreateSkillDir 创建新 skill 目录 + SKILL.md。
// skillsDir 是 skills 根目录(如 ./skills 或 ~/.skills)。
func CreateSkillDir(spec SkillSpec, skillsDir string) (string, error) {
	if spec.Name == "" {
		return "", fmt.Errorf("skill name 不能为空")
	}

	// 目录名:用 name(转小写,空格转连字符)
	dirName := strings.ToLower(strings.ReplaceAll(spec.Name, " ", "-"))
	skillDir := filepath.Join(skillsDir, dirName)

	// 如果已存在,报错
	if _, err := os.Stat(skillDir); err == nil {
		return "", fmt.Errorf("skill %q 已存在: %s", dirName, skillDir)
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 生成 SKILL.md
	content := renderSkillMD(spec)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}

	return skillDir, nil
}

// renderSkillMD 生成 SKILL.md 内容(frontmatter + body)。
func renderSkillMD(spec SkillSpec) string {
	var sb strings.Builder

	// frontmatter
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", strings.ToLower(strings.ReplaceAll(spec.Name, " ", "-"))))
	sb.WriteString(fmt.Sprintf("description: %s\n", spec.Description))
	if spec.Category != "" {
		sb.WriteString(fmt.Sprintf("category: %s\n", spec.Category))
	}
	sb.WriteString(fmt.Sprintf("created: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString("---\n\n")

	// title
	sb.WriteString(fmt.Sprintf("# %s\n\n", spec.Name))
	sb.WriteString(spec.Description + "\n\n")

	// steps
	if len(spec.Steps) > 0 {
		sb.WriteString("## 步骤\n\n")
		for i, step := range spec.Steps {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
		sb.WriteString("\n")
	}

	// 占位:让 agent 后续补充实现
	sb.WriteString("## 实现细节\n\n")
	sb.WriteString("> TODO: 补充具体实现逻辑。\n")

	return sb.String()
}

// CreateSkillToolDef 暴露为 Agent 可调用的工具。
func CreateSkillToolDef() ToolDef {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "skill 名称"},
			"description": map[string]any{"type": "string", "description": "一句话描述"},
			"category":    map[string]any{"type": "string", "description": "分类"},
			"steps": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "步骤描述数组",
			},
		},
		"required": []string{"name", "description"},
	}
	paramsJSON, _ := json.Marshal(params)
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "CreateSkill",
			Description: "创建一个新的 skill 目录(含 SKILL.md)。用于沉淀可复用的工作流程。",
			Parameters:  paramsJSON,
		},
	}
}
