package resource

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	"testing"
)

func registerOwnedEnumerationProvider(t *testing.T, g *Gateway, name string, p *fixtureProvider) {
	t.Helper()
	require.NoError(t, g.Manager.RegisterLocal(context.Background(), name, &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: p.owner}, func(context.Context) (mcpclient.Interface, error) { return p, nil }))
}
func enumerationComposite(t *testing.T, g *Gateway, owner, uri string) *CompositeWindowCatalog {
	t.Helper()
	proof, err := types.NewWindowTargetHMAC(bytes.Repeat([]byte{3}, 32))
	require.NoError(t, err)
	return &CompositeWindowCatalog{Local: localCatalogStub{service.WindowDefinitionSummary{WindowID: "native", ResourceURI: uri, ProviderIdentity: owner}}, LocalProviderIdentity: owner, Remote: &WindowCatalog{Gateway: g, TargetProof: proof}}
}
