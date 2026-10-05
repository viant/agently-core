package sdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/sdk/api"
	workspace "github.com/viant/agently-core/service/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestAGUIWorkspaceResourceCommandsReuseFilesystemDomainStore(t *testing.T) {
	client := &backendClient{store: fsstore.New(t.TempDir())}
	ctx := context.Background()
	execute := func(operation string, payload any) any {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		result, handled, err := dispatchAGUIWorkspace(ctx, client, "thread", operation, raw)
		require.NoError(t, err)
		require.True(t, handled)
		return result
	}
	execute("workspace.resource.save", map[string]any{"kind": "agents", "name": "sample", "data": []byte("name: sample\n")})
	result := execute("workspace.resource.get", map[string]any{"kind": "agents", "name": "sample"}).(*GetResourceOutput)
	require.Equal(t, []byte("name: sample\n"), result.Data)
	listing := execute("workspace.resource.list", map[string]any{"kind": "agents"}).(*ListResourcesOutput)
	require.Equal(t, []string{"sample"}, listing.Names)
	exported := execute("workspace.resource.export", map[string]any{"kinds": []string{"agents"}}).(*ExportResourcesOutput)
	require.Len(t, exported.Resources, 1)
	imported := execute("workspace.resource.import", map[string]any{"resources": exported.Resources}).(*ImportResourcesOutput)
	require.Equal(t, 1, imported.Skipped)
	execute("workspace.resource.delete", map[string]any{"kind": "agents", "name": "sample"})
	_, _, err := dispatchAGUIWorkspace(ctx, client, "thread", "workspace.resource.save", json.RawMessage(`{"kind":"../escape","name":"x","data":"eA=="}`))
	require.Error(t, err)
	_, _, err = dispatchAGUIWorkspace(ctx, client, "thread", "workspace.resource.import", json.RawMessage(`{"resources":[{"kind":"agents","name":"../../escape","data":"eA=="}]}`))
	require.Error(t, err)
	for _, op := range []string{"workspace.resource.get", "datasource.fetch", "lookup.registry", "feed.get"} {
		require.Error(t, extensions.ValidateWorkspacePayload(op, []byte(`{"userId":"spoof"}`)))
	}
}
func TestAGUIWorkspaceMetadataUsesConfiguredTypedBinding(t *testing.T) {
	client := &aguiGoalClient{}
	handler := workspace.NewMetadataHandler(nil, nil, "metadata-version")
	ctx := WithAGUIWorkspaceBindings(context.Background(), AGUIWorkspaceBindings{Metadata: handler})
	result, handled, err := dispatchAGUIWorkspace(ctx, client, "thread", "workspace.metadata.get", nil)
	require.NoError(t, err)
	require.True(t, handled)
	metadata := result.(*workspace.MetadataResponse)
	require.Equal(t, "metadata-version", metadata.Version)
	require.NotEmpty(t, metadata.MetadataVersion)
	_, _, err = dispatchAGUIWorkspace(context.Background(), client, "thread", "workspace.metadata.get", nil)
	require.Error(t, err)
	result, _, err = dispatchAGUIWorkspace(ctx, client, "thread", "workspace.publicagents.list", nil)
	require.NoError(t, err)
	require.NotNil(t, result.(*workspace.PublicAgentsResponse).AgentInfos)
}

type workspaceFeedClient struct {
	Client
	calls int
}

func (c *workspaceFeedClient) ListFeedSpecs() []*FeedSpec {
	return []*FeedSpec{{ID: "feed", Title: "Live data", Presentation: &FeedPresentation{Target: "inline"}, Match: FeedMatch{Service: "service", Method: "method"}, DataSource: map[string]any{"source": "data"}, UI: map[string]any{"type": "table"}}}
}
func (c *workspaceFeedClient) GetTranscript(_ context.Context, input *GetTranscriptInput, _ ...TranscriptOption) (*ConversationStateResponse, error) {
	c.calls++
	return &ConversationStateResponse{Feeds: []*api.ActiveFeedState{{FeedID: "feed", Data: json.RawMessage(`{"items":[{"n":9007199254740991}]}`)}}}, nil
}
func TestAGUIFeedCommandsPreserveRichDescriptorsOutsideChat(t *testing.T) {
	client := &workspaceFeedClient{}
	ctx := context.Background()
	result, handled, err := dispatchAGUIWorkspace(ctx, client, "thread", "feed.get", json.RawMessage(`{"id":"feed"}`))
	require.NoError(t, err)
	require.True(t, handled)
	data := result.(map[string]any)
	require.Equal(t, "Live data", data["title"])
	require.Equal(t, map[string]any{"source": "data"}, data["dataSources"])
	require.Equal(t, map[string]any{"type": "table"}, data["ui"])
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `9007199254740991`)
	_, handled, err = dispatchAGUIWorkspace(ctx, client, "thread", "feed.list", nil)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 1, client.calls)
}
