package executor

import (
	"context"
	"errors"

	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/policy"
	reporting "github.com/viant/agently-core/service/reporting"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/authz"
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
	out.Reporting.SetResourceDatasetExecutor(reporting.NewResourceDatasetExecutorWithProvider(out.Registry, out.Reporting.AuthorizeResourceDataset, out.providerReportDatasource, out.ComponentDispatcher))
	catalog := &resources.ReportGatewayCatalog{Gateway: out.PrimitiveProviders, BeginMetadataRead: out.DatasourceReadSequence, Capabilities: func(ctx context.Context, _ identity.VerifiedActor, _ reportcatalog.ReportCatalogCandidate, pin identity.ResolvedResource) (reportcatalog.ReportCapabilities, error) {
		result := reportcatalog.ReportCapabilities{}
		for _, check := range []struct {
			operation string
			allowed   *bool
		}{{"report.retrieve", &result.Open}, {"report.execute", &result.Run}, {"report.export", &result.Export}} {
			if out.ReportAuthorizer != nil {
				err := out.ReportAuthorizer(requestctx.WithResolvedResource(ctx, pin), check.operation, pin.URI)
				if err != nil {
					if errors.Is(err, identity.ErrResourceDenied) || errors.Is(err, authz.ErrDenied) || errors.Is(err, policy.ErrDenied) {
						continue
					}
					return reportcatalog.ReportCapabilities{}, err
				}
			}
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
