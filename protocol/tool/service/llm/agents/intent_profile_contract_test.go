package agents

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	mcpadapter "github.com/viant/agently-core/protocol/tool/adapter/mcp"
	"gopkg.in/yaml.v3"
)

func TestRunInput_PlaybookContractAndLegacyDecode(t *testing.T) {
	for name, raw := range map[string]string{
		"canonical":     `{"playbookId":"product_knowledge"}`,
		"legacy intent": `{"intentProfileId":"product_knowledge"}`,
		"legacy prompt": `{"promptProfileId":"product_knowledge"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var input RunInput
			require.NoError(t, json.Unmarshal([]byte(raw), &input))
			require.Equal(t, "product_knowledge", input.effectiveIntentProfileID())
			encoded, err := json.Marshal(&input)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"playbookId":"product_knowledge"`)
			require.NotContains(t, string(encoded), "intentProfileId")
			require.NotContains(t, string(encoded), "promptProfileId")
		})
	}

	var yamlInput RunInput
	require.NoError(t, yaml.Unmarshal([]byte("promptProfileId: product_knowledge\n"), &yamlInput))
	require.Equal(t, "product_knowledge", yamlInput.effectiveIntentProfileID())
	var conflict RunInput
	require.ErrorContains(t, json.Unmarshal([]byte(`{"playbookId":"a","intentProfileId":"b"}`), &conflict), "conflicting")
}

func TestAgentMCPToolSchemasRetainPlaybookAndLegacyAliasInputs(t *testing.T) {
	tools := mcpadapter.FromService(New(nil))
	for _, method := range []string{"run", "query"} {
		var found bool
		for _, tool := range tools {
			if tool.Name != method {
				continue
			}
			found = true
			for _, field := range []string{"playbookId", "intentProfileId", "promptProfileId"} {
				require.Contains(t, tool.InputSchema.Properties, field, "%s must admit %s before JSON decoding", method, field)
			}
		}
		require.True(t, found, "missing MCP method %s", method)
	}
}
