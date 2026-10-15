package agent

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"testing"
)

func TestIntakeCapabilitiesUseExecutionSurfaceAndSkillMetadataOnly(t *testing.T) {
	registry := &fakeRegistry{defs: []llm.ToolDefinition{
		{Name: "inventory:rank", Description: "Rank catalog items by capacity", Parameters: map[string]interface{}{"schema-internal-marker": true}},
		{Name: "inventory:erase", Description: "Erase inventory"},
	}}
	service := &Service{registry: registry, skillSvc: newLoadedTestSkillService(t, t.TempDir(), "catalog-help")}
	input := &QueryInput{Agent: &agentmdl.Agent{Skills: []string{"catalog-help"}}, ToolsAllowed: []string{"inventory:rank"}}
	summary, err := service.intakeCapabilitySummary(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, summary, "Rank catalog items by capacity")
	require.Contains(t, summary, "catalog-help")
	require.NotContains(t, summary, "erase")
	require.NotContains(t, summary, "schema-internal-marker")
	require.NotContains(t, summary, "body")
	var items []map[string]string
	require.NoError(t, json.Unmarshal([]byte(summary), &items))
	require.Len(t, items, 2)
	require.Equal(t, "skill", items[0]["kind"])
	require.Equal(t, "tool", items[1]["kind"])
	repeated, err := service.intakeCapabilitySummary(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, summary, repeated)
	require.Equal(t, []string{"inventory:rank"}, input.ToolsAllowed, "routing metadata must not expand caller tool permissions")
	require.Empty(t, input.PromptProfileId)
}
