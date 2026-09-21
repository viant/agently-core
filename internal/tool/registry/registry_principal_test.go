package tool

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	memory "github.com/viant/agently-core/runtime/requestctx"
	schema "github.com/viant/mcp-protocol/schema"
	"testing"

	client "github.com/viant/mcp/client"
)

type principalManager struct {
	discoveryManagerStub
	getCalls atomic.Int64
}

func (m *principalManager) Get(ctx context.Context, scope, server string) (client.Interface, error) {
	m.getCalls.Add(1)
	tools := []schema.Tool{}
	if authctx.EffectiveUserID(ctx) == "alice" {
		tools = append(tools, schema.Tool{Name: "secret", InputSchema: schema.ToolInputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{"timeoutMs": {"type": "integer"}}}})
	}
	return &discoveryListClient{tools: tools}, nil
}
func TestProtectedToolDefinitionsNeverEnterSharedCache(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "private")
	mgr := &principalManager{}
	r := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}, discoveryToolsTTL: time.Minute}
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
	require.EqualValues(t, 2, mgr.getCalls.Load(), "one authorized discovery per principal should be reused across definitions/get/match")
}

func TestPrincipalToolDefinitionsCacheSeparatesTokenIdentity(t *testing.T) {
	r := &Registry{discoveryToolsTTL: time.Minute}
	r.storePrincipalDiscoveryTools("private", "alice", "token-a", false, []schema.Tool{{Name: "alpha"}})

	tools, ok := r.loadPrincipalDiscoveryTools("private", "alice", "token-a", false)
	require.True(t, ok)
	require.Len(t, tools, 1)
	require.Equal(t, "alpha", tools[0].Name)

	_, ok = r.loadPrincipalDiscoveryTools("private", "alice", "token-b", false)
	require.False(t, ok)
	_, ok = r.loadPrincipalDiscoveryTools("private", "bob", "token-a", false)
	require.False(t, ok)
}
