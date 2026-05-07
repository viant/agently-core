package planner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	afsurl "github.com/viant/afs/url"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

func TestStaticContractSchema(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"strategyFamily": map[string]interface{}{"type": "string"},
		},
	}
	contract := NewStaticContract(schema)
	got, err := contract.Schema(context.Background())
	require.NoError(t, err)
	require.Equal(t, "object", got["type"])
	props, ok := got["properties"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, props, "strategyFamily")
}

func TestFileResolverLoadsSchemaNextToPlannerAgent(t *testing.T) {
	root := t.TempDir()
	agentPath := filepath.Join(root, "steward-planner.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte("id: steward_planner\nname: Steward Planner\n"), 0o644))
	schemaPath := filepath.Join(root, "planner.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type":"object","properties":{"strategyFamily":{"type":"string"}}}`), 0o644))
	rulesPath := filepath.Join(root, "planner.validation.json")
	require.NoError(t, os.WriteFile(rulesPath, []byte(`{"rules":[{"kind":"subset","field":"executionOrder","within":"requiredEvidence"}]}`), 0o644))

	resolver := NewFileResolver(afs.New())
	contract, err := resolver.Resolve(context.Background(), &agentmdl.Agent{
		Source: &agentmdl.Source{URL: afsurl.ToFileURL(agentPath)},
	})
	require.NoError(t, err)
	require.NotNil(t, contract)

	schema, err := contract.Schema(context.Background())
	require.NoError(t, err)
	require.Equal(t, "object", schema["type"])

	errs := contract.Validate(context.Background(), Output{
		"requiredEvidence": []string{"baseline"},
		"executionOrder":   []string{"baseline", "confirmation"},
	}, ValidationContext{})
	require.Len(t, errs, 1)
	require.Equal(t, "execution_order_undeclared", errs[0].Code)
}
