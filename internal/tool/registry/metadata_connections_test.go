package tool

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestMetadataTransportExcludedButPublishedDataConnectionRemains(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "reader"})
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	var metadataCalls, dataCalls atomic.Int64
	for _, name := range []string{"metadata", "data"} {
		require.NoError(t, mgr.RegisterLocal(ctx, name, &config.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "same-producer", ToolsListVisibility: config.ToolsListVisibilityPrivate}, func(context.Context) (mcpclient.Interface, error) {
			if name == "metadata" {
				metadataCalls.Add(1)
				return nil, jsonrpc.NewHTTPStatusError(http.StatusForbidden)
			}
			dataCalls.Add(1)
			return &fakeClient{}, nil
		}))
	}
	reg, err := NewWithManager(mgr)
	require.NoError(t, err)
	reg.cache["metadata:obsolete"] = &toolCacheEntry{def: llm.ToolDefinition{Name: "metadata:obsolete", SourceMCPServer: "metadata"}}
	reg.RegisterMetadataConnections("metadata", "mcp://workspace/metadata")
	defs := reg.DefinitionsWithContext(ctx)
	require.NotEmpty(t, defs)
	for _, d := range defs {
		require.NotEqual(t, "metadata", d.SourceMCPServer)
	}
	for _, name := range []string{"metadata", "mcp://workspace/metadata"} {
		tools, err := reg.listServerTools(ctx, name)
		require.NoError(t, err)
		require.Empty(t, tools)
	}
	require.Zero(t, metadataCalls.Load())
	require.Positive(t, dataCalls.Load())
	require.Empty(t, reg.LastWarnings())
}
