package engine

// sandbox.go — 动态沙箱压测层(MRD 5.3 / 3.1)。
//
// 双轨架构:
//   Phase 2 — Docker 容器池(优先,sandbox_pool.go)
//     隔离更强:网络隔离(--network none)、资源限制(CPU/内存)、
//     只读文件系统、无特权模式。
//     需安装 Docker 并预构建镜像,无 Docker 时自动回退到进程级。
//
//   Phase 1 — 进程级沙箱(回退,本文件)
//     在临时目录编译执行,不依赖 Docker。
//     支持 Go / Python / JavaScript 的编译+测试+Lint。
//
// 统一入口 RunSandbox:
//   1. 检测语言
//   2. 若容器池已启用(globalSandboxPool.Enabled) → 走 Docker 执行
//   3. Docker 不可用或失败 → 自动回退到进程级沙箱
//   4. 采集硬指标:编译时延、编译成功/失败、Lint 错误数、测试通过率
//
// Test-Gen 集成:
//   若传入了 testCode(由 testgen.go 生成),沙箱会将测试代码与源码
//   一同编译执行,实现"自动生成测试→沙箱验证"闭环。
//
// 产出 SandboxResult,供报告聚合(Pass@1、编译成功率等)。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// globalSandboxPool 是包级全局容器池实例,在 main.go 中通过 InitSandboxPool 初始化。
// engine.go 的 Run 函数调用 RunSandbox 时,RunSandbox 会优先走容器池,
// 容器池不可用时自动回退到进程级沙箱。
var globalSandboxPool *ContainerPool

// currentSandboxPermLevel 保存当前评测任务的被测 Agent 权限级别(MRD §5.6)。
// 由 engine.Run 在评测开始时通过 SetSandboxPermLevel 设置,供沙箱预检读取。
//
// 并发安全(2026-07-02 TD-1):已改为存储在 per-goroutine 的 EvalContext 中
// (见 evalctx.go),各 worker goroutine 互不干扰,不再有包级变量被覆盖的问题。

// SetSandboxPermLevel 设置当前任务的沙箱权限级别(engine.Run 调用)。
// 操作当前 goroutine 的 EvalContext。
func SetSandboxPermLevel(level string) {
	if level == "" {
		level = PermWorkspaceWrite
	}
	if ec := currentEvalContext(); ec != nil {
		ec.SandboxPerm = level
	}
}

// currentSandboxPermLevel 读取当前沙箱权限级别。
func currentSandboxPermLevel() string {
	return currentSandboxPermSafe()
}

// SandboxResult 是一次沙箱执行的结果。
type SandboxResult struct {
	Success      bool    `json:"success"`       // 编译是否成功
	CompileTime  int64   `json:"compile_time"`  // 编译耗时毫秒
	CompileError string  `json:"compile_error"` // 编译错误信息(失败时)
	TestPassed   int     `json:"test_passed"`   // 通过的测试数
	TestFailed   int     `json:"test_failed"`   // 失败的测试数
	TestTotal    int     `json:"test_total"`    // 总测试数
	LintErrors   int     `json:"lint_errors"`   // Lint 错误数
	Language     string  `json:"language"`       // 检测到的语言
	Score        float64 `json:"score"`          // 沙箱综合分 0-100
	IsMock       bool    `json:"is_mock"`       // 是否 mock(无法检测语言时)
}

// InitSandboxPool 初始化全局容器池。由 main.go 启动时调用。
// 不调用时全局池为 nil,RunSandbox 走纯进程级沙箱。
func InitSandboxPool(cfg ContainerPoolConfig) {
	globalSandboxPool = NewContainerPool(cfg)
}

// RunSandbox 在隔离环境中编译执行被测 Agent 生成的代码。
//
// 双轨策略:
//   1. 检测语言
//   2. 若 globalSandboxPool 已启用且语言支持 → 走 Docker 容器池
//   3. Docker 执行失败或池满 → 自动回退到进程级沙箱
//   4. 进程级沙箱:创建临时目录→写入文件→编译→测试→Lint
//
// 参数:
//   - answer: 被测 Agent 生成的代码
//   - testCode: 可选,由 testgen.go 生成的测试代码(传空字符串表示不注入)
//
// 返回 SandboxResult 供报告聚合。
func RunSandbox(answer string) SandboxResult {
	return RunSandboxWithTests(answer, "")
}

// RunSandboxWithTests 在沙箱中执行代码并运行注入的测试代码。
// testCode 由 Test-Gen Agent(testgen.go) 生成,针对 Diff 块自动创建。
//
// 优先级:Docker 容器池 → 进程级沙箱
// 回退保护:当从容器池的 Execute 回退时,通过 poolFallback 标记跳过二次池检。
//
// 代码提取:LLM 回答常带自然语言解释(如"以下是一个线程安全计数器:\n```go\n..."),
// 直接喂给编译器会报 "expected 'package', found 以下是"。
// 这里先提取 markdown 代码块,只编译纯代码。
func RunSandboxWithTests(answer, testCode string) SandboxResult {
	// 提取纯代码(去除自然语言解释),detectLanguage 内部也会提取但这里统一提取一次
	code := extractCodeBlock(answer)
	if code == "" {
		code = answer // 无代码块,用原始回答(可能是纯代码)
	}

	lang := detectLanguage(code)
	if lang == "unknown" {
		// 用原始 answer 再试一次(代码块提取可能漏了)
		lang = detectLanguage(answer)
		if lang == "unknown" {
			NewSlog("sandbox").Warn("无法检测语言,跳过沙箱执行")
			return SandboxResult{Score: 50, IsMock: true, Language: "unknown"}
		}
	}

	// 优先使用 Docker 容器池(传纯代码)
	if globalSandboxPool != nil && globalSandboxPool.config.Enabled {
		combined := code
		if testCode != "" {
			combined = code + "\n\n" + testCode
		}
		result, err := globalSandboxPool.Execute(combined, lang)
		if err == nil {
			return result
		}
		NewSlog("sandbox").Error("Docker 容器池执行失败(),回退到进程级沙箱", "err", err)
	}

	return runSandboxProcess(code, testCode, lang)
}

// runSandboxProcess 纯进程级沙箱执行(跳过 Docker 池检查,避免递归回退)。
func runSandboxProcess(answer, testCode, lang string) SandboxResult {
	// 权限门控(MRD §5.6):预检被测代码内容,拦截嵌入的极端自毁命令。
	// 沙箱在隔离 tmpDir 里运行,路径越界天然不可能,主要风险是代码内嵌的危险调用。
	gate := NewPermissionGate(currentSandboxPermLevel(), "")
	if allow, reason := gate.CheckSandboxCode(answer); !allow {
		NewSlog("sandbox").Warn("权限门控拒绝执行被测代码", "reason", reason)
		return SandboxResult{
			Success:      false,
			Language:     lang,
			CompileError: fmt.Sprintf("[权限门控拦截] %s", reason),
			Score:        0,
			IsMock:       true,
		}
	}

	tmpDir, err := os.MkdirTemp("", "eval-sandbox-*")
	if err != nil {
		NewSlog("sandbox").Error("创建临时目录失败", "err", err)
		return SandboxResult{Score: 0, Language: lang}
	}
	defer os.RemoveAll(tmpDir)

	start := time.Now()

	switch lang {
	case "go":
		return sandboxGo(tmpDir, answer, testCode, start)
	case "python":
		return sandboxPython(tmpDir, answer, testCode, start)
	default:
		return sandboxGeneric(tmpDir, answer, lang, testCode, start)
	}
}

// detectLanguage 从代码内容推断编程语言。
func detectLanguage(code string) string {
	// 先尝试提取 markdown 代码块(LLM 回答常带自然语言解释,纯文本检测会失败)
	if block := extractCodeBlock(code); block != "" {
		code = block
	}
	low := strings.ToLower(code)

	// Go 特征
	if regexp.MustCompile(`(?m)^package\s+\w+`).MatchString(code) ||
		regexp.MustCompile(`(?m)^func\s+\w+`).MatchString(code) ||
		strings.Contains(low, "import (") ||
		strings.Contains(low, "\"fmt\"") {
		return "go"
	}

	// Python 特征
	if regexp.MustCompile(`(?m)^def\s+\w+`).MatchString(code) ||
		regexp.MustCompile(`(?m)^class\s+\w+`).MatchString(code) ||
		strings.Contains(low, "import os") ||
		strings.Contains(low, "from typing") ||
		strings.Contains(low, "if __name__") {
		return "python"
	}

	// JavaScript/TypeScript 特征
	if regexp.MustCompile(`(?m)^(const|let|var)\s+\w+\s*=`).MatchString(code) ||
		strings.Contains(low, "module.exports") ||
		strings.Contains(low, "async function") {
		return "javascript"
	}

	// SQL 特征(SQL 注入修复等题)
	if regexp.MustCompile(`(?i)\b(select|insert|update|delete|create\s+table)\b`).MatchString(code) {
		if strings.Contains(low, "where") || strings.Contains(low, "from ") {
			return "sql"
		}
	}

	return "unknown"
}

// extractCodeBlock 从回答中提取第一个 markdown 代码块内容(去除自然语言)。
// 匹配 ```go / ```python / ```js / ``` 等围栏。
func extractCodeBlock(s string) string {
	re := regexp.MustCompile("(?s)```(?:\\w+)?\\s*\\n(.*?)```")
	m := re.FindStringSubmatch(s)
	if len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// completeGoCode 补全 Go 代码片段,使其可编译。
//   - 缺 package 声明 → 补 "package main"
//   - 有 main 函数但缺 package → 补 "package main"
//   - 代码里用了 fmt/sync 等但缺 import → 尝试自动补(基于常见用法)
func completeGoCode(code string) string {
	// 已有 package 声明,直接返回
	if regexp.MustCompile(`(?m)^package\s+\w+`).MatchString(code) {
		return code
	}

	// 检测需要的 import(基于代码内容推断)
	var imports []string
	low := strings.ToLower(code)
	if strings.Contains(low, "fmt.") {
		imports = append(imports, "\"fmt\"")
	}
	if strings.Contains(low, "sync.") {
		imports = append(imports, "\"sync\"")
	}
	if strings.Contains(low, "time.") {
		imports = append(imports, "\"time\"")
	}
	if strings.Contains(low, "net/http") {
		imports = append(imports, "\"net/http\"")
	}
	if strings.Contains(low, "errors.") {
		imports = append(imports, "\"errors\"")
	}
	if strings.Contains(low, "strconv.") {
		imports = append(imports, "\"strconv\"")
	}
	if strings.Contains(low, "strings.") {
		imports = append(imports, "\"strings\"")
	}
	if strings.Contains(low, "context.") {
		imports = append(imports, "\"context\"")
	}
	if strings.Contains(low, "os.") {
		imports = append(imports, "\"os\"")
	}

	// 拼接:package main + import 块 + 原始代码
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	if len(imports) > 0 {
		sb.WriteString("import (\n")
		for _, imp := range imports {
			sb.WriteString("\t" + imp + "\n")
		}
		sb.WriteString(")\n\n")
	}
	sb.WriteString(code)

	// 如果代码里没有 main 函数(只是类型/函数定义),补一个空 main 让它可编译
	if !regexp.MustCompile(`(?m)^func\s+main\s*\(`).MatchString(code) {
		sb.WriteString("\n\nfunc main() {}\n")
	}

	return sb.String()
}

// sandboxGo 在临时目录里跑 Go 编译 + 测试(含扩展测试)。
func sandboxGo(tmpDir, code, testCode string, start time.Time) SandboxResult {
	result := SandboxResult{Language: "go"}

	// 补全 Go 代码:LLM 回答的代码片段可能缺少 package 声明或 import,
	// 直接编译会报 "expected 'package'"。这里做最小补全。
	code = completeGoCode(code)

	// 写入 main.go
	mainFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(mainFile, []byte(code), 0644); err != nil {
		result.CompileError = fmt.Sprintf("写入文件失败: %v", err)
		return result
	}

	// 如果有 testCode,写入 main_test.go(Test-Gen Agent 自动生成的测试)
	hasTest := false
	if testCode != "" {
		testFile := filepath.Join(tmpDir, "main_test.go")
		if err := os.WriteFile(testFile, []byte(testCode), 0644); err == nil {
			hasTest = true
			NewSlog("sandbox").Info("Test-Gen Agent 已注入测试代码( 字节)", "testCode_count", len(testCode))
		}
	}

		// 初始化 go module(必须在 tmpDir 中执行,避免污染源目录)
		initCmd := exec.Command("go", "mod", "init", "sandbox")
		initCmd.Dir = tmpDir
		initCmd.Run()

	// 尝试编译
	compileStart := time.Now()
	buildCmd := exec.Command("go", "build", "-o", "sandbox_bin", ".")
	buildCmd.Dir = tmpDir
	buildOut, buildErr := buildCmd.CombinedOutput()
	result.CompileTime = time.Since(compileStart).Milliseconds()

	if buildErr != nil {
		result.Success = false
		result.CompileError = truncate(string(buildOut), 500)
		result.Score = scoreFromCompile(false, 0, 0)
		return result
	}
	result.Success = true

	// 检查代码中是否自带 Test 函数(兼容原有逻辑)
	if !hasTest && strings.Contains(code, "func Test") {
		testFile := filepath.Join(tmpDir, "main_test.go")
		os.WriteFile(testFile, []byte(code), 0644)
		hasTest = true
	}

	if hasTest {
		testStart := time.Now()
		testCmd := exec.Command("go", "test", "-v", "-count=1", "./...")
		testCmd.Dir = tmpDir
		testOut, _ := testCmd.CombinedOutput()
		_ = time.Since(testStart)

		result.TestPassed, result.TestFailed = parseGoTestOutput(string(testOut))
		result.TestTotal = result.TestPassed + result.TestFailed
	}

	// Lint 检查
	result.LintErrors = countLintIssues(code)

	result.Score = scoreFromCompile(true, result.TestPassed, result.TestFailed)
	return result
}

// sandboxPython 在临时目录里跑 Python 编译检查 + 测试(含扩展测试)。
func sandboxPython(tmpDir, code, testCode string, start time.Time) SandboxResult {
	result := SandboxResult{Language: "python"}

	// 写入 main.py
	mainFile := filepath.Join(tmpDir, "main.py")
	if err := os.WriteFile(mainFile, []byte(code), 0644); err != nil {
		result.CompileError = fmt.Sprintf("写入文件失败: %v", err)
		return result
	}

	// 如果有 testCode,写入 test_main.py(Test-Gen Agent 自动生成的测试)
	hasTest := false
	if testCode != "" {
		testFile := filepath.Join(tmpDir, "test_main.py")
		if err := os.WriteFile(testFile, []byte(testCode), 0644); err == nil {
			hasTest = true
			NewSlog("sandbox").Info("Test-Gen Agent 已注入 Python 测试( 字节)", "testCode_count", len(testCode))
		}
	}

	// 语法检查(python -m py_compile)
	compileStart := time.Now()
	buildCmd := exec.Command("python", "-m", "py_compile", mainFile)
	buildCmd.Dir = tmpDir
	buildOut, buildErr := buildCmd.CombinedOutput()
	result.CompileTime = time.Since(compileStart).Milliseconds()

	if buildErr != nil {
		result.Success = false
		result.CompileError = truncate(string(buildOut), 500)
		result.Score = scoreFromCompile(false, 0, 0)
		return result
	}
	result.Success = true

	// 检查代码中是否自带测试
	if !hasTest && (strings.Contains(code, "def test_") || strings.Contains(code, "class Test")) {
		testFile := filepath.Join(tmpDir, "test_main.py")
		os.WriteFile(testFile, []byte(code), 0644)
		hasTest = true
	}

	if hasTest {
		testCmd := exec.Command("python", "-m", "pytest", filepath.Join(tmpDir, "test_main.py"), "-v", "--tb=short")
		testCmd.Dir = tmpDir
		testOut, _ := testCmd.CombinedOutput()
		result.TestPassed, result.TestFailed = parsePytestOutput(string(testOut))
		result.TestTotal = result.TestPassed + result.TestFailed
	}

	result.LintErrors = countLintIssues(code)
	result.Score = scoreFromCompile(true, result.TestPassed, result.TestFailed)
	return result
}

// sandboxGeneric 通用沙箱(JavaScript 等,只做语法检查)。
func sandboxGeneric(tmpDir, code, lang, testCode string, start time.Time) SandboxResult {
	result := SandboxResult{Language: lang}

	ext := map[string]string{"javascript": ".js"}[lang]
	if ext == "" {
		ext = ".txt"
	}

	mainFile := filepath.Join(tmpDir, "main"+ext)
	os.WriteFile(mainFile, []byte(code), 0644)

	// 如果有 testCode 且是 JavaScript,写入测试文件
	if testCode != "" && lang == "javascript" {
		testFile := filepath.Join(tmpDir, "main.test"+ext)
		os.WriteFile(testFile, []byte(testCode), 0644)
		NewSlog("sandbox").Info("Test-Gen Agent 已注入 JS 测试( 字节)", "testCode_count", len(testCode))
	}

	// Node.js 语法检查
	if lang == "javascript" {
		compileStart := time.Now()
		cmd := exec.Command("node", "--check", mainFile)
		cmd.Dir = tmpDir
		_, err := cmd.CombinedOutput()
		result.CompileTime = time.Since(compileStart).Milliseconds()
		result.Success = err == nil
		if err != nil {
			result.CompileError = "语法错误"
		}
	} else {
		result.Success = true // 无法检查,默认成功
	}

	result.LintErrors = countLintIssues(code)
	result.Score = scoreFromCompile(result.Success, 0, 0)
	return result
}

// scoreFromCompile 根据编译结果和测试通过率计算沙箱分。
func scoreFromCompile(compileOK bool, testPassed, testFailed int) float64 {
	if !compileOK {
		return 20 // 编译失败=低分
	}
	score := 70.0 // 编译成功=基础分
	if testPassed+testFailed > 0 {
		passRate := float64(testPassed) / float64(testPassed+testFailed)
		score = 50 + passRate*50 // 测试通过率映射到 50-100
	}
	return score
}

// parseGoTestOutput 解析 go test -v 的输出,统计通过/失败数。
func parseGoTestOutput(output string) (passed, failed int) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- PASS:") {
			passed++
		} else if strings.HasPrefix(line, "--- FAIL:") {
			failed++
		}
	}
	return
}

// parsePytestOutput 解析 pytest 的输出,统计通过/失败数。
func parsePytestOutput(output string) (passed, failed int) {
	// pytest 输出: "X passed, Y failed"
	passRe := regexp.MustCompile(`(\d+)\s+passed`)
	failRe := regexp.MustCompile(`(\d+)\s+failed`)

	if m := passRe.FindStringSubmatch(output); len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &passed)
	}
	if m := failRe.FindStringSubmatch(output); len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &failed)
	}
	return
}

// countLintIssues 简化的 Lint 检查(基于关键词)。
func countLintIssues(code string) int {
	issues := 0
	low := strings.ToLower(code)

	// 硬编码密钥
	if regexp.MustCompile(`(sk-[a-z0-9]{10,}|api[_-]?key\s*[=:]\s*["'][^"']{8,})`).MatchString(low) {
		issues++
	}
	// fmt.Sprintf 拼接 SQL
	if regexp.MustCompile(`sprintf\s*\([^)]*(select|insert|update|delete)`).MatchString(low) {
		issues++
	}
	// 未处理错误(Go: err != nil 检查)
	if strings.Contains(low, "err") && !strings.Contains(low, "err !=") && !strings.Contains(low, "err ==") {
		issues++
	}

	return issues
}

// truncate 截断字符串。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
