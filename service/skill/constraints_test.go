package skill

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

type constraintRegistry struct {
	defs []llm.ToolDefinition
}

func (r *constraintRegistry) Definitions() []llm.ToolDefinition { return r.defs }
func (r *constraintRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	target := strings.TrimSpace(strings.ToLower(mcpname.Canonical(name)))
	for i := range r.defs {
		if strings.TrimSpace(strings.ToLower(mcpname.Canonical(r.defs[i].Name))) == target {
			def := r.defs[i]
			return &def, true
		}
	}
	return nil, false
}
func (r *constraintRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	pattern = strings.TrimSpace(pattern)
	var out []*llm.ToolDefinition
	for i := range r.defs {
		name := strings.TrimSpace(strings.ToLower(mcpname.Canonical(r.defs[i].Name)))
		pp := strings.TrimSpace(strings.ToLower(mcpname.Canonical(pattern)))
		if pp == "" {
			continue
		}
		if pp == name || (strings.HasSuffix(pp, "*") && strings.HasPrefix(name, strings.TrimSuffix(pp, "*"))) || (!strings.Contains(pattern, ":") && strings.HasPrefix(name, pp)) {
			def := r.defs[i]
			out = append(out, &def)
		}
	}
	return out
}
func (r *constraintRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *constraintRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (r *constraintRegistry) SetDebugLogger(io.Writer)                 {}
func (r *constraintRegistry) Initialize(context.Context)               {}
func (r *constraintRegistry) ToolTimeout(string) (time.Duration, bool) { return 0, false }

type exactCanonicalRegistry struct {
	defs []llm.ToolDefinition
}

func (r *exactCanonicalRegistry) Definitions() []llm.ToolDefinition { return r.defs }
func (r *exactCanonicalRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	target := strings.TrimSpace(mcpname.Canonical(name))
	for i := range r.defs {
		if strings.TrimSpace(mcpname.Canonical(r.defs[i].Name)) == target {
			def := r.defs[i]
			return &def, true
		}
	}
	return nil, false
}
func (r *exactCanonicalRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	pattern = strings.TrimSpace(pattern)
	var out []*llm.ToolDefinition
	for i := range r.defs {
		name := strings.TrimSpace(mcpname.Canonical(r.defs[i].Name))
		if pattern == name {
			def := r.defs[i]
			out = append(out, &def)
		}
	}
	return out
}
func (r *exactCanonicalRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *exactCanonicalRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (r *exactCanonicalRegistry) SetDebugLogger(io.Writer)                 {}
func (r *exactCanonicalRegistry) Initialize(context.Context)               {}
func (r *exactCanonicalRegistry) ToolTimeout(string) (time.Duration, bool) { return 0, false }

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

func TestExpandDefinitionsForConstraints_AddsAllowedSkillTools(t *testing.T) {
	skills := []*skillproto.Skill{{
		Frontmatter: skillproto.Frontmatter{
			Name:         "forecasting-cube",
			AllowedTools: "steward:ForecastingCube",
		},
	}}
	c := BuildConstraints(skills)
	if c == nil {
		t.Fatalf("expected constraints")
	}
	reg := &constraintRegistry{defs: []llm.ToolDefinition{
		{Name: "prompt:list"},
		{Name: "steward:ForecastingCube"},
	}}
	defs := []*llm.ToolDefinition{{Name: "prompt:list"}}
	expanded := ExpandDefinitionsForConstraints(defs, reg, c)
	if len(expanded) != 2 {
		t.Fatalf("expanded defs = %#v", expanded)
	}
	narrowed := NarrowDefinitionsForConstraints(expanded, c)
	if len(narrowed) != 1 || narrowed[0].Name != "steward:ForecastingCube" {
		t.Fatalf("narrowed defs = %#v", narrowed)
	}
}

func TestExpandDefinitionsForConstraints_NormalizesAllowedToolPatternForRegistryLookup(t *testing.T) {
	skills := []*skillproto.Skill{{
		Frontmatter: skillproto.Frontmatter{
			Name:         "forecasting-cube",
			AllowedTools: "steward:ForecastingCube",
		},
	}}
	c := BuildConstraints(skills)
	if c == nil {
		t.Fatalf("expected constraints")
	}
	reg := &exactCanonicalRegistry{defs: []llm.ToolDefinition{
		{Name: "steward:ForecastingCube"},
	}}
	expanded := ExpandDefinitionsForConstraints(nil, reg, c)
	if len(expanded) != 1 || expanded[0] == nil || expanded[0].Name != "steward:ForecastingCube" {
		t.Fatalf("expanded defs = %#v", expanded)
	}
}
