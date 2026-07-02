package engine

// sandbox_pool.go — Docker 容器池升级(MRD 5.3, 从进程级沙箱升级)。
//
// 当前状态(sandbox.go):
//   进程级沙箱,在临时目录中编译执行代码。功能完整但:
//   - 无网络隔离(被测代码可访问外网)
//   - 无资源限制(CPU/内存无上限)
//   - 无法并发隔离(共享文件系统)
//   - 安全风险(被测恶意代码可能影响宿主机)
//
// 升级目标(Docker 容器池):
//   1. 预建镜像:eval-sandbox-go / eval-sandbox-python / eval-sandbox-js
//      内含编译工具链 + 测试框架 + Lint 工具
//   2. 容器池:维护 N 个预热容器,评测任务来时复用,评测后清理
//   3. 资源限制:CPU 1核 / 内存 512MB / 磁盘 1GB / 超时 60s
//   4. 网络策略:默认无外网(防止数据泄露),可选开白名单
//   5. 安全隔离:只读根文件系统,禁止特权模式,AppArmor/SELinux(生产)
//
// 升级路径(逐步):
//   Phase 1 — 单容器模式:      每次评测创建新容器,用完销毁
//   Phase 2 — 容器池(本文件):   预建 pool,复用容器,减少启动延迟
//   Phase 3 — MCP Server 集成:  通过 mcp-docker 暴露沙箱能力
//
// 本文件实现 Phase 2 的骨架:
//   - ContainerPool 管理容器生命周期
//   - 与现有 sandbox.go 接口兼容(SandboxResult)
//   - 无 Docker 时自动回退到进程级沙箱(保持向后兼容)
//
// 使用方式:
//   pool := NewContainerPool(config)
//   result, err := pool.Execute(code, language)
//   // err == nil && result 即为 SandboxResult

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ContainerPoolConfig 容器池配置。
type ContainerPoolConfig struct {
	Enabled       bool   `json:"enabled"`        // 是否启用 Docker 模式
	MaxContainers int    `json:"max_containers"` // 最大并发容器数
	ImageGo       string `json:"image_go"`       // Go 沙箱镜像(默认 eval-sandbox-go:latest)
	ImagePython   string `json:"image_python"`   // Python 沙箱镜像
	ImageNode     string `json:"image_node"`     // Node.js 沙箱镜像
	CPULimit      string `json:"cpu_limit"`      // CPU 限制(如 "1")
	MemoryLimit   string `json:"memory_limit"`   // 内存限制(如 "512m")
	TimeoutSec    int    `json:"timeout_sec"`    // 执行超时(秒)
}

// DefaultPoolConfig 返回默认容器池配置。
func DefaultPoolConfig() ContainerPoolConfig {
	return ContainerPoolConfig{
		Enabled:       false, // 默认关闭,需用户主动开启并确保 Docker 可用
		MaxContainers: 3,
		ImageGo:       "eval-sandbox-go:latest",
		ImagePython:   "eval-sandbox-python:latest",
		ImageNode:     "eval-sandbox-node:latest",
		CPULimit:      "1",
		MemoryLimit:   "512m",
		TimeoutSec:    60,
	}
}

// ContainerPool 管理 Docker 沙箱容器池。
type ContainerPool struct {
	config ContainerPoolConfig
	mu     sync.Mutex
	active int // 当前活跃容器数
}

// NewContainerPool 创建一个容器池(不自动启动容器)。
func NewContainerPool(config ContainerPoolConfig) *ContainerPool {
	if !config.Enabled {
		NewSlog("sandbox_pool").Info("Docker 模式未启用,使用进程级沙箱")
		return &ContainerPool{config: config}
	}

	// 检查 Docker 是否可用
	if !dockerAvailable() {
		NewSlog("sandbox_pool").Warn("Docker 不可用,回退到进程级沙箱")
		config.Enabled = false
		return &ContainerPool{config: config}
	}

	NewSlog("sandbox_pool").Info("Docker 容器池就绪: max=, go=, py=, js=", "MaxContainers", config.MaxContainers, "ImageGo", config.ImageGo, "ImagePython", config.ImagePython, "ImageNode", config.ImageNode)
	return &ContainerPool{config: config}
}

// Execute 在 Docker 沙箱中执行代码。
// Docker 不可用时自动回退到进程级沙箱(runSandboxProcess)。
func (p *ContainerPool) Execute(code, language string) (SandboxResult, error) {
	if !p.config.Enabled {
		// 回退到进程级沙箱(跳过 Docker 池,避免递归)
		return runSandboxProcess(code, "", language), nil
	}

	// 检查并发限制
	p.mu.Lock()
	if p.active >= p.config.MaxContainers {
		p.mu.Unlock()
		return SandboxResult{Success: false, Score: 0, Language: language},
			fmt.Errorf("容器池已满(%d/%d),请稍后重试", p.active, p.config.MaxContainers)
	}
	p.active++
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()

	image := p.imageForLanguage(language)
	if image == "" {
		return SandboxResult{Score: 50, IsMock: true, Language: language}, nil
	}

	// 创建临时文件
	tmpDir, err := os.MkdirTemp("", "eval-docker-*")
	if err != nil {
		return SandboxResult{}, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 写入代码文件
	codeFile := writeCodeFile(tmpDir, code, language)

	// 构建 Docker 命令
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.config.TimeoutSec)*time.Second)
	defer cancel()

	// docker run --rm -v tmpDir:/code:ro --network none --cpus=1 --memory=512m image cmd
	args := []string{
		"run", "--rm",
		"-v", tmpDir + ":/code:ro",
		"--network", "none",
		"--cpus", p.config.CPULimit,
		"--memory", p.config.MemoryLimit,
		"--read-only",
		"--cap-drop", "ALL",
		image,
		"/eval.sh", codeFile, language,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return SandboxResult{Success: false, Score: 20, Language: language,
				CompileError: fmt.Sprintf("执行超时(%ds)", p.config.TimeoutSec)}, nil
		}
		return parseDockerOutput(outStr, language, false)
	}

	return parseDockerOutput(outStr, language, true)
}

// dockerAvailable 检查 Docker 是否可用。
func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// imageForLanguage 返回语言对应的 Docker 镜像。
func (p *ContainerPool) imageForLanguage(lang string) string {
	switch lang {
	case "go":
		return p.config.ImageGo
	case "python":
		return p.config.ImagePython
	case "javascript":
		return p.config.ImageNode
	default:
		return ""
	}
}

// writeCodeFile 将代码写入临时文件,返回文件名。
func writeCodeFile(tmpDir, code, language string) string {
	ext := map[string]string{
		"go":         ".go",
		"python":     ".py",
		"javascript": ".js",
	}[language]
	if ext == "" {
		ext = ".txt"
	}
	filename := tmpDir + "/code" + ext
	os.WriteFile(filename, []byte(code), 0644)
	return "code" + ext
}

// parseDockerOutput 解析 Docker 沙箱的 JSON 输出为 SandboxResult。
// eval.sh 输出标准 JSON 格式,见 docker/eval.sh。
func parseDockerOutput(output, language string, success bool) (SandboxResult, error) {
	// 从输出中提取 JSON 行(eval.sh 最后一行是 JSON)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	jsonLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "{") {
			jsonLine = strings.TrimSpace(lines[i])
			break
		}
	}

	if jsonLine == "" {
		return SandboxResult{
			Success:  false,
			Language: language,
			Score:    20,
			CompileError: output,
		}, nil
	}

	var result SandboxResult
	if err := json.Unmarshal([]byte(jsonLine), &result); err != nil {
		NewSlog("sandbox_pool").Error("Docker 输出解析失败: , raw=", "err", err, "truncateStroutput200", truncateStr(output, 200))
		return SandboxResult{
			Success:     false,
			Language:    language,
			Score:       20,
			CompileError: output,
		}, nil
	}

	NewSlog("sandbox_pool").Info("Docker 沙箱完成: lang= success= time=ms score=", "language", language, "Success", result.Success, "CompileTime", result.CompileTime, "Score", result.Score)
	return result, nil
}

// ── Dockerfile 模板(供参考,实际构建时用)──
//
// # eval-sandbox-go/Dockerfile:
// FROM golang:1.22-alpine
// RUN apk add --no-cache git gcc musl-dev
// COPY eval.sh /eval.sh
// RUN chmod +x /eval.sh
// ENTRYPOINT ["/eval.sh"]
//
// # eval-sandbox-python/Dockerfile:
// FROM python:3.12-alpine
// RUN pip install pytest pylint
// COPY eval.sh /eval.sh
// RUN chmod +x /eval.sh
// ENTRYPOINT ["/eval.sh"]
//
// # eval-sandbox-node/Dockerfile:
// FROM node:20-alpine
// RUN npm install -g eslint
// COPY eval.sh /eval.sh
// RUN chmod +x /eval.sh
// ENTRYPOINT ["/eval.sh"]
//
// # eval.sh(简化版):
// #!/bin/sh
// # 用法: /eval.sh <code_file> <language>
// FILE="$1"
// LANG="$2"
// case "$LANG" in
//   go)       cd /code && go build -o /tmp/out "$FILE" 2>&1 ;;
//   python)   cd /code && python -m py_compile "$FILE" 2>&1 ;;
//   javascript) cd /code && node --check "$FILE" 2>&1 ;;
// esac
// echo '{"success":true,"compile_time":0,"language":"'$LANG'"}'
