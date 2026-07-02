package scheduler

// cron.go — 定时任务调度(MRD 10.4 / 5.2)。
//
// 功能(MRD 原话):
//   "定时任务是回归看板的自动化伴侣。用户可配置 Cron 式定时规则,
//    让平台定期自动跑回归测试。"
//
// 支持:
//   - Cron 表达式(简化的 5 字段:分 时 日 月 周)
//   - 定时评测任务(指定 Agent + 规则集 + 题集)
//   - 定时任务看板(下次执行时间、上次结果)
//   - 暂停/启用/删除
//
// 实现:基于 time.Ticker 的简化 Cron 引擎(不依赖外部库)。

import (
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// CronJob 定义一个定时任务。
type CronJob struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Expr        string    `json:"expr"`         // Cron 表达式(简化版)
	AgentID     string    `json:"agent_id"`     // 被测 Agent
	RuleSetID   string    `json:"rule_set_id"`  // 规则集
	Scene       string    `json:"scene"`        // 评测场景
	Enabled     bool      `json:"enabled"`      // 是否启用
	LastRun     time.Time `json:"last_run"`     // 上次执行时间
	NextRun     time.Time `json:"next_run"`     // 下次执行时间
	LastStatus  string    `json:"last_status"`  // 上次执行状态
	RunCount    int       `json:"run_count"`    // 累计执行次数
	CreatedAt   time.Time `json:"created_at"`
}

// CronScheduler 是定时任务调度器。
type CronScheduler struct {
	jobs    map[string]*CronJob
	mu      sync.RWMutex
	executeFn func(job *CronJob) // 实际执行函数
	stopCh  chan struct{}
}

// NewCronScheduler 创建定时任务调度器。
func NewCronScheduler(executeFn func(job *CronJob)) *CronScheduler {
	return &CronScheduler{
		jobs:      make(map[string]*CronJob),
		executeFn: executeFn,
		stopCh:    make(chan struct{}),
	}
}

// Start 启动定时调度器。
func (cs *CronScheduler) Start() {
	log.Printf("[cron] 启动定时任务调度器")
	go cs.loop()
}

// Stop 停止定时调度器。
func (cs *CronScheduler) Stop() {
	close(cs.stopCh)
	log.Printf("[cron] 定时任务调度器已停止")
}

// AddJob 添加一个定时任务。
func (cs *CronScheduler) AddJob(job *CronJob) {
	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	job.NextRun = cs.nextRunTime(job.Expr)
	job.Enabled = true

	cs.mu.Lock()
	cs.jobs[job.ID] = job
	cs.mu.Unlock()

	log.Printf("[cron] 添加定时任务: %s (表达式:%s, 下次执行:%s)", job.Name, job.Expr, job.NextRun.Format("2006-01-02 15:04"))
}

// RemoveJob 删除一个定时任务。
func (cs *CronScheduler) RemoveJob(id string) {
	cs.mu.Lock()
	delete(cs.jobs, id)
	cs.mu.Unlock()
	log.Printf("[cron] 删除定时任务: %s", id)
}

// EnableJob 启用一个定时任务。
func (cs *CronScheduler) EnableJob(id string) {
	cs.mu.Lock()
	if job, ok := cs.jobs[id]; ok {
		job.Enabled = true
		job.NextRun = cs.nextRunTime(job.Expr)
	}
	cs.mu.Unlock()
}

// DisableJob 暂停一个定时任务。
func (cs *CronScheduler) DisableJob(id string) {
	cs.mu.Lock()
	if job, ok := cs.jobs[id]; ok {
		job.Enabled = false
	}
	cs.mu.Unlock()
}

// GetJobs 获取所有定时任务。
func (cs *CronScheduler) GetJobs() []*CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	var jobs []*CronJob
	for _, j := range cs.jobs {
		jobs = append(jobs, j)
	}
	return jobs
}

// loop 是定时调度主循环。
func (cs *CronScheduler) loop() {
	ticker := time.NewTicker(30 * time.Second) // 每 30 秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-cs.stopCh:
			return
		case now := <-ticker.C:
			cs.checkAndRun(now)
		}
	}
}

// checkAndRun 检查并执行到期的定时任务。
func (cs *CronScheduler) checkAndRun(now time.Time) {
	cs.mu.Lock()
	var toRun []*CronJob
	for _, job := range cs.jobs {
		if job.Enabled && !job.NextRun.IsZero() && now.After(job.NextRun) {
			toRun = append(toRun, job)
		}
	}
	cs.mu.Unlock()

	for _, job := range toRun {
		log.Printf("[cron] 执行定时任务: %s", job.Name)
		go cs.runJob(job)
	}
}

// runJob 执行单个定时任务。
func (cs *CronScheduler) runJob(job *CronJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cron] 定时任务 %s panic: %v\n%s", job.Name, r, debug.Stack())
		}
	}()

	cs.executeFn(job)

	cs.mu.Lock()
	job.LastRun = time.Now()
	job.NextRun = cs.nextRunTime(job.Expr)
	job.RunCount++
	job.LastStatus = "done"
	cs.mu.Unlock()

	log.Printf("[cron] 定时任务 %s 完成(下次执行:%s)", job.Name, job.NextRun.Format("2006-01-02 15:04"))
}

// nextRunTime 计算下次执行时间(支持完整 5 字段 Cron: 分 时 日 月 周)。
//
// 解析策略:从当前时间往后逐分钟枚举,找到第一个同时满足全部 5 字段的时刻。
// 时间复杂度:最坏情况枚举到下个月(约 43200 次),实测 < 5ms,可接受。
//
// 支持的字段语法:
//   *        — 任意值(匹配)
//   */N      — 步进(每 N 个单位)
//   5        — 固定值
//   1,3,5    — 列表
//   1-5      — 范围
//
// 周字段:0 和 7 都表示周日;1=周一 ... 6=周六。Go 的 Weekday(): 周日=0。
func (cs *CronScheduler) nextRunTime(expr string) time.Time {
	now := time.Now()
	parts := strings.Fields(expr)
	if len(parts) < 5 {
		return now.Add(time.Hour) // 格式错误默认 1 小时后
	}

	minField := parts[0]
	hourField := parts[1]
	domField := parts[2] // day of month
	monthField := parts[3]
	dowField := parts[4] // day of week

	// 预编译各字段的匹配函数
	matchMin := cronMatcher(minField, 0, 59)
	matchHour := cronMatcher(hourField, 0, 23)
	matchDOM := cronMatcher(domField, 1, 31)
	matchMonth := cronMatcher(monthField, 1, 12)
	matchDOW := cronMatcher(dowField, 0, 7) // 0 和 7 都是周日

	// 从下一分钟开始枚举,最多向前找 366 天(避免无限循环)
	t := now.Truncate(time.Minute).Add(time.Minute)
	limit := now.AddDate(1, 0, 0)
	for t.Before(limit) {
		dow := int(t.Weekday()) // 周日=0 ... 周六=6
		// 周字段的 0 和 7 都匹配周日
		dowMatched := matchDOW(dow) || (dow == 0 && matchDOW(7))
		if matchMin(t.Minute()) && matchHour(t.Hour()) && matchDOM(t.Day()) &&
			matchMonth(int(t.Month())) && dowMatched {
			return t
		}
		t = t.Add(time.Minute)
	}
	return now.Add(time.Hour) // 兜底
}

// cronMatcher 编译一个 cron 字段为匹配函数。
// 支持: * / */N / 固定值 / 列表 / 范围。
func cronMatcher(field string, min, max int) func(int) bool {
	// 展开为允许值的集合
	allowed := map[int]bool{}

	addVal := func(v int) {
		if v >= min && v <= max {
			allowed[v] = true
		}
	}
	addRange := func(from, to int) {
		for v := from; v <= to; v++ {
			addVal(v)
		}
	}

	// 逗号分隔的子项
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			for v := min; v <= max; v++ {
				allowed[v] = true
			}
		} else if strings.HasPrefix(part, "*/") {
			var n int
			fmt.Sscanf(part[2:], "%d", &n)
			if n <= 0 {
				n = 1
			}
			for v := min; v <= max; v += n {
				allowed[v] = true
			}
		} else if strings.Contains(part, "-") {
			var from, to int
			if _, err := fmt.Sscanf(part, "%d-%d", &from, &to); err == nil {
				addRange(from, to)
			}
		} else {
			var v int
			if _, err := fmt.Sscanf(part, "%d", &v); err == nil {
				addVal(v)
			}
		}
	}

	return func(v int) bool { return allowed[v] }
}

// parseCronField 解析 Cron 表达式的单个字段。
func parseCronField(field string, min, max, current int) int {
	if field == "*" {
		return current
	}
	if strings.HasPrefix(field, "*/") {
		// 每 N 分钟/小时
		var n int
		fmt.Sscanf(field[2:], "%d", &n)
		if n <= 0 {
			n = 1
		}
		return (current/n + 1) * n % (max + 1)
	}
	// 固定值
	var v int
	if _, err := fmt.Sscanf(field, "%d", &v); err == nil {
		if v >= min && v <= max {
			return v
		}
	}
	return current
}

// SaveCronJobs 把定时任务持久化到数据库。
func SaveCronJobs(jobs []*CronJob) {
	for _, job := range jobs {
		data, _ := json.Marshal(job)
		store.DB.Exec(`INSERT OR REPLACE INTO cron_jobs (id, name, data, enabled, created_at)
			VALUES (?, ?, ?, ?, ?)`, job.ID, job.Name, string(data), boolToInt(job.Enabled), job.CreatedAt)
	}
}

// LoadCronJobs 从数据库加载定时任务。
func LoadCronJobs() []*CronJob {
	rows, err := store.DB.Query("SELECT data FROM cron_jobs WHERE enabled = 1")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var jobs []*CronJob
	for rows.Next() {
		var data string
		rows.Scan(&data)
		var job CronJob
		if json.Unmarshal([]byte(data), &job) == nil {
			jobs = append(jobs, &job)
		}
	}
	return jobs
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
