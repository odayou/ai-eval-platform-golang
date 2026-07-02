package engine

// bootstrap.go — 模块 9 跨切面：全局初始化入口。

import (
	"fmt"
	"time"
)
//
// 在 server 启动时调用 InitEngine() 完成：
//   1. 健康检查启动（每 5 分钟 ping 各模型端点）
//   2. 双路 token 追踪器预热
//   3. 统计缓存初始化
//   4. 事件流（event stream）初始化
//
// 后续可扩展：
//   - OpenTelemetry tracing
//   - Prometheus metrics
//   - 结构化日志（slog + traceID 关联）

// InitEngine 初始化引擎全局状态（server 启动时调用一次）。
func InitEngine() {
	// 启动模型健康检查（每 5 分钟 ping 各模型端点，更新 available 字段）
	StartModelHealthCheck(5 * time.Minute)

	// 预热全局追踪器
	ResetDualTokenTracker()
	ResetStatsCache()

	logf("[bootstrap] 引擎初始化完成: 健康检查已启动, 追踪器已预热")
}

// ResetStatsCache 重置统计缓存（每个评测任务开始前调用）。
func ResetStatsCache() {
	globalStatsCache = initStatsCache()
}

// ShutdownEngine 关闭引擎（server 退出时调用）。
func ShutdownEngine() {
	StopModelHealthCheck()
	// 刷新任何未持久化的状态
	logf("[bootstrap] 引擎已关闭")
}

func logf(format string, args ...interface{}) {
	NewSlog("bootstrap").Info(fmt.Sprintf(format, args...))
}
