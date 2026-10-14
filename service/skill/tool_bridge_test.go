package skill

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
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
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: schema.MethodSkillsList}, {Name: schema.MethodSkillsGet}}}, nil
}

func (f *legacySkillFixture) CallTool(ctx context.Context, p *schema.CallToolRequestParams, opts ...client.RequestOption) (*schema.CallToolResult, error) {
	f.names = append(f.names, p.Name)
	f.subjects = append(f.subjects, authctx.EffectiveUserID(ctx))
	var value interface{} = map[string]interface{}{"skill": f.entry}
	if p.Name == "legacy_list" || p.Name == schema.MethodSkillsList {
		value = map[string]interface{}{"skills": []schema.Skill{f.entry}}
	}
	data, _ := json.Marshal(value)
	var object map[string]interface{}
	_ = json.Unmarshal(data, &object)
	return &schema.CallToolResult{StructuredContent: object}, nil
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

func TestCapabilityNegotiationRejectsOrdinarySkillToolsWithoutNativeExtension(t *testing.T) {
	legacy := &fallbackCapabilityClient{legacySkillFixture: &legacySkillFixture{}}
	service := &Service{mcpSource: &fallbackCapabilitySource{cli: legacy}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	_, _, _, err := service.remoteClient(ctx, "ordinary", &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}})
	require.ErrorContains(t, err, "extension not advertised")
	require.Empty(t, legacy.names, "ordinary tools must never be used as a substitute for native skills")
}
