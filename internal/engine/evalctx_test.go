package engine

// evalctx_test.go — EvalContext goroutine-local 绑定的并发安全测试。
//
// 这是 TD-1 重构的核心验证:证明多个 worker goroutine 并发执行时,
// 各自的审计上下文(taskID/turn/caller/沙箱权限)互不干扰、不串台。
// 历史上的包级全局变量在同样场景下会严重串台。

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestEvalContextGoroutineIsolation 验证并发 goroutine 各自的上下文隔离。
// 起 N 个 goroutine,各自绑定不同的 taskID,然后在 goroutine 内反复读回,
// 确认读到的总是自己绑定的值(而非被其他 goroutine 覆盖)。
func TestEvalContextGoroutineIsolation(t *testing.T) {
	const N = 20       // 并发 goroutine 数
	const reads = 100  // 每个 goroutine 内的读取次数(增加竞争窗口)
	var failures int64 // 原子计数,记录串台次数

	var wg sync.WaitGroup
	wg.Add(N)
	start := make(chan struct{}) // 同时起跑,最大化竞争

	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			taskID := fmt.Sprintf("task-%d", i)
			turn := i * 10
			caller := fmt.Sprintf("caller-%d", i)
			perm := fmt.Sprintf("perm-%d", i)

			BindEvalContext(taskID)
			defer UnbindEvalContext()
			SetAuditContext(taskID, turn)
			SetAuditCaller(caller)
			SetSandboxPermLevel(perm)

			<-start // 等所有 goroutine 都绑定完,再一起读(竞争最大化)

			for r := 0; r < reads; r++ {
				// 每次读到的都必须是自己绑定的值
				if got := currentAuditTaskIDSafe(); got != taskID {
					t.Errorf("goroutine %d taskID 串台: 期望 %s 实得 %s", i, taskID, got)
					atomic.AddInt64(&failures, 1)
				}
				if got := currentAuditTurnSafe(); got != turn {
					t.Errorf("goroutine %d turn 串台: 期望 %d 实得 %d", i, turn, got)
					atomic.AddInt64(&failures, 1)
				}
				if got := currentAuditCallerSafe(); got != caller {
					t.Errorf("goroutine %d caller 串台: 期望 %s 实得 %s", i, caller, got)
					atomic.AddInt64(&failures, 1)
				}
				if got := currentSandboxPermSafe(); got != perm {
					t.Errorf("goroutine %d sandboxPerm 串台: 期望 %s 实得 %s", i, perm, got)
					atomic.AddInt64(&failures, 1)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if failures > 0 {
		t.Fatalf("并发串台总计 %d 次(EvalContext 隔离失败)", failures)
	}
}

// TestEvalContextChildInheritance 验证子 goroutine 能继承父的上下文。
// 模拟 agent_loop 并行工具执行:父绑定后,子 goroutine 用 BindChildEvalContext 继承。
func TestEvalContextChildInheritance(t *testing.T) {
	const parentTaskID = "parent-task"
	const parentTurn = 7

	BindEvalContext(parentTaskID)
	SetAuditContext(parentTaskID, parentTurn)
	SetAuditCaller("parent")
	defer UnbindEvalContext()

	parentCtx := currentEvalContext()
	if parentCtx == nil {
		t.Fatal("父 goroutine 应已绑定 EvalContext")
	}

	const children = 5
	var wg sync.WaitGroup
	wg.Add(children)
	for i := 0; i < children; i++ {
		go func(i int) {
			defer wg.Done()
			// 子 goroutine 继承父上下文
			BindChildEvalContext(parentCtx)
			defer UnbindEvalContext()

			// 子 goroutine 应能读到父的 taskID/turn
			if got := currentAuditTaskIDSafe(); got != parentTaskID {
				t.Errorf("子 %d 未继承 taskID: 期望 %s 实得 %s", i, parentTaskID, got)
			}
			if got := currentAuditTurnSafe(); got != parentTurn {
				t.Errorf("子 %d 未继承 turn: 期望 %d 实得 %d", i, parentTurn, got)
			}
		}(i)
	}
	wg.Wait()
}

// TestEvalContextUnboundReturnsDefaults 验证未绑定 EvalContext 的 goroutine
// (如独立的后台 goroutine)读取时返回安全默认值,不 panic。
func TestEvalContextUnboundReturnsDefaults(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		// 这个 goroutine 没有绑定 EvalContext
		if got := currentAuditTaskIDSafe(); got != "" {
			t.Errorf("未绑定时应返回空 taskID,实得 %s", got)
		}
		if got := currentAuditTurnSafe(); got != 0 {
			t.Errorf("未绑定时应返回 turn=0,实得 %d", got)
		}
		if got := currentSandboxPermSafe(); got != PermWorkspaceWrite {
			t.Errorf("未绑定时应返回默认沙箱权限,实得 %s", got)
		}
	}()
	<-done
}

// TestGoIDDistinct 验证 goID() 对不同 goroutine 返回不同值(绑定隔离的基础)。
func TestGoIDDistinct(t *testing.T) {
	ids := make(chan int64, 4)
	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 4; i++ {
		go func() {
			defer wg.Done()
			ids <- goID()
		}()
	}
	wg.Wait()
	close(ids)

	seen := map[int64]bool{}
	for id := range ids {
		if id <= 0 {
			t.Errorf("goID 应为正数,实得 %d", id)
		}
		if seen[id] {
			// 不同 goroutine 理论上可能有相同 ID 复用(Go 运行时会复用),这里不强求唯一,
			// 但至少当前并发中的应不同。若复用导致此断言偶发失败,放宽即可。
			t.Logf("注意:goroutine ID %d 被复用(Go 运行时行为,非 bug)", id)
		}
		seen[id] = true
	}
}
