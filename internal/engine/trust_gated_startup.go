package engine

// trust_gated_startup.go — 三阶段安全启动门控(skill #14)。
// prefetch → context → trust 门控 deferred init + bootstrap graph。

import (
	"sync"
)

// BootstrapPhase 启动阶段。
type BootstrapPhase int

const (
	PhasePrefetch BootstrapPhase = iota // 预加载(可信环境才执行)
	PhaseContext                        // 上下文初始化
	PhaseTrust                          // trust 门控(检查可信后放行)
)

// BootstrapMilestone 启动里程碑。
type BootstrapMilestone struct {
	Name     string
	Phase    BootstrapPhase
	Required bool                 // 是否必须成功
	Fn       func() error         // 初始化函数
	RunAfter []string             // 依赖的里程碑(先跑完才能跑)
}

// BootstrapGraph 启动图(管理里程碑的依赖顺序执行)。
type BootstrapGraph struct {
	mu         sync.Mutex
	milestones []BootstrapMilestone
	completed  map[string]bool
	trustedEnv bool // 环境是否可信(不可信则跳过 prefetch)
}

// NewBootstrapGraph 创建启动图。
func NewBootstrapGraph(trustedEnv bool) *BootstrapGraph {
	return &BootstrapGraph{
		completed:  make(map[string]bool),
		trustedEnv: trustedEnv,
	}
}

// AddMilestone 添加里程碑。
func (g *BootstrapGraph) AddMilestone(m BootstrapMilestone) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.milestones = append(g.milestones, m)
}

// Run 按依赖顺序执行所有里程碑。
// 不可信环境跳过 PhasePrefetch 阶段的里程碑。
func (g *BootstrapGraph) Run() error {
	g.mu.Lock()
	milestones := make([]BootstrapMilestone, len(g.milestones))
	copy(milestones, g.milestones)
	g.mu.Unlock()

	for _, m := range milestones {
		// 不可信环境跳过 prefetch
		if m.Phase == PhasePrefetch && !g.trustedEnv {
			NewSlog("bootstrap").Warn("跳过 prefetch(不可信环境)", "Name", m.Name)
			continue
		}

		// 检查依赖是否已完成
		for _, dep := range m.RunAfter {
			if !g.completed[dep] {
				if m.Required {
					return ErrBootstrapDepNotMet{m.Name, dep}
				}
				NewSlog("bootstrap").Warn("跳过(依赖未满足): 需要", "Name", m.Name, "dep", dep)
				continue
			}
		}

		// 执行
		if m.Fn != nil {
			if err := m.Fn(); err != nil {
				if m.Required {
					return err
				}
				NewSlog("bootstrap").Error("非必须里程碑失败(忽略)", "Name", m.Name, "err", err)
				continue
			}
		}
		g.mu.Lock()
		g.completed[m.Name] = true
		g.mu.Unlock()
		NewSlog("bootstrap").Info("里程碑完成: (phase=)", "Name", m.Name, "Phase", m.Phase)
	}
	return nil
}

// IsCompleted 检查里程碑是否已完成。
func (g *BootstrapGraph) IsCompleted(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.completed[name]
}

// ErrBootstrapDepNotMet 依赖未满足错误。
type ErrBootstrapDepNotMet struct {
	Milestone string
	Dep       string
}

func (e ErrBootstrapDepNotMet) Error() string {
	return "bootstrap: " + e.Milestone + " 需要 " + e.Dep + " 先完成"
}
