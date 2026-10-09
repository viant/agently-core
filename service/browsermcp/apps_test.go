package browsermcp

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"testing"
)

func TestBrowserCatalogOfficialUIVisibilityAndResourceMetadata(t *testing.T) {
	registry := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		return []cfg.BrowserDescriptor{{Name: "device", AllowedTools: []string{"*"}}}, nil
	})
	input := Registration{ConversationID: "conversation", ThreadID: "thread", Server: "device", ConnectionID: "connection", Resources: true, Tools: []Tool{
		{Name: "show", InputSchema: json.RawMessage(`{"type":"object"}`), Meta: json.RawMessage(`{"ui":{"resourceUri":"ui://device/app"},"private":"discard"}`)},
		{Name: "refresh", InputSchema: json.RawMessage(`{"type":"object"}`), Meta: json.RawMessage(`{"ui":{"visibility":["app"]}}`)},
	}}
	catalog, err := registry.Register(context.Background(), "owner", input)
	require.NoError(t, err)
	require.True(t, catalog.Resources)
	require.Contains(t, string(catalog.Tools[0].Metadata), "ui://device/app")
	require.NotContains(t, string(catalog.Tools[0].Metadata), "private")
	_, err = registry.Validate(context.Background(), "owner", "thread", []agui.Tool{catalog.Tools[0]})
	require.NoError(t, err)
	_, err = registry.Validate(context.Background(), "owner", "thread", []agui.Tool{catalog.Tools[1]})
	require.Error(t, err, "app-only tools cannot enter the model tool surface")
	input.Tools[0].Meta = json.RawMessage(`{"ui":{"resourceUri":"https://other/app"}}`)
	_, err = registry.Register(context.Background(), "owner", input)
	require.Error(t, err)
	input.Tools[0].Meta = json.RawMessage(`{"ui":{"visibility":["administrator"]}}`)
	_, err = registry.Register(context.Background(), "owner", input)
	require.Error(t, err)
}
