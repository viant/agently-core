package tool

import (
	"context"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/mcp"
	"testing"
)

func TestOrdinaryMetadataScopeReusesUserAcrossConversations(t *testing.T) {
	mgr := &metadataScopeManager{}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr = mgr
	user := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	first := requestctx.WithConversationID(user, "first")
	second := requestctx.WithConversationID(user, "second")
	a := reg.discoveryClientScope(first, "ordinary", "token-a", false)
	require.Equal(t, a, reg.discoveryClientScope(second, "ordinary", "token-a", false))
	require.NotEqual(t, a, reg.discoveryClientScope(second, "ordinary", "token-b", false))
	bob := authctx.WithUserInfo(second, &authctx.UserInfo{Subject: "bob"})
	require.NotEqual(t, a, reg.discoveryClientScope(bob, "ordinary", "token-a", false))
	require.Equal(t, reg.discoveryClientScope(first, "ordinary", "", true), reg.discoveryClientScope(bob, "ordinary", "", true))
	require.Equal(t, "first", requestctx.ConversationIDFromContext(first), "discovery must not rewrite the execution context")
}

type metadataScopeManager struct{ outputSchemaManager }

func (m *metadataScopeManager) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://example.test/mcp"}}}}, nil
}
