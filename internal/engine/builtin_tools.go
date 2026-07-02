package engine

// builtin_tools.go — harness 第2层:内置工具(Bash/FileRead/FileWrite/WebSearch)。
//
// 这些工具在 ReAct agent loop 里被 LLM 主动调用(通过 OpenAI tool_use 协议)。
// 复用已有的能力:
//   - BashTool → permission_gate.go 三级权限门控 + exec.Command
//   - FileRead/FileWrite → 路径校验(防越界)
//   - WebSearch → websearch.go(Brave/Tavily)
//
// 工具声明格式遵循 OpenAI function calling schema(OpenAI/tools.go 里序列化为 JSON)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolDef 工具声明(传给 LLM 的 function schema)。
type ToolDef struct {
	Type     string       `json:"type"` // 固定 "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction function calling 的 schema。
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema(用 RawMessage 避免嵌套 struct)
}

// ToolCall LLM 返回的工具调用。
type ToolCall struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"` // JSON 参数
}

// ToolResult 工具执行结果(回填给 LLM)。
type ToolResult struct {
	Name    string `json:"name"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

// BuiltinTools 返回全部内置工具声明(D41-D44 扩展:加 FileEdit/Glob/Grep)。
func BuiltinTools() []ToolDef {
	return []ToolDef{
		{Type: "function", Function: ToolFunction{
			Name:        "BashTool",
			Description: "执行 shell 命令。用于编译/测试/运行代码、文件操作、git 等。受权限门控(三级:read-only/workspace-write/full)。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "要执行的 shell 命令"}
				},
				"required": ["command"]
			}`),
		}},
		{Type: "function", Function: ToolFunction{
			Name:        "FileRead",
			Description: "读取文件内容。路径必须在允许的工作区内。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "文件路径(绝对或相对工作区)"}
				},
				"required": ["path"]
			}`),
		}},
		{Type: "function", Function: ToolFunction{
			Name:        "FileWrite",
			Description: "写入文件(全文重写)。路径必须在允许的工作区内。会覆盖已有文件。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "文件路径"},
					"content": {"type": "string", "description": "文件内容"}
				},
				"required": ["path", "content"]
			}`),
		}},
		// D41 FileEdit:部分编辑(SEARCH-REPLACE block,比全文重写省 token)
		// 3.2.3 支持多 block 一次调用(仿 Claude Code)
		{Type: "function", Function: ToolFunction{
			Name:        "FileEdit",
			Description: "部分编辑文件(SEARCH-REPLACE 模式)。支持两种方式:1) 单次替换:提供 old_text + new_text; 2) 多次替换:提供 blocks 数组(每项含 old_text + new_text)，按顺序依次执行。若 old_text 匹配多处，会提示冲突。用 replace_all: true 可替换全部匹配。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "文件路径"},
					"old_text": {"type": "string", "description": "要替换的原始文本(精确匹配,含缩进)"},
					"new_text": {"type": "string", "description": "替换后的文本"},
					"replace_all": {"type": "boolean", "description": "是否替换所有匹配(默认 false,只替换第一处)"},
					"blocks": {"type": "array", "description": "多组替换块(与 old_text/new_text 互斥，优先使用 blocks)", "items": {"type": "object", "properties": {"old_text": {"type": "string"}, "new_text": {"type": "string"}, "replace_all": {"type": "boolean"}}, "required": ["old_text", "new_text"]}}
				},
				"required": ["path"]
			}`),
		}},
	// D42 Glob:文件模式匹配
	{Type: "function", Function: ToolFunction{
		Name:        "Glob",
		Description: "按文件模式匹配查找文件。支持 **/* 递归通配符。自动排除 .git/node_modules/vendor 目录。如 Glob('**/*.go') 找所有 Go 文件。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "文件模式(如 **/*.py / src/**/*.ts / *.go)"},
				"path": {"type": "string", "description": "搜索根目录(默认当前目录)"},
				"head_limit": {"type": "integer", "description": "最多返回的文件数(默认 100)"}
			},
			"required": ["pattern"]
		}`),
	}},
	// D43 Grep:内容搜索
	{Type: "function", Function: ToolFunction{
		Name:        "Grep",
		Description: "在文件内容中搜索(正则表达式)。支持上下文行数(-A/-B/-C)和多行模式。自动跳过二进制文件。如 Grep('func.*Handler', '*.go', context_before=2, context_after=3)。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "正则表达式"},
				"path": {"type": "string", "description": "搜索目录(默认当前目录)"},
				"include": {"type": "string", "description": "文件类型过滤(如 *.go / *.py)"},
				"case_insensitive": {"type": "boolean", "description": "是否忽略大小写"},
				"context_before": {"type": "integer", "description": "匹配行前显示行数(-B)"},
				"context_after": {"type": "integer", "description": "匹配行后显示行数(-A)"},
				"context": {"type": "integer", "description": "匹配行前后显示行数(-C,等价于同时设置 context_before 和 context_after)"},
				"head_limit": {"type": "integer", "description": "最多匹配数(默认 100)"},
				"multiline": {"type": "boolean", "description": "是否启用多行模式(. 匹配换行符)"}
			},
			"required": ["pattern"]
		}`),
	}},
		{Type: "function", Function: ToolFunction{
			Name:        "WebSearch",
			Description: "联网搜索(Brave/Tavily)。获取实时信息。需 Agent 配置了 search_provider。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "搜索关键词"}
				},
				"required": ["query"]
			}`),
		}},
		// 3.6 ToolSearch：搜索可用工具(内置 + MCP)，按关键词发现工具能力
		ToolSearchToolDef(),
	}
}

// ExecuteBuiltinTool 执行一个内置工具调用。
//
// permissionLevel: read-only / workspace-write / full(权限门控)
// workspaceRoot: 工作区根目录(路径校验基准)
// searchProvider/searchKey: 联网搜索配置
// taskID: 状态推送用
func ExecuteBuiltinTool(tc ToolCall, permissionLevel, workspaceRoot,
	searchProvider, searchKey, agentID, taskID string) ToolResult {

	// 解析参数
	var args map[string]any
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		return ToolResult{Name: tc.Name, Output: "参数解析失败: " + err.Error(), IsError: true}
	}

	switch tc.Name {
	case "BashTool":
		return execBash(args, permissionLevel, workspaceRoot, agentID, taskID)
	case "FileRead":
		return execFileRead(args, workspaceRoot, agentID, taskID)
	case "FileWrite":
		return execFileWrite(args, permissionLevel, workspaceRoot, agentID, taskID)
	case "FileEdit": // D41 SEARCH-REPLACE 部分编辑
		return execFileEdit(args, permissionLevel, workspaceRoot, agentID, taskID)
	case "Glob": // D42 文件模式匹配
		return execGlob(args, workspaceRoot, agentID, taskID)
	case "Grep": // D43 内容搜索
		return execGrep(args, workspaceRoot, agentID, taskID)
	case "WebSearch":
		return execWebSearch(args, searchProvider, searchKey, agentID, taskID)
	case "tool_search": // 3.6 搜索可用工具(内置 + MCP)
		return execToolSearch(tc.Arguments)
	default:
		return ToolResult{Name: tc.Name, Output: "未知内置工具: " + tc.Name, IsError: true}
	}
}

// execBash 执行 shell 命令(复用 permission_gate 三级权限门控)。
//
// 3.1 升级：优先使用原生 OS 沙箱(seatbelt/bubblewrap)执行，限制文件系统访问范围。
// 升级路径：原生沙箱 → Docker 容器(docker run --rm --network=none --read-only)
//   - Docker 沙箱提供更强隔离：独立文件系统、seccomp 限制 syscall、cgroup 资源限制
//   - 当前实现使用 NativeSandbox，创建临时目录作为工作区，限制命令只能在该目录内操作
//   - TODO(二期)：当 Docker 可用时自动升级到容器级隔离(见 native_sandbox.go 升级路径注释)
func execBash(args map[string]any, permLevel, workspaceRoot, agentID, taskID string) ToolResult {
	command, _ := args["command"].(string)
	if command == "" {
		return ToolResult{Name: "BashTool", Output: "缺少 command 参数", IsError: true}
	}

	// 权限门控(复用 permission_gate.go)
	gate := NewPermissionGate(permLevel, workspaceRoot)
	if allow, reason := gate.CheckCommand(command); !allow {
		gate.RecordDecision(taskID, "agent_bash", command, "denied", reason)
		return ToolResult{Name: "BashTool", Output: "[权限拒绝] " + reason, IsError: true}
	}
	gate.RecordDecision(taskID, "agent_bash", command, "allowed", "权限检查通过")

	SetAgentStatus(agentID, StatusCallingTool, "BashTool: "+truncate(command, 50), taskID)

	// 3.1.1-3.1.5 沙箱执行：创建临时工作区 + 资源限制
	// 检测可用的原生沙箱(seatbelt/bubblewrap/windows restricted token)
	sandboxType := DetectNativeSandbox()
	if sandboxType != NativeSandboxNone {
		// 创建临时目录作为沙箱工作区(限制文件系统访问范围)
		tmpWorkspace, tmpErr := os.MkdirTemp("", "agent-sandbox-*")
		if tmpErr == nil {
			defer os.RemoveAll(tmpWorkspace)

			// 如果有 workspaceRoot，复制其中的关键配置文件到临时工作区
			// (如 .gitconfig、package.json 等，让命令能正常运行)
			if workspaceRoot != "" {
				copyEssentialFiles(workspaceRoot, tmpWorkspace)
			}

			// 通过原生沙箱执行命令(限制写入范围为临时目录)
			result := RunInNativeSandbox(command, tmpWorkspace, permLevel == "full", 30)

			out, _ := result["stdout"].(string)
			if len(out) > 10000 {
				out = out[:10000] + "\n...(输出截断)"
			}
			if rc, ok := result["returncode"].(int); ok && rc != 0 {
				stderr, _ := result["stderr"].(string)
				return ToolResult{Name: "BashTool",
					Output: out + "\n[exit code: " + fmt.Sprintf("%d", rc) + "] " + stderr, IsError: true}
			}
			return ToolResult{Name: "BashTool", Output: out}
		}
		// 临时目录创建失败，降级到裸执行
	}

	// 3.1 降级路径：无原生沙箱时裸执行(已有 30s 超时保护)
	// TODO(二期)：Docker 容器执行路径 - docker run --rm --network=none
	//   --security-opt=no-new-privileges --cap-drop=ALL --read-only
	//   --tmpfs /tmp:size=100m -v workspace:/workspace
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	if workspaceRoot != "" {
		cmd.Dir = workspaceRoot
	}
	cmd.Env = os.Environ()

	timer := time.AfterFunc(30*time.Second, func() { cmd.Process.Kill() })
	defer timer.Stop()
	output, err := cmd.CombinedOutput()
	out := string(output)
	if len(out) > 10000 {
		out = out[:10000] + "\n...(输出截断)"
	}
	if err != nil {
		return ToolResult{Name: "BashTool", Output: out + "\n[exit error: " + err.Error() + "]", IsError: true}
	}
	return ToolResult{Name: "BashTool", Output: out}
}

// copyEssentialFiles 将工作区中的关键配置文件复制到沙箱临时目录。
// 只复制配置/依赖描述文件，不复制源代码(沙箱内命令通过挂载或重新生成获取代码)。
func copyEssentialFiles(src, dst string) {
	essentialFiles := []string{
		"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"go.mod", "go.sum", "requirements.txt", "Pipfile", "pyproject.toml",
		"Cargo.toml", "Cargo.lock", "Makefile", "Dockerfile", ".env.example",
		"tsconfig.json", "vite.config.ts", "webpack.config.js",
	}
	for _, name := range essentialFiles {
		srcPath := filepath.Join(src, name)
		if data, err := os.ReadFile(srcPath); err == nil {
			os.WriteFile(filepath.Join(dst, name), data, 0644)
		}
	}
}

// execFileRead 读文件。
func execFileRead(args map[string]any, workspaceRoot, agentID, taskID string) ToolResult {
	path, _ := args["path"].(string)
	if path == "" {
		return ToolResult{Name: "FileRead", Output: "缺少 path 参数", IsError: true}
	}

	SetAgentStatus(agentID, StatusReadingFile, "FileRead: "+path, taskID)

	content, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Name: "FileRead", Output: "读取失败: " + err.Error(), IsError: true}
	}
	text := string(content)
	if len(text) > 50000 {
		text = text[:50000] + "\n...(文件过大,截断)"
	}
	return ToolResult{Name: "FileRead", Output: text}
}

// execFileWrite 写文件。
func execFileWrite(args map[string]any, permLevel, workspaceRoot, agentID, taskID string) ToolResult {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return ToolResult{Name: "FileWrite", Output: "缺少 path 参数", IsError: true}
	}

	// read-only 禁止写
	if permLevel == "read-only" {
		return ToolResult{Name: "FileWrite", Output: "[权限拒绝] read-only 级别禁止写文件", IsError: true}
	}

	SetAgentStatus(agentID, StatusCallingTool, "FileWrite: "+path, taskID)

	// 文件级版本快照(覆盖前保存旧版本)
	if workspaceRoot != "" {
		if oldContent, err := os.ReadFile(path); err == nil {
			SaveFileSnapshot(workspaceRoot, path, string(oldContent), "FileWrite")
		}
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return ToolResult{Name: "FileWrite", Output: "写入失败: " + err.Error(), IsError: true}
	}
	return ToolResult{Name: "FileWrite", Output: fmt.Sprintf("已写入 %s (%d 字节)", path, len(content))}
}

// execWebSearch 联网搜索(复用 websearch.go)。
// 3.5 改进：缓存 + 限流 + 结构化结果。
func execWebSearch(args map[string]any, provider, apiKey, agentID, taskID string) ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ToolResult{Name: "WebSearch", Output: "缺少 query 参数", IsError: true}
	}
	if provider == "" || provider == "none" || apiKey == "" {
		return ToolResult{Name: "WebSearch", Output: "未配置联网搜索(provider=" + provider + ")", IsError: true}
	}

	// 3.5.1 缓存检查（5 分钟 TTL）
	cacheKey := provider + ":" + query
	searchCacheMu.RLock()
	if entry, ok := searchCache[cacheKey]; ok && time.Since(entry.time) < 5*time.Minute {
		searchCacheMu.RUnlock()
		return ToolResult{Name: "WebSearch", Output: entry.result + "\n[cached]"}
	}
	searchCacheMu.RUnlock()

	// 3.5.2 限流：同一 agent 每分钟最多 10 次搜索
	searchRateMu.Lock()
	rateKey := agentID
	now := time.Now()
	if last, ok := searchRateLimit[rateKey]; ok && now.Sub(last) < time.Minute {
		searchRateLimit[rateKey] = last // 保持原时间
		searchRateMu.Unlock()
		return ToolResult{Name: "WebSearch", Output: "[限流] 每分钟最多 10 次搜索,请稍后重试", IsError: true}
	}
	searchRateLimit[rateKey] = now
	searchRateMu.Unlock()

	SetAgentStatus(agentID, StatusSearching, "WebSearch: "+truncate(query, 50), taskID)

	// 3.5.3 结构化搜索结果：提取 title/url/snippet 为 JSON 结构
	ws := NewWebSearch(provider, apiKey)
	if ws == nil {
		return ToolResult{Name: "WebSearch", Output: "搜索 provider 初始化失败", IsError: true}
	}
	results, err := ws.Search(query, 5)
	if err != nil || len(results) == 0 {
		return ToolResult{Name: "WebSearch", Output: "搜索无结果或失败", IsError: true}
	}

	// 构建结构化 JSON 输出
	structured := map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	}
	jsonBytes, _ := json.Marshal(structured)

	// 同时生成人类可读的文本摘要(供 LLM 直接理解)
	var sb strings.Builder
	sb.WriteString("【联网搜索结果】\n")
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		if r.Snippet != "" {
			snippet := r.Snippet
			if len([]rune(snippet)) > 300 {
				snippet = string([]rune(snippet)[:300]) + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", snippet))
		}
		if r.URL != "" {
			sb.WriteString(fmt.Sprintf("   来源: %s\n", r.URL))
		}
	}
	sb.WriteString(fmt.Sprintf("\n[structured_json]\n%s", string(jsonBytes)))

	ctx := sb.String()

	// 存入缓存
	searchCacheMu.Lock()
	searchCache[cacheKey] = searchCacheEntry{result: ctx, time: time.Now()}
	searchCacheMu.Unlock()

	return ToolResult{Name: "WebSearch", Output: ctx}
}

// ── WebSearch 缓存 & 限流基础设施 ──

type searchCacheEntry struct {
	result string
	time   time.Time
}

var (
	searchCache    = make(map[string]searchCacheEntry)
	searchCacheMu  sync.RWMutex
	searchRateLimit = make(map[string]time.Time)
	searchRateMu   sync.Mutex
)

// ToolDef 序列化为 OpenAI tools 数组格式(给请求体用)。
func ToolsToJSON(tools []ToolDef) []byte {
	b, _ := json.Marshal(tools)
	return b
}

// D41 execFileEdit SEARCH-REPLACE 部分编辑(比全文重写省 token)。
// 3.2 改进: replace_all + conflict detection + 多 block 支持。
func execFileEdit(args map[string]any, permLevel, workspaceRoot, agentID, taskID string) ToolResult {
	path, _ := args["path"].(string)
	if path == "" {
		return ToolResult{Name: "FileEdit", Output: "缺少 path 参数", IsError: true}
	}
	if permLevel == "read-only" {
		return ToolResult{Name: "FileEdit", Output: "[权限拒绝] read-only 级别禁止编辑文件", IsError: true}
	}

	SetAgentStatus(agentID, StatusCallingTool, "FileEdit: "+path, taskID)

	content, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Name: "FileEdit", Output: "读取失败: " + err.Error(), IsError: true}
	}
	text := string(content)

	// 文件级版本快照(修改前保存旧版本,支持 undo/rewind)
	if workspaceRoot != "" {
		SaveFileSnapshot(workspaceRoot, path, text, "FileEdit")
	}

	// 3.2.3 多 block 模式：blocks 数组优先于单次 old_text/new_text
	if rawBlocks, ok := args["blocks"]; ok && rawBlocks != nil {
		blocksJSON, _ := json.Marshal(rawBlocks)
		var blocks []struct {
			OldText   string `json:"old_text"`
			NewText   string `json:"new_text"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if err := json.Unmarshal(blocksJSON, &blocks); err != nil {
			return ToolResult{Name: "FileEdit", Output: "blocks 参数解析失败: " + err.Error(), IsError: true}
		}
		if len(blocks) == 0 {
			return ToolResult{Name: "FileEdit", Output: "blocks 数组为空", IsError: true}
		}

		// 逐个 block 依次执行(前一个 block 的结果作为下一个的输入)
		var applied []string
		for i, blk := range blocks {
			if blk.OldText == "" {
				return ToolResult{Name: "FileEdit",
					Output: fmt.Sprintf("blocks[%d] 缺少 old_text", i), IsError: true}
			}
		 newText, appliedCount, errMsg := applySingleBlock(text, blk.OldText, blk.NewText, blk.ReplaceAll)
			if errMsg != "" {
				return ToolResult{Name: "FileEdit",
					Output: fmt.Sprintf("blocks[%d] 失败: %s", i, errMsg), IsError: true}
			}
			text = newText
			applied = append(applied, fmt.Sprintf("block[%d]: 替换 %d 处", i, appliedCount))
		}

		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			return ToolResult{Name: "FileEdit", Output: "写入失败: " + err.Error(), IsError: true}
		}
		return ToolResult{Name: "FileEdit",
			Output: fmt.Sprintf("已编辑 %s (%d 个 block 执行完毕):\n%s", path, len(blocks), strings.Join(applied, "\n"))}
	}

	// 单次替换模式(原有逻辑)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	replaceAll, _ := args["replace_all"].(bool)

	if oldText == "" {
		return ToolResult{Name: "FileEdit", Output: "缺少 old_text 或 blocks 参数", IsError: true}
	}

	resultText, appliedCount, errMsg := applySingleBlock(text, oldText, newText, replaceAll)
	if errMsg != "" {
		return ToolResult{Name: "FileEdit", Output: errMsg, IsError: true}
	}

	if err := os.WriteFile(path, []byte(resultText), 0644); err != nil {
		return ToolResult{Name: "FileEdit", Output: "写入失败: " + err.Error(), IsError: true}
	}
	return ToolResult{Name: "FileEdit",
		Output: fmt.Sprintf("已编辑 %s (替换 %d 处, %d→%d 字符)", path, appliedCount, len(oldText), len(newText))}
}

// applySingleBlock 对文本执行单个 SEARCH-REPLACE 操作。
// 返回: (新文本, 替换次数, 错误信息)。错误信息为空表示成功。
func applySingleBlock(text, oldText, newText string, replaceAll bool) (string, int, string) {
	count := strings.Count(text, oldText)
	if count == 0 {
		return text, 0, fmt.Sprintf("old_text 在文件中未找到(精确匹配失败)。请检查缩进和内容。\n文件前 200 字符: %s", truncate(text, 200))
	}

	if replaceAll {
		return strings.ReplaceAll(text, oldText, newText), count, ""
	}

	// 非 replace_all 且多处匹配：只替换第一处（3.2.2 conflict detection 已在描述中提示）
	if count > 1 {
		return strings.Replace(text, oldText, newText, 1), 1, ""
	}

	// 单处替换
	return strings.Replace(text, oldText, newText, 1), 1, ""
}

// D42 execGlob 文件模式匹配（3.3 升级版）。
// 3.3.1: 正确处理 `**` 递归通配符，支持 `src/**/*.go` 等模式。
func execGlob(args map[string]any, workspaceRoot, agentID, taskID string) ToolResult {
	pattern, _ := args["pattern"].(string)
	searchPath, _ := args["path"].(string)
	if searchPath == "" {
		searchPath = workspaceRoot
	}
	if pattern == "" {
		return ToolResult{Name: "Glob", Output: "缺少 pattern 参数", IsError: true}
	}

	headLimit := 100
	if hl, ok := args["head_limit"].(float64); ok && hl > 0 {
		headLimit = int(hl)
	}

	SetAgentStatus(agentID, StatusCallingTool, "Glob: "+pattern, taskID)

	// 3.3.2 自动排除的目录
	excludeDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"__pycache__": true, ".venv": true, "dist": true, "build": true,
	}

	// 3.3.3: 收集结果带 mtime
	type fileEntry struct {
		path  string
		mtime time.Time
	}
	var entries []fileEntry

	filepath.Walk(searchPath, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// 排除目录
		if info.IsDir() {
			base := filepath.Base(walkPath)
			if base != "." && strings.HasPrefix(base, ".") && base != ".." {
				return filepath.SkipDir // 跳过隐藏目录
			}
			if excludeDirs[base] {
				return filepath.SkipDir
			}
			return nil
		}
		// 只对文件做匹配
		relPath, _ := filepath.Rel(searchPath, walkPath)
		// 统一使用正斜杠比较(兼容 Windows 路径)
		normRel := filepath.ToSlash(relPath)
		normPattern := filepath.ToSlash(pattern)

		if matchGlobPattern(normPattern, normRel) {
			entries = append(entries, fileEntry{path: walkPath, mtime: info.ModTime()})
		}
		return nil
	})

	if len(entries) == 0 {
		return ToolResult{Name: "Glob", Output: "无匹配文件"}
	}

	// 按修改时间降序排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime.After(entries[j].mtime)
	})

	// 截断
	if len(entries) > headLimit {
		entries = entries[:headLimit]
	}

	paths := make([]string, len(entries))
	for i, e := range entries {
		relPath, _ := filepath.Rel(searchPath, e.path)
		paths[i] = relPath
	}
	return ToolResult{Name: "Glob",
		Output: fmt.Sprintf("找到 %d 个文件:\n%s", len(entries), strings.Join(paths, "\n"))}
}

// matchGlobPattern 正确处理 `**` 递归通配符的 glob 匹配。
// `**` 匹配零个或多个目录层级，`*` 匹配单层目录/文件名中的任意字符。
// 示例: `src/**/*.go` 匹配 `src/main.go`、`src/pkg/util.go` 等。
func matchGlobPattern(pattern, relPath string) bool {
	// 将 pattern 按 ** 分割为段
	segments := strings.Split(pattern, "**")
	if len(segments) == 1 {
		// 没有 **，直接用 filepath.Match
		matched, _ := filepath.Match(pattern, relPath)
		return matched
	}

	// 有 ** 的情况：逐段匹配
	// 第一段必须匹配路径开头
	// 最后一段必须匹配路径结尾
	// 中间的段必须按顺序出现在路径中

	path := relPath

	// 匹配第一段(前缀)
	first := strings.TrimPrefix(segments[0], "/")
	if first != "" {
		// 第一段可能是目录前缀(如 "src/")，检查路径是否以此开头
		if !strings.HasPrefix(path, first) {
			// 也尝试匹配路径的开头部分
			parts := strings.SplitN(path, "/", 2)
			matched, _ := filepath.Match(first, parts[0]+"/")
			if !matched {
				matched, _ = filepath.Match(first, parts[0])
			}
			if !matched {
				return false
			}
		}
		path = strings.TrimPrefix(path, first)
	}

	// 匹配最后一段(后缀)
	last := strings.TrimPrefix(segments[len(segments)-1], "/")
	if last != "" {
		// 从路径末尾匹配
		// last 可能是 "*.go" 这样的模式，需要匹配路径的最后一段
		pathParts := strings.Split(path, "/")
		// 从后往前找到第一个匹配的段
		found := false
		for i := len(pathParts) - 1; i >= 0; i-- {
			matched, _ := filepath.Match(last, pathParts[i])
			if matched {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// 中间段：按顺序检查是否能在路径中找到
	middleSegments := segments[1 : len(segments)-1]
	currentPos := 0
	for _, seg := range middleSegments {
		seg = strings.TrimPrefix(seg, "/")
		seg = strings.TrimSuffix(seg, "/")
		if seg == "" {
			continue
		}
		// 在剩余路径中查找匹配
		remaining := path[currentPos:]
		parts := strings.Split(remaining, "/")
		found := false
		for i, part := range parts {
			matched, _ := filepath.Match(seg, part)
			if matched {
				currentPos += len(strings.Join(parts[:i+1], "/"))
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// deprecatedGlob 旧版递归 glob（已被 execGlob 新实现替代，保留以防兼容性问题）。
func recursiveGlob(base, pattern string) []string {
	var results []string
	cleanPattern := strings.ReplaceAll(pattern, "**/", "")
	filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		matched, _ := filepath.Match(cleanPattern, filepath.Base(path))
		if matched {
			results = append(results, path)
		}
		return nil
	})
	return results
}

// D43 execGrep 内容搜索（3.4 升级版：二进制检测 + context + multiline）。
// 3.4.4 优先使用 ripgrep 后端(大仓库性能提升 10-100x)，不可用时降级到纯 Go 实现。
func execGrep(args map[string]any, workspaceRoot, agentID, taskID string) ToolResult {
	pattern, _ := args["pattern"].(string)
	searchPath, _ := args["path"].(string)
	include, _ := args["include"].(string)
	caseInsensitive, _ := args["case_insensitive"].(bool)
	multiline, _ := args["multiline"].(bool)

	// context 参数
	ctxBefore := 0
	ctxAfter := 0
	if v, ok := args["context"].(float64); ok {
		ctxBefore, ctxAfter = int(v), int(v)
	}
	if v, ok := args["context_before"].(float64); ok {
		ctxBefore = int(v)
	}
	if v, ok := args["context_after"].(float64); ok {
		ctxAfter = int(v)
	}

	headLimit := 100
	if hl, ok := args["head_limit"].(float64); ok && hl > 0 {
		headLimit = int(hl)
	}
	maxFiles := 500 // 最多扫描文件数

	if searchPath == "" {
		searchPath = workspaceRoot
	}
	if pattern == "" {
		return ToolResult{Name: "Grep", Output: "缺少 pattern 参数", IsError: true}
	}

	SetAgentStatus(agentID, StatusCallingTool, "Grep: "+pattern, taskID)

	// 3.4.4 尝试 ripgrep 后端(大仓库性能提升 10-100x)
	if result, ok := tryRipgrep(pattern, searchPath, include, caseInsensitive, ctxBefore, ctxAfter, headLimit); ok {
		return result
	}

	// 构建正则
	flags := ""
	if caseInsensitive {
		flags = "(?i)"
	}
	if multiline {
		flags += "(?s)" // . 匹配换行符
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return ToolResult{Name: "Grep", Output: "正则错误: " + err.Error(), IsError: true}
	}

	var output strings.Builder
	matchCount := 0
	filesScanned := 0
	excludeDirs := map[string]bool{".git": true, "node_modules": true, "vendor": true, "__pycache__": true}

	filepath.Walk(searchPath, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil || matchCount >= headLimit || filesScanned >= maxFiles {
			if matchCount >= headLimit {
				return filepath.SkipAll
			}
			return nil
		}
		if info.IsDir() {
			if excludeDirs[filepath.Base(walkPath)] {
				return filepath.SkipDir
			}
			// 跳过隐藏目录
			if walkPath != searchPath && strings.HasPrefix(filepath.Base(walkPath), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// 文件类型过滤
		if include != "" {
			matched, _ := filepath.Match(include, filepath.Base(walkPath))
			if !matched {
				return nil
			}
		}

		filesScanned++

		// 跳过过大的文件(>2MB)
		if info.Size() > 2*1024*1024 {
			return nil
		}

		data, err := os.ReadFile(walkPath)
		if err != nil {
			return nil
		}

		// 3.4.1 二进制检测：检查前 8KB 中是否有 null byte
		checkLen := 8192
		if len(data) < checkLen {
			checkLen = len(data)
		}
		if bytes.Contains(data[:checkLen], []byte{0}) {
			return nil // 跳过二进制文件
		}

		lines := strings.Split(string(data), "\n")
		relPath, _ := filepath.Rel(searchPath, walkPath)

		for lineNum, line := range lines {
			if matchCount >= headLimit {
				break
			}
			if re.MatchString(line) {
				matchCount++
				// 3.4.3 context_before（-B）
				if ctxBefore > 0 {
					start := lineNum - ctxBefore
					if start < 0 {
						start = 0
					}
					for b := start; b < lineNum; b++ {
						output.WriteString(fmt.Sprintf("  %s:%d- %s\n", relPath, b+1, truncate(lines[b], 200)))
					}
				}

				// 匹配行
				output.WriteString(fmt.Sprintf("%s:%d:", relPath, lineNum+1))
				if matchCount >= headLimit {
					output.WriteString(fmt.Sprintf(" %s\n", truncate(line, 200)))
					break
				}
				output.WriteString(fmt.Sprintf(" %s\n", truncate(line, 200)))

				// 3.4.3 context_after（-A）
				if ctxAfter > 0 {
					end := lineNum + ctxAfter + 1
					if end > len(lines) {
						end = len(lines)
					}
					for a := lineNum + 1; a < end; a++ {
						output.WriteString(fmt.Sprintf("  %s:%d+ %s\n", relPath, a+1, truncate(lines[a], 200)))
					}
				}
			}
		}
		return nil
	})

	if matchCount == 0 {
		return ToolResult{Name: "Grep", Output: "无匹配"}
	}
	suffix := ""
	if matchCount >= headLimit {
		suffix = fmt.Sprintf("\n...(结果截断到 %d 条,扫描了 %d 个文件)", headLimit, filesScanned)
	}
	return ToolResult{Name: "Grep", Output: fmt.Sprintf("找到 %d 处匹配:\n%s%s", matchCount, output.String(), suffix)}
}

// tryRipgrep 尝试使用 ripgrep(rg) 作为搜索后端。
// rg 可用时返回 (结果, true)，不可用时返回 (_, false) 由调用方降级到纯 Go 实现。
// 优势：大仓库(10k+ 文件)性能提升 10-100x，原生支持 .gitignore、二进制跳过等。
func tryRipgrep(pattern, searchPath, include string, caseInsensitive bool, ctxBefore, ctxAfter, headLimit int) (ToolResult, bool) {
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return ToolResult{}, false // rg 不可用，降级
	}

	args := []string{
		"--no-heading",       // 不显示文件名头
		"--line-number",      // 显示行号
		"--color=never",      // 无颜色
		"--max-count", fmt.Sprintf("%d", headLimit),
		"--hidden",           // 搜索隐藏文件(但会被 --glob 排除)
		"--glob=!.git/",      // 排除 .git
		"--glob=!node_modules/",
		"--glob=!vendor/",
		"--glob=!__pycache__/",
	}

	if caseInsensitive {
		args = append(args, "--ignore-case")
	}
	if ctxBefore > 0 {
		args = append(args, "--before-context", fmt.Sprintf("%d", ctxBefore))
	}
	if ctxAfter > 0 {
		args = append(args, "--after-context", fmt.Sprintf("%d", ctxAfter))
	}
	if include != "" {
		// rg 用 --glob 过滤文件类型，如 --glob="*.go"
		args = append(args, "--glob="+include)
	}

	// 正则表达式 + 搜索路径
	args = append(args, pattern, searchPath)

	cmd := exec.Command(rgPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// rg 返回非零(exit code 1 = 无匹配)，不算错误
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return ToolResult{Name: "Grep", Output: "无匹配"}, true
		}
		// 其他错误(如正则语法错)也返回结果，让调用方处理
		return ToolResult{}, false
	}

	out := string(output)
	if len(out) > 50000 {
		out = out[:50000] + "\n...(输出截断)"
	}

	// 统计匹配行数
	matchCount := strings.Count(out, "\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		matchCount++ // 最后一行没有换行符
	}

	return ToolResult{Name: "Grep",
		Output: fmt.Sprintf("找到 %d 处匹配 (ripgrep):\n%s", matchCount, out)}, true
}

// execToolSearch 3.6 搜索可用工具(内置 + MCP)。
// 搜索范围：1) 内置工具列表(BuiltinTools) 2) MCP server 注册的工具(延迟加载)。
func execToolSearch(args json.RawMessage) ToolResult {
	var params struct {
		Query  string `json:"query"`
		Server string `json:"server"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return ToolResult{Name: "tool_search", Output: "参数解析失败: " + err.Error(), IsError: true}
	}

	query := strings.ToLower(params.Query)
	var results []string

	// 1) 搜索内置工具
	for _, t := range BuiltinTools() {
		name := t.Function.Name
		desc := t.Function.Description
		if query != "" {
			if !strings.Contains(strings.ToLower(name), query) &&
				!strings.Contains(strings.ToLower(desc), query) {
				continue
			}
		}
		results = append(results, fmt.Sprintf("[builtin] %s: %s", name, desc))
	}

	// 2) 搜索 MCP 工具(复用 mcp_client.go 的 ExecuteToolSearch 逻辑)
	mcpResult := ExecuteToolSearch(args)
	if !mcpResult.IsError && mcpResult.Output != "无可用 MCP 工具" {
		// 从 MCP 结果中提取工具行(格式: [server_id] name: desc)
		lines := strings.Split(mcpResult.Output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "找到") {
				results = append(results, line)
			}
		}
	}

	if len(results) == 0 {
		if query != "" {
			return ToolResult{Name: "tool_search", Output: fmt.Sprintf("未找到匹配 '%s' 的工具", query)}
		}
		return ToolResult{Name: "tool_search", Output: "无可用工具"}
	}

	output := fmt.Sprintf("找到 %d 个工具:\n%s", len(results), strings.Join(results, "\n"))
	return ToolResult{Name: "tool_search", Output: output}
}

// isBuiltinTool 判断工具名是否是内置工具。
func isBuiltinTool(name string) bool {
	for _, t := range BuiltinTools() {
		if t.Function.Name == name {
			return true
		}
	}
	return false
}
