package engine

// execpolicy.go — 声明式命令授权规则(skill #20)。
// 轻量级规则引擎(非完整 Starlark DSL,而是 prefix + glob 匹配)。
// 多规则取最严(deny 优先于 allow)。

import (
	"path/filepath"
	"strings"
)

// ExecPolicyRule 单条命令授权规则。
type ExecPolicyRule struct {
	Pattern string // 匹配模式(前缀或 glob,如 "git *" / "rm -rf *")
	Action  string // allow / deny / ask
}

// ExecPolicy 命令授权策略。
type ExecPolicy struct {
	rules []ExecPolicyRule
}

// NewExecPolicy 创建空策略。
func NewExecPolicy() *ExecPolicy {
	return &ExecPolicy{}
}

// AddRule 添加规则(后添加的优先级更高)。
func (p *ExecPolicy) AddRule(pattern, action string) {
	p.rules = append(p.rules, ExecPolicyRule{Pattern: pattern, Action: action})
}

// Evaluate 评估命令,返回 allow/deny/ask。
// 多规则取最严:deny > ask > allow。
func (p *ExecPolicy) Evaluate(command string) string {
	result := "allow" // 默认允许
	for _, rule := range p.rules {
		if matchCommand(rule.Pattern, command) {
			// 取最严
			if rule.Action == "deny" {
				return "deny"
			}
			if rule.Action == "ask" && result == "allow" {
				result = "ask"
			}
		}
	}
	return result
}

// matchCommand 检查命令是否匹配模式。
// 支持前缀匹配("git ")和 glob 匹配("rm -rf *")。
func matchCommand(pattern, command string) bool {
	// glob 模式(含 *)
	if strings.Contains(pattern, "*") {
		matched, _ := filepath.Match(pattern, command)
		return matched
	}
	// 前缀匹配
	return strings.HasPrefix(command, pattern)
}

// DefaultExecPolicy 返回默认安全策略。
func DefaultExecPolicy() *ExecPolicy {
	p := NewExecPolicy()
	// 危险命令 deny
	p.AddRule("rm -rf /", "deny")
	p.AddRule("mkfs", "deny")
	p.AddRule("dd if=/dev/", "deny")
	p.AddRule(":(){ :|:& };:", "deny") // fork bomb
	// 写操作 ask
	p.AddRule("rm ", "ask")
	p.AddRule("mv ", "ask")
	p.AddRule("chmod 777", "ask")
	// 只读操作 allow(默认)
	return p
}
