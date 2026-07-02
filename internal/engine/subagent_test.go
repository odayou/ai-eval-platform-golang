package engine

import (
	"testing"
)

func TestFilterSubagentTools_EmptyAllowed(t *testing.T) {
	allTools := []ToolDef{
		{Function: ToolFunction{Name: "FileRead"}},
		{Function: ToolFunction{Name: "BashTool"}},
	}
	filtered := filterSubagentTools(allTools, nil)
	if len(filtered) != len(allTools) {
		t.Errorf("expected %d tools (nil = inherit all), got %d", len(allTools), len(filtered))
	}
}

func TestFilterSubagentTools_Filtered(t *testing.T) {
	allTools := []ToolDef{
		{Function: ToolFunction{Name: "FileRead"}},
		{Function: ToolFunction{Name: "FileWrite"}},
		{Function: ToolFunction{Name: "BashTool"}},
	}
	filtered := filterSubagentTools(allTools, []string{"FileRead", "BashTool"})
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools, got %d", len(filtered))
	}
	if filtered[0].Function.Name != "FileRead" {
		t.Errorf("expected FileRead, got %s", filtered[0].Function.Name)
	}
	if filtered[1].Function.Name != "BashTool" {
		t.Errorf("expected BashTool, got %s", filtered[1].Function.Name)
	}
}

func TestFilterSubagentTools_EmptyAllowedList(t *testing.T) {
	allTools := []ToolDef{
		{Function: ToolFunction{Name: "FileRead"}},
	}
	filtered := filterSubagentTools(allTools, []string{})
	if len(filtered) != len(allTools) {
		t.Errorf("empty list should inherit all, got %d", len(filtered))
	}
}

func TestSetMaxSubagentDepth(t *testing.T) {
	oldDepth := maxSubagentDepth
	defer func() { maxSubagentDepth = oldDepth }()

	SetMaxSubagentDepth(3)
	if maxSubagentDepth != 3 {
		t.Errorf("expected depth 3, got %d", maxSubagentDepth)
	}

	// < 1 should be ignored
	SetMaxSubagentDepth(0)
	if maxSubagentDepth != 3 {
		t.Errorf("depth 0 should be ignored, got %d", maxSubagentDepth)
	}
}

func TestSendMessageToolDef(t *testing.T) {
	def := SendMessageToolDef()
	if def.Function.Name != "SendMessage" {
		t.Errorf("expected SendMessage, got %s", def.Function.Name)
	}
}

func TestCustomSubagentDef_DefaultTools(t *testing.T) {
	defs := LoadCustomSubagents()
	explore, ok := defs["Explore"]
	if !ok {
		t.Fatal("Explore should be in default subagents")
	}
	if len(explore.Tools) == 0 {
		t.Errorf("Explore should have tools defined")
	}
	if explore.MaxTurns <= 0 {
		t.Errorf("Explore MaxTurns = %d, want > 0", explore.MaxTurns)
	}

	plan, ok := defs["Plan"]
	if !ok {
		t.Fatal("Plan should be in default subagents")
	}
	if plan.MaxTurns <= 0 {
		t.Errorf("Plan MaxTurns = %d, want > 0", plan.MaxTurns)
	}

	general, ok := defs["General"]
	if !ok {
		t.Fatal("General should be in default subagents")
	}
	if general.MaxTurns <= 0 {
		t.Errorf("General MaxTurns = %d, want > 0", general.MaxTurns)
	}
}
