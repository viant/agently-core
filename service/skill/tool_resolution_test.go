package skill

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

func TestSkillToolResolution(t *testing.T) {
	reg := &constraintRegistry{defs: []llm.ToolDefinition{
		{Name: "alpha:lookup"}, {Name: "beta:lookup"}, {Name: "alpha-lookup"},
		{Name: "alpha:unique"}, {Name: "alpha:lookup_extra"},
	}}
	require.Len(t, matchSkillTool(context.Background(), reg, "lookup"), 2)
	require.Equal(t, "alpha:unique", matchSkillTool(context.Background(), reg, "unique")[0].Name)
	require.Empty(t, matchSkillTool(context.Background(), reg, "missing"))
	require.Empty(t, matchSkillTool(context.Background(), reg, "missing:unique"))
	require.Len(t, matchSkillTool(context.Background(), reg, "beta:lookup"), 1)
}

func TestSkillResolutionGuidanceAndCatalogIsolation(t *testing.T) {
	reg := &constraintRegistry{defs: []llm.ToolDefinition{{Name: "alpha:lookup"}, {Name: "beta:lookup"}, {Name: "alpha:unique"}}}
	svc := &Service{toolRegistry: reg}
	original := &skillproto.Skill{Frontmatter: skillproto.Frontmatter{Name: "test", AllowedTools: "lookup unique Bash(git:*) system/exec:*"}, Body: "Do the task."}
	resolved := svc.resolvedToolSkill(context.Background(), original)
	require.Equal(t, "lookup unique Bash(git:*) system/exec:*", original.Frontmatter.AllowedTools)
	require.Equal(t, "Do the task.", original.Body)
	require.Equal(t, "alpha:lookup beta:lookup alpha:unique Bash(git:*) system/exec:*", resolved.Frontmatter.AllowedTools)
	require.Contains(t, resolved.Body, "existing elicitation flow")
	require.Contains(t, resolved.Body, "if they cancel or do not choose")
	require.Contains(t, resolved.Body, `Resolve tool "unique" to "alpha:unique"`)
	// Resolution is metadata-only; no execution or hardcoded elicitation is invoked.
	qualified := &skillproto.Skill{Frontmatter: skillproto.Frontmatter{AllowedTools: "alpha:lookup alpha:* Bash(git:*)"}, Body: "unchanged"}
	require.Equal(t, qualified, svc.resolvedToolSkill(context.Background(), qualified))
}

type scopedSkillRegistry struct {
	constraintRegistry
	seen context.Context
}

func (r *scopedSkillRegistry) MatchDefinitionWithContext(ctx context.Context, pattern string) []*llm.ToolDefinition {
	r.seen = ctx
	return r.MatchDefinition(pattern)
}
func (r *scopedSkillRegistry) DefinitionsWithContext(ctx context.Context) []llm.ToolDefinition {
	r.seen = ctx
	return r.defs
}
func TestSkillBareToolSurfacePreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := &scopedSkillRegistry{constraintRegistry: constraintRegistry{defs: []llm.ToolDefinition{{Name: "alpha:lookup"}}}}
	defs, missing := ExpandDefinitionsForConstraintsWithContext(ctx, nil, reg, &Constraints{ToolPatterns: []string{"lookup"}})
	require.Empty(t, missing)
	require.Len(t, defs, 1)
	require.Same(t, ctx, reg.seen)
}

func TestSkillActivationIncludesToolChoiceGuidance(t *testing.T) {
	svc := &Service{toolRegistry: &constraintRegistry{defs: []llm.ToolDefinition{{Name: "alpha:lookup"}, {Name: "beta:lookup"}}}}
	item := &skillproto.Skill{Frontmatter: skillproto.Frontmatter{Name: "lookup-skill", AllowedTools: "lookup"}, Body: "Look up the requested record."}
	body, _, err := svc.activateResolvedWithContext(context.Background(), item, "")
	require.NoError(t, err)
	require.Contains(t, body, "alpha:lookup, beta:lookup")
	require.Contains(t, body, "ask the user")
	resolved := svc.resolvedToolSkill(context.Background(), item)
	child := deriveDynamicSkillAgent(nil, resolved, body)
	require.Len(t, child.Tool.Items, 2)
	require.Equal(t, "alpha:lookup", child.Tool.Items[0].Name)
	require.Equal(t, "beta:lookup", child.Tool.Items[1].Name)
}

func TestSkillResolutionPreservesEmptyBodyError(t *testing.T) {
	svc := &Service{toolRegistry: &constraintRegistry{defs: []llm.ToolDefinition{{Name: "alpha:lookup"}}}}
	_, _, err := svc.activateResolvedWithContext(context.Background(), &skillproto.Skill{Frontmatter: skillproto.Frontmatter{Name: "empty", AllowedTools: "lookup"}}, "")
	require.ErrorContains(t, err, "empty body")
}
