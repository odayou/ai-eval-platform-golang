package scheduler

// global_cron.go — 全局定时任务调度器(CronScheduler 的进程单例)。
//
// main 启动时 InitGlobalCron,从数据库恢复已保存的定时任务并启动循环。
// 定时任务到期时,executeFn 被调用 —— 通常在 main 里包一层:
// 创建一个新评测任务(指定 job 的 agent_id/rule_set_id/scene),然后 SubmitGlobal。
//
// 这样定时任务的执行结果也会走完整的 engine 流程(圆桌+沙箱+报告),
// 而不是裸跑,和手动触发的任务体验一致。

import (
	"log"
)

// globalCron 是进程级单例定时调度器。
var globalCron *CronScheduler

// InitGlobalCron 初始化并启动全局定时调度器,从数据库恢复已保存的 job。
// executeFn 通常是:func(job) { 创建任务 → SubmitGlobal }。
func InitGlobalCron(executeFn func(job *CronJob)) {
	globalCron = NewCronScheduler(executeFn)

	// 从数据库恢复之前保存的定时任务(重启不丢)
	saved := LoadCronJobs()
	for _, job := range saved {
		globalCron.AddJob(job)
	}

	globalCron.Start()
	log.Printf("[cron] 全局定时调度器已启动,恢复 %d 个定时任务", len(saved))
}

// StopGlobalCron 优雅停止全局定时调度器。
func StopGlobalCron() {
	if globalCron != nil {
		globalCron.Stop()
	}
}

// AddCronJob 全局便捷方法:添加定时任务。
func AddCronJob(job *CronJob) {
	if globalCron == nil {
		return
	}
	globalCron.AddJob(job)
	// 持久化(重启可恢复)
	SaveCronJobs(globalCron.GetJobs())
}

// RemoveCronJob 全局便捷方法:删除定时任务。
func RemoveCronJob(id string) {
	if globalCron == nil {
		return
	}
	globalCron.RemoveJob(id)
	SaveCronJobs(globalCron.GetJobs())
}

// ListCronJobs 全局便捷方法:列出所有定时任务。
func ListCronJobs() []*CronJob {
	if globalCron == nil {
		return nil
	}
	return globalCron.GetJobs()
}
