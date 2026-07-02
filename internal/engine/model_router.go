package engine

// model_router.go — 1.5 模型路由、fallback 链、健康检查。
//
// 当前（1.5 之前）：
//   lookupModelEndpoint() 返回单个模型，调用失败直接走 mock 回退。
//
// 目标：
//   1. model fallback 链：主模型失败 → 自动尝试下一个可用模型
//   2. 健康检查：后台定期 ping 各模型端点，动态更新 available 字段

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"
)

// ── ModelEndpoint 模型端点信息 ──

// ModelEndpoint 包含调用 LLM 所需的全部端点信息。
type ModelEndpoint struct {
	BaseURL   string
	APIKey    string
	RealName  string // 真实模型名（如 glm-4-flash）
	ModelID   string // 数据库 ID（如 m-glm-flash）
	APIFormat string // API 协议格式:openai|anthropic|gemini|''(空=自动检测)
}

// ── Fallback 链 ──

// GetModelFallbackChain 返回模型 fallback 链：主模型优先，其余按 available → 同厂商 → 字母序排列。
//
// 用法：
//   chain := GetModelFallbackChain("gpt-4o")
//   for _, ep := range chain {
//       result, err := callProviderWithRetry(ep.BaseURL, ep.APIKey, ep.RealName, prompt)
//       if err == nil { return result }
//   }
//   return mockAnswer(...)
func GetModelFallbackChain(primaryRef string) []ModelEndpoint {
	if store.DB == nil {
		return nil
	}

	rows, err := store.DB.Query(`SELECT id, name, base_url, COALESCE(api_key,''), COALESCE(api_format,''), COALESCE(available,0)
		FROM models ORDER BY available DESC, name ASC`)
	if err != nil {
		NewSlog("router").Error("查询 models 失败", "err", err)
		return nil
	}
	defer rows.Close()

	var primary ModelEndpoint
	var fallbacks []ModelEndpoint

	for rows.Next() {
		var id, name, baseURL, apiKey, apiFormat string
		var avail int
		rows.Scan(&id, &name, &baseURL, &apiKey, &apiFormat, &avail)

		if apiKey == "" {
			continue // 没配 Key 的跳过
		}

		ep := ModelEndpoint{
			BaseURL:   baseURL,
			APIKey:    apiKey,
			RealName:  name,
			ModelID:   id,
			APIFormat: apiFormat,
		}

		// 主模型优先级最高
		if id == primaryRef || name == primaryRef {
			// 优先取 available=1 的，其次 available=0
			if primary.BaseURL == "" || avail > 0 {
				primary = ep
			}
			continue
		}

		// 同厂商模型优先（通过 baseURL 判断）
		if primary.BaseURL != "" && baseURL == primary.BaseURL {
			// 同厂商的 fallback 插到前面
			if avail > 0 {
				fallbacks = append([]ModelEndpoint{ep}, fallbacks...)
			} else {
				fallbacks = append(fallbacks, ep)
			}
		} else {
			// 不同厂商的放后面
			fallbacks = append(fallbacks, ep)
		}
	}

	// 构建完整链
	chain := make([]ModelEndpoint, 0, 1+len(fallbacks))
	if primary.BaseURL != "" {
		chain = append(chain, primary)
	}
	chain = append(chain, fallbacks...)

	return chain
}

// callProviderWithFallback 带 fallback 链的 LLM 调用。
//
// 依次尝试 fallback 链中的每个模型，成功即返回。
// 所有模型都失败时返回最后错误 + mock 特征。
func callProviderWithFallback(primaryRef, prompt string) (ChatResult, error) {
	chain := GetModelFallbackChain(primaryRef)
	if len(chain) == 0 {
		return ChatResult{}, fmt.Errorf("no available models for %q", primaryRef)
	}

	var lastErr error
	for i, ep := range chain {
		prefix := ""
		if i > 0 {
			prefix = "[fallback] "
		}
		NewSlog("router").Info("trying model ()", "prefix", prefix, "RealName", ep.RealName, "BaseURL", ep.BaseURL)

		result, err := callProviderWithRetry(ep.BaseURL, ep.APIKey, ep.APIFormat, ep.RealName, prompt)
		if err == nil {
			if i > 0 {
				NewSlog("router").Info("fallback 成功: →", "primaryRef", primaryRef, "RealName", ep.RealName)
			}
			return result, nil
		}

		NewSlog("router").Info("model failed", "RealName", ep.RealName, "err", err)
		lastErr = err

		// 不可重试错误（401/403）不继续 fallback（Key 问题，换模型也没用）
		errStr := err.Error()
		if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") {
			NewSlog("router").Error("认证错误,停止 fallback")
			break
		}
	}

	return ChatResult{}, fmt.Errorf("all %d models exhausted, last error: %w", len(chain), lastErr)
}

// ── 健康检查 ──

// ModelHealthStatus 单模型健康检查结果。
type ModelHealthStatus struct {
	ModelID     string        `json:"model_id"`
	ModelName   string        `json:"model_name"`
	BaseURL     string        `json:"base_url"`
	Available   bool          `json:"available"`
	LatencyMs   int64         `json:"latency_ms"`
	LastCheck   time.Time     `json:"last_check"`
	Error       string        `json:"error,omitempty"`
}

// ModelHealthChecker 定期 ping 各模型端点，更新 available 状态。
type ModelHealthChecker struct {
	mu       sync.RWMutex
	statuses map[string]*ModelHealthStatus // modelID → status
	stopCh   chan struct{}
	interval time.Duration
}

// globalHealthChecker 全局健康检查器。
var globalHealthChecker = &ModelHealthChecker{
	statuses: make(map[string]*ModelHealthStatus),
	stopCh:   make(chan struct{}),
	interval: 5 * time.Minute, // 默认每 5 分钟检查一次
}

// GetHealthChecker 获取全局健康检查器。
func GetHealthChecker() *ModelHealthChecker {
	return globalHealthChecker
}

// Start 启动后台健康检查（非阻塞，goroutine）。
func (h *ModelHealthChecker) Start(interval time.Duration) {
	h.interval = interval
	go h.loop()
	NewSlog("health").Info("模型健康检查启动，间隔", "interval", interval)
}

// Stop 停止健康检查。
func (h *ModelHealthChecker) Stop() {
	close(h.stopCh)
}

// Statuses 返回当前所有模型的健康状态快照。
func (h *ModelHealthChecker) Statuses() []ModelHealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]ModelHealthStatus, 0, len(h.statuses))
	for _, s := range h.statuses {
		result = append(result, *s)
	}
	return result
}

// loop 后台检查循环。
func (h *ModelHealthChecker) loop() {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	// 启动时立即检查一次
	h.checkAll()

	for {
		select {
		case <-ticker.C:
			h.checkAll()
		case <-h.stopCh:
			NewSlog("health").Info("模型健康检查停止")
			return
		}
	}
}

// checkAll 检查所有 models 表中的模型。
func (h *ModelHealthChecker) checkAll() {
	if store.DB == nil {
		return
	}

	rows, err := store.DB.Query(`SELECT id, name, base_url, COALESCE(api_key,'') FROM models`)
	if err != nil {
		NewSlog("health").Error("查询 models 失败", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, baseURL, apiKey string
		rows.Scan(&id, &name, &baseURL, &apiKey)

		if apiKey == "" {
			continue // 没配置 Key 的跳过
		}

		status := h.pingModel(id, name, baseURL, apiKey)

		h.mu.Lock()
		h.statuses[id] = status
		h.mu.Unlock()

		// 更新 DB 的 available 字段
		avail := 0
		if status.Available {
			avail = 1
		}
		store.DB.Exec(`UPDATE models SET available = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, avail, id)

		if status.Available {
			NewSlog("health").Info("✓ () — ms", "name", name, "baseURL", baseURL, "LatencyMs", status.LatencyMs)
		} else {
			NewSlog("health").Info("✗ () —", "name", name, "baseURL", baseURL, "Error", status.Error)
		}
	}
}

// pingModel ping 单个模型端点。
//
// 策略：发一个简单的模型列表请求（GET /v1/models 或等价的轻量端点），
// 检查 HTTP 状态码（200/401 算连通，401 说明 Key 有效但权限不足，也算可用）。
func (h *ModelHealthChecker) pingModel(id, name, baseURL, apiKey string) *ModelHealthStatus {
	status := &ModelHealthStatus{
		ModelID:   id,
		ModelName: name,
		BaseURL:   baseURL,
		LastCheck: time.Now(),
	}

	// 对 baseURL 做基本清洗
	url := strings.TrimRight(baseURL, "/")
	// Anthropic 和 Gemini 用不同端点
	provider := DetectProvider(baseURL)
	switch provider {
	case ProviderAnthropic:
		url += "/v1/messages" // 最小的有效端点（需要 API key）
	case ProviderGemini:
		url += "/v1beta/models" // 模型列表
	default:
		url += "/models" // OpenAI 兼容的模型列表端点
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		status.Error = err.Error()
		return status
	}

	switch provider {
	case ProviderAnthropic:
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	start := time.Now()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	status.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer resp.Body.Close()

	// 200 OK / 401 Unauthorized 都算连通（401 说明 endpoint 可达）
	switch resp.StatusCode {
	case http.StatusOK, http.StatusUnauthorized:
		status.Available = true
	default:
		status.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	return status
}

// ── 便捷入口：启动健康检查 ──

// StartModelHealthCheck 启动全局模型健康检查（默认 5 分钟间隔）。
// 在 server 启动时调用。
func StartModelHealthCheck(interval time.Duration) {
	globalHealthChecker.Start(interval)
}

// StopModelHealthCheck 停止全局模型健康检查。
func StopModelHealthCheck() {
	globalHealthChecker.Stop()
}

// GetModelHealthStatuses 返回所有模型健康状态快照。
func GetModelHealthStatuses() []ModelHealthStatus {
	return globalHealthChecker.Statuses()
}
