package bundle

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"gopkg.in/yaml.v3"
)

func TestTrustedToolTiersYAMLValidationAndResolverMetadata(t *testing.T) {
	var bundle Bundle
	require.NoError(t, yaml.Unmarshal([]byte(`id: fixture
match:
 - {name: 'service:*', tier: onDemand, alwaysInclude: true, exclude: ['service:blocked']}
 - {name: 'service:read', tier: critical}
`), &bundle))
	require.NoError(t, bundle.Validate())
	definitions := map[string]*llm.ToolDefinition{"service:read": {Name: "service:read"}, "service:blocked": {Name: "service:blocked"}}
	result := ResolveDefinitionsWithOptions(&bundle, func(pattern string) []*llm.ToolDefinition {
		if pattern == "service:*" {
			return []*llm.ToolDefinition{definitions["service:read"], definitions["service:blocked"]}
		}
		if definition := definitions[pattern]; definition != nil {
			return []*llm.ToolDefinition{definition}
		}
		return nil
	})
	require.Len(t, result.Definitions, 1)
	require.True(t, result.Definitions[0].AlwaysInclude)
	require.Equal(t, llm.ToolTierCritical, result.Definitions[0].Tier)
	require.False(t, definitions["service:read"].AlwaysInclude, "resolver cannot annotate shared source schemas")
	require.Empty(t, definitions["service:read"].Tier)
	for _, tier := range []llm.ToolTier{"", llm.ToolTierBase, llm.ToolTierCritical, llm.ToolTierOnDemand} {
		bundle.Match[0].Tier = tier
		require.NoError(t, bundle.Validate())
	}
	bundle.Match[0].Tier = "critical-ish"
	require.ErrorContains(t, bundle.Validate(), "invalid tool tier")
}
