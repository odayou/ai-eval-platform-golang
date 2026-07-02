package engine

// acp_backend.go — ACP 多 Agent 后端(skill #53)。
// agent_kind 判别 + 外部 CLI 后端适配(claude/codex/gemini)。

import (
	"fmt"
	"os/exec"
	"strings"
)

// AgentKind Agent 后端类型。
type AgentKind string

const (
	KindClaude AgentKind = "claude" // Claude Code CLI
	KindCodex  AgentKind = "codex"  // Codex CLI
	KindGemini AgentKind = "gemini" // Gemini CLI
	KindNative AgentKind = "native" // 本平台内置
)

// ACPBackend 单个 Agent 后端配置。
type ACPBackend struct {
	Kind        AgentKind
	CLIPath     string // CLI 可执行文件路径
	BaseCommand string // 基础命令(如 "claude" / "codex")
	Available   bool   // 是否可用(CLI 存在)
}

// DetectAgentKind 从命令/配置判别 agent 类型。
func DetectAgentKind(command string) AgentKind {
	low := strings.ToLower(command)
	switch {
	case strings.Contains(low, "claude"):
		return KindClaude
	case strings.Contains(low, "codex"):
		return KindCodex
	case strings.Contains(low, "gemini"):
		return KindGemini
	default:
		return KindNative
	}
}

// DetectACPBackends 检测系统可用的 ACP 后端。
func DetectACPBackends() []ACPBackend {
	candidates := []ACPBackend{
		{Kind: KindClaude, BaseCommand: "claude", CLIPath: "claude"},
		{Kind: KindCodex, BaseCommand: "codex", CLIPath: "codex"},
		{Kind: KindGemini, BaseCommand: "gemini", CLIPath: "gemini"},
	}
	for i := range candidates {
		if _, err := exec.LookPath(candidates[i].CLIPath); err == nil {
			candidates[i].Available = true
		}
	}
	// 本平台始终可用
	candidates = append(candidates, ACPBackend{
		Kind: KindNative, BaseCommand: "eval-platform", Available: true,
	})
	return candidates
}

// InvokeACPBackend 调用外部 ACP 后端 CLI(简化版:subprocess + 超时)。
func InvokeACPBackend(backend ACPBackend, prompt string) (string, error) {
	if !backend.Available {
		return "", fmt.Errorf("backend %s 不可用", backend.Kind)
	}
	cmd := exec.Command(backend.CLIPath, "--print", prompt)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ACP 调用失败(%s): %w", backend.Kind, err)
	}
	return string(output), nil
}
