package api

// proxy_api.go — MITM 代理管理 API(MRD v2 5.1 协议逆向代理轨道)。
//
// 端点:
//   POST /api/proxy/start            — 启动代理(port + target URL)
//   POST /api/proxy/stop             — 停止代理
//   GET  /api/proxy/status           — 代理状态
//   GET  /api/proxy/sessions         — 捕获的会话列表
//   GET  /api/proxy/sessions/:id     — 单会话详情(含请求列表)
//   POST /api/proxy/sessions/:id/convert — 会话转为评测任务

import (
	"net/http"

	"ai-eval-platform/internal/proxy"
	"ai-eval-platform/internal/scheduler"

	"github.com/gin-gonic/gin"
)

// startProxy POST /api/proxy/start
// Body: {"listen_addr": ":9090", "target_url": "https://api.anthropic.com"}
func startProxy(c *gin.Context) {
	var req struct {
		ListenAddr string `json:"listen_addr" binding:"required"` // 如 :9090
		TargetURL  string `json:"target_url" binding:"required"`  // 如 https://api.openai.com
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if proxy.GlobalProxy != nil && proxy.GlobalProxy.Status()["running"].(bool) {
		proxy.GlobalProxy.Stop()
	}

	proxy.GlobalProxy = proxy.New(req.ListenAddr, req.TargetURL)
	if err := proxy.GlobalProxy.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "启动代理失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"listen_addr": req.ListenAddr,
		"target_url":  req.TargetURL,
		"message":     "MITM 代理已启动,请将被测 Agent 的请求转发至此端口",
	})
}

// stopProxy POST /api/proxy/stop
func stopProxy(c *gin.Context) {
	if proxy.GlobalProxy != nil {
		proxy.GlobalProxy.Stop()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "代理已停止"})
}

// proxyStatus GET /api/proxy/status
func proxyStatus(c *gin.Context) {
	if proxy.GlobalProxy == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, proxy.GlobalProxy.Status())
}

// proxySessions GET /api/proxy/sessions
func proxySessions(c *gin.Context) {
	if proxy.GlobalProxy == nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}
	sessions := proxy.GlobalProxy.GetSessions()
	// 返回摘要(不含完整请求体,避免响应过大)
	type sessionSummary struct {
		ID           string `json:"id"`
		TargetURL    string `json:"target_url"`
		StartTime    string `json:"start_time"`
		RequestCount int    `json:"request_count"`
	}
	items := make([]sessionSummary, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, sessionSummary{
			ID:           s.ID,
			TargetURL:    s.TargetURL,
			StartTime:    s.StartTime.Format("2006-01-02 15:04:05"),
			RequestCount: len(s.Requests),
		})
	}
	c.JSON(http.StatusOK, items)
}

// proxySessionDetail GET /api/proxy/sessions/:id
func proxySessionDetail(c *gin.Context) {
	id := c.Param("id")
	if proxy.GlobalProxy == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "代理未运行"})
		return
	}
	for _, s := range proxy.GlobalProxy.GetSessions() {
		if s.ID == id {
			c.JSON(http.StatusOK, s)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
}

// replaySession POST /api/proxy/sessions/:id/replay
// Body: {"target_url": "https://api.openai.com/v1"}
// 把捕获的会话回放到另一个模型 API,返回对比结果。
func replaySession(c *gin.Context) {
	sessionID := c.Param("id")
	var req struct {
		TargetURL string `json:"target_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	exchanges, err := proxy.ReplaySession(sessionID, req.TargetURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"replayed_to": req.TargetURL,
		"count": len(exchanges),
		"exchanges": exchanges,
	})
}

// convertSession POST /api/proxy/sessions/:id/convert
// Body: { "agent_id": "a-codegen" }
func convertSession(c *gin.Context) {
	sessionID := c.Param("id")
	var req struct {
		AgentID string `json:"agent_id"`
	}
	c.ShouldBindJSON(&req)
	if req.AgentID == "" {
		req.AgentID = "a-codegen"
	}

	taskID, err := proxy.ConvertSessionToTask(sessionID, req.AgentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 触发评测
		scheduler.SubmitGlobalHigh(taskID)

		c.JSON(http.StatusOK, gin.H{
			"task_id": taskID,
			"message": "会话已转为评测任务,引擎开始评测",
		})
	}

	// ── HTTPS MITM 代理(proxy_tls.go)──

	// startTLSProxy POST /api/proxy/tls/start
	// Body: {"listen_addr": ":9443"}
	// 启动 HTTPS MITM 代理(需要先安装 CA 证书)。
	func startTLSProxy(c *gin.Context) {
		var req struct {
			ListenAddr string `json:"listen_addr"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.ListenAddr == "" {
			req.ListenAddr = ":9443"
		}

		if err := proxy.StartTLSProxy(req.ListenAddr); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"ok":          true,
			"listen_addr": req.ListenAddr,
			"protocol":    "HTTPS MITM",
			"ca_ready":    proxy.CAExists(),
			"message":     "HTTPS MITM 代理已启动,请确保已安装自签 CA 证书到系统信任证书库",
		})
	}

	// stopTLSProxy POST /api/proxy/tls/stop
	func stopTLSProxy(c *gin.Context) {
		proxy.StopTLSProxy()
		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "HTTPS 代理已停止"})
	}

	// tlsProxyStatus GET /api/proxy/tls/status
	func tlsProxyStatus(c *gin.Context) {
		c.JSON(http.StatusOK, proxy.GetTLSProxyStatus())
	}

	// tlsProxySessions GET /api/proxy/tls/sessions
	func tlsProxySessions(c *gin.Context) {
		sessions := proxy.GetTLSProxySessions()
		type sessionSummary struct {
			ID           string `json:"id"`
			TargetURL    string `json:"target_url"`
			StartTime    string `json:"start_time"`
			RequestCount int    `json:"request_count"`
			Protocol     string `json:"protocol"`
		}
		items := make([]sessionSummary, 0, len(sessions))
		for _, s := range sessions {
			items = append(items, sessionSummary{
				ID:           s.ID,
				TargetURL:    s.TargetURL,
				StartTime:    s.StartTime.Format("2006-01-02 15:04:05"),
				RequestCount: len(s.Requests),
				Protocol:     "HTTPS",
			})
		}
		c.JSON(http.StatusOK, items)
	}

	// tlsGenerateCA POST /api/proxy/tls/generate-ca
	// 生成/重新生成 CA 证书。
	func tlsGenerateCA(c *gin.Context) {
		if err := proxy.EnsureCA(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":               true,
			"ca_cert":          proxy.CertPaths.CACert,
			"ca_key":           proxy.CertPaths.CAKey,
			"install_guide":    proxy.GetCAInstallInstructions(),
			"message":          "CA 证书已生成,请按指引安装到系统信任证书库",
		})
	}

	// tlsCAStatus GET /api/proxy/tls/ca-status
	func tlsCAStatus(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"ca_exists":      proxy.CAExists(),
			"ca_cert_path":   proxy.CertPaths.CACert,
			"install_guide":  proxy.GetCAInstallInstructions(),
		})
	}

	// ── 协议逆向:环境变量重定向(对标 claude-code-router) ──

	// generateRedirectScript POST /api/proxy/redirect-script
	// 生成把闭源客户端重定向到本地代理的环境变量脚本。
	// Body: {"local_proxy_url": "http://127.0.0.1:9090", "protocol": "anthropic",
	//        "api_key": "sk-...", "format": "sh"}
	func generateRedirectScript(c *gin.Context) {
		var req struct {
			LocalProxyURL  string `json:"local_proxy_url"`
			Protocol       string `json:"protocol"`
			APIKey         string `json:"api_key"`
			Format         string `json:"format"` // sh | cmd | powershell
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.LocalProxyURL == "" {
			req.LocalProxyURL = "http://127.0.0.1:9090"
		}
		if req.Protocol == "" {
			req.Protocol = "openai"
		}
		if req.Format == "" {
			req.Format = proxy.ShellHint()
		}

		cfg := proxy.RedirectConfig{
			LocalProxyURL:  req.LocalProxyURL,
			Protocol:       req.Protocol,
			APIKey:         req.APIKey,
		}

		c.JSON(http.StatusOK, gin.H{
			"script":     cfg.ShellExportScript(req.Format),
			"env_vars":   cfg.RedirectEnvVars(),
			"format":     req.Format,
			"protocol":   req.Protocol,
			"hint":       "在启动被测客户端的终端里执行此脚本,客户端流量将经过本地代理(需先启动 HTTP 代理)",
		})
	}
