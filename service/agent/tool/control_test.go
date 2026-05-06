package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

func TestFromAgent_IncludesSkillControlToolsWhenSkillsVisible(t *testing.T) {
	agent := &agentmdl.Agent{
		Skills: []string{"forecast"},
		Tool: agentmdl.Tool{
			Bundles: []string{"orchestrator"},
			Items: []*llm.Tool{
				{Name: "system/os:getEnv"},
			},
		},
	}

	actual := FromAgent(agent)

	assert.Equal(t, []string{"orchestrator"}, actual.Bundles)
	assert.Equal(t, []string{
		mcpname.Canonical("system/os:getEnv"),
		skillproto.ListToolNameCanonical,
		skillproto.ActivateToolNameCanonical,
	}, actual.Tools)
}

func TestMerge_DedupesCaseInsensitiveSelections(t *testing.T) {
	actual := Merge(
		Selection{Bundles: []string{"orchestrator", "ORCHESTRATOR"}, Tools: []string{"prompt:list"}},
		Selection{Bundles: []string{"forecast"}, Tools: []string{"Prompt:List", "steward/ForecastingCube"}},
	)

	assert.Equal(t, []string{"orchestrator", "forecast"}, actual.Bundles)
	assert.Equal(t, []string{
		mcpname.Canonical("prompt:list"),
		mcpname.Canonical("steward/ForecastingCube"),
	}, actual.Tools)
}
