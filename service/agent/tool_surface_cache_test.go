package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/tool/bundle"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	agenttool "github.com/viant/agently-core/service/agent/tool"
	"github.com/viant/agently-core/service/browsermcp"
)

func snapshotSurfaceKeys(cache *sync.Map) []interface{} {
	var keys []interface{}
	cache.Range(func(key, value interface{}) bool { keys = append(keys, key); return true })
	return keys
}

type scopedCacheRegistry struct {
	fakeRegistry
	calls      int
	kind       string
	generation int
}

func (r *scopedCacheRegistry) ToolSurfaceCacheIdentity(ctx context.Context, pattern string) (string, string) {
	if r.kind == "private" {
		return r.kind, fmt.Sprintf("%s/%s/%d", authctx.EffectiveUserID(ctx), authctx.Bearer(ctx), r.generation)
	}
	return "public", fmt.Sprint(r.generation)
}

func (r *scopedCacheRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	r.calls++
	return r.fakeRegistry.MatchDefinition(pattern)
}

func cacheUser(user, token, conversation string) context.Context {
	ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: user}), token)
	return requestctx.WithConversationID(ctx, conversation)
}

func TestPublicSurfaceReuseAcrossUsersAndBrowserCatalogChanges(t *testing.T) {
	r := &scopedCacheRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "backend-read", Description: "original"}}}}
	s := &Service{registry: r}
	first, err := s.matchDefinitionsResult(cacheUser("alice", "a", "one"), "backend:*")
	require.NoError(t, err)
	require.Len(t, first, 1)
	first[0].Description = "caller mutation"
	ctx := browsermcp.WithDefinitions(cacheUser("bob", "b", "two"), map[string]llm.ToolDefinition{"device-read": {Name: "device-read"}})
	second, err := s.matchDefinitionsResult(ctx, "backend:*")
	require.NoError(t, err)
	require.Equal(t, 1, r.calls)
	require.Equal(t, "original", second[0].Description)
	r.generation++
	_, err = s.matchDefinitionsResult(ctx, "backend:*")
	require.NoError(t, err)
	require.Equal(t, 2, r.calls)
}

func TestPrivateSurfaceCacheSeparatesUserCredentialAndCatalogGeneration(t *testing.T) {
	r := &scopedCacheRegistry{kind: "private", fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "backend-read"}}}}
	s := &Service{registry: r}
	for _, ctx := range []context.Context{cacheUser("alice", "a", "one"), cacheUser("alice", "a", "two"), cacheUser("bob", "b", "three"), cacheUser("alice", "new", "four")} {
		_, err := s.matchDefinitionsResult(ctx, "backend:*")
		require.NoError(t, err)
	}
	require.Equal(t, 3, r.calls)
	require.Empty(t, snapshotSurfaceKeys(&s.publicToolSurfaceCache))
}

func TestBrowserSurfaceCacheSeparatesConversationAndLeavesBackendWarm(t *testing.T) {
	r := &scopedCacheRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "backend-read"}}}}
	s := &Service{registry: r}
	_, err := s.matchDefinitionsResult(context.Background(), "backend:*")
	require.NoError(t, err)
	for _, conversation := range []string{"one", "one", "two"} {
		ctx := browsermcp.WithDefinitions(cacheUser("alice", "a", conversation), map[string]llm.ToolDefinition{"device-read": {Name: "device-read", Description: "catalog1"}})
		_, err = s.matchDefinitionsResult(ctx, "device:*")
		require.NoError(t, err)
		_, err = s.matchDefinitionsResult(ctx, "backend:*")
		require.NoError(t, err)
	}
	require.Equal(t, 1, r.calls)
	require.Len(t, snapshotSurfaceKeys(&s.browserToolSurfaceCache), 2)
	ctx := browsermcp.WithDefinitions(cacheUser("alice", "a", "one"), map[string]llm.ToolDefinition{"device-read": {Name: "device-read", Description: "catalog2"}})
	_, err = s.matchDefinitionsResult(ctx, "device:*")
	require.NoError(t, err)
	require.Len(t, snapshotSurfaceKeys(&s.browserToolSurfaceCache), 3)
}

func TestSurfaceCacheExpires(t *testing.T) {
	s := &Service{}
	s.publicToolSurfaceCache.Store("expired", &catalogSurfaceEntry{expiresAt: time.Now().Add(-time.Second)})
	_, ok := loadCatalogSurface(&s.publicToolSurfaceCache, "expired")
	require.False(t, ok)
}

func TestBrowserBundleOnlySelectionUsesUpdatedCatalogAndKeepsBackendCache(t *testing.T) {
	r := &scopedCacheRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "backend-read"}}}}
	s := &Service{registry: r, toolBundles: func(context.Context) ([]*bundle.Bundle, error) {
		return []*bundle.Bundle{{ID: "mixed", Match: []llm.Tool{{Name: "backend:*"}, {Name: "device:*"}}}}, nil
	}}
	control := agenttool.Selection{Bundles: []string{"mixed"}}
	ctx := cacheUser("alice", "a", "thread")
	firstCalls, firstBrowserKeys := 0, 0
	for i, description := range []string{"old schema", "new schema", "new schema"} {
		scoped := browsermcp.WithDefinitions(ctx, map[string]llm.ToolDefinition{"device-read": {Name: "device-read", Description: description}})
		definitions, err := s.resolveStructuredToolDefinitionsOnce(scoped, control)
		require.NoError(t, err)
		require.Len(t, definitions, 2)
		for _, definition := range definitions {
			if definition.Name == "device-read" {
				require.Equal(t, description, definition.Description)
			}
		}
		if i == 0 {
			firstCalls = r.calls
			firstBrowserKeys = len(snapshotSurfaceKeys(&s.browserToolSurfaceCache))
		}
	}
	require.Positive(t, firstCalls)
	require.Equal(t, firstCalls, r.calls, "a browser catalog change must not recompute backend patterns")
	require.Len(t, snapshotSurfaceKeys(&s.browserToolSurfaceCache), 2*firstBrowserKeys, "unchanged browser generation should reuse its entries")
	_, err := s.resolveStructuredToolDefinitionsOnce(ctx, control)
	require.NoError(t, err)
	// Without a validated browser catalog the old browser definitions cannot
	// escape through the backend or selection caches.
	definitions, err := s.resolveStructuredToolDefinitionsOnce(ctx, control)
	require.NoError(t, err)
	for _, definition := range definitions {
		require.NotEqual(t, "device-read", definition.Name)
	}
}
