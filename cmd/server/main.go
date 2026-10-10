// Package main 是平台启动入口。
// 职责仅为装配:加载配置 → 初始化 SQLite → 启动全局调度器与定时任务 → 启动 HTTP 服务。
// 业务逻辑全部在 internal/ 各包中,入口保持薄层,禁止业务代码。
package main

import (
	"log"
	"time"

	"ai-eval-platform/internal/api"
	"ai-eval-platform/internal/config"
	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/scheduler"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func main() {
	cfg := config.Load()

	// SQLite 初始化(建表 + 增量迁移在 store.Init 内完成)
	store.Init(cfg.DBPath)
	defer store.Close()

	// 全局并发调度器:评测任务 worker pool,executeFn = engine.Run
	// (设计依据:scheduler/global.go 顶部注释 "main 启动时 InitGlobal(engine.Run)")
	scheduler.InitGlobal(engine.Run, cfg.MaxConcurrentEvals)
	defer scheduler.StopGlobal()

	// 全局定时任务调度器:到期后创建新评测任务并入队,复用完整评测链路
	// (设计依据:scheduler/global_cron.go 顶部注释 + api/task.go rerunTask 的既有模式)
	scheduler.InitGlobalCron(func(job *scheduler.CronJob) {
		id := uuid.New().String()
		now := time.Now().Format(time.RFC3339)
		if err := store.InsertTask(store.TaskCreateParams{
			ID: id, Name: job.Name + " (定时)", Scene: job.Scene,
			AgentID: job.AgentID, RuleSet: job.RuleSetID,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			log.Printf("[cron] 创建定时任务 %s 失败: %v", job.Name, err)
			return
		}
		if err := scheduler.SubmitGlobal(id); err != nil {
			scheduler.RunDirect(id)
		}
	})
	defer scheduler.StopGlobalCron()

	gin.SetMode(cfg.GinMode)
	r := api.NewRouter(api.RouterOptions{
		CORSOrigins: cfg.CORSOrigins,
		RateLimit:   cfg.RateLimit,
		RateBurst:   cfg.RateBurst,
	})

	log.Printf("[main] 服务启动: http://localhost:%s (DB: %s)", cfg.Port, cfg.DBPath)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
