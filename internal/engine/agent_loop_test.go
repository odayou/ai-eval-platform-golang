package engine

import (
	"errors"
	"testing"
)

// ── 2.1 错误分类器 ──

func TestClassifyAgentError(t *testing.T) {
	cases := []struct {
		err      error
		expected agentErrorType
	}{
		{errors.New("401 unauthorized"), errFatal},
		{errors.New("403 forbidden"), errFatal},
		{errors.New("429 rate limited"), errRetryable},
		{errors.New("500 internal server error"), errRetryable},
		{errors.New("502 bad gateway"), errRetryable},
		{errors.New("503 service unavailable"), errRetryable},
		{errors.New("timeout after 10s"), errRetryable},
		{errors.New("connection refused"), errRetryable},
		{errors.New("connection reset by peer"), errRetryable},
		{errors.New("unexpected EOF"), errRetryable},
		{errors.New("invalid JSON: unexpected token"), errMalformed},
		{errors.New("json: cannot unmarshal"), errMalformed},
		{errors.New("parse error at line 1"), errMalformed},
		{errors.New("network timeout"), errRetryable},
		{errors.New("unknown error occurred"), errUnknown},
	}

	for _, c := range cases {
		got := classifyAgentError(c.err)
		if got != c.expected {
			t.Errorf("classifyAgentError(%q) = %v, want %v", c.err.Error(), got, c.expected)
		}
	}
}

func TestClassifyAgentError_Nil(t *testing.T) {
	got := classifyAgentError(nil)
	if got != errUnknown {
		t.Errorf("classifyAgentError(nil) = %v, want errUnknown", got)
	}
}

// ── isDangerousCommand ──

func TestIsDangerousCommand(t *testing.T) {
	cases := []struct {
		cmd      string
		dangerous bool
	}{
		{"ls -la", false},
		{"echo hello", false},
		{"cat file.txt", false},
		{"rm file.txt", true},
		{"rm -rf /", true},
		{"sudo apt install", true},
		{"chmod 777 file", true},
		{"docker run --rm alpine", true},
		{"pip install flask", true},
		{"npm install -g yarn", true},
		{"curl http://example.com", true},
		{"cd /tmp && go build", false},
		{"git status", false},
		{"shutdown -h now", true},
		{"reboot", true},
		{"df -h", false},
		{"> /etc/passwd", true},
	}

	for _, c := range cases {
		got := isDangerousCommand(c.cmd)
		if got != c.dangerous {
			t.Errorf("isDangerousCommand(%q) = %v, want %v", c.cmd, got, c.dangerous)
		}
	}
}

// ── 2.4 canRunParallel ──

func TestCanRunParallel(t *testing.T) {
	cases := []struct {
		name     string
		expected bool
	}{
		{"FileRead", true},
		{"Glob", true},
		{"Grep", true},
		{"WebSearch", true},
		{"Task", true},
		{"LoadSkill", true},
		{"TodoWrite", true},
		{"FileWrite", false},
		{"BashTool", false},
		{"apply_patch", false},
		{"SendMessage", false},
		{"Verify", false},
	}

	for _, c := range cases {
		tc := ToolCall{Name: c.name}
		got := canRunParallel(tc)
		if got != c.expected {
			t.Errorf("canRunParallel(%q) = %v, want %v", c.name, got, c.expected)
		}
	}
}

// ── 2.4 executeToolsParallel（模拟测试）──

func TestExecuteToolsParallel_Empty(t *testing.T) {
	results := executeToolsParallel(nil, toolExecRequest{})
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestCanRunParallel_AllReadTools(t *testing.T) {
	tools := []ToolCall{
		{Name: "FileRead", Arguments: []byte(`{"path": "test.txt"}`)},
		{Name: "Glob", Arguments: []byte(`{"pattern": "*.go"}`)},
	}
	for _, tc := range tools {
		if !canRunParallel(tc) {
			t.Errorf("%s should be parallelizable", tc.Name)
		}
	}
}

func TestCanRunParallel_WriteTools(t *testing.T) {
	writeTools := []string{"FileWrite", "BashTool", "apply_patch"}
	for _, name := range writeTools {
		tc := ToolCall{Name: name}
		if canRunParallel(tc) {
			t.Errorf("%s should NOT be parallelizable", name)
		}
	}
}

// ── ToolResult 结构 ──

func TestToolResult_IsError(t *testing.T) {
	r := ToolResult{Name: "BashTool", Output: "success", IsError: false}
	if r.IsError {
		t.Errorf("expected not error")
	}
	if r.Name != "BashTool" {
		t.Errorf("name = %q, want BashTool", r.Name)
	}
}

// ── agentErrorType.String ──

func TestAgentErrorType_String(t *testing.T) {
	if errRetryable.String() != "retryable" {
		t.Errorf("errRetryable.String() = %q", errRetryable.String())
	}
	if errFatal.String() != "fatal" {
		t.Errorf("errFatal.String() = %q", errFatal.String())
	}
	if errMalformed.String() != "malformed" {
		t.Errorf("errMalformed.String() = %q", errMalformed.String())
	}
	if errUnknown.String() != "unknown" {
		t.Errorf("errUnknown.String() = %q", errUnknown.String())
	}
}
