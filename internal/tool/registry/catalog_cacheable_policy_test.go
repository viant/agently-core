package tool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	config "github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

// Independently constructed catalogs exercise policy preparation at realistic
// cardinality without retaining any source schemas or deployment settings.
type catalogPolicyManager struct {
	discoveryManagerStub
	reads atomic.Int64
}

func (m *catalogPolicyManager) Names(context.Context) ([]string, error) {
	return []string{"service"}, nil
}
func (m *catalogPolicyManager) Options(ctx context.Context, server string) (*config.MCPClient, error) {
	m.reads.Add(1)
	return &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate, Cacheable: map[string]bool{"service:method0": authctx.EffectiveUserID(ctx) == "alice"}}, nil
}

func TestCatalogCacheablePolicyPreparationIsBoundedAndActorScoped(t *testing.T) {
	tools := make([]schema.Tool, 300)
	for i := range tools {
		tools[i] = schema.Tool{Name: fmt.Sprintf("method%d", i)}
	}
	mgr := &catalogPolicyManager{discoveryManagerStub: discoveryManagerStub{getFunc: func(string, string) (client.Interface, error) { return &discoveryListClient{tools: tools}, nil }}}
	reg := &Registry{mgr: mgr}
	for _, actor := range []string{"alice", "bob"} {
		before := mgr.reads.Load()
		ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: actor})
		definitions := reg.rawDefinitionsWithContext(ctx)
		require.Len(t, definitions, len(tools))
		require.Less(t, mgr.reads.Load()-before, int64(25), "policy reads must not scale with the number of tools")
		found := false
		for _, definition := range definitions {
			if definition.Name == "service:method0" {
				found = true
				require.Equal(t, actor == "alice", definition.Cacheable)
			}
		}
		require.True(t, found)
	}
	require.Empty(t, reg.cache, "private definitions stay outside the shared registry cache")
}

func TestCatalogCacheablePolicyKeepsInternalPrecedenceDuringRefresh(t *testing.T) {
	mgr := &catalogPolicyManager{}
	reg := &Registry{mgr: mgr, internalCacheable: map[string]map[string]bool{"service": {"method0": false}}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	tools := []schema.Tool{{Name: "method0"}, {Name: "method1"}, {Name: "method2"}}
	for _, replace := range []func(string, []schema.Tool){reg.mergeServerTools, reg.replaceServerTools} {
		before := mgr.reads.Load()
		replace("service", tools)
		require.LessOrEqual(t, mgr.reads.Load()-before, int64(3))
		require.False(t, reg.cache["service/method0"].def.Cacheable)
	}
	apply := reg.cacheableOverride(ctx, "service")
	// Internal annotations override the configured policy and need no lookup.
	definition := reg.cache["service/method0"].def
	before := mgr.reads.Load()
	apply(&definition)
	require.Equal(t, before, mgr.reads.Load())
	require.False(t, definition.Cacheable)
}
