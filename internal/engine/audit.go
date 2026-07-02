package engine

// audit.go — 白盒调用审计日志的统一记录层。
//
// 提供统一的 recordAudit 函数,各 LLM/工具/事件埋点都调它落库到 audit_logs 表。
// 设计原则:
//   - 旁路记录,绝不影响主流程(失败仅 log)
//   - 落库前对敏感字段脱敏(避免 API Key 泄漏)
//   - taskID/caller/turn 通过 per-goroutine 的 EvalContext 传递(见 evalctx.go),
//     并发 worker 各自的 goroutine 互不干扰,审计不串台。
//
// 4 个埋点位置:
//   A. callProviderWithRetryAndSystem(覆盖 ~70% LLM 调用)
//   B. callLLMInternal OpenAI 分支 + callLLMWithToolsStream(agent loop 的 tools/SSE 路径)
//   C. executeSingleTool(工具调用)
//   D. ws.Emit(事件流)
//
// 并发安全(2026-07-02 TD-1 重构):
//   历史上用包级全局变量传递,依赖"评测串行"假设,并发 worker 会串台。
//   现已改为 per-goroutine 的 EvalContext(见 evalctx.go),各 worker goroutine
//   绑定各自的上下文,深层调用链读取的是属于当前 goroutine 的上下文。
//   SetAuditContext/SetAuditCaller/ClearAuditContext 的函数签名保持不变(向后
//   兼容所有现有调用点),内部改为操作 per-goroutine 的 EvalContext。

import (
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// SetAuditContext 设置当前审计上下文(评测任务开始时调用)。
// 内部操作当前 goroutine 绑定的 *EvalContext(per-goroutine 隔离,并发安全)。
// 若当前 goroutine 未绑定 EvalContext(理论上不该发生——engine.Run 应先 BindEvalContext),
// 则补绑一个。
func SetAuditContext(taskID string, turn int) {
	ec := currentEvalContext()
	if ec == nil {
		// 防御:理论上 engine.Run 入口已 Bind。兜底补绑,避免审计丢失。
		ec = BindEvalContext(taskID)
	}
	ec.TaskID = taskID
	ec.Turn = turn
}

// SetAuditCaller 设置当前调用者角色(LLM 调用方入口设置,如 "judge"/"router"/"compaction")。
// 操作当前 goroutine 的 EvalContext。
func SetAuditCaller(caller string) {
	if ec := currentEvalContext(); ec != nil {
		ec.Caller = caller
	}
	// 未绑定则忽略(系统内部调用,无需审计 caller)
}

// currentAuditCaller 返回当前调用者角色(供 provider 收口点用)。
func currentAuditCaller() string {
	if c := currentAuditCallerSafe(); c != "" {
		return c
	}
	return "unknown"
}

// ClearAuditContext 清除审计上下文(评测任务结束时调用)。
// 注意:这里只清字段不清绑定。实际解绑由 engine.Run 的 defer UnbindEvalContext() 完成。
// 保留此函数是为了向后兼容(归因 goroutine 等历史调用点)。
func ClearAuditContext() {
	if ec := currentEvalContext(); ec != nil {
		ec.TaskID = ""
		ec.Turn = 0
		ec.Caller = ""
	}
}

// CurrentAuditTaskID 返回当前审计任务 ID(供 ws.Emit 等外部模块用)。
func CurrentAuditTaskID() string {
	return currentAuditTaskIDSafe()
}

// ── LLM 调用审计 ──

// AuditLLMFields LLM 调用审计字段。
type AuditLLMFields struct {
	Caller       string // 调用者角色:agent/judge/expert_security/question_gen/router/...
	Provider     string // openai/anthropic/gemini
	Model        string
	BaseURL      string
	APIFormat    string
	SystemPrompt string
	Prompt       string // 用户输入
	Result       ChatResult
	Error        error
	Latency      time.Duration
	HTTPStatus   int
	Attempt      int // 重试次数(0=首次)
}

// RecordLLMAudit 记录一次 LLM 调用到审计日志。
// taskID/turn 从当前 goroutine 的 EvalContext 取(per-goroutine 隔离,并发安全)。
// 失败仅 log,不影响主流程。
func RecordLLMAudit(f AuditLLMFields) {
	taskID := currentAuditTaskIDSafe()
	if taskID == "" {
		return // 无上下文,跳过(如系统内部调用)
	}
	turn := currentAuditTurnSafe()

	isError := f.Error != nil
	errText := ""
	if isError {
		errText = truncStr(f.Error.Error(), 1000)
	}

	output := ""
	reasoning := ""
	tokenIn, tokenOut := 0, 0
	if f.Result.Text != "" || !isError {
		output = f.Result.Text
		reasoning = f.Result.Reasoning
		tokenIn = f.Result.TokenIn
		tokenOut = f.Result.TokenOut
	}

	if err := store.InsertAuditLog(store.AuditLogRow{
		TaskID:       taskID,
		Turn:         turn,
		Kind:         "llm",
		Caller:       f.Caller,
		Provider:     f.Provider,
		Model:        f.Model,
		BaseURL:      f.BaseURL,
		APIFormat:    f.APIFormat,
		SystemPrompt: redactForAudit(f.SystemPrompt),
		InputText:    redactForAudit(f.Prompt),
		OutputText:   redactForAudit(output),
		Reasoning:    redactForAudit(truncStr(reasoning, 8000)),
		TokenIn:      tokenIn,
		TokenOut:     tokenOut,
		LatencyMs:    int(f.Latency / time.Millisecond),
		HTTPStatus:   f.HTTPStatus,
		Attempt:      f.Attempt,
		IsMock:       f.Result.IsMock,
		IsError:      isError,
		ErrorText:    redactForAudit(errText),
	}); err != nil {
		NewSlog("audit").Error("LLM 记录失败(caller=)", "Caller", f.Caller, "err", err)
	}
}

// ── 工具调用审计 ──

// RecordToolAudit 记录一次工具调用。
func RecordToolAudit(taskID string, turn int, caller, toolName, input, output string, isError bool, errMsg string, latency time.Duration) {
	if taskID == "" {
		return
	}
	if err := store.InsertAuditLog(store.AuditLogRow{
		TaskID:     taskID,
		Turn:       turn,
		Kind:       "tool",
		Caller:     caller,
		ToolName:   toolName,
		InputText:  redactForAudit(truncStr(input, 4000)),
		OutputText: redactForAudit(truncStr(output, 8000)),
		LatencyMs:  int(latency / time.Millisecond),
		IsError:    isError,
		ErrorText:  truncStr(errMsg, 1000),
	}); err != nil {
		NewSlog("audit").Error("tool 记录失败(tool=)", "toolName", toolName, "err", err)
	}
}

// ── 事件流审计 ──

// RecordEventAudit 记录一次运行时事件(WebSocket 事件流)。
func RecordEventAudit(taskID string, eventType string, dataJSON string) {
	if taskID == "" {
		return
	}
	if err := store.InsertAuditLog(store.AuditLogRow{
		TaskID:    taskID,
		Kind:      "event",
		Caller:    "event_" + eventType,
		InputText: truncStr(dataJSON, 4000),
	}); err != nil {
		NewSlog("audit").Error("event 记录失败(type=)", "eventType", eventType, "err", err)
	}
}

// ── 工具函数 ──

// redactForAudit 脱敏(复用 secure_inject.go 的脱敏逻辑,丢弃脱敏计数)。
func redactForAudit(s string) string {
	out, _ := RedactText(s)
	return out
}

// truncStr 截断字符串到指定长度(超出加省略号)。
func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]"
}

// NewAuditID 生成审计日志 ID。
func NewAuditID() string {
	return "al-" + uuid.New().String()[:12]
}
