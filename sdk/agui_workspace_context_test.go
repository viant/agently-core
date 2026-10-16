package sdk

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestAGUIWorkspaceHintsPreserveIdentifiersWithoutGrantingPermissions(t *testing.T) {
	query := &agentsvc.QueryInput{UserId: "authenticated-user", ConversationID: "thread"}
	extension := &agui.Extension{Payload: &agui.ExecutionPayload{Context: json.RawMessage(`{"workspace":{"windowId":"orders","title":"Orders","renderer":"forgeWindow","internalTabs":{"details":"activity"},"dataSources":{"records":{"selectedKeys":{"id":9007199254740993},"selectedIds":[{"id":123},{"id":456}]}}}}`)}}
	require.NoError(t, applyAGUIExecution(query, extension, false))
	workspace := query.Context["workspace"].(map[string]any)
	require.Equal(t, "orders", workspace["windowId"])
	require.Equal(t, "Orders", workspace["title"])
	require.Equal(t, "activity", workspace["internalTabs"].(map[string]any)["details"])
	records := workspace["dataSources"].(map[string]any)["records"].(map[string]any)
	require.Equal(t, json.Number("9007199254740993"), records["selectedKeys"].(map[string]any)["id"])
	require.Len(t, records["selectedIds"], 2)
	require.Equal(t, "authenticated-user", query.UserId)
	require.Equal(t, "thread", query.ConversationID)
	require.Empty(t, query.ToolsAllowed)
	require.Empty(t, query.ResourceURIs)
}
