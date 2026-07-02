package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// ── 3.2 FileEdit ──

func TestExecFileEdit_MissingParams(t *testing.T) {
	r := execFileEdit(map[string]any{"path": "", "old_text": "", "new_text": ""},
		"workspace-write", ".", "test", "")
	if !r.IsError {
		t.Errorf("expected error for missing params")
	}
}

func TestExecFileEdit_ReadOnly(t *testing.T) {
	r := execFileEdit(map[string]any{"path": "test.txt", "old_text": "old", "new_text": "new"},
		"read-only", ".", "test", "")
	if !r.IsError {
		t.Errorf("expected error for read-only perm")
	}
}

func TestExecFileEdit_SimpleReplace(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("hello world hello"), 0644)

	r := execFileEdit(map[string]any{
		"path":    filePath,
		"old_text": "hello",
		"new_text": "hi",
	}, "workspace-write", tmpDir, "test", "")

	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	content, _ := os.ReadFile(filePath)
	if string(content) != "hi world hello" {
		t.Errorf("expected 'hi world hello', got %q", string(content))
	}
}

func TestExecFileEdit_ReplaceAll(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("hello world hello"), 0644)

	r := execFileEdit(map[string]any{
		"path":        filePath,
		"old_text":    "hello",
		"new_text":    "hi",
		"replace_all": true,
	}, "workspace-write", tmpDir, "test", "")

	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	content, _ := os.ReadFile(filePath)
	if string(content) != "hi world hi" {
		t.Errorf("expected 'hi world hi', got %q", string(content))
	}
}

func TestExecFileEdit_ConflictDetection(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("hello world hello"), 0644)

	r := execFileEdit(map[string]any{
		"path":    filePath,
		"old_text": "missing",
		"new_text": "hi",
	}, "workspace-write", tmpDir, "test", "")

	if !r.IsError {
		t.Errorf("expected error for missing old_text")
	}
}

// ── 3.3 Glob ──

func TestExecGlob_MissingPattern(t *testing.T) {
	r := execGlob(map[string]any{}, ".", "test", "")
	if !r.IsError {
		t.Errorf("expected error for missing pattern")
	}
}

func TestExecGlob_MatchSimple(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.py"), []byte("print('hi')"), 0644)

	r := execGlob(map[string]any{"pattern": "*.go"}, tmpDir, "test", "")
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	if !filepathMatch(r.Output, "a.go") {
		t.Errorf("expected a.go in output, got: %s", r.Output)
	}
	if filepathMatch(r.Output, "b.py") {
		t.Errorf("did not expect b.py in output: %s", r.Output)
	}
}

// filepathMatch 检查输出是否包含路径（简化版）
func filepathMatch(output, name string) bool {
	for i := 0; i <= len(output)-len(name); i++ {
		if output[i] == name[0] {
			match := true
			for j := 0; j < len(name); j++ {
				if i+j >= len(output) || output[i+j] != name[j] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// ── 3.4 Grep ──

func TestExecGrep_MissingPattern(t *testing.T) {
	r := execGrep(map[string]any{}, ".", "test", "")
	if !r.IsError {
		t.Errorf("expected error for missing pattern")
	}
}

func TestExecGrep_FindMatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello world\nfunc main() {\nfoo bar\n"), 0644)

	r := execGrep(map[string]any{"pattern": "func"}, tmpDir, "test", "")
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	if !filepathMatch(r.Output, "func") {
		t.Errorf("expected 'func' in output, got: %s", r.Output)
	}
}

func TestExecGrep_CaseInsensitive(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("Hello World"), 0644)

	r := execGrep(map[string]any{"pattern": "hello", "case_insensitive": true}, tmpDir, "test", "")
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	if !filepathMatch(r.Output, "Hello") {
		t.Errorf("expected 'Hello' in case-insensitive output, got: %s", r.Output)
	}
}

func TestExecGrep_ContextBefore(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("line1\nline2\nmatch\nline4"), 0644)

	r := execGrep(map[string]any{"pattern": "match", "context_before": 1.0}, tmpDir, "test", "")
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	if !filepathMatch(r.Output, "line2") {
		t.Errorf("expected 'line2' (context before) in output, got: %s", r.Output)
	}
}

func TestExecGrep_ContextAfter(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("line1\nmatch\nline3\nline4"), 0644)

	r := execGrep(map[string]any{"pattern": "match", "context_after": 1.0}, tmpDir, "test", "")
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Output)
	}
	t.Logf("context_after output: %s", r.Output)
}