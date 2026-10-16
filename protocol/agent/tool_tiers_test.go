package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"gopkg.in/yaml.v3"
)

func TestAgentTrustedToolRuleTierYAML(t *testing.T) {
	var agent Agent
	require.NoError(t, yaml.Unmarshal([]byte("tool:\n  items:\n    - {name: 'arbitrary:lookup', tier: critical, alwaysInclude: true}\n"), &agent))
	require.NoError(t, agent.Validate())
	require.Equal(t, llm.ToolTierCritical, agent.Tool.Items[0].Tier)
	require.True(t, agent.Tool.Items[0].AlwaysInclude)
	agent.Tool.Items[0].Tier = "unknown"
	require.ErrorContains(t, agent.Validate(), "invalid tool tier")
}
