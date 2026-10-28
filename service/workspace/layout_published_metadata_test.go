package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	cfg "github.com/viant/mcp/client/config"
)

type layoutPublishedTrap struct{ options atomic.Int64 }

func (p *layoutPublishedTrap) Options(context.Context, string) (*cfg.MCPClient, error) {
	p.options.Add(1)
	return nil, errors.New("unexpected transport metadata")
}
func (*layoutPublishedTrap) Names(context.Context) ([]string, error) { return []string{"catalog"}, nil }

type layoutPublishedSource struct {
	raw    json.RawMessage
	hash   string
	gets   atomic.Int64
	denied bool
}

func (s *layoutPublishedSource) AdmissionKey(context.Context) (string, error) {
	if s.denied {
		return "", identity.ErrResourceDenied
	}
	return "verified-session", nil
}
func (s *layoutPublishedSource) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	if s.denied {
		return nil, identity.ErrResourceDenied
	}
	return []primitive.NamespaceCapabilities{{ProviderIdentity: "publisher", Namespace: "example", Kinds: []primitive.KindSupport{{Kind: "navigation", FormatVersions: []int64{1}, Operations: []string{"list", "get"}}}}}, nil
}
func (s *layoutPublishedSource) List(context.Context, string, string) ([]primitive.ResourceState, error) {
	if s.denied {
		return nil, identity.ErrResourceDenied
	}
	return []primitive.ResourceState{{URI: "navigation://example/layout", Kind: "navigation", Namespace: "example", Name: "layout", Title: "Example", ContentFingerprint: s.hash}}, nil
}
func (s *layoutPublishedSource) Get(_ context.Context, uri string) (*primitive.ResourceState, error) {
	if s.denied || uri != "navigation://example/layout" {
		return nil, identity.ErrResourceDenied
	}
	s.gets.Add(1)
	return &primitive.ResourceState{URI: uri, Kind: "navigation", Namespace: "example", Name: "layout", Title: "Example", FormatVersion: 1, ContentFingerprint: s.hash, DefinitionBytes: bytes.Clone(s.raw)}, nil
}
func (s *layoutPublishedSource) CurrentHash(context.Context, string) (string, error) {
	if s.denied {
		return "", identity.ErrResourceDenied
	}
	return s.hash, nil
}
func newPublishedLayoutFixture(t *testing.T) (*resources.Gateway, *layoutPublishedSource, *layoutPublishedTrap) {
	t.Helper()
	trap := &layoutPublishedTrap{}
	pool, err := manager.New(trap)
	require.NoError(t, err)
	t.Cleanup(func() { pool.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(pool, actor, verify, "host")
	t.Cleanup(gateway.Close)
	source := &layoutPublishedSource{raw: json.RawMessage(`{"schemaVersion":1,"applications":[{"id":"example","title":"Example","menus":[{"id":"open","title":"Open","action":{"type":"window","resourceUri":"window://example/view"}}]}]}`)}
	source.hash = identity.ContentFingerprint(source.raw)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "publisher"}, []string{"example"}, source, "mcp://workspace/catalog"))
	return gateway, source, trap
}
func TestGatewayLayoutMenuReaderUsesPublishedCodecAndCheapReleaseFence(t *testing.T) {
	gateway, source, trap := newPublishedLayoutFixture(t)
	ctx := context.Background()
	reader := NewGatewayLayoutMenuReader(gateway, []GatewayMenuSource{{Resource: identity.ResourceRef{URI: "navigation://example/layout"}, ProviderIdentity: "publisher"}})
	first, err := reader(ctx)
	require.NoError(t, err)
	require.Contains(t, string(first.Applications), "Example")
	require.NoError(t, first.Revalidate(ctx))
	first.Applications[0] = '!'
	second, err := reader(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":"example","title":"Example","menus":[{"id":"open","title":"Open","action":{"type":"window","resourceUri":"window://example/view","providerIdentity":"publisher"}}],"providerIdentity":"publisher","resourceNamespace":"example","resourceConnection":"catalog"}]`, string(second.Applications))
	require.EqualValues(t, 1, source.gets.Load())
	require.Zero(t, trap.options.Load())
	source.denied = true
	require.Error(t, second.Revalidate(ctx))
	_, err = reader(ctx)
	require.Error(t, err)
	require.EqualValues(t, 1, source.gets.Load())
	require.Zero(t, trap.options.Load())
}
func TestApplicationMenuReaderCanonicalConfiguredConnectionStaysLocal(t *testing.T) {
	gateway, source, trap := newPublishedLayoutFixture(t)
	reader, err := ApplicationMenuReader(gateway, []byte("applicationSources:\n  - connection: mcp://workspace/catalog\n    resource: navigation://example/layout\n    applicationId: example\n"))
	require.NoError(t, err)
	held, err := reader(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(held.Applications), "Example")
	require.Contains(t, string(held.Applications), "mcp://workspace/catalog")
	require.NoError(t, held.Revalidate(context.Background()))
	require.EqualValues(t, 1, source.gets.Load())
	require.Zero(t, trap.options.Load())
}
