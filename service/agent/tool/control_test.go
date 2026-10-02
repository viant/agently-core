package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	intent "github.com/viant/agently-core/protocol/intent"
	mcpname2 "github.com/viant/agently-core/protocol/mcpname"
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
		mcpname2.Canonical("system/os:getEnv"),
		skillproto.ListToolNameCanonical,
		mcpname2.Canonical(skillproto.GetToolName),
		skillproto.ActivateToolNameCanonical,
	}, actual.Tools)
}

func TestMerge_DedupesCaseInsensitiveSelections(t *testing.T) {
	actual := Merge(
		Selection{Bundles: []string{"orchestrator", "ORCHESTRATOR"}, Tools: []string{"prompt:list"}},
		Selection{Bundles: []string{"forecast"}, Tools: []string{"Prompt:List", "workspace/ForecastCube"}},
	)

	assert.Equal(t, []string{"orchestrator", "forecast"}, actual.Bundles)
	assert.Equal(t, []string{
		mcpname2.Canonical("prompt:list"),
		mcpname2.Canonical("workspace/ForecastCube"),
	}, actual.Tools)
}

func TestFromPromptProfile_ActivatesOnlyDeclaredProfileTools(t *testing.T) {
	actual := FromPromptProfile(&intent.Profile{
		ToolBundles:    []string{"practice-control"},
		PreferredTools: []string{"pathwise/practice:select", "PATHWISE/PRACTICE:SELECT"},
	})

	assert.Equal(t, []string{"practice-control"}, actual.Bundles)
	assert.Equal(t, []string{mcpname2.Canonical("pathwise/practice:select")}, actual.Tools)
}
