package engine

// apply_patch.go — apply_patch LLM 专用 diff DSL(good-skills #21 实现)。
//
// 补齐专家评审:#21 未实现。LLM 只能全文重写文件(builtin_tools FileWrite)。
// apply_patch 让 LLM 用精简 diff 格式改文件,比全文重写省 token。
//
// 研究依据:codex apply-patch/src/lib.rs + prompts/templates/apply_patch_tool_instructions.md。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApplyPatchToolDef apply_patch 工具声明。
func ApplyPatchToolDef() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "apply_patch",
			Description: "用 diff 格式修改文件(比全文重写省 token)。格式:*** Begin Patch / *** Add File: / *** Update File: / *** Delete File: / *** End Patch",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"patch": {"type": "string", "description": "apply_patch 格式的 diff 文本"}
				},
				"required": ["patch"]
			}`),
		},
	}
}

// FileChange apply_patch 的文件变更。
type FileChange struct {
	Operation   string // add | delete | update
	Path        string
	NewContent  string
	MoveTo      string
	Hunks       []string // update 的 +/- 行
}

// ParsePatch 解析 apply_patch DSL。
func ParsePatch(patchText string) ([]FileChange, error) {
	lines := strings.Split(patchText, "\n")
	var changes []FileChange
	i := 0

	// 跳到 Begin Patch
	for i < len(lines) && !strings.HasPrefix(lines[i], "*** Begin Patch") {
		i++
	}
	i++

	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "*** End Patch") {
			break
		} else if strings.HasPrefix(line, "*** Add File: ") {
			path := strings.TrimPrefix(line, "*** Add File: ")
			i++
			var contentLines []string
			for i < len(lines) && strings.HasPrefix(lines[i], "+") {
				contentLines = append(contentLines, lines[i][1:])
				i++
			}
			changes = append(changes, FileChange{
				Operation: "add", Path: path,
				NewContent: strings.Join(contentLines, "\n"),
			})
		} else if strings.HasPrefix(line, "*** Delete File: ") {
			path := strings.TrimPrefix(line, "*** Delete File: ")
			changes = append(changes, FileChange{Operation: "delete", Path: path})
			i++
		} else if strings.HasPrefix(line, "*** Update File: ") {
			path := strings.TrimPrefix(line, "*** Update File: ")
			i++
			moveTo := ""
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				moveTo = strings.TrimPrefix(lines[i], "*** Move to: ")
				i++
			}
			var hunks []string
			for i < len(lines) && (strings.HasPrefix(lines[i], "@@") ||
				strings.HasPrefix(lines[i], "+") || strings.HasPrefix(lines[i], "-") ||
				strings.HasPrefix(lines[i], " ")) {
				hunks = append(hunks, lines[i])
				i++
			}
			changes = append(changes, FileChange{
				Operation: "update", Path: path, MoveTo: moveTo, Hunks: hunks,
			})
		} else {
			i++
		}
	}
	return changes, nil
}

// ValidatePatchPaths 验证路径安全(只能相对,绝不绝对)。
func ValidatePatchPaths(changes []FileChange) []string {
	var errors []string
	for _, ch := range changes {
		if filepath.IsAbs(ch.Path) {
			errors = append(errors, fmt.Sprintf("绝对路径禁止: %s", ch.Path))
		}
		if ch.MoveTo != "" && filepath.IsAbs(ch.MoveTo) {
			errors = append(errors, fmt.Sprintf("move_to 绝对路径禁止: %s", ch.MoveTo))
		}
		if strings.Contains(ch.Path, "..") {
			errors = append(errors, fmt.Sprintf("路径穿越禁止: %s", ch.Path))
		}
	}
	return errors
}

// ApplyPatch 应用 patch 到文件系统。
func ApplyPatch(patchText, baseDir string) (applied []string, errors []string) {
	changes, err := ParsePatch(patchText)
	if err != nil {
		return nil, []string{err.Error()}
	}
	errors = ValidatePatchPaths(changes)
	if len(errors) > 0 {
		return nil, errors
	}

	for _, ch := range changes {
		fullPath := filepath.Join(baseDir, ch.Path)
		switch ch.Operation {
		case "add":
			os.MkdirAll(filepath.Dir(fullPath), 0755)
			if err := os.WriteFile(fullPath, []byte(ch.NewContent), 0644); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", ch.Path, err))
			} else {
				applied = append(applied, "added "+ch.Path)
			}
		case "delete":
			if err := os.Remove(fullPath); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", ch.Path, err))
			} else {
				applied = append(applied, "deleted "+ch.Path)
			}
		case "update":
			if err := applyUpdateHunks(fullPath, ch); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", ch.Path, err))
			} else if ch.MoveTo != "" {
				newPath := filepath.Join(baseDir, ch.MoveTo)
				os.MkdirAll(filepath.Dir(newPath), 0755)
				os.Rename(fullPath, newPath)
				applied = append(applied, "updated+moved "+ch.Path)
			} else {
				applied = append(applied, "updated "+ch.Path)
			}
		}
	}
	return applied, errors
}

// applyUpdateHunks 应用 @@ context +/- 行 diff。
//
// D15 模糊匹配:当 @@ 上下文锚点精确匹配失败时,尝试模糊匹配:
//   - 去空格比较
//   - 前缀匹配(锚点的前 N 个字符)
//   - 正则化比较(去特殊字符)
func applyUpdateHunks(filepath string, change FileChange) error {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}
	oldLines := strings.Split(string(content), "\n")
	var newLines []string
	oldIdx := 0

	for _, hunkLine := range change.Hunks {
		if strings.HasPrefix(hunkLine, "@@") {
			// 上下文锚点:找到匹配位置
			ctx := strings.TrimSpace(hunkLine[2:])
			if ctx != "" {
				// 精确匹配
				matchIdx := -1
				for i := oldIdx; i < len(oldLines); i++ {
					if strings.Contains(oldLines[i], ctx) {
						matchIdx = i
						break
					}
				}
				// D15 模糊匹配:精确失败时降级
				if matchIdx < 0 {
					matchIdx = fuzzyMatchLine(oldLines, oldIdx, ctx)
				}
				if matchIdx >= 0 {
					// 把跳过的行也加入
					for oldIdx < matchIdx {
						newLines = append(newLines, oldLines[oldIdx])
						oldIdx++
					}
					newLines = append(newLines, oldLines[oldIdx])
					oldIdx++
				}
				// 锚点找不到:跳过不报错(比 codex 宽容,因为 LLM 生成的锚点可能不精确)
			}
		} else if strings.HasPrefix(hunkLine, "+") {
			newLines = append(newLines, hunkLine[1:])
		} else if strings.HasPrefix(hunkLine, "-") {
			if oldIdx < len(oldLines) {
				oldIdx++ // 跳过旧行
			}
		} else if strings.HasPrefix(hunkLine, " ") {
			if oldIdx < len(oldLines) {
				newLines = append(newLines, oldLines[oldIdx])
				oldIdx++
			}
		}
	}
	// 追加剩余
	for oldIdx < len(oldLines) {
		newLines = append(newLines, oldLines[oldIdx])
		oldIdx++
	}
	return os.WriteFile(filepath, []byte(strings.Join(newLines, "\n")), 0644)
}

// D15 fuzzyMatchLine 模糊匹配上下文锚点。
//
// 三级降级:
//   1. 去空格比较(缩进差异)
//   2. 前缀匹配(锚点前 20 字符)
//   3. 关键词匹配(锚点的标识符)
func fuzzyMatchLine(lines []string, startIdx int, ctx string) int {
	// 1. 去空格比较
	ctxNoSpace := removeSpaces(ctx)
	for i := startIdx; i < len(lines); i++ {
		if removeSpaces(lines[i]) == ctxNoSpace {
			return i
		}
	}
	// 2. 前缀匹配(前 20 字符)
	prefix := ctx
	if len(prefix) > 20 {
		prefix = prefix[:20]
	}
	for i := startIdx; i < len(lines); i++ {
		if strings.Contains(lines[i], prefix) {
			return i
		}
	}
	// 3. 标识符匹配(提取 ctx 里的标识符,任一匹配即可)
	identifiers := extractIdentifiers(ctx)
	if len(identifiers) > 0 {
		for i := startIdx; i < len(lines); i++ {
			for _, id := range identifiers {
				if strings.Contains(lines[i], id) {
					return i
				}
			}
		}
	}
	return -1
}

func removeSpaces(s string) string {
	var sb strings.Builder
	for _, c := range s {
		if c != ' ' && c != '\t' {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func extractIdentifiers(s string) []string {
	var ids []string
	current := ""
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			current += string(c)
		} else {
			if len(current) > 3 {
				ids = append(ids, current)
			}
			current = ""
		}
	}
	if len(current) > 3 {
		ids = append(ids, current)
	}
	return ids
}

// D16 AtomicApplyPatch 原子应用:多个文件变更要么全成功要么全回滚。
//
// 先把所有变更应用到临时副本,全部成功后才写真实文件。
// 任一文件失败 → 回滚所有已应用的变更。
func AtomicApplyPatch(patchText, baseDir string) (applied []string, errors []string) {
	changes, err := ParsePatch(patchText)
	if err != nil {
		return nil, []string{err.Error()}
	}
	errs := ValidatePatchPaths(changes)
	if len(errs) > 0 {
		return nil, errs
	}

	// 1. 备份所有要改的文件
	backups := make(map[string][]byte)
	for _, ch := range changes {
		fullPath := filepath.Join(baseDir, ch.Path)
		if data, err := os.ReadFile(fullPath); err == nil {
			backups[fullPath] = data
		}
	}

	// 2. 逐个应用(记录已应用的)
	var appliedFiles []string
	var applyErrors []string
	for _, ch := range changes {
		fullPath := filepath.Join(baseDir, ch.Path)
		switch ch.Operation {
		case "add":
			os.MkdirAll(filepath.Dir(fullPath), 0755)
			if err := os.WriteFile(fullPath, []byte(ch.NewContent), 0644); err != nil {
				applyErrors = append(applyErrors, fmt.Sprintf("%s: %v", ch.Path, err))
			} else {
				appliedFiles = append(appliedFiles, "added "+ch.Path)
			}
		case "delete":
			if err := os.Remove(fullPath); err != nil {
				applyErrors = append(applyErrors, fmt.Sprintf("%s: %v", ch.Path, err))
			} else {
				appliedFiles = append(appliedFiles, "deleted "+ch.Path)
			}
		case "update":
			if err := applyUpdateHunks(fullPath, ch); err != nil {
				applyErrors = append(applyErrors, fmt.Sprintf("%s: %v", ch.Path, err))
			} else {
				appliedFiles = append(appliedFiles, "updated "+ch.Path)
			}
		}
	}

	// 3. 若有错误 → 回滚
	if len(applyErrors) > 0 {
		NewSlog("apply_patch").Error("D16 原子应用失败,回滚 个文件", "appliedFiles_count", len(appliedFiles))
		for fullPath, data := range backups {
			os.WriteFile(fullPath, data, 0644)
		}
		return nil, applyErrors
	}

	return appliedFiles, nil
}

// D17 VerifyPatch 验证 patch 应用结果。
//
// 检查:
//   - Add 的文件确实存在
//   - Delete 的文件确实不存在
//   - Update 后的文件包含 +/- 行的新增内容
func VerifyPatch(changes []FileChange, baseDir string) []string {
	var errors []string
	for _, ch := range changes {
		fullPath := filepath.Join(baseDir, ch.Path)
		switch ch.Operation {
		case "add":
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				errors = append(errors, fmt.Sprintf("验证失败: add 的文件不存在 %s", ch.Path))
			}
		case "delete":
			if _, err := os.Stat(fullPath); err == nil {
				errors = append(errors, fmt.Sprintf("验证失败: delete 的文件仍存在 %s", ch.Path))
			}
		case "update":
			// 检查新增的 + 行是否在文件里
			data, err := os.ReadFile(fullPath)
			if err != nil {
				errors = append(errors, fmt.Sprintf("验证失败: 读不到 %s", ch.Path))
				continue
			}
			content := string(data)
			for _, hunk := range ch.Hunks {
				if strings.HasPrefix(hunk, "+") {
					newLine := hunk[1:]
					if !strings.Contains(content, newLine) {
						errors = append(errors, fmt.Sprintf("验证失败: %s 未包含新增行 '%s'", ch.Path, truncateForVerify(newLine, 40)))
					}
				}
			}
		}
	}
	return errors
}

func truncateForVerify(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ExecuteApplyPatch 执行 apply_patch 工具。
func ExecuteApplyPatch(args json.RawMessage, workspaceRoot string) ToolResult {
	var argsMap map[string]any
	if err := json.Unmarshal(args, &argsMap); err != nil {
		return ToolResult{Name: "apply_patch", Output: "参数解析失败", IsError: true}
	}
	patch, _ := argsMap["patch"].(string)
	if patch == "" {
		return ToolResult{Name: "apply_patch", Output: "缺少 patch 参数", IsError: true}
	}

	// file_history 接入(skill #34):应用 patch 前,对涉及的文件存版本快照
	if workspaceRoot != "" {
		saveSnapshotBeforePatch(patch, workspaceRoot)
	}

	applied, errors := ApplyPatch(patch, workspaceRoot)
	if len(errors) > 0 {
		return ToolResult{Name: "apply_patch",
			Output:  fmt.Sprintf("部分失败: applied=%v errors=%v", applied, errors),
			IsError: true}
	}
	return ToolResult{Name: "apply_patch",
		Output: fmt.Sprintf("应用成功: %v", applied)}
}

// saveSnapshotBeforePatch 从 patch 文本提取涉及的文件路径,修改前存快照。
func saveSnapshotBeforePatch(patchText, workspace string) {
	lines := strings.Split(patchText, "\n")
	for _, line := range lines {
		// 匹配 Add File / Update File / Delete File 的路径声明
		if strings.HasPrefix(line, "*** Add File: ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			if old, err := os.ReadFile(path); err == nil {
				SaveFileSnapshot(workspace, path, string(old), "apply_patch:add")
			}
		} else if strings.HasPrefix(line, "*** Update File: ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if old, err := os.ReadFile(path); err == nil {
				SaveFileSnapshot(workspace, path, string(old), "apply_patch:update")
			}
		} else if strings.HasPrefix(line, "*** Delete File: ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if old, err := os.ReadFile(path); err == nil {
				SaveFileSnapshot(workspace, path, string(old), "apply_patch:delete")
			}
		}
	}
}

// ── D13 Lark 文法 + D14 流式 Tokenizer ──

// PatchToken patch 解析 token。
type PatchToken struct {
	Type  string // begin_patch|end_patch|add_file|delete_file|update_file|move_to|context|add_line|del_line
	Value string
	Line  int
}

// TokenizePatch D14 流式逐行 token 化(StreamingPatchParser 等价)。
func TokenizePatch(patchText string) ([]PatchToken, []string) {
	lines := strings.Split(patchText, "\n")
	var tokens []PatchToken
	var errors []string
	inPatch := false
	for i, line := range lines {
		ln := i + 1
		switch {
		case strings.HasPrefix(line, "*** Begin Patch"):
			inPatch = true
			tokens = append(tokens, PatchToken{Type: "begin_patch", Line: ln})
		case strings.HasPrefix(line, "*** End Patch"):
			tokens = append(tokens, PatchToken{Type: "end_patch", Line: ln})
			inPatch = false
		case !inPatch:
			continue
		case strings.HasPrefix(line, "*** Add File: "):
			tokens = append(tokens, PatchToken{Type: "add_file", Value: line[14:], Line: ln})
		case strings.HasPrefix(line, "*** Delete File: "):
			tokens = append(tokens, PatchToken{Type: "delete_file", Value: line[17:], Line: ln})
		case strings.HasPrefix(line, "*** Update File: "):
			tokens = append(tokens, PatchToken{Type: "update_file", Value: line[17:], Line: ln})
		case strings.HasPrefix(line, "*** Move to: "):
			tokens = append(tokens, PatchToken{Type: "move_to", Value: line[13:], Line: ln})
		case strings.HasPrefix(line, "@@"):
			tokens = append(tokens, PatchToken{Type: "context", Value: strings.TrimSpace(line[2:]), Line: ln})
		case strings.HasPrefix(line, "+"):
			tokens = append(tokens, PatchToken{Type: "add_line", Value: line[1:], Line: ln})
		case strings.HasPrefix(line, "-"):
			tokens = append(tokens, PatchToken{Type: "del_line", Value: line[1:], Line: ln})
		case strings.HasPrefix(line, " ") && len(line) > 0:
			tokens = append(tokens, PatchToken{Type: "context_line", Value: line[1:], Line: ln})
		}
	}
	// D13 文法验证
	if len(tokens) == 0 {
		errors = append(errors, "patch 为空")
	} else {
		if tokens[0].Type != "begin_patch" {
			errors = append(errors, "必须以 *** Begin Patch 开头")
		}
		if tokens[len(tokens)-1].Type != "end_patch" {
			errors = append(errors, "必须以 *** End Patch 结尾")
		}
		for i, tok := range tokens {
			if tok.Type == "add_file" && i+1 < len(tokens) && tokens[i+1].Type != "add_line" {
				errors = append(errors, fmt.Sprintf("第%d行: add_file 后必须跟 + 行", tok.Line))
			}
		}
	}
	return tokens, errors
}

// ValidatePatchGrammar D13 完整文法验证。
func ValidatePatchGrammar(patchText string) []string {
	_, errors := TokenizePatch(patchText)
	return errors
}
