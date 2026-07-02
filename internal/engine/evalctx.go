package engine

// evalctx.go — 评测上下文(EvalContext)与 goroutine-local 绑定机制。
//
// 背景(见 docs/TECH_DEBT.md TD-1):
// 历史上 taskID/turn/caller/沙箱权限 用包级全局变量传递,依赖"评测串行执行"的假设。
// 但 scheduler 默认起多个 worker 并发执行 engine.Run,这些全局变量会互相覆盖,
// 导致审计日志 taskID/turn 串台、沙箱权限级别错乱。
//
// 方案:每个 worker goroutine 在 task 开始时把自己的 EvalContext 绑定到当前
// goroutine,深层调用链(RecordLLMAudit / RecordToolAudit / 沙箱预检)通过
// currentEvalContext() 取到"属于当前 goroutine 的"上下文,从而并发安全。
//
// 为什么不用 context.Context 透传?
//   调用链太深(Run→InvokeAgent→callProviderWithRetry→RecordLLMAudit),且
//   callProvider* 是通用 LLM 封装,给它们强加 ctx 参数会污染抽象、波及几百处调用方。
//   goroutine-local 绑定是这种场景下侵入性最小的正确解法。
//
// 为什么不用纯锁串行化访问?
//   那等于把并发评测退化回串行,失去并发优化的意义。per-goroutine 绑定让各 worker
//   真正并行且互不干扰。
//
// goroutine ID 提取说明:
//   Go 没有公开的 goroutine ID API。这里用 runtime.Stack() 解析,是 grpc-go、
//   logrus、zap 等成熟库验证过的做法。开销在 task 启动时一次性产生(绑定/解绑),
//   深层调用只查 sync.Map,不影响热路径性能。

import (
	"runtime"
	"strconv"
	"sync"
)

// EvalContext 持有一次评测任务运行期间的全部审计/沙箱上下文。
// 由 worker goroutine 在 task 开始时创建并绑定,task 结束时解绑。
//
// 字段语义对应原包级变量:
//   TaskID      ← currentAuditTaskID
//   Turn        ← currentAuditTurn(逐题 +1)
//   Caller      ← currentAuditCallerVar(agent/judge/roundtable_leader/...)
//   SandboxPerm ← currentSandboxPermLevelVar(workspace-write/full-auto/...)
type EvalContext struct {
	TaskID      string
	Turn        int
	Caller      string
	SandboxPerm string
}

// evalContexts 按 goroutine ID 绑定的上下文表。per-goroutine 隔离 = 并发安全。
var evalContexts sync.Map // map[int64]*EvalContext

// goID 返回当前 goroutine 的 ID。
// 通过解析 runtime.Stack 输出首行 "goroutine N [status]:" 提取 N。
func goID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// 输出形如: "goroutine 18 [runnable]:\n..."
	// 跳过 "goroutine " 前缀(10 字节),解析到空格为止
	b := buf[10:n]
	// 截断到第一个空格
	for i, c := range b {
		if c == ' ' {
			b = b[:i]
			break
		}
	}
	id, _ := strconv.ParseInt(string(b), 10, 64)
	return id
}

// BindEvalContext 在当前 goroutine 上绑定一个 EvalContext。
// 返回的 *EvalContext 可由调用方继续修改 Turn/Caller 等字段(指针语义,改了即生效)。
// 必须配对调用 UnbindEvalContext,否则条目泄漏(task 结束时解绑)。
//
// 调用方(engine.Run / api 调试入口)负责配对。详见 bindScope 辅助。
func BindEvalContext(taskID string) *EvalContext {
	ctx := &EvalContext{TaskID: taskID}
	evalContexts.Store(goID(), ctx)
	return ctx
}

// UnbindEvalContext 解绑当前 goroutine 的 EvalContext(task 结束时调用)。
func UnbindEvalContext() {
	evalContexts.Delete(goID())
}

// BindChildEvalContext 让子 goroutine 继承父 goroutine 的 EvalContext。
// 用于 agent_loop 的并行工具执行:子 goroutine 与父 goroutine ID 不同,
// 需显式把父的上下文重新绑定到子 goroutine 的 ID 上,否则子 goroutine 内
// currentEvalContext() 返回 nil,工具审计的 turn/caller 会丢失。
//
// 注意:这里让子 goroutine 与父共享同一个 *EvalContext 指针(只读场景安全)。
// 子 goroutine 不应修改 ctx 字段(避免与父竞争);只读取用于审计记录。
// 用法:
//
//	parentCtx := currentEvalContext()
//	go func() {
//	    BindChildEvalContext(parentCtx)
//	    defer UnbindEvalContext()
//	    ...
//	}()
func BindChildEvalContext(parent *EvalContext) {
	if parent == nil {
		return
	}
	evalContexts.Store(goID(), parent)
}

// currentEvalContext 返回当前 goroutine 绑定的 EvalContext。
// 未绑定(如系统内部调用、独立 goroutine)返回 nil,调用方据此跳过审计。
func currentEvalContext() *EvalContext {
	v, ok := evalContexts.Load(goID())
	if !ok {
		return nil
	}
	return v.(*EvalContext)
}

// ── 便捷访问(替代原包级变量的读路径,深层调用链用这些)──

// currentAuditTaskIDSafe 返回当前 goroutine 的审计 taskID,未绑定返回 ""。
func currentAuditTaskIDSafe() string {
	if ec := currentEvalContext(); ec != nil {
		return ec.TaskID
	}
	return ""
}

// currentAuditTurnSafe 返回当前 goroutine 的审计 turn,未绑定返回 0。
func currentAuditTurnSafe() int {
	if ec := currentEvalContext(); ec != nil {
		return ec.Turn
	}
	return 0
}

// currentAuditCallerSafe 返回当前 goroutine 的调用者角色,未绑定返回 ""。
func currentAuditCallerSafe() string {
	if ec := currentEvalContext(); ec != nil {
		return ec.Caller
	}
	return ""
}

// currentSandboxPermSafe 返回当前 goroutine 的沙箱权限级别,未绑定返回默认值。
func currentSandboxPermSafe() string {
	if ec := currentEvalContext(); ec != nil {
		if ec.SandboxPerm != "" {
			return ec.SandboxPerm
		}
	}
	return PermWorkspaceWrite
}
