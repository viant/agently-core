package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
)

type fakeRegistry struct {
	defs []llm.ToolDefinition
}

func (r *fakeRegistry) Definitions() []llm.ToolDefinition { return r.defs }

func (r *fakeRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	pattern = strings.TrimSpace(pattern)
	var out []*llm.ToolDefinition
	for i := range r.defs {
		name := r.defs[i].Name
		if matchPattern(pattern, name) {
			d := r.defs[i]
			out = append(out, &d)
		}
	}
	return out
}

func matchPattern(pattern, name string) bool {
	if strings.TrimSpace(pattern) == "" {
		return false
	}
	pcanon := canon(pattern)
	ncanon := canon(name)
	if pcanon == "*" {
		return true
	}
	if pcanon == ncanon {
		return true
	}
	if strings.Contains(pcanon, "*") {
		prefix := strings.TrimSuffix(pcanon, "*")
		return strings.HasPrefix(ncanon, prefix)
	}
	// service-only pattern
	raw := strings.TrimSpace(pattern)
	if raw != "" && !strings.Contains(raw, ":") && !strings.Contains(raw, "*") {
		return strings.HasPrefix(ncanon, pcanon)
	}
	return false
}

func canon(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ReplaceAll(mcpname.Canonical(s), "-", "_")
}

func (r *fakeRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	target := canon(name)
	for i := range r.defs {
		if canon(r.defs[i].Name) == target {
			def := r.defs[i]
			return &def, true
		}
	}
	return nil, false
}
func (r *fakeRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *fakeRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (r *fakeRegistry) SetDebugLogger(io.Writer)                 {}
func (r *fakeRegistry) Initialize(context.Context)               {}
func (r *fakeRegistry) ToolTimeout(string) (time.Duration, bool) { return 0, false }

func TestResolveTools_WithBundles(t *testing.T) {
	testCases := []struct {
		name        string
		query       *QueryInput
		bundles     []*toolbundle.Bundle
		defs        []llm.ToolDefinition
		expectNames []string
	}{
		{
			name: "runtime_bundle_selection_expands_tools",
			query: &QueryInput{
				ToolBundles: []string{"resources"},
				Agent:       &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "resources",
					Match: []llm.Tool{
						{Name: "resources/*", Exclude: []string{"resources:matchDocuments"}},
					},
				},
			},
			defs: []llm.ToolDefinition{
				{Name: "resources:read"},
				{Name: "resources:list"},
				{Name: "resources:matchDocuments"},
				{Name: "system/exec:execute"},
			},
			expectNames: []string{"resources:list", "resources:read"},
		},
		{
			name: "agent_bundle_selection_used_when_no_runtime_override",
			query: &QueryInput{
				Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "system",
					Match: []llm.Tool{
						{Name: "system/*"},
					},
				},
			},
			defs: []llm.ToolDefinition{
				{Name: "resources:read"},
				{Name: "system/exec:execute"},
				{Name: "system/os:getEnv"},
			},
			expectNames: []string{"system/exec:execute", "system/os:getEnv"},
		},
		{
			name: "empty_tools_allowed_does_not_disable_bundle_resolution",
			query: &QueryInput{
				ToolsAllowed: []string{},
				Agent:        &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "system",
					Match: []llm.Tool{
						{Name: "system/*"},
					},
				},
			},
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "system/os:getEnv"},
			},
			expectNames: []string{"system/exec:execute", "system/os:getEnv"},
		},
		{
			name: "explicit_tools_allowed_override_agent_bundles",
			query: &QueryInput{
				ToolsAllowed: []string{"system/os:getEnv"},
				Agent:        &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "system",
					Match: []llm.Tool{
						{Name: "system/*"},
					},
				},
			},
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "system/os:getEnv"},
			},
			expectNames: []string{"system/os:getEnv"},
		},
		{
			name: "steward_bundle_matches_colon_registry_names",
			query: &QueryInput{
				Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"steward-agent"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "steward-agent",
					Match: []llm.Tool{
						{Name: "steward-AdHierarchy"},
						{Name: "steward-SaveRecommendation"},
					},
				},
			},
			defs: []llm.ToolDefinition{
				{Name: "steward:AdHierarchy"},
				{Name: "steward:SaveRecommendation"},
				{Name: "llm/agents:run"},
			},
			expectNames: []string{"steward:AdHierarchy", "steward:SaveRecommendation"},
		},
		{
			name: "service_style_bundle_id_falls_back_to_direct_definition_match",
			query: &QueryInput{
				Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system/exec"}}},
			},
			bundles: nil,
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "system/os:getEnv"},
			},
			expectNames: []string{"system/exec:execute"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeRegistry{defs: tc.defs}
			svc := &Service{
				registry: reg,
				toolBundles: func(ctx context.Context) ([]*toolbundle.Bundle, error) {
					return tc.bundles, nil
				},
			}
			ctx, _ := withWarnings(context.Background())
			actual, err := svc.resolveTools(ctx, tc.query)
			assert.EqualValues(t, nil, err)
			var got []string
			for _, t := range actual {
				got = append(got, t.Definition.Name)
			}
			assert.EqualValues(t, tc.expectNames, got)
		})
	}
}

func TestResolveBundleDefinitions_WithPromptApprovalBundle(t *testing.T) {
	reg := &fakeRegistry{defs: []llm.ToolDefinition{{Name: "system/os:getEnv"}}}
	svc := &Service{
		registry: reg,
		toolBundles: func(ctx context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "system",
				Match: []llm.Tool{{
					Name: "system/os:*",
					Approval: &llm.ApprovalConfig{
						Mode: llm.ApprovalModePrompt,
					},
				}},
			}}, nil
		},
	}

	ctx := toolapprovalqueue.WithState(context.Background())
	actual, err := svc.resolveBundleDefinitions(ctx, []string{"system"})

	if assert.NoError(t, err) {
		assert.Len(t, actual, 1)
		cfg, ok := toolapprovalqueue.ConfigFor(ctx, "system/os:getEnv")
		if assert.True(t, ok) {
			assert.NotNil(t, cfg)
			assert.True(t, cfg.IsPrompt())
		}
	}
}
