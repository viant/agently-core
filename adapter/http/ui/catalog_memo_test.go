package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/mcp"
)

type windowCatalogConfigReader struct{ lists, loads int }

func (r *windowCatalogConfigReader) List(context.Context) ([]string, error) {
	r.lists++
	return []string{"fixture"}, nil
}
func (r *windowCatalogConfigReader) Load(context.Context, string) (*cfg.MCPClient, error) {
	r.loads++
	return &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}}, nil
}
func (*windowCatalogConfigReader) DefinitionContentGeneration(context.Context) string {
	return "revision-1"
}

type memoWindowProvider struct {
	windowAccessFixture
	manager *manager.Manager
}

func (p *memoWindowProvider) WindowDefinitionGet(ctx context.Context, input *forgeservice.WindowDefinitionGetInput) (*forgeservice.WindowDefinitionGetOutput, error) {
	for range 10 {
		if _, err := p.manager.Names(ctx); err != nil {
			return nil, err
		}
		if _, err := p.manager.Options(ctx, "fixture"); err != nil {
			return nil, err
		}
	}
	return p.windowAccessFixture.WindowDefinitionGet(ctx, input)
}

func TestWindowHTTPReusesCatalogPreparationOnlyWithinRequest(t *testing.T) {
	reader := &windowCatalogConfigReader{}
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoDefinitionReader(reader)))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	provider := &memoWindowProvider{windowAccessFixture: windowAccessFixture{allow: true}, manager: mgr}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(provider))
	for request := 1; request <= 2; request++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/customer", nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, request, reader.lists, "one catalog list per HTTP request, including nested window reads")
		require.Equal(t, request, reader.loads, "one immutable configuration preparation per request")
	}
}
