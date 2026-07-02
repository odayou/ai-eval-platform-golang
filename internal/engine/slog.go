package engine

// slog.go — 结构化日志模块（TODO 9.2.3 补齐）。
//
// 现状问题：全项目使用 log.Printf 散落各处，无 traceID 关联，
// 排查问题时无法将同一评测任务的日志串联。
//
// 方案：
//   - 基于 Go 1.21+ 的 log/slog（标准库，零依赖）
//   - 每个评测任务生成唯一 traceID（= taskID）
//   - 所有日志自动携带 traceID、模块名、时间戳
//   - 同时输出到 stdout（开发）和文件（生产）
//
// 用法：
//   slog := NewSlog("engine").With("task_id", taskID)
//   slog.Info("评测开始", "agent", agentName, "rules", len(rules))
//   slog.Error("评测失败", "err", err)

import (
	"log/slog"
	"os"
	"sync"
)

var (
	globalLogger *slog.Logger
	loggerOnce   sync.Once
)

// InitSlog 初始化全局结构化日志（调用一次）。
//
// 输出格式：JSON（生产环境可被 ELK/Loki 等日志系统解析）。
// 开发时可改为 TextHandler 看彩色输出。
func InitSlog() {
	loggerOnce.Do(func() {
		// 生产环境用 JSON 格式
		handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
		globalLogger = slog.New(handler)
		slog.SetDefault(globalLogger)
	})
}

// SlogModule 是带模块名的结构化日志包装器。
type SlogModule struct {
	logger *slog.Logger
}

// NewSlog 创建带模块名的日志器。
//
// 用法：
//   log := NewSlog("agent_loop")
//   log.Info("循环启动", "max_turns", 30)
func NewSlog(module string) *SlogModule {
	if globalLogger == nil {
		InitSlog()
	}
	return &SlogModule{
		logger: globalLogger.With("module", module),
	}
}

// With 添加上下文字段（返回新实例，不修改原实例）。
func (s *SlogModule) With(args ...any) *SlogModule {
	return &SlogModule{logger: s.logger.With(args...)}
}

// Info 记录信息级别日志。
func (s *SlogModule) Info(msg string, args ...any) {
	s.logger.Info(msg, args...)
}

// Warn 记录警告级别日志。
func (s *SlogModule) Warn(msg string, args ...any) {
	s.logger.Warn(msg, args...)
}

// Error 记录错误级别日志。
func (s *SlogModule) Error(msg string, args ...any) {
	s.logger.Error(msg, args...)
}

// Debug 记录调试级别日志。
func (s *SlogModule) Debug(msg string, args ...any) {
	s.logger.Debug(msg, args...)
}

// ── 便捷函数（替代 log.Printf）──

// LogInfo 全局信息日志（自动初始化）。
func LogInfo(msg string, args ...any) {
	if globalLogger == nil {
		InitSlog()
	}
	globalLogger.Info(msg, args...)
}

// LogError 全局错误日志。
func LogError(msg string, args ...any) {
	if globalLogger == nil {
		InitSlog()
	}
	globalLogger.Error(msg, args...)
}

// LogWarn 全局警告日志。
func LogWarn(msg string, args ...any) {
	if globalLogger == nil {
		InitSlog()
	}
	globalLogger.Warn(msg, args...)
}

// LogDebug 全局调试日志。
func LogDebug(msg string, args ...any) {
	if globalLogger == nil {
		InitSlog()
	}
	globalLogger.Debug(msg, args...)
}

// WithTraceID 创建带 traceID 的日志器（traceID = taskID）。
//
// 用法：
//   log := WithTraceID("task-abc-123")
//   log.Info("评测开始")  // 输出包含 "trace_id":"task-abc-123"
func WithTraceID(traceID string) *SlogModule {
	if globalLogger == nil {
		InitSlog()
	}
	return &SlogModule{
		logger: globalLogger.With("trace_id", traceID),
	}
}

// NewTaskSlog 创建带模块名 + 当前 goroutine 的 task_id 的日志器。
//
// 配合 TD-1 的 EvalContext(goroutine-local 绑定)使用:评测主流程的任何位置
// 调用 NewTaskSlog("engine"),都会自动把当前 worker 的 task_id 注入日志字段,
// 无需调用方手动传 taskID。并发 worker 的日志各自携带正确的 task_id,可被
// 日志系统按 task_id 串联。
//
// 当前 goroutine 未绑定 EvalContext(如系统内部调用)时,task_id 字段为空,
// 不影响日志输出。
func NewTaskSlog(module string) *SlogModule {
	s := NewSlog(module)
	if tid := currentAuditTaskIDSafe(); tid != "" {
		return s.With("task_id", tid)
	}
	return s
}
