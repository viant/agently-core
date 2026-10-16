package executor

import (
	"context"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	provider "github.com/viant/agently-core/service/resource/provider"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
	mcp "github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"

	identity "github.com/viant/agently-core/protocol/resource"
	reporting "github.com/viant/agently-core/service/reporting"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	resources "github.com/viant/agently-core/service/resource"
)

// Report delivery is assembled independently of local report storage. The
// gateway retains the provider's original pin; managing MCP owns dataset ACLs.
func (b *Builder) configurePrimitiveReports(out *Runtime) error {
	if out.Reporting == nil || out.PrimitiveProviders == nil {
		return nil
	}
	// Explicit service delivery remains caller-owned unless canonical report
	// resources were also supplied to the Builder.
	if b.reportingService != nil && b.reportCatalog == nil {
		return nil
	}
	// A custom legacy catalog has no trusted inventory contract. Preserve its
	// reader and executor; gateway presence alone says nothing about reports.
	if b.reportCatalog != nil {
		if _, known := b.reportCatalog.(*reportcatalog.ReportCatalogService); !known || b.reportResourceResolver == nil {
			return nil
		}
	}
	if b.reportResourceService == nil {
		if err := b.configureUserReports(out); err != nil {
			return err
		}
	}
	out.Reporting.SetResourceDatasetExecutor(reporting.NewResourceDatasetExecutorWithProvider(out.Registry, out.Reporting.AuthorizeResourceDataset, out.providerReportDatasource, out.ComponentDispatcher))
	catalog := &resources.ReportGatewayCatalog{Gateway: out.PrimitiveProviders, BeginMetadataRead: out.DatasourceReadSequence, Capabilities: func(ctx context.Context, _ identity.VerifiedActor, _ reportcatalog.ReportCatalogCandidate, pin identity.ResolvedResource) (reportcatalog.ReportCapabilities, error) {
		result := reportcatalog.ReportCapabilities{}
		for _, check := range []struct {
			operation string
			allowed   *bool
		}{{"report.retrieve", &result.Open}, {"report.execute", &result.Run}, {"report.export", &result.Export}} {

			*check.allowed = true
		}
		return result, nil
	}}

	if b.reportCatalog != nil && out.LocalPrimitiveProvider == nil {
		local, ok := b.reportCatalog.(*reportcatalog.ReportCatalogService)
		if !ok || b.reportResourceResolver == nil {
			return nil
		}
		owner := &primitiveReportLocalReader{local: local, resolve: b.reportResourceResolver}
		out.Reporting.SetReportCatalog(&primitiveReportLocalCatalog{local: local, remote: catalog, owner: owner})
		out.Reporting.SetResourceReaderFactory(func(ctx context.Context, operation string) (reporting.ResourceReader, error) {
			remote, err := catalog.Reader(ctx, operation)
			if err != nil {
				return nil, err
			}
			provider, ok := remote.(reporting.ProviderResourceReader)
			if !ok {
				return nil, identity.ErrResourceDenied
			}
			return &primitiveReportLocalReader{local: local, resolve: b.reportResourceResolver, remote: provider, operation: operation}, nil
		})
		return nil
	}
	out.Reporting.SetReportCatalog(catalog)
	out.Reporting.SetResourceReaderFactory(catalog.Reader)
	return nil
}

// configureUserReports registers an owning source through the same in-memory
// MCP protocol as workspace YAML. The reporting consumer receives only its
// generic gateway authoring facade; it never gets a direct store write path.
func (b *Builder) configureUserReports(out *Runtime) error {
	const name = "workspace-users"
	source, err := userprovider.NewReports(out.Reporting.Store(), name)
	if err != nil {
		return err
	}
	local, err := provider.NewLocalProvider(provider.LocalConfig{
		ProviderIdentity: name, Actor: out.PrimitiveProviders.Actor, Verify: out.PrimitiveProviders.Verify,
		Catalog: source, Authoring: source, Authorize: userprovider.Authorize,
		Validators: map[string]provider.LocalResourceValidator{"report": resources.ValidateReportEnvelope},
	})
	if err != nil {
		return err
	}
	if err = out.MCPManager.RegisterLocal(context.Background(), name, &mcpcfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: name, PrimitiveNamespaces: []string{"users"},
	}, func(context.Context) (mcpclient.Interface, error) { return provider.NewLocalMCPClient(local) }); err != nil {
		return err
	}
	writer, err := resources.NewAuthoring(out.PrimitiveProviders, name)
	if err != nil {
		return err
	}
	out.Reporting.SetReportResourceService(writer)
	return nil
}
