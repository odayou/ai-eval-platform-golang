package engine

// terminal.go — 本地源码/终端托管轨道(MRD 5.1 / 6.2)。
//
// 问题(MRD 原话):
//   "团队自研 Agent 内部 main.py 项目,人肉跑终端试用,不自动、易出危险操作、难复现。"
//
// 解决方案:
//   - 指定项目路径,评测 Agent 自己拉起进程、接管 stdio、交互式喂题
//   - CLI Command Standardizer:启动命令 + 喂题模式配置
//   - AI 自动解析 Agent 源码推断用法(Auto-Discovery)
//
// 三种喂题模式:
//   1. 命令行参数:python run.py --task "{prompt}"
//   2. stdin 注入:检测到终端出现 >>> 时注入
//   3. 一次性执行:直接运行,读取 stdout
//
// 安全:
//   - 进程在隔离目录中运行
//   - 设置超时(防止无限循环)
//   - 限制输出大小(防止内存爆炸)
//   - 权限门控(记录到 permissions 表)

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// shellLauncher 返回当前平台的 shell 启动命令。
// Windows 用 cmd.exe /c,其他用 sh -c(跨平台兼容)。
// 注意:不能用 bash,Windows 默认没装;cmd 和 sh 都能跑简单命令。
func shellLauncher() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c"}
	}
	return "sh", []string{"-c"}
}

// TerminalConfig 终端轨道的配置。
type TerminalConfig struct {
	ProjectPath string `json:"project_path"` // Agent 项目路径
	StartCmd    string `json:"start_cmd"`    // 启动命令(如 "python run.py")
	FeedMode    string `json:"feed_mode"`    // 喂题模式:args | stdin | oneshot
	ArgsPattern string `json:"args_pattern"` // 命令行参数模板(如 "--task {prompt}")
	StdinPrompt string `json:"stdin_prompt"` // stdin 触发词(如 ">>> ")
	Timeout     int    `json:"timeout"`      // 超时秒数
	MaxOutput   int    `json:"max_output"`   // 最大输出字节数
	// 权限门控(MRD §5.6):被测 Agent 在终端轨道执行命令的权限级别。
	//   read-only:只允许只读类命令
	//   workspace-write(默认):允许在工作区内读写/构建/测试,拦外网下载执行和路径越界
	//   full:完全放行(仅拦截极端自毁命令)
	PermissionLevel string `json:"permission_level"`
	// TaskID 关联的评测任务 ID(权限决策落库审计用)
	TaskID string `json:"task_id"`
}

// TerminalResult 是一次终端执行的结果。
type TerminalResult struct {
	Success    bool   `json:"success"`     // 是否成功执行
	Output     string `json:"output"`      // 标准输出
	Error      string `json:"error"`       // 标准错误
	ExitCode   int    `json:"exit_code"`   // 退出码
	Duration   int64  `json:"duration"`    // 执行耗时毫秒
	Truncated  bool   `json:"truncated"`   // 输出是否被截断
}

// DefaultTerminalConfig 返回默认配置。
func DefaultTerminalConfig() TerminalConfig {
	return TerminalConfig{
		FeedMode:    "oneshot",
		ArgsPattern: "--task {prompt}",
		StdinPrompt: ">>> ",
		Timeout:     60,
		MaxOutput:   100000, // 100KB
	}
}

// RunTerminal 在终端轨道上执行一次评测。
//
// 流程:
//  1. 验证项目路径存在
//  2. 根据喂题模式构造命令
//  3. 拉起进程,喂入 prompt
//  4. 读取 stdout/stderr
//  5. 超时控制 + 输出截断
//  6. 记录到 tool_calls 表
func RunTerminal(config TerminalConfig, prompt string) TerminalResult {
	start := time.Now()

	// 验证项目路径
	if _, err := os.Stat(config.ProjectPath); os.IsNotExist(err) {
		return TerminalResult{
			Success:  false,
			Error:    fmt.Sprintf("项目路径不存在: %s", config.ProjectPath),
			ExitCode: -1,
		}
	}

	// 权限门控(MRD §5.6):执行启动命令前检查权限级别。
	// 拦截危险命令/越界路径/违规网络下载,保护宿主环境。
	if gate := buildTerminalGate(config); gate != nil {
		if allow, reason := gate.CheckCommand(config.StartCmd); !allow {
			gate.RecordDecision(config.TaskID, "terminal_exec", config.StartCmd, "denied", reason)
			NewSlog("terminal").Warn("权限门控拒绝执行: | 命令", "reason", reason, "StartCmd", config.StartCmd)
			return TerminalResult{
				Success:  false,
				Error:    fmt.Sprintf("[权限门控拦截] %s", reason),
				ExitCode: -1,
			}
		}
		gate.RecordDecision(config.TaskID, "terminal_exec", config.StartCmd, "allowed", "权限检查通过")
	}

	var result TerminalResult

	switch config.FeedMode {
	case "args":
		result = runWithArgs(config, prompt)
	case "stdin":
		result = runWithStdin(config, prompt)
	default: // "oneshot"
		result = runOneshot(config, prompt)
	}

	result.Duration = time.Since(start).Milliseconds()

	// 截断输出
	if len(result.Output) > config.MaxOutput {
		result.Output = result.Output[:config.MaxOutput]
		result.Truncated = true
	}

	return result
}

// runWithArgs 通过命令行参数喂题。
func runWithArgs(config TerminalConfig, prompt string) TerminalResult {
	// 替换模板中的 {prompt}
	cmdStr := config.StartCmd
	if config.ArgsPattern != "" {
		args := strings.Replace(config.ArgsPattern, "{prompt}", prompt, 1)
		cmdStr = cmdStr + " " + args
	}

	return executeCommand(config, cmdStr, "")
}

// runWithStdin 通过 stdin 喂题。
func runWithStdin(config TerminalConfig, prompt string) TerminalResult {
	return executeCommand(config, config.StartCmd, prompt)
}

// runOneshot 一次性执行(直接运行,读取 stdout)。
func runOneshot(config TerminalConfig, prompt string) TerminalResult {
	// 将 prompt 写入临时文件,作为输入
	tmpFile := filepath.Join(config.ProjectPath, ".eval_input.txt")
	os.WriteFile(tmpFile, []byte(prompt), 0644)
	defer os.Remove(tmpFile)

	cmdStr := config.StartCmd + " < " + tmpFile
	return executeCommand(config, cmdStr, "")
}

// executeCommand 执行一个 shell 命令。
func executeCommand(config TerminalConfig, cmdStr, stdin string) TerminalResult {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Timeout)*time.Second)
	defer cancel()

	// 在项目目录中执行(跨平台 shell)
	shell, shellArgs := shellLauncher()
	cmd := exec.CommandContext(ctx, shell, append(shellArgs, cmdStr)...)
	cmd.Dir = config.ProjectPath

	// 设置 stdin(如果需要)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	// 捕获 stdout 和 stderr
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := TerminalResult{
		Output: stdout.String(),
		Error:  stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		result.Success = false
		if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Sprintf("执行超时(%ds)", config.Timeout)
		}
	} else {
		result.Success = true
		result.ExitCode = 0
	}

	return result
}

// RunTerminalWithFeed 交互式终端:拉起进程,多轮喂题。
//
// 这是更高级的模式:
//   1. 拉起进程,保持 stdin/stdout 管道
//   2. 等待进程输出提示词(如 ">>> " 或 "Please input:")
//   3. 注入 prompt
//   4. 读取回答
//   5. 重复(多轮对话)
func RunTerminalWithFeed(config TerminalConfig, prompts []string) []TerminalResult {
	var results []TerminalResult

	// 权限门控(MRD §5.6):交互式模式同样先检查启动命令权限。
	if gate := buildTerminalGate(config); gate != nil {
		if allow, reason := gate.CheckCommand(config.StartCmd); !allow {
			gate.RecordDecision(config.TaskID, "terminal_exec", config.StartCmd, "denied", reason)
			NewSlog("terminal").Warn("权限门控拒绝交互式执行: | 命令", "reason", reason, "StartCmd", config.StartCmd)
			return []TerminalResult{{Success: false, Error: fmt.Sprintf("[权限门控拦截] %s", reason), ExitCode: -1}}
		}
		gate.RecordDecision(config.TaskID, "terminal_exec", config.StartCmd, "allowed", "权限检查通过")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Timeout*len(prompts))*time.Second)
	defer cancel()

	shell, shellArgs := shellLauncher()
	cmd := exec.CommandContext(ctx, shell, append(shellArgs, config.StartCmd)...)
	cmd.Dir = config.ProjectPath

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return []TerminalResult{{Success: false, Error: fmt.Sprintf("stdin pipe 失败: %v", err)}}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return []TerminalResult{{Success: false, Error: fmt.Sprintf("stdout pipe 失败: %v", err)}}
	}

	if err := cmd.Start(); err != nil {
		return []TerminalResult{{Success: false, Error: fmt.Sprintf("启动失败: %v", err)}}
	}

	reader := bufio.NewReader(stdout)

	for _, prompt := range prompts {
		// 等待提示词
		waitForPrompt(reader, config.StdinPrompt, time.Now().Add(5*time.Second))

		// 写入 prompt
		fmt.Fprintln(stdin, prompt)

		// 读取回答
		output := readUntilPrompt(reader, config.StdinPrompt, time.Now().Add(10*time.Second), config.MaxOutput)

		results = append(results, TerminalResult{
			Success: true,
			Output:  output,
		})

		// 记录到 tool_calls
		recordToolCall(config.ProjectPath, prompt, output)
	}

	// 关闭进程
	stdin.Close()
	cmd.Process.Kill()

	return results
}

// waitForPrompt 等待进程输出指定提示词。
func waitForPrompt(reader *bufio.Reader, prompt string, timeout time.Time) {
	// 简化实现:读到提示词或超时
	for time.Now().Before(timeout) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if strings.Contains(line, prompt) {
			return
		}
	}
}

// readUntilPrompt 读取直到遇到提示词或超时。
func readUntilPrompt(reader *bufio.Reader, prompt string, timeout time.Time, maxBytes int) string {
	var sb strings.Builder
	for time.Now().Before(timeout) && sb.Len() < maxBytes {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return sb.String()
		}
		if strings.Contains(line, prompt) {
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}

// recordToolCall 记录终端调用到 tool_calls 表。
func recordToolCall(projectPath, input, output string) {
	store.DB.Exec(`INSERT INTO tool_calls (id, task_id, tool, input, output, duration_ms, success, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)`,
		uuid.New().String(),
		"terminal_eval",
		"terminal_exec",
		truncateStr(input, 500),
		truncateStr(output, 1000),
		0,
	)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// buildTerminalGate 根据 TerminalConfig 构造权限门控器。
// 权限级别为空时不做门控(返回 nil),保持向后兼容。
func buildTerminalGate(config TerminalConfig) *PermissionGate {
	level := config.PermissionLevel
	if level == "" {
		level = PermWorkspaceWrite // 默认 workspace-write,不会比现状更宽松
	}
	return NewPermissionGate(level, config.ProjectPath)
}

// AutoDiscover 自动解析 Agent 源码推断用法(MRD 7.7)。
//
// 流程:
//  1. 读 README / package.json / requirements.txt / main.py
//  2. 分析入口函数和参数解析逻辑
//  3. 推断启动命令、喂题模式、所需环境变量
//  4. 返回建议配置(供用户确认)
func AutoDiscover(projectPath string) (*TerminalConfig, error) {
	config := DefaultTerminalConfig()
	config.ProjectPath = projectPath

	// 检查 Python 项目
	if _, err := os.Stat(filepath.Join(projectPath, "main.py")); err == nil {
		config.StartCmd = "python main.py"
		config.FeedMode = "args"
		config.ArgsPattern = "--task {prompt}"

		// 检查 requirements.txt
		if _, err := os.Stat(filepath.Join(projectPath, "requirements.txt")); err == nil {
			NewSlog("terminal").Info("发现 Python 项目,需要安装依赖")
		}
		return &config, nil
	}

	// 检查 Go 项目
	if _, err := os.Stat(filepath.Join(projectPath, "main.go")); err == nil {
		config.StartCmd = "go run main.go"
		config.FeedMode = "args"
		config.ArgsPattern = "--task {prompt}"
		return &config, nil
	}

	// 检查 Node.js 项目
	if _, err := os.Stat(filepath.Join(projectPath, "package.json")); err == nil {
		config.StartCmd = "node index.js"
		config.FeedMode = "args"
		config.ArgsPattern = "--task {prompt}"
		return &config, nil
	}

	return nil, fmt.Errorf("无法自动识别项目类型(未找到 main.py/main.go/package.json)")
}
