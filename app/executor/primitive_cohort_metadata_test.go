package executor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
	resources "github.com/viant/forge/backend/resourcehost"
)

type cohortMetadataFixture struct{ calls int }

func (*cohortMetadataFixture) AdmissionKey(context.Context) (string, error) {
	return "synthetic-session", nil
}
func (*cohortMetadataFixture) CurrentHash(context.Context, string) (string, error) {
	return identity.ContentFingerprint([]byte(`{"schemaVersion":1}`)), nil
}
func (*cohortMetadataFixture) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: "producer", Namespace: "example"}}, nil
}
func (*cohortMetadataFixture) List(context.Context, string, string) ([]primitive.ResourceState, error) {
	return nil, nil
}
func (s *cohortMetadataFixture) Get(_ context.Context, uri string) (*primitive.ResourceState, error) {
	s.calls++
	u, err := identity.ParseResourceURI(uri)
	if err != nil {
		return nil, err
	}
	raw := []byte(`{"schemaVersion":1}`)
	return &primitive.ResourceState{URI: uri, Kind: u.Kind, Namespace: u.Namespace, Name: u.Name, DefinitionBytes: raw, ContentFingerprint: identity.ContentFingerprint(raw)}, nil
}
func TestInstallPublishedMetadataConnectsBothConsumers(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	runtime := &Runtime{PrimitiveProviders: resources.NewGateway(mgr, actor, verify, "host"), mcpDefinitions: &mcpDefinitionRuntime{gateway: resources.NewGateway(mgr, actor, verify, "configuration-host")}}
	defer runtime.PrimitiveProviders.Close()
	defer runtime.mcpDefinitions.gateway.Close()
	source := &cohortMetadataFixture{}
	connection := resources.Connection{Name: "source", ProviderIdentity: "producer"}
	require.NoError(t, runtime.InstallPublishedMetadata(connection, []string{"example"}, source))
	for _, gateway := range []*resources.Gateway{runtime.PrimitiveProviders, runtime.mcpDefinitions.gateway} {
		got, err := gateway.Get(context.Background(), connection, identity.ResourceRef{URI: "report://example/sample"}, nil)
		require.NoError(t, err)
		require.Equal(t, identity.PublishedCandidate, got.ResolvedResource.Kind)
		require.Empty(t, got.ResolvedResource.Selector())
	}
	require.Equal(t, 2, source.calls)
}
