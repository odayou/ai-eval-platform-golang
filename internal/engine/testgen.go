package engine

// testgen.go — Test-Gen Agent: 针对 Diff 块自动生成单元测试 + 高并发流量 Mock(MRD 5.3)。
//
// MRD 原话:
//   "触发内部 Test-Gen Agent,针对 Diff 块自动生成单元测试和高并发流量 Mock 测试。
//    硬指标采集:Pass@1 成功率、编译时延、Lint 错误数、编译成功率、Edit Distance。"
//
// 功能:
//   1. 解析被测 Agent 生成的代码 → 提取函数签名列表
//   2. 对比原题期望(question.expected) → 识别 Diff 块(新增/修改的函数)
//   3. 调用 LLM 为每个 Diff 块生成:
//      a. 标准单元测试(正常输入 → 期望输出)
//      b. 边界测试(空指针/越界/零值)
//      c. 高并发 Mock 测试(goroutine 并发调用 → 测数据竞争/死锁)
//   4. 组装为完整的测试代码文件返回
//
// 设计:
//   不修改被测 Agent 生成的源码,只生成独立的测试文件。
//   测试代码由沙箱层(sandbox.go)与源码一同编译运行。
//   无 LLM Key 时使用 mock 测试模板兜底。

import (
	"fmt"
	"regexp"
	"strings"
)

// ── Diff 块分析 ──

// FunctionDef 描述源代码中一个函数/方法的定义。
type FunctionDef struct {
	Name       string `json:"name"`        // 函数名
	Signature  string `json:"signature"`   // 完整签名(如 "func Add(a, b int) int")
	Params     int    `json:"params"`      // 参数数量
	Returns    int    `json:"returns"`     // 返回值数量
	IsExported bool   `json:"is_exported"` // 是否导出(大写开头)
	Body       string `json:"body"`        // 函数体(前 200 字符)
}

// DiffBlock 描述一个需要自动生成测试的代码块。
type DiffBlock struct {
	Function   FunctionDef `json:"function"`    // 目标函数
	Reason     string      `json:"reason"`      // 为何需要测试(新功能/修改/边界)
	Difficulty string      `json:"difficulty"`  // 测试难度:unit / boundary / concurrency
}

// ── Test-Gen 引擎 ──

// GenerateTests 是 Test-Gen Agent 的主入口。
//
// 参数:
//   - answer: 被测 Agent 生成的代码
//   - prompt: 原题(用于理解上下文)
//   - expected: 期望输出(可空,用于对比 Diff)
//   - language: 编程语言(go/python/javascript)
//
// 返回:
//   - testCode: 生成的测试代码(空字符串表示无需生成)
//   - blocks: 识别到的 Diff 块列表(供日志和报告展示)
//
// 流程:
//   1. 解析 answer 提取所有函数签名(parseFunctions)
//   2. 对比 expected 识别 Diff 块(需要测试的目标)
//   3. 检查代码中是否已有 Test 函数(有则跳过,避免重复)
//   4. 调 LLM 为每个 Diff 块生成测试代码(或 mock 兜底)
//   5. 组装完整测试文件
func GenerateTests(answer, prompt, expected, language string) (testCode string, blocks []DiffBlock) {
	// 如果已包含 test 代码,跳过自动生成(避免重复)
	if hasExistingTests(answer, language) {
		NewSlog("testgen").Warn("被测代码已包含测试,跳过自动 Test-Gen")
		return "", nil
	}

	// 1. 解析函数
	funcs := parseFunctions(answer, language)
	if len(funcs) == 0 {
		NewSlog("testgen").Warn("未检测到可测试的函数,跳过")
		return "", nil
	}

	// 2. 识别 Diff 块(需要测试的目标)
	blocks = identifyDiffBlocks(funcs, expected, language)
	if len(blocks) == 0 {
		// 没有显式 Diff → 对所有导出函数生成基础单元测试
		for _, f := range funcs {
			if f.IsExported {
				blocks = append(blocks, DiffBlock{
					Function:   f,
					Reason:     "导出函数,需基础单元测试覆盖",
					Difficulty: "unit",
				})
			}
		}
	}
	// 限制 blocks 数量避免 token 爆炸
	if len(blocks) > 5 {
		blocks = blocks[:5]
	}

	if len(blocks) == 0 {
		NewSlog("testgen").Info("无需要生成测试的 Diff 块")
		return "", nil
	}

	// 3. 生成测试代码
	testCode = generateTestCode(blocks, answer, prompt, language)
	if testCode == "" {
		NewSlog("testgen").Info("测试代码生成为空,使用 mock 兜底")
		testCode = mockTestCode(blocks, language)
	}

	NewSlog("testgen").Info("完成: 个 Diff 块, 字节测试代码", "blocks_count", len(blocks), "testCode_count", len(testCode))
	return testCode, blocks
}

// ── 函数解析 ──

// parseFunctions 从源代码中提取函数/方法定义。
func parseFunctions(code, language string) []FunctionDef {
	switch language {
	case "go":
		return parseGoFunctions(code)
	case "python":
		return parsePythonFunctions(code)
	case "javascript":
		return parseJSFunctions(code)
	default:
		return nil
	}
}

// parseGoFunctions 解析 Go 源代码中的函数定义。
func parseGoFunctions(code string) []FunctionDef {
	var funcs []FunctionDef
	// 匹配 func 定义(支持方法接收器)
	re := regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s+)?(\w+)\s*\(([^)]*)\)\s*(?:\(?[^)]*\)?\s*)?\{`)
	matches := re.FindAllStringSubmatchIndex(code, -1)

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		name := code[match[2]:match[3]]
		paramsStr := code[match[4]:match[5]]

		// 获取函数体(截取前 200 字符)
		bodyStart := match[1]
		bodyEnd := bodyStart + 200
		if bodyEnd > len(code) {
			bodyEnd = len(code)
		}
		body := code[bodyStart:bodyEnd]

		params := 0
		if strings.TrimSpace(paramsStr) != "" {
			params = strings.Count(paramsStr, ",") + 1
		}

		isExported := name[0] >= 'A' && name[0] <= 'Z'

		funcs = append(funcs, FunctionDef{
			Name:       name,
			Signature:  code[match[0]:bodyStart] + "...",
			Params:     params,
			Returns:    strings.Count(code[match[0]:bodyStart], ")") - 1,
			IsExported: isExported,
			Body:       truncate(body, 200),
		})
	}

	return funcs
}

// parsePythonFunctions 解析 Python 函数定义。
func parsePythonFunctions(code string) []FunctionDef {
	var funcs []FunctionDef
	re := regexp.MustCompile(`(?m)^(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\):`)
	matches := re.FindAllStringSubmatch(code, -1)

	for _, m := range matches {
		name := m[1]
		paramsStr := m[2]
		params := 0
		if strings.TrimSpace(paramsStr) != "" && paramsStr != "self" {
			params = strings.Count(paramsStr, ",") + 1
			// 减去 self/cls
			if strings.Contains(paramsStr, "self") {
				params--
			}
			if strings.Contains(paramsStr, "cls") {
				params--
			}
			if params < 0 {
				params = 0
			}
		}
		isExported := !strings.HasPrefix(name, "_")
		funcs = append(funcs, FunctionDef{
			Name:       name,
			Signature:  m[0],
			Params:     params,
			Returns:    1,
			IsExported: isExported,
		})
	}
	return funcs
}

// parseJSFunctions 解析 JavaScript 函数定义。
func parseJSFunctions(code string) []FunctionDef {
	var funcs []FunctionDef
	re := regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(([^)]*)\)`)
	matches := re.FindAllStringSubmatch(code, -1)

	// 箭头函数: const fn = (...) => ...
	re2 := regexp.MustCompile(`(?m)^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?\(([^)]*)\)\s*=>`)
	matches2 := re2.FindAllStringSubmatch(code, -1)

	for _, m := range matches {
		name := m[1]
		paramsStr := m[2]
		params := 0
		if strings.TrimSpace(paramsStr) != "" {
			params = strings.Count(paramsStr, ",") + 1
		}
		isExported := !strings.HasPrefix(name, "_")
		funcs = append(funcs, FunctionDef{
			Name:       name,
			Signature:  m[0],
			Params:     params,
			Returns:    1,
			IsExported: isExported,
		})
	}
	for _, m := range matches2 {
		name := m[1]
		paramsStr := m[2]
		params := 0
		if strings.TrimSpace(paramsStr) != "" {
			params = strings.Count(paramsStr, ",") + 1
		}
		isExported := !strings.HasPrefix(name, "_")
		funcs = append(funcs, FunctionDef{
			Name:       name,
			Signature:  m[0],
			Params:     params,
			Returns:    1,
			IsExported: isExported,
		})
	}
	return funcs
}

// ── Diff 块识别 ──

// identifyDiffBlocks 对比代码与期望输出,识别需要测试的 Diff 块。
func identifyDiffBlocks(funcs []FunctionDef, expected, language string) []DiffBlock {
	var blocks []DiffBlock

	// 如果期望输出为空 → 对所有新函数生成单元测试
	if expected == "" {
		for _, f := range funcs {
			if f.IsExported {
				blocks = append(blocks, DiffBlock{
					Function:   f,
					Reason:     "新增导出函数",
					Difficulty: "unit",
				})
			}
		}
		return blocks
	}

	// 对比期望输出中的函数名,找差异
	expectedFuncs := parseFunctions(expected, language)
	expectedNames := make(map[string]bool)
	for _, f := range expectedFuncs {
		expectedNames[f.Name] = true
	}

	for _, f := range funcs {
		if !expectedNames[f.Name] {
			// 新函数 → 单元测试 + 并发测试(若含 channel/mutex)
			diffType := "unit"
			reason := "新增函数"
			if strings.Contains(f.Body, "chan") || strings.Contains(f.Body, "sync.") ||
				strings.Contains(f.Body, "Mutex") || strings.Contains(f.Body, "goroutine") {
				diffType = "concurrency"
				reason = "新增并发函数,需高并发 Mock 测试"
			} else if f.Params > 2 {
				diffType = "boundary"
				reason = "多参数函数,需边界测试覆盖"
			}
			blocks = append(blocks, DiffBlock{
				Function:   f,
				Reason:     reason,
				Difficulty: diffType,
			})
		}
	}

	return blocks
}

// hasExistingTests 检查代码中是否已包含测试函数。
func hasExistingTests(code, language string) bool {
	switch language {
	case "go":
		return strings.Contains(code, "func Test") || strings.Contains(code, "_test.go")
	case "python":
		return strings.Contains(code, "def test_") || strings.Contains(code, "class Test")
	case "javascript":
		return strings.Contains(code, "describe(") || strings.Contains(code, "it(") ||
			strings.Contains(code, "test(")
	}
	return false
}

// ── 测试代码生成 ──

// generateTestCode 调用 LLM 为 Diff 块生成测试代码。
func generateTestCode(blocks []DiffBlock, answer, prompt, language string) string {
	// 构建生成测试的 prompt
	p := buildTestGenPrompt(blocks, answer, prompt, language)

	// 调 LLM(考官模型:question_gen 角色,自动回退到 main)
	baseURL, apiKey, realName, apiFormat := LookupJudgeEndpoint("question_gen")

	if baseURL == "" || apiKey == "" {
		NewSlog("testgen").Info("无可用 LLM Key,使用 mock 测试模板")
		return mockTestCode(blocks, language)
	}

	result, err := callOpenAICompatible(baseURL, apiKey, apiFormat, realName, p)
	if err != nil {
		NewSlog("testgen").Error("LLM 调用失败(),回退 mock 测试", "err", err)
		return mockTestCode(blocks, language)
	}

	// 提取生成的测试代码
	testCode := extractTestCode(result.Text, language)
	return testCode
}

// buildTestGenPrompt 构建 Test-Gen Agent 的 prompt。
func buildTestGenPrompt(blocks []DiffBlock, answer, prompt, language string) string {
	var sb strings.Builder

	sb.WriteString("你是 Test-Gen Agent,负责为以下 AI 生成的代码编写测试。\n\n")
	sb.WriteString("## 原题\n")
	sb.WriteString(prompt)
	sb.WriteString("\n\n## 被测代码\n")
	sb.WriteString("```" + language + "\n")
	// 截断代码避免 token 爆炸
	codeSnippet := truncate(answer, 3000)
	sb.WriteString(codeSnippet)
	sb.WriteString("\n```\n\n")

	sb.WriteString("## 需要测试的函数\n")
	for i, b := range blocks {
		sb.WriteString(fmt.Sprintf("%d. `%s` (%s) — %s\n", i+1, b.Function.Signature, b.Difficulty, b.Reason))
	}
	sb.WriteString("\n")

	// 根据语言生成对应的测试框架代码
	switch language {
	case "go":
		sb.WriteString(`请为以上函数生成 Go 测试代码(使用 testing 包)。要求:
1. 标准单元测试:正常输入 → 验证期望输出
2. 边界测试:空值、零值、负数、超大值
3. 高并发测试(仅对含 goroutine/channel/mutex 的函数):使用 go test -race 风格,
   启动 10 个 goroutine 并发调用,检测数据竞争。
4. 所有测试函数以 TestXxx 命名
5. 使用 t.Run 子测试组织

仅输出完整的 .go 测试文件内容,不要额外解释。
`)
	case "python":
		sb.WriteString(`请为以上函数生成 Python 测试代码(使用 pytest)。要求:
1. 标准单元测试:正常输入 → 验证期望输出
2. 边界测试:空值、None、负数
3. 高并发测试(仅对含 threading/asyncio 的函数):使用 concurrent.futures 启动并发调用
4. 测试函数以 test_ 开头

仅输出完整的 .py 测试文件内容,不要额外解释。
`)
	case "javascript":
		sb.WriteString(`请为以上函数生成 JavaScript 测试代码(使用 Jest)。要求:
1. 标准单元测试:正常输入 → 验证期望输出
2. 边界测试:null、undefined、空值
3. 高并发测试(仅对含 Promise/async 的函数):使用 Promise.all 启动并发调用
4. 使用 describe/it 组织

仅输出完整的 .test.js 测试文件内容,不要额外解释。
`)
	}

	return sb.String()
}

// extractTestCode 从 LLM 返回中提取测试代码块。
func extractTestCode(text, language string) string {
	// 尝试提取 ``` 代码块
	codeBlockRe := regexp.MustCompile("```(?:" + language + ")?\n?([\\s\\S]*?)```")
	matches := codeBlockRe.FindStringSubmatch(text)
	if len(matches) >= 2 {
		code := strings.TrimSpace(matches[1])
		if len(code) > 50 {
			return code
		}
	}

	// 兜底:全文即为代码
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 100 {
		return trimmed
	}

	return ""
}

// ── Mock 测试模板 ──

// mockTestCode 当 LLM 不可用时,为 Diff 块生成 mock 测试代码。
func mockTestCode(blocks []DiffBlock, language string) string {
	switch language {
	case "go":
		return mockGoTestCode(blocks)
	case "python":
		return mockPythonTestCode(blocks)
	case "javascript":
		return mockJSTestCode(blocks)
	}
	return ""
}

// mockGoTestCode 生成 Go mock 测试代码。
func mockGoTestCode(blocks []DiffBlock) string {
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	sb.WriteString("import (\n\t\"testing\"\n")
	// 检查是否需要并发相关的 import
	needsSync := false
	for _, b := range blocks {
		if b.Difficulty == "concurrency" {
			needsSync = true
			break
		}
	}
	if needsSync {
		sb.WriteString("\t\"sync\"\n")
	}
	sb.WriteString(")\n\n")

	for _, b := range blocks {
		fn := b.Function

		// 基础单元测试
		sb.WriteString(fmt.Sprintf("// %s — %s\n", fn.Name, b.Reason))
		sb.WriteString(fmt.Sprintf("func Test%s_Unit(t *testing.T) {\n", fn.Name))
		sb.WriteString("\ttests := []struct {\n\t\tname string\n")
		sb.WriteString("\t\twant interface{}\n")
		sb.WriteString("\t}{\n")
		sb.WriteString("\t\t{name: \"basic\", want: nil},\n")
		sb.WriteString("\t}\n")
		sb.WriteString("\tfor _, tt := range tests {\n")
		sb.WriteString(fmt.Sprintf("\t\tt.Run(tt.name, func(t *testing.T) {\n"))
		sb.WriteString(fmt.Sprintf("\t\t\tgot := %s()\n", fn.Name))
		sb.WriteString("\t\t\t// TODO: 验证结果\n")
		sb.WriteString("\t\t\t_ = got\n")
		sb.WriteString("\t\t})\n")
		sb.WriteString("\t}\n")
		sb.WriteString("}\n\n")

		// 边界测试
		sb.WriteString(fmt.Sprintf("func Test%s_Boundary(t *testing.T) {\n", fn.Name))
		sb.WriteString("\tt.Run(\"empty input\", func(t *testing.T) {\n")
		sb.WriteString("\t\t// 测试零值/空值场景\n")
		sb.WriteString("\t\t// TODO: 补充边界断言\n")
		sb.WriteString("\t})\n")
		sb.WriteString("}\n\n")

		// 高并发测试(仅对并发函数)
		if b.Difficulty == "concurrency" {
			sb.WriteString(fmt.Sprintf("func Test%s_Concurrency(t *testing.T) {\n", fn.Name))
			sb.WriteString("\t// 高并发 Mock 测试:10 个 goroutine 并发调用\n")
			sb.WriteString("\tvar wg sync.WaitGroup\n")
			sb.WriteString("\tfor i := 0; i < 10; i++ {\n")
			sb.WriteString("\t\twg.Add(1)\n")
			sb.WriteString("\t\tgo func() {\n")
			sb.WriteString("\t\t\tdefer wg.Done()\n")
			sb.WriteString(fmt.Sprintf("\t\t\t_ = %s()\n", fn.Name))
			sb.WriteString("\t\t}()\n")
			sb.WriteString("\t}\n")
			sb.WriteString("\twg.Wait()\n")
			sb.WriteString("}\n\n")
		}
	}

	return sb.String()
}

// mockPythonTestCode 生成 Python mock 测试代码。
func mockPythonTestCode(blocks []DiffBlock) string {
	var sb strings.Builder
	sb.WriteString("import pytest\nimport concurrent.futures\n\n")

	for _, b := range blocks {
		fn := b.Function
		sb.WriteString(fmt.Sprintf("class Test%s:\n", fn.Name))

		// 单元测试
		sb.WriteString("    def test_unit(self):\n")
		sb.WriteString(fmt.Sprintf("        result = %s()\n", fn.Name))
		sb.WriteString("        # TODO: 验证结果\n")
		sb.WriteString("        assert result is not None\n\n")

		// 边界
		sb.WriteString("    def test_boundary(self):\n")
		sb.WriteString("        # TODO: 边界测试\n")
		sb.WriteString("        pass\n\n")

		// 并发
		if b.Difficulty == "concurrency" {
			sb.WriteString("    def test_concurrency(self):\n")
			sb.WriteString("        with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:\n")
			sb.WriteString(fmt.Sprintf("            futures = [executor.submit(%s) for _ in range(10)]\n", fn.Name))
			sb.WriteString("            results = [f.result() for f in futures]\n")
			sb.WriteString("            assert len(results) == 10\n\n")
		}
	}

	return sb.String()
}

// mockJSTestCode 生成 JavaScript mock 测试代码。
func mockJSTestCode(blocks []DiffBlock) string {
	var sb strings.Builder
	sb.WriteString("const assert = require('assert');\n\n")

	for _, b := range blocks {
		fn := b.Function
		sb.WriteString(fmt.Sprintf("describe('%s', function() {\n", fn.Name))

		// 单元测试
		sb.WriteString("    it('should handle basic case', function() {\n")
		sb.WriteString(fmt.Sprintf("        const result = %s();\n", fn.Name))
		sb.WriteString("        // TODO: 验证结果\n")
		sb.WriteString("        assert.ok(result !== undefined);\n")
		sb.WriteString("    });\n\n")

		// 边界
		sb.WriteString("    it('should handle boundary', function() {\n")
		sb.WriteString("        // TODO: 边界测试\n")
		sb.WriteString("    });\n\n")

		// 并发
		if b.Difficulty == "concurrency" {
			sb.WriteString("    it('should handle concurrent calls', async function() {\n")
			sb.WriteString(fmt.Sprintf("        const promises = Array(10).fill().map(() => %s());\n", fn.Name))
			sb.WriteString("        const results = await Promise.all(promises);\n")
			sb.WriteString("        assert.equal(results.length, 10);\n")
			sb.WriteString("    });\n")
		}

		sb.WriteString("});\n\n")
	}

	return sb.String()
}
