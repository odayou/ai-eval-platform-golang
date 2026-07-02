package engine

// statusline.go — 状态栏自定义命令(skill #38)。
//
// harness 把自身内部状态(token 用量/模型/会话)通过 stdin JSON 喂给用户命令,
// 命令的 stdout 显示在状态栏。支持自定义命令 + 内置模板。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// StatusLineContext 喂给 statusLine 命令的上下文 JSON。
type StatusLineContext struct {
	Model         string             `json:"model"`
	SessionID     string             `json:"session_id"`
	Cwd           string             `json:"cwd"`
	ContextWindow ContextWindowInfo  `json:"context_window"`
	AgentID       string             `json:"agent_id,omitempty"`
	TaskID        string             `json:"task_id,omitempty"`
}

// ContextWindowInfo 上下文窗口用量信息。
type ContextWindowInfo struct {
	CurrentUsage int   `json:"current_usage"` // 已用 token
	MaxTokens    int   `json:"max_tokens"`    // 最大 token
}

// StatusLineConfig 状态栏配置。
type StatusLineConfig struct {
	Command string // shell 命令,收 stdin JSON,stdout 显示
}

// 内置 statusLine 模板
var StatusLineTemplates = map[string]string{
	"context_pct": `python3 -c "import sys,json;d=json.load(sys.stdin);u=d.get('context_window',{});pct=u.get('current_usage',0)*100//max(1,u.get('max_tokens',1));print(f'context: {pct}% | model: {d.get(\"model\",\"?\")}') "`,
	"simple":      `echo "agent ready"`,
	"with_git":    `python3 -c "import sys,json,subprocess;d=json.load(sys.stdin);u=d.get('context_window',{});pct=u.get('current_usage',0)*100//max(1,u.get('max_tokens',1));
try:
 branch=subprocess.check_output(['git','rev-parse','--abbrev-ref','HEAD'],cwd=d.get('cwd','.'),text=True,stderr=subprocess.DEVNULL).strip()
except: branch='-'
print(f'[{branch}] context: {pct}% | {d.get(\"model\",\"?\")}')"`,
}

// RenderStatusLine 执行 statusLine 命令,返回状态栏文本。
// 5 秒超时,失败降级为错误提示不阻塞。
func RenderStatusLine(config StatusLineConfig, ctx StatusLineContext) string {
	if config.Command == "" {
		return ""
	}

	stdinJSON, _ := json.Marshal(ctx)

	// 5 秒超时
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "sh", "-c", config.Command)
	cmd.Stdin = bytes.NewReader(stdinJSON)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return "[statusline: timeout]"
		}
		return fmt.Sprintf("[statusline error: %v]", err)
	}

	return stdout.String()
}

// FormatStatusLine 默认状态栏格式(不依赖外部命令,纯 Go 计算)。
func FormatStatusLine(model string, tokenUsed, tokenMax int, taskID string) string {
	pct := 0
	if tokenMax > 0 {
		pct = tokenUsed * 100 / tokenMax
	}
	task := ""
	if taskID != "" {
		task = " | task: " + taskID[:8]
	}
	return fmt.Sprintf("model: %s | context: %d%% (%d/%d)%s",
		model, pct, tokenUsed, tokenMax, task)
}
