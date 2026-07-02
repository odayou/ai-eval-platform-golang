package engine

// permission_gate.go — 三级权限门控(MRD §5.6,借鉴 claw-code permissions.py)。
//
// claw-code 的设计(src/permissions.py):
//   class ToolPermissionContext:
//       deny_names: frozenset[str]        # 禁止的工具名
//       deny_prefixes: tuple[str, ...]    # 禁止的工具名前缀
//       workspace_scope: WorkspacePathScope  # 工作区路径范围
//       def blocks(tool_name) -> bool              # 黑名单拦截
//       def validate_payload_scope(tool, payload)  # 路径越界检测
//
// 本文件把上述思想迁移为 Go 的三级权限模型,在执行被测 Agent 的代码前做拦截:
//
//   权限级别            允许的操作                            拦截的操作
//   ─────────────────────────────────────────────────────────────────────
//   read-only          只读类命令(ls/cat/grep/git status 等) 任何写操作/网络/危险命令
//   workspace-write    工作区内读写、构建、跑测试              越出工作区路径 / 危险命令 / 外网下载执行
//   full               全部放行(仅审计)                       仅拦截极端自毁命令(rm -rf / 等)
//
// 接入点:
//   - terminal.go:executeCommand 执行 shell 前
//   - sandbox.go 进程级执行前
// 拒绝时:写 permissions 表(decision=denied) + 返回 error,被测代码不执行。

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// 权限级别常量。
const (
	PermReadOnly       = "read-only"       // 只读:只能看不能改
	PermWorkspaceWrite = "workspace-write" // 工作区写:在工作区内读写/构建/测试
	PermFull           = "full"            // 完全放行(仅审计)
)

// PermissionGate 权限门控器。
//
// 字段映射 claw-code 的 ToolPermissionContext:
//   - WorkspaceRoot ↔ workspace_scope(路径越界检测基准)
//   - 危险命令黑名单 ↔ deny_names / deny_prefixes
type PermissionGate struct {
	Level         string // read-only | workspace-write | full
	WorkspaceRoot string // 工作区根目录(workspace-write 级别做路径越界检测)
}

// NewPermissionGate 按 Agent 的权限级别构造门控器。
func NewPermissionGate(level, workspaceRoot string) *PermissionGate {
	if level == "" {
		level = PermWorkspaceWrite
	}
	return &PermissionGate{Level: level, WorkspaceRoot: workspaceRoot}
}

// CheckCommand 在执行一条 shell 命令前做权限检查。
// 返回 (allow, reason):allow=false 时 reason 说明被拦截原因。
func (g *PermissionGate) CheckCommand(cmd string) (allow bool, reason string) {
	if cmd == "" {
		return true, ""
	}
	low := strings.ToLower(strings.TrimSpace(cmd))

	// 第一道:极端自毁命令(任何级别都拦,包括 full)。这是底线。
	if blk, r := matchDangerous(low); blk {
		return false, r
	}

	switch g.Level {
	case PermFull:
		// full:只拦极端命令(上面已查),其余放行
		return true, ""

	case PermReadOnly:
		// read-only:禁止任何重定向写(> / >> / tee 写文件)
		if hasWriteRedirect(low) {
			return false, fmt.Sprintf("read-only 级别禁止重定向写文件: %s", truncateCmd(low))
		}
		// read-only:必须是白名单内的只读命令
		if isReadOnlyCommand(low) {
			return true, ""
		}
		return false, fmt.Sprintf("read-only 级别禁止执行写/网络类命令: %s", truncateCmd(low))

	case PermWorkspaceWrite:
		// workspace-write:先过 execpolicy 声明式规则(skill #20),再过原有检查
		policy := DefaultExecPolicy()
		switch policy.Evaluate(low) {
		case "deny":
			return false, fmt.Sprintf("execpolicy 拒绝(声明式规则): %s", truncateCmd(low))
		case "ask":
			// ask 在无交互场景下默认放行(记录审计),有交互时可弹出确认
			// 这里不拦截,让 Guardian(LLM 审批)处理
		}
		// 拦外网下载执行 + 路径越界
		if blk, r := matchNetworkExec(low); blk {
			return false, r
		}
		if g.WorkspaceRoot != "" {
			if blk, r := g.checkPathEscape(cmd); blk {
				return false, r
			}
		}
		return true, ""

	default:
		// 未知级别按最严处理
		return false, fmt.Sprintf("未知权限级别 %q,默认拒绝", g.Level)
	}
}

// RecordDecision 把一次权限决策落库(审计)。
// decision: allowed | denied
func (g *PermissionGate) RecordDecision(taskID, tool, args, decision, reason string) {
	if store.DB == nil {
		return
	}
	store.DB.Exec(`INSERT INTO permissions (id, task_id, tool, args, level, decision, reason, chaos_injected, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP)`,
		uuid.New().String(), taskID, tool, truncateCmd(args), g.Level, decision, reason)
}

// ── 危险命令检测 ──

// matchDangerous 匹配极端自毁命令(任何级别都拦)。
// 返回 (true, 原因) 表示命中。
func matchDangerous(low string) (bool, string) {
	for _, pat := range extremeDangerPatterns {
		if pat.regex.MatchString(low) {
			return true, fmt.Sprintf("命中极端危险命令(%s),已拦截保护环境: %s", pat.desc, truncateCmd(low))
		}
	}
	return false, ""
}

// extremeDangerPattern 一条极端危险命令规则。
type extremeDangerPattern struct {
	regex *regexp.Regexp
	desc  string
}

// extremeDangerPatterns 极端危险命令清单。
// 这些命令即便 full 级别也拦——它们只会摧毁环境,没有正当评测理由。
var extremeDangerPatterns = []extremeDangerPattern{
	// rm -rf / 根目录递归删除(允许 rm -rf ./xxx 工作区内删除)
	{regexp.MustCompile(`\brm\s+(-[a-z]*r[a-z]*f?|--recursive[^|]*--force|-[a-z]*f[a-z]*r?)\s+/(?:\s|$)`), "递归删除根目录"},
	{regexp.MustCompile(`\brm\s+-[a-z]*r[a-z]*\s+-[a-z]*f\s+/(?:\s|$)`), "递归删除根目录"},
	// fork 炸弹 :(){:|:&};:
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{.*:.*\|.*:.*&.*\}\s*;`), "fork 炸弹"},
	// mkfs 格式化磁盘
	{regexp.MustCompile(`\bmkfs(?:\.\w+)?\s+/dev/`), "格式化磁盘设备"},
	// dd 写裸设备
	{regexp.MustCompile(`\bdd\s+.*\bof=/dev/(?:sd|nvme|hd|vd|disk)`), "dd 写裸磁盘设备"},
	// 关机/重启
	{regexp.MustCompile(`\b(shutdown|poweroff|halt|reboot)\b`), "关机/重启"},
	// 覆写重要系统文件
	{regexp.MustCompile(`>\s*/dev/sda`), "覆写磁盘设备"},
}

// matchNetworkExec 匹配"从外网下载并执行"(workspace-write 级别拦)。
// 典型:curl ... | sh / wget ... | bash / curl -o /tmp/x && ./x
func matchNetworkExec(low string) (bool, string) {
	for _, pat := range networkExecPatterns {
		if pat.regex.MatchString(low) {
			return true, fmt.Sprintf("workspace-write 级别禁止外网下载执行(%s): %s", pat.desc, truncateCmd(low))
		}
	}
	return false, ""
}

type networkExecPattern struct {
	regex *regexp.Regexp
	desc  string
}

var networkExecPatterns = []networkExecPattern{
	{regexp.MustCompile(`\b(curl|wget)\b.*\|\s*(sh|bash|zsh|python|perl|ruby)\b`), "管道直接执行网络下载内容"},
	{regexp.MustCompile(`\b(curl|wget)\b.*\|\s*sudo\s+(sh|bash)`), "管道 sudo 执行网络下载内容"},
}

// ── read-only 白名单 ──

// isReadOnlyCommand 判断命令是否属于"只读"类。
// 拆出第一个命令词(忽略 env 前缀),查只读白名单。
func isReadOnlyCommand(low string) bool {
	// 去掉环境变量前缀(如 FOO=bar cmd),取真正的命令
	cmd := stripEnvPrefix(low)
	// 取命令首词(支持带路径,如 /usr/bin/ls → ls)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	first := filepath.Base(fields[0])

	// 直接命中只读命令
	if readOnlyCommands[first] {
		return true
	}
	// git 只读子命令
	if first == "git" && len(fields) > 1 {
		if gitReadOnlySubcmds[fields[1]] {
			return true
		}
	}
	// go 只读子命令
	if first == "go" && len(fields) > 1 {
		if goReadOnlySubcmds[fields[1]] {
			return true
		}
	}
	return false
}

// hasWriteRedirect 检测命令是否含重定向写文件(> / >> 且非 2>&1 这类描述符合并)。
func hasWriteRedirect(low string) bool {
	// 匹配 > 或 >> 后跟文件(排除 >&  >&-  2>&1 这类描述符合并)
	re := regexp.MustCompile(`(^|[^&])>>?\s+[^\s&|]`)
	return re.MatchString(low)
}

// stripEnvPrefix 去掉命令前的 KEY=VAL 环境变量前缀,返回真正的命令部分。
func stripEnvPrefix(low string) string {
	envVar := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S+\s+`)
	for envVar.MatchString(low) {
		low = envVar.ReplaceAllString(low, "")
	}
	return low
}

// readOnlyCommands 只读类命令白名单(不修改文件系统/不发起出站连接)。
var readOnlyCommands = map[string]bool{
	// 查看类
	"ls": true, "ll": true, "dir": true, "cat": true, "head": true, "tail": true,
	"less": true, "more": true, "wc": true, "file": true, "stat": true,
	// 搜索类
	"grep": true, "rg": true, "find": true, "ag": true, "ack": true,
	// 树形/比较
	"tree": true, "diff": true,
	// 信息类
	"echo": true, "printf": true, "pwd": true, "whoami": true, "id": true,
	"uname": true, "hostname": true, "date": true, "uptime": true,
	"env": true, "printenv": true,
	// 进程/资源查看(只读)
	"ps": true, "top": true, "free": true, "df": true, "du": true,
}

// gitReadOnlySubcmds git 的只读子命令。
var gitReadOnlySubcmds = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "branch": true,
	"blame": true, "remote": true, "ls-files": true, "ls-tree": true,
	"rev-parse": true, "describe": true, "stash": true, // stash list 等只读
}

// goReadOnlySubcmds go 的只读子命令。
var goReadOnlySubcmds = map[string]bool{
	"version": true, "env": true, "list": true, "vet": true, "fmt": true,
	"doc": true, "test": true, // test 本质只读(不写源码)
}

// ── 路径越界检测(借鉴 claw-code WorkspacePathScope) ──

// checkPathEscape 检测命令是否引用了工作区之外的绝对路径。
// workspace-write 级别:允许在工作区内任意操作,但不准越界改 /etc、/usr、用户主目录等。
// 返回 (true, 原因) 表示越界被拦。
func (g *PermissionGate) checkPathEscape(cmd string) (bool, string) {
	// 提取命令中所有绝对路径(以 / 开头)。
	// 兼容三种位置:命令首部(/usr/bin/go ...)、参数后(cat /etc/x)、赋值后(KEY=/path)。
	absPathRe := regexp.MustCompile(`(?:^|\s|=|["'])((?:/[A-Za-z0-9._\-]+)+)`)
	matches := absPathRe.FindAllStringSubmatch(cmd, -1)

	root, _ := filepath.Abs(g.WorkspaceRoot)
	for _, m := range matches {
		p := filepath.Clean(m[1])
		// 允许的"工作区外但安全"的路径
		if isSafeSystemPath(p) {
			continue
		}
		// 检查是否在工作区内
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return true, fmt.Sprintf("workspace-write 级别禁止越界操作工作区外路径: %s (工作区: %s)", p, root)
		}
	}
	return false, ""
}

// isSafeSystemPath 判断一个绝对路径是否"即便在工作区外也安全可访问"。
// 这些是命令正常工作需要的系统路径(库、临时目录),不拦。
// 注意:用 filepath.Clean 后在 Windows 下分隔符是 '\',统一归一为 '/' 再比较。
func isSafeSystemPath(p string) bool {
	// 归一化分隔符(Windows 下 filepath.Clean 会把 / 转成 \)
	norm := strings.ReplaceAll(p, "\\", "/")
	safePrefixes := []string{
		"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr",
		"/tmp/", "/var/tmp/",
		"/usr/bin/", "/usr/local/bin/", "/usr/lib/", "/usr/local/lib/",
		"/bin/", "/sbin/", "/opt/", // 可执行程序目录
		"/proc/", "/sys/", // 内核信息(只读)
	}
	for _, sp := range safePrefixes {
		if strings.HasPrefix(norm, sp) || norm == strings.TrimSuffix(sp, "/") {
			return true
		}
	}
	return false
}

// truncateCmd 截断命令字符串(日志/审计用,避免超长)。
func truncateCmd(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(截断)"
}

// CheckSandboxCode 预检被测 Agent 生成的代码,拦截嵌入在代码里的极端自毁命令。
//
// 沙箱执行的是被测代码(编译/解释),与终端轨道执行用户配置的 StartCmd 性质不同。
// 真正的风险是:被测代码里嵌入了 os.system("rm -rf /") / exec.Command("rm","-rf","/") 等。
// 本函数扫描代码文本,匹配跨语言的"调用 shell 删根目录/fork 炸弹/格式化磁盘"模式。
//
// 拦截策略(与 CheckCommand 一致):
//   - 极端自毁命令:任何级别都拦(底线)
//   - read-only 级别:额外拦代码里的写文件/网络请求/子进程调用
// 返回 (allow, reason):allow=false 时沙箱应跳过执行并标记。
func (g *PermissionGate) CheckSandboxCode(code string) (allow bool, reason string) {
	if code == "" {
		return true, ""
	}
	low := strings.ToLower(code)

	// 极端自毁:跨语言模式(Go/Python/Node 的 os/exec/subprocess 调 rm -rf /)
	for _, pat := range sandboxExtremePatterns {
		if pat.regex.MatchString(low) {
			return false, fmt.Sprintf("被测代码命中极端危险模式(%s),沙箱拒绝执行: %s", pat.desc, truncateCmd(matchSnippet(low, pat.regex)))
		}
	}

	// read-only 级别:额外拦写文件/网络/子进程
	if g.Level == PermReadOnly {
		if blk, r := matchReadOnlyViolationInCode(low); blk {
			return false, r
		}
	}
	return true, ""
}

// matchSnippet 提取匹配点的上下文片段(供日志定位)。
func matchSnippet(low string, re *regexp.Regexp) string {
	loc := re.FindStringIndex(low)
	if loc == nil {
		return ""
	}
	start := loc[0] - 30
	if start < 0 {
		start = 0
	}
	end := loc[1] + 30
	if end > len(low) {
		end = len(low)
	}
	return low[start:end]
}

// sandboxExtremePatterns 沙箱里跨语言的极端自毁命令模式。
var sandboxExtremePatterns = []extremeDangerPattern{
	// rm -rf / (跨语言:os.system/subprocess/exec.Command/popen)——字符串拼接形式
	{regexp.MustCompile(`(os\.system|subprocess\.(?:run|call|popen|Popen)|exec\.command|child_process\.exec|popen)\s*\([^)]*rm\s+-[a-z]*r[a-z]*f?\s+/(?:\s|['"])`), "调用 shell 删根目录"},
	// 列表形式:["rm","-rf","/"] 或 ("rm","-rf","/")——参数分散在引号里
	{regexp.MustCompile(`["'\[(]\s*["']rm["']\s*,\s*["']-[a-z]*r[a-z]*f?["']\s*,\s*["']/(?:["']|\s)`), "列表参数删根目录"},
	// 直接 rm -rf / 字符串(拼接到 shell 的场景)
	{regexp.MustCompile(`["']rm\s+-[a-z]*r[a-z]*f?\s+/(\s|['"])`), "删根目录命令字符串"},
	// fork 炸弹
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{.*:.*\|.*:.*&.*\}`), "fork 炸弹"},
	// 格式化磁盘
	{regexp.MustCompile(`mkfs(?:\.\w+)?\s+/dev/`), "格式化磁盘"},
	// dd 写裸磁盘设备
	{regexp.MustCompile(`dd\s+.*\bof=/dev/(?:sd|nvme|hd|vd|disk)`), "dd 覆写磁盘"},
}

// matchReadOnlyViolationInCode 在 read-only 级别下,检测代码里的写/网络/子进程操作。
func matchReadOnlyViolationInCode(low string) (bool, string) {
	violations := []struct {
		re   *regexp.Regexp
		desc string
	}{
		{regexp.MustCompile(`os\.(?:create|write|remove|rename|mkdir|makedirs|chmod|unlink)\b`), "文件写操作"},
		{regexp.MustCompile(`(open|io\.open)\s*\([^)]*['"][wa+x]`), "文件写/追加打开"},
		{regexp.MustCompile(`(exec\.command|child_process\.exec|subprocess\.(?:run|call|popen)|os\.system)\s*\(`), "子进程调用"},
		{regexp.MustCompile(`(http\.(?:get|post|do|newrequest)|requests\.(?:get|post|put|delete|patch)|fetch\(|axios\.(?:get|post)|urllib)\s*\(`), "网络请求"},
	}
	for _, v := range violations {
		if v.re.MatchString(low) {
			return true, fmt.Sprintf("read-only 级别禁止被测代码执行%s", v.desc)
		}
	}
	return false, ""
}
