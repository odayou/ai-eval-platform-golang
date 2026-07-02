package engine

// probe.go — 模型连通性检测(供 Settings 页"测试"按钮)。
//
// 真正向模型端点发一个最小请求,返回是否连通 + 延迟 + 错误信息。
// 检测用极简 prompt("hi"),max_tokens=5,成本几乎为零。
// 成功后会顺手把 models.available 标记为 1(可供引擎正常使用)。

import (
	"fmt"
	"time"

	"ai-eval-platform/internal/store"
)

// ProbeResult 是连通性检测的结果。
type ProbeResult struct {
	OK        bool   `json:"ok"`         // 是否连通
	LatencyMs int64  `json:"latency_ms"` // 延迟毫秒
	Model     string `json:"model"`      // 实际调用的模型名(厂商真实 ID)
	Message   string `json:"message"`    // 结果说明(成功/错误原因)
}

// ProbeModel 检测某个模型的连通性。
// modelRef 可以是 models.id 或 models.name(与引擎 lookupModelEndpoint 一致)。
func ProbeModel(modelRef string) ProbeResult {
	baseURL, apiKey, realName, apiFormat := lookupModelEndpoint(modelRef)

	if baseURL == "" {
		return ProbeResult{OK: false, Message: "未找到该模型的端点配置(请先在资源管理添加)"}
	}
	if apiKey == "" {
		return ProbeResult{OK: false, Model: realName, Message: "该模型未配置 API Key(请在下方输入框填入)"}
	}

	// 发一个极简请求测连通(max_tokens=5,成本≈0)
	start := time.Now()
	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, "hi")
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return ProbeResult{
			OK:        false,
			LatencyMs: latency,
			Model:     realName,
			Message:   fmt.Sprintf("连接失败: %v", err),
		}
	}

	// 成功:标记该模型 available=1,供引擎正常使用
	markModelAvailable(modelRef, realName)

	return ProbeResult{
		OK:        true,
		LatencyMs: latency,
		Model:     realName,
		Message:   fmt.Sprintf("连接成功,响应正常(返回 %d 字符)", len(result.Text)),
	}
}

// ProbeModelEndpoint 用显式端点检测连通性(不查 models 表)。
// 供考官模型(judge_models)检测用,避免依赖考生表。
// apiFormat 非空时优先用显式声明(openai/anthropic/gemini),空则自动检测。
func ProbeModelEndpoint(baseURL, apiKey, apiFormat, model string) ProbeResult {
	if baseURL == "" {
		return ProbeResult{OK: false, Message: "Base URL 为空"}
	}
	if apiKey == "" {
		return ProbeResult{OK: false, Model: model, Message: "未配置 API Key"}
	}
	start := time.Now()
	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, model, "hi")
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeResult{OK: false, LatencyMs: latency, Model: model, Message: fmt.Sprintf("连接失败: %v", err)}
	}
	return ProbeResult{OK: true, LatencyMs: latency, Model: model, Message: fmt.Sprintf("连接成功,响应正常(返回 %d 字符)", len(result.Text))}
}

// markModelAvailable 把模型标记为可用(available=1)。
// 按 modelRef 匹配 id 或 name。
func markModelAvailable(modelRef, realName string) {
	// 优先按 name 匹配(realName 就是 models.name)
	res, err := store.DB.Exec("UPDATE models SET available=1, updated_at=CURRENT_TIMESTAMP WHERE name=?", realName)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			return
		}
	}
	// 兜底:按 id 匹配
	store.DB.Exec("UPDATE models SET available=1, updated_at=CURRENT_TIMESTAMP WHERE id=?", modelRef)
}
