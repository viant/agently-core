package executor_test

import (
	"context"
	"encoding/json"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	mcp "github.com/viant/mcp"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	reporting "github.com/viant/agently-core/service/reporting"
	catalog "github.com/viant/agently-core/service/reporting/catalog"
)

type customReportCatalog struct{ catalog.Provider }
type customReportPolicy struct {
	denied *bool
	until  time.Time
}

func (p customReportPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if *p.denied {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "custom-owner", ValidUntil: p.until}, nil
}

func TestBuilderPreservesExplicitCustomReportDeliveryAlongsidePrimitiveGateway(t *testing.T) {
	ctx := authsvc.InjectUser(context.Background(), "custom-owner")
	uri, _ := identity.ParseResourceURI("report://custom/local")
	denied := false
	until := time.Now().Add(time.Hour)
	spec, err := json.Marshal(validReportingIntegrationSpecPayload())
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "reportDocument": map[string]any{"title": "Custom"}, "reportSpec": json.RawMessage(spec), "dataSources": map[string]any{"demo": map[string]any{"id": "demo", "cardinality": "collection", "backend": map[string]any{"kind": "inline", "rows": []map[string]any{{"id": "owned"}}}}}})
	require.NoError(t, err)
	reader := &identity.ResourceResolver{ProviderIdentity: "custom-owner", Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}, Policy: customReportPolicy{&denied, until}}
	actor := identity.VerifiedActor{Subject: "custom-owner", Issuer: "fixture", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: until}
	nativeCatalog := &catalog.ReportCatalogService{Identity: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Resolver: func(context.Context) (*identity.ResourceResolver, error) { return reader, nil }, Inventories: []catalog.ReportInventory{catalog.ReportInventoryFunc(func(context.Context, identity.VerifiedActor) ([]catalog.ReportCatalogCandidate, error) {
		return []catalog.ReportCatalogCandidate{{URI: uri.String(), Title: "Custom"}}, nil
	})}}
	wrapped := &customReportCatalog{Provider: nativeCatalog}
	for _, configuredCatalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit-service", true: "custom-builder-catalog"}[configuredCatalog], func(t *testing.T) {
			denied = false
			calls := 0
			service := reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), Compiler: reporting.NewReportSpecCompiler(time.Now), Exporter: reporting.NewForgeExporter(nil), ReportCatalog: wrapped})
			service.SetResourceReaderFactory(func(context.Context, string) (reporting.ResourceReader, error) { return reader, nil })
			service.SetResourceDatasetExecutor(func(context.Context, identity.ResolvedResource, *dsproto.DataSource, map[string]any) (*dsproto.FetchResult, error) {
				calls++
				return &dsproto.FetchResult{Rows: []map[string]any{{"id": "owned"}}}, nil
			})
			ordinaryRequests := 0
			ordinaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ordinaryRequests++; w.WriteHeader(http.StatusBadRequest) }))
			defer ordinaryServer.Close()
			mgr, err := manager.New(reportIntegrationProvider{options: map[string]*mcpcfg.MCPClient{"ordinary-data": {ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: ordinaryServer.URL + "/mcp"}}}}}})
			require.NoError(t, err)
			builder := executor.NewBuilder().WithMCPManager(mgr).WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithPrimitiveAuthority(func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "gateway").WithReportingService(service).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}})
			if configuredCatalog {
				builder.WithReportResources(wrapped, func(context.Context, string) (*identity.ResourceResolver, error) { return reader, nil })
			}
			built, err := builder.Build(ctx)
			require.NoError(t, err)
			defer built.Close(ctx)
			require.NotNil(t, built.PrimitiveProviders)
			run, err := built.Reporting.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: uri.String()}})
			require.NoError(t, err)
			require.Equal(t, "custom-owner", run.Resource.ProviderIdentity)
			require.Equal(t, 1, calls, "caller-owned custom executor was replaced")
			before := calls
			denied = true
			_, err = built.Reporting.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: uri.String()}})
			require.ErrorIs(t, err, identity.ErrResourceDenied)
			require.Equal(t, before, calls, "local denial reached a new executor")
			require.Zero(t, ordinaryRequests, "gateway presence must not contact ordinary MCP for custom local reports or denial")
		})
	}
}
