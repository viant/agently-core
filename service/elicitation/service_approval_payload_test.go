package elicitation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agent/execution"
)

func TestEnrichApprovalPayload(t *testing.T) {
	req := &execution.Elicitation{}
	req.RequestedSchema.Properties = map[string]interface{}{
		"_approvalMeta": map[string]interface{}{
			"type": "string",
			"const": `{
				"toolName":"system/os:getEnv",
				"title":"OS Env Access",
				"editors":[
					{
						"name":"names",
						"kind":"checkbox_list",
						"options":[
							{"id":"HOME","label":"HOME","selected":true},
							{"id":"SHELL","label":"SHELL","selected":true},
							{"id":"PATH","label":"PATH","selected":true}
						]
					}
				]
			}`,
		},
	}

	payload := map[string]interface{}{
		"editedFields": map[string]interface{}{
			"names": []interface{}{"HOME", "PATH"},
		},
	}

	got := enrichApprovalPayload(payload, req)
	decision, ok := got["approvalDecision"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "tool_approval", decision["type"])
	require.Equal(t, "system/os:getEnv", decision["toolName"])
	require.Equal(t, true, decision["isPartial"])

	fields, ok := decision["fields"].(map[string]interface{})
	require.True(t, ok)
	names, ok := fields["names"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, []string{"HOME", "PATH"}, names["approved"])
	require.Equal(t, []string{"SHELL"}, names["denied"])
}
