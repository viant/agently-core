package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/service/core"
)

type continuationFinder struct{}

func (continuationFinder) Find(context.Context, string) (llm.Model, error) {
	return continuationModel{}, nil
}

type continuationModel struct{}

func (continuationModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return nil, nil
}

func (continuationModel) Implements(feature string) bool {
	return feature == base.SupportsContextContinuation
}

func TestService_BuildToolSignatures_WithBundles(t *testing.T) {
	testCases := []struct {
		name        string
		input       *QueryInput
		bundles     []*toolbundle.Bundle
		defs        []llm.ToolDefinition
		expectNames []string
	}{
		{
			name: "bundles_only_includes_signatures",
			input: &QueryInput{
				Agent: &agentmdl.Agent{
					Tool: agentmdl.Tool{Bundles: []string{"system"}},
				},
			},
			bundles: []*toolbundle.Bundle{
				{ID: "system", Match: []llm.Tool{{Name: "system/*"}}},
			},
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "system/os:getEnv"},
				{Name: "resources:read"},
			},
			expectNames: []string{"system_exec-execute", "system_os-getEnv"},
		},
		{
			name: "no_tool_config_returns_empty",
			input: &QueryInput{
				Agent: &agentmdl.Agent{},
			},
			bundles:     nil,
			defs:        []llm.ToolDefinition{{Name: "system/exec:execute"}},
			expectNames: nil,
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
			actual, err := svc.buildToolSignatures(context.Background(), tc.input)
			assert.EqualValues(t, nil, err)
			var got []string
			for _, d := range actual {
				got = append(got, d.Name)
			}
			assert.EqualValues(t, tc.expectNames, got)
		})
	}
}

func TestDedupeToolDefinitions_DedupesCanonicalAliases(t *testing.T) {
	input := []*llm.ToolDefinition{
		{Name: "llm_agents-run"},
		{Name: "llm/agents:run"},
		{Name: "system_exec-execute"},
	}

	actual := dedupeToolDefinitions(input)
	var got []string
	for _, item := range actual {
		if item == nil {
			continue
		}
		got = append(got, item.Name)
	}

	assert.EqualValues(t, []string{"llm_agents-run", "system_exec-execute"}, got)
}

func TestFilterDelegationDiscoveryTools_RemovesAgentsListWhenDirectoryDocPresent(t *testing.T) {
	defs := []*llm.ToolDefinition{
		{Name: "llm/agents:list"},
		{Name: "llm/agents:run"},
		{Name: "system/exec:execute"},
	}
	docs := &binding.Documents{
		Items: []*binding.Document{
			{SourceURI: "internal://llm/agents/list"},
		},
	}
	filtered := filterDelegationDiscoveryTools(defs, docs)
	var got []string
	for _, def := range filtered {
		if def == nil {
			continue
		}
		got = append(got, def.Name)
	}
	assert.EqualValues(t, []string{"llm/agents:run", "system/exec:execute"}, got)
}

func TestEnsureInternalToolsIfNeeded_SkipsMessageToolsForCapabilityAgent(t *testing.T) {
	reg := &fakeRegistry{
		defs: []llm.ToolDefinition{
			{Name: "message/show"},
			{Name: "message/match"},
			{Name: "message/summarize"},
			{Name: "message/remove"},
		},
	}
	svc := &Service{
		registry: reg,
		llm:      core.New(continuationFinder{}, reg, nil),
	}
	binding := &binding.Binding{Model: "openai_gpt-5.4"}
	input := &QueryInput{
		AgentID: "agent_selector",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "agent_selector", Name: "Agent Selector"},
		},
	}

	svc.ensureInternalToolsIfNeeded(context.Background(), input, binding)

	assert.Empty(t, binding.Tools.Signatures)
}

func TestBuildToolSignatures_ExposesMessageShowOnlyWhenOverflowDetected(t *testing.T) {
	reg := &fakeRegistry{
		defs: []llm.ToolDefinition{
			{Name: "message/show"},
			{Name: "message/match"},
			{Name: "message/summarize"},
			{Name: "message/remove"},
		},
	}
	svc := &Service{
		registry: reg,
		llm:      core.New(continuationFinder{}, reg, nil),
	}

	withOverflow := &binding.Binding{
		Model: "openai_gpt-5.4",
		Flags: binding.Flags{
			HasMessageOverflow: true,
		},
	}
	svc.ensureInternalToolsIfNeeded(context.Background(), &QueryInput{
		Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "steward", Name: "Steward"}},
	}, withOverflow)
	var overflowNames []string
	for _, sig := range withOverflow.Tools.Signatures {
		if sig == nil {
			continue
		}
		overflowNames = append(overflowNames, sig.Name)
	}
	assert.Contains(t, overflowNames, "message-show")
	assert.Contains(t, overflowNames, "message-match")

	withoutOverflow := &binding.Binding{
		Model: "openai_gpt-5.4",
	}
	svc.ensureInternalToolsIfNeeded(context.Background(), &QueryInput{
		Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "steward", Name: "Steward"}},
	}, withoutOverflow)
	var normalNames []string
	for _, sig := range withoutOverflow.Tools.Signatures {
		if sig == nil {
			continue
		}
		normalNames = append(normalNames, sig.Name)
	}
	assert.NotContains(t, normalNames, "message-show")
	assert.NotContains(t, normalNames, "message-match")
	assert.NotContains(t, normalNames, "message-summarize")
	assert.NotContains(t, normalNames, "message-remove")
}
