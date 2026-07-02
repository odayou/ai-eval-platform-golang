package api

// cron_api.go — 定时任务管理 API(对应 ⑤ cron 调度器)。
//
// 端点:
//   GET    /api/cron-jobs          列出所有定时任务
//   POST   /api/cron-jobs          创建定时任务
//   DELETE /api/cron-jobs/:id      删除定时任务
//   PUT    /api/cron-jobs/:id/toggle  启用/暂停
//   GET    /api/scheduler/stats    调度器运行统计(队列/运行数)

import (
	"net/http"

	"ai-eval-platform/internal/scheduler"

	"github.com/gin-gonic/gin"
)

// listCronJobs GET /api/cron-jobs
func listCronJobs(c *gin.Context) {
	jobs := scheduler.ListCronJobs()
	if jobs == nil {
		jobs = []*scheduler.CronJob{}
	}
	c.JSON(http.StatusOK, jobs)
}

// createCronJobReq 创建定时任务的请求体。
type createCronJobReq struct {
	Name      string `json:"name" binding:"required"`
	Expr      string `json:"expr" binding:"required"`   // Cron 表达式,如 "0 2 * * *"(每天2点)
	AgentID   string `json:"agent_id"`
	RuleSetID string `json:"rule_set_id"`
	Scene     string `json:"scene"`
}

// createCronJob POST /api/cron-jobs
func createCronJob(c *gin.Context) {
	var req createCronJobReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scene == "" {
		req.Scene = "agent"
	}

	job := &scheduler.CronJob{
		Name:      req.Name,
		Expr:      req.Expr,
		AgentID:   req.AgentID,
		RuleSetID: req.RuleSetID,
		Scene:     req.Scene,
		Enabled:   true,
	}
	scheduler.AddCronJob(job)
	c.JSON(http.StatusCreated, job)
}

// deleteCronJob DELETE /api/cron-jobs/:id
func deleteCronJob(c *gin.Context) {
	id := c.Param("id")
	scheduler.RemoveCronJob(id)
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// toggleCronJob PUT /api/cron-jobs/:id/toggle
// Body: {"enabled": true/false}
func toggleCronJob(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 简化:删了重建(因为 CronScheduler 内部 job 是指针,直接改字段也行但这里走安全路径)
	jobs := scheduler.ListCronJobs()
	for _, j := range jobs {
		if j.ID == id {
			j.Enabled = req.Enabled
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "enabled": req.Enabled})
}

// schedulerStats GET /api/scheduler/stats
// 返回 worker pool 的队列/运行/完成统计。
func schedulerStats(c *gin.Context) {
	stats := scheduler.GetGlobalStats()
	c.JSON(http.StatusOK, stats)
}
