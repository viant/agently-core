package sdk

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

// Independently constructed contract fixture with a closed generic feed.
type publishedFeedFixture struct {
	denied bool
	gets   int
	state  primitive.ResourceState
}

func (s *publishedFeedFixture) AdmissionKey(context.Context) (string, error) {
	if s.denied {
		return "", identity.ErrResourceDenied
	}
	return "synthetic-session", nil
}
func (s *publishedFeedFixture) CurrentHash(ctx context.Context, uri string) (string, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return "", err
	}
	if uri != s.state.URI {
		return "", identity.ErrResourceDenied
	}
	return s.state.ContentFingerprint, nil
}
func (*publishedFeedFixture) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return []primitive.NamespaceCapabilities{{Namespace: "example", ProviderIdentity: "publisher", Kinds: []primitive.KindSupport{{Kind: "toolfeed", Operations: []string{"list", "get"}}}}}, nil
}
func (s *publishedFeedFixture) List(ctx context.Context, kind, namespace string) ([]primitive.ResourceState, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return nil, err
	}
	if kind != "toolfeed" || namespace != "" && namespace != "example" {
		return nil, nil
	}
	return []primitive.ResourceState{s.state}, nil
}
func (s *publishedFeedFixture) Get(ctx context.Context, uri string) (*primitive.ResourceState, error) {
	s.gets++
	if _, err := s.CurrentHash(ctx, uri); err != nil {
		return nil, err
	}
	value := s.state
	return &value, nil
}

type publishedFeedTrap struct{ calls int }

func (*publishedFeedTrap) Names(context.Context) ([]string, error) { return []string{"catalog"}, nil }
func (*publishedFeedTrap) CatalogNames(context.Context) ([]string, error) {
	return []string{"catalog"}, nil
}
func (p *publishedFeedTrap) Options(context.Context, string) (*cfg.MCPClient, error) {
	p.calls++
	return nil, errors.New("unexpected metadata options")
}

func TestPublishedToolfeedSDKUsesSharedTypedLocalLoader(t *testing.T) {
	ctx := context.Background()
	trap := &publishedFeedTrap{}
	mgr, err := manager.New(trap)
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	raw := []byte(`{"id":"review","title":"Review","match":{"service":"example","method":"read"},"ui":{"large":9007199254740993},"dataSource":{"items":{"rows":[{"value":1}]}}}`)
	source := &publishedFeedFixture{state: primitive.ResourceState{URI: "toolfeed://example/review", Kind: "toolfeed", Namespace: "example", Name: "review", Title: "Review", FormatVersion: 1, DefinitionBytes: raw, ContentFingerprint: identity.ContentFingerprint(raw)}}
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "publisher"}, []string{"example"}, source))
	consumer := &backendClient{primitiveProviders: gateway}
	first, err := consumer.GetFeedSpecContext(ctx, source.state.URI)
	require.NoError(t, err)
	require.Equal(t, "publisher", first.ProviderIdentity)
	require.Contains(t, first.UI.(map[string]any)["large"].(interface{ String() string }).String(), "9007199254740993")
	first.UI.(map[string]any)["large"] = "mutated"
	second, err := consumer.GetFeedSpecContext(ctx, source.state.URI)
	require.NoError(t, err)
	require.NotEqual(t, "mutated", second.UI.(map[string]any)["large"])
	admitted, err := consumer.admitFeedSpec(ctx, second)
	require.NoError(t, err)
	require.Equal(t, source.state.ContentFingerprint, admitted.DefinitionFingerprint)
	require.Equal(t, 1, source.gets, "warm reads reuse typed bytes while admission is checked")
	require.Zero(t, trap.calls)
	source.denied = true
	_, err = consumer.GetFeedSpecContext(ctx, source.state.URI)
	require.Error(t, err)
	_, err = consumer.admitFeedSpec(ctx, second)
	require.Error(t, err)
	require.Zero(t, trap.calls)
}
