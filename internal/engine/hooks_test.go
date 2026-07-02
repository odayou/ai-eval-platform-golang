package engine

import (
	"testing"

	"ai-eval-platform/internal/store"
)

func TestMatchMatcher(t *testing.T) {
	cases := []struct {
		matcher  string
		toolName string
		match    bool
	}{
		{"", "BashTool", true},     // empty = match all
		{"*", "BashTool", true},    // * = match all
		{"BashTool", "BashTool", true},
		{"BashTool", "FileRead", false},
		{"Bash*", "BashTool", true},
		{"Bash*", "BashScript", true},
		{"*Tool", "BashTool", true},
		{"*Tool", "FileRead", false},
	}

	for _, c := range cases {
		got := matchMatcher(c.matcher, c.toolName)
		if got != c.match {
			t.Errorf("matchMatcher(%q, %q) = %v, want %v", c.matcher, c.toolName, got, c.match)
		}
	}
}

func TestParseHookStdout_Empty(t *testing.T) {
	d := parseHookStdout("")
	if !d.Allow {
		t.Errorf("expected allow on empty")
	}
}

func TestParseHookStdout_JSONAllow(t *testing.T) {
	d := parseHookStdout(`{"decision": "", "message": "ok"}`)
	if !d.Allow {
		t.Errorf("expected allow")
	}
	if d.Message != "ok" {
		t.Errorf("expected message 'ok', got %q", d.Message)
	}
}

func TestParseHookStdout_JSONBlock(t *testing.T) {
	d := parseHookStdout(`{"decision": "block", "message": "dangerous"}`)
	if d.Allow {
		t.Errorf("expected block")
	}
	if d.Message != "dangerous" {
		t.Errorf("expected message 'dangerous', got %q", d.Message)
	}
}

func TestParseHookStdout_PlainText(t *testing.T) {
	d := parseHookStdout("custom message")
	if !d.Allow {
		t.Errorf("expected allow on plain text")
	}
	if d.Message != "custom message" {
		t.Errorf("expected 'custom message', got %q", d.Message)
	}
}

func TestSortHooksByPriority(t *testing.T) {
	hooks := []HookRowExt{
		{HookRow: store.HookRow{Command: "low"}, Priority: 1},
		{HookRow: store.HookRow{Command: "high"}, Priority: 10},
		{HookRow: store.HookRow{Command: "mid"}, Priority: 5},
	}
	sorted := sortHooksByPriority(hooks)
	if sorted[0].Command != "high" || sorted[1].Command != "mid" || sorted[2].Command != "low" {
		t.Errorf("expected high > mid > low, got: %s %s %s",
			sorted[0].Command, sorted[1].Command, sorted[2].Command)
	}
}

func TestGetEventTimeout(t *testing.T) {
	// 已知事件的超时
	if d := getEventTimeout(HookPreToolUse); d.Seconds() != 10 {
		t.Errorf("PreToolUse timeout = %v, want 10s", d)
	}
	if d := getEventTimeout(HookStop); d.Seconds() != 60 {
		t.Errorf("Stop timeout = %v, want 60s", d)
	}
	// 未知事件返回默认 10s
	if d := getEventTimeout("UnknownEvent"); d.Seconds() != 10 {
		t.Errorf("unknown event timeout = %v, want 10s", d)
	}
}

func TestBuiltinPreToolHooks(t *testing.T) {
	// read-only 允许只读命令（ls/cat/echo），禁止写命令
	d := BuiltinPreToolHooks("BashTool", "ls -la", "read-only")
	if !d.Allow {
		t.Errorf("read-only should allow ls -la, got blocked: %s", d.Message)
	}

	d2 := BuiltinPreToolHooks("BashTool", "ls -la", "full")
	if !d2.Allow {
		t.Errorf("full perm should allow BashTool")
	}

	d3 := BuiltinPreToolHooks("FileRead", "", "read-only")
	if !d3.Allow {
		t.Errorf("FileRead should be allowed in read-only")
	}

	// 写命令在 read-only 下应被拒绝
	d4 := BuiltinPreToolHooks("BashTool", "rm file.txt", "read-only")
	if d4.Allow {
		t.Errorf("read-only should block rm")
	}
}
