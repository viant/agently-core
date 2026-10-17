package intake

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/xeipuuv/gojsonschema"
)

func TestDiscoveryNamespaceSchemaKeepsExactToolsSeparateAndLegacyOptional(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{Scope: []string{"tools"}}
	legacy := svc.buildOutputJSONSchema(context.Background(), cfg, WithToolNames([]string{"remote-read"}))
	legacyProps := legacy["properties"].(map[string]interface{})["prompting"].(map[string]interface{})["properties"].(map[string]interface{})
	require.NotContains(t, legacyProps, "discoveryNamespaces", "no namespace metadata preserves the old output contract")
	schema := svc.buildGenerateInputWithContext(context.Background(), "model", "instructions", "request", "user", cfg, WithToolNames([]string{"remote-read"}), WithDiscoveryNamespaces([]string{"analysis", "planning"})).Options.OutputSchema
	for _, tc := range []struct {
		name         string
		hints, tools []string
		valid        bool
	}{
		{"namespace plus empty initial tools", []string{"analysis"}, []string{}, true},
		{"all domains remain valid hints", []string{"analysis", "planning"}, []string{"remote-read"}, true},
		{"optional empty hints", []string{}, []string{"remote-read"}, true},
		{"invalid domain", []string{"stale"}, []string{}, false},
		{"namespace is not a tool name", []string{"analysis"}, []string{"analysis"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := map[string]interface{}{"prompting": map[string]interface{}{"appendToolBundles": []string{}, "discoveryNamespaces": tc.hints, "toolSelection": ToolSelection{Names: tc.tools, Complete: true}}}
			result, err := gojsonschema.Validate(gojsonschema.NewGoLoader(schema), gojsonschema.NewGoLoader(value))
			require.NoError(t, err)
			require.Equal(t, tc.valid, result.Valid(), "%v", result.Errors())
		})
	}
}

func TestDiscoveryNamespaceValidationNeverChangesToolOrWorkflowChoices(t *testing.T) {
	tc := &Context{Prompting: PromptingContext{DiscoveryNamespaces: []string{"stale", "analysis", "analysis", "planning"}, ToolSelection: &ToolSelection{Names: []string{"remote-read"}, Complete: true}, AppendToolBundles: []string{"workflow"}}}
	validateDiscoveryNamespaces(tc, []string{"analysis"})
	require.Equal(t, []string{"analysis"}, tc.Prompting.DiscoveryNamespaces)
	require.Equal(t, &ToolSelection{Names: []string{"remote-read"}, Complete: true}, tc.Prompting.ToolSelection)
	require.Equal(t, []string{"workflow"}, tc.Prompting.AppendToolBundles)
	validateDiscoveryNamespaces(tc, nil)
	require.Empty(t, tc.Prompting.DiscoveryNamespaces, "missing/stale metadata removes hints without rejecting the request")
	require.True(t, tc.Prompting.ToolSelection.Complete)
}

func TestDiscoveryNamespaceWireAndContextCopies(t *testing.T) {
	original, err := parseOutput(`{"prompting":{"discoveryNamespaces":["analysis"],"toolSelection":{"names":[],"complete":true}}}`)
	require.NoError(t, err)
	wire, err := canonicalIntakeContent(original)
	require.NoError(t, err)
	require.Contains(t, wire, "discoveryNamespaces")
	again, err := parseOutput(wire)
	require.NoError(t, err)
	require.Equal(t, original.Prompting.DiscoveryNamespaces, again.Prompting.DiscoveryNamespaces)
	_, copy := StoreCallerProvided(nil, original)
	copy.Prompting.DiscoveryNamespaces[0] = "changed"
	require.Equal(t, []string{"analysis"}, original.Prompting.DiscoveryNamespaces)
	filterByScope(again, &agentmdl.Intake{Scope: []string{"profile"}})
	require.Nil(t, again.Prompting.DiscoveryNamespaces)
	old, err := parseOutput(`{"prompting":{"toolSelection":{"names":["remote-read"],"complete":true}}}`)
	require.NoError(t, err)
	require.Nil(t, old.Prompting.DiscoveryNamespaces)
	wire, err = canonicalIntakeContent(old)
	require.NoError(t, err)
	var value map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(wire), &value))
	require.NotContains(t, value["prompting"], "discoveryNamespaces")
}
