package tool

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type bodyCaptureClient struct {
	mcpclient.Interface
	called *mcpschema.CallToolRequestParams
}

func (c *bodyCaptureClient) CallTool(_ context.Context, params *mcpschema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.called = params
	return &mcpschema.CallToolResult{}, nil
}

func TestExecutePromotesLegacyBodyOnlyWhenMCPDeclaresItsFields(t *testing.T) {
	for _, tc := range []struct {
		name       string
		properties mcpschema.ToolInputSchemaProperties
		args       map[string]interface{}
		expected   map[string]interface{}
	}{
		{
			name: "declared fields promoted",
			properties: mcpschema.ToolInputSchemaProperties{
				"Field": {"type": "string"}, "Operation": {"type": "string"},
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
			name:       "native Body preserved",
			properties: mcpschema.ToolInputSchemaProperties{"Body": {"type": "object"}, "item": {"type": "string"}},
			args:       map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
			expected:   map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
		},
		{
			name:       "unknown nested field preserved",
			properties: mcpschema.ToolInputSchemaProperties{"item": {"type": "string"}},
			args:       map[string]interface{}{"Body": map[string]interface{}{"item": "x", "other": "y"}},
			expected:   map[string]interface{}{"Body": map[string]interface{}{"item": "x", "other": "y"}},
		},
		{
			name:       "collision preserved",
			properties: mcpschema.ToolInputSchemaProperties{"item": {"type": "string"}},
			args:       map[string]interface{}{"item": "top", "Body": map[string]interface{}{"item": "nested"}},
			expected:   map[string]interface{}{"item": "top", "Body": map[string]interface{}{"item": "nested"}},
		},
		{
			name:     "missing schema preserved",
			args:     map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
			expected: map[string]interface{}{"Body": map[string]interface{}{"item": "x"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := maps.Clone(tc.args)
			client := &bodyCaptureClient{}
			entry := &toolCacheEntry{
				def: llm.ToolDefinition{Name: "platform/TargetingTree"},
				mcpDef: mcpschema.Tool{Name: "TargetingTree", InputSchema: mcpschema.ToolInputSchema{
					Type: "object", Properties: tc.properties,
				}},
			}
			reg := &Registry{
				internal: map[string]mcpclient.Interface{"platform": client},
				cache:    map[string]*toolCacheEntry{"platform/TargetingTree": entry},
			}
			_, err := reg.Execute(context.Background(), "platform/TargetingTree", tc.args)
			require.NoError(t, err)
			require.NotNil(t, client.called)
			require.Equal(t, "TargetingTree", client.called.Name)
			require.Equal(t, tc.expected, client.called.Arguments)
			require.Equal(t, original, tc.args, "caller arguments changed")
		})
	}
}
