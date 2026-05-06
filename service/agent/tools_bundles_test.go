package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	skillsvc "github.com/viant/agently-core/service/skill"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
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
	raw := strings.TrimSpace(pattern)
	switch {
	case strings.HasSuffix(raw, "/*"):
		root := strings.TrimSuffix(raw, "/*")
		service := mcpname.Name(mcpname.Canonical(name)).Service()
		return service == root || strings.HasPrefix(service, root+"/")
	case strings.HasSuffix(raw, ":*"):
		root := strings.TrimSuffix(raw, ":*")
		service := mcpname.Name(mcpname.Canonical(name)).Service()
		return service == root
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
	return mcpname.Canonical(s)
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

type countingRegistry struct {
	fakeRegistry
	matchCalls int
}

func (r *countingRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	r.matchCalls++
	return r.fakeRegistry.MatchDefinition(pattern)
}

func TestResolveTools_WithBundles(t *testing.T) {
	testCases := []struct {
		name        string
		query       *QueryInput
		bundles     []*toolbundle.Bundle
		defs        []llm.ToolDefinition
		expectNames []string
	}{
		{
			name: "runtime_bundle_selection_merges_with_agent_bundles",
			query: &QueryInput{
				ToolBundles: []string{"resources"},
				Agent:       &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "system",
					Match: []llm.Tool{
						{Name: "system/*"},
					},
				},
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
				{Name: "system/os:getEnv"},
			},
			expectNames: []string{canon("resources:list"), canon("resources:read"), canon("system/exec:execute"), canon("system/os:getEnv")},
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
			expectNames: []string{canon("system/exec:execute"), canon("system/os:getEnv")},
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
			expectNames: []string{canon("system/exec:execute"), canon("system/os:getEnv")},
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
			expectNames: []string{canon("system/os:getEnv")},
		},
		{
			name: "analyst_bundle_matches_colon_registry_names",
			query: &QueryInput{
				Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"analyst-agent"}}},
			},
			bundles: []*toolbundle.Bundle{
				{
					ID: "analyst-agent",
					Match: []llm.Tool{
						{Name: "analyst-ResourceTree"},
						{Name: "analyst-SaveDecision"},
					},
				},
			},
			defs: []llm.ToolDefinition{
				{Name: "analyst:ResourceTree"},
				{Name: "analyst:SaveDecision"},
				{Name: "llm/agents:run"},
			},
			expectNames: []string{canon("analyst:ResourceTree"), canon("analyst:SaveDecision")},
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
			expectNames: []string{canon("system/exec:execute")},
		},
		{
			name: "default_system_patch_bundle_excludes_commit_and_rollback",
			query: &QueryInput{
				Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"system/patch"}}},
			},
			bundles: nil,
			defs: []llm.ToolDefinition{
				{Name: "system/patch:apply"},
				{Name: "system/patch:replace"},
				{Name: "system/patch:snapshot"},
				{Name: "system/patch:commit"},
				{Name: "system/patch:rollback"},
			},
			expectNames: []string{canon("system/patch:apply"), canon("system/patch:replace"), canon("system/patch:snapshot")},
		},
		{
			name: "default_scratchpad_bundle_exposes_memory_tools",
			query: &QueryInput{
				Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"scratchpad"}}},
			},
			bundles: nil,
			defs: []llm.ToolDefinition{
				{Name: "scratchpad:memorize"},
				{Name: "scratchpad:append"},
				{Name: "scratchpad:list"},
				{Name: "scratchpad:fetch"},
				{Name: "system/patch:apply"},
			},
			expectNames: []string{canon("scratchpad:append"), canon("scratchpad:fetch"), canon("scratchpad:list"), canon("scratchpad:memorize")},
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
			sort.Strings(got)
			sort.Strings(tc.expectNames)
			assert.EqualValues(t, tc.expectNames, got)
		})
	}
}

func TestResolveTools_CachesStructuredBundleResolution(t *testing.T) {
	registry := &countingRegistry{
		fakeRegistry: fakeRegistry{
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "system/os:getEnv"},
			},
		},
	}
	svc := &Service{
		registry: registry,
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "system",
				Match: []llm.Tool{
					{Name: "system/*"},
				},
			}}, nil
		},
	}
	query := &QueryInput{
		Agent: &agentmdl.Agent{
			Tool: agentmdl.Tool{Bundles: []string{"system"}},
		},
	}

	_, err := svc.resolveTools(context.Background(), query)
	require.NoError(t, err)
	firstCalls := registry.matchCalls
	require.Greater(t, firstCalls, 0)

	_, err = svc.resolveTools(context.Background(), query)
	require.NoError(t, err)
	assert.Equal(t, firstCalls, registry.matchCalls)
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

func TestResolveToolControl_MergesAgentProfileAndRuntimeSelections(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptDir, 0o755))
	profileBody := []byte("id: repo_analysis\nname: Repository Analysis\ndescription: repo analysis profile\ntoolBundles:\n  - profile-tools\nmessages:\n  - role: system\n    text: Delegate repository analysis first.\n")
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "repo_analysis.yaml"), profileBody, 0o644))

	svc := &Service{
		promptRepo: promptrepo.NewWithStore(fsstore.New(tmpDir)),
	}
	actual, err := svc.resolveToolControl(context.Background(), &QueryInput{
		PromptProfileId: "repo_analysis",
		ToolBundles:     []string{"analyst-performance-tools", "analyst-baseline"},
		Agent: &agentmdl.Agent{
			Skills: []string{"forecast"},
			Tool: agentmdl.Tool{
				Bundles: []string{"orchestrator"},
				Items: []*llm.Tool{
					{Name: "system/os:getEnv"},
				},
			},
		},
	})

	require.NoError(t, err)
	assert.EqualValues(t,
		[]string{"orchestrator", "profile-tools", "analyst-performance-tools", "analyst-baseline"},
		actual.Bundles,
	)
	assert.EqualValues(t,
		[]string{mcpname.Canonical("system/os:getEnv"), mcpname.Canonical("llm/skills:list"), mcpname.Canonical("llm/skills:activate")},
		actual.Tools,
	)
}

func TestResolveTools_SkillControlToolsAreNotBundleDriven(t *testing.T) {
	tmpDir := t.TempDir()
	skillRoot := filepath.Join(tmpDir, "skills", "forecast")
	require.NoError(t, os.MkdirAll(skillRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(`---
name: forecast
description: Forecast skill.
allowed-tools: analyst:MetricsCube
---

body
`), 0o644))

	skillSvc := skillsvc.New(&execconfig.Defaults{
		Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(tmpDir, "skills")}},
	}, nil, nil)
	require.NoError(t, skillSvc.Load(context.Background()))

	reg := &fakeRegistry{defs: []llm.ToolDefinition{
		{Name: "llm/skills:list"},
		{Name: "llm/skills:activate"},
		{Name: "prompt:list"},
	}}
	svc := &Service{
		registry: reg,
		skillSvc: skillSvc,
		toolBundles: func(ctx context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "orchestrator",
				Match: []llm.Tool{
					{Name: "prompt:*"},
				},
			}}, nil
		},
	}
	ctx, _ := withWarnings(context.Background())
	actual, err := svc.resolveTools(ctx, &QueryInput{
		Agent: &agentmdl.Agent{
			Skills: []string{"forecast"},
			Tool:   agentmdl.Tool{Bundles: []string{"orchestrator"}},
		},
	})
	require.NoError(t, err)
	var got []string
	for _, tool := range actual {
		got = append(got, tool.Definition.Name)
	}
	sort.Strings(got)
	assert.EqualValues(t, []string{canon("llm/skills:activate"), canon("llm/skills:list"), canon("prompt:list")}, got)
}

func TestResolveTools_DoesNotExposeSkillControlToolsWhenNoVisibleSkills(t *testing.T) {
	reg := &fakeRegistry{defs: []llm.ToolDefinition{
		{Name: "llm/skills:list"},
		{Name: "llm/skills:activate"},
		{Name: "prompt:list"},
	}}
	svc := &Service{
		registry: reg,
		toolBundles: func(ctx context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "orchestrator",
				Match: []llm.Tool{
					{Name: "prompt:*"},
				},
			}}, nil
		},
	}
	ctx, _ := withWarnings(context.Background())
	actual, err := svc.resolveTools(ctx, &QueryInput{
		Agent: &agentmdl.Agent{
			Tool: agentmdl.Tool{Bundles: []string{"orchestrator"}},
		},
	})
	require.NoError(t, err)
	var got []string
	for _, tool := range actual {
		got = append(got, tool.Definition.Name)
	}
	assert.EqualValues(t, []string{canon("prompt:list")}, got)
}
