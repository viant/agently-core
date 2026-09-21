package skill

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	proto "github.com/viant/agently-core/protocol/skill"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	"testing"
)

type legacySkillFixture struct {
	client.Interface
	names    []string
	subjects []string
	entry    schema.Skill
}

func (f *legacySkillFixture) ListTools(context.Context, *string, ...client.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: defaultSkillListTool}, {Name: defaultSkillGetTool}}}, nil
}

func (f *legacySkillFixture) CallTool(ctx context.Context, p *schema.CallToolRequestParams, opts ...client.RequestOption) (*schema.CallToolResult, error) {
	f.names = append(f.names, p.Name)
	f.subjects = append(f.subjects, authctx.EffectiveUserID(ctx))
	var value interface{} = map[string]interface{}{"skill": f.entry}
	if p.Name == "legacy_list" || p.Name == defaultSkillListTool {
		value = map[string]interface{}{"skills": []schema.Skill{f.entry}}
	}
	data, _ := json.Marshal(value)
	var object map[string]interface{}
	_ = json.Unmarshal(data, &object)
	return &schema.CallToolResult{StructuredContent: object}, nil
}
func TestLegacyToolBridgePreservesPrincipalAndMetadata(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: x\n---\nbody"}
	legacy := &legacySkillFixture{entry: fixture.entry()}
	bridge := &toolSkillBridge{Interface: legacy, tools: cfg.SkillToolBridge{List: "legacy_list", Get: "legacy_get"}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	list, err := bridge.ListSkills(ctx, nil)
	require.NoError(t, err)
	require.Len(t, list.Skills, 1)
	require.Equal(t, "private", string(list.CacheScope))
	got, err := bridge.GetSkill(ctx, list.Skills[0].Uri)
	require.NoError(t, err)
	require.Equal(t, list.Skills[0], got.Skill)
	require.Equal(t, []string{"legacy_list", "legacy_get"}, legacy.names)
	require.Equal(t, []string{"alice", "alice"}, legacy.subjects)
}

func TestLegacyToolBridgeAutoDiscoversConventionalTools(t *testing.T) {
	legacy := &legacySkillFixture{}
	resolved, err := resolveSkillToolBridge(context.Background(), legacy, &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, nil)
	require.NoError(t, err)
	require.Equal(t, cfg.SkillToolBridge{List: defaultSkillListTool, Get: defaultSkillGetTool}, resolved)
	legacy.names = nil
	legacy.entry = (&skillMCPFixture{body: "---\nname: review\ndescription: Review\n---\nBody"}).entry()
	bridge := &toolSkillBridge{Interface: legacy, tools: resolved}
	listed, err := bridge.ListSkills(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, listed.Skills, 1)
	require.Equal(t, []string{defaultSkillListTool}, legacy.names)
}

type fallbackCapabilityClient struct{ *legacySkillFixture }

func (c *fallbackCapabilityClient) Initialize(context.Context, ...client.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{ProtocolVersion: "2026-07-28"}, nil
}
func (c *fallbackCapabilityClient) Discover(context.Context, ...client.RequestOption) (*schema.DiscoverResult, error) {
	return &schema.DiscoverResult{Capabilities: schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}}}, nil
}

type fallbackCapabilitySource struct{ cli client.Interface }

func (s *fallbackCapabilitySource) Names(context.Context) ([]string, error) {
	return []string{"datly"}, nil
}
func (s *fallbackCapabilitySource) Options(context.Context, string) (*cfg.MCPClient, error) {
	return &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, nil
}
func (s *fallbackCapabilitySource) Get(context.Context, string, string) (client.Interface, error) {
	return s.cli, nil
}
func (s *fallbackCapabilitySource) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	return ctx
}
func (s *fallbackCapabilitySource) UseIDToken(context.Context, string) bool { return false }

func TestCapabilityNegotiationUsesToolFallbackWhenExtensionIsAbsent(t *testing.T) {
	entry := (&skillMCPFixture{body: "---\nname: review\ndescription: Review\n---\nBody"}).entry()
	legacy := &legacySkillFixture{entry: entry}
	service := &Service{mcpSource: &fallbackCapabilitySource{cli: &fallbackCapabilityClient{legacySkillFixture: legacy}}, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	listed, err := service.ListVisible(ctx, &agentmdl.Agent{Skills: []string{"*"}})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, []string{defaultSkillListTool}, legacy.names)
}
