package skill

import (
	"context"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

func TestBuildConstraintsAndValidateExecution(t *testing.T) {
	skills := []*skillproto.Skill{{
		Frontmatter: skillproto.Frontmatter{
			Name:         "playwright-cli",
			AllowedTools: "Bash(playwright-cli:*) Bash(npx:*) Bash(npm:*) system/exec:execute",
		},
	}}
	c := BuildConstraints(skills)
	if c == nil {
		t.Fatalf("expected constraints")
	}
	defs := []*llm.ToolDefinition{
		{Name: "system/exec:execute"},
		{Name: "system/patch:apply"},
	}
	narrowed := NarrowDefinitionsForConstraints(defs, c)
	if len(narrowed) != 1 || narrowed[0].Name != "system/exec:execute" {
		t.Fatalf("narrowed defs = %#v", narrowed)
	}
	ctx := WithConstraints(context.Background(), c)
	if err := ValidateExecution(ctx, "system/exec:execute", map[string]interface{}{"commands": []string{"playwright-cli open", "npx playwright --version"}}); err != nil {
		t.Fatalf("ValidateExecution() unexpected error: %v", err)
	}
	if err := ValidateExecution(ctx, "system/exec:execute", map[string]interface{}{"commands": []string{"rm -rf /tmp/x"}}); err == nil {
		t.Fatalf("expected command rejection")
	}
}

func TestBuildConstraints_AllowsWorkspaceToolAlongsidePreprocessExec(t *testing.T) {
	skills := []*skillproto.Skill{{
		Frontmatter: skillproto.Frontmatter{
			Name:         "targeting-tree",
			AllowedTools: "system/exec:execute platform:TargetingTree",
		},
	}}
	c := BuildConstraints(skills)
	if c == nil {
		t.Fatalf("expected constraints")
	}
	ctx := WithConstraints(context.Background(), c)
	if err := ValidateExecution(ctx, "platform:TargetingTree", map[string]interface{}{"Field": "IRIS_SEGMENTS"}); err != nil {
		t.Fatalf("ValidateExecution() unexpected platform tool error: %v", err)
	}
}
