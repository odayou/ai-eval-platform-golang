package engine

// backpressure.go — harness 第6层:Back-Pressure(自我验证回路)。
//
// Agent loop 结束前(Stop 事件)跑验证命令(测试/lint/类型检查),
// 验证失败则反馈给 agent 继续修(回 agent loop),不直接停止。
//
// 研究依据:
//   - harness-engineering concepts/06 HumanLayer 六杠杆之6:
//     Back-Pressure = 测试/构建/类型检查 = 自我验证回路
//   - learn-harness-engineering gotchas:lint 错误信息 = 修复指令(让 agent 能自我纠正)
//   - harness-engineering thinking/harness-for-solo-developers:基础测试+lint 是最小 harness
//   - Claude Code 的 code-review/verify skill:Stop 前跑验证
//
// 实现策略:
//   - agent 配置 verify_command(如 "go test ./..." / "npm run build")
//   - agent loop 完成后,若配了 verify_command,跑它
//   - 验证通过 → 正常结束
//   - 验证失败 → 把错误反馈注入 messages,让 agent 再修一轮(最多 retryLimit 次)

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// VerifyConfig 验证配置。
type VerifyConfig struct {
	Command     string // 验证命令(如 "go test ./...")
	RetryLimit  int    // 验证失败后最多让 agent 重试几次(默认 2)
	WorkspaceRoot string // 工作目录
}

// VerifyResult 验证结果。
type VerifyResult struct {
	Passed  bool   `json:"passed"`
	Output  string `json:"output"`
	Retries int    `json:"retries"` // 重试了几次
}

// RunVerify 跑验证命令(如 go test / npm build)。
//
// 返回验证结果(通过/失败 + 输出)。
// 失败时输出会包含错误信息(供 agent 读取修正)。
func RunVerify(command, workspaceRoot string) VerifyResult {
	if command == "" {
		return VerifyResult{Passed: true, Output: "未配置验证命令,跳过"}
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	if workspaceRoot != "" {
		cmd.Dir = workspaceRoot
	}

	timer := time.AfterFunc(120*time.Second, func() { cmd.Process.Kill() })
	defer timer.Stop()

	output, err := cmd.CombinedOutput()
	out := string(output)
	if len(out) > 8000 {
		out = out[:8000] + "\n...(验证输出截断)"
	}

	if err != nil {
		return VerifyResult{
			Passed: false,
			Output: fmt.Sprintf("验证失败(%s):\n%s", err.Error(), out),
		}
	}
	return VerifyResult{Passed: true, Output: "验证通过:\n" + out}
}

// VerifyWithRetry 带 retry 的验证回路(核心 back-pressure 机制)。
//
// 流程:
//  1. 跑验证命令
//  2. 通过 → 返回成功
//  3. 失败 → 把错误反馈注入 agent 的 messages,让 agent 再跑一轮 ReAct
//  4. 循环直到通过或达 retryLimit
//
// 参数:
//   - verify: 验证配置
//   - messages: agent loop 的 messages(失败时追加验证反馈让 agent 修)
//   - llmCall: 调 LLM 的函数(供 agent 再跑一轮)
//
// 返回:最终验证结果 + 更新后的 messages。
func VerifyWithRetry(verify VerifyConfig, messages []loopMessage,
	llmCall func([]loopMessage) (string, error)) (VerifyResult, []loopMessage) {

	retryLimit := verify.RetryLimit
	if retryLimit <= 0 {
		retryLimit = 2
	}

	var result VerifyResult
	for attempt := 0; attempt <= retryLimit; attempt++ {
		result = RunVerify(verify.Command, verify.WorkspaceRoot)
		result.Retries = attempt

		if result.Passed {
			NewSlog("backpressure").Info("验证通过(尝试 /)", "attempt", attempt, "retryLimit", retryLimit)
			return result, messages
		}

		NewSlog("backpressure").Error("验证失败(尝试 /),反馈给 agent 修正", "attempt", attempt, "retryLimit", retryLimit)
		if attempt >= retryLimit {
			// 达 retry 上限,返回最终失败
			result.Output = fmt.Sprintf("验证在 %d 次重试后仍未通过:\n%s", retryLimit, result.Output)
			return result, messages
		}

		// 把验证失败反馈注入 messages,让 agent 修
		feedback := fmt.Sprintf("【验证失败,请修复】\n%s\n\n请根据上述错误修复问题,然后我会再次验证。", result.Output)
		messages = append(messages, loopMessage{Role: "user", Content: feedback})

		// 让 agent 跑一轮修
		_, err := llmCall(messages)
		if err != nil {
			NewSlog("backpressure").Error("agent 修正调用失败", "err", err)
			return result, messages
		}
	}
	return result, messages
}

// FormatVerifyFeedback 把验证失败格式化为给 agent 的反馈消息(harness 第6层)。
//
// 依据 learn-harness-engineering gotchas:lint 错误信息 = 修复指令。
// 不是干巴巴的"验证失败",而是告诉 agent 具体怎么修。
func FormatVerifyFeedback(result VerifyResult) string {
	if result.Passed {
		return ""
	}
	return fmt.Sprintf(`【Back-Pressure 验证未通过】

验证命令执行失败,请根据以下输出修复问题:

%s

修复指引:
- 仔细阅读上面的错误信息
- 定位失败的具体文件和行号
- 修复后确保验证能通过
- 不要引入新的问题`, result.Output)
}

// VerifyToolDef 返回 Verify 工具声明(agent 可主动调验证)。
func VerifyToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "Verify",
			Description: "运行自我验证(测试/lint/构建)。Agent 完成任务后应主动调用此工具确认产出质量。配置在 agent.verify_command。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
		},
	}
}

// ExecuteVerify 执行 Verify 工具。
func ExecuteVerify(command, workspaceRoot string) ToolResult {
	result := RunVerify(command, workspaceRoot)
	output := result.Output
	if !result.Passed {
		feedback := FormatVerifyFeedback(result)
		if feedback != "" {
			output = feedback
		}
	}
	return ToolResult{Name: "Verify", Output: output, IsError: !result.Passed}
}

// 消除未使用 import 警告(json 在 FormatVerifyFeedback 间接用)
var _ = json.Marshal
var _ = strings.TrimSpace
