package engine

// permission_mode.go — harness 权限模式(good-skills #37 补齐)。
//
// 补齐专家评审发现:permission_gate.go 只有 read-only/workspace-write/full 三级,
// 缺 Claude Code 风格的 default/acceptEdits/plan/bypassPermissions 模式 +
// allow/deny/ask 三态规则 + Tool(specifier) 解析。
//
// 与 permission_gate.go 的关系:
//   - permission_gate.go: 命令级门控(BashTool 执行 shell 前检查)→ 面向被测 Agent 沙箱
//   - permission_mode.go: harness 级模式(agent 整体行为模式) → 面向 harness 自身
// 两者互补,不冲突。

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HarnessMode harness 权限模式（Claude Code 风格四态）。
type HarnessMode string

const (
	ModeDefault        HarnessMode = "default"         // 每次操作都问
	ModeAcceptEdits    HarnessMode = "acceptEdits"     // 自动批准文件编辑(shell 仍问)
	ModePlan           HarnessMode = "plan"            // 只规划不执行(只读)
	ModeBypassPerms    HarnessMode = "bypassPermissions" // 跳过所有审批(最危险)
)

// RuleDecision 三态规则决策。
type RuleDecision string

const (
	RuleAllow RuleDecision = "allow"
	RuleDeny  RuleDecision = "deny"
	RuleAsk   RuleDecision = "ask"
)

// PermissionRule 一条权限规则:Tool(specifier)。
type PermissionRule struct {
	Tool      string // BashTool / Edit / Read / ...
	Specifier string // npm run build / docs/** / .env / *
}

// ParsePermissionRule 解析 "Bash(npm run build)" 格式。
func ParsePermissionRule(text string) PermissionRule {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "("); idx > 0 && strings.HasSuffix(text, ")") {
		return PermissionRule{Tool: text[:idx], Specifier: text[idx+1 : len(text)-1]}
	}
	return PermissionRule{Tool: text, Specifier: "*"}
}

// Matches 检查规则是否匹配工具调用。
func (r PermissionRule) Matches(toolName, args string) bool {
	if r.Tool != toolName {
		return false
	}
	if r.Specifier == "*" {
		return true
	}
	// BashTool specifier 匹配命令前缀
	if toolName == "BashTool" || toolName == "Bash" {
		prefix := strings.TrimSuffix(r.Specifier, "*")
		return strings.HasPrefix(args, prefix)
	}
	// Edit/Read specifier 匹配路径 glob(简化:前缀匹配)
	prefix := strings.TrimSuffix(r.Specifier, "*")
	return strings.HasPrefix(args, prefix)
}

// HarnessPermissionConfig harness 权限配置。
type HarnessPermissionConfig struct {
	Mode             HarnessMode     `json:"mode"`
	Allow            []PermissionRule `json:"allow"`
	Deny             []PermissionRule `json:"deny"`
	Ask              []PermissionRule `json:"ask"`
	DisableBypass    bool             `json:"disable_bypass"` // 安全锁:禁止 bypass 模式
}

// DefaultHarnessPermission 默认配置。
func DefaultHarnessPermission() HarnessPermissionConfig {
	return HarnessPermissionConfig{
		Mode: ModeDefault,
	}
}

// Evaluate 评估操作 → 决策(优先级 deny > ask > allow > mode)。
func (c *HarnessPermissionConfig) Evaluate(toolName, args string) RuleDecision {
	// plan mode:全部 ask(只读)
	if c.Mode == ModePlan {
		return RuleAsk
	}
	// bypass:全部 allow(若允许)
	if c.Mode == ModeBypassPerms && !c.DisableBypass {
		return RuleAllow
	}
	// deny 最高优先级
	for _, rule := range c.Deny {
		if rule.Matches(toolName, args) {
			return RuleDeny
		}
	}
	// ask 次之
	for _, rule := range c.Ask {
		if rule.Matches(toolName, args) {
			return RuleAsk
		}
	}
	// allow
	for _, rule := range c.Allow {
		if rule.Matches(toolName, args) {
			return RuleAllow
		}
	}
	// 回退到 mode
	if c.Mode == ModeAcceptEdits && (toolName == "Edit" || toolName == "FileWrite") {
		return RuleAllow
	}
	return RuleAsk // default mode → ask
}

// Validate 校验配置(返回错误列表)。
func (c *HarnessPermissionConfig) Validate() []string {
	var errors []string
	if c.Mode == ModeBypassPerms && c.DisableBypass {
		errors = append(errors, "disable_bypass 锁定,bypassPermissions 模式被禁止")
	}
	return errors
}

// FormatRules 格式化规则为可读字符串(供 UI 展示)。
func (c *HarnessPermissionConfig) FormatRules() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("模式: %s\n", c.Mode))
	if len(c.Allow) > 0 {
		sb.WriteString("允许: ")
		for i, r := range c.Allow {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%s)", r.Tool, r.Specifier))
		}
		sb.WriteString("\n")
	}
	if len(c.Deny) > 0 {
		sb.WriteString("拒绝: ")
		for i, r := range c.Deny {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%s)", r.Tool, r.Specifier))
		}
		sb.WriteString("\n")
	}
	if len(c.Ask) > 0 {
		sb.WriteString("询问: ")
		for i, r := range c.Ask {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%s)", r.Tool, r.Specifier))
		}
		sb.WriteString("\n")
	}
	if c.DisableBypass {
		sb.WriteString("🔒 bypassPermissions 已锁定\n")
	}
	return sb.String()
}

// ── D25 规则热加载(从 JSON 配置加载 + 运行时动态修改) ──
func LoadPermissionConfig(configJSON string) (*HarnessPermissionConfig, error) {
	if configJSON == "" {
		cfg := DefaultHarnessPermission()
		return &cfg, nil
	}
	var raw struct {
		Mode          string   `json:"mode"`
		Allow         []string `json:"allow"`
		Deny          []string `json:"deny"`
		Ask           []string `json:"ask"`
		DisableBypass bool     `json:"disable_bypass"`
	}
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return nil, fmt.Errorf("权限配置解析失败: %w", err)
	}
	cfg := &HarnessPermissionConfig{Mode: HarnessMode(raw.Mode), DisableBypass: raw.DisableBypass}
	if cfg.Mode == "" { cfg.Mode = ModeDefault }
	for _, r := range raw.Allow { cfg.Allow = append(cfg.Allow, ParsePermissionRule(r)) }
	for _, r := range raw.Deny { cfg.Deny = append(cfg.Deny, ParsePermissionRule(r)) }
	for _, r := range raw.Ask { cfg.Ask = append(cfg.Ask, ParsePermissionRule(r)) }
	return cfg, nil
}

// ── D26 审批状态缓存(已批准的命令前缀不重复问) ──
type ApprovalCache struct {
	approved map[string]bool
	denied   map[string]bool
	maxSize  int
}

var globalApprovalCache = &ApprovalCache{approved: make(map[string]bool), denied: make(map[string]bool), maxSize: 100}

func (c *ApprovalCache) GetCachedApproval(command string) (RuleDecision, bool) {
	if c.denied[command] { return RuleDeny, true }
	if c.approved[command] { return RuleAllow, true }
	for prefix := range c.approved { if strings.HasPrefix(command, prefix) { return RuleAllow, true } }
	for prefix := range c.denied { if strings.HasPrefix(command, prefix) { return RuleDeny, true } }
	return RuleAsk, false
}

func (c *ApprovalCache) RecordApproval(command string, decision RuleDecision) {
	switch decision {
	case RuleAllow:
		if len(c.approved) >= c.maxSize { for k := range c.approved { delete(c.approved, k); break } }
		c.approved[command] = true
	case RuleDeny:
		if len(c.denied) >= c.maxSize { for k := range c.denied { delete(c.denied, k); break } }
		c.denied[command] = true
	}
}

func (c *ApprovalCache) Invalidate() { c.approved = make(map[string]bool); c.denied = make(map[string]bool) }

func (c *HarnessPermissionConfig) EvaluateWithCache(toolName, args string, cache *ApprovalCache) RuleDecision {
	if cache != nil {
		if decision, found := cache.GetCachedApproval(args); found { return decision }
	}
	decision := c.Evaluate(toolName, args)
	if cache != nil { cache.RecordApproval(args, decision) }
	return decision
}
