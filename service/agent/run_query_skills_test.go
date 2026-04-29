package agent

import (
	"testing"

	bindpkg "github.com/viant/agently-core/protocol/binding"
)

func TestParseExplicitSkillInvocation(t *testing.T) {
	tests := []struct {
		input string
		name  string
		args  string
		ok    bool
	}{
		{input: "/playwright-cli https://example.com", name: "playwright-cli", args: "https://example.com", ok: true},
		{input: "   $playwright-cli run smoke", name: "playwright-cli", args: "run smoke", ok: true},
		{input: "/help", ok: false},
		{input: "please use /playwright-cli", ok: false},
	}
	for _, tc := range tests {
		name, args, ok := parseExplicitSkillInvocation(tc.input)
		if name != tc.name || args != tc.args || ok != tc.ok {
			t.Fatalf("parseExplicitSkillInvocation(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.input, name, args, ok, tc.name, tc.args, tc.ok)
		}
	}
}

func TestMergeReplayMessages_PreservesSyntheticSkillActivation(t *testing.T) {
	activation := &bindpkg.Message{
		ID:       "skill-activate-forecasting-cube",
		ToolOpID: "skill-activate-forecasting-cube",
		ToolName: "llm/skills:activate",
	}
	toolResult := &bindpkg.Message{
		ID:       "",
		ToolOpID: "call-1",
		ToolName: "steward:ForecastingCube",
	}
	merged := mergeReplayMessages([]*bindpkg.Message{activation}, []*bindpkg.Message{toolResult})
	if len(merged) != 2 {
		t.Fatalf("merged len = %d, want 2", len(merged))
	}
	if merged[0] != activation {
		t.Fatalf("expected activation to remain first, got %#v", merged[0])
	}
	if merged[1] != toolResult {
		t.Fatalf("expected tool result to append, got %#v", merged[1])
	}
}
