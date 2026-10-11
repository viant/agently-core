package resource

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type countedReportClient struct {
	mcpclient.Interface
	mu    sync.Mutex
	calls map[string]int
}

func (c *countedReportClient) CallTool(ctx context.Context, request *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.mu.Lock()
	c.calls[request.Name]++
	c.mu.Unlock()
	return c.Interface.CallTool(ctx, request, options...)
}

func TestReportThreePageGatewayCallCounts(t *testing.T) {
	catalog, source, _, _, provider := reportGatewayFixture(t)
	descriptor := &dsproto.DataSource{ID: "rows", Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "producer", Method: "read"}, DataSource: types.DataSource{Paging: &types.PagingConfig{Enabled: true, Mode: "cursor", Parameters: &types.PagingParameters{Page: "resume"}, DataInfoSelectors: &types.DataInfoSelectors{HasMore: "more", NextCursor: "next"}}}}
	rawDescriptor, err := json.Marshal(descriptor)
	require.NoError(t, err)
	envelope := registry.ReportEnvelope{SchemaVersion: 1, ReportDocument: json.RawMessage(`{"title":"Sales"}`), ReportSpec: json.RawMessage(`{"kind":"reportSpec","version":1,"source":{"kind":"dashboard.reportBuilder","containerId":"sales","stateKey":"sales","dataSourceRef":"rows"},"title":"Sales","parameters":{"viewMode":"table","groupBy":"","pageSize":10,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"table","resultPanePosition":"bottom","blockOrder":[]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"rows","dataSourceRef":"rows","request":{}}],"blocks":[{"id":"rows-table","kind":"table","datasetId":"rows","columns":[{"field":"id","label":"ID"}]}]}`), DataSources: map[string]json.RawMessage{"rows": rawDescriptor}}
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	source.mu.Lock()
	source.raw = raw
	source.mu.Unlock()
	client, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	counter := &countedReportClient{Interface: client, calls: map[string]int{}}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	oldGateway := catalog.Gateway
	catalog.Gateway = NewGateway(mgr, oldGateway.Actor, oldGateway.Verify, "aggregate")
	t.Cleanup(catalog.Gateway.Close)
	require.NoError(t, catalog.Gateway.Manager.RegisterLocal(context.Background(), "internal", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return counter, nil }))
	service := reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), Compiler: gatewayReportCompiler(func(_ context.Context, in *reporting.CompileRequest) (*reporting.CompileResult, error) {
		return &reporting.CompileResult{ReportSpec: in.Document}, nil
	})})
	service.SetResourceReaderFactory(catalog.Reader)
	dataCalls := 0
	service.SetResourceDatasetExecutor(func(ctx context.Context, _ identity.ResolvedResource, exact *dsproto.DataSource, args map[string]interface{}) (*dsproto.FetchResult, error) {
		require.NoError(t, service.AuthorizeResourceDataset(ctx, exact, args))
		dataCalls++
		if dataCalls == 1 {
			require.NotContains(t, args, "resume")
		} else {
			require.Equal(t, map[int]string{2: "second", 3: "third"}[dataCalls], args["resume"])
		}
		result := &dsproto.FetchResult{Rows: []map[string]interface{}{{"id": dataCalls}}, DataInfo: map[string]interface{}{"more": dataCalls < 3, "next": map[int]string{1: "second", 2: "third"}[dataCalls]}}
		require.NoError(t, service.AuthorizeResourceDataset(ctx, exact, args))
		return result, nil
	})
	result, err := service.ExecuteResource(context.Background(), &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: "report://steward/sales"}})
	require.NoError(t, err)
	require.Len(t, result.Datasets["rows"].Rows, 3)
	counter.mu.Lock()
	defer counter.mu.Unlock()
	t.Logf("actual three-page run: reports/get=%d namespaces/list=%d namespaces/get=%d data=%d", counter.calls["reports/get"], counter.calls["namespaces/list"], counter.calls["namespaces/get"], dataCalls)
	require.Equal(t, 10, counter.calls["reports/get"])
	require.Equal(t, 11, counter.calls["namespaces/list"])
	require.Equal(t, 11, counter.calls["namespaces/get"])
	require.Equal(t, 3, dataCalls)
}
