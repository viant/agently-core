package manager

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/clienthandler"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

func TestCachedRPCClientFencesCallbackAcrossConversationChange(t *testing.T) {
	var callback *clienthandler.Handler
	server, err := mcpserver.New(mcpserver.WithStreamableURI("/mcp"), mcpserver.WithNewHandler(func(ctx context.Context, n transport.Notifier, l logger.Logger, c protocolclient.Operations) (protocol.Handler, error) {
		h := protocol.NewDefaultHandler(n, l, c)
		h.Registry.RegisterToolWithSchema("owned.upload", "Owned upload", schema.ToolInputSchema{Type: "object"}, nil, func(ctx context.Context, r *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			_, rpcErr := c.CreateMessage(ctx, &jsonrpc.TypedRequest[*schema.CreateMessageRequest]{Request: &schema.CreateMessageRequest{}})
			if rpcErr == nil || !strings.Contains(rpcErr.Message, "artifact payload") {
				return nil, jsonrpc.NewInternalError("callback fence absent", nil)
			}
			return &schema.CallToolResult{StructuredContent: map[string]any{"callbackBlocked": true}}, nil
		})
		return h, nil
	}))
	require.NoError(t, err)
	server.UseStreamableHTTP(true)
	peer := httptest.NewServer(server.HTTP(context.Background(), "").Handler)
	defer peer.Close()
	mgr, err := New(&contextBoundProvider{url: peer.URL + "/mcp"}, WithHandlerFactory(func() protocolclient.Handler { callback = clienthandler.New(nil, nil); return callback }))
	require.NoError(t, err)
	defer mgr.CloseConversation("alpha")
	client, err := mgr.Get(context.Background(), "alpha", "owned")
	require.NoError(t, err)
	// This exact cached RPC session still owns the callback handler created for
	// alpha; the tool request is now bound to beta. No wire metadata is added.
	current := requestctx.WithConversationID(context.Background(), "beta")
	cached, err := mgr.Get(current, "alpha", "owned")
	require.NoError(t, err)
	require.Same(t, client, cached)
	fence, ok := cached.(interface{ BeginSensitivePayload() (func(), error) })
	require.True(t, ok)
	end, err := fence.BeginSensitivePayload()
	require.NoError(t, err)
	result, err := cached.CallTool(current, &schema.CallToolRequestParams{Name: "owned.upload", Arguments: map[string]any{"body": "AAEC"}})
	end()
	require.NoError(t, err)
	require.NotNil(t, result)
	ordinaryCallback := callback
	isolated, err := mgr.NewSensitiveClient(current, "beta", "owned")
	require.NoError(t, err)
	isolatedCallback := callback
	_, err = isolated.CallTool(current, &schema.CallToolRequestParams{Name: "owned.upload", Arguments: map[string]any{"body": "AAEC"}})
	require.NoError(t, err)
	isolated.(interface{ Close() }).Close()
	// A late queued callback retains the isolated session's permanent fence.
	_, lateErr := isolatedCallback.CreateMessage(current, nil)
	require.NotNil(t, lateErr)
	require.Contains(t, lateErr.Message, "artifact payload")
	callback = ordinaryCallback
	_, ordinaryErr := callback.CreateMessage(current, nil)
	require.NotNil(t, ordinaryErr)
	require.NotContains(t, ordinaryErr.Message, "artifact payload", "normal callbacks restored after dispatch")
}
