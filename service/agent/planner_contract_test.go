package agent

import planner "github.com/viant/agently-core/service/planner"
import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

func testPlannerContractResolver() planner.Resolver {
	return planner.NewStaticResolver(planner.NewStaticContract(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"strategyFamily": map[string]interface{}{"type": "string"},
			"baseProfiles": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"toolBundles": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"templateId": map[string]interface{}{"type": "string"},
			"requiredEvidence": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"executionOrder": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"finalizationGuards": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"narrationPolicy": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"baselineFirst":      map[string]interface{}{"type": "boolean"},
					"persistMidTurnNote": map[string]interface{}{"type": "boolean"},
				},
				"required":             []string{"baselineFirst", "persistMidTurnNote"},
				"additionalProperties": false,
			},
			"workspaceExtensions": map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{},
				"required":             []string{},
				"additionalProperties": false,
			},
			"parallelToolCalls": map[string]interface{}{"type": "boolean"},
		},
		"required": []string{
			"strategyFamily",
			"baseProfiles",
			"toolBundles",
			"templateId",
			"requiredEvidence",
			"executionOrder",
			"finalizationGuards",
			"narrationPolicy",
			"workspaceExtensions",
			"parallelToolCalls",
		},
		"additionalProperties": false,
	}))
}

func mustResolveTestPlannerContract(t *testing.T, resolver planner.Resolver, agent *agentmdl.Agent) planner.Contract {
	t.Helper()
	contract, err := resolver.Resolve(context.Background(), agent)
	require.NoError(t, err)
	require.NotNil(t, contract)
	return contract
}
