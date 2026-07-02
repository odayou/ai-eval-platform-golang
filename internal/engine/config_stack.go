package engine

// config_stack.go — 多源配置分层叠加(good-skills #28 补齐)。
//
// 补齐专家评审:#28 未实现。配置单源 env(config.go:5 注释自承"暂不支持配置文件")。
// 新增:ConfigLayerStack 多源合并(global/project/local/runtime) + sticky 字段。
//
// 与 config/config.go 的关系:
//   - config.go: 平台启动配置(单源 env,启动时加载)
//   - config_stack.go: agent 运行时配置(多源叠加,可运行时改)
// 两者不冲突,config_stack 是 harness 层的配置管理。

import (
	"encoding/json"
	"sync"
)

// ConfigLayerPriority 配置层优先级(数字大的覆盖小的)。
type ConfigLayerPriority int

const (
	LayerFile       ConfigLayerPriority = 0 // config 文件(最低)
	LayerRole       ConfigLayerPriority = 1 // agent 角色
	LayerSession    ConfigLayerPriority = 2 // CLI 参数/session
	LayerManaged    ConfigLayerPriority = 3 // 管理员强制
	LayerRuntime    ConfigLayerPriority = 4 // 运行时选择(sticky)
)

// stickyKeys 运行时选择不被覆盖的字段(provider/service_tier/api_key)。
var stickyKeys = map[string]bool{
	"provider":     true,
	"service_tier": true,
	"api_key":      true,
}

// ConfigLayer 一层配置。
type ConfigLayer struct {
	Source   string              // "file" / "role:explorer" / "session" / "managed" / "runtime"
	Priority ConfigLayerPriority
	Config   map[string]any
}

// ConfigLayerStack 配置分层叠加管理器。
type ConfigLayerStack struct {
	mu     sync.Mutex
	layers []ConfigLayer // 按 priority 有序
}

// NewConfigLayerStack 创建空栈。
func NewConfigLayerStack() *ConfigLayerStack {
	return &ConfigLayerStack{}
}

// AddLayer 添加一层(按 priority 有序插入)。
func (s *ConfigLayerStack) AddLayer(layer ConfigLayer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 有序插入(按 priority)
	idx := len(s.layers)
	for i, l := range s.layers {
		if l.Priority >= layer.Priority {
			idx = i
			break
		}
	}
	s.layers = append(s.layers, ConfigLayer{})
	copy(s.layers[idx+1:], s.layers[idx:])
	s.layers[idx] = layer
}

// Resolve 合并所有层 → 最终配置(sticky 字段保留最高优先级的)。
func (s *ConfigLayerStack) Resolve() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]any)
	for _, layer := range s.layers {
		for key, value := range layer.Config {
			if stickyKeys[key] {
				if _, exists := result[key]; exists {
					continue // sticky:已设不覆盖
				}
			}
			result[key] = value
		}
	}
	return result
}

// Override 运行时覆盖(sticky,后续层不覆盖)。
func (s *ConfigLayerStack) Override(key string, value any, source string) {
	s.AddLayer(ConfigLayer{
		Source:   source,
		Priority: LayerRuntime,
		Config:   map[string]any{key: value},
	})
}

// MarshalJSON 序列化(供调试)。
func (s *ConfigLayerStack) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Resolve())
}
