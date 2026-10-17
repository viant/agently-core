package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
)

func preparedDiscoveryFixture() (*Service, *QueryInput, []llm.ToolDefinition, []*toolbundle.Bundle) {
	var definitions []llm.ToolDefinition
	for _, name := range []string{"analytics/query:read", "analytics/query:readMore", "analytics/query:secret", "other:lookup"} {
		definitions = append(definitions, llm.ToolDefinition{Name: name, Description: "Neutral operation", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"filter": map[string]interface{}{"type": "string"}}}, OutputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"result": map[string]interface{}{"type": "array"}}}, Required: []string{"filter"}, Strict: true, Cacheable: true, Tier: llm.ToolTierBase, DiscoveryNamespace: "untrusted-producer-value", SourceMCPServer: "connection", SourceResourceURI: "mcp://analysis/catalog", SourceProviderIdentity: "catalog-owner"})
	}
	bundles := []*toolbundle.Bundle{
		{ID: "broad", Match: []llm.Tool{{Name: "analytics/query:*", Exclude: []string{"analytics/query:secret"}, Tier: llm.ToolTierOnDemand}}},
		{ID: "retained", Match: []llm.Tool{{Name: "analytics/query.read", AlwaysInclude: true, Tier: llm.ToolTierCritical}, {Name: "analytics/query-read", AlwaysInclude: true, Tier: llm.ToolTierCritical}}},
	}
	service := &Service{registry: &fakeRegistry{defs: definitions}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) { return bundles, nil }}
	input := &QueryInput{Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "neutral"}, Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "other:lookup", Tier: llm.ToolTierBase}}}}}
	return service, input, definitions, bundles
}

func TestPreparedDiscoveryPreservesCompleteMetadataAndSourceInputs(t *testing.T) {
	for _, allowSecret := range []bool{false, true} {
		t.Run(map[bool]string{false: "excluded", true: "trusted-union"}[allowSecret], func(t *testing.T) {
			s, input, definitions, bundles := preparedDiscoveryFixture()
			_, beforeInput, beforeDefinitions, beforeBundles := preparedDiscoveryFixture()
			if allowSecret {
				bundles[1].Match = append(bundles[1].Match, llm.Tool{Name: "analytics/query:secret"})
				beforeBundles[1].Match = append(beforeBundles[1].Match, llm.Tool{Name: "analytics/query:secret"})
			}
			actual, err := s.discoverableToolDefinitions(context.Background(), input)
			require.NoError(t, err)
			var expected []llm.ToolDefinition
			for _, definition := range beforeDefinitions {
				if definition.Name == "analytics/query:secret" && !allowSecret {
					continue
				}
				definition.Name = mcpname.Canonical(definition.Name)
				definition.DiscoveryNamespace = ""
				if definition.Name == "analytics_query-read" || definition.Name == "analytics_query-readMore" {
					definition.Tier = llm.ToolTierCritical
					definition.AlwaysInclude = true
				}
				expected = append(expected, definition)
			}
			require.Equal(t, expected, actual, "all schema, source and retention metadata must survive")
			require.Equal(t, beforeDefinitions, definitions)
			require.Equal(t, beforeBundles, bundles)
			require.Equal(t, beforeInput, input)
		})
	}
}

func TestPreparedDiscoveryUsesFreshRulesCatalogAndCallerScope(t *testing.T) {
	s, input, _, bundles := preparedDiscoveryFixture()
	ctx := context.Background()
	first, err := s.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Len(t, first, 3)
	// A later invocation must recompute exclusion decisions from the current rules.
	bundles[0].Match[0].Exclude = nil
	updated, err := s.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Len(t, updated, 4)
	// A new authorized catalog with the same name carries its current schema.
	reg := s.registry.(*fakeRegistry)
	reg.defs = []llm.ToolDefinition{{Name: "other:lookup", Description: "Current definition", Parameters: map[string]interface{}{"current": true}}}
	input.ToolsAllowed = []string{"other:lookup"}
	updated, err = s.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Len(t, updated, 1)
	require.Equal(t, "Current definition", updated[0].Description)
	require.Equal(t, map[string]interface{}{"current": true}, updated[0].Parameters)
	input.ToolsAllowed = []string{"not-available:read"}
	updated, err = s.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Empty(t, updated)
}
