package engine

// command_alias.go — 命令别名归一化与来源分类(skill #15)。
//
// 移植自 claw-code src/commands.py + src/command_graph.py。
// 功能:
//   1. 命令别名查表归一化(如 ls/list → list,rm/del → delete)
//   2. source_hint 软分类(builtins/plugin_like/skill_like/mcp_like)
//   3. handled 统一成败旗标
//
// 用途: BashTool 执行命令前归一化别名,提升命令识别准确性。

import "strings"

// CommandAlias 别名表(大写不敏感)。新增别名只改这个 map,不改逻辑。
var commandAliases = map[string]string{
	// 常见缩写
	"ls":      "list",
	"dir":     "list",
	"del":     "delete",
	"rm":      "delete",
	"mv":      "move",
	"cp":      "copy",
	"cls":     "clear",
	"cat":     "read",
	"type":    "read",
	"grep":    "search",
	"findstr": "search",
	"md":      "mkdir",
	"rd":      "rmdir",
	// 工具别名
	"plugins":     "plugin",
	"marketplace": "plugin",
	"cmds":        "command",
	"help":        "?",
	"quit":        "exit",
}

// NormalizeCommand 归一化命令名(别名查表,大小写不敏感)。
// 调用方无感:needle = ALIASES.get(name, name)。
func NormalizeCommand(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := commandAliases[lower]; ok {
		return canonical
	}
	return lower
}

// CommandKind 命令来源分类。
type CommandKind string

const (
	KindBuiltin   CommandKind = "builtins"
	KindPlugin    CommandKind = "plugin_like"
	KindSkill     CommandKind = "skill_like"
	KindMCP       CommandKind = "mcp_like"
	KindUnknown   CommandKind = "unknown"
)

// ClassifyCommand 按 source_hint 子串软分类(不硬编码清单)。
// 含 'plugin' → plugin_like, 'skill'/'skills' → skill_like,
// 'mcp' → mcp_like, 其余 → builtins。
func ClassifyCommand(sourceHint string) CommandKind {
	hint := strings.ToLower(sourceHint)
	switch {
	case strings.Contains(hint, "plugin"):
		return KindPlugin
	case strings.Contains(hint, "skill"):
		return KindSkill
	case strings.Contains(hint, "mcp"):
		return KindMCP
	case strings.Contains(hint, "builtin") || hint == "":
		return KindBuiltin
	default:
		return KindUnknown
	}
}

// CommandExecution 命令执行回执(handled 是成败旗标)。
type CommandExecution struct {
	Name       string
	SourceHint string
	Handled    bool
	Message    string
}

// ExitCode 把 handled 映射成退出码(0=成功, 1=失败)。
func (e CommandExecution) ExitCode() int {
	if e.Handled {
		return 0
	}
	return 1
}
