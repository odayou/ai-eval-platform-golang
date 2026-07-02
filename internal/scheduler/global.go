package scheduler

// global.go — 全局调度器实例 + 便捷访问方法。
//
// 设计:main 启动时 InitGlobal(engine.Run),进程内单例。
// api 包的 createTask 通过 SubmitGlobal 入队,不再裸 go engine.Run。
// main 退出时 StopGlobal 优雅停止 worker pool。

import (
	"log"
)

// globalScheduler 是进程级单例调度器。
var globalScheduler *Scheduler

// InitGlobal 初始化并启动全局调度器。executeFn 通常是 engine.Run。
//
// worker 数由 maxWorkers 显式传入(main 从 config.MaxConcurrentEvals 读取)。
// ≤0 时回退到 DefaultConfig 的值。
//
// 并发安全(2026-07-02 TD-1 完成后):engine 的审计/沙箱上下文已改为
// per-goroutine 的 EvalContext(见 engine/evalctx.go),各 worker 绑定各自的
// 上下文,并发执行互不串台。瓶颈转为 SQLite 单写模型(WAL + busy_timeout 兜底)。
func InitGlobal(executeFn func(taskID string), maxWorkers int) {
	cfg := DefaultConfig()
	if maxWorkers > 0 {
		cfg.MaxWorkers = maxWorkers
	}
	globalScheduler = New(cfg, executeFn)
	globalScheduler.Start()
	log.Printf("[scheduler] 全局调度器已启动: %d workers, 队列容量 %d", cfg.MaxWorkers, cfg.QueueSize)
}

// StopGlobal 优雅停止全局调度器(等待 worker 完成当前任务)。
func StopGlobal() {
	if globalScheduler != nil {
		globalScheduler.Stop()
	}
}

// SubmitGlobal 向全局调度器提交任务(普通优先级)。
// 返回 error 表示队列已满(此时调用方应降级处理)。
func SubmitGlobal(taskID string) error {
	if globalScheduler == nil {
		// 调度器未初始化(理论上不会发生),降级为无队列直接执行
		log.Printf("[scheduler] 未初始化,直接执行 task %s", taskID)
		return nil
	}
	return globalScheduler.Submit(taskID, 0)
}

// SubmitGlobalHigh 向全局调度器提交高优先级任务(手动触发)。
func SubmitGlobalHigh(taskID string) error {
	if globalScheduler == nil {
		return nil
	}
	return globalScheduler.Submit(taskID, 1)
}

// GetGlobalStats 获取全局调度器统计(供看板展示队列/运行数)。
func GetGlobalStats() Stats {
	if globalScheduler == nil {
		return Stats{}
	}
	return globalScheduler.GetStats()
}

// RunDirect 绕过队列直接执行任务(队列满时的降级保底)。
// 内部调 executeFn(即 engine.Run),不经过 worker pool。
func RunDirect(taskID string) {
	if globalScheduler == nil {
		log.Printf("[scheduler] RunDirect: 调度器未初始化,跳过 task %s", taskID)
		return
	}
	go globalScheduler.executeFn(taskID)
}
