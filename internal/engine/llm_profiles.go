package engine

// llm_profiles.go — LLM Profile 多配置切换(skill #45)。
//
// Agent 可以管理多个 LLM profile(如 fast/quality/cheap),运行时自主切换。
// usage_id 指纹防 prompt cache 陷阱(切换后缓存自动失效)。

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// LLMProfile 单个 LLM 配置 profile。
type LLMProfile struct {
	Name      string `json:"name"`       // profile 名(如 "fast"/"quality")
	Model     string `json:"model"`      // 模型名
	BaseURL   string `json:"base_url"`   // 端点
	APIKey    string `json:"api_key"`    // Key(加密存储)
	APIFormat string `json:"api_format"` // 协议格式
}

// LLMProfiles profile 管理器。
type LLMProfiles struct {
	mu       sync.RWMutex
	profiles map[string]*LLMProfile
	active   string // 当前激活的 profile 名
	maxNum   int
}

// globalProfiles 全局 profile 管理器单例。
var globalProfiles = &LLMProfiles{
	profiles: make(map[string]*LLMProfile),
	maxNum:   10,
}

// GetLLMProfiles 获取全局 profile 管理器。
func GetLLMProfiles() *LLMProfiles { return globalProfiles }

// AddProfile 添加一个 profile。
func (p *LLMProfiles) AddProfile(profile LLMProfile) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.profiles) >= p.maxNum {
		return ErrTooManyProfiles
	}
	p.profiles[profile.Name] = &profile
	if p.active == "" {
		p.active = profile.Name
	}
	return nil
}

// SwitchProfile 切换到指定 profile。返回新的 usage_id(缓存指纹)。
func (p *LLMProfiles) SwitchProfile(name string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.profiles[name]; !ok {
		return "", ErrProfileNotFound
	}
	p.active = name
	// 切换后生成新的 usage_id(防止旧缓存命中)
	return generateUsageID(name), nil
}

// GetActive 返回当前激活的 profile。
func (p *LLMProfiles) GetActive() *LLMProfile {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.active == "" {
		return nil
	}
	return p.profiles[p.active]
}

// ListProfiles 列出所有 profile 名。
func (p *LLMProfiles) ListProfiles() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.profiles))
	for n := range p.profiles {
		names = append(names, n)
	}
	return names
}

// generateUsageID 生成 usage_id 指纹(防缓存陷阱)。
// 切换 profile 后,usage_id 变化,prompt cache 自动失效。
func generateUsageID(profileName string) string {
	h := sha256.Sum256([]byte(profileName))
	return hex.EncodeToString(h[:])[:16]
}

// 自定义错误
var (
	ErrTooManyProfiles  = &profileError{"已达到最大 profile 数"}
	ErrProfileNotFound  = &profileError{"profile 不存在"}
)

type profileError struct{ msg string }

func (e *profileError) Error() string { return e.msg }
