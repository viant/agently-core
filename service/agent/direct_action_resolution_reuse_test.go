package agent

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	intakesvc "github.com/viant/agently-core/service/intake"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type directActionIdentityReads struct {
	*registry.Registry
	reads int
}

func (r *directActionIdentityReads) ToolSurfaceCacheIdentity(ctx context.Context, pattern string) (string, string) {
	r.reads++
	return r.Registry.ToolSurfaceCacheIdentity(ctx, pattern)
}

func TestDirectActionPolicyReusesEquivalentNativeMatches(t *testing.T) {
	ctx := manager.WithToolCatalogReadSnapshot(manager.WithNamesMemo(cacheUser("reader", "synthetic-token", "example-chat")))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	remote := &punctuatedDiscoveryService{names: []string{"read", "blocked", "nested_read"}}
	require.NoError(t, mgr.RegisterLocal(ctx, "inventory", &config.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "synthetic", ToolsListVisibility: config.ToolsListVisibilityPrivate}, func(context.Context) (mcpclient.Interface, error) { return remote, nil }))
	native, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	// Exercise the actual registry first: these authored spellings return the
	// same definitions from the manager-owned authenticated catalog.
	var expected []string
	for _, pattern := range []string{"inventory-*", "inventory.*", "inventory/*", "inventory:*"} {
		defs, err := native.MatchDefinitionWithContextResult(ctx, pattern)
		require.NoError(t, err)
		var names []string
		for _, def := range defs {
			names = append(names, mcpname.Canonical(def.Name))
		}
		sort.Strings(names)
		if expected == nil {
			expected = names
		} else {
			require.Equal(t, expected, names)
		}
	}
	require.Len(t, expected, 3)
	counted := &directActionIdentityReads{Registry: native}
	svc := &Service{registry: counted, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "allowed", Match: []llm.Tool{{Name: "inventory:*", Exclude: []string{"inventory:blocked"}}, {Name: "inventory/*", Exclude: []string{"inventory/blocked"}}}}}, nil
	}}
	input := &QueryInput{Agent: &agentmdl.Agent{Intake: agentmdl.Intake{}}}
	input.Agent.Intake.Tool.Bundles = []string{"allowed"}
	allowed, err := svc.directActionAllowedToolNames(ctx, &input.Agent.Intake)
	require.NoError(t, err)
	require.Contains(t, allowed, mcpname.Canonical("inventory:read"))
	require.Contains(t, allowed, mcpname.Canonical("inventory:nested_read"))
	require.NotContains(t, allowed, mcpname.Canonical("inventory:blocked"))
	require.Equal(t, 4, counted.reads, "one existing before/after catalog identity read per distinct include/exclude match")
	require.Error(t, svc.authorizeDirectAction(ctx, input, &intakesvc.DirectActionContext{ToolName: "inventory:blocked"}))
	require.NoError(t, svc.authorizeDirectAction(ctx, input, &intakesvc.DirectActionContext{ToolName: "inventory:read"}))
	require.Greater(t, counted.reads, 4, "a later policy resolution rechecks native catalog identity")
}

func TestDirectActionPolicyStillPropagatesDistinctPatternFailure(t *testing.T) {
	svc, reg := selectedSchemaService()
	input := &QueryInput{Agent: &agentmdl.Agent{Intake: agentmdl.Intake{}}}
	input.Agent.Intake.Tool.Bundles = []string{"base"}
	err := svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{ToolName: "local:run"})
	require.True(t, errors.Is(err, reg.broken))
}

func TestBundleEquivalentPatternFailureRemainsObservable(t *testing.T) {
	svc, _ := selectedSchemaService()
	calls := 0
	failure := errors.New("synthetic catalog failure")
	_, err := svc.resolveBundleResultWithMatcher(context.Background(), []string{"required"}, func(context.Context, string) ([]*llm.ToolDefinition, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return []*llm.ToolDefinition{{Name: "remote:query"}}, nil
	})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 2, calls, "failed lookup is not memoized; its equivalent spelling retries the native match")
}

func TestBundleMatchReusePreservesLiteralNamespaceNames(t *testing.T) {
	svc := &Service{registry: &fakeRegistry{}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "both", Match: []llm.Tool{{Name: "stock-a:*"}, {Name: "stock/a:*"}}}}, nil
	}}
	result, err := svc.resolveBundleResultWithMatcher(context.Background(), []string{"both"}, func(_ context.Context, pattern string) ([]*llm.ToolDefinition, error) {
		name := "stock/a:right"
		if strings.Contains(pattern, "stock-a") {
			name = "stock-a:left"
		}
		return []*llm.ToolDefinition{{Name: name}}, nil
	})
	require.NoError(t, err)
	var names []string
	for _, definition := range result.Definitions {
		names = append(names, mcpname.Canonical(definition.Name))
	}
	require.ElementsMatch(t, []string{mcpname.Canonical("stock-a:left"), mcpname.Canonical("stock/a:right")}, names)
}
