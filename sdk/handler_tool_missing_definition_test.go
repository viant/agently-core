package sdk

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	toolregistry "github.com/viant/agently-core/internal/tool/registry"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/authz"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type missingDefinitionHTTPProvider struct {
	reader  *resources.MCPReferenceReader
	failure error
}

func (*missingDefinitionHTTPProvider) Names(context.Context) ([]string, error) { return nil, nil }
func (p *missingDefinitionHTTPProvider) Options(ctx context.Context, name string) (*cfg.MCPClient, error) {
	if p.failure != nil {
		return nil, p.failure
	}
	return p.reader.Load(ctx, name)
}

func TestToolHTTPMissingDefinitionRetainsTypedFailureWithoutDispatch(t *testing.T) {
	ctx := context.Background()
	actor, verify := resources.PublicReadActors()
	configuration, err := resources.NewLocalProvider(resources.LocalConfig{
		ProviderIdentity: "configuration", Actor: actor, Verify: verify,
		Authorize:  func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil },
		Catalog:    producer.EmptyCatalog{{Namespace: "workspace", Kinds: []primitive.KindSupport{{Kind: "mcp", FormatVersions: []int64{1}, Operations: []string{"list", "get"}}}}},
		Validators: map[string]resources.LocalResourceValidator{"mcp": resources.ValidateMCPDefinition},
	})
	require.NoError(t, err)
	provider := &missingDefinitionHTTPProvider{}
	var transportCalls atomic.Int32
	mgr, err := manager.New(provider, manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		transportCalls.Add(1)
		return nil, errors.New("unexpected remote dispatch")
	}))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	require.NoError(t, mgr.RegisterLocal(ctx, "configuration", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "configuration", ResourceCacheScopes: map[string]string{"workspace": cfg.ResourceCachePublic}}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(configuration) }))
	gateway := resources.NewGateway(mgr, actor, verify, "consumer")
	t.Cleanup(gateway.Close)
	provider.reader, err = resources.NewMCPReferenceReader(gateway, resources.MCPDefinitionSource{ConnectionName: "configuration", ProviderIdentity: "configuration", Namespace: "workspace"}, nil)
	require.NoError(t, err)
	registry, err := toolregistry.NewWithManager(mgr)
	require.NoError(t, err)
	client := &backendClient{registry: registry}
	for _, tc := range []struct {
		name    string
		failure error
		status  int
	}{
		{"reader missing", nil, http.StatusNotFound},
		{"denied", errors.Join(identity.ErrResourceDenied, errors.New("private definition detail")), http.StatusForbidden},
		{"denial precedes missing", errors.Join(identity.ErrResourceDenied, fs.ErrNotExist), http.StatusForbidden},
		{"unavailable", errors.Join(authz.ErrUnavailable, errors.New("private endpoint detail")), http.StatusServiceUnavailable},
		{"unknown failure", errors.New("private unexpected failure"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.failure = tc.failure
			request := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", strings.NewReader(`{"name":"mcp://example/hidden:artifact/discover","args":{}}`))
			response := httptest.NewRecorder()
			handleExecuteToolByName(client)(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "private")
			require.NotContains(t, response.Body.String(), "mcp://")
			require.NotContains(t, response.Body.String(), "hidden")
			require.Zero(t, transportCalls.Load())
		})
	}
}
