package engine

// agents_md_discovery.go — AGENTS.md 文件系统多层发现(good-skills #23 补齐)。
//
// 补齐专家评审:#23 有根 AGENTS.md + agent.agents_md 字段,缺文件系统多层发现。
// 新增:从 cwd 向上找 .git 定位项目根 + 逐层收集 AGENTS.md + override 优先。
//
// 研究依据:codex core/src/agents_md.rs(多层发现 + AGENTS.override.md 优先)。

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// projectRootMarkers 项目根标记（8.1 扩展：加前端/Rust/C++/Python 项目标记）。
var projectRootMarkers = []string{
	".git", ".hg", ".svn",
	"go.mod", "Cargo.toml", "CMakeLists.txt",
	"package.json", "pyproject.toml", "setup.py",
	"Makefile", "Gemfile",
}

// candidateFilenames AGENTS.md 候选文件名(优先级高→低)。
var candidateFilenames = []string{
	"AGENTS.override.md", // 最高优先(本地覆盖)
	"AGENTS.md",          // 标准项目
	"CLAUDE.md",          // 兼容
}

// FindProjectRoot 从 cwd 向上找项目根。
func FindProjectRoot(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(cwd)
	current := abs
	for {
		for _, marker := range projectRootMarkers {
			if _, err := os.Stat(filepath.Join(current, marker)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break // 根目录
		}
		current = parent
	}
	return abs
}

// DiscoverAgentsMD 从项目根到 cwd 逐层收集 AGENTS.md(每层取优先级最高的)。
func DiscoverAgentsMD(cwd string) []string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(cwd)
	root := FindProjectRoot(abs)

	// 收集 root → cwd 路径上的所有目录
	var dirs []string
	current := abs
	for {
		dirs = append(dirs, current)
		if current == root {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	// 反转:从根到 cwd
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	var found []string
	for _, dir := range dirs {
		// 每层取优先级最高的
		for _, name := range candidateFilenames {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				found = append(found, candidate)
				break // 每层只取一个
			}
		}
	}
	return found
}

// LoadAndConcatAgentsMD 加载并拼接 AGENTS.md 文件(字节预算截断)。
func LoadAndConcatAgentsMD(cwd string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 32768 // 默认 32K
	}
	paths := DiscoverAgentsMD(cwd)
	var parts []string
	total := 0
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cost := len(content)
		if total+cost > maxBytes {
			remaining := maxBytes - total
			if remaining > 0 {
				parts = append(parts, string(content[:remaining]))
			}
			break
		}
		parts = append(parts, string(content))
		total += cost
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n--- project-doc ---\n\n")
}

// GetAgentsMDForCWD 一站式:发现 + 加载 + 拼接。
func GetAgentsMDForCWD(cwd string) string {
	return LoadAndConcatAgentsMD(cwd, 32768)
}

// ── 8.2 缓存（30s TTL + mtime 失效检测）──

type agentsMDCacheEntry struct {
	content string
	time    time.Time
	mtime   time.Time
}

var (
	agentsMDCache   = make(map[string]agentsMDCacheEntry)
	agentsMDCacheMu sync.RWMutex
)

// GetAgentsMDForCWDCached 带缓存的 AGENTS.md 获取。
//
// 8.3 实现：双层失效策略
//   - TTL 失效：30 秒过期（常规场景）
//   - mtime 失效：文件修改时间变化时立即失效（不等 TTL）
//
// 这样既保证了高频调用时的性能（缓存命中），
// 又保证了文件编辑后能及时感知（mtime 检测）。
func GetAgentsMDForCWDCached(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(cwd)

	agentsMDCacheMu.RLock()
	entry, ok := agentsMDCache[abs]
	agentsMDCacheMu.RUnlock()

	if ok {
		// 8.3：TTL 内也要检查 mtime（文件变更立即失效，不等 TTL）
		if time.Since(entry.time) < 30*time.Second {
			if !isAgentsMDModified(abs, entry.mtime) {
				return entry.content
			}
			// mtime 变化，即使 TTL 未过期也要刷新缓存
			NewSlog("agents_md").Info("文件变更检测: mtime 已变化,缓存立即失效(cwd=)", "abs", abs)
		}
		// 8.3：TTL 过期后检查 mtime（文件未改则续期，不重新读取）
		if !isAgentsMDModified(abs, entry.mtime) {
			agentsMDCacheMu.Lock()
			entry.time = time.Now() // 续期
			agentsMDCache[abs] = entry
			agentsMDCacheMu.Unlock()
			return entry.content
		}
	}

	// 缓存未命中或文件已修改 → 重新加载
	content, latestMtime := loadAndConcatWithMtime(abs, 32768)
	agentsMDCacheMu.Lock()
	agentsMDCache[abs] = agentsMDCacheEntry{
		content: content,
		time:    time.Now(),
		mtime:   latestMtime,
	}
	agentsMDCacheMu.Unlock()
	return content
}

// isAgentsMDModified 检测 AGENTS.md 文件是否被修改（8.3 mtime 失效）。
func isAgentsMDModified(cwd string, cachedMtime time.Time) bool {
	paths := DiscoverAgentsMD(cwd)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(cachedMtime) {
			return true // 任意一个文件被修改即失效
		}
	}
	return false
}

// loadAndConcatWithMtime 加载 AGENTS.md 并返回最新 mtime。
func loadAndConcatWithMtime(cwd string, maxBytes int) (string, time.Time) {
	if maxBytes <= 0 {
		maxBytes = 32768
	}
	paths := DiscoverAgentsMD(cwd)
	var parts []string
	var latestMtime time.Time
	total := 0
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMtime) {
			latestMtime = info.ModTime()
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cost := len(content)
		if total+cost > maxBytes {
			remaining := maxBytes - total
			if remaining > 0 {
				parts = append(parts, string(content[:remaining]))
			}
			break
		}
		parts = append(parts, string(content))
		total += cost
	}
	if len(parts) == 0 {
		return "", latestMtime
	}
	return strings.Join(parts, "\n\n--- project-doc ---\n\n"), latestMtime
}
