package tool

import (
	"context"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	memory "github.com/viant/agently-core/runtime/requestctx"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	"testing"
)

type principalManager struct{ discoveryManagerStub }

func (m *principalManager) Get(ctx context.Context, scope, server string) (client.Interface, error) {
	tools := []schema.Tool{}
	if authctx.EffectiveUserID(ctx) == "alice" {
		tools = append(tools, schema.Tool{Name: "secret", InputSchema: schema.ToolInputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{"timeoutMs": {"type": "integer"}}}})
	}
	return &discoveryListClient{tools: tools}, nil
}
func TestProtectedToolDefinitionsNeverEnterSharedCache(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "private")
	r := &Registry{mgr: &principalManager{}, cache: map[string]*toolCacheEntry{}}
	alice := memory.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "same-conversation")
	bob := memory.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), "same-conversation")
	require.Len(t, r.DefinitionsWithContext(alice), 1)
	_, ok := r.GetDefinitionWithContext(alice, "private:secret")
	require.True(t, ok)
	require.Len(t, r.MatchDefinitionWithContext(alice, "private:*"), 1)
	require.Empty(t, r.cache)
	_, cancel, args := r.applyTimeoutMs(alice, "private:secret", map[string]interface{}{"timeoutMs": 1000})
	if cancel != nil {
		defer cancel()
	}
	require.Contains(t, args, "timeoutMs")
	require.Empty(t, r.DefinitionsWithContext(bob))
	_, ok = r.GetDefinitionWithContext(bob, "private:secret")
	require.False(t, ok)
	require.Empty(t, r.MatchDefinitionWithContext(bob, "private:*"))
	require.Empty(t, r.Definitions())
	require.Empty(t, r.cache)
}
