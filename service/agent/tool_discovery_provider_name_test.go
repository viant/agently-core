package agent

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	openaiprovider "github.com/viant/agently-core/genai/llm/provider/openai"
	"github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/mcp"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type punctuatedDiscoveryService struct {
	mcpclient.Interface
	names, called []string
}

func (s *punctuatedDiscoveryService) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	result := &schema.ListToolsResult{}
	for _, name := range s.names {
		result.Tools = append(result.Tools, schema.Tool{Name: name})
	}
	return result, nil
}
func (s *punctuatedDiscoveryService) CallTool(_ context.Context, input *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	s.called = append(s.called, input.Name)
	return &schema.CallToolResult{}, nil
}
func registerPunctuatedDiscoveryClient(t *testing.T, mgr *manager.Manager, client *punctuatedDiscoveryService, ctx context.Context) {
	t.Helper()
	require.NoError(t, mgr.RegisterLocal(ctx, "internal-skills", &config.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "fixture", ToolsListVisibility: config.ToolsListVisibilityPrivate}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
}
func TestDynamicToolListNamesReachOpenAIAndRouteLiteralMethods(t *testing.T) {
	for _, method := range []string{"resource/get", "cursor.number-list", "cursor-number"} {
		t.Run(method, func(t *testing.T) {
			s, ctx, in := tierFixture(t)
			ctx = toolselection.WithState(auth.WithUserInfo(ctx, &auth.UserInfo{Subject: "fixture"}), nil)
			owned := apiconv.NewConversation()
			owned.SetId("conversation")
			owned.SetCreatedByUserID("fixture")
			require.NoError(t, s.conversation.PatchConversations(context.Background(), owned))
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			reg, err := registry.NewWithManager(mgr)
			require.NoError(t, err)
			remote := &punctuatedDiscoveryService{names: []string{method}}
			registerPunctuatedDiscoveryClient(t, mgr, remote, ctx)
			require.NoError(t, reg.AddInternalService(toolservice.New()))
			s.registry = reg
			s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
				return []*toolbundle.Bundle{{ID: "discovery", Match: []llm.Tool{{Name: "llm/tools:list", AlwaysInclude: true}}}}, nil
			}
			// The punctuated tool is absent from the initial surface and all bundles.
			before, err := s.buildToolSignatures(ctx, in)
			require.NoError(t, err)
			canonical := mcpname.Canonical("internal-skills:" + method)
			require.NotContains(t, signatureNames(before), canonical)
			list, _ := toolservice.New().Method("list")
			output := &toolservice.ListOutput{}
			require.NoError(t, list(ctx, &toolservice.ListInput{Query: "internal-skills", Limit: 1}, output))
			require.Len(t, output.Tools, 1)
			// Use the catalog's returned alias, not a reconstructed server/method.
			canonical = output.Tools[0].Name
			names := finalTierNames(t, s, ctx, in)
			require.Contains(t, names, canonical)
			definitions, err := s.buildToolSignatures(ctx, in)
			require.NoError(t, err)
			tools := make([]llm.Tool, 0, len(definitions))
			for _, definition := range definitions {
				tools = append(tools, llm.Tool{Type: "function", Definition: *definition})
			}
			request, err := (&openaiprovider.Client{}).ToRequestContext(ctx, &llm.GenerateRequest{Options: &llm.Options{Tools: tools}})
			require.NoError(t, err)
			valid := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
			for _, tool := range request.Tools {
				require.Regexp(t, valid, tool.Function.Name)
			}
			_, err = reg.Execute(ctx, canonical, map[string]interface{}{})
			require.NoError(t, err)
			require.Equal(t, []string{method}, remote.called, "alias must route to the exact advertised punctuation")
		})
	}
}
func TestDynamicToolNormalizationCollisionFailsClosed(t *testing.T) {
	s, ctx, in := tierFixture(t)
	ctx = toolselection.WithState(auth.WithUserInfo(ctx, &auth.UserInfo{Subject: "fixture"}), nil)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	remote := &punctuatedDiscoveryService{names: []string{"resource/get", "resource_get"}}
	registerPunctuatedDiscoveryClient(t, mgr, remote, ctx)
	s.registry = reg
	s.toolBundles = nil
	_, err = s.discoverableToolDefinitions(ctx, in)
	require.ErrorContains(t, err, "ambiguous advertised MCP tool alias")
	_, err = reg.Execute(ctx, mcpname.Canonical("internal-skills:resource/get"), nil)
	require.ErrorContains(t, err, "ambiguous advertised MCP tool alias")
	require.Empty(t, remote.called)
}
