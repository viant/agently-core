package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	ui "github.com/viant/agently-core/adapter/http/ui"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type routingLocalCatalog struct {
	localCatalogStub
	lists, gets int
	deny        bool
}

func (c *routingLocalCatalog) List(ctx context.Context, in *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	c.lists++
	return c.localCatalogStub.List(ctx, in)
}
func (c *routingLocalCatalog) Get(_ context.Context, in *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	c.gets++
	if c.deny {
		return nil, identity.ErrResourceDenied
	}
	return &service.WindowDefinitionGetOutput{WindowID: in.WindowID, Definition: &types.Window{WindowKey: in.WindowID}}, nil
}

type routingMetadataProvider struct {
	*fixtureProvider
	rows []primitive.ResourceState
}

func (p *routingMetadataProvider) CallTool(ctx context.Context, r *schema.CallToolRequestParams, opts ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	if r.Name == "windows/list" {
		p.mu.Lock()
		p.calls[r.Name]++
		p.mu.Unlock()
		return &schema.CallToolResult{StructuredContent: map[string]any{"windows": p.rows, "complete": true}}, nil
	}
	result, err := p.fixtureProvider.CallTool(ctx, r, opts...)
	if err == nil && r.Name == "windows/get" {
		value := result.StructuredContent.(primitive.GetResult)
		uri := r.Arguments["uri"].(string)
		value.Resource.URI = uri
		value.ResolvedResource.URI = uri
		result.StructuredContent = value
	}
	return result, err
}
func routingFixture(t *testing.T) (*CompositeWindowCatalog, *routingMetadataProvider, *routingLocalCatalog) {
	t.Helper()
	p := &routingMetadataProvider{fixtureProvider: provider("studio-a"), rows: []primitive.ResourceState{{URI: "window://example/nested/sales", Kind: "window", Namespace: "example", Name: "nested/sales"}}}
	p.definition = localWindowBytes(t)
	var envelope types.WindowResourceEnvelope
	require.NoError(t, json.Unmarshal(p.definition, &envelope))
	envelope.Targets = append(envelope.Targets, types.WindowTargetBinding{Target: types.WindowTarget{Platform: "web", FormFactor: "desktop"}, Variant: envelope.Targets[0].Variant})
	p.definition, _ = json.Marshal(envelope)
	g, _, _ := fixtureGateway(t, map[string]*fixtureProvider{})
	require.NoError(t, g.Manager.RegisterLocal(context.Background(), "remote", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: p.owner}, func(context.Context) (mcpclient.Interface, error) { return p, nil }))
	c := enumerationComposite(t, g, "native-owner", "window://local/nested/sales")
	local := &routingLocalCatalog{localCatalogStub: localCatalogStub{service.WindowDefinitionSummary{WindowID: "nested/sales", ResourceURI: "window://local/nested/sales", Namespace: "local", ProviderIdentity: "native-owner"}}}
	c.Local = local
	return c, p, local
}
func TestScopedPlainWindowRoutesExactVisibleMetadataWithoutLocalShadow(t *testing.T) {
	c, p, local := routingFixture(t)
	ctx := context.Background()
	in := &service.WindowDefinitionGetInput{WindowID: "nested/sales", Namespace: "example", ProviderIdentity: "studio-a"}
	for i := 0; i < 2; i++ {
		out, err := c.Get(ctx, in)
		require.NoError(t, err)
		require.Equal(t, "window://example/nested/sales", out.Definition.Resource.URI)
		require.Equal(t, "window://example/nested/sales", out.Definition.WindowKey)
	}
	require.Zero(t, local.gets)
	require.Zero(t, local.lists)
	require.Equal(t, 1, p.calls["windows/list"], "contextual routing reuses the shared visible snapshot")
	require.Equal(t, 4, p.calls["windows/get"], "catalog metadata never replaces fresh Get")
	_, err := c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: "nested/sales"})
	require.NoError(t, err)
	require.Equal(t, 1, local.gets)
	require.Zero(t, local.lists)
	_, err = c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: "nested/sales", Namespace: "local", ProviderIdentity: "native-owner"})
	require.NoError(t, err)
	require.Equal(t, 2, local.gets)
	require.Equal(t, 1, local.lists)
	_, err = c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: "nested/sales", Namespace: "other", ProviderIdentity: "native-owner"})
	require.Error(t, err)
	require.Equal(t, 2, local.gets)
	local.deny = true
	_, err = c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: "nested/sales"})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, 4, p.calls["windows/get"])
}
func TestScopedPlainWindowRejectsMissingAndAmbiguousAliases(t *testing.T) {
	c, p, _ := routingFixture(t)
	ctx := context.Background()
	for _, in := range []*service.WindowDefinitionGetInput{{WindowID: "sale", Namespace: "example", ProviderIdentity: "studio-a"}, {WindowID: "nested/sales", Namespace: "example", ProviderIdentity: "wrong"}, {WindowID: "nested/sales", ProviderIdentity: "studio-a"}} {
		out, err := c.Get(ctx, in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	other := &routingMetadataProvider{fixtureProvider: provider("studio-b"), rows: p.rows}
	other.definition = localWindowBytes(t)
	require.NoError(t, c.Remote.Gateway.Manager.RegisterLocal(ctx, "other", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: other.owner}, func(context.Context) (mcpclient.Interface, error) { return other, nil }))
	out, err := c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: "nested/sales", Namespace: "example"})
	require.ErrorIs(t, err, ErrCollision)
	require.Nil(t, out)
	require.Zero(t, p.calls["windows/get"])
}

type routingHTTPProvider struct {
	catalog    *CompositeWindowCatalog
	parameters map[string]any
	inputs     []*service.WindowDefinitionGetInput
	emitted    string
}

func (*routingHTTPProvider) UsesWindowResourceResolution() bool { return true }
func (p *routingHTTPProvider) WindowDefinitionsList(ctx context.Context, in *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	return p.catalog.List(ctx, in)
}
func (p *routingHTTPProvider) WindowDefinitionGet(ctx context.Context, in *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	p.inputs = append(p.inputs, in)
	return p.catalog.Get(ctx, in)
}
func (p *routingHTTPProvider) AdmitWindowOpenDecision(ctx context.Context, w *types.Window, parameters map[string]any) (*service.WindowOpenDecision, error) {
	p.parameters = parameters
	return &service.WindowOpenDecision{Window: w, ValidUntil: w.Resource.ValidUntil}, nil
}
func (p *routingHTTPProvider) EmitWindowSelection(ctx context.Context, w *types.Window, _ map[string]any) (*types.Window, error) {
	ref, err := p.catalog.ResourceReference(ctx, w.WindowKey)
	if err != nil {
		return nil, err
	}
	if ref.URI != w.Resource.URI {
		return nil, identity.ErrResourceDenied
	}
	p.emitted = w.WindowKey
	return w, nil
}
func TestScopedPlainWindowHTTPUsesCanonicalChildForRevalidationAndEmission(t *testing.T) {
	c, p, local := routingFixture(t)
	bridge := &routingHTTPProvider{catalog: c}
	handler := ui.NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, ui.WithWindowResourceProvider(bridge))
	query := url.Values{"namespace": {"example"}, "providerIdentity": {"studio-a"}, "applyPermission": {"true"}, "windowParams": {`{"AdvertiserId":[990001]}`}, "platform": {"web"}, "formFactor": {"desktop"}}
	request := httptest.NewRequest(http.MethodGet, "/window/nested/sales?"+query.Encode(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, map[string]any{"AdvertiserId": []any{json.Number("990001")}}, bridge.parameters)
	require.Equal(t, "window://example/nested/sales", bridge.emitted)
	require.Zero(t, local.gets)
	require.Len(t, bridge.inputs, 1)
	require.Equal(t, "nested/sales", bridge.inputs[0].WindowID)
	require.Nil(t, bridge.inputs[0].ResolvedResource)
	require.Equal(t, 1, p.calls["windows/get"], "one managing provider Get per metadata HTTP")
	var body struct {
		Data *types.Window `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "window://example/nested/sales", body.Data.Resource.URI)
	query.Set("namespace", "missing")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/nested/sales?"+query.Encode(), nil))
	require.Equal(t, http.StatusNotFound, response.Code)
}
