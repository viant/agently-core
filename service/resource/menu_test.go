package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const menuFixture = `{"schemaVersion":1,"applications":[{"id":"steward","title":"Workspace","icon":"applications","menus":[{"id":"advertisers","title":"Advertisers","icon":"office","action":{"type":"window","windowKey":"advertiserList","parameters":{"active":true}}},{"id":"campaigns","title":"Campaigns","icon":"timeline-events","action":{"type":"window","windowKey":"campaignList"}},{"id":"group","title":"Hidden","menus":[{"id":"secret","action":{"type":"window","windowKey":"secretWindow"}}]}]}]}`

func menuFixtureProvider(t *testing.T) (*LocalProvider, *localFixtureSource, *identity.VerifiedActor, *bool) {
	p, source, actor, revoked := localFixture(t)
	source.raw = json.RawMessage(menuFixture)
	b := p.bindings["window://team/sales"]
	b.URI = "menu://team/navigation"
	b.FormatVersion = 1
	b.Title = "Navigation"
	p.bindings = map[string]LocalResourceBinding{b.URI: b}
	p.validators = map[string]LocalResourceValidator{"menu": ValidateMenu}
	p.menuActionVisibility = func(_ context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (bool, error) {
		return !strings.Contains(string(raw), "secretWindow"), nil
	}
	return p, source, actor, revoked
}
func TestMenuProjectionNeverExposesSourceAndPreservesActions(t *testing.T) {
	p, source, _, _ := menuFixtureProvider(t)
	result, err := p.Get(context.Background(), "menu", primitive.GetRequest{URI: "menu://team/navigation"})
	require.NoError(t, err)
	require.Empty(t, result.Resource.Definition)
	require.Empty(t, result.Resource.DefinitionBytes)
	require.Equal(t, identity.ContentFingerprint(source.bytes()), result.ResolvedResource.ContentFingerprint)
	require.Equal(t, result.ResolvedResource.ContentFingerprint, result.Resource.ContentFingerprint)
	visible := string(result.MenuProjection.DefinitionBytes)
	require.NotContains(t, visible, "secretWindow")
	require.NotContains(t, visible, "Hidden")
	require.Contains(t, visible, `"windowKey":"advertiserList"`)
	require.Contains(t, visible, `"windowKey":"campaignList"`)
	require.Contains(t, visible, `"parameters":{"active":true}`)
	require.Equal(t, identity.ContentFingerprint(result.MenuProjection.DefinitionBytes), result.MenuProjection.ContentFingerprint)
	require.NotEqual(t, result.Resource.ContentFingerprint, result.MenuProjection.ContentFingerprint)
}
func TestMenuProducerDeniesRevocationDriftAndExpiry(t *testing.T) {
	for _, mode := range []string{"revocation", "drift", "expiry", "child-revocation", "late-drift"} {
		t.Run(mode, func(t *testing.T) {
			p, source, actor, revoked := menuFixtureProvider(t)
			calls := 0
			p.menuActionVisibility = func(_ context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (bool, error) {
				calls++
				switch mode {
				case "revocation":
					*revoked = true
				case "drift":
					source.raw = json.RawMessage(`{"schemaVersion":1,"applications":[]}`)
				case "expiry":
					actor.ValidUntil = time.Now().Add(-time.Second)
				case "late-drift":
					if calls > 3 {
						source.raw = json.RawMessage(`{"schemaVersion":1,"applications":[]}`)
					}
				case "child-revocation":
					if calls > 3 {
						return false, nil
					}
				}
				return !strings.Contains(string(raw), "secretWindow"), nil
			}
			got, err := p.Get(context.Background(), "menu", primitive.GetRequest{URI: "menu://team/navigation"})
			require.Error(t, err)
			require.Nil(t, got)
		})
	}
}
func TestMenuGatewayOverOrdinaryProtectedMCP(t *testing.T) {
	p, _, actor, _ := menuFixtureProvider(t)
	handler, err := NewMCPHandler(p, func(next http.Handler) http.Handler { return next })
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	defer server.Close()
	options := fixtureOptions{"producer": {ClientOptions: &mcp.ClientOptions{Name: "producer", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}}}
	mgr, err := manager.New(options)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "consumer")
	defer gateway.Close()
	rows, err := gateway.List(context.Background(), "menu", "team")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	got, err := gateway.GetMenu(context.Background(), rows[0].Connection, identity.ResourceRef{URI: rows[0].Resource.URI}, nil)
	require.NoError(t, err)
	wire, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "secretWindow")
	_, err = gateway.Get(context.Background(), rows[0].Connection, identity.ResourceRef{URI: rows[0].Resource.URI}, nil)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	p.menuActionVisibility = func(context.Context, identity.VerifiedActor, identity.ResourceURI, json.RawMessage) (bool, error) {
		return false, nil
	}
	fresh, err := gateway.GetMenu(context.Background(), rows[0].Connection, identity.ResourceRef{URI: rows[0].Resource.URI}, got.ResolvedResource)
	require.NoError(t, err)
	require.NotContains(t, string(fresh.MenuProjection.DefinitionBytes), "advertiserList")
}

type stampedMenuSource struct{ raw json.RawMessage }

func (s *stampedMenuSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.raw)}, {Kind: identity.StampedCandidate, Revision: "release-7", ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s *stampedMenuSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	return append(json.RawMessage(nil), s.raw...), nil
}

type stampMenuPolicy struct{ until time.Time }

func (p stampMenuPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	for _, c := range candidates {
		if c.Kind == identity.StampedCandidate && (ref.Revision == "" || ref.Revision == "release-7") {
			return identity.ResourceDecision{Candidate: c, AuthorityBinding: "source-menu-authority", ValidUntil: p.until}, nil
		}
	}
	return identity.ResourceDecision{}, identity.ErrResourceDenied
}
func TestMenuGenericAuthoredSourceSelectsExactStamp(t *testing.T) {
	p, _, actor, _ := menuFixtureProvider(t)
	b := p.bindings["menu://team/navigation"]
	source := &stampedMenuSource{raw: json.RawMessage(menuFixture)}
	b.Resolver = func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: p.identity, Source: source, Policy: stampMenuPolicy{until: actor.ValidUntil}}, nil
	}
	p.bindings[b.URI] = b
	for _, revision := range []string{"", "release-7"} {
		got, err := p.Get(context.Background(), "menu", primitive.GetRequest{URI: b.URI, Revision: revision})
		require.NoError(t, err)
		require.Equal(t, "release-7", got.Resource.Revision)
		require.Equal(t, identity.StampedCandidate, got.Resource.Lifecycle)
		require.Equal(t, "release-7", got.ResolvedResource.Revision)
	}
	_, err := p.Get(context.Background(), "menu", primitive.GetRequest{URI: b.URI, Revision: "release-8"})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
}
