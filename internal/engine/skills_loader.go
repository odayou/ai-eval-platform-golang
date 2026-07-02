package engine

// skills_loader.go — harness 第3层:Skills 按需加载知识包。
//
// 从 skills 表读取 agent 配置的 skill,把内容注入 system prompt。
//
// 研究依据:
//   - Claude Code .claude/ 的 skill_listing 动态注入(agent 先看 name+desc,触发时加载全文)
//   - learn-harness-engineering skill-runtime-pattern:渐进式披露(progressive disclosure)
//   - harness-engineering concepts/06:Skills = 渐进式披露,按需加载知识包
//
// 实现策略(渐进式披露):
//   1. agent 启动时把引用的 skill 的 name+description 作为"可用技能清单"注入 system prompt
//   2. agent 可在 ReAct 循环里通过 LoadSkill 工具按需加载某个 skill 的完整内容
//   3. 这样避免一次性把所有 skill 全文塞进 prompt(token 浪费)

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// SkillSummary skill 摘要(注入 system prompt 的"可用技能清单")。
type SkillSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SkillContent skill 完整内容(LoadSkill 工具加载后返回)。
type SkillContent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// LoadSkillSummaries 从 skills 表读 skill 摘要列表(注入 system prompt 用)。
//
// skillIDs: agent.SkillIDs 字段(JSON 数组,引用 skills.id)
// 若 skillIDs 为空或解析失败,返回 nil(无 skill 注入)。
func LoadSkillSummaries(skillIDsJSON string) []SkillSummary {
	if skillIDsJSON == "" || skillIDsJSON == "[]" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(skillIDsJSON), &ids); err != nil {
		return nil
	}
	if len(ids) == 0 {
		return nil
	}

	var summaries []SkillSummary
	for _, id := range ids {
		var name, desc string
		store.DB.QueryRow(`SELECT id, name, COALESCE(description,'') FROM skills WHERE id = ?`, id).
			Scan(&id, &name, &desc)
		if name != "" {
			summaries = append(summaries, SkillSummary{ID: id, Name: name, Description: desc})
		}
	}
	return summaries
}

// InjectSkillsIntoSystem 把 skill 摘要清单注入 system prompt。
//
// 格式:
//
//	【可用技能(Skills)】
//	你可以在对话中用 LoadSkill 工具加载以下技能的完整内容:
//	- skill-name: skill description
//	- ...
//
//	需要某技能的详细指引时,调 LoadSkill(skill_id="xxx") 加载。
func InjectSkillsIntoSystem(systemPrompt string, summaries []SkillSummary) string {
	if len(summaries) == 0 {
		return systemPrompt
	}
	skillSection := "\n\n【可用技能(Skills)】\n你可以在对话中用 LoadSkill 工具加载以下技能的完整内容:\n"
	for _, s := range summaries {
		skillSection += fmt.Sprintf("- %s (id=%s): %s\n", s.Name, s.ID, s.Description)
	}
	skillSection += "\n需要某技能的详细指引时,调 LoadSkill(skill_id=\"xxx\") 加载其完整内容。"
	return systemPrompt + skillSection
}

// LoadSkillToolDef 返回 LoadSkill 工具声明(暴露给 agent 的按需加载工具)。
func LoadSkillToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "LoadSkill",
			Description: "加载一个技能(skill)的完整内容。技能包含领域知识、工作流、最佳实践。需要某个技能的详细指引时调用。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"skill_id": {"type": "string", "description": "技能 ID(从 system prompt 的可用技能清单获取)"}
				},
				"required": ["skill_id"]
			}`),
		},
	}
}

// ExecuteLoadSkill 执行 LoadSkill 工具:读 skill 完整内容返回。
func ExecuteLoadSkill(args json.RawMessage) ToolResult {
	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return ToolResult{Name: "LoadSkill", Output: "参数解析失败", IsError: true}
	}
	skillID, _ := argsMap["skill_id"].(string)
	if skillID == "" {
		return ToolResult{Name: "LoadSkill", Output: "缺少 skill_id 参数", IsError: true}
	}

	var name, content string
	err := store.DB.QueryRow(`SELECT name, COALESCE(content,'') FROM skills WHERE id = ?`, skillID).
		Scan(&name, &content)
	if err != nil {
		return ToolResult{Name: "LoadSkill", Output: "技能不存在: " + skillID, IsError: true}
	}
	if content == "" {
		return ToolResult{Name: "LoadSkill", Output: "技能内容为空: " + name, IsError: true}
	}
	return ToolResult{Name: "LoadSkill", Output: fmt.Sprintf("【技能: %s】\n%s", name, content)}
}

// BuildSkillSystemPrompt 组装含 skill 清单的 system prompt(供 agent_loop 调用)。
func BuildSkillSystemPrompt(baseSystemPrompt, agentsMD, skillIDsJSON string) string {
	system := agentsMD
	if system != "" {
		system += "\n\n"
	}
	system += baseSystemPrompt
	summaries := LoadSkillSummaries(skillIDsJSON)
	return InjectSkillsIntoSystem(system, summaries)
}

// ── D38 三级 skill 来源(managed/user/project,仿 Claude Code) ──
//
// Claude Code 的 skill 从三个目录加载:
//   managed: ProgramData/...skills(管理员装,全局)
//   user:    ~/.claude/skills(用户级)
//   project: .claude/skills(项目级,入 git)
// 本函数从文件系统扫描这三个目录的 SKILL.md。

// SkillSourceFile 从文件系统发现的 skill。
type SkillSourceFile struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // managed|user|project
	Content     string `json:"content"`
}

// DiscoverSkillsFromFilesystem D38 从文件系统三级目录发现 skill。
func DiscoverSkillsFromFilesystem(cwd string) []SkillSourceFile {
	var skills []SkillSourceFile

	// 三级目录(优先级:project > user > managed)
	dirs := []struct{ path, source string }{
		{filepath.Join(cwd, ".claude", "skills"), "project"},
		{filepath.Join(homeDir(), ".claude", "skills"), "user"},
		{filepath.Join(managedDir(), "skills"), "managed"},
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillMD := filepath.Join(dir.path, entry.Name(), "SKILL.md")
			data, err := os.ReadFile(skillMD)
			if err != nil {
				continue
			}
			text := string(data)
			// 解析 frontmatter(name + description)
			name := entry.Name()
			desc := ""
			if strings.HasPrefix(text, "---") {
				end := strings.Index(text[3:], "---")
				if end > 0 {
					fm := text[3 : 3+end]
					for _, line := range strings.Split(fm, "\n") {
						if strings.HasPrefix(line, "name:") {
							name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
						}
						if strings.HasPrefix(line, "description:") {
							desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
						}
					}
				}
			}
			skills = append(skills, SkillSourceFile{
				Path: skillMD, Name: name, Description: desc,
				Source: dir.source, Content: text,
			})
		}
	}
	return skills
}

// D39 触发条件匹配(从 description 里提取触发词,匹配时才注入该 skill)。
//
// Claude Code 的 skill 用 description 描述"何时使用",
// agent 看到清单后自己决定何时 LoadSkill。这里做预过滤:
// 如果用户 prompt 含 description 里的关键词,优先注入该 skill。
func MatchSkillsByPrompt(skills []SkillSummary, prompt string) []SkillSummary {
	if len(skills) == 0 || prompt == "" {
		return skills // 无条件返回全部
	}
	promptLower := strings.ToLower(prompt)
	var matched []SkillSummary
	for _, s := range skills {
		// 从 description 提取关键词(简化:按空格分词,取 >4 字符的)
		descLower := strings.ToLower(s.Description)
		words := strings.Fields(descLower)
		for _, w := range words {
			if len(w) > 4 && strings.Contains(promptLower, w) {
				matched = append(matched, s)
				break
			}
		}
	}
	if len(matched) == 0 {
		return skills // 无匹配则返回全部(不过度过滤)
	}
	return matched
}

// D40 skill 版本管理(检查文件 mtime,变化时使缓存失效)。
type SkillVersionCache struct {
	mu    sync.RWMutex
	cache map[string]time.Time // path → lastMod
}

var globalSkillCache = &SkillVersionCache{cache: make(map[string]time.Time)}

// CheckSkillChanged 检查 skill 是否变更(对比 mtime)。
func (c *SkillVersionCache) CheckSkillChanged(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return true // 读不到=变更(需重新加载)
	}
	lastMod, exists := c.cache[path]
	if !exists || !info.ModTime().Equal(lastMod) {
		c.cache[path] = info.ModTime()
		return true // 变更
	}
	return false // 未变更
}

// 辅助
func homeDir() string {
	dir, _ := os.UserHomeDir()
	return dir
}

func managedDir() string {
	// Windows: C:\ProgramData\ClaudeCode
	// Linux/Mac: /usr/local/share/claude-code
	return "/usr/local/share/claude-code"
}
