package intake

import (
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"testing"
)

func TestPlannerEnabledIntakeExposesExplicitRoutingOutcome(t *testing.T) {
	cfg := &agentmdl.Intake{PlannerEnabled: true, Scope: []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile}}
	schema := buildOutputJSONSchema(cfg)
	props := schema["properties"].(map[string]interface{})
	routing := props["routing"].(map[string]interface{})
	mode := routing["properties"].(map[string]interface{})["mode"].(map[string]interface{})
	require.Equal(t, []string{ModeRoute, ModePlanner, ModeClarify}, mode["enum"])
	require.Contains(t, schema["required"], "routing")
	require.Contains(t, buildOutputSchema(cfg), "An empty profile does not select the planner")
	for _, value := range []string{ModeRoute, ModePlanner, ModeClarify} {
		var result Context
		require.NoError(t, unmarshalContext([]byte(`{"routing":{"mode":"`+value+`"},"prompting":{"suggestedProfileId":""}}`), &result))
		require.Equal(t, value, result.Routing.Mode)
		require.Empty(t, result.Prompting.SuggestedProfileID)
	}
	cfg.PlannerEnabled = false
	require.NotContains(t, buildOutputJSONSchema(cfg)["properties"], "routing", "legacy agents keep their existing output contract")
}
