package engine

// plugin_system.go — 插件系统(skill #33)。
// 四扩展点(agents/commands/skills/hooks) + 三层治理(installed/marketplace/blocklist)。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PluginManifest 插件清单(plugin.json)。
type PluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	// 四扩展点
	Agents   []string `json:"agents,omitempty"`   // agent 定义文件
	Commands []string `json:"commands,omitempty"` // 命令定义
	Skills   []string `json:"skills,omitempty"`   // skill 目录
	Hooks    []string `json:"hooks,omitempty"`    // hook 定义
}

// PluginStatus 插件状态。
type PluginStatus string

const (
	PluginInstalled  PluginStatus = "installed"
	PluginMarketplace PluginStatus = "marketplace"
	PluginBlocked     PluginStatus = "blocked"
)

// Plugin 已安装的插件。
type Plugin struct {
	Manifest PluginManifest
	Status   PluginStatus
	Path     string // 插件目录路径
}

// PluginRegistry 插件注册表。
type PluginRegistry struct {
	mu       sync.RWMutex
	plugins  map[string]*Plugin
	pluginsDir string // 插件根目录
}

// NewPluginRegistry 创建插件注册表。
func NewPluginRegistry(pluginsDir string) *PluginRegistry {
	return &PluginRegistry{
		plugins:    make(map[string]*Plugin),
		pluginsDir: pluginsDir,
	}
}

// DiscoverPlugins 扫描插件目录,加载所有 plugin.json。
func (r *PluginRegistry) DiscoverPlugins() error {
	entries, err := os.ReadDir(r.pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 无插件目录,正常
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(r.pluginsDir, entry.Name(), "plugin.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // 无 manifest,跳过
		}
		var manifest PluginManifest
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		if manifest.Name == "" {
			manifest.Name = entry.Name()
		}
		r.mu.Lock()
		r.plugins[manifest.Name] = &Plugin{
			Manifest: manifest,
			Status:   PluginInstalled,
			Path:     filepath.Join(r.pluginsDir, entry.Name()),
		}
		r.mu.Unlock()
	}
	return nil
}

// GetPlugin 获取插件。
func (r *PluginRegistry) GetPlugin(name string) (*Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// ListPlugins 列出所有插件。
func (r *PluginRegistry) ListPlugins() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		list = append(list, p)
	}
	return list
}

// BlockPlugin 将插件加入黑名单。
func (r *PluginRegistry) BlockPlugin(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.plugins[name]; ok {
		p.Status = PluginBlocked
		return nil
	}
	return fmt.Errorf("plugin %q not found", name)
}
