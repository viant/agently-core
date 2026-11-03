package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/reporting/catalog"
)

type bootstrapReportCatalog struct {
	catalog.Provider
	result *catalog.ReportCatalogResult
	err    error
	query  catalog.ReportCatalogQuery
}

func (p *bootstrapReportCatalog) List(_ context.Context, query catalog.ReportCatalogQuery) (*catalog.ReportCatalogResult, error) {
	p.query = query
	return p.result, p.err
}

func TestWorkspaceUIBootstrapIncludesPublishedReportIdentities(t *testing.T) {
	provider := &bootstrapReportCatalog{result: &catalog.ReportCatalogResult{Reports: []catalog.ReportCatalogEntry{
		{ReportCatalogCandidate: catalog.ReportCatalogCandidate{URI: "report://example/delivery", Title: "Delivery summary"}, Namespace: "example", Capabilities: catalog.ReportCapabilities{Open: true}},
		{ReportCatalogCandidate: catalog.ReportCatalogCandidate{URI: "report://another/retention", Title: "Retention analysis"}, Namespace: "another", Capabilities: catalog.ReportCapabilities{Open: true}},
	}}}
	service := &Service{uiBridge: bridge.NewService(&bridge.Config{ReportCatalog: provider})}
	summary := service.workspaceUIBootstrap(context.Background(), "")
	require.Contains(t, summary, `resourceUri=report://example/delivery`)
	require.Contains(t, summary, `title="Delivery summary"`)
	require.Contains(t, summary, `namespace=example`)
	require.Contains(t, summary, `resourceUri=report://another/retention`)
	require.Contains(t, summary, `title="Retention analysis"`)
	require.Empty(t, provider.query.Namespace, "generic intake must not select a business namespace")
	require.Positive(t, provider.query.Limit)
	provider.err = errors.New("catalog unavailable")
	require.NotContains(t, service.workspaceUIBootstrap(context.Background(), ""), "report://example/delivery")
}
