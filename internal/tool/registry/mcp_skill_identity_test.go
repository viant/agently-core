package tool_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	skillproto "github.com/viant/agently-core/protocol/skill"
	toolproto "github.com/viant/agently-core/protocol/tool"
	skills "github.com/viant/agently-core/service/skill"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type skillIdentityProvider struct{ private bool }

func (p *skillIdentityProvider) Options(context.Context, string) (*config.MCPClient, error) {
	visibility := config.ToolsListVisibilityPublic
	if p.private {
		visibility = config.ToolsListVisibilityPrivate
	}
	return &config.MCPClient{ToolsListVisibility: visibility}, nil
}

type skillIdentityClient struct {
	client.Interface
	tools []schema.Tool
}

func (c *skillIdentityClient) Initialize(context.Context, ...client.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{}, nil
}
func (c *skillIdentityClient) ListTools(context.Context, *string, ...client.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: c.tools}, nil
}
func (c *skillIdentityClient) CallTool(context.Context, *schema.CallToolRequestParams, ...client.RequestOption) (*schema.CallToolResult, error) {
	return &schema.CallToolResult{}, nil
}

func skillIdentityRegistry(t *testing.T, private bool) *registry.Registry {
	t.Helper()
	t.Setenv("AGENTLY_MCP_SERVERS", "datly,other")
	manager, err := manager.New(&skillIdentityProvider{private: private}, manager.WithClientFactory(func(ctx context.Context, _, _ string) (client.Interface, error) {
		tools := []schema.Tool{{Name: "datly.components"}, {Name: "literal-with_under.parts"}}
		if private && authctx.EffectiveUserID(ctx) != "alice" {
			tools = nil
		}
		return &skillIdentityClient{tools: tools}, nil
	}))
	require.NoError(t, err)
	reg, err := registry.NewWithManager(manager)
	require.NoError(t, err)
	if !private {
		reg.Initialize(context.Background())
	}
	return reg
}
func skillIdentityContext(reg toolproto.Registry, user string) context.Context {
	service := skills.New(nil, nil, nil)
	service.SetToolRegistry(toolproto.WithConversation(reg, "owned-skill-conversation"))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: user})
	c := skills.BuildConstraints([]*skillproto.Skill{{CatalogURI: "mcp://datly/skills/reader", ServerID: "datly", Body: "owned reader skill"}})
	return skills.WithRuntimeState(skills.WithConstraints(ctx, c), service, &agent.Agent{}, nil)
}
func TestRemoteSkillUsesDiscoveredSameServerIdentity(t *testing.T) {
	reg := skillIdentityRegistry(t, false)
	ctx := skillIdentityContext(reg, "alice")
	same := mcpname.Canonical("datly/datly.components")
	require.NoError(t, skills.ValidateExecution(ctx, same, nil))
	require.NoError(t, skills.ValidateExecution(ctx, "datly/datly.components", nil))
	require.NoError(t, skills.ValidateExecution(ctx, mcpname.Canonical("datly/literal-with_under.parts"), nil))
	for _, name := range []string{mcpname.Canonical("other/datly.components"), "system/exec:execute", "datly/unknown.tool"} {
		require.ErrorContains(t, skills.ValidateExecution(ctx, name, nil), "cross-origin")
	}
	require.NoError(t, skills.ValidateExecution(ctx, "llm/skills:list", nil))
	require.NoError(t, skills.ValidateExecution(ctx, "llm/skills:get", nil))
	defs := []*llm.ToolDefinition{{Name: "datly/datly.components"}, {Name: "other/datly.components"}, {Name: "system/exec:execute"}}
	filtered, _ := skills.ExpandDefinitionsForConstraintsWithContext(ctx, defs, toolproto.WithConversation(reg, "owned-skill-conversation"), &skills.Constraints{RemoteServers: []string{"datly"}})
	require.Len(t, filtered, 1)
	require.Equal(t, "datly/datly.components", filtered[0].Name)
}
func TestRemoteSkillCatalogIdentityPreservesPrincipalIsolation(t *testing.T) {
	reg := skillIdentityRegistry(t, true)
	alice := skillIdentityContext(reg, "alice")
	require.NotEmpty(t, reg.MatchDefinitionWithContext(alice, "datly:*"))
	name := mcpname.Canonical("datly/datly.components")
	require.NoError(t, skills.ValidateExecution(alice, name, nil))
	require.ErrorContains(t, skills.ValidateExecution(skillIdentityContext(reg, "bob"), name, nil), "cross-origin")
	require.ErrorContains(t, skills.ValidateExecution(skillIdentityContext(reg, ""), name, nil), "cross-origin")
}
