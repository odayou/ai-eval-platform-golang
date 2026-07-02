package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// ── D69 apply_patch 测试 ──

func TestParsePatch_AddFile(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: hello.txt\n+Hello world\n+Second line\n*** End Patch"
	changes, err := ParsePatch(patch)
	if err != nil {
		t.Fatalf("ParsePatch 失败: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("应解析出 1 个变更,实际 %d", len(changes))
	}
	if changes[0].Operation != "add" || changes[0].Path != "hello.txt" {
		t.Errorf("预期 add hello.txt, 实际 %s %s", changes[0].Operation, changes[0].Path)
	}
	if changes[0].NewContent != "Hello world\nSecond line" {
		t.Errorf("内容不匹配: %q", changes[0].NewContent)
	}
}

func TestParsePatch_DeleteFile(t *testing.T) {
	patch := "*** Begin Patch\n*** Delete File: obsolete.txt\n*** End Patch"
	changes, _ := ParsePatch(patch)
	if len(changes) != 1 || changes[0].Operation != "delete" {
		t.Errorf("应解析出 delete obsolete.txt")
	}
}

func TestValidatePatchPaths_AbsolutePath(t *testing.T) {
	// Windows 和 Unix 都能测:用 .. 穿越
	changes := []FileChange{
		{Operation: "add", Path: "../../../etc/passwd"},
	}
	errors := ValidatePatchPaths(changes)
	if len(errors) == 0 {
		t.Error("路径穿越应被拒绝")
	}
}

func TestValidatePatchPaths_PathTraversal(t *testing.T) {
	changes := []FileChange{
		{Operation: "add", Path: "../../../etc/passwd"},
	}
	errors := ValidatePatchPaths(changes)
	if len(errors) == 0 {
		t.Error("路径穿越应被拒绝")
	}
}

func TestApplyPatch_AddAndRead(t *testing.T) {
	tmpDir := t.TempDir()
	patch := "*** Begin Patch\n*** Add File: test.txt\n+Hello from patch\n*** End Patch"
	applied, errors := ApplyPatch(patch, tmpDir)
	if len(errors) > 0 {
		t.Fatalf("应用失败: %v", errors)
	}
	if len(applied) != 1 {
		t.Fatalf("应应用 1 个变更,实际 %d", len(applied))
	}
	content, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	if err != nil {
		t.Fatalf("读文件失败: %v", err)
	}
	if string(content) != "Hello from patch" {
		t.Errorf("内容不匹配: %q", string(content))
	}
}

func TestFuzzyMatchLine_Exact(t *testing.T) {
	lines := []string{"func main() {", "    fmt.Println(\"hi\")", "}"}
	idx := fuzzyMatchLine(lines, 0, "fmt.Println(\"hi\")")
	if idx != 1 {
		t.Errorf("精确匹配应返回 1, 实际 %d", idx)
	}
}

func TestFuzzyMatchLine_NoSpace(t *testing.T) {
	lines := []string{"  func main() {"}
	idx := fuzzyMatchLine(lines, 0, "func main() {")
	if idx != 0 {
		t.Errorf("去空格匹配应返回 0, 实际 %d", idx)
	}
}

// ── D70 Guardian 测试 ──

func TestClassifyRisk_Critical(t *testing.T) {
	req := GuardianApprovalRequest{Type: "shell", Command: "rm -rf /"}
	risk := ClassifyRisk(req)
	if risk != RiskCritical {
		t.Errorf("rm -rf / 应判 critical, 实际 %s", risk)
	}
	req2 := GuardianApprovalRequest{Type: "shell", Command: "mkfs.ext4 /dev/sda"}
	if ClassifyRisk(req2) != RiskCritical {
		t.Error("mkfs 应判 critical")
	}
}

func TestClassifyRisk_Low(t *testing.T) {
	req := GuardianApprovalRequest{Type: "shell", Command: "ls -la"}
	if ClassifyRisk(req) != RiskLow {
		t.Error("ls 应判 low")
	}
	req2 := GuardianApprovalRequest{Type: "shell", Command: "go test ./..."}
	if ClassifyRisk(req2) != RiskLow {
		t.Error("go test 应判 low")
	}
}

func TestClassifyRisk_CredentialProbe(t *testing.T) {
	req := GuardianApprovalRequest{Type: "shell", Command: "cat ~/.ssh/id_rsa"}
	if ClassifyRisk(req) != RiskHigh {
		t.Error("读 id_rsa 应判 high")
	}
}

func TestClassifyRisk_NetworkMetadata(t *testing.T) {
	req := GuardianApprovalRequest{Type: "network", NetworkTarget: "169.254.169.254:80"}
	if ClassifyRisk(req) != RiskCritical {
		t.Error("访问 AWS 元数据应判 critical")
	}
}

func TestCircuitBreaker_ConsecutiveDenials(t *testing.T) {
	cb := NewGuardianCircuitBreaker()
	for i := 0; i < 2; i++ {
		if cb.RecordDenial() {
			t.Errorf("第 %d 次拒批不应熔断", i+1)
		}
	}
	if !cb.RecordDenial() {
		t.Error("第 3 次连续拒批应熔断")
	}
}

func TestCircuitBreaker_ResetOnAllow(t *testing.T) {
	cb := NewGuardianCircuitBreaker()
	cb.RecordDenial()
	cb.RecordDenial()
	cb.RecordAllow()
	if cb.RecordDenial() {
		t.Error("allow 后连续计数应重置, 第 1 次拒批不应熔断")
	}
}

func TestCompressGuardianTranscript(t *testing.T) {
	messages := make([]string, 100)
	for i := range messages {
		messages[i] = "这是第 " + string(rune('A'+i%26)) + " 条消息"
	}
	result := CompressGuardianTranscript(messages, nil, 1000, 0)
	if len(result) == 0 {
		t.Error("压缩结果不应为空")
	}
	if len(result) > 5000 {
		t.Error("压缩结果应被截断到合理大小")
	}
}
