package auth_test

import (
	"github.com/stretchr/testify/require"
	toolmcp "github.com/viant/agently-core/protocol/tool/adapter/mcp"
	"github.com/viant/agently-core/service/auth"
	"testing"
)

func TestWorkspaceToolPinParametersUseObjectSchema(t *testing.T) {
	for _, tool := range toolmcp.FromService(auth.NewWorkspaceTools(nil)) {
		if tool.Name != "update" {
			continue
		}
		properties := tool.InputSchema.Properties
		pin := properties["pin"]["properties"].(map[string]map[string]interface{})
		require.Equal(t, "object", pin["parameters"]["type"])
		require.Contains(t, properties["groupId"]["description"], "Required stable ID for create_group")
		return
	}
	t.Fatal("workspace update tool missing")
}
