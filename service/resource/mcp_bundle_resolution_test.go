package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/protocol/tool/bundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type bundleCatalogClient struct {
	mcpclient.Interface
	tools []schema.Tool
	calls []string
}

func (c *bundleCatalogClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: c.tools}, nil
}
func (c *bundleCatalogClient) CallTool(_ context.Context, request *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.calls = append(c.calls, request.Name)
	return &schema.CallToolResult{}, nil
}

const bundleConnectionURI = "mcp://inventory/nested/connection"
const bundleSeparateConnectionURI = "mcp://inventory/nested_connection"

func publishedBundleCatalog(t *testing.T, separateTools []schema.Tool) (context.Context, *registry.Registry, *bundleCatalogClient) {
	t.Helper()
	actor, verify := PublicReadActors()
	internal := &referenceCatalog{namespace: "workspace", owner: "configuration", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	publisher := &referenceCatalog{namespace: "inventory", owner: "inventory-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}, uriByName: map[string]string{"logical": "mcp://inventory/nested/connection", "separate": "mcp://inventory/nested_connection"}}
	internal.set("", "publisher", referenceConfig("publisher", "inventory-owner", cfg.ResourceCacheUser))
	for _, name := range []string{"logical", "separate"} {
		definition := referenceConfig("handshake-only", "", cfg.ResourceCacheUser)
		definition.ToolsListVisibility = cfg.ToolsListVisibilityPrivate
		publisher.set("alice", name, definition)
	}
	remote := &bundleCatalogClient{tools: []schema.Tool{{Name: "ReadItem"}, {Name: "literal.tool"}, {Name: "nested/tool:operation"}}}
	separate := &bundleCatalogClient{tools: separateTools}
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoStateStore(fsstore.NewStateStore(t.TempDir()))), manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) {
		switch name {
		case "publisher":
			return referenceClient(t, publisher)
		case "mcp://inventory/nested/connection":
			return remote, nil
		case "mcp://inventory/nested_connection":
			return separate, nil
		default:
			return nil, fmt.Errorf("unexpected connection %q", name)
		}
	}))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	require.NoError(t, mgr.RegisterLocal(context.Background(), "configuration", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "configuration", ResourceCacheScopes: map[string]string{"workspace": cfg.ResourceCachePublic}}, func(context.Context) (mcpclient.Interface, error) { return referenceClient(t, internal) }))
	gateway := NewGateway(mgr, actor, verify, "bundle-reader")
	t.Cleanup(func() { gateway.Close() })
	reader, err := NewMCPReferenceReader(gateway, MCPDefinitionSource{ConnectionName: "configuration", ProviderIdentity: "configuration", Namespace: "workspace"}, []identity.ResourceRef{{URI: "mcp://workspace/publisher"}})
	require.NoError(t, err)
	_, err = mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	names, err := reader.List(ctx)
	require.NoError(t, err)
	require.Contains(t, names, "mcp://inventory/nested/connection")
	require.Contains(t, names, bundleSeparateConnectionURI)
	return ctx, reg, remote
}

func TestPublishedMCPConnectionBundlePreservesURIAndLiteralMethods(t *testing.T) {
	for _, method := range []string{"ReadItem", "literal.tool", "nested/tool:operation"} {
		for _, warm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/warm=%t", method, warm), func(t *testing.T) {
				ctx, reg, remote := publishedBundleCatalog(t, []schema.Tool{{Name: "OtherItem"}})
				exact := bundleConnectionURI + ":" + method
				if warm {
					defs, err := reg.MatchDefinitionWithContextResult(ctx, exact)
					require.NoError(t, err)
					require.Len(t, defs, 1)
				}
				var failures []error
				resolved := bundle.ResolveDefinitions(&bundle.Bundle{ID: "items", Match: []llm.Tool{{Name: exact}}}, func(pattern string) []*llm.ToolDefinition {
					definitions, err := reg.MatchDefinitionWithContextResult(ctx, pattern)
					if err != nil {
						failures = append(failures, err)
					}
					return definitions
				})
				require.Empty(t, failures, "bundle expansion must not invent additional connection names")
				require.Len(t, resolved, 1)
				require.Equal(t, bundleConnectionURI+"/"+method, resolved[0].Name)
				require.Equal(t, bundleConnectionURI, resolved[0].SourceMCPServer)
				_, err := reg.ExecuteExactJSON(ctx, exact, map[string]interface{}{}, "")
				require.NoError(t, err)
				require.Equal(t, []string{method}, remote.calls)
			})
		}
	}
}

func TestPublishedMCPConnectionBundleLookupBoundaries(t *testing.T) {
	// These distinct resource URIs share a model alias, not a catalog identity.
	require.Equal(t, mcpname.Canonical(bundleConnectionURI+":ReadItem"), mcpname.Canonical(bundleSeparateConnectionURI+":ReadItem"))
	for _, tc := range []struct {
		name           string
		rule           llm.Tool
		separateTools  []schema.Tool
		warm           []string
		ambiguousAlias bool
		wantServer     string
		wantMethods    []string
		wantErrors     int
	}{
		{
			name:        "wildcard_with_exclusion",
			rule:        llm.Tool{Name: bundleConnectionURI + ":*", Exclude: []string{bundleConnectionURI + ":literal.tool"}},
			wantMethods: []string{"ReadItem", "nested/tool:operation"},
		},
		{
			name: "absent_method",
			rule: llm.Tool{Name: bundleConnectionURI + ":Absent"},
		},
		{
			name:        "absent_exclusion",
			rule:        llm.Tool{Name: bundleConnectionURI + ":ReadItem", Exclude: []string{bundleConnectionURI + ":Absent"}},
			wantMethods: []string{"ReadItem"},
		},
		{
			name:           "literal_URI_despite_ambiguous_alias",
			rule:           llm.Tool{Name: bundleConnectionURI + ":ReadItem"},
			separateTools:  []schema.Tool{{Name: "ReadItem"}, {Name: "OtherItem"}},
			warm:           []string{bundleConnectionURI + ":ReadItem", bundleSeparateConnectionURI + ":ReadItem"},
			ambiguousAlias: true,
			wantMethods:    []string{"ReadItem"},
		},
		{
			name: "absent_method_in_observed_colliding_catalog",
			rule: llm.Tool{Name: bundleConnectionURI + ":OtherItem"},
			warm: []string{bundleSeparateConnectionURI + ":OtherItem"},
		},
		{
			name:       "absent_server",
			rule:       llm.Tool{Name: "mcp://inventory/absent:ReadItem"},
			wantErrors: 1,
		},
		{
			name:        "separate_literal_URI",
			rule:        llm.Tool{Name: bundleSeparateConnectionURI + ":OtherItem"},
			wantServer:  bundleSeparateConnectionURI,
			wantMethods: []string{"OtherItem"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := tc.separateTools
			if tools == nil {
				tools = []schema.Tool{{Name: "OtherItem"}}
			}
			ctx, reg, _ := publishedBundleCatalog(t, tools)
			for _, pattern := range tc.warm {
				defs, err := reg.MatchDefinitionWithContextResult(ctx, pattern)
				require.NoError(t, err)
				require.Len(t, defs, 1)
			}
			if tc.ambiguousAlias {
				_, _, _, err := reg.ResolveMCPIdentity(ctx, mcpname.Canonical(tc.rule.Name))
				require.ErrorContains(t, err, "ambiguous advertised MCP tool alias")
			}
			var failures []error
			var lookups []string
			resolved := bundle.ResolveDefinitions(&bundle.Bundle{Match: []llm.Tool{tc.rule}}, func(pattern string) []*llm.ToolDefinition {
				lookups = append(lookups, pattern)
				defs, err := reg.MatchDefinitionWithContextResult(ctx, pattern)
				if err != nil {
					failures = append(failures, err)
				}
				return defs
			})
			wantLookups := append([]string{}, tc.rule.Exclude...)
			wantLookups = append(wantLookups, tc.rule.Name)
			require.Equal(t, wantLookups, lookups)
			require.Len(t, failures, tc.wantErrors)
			if tc.wantErrors != 0 {
				require.ErrorContains(t, failures[0], "MCP definition not found")
			}
			server := tc.wantServer
			if server == "" {
				server = bundleConnectionURI
			}
			var wantNames, names []string
			for _, method := range tc.wantMethods {
				wantNames = append(wantNames, server+"/"+method)
			}
			for _, def := range resolved {
				names = append(names, def.Name)
				require.Equal(t, server, def.SourceMCPServer)
			}
			require.ElementsMatch(t, wantNames, names)
		})
	}
}
