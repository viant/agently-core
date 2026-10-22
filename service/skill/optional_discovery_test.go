package skill

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	proto "github.com/viant/agently-core/protocol/skill"
	resources "github.com/viant/agently-core/service/resource"
	mcpclient "github.com/viant/mcp/client"
	authcfg "github.com/viant/mcp/client/auth/config"
)

type optionalSkillSource struct {
	skillSourceFixture
	failure error
}

func TestSkillPageWithNativeGatewaySourceSkipsUnlinkedParticipant(t *testing.T) {
	missing := &authcfg.OAuthLinkRequiredError{ServerName: "optional", ProviderRef: "optional-oauth"}
	f := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
	options := querySkillOptions{"available": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, "optional": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}}
	linked := false
	mgr, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) {
		if name == "optional" && !linked {
			return nil, missing
		}
		return f, nil
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("skill-catalog")
	actor := identity.VerifiedActor{Issuer: "fixture", Subject: "alice", TenantID: "team", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	g := resources.NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "aggregate")
	defer g.Close()
	s := &Service{mcpSource: &resources.NativeSkillSource{Gateway: g}, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agentmdl.Agent{Skills: []string{"*"}}
	page, err := s.ListVisiblePage(ctx, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "review", page.Items[0].Name)
	require.Zero(t, f.readCalls)
	_, err = s.GetVisible(ctx, a, "skill://optional/review")
	require.ErrorIs(t, err, missing)
	linked = true
	page, err = s.ListVisiblePage(ctx, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "a partial cached page must not conceal a newly linked provider")
}

func (*optionalSkillSource) Names(context.Context) ([]string, error) {
	return []string{"optional", "available"}, nil
}
func (s *optionalSkillSource) Get(ctx context.Context, conversation, name string) (mcpclient.Interface, error) {
	if name == "optional" {
		return nil, s.failure
	}
	return s.skillSourceFixture.Get(ctx, conversation, name)
}
func TestSkillDiscoverySkipsOnlyUnlinkedOptionalProvider(t *testing.T) {
	missing := &authcfg.OAuthLinkRequiredError{ServerName: "optional", ProviderRef: "optional-oauth"}
	for _, tc := range []struct {
		name    string
		failure error
		skip    bool
	}{
		{"typed missing credential", missing, true},
		{"network", errors.New("offline"), false},
		{"text is not type", errors.New("mcp_oauth_link_required"), false},
		{"joined network", errors.Join(missing, errors.New("offline")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
			source := &optionalSkillSource{skillSourceFixture: skillSourceFixture{f: f}, failure: tc.failure}
			service := &Service{mcpSource: source, registry: proto.NewRegistry()}
			actor := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			agent := &agentmdl.Agent{Skills: []string{"*"}}
			page, err := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{})
			if !tc.skip {
				require.ErrorIs(t, err, tc.failure)
				return
			}
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, "review", page.Items[0].Name)
			require.Zero(t, f.readCalls, "metadata discovery must not read a manifest")
			_, err = service.GetVisible(actor, agent, "skill://optional/review")
			require.ErrorIs(t, err, missing, "explicit selection must retain the credential requirement")
		})
	}
}
