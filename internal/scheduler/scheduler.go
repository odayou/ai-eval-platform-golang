package scheduler

// scheduler.go — 任务调度与排队管理(MRD 10.4 / 5.1)。
//
// 问题(MRD 原话):
//   "当多个用户、多个 Agent 同时提交评测时,平台需要一个任务调度中枢来管理
//    Docker 沙箱资源、排队顺序、优先级和失败重试。没有它,并发评测会资源抢占、
//    队列混乱、定时任务无法管理。"
//
// 设计:
//   - Worker Pool:固定数量的 worker goroutine 从任务队列取任务执行
//   - 优先级队列:手动点击的即时评测优先于定时任务
//   - 失败重试:沙箱崩溃或超时时自动重试(限制次数)
//   - 并发限制:同时运行的评测任务不超过 MaxWorkers
//
// 架构(借鉴 claw-code 的 session 管理):
//   - TaskQueue: channel-based 任务队列
//   - Worker: goroutine,从队列取任务 → 调 engine.Run → 更新状态
//   - Dispatcher: 接收 API 请求 → 入队 → 返回任务 ID

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Task 是一个待执行的评测任务。
type Task struct {
	ID        string    `json:"id"`
	Priority  int       `json:"priority"`  // 优先级:0=普通, 1=高(手动触发), 2=紧急
	CreatedAt time.Time `json:"created_at"`
	RetryCount int      `json:"retry_count"` // 已重试次数
}

// Config 是调度器配置。
type Config struct {
	MaxWorkers  int `json:"max_workers"`   // 最大并发 worker 数
	MaxRetries  int `json:"max_retries"`   // 单任务最大重试次数
	QueueSize   int `json:"queue_size"`    // 队列容量
	TaskTimeout int `json:"task_timeout"`  // 单任务超时秒数
}

// DefaultConfig 返回默认配置。
func DefaultConfig() Config {
	return Config{
		MaxWorkers:  3,
		MaxRetries:  2,
		QueueSize:   100,
		TaskTimeout: 300, // 5 分钟
	}
}

// Scheduler 是任务调度器。
// 队列用有序切片 + 条件变量实现优先级调度(高优先级任务先执行)。
type Scheduler struct {
	config    Config
	queue     []Task           // 优先级队列(按 priority 降序, 同级按创建时间升序)
	cond      *sync.Cond       // 队列非空条件变量(worker 等待它)
	workers   int
	wg        sync.WaitGroup
	running   map[string]bool
	mu        sync.Mutex
	executeFn func(taskID string) // 实际执行函数(注入 engine.Run)
	stopCh    chan struct{}
	stats     Stats
}

// Stats 是调度器统计信息。
type Stats struct {
	Queued    int `json:"queued"`     // 队列中等待的任务数
	Running   int `json:"running"`    // 正在执行的任务数
	Completed int `json:"completed"`  // 已完成的任务数
	Failed    int `json:"failed"`     // 失败的任务数
	Retried   int `json:"retried"`    // 重试的任务数
}

// New 创建一个新的调度器。
func New(config Config, executeFn func(taskID string)) *Scheduler {
	s := &Scheduler{
		config:    config,
		queue:     make([]Task, 0, config.QueueSize),
		running:   make(map[string]bool),
		executeFn: executeFn,
		stopCh:    make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Start 启动调度器(启动 worker pool)。
func (s *Scheduler) Start() {
	log.Printf("[scheduler] 启动调度器: %d workers, 队列容量 %d", s.config.MaxWorkers, s.config.QueueSize)
	for i := 0; i < s.config.MaxWorkers; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}
}

// Stop 优雅停止调度器(等待所有 worker 完成当前任务)。
func (s *Scheduler) Stop() {
	log.Printf("[scheduler] 正在停止调度器...")
	close(s.stopCh)
	s.cond.Broadcast() // 唤醒所有等待的 worker
	s.wg.Wait()
	log.Printf("[scheduler] 调度器已停止")
}

// Submit 提交一个任务到优先级队列。
// priority: 0=普通, 1=高(手动触发), 2=紧急。高优先级任务排在队列前面,worker 先取。
func (s *Scheduler) Submit(taskID string, priority int) error {
	task := Task{
		ID:        taskID,
		Priority:  priority,
		CreatedAt: time.Now(),
	}

	s.mu.Lock()
	if len(s.queue) >= s.config.QueueSize {
		s.mu.Unlock()
		return fmt.Errorf("queue is full (capacity: %d)", s.config.QueueSize)
	}
	// 按优先级降序 + 创建时间升序插入(保持有序)
	inserted := false
	for i, t := range s.queue {
		if task.Priority > t.Priority {
			s.queue = append(s.queue, Task{})
			copy(s.queue[i+1:], s.queue[i:])
			s.queue[i] = task
			inserted = true
			break
		}
	}
	if !inserted {
		s.queue = append(s.queue, task)
	}
	s.stats.Queued++
	s.mu.Unlock()
	s.cond.Signal() // 通知一个等待的 worker

	log.Printf("[scheduler] 任务入队: %s (优先级:%d, 队列长度:%d)", taskID, priority, len(s.queue))
	return nil
}

// worker 是一个 worker goroutine。
// 从优先级队列取最高优先级的任务执行。
func (s *Scheduler) worker(id int) {
	defer s.wg.Done()
	log.Printf("[scheduler] worker %d 启动", id)

	for {
		s.mu.Lock()
		// 等待队列非空(条件变量,Stop 时广播唤醒)
		for len(s.queue) == 0 {
			select {
			case <-s.stopCh:
				s.mu.Unlock()
				log.Printf("[scheduler] worker %d 收到停止信号", id)
				return
			default:
			}
			s.cond.Wait()
			// 检查停止信号(广播唤醒后退出)
			select {
			case <-s.stopCh:
				s.mu.Unlock()
				log.Printf("[scheduler] worker %d 收到停止信号", id)
				return
			default:
			}
		}
		// 取队首(最高优先级)
		task := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()

		s.executeTask(id, task)
	}
}

// executeTask 执行单个任务。
func (s *Scheduler) executeTask(workerID int, task Task) {
	// 检查是否已在运行(防重复)
	s.mu.Lock()
	if s.running[task.ID] {
		s.mu.Unlock()
		return
	}
	s.running[task.ID] = true
	s.stats.Queued--
	s.stats.Running++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.running, task.ID)
		s.stats.Running--
		s.mu.Unlock()
	}()

	log.Printf("[scheduler] worker %d 开始执行任务 %s (重试:%d)", workerID, task.ID, task.RetryCount)

	// 执行(带超时)
	done := make(chan struct{})
	go func() {
		s.executeFn(task.ID)
		close(done)
	}()

	select {
	case <-done:
		// 正常完成
		s.mu.Lock()
		s.stats.Completed++
		s.mu.Unlock()
		log.Printf("[scheduler] worker %d 任务 %s 完成", workerID, task.ID)
	case <-time.After(time.Duration(s.config.TaskTimeout) * time.Second):
		// 超时
		log.Printf("[scheduler] worker %d 任务 %s 超时(%ds)", workerID, task.ID, s.config.TaskTimeout)
		s.handleRetry(task)
	}
}

// handleRetry 处理任务重试。
func (s *Scheduler) handleRetry(task Task) {
	if task.RetryCount >= s.config.MaxRetries {
		log.Printf("[scheduler] 任务 %s 达到最大重试次数(%d),标记失败", task.ID, s.config.MaxRetries)
		s.mu.Lock()
		s.stats.Failed++
		s.mu.Unlock()
		return
	}

	// 重新入队(重试次数+1,保留原优先级)
	task.RetryCount++
	if err := s.Submit(task.ID, task.Priority); err != nil {
		log.Printf("[scheduler] 任务 %s 重试失败:%v", task.ID, err)
		s.mu.Lock()
		s.stats.Failed++
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.stats.Retried++
	s.mu.Unlock()
	log.Printf("[scheduler] 任务 %s 重新入队(重试:%d)", task.ID, task.RetryCount)
}

// GetStats 获取调度器统计信息。
func (s *Scheduler) GetStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Queued = len(s.queue)
	return s.stats
}

// GetQueueLength 获取当前队列长度。
func (s *Scheduler) GetQueueLength() int {
	return len(s.queue)
}

// IsRunning 检查某个任务是否正在执行。
func (s *Scheduler) IsRunning(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[taskID]
}
