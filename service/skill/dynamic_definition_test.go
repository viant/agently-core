package skill

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agent "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	proto "github.com/viant/agently-core/protocol/skill"
	format "github.com/viant/mcp-protocol/extension/skills"
	protocol "github.com/viant/mcp-protocol/server"
	client "github.com/viant/mcp/client"
	server "github.com/viant/mcp/server"
)

type publishedWireSource struct {
	wireSource
	options *cfg.MCPClient
}

type fixedNativeSkillDelivery struct{ rows []proto.Metadata }

func (d *fixedNativeSkillDelivery) List(context.Context) ([]proto.Metadata, error) {
	return d.rows, nil
}
func (d *fixedNativeSkillDelivery) ReadLocal(context.Context, *proto.Skill) (*proto.Skill, error) {
	return nil, nil
}
func (d *fixedNativeSkillDelivery) Read(context.Context, string) (*proto.Skill, bool, error) {
	return nil, false, nil
}

func TestSkillDiscoveryDeduplicatesOnlySameConnectionAndUpstreamURI(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Source skill\n---\nSource instruction."}
	source := &skillSourceFixture{f: fixture}
	delivery := &fixedNativeSkillDelivery{rows: []proto.Metadata{{Name: "review", URI: fixture.entry().Uri, ServerID: "github"}}}
	s := &Service{mcpSource: source, delivery: delivery, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agent.Agent{Skills: []string{"*"}}
	page, err := s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, fixture.entry().Uri, page[0].URI)
	delivery.rows[0].ServerID = "different-source"
	page, err = s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Len(t, page, 2, "same name and upstream URI on another source must stay distinct")
	delivery.rows[0].ServerID = "github"
	delivery.rows[0].URI = "skill://vendor/different/SKILL.md"
	page, err = s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Len(t, page, 2, "same source and name with different upstream URIs must stay distinct")
}

func (s *publishedWireSource) Options(context.Context, string) (*cfg.MCPClient, error) {
	return s.options, nil
}

func TestPublishedMCPDefinitionNegotiatesSkillsAndReadsSelectedSupport(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	compiled, err := (format.Compiler{Source: fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: procedure\ndescription: Producer procedure\n---\nRead refs/note.txt.")}, "refs/note.txt": {Data: []byte("producer support")}}}).Compile(ctx, "skill://publisher/procedure/SKILL.md")
	require.NoError(t, err)
	srv, err := server.New(server.WithNewHandler(protocol.WithDefaultHandler(ctx, func(h *protocol.DefaultHandler) error { return h.RegisterStaticSkill(compiled) })))
	require.NoError(t, err)
	wire := &skillWire{}
	wire.handler = srv.NewHandler(ctx, wire)
	cli := client.New("test", "1", wire, client.WithProtocolVersion("2026-07-28"))
	defer cli.Close()
	source := &publishedWireSource{wireSource: wireSource{client: cli}, options: &cfg.MCPClient{ResolvedIdentity: &cfg.ResolvedServiceIdentity{ServiceName: "mcp://studio/publisher", ResourceURI: "mcp://studio/publisher", ProviderIdentity: "studio"}}}
	s := &Service{mcpSource: source, registry: proto.NewRegistry()}
	a := &agent.Agent{Skills: []string{"*"}}
	page, err := s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.NotContains(t, wire.methods, "resources/read")
	require.NotContains(t, wire.methods, "skills/get")
	item, err := s.GetVisible(ctx, a, page[0].URI)
	require.NoError(t, err)
	text, _, _, err := s.readSupporting(ctx, item, "refs/note.txt")
	require.NoError(t, err)
	require.Equal(t, "producer support", text)
	count := 0
	for _, method := range wire.methods {
		if method == "initialize" || method == "server/discover" {
			count++
		}
	}
	require.Equal(t, 1, count, "capability negotiation reuses the initialized native client")
	require.Nil(t, source.options.SkillDiscovery, "default federation must not mutate the published definition")
	source.options.SkillDiscovery = &cfg.SkillDiscovery{Enabled: false}
	hidden, err := s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Empty(t, hidden)
	_, err = s.GetVisible(ctx, a, page[0].URI)
	require.Error(t, err)
	_, _, _, err = s.readSupporting(ctx, item, "refs/note.txt")
	require.Error(t, err, "explicit opt-out also applies to supporting files")
}

func TestAutomaticPublishedSkillsSkipUnsupportedCapabilityButKeepExplicitErrors(t *testing.T) {
	fixture := &skillMCPFixture{noExtension: true}
	source := &publishedWireSource{wireSource: wireSource{client: fixture}, options: &cfg.MCPClient{ResolvedIdentity: &cfg.ResolvedServiceIdentity{ResourceURI: "mcp://studio/tools"}}}
	s := &Service{mcpSource: source, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agent.Agent{Skills: []string{"*"}}
	page, err := s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Empty(t, page)
	require.Zero(t, fixture.listCalls)
	require.Zero(t, fixture.readCalls)
	source.options.SkillDiscovery = &cfg.SkillDiscovery{Enabled: true}
	_, err = s.ListVisible(ctx, a)
	require.ErrorIs(t, err, errSkillCapabilityUnavailable)
	source.options = &cfg.MCPClient{}
	page, err = s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Empty(t, page, "ordinary static connections retain explicit opt-in")
}
