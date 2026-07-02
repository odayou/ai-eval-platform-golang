package api

// terminal_api.go — 终端轨道 API(MRD 5.1 / ⑥协议层)。
//
// 让平台能评测"团队自研本地 Agent":指定项目路径,平台拉起进程、喂题、收 stdout。
//
// 端点:
//   POST /api/terminal/discover  — 自动识别项目类型,返回建议配置
//   POST /api/terminal/test      — 用配置对一道题执行一次,返回 stdout/stderr/耗时
//
// Agent 的 adapter='terminal' 时,TerminalConfig JSON 存在 agents.tools 字段(复用)。
// engine.Run 检测到 terminal adapter 时,走 RunTerminal 替代 ChatAgent。

import (
	"net/http"

	"ai-eval-platform/internal/engine"

	"github.com/gin-gonic/gin"
)

// discoverTerminal POST /api/terminal/discover
// Body: {"project_path": "/path/to/agent/project"}
// 自动识别项目类型(Python/Go/Node),返回建议的 TerminalConfig。
func discoverTerminal(c *gin.Context) {
	var req struct {
		ProjectPath string `json:"project_path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	config, err := engine.AutoDiscover(req.ProjectPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "ok": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"config": config,
	})
}

// testTerminal POST /api/terminal/test
// Body: {"config": {...TerminalConfig...}, "prompt": "实现一个快速排序"}
// 用给定配置执行一次,返回 stdout/stderr/耗时/退出码。
func testTerminal(c *gin.Context) {
	var req struct {
		Config engine.TerminalConfig `json:"config"`
		Prompt string                `json:"prompt" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 兜底默认值
	if req.Config.Timeout == 0 {
		req.Config.Timeout = 60
	}
	if req.Config.MaxOutput == 0 {
		req.Config.MaxOutput = 100000
	}
	if req.Config.FeedMode == "" {
		req.Config.FeedMode = "oneshot"
	}

	result := engine.RunTerminal(req.Config, req.Prompt)
	c.JSON(http.StatusOK, result)
}
