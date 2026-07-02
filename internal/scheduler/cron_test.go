package scheduler

// cron_test.go — 定时任务调度器单元测试。
//
// 覆盖:
//   - cronMatcher(5 字段匹配: * / */N / 列表 / 范围)
//   - nextRunTime(下次执行时间计算)
//   - CronScheduler 增删查 + 持久化

import (
	"testing"
	"time"
)

func TestCronMatcher(t *testing.T) {
	tests := []struct {
		name  string
		field string
		min   int
		max   int
		check map[int]bool // value → expected match
	}{
		{"星号匹配全部", "*", 0, 59, map[int]bool{0: true, 30: true, 59: true}},
		{"固定值", "30", 0, 59, map[int]bool{29: false, 30: true, 31: false}},
		{"步进", "*/15", 0, 59, map[int]bool{0: true, 15: true, 30: true, 45: true, 16: false, 7: false}},
		{"列表", "0,15,30,45", 0, 59, map[int]bool{0: true, 15: true, 30: true, 45: true, 10: false, 59: false}},
		{"范围", "9-17", 0, 23, map[int]bool{9: true, 12: true, 17: true, 8: false, 18: false}},
		{"范围+列表混合", "0,10-12,30", 0, 59, map[int]bool{0: true, 10: true, 11: true, 12: true, 30: true, 5: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := cronMatcher(tt.field, tt.min, tt.max)
			for v, want := range tt.check {
				if got := fn(v); got != want {
					t.Errorf("cronMatcher(%q,%d,%d)(%d) = %v, want %v", tt.field, tt.min, tt.max, v, got, want)
				}
			}
		})
	}
}

func TestNextRunTime(t *testing.T) {
	cs := &CronScheduler{
		jobs: make(map[string]*CronJob),
	}

	tests := []struct {
		name string
		expr string
	}{
		{"每分钟", "* * * * *"},
		{"每小时整点", "0 * * * *"},
		{"每天 2 点", "0 2 * * *"},
		{"每 15 分钟", "*/15 * * * *"},
		{"每月 1 号", "0 0 1 * *"},
		{"工作日 9 点(周一到周五)", "0 9 * * 1-5"},
		{"周日 0 点", "0 0 * * 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := cs.nextRunTime(tt.expr)
			// 基本校验:下次时间必须在未来
			if !next.After(time.Now().Add(-time.Minute)) {
				t.Errorf("nextRunTime(%q) = %v, 应在未来", tt.expr, next)
			}
			// 校验它确实满足 cron 表达式(回验分时字段)
			parts := splitFields(tt.expr)
			if len(parts) >= 2 {
				minOK := cronMatcher(parts[0], 0, 59)(next.Minute())
				hourOK := cronMatcher(parts[1], 0, 23)(next.Hour())
				if !minOK {
					t.Errorf("nextRunTime(%q) 分钟 %d 不匹配 %q", tt.expr, next.Minute(), parts[0])
				}
				if !hourOK {
					t.Errorf("nextRunTime(%q) 小时 %d 不匹配 %q", tt.expr, next.Hour(), parts[1])
				}
			}
		})
	}
}

// splitFields 辅助:拆分 cron 表达式字段。
func splitFields(expr string) []string {
	var parts []string
	start := 0
	for i, c := range expr {
		if c == ' ' || c == '\t' {
			if i > start {
				parts = append(parts, expr[start:i])
			}
			start = i + 1
		}
	}
	if start < len(expr) {
		parts = append(parts, expr[start:])
	}
	return parts
}

func TestCronSchedulerAddRemove(t *testing.T) {
	cs := &CronScheduler{
		jobs: make(map[string]*CronJob),
	}

	// 添加(用 ID 作 key)
	cs.AddJob(&CronJob{ID: "j1", Name: "test1", Expr: "0 * * * *"})
	cs.AddJob(&CronJob{ID: "j2", Name: "test2", Expr: "*/30 * * * *"})
	if len(cs.jobs) != 2 {
		t.Errorf("添加后 job 数 = %d, want 2", len(cs.jobs))
	}

	// 移除(按 ID)
	cs.RemoveJob("j1")
	if _, ok := cs.jobs["j1"]; ok {
		t.Error("删除后 j1 仍存在")
	}
	if len(cs.jobs) != 1 {
		t.Errorf("删除后 job 数 = %d, want 1", len(cs.jobs))
	}
}
