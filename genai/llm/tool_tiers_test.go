package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestProducerSchemasCannotSupplyTrustedToolTierOrRetention(t *testing.T) {
	var definition ToolDefinition
	require.NoError(t, json.Unmarshal([]byte(`{"name":"external:read","tier":"critical","alwaysInclude":true}`), &definition))
	require.Empty(t, definition.Tier)
	require.False(t, definition.AlwaysInclude)
	tool := &mcpschema.Tool{Name: "external:read", Meta: map[string]interface{}{"tier": "critical", "alwaysInclude": true}}
	converted := ToolDefinitionFromMcpTool(tool)
	require.Empty(t, converted.Tier)
	require.False(t, converted.AlwaysInclude)
	definition.Tier = ToolTierCritical
	definition.AlwaysInclude = true
	wire, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "critical")
	require.NotContains(t, string(wire), "alwaysInclude")
}
