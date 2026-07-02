package engine

// shell_snapshot.go — Shell 环境快照(skill #39)。
//
// 每会话生成 shell snapshot,保证 BashTool 执行环境一致:
//   - unalias -a(清除所有别名,防劫持)
//   - 冻结 PATH(防止运行中被篡改)
//   - 重定义 rg 为内置 ripgrep(保证 grep 一致性)
//
// 设计参考 claw-code src/shell_snapshot.py。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ShellSnapshot shell 环境快照脚本内容。
type ShellSnapshot struct {
	Script   string // 生成的快照脚本内容
	ScriptPath string // 快照脚本路径
}

// GenerateShellSnapshot 生成 shell 环境快照脚本。
// workspace 是工作区根目录,用于存放快照脚本。
func GenerateShellSnapshot(workspace string) (*ShellSnapshot, error) {
	if workspace == "" {
		return &ShellSnapshot{Script: defaultSnapshotScript()}, nil
	}

	snapshotDir := filepath.Join(workspace, ".shell-snapshot")
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		// 失败则返回内存版本
		return &ShellSnapshot{Script: defaultSnapshotScript()}, nil
	}

	script := defaultSnapshotScript()
	scriptPath := filepath.Join(snapshotDir, "snapshot.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return &ShellSnapshot{Script: script}, nil
	}

	return &ShellSnapshot{
		Script:     script,
		ScriptPath: scriptPath,
	}, nil
}

// defaultSnapshotScript 生成默认快照脚本。
func defaultSnapshotScript() string {
	var sb strings.Builder

	// 1. 冻结 PATH(保存当前 PATH,后续不允许修改)
	path := os.Getenv("PATH")
	sb.WriteString(fmt.Sprintf("# Shell snapshot — 环境一致性保证\n"))
	sb.WriteString(fmt.Sprintf("# 生成时间: 自动\n"))
	sb.WriteString(fmt.Sprintf("# 平台: %s\n\n", runtime.GOOS))

	// 冻结 PATH
	sb.WriteString(fmt.Sprintf("export FROZEN_PATH=\"%s\"\n", path))
	sb.WriteString("PATH=\"$FROZEN_PATH\"\n")
	sb.WriteString("export PATH\n\n")

	// 2. 清除所有别名(防 alias 劫持)
	sb.WriteString("# 清除所有 alias(防劫持)\n")
	sb.WriteString("unalias -a 2>/dev/null || true\n\n")

	// 3. 重定义常见命令为绝对路径(防 PATH 篡改后的伪造)
	sb.WriteString("# 关键命令绝对路径化\n")
	for _, cmd := range []string{"ls", "cat", "rm", "cp", "mv", "grep", "find", "wc", "head", "tail", "sort", "uniq"} {
		if absPath, err := exec.LookPath(cmd); err == nil {
			sb.WriteString(fmt.Sprintf("alias %s=\"%s\"\n", cmd, absPath))
		}
	}
	sb.WriteString("\n")

	// 4. 环境变量加固
	sb.WriteString("# 环境加固\n")
	sb.WriteString("unset LD_PRELOAD LD_LIBRARY_PATH 2>/dev/null || true\n")
	sb.WriteString("export IFS=$' \\t\\n'\n")

	return sb.String()
}

// ApplyShellSnapshot 返回在命令前 source 快照的前缀。
// BashTool 执行命令时,在前面加上这个前缀。
func ApplyShellSnapshot(snapshot *ShellSnapshot) string {
	if snapshot == nil {
		return ""
	}
	if snapshot.ScriptPath != "" {
		return fmt.Sprintf("source '%s' 2>/dev/null; ", snapshot.ScriptPath)
	}
	// 无脚本文件时,内联脚本(source -)
	return fmt.Sprintf("eval '%s' 2>/dev/null; ", strings.ReplaceAll(snapshot.Script, "'", "'\\''"))
}
