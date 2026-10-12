package schema

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestPromoteLegacyBodyArguments(t *testing.T) {
	for _, tc := range []struct {
		name       string
		properties mcpschema.ToolInputSchemaProperties
		args       map[string]interface{}
		expected   map[string]interface{}
	}{
		{
			name: "declared nested inputs",
			properties: mcpschema.ToolInputSchemaProperties{
				"treeModelParam": {"type": "object"}, "treeLookupParam": {"type": "array"},
			},
			args: map[string]interface{}{
				"Field": "LOCATION", "Operation": "map",
				"Body": map[string]interface{}{
					"treeModelParam":  map[string]interface{}{"advertiserId": 111482, "channels": []interface{}{"DISPLAY"}},
					"treeLookupParam": []interface{}{map[string]interface{}{"namespace": "location", "id": "US/CA"}},
				},
			},
			expected: map[string]interface{}{
				"Field": "LOCATION", "Operation": "map",
				"treeModelParam":  map[string]interface{}{"advertiserId": 111482, "channels": []interface{}{"DISPLAY"}},
				"treeLookupParam": []interface{}{map[string]interface{}{"namespace": "location", "id": "US/CA"}},
			},
		},
		{
			name:       "native Body",
			properties: mcpschema.ToolInputSchemaProperties{"Body": {"type": "object"}, "item": {"type": "string"}},
			args:       map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
			expected:   map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
		},
		{
			name:       "unknown field",
			properties: mcpschema.ToolInputSchemaProperties{"item": {"type": "string"}},
			args:       map[string]interface{}{"Body": map[string]interface{}{"item": "x", "other": "y"}},
			expected:   map[string]interface{}{"Body": map[string]interface{}{"item": "x", "other": "y"}},
		},
		{
			name:       "collision",
			properties: mcpschema.ToolInputSchemaProperties{"item": {"type": "string"}},
			args:       map[string]interface{}{"item": "top", "Body": map[string]interface{}{"item": "nested"}},
			expected:   map[string]interface{}{"item": "top", "Body": map[string]interface{}{"item": "nested"}},
		},
		{
			name:     "no schema",
			args:     map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
			expected: map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := maps.Clone(tc.args)
			got := PromoteLegacyBodyArguments(tc.properties, tc.args)
			require.Equal(t, tc.expected, got)
			require.Equal(t, original, tc.args)
		})
	}
}
