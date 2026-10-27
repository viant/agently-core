package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	schema "github.com/viant/mcp-protocol/schema"
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

// Independently constructed published providers exercise automatic federation
// without application names, remote endpoints, or live service responses.
type automaticSkillSource struct {
	optionalSkillSource
	optionalClient mcpclient.Interface
	cancel         context.CancelFunc
	duplicate      bool
}

func (s *automaticSkillSource) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	uri := "mcp://example/" + name
	if s.duplicate {
		uri = "mcp://example/shared"
	}
	return &cfg.MCPClient{ResolvedIdentity: &cfg.ResolvedServiceIdentity{ResourceURI: uri}}, nil
}

func (s *automaticSkillSource) Get(ctx context.Context, conversation, name string) (mcpclient.Interface, error) {
	if name == "optional" {
		if s.cancel != nil {
			s.cancel()
		}
		if s.optionalClient != nil {
			return s.optionalClient, nil
		}
	}
	return s.optionalSkillSource.Get(ctx, conversation, name)
}

type unavailableSkillClient struct {
	*skillMCPFixture
	initializeFailure error
	listFailure       error
}

func (c *unavailableSkillClient) Initialize(ctx context.Context, opts ...mcpclient.RequestOption) (*schema.InitializeResult, error) {
	if c.initializeFailure != nil {
		return nil, c.initializeFailure
	}
	return c.skillMCPFixture.Initialize(ctx, opts...)
}

func (c *unavailableSkillClient) ListSkills(ctx context.Context, cursor *string, opts ...mcpclient.RequestOption) (*schema.ListSkillsResult, error) {
	if c.listFailure != nil {
		return nil, c.listFailure
	}
	return c.skillMCPFixture.ListSkills(ctx, cursor, opts...)
}

func TestAutomaticSkillDiscoveryIsolatesUnavailableProviders(t *testing.T) {
	failure := errors.New("synthetic provider unavailable")
	for _, stage := range []string{"connect", "initialize", "list", "provider timeout", "protocol negotiation"} {
		t.Run(stage, func(t *testing.T) {
			fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
			source := &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}, failure: failure}}
			if stage == "protocol negotiation" {
				source.failure = fmt.Errorf("automatic protocol attempts failed: latest discovery: %w; legacy initialize: %w", failure, errors.New("synthetic legacy initialize 400"))
			}
			if stage != "connect" && stage != "protocol negotiation" {
				client := &unavailableSkillClient{skillMCPFixture: fixture}
				if stage == "initialize" {
					client.initializeFailure = failure
				} else if stage == "provider timeout" {
					client.listFailure = context.DeadlineExceeded
				} else {
					client.listFailure = failure
				}
				source.optionalClient = client
			}
			service := &Service{mcpSource: source, registry: proto.NewRegistry()}
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			ctx, diagnostics := withDiscoveryDiagnostics(ctx)
			agent := &agentmdl.Agent{Skills: []string{"*"}}
			page, err := service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, "review", page.Items[0].Name)
			require.False(t, page.Complete, "unavailable providers cannot yield a complete aggregate")
			require.Len(t, diagnostics.messages, 1)
			require.Contains(t, diagnostics.messages[0], "catalog results are partial")
			require.NotContains(t, diagnostics.messages[0], failure.Error(), "upstream payloads must not enter public diagnostics")
			require.Empty(t, service.Diagnostics(), "remote failures must not contaminate the shared registry")
			require.Zero(t, fixture.readCalls)
			config, err := source.Options(ctx, "optional")
			require.NoError(t, err)
			catalog := ordinarySkillDiscovery(config).SkillDiscovery.CatalogID
			_, err = service.GetVisible(ctx, agent, "skill://"+catalog+"/review")
			if stage == "provider timeout" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, failure, "explicit source selection must surface its failure")
			}
			// A subsequent request discovers a recovered provider immediately.
			source.optionalClient = fixture
			ctx, _ = withDiscoveryDiagnostics(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}))
			page, err = service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
			require.NoError(t, err)
			require.Len(t, page.Items, 2)
			require.True(t, page.Complete)
		})
	}
}

func TestAutomaticSkillDiscoveryPreservesCancellationAndIntegrity(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		source := &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}, failure: errors.New("offline")}, cancel: cancel}
		service := &Service{mcpSource: source, registry: proto.NewRegistry()}
		_, err := service.ListVisible(ctx, agent)
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("duplicate catalog", func(t *testing.T) {
		source := &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}}, duplicate: true}
		service := &Service{mcpSource: source, registry: proto.NewRegistry()}
		_, err := service.ListVisible(ctx, agent)
		require.ErrorContains(t, err, "duplicate MCP skill catalog ID")
	})
	t.Run("joined failures", func(t *testing.T) {
		failure := fmt.Errorf("automatic protocol attempts failed: discovery: %w; initialize: %w", errors.New("offline"), identity.ErrResourceStale)
		source := &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}, failure: failure}}
		service := &Service{mcpSource: source, registry: proto.NewRegistry()}
		_, err := service.ListVisible(ctx, agent)
		require.ErrorIs(t, err, failure)
	})
	t.Run("repeated cursor", func(t *testing.T) {
		malformed := &skillMCPFixture{body: fixture.body, repeat: true, hide: true}
		source := &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}}, optionalClient: malformed}
		service := &Service{mcpSource: source, registry: proto.NewRegistry()}
		_, err := service.ListVisible(ctx, agent)
		require.ErrorContains(t, err, "repeated resource cursor")
	})
}

type retainedAutomaticSkillSource struct {
	*automaticSkillSource
	catalogs map[string]*primitive.Catalog[json.RawMessage]
	loads    int
}

func (*retainedAutomaticSkillSource) CatalogScope(context.Context, string, []string) (string, bool, error) {
	return "synthetic-caller-scope", true, nil
}
func (s *retainedAutomaticSkillSource) VisibleCatalog(ctx context.Context, key string, _ time.Time, load func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), _ ...func() time.Time) (*primitive.Catalog[json.RawMessage], error) {
	if cached := s.catalogs[key]; cached != nil {
		return cached, nil
	}
	s.loads++
	items, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if s.catalogs == nil {
		s.catalogs = map[string]*primitive.Catalog[json.RawMessage]{}
	}
	index := primitive.NewCatalog(key, items)
	s.catalogs[key] = index
	return index, nil
}
func (*retainedAutomaticSkillSource) CheckVisibleCatalog(context.Context, *primitive.Catalog[json.RawMessage]) error {
	return nil
}

func TestAutomaticSkillCatalogRetainsHealthyCacheAndRetriesPartial(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
	source := &retainedAutomaticSkillSource{automaticSkillSource: &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}, failure: errors.New("offline")}}}
	service := &Service{mcpSource: source, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	first, err := service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.False(t, first.Complete)
	source.optionalClient = fixture
	second, err := service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, second.Items, 2, "partial catalogs must not hide recovered providers")
	require.True(t, second.Complete)
	loads, lists := source.loads, fixture.listCalls
	third, err := service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Equal(t, second, third)
	require.Equal(t, loads, source.loads, "healthy automatic catalogs retain the shared cache")
	require.Equal(t, lists, fixture.listCalls)
}

func TestPartialAutomaticSkillCatalogDoesNotOfferUnresumableCursor(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
	source := &retainedAutomaticSkillSource{automaticSkillSource: &automaticSkillSource{optionalSkillSource: optionalSkillSource{skillSourceFixture: skillSourceFixture{f: fixture}, failure: errors.New("offline")}}}
	registry := proto.NewRegistry()
	registry.Add(&proto.Skill{Frontmatter: proto.Frontmatter{Name: "local", Description: "Synthetic local skill"}}, nil)
	service := &Service{mcpSource: source, registry: registry}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	ctx, diagnostics := withDiscoveryDiagnostics(ctx)
	page, err := service.ListVisiblePage(ctx, &agentmdl.Agent{Skills: []string{"*"}}, primitive.CatalogQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, 2, page.Total)
	require.False(t, page.Complete)
	require.Empty(t, page.NextCursor)
	require.Contains(t, diagnostics.messages[0], "retry discovery")
}

func TestAutomaticSkillPageWithNativeGatewayKeepsHealthyProvider(t *testing.T) {
	failure := fmt.Errorf("automatic protocol attempts failed: latest discovery: %w; legacy initialize: %w", errors.New("synthetic discovery 400"), errors.New("synthetic initialize 400"))
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Available review skill\n---\nRead carefully."}
	options := querySkillOptions{
		"available": {ResolvedIdentity: &cfg.ResolvedServiceIdentity{ResourceURI: "mcp://example/available"}},
		"optional":  {ResolvedIdentity: &cfg.ResolvedServiceIdentity{ResourceURI: "mcp://example/optional"}},
	}

	recovered := false
	mgr, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) {
		if name == "optional" && !recovered {
			return nil, failure
		}
		return fixture, nil
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("skill-catalog")
	actor := identity.VerifiedActor{Issuer: "fixture", Subject: "alice", TenantID: "team", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	g := resources.NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "aggregate")
	defer g.Close()
	service := &Service{mcpSource: &resources.NativeSkillSource{Gateway: g}, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	ctx, diagnostics := withDiscoveryDiagnostics(ctx)
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	page, err := service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.False(t, page.Complete)
	require.Len(t, diagnostics.messages, 1)
	catalog := ordinarySkillDiscovery(options["optional"]).SkillDiscovery.CatalogID
	_, err = service.GetVisible(ctx, agent, "skill://"+catalog+"/review")
	require.ErrorIs(t, err, failure)
	recovered = true
	ctx = authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	page, err = service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.True(t, page.Complete)
	calls := fixture.listCalls
	_, err = service.ListVisiblePage(ctx, agent, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Equal(t, calls, fixture.listCalls)
}

func TestOptionalDiscoveryJoinedContactClassification(t *testing.T) {
	transport := errors.New("synthetic discovery 400")
	fallback := errors.New("synthetic initialize 400")
	for _, failure := range []error{
		errors.Join(transport, fallback),
		fmt.Errorf("automatic protocol attempts failed: discovery: %w; initialize: %w", transport, fallback),
		fmt.Errorf("outer: %w", fmt.Errorf("attempts: %w; %w", transport, fallback)),
	} {
		require.True(t, optionalDiscoveryUnavailable(&discoveryUnavailable{failure}))
	}
	for _, required := range []error{identity.ErrResourceDenied, identity.ErrResourceStale, identity.ErrInvalid, context.Canceled} {
		failure := fmt.Errorf("automatic protocol attempts failed: discovery: %w; initialize: %w", transport, required)
		require.False(t, optionalDiscoveryUnavailable(&discoveryUnavailable{failure}))
	}
	require.False(t, optionalDiscoveryUnavailable(fmt.Errorf("mixed boundaries: %w; %w", &discoveryUnavailable{transport}, errors.New("unclassified failure outside contact boundary"))))
}
